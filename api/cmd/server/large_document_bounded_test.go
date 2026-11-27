package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// A workspace with a document of tens of megabytes used to fall back to
// legacy reads at startup (#264), and users opened and edited the document
// there. Bounded admission now accepts it within the metadata work budget;
// the document's request paths must then serve and edit it within the same
// budget instead of refusing it. These tests open such a workspace with
// bounded metadata on and use the document over HTTP:
//
//   - with a work budget that admits the document, it opens with its whole
//     content, appears in the document list, history and subscribers, takes
//     title and content edits and serves them back;
//   - with a budget too small for it, the start falls back to legacy reads
//     and every route still serves the document (no 500, no lost access);
//   - with the budget held by other work, an edit answers the retryable 503
//     and writes nothing.
//
// The document of #264 is 24 MB of text: eight times its decode cost passes
// 256 MiB, the old fixed limit of one metadata operation. CI writes 3 MB and
// raises the work a record reserves per byte of decode cost in the same
// proportion, so admission decides as it would for the full document.
// FLOW_TEST_LARGE_DOCUMENT_BYTES=24000000 runs the full document with the
// production factor; FLOW_TEST_LARGE_DOCUMENT_BUDGET sets the admitting work
// budget (default 2 GiB: the title edit made under legacy reads keeps the old
// text as a revision, which triples the record's decode cost).
func TestBoundedLargeDocumentRoutes(t *testing.T) {
	const fullSize = 24_000_000
	size, budget := int64(3_000_000), int64(2<<30)
	for name, value := range map[string]*int64{"FLOW_TEST_LARGE_DOCUMENT_BYTES": &size, "FLOW_TEST_LARGE_DOCUMENT_BUDGET": &budget} {
		if raw := os.Getenv(name); raw != "" {
			parsed, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || parsed <= 0 {
				t.Fatalf("%s=%q", name, raw)
			}
			*value = parsed
		}
	}
	if size < fullSize {
		t.Cleanup(store.SetMetadataRecordWorkFactorForTesting(int(8 * fullSize / size)))
	}
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_METADATA_CATALOG", "on")
	t.Setenv("FLOW_SYNC_V2", "on")
	config := scaleP03DatabaseConfig(t)
	config.MaxTransactionBytes, config.MaxStateBytes = 512<<20, 512<<20
	if config.Driver == "mysql" {
		config.RecordJournalCapture = store.RecordJournalCaptureApplication
	}
	const workspace = "large-document"
	text := strings.Repeat("large document text ", int(size)/20+1)[:size]

	open := func(bounded string, work int64) (*store.SQLiteStore, http.Handler) {
		t.Helper()
		t.Setenv("FLOW_BOUNDED_METADATA", bounded)
		t.Setenv("FLOW_METADATA_WORK_BYTES", strconv.FormatInt(work, 10))
		repo, err := store.OpenDatabase(config)
		if err != nil {
			t.Fatal(err)
		}
		return repo, newHandler(&server{store: repo, uploadPath: t.TempDir(), authDisabled: true})
	}
	call := func(handler http.Handler, method, path string, input any, output any) int {
		t.Helper()
		var body []byte
		if input != nil {
			raw, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			body = raw
		}
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Workspace-Key", workspace)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code == http.StatusOK || w.Code == http.StatusCreated {
			if output != nil {
				if err := json.Unmarshal(w.Body.Bytes(), output); err != nil {
					t.Fatalf("%s %s: %v", method, path, err)
				}
			}
		} else {
			t.Logf("%s %s: %d %.300s", method, path, w.Code, w.Body.String())
		}
		return w.Code
	}
	expect := func(handler http.Handler, method, path string, input any, want int, output any) {
		t.Helper()
		if code := call(handler, method, path, input, output); code != want {
			t.Fatalf("%s %s: status %d, want %d", method, path, code, want)
		}
	}

	// Seed with bounded metadata off, as a database written before the
	// document grew, and prepare the catalog.
	repo, handler := open("off", budget)
	expect(handler, http.MethodPost, "/api/workspaces", map[string]any{"name": "Large document", "urlKey": workspace, "region": "us"}, http.StatusCreated, nil)
	id := "large-document-record"
	now := time.Now().UTC()
	if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "documents"), workspace, "test.large_document", "", nil, func(data *domain.Bootstrap) error {
		data.Documents = append(data.Documents, domain.Document{
			ID: id, SlugID: id, Title: "Large document", Content: text, Creator: data.Viewer, Version: 1,
			ProjectIDs: []string{}, TeamIDs: []string{data.Teams[0].ID}, SubscriberIDs: []string{data.Viewer.ID}, CreatedAt: now, UpdatedAt: now,
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if prepared, err := repo.PrepareBoundedMetadata(t.Context()); err != nil || !prepared.Ready {
		t.Fatalf("prepare bounded metadata: %+v %v", prepared, err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}

	routes := func(handler http.Handler, want string) {
		t.Helper()
		var document domain.Document
		expect(handler, http.MethodGet, "/api/documents/"+id, nil, http.StatusOK, &document)
		if document.ID != id || document.Content != want {
			t.Fatalf("GET document: content of %d bytes, want %d", len(document.Content), len(want))
		}
		var list json.RawMessage
		expect(handler, http.MethodGet, "/api/documents", nil, http.StatusOK, &list)
		if !bytes.Contains(list, []byte(id)) {
			t.Fatal("the document list lost the document")
		}
		expect(handler, http.MethodGet, "/api/documents/"+id+"/history", nil, http.StatusOK, nil)
		expect(handler, http.MethodGet, "/api/documents/"+id+"/subscribers", nil, http.StatusOK, nil)
		expect(handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK, nil)
	}
	// Title edits rewrite the whole record. A request body is limited to
	// 16 MiB in every mode, so the content edit below shrinks the text.
	edit := func(handler http.Handler, input map[string]any, title, content string) {
		t.Helper()
		var updated domain.Document
		expect(handler, http.MethodPatch, "/api/documents/"+id, input, http.StatusOK, &updated)
		var document domain.Document
		expect(handler, http.MethodGet, "/api/documents/"+id, nil, http.StatusOK, &document)
		if document.Title != title || document.Content != content {
			t.Fatalf("edit not served back: %q, content of %d bytes, want %q and %d", document.Title, len(document.Content), title, len(content))
		}
	}

	// A budget too small for the document: the start falls back and the
	// document stays readable and editable.
	repo, handler = open("on", 256<<20)
	if repo.BoundedMetadataEnabled() {
		t.Log("a 256 MiB budget admitted the document; the fallback case is not exercised at this size")
	}
	routes(handler, text)
	edit(handler, map[string]any{"title": "Edited in fallback"}, "Edited in fallback", text)
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}

	// A budget that admits the document: bounded reads and edits.
	repo, handler = open("on", budget)
	defer repo.Close()
	if !repo.BoundedMetadataEnabled() {
		t.Fatal("the large document was not admitted")
	}
	routes(handler, text)
	edit(handler, map[string]any{"title": "Edited bounded"}, "Edited bounded", text)
	routes(handler, text)

	// The budget held by other work: an edit is refused with the retryable
	// 503 and writes nothing.
	restoreWait := store.SetMetadataWorkRetryWaitForTesting(200 * time.Millisecond)
	defer restoreWait()
	release, err := repo.HoldMetadataWorkForTesting(repo.MetadataWorkLimitForTesting() - repo.MetadataWorkUsedForTesting() - 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	code := call(handler, http.MethodPatch, "/api/documents/"+id, map[string]any{"title": "Refused"}, nil)
	release()
	if code != http.StatusServiceUnavailable {
		t.Fatalf("an edit under a held budget answered %d, want 503", code)
	}
	var document domain.Document
	expect(handler, http.MethodGet, "/api/documents/"+id, nil, http.StatusOK, &document)
	if document.Title != "Edited bounded" || document.Content != text {
		t.Fatal("a refused edit wrote")
	}

	// A content edit replaces the large text.
	content := text[:min(len(text), 8_000_000)] + " edited"
	edit(handler, map[string]any{"content": content}, "Edited bounded", content)
	routes(handler, content)
}
