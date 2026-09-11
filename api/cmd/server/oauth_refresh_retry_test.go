package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestOAuthRefreshHTTPFailedAttemptCanRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oauth-retry.db")
	repo, err := store.OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	grant := domain.OAuthRefreshGrant{ClientID: "retry-client", WorkspaceKey: data.Workspace.URLKey, UserID: data.Viewer.ID, Scopes: []string{"read"}, AuthorizationID: "retry-grant", ExpiresAt: time.Now().UTC().Add(time.Hour)}
	if err := repo.MutateWorkspace(t.Context(), grant.WorkspaceKey, "oauth_authorization.created", grant.AuthorizationID, nil, func(next *domain.Bootstrap) error {
		next.OAuthAuthorizations = append(next.OAuthAuthorizations, domain.OAuthAuthorization{ID: grant.AuthorizationID, ClientID: grant.ClientID, UserID: grant.UserID, Scopes: grant.Scopes})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateOAuthRefreshToken(t.Context(), "retry-refresh", grant); err != nil {
		t.Fatal(err)
	}
	host := httptest.NewServer(newHandler(&server{store: repo, uploadPath: t.TempDir()}))
	defer host.Close()
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {"retry-refresh"}, "client_id": {"wrong-client"}}
	denied := postOAuthForm[map[string]any](t, host.URL+"/oauth/token", form, http.StatusBadRequest)
	if denied["error"] != "invalid_grant" {
		t.Fatalf("bad client response: %v", denied)
	}
	form.Set("client_id", grant.ClientID)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(t.Context(), `CREATE TRIGGER fail_refresh BEFORE INSERT ON oauth_refresh_tokens BEGIN SELECT RAISE(ABORT,'simulated disk failure'); END`); err != nil {
		t.Fatal(err)
	}
	failed := postOAuthForm[map[string]any](t, host.URL+"/oauth/token", form, http.StatusInternalServerError)
	if failed["error"] != "server_error" {
		t.Fatalf("temporary failure looked like revoked grant: %v", failed)
	}
	if _, err := db.ExecContext(t.Context(), `DROP TRIGGER fail_refresh`); err != nil {
		t.Fatal(err)
	}
	issued := postOAuthForm[map[string]any](t, host.URL+"/oauth/token", form, http.StatusOK)
	if issued["refresh_token"] == "" || issued["access_token"] == "" {
		t.Fatal("successful retry omitted tokens")
	}
	postOAuthForm[map[string]any](t, host.URL+"/oauth/token", form, http.StatusBadRequest)
}
