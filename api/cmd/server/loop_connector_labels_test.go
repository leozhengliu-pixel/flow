package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

const testConnectorToolName = "external_13e42ac3f0a14b6d9c7e58a1b2d3c4e5"

func offeredCodeSearch(t *testing.T) context.Context {
	t.Helper()
	tool := connectorTool{Definition: agentProviderTool{Name: testConnectorToolName}, Policy: applicationPolicy{Name: "code", DisplayName: "Code search"}, RemoteName: "search_code"}
	return context.WithValue(t.Context(), connectorToolsKey{}, []connectorTool{tool})
}

// A connector tool call is named after its tool and connector as it starts, in the run and in the event log.
func TestLoopRunNamesConnectorToolCalls(t *testing.T) {
	ctx := offeredCodeSearch(t)
	call := domain.AgentToolCall{ID: "call", Name: testConnectorToolName}
	rec := &loopRunRecorder{}
	index := rec.startTool(ctx, call)
	started := rec.run.ToolCalls[index]
	if started.Label != "Used search_code in Code search" || started.Connector != "Code search" || started.RemoteTool != "search_code" {
		t.Fatalf("started connector call = %#v", started)
	}
	rec.finishTool(index, "completed", nil, false, nil, nil)
	if len(rec.events) != 2 {
		t.Fatalf("events = %d", len(rec.events))
	}
	for _, event := range rec.events {
		payload, _ := json.Marshal(event)
		if !strings.Contains(string(payload), "Code search") || !strings.Contains(string(payload), "search_code") || strings.Contains(string(payload), "Used 13e42ac3") {
			t.Fatalf("event lacks the connector names: %s", payload)
		}
	}

	// A reply continuing a run records its tool calls the same way.
	answer := domain.LoopRunReply{}
	reply := &loopRunRecorder{reply: &answer}
	reply.startTool(ctx, call)
	if got := answer.ToolCalls[0]; got.Connector != "Code search" || got.RemoteTool != "search_code" {
		t.Fatalf("reply connector call = %#v", got)
	}
}

// A connector tool that is not offered keeps a readable label, never its hash.
func TestLoopToolLabelNeverShowsAConnectorHash(t *testing.T) {
	rec := &loopRunRecorder{}
	index := rec.startTool(t.Context(), domain.AgentToolCall{ID: "call", Name: testConnectorToolName})
	got := rec.run.ToolCalls[index]
	if got.Label != "Used a connector tool" || got.Connector != "" || got.RemoteTool != "" {
		t.Fatalf("unresolved connector call = %#v", got)
	}
	if label := loopCallLabel(domain.AgentToolCall{Name: testConnectorToolName, RemoteTool: "search_code"}, nil); label != "Used search_code" {
		t.Fatalf("label without a connector = %q", label)
	}
}
