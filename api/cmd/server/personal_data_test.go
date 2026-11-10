package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// personalDataFixture is a signed-in member of the test workspace who created
// an issue, commented on it (mentioning themselves), searched, and has a
// private Flow Agent conversation.
type personalDataFixture struct {
	baseURL string
	repo    *store.SQLiteStore
	admin   *http.Client
	member  *http.Client
	user    domain.User
	issue   domain.Issue
	comment domain.Comment
}

func newPersonalDataFixture(t *testing.T, repo *store.SQLiteStore, label string) personalDataFixture {
	t.Helper()
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	server := newAuthServerForStore(t, repo)
	// Shared external databases keep earlier runs: names and keys are unique per run.
	suffix := fmt.Sprint(time.Now().UnixNano() / 1000)
	admin := peerAdmin(t, server.URL)
	team := authRequest[domain.Team](t, admin, http.MethodPost, server.URL+"/api/workspaces/test-workspace/teams", map[string]any{"name": "Personal Data " + suffix, "key": "PD" + suffix[len(suffix)-6:]}, "", http.StatusCreated)
	member, user := joinByInvitation(t, server.URL, admin, "Pat "+label+" "+suffix[len(suffix)-6:], "pat-"+label+"-"+suffix+"@example.com", "member", team.ID)
	issue := authRequest[domain.Issue](t, member, http.MethodPost, server.URL+"/api/issues", map[string]any{"title": "Written for the " + label + " check", "teamId": team.ID}, "test-workspace", http.StatusCreated)
	mention := map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "mention", "attrs": map[string]any{"id": user.ID, "label": user.DisplayName}}}}}}
	comment := authRequest[domain.Comment](t, member, http.MethodPost, server.URL+"/api/issues/"+issue.ID+"/comments", map[string]any{"body": "Pat's own comment", "bodyData": mention}, "test-workspace", http.StatusCreated)
	ctx := context.Background()
	metadata, _ := repo.WorkspaceMetadataFields("test-workspace", "workspace")
	if err := repo.RecordSearch(ctx, metadata.Workspace.ID, user.ID, "pat's private search"); err != nil {
		t.Fatal(err)
	}
	session := domain.AgentSession{ID: "agent_session_pd_" + suffix, UserID: user.ID, Title: "Pat's private chat", Messages: []domain.AgentMessage{{ID: "msg_" + suffix, Role: "user", Content: "Only Pat may read this"}}, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	err := repo.MutateWorkspace(store.ContextWithActor(ctx, user), "test-workspace", "agent.session_created", session.ID, nil, func(data *domain.Bootstrap) error {
		data.AgentSessions = append(data.AgentSessions, session)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return personalDataFixture{baseURL: server.URL, repo: repo, admin: admin, member: member, user: user, issue: issue, comment: comment}
}

func personalDataRequest(t *testing.T, client *http.Client, method, url string, input any, workspaceKey string) (int, []byte) {
	t.Helper()
	var body io.Reader
	if input != nil {
		raw, _ := json.Marshal(input)
		body = bytes.NewReader(raw)
	}
	request, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if workspaceKey != "" {
		request.Header.Set("X-Workspace-Key", workspaceKey)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	return response.StatusCode, raw
}

func waitForPersonalDataExport(t *testing.T, client *http.Client, baseURL, id string) domain.PersonalDataExport {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		items := authRequest[[]domain.PersonalDataExport](t, client, http.MethodGet, baseURL+"/api/account/data-exports", nil, "", http.StatusOK)
		for _, item := range items {
			if item.ID == id && item.Status != "queued" && item.Status != "running" {
				return item
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("export %s did not finish", id)
	return domain.PersonalDataExport{}
}

func TestPersonalDataExport(t *testing.T) {
	for name, repo := range favoriteTabTestStores(t) {
		t.Run(name, func(t *testing.T) { testPersonalDataExport(t, repo) })
	}
}

func testPersonalDataExport(t *testing.T, repo *store.SQLiteStore) {
	f := newPersonalDataFixture(t, repo, "export")
	requested := authRequest[domain.PersonalDataExport](t, f.member, http.MethodPost, f.baseURL+"/api/account/data-exports", nil, "", http.StatusAccepted)
	done := waitForPersonalDataExport(t, f.member, f.baseURL, requested.ID)
	if done.Status != "completed" || done.DownloadURL == "" || done.ExpiresAt == nil || done.ExpiresAt.Before(time.Now().Add(6*24*time.Hour)) {
		t.Fatalf("export = %+v", done)
	}

	// Only its owner can download it.
	if status, _ := personalDataRequest(t, f.admin, http.MethodGet, f.baseURL+done.DownloadURL, nil, ""); status != http.StatusNotFound {
		t.Fatalf("another account downloaded the export: %d", status)
	}
	status, archive := personalDataRequest(t, f.member, http.MethodGet, f.baseURL+done.DownloadURL, nil, "")
	if status != http.StatusOK {
		t.Fatalf("download = %d %s", status, archive)
	}
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, file := range reader.File {
		handle, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(handle)
		handle.Close()
		files[file.Name] = string(raw)
	}
	expect := func(name string, needles ...string) {
		t.Helper()
		content, ok := files[name]
		if !ok {
			t.Fatalf("%s missing from the export (%d files)", name, len(files))
		}
		for _, needle := range needles {
			if !strings.Contains(content, needle) {
				t.Fatalf("%s lacks %q:\n%s", name, needle, content)
			}
		}
	}
	expect("README.txt", "Deleted user")
	expect("account.json", f.user.Email, "test-workspace")
	expect("identities.json")
	expect("sessions.json")
	expect("workspaces/test-workspace/issues.json", f.issue.ID, "Written for the export check")
	expect("workspaces/test-workspace/comments.json", f.comment.ID, "Pat's own comment")
	expect("workspaces/test-workspace/agent-chats.json", "Only Pat may read this")
	expect("workspaces/test-workspace/search-history.json", "pat's private search")
	for name, content := range files {
		for _, secret := range []string{"secretHash", "passwordHash", "password_hash", "credentialJson", "claimsJson\": \"{"} {
			if strings.Contains(content, secret) {
				t.Fatalf("%s carries %s", name, secret)
			}
		}
	}
	// Someone else's private agent chat is not in it.
	adminExport := authRequest[domain.PersonalDataExport](t, f.admin, http.MethodPost, f.baseURL+"/api/account/data-exports", nil, "", http.StatusAccepted)
	adminDone := waitForPersonalDataExport(t, f.admin, f.baseURL, adminExport.ID)
	_, adminArchive := personalDataRequest(t, f.admin, http.MethodGet, f.baseURL+adminDone.DownloadURL, nil, "")
	if bytes.Contains(adminArchive, []byte("Only Pat may read this")) {
		t.Fatal("another person's agent chat is in the admin's export")
	}

	// Rate limit: three per day.
	for range 2 {
		next := authRequest[domain.PersonalDataExport](t, f.member, http.MethodPost, f.baseURL+"/api/account/data-exports", nil, "", http.StatusAccepted)
		waitForPersonalDataExport(t, f.member, f.baseURL, next.ID)
	}
	authRequest[map[string]any](t, f.member, http.MethodPost, f.baseURL+"/api/account/data-exports", nil, "", http.StatusTooManyRequests)

	// The request is audited in the member's workspace.
	audit := authRequest[[]domain.AuditLogEntry](t, f.admin, http.MethodGet, f.baseURL+"/api/workspace/audit-log", nil, "test-workspace", http.StatusOK)
	if !slices.ContainsFunc(audit, func(entry domain.AuditLogEntry) bool {
		return entry.Action == "data_export_requested" && entry.ResourceID == f.user.ID
	}) {
		t.Fatal("the export request was not audited")
	}
}

func TestAccountDeletionErasesPersonalData(t *testing.T) {
	for name, repo := range favoriteTabTestStores(t) {
		t.Run(name, func(t *testing.T) { testAccountDeletionErasesPersonalData(t, repo) })
	}
}

func testAccountDeletionErasesPersonalData(t *testing.T, repo *store.SQLiteStore) {
	f := newPersonalDataFixture(t, repo, "delete")
	authRequest[map[string]any](t, f.member, http.MethodDelete, f.baseURL+"/api/account", map[string]string{"confirmation": "someone-else@example.com"}, "", http.StatusBadRequest)
	authRequest[any](t, f.member, http.MethodDelete, f.baseURL+"/api/account", map[string]string{"confirmation": strings.ToUpper(f.user.Email)}, "", http.StatusNoContent)

	// Signed out everywhere, and the sign-in is gone.
	authRequest[map[string]any](t, f.member, http.MethodGet, f.baseURL+"/api/account/bootstrap", nil, "", http.StatusUnauthorized)
	authRequest[map[string]any](t, authClient(t), http.MethodPost, f.baseURL+"/api/auth/login", map[string]string{"email": f.user.Email, "password": "initial-pass"}, "", http.StatusUnauthorized)

	// The content stays, attributed to the pseudonymous "Deleted user".
	detail := issueContext(t, f)
	issue, comments := detail.Issue, detail.Comments
	if issue.Creator.ID != f.user.ID || issue.Creator.DisplayName != domain.DeletedUserDisplayName || issue.Creator.Email != "" || !issue.Creator.Deleted {
		t.Fatalf("creator = %+v", issue.Creator)
	}
	index := slices.IndexFunc(comments, func(item domain.Comment) bool { return item.ID == f.comment.ID })
	if index < 0 || comments[index].User.DisplayName != domain.DeletedUserDisplayName || comments[index].User.Email != "" {
		t.Fatalf("comments = %+v", comments)
	}
	raw, _ := json.Marshal(comments[index].BodyData)
	if strings.Contains(string(raw), f.user.DisplayName) || !strings.Contains(string(raw), domain.DeletedUserDisplayName) {
		t.Fatalf("the mention keeps the name: %s", raw)
	}
	bootstrap := authRequest[domain.Bootstrap](t, f.admin, http.MethodGet, f.baseURL+"/api/bootstrap", nil, "test-workspace", http.StatusOK)
	encoded, _ := json.Marshal(bootstrap)
	for _, personal := range []string{f.user.Email, f.user.DisplayName, "Pat's private chat"} {
		if strings.Contains(string(encoded), personal) {
			t.Fatalf("the workspace still shows %q", personal)
		}
	}
	// Nothing at rest keeps the name or email either.
	if leaks := repo.PersonalDataLeaks(context.Background(), "test-workspace", f.user.Email, f.user.DisplayName); len(leaks) > 0 {
		t.Fatalf("stored copies keep personal data: %v", leaks)
	}
	// The address can sign up again, as a new account.
	_, again := verifiedAuthClient(t, f.baseURL, "Pat Again", f.user.Email)
	if again.ID == f.user.ID {
		t.Fatal("the new sign-up reused the deleted account")
	}
	// A deleted person cannot be newly assigned.
	authRequest[map[string]any](t, f.admin, http.MethodPatch, f.baseURL+"/api/issues/"+f.issue.ID, map[string]any{"assigneeId": f.user.ID}, "test-workspace", http.StatusBadRequest)
	audit := authRequest[[]domain.AuditLogEntry](t, f.admin, http.MethodGet, f.baseURL+"/api/workspace/audit-log", nil, "test-workspace", http.StatusOK)
	if !slices.ContainsFunc(audit, func(entry domain.AuditLogEntry) bool { return entry.Action == "erased" && entry.ResourceID == f.user.ID }) {
		t.Fatal("the erasure was not audited")
	}
}

func TestAccountDeletionRefusesTheLastAdmin(t *testing.T) {
	f := newPersonalDataFixture(t, openPersonalDataStore(t), "owner")
	key := "pat-owned-" + fmt.Sprint(time.Now().UnixNano()%100000)
	authRequest[domain.Bootstrap](t, f.member, http.MethodPost, f.baseURL+"/api/workspaces", map[string]string{"name": "Pat's own", "urlKey": key}, "", http.StatusCreated)
	refused := authRequest[struct {
		Code       string              `json:"code"`
		Workspaces []map[string]string `json:"workspaces"`
	}](t, f.member, http.MethodDelete, f.baseURL+"/api/account", map[string]string{"confirmation": f.user.Email}, "", http.StatusConflict)
	if refused.Code != "LAST_ADMIN" || len(refused.Workspaces) != 1 || refused.Workspaces[0]["urlKey"] != key {
		t.Fatalf("refusal = %+v", refused)
	}
	// Nothing was erased.
	authRequest[domain.AccountBootstrap](t, f.member, http.MethodGet, f.baseURL+"/api/account/bootstrap", nil, "", http.StatusOK)
}

func TestAdminErasesAMemberInTheirWorkspaceOnly(t *testing.T) {
	f := newPersonalDataFixture(t, openPersonalDataStore(t), "erase")
	erase := f.baseURL + "/api/workspaces/test-workspace/members/" + f.user.ID + "/erase"
	// The member has a workspace of their own too, with another admin, so
	// the account lives on.
	key := "pat-elsewhere-" + fmt.Sprint(time.Now().UnixNano()%100000)
	authRequest[domain.Bootstrap](t, f.member, http.MethodPost, f.baseURL+"/api/workspaces", map[string]string{"name": "Pat elsewhere", "urlKey": key}, "", http.StatusCreated)

	// Members cannot erase anyone, and admins confirm with the email.
	authRequest[map[string]any](t, f.member, http.MethodPost, erase, map[string]string{"confirmation": f.user.Email}, "test-workspace", http.StatusForbidden)
	authRequest[map[string]any](t, f.admin, http.MethodPost, erase, map[string]string{"confirmation": "wrong"}, "test-workspace", http.StatusBadRequest)
	result := authRequest[map[string]string](t, f.admin, http.MethodPost, erase, map[string]string{"confirmation": f.user.Email}, "test-workspace", http.StatusOK)
	if result["scope"] != "workspace" {
		t.Fatalf("result = %v", result)
	}

	// The account and its other workspace remain, with its name.
	account := authRequest[domain.AccountBootstrap](t, f.member, http.MethodGet, f.baseURL+"/api/account/bootstrap", nil, "", http.StatusOK)
	if account.Viewer.DisplayName != f.user.DisplayName || slices.ContainsFunc(account.Workspaces, func(item domain.WorkspaceMembership) bool { return item.Workspace.URLKey == "test-workspace" }) {
		t.Fatalf("account = %+v", account)
	}
	// In the workspace, their content shows the pseudonymous identity.
	issue := issueContext(t, f).Issue
	if issue.Creator.DisplayName != domain.DeletedUserDisplayName || issue.Creator.Email != "" {
		t.Fatalf("creator = %+v", issue.Creator)
	}
	if leaks := f.repo.PersonalDataLeaks(context.Background(), "test-workspace", f.user.Email, f.user.DisplayName); len(leaks) > 0 {
		t.Fatalf("stored copies keep personal data: %v", leaks)
	}
	// The workspace cannot take the account back (its old content would be
	// re-identified), and a second erasure finds nobody.
	invitations := authRequest[[]domain.Invitation](t, f.admin, http.MethodPost, f.baseURL+"/api/workspaces/test-workspace/invitations", map[string]any{"emails": []string{f.user.Email}, "role": "member"}, "", http.StatusCreated)
	authRequest[map[string]any](t, f.member, http.MethodPost, f.baseURL+"/api/invitations/accept", map[string]string{"token": invitations[0].Token}, "", http.StatusConflict)
	authRequest[map[string]any](t, f.admin, http.MethodPost, erase, map[string]string{"confirmation": f.user.Email}, "test-workspace", http.StatusNotFound)
	// Admins cannot erase themselves here.
	admin := authRequest[domain.AccountBootstrap](t, f.admin, http.MethodGet, f.baseURL+"/api/account/bootstrap", nil, "", http.StatusOK).Viewer
	authRequest[map[string]any](t, f.admin, http.MethodPost, f.baseURL+"/api/workspaces/test-workspace/members/"+admin.ID+"/erase", map[string]string{"confirmation": admin.Email}, "test-workspace", http.StatusBadRequest)
}

func TestAdminErasureDeletesAnAccountOnlyThisWorkspaceHas(t *testing.T) {
	f := newPersonalDataFixture(t, openPersonalDataStore(t), "only")
	result := authRequest[map[string]string](t, f.admin, http.MethodPost, f.baseURL+"/api/workspaces/test-workspace/members/"+f.user.ID+"/erase", map[string]string{"confirmation": f.user.Email}, "test-workspace", http.StatusOK)
	if result["scope"] != "account" {
		t.Fatalf("result = %v", result)
	}
	authRequest[map[string]any](t, f.member, http.MethodGet, f.baseURL+"/api/account/bootstrap", nil, "", http.StatusUnauthorized)
	user, err := f.repo.UserByID(context.Background(), f.user.ID)
	if err != nil || !user.Deleted || user.Email != "" || user.DisplayName != domain.DeletedUserDisplayName || user.Active {
		t.Fatalf("account = %+v, %v", user, err)
	}
}

type personalDataIssueContext struct {
	Issue    domain.Issue     `json:"issue"`
	Comments []domain.Comment `json:"comments"`
}

func issueContext(t *testing.T, f personalDataFixture) personalDataIssueContext {
	t.Helper()
	return authRequest[personalDataIssueContext](t, f.admin, http.MethodGet, f.baseURL+"/api/issue-records/"+f.issue.ID+"/context", nil, "test-workspace", http.StatusOK)
}

func openPersonalDataStore(t *testing.T) *store.SQLiteStore {
	t.Helper()
	stores := favoriteTabTestStores(t)
	return stores["sqlite"]
}
