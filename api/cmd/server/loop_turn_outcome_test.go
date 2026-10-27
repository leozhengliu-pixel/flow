package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Loop runs read every model turn: cut off at the output limit, reasoning only, empty after tool calls, a stream
// Flow cannot parse, a tool call cut mid-arguments, or a usable answer. Unusable turns get a bounded, targeted
// continuation and then end the run with an accurate reason; succeeded writes are never replayed. Each turn is
// recorded in the run's event log without its text or reasoning.

const loopTurnSecret = "PRIVATE-REASONING-TEXT"

// rawLoopReply is one scripted provider response.
type rawLoopReply struct {
	status      int
	contentType string
	body        string
}

// rawLoopProvider answers each request with the next scripted response, in any protocol.
type rawLoopProvider struct {
	mu      sync.Mutex
	replies []rawLoopReply
	inputs  []string
}

func (p *rawLoopProvider) serve(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	p.mu.Lock()
	p.inputs = append(p.inputs, string(raw))
	// An unscripted request fails without a retry, so a test sees it as an extra call.
	reply := rawLoopReply{status: http.StatusBadRequest, body: `{"error":{"message":"unscripted request"}}`}
	if len(p.replies) > 0 {
		reply, p.replies = p.replies[0], p.replies[1:]
	}
	p.mu.Unlock()
	if reply.status == 0 {
		reply.status = http.StatusOK
	}
	if reply.contentType == "" {
		reply.contentType = "text/event-stream"
	}
	w.Header().Set("Content-Type", reply.contentType)
	w.WriteHeader(reply.status)
	_, _ = w.Write([]byte(reply.body))
}

func (p *rawLoopProvider) requests() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.inputs...)
}

func newRawLoopServer(t *testing.T, protocol string, provider *rawLoopProvider) (*server, http.Handler) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(provider.serve))
	t.Cleanup(upstream.Close)
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	srv := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true, agent: appconfig.AgentConfig{Enabled: true, Protocol: protocol, BaseURL: upstream.URL, Model: "flow-test", MaxOutputTokens: 256, ToolsEnabled: true, WriteTools: true, AnthropicVersion: "2023-06-01"}, agentClient: upstream.Client()}
	srv.loopRunLimits.Store(testLoopLimits())
	return srv, newHandler(srv)
}

// loopWire writes scripted turns in one provider protocol.
type loopWire struct {
	protocol string
	// truncation is the stop reason the protocol reports for a turn cut at the output limit.
	truncation string
	tool       func(id, name, args string) rawLoopReply
	// cutTool is a tool call whose arguments stop mid-way at the output limit.
	cutTool   func(id, name, partial string) rawLoopReply
	text      func(text string, truncated bool) rawLoopReply
	reasoning func(reasoning string, truncated bool) rawLoopReply
}

func sseFrames(frames ...any) string {
	var body strings.Builder
	for _, frame := range frames {
		raw, _ := json.Marshal(frame)
		body.WriteString("data: " + string(raw) + "\n\n")
	}
	return body.String()
}

func sseEvents(events ...map[string]any) string {
	var body strings.Builder
	for _, event := range events {
		raw, _ := json.Marshal(event)
		body.WriteString("event: " + event["type"].(string) + "\ndata: " + string(raw) + "\n\n")
	}
	return body.String()
}

var chatWire = loopWire{
	protocol: "openai-chat-completions", truncation: "length",
	tool: func(id, name, args string) rawLoopReply {
		return rawLoopReply{body: sseFrames(
			map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"role": "assistant", "content": ""}}}},
			map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": id, "function": map[string]any{"name": name, "arguments": args}}}}, "finish_reason": "tool_calls"}}},
		) + "data: [DONE]\n\n"}
	},
	cutTool: func(id, name, partial string) rawLoopReply {
		return rawLoopReply{body: sseFrames(
			map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": id, "function": map[string]any{"name": name, "arguments": partial}}}}, "finish_reason": "length"}}},
		) + "data: [DONE]\n\n"}
	},
	text: func(text string, truncated bool) rawLoopReply {
		finish := "stop"
		if truncated {
			finish = "length"
		}
		return rawLoopReply{body: sseFrames(
			map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": text}}}},
			map[string]any{"choices": []any{map[string]any{"delta": map[string]any{}, "finish_reason": finish}}, "usage": map[string]any{"prompt_tokens": 900, "completion_tokens": 256, "prompt_tokens_details": map[string]any{"cached_tokens": 512}}},
		) + "data: [DONE]\n\n"}
	},
	reasoning: func(reasoning string, truncated bool) rawLoopReply {
		finish := "stop"
		if truncated {
			finish = "length"
		}
		return rawLoopReply{body: sseFrames(
			map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"reasoning_content": reasoning}}}},
			map[string]any{"choices": []any{map[string]any{"delta": map[string]any{}, "finish_reason": finish}}, "usage": map[string]any{"prompt_tokens": 900, "completion_tokens": 200, "completion_tokens_details": map[string]any{"reasoning_tokens": 198}}},
		) + "data: [DONE]\n\n"}
	},
}

func responsesEnd(truncated bool) map[string]any {
	if truncated {
		return map[string]any{"type": "response.incomplete", "response": map[string]any{"status": "incomplete", "incomplete_details": map[string]any{"reason": "max_output_tokens"}, "usage": map[string]any{"input_tokens": 900, "output_tokens": 256, "output_tokens_details": map[string]any{"reasoning_tokens": 40}}}}
	}
	return map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed", "usage": map[string]any{"input_tokens": 900, "output_tokens": 40}}}
}

var responsesWire = loopWire{
	protocol: "openai-responses", truncation: "incomplete",
	tool: func(id, name, args string) rawLoopReply {
		return rawLoopReply{body: sseEvents(
			map[string]any{"type": "response.output_item.added", "item": map[string]any{"id": "item_" + id, "call_id": id, "type": "function_call", "name": name, "arguments": ""}},
			map[string]any{"type": "response.function_call_arguments.done", "item_id": "item_" + id, "arguments": args},
			responsesEnd(false),
		)}
	},
	cutTool: func(id, name, partial string) rawLoopReply {
		return rawLoopReply{body: sseEvents(
			map[string]any{"type": "response.output_item.added", "item": map[string]any{"id": "item_" + id, "call_id": id, "type": "function_call", "name": name, "arguments": ""}},
			map[string]any{"type": "response.function_call_arguments.delta", "item_id": "item_" + id, "delta": partial},
			responsesEnd(true),
		)}
	},
	text: func(text string, truncated bool) rawLoopReply {
		return rawLoopReply{body: sseEvents(map[string]any{"type": "response.output_text.delta", "delta": text}, responsesEnd(truncated))}
	},
	reasoning: func(reasoning string, truncated bool) rawLoopReply {
		return rawLoopReply{body: sseEvents(map[string]any{"type": "response.reasoning_text.delta", "delta": reasoning}, responsesEnd(truncated))}
	},
}

func anthropicBody(stop string, blocks ...map[string]any) rawLoopReply {
	events := []map[string]any{{"type": "message_start", "message": map[string]any{"id": "msg_1", "usage": map[string]any{"input_tokens": 900}}}}
	events = append(events, blocks...)
	events = append(events, map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop}, "usage": map[string]any{"output_tokens": 256}}, map[string]any{"type": "message_stop"})
	return rawLoopReply{body: sseEvents(events...)}
}

var anthropicWire = loopWire{
	protocol: "anthropic-messages", truncation: "max_tokens",
	tool: func(id, name, args string) rawLoopReply {
		return anthropicBody("tool_use",
			map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "tool_use", "id": id, "name": name, "input": map[string]any{}}},
			map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": args}})
	},
	cutTool: func(id, name, partial string) rawLoopReply {
		return anthropicBody("max_tokens",
			map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "tool_use", "id": id, "name": name, "input": map[string]any{}}},
			map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": partial}})
	},
	text: func(text string, truncated bool) rawLoopReply {
		stop := "end_turn"
		if truncated {
			stop = "max_tokens"
		}
		return anthropicBody(stop, map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": text}})
	},
	reasoning: func(reasoning string, truncated bool) rawLoopReply {
		stop := "end_turn"
		if truncated {
			stop = "max_tokens"
		}
		return anthropicBody(stop, map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "thinking_delta", "thinking": reasoning}})
	},
}

var loopWires = []loopWire{chatWire, responsesWire, anthropicWire}

const loopFinishDone = `{"status":"done","summary":"Wrote the team digest."}`

// modelTurnRecords reads the run's per-turn diagnostics and checks they hold no model text.
func modelTurnRecords(t *testing.T, handler http.Handler, run domain.LoopRun) []loopModelTurnRecord {
	t.Helper()
	events := requestJSON[[]store.LoopRunEvent](t, handler, http.MethodGet, "/api/loops/"+run.LoopID+"/runs/"+run.ID+"/events", nil, http.StatusOK)
	records := []loopModelTurnRecord{}
	for _, event := range events {
		if strings.Contains(string(event.Data), loopTurnSecret) {
			t.Fatalf("event %s leaked model text: %s", event.Kind, event.Data)
		}
		if event.Kind != "model_turn" {
			continue
		}
		var record loopModelTurnRecord
		if err := json.Unmarshal(event.Data, &record); err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	return records
}

func outcomes(records []loopModelTurnRecord) []string {
	result := []string{}
	for _, record := range records {
		result = append(result, string(record.Outcome))
	}
	return result
}

func runLoopScript(t *testing.T, wire loopWire, instructions string, replies ...rawLoopReply) (*rawLoopProvider, http.Handler, domain.LoopRun) {
	t.Helper()
	provider := &rawLoopProvider{replies: replies}
	_, handler := newRawLoopServer(t, wire.protocol, provider)
	loop, started := startTestRun(t, handler, "Team digest", instructions, nil)
	return provider, handler, waitForRun(t, handler, loop.ID, started.ID)
}

func TestLoopEmptyTurnsAfterToolsEndWithAnAccurateReason(t *testing.T) {
	wire := chatWire
	provider, handler, run := runLoopScript(t, wire, "List the members and the open issues, then report.",
		wire.tool("call_1", "list_users", `{}`),
		wire.tool("call_2", "list_issues", `{"limit":5}`),
		wire.text("", false),
		wire.text("", false),
	)
	if run.Status != "needs_review" || run.FailureReason != "empty_response" {
		t.Fatalf("run = %s/%s: %s", run.Status, run.FailureReason, run.Error)
	}
	if !strings.Contains(run.Error, "no text and no tool call after the run's tool calls: 2 (stop reason stop)") || strings.Contains(run.Error, "said it would continue") {
		t.Fatalf("error = %q", run.Error)
	}
	requests := provider.requests()
	if len(requests) != 4 || !strings.Contains(requests[3], "Your last reply was empty") {
		t.Fatalf("%d requests; the continuation must be one targeted prompt", len(requests))
	}
	// The two reads that succeeded are kept.
	if len(run.ToolCalls) != 2 || run.ToolCalls[0].Status != "completed" || run.ToolCalls[1].Status != "completed" {
		t.Fatalf("tool calls = %+v", run.ToolCalls)
	}
	records := modelTurnRecords(t, handler, run)
	if got := strings.Join(outcomes(records), ","); got != "tool_calls,tool_calls,empty,empty" {
		t.Fatalf("outcomes = %s", got)
	}
	if records[2].Continuation != "empty" || records[3].Continuation != "" || records[2].StopReason != "stop" || records[2].Model != "flow-test" || records[2].Usage == nil || records[2].Usage.CachedTokens != 512 || !records[2].StreamFinished || records[2].ResponseBytes == 0 {
		t.Fatalf("records = %+v", records)
	}
}

func TestLoopReasoningOnlyTurnGetsOneContinuation(t *testing.T) {
	wire := chatWire
	provider, handler, run := runLoopScript(t, wire, "Report open issues.",
		wire.tool("call_1", "list_issues", `{"limit":5}`),
		wire.reasoning(loopTurnSecret, false),
		wire.tool("call_2", "finish_run", loopFinishDone),
	)
	if run.Status != "completed" || run.Output != "Wrote the team digest." {
		t.Fatalf("run = %s/%s: %s", run.Status, run.FailureReason, run.Error)
	}
	requests := provider.requests()
	if len(requests) != 3 || !strings.Contains(requests[2], "contained only reasoning") {
		t.Fatalf("%d requests", len(requests))
	}
	records := modelTurnRecords(t, handler, run)
	if records[1].Outcome != loopTurnReasoningOnly || records[1].Continuation != "reasoning_only" || records[1].ReasoningChars != len(loopTurnSecret) || records[1].TextChars != 0 || records[1].Usage == nil || records[1].Usage.ReasoningTokens != 198 {
		t.Fatalf("records = %+v", records)
	}

	// Reasoning only again after the continuation ends the run with that reason.
	provider, _, run = runLoopScript(t, wire, "Report open issues.",
		wire.tool("call_1", "list_issues", `{"limit":5}`),
		wire.reasoning(loopTurnSecret, false),
		wire.reasoning(loopTurnSecret, false),
	)
	if run.Status != "needs_review" || run.FailureReason != "reasoning_only" || !strings.Contains(run.Error, "only reasoning and no answer or tool call: 2") || len(provider.requests()) != 3 {
		t.Fatalf("run = %s/%s: %s after %d requests", run.Status, run.FailureReason, run.Error, len(provider.requests()))
	}
}

func TestLoopTruncatedTurnsPerProtocol(t *testing.T) {
	for _, wire := range loopWires {
		t.Run(wire.protocol, func(t *testing.T) {
			// Cut off three times: two targeted continuations, then an accurate failure.
			provider, handler, run := runLoopScript(t, wire, "Report the teams.",
				wire.tool("call_1", "list_teams", `{}`),
				wire.text("Partial report one", true),
				wire.text("Partial report two", true),
				wire.text("Partial report three", true),
			)
			if run.Status != "needs_review" || run.FailureReason != "output_truncated" || !strings.Contains(run.Error, "cut off at the output limit: 3 (stop reason "+wire.truncation+")") {
				t.Fatalf("run = %s/%s: %s", run.Status, run.FailureReason, run.Error)
			}
			requests := provider.requests()
			if len(requests) != 4 || !strings.Contains(requests[2], "was cut off at the output limit") || !strings.Contains(requests[2], "Partial report one") {
				t.Fatalf("%d requests", len(requests))
			}
			if !strings.Contains(run.Output, "Partial report three") {
				t.Fatalf("output = %q", run.Output)
			}
			records := modelTurnRecords(t, handler, run)
			if got := strings.Join(outcomes(records), ","); got != "tool_calls,truncated,truncated,truncated" || records[1].StopReason != wire.truncation || records[1].Usage == nil || records[1].Usage.OutputTokens != 256 {
				t.Fatalf("outcomes = %s records = %+v", got, records)
			}

			// Reasoning that used the whole output budget, then a recovered turn.
			_, _, run = runLoopScript(t, wire, "Report the teams.",
				wire.tool("call_1", "list_teams", `{}`),
				wire.reasoning(loopTurnSecret, true),
				wire.tool("call_2", "finish_run", loopFinishDone),
			)
			if run.Status != "completed" {
				t.Fatalf("recovered run = %s/%s: %s", run.Status, run.FailureReason, run.Error)
			}

			// A tool call cut mid-arguments is never run; the resent call is.
			provider, handler, run = runLoopScript(t, wire, "Write the team digest document.",
				wire.cutTool("call_1", "save_document", `{"title":"Team digest","content":"# Team dig`),
				wire.tool("call_2", "save_document", `{"title":"Team digest","content":"# Team digest\nAll good."}`),
				wire.tool("call_3", "finish_run", loopFinishDone),
			)
			if run.Status != "completed" || run.Produced["document"] != 1 || len(run.ToolCalls) != 2 || run.ToolCalls[0].Status != "blocked" || run.ToolCalls[1].Status != "completed" {
				t.Fatalf("run = %s/%s: %s, calls %+v", run.Status, run.FailureReason, run.Error, run.ToolCalls)
			}
			if requests := provider.requests(); !strings.Contains(requests[1], "cut off at the model's output limit before its arguments were complete") {
				t.Fatal("the cut call was not answered with an error")
			}
			if documents := teamDigestDocuments(t, handler); documents != 1 {
				t.Fatalf("%d documents", documents)
			}
		})
	}
}

func teamDigestDocuments(t *testing.T, handler http.Handler) int {
	t.Helper()
	count := 0
	for _, document := range requestJSON[[]domain.Document](t, handler, http.MethodGet, "/api/documents", nil, http.StatusOK) {
		if document.Title == "Team digest" {
			count++
		}
	}
	return count
}

func TestLoopUnreadableStreams(t *testing.T) {
	wire := chatWire
	garbage := rawLoopReply{body: "data: {\"choices\":[{\"delta\":\n\ndata: not json\n\n"}
	// One unreadable stream is asked again as it was.
	provider, handler, run := runLoopScript(t, wire, "Report the teams.",
		wire.tool("call_1", "list_teams", `{}`),
		garbage,
		wire.tool("call_2", "finish_run", loopFinishDone),
	)
	if run.Status != "completed" {
		t.Fatalf("run = %s/%s: %s", run.Status, run.FailureReason, run.Error)
	}
	if requests := provider.requests(); len(requests) != 3 || requests[1] != requests[2] {
		t.Fatalf("the unreadable turn must be asked again unchanged (%d requests)", len(requests))
	}
	records := modelTurnRecords(t, handler, run)
	if records[1].Outcome != loopTurnUnreadable || records[1].UnreadableEvents != 2 || records[1].StreamEvents != 2 || records[1].StreamFinished || records[1].Continuation != "unreadable" {
		t.Fatalf("records = %+v", records)
	}

	// Twice ends the run with the parse failure.
	_, _, run = runLoopScript(t, wire, "Report the teams.", wire.tool("call_1", "list_teams", `{}`), garbage, garbage)
	if run.Status != "needs_review" || run.FailureReason != "stream_error" || run.Error != "The model stream could not be parsed: 2 of 2 stream events were not valid JSON" {
		t.Fatalf("run = %s/%s: %s", run.Status, run.FailureReason, run.Error)
	}

	// A stream without its Content-Type is still read as one.
	untyped := wire.tool("call_1", "finish_run", loopFinishDone)
	untyped.contentType = "application/json"
	if _, _, run = runLoopScript(t, wire, "Report the teams.", untyped); run.Status != "completed" {
		t.Fatalf("untyped stream run = %s/%s: %s", run.Status, run.FailureReason, run.Error)
	}

	// A JSON body that is not a response fails as unreadable, without retrying.
	provider, _, run = runLoopScript(t, wire, "Report the teams.", rawLoopReply{contentType: "application/json", body: "<html>gateway</html>"})
	if run.Status != "failed" || run.FailureReason != "stream_error" || len(provider.requests()) != 1 {
		t.Fatalf("run = %s/%s: %s", run.Status, run.FailureReason, run.Error)
	}

	// The provider says it called tools, but none arrived in a shape Flow reads.
	claimed := rawLoopReply{body: sseFrames(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"function_call": map[string]any{"name": "list_teams"}}, "finish_reason": "tool_calls"}}}) + "data: [DONE]\n\n"}
	_, _, run = runLoopScript(t, wire, "Report the teams.", wire.tool("call_1", "list_teams", `{}`), claimed, claimed)
	if run.FailureReason != "stream_error" || !strings.Contains(run.Error, "no tool call could be read") {
		t.Fatalf("run = %s/%s: %s", run.Status, run.FailureReason, run.Error)
	}
}

// The full document chain: two reads, one document write, a transient provider failure and an empty turn after
// the write. Neither the provider retry nor the continuation repeats the write.
func TestLoopDocumentChainNeverRepeatsTheWrite(t *testing.T) {
	for _, wire := range loopWires {
		t.Run(wire.protocol, func(t *testing.T) {
			provider, handler, run := runLoopScript(t, wire, "Read the members and the open issues, then write the team digest document.",
				wire.tool("call_1", "list_users", `{}`),
				wire.tool("call_2", "list_issues", `{"limit":5}`),
				wire.tool("call_3", "save_document", `{"title":"Team digest","content":"# Team digest\nTwo open issues."}`),
				rawLoopReply{status: http.StatusServiceUnavailable, contentType: "application/json", body: `{"error":{"message":"upstream unavailable"}}`},
				wire.text("", false),
				wire.tool("call_4", "finish_run", loopFinishDone),
			)
			if run.Status != "completed" || run.Retries != 1 || run.Produced["document"] != 1 || run.Output != "Wrote the team digest." {
				t.Fatalf("run = %s/%s: %s (retries %d, produced %v)", run.Status, run.FailureReason, run.Error, run.Retries, run.Produced)
			}
			writes := 0
			for _, call := range run.ToolCalls {
				if call.Name == "save_document" {
					writes++
				}
			}
			if writes != 1 || teamDigestDocuments(t, handler) != 1 || len(provider.requests()) != 6 {
				t.Fatalf("%d save_document calls, %d documents, %d requests", writes, teamDigestDocuments(t, handler), len(provider.requests()))
			}
			records := modelTurnRecords(t, handler, run)
			if got := strings.Join(outcomes(records), ","); got != "tool_calls,tool_calls,tool_calls,empty,tool_calls" || records[3].Retries != 1 {
				t.Fatalf("outcomes = %s records = %+v", got, records)
			}
		})
	}
}

// Each adapter surfaces the raw stop reason, its detail, the reasoning and the usage, streamed or not.
func TestAgentProviderTurnDiagnostics(t *testing.T) {
	tests := []struct {
		name, protocol, contentType, body string
		stop, detail, reasoning           string
		output                            int
	}{
		{"responses json incomplete", "openai-responses", "application/json", `{"id":"r","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"why"}]}],"usage":{"input_tokens":10,"output_tokens":64}}`, "incomplete", "max_output_tokens", "why", 64},
		{"chat json length", "openai-chat-completions", "application/json", `{"choices":[{"finish_reason":"length","message":{"content":"","reasoning_content":"why"}}],"usage":{"prompt_tokens":10,"completion_tokens":64}}`, "length", "", "why", 64},
		{"anthropic json max_tokens", "anthropic-messages", "application/json", `{"id":"m","stop_reason":"max_tokens","content":[{"type":"thinking","thinking":"why"}],"usage":{"input_tokens":10,"output_tokens":64}}`, "max_tokens", "", "why", 64},
		{"chat stream reasoning field", "openai-chat-completions", "text/event-stream", "data: {\"choices\":[{\"delta\":{\"reasoning\":\"why\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"length\"}],\"usage\":{\"completion_tokens\":64}}\n\ndata: [DONE]\n\n", "length", "", "why", 64},
		{"chat stream whole messages", "openai-chat-completions", "text/event-stream", "data: {\"choices\":[{\"message\":{\"reasoning_content\":\"why\"},\"finish_reason\":\"stop\"}],\"usage\":{\"completion_tokens\":64}}\n\n", "stop", "", "why", 64},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", test.contentType)
				_, _ = w.Write([]byte(test.body))
			}))
			defer provider.Close()
			s := &server{agent: appconfig.AgentConfig{Protocol: test.protocol, BaseURL: provider.URL, Model: "model", MaxOutputTokens: 64, AnthropicVersion: "2023-06-01"}, agentClient: provider.Client()}
			turn, err := s.requestAgentTurn(context.Background(), []agentProviderMessage{{Role: "user", Content: "hello"}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if turn.StopReason != test.stop || turn.StopDetail != test.detail || turn.Reasoning != test.reasoning || turn.Usage == nil || turn.Usage.OutputTokens != test.output || turn.Stream.Bytes == 0 || !turn.Stream.Finished {
				t.Fatalf("turn = %+v usage %+v", turn, turn.Usage)
			}
		})
	}
}

func TestReadLoopTurn(t *testing.T) {
	finished := agentStreamStats{Events: 3, Finished: true, Bytes: 300}
	call := domain.AgentToolCall{ID: "call_1", Name: "save_document", Arguments: json.RawMessage(`{"title":"A"}`)}
	cut := domain.AgentToolCall{ID: "call_2", Name: "save_document", Arguments: json.RawMessage(`{"title":"A","content":"# A`)}
	tests := []struct {
		name string
		turn agentProviderTurn
		want loopTurnOutcome
	}{
		{"answer", agentProviderTurn{Text: "Three bugs are open, all assigned.", StopReason: "stop", Stream: finished}, loopTurnAnswer},
		{"promise", agentProviderTurn{Text: "I'll check the remaining items now.", StopReason: "stop", Stream: finished}, loopTurnPromise},
		{"chinese promise", agentProviderTurn{Text: "接下来我将检查剩余事项。", StopReason: "stop", Stream: finished}, loopTurnPromise},
		{"empty", agentProviderTurn{StopReason: "stop", Stream: finished}, loopTurnEmpty},
		{"whitespace", agentProviderTurn{Text: " \n", StopReason: "end_turn", Stream: finished}, loopTurnEmpty},
		{"reasoning only", agentProviderTurn{Reasoning: "thinking", StopReason: "stop", Stream: finished}, loopTurnReasoningOnly},
		{"reasoning tokens only", agentProviderTurn{StopReason: "completed", Usage: &agentProviderUsage{ReasoningTokens: 90}, Stream: finished}, loopTurnReasoningOnly},
		{"chat length", agentProviderTurn{Text: "Partial", StopReason: "length", Stream: finished}, loopTurnTruncated},
		{"anthropic max_tokens", agentProviderTurn{Reasoning: "x", StopReason: "max_tokens", Stream: finished}, loopTurnTruncated},
		{"responses incomplete", agentProviderTurn{StopReason: "incomplete", StopDetail: "max_output_tokens", Stream: finished}, loopTurnTruncated},
		{"responses filtered", agentProviderTurn{Text: "Part", StopReason: "incomplete", StopDetail: "content_filter", Stream: finished}, loopTurnFiltered},
		{"chat filtered", agentProviderTurn{StopReason: "content_filter", Stream: finished}, loopTurnFiltered},
		{"tool call text", agentProviderTurn{Text: "<tool_call>{\"name\":\"list_issues\"}</tool_call>", StopReason: "stop", Stream: finished}, loopTurnToolCallText},
		{"claimed tool calls", agentProviderTurn{StopReason: "tool_calls", Stream: finished}, loopTurnUnreadable},
		{"invalid frames", agentProviderTurn{Stream: agentStreamStats{Events: 4, Unreadable: 2, Bytes: 200}}, loopTurnUnreadable},
		{"unknown frames", agentProviderTurn{StopReason: "stop", Stream: agentStreamStats{Events: 5, Unrecognized: 4, Finished: true}}, loopTurnUnreadable},
		{"no frames", agentProviderTurn{Stream: agentStreamStats{Bytes: 512}}, loopTurnUnreadable},
		{"unfinished stream", agentProviderTurn{Stream: agentStreamStats{Events: 3}}, loopTurnUnreadable},
		{"tool calls", agentProviderTurn{ToolCalls: []domain.AgentToolCall{call}, StopReason: "tool_calls", Stream: finished}, loopTurnToolCalls},
		{"tool call cut at the limit", agentProviderTurn{ToolCalls: []domain.AgentToolCall{call, cut}, StopReason: "length", Stream: finished}, loopTurnTruncated},
		{"tool call cut by the stream", agentProviderTurn{ToolCalls: []domain.AgentToolCall{cut}, Stream: agentStreamStats{Events: 3}}, loopTurnUnreadable},
		{"complete call at the limit", agentProviderTurn{ToolCalls: []domain.AgentToolCall{call}, StopReason: "length", Stream: finished}, loopTurnToolCalls},
	}
	for _, test := range tests {
		reading := readLoopTurn(test.turn)
		if reading.outcome != test.want {
			t.Errorf("%s: outcome %s, want %s (%s)", test.name, reading.outcome, test.want, reading.detail)
		}
		if test.name == "tool call cut at the limit" && (!reading.cut[1] || reading.cut[0]) {
			t.Errorf("cut = %v", reading.cut)
		}
	}
	if loopTextPromisesMoreWork("") || loopTextPromisesMoreWork("  ") {
		t.Fatal("empty text promises nothing")
	}
}

func TestLoopTurnRecoveryIsBounded(t *testing.T) {
	recovery := newLoopTurnRecovery(loopRunLimits{Timeout: loopRunTimeout})
	ctx := context.Background()
	turn := agentProviderTurn{StopReason: "length"}
	allowed := 0
	for range 10 {
		for _, outcome := range []loopTurnOutcome{loopTurnTruncated, loopTurnReasoningOnly, loopTurnEmpty, loopTurnUnreadable} {
			if recovery.allow(ctx, outcome, turn, 0) {
				allowed++
			}
		}
	}
	if allowed != loopRecoveryTotal {
		t.Fatalf("%d continuations, want the run total %d", allowed, loopRecoveryTotal)
	}
	// Per cause: truncation gets two, the others one.
	recovery = newLoopTurnRecovery(loopRunLimits{Timeout: loopRunTimeout})
	if !recovery.allow(ctx, loopTurnTruncated, turn, 0) || !recovery.allow(ctx, loopTurnTruncated, turn, 0) || recovery.allow(ctx, loopTurnTruncated, turn, 0) {
		t.Fatal("truncation gets two continuations")
	}
	if recovery.allow(ctx, loopTurnFiltered, turn, 0) {
		t.Fatal("a filtered turn is never continued")
	}
	// The model time spent on unusable turns caps them too.
	recovery = newLoopTurnRecovery(loopRunLimits{Timeout: loopRunTimeout})
	if recovery.allow(ctx, loopTurnEmpty, turn, loopRecoveryMaxWait) {
		t.Fatal("no continuation once unusable turns used the wait budget")
	}
	// No continuation starts when the run is about to time out.
	short, cancel := context.WithTimeout(ctx, loopRecoveryMinRemaining/2)
	defer cancel()
	recovery = newLoopTurnRecovery(loopRunLimits{Timeout: loopRunTimeout})
	if recovery.allow(short, loopTurnEmpty, turn, 0) {
		t.Fatal("no continuation in the run's last seconds")
	}
}
