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
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW202IntegrationEnvelopeDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		view, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), f.data.Workspace.URLKey)
		if err != nil {
			t.Fatal(err)
		}
		service := &server{store: view}
		for index, input := range []struct{ payload, previous string }{
			{`{"id":"one"}`, ``},
			{`{"id":"one","previousValues":"replace"}`, `{"before":true}`},
			{`{"id":"first","id":"last","x":1e-7}`, `[null,2,"<>&\u2028"]`},
			{`null`, `{"unused":true}`},
			{`[1,2]`, `{"unused":true}`},
			{`{"id":"unterminated"`, `{"unused":true}`},
			{`{"id":"valid"}`, `{"invalid"`},
			{`{"n":1e999}`, `{"unused":true}`},
			{`{"body":"` + strings.Repeat("large supported payload ", 180000) + `"}`, `{"before":"large"}`},
		} {
			t.Run(fmt.Sprint(index), func(t *testing.T) {
				delivery := domain.IntegrationDelivery{Payload: json.RawMessage(input.payload), PreviousValues: json.RawMessage(input.previous)}
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				expected := integrationDeliveryPayload(delivery)
				payload, close, err := service.prepareBoundedIntegrationEnvelope(context.WithoutCancel(ctx), delivery)
				if err != nil || !bytes.Equal(expected, payload) {
					t.Fatal("bounded envelope differs from literal JSON handling", err)
				}
				if view.MetadataWorkUsedForTesting() == 0 {
					t.Fatal("encoded network envelope escaped ownership")
				}
				close()
				close()
				if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
					t.Fatal("network envelope leaked work or decoded metadata")
				}
			})
		}
	}
}

func scaleP21SW202Normalize(t *testing.T, value any, started, finished time.Time, endpoints map[string]string) any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	var normalize func(any)
	normalize = func(value any) {
		switch value := value.(type) {
		case map[string]any:
			for key, child := range value {
				if text, ok := child.(string); ok {
					for endpoint, replacement := range endpoints {
						text = strings.ReplaceAll(text, endpoint, replacement)
					}
					value[key] = text
					if at, err := time.Parse(time.RFC3339Nano, text); err == nil {
						if !at.Before(started) && !at.After(finished) {
							value[key] = "<generated-clock>"
						} else if key == "nextAttemptAt" {
							for shift := time.Minute; shift <= 64*time.Minute; shift *= 2 {
								if !at.Before(started.Add(shift)) && !at.After(finished.Add(shift)) {
									value[key] = "<generated-retry-clock+" + shift.String() + ">"
								}
							}
						}
					}
				}
				normalize(child)
			}
		case []any:
			for _, child := range value {
				normalize(child)
			}
		}
	}
	normalize(result)
	return result
}

func scaleP21SW202Difference(left, right any) string {
	var difference func(string, any, any) string
	difference = func(path string, left, right any) string {
		if reflect.DeepEqual(left, right) {
			return ""
		}
		if values, ok := left.(map[string]any); ok {
			if other, ok := right.(map[string]any); ok {
				keys := make([]string, 0, len(values)+len(other))
				for key := range values {
					keys = append(keys, key)
				}
				for key := range other {
					if _, found := values[key]; !found {
						keys = append(keys, key)
					}
				}
				slices.Sort(keys)
				for _, key := range keys {
					value, found := values[key]
					next, nextFound := other[key]
					if found != nextFound {
						return fmt.Sprintf("%s.%s presence literal=%t bounded=%t", path, key, found, nextFound)
					}
					if result := difference(path+"."+key, value, next); result != "" {
						return result
					}
				}
			}
		}
		if values, ok := left.([]any); ok {
			if other, ok := right.([]any); ok {
				if len(values) != len(other) {
					return fmt.Sprintf("%s length literal=%d bounded=%d", path, len(values), len(other))
				}
				for index := range values {
					if result := difference(fmt.Sprintf("%s[%d]", path, index), values[index], other[index]); result != "" {
						return result
					}
				}
			}
		}
		summary := func(value any) string {
			switch value := value.(type) {
			case map[string]any:
				return fmt.Sprintf("<map keys=%d>", len(value))
			case []any:
				return fmt.Sprintf("<array items=%d>", len(value))
			}
			if text, ok := value.(string); ok {
				if len(text) > 160 {
					return fmt.Sprintf("<string bytes=%d>", len(text))
				}
				return fmt.Sprintf("%q", text)
			}
			return fmt.Sprintf("%v", value)
		}
		return fmt.Sprintf("%s literal=%s bounded=%s", path, summary(left), summary(right))
	}
	return difference("$", left, right)
}

func scaleP21SW202Native(t *testing.T, repo *store.SQLiteStore, workspace string) (map[string]json.RawMessage, func()) {
	t.Helper()
	result := map[string]json.RawMessage{}
	if !repo.BoundedMetadataEnabled() {
		data, found := repo.WorkspaceMetadataFields(workspace, "integrationDeliveries", "integrationConnections")
		if !found {
			t.Fatal("native worker fixture missing")
		}
		for field, value := range map[string]any{
			"integrationDeliveries": data.IntegrationDeliveries, "integrationConnections": data.IntegrationConnections,
		} {
			raw, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			result[field] = raw
		}
		return result, func() {}
	}
	read, found, err := repo.BeginInstalledMetadataRead(t.Context(), workspace)
	if err != nil || !found {
		t.Fatal("native installed worker fixture", found, err)
	}
	closes := []func(){read.Close}
	close := func() {
		for index := len(closes) - 1; index >= 0; index-- {
			closes[index]()
		}
	}
	t.Cleanup(close)
	for _, field := range []string{"integrationDeliveries", "integrationConnections"} {
		work, err := repo.BeginMetadataJSONRead(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		closes = append(closes, work.Close)
		buffer := &scaleP21SW182Stream{work: work, ctx: t.Context()}
		if err := read.StreamFieldJSON(t.Context(), field, buffer); err != nil {
			t.Fatal("stream native worker occurrences", err)
		}
		result[field] = buffer.Bytes()
	}
	return result, close
}

func scaleP21SW202ProcessCPU(t *testing.T) time.Duration {
	t.Helper()
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		t.Fatal(err)
	}
	return time.Duration(usage.Utime.Nano() + usage.Stime.Nano())
}

func scaleP21SW202ReloadOracle(t *testing.T, repo *store.SQLiteStore, workspace string) {
	t.Helper()
	if repo.BoundedMetadataEnabled() {
		t.Fatal("SQL oracle must use the independent literal fixture")
	}
	before := repo.WorkspaceMetadataDecodes()
	if err := repo.ForgetInstalledMetadataRevisionForTesting(workspace); err != nil {
		t.Fatal(err)
	}
	if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	if repo.WorkspaceMetadataDecodes() <= before {
		t.Fatal("SQL oracle reused its warm native generation")
	}
}

func TestScaleP21SW202DeliveryWorkerNetworkAndCAS(t *testing.T) {
	scaleP21SW202DeliveryWorkerNetworkAndCAS(t, []string{
		"delivered", "large-delivered", "failed", "canceled-network",
		"attempts-cas", "status-cas", "connection-move", "hipaa-after-claim",
	})
}

func scaleP21SW202DeliveryWorkerNetworkAndCAS(t *testing.T, modes []string) {
	scaleP21SW202DeliveryWorker(t, modes, nil)
}

// scaleP21SW202DeliveryWorker optionally records, per leg, the wall and
// process CPU time from the held response's release to the worker's return:
// the response close plus the background retry persistence.
func scaleP21SW202DeliveryWorker(t *testing.T, modes []string, record func(bounded bool, mode string, reload bool, elapsed, cpu time.Duration)) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for driver, repo := range scaleP03ServerTestStores(t) {
		postgres := driver == "postgres" || driver == "postgresql"
		f := scaleP03Fixture(t, repo)
		workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw202-worker-")
		id, connection, replacement := prefix+"delivery", prefix+"connection", prefix+"replacement"
		at := time.Date(2026, 11, 16, 1, 2, 3, 4, time.UTC)
		secret := "sw202-fixture-signing-value"
		for _, reload := range []bool{false, true} {
			for _, mode := range modes {
				t.Run(fmt.Sprintf("reload=%t/%s", reload, mode), func(t *testing.T) {
					rawUTF8 := strings.HasPrefix(mode, "http-invalid-utf8")
					// See scaleP21SW345PostgresInlineRefusal: each leg then
					// needs its own workspace.
					refused := postgres && mode == "http-invalid-utf8-inline"
					var acceptedPayload json.RawMessage
					if rawUTF8 && !refused {
						acceptedPayload = scaleP21SW345AcceptedPayload(t, repo, f, connection)
					}
					type outcome struct {
						Result                      domain.IntegrationDelivery
						Deliveries                  []domain.IntegrationDelivery
						Connections                 []domain.IntegrationConnection
						Native, BeforePeer          map[string]json.RawMessage
						Webhooks, Realtime          []any
						Error, RequestBody          string
						Signed, WrongEndpointCalled bool
					}
					var results []any
					for _, bounded := range []bool{false, true} {
						workspace := workspace
						if refused {
							legFixture := scaleP03Fixture(t, repo)
							workspace = legFixture.data.Workspace.URLKey
							acceptedPayload = scaleP21SW345AcceptedPayload(t, repo, legFixture, connection)
						}
						var duplicateWitness *scaleP21SW345Duplicates
						repo.SetWebhookSink(nil)
						repo.SetRealtimeSink(nil)
						received := make(chan struct{}, 1)
						release := make(chan struct{})
						released := false
						releaseOutbound := func() {
							if !released {
								close(release)
								released = true
							}
						}
						requestBody, signed := "", false
						var wrongEndpoint atomic.Bool
						other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							wrongEndpoint.Store(true)
							w.WriteHeader(http.StatusNoContent)
						}))
						defer other.Close()
						destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							body, _ := io.ReadAll(r.Body)
							requestBody = string(body)
							mac := hmac.New(sha256.New, []byte(secret))
							_, _ = mac.Write([]byte(r.Header.Get("X-Flow-Timestamp") + "." + string(body)))
							signed = r.Header.Get("X-Flow-Signature-256") == "sha256="+hex.EncodeToString(mac.Sum(nil)) &&
								r.Header.Get("X-Flow-Delivery") == id && r.Header.Get("X-Flow-Event") == "issue.updated"
							received <- struct{}{}
							select {
							case <-release:
							case <-r.Context().Done():
								return
							}
							if mode == "failed" {
								w.WriteHeader(http.StatusBadGateway)
							} else {
								w.WriteHeader(http.StatusNoContent)
							}
						}))
						defer destination.Close()
						if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw202_worker_seed", "", nil, func(data *domain.Bootstrap) error {
							data.WorkspaceSettings.HIPAACompliance = false
							data.Webhooks = []domain.Webhook{{ID: prefix + "hook", Enabled: true, URL: "https://example.invalid/hook"}}
							data.Documents = []domain.Document{{ID: prefix + "shadow", ContentData: map[string]any{"id": id, "marker": "earlier aggregate"}}}
							data.IntegrationConnections = []domain.IntegrationConnection{
								{ID: connection, Provider: "fixture", Config: map[string]string{"deliveryURL": destination.URL, "deliverySecret": secret}, DeliveryAttempts: 3},
								{ID: connection, Provider: "fixture", Config: map[string]string{"deliveryURL": other.URL}, DeliveryAttempts: 71},
								{ID: replacement, Provider: "fixture", Config: map[string]string{"deliveryURL": other.URL}, DeliveryAttempts: 11},
							}
							data.IntegrationDeliveries = []domain.IntegrationDelivery{
								{ID: id, ConnectionID: connection, EventType: "issue.updated", Status: "failed", Attempts: 1,
									Payload: json.RawMessage(`{"id":"outbound","body":"exact"}`), PreviousValues: json.RawMessage(`{"status":"before"}`),
									CreatedAt: at, UpdatedAt: at, NextAttemptAt: new(at.AddDate(20, 0, 0))},
								{ID: id, ConnectionID: connection, EventType: "untouched", Status: "delivered", Attempts: 7, Payload: json.RawMessage(`{"later":true}`), CreatedAt: at, UpdatedAt: at},
							}
							if mode == "large-delivered" {
								payload, err := json.Marshal(map[string]string{"id": "outbound", "body": strings.Repeat("large supported payload ", 180000)})
								if err != nil {
									return err
								}
								data.IntegrationDeliveries[0].Payload = payload
							}
							if rawUTF8 {
								data.Documents = nil
								if mode == "http-invalid-utf8-inline" {
									duplicateWitness = scaleP21SW345DuplicateWitness(data)
								} else {
									data.IntegrationDeliveries = data.IntegrationDeliveries[:1]
									data.IntegrationConnections = []domain.IntegrationConnection{
										data.IntegrationConnections[0], data.IntegrationConnections[2],
									}
								}
								data.IntegrationDeliveries[0].Payload = bytes.Clone(acceptedPayload)
							}
							if strings.HasPrefix(mode, "near-limit-no-shadow-") {
								// This is the delivery itself as the aggregate
								// winner, without an earlier Document shadow.
								data.Documents = nil
								payload, err := json.Marshal(map[string]string{
									"id":   "outbound",
									"body": strings.Repeat("<>&", ((16<<20)-(64<<10))/18),
								})
								if err != nil {
									return err
								}
								data.IntegrationDeliveries[0].Payload = payload
								if mode == "near-limit-no-shadow-split" {
									data.IntegrationDeliveries = data.IntegrationDeliveries[:1]
									data.IntegrationConnections = []domain.IntegrationConnection{
										data.IntegrationConnections[0], data.IntegrationConnections[2],
									}
								}
								envelope := integrationDeliveryPayload(data.IntegrationDeliveries[0])
								if len(envelope) <= 15<<20 || len(envelope) >= 16<<20 {
									return fmt.Errorf("near-limit envelope witness has %d bytes", len(envelope))
								}
								t.Logf("decoded_string_bytes=%d encoded_payload_bytes=%d encoded_envelope_bytes=%d",
									3*(((16<<20)-(64<<10))/18), len(payload), len(envelope))
							}
							return nil
						}); err != nil {
							t.Fatal(err)
						}
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
							if reload && (strings.HasPrefix(mode, "near-limit-no-shadow-") || rawUTF8) {
								// The view constructor captures owner-installed
								// values; force its SQL loader only afterward.
								if err := source.ForgetInstalledMetadataRevisionForTesting(workspace); err != nil {
									t.Fatal(err)
								}
								if err := source.ReloadWorkspace(t.Context(), workspace); err != nil {
									t.Fatal("actual near-limit bounded SQL reload", err)
								}
								if source.WorkspaceMetadataDecodes() != 0 {
									t.Fatal("bounded SQL reload decoded full metadata")
								}
							}
						}
						got := outcome{}
						var nativeCloses []func()
						source.SetWebhookSink(func(_ string, event domain.DomainEvent) {
							if event.Type != "integration_delivery.claimed" && event.Type != "integration_delivery.retry" {
								return
							}
							got.Webhooks = append(got.Webhooks, []any{event.Type, event.AggregateID, event.ActorID, event.Payload, event.PreviousValues, event.Snapshot})
						})
						source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
							if event.Type != "integration_delivery.claimed" && event.Type != "integration_delivery.retry" {
								return
							}
							got.Realtime = append(got.Realtime, []any{event.Type, event.AggregateID, event.ActorID, event.Payload})
						})
						ctx, closeOutput := beginMetadataOutputReads(t.Context())
						ctx, cancel := context.WithCancel(ctx)
						service := &server{store: source, authDisabled: true}
						type callResult struct {
							value domain.IntegrationDelivery
							err   error
						}
						done := make(chan callResult, 1)
						joined := false
						started := time.Now().UTC()
						go func() {
							value, err := service.processIntegrationDelivery(ctx, workspace, id)
							done <- callResult{value, err}
						}()
						defer func() {
							cancel()
							releaseOutbound()
							if !joined {
								<-done
							}
							source.SetWebhookSink(nil)
							source.SetRealtimeSink(nil)
							for _, close := range nativeCloses {
								close()
							}
							closeOutput()
						}()
						select {
						case <-received:
						case result := <-done:
							joined = true
							if refused {
								source.SetWebhookSink(nil)
								source.SetRealtimeSink(nil)
								native, closeNative := scaleP21SW202Native(t, source, workspace)
								results = append(results, scaleP21SW345PostgresInlineRefusal(t, repo, workspace, result.err, native,
									got.Webhooks, got.Realtime, requestBody, map[string]string{destination.URL: "<captured-endpoint>", other.URL: "<other-endpoint>"}))
								closeNative()
								closeOutput()
								if bounded && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
									t.Fatal("refused worker retained work or decoded full metadata")
								}
								continue
							}
							t.Fatalf("worker returned before the held outbound call: bounded=%t mode=%s err=%v resultAttempts=%d", bounded, mode, result.err, result.value.Attempts)
						// The literal 10s network timeout starts after the
						// claim commits; it does not bound the claim phase.
						case <-ctx.Done():
							t.Fatalf("worker canceled before the held outbound call: bounded=%t mode=%s err=%v", bounded, mode, ctx.Err())
						}
						if mode == "attempts-cas" || mode == "status-cas" || mode == "connection-move" || mode == "hipaa-after-claim" {
							var closeNative func()
							got.BeforePeer, closeNative = scaleP21SW202Native(t, source, workspace)
							nativeCloses = append(nativeCloses, closeNative)
							// The peer starts from the same canonical generation
							// in both runs, after native warm state is captured.
							scaleP21SW202ReloadOracle(t, repo, workspace)
							peerCtx, peerCancel := context.WithTimeout(t.Context(), 3*time.Second)
							err := repo.MutateWorkspace(peerCtx, workspace, "scale_p21.sw202_peer", "", nil, func(data *domain.Bootstrap) error {
								t.Logf("peer input bounded=%t mode=%s status=%s attempts=%d connection=%s", bounded, mode, data.IntegrationDeliveries[0].Status, data.IntegrationDeliveries[0].Attempts, data.IntegrationDeliveries[0].ConnectionID)
								switch mode {
								case "attempts-cas":
									data.IntegrationDeliveries[0].Attempts++
								case "status-cas":
									data.IntegrationDeliveries[0].Status = "pending"
								case "connection-move":
									data.IntegrationDeliveries[0].ConnectionID = replacement
									data.IntegrationConnections[0].Config["deliveryURL"] = other.URL
								case "hipaa-after-claim":
									data.WorkspaceSettings.HIPAACompliance = true
								}
								return nil
							})
							peerCancel()
							if err != nil {
								t.Fatal("network call retained a metadata transaction/lock", err)
							}
							if bounded {
								if err := source.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
									t.Fatal(err)
								}
							}
						}
						var result callResult
						if mode == "canceled-network" {
							cancel()
							result = <-done
							joined = true
							releaseOutbound()
						} else {
							released, releasedCPU := time.Now(), scaleP21SW202ProcessCPU(t)
							releaseOutbound()
							result = <-done
							joined = true
							if record != nil {
								record(bounded, mode, reload, time.Since(released), scaleP21SW202ProcessCPU(t)-releasedCPU)
							}
						}
						cancel()
						source.SetWebhookSink(nil)
						source.SetRealtimeSink(nil)
						got.Result, got.RequestBody, got.Signed, got.WrongEndpointCalled = result.value, requestBody, signed, wrongEndpoint.Load()
						if result.err != nil {
							got.Error = result.err.Error()
						}
						var closeNative func()
						got.Native, closeNative = scaleP21SW202Native(t, source, workspace)
						nativeCloses = append(nativeCloses, closeNative)
						scaleP21SW202ReloadOracle(t, repo, workspace)
						state, _ := repo.WorkspaceMetadataFields(workspace, "integrationDeliveries", "integrationConnections")
						got.Deliveries, got.Connections = state.IntegrationDeliveries, state.IntegrationConnections
						finished := time.Now().UTC()
						if !got.Signed || got.WrongEndpointCalled {
							t.Fatal("first connection, captured endpoint or signature changed")
						}
						if mode == "attempts-cas" || mode == "status-cas" {
							if got.Error != "delivery changed while retrying" {
								t.Fatal("delivery CAS refusal changed", got.Error)
							}
						} else if result.err != nil || got.Result.Attempts != 2 {
							t.Fatalf("background completion did not persist: bounded=%t mode=%s err=%v resultAttempts=%d", bounded, mode, result.err, got.Result.Attempts)
						}
						if mode == "canceled-network" && got.Result.Status != "failed" {
							t.Fatal("cancellation prevented background failure persistence")
						}
						if mode == "large-delivered" && (len(got.Result.Payload) < 4<<20 || len(got.RequestBody) < 4<<20) {
							t.Fatal("large delivery was truncated")
						}
						if rawUTF8 {
							scaleP21SW345WorkerPayload(t, acceptedPayload, got.Result, got.Native, got.Deliveries, got.Connections, got.RequestBody, duplicateWitness)
						}
						if strings.HasPrefix(mode, "near-limit-no-shadow-") {
							if len(got.RequestBody) <= 15<<20 || len(got.RequestBody) >= 16<<20 ||
								len(got.Result.Payload) <= 15<<20 {
								t.Fatal("near-limit delivery or envelope was truncated")
							}
							if len(got.Webhooks) != 2 || len(got.Realtime) != 2 {
								t.Fatal("claim/retry publication missing")
							}
							expectedBody := strings.Repeat("<>&", ((16<<20)-(64<<10))/18)
							for _, snapshot := range got.Webhooks {
								fields := snapshot.([]any)
								var envelope struct {
									Entity struct {
										ID      string `json:"id"`
										Payload struct {
											Body string `json:"body"`
										} `json:"payload"`
									} `json:"entity"`
								}
								raw := fields[5].(json.RawMessage)
								if err := json.Unmarshal(raw, &envelope); err != nil ||
									envelope.Entity.ID != id || envelope.Entity.Payload.Body != expectedBody {
									t.Fatal("real delivery aggregate winner or complete payload changed", err)
								}
							}
						}
						normalized := scaleP21SW202Normalize(t, got, started, finished, map[string]string{
							destination.URL: "<captured-endpoint>", other.URL: "<other-endpoint>",
						})
						if rawUTF8 {
							results = append(results, []any{normalized, scaleP21SW345RawWitnesses(t, got)})
						} else {
							results = append(results, normalized)
						}
						for _, close := range nativeCloses {
							close()
						}
						if bounded && result.err == nil && source.MetadataWorkUsedForTesting() == 0 {
							t.Fatal("returned delivery escaped HTTP output ownership")
						}
						closeOutput()
						if bounded && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
							t.Fatal("worker retained work or decoded full metadata")
						}
					}
					if !reflect.DeepEqual(results[0], results[1]) {
						t.Fatal("literal/bounded delivery worker differs:", scaleP21SW202Difference(results[0], results[1]))
					}
				})
			}
		}
	}
}
