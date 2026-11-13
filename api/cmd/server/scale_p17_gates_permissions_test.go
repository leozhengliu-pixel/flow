package main

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestScaleP17GatesPermissionSubjectsUseRecordScope(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_DOCUMENT_RECORDS", "on")
	data := domain.Bootstrap{Documents: []domain.Document{{ID: "document"}}}
	r := httptest.NewRequest(http.MethodPut, "/api/documents/document/permissions", nil)
	r = documentPermissionReadRequest(r, []domain.DocumentPermission{
		{SubjectType: " USER ", SubjectID: " member ", Role: "editor"},
		{SubjectType: "user", SubjectID: "member", Role: "viewer"},
		{SubjectType: "team", SubjectID: "team", Role: "editor"},
		{SubjectType: "workspace", Role: "viewer"},
	})
	scope, ok := documentReadRequirements(data, r)
	if !ok || slices.Contains(scope.Fields, "users") || !slices.Equal(scope.Read, []store.RecordKey{{Kind: "users", ID: "member"}}) {
		t.Fatalf("permission subjects did not remain a deduplicated point read: %+v", scope)
	}
	ordinary := httptest.NewRequest(http.MethodGet, "/api/documents/document", nil)
	scope, ok = documentReadRequirements(data, ordinary)
	if !ok || slices.Contains(scope.Fields, "users") || slices.ContainsFunc(scope.Read, func(key store.RecordKey) bool { return key.Kind == "users" }) {
		t.Fatal("ordinary document read expanded its user directory")
	}
}

func TestScaleP17GatesPermissionSubjectSelection(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_DOCUMENT_RECORDS", "on")
	db := newScaleP04Database(t)
	handler := db.handler(t, true)
	doc := requestJSON[domain.Document](t, handler, http.MethodPost, "/api/documents", map[string]any{"title": "Permission subject selection"}, http.StatusCreated)
	if len(db.users) < 2 {
		t.Fatal("isolated fixture has no second member")
	}
	permission := domain.DocumentPermission{SubjectType: "user", SubjectID: db.users[1].ID, Role: "editor"}
	r := httptest.NewRequest(http.MethodPut, "/api/documents/"+doc.ID+"/permissions", nil)
	r = documentPermissionReadRequest(r, []domain.DocumentPermission{permission})
	selected, err := db.repo.SelectedDocumentMetadata(t.Context(), db.key, "", nil, func(data domain.Bootstrap) (store.MetadataRecordScope, bool) {
		return documentReadRequirements(data, r)
	})
	if err != nil || len(selected.Users) != 1 || selected.Users[0].ID != permission.SubjectID || !validDocumentPermission(selected, permission) {
		t.Fatalf("selected permission subject was omitted: users=%d err=%v", len(selected.Users), err)
	}
	permission.SubjectID = "outside-the-workspace"
	if validDocumentPermission(selected, permission) {
		t.Fatal("an unselected foreign user became a valid permission subject")
	}
}
