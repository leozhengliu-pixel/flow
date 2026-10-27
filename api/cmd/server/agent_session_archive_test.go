package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/coding"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// archiveFixture is a workspace with real signed-in people: the owner
// (admin), the member who starts sessions (initiator), another member and a
// guest (both in the team, so they see the issue), and an installed
// application with its own OAuth token.
type archiveFixture struct {
	t                                *testing.T
	s                                *server
	host                             *httptest.Server
	admin, initiator, other, guest   *http.Client
	initiatorUser, otherUser, guestU domain.User
	app                              domain.ApplicationInstallation
	team                             string
}

const archiveWorkspace = "test-workspace"

func newArchiveFixture(t *testing.T) *archiveFixture {
	t.Helper()
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	if err := repo.RegisterOAuthClient(t.Context(), domain.OAuthClient{ClientID: "archive-agent", ClientName: "Archive agent", RedirectURIs: []string{"https://example.test/callback"}}); err != nil {
		t.Fatal(err)
	}
	f := &archiveFixture{t: t, s: &server{store: repo, uploadPath: t.TempDir()}}
	f.host = httptest.NewServer(newHandler(f.s))
	t.Cleanup(f.host.Close)
	f.team = repo.Bootstrap().Teams[0].ID
	f.admin = authClient(t)
	authRequest[domain.AuthSession](t, f.admin, http.MethodPost, f.host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	join := func(name, email, role string) (*http.Client, domain.User) {
		client, user := verifiedAuthClient(t, f.host.URL, name, email)
		invite := authRequest[[]domain.Invitation](t, f.admin, http.MethodPost, f.host.URL+"/api/workspaces/"+archiveWorkspace+"/invitations", map[string]any{"emails": []string{email}, "role": "member"}, "", http.StatusCreated)
		authRequest[domain.WorkspaceMembership](t, client, http.MethodPost, f.host.URL+"/api/invitations/accept", map[string]string{"token": invite[0].Token}, "", http.StatusOK)
		authRequest[any](t, f.admin, http.MethodPut, f.host.URL+"/api/workspaces/"+archiveWorkspace+"/teams/"+f.team+"/members/"+user.ID, map[string]any{"member": true}, "", http.StatusNoContent)
		if role != "member" {
			authRequest[domain.WorkspaceMember](t, f.admin, http.MethodPatch, f.host.URL+"/api/workspaces/"+archiveWorkspace+"/members/"+user.ID, map[string]string{"role": role}, "", http.StatusOK)
		}
		return client, user
	}
	f.initiator, f.initiatorUser = join("Session starter", "archive-initiator@example.com", "member")
	f.other, f.otherUser = join("Other member", "archive-other@example.com", "member")
	f.guest, f.guestU = join("Guest", "archive-guest@example.com", "guest")
	installed := authRequest[struct {
		Application domain.ApplicationInstallation `json:"application"`
	}](t, f.admin, http.MethodPost, f.host.URL+"/api/application-installations", map[string]any{"clientId": "archive-agent", "teamIds": []string{f.team}, "scopes": []string{"read", "write", "app:assignable", "app:mentionable"}, "active": true}, archiveWorkspace, http.StatusOK)
	f.app = installed.Application
	if _, err := repo.CreateOAuthAuthorizationGrant(t.Context(), "archive-code", domain.OAuthAuthorizationCode{ClientID: "archive-agent", Actor: "app", InstallerID: "usr_admin", UserID: "usr_admin", WorkspaceKey: archiveWorkspace, TeamIDs: []string{f.team}, Scopes: []string{"read", "write", "app:assignable", "app:mentionable"}, ExpiresAt: time.Now().Add(time.Hour)}, domain.OAuthAuthorization{ID: "archive-grant"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ExchangeOAuthGrant(t.Context(), "authorization_code", "archive-code", "archive-agent", "archive-refresh", domain.APIKey{ID: "archive-access", SecretHash: secretHash("archive-app-token"), CreatedAt: time.Now()}, func(domain.OAuthAuthorizationCode) bool { return true }, nil); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *archiveFixture) url(path string) string { return f.host.URL + path }

// appCall makes a request with the application's OAuth token.
func (f *archiveFixture) appCall(method, path, body string) (int, string) {
	f.t.Helper()
	request, err := http.NewRequest(method, f.url(path), strings.NewReader(body))
	if err != nil {
		f.t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer archive-app-token")
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		f.t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	return response.StatusCode, string(raw)
}

func (f *archiveFixture) task(id string) domain.AgentTask {
	f.t.Helper()
	task, err := f.s.store.AgentTask(f.t.Context(), archiveWorkspace, id)
	if err != nil {
		f.t.Fatal(err)
	}
	return task
}

func (f *archiveFixture) appActivity(id, kind, body string) domain.AgentTask {
	f.t.Helper()
	task := f.task(id)
	next, err := f.s.store.AppendAgentActivity(f.t.Context(), archiveWorkspace, id, task.Version, domain.AgentActivity{ActorID: f.app.UserID, Type: kind, Body: body})
	if err != nil {
		f.t.Fatalf("%s: %v", kind, err)
	}
	return next
}

func (f *archiveFixture) delegate(issueID string) string {
	f.t.Helper()
	authRequest[domain.Issue](f.t, f.initiator, http.MethodPatch, f.url("/api/issue-records/"+issueID), map[string]any{"delegateId": ""}, archiveWorkspace, http.StatusOK)
	updated := authRequest[domain.Issue](f.t, f.initiator, http.MethodPatch, f.url("/api/issue-records/"+issueID), map[string]any{"delegateId": f.app.UserID}, archiveWorkspace, http.StatusOK)
	if updated.AgentSessionID == "" {
		f.t.Fatalf("delegation started no session: %+v", updated)
	}
	return updated.AgentSessionID
}

func (f *archiveFixture) sessions(client *http.Client, issueID string, includeArchived bool) []domain.AgentTask {
	f.t.Helper()
	path := "/api/agent-tasks?resourceType=issue&resourceId=" + issueID
	if includeArchived {
		path += "&includeArchived=true"
	}
	return authRequest[[]domain.AgentTask](f.t, client, http.MethodGet, f.url(path), nil, archiveWorkspace, http.StatusOK)
}

func sessionIDs(tasks []domain.AgentTask) []string {
	ids := []string{}
	for _, task := range tasks {
		ids = append(ids, task.ID)
	}
	slices.Sort(ids)
	return ids
}

func (f *archiveFixture) archive(client *http.Client, id string, want int) agentSessionArchiveResponse {
	f.t.Helper()
	return authRequest[agentSessionArchiveResponse](f.t, client, http.MethodPost, f.url("/api/agent-tasks/"+id+"/archive"), map[string]any{}, archiveWorkspace, want)
}

// realtimeStream follows a signed-in person's realtime stream.
func (f *archiveFixture) realtimeStream(client *http.Client) <-chan string {
	f.t.Helper()
	response, err := client.Get(f.url("/api/realtime/events?workspace=" + archiveWorkspace))
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { response.Body.Close() })
	lines := make(chan string, 256)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 1<<20), 1<<20)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()
	select {
	case line := <-lines:
		if !strings.Contains(line, "connected") {
			f.t.Fatalf("handshake: %s", line)
		}
	case <-time.After(3 * time.Second):
		f.t.Fatal("no realtime handshake")
	}
	return lines
}

// archiveEvents counts agent_task.updated pushes that archive taskID within wait.
func archiveEvents(lines <-chan string, taskID string, wait time.Duration) int {
	count := 0
	deadline := time.After(wait)
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				return count
			}
			if strings.Contains(line, `"agent_task.updated"`) && strings.Contains(line, `"taskId":"`+taskID+`"`) && strings.Contains(line, `"archived":true`) {
				count++
			}
		case <-deadline:
			return count
		}
	}
}

func (f *archiveFixture) auditEntries(taskID string) int {
	f.t.Helper()
	data, ok := f.s.store.WorkspaceMetadataFields(archiveWorkspace, "auditLog")
	if !ok {
		f.t.Fatal("no workspace")
	}
	count := 0
	for _, entry := range data.AuditLog {
		if entry.Action == "agent_session.archived" && entry.ResourceID == taskID {
			count++
		}
	}
	return count
}

// Archiving a whole session from the issue activity: who may, what leaves the
// activity and what stays, the running session's stop, the race with a run
// still writing, other people's live refresh and an idempotent repeat.
func TestAgentSessionArchiveForSignedInPeople(t *testing.T) {
	f := newArchiveFixture(t)
	ctx := t.Context()
	issue := authRequest[domain.Issue](t, f.initiator, http.MethodPost, f.url("/api/issues"), map[string]any{"title": "Agent work", "teamId": f.team}, archiveWorkspace, http.StatusCreated)

	// A successful round: the agent's report, a document, an attachment and a linked issue.
	succeeded := f.delegate(issue.ID)
	if task := f.task(succeeded); task.CreatorID != f.initiatorUser.ID || task.Trigger != "delegation" {
		t.Fatalf("delegated session: %+v", task)
	}
	f.appActivity(succeeded, "response", "Report: the fix is in")
	report := authRequest[domain.Document](t, f.admin, http.MethodPost, f.url("/api/documents"), map[string]any{"title": "Agent report", "teamIds": []string{f.team}}, archiveWorkspace, http.StatusCreated)
	attachment := uploadAuthAttachment(t, f.initiator, f.host.URL, issue.ID, archiveWorkspace, http.StatusCreated)
	linked := authRequest[domain.Issue](t, f.initiator, http.MethodPost, f.url("/api/issues"), map[string]any{"title": "Follow-up from the agent", "teamId": f.team, "parentId": issue.ID}, archiveWorkspace, http.StatusCreated)
	successLog, _ := f.s.store.AgentActivities(ctx, archiveWorkspace, succeeded, "")

	// A canceled round (a person stopped it) and a failed round started from a comment.
	canceled := f.delegate(issue.ID)
	authRequest[domain.AgentTask](t, f.initiator, http.MethodPost, f.url("/api/agent-tasks/"+canceled+"/activities"), map[string]any{"expectedVersion": f.task(canceled).Version, "type": "canceled"}, archiveWorkspace, http.StatusOK)
	mention := authRequest[domain.Comment](t, f.initiator, http.MethodPost, f.url("/api/issues/"+issue.ID+"/comments"), map[string]any{"body": "Please investigate", "bodyData": map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "mention", "attrs": map[string]any{"id": f.app.UserID, "label": "Archive agent"}}}}}}}, archiveWorkspace, http.StatusCreated)
	failed := ""
	for _, task := range f.sessions(f.initiator, issue.ID, false) {
		if task.CommentID == mention.ID {
			failed = task.ID
		}
	}
	if failed == "" {
		t.Fatal("the mention started no session")
	}
	f.appActivity(failed, "error", "The model failed")
	all := []string{succeeded, canceled, failed}
	slices.Sort(all)
	if got := sessionIDs(f.sessions(f.other, issue.ID, false)); !slices.Equal(got, all) {
		t.Fatalf("sessions before archiving: %v, want %v", got, all)
	}

	// Two people watch the issue live.
	initiatorStream, otherStream := f.realtimeStream(f.initiator), f.realtimeStream(f.other)

	// Who may archive: not another member, not a guest, not the application (it did not open the session).
	f.archive(f.other, canceled, http.StatusForbidden)
	f.archive(f.guest, canceled, http.StatusForbidden)
	if status, body := f.appCall(http.MethodPost, "/api/agent-tasks/"+canceled+"/archive", `{}`); status != http.StatusForbidden {
		t.Fatalf("application archive = %d: %s", status, body)
	}
	if task := f.task(canceled); task.ArchivedAt != nil {
		t.Fatal("a refused archive archived the session")
	}
	head, err := f.s.store.WorkspaceChangeHead(ctx, archiveWorkspace)
	if err != nil {
		t.Fatal(err)
	}

	// The initiator archives the canceled round; both viewers' threads drop it live.
	archived := f.archive(f.initiator, canceled, http.StatusOK)
	if !archived.Archived || archived.AlreadyArchived || archived.Stopped || archived.PreviousStatus != "canceled" || archived.Session.ArchivedAt == nil || archived.Session.ArchivedBy != f.initiatorUser.ID {
		t.Fatalf("archive response: %+v", archived)
	}
	if archiveEvents(initiatorStream, canceled, 3*time.Second) != 1 || archiveEvents(otherStream, canceled, time.Second) != 1 {
		t.Fatal("the archive did not reach both viewers once")
	}
	if f.auditEntries(canceled) != 1 {
		t.Fatal("the archive was not audited")
	}
	after, err := f.s.store.WorkspaceChangeHead(ctx, archiveWorkspace)
	if err != nil || after <= head {
		t.Fatalf("change head %d -> %d: the audit entry is a change", head, after)
	}
	// Archiving it again (an admin, a double click) changes nothing: no write, no change, no push, no audit entry.
	version := f.task(canceled).Version
	again := f.archive(f.admin, canceled, http.StatusOK)
	if !again.AlreadyArchived || again.Session.ArchivedBy != f.initiatorUser.ID || f.task(canceled).Version != version {
		t.Fatalf("repeat archive: %+v", again)
	}
	if repeat, _ := f.s.store.WorkspaceChangeHead(ctx, archiveWorkspace); repeat != after {
		t.Fatalf("a repeat archive wrote a change: %d -> %d", after, repeat)
	}
	if archiveEvents(initiatorStream, canceled, 700*time.Millisecond) != 0 || f.auditEntries(canceled) != 1 {
		t.Fatal("a repeat archive published or audited again")
	}

	// An admin archives the failed comment-triggered round; the comment that mentioned the agent stays.
	failedArchive := f.archive(f.admin, failed, http.StatusOK)
	if !failedArchive.Archived || failedArchive.Stopped || failedArchive.Session.Status != "error" {
		t.Fatalf("failed round: %+v", failedArchive)
	}
	if _, _, err := f.s.store.CommentResource(ctx, archiveWorkspace, mention.ID); err != nil {
		t.Fatalf("the mention comment was removed: %v", err)
	}

	// A running round: the run is still streaming when the initiator archives it.
	running := f.delegate(issue.ID)
	started := make(chan struct{})
	var runErr error
	var run sync.WaitGroup
	run.Add(1)
	go func() {
		defer run.Done()
		current, wrote := f.task(running), 0
		for attempt := 0; attempt < 5000; attempt++ {
			next, err := appendBuiltinActivity(ctx, f.s.store, current, domain.AgentActivity{ActorID: f.app.UserID, Type: "output", Body: "chunk "})
			if err == nil {
				current, wrote = next, wrote+1
				if wrote == 3 {
					close(started)
				}
				continue
			}
			if errors.Is(err, store.ErrIssueVersion) {
				if current, err = f.s.store.AgentTask(ctx, archiveWorkspace, running); err == nil {
					continue
				}
			}
			runErr = err
			return
		}
	}()
	<-started
	stopped := f.archive(f.initiator, running, http.StatusOK)
	run.Wait()
	if !stopped.Stopped || stopped.Session.Status != "canceled" || !errors.Is(runErr, store.ErrAgentTaskArchived) {
		t.Fatalf("running round: %+v (run ended with %v)", stopped, runErr)
	}
	runLog, _ := f.s.store.AgentActivities(ctx, archiveWorkspace, running, "")
	if last := runLog[len(runLog)-1]; last.Type != "canceled" || last.ActorID != f.initiatorUser.ID || last.ID != paddedVersion(stopped.Session.Version) {
		t.Fatalf("the run wrote after the archive: %+v", last)
	}
	// Late writes, from the application's token too, are refused and never reopen it.
	if status, body := f.appCall(http.MethodPost, "/api/agent-tasks/"+running+"/activities", `{"expectedVersion":`+strconv.FormatInt(stopped.Session.Version, 10)+`,"type":"response","body":"late"}`); status != http.StatusConflict || !strings.Contains(body, "archived") {
		t.Fatalf("late application reply = %d: %s", status, body)
	}
	if status, body := f.appCall(http.MethodPatch, "/api/agent-tasks/"+running, `{"plan":[{"content":"Revive","status":"inProgress"}]}`); status != http.StatusConflict {
		t.Fatalf("late plan = %d: %s", status, body)
	}
	authRequest[any](t, f.initiator, http.MethodPost, f.url("/api/agent-tasks/"+running+"/activities"), map[string]any{"expectedVersion": stopped.Session.Version, "type": "retry"}, archiveWorkspace, http.StatusConflict)

	// What the activity shows now, for everyone: only the successful round, with its report and links intact.
	for _, client := range []*http.Client{f.initiator, f.other, f.admin} {
		if got := sessionIDs(f.sessions(client, issue.ID, false)); !slices.Equal(got, []string{succeeded}) {
			t.Fatalf("sessions after archiving: %v", got)
		}
	}
	if got := f.sessions(f.admin, issue.ID, true); len(got) != 4 {
		t.Fatalf("includeArchived lists %d sessions, want 4", len(got))
	}
	// The log of an archived session is still readable.
	read := authRequest[struct {
		Session    domain.AgentTask       `json:"session"`
		Activities []domain.AgentActivity `json:"activities"`
	}](t, f.other, http.MethodGet, f.url("/api/agent-tasks/"+failed), nil, archiveWorkspace, http.StatusOK)
	if read.Session.ArchivedAt == nil || len(read.Activities) == 0 || read.Activities[len(read.Activities)-1].Body != "The model failed" {
		t.Fatalf("archived log: %+v", read)
	}
	if log, _ := f.s.store.AgentActivities(ctx, archiveWorkspace, succeeded, ""); len(log) != len(successLog) || f.task(succeeded).ArchivedAt != nil || f.task(succeeded).Status != "complete" {
		t.Fatal("the successful round changed")
	}
	detail := authRequest[domain.Issue](t, f.other, http.MethodGet, f.url("/api/issue-records/"+issue.ID), nil, archiveWorkspace, http.StatusOK)
	if !slices.ContainsFunc(detail.Attachments, func(item domain.Attachment) bool { return item.ID == attachment.ID }) || detail.Delegate == nil {
		t.Fatalf("issue after archiving: attachments=%+v delegate=%v", detail.Attachments, detail.Delegate)
	}
	authRequest[domain.Issue](t, f.other, http.MethodGet, f.url("/api/issue-records/"+linked.ID), nil, archiveWorkspace, http.StatusOK)
	authRequest[domain.Document](t, f.other, http.MethodGet, f.url("/api/documents/"+report.ID), nil, archiveWorkspace, http.StatusOK)

	// An application may archive a session it opened itself, and only that.
	status, body := f.appCall(http.MethodPost, "/api/agent-tasks", `{"issueId":"`+issue.ID+`"}`)
	if status != http.StatusCreated {
		t.Fatalf("agent-opened session = %d: %s", status, body)
	}
	opened := ""
	for _, task := range f.sessions(f.admin, issue.ID, false) {
		if task.Trigger == "agent" {
			opened = task.ID
		}
	}
	if status, body := f.appCall(http.MethodPost, "/api/agent-tasks/"+opened+"/archive", `{}`); status != http.StatusOK || !strings.Contains(body, `"archived":true`) {
		t.Fatalf("application archives its own session = %d: %s", status, body)
	}
	if status, _ := f.appCall(http.MethodPost, "/api/agent-tasks/"+succeeded+"/archive", `{}`); status != http.StatusForbidden || f.task(succeeded).ArchivedAt != nil {
		t.Fatalf("application archived a person's session: %d", status)
	}
}

// Archiving a task that anchors a coding session stops a working session but
// does not archive it (that would close its pull requests): the session, its
// thread and its output stay readable; only the person who started it or an
// admin can do it.
func TestAgentSessionArchiveStopsItsCodingSession(t *testing.T) {
	f := newCodingAccessFixture(t)
	issue := requestJSON[domain.Issue](t, f.handler, http.MethodPost, "/api/issues", map[string]any{"title": "Flaky test", "teamId": f.workspace.Teams[0].ID}, http.StatusCreated)
	session := f.start(coding.StartInput{Source: domain.CodingSourceIssue, IssueID: issue.ID, CreatorID: "usr_member"})
	admins := f.start(coding.StartInput{Source: domain.CodingSourceIssue, IssueID: issue.ID, CreatorID: "usr_admin"})
	f.expect("usr_member", http.MethodPost, "/api/agent-tasks/"+admins.AgentSessionID+"/archive", http.StatusForbidden)
	f.expect("usr_member", http.MethodPost, "/api/agent-tasks/"+session.AgentSessionID+"/archive", http.StatusOK)
	stored, err := f.s.coding.service.Get(context.Background(), f.workspace.Workspace.URLKey, session.ID)
	if err != nil || !stored.StopRequested || stored.StoppedBy != "usr_member" || stored.ArchivedAt != nil {
		t.Fatalf("coding session after the archive: %+v %v", stored, err)
	}
	f.expect("usr_member", http.MethodGet, "/api/coding/sessions/"+session.ID, http.StatusOK)
	f.expect("usr_member", http.MethodGet, "/api/agent-tasks/"+session.AgentSessionID, http.StatusOK)
	anchor, err := f.s.store.AgentTask(context.Background(), f.workspace.Workspace.URLKey, session.AgentSessionID)
	if err != nil || anchor.ArchivedAt == nil {
		t.Fatalf("anchor: %+v %v", anchor, err)
	}
	if other, _ := f.s.coding.service.Get(context.Background(), f.workspace.Workspace.URLKey, admins.ID); other.StopRequested {
		t.Fatal("another coding session was stopped")
	}
}

// Deleting a plain comment, deleting one agent message and archiving a whole
// session are three different scopes on the same issue: the first removes
// only that comment, the second hides only that reply (the session stays
// listed), the third hides the session with every message in it (the
// person's reply too) while its log, the other session and the comments stay.
func TestAgentSessionArchiveTellsACommentAMessageAndASessionApart(t *testing.T) {
	f := newArchiveFixture(t)
	ctx := t.Context()
	issue := authRequest[domain.Issue](t, f.initiator, http.MethodPost, f.url("/api/issues"), map[string]any{"title": "Three scopes", "teamId": f.team}, archiveWorkspace, http.StatusCreated)
	session := f.delegate(issue.ID)
	firstAnswer := paddedVersion(f.appActivity(session, "response", "First answer").Version)
	replied := authRequest[domain.AgentTask](t, f.initiator, http.MethodPost, f.url("/api/agent-tasks/"+session+"/activities"), map[string]any{"expectedVersion": f.task(session).Version, "type": "prompt", "body": "Use the other branch"}, archiveWorkspace, http.StatusOK)
	reply := paddedVersion(replied.Version)
	f.appActivity(session, "response", "Second answer")
	kept := authRequest[domain.Comment](t, f.other, http.MethodPost, f.url("/api/issues/"+issue.ID+"/comments"), map[string]any{"body": "A note that stays"}, archiveWorkspace, http.StatusCreated)
	plain := authRequest[domain.Comment](t, f.other, http.MethodPost, f.url("/api/issues/"+issue.ID+"/comments"), map[string]any{"body": "A note to delete"}, archiveWorkspace, http.StatusCreated)
	type sessionRead struct {
		Session    domain.AgentTask       `json:"session"`
		Activities []domain.AgentActivity `json:"activities"`
	}
	readSession := func() sessionRead {
		return authRequest[sessionRead](t, f.other, http.MethodGet, f.url("/api/agent-tasks/"+session), nil, archiveWorkspace, http.StatusOK)
	}
	commentIDs := func() []string {
		comments, _, err := f.s.store.IssueContent(ctx, archiveWorkspace, issue.ID)
		if err != nil {
			t.Fatal(err)
		}
		ids := []string{}
		for _, comment := range comments {
			ids = append(ids, comment.ID)
		}
		return ids
	}
	logLength := len(readSession().Activities)

	// 1. A plain comment: only it goes; the session and its messages are untouched.
	authRequest[any](t, f.other, http.MethodDelete, f.url("/api/issue-records/"+issue.ID+"/comments/"+plain.ID), nil, archiveWorkspace, http.StatusNoContent)
	if ids := commentIDs(); slices.Contains(ids, plain.ID) || !slices.Contains(ids, kept.ID) {
		t.Fatalf("comments after deleting one: %v", ids)
	}
	if got := sessionIDs(f.sessions(f.other, issue.ID, false)); !slices.Equal(got, []string{session}) || len(readSession().Activities) != logLength {
		t.Fatalf("deleting a comment changed the session: %v", got)
	}

	// 2. One agent message: a "deleted" activity hides that reply; the session stays listed with its other messages.
	authRequest[domain.AgentTask](t, f.initiator, http.MethodPost, f.url("/api/agent-tasks/"+session+"/activities"), map[string]any{"expectedVersion": f.task(session).Version, "type": "deleted", "activityId": firstAnswer}, archiveWorkspace, http.StatusOK)
	read := readSession()
	hidden := slices.ContainsFunc(read.Activities, func(item domain.AgentActivity) bool {
		return item.Type == "deleted" && item.Metadata["activityId"] == firstAnswer
	})
	if !hidden || read.Session.ArchivedAt != nil || !slices.ContainsFunc(read.Activities, func(item domain.AgentActivity) bool { return item.ID == reply && item.Body == "Use the other branch" }) {
		t.Fatalf("deleting one agent message: hidden=%v archived=%v", hidden, read.Session.ArchivedAt)
	}
	if got := sessionIDs(f.sessions(f.other, issue.ID, false)); !slices.Equal(got, []string{session}) {
		t.Fatalf("deleting one message hid the session: %v", got)
	}

	// 3. The whole session: hidden with the person's reply for everyone, its log and the comments kept.
	other := f.delegate(issue.ID)
	f.archive(f.initiator, session, http.StatusOK)
	for _, client := range []*http.Client{f.initiator, f.other, f.admin} {
		if got := sessionIDs(f.sessions(client, issue.ID, false)); !slices.Equal(got, []string{other}) {
			t.Fatalf("sessions after archiving: %v, want only %s", got, other)
		}
	}
	read = readSession()
	if read.Session.ArchivedAt == nil || !slices.ContainsFunc(read.Activities, func(item domain.AgentActivity) bool { return item.ID == reply }) || !slices.ContainsFunc(read.Activities, func(item domain.AgentActivity) bool { return item.ID == firstAnswer }) {
		t.Fatalf("the archived session's log lost messages: %+v", read.Activities)
	}
	if ids := commentIDs(); !slices.Contains(ids, kept.ID) {
		t.Fatalf("archiving the session removed a comment: %v", ids)
	}
	// A person's reply to the archived session is refused, so it cannot reappear.
	authRequest[any](t, f.initiator, http.MethodPost, f.url("/api/agent-tasks/"+session+"/activities"), map[string]any{"expectedVersion": read.Session.Version, "type": "prompt", "body": "Are you there?"}, archiveWorkspace, http.StatusConflict)
}
