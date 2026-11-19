package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func scaleP21SW345AcceptedPayload(t *testing.T, repo *store.SQLiteStore, f scaleP03PlatformFixture, connection string) json.RawMessage {
	t.Helper()
	workspace := f.data.Workspace.URLKey
	if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw345_producer_seed", "", nil, func(data *domain.Bootstrap) error {
		data.IntegrationConnections = []domain.IntegrationConnection{
			{ID: connection, Provider: "fixture", Status: "configured", Scopes: []string{"events:write"}},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	payload := append([]byte(`{"id":"outbound","body":"`), bytes.Repeat([]byte{'x'}, 100<<10)...)
	payload = append(payload, 0xff)
	payload = append(payload, []byte(`"}`)...)
	body, err := json.Marshal(struct {
		ConnectionID string          `json:"connectionId"`
		EventType    string          `json:"eventType"`
		Payload      json.RawMessage `json:"payload"`
	}{connection, "issue.updated", payload})
	if err != nil || !json.Valid(body) || utf8.Valid(body) || len(body) >= 256<<10 {
		t.Fatal("HTTP raw-payload witness changed before transport", len(body), err)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, f.host.URL+"/api/integration-deliveries", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Workspace-Key", workspace)
	response, err := f.admin.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusAccepted {
		t.Fatal("HTTP producer did not accept its json.Valid raw-payload witness", response.StatusCode, len(responseBody), err)
	}
	var created domain.IntegrationDelivery
	if err := json.Unmarshal(responseBody, &created); err != nil || created.ID == "" || created.Status != "pending" {
		t.Fatal("HTTP producer result", err, created.ID, created.Status)
	}
	if !bytes.Equal(created.Payload, payload) {
		t.Fatal("HTTP response boundary sanitized or changed the raw invalid UTF-8 payload")
	}
	checkStored := func(phase string) {
		data, found := repo.WorkspaceMetadataFields(workspace, "integrationDeliveries")
		if !found {
			t.Fatal("HTTP producer workspace disappeared", phase)
		}
		for _, delivery := range data.IntegrationDeliveries {
			if delivery.ID == created.ID {
				if !bytes.Equal(delivery.Payload, payload) || utf8.Valid(delivery.Payload) {
					t.Fatal("HTTP producer storage boundary sanitized or changed the raw invalid UTF-8 payload", phase)
				}
				return
			}
		}
		t.Fatal("HTTP producer delivery disappeared", phase)
	}
	checkStored("warm")
	scaleP21SW202ReloadOracle(t, repo, workspace)
	checkStored("actual SQL reload")
	t.Logf("HTTP producer accepted status=%d payload_bytes=%d invalid_UTF8_preserved_warm_and_SQL=true", response.StatusCode, len(payload))
	return bytes.Clone(payload)
}

type scaleP21SW345Duplicates struct {
	delivery   domain.IntegrationDelivery
	connection domain.IntegrationConnection
}

func scaleP21SW345DuplicateWitness(data *domain.Bootstrap) *scaleP21SW345Duplicates {
	delivery := &data.IntegrationDeliveries[1]
	delivery.IdempotencyKey = "later-idempotency"
	delivery.ResourceID, delivery.Channel = "later-resource", "later-channel"
	delivery.PreviousValues = json.RawMessage(`{"later":{"all":"preserved"}}`)
	delivery.LastError = "later delivery error"
	connection := &data.IntegrationConnections[1]
	connection.Name, connection.Status = "later connection", "configured"
	connection.Scopes, connection.Channels = []string{"later-scope"}, []string{"later-channel"}
	connection.LastError, connection.LastTestStatus = "later connection error", "later test"
	connection.Config["untouched"] = "complete second occurrence"
	result := &scaleP21SW345Duplicates{delivery: *delivery, connection: *connection}
	result.delivery.Payload = bytes.Clone(delivery.Payload)
	result.delivery.PreviousValues = bytes.Clone(delivery.PreviousValues)
	result.connection.Config = maps.Clone(connection.Config)
	result.connection.Scopes = slices.Clone(connection.Scopes)
	result.connection.Channels = slices.Clone(connection.Channels)
	return result
}

func scaleP21SW345WorkerPayload(t *testing.T, accepted json.RawMessage, result domain.IntegrationDelivery, native map[string]json.RawMessage, stored []domain.IntegrationDelivery, connections []domain.IntegrationConnection, requestBody string, duplicate *scaleP21SW345Duplicates) {
	t.Helper()
	deliveryCount, connectionCount := 1, 2
	if duplicate != nil {
		deliveryCount, connectionCount = 2, 3
	}
	if !bytes.Equal(result.Payload, accepted) || len(stored) != deliveryCount || !bytes.Equal(stored[0].Payload, accepted) {
		t.Fatal("worker result or actual SQL reload changed producer-accepted raw payload bytes")
	}
	var installed []domain.IntegrationDelivery
	if err := json.Unmarshal(native["integrationDeliveries"], &installed); err != nil || len(installed) != deliveryCount || !bytes.Equal(installed[0].Payload, accepted) {
		t.Fatal("installed worker state changed producer-accepted raw payload bytes", err)
	}
	var installedConnections []domain.IntegrationConnection
	if err := json.Unmarshal(native["integrationConnections"], &installedConnections); err != nil ||
		len(installedConnections) != connectionCount || len(connections) != connectionCount {
		t.Fatal("worker changed connection occurrence counts", err)
	}
	if duplicate != nil && (!reflect.DeepEqual(stored[1], duplicate.delivery) || !reflect.DeepEqual(installed[1], duplicate.delivery) ||
		!reflect.DeepEqual(connections[1], duplicate.connection) || !reflect.DeepEqual(installedConnections[1], duplicate.connection)) {
		t.Fatal("worker changed a complete second duplicate-ID occurrence")
	}
	expectedBody := integrationDeliveryPayload(domain.IntegrationDelivery{
		Payload: accepted, PreviousValues: json.RawMessage(`{"status":"before"}`),
	})
	if !bytes.Equal([]byte(requestBody), expectedBody) {
		t.Fatal("network envelope differs from the literal raw-payload handling")
	}
}

// The ordinary normalizer replaces invalid UTF-8 while decoding strings.
// Keep raw-token witnesses for outputs, including publication snapshots.
func scaleP21SW345RawWitnesses(t *testing.T, value any) []string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result []string
	var visit func(json.RawMessage, string)
	visit = func(raw json.RawMessage, path string) {
		switch raw[0] {
		case '{':
			var object map[string]json.RawMessage
			if err := json.Unmarshal(raw, &object); err != nil {
				t.Fatal(err)
			}
			for key, child := range object {
				visit(child, path+"."+key)
			}
		case '[':
			var array []json.RawMessage
			if err := json.Unmarshal(raw, &array); err != nil {
				t.Fatal(err)
			}
			for index, child := range array {
				visit(child, fmt.Sprintf("%s[%d]", path, index))
			}
		case '"':
			if !utf8.Valid(raw) {
				result = append(result, fmt.Sprintf("%s:%x", path, sha256.Sum256(raw)))
			}
		}
	}
	visit(raw, "$")
	slices.Sort(result)
	if len(result) == 0 {
		t.Fatal("worker comparison lost its accepted raw-byte witness")
	}
	return result
}

// scaleP21SW345PostgresInlineRefusal witnesses, on PostgreSQL, a limit of the
// literal writer (unchanged from main) that the bounded worker must share
// exactly. The workspace root is BYTEA and the seed write stores an inline
// collection's raw invalid UTF-8 payload bytes, but metadataFieldRecordBacked
// probes the stored root's collection shapes with
// convert_from(data,'UTF8')::jsonb, which PostgreSQL refuses (SQLSTATE
// 22021). Every later write that rebases a field without a record row (the
// inline deliveries here) fails: the claim is refused before any network call
// and nothing is committed, published or installed, and the workspace stays
// refused for later writes in both modes. SQLite and MySQL read those bytes,
// so there the worker delivers as in the split shape.
func scaleP21SW345PostgresInlineRefusal(t *testing.T, repo *store.SQLiteStore, workspace string, err error, native map[string]json.RawMessage, webhooks, realtime []any, requestBody string, endpoints map[string]string) any {
	t.Helper()
	const refusal = "SQLSTATE 22021"
	if err == nil || !strings.Contains(err.Error(), refusal) || requestBody != "" || len(webhooks) != 0 || len(realtime) != 0 {
		t.Fatal("PostgreSQL inline raw-UTF-8 claim was not refused before the network call", err, len(requestBody), len(webhooks), len(realtime))
	}
	warm, _ := repo.WorkspaceMetadataFields(workspace, "integrationDeliveries", "integrationConnections")
	scaleP21SW202ReloadOracle(t, repo, workspace)
	state, _ := repo.WorkspaceMetadataFields(workspace, "integrationDeliveries", "integrationConnections")
	// Warm and SQL-loaded connections differ only in nil versus empty
	// collections; their delivery counters are the claim's only writes.
	counters := func(values []domain.IntegrationConnection) []string {
		var result []string
		for _, value := range values {
			result = append(result, fmt.Sprint(value.ID, value.DeliveryAttempts, value.LastDeliveryAt, value.LastError))
		}
		return result
	}
	if len(state.IntegrationDeliveries) != 2 || state.IntegrationDeliveries[0].Status != "failed" || state.IntegrationDeliveries[0].Attempts != 1 ||
		!reflect.DeepEqual(warm.IntegrationDeliveries, state.IntegrationDeliveries) ||
		!slices.Equal(counters(warm.IntegrationConnections), counters(state.IntegrationConnections)) {
		t.Fatal("refused claim committed or installed a change")
	}
	probe := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw345_refusal_probe", "", nil, func(data *domain.Bootstrap) error {
		data.IntegrationConnections[0].Name = "later write"
		return nil
	})
	if probe == nil || !strings.Contains(probe.Error(), refusal) {
		t.Fatal("the literal PostgreSQL refusal is no longer persistent; restate this witness", probe)
	}
	outcome := []any{err.Error(), probe.Error(), native, state.IntegrationDeliveries, state.IntegrationConnections}
	return []any{scaleP21SW202Normalize(t, outcome, time.Time{}, time.Time{}, endpoints), scaleP21SW345RawWitnesses(t, outcome)}
}

func TestScaleP21SW345IntegrationHTTPRawUTF8(t *testing.T) {
	scaleP21SW202DeliveryWorkerNetworkAndCAS(t, []string{"http-invalid-utf8-split", "http-invalid-utf8-inline"})
}
