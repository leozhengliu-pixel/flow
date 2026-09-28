package main

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func postProjectCommentMedia(t *testing.T, client *http.Client, url, name, contentType, contents string) (int, []byte) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if name != "" {
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", `form-data; name="file"; filename="`+name+`"`)
		header.Set("Content-Type", contentType)
		part, err := writer.CreatePart(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte(contents)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, url, &body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("X-Workspace-Key", "test-workspace")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	return response.StatusCode, raw
}

func TestProjectCommentAttachmentUploadIsInlineAndVisible(t *testing.T) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "comment-media.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	server := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir()}))
	defer server.Close()
	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, http.MethodPost, server.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	bootstrap := authRequest[domain.Bootstrap](t, admin, http.MethodGet, server.URL+"/api/bootstrap", nil, "test-workspace", http.StatusOK)
	if len(bootstrap.Projects) == 0 {
		t.Fatal("seed must include a project")
	}
	project := bootstrap.Projects[0]
	endpoint := server.URL + "/api/projects/" + project.ID + "/comment-attachments"

	status, raw := postProjectCommentMedia(t, admin, endpoint, "diagram.png", "image/png", "\x89PNG\r\n\x1a\ninline image")
	if status != http.StatusCreated {
		t.Fatalf("upload status %d: %s", status, raw)
	}
	var attachment domain.Attachment
	if err := json.Unmarshal(raw, &attachment); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(attachment.URL, "/uploads/project_comment_attachment_") || attachment.Title != "diagram.png" || attachment.Size == 0 || attachment.Creator.ID == "" {
		t.Fatalf("attachment = %#v", attachment)
	}
	authStatus(t, admin, http.MethodGet, server.URL+attachment.URL, http.StatusOK)

	// The inline URL is recorded on the project so it stays servable, but it
	// is not part of the list projection.
	refreshed := authRequest[domain.Bootstrap](t, admin, http.MethodGet, server.URL+"/api/bootstrap", nil, "test-workspace", http.StatusOK)
	index := slices.IndexFunc(refreshed.Projects, func(item domain.Project) bool { return item.ID == project.ID })
	if index < 0 {
		t.Fatalf("project %s missing from bootstrap", project.ID)
	}
	detail := refreshed.Projects[index]
	if !slices.ContainsFunc(detail.CommentAttachments, func(item domain.Attachment) bool { return item.URL == attachment.URL }) {
		t.Fatalf("project comment attachments = %#v", detail.CommentAttachments)
	}
	if listed := store.ProjectListProjection(detail); listed.CommentAttachments != nil {
		t.Fatalf("list projection leaked comment attachments: %#v", listed.CommentAttachments)
	}

	outsider, _ := verifiedAuthClient(t, server.URL, "Outsider", "outsider-comment-media@example.com")
	authStatus(t, outsider, http.MethodGet, server.URL+attachment.URL, http.StatusNotFound)

	if status, raw := postProjectCommentMedia(t, admin, endpoint, "", "", ""); status != http.StatusBadRequest {
		t.Fatalf("missing file status %d: %s", status, raw)
	}
	if status, raw := postProjectCommentMedia(t, admin, server.URL+"/api/projects/missing-project/comment-attachments", "a.txt", "text/plain", "x"); status != http.StatusNotFound && status != http.StatusForbidden {
		t.Fatalf("unknown project status %d: %s", status, raw)
	}
}
