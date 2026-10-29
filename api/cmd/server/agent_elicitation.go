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
	session, err := ownedAgentSession(&data, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, "Elicitation not found")
		return
	}
	id := r.PathValue("elicitationId")
	found, message := s.answerAgentElicitation(workspaceKey(r), session.ID, data.Viewer.ID, id, input)
	if found {
		if message != "" {
			writeError(w, 400, message)
			return
		}
		writeJSON(w, 200, map[string]string{"action": input.Action})
		return
	}
	// The reply may be running on another server: its saved copy shows the question waiting (agent_live_answer.go).
	if part, waiting := s.storedAgentLivePart(r, *session, id); waiting && part.Elicitation != nil && s.coordinator != nil {
		if message := validAgentElicitationAnswer(part.Elicitation.Mode, part.Elicitation.Schema, &input); message != "" {
			writeError(w, 400, message)
			return
		}
		if s.forwardAgentAnswer(r, agentForwardedAnswer{Kind: "elicitation", ID: id, SessionID: session.ID, UserID: data.Viewer.ID, Elicitation: &input}) {
			writeJSON(w, http.StatusAccepted, map[string]string{"action": input.Action})
			return
		}
		writeError(w, http.StatusConflict, agentAnswerElsewhereError)
		return
	}
	writeError(w, 404, "Elicitation expired or not found")
}

// answerAgentElicitation hands input to the run in this process waiting on elicitation id. found is false when none
// is; message is the validation error that kept the answer from it.
func (s *server) answerAgentElicitation(workspace, sessionID, userID, id string, input mcp.ElicitResult) (found bool, message string) {
	s.agentApprovalsMu.Lock()
	defer s.agentApprovalsMu.Unlock()
	pending := s.agentElicitations[id]
	if pending == nil || pending.Workspace != workspace || pending.SessionID != sessionID || pending.UserID != userID {
		return false, ""
	}
	if message := validAgentElicitationAnswer(pending.Mode, pending.Schema, &input); message != "" {
		return true, message
	}
	delete(s.agentElicitations, id)
	pending.Decision <- input
	return true, ""
}

// validAgentElicitationAnswer checks an accepted form answer against its schema (and drops content from any other
// answer); it returns the error to show, or "".
func validAgentElicitationAnswer(mode string, schemaJSON json.RawMessage, input *mcp.ElicitResult) string {
	if input.Action != "accept" || mode != "form" {
		input.Content = nil
		return ""
	}
	var schema jsonschema.Schema
	if json.Unmarshal(schemaJSON, &schema) != nil {
		return "Invalid form schema"
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		return "Invalid form schema"
	}
	if err = resolved.Validate(input.Content); err != nil {
		return "Response does not satisfy the requested form"
	}
	return ""
}

// agentElicitationContext is what a chat reply's connector question waits under: the connector call's context (the
// MCP SDK hands the handler its connection's, which a Stop does not end) and the reply's run, so stopping a reply
// that waits on a question cancels the question instead of leaving it running until the question times out.
func agentElicitationContext(call, run context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(call)
	stop := context.AfterFunc(run, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}

func (s *server) requestAgentElicitation(ctx context.Context, workspace, sessionID, userID string, item applicationPolicy, params *mcp.ElicitParams, emit func(domain.AgentMessagePart, string) error) (*mcp.ElicitResult, error) {
	if params == nil || len(params.Message) > 8000 {
		return nil, errInvalid
	}
	mode := params.Mode
	if mode == "" {
		mode = "form"
	}
	if mode == "url" && !integrationEndpointSafe(ctx, params.URL, s.authDisabled, outboundTrustedPrivate) {
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
	// The answered question is a new value: the asked one was handed to emit, which may keep it (a saved reply, a
	// cached copy a later save is compared with), so changing it in place would change what was saved before.
	answered := *prompt
	answered.Action = result.Action
	part.Elicitation = &answered
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
