package main

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW47DeferredAuditAggregateDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
		prefix := idgen.New("sw47-audit-")
		body := strings.Repeat("large aggregate body ", 18000)
		type operation struct {
			name string
			id   string
			run  func(*server, *http.Request, string, domain.User)
		}
		operations := []operation{
			{
				name: "connector", id: prefix + "-connector",
				run: func(s *server, _ *http.Request, key string, actor domain.User) {
					s.auditAgentRunConnectorCall(&agentRunGrant{
						run:       store.AgentRun{ID: prefix + "-connector", WorkspaceKey: key, SessionID: prefix + "-session"},
						principal: agentRunPrincipal{UserID: actor.ID},
					}, connectorTool{Policy: applicationPolicy{ID: prefix + "-policy", Name: "Owned connector"}, RemoteName: "read"},
						[]byte(`{"isError":true}`), nil, 5*time.Millisecond)
				},
			},
			{
				name: "archive", id: prefix + "-archive",
				run: func(s *server, r *http.Request, key string, actor domain.User) {
					s.auditAgentSessionArchive(r, store.AgentTaskArchive{
						Task: domain.AgentTask{
							ID: prefix + "-archive", WorkspaceKey: key, ResourceType: "project",
							ResourceID: prefix + "-resource", AppUserID: prefix + "-app", CreatorID: actor.ID,
							CommentID: prefix + "-comment", CodingSessionID: prefix + "-coding", Trigger: "manual",
						},
						Archived: true, Stopped: true, PreviousStatus: "working",
					})
				},
			},
		}
		reset := func() {
			t.Helper()
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				source.SetWebhookSink(nil)
				source.SetRealtimeSink(nil)
			}
			if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "documents"), workspace, "document.updated", "", nil, func(data *domain.Bootstrap) error {
				data.Documents = nil
				for _, op := range operations {
					data.Documents = append(data.Documents, domain.Document{
						ID: op.id + "-document", Title: "Earlier aggregate", Creator: f.data.Viewer,
						ContentData: map[string]any{
							"id": op.id, "marker": "warm\x00" + op.name, "body": body,
							"hiddenCount": int64(9007199254740993), "actorId": f.data.Viewer.ID,
							"teamIds": []any{f.public},
						},
					})
				}
				return nil
			}); err != nil {
				t.Fatal("seed accepted large nested aggregate", err)
			}
		}
		type outcome struct {
			Audits  []domain.AuditLogEntry
			Effects any
		}
		f.matrix(t, func(t *testing.T, principal domain.User, credential *domain.APIKey) {
			actor := principal
			if credential != nil && credential.TeamRestriction == "all" {
				actor.Via = &domain.ActorBot{
					ID: prefix + "-credential", Type: domain.ActorBotTypeOAuthApplication,
					Name: "Owned application", OnBehalfOfID: actor.ID, OnBehalfOfName: actor.Name,
				}
			}
			for _, phase := range []string{"active", "revoked", "cancelled", "missing"} {
				t.Run(phase, func(t *testing.T) {
					if phase == "revoked" {
						if err := repo.SuspendMember(t.Context(), workspaceID, principal.ID); err != nil {
							t.Fatal(err)
						}
						defer func() {
							if err := repo.ResumeMember(t.Context(), workspaceID, principal.ID); err != nil {
								t.Error(err)
							}
						}()
					}
					var outcomes []outcome
					for _, source := range []*store.SQLiteStore{repo, f.probe} {
						reset()
						before := scaleP21M3Fingerprint(t, repo, workspace, "documents")
						var events []domain.DomainEvent
						var realtime []domain.RealtimeEvent
						source.SetWebhookSink(func(_ string, event domain.DomainEvent) { events = append(events, event) })
						source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { realtime = append(realtime, event) })
						started := time.Now().UTC()
						for _, op := range operations {
							key := workspace
							if phase == "missing" {
								key = prefix + "-missing"
							}
							r := f.request(actor, credential, nil)
							if phase == "cancelled" {
								ctx, cancel := context.WithCancel(r.Context())
								cancel()
								r = r.WithContext(ctx)
							}
							op.run(&server{store: source}, r, key, actor)
						}
						expected := len(operations)
						if phase == "missing" {
							expected = 0
						}
						audits, err := repo.ListAuditEntries(t.Context(), workspaceID, store.AuditQuery{Since: started})
						if err != nil || len(audits) != expected || len(events) != expected || len(realtime) != expected {
							t.Fatalf("bounded=%v phase=%s audit/webhook/realtime=%d/%d/%d expected=%d err=%v",
								source == f.probe, phase, len(audits), len(events), len(realtime), expected, err)
						}
						for i := range audits {
							audits[i].ID, audits[i].Hash, audits[i].Seq, audits[i].CreatedAt = "", "", 0, time.Time{}
						}
						for i, event := range realtime {
							// The legacy aggregate's generic JSON conversion rounds int64
							// through float64; the wire value must preserve that result.
							var payload struct {
								Entity struct {
									ID          string      `json:"id"`
									Marker      string      `json:"marker"`
									Body        string      `json:"body"`
									HiddenCount json.Number `json:"hiddenCount"`
									ActorID     string      `json:"actorId"`
									TeamIDs     []string    `json:"teamIds"`
								} `json:"entity"`
							}
							if err := json.Unmarshal(event.Payload, &payload); err != nil ||
								payload.Entity.ID != operations[i].id ||
								payload.Entity.Marker != "warm\x00"+operations[i].name || payload.Entity.Body != body ||
								payload.Entity.HiddenCount.String() != "9007199254740992" ||
								payload.Entity.ActorID != f.data.Viewer.ID || !reflect.DeepEqual(payload.Entity.TeamIDs, []string{f.public}) {
								t.Fatalf("bounded=%v phase=%s earlier aggregate id=%q marker=%q bodyBytes=%d count=%q actor=%q teams=%q err=%v",
									source == f.probe, phase, payload.Entity.ID, payload.Entity.Marker, len(payload.Entity.Body),
									payload.Entity.HiddenCount.String(), payload.Entity.ActorID, payload.Entity.TeamIDs, err)
							}
							if len(events[i].Snapshot) <= 256<<10 {
								t.Fatal("large aggregate webhook snapshot was omitted")
							}
						}
						if after := scaleP21M3Fingerprint(t, repo, workspace, "documents"); after != before {
							t.Fatal("audit changed unrelated canonical documents")
						}
						outcomes = append(outcomes, outcome{Audits: audits, Effects: scaleP21SW22Effects(t, events, realtime)})
					}
					if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
						t.Fatal("large collision audit/effect semantics differ")
					}
				})
			}
		})
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("deferred audit decoded complete metadata")
		}
	}
}
