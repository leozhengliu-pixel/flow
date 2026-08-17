package main

import (
	"bytes"
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
