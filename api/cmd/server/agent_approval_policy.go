package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

// Like the reference app's agent, Flow's agent writes without asking: deletes (one item or many) and status updates it
// wrote itself run at once. The only calls that pause for the user's approval (Approve / Reject options) are tools
// of an MCP connector set to always ask (see connectorTool.needsApproval). Approvals saved by earlier versions for
// bulk deletes or agent-written updates still render (agentApprovalPreview) and still run or drop their held calls
// once answered. The held calls run once the user approves (a picked option or a reply such as "yes" / "批准");
// any other reply rejects them.

const (
	agentApprovalApprove = "approve"
	agentApprovalReject  = "reject"
)

var agentApprovalWords = map[string]bool{
	"approve": true, "approved": true, "yes": true, "y": true, "ok": true, "okay": true, "sure": true, "confirm": true,
	"confirmed": true, "proceed": true, "go ahead": true, "do it": true, "yes please": true, "lgtm": true,
	"批准": true, "同意": true, "确认": true, "确定": true, "是": true, "是的": true, "好": true, "好的": true, "可以": true,
	"继续": true, "执行": true, "通过": true, "行": true, "没问题": true,
}

// agentApprovalAnswer reads a reply to an approval: "approve" for a yes, "reject" for anything else.
func agentApprovalAnswer(reply string) string {
	normalized := strings.ToLower(strings.TrimSpace(reply))
	normalized = strings.TrimRight(normalized, " .!。！~")
	normalized = strings.Join(strings.Fields(normalized), " ")
	if agentApprovalWords[normalized] {
		return agentApprovalApprove
	}
	return agentApprovalReject
}

func agentToolBaseName(name string) string {
	return strings.TrimPrefix(name, "mcp__flow.")
}

// agentDeleteReferences lists the items a delete call names.
func agentDeleteReferences(call domain.AgentToolCall) []string {
	var args map[string]any
	if json.Unmarshal(call.Arguments, &args) != nil {
		return nil
	}
	refs := []string{}
	add := func(value any) {
		switch typed := value.(type) {
		case string:
			if text := normalizeAgentReference(typed, strings.Contains(call.Name, "issue")); strings.TrimSpace(text) != "" {
				refs = append(refs, text)
			}
		case []any:
			for _, item := range typed {
				if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
					refs = append(refs, normalizeAgentReference(text, strings.Contains(call.Name, "issue")))
				}
			}
		}
	}
	for key, value := range args {
		if key == "id" || key == "ids" || key == "commentId" || key == "draftId" || strings.HasSuffix(key, "Ids") {
			add(value)
		}
	}
	return refs
}

// agentIssueReferenceShape is an issue identifier (DEV-42) or internal issue id (issue_42).
var agentIssueReferenceShape = regexp.MustCompile(`(?i)^(?:[a-z][a-z0-9]{0,9}-[0-9]{1,7}|issue_[a-z0-9]+)$`)

// agentUngroundedIssueTarget returns the issue a delete_issue call, or a save_issue call that updates an existing
// issue, names without the conversation ever having shown it, or "". The grounding is what the user wrote, the
// loaded context and replies they saw (messages before historyEnd), and successful tool results; the model's own
// arguments and text in this run do not count. Writing to an issue nobody looked up is a guess: a model asked to
// create sub-issues of DEV-230 updated DEV-231…233 ("the next numbers"), unrelated issues that a later
// "delete the sub-issues" then deleted.
func agentUngroundedIssueTarget(call domain.AgentToolCall, messages []agentProviderMessage, historyEnd int) string {
	var refs []string
	switch agentToolBaseName(call.Name) {
	case "delete_issue":
		refs = agentDeleteReferences(call)
	case "save_issue":
		var args map[string]any
		if json.Unmarshal(call.Arguments, &args) != nil {
			return ""
		}
		if id, ok := args["id"].(string); ok && strings.TrimSpace(id) != "" {
			refs = []string{normalizeAgentReference(id, true)}
		}
	default:
		return ""
	}
	if len(refs) == 0 {
		return ""
	}
	var seen strings.Builder
	for index, message := range messages {
		switch {
		case message.ToolResult != nil:
			if !message.ToolResult.IsError {
				seen.WriteString(message.ToolResult.Content)
			}
		case message.Role == "assistant" && index >= historyEnd:
			// This run's own text is not evidence.
		default:
			seen.WriteString(message.Content)
		}
		seen.WriteString("\n")
	}
	text := strings.ToUpper(seen.String())
	for _, ref := range refs {
		ref = strings.ToUpper(strings.TrimSpace(ref))
		if !agentIssueReferenceShape.MatchString(ref) {
			continue // not an id or identifier; the tool resolves (or rejects) it
		}
		if !agentTextMentionsReference(text, ref) {
			return ref
		}
	}
	return ""
}

// agentTextMentionsReference reports whether text holds ref as a whole token (DEV-23 is not in "DEV-231").
func agentTextMentionsReference(text, ref string) bool {
	for offset := 0; ; {
		at := strings.Index(text[offset:], ref)
		if at < 0 {
			return false
		}
		start, end := offset+at, offset+at+len(ref)
		if (start == 0 || !isASCIIWordByte(text[start-1]) && text[start-1] != '-') && (end == len(text) || !isASCIIWordByte(text[end])) {
			return true
		}
		offset = start + 1
	}
}

// agentUngroundedIssueMessage is the tool error for a write to an issue the conversation never showed.
func agentUngroundedIssueMessage(ref string) string {
	return fmt.Sprintf("Not run: %s has not appeared in this conversation (not in the user's request, the context or any tool result), so it may not be the issue you mean. To create an issue, call save_issue without id. To change or delete an existing issue, look it up first (get_issue, or list_issues with parentId for sub-issues) and use the id or identifier the result gives.", ref)
}

// agentApprovalPreview is the approval as markdown: the question the model (and plain-text clients) see.
func agentApprovalPreview(approval domain.AgentApproval) string {
	var text strings.Builder
	link := func(target domain.AgentApprovalTarget) string {
		label := target.Label
		if target.Title != "" {
			label += " " + target.Title
		}
		if target.Href == "" {
			return label
		}
		return fmt.Sprintf("[%s](%s)", strings.NewReplacer("[", `\[`, "]", `\]`).Replace(label), target.Href)
	}
	if len(approval.Targets) > 0 {
		permanent := slices.ContainsFunc(approval.Targets, func(target domain.AgentApprovalTarget) bool { return target.Permanent })
		if permanent {
			fmt.Fprintf(&text, "Delete these %d items? Items marked permanent can't be restored.\n", len(approval.Targets))
		} else {
			fmt.Fprintf(&text, "Delete these %d items? They go to the trash and can be restored.\n", len(approval.Targets))
		}
		for _, target := range approval.Targets {
			text.WriteString("- " + link(target))
			if target.Permanent {
				text.WriteString(" (permanent)")
			}
			if target.SubIssues > 0 {
				fmt.Fprintf(&text, " (has %d sub-issues)", target.SubIssues)
			}
			for _, kind := range slices.Sorted(maps.Keys(target.Affected)) {
				fmt.Fprintf(&text, " (%d %s)", target.Affected[kind], kind)
			}
			text.WriteString("\n")
		}
	}
	if approval.Body != "" || approval.Kind == "publish" {
		if text.Len() > 0 {
			text.WriteString("\n")
		}
		text.WriteString("Post this update")
		if approval.Parent != nil {
			text.WriteString(" to " + link(*approval.Parent))
		}
		text.WriteString("?\n\n")
		for _, line := range strings.Split(approval.Body, "\n") {
			text.WriteString("> " + line + "\n")
		}
		if approval.Health != "" {
			fmt.Fprintf(&text, "\nHealth: %s\n", approval.Health)
		}
	}
	return strings.TrimSpace(text.String())
}

// Approval is driven only by real tool calls: Flow holds a call that needs the user's approval and asks for it
// itself. A model that instead writes the question as its answer ("Allow Code search: search_code?") would end the
// run with nothing to approve, so such a turn is retried once with agentTextApprovalNudge and then fails with
// agentTextApprovalError.
const (
	agentTextApprovalNudge = "Your last reply asked for permission in text instead of calling a tool, so nothing ran and the user has nothing to approve. Do not ask for approval in text: Flow asks the user itself when a tool needs approval and resumes you afterwards. Call the tool you need now, or answer without it."
	agentTextApprovalError = "The agent asked for approval in text instead of calling the tool; nothing ran. Retry."
)

// agentTextApprovalLine is Flow's own approval phrasing on a line of its own, in English ("Allow <names>?") or as
// the same question in Chinese ("允许 <names> 吗？", "是否允许 <names>？").
var agentTextApprovalLine = regexp.MustCompile(`^(?:(?i:allow)\s+(.+?)|(?:是否|请|可以|能否)?允许\s*(.+?)\s*吗?)\s*[?？]$`)

// agentTextApprovalRequest reports whether a reply without tool calls asks the user to allow a tool offered in ctx,
// in Flow's approval phrasing: a line "Allow <names>?" where a name is an offered tool ("search_code",
// "save_issue") or a connector's tool ("Code search: search_code"). Logs the turn's shape (never its text) so a
// recurrence shows whether the provider reported tool calls it did not deliver.
func (s *server) agentTextApprovalRequest(ctx context.Context, turn agentProviderTurn) bool {
	if len(turn.ToolCalls) > 0 || strings.TrimSpace(turn.Text) == "" {
		return false
	}
	tools, connectors := map[string]bool{}, map[string]bool{}
	for _, tool := range s.agentTurnTools(ctx) {
		tools[tool.Name], tools[strings.TrimPrefix(tool.Name, "mcp__flow.")] = true, true
	}
	if items, ok := ctx.Value(connectorToolsKey{}).([]connectorTool); ok {
		for _, tool := range items {
			tools[tool.RemoteName], connectors[strings.ToLower(tool.Policy.Name)] = true, true
		}
	}
	for _, line := range strings.Split(turn.Text, "\n") {
		match := agentTextApprovalLine.FindStringSubmatch(strings.Trim(strings.TrimSpace(line), "*_`> -"))
		if match == nil {
			continue
		}
		names := match[1] + match[2]
		for _, name := range strings.FieldsFunc(names, func(r rune) bool { return r == ',' || r == '，' || r == '、' }) {
			name = strings.Trim(strings.TrimSpace(name), "*_`\"'“”「」")
			connector, tool, prefixed := strings.Cut(strings.Replace(name, "：", ":", 1), ":")
			if tools[name] || prefixed && (connectors[strings.ToLower(strings.TrimSpace(connector))] || tools[strings.TrimSpace(tool)]) {
				slog.WarnContext(ctx, "agent asked for tool approval in text", "protocol", s.agent.Protocol, "model", s.agent.Model, "stop_reason", turn.StopReason, "text_bytes", len(turn.Text), "reasoning_bytes", len(turn.Reasoning))
				return true
			}
		}
	}
	return false
}

// agentApprovalNarration is what an approval question said before Flow's own question (the turn's text), so a later
// run replays the agent's words without the question: replayed as the agent's own text, "Allow …?" taught models
// to ask for approval in text instead of calling the tool.
func agentApprovalNarration(activity domain.AgentActivity) string {
	body := strings.TrimSpace(activity.Body)
	if index := strings.LastIndex(body, "Allow "); index >= 0 && strings.HasSuffix(body, "?") && (index == 0 || strings.HasSuffix(body[:index], "\n\n")) {
		return strings.TrimSpace(body[:index])
	}
	var approval domain.AgentApproval
	if raw, err := json.Marshal(activity.SignalMetadata["approval"]); err == nil && json.Unmarshal(raw, &approval) == nil {
		if preview := agentApprovalPreview(approval); preview != "" && strings.HasSuffix(body, preview) {
			return strings.TrimSpace(strings.TrimSuffix(body, preview))
		}
	}
	return ""
}

// agentApprovalOutcome is the person's answer to an approval as a later run reads it: what they decided about which
// calls (and their reply when it was more than the decision), rather than the bare "approve" or "reject".
func agentApprovalOutcome(ctx context.Context, activity domain.AgentActivity) string {
	names := agentHeldCallNames(ctx, agentApprovalBatchCalls(*activity.ToolCall))
	text := "Approved: " + names + " (Flow ran it)."
	if activity.ToolCall.Status != "approved" {
		text = "Rejected: " + names + " (nothing ran)."
	}
	// A picked option is its value, optionally followed by the person's note ("approve\n\nOnly the main repo").
	body := strings.TrimSpace(activity.Body)
	for _, value := range []string{agentApprovalApprove, agentApprovalReject} {
		if body == value {
			body = ""
		} else if rest, ok := strings.CutPrefix(body, value+"\n\n"); ok {
			body = strings.TrimSpace(rest)
		}
	}
	if body != "" {
		text += "\n" + body
	}
	return text
}

// agentApprovalBatchTool names the pending tool of an agent thread that holds the calls of one approval.
const agentApprovalBatchTool = "flow_approval"

// agentApprovalBatchCall packs an approval's held calls into a thread's single pending tool call.
func agentApprovalBatchCall(approval domain.AgentApproval) domain.AgentToolCall {
	raw, _ := json.Marshal(approval.Calls)
	id := "approval_calls"
	if len(approval.Calls) > 0 {
		id = "approval_" + approval.Calls[0].ID
	}
	return domain.AgentToolCall{ID: id, Name: agentApprovalBatchTool, Arguments: raw, Status: "pending"}
}

// agentApprovalBatchCalls unpacks a thread's pending tool call into the calls it holds.
func agentApprovalBatchCalls(call domain.AgentToolCall) []domain.AgentToolCall {
	if call.Name != agentApprovalBatchTool {
		return []domain.AgentToolCall{call}
	}
	var calls []domain.AgentToolCall
	_ = json.Unmarshal(call.Arguments, &calls)
	return calls
}

// pendingAgentApproval finds the approval the session's latest user message answered and that has not been
// handled yet: the reply before it stopped on an approval question.
func pendingAgentApproval(session *domain.AgentSession) (messageID, partID string, approval domain.AgentApproval, decision string, ok bool) {
	count := len(session.Messages)
	if count < 2 || session.Messages[count-1].Role != "user" || session.Messages[count-2].Role != "assistant" {
		return
	}
	reply := session.Messages[count-2]
	for _, part := range reply.Parts {
		if part.Type != "question" || part.Question == nil || part.Question.Approval == nil || part.Question.Approval.Decision != "" || part.Status != "completed" {
			continue
		}
		decision = agentApprovalReject
		if part.Question.Selected == agentApprovalApprove || part.Question.Selected == "" && agentApprovalAnswer(part.Question.Answer) == agentApprovalApprove {
			decision = agentApprovalApprove
		}
		return reply.ID, part.ID, *part.Question.Approval, decision, true
	}
	return
}

// resumeAgentApproval runs the calls an approved question held (as steps of the new reply) and returns the
// conversation the model continues from. A rejected approval runs nothing; the user's answer tells the model.
func (s *server) resumeAgentApproval(r *http.Request, data domain.Bootstrap, session *domain.AgentSession, messageID string, writer *agentEventWriter, parts *[]domain.AgentMessagePart, partIndex map[string]int) ([]agentProviderMessage, error) {
	replyID, partID, approval, decision, ok := pendingAgentApproval(session)
	if !ok {
		return nil, nil
	}
	// The decision is saved first, so a retried reply never runs the calls twice.
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "agent.approval_resolved", session.ID, nil, func(next *domain.Bootstrap) error {
		current, err := ownedAgentSession(next, session.ID)
		if err != nil {
			return err
		}
		for messageIndex := range current.Messages {
			if current.Messages[messageIndex].ID != replyID {
				continue
			}
			for index := range current.Messages[messageIndex].Parts {
				part := &current.Messages[messageIndex].Parts[index]
				if part.ID == partID && part.Question != nil && part.Question.Approval != nil {
					if part.Question.Approval.Decision != "" {
						return fmt.Errorf("%w: this approval was already handled", errInvalid)
					}
					resolved := *part.Question.Approval
					resolved.Decision = map[string]string{agentApprovalApprove: "approved", agentApprovalReject: "rejected"}[decision]
					question := *part.Question
					question.Approval = &resolved
					part.Question = &question
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if decision != agentApprovalApprove || len(approval.Calls) == 0 {
		return []agentProviderMessage{{Role: "system", Content: "The user rejected the held action(s); nothing was changed. Do not retry them unless the user asks again."}}, nil
	}
	calls := make([]domain.AgentToolCall, 0, len(approval.Calls))
	results := make([]agentProviderMessage, 0, len(approval.Calls))
	for _, held := range approval.Calls {
		call := held
		call.ID = "approved_" + held.ID
		call.Status, call.Result, call.Error = "running", nil, ""
		result, callErr := s.executeAgentToolRecordingChange(r, data, &call)
		call.Status = "completed"
		content := string(result)
		if callErr != nil {
			call.Status, call.Error = "error", callErr.Error()
			content = callErr.Error()
			result, _ = json.Marshal(map[string]string{"error": callErr.Error()})
		}
		call.Result = json.RawMessage(result)
		part := domain.AgentMessagePart{ID: fmt.Sprintf("%s_tool_%d", messageID, len(*parts)), Type: "toolCall", Status: call.Status, ToolCall: cloneToolCall(&call)}
		partIndex["tool:"+call.ID] = len(*parts)
		*parts = append(*parts, part)
		if writer != nil {
			if err := writer.send(agentStreamEvent{Type: "tool.completed", MessageID: messageID, Part: &part}); err != nil {
				return nil, err
			}
		}
		calls = append(calls, call)
		results = append(results, agentProviderMessage{Role: "tool", ToolResult: &agentProviderToolResult{CallID: call.ID, Content: content, IsError: callErr != nil}})
	}
	return append([]agentProviderMessage{{Role: "assistant", ToolCalls: calls}}, results...), nil
}
