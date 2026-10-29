package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Card controls. Group notification cards and personal notification cards
// carry one-click controls: Assign to me, a status select and a priority
// select, next to Open in Flow. A click runs as the clicking member's linked
// Flow account, with that account's permissions; on a group card the
// binding must still allow updates and the issue must still be in its
// scope. After the change the card is redrawn in place (PATCH of the same
// message). Refusals reach the member privately, in their language, with a
// stable code.

const (
	chatQuickAssign   = "quick_assign"
	chatQuickState    = "quick_state"
	chatQuickPriority = "quick_priority"
)

func chatQuickActionKind(kind string) bool {
	return kind == chatQuickAssign || kind == chatQuickState || kind == chatQuickPriority
}

// chatExtendedAction is the stored payload of card controls and message
// shortcut forms. The embedded fields are what the callback router checks
// (chatActionMatches); the others belong to the extended handlers.
type chatExtendedAction struct {
	chatCardAction
	// Personal notification cards: what the card shows, to redraw it.
	CardKey   string `json:"cardKey,omitempty"`
	CardTitle string `json:"cardTitle,omitempty"`
	CardLine  string `json:"cardLine,omitempty"`
	CardURL   string `json:"cardUrl,omitempty"`
	// Message shortcut forms.
	TeamIDs      []string `json:"teamIds,omitempty"`
	SourceChatID string   `json:"sourceChatId,omitempty"`
	// SourceRootID is the root of the thread the message is in, if any.
	SourceRootID string `json:"sourceRootId,omitempty"`
	Verified     bool   `json:"verified,omitempty"`
}

// chatRunExtendedAction handles the actions this file and the message
// shortcut define; handled is false for every other action.
func (s *server) chatRunExtendedAction(ctx context.Context, record domain.ChatIntegrationRecord, identity domain.ChatIdentity, metadata domain.Bootstrap, job domain.ChatJob, command chatInboundCommand, stored domain.ChatAction, action chatCardAction) (bool, error) {
	switch {
	case chatQuickActionKind(action.Kind):
		return true, s.chatRunQuickAction(ctx, record, identity, metadata, job, command, stored)
	case action.Kind == chatMessageCreateKind:
		return true, s.chatCreateFromMessage(ctx, record, identity, metadata, job, command, stored)
	case action.Kind == chatMessageRequestKind:
		return true, s.chatRequestFromForm(ctx, record, identity, metadata, job, command, stored)
	}
	return false, nil
}

// chatActionErrors are the refusals of card controls and message shortcuts:
// code -> English, Chinese.
var chatActionErrors = map[string][2]string{
	"chat_not_linked":               {"Connect your Flow account first.", "请先关联你的 Flow 账号。"},
	"chat_action_expired":           {"This action has expired. Open a current card and try again.", "此操作已过期，请打开最新的卡片后重试。"},
	"chat_binding_unavailable":      {"This group's Flow binding is no longer available.", "此群的 Flow 绑定已不可用。"},
	"chat_binding_scope_changed":    {"This group's Flow scope changed after the card was sent. Open a current card.", "卡片发出后此群的 Flow 范围已变更，请打开最新的卡片。"},
	"chat_binding_updates_disabled": {"This group is not enabled for updating Flow issues.", "此群未开启更新 Flow 事项。"},
	"chat_binding_create_disabled":  {"This group is not enabled for creating Flow issues.", "此群未开启创建 Flow 事项。"},
	"chat_issue_unavailable":        {"You no longer have access to this issue.", "你已无权访问此事项。"},
	"chat_issue_outside_binding":    {"This issue is no longer in this group's Flow scope.", "此事项已不在此群的 Flow 范围内。"},
	"chat_action_forbidden":         {"Your Flow account does not have permission to perform this action.", "你的 Flow 账号没有执行此操作的权限。"},
	"chat_action_conflict":          {"This issue changed at the same time. Try again.", "此事项刚刚被修改，请重试。"},
	"chat_action_invalid":           {"This value is no longer valid for this issue.", "该值对此事项已无效。"},
	"chat_action_failed":            {"Flow could not apply this change. Open Flow to check it.", "Flow 无法应用此更改，请在 Flow 中查看。"},
	"chat_reconnect_required":       {chatReconnectMessage, "你关联的聊天账号已过期，请在 设置 > 已关联账号 中重新关联。"},
	"chat_form_invalid":             {"The form has invalid fields. Start again from the message.", "表单内容无效，请从消息重新开始。"},
	"chat_team_unavailable":         {"You can't create issues in any team from here.", "你无法在此创建任何团队的事项。"},
	"chat_message_unavailable":      {"Flow could not read this message. Try again from the message.", "Flow 无法读取此消息，请从消息重新操作。"},
	"chat_requests_disabled":        {"This group does not take requests. A team owner can turn them on in the group's Flow binding.", "此群未开启需求收集。团队管理员可以在此群的 Flow 绑定中开启。"},
	"chat_request_forbidden":        {"Your Flow account can't submit requests to this team.", "你的 Flow 账号无权向此团队提交需求。"},
	"chat_request_empty":            {"Write the request after the mention, or reply to the message you want to submit.", "请在 @ 机器人后写下需求，或回复要提交的消息。"},
}

// chatLanguage is the member's Flow interface language for chat replies:
// Chinese or English; without a choice, the platform's (Feishu: Chinese,
// Lark: English).
func chatLanguage(metadata domain.Bootstrap, userID, provider string) string {
	language := strings.ToLower(metadata.UserSettings[userID].Language)
	switch {
	case strings.HasPrefix(language, "zh"):
		return "zh"
	case language != "" && language != "system" && language != "auto":
		return "en"
	case provider == "feishu":
		return "zh"
	}
	return "en"
}

func chatActionErrorText(code, language string) string {
	messages, ok := chatActionErrors[code]
	if !ok {
		code, messages = "chat_action_failed", chatActionErrors["chat_action_failed"]
	}
	if language == "zh" {
		return messages[1] + "（错误码：" + code + "）"
	}
	return messages[0] + " (code: " + code + ")"
}

func (s *server) chatSendActionError(ctx context.Context, record domain.ChatIntegrationRecord, metadata domain.Bootstrap, identity domain.ChatIdentity, job domain.ChatJob, code string) error {
	return s.chatSendPersonalText(ctx, record, identity.OpenID, chatActionErrorText(code, chatLanguage(metadata, identity.UserID, record.Provider)), job.DedupKey)
}

// chatMutationCode maps a refused Flow call to its chat code; other errors
// (transient) are returned for a retry.
func chatMutationCode(err error, fallback string) (string, error) {
	var failure *chatMutationError
	if !errors.As(err, &failure) || failure.Status < 400 || failure.Status >= 500 {
		return "", err
	}
	switch {
	case failure.Message == chatReconnectMessage:
		return "chat_reconnect_required", nil
	case failure.Status == http.StatusConflict:
		return "chat_action_conflict", nil
	case failure.Status == http.StatusForbidden:
		return "chat_action_forbidden", nil
	case failure.Status == http.StatusNotFound:
		return "chat_issue_unavailable", nil
	case failure.Status == http.StatusBadRequest || failure.Status == http.StatusUnprocessableEntity:
		return "chat_action_invalid", nil
	}
	return fallback, nil
}

func chatI18nPlain(en, zh string) map[string]any {
	return map[string]any{"tag": "plain_text", "content": en, "i18n": map[string]string{"en_us": en, "zh_cn": zh}}
}

func chatPriorityOptions() []map[string]any {
	return []map[string]any{chatOption("0", "No priority"), chatOption("1", "Urgent"), chatOption("2", "High"), chatOption("3", "Medium"), chatOption("4", "Low")}
}

// chatQuickElements draws the controls with the given action IDs.
func chatQuickElements(metadata domain.Bootstrap, issue domain.Issue, ids map[string]string) []map[string]any {
	controls := []map[string]any{{"tag": "button", "text": chatI18nPlain("Assign to me", "分配给我"), "type": "primary", "value": map[string]string{"actionId": ids[chatQuickAssign]}}}
	options, stateIDs, _ := chatStateOptions(metadata, issue.Team.ID)
	if len(options) > 0 {
		state := map[string]any{"tag": "select_static", "placeholder": chatI18nPlain("Status", "状态"), "options": options, "value": map[string]string{"actionId": ids[chatQuickState]}}
		if slices.Contains(stateIDs, issue.State.ID) {
			state["initial_option"] = issue.State.ID
		}
		controls = append(controls, state)
	}
	controls = append(controls, map[string]any{"tag": "select_static", "placeholder": chatI18nPlain("Priority", "优先级"), "options": chatPriorityOptions(), "initial_option": strconv.Itoa(min(max(issue.Priority, 0), 4)), "value": map[string]string{"actionId": ids[chatQuickPriority]}})
	return []map[string]any{{"tag": "action", "actions": controls}}
}

func chatIssueSummaryLine(issue domain.Issue) string {
	assignee := "Unassigned"
	if issue.Assignee != nil {
		assignee = issue.Assignee.Name
	}
	return fmt.Sprintf("%s · %s · %s · %s", issue.Team.Name, issue.State.Name, priorityLabel(issue.Priority), assignee)
}

// chatGroupQuickControls stores the shared controls of a group issue card.
// Like the card's other shared actions they are tied to the issue version
// the card shows, and messageID is filled in once the card is sent.
func (s *server) chatGroupQuickControls(ctx context.Context, record domain.ChatIntegrationRecord, binding domain.ChatBinding, issue domain.Issue, key, messageID, sourceMessage string) ([]map[string]any, []domain.ChatAction, error) {
	metadata, _ := s.store.WorkspaceMetadataFields(record.WorkspaceKey, "states", "teamSettings")
	ids := map[string]string{}
	actions := []domain.ChatAction{}
	for _, kind := range []string{chatQuickAssign, chatQuickState, chatQuickPriority} {
		id := chatSharedActionID(record, binding, issue, key, kind)
		payload, _ := json.Marshal(chatCardAction{Kind: kind, BindingID: binding.ID, BindingScope: chatBindingFingerprint(binding), ChatID: binding.ChatID, MessageID: messageID, IssueID: issue.ID, ExpectedVersion: issue.Version, SourceMessageID: sourceMessage})
		action := domain.ChatAction{ID: id, WorkspaceKey: record.WorkspaceKey, ConnectionID: record.ConnectionID, Payload: payload, ExpiresAt: time.Now().UTC().Add(14 * 24 * time.Hour)}
		if err := s.store.PutChatAction(ctx, action); err != nil {
			return nil, nil, err
		}
		ids[kind] = id
		actions = append(actions, action)
	}
	return chatQuickElements(metadata, issue, ids), actions, nil
}

func chatPersonalActionID(record domain.ChatIntegrationRecord, cardKey, kind string) string {
	return "chat_action_" + chatKey(record.ConnectionID, cardKey, "personal_card", kind)
}

// chatPersonalIssueCard draws a personal notification card of an issue. Its
// controls belong to the recipient only and stay usable for the card's
// lifetime: each click reads the issue's current state.
func (s *server) chatPersonalIssueCard(ctx context.Context, record domain.ChatIntegrationRecord, identity domain.ChatIdentity, issue domain.Issue, action chatExtendedAction) (map[string]any, []domain.ChatAction, error) {
	metadata, _ := s.store.WorkspaceMetadataFields(record.WorkspaceKey, "states", "teamSettings")
	ids := map[string]string{}
	actions := []domain.ChatAction{}
	for _, kind := range []string{chatQuickAssign, chatQuickState, chatQuickPriority} {
		stored := action
		stored.Kind, stored.IssueID, stored.OpenID = kind, issue.ID, identity.OpenID
		payload, _ := json.Marshal(stored)
		item := domain.ChatAction{ID: chatPersonalActionID(record, action.CardKey, kind), WorkspaceKey: record.WorkspaceKey, ConnectionID: record.ConnectionID, UserID: identity.UserID, Payload: payload, ExpiresAt: time.Now().UTC().Add(14 * 24 * time.Hour)}
		if err := s.store.PutChatAction(ctx, item); err != nil {
			return nil, nil, err
		}
		ids[kind] = item.ID
		actions = append(actions, item)
	}
	elements := []map[string]any{chatCardText(action.CardLine), chatCardText(issue.Identifier + " " + issue.Title + "\n" + chatIssueSummaryLine(issue)), {"tag": "action", "actions": []map[string]any{{"tag": "button", "text": chatI18nPlain("Open in Flow", "在 Flow 中打开"), "url": action.CardURL}}}}
	elements = append(elements, chatQuickElements(metadata, issue, ids)...)
	return chatCard(action.CardTitle, elements...), actions, nil
}

// chatPersonalNotificationCard is the card of a personal notification: with
// controls when it is about an issue the recipient can open, otherwise the
// notification with Open in Flow.
func (s *server) chatPersonalNotificationCard(ctx context.Context, record domain.ChatIntegrationRecord, identity domain.ChatIdentity, payload chatPersonalJob, item notificationEmailItem, job domain.ChatJob) (map[string]any, []domain.ChatAction, error) {
	plain := chatCard(item.Title, chatCardText(item.Line), map[string]any{"tag": "action", "actions": []map[string]any{{"tag": "button", "text": chatPlain("Open in Flow"), "url": item.URL}}})
	notification, err := s.store.NotificationRecord(ctx, record.WorkspaceKey, payload.UserID, payload.NotificationID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (notification.IssueID == "" || notification.ReviewID != "") {
		return plain, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var issue domain.Issue
	if err := s.chatAuthorizedMutation(withChatIdentityAuthentication(ctx, identity), record.WorkspaceKey, identity.UserID, http.MethodGet, "/api/issue-records/"+url.PathEscape(notification.IssueID), nil, &issue); err != nil {
		var failure *chatMutationError
		if errors.As(err, &failure) {
			return plain, nil, nil
		}
		return nil, nil, err
	}
	return s.chatPersonalIssueCard(ctx, record, identity, issue, chatExtendedAction{CardKey: job.DedupKey, CardTitle: item.Title, CardLine: item.Line, CardURL: item.URL})
}

// chatAttachPersonalActions ties a sent personal card's controls to its
// message, so only clicks on that message (by its recipient) match them.
func (s *server) chatAttachPersonalActions(ctx context.Context, actions []domain.ChatAction, result chatMessageResult) error {
	if len(actions) == 0 {
		return nil
	}
	if result.Data.MessageID == "" || result.Data.ChatID == "" {
		return errorsChatResponse()
	}
	for _, stored := range actions {
		var action chatExtendedAction
		if json.Unmarshal(stored.Payload, &action) != nil {
			return chatInvalid("invalid card action")
		}
		action.PersonalChatID, action.PersonalMessage = result.Data.ChatID, result.Data.MessageID
		stored.Payload, _ = json.Marshal(action)
		if err := s.store.UpdateChatAction(ctx, stored); err != nil {
			return err
		}
	}
	return nil
}

func (s *server) chatRunQuickAction(ctx context.Context, record domain.ChatIntegrationRecord, identity domain.ChatIdentity, metadata domain.Bootstrap, job domain.ChatJob, command chatInboundCommand, stored domain.ChatAction) error {
	var action chatExtendedAction
	if json.Unmarshal(stored.Payload, &action) != nil {
		return chatInvalid("invalid card action")
	}
	fail := func(code string) error { return s.chatSendActionError(ctx, record, metadata, identity, job, code) }
	group := action.BindingID != ""
	var binding domain.ChatBinding
	if group {
		var err error
		binding, err = s.chatBindingByID(ctx, record, action.BindingID)
		if err != nil {
			var permanent chatPermanentError
			var refusal chatGroupRefusal
			if errors.As(err, &permanent) || errors.As(err, &refusal) || errors.Is(err, store.ErrAuthForbidden) {
				return fail("chat_binding_unavailable")
			}
			return err
		}
		if action.BindingScope != chatBindingFingerprint(binding) || action.ChatID != binding.ChatID {
			return fail("chat_binding_scope_changed")
		}
		if !binding.AllowUpdate {
			return fail("chat_binding_updates_disabled")
		}
	} else if stored.UserID == "" || stored.UserID != identity.UserID {
		return fail("chat_action_expired")
	}
	var issue domain.Issue
	if err := s.chatAuthorizedMutation(ctx, record.WorkspaceKey, identity.UserID, http.MethodGet, "/api/issue-records/"+url.PathEscape(action.IssueID), nil, &issue); err != nil {
		code, err := chatMutationCode(err, "chat_issue_unavailable")
		if err != nil {
			return err
		}
		if code == "chat_action_forbidden" {
			code = "chat_issue_unavailable"
		}
		return fail(code)
	}
	if group && !chatIssueMatchesBinding(issue, binding) {
		return fail("chat_issue_outside_binding")
	}
	if issuePermissionRank(issueRole(s, metadata, issue)) < issuePermissionRank("editor") {
		return fail("chat_action_forbidden")
	}
	input := domain.IssueUpdateInput{ExpectedVersion: &issue.Version}
	option := chatString(command.Values, "option")
	switch action.Kind {
	case chatQuickAssign:
		assignee := identity.UserID
		input.AssigneeID = &assignee
	case chatQuickState:
		if !slices.ContainsFunc(statesForTeam(&metadata, issue.Team.ID), func(state domain.WorkflowState) bool { return state.ID == option }) {
			return fail("chat_action_invalid")
		}
		input.StateID = &option
	case chatQuickPriority:
		priority, err := strconv.Atoi(option)
		if err != nil || priority < 0 || priority > 4 {
			return fail("chat_action_invalid")
		}
		input.Priority = &priority
	}
	// Every click is its own change (the callback dedup key includes the
	// platform event): its receipt makes a retry after the commit read the
	// issue instead of writing again.
	var updated domain.Issue
	mutationCtx := store.WithChatMutationKey(ctx, chatKey(record.ConnectionID, job.DedupKey))
	if err := s.chatAuthorizedMutation(mutationCtx, record.WorkspaceKey, identity.UserID, http.MethodPatch, "/api/issue-records/"+url.PathEscape(issue.ID), input, &updated); err != nil {
		code, err := chatMutationCode(err, "chat_action_failed")
		if err != nil {
			return err
		}
		return fail(code)
	}
	if group {
		return s.chatRefreshIssueCards(ctx, record, binding, updated)
	}
	if action.PersonalMessage == "" {
		return nil
	}
	card, _, err := s.chatPersonalIssueCard(ctx, record, identity, updated, action)
	if err != nil {
		return err
	}
	content, _ := json.Marshal(card)
	return s.chatWorkflowRequest(ctx, record, http.MethodPatch, "/open-apis/im/v1/messages/"+url.PathEscape(action.PersonalMessage), map[string]string{"content": string(content)}, nil)
}

// chatCallbackToast is the immediate answer to a card click. Controls
// change the card itself; forms and other actions report in the member's
// bot conversation.
func (s *server) chatCallbackToast(ctx context.Context, record domain.ChatIntegrationRecord, command chatInboundCommand) map[string]any {
	stored, err := s.store.GetChatAction(ctx, record.WorkspaceKey, record.ConnectionID, command.ActionID)
	var action chatCardAction
	if err == nil && json.Unmarshal(stored.Payload, &action) == nil && chatQuickActionKind(action.Kind) {
		return map[string]any{"toast": map[string]any{"type": "info", "content": "Updating…", "i18n": map[string]string{"en_us": "Updating…", "zh_cn": "正在更新…"}}}
	}
	return map[string]any{"toast": map[string]any{"type": "info", "content": "Processing. The result will arrive in your Flow bot conversation.", "i18n": map[string]string{"en_us": "Processing. The result will arrive in your Flow bot conversation.", "zh_cn": "处理中，结果将发送到你与 Flow 机器人的会话。"}}}
}
