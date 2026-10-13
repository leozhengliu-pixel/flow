package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"flow/api/internal/domain"
)

// Every provider protocol requires each tool call in the conversation to be answered by its output before the
// next turn (OpenAI Responses: "No tool output found for function call fc_…"). Stored chats break that rule when a
// reply was stopped or failed mid-call, when a question or approval is still open, or when an older reply was
// saved without results; one such message used to poison the chat for good. sanitizeAgentProviderMessages repairs
// any conversation right before it is sent.

// agentToolCancelledOutput answers a call whose result never arrived.
const agentToolCancelledOutput = `{"error":"Cancelled: the user stopped the reply before this tool finished."}`

// agentHostedToolPrefix marks a provider-hosted tool (native web search) shown as a step: it ran on the provider's
// side and is never replayed as a function call.
const agentHostedToolPrefix = "hosted_"

// sanitizeAgentProviderMessages returns a copy of messages in which every tool call is immediately followed by
// exactly one output (a synthetic "cancelled" one when it is missing), outputs without a matching call are dropped,
// calls without an id or name are removed, unparsable call arguments become {}, and empty assistant messages go.
func sanitizeAgentProviderMessages(messages []agentProviderMessage) []agentProviderMessage {
	result := make([]agentProviderMessage, 0, len(messages))
	seenCalls := map[string]bool{}
	for index := 0; index < len(messages); index++ {
		message := messages[index]
		if message.ToolResult != nil {
			// A result not directly after its call (orphan or duplicate).
			continue
		}
		if len(message.ToolCalls) == 0 {
			if message.Role == "assistant" && strings.TrimSpace(message.Content) == "" && len(message.Images) == 0 {
				continue
			}
			result = append(result, message)
			continue
		}
		calls := make([]domain.AgentToolCall, 0, len(message.ToolCalls))
		for _, call := range message.ToolCalls {
			if call.ID == "" || call.Name == "" || seenCalls[call.ID] || strings.HasPrefix(call.ID, agentHostedToolPrefix) {
				continue
			}
			seenCalls[call.ID] = true
			if len(call.Arguments) > 0 && !json.Valid(call.Arguments) {
				call.Arguments = json.RawMessage(`{}`)
			}
			calls = append(calls, call)
		}
		// The results that follow this message, by call id (the first one wins).
		outputs := map[string]*agentProviderToolResult{}
		next := index + 1
		for ; next < len(messages) && messages[next].ToolResult != nil; next++ {
			output := messages[next].ToolResult
			if _, ok := outputs[output.CallID]; !ok {
				outputs[output.CallID] = output
			}
		}
		index = next - 1
		message.ToolCalls = calls
		if len(calls) == 0 {
			if strings.TrimSpace(message.Content) != "" {
				result = append(result, message)
			}
			continue
		}
		result = append(result, message)
		for _, call := range calls {
			output := outputs[call.ID]
			if output == nil {
				output = &agentProviderToolResult{CallID: call.ID, Content: agentToolCancelledOutput, IsError: true}
			}
			result = append(result, agentProviderMessage{Role: "tool", ToolResult: output})
		}
	}
	return result
}

// agentInterruptedToolResult is the result saved for a tool call that never finished.
func agentInterruptedToolResult(stopped bool) (string, json.RawMessage) {
	message := "The reply failed before this step finished."
	if stopped {
		message = "Stopped before this step finished."
	}
	raw, _ := json.Marshal(map[string]string{"error": "Cancelled: " + message})
	return message, raw
}

// settleInterruptedAgentParts gives every tool call of a stopped or failed reply a result, so the saved reply shows
// the step as not finished and the chat stays valid for the next message.
func settleInterruptedAgentParts(parts []domain.AgentMessagePart, stopped bool) {
	message, result := agentInterruptedToolResult(stopped)
	for index := range parts {
		part := &parts[index]
		if part.ToolCall == nil || part.Type != "toolCall" || len(part.ToolCall.Result) > 0 {
			continue
		}
		call := *part.ToolCall
		call.Status, call.Error, call.Result, call.ApprovalID = "error", message, result, ""
		part.ToolCall = &call
		part.Status = "error"
	}
}

var agentMarkdownIssueLink = regexp.MustCompile(`\[((?:[^\]\\]|\\.)*)\]\((?:[a-z]+:)?[^)\s]*/issue/([A-Za-z][A-Za-z0-9]{0,9}-[0-9]{1,7})[^)\s]*\)`)

// agentModelUserText rewrites the chips of a user message into references the model can copy into tool arguments:
// an @-mentioned or linked issue becomes `DEV-42 ("Title")`, a project `project "Name"`, a document
// `document "Title"`. issueTitle looks up a loaded issue's title by identifier (it may return "").
func agentModelUserText(content string, mentions []domain.AgentMention, issueTitle func(identifier string) string) string {
	issueRef := func(identifier string) string {
		identifier = strings.ToUpper(strings.TrimSpace(identifier))
		if title := strings.TrimSpace(issueTitle(identifier)); title != "" {
			return fmt.Sprintf("%s (%q)", identifier, title)
		}
		return identifier
	}
	// Links to issues ([DEV-42 Title](/ws/issue/DEV-42/…)), as comments and pasted chips carry them.
	content = agentMarkdownIssueLink.ReplaceAllStringFunc(content, func(link string) string {
		match := agentMarkdownIssueLink.FindStringSubmatch(link)
		return issueRef(match[2])
	})
	if len(mentions) == 0 {
		return content
	}
	ordered := append([]domain.AgentMention(nil), mentions...)
	// Longer labels first, so "@Export revamp v2" is not cut by "@Export revamp".
	sort.SliceStable(ordered, func(a, b int) bool { return len(ordered[a].Label) > len(ordered[b].Label) })
	for _, mention := range ordered {
		label := strings.TrimSpace(mention.Label)
		if label == "" {
			continue
		}
		var replacement string
		switch mention.Type {
		case "issue":
			replacement = issueRef(label)
		case "project":
			replacement = fmt.Sprintf("project %q", label)
		case "document":
			replacement = fmt.Sprintf("document %q", label)
		default:
			continue
		}
		content = strings.ReplaceAll(content, "@"+label, replacement)
	}
	return content
}
