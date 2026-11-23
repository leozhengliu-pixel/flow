package main

import (
	"context"
	"database/sql/driver"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestDatabaseOutageMutationRoutes(t *testing.T) {
	repo := openOutageStore(t, store.DatabaseConfig{Driver: "sqlite", Path: filepath.Join(t.TempDir(), "mutations.db")}, true)
	s := &server{store: repo, coordinationContext: t.Context()}
	handler := newHandler(s)
	metadata, _ := repo.WorkspaceSettingsMetadata("")
	workspace := metadata.Workspace.URLKey
	user, token := outageSession(t, repo)
	cookie := outageCookie(workspace, token)
	teams := outageResponse[connection[domain.Team]](t, handler, outageRequest{"GET", "/api/teams", "", cookie}, http.StatusOK).Nodes
	if len(teams) == 0 {
		t.Fatal("fixture has no teams")
	}
	team := teams[0].ID
	issue := outageResponse[domain.Issue](t, handler, outageRequest{"POST", "/api/issue-records",
		fmt.Sprintf(`{"title":"Outage issue","teamId":%q}`, team), cookie}, http.StatusCreated)
	project := outageResponse[domain.Project](t, handler, outageRequest{"POST", "/api/projects",
		fmt.Sprintf(`{"name":"Outage project","teamIds":[%q]}`, team), cookie}, http.StatusCreated)
	view := outageResponse[domain.SavedView](t, handler, outageRequest{"POST", "/api/views",
		`{"name":"Outage view","resource":"issues","scope":"workspace"}`, cookie}, http.StatusCreated)
	document := outageResponse[domain.Document](t, handler, outageRequest{"POST", "/api/documents",
		`{"title":"Outage document"}`, cookie}, http.StatusCreated)
	keyID, _ := createOutageAPIKey(t, handler, cookie, "Outage revoke key")
	if _, err := repo.AuthenticateSession(t.Context(), token); err != nil {
		t.Fatal("fixture did not authenticate before outage")
	}
	if err := repo.CloseDatabasePoolForTesting(); err != nil {
		t.Fatal("could not close test database")
	}
	for _, tc := range []struct {
		method, pattern, path, body string
		mutation                    http.HandlerFunc
	}{
		{"POST", "/api/issue-records/{id}/comments", "/api/issue-records/" + issue.ID + "/comments", `{"body":"Outage comment"}`, func(w http.ResponseWriter, r *http.Request) {
			// The route alias binds the issue discussion before calling the
			// writer. A failed authorization read would hide the write error.
			ctx := store.WithIssueDiscussionMutation(r.Context(), r.PathValue("id"), "")
			s.createComment(w, r.WithContext(ctx))
		}},
		{"POST", "/api/documents", "/api/documents", `{"title":"New document"}`, s.createPortfolioDocument},
		{"PATCH", "/api/projects/{id}", "/api/projects/" + project.ID, `{"summary":"Updated project"}`, s.updatePortfolioProject},
		{"POST", "/api/api-keys", "/api/api-keys", `{"name":"New key","scopes":["read"]}`, s.createAPIKey},
		{"DELETE", "/api/api-keys/{id}", "/api/api-keys/" + keyID, "", s.revokeAPIKey},
		{"DELETE", "/api/views/{id}", "/api/views/" + view.ID, "", s.deleteSavedView},
		{"POST", "/api/views", "/api/views", `{"name":"New view","resource":"issues","scope":"workspace"}`, s.createSavedView},
		{"POST", "/api/labels", "/api/labels", `{"name":"New label"}`, s.createWorkspaceLabel},
		{"DELETE", "/api/documents/{id}", "/api/documents/" + document.ID, "", s.deletePortfolioDocument},
	} {
		t.Run(tc.method+" "+tc.pattern, func(t *testing.T) {
			// Begin at the normal route handler after the authenticated
			// workspace and actor are established.
			ctx := context.WithValue(t.Context(), authUserContextKey{}, user)
			ctx = context.WithValue(ctx, workspaceKeyContextKey{}, workspace)
			ctx, release := beginMetadataOutputReads(store.ContextWithActor(ctx, user))
			defer release()
			mux := http.NewServeMux()
			mux.HandleFunc(tc.method+" "+tc.pattern, tc.mutation)
			assertDatabaseOutage(t, (outageRequest{tc.method, tc.path, tc.body, nil}).serve(ctx, apiErrorEnvelope(mux)))
		})
	}
}

func TestDatabaseOutageSharedErrorWriters(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/documents", nil)
	writers := map[string]func(http.ResponseWriter, error){
		"mutation": func(w http.ResponseWriter, err error) { respondMutation(w, err, http.StatusCreated, nil) },
		"internal": func(w http.ResponseWriter, err error) { writeInternalError(w, req, err) },
		"api":      func(w http.ResponseWriter, err error) { writeErrorFor(w, req, http.StatusBadRequest, err) },
	}
	for name, write := range writers {
		t.Run(name, func(t *testing.T) {
			for _, err := range []error{driver.ErrBadConn, mysql.ErrInvalidConn, io.EOF, syscall.ECONNREFUSED, pgconn.ErrConnClosed} {
				w := httptest.NewRecorder()
				write(w, fmt.Errorf("store operation: %w", err))
				assertDatabaseOutage(t, w)
			}
			w := httptest.NewRecorder()
			write(w, &mysql.MySQLError{Number: 1062})
			if w.Code != http.StatusInternalServerError || w.Header().Get("Retry-After") != "" {
				t.Fatalf("non-connection SQL error returned %d, want 500 without Retry-After", w.Code)
			}
		})
	}
}

func TestDatabaseOutageLogOmitsRequestValues(t *testing.T) {
	logs := captureServerLogs(t)
	const marker = "private-fixture-value"
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/projects/{id}", func(w http.ResponseWriter, r *http.Request) {
		err := fmt.Errorf("update project %s: %w", r.PathValue("id"), mysql.ErrInvalidConn)
		respondMutation(w, err, http.StatusOK, nil)
	})
	r := httptest.NewRequest(http.MethodPatch, "/api/projects/"+marker, nil)
	r.Header.Set("X-Request-ID", marker)
	w := httptest.NewRecorder()
	requestLog(mux, apiErrorEnvelope(mux)).ServeHTTP(w, r)
	assertDatabaseOutage(t, w)
	if text := logs.String(); strings.Contains(text, marker) || !strings.Contains(text, "cause=mysql_invalid_connection") {
		t.Fatal("outage log did not use a redacted driver code")
	}
}
