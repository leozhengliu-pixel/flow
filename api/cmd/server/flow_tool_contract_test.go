package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// contractCase is one argument set for a Flow tool and whether the contract accepts it.
type contractCase struct {
	name   string
	args   map[string]any
	accept bool
}

// contractPaths calls Flow tools both ways: the built-in agent path and the /mcp handler.
type contractPaths struct {
	*mcpContractFixture
	mcp http.HandlerFunc
}

func newContractPaths(t *testing.T) *contractPaths {
	t.Helper()
	f := newMCPContractFixture(t)
	f.service.agent.ToolsEnabled, f.service.agent.WriteTools = true, true
	return &contractPaths{mcpContractFixture: f, mcp: f.service.mcpHTTP(false)}
}

// builtinToolCall runs a Flow tool the way chat, loops and application agents do (executeAgentTool), as f's viewer.
func builtinToolCall(f *contractPaths, name string, args map[string]any) ([]byte, error) {
	ctx := context.WithValue(context.Background(), authUserContextKey{}, f.data.Viewer)
	ctx = store.ContextWithActor(ctx, f.data.Viewer)
	request := httptest.NewRequest(http.MethodPost, "/api/agent?workspace="+f.data.Workspace.URLKey, nil).WithContext(ctx)
	raw, _ := json.Marshal(args)
	return f.service.executeAgentTool(request, f.data, domain.AgentToolCall{Name: name, Arguments: raw})
}

// mcpRefusal is the text of an /mcp tools/call that failed its argument check, or "" when the arguments passed it.
func mcpRefusal(t *testing.T, f *contractPaths, name string, args map[string]any) string {
	t.Helper()
	// The /mcp handler itself, without the HTTP middleware's per-key rate limit (this test makes thousands of calls).
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
	request := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+f.secret)
	recorder := httptest.NewRecorder()
	f.mcp(recorder, request)
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("%s: %d %s", name, recorder.Code, recorder.Body.String())
	}
	result, _ := response["result"].(map[string]any)
	if result == nil {
		t.Fatalf("%s: protocol error %v", name, response["error"])
	}
	if result["isError"] != true {
		return ""
	}
	text := result["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.HasPrefix(text, "Invalid arguments for "+name+": ") {
		return ""
	}
	return text
}

// builtinRefusal is the same for the built-in path.
func builtinRefusal(f *contractPaths, name string, args map[string]any) string {
	_, err := builtinToolCall(f, name, args)
	if err == nil || !strings.HasPrefix(err.Error(), "Invalid arguments for "+name+": ") {
		return ""
	}
	return err.Error()
}

// sampleSchemaValue is a value the schema node accepts: the first enum value, the minimum, one sample item.
func sampleSchemaValue(node map[string]any) any {
	if values, ok := node["enum"].([]any); ok && len(values) > 0 {
		return values[0]
	}
	if branches, ok := node["oneOf"].([]any); ok && len(branches) > 0 {
		return sampleSchemaValue(branches[0].(map[string]any))
	}
	kind, _ := node["type"].(string)
	if kinds, ok := node["type"].([]any); ok && len(kinds) > 0 {
		kind, _ = kinds[0].(string)
	}
	switch kind {
	case "integer", "number":
		if minimum, ok := node["minimum"].(float64); ok {
			return minimum
		}
		return float64(1)
	case "boolean":
		return true
	case "array":
		if items, ok := node["items"].(map[string]any); ok {
			return []any{sampleSchemaValue(items)}
		}
		return []any{}
	case "object":
		return sampleSchemaObject(node)
	}
	return "x"
}

// sampleSchemaObject fills an object schema's required properties.
func sampleSchemaObject(node map[string]any) map[string]any {
	object := map[string]any{}
	properties, _ := node["properties"].(map[string]any)
	required, _ := node["required"].([]any)
	for _, key := range required {
		if property, ok := properties[key.(string)].(map[string]any); ok {
			object[key.(string)] = sampleSchemaValue(property)
		}
	}
	return object
}

// wrongSchemaValue is a value of a JSON type the schema node does not allow; ok is false for a node of any type.
func wrongSchemaValue(node map[string]any) (any, bool) {
	allowed := map[string]bool{}
	switch kind := node["type"].(type) {
	case string:
		allowed[kind] = true
	case []any:
		for _, item := range kind {
			allowed[item.(string)] = true
		}
	default:
		return nil, false
	}
	if allowed["number"] {
		allowed["integer"] = true
	}
	// A quoted number is normalised into a number, so it is not a wrong value for a numeric argument.
	for _, candidate := range []struct {
		kind  string
		value any
	}{{"object", map[string]any{"unexpected": true}}, {"array", []any{"x"}}, {"string", "not-a-number"}, {"integer", float64(12345)}, {"boolean", false}} {
		if !allowed[candidate.kind] {
			return candidate.value, true
		}
	}
	return nil, false
}

// contractCases derives the argument sets of one tool from its published schema.
func contractCases(tool flowMCPTool) []contractCase {
	var schema map[string]any
	_ = json.Unmarshal(tool.InputSchema, &schema)
	valid := sampleSchemaObject(schema)
	if tool.Name == "save_document" {
		valid["title"] = "Contract"
	}
	with := func(key string, value any) map[string]any {
		args := maps.Clone(valid)
		args[key] = value
		return args
	}
	// The attachment tools take exactly one of `issue` or `document` (checked by the handler, the same refusal on
	// both paths).
	attachmentTool := slices.Contains([]string{"prepare_attachment_upload", "create_attachment", "create_attachment_from_upload", "save_attachment"}, tool.Name)
	if attachmentTool {
		valid["issue"] = "x"
	}
	cases := []contractCase{{name: "required arguments", args: valid, accept: true}, {name: "unknown argument", args: with("notAnArgument", "x")}}
	if attachmentTool {
		noTarget := maps.Clone(valid)
		delete(noTarget, "issue")
		cases = append(cases, contractCase{name: "no issue or document", args: noTarget}, contractCase{name: "both issue and document", args: with("document", "x")})
	}
	required, _ := schema["required"].([]any)
	if len(required) > 0 {
		args := maps.Clone(valid)
		delete(args, required[0].(string))
		cases = append(cases, contractCase{name: "missing " + required[0].(string), args: args})
	}
	properties, _ := schema["properties"].(map[string]any)
	keys := slices.Sorted(maps.Keys(properties))
	for _, key := range keys {
		property := properties[key].(map[string]any)
		if value, ok := wrongSchemaValue(property); ok {
			cases = append(cases, contractCase{name: "wrong type for " + key, args: with(key, value)})
		}
		if values, ok := property["enum"].([]any); ok && len(values) > 0 {
			if _, isString := values[0].(string); isString {
				cases = append(cases, contractCase{name: "unlisted value for " + key, args: with(key, "not-an-option")})
			}
		}
	}
	return cases
}

// Every Flow tool: the built-in agent path and /mcp accept and refuse the same arguments, and a refusal reads the
// same on both.
func TestBuiltinAndMCPAgreeOnEveryToolsArguments(t *testing.T) {
	f := newContractPaths(t)
	checked := 0
	for _, tool := range flowToolContract() {
		if agentMCPOnlyTools[tool.Name] {
			// Not offered to the built-in agent (agentMCPOnlyTools).
			continue
		}
		for _, item := range contractCases(tool) {
			builtin := builtinRefusal(f, tool.Name, maps.Clone(item.args))
			remote := mcpRefusal(t, f, tool.Name, maps.Clone(item.args))
			if (builtin == "") != item.accept || (remote == "") != item.accept {
				t.Errorf("%s, %s %v: built-in refused=%v /mcp refused=%v, want refused=%v (built-in %q, /mcp %q)", tool.Name, item.name, item.args, builtin != "", remote != "", !item.accept, builtin, remote)
				continue
			}
			if builtin != remote {
				t.Errorf("%s, %s: refusals differ:\n built-in %q\n /mcp     %q", tool.Name, item.name, builtin, remote)
			}
			checked++
		}
	}
	if checked < 500 {
		t.Fatalf("only %d argument sets checked", checked)
	}
}

// The loose forms the handlers accept are part of the contract on both paths: full or capitalised weekday names,
// quoted numbers and booleans. Other loose forms are refused on both.
func TestLooseToolArgumentsAreNormalisedOnBothPaths(t *testing.T) {
	f := newContractPaths(t)
	for _, item := range []struct {
		tool   string
		args   map[string]any
		accept bool
	}{
		{"save_loop", map[string]any{"name": "Weekly", "trigger": "schedule", "unit": "week", "weekdays": []any{"monday"}}, true},
		{"save_loop", map[string]any{"name": "Weekly", "trigger": "schedule", "unit": "week", "weekdays": []any{"Mon", "FRIDAY"}}, true},
		{"save_loop", map[string]any{"name": "Weekly", "trigger": "schedule", "unit": "week", "interval": "2"}, true},
		{"save_loop", map[string]any{"name": "Weekly", "trigger": "schedule", "publish": "false"}, true},
		{"save_loop", map[string]any{"name": "Weekly", "weekdays": []any{"funday"}}, false},
		{"save_loop", map[string]any{"name": "Weekly", "weekdays": "mon"}, false},
		{"save_loop", map[string]any{"name": "Weekly", "interval": "2.5"}, false},
		{"save_loop", map[string]any{"name": "Weekly", "interval": "two"}, false},
		{"save_loop", map[string]any{"name": "Weekly", "starting": "2026-11-01"}, false},
		{"save_issue", map[string]any{"title": "Quoted", "team": f.data.Teams[0].Key, "priority": "2", "estimate": "3"}, true},
		{"save_issue", map[string]any{"title": "Label string", "team": f.data.Teams[0].Key, "labels": "Bug"}, false},
		{"save_issue", map[string]any{"title": "Null title", "team": f.data.Teams[0].Key, "description": nil}, false},
		{"list_issues", map[string]any{"limit": "5", "includeArchived": "true"}, true},
	} {
		builtin := builtinRefusal(f, item.tool, maps.Clone(item.args))
		remote := mcpRefusal(t, f, item.tool, maps.Clone(item.args))
		if (builtin == "") != item.accept || (remote == "") != item.accept || builtin != remote {
			t.Errorf("%s %v: built-in %q, /mcp %q, want accepted=%v", item.tool, item.args, builtin, remote, item.accept)
		}
	}
	// The normalised values reach the handler.
	raw, err := builtinToolCall(f, "save_loop", map[string]any{"name": "Monday digest", "trigger": "schedule", "unit": "week", "interval": "2", "weekdays": []any{"Monday", "fri"}})
	if err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &receipt)
	var loop domain.Loop
	for _, item := range f.repository.Bootstrap().Loops {
		if item.ID == receipt.ID {
			loop = item
		}
	}
	if days := fmt.Sprint(loop.TriggerConfig["weekdays"]); days != "[mon fri]" || fmt.Sprint(loop.TriggerConfig["interval"]) != "2" {
		t.Fatalf("saved schedule = %v (receipt %s)", loop.TriggerConfig, raw)
	}
	raw, err = builtinToolCall(f, "save_issue", map[string]any{"title": "Quoted priority", "team": f.data.Teams[0].Key, "priority": "2"})
	if err != nil {
		t.Fatal(err)
	}
	var issue struct {
		Priority float64 `json:"priority"`
	}
	if json.Unmarshal(raw, &issue) != nil || issue.Priority != 2 {
		t.Fatalf("quoted priority was not applied: %s", raw)
	}
}

func TestNaturalLanguageToolArgumentsAreNormalised(t *testing.T) {
	cases := []struct {
		tool string
		args map[string]any
		want map[string]any
	}{
		{"save_issue", map[string]any{"title": "x", "team": "ENG", "priority": "高", "state": "进行中", "dueDate": "明天"}, map[string]any{"priority": float64(2), "state": "started"}},
		{"save_project", map[string]any{"name": "x", "setTeams": []any{"ENG"}, "priority": "紧急", "state": "已完成", "targetDate": "in 2 weeks"}, map[string]any{"priority": float64(1), "state": "completed"}},
		{"save_view", map[string]any{"name": "x", "filters": []any{map[string]any{"field": "priority", "values": []any{"高"}}, map[string]any{"field": "status", "values": []any{"进行中"}}}}, map[string]any{}},
		{"save_loop", map[string]any{"name": "x", "event": "labels", "value": "完成"}, map[string]any{"value": "完成"}},
		{"save_loop", map[string]any{"name": "x", "event": "status", "value": "进行中"}, map[string]any{"value": "started"}},
	}
	for _, item := range cases {
		tool, ok := flowToolByName(item.tool)
		if !ok {
			t.Fatalf("missing tool %s", item.tool)
		}
		if err := checkFlowToolArgs(tool, item.args); err != nil {
			t.Fatalf("%s rejected natural arguments: %v", item.tool, err)
		}
		for key, want := range item.want {
			if item.args[key] != want {
				t.Errorf("%s %s = %#v, want %#v", item.tool, key, item.args[key], want)
			}
		}
	}
}

// A save_document patch passed as one object is refused on the built-in path before anything runs, with the
// shape it needs, and the document keeps its version.
func TestBuiltinRefusesObjectPatchBeforeSaving(t *testing.T) {
	f := newContractPaths(t)
	raw, err := builtinToolCall(f, "save_document", map[string]any{"title": "Notes", "content": "Start"})
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &created)
	version := func() int64 {
		for _, document := range f.repository.Bootstrap().Documents {
			if document.ID == created.ID {
				return document.Version
			}
		}
		t.Fatalf("document %s not found", created.ID)
		return 0
	}
	before := version()
	_, err = builtinToolCall(f, "save_document", map[string]any{"id": created.ID, "patch": map[string]any{"op": "append", "text": "More"}})
	if err == nil || !strings.HasPrefix(err.Error(), "Invalid arguments for save_document: ") || !strings.Contains(err.Error(), mcpPatchShapeHint) || !strings.Contains(err.Error(), "/properties/patch") {
		t.Fatalf("object patch error = %v", err)
	}
	if after := version(); after != before {
		t.Fatalf("refused patch changed the document version %d -> %d", before, after)
	}
}

// A loop run whose model sends a bad argument gets the tool error, and its corrected call goes through.
func TestLoopRunRecoversFromInvalidToolArguments(t *testing.T) {
	provider := &fakeLoopProvider{}
	srv, handler := newReliableLoopServer(t, provider)
	team := srv.store.Bootstrap().Teams[0].Key
	before := len(srv.store.Bootstrap().Issues)
	provider.script(
		`tool:save_issue {"title":"Weekly bug digest","team":"`+team+`","labels":"Bug","assigneeId":"me"}`,
		`tool:save_issue {"title":"Weekly bug digest","team":"`+team+`"}`,
		`tool:finish_run {"status":"done","summary":"Created the weekly bug digest issue."}`,
	)
	loop, started := startTestRun(t, handler, "Digest", "Create an issue titled \"Weekly bug digest\".", nil)
	run := waitForRun(t, handler, loop.ID, started.ID)
	if len(run.ToolCalls) < 2 || run.ToolCalls[0].Status != "error" || !strings.HasPrefix(run.ToolCalls[0].Error, "Invalid arguments for save_issue: ") || run.ToolCalls[1].Status != "completed" {
		t.Fatalf("tool calls = %+v", run.ToolCalls)
	}
	if !strings.Contains(provider.inputs[1], "Invalid arguments for save_issue") {
		t.Fatal("the model did not read the argument error")
	}
	if run.Status != "completed" || len(srv.store.Bootstrap().Issues) != before+1 {
		t.Fatalf("run %s (%s), %d issues created", run.Status, run.Error, len(srv.store.Bootstrap().Issues)-before)
	}
}

// A chat reply whose model sends a bad argument gets the tool error, and its corrected call goes through.
func TestChatRecoversFromInvalidToolArguments(t *testing.T) {
	chat := newFileToolChat(t)
	team := chat.srv.store.Bootstrap().Teams[0].Key
	before := len(chat.srv.store.Bootstrap().Issues)
	chat.provider.replies = []string{
		`tool:save_issue {"title":"Fix login","team":"` + team + `","priority":"urgent"}`,
		`tool:save_issue {"title":"Fix login","team":"` + team + `","priority":1}`,
		"Created the issue.",
	}
	session := chat.ask(t, "Create an issue titled Fix login with urgent priority")
	calls := toolCalls(session)
	if len(calls) != 2 || calls[0].Status != "failed" && calls[0].Status != "error" {
		t.Fatalf("tool calls = %+v", calls)
	}
	if inputs := chat.inputs(); len(inputs) < 2 || !strings.Contains(inputs[1], "Invalid arguments for save_issue") {
		t.Fatal("the model did not read the argument error")
	}
	if got := len(chat.srv.store.Bootstrap().Issues); got != before+1 {
		t.Fatalf("%d issues created", got-before)
	}
}
