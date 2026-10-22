package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// ageDocumentWithoutHistory makes a document look like one created days ago
// that was never edited since: a body, no versions.
func ageDocumentWithoutHistory(t *testing.T, f *mcpContractFixture, id string, at time.Time) {
	t.Helper()
	if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "test.aged", id, nil, func(data *domain.Bootstrap) error {
		document, err := documentByID(data, id)
		if err != nil {
			return err
		}
		document.Revisions = []domain.DocumentRevision{}
		document.CreatedAt, document.UpdatedAt = at, at
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func ageStoredDocument(t *testing.T, repository *store.SQLiteStore, id string, at time.Time, revisions []domain.DocumentRevision) {
	t.Helper()
	workspace := repository.Bootstrap().Workspace.URLKey
	if err := repository.MutateWorkspace(context.Background(), workspace, "test.aged", id, nil, func(data *domain.Bootstrap) error {
		document, err := documentByID(data, id)
		if err != nil {
			return err
		}
		document.Revisions = revisions
		document.CreatedAt, document.UpdatedAt = at, at
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func documentHistory(t *testing.T, handler http.Handler, id string) []domain.DocumentRevision {
	t.Helper()
	return requestJSON[struct {
		Nodes []domain.DocumentRevision `json:"nodes"`
	}](t, handler, http.MethodGet, "/api/documents/"+id+"/history?limit=50", nil, http.StatusOK).Nodes
}

func richParagraph(text string) map[string]any {
	return map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": text}}}}}
}

// Editor saves (Markdown + collaborative state + rich JSON, the path the
// realtime editor persists through) keep the pre-edit baseline with its
// rich state, and a following burst never merges into it.
func TestDocumentFirstEditorSaveKeepsRichBaseline(t *testing.T) {
	repository, handler := newDocumentLifecycleServer(t)
	created := requestJSON[domain.Document](t, handler, http.MethodPost, "/api/documents", map[string]any{"title": "Plan", "content": "Original text", "contentData": richParagraph("Original text")}, http.StatusCreated)
	// The same body again is no content change: no version is written.
	requestJSON[domain.Document](t, handler, http.MethodPatch, "/api/documents/"+created.ID, map[string]any{"content": "Original text"}, http.StatusOK)
	if history := documentHistory(t, handler, created.ID); len(history) != 0 {
		t.Fatalf("an unchanged save wrote versions: %#v", history)
	}
	weekAgo := time.Now().UTC().AddDate(0, 0, -7).Truncate(time.Second)
	ageStoredDocument(t, repository, created.ID, weekAgo, []domain.DocumentRevision{})

	for _, text := range []string{"Updated text", "Updated text again"} {
		requestJSON[domain.Document](t, handler, http.MethodPatch, "/api/documents/"+created.ID, map[string]any{"content": text, "contentState": "yjs-" + text, "contentData": richParagraph(text)}, http.StatusOK)
	}
	history := documentHistory(t, handler, created.ID)
	if len(history) != 2 || history[0].Content != "Updated text again" || history[0].ContentState == "" {
		t.Fatalf("history after an editing burst: %#v", history)
	}
	baseline := history[1]
	if baseline.Content != "Original text" || baseline.Title != "Plan" || !jsonEqual(baseline.ContentData, richParagraph("Original text")) || !baseline.CreatedAt.Equal(weekAgo) || baseline.StartedAt != nil || baseline.Author.ID != created.Creator.ID {
		t.Fatalf("baseline: %#v", baseline)
	}

	restored := requestJSON[domain.Document](t, handler, http.MethodPost, "/api/documents/"+created.ID+"/restore/"+baseline.ID, nil, http.StatusOK)
	if restored.Content != "Original text" || !jsonEqual(restored.ContentData, richParagraph("Original text")) || restored.ContentState != "" {
		t.Fatalf("restored baseline: %#v", restored)
	}
	if history := documentHistory(t, handler, created.ID); len(history) != 3 || history[0].Content != "Original text" || history[2].ID != baseline.ID {
		t.Fatalf("history after restore: %#v", history)
	}
}

func TestDocumentBaselineRules(t *testing.T) {
	repository, handler := newDocumentLifecycleServer(t)
	weekAgo := time.Now().UTC().AddDate(0, 0, -7).Truncate(time.Second)

	// A title-only change keeps the old title with the body.
	renamed := requestJSON[domain.Document](t, handler, http.MethodPost, "/api/documents", map[string]any{"title": "Old name", "content": "Body"}, http.StatusCreated)
	ageStoredDocument(t, repository, renamed.ID, weekAgo, nil)
	requestJSON[domain.Document](t, handler, http.MethodPatch, "/api/documents/"+renamed.ID, map[string]any{"title": "New name"}, http.StatusOK)
	if history := documentHistory(t, handler, renamed.ID); len(history) != 2 || history[0].Title != "New name" || history[1].Title != "Old name" || history[1].Content != "Body" {
		t.Fatalf("title-only change: %#v", history)
	}

	// An empty document has nothing to keep.
	empty := requestJSON[domain.Document](t, handler, http.MethodPost, "/api/documents", map[string]any{"title": "Fresh"}, http.StatusCreated)
	requestJSON[domain.Document](t, handler, http.MethodPatch, "/api/documents/"+empty.ID, map[string]any{"content": "First words"}, http.StatusOK)
	if history := documentHistory(t, handler, empty.ID); len(history) != 1 || history[0].Content != "First words" {
		t.Fatalf("empty document: %#v", history)
	}

	// A document that already has versions gets no extra baseline.
	versioned := requestJSON[domain.Document](t, handler, http.MethodPost, "/api/documents", map[string]any{"title": "Versioned", "content": "v2"}, http.StatusCreated)
	older := weekAgo.Add(-time.Hour)
	ageStoredDocument(t, repository, versioned.ID, weekAgo, []domain.DocumentRevision{{ID: "rev-v2", DocumentID: versioned.ID, Title: "Versioned", Content: "v2", Author: versioned.Creator, CreatedAt: weekAgo, StartedAt: &older}})
	requestJSON[domain.Document](t, handler, http.MethodPatch, "/api/documents/"+versioned.ID, map[string]any{"content": "v3"}, http.StatusOK)
	if history := documentHistory(t, handler, versioned.ID); len(history) != 2 || history[0].Content != "v3" || history[1].ID != "rev-v2" {
		t.Fatalf("document with history: %#v", history)
	}

	// Publishing a draft replaces the body too.
	drafted := requestJSON[domain.Document](t, handler, http.MethodPost, "/api/documents", map[string]any{"title": "Drafted", "content": "Published body"}, http.StatusCreated)
	ageStoredDocument(t, repository, drafted.ID, weekAgo, nil)
	draft := requestJSON[domain.DocumentContentDraft](t, handler, http.MethodPost, "/api/documents/"+drafted.ID+"/drafts", map[string]any{"content": "Draft body"}, http.StatusCreated)
	requestJSON[domain.Document](t, handler, http.MethodPost, "/api/documents/"+drafted.ID+"/drafts/"+draft.ID+"/publish", nil, http.StatusOK)
	if history := documentHistory(t, handler, drafted.ID); len(history) != 2 || history[0].Content != "Draft body" || history[1].Content != "Published body" {
		t.Fatalf("published draft: %#v", history)
	}
}

// The admin import adds a preserved earlier state as a version without
// touching the document, in time order, once.
func TestImportDocumentRevision(t *testing.T) {
	repository, handler := newDocumentLifecycleServer(t)
	created := requestJSON[domain.Document](t, handler, http.MethodPost, "/api/documents", map[string]any{"title": "Spec", "content": "Original text"}, http.StatusCreated)
	weekAgo := time.Now().UTC().AddDate(0, 0, -7).Truncate(time.Second)
	ageStoredDocument(t, repository, created.ID, weekAgo, nil)
	// The production case: the edit happened before the baseline fix, so the
	// only version is the post-edit state.
	edited := weekAgo.Add(time.Hour)
	if err := repository.MutateWorkspace(context.Background(), repository.Bootstrap().Workspace.URLKey, "test.lost_baseline", created.ID, nil, func(data *domain.Bootstrap) error {
		document, err := documentByID(data, created.ID)
		if err == nil {
			document.Content, document.UpdatedAt, document.Version = "Updated text", edited, 1
			document.Revisions = []domain.DocumentRevision{{ID: "rev-current", DocumentID: created.ID, Title: "Spec", Content: "Updated text", Author: created.Creator, AuthorIDs: []string{created.Creator.ID}, CreatedAt: edited, StartedAt: &edited}}
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	path := "/api/workspace/documents/" + created.ID + "/revisions/import"
	input := map[string]any{"title": "Spec", "content": "Original text", "contentData": richParagraph("Original text"), "createdAt": weekAgo}

	requestJSON[any](t, handler, http.MethodPost, path, map[string]any{"content": "x"}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodPost, path, map[string]any{"content": "x", "createdAt": edited}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodPost, path, map[string]any{"content": "x", "createdAt": weekAgo, "authorId": "usr_nobody"}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodPost, "/api/workspace/documents/missing/revisions/import", input, http.StatusNotFound)

	first := requestJSON[map[string]any](t, handler, http.MethodPost, path, input, http.StatusCreated)
	again := requestJSON[map[string]any](t, handler, http.MethodPost, path, input, http.StatusOK)
	if first["imported"] != true || again["imported"] != false || first["revision"].(map[string]any)["id"] != again["revision"].(map[string]any)["id"] {
		t.Fatalf("import receipts: %v / %v", first, again)
	}
	document := requestJSON[domain.Document](t, handler, http.MethodGet, "/api/documents/"+created.ID, nil, http.StatusOK)
	if document.Content != "Updated text" || document.Version != 1 || !document.UpdatedAt.Equal(edited) || document.Title != "Spec" {
		t.Fatalf("import changed the document: %q v%d %v", document.Content, document.Version, document.UpdatedAt)
	}
	history := documentHistory(t, handler, created.ID)
	if len(history) != 2 || history[0].ID != "rev-current" || history[1].Content != "Original text" || !history[1].CreatedAt.Equal(weekAgo) || !jsonEqual(history[1].ContentData, richParagraph("Original text")) {
		t.Fatalf("history after import: %#v", history)
	}
	audits := 0
	for _, entry := range repository.Bootstrap().AuditLog {
		if entry.Action == "revision_imported" && entry.ResourceID == created.ID {
			audits++
		}
	}
	if audits != 1 {
		t.Fatalf("audit entries = %d, want 1", audits)
	}
	restored := requestJSON[domain.Document](t, handler, http.MethodPost, "/api/documents/"+created.ID+"/restore/"+history[1].ID, nil, http.StatusOK)
	if restored.Content != "Original text" || !jsonEqual(restored.ContentData, richParagraph("Original text")) {
		t.Fatalf("restored import: %#v", restored)
	}
}

// The first edit of a document that has a body but no history keeps the
// pre-edit state as a restorable version.
func TestMCPFirstDocumentEditKeepsPreEditBaseline(t *testing.T) {
	f := newMCPContractFixture(t)
	created := mcpObject(t, f, "save_document", map[string]any{"title": "Spec", "content": "Original text"})
	id := created["id"].(string)
	weekAgo := time.Now().UTC().AddDate(0, 0, -7).Truncate(time.Second)
	ageDocumentWithoutHistory(t, f, id, weekAgo)

	mcpObject(t, f, "save_document", map[string]any{"id": id, "patch": []any{map[string]any{"op": "replace", "old_string": "Original", "new_string": "Updated"}}})
	items := mcpItems(t, mcpObject(t, f, "list_document_history", map[string]any{"id": id}))
	var baseline map[string]any
	for _, item := range items {
		if item["content"] == "Original text" {
			baseline = item
		}
	}
	if baseline == nil {
		t.Fatalf("first update lost the pre-edit baseline: history has %d entries; expected a restorable Original text version: %v", len(items), items)
	}
	if len(items) != 2 || items[0]["content"] != "Updated text" || items[0]["current"] != true || baseline["current"] != false || baseline["title"] != "Spec" {
		t.Fatalf("history after the first edit: %v", items)
	}
	if at, err := time.Parse(time.RFC3339Nano, baseline["createdAt"].(string)); err != nil || !at.Equal(weekAgo) {
		t.Fatalf("baseline time = %v, want the pre-edit updatedAt %v", baseline["createdAt"], weekAgo)
	}
	if author := baseline["author"].(map[string]any); author["id"] != f.data.Viewer.ID {
		t.Fatalf("baseline author: %v", author)
	}

	// A second edit right away joins the post-edit version, never the baseline.
	mcpObject(t, f, "save_document", map[string]any{"id": id, "patch": []any{map[string]any{"op": "replace", "old_string": "Updated", "new_string": "Revised"}}})
	items = mcpItems(t, mcpObject(t, f, "list_document_history", map[string]any{"id": id}))
	if len(items) != 2 || items[0]["content"] != "Revised text" || items[1]["content"] != "Original text" {
		t.Fatalf("history after a second edit: %v", items)
	}

	restored := mcpObject(t, f, "restore_document_version", map[string]any{"id": id, "revisionId": baseline["id"]})
	if restored["restored"] != true {
		t.Fatalf("restore receipt: %v", restored)
	}
	document, _ := f.document(t, id)
	if document.Content != "Original text" || document.Title != "Spec" {
		t.Fatalf("restored baseline: %q %q", document.Title, document.Content)
	}
	items = mcpItems(t, mcpObject(t, f, "list_document_history", map[string]any{"id": id}))
	if len(items) != 3 || items[0]["content"] != "Original text" || items[0]["current"] != true || items[1]["content"] != "Revised text" || items[2]["id"] != baseline["id"] {
		t.Fatalf("history after restoring the baseline: %v", items)
	}
}
