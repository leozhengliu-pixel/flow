package main

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func waitForLoopReply(t *testing.T, handler http.Handler, loopID, runID string, count int) domain.LoopRun {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		run := requestJSON[domain.LoopRun](t, handler, http.MethodGet, "/api/loops/"+loopID+"/runs/"+runID, nil, http.StatusOK)
		if len(run.Replies) == count && run.Replies[count-1].Status != "running" {
			return run
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("loop reply did not finish")
	return domain.LoopRun{}
}

func TestLoopRunRepliesContinueTheConversation(t *testing.T) {
	provider := &fakeLoopProvider{replies: []string{"Posted the weekly summary.", "It covered 3 bugs.", "Done."}}
	_, handler := newLoopTestServer(t, provider)
	loop := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops", map[string]any{"name": "Weekly summary", "instructions": "Summarize open bugs.", "status": "published"}, http.StatusCreated)
	started := requestJSON[domain.LoopRun](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs", nil, http.StatusAccepted)
	waitForLoopRun(t, handler, loop.ID)

	requestJSON[any](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs/"+started.ID+"/replies", map[string]any{"body": "   "}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs/nope/replies", map[string]any{"body": "Hi"}, http.StatusNotFound)
	accepted := requestJSON[domain.LoopRun](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs/"+started.ID+"/replies", map[string]any{"body": "How many bugs?"}, http.StatusAccepted)
	if len(accepted.Replies) != 1 || accepted.Replies[0].Body != "How many bugs?" || accepted.Replies[0].Status != "running" {
		t.Fatalf("accepted reply = %#v", accepted.Replies)
	}
	run := waitForLoopReply(t, handler, loop.ID, started.ID, 1)
	if reply := run.Replies[0]; reply.Status != "completed" || reply.Output != "It covered 3 bugs." || reply.FinishedAt == nil {
		t.Fatalf("reply = %#v", reply)
	}
	if run.Output != "Posted the weekly summary." || run.Status != "completed" {
		t.Fatalf("the run itself changed: %#v", run)
	}
	requestJSON[any](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs/"+started.ID+"/replies", map[string]any{"body": "Thanks"}, http.StatusAccepted)
	waitForLoopReply(t, handler, loop.ID, started.ID, 2)

	provider.mu.Lock()
	defer provider.mu.Unlock()
	last := provider.inputs[len(provider.inputs)-1]
	for _, want := range []string{"Summarize open bugs.", "Posted the weekly summary.", "How many bugs?", "It covered 3 bugs.", "Thanks"} {
		if !strings.Contains(last, want) {
			t.Fatalf("reply prompt is missing %q: %s", want, last)
		}
	}
}

func TestLoopEditPolicy(t *testing.T) {
	data := &domain.Bootstrap{
		ViewerRole:  "member",
		TeamMembers: []domain.TeamMember{{TeamID: "team-1", UserID: "lead", Role: "owner"}, {TeamID: "team-1", UserID: "member", Role: "member"}},
	}
	loop := domain.Loop{ID: "loop-1", Level: "team", TeamID: "team-1", OwnerID: "owner"}
	cases := []struct {
		policy, user string
		want         bool
	}{
		{"", "member", true},
		{"teamOwners", "member", false},
		{"teamOwners", "lead", true},
		{"teamOwners", "owner", true},
		{"owner", "lead", false},
		{"owner", "owner", true},
	}
	for _, item := range cases {
		loop.EditPolicy = item.policy
		if got := canEditLoop(data, loop, item.user); got != item.want {
			t.Errorf("policy %q user %q = %v, want %v", item.policy, item.user, got, item.want)
		}
	}
	workspaceLoop := domain.Loop{ID: "loop-2", Level: "workspace", OwnerID: "owner", EditPolicy: "teamOwners"}
	if canEditLoop(data, workspaceLoop, "lead") {
		t.Error("team owners of another team must not edit a workspace loop limited to owners")
	}
	data.ViewerRole = "admin"
	loop.EditPolicy = "owner"
	if !canEditLoop(data, loop, "member") {
		t.Error("workspace admins can always edit")
	}
}

func TestLoopEditPolicyIsValidatedAndEnforced(t *testing.T) {
	srv, handler := newLoopTestServer(t, &fakeLoopProvider{})
	loop := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops", map[string]any{"name": "Weekly summary", "instructions": "Summarize open bugs.", "status": "published"}, http.StatusCreated)
	requestJSON[any](t, handler, http.MethodPatch, "/api/loops/"+loop.ID, map[string]any{"editPolicy": "everyone"}, http.StatusBadRequest)
	updated := requestJSON[domain.Loop](t, handler, http.MethodPatch, "/api/loops/"+loop.ID, map[string]any{"editPolicy": "owner"}, http.StatusOK)
	if updated.EditPolicy != "owner" || updated.Version != loop.Version {
		t.Fatalf("editPolicy = %q version %d → %d", updated.EditPolicy, loop.Version, updated.Version)
	}
	reset := requestJSON[domain.Loop](t, handler, http.MethodPatch, "/api/loops/"+loop.ID, map[string]any{"editPolicy": "all"}, http.StatusOK)
	if reset.EditPolicy != "" {
		t.Fatalf("all is stored as the default, got %q", reset.EditPolicy)
	}
	// With authentication, a member the policy leaves out is refused.
	requestJSON[any](t, handler, http.MethodPatch, "/api/loops/"+loop.ID, map[string]any{"editPolicy": "owner", "ownerId": "someone-else"}, http.StatusBadRequest)
	srv.authDisabled = false
	data := &domain.Bootstrap{ViewerRole: "member", Viewer: domain.User{ID: "member"}}
	if err := srv.checkLoopEditor(data, domain.Loop{OwnerID: "owner", EditPolicy: "owner"}); err != errLoopEditForbidden {
		t.Fatalf("checkLoopEditor = %v", err)
	}
	if err := srv.checkLoopEditor(data, domain.Loop{OwnerID: "member", EditPolicy: "owner"}); err != nil {
		t.Fatalf("the loop owner can edit: %v", err)
	}
}

func TestAuditLogListsLoopChanges(t *testing.T) {
	_, handler := newLoopTestServer(t, &fakeLoopProvider{})
	loop := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops", map[string]any{"name": "Weekly summary", "instructions": "Summarize open bugs.", "status": "published"}, http.StatusCreated)
	requestJSON[any](t, handler, http.MethodPatch, "/api/loops/"+loop.ID, map[string]any{"enabled": false}, http.StatusOK)
	entries := requestJSON[[]domain.AuditLogEntry](t, handler, http.MethodGet, "/api/workspace/audit-log", nil, http.StatusOK)
	if len(entries) == 0 || entries[0].ResourceType != "loop" || entries[0].ResourceID != loop.ID || entries[0].Action != "updated" {
		t.Fatalf("audit log = %#v", entries)
	}
}
