package main

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/websearch"
)

// The web on the worker, as in-process: a chat run is offered fetch_url (and web_search with a search provider)
// through /mcp while the workspace lets Flow Agent use the web, runs them through Flow's SSRF-safe fetcher and
// search provider under the reply's budget, and loses them when an admin turns web access off. Without a search
// provider the payload asks the worker for the provider's hosted search, as the in-process turn adds it.
func TestWorkerRunsUseTheWebAsInProcess(t *testing.T) {
	f := newWorkerChatFixture(t)
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><head><title>Changelog</title></head><body><p>Flow 2.0 ships loops.</p></body></html>"))
	}))
	defer page.Close()
	f.s.webFetcher = &websearch.Fetcher{AllowAddr: func(addr netip.Addr) bool { return addr.Unmap().IsLoopback() }}
	f.s.agent.NativeWebSearch = true

	events := f.stream("/api/agent/sessions/stream", `{"message":"What does the changelog say?","location":"page"}`)
	nextEvent(t, events, "session.started")
	run := f.mustClaim(t)
	if chat := f.payload(t, run).Chat; !chat.HostedWebSearch {
		t.Fatalf("without a search provider the run is not asked to use hosted search: %+v", chat)
	}
	token := f.token(t, run)
	tools := listedRunTools(t, f.handler, token)
	if tools[fetchURLToolName] == nil || tools[webSearchToolName] != nil {
		t.Fatalf("web tools without a provider: fetch_url %v web_search %v", tools[fetchURLToolName] != nil, tools[webSearchToolName] != nil)
	}
	fetched := callRunTool(t, f.handler, token, fetchURLToolName, map[string]any{"url": page.URL + "/changelog"}, nil)
	if fetched["isError"] == true || !strings.Contains(runToolText(fetched), "Flow 2.0 ships loops.") {
		t.Fatalf("fetch_url = %v", fetched)
	}
	f.post(t, run, map[string]any{"seq": 1, "type": "response", "body": "Flow 2.0 ships loops."})
	f.complete(t, run, "done")
	drain(t, events)

	// A search provider: web_search through it, and no hosted search.
	f.s.webSearch = &fakeWebSearch{results: []websearch.Result{{Title: "Changelog", URL: page.URL + "/changelog", Snippet: "Flow 2.0"}}}
	searching := f.stream("/api/agent/sessions/stream", `{"message":"Search the web for the Flow changelog","location":"page"}`)
	nextEvent(t, searching, "session.started")
	second := f.mustClaim(t)
	if chat := f.payload(t, second).Chat; chat.HostedWebSearch {
		t.Fatal("hosted search asked for although a search provider is configured")
	}
	secondToken := f.token(t, second)
	if result := callRunTool(t, f.handler, secondToken, webSearchToolName, map[string]any{"query": "flow changelog"}, nil); result["isError"] == true || !strings.Contains(runToolText(result), "Changelog") {
		t.Fatalf("web_search = %v", result)
	}
	// An admin turns web access off: the tools are gone at once.
	if err := mutateTestWorkspace(t, f.s.store, func(data *domain.Bootstrap) { off := false; data.WorkspaceSettings.AgentWebAccess = &off }); err != nil {
		t.Fatal(err)
	}
	if tools := listedRunTools(t, f.handler, secondToken); tools[fetchURLToolName] != nil || tools[webSearchToolName] != nil {
		t.Fatal("web tools are still offered after web access was turned off")
	}
	if result := callRunTool(t, f.handler, secondToken, fetchURLToolName, map[string]any{"url": page.URL}, nil); result["isError"] != true {
		t.Fatalf("fetch_url after web access was turned off = %v", result)
	}
	f.post(t, second, map[string]any{"seq": 1, "type": "response", "body": "Done."})
	f.complete(t, second, "done")
	drain(t, searching)
}
