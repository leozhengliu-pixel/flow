package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

const maxAgentToolTurns = 8

type agentStreamEvent struct {
	Type       string                   `json:"type"`
	Session    *domain.AgentSession     `json:"session,omitempty"`
	MessageID  string                   `json:"messageId,omitempty"`
	Delta      string                   `json:"delta,omitempty"`
	Part       *domain.AgentMessagePart `json:"part,omitempty"`
	ApprovalID string                   `json:"approvalId,omitempty"`
	Decision   string                   `json:"decision,omitempty"`
	Error      string                   `json:"error,omitempty"`
}

// agentApproval coordinates a pending write tool call with the browser. The
// stream remains open while the user makes a decision, just like the native
// agent client. The channel is buffered so resolving an approval never blocks
// the HTTP handler while the stream is unwinding.
type agentApproval struct {
	WorkspaceKey string
	SessionID    string
	UserID       string
	Decision     chan string
}

type agentApprovalInput struct {
	Decision string `json:"decision"`
}

func (s *server) registerAgentApproval(approvalID string, approval *agentApproval) {
	s.agentApprovalsMu.Lock()
	if s.agentApprovals == nil {
		s.agentApprovals = make(map[string]*agentApproval)
	}
	s.agentApprovals[approvalID] = approval
	s.agentApprovalsMu.Unlock()
}

func (s *server) takeAgentApproval(approvalID string) *agentApproval {
	s.agentApprovalsMu.Lock()
	defer s.agentApprovalsMu.Unlock()
	approval := s.agentApprovals[approvalID]
	if approval != nil {
		delete(s.agentApprovals, approvalID)
	}
	return approval
}

func (s *server) resolveAgentApproval(w http.ResponseWriter, r *http.Request) {
	if !s.requireAgent(w) {
		return
	}
	var input agentApprovalInput
	if !decodeJSON(w, r, &input) {
		return
	}
	decision := strings.ToLower(strings.TrimSpace(input.Decision))
	switch decision {
	case "approve", "approved", "allow":
		decision = "approved"
	case "reject", "rejected", "deny", "decline":
		decision = "rejected"
	default:
		writeError(w, http.StatusBadRequest, "decision must be approve or reject")
		return
	}
	approvalID, sessionID := r.PathValue("approvalId"), r.PathValue("id")
	if approvalID == "" || sessionID == "" {
		writeError(w, http.StatusNotFound, "agent approval not found")
		return
	}
	data := s.workspaceData(r)
	if _, err := ownedAgentSession(&data, sessionID); err != nil {
		writeError(w, http.StatusNotFound, "agent approval not found")
		return
	}
	s.agentApprovalsMu.Lock()
	approval := s.agentApprovals[approvalID]
	if approval == nil || approval.SessionID != sessionID || approval.WorkspaceKey != workspaceKey(r) || approval.UserID != data.Viewer.ID {
		s.agentApprovalsMu.Unlock()
		writeError(w, http.StatusNotFound, "agent approval not found")
		return
	}
	delete(s.agentApprovals, approvalID)
	s.agentApprovalsMu.Unlock()
	approval.Decision <- decision
	writeJSON(w, http.StatusOK, map[string]string{"approvalId": approvalID, "decision": decision})
}

func (s *server) waitForAgentApproval(ctx context.Context, approvalID string, approval *agentApproval) string {
	defer func() {
		// A timed-out/cancelled stream can leave an approval in the map. Removing
		// it here also makes a late browser click a harmless 404.
		s.agentApprovalsMu.Lock()
		if s.agentApprovals != nil {
			delete(s.agentApprovals, approvalID)
		}
		s.agentApprovalsMu.Unlock()
	}()
	timer := time.NewTimer(30 * time.Minute)
	defer timer.Stop()
	select {
	case decision := <-approval.Decision:
		return decision
	case <-ctx.Done():
		return "rejected"
	case <-timer.C:
		return "rejected"
	}
}

type agentEventWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

func newAgentEventWriter(w http.ResponseWriter) (*agentEventWriter, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("streaming is not supported")
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	return &agentEventWriter{w: w, flusher: flusher}, nil
}

func (w *agentEventWriter) send(event agentStreamEvent) error {
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w.w, "event: %s\ndata: %s\n\n", event.Type, raw); err != nil {
		return err
	}
	w.flusher.Flush()
	return nil
}

func (s *server) createAgentSessionStream(w http.ResponseWriter, r *http.Request) {
	if !s.requireAgent(w) {
		return
	}
	var input agentSessionInput
	if !decodeJSON(w, r, &input) {
		return
	}
	var ok bool
	if input.Message, ok = validAgentMessage(w, input.Message); !ok || !validAgentIssueCount(w, input.IssueIDs) {
		return
	}
	if input.Location == "" {
		input.Location = "page"
	}
	if input.Location != "page" && input.Location != "toolbar" {
		writeError(w, http.StatusBadRequest, "location must be page or toolbar")
		return
	}
	session, err := s.beginAgentSession(r, input)
	if err != nil {
		respondMutation(w, err, http.StatusCreated, session)
		return
	}
	s.streamAgentSession(w, r, session.ID)
}

func (s *server) createAgentSessionMessageStream(w http.ResponseWriter, r *http.Request) {
	if !s.requireAgent(w) {
		return
	}
	message, ok := decodeAgentMessage(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if err := s.appendAgentSessionMessage(r, id, message); err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	s.streamAgentSession(w, r, id)
}

func (s *server) updateAgentSessionMessageStream(w http.ResponseWriter, r *http.Request) {
	if !s.requireAgent(w) {
		return
	}
	message, ok := decodeAgentMessage(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if err := s.replaceAgentSessionMessage(r, id, r.PathValue("messageId"), message); err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	s.streamAgentSession(w, r, id)
}

func (s *server) streamAgentSession(w http.ResponseWriter, r *http.Request, id string) {
	writer, err := newAgentEventWriter(w)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	completed, err := s.runAgentSession(r, id, writer)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}
		_ = writer.send(agentStreamEvent{Type: "error", Error: err.Error()})
		return
	}
	_ = writer.send(agentStreamEvent{Type: "session.completed", Session: &completed})
}

func (s *server) runAgentSession(r *http.Request, id string, writer *agentEventWriter) (domain.AgentSession, error) {
	data := s.workspaceData(r)
	if err := agentWorkspacePolicy(data.WorkspaceSettings, data.ViewerRole); err != nil {
		return domain.AgentSession{}, err
	}
	session, err := ownedAgentSession(&data, id)
	if err != nil {
		return domain.AgentSession{}, err
	}
	contextData, err := s.agentIssueContext(r, session.IssueIDs)
	if err != nil {
		return domain.AgentSession{}, err
	}
	data.Issues = contextData.Issues
	issues := selectedAgentIssues(data.Issues, session.IssueIDs)
	skills := selectedAgentSkills(data.AgentSkills, session.SkillIDs, session.UserID)
	messages := agentProviderHistory(*session, workspaceAgentSystemPrompt(data, issues, skills))
	connectors, err := s.discoverConnectorTools(r.Context(), data)
	if err != nil {
		return domain.AgentSession{}, err
	}
	r = r.WithContext(context.WithValue(r.Context(), connectorToolsKey{}, connectors))
	messageID := fmt.Sprintf("agent_message_%d", time.Now().UnixNano())
	started := time.Now()
	parts := []domain.AgentMessagePart{}
	partIndex := map[string]int{}
	partText := map[string]*strings.Builder{}
	streamBytes := 0
	if writer != nil {
		snapshot := *session
		if err := writer.send(agentStreamEvent{Type: "session.started", Session: &snapshot, MessageID: messageID}); err != nil {
			return domain.AgentSession{}, err
		}
	}

	emit := func(event agentProviderEvent) error {
		streamBytes += len(event.Delta)
		if streamBytes > 8<<20 || len(parts) > 1024 {
			return fmt.Errorf("agent response exceeds the stream budget")
		}
		switch event.Type {
		case "text.delta", "reasoning.delta":
			partType := strings.TrimSuffix(event.Type, ".delta")
			index, ok := partIndex[partType]
			if !ok {
				index = len(parts)
				partIndex[partType] = index
				parts = append(parts, domain.AgentMessagePart{ID: fmt.Sprintf("%s_%s", messageID, partType), Type: partType, Status: "running"})
			}
			builder := partText[partType]
			if builder == nil {
				builder = &strings.Builder{}
				partText[partType] = builder
			}
			builder.WriteString(event.Delta)
			parts[index].Text = builder.String()
			if writer != nil {
				part := parts[index]
				if event.Type == "text.delta" {
					part.Text = ""
				}
				return writer.send(agentStreamEvent{Type: event.Type, MessageID: messageID, Delta: event.Delta, Part: &part})
			}
		case "tool.started", "tool.delta":
			if event.ToolCall == nil {
				return nil
			}
			key := "tool:" + event.ToolCall.ID
			index, ok := partIndex[key]
			if !ok {
				index = len(parts)
				partIndex[key] = index
				parts = append(parts, domain.AgentMessagePart{ID: fmt.Sprintf("%s_tool_%d", messageID, index), Type: "toolCall", Status: "running", ToolCall: cloneToolCall(event.ToolCall)})
			} else if event.Type == "tool.started" {
				parts[index].ToolCall = cloneToolCall(event.ToolCall)
			}
			if writer != nil {
				part := parts[index]
				return writer.send(agentStreamEvent{Type: event.Type, MessageID: messageID, Delta: event.Delta, Part: &part})
			}
		}
		return nil
	}

	finalText := ""
	for turnIndex := 0; turnIndex < maxAgentToolTurns; turnIndex++ {
		turn, err := s.requestAgentTurn(r.Context(), messages, emit)
		if err != nil {
			failureRequest := r
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				failureRequest = r.Clone(context.WithoutCancel(r.Context()))
			}
			return s.persistAgentFailure(failureRequest, *session, messageID, finalText, parts, started, err)
		}
		finalText += turn.Text
		if len(turn.ToolCalls) == 0 {
			break
		}
		messages = append(messages, agentProviderMessage{Role: "assistant", Content: turn.Text, ToolCalls: turn.ToolCalls})
		for _, toolCall := range turn.ToolCalls {
			call := toolCall
			if s.agentToolRequiresApproval(call.Name) || strings.HasPrefix(call.Name, "external_") {
				approvalID := fmt.Sprintf("agent_approval_%d", time.Now().UnixNano())
				call.ApprovalID = approvalID
				call.Status = "pending"
				key := "tool:" + call.ID
				index, ok := partIndex[key]
				if !ok {
					index = len(parts)
					partIndex[key] = index
					parts = append(parts, domain.AgentMessagePart{ID: fmt.Sprintf("%s_tool_%d", messageID, index), Type: "toolCall", Status: "pending", ToolCall: cloneToolCall(&call)})
				} else {
					parts[index].Status = "pending"
					parts[index].ToolCall = cloneToolCall(&call)
				}
				approval := &agentApproval{WorkspaceKey: workspaceKey(r), SessionID: session.ID, UserID: session.UserID, Decision: make(chan string, 1)}
				s.registerAgentApproval(approvalID, approval)
				if writer != nil {
					part := parts[index]
					if err := writer.send(agentStreamEvent{Type: "tool.approval_required", MessageID: messageID, ApprovalID: approvalID, Part: &part}); err != nil {
						return domain.AgentSession{}, err
					}
				}
				decision := s.waitForAgentApproval(r.Context(), approvalID, approval)
				if decision != "approved" {
					call.Status = "error"
					call.Error = "Action declined by user"
					call.Result = json.RawMessage(`{"error":"Action declined by user"}`)
					parts[index].Status = "error"
					parts[index].ToolCall = cloneToolCall(&call)
					if writer != nil {
						part := parts[index]
						if err := writer.send(agentStreamEvent{Type: "tool.approval_resolved", MessageID: messageID, ApprovalID: approvalID, Decision: "rejected", Part: &part}); err != nil {
							return domain.AgentSession{}, err
						}
					}
					messages = append(messages, agentProviderMessage{Role: "tool", ToolResult: &agentProviderToolResult{CallID: call.ID, Content: "Action declined by user", IsError: true}})
					continue
				}
				call.Status = "running"
				parts[index].Status = "running"
				parts[index].ToolCall = cloneToolCall(&call)
				if writer != nil {
					part := parts[index]
					if err := writer.send(agentStreamEvent{Type: "tool.approval_resolved", MessageID: messageID, ApprovalID: approvalID, Decision: "approved", Part: &part}); err != nil {
						return domain.AgentSession{}, err
					}
				}
			}
			result, callErr := s.executeAgentTool(r, data, call)
			call.Status = "completed"
			if callErr != nil {
				call.Status, call.Error = "error", callErr.Error()
			}
			call.Result = json.RawMessage(result)
			key := "tool:" + call.ID
			if index, ok := partIndex[key]; ok {
				parts[index].Status = call.Status
				parts[index].ToolCall = cloneToolCall(&call)
			} else {
				partIndex[key] = len(parts)
				parts = append(parts, domain.AgentMessagePart{ID: fmt.Sprintf("%s_tool_%d", messageID, len(parts)), Type: "toolCall", Status: call.Status, ToolCall: cloneToolCall(&call)})
			}
			if writer != nil {
				part := parts[partIndex[key]]
				if err := writer.send(agentStreamEvent{Type: "tool.completed", MessageID: messageID, Part: &part}); err != nil {
					return domain.AgentSession{}, err
				}
			}
			content := string(result)
			if callErr != nil {
				content = callErr.Error()
			}
			messages = append(messages, agentProviderMessage{Role: "tool", ToolResult: &agentProviderToolResult{CallID: call.ID, Content: content, IsError: callErr != nil}})
		}
		if turnIndex == maxAgentToolTurns-1 {
			return s.persistAgentFailure(r, *session, messageID, finalText, parts, started, fmt.Errorf("Flow Agent exceeded the tool turn limit"))
		}
	}
	for index := range parts {
		if parts[index].Status == "running" {
			parts[index].Status = "completed"
		}
	}
	if strings.TrimSpace(finalText) == "" {
		return s.persistAgentFailure(r, *session, messageID, finalText, parts, started, fmt.Errorf("Flow Agent provider returned an empty response"))
	}
	return s.persistAgentCompletion(r, *session, domain.AgentMessage{ID: messageID, Role: "assistant", Content: strings.TrimSpace(finalText), Parts: parts, DurationMS: time.Since(started).Milliseconds(), CreatedAt: time.Now().UTC()})
}

func (s *server) agentToolRequiresApproval(name string) bool {
	if !s.agent.WriteTools || !s.agent.ToolsEnabled {
		return false
	}
	var inventory []flowMCPTool
	if err := json.Unmarshal(flowMCPToolInventory, &inventory); err != nil {
		return false
	}
	name = strings.TrimPrefix(name, "mcp__flow.")
	for _, item := range inventory {
		if strings.TrimPrefix(item.Name, "mcp__flow.") == name {
			return item.Access == "write"
		}
	}
	return false
}

func (s *server) executeAgentTool(r *http.Request, data domain.Bootstrap, call domain.AgentToolCall) ([]byte, error) {
	if s.store != nil {
		fresh, ok := s.store.WorkspaceMetadata(workspaceKey(r))
		if !ok {
			return nil, errNotFound
		}
		role := data.ViewerRole
		if !s.authDisabled {
			var status string
			var err error
			role, status, err = s.store.WorkspaceRole(r.Context(), fresh.Workspace.ID, data.Viewer.ID)
			if err != nil || status != "active" {
				return nil, fmt.Errorf("Workspace access was revoked")
			}
		}
		if err := agentWorkspacePolicy(fresh.WorkspaceSettings, role); err != nil {
			return nil, err
		}
	}
	if !s.agent.ToolsEnabled {
		return nil, fmt.Errorf("Agent tools are disabled")
	}
	if strings.HasPrefix(call.Name, "external_") {
		return s.executeConnectorTool(r, data, call)
	}
	definitions, err := s.agentToolDefinitions()
	if err != nil {
		return nil, err
	}
	allowed := false
	for _, definition := range definitions {
		if definition.Name == strings.TrimPrefix(call.Name, "mcp__flow.") {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, fmt.Errorf("Agent tool is not enabled: %s", call.Name)
	}
	var args map[string]any
	if len(call.Arguments) > 0 {
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments for %s", call.Name)
		}
	}
	if args == nil {
		args = map[string]any{}
	}
	args["__flowBaseURL"] = externalBaseURL(r)
	actor := mcpActor{WorkspaceKey: workspaceKey(r), User: data.Viewer, APIKey: domain.APIKey{Scopes: []string{"read", "write"}}}
	ctx := context.WithValue(r.Context(), authUserContextKey{}, data.Viewer)
	ctx = context.WithValue(ctx, workspaceKeyContextKey{}, workspaceKey(r))
	ctx = store.ContextWithActor(ctx, data.Viewer)
	result, err := s.callFlowTool(ctx, actor, call.Name, args)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("could not encode result for %s", call.Name)
	}
	if len(raw) > 128<<10 {
		raw, _ = json.Marshal(map[string]any{"truncated": true, "preview": string(raw[:128<<10])})
	}
	return raw, nil
}

func agentProviderHistory(session domain.AgentSession, system string) []agentProviderMessage {
	messages := []agentProviderMessage{{Role: "system", Content: system}}
	start := len(session.Messages)
	bytes := len(system)
	for start > 0 && len(session.Messages)-start < 32 {
		raw, _ := json.Marshal(session.Messages[start-1])
		if start < len(session.Messages) && bytes+len(raw) > 8<<20 {
			break
		}
		bytes += len(raw)
		start--
	}
	for _, message := range session.Messages[start:] {
		providerMessage := agentProviderMessage{Role: message.Role, Content: message.Content}
		for _, part := range message.Parts {
			if part.ToolCall != nil {
				providerMessage.ToolCalls = append(providerMessage.ToolCalls, *part.ToolCall)
			}
		}
		messages = append(messages, providerMessage)
		for _, part := range message.Parts {
			if part.ToolCall == nil || len(part.ToolCall.Result) == 0 {
				continue
			}
			messages = append(messages, agentProviderMessage{Role: "tool", ToolResult: &agentProviderToolResult{CallID: part.ToolCall.ID, Content: string(part.ToolCall.Result), IsError: part.ToolCall.Status == "error"}})
		}
	}
	if len(messages) > 41 {
		start := len(messages) - 40
		for start < len(messages) && messages[start].ToolResult != nil {
			start++
		}
		messages = append(messages[:1], messages[start:]...)
	}
	return messages
}

func (s *server) persistAgentCompletion(r *http.Request, session domain.AgentSession, message domain.AgentMessage) (domain.AgentSession, error) {
	var completed domain.AgentSession
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "agent.message_completed", session.ID, nil, func(data *domain.Bootstrap) error {
		current, err := ownedAgentSession(data, session.ID)
		if err != nil {
			return err
		}
		current.Messages = append(current.Messages, message)
		data.AgentActivities = append(data.AgentActivities, domain.AgentActivity{ID: fmt.Sprintf("agent_activity_%d", message.CreatedAt.UnixNano()), SessionID: session.ID, Type: "response", Status: "completed", Body: message.Content, Metadata: map[string]any{"durationMs": message.DurationMS, "partCount": len(message.Parts)}, CreatedAt: message.CreatedAt, UpdatedAt: message.CreatedAt})
		current.UpdatedAt = message.CreatedAt
		completed = *current
		return nil
	})
	return completed, err
}

func (s *server) persistAgentFailure(r *http.Request, session domain.AgentSession, messageID, text string, parts []domain.AgentMessagePart, started time.Time, cause error) (domain.AgentSession, error) {
	for index := range parts {
		if parts[index].Status == "running" {
			parts[index].Status = "error"
		}
	}
	parts = append(parts, domain.AgentMessagePart{ID: messageID + "_error", Type: "error", Text: cause.Error(), Status: "error"})
	if strings.TrimSpace(text) != "" || len(parts) > 1 {
		_, _ = s.persistAgentCompletion(r, session, domain.AgentMessage{ID: messageID, Role: "assistant", Content: strings.TrimSpace(text), Parts: parts, DurationMS: time.Since(started).Milliseconds(), CreatedAt: time.Now().UTC()})
	}
	return domain.AgentSession{}, cause
}
