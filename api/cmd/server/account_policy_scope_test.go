package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func sessionTokenOf(t *testing.T, client *http.Client, baseURL string) string {
	t.Helper()
	parsed, _ := url.Parse(baseURL)
	for _, cookie := range client.Jar.Cookies(parsed) {
		if cookie.Name == sessionCookieName {
			return cookie.Value
		}
	}
	t.Fatal("missing test session")
	return ""
}

type policyScopeFixture struct {
	repository *store.SQLiteStore
	baseURL    string
	admin      *http.Client
	token      string
}

func newPolicyScopeFixture(t *testing.T) policyScopeFixture {
	t.Helper()
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "policy-scope.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	api := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir()}))
	t.Cleanup(api.Close)
	admin := peerAdmin(t, api.URL)
	return policyScopeFixture{repository, api.URL, admin, sessionTokenOf(t, admin, api.URL)}
}

func (f policyScopeFixture) mutate(t *testing.T, change func(*domain.Bootstrap)) {
	t.Helper()
	if err := f.repository.MutateWorkspace(store.WithColdWork(t.Context(), "policy fixture"), "test-workspace", "test.policy", "", nil, func(data *domain.Bootstrap) error {
		change(data)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func (f policyScopeFixture) signIn(t *testing.T, provider, issuer string, mfa bool) {
	t.Helper()
	if err := f.repository.SetSessionAuthentication(t.Context(), f.token, provider, issuer, mfa); err != nil {
		t.Fatal(err)
	}
}

// Enforced SSO holds for a browser session on the workspace routes: the
// provider list is read from the workspace, not taken from the request.
func TestEnforcedSingleSignOnAppliesToBrowserSessions(t *testing.T) {
	f := newPolicyScopeFixture(t)
	f.mutate(t, func(data *domain.Bootstrap) {
		data.WorkspaceSettings.DisableAdminBypass = true
		data.IdentityProviders = []domain.IdentityProvider{{ID: "idp", Issuer: "https://sso.example.test", Enabled: true, Enforced: true}}
	})
	blocked := authRequest[map[string]string](t, f.admin, http.MethodGet, f.baseURL+"/api/bootstrap", nil, "test-workspace", http.StatusForbidden)
	if blocked["code"] != "authentication_method_required" {
		t.Fatalf("wrong policy response: %#v", blocked)
	}
	f.signIn(t, "enterprise:idp", "https://sso.example.test", false)
	authRequest[domain.Bootstrap](t, f.admin, http.MethodGet, f.baseURL+"/api/bootstrap", nil, "test-workspace", http.StatusOK)
	// The admin bypass is unchanged while it is on.
	f.signIn(t, "password", "", false)
	f.mutate(t, func(data *domain.Bootstrap) { data.WorkspaceSettings.DisableAdminBypass = false })
	authRequest[domain.Bootstrap](t, f.admin, http.MethodGet, f.baseURL+"/api/bootstrap", nil, "test-workspace", http.StatusOK)
}

// A file of a workspace with an authentication policy is served, and signed,
// only to a session that satisfies that policy, whatever workspace the request
// names (or none).
func TestFilesFollowTheirWorkspaceAuthenticationPolicy(t *testing.T) {
	f := newPolicyScopeFixture(t)
	bootstrap := authRequest[domain.Bootstrap](t, f.admin, http.MethodGet, f.baseURL+"/api/bootstrap", nil, "test-workspace", http.StatusOK)
	other := authRequest[domain.Bootstrap](t, f.admin, http.MethodPost, f.baseURL+"/api/workspaces", map[string]string{"name": "Plain", "urlKey": "plain-space"}, "", http.StatusCreated)
	attachment := uploadAuthAttachment(t, f.admin, f.baseURL, bootstrap.Issues[0].ID, "test-workspace", http.StatusCreated)
	file := f.baseURL + attachment.URL
	sign := func(want int) {
		t.Helper()
		authRequest[any](t, f.admin, http.MethodGet, f.baseURL+"/api/files/sign?url="+url.QueryEscape(attachment.URL), nil, other.Workspace.URLKey, want)
	}
	authStatus(t, f.admin, http.MethodGet, file, http.StatusOK)
	sign(http.StatusOK)

	enforceSSO := func(bypass bool) {
		f.mutate(t, func(data *domain.Bootstrap) {
			data.WorkspaceSettings.DisableAdminBypass = bypass
			data.WorkspaceSettings.RequireTwoFactor = false
			data.IdentityProviders = []domain.IdentityProvider{{ID: "idp", Issuer: "https://sso.example.test", Enabled: true, Enforced: true}}
		})
	}
	enforceSSO(true)
	authStatus(t, f.admin, http.MethodGet, file, http.StatusNotFound)
	sign(http.StatusNotFound)
	f.signIn(t, "enterprise:idp", "https://sso.example.test", false)
	authStatus(t, f.admin, http.MethodGet, file, http.StatusOK)
	sign(http.StatusOK)
	// Admins keep their bypass while it is on.
	f.signIn(t, "password", "", false)
	enforceSSO(false)
	authStatus(t, f.admin, http.MethodGet, file, http.StatusOK)

	f.mutate(t, func(data *domain.Bootstrap) {
		data.IdentityProviders = nil
		data.WorkspaceSettings.RequireTwoFactor = true
	})
	authStatus(t, f.admin, http.MethodGet, file, http.StatusNotFound)
	sign(http.StatusNotFound)
	f.signIn(t, "password", "", true)
	authStatus(t, f.admin, http.MethodGet, file, http.StatusOK)
	sign(http.StatusOK)
}

// The bounded and the original attachment reads skip a workspace whose policy
// the session does not satisfy, and read the others as before.
func TestAttachmentVisibilitySkipsWorkspacesOutsideThePolicy(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21SW268Seed(t, repo)
		workspace := f.data.Workspace.URLKey
		view, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace, f.other)
		if err != nil {
			t.Fatal(err)
		}
		member := f.principals[2].user
		for name, source := range map[string]*store.SQLiteStore{"original": repo, "bounded": view} {
			service := &server{store: source, uploadPath: f.uploads}
			account, err := repo.AccountForUser(t.Context(), member.ID)
			if err != nil {
				t.Fatal(err)
			}
			visible := func(allowed map[string]bool) bool {
				check := sessionPolicyChecker(func(_ context.Context, key, _ string) bool { return allowed[key] })
				ctx := context.WithValue(t.Context(), sessionPolicyKey{}, check)
				return service.attachmentVisible(ctx, account, member.ID, scaleP21SW268URL("issue-public")) ||
					service.attachmentVisible(ctx, account, member.ID, scaleP21SW268URL("other-workspace"))
			}
			if !visible(map[string]bool{workspace: true, f.other: true}) {
				t.Fatalf("%s: files of workspaces that pass the policy are not visible", name)
			}
			if visible(map[string]bool{}) {
				t.Fatalf("%s: files of workspaces outside the policy are visible", name)
			}
			if visible(map[string]bool{workspace: false, f.other: false}) {
				t.Fatalf("%s: denied workspaces are visible", name)
			}
			ctx := context.WithValue(t.Context(), sessionPolicyKey{}, sessionPolicyChecker(func(_ context.Context, key, _ string) bool { return key == f.other }))
			if service.attachmentVisible(ctx, account, member.ID, scaleP21SW268URL("issue-public")) || !service.attachmentVisible(ctx, account, member.ID, scaleP21SW268URL("other-workspace")) {
				t.Fatalf("%s: the policy of one workspace must not decide another's files", name)
			}
		}
	}
}

func TestSessionPolicyAnswersMatchOnBoundedMetadata(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		if err := repo.MutateWorkspace(store.WithColdWork(t.Context(), "workspace fixture"), workspace, "test.policy", "", nil, func(data *domain.Bootstrap) error {
			data.WorkspaceSettings.DisableAdminBypass = true
			data.IdentityProviders = []domain.IdentityProvider{{ID: "idp", Issuer: "https://sso.example.test", Enabled: true, Enforced: true}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for _, auth := range []domain.ChatIdentity{{AuthProvider: "password"}, {AuthProvider: "enterprise:idp"}, {AuthProvider: "oidc", AuthIssuer: "https://sso.example.test/"}} {
			var answers []bool
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				service := &server{store: source}
				r := f.request(f.data.Viewer, nil, nil)
				auth.UserID = f.data.Viewer.ID
				r = r.WithContext(withChatIdentityAuthentication(r.Context(), auth))
				answers = append(answers, service.sessionSatisfiesWorkspacePolicy(r, workspace, "member"))
			}
			want := auth.AuthProvider != "password"
			if answers[0] != want || answers[1] != want {
				t.Fatalf("%s: original=%t bounded=%t, want %t", auth.AuthProvider, answers[0], answers[1], want)
			}
		}
	}
}

func (f policyScopeFixture) addPasskey(t *testing.T) {
	t.Helper()
	f.mutate(t, func(data *domain.Bootstrap) {
		data.Passkeys = append(data.Passkeys, domain.Passkey{ID: "passkey_policy", UserID: "usr_admin", Name: "Key", CredentialJSON: "{}", CreatedAt: time.Now().UTC()})
	})
}

func (f policyScopeFixture) factorRequest(t *testing.T, method, path string, header string, want int) map[string]string {
	t.Helper()
	body := strings.NewReader("{}")
	request, err := http.NewRequest(method, f.baseURL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	// WebAuthn needs a domain relying party, not the test server's IP address.
	request.Header.Set("Origin", "http://localhost:5173")
	if header != "" {
		request.Header.Set("X-Workspace-Key", header)
	}
	response, err := f.admin.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode != want {
		t.Fatalf("%s %s status %d, want %d: %s", method, path, response.StatusCode, want, raw)
	}
	result := map[string]string{}
	_ = json.Unmarshal(raw, &result)
	return result
}

// Changing the account's factors does not depend on a workspace header, and
// an enrolled factor can only be changed shortly after a verification.
func TestAccountFactorChangesNeedRecentVerification(t *testing.T) {
	t.Run("the first passkey needs a session that just signed in", func(t *testing.T) {
		f := newPolicyScopeFixture(t)
		f.factorRequest(t, http.MethodPost, "/api/account/passkeys/register/start", "", http.StatusOK)
		if err := f.repository.BackdateSessionForTesting(t.Context(), f.token, 20*time.Minute); err != nil {
			t.Fatal(err)
		}
		refused := f.factorRequest(t, http.MethodPost, "/api/account/passkeys/register/start", "", http.StatusForbidden)
		if refused["code"] != "session_reauth_required" {
			t.Fatalf("old session: %#v", refused)
		}
		f.factorRequest(t, http.MethodPost, "/api/account/passkeys/register/start", "test-workspace", http.StatusForbidden)
	})
	t.Run("a workspace requiring two-factor decides without a header", func(t *testing.T) {
		f := newPolicyScopeFixture(t)
		f.addPasskey(t)
		f.mutate(t, func(data *domain.Bootstrap) { data.WorkspaceSettings.RequireTwoFactor = true })
		for _, call := range [][2]string{{http.MethodPost, "/api/account/passkeys/register/start"}, {http.MethodPost, "/api/account/passkeys/register/finish"}, {http.MethodPatch, "/api/account/passkeys/passkey_policy"}, {http.MethodDelete, "/api/account/passkeys/passkey_policy"}} {
			if refused := f.factorRequest(t, call[0], call[1], "", http.StatusForbidden); refused["code"] != "mfa_required" {
				t.Fatalf("%s %s: %#v", call[0], call[1], refused)
			}
		}
		f.signIn(t, "password", "", true)
		f.factorRequest(t, http.MethodPost, "/api/account/passkeys/register/start", "", http.StatusOK)
	})
	t.Run("adding or removing a passkey needs a verification within ten minutes", func(t *testing.T) {
		f := newPolicyScopeFixture(t)
		f.addPasskey(t)
		for _, call := range [][2]string{{http.MethodPost, "/api/account/passkeys/register/start"}, {http.MethodDelete, "/api/account/passkeys/passkey_policy"}} {
			if refused := f.factorRequest(t, call[0], call[1], "", http.StatusForbidden); refused["code"] != "mfa_reauth_required" {
				t.Fatalf("unverified %s %s: %#v", call[0], call[1], refused)
			}
		}
		// Renaming is not a factor change.
		authRequest[domain.Passkey](t, f.admin, http.MethodPatch, f.baseURL+"/api/account/passkeys/passkey_policy", map[string]string{"name": "Renamed"}, "", http.StatusOK)
		f.signIn(t, "password", "", true)
		if err := f.repository.BackdateSessionForTesting(t.Context(), f.token, 11*time.Minute); err != nil {
			t.Fatal(err)
		}
		if refused := f.factorRequest(t, http.MethodPost, "/api/account/passkeys/register/start", "", http.StatusForbidden); refused["code"] != "mfa_reauth_required" {
			t.Fatalf("stale verification: %#v", refused)
		}
		f.signIn(t, "password", "", true)
		f.factorRequest(t, http.MethodPost, "/api/account/passkeys/register/start", "", http.StatusOK)
		f.factorRequest(t, http.MethodDelete, "/api/account/passkeys/passkey_policy", "test-workspace", http.StatusNoContent)
	})
}
