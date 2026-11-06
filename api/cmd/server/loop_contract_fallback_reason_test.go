package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// Each class of a failed contract extraction reaches the loop's stored fallback contract as the structured
// fallbackReason field (scripted provider replies only), next to the generic notice, and the field holds a fixed
// explanation, never the model's output or reasoning.
func TestLoopContractFallbackReasonReachesTheResponse(t *testing.T) {
	for _, item := range []struct {
		name  string
		body  string
		class string
	}{
		{"empty", structuredChatSSE(`{"choices":[{"delta":{"role":"assistant","content":""}}]}`), "empty"},
		{"reasoning only", structuredReasoningOnlySSE(), "reasoning_only"},
		{"truncated", structuredChatSSE(structuredToolCallFrame(`{"readOnly":true,"outputs":[{"kind":"doc`, "length")), "truncated"},
		{"filtered", structuredChatSSE(`{"choices":[{"delta":{"role":"assistant","content":""},"finish_reason":"content_filter"}]}`), "filtered"},
		{"tool call as text", structuredChatSSE(structuredContentFrame("<tool_call>"+loopContractToolName+"</tool_call>", "stop")), "tool_call_text"},
		{"unreadable", "data: not json\n\ndata: still not json\n\n", "unreadable"},
		{"invalid contract", structuredChatSSE(structuredToolCallFrame(`{"readOnly":false,"outputs":[{"kind":"nonsense","operation":"create"}]}`, "tool_calls")), "invalid_contract"},
	} {
		t.Run(item.name, func(t *testing.T) {
			captureStructuredLogs(t)
			_, upstream := newStructuredChatServer(t, item.body)
			srv, handler := newReliableLoopServer(t, &fakeLoopProvider{})
			srv.agent, srv.agentClient, srv.loopContractLive, srv.loopContractDebounce = structuredChatAgent(upstream), upstream.Client(), true, time.Hour

			loop := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops", map[string]any{"name": "Daily summary", "instructions": loopDailyDocumentZH, "status": "published"}, http.StatusCreated)
			path := "/api/loops/" + loop.ID + "/output-contract"
			for _, body := range []map[string]any{{}, {"force": true}} {
				analyzed := requestJSON[domain.Loop](t, handler, http.MethodPost, path, body, http.StatusOK)
				contract := analyzed.OutputContract
				if contract == nil || contract.Source != loopContractFallback || !strings.HasPrefix(contract.Error, loopContractFallbackNotice) {
					t.Fatalf("fallback with %v = %+v", body, contract)
				}
				reason := contract.FallbackReason
				if reason == nil || reason.Class != item.class || reason.Explanation == "" || !strings.Contains(contract.Error, "("+item.class+": "+reason.Explanation+")") {
					t.Fatalf("fallbackReason with %v = %+v, error %q, want class %s", body, reason, contract.Error, item.class)
				}
				if strings.Contains(reason.Explanation, structuredReasoningMarker) {
					t.Fatalf("the reasoning reached the explanation: %q", reason.Explanation)
				}
			}

			// The wire shape: a nested object the client reads without parsing the error text.
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/loops/"+loop.ID, nil))
			var wire struct {
				OutputContract struct {
					FallbackReason map[string]string `json:"fallbackReason"`
				} `json:"outputContract"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &wire); err != nil || wire.OutputContract.FallbackReason["class"] != item.class || wire.OutputContract.FallbackReason["explanation"] == "" {
				t.Fatalf("served loop = %s (%v)", response.Body.String(), err)
			}
		})
	}
}

// Without a model the class is no_model; a fallback saved before the field existed (or a cancelled wait, which
// has no class) serves no fallbackReason, so the client shows the generic notice alone.
func TestLoopContractFallbackReasonNoModelAndLegacy(t *testing.T) {
	srv, handler := newReliableLoopServer(t, &fakeLoopProvider{})
	srv.agent.Enabled = false
	loop := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops", map[string]any{"name": "x", "instructions": "Create an issue for each failing check."}, http.StatusCreated)
	analyzed := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/output-contract", nil, http.StatusOK)
	if reason := analyzed.OutputContract.FallbackReason; reason == nil || reason.Class != "no_model" {
		t.Fatalf("fallbackReason without a model = %+v", reason)
	}

	legacy := loopFallbackContract("hash", loopContractFallbackNotice+" (reasoning_only: the response held only reasoning)")
	raw, err := json.Marshal(legacy)
	if err != nil || strings.Contains(string(raw), "fallbackReason") || !strings.Contains(string(raw), `"error"`) {
		t.Fatalf("legacy contract = %s (%v)", raw, err)
	}
	if loopContractFallbackCause(nil) != nil || loopContractFallbackReason(nil) != loopContractFallbackNotice {
		t.Fatal("no error gives the generic notice alone")
	}
}
