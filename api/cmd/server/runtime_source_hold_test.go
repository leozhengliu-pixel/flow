package main

import (
	"encoding/json"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// These ordinary typed producer states are accepted by the legacy store.
// Keep the consumer pending until its bounded predicate preserves them.
func TestScaleP21SW16RuntimeTypedSourceHold(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_INTERNAL_CONVERSATION_RECORDS", "off")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, id := f.data.Workspace.URLKey, idgen.New("sw16-runtime-source-")
		owner := f.data.Viewer.ID
		principal, _ := json.Marshal(agentRunPrincipal{Mode: agentRunModeUser, UserID: owner})
		payload, _ := json.Marshal(map[string]any{"chat": workerChatRunInfo{MessageID: workerChatMessagePrefix + id, LoopQuestions: true}})
		run := store.AgentRun{WorkspaceKey: workspace, Kind: store.AgentRunKindChat, SessionID: id + "-session", Principal: principal, Payload: payload}
		for _, spec := range []struct {
			name            string
			sessions        []domain.AgentSession
			loops           []domain.Loop
			legacy, bounded bool
		}{
			{
				name:     "stored-owner-NUL",
				sessions: []domain.AgentSession{{ID: run.SessionID, UserID: owner + "\x00", LoopIDs: []string{id + "-loop"}}},
				loops:    []domain.Loop{{ID: id + "-loop", Status: "draft"}},
				legacy:   false, bounded: true,
			},
			{
				name:     "stored-status-NUL",
				sessions: []domain.AgentSession{{ID: run.SessionID, UserID: owner, LoopIDs: []string{id + "-loop"}}},
				loops:    []domain.Loop{{ID: id + "-loop", Status: "draft\x00"}},
				legacy:   false, bounded: true,
			},
			{
				name: "duplicate-session-inline",
				sessions: []domain.AgentSession{
					{ID: run.SessionID, UserID: owner, LoopIDs: []string{id + "-loop"}},
					{ID: run.SessionID, UserID: owner},
				},
				loops:  []domain.Loop{{ID: id + "-loop", Status: "draft"}},
				legacy: true, bounded: false,
			},
		} {
			t.Run(spec.name, func(t *testing.T) {
				if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "agentSessions", "loops"), workspace, "agent.session_updated", "", nil, func(data *domain.Bootstrap) error {
					data.AgentSessions, data.Loops = spec.sessions, spec.loops
					return nil
				}); err != nil {
					t.Fatal("ordinary typed producer rejected the source witness", err)
				}
				legacy := (&server{store: repo}).workerChatAsksLoopQuestions(run)
				bounded := (&server{store: f.probe}).workerChatAsksLoopQuestions(run)
				if legacy != spec.legacy || bounded != spec.bounded {
					t.Fatalf("typed source hold changed: legacy=%t bounded=%t want=%t/%t", legacy, bounded, spec.legacy, spec.bounded)
				}
			})
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("scalar predicate unexpectedly decoded full metadata")
		}
	}
}
