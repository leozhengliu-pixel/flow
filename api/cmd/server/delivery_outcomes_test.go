package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// scaleP21SW510Coordinator is a pass-through workspace lock. It counts the
// worker's lock acquisitions and runs an injected peer write on the worker's
// claim lock, before the claim callback reloads its preimage. Peer writes are
// not themselves intercepted.
type scaleP21SW510Coordinator struct {
	mu     sync.Mutex
	peer   bool
	locks  int
	before func(lock int)
}

func (c *scaleP21SW510Coordinator) WithWorkspaceLock(ctx context.Context, workspace string, apply func() error) error {
	c.mu.Lock()
	if c.peer {
		c.mu.Unlock()
		return apply()
	}
	c.locks++
	lock, before := c.locks, c.before
	c.peer = true
	c.mu.Unlock()
	if before != nil {
		before(lock)
	}
	c.mu.Lock()
	c.peer = false
	c.mu.Unlock()
	return apply()
}

type scaleP21SW510Request struct {
	Path, ContentType, Delivery, Event, Authorization string
	Timestamp, Signature                              string
	Body                                              string
}

type scaleP21SW510Case struct {
	name string
	// seed edits the common fixture: duplicate delivery and connection IDs
	// with the selected first occurrences, a Document shadow and a webhook.
	seed func(data *domain.Bootstrap, endpoint, other string)
	env  map[string]string
	// secret is the key a receiver verifies with; empty means unsigned.
	secret    string
	status    int
	workspace string
	// peerClaim runs on the claim lock, before the claim callback.
	peerClaim func(data *domain.Bootstrap)
	// peerNetwork runs while the outbound request is held.
	peerNetwork func(data *domain.Bootstrap)
}

func scaleP21SW510Cases() []scaleP21SW510Case {
	deleteDelivery := func(data *domain.Bootstrap) { data.IntegrationDeliveries = data.IntegrationDeliveries[:0] }
	promoteDuplicate := func(data *domain.Bootstrap) { data.IntegrationDeliveries = data.IntegrationDeliveries[1:] }
	deleteConnection := func(data *domain.Bootstrap) { data.IntegrationConnections = data.IntegrationConnections[2:] }
	return []scaleP21SW510Case{
		{name: "delivered-signed-token", secret: "sw510-config-value", status: http.StatusNoContent,
			env: map[string]string{"FLOW_INTEGRATION_SW510_TOKEN": "sw510-token-value"}},
		{name: "failed-retry", secret: "sw510-config-value", status: http.StatusBadGateway},
		{name: "failed-final-attempt", secret: "sw510-config-value", status: http.StatusInternalServerError,
			seed: func(data *domain.Bootstrap, _, _ string) { data.IntegrationDeliveries[0].Attempts = 7 }},
		{name: "delivered-after-final-failure-window", secret: "sw510-config-value", status: http.StatusOK,
			seed: func(data *domain.Bootstrap, _, _ string) {
				data.IntegrationDeliveries[0].Attempts, data.IntegrationDeliveries[0].Status = 7, "pending"
				data.IntegrationDeliveries[0].NextAttemptAt, data.IntegrationDeliveries[0].LastError = nil, ""
			}},
		{name: "permanent-retry-limit",
			seed: func(data *domain.Bootstrap, _, _ string) { data.IntegrationDeliveries[0].Attempts = 8 }},
		{name: "not-retryable-delivered",
			seed: func(data *domain.Bootstrap, _, _ string) { data.IntegrationDeliveries[0].Status = "delivered" }},
		{name: "not-retryable-delivering",
			seed: func(data *domain.Bootstrap, _, _ string) { data.IntegrationDeliveries[0].Status = "delivering" }},
		{name: "hipaa-disabled-processing",
			seed: func(data *domain.Bootstrap, _, _ string) { data.WorkspaceSettings.HIPAACompliance = true }},
		{name: "disabled-connection-status-still-delivers", secret: "sw510-config-value", status: http.StatusAccepted,
			seed: func(data *domain.Bootstrap, _, _ string) { data.IntegrationConnections[0].Status = "disabled" }},
		{name: "missing-workspace", workspace: "sw510-missing-workspace"},
		{name: "missing-delivery", seed: func(data *domain.Bootstrap, _, _ string) { deleteDelivery(data) }},
		{name: "deleted-target-connection", seed: func(data *domain.Bootstrap, _, _ string) { deleteConnection(data) }},
		{name: "empty-endpoint", seed: func(data *domain.Bootstrap, _, _ string) {
			data.IntegrationConnections[0].Config = map[string]string{"deliveryURL": "  "}
		}},
		{name: "unsupported-endpoint", seed: func(data *domain.Bootstrap, _, _ string) {
			data.IntegrationConnections[0].Config = map[string]string{"deliveryURL": "ftp://example.invalid/hook"}
		}},
		{name: "secret-from-named-env", secret: "sw510-named-env-value", status: http.StatusNoContent,
			env: map[string]string{"FLOW_INTEGRATION_SW510_SECRET": " sw510-named-env-value "},
			seed: func(data *domain.Bootstrap, _, _ string) {
				data.IntegrationConnections[0].Config["deliverySecretEnv"] = " FLOW_INTEGRATION_SW510_SECRET "
			}},
		{name: "secret-env-outside-namespace-ignored", secret: "sw510-config-value", status: http.StatusNoContent,
			env: map[string]string{"SW510_OUTSIDE_SECRET": "sw510-outside-value"},
			seed: func(data *domain.Bootstrap, _, _ string) {
				data.IntegrationConnections[0].Config["deliverySecretEnv"] = "SW510_OUTSIDE_SECRET"
			}},
		{name: "secret-from-provider-webhook-env", secret: "sw510-provider-value", status: http.StatusNoContent,
			env: map[string]string{"FLOW_INTEGRATION_FIXTURE_WEBHOOK_SECRET": "sw510-provider-value"},
			seed: func(data *domain.Bootstrap, _, _ string) {
				delete(data.IntegrationConnections[0].Config, "deliverySecret")
				data.IntegrationConnections[0].Config["deliverySecretEnv"] = "FLOW_INTEGRATION_SW510_UNSET"
			}},
		{name: "secret-provider-delivery-env-precedence", secret: "sw510-provider-delivery", status: http.StatusNoContent,
			env: map[string]string{
				"FLOW_INTEGRATION_FIXTURE_DELIVERY_SECRET": "sw510-provider-delivery",
				"FLOW_INTEGRATION_FIXTURE_WEBHOOK_SECRET":  "sw510-provider-webhook",
			},
			seed: func(data *domain.Bootstrap, _, _ string) {
				delete(data.IntegrationConnections[0].Config, "deliverySecret")
			}},
		{name: "unsigned-no-secret", status: http.StatusNoContent,
			seed: func(data *domain.Bootstrap, _, _ string) {
				delete(data.IntegrationConnections[0].Config, "deliverySecret")
				data.IntegrationConnections[0].Config["deliveryTokenEnv"] = "SW510_OUTSIDE_TOKEN"
			},
			env: map[string]string{"SW510_OUTSIDE_TOKEN": "sw510-outside-token"}},
		{name: "payload-without-previous-values", secret: "sw510-config-value", status: http.StatusNoContent,
			seed: func(data *domain.Bootstrap, _, _ string) { data.IntegrationDeliveries[0].PreviousValues = nil }},
		{name: "peer-claimed-before-claim",
			peerClaim: func(data *domain.Bootstrap) { data.IntegrationDeliveries[0].Status = "delivering" }},
		{name: "peer-exhausted-before-claim",
			peerClaim: func(data *domain.Bootstrap) { data.IntegrationDeliveries[0].Attempts = 8 }},
		{name: "peer-deleted-delivery-before-claim", peerClaim: deleteDelivery},
		{name: "peer-deleted-connection-before-claim", peerClaim: deleteConnection},
		{name: "peer-promoted-duplicate-before-claim", secret: "sw510-config-value", status: http.StatusNoContent,
			peerClaim: promoteDuplicate},
		{name: "peer-hipaa-before-claim", secret: "sw510-config-value", status: http.StatusNoContent,
			peerClaim: func(data *domain.Bootstrap) { data.WorkspaceSettings.HIPAACompliance = true }},
		{name: "peer-payload-before-claim", secret: "sw510-config-value", status: http.StatusNoContent,
			peerClaim: func(data *domain.Bootstrap) {
				data.IntegrationDeliveries[0].Payload = json.RawMessage(`{"id":"peer","body":"claimed bytes"}`)
				data.IntegrationDeliveries[0].EventType = "issue.peer"
			}},
		{name: "peer-deleted-delivery-during-network", secret: "sw510-config-value", status: http.StatusNoContent,
			peerNetwork: deleteDelivery},
		{name: "peer-deleted-connection-during-network", secret: "sw510-config-value", status: http.StatusNoContent,
			peerNetwork: deleteConnection},
		{name: "peer-promoted-duplicate-during-network", secret: "sw510-config-value", status: http.StatusNoContent,
			peerNetwork: promoteDuplicate},
		{name: "peer-status-during-network", secret: "sw510-config-value", status: http.StatusNoContent,
			peerNetwork: func(data *domain.Bootstrap) { data.IntegrationDeliveries[0].Status = "failed" }},
	}
}

func scaleP21SW510Seed(t *testing.T, repo *store.SQLiteStore, workspace, id, connection, replacement, endpoint, other string, test scaleP21SW510Case) {
	t.Helper()
	at := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw510_seed", "", nil, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.HIPAACompliance = false
		data.Webhooks = []domain.Webhook{{ID: id + "-hook", Enabled: true, URL: "https://example.invalid/hook"}}
		data.Documents = []domain.Document{{ID: id + "-shadow", ContentData: map[string]any{"id": id, "marker": "earlier aggregate"}}}
		data.IntegrationConnections = []domain.IntegrationConnection{
			{ID: connection, Provider: "fixture", Status: "connected", DeliveryAttempts: 3, LastError: "older", Scopes: []string{}, Channels: []string{"sw510"},
				Config: map[string]string{"deliveryURL": " " + endpoint + " ", "deliverySecret": " sw510-config-value ", "deliveryTokenEnv": "FLOW_INTEGRATION_SW510_TOKEN"}},
			{ID: connection, Provider: "fixture", Config: map[string]string{"deliveryURL": other, "deliverySecret": "duplicate"}, DeliveryAttempts: 71, Scopes: []string{}, Channels: []string{}},
			{ID: replacement, Provider: "fixture", Config: map[string]string{"deliveryURL": other}, DeliveryAttempts: 11, Scopes: []string{}, Channels: []string{}},
		}
		data.IntegrationDeliveries = []domain.IntegrationDelivery{
			{ID: id, ConnectionID: connection, EventType: "issue.updated", ResourceID: "sw510-resource", Channel: "sw510",
				Status: "failed", Attempts: 1, LastError: "HTTP 500",
				Payload: json.RawMessage(`{"id":"outbound","body":"exact <>&","n":9007199254740993}`), PreviousValues: json.RawMessage(`{"status":"before"}`),
				CreatedAt: at, UpdatedAt: at, NextAttemptAt: new(at.Add(time.Minute))},
			{ID: id, ConnectionID: replacement, EventType: "untouched", Status: "pending", Attempts: 0, Payload: json.RawMessage(`{"later":true}`), CreatedAt: at, UpdatedAt: at},
		}
		if test.seed != nil {
			test.seed(data, endpoint, other)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// The peer is the independent literal store. It first reloads the canonical
// generation, which the bounded worker's writes reached only through SQL.
func scaleP21SW510Mutate(t *testing.T, repo *store.SQLiteStore, workspace string, edit func(*domain.Bootstrap)) {
	t.Helper()
	// Runs on the destination's handler goroutine too: report, never FailNow.
	if err := repo.ForgetInstalledMetadataRevisionForTesting(workspace); err != nil {
		t.Error("peer reload", err)
		return
	}
	if err := repo.ReloadWorkspace(context.WithoutCancel(t.Context()), workspace); err != nil {
		t.Error("peer reload", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
	defer cancel()
	if err := repo.MutateWorkspace(ctx, workspace, "scale_p21.sw510_peer", "", nil, func(data *domain.Bootstrap) error {
		edit(data)
		return nil
	}); err != nil {
		t.Error("peer write", err)
	}
}

// TestScaleP21SW510DeliveryOutcomesDifferential runs the retry route against
// the literal worker and the bounded worker, warm and actually SQL-reloaded,
// with a workspace coordinator: responses, outbound requests (endpoint, body,
// HMAC/token headers), native and reloaded stored state and published events
// must be identical for every outcome.
func TestScaleP21SW510DeliveryOutcomesDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw510-")
		id, connection, replacement := prefix+"delivery", prefix+"connection", prefix+"replacement"
		for _, test := range scaleP21SW510Cases() {
			for _, reload := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/reload=%t", test.name, reload), func(t *testing.T) {
					for name, value := range test.env {
						t.Setenv(name, value)
					}
					var results []any
					for _, bounded := range []bool{false, true} {
						results = append(results, scaleP21SW510Run(t, repo, workspace, id, connection, replacement, test, reload, bounded))
					}
					if !reflect.DeepEqual(results[0], results[1]) {
						t.Fatal("literal/bounded delivery outcome differs:", scaleP21SW202Difference(results[0], results[1]))
					}
				})
			}
		}
	}
}

func scaleP21SW510Run(t *testing.T, repo *store.SQLiteStore, workspace, id, connection, replacement string, test scaleP21SW510Case, reload, bounded bool) any {
	t.Helper()
	var requests []scaleP21SW510Request
	var requestMu sync.Mutex
	var wrongEndpoint atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wrongEndpoint.Store(true)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer other.Close()
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		request := scaleP21SW510Request{
			Path: r.URL.Path, ContentType: r.Header.Get("Content-Type"), Delivery: r.Header.Get("X-Flow-Delivery"),
			Event: r.Header.Get("X-Flow-Event"), Authorization: r.Header.Get("Authorization"), Body: string(body),
		}
		if timestamp := r.Header.Get("X-Flow-Timestamp"); timestamp != "" {
			request.Timestamp = "<present>"
			mac := hmac.New(sha256.New, []byte(test.secret))
			_, _ = mac.Write([]byte(timestamp + "." + string(body)))
			request.Signature = "<invalid>"
			if r.Header.Get("X-Flow-Signature-256") == "sha256="+hex.EncodeToString(mac.Sum(nil)) {
				request.Signature = "<valid for the case secret>"
			}
		} else if r.Header.Get("X-Flow-Signature-256") != "" {
			request.Signature = "<signature without timestamp>"
		}
		requestMu.Lock()
		requests = append(requests, request)
		requestMu.Unlock()
		if test.peerNetwork != nil {
			scaleP21SW510Mutate(t, repo, workspace, test.peerNetwork)
		}
		w.WriteHeader(test.status)
	}))
	defer destination.Close()
	scaleP21SW510Seed(t, repo, workspace, id, connection, replacement, destination.URL, other.URL, test)
	if reload {
		scaleP21SW202ReloadOracle(t, repo, workspace)
	}
	source := repo
	if bounded {
		var err error
		source, err = repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		if reload {
			if err := source.ForgetInstalledMetadataRevisionForTesting(workspace); err != nil {
				t.Fatal(err)
			}
			if err := source.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal("bounded SQL reload", err)
			}
			if source.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("bounded SQL reload decoded full metadata")
			}
		}
	}
	type outcome struct {
		Status             int
		Body               any
		Requests           []scaleP21SW510Request
		WrongEndpoint      bool
		Deliveries         []domain.IntegrationDelivery
		Connections        []domain.IntegrationConnection
		Native             map[string]json.RawMessage
		Webhooks, Realtime []any
	}
	got := outcome{}
	var eventsMu sync.Mutex
	source.SetWebhookSink(func(_ string, event domain.DomainEvent) {
		if event.Type != "integration_delivery.claimed" && event.Type != "integration_delivery.retry" {
			return
		}
		eventsMu.Lock()
		got.Webhooks = append(got.Webhooks, []any{event.Type, event.AggregateID, event.ActorID, event.Payload, event.PreviousValues, event.Snapshot})
		eventsMu.Unlock()
	})
	source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
		if event.Type != "integration_delivery.claimed" && event.Type != "integration_delivery.retry" {
			return
		}
		eventsMu.Lock()
		got.Realtime = append(got.Realtime, []any{event.Type, event.AggregateID, event.ActorID, event.Payload})
		eventsMu.Unlock()
	})
	coordinator := &scaleP21SW510Coordinator{before: func(lock int) {
		if lock == 1 && test.peerClaim != nil {
			scaleP21SW510Mutate(t, repo, workspace, test.peerClaim)
		}
	}}
	source.SetWorkspaceCoordinator(coordinator)
	key := workspace
	if test.workspace != "" {
		key = test.workspace
	}
	started := time.Now().UTC()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/integration-deliveries/"+id+"/retry", nil)
	request.Header.Set("X-Workspace-Key", key)
	request.SetPathValue("id", id)
	(&server{store: source, authDisabled: true}).retryIntegrationDelivery(recorder, request)
	source.SetWorkspaceCoordinator(nil)
	source.SetWebhookSink(nil)
	source.SetRealtimeSink(nil)
	got.Status, got.Requests, got.WrongEndpoint = recorder.Code, requests, wrongEndpoint.Load()
	if err := json.Unmarshal(recorder.Body.Bytes(), &got.Body); err != nil {
		t.Fatal("response body is not JSON", err, recorder.Body.String())
	}
	native, closeNative := scaleP21SW202Native(t, source, workspace)
	got.Native = native
	scaleP21SW202ReloadOracle(t, repo, workspace)
	state, _ := repo.WorkspaceMetadataFields(workspace, "integrationDeliveries", "integrationConnections")
	got.Deliveries, got.Connections = state.IntegrationDeliveries, state.IntegrationConnections
	finished := time.Now().UTC()
	closeNative()
	if bounded && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
		t.Fatal("bounded worker retained work or decoded full metadata")
	}
	if got.WrongEndpoint {
		t.Fatal("worker called a non-first or peer-replaced endpoint")
	}
	if test.status != 0 && len(got.Requests) != 1 {
		t.Fatalf("expected one outbound request, got %d (status %d %v)", len(got.Requests), got.Status, got.Body)
	}
	if test.status == 0 && len(got.Requests) != 0 {
		t.Fatal("refused delivery reached the network")
	}
	// Lock acquisitions are not compared: with a coordinator the literal
	// writer runs a failed callback once more on reloaded records.
	t.Logf("bounded=%t status=%d requests=%d locks=%d webhooks=%d", bounded, got.Status, len(got.Requests), coordinator.locks, len(got.Webhooks))
	return scaleP21SW202Normalize(t, got, started, finished, map[string]string{
		destination.URL: "<captured-endpoint>", other.URL: "<other-endpoint>",
	})
}

// TestScaleP21SW510ConcurrentWorkers proves two workers on one delivery: the
// sequenced run holds the first worker's outbound request while the second
// worker runs to completion (exact literal refusal), and the racing run starts
// both together (one request and one conflict, in either claim order).
func TestScaleP21SW510ConcurrentWorkers(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw510-workers-")
		id, connection, replacement := prefix+"delivery", prefix+"connection", prefix+"replacement"
		for _, racing := range []bool{false, true} {
			t.Run(fmt.Sprintf("racing=%t", racing), func(t *testing.T) {
				var results []any
				for _, bounded := range []bool{false, true} {
					var requests atomic.Int32
					received := make(chan struct{}, 2)
					release := make(chan struct{})
					destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						_, _ = io.Copy(io.Discard, r.Body)
						requests.Add(1)
						received <- struct{}{}
						<-release
						w.WriteHeader(http.StatusNoContent)
					}))
					scaleP21SW510Seed(t, repo, workspace, id, connection, replacement, destination.URL, destination.URL, scaleP21SW510Case{})
					source := repo
					if bounded {
						var err error
						if source, err = repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace); err != nil {
							t.Fatal(err)
						}
					}
					var events atomic.Int32
					source.SetWebhookSink(func(_ string, event domain.DomainEvent) {
						if strings.HasPrefix(event.Type, "integration_delivery.") {
							events.Add(1)
						}
					})
					service := &server{store: source, authDisabled: true}
					type call struct {
						value domain.IntegrationDelivery
						err   error
					}
					run := func(done chan<- call) {
						value, err := service.processIntegrationDelivery(t.Context(), workspace, id)
						done <- call{value, err}
					}
					first, second := make(chan call, 1), make(chan call, 1)
					started := time.Now().UTC()
					var calls []call
					if racing {
						go run(first)
						go run(second)
						<-received
						// One of them holds the network; the other must
						// finish with a conflict without reaching it.
						var refused call
						select {
						case refused = <-first:
						case refused = <-second:
						case <-time.After(30 * time.Second):
							t.Fatal("second worker did not finish while the first held the network")
						}
						close(release)
						var delivered call
						select {
						case delivered = <-first:
						case delivered = <-second:
						}
						if !errors.Is(refused.err, errConflict) || refused.value.ID != "" {
							t.Fatal("racing worker was not refused with a conflict", refused.err)
						}
						refused.err = errConflict
						calls = []call{delivered, refused}
					} else {
						go run(first)
						<-received
						run(second)
						refused := <-second
						close(release)
						calls = []call{<-first, refused}
					}
					destination.Close()
					source.SetWebhookSink(nil)
					scaleP21SW202ReloadOracle(t, repo, workspace)
					state, _ := repo.WorkspaceMetadataFields(workspace, "integrationDeliveries", "integrationConnections")
					finished := time.Now().UTC()
					if requests.Load() != 1 || calls[0].err != nil || calls[0].value.Status != "delivered" || events.Load() != 2 {
						t.Fatal("concurrent workers did not deliver exactly once", requests.Load(), calls[0].err, events.Load())
					}
					errorsText := []string{fmt.Sprint(calls[0].err), fmt.Sprint(calls[1].err)}
					results = append(results, scaleP21SW202Normalize(t, []any{
						calls[0].value, calls[1].value, errorsText, state.IntegrationDeliveries, state.IntegrationConnections,
					}, started, finished, map[string]string{destination.URL: "<endpoint>"}))
					if bounded && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
						t.Fatal("bounded workers retained work or decoded full metadata")
					}
				}
				if !reflect.DeepEqual(results[0], results[1]) {
					t.Fatal("literal/bounded concurrent workers differ:", scaleP21SW202Difference(results[0], results[1]))
				}
			})
		}
	}
}
