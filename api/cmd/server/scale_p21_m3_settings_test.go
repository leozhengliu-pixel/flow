package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func scaleP21M3Request(workspace string, user domain.User, key *domain.APIKey, body string) *http.Request {
	r := httptest.NewRequest(http.MethodPatch, "/api/workspace/agent-guidance", strings.NewReader(body))
	r.Header.Set("X-Workspace-Key", workspace)
	if key != nil && key.TeamRestriction == "all" {
		user.Via = &domain.ActorBot{ID: "m3-via", Type: domain.ActorBotTypeOAuthApplication, Name: "Fixture connector", OnBehalfOfID: user.ID, OnBehalfOfName: user.Name}
	}
	ctx := context.WithValue(r.Context(), authUserContextKey{}, user)
	ctx = store.ContextWithActor(ctx, user)
	if key != nil {
		ctx = context.WithValue(ctx, apiKeyContextKey{}, *key)
	}
	return r.WithContext(ctx)
}

// The legacy writer relies on route middleware for membership. Apply that
// same live boundary before either handler; a store view does not certify the
// unported production middleware's metadata-loading implementation.
func scaleP21M3Call(s *server, w http.ResponseWriter, r *http.Request, handler func(*server, http.ResponseWriter, *http.Request)) {
	workspaceID, err := s.store.WorkspaceIDForKey(r.Context(), workspaceKey(r))
	if err != nil {
		writeError(w, http.StatusForbidden, "Workspace access required")
		return
	}
	_, status, err := s.store.WorkspaceRole(r.Context(), workspaceID, authUser(r).ID)
	if err != nil || status != "active" {
		writeError(w, http.StatusForbidden, "Workspace access required")
		return
	}
	handler(s, w, r)
}

// Keep the same fixture and operation before and after migrating the handler.
// This reports allocation cost, not a production capacity or latency claim.
func TestScaleP21M3GuidanceMeasurement(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace := f.data.Workspace.URLKey
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.seed", "", nil, func(data *domain.Bootstrap) error {
			data.Projects = append(data.Projects, domain.Project{ID: "m3-unrelated", Description: strings.Repeat("unrelated ", 40000)})
			data.WorkspaceSettings.AgentInstructions = "unchanged"
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, mode := range []struct {
			name string
			repo *store.SQLiteStore
		}{{"off", repo}, {"bounded-view", probe}} {
			service := &server{store: mode.repo}
			run := func() {
				w := httptest.NewRecorder()
				service.updateWorkspaceAgentGuidance(w, scaleP21M3Request(workspace, f.data.Viewer, nil, `{"instructions":"unchanged"}`))
				if w.Code != http.StatusOK {
					t.Fatalf("%s: status=%d body=%s", mode.name, w.Code, w.Body.String())
				}
			}
			run()
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			for range 5 {
				run()
			}
			runtime.ReadMemStats(&after)
			t.Logf("%s: bytes/op=%d allocs/op=%d full-decodes=%d", mode.name, (after.TotalAlloc-before.TotalAlloc)/5, (after.Mallocs-before.Mallocs)/5, mode.repo.WorkspaceMetadataDecodes())
		}
	}
}

func scaleP21M3JSON(t *testing.T, raw []byte) any {
	t.Helper()
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var result any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

type scaleP21M3Principal struct {
	name string
	user domain.User
}

func scaleP21M3Principals(t *testing.T, f scaleP03PlatformFixture) []scaleP21M3Principal {
	t.Helper()
	public, private, child := idgen.New("m3-public-"), idgen.New("m3-private-"), idgen.New("m3-child-")
	if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.seed", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: public, Key: public}, domain.Team{ID: private, Key: private, Private: true}, domain.Team{ID: child, Key: child})
		data.TeamSettings[private] = domain.TeamSettings{TeamID: private, Access: "private"}
		data.TeamSettings[child] = domain.TeamSettings{TeamID: child, ParentTeamID: private, Access: "public"}
		data.Projects = append(data.Projects, domain.Project{ID: "m3-hidden", SlugID: "m3-old-slug", TeamIDs: []string{private}, Description: strings.Repeat("unrelated ", 40000)})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	principals := []scaleP21M3Principal{{"admin-private-outsider", f.data.Viewer}}
	for _, spec := range []struct {
		name, role string
		teams      []string
	}{
		{"admin", "admin", []string{private}},
		{"member-private-outsider", "member", []string{public}},
		{"guest-explicit", "guest", []string{public}},
		{"sub-team-inheritance", "member", []string{private}},
	} {
		_, user := scaleP03Join(t, f, spec.name, idgen.New("m3-principal-")+"@example.test", spec.role, spec.teams...)
		principals = append(principals, scaleP21M3Principal{spec.name, user})
		if spec.role == "guest" {
			if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.grant", "", nil, func(data *domain.Bootstrap) error {
				for index := range data.Projects {
					if data.Projects[index].ID == "m3-hidden" {
						data.Projects[index].MemberIDs = []string{user.ID}
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	return principals
}

func scaleP21M3Credentials() []struct {
	name string
	key  *domain.APIKey
} {
	return []struct {
		name string
		key  *domain.APIKey
	}{
		{"session", nil},
		{"unrestricted", &domain.APIKey{ID: "m3-key", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "all"}},
		{"selected-empty", &domain.APIKey{ID: "m3-key", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "selected", TeamIDs: []string{}}},
		{"selected-set", &domain.APIKey{ID: "m3-key", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "selected", TeamIDs: []string{"m3-no-content"}}},
	}
}

// These are handler-boundary differentials. Production route authorization is
// independently registered and must be migrated before enabling the flag.
func TestScaleP21M3GuidanceDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace := f.data.Workspace.URLKey
		principals := scaleP21M3Principals(t, f)
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		unrelated := scaleP21M3Fingerprint(t, probe, workspace, "projects")
		reset := func() {
			t.Helper()
			if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.reset", "", nil, func(data *domain.Bootstrap) error {
				data.WorkspaceSettings.AgentInstructions = "before"
				data.WorkspaceSettings.AgentInstructionsEditedAt = nil
				data.WorkspaceSettings.AgentInstructionsEditedByID = ""
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		for _, principal := range principals {
			for _, credential := range scaleP21M3Credentials() {
				t.Run(principal.name+"/"+credential.name, func(t *testing.T) {
					for _, body := range []string{`{"instructions":" after "}`, `{"instructions":"before"}`, `{"instructions":"` + strings.Repeat("x", 8001) + `"}`} {
						var responses []any
						var effects []any
						var events []any
						for _, current := range []*store.SQLiteStore{repo, probe} {
							reset()
							service := &server{store: current}
							var event domain.DomainEvent
							var realtime domain.RealtimeEvent
							current.SetWebhookSink(func(_ string, next domain.DomainEvent) { event = next })
							current.SetRealtimeSink(func(_ string, next domain.RealtimeEvent) { realtime = next })
							w := httptest.NewRecorder()
							scaleP21M3Call(service, w, scaleP21M3Request(workspace, principal.user, credential.key, body), (*server).updateWorkspaceAgentGuidance)
							responses = append(responses, []any{w.Code, scaleP21M3JSON(t, w.Body.Bytes())})
							data, ok, err := probe.WorkspaceMetadataScoped(t.Context(), workspace, store.MetadataRecordScope{Fields: []string{"workspaceSettings"}})
							if err != nil || !ok {
								t.Fatalf("read persisted settings: %v", err)
							}
							settings := data.WorkspaceSettings
							if strings.Contains(body, " after ") && w.Code == http.StatusOK && (settings.AgentInstructionsEditedAt == nil || settings.AgentInstructionsEditedByID != principal.user.ID) {
								t.Fatal("editor attribution lost")
							}
							settings.AgentInstructionsEditedAt = nil
							effects = append(effects, settings)
							events = append(events, []any{event.Type, event.AggregateID, string(event.Payload), string(event.PreviousValues), realtime.Type, string(realtime.Payload)})
						}
						if !reflect.DeepEqual(responses[0], responses[1]) || !reflect.DeepEqual(effects[0], effects[1]) || !reflect.DeepEqual(events[0], events[1]) {
							t.Fatalf("guidance differs: responses=%v effects=%v events=%v", responses, effects, events)
						}
					}
				})
			}
		}
		reset()
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		if err := probe.MutateWorkspaceScoped(cancelled, workspace, "workspace.agent_guidance_updated", "workspace", nil, store.MetadataRecordScope{Fields: []string{"workspaceSettings"}}, func(data *domain.Bootstrap) error {
			data.WorkspaceSettings.AgentInstructions = "must roll back"
			return nil
		}); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled mutation: %v", err)
		}
		data, _, err := probe.WorkspaceMetadataScoped(t.Context(), workspace, store.MetadataRecordScope{Fields: []string{"workspaceSettings"}})
		if err != nil || data.WorkspaceSettings.AgentInstructions != "before" {
			t.Fatal("cancelled mutation persisted")
		}
		data.WorkspaceSettings.FeatureFlags["m3-owned"] = true
		again, _, err := probe.WorkspaceMetadataScoped(t.Context(), workspace, store.MetadataRecordScope{Fields: []string{"workspaceSettings"}})
		if err != nil || again.WorkspaceSettings.FeatureFlags["m3-owned"] {
			t.Fatal("returned settings alias canonical state")
		}
		oversized := strings.Repeat("large", 60000)
		if err := probe.MutateWorkspaceScoped(t.Context(), workspace, "workspace.agent_guidance_updated", "workspace", nil, store.MetadataRecordScope{Fields: []string{"workspaceSettings"}}, func(data *domain.Bootstrap) error {
			data.WorkspaceSettings.AgentInstructions = oversized
			return nil
		}); !errors.Is(err, store.ErrMetadataPageBudget) {
			t.Fatalf("oversized settings accepted: %v", err)
		}
		// A foreign workspace binding and authority revoked before queued or
		// replayed execution must fail on both representations.
		foreign, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "M3 foreign", idgen.New("m3-foreign-"), "us")
		if err != nil {
			t.Fatal(err)
		}
		f.resources.workspaces = append(f.resources.workspaces, foreign.Workspace.URLKey)
		member := principals[len(principals)-1].user
		for _, phase := range []string{"cross-workspace", "queued-revocation", "replay-revocation"} {
			key := foreign.Workspace.URLKey
			if phase != "cross-workspace" {
				key = workspace
				if phase == "queued-revocation" {
					if err := repo.SuspendMember(t.Context(), f.data.Workspace.ID, member.ID); err != nil {
						t.Fatal(err)
					}
				}
			}
			for _, current := range []*store.SQLiteStore{repo, probe} {
				w := httptest.NewRecorder()
				scaleP21M3Call(&server{store: current}, w, scaleP21M3Request(key, member, nil, `{"instructions":"denied"}`), (*server).updateWorkspaceAgentGuidance)
				if w.Code != http.StatusForbidden {
					t.Fatalf("%s status=%d body=%s", phase, w.Code, w.Body.String())
				}
			}
		}
		if probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("bounded handler decoded full metadata")
		}
		if scaleP21M3Fingerprint(t, probe, workspace, "projects") != unrelated {
			t.Fatal("guidance handler changed unrelated canonical bodies")
		}
	}
}
