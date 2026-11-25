package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func scaleP21SW24GitHub(t *testing.T, repo *store.SQLiteStore) (*server, *atomic.Int64) {
	t.Helper()
	calls := &atomic.Int64{}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/app/installations/42":
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ey") {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"id":42,"account":{"login":"sw24-org","type":"Organization"},"repository_selection":"selected"}`))
		case "/user/installations":
			if r.Header.Get("Authorization") != "Bearer sw24-fake-user-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"total_count":2,"installations":[{"id":42,"account":{"login":"sw24-org","type":"Organization"},"repository_selection":"selected"},{"id":43,"account":{"login":"sw24-other","type":"User"},"repository_selection":"all"}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(api.Close)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	service := &server{store: repo}
	service.initCoding(appconfig.CodingConfig{
		Provider: appconfig.CodingProviderDisabled,
		GitHubApp: appconfig.GitHubAppConfig{
			AppID: "123", Slug: "sw24-app", ClientID: "sw24-client", ClientSecret: "sw24-fake-secret",
			PrivateKey: encoded, WebhookSecret: "sw24-fake-webhook-secret", Host: "github.com", APIURL: api.URL,
		},
	})
	return service, calls
}

func scaleP21SW24GitHubResponse(t *testing.T, service *server, w *httptest.ResponseRecorder, kind string) any {
	t.Helper()
	response := scaleP21M3JSON(t, w.Body.Bytes())
	if w.Code != http.StatusOK || kind != "install" && kind != "authorize" {
		return response
	}
	object, ok := response.(map[string]any)
	if !ok {
		t.Fatal("authorization response is not an object")
	}
	address, _ := object["url"].(string)
	parsed, err := url.Parse(address)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	state := query.Get("state")
	if state == "" {
		t.Fatal("authorization omitted state")
	}
	if kind == "install" {
		signed, err := service.verifyGitHubAppState(state)
		if err != nil || signed.Nonce == "" || signed.Purpose != "install" || signed.Expires < time.Now().Unix() ||
			signed.Expires > time.Now().Add(gitHubAppStateTTL+time.Second).Unix() {
			t.Fatal("installation state signature or lifetime changed", err)
		}
		signed.Nonce, signed.Expires = "<generated-nonce>", 0
		object["state"] = signed
	} else {
		saved, err := service.consumeCodeHostLinkState(t.Context(), state)
		if err != nil || saved.Verifier == "" || saved.ReturnTo != "integration" ||
			query.Get("code_challenge") != pkceChallenge(saved.Verifier) || query.Get("code_challenge_method") != "S256" {
			t.Fatal("account authorization state or PKCE changed", err)
		}
		saved.Verifier = "<generated-verifier>"
		object["state"] = saved
		query.Set("code_challenge", "<generated-challenge>")
	}
	query.Set("state", "<generated-state>")
	parsed.RawQuery = query.Encode()
	object["url"] = parsed.String()
	return response
}

func TestScaleP21SW24GitHubIdentityDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	enableConnectorSecrets(t)
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		base, calls := scaleP21SW24GitHub(t, repo)
		for _, principal := range f.principals {
			account, err := repo.UpsertCodeHostAccount(t.Context(), domain.CodeHostAccount{
				ID: idgen.New("sw24-codehost-"), UserID: principal.user.ID, CodeHost: domain.CodeHostGitHub,
				HostName: "github.com", BaseURL: "https://github.com", ExternalUserID: principal.user.ID,
				Login: "sw24-member", AuthType: domain.CodeHostAuthGitHubApp,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := base.writeCodeHostTokens(t.Context(), account.ID, codeHostAccountTokens{
				AccessToken: "sw24-fake-user-token", ExpiresAt: time.Now().Add(time.Hour),
			}); err != nil {
				t.Fatal(err)
			}
		}
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "loops"), workspace, "scale_p21_sw24.seed", "", nil, func(data *domain.Bootstrap) error {
			for range 70 {
				data.Loops = append(data.Loops, domain.Loop{ID: idgen.New("sw24-unused-loop-"), Instructions: "Unused"})
			}
			data.Loops[0].Instructions = strings.Repeat("unrelated instructions ", 20000)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		before := scaleP21M3Fingerprint(t, repo, workspace, "loops")
		reset := func(linked bool) {
			t.Helper()
			if _, err := repo.UnlinkCodeHostInstallation(t.Context(), workspace, domain.CodeHostGitHub, "42"); err != nil {
				t.Fatal(err)
			}
			if linked {
				if err := repo.UpsertCodeHostInstallation(t.Context(), domain.CodeHostInstallation{
					WorkspaceKey: workspace, CodeHost: domain.CodeHostGitHub, HostName: "github.com",
					InstallationID: "42", AccountLogin: "sw24-org", AccountType: "Organization", RepositorySelection: "selected",
				}); err != nil {
					t.Fatal(err)
				}
			}
		}
		operations := []struct {
			name    string
			handler func(*server, http.ResponseWriter, *http.Request)
			linked  bool
			admin   bool
			want    int
		}{
			{"status", (*server).getGitHubApp, true, false, http.StatusOK},
			{"install", (*server).startGitHubAppInstall, true, true, http.StatusOK},
			{"authorize", (*server).startGitHubAppAuthorization, true, false, http.StatusOK},
			{"remove", (*server).removeGitHubAppInstallation, true, true, http.StatusNoContent},
			{"available", (*server).listAvailableGitHubAppInstallations, false, true, http.StatusOK},
			{"link", (*server).linkExistingGitHubAppInstallation, false, true, http.StatusOK},
		}
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			role, _, err := repo.WorkspaceRole(t.Context(), f.data.Workspace.ID, user.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, operation := range operations {
				t.Run(operation.name, func(t *testing.T) {
					var outcomes []any
					for _, source := range []*store.SQLiteStore{repo, f.probe} {
						reset(operation.linked)
						service := &server{store: source, coding: base.coding}
						r, w := f.request(user, key, map[string]string{"installationId": "42"}), httptest.NewRecorder()
						r.SetPathValue("id", "42")
						scaleP21M3Call(service, w, r, operation.handler)
						want := operation.want
						if operation.admin && !workspaceAdminRole(role) {
							want = http.StatusForbidden
						}
						if w.Code != want {
							t.Fatalf("GitHub %s status=%d want=%d body=%s", operation.name, w.Code, want, w.Body.String())
						}
						rows, err := repo.CodeHostInstallations(t.Context(), workspace, domain.CodeHostGitHub)
						if err != nil {
							t.Fatal(err)
						}
						for i := range rows {
							rows[i].CreatedAt, rows[i].UpdatedAt = time.Time{}, time.Time{}
						}
						outcomes = append(outcomes, []any{w.Code, scaleP21SW24GitHubResponse(t, service, w, operation.name), rows})
					}
					if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
						t.Fatalf("GitHub identity differential: %s", scaleP21M2Diff(outcomes[0], outcomes[1]))
					}
				})
			}
		})
		member := f.principals[1].user
		queued := f.request(member, nil, map[string]string{"installationId": "42"})
		queued.SetPathValue("id", "42")
		if err := repo.SuspendMember(t.Context(), f.data.Workspace.ID, member.ID); err != nil {
			t.Fatal(err)
		}
		for _, phase := range []string{"queued", "replayed"} {
			for _, operation := range operations {
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					beforeCalls := calls.Load()
					w := httptest.NewRecorder()
					scaleP21M3Call(&server{store: source, coding: base.coding}, w, queued, operation.handler)
					if w.Code != http.StatusForbidden || beforeCalls != calls.Load() {
						t.Fatalf("%s GitHub call reused revoked authority", phase)
					}
				}
			}
		}
		foreign, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "SW24 foreign", idgen.New("sw24-foreign-"), "us")
		if err != nil {
			t.Fatal(err)
		}
		f.resources.workspaces = append(f.resources.workspaces, foreign.Workspace.URLKey)
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("GitHub identity read decoded all metadata")
		}
		f.probe, err = repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		reset(true)
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			service := &server{store: source, coding: base.coding}
			r, w := f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
			r.Header.Set("X-Workspace-Key", foreign.Workspace.URLKey)
			r.SetPathValue("workspaceKey", foreign.Workspace.URLKey)
			r.SetPathValue("id", "42")
			scaleP21M3Call(service, w, r, (*server).removeGitHubAppInstallation)
			if w.Code != http.StatusNotFound {
				t.Fatal("foreign workspace removed another installation")
			}
			rows, err := repo.CodeHostInstallations(t.Context(), workspace, domain.CodeHostGitHub)
			if err != nil || len(rows) != 1 {
				t.Fatal("owned workspace installation changed", err)
			}
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		for _, handler := range []func(*server, http.ResponseWriter, *http.Request){(*server).getGitHubApp, (*server).startGitHubAppAuthorization} {
			r, w := f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
			beforeCalls := calls.Load()
			handler(&server{store: f.probe, coding: base.coding}, w, r.WithContext(ctx))
			if w.Body.Len() > 0 && w.Code < http.StatusBadRequest || strings.Contains(w.Body.String(), `"url"`) || calls.Load() != beforeCalls {
				t.Fatal("cancelled identity read released GitHub state")
			}
		}
		r, w := f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
		(&server{store: f.probe, coding: base.coding}).getGitHubApp(w, r)
		if w.Code != http.StatusOK {
			t.Fatal("GitHub read after cancellation did not recover")
		}
		if before != scaleP21M3Fingerprint(t, repo, workspace, "loops") || f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("GitHub identity read changed unrelated metadata or decoded all metadata")
		}
	}
}
