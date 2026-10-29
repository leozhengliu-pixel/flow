package main

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

var chatActionIDPattern = regexp.MustCompile(`"actionId":"(chat_action_[0-9a-f]+)"`)

// chatCardActionIDs maps the kinds of a card's stored actions to their IDs.
func chatCardActionIDs(t *testing.T, f *chatWorkflowFixture, content string) map[string]string {
	t.Helper()
	ids := map[string]string{}
	for _, match := range chatActionIDPattern.FindAllStringSubmatch(content, -1) {
		stored, err := f.s.store.GetChatAction(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, match[1])
		if err != nil {
			t.Fatalf("card action %s not stored: %v", match[1], err)
		}
		var action chatCardAction
		_ = json.Unmarshal(stored.Payload, &action)
		ids[action.Kind] = match[1]
	}
	return ids
}

// click delivers a card callback and runs the queued command.
func (f *chatWorkflowFixture) click(t *testing.T, openID, chatID, messageID, actionID, option, eventID string) error {
	t.Helper()
	envelope := f.messageEnvelope()
	envelope.Header.EventType, envelope.Header.EventID = "card.action.trigger", eventID
	envelope.Event.Operator.TenantKey, envelope.Event.Operator.OpenID = f.record.TenantKey, openID
	envelope.Event.Context.ChatID, envelope.Event.Context.MessageID = chatID, messageID
	envelope.Event.Action.Value = map[string]json.RawMessage{"actionId": json.RawMessage(strconv.Quote(actionID))}
	envelope.Event.Action.Option = option
	command, dedup, accepted, err := f.s.chatCommandForEnvelope(t.Context(), f.record, envelope)
	if err != nil || !accepted {
		return err
	}
	job := domain.ChatJob{ID: "chat_job_" + dedup, WorkspaceKey: f.record.WorkspaceKey, ConnectionID: f.record.ConnectionID, Kind: "inbound", DedupKey: dedup}
	job.Payload, _ = json.Marshal(command)
	return f.s.processChatJob(t.Context(), job)
}

func (f *chatWorkflowFixture) lastText(t *testing.T) string {
	t.Helper()
	if len(f.sent) == 0 || f.sent[len(f.sent)-1]["msg_type"] != "text" {
		t.Fatalf("no private text reply: %#v", f.sent)
	}
	return f.sent[len(f.sent)-1]["content"].(string)
}

func chatOtherState(t *testing.T, f *chatWorkflowFixture, issue domain.Issue) string {
	t.Helper()
	data := f.s.store.Bootstrap()
	for _, state := range statesForTeam(&data, issue.Team.ID) {
		if state.ID != issue.State.ID {
			return state.ID
		}
	}
	t.Fatal("team has a single state")
	return ""
}

func TestChatGroupCardControlsRunAsClickerAndRedrawTheCard(t *testing.T) {
	f := newChatWorkflowFixture(t)
	issue := f.s.store.Bootstrap().Issues[0]
	ctx := withChatIdentityAuthentication(t.Context(), f.identity)
	var unassigned domain.Issue
	if err := f.s.chatAuthorizedMutation(ctx, f.record.WorkspaceKey, f.identity.UserID, http.MethodPatch, "/api/issue-records/"+issue.ID, map[string]any{"assigneeId": "", "priority": 4, "expectedVersion": issue.Version}, &unassigned); err != nil {
		t.Fatal(err)
	}
	if err := f.s.chatSendIssueCard(t.Context(), f.record, f.binding, unassigned, "controls_card", "", "issue.created"); err != nil {
		t.Fatal(err)
	}
	content := f.sent[0]["content"].(string)
	ids := chatCardActionIDs(t, f, content)
	for _, kind := range []string{chatQuickAssign, chatQuickState, chatQuickPriority, "open_state", "open_assignee", "open_comment"} {
		if ids[kind] == "" {
			t.Fatalf("card lacks %s: %s", kind, content)
		}
	}
	if !strings.Contains(content, "Open in Flow") || !strings.Contains(content, `"zh_cn":"分配给我"`) {
		t.Fatalf("card controls not labelled: %s", content)
	}
	messages, err := f.s.store.ChatMessagesForIssue(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, issue.ID)
	if err != nil || len(messages) != 1 {
		t.Fatalf("card message: %#v %v", messages, err)
	}
	messageID := messages[0].MessageID

	if err := f.click(t, f.identity.OpenID, f.binding.ChatID, messageID, ids[chatQuickAssign], "", "evt_assign"); err != nil {
		t.Fatal(err)
	}
	after, _ := f.s.store.IssueRecord(t.Context(), f.record.WorkspaceKey, issue.ID)
	if after.Assignee == nil || after.Assignee.ID != f.identity.UserID {
		t.Fatalf("assign to me: %+v", after.Assignee)
	}
	if len(f.patched) != 1 || !strings.Contains(f.patched[0]["content"].(string), "Assign to me") {
		t.Fatalf("card not redrawn in place: %#v", f.patched)
	}
	// Each control of the card keeps working, with every click its own change.
	if err := f.click(t, f.identity.OpenID, f.binding.ChatID, messageID, ids[chatQuickPriority], "1", "evt_priority"); err != nil {
		t.Fatal(err)
	}
	target := chatOtherState(t, f, after)
	if err := f.click(t, f.identity.OpenID, f.binding.ChatID, messageID, ids[chatQuickState], target, "evt_state"); err != nil {
		t.Fatal(err)
	}
	after, _ = f.s.store.IssueRecord(t.Context(), f.record.WorkspaceKey, issue.ID)
	if after.Priority != 1 || after.State.ID != target || len(f.patched) != 3 {
		t.Fatalf("priority/status controls: priority %d state %s patches %d", after.Priority, after.State.ID, len(f.patched))
	}
	if !strings.Contains(f.patched[2]["content"].(string), `"initial_option":"`+target+`"`) {
		t.Fatalf("redrawn card does not show the new status: %s", f.patched[2]["content"])
	}
	// The same callback delivered again is one change.
	if err := f.click(t, f.identity.OpenID, f.binding.ChatID, messageID, ids[chatQuickPriority], "1", "evt_priority"); err != nil {
		t.Fatal(err)
	}
	if replay, _ := f.s.store.IssueRecord(t.Context(), f.record.WorkspaceKey, issue.ID); replay.Version != after.Version {
		t.Fatalf("a replayed click wrote again: %d %d", replay.Version, after.Version)
	}
	// An invalid status is refused with its code.
	if err := f.click(t, f.identity.OpenID, f.binding.ChatID, messageID, ids[chatQuickState], "state_of_another_team", "evt_invalid"); err != nil {
		t.Fatal(err)
	}
	if text := f.lastText(t); !strings.Contains(text, "chat_action_invalid") {
		t.Fatalf("invalid status: %s", text)
	}

	// The binding no longer allows updates: refused, in the member's language.
	binding := f.binding
	binding.AllowUpdate = false
	if err := f.s.store.PutChatBinding(t.Context(), f.record.WorkspaceKey, binding); err != nil {
		t.Fatal(err)
	}
	if err := f.s.store.MutateWorkspace(t.Context(), f.record.WorkspaceKey, "user_settings.updated", f.identity.UserID, nil, func(data *domain.Bootstrap) error {
		settings := data.UserSettings[f.identity.UserID]
		settings.UserID, settings.Language = f.identity.UserID, "zh-CN"
		data.UserSettings[f.identity.UserID] = settings
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.click(t, f.identity.OpenID, f.binding.ChatID, messageID, ids[chatQuickPriority], "3", "evt_disabled"); err != nil {
		t.Fatal(err)
	}
	if text := f.lastText(t); !strings.Contains(text, "chat_binding_updates_disabled") || !strings.Contains(text, "此群未开启更新") {
		t.Fatalf("updates-disabled refusal: %s", text)
	}
	if unchanged, _ := f.s.store.IssueRecord(t.Context(), f.record.WorkspaceKey, issue.ID); unchanged.Priority != 1 {
		t.Fatalf("binding without updates changed the issue: %d", unchanged.Priority)
	}
}

func TestChatCardControlsRefuseViewersAndUnlinkedMembers(t *testing.T) {
	f := newChatWorkflowFixture(t)
	data := f.s.store.Bootstrap()
	issue := data.Issues[0]
	user, verification, err := f.s.store.Register(t.Context(), "Chat control viewer", "chat-control-viewer@example.test", "long-password-test")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.store.VerifyEmail(t.Context(), verification); err != nil {
		t.Fatal(err)
	}
	if err := f.s.store.EnsureWorkspaceMembership(t.Context(), data.Workspace.ID, user.ID); err != nil {
		t.Fatal(err)
	}
	viewer := domain.ChatIdentity{UserID: user.ID, OpenID: "ou_control_viewer", TenantKey: f.record.TenantKey, AuthProvider: "password", AuthenticatedAt: time.Now().UTC()}
	if err := f.s.store.PutChatIdentity(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, viewer); err != nil {
		t.Fatal(err)
	}
	if err := f.s.chatSendIssueCard(t.Context(), f.record, f.binding, issue, "viewer_controls", "", "issue.created"); err != nil {
		t.Fatal(err)
	}
	ids := chatCardActionIDs(t, f, f.sent[0]["content"].(string))
	messages, _ := f.s.store.ChatMessagesForIssue(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, issue.ID)
	if err := f.click(t, viewer.OpenID, f.binding.ChatID, messages[0].MessageID, ids[chatQuickAssign], "", "evt_viewer"); err != nil {
		t.Fatal(err)
	}
	if text := f.lastText(t); !strings.Contains(text, "chat_action_forbidden") || f.sent[len(f.sent)-1]["receive_id"] != viewer.OpenID {
		t.Fatalf("viewer refusal not private or without code: %#v", f.sent[len(f.sent)-1])
	}
	if after, _ := f.s.store.IssueRecord(t.Context(), f.record.WorkspaceKey, issue.ID); after.Version != issue.Version || len(f.patched) != 0 {
		t.Fatal("a viewer's click changed the issue or the card")
	}
	if err := f.click(t, "ou_not_linked", f.binding.ChatID, messages[0].MessageID, ids[chatQuickAssign], "", "evt_unlinked"); err != nil {
		t.Fatal(err)
	}
	if text := f.lastText(t); !strings.Contains(text, "Connect your Flow account") || f.sent[len(f.sent)-1]["receive_id"] != "ou_not_linked" {
		t.Fatalf("unlinked clicker: %s", text)
	}
}

func TestChatPersonalNotificationCardControls(t *testing.T) {
	f := newChatWorkflowFixture(t)
	data := f.s.store.Bootstrap()
	issue := data.Issues[0]
	chatPersonalPreferences(t, f, data.Viewer.ID, &domain.NotificationChannelPreferences{Enabled: true})
	chatPersonalNotify(t, f, data.Viewer.ID, "notification_controls", "assignments", issue.ID)
	chatPersonalRun(t, f)
	if len(f.sent) != 1 || f.sent[0]["msg_type"] != "interactive" {
		t.Fatalf("personal card: %#v", f.sent)
	}
	content := f.sent[0]["content"].(string)
	ids := chatCardActionIDs(t, f, content)
	if ids[chatQuickAssign] == "" || ids[chatQuickState] == "" || ids[chatQuickPriority] == "" || !strings.Contains(content, "Open in Flow") {
		t.Fatalf("personal card controls: %s", content)
	}
	stored, _ := f.s.store.GetChatAction(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, ids[chatQuickPriority])
	var action chatExtendedAction
	_ = json.Unmarshal(stored.Payload, &action)
	if stored.UserID != f.identity.UserID || action.PersonalMessage == "" || action.PersonalChatID != "oc_dm_test" {
		t.Fatalf("personal controls not tied to the recipient's message: %+v", action)
	}
	// Another member cannot use them.
	if err := f.click(t, "ou_someone_else", action.PersonalChatID, action.PersonalMessage, ids[chatQuickPriority], "1", "evt_other"); err == nil {
		t.Fatal("another member's click on a personal card was accepted")
	}
	for index, option := range []string{"2", "1"} {
		if err := f.click(t, f.identity.OpenID, action.PersonalChatID, action.PersonalMessage, ids[chatQuickPriority], option, "evt_personal_"+option); err != nil {
			t.Fatal(err)
		}
		after, _ := f.s.store.IssueRecord(t.Context(), f.record.WorkspaceKey, issue.ID)
		if strconv.Itoa(after.Priority) != option || len(f.patched) != index+1 {
			t.Fatalf("personal priority click %d: priority %d patches %d", index, after.Priority, len(f.patched))
		}
		if !strings.Contains(f.patched[index]["content"].(string), `"initial_option":"`+option+`"`) || !strings.Contains(f.patched[index]["content"].(string), "assigned the issue to you") {
			t.Fatalf("personal card not redrawn: %s", f.patched[index]["content"])
		}
	}
	if err := f.click(t, f.identity.OpenID, action.PersonalChatID, action.PersonalMessage, ids[chatQuickAssign], "", "evt_personal_assign"); err != nil {
		t.Fatal(err)
	}
	if after, _ := f.s.store.IssueRecord(t.Context(), f.record.WorkspaceKey, issue.ID); after.Assignee == nil || after.Assignee.ID != f.identity.UserID {
		t.Fatalf("personal assign to me: %+v", after.Assignee)
	}
}

func TestChatActionErrorsAreLocalized(t *testing.T) {
	data := domain.Bootstrap{UserSettings: map[string]domain.UserSettings{"zh": {Language: "zh-CN"}, "en": {Language: "en"}}}
	if chatLanguage(data, "zh", "lark") != "zh" || chatLanguage(data, "en", "feishu") != "en" || chatLanguage(data, "none", "feishu") != "zh" || chatLanguage(data, "none", "lark") != "en" {
		t.Fatal("chat language choice")
	}
	if text := chatActionErrorText("chat_action_forbidden", "zh"); !strings.Contains(text, "没有执行此操作的权限") || !strings.Contains(text, "chat_action_forbidden") {
		t.Fatalf("zh error: %s", text)
	}
	if text := chatActionErrorText("unknown", "en"); !strings.Contains(text, "chat_action_failed") {
		t.Fatalf("fallback error: %s", text)
	}
}
