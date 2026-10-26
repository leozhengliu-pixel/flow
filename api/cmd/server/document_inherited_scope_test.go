package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// TestDocumentInheritsPrivateTeamScopeFromItsLinks covers the scope a
// document inherits from the issue and projects it is attached to: only a
// private team narrows access, the document's grants and its own teams are
// never rewritten, and the scope follows link changes, issue team moves and
// project team changes.
func TestDocumentInheritsPrivateTeamScopeFromItsLinks(t *testing.T) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "document-scope.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	s := &server{store: repository, uploadPath: t.TempDir()}
	host := httptest.NewServer(newHandler(s))
	defer host.Close()
	const workspace = "test-workspace"

	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, http.MethodPost, host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	member := func(name, email string) (*http.Client, domain.User) {
		client, user := verifiedAuthClient(t, host.URL, name, email)
		invitation := authRequest[[]domain.Invitation](t, admin, http.MethodPost, host.URL+"/api/workspaces/"+workspace+"/invitations", map[string]any{"emails": []string{user.Email}, "role": "member"}, "", http.StatusCreated)
		authRequest[domain.WorkspaceMembership](t, client, http.MethodPost, host.URL+"/api/invitations/accept", map[string]string{"token": invitation[0].Token}, "", http.StatusOK)
		return client, user
	}
	insider, insiderUser := member("Private member", "scope-insider@example.com")
	outsider, _ := member("Workspace member", "scope-outsider@example.com")

	bootstrap := authRequest[domain.Bootstrap](t, admin, http.MethodGet, host.URL+"/api/bootstrap", nil, workspace, http.StatusOK)
	publicTeam := bootstrap.Teams[0]
	privateTeam := authRequest[domain.Team](t, admin, http.MethodPost, host.URL+"/api/workspaces/"+workspace+"/teams", map[string]any{"name": "Scope private", "key": "SCP", "private": true}, "", http.StatusCreated)
	authRequest[any](t, admin, http.MethodPut, host.URL+"/api/workspaces/"+workspace+"/teams/"+privateTeam.ID+"/members/"+insiderUser.ID, map[string]any{"member": true}, "", http.StatusNoContent)
	publicIssue := authRequest[domain.Issue](t, admin, http.MethodPost, host.URL+"/api/issues", map[string]any{"title": "Public issue", "teamId": publicTeam.ID}, workspace, http.StatusCreated)
	privateIssue := authRequest[domain.Issue](t, admin, http.MethodPost, host.URL+"/api/issues", map[string]any{"title": "Private issue", "teamId": privateTeam.ID}, workspace, http.StatusCreated)

	create := func(input map[string]any) domain.Document {
		t.Helper()
		return authRequest[domain.Document](t, admin, http.MethodPost, host.URL+"/api/documents", input, workspace, http.StatusCreated)
	}
	patch := func(client *http.Client, document domain.Document, input map[string]any) domain.Document {
		t.Helper()
		return authRequest[domain.Document](t, client, http.MethodPatch, host.URL+"/api/documents/"+document.ID, input, workspace, http.StatusOK)
	}
	status := func(client *http.Client, method, path string, input any) int {
		t.Helper()
		var body *strings.Reader
		if input != nil {
			raw, _ := json.Marshal(input)
			body = strings.NewReader(string(raw))
		} else {
			body = strings.NewReader("")
		}
		request, _ := http.NewRequest(method, host.URL+path, body)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Workspace-Key", workspace)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode >= 300 {
			raw, _ := io.ReadAll(response.Body)
			t.Logf("%s %s = %d: %s", method, path, response.StatusCode, raw)
		}
		return response.StatusCode
	}
	// access reports whether client can read (GET, bootstrap) and edit the document.
	access := func(client *http.Client, document domain.Document) (bool, bool) {
		t.Helper()
		read := status(client, http.MethodGet, "/api/documents/"+document.ID, nil) == http.StatusOK
		listed := slices.ContainsFunc(authRequest[domain.Bootstrap](t, client, http.MethodGet, host.URL+"/api/bootstrap", nil, workspace, http.StatusOK).Documents, func(item domain.Document) bool { return item.ID == document.ID })
		if read != listed {
			t.Fatalf("document %s: GET readable=%v but bootstrap listed=%v", document.Title, read, listed)
		}
		edit := status(client, http.MethodPatch, "/api/documents/"+document.ID, map[string]any{"content": "edit " + time.Now().Format(time.RFC3339Nano)}) == http.StatusOK
		return read, edit
	}
	expectAccess := func(label string, client *http.Client, document domain.Document, wantRead, wantEdit bool) {
		t.Helper()
		if read, edit := access(client, document); read != wantRead || edit != wantEdit {
			t.Fatalf("%s: read=%v edit=%v, want read=%v edit=%v", label, read, edit, wantRead, wantEdit)
		}
	}
	grants := func(document domain.Document) []string {
		result := []string{}
		for _, permission := range document.Permissions {
			result = append(result, permission.SubjectType+":"+permission.SubjectID+":"+permission.Role)
		}
		slices.Sort(result)
		return result
	}
	reload := func(document domain.Document) domain.Document {
		t.Helper()
		return authRequest[domain.Document](t, admin, http.MethodGet, host.URL+"/api/documents/"+document.ID, nil, workspace, http.StatusOK)
	}

	// Team grants follow the membership records: a member who joined after
	// the workspace snapshot was written can edit the team's document.
	teamDoc := create(map[string]any{"title": "Team doc", "teamIds": []string{privateTeam.ID}})
	expectAccess("explicit private team doc", insider, teamDoc, true, true)
	expectAccess("explicit private team doc, outside the team", outsider, teamDoc, false, false)
	// A document on a public team's issue keeps workspace-wide edit access.
	onPublic := create(map[string]any{"title": "On public issue", "issueId": publicIssue.ID})
	if len(onPublic.TeamIDs) != 0 || !slices.Equal(onPublic.InheritedTeamIDs, []string{publicTeam.ID}) {
		t.Fatalf("public issue document teams=%v inherited=%v", onPublic.TeamIDs, onPublic.InheritedTeamIDs)
	}
	expectAccess("workspace member on public issue document", outsider, onPublic, true, true)

	// A document on a private team's issue is scoped to that team; its
	// grants are the default workspace grant, unchanged.
	onPrivate := create(map[string]any{"title": "On private issue", "issueId": privateIssue.ID})
	unlinked := create(map[string]any{"title": "Unlinked"})
	if !slices.Equal(grantRoles(onPrivate), grantRoles(unlinked)) || !slices.Equal(onPrivate.InheritedTeamIDs, []string{privateTeam.ID}) {
		t.Fatalf("private issue document grants=%v (unlinked %v) inherited=%v", grantRoles(onPrivate), grantRoles(unlinked), onPrivate.InheritedTeamIDs)
	}
	expectAccess("workspace member outside the private team", outsider, onPrivate, false, false)
	expectAccess("private team member", insider, onPrivate, true, true)

	// Linking later gives the same grants and scope as creating on the issue.
	expectAccess("unlinked document", outsider, unlinked, true, true)
	linked := patch(admin, unlinked, map[string]any{"issueId": privateIssue.ID})
	if !slices.Equal(grantRoles(linked), grantRoles(onPrivate)) || !slices.Equal(linked.InheritedTeamIDs, onPrivate.InheritedTeamIDs) || len(linked.TeamIDs) != 0 {
		t.Fatalf("link later grants=%v inherited=%v teams=%v, create on issue grants=%v inherited=%v", grantRoles(linked), linked.InheritedTeamIDs, linked.TeamIDs, grantRoles(onPrivate), onPrivate.InheritedTeamIDs)
	}
	expectAccess("linked later, outside the team", outsider, linked, false, false)

	// Unlinking removes the inherited scope.
	unlinked = patch(admin, linked, map[string]any{"issueId": ""})
	if len(unlinked.InheritedTeamIDs) != 0 || unlinked.IssueTeamID != "" {
		t.Fatalf("unlinked document kept its inherited scope: %v %q", unlinked.InheritedTeamIDs, unlinked.IssueTeamID)
	}
	expectAccess("unlinked again", outsider, unlinked, true, true)

	// An unrelated edit never changes access.
	before := grants(reload(onPrivate))
	edited := patch(insider, onPrivate, map[string]any{"title": "Renamed private"})
	if !slices.Equal(grants(edited), before) || !slices.Equal(edited.InheritedTeamIDs, []string{privateTeam.ID}) || len(edited.TeamIDs) != 0 {
		t.Fatalf("unrelated edit changed access: grants=%v (was %v) inherited=%v teams=%v", grants(edited), before, edited.InheritedTeamIDs, edited.TeamIDs)
	}

	// The user's explicit team choice is respected: clearing it stays cleared.
	chosen := create(map[string]any{"title": "Explicit team", "teamIds": []string{publicTeam.ID}, "issueId": publicIssue.ID})
	cleared := patch(admin, chosen, map[string]any{"teamIds": []string{}})
	cleared = patch(admin, cleared, map[string]any{"title": "Explicit team renamed"})
	if len(cleared.TeamIDs) != 0 {
		t.Fatalf("cleared team selection came back: %v", cleared.TeamIDs)
	}

	// An issue moving to a private team takes its documents into the scope.
	moving := create(map[string]any{"title": "Follows its issue", "issueId": publicIssue.ID})
	expectAccess("before the issue moves", outsider, moving, true, true)
	authRequest[domain.Issue](t, admin, http.MethodPatch, host.URL+"/api/issues/"+publicIssue.ID, map[string]any{"teamId": privateTeam.ID}, workspace, http.StatusOK)
	if moved := reload(moving); !slices.Equal(moved.InheritedTeamIDs, []string{privateTeam.ID}) || !slices.Equal(grants(moved), grants(moving)) {
		t.Fatalf("document did not follow its issue: inherited=%v grants=%v", moved.InheritedTeamIDs, grants(moved))
	}
	expectAccess("after the issue moved to the private team", outsider, moving, false, false)
	expectAccess("private member after the issue moved", insider, moving, true, true)

	// A project's teams changing updates its documents' scope.
	project := authRequest[domain.Project](t, admin, http.MethodPost, host.URL+"/api/projects", map[string]any{"name": "Scoped project", "teamIds": []string{publicTeam.ID}}, workspace, http.StatusCreated)
	onProject := create(map[string]any{"title": "Project spec", "projectIds": []string{project.ID}})
	expectAccess("public project document", outsider, onProject, true, true)
	authRequest[domain.Project](t, admin, http.MethodPatch, host.URL+"/api/projects/"+project.ID, map[string]any{"teamIds": []string{privateTeam.ID}}, workspace, http.StatusOK)
	expectAccess("project moved to the private team", outsider, onProject, false, false)
	expectAccess("private member on the private project document", insider, onProject, true, true)
	authRequest[domain.Project](t, admin, http.MethodPatch, host.URL+"/api/projects/"+project.ID, map[string]any{"teamIds": []string{publicTeam.ID}}, workspace, http.StatusOK)
	expectAccess("project back in the public team", outsider, onProject, true, true)

	// A document shared with a user directly stays shared outside the scope.
	authRequest[[]domain.DocumentPermission](t, admin, http.MethodPut, host.URL+"/api/documents/"+onPrivate.ID+"/permissions", map[string]any{"permissions": []map[string]string{
		{"subjectType": "workspace", "subjectId": "", "role": "editor"},
	}}, workspace, http.StatusOK)
	expectAccess("workspace grant still scoped", outsider, onPrivate, false, false)

	// A stored document from before inherited scopes keeps its access on its
	// next unrelated edit.
	var legacyID string
	if err := repository.MutateWorkspace(t.Context(), workspace, "test.legacy_document", "", nil, func(data *domain.Bootstrap) error {
		for index := range data.Documents {
			if data.Documents[index].ID == moving.ID {
				data.Documents[index].InheritedTeamIDs, data.Documents[index].IssueTeamID = nil, ""
				legacyID = moving.ID
			}
		}
		return nil
	}); err != nil || legacyID == "" {
		t.Fatalf("prepare legacy document: %v", err)
	}
	expectAccess("legacy document on a private issue", outsider, moving, true, true)
	patch(admin, moving, map[string]any{"title": "Legacy renamed"})
	expectAccess("legacy document after an unrelated edit", outsider, moving, true, true)

	// Application principals are limited to their teams: an app installed in
	// the public team reads the documents attached to that team's issues and
	// projects (without the document being narrowed to the team), not other
	// workspace documents, and not a document scoped to a private team; an app
	// installed in that private team reads it.
	publicApp := installScopeTestApplication(t, s, admin, host.URL, "scope-public-app", publicTeam.ID, "public-token")
	privateApp := installScopeTestApplication(t, s, admin, host.URL, "scope-private-app", privateTeam.ID, "private-token")
	onOtherPublic := create(map[string]any{"title": "Second public", "issueId": authRequest[domain.Issue](t, admin, http.MethodPost, host.URL+"/api/issues", map[string]any{"title": "Another public issue", "teamId": publicTeam.ID}, workspace, http.StatusCreated).ID})
	for _, check := range []struct {
		token    string
		document domain.Document
		want     int
	}{
		{publicApp, onOtherPublic, http.StatusOK},
		{publicApp, onProject, http.StatusOK},
		{publicApp, unlinked, http.StatusNotFound},
		{publicApp, onPrivate, http.StatusNotFound},
		{privateApp, onPrivate, http.StatusOK},
		{privateApp, onOtherPublic, http.StatusNotFound},
	} {
		request, _ := http.NewRequest(http.MethodGet, host.URL+"/api/documents/"+check.document.ID, nil)
		request.Header.Set("Authorization", "Bearer "+check.token)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != check.want {
			t.Errorf("application %s reading %q = %d, want %d: %s", check.token, check.document.Title, response.StatusCode, check.want, raw)
		}
	}
}

func grantRoles(document domain.Document) []string {
	result := []string{}
	for _, permission := range document.Permissions {
		subject := permission.SubjectID
		if permission.SubjectType == "user" {
			subject = "user"
		}
		result = append(result, permission.SubjectType+":"+subject+":"+permission.Role)
	}
	slices.Sort(result)
	return result
}

// installScopeTestApplication installs an OAuth application limited to one
// team and returns an app actor token for it.
func installScopeTestApplication(t *testing.T, s *server, admin *http.Client, baseURL, clientID, teamID, token string) string {
	t.Helper()
	ctx := t.Context()
	if err := s.store.RegisterOAuthClient(ctx, domain.OAuthClient{ClientID: clientID, ClientName: clientID, RedirectURIs: []string{"https://example.test/callback"}}); err != nil {
		t.Fatal(err)
	}
	installed := authRequest[struct {
		Application domain.ApplicationInstallation `json:"application"`
	}](t, admin, http.MethodPost, baseURL+"/api/application-installations", map[string]any{"clientId": clientID, "teamIds": []string{teamID}, "scopes": []string{"read"}, "active": true}, "test-workspace", http.StatusOK)
	bootstrap := authRequest[domain.Bootstrap](t, admin, http.MethodGet, baseURL+"/api/bootstrap", nil, "test-workspace", http.StatusOK)
	code := clientID + "-code"
	if _, err := s.store.CreateOAuthAuthorizationGrant(ctx, code, domain.OAuthAuthorizationCode{ClientID: clientID, Actor: "app", InstallerID: bootstrap.Viewer.ID, UserID: bootstrap.Viewer.ID, WorkspaceKey: "test-workspace", TeamIDs: installed.Application.TeamIDs, Scopes: []string{"read"}, ExpiresAt: time.Now().Add(time.Hour)}, domain.OAuthAuthorization{ID: clientID + "-grant"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.ExchangeOAuthGrant(ctx, "authorization_code", code, clientID, clientID+"-refresh", domain.APIKey{ID: clientID + "-access", SecretHash: secretHash(token), CreatedAt: time.Now()}, func(domain.OAuthAuthorizationCode) bool { return true }, nil); err != nil {
		t.Fatal(err)
	}
	return token
}
