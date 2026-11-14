package main

import (
	"context"
	"net/http"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW49PersonalAuditBaseline(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		var queries atomic.Int64
		r := f.literalRequest(f.data.Viewer, nil, nil)
		r = r.WithContext(store.WithSQLStatementObserver(r.Context(), func(string) { queries.Add(1) }))
		count := 0
		repo.SetWebhookSink(func(string, domain.DomainEvent) { count++ })
		defer repo.SetWebhookSink(nil)
		started := time.Now().UTC()
		(&server{store: repo}).auditPersonalConnection(r.Context(), r, f.data.Workspace.URLKey, "sw49.baseline", domain.PersonalConnection{
			ID: idgen.New("sw49-connection-"), UserID: f.data.Viewer.ID, Provider: domain.PersonalConnectionGoogleCalendar, Account: "account@example.test",
		})
		if count != 1 {
			t.Fatalf("literal baseline did not publish: %d", count)
		}
		audits, err := repo.ListAuditEntries(t.Context(), f.data.Workspace.ID, store.AuditQuery{Since: started})
		if err != nil || len(audits) != 1 {
			t.Fatal("literal baseline audit", len(audits), err)
		}
		t.Logf("literal personal-connection audit primary SQL=%d", queries.Load())
	}
}

func TestScaleP21SW49PersonalAuditDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
		prefix := idgen.New("sw49-audit-")
		type outcome struct {
			Audits   []domain.AuditLogEntry
			Events   []domain.DomainEvent
			Realtime []domain.RealtimeEvent
			Viewer   domain.User
		}
		f.matrix(t, func(t *testing.T, principal domain.User, key *domain.APIKey) {
			actor := principal
			if key != nil && key.TeamRestriction == "all" {
				actor.Via = &domain.ActorBot{ID: prefix + "-credential", Type: domain.ActorBotTypeOAuthApplication, Name: "Audit credential", OnBehalfOfID: principal.ID, OnBehalfOfName: principal.Name}
			}
			for _, phase := range []string{"active", "queued-revocation", "replayed-revocation", "cancelled"} {
				t.Run(phase, func(t *testing.T) {
					_, status, err := repo.WorkspaceRole(t.Context(), workspaceID, principal.ID)
					if err != nil {
						t.Fatal(err)
					}
					if status != "active" {
						if err := repo.ResumeMember(t.Context(), workspaceID, principal.ID); err != nil {
							t.Fatal(err)
						}
					}
					if phase == "queued-revocation" || phase == "replayed-revocation" {
						if err := repo.SuspendMember(t.Context(), workspaceID, principal.ID); err != nil {
							t.Fatal(err)
						}
					}
					defer func() {
						if phase == "queued-revocation" || phase == "replayed-revocation" {
							if err := repo.ResumeMember(t.Context(), workspaceID, principal.ID); err != nil {
								t.Error(err)
							}
						}
					}()
					targets := []struct {
						name, id string
						nested   bool
					}{
						{"user-prefix", principal.ID, false},
						{"legacy-nested", prefix + "-connection", true},
						{"missing-prefix-nested", "usr_" + prefix + "-missing", true},
						{"missing", prefix + "-missing", false},
					}
					requestSources := []bool{true}
					if principal.ID == f.data.Viewer.ID && key == nil {
						requestSources = []bool{false, true}
					} else {
						targets = targets[1:2]
					}
					for _, target := range targets {
						for _, requestSource := range requestSources {
							var results []outcome
							var queryCounts []int
							for _, source := range []*store.SQLiteStore{repo, f.probe} {
								repo.SetWebhookSink(nil)
								repo.SetRealtimeSink(nil)
								if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
									t.Fatal(err)
								}
								if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "settings"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
									delete(data.Settings, prefix)
									return nil
								}); err != nil {
									t.Fatal(err)
								}
								if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "settings"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
									data.Settings[prefix] = map[string]any{"unrelated": "unchanged"}
									if target.nested {
										data.Settings[prefix] = map[string]any{"wrapper": map[string]any{"id": target.id, "name": "Warm before\x00after", "hidden": true}}
									}
									return nil
								}); err != nil {
									t.Fatal(err)
								}
								if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
									t.Fatal(err)
								}
								before, ok := repo.WorkspaceMetadata(workspace)
								if !ok {
									t.Fatal("owned workspace disappeared")
								}
								if target.nested {
									nested := before.Settings[prefix].(map[string]any)["wrapper"].(map[string]any)
									if nested["name"] != "Warm before\x00after" {
										t.Fatal("paired phase did not install the literal warm preimage")
									}
								}
								projectHash := scaleP21M3Fingerprint(t, repo, workspace, "projects")
								settingsHash := scaleP21M3Fingerprint(t, repo, workspace, "settings")
								got := outcome{}
								source.SetWebhookSink(func(_ string, event domain.DomainEvent) { got.Events = append(got.Events, event) })
								source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { got.Realtime = append(got.Realtime, event) })
								var queries atomic.Int64
								r := f.literalRequest(actor, key, nil)
								r = r.WithContext(store.WithSQLStatementObserver(r.Context(), func(string) { queries.Add(1) }))
								if phase == "cancelled" {
									ctx, cancel := context.WithCancel(r.Context())
									cancel()
									r = r.WithContext(ctx)
								}
								ctx := r.Context()
								var request *http.Request
								if requestSource {
									request = r
									// The request context has precedence over this
									// caller context, including actor attribution.
									ctx = store.ContextWithActor(context.Background(), f.data.Viewer)
								}
								connection := domain.PersonalConnection{ID: target.id, UserID: principal.ID, Provider: domain.PersonalConnectionGoogleCalendar, Account: "account@example.test"}
								started := time.Now().UTC()
								(&server{store: source}).auditPersonalConnection(ctx, request, workspace, "sw49.connection", connection)
								queryCounts = append(queryCounts, int(queries.Load()))
								t.Logf("before effect comparison target=%s request=%v bounded=%v SQL=%d", target.name, requestSource, source == f.probe, queries.Load())
								var err error
								got.Audits, err = repo.ListAuditEntries(t.Context(), workspaceID, store.AuditQuery{Since: started})
								if err != nil {
									t.Fatal(err)
								}
								source.SetWebhookSink(nil)
								source.SetRealtimeSink(nil)
								if len(got.Audits) != 1 || len(got.Events) != 1 || len(got.Realtime) != 1 {
									t.Fatalf("bounded=%v target=%s request=%v audits/events/realtime=%d/%d/%d", source == f.probe, target.name, requestSource, len(got.Audits), len(got.Events), len(got.Realtime))
								}
								if got.Audits[0].Actor.ID != principal.ID || got.Audits[0].ResourceID != target.id ||
									got.Audits[0].Metadata["provider"] != connection.Provider || got.Audits[0].Metadata["account"] != connection.Account ||
									got.Audits[0].Metadata["userId"] != principal.ID {
									t.Fatal("audit attribution or metadata changed", got.Audits[0])
								}
								for i := range got.Audits {
									got.Audits[i].ID, got.Audits[i].Hash, got.Audits[i].Seq, got.Audits[i].CreatedAt = "", "", 0, time.Time{}
								}
								for i := range got.Events {
									got.Events[i].ID, got.Events[i].CreatedAt = "", time.Time{}
								}
								for i := range got.Realtime {
									got.Realtime[i].ID, got.Realtime[i].Seq, got.Realtime[i].CreatedAt = "", 0, time.Time{}
								}
								read, found, err := source.WorkspaceMetadataScoped(t.Context(), workspace, store.MetadataRecordScope{})
								if err != nil || !found {
									t.Fatal("read installed viewer", found, err)
								}
								got.Viewer = read.Viewer
								if !reflect.DeepEqual(before.Viewer, got.Viewer) ||
									scaleP21M3Fingerprint(t, repo, workspace, "projects") != projectHash ||
									scaleP21M3Fingerprint(t, repo, workspace, "settings") != settingsHash {
									t.Fatal("audit helper changed viewer or unrelated canonical records")
								}
								results = append(results, got)
							}
							if !reflect.DeepEqual(results[0], results[1]) {
								t.Fatalf("target=%s request=%v differs: %s", target.name, requestSource, scaleP21M2Diff(results[0], results[1]))
							}
							t.Logf("target=%s request=%v SQL old=%d bounded=%d", target.name, requestSource, queryCounts[0], queryCounts[1])
						}
					}
				})
			}
		})
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("personal connection audits decoded full metadata")
		}
	}
}
