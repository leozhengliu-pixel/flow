package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	appconfig "flow/api/internal/config"
)

func TestAgentProviderStreamingProtocols(t *testing.T) {
	tests := []struct {
		name       string
		protocol   string
		path       string
		stream     string
		assertBody func(*testing.T, map[string]any, http.Header)
	}{
		{
			name: "OpenAI Responses", protocol: "openai-responses", path: "/responses",
			stream: "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n" +
				"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Hello \"}\n\n" +
				"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"world\"}\n\n" +
				"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"item_1\",\"call_id\":\"call_1\",\"type\":\"function_call\",\"name\":\"list_issues\",\"arguments\":\"\"}}\n\n" +
				"event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"item_id\":\"item_1\",\"delta\":\"{\\\"query\\\":\\\"bug\\\"}\"}\n\n" +
				"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n",
			assertBody: func(t *testing.T, body map[string]any, header http.Header) {
				if body["stream"] != true || body["instructions"] != "System" || header.Get("Authorization") != "Bearer secret" {
					t.Fatalf("Responses request body=%#v headers=%v", body, header)
				}
			},
		},
		{
			name: "Anthropic Messages", protocol: "anthropic-messages", path: "/messages",
			stream: "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\"}}\n\n" +
				"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
				"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello world\"}}\n\n" +
				"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"tool_1\",\"name\":\"list_issues\",\"input\":{}}}\n\n" +
				"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"query\\\":\\\"bug\\\"}\"}}\n\n" +
				"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\n" +
				"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
			assertBody: func(t *testing.T, body map[string]any, header http.Header) {
				if body["stream"] != true || body["system"] != "System" || header.Get("x-api-key") != "secret" || header.Get("anthropic-version") != "2023-06-01" {
					t.Fatalf("Anthropic request body=%#v headers=%v", body, header)
				}
			},
		},
		{
			name: "Chat Completions", protocol: "openai-chat-completions", path: "/chat/completions",
			stream: "data: {\"choices\":[{\"delta\":{\"content\":\"Hello \"},\"finish_reason\":null}]}\n\n" +
				"data: {\"choices\":[{\"delta\":{\"content\":\"world\",\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"function\":{\"name\":\"list_issues\",\"arguments\":\"{\\\"query\\\":\\\"bug\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n" +
				"data: [DONE]\n\n",
			assertBody: func(t *testing.T, body map[string]any, header http.Header) {
				if body["stream"] != true || header.Get("Authorization") != "Bearer secret" {
					t.Fatalf("Chat request body=%#v headers=%v", body, header)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != test.path {
					t.Errorf("path=%q want %q", r.URL.Path, test.path)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				test.assertBody(t, body, r.Header)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte(test.stream))
			}))
			defer provider.Close()
			s := &server{agent: appconfig.AgentConfig{Enabled: true, Protocol: test.protocol, BaseURL: provider.URL, APIKey: "secret", Model: "model", MaxOutputTokens: 100, AnthropicVersion: "2023-06-01"}, agentClient: provider.Client()}
			events := []agentProviderEvent{}
			turn, err := s.requestAgentTurn(context.Background(), []agentProviderMessage{{Role: "system", Content: "System"}, {Role: "user", Content: "Hello"}}, func(event agentProviderEvent) error {
				events = append(events, event)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if turn.Text != "Hello world" || len(turn.ToolCalls) != 1 || turn.ToolCalls[0].Name != "list_issues" || !strings.Contains(string(turn.ToolCalls[0].Arguments), "bug") {
				t.Fatalf("turn=%#v", turn)
			}
			var arguments map[string]any
			if err := json.Unmarshal(turn.ToolCalls[0].Arguments, &arguments); err != nil || arguments["query"] != "bug" {
				t.Fatalf("tool arguments=%s err=%v", turn.ToolCalls[0].Arguments, err)
			}
			if len(events) < 2 {
				t.Fatalf("events=%#v", events)
			}
		})
	}
}

func TestAgentProviderNonStreamingFallbacks(t *testing.T) {
	tests := []struct {
		protocol string
		path     string
		body     string
	}{
		{"openai-responses", "/responses", `{"id":"resp","output":[{"type":"message","content":[{"type":"output_text","text":"response text"}]}]}`},
		{"openai-chat-completions", "/chat/completions", `{"choices":[{"finish_reason":"stop","message":{"content":"response text"}}]}`},
		{"anthropic-messages", "/messages", `{"id":"msg","stop_reason":"end_turn","content":[{"type":"text","text":"response text"}]}`},
	}
	for _, test := range tests {
		t.Run(test.protocol, func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != test.path {
					t.Errorf("path=%q", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(test.body))
			}))
			defer provider.Close()
			s := &server{agent: appconfig.AgentConfig{Protocol: test.protocol, BaseURL: provider.URL, Model: "model", MaxOutputTokens: 100}, agentClient: provider.Client()}
			turn, err := s.requestAgentTurn(context.Background(), []agentProviderMessage{{Role: "user", Content: "hello"}}, nil)
			if err != nil || turn.Text != "response text" {
				t.Fatalf("turn=%#v err=%v", turn, err)
			}
		})
	}
}

func TestAgentToolInventoryRespectsWritePolicy(t *testing.T) {
	s := &server{agent: appconfig.AgentConfig{ToolsEnabled: true}}
	readTools, err := s.agentToolDefinitions()
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range readTools {
		if tool.Access == "write" {
			t.Fatalf("write tool exposed by default: %s", tool.Name)
		}
	}
	if len(readTools) == 0 {
		t.Fatal("read tool inventory is empty")
	}
	s.agent.WriteTools = true
	allTools, err := s.agentToolDefinitions()
	if err != nil || len(allTools) <= len(readTools) {
		t.Fatalf("write tools were not enabled: read=%d all=%d err=%v", len(readTools), len(allTools), err)
	}
}
