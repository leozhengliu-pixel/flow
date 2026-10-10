package main

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func (f *mcpContractFixture) document(t *testing.T, id string) (domain.Document, bool) {
	t.Helper()
	for _, item := range f.repository.Bootstrap().Documents {
		if item.ID == id {
			return item, true
		}
	}
	return domain.Document{}, false
}

func TestMCPDeleteAndRestoreDocument(t *testing.T) {
	f := newMCPContractFixture(t)
	created := mcpObject(t, f, "save_document", map[string]any{"title": "Retired plan", "content": "Old plan"})
	id, slug := created["id"].(string), created["slugId"].(string)

	restricted := f.addMCPKey(t, "restricted-document-delete", "usr_admin", []string{"read", "write"}, []string{})
	mcpObject(t, f, "save_document", map[string]any{"id": id, "team": "TST"})
	mcpDeniedWith(t, f, restricted, "delete_document", map[string]any{"id": id}, "not found")
	readOnly := f.addMCPKey(t, "read-only-document-delete", "usr_admin", []string{"read"}, nil)
	mcpDeniedWith(t, f, readOnly, "delete_document", map[string]any{"id": id}, "write scope")

	deleted := mcpObject(t, f, "delete_document", map[string]any{"id": slug})
	if deleted["deleted"] != true || deleted["id"] != id || deleted["title"] != "Retired plan" {
		t.Fatalf("delete receipt: %v", deleted)
	}
	until, err := time.Parse(time.RFC3339Nano, deleted["restorableUntil"].(string))
	if err != nil || until.Before(time.Now().AddDate(0, 0, 29)) {
		t.Fatalf("restorableUntil: %v %v", deleted["restorableUntil"], err)
	}
	if _, ok := f.document(t, id); ok {
		t.Fatal("document still listed after delete")
	}
	if message := mcpToolError(t, f, "get_document", map[string]any{"id": slug}); !strings.Contains(message, "not found") {
		t.Fatalf("get deleted document: %s", message)
	}
	mcpDeniedWith(t, f, restricted, "restore_document", map[string]any{"id": slug}, "not found")

	restored := mcpObject(t, f, "restore_document", map[string]any{"id": slug})
	if restored["restored"] != true || restored["id"] != id || !strings.HasSuffix(restored["url"].(string), "/document/"+slug) {
		t.Fatalf("restore receipt: %v", restored)
	}
	if document, ok := f.document(t, id); !ok || document.Content != "Old plan" {
		t.Fatalf("restored document: %+v %v", document, ok)
	}
	if message := mcpToolError(t, f, "restore_document", map[string]any{"id": id}); !strings.Contains(message, "not deleted") {
		t.Fatalf("restore live document: %s", message)
	}
	if message := mcpToolError(t, f, "restore_document", map[string]any{"id": "never-existed"}); !strings.Contains(message, "not found") {
		t.Fatalf("restore missing document: %s", message)
	}
}

func TestMCPGetDocumentResolvesPreviousSlugs(t *testing.T) {
	f := newMCPContractFixture(t)
	created := mcpObject(t, f, "save_document", map[string]any{"title": "Renamed spec"})
	if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "test.previous_slug", created["id"].(string), nil, func(data *domain.Bootstrap) error {
		document, err := documentByID(data, created["id"].(string))
		if err == nil {
			document.PreviousSlugIDs = []string{"old-spec-abc123"}
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if found := mcpObject(t, f, "get_document", map[string]any{"id": "old-spec-abc123"}); found["id"] != created["id"] {
		t.Fatalf("previous slug resolved to %v", found["id"])
	}
}

func TestMCPDocumentHistoryAndRestoreVersion(t *testing.T) {
	f := newMCPContractFixture(t)
	created := mcpObject(t, f, "save_document", map[string]any{"title": "Runbook", "content": "Current steps"})
	id := created["id"].(string)
	now := time.Now().UTC()
	older, oldest := now.Add(-2*time.Hour), now.Add(-3*time.Hour)
	big := strings.Repeat("x", mcpDocumentRevisionContentLimit+500)
	if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "test.revisions", id, nil, func(data *domain.Bootstrap) error {
		document, err := documentByID(data, id)
		if err != nil {
			return err
		}
		document.Revisions = []domain.DocumentRevision{
			{ID: "rev-current", DocumentID: id, Title: "Runbook", Content: "Current steps", Author: data.Viewer, AuthorIDs: []string{data.Viewer.ID, "usr_member"}, CreatedAt: now, StartedAt: &now},
			{ID: "rev-big", DocumentID: id, Title: "Runbook", Content: big, Author: data.Viewer, CreatedAt: older, StartedAt: &older},
			{ID: "rev-old", DocumentID: id, Title: "Runbook v1", Content: "First steps", Author: data.Viewer, CreatedAt: oldest, StartedAt: &oldest},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	history := mcpObject(t, f, "list_document_history", map[string]any{"id": id})
	items := mcpItems(t, history)
	if len(items) != 3 || items[0]["id"] != "rev-current" || items[0]["current"] != true || items[1]["current"] != false || items[2]["content"] != "First steps" {
		t.Fatalf("history: %v", items)
	}
	if author := items[0]["author"].(map[string]any); author["id"] != f.data.Viewer.ID || author["name"] == "" {
		t.Fatalf("author: %v", author)
	}
	if authors := items[0]["authors"].([]any); len(authors) != 2 {
		t.Fatalf("authors: %v", authors)
	}
	if content := items[1]["content"].(string); len(content) != mcpDocumentRevisionContentLimit || items[1]["contentTruncated"] != true || items[0]["contentTruncated"] != false {
		t.Fatalf("truncation: %d %v", len(content), items[1]["contentTruncated"])
	}
	first := mcpObject(t, f, "list_document_history", map[string]any{"id": id, "limit": 1})
	if items := mcpItems(t, first); len(items) != 1 || first["nextCursor"] != "1" {
		t.Fatalf("first page: %v", first)
	}
	second := mcpObject(t, f, "list_document_history", map[string]any{"id": id, "limit": 1, "cursor": "1"})
	if items := mcpItems(t, second); len(items) != 1 || items[0]["id"] != "rev-big" || items[0]["current"] != false {
		t.Fatalf("second page: %v", second)
	}

	restored := mcpObject(t, f, "restore_document_version", map[string]any{"id": created["slugId"], "revisionId": "rev-old"})
	if restored["restored"] != true || restored["revisionId"] != "rev-old" {
		t.Fatalf("restore receipt: %v", restored)
	}
	if document, _ := f.document(t, id); document.Content != "First steps" || document.Title != "Runbook v1" {
		t.Fatalf("restored version: %q %q", document.Title, document.Content)
	}
	if message := mcpToolError(t, f, "restore_document_version", map[string]any{"id": id, "revisionId": "rev-missing"}); !strings.Contains(message, "not found") {
		t.Fatalf("missing version: %s", message)
	}
}

func TestMCPDocumentPermissionsWithFriendlySubjects(t *testing.T) {
	f := newMCPContractFixture(t)
	created := mcpObject(t, f, "save_document", map[string]any{"title": "Shared notes"})
	id := created["id"].(string)
	var member domain.User
	for _, user := range f.data.Users {
		if user.ID == "usr_member" {
			member = user
		}
	}
	if member.Email == "" {
		t.Fatal("fixture member not found")
	}
	empty := mcpObject(t, f, "get_document_permissions", map[string]any{"id": id})
	if items := mcpItems(t, empty); len(items) != 2 || items[0]["role"] != "owner" || items[0]["subjectId"] != f.data.Viewer.ID || items[1]["subjectType"] != "workspace" || items[1]["role"] != "editor" {
		t.Fatalf("initial permissions: %v", items)
	}

	saved := mcpObject(t, f, "save_document_permissions", map[string]any{"id": created["slugId"], "permissions": []map[string]any{
		{"subject": "workspace", "role": "viewer"},
		{"subject": "TST", "role": "editor"},
		{"subject": member.Email, "role": "commenter"},
		{"subjectType": "user", "subject": "me", "role": "owner"},
	}})
	if saved["explicit"] != true {
		t.Fatalf("receipt: %v", saved)
	}
	roles := map[string]string{}
	for _, item := range mcpItems(t, saved) {
		roles[item["subjectType"].(string)+":"+item["subjectId"].(string)] = item["role"].(string)
		if item["subject"] == nil {
			t.Fatalf("unresolved subject: %v", item)
		}
	}
	want := map[string]string{"workspace:" + f.data.Workspace.ID: "viewer", "team:team_test": "editor", "user:" + member.ID: "commenter", "user:" + f.data.Viewer.ID: "owner"}
	for key, role := range want {
		if roles[key] != role {
			t.Fatalf("permissions %v, want %v", roles, want)
		}
	}
	document, _ := f.document(t, id)
	if len(document.Permissions) != 4 {
		t.Fatalf("stored permissions: %+v", document.Permissions)
	}
	listed := mcpObject(t, f, "get_document_permissions", map[string]any{"id": id})
	if !slices.ContainsFunc(mcpItems(t, listed), func(item map[string]any) bool {
		subject, _ := item["subject"].(map[string]any)
		return item["subjectType"] == "team" && subject["key"] == "TST"
	}) {
		t.Fatalf("listed permissions: %v", listed)
	}

	// Replace semantics: entries left out lose access; the creator stays owner.
	mcpObject(t, f, "save_document_permissions", map[string]any{"id": id, "permissions": []map[string]any{{"subject": member.Name, "role": "editor"}}})
	if document, _ = f.document(t, id); len(document.Permissions) != 2 || !slices.ContainsFunc(document.Permissions, func(item domain.DocumentPermission) bool {
		return item.SubjectID == f.data.Viewer.ID && item.Role == "owner"
	}) {
		t.Fatalf("replaced permissions: %+v", document.Permissions)
	}
	if message := mcpToolError(t, f, "save_document_permissions", map[string]any{"id": id, "permissions": []map[string]any{{"subject": "TST", "role": "owner"}}}); !strings.Contains(message, "only users") {
		t.Fatalf("team owner: %s", message)
	}
	if message := mcpToolError(t, f, "save_document_permissions", map[string]any{"id": id, "permissions": []map[string]any{{"subject": "nobody-here", "role": "viewer"}}}); !strings.Contains(message, "no user or team") {
		t.Fatalf("unknown subject: %s", message)
	}
	readOnly := f.addMCPKey(t, "read-only-document-permissions", "usr_admin", []string{"read"}, nil)
	mcpDeniedWith(t, f, readOnly, "save_document_permissions", map[string]any{"id": id, "permissions": []map[string]any{}}, "write scope")
}

func TestMCPDocumentReminder(t *testing.T) {
	f := newMCPContractFixture(t)
	created := mcpObject(t, f, "save_document", map[string]any{"title": "Quarterly review"})
	reminder := mcpObject(t, f, "create_reminder", map[string]any{"document": created["slugId"], "remindAt": "P1D"})
	target := reminder["target"].(map[string]any)
	if target["type"] != "document" || target["id"] != created["id"] {
		t.Fatalf("reminder: %v", reminder)
	}
	if !slices.ContainsFunc(f.repository.Bootstrap().Notifications, func(item domain.Notification) bool {
		return item.ID == reminder["id"] && item.Type == "documentReminder" && item.SourceID == created["id"] && item.SnoozedUntil != nil
	}) {
		t.Fatal("document reminder not stored")
	}
	if message := mcpToolError(t, f, "create_reminder", map[string]any{"document": created["id"], "issue": f.data.Issues[0].ID, "remindAt": "P1D"}); !strings.Contains(message, "exactly one") {
		t.Fatalf("two targets: %s", message)
	}
}

func TestMCPListDocumentCommentsCarriesInlineAnchors(t *testing.T) {
	f := newMCPContractFixture(t)
	created := mcpObject(t, f, "save_document", map[string]any{"title": "Design doc", "content": "We will ship the new editor in May."})
	id := created["id"].(string)
	actor := mcpActor{WorkspaceKey: f.data.Workspace.URLKey, User: f.data.Viewer}
	comment := func(input map[string]any) string {
		t.Helper()
		result, err := f.service.invokeMCPRoute(t.Context(), actor, http.MethodPost, "/api/documents/"+id+"/comments", map[string]string{"id": id}, input, f.service.createDocumentComment)
		if err != nil {
			t.Fatal(err)
		}
		return result.(map[string]any)["id"].(string)
	}
	inline := comment(map[string]any{"body": "Is May realistic?", "anchorId": "anchor-may", "quotedText": "ship the new editor in May"})
	reply := comment(map[string]any{"body": "Yes", "parentId": inline})
	page := comment(map[string]any{"body": "Looks good overall"})

	listed := mcpObject(t, f, "list_comments", map[string]any{"documentId": created["slugId"]})
	byID := map[string]map[string]any{}
	for _, item := range mcpItems(t, listed) {
		byID[item["id"].(string)] = item
	}
	if item := byID[inline]; item["quotedText"] != "ship the new editor in May" || item["anchorId"] != "anchor-may" || item["resolved"] != false {
		t.Fatalf("inline comment: %v", item)
	}
	for _, other := range []string{reply, page} {
		item, ok := byID[other]
		if !ok {
			t.Fatalf("comment %s missing from %v", other, byID)
		}
		if quoted, present := item["quotedText"]; !present || quoted != nil || item["anchorId"] != nil {
			t.Fatalf("non-inline comment should carry null quotedText/anchorId: %v", item)
		}
	}
}
