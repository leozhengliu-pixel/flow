package main

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW7DeferredAuditsDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
		prefix := idgen.New("sw7-audit-")
		missingWorkspace, missingUser := prefix+"-missing-workspace", prefix+"-missing-user"
		projectHash := scaleP21M3Fingerprint(t, repo, workspace, "projects")
		settingsHash := scaleP21M2UnrelatedHash(t, t.Context(), repo, workspace)
		otherUser := func(actor domain.User) domain.User {
			if actor.ID == missingUser {
				return actor
			}
			for _, candidate := range f.principals {
				if candidate.user.ID != actor.ID {
					return candidate.user
				}
			}
			t.Fatal("fixture needs a distinct audit subject")
			return domain.User{}
		}
		type operation struct {
			name         string
			detached     bool
			personal     bool
			otherSubject bool
			run          func(*server, *http.Request, string, domain.User)
		}
		operations := []operation{
			{
				name: "connector-ok", detached: true,
				run: func(s *server, _ *http.Request, key string, user domain.User) {
					s.auditAgentRunConnectorCall(&agentRunGrant{
						run:       store.AgentRun{ID: prefix + "-connector-ok", WorkspaceKey: key, SessionID: prefix + "-session"},
						principal: agentRunPrincipal{UserID: user.ID},
					}, connectorTool{Policy: applicationPolicy{ID: prefix + "-policy", Name: "Audit connector"}, RemoteName: "read"},
						[]byte(`{"content":[]}`), nil, 5*time.Millisecond)
				},
			},
			{
				name: "connector-tool-error", detached: true,
				run: func(s *server, _ *http.Request, key string, user domain.User) {
					s.auditAgentRunConnectorCall(&agentRunGrant{
						run:       store.AgentRun{ID: prefix + "-connector-tool-error", WorkspaceKey: key, SessionID: prefix + "-session"},
						principal: agentRunPrincipal{UserID: user.ID},
					}, connectorTool{Policy: applicationPolicy{ID: prefix + "-policy", Name: "Audit connector"}, RemoteName: "read"},
						[]byte(`{"isError":true}`), nil, 5*time.Millisecond)
				},
			},
			{
				name: "connector-failed", detached: true,
				run: func(s *server, _ *http.Request, key string, user domain.User) {
					s.auditAgentRunConnectorCall(&agentRunGrant{
						run:       store.AgentRun{ID: prefix + "-connector-failed", WorkspaceKey: key, SessionID: prefix + "-session"},
						principal: agentRunPrincipal{UserID: user.ID},
					}, connectorTool{Policy: applicationPolicy{ID: prefix + "-policy", Name: "Audit connector"}, RemoteName: "read"},
						nil, errors.New("fixture transport failure"), 5*time.Millisecond)
				},
			},
			{
				name: "agent-session", detached: true,
				run: func(s *server, r *http.Request, key string, user domain.User) {
					s.auditAgentSessionArchive(r, store.AgentTaskArchive{
						Task: domain.AgentTask{
							ID: prefix + "-agent-session", WorkspaceKey: key, ResourceType: "project",
							ResourceID: prefix + "-resource", AppUserID: prefix + "-agent", CreatorID: user.ID,
							CommentID: prefix + "-comment", CodingSessionID: prefix + "-coding", Trigger: "manual",
						},
						Archived: true, Stopped: true, PreviousStatus: "working",
					})
				},
			},
			{
				name: "coding-environment", detached: true,
				run: func(s *server, r *http.Request, key string, _ domain.User) {
					s.auditCodingEnvironment(r, key, "sw7.environment", prefix+"-environment", map[string]any{"name": "Owned environment"})
				},
			},
			{
				name: "code-host-request", detached: true,
				run: func(s *server, r *http.Request, key string, user domain.User) {
					s.auditCodeHostAccount(r.Context(), r, key, "sw7.code-host-request", domain.CodeHostAccount{
						ID: prefix + "-code-host-request", UserID: user.ID, CodeHost: "gitlab",
						HostName: "gitlab.example.test", Login: "owned-user", AuthType: "oauth",
					})
				},
			},
			{
				name: "code-host-context", detached: true,
				run: func(s *server, r *http.Request, key string, user domain.User) {
					s.auditCodeHostAccount(r.Context(), nil, key, "sw7.code-host-context", domain.CodeHostAccount{
						ID: prefix + "-code-host-context", UserID: user.ID, CodeHost: "github",
						HostName: "github.example.test", Login: "owned-user", AuthType: "pat",
					})
				},
			},
			{
				name: "member-status-self",
				run: func(s *server, r *http.Request, _ string, user domain.User) {
					r.SetPathValue("userId", user.ID)
					s.recordMemberStatusAudit(r, "sw7.member-status")
				},
			},
			{
				name: "member-status-other", otherSubject: true,
				run: func(s *server, r *http.Request, _ string, user domain.User) {
					r.SetPathValue("userId", otherUser(user).ID)
					s.recordMemberStatusAudit(r, "sw7.member-status-other")
				},
			},
			{
				name: "personal-data-self", personal: true,
				run: func(s *server, r *http.Request, _ string, user domain.User) {
					s.auditPersonalData(r.Context(), user.ID, "sw7.personal-data", map[string]any{"format": "owned-fixture"})
				},
			},
			{
				name: "personal-data-other", personal: true, otherSubject: true,
				run: func(s *server, r *http.Request, _ string, user domain.User) {
					s.auditPersonalData(r.Context(), otherUser(user).ID, "sw7.personal-data-other", map[string]any{"format": "owned-fixture"})
				},
			},
		}
		f.matrix(t, func(t *testing.T, principal domain.User, credential *domain.APIKey) {
			actor := principal
			if credential != nil && credential.TeamRestriction == "all" {
				actor.Via = &domain.ActorBot{
					ID: prefix + "-credential", Type: domain.ActorBotTypeOAuthApplication,
					Name: "Audit application", OnBehalfOfID: actor.ID, OnBehalfOfName: actor.Name,
				}
			}
			for _, phase := range []string{"active", "revoked", "cancelled", "missing"} {
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
					if phase == "revoked" {
						if err := repo.SuspendMember(t.Context(), workspaceID, principal.ID); err != nil {
							t.Fatal(err)
						}
					}
					type outcome struct {
						BeforeViewer domain.User
						Viewer       domain.User
						Audits       []domain.AuditLogEntry
						Webhooks     []domain.DomainEvent
						Realtime     []domain.RealtimeEvent
						Records      any
					}
					var outcomes []outcome
					for _, source := range []*store.SQLiteStore{repo, f.probe} {
						repo.SetWebhookSink(nil)
						repo.SetRealtimeSink(nil)
						seedCtx := store.WithMetadataFields(store.ContextWithActor(t.Context(), actor), "settings")
						if err := repo.MutateWorkspace(seedCtx, workspace, "application_policy.updated", prefix, nil, func(data *domain.Bootstrap) error {
							data.Settings[prefix] = "unchanged"
							return nil
						}); err != nil {
							t.Fatal(err)
						}
						if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
							t.Fatal(err)
						}
						before, ok, err := f.probe.WorkspaceMetadataScoped(t.Context(), workspace, store.MetadataRecordScope{})
						if err != nil || !ok {
							t.Fatalf("read persisted viewer: ok=%v err=%v", ok, err)
						}
						got := outcome{BeforeViewer: before.Viewer}
						source.SetWebhookSink(func(_ string, event domain.DomainEvent) { got.Webhooks = append(got.Webhooks, event) })
						source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { got.Realtime = append(got.Realtime, event) })
						started := time.Now().UTC()
						expected := 0
						for _, operation := range operations {
							key, user := workspace, actor
							if phase == "missing" {
								key = missingWorkspace
								if operation.personal {
									user.ID = missingUser
								}
							}
							r := f.request(user, credential, nil)
							r.SetPathValue("workspaceKey", key)
							r.SetPathValue("workspace", key)
							if phase == "cancelled" {
								ctx, cancel := context.WithCancel(r.Context())
								cancel()
								r = r.WithContext(ctx)
							}
							operation.run(&server{store: source}, r, key, user)
							if phase != "missing" && (phase != "cancelled" || operation.detached) && (phase != "revoked" || !operation.personal || operation.otherSubject) {
								expected++
							}
						}
						got.Audits, err = repo.ListAuditEntries(t.Context(), workspaceID, store.AuditQuery{Since: started})
						if err != nil {
							t.Fatal(err)
						}
						if len(got.Audits) != expected || len(got.Webhooks) != expected || len(got.Realtime) != expected {
							t.Fatalf("effects phase=%s bounded=%v audits=%d webhook=%d realtime=%d want=%d", phase, source == f.probe, len(got.Audits), len(got.Webhooks), len(got.Realtime), expected)
						}
						for i := range got.Audits {
							expectedActor := principal.ID
							background := got.Audits[i].ResourceType == "agent_run"
							if background {
								expectedActor = before.Viewer.ID
							}
							if got.Audits[i].Actor.ID != expectedActor {
								t.Fatalf("deferred audit lost its original actor: %#v", got.Audits[i])
							}
							if background && got.Audits[i].BotActor != nil {
								t.Fatalf("background audit inherited request credential attribution: %#v", got.Audits[i])
							}
							if actor.Via != nil && !background {
								if got.Audits[i].BotActor == nil || got.Audits[i].BotActor.ID != actor.Via.ID {
									t.Fatalf("deferred audit lost request credential attribution: %#v", got.Audits[i])
								}
							}
							got.Audits[i].ID, got.Audits[i].Hash, got.Audits[i].Seq, got.Audits[i].CreatedAt = "", "", 0, time.Time{}
						}
						for i := range got.Webhooks {
							got.Webhooks[i].ID, got.Webhooks[i].CreatedAt = "", time.Time{}
						}
						for i := range got.Realtime {
							got.Realtime[i].ID, got.Realtime[i].Seq, got.Realtime[i].CreatedAt = "", 0, time.Time{}
						}
						got.Records = scaleP21M2Canonical(t, repo, workspace,
							store.RecordKey{Kind: "settings", ID: prefix}, store.RecordKey{Kind: "users", ID: principal.ID})
						after, ok, err := f.probe.WorkspaceMetadataScoped(t.Context(), workspace, store.MetadataRecordScope{})
						if err != nil || !ok {
							t.Fatalf("read persisted viewer after audits: ok=%v err=%v", ok, err)
						}
						got.Viewer = after.Viewer
						if !reflect.DeepEqual(got.BeforeViewer, got.Viewer) {
							t.Fatal("audit-only helpers changed the persisted root viewer")
						}
						if scaleP21M3Fingerprint(t, source, workspace, "projects") != projectHash ||
							scaleP21M2UnrelatedHash(t, t.Context(), source, workspace) != settingsHash {
							t.Fatal("deferred audits changed unrelated canonical rows")
						}
						outcomes = append(outcomes, got)
					}
					if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
						t.Fatalf("deferred audit semantics differ: %s", scaleP21M2Diff(outcomes[0], outcomes[1]))
					}
					if phase == "revoked" {
						if err := repo.ResumeMember(t.Context(), workspaceID, principal.ID); err != nil {
							t.Fatal(err)
						}
					}
				})
			}
		})
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("deferred audits decoded complete metadata")
		}
	}
}
