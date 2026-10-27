package main

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

// The report: the model passed save_document's patch as one object instead of
// a list of edits. The call must fail with a tool error that names the
// expected shape — not complete while changing nothing — the document keeps
// its body and version, and the run's outcome says the agent's "done" summary
// claims an append the run did not make.
func TestLoopRunAppendWithAnObjectPatchIsAToolError(t *testing.T) {
	provider := &fakeLoopProvider{}
	_, handler := newReliableLoopServer(t, provider)
	document := loopTestDocument(t, handler, map[string]any{"title": "团队文档", "content": "Existing introduction"})
	before := requestJSON[domain.Document](t, handler, http.MethodGet, "/api/documents/"+document.ID, nil, http.StatusOK)
	provider.script(
		`tool:save_document {"id":"`+document.ID+`","patch":{"op":"append","text":"\n\n## 今日总结\n测试内容"}}`,
		`tool:finish_run {"status":"done","summary":"已将今日总结追加到团队文档。"}`,
	)
	loop, started := startTestRun(t, handler, "团队日报", "将今日总结追加到已有团队文档中。", nil)
	run := waitForRun(t, handler, loop.ID, started.ID)
	call := loopRunCall(run, "save_document", 0)
	if call.Status != "error" || !strings.Contains(call.Error, "Invalid arguments for save_document") || !strings.Contains(call.Error, `[{"op":"append","text":"..."}]`) {
		t.Fatalf("object patch call = %s %q", call.Status, call.Error)
	}
	if input := provider.lastInput(); !strings.Contains(input, "Invalid arguments for save_document") {
		t.Fatalf("the model was not told the arguments are invalid: %s", input)
	}
	after := requestJSON[domain.Document](t, handler, http.MethodGet, "/api/documents/"+document.ID, nil, http.StatusOK)
	if after.Content != "Existing introduction" || after.Version != before.Version {
		t.Fatalf("document after the invalid call = %q version %d (was %d)", after.Content, after.Version, before.Version)
	}
	if run.Status != "needs_review" || run.Produced["document"] != 0 {
		t.Fatalf("run = %s %s %q produced %v", run.Status, run.FailureReason, run.Error, run.Produced)
	}
	if !strings.Contains(run.Error, loopDoneClaimNote) {
		t.Fatalf("the outcome does not say the agent's summary claims work the run did not do: %q", run.Error)
	}
}

// save_document on an existing document that changes nothing does not bump
// its version: no field at all is an error, the same body is reported as
// unchanged. The /mcp endpoint refuses the object patch with the same hint.
func TestSaveDocumentThatChangesNothingKeepsTheVersion(t *testing.T) {
	f := newMCPContractFixture(t)
	created := mcpObject(t, f, "save_document", map[string]any{"title": "Team log", "content": "Existing introduction"})
	id := created["id"].(string)
	version := func() int64 {
		t.Helper()
		for _, document := range f.repository.Bootstrap().Documents {
			if document.ID == id {
				return document.Version
			}
		}
		t.Fatal("document not found")
		return 0
	}
	start := version()
	if message := mcpToolError(t, f, "save_document", map[string]any{"id": id}); !strings.Contains(message, "nothing to save") {
		t.Fatalf("save_document with only an id = %q", message)
	}
	if same := mcpObject(t, f, "save_document", map[string]any{"id": id, "content": "Existing introduction"}); same["unchanged"] != true {
		t.Fatalf("saving the same body = %v", same)
	}
	if message := mcpToolError(t, f, "save_document", map[string]any{"id": id, "patch": map[string]any{"op": "append", "text": "\n\nMore"}}); !strings.Contains(message, "Invalid arguments for save_document") || !strings.Contains(message, `[{"op":"append","text":"..."}]`) {
		t.Fatalf("object patch over /mcp = %q", message)
	}
	if got := version(); got != start {
		t.Fatalf("version %d after writes that changed nothing, want %d", got, start)
	}
}

// The web app explains the outcome note in every language.
func TestLoopDoneClaimNoteTranslated(t *testing.T) {
	labels := readWebSource(t, "components/loops/loop-run-labels.ts")
	if !strings.Contains(labels, strconv.Quote(loopDoneClaimNote)) {
		t.Errorf("loop-run-labels.ts does not read the note %q", loopDoneClaimNote)
	}
}
