package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
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
	// detached: the client went away but the run continues (loop builder
	// sessions navigate to the new loop mid-turn); later events are dropped.
	detached bool
	lenient  bool
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
	if w.detached {
		return nil
	}
	if _, err := fmt.Fprintf(w.w, "event: %s\ndata: %s\n\n", event.Type, raw); err != nil {
		if w.lenient {
			w.detached = true
			return nil
		}
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
	if input.Message, ok = validAgentMessage(w, input.Message); !ok || !validAgentMentionCounts(w, input.IssueIDs, input.ProjectIDs, input.DocumentIDs) || !validAgentMentionList(w, input.Mentions, input.UserIDs) {
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
	input, ok := s.decodeAgentMessageInput(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if err := s.appendAgentSessionMessage(r, id, input); err != nil {
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
	// Like Linear, a reply keeps going when the page that asked goes away
	// (navigating, reloading, closing the tab) and is saved when it finishes;
	// only an explicit stop (POST …/stop) ends it early.
	runCtx, cancelRun := context.WithCancel(context.WithoutCancel(r.Context()))
	defer cancelRun()
	release := s.registerAgentRun(session.ID, cancelRun)
	defer release()
	r = r.WithContext(runCtx)
	if writer != nil {
		writer.lenient = true
	}
	contextData, err := s.agentIssueContext(r, session.IssueIDs)
	if err != nil {
		return domain.AgentSession{}, err
	}
	data.Issues = contextData.Issues
	issues := selectedAgentIssues(data.Issues, session.IssueIDs)
	skills := selectedAgentSkills(data.AgentSkills, session.SkillIDs, session.UserID)
	mentions := agentMentionPrompt(selectedAgentProjects(data.Projects, session.ProjectIDs), selectedAgentDocuments(data.Documents, session.DocumentIDs), selectedAgentUsers(data.Users, session.UserIDs))
	webNote := ""
	if data.WorkspaceSettings.AgentWebSearch && s.webSearchAvailable() {
		r = r.WithContext(withAgentWebTools(r.Context()))
		webNote = agentWebSearchNote
	}
	// Resources the request names in plain text are loaded up front, so a simple question needs one model call.
	refs := findAgentMessageReferences(data, agentLatestUserMessage(*session))
	contextIssues := append(slices.Clone(issues), s.agentReferencedIssues(r, refs.IssueIdentifiers, issues)...)
	contextMentions := agentMentionPrompt(mergeAgentProjects(selectedAgentProjects(data.Projects, session.ProjectIDs), refs.Projects), mergeAgentDocuments(selectedAgentDocuments(data.Documents, session.DocumentIDs), refs.Documents), selectedAgentUsers(data.Users, session.UserIDs))
	// Chat offers a tool subset picked for this request (see agent_chat_tools.go) and shares a prompt cache per user.
	r = r.WithContext(withAgentChatToolset(r.Context(), newAgentChatToolset(*session)))
	r = r.WithContext(context.WithValue(r.Context(), agentPromptCacheKey{}, agentChatCacheKey(workspaceKey(r), data.Viewer.ID)))
	system := buildWorkspaceAgentPrompt(data, contextIssues, skills, agentChatToolRule, s.agentWriteAccessNote()+webNote, contextMentions+loopBuilderPrompt(data, *session))
	messages := agentProviderHistory(*session, system)
	s.addLoopAttachmentInputs(r.Context(), data, *session, messages)
	s.rememberAgentOrigin(r)
	titleDone := s.startAgentSessionTitle(r, *session, mentions)
	messageID := fmt.Sprintf("agent_message_%d", time.Now().UnixNano())
	started := time.Now()
	parts := []domain.AgentMessagePart{}
	partIndex := map[string]int{}
	partText := map[string]*strings.Builder{}
	streamBytes := 0
	var elicitMu sync.Mutex
	connectorContext := connectorRequestContext{Workspace: workspaceKey(r), UserID: data.Viewer.ID}
	if writer != nil {
		connectorContext.Elicit = func(ctx context.Context, item applicationPolicy, params *mcp.ElicitParams) (*mcp.ElicitResult, error) {
			elicitMu.Lock()
			defer elicitMu.Unlock()
			return s.requestAgentElicitation(ctx, workspaceKey(r), session.ID, session.UserID, item, params, func(part domain.AgentMessagePart, kind string) error {
				index, found := partIndex[part.ID]
				if found {
					parts[index] = part
				} else {
					partIndex[part.ID] = len(parts)
					parts = append(parts, part)
				}
				return writer.send(agentStreamEvent{Type: kind, MessageID: messageID, Part: &part})
			})
		}
	}
	r = r.WithContext(context.WithValue(r.Context(), connectorContextKey{}, connectorContext))
	if writer != nil && sessionHasDraftLoop(data, *session) {
		r = r.WithContext(context.WithValue(r.Context(), agentExtraToolsKey{}, []agentProviderTool{loopQuestionToolDefinition}))
	}
	if writer != nil {
		snapshot := *session
		if err := writer.send(agentStreamEvent{Type: "session.started", Session: &snapshot, MessageID: messageID}); err != nil {
			return domain.AgentSession{}, err
		}
	}

	connectors, err := s.discoverConnectorTools(r.Context(), data)
	if err != nil {
		return s.persistAgentFailure(r, *session, messageID, "", parts, started, err)
	}
	r = r.WithContext(context.WithValue(r.Context(), connectorToolsKey{}, connectors))
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
			// load_tools is bookkeeping, not work the user needs to see.
			if event.ToolCall == nil || event.ToolCall.Name == agentLoadToolsTool {
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
	seenNarration := map[string]bool{}
	for turnIndex := 0; turnIndex < maxAgentToolTurns; turnIndex++ {
		turnStart := len(parts)
		// The loop builder's intro and later replies are separate paragraphs.
		if len(session.LoopIDs) > 0 && strings.TrimSpace(finalText) != "" && !strings.HasSuffix(finalText, "\n\n") {
			if err := emit(agentProviderEvent{Type: "text.delta", Delta: "\n\n"}); err == nil {
				finalText += "\n\n"
			}
		}
		turn, err := s.requestAgentChatTurn(r.Context(), messages, emit)
		if err != nil {
			failureRequest := r
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				failureRequest = r.Clone(context.WithoutCancel(r.Context()))
			}
			return s.persistAgentFailure(failureRequest, *session, messageID, finalText, parts, started, err)
		}
		// Some gateways let the model print report_progress as JSON text instead of a call; turn it back into steps.
		if leaked, rest := leakedProgressSteps(turn.Text); len(leaked) > 0 {
			turn.Text = rest
			steps := make([]domain.AgentMessagePart, 0, len(leaked))
			for index, step := range leaked {
				steps = append(steps, domain.AgentMessagePart{ID: fmt.Sprintf("%s_step_%d_%d", messageID, turnIndex, index), Type: "step", Title: step.Title, Text: step.Message, Status: "completed"})
			}
			parts = append(parts[:turnStart], append(steps, parts[turnStart:]...)...)
			for key, index := range partIndex {
				if index >= turnStart {
					partIndex[key] = index + len(steps)
				}
			}
			if textIndex, ok := partIndex["text"]; ok {
				cleaned := &strings.Builder{}
				cleaned.WriteString(finalText + rest)
				partText["text"] = cleaned
				parts[textIndex].Text = cleaned.String()
			}
		}
		narrationKey := strings.Join(strings.Fields(turn.Text), " ")
		repeated := narrationKey != "" && seenNarration[narrationKey]
		if narrationKey != "" {
			seenNarration[narrationKey] = true
		}
		// The loop builder speaks before its questions, like Linear's "I've opened a draft…";
		// that text is part of the reply, not folded narration.
		keepVisible := len(session.LoopIDs) > 0 && !repeated && slices.ContainsFunc(turn.ToolCalls, func(call domain.AgentToolCall) bool {
			return strings.TrimPrefix(call.Name, "mcp__flow.") == "ask_question"
		})
		if keepVisible {
			// Stays in the reply; separated from earlier text before the turn started.
		} else if len(turn.ToolCalls) > 0 && strings.TrimSpace(turn.Text) != "" {
			// Linear keeps mid-task narration inside the work group; only the last turn's text is the answer.
			narration := strings.TrimSpace(turn.Text)
			turn.Text = ""
			if textIndex, ok := partIndex["text"]; ok {
				cleaned := &strings.Builder{}
				cleaned.WriteString(finalText)
				partText["text"] = cleaned
				parts[textIndex].Text = cleaned.String()
				if strings.TrimSpace(finalText) == "" {
					parts = removeAgentPart(parts, partIndex, textIndex)
					delete(partText, "text")
				}
			}
			if !repeated && !agentTurnHasProgressMessage(turn.ToolCalls) {
				insertAt := min(turnStart, len(parts))
				narrationPart := domain.AgentMessagePart{ID: fmt.Sprintf("%s_narration_%d", messageID, turnIndex), Type: "reasoning", Text: narration, Status: "completed"}
				parts = append(parts[:insertAt], append([]domain.AgentMessagePart{narrationPart}, parts[insertAt:]...)...)
				for key, index := range partIndex {
					if index >= insertAt {
						partIndex[key] = index + 1
					}
				}
			}
			if writer != nil {
				if err := writer.send(agentStreamEvent{Type: "text.replaced", MessageID: messageID, Delta: finalText}); err != nil {
					return domain.AgentSession{}, err
				}
			}
		}
		finalText += turn.Text
		if len(turn.ToolCalls) == 0 {
			break
		}
		messages = append(messages, agentProviderMessage{Role: "assistant", Content: turn.Text, ToolCalls: turn.ToolCalls})
		for _, toolCall := range turn.ToolCalls {
			call := toolCall
			if call.Name == agentProgressTool {
				var progress struct {
					Title   string `json:"title"`
					Message string `json:"message"`
				}
				_ = json.Unmarshal(call.Arguments, &progress)
				placeholder, hasPlaceholder := partIndex["tool:"+call.ID]
				if title := strings.TrimSpace(progress.Title); title != "" {
					step := domain.AgentMessagePart{ID: fmt.Sprintf("%s_step_%d", messageID, len(parts)), Type: "step", Title: strings.TrimRight(title, ".…"), Text: strings.TrimSpace(progress.Message), Status: "completed"}
					index := len(parts)
					if hasPlaceholder {
						// Reuse the streamed tool row's slot so the step keeps its position and no empty tool row remains.
						index = placeholder
						step.ID = parts[index].ID
						parts[index] = step
						delete(partIndex, "tool:"+call.ID)
					} else {
						parts = append(parts, step)
					}
					if writer != nil {
						part := parts[index]
						if err := writer.send(agentStreamEvent{Type: "tool.completed", MessageID: messageID, Part: &part}); err != nil {
							return domain.AgentSession{}, err
						}
					}
				}
				if hasPlaceholder && parts[placeholder].Type == "toolCall" {
					parts = removeAgentPart(parts, partIndex, placeholder)
				}
				messages = append(messages, agentProviderMessage{Role: "tool", ToolResult: &agentProviderToolResult{CallID: call.ID, Content: `{"ok":true}`}})
				continue
			}
			if call.Name == agentLoadToolsTool {
				content, isError := `{"error":"load_tools is not available"}`, true
				if toolset := agentChatToolsetFrom(r.Context()); toolset != nil {
					available, _ := s.agentToolDefinitions()
					content, isError = toolset.load(call.Arguments, available)
				}
				messages = append(messages, agentProviderMessage{Role: "tool", ToolResult: &agentProviderToolResult{CallID: call.ID, Content: content, IsError: isError}})
				continue
			}
			if call.Name == loopQuestionTool {
				content, isError := s.askLoopQuestion(r, session, call, func(part domain.AgentMessagePart, kind string) error {
					index, found := partIndex[part.ID]
					if found {
						parts[index] = part
					} else {
						partIndex[part.ID] = len(parts)
						parts = append(parts, part)
					}
					if writer == nil {
						return nil
					}
					return writer.send(agentStreamEvent{Type: kind, MessageID: messageID, Part: &part})
				})
				if placeholder, ok := partIndex["tool:"+call.ID]; ok && parts[placeholder].Type == "toolCall" {
					parts = removeAgentPart(parts, partIndex, placeholder)
					delete(partIndex, "tool:"+call.ID)
				}
				messages = append(messages, agentProviderMessage{Role: "tool", ToolResult: &agentProviderToolResult{CallID: call.ID, Content: content, IsError: isError}})
				continue
			}
			if (s.agentToolRequiresApproval(call.Name) || strings.HasPrefix(call.Name, "external_")) && !sessionLoopTool(*session, call) {
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
			if callErr == nil && strings.TrimPrefix(call.Name, "mcp__flow.") == "save_loop" {
				call.Title = saveLoopToolTitle(result)
				var saveArgs struct {
					Publish bool `json:"publish"`
				}
				if _ = json.Unmarshal(call.Arguments, &saveArgs); saveArgs.Publish {
					call.Title = "Created automation"
				} else if call.Title == "" {
					call.Title = "Updated workflow definition draft"
				}
			}
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
	// Give the parallel title request a moment so the completed session usually carries it.
	if titleDone != nil {
		// The page also picks up a late title, so a fast reply does not wait long for it.
		select {
		case <-titleDone:
		case <-time.After(500 * time.Millisecond):
		}
	}
	return s.persistAgentCompletion(r, *session, domain.AgentMessage{ID: messageID, Role: "assistant", Content: strings.TrimSpace(finalText), Parts: parts, DurationMS: time.Since(started).Milliseconds(), CreatedAt: time.Now().UTC()})
}

// agentChatCacheKey groups one user's chat requests in a workspace for provider prompt caching: they share the
// static instructions and, mostly, the tool list.
func agentChatCacheKey(workspace, userID string) string {
	sum := sha256.Sum256([]byte(workspace + "\x00" + userID))
	return "flow-chat-" + hex.EncodeToString(sum[:10])
}

func mergeAgentProjects(current, extra []domain.Project) []domain.Project {
	for _, project := range extra {
		if !slices.ContainsFunc(current, func(item domain.Project) bool { return item.ID == project.ID }) {
			current = append(current, project)
		}
	}
	return current
}

func mergeAgentDocuments(current, extra []domain.Document) []domain.Document {
	for _, document := range extra {
		if !slices.ContainsFunc(current, func(item domain.Document) bool { return item.ID == document.ID }) {
			current = append(current, document)
		}
	}
	return current
}

// agentChatRetryDelay is the pause before a chat turn that failed transiently is asked again.
var agentChatRetryDelay = time.Second

// requestAgentChatTurn asks the provider for one chat turn, retrying once when the request failed transiently
// (connection error, 429/5xx, a stalled or overloaded gateway) before anything was streamed. A turn that already
// showed output is never repeated, so the reply cannot duplicate.
func (s *server) requestAgentChatTurn(ctx context.Context, messages []agentProviderMessage, emit func(agentProviderEvent) error) (agentProviderTurn, error) {
	for attempt := 0; ; attempt++ {
		emitted := false
		turn, err := s.requestAgentTurn(ctx, messages, func(event agentProviderEvent) error {
			emitted = true
			return emit(event)
		})
		if err == nil || emitted || attempt > 0 || ctx.Err() != nil || !loopTransientError(err) {
			return turn, err
		}
		select {
		case <-ctx.Done():
			return turn, ctx.Err()
		case <-time.After(agentChatRetryDelay):
		}
	}
}

const agentTitleSystemPrompt = `Write a short title for a chat that starts with the request below.
Rules: 3 to 7 words; use the language of the request; name the concrete subject (project, issue, or topic) when one is given; Title Case for English; no quotes, emoji, or trailing punctuation. Reply with the title only.`

// startAgentSessionTitle names a new chat the way Linear does ("Summarize Compare Test Project Status")
// instead of echoing the first line. It runs beside the main turn and only replaces the default title.
func (s *server) startAgentSessionTitle(r *http.Request, session domain.AgentSession, extra string) <-chan struct{} {
	if !s.agentAutoTitle || s.store == nil || len(session.Messages) != 1 || session.Messages[0].Role != "user" || session.Title != agentSessionTitle(session.Messages[0].Content) {
		return nil
	}
	done := make(chan struct{})
	workspace := workspaceKey(r)
	// Setting up a loop from a template: Linear titles the conversation with the loop's name.
	if len(session.LoopIDs) > 0 {
		if metadata, ok := s.store.WorkspaceMetadata(workspace); ok {
			if loop := loopByID(&metadata, session.LoopIDs[0]); loop != nil && loop.TemplateID != "" && strings.TrimSpace(loop.Name) != "" {
				_ = s.store.MutateWorkspace(context.WithoutCancel(r.Context()), workspace, "agent.session_titled", session.ID, nil, func(data *domain.Bootstrap) error {
					current, err := ownedAgentSession(data, session.ID)
					if err != nil {
						return err
					}
					current.Title = loop.Name
					return nil
				})
				close(done)
				return done
			}
		}
	}
	request := session.Messages[0].Content
	if runes := []rune(request); len(runes) > 1200 {
		request = string(runes[:1200])
	}
	if extra = strings.TrimSpace(extra); extra != "" {
		if runes := []rune(extra); len(runes) > 800 {
			extra = string(runes[:800])
		}
		request += "\n\nContext:\n" + extra
	}
	go func() {
		defer close(done)
		ctx, cancel := contextWithTimeout(r, 60*time.Second)
		defer cancel()
		turn, err := s.requestAgentTurnWithoutTools(ctx, []agentProviderMessage{{Role: "system", Content: agentTitleSystemPrompt}, {Role: "user", Content: request}})
		if err != nil {
			return
		}
		title := cleanAgentSessionTitle(turn.Text)
		if title == "" {
			return
		}
		_ = s.store.MutateWorkspace(ctx, workspace, "agent.session_titled", session.ID, nil, func(data *domain.Bootstrap) error {
			current, err := ownedAgentSession(data, session.ID)
			if err != nil || len(current.Messages) == 0 || current.Title != agentSessionTitle(current.Messages[0].Content) {
				return err
			}
			current.Title = title
			return nil
		})
	}()
	return done
}

// contextWithTimeout outlives the HTTP request so background work can finish after the stream closes.
func contextWithTimeout(r *http.Request, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(r.Context()), timeout)
}

func cleanAgentSessionTitle(text string) string {
	title := strings.TrimSpace(strings.Split(strings.TrimSpace(text), "\n")[0])
	title = strings.Trim(title, "\"'`“”‘’「」#*_ ")
	title = strings.TrimRight(title, ".。!！?？:：")
	if runes := []rune(title); len(runes) > 60 {
		title = string(runes[:60])
	}
	return strings.TrimSpace(title)
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
	if call.Name == agentProgressTool {
		return []byte(`{"ok":true}`), nil
	}
	if s.store != nil {
		// The policy check needs the workspace id and settings only.
		fresh, ok := s.store.WorkspaceSettingsMetadata(workspaceKey(r))
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
	if isWebTool(call.Name) {
		return s.executeWebTool(r.Context(), call.Name, call.Arguments)
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
	if base, ok := r.Context().Value(agentBaseURLKey{}).(string); ok {
		args["__flowBaseURL"] = base
	}
	actor := mcpActor{WorkspaceKey: workspaceKey(r), User: data.Viewer, APIKey: domain.APIKey{Scopes: []string{"read", "write"}}}
	if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok {
		actor.APIKey = key
	}
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
	// A stopped run still saves what it produced.
	err := s.store.MutateWorkspace(context.WithoutCancel(r.Context()), workspaceKey(r), "agent.message_completed", session.ID, nil, func(data *domain.Bootstrap) error {
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
	reason := cause.Error()
	if errors.Is(cause, context.Canceled) {
		reason = "Generation stopped"
	}
	parts = append(parts, domain.AgentMessagePart{ID: messageID + "_error", Type: "error", Text: reason, Status: "error"})
	// Always save the failed reply (with its error) so the question never
	// looks like it's still waiting for an answer when the chat is reopened.
	_, _ = s.persistAgentCompletion(r, session, domain.AgentMessage{ID: messageID, Role: "assistant", Content: strings.TrimSpace(text), Parts: parts, DurationMS: time.Since(started).Milliseconds(), CreatedAt: time.Now().UTC()})
	return domain.AgentSession{}, cause
}

type leakedProgressStep struct {
	Title   string `json:"title"`
	Message string `json:"message"`
}

// leakedProgressSteps peels report_progress payloads ({"title":…,"message":…}) off the start of a turn's text.
func leakedProgressSteps(text string) ([]leakedProgressStep, string) {
	rest := strings.TrimLeft(text, " \n\t")
	var steps []leakedProgressStep
	for strings.HasPrefix(rest, "{") {
		decoder := json.NewDecoder(strings.NewReader(rest))
		var raw map[string]json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			break
		}
		var step leakedProgressStep
		if _, ok := raw["title"]; !ok || len(raw) > 2 {
			break
		}
		if data, err := json.Marshal(raw); err != nil || json.Unmarshal(data, &step) != nil || strings.TrimSpace(step.Title) == "" {
			break
		}
		step.Title = strings.TrimRight(strings.TrimSpace(step.Title), ".…")
		step.Message = strings.TrimSpace(step.Message)
		steps = append(steps, step)
		rest = strings.TrimLeft(rest[decoder.InputOffset():], " \n\t")
	}
	if len(steps) == 0 {
		return nil, text
	}
	return steps, rest
}

// removeAgentPart drops parts[index] and keeps partIndex pointing at the right slots.
func removeAgentPart(parts []domain.AgentMessagePart, partIndex map[string]int, index int) []domain.AgentMessagePart {
	parts = append(parts[:index], parts[index+1:]...)
	for key, value := range partIndex {
		switch {
		case value == index:
			delete(partIndex, key)
		case value > index:
			partIndex[key] = value - 1
		}
	}
	return parts
}

// agentTurnHasProgressMessage reports whether the turn already narrated its plan through report_progress.
func agentTurnHasProgressMessage(calls []domain.AgentToolCall) bool {
	for _, call := range calls {
		if call.Name != agentProgressTool {
			continue
		}
		var progress struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(call.Arguments, &progress) == nil && strings.TrimSpace(progress.Message) != "" {
			return true
		}
	}
	return false
}

// agentWriteAccessNote tells the model why it cannot change data when the server runs without write tools,
// so it gives the user the real reason instead of a vague "not available here".
func (s *server) agentWriteAccessNote() string {
	if s.agent.WriteTools && s.agent.ToolsEnabled {
		return ""
	}
	return "\nWrite access: this Flow server has Agent write actions turned off, so you can only read. When asked to create, change, or delete anything, say that write actions are disabled for Flow Agent on this server (an admin enables them with FLOW_AGENT_WRITE_TOOLS=true), then offer the exact values you would use.\n"
}

// registerAgentRun records a running reply so it can be stopped explicitly;
// the returned func unregisters it.
func (s *server) registerAgentRun(sessionID string, cancel context.CancelFunc) func() {
	token := new(int)
	s.agentRuns.Store(sessionID, agentRun{cancel: cancel, token: token})
	return func() {
		if current, ok := s.agentRuns.Load(sessionID); ok && current.(agentRun).token == token {
			s.agentRuns.Delete(sessionID)
		}
	}
}

type agentRun struct {
	cancel context.CancelFunc
	token  *int
}

// stopAgentSession ends the reply running for the viewer's chat, if any.
func (s *server) stopAgentSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	data := s.workspaceData(r)
	if _, err := ownedAgentSession(&data, id); err != nil {
		respondMutation(w, err, http.StatusNoContent, nil)
		return
	}
	if run, ok := s.agentRuns.Load(id); ok {
		run.(agentRun).cancel()
	}
	w.WriteHeader(http.StatusNoContent)
}
