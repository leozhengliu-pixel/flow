package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// A single document (and its subscribers) is read from workspace metadata:
// the read never decodes issue, comment or activity records, and answers the
// same document the full workspace read did.
func TestDocumentReadsAvoidIssueHydration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "document-reads.db")
	repo, err := store.OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	initial := repo.Bootstrap()
	key, team := initial.Workspace.URLKey, initial.Teams[0].ID
	host := httptest.NewServer(newHandler(&server{store: repo, uploadPath: t.TempDir()}))
	defer host.Close()
	admin, member := authClient(t), authClient(t)
	for email, client := range map[string]*http.Client{"admin@example.test": admin, "member@example.test": member} {
		authRequest[domain.AuthSession](t, client, "POST", host.URL+"/api/auth/login", map[string]string{"email": email, "password": "test-password"}, "", http.StatusOK)
	}
	authRequest[domain.WorkspaceMember](t, admin, "PATCH", host.URL+"/api/workspaces/"+key+"/members/usr_member", map[string]string{"role": "member"}, "", http.StatusOK)
	created := authRequest[domain.Document](t, admin, "POST", host.URL+"/api/documents", map[string]any{"title": "Metadata read", "content": "Body", "teamIds": []string{team}}, key, http.StatusCreated)
	deleted := authRequest[domain.Document](t, admin, "POST", host.URL+"/api/documents", map[string]any{"title": "Deleted read", "content": "Gone", "teamIds": []string{team}}, key, http.StatusCreated)
	authRequest[any](t, admin, "DELETE", host.URL+"/api/documents/"+deleted.ID, nil, key, http.StatusNoContent)

	// What the full workspace read answered for each viewer.
	want := map[string]string{}
	for _, viewer := range []string{"usr_admin", "usr_member"} {
		data, ok, err := repo.BootstrapForUser(t.Context(), key, viewer)
		if err != nil || !ok {
			t.Fatalf("full read for %s: %v", viewer, err)
		}
		for _, document := range data.Documents {
			if document.ID == created.ID {
				raw, _ := json.Marshal(document)
				want[viewer] = string(raw)
			}
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{`UPDATE issue_records SET data=?`, `UPDATE workspace_content_records SET data=? WHERE kind IN ('comment','activity')`} {
		if _, err := db.Exec(statement, []byte("invalid-unrelated-payload")); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := repo.BootstrapForContext(t.Context(), key); ok {
		t.Fatal("poisoned fixture did not block full hydration")
	}

	for viewer, client := range map[string]*http.Client{"usr_admin": admin, "usr_member": member} {
		for _, id := range []string{created.ID, created.SlugID} {
			got := authRequest[json.RawMessage](t, client, "GET", host.URL+"/api/documents/"+id, nil, key, http.StatusOK)
			if string(got) != want[viewer] {
				t.Fatalf("%s reading %s\n got %.300s\nwant %.300s", viewer, id, got, want[viewer])
			}
		}
		subscribers := authRequest[[]domain.User](t, client, "GET", host.URL+"/api/documents/"+created.ID+"/subscribers", nil, key, http.StatusOK)
		if len(subscribers) != 1 || subscribers[0].ID != "usr_admin" {
			t.Fatalf("%s subscribers = %+v", viewer, subscribers)
		}
		// A deleted document is still read from the trash, which shows a
		// member only what they deleted themselves.
		if viewer == "usr_member" {
			authRequest[any](t, client, "GET", host.URL+"/api/documents/"+deleted.ID, nil, key, http.StatusNotFound)
		} else if trashed := authRequest[domain.Document](t, client, "GET", host.URL+"/api/documents/"+deleted.ID, nil, key, http.StatusOK); trashed.DeletedAt == nil {
			t.Fatalf("%s did not read the deleted document from the trash", viewer)
		}
		authRequest[any](t, client, "GET", host.URL+"/api/documents/document_missing", nil, key, http.StatusNotFound)
	}
	authRequest[any](t, authClient(t), "GET", host.URL+"/api/documents/"+created.ID, nil, key, http.StatusUnauthorized)
}
