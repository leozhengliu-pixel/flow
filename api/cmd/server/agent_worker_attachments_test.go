package main

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

// Files sent with a chat message reach a worker reply as they reach an in-process one: the image as an image input
// (its bytes read when the run starts, never kept with the run), every other file as the note the model reads it
// by, and read_file served through /mcp for the conversation's own files only; the finished call is labelled as
// in-process.
func TestWorkerChatAttachmentsReachTheWorker(t *testing.T) {
	f := newWorkerChatFixture(t)
	f.s.uploadPath = t.TempDir()
	png := base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\n0000fake-screenshot"))
	body, _ := json.Marshal(map[string]any{"message": "What is wrong here?", "location": "page", "attachments": []map[string]any{
		{"name": "screen.png", "contentType": "image/png", "size": 26, "content": "data:image/png;base64," + png},
		{"name": "notes.md", "contentType": "text/markdown", "size": 30, "content": "# Notes\nThe header overlaps."},
		{"name": "spec.pdf", "contentType": "application/pdf", "size": 900},
	}})
	events := f.stream("/api/agent/sessions/stream", string(body))
	sessionID := nextEvent(t, events, "session.started").Session.ID
	run := f.mustClaim(t)

	stored, found, err := f.s.store.AgentRunKey(t.Context(), run.ID, workerChatContextKey)
	if err != nil || !found || strings.Contains(string(stored), png) {
		t.Fatalf("the run keeps the image bytes (%v %v): %.300s", found, err, stored)
	}
	payload := f.payload(t, run)
	last := payload.Context.Messages[len(payload.Context.Messages)-1]
	sent := f.reply(t, sessionID)
	session := apiCall[domain.AgentSession](t, f.api.URL, "GET", "/api/agent/sessions/"+sessionID, 200)
	notes := session.Messages[0].Attachments[1]
	if len(last.Images) != 1 || last.Images[0].Data != png || last.Images[0].MediaType != "image/png" || last.Images[0].Source != "" {
		t.Fatalf("payload images = %+v", last.Images)
	}
	for _, want := range []string{"What is wrong here?", "Attached image screen.png (shown below).", "Attached file notes.md (text/markdown, 28 bytes) — id: " + notes.URL + ". Use read_file to read it.", "Attached file spec.pdf (application/pdf, 900 bytes); its contents were not kept."} {
		if !strings.Contains(last.Content, want) {
			t.Fatalf("payload message lacks %q: %s", want, last.Content)
		}
	}
	if strings.Contains(last.Content, "The header overlaps.") {
		t.Fatalf("a text file was inlined: %s", last.Content)
	}

	token := f.token(t, run)
	tools := listedRunTools(t, f.handler, token)
	if tools[readFileTool] == nil || tools[transcribeMediaTool] != nil {
		t.Fatalf("session tools listed: read_file %v, transcribe_media %v", tools[readFileTool] != nil, tools[transcribeMediaTool] != nil)
	}
	result := callRunTool(t, f.handler, token, readFileTool, map[string]any{"file": notes.URL}, nil)
	if result["isError"] == true || !strings.Contains(runToolText(result), "The header overlaps.") {
		t.Fatalf("read_file = %v", result)
	}
	if other := callRunTool(t, f.handler, token, readFileTool, map[string]any{"file": "/uploads/agent_attachment_other_0_x.md"}, nil); other["isError"] != true {
		t.Fatalf("read_file opened a file of another conversation: %v", other)
	}

	f.post(t, run, map[string]any{"seq": 1, "type": "action", "callId": "c1", "content": map[string]any{"action": readFileTool, "parameter": map[string]any{"file": notes.URL}}})
	f.post(t, run, map[string]any{"seq": 2, "type": "action.result", "callId": "c1", "content": map[string]any{"action": readFileTool, "parameter": map[string]any{"file": notes.URL}, "result": runToolText(result)}})
	f.post(t, run, map[string]any{"seq": 3, "type": "response", "body": "The header overlaps the screenshot."})
	f.complete(t, run, "done")
	drain(t, events)
	final := f.reply(t, sessionID)
	call := toolPart(final.Parts, "c1")
	if call == nil || call.ToolCall.Title != "Read file notes.md" || call.ToolCall.Status != "completed" || final.Content != "The header overlaps the screenshot." {
		t.Fatalf("finished reply = %+v (sent %s)", final.Parts, sent.ID)
	}

	// A chat without files is offered no file tools.
	plain := f.stream("/api/agent/sessions/stream", `{"message":"Hello","location":"page"}`)
	nextEvent(t, plain, "session.started")
	other := f.mustClaim(t)
	if tools := listedRunTools(t, f.handler, f.token(t, other)); tools[readFileTool] != nil {
		t.Fatal("read_file is offered without files")
	}
	f.post(t, other, map[string]any{"seq": 1, "type": "response", "body": "Hi."})
	f.complete(t, other, "done")
	drain(t, plain)
}
