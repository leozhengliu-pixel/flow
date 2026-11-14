package main

import (
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func scaleP21SW45ImportInputFingerprint(t *testing.T, repo *store.SQLiteStore, workspace string) [32]byte {
	t.Helper()
	digest := sha256.New()
	found := false
	if err := repo.WalkContentRecords(t.Context(), workspace, "import_input", func(_ string, raw json.RawMessage) error {
		found = true
		_, err := digest.Write(raw)
		return err
	}); err != nil || !found {
		t.Fatal("read actual uploaded import input", err)
	}
	var result [32]byte
	copy(result[:], digest.Sum(nil))
	return result
}

func TestScaleP21SW45ImportAggregateCollision(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		for _, operation := range scaleP21SW45ImportOperations() {
			f.matrix(t, func(t *testing.T, actor domain.User, credential *domain.APIKey) {
				var outputs []any
				for _, bounded := range []bool{false, true} {
					// Fresh inserts keep both literal phases on the same warm
					// preimage instead of rebasing an unchanged NUL field.
					if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "importJobs", "documents"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
						data.ImportJobs, data.Documents = nil, nil
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw45.collision", "", nil, func(data *domain.Bootstrap) error {
						operation.seed(data, actor)
						data.ImportJobs[0].Filename = "Before\x00after"
						data.ImportJobs[0].Headers = []string{"Title\x00header", "Owner"}
						data.ImportJobs[0].Mapping = map[string]string{"title": "Title\x00header"}
						data.Documents = []domain.Document{{
							ID: "sw45-earlier-document", Creator: actor, Title: "Earlier entity",
							ContentData: map[string]any{
								"id": operation.id, "marker": "retained\x00marker", "hiddenCount": 37,
								"body": strings.Repeat("aggregate body ", 22000),
							},
						}}
						data.Webhooks = []domain.Webhook{{ID: "sw45-hook", Enabled: true, URL: "https://example.test/sw45"}}
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					installed, found := repo.WorkspaceMetadata(workspace)
					if !found || installed.ImportJobs[0].Filename != "Before\x00after" ||
						installed.ImportJobs[0].Headers[0] != "Title\x00header" {
						t.Fatal("literal fixture did not retain the required warm preimage")
					}
					current := repo
					if bounded {
						if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
							t.Fatal(err)
						}
						current = f.probe
						selected, ok, err := current.WorkspaceMetadataScoped(t.Context(), workspace, store.MetadataRecordScope{Read: []store.RecordKey{{Kind: "importJobs", ID: operation.id}}})
						if err != nil || !ok || len(selected.ImportJobs) != 1 || !reflect.DeepEqual(selected.ImportJobs[0], installed.ImportJobs[0]) {
							t.Fatal("paired phases did not receive the exact same installed job", err)
						}
					}
					var webhook domain.DomainEvent
					var realtime domain.RealtimeEvent
					inputBefore := scaleP21SW45ImportInputFingerprint(t, repo, workspace)
					current.SetWebhookSink(func(_ string, event domain.DomainEvent) { webhook = event })
					current.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { realtime = event })
					request := scaleP21M3Request(workspace, actor, credential, operation.body)
					request.SetPathValue("id", operation.id)
					response := httptest.NewRecorder()
					scaleP21M3Call(&server{store: current}, response, request, operation.handler)
					current.SetWebhookSink(nil)
					current.SetRealtimeSink(nil)
					if response.Code != http.StatusOK {
						t.Fatalf("%s bounded=%t status=%d body=%s", operation.name, bounded, response.Code, response.Body.String())
					}
					if inputAfter := scaleP21SW45ImportInputFingerprint(t, repo, workspace); inputAfter != inputBefore {
						t.Fatalf("%s bounded=%t changed or deleted uploaded import input: before=%x after=%x", operation.name, bounded, inputBefore, inputAfter)
					}
					var payload struct {
						Entity struct {
							ID, Marker, Body string
							HiddenCount      int
						} `json:"entity"`
					}
					if err := json.Unmarshal(realtime.Payload, &payload); err != nil || payload.Entity.ID != operation.id ||
						payload.Entity.Marker != "retained\x00marker" || payload.Entity.HiddenCount != 37 ||
						len(payload.Entity.Body) <= store.MetadataRecordPageMaxBytes {
						t.Fatalf("bounded=%t legacy earlier large nested entity changed: %v", bounded, err)
					}
					outputs = append(outputs, []any{
						scaleP21M3PointJSON(t, scaleP21M3JSON(t, response.Body.Bytes())),
						scaleP21M3PointJSON(t, webhook.PreviousValues), scaleP21M3PointJSON(t, webhook.Snapshot),
						scaleP21M3PointJSON(t, realtime.Payload),
					})
					if bounded && current.WorkspaceMetadataDecodes() != 0 {
						t.Fatal("point writer decoded full metadata")
					}
				}
				if !reflect.DeepEqual(outputs[0], outputs[1]) {
					t.Fatalf("%s collision HTTP/webhook/realtime differ", operation.name)
				}
			})
		}
	}
}

func TestScaleP21SW45ImportTransitionMeasurement(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		for _, operation := range scaleP21SW45ImportOperations() {
			for _, bounded := range []bool{false, true} {
				if err := repo.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21_sw45.measure", "", nil, func(data *domain.Bootstrap) error {
					operation.seed(data, f.data.Viewer)
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				current := repo
				if bounded {
					var err error
					current, err = repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), f.data.Workspace.URLKey)
					if err != nil {
						t.Fatal(err)
					}
				}
				statements := 0
				request := scaleP21M3Request(f.data.Workspace.URLKey, f.data.Viewer, nil, operation.body)
				request.SetPathValue("id", operation.id)
				request = request.WithContext(store.WithSQLStatementObserver(request.Context(), func(string) { statements++ }))
				var before, after runtime.MemStats
				runtime.ReadMemStats(&before)
				started := time.Now()
				response := httptest.NewRecorder()
				scaleP21M3Call(&server{store: current}, response, request, operation.handler)
				elapsed := time.Since(started)
				runtime.ReadMemStats(&after)
				if response.Code != http.StatusOK {
					t.Fatalf("%s bounded=%t status=%d body=%s", operation.name, bounded, response.Code, response.Body.String())
				}
				t.Logf("%s bounded=%t allocated=%d sql=%d elapsed=%s", operation.name, bounded, after.TotalAlloc-before.TotalAlloc, statements, elapsed)
			}
		}
	}
}
