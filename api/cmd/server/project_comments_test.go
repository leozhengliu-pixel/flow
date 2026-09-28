package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// TestProjectCommentThreadLifecycle covers the Linear-style project comment
// card actions: replies, author-only edits, reaction toggles, and cascading
// deletes, plus the domain events and bootstrap projection they produce.
func TestProjectCommentThreadLifecycle(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	project := requestJSON[domain.Project](t, handler, http.MethodPost, "/api/projects", map[string]any{"name": "Comment thread", "teamIds": []string{"team_test"}}, http.StatusCreated)
	base := "/api/projects/" + project.ID + "/comments"

	root := requestJSON[domain.Comment](t, handler, http.MethodPost, base, map[string]any{"body": "Root comment"}, http.StatusCreated)
	if root.Version != 1 || root.ParentID != nil || root.User.ID != "usr_admin" {
		t.Fatalf("root comment = %#v", root)
	}
	reply := requestJSON[domain.Comment](t, handler, http.MethodPost, base, map[string]any{"body": "Reply", "parentId": root.ID}, http.StatusCreated)
	if reply.ParentID == nil || *reply.ParentID != root.ID {
		t.Fatalf("reply parent = %#v", reply.ParentID)
	}
	// Replying to a reply stays in the root thread (single-level threads).
	nested := requestJSON[domain.Comment](t, handler, http.MethodPost, base, map[string]any{"body": "Nested", "parentId": reply.ID}, http.StatusCreated)
	if nested.ParentID == nil || *nested.ParentID != root.ID {
		t.Fatalf("nested reply parent = %#v", nested.ParentID)
	}
	requestJSON[any](t, handler, http.MethodPost, base, map[string]any{"body": "Orphan", "parentId": "missing"}, http.StatusNotFound)

	edited := requestJSON[domain.Comment](t, handler, http.MethodPatch, base+"/"+root.ID, map[string]any{"body": "Edited root", "bodyData": map[string]any{"type": "doc"}}, http.StatusOK)
	if edited.Body != "Edited root" || edited.EditedAt == nil || edited.Version != 2 || edited.BodyData["type"] != "doc" {
		t.Fatalf("edited comment = %#v", edited)
	}
	stale := int64(1)
	requestJSON[any](t, handler, http.MethodPatch, base+"/"+root.ID, map[string]any{"body": "Stale", "expectedVersion": stale}, http.StatusConflict)
	requestJSON[any](t, handler, http.MethodPatch, base+"/"+root.ID, map[string]any{"body": "  "}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodPatch, base+"/missing", map[string]any{"body": "x"}, http.StatusNotFound)

	reacted := requestJSON[domain.Comment](t, handler, http.MethodPost, base+"/"+root.ID+"/reactions", map[string]any{"emoji": "👍"}, http.StatusOK)
	if !slices.Equal(reacted.Reactions["👍"], []string{"usr_admin"}) {
		t.Fatalf("reaction add = %#v", reacted.Reactions)
	}
	reacted = requestJSON[domain.Comment](t, handler, http.MethodPost, base+"/"+root.ID+"/reactions", map[string]any{"emoji": "👍"}, http.StatusOK)
	if _, ok := reacted.Reactions["👍"]; ok {
		t.Fatalf("reaction toggle off = %#v", reacted.Reactions)
	}
	requestJSON[any](t, handler, http.MethodPost, base+"/"+root.ID+"/reactions", map[string]any{"emoji": ""}, http.StatusBadRequest)

	// Another member's comment cannot be edited by the viewer.
	foreign := "project_comment_foreign"
	if err := repository.MutateWorkspace(t.Context(), "", "project.commented", project.ID, nil, func(data *domain.Bootstrap) error {
		for index := range data.Projects {
			if data.Projects[index].ID == project.ID {
				data.Projects[index].Comments = append(data.Projects[index].Comments, domain.Comment{ID: foreign, Version: 1, Body: "Theirs", Reactions: map[string][]string{}, User: domain.User{ID: "usr_other", Name: "Other"}})
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	requestJSON[any](t, handler, http.MethodPatch, base+"/"+foreign, map[string]any{"body": "Hijacked"}, http.StatusForbidden)

	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	index := slices.IndexFunc(bootstrap.Projects, func(item domain.Project) bool { return item.ID == project.ID })
	if index < 0 || len(bootstrap.Projects[index].Comments) != 4 {
		t.Fatalf("bootstrap project comments = %#v", bootstrap.Projects)
	}

	requestJSON[any](t, handler, http.MethodDelete, base+"/"+root.ID, nil, http.StatusNoContent)
	requestJSON[any](t, handler, http.MethodDelete, base+"/"+root.ID, nil, http.StatusNotFound)
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	index = slices.IndexFunc(bootstrap.Projects, func(item domain.Project) bool { return item.ID == project.ID })
	if comments := bootstrap.Projects[index].Comments; len(comments) != 1 || comments[0].ID != foreign {
		t.Fatalf("delete should cascade to replies, got %#v", comments)
	}

	events := requestJSON[[]domain.DomainEvent](t, handler, http.MethodGet, "/api/events?aggregateId="+project.ID, nil, http.StatusOK)
	var types []string
	for _, event := range events {
		types = append(types, event.Type)
	}
	for _, want := range []string{"project.comment_updated", "project.comment_reaction_toggled", "project.comment_deleted"} {
		if !slices.Contains(types, want) {
			t.Fatalf("missing %s in events %v", want, types)
		}
	}
}

// TestProjectCommentDeletePermissions verifies that members may delete only
// their own project comments while workspace admins may moderate any.
func TestProjectCommentDeletePermissions(t *testing.T) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "project-comment-acl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	server := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir()}))
	defer server.Close()

	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, http.MethodPost, server.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	member, _ := verifiedAuthClient(t, server.URL, "Project member", "project-member@example.com")
	invitation := authRequest[[]domain.Invitation](t, admin, http.MethodPost, server.URL+"/api/workspaces/test-workspace/invitations", map[string]any{"emails": []string{"project-member@example.com"}, "role": "member"}, "", http.StatusCreated)
	authRequest[domain.WorkspaceMembership](t, member, http.MethodPost, server.URL+"/api/invitations/accept", map[string]string{"token": invitation[0].Token}, "", http.StatusOK)

	project := authRequest[domain.Project](t, admin, http.MethodPost, server.URL+"/api/projects", map[string]any{"name": "ACL comments", "teamIds": []string{"team_test"}}, "test-workspace", http.StatusCreated)
	base := server.URL + "/api/projects/" + project.ID + "/comments"
	adminComment := authRequest[domain.Comment](t, admin, http.MethodPost, base, map[string]string{"body": "Admin comment"}, "test-workspace", http.StatusCreated)
	memberComment := authRequest[domain.Comment](t, member, http.MethodPost, base, map[string]string{"body": "Member comment"}, "test-workspace", http.StatusCreated)

	authRequest[any](t, member, http.MethodPatch, base+"/"+adminComment.ID, map[string]string{"body": "Not mine"}, "test-workspace", http.StatusForbidden)
	authRequest[any](t, member, http.MethodDelete, base+"/"+adminComment.ID, nil, "test-workspace", http.StatusForbidden)
	authRequest[any](t, admin, http.MethodPatch, base+"/"+memberComment.ID, map[string]string{"body": "Admins cannot rewrite"}, "test-workspace", http.StatusForbidden)
	authRequest[domain.Comment](t, member, http.MethodPost, base+"/"+adminComment.ID+"/reactions", map[string]string{"emoji": "🎉"}, "test-workspace", http.StatusOK)
	authRequest[domain.Comment](t, member, http.MethodPatch, base+"/"+memberComment.ID, map[string]string{"body": "Member edit"}, "test-workspace", http.StatusOK)
	authRequest[any](t, admin, http.MethodDelete, base+"/"+memberComment.ID, nil, "test-workspace", http.StatusNoContent)
	authRequest[any](t, admin, http.MethodDelete, base+"/"+adminComment.ID, nil, "test-workspace", http.StatusNoContent)
}
