package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"flow/api/internal/coding"
	"flow/api/internal/domain"
	"flow/api/internal/llmproxy"
	"flow/api/internal/store"
)

// codingActorFixture adds a private team that usr_member is not in, an issue
// in it and an issue in the public first team, to the coding access fixture.
type codingActorFixture struct {
	*codingAccessFixture
	privateIssue domain.Issue
	publicIssue  domain.Issue
}

// codingActorModes runs a test against the legacy store and against the
// bounded-metadata store view; the results must be equal.
func codingActorModes(t *testing.T, run func(t *testing.T, f *codingActorFixture)) {
	for _, bounded := range []bool{false, true} {
		name := "unbounded"
		if bounded {
			name = "bounded"
		}
		t.Run(name, func(t *testing.T) {
			if bounded {
				t.Setenv("FLOW_RECORD_HYDRATION", "on")
				t.Setenv("FLOW_RECORD_JOURNAL", "on")
			}
			run(t, newCodingActorFixture(t, bounded))
		})
	}
}

const codingScopedKeySecret = "coding-scoped-key"

func newCodingActorFixture(t *testing.T, bounded bool) *codingActorFixture {
	t.Helper()
	f := &codingActorFixture{codingAccessFixture: newCodingAccessFixture(t)}
	workspace := f.workspace.Workspace.URLKey
	if err := f.s.store.MutateWorkspace(t.Context(), workspace, "coding_actor.seed", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: "coding-private", Name: "Hidden", Key: "HID", Private: true})
		data.TeamSettings["coding-private"] = domain.TeamSettings{TeamID: "coding-private", Access: "private"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.s.store.SetTeamMembership(t.Context(), f.workspace.Workspace.ID, "coding-private", "usr_admin", "member", true); err != nil {
		t.Fatal(err)
	}
	f.privateIssue = requestJSON[domain.Issue](t, f.handler, http.MethodPost, "/api/issues", map[string]any{"title": "Secret rollout plan", "description": "private details", "teamId": "coding-private"}, http.StatusCreated)
	f.publicIssue = requestJSON[domain.Issue](t, f.handler, http.MethodPost, "/api/issues", map[string]any{"title": "Flaky test", "teamId": f.workspace.Teams[0].ID}, http.StatusCreated)
	if err := f.s.store.MutateWorkspace(t.Context(), workspace, "api_key.created", "key_scoped", nil, func(data *domain.Bootstrap) error {
		data.APIKeys = append(data.APIKeys, domain.APIKey{ID: "key_scoped", SecretHash: secretHash(codingScopedKeySecret), CreatorID: "usr_admin", Scopes: []string{"read", "write"}, TeamRestriction: "selected", TeamIDs: []string{f.workspace.Teams[0].ID}, CreatedAt: time.Now().UTC()})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if bounded {
		// Serve the same database through the bounded-metadata view.
		view, err := f.s.store.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		bs := &server{store: view, uploadPath: t.TempDir(), authDisabled: true}
		bs.agent.Enabled = true
		for _, key := range view.WorkspaceKeys() {
			bs.ensureBuiltinApplication(t.Context(), key)
		}
		bs.initCoding(f.s.coding.config)
		f.s, f.handler = bs, newHandler(bs)
	}
	return f
}

func (f *codingActorFixture) post(secret, path, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	f.s.authDisabled = false
	defer func() { f.s.authDisabled = true }()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+secret)
	request.Header.Set("X-Workspace-Key", f.workspace.Workspace.URLKey)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	f.handler.ServeHTTP(recorder, request)
	return recorder
}

func (f *codingActorFixture) sessionCount() int {
	f.t.Helper()
	sessions, err := f.s.store.ListCodingSessions(f.t.Context(), f.workspace.Workspace.URLKey, store.CodingSessionFilter{Limit: 100})
	if err != nil {
		f.t.Fatal(err)
	}
	return len(sessions)
}

func (f *codingActorFixture) expectCreate(secret string, body string, want int) *httptest.ResponseRecorder {
	f.t.Helper()
	recorder := f.post(secret, "/api/coding/sessions", body)
	if recorder.Code != want {
		f.t.Fatalf("POST /api/coding/sessions %s = %d, want %d: %s", body, recorder.Code, want, recorder.Body.String())
	}
	return recorder
}

// A member outside a private team cannot start a session on its issue: the
// issue is not found, no session exists, and nothing of the issue (title,
// branch name) comes back.
func TestCodingStartRefusesUnreadableIssue(t *testing.T) {
	codingActorModes(t, func(t *testing.T, f *codingActorFixture) {
		for _, route := range []string{"/api/coding/sessions", "/api/issues/" + f.privateIssue.ID + "/coding-session"} {
			body := `{"prompt":"summarize this issue","issueId":"` + f.privateIssue.ID + `"}`
			recorder := f.post(f.secrets["usr_member"], route, body)
			// The issue route may be stopped earlier by the team check (403).
			if recorder.Code != http.StatusNotFound && recorder.Code != http.StatusForbidden {
				t.Fatalf("%s: %d %s", route, recorder.Code, recorder.Body.String())
			}
			if strings.Contains(recorder.Body.String(), "Secret rollout plan") || strings.Contains(recorder.Body.String(), "private details") {
				t.Fatalf("%s leaks the issue: %s", route, recorder.Body.String())
			}
		}
		// The issue identifier is no way around it.
		f.expectCreate(f.secrets["usr_member"], `{"prompt":"summarize","issueId":"`+f.privateIssue.Identifier+`"}`, http.StatusNotFound)
		if got := f.sessionCount(); got != 0 {
			t.Fatalf("%d sessions were created for an unreadable issue", got)
		}
		// People who can read the issue keep starting sessions.
		created := f.expectCreate(f.secrets["usr_admin"], `{"prompt":"fix it","issueId":"`+f.privateIssue.ID+`"}`, http.StatusCreated)
		if !strings.Contains(created.Body.String(), f.privateIssue.ID) {
			t.Fatalf("created: %s", created.Body.String())
		}
		f.expectCreate(f.secrets["usr_member"], `{"prompt":"fix it","issueId":"`+f.publicIssue.ID+`"}`, http.StatusCreated)
		if got := f.sessionCount(); got != 2 {
			t.Fatalf("sessions = %d", got)
		}
	})
}

// An API key limited to some teams cannot start a session on an issue of
// another team, though its owner can read that issue.
func TestCodingStartHonorsAPIKeyTeamScope(t *testing.T) {
	codingActorModes(t, func(t *testing.T, f *codingActorFixture) {
		secret := codingScopedKeySecret
		f.expectCreate(secret, `{"prompt":"x","issueId":"`+f.privateIssue.ID+`"}`, http.StatusNotFound)
		if recorder := f.post(secret, "/api/issues/"+f.privateIssue.ID+"/coding-session", `{"prompt":"x"}`); recorder.Code != http.StatusNotFound && recorder.Code != http.StatusForbidden {
			t.Fatalf("issue route: %d %s", recorder.Code, recorder.Body.String())
		}
		if got := f.sessionCount(); got != 0 {
			t.Fatalf("%d sessions", got)
		}
		f.expectCreate(secret, `{"prompt":"x","issueId":"`+f.publicIssue.ID+`"}`, http.StatusCreated)
	})
}

func TestCodingStartValidatesEnvironment(t *testing.T) {
	codingActorModes(t, func(t *testing.T, f *codingActorFixture) {
		workspace := f.workspace.Workspace.URLKey
		create := func(workspace, name string) domain.CodingEnvironment {
			environment, err := f.s.codingStore().CreateCodingEnvironment(t.Context(), domain.CodingEnvironment{WorkspaceKey: workspace, Name: name, CodeHost: domain.CodeHostGitHub, HostName: "github.com", Repository: "acme/" + name, DefaultBranch: "main", SandboxSize: domain.CodingSizeMedium})
			if err != nil {
				t.Fatal(err)
			}
			return environment
		}
		active := create(workspace, "web")
		archived := create(workspace, "old")
		if _, err := f.s.codingStore().SetCodingEnvironmentArchived(t.Context(), workspace, archived.ID, true); err != nil {
			t.Fatal(err)
		}
		foreign := create("another-workspace", "foreign")
		f.expectCreate(f.secrets["usr_member"], `{"prompt":"x","source":"agentChat","environmentId":"env_missing"}`, http.StatusNotFound)
		f.expectCreate(f.secrets["usr_member"], `{"prompt":"x","source":"agentChat","environmentId":"`+foreign.ID+`"}`, http.StatusNotFound)
		f.expectCreate(f.secrets["usr_member"], `{"prompt":"x","source":"agentChat","environmentId":"`+archived.ID+`"}`, http.StatusBadRequest)
		if got := f.sessionCount(); got != 0 {
			t.Fatalf("%d sessions", got)
		}
		f.expectCreate(f.secrets["usr_member"], `{"prompt":"x","source":"agentChat","environmentId":"`+active.ID+`"}`, http.StatusCreated)
	})
}

func TestCodingStartValidatesSourceID(t *testing.T) {
	codingActorModes(t, func(t *testing.T, f *codingActorFixture) {
		comment := requestJSON[domain.Comment](t, f.handler, http.MethodPost, "/api/issues/"+f.publicIssue.ID+"/comments", map[string]any{"body": "please fix"}, http.StatusCreated)
		other := requestJSON[domain.Comment](t, f.handler, http.MethodPost, "/api/issues/"+f.privateIssue.ID+"/comments", map[string]any{"body": "private comment"}, http.StatusCreated)
		member := f.secrets["usr_member"]
		// A comment of an issue the actor cannot read, or of another issue, is not a source.
		f.expectCreate(member, `{"prompt":"x","source":"comment","issueId":"`+f.publicIssue.ID+`","sourceId":"`+other.ID+`"}`, http.StatusNotFound)
		f.expectCreate(member, `{"prompt":"x","source":"comment","issueId":"`+f.privateIssue.ID+`","sourceId":"`+other.ID+`"}`, http.StatusNotFound)
		f.expectCreate(member, `{"prompt":"x","source":"comment","sourceId":"`+other.ID+`"}`, http.StatusNotFound)
		f.expectCreate(member, `{"prompt":"x","source":"agentChat","sourceId":"`+other.ID+`"}`, http.StatusBadRequest)
		if got := f.sessionCount(); got != 0 {
			t.Fatalf("%d sessions", got)
		}
		f.expectCreate(member, `{"prompt":"x","source":"comment","issueId":"`+f.publicIssue.ID+`","sourceId":"`+comment.ID+`"}`, http.StatusCreated)
	})
}

// A session that starts later (a trigger, a loop, a restart) runs as its
// creator: when the creator lost access since it was scheduled, the start is
// refused at run time, and the sandbox is not provisioned.
func TestCodingStartRechecksActorAtRunTime(t *testing.T) {
	codingActorModes(t, func(t *testing.T, f *codingActorFixture) {
		workspace := f.workspace.Workspace.URLKey
		start := func(creator, issueID string) error {
			f.s.authDisabled = false
			defer func() { f.s.authDisabled = true }()
			_, _, err := f.s.coding.service.Start(t.Context(), coding.StartInput{WorkspaceKey: workspace, Source: domain.CodingSourceIssue, SourceID: issueID, IssueID: issueID, Prompt: "scheduled", CreatorID: creator})
			return err
		}
		if err := start("usr_member", f.publicIssue.ID); err != nil {
			t.Fatal(err)
		}
		if err := start("usr_member", f.privateIssue.ID); err != coding.ErrNotFound {
			t.Fatalf("private issue as member: %v", err)
		}
		// A guest, a removed member and an unknown user cannot start sessions.
		if err := f.s.store.UpdateMemberRole(t.Context(), f.workspace.Workspace.ID, "usr_member", "guest"); err != nil {
			t.Fatal(err)
		}
		if err := start("usr_member", f.publicIssue.ID); err != coding.ErrForbidden {
			t.Fatalf("guest: %v", err)
		}
		if err := start("usr_gone", f.publicIssue.ID); err != coding.ErrForbidden {
			t.Fatalf("non-member: %v", err)
		}
		// The directory answers the same way for the context reads of a running
		// session: a guest reads no issue of a private team.
		f.s.authDisabled = false
		defer func() { f.s.authDisabled = true }()
		directory := codingDirectory{s: f.s}
		if _, err := directory.CodingIssueFor(t.Context(), workspace, "usr_member", f.privateIssue.ID); err != coding.ErrNotFound {
			t.Fatalf("guest context read: %v", err)
		}
		if err := directory.CodingActorAllowed(t.Context(), workspace, "usr_member", false); err != coding.ErrForbidden {
			t.Fatalf("guest actor: %v", err)
		}
		if err := directory.CodingActorAllowed(t.Context(), workspace, "usr_member", true); err != nil {
			t.Fatalf("approved guest actor: %v", err)
		}
		if _, err := directory.CodingIssueFor(t.Context(), workspace, "usr_admin", f.privateIssue.ID); err != nil {
			t.Fatalf("admin context read: %v", err)
		}
	})
}

// Revoking the creator's access mid-session stops the next prompt and every
// further model request of the session.
func TestCodingPromptAndModelRequestsRecheckCreator(t *testing.T) {
	codingActorModes(t, func(t *testing.T, f *codingActorFixture) {
		workspace := f.workspace.Workspace.URLKey
		created := f.expectCreate(f.secrets["usr_member"], `{"prompt":"fix it","issueId":"`+f.publicIssue.ID+`"}`, http.StatusCreated)
		var session domain.CodingSession
		if err := json.Unmarshal(created.Body.Bytes(), &session); err != nil {
			t.Fatal(err)
		}
		f.s.authDisabled = false
		defer func() { f.s.authDisabled = true }()
		caller := codingSandboxCaller{session: session, grant: coding.SandboxGrant{WorkspaceKey: workspace, SessionID: session.ID}}
		if err := f.s.codingLLMPolicy(t.Context(), caller); err != nil {
			t.Fatalf("model request before the revocation: %v", err)
		}
		if _, err := f.s.coding.service.Prompt(t.Context(), workspace, session.ID, coding.PromptInput{Prompt: "continue", AuthorID: "usr_member"}); err != nil {
			t.Fatalf("prompt before the revocation: %v", err)
		}
		queued, _ := f.s.store.CodingInbox(t.Context(), session.ID, 0)
		before := len(queued)
		if err := f.s.store.UpdateMemberRole(t.Context(), f.workspace.Workspace.ID, "usr_member", "guest"); err != nil {
			t.Fatal(err)
		}
		if _, err := f.s.coding.service.Prompt(t.Context(), workspace, session.ID, coding.PromptInput{Prompt: "continue", AuthorID: "usr_member"}); !errors.Is(err, coding.ErrForbidden) {
			t.Fatalf("prompt after the revocation: %v", err)
		}
		if err := f.s.codingLLMPolicy(t.Context(), caller); !errors.Is(err, llmproxy.ErrPolicy) {
			t.Fatalf("model request after the revocation: %v", err)
		}
		if after, _ := f.s.store.CodingInbox(t.Context(), session.ID, 0); len(after) != before {
			t.Fatalf("a refused prompt was queued: %d items, was %d", len(after), before)
		}
	})
}
