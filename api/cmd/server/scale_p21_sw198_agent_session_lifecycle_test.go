package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type scaleP21SW198State struct {
	Root, Rows [][]any
	Native     map[string]json.RawMessage
	Error      string
	Swept      int
}

func scaleP21SW198StateFor(t *testing.T, f scaleP21IssueUpdateFixture, repo *store.SQLiteStore) scaleP21SW198State {
	t.Helper()
	workspace := f.data.Workspace.URLKey
	got := scaleP21SW198State{
		Root:   f.rows(t, `SELECT workspace_id,data FROM workspace_states WHERE workspace_key=?`, workspace),
		Rows:   f.rows(t, `SELECT field,record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=? ORDER BY field,record_key`, workspace),
		Native: map[string]json.RawMessage{},
	}
	if !repo.BoundedMetadataEnabled() {
		data, found := repo.WorkspaceMetadataFields(workspace, "agentSessions", "agentActivities", "viewer", "users", "members")
		if !found {
			t.Fatal("owned session fixture missing")
		}
		for name, value := range map[string]any{
			"agentSessions": data.AgentSessions, "agentActivities": data.AgentActivities,
			"viewer": data.Viewer, "users": data.Users, "members": data.Members,
		} {
			raw, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			got.Native[name] = raw
		}
		return got
	}
	read, found, err := repo.BeginInstalledMetadataRead(t.Context(), workspace)
	if err != nil || !found {
		t.Fatal("owned installed session fixture", found, err)
	}
	defer read.Close()
	for _, field := range []string{"agentSessions", "agentActivities", "viewer", "users", "members"} {
		var raw strings.Builder
		if err := read.StreamFieldJSON(t.Context(), field, &raw); err != nil {
			t.Fatal(err)
		}
		got.Native[field] = json.RawMessage(raw.String())
	}
	return got
}

func scaleP21SW198ReloadSource(t *testing.T, f scaleP21IssueUpdateFixture, repo *store.SQLiteStore) {
	t.Helper()
	// Both loaders keep an exact installed copy. Advance only the owned
	// fixture's metadata revision so this test reaches the actual SQL path.
	f.exec(t, `UPDATE workspace_cache_revisions SET revision=revision+1 WHERE workspace_key=? AND kind='metadata'`, f.data.Workspace.URLKey)
	decodes := repo.WorkspaceMetadataDecodes()
	if err := repo.ReloadWorkspace(t.Context(), f.data.Workspace.URLKey); err != nil {
		t.Fatal("session source reload", err)
	}
	if !repo.BoundedMetadataEnabled() && repo.WorkspaceMetadataDecodes() <= decodes {
		t.Fatal("literal SQL-loaded source did not decode")
	}
	if repo.BoundedMetadataEnabled() && repo.WorkspaceMetadataDecodes() != decodes {
		t.Fatal("bounded source reload used full hydration")
	}
}

func scaleP21SW198Seed(t *testing.T, f scaleP21IssueUpdateFixture, owner domain.User, operation, shape string) (domain.AgentMessage, string) {
	t.Helper()
	at := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	message := scaleP21SW159Message("sw198-reply", at)
	if operation != "sweep" {
		message.ID = workerChatMessagePrefix + message.ID
		// The real fixture HTTP server has its own recovery ticker. These
		// CAS/marker cases deliberately have no durable worker run; keep
		// that unrelated ticker from expiring their synthetic reply.
		future := at.AddDate(10, 0, 0)
		message.UpdatedAt = &future
	} else {
		// Only the separate service under test should interrupt this
		// abandoned reply. The fixture's unrelated live ticker stays out.
		f.service.agentLiveReplies.Store(message.ID, &agentLiveReply{})
		t.Cleanup(func() { f.service.agentLiveReplies.Delete(message.ID) })
	}
	session := domain.AgentSession{
		ID: "sw198-session", SlugID: "sw198-old-slug", UserID: owner.ID,
		Title: "Native session", Messages: []domain.AgentMessage{message},
		IssueIDs: []string{f.triage.ID}, ProjectIDs: []string{}, SkillIDs: nil,
		CreatedAt: at, UpdatedAt: at, LastReadAt: agentReadStamp(at.Add(-time.Hour)),
		Feedback: []domain.AgentFeedback{{Rating: "positive", Comment: "keep", UpdatedAt: at}},
	}
	sessions := []domain.AgentSession{session}
	selected := session.ID
	switch shape {
	case "slug-shadow":
		shadow := session
		shadow.ID, shadow.Title = "sw198-slug-shadow", "first owned slug"
		sessions = []domain.AgentSession{shadow, session}
		selected = session.SlugID
	case "old-slug":
		selected = session.SlugID
	case "first-missing-message":
		empty := session
		empty.Messages = nil
		sessions = []domain.AgentSession{empty, session}
	case "first-finished-message":
		first := session
		first.Messages = slices.Clone(session.Messages)
		first.Messages[0].Status = ""
		sessions = []domain.AgentSession{first, session}
	case "duplicate-message":
		sessions[0].Messages = append(slices.Clone(session.Messages), message)
	case "deleted-first":
		deleted := session
		deleted.DeletedAt = &at
		sessions = []domain.AgentSession{deleted, session}
	case "foreign-owner-first":
		other := session
		other.UserID += "-foreign"
		sessions = []domain.AgentSession{other, session}
	case "empty-id":
		sessions[0].ID, selected = "", ""
	case "nul-id":
		sessions[0].ID, selected = session.ID+"\x00", session.ID+"\x00"
	case "empty-messages":
		sessions[0].Messages = []domain.AgentMessage{}
	case "nil-messages":
		sessions[0].Messages = nil
	case "already-read":
		sessions[0].LastReadAt = agentReadStamp(at)
	case "future-updated":
		future := time.Date(2036, 1, 1, 0, 0, 0, 123, time.UTC)
		sessions[0].UpdatedAt = future
	case "large-user-message", "large-user-message-duplicate-id":
		// User text stays inline under the real offload policy. This is a
		// supported transcript, not a custom type or an offload bypass.
		sessions[0].Messages = append([]domain.AgentMessage{{
			ID: "sw198-user", Role: "user", Content: strings.Repeat("user input ", 230000), CreatedAt: at,
		}}, sessions[0].Messages...)
		if shape == "large-user-message-duplicate-id" {
			sessions = append(sessions, sessions[0])
		}
	case "local-active":
		// The operation installs the same message in the local-reply map.
	}
	err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.sw198_seed", "", nil, func(data *domain.Bootstrap) error {
		data.AgentSessions = sessions
		data.AgentActivities = []domain.AgentActivity{{ID: "sw198-old-activity", SessionID: session.ID, Type: "response", Body: "keep old", CreatedAt: at, UpdatedAt: at}}
		// The read marker's unscoped display refresh must remain observable;
		// scoped worker writes must keep this stale member snapshot.
		for index := range data.Members {
			if data.Members[index].User.ID == owner.ID {
				data.Members[index].User.Name = "stale member snapshot"
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return message, selected
}

func scaleP21SW198Case(t *testing.T, f scaleP21IssueUpdateFixture, owner domain.User, key *domain.APIKey, operation, shape string) {
	t.Helper()
	t.Run("warm-source", func(t *testing.T) {
		scaleP21SW198CaseSource(t, f, owner, key, operation, shape, false)
	})
	t.Run("reloaded-source", func(t *testing.T) {
		scaleP21SW198CaseSource(t, f, owner, key, operation, shape, true)
	})
}

func scaleP21SW198CaseSource(t *testing.T, f scaleP21IssueUpdateFixture, owner domain.User, key *domain.APIKey, operation, shape string, reload bool) {
	t.Helper()
	var saved domain.AgentMessage
	var sessionID string
	f.scaleP21TriageWriterFixture.prepare = func(t *testing.T) {
		saved, sessionID = scaleP21SW198Seed(t, f, owner, operation, shape)
		if reload {
			scaleP21SW198ReloadSource(t, f, f.repository)
		}
	}
	defer func() { f.scaleP21TriageWriterFixture.prepare = nil }()
	name := operation + "/" + shape
	call := func(service *server) *httptest.ResponseRecorder {
		if reload && service.store.BoundedMetadataEnabled() {
			// Reuse the owned view instead of retaining a fresh complete
			// test generation for every matrix row. A distinct SQL stamp
			// forces the actual bounded loader, rather than its exact-copy
			// early exit or the test-view constructor's owner capture.
			scaleP21SW198ReloadSource(t, f, service.store)
		}
		ctx := f.literalRequest(owner, key, nil).Context()
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		if shape == "cancel-before" {
			cancel()
		}
		cancelled := false
		if shape == "cancel-persist" {
			ctx = store.WithSQLStatementObserver(ctx, func(query string) {
				if strings.Contains(strings.ToLower(query), "insert into domain_events") {
					cancelled = true
					cancel()
				}
			})
		}
		var saveErr error
		swept := 0
		workspace := f.data.Workspace.URLKey
		if shape == "foreign-workspace" {
			workspace += "-missing"
		}
		if shape == "missing-session" {
			sessionID += "-missing"
		}
		switch operation {
		case "marker":
			service.chatDMMarkSessionRead(ctx, workspace, owner.ID, sessionID)
		case "worker", "terminal":
			reply := &workerChatReply{
				s: service, owner: owner,
				run:  store.AgentRun{WorkspaceKey: workspace, SessionID: sessionID, ID: "sw198-run"},
				info: workerChatRunInfo{MessageID: saved.ID},
			}
			draft, err := cloneAgentMessage(saved)
			if err != nil {
				t.Fatal(err)
			}
			draft.Content, draft.Parts[0].Text = "saved replacement", "new text"
			if shape == "stale-cursor" {
				saved.WorkerCursor = &domain.AgentWorkerCursor{Run: "stale", Attempt: 2, Seq: 1}
			}
			if shape == "stale-time" {
				stale := saved.UpdatedAt.Add(-time.Nanosecond)
				saved.UpdatedAt = &stale
			}
			next := &workerChatDraft{c: reply, message: draft}
			if operation == "terminal" {
				next.finished = true
				next.message.UpdatedAt, next.message.Status = nil, ""
				next.message.Outcome = &domain.AgentReplyOutcome{Status: agentOutcomeCompleted}
				activity := agentReplyActivity(sessionID, next.message, nil)
				// Keep an existing generated activity stable across source modes.
				activity.ID = "sw198-terminal-activity"
				next.activity = &activity
			}
			saveErr = reply.save(ctx, saved, next)
		case "sweep":
			if shape == "local-active" {
				service.agentLiveReplies.Store(saved.ID, &agentLiveReply{})
				defer service.agentLiveReplies.Delete(saved.ID)
			}
			if shape == "cancel-before" {
				// The production global wrapper checks cancellation before
				// entering each workspace, including the extracted old loop.
				swept = service.sweepAgentLiveReplies(ctx, time.Now().Add(time.Hour))
			} else {
				swept = service.sweepAgentLiveWorkspace(ctx, workspace, time.Now().Add(time.Hour))
			}
		}
		if shape == "cancel-persist" && !cancelled {
			t.Fatal("cancellation did not reach the event insert")
		}
		if strings.HasPrefix(shape, "large-user-message") && service.store.BoundedMetadataEnabled() &&
			(errors.Is(saveErr, store.ErrMetadataWorkBudget) || errors.Is(saveErr, store.ErrBoundedMetadataCoverage)) {
			t.Fatal("legal large session refused", saveErr)
		}
		result := scaleP21SW198StateFor(t, f, service.store)
		if shape == "future-updated" {
			var sessions []domain.AgentSession
			if err := json.Unmarshal(result.Native["agentSessions"], &sessions); err != nil {
				t.Fatal(err)
			}
			if len(sessions) != 1 || sessions[0].LastReadAt == nil || !sessions[0].LastReadAt.Equal(sessions[0].UpdatedAt) {
				t.Fatal("future read marker did not use the exact session UpdatedAt")
			}
		}
		result.Swept = swept
		if saveErr != nil {
			result.Error = saveErr.Error()
		}
		w := httptest.NewRecorder()
		if err := json.NewEncoder(w).Encode(result); err != nil {
			t.Fatal(err)
		}
		return w
	}
	// Native warm values and SQL-loaded values can legitimately differ
	// (NUL persistence and offloaded-body references). Compare both against
	// their matching literal oracle instead of normalizing that difference.
	var outcomes []scaleP21IssueUpdateOutcome
	var expected string
	for index, source := range []string{"legacy", "warm"} {
		got, started := f.run(t, source, call)
		outcomes = append(outcomes, got)
		normalized := scaleP21IssueUpdateNormalize(t, got, started)
		if index == 0 {
			expected = normalized
		} else if normalized != expected {
			t.Fatalf("%s source=%s reload=%t changed: %s", name, source, reload, scaleP21M2Diff(expected, normalized))
		}
	}
	if strings.HasPrefix(shape, "large-user-message") {
		expected := 1
		if shape == "large-user-message-duplicate-id" && operation == "sweep" {
			expected = 2
		}
		for _, outcome := range outcomes {
			if len(outcome.State.Events) != expected {
				t.Fatal("legal large session changed its commit count", len(outcome.State.Events), expected)
			}
		}
	}
}

// The lifecycle differential and permission matrix run one top-level test
// per operation: each case is a complete legacy/warm and warm/reloaded
// differential (about 1-2 s without the race detector), and a single test of
// all four operations ran 13 (differential) and 22 (matrix) minutes under
// the race detector on CI, too long for one shard.
func scaleP21SW198Differential(t *testing.T, operation string) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21IssueUpdateSetup(t)
	for _, shape := range []string{
		"native", "first-missing-message", "first-finished-message", "duplicate-message", "deleted-first",
		"foreign-owner-first", "empty-id", "nul-id", "empty-messages", "nil-messages",
		"foreign-workspace", "cancel-before", "cancel-persist",
	} {
		if shape == "cancel-persist" && operation == "worker" {
			// worker.save deliberately detaches its input context.
			continue
		}
		t.Run(operation+"/"+shape, func(t *testing.T) {
			scaleP21SW198Case(t, f, f.data.Viewer, nil, operation, shape)
		})
	}
	for _, test := range []struct{ operation, shape string }{
		{"marker", "already-read"}, {"marker", "future-updated"}, {"worker", "slug-shadow"},
		{"worker", "stale-time"}, {"worker", "stale-cursor"}, {"sweep", "local-active"},
		{"marker", "missing-session"}, {"worker", "missing-session"},
		{"marker", "old-slug"}, {"worker", "old-slug"},
	} {
		if test.operation != operation {
			continue
		}
		t.Run(test.operation+"/"+test.shape, func(t *testing.T) {
			scaleP21SW198Case(t, f, f.data.Viewer, nil, test.operation, test.shape)
		})
	}
}

func TestScaleP21SW198AgentSessionLifecycleDifferentialMarker(t *testing.T) {
	scaleP21SW198Differential(t, "marker")
}

func TestScaleP21SW198AgentSessionLifecycleDifferentialWorker(t *testing.T) {
	scaleP21SW198Differential(t, "worker")
}

func TestScaleP21SW198AgentSessionLifecycleDifferentialTerminal(t *testing.T) {
	scaleP21SW198Differential(t, "terminal")
}

func TestScaleP21SW198AgentSessionLifecycleDifferentialSweep(t *testing.T) {
	scaleP21SW198Differential(t, "sweep")
}

func scaleP21SW198Matrix(t *testing.T, operation string) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21IssueUpdateSetup(t)
	f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
		t.Run(operation, func(t *testing.T) {
			scaleP21SW198Case(t, f, user, key, operation, "native")
		})
	})
}

func TestScaleP21SW198AgentSessionLifecycleMatrixMarker(t *testing.T) {
	scaleP21SW198Matrix(t, "marker")
}

func TestScaleP21SW198AgentSessionLifecycleMatrixWorker(t *testing.T) {
	scaleP21SW198Matrix(t, "worker")
}

func TestScaleP21SW198AgentSessionLifecycleMatrixTerminal(t *testing.T) {
	scaleP21SW198Matrix(t, "terminal")
}

func TestScaleP21SW198AgentSessionLifecycleMatrixSweep(t *testing.T) {
	scaleP21SW198Matrix(t, "sweep")
}

// This is an activation gate, not an expected-budget-refusal test. Keep its
// consumers pending until the complete native/row ownership plan passes.
func TestScaleP21SW198LargeSessionLifecycleDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21IssueUpdateSetup(t)
	for _, operation := range []string{"marker", "worker", "terminal", "sweep"} {
		for _, shape := range []string{"large-user-message", "large-user-message-duplicate-id"} {
			t.Run(operation+"/"+shape, func(t *testing.T) {
				scaleP21SW198Case(t, f, f.data.Viewer, nil, operation, shape)
			})
		}
	}
}

func TestScaleP21SW198SessionLifecycleConcurrentCAS(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21IssueUpdateSetup(t)
	owner := f.data.Viewer
	var saved domain.AgentMessage
	var sessionID string
	for _, reload := range []bool{false, true} {
		f.scaleP21TriageWriterFixture.prepare = func(t *testing.T) {
			saved, sessionID = scaleP21SW198Seed(t, f, owner, "worker", "native")
			if reload {
				scaleP21SW198ReloadSource(t, f, f.repository)
			}
		}
		var expected string
		for index, source := range []string{"legacy", "warm"} {
			got, started := f.run(t, source, func(service *server) *httptest.ResponseRecorder {
				if reload && service.store.BoundedMetadataEnabled() {
					scaleP21SW198ReloadSource(t, f, service.store)
				}
				reply := &workerChatReply{
					s: service, owner: owner,
					run:  store.AgentRun{WorkspaceKey: f.data.Workspace.URLKey, SessionID: sessionID, ID: "sw198-run"},
					info: workerChatRunInfo{MessageID: saved.ID},
				}
				results := make(chan error, 2)
				start := make(chan struct{})
				for range 2 {
					draft, err := cloneAgentMessage(saved)
					if err != nil {
						t.Fatal(err)
					}
					draft.Content, draft.Parts[0].Text = "one winning update", "one winning update"
					go func(message domain.AgentMessage) {
						<-start
						results <- reply.save(t.Context(), saved, &workerChatDraft{c: reply, message: message})
					}(draft)
				}
				close(start)
				accepted, stale := 0, 0
				for range 2 {
					switch err := <-results; {
					case err == nil:
						accepted++
					case errors.Is(err, errWorkerChatStale):
						stale++
					default:
						t.Fatal("concurrent session save changed error", err)
					}
				}
				if accepted != 1 || stale != 1 {
					t.Fatal("session CAS accepted multiple saves", accepted, stale)
				}
				w := httptest.NewRecorder()
				if err := json.NewEncoder(w).Encode(scaleP21SW198StateFor(t, f, service.store)); err != nil {
					t.Fatal(err)
				}
				return w
			})
			if len(got.State.Events) != 1 {
				t.Fatal("concurrent CAS published multiple mutations", len(got.State.Events))
			}
			normalized := scaleP21IssueUpdateNormalize(t, got, started)
			if index == 0 {
				expected = normalized
			} else if normalized != expected {
				t.Fatalf("concurrent session CAS reload=%t differs: %s", reload, scaleP21M2Diff(expected, normalized))
			}
		}
	}
	f.scaleP21TriageWriterFixture.prepare = nil
}

func TestScaleP21SW198SessionLifecycleRevokedMembership(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21IssueUpdateSetup(t)
	member := f.principals[2].user
	f.exec(t, `UPDATE workspace_memberships SET status='suspended' WHERE workspace_id=? AND user_id=?`, f.data.Workspace.ID, member.ID)
	defer f.exec(t, `UPDATE workspace_memberships SET status='active' WHERE workspace_id=? AND user_id=?`, f.data.Workspace.ID, member.ID)
	for _, operation := range []string{"marker", "worker", "terminal", "sweep"} {
		t.Run(operation, func(t *testing.T) {
			// These trusted maintenance callbacks never refused suspended
			// membership. Keep queued work's exact existing persistence;
			// route/run authorization remains in its reviewed outer boundary.
			scaleP21SW198Case(t, f, member, nil, operation, "native")
		})
	}
}
