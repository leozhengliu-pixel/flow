package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

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
			if got, _ := property["maxLength"].(float64); int(got) != limit.max {
				t.Errorf("%s.%s maxLength = %v, want %d (the server limit)", name, field, property["maxLength"], limit.max)
			}
		}
	}
	// No schema advertises a limit the server does not have.
	for _, tool := range flowToolContract() {
		properties, _ := flowToolProperties(t, tool)
		for field, property := range properties {
			if _, has := property["maxLength"]; has && flowToolLengthLimits[tool.Name][field].max == 0 {
				t.Errorf("%s.%s advertises maxLength %v but flowToolLengthLimits has no limit for it", tool.Name, field, property["maxLength"])
			}
		}
	}
	if flowToolLengthLimits["save_issue"]["title"].max != domain.IssueTitleMaxLength || flowToolLengthLimits["save_issue"]["icon"].max != issueIconMaxLength || flowToolLengthLimits["prepare_attachment_upload"]["title"].max != domain.UploadTitleMaxLength || flowToolLengthLimits["prepare_attachment_upload"]["contentType"].max != uploadContentTypeMaxLength || flowToolLengthLimits["save_team"]["key"].max != teamKeyMaxLength {
		t.Fatal("flowToolLengthLimits no longer uses the validators' constants")
	}
}

// The shared schema check (/mcp and the built-in agent) and the server's own
// check both count characters: a multi-byte value at the limit passes both,
// one character over fails both, the schema error naming the field.
func TestFlowToolSchemaRefusesOverlongText(t *testing.T) {
	for name, fields := range flowToolLengthLimits {
		tool, _ := flowToolByName(name)
		properties, required := flowToolProperties(t, tool)
		for field, limit := range fields {
			args := map[string]any{}
			for _, key := range required {
				args[key] = flowToolSampleValue(properties[key])
			}
			atLimit, over := strings.Repeat("界", limit.max), strings.Repeat("界", limit.max+1)
			args[field] = atLimit
			if err := checkFlowToolArgs(tool, args); err != nil {
				t.Errorf("%s.%s at the limit refused by the schema: %v", name, field, err)
			}
			args[field] = over
			err := checkFlowToolArgs(tool, args)
			if err == nil || !strings.Contains(err.Error(), field) || !strings.Contains(err.Error(), "Invalid arguments for "+name) {
				t.Errorf("%s.%s over the limit = %v", name, field, err)
			}
			if limit.check == nil {
				continue
			}
			if err := limit.check(atLimit); err != nil {
				t.Errorf("%s.%s at the limit (%d characters, %d bytes) refused by the server: %v", name, field, limit.max, len(atLimit), err)
			}
			if err := limit.check(over); err == nil {
				t.Errorf("%s.%s one character over accepted by the server", name, field)
			}
		}
	}
}

// The REST path runs the same checks on multi-byte values: an icon of 191
// characters (573 bytes) is saved and 192 is a 400; a staged upload whose
// media type is 255 characters with a non-ASCII parameter is prepared and one
// over is refused.
func TestMultiByteIconAndContentTypeAtTheLimit(t *testing.T) {
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "limits.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	service := &server{store: repo, uploadPath: t.TempDir(), authDisabled: true}
	host := httptest.NewServer(newHandler(service))
	defer host.Close()
	data := repo.Bootstrap()
	issue := data.Issues[0]
	send := func(method, path string, body any) (int, string) {
		raw, _ := json.Marshal(body)
		request, _ := http.NewRequest(method, host.URL+path, bytes.NewReader(raw))
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		text, _ := io.ReadAll(response.Body)
		return response.StatusCode, string(text)
	}
	if status, body := send(http.MethodPatch, "/api/issues/"+issue.ID, map[string]any{"icon": strings.Repeat("界", issueIconMaxLength)}); status != http.StatusOK {
		t.Fatalf("icon at the limit = %d %s", status, body)
	}
	if status, body := send(http.MethodPatch, "/api/issues/"+issue.ID, map[string]any{"icon": strings.Repeat("界", issueIconMaxLength+1)}); status != http.StatusBadRequest || !strings.Contains(body, "icon must be 191 characters or fewer") {
		t.Fatalf("icon over the limit = %d %s", status, body)
	}
	prefix := `text/plain; name="`
	contentType := func(length int) string {
		return prefix + strings.Repeat("界", length-len(prefix)-1) + `"`
	}
	if got := utf8.RuneCountInString(contentType(uploadContentTypeMaxLength)); got != uploadContentTypeMaxLength {
		t.Fatalf("content type length %d", got)
	}
	upload := func(contentType string) (int, string) {
		return send(http.MethodPost, "/api/files?workspace="+data.Workspace.URLKey, map[string]any{"filename": "notes.txt", "contentType": contentType, "size": 4})
	}
	if status, body := upload(contentType(uploadContentTypeMaxLength)); status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("content type at the limit = %d %s", status, body)
	}
	if status, body := upload(contentType(uploadContentTypeMaxLength + 1)); status != http.StatusBadRequest {
		t.Fatalf("content type over the limit = %d %s", status, body)
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
