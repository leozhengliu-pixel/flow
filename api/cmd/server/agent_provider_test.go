package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
				"event: response.reasoning_summary_text.delta\ndata: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"Checked the workspace.\"}\n\n" +
				"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Hello \"}\n\n" +
				"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"world\"}\n\n" +
				"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"item_1\",\"call_id\":\"call_1\",\"type\":\"function_call\",\"name\":\"list_issues\",\"arguments\":\"\"}}\n\n" +
				"event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"item_id\":\"item_1\",\"delta\":\"{\\\"query\\\":\\\"bug\\\"}\"}\n\n" +
				"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n",
			assertBody: func(t *testing.T, body map[string]any, header http.Header) {
				reasoning, _ := body["reasoning"].(map[string]any)
				if body["stream"] != true || body["instructions"] != "System" || reasoning["summary"] != "auto" || header.Get("Authorization") != "Bearer secret" {
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
			if test.protocol == "openai-responses" && turn.Reasoning != "Checked the workspace." {
				t.Fatalf("reasoning=%q events=%#v", turn.Reasoning, events)
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
		{"openai-responses", "/responses", `{"id":"resp","output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"response reasoning"}]},{"type":"message","content":[{"type":"output_text","text":"response text"}]}]}`},
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
			if test.protocol == "openai-responses" && turn.Reasoning != "response reasoning" {
				t.Fatalf("reasoning=%q", turn.Reasoning)
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

func TestAgentReasoningEffortIsSentWhenConfigured(t *testing.T) {
	tests := []struct {
		protocol string
		path     string
		body     string
		effort   func(map[string]any) any
	}{
		{"openai-responses", "/responses", `{"id":"resp","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`, func(body map[string]any) any {
			reasoning, _ := body["reasoning"].(map[string]any)
			if reasoning["summary"] != "auto" {
				return "missing summary"
			}
			return reasoning["effort"]
		}},
		{"openai-chat-completions", "/chat/completions", `{"choices":[{"finish_reason":"stop","message":{"content":"ok"}}]}`, func(body map[string]any) any { return body["reasoning_effort"] }},
	}
	for _, test := range tests {
		for _, effort := range []string{"", "medium"} {
			t.Run(test.protocol+"/"+effort, func(t *testing.T) {
				var got any
				provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					got = test.effort(body)
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(test.body))
				}))
				defer provider.Close()
				s := &server{agent: appconfig.AgentConfig{Protocol: test.protocol, BaseURL: provider.URL, Model: "model", MaxOutputTokens: 100, ReasoningEffort: effort}, agentClient: provider.Client()}
				if _, err := s.requestAgentTurn(context.Background(), []agentProviderMessage{{Role: "user", Content: "hello"}}, nil); err != nil {
					t.Fatal(err)
				}
				if effort == "" && got != nil || effort != "" && got != effort {
					t.Fatalf("effort sent = %#v, want %q", got, effort)
				}
			})
		}
	}
}

// FLOW_AGENT_TIMEOUT bounds how long a streamed answer may stall, not how long it may run.
func TestAgentStreamTimeoutIsAnIdleTimeout(t *testing.T) {
	streamChunks := func(chunks int, gap time.Duration, stall bool) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			flusher := w.(http.Flusher)
			for index := 0; index < chunks; index++ {
				_, _ = fmt.Fprintf(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"x\"}\n\n")
				flusher.Flush()
				time.Sleep(gap)
			}
			if stall {
				select {
				case <-r.Context().Done():
				case <-time.After(2 * time.Second):
				}
				return
			}
			_, _ = fmt.Fprintf(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp\",\"output\":[]}}\n\n")
		}))
	}
	config := func(url string) appconfig.AgentConfig {
		return appconfig.AgentConfig{Protocol: "openai-responses", BaseURL: url, Model: "model", MaxOutputTokens: 100, Timeout: 250 * time.Millisecond}
	}

	if newAgentHTTPClient().Timeout != 0 {
		t.Fatal("the server's provider client must not cap the whole streamed response")
	}
	// nil is the fallback client; newAgentHTTPClient is the one main() installs. Both must keep long turns alive.
	for name, client := range map[string]*http.Client{"fallback": nil, "server": newAgentHTTPClient()} {
		t.Run(name, func(t *testing.T) {
			steady := streamChunks(8, 80*time.Millisecond, false)
			defer steady.Close()
			s := &server{agent: config(steady.URL), agentClient: client}
			start := time.Now()
			turn, err := s.requestAgentTurn(context.Background(), []agentProviderMessage{{Role: "user", Content: "hello"}}, nil)
			if err != nil || turn.Text != "xxxxxxxx" {
				t.Fatalf("steady stream: turn=%#v err=%v", turn, err)
			}
			if time.Since(start) < 500*time.Millisecond {
				t.Fatalf("the stream finished before the timeout elapsed (%s), so it proves nothing", time.Since(start))
			}

			stalled := streamChunks(1, 0, true)
			defer stalled.Close()
			s = &server{agent: config(stalled.URL), agentClient: client}
			_, err = s.requestAgentTurn(context.Background(), []agentProviderMessage{{Role: "user", Content: "hello"}}, nil)
			var providerErr *agentProviderError
			if !errors.As(err, &providerErr) || !providerErr.timeout || !providerErr.transient {
				t.Fatalf("stalled stream error = %v", err)
			}
			if strings.Contains(err.Error(), "could not read Agent stream") {
				t.Fatalf("an idle timeout should read as the provider stalling, got %q", err)
			}
		})
	}
}

// A provider hiccup before anything streamed is asked again once; a turn that already showed output is not.
func TestAgentChatTurnRetriesTransientFailuresBeforeOutput(t *testing.T) {
	previous := agentChatRetryDelay
	agentChatRetryDelay = 0
	defer func() { agentChatRetryDelay = previous }()
	completed := "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n" +
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp\",\"status\":\"completed\"}}\n\n"
	failures := map[string]func(http.ResponseWriter){
		"gateway 502": func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":{"message":"upstream unavailable"}}`))
		},
		"stream server_error": func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"server_error\",\"message\":\"The server had an error\"}}}\n\n")
		},
	}
	for name, fail := range failures {
		t.Run(name, func(t *testing.T) {
			calls := 0
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls == 1 {
					fail(w)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, completed)
			}))
			defer provider.Close()
			s := &server{agent: appconfig.AgentConfig{Protocol: "openai-responses", BaseURL: provider.URL, Model: "model", MaxOutputTokens: 100}, agentClient: provider.Client()}
			turn, err := s.requestAgentChatTurn(context.Background(), []agentProviderMessage{{Role: "user", Content: "hello"}}, func(agentProviderEvent) error { return nil })
			if err != nil || turn.Text != "ok" || calls != 2 {
				t.Fatalf("turn=%#v err=%v calls=%d", turn, err, calls)
			}
		})
	}

	t.Run("after output", func(t *testing.T) {
		calls := 0
		provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n"+
				"event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"server_error\",\"message\":\"boom\"}}}\n\n")
		}))
		defer provider.Close()
		s := &server{agent: appconfig.AgentConfig{Protocol: "openai-responses", BaseURL: provider.URL, Model: "model", MaxOutputTokens: 100}, agentClient: provider.Client()}
		_, err := s.requestAgentChatTurn(context.Background(), []agentProviderMessage{{Role: "user", Content: "hello"}}, func(agentProviderEvent) error { return nil })
		if err == nil || err.Error() != "boom" || calls != 1 {
			t.Fatalf("err=%v calls=%d", err, calls)
		}
	})

	t.Run("permanent", func(t *testing.T) {
		calls := 0
		provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"bad tools"}}`))
		}))
		defer provider.Close()
		s := &server{agent: appconfig.AgentConfig{Protocol: "openai-responses", BaseURL: provider.URL, Model: "model", MaxOutputTokens: 100}, agentClient: provider.Client()}
		if _, err := s.requestAgentChatTurn(context.Background(), []agentProviderMessage{{Role: "user", Content: "hello"}}, func(agentProviderEvent) error { return nil }); err == nil || calls != 1 {
			t.Fatalf("err=%v calls=%d", err, calls)
		}
	})
}
