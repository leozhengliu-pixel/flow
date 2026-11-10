package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func uploadIssueAttachmentForTest(t *testing.T, handler http.Handler, issueID, filename, content string) domain.Attachment {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/issues/"+issueID+"/attachments", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("issue attachment status %d: %s", recorder.Code, recorder.Body.String())
	}
	var attachment domain.Attachment
	if err := json.Unmarshal(recorder.Body.Bytes(), &attachment); err != nil {
		t.Fatal(err)
	}
	return attachment
}

func countRows(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var count int
	if err := db.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// A trashed issue keeps its attachment files until the trash entry is
// purged; purging deletes the files no other issue attaches. Search
// documents and the attachment index go when the issue is deleted.
func TestTrashPurgeDeletesTheAttachmentFilesOfPurgedIssues(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "flow.db")
	repository, err := store.OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	uploads := t.TempDir()
	s := &server{store: repository, uploadPath: uploads, authDisabled: true}
	handler := newHandler(s)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	create := func(title string) domain.Issue {
		return requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": title, "teamId": bootstrap.Teams[0].ID}, http.StatusCreated)
	}
	doomed := create("Purge leak probe")
	kept := create("Keeps a shared file")
	attachment := uploadIssueAttachmentForTest(t, handler, doomed.ID, "report.txt", "secret contents")
	shared := uploadIssueAttachmentForTest(t, handler, doomed.ID, "shared.txt", "shared contents")
	name := strings.TrimPrefix(attachment.URL, "/uploads/")
	sharedName := strings.TrimPrefix(shared.URL, "/uploads/")
	// The second issue links the same upload (an attachment copied by URL).
	if err := repository.MutateWorkspace(store.WithIssueRecordMutations(ctx, kept.ID), "test-workspace", "attachment.created", kept.ID, nil, func(data *domain.Bootstrap) error {
		for index := range data.Issues {
			if data.Issues[index].ID == kept.ID {
				data.Issues[index].Attachments = append(data.Issues[index].Attachments, domain.Attachment{ID: "attachment_copy", IssueID: kept.ID, Title: "shared.txt", URL: shared.URL, CreatedAt: time.Now().UTC()})
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(uploads, name)); err != nil {
		t.Fatalf("uploaded attachment missing: %v", err)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM issue_search_documents WHERE workspace_key='test-workspace' AND issue_id=?`, doomed.ID); got != 1 {
		t.Fatalf("search documents before delete = %d, want 1", got)
	}

	requestJSON[any](t, handler, http.MethodDelete, "/api/issues/"+doomed.ID, nil, http.StatusNoContent)
	for table, query := range map[string]string{
		"issue_search_documents":   `SELECT COUNT(*) FROM issue_search_documents WHERE workspace_key='test-workspace' AND issue_id=?`,
		"issue_attachment_records": `SELECT COUNT(*) FROM issue_attachment_records WHERE workspace_key='test-workspace' AND issue_id=?`,
		"issue_records":            `SELECT COUNT(*) FROM issue_records WHERE workspace_key='test-workspace' AND id=?`,
	} {
		if got := countRows(t, db, query, doomed.ID); got != 0 {
			t.Fatalf("%s rows after delete = %d, want 0", table, got)
		}
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM issue_search_fts WHERE issue_search_fts MATCH ?`, `"Purge leak probe"`); got != 0 {
		t.Fatalf("full-text index still finds the deleted issue: %d", got)
	}
	// Restorable until the trash entry is purged: the file stays.
	if _, err := os.Stat(filepath.Join(uploads, name)); err != nil {
		t.Fatalf("trashed issue's attachment was deleted early: %v", err)
	}

	if _, err := s.purgeExpiredTrash(ctx, "test-workspace", time.Now().Add(trashRetention+time.Hour)); err != nil {
		t.Fatal(err)
	}
	if data, _ := repository.WorkspaceMetadataFields("test-workspace", "trash"); len(data.Trash) != 0 {
		t.Fatalf("trash after purge = %d entries", len(data.Trash))
	}
	if _, err := os.Stat(filepath.Join(uploads, name)); !os.IsNotExist(err) {
		t.Fatalf("purged issue's attachment file left behind (stat err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(uploads, sharedName)); err != nil {
		t.Fatalf("file another issue attaches was deleted: %v", err)
	}
}
