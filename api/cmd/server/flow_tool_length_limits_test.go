package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// flowToolProperties returns a Flow tool's top-level input properties as
// published.
func flowToolProperties(t *testing.T, tool flowMCPTool) (map[string]map[string]any, []string) {
	t.Helper()
	var schema struct {
		Required   []string                  `json:"required"`
		Properties map[string]map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	return schema.Properties, schema.Required
}

// The tool schemas advertise exactly the server's text limits
// (flowToolLengthLimits, built from the validators' constants) as maxLength.
func TestFlowToolSchemasAdvertiseServerLengthLimits(t *testing.T) {
	for name, fields := range flowToolLengthLimits {
		tool, ok := flowToolByName(name)
		if !ok {
			t.Fatalf("tool %s not found", name)
		}
		properties, _ := flowToolProperties(t, tool)
		for field, limit := range fields {
			property, ok := properties[field]
			if !ok {
				t.Fatalf("%s has no %s argument", name, field)
			}
			if got, _ := property["maxLength"].(float64); int(got) != limit {
				t.Errorf("%s.%s maxLength = %v, want %d (the server limit)", name, field, property["maxLength"], limit)
			}
		}
	}
	// No schema advertises a limit the server does not have.
	for _, tool := range flowToolContract() {
		properties, _ := flowToolProperties(t, tool)
		for field, property := range properties {
			if _, has := property["maxLength"]; has && flowToolLengthLimits[tool.Name][field] == 0 {
				t.Errorf("%s.%s advertises maxLength %v but flowToolLengthLimits has no limit for it", tool.Name, field, property["maxLength"])
			}
		}
	}
	if flowToolLengthLimits["save_issue"]["title"] != domain.IssueTitleMaxLength || flowToolLengthLimits["prepare_attachment_upload"]["title"] != domain.UploadTitleMaxLength || flowToolLengthLimits["save_team"]["key"] != teamKeyMaxLength {
		t.Fatal("flowToolLengthLimits no longer uses the validators' constants")
	}
}

// The shared schema check (/mcp and the built-in agent) refuses a value one
// character over the limit, naming the field, and accepts the limit itself;
// lengths are characters, as the validators count them.
func TestFlowToolSchemaRefusesOverlongText(t *testing.T) {
	for name, fields := range flowToolLengthLimits {
		tool, _ := flowToolByName(name)
		properties, required := flowToolProperties(t, tool)
		for field, limit := range fields {
			args := map[string]any{}
			for _, key := range required {
				args[key] = flowToolSampleValue(properties[key])
			}
			args[field] = strings.Repeat("界", limit)
			if err := checkFlowToolArgs(tool, args); err != nil {
				t.Errorf("%s.%s at the limit refused: %v", name, field, err)
			}
			args[field] = strings.Repeat("界", limit+1)
			err := checkFlowToolArgs(tool, args)
			if err == nil || !strings.Contains(err.Error(), field) || !strings.Contains(err.Error(), "Invalid arguments for "+name) {
				t.Errorf("%s.%s over the limit = %v", name, field, err)
			}
		}
	}
}

func flowToolSampleValue(property map[string]any) any {
	if values, ok := property["enum"].([]any); ok && len(values) > 0 {
		return values[0]
	}
	kind, _ := property["type"].(string)
	if kinds, ok := property["type"].([]any); ok && len(kinds) > 0 {
		kind, _ = kinds[0].(string)
	}
	switch kind {
	case "integer", "number":
		return float64(1)
	case "boolean":
		return true
	case "array":
		return []any{}
	case "object":
		return map[string]any{}
	}
	return "x"
}

// End to end through the built-in agent's tool path: an over-long issue
// title is the schema error and nothing is saved.
func TestAgentToolOverlongTitleIsSchemaError(t *testing.T) {
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "limits.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	service := &server{store: repo, uploadPath: t.TempDir(), authDisabled: true}
	service.agent.ToolsEnabled, service.agent.WriteTools = true, true
	data := repo.Bootstrap()
	before := len(data.Issues)
	ctx := context.WithValue(context.Background(), authUserContextKey{}, data.Viewer)
	ctx = store.ContextWithActor(ctx, data.Viewer)
	request := httptest.NewRequest(http.MethodPost, "/api/agent?workspace="+data.Workspace.URLKey, nil).WithContext(ctx)
	raw, _ := json.Marshal(map[string]any{"title": strings.Repeat("a", domain.IssueTitleMaxLength+1), "team": data.Teams[0].ID})
	_, err = service.executeAgentTool(request, data, domain.AgentToolCall{Name: "save_issue", Arguments: raw})
	if err == nil || !strings.Contains(err.Error(), "Invalid arguments for save_issue") || !strings.Contains(err.Error(), "title") {
		t.Fatalf("save_issue with a long title = %v", err)
	}
	if after := len(repo.Bootstrap().Issues); after != before {
		t.Fatalf("issues %d -> %d", before, after)
	}
}
