package llmproxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/config"
	"flow/api/internal/domain"
)

const (
	sandboxTokenValue = "fcs_sandbox-token-value"
	anthropicKey      = "sk-ant-real-upstream-key"
	openAIKey         = "sk-openai-real-upstream-key"
	openSourceKey     = "oss-real-upstream-key"
	secretPrompt      = "TOP-SECRET-PROMPT-CONTENT"
)

type fakeAuth struct {
	callers map[string]Caller
	err     error
}

func (a fakeAuth) AuthenticateLLMCaller(_ context.Context, token string) (Caller, error) {
	if a.err != nil {
		return Caller{}, a.err
	}
	caller, ok := a.callers[token]
	if !ok {
		return Caller{}, ErrUnauthenticated
	}
	return caller, nil
}

type fakeMeter struct {
	mu       sync.Mutex
	spendErr error
	checks   int
	records  []domain.CodingUsageRecord
}

func (m *fakeMeter) CheckSpend(context.Context, Caller) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checks++
	return m.spendErr
}

func (m *fakeMeter) RecordUsage(_ context.Context, record domain.CodingUsageRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records = append(m.records, record)
	return nil
}

func (m *fakeMeter) recorded() []domain.CodingUsageRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]domain.CodingUsageRecord(nil), m.records...)
}

type fakeSwitcher struct {
	mu       sync.Mutex
	switches []string
}

func (s *fakeSwitcher) RecordModelSwitch(_ context.Context, caller Caller, from, to, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.switches = append(s.switches, caller.SessionID+":"+from+"->"+to+":"+reason)
	return nil
}

// upstreamCall is what a fake upstream saw.
type upstreamCall struct {
	Path          string
	Query         string
	Authorization string
	APIKey        string
	Body          map[string]any
	Header        http.Header
}

type fakeUpstream struct {
	*httptest.Server
	mu    sync.Mutex
	calls []upstreamCall
}

func newUpstream(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, call upstreamCall)) *fakeUpstream {
	t.Helper()
	upstream := &fakeUpstream{}
	upstream.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		call := upstreamCall{Path: r.URL.Path, Query: r.URL.RawQuery, Authorization: r.Header.Get("Authorization"), APIKey: r.Header.Get("x-api-key"), Header: r.Header.Clone()}
		_ = json.Unmarshal(raw, &call.Body)
		upstream.mu.Lock()
		upstream.calls = append(upstream.calls, call)
		upstream.mu.Unlock()
		handler(w, r, call)
	}))
	t.Cleanup(upstream.Close)
	return upstream
}

func (u *fakeUpstream) recorded() []upstreamCall {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]upstreamCall(nil), u.calls...)
}

type harness struct {
	proxy    *Proxy
	server   *httptest.Server
	meter    *fakeMeter
	switcher *fakeSwitcher
	logs     *syncBuffer
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func newHarness(t *testing.T, cfg config.CodingLLMConfig, callers map[string]Caller, limits Limits) *harness {
	t.Helper()
	logs := &syncBuffer{}
	h := &harness{meter: &fakeMeter{}, switcher: &fakeSwitcher{}, logs: logs}
	if cfg.Prices == nil {
		cfg.Prices = map[string]config.ModelPrice{}
	}
	h.proxy = &Proxy{Config: cfg, Auth: fakeAuth{callers: callers}, Meter: h.meter, Switcher: h.switcher, Limits: limits, Logger: slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))}
	mux := http.NewServeMux()
	mux.Handle(PathPrefix, h.proxy)
	h.server = httptest.NewServer(mux)
	t.Cleanup(h.server.Close)
	return h
}

func (h *harness) do(t *testing.T, method, path string, body any, headers map[string]string) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequest(method, h.server.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func bearer() map[string]string {
	return map[string]string{"Authorization": "Bearer " + sandboxTokenValue}
}

func readAll(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func claudeCaller() Caller {
	return Caller{WorkspaceKey: "ws", SessionID: "sess-claude", SandboxID: "sbx-1", UserID: "user-1", LoopID: "loop-1", Harness: "claudeCode", Model: "claude-sonnet-4-5"}
}

func baseConfig() config.CodingLLMConfig {
	return config.CodingLLMConfig{
		AnthropicAPIKey: anthropicKey, OpenAIAPIKey: openAIKey, OpenSourceAPIKey: openSourceKey,
		ClaudeCodeModels: []string{"claude-sonnet-4-5", "claude-opus-4-1", "claude-cheap-1"},
		CodexModels:      []string{"gpt-5-codex", "gpt-5"},
		OpenSourceModels: []string{"qwen3-coder"},
		Prices: map[string]config.ModelPrice{
			"claude-sonnet-4-5": {Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75},
			"claude-opus-4-1":   {Input: 15, Output: 75},
			"claude-cheap-1":    {Input: 1, Output: 5},
			"gpt-5-codex":       {Input: 1.25, Output: 10, CacheRead: 0.125},
			"gpt-5":             {Input: 1.25, Output: 10},
		},
		Timeout: 5 * time.Second,
	}
}

func sse(w http.ResponseWriter, event, data string) {
	if event != "" {
		fmt.Fprintf(w, "event: %s\n", event)
	}
	fmt.Fprintf(w, "data: %s\n\n", data)
	w.(http.Flusher).Flush()
}

// TestAnthropicStreamingPassThroughAndUsage checks that SSE events reach the
// client before the upstream finishes (no buffering), that the real key is
// injected and the sandbox token is not forwarded, and that message_start +
// message_delta usage is metered and priced.
func TestAnthropicStreamingPassThroughAndUsage(t *testing.T) {
	release := make(chan struct{})
	upstream := newUpstream(t, func(w http.ResponseWriter, r *http.Request, call upstreamCall) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Set-Cookie", "upstream=1")
		sse(w, "message_start", `{"type":"message_start","message":{"id":"msg_1","model":"claude-sonnet-4-5-20250929","usage":{"input_tokens":1000,"cache_creation_input_tokens":200,"cache_read_input_tokens":3000,"output_tokens":1}}}`)
		select {
		case <-release:
		case <-time.After(5 * time.Second):
			return
		}
		sse(w, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`)
		sse(w, "message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":500}}`)
		sse(w, "message_stop", `{"type":"message_stop"}`)
	})
	cfg := baseConfig()
	cfg.AnthropicBaseURL = upstream.URL
	h := newHarness(t, cfg, map[string]Caller{sandboxTokenValue: claudeCaller()}, Limits{})

	response := h.do(t, http.MethodPost, "/internal/llm-proxy/anthropic/v1/messages?beta=true", map[string]any{
		"model": "claude-sonnet-4-5", "stream": true, "max_tokens": 10, "messages": []any{map[string]any{"role": "user", "content": secretPrompt}},
	}, map[string]string{"x-api-key": sandboxTokenValue, "anthropic-version": "2023-06-01", "anthropic-beta": "prompt-caching", "Cookie": "flow_session=abc"})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", response.StatusCode, readAll(t, response))
	}
	if response.Header.Get("Set-Cookie") != "" {
		t.Fatal("upstream cookies must not reach the sandbox")
	}
	reader := bufio.NewReader(response.Body)
	first, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first, "message_start") {
		t.Fatalf("first line %q", first)
	}
	// The upstream is still blocked: the first event arrived unbuffered.
	close(release)
	rest, _ := io.ReadAll(reader)
	response.Body.Close()
	if !strings.Contains(string(rest), "message_stop") || !strings.Contains(string(rest), "hello") {
		t.Fatalf("stream body: %s", rest)
	}

	calls := upstream.recorded()
	if len(calls) != 1 {
		t.Fatalf("calls: %d", len(calls))
	}
	call := calls[0]
	if call.APIKey != anthropicKey || call.Authorization != "" {
		t.Fatalf("upstream auth: key=%q authorization=%q", call.APIKey, call.Authorization)
	}
	if call.Path != "/v1/messages" || call.Query != "beta=true" {
		t.Fatalf("upstream path %s?%s", call.Path, call.Query)
	}
	if call.Header.Get("Anthropic-Version") != "2023-06-01" || call.Header.Get("Anthropic-Beta") != "prompt-caching" {
		t.Fatalf("anthropic headers not forwarded: %v", call.Header)
	}
	if call.Header.Get("Cookie") != "" {
		t.Fatal("cookies must not be forwarded upstream")
	}

	records := waitRecords(t, h.meter, 1)
	record := records[0]
	if record.InputTokens != 1000 || record.OutputTokens != 500 || record.CacheReadTokens != 3000 || record.CacheWriteTokens != 200 {
		t.Fatalf("usage: %+v", record)
	}
	// 1000×3 + 500×15 + 3000×0.3 + 200×3.75 = 3000 + 7500 + 900 + 750 micro-USD.
	if record.CostMicros != 12150 || record.Model != "claude-sonnet-4-5" || record.Kind != "llm" {
		t.Fatalf("record: %+v", record)
	}
	if record.SessionID != "sess-claude" || record.SandboxID != "sbx-1" || record.UserID != "user-1" || record.LoopID != "loop-1" || record.WorkspaceKey != "ws" || len(record.ID) != 26 {
		t.Fatalf("attribution: %+v", record)
	}
	assertLogsClean(t, h.logs.String())
}

func waitRecords(t *testing.T, meter *fakeMeter, n int) []domain.CodingUsageRecord {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		records := meter.recorded()
		if len(records) >= n {
			return records
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected %d usage records, got %d", n, len(records))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func assertLogsClean(t *testing.T, logs string) {
	t.Helper()
	if logs == "" {
		t.Fatal("expected request logs")
	}
	for _, secret := range []string{secretPrompt, sandboxTokenValue, anthropicKey, openAIKey, openSourceKey} {
		if strings.Contains(logs, secret) {
			t.Fatalf("logs contain a secret or prompt (%q): %s", secret, logs)
		}
	}
}

func TestOpenAIResponsesStreamingUsage(t *testing.T) {
	upstream := newUpstream(t, func(w http.ResponseWriter, r *http.Request, call upstreamCall) {
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, "response.created", `{"type":"response.created","response":{"id":"resp_1","model":"gpt-5-codex"}}`)
		sse(w, "response.output_text.delta", `{"type":"response.output_text.delta","delta":"hi"}`)
		sse(w, "response.completed", `{"type":"response.completed","response":{"id":"resp_1","model":"gpt-5-codex","usage":{"input_tokens":2000,"input_tokens_details":{"cached_tokens":1500},"output_tokens":300,"output_tokens_details":{"reasoning_tokens":100},"total_tokens":2300}}}`)
	})
	cfg := baseConfig()
	cfg.OpenAIBaseURL = upstream.URL + "/v1"
	caller := Caller{WorkspaceKey: "ws", SessionID: "sess-codex", SandboxID: "sbx-2", UserID: "user-2", Harness: "codex", Model: "gpt-5-codex"}
	h := newHarness(t, cfg, map[string]Caller{sandboxTokenValue: caller}, Limits{})
	response := h.do(t, http.MethodPost, "/internal/llm-proxy/openai/v1/responses", map[string]any{"model": "gpt-5-codex", "stream": true, "input": secretPrompt}, bearer())
	body := readAll(t, response)
	if response.StatusCode != http.StatusOK || !strings.Contains(body, "response.completed") {
		t.Fatalf("status %d: %s", response.StatusCode, body)
	}
	call := upstream.recorded()[0]
	if call.Authorization != "Bearer "+openAIKey || call.APIKey != "" || call.Path != "/v1/responses" {
		t.Fatalf("upstream call: %+v", call)
	}
	record := waitRecords(t, h.meter, 1)[0]
	if record.InputTokens != 500 || record.CacheReadTokens != 1500 || record.OutputTokens != 300 {
		t.Fatalf("usage: %+v", record)
	}
	// 500×1.25 + 300×10 + 1500×0.125 = 625 + 3000 + 187.5 → 3813 (rounded).
	if record.CostMicros != 3813 {
		t.Fatalf("cost: %d", record.CostMicros)
	}
	assertLogsClean(t, h.logs.String())
}

func TestChatCompletionsForcesStreamUsage(t *testing.T) {
	upstream := newUpstream(t, func(w http.ResponseWriter, r *http.Request, call upstreamCall) {
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, "", `{"id":"c1","object":"chat.completion.chunk","model":"gpt-5","choices":[{"index":0,"delta":{"content":"hi"}}]}`)
		sse(w, "", `{"id":"c1","object":"chat.completion.chunk","model":"gpt-5","choices":[],"usage":{"prompt_tokens":120,"completion_tokens":30,"prompt_tokens_details":{"cached_tokens":20}}}`)
		sse(w, "", `[DONE]`)
	})
	cfg := baseConfig()
	cfg.OpenAIBaseURL = upstream.URL + "/v1"
	caller := Caller{WorkspaceKey: "ws", SessionID: "sess-chat", Harness: "codex", Model: "gpt-5"}
	h := newHarness(t, cfg, map[string]Caller{sandboxTokenValue: caller}, Limits{})
	response := h.do(t, http.MethodPost, "/internal/llm-proxy/openai/v1/chat/completions", map[string]any{
		"model": "gpt-5", "stream": true, "stream_options": map[string]any{"include_obfuscation": false}, "messages": []any{map[string]any{"role": "user", "content": secretPrompt}},
	}, bearer())
	body := readAll(t, response)
	if response.StatusCode != http.StatusOK || !strings.Contains(body, "[DONE]") {
		t.Fatalf("status %d: %s", response.StatusCode, body)
	}
	call := upstream.recorded()[0]
	options, _ := call.Body["stream_options"].(map[string]any)
	if options["include_usage"] != true || options["include_obfuscation"] != false {
		t.Fatalf("stream_options not forced: %v", call.Body["stream_options"])
	}
	if call.Path != "/v1/chat/completions" {
		t.Fatalf("path %s", call.Path)
	}
	record := waitRecords(t, h.meter, 1)[0]
	if record.InputTokens != 100 || record.CacheReadTokens != 20 || record.OutputTokens != 30 || record.CostMicros != 425 {
		t.Fatalf("usage: %+v", record)
	}
}

func TestOpenSourceNonStreamedUsageAndModels(t *testing.T) {
	upstream := newUpstream(t, func(w http.ResponseWriter, r *http.Request, call upstreamCall) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"c2","object":"chat.completion","model":"qwen3-coder","choices":[{"index":0,"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":40,"completion_tokens":8}}`)
	})
	cfg := baseConfig()
	cfg.OpenSourceBaseURL = upstream.URL + "/v1"
	cfg.Prices["qwen3-coder"] = config.ModelPrice{Input: 0.5, Output: 1}
	caller := Caller{WorkspaceKey: "ws", SessionID: "sess-oss", Harness: "openSource", AutoModel: true}
	h := newHarness(t, cfg, map[string]Caller{sandboxTokenValue: caller}, Limits{})

	response := h.do(t, http.MethodPost, "/internal/llm-proxy/opensource/v1/chat/completions", map[string]any{"model": "anything", "messages": []any{}}, bearer())
	if body := readAll(t, response); response.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", response.StatusCode, body)
	}
	call := upstream.recorded()[0]
	if call.Body["model"] != "qwen3-coder" || call.Authorization != "Bearer "+openSourceKey {
		t.Fatalf("upstream call: %+v", call)
	}
	if _, present := call.Body["stream_options"]; present {
		t.Fatal("stream_options must only be added to streamed requests")
	}
	record := waitRecords(t, h.meter, 1)[0]
	if record.InputTokens != 40 || record.OutputTokens != 8 || record.CostMicros != 28 {
		t.Fatalf("usage: %+v", record)
	}

	response = h.do(t, http.MethodGet, "/internal/llm-proxy/opensource/v1/models", nil, bearer())
	var list struct {
		Object string `json:"object"`
		Data   []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(readAll(t, response)), &list); err != nil || list.Object != "list" || len(list.Data) != 1 || list.Data[0].ID != "qwen3-coder" {
		t.Fatalf("models: %+v %v", list, err)
	}
	if len(upstream.recorded()) != 1 {
		t.Fatal("the model list is served by Flow, not the upstream")
	}
}

// The Pi harness calls the OpenAI-compatible (open source) upstream with
// its own model list, and nothing else.
func TestPiHarnessUsesOpenSourceUpstream(t *testing.T) {
	upstream := newUpstream(t, func(w http.ResponseWriter, r *http.Request, call upstreamCall) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"c3","object":"chat.completion","model":"glm-5","choices":[{"index":0,"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":4,"completion_tokens":1}}`)
	})
	cfg := baseConfig()
	cfg.OpenSourceBaseURL = upstream.URL + "/v1"
	cfg.PiModels = []string{"glm-5"}
	caller := Caller{WorkspaceKey: "ws", SessionID: "sess-pi", Harness: "pi", AutoModel: true}
	h := newHarness(t, cfg, map[string]Caller{sandboxTokenValue: caller}, Limits{})

	response := h.do(t, http.MethodPost, "/internal/llm-proxy/opensource/v1/chat/completions", map[string]any{"model": "glm-5", "messages": []any{}}, bearer())
	if body := readAll(t, response); response.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", response.StatusCode, body)
	}
	if call := upstream.recorded()[0]; call.Body["model"] != "glm-5" || call.Authorization != "Bearer "+openSourceKey {
		t.Fatalf("upstream call: %+v", call)
	}
	response = h.do(t, http.MethodPost, "/internal/llm-proxy/openai/v1/chat/completions", map[string]any{"model": "glm-5", "messages": []any{}}, bearer())
	if body := readAll(t, response); response.StatusCode == http.StatusOK {
		t.Fatalf("pi must not reach the OpenAI upstream: %s", body)
	}
	if len(upstream.recorded()) != 1 {
		t.Fatal("only the open source upstream may be called")
	}
}

func TestAuthFailures(t *testing.T) {
	upstream := newUpstream(t, func(w http.ResponseWriter, r *http.Request, call upstreamCall) {
		t.Error("upstream must not be called")
	})
	cfg := baseConfig()
	cfg.AnthropicBaseURL, cfg.OpenAIBaseURL = upstream.URL, upstream.URL
	h := newHarness(t, cfg, map[string]Caller{sandboxTokenValue: claudeCaller()}, Limits{})
	body := map[string]any{"model": "claude-sonnet-4-5", "messages": []any{}}
	cases := []struct {
		name    string
		path    string
		headers map[string]string
		status  int
	}{
		{"missing token", "/internal/llm-proxy/anthropic/v1/messages", nil, http.StatusUnauthorized},
		{"unknown token", "/internal/llm-proxy/anthropic/v1/messages", map[string]string{"x-api-key": "fcs_wrong"}, http.StatusUnauthorized},
		{"basic auth is not a token", "/internal/llm-proxy/anthropic/v1/messages", map[string]string{"Authorization": "Basic " + sandboxTokenValue}, http.StatusUnauthorized},
		{"harness cannot use another upstream", "/internal/llm-proxy/openai/v1/responses", bearer(), http.StatusForbidden},
		{"unknown route", "/internal/llm-proxy/anthropic/v1/complete", bearer(), http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response := h.do(t, http.MethodPost, tc.path, body, tc.headers)
			text := readAll(t, response)
			if response.StatusCode != tc.status {
				t.Fatalf("status %d, want %d: %s", response.StatusCode, tc.status, text)
			}
			var envelope struct {
				Error struct {
					Type string `json:"type"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(text), &envelope); err != nil || envelope.Error.Type == "" {
				t.Fatalf("error body: %s", text)
			}
		})
	}

	// A store outage is not reported as a bad token.
	h.proxy.Auth = fakeAuth{err: errors.New("database is down")}
	response := h.do(t, http.MethodPost, "/internal/llm-proxy/anthropic/v1/messages", body, bearer())
	if readAll(t, response); response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("store error status %d", response.StatusCode)
	}
	assertLogsClean(t, h.logs.String())
}

func TestUnconfiguredUpstream(t *testing.T) {
	cfg := baseConfig()
	cfg.AnthropicBaseURL = "http://127.0.0.1:1"
	cfg.AnthropicAPIKey = ""
	h := newHarness(t, cfg, map[string]Caller{sandboxTokenValue: claudeCaller()}, Limits{})
	response := h.do(t, http.MethodPost, "/internal/llm-proxy/anthropic/v1/messages", map[string]any{"model": "x"}, bearer())
	if readAll(t, response); response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status %d", response.StatusCode)
	}
}

func TestModelAllowlistRewrites(t *testing.T) {
	upstream := newUpstream(t, func(w http.ResponseWriter, r *http.Request, call upstreamCall) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"type":"message","model":%q,"usage":{"input_tokens":10,"output_tokens":2}}`, call.Body["model"])
	})
	cfg := baseConfig()
	cfg.AnthropicBaseURL = upstream.URL
	h := newHarness(t, cfg, map[string]Caller{sandboxTokenValue: claudeCaller()}, Limits{})
	cases := []struct {
		requested, upstreamModel string
	}{
		{"claude-sonnet-4-5", "claude-sonnet-4-5"},
		{"claude-opus-4-1", "claude-sonnet-4-5"},        // another catalog model: rewritten to the session's
		{"gpt-4o", "claude-sonnet-4-5"},                 // anything else: rewritten
		{"", "claude-sonnet-4-5"},                       // missing model: the session's
		{"claude-3-5-haiku-20241022", "claude-cheap-1"}, // background small model: cheapest priced
		{"claude-haiku-4-5", "claude-cheap-1"},          // any haiku
	}
	for i, tc := range cases {
		body := map[string]any{"max_tokens": 1, "messages": []any{}}
		if tc.requested != "" {
			body["model"] = tc.requested
		}
		response := h.do(t, http.MethodPost, "/internal/llm-proxy/anthropic/v1/messages", body, bearer())
		if text := readAll(t, response); response.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d: %s", tc.requested, response.StatusCode, text)
		}
		if got := upstream.recorded()[i].Body["model"]; got != tc.upstreamModel {
			t.Fatalf("%q was sent as %v, want %s", tc.requested, got, tc.upstreamModel)
		}
		if response.Header.Get("X-Flow-Model") != tc.upstreamModel {
			t.Fatalf("X-Flow-Model %q", response.Header.Get("X-Flow-Model"))
		}
	}
	records := waitRecords(t, h.meter, len(cases))
	if records[4].Model != "claude-cheap-1" || records[4].CostMicros != 20 {
		t.Fatalf("small model record: %+v", records[4])
	}

	// No model at all for the session: refused.
	h.proxy.Auth = fakeAuth{callers: map[string]Caller{sandboxTokenValue: {SessionID: "s", Harness: "openSource"}}}
	h.proxy.Config.OpenSourceBaseURL = upstream.URL
	h.proxy.Config.OpenSourceModels = nil
	response := h.do(t, http.MethodPost, "/internal/llm-proxy/opensource/v1/chat/completions", map[string]any{"model": "x"}, bearer())
	if readAll(t, response); response.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d", response.StatusCode)
	}
}

func TestSpendLimitDeniesBeforeUpstream(t *testing.T) {
	upstream := newUpstream(t, func(w http.ResponseWriter, r *http.Request, call upstreamCall) {
		t.Error("upstream must not be called over the limit")
	})
	cfg := baseConfig()
	cfg.AnthropicBaseURL = upstream.URL
	h := newHarness(t, cfg, map[string]Caller{sandboxTokenValue: claudeCaller()}, Limits{})
	h.meter.spendErr = fmt.Errorf("user limit: %w", ErrSpendLimit)
	response := h.do(t, http.MethodPost, "/internal/llm-proxy/anthropic/v1/messages", map[string]any{"model": "claude-sonnet-4-5"}, bearer())
	text := readAll(t, response)
	if response.StatusCode != http.StatusPaymentRequired || response.Header.Get("X-Flow-Error-Code") != "quotaExceeded" {
		t.Fatalf("status %d headers %v: %s", response.StatusCode, response.Header, text)
	}
	var envelope struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(text), &envelope); err != nil || envelope.Error.Type != "quota_exceeded" {
		t.Fatalf("body %s", text)
	}

	// A spend check that cannot run fails closed with a retryable status.
	h.meter.spendErr = errors.New("database is down")
	response = h.do(t, http.MethodPost, "/internal/llm-proxy/anthropic/v1/messages", map[string]any{"model": "claude-sonnet-4-5"}, bearer())
	if readAll(t, response); response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status %d", response.StatusCode)
	}
}

type codedErr struct{}

func (codedErr) Error() string                           { return "over" }
func (codedErr) CodingErrorCode() domain.CodingErrorCode { return domain.CodingErrQuotaExceeded }

func TestGuardedMeterCachesDecisions(t *testing.T) {
	now := time.Unix(1000, 0)
	calls := 0
	var result error
	meter := &GuardedMeter{Guard: SpendCheckerFunc(func(_ context.Context, ws, user, loop string) error {
		calls++
		if ws != "ws" || user != "u" || loop != "l" {
			t.Errorf("guard args %s %s %s", ws, user, loop)
		}
		return result
	}), Now: func() time.Time { return now }}
	caller := Caller{WorkspaceKey: "ws", UserID: "u", LoopID: "l"}
	if err := meter.CheckSpend(context.Background(), caller); err != nil || calls != 1 {
		t.Fatalf("first check: %v calls=%d", err, calls)
	}
	result = codedErr{}
	if err := meter.CheckSpend(context.Background(), caller); err != nil || calls != 1 {
		t.Fatalf("cached allow: %v calls=%d", err, calls)
	}
	now = now.Add(31 * time.Second)
	err := meter.CheckSpend(context.Background(), caller)
	if !errors.Is(err, ErrSpendLimit) || !IsSpendLimit(err) || calls != 2 {
		t.Fatalf("limit after expiry: %v calls=%d", err, calls)
	}
	if err := meter.CheckSpend(context.Background(), caller); !errors.Is(err, ErrSpendLimit) || calls != 2 {
		t.Fatalf("cached deny: %v calls=%d", err, calls)
	}
	// Transient failures are not cached.
	now = now.Add(31 * time.Second)
	result = errors.New("db down")
	for range 2 {
		if err := meter.CheckSpend(context.Background(), caller); err == nil || IsSpendLimit(err) {
			t.Fatalf("transient: %v", err)
		}
	}
	if calls != 4 {
		t.Fatalf("transient errors were cached: calls=%d", calls)
	}
	// No guard: spend is not enforced.
	if err := (&GuardedMeter{}).CheckSpend(context.Background(), caller); err != nil {
		t.Fatal(err)
	}
}

func TestRequestSizeLimit(t *testing.T) {
	upstream := newUpstream(t, func(w http.ResponseWriter, r *http.Request, call upstreamCall) {
		t.Error("oversized requests must not be forwarded")
	})
	cfg := baseConfig()
	cfg.AnthropicBaseURL = upstream.URL
	h := newHarness(t, cfg, map[string]Caller{sandboxTokenValue: claudeCaller()}, Limits{MaxBodyBytes: 1024})
	response := h.do(t, http.MethodPost, "/internal/llm-proxy/anthropic/v1/messages", map[string]any{"model": "claude-sonnet-4-5", "pad": strings.Repeat("x", 2048)}, bearer())
	if readAll(t, response); response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d", response.StatusCode)
	}
	response = h.do(t, http.MethodPost, "/internal/llm-proxy/anthropic/v1/messages", []any{"not", "an", "object"}, bearer())
	if readAll(t, response); response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d", response.StatusCode)
	}
}

func TestPerSessionRateLimit(t *testing.T) {
	upstream := newUpstream(t, func(w http.ResponseWriter, r *http.Request, call upstreamCall) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	})
	cfg := baseConfig()
	cfg.AnthropicBaseURL = upstream.URL
	other := claudeCaller()
	other.SessionID = "sess-other"
	h := newHarness(t, cfg, map[string]Caller{sandboxTokenValue: claudeCaller(), "fcs_other": other}, Limits{RequestsPerMinute: 1, Burst: 2})
	for i := range 2 {
		response := h.do(t, http.MethodPost, "/internal/llm-proxy/anthropic/v1/messages", map[string]any{"model": "claude-sonnet-4-5"}, bearer())
		if readAll(t, response); response.StatusCode != http.StatusOK {
			t.Fatalf("request %d status %d", i, response.StatusCode)
		}
	}
	response := h.do(t, http.MethodPost, "/internal/llm-proxy/anthropic/v1/messages", map[string]any{"model": "claude-sonnet-4-5"}, bearer())
	if readAll(t, response); response.StatusCode != http.StatusTooManyRequests || response.Header.Get("Retry-After") == "" {
		t.Fatalf("status %d retry-after %q", response.StatusCode, response.Header.Get("Retry-After"))
	}
	// Another session has its own budget.
	response = h.do(t, http.MethodPost, "/internal/llm-proxy/anthropic/v1/messages", map[string]any{"model": "claude-sonnet-4-5"}, map[string]string{"x-api-key": "fcs_other"})
	if readAll(t, response); response.StatusCode != http.StatusOK {
		t.Fatalf("other session status %d", response.StatusCode)
	}
}

func TestConcurrencyCap(t *testing.T) {
	limiter := newSessionLimiter(6000, 100, 1, time.Now)
	release, _, ok := limiter.acquire("s")
	if !ok {
		t.Fatal("first acquire")
	}
	if _, retry, ok := limiter.acquire("s"); ok || retry <= 0 {
		t.Fatal("second in-flight request must be refused")
	}
	release()
	release() // idempotent
	if _, _, ok := limiter.acquire("s"); !ok {
		t.Fatal("slot not released")
	}
}

func TestUpstreamTimeout(t *testing.T) {
	upstream := newUpstream(t, func(w http.ResponseWriter, r *http.Request, call upstreamCall) {
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	})
	cfg := baseConfig()
	cfg.AnthropicBaseURL = upstream.URL
	cfg.Timeout = 100 * time.Millisecond
	h := newHarness(t, cfg, map[string]Caller{sandboxTokenValue: claudeCaller()}, Limits{})
	response := h.do(t, http.MethodPost, "/internal/llm-proxy/anthropic/v1/messages", map[string]any{"model": "claude-sonnet-4-5"}, bearer())
	if readAll(t, response); response.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("status %d", response.StatusCode)
	}
}

func TestAutoFallbackOnMissingModel(t *testing.T) {
	upstream := newUpstream(t, func(w http.ResponseWriter, r *http.Request, call upstreamCall) {
		w.Header().Set("Content-Type", "application/json")
		if call.Body["model"] == "gpt-5-codex" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":{"message":"The model gpt-5-codex does not exist","type":"invalid_request_error","code":"model_not_found"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"object":"response","model":"gpt-5","usage":{"input_tokens":10,"output_tokens":1}}`)
	})
	cfg := baseConfig()
	cfg.OpenAIBaseURL = upstream.URL + "/v1"
	caller := Caller{WorkspaceKey: "ws", SessionID: "sess-auto", Harness: "codex", Model: "gpt-5-codex", AutoModel: true}
	h := newHarness(t, cfg, map[string]Caller{sandboxTokenValue: caller}, Limits{})
	response := h.do(t, http.MethodPost, "/internal/llm-proxy/openai/v1/responses", map[string]any{"model": "gpt-5-codex", "input": "x"}, bearer())
	if text := readAll(t, response); response.StatusCode != http.StatusOK || response.Header.Get("X-Flow-Model") != "gpt-5" {
		t.Fatalf("status %d model %q: %s", response.StatusCode, response.Header.Get("X-Flow-Model"), text)
	}
	if calls := upstream.recorded(); len(calls) != 2 || calls[1].Body["model"] != "gpt-5" {
		t.Fatalf("calls: %+v", calls)
	}
	if len(h.switcher.switches) != 1 || h.switcher.switches[0] != "sess-auto:gpt-5-codex->gpt-5:model_not_found" {
		t.Fatalf("switches: %v", h.switcher.switches)
	}
	// Later requests go straight to the fallback model.
	response = h.do(t, http.MethodPost, "/internal/llm-proxy/openai/v1/responses", map[string]any{"model": "gpt-5-codex", "input": "x"}, bearer())
	readAll(t, response)
	if calls := upstream.recorded(); len(calls) != 3 || calls[2].Body["model"] != "gpt-5" {
		t.Fatalf("calls after switch: %d", len(calls))
	}
	if records := waitRecords(t, h.meter, 2); records[0].Model != "gpt-5" {
		t.Fatalf("records: %+v", records)
	}
}

func TestAutoFallbackOnOverloadAndPinnedModelUnavailable(t *testing.T) {
	upstream := newUpstream(t, func(w http.ResponseWriter, r *http.Request, call upstreamCall) {
		w.Header().Set("Content-Type", "application/json")
		switch call.Body["model"] {
		case "claude-sonnet-4-5":
			w.WriteHeader(529)
			_, _ = io.WriteString(w, `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)
		case "claude-opus-4-1":
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"type":"error","error":{"type":"not_found_error","message":"model: claude-opus-4-1"}}`)
		default:
			_, _ = io.WriteString(w, `{"type":"message","usage":{"input_tokens":1,"output_tokens":1}}`)
		}
	})
	cfg := baseConfig()
	cfg.AnthropicBaseURL = upstream.URL
	auto := claudeCaller()
	auto.AutoModel = true
	pinned := claudeCaller()
	pinned.SessionID, pinned.Model = "sess-pinned", "claude-opus-4-1"
	h := newHarness(t, cfg, map[string]Caller{sandboxTokenValue: auto, "fcs_pinned": pinned}, Limits{})

	response := h.do(t, http.MethodPost, "/internal/llm-proxy/anthropic/v1/messages", map[string]any{"model": "claude-sonnet-4-5"}, bearer())
	readAll(t, response)
	// sonnet is overloaded -> opus (missing, but only one retry is allowed), so the 404 passes through.
	if response.StatusCode != http.StatusNotFound || response.Header.Get("X-Flow-Model") != "claude-opus-4-1" {
		t.Fatalf("status %d model %s", response.StatusCode, response.Header.Get("X-Flow-Model"))
	}
	// Overload is transient: the next request tries the session's model again.
	response = h.do(t, http.MethodPost, "/internal/llm-proxy/anthropic/v1/messages", map[string]any{"model": "claude-sonnet-4-5"}, bearer())
	readAll(t, response)
	if calls := upstream.recorded(); calls[2].Body["model"] != "claude-sonnet-4-5" {
		t.Fatalf("overload must not pin the fallback: %v", calls[2].Body["model"])
	}

	response = h.do(t, http.MethodPost, "/internal/llm-proxy/anthropic/v1/messages", map[string]any{"model": "claude-opus-4-1"}, map[string]string{"x-api-key": "fcs_pinned"})
	text := readAll(t, response)
	if response.StatusCode != http.StatusNotFound || response.Header.Get("X-Flow-Error-Code") != "modelUnavailable" || !strings.Contains(text, "not_found_error") {
		t.Fatalf("pinned: status %d code %q body %s", response.StatusCode, response.Header.Get("X-Flow-Error-Code"), text)
	}
	if len(upstream.recorded()) != 5 {
		t.Fatalf("pinned sessions must not retry: %d calls", len(upstream.recorded()))
	}
}

func TestCountTokensIsNotMetered(t *testing.T) {
	upstream := newUpstream(t, func(w http.ResponseWriter, r *http.Request, call upstreamCall) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"input_tokens":12345}`)
	})
	cfg := baseConfig()
	cfg.AnthropicBaseURL = upstream.URL
	h := newHarness(t, cfg, map[string]Caller{sandboxTokenValue: claudeCaller()}, Limits{})
	response := h.do(t, http.MethodPost, "/internal/llm-proxy/anthropic/v1/messages/count_tokens", map[string]any{"model": "claude-opus-4-1", "messages": []any{}}, bearer())
	if text := readAll(t, response); response.StatusCode != http.StatusOK || !strings.Contains(text, "12345") {
		t.Fatalf("status %d: %s", response.StatusCode, text)
	}
	if upstream.recorded()[0].Path != "/v1/messages/count_tokens" || upstream.recorded()[0].Body["model"] != "claude-sonnet-4-5" {
		t.Fatalf("call: %+v", upstream.recorded()[0])
	}
	time.Sleep(20 * time.Millisecond)
	if len(h.meter.recorded()) != 0 {
		t.Fatal("count_tokens must not be billed")
	}
}

func TestUpstreamErrorsPassThroughUnmetered(t *testing.T) {
	upstream := newUpstream(t, func(w http.ResponseWriter, r *http.Request, call upstreamCall) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"},"usage":{"input_tokens":5}}`)
	})
	cfg := baseConfig()
	cfg.AnthropicBaseURL = upstream.URL
	h := newHarness(t, cfg, map[string]Caller{sandboxTokenValue: claudeCaller()}, Limits{})
	response := h.do(t, http.MethodPost, "/internal/llm-proxy/anthropic/v1/messages", map[string]any{"model": "claude-sonnet-4-5"}, bearer())
	if text := readAll(t, response); response.StatusCode != http.StatusTooManyRequests || !strings.Contains(text, "rate_limit_error") {
		t.Fatalf("status %d: %s", response.StatusCode, text)
	}
	time.Sleep(20 * time.Millisecond)
	if len(h.meter.recorded()) != 0 {
		t.Fatal("error answers are not billed")
	}
}

func TestUnpricedModelRecordedAtZero(t *testing.T) {
	upstream := newUpstream(t, func(w http.ResponseWriter, r *http.Request, call upstreamCall) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"type":"message","usage":{"input_tokens":100,"output_tokens":10}}`)
	})
	cfg := baseConfig()
	cfg.AnthropicBaseURL = upstream.URL
	cfg.Prices = nil
	h := newHarness(t, cfg, map[string]Caller{sandboxTokenValue: claudeCaller()}, Limits{})
	response := h.do(t, http.MethodPost, "/internal/llm-proxy/anthropic/v1/messages", map[string]any{"model": "claude-sonnet-4-5"}, bearer())
	readAll(t, response)
	record := waitRecords(t, h.meter, 1)[0]
	if record.CostMicros != 0 || record.InputTokens != 100 {
		t.Fatalf("record: %+v", record)
	}
	if !strings.Contains(h.logs.String(), "no configured price") {
		t.Fatal("expected an unpriced warning")
	}
}
