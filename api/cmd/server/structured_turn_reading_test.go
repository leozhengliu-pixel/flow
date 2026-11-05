package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
)

// structuredReasoningMarker stands for the model's reasoning: it must never reach an error, a log line, a stored
// contract or a later request.
const structuredReasoningMarker = "SYNTHETIC-REASONING-MARKER-7f3a"

const structuredValidContract = `{"readOnly":true,"outputs":[],"always":false,"skipConditions":[]}`

// chatSSE is a Chat Completions stream of these event payloads, ending with [DONE].
func structuredChatSSE(events ...string) string {
	var body strings.Builder
	for _, event := range events {
		body.WriteString("data: " + event + "\n\n")
	}
	body.WriteString("data: [DONE]\n\n")
	return body.String()
}

func structuredToolCallFrame(arguments, finish string) string {
	quoted, _ := json.Marshal(arguments)
	frame := `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"` + loopContractToolName + `","arguments":` + string(quoted) + `}}]}`
	if finish != "" {
		frame += `,"finish_reason":"` + finish + `"`
	}
	return frame + `}]}`
}

func structuredContentFrame(content, finish string) string {
	quoted, _ := json.Marshal(content)
	return `{"choices":[{"delta":{"content":` + string(quoted) + `},"finish_reason":"` + finish + `"}]}`
}

func structuredReasoningOnlySSE() string {
	events := []string{`{"choices":[{"delta":{"role":"assistant","content":""}}]}`}
	quoted, _ := json.Marshal(structuredReasoningMarker + " thinking about the contract. ")
	for range 40 {
		events = append(events, `{"choices":[{"delta":{"reasoning_content":`+string(quoted)+`}}]}`)
	}
	events = append(events, `{"choices":[],"usage":{"prompt_tokens":900,"completion_tokens":1837}}`)
	return structuredChatSSE(events...)
}

// structuredChatServer answers every Chat Completions request with body and records the requests.
type structuredChatServer struct {
	mu       sync.Mutex
	requests []string
}

func newStructuredChatServer(t *testing.T, body string) (*structuredChatServer, *httptest.Server) {
	t.Helper()
	recorder := &structuredChatServer{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload bytes.Buffer
		_, _ = payload.ReadFrom(r.Body)
		recorder.mu.Lock()
		recorder.requests = append(recorder.requests, payload.String())
		recorder.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(upstream.Close)
	return recorder, upstream
}

func (recorder *structuredChatServer) snapshot() []string {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]string(nil), recorder.requests...)
}

func structuredChatAgent(upstream *httptest.Server) appconfig.AgentConfig {
	return appconfig.AgentConfig{Enabled: true, Protocol: "openai-chat-completions", BaseURL: upstream.URL, Model: "flow-test", MaxOutputTokens: 256}
}

func captureStructuredLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })
	return &logs
}

// A structured turn is read before its contract is parsed: a response that delivered nothing (empty, reasoning
// only, cut-off arguments) is classified as such, asked again once with a hint for its class, and never reported
// as a JSON error; tool arguments and a JSON answer as text are both read.
func TestLoopContractExtractionClassifiesStructuredTurns(t *testing.T) {
	for _, item := range []struct {
		name     string
		body     string
		class    loopTurnOutcome
		hint     string
		requests int
	}{
		{"empty output", structuredChatSSE(`{"choices":[{"delta":{"role":"assistant","content":""}}]}`), loopTurnEmpty, "Your last reply was empty", 2},
		{"reasoning only", structuredReasoningOnlySSE(), loopTurnReasoningOnly, "contained only reasoning", 2},
		{"truncated arguments", structuredChatSSE(structuredToolCallFrame(`{"readOnly":true,"outputs":[{"kind":"doc`, "length")), loopTurnTruncated, "cut off at the output limit", 2},
		{"valid tool arguments", structuredChatSSE(structuredToolCallFrame(structuredValidContract, "tool_calls")), "", "", 1},
		{"valid JSON content", structuredChatSSE(structuredContentFrame("```json\n"+structuredValidContract+"\n```", "stop")), "", "", 1},
	} {
		t.Run(item.name, func(t *testing.T) {
			logs := captureStructuredLogs(t)
			recorder, upstream := newStructuredChatServer(t, item.body)
			srv := &server{loopContractLive: true, agent: structuredChatAgent(upstream), agentClient: upstream.Client()}
			contract, err := srv.requestLoopContract(context.Background(), domain.Loop{ID: "loop_1", Name: "x", Instructions: "Summarize open bugs."}, nil)
			requests := recorder.snapshot()
			var missing *agentStructuredMissingError
			class := loopTurnOutcome("")
			if errors.As(err, &missing) {
				class = missing.class
			}
			t.Logf("case=%q class=%q requests=%d err=%v", item.name, class, len(requests), err)
			if len(requests) != item.requests {
				t.Fatalf("%d requests, want %d", len(requests), item.requests)
			}
			if item.class == "" {
				if err != nil || contract == nil || !contract.ReadOnly || contract.Source != loopContractModel {
					t.Fatalf("contract = %+v, %v", contract, err)
				}
				return
			}
			if contract != nil || missing == nil || missing.class != item.class {
				t.Fatalf("contract = %+v, err = %v, want class %s", contract, err, item.class)
			}
			if errors.Is(err, errLoopContractInvalid) || errors.Is(err, errLoopContractNotJSON) || strings.Contains(err.Error(), "unexpected end of JSON input") {
				t.Fatalf("a missing result reported as an invalid contract: %v", err)
			}
			if !strings.Contains(requests[1], item.hint) {
				t.Fatalf("second request lacks the %s hint: %s", item.class, requests[1])
			}
			reason := loopContractFallbackReason(err)
			if !strings.HasPrefix(reason, loopContractFallbackNotice) || !strings.Contains(reason, "("+string(item.class)+": ") || strings.Contains(reason, "no model") {
				t.Fatalf("fallback reason = %q", reason)
			}
			for where, text := range map[string]string{"error": err.Error(), "logs": logs.String(), "second request": requests[1], "fallback reason": reason} {
				if strings.Contains(text, structuredReasoningMarker) {
					t.Fatalf("the reasoning reached the %s: %s", where, text)
				}
			}
		})
	}
}

// The observed failure: HTTP 200, a parseable stream of reasoning only, [DONE] without a finish reason. The error
// carries the class and the response's sizes, and the stored fallback says which class it was, on both the read
// that runs and the editor's forced read.
func TestLoopContractReasoningOnlyFallbackKeepsClass(t *testing.T) {
	logs := captureStructuredLogs(t)
	recorder, upstream := newStructuredChatServer(t, structuredReasoningOnlySSE())
	srv, handler := newReliableLoopServer(t, &fakeLoopProvider{})
	srv.agent, srv.agentClient, srv.loopContractLive, srv.loopContractDebounce = structuredChatAgent(upstream), upstream.Client(), true, time.Hour

	_, err := srv.requestLoopContract(context.Background(), domain.Loop{ID: "loop_1", Name: "x", Instructions: "Summarize open bugs."}, nil)
	var missing *agentStructuredMissingError
	if !errors.As(err, &missing) || missing.class != loopTurnReasoningOnly || missing.contentBytes != 0 || missing.reasoningBytes == 0 || missing.toolCalls != 0 || missing.finishReason != "none" || !missing.streamFinished || missing.usage == nil || missing.usage.OutputTokens != 1837 {
		t.Fatalf("error = %#v", err)
	}
	for _, field := range []string{"reasoning_only", "content_bytes=0", "tool_calls=0", "finish_reason=none", "stream_finished=true", "output_tokens=1837"} {
		if !strings.Contains(err.Error(), field) {
			t.Fatalf("error %q lacks %s", err, field)
		}
	}

	loop := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops", map[string]any{"name": "每日总结", "instructions": loopDailyDocumentZH, "status": "published"}, http.StatusCreated)
	before := len(recorder.snapshot())
	for _, body := range []map[string]any{{}, {"force": true}} {
		analyzed := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/output-contract", body, http.StatusOK)
		contract := analyzed.OutputContract
		if contract == nil || contract.Source != loopContractFallback || !contract.ReadOnly || !strings.HasPrefix(contract.Error, loopContractFallbackNotice) || !strings.Contains(contract.Error, "(reasoning_only: ") || strings.Contains(contract.Error, structuredReasoningMarker) {
			t.Fatalf("fallback with %v = %+v", body, contract)
		}
		t.Logf("stored fallback (%v): %q", body, contract.Error)
	}
	if requests := len(recorder.snapshot()) - before; requests != 4 {
		t.Fatalf("%d extraction requests for two reads, want 2 each", requests)
	}
	if strings.Contains(logs.String(), structuredReasoningMarker) || !strings.Contains(logs.String(), "reasoning_only") {
		t.Fatalf("logs = %s", logs.String())
	}
}

// The issue match check reads its result the same way: reasoning only is a missing result, not an invalid answer.
func TestLoopIssueMatchReasoningOnlyIsMissingResult(t *testing.T) {
	_, upstream := newStructuredChatServer(t, structuredReasoningOnlySSE())
	srv := &server{agent: structuredChatAgent(upstream), agentClient: upstream.Client()}
	_, _, err := srv.requestLoopIssueMatch(context.Background(), domain.Issue{Title: "x"}, "open bugs")
	var missing *agentStructuredMissingError
	if !errors.As(err, &missing) || missing.class != loopTurnReasoningOnly || strings.Contains(err.Error(), structuredReasoningMarker) {
		t.Fatalf("error = %v", err)
	}
}
