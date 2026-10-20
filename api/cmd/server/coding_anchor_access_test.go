package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/coding"
	"flow/api/internal/domain"
)

// codingAccessFixture is a coding-enabled workspace with the fixture's owner
// (usr_admin) and member (usr_member), each with an API key, and
// authentication on.
type codingAccessFixture struct {
	t         *testing.T
	s         *server
	handler   http.Handler
	workspace domain.Bootstrap
	secrets   map[string]string
}

func newCodingAccessFixture(t *testing.T) *codingAccessFixture {
	t.Helper()
	s, handler := codingTestServer(t, "")
	requestJSON[codingSettingsView](t, handler, http.MethodPatch, "/api/coding/settings", map[string]any{"enabled": true}, http.StatusOK)
	data := s.store.Bootstrap()
	f := &codingAccessFixture{t: t, s: s, handler: handler, workspace: data, secrets: map[string]string{}}
	// The fixture makes both people admins; usr_member is a plain member here.
	if err := s.store.UpdateMemberRole(t.Context(), data.Workspace.ID, "usr_member", "member"); err != nil {
		t.Fatal(err)
	}
	if role, _, err := s.store.WorkspaceRole(t.Context(), data.Workspace.ID, "usr_admin"); err != nil || !workspaceAdminRole(role) {
		t.Fatalf("usr_admin role %q %v", role, err)
	}
	for _, user := range []string{"usr_admin", "usr_member"} {
		secret := "coding-access-" + user
		key := domain.APIKey{ID: "key_" + user, SecretHash: secretHash(secret), CreatorID: user, Scopes: []string{"read", "write"}, CreatedAt: time.Now().UTC()}
		if err := s.store.MutateWorkspace(t.Context(), data.Workspace.URLKey, "api_key.created", key.ID, nil, func(data *domain.Bootstrap) error {
			data.APIKeys = append(data.APIKeys, key)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		f.secrets[user] = secret
	}
	return f
}

// start creates a session the way a trigger does (the anchor in the same
// transaction).
func (f *codingAccessFixture) start(input coding.StartInput) domain.CodingSession {
	f.t.Helper()
	input.WorkspaceKey = f.workspace.Workspace.URLKey
	if input.Prompt == "" {
		input.Prompt = "Fix the flaky test"
	}
	session, _, err := f.s.coding.service.Start(context.Background(), input)
	if err != nil {
		f.t.Fatal(err)
	}
	return session
}

// call makes a request as a user with authentication on.
func (f *codingAccessFixture) call(user, method, path string) *httptest.ResponseRecorder {
	f.t.Helper()
	f.s.authDisabled = false
	defer func() { f.s.authDisabled = true }()
	var body *strings.Reader
	if method == http.MethodPost {
		body = strings.NewReader(`{}`)
	} else {
		body = strings.NewReader("")
	}
	request := httptest.NewRequest(method, path, body)
	request.Header.Set("Authorization", "Bearer "+f.secrets[user])
	request.Header.Set("X-Workspace-Key", f.workspace.Workspace.URLKey)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	f.handler.ServeHTTP(recorder, request)
	return recorder
}

func (f *codingAccessFixture) expect(user, method, path string, want int) *httptest.ResponseRecorder {
	f.t.Helper()
	recorder := f.call(user, method, path)
	if recorder.Code != want {
		f.t.Fatalf("%s %s as %s = %d, want %d: %s", method, path, user, recorder.Code, want, recorder.Body.String())
	}
	return recorder
}

// reads checks the session, its thread (the anchor's messages), its
// artifacts, its diff and the list for one user.
func (f *codingAccessFixture) reads(user string, session domain.CodingSession, allowed bool) {
	f.t.Helper()
	want := http.StatusNotFound
	if allowed {
		want = http.StatusOK
	}
	f.expect(user, http.MethodGet, "/api/coding/sessions/"+session.ID, want)
	f.expect(user, http.MethodGet, "/api/agent-tasks/"+session.AgentSessionID, want)
	f.expect(user, http.MethodGet, "/api/coding/sessions/"+session.ID+"/diff", want)
	artifacts := f.expect(user, http.MethodGet, "/api/coding/sessions/"+session.ID+"/artifacts", want)
	if allowed && strings.TrimSpace(artifacts.Body.String()) != "[]" {
		f.t.Fatalf("a session without artifacts lists []: %s", artifacts.Body.String())
	}
	if !allowed && strings.Contains(artifacts.Body.String(), "not enabled") {
		f.t.Fatalf("a refused read is not the runtime-disabled answer: %s", artifacts.Body.String())
	}
	var list []domain.CodingSession
	if err := json.Unmarshal(f.expect(user, http.MethodGet, "/api/coding/sessions", http.StatusOK).Body.Bytes(), &list); err != nil {
		f.t.Fatal(err)
	}
	listed := slices.ContainsFunc(list, func(item domain.CodingSession) bool { return item.ID == session.ID })
	if listed != allowed {
		f.t.Fatalf("%s lists session %s = %v, want %v", user, session.ID, listed, allowed)
	}
}

// A standalone agent chat session (no issue) failed before it ran: its
// creator and the workspace admins read the session, its messages and its
// (empty) artifacts; another member does not, and cannot act on it.
func TestCodingStandaloneChatSessionAccess(t *testing.T) {
	f := newCodingAccessFixture(t)
	mine := f.start(coding.StartInput{Source: domain.CodingSourceAgentChat, CreatorID: "usr_member"})
	task, err := f.s.store.AgentTask(t.Context(), f.workspace.Workspace.URLKey, mine.AgentSessionID)
	if err != nil || task.ResourceType != "agentChat" || task.IssueID != "" {
		t.Fatalf("anchor: %+v %v", task, err)
	}
	f.reads("usr_member", mine, true) // the creator
	f.reads("usr_admin", mine, true)  // a workspace owner
	f.expect("usr_member", http.MethodPost, "/api/coding/sessions/"+mine.ID+"/stop", http.StatusOK)

	theirs := f.start(coding.StartInput{Source: domain.CodingSourceAgentChat, CreatorID: "usr_admin"})
	f.reads("usr_admin", theirs, true)
	f.reads("usr_member", theirs, false) // another member's private chat session
	for _, action := range []string{"stop", "prompt", "restart", "dismiss", "subscription"} {
		f.expect("usr_member", http.MethodPost, "/api/coding/sessions/"+theirs.ID+"/"+action, http.StatusNotFound)
	}
	f.expect("usr_member", http.MethodPost, "/api/agent-tasks/"+theirs.AgentSessionID+"/activities", http.StatusNotFound)

	// A guest reads only their own session.
	if err := f.s.store.UpdateMemberRole(t.Context(), f.workspace.Workspace.ID, "usr_member", "guest"); err != nil {
		t.Fatal(err)
	}
	f.reads("usr_member", mine, true)
	f.reads("usr_member", theirs, false)
}

// An issue session follows the issue: members who see the issue read it.
func TestCodingIssueSessionAccess(t *testing.T) {
	f := newCodingAccessFixture(t)
	issue := requestJSON[domain.Issue](t, f.handler, http.MethodPost, "/api/issues", map[string]any{"title": "Flaky test", "teamId": f.workspace.Teams[0].ID}, http.StatusCreated)
	session := f.start(coding.StartInput{Source: domain.CodingSourceIssue, IssueID: issue.ID, CreatorID: "usr_admin"})
	f.reads("usr_admin", session, true)
	f.reads("usr_member", session, true)
	if err := f.s.store.UpdateMemberRole(t.Context(), f.workspace.Workspace.ID, "usr_member", "guest"); err != nil {
		t.Fatal(err)
	}
	f.reads("usr_member", session, false)
}

// "Configure/Fix with agent" sessions are admin settings; pull request and
// loop sessions without an issue are workspace-wide like reviews and loops.
func TestCodingEnvironmentAndPullRequestSessionAccess(t *testing.T) {
	f := newCodingAccessFixture(t)
	environment := f.start(coding.StartInput{Source: domain.CodingSourceEnvironmentSetup, SourceID: "env_1", ResourceType: codingEnvironmentAnchorResource, ResourceID: "env_1", CreatorID: "usr_admin"})
	f.reads("usr_admin", environment, true)
	f.reads("usr_member", environment, false)

	review := f.start(coding.StartInput{Source: domain.CodingSourcePullRequest, SourceID: "review_1", ResourceType: "pullRequest", ResourceID: "review_1", CreatorID: "usr_admin"})
	f.reads("usr_member", review, true)
	loop := f.start(coding.StartInput{Source: domain.CodingSourceLoop, SourceID: "run_1", ResourceType: "loop", CreatorID: "usr_admin", LoopID: "loop_1"})
	f.reads("usr_member", loop, true)
	external := f.start(coding.StartInput{Source: domain.CodingSourceExternal, SourceID: "mail_1", ResourceType: domain.CodingSourceExternal, CreatorID: "usr_admin"})
	f.reads("usr_member", external, false)
	f.reads("usr_admin", external, true)
	if err := f.s.store.UpdateMemberRole(t.Context(), f.workspace.Workspace.ID, "usr_member", "guest"); err != nil {
		t.Fatal(err)
	}
	f.reads("usr_member", review, false)
}

func TestCodingAnchorAccessRule(t *testing.T) {
	chat := domain.AgentTask{ResourceType: "agentChat", CreatorID: "creator", CodingSessionID: "cs"}
	for _, test := range []struct {
		viewer domain.User
		role   string
		task   domain.AgentTask
		ok     bool
	}{
		{domain.User{ID: "creator"}, "member", chat, true},
		{domain.User{ID: "creator"}, "guest", chat, true},
		{domain.User{ID: "admin"}, "admin", chat, true},
		{domain.User{ID: "owner"}, "owner", chat, true},
		{domain.User{ID: "other"}, "member", chat, false},
		{domain.User{ID: "other"}, "", chat, false},
		{domain.User{ID: "agent", App: true}, "app", chat, true},
		{domain.User{ID: "other"}, "member", domain.AgentTask{ResourceType: "pullRequest", CreatorID: "creator", CodingSessionID: "cs"}, true},
		{domain.User{ID: "other"}, "guest", domain.AgentTask{ResourceType: "loop", CreatorID: "creator", CodingSessionID: "cs"}, false},
		{domain.User{ID: "other"}, "member", domain.AgentTask{ResourceType: "external", CreatorID: "creator", CodingSessionID: "cs"}, false},
	} {
		if err := codingAnchorAccess(test.viewer, test.role, test.task); (err == nil) != test.ok {
			t.Errorf("%s (%s) on %s: %v, want ok=%v", test.viewer.ID, test.role, test.task.ResourceType, err, test.ok)
		}
	}
	if codingAnchorResource(domain.AgentTask{ResourceType: "document", CodingSessionID: "cs"}) || !codingAnchorResource(domain.AgentTask{ResourceType: "agentChat"}) || codingAnchorResource(domain.AgentTask{ResourceType: "pullRequest"}) {
		t.Fatal("codingAnchorResource")
	}
}
