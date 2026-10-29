package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Direct messages with the workspace's Lark or Feishu bot.
//
// A member who has not linked their chat account gets a card that links it,
// and nothing else happens. For a linked member, a direct message is the Flow
// Agent of the app: it runs as that member, through the same handlers as the
// Agent page (agent_stream.go, or the agent worker with
// FLOW_AGENT_RUNTIME=worker), so it has exactly their permissions, their rate
// limits and the same tool budget, write ledger and approvals. Each
// conversation is an Agent chat marked with the platform (AgentSession.Origin)
// and listed on the Agent page. The answer streams into one card the bot keeps
// updating; replies to it continue the same chat. A call that waits for
// approval gets its own card with Approve / Reject. Images and files sent in
// the message become the chat message's attachments, with the composer's
// limits.
//
// Quick commands answer without the agent: "new issue" / "新建事项" (a form
// that creates the issue), "my issues" / "我的事项", "inbox" / "收件箱",
// "switch workspace" / "切换工作区", and a pasted Flow link (a preview of
// what the member can open). A reply to a personal notification card comments
// on its issue.
//
// A member linked in several workspaces of the same tenant (each workspace
// connects its own app) picks one on their first message; the choice is
// remembered until they switch. Everything runs with the chosen workspace's
// own linked account, so unlinking either account stops it: queued messages
// get the link card, pending cards expire with the account (their actions are
// deleted with it), and a reply that is running stops.
//
// The conversation state (reply chains, the chosen workspace) is kept as the
// member's chat actions, which unlinking deletes; it expires after 30 days.

const (
	chatDMThreadPrefix    = "chat_dm_thread_"
	chatDMWorkspacePrefix = "chat_dm_workspace_"
	chatDMStateLifetime   = 30 * 24 * time.Hour
	chatDMFormLifetime    = 15 * time.Minute
	// A reply may wait on approvals for agentApprovalTimeout.
	chatDMRunLimit = agentApprovalTimeout + 15*time.Minute
)

// How often a reply card is updated while the reply streams (the platform
// allows a few updates per second per message), and how often a running reply
// checks that the member is still linked.
var (
	chatDMUpdateEvery    = 1200 * time.Millisecond
	chatDMLinkCheckEvery = 5 * time.Second
)

// chatDMReplies counts replies running in this process (tests wait on it),
// and chatDMActive holds the chats they answer.
var (
	chatDMReplies sync.WaitGroup
	chatDMActive  sync.Map
)

type chatDMOriginKey struct{}

// chatDMSessionOrigin is the platform an Agent chat is started from, set only
// on the requests of a direct message.
func chatDMSessionOrigin(ctx context.Context) string {
	origin, _ := ctx.Value(chatDMOriginKey{}).(string)
	return origin
}

// chatDMThread is what a message of the conversation belongs to: an Agent
// chat, or (for a personal notification card) an issue.
type chatDMThread struct {
	Kind         string `json:"kind"` // session or notification
	OpenID       string `json:"openId"`
	WorkspaceKey string `json:"workspaceKey"`
	SessionID    string `json:"sessionId,omitempty"`
	IssueID      string `json:"issueId,omitempty"`
}

// chatDMAction is a direct-message card button or form. Its JSON shares the
// personal action fields of chatCardAction, so the webhook checks the clicker,
// chat and message the same way (chatActionMatches).
type chatDMAction struct {
	Kind            string              `json:"kind"` // dm_workspace, dm_approval, dm_create
	OpenID          string              `json:"openId"`
	PersonalChatID  string              `json:"personalChatId,omitempty"`
	PersonalMessage string              `json:"personalMessage,omitempty"`
	WorkspaceKey    string              `json:"workspaceKey,omitempty"`
	SessionID       string              `json:"sessionId,omitempty"`
	ApprovalID      string              `json:"approvalId,omitempty"`
	Decision        string              `json:"decision,omitempty"`
	Sibling         string              `json:"sibling,omitempty"`
	Label           string              `json:"label,omitempty"`
	Preview         string              `json:"preview,omitempty"`
	TeamIDs         []string            `json:"teamIds,omitempty"`
	AssigneeIDs     []string            `json:"assigneeIds,omitempty"`
	Pending         *chatInboundCommand `json:"pending,omitempty"`
	Chinese         bool                `json:"zh,omitempty"`
}

type chatDMPendingAction struct {
	ID     string
	Action chatDMAction
}

// chatDMTarget is a workspace the member may use here, with their linked
// account in it.
type chatDMTarget struct {
	Workspace string
	Name      string
	Identity  domain.ChatIdentity
}

// chatDMRequest is one message (or card action) being answered.
type chatDMRequest struct {
	record   domain.ChatIntegrationRecord
	identity domain.ChatIdentity // linked through this bot
	target   chatDMTarget
	command  chatInboundCommand
	job      domain.ChatJob
	zh       bool
	metadata domain.Bootstrap
}

func (dm chatDMRequest) authContext(ctx context.Context) context.Context {
	return withChatIdentityAuthentication(ctx, dm.target.Identity)
}

// chatDirectMessageCommand accepts a member's direct message to the bot.
func chatDirectMessageCommand(record domain.ChatIntegrationRecord, envelope chatWebhookEnvelope) (chatInboundCommand, string, bool, error) {
	event := envelope.Event
	message := event.Message
	openID := event.Sender.SenderID.OpenID
	if event.Sender.SenderType != "user" || event.Sender.TenantKey != record.TenantKey || openID == "" || openID == record.BotOpenID || message.MessageID == "" || message.ChatID == "" || len(message.Content) > 48<<10 {
		return chatInboundCommand{}, "", false, nil
	}
	values := map[string]json.RawMessage{}
	for key, value := range map[string]string{"messageType": message.MessageType, "content": message.Content, "rootId": message.RootID, "parentId": message.ParentID} {
		if value != "" {
			raw, _ := json.Marshal(value)
			values[key] = raw
		}
	}
	return chatInboundCommand{Kind: "dm_message", OpenID: openID, ChatID: message.ChatID, MessageID: message.MessageID, Values: values}, chatKey(record.ConnectionID, "message", message.MessageID), true, nil
}

// processChatDirectMessage handles a queued direct message or a click on a
// direct-message card; false leaves the command to the group workflows.
func (s *server) processChatDirectMessage(ctx context.Context, record domain.ChatIntegrationRecord, job domain.ChatJob, command chatInboundCommand) (bool, error) {
	switch command.Kind {
	case "dm_message":
		return true, s.chatDMMessage(ctx, record, job, command)
	case "card_action":
		stored, err := s.store.GetChatAction(ctx, record.WorkspaceKey, record.ConnectionID, command.ActionID)
		if err != nil {
			return false, nil
		}
		var action chatDMAction
		if json.Unmarshal(stored.Payload, &action) != nil || !strings.HasPrefix(action.Kind, "dm_") {
			return false, nil
		}
		return true, s.chatDMCardAction(ctx, record, job, command, stored, action)
	}
	return false, nil
}

func chatRawString(values map[string]json.RawMessage, key string) string {
	var result string
	_ = json.Unmarshal(values[key], &result)
	return result
}

// chatDMLinked is the member's account linked through this bot.
func (s *server) chatDMLinked(ctx context.Context, record domain.ChatIntegrationRecord, openID string) (domain.ChatIdentity, bool, error) {
	identity, err := s.store.ChatIdentityForOpenID(ctx, record.WorkspaceKey, record.ConnectionID, openID)
	if errors.Is(err, sql.ErrNoRows) {
		return identity, false, nil
	}
	if err != nil {
		return identity, false, err
	}
	return identity, identity.TenantKey == record.TenantKey && identity.UserID != "", nil
}

func chatDMCommandIs(text string, names ...string) bool {
	text = strings.ToLower(strings.TrimSpace(strings.Trim(strings.TrimSpace(text), "/")))
	return slices.Contains(names, text)
}

// chatDMNewIssueTitle reads "new issue <title>" / "新建事项 <title>".
func chatDMNewIssueTitle(text string) (string, bool) {
	trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), "/"))
	for _, prefix := range []string{"新建事项", "new issue"} {
		if len(trimmed) >= len(prefix) && strings.EqualFold(trimmed[:len(prefix)], prefix) {
			rest := trimmed[len(prefix):]
			if rest != "" && !strings.HasPrefix(rest, " ") && !strings.HasPrefix(rest, "：") && !strings.HasPrefix(rest, ":") && prefix == "new issue" {
				continue
			}
			rest = strings.TrimSpace(strings.TrimLeft(rest, ":："))
			if utf8.RuneCountInString(rest) > 1000 {
				rest = string([]rune(rest)[:1000])
			}
			return rest, true
		}
	}
	return "", false
}

func (s *server) chatDMMessage(ctx context.Context, record domain.ChatIntegrationRecord, job domain.ChatJob, command chatInboundCommand) error {
	text, resources, readable := chatDMContent(chatString(command.Values, "messageType"), chatRawString(command.Values, "content"))
	zh := chatDMChinese(text, record.Provider)
	identity, linked, err := s.chatDMLinked(ctx, record, command.OpenID)
	if err != nil {
		return err
	}
	if !linked {
		_, _, err = s.chatDMSend(ctx, record, command.OpenID, command.MessageID, job.DedupKey+":link", s.chatDMLinkCard(record.WorkspaceKey, zh))
		return err
	}
	dm := chatDMRequest{record: record, identity: identity, command: command, job: job, zh: zh}
	if !readable {
		return s.chatDMNotice(ctx, dm, chatDMText(zh, "Flow can read text, rich text, images and files here.", "这里支持文字、富文本、图片和文件。"))
	}
	if chatDMCommandIs(text, "switch workspace", "切换工作区") {
		targets, err := s.chatDMTargets(ctx, record, identity)
		if err != nil {
			return err
		}
		if len(targets) < 2 {
			return s.chatDMNotice(ctx, dm, chatDMText(zh, "You're linked in one workspace here: "+targets[0].Name+".", "你在这里只关联了一个工作区："+targets[0].Name+"。"))
		}
		return s.chatDMWorkspaceChoice(ctx, dm, targets, nil)
	}
	var thread *chatDMThread
	for _, id := range []string{chatString(command.Values, "parentId"), chatString(command.Values, "rootId")} {
		if id == "" {
			continue
		}
		if found, ok := s.chatDMThreadFor(ctx, record, identity, id); ok {
			thread = &found
			break
		}
	}
	target, ok, err := s.chatDMChooseTarget(ctx, dm, thread)
	if !ok || err != nil {
		return err
	}
	dm.target = target
	if handled, err := s.chatDMAuthorize(ctx, &dm); handled {
		return err
	}
	if thread != nil && thread.Kind == "notification" {
		return s.chatDMComment(ctx, dm, *thread, text)
	}
	if title, ok := chatDMNewIssueTitle(text); ok && len(resources) == 0 {
		return s.chatDMNewIssueForm(ctx, dm, title)
	}
	switch {
	case len(resources) == 0 && chatDMCommandIs(text, "my issues", "我的事项"):
		return s.chatDMMyIssues(ctx, dm)
	case len(resources) == 0 && chatDMCommandIs(text, "inbox", "收件箱"):
		return s.chatDMInbox(ctx, dm)
	}
	if link, ok := s.chatDMFlowLink(text); ok && len(resources) == 0 {
		return s.chatDMLinkPreview(ctx, dm, link)
	}
	sessionID := ""
	if thread != nil {
		sessionID = thread.SessionID
	}
	return s.chatDMStartReply(ctx, dm, sessionID, text, resources)
}

// chatDMAuthorize checks the member may still act in the target workspace,
// as every chat action does, and that the workspace lets its content reach
// chat applications; it answers the member when not (handled).
func (s *server) chatDMAuthorize(ctx context.Context, dm *chatDMRequest) (bool, error) {
	if err := s.chatAuthorizedMutation(dm.authContext(ctx), dm.target.Workspace, dm.identity.UserID, http.MethodGet, "/api/issue-records/bootstrap", nil, &dm.metadata); err != nil {
		return true, s.chatDMFailure(ctx, *dm, err)
	}
	if settings, ok := s.store.WorkspaceSettingsMetadata(dm.target.Workspace); !ok || settings.WorkspaceSettings.HIPAACompliance {
		return true, s.chatDMNotice(ctx, *dm, chatDMText(dm.zh, "This workspace keeps its content out of chat apps. Open Flow to continue: ", "这个工作区不允许把内容发送到聊天应用。请在 Flow 中继续：")+s.chatDMWorkspaceURL(dm.target.Workspace, ""))
	}
	return false, nil
}

func (s *server) chatDMFailure(ctx context.Context, dm chatDMRequest, err error) error {
	var failure *chatMutationError
	if !errors.As(err, &failure) || failure.Status < 400 || failure.Status >= 500 {
		return err
	}
	if failure.Message == chatReconnectMessage || failure.Status == http.StatusUnauthorized {
		card := s.chatDMLinkCard(dm.target.Workspace, dm.zh)
		card["elements"] = append([]map[string]any{chatCardText(chatDMText(dm.zh, "Your linked chat account has expired.", "你关联的聊天账号已过期。"))}, card["elements"].([]map[string]any)...)
		_, _, sendErr := s.chatDMSend(ctx, dm.record, dm.command.OpenID, dm.command.MessageID, dm.job.DedupKey+":expired", card)
		return sendErr
	}
	return s.chatDMNotice(ctx, dm, chatDMText(dm.zh, "Your Flow access to this workspace has changed. Open Flow and check your access.", "你在这个工作区的 Flow 权限已变化。请打开 Flow 检查你的权限。"))
}

// chatDMNotice replies with a short message card.
func (s *server) chatDMNotice(ctx context.Context, dm chatDMRequest, text string) error {
	_, _, err := s.chatDMSend(ctx, dm.record, dm.command.OpenID, dm.command.MessageID, dm.job.DedupKey+":notice", chatDMNoticeCard("Flow", text))
	return err
}

// Conversation state.

func chatDMThreadID(record domain.ChatIntegrationRecord, messageID string) string {
	return chatDMThreadPrefix + chatKey(record.ConnectionID, messageID)
}

func (s *server) chatDMPutState(ctx context.Context, record domain.ChatIntegrationRecord, userID, id string, value any) error {
	payload, _ := json.Marshal(value)
	if _, err := s.store.ConsumeChatAction(ctx, record.WorkspaceKey, record.ConnectionID, id); err != nil {
		return err
	}
	return s.store.PutChatAction(ctx, domain.ChatAction{ID: id, WorkspaceKey: record.WorkspaceKey, ConnectionID: record.ConnectionID, UserID: userID, Payload: payload, ExpiresAt: time.Now().UTC().Add(chatDMStateLifetime)})
}

func (s *server) chatDMRememberThread(ctx context.Context, record domain.ChatIntegrationRecord, userID, messageID string, thread chatDMThread) {
	if messageID == "" {
		return
	}
	if err := s.chatDMPutState(ctx, record, userID, chatDMThreadID(record, messageID), thread); err != nil {
		log.Printf("Chat direct message workspace=%s: %v", record.WorkspaceKey, err)
	}
}

func (s *server) chatDMThreadFor(ctx context.Context, record domain.ChatIntegrationRecord, identity domain.ChatIdentity, messageID string) (chatDMThread, bool) {
	stored, err := s.store.GetChatAction(ctx, record.WorkspaceKey, record.ConnectionID, chatDMThreadID(record, messageID))
	var thread chatDMThread
	if err != nil || stored.UserID != identity.UserID || json.Unmarshal(stored.Payload, &thread) != nil || thread.OpenID != identity.OpenID || thread.Kind != "session" && thread.Kind != "notification" {
		return chatDMThread{}, false
	}
	return thread, true
}

// rememberChatNotificationCard keeps which issue a personal notification card
// is about, so a reply to it comments there.
func (s *server) rememberChatNotificationCard(ctx context.Context, record domain.ChatIntegrationRecord, identity domain.ChatIdentity, notificationID, messageID string) {
	if messageID == "" {
		return
	}
	notification, err := s.store.NotificationRecord(ctx, record.WorkspaceKey, identity.UserID, notificationID)
	if err != nil || notification.IssueID == "" || notification.ReviewID != "" {
		return
	}
	s.chatDMRememberThread(ctx, record, identity.UserID, messageID, chatDMThread{Kind: "notification", OpenID: identity.OpenID, WorkspaceKey: record.WorkspaceKey, IssueID: notification.IssueID})
}

func chatDMPreferenceID(record domain.ChatIntegrationRecord, openID string) string {
	return chatDMWorkspacePrefix + chatKey(record.ConnectionID, openID)
}

func (s *server) chatDMPreference(ctx context.Context, record domain.ChatIntegrationRecord, identity domain.ChatIdentity) (string, bool) {
	stored, err := s.store.GetChatAction(ctx, record.WorkspaceKey, record.ConnectionID, chatDMPreferenceID(record, identity.OpenID))
	var preference chatDMThread
	if err != nil || stored.UserID != identity.UserID || json.Unmarshal(stored.Payload, &preference) != nil || preference.Kind != "workspace" || preference.OpenID != identity.OpenID {
		return "", false
	}
	return preference.WorkspaceKey, true
}

// Workspaces.

// chatDMTargets lists the workspaces the member may use with this bot: its
// own, and the others of the same tenant where they linked their account and
// the app is connected.
func (s *server) chatDMTargets(ctx context.Context, record domain.ChatIntegrationRecord, identity domain.ChatIdentity) ([]chatDMTarget, error) {
	targets := []chatDMTarget{{Workspace: record.WorkspaceKey, Identity: identity}}
	linked, err := s.store.ChatIdentitiesForUser(ctx, identity.UserID)
	if err != nil {
		return nil, err
	}
	for _, item := range linked {
		if item.Identity.TenantKey != record.TenantKey || slices.ContainsFunc(targets, func(target chatDMTarget) bool { return target.Workspace == item.WorkspaceKey }) {
			continue
		}
		other, err := s.store.ChatIntegrationByID(ctx, item.Provider, item.ConnectionID)
		if err != nil || !other.Connected || other.TenantKey != record.TenantKey || other.WorkspaceKey != item.WorkspaceKey {
			continue
		}
		targets = append(targets, chatDMTarget{Workspace: item.WorkspaceKey, Identity: item.Identity})
	}
	for index := range targets {
		targets[index].Name = targets[index].Workspace
		if metadata, ok := s.store.WorkspaceSettingsMetadata(targets[index].Workspace); ok && metadata.Workspace.Name != "" {
			targets[index].Name = metadata.Workspace.Name
		}
	}
	return targets, nil
}

func chatDMFindTarget(targets []chatDMTarget, workspace string) (chatDMTarget, bool) {
	index := slices.IndexFunc(targets, func(target chatDMTarget) bool { return target.Workspace == workspace })
	if index < 0 {
		return chatDMTarget{}, false
	}
	return targets[index], true
}

// chatDMChooseTarget is the workspace of the message: its conversation's, the
// member's choice, or the only one; otherwise it asks (ok false).
func (s *server) chatDMChooseTarget(ctx context.Context, dm chatDMRequest, thread *chatDMThread) (chatDMTarget, bool, error) {
	targets, err := s.chatDMTargets(ctx, dm.record, dm.identity)
	if err != nil {
		return chatDMTarget{}, false, err
	}
	if thread != nil {
		if target, ok := chatDMFindTarget(targets, thread.WorkspaceKey); ok {
			return target, true, nil
		}
		return chatDMTarget{}, false, s.chatDMNotice(ctx, dm, chatDMText(dm.zh, "This conversation belongs to a workspace you're no longer linked in here.", "这段对话属于一个你已不再关联的工作区。"))
	}
	if workspace, ok := s.chatDMPreference(ctx, dm.record, dm.identity); ok {
		if target, ok := chatDMFindTarget(targets, workspace); ok {
			return target, true, nil
		}
	}
	if len(targets) == 1 {
		return targets[0], true, nil
	}
	pending := dm.command
	return chatDMTarget{}, false, s.chatDMWorkspaceChoice(ctx, dm, targets, &pending)
}

func (s *server) chatDMWorkspaceChoice(ctx context.Context, dm chatDMRequest, targets []chatDMTarget, pending *chatInboundCommand) error {
	if pending != nil {
		if raw, _ := json.Marshal(pending); len(raw) > 56<<10 {
			pending = nil
		}
	}
	current, _ := s.chatDMPreference(ctx, dm.record, dm.identity)
	buttons := []map[string]any{}
	actions := []chatDMPendingAction{}
	for _, target := range targets[:min(len(targets), 10)] {
		id := "chat_action_" + chatKey(dm.record.ConnectionID, dm.job.DedupKey, "workspace", target.Workspace)
		kind := "default"
		if target.Workspace == current {
			kind = "primary"
		}
		buttons = append(buttons, chatDMActionButton(target.Name, id, kind))
		actions = append(actions, chatDMPendingAction{ID: id, Action: chatDMAction{Kind: "dm_workspace", OpenID: dm.command.OpenID, WorkspaceKey: target.Workspace, Pending: pending, Chinese: dm.zh}})
	}
	card := chatDMCard(chatDMText(dm.zh, "Choose a workspace", "选择工作区"), "blue",
		chatCardText(chatDMText(dm.zh, "You're linked in several Flow workspaces. Which one should Flow use in this chat? Send “switch workspace” to change it later.", "你关联了多个 Flow 工作区。这个对话要使用哪一个？之后发送“切换工作区”即可更改。")),
		chatDMButtons(buttons...))
	_, err := s.chatDMSendWithActions(ctx, dm, dm.job.DedupKey+":workspace", card, actions, chatDMFormLifetime)
	return err
}

// chatDMSendWithActions saves the card's actions, replies with the card and
// ties the actions to the message it became.
func (s *server) chatDMSendWithActions(ctx context.Context, dm chatDMRequest, key string, card map[string]any, actions []chatDMPendingAction, lifetime time.Duration) (string, error) {
	expires := time.Now().UTC().Add(lifetime)
	stored := make([]domain.ChatAction, 0, len(actions))
	for _, item := range actions {
		payload, _ := json.Marshal(item.Action)
		action := domain.ChatAction{ID: item.ID, WorkspaceKey: dm.record.WorkspaceKey, ConnectionID: dm.record.ConnectionID, UserID: dm.identity.UserID, Payload: payload, ExpiresAt: expires}
		if err := s.store.PutChatAction(ctx, action); err != nil {
			return "", err
		}
		stored = append(stored, action)
	}
	messageID, chatID, err := s.chatDMSend(ctx, dm.record, dm.command.OpenID, dm.command.MessageID, key, card)
	if err != nil {
		return "", err
	}
	chatID = firstNonEmpty(chatID, dm.command.ChatID)
	for index, item := range actions {
		item.Action.PersonalChatID, item.Action.PersonalMessage = chatID, messageID
		stored[index].Payload, _ = json.Marshal(item.Action)
		if err := s.store.UpdateChatAction(ctx, stored[index]); err != nil {
			return "", err
		}
	}
	return messageID, nil
}

// Card actions.

func (s *server) chatDMCardAction(ctx context.Context, record domain.ChatIntegrationRecord, job domain.ChatJob, command chatInboundCommand, stored domain.ChatAction, action chatDMAction) error {
	zh := action.Chinese
	identity, linked, err := s.chatDMLinked(ctx, record, command.OpenID)
	if err != nil {
		return err
	}
	dm := chatDMRequest{record: record, identity: identity, command: command, job: job, zh: zh}
	if !linked {
		_, _, err = s.chatDMSend(ctx, record, command.OpenID, "", job.DedupKey+":link", s.chatDMLinkCard(record.WorkspaceKey, zh))
		return err
	}
	if stored.UserID != identity.UserID || action.OpenID != command.OpenID || action.PersonalChatID != command.ChatID || action.PersonalMessage != command.MessageID {
		return nil
	}
	// command.MessageID is the card: notices reply to it, results update it.
	targets, err := s.chatDMTargets(ctx, record, identity)
	if err != nil {
		return err
	}
	target, ok := chatDMFindTarget(targets, action.WorkspaceKey)
	if !ok {
		return s.chatDMNotice(ctx, dm, chatDMText(zh, "You're no longer linked in this workspace here.", "你已不再关联这个工作区。"))
	}
	dm.target = target
	switch action.Kind {
	case "dm_workspace":
		if err := s.chatDMPutState(ctx, record, identity.UserID, chatDMPreferenceID(record, identity.OpenID), chatDMThread{Kind: "workspace", OpenID: identity.OpenID, WorkspaceKey: target.Workspace}); err != nil {
			return err
		}
		_, _ = s.store.ConsumeChatAction(ctx, record.WorkspaceKey, record.ConnectionID, command.ActionID)
		if err := s.chatDMUpdate(ctx, record, command.MessageID, chatDMNoticeCard(chatDMText(zh, "Workspace", "工作区"), chatDMText(zh, "Flow uses "+target.Name+" in this chat. Send “switch workspace” to change it.", "这个对话使用 "+target.Name+"。发送“切换工作区”即可更改。"))); err != nil {
			return err
		}
		if action.Pending != nil && action.Pending.Kind == "dm_message" && action.Pending.OpenID == command.OpenID {
			return s.chatDMMessage(ctx, record, job, *action.Pending)
		}
		return nil
	case "dm_approval":
		return s.chatDMAnswerApproval(ctx, dm, action)
	case "dm_create":
		return s.chatDMCreateIssue(ctx, dm, action)
	}
	return nil
}

func (s *server) chatDMAnswerApproval(ctx context.Context, dm chatDMRequest, action chatDMAction) error {
	record, command := dm.record, dm.command
	if used, err := s.store.ConsumeChatAction(ctx, record.WorkspaceKey, record.ConnectionID, command.ActionID); err != nil || !used {
		return err
	}
	if action.Sibling != "" {
		_, _ = s.store.ConsumeChatAction(ctx, record.WorkspaceKey, record.ConnectionID, action.Sibling)
	}
	w := newChatDMStream(nil)
	path := "/api/agent/sessions/" + url.PathEscape(action.SessionID) + "/approvals/" + url.PathEscape(action.ApprovalID)
	if err := s.chatDMAgentCall(ctx, dm, http.MethodPost, path, map[string]string{"decision": action.Decision}, w); err != nil {
		var failure *chatMutationError
		if errors.As(err, &failure) {
			return s.chatDMFailure(ctx, dm, err)
		}
		return err
	}
	resolution := "expired"
	if w.status == http.StatusOK || w.status == http.StatusAccepted {
		resolution = map[string]string{"approve": "approved", "reject": "rejected"}[action.Decision]
	}
	if w.status == http.StatusUnauthorized {
		return s.chatDMFailure(ctx, dm, &chatMutationError{Status: w.status, Message: chatReconnectMessage})
	}
	return s.chatDMUpdate(ctx, record, command.MessageID, chatDMApprovalCard(action.Label, action.Preview, "", "", resolution, dm.zh))
}

// Agent replies.

// chatDMStartReply answers with the Flow Agent: a new chat, or the next
// message of sessionID. The reply runs in the background; its card follows it.
func (s *server) chatDMStartReply(ctx context.Context, dm chatDMRequest, sessionID, text string, resources []chatDMResource) error {
	if sessionID != "" {
		if _, busy := chatDMActive.LoadOrStore(sessionID, struct{}{}); busy {
			return s.chatDMNotice(ctx, dm, chatDMText(dm.zh, "Flow is still answering your previous message in this conversation. Reply again when it has finished.", "Flow 还在回复这段对话中的上一条消息，请等它完成后再回复。"))
		}
	}
	started := false
	defer func() {
		if !started && sessionID != "" {
			chatDMActive.Delete(sessionID)
		}
	}()
	attachments, problem, err := s.chatDMAttachments(ctx, dm.record, dm.command.MessageID, resources, dm.zh)
	if err != nil {
		return err
	}
	if problem != "" {
		return s.chatDMNotice(ctx, dm, problem)
	}
	message := text
	if strings.TrimSpace(message) == "" {
		message = chatDMText(dm.zh, "See the attached file.", "请看附件。")
	}
	cardID, _, err := s.chatDMSend(ctx, dm.record, dm.command.OpenID, dm.command.MessageID, dm.job.DedupKey+":reply", chatDMReplyCard(chatDMReplyView{Status: "working"}, dm.zh))
	if err != nil {
		return err
	}
	method, path := http.MethodPost, "/api/agent/sessions/stream"
	var body any = agentSessionInput{Message: message, Location: "page", Attachments: attachments}
	if sessionID != "" {
		path = "/api/agent/sessions/" + url.PathEscape(sessionID) + "/messages/stream"
		body = agentMessageInput{Message: message, Attachments: attachments}
	}
	reply := &chatDMReply{s: s, dm: dm, cardID: cardID, sessionID: sessionID, wake: make(chan struct{}, 1), approvalCards: map[string]chatDMApprovalCardState{}, resolved: map[string]string{}}
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), chatDMRunLimit)
	chatDMReplies.Add(1)
	started = true
	go func() {
		defer chatDMReplies.Done()
		defer cancel()
		reply.run(runCtx, method, path, body)
	}()
	return nil
}

// chatDMAgentCall runs an Agent route as the member in the target workspace,
// through the same authorization, rate limit and handler as the app.
func (s *server) chatDMAgentCall(ctx context.Context, dm chatDMRequest, method, path string, input any, w http.ResponseWriter) error {
	user, err := s.store.UserByID(ctx, dm.identity.UserID)
	if err != nil || !user.Active {
		return &chatMutationError{Status: http.StatusForbidden, Message: "The linked Flow account is unavailable"}
	}
	ctx = dm.authContext(ctx)
	ctx = context.WithValue(ctx, authUserContextKey{}, user)
	ctx = context.WithValue(ctx, workspaceKeyContextKey{}, dm.target.Workspace)
	ctx = store.ContextWithActor(ctx, user)
	ctx = context.WithValue(ctx, agentBaseURLKey{}, s.chatAppOrigin())
	ctx = context.WithValue(ctx, chatDMOriginKey{}, dm.record.Provider)
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	r, err := http.NewRequestWithContext(ctx, method, "http://flow.internal"+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Workspace-Key", dm.target.Workspace)
	if _, ok := chatIdentityAuthentication(r); !ok && !s.authDisabled {
		return &chatMutationError{Status: http.StatusForbidden, Message: chatReconnectMessage}
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	var handler http.HandlerFunc
	switch {
	case method == http.MethodPost && r.URL.Path == "/api/agent/sessions/stream":
		handler = s.createAgentSessionStream
	case method == http.MethodPost && len(parts) == 6 && parts[0] == "api" && parts[1] == "agent" && parts[2] == "sessions" && parts[3] != "" && parts[4] == "messages" && parts[5] == "stream":
		r.SetPathValue("id", parts[3])
		handler = s.createAgentSessionMessageStream
	case method == http.MethodPost && len(parts) == 6 && parts[0] == "api" && parts[1] == "agent" && parts[2] == "sessions" && parts[3] != "" && parts[4] == "approvals" && parts[5] != "":
		r.SetPathValue("id", parts[3])
		r.SetPathValue("approvalId", parts[5])
		handler = s.resolveAgentApproval
	}
	if handler == nil || r.URL.RawQuery != "" {
		return errors.New("unsupported chat operation")
	}
	if !s.authorizeWorkspaceRequest(w, r, user) {
		return nil
	}
	if !s.authDisabled {
		limiter := s.apiRateLimits()
		subject := apiRateSubject{Category: apiRateSession, Key: "session:" + user.ID}
		if !limiter.exempt(subject) && !limiter.check(r.Context(), r, subject).write(w) {
			return nil
		}
	}
	handler(w, r)
	return nil
}

// chatDMStream receives an Agent route's response: its server-sent events as
// they are written, or an error body.
type chatDMStream struct {
	mu      sync.Mutex
	header  http.Header
	status  int
	stream  bool
	body    bytes.Buffer
	pending []byte
	observe func(agentStreamEvent)
}

func newChatDMStream(observe func(agentStreamEvent)) *chatDMStream {
	return &chatDMStream{header: http.Header{}, observe: observe}
}

func (w *chatDMStream) Header() http.Header { return w.header }

func (w *chatDMStream) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.stream = strings.HasPrefix(w.header.Get("Content-Type"), "text/event-stream")
	}
}

func (w *chatDMStream) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.WriteHeader(http.StatusOK)
	if !w.stream {
		if w.body.Len() < 64<<10 {
			w.body.Write(data)
		}
		return len(data), nil
	}
	w.pending = append(w.pending, data...)
	for {
		index := bytes.Index(w.pending, []byte("\n\n"))
		if index < 0 {
			break
		}
		frame := w.pending[:index]
		w.pending = w.pending[index+2:]
		for line := range bytes.SplitSeq(frame, []byte("\n")) {
			payload, ok := bytes.CutPrefix(line, []byte("data: "))
			if !ok {
				continue
			}
			var event agentStreamEvent
			if json.Unmarshal(payload, &event) == nil && w.observe != nil {
				w.observe(event)
			}
		}
	}
	if len(w.pending) > 16<<20 {
		w.pending = nil
	}
	return len(data), nil
}

func (w *chatDMStream) Flush() {}

func (w *chatDMStream) errorMessage() string {
	var payload struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(w.body.Bytes(), &payload)
	return payload.Error
}

// chatDMRefusal says why the agent did not run for the member.
func chatDMRefusal(status int, message string, zh bool) string {
	switch {
	case status == http.StatusServiceUnavailable && message == "Flow Agent is not configured":
		return chatDMText(zh, "Flow Agent isn't set up in this workspace. Ask a workspace admin to configure it.", "这个工作区还没有配置 Flow Agent，请联系工作区管理员。")
	case status == http.StatusForbidden && strings.Contains(message, "Agent access is disabled"):
		return chatDMText(zh, "Flow Agent isn't available to you in this workspace.", "你在这个工作区无法使用 Flow Agent。")
	case status == http.StatusTooManyRequests:
		return chatDMText(zh, "You're sending requests faster than Flow allows. Wait a moment and try again.", "请求过于频繁，请稍后再试。")
	case status == http.StatusUnauthorized:
		return chatReconnectMessage
	case message != "":
		return message
	}
	return chatDMText(zh, fmt.Sprintf("Flow could not answer (HTTP %d).", status), fmt.Sprintf("Flow 无法回复（HTTP %d）。", status))
}

type chatDMApprovalCardState struct {
	MessageID string
	Label     string
	Preview   string
}

type chatDMApprovalRequest struct {
	ID      string
	Label   string
	Preview string
}

// chatDMReply follows one agent reply and keeps its card current.
type chatDMReply struct {
	s      *server
	dm     chatDMRequest
	cardID string
	wake   chan struct{}

	mu            sync.Mutex
	sessionID     string
	slug          string
	threaded      bool
	text          string
	steps         []chatDMStep
	approvals     []chatDMApprovalRequest
	approvalCards map[string]chatDMApprovalCardState
	resolved      map[string]string
	final         *domain.AgentSession
	failure       string
	finished      bool
}

func (r *chatDMReply) poke() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *chatDMReply) observe(event agentStreamEvent) {
	r.mu.Lock()
	defer r.poke()
	defer r.mu.Unlock()
	switch event.Type {
	case "session.started":
		if event.Session != nil {
			r.sessionID, r.slug = event.Session.ID, event.Session.SlugID
			chatDMActive.Store(r.sessionID, struct{}{})
		}
	case "text.delta":
		r.text += event.Delta
	case "text.replaced":
		r.text = event.Delta
	case "tool.started", "tool.delta", "tool.completed", "tool.approval_required", "tool.approval_resolved":
		if event.Part == nil || event.Part.ToolCall == nil {
			return
		}
		step := chatDMStep{ID: event.Part.ID, Label: chatDMToolLabel(event.Part.ToolCall), Status: firstNonEmpty(event.Part.Status, event.Part.ToolCall.Status)}
		if index := slices.IndexFunc(r.steps, func(item chatDMStep) bool { return item.ID == step.ID }); index >= 0 {
			r.steps[index] = step
		} else {
			r.steps = append(r.steps, step)
		}
		if event.Type == "tool.approval_required" && event.ApprovalID != "" {
			r.approvals = append(r.approvals, chatDMApprovalRequest{ID: event.ApprovalID, Label: step.Label, Preview: chatDMApprovalPreview(event.Part.ToolCall, r.dm.zh)})
		}
		if event.Type == "tool.approval_resolved" && event.ApprovalID != "" {
			r.resolved[event.ApprovalID] = event.Decision
		}
	case "session.completed":
		r.final = event.Session
	case "error":
		r.failure = event.Error
	}
}

func (r *chatDMReply) run(ctx context.Context, method, path string, body any) {
	done := make(chan struct{})
	go r.follow(ctx, done)
	w := newChatDMStream(r.observe)
	err := r.s.chatDMAgentCall(ctx, r.dm, method, path, body, w)
	r.mu.Lock()
	switch {
	case err != nil:
		var failure *chatMutationError
		if errors.As(err, &failure) {
			r.failure = chatDMRefusal(failure.Status, failure.Message, r.dm.zh)
		} else {
			log.Printf("Chat direct message workspace=%s: %v", r.dm.target.Workspace, err)
			r.failure = chatDMText(r.dm.zh, "Flow could not answer. Try again in a moment.", "Flow 暂时无法回复，请稍后再试。")
		}
	case w.status != 0 && !w.stream:
		r.failure = chatDMRefusal(w.status, w.errorMessage(), r.dm.zh)
	case w.status == 0:
		r.failure = chatDMText(r.dm.zh, "Flow could not answer. Try again in a moment.", "Flow 暂时无法回复，请稍后再试。")
	}
	r.finished = true
	r.mu.Unlock()
	r.poke()
	<-done
	r.mu.Lock()
	if r.sessionID != "" {
		chatDMActive.Delete(r.sessionID)
	}
	r.mu.Unlock()
}

// view is what the card shows now.
func (r *chatDMReply) view() chatDMReplyView {
	view := chatDMReplyView{Text: r.text, Steps: slices.Clone(r.steps), Status: "working"}
	if r.slug != "" {
		view.Link = r.s.chatDMWorkspaceURL(r.dm.target.Workspace, "/agent/"+url.PathEscape(r.slug))
	}
	for _, step := range r.steps {
		if step.Status == "pending" {
			view.Status = "waiting"
		}
	}
	if r.final != nil {
		final := chatDMFinalView(*r.final, r.dm.zh)
		final.Link = view.Link
		return final
	}
	if r.finished {
		view.Status = "failed"
		view.Failure = r.failure
		if r.failure == "" || r.failure == "Generation stopped" || r.failure == context.Canceled.Error() {
			view.Status, view.Failure = "stopped", ""
		}
	}
	return view
}

// chatDMFinalView reads the saved reply: the answer as the write ledger left
// it, the steps it ran, the question it stopped on, or how it failed.
func chatDMFinalView(session domain.AgentSession, zh bool) chatDMReplyView {
	view := chatDMReplyView{Status: "done"}
	var message *domain.AgentMessage
	for index := len(session.Messages) - 1; index >= 0; index-- {
		if session.Messages[index].Role == "assistant" {
			message = &session.Messages[index]
			break
		}
	}
	if message == nil {
		return view
	}
	view.Text = message.Content
	texts := []string{}
	for _, part := range message.Parts {
		switch {
		case part.ToolCall != nil:
			view.Steps = append(view.Steps, chatDMStep{ID: part.ID, Label: chatDMToolLabel(part.ToolCall), Status: firstNonEmpty(part.Status, part.ToolCall.Status)})
		case part.Question != nil:
			lines := []string{part.Text}
			for index, option := range part.Question.Options {
				lines = append(lines, strconv.Itoa(index+1)+". "+option.Label)
			}
			lines = append(lines, chatDMText(zh, "_Reply to this message with your answer._", "_回复这条消息作答。_"))
			view.Questions = append(view.Questions, strings.Join(lines, "\n"))
		case part.Elicitation != nil:
			view.Questions = append(view.Questions, chatDMText(zh, "Flow needs more input from you. Open the chat in Flow to answer.", "Flow 需要你补充信息，请在 Flow 中打开对话作答。"))
		case part.Type == "error":
			view.Status, view.Failure = "failed", part.Text
			if part.Text == "Generation stopped" {
				view.Status, view.Failure = "stopped", ""
			}
		case part.Type == "text" && part.Text != "":
			texts = append(texts, part.Text)
		}
	}
	if view.Text == "" {
		view.Text = strings.Join(texts, "\n\n")
	}
	return view
}

// follow updates the card while the reply runs (at most every
// chatDMUpdateEvery), sends approval cards, and stops the reply when the
// member's account is unlinked. It ends after the final update.
func (r *chatDMReply) follow(ctx context.Context, done chan struct{}) {
	defer close(done)
	record, zh := r.dm.record, r.dm.zh
	var last []byte
	var lastAt time.Time
	lastCheck := time.Now()
	check := time.NewTicker(chatDMLinkCheckEvery)
	defer check.Stop()
	// The reply ends on its own (an in-process reply outlives its request;
	// a worker reply's request ends with ctx), so this waits for it rather
	// than for ctx.
	for {
		select {
		case <-r.wake:
		case <-check.C:
		}
		r.mu.Lock()
		finished := r.finished
		r.mu.Unlock()
		if wait := chatDMUpdateEvery - time.Since(lastAt); !finished && wait > 0 {
			time.Sleep(wait)
		}
		if time.Since(lastCheck) >= chatDMLinkCheckEvery {
			lastCheck = time.Now()
			if !r.stillLinked(ctx) {
				r.stopForUnlink(ctx)
				return
			}
		}
		r.mu.Lock()
		finished = r.finished
		view := r.view()
		sessionID, threaded := r.sessionID, r.threaded
		approvals := r.approvals
		r.approvals = nil
		resolved := r.resolved
		r.resolved = map[string]string{}
		r.mu.Unlock()
		persist := context.WithoutCancel(ctx)
		if sessionID != "" && !threaded {
			thread := chatDMThread{Kind: "session", OpenID: r.dm.command.OpenID, WorkspaceKey: r.dm.target.Workspace, SessionID: sessionID}
			r.s.chatDMRememberThread(persist, record, r.dm.identity.UserID, r.dm.command.MessageID, thread)
			r.s.chatDMRememberThread(persist, record, r.dm.identity.UserID, r.cardID, thread)
			r.mu.Lock()
			r.threaded = true
			r.mu.Unlock()
		}
		for _, approval := range approvals {
			r.sendApproval(persist, sessionID, approval)
		}
		for approvalID, decision := range resolved {
			if card, ok := r.approvalCards[approvalID]; ok {
				if err := r.s.chatDMUpdate(persist, record, card.MessageID, chatDMApprovalCard(card.Label, card.Preview, "", "", decision, zh)); err != nil {
					log.Printf("Chat direct message workspace=%s: %v", record.WorkspaceKey, err)
				}
			}
		}
		raw, _ := json.Marshal(chatDMReplyCard(view, zh))
		shown := bytes.Equal(raw, last)
		if !shown {
			if err := r.s.chatDMUpdate(persist, record, r.cardID, chatDMReplyCard(view, zh)); err != nil {
				log.Printf("Chat direct message workspace=%s: %v", record.WorkspaceKey, err)
			} else {
				last, shown = raw, true
			}
			lastAt = time.Now()
		}
		if finished {
			// The member wrote and read this exchange in the chat: it is not
			// unread on their Agent page.
			if shown && sessionID != "" {
				r.s.chatDMMarkSessionRead(persist, r.dm.target.Workspace, r.dm.identity.UserID, sessionID)
			}
			return
		}
	}
}

func (r *chatDMReply) sendApproval(ctx context.Context, sessionID string, approval chatDMApprovalRequest) {
	dm := r.dm
	approveID := "chat_action_" + chatKey(dm.record.ConnectionID, approval.ID, "approve")
	rejectID := "chat_action_" + chatKey(dm.record.ConnectionID, approval.ID, "reject")
	base := chatDMAction{Kind: "dm_approval", OpenID: dm.command.OpenID, WorkspaceKey: dm.target.Workspace, SessionID: sessionID, ApprovalID: approval.ID, Label: approval.Label, Preview: approval.Preview, Chinese: dm.zh}
	approve, reject := base, base
	approve.Decision, approve.Sibling = "approve", rejectID
	reject.Decision, reject.Sibling = "reject", approveID
	card := chatDMApprovalCard(approval.Label, approval.Preview, approveID, rejectID, "", dm.zh)
	messageID, err := r.s.chatDMSendWithActions(ctx, dm, dm.job.DedupKey+":approval:"+approval.ID, card, []chatDMPendingAction{{ID: approveID, Action: approve}, {ID: rejectID, Action: reject}}, agentApprovalTimeout+time.Minute)
	if err != nil {
		log.Printf("Chat direct message workspace=%s: %v", dm.record.WorkspaceKey, err)
		return
	}
	r.approvalCards[approval.ID] = chatDMApprovalCardState{MessageID: messageID, Label: approval.Label, Preview: approval.Preview}
	r.s.chatDMRememberThread(ctx, dm.record, dm.identity.UserID, messageID, chatDMThread{Kind: "session", OpenID: dm.command.OpenID, WorkspaceKey: dm.target.Workspace, SessionID: sessionID})
}

// stillLinked: the member's accounts (through this bot and in the target
// workspace) are still linked.
func (r *chatDMReply) stillLinked(ctx context.Context) bool {
	ctx = context.WithoutCancel(ctx)
	identity, linked, err := r.s.chatDMLinked(ctx, r.dm.record, r.dm.command.OpenID)
	if err != nil {
		return true
	}
	if !linked || identity.UserID != r.dm.identity.UserID {
		return false
	}
	if r.dm.target.Workspace == r.dm.record.WorkspaceKey {
		return true
	}
	linkedTargets, err := r.s.store.ChatIdentitiesForUser(ctx, r.dm.identity.UserID)
	if err != nil {
		return true
	}
	return slices.ContainsFunc(linkedTargets, func(item store.ChatLinkedIdentity) bool {
		return item.WorkspaceKey == r.dm.target.Workspace && item.Identity.OpenID == r.dm.target.Identity.OpenID
	})
}

// stopForUnlink ends the reply of an account that was unlinked, and says so
// on its card.
func (r *chatDMReply) stopForUnlink(ctx context.Context) {
	ctx = context.WithoutCancel(ctx)
	r.mu.Lock()
	sessionID := r.sessionID
	r.mu.Unlock()
	if sessionID != "" && !r.s.stopAgentRun(r.dm.target.Workspace, sessionID) {
		if data, ok := r.s.store.WorkspaceMetadata(r.dm.target.Workspace); ok {
			for _, session := range data.AgentSessions {
				if session.ID == sessionID && session.UserID == r.dm.identity.UserID {
					r.s.stopWorkerChat(ctx, session, r.dm.target.Workspace)
				}
			}
		}
	}
	view := chatDMReplyView{Status: "stopped", Failure: chatDMText(r.dm.zh, "Stopped: this chat account is no longer linked to Flow.", "已停止：这个聊天账号已不再关联 Flow。")}
	if err := r.s.chatDMUpdate(ctx, r.dm.record, r.cardID, chatDMReplyCard(view, r.dm.zh)); err != nil {
		log.Printf("Chat direct message workspace=%s: %v", r.dm.record.WorkspaceKey, err)
	}
}

// chatDMMarkSessionRead marks the member's Agent chat read up to its latest
// change, as opening it in Flow does: the member sent the message and saw
// the reply in the chat.
func (s *server) chatDMMarkSessionRead(ctx context.Context, workspace, userID, sessionID string) {
	user, err := s.store.UserByID(ctx, userID)
	if err != nil {
		return
	}
	err = s.store.MutateWorkspace(store.ContextWithActor(ctx, user), workspace, "agent.session_read", sessionID, nil, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.AgentSessions, func(session domain.AgentSession) bool {
			return session.ID == sessionID && session.UserID == userID && session.DeletedAt == nil
		})
		if index < 0 {
			return errNotFound
		}
		session := &data.AgentSessions[index]
		if agentSessionRead(*session) {
			return store.ErrNoMutation
		}
		readAt := time.Now().UTC()
		if session.UpdatedAt.After(readAt) {
			readAt = session.UpdatedAt
		}
		session.LastReadAt = agentReadStamp(readAt)
		return nil
	})
	if err != nil && !errors.Is(err, store.ErrNoMutation) && !errors.Is(err, errNotFound) {
		log.Printf("Chat direct message workspace=%s: mark read: %v", workspace, err)
	}
}
