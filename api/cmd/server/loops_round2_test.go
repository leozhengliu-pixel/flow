package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/websearch"
)

type fakeWebSearch struct {
	mu      sync.Mutex
	queries []string
	results []websearch.Result
}

func (f *fakeWebSearch) Name() string { return "fake" }

func (f *fakeWebSearch) Search(_ context.Context, query string, maxResults int) ([]websearch.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, fmt.Sprintf("%s|%d", query, maxResults))
	return f.results, nil
}

func TestLoopPublishedVersionsAndRestore(t *testing.T) {
	provider := &fakeLoopProvider{}
	srv, handler := newLoopTestServer(t, provider)
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	draft := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops", map[string]any{"level": "team", "teamId": bootstrap.Teams[0].ID, "templateId": "triage-agent"}, http.StatusCreated)
	if versions := requestJSON[[]domain.LoopVersion](t, handler, http.MethodGet, "/api/loops/"+draft.ID+"/versions", nil, http.StatusOK); len(versions) != 0 || draft.Version != 0 {
		t.Fatalf("draft versions = %v version=%d", versions, draft.Version)
	}
	requestJSON[any](t, handler, http.MethodPatch, "/api/loops/"+draft.ID, map[string]any{"instructions": "Draft edit"}, http.StatusOK)
	published := requestJSON[domain.Loop](t, handler, http.MethodPatch, "/api/loops/"+draft.ID, map[string]any{"status": "published"}, http.StatusOK)
	if published.Version != 1 || published.VersionID != draft.ID+"_v1" {
		t.Fatalf("published version = %d %s", published.Version, published.VersionID)
	}
	// Enable/disable and owner changes are not versioned.
	requestJSON[any](t, handler, http.MethodPatch, "/api/loops/"+draft.ID, map[string]any{"enabled": false}, http.StatusOK)
	changed := requestJSON[domain.Loop](t, handler, http.MethodPatch, "/api/loops/"+draft.ID, map[string]any{"instructions": "Route issues carefully.", "webSearch": true, "name": "Triage v2"}, http.StatusOK)
	if changed.Version != 2 {
		t.Fatalf("changed version = %d", changed.Version)
	}
	versions := requestJSON[[]domain.LoopVersion](t, handler, http.MethodGet, "/api/loops/"+draft.ID+"/versions", nil, http.StatusOK)
	if len(versions) != 2 || versions[0].Version != 2 || !versions[0].Current || versions[1].Current || strings.Join(versions[0].ChangeSummary, ",") != "name,instructions,webSearch" || strings.Join(versions[1].ChangeSummary, ",") != "published" {
		t.Fatalf("versions = %#v", versions)
	}
	if versions[1].Definition.Instructions != "Draft edit" || versions[1].PublishedBy.ID != bootstrap.Viewer.ID {
		t.Fatalf("v1 = %#v", versions[1])
	}
	one := requestJSON[domain.LoopVersion](t, handler, http.MethodGet, "/api/loops/"+draft.ID+"/versions/"+versions[1].ID, nil, http.StatusOK)
	if one.Version != 1 || one.Definition.Name != "Triage agent" {
		t.Fatalf("version 1 = %#v", one)
	}
	requestJSON[any](t, handler, http.MethodGet, "/api/loops/"+draft.ID+"/versions/nope", nil, http.StatusNotFound)

	restored := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops/"+draft.ID+"/versions/"+versions[1].ID+"/restore", nil, http.StatusOK)
	if restored.Version != 3 || restored.Instructions != "Draft edit" || restored.Name != "Triage agent" || restored.WebSearch || restored.Enabled {
		t.Fatalf("restored = %#v", restored)
	}
	versions = requestJSON[[]domain.LoopVersion](t, handler, http.MethodGet, "/api/loops/"+draft.ID+"/versions", nil, http.StatusOK)
	if len(versions) != 3 || versions[0].RestoredFromVersion != 1 || versions[0].ChangeSummary[0] != "restored" {
		t.Fatalf("after restore = %#v", versions[0])
	}
	other := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops", map[string]any{"level": "workspace"}, http.StatusCreated)
	requestJSON[any](t, handler, http.MethodPost, "/api/loops/"+other.ID+"/versions/"+versions[1].ID+"/restore", nil, http.StatusConflict)

	// Runs record the version they ran.
	requestJSON[any](t, handler, http.MethodPatch, "/api/loops/"+draft.ID, map[string]any{"enabled": true}, http.StatusOK)
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Needs triage", "teamId": bootstrap.Teams[0].ID}, http.StatusCreated)
	requestJSON[domain.LoopRun](t, handler, http.MethodPost, "/api/loops/"+draft.ID+"/runs", map[string]any{"entityId": issue.ID}, http.StatusAccepted)
	run := waitForLoopRun(t, handler, draft.ID)
	if run.Version != 3 || run.VersionID != draft.ID+"_v3" {
		t.Fatalf("run version = %d %s", run.Version, run.VersionID)
	}

	// Loops published before versions existed get a v1, stored on first run.
	legacy := domain.Loop{ID: "loop_legacy_v", Name: "Legacy", Status: "published", Level: "workspace", TriggerType: "schedule", TriggerConfig: map[string]any{"interval": 1, "unit": "day", "time": "10:00"}, Instructions: "Old.", TeamAccess: "allPublic", Enabled: true, OwnerID: bootstrap.Viewer.ID, Creator: bootstrap.Viewer, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := srv.store.MutateWorkspace(t.Context(), bootstrap.Workspace.URLKey, "loop.created", legacy.ID, nil, func(data *domain.Bootstrap) error {
		data.Loops = append(data.Loops, legacy)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	legacyVersions := requestJSON[[]domain.LoopVersion](t, handler, http.MethodGet, "/api/loops/"+legacy.ID+"/versions", nil, http.StatusOK)
	if len(legacyVersions) != 1 || legacyVersions[0].ID != legacy.ID+"_v1" || legacyVersions[0].Definition.Instructions != "Old." {
		t.Fatalf("legacy versions = %#v", legacyVersions)
	}
	if loop := requestJSON[domain.Loop](t, handler, http.MethodGet, "/api/loops/"+legacy.ID, nil, http.StatusOK); loop.Version != 1 {
		t.Fatalf("legacy loop version = %d", loop.Version)
	}
	requestJSON[any](t, handler, http.MethodPatch, "/api/loops/"+legacy.ID, map[string]any{"instructions": "New."}, http.StatusOK)
	legacyVersions = requestJSON[[]domain.LoopVersion](t, handler, http.MethodGet, "/api/loops/"+legacy.ID+"/versions", nil, http.StatusOK)
	if len(legacyVersions) != 2 || legacyVersions[1].Definition.Instructions != "Old." || legacyVersions[0].Definition.Instructions != "New." {
		t.Fatalf("legacy after edit = %#v", legacyVersions)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/loops/"+legacy.ID, nil, http.StatusNoContent)
	data, _ := srv.store.WorkspaceMetadata(bootstrap.Workspace.URLKey)
	for _, version := range data.LoopVersions {
		if version.LoopID == legacy.ID {
			t.Fatal("versions of a deleted loop were kept")
		}
	}
}

func TestLoopVersionHistoryIsBounded(t *testing.T) {
	data := &domain.Bootstrap{Viewer: domain.User{ID: "user_1"}}
	loop := domain.Loop{ID: "loop_1", Status: "published", Name: "A", TriggerType: "schedule"}
	recordLoopVersion(data, nil, &loop, time.Now(), 0)
	for index := 0; index < 60; index++ {
		before := loop
		loop.Name = fmt.Sprintf("Name %d", index)
		recordLoopVersion(data, &before, &loop, time.Now(), 0)
	}
	versions := loopVersionsFor(data, loop.ID)
	if len(versions) != loopVersionHistory || versions[0].Version != 61 || versions[len(versions)-1].Version != 12 || loop.Version != 61 {
		t.Fatalf("kept %d versions, newest %d oldest %d", len(versions), versions[0].Version, versions[len(versions)-1].Version)
	}
}

func TestLoopRunFeedback(t *testing.T) {
	provider := &fakeLoopProvider{replies: []string{"Done."}}
	_, handler := newLoopTestServer(t, provider)
	loop := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops", map[string]any{"name": "Weekly", "instructions": "Summarize.", "status": "published"}, http.StatusCreated)
	requestJSON[domain.LoopRun](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs", nil, http.StatusAccepted)
	run := waitForLoopRun(t, handler, loop.ID)
	if run.ViewerRating != nil || run.FeedbackCounts == nil || run.FeedbackCounts.Up != 0 {
		t.Fatalf("initial feedback = %#v %#v", run.ViewerRating, run.FeedbackCounts)
	}
	path := "/api/loops/" + loop.ID + "/runs/" + run.ID + "/feedback"
	rated := requestJSON[domain.LoopRun](t, handler, http.MethodPost, path, map[string]any{"rating": "up", "comment": "  Great  "}, http.StatusOK)
	if rated.ViewerRating == nil || *rated.ViewerRating != "up" || rated.ViewerComment != "Great" || rated.FeedbackCounts.Up != 1 {
		t.Fatalf("rated = %#v", rated)
	}
	rated = requestJSON[domain.LoopRun](t, handler, http.MethodPost, path, map[string]any{"rating": "down"}, http.StatusOK)
	if *rated.ViewerRating != "down" || rated.FeedbackCounts.Up != 0 || rated.FeedbackCounts.Down != 1 || len(rated.Feedback) != 1 {
		t.Fatalf("re-rated = %#v", rated)
	}
	fetched := requestJSON[domain.LoopRun](t, handler, http.MethodGet, "/api/loops/"+loop.ID+"/runs/"+run.ID, nil, http.StatusOK)
	if fetched.ViewerRating == nil || *fetched.ViewerRating != "down" {
		t.Fatalf("fetched rating = %#v", fetched.ViewerRating)
	}
	cleared := requestJSON[domain.LoopRun](t, handler, http.MethodPost, path, map[string]any{"rating": nil}, http.StatusOK)
	if cleared.ViewerRating != nil || cleared.FeedbackCounts.Down != 0 || len(cleared.Feedback) != 0 {
		t.Fatalf("cleared = %#v", cleared)
	}
	requestJSON[any](t, handler, http.MethodPost, path, map[string]any{"rating": "meh"}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs/missing/feedback", map[string]any{"rating": "up"}, http.StatusNotFound)
}

func TestLoopWebSearchTools(t *testing.T) {
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><head><title>Changelog</title></head><body><p>Flow 2.0 ships loops.</p></body></html>"))
	}))
	defer page.Close()
	provider := &fakeLoopProvider{replies: []string{
		`tool:web_search {"query":"flow changelog","maxResults":3}`,
		`tool:fetch_url {"url":"` + page.URL + `/changelog"}`,
		"Flow 2.0 ships loops ([changelog](" + page.URL + "/changelog)).",
	}}
	srv, handler := newLoopTestServer(t, provider)
	search := &fakeWebSearch{results: []websearch.Result{{Title: "Changelog", URL: page.URL + "/changelog", Snippet: "Flow 2.0"}}}
	srv.webSearch = search
	srv.webFetcher = &websearch.Fetcher{AllowAddr: func(addr netip.Addr) bool { return addr.Unmap().IsLoopback() }}
	config := requestJSON[map[string]any](t, handler, http.MethodGet, "/api/loop-config", nil, http.StatusOK)
	if config["webSearchAvailable"] != true || config["webSearchProvider"] != "fake" {
		t.Fatalf("loop config = %v", config)
	}
	loop := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops", map[string]any{"name": "Release watch", "instructions": "Check the changelog.", "status": "published", "webSearch": true}, http.StatusCreated)
	requestJSON[domain.LoopRun](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs", nil, http.StatusAccepted)
	run := waitForLoopRun(t, handler, loop.ID)
	if run.Status != "completed" || len(run.ToolCalls) != 2 || len(run.Notices) != 0 {
		t.Fatalf("run = %#v", run)
	}
	if run.ToolCalls[0].Label != "Searched the web" || run.ToolCalls[0].Args != "flow changelog" || run.ToolCalls[0].Status != "completed" {
		t.Fatalf("search call = %#v", run.ToolCalls[0])
	}
	if run.ToolCalls[1].Label != "Read web page" || run.ToolCalls[1].Status != "completed" {
		t.Fatalf("fetch call = %#v", run.ToolCalls[1])
	}
	provider.mu.Lock()
	tools, prompt, fetchInput := strings.Join(provider.tools[0], ","), provider.inputs[0], provider.inputs[2]
	provider.mu.Unlock()
	if !strings.Contains(tools, "web_search") || !strings.Contains(tools, "fetch_url") || !strings.Contains(prompt, "Cite every web source") {
		t.Fatalf("tools=%s prompt=%s", tools, prompt)
	}
	if !strings.Contains(fetchInput, "Flow 2.0 ships loops.") {
		t.Fatalf("fetched page not returned to the model: %s", fetchInput)
	}
	if len(search.queries) != 1 || search.queries[0] != "flow changelog|3" {
		t.Fatalf("queries = %v", search.queries)
	}

	// Loops without web search do not get the tools.
	provider.mu.Lock()
	provider.replies, provider.tools, provider.inputs = []string{"Done."}, nil, nil
	provider.mu.Unlock()
	plain := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops", map[string]any{"name": "Plain", "instructions": "Do it.", "status": "published"}, http.StatusCreated)
	requestJSON[domain.LoopRun](t, handler, http.MethodPost, "/api/loops/"+plain.ID+"/runs", nil, http.StatusAccepted)
	waitForLoopRun(t, handler, plain.ID)
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if strings.Contains(strings.Join(provider.tools[0], ","), "web_search") {
		t.Fatal("web tools offered to a loop without web search")
	}
}

func TestLoopWebSearchWithoutProviderAddsNotice(t *testing.T) {
	provider := &fakeLoopProvider{replies: []string{"Done."}}
	_, handler := newLoopTestServer(t, provider)
	if config := requestJSON[map[string]any](t, handler, http.MethodGet, "/api/loop-config", nil, http.StatusOK); config["webSearchAvailable"] != false || config["webSearchProvider"] != "" {
		t.Fatalf("config = %v", config)
	}
	loop := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops", map[string]any{"name": "Web", "instructions": "Search.", "status": "published", "webSearch": true}, http.StatusCreated)
	started := requestJSON[domain.LoopRun](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs", nil, http.StatusAccepted)
	run := waitForLoopRun(t, handler, loop.ID)
	if len(started.Notices) != 1 || len(run.Notices) != 1 || run.Notices[0] != loopWebSearchUnavailable {
		t.Fatalf("notices = %v / %v", started.Notices, run.Notices)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if strings.Contains(strings.Join(provider.tools[0], ","), "web_search") || !strings.Contains(provider.inputs[0], "no web search provider is configured") {
		t.Fatalf("tools=%v prompt=%s", provider.tools[0], provider.inputs[0])
	}
}

func TestAgentChatWebSearchFollowsWorkspaceSetting(t *testing.T) {
	provider := &fakeLoopProvider{}
	srv, handler := newLoopTestServer(t, provider)
	host := httptest.NewServer(handler)
	t.Cleanup(host.Close)
	srv.webSearch = &fakeWebSearch{results: []websearch.Result{{Title: "A", URL: "https://example.com/a", Snippet: "a"}}}
	provider.replies = []string{"Hello."}
	streamAgent(t, srv, host, "/api/agent/sessions/stream", map[string]any{"message": "Hi", "location": "page"}, nil)
	provider.mu.Lock()
	if strings.Contains(strings.Join(provider.tools[0], ","), "web_search") {
		provider.mu.Unlock()
		t.Fatal("web search offered while the workspace setting is off")
	}
	provider.replies, provider.tools, provider.inputs = []string{`tool:web_search {"query":"flow"}`, "Found it: https://example.com/a"}, nil, nil
	provider.mu.Unlock()
	updated := requestJSON[map[string]any](t, handler, http.MethodPatch, "/api/workspace/loop-settings", map[string]any{"agentWebSearch": true}, http.StatusOK)
	if updated["agentWebSearch"] != true {
		t.Fatalf("settings = %v", updated)
	}
	stream := streamAgent(t, srv, host, "/api/agent/sessions/stream", map[string]any{"message": "Search the web", "location": "page"}, nil)
	if strings.Contains(stream, "tool.approval_required") || !strings.Contains(stream, "example.com/a") {
		t.Fatalf("stream = %s", stream)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if !strings.Contains(strings.Join(provider.tools[0], ","), "web_search") || !strings.Contains(provider.inputs[0], "cite the URLs") {
		t.Fatalf("tools = %v", provider.tools[0])
	}
}

func TestLoopSettingsTrustedSources(t *testing.T) {
	_, handler := newLoopTestServer(t, &fakeLoopProvider{})
	config := requestJSON[map[string]any](t, handler, http.MethodGet, "/api/workspace/loop-settings", nil, http.StatusOK)
	if config["trustedSourcesMode"] != "none" || config["externalLoopTriggers"] != false {
		t.Fatalf("config = %v", config)
	}
	updated := requestJSON[map[string]any](t, handler, http.MethodPatch, "/api/workspace/loop-settings", map[string]any{"externalLoopTriggers": true, "trustedSourcesMode": "allowlist", "trustedSourcesAllowlist": []string{"integration:Slack", "integration:slack", "appUser:user_x"}}, http.StatusOK)
	if updated["trustedSourcesMode"] != "allowlist" || fmt.Sprint(updated["trustedSourcesAllowlist"]) != "[integration:slack appUser:user_x]" || updated["externalLoopTriggers"] != true {
		t.Fatalf("updated = %v", updated)
	}
	options := fmt.Sprint(updated["trustedSourceOptions"])
	if !strings.Contains(options, "integration:email") || !strings.Contains(options, "integration:slack") {
		t.Fatalf("options = %s", options)
	}
	requestJSON[any](t, handler, http.MethodPatch, "/api/workspace/loop-settings", map[string]any{"trustedSourcesMode": "everyone"}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodPatch, "/api/workspace/loop-settings", map[string]any{"trustedSourcesAllowlist": []string{"slack"}}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodPatch, "/api/workspace/preferences", map[string]any{"trustedSourcesMode": "everyone"}, http.StatusBadRequest)
	settings := requestJSON[domain.WorkspaceSettings](t, handler, http.MethodGet, "/api/workspace/preferences", nil, http.StatusOK)
	if settings.TrustedSourcesMode != "allowlist" || len(settings.TrustedSourcesAllowlist) != 2 || !settings.ExternalLoopTriggers {
		t.Fatalf("stored settings = %#v", settings)
	}
}

func TestLoopInstructionsDataResolvesMentions(t *testing.T) {
	provider := &fakeLoopProvider{replies: []string{"Done."}}
	_, handler := newLoopTestServer(t, provider)
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Import your data", "teamId": bootstrap.Teams[0].ID}, http.StatusCreated)
	document := map[string]any{"type": "doc", "content": []any{
		map[string]any{"type": "paragraph", "content": []any{
			map[string]any{"type": "text", "text": "Check "},
			map[string]any{"type": "mention", "attrs": map[string]any{"id": issue.ID, "label": issue.Identifier, "mentionType": "issue"}},
			map[string]any{"type": "text", "text": " and ping "},
			map[string]any{"type": "mention", "attrs": map[string]any{"id": bootstrap.Viewer.ID, "label": bootstrap.Viewer.Name, "mentionType": "user"}},
		}},
		map[string]any{"type": "bulletList", "content": []any{
			map[string]any{"type": "listItem", "content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": "Report blockers"}}}}},
		}},
	}}
	// Only the document: markdown is derived from it.
	loop := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops", map[string]any{"name": "Chips", "instructionsData": document, "status": "published"}, http.StatusCreated)
	if loop.InstructionsData == nil || !strings.Contains(loop.Instructions, "Check "+issue.Identifier+" and ping @") || !strings.Contains(loop.Instructions, "- Report blockers") {
		t.Fatalf("derived instructions = %q", loop.Instructions)
	}
	updated := requestJSON[domain.Loop](t, handler, http.MethodPatch, "/api/loops/"+loop.ID, map[string]any{"instructions": "Check the issue and ping the owner.", "instructionsData": document}, http.StatusOK)
	if updated.Instructions != "Check the issue and ping the owner." {
		t.Fatalf("markdown not kept: %q", updated.Instructions)
	}
	requestJSON[domain.LoopRun](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs", nil, http.StatusAccepted)
	waitForLoopRun(t, handler, loop.ID)
	provider.mu.Lock()
	prompt := provider.inputs[0]
	provider.mu.Unlock()
	for _, want := range []string{"Check the issue and ping the owner.", "Referenced in the instructions", fmt.Sprintf("Issue %s \\\"Import your data\\\" (id %s)", issue.Identifier, issue.ID), "(id " + bootstrap.Viewer.ID + ")"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q: %s", want, prompt)
		}
	}
	requestJSON[any](t, handler, http.MethodPatch, "/api/loops/"+loop.ID, map[string]any{"instructionsData": "text"}, http.StatusBadRequest)
	cleared := requestJSON[domain.Loop](t, handler, http.MethodPatch, "/api/loops/"+loop.ID, map[string]any{"instructionsData": nil}, http.StatusOK)
	if cleared.InstructionsData != nil || cleared.Instructions == "" {
		t.Fatalf("cleared = %#v", cleared)
	}
}

func uploadLoopFile(t *testing.T, handler http.Handler, name, contentType string, content []byte) (int, map[string]any) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := make(map[string][]string)
	header["Content-Disposition"] = []string{`form-data; name="file"; filename="` + name + `"`}
	header["Content-Type"] = []string{contentType}
	part, _ := writer.CreatePart(header)
	_, _ = part.Write(content)
	_ = writer.Close()
	request := httptest.NewRequest(http.MethodPost, "/api/loops/attachments", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	var result map[string]any
	_ = json.Unmarshal(recorder.Body.Bytes(), &result)
	return recorder.Code, result
}

func TestLoopPromptAttachmentsReachTheBuilder(t *testing.T) {
	provider := &fakeLoopProvider{}
	srv, handler := newLoopTestServer(t, provider)
	host := httptest.NewServer(handler)
	t.Cleanup(host.Close)
	status, text := uploadLoopFile(t, handler, "runbook.md", "text/markdown", []byte("# Runbook\nEscalate P1 bugs to on-call."))
	if status != http.StatusCreated || text["name"] != "runbook.md" || !strings.HasPrefix(fmt.Sprint(text["url"]), "/uploads/") {
		t.Fatalf("upload = %d %v", status, text)
	}
	png := []byte("\x89PNG\r\n\x1a\n0000fake-image")
	status, image := uploadLoopFile(t, handler, "screen.png", "image/png", png)
	if status != http.StatusCreated {
		t.Fatalf("image upload = %d %v", status, image)
	}
	requestJSON[any](t, handler, http.MethodPost, "/api/loops", map[string]any{"level": "workspace", "prompt": "x", "attachmentIds": []string{"loop_attachment_missing"}}, http.StatusBadRequest)
	draft := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops", map[string]any{"level": "workspace", "prompt": "Build an escalation loop from this runbook", "attachmentIds": []string{text["id"].(string), image["id"].(string)}}, http.StatusCreated)
	if len(draft.Attachments) != 2 || draft.Attachments[0].Name != "runbook.md" {
		t.Fatalf("draft attachments = %#v", draft.Attachments)
	}
	// An attachment can only be used once.
	requestJSON[any](t, handler, http.MethodPost, "/api/loops", map[string]any{"level": "workspace", "prompt": "again", "attachmentIds": []string{text["id"].(string)}}, http.StatusBadRequest)
	provider.replies = []string{"It remains a draft for your review; I didn't publish, enable, or run it."}
	streamAgent(t, srv, host, "/api/agent/sessions/stream", map[string]any{"message": "Build an escalation loop from this runbook", "location": "toolbar", "loopIds": []string{draft.ID}}, nil)
	provider.mu.Lock()
	defer provider.mu.Unlock()
	input := provider.inputs[0]
	if !strings.Contains(input, "Escalate P1 bugs to on-call.") || !strings.Contains(input, "input_image") || !strings.Contains(input, "data:image/png;base64,") {
		t.Fatalf("attachments missing from builder input: %s", input)
	}
	// Uploads are served to workspace members.
	request := httptest.NewRequest(http.MethodGet, fmt.Sprint(text["url"]), nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "Escalate") {
		t.Fatalf("serve upload = %d", recorder.Code)
	}
}

func TestProviderMessagesCarryImages(t *testing.T) {
	messages := []agentProviderMessage{{Role: "system", Content: "sys"}, {Role: "user", Content: "look", Images: []agentProviderImage{{MediaType: "image/png", Data: "QUJD"}}}}
	raw, _ := json.Marshal(chatMessages(messages))
	if !strings.Contains(string(raw), `"image_url":{"url":"data:image/png;base64,QUJD"}`) {
		t.Fatalf("chat = %s", raw)
	}
	_, anthropic := anthropicMessages(messages)
	raw, _ = json.Marshal(anthropic)
	if !strings.Contains(string(raw), `"media_type":"image/png"`) || !strings.Contains(string(raw), `"type":"base64"`) {
		t.Fatalf("anthropic = %s", raw)
	}
	_, responses := responsesInput(messages)
	raw, _ = json.Marshal(responses)
	if !strings.Contains(string(raw), `"type":"input_image"`) {
		t.Fatalf("responses = %s", raw)
	}
}
