package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestDocumentAttachmentUploadIsServedToDocumentViewersOnly(t *testing.T) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "document-media.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	server := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir()}))
	defer server.Close()
	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, http.MethodPost, server.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	document := authRequest[domain.Document](t, admin, http.MethodPost, server.URL+"/api/documents", map[string]any{"title": "With media"}, "test-workspace", http.StatusCreated)
	endpoint := server.URL + "/api/documents/" + document.ID + "/attachments"

	status, raw := postProjectCommentMedia(t, admin, endpoint, "shot.png", "image/png", "\x89PNG\r\n\x1a\npasted image")
	if status != http.StatusCreated {
		t.Fatalf("upload status %d: %s", status, raw)
	}
	var attachment domain.Attachment
	if err := json.Unmarshal(raw, &attachment); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(attachment.URL, "/uploads/document_attachment_") || attachment.Title != "shot.png" || attachment.Size == 0 || attachment.Creator.ID == "" {
		t.Fatalf("attachment = %#v", attachment)
	}
	authStatus(t, admin, http.MethodGet, server.URL+attachment.URL, http.StatusOK)

	refreshed := authRequest[[]domain.Document](t, admin, http.MethodGet, server.URL+"/api/documents", nil, "test-workspace", http.StatusOK)
	index := slices.IndexFunc(refreshed, func(item domain.Document) bool { return item.ID == document.ID })
	if index < 0 || !slices.ContainsFunc(refreshed[index].Attachments, func(item domain.Attachment) bool { return item.URL == attachment.URL }) {
		t.Fatalf("document attachments = %#v", refreshed)
	}

	outsider, _ := verifiedAuthClient(t, server.URL, "Outsider", "outsider-document-media@example.com")
	authStatus(t, outsider, http.MethodGet, server.URL+attachment.URL, http.StatusNotFound)

	if status, raw := postProjectCommentMedia(t, admin, endpoint, "", "", ""); status != http.StatusBadRequest {
		t.Fatalf("missing file status %d: %s", status, raw)
	}
	if status, raw := postProjectCommentMedia(t, admin, server.URL+"/api/documents/missing-document/attachments", "a.txt", "text/plain", "x"); status != http.StatusNotFound && status != http.StatusForbidden {
		t.Fatalf("unknown document status %d: %s", status, raw)
	}
}
