package main

import (
	"net/http"
	"slices"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP17CanonicalAndSelectedWritersInterleave(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	peers := newScaleP03PeerServers(t)
	f := scaleP03CreateFixture(t, peers.stores[0], peers.servers[0], peers.first)
	workspace := f.data.Workspace.URLKey
	member, user := scaleP03Join(t, f, "P17 peer member", idgen.New("p17-peer-")+"@example.test", "member", f.data.Teams[0].ID)
	peers.stopListening(0)
	peers.stopListening(1)
	t.Setenv("FLOW_DOCUMENT_RECORDS", "off")
	t.Setenv("FLOW_TRASH_RECORDS", "off")
	project := authRequest[domain.Project](t, f.admin, "POST", peers.first.URL+"/api/projects", map[string]any{"name": "P17 peer project", "teamIds": []string{f.data.Teams[0].ID}}, workspace, http.StatusCreated)
	document := authRequest[domain.Document](t, f.admin, "POST", peers.first.URL+"/api/documents", map[string]any{"title": "P17 canonical writer", "content": "original", "projectIds": []string{project.ID}}, workspace, http.StatusCreated)
	authRequest[any](t, f.admin, "PUT", peers.first.URL+"/api/favorites/document/"+document.ID, nil, workspace, http.StatusOK)
	authRequest[any](t, f.admin, "PUT", peers.first.URL+"/api/subscriptions/document/"+document.ID, nil, workspace, http.StatusOK)
	t.Setenv("FLOW_DOCUMENT_RECORDS", "on")
	t.Setenv("FLOW_TRASH_RECORDS", "on")
	authRequest[domain.Document](t, f.admin, "PATCH", peers.second.URL+"/api/documents/"+document.ID, map[string]any{"content": "selected writer"}, workspace, http.StatusOK)
	t.Setenv("FLOW_DOCUMENT_RECORDS", "off")
	other := authRequest[domain.Document](t, f.admin, "POST", peers.first.URL+"/api/documents", map[string]any{"title": "P17 independent canonical document", "content": "untouched"}, workspace, http.StatusCreated)
	t.Setenv("FLOW_DOCUMENT_RECORDS", "on")
	saved := authRequest[domain.Document](t, f.admin, "PATCH", peers.second.URL+"/api/documents/"+document.ID, map[string]any{"title": "P17 selected rename"}, workspace, http.StatusOK)
	if saved.Content != "selected writer" || len(saved.Revisions) != 2 {
		t.Fatal("selected writer lost a canonical edit or history")
	}
	authRequest[any](t, f.admin, "DELETE", peers.second.URL+"/api/documents/"+document.ID, nil, workspace, http.StatusNoContent)
	deleted := authRequest[domain.Document](t, f.admin, "GET", peers.first.URL+"/api/documents/"+document.SlugID, nil, workspace, http.StatusOK)
	if deleted.DeletedAt == nil {
		t.Fatal("canonical replica lost the deleted state on an old link")
	}
	data := authRequest[domain.Bootstrap](t, f.admin, "GET", peers.second.URL+"/api/bootstrap", nil, workspace, http.StatusOK)
	index := slices.IndexFunc(data.Trash, func(entry domain.TrashEntry) bool { return entry.ResourceID == document.ID })
	if index < 0 {
		t.Fatal("selected deletion has no canonical trash row")
	}
	t.Setenv("FLOW_TRASH_RECORDS", "off")
	authRequest[domain.Document](t, f.admin, "POST", peers.first.URL+"/api/trash/"+data.Trash[index].ID+"/restore", nil, workspace, http.StatusOK)
	data = authRequest[domain.Bootstrap](t, f.admin, "GET", peers.second.URL+"/api/bootstrap", nil, workspace, http.StatusOK)
	if !slices.ContainsFunc(data.Documents, func(item domain.Document) bool { return item.ID == other.ID && item.Content == "untouched" }) || !slices.ContainsFunc(data.Favorites, func(item domain.Favorite) bool { return item.ResourceID == document.ID }) || !slices.ContainsFunc(data.Subscriptions, func(item domain.Subscription) bool { return item.ResourceID == document.ID }) {
		t.Fatal("interleaved restore lost an unrelated document or preferences")
	}
	authRequest[domain.Document](t, member, "GET", peers.second.URL+"/api/documents/"+document.ID, nil, workspace, http.StatusOK)
	t.Setenv("FLOW_DOCUMENT_RECORDS", "off")
	authRequest[[]domain.DocumentPermission](t, f.admin, "PUT", peers.first.URL+"/api/documents/"+document.ID+"/permissions", map[string]any{"permissions": []map[string]string{{"subjectType": "user", "subjectId": user.ID, "role": "editor"}}}, workspace, http.StatusOK)
	authRequest[[]domain.DocumentPermission](t, f.admin, "PUT", peers.first.URL+"/api/documents/"+document.ID+"/permissions", map[string]any{"permissions": []any{}}, workspace, http.StatusOK)
	t.Setenv("FLOW_DOCUMENT_RECORDS", "on")
	authRequest[any](t, member, "GET", peers.second.URL+"/api/documents/"+document.ID, nil, workspace, http.StatusNotFound)
}
