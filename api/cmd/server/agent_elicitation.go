package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type agentElicitation struct {
	Workspace, SessionID, UserID string
	Schema                       json.RawMessage
	Mode                         string
	Decision                     chan mcp.ElicitResult
}

func (s *server) resolveAgentElicitation(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var input mcp.ElicitResult
	if !decodeJSON(w, r, &input) {
		return
	}
	if !slices.Contains([]string{"accept", "decline", "cancel"}, input.Action) {
		writeError(w, 400, "Invalid response action")
		return
	}
	data := s.workspaceData(r)
	if _, err := ownedAgentSession(&data, r.PathValue("id")); err != nil {
		writeError(w, 404, "Elicitation not found")
		return
	}
	s.agentApprovalsMu.Lock()
	defer s.agentApprovalsMu.Unlock()
	id := r.PathValue("elicitationId")
	pending := s.agentElicitations[id]
	if pending == nil || pending.Workspace != workspaceKey(r) || pending.SessionID != r.PathValue("id") || pending.UserID != data.Viewer.ID {
		writeError(w, 404, "Elicitation expired or not found")
		return
	}
	if input.Action == "accept" && pending.Mode == "form" {
		var schema jsonschema.Schema
		if json.Unmarshal(pending.Schema, &schema) != nil {
			writeError(w, 400, "Invalid form schema")
			return
		}
		resolved, err := schema.Resolve(nil)
		if err != nil {
			writeError(w, 400, "Invalid form schema")
			return
		}
		if err = resolved.Validate(input.Content); err != nil {
			writeError(w, 400, "Response does not satisfy the requested form")
			return
		}
	} else {
		input.Content = nil
	}
	delete(s.agentElicitations, id)
	pending.Decision <- input
	writeJSON(w, 200, map[string]string{"action": input.Action})
}

func (s *server) requestAgentElicitation(ctx context.Context, workspace, sessionID, userID string, item applicationPolicy, params *mcp.ElicitParams, emit func(domain.AgentMessagePart, string) error) (*mcp.ElicitResult, error) {
	if params == nil || len(params.Message) > 8000 {
		return nil, errInvalid
	}
	mode := params.Mode
	if mode == "" {
		mode = "form"
	}
	if mode == "url" && !integrationEndpointSafe(ctx, params.URL, s.authDisabled) {
		return nil, fmt.Errorf("Invalid external confirmation URL")
	}
	schema, _ := json.Marshal(params.RequestedSchema)
	if len(schema) > 32<<10 {
		return nil, fmt.Errorf("Elicitation schema exceeds limit")
	}
	if mode == "form" {
		var shape struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		_ = json.Unmarshal(schema, &shape)
		if len(shape.Properties) > 32 {
			return nil, fmt.Errorf("Too many elicitation fields")
		}
		for key, raw := range shape.Properties {
			var field struct {
				Format string `json:"format"`
			}
			_ = json.Unmarshal(raw, &field)
			lower := strings.ToLower(key)
			if field.Format == "password" || strings.Contains(lower, "password") || strings.Contains(lower, "secret") || strings.Contains(lower, "access_token") || strings.Contains(lower, "api_key") {
				return nil, fmt.Errorf("Credentials must be requested through external URL confirmation")
			}
		}
	}
	id, err := randomSecret("elicit_")
	if err != nil {
		return nil, err
	}
	pending := &agentElicitation{Workspace: workspace, SessionID: sessionID, UserID: userID, Schema: schema, Mode: mode, Decision: make(chan mcp.ElicitResult, 1)}
	s.agentApprovalsMu.Lock()
	if s.agentElicitations == nil {
		s.agentElicitations = map[string]*agentElicitation{}
	}
	s.agentElicitations[id] = pending
	s.agentApprovalsMu.Unlock()
	defer func() { s.agentApprovalsMu.Lock(); delete(s.agentElicitations, id); s.agentApprovalsMu.Unlock() }()
	prompt := &domain.AgentElicitation{ID: id, SessionID: sessionID, ConnectorName: item.Name, ConnectorURL: item.URL, Mode: mode, Message: params.Message, Schema: schema, URL: params.URL}
	part := domain.AgentMessagePart{ID: id, Type: "elicitation", Status: "pending", Elicitation: prompt}
	if err := emit(part, "elicitation.requested"); err != nil {
		return nil, err
	}
	result := mcp.ElicitResult{Action: "cancel"}
	timer := time.NewTimer(5 * time.Minute)
	defer timer.Stop()
	select {
	case result = <-pending.Decision:
	case <-ctx.Done():
	case <-timer.C:
	}
	prompt.Action = result.Action
	part.Status = "completed"
	if result.Action == "cancel" {
		part.Status = "error"
	}
	if err := emit(part, "elicitation.resolved"); err != nil {
		return nil, err
	}
	// Form answers go to the requesting server, never into the stored conversation.
	return &result, nil
}
