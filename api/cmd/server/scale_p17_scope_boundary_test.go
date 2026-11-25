package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP17UnmigratedDocumentHandlersKeepCompleteInputs(t *testing.T) {
	for _, path := range []string{"/api/documents?first=10", "/api/documents?connection=true", "/api/documents/id/drafts", "/api/documents/id/attachments", "/api/documents/id/reminders", "/api/documents/id/future-detail", "/api/documents/id/comments/comment/attachments"} {
		if documentRecordRequest(httptest.NewRequest("GET", path, nil)) {
			t.Fatalf("unmigrated handler received a partial handoff: %s", path)
		}
	}
}

func TestScaleP17DevelopmentReadUsesTheDefaultWorkspace(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_DOCUMENT_RECORDS", "on")
	db := newScaleP04Database(t)
	if err := db.repo.MutateWorkspace(t.Context(), db.key, "p17.fixture", "", nil, func(data *domain.Bootstrap) error {
		data.Documents = append(data.Documents, domain.Document{ID: "p17-default", Title: "Default workspace", Creator: data.Viewer})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	service := &server{store: db.repo, authDisabled: true}
	r := httptest.NewRequest("GET", "/api/documents/p17-default", nil)
	r.SetPathValue("id", "p17-default")
	w := httptest.NewRecorder()
	service.getDocument(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("default-workspace detail status=%d: %s", w.Code, w.Body.String())
	}
}
