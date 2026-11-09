package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"flow/api/internal/domain"
)

// Requests the Flow Agent files from a group conversation (chat_group_agent.go).
//
// A turn of the agent that a member started in a Lark or Feishu group whose
// one active binding takes requests (chatBindingForChat, RequestsEnabled), in
// the binding's workspace, has the file_request tool. It makes a request from
// a message of the group exactly as “@Flow 提需求” does, through
// chatFileRequest: as the member, with their permissions, into the binding's
// team triage with its template and priority, as the same request record
// (an Ask from the platform, source mention), with the group's link, the
// message's thread as the request's thread and the same confirmation card.
// One message carries one request.
//
// The tool is a session tool, not part of Flow's MCP tools: the turn's
// request context carries the group (chatGroupRequestScope) in process, and a
// worker run's info carries it to /mcp (agent_runtime_session_tools.go), so
// it is never offered in direct messages, on the Agent page or in a later turn
// of the same chat asked elsewhere. Every call waits for the member's approval
// (an approval card in the thread only they can answer, agentToolNeedsApproval;
// _meta flow/approval on the worker), and it is a write in the ledger. When it
// runs, the member's link, the binding and their access are checked again.

const chatGroupRequestTool = "file_request"

// chatGroupRequestScope is the group turn a request may be filed from.
type chatGroupRequestScope struct {
	Provider     string `json:"provider"`
	ConnectionID string `json:"connectionId"`
	ChatID       string `json:"chatId"`
	// OpenID is the member who asked; MessageID their message and Root its
	// thread's root (the message itself when it starts the thread).
	OpenID    string `json:"openId"`
	MessageID string `json:"messageId"`
	Root      string `json:"root"`
}

type chatGroupRequestsKey struct{}

func withChatGroupRequests(ctx context.Context, scope chatGroupRequestScope) context.Context {
	return context.WithValue(ctx, chatGroupRequestsKey{}, scope)
}

// chatGroupRequestsFrom is the group a request may be filed from in ctx.
func chatGroupRequestsFrom(ctx context.Context) (chatGroupRequestScope, bool) {
	scope, ok := ctx.Value(chatGroupRequestsKey{}).(chatGroupRequestScope)
	return scope, ok && scope.ConnectionID != "" && scope.ChatID != "" && scope.OpenID != "" && scope.Root != ""
}

var chatGroupRequestToolDefinition = agentProviderTool{
	Name:        chatGroupRequestTool,
	Description: "File a request from this group conversation into the triage of the team the group is bound to, as the member who asked you, exactly as their “@Flow 提需求 <text>” would. Use it only when the member asks to file something as a request (for example “把上面这条提成需求”, “file this as a request”); they approve it before it is filed. Write the title and description from the conversation. messageId is the group message the request is about (an om_… id from the group's messages in your instructions); leave it out for the thread's root (the member's own message when it started no thread). A message carries one request only.",
	Parameters:  json.RawMessage(`{"type":"object","required":["title"],"properties":{"title":{"type":"string","description":"The request's title: one line, at most 200 characters"},"description":{"type":"string","description":"The request's details, written from the conversation"},"messageId":{"type":"string","description":"The group message the request is about (om_…); default: the thread's root"}},"additionalProperties":false}`),
	Access:      "write",
}

// chatGroupRequestInput is file_request's arguments.
type chatGroupRequestInput struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	MessageID   string `json:"messageId"`
}

// executeChatGroupRequest runs a file_request call as viewer in workspace.
func (s *server) executeChatGroupRequest(ctx context.Context, workspace string, viewer domain.User, call domain.AgentToolCall) ([]byte, error) {
	scope, ok := chatGroupRequestsFrom(ctx)
	if !ok {
		return nil, fmt.Errorf("%s is available only when a member asks Flow in a group that takes requests", chatGroupRequestTool)
	}
	var input chatGroupRequestInput
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return nil, fmt.Errorf("Invalid arguments for %s: pass title, and optionally description and messageId", chatGroupRequestTool)
	}
	title := strings.Join(strings.Fields(input.Title), " ")
	description := strings.TrimSpace(input.Description)
	switch {
	case title == "":
		return nil, fmt.Errorf("title is required: write the request's title from the conversation")
	case utf8.RuneCountInString(title) > 200:
		return nil, fmt.Errorf("title is too long: keep it to one line of at most 200 characters and put the rest in description")
	case utf8.RuneCountInString(description) > 8000:
		return nil, fmt.Errorf("description is too long: keep it under 8000 characters")
	}
	record, err := s.store.ChatIntegrationByID(ctx, scope.Provider, scope.ConnectionID)
	if err != nil || !record.Connected || record.WorkspaceKey != workspace {
		return nil, fmt.Errorf("Nothing was filed: the group's chat app is no longer connected to this workspace")
	}
	language := chatProviderLanguage(record.Provider)
	identity, err := s.store.ChatIdentityForOpenID(ctx, record.WorkspaceKey, record.ConnectionID, scope.OpenID)
	if err != nil || identity.UserID != viewer.ID || identity.TenantKey != record.TenantKey {
		return nil, fmt.Errorf("Nothing was filed: the member's chat account is no longer linked to their Flow account")
	}
	binding, err := s.chatBindingForChat(ctx, record, scope.ChatID)
	if err != nil || !binding.RequestsEnabled() {
		return nil, fmt.Errorf("Nothing was filed: %s", chatActionErrorText("chat_requests_disabled", language))
	}
	ctx = withChatIdentityAuthentication(ctx, identity)
	var metadata domain.Bootstrap
	if err := s.chatAuthorizedMutation(ctx, record.WorkspaceKey, identity.UserID, http.MethodGet, "/api/issue-records/bootstrap", nil, &metadata); err != nil {
		if _, transient := chatMutationCode(err, ""); transient != nil {
			return nil, transient
		}
		return nil, fmt.Errorf("Nothing was filed: the member's Flow access to this workspace has changed")
	}
	language = chatLanguage(metadata, identity.UserID, record.Provider)
	draft, code, err := s.chatGroupRequestDraft(ctx, record, scope, input.MessageID)
	if err != nil {
		return nil, err
	}
	if code != "" {
		return nil, fmt.Errorf("Nothing was filed: %s (pass the om_… id of a message of this group, or leave messageId out)", chatActionErrorText(code, language))
	}
	// The request reads as an explicit “@Flow 提需求 <title>\n<description>”: its
	// title is the first line, its description the text and the group's link.
	draft.Text, draft.Source = title, domain.ChatRequestSourceMention
	if description != "" {
		draft.Text += "\n" + description
	}
	job := domain.ChatJob{WorkspaceKey: record.WorkspaceKey, ConnectionID: record.ConnectionID, DedupKey: chatKey(record.ConnectionID, "agent_request", draft.MessageID)}
	outcome, err := s.chatFileRequest(ctx, record, binding, identity, metadata, job, draft)
	switch {
	case err != nil:
		return nil, err
	case outcome.Code != "":
		return nil, fmt.Errorf("Nothing was filed: %s", chatActionErrorText(outcome.Code, language))
	case outcome.Busy:
		return nil, fmt.Errorf("Nothing was filed: another member is filing a request from this message right now")
	case outcome.Existing != nil:
		text := chatT(language, "message_is_request")
		var issue domain.Issue
		if s.chatAuthorizedMutation(ctx, record.WorkspaceKey, identity.UserID, http.MethodGet, "/api/issue-records/"+url.PathEscape(outcome.Existing.IssueID), nil, &issue) == nil {
			text = chatT(language, "message_is_request_link", issue.Identifier+" "+chatRequestIssueURL(s, record, issue))
		}
		return nil, fmt.Errorf("Nothing was filed: %s (a message carries one request only; pass another messageId to file a different one)", text)
	}
	issue := outcome.Issue
	return json.Marshal(map[string]any{
		"identifier": issue.Identifier,
		"title":      issue.Title,
		"team":       issue.Team.Name,
		"state":      issue.State.Name,
		"url":        chatRequestIssueURL(s, record, issue),
		"filed":      "The request is in the team's triage; Flow posts its card in the message's thread in the group.",
	})
}

// chatGroupRequestDraft is the group message a request is filed from: the
// thread's root by default, the member's message, or another message of the
// group the bot can read. code refuses a message that is not the group's.
func (s *server) chatGroupRequestDraft(ctx context.Context, record domain.ChatIntegrationRecord, scope chatGroupRequestScope, messageID string) (chatRequestDraft, string, error) {
	id := strings.TrimSpace(messageID)
	switch {
	case id == "" || id == scope.Root:
		return chatRequestDraft{MessageID: scope.Root, ChatID: scope.ChatID}, "", nil
	case id == scope.MessageID:
		return chatRequestDraft{MessageID: scope.MessageID, ChatID: scope.ChatID, RootID: scope.Root}, "", nil
	case !chatValidPlatformID(id, "om_"):
		return chatRequestDraft{}, "chat_message_unavailable", nil
	}
	read, found, err := s.chatReadMessage(ctx, record, id)
	if err != nil {
		return chatRequestDraft{}, "", err
	}
	if !found || read.ChatID != scope.ChatID {
		return chatRequestDraft{}, "chat_message_unavailable", nil
	}
	return chatRequestDraft{MessageID: id, ChatID: scope.ChatID, RootID: read.RootID}, "", nil
}

// chatGroupRequestTitle labels a finished file_request step.
func chatGroupRequestTitle(result json.RawMessage) string {
	var filed struct {
		Identifier string `json:"identifier"`
	}
	if json.Unmarshal(result, &filed) != nil || filed.Identifier == "" {
		return ""
	}
	return "Filed request " + filed.Identifier
}

// chatGroupRequestPreview is a file_request call as its approval card shows
// it: the title, then the description.
func chatGroupRequestPreview(arguments json.RawMessage) string {
	var input chatGroupRequestInput
	if json.Unmarshal(arguments, &input) != nil {
		return ""
	}
	return strings.TrimSpace(strings.Join(strings.Fields(input.Title), " ") + "\n" + strings.TrimSpace(input.Description))
}
