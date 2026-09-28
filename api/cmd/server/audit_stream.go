package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

const (
	// Each audit entry is retried this many times before it is recorded as a
	// delivery failure and skipped.
	auditStreamMaxAttempts = 5
	auditStreamBaseBackoff = 30 * time.Second
	// A stream that keeps failing for this long is disabled.
	auditStreamDisableAfter = 24 * time.Hour
	auditStreamBatch        = 50
)

type auditStreamStatus struct {
	Webhook  *domain.Webhook              `json:"webhook"`
	Failures []domain.WebhookFailureEvent `json:"failures"`
}

type auditStreamInput struct {
	URL     *string `json:"url"`
	Secret  *string `json:"secret"`
	Enabled *bool   `json:"enabled"`
}

func auditStreamWebhook(data *domain.Bootstrap) int {
	return slices.IndexFunc(data.Webhooks, func(item domain.Webhook) bool { return item.AuditLog })
}

func (s *server) getAuditStream(w http.ResponseWriter, r *http.Request) {
	data, ok := s.store.WorkspaceMetadata(workspaceKey(r))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	status := auditStreamStatus{Failures: []domain.WebhookFailureEvent{}}
	if index := auditStreamWebhook(&data); index >= 0 {
		webhook := publicWebhook(data.Webhooks[index])
		status.Webhook = &webhook
		for _, item := range data.WebhookFailureEvents {
			if item.WebhookID == webhook.ID {
				status.Failures = append(status.Failures, item)
			}
		}
		slices.Reverse(status.Failures)
	}
	writeJSON(w, http.StatusOK, status)
}

func validAuditStreamURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return "", fmt.Errorf("%w: URL must be http or https", errInvalid)
	}
	return value, nil
}

func (s *server) createAuditStream(w http.ResponseWriter, r *http.Request) {
	var input auditStreamInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.URL == nil {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}
	target, err := validAuditStreamURL(*input.URL)
	if err != nil {
		writeError(w, http.StatusBadRequest, "URL must be http or https")
		return
	}
	secret := ""
	if input.Secret != nil {
		secret = strings.TrimSpace(*input.Secret)
	}
	if secret == "" {
		if secret, err = randomWebhookSecret(); err != nil {
			respondMutation(w, err, http.StatusInternalServerError, nil)
			return
		}
	} else if len(secret) < 16 || len(secret) > 256 {
		writeError(w, http.StatusBadRequest, "signing secret must be 16 to 256 characters")
		return
	}
	actor := requestActor(s, r)
	var created domain.Webhook
	err = s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "webhook.created", map[string]any{"auditLog": true, "url": target}, func(data *domain.Bootstrap) (string, error) {
		if auditStreamWebhook(data) >= 0 {
			return "", errConflict
		}
		now := time.Now().UTC()
		created = domain.Webhook{
			ID: fmt.Sprintf("webhook_%d", now.UnixNano()), Name: "Audit log stream", URL: target, AuditLog: true,
			ResourceTypes: []string{"audit_entries"}, TeamIDs: []string{}, TeamRestriction: "all", Enabled: true,
			CreatorID: actor.ID, SecretPrefix: secret[:min(len(secret), 12)], AuditCursorAt: &now, CreatedAt: now, UpdatedAt: now,
		}
		data.Webhooks = append(data.Webhooks, created)
		appendAudit(data, "created", "audit_log_stream", created.ID, map[string]any{"url": target})
		return created.ID, nil
	})
	if err != nil {
		if err == errConflict {
			writeError(w, http.StatusConflict, "Audit log streaming is already configured")
			return
		}
		respondMutation(w, err, http.StatusCreated, nil)
		return
	}
	if err := s.store.SetAuditStreamSecret(r.Context(), workspaceKey(r), created.ID, secret); err != nil {
		respondMutation(w, err, http.StatusCreated, nil)
		return
	}
	response := publicWebhookMap(created)
	response["secret"] = secret
	writeJSON(w, http.StatusCreated, response)
}

// updateAuditStream re-enables a stream that stopped after repeated failures.
func (s *server) updateAuditStream(w http.ResponseWriter, r *http.Request) {
	var input auditStreamInput
	if !decodeJSON(w, r, &input) {
		return
	}
	var updated domain.Webhook
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "webhook.updated", "audit_log_stream", input, func(data *domain.Bootstrap) error {
		index := auditStreamWebhook(data)
		if index < 0 {
			return errNotFound
		}
		item := &data.Webhooks[index]
		if input.URL != nil {
			target, err := validAuditStreamURL(*input.URL)
			if err != nil {
				return err
			}
			item.URL = target
		}
		if input.Enabled != nil {
			item.Enabled = *input.Enabled
			if item.Enabled {
				item.DisabledReason, item.FailingSince, item.NextAttemptAt, item.DeliveryAttempts = "", nil, nil, 0
			}
		}
		item.UpdatedAt = time.Now().UTC()
		updated = *item
		appendAudit(data, "updated", "audit_log_stream", item.ID, map[string]any{"enabled": item.Enabled})
		return nil
	})
	respondMutation(w, err, http.StatusOK, publicWebhook(updated))
}

func (s *server) deleteAuditStream(w http.ResponseWriter, r *http.Request) {
	var removedID string
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "webhook.deleted", "audit_log_stream", nil, func(data *domain.Bootstrap) error {
		index := auditStreamWebhook(data)
		if index < 0 {
			return errNotFound
		}
		removedID = data.Webhooks[index].ID
		data.Webhooks = slices.Delete(data.Webhooks, index, index+1)
		data.WebhookFailureEvents = slices.DeleteFunc(data.WebhookFailureEvents, func(item domain.WebhookFailureEvent) bool { return item.WebhookID == removedID })
		appendAudit(data, "deleted", "audit_log_stream", removedID, nil)
		return nil
	})
	if err == nil {
		err = s.store.DeleteAuditStreamSecret(r.Context(), removedID)
	}
	respondMutation(w, err, http.StatusNoContent, nil)
}

// streamAuditLog delivers audit entries newer than the stream cursor, oldest
// first. Each entry is retried with backoff; after the last attempt it is
// recorded as a failure and skipped. A stream failing for a day is disabled.
func (s *server) streamAuditLog(ctx context.Context, workspace string, now time.Time) {
	data, ok := s.store.WorkspaceMetadata(workspace)
	if !ok {
		return
	}
	index := auditStreamWebhook(&data)
	if index < 0 {
		return
	}
	webhook := data.Webhooks[index]
	if !webhook.Enabled || webhook.NextAttemptAt != nil && now.Before(*webhook.NextAttemptAt) {
		return
	}
	pending := pendingAuditEntries(data.AuditLog, webhook)
	if len(pending) == 0 {
		return
	}
	secret, err := s.store.AuditStreamSecret(ctx, webhook.ID)
	if err != nil {
		log.Printf("Audit stream secret workspace=%s: %v", workspace, err)
		return
	}
	for _, entry := range pending[:min(len(pending), auditStreamBatch)] {
		envelope := map[string]any{
			"id": entry.ID, "type": "AuditEntry", "action": "create", "data": entry,
			"organizationId": data.Workspace.ID, "createdAt": entry.CreatedAt.UTC().Format(time.RFC3339Nano), "webhookTimestamp": now.UnixMilli(),
		}
		status, detail, err := s.sendAuditEntry(ctx, webhook.URL, secret, entry.ID, envelope)
		if err == nil {
			s.recordAuditStreamProgress(workspace, webhook.ID, entry, true, now)
			continue
		}
		attempts := webhook.DeliveryAttempts + 1
		if attempts >= auditStreamMaxAttempts {
			s.recordWebhookFailure(workspace, webhook, flowWebhookEnvelope{ID: entry.ID, Type: "AuditEntry", Action: "create"}, status, detail)
			s.recordAuditStreamProgress(workspace, webhook.ID, entry, false, now)
		} else {
			s.recordAuditStreamRetry(workspace, webhook.ID, attempts, now)
		}
		return
	}
}

func pendingAuditEntries(entries []domain.AuditLogEntry, webhook domain.Webhook) []domain.AuditLogEntry {
	var pending []domain.AuditLogEntry
	for _, entry := range entries {
		if entry.ResourceType == "audit_log_stream" && entry.ResourceID == webhook.ID && entry.Action == "created" {
			continue
		}
		if webhook.AuditCursorAt != nil && (entry.CreatedAt.Before(*webhook.AuditCursorAt) || entry.CreatedAt.Equal(*webhook.AuditCursorAt) && entry.ID <= webhook.AuditCursorID) {
			continue
		}
		pending = append(pending, entry)
	}
	// The audit log is stored newest first.
	slices.SortFunc(pending, func(a, b domain.AuditLogEntry) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return pending
}

func (s *server) sendAuditEntry(ctx context.Context, target, secret, deliveryID string, envelope map[string]any) (int, string, error) {
	body, err := json.Marshal(envelope)
	if err != nil {
		return 0, err.Error(), err
	}
	if !integrationEndpointSafe(ctx, target, s.authDisabled) {
		return 0, "endpoint not allowed", errInvalid
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return 0, err.Error(), err
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Flow-Event", "AuditEntry")
	request.Header.Set("X-Flow-Delivery", deliveryID)
	request.Header.Set("X-Flow-Signature", hex.EncodeToString(mac.Sum(nil)))
	client := secureOutboundClient(10 * time.Second)
	if s.authDisabled && safeLocalDevelopmentURL(target) {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, err.Error(), err
	}
	defer response.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		text := strings.TrimSpace(string(payload))
		if text == "" {
			text = fmt.Sprintf("HTTP %d", response.StatusCode)
		}
		return response.StatusCode, text, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	return response.StatusCode, "", nil
}

// recordAuditStreamProgress moves the cursor past an entry. A delivered entry
// clears the failure state; a skipped one starts or continues it.
func (s *server) recordAuditStreamProgress(workspace, webhookID string, entry domain.AuditLogEntry, delivered bool, now time.Time) {
	s.mutateAuditStream(workspace, webhookID, func(item *domain.Webhook) {
		at := entry.CreatedAt
		item.AuditCursorAt, item.AuditCursorID, item.DeliveryAttempts, item.NextAttemptAt = &at, entry.ID, 0, nil
		if delivered {
			item.FailingSince = nil
			return
		}
		if item.FailingSince == nil {
			item.FailingSince = &now
		} else if now.Sub(*item.FailingSince) >= auditStreamDisableAfter {
			item.Enabled, item.DisabledReason = false, "deliveryFailures"
		}
	})
}

func (s *server) recordAuditStreamRetry(workspace, webhookID string, attempts int, now time.Time) {
	s.mutateAuditStream(workspace, webhookID, func(item *domain.Webhook) {
		next := now.Add(auditStreamBaseBackoff << (attempts - 1))
		item.DeliveryAttempts, item.NextAttemptAt = attempts, &next
	})
}

func (s *server) mutateAuditStream(workspace, webhookID string, apply func(*domain.Webhook)) {
	err := s.store.MutateWorkspace(context.Background(), workspace, "webhook.audit_stream_progress", webhookID, nil, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.Webhooks, func(item domain.Webhook) bool { return item.ID == webhookID && item.AuditLog })
		if index < 0 {
			return errNotFound
		}
		apply(&data.Webhooks[index])
		return nil
	})
	if err != nil {
		log.Printf("Audit stream progress workspace=%s: %v", workspace, err)
	}
}
