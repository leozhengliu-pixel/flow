package main

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Reuse the established complete principal/key, subject, attribution and
// revocation matrix against the newly integrated logical/aggregate foundation.
func TestScaleP21SW42DeferredAuditsMatrix(t *testing.T) {
	TestScaleP21SW7DeferredAuditsDifferential(t)
}

func TestScaleP21SW42DeferredAuditAggregateSources(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		resourceID := "sw42-audit-resource"
		operations := []struct {
			name string
			run  func(*server, *http.Request, string)
		}{
			{"environment", func(s *server, r *http.Request, workspace string) {
				s.auditCodingEnvironment(r, workspace, "sw42.environment", resourceID, map[string]any{"native": "metadata\x00value", "number": int64(9007199254740993)})
			}},
			{"code-host-request", func(s *server, r *http.Request, workspace string) {
				s.auditCodeHostAccount(context.Background(), r, workspace, "sw42.code-host-request",
					domain.CodeHostAccount{ID: resourceID, UserID: f.data.Viewer.ID, CodeHost: "gitlab", HostName: "gitlab.example.test", Login: "native\x00login", AuthType: "oauth"})
			}},
			{"code-host-context", func(s *server, r *http.Request, workspace string) {
				s.auditCodeHostAccount(r.Context(), nil, workspace, "sw42.code-host-context",
					domain.CodeHostAccount{ID: resourceID, UserID: f.data.Viewer.ID, CodeHost: "github", HostName: "github.com", Login: "native\x00login", AuthType: "pat"})
			}},
		}
		for _, operation := range operations {
			for _, source := range []string{"warm", "reloaded", "large", "cancelled"} {
				t.Run(operation.name+"/"+source, func(t *testing.T) {
					var outcomes []any
					for _, current := range []*store.SQLiteStore{repo, f.probe} {
						repo.SetWebhookSink(nil)
						repo.SetRealtimeSink(nil)
						if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
							t.Fatal(err)
						}
						body := "first\x00native"
						if source == "large" {
							body += strings.Repeat("legal large audit aggregate ", 16000)
						}
						if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "documents", "settings"), workspace, "scale_p21_sw42.audit_seed", "", nil, func(data *domain.Bootstrap) error {
							data.Documents = []domain.Document{
								{ID: "doc_sw42_first", Title: "First", Creator: f.data.Viewer, ContentData: map[string]any{"id": resourceID, "body": body}},
								{ID: "doc_sw42_second", Title: "Second", Creator: f.data.Viewer, ContentData: map[string]any{"id": resourceID, "body": "later duplicate aggregate"}},
							}
							data.Settings["sw42-audit-native"] = int64(9007199254740993)
							return nil
						}); err != nil {
							t.Fatal(err)
						}
						if source == "reloaded" {
							if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
								t.Fatal(err)
							}
							reloaded, found := repo.WorkspaceMetadataFields(workspace, "documents")
							if !found || len(reloaded.Documents) == 0 {
								t.Fatal("literal reload lost aggregate fixture")
							}
							body, _ = reloaded.Documents[0].ContentData["body"].(string)
						}
						if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
							t.Fatal(err)
						}
						before := scaleP21M3Fingerprint(t, repo, workspace, "documents")
						var event domain.DomainEvent
						var realtime domain.RealtimeEvent
						webhooks, broadcasts := 0, 0
						current.SetWebhookSink(func(_ string, value domain.DomainEvent) { event = value; webhooks++ })
						current.SetRealtimeSink(func(_ string, value domain.RealtimeEvent) { realtime = value; broadcasts++ })
						r := f.literalRequest(f.data.Viewer, nil, nil)
						if source == "cancelled" {
							ctx, cancel := context.WithCancel(r.Context())
							cancel()
							r = r.WithContext(ctx)
						}
						started := time.Now().UTC()
						operation.run(&server{store: current}, r, workspace)
						current.SetWebhookSink(nil)
						current.SetRealtimeSink(nil)
						if webhooks != 1 || broadcasts != 1 {
							t.Fatal("detached audit failed to publish exactly once", webhooks, broadcasts)
						}
						if entity := snapshotEntity(event); entity["body"] != body {
							t.Fatal("audit lost first nested aggregate precedence", entity["body"])
						}
						if before != scaleP21M3Fingerprint(t, repo, workspace, "documents") {
							t.Fatal("read-only audit collision changed canonical documents")
						}
						audits, err := repo.ListAuditEntries(t.Context(), f.data.Workspace.ID, store.AuditQuery{Since: started})
						if err != nil || len(audits) != 1 {
							t.Fatal("audit SQL side effect", len(audits), err)
						}
						for index := range audits {
							audits[index].ID, audits[index].Hash, audits[index].Seq, audits[index].CreatedAt = "", "", 0, time.Time{}
						}
						event.ID, event.CreatedAt = "", time.Time{}
						realtime.ID, realtime.Seq, realtime.CreatedAt = "", 0, time.Time{}
						outcomes = append(outcomes, scaleP21M2JSON(t, []any{audits, event, realtime}, false))
						if current == f.probe && current.WorkspaceMetadataDecodes() != 0 {
							t.Fatal("deferred audit decoded full metadata")
						}
					}
					if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
						t.Fatal(scaleP21M2Diff(outcomes[0], outcomes[1]))
					}
				})
			}
		}
	}
}
