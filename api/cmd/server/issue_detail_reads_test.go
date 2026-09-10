package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestIssueDetailHTTPBodyDoesNotDependOnHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "detail.db")
	repo, err := store.OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	handler := newHandler(&server{store: repo, uploadPath: t.TempDir(), authDisabled: true})
	body := strings.TrimSpace(strings.Repeat("Complete issue body, not a list preview.\n", 1000))
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issue-records", map[string]any{"title": "History failure isolation", "description": body}, http.StatusCreated)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, kind := range []string{"comment", "activity"} {
		for _, id := range []string{issue.ID, "unrelated-history"} {
			if _, err := db.ExecContext(t.Context(), `INSERT INTO workspace_content_records(workspace_key,kind,resource_id,id,created_at,data) VALUES('test-workspace',?,?,?,'2026-09-10T00:00:00Z','broken history JSON')`, kind, id, kind+"-"+id); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, id := range []string{issue.ID, strings.ToLower(issue.Identifier)} {
		got := requestJSON[domain.Issue](t, handler, http.MethodGet, "/api/issue-records/"+id, nil, http.StatusOK)
		if got.Description != body || got.ID != issue.ID {
			t.Fatalf("detail returned a partial body: id=%s bytes=%d", got.ID, len(got.Description))
		}
		requestJSON[any](t, handler, http.MethodGet, "/api/issue-records/"+id+"/history", nil, http.StatusInternalServerError)
	}
	if _, err := db.ExecContext(t.Context(), `DELETE FROM workspace_content_records WHERE workspace_key='test-workspace' AND resource_id=?`, issue.ID); err != nil {
		t.Fatal(err)
	}
	requestJSON[any](t, handler, http.MethodGet, "/api/issue-records/"+issue.ID+"/history", nil, http.StatusOK)
}

type issueReadKeyTransport struct{ secret string }

func (transport issueReadKeyTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	copy := request.Clone(request.Context())
	copy.Header = request.Header.Clone()
	copy.Header.Set("Authorization", "Bearer "+transport.secret)
	return http.DefaultTransport.RoundTrip(copy)
}

func TestIssueDetailHTTPHistoryAndVisibilityHonorSharesAndAPIKeyScopes(t *testing.T) {
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "detail-auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	host := httptest.NewServer(newHandler(&server{store: repo, uploadPath: t.TempDir()}))
	defer host.Close()
	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, http.MethodPost, host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	reader, user := verifiedAuthClient(t, host.URL, "Detail viewer", "detail-viewer@example.test")
	invites := authRequest[[]domain.Invitation](t, admin, http.MethodPost, host.URL+"/api/workspaces/test-workspace/invitations", map[string]any{"emails": []string{user.Email}, "role": "member"}, "", http.StatusCreated)
	authRequest[domain.WorkspaceMembership](t, reader, http.MethodPost, host.URL+"/api/invitations/accept", map[string]string{"token": invites[0].Token}, "", http.StatusOK)
	public := repo.Bootstrap().Issues[0]
	team := authRequest[domain.Team](t, admin, http.MethodPost, host.URL+"/api/workspaces/test-workspace/teams", map[string]any{"name": "Detail private", "key": "DPR", "private": true}, "", http.StatusCreated)
	private := authRequest[domain.Issue](t, admin, http.MethodPost, host.URL+"/api/issue-records", map[string]any{"title": "Private detail", "teamId": team.ID}, "test-workspace", http.StatusCreated)
	shared := authRequest[domain.Issue](t, admin, http.MethodPost, host.URL+"/api/issue-records", map[string]any{"title": "Shared read-only detail", "teamId": team.ID}, "test-workspace", http.StatusCreated)
	authRequest[any](t, reader, http.MethodGet, host.URL+"/api/issue-records/"+private.ID+"/history", nil, "test-workspace", http.StatusNotFound)
	authRequest[[]domain.IssuePermission](t, admin, http.MethodPut, host.URL+"/api/issues/"+shared.ID+"/permissions", map[string]any{"permissions": []map[string]string{{"subjectType": "user", "subjectId": user.ID, "role": "viewer"}}}, "test-workspace", http.StatusOK)
	authRequest[any](t, reader, http.MethodGet, host.URL+"/api/issue-records/"+shared.Identifier+"/history", nil, "test-workspace", http.StatusOK)
	authRequest[any](t, reader, http.MethodPatch, host.URL+"/api/issue-records/"+shared.ID, map[string]string{"title": "Forbidden edit"}, "test-workspace", http.StatusForbidden)
	type visibility struct {
		IDs []string `json:"ids"`
	}
	input := map[string]any{"ids": []string{public.ID, private.ID, shared.ID, "missing"}}
	got := authRequest[visibility](t, reader, http.MethodPost, host.URL+"/api/issue-records/visibility", input, "test-workspace", http.StatusOK)
	if !slices.Equal(got.IDs, []string{public.ID, shared.ID}) {
		t.Fatalf("visibility leaked hidden IDs: %v", got.IDs)
	}
	ids := make([]string, 2000)
	for i := range ids {
		ids[i] = public.ID
	}
	authRequest[visibility](t, reader, http.MethodPost, host.URL+"/api/issue-records/visibility", map[string]any{"ids": ids}, "test-workspace", http.StatusOK)
	authRequest[any](t, reader, http.MethodPost, host.URL+"/api/issue-records/visibility", map[string]any{"ids": append(ids, public.ID)}, "test-workspace", http.StatusBadRequest)
	key := authRequest[struct {
		Secret string `json:"secret"`
	}](t, admin, http.MethodPost, host.URL+"/api/api-keys", map[string]any{"name": "Read-only detail test", "scopes": []string{"read"}, "teamIds": []string{public.Team.ID}}, "test-workspace", http.StatusCreated)
	keyClient := &http.Client{Transport: issueReadKeyTransport{secret: key.Secret}}
	authRequest[domain.Issue](t, keyClient, http.MethodGet, host.URL+"/api/issue-records/"+public.Identifier, nil, "test-workspace", http.StatusOK)
	authRequest[any](t, keyClient, http.MethodGet, host.URL+"/api/issue-records/"+public.ID+"/history", nil, "test-workspace", http.StatusOK)
	authRequest[any](t, keyClient, http.MethodGet, host.URL+"/api/issue-records/"+shared.ID+"/history", nil, "test-workspace", http.StatusNotFound)
	got = authRequest[visibility](t, keyClient, http.MethodPost, host.URL+"/api/issue-records/visibility", input, "test-workspace", http.StatusOK)
	if !slices.Equal(got.IDs, []string{public.ID}) {
		t.Fatalf("key visibility bypassed team restriction: %v", got.IDs)
	}
	authRequest[any](t, keyClient, http.MethodPost, host.URL+"/api/issue-records/"+public.ID+"/comments", map[string]string{"body": "Must not write"}, "test-workspace", http.StatusUnauthorized)
}
