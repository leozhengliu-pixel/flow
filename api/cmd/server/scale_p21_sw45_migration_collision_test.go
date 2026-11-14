package main

import (
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func scaleP21SW45MigrationInputFingerprint(t *testing.T, repo *store.SQLiteStore, workspace string) [32]byte {
	t.Helper()
	digest := sha256.New()
	count := 0
	if err := repo.WalkContentRecords(t.Context(), workspace, "migration_input", func(_ string, raw json.RawMessage) error {
		count++
		_, err := digest.Write(raw)
		return err
	}); err != nil || count != 2 {
		t.Fatal("read both actual uploaded migration inputs", count, err)
	}
	var result [32]byte
	copy(result[:], digest.Sum(nil))
	return result
}

func TestScaleP21SW45MigrationMappingAggregateCollision(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		operation := scaleP21SW45MigrationMappingOperation()
		f.matrix(t, func(t *testing.T, actor domain.User, credential *domain.APIKey) {
			for _, earlier := range []string{"draft", "document"} {
				var outputs []any
				for _, bounded := range []bool{false, true} {
					if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "migrationJobs", "drafts", "documents"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
						data.MigrationJobs, data.Drafts, data.Documents = nil, nil, nil
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw45.mapping_collision", "", nil, func(data *domain.Bootstrap) error {
						operation.seed(data, actor)
						data.MigrationJobs[0].Filename = "Source\x00warm"
						data.MigrationJobs[0].Mappings[0].SourceName = "Source\x00name"
						data.Drafts = []domain.Draft{{
							ID: "sw45-mapping-draft", ContentData: map[string]any{
								"id": operation.id, "marker": "draft\x00first", "hiddenCount": 37,
								"body": strings.Repeat("aggregate body ", 22000),
							},
						}}
						data.Documents = []domain.Document{{
							ID: "sw45-mapping-document", Creator: actor, Title: "Later entity",
							ContentData: map[string]any{"id": operation.id + "-unrelated", "marker": "document-unrelated", "hiddenCount": 91},
						}}
						if earlier == "document" {
							data.Documents[0].ContentData = map[string]any{
								"id": operation.id, "marker": "document\x00first", "hiddenCount": 37,
								"body": strings.Repeat("aggregate body ", 22000),
							}
						}
						data.Webhooks = []domain.Webhook{{ID: "sw45-mapping-hook", Enabled: true, URL: "https://example.test/sw45-mapping"}}
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					installed, found := repo.WorkspaceMetadata(workspace)
					if !found || installed.MigrationJobs[0].Filename != "Source\x00warm" ||
						installed.MigrationJobs[0].Mappings[0].SourceName != "Source\x00name" {
						t.Fatal("literal fixture did not retain the required warm mapping preimage")
					}
					current := repo
					if bounded {
						if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
							t.Fatal(err)
						}
						current = f.probe
						selected, ok, err := current.WorkspaceMetadataScoped(t.Context(), workspace, store.MetadataRecordScope{Read: []store.RecordKey{{Kind: "migrationJobs", ID: operation.id}}})
						if err != nil || !ok || len(selected.MigrationJobs) != 1 || !reflect.DeepEqual(selected.MigrationJobs[0], installed.MigrationJobs[0]) {
							t.Fatal("paired phases did not receive the same installed migration", err)
						}
					}
					inputBefore := scaleP21SW45MigrationInputFingerprint(t, repo, workspace)
					var webhook domain.DomainEvent
					var realtime domain.RealtimeEvent
					current.SetWebhookSink(func(_ string, event domain.DomainEvent) { webhook = event })
					current.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { realtime = event })
					request := scaleP21M3Request(workspace, actor, credential, operation.body)
					request.SetPathValue("id", operation.id)
					response := httptest.NewRecorder()
					scaleP21M3Call(&server{store: current}, response, request, operation.handler)
					current.SetWebhookSink(nil)
					current.SetRealtimeSink(nil)
					if response.Code != http.StatusOK {
						t.Fatalf("bounded=%t status=%d body=%s", bounded, response.Code, response.Body.String())
					}
					if inputAfter := scaleP21SW45MigrationInputFingerprint(t, repo, workspace); inputAfter != inputBefore {
						t.Fatalf("bounded=%t changed or deleted migration input", bounded)
					}
					var payload struct {
						Entity struct {
							ID, Marker, Body string
							HiddenCount      int
						} `json:"entity"`
					}
					if err := json.Unmarshal(realtime.Payload, &payload); err != nil || payload.Entity.ID != operation.id ||
						payload.Entity.Marker != earlier+"\x00first" || payload.Entity.HiddenCount != 37 ||
						len(payload.Entity.Body) <= store.MetadataRecordPageMaxBytes {
						t.Fatalf("earlier=%s bounded=%t legacy earlier large nested entity changed: %v", earlier, bounded, err)
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
					t.Fatalf("earlier=%s migration mapping collision HTTP/webhook/realtime differ", earlier)
				}
			}
		})
	}
}
