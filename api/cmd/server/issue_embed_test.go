package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"path/filepath"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestAttachmentEmbedMarkdownKinds(t *testing.T) {
	image := attachmentEmbedMarkdown(domain.Attachment{Title: "shot.png", URL: "/uploads/shot.png", ContentType: "image/png"})
	if image != "![shot.png](/uploads/shot.png)" {
		t.Fatalf("image markdown = %q", image)
	}
	video := attachmentEmbedMarkdown(domain.Attachment{Title: "clip.mp4", URL: "/uploads/clip.mp4", ContentType: "video/mp4"})
	if video != "::video[clip.mp4](/uploads/clip.mp4)" {
		t.Fatalf("video markdown = %q", video)
	}
	file := attachmentEmbedMarkdown(domain.Attachment{Title: "notes.txt", URL: "/uploads/notes.txt", ContentType: "text/plain", Size: 12})
	if file != `::file[notes.txt](/uploads/notes.txt){size="12" type="text/plain"}` {
		t.Fatalf("file markdown = %q", file)
	}
	if attachmentEmbedKind(domain.Attachment{ContentType: "text/uri-list", URL: "https://example.test"}) != "link" {
		t.Fatal("uri-list should stay a resource link")
	}
	if attachmentEmbedKind(domain.Attachment{Title: "photo.JPEG", ContentType: "application/octet-stream"}) != "image" {
		t.Fatal("image extension should embed as an image when the MIME type is generic")
	}
}

func TestAppendIssueEmbedMarkdown(t *testing.T) {
	if got := appendIssueEmbedMarkdown("", "![a](/uploads/a.png)"); got != "![a](/uploads/a.png)\n" {
		t.Fatalf("empty body = %q", got)
	}
	if got := appendIssueEmbedMarkdown("Intro\n", "![a](/uploads/a.png)"); got != "Intro\n\n![a](/uploads/a.png)\n" {
		t.Fatalf("existing body = %q", got)
	}
}

func TestHTTPAttachmentEmbedsInIssueDescription(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "embed.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, authDisabled: true, uploadPath: t.TempDir()})
	created := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issue-records", map[string]any{"title": "Embed target", "description": "Existing copy"}, http.StatusCreated)

	image := uploadAttachmentWith(t, handler, created.ID, "shot.png", "image/png", []byte("\x89PNG\r\n\x1a\n"), nil)
	issue := requestJSON[domain.Issue](t, handler, http.MethodGet, "/api/issue-records/"+created.ID, nil, http.StatusOK)
	wantImage := "![shot.png](" + image.URL + ")"
	if !strings.Contains(issue.Description, wantImage) || issue.DescriptionState != "" {
		t.Fatalf("image did not land in the description: %+v", issue)
	}
	if issue.DocumentContent == nil || issue.DocumentContent.Content != issue.Description || issue.DocumentContent.ContentState != "" {
		t.Fatalf("image embed left a stale rich-text snapshot: %+v", issue.DocumentContent)
	}

	plain := uploadAttachmentWith(t, handler, created.ID, "notes.txt", "text/plain", []byte("hello"), nil)
	issue = requestJSON[domain.Issue](t, handler, http.MethodGet, "/api/issue-records/"+created.ID, nil, http.StatusOK)
	if !strings.Contains(issue.Description, wantImage) || !strings.Contains(issue.Description, `::file[notes.txt](`+plain.URL+`)`) {
		t.Fatalf("file card was not appended: %q", issue.Description)
	}

	before := issue.Description
	skipped := uploadAttachmentWith(t, handler, created.ID, "ignored.png", "image/png", []byte("\x89PNG"), map[string]string{"embed": "0"})
	issue = requestJSON[domain.Issue](t, handler, http.MethodGet, "/api/issue-records/"+created.ID, nil, http.StatusOK)
	if issue.Description != before {
		t.Fatalf("embed=0 rewrote the description: %q -> %q", before, issue.Description)
	}
	if skipped.URL == "" {
		t.Fatal("embed=0 still has to store the file")
	}

	link := requestJSON[domain.Attachment](t, handler, http.MethodPost, "/api/issues/"+created.ID+"/links", map[string]string{"url": "https://example.test/doc", "title": "Doc"}, http.StatusCreated)
	issue = requestJSON[domain.Issue](t, handler, http.MethodGet, "/api/issue-records/"+created.ID, nil, http.StatusOK)
	if strings.Contains(issue.Description, link.URL) {
		t.Fatalf("resource links should not embed: %q", issue.Description)
	}
}

func TestMCPAttachmentEmbedsInIssueDescription(t *testing.T) {
	repository, actor, ctx := newMCPToolTestContext(t)
	service := &server{store: repository, uploadPath: t.TempDir()}
	created, err := service.callFlowTool(ctx, actor, "save_issue", map[string]any{"title": "MCP embed", "description": "Agent body", "team": repository.Bootstrap().Teams[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	var issue domain.Issue
	if err := jsonClone(created, &issue); err != nil {
		t.Fatal(err)
	}
	body := []byte("\x89PNG\r\n\x1a\nflow")
	digest := sha256.Sum256(body)
	result, err := service.callFlowTool(ctx, actor, "create_attachment", map[string]any{
		"issue": issue.ID, "filename": "diagram.png", "contentType": "image/png",
		"base64Content": base64.StdEncoding.EncodeToString(body), "sha256": hex.EncodeToString(digest[:]), "size": len(body),
	})
	if err != nil {
		t.Fatal(err)
	}
	var attachment domain.Attachment
	if err := jsonClone(result, &attachment); err != nil {
		t.Fatal(err)
	}
	stored, err := repository.IssueRecord(ctx, actor.WorkspaceKey, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stored.Description, "![diagram.png]("+attachment.URL+")") || stored.DescriptionState != "" {
		t.Fatalf("MCP attachment stayed in resources: %+v", stored)
	}
	if stored.DocumentContent == nil || stored.DocumentContent.Content != stored.Description {
		t.Fatalf("MCP embed did not replace the editor snapshot: %+v", stored.DocumentContent)
	}

	again, err := service.callFlowTool(ctx, actor, "create_attachment", map[string]any{
		"issue": issue.ID, "filename": "diagram.png", "contentType": "image/png",
		"base64Content": base64.StdEncoding.EncodeToString(body), "sha256": hex.EncodeToString(digest[:]), "size": len(body),
	})
	if err != nil {
		t.Fatal(err)
	}
	var duplicate domain.Attachment
	if err := jsonClone(again, &duplicate); err != nil {
		t.Fatal(err)
	}
	after, err := repository.IssueRecord(ctx, actor.WorkspaceKey, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(after.Description, attachment.URL) != 1 {
		t.Fatalf("existing URL should not be re-embedded: %q", after.Description)
	}
	if !strings.Contains(after.Description, duplicate.URL) {
		t.Fatalf("a new upload should still embed: %q", after.Description)
	}
}

func uploadAttachmentWith(t *testing.T, handler http.Handler, issueID, name, contentType string, contents []byte, fields map[string]string) domain.Attachment {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="file"; filename="`+name+`"`)
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(contents); err != nil {
		t.Fatal(err)
	}
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/issues/"+issueID+"/attachments", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("upload status %d: %s", recorder.Code, recorder.Body.String())
	}
	var attachment domain.Attachment
	if err := json.Unmarshal(recorder.Body.Bytes(), &attachment); err != nil {
		t.Fatal(err)
	}
	return attachment
}
