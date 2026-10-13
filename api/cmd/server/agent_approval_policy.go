package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

// Like the reference app's agent, Flow's agent writes without asking, with two exceptions that pause for the user's approval
// (shown as a readable preview with Approve / Reject options):
//   - deleting more than one item in one go (a turn of delete_* calls naming two or more targets);
//   - publishing a project or initiative update the agent wrote on its own initiative — the user's latest message
//     did not ask to post or publish it.
// The held calls run once the user approves (a picked option or a reply such as "yes" / "批准"); any other reply
// rejects them.

const (
	agentApprovalApprove = "approve"
	agentApprovalReject  = "reject"
)

// agentApprovalOptions are the choices of an approval question; the app translates the labels.
var agentApprovalOptions = []domain.AgentQuestionOption{{Label: "Approve", Value: agentApprovalApprove}, {Label: "Reject", Value: agentApprovalReject}}

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

var agentPublishRequest = regexp.MustCompile(`(?i)\b(post|publish|share|send|submit)\b|发布|发表|发出|发送|贴出|提交|分享`)

// agentUserAskedToPublish reports whether the user's message asks to post an update, so the agent may publish it
// without asking again.
func agentUserAskedToPublish(message string) bool {
	return agentPublishRequest.MatchString(message)
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

// agentApprovalForTurn returns the approval a turn's calls need, holding the calls it names, or nil when they may
// all run now. latestUser is the user's latest message.
func (s *server) agentApprovalForTurn(r *http.Request, data domain.Bootstrap, calls []domain.AgentToolCall, latestUser string) *domain.AgentApproval {
	// The user just approved (for example after the agent asked itself): do not ask twice.
	if agentApprovalAnswer(latestUser) == agentApprovalApprove {
		return nil
	}
	var deletes, publishes []domain.AgentToolCall
	targets := []domain.AgentApprovalTarget{}
	for _, call := range calls {
		name := agentToolBaseName(call.Name)
		switch {
		case strings.HasPrefix(name, "delete_"):
			deletes = append(deletes, call)
			for _, ref := range agentDeleteReferences(call) {
				targets = append(targets, s.agentApprovalTarget(r, data, name, ref))
			}
		case name == "save_status_update":
			var args struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(call.Arguments, &args)
			if strings.TrimSpace(args.ID) == "" && !agentUserAskedToPublish(latestUser) {
				publishes = append(publishes, call)
			}
		}
	}
	approval := &domain.AgentApproval{}
	if len(targets) > 1 {
		approval.Kind = "delete"
		approval.Targets = targets
		approval.Calls = append(approval.Calls, deletes...)
	}
	if len(publishes) > 0 {
		if approval.Kind == "" {
			approval.Kind = "publish"
		}
		var args struct {
			Body       string `json:"body"`
			Health     string `json:"health"`
			Type       string `json:"type"`
			Project    string `json:"project"`
			Initiative string `json:"initiative"`
		}
		_ = json.Unmarshal(publishes[0].Arguments, &args)
		approval.Body, approval.Health = strings.TrimSpace(args.Body), args.Health
		if args.Type == "initiative" || args.Initiative != "" && args.Project == "" {
			parent := s.agentApprovalTarget(r, data, "delete_initiative", args.Initiative)
			approval.Parent = &parent
		} else if args.Project != "" {
			parent := s.agentApprovalTarget(r, data, "delete_project", args.Project)
			approval.Parent = &parent
		}
		approval.Calls = append(approval.Calls, publishes...)
	}
	if approval.Kind == "" {
		return nil
	}
	return approval
}

// agentApprovalTarget names one item a held call would change, for the preview.
func (s *server) agentApprovalTarget(r *http.Request, data domain.Bootstrap, tool, ref string) domain.AgentApprovalTarget {
	root := "/" + url.PathEscape(data.Workspace.URLKey)
	kind := strings.TrimPrefix(tool, "delete_")
	target := domain.AgentApprovalTarget{Kind: kind, ID: ref, Label: ref}
	switch kind {
	case "issue":
		if issue, ok := s.agentApprovalIssue(r, data, ref); ok {
			target.ID, target.Label, target.Title, target.SubIssues = issue.ID, issue.Identifier, issue.Title, len(issue.SubIssueIDs)
			target.Href = root + "/issue/" + url.PathEscape(issue.Identifier)
		}
	case "document":
		if document, err := mcpFindDocument(data, ref); err == nil {
			target.ID, target.Label, target.Href = document.ID, documentDisplayTitle(document.Title), root+"/document/"+url.PathEscape(document.SlugID)
		}
	case "project":
		if project, err := mcpFindProject(data, ref); err == nil {
			target.ID, target.Label, target.Href = project.ID, project.Name, root+"/project/"+url.PathEscape(project.SlugID)+"/overview"
		}
	case "initiative":
		if initiative, err := mcpFindInitiative(data, ref); err == nil {
			target.ID, target.Label, target.Href = initiative.ID, initiative.Name, root+"/initiative/"+url.PathEscape(initiative.SlugID)+"/overview"
		}
	}
	// Other delete tools describe their target (name, linked items) without deleting it.
	if slices.Contains(mcpDeleteTools, tool) {
		if preview, err := s.flowDeletePreview(r.Context(), s.agentApprovalActor(r, data), tool, map[string]any{"id": ref}); err == nil {
			if name, _ := preview["name"].(string); name != "" {
				target.Label = name
			}
			if id, _ := preview["id"].(string); id != "" {
				target.ID = id
			}
			if affected, ok := preview["affected"].(map[string]int); ok && len(affected) > 0 {
				target.Affected = affected
			}
			if restorable, ok := preview["restorable"].(bool); ok && !restorable {
				target.Permanent = true
			}
		}
	}
	switch kind {
	case "label":
		if label, err := mcpFindLabel(data, ref); err == nil {
			target.ID, target.Label = label.ID, label.Name
		}
	case "view":
		if view, err := mcpFindView(data, ref); err == nil {
			target.ID, target.Label = view.ID, view.Name
		}
	case "customer":
		if customer, err := mcpFindCustomer(data, ref); err == nil {
			target.ID, target.Label = customer.ID, customer.Name
		}
	}
	return target
}

// agentApprovalIssue loads an issue the viewer can see by id or identifier.
func (s *server) agentApprovalIssue(r *http.Request, data domain.Bootstrap, ref string) (domain.Issue, bool) {
	if issue, err := mcpFindIssue(data, ref); err == nil {
		return issue, true
	}
	if s.store == nil {
		return domain.Issue{}, false
	}
	query, err := s.mcpIssueQuery(r.Context(), s.agentApprovalActor(r, data))
	if err != nil {
		return domain.Issue{}, false
	}
	issue, err := s.store.AuthorizedIssueRecord(r.Context(), query, ref)
	return issue, err == nil
}

// agentApprovalActor is who looks the targets up: the viewer (or, in a thread, the person who asked the agent).
func (s *server) agentApprovalActor(r *http.Request, data domain.Bootstrap) mcpActor {
	actor := mcpActor{WorkspaceKey: workspaceKey(r), User: data.Viewer, APIKey: domain.APIKey{Scopes: []string{"read"}}}
	if invoker, ok := r.Context().Value(builtinInvokerContextKey{}).(domain.User); ok {
		actor.AuthorizationUserID = invoker.ID
	}
	if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok {
		actor.APIKey = key
	}
	return actor
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

// agentHeldCallResult is what the model is told for a call that waits for approval.
const agentHeldCallResult = `{"status":"awaiting_approval","message":"The app asked the user to approve this action. Do not call the tool again; it runs once they approve."}`

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
