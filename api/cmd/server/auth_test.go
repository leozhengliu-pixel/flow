package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestAuthenticationInvitationAndAuthorizationLifecycle(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	server := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir()}))
	defer server.Close()
	admin := authClient(t)
	member := authClient(t)

	authRequest[any](t, member, http.MethodGet, server.URL+"/api/bootstrap", nil, "cleantrack", http.StatusUnauthorized)
	authRequest[domain.AuthSession](t, admin, http.MethodPost, server.URL+"/api/auth/login", map[string]string{"email": "leo.zheng.liu@example.com", "password": "flow-demo"}, "", http.StatusOK)

	registered := authRequest[struct {
		User              domain.User `json:"user"`
		VerificationToken string      `json:"verificationToken"`
	}](t, member, http.MethodPost, server.URL+"/api/auth/register", map[string]string{"name": "Invitee", "email": "invitee@example.com", "password": "initial-pass"}, "", http.StatusCreated)
	if registered.VerificationToken == "" {
		t.Fatal("development verification token missing")
	}
	authRequest[any](t, member, http.MethodPost, server.URL+"/api/auth/login", map[string]string{"email": "invitee@example.com", "password": "initial-pass"}, "", http.StatusUnauthorized)
	authRequest[any](t, member, http.MethodPost, server.URL+"/api/auth/verify-email", map[string]string{"token": registered.VerificationToken}, "", http.StatusOK)
	authRequest[domain.AuthSession](t, member, http.MethodPost, server.URL+"/api/auth/login", map[string]string{"email": "invitee@example.com", "password": "initial-pass"}, "", http.StatusOK)

	bootstrap := authRequest[domain.Bootstrap](t, admin, http.MethodGet, server.URL+"/api/bootstrap", nil, "cleantrack", http.StatusOK)
	if bootstrap.ViewerRole != "admin" || len(bootstrap.Members) == 0 {
		t.Fatalf("admin bootstrap missing auth projection: %#v", bootstrap)
	}
	invites := authRequest[[]domain.Invitation](t, admin, http.MethodPost, server.URL+"/api/workspaces/cleantrack/invitations", map[string]any{"emails": []string{"invitee@example.com"}, "role": "member", "teamIds": []string{bootstrap.Teams[0].ID}}, "", http.StatusCreated)
	if len(invites) != 1 || invites[0].Token == "" {
		t.Fatalf("invitation token missing: %#v", invites)
	}
	preview := authRequest[map[string]any](t, authClient(t), http.MethodGet, server.URL+"/api/invitations/preview/"+invites[0].Token, nil, "", http.StatusOK)
	if preview["email"] != "invitee@example.com" {
		t.Fatalf("invitation preview = %#v", preview)
	}
	authRequest[domain.WorkspaceMembership](t, member, http.MethodPost, server.URL+"/api/invitations/accept", map[string]string{"token": invites[0].Token}, "", http.StatusOK)
	account := authRequest[domain.AccountBootstrap](t, member, http.MethodGet, server.URL+"/api/account/bootstrap", nil, "", http.StatusOK)
	if len(account.Workspaces) != 1 || account.Workspaces[0].Role != "Member" {
		t.Fatalf("accepted membership = %#v", account.Workspaces)
	}

	privateTeam := authRequest[domain.Team](t, admin, http.MethodPost, server.URL+"/api/workspaces/cleantrack/teams", map[string]any{"name": "Private", "key": "PRV", "private": true}, "", http.StatusCreated)
	hiddenIssue := authRequest[domain.Issue](t, admin, http.MethodPost, server.URL+"/api/issues", map[string]any{"title": "Private issue", "teamId": privateTeam.ID}, "cleantrack", http.StatusCreated)
	authRequest[any](t, member, http.MethodPatch, server.URL+"/api/issues/"+hiddenIssue.ID, map[string]string{"title": "Not allowed"}, "cleantrack", http.StatusForbidden)
	attachment := uploadAuthAttachment(t, admin, server.URL, hiddenIssue.ID, "cleantrack", http.StatusCreated)
	authStatus(t, member, http.MethodGet, server.URL+attachment.URL, http.StatusNotFound)
	authStatus(t, admin, http.MethodGet, server.URL+attachment.URL, http.StatusOK)
	authRequest[any](t, member, http.MethodPatch, server.URL+"/api/workspaces/cleantrack/teams/"+privateTeam.ID, map[string]string{"name": "Not allowed"}, "", http.StatusForbidden)
	authRequest[any](t, admin, http.MethodPut, server.URL+"/api/workspaces/cleantrack/teams/"+privateTeam.ID+"/members/"+registered.User.ID, map[string]any{"member": true, "role": "owner"}, "", http.StatusNoContent)
	authRequest[domain.Team](t, member, http.MethodPatch, server.URL+"/api/workspaces/cleantrack/teams/"+privateTeam.ID, map[string]string{"name": "Owner managed"}, "", http.StatusOK)
	authRequest[any](t, member, http.MethodPost, server.URL+"/api/workspaces/cleantrack/invitations", map[string]any{"emails": []string{"blocked@example.com"}}, "", http.StatusForbidden)

	personalWorkspace := authRequest[domain.Bootstrap](t, member, http.MethodPost, server.URL+"/api/workspaces", map[string]string{"name": "Invitee Space", "urlKey": "invitee-space"}, "", http.StatusCreated)
	authRequest[any](t, member, http.MethodPatch, server.URL+"/api/workspaces/invitee-space/members/"+registered.User.ID, map[string]string{"role": "member"}, "", http.StatusConflict)
	if personalWorkspace.Viewer.ID != registered.User.ID {
		t.Fatalf("workspace actor = %q, want %q", personalWorkspace.Viewer.ID, registered.User.ID)
	}

	authRequest[domain.WorkspaceMember](t, admin, http.MethodPatch, server.URL+"/api/workspaces/cleantrack/members/"+registered.User.ID, map[string]string{"role": "guest"}, "", http.StatusOK)
	guestBootstrap := authRequest[domain.Bootstrap](t, member, http.MethodGet, server.URL+"/api/bootstrap", nil, "cleantrack", http.StatusOK)
	if guestBootstrap.ViewerRole != "guest" || len(guestBootstrap.Initiatives) != 0 || len(guestBootstrap.Customers) != 0 || len(guestBootstrap.Invitations) != 0 {
		t.Fatalf("guest projection leaked workspace resources: role=%s initiatives=%d customers=%d invitations=%d", guestBootstrap.ViewerRole, len(guestBootstrap.Initiatives), len(guestBootstrap.Customers), len(guestBootstrap.Invitations))
	}

	authRequest[any](t, admin, http.MethodPost, server.URL+"/api/workspaces/cleantrack/members/"+registered.User.ID+"/suspend", nil, "", http.StatusNoContent)
	authRequest[domain.AuthSession](t, member, http.MethodGet, server.URL+"/api/auth/session", nil, "", http.StatusOK)
	authRequest[any](t, member, http.MethodGet, server.URL+"/api/bootstrap", nil, "cleantrack", http.StatusForbidden)
	remaining := authRequest[domain.AccountBootstrap](t, member, http.MethodGet, server.URL+"/api/account/bootstrap", nil, "", http.StatusOK)
	if len(remaining.Workspaces) != 1 || remaining.Workspaces[0].Workspace.URLKey != "invitee-space" {
		t.Fatalf("suspension leaked or removed the wrong workspace memberships: %#v", remaining.Workspaces)
	}
}

func TestPasswordResetRevokesSessions(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "reset.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	server := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir()}))
	defer server.Close()
	client := authClient(t)
	authRequest[domain.AuthSession](t, client, http.MethodPost, server.URL+"/api/auth/login", map[string]string{"email": "leo.zheng.liu@example.com", "password": "flow-demo"}, "", http.StatusOK)
	reset := authRequest[struct {
		ResetToken string `json:"resetToken"`
	}](t, authClient(t), http.MethodPost, server.URL+"/api/auth/forgot-password", map[string]string{"email": "leo.zheng.liu@example.com"}, "", http.StatusOK)
	authRequest[any](t, authClient(t), http.MethodPost, server.URL+"/api/auth/reset-password", map[string]string{"token": reset.ResetToken, "password": "updated-pass"}, "", http.StatusOK)
	authRequest[any](t, client, http.MethodGet, server.URL+"/api/auth/session", nil, "", http.StatusUnauthorized)
	authRequest[domain.AuthSession](t, authClient(t), http.MethodPost, server.URL+"/api/auth/login", map[string]string{"email": "leo.zheng.liu@example.com", "password": "updated-pass"}, "", http.StatusOK)
}

func TestAPIKeyScopesAndTeamProjection(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "api-key.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	server := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir()}))
	defer server.Close()
	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, http.MethodPost, server.URL+"/api/auth/login", map[string]string{"email": "leo.zheng.liu@example.com", "password": "flow-demo"}, "", http.StatusOK)
	bootstrap := authRequest[domain.Bootstrap](t, admin, http.MethodGet, server.URL+"/api/bootstrap", nil, "cleantrack", http.StatusOK)
	team := authRequest[domain.Team](t, admin, http.MethodPost, server.URL+"/api/workspaces/cleantrack/teams", map[string]any{"name": "API hidden", "key": "APIH"}, "", http.StatusCreated)
	authRequest[domain.Issue](t, admin, http.MethodPost, server.URL+"/api/issues", map[string]any{"title": "Hidden from scoped key", "teamId": team.ID}, "cleantrack", http.StatusCreated)
	key := authRequest[struct {
		Secret string `json:"secret"`
	}](t, admin, http.MethodPost, server.URL+"/api/api-keys", map[string]any{"name": "Scoped reader", "scopes": []string{"read"}, "teamIds": []string{bootstrap.Teams[0].ID}}, "cleantrack", http.StatusCreated)
	request, err := http.NewRequest(http.MethodGet, server.URL+"/api/bootstrap", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+key.Secret)
	request.Header.Set("X-Workspace-Key", "cleantrack")
	response, err := authClient(t).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("scoped bootstrap status = %d", response.StatusCode)
	}
	var projected domain.Bootstrap
	if err := json.NewDecoder(response.Body).Decode(&projected); err != nil {
		t.Fatal(err)
	}
	if len(projected.Teams) != 1 || projected.Teams[0].ID != bootstrap.Teams[0].ID || slices.ContainsFunc(projected.Issues, func(item domain.Issue) bool { return item.Team.ID == team.ID }) {
		t.Fatalf("API key team projection leaked resources: teams=%#v issues=%#v", projected.Teams, projected.Issues)
	}
	writeRequest, _ := http.NewRequest(http.MethodPost, server.URL+"/api/issues", bytes.NewBufferString(`{"title":"Denied","teamId":"`+bootstrap.Teams[0].ID+`"}`))
	writeRequest.Header.Set("Authorization", "Bearer "+key.Secret)
	writeRequest.Header.Set("X-Workspace-Key", "cleantrack")
	writeRequest.Header.Set("Content-Type", "application/json")
	writeResponse, err := authClient(t).Do(writeRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer writeResponse.Body.Close()
	if writeResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("read-only key write status = %d", writeResponse.StatusCode)
	}
	app := authRequest[domain.OAuthApplication](t, admin, http.MethodPost, server.URL+"/api/oauth-applications", map[string]any{"name": "Automation client", "redirectUris": []string{}, "scopes": []string{"read"}}, "cleantrack", http.StatusCreated)
	token := authRequest[struct {
		AccessToken string `json:"access_token"`
	}](t, authClient(t), http.MethodPost, server.URL+"/api/oauth/token", map[string]string{"grant_type": "client_credentials", "client_id": app.ClientID, "client_secret": app.ClientSecret}, "cleantrack", http.StatusOK)
	if token.AccessToken == "" {
		t.Fatal("OAuth client credentials exchange returned no access token")
	}
	oauthRequest, _ := http.NewRequest(http.MethodGet, server.URL+"/api/bootstrap", nil)
	oauthRequest.Header.Set("Authorization", "Bearer "+token.AccessToken)
	oauthRequest.Header.Set("X-Workspace-Key", "cleantrack")
	oauthResponse, err := authClient(t).Do(oauthRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer oauthResponse.Body.Close()
	if oauthResponse.StatusCode != http.StatusOK {
		t.Fatalf("OAuth access token status = %d", oauthResponse.StatusCode)
	}
}

func TestReleaseAuthorizationAndFeatureGate(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "release-auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	server := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir()}))
	defer server.Close()

	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, http.MethodPost, server.URL+"/api/auth/login", map[string]string{"email": "leo.zheng.liu@example.com", "password": "flow-demo"}, "", http.StatusOK)
	bootstrap := authRequest[domain.Bootstrap](t, admin, http.MethodGet, server.URL+"/api/bootstrap", nil, "cleantrack", http.StatusOK)
	publicTeam := bootstrap.Teams[0]

	member, memberUser := verifiedAuthClient(t, server.URL, "Release member", "release-member@example.com")
	guest, guestUser := verifiedAuthClient(t, server.URL, "Release guest", "release-guest@example.com")
	for _, invitation := range []struct {
		client *http.Client
		user   domain.User
		role   string
	}{
		{client: member, user: memberUser, role: "member"},
		{client: guest, user: guestUser, role: "guest"},
	} {
		invites := authRequest[[]domain.Invitation](t, admin, http.MethodPost, server.URL+"/api/workspaces/cleantrack/invitations", map[string]any{
			"emails": []string{invitation.user.Email}, "role": invitation.role, "teamIds": []string{publicTeam.ID},
		}, "", http.StatusCreated)
		authRequest[domain.WorkspaceMembership](t, invitation.client, http.MethodPost, server.URL+"/api/invitations/accept", map[string]string{"token": invites[0].Token}, "", http.StatusOK)
	}

	privateTeam := authRequest[domain.Team](t, admin, http.MethodPost, server.URL+"/api/workspaces/cleantrack/teams", map[string]any{"name": "Release private", "key": "RPRV", "private": true}, "", http.StatusCreated)
	publicIssue := authRequest[domain.Issue](t, admin, http.MethodPost, server.URL+"/api/issues", map[string]any{"title": "Public release issue", "teamId": publicTeam.ID}, "cleantrack", http.StatusCreated)
	hiddenIssue := authRequest[domain.Issue](t, admin, http.MethodPost, server.URL+"/api/issues", map[string]any{"title": "Private release issue", "teamId": privateTeam.ID}, "cleantrack", http.StatusCreated)
	hiddenProject := authRequest[domain.Project](t, admin, http.MethodPost, server.URL+"/api/projects", map[string]any{"name": "Private release project", "teamIds": []string{privateTeam.ID}}, "cleantrack", http.StatusCreated)
	publicPipeline := authRequest[domain.ReleasePipeline](t, admin, http.MethodPost, server.URL+"/api/release-pipelines", map[string]any{"name": "Public deploys", "teamIds": []string{publicTeam.ID}}, "cleantrack", http.StatusCreated)
	privatePipeline := authRequest[domain.ReleasePipeline](t, admin, http.MethodPost, server.URL+"/api/release-pipelines", map[string]any{"name": "Private deploys", "teamIds": []string{privateTeam.ID}}, "cleantrack", http.StatusCreated)
	publicRelease := authRequest[domain.Release](t, admin, http.MethodPost, server.URL+"/api/releases", map[string]any{"name": "Public release", "pipelineId": publicPipeline.ID}, "cleantrack", http.StatusCreated)
	privateRelease := authRequest[domain.Release](t, admin, http.MethodPost, server.URL+"/api/releases", map[string]any{
		"name": "Private release", "pipelineId": privatePipeline.ID, "issueIds": []string{hiddenIssue.ID}, "projectIds": []string{hiddenProject.ID},
	}, "cleantrack", http.StatusCreated)

	memberBootstrap := authRequest[domain.Bootstrap](t, member, http.MethodGet, server.URL+"/api/bootstrap", nil, "cleantrack", http.StatusOK)
	if !slices.ContainsFunc(memberBootstrap.ReleasePipelines, func(item domain.ReleasePipeline) bool { return item.ID == publicPipeline.ID }) ||
		slices.ContainsFunc(memberBootstrap.ReleasePipelines, func(item domain.ReleasePipeline) bool { return item.ID == privatePipeline.ID }) ||
		!slices.ContainsFunc(memberBootstrap.Releases, func(item domain.Release) bool { return item.ID == publicRelease.ID }) ||
		slices.ContainsFunc(memberBootstrap.Releases, func(item domain.Release) bool { return item.ID == privateRelease.ID }) {
		t.Fatalf("member release projection leaked private resources: pipelines=%#v releases=%#v", memberBootstrap.ReleasePipelines, memberBootstrap.Releases)
	}
	listedPipelines := authRequest[[]domain.ReleasePipeline](t, member, http.MethodGet, server.URL+"/api/release-pipelines", nil, "cleantrack", http.StatusOK)
	listedReleases := authRequest[[]domain.Release](t, member, http.MethodGet, server.URL+"/api/releases", nil, "cleantrack", http.StatusOK)
	if slices.ContainsFunc(listedPipelines, func(item domain.ReleasePipeline) bool { return item.ID == privatePipeline.ID }) || slices.ContainsFunc(listedReleases, func(item domain.Release) bool { return item.ID == privateRelease.ID }) {
		t.Fatal("release list API leaked private resources")
	}
	guestBootstrap := authRequest[domain.Bootstrap](t, guest, http.MethodGet, server.URL+"/api/bootstrap", nil, "cleantrack", http.StatusOK)
	if slices.ContainsFunc(guestBootstrap.ReleasePipelines, func(item domain.ReleasePipeline) bool { return item.ID == privatePipeline.ID }) ||
		slices.ContainsFunc(guestBootstrap.Releases, func(item domain.Release) bool { return item.ID == privateRelease.ID }) {
		t.Fatal("guest release projection leaked private resources")
	}
	authRequest[domain.Release](t, member, http.MethodPatch, server.URL+"/api/releases/"+publicRelease.ID, map[string]any{"status": "inProgress"}, "cleantrack", http.StatusOK)
	authRequest[[]domain.Release](t, member, http.MethodPut, server.URL+"/api/issues/"+publicIssue.ID+"/releases", map[string]any{"releaseIds": []string{publicRelease.ID}}, "cleantrack", http.StatusOK)
	authRequest[any](t, member, http.MethodPut, server.URL+"/api/issues/"+publicIssue.ID+"/releases", map[string]any{"releaseIds": []string{privateRelease.ID}}, "cleantrack", http.StatusForbidden)
	authRequest[any](t, member, http.MethodGet, server.URL+"/api/release-pipelines/"+privatePipeline.ID, nil, "cleantrack", http.StatusForbidden)
	authRequest[any](t, member, http.MethodGet, server.URL+"/api/releases/"+privateRelease.ID, nil, "cleantrack", http.StatusForbidden)
	authRequest[any](t, member, http.MethodPatch, server.URL+"/api/releases/"+publicRelease.ID, map[string]any{"pipelineId": privatePipeline.ID}, "cleantrack", http.StatusForbidden)
	authRequest[any](t, member, http.MethodPost, server.URL+"/api/releases", map[string]any{"name": "Hidden association", "issueIds": []string{hiddenIssue.ID}}, "cleantrack", http.StatusForbidden)
	authRequest[any](t, member, http.MethodPost, server.URL+"/api/releases/reorder", map[string]any{"pipelineId": privatePipeline.ID, "ids": []string{privateRelease.ID}}, "cleantrack", http.StatusForbidden)
	authRequest[any](t, member, http.MethodDelete, server.URL+"/api/releases/"+privateRelease.ID, nil, "cleantrack", http.StatusForbidden)

	for _, client := range []*http.Client{member, guest} {
		authRequest[any](t, client, http.MethodPost, server.URL+"/api/release-pipelines", map[string]any{"name": "Denied", "teamIds": []string{publicTeam.ID}}, "cleantrack", http.StatusForbidden)
		authRequest[any](t, client, http.MethodPatch, server.URL+"/api/release-pipelines/"+publicPipeline.ID, map[string]any{"name": "Denied"}, "cleantrack", http.StatusForbidden)
		authRequest[any](t, client, http.MethodPost, server.URL+"/api/release-pipelines/reorder", map[string]any{"ids": []string{publicPipeline.ID}}, "cleantrack", http.StatusForbidden)
		authRequest[any](t, client, http.MethodPost, server.URL+"/api/release-pipelines/"+publicPipeline.ID+"/access-key", nil, "cleantrack", http.StatusForbidden)
		authRequest[any](t, client, http.MethodDelete, server.URL+"/api/release-pipelines/"+publicPipeline.ID, nil, "cleantrack", http.StatusForbidden)
	}
	adminKey := authRequest[releasePipelineAccessKey](t, admin, http.MethodPost, server.URL+"/api/release-pipelines/"+publicPipeline.ID+"/access-key", nil, "cleantrack", http.StatusCreated)
	if adminKey.Secret == "" {
		t.Fatal("admin access-key rotation returned no secret")
	}
	authRequest[any](t, admin, http.MethodPost, server.URL+"/api/release-pipelines", map[string]any{"name": "Unknown team", "teamIds": []string{"team_unknown"}}, "cleantrack", http.StatusForbidden)
	authRequest[any](t, admin, http.MethodPatch, server.URL+"/api/release-pipelines/"+publicPipeline.ID, map[string]any{"teamIds": []string{"team_unknown"}}, "cleantrack", http.StatusForbidden)
	authRequest[any](t, admin, http.MethodPost, server.URL+"/api/release-pipelines/reorder", map[string]any{"ids": []string{"release_pipeline_unknown"}}, "cleantrack", http.StatusForbidden)
	disposablePipeline := authRequest[domain.ReleasePipeline](t, admin, http.MethodPost, server.URL+"/api/release-pipelines", map[string]any{"name": "Disposable", "teamIds": []string{publicTeam.ID}}, "cleantrack", http.StatusCreated)
	authRequest[any](t, admin, http.MethodDelete, server.URL+"/api/release-pipelines/"+disposablePipeline.ID, nil, "cleantrack", http.StatusNoContent)
	adminBootstrap := authRequest[domain.Bootstrap](t, admin, http.MethodGet, server.URL+"/api/bootstrap", nil, "cleantrack", http.StatusOK)
	trashIndex := slices.IndexFunc(adminBootstrap.Trash, func(item domain.TrashEntry) bool {
		return item.ResourceType == "release_pipeline" && item.ResourceID == disposablePipeline.ID
	})
	if trashIndex < 0 {
		t.Fatal("deleted pipeline did not enter trash")
	}
	pipelineTrashID := adminBootstrap.Trash[trashIndex].ID
	authRequest[any](t, member, http.MethodPost, server.URL+"/api/trash/"+pipelineTrashID+"/restore", nil, "cleantrack", http.StatusForbidden)

	err = repository.MutateWorkspace(context.Background(), "cleantrack", "test.releases_disabled", "workspace", nil, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.FeatureFlags["releases"] = false
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/releases", "/api/release-pipelines"} {
		authRequest[any](t, admin, http.MethodGet, server.URL+path, nil, "cleantrack", http.StatusForbidden)
	}
	authRequest[any](t, admin, http.MethodPost, server.URL+"/api/releases", map[string]any{"name": "Disabled"}, "cleantrack", http.StatusForbidden)
	authRequest[any](t, admin, http.MethodPut, server.URL+"/api/issues/"+publicIssue.ID+"/releases", map[string]any{"releaseIds": []string{publicRelease.ID}}, "cleantrack", http.StatusForbidden)
	authRequest[any](t, admin, http.MethodPost, server.URL+"/api/release-pipelines/"+publicPipeline.ID+"/access-key", nil, "cleantrack", http.StatusForbidden)
	authRequest[any](t, admin, http.MethodPost, server.URL+"/api/trash/"+pipelineTrashID+"/restore", nil, "cleantrack", http.StatusForbidden)
}

func TestAuthenticationRateLimit(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "rate-limit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	server := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir()}))
	defer server.Close()
	client := authClient(t)
	for range 8 {
		authRequest[any](t, client, http.MethodPost, server.URL+"/api/auth/login", map[string]string{"email": "nobody@example.com", "password": "not-the-password"}, "", http.StatusUnauthorized)
	}
	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/auth/login", bytes.NewBufferString(`{"email":"nobody@example.com","password":"not-the-password"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusTooManyRequests || response.Header.Get("Retry-After") == "" {
		t.Fatalf("rate limited response = %d Retry-After=%q", response.StatusCode, response.Header.Get("Retry-After"))
	}
}

func authClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar}
}

func verifiedAuthClient(t *testing.T, baseURL, name, email string) (*http.Client, domain.User) {
	t.Helper()
	client := authClient(t)
	registered := authRequest[struct {
		User              domain.User `json:"user"`
		VerificationToken string      `json:"verificationToken"`
	}](t, client, http.MethodPost, baseURL+"/api/auth/register", map[string]string{"name": name, "email": email, "password": "initial-pass"}, "", http.StatusCreated)
	authRequest[any](t, client, http.MethodPost, baseURL+"/api/auth/verify-email", map[string]string{"token": registered.VerificationToken}, "", http.StatusOK)
	authRequest[domain.AuthSession](t, client, http.MethodPost, baseURL+"/api/auth/login", map[string]string{"email": email, "password": "initial-pass"}, "", http.StatusOK)
	return client, registered.User
}

func uploadAuthAttachment(t *testing.T, client *http.Client, baseURL, issueID, workspaceKey string, wantStatus int) domain.Attachment {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "permission-check.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("private attachment")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, baseURL+"/api/issues/"+issueID+"/attachments", &body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("X-Workspace-Key", workspaceKey)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode != wantStatus {
		t.Fatalf("upload attachment status %d, want %d: %s", response.StatusCode, wantStatus, raw)
	}
	var attachment domain.Attachment
	if err := json.Unmarshal(raw, &attachment); err != nil {
		t.Fatal(err)
	}
	return attachment
}

func authStatus(t *testing.T, client *http.Client, method, url string, wantStatus int) {
	t.Helper()
	request, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != wantStatus {
		raw, _ := io.ReadAll(response.Body)
		t.Fatalf("%s %s status %d, want %d: %s", method, url, response.StatusCode, wantStatus, raw)
	}
}

func authRequest[T any](t *testing.T, client *http.Client, method, url string, input any, workspaceKey string, wantStatus int) T {
	t.Helper()
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
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
	if response.StatusCode != wantStatus {
		t.Fatalf("%s %s status %d, want %d: %s", method, url, response.StatusCode, wantStatus, raw)
	}
	var result T
	if response.StatusCode != http.StatusNoContent && len(raw) > 0 {
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
	}
	return result
}
