package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func agentTaskPollCredential(t *testing.T, repo *store.SQLiteStore, workspace, installer, name string, teams []string) (domain.ApplicationInstallation, string) {
	t.Helper()
	ctx := t.Context()
	client := domain.OAuthClient{ClientID: "task-poll-" + name, ClientName: "Poll test", RedirectURIs: []string{"https://app.example.test/callback"}}
	if err := repo.RegisterOAuthClient(ctx, client); err != nil {
		t.Fatal("could not register poll application")
	}
	scopes := []string{"read"}
	app, err := repo.InstallApplication(ctx, workspace, domain.ApplicationInstallation{
		ClientID: client.ClientID, Name: client.ClientName, Active: true, InstalledBy: installer, TeamIDs: teams, Scopes: scopes,
	}, "")
	if err != nil {
		t.Fatal("could not install poll application")
	}
	code := "task-poll-code-" + name
	_, err = repo.CreateOAuthAuthorizationGrant(ctx, code, domain.OAuthAuthorizationCode{
		ClientID: client.ClientID, Actor: "app", InstallerID: installer, UserID: installer, WorkspaceKey: workspace,
		TeamIDs: teams, Scopes: scopes, ExpiresAt: time.Now().Add(time.Hour),
	}, domain.OAuthAuthorization{ID: "task-poll-grant-" + name}, nil)
	if err != nil {
		t.Fatal("could not create poll authorization")
	}
	secret, err := randomSecret("task-poll-")
	if err != nil {
		t.Fatal("could not generate poll credential")
	}
	_, err = repo.ExchangeOAuthGrant(ctx, "authorization_code", code, client.ClientID, "task-poll-refresh-"+name,
		domain.APIKey{ID: "task-poll-key-" + name, SecretHash: secretHash(secret), CreatedAt: time.Now()}, func(domain.OAuthAuthorizationCode) bool { return true }, nil)
	if err != nil {
		t.Fatal("could not exchange poll authorization")
	}
	return app, secret
}

func agentTaskPollRequest(method, workspace, query, bearer, session string) *http.Request {
	r := httptest.NewRequest(method, "/api/agent-tasks"+query, nil)
	r.Header.Set("X-Workspace-Key", workspace)
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	if session != "" {
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	}
	return r
}

func TestAgentTaskListAuthorization(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	repo := pulseTestRepo(t)
	data := repo.Bootstrap()
	workspace := data.Workspace.URLKey
	team := data.Issues[0].Team
	private := domain.Team{ID: "poll-private", Key: "POL", Private: true}
	child := domain.Team{ID: "poll-child", Key: "PCH"}
	if err := repo.UpdateMemberRole(t.Context(), data.Workspace.ID, "usr_member", "guest"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetTeamMembership(t.Context(), data.Workspace.ID, team.ID, "usr_member", "member", true); err != nil {
		t.Fatal(err)
	}
	issue := data.Issues[0].ID
	const privateIssue = "poll-private-issue"
	if err := repo.MutateWorkspace(t.Context(), workspace, "test.poll_anchors", "", nil, func(next *domain.Bootstrap) error {
		next.Teams = append(next.Teams, private, child)
		next.TeamSettings[private.ID] = domain.TeamSettings{Access: "private"}
		next.TeamSettings[child.ID] = domain.TeamSettings{ParentTeamID: private.ID}
		next.Issues = append(next.Issues, domain.Issue{ID: privateIssue, Identifier: "POL-91", Number: 91, Team: private, State: data.States[0], Creator: data.Viewer})
		next.Projects = append(next.Projects,
			domain.Project{ID: "poll-project", TeamIDs: []string{team.ID, private.ID}},
			domain.Project{ID: "poll-private-project", TeamIDs: []string{child.ID}},
			domain.Project{ID: "poll-unscoped-project"},
		)
		next.Documents = append(next.Documents,
			domain.Document{ID: "poll-document", PreviousSlugIDs: []string{"poll-previous", "poll-collision"}, Creator: data.Viewer, TeamIDs: []string{team.ID}},
			domain.Document{ID: "poll-private-document", SlugID: "poll-collision", Creator: data.Viewer, TeamIDs: []string{private.ID}},
			domain.Document{ID: "poll-inherited-document", IssueID: issue, IssueTeamID: team.ID, InheritedTeamIDs: []string{team.ID}, Creator: data.Viewer},
			domain.Document{ID: "poll-closed-link", IssueID: privateIssue, IssueTeamID: private.ID, InheritedTeamIDs: []string{private.ID}, Creator: data.Viewer},
			domain.Document{ID: "poll-grant", TeamIDs: []string{private.ID}, Creator: data.Viewer,
				Permissions: []domain.DocumentPermission{{SubjectType: "user", SubjectID: "usr_member", Role: "viewer"}}},
		)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	active, token := agentTaskPollCredential(t, repo, workspace, data.Viewer.ID, "active", []string{team.ID})
	empty, emptyToken := agentTaskPollCredential(t, repo, workspace, data.Viewer.ID, "empty", []string{team.ID})
	_, guestSession, err := repo.Login(t.Context(), "member@example.test", "test-password")
	if err != nil {
		t.Fatal("could not create guest session")
	}
	_, adminSession, err := repo.Login(t.Context(), "admin@example.test", "test-password")
	if err != nil {
		t.Fatal("could not create admin session")
	}
	for _, item := range []struct{ id, kind, ref, creator, coding string }{
		{"issue", "issue", issue, data.Viewer.ID, ""},
		{"guest-own", "issue", issue, "usr_member", "guest-coding"},
		{"guest-other", "issue", issue, data.Viewer.ID, "admin-coding"},
		{"archived", "issue", issue, data.Viewer.ID, ""},
		{"private-issue", "issue", privateIssue, data.Viewer.ID, ""},
		{"project", "project", "poll-project", data.Viewer.ID, ""},
		{"private-project", "project", "poll-private-project", data.Viewer.ID, ""},
		{"unscoped-project", "project", "poll-unscoped-project", data.Viewer.ID, ""},
		{"document", "document", "poll-document", data.Viewer.ID, ""},
		{"document-alias", "document", "poll-previous", data.Viewer.ID, ""},
		{"alias-collision", "document", "poll-collision", data.Viewer.ID, ""},
		{"inherited", "document", "poll-inherited-document", data.Viewer.ID, ""},
		{"inherited-private", "document", "poll-closed-link", data.Viewer.ID, ""},
		{"grant", "document", "poll-grant", data.Viewer.ID, ""},
		{"deleted-anchor", "document", "poll-missing", data.Viewer.ID, ""},
		{"environment", codingEnvironmentAnchorResource, "poll-environment", data.Viewer.ID, "setup-coding"},
		{"chat", codingAnchorAgentChat, "poll-chat", data.Viewer.ID, "chat-coding"},
		{"loop", codingAnchorLoop, "poll-loop", "usr_member", "loop-coding"},
	} {
		task := domain.AgentTask{ID: item.id, WorkspaceKey: workspace, ResourceType: item.kind, ResourceID: item.ref,
			CreatorID: item.creator, AppUserID: active.UserID, CodingSessionID: item.coding}
		if item.kind == "issue" {
			task.IssueID = item.ref
		}
		if _, err := repo.CreateAgentInitiatedTask(t.Context(), task); err != nil {
			t.Fatal("could not create fixture task")
		}
	}
	// A second app's completed task is not eligible for its global poll.
	other := domain.AgentTask{ID: "other-app", WorkspaceKey: workspace, AppUserID: empty.UserID, CreatorID: data.Viewer.ID, IssueID: issue}
	if _, err := repo.CreateAgentInitiatedTask(t.Context(), other); err != nil {
		t.Fatal("could not create second app task")
	}
	for _, id := range []string{"archived", "other-app"} {
		if _, err := repo.ArchiveAgentTask(t.Context(), workspace, id, data.Viewer.ID); err != nil {
			t.Fatal("could not archive fixture task")
		}
	}

	// A personal key's grants remain restricted when the key selects no team.
	selectedKey := domain.APIKey{ID: "poll-selected-empty", CreatorID: data.Viewer.ID, SecretHash: secretHash("poll-personal"),
		Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}, CreatedAt: time.Now()}
	if err := repo.MutateWorkspace(t.Context(), workspace, "api_key.created", selectedKey.ID, nil, func(next *domain.Bootstrap) error {
		next.APIKeys = append(next.APIKeys, selectedKey)
		return nil
	}); err != nil {
		t.Fatal("could not create personal key")
	}
	var revocationTokens [2]string
	for i := range revocationTokens {
		app, secret := agentTaskPollCredential(t, repo, workspace, data.Viewer.ID, fmt.Sprintf("in-flight-%d", i), []string{team.ID})
		revocationTokens[i] = secret
		task := domain.AgentTask{
			ID: fmt.Sprintf("poll-in-flight-%d", i), WorkspaceKey: workspace,
			AppUserID: app.UserID, CreatorID: data.Viewer.ID,
			ResourceType: "project", ResourceID: "poll-project",
		}
		if _, err := repo.CreateAgentInitiatedTask(t.Context(), task); err != nil {
			t.Fatal("could not create in-flight revocation fixture")
		}
	}
	bounded, err := repo.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	anchor := func(kind, ref string) string {
		return "?resourceType=" + kind + "&resourceId=" + url.QueryEscape(ref)
	}
	for i, source := range []*store.SQLiteStore{repo, bounded} {
		t.Run(fmt.Sprintf("bounded=%t", source.BoundedMetadataEnabled()), func(t *testing.T) {
			service := &server{store: source, uploadPath: t.TempDir()}
			handler := newHandler(service)
			check := func(name, wkey, query, bearer, session string, status int, ids ...string) {
				t.Helper()
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, agentTaskPollRequest(http.MethodGet, wkey, query, bearer, session))
				if w.Code != status {
					t.Errorf("%s got HTTP %d, want %d", name, w.Code, status)
					return
				}
				if status != http.StatusOK {
					return
				}
				var tasks []domain.AgentTask
				if json.Unmarshal(w.Body.Bytes(), &tasks) != nil || tasks == nil {
					t.Errorf("%s returned an invalid task array", name)
					return
				}
				actual := make([]string, 0, len(tasks))
				for _, task := range tasks {
					actual = append(actual, task.ID)
				}
				slices.Sort(actual)
				slices.Sort(ids)
				if !slices.Equal(actual, ids) {
					t.Errorf("%s returned a different task set (got %d, want %d)", name, len(actual), len(ids))
				}
			}
			check("empty", workspace, "", emptyToken, "", 200)
			check("active via credential workspace", "", "", token, "", 200,
				"issue", "guest-own", "guest-other", "project", "document", "document-alias", "alias-collision", "inherited", "environment", "chat", "loop")
			check("foreign workspace", "foreign", "", token, adminSession, 401)
			check("guest requires anchor", workspace, "", "", guestSession, 400)
			check("guest sees only own coding session", workspace, "?issueId="+issue, "", guestSession, 200, "issue", "guest-own")
			check("archive opt-in", workspace, "?issueId="+issue+"&includeArchived=true", token, "", 200, "issue", "guest-own", "guest-other", "archived")
			check("private issue", workspace, "?issueId="+privateIssue, token, "", 404)
			check("private child project", workspace, anchor("project", "poll-private-project"), token, "", 404)
			check("unscoped project", workspace, anchor("project", "poll-unscoped-project"), token, "", 404)
			check("private inherited document", workspace, anchor("document", "poll-closed-link"), token, "", 404)
			check("document grant for guest", workspace, anchor("document", "poll-grant"), "", guestSession, 200, "grant")
			check("document grant excluded by key", workspace, anchor("document", "poll-grant"), token, "", 404)
			check("selected-empty issue", workspace, "?issueId="+issue, "poll-personal", "", 404)
			check("selected-empty project", workspace, anchor("project", "poll-project"), "poll-personal", "", 404)
			check("selected-empty document", workspace, anchor("document", "poll-document"), "poll-personal", "", 404)
			check("private chat", workspace, anchor(codingAnchorAgentChat, "poll-chat"), "", guestSession, 404)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, agentTaskPollRequest(http.MethodHead, workspace, "", emptyToken, ""))
			if w.Code != http.StatusOK {
				t.Errorf("HEAD returned HTTP %d", w.Code)
			}

			check("pre-revocation", workspace, "", revocationTokens[i], "", 200, fmt.Sprintf("poll-in-flight-%d", i))
			// Revoke after the middleware has authenticated this bearer and
			// before the same request reads and delivers its pending task.
			mux := http.NewServeMux()
			entered := false
			mux.HandleFunc("GET /api/agent-tasks", func(w http.ResponseWriter, r *http.Request) {
				entered = true
				if err := repo.RevokeOAuthAccessToken(r.Context(), secretHash(revocationTokens[i])); err != nil {
					t.Fatal("could not revoke in-flight poll credential")
				}
				service.listApplicationAgentTasks(w, r)
			})
			w = httptest.NewRecorder()
			service.authenticate(mux).ServeHTTP(w, agentTaskPollRequest(http.MethodGet, workspace, "", revocationTokens[i], adminSession))
			if !entered || w.Code != http.StatusUnauthorized {
				t.Errorf("in-flight revocation entered=%t, HTTP %d", entered, w.Code)
			}
		})
	}

	if err := repo.RevokeOAuthAccessToken(t.Context(), secretHash(token)); err != nil {
		t.Fatal("could not revoke poll credential")
	}
	for _, source := range []*store.SQLiteStore{repo, bounded} {
		w := httptest.NewRecorder()
		newHandler(&server{store: source, uploadPath: t.TempDir()}).ServeHTTP(w, agentTaskPollRequest(http.MethodGet, workspace, "", token, adminSession))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("bounded=%t: revoked bearer with a cookie got HTTP %d", source.BoundedMetadataEnabled(), w.Code)
		}
	}
}

func TestAgentTaskEmptyPollAllocation(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	repo := pulseTestRepo(t)
	data := repo.Bootstrap()
	workspace := data.Workspace.URLKey
	_, token := agentTaskPollCredential(t, repo, workspace, data.Viewer.ID, "allocation", []string{data.Teams[0].ID})
	handler := newHandler(&server{store: repo, uploadPath: t.TempDir()})
	poll := func() {
		t.Helper()
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, agentTaskPollRequest(http.MethodGet, workspace, "", token, ""))
		if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != "[]" {
			t.Fatalf("empty poll returned HTTP %d or a nonempty list", w.Code)
		}
	}
	allocated := func() uint64 {
		const runs = 12
		poll()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		for range runs {
			poll()
		}
		runtime.ReadMemStats(&after)
		return (after.TotalAlloc - before.TotalAlloc) / runs
	}
	small := allocated()
	if err := repo.MutateWorkspace(t.Context(), workspace, "test.poll_size", "", nil, func(next *domain.Bootstrap) error {
		for i := range 1000 {
			ref := fmt.Sprintf("poll-size-%d", i)
			next.Projects = append(next.Projects, domain.Project{ID: ref, TeamIDs: []string{data.Teams[0].ID}, MemberIDs: []string{data.Viewer.ID}})
			next.Documents = append(next.Documents, domain.Document{ID: ref, TeamIDs: []string{data.Teams[0].ID}, Creator: data.Viewer,
				Content: strings.Repeat("Synthetic content. ", 200), PreviousSlugIDs: []string{ref + "-old"}})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	large := allocated()
	t.Logf("bytes allocated per empty poll: small=%d large=%d", small, large)
	if large > small+(256<<10) {
		t.Fatal("empty polling allocations grow with workspace metadata")
	}
}
