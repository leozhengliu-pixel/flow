package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestReviewReactionPersistsWithoutCreatingComment(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	data := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	review := data.Reviews[0]
	path := "/api/reviews/" + review.ID
	updated := requestJSON[domain.CodeReview](t, handler, http.MethodPost, path+"/reactions", map[string]string{"emoji": "+1"}, http.StatusOK)
	if len(updated.Reactions["+1"]) != 1 || updated.Reactions["+1"][0] != data.Viewer.ID || len(updated.Events) != len(review.Events) {
		t.Fatalf("reaction must belong to the viewer without creating a comment: %#v", updated)
	}
	reloaded := requestJSON[domain.CodeReview](t, handler, http.MethodGet, path, nil, http.StatusOK)
	if len(reloaded.Reactions["+1"]) != 1 {
		t.Fatal("reaction did not survive reload")
	}
	updated = requestJSON[domain.CodeReview](t, handler, http.MethodPost, path+"/reactions", map[string]string{"emoji": "+1"}, http.StatusOK)
	if len(updated.Reactions) != 0 {
		t.Fatal("second click must remove the reaction")
	}
	requestJSON[any](t, handler, http.MethodPost, path+"/reactions", map[string]string{"emoji": " "}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodPost, "/api/reviews/missing/reactions", map[string]string{"emoji": "+1"}, http.StatusNotFound)
}

func TestReviewAttachmentIsRecordedAndVisibleOnlyToMembers(t *testing.T) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	api := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir()}))
	defer api.Close()
	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, http.MethodPost, api.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	data := authRequest[domain.Bootstrap](t, admin, http.MethodGet, api.URL+"/api/bootstrap", nil, "test-workspace", http.StatusOK)
	path := api.URL + "/api/reviews/" + data.Reviews[0].ID
	status, raw := postProjectCommentMedia(t, admin, path+"/comment-attachments", "notes.txt", "text/plain", "Review notes")
	if status != http.StatusCreated {
		t.Fatalf("upload: %d %s", status, raw)
	}
	var attachment domain.Attachment
	if err := json.Unmarshal(raw, &attachment); err != nil {
		t.Fatal(err)
	}
	reloaded := authRequest[domain.CodeReview](t, admin, http.MethodGet, path, nil, "test-workspace", http.StatusOK)
	if len(reloaded.CommentAttachments) != 1 || reloaded.CommentAttachments[0].URL != attachment.URL {
		t.Fatal("upload was not recorded on the review")
	}
	authStatus(t, admin, http.MethodGet, api.URL+attachment.URL, http.StatusOK)
	outsider, _ := verifiedAuthClient(t, api.URL, "Outsider", "review-outsider@example.com")
	authStatus(t, outsider, http.MethodGet, api.URL+attachment.URL, http.StatusNotFound)
}
