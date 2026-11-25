package main

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP20CanonicalWriterAndBoundedReplica(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_CONNECTOR_SECRET_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("p", 32))))
	peers := newScaleP03PeerServers(t)
	f := scaleP03CreateFixture(t, peers.stores[0], peers.servers[0], peers.first)
	workspace := f.data.Workspace.URLKey
	peers.stopListening(0)
	peers.stopListening(1)
	t.Setenv("FLOW_DOCUMENT_RECORDS", "off")
	t.Setenv("FLOW_TRASH_RECORDS", "off")
	doc := authRequest[domain.Document](t, f.admin, "POST", peers.first.URL+"/api/documents", map[string]any{"title": "P20 old writer", "content": "Original body", "teamIds": []string{f.data.Teams[0].ID}}, workspace, http.StatusCreated)
	saved := authRequest[domain.Document](t, f.admin, "PATCH", peers.first.URL+"/api/documents/"+doc.ID, map[string]any{"title": "P20 renamed", "content": "Changed body"}, workspace, http.StatusOK)
	t.Setenv("FLOW_DOCUMENT_RECORDS", "on")
	read := authRequest[domain.Document](t, f.admin, "GET", peers.second.URL+"/api/documents/"+doc.SlugID+"?history=summary", nil, workspace, http.StatusOK)
	if read.Content != saved.Content || read.Title != saved.Title || read.Revisions[0].Content != "" {
		t.Fatal("bounded replica did not see the canonical edit or sent history bodies")
	}
	for _, revision := range saved.Revisions {
		got := authRequest[domain.DocumentRevision](t, f.admin, "GET", peers.second.URL+"/api/documents/"+doc.ID+"/history/"+revision.ID, nil, workspace, http.StatusOK)
		if got.Content != revision.Content {
			t.Fatal("selected version changed across replicas")
		}
	}
	t.Setenv("FLOW_DOCUMENT_RECORDS", "off")
	authRequest[any](t, f.admin, "DELETE", peers.first.URL+"/api/documents/"+doc.ID, nil, workspace, http.StatusNoContent)
	entry := authRequest[domain.TrashEntry](t, f.admin, "GET", peers.second.URL+"/api/resource-windows/trash?resourceId="+doc.ID, nil, workspace, http.StatusOK)
	if entry.ResourceID != doc.ID || entry.Links != nil {
		t.Fatal("targeted trash lost its identity or exposed restore links")
	}
	authRequest[domain.Document](t, f.admin, "POST", peers.first.URL+"/api/trash/"+entry.ID+"/restore", nil, workspace, http.StatusOK)
	read = authRequest[domain.Document](t, f.admin, "GET", peers.second.URL+"/api/documents/"+doc.ID+"?history=summary", nil, workspace, http.StatusOK)
	if read.DeletedAt != nil || read.Content != saved.Content {
		t.Fatal("bounded replica did not see canonical restoration")
	}
}

func TestScaleP20QueuedVersionRevocation(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	peers := newScaleP03PeerServers(t)
	f := scaleP03CreateFixture(t, peers.stores[0], peers.servers[0], peers.first)
	workspace := f.data.Workspace.URLKey
	member, user := scaleP03Join(t, f, "P20 version reader", idgen.New("p20-reader-")+"@example.test", "member", f.data.Teams[0].ID)
	peers.stopListening(0)
	peers.stopListening(1)
	doc := authRequest[domain.Document](t, f.admin, "POST", peers.first.URL+"/api/documents", map[string]any{"title": "P20 protected version", "content": strings.Repeat("protected history ", 1024)}, workspace, http.StatusCreated)
	authRequest[[]domain.DocumentPermission](t, f.admin, "PUT", peers.first.URL+"/api/documents/"+doc.ID+"/permissions", map[string]any{"permissions": []map[string]string{{"subjectType": "user", "subjectId": user.ID, "role": "viewer"}}}, workspace, http.StatusOK)
	saved := authRequest[domain.Document](t, f.admin, "PATCH", peers.first.URL+"/api/documents/"+doc.ID, map[string]any{"content": "Current body"}, workspace, http.StatusOK)
	if len(saved.Revisions) < 2 {
		t.Fatal("version fixture has no offloaded baseline")
	}
	version := saved.Revisions[len(saved.Revisions)-1].ID
	r := httptest.NewRequest(http.MethodGet, "/api/documents/"+doc.ID+"/history/"+version, nil)
	r.Header.Set("X-Workspace-Key", workspace)
	origin, _ := url.Parse(peers.second.URL)
	for _, cookie := range member.Jar.Cookies(origin) {
		r.AddCookie(cookie)
	}
	revoked := false
	r = r.WithContext(store.WithSQLStatementObserver(r.Context(), func(query string) {
		if !revoked && strings.Contains(query, "FROM metadata_blobs") {
			revoked = true
			authRequest[[]domain.DocumentPermission](t, f.admin, "PUT", peers.first.URL+"/api/documents/"+doc.ID+"/permissions", map[string]any{"permissions": []any{}}, workspace, http.StatusOK)
		}
	}))
	w := httptest.NewRecorder()
	peers.second.Config.Handler.ServeHTTP(w, r)
	if !revoked || w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), "protected history") {
		t.Fatalf("queued version escaped revocation: revoked=%v status=%d", revoked, w.Code)
	}
}
