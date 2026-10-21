package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"flow/api/internal/coding"
	"flow/api/internal/domain"
)

// A coding session that reuses a delegation task its agent already ended:
// the approval request reaches the thread, approving through the API answers
// with the approved session (also when its notice cannot be posted), and
// retried or concurrent approvals record one decision.
func TestCodingApprovalOnEndedDelegationAnchor(t *testing.T) {
	s, handler := codingTestServer(t, "")
	requestJSON[codingSettingsView](t, handler, http.MethodPatch, "/api/coding/settings", map[string]any{"enabled": true}, http.StatusOK)
	ctx := t.Context()
	metadata, _ := s.store.WorkspaceSettingsMetadata("")
	workspace := metadata.Workspace.URLKey
	data, _ := s.store.WorkspaceMetadata(workspace)
	viewer := data.Viewer.ID
	agent := s.builtinAgentUserID(ctx, workspace)
	if viewer == "" || agent == "" {
		t.Fatalf("fixture: viewer=%q agent=%q", viewer, agent)
	}
	notifier := &codingTriggerNotifier{s: s}
	activities := func(t *testing.T, anchorID, key string) int {
		t.Helper()
		items, err := s.store.AgentActivities(ctx, workspace, anchorID, "")
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, item := range items {
			if item.Metadata["codingKey"] == key {
				count++
			}
		}
		return count
	}
	// endedDelegation creates a delegation task and ends it the way its agent would.
	endedDelegation := func(t *testing.T, id string, ending domain.AgentActivity) {
		t.Helper()
		task, err := s.store.CreateAgentInitiatedTask(ctx, domain.AgentTask{ID: id, WorkspaceKey: workspace, IssueID: "issue_" + id, AppUserID: agent, CreatorID: viewer})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.store.AppendAgentActivity(ctx, workspace, id, task.Version, ending); err != nil {
			t.Fatal(err)
		}
	}
	start := func(t *testing.T, anchorID string) domain.CodingSession {
		t.Helper()
		session, created, err := s.coding.service.Start(ctx, coding.StartInput{
			WorkspaceKey: workspace, Source: domain.CodingSourceAgentChat, AnchorID: anchorID, Prompt: "Fix the export",
			CreatorID: viewer, RequireApproval: true, IdempotencyKey: "delegation:" + anchorID,
		})
		if err != nil || !created || session.Phase != domain.CodingPhaseAwaitingApproval || session.AgentSessionID != anchorID {
			t.Fatalf("start: %+v created=%v err=%v", session, created, err)
		}
		request := coding.TriggerStartRequest{Title: "Export", Prompt: "Fix the export", ExternalSource: "integration:email"}
		if err := notifier.ApprovalRequested(ctx, session, request); err != nil {
			t.Fatalf("approval request on the reused anchor: %v", err)
		}
		// Notifications are keyed: repeating one posts nothing new.
		if err := notifier.ApprovalRequested(ctx, session, request); err != nil || activities(t, anchorID, "approval") != 1 {
			t.Fatalf("repeated approval request: %v (%d)", err, activities(t, anchorID, "approval"))
		}
		if task, _ := s.store.AgentTask(ctx, workspace, anchorID); task.Status != "awaitingInput" {
			t.Fatalf("anchor waiting for the approval: %+v", task)
		}
		return session
	}
	// approve answers POST /approve; it is safe to call from goroutines.
	approve := func(id string) (int, domain.CodingSession, string) {
		request := httptest.NewRequest(http.MethodPost, "/api/coding/sessions/"+id+"/approve", strings.NewReader(`{}`))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		var session domain.CodingSession
		_ = json.Unmarshal(recorder.Body.Bytes(), &session)
		return recorder.Code, session, recorder.Body.String()
	}

	t.Run("completed delegation, concurrent and retried approvals", func(t *testing.T) {
		endedDelegation(t, "task_complete", domain.AgentActivity{ActorID: agent, Type: "response", Body: "I started a coding session."})
		session := start(t, "task_complete")
		var wg sync.WaitGroup
		codes, results, bodies := make([]int, 4), make([]domain.CodingSession, 4), make([]string, 4)
		for i := range codes {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				codes[i], results[i], bodies[i] = approve(session.ID)
			}(i)
		}
		wg.Wait()
		for i := range codes {
			if codes[i] != http.StatusOK || results[i].ApprovalState != coding.ApprovalApproved || results[i].Phase == domain.CodingPhaseAwaitingApproval {
				t.Fatalf("approval %d answered %d: %s", i, codes[i], bodies[i])
			}
		}
		if code, again, body := approve(session.ID); code != http.StatusOK || again.ApprovalState != coding.ApprovalApproved {
			t.Fatalf("retried approval answered %d: %s", code, body)
		}
		if got := activities(t, "task_complete", "approval:approved"); got != 1 {
			t.Fatalf("approval decisions on the anchor = %d", got)
		}
		if task, _ := s.store.AgentTask(ctx, workspace, "task_complete"); task.Status != "active" {
			t.Fatalf("anchor after the approval: %+v", task)
		}
	})

	t.Run("errored delegation, decision notice refused after a stop", func(t *testing.T) {
		endedDelegation(t, "task_error", domain.AgentActivity{ActorID: agent, Type: "error", Body: "Execution was interrupted."})
		session := start(t, "task_error")
		// Someone stops the thread outside the session (no session stop):
		// the decision notice cannot be posted, yet the approval committed.
		task, _ := s.store.AgentTask(ctx, workspace, "task_error")
		if _, err := s.store.AppendAgentActivity(ctx, workspace, "task_error", task.Version, domain.AgentActivity{ActorID: viewer, Type: "prompt", Signal: "stop"}); err != nil {
			t.Fatal(err)
		}
		before, _ := s.store.AgentTask(ctx, workspace, "task_error")
		if code, approved, body := approve(session.ID); code != http.StatusOK || approved.ApprovalState != coding.ApprovalApproved {
			t.Fatalf("approval answered %d: %s", code, body)
		}
		after, _ := s.store.AgentTask(ctx, workspace, "task_error")
		if after.Status != "canceled" || after.Version != before.Version {
			t.Fatalf("a stopped anchor was reopened: %+v", after)
		}
		if err := notifier.ApprovalResolved(ctx, session, coding.ApprovalApproved, viewer); !errors.Is(err, errCodingAnchorRefused) {
			t.Fatalf("decision on a stopped anchor: %v", err)
		}
	})

	t.Run("revoked delegation", func(t *testing.T) {
		endedDelegation(t, "task_revoked", domain.AgentActivity{Type: "canceled", Body: "Application access was revoked."})
		_, _, err := s.coding.service.Start(ctx, coding.StartInput{
			WorkspaceKey: workspace, Source: domain.CodingSourceAgentChat, AnchorID: "task_revoked", Prompt: "Fix the export",
			CreatorID: viewer, IdempotencyKey: "delegation:task_revoked",
		})
		if !errors.Is(err, domain.ErrCodingAnchorRevoked) {
			t.Fatalf("start on a revoked delegation: %v", err)
		}
		if status, code := codingErrorStatus(err); status != http.StatusForbidden || code != string(domain.CodingErrPermissionDenied) {
			t.Fatalf("revoked delegation answers %d %s", status, code)
		}
		if _, err := s.coding.service.GetByAnchor(ctx, workspace, "task_revoked"); err == nil {
			t.Fatal("a session was created on a revoked delegation")
		}
	})
}
