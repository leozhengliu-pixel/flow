package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func (s *server) processBoundedIntegrationDelivery(ctx context.Context, workspace, id string) (domain.IntegrationDelivery, error) {
	ctx, closeOutput := beginMetadataOutputReads(ctx)
	defer closeOutput()
	read, found, err := s.store.BeginIntegrationDeliveryRead(ctx, workspace, id)
	if err != nil {
		return domain.IntegrationDelivery{}, err
	}
	if !found {
		return domain.IntegrationDelivery{}, errNotFound
	}
	defer read.Close()
	if read.HIPAA {
		return domain.IntegrationDelivery{}, fmt.Errorf("%w: external processing is disabled by workspace policy", errConflict)
	}
	if !read.DeliveryFound {
		return domain.IntegrationDelivery{}, errNotFound
	}
	snapshot := read.Delivery
	if snapshot.Status != "pending" && snapshot.Status != "failed" {
		return domain.IntegrationDelivery{}, fmt.Errorf("%w: delivery is not retryable", errConflict)
	}
	if snapshot.Attempts >= 8 {
		return domain.IntegrationDelivery{}, fmt.Errorf("%w: delivery retry limit reached", errConflict)
	}
	if !read.ConnectionFound {
		return domain.IntegrationDelivery{}, errNotFound
	}
	connectionSnapshot := read.Connection
	endpoint := strings.TrimSpace(connectionSnapshot.Config["deliveryURL"])
	if err := integrationEndpointError(ctx, endpoint, s.authDisabled, outboundTrustedPrivate); err != nil {
		return domain.IntegrationDelivery{}, fmt.Errorf("%w: delivery URL is not allowed: %s", errInvalid, err.Error())
	}
	claimCtx, closeClaim := beginIndependentMetadataOutputReads(ctx)
	defer closeClaim()
	var payload []byte
	var closeEnvelope func()
	var envelopeWork time.Duration
	defer func() {
		if closeEnvelope != nil {
			closeEnvelope()
		}
	}()
	err = s.store.MutateIntegrationDelivery(claimCtx, workspace, id, "integration_delivery.claimed", func(current *domain.Bootstrap) error {
		i := slices.IndexFunc(current.IntegrationDeliveries, func(v domain.IntegrationDelivery) bool { return v.ID == id })
		if i < 0 {
			return errNotFound
		}
		if current.IntegrationDeliveries[i].Attempts >= 8 {
			return errConflict
		}
		if current.IntegrationDeliveries[i].Status == "delivering" {
			return errConflict
		}
		snapshot = current.IntegrationDeliveries[i]
		connectionIndex := slices.IndexFunc(current.IntegrationConnections, func(v domain.IntegrationConnection) bool { return v.ID == snapshot.ConnectionID })
		if connectionIndex < 0 {
			return errNotFound
		}
		if closeEnvelope != nil {
			closeEnvelope()
			closeEnvelope, payload = nil, nil
		}
		claimedAt := time.Now().UTC()
		startedEnvelope := time.Now()
		var err error
		payload, closeEnvelope, err = s.prepareBoundedIntegrationEnvelope(context.WithoutCancel(ctx), snapshot)
		envelopeWork = time.Since(startedEnvelope)
		if err != nil {
			return err
		}
		current.IntegrationDeliveries[i].Status = "delivering"
		current.IntegrationDeliveries[i].UpdatedAt = claimedAt
		return nil
	})
	if err != nil {
		return domain.IntegrationDelivery{}, err
	}
	// The original ten-second call context includes envelope preparation.
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second-envelopeWork)
	defer cancel()
	req, requestErr := http.NewRequestWithContext(callCtx, http.MethodPost, endpoint, strings.NewReader(string(payload)))
	if requestErr != nil {
		return domain.IntegrationDelivery{}, fmt.Errorf("%w: invalid delivery request", errInvalid)
	}
	req.Header.Set("Content-Type", "application/json")
	deliverySecret := strings.TrimSpace(connectionSnapshot.Config["deliverySecret"])
	if envName := strings.TrimSpace(connectionSnapshot.Config["deliverySecretEnv"]); strings.HasPrefix(envName, "FLOW_INTEGRATION_") {
		deliverySecret = strings.TrimSpace(os.Getenv(envName))
	}
	if deliverySecret == "" {
		for _, suffix := range []string{"DELIVERY_SECRET", "WEBHOOK_SECRET"} {
			if value := strings.TrimSpace(os.Getenv("FLOW_INTEGRATION_" + strings.ToUpper(connectionSnapshot.Provider) + "_" + suffix)); value != "" {
				deliverySecret = value
				break
			}
		}
	}
	if deliverySecret != "" {
		timestamp := fmt.Sprint(time.Now().UTC().Unix())
		envelope := timestamp + "." + string(payload)
		mac := hmac.New(sha256.New, []byte(deliverySecret))
		_, _ = mac.Write([]byte(envelope))
		req.Header.Set("X-Flow-Timestamp", timestamp)
		req.Header.Set("X-Flow-Delivery", snapshot.ID)
		req.Header.Set("X-Flow-Event", snapshot.EventType)
		req.Header.Set("X-Flow-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	if envName := connectionSnapshot.Config["deliveryTokenEnv"]; strings.HasPrefix(envName, "FLOW_INTEGRATION_") {
		if token := os.Getenv(envName); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	client := secureOutboundClient(10*time.Second, outboundTrustedPrivate)
	if s.authDisabled && safeLocalDevelopmentURL(endpoint) {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, callErr := client.Do(req)
	status, lastError := "delivered", ""
	if callErr != nil {
		// Clone the retained prefix rather than retaining a large URL/error
		// backing string after the network owner is released.
		status, lastError = "failed", strings.Clone(truncateSecurityError(callErr.Error()))
	} else {
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			status, lastError = "failed", fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
	}
	snapshot = domain.IntegrationDelivery{Attempts: snapshot.Attempts}
	connectionSnapshot = domain.IntegrationConnection{}
	payload, req, resp, endpoint, deliverySecret = nil, nil, nil, "", ""
	read.Close()
	closeEnvelope()
	closeClaim()
	var result domain.IntegrationDelivery
	var resultEncoding *store.MetadataJSONRead
	persistCtx, persistCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer persistCancel()
	// Background persistence intentionally inherits no actor, cancellation or
	// request values. Only its captured output shares the outer lifetime.
	persistCtx = store.WithMetadataOutputOwner(persistCtx, func(close func()) bool {
		return registerRequestReadClose(ctx, close)
	})
	err = s.store.MutateIntegrationDelivery(persistCtx, workspace, id, "integration_delivery.retry", func(data *domain.Bootstrap) error {
		i := slices.IndexFunc(data.IntegrationDeliveries, func(v domain.IntegrationDelivery) bool { return v.ID == id })
		if i < 0 {
			return errNotFound
		}
		d := &data.IntegrationDeliveries[i]
		if d.Attempts != snapshot.Attempts || d.Status != "delivering" {
			return errors.New("delivery changed while retrying")
		}
		connectionIndex := slices.IndexFunc(data.IntegrationConnections, func(v domain.IntegrationConnection) bool { return v.ID == d.ConnectionID })
		if connectionIndex < 0 {
			return errNotFound
		}
		connection := &data.IntegrationConnections[connectionIndex]
		d.Attempts++
		now := time.Now().UTC()
		d.UpdatedAt = now
		connection.DeliveryAttempts++
		connection.LastDeliveryAt = &now
		d.Status, d.LastError = status, lastError
		if d.Status == "failed" {
			next := now.Add(time.Duration(1<<min(d.Attempts, 6)) * time.Minute)
			d.NextAttemptAt = &next
			connection.LastError = d.LastError
		} else {
			d.NextAttemptAt = nil
			connection.LastError = ""
		}
		result = *d
		if resultEncoding != nil {
			resultEncoding.Close()
			resultEncoding = nil
		}
		output, err := s.store.BeginMetadataJSONRead(persistCtx)
		if err != nil {
			return err
		}
		if !registerRequestReadClose(ctx, output.Close) {
			output.Close()
			return store.ErrBoundedMetadataCoverage
		}
		resultEncoding = output
		if err := output.ReserveIntegrationDeliveryEncoding(persistCtx, result); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	return result, nil
}
