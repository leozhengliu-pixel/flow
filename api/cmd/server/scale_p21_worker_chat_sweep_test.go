package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21WorkerChatSweepDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_INTERNAL_CONVERSATION_RECORDS", "off")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace := f.data.Workspace.URLKey
		probe, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{
			"queued-expired", "queued-recent", "deleted", "duplicate-sessions", "duplicate-messages",
			"empty-session", "empty-owner", "nul-session", "nul-owner", "nul-message", "nul-prefix", "nul-status",
			"finished", "non-worker", "nil-messages", "empty-messages",
			"missing-run-stale", "missing-run-recent", "missing-run-nil-stamp", "missing-workspace", "canceled",
		} {
			t.Run(name, func(t *testing.T) {
				type outcome struct {
					Swept               int
					Canceled, StateGone []bool
					Statements          []string
				}
				var outputs []outcome
				runSessionID := idgen.New("worker-sweep-session-")
				ownerID := idgen.New("worker-sweep-missing-owner-")
				runMessageID := workerChatMessagePrefix + idgen.New("sweep-")
				at := time.Now().UTC()
				for _, source := range []*store.SQLiteStore{repo, probe} {
					sessionID := runSessionID
					owner := ownerID
					messageID, status := runMessageID, agentMessageStreaming
					now := at.Add(2 * time.Hour)
					switch name {
					case "queued-recent", "missing-run-recent":
						now = at
					case "empty-session":
						sessionID = ""
					case "empty-owner":
						owner = ""
					case "nul-session":
						sessionID += "\x00"
					case "nul-owner":
						owner += "\x00"
					case "nul-message":
						messageID += "\x00"
					case "nul-prefix":
						messageID = "agent_mes\x00sage_" + strings.TrimPrefix(runMessageID, "agent_message_")
					case "nul-status":
						status += "\x00"
					case "finished":
						status = ""
					case "non-worker":
						messageID = "legacy_" + runMessageID
					}
					message := domain.AgentMessage{ID: messageID, Status: status, UpdatedAt: &at, Content: strings.Repeat("ignored worker transcript ", 20000), Parts: []domain.AgentMessagePart{{ID: "ignored-part", Text: strings.Repeat("ignored worker part ", 20000)}}}
					if name == "missing-run-nil-stamp" {
						message.UpdatedAt = nil
					}
					session := domain.AgentSession{ID: sessionID, UserID: owner, Messages: []domain.AgentMessage{message}, Title: strings.Repeat("ignored session title ", 10000)}
					sessions := []domain.AgentSession{session}
					switch name {
					case "deleted":
						sessions[0].DeletedAt = &at
					case "duplicate-sessions":
						sessions = []domain.AgentSession{{ID: sessionID, UserID: owner}, session, session}
					case "duplicate-messages":
						sessions[0].Messages = append(sessions[0].Messages, message)
					case "nil-messages":
						sessions[0].Messages = nil
					case "empty-messages":
						sessions[0].Messages = []domain.AgentMessage{}
					}
					if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "agentSessions"), workspace, "scale_p21.worker_sweep_reset", "", nil, func(data *domain.Bootstrap) error {
						data.AgentSessions = nil
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "agentSessions"), workspace, "scale_p21.worker_sweep", "", nil, func(data *domain.Bootstrap) error {
						data.AgentSessions = sessions
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					if err := probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
						t.Fatal(err)
					}
					preimage, found := repo.WorkspaceMetadataFields(workspace, "agentSessions")
					if !found {
						t.Fatal("owned installed session preimage missing")
					}
					if !reflect.DeepEqual(preimage.AgentSessions, sessions) {
						t.Fatal("trusted worker fixture changed native selector fields")
					}
					var runIDs []string
					if !strings.HasPrefix(name, "missing-run-") {
						payload, _ := json.Marshal(map[string]any{"chat": workerChatRunInfo{MessageID: runMessageID, Resume: &workerChatResume{}}})
						// A real queued run exercises cancellation and resume-state deletion.
						// Its unusable principal keeps the independent full-message
						// recovery reader/writer outside this header adapter's proof.
						run, inserted, err := repo.EnqueueAgentRun(t.Context(), store.AgentRunInput{WorkspaceKey: workspace, SessionID: runSessionID, Kind: store.AgentRunKindChat, Payload: payload})
						if err != nil || !inserted {
							t.Fatal(inserted, err)
						}
						runIDs = append(runIDs, run.ID)
					}
					if err := repo.SaveAgentSessionState(t.Context(), store.AgentSessionState{WorkspaceKey: workspace, SessionID: runSessionID, Data: []byte("retained resume state")}); err != nil {
						t.Fatal(err)
					}
					result := outcome{}
					ctx := store.WithSQLStatementObserver(t.Context(), func(query string) { result.Statements = append(result.Statements, query) })
					if name == "canceled" {
						var cancel context.CancelFunc
						ctx, cancel = context.WithCancel(ctx)
						cancel()
					}
					key := workspace
					if name == "missing-workspace" {
						key += "-missing"
					}
					service := &server{store: source}
					before := probe.WorkspaceMetadataDecodes()
					result.Swept = service.sweepWorkerChatWorkspace(ctx, key, now)
					for _, id := range runIDs {
						run, err := repo.AgentRun(t.Context(), id)
						if err != nil {
							t.Fatal(err)
						}
						result.Canceled = append(result.Canceled, run.Status == store.AgentRunCanceled && run.Error == workerChatNoWorker)
					}
					_, err := repo.AgentSessionStateFor(t.Context(), workspace, runSessionID)
					if err != nil && !errors.Is(err, store.ErrAgentRunNotFound) {
						t.Fatal(err)
					}
					result.StateGone = []bool{errors.Is(err, store.ErrAgentRunNotFound)}
					wantCancel := name == "queued-expired" || name == "deleted" || name == "duplicate-sessions" || name == "duplicate-messages" || name == "empty-owner" || name == "nul-owner"
					if driver := scaleP03DatabaseConfig(t).Driver; name == "nul-session" && driver != "" && driver != "sqlite" {
						// Only SQLite tells the session ID with NUL from the run's.
						// PostgreSQL text cannot hold NUL (every SQL argument drops
						// it) and MySQL's utf8mb4_0900_ai_ci ignores it, so the
						// literal run lookup finds the run and cancels it.
						wantCancel = true
					}
					if len(runIDs) != 0 && !reflect.DeepEqual(result.Canceled, []bool{wantCancel}) || result.StateGone[0] != wantCancel || result.Swept != 0 {
						t.Fatal("worker header selection changed queue/state effects", result)
					}
					if name == "canceled" {
						// The owned reader refuses a canceled construction before the
						// legacy loop would attempt its canceled SQL lookups.
						result.Statements = nil
						if got := service.sweepWorkerChatReplies(ctx, now); got != 0 {
							t.Fatal("canceled global wrapper continued", got)
						}
					}
					if source == probe && (probe.WorkspaceMetadataDecodes() != before || probe.MetadataWorkUsedForTesting() != 0) {
						t.Fatal("worker sweep hydrated metadata or leaked header ownership")
					}
					after, found := repo.WorkspaceMetadataFields(workspace, "agentSessions")
					if !found || !reflect.DeepEqual(after.AgentSessions, preimage.AgentSessions) {
						t.Fatal("header sweep changed native transcript data")
					}
					outputs = append(outputs, result)
				}
				if !reflect.DeepEqual(outputs[0], outputs[1]) {
					t.Fatal("literal/bounded worker header sweep differs", outputs[0], outputs[1])
				}
			})
		}
	}
}
