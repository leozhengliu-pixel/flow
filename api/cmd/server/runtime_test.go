package main

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW16WorkerDraftLoopDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw16-runtime-")
		stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		draftID, publishedID := prefix+"draft", prefix+"published"
		sessions := []domain.AgentSession{}
		for _, principal := range f.principals {
			id := prefix + principal.user.ID
			sessions = append(sessions,
				domain.AgentSession{ID: id, SlugID: id + "-old-slug", UserID: principal.user.ID, Title: strings.Repeat("large session title ", 18000), LoopIDs: []string{publishedID, "missing", draftID}},
				domain.AgentSession{ID: id + "-deleted", UserID: principal.user.ID, DeletedAt: &stamp, LoopIDs: []string{draftID}},
				domain.AgentSession{ID: id + "-no-draft", UserID: principal.user.ID, LoopIDs: []string{publishedID}},
				domain.AgentSession{ID: id + "-wrong-loop-case", UserID: principal.user.ID, LoopIDs: []string{strings.ToUpper(draftID)}},
				domain.AgentSession{ID: id + "-loop-slug", UserID: principal.user.ID, LoopIDs: []string{domain.LoopSlugID(domain.Loop{ID: draftID, Name: "Draft"})}},
			)
		}
		loops := []domain.Loop{
			{ID: draftID, Name: "Draft", Status: "draft", Instructions: strings.Repeat("large legal loop instructions ", 14000)},
			{ID: publishedID, Status: "published"},
		}
		for range 75 {
			id := idgen.New("sw16-unused-")
			sessions = append(sessions, domain.AgentSession{ID: id, UserID: f.data.Viewer.ID, LoopIDs: []string{id}})
			loops = append(loops, domain.Loop{ID: id, Status: "draft"})
		}
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "agentSessions", "loops"), workspace, "agent.session_updated", "", nil, func(data *domain.Bootstrap) error {
			data.AgentSessions, data.Loops = sessions, loops
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		beforeSessions := scaleP21M3Fingerprint(t, repo, workspace, "agentSessions")
		beforeLoops := scaleP21M3Fingerprint(t, repo, workspace, "loops")
		makeRun := func(user domain.User, key *domain.APIKey, sessionID string, question bool) store.AgentRun {
			t.Helper()
			principal := agentRunPrincipal{Mode: agentRunModeUser, UserID: user.ID}
			if key != nil {
				principal.Scopes = key.Scopes
				if key.TeamRestriction == "selected" {
					principal.TeamIDs = key.TeamIDs
				}
			}
			rawPrincipal, err := json.Marshal(principal)
			if err != nil {
				t.Fatal(err)
			}
			rawPayload, err := json.Marshal(map[string]any{"chat": workerChatRunInfo{MessageID: workerChatMessagePrefix + prefix, LoopQuestions: question}})
			if err != nil {
				t.Fatal(err)
			}
			return store.AgentRun{WorkspaceKey: workspace, Kind: store.AgentRunKindChat, SessionID: sessionID, Principal: rawPrincipal, Payload: rawPayload}
		}
		off, bounded := &server{store: repo}, &server{store: f.probe}
		for _, native := range []string{"off", "on"} {
			t.Run("conversation-records="+native, func(t *testing.T) {
				t.Setenv("FLOW_INTERNAL_CONVERSATION_RECORDS", native)
				f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
					id := prefix + user.ID
					for _, spec := range []struct {
						name, session string
						question      bool
						change        func(*store.AgentRun)
						want          bool
					}{
						{"owned-large", id, true, nil, true},
						{"deleted-retained", id + "-deleted", true, nil, true},
						{"published-only", id + "-no-draft", true, nil, false},
						{"wrong-loop-case", id + "-wrong-loop-case", true, nil, false},
						{"loop-slug", id + "-loop-slug", true, nil, false},
						{"missing", id + "-missing", true, nil, false},
						{"session-slug", id + "-old-slug", true, nil, false},
						{"long-id", strings.Repeat("x", 192), true, nil, false},
						{"nul-id", id + "\x00", true, nil, false},
						{"nul-id-prefix", "\x00" + id, true, nil, false},
						{"questions-disabled", id, false, nil, false},
						{"foreign-owner", id, true, func(run *store.AgentRun) {
							run.Principal, _ = json.Marshal(agentRunPrincipal{Mode: agentRunModeUser, UserID: user.ID + "-other"})
						}, false},
						{"nul-owner", id, true, func(run *store.AgentRun) {
							run.Principal, _ = json.Marshal(agentRunPrincipal{Mode: agentRunModeUser, UserID: user.ID + "\x00"})
						}, false},
						{"app-mode-uses-original-user-boundary", id, true, func(run *store.AgentRun) {
							run.Principal, _ = json.Marshal(agentRunPrincipal{Mode: agentRunModeApp, UserID: user.ID, AppUserID: "sw16-app-user"})
						}, true},
						{"invalid-app-principal", id, true, func(run *store.AgentRun) {
							run.Principal, _ = json.Marshal(agentRunPrincipal{Mode: agentRunModeApp, UserID: user.ID})
						}, false},
						{"missing-workspace", id, true, func(run *store.AgentRun) { run.WorkspaceKey += "-missing" }, false},
						{"nul-workspace", id, true, func(run *store.AgentRun) { run.WorkspaceKey += "\x00" }, false},
						{"invalid-principal", id, true, func(run *store.AgentRun) { run.Principal = json.RawMessage(`{"mode":"unknown"}`) }, false},
						{"invalid-chat", id, true, func(run *store.AgentRun) { run.Kind = "other" }, false},
					} {
						t.Run(spec.name, func(t *testing.T) {
							run := makeRun(user, key, spec.session, spec.question)
							if spec.change != nil {
								spec.change(&run)
							}
							old, next := off.workerChatAsksLoopQuestions(run), bounded.workerChatAsksLoopQuestions(run)
							if old != spec.want || next != old {
								t.Fatalf("draft-question old=%t bounded=%t want=%t", old, next, spec.want)
							}
						})
					}
				})
			})
		}
		t.Setenv("FLOW_INTERNAL_CONVERSATION_RECORDS", "off")
		run := makeRun(f.data.Viewer, nil, prefix+f.data.Viewer.ID, true)
		for _, service := range []*server{off, bounded} {
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			for range 5 {
				if !service.workerChatAsksLoopQuestions(run) {
					t.Fatal("measurement fixture stopped advertising the question")
				}
			}
			runtime.ReadMemStats(&after)
			t.Logf("bounded=%t draft-loop predicate bytes/op=%d allocs/op=%d", service.store.BoundedMetadataEnabled(), (after.TotalAlloc-before.TotalAlloc)/5, (after.Mallocs-before.Mallocs)/5)
		}
		if scaleP21M3Fingerprint(t, repo, workspace, "agentSessions") != beforeSessions ||
			scaleP21M3Fingerprint(t, repo, workspace, "loops") != beforeLoops || f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("draft-loop predicate rewrote rows or decoded metadata")
		}
		member := f.principals[2].user
		queued := makeRun(member, nil, prefix+member.ID, true)
		if err := repo.SuspendMember(t.Context(), f.data.Workspace.ID, member.ID); err != nil {
			t.Fatal(err)
		}
		for _, native := range []string{"off", "on"} {
			t.Setenv("FLOW_INTERNAL_CONVERSATION_RECORDS", native)
			for _, phase := range []string{"queued", "replayed"} {
				// This selector preserves its established boundary. Native
				// conversation reads enforce live membership; the old metadata
				// selector delegates that check to the actual run tool boundary.
				want := native == "off"
				old, next := off.workerChatAsksLoopQuestions(queued), bounded.workerChatAsksLoopQuestions(queued)
				if old != want || next != old {
					t.Fatalf("%s native=%s revoked predicate old=%t bounded=%t want=%t", phase, native, old, next, want)
				}
			}
		}
	}
}
