package llmproxy

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"flow/api/internal/config"
)

func decodeChat(t *testing.T, document map[string]json.RawMessage) map[string]any {
	t.Helper()
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func rawDocument(t *testing.T, body string) map[string]json.RawMessage {
	t.Helper()
	var document map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func carrier(text string) string {
	return reasoningCarrierPrefix + base64.StdEncoding.EncodeToString([]byte(text))
}

// TestResponsesToChatRequest covers the request shape Codex sends.
func TestResponsesToChatRequest(t *testing.T) {
	body := `{
		"model": "deepseek-flash", "instructions": "You are Codex.", "stream": true, "store": false,
		"include": ["reasoning.encrypted_content"], "prompt_cache_key": "k", "parallel_tool_calls": true,
		"tool_choice": "auto", "reasoning": {"effort": "low", "summary": "auto"}, "max_output_tokens": 500,
		"text": {"format": {"type": "json_schema", "name": "out", "schema": {"type": "object"}, "strict": true}},
		"tools": [
			{"type": "function", "name": "exec_command", "description": "Run", "strict": false, "parameters": {"type": "object", "properties": {"cmd": {"type": "string"}}}},
			{"type": "custom", "name": "apply_patch", "description": "Patch", "format": {"type": "grammar", "syntax": "lark", "definition": "start: x"}},
			{"type": "web_search", "external_web_access": true},
			{"type": "namespace", "name": "multi_agent_v1", "tools": [{"type": "function", "name": "close_agent"}]}
		],
		"input": [
			{"type": "message", "role": "developer", "content": [{"type": "input_text", "text": "dev rules"}]},
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "look"}, {"type": "input_image", "image_url": "data:image/png;base64,AAA"}]},
			{"type": "reasoning", "id": "rs_1", "summary": [{"type": "summary_text", "text": "summary only"}], "encrypted_content": "` + carrier("I should run ls") + `"},
			{"type": "function_call", "call_id": "call_1", "name": "exec_command", "arguments": "{\"cmd\":\"ls\"}"},
			{"type": "function_call", "call_id": "call_2", "name": "exec_command", "arguments": "{\"cmd\":\"pwd\"}"},
			{"type": "function_call_output", "call_id": "call_1", "output": "a.txt"},
			{"type": "function_call_output", "call_id": "call_2", "output": [{"type": "input_text", "text": "/repo"}]},
			{"type": "reasoning", "id": "rs_2", "content": [{"type": "reasoning_text", "text": "patch it"}]},
			{"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": "Patching."}]},
			{"type": "custom_tool_call", "call_id": "call_3", "name": "apply_patch", "input": "*** Begin Patch"},
			{"type": "custom_tool_call_output", "call_id": "call_3", "output": "Done"},
			{"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": "All set."}]},
			{"type": "message", "role": "user", "content": "thanks"}
		]
	}`
	chat, translation, err := responsesToChat(rawDocument(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if !translation.stream || !translation.custom["apply_patch"] || translation.custom["exec_command"] {
		t.Fatalf("translation state: %+v", translation)
	}
	got := decodeChat(t, chat)
	for _, dropped := range []string{"input", "instructions", "store", "include", "prompt_cache_key", "reasoning", "text", "max_output_tokens"} {
		if _, present := got[dropped]; present {
			t.Errorf("%s must not reach a chat upstream", dropped)
		}
	}
	if got["model"] != "deepseek-flash" || got["stream"] != true || got["max_tokens"] != float64(500) || got["reasoning_effort"] != "low" || got["tool_choice"] != "auto" || got["parallel_tool_calls"] != true {
		t.Fatalf("scalars: %v", got)
	}
	format, _ := got["response_format"].(map[string]any)
	if schema, _ := format["json_schema"].(map[string]any); format["type"] != "json_schema" || schema["name"] != "out" || schema["strict"] != true {
		t.Fatalf("response_format: %v", got["response_format"])
	}
	tools, _ := got["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("hosted tools and namespaces are dropped, got %v", tools)
	}
	patch := tools[1].(map[string]any)["function"].(map[string]any)
	if patch["name"] != "apply_patch" || !strings.Contains(patch["parameters"].(map[string]any)["properties"].(map[string]any)["input"].(map[string]any)["description"].(string), "start: x") {
		t.Fatalf("custom tool: %v", patch)
	}
	if _, strict := tools[0].(map[string]any)["function"].(map[string]any)["strict"]; strict {
		t.Fatal("strict is not forwarded")
	}

	messages, _ := got["messages"].([]any)
	want := []struct{ role, content, reasoning, toolCalls, toolCallID string }{
		{role: "system", content: "You are Codex."},
		{role: "system", content: "dev rules"},
		{role: "user", content: "<parts>"},
		{role: "assistant", content: "<nil>", reasoning: "I should run ls", toolCalls: "call_1,call_2"},
		{role: "tool", content: "a.txt", toolCallID: "call_1"},
		{role: "tool", content: "/repo", toolCallID: "call_2"},
		{role: "assistant", content: "Patching.", reasoning: "patch it", toolCalls: "call_3"},
		{role: "tool", content: "Done", toolCallID: "call_3"},
		{role: "assistant", content: "All set."},
		{role: "user", content: "thanks"},
	}
	if len(messages) != len(want) {
		t.Fatalf("messages: %d, want %d: %v", len(messages), len(want), messages)
	}
	for i, expected := range want {
		message := messages[i].(map[string]any)
		content := "<nil>"
		switch value := message["content"].(type) {
		case string:
			content = value
		case []any:
			content = "<parts>"
		}
		var ids []string
		calls, _ := message["tool_calls"].([]any)
		for _, call := range calls {
			ids = append(ids, call.(map[string]any)["id"].(string))
		}
		reasoning, _ := message["reasoning_content"].(string)
		toolCallID, _ := message["tool_call_id"].(string)
		if message["role"] != expected.role || content != expected.content || reasoning != expected.reasoning || strings.Join(ids, ",") != expected.toolCalls || toolCallID != expected.toolCallID {
			t.Errorf("message %d: %v, want %+v", i, message, expected)
		}
	}
	image := messages[2].(map[string]any)["content"].([]any)[1].(map[string]any)
	if image["type"] != "image_url" || image["image_url"].(map[string]any)["url"] != "data:image/png;base64,AAA" {
		t.Fatalf("image part: %v", image)
	}
	custom := messages[6].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)["function"].(map[string]any)
	if custom["arguments"] != `{"input":"*** Begin Patch"}` {
		t.Fatalf("custom call arguments: %v", custom)
	}
}

func TestResponsesToChatEdgeCases(t *testing.T) {
	t.Run("string input", func(t *testing.T) {
		chat, _, err := responsesToChat(rawDocument(t, `{"model":"m","input":"hi"}`))
		if err != nil {
			t.Fatal(err)
		}
		messages := decodeChat(t, chat)["messages"].([]any)
		if len(messages) != 1 || messages[0].(map[string]any)["content"] != "hi" {
			t.Fatalf("messages: %v", messages)
		}
	})
	t.Run("previous_response_id refused", func(t *testing.T) {
		if _, _, err := responsesToChat(rawDocument(t, `{"model":"m","input":"hi","previous_response_id":"resp_1"}`)); !errors.Is(err, errPreviousResponse) {
			t.Fatalf("err: %v", err)
		}
	})
	t.Run("bad input", func(t *testing.T) {
		if _, _, err := responsesToChat(rawDocument(t, `{"model":"m","input":{"x":1}}`)); err == nil {
			t.Fatal("expected an error")
		}
	})
	t.Run("thinking tool call without reasoning gets empty reasoning_content", func(t *testing.T) {
		body := `{"model":"m","reasoning":{"effort":"high"},"input":[{"type":"function_call","call_id":"c","name":"f","arguments":"{}"},{"type":"function_call_output","call_id":"c","output":"ok"}]}`
		chat, _, err := responsesToChat(rawDocument(t, body))
		if err != nil {
			t.Fatal(err)
		}
		assistant := decodeChat(t, chat)["messages"].([]any)[0].(map[string]any)
		if reasoning, present := assistant["reasoning_content"]; !present || reasoning != "" || assistant["content"] != nil {
			t.Fatalf("assistant: %v", assistant)
		}
	})
	t.Run("no reasoning_content without thinking", func(t *testing.T) {
		body := `{"model":"m","input":[{"type":"function_call","call_id":"c","name":"f","arguments":"{}"}],"tools":[{"type":"web_search"}],"tool_choice":"required"}`
		chat, _, err := responsesToChat(rawDocument(t, body))
		if err != nil {
			t.Fatal(err)
		}
		got := decodeChat(t, chat)
		if _, present := got["messages"].([]any)[0].(map[string]any)["reasoning_content"]; present {
			t.Fatal("reasoning_content only goes to thinking requests")
		}
		if _, present := got["tool_choice"]; present {
			t.Fatal("tool_choice is dropped when no tool survives")
		}
	})
	t.Run("named tool choice", func(t *testing.T) {
		chat, _, _ := responsesToChat(rawDocument(t, `{"model":"m","input":"x","tools":[{"type":"function","name":"f"}],"tool_choice":{"type":"function","name":"f"}}`))
		choice := decodeChat(t, chat)["tool_choice"].(map[string]any)
		if choice["function"].(map[string]any)["name"] != "f" {
			t.Fatalf("tool_choice: %v", choice)
		}
	})
}

// sseEvent is one parsed Responses SSE event.
type sseEvent struct {
	name string
	data map[string]any
}

func readEvents(t *testing.T, body io.Reader) []sseEvent {
	t.Helper()
	var events []sseEvent
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	var name string
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			var data map[string]any
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &data); err != nil {
				t.Fatalf("bad event data %q: %v", line, err)
			}
			if data["type"] != name {
				t.Fatalf("event %s carries type %v", name, data["type"])
			}
			events = append(events, sseEvent{name, data})
		}
	}
	return events
}

func chatWireHarness(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, call upstreamCall)) (*harness, *fakeUpstream) {
	t.Helper()
	upstream := newUpstream(t, handler)
	cfg := baseConfig()
	cfg.OpenAIBaseURL = upstream.URL + "/v1"
	cfg.OpenAIWireAPI = config.OpenAIWireAPIChat
	cfg.CodexModels = []string{"deepseek-flash"}
	cfg.Prices["deepseek-flash"] = config.ModelPrice{Input: 1, Output: 2, CacheRead: 0.1}
	caller := Caller{WorkspaceKey: "ws", SessionID: "sess-codex", SandboxID: "sbx", Harness: "codex", Model: "deepseek-flash"}
	return newHarness(t, cfg, map[string]Caller{sandboxTokenValue: caller}, Limits{}), upstream
}

func codexRequest(stream bool) map[string]any {
	return map[string]any{
		"model": "deepseek-flash", "stream": stream, "instructions": "rules", "reasoning": map[string]any{"effort": "low"},
		"tools": []any{
			map[string]any{"type": "function", "name": "exec_command", "parameters": map[string]any{"type": "object"}},
			map[string]any{"type": "custom", "name": "apply_patch"},
		},
		"input": []any{map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": secretPrompt}}}},
	}
}

// TestChatWireStreaming runs a Codex-style streamed Responses request through
// a chat-only upstream: reasoning, text and a tool call split across chunks
// come back as Responses events, and the chat usage chunk is metered.
func TestChatWireStreaming(t *testing.T) {
	h, upstream := chatWireHarness(t, func(w http.ResponseWriter, r *http.Request, call upstreamCall) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Content-Length", "99999")
		for _, chunk := range []string{
			`{"id":"c1","object":"chat.completion.chunk","created":1700000000,"model":"deepseek-flash","choices":[{"index":0,"delta":{"role":"assistant","content":null,"reasoning_content":"Let me "}}],"usage":null}`,
			`{"id":"c1","model":"deepseek-flash","choices":[{"index":0,"delta":{"reasoning_content":"look."}}],"usage":null}`,
			`{"id":"c1","model":"deepseek-flash","choices":[{"index":0,"delta":{"content":"Checking"}}],"usage":null}`,
			`{"id":"c1","model":"deepseek-flash","choices":[{"index":0,"delta":{"content":" files."}}],"usage":null}`,
			`{"id":"c1","model":"deepseek-flash","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"exec_command","arguments":""}}]}}],"usage":null}`,
			`{"id":"c1","model":"deepseek-flash","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"cmd\":"}}]}}],"usage":null}`,
			`{"id":"c1","model":"deepseek-flash","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_b","type":"function","function":{"name":"apply_patch","arguments":"{\"input\":\"*** Begin"}}]}}],"usage":null}`,
			`{"id":"c1","model":"deepseek-flash","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"ls\"}"}},{"index":1,"function":{"arguments":" Patch\"}"}}]}}],"usage":null}`,
			`{"id":"c1","model":"deepseek-flash","choices":[{"index":0,"delta":{"content":"","reasoning_content":null},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1000,"completion_tokens":50,"total_tokens":1050,"prompt_tokens_details":{"cached_tokens":600},"completion_tokens_details":{"reasoning_tokens":12},"prompt_cache_hit_tokens":600,"prompt_cache_miss_tokens":400}}`,
			`[DONE]`,
		} {
			sse(w, "", chunk)
		}
	})
	response := h.do(t, http.MethodPost, "/internal/llm-proxy/openai/v1/responses", codexRequest(true), bearer())
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") || response.Header.Get("Content-Length") != "" {
		t.Fatalf("status %d headers %v", response.StatusCode, response.Header)
	}
	events := readEvents(t, response.Body)
	response.Body.Close()

	call := upstream.recorded()[0]
	if call.Path != "/v1/chat/completions" || call.Authorization != "Bearer "+openAIKey {
		t.Fatalf("upstream call: %s %s", call.Path, call.Authorization)
	}
	if options, _ := call.Body["stream_options"].(map[string]any); options["include_usage"] != true {
		t.Fatalf("stream usage not requested: %v", call.Body)
	}
	if _, present := call.Body["input"]; present {
		t.Fatal("a Responses body reached the chat upstream")
	}

	var names []string
	var done []map[string]any
	var completed map[string]any
	for _, event := range events {
		names = append(names, event.name)
		switch event.name {
		case "response.output_item.done":
			done = append(done, event.data["item"].(map[string]any))
		case "response.completed":
			completed = event.data["response"].(map[string]any)
		}
	}
	if names[0] != "response.created" || names[len(names)-1] != "response.completed" {
		t.Fatalf("event order: %v", names)
	}
	for i, event := range events {
		if event.data["sequence_number"] != float64(i) {
			t.Fatalf("sequence numbers: %v", names)
		}
	}
	if len(done) != 4 {
		t.Fatalf("items: %v", done)
	}
	reasoning, message, function, custom := done[0], done[1], done[2], done[3]
	if reasoning["type"] != "reasoning" || reasoning["encrypted_content"] != carrier("Let me look.") {
		t.Fatalf("reasoning item: %v", reasoning)
	}
	if message["type"] != "message" || message["content"].([]any)[0].(map[string]any)["text"] != "Checking files." {
		t.Fatalf("message item: %v", message)
	}
	if function["type"] != "function_call" || function["call_id"] != "call_a" || function["name"] != "exec_command" || function["arguments"] != `{"cmd":"ls"}` {
		t.Fatalf("function call: %v", function)
	}
	if custom["type"] != "custom_tool_call" || custom["call_id"] != "call_b" || custom["input"] != "*** Begin Patch" {
		t.Fatalf("custom call: %v", custom)
	}
	usage := completed["usage"].(map[string]any)
	if usage["input_tokens"] != float64(1000) || usage["output_tokens"] != float64(50) || usage["input_tokens_details"].(map[string]any)["cached_tokens"] != float64(600) || usage["output_tokens_details"].(map[string]any)["reasoning_tokens"] != float64(12) {
		t.Fatalf("usage: %v", usage)
	}
	if output := completed["output"].([]any); len(output) != 4 || completed["status"] != "completed" || completed["model"] != "deepseek-flash" {
		t.Fatalf("completed: %v", completed)
	}

	record := waitRecords(t, h.meter, 1)[0]
	// 400 input × $1 + 50 output × $2 + 600 cached × $0.1 per million = 560 micro-USD.
	if record.InputTokens != 400 || record.CacheReadTokens != 600 || record.OutputTokens != 50 || record.CostMicros != 560 || record.Model != "deepseek-flash" {
		t.Fatalf("usage record: %+v", record)
	}
	assertLogsClean(t, h.logs.String())
	if !strings.Contains(h.logs.String(), `"endpoint":"responses->chat_completions"`) {
		t.Fatalf("the request log must show the translation: %s", h.logs.String())
	}
}

// TestChatWireRoundTrip feeds the reasoning item of one response back as input
// and checks the upstream gets it as reasoning_content.
func TestChatWireRoundTrip(t *testing.T) {
	h, upstream := chatWireHarness(t, func(w http.ResponseWriter, r *http.Request, call upstreamCall) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"c2","object":"chat.completion","model":"deepseek-flash","choices":[{"index":0,"message":{"role":"assistant","content":"Done.","reasoning_content":"Easy."},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":3,"prompt_cache_hit_tokens":4}}`)
	})
	body := codexRequest(false)
	body["input"] = []any{
		map[string]any{"type": "message", "role": "user", "content": "go"},
		map[string]any{"type": "reasoning", "id": "rs_1", "summary": []any{}, "encrypted_content": carrier("think first")},
		map[string]any{"type": "function_call", "call_id": "call_a", "name": "exec_command", "arguments": "{}"},
		map[string]any{"type": "function_call_output", "call_id": "call_a", "output": "ok"},
	}
	response := h.do(t, http.MethodPost, "/internal/llm-proxy/openai/v1/responses", body, bearer())
	raw := readAll(t, response)
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("status %d: %s", response.StatusCode, raw)
	}
	var object map[string]any
	if err := json.Unmarshal([]byte(raw), &object); err != nil {
		t.Fatal(err)
	}
	output := object["output"].([]any)
	if object["object"] != "response" || object["status"] != "completed" || len(output) != 2 || output[1].(map[string]any)["type"] != "message" {
		t.Fatalf("response object: %v", object)
	}
	if usage := object["usage"].(map[string]any); usage["input_tokens_details"].(map[string]any)["cached_tokens"] != float64(4) {
		t.Fatalf("usage: %v", usage)
	}
	messages := upstream.recorded()[0].Body["messages"].([]any)
	assistant := messages[2].(map[string]any)
	if assistant["reasoning_content"] != "think first" || assistant["tool_calls"] == nil {
		t.Fatalf("assistant message: %v", assistant)
	}
	if _, present := upstream.recorded()[0].Body["stream_options"]; present {
		t.Fatal("stream_options only for streamed requests")
	}
	if record := waitRecords(t, h.meter, 1)[0]; record.InputTokens != 6 || record.CacheReadTokens != 4 || record.OutputTokens != 3 {
		t.Fatalf("usage record: %+v", record)
	}
}

func TestChatWireEndings(t *testing.T) {
	cases := []struct {
		name   string
		chunks []string
		event  string
		check  func(t *testing.T, response map[string]any)
	}{
		{
			name:   "length",
			chunks: []string{`{"id":"c","model":"deepseek-flash","choices":[{"index":0,"delta":{"content":"partial"},"finish_reason":"length"}]}`, `[DONE]`},
			event:  "response.incomplete",
			check: func(t *testing.T, response map[string]any) {
				if response["incomplete_details"].(map[string]any)["reason"] != "max_output_tokens" {
					t.Fatalf("incomplete: %v", response)
				}
			},
		},
		{
			name:   "cut short",
			chunks: []string{`{"id":"c","model":"deepseek-flash","choices":[{"index":0,"delta":{"content":"par"}}]}`},
			event:  "response.failed",
		},
		{
			name:   "error chunk",
			chunks: []string{`{"error":{"message":"upstream overloaded","type":"server_error"}}`},
			event:  "response.failed",
			check: func(t *testing.T, response map[string]any) {
				if response["error"].(map[string]any)["message"] != "upstream overloaded" {
					t.Fatalf("failed: %v", response)
				}
			},
		},
		{
			name:   "finish without DONE",
			chunks: []string{`{"id":"c","model":"deepseek-flash","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`},
			event:  "response.completed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := chatWireHarness(t, func(w http.ResponseWriter, r *http.Request, call upstreamCall) {
				w.Header().Set("Content-Type", "text/event-stream")
				for _, chunk := range tc.chunks {
					sse(w, "", chunk)
				}
			})
			response := h.do(t, http.MethodPost, "/internal/llm-proxy/openai/v1/responses", codexRequest(true), bearer())
			events := readEvents(t, response.Body)
			response.Body.Close()
			last := events[len(events)-1]
			if last.name != tc.event {
				t.Fatalf("last event %s, want %s", last.name, tc.event)
			}
			if tc.check != nil {
				tc.check(t, last.data["response"].(map[string]any))
			}
		})
	}
}

// TestChatWireErrorsPassThrough: upstream errors keep their status and
// OpenAI-shaped body, and a refused previous_response_id never goes upstream.
func TestChatWireErrorsPassThrough(t *testing.T) {
	h, upstream := chatWireHarness(t, func(w http.ResponseWriter, r *http.Request, call upstreamCall) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = io.WriteString(w, `{"error":{"message":"Insufficient Balance","type":"unknown_error"}}`)
	})
	response := h.do(t, http.MethodPost, "/internal/llm-proxy/openai/v1/responses", codexRequest(true), bearer())
	if body := readAll(t, response); response.StatusCode != http.StatusPaymentRequired || !strings.Contains(body, "Insufficient Balance") {
		t.Fatalf("status %d: %s", response.StatusCode, body)
	}
	body := codexRequest(true)
	body["previous_response_id"] = "resp_1"
	response = h.do(t, http.MethodPost, "/internal/llm-proxy/openai/v1/responses", body, bearer())
	if raw := readAll(t, response); response.StatusCode != http.StatusBadRequest || !strings.Contains(raw, "previous_response_id") {
		t.Fatalf("status %d: %s", response.StatusCode, raw)
	}
	if calls := len(upstream.recorded()); calls != 1 {
		t.Fatalf("upstream calls: %d", calls)
	}
	if records := h.meter.recorded(); len(records) != 0 {
		t.Fatalf("errors are not metered: %v", records)
	}
}

// TestChatWireOnlyForOpenAIResponses: chat completions requests and the
// default wire API are passed through untouched.
func TestChatWireOnlyForOpenAIResponses(t *testing.T) {
	h, upstream := chatWireHarness(t, func(w http.ResponseWriter, r *http.Request, call upstreamCall) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"c","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hi"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	})
	response := h.do(t, http.MethodPost, "/internal/llm-proxy/openai/v1/chat/completions", map[string]any{"model": "deepseek-flash", "messages": []any{map[string]any{"role": "user", "content": "x"}}}, bearer())
	if body := readAll(t, response); !strings.Contains(body, `"chat.completion"`) {
		t.Fatalf("chat completions must pass through: %s", body)
	}
	if path := upstream.recorded()[0].Path; path != "/v1/chat/completions" {
		t.Fatalf("path %s", path)
	}
}
