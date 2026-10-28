package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"flow/api/internal/domain"
)

// annotatedMCP is a local MCP server with one tool its annotations mark read-only (read_notes), one marked as
// writing (post_note) and one without annotations (tidy_notes). It counts tools/call per tool.
type annotatedMCP struct {
	*httptest.Server
	mu    sync.Mutex
	calls map[string]int
}

func newAnnotatedMCP(t *testing.T) *annotatedMCP {
	t.Helper()
	server := &annotatedMCP{calls: map[string]int{}}
	server.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusOK)
			return
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&request) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var result any
		switch request.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "session-notes")
			result = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "notes", "version": "1"}}
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
			return
		case "tools/list":
			object := map[string]any{"type": "object"}
			result = map[string]any{"tools": []any{
				map[string]any{"name": "read_notes", "description": "Read today's notes", "inputSchema": object, "annotations": map[string]any{"readOnlyHint": true}},
				map[string]any{"name": "post_note", "description": "Post a note", "inputSchema": object, "annotations": map[string]any{"readOnlyHint": false, "destructiveHint": false}},
				map[string]any{"name": "tidy_notes", "description": "Tidy notes", "inputSchema": object},
			}}
		case "tools/call":
			server.mu.Lock()
			server.calls[request.Params.Name]++
			server.mu.Unlock()
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "notes: shipped the importer"}}}
		default:
			result = map[string]any{}
		}
		raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	}))
	t.Cleanup(server.Close)
	return server
}

func (server *annotatedMCP) called(tool string) int {
	server.mu.Lock()
	defer server.mu.Unlock()
	return server.calls[tool]
}

const replyOnlyInstructions = "请总结团队今天的工作。只回复总结文本即可，不要在任何事项、项目、文档或其他资源上发布评论或写入内容。"

// A reply-only loop runs a connector's read-only tools and refuses the ones that may write, before they reach the
// connector; a normal loop runs both.
func TestReplyOnlyLoopRefusesConnectorWrites(t *testing.T) {
	provider := &fakeLoopProvider{}
	srv, handler := newReliableLoopServer(t, provider)
	notes := newAnnotatedMCP(t)
	setTestConnectors(t, srv.store, applicationPolicy{ID: "notes", Kind: "mcp", Name: "Notes", URL: notes.URL, Status: "approved", Shared: true})
	read, write, unknown := connectorToolName("notes", "read_notes"), connectorToolName("notes", "post_note"), connectorToolName("notes", "tidy_notes")
	byName := func(run domain.LoopRun, name string) domain.LoopRunToolCall {
		for _, call := range run.ToolCalls {
			if call.Name == name {
				return call
			}
		}
		t.Fatalf("run has no %s call: %+v", name, run.ToolCalls)
		return domain.LoopRunToolCall{}
	}

	provider.script(
		"tool:"+read+" {}",
		"tool:"+write+` {"text":"summary"}`,
		"tool:"+unknown+" {}",
		`tool:finish_run {"status":"done","summary":"今天团队发布了导入器。"}`,
	)
	loop, started := startTestRun(t, handler, "Daily summary", replyOnlyInstructions, map[string]any{"connectorIds": []string{"notes"}})
	run := waitForRun(t, handler, loop.ID, started.ID)
	if run.Status != "completed" || !strings.Contains(run.Output, "今天团队发布了导入器。") {
		t.Fatalf("reply-only run %s (%s): output %q", run.Status, run.Error, run.Output)
	}
	if notes.called("read_notes") != 1 || notes.called("post_note") != 0 || notes.called("tidy_notes") != 0 {
		t.Fatalf("connector calls = %v, want only read_notes", notes.calls)
	}
	if call := byName(run, read); call.Status != "completed" {
		t.Fatalf("read-only connector tool: %+v", call)
	}
	if call := byName(run, write); call.Status != "blocked" || !strings.HasPrefix(call.Error, loopProhibitedCallPrefix) || !strings.Contains(call.Error, "post_note") || !strings.Contains(call.Error, "read-only tools") {
		t.Fatalf("write connector tool: %+v", call)
	}
	if call := byName(run, unknown); call.Status != "blocked" || !strings.Contains(call.Error, "does not say whether it changes data") {
		t.Fatalf("unannotated connector tool: %+v", call)
	}
	for kind, count := range run.Produced {
		if count != 0 {
			t.Fatalf("reply-only run produced %d %s", count, kind)
		}
	}
	if !slices.Contains(run.Notices, loopRefusedWriteNotice) {
		t.Fatalf("notices %v lack the refusal notice", run.Notices)
	}
	// The model reads the refusal as its tool result.
	if !strings.Contains(provider.lastInput(), "treated as a write and was not called") {
		t.Fatal("the model did not get the refusal as the tool result")
	}

	// A normal loop runs both connector tools as before.
	provider.script(
		"tool:"+read+" {}",
		"tool:"+write+` {"text":"summary"}`,
		`tool:finish_run {"status":"done","summary":"Posted the note."}`,
	)
	loop, started = startTestRun(t, handler, "Post notes", "Read today's notes and post a note with the summary.", map[string]any{"connectorIds": []string{"notes"}})
	run = waitForRun(t, handler, loop.ID, started.ID)
	if run.Status != "completed" || notes.called("read_notes") != 2 || notes.called("post_note") != 1 {
		t.Fatalf("normal run %s (%s): connector calls %v", run.Status, run.Error, notes.calls)
	}
	if call := byName(run, write); call.Status != "completed" {
		t.Fatalf("normal run write: %+v", call)
	}
}

// Discovery keeps each tool's readOnlyHint, and worker runs see it.
func TestConnectorDiscoveryKeepsReadOnlyHint(t *testing.T) {
	notes := newAnnotatedMCP(t)
	s := &server{authDisabled: true}
	s.agent.ToolsEnabled, s.agent.WriteTools = true, true
	tools, err := s.listConnectorTools(t.Context(), applicationPolicy{ID: "notes", Kind: "mcp", Name: "Notes", URL: notes.URL, Status: "approved", Shared: true})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][2]bool{}
	for _, tool := range tools {
		got[tool.RemoteName] = [2]bool{tool.ReadOnly, tool.Annotated}
	}
	want := map[string][2]bool{"read_notes": {true, true}, "post_note": {false, true}, "tidy_notes": {false, false}}
	for name, flags := range want {
		if got[name] != flags {
			t.Errorf("%s: readOnly, annotated = %v, want %v", name, got[name], flags)
		}
	}
}
