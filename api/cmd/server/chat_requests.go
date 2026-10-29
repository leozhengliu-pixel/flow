package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Requests from group messages. In a group bound to a team whose binding
// takes requests, a member turns a message into a request in the team's
// triage: by explicitly asking the bot ("@Flow 提需求 …"), by adding the
// binding's reaction, or with the message shortcut's Submit as request. The request is
// the same record as Flow's other requests (an Ask, source feishu or lark)
// and its issue, created as the requester's linked Flow account with their
// permissions, from the binding's request template and priority. Unlinked
// members get a card to link their account; members who cannot create issues
// in the team are told privately.
//
// A message carries one request only (store/chat_intake_requests.go), and
// the message's thread becomes the request's thread (chat_issue_threads.go):
// the confirmation card replies there, members' replies become comments when
// the binding allows updates, Flow comments are posted back, status changes
// post a short note, and closing the request replies with its outcome.

const (
	chatRequestKind        = "request"         // inbound command: make a request from a message
	chatMessageRequestKind = "message_request" // stored form action: the shortcut's Submit as request
	chatRequestResultKind  = "request_result"  // card job: confirm in the message's thread
	chatRequestStatusKind  = "request.status"  // notification: the request's status changed
	// A reservation left by a requester whose job stopped can be taken over
	// by another member after this long.
	chatRequestReservationTimeout = 10 * time.Minute
)

// chatRequestPrefixes start an explicit request after the bot's mention.
var chatRequestPrefixes = []string{"提需求", "提个需求", "需求", "request"}

// chatRequestEmoji is the shape of the platform's emoji types.
var chatRequestEmoji = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)

// chatRequestText strips a request prefix: the rest of the text and whether
// the prefix was there.
func chatRequestText(text string) (string, bool) {
	text = strings.TrimSpace(text)
	lower := strings.ToLower(text)
	for _, prefix := range chatRequestPrefixes {
		if strings.HasPrefix(lower, prefix) {
			rest := text[len(prefix):]
			if prefix == "request" && rest != "" && !strings.ContainsAny(rest[:1], " \t\n:：,，") {
				continue // "requested …" is not the command
			}
			return strings.TrimLeft(strings.TrimSpace(rest), ":：,，"), true
		}
	}
	return text, false
}

// chatRequestBindings lists the connection's bindings that take requests.
func (s *server) chatRequestBindings(ctx context.Context, record domain.ChatIntegrationRecord) []domain.ChatBinding {
	bindings, err := s.store.ChatBindings(ctx, record.WorkspaceKey, record.ConnectionID)
	if err != nil {
		return nil
	}
	return slices.DeleteFunc(bindings, func(binding domain.ChatBinding) bool {
		return !binding.Enabled || binding.RevokedAt != nil || !binding.RequestsEnabled()
	})
}

// chatRequestMentionCommand turns a group message that mentions the bot into
// a request command: with a request prefix anywhere the bot is, and without
// one only when the binding opts in. A bare mention replying to a
// message makes the request from that message.
func (s *server) chatRequestMentionCommand(ctx context.Context, record domain.ChatIntegrationRecord, envelope chatWebhookEnvelope, text string) (chatInboundCommand, string, bool) {
	message := envelope.Event.Message
	body, explicit := chatRequestText(text)
	if !explicit && !slices.ContainsFunc(s.chatRequestBindings(ctx, record), func(binding domain.ChatBinding) bool {
		return binding.ChatID == message.ChatID && binding.Requests.EveryMention
	}) {
		return chatInboundCommand{}, "", false
	}
	values := map[string]json.RawMessage{"source": json.RawMessage(strconv.Quote(domain.ChatRequestSourceMention))}
	if explicit {
		values["explicit"] = json.RawMessage(`"true"`)
	}
	source := message.MessageID
	switch {
	case body != "":
		values["text"], _ = json.Marshal(chatTruncateText(body, 8000))
		if message.RootID != "" && message.RootID != message.MessageID {
			values["rootId"] = json.RawMessage(strconv.Quote(message.RootID))
		}
	case message.ParentID != "" && chatValidPlatformID(message.ParentID, "om_"):
		source = message.ParentID
		values["read"] = json.RawMessage(`"true"`)
	case !explicit:
		return chatInboundCommand{}, "", false
	}
	command := chatInboundCommand{Kind: chatRequestKind, OpenID: envelope.Event.Sender.SenderID.OpenID, ChatID: message.ChatID, MessageID: source, Values: values}
	return command, chatKey(record.ConnectionID, "message", message.MessageID), true
}

// chatRequestReactionCommand: a member added a reaction that a binding takes
// as a request. The event names the message only; its group is read when the
// job runs.
func (s *server) chatRequestReactionCommand(ctx context.Context, record domain.ChatIntegrationRecord, envelope chatWebhookEnvelope) (chatInboundCommand, string, bool) {
	event := envelope.Event
	emoji, openID := event.ReactionType.EmojiType, event.UserID.OpenID
	if event.OperatorType != "user" || openID == "" || openID == record.BotOpenID || !chatValidPlatformID(event.MessageID, "om_") || !chatRequestEmoji.MatchString(emoji) {
		return chatInboundCommand{}, "", false
	}
	if !slices.ContainsFunc(s.chatRequestBindings(ctx, record), func(binding domain.ChatBinding) bool { return binding.Requests.Emoji == emoji }) {
		return chatInboundCommand{}, "", false
	}
	values := map[string]json.RawMessage{"source": json.RawMessage(strconv.Quote(domain.ChatRequestSourceReaction)), "emoji": json.RawMessage(strconv.Quote(emoji)), "read": json.RawMessage(`"true"`)}
	command := chatInboundCommand{Kind: chatRequestKind, OpenID: openID, MessageID: event.MessageID, Values: values}
	return command, chatKey(record.ConnectionID, "reaction", event.MessageID, openID, emoji), true
}

// chatRequestDraft is the message a request is made from.
type chatRequestDraft struct {
	MessageID, ChatID, RootID string
	Text, Title, Description  string
	Source                    string
	// Personal: the requester used a private form and hears back there.
	Personal      bool
	FormMessageID string
}

// chatReadMessage reads a message with the bot's access: its group, thread
// root and text. found is false when the bot cannot read it.
func (s *server) chatReadMessage(ctx context.Context, record domain.ChatIntegrationRecord, messageID string) (chatRequestDraft, bool, error) {
	var message chatPlatformMessage
	err := s.chatPlatformRequest(ctx, record, http.MethodGet, "/open-apis/im/v1/messages/"+url.PathEscape(messageID), nil, &message)
	var platform *chatPlatformError
	switch {
	case err == nil:
	case errors.As(err, &platform) && !platform.Retryable:
		return chatRequestDraft{}, false, nil
	default:
		return chatRequestDraft{}, false, err
	}
	if len(message.Data.Items) == 0 || message.Data.Items[0].MessageID != messageID || message.Data.Items[0].ChatID == "" {
		return chatRequestDraft{}, false, nil
	}
	item := message.Data.Items[0]
	draft := chatRequestDraft{MessageID: messageID, ChatID: item.ChatID, Text: chatMessageText(item.MsgType, item.Body.Content)}
	if item.RootID != "" && item.RootID != messageID {
		draft.RootID = item.RootID
	}
	return draft, true, nil
}

// processChatRequest makes a request from a mention or a reaction.
func (s *server) processChatRequest(ctx context.Context, record domain.ChatIntegrationRecord, job domain.ChatJob, command chatInboundCommand) error {
	source := chatString(command.Values, "source")
	explicit := chatString(command.Values, "explicit") == "true"
	draft := chatRequestDraft{MessageID: command.MessageID, ChatID: command.ChatID, Text: chatString(command.Values, "text"), RootID: chatString(command.Values, "rootId"), Source: source}
	if chatString(command.Values, "read") == "true" {
		read, found, err := s.chatReadMessage(ctx, record, command.MessageID)
		if err != nil {
			return err
		}
		if !found || command.ChatID != "" && read.ChatID != command.ChatID {
			if explicit {
				return s.chatSendPersonalText(ctx, record, command.OpenID, chatActionErrorText("chat_message_unavailable", chatProviderLanguage(record.Provider)), job.DedupKey)
			}
			return nil
		}
		read.Source = source
		draft = read
	}
	binding, err := s.chatBindingForChat(ctx, record, draft.ChatID)
	if err != nil {
		var permanent chatPermanentError
		var refusal chatGroupRefusal
		if !errors.As(err, &permanent) && !errors.As(err, &refusal) && !errors.Is(err, store.ErrAuthForbidden) {
			return err
		}
	}
	if err != nil || !binding.RequestsEnabled() || source == domain.ChatRequestSourceReaction && binding.Requests.Emoji != chatString(command.Values, "emoji") {
		if explicit {
			return s.chatSendPersonalText(ctx, record, command.OpenID, chatActionErrorText("chat_requests_disabled", chatProviderLanguage(record.Provider)), job.DedupKey)
		}
		return nil
	}
	if source == domain.ChatRequestSourceMention && !explicit && !binding.Requests.EveryMention {
		return s.chatSendGroupHelp(ctx, record, job, command)
	}
	identity, err := s.store.ChatIdentityForOpenID(ctx, record.WorkspaceKey, record.ConnectionID, command.OpenID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if errors.Is(err, sql.ErrNoRows) || identity.TenantKey != record.TenantKey {
		return s.chatSendLinkAccountCard(ctx, record, job, command.OpenID)
	}
	ctx = withChatIdentityAuthentication(ctx, identity)
	var metadata domain.Bootstrap
	if err := s.chatAuthorizedMutation(ctx, record.WorkspaceKey, identity.UserID, http.MethodGet, "/api/issue-records/bootstrap", nil, &metadata); err != nil {
		return s.chatMutationFailure(ctx, record, job, command.OpenID, err, "Your Flow workspace access has changed. Sign in to Flow and check your access.")
	}
	return s.chatCreateRequest(ctx, record, binding, identity, metadata, job, draft)
}

func chatProviderLanguage(provider string) string {
	if provider == "feishu" {
		return "zh"
	}
	return "en"
}

// chatRequestFromForm submits the message shortcut's form as a request.
func (s *server) chatRequestFromForm(ctx context.Context, record domain.ChatIntegrationRecord, identity domain.ChatIdentity, metadata domain.Bootstrap, job domain.ChatJob, command chatInboundCommand, stored domain.ChatAction) error {
	var action chatExtendedAction
	if json.Unmarshal(stored.Payload, &action) != nil {
		return chatInvalid("invalid stored form")
	}
	fail := func(code string) error { return s.chatSendActionError(ctx, record, metadata, identity, job, code) }
	if stored.UserID == "" || stored.UserID != identity.UserID || !action.Verified || action.BindingID == "" {
		return fail("chat_action_expired")
	}
	if handled, err := s.chatMessageIntakeResult(ctx, record, identity, metadata, job, action); handled {
		return err
	}
	binding, err := s.chatBindingByID(ctx, record, action.BindingID)
	if err != nil {
		var permanent chatPermanentError
		var refusal chatGroupRefusal
		if errors.As(err, &permanent) || errors.As(err, &refusal) || errors.Is(err, store.ErrAuthForbidden) {
			return fail("chat_binding_unavailable")
		}
		return err
	}
	if action.BindingScope != chatBindingFingerprint(binding) || binding.ChatID != action.SourceChatID {
		return fail("chat_binding_scope_changed")
	}
	if !binding.RequestsEnabled() {
		return fail("chat_requests_disabled")
	}
	title := chatString(command.Values, "title")
	if title == "" {
		title = action.Title
	}
	description := chatString(command.Values, "description")
	if len(title) > 2048 || utf8.RuneCountInString(title) > 1000 || utf8.RuneCountInString(description) > 10000 {
		return fail("chat_form_invalid")
	}
	draft := chatRequestDraft{MessageID: action.SourceMessageID, ChatID: action.SourceChatID, RootID: action.SourceRootID, Title: title, Description: description, Source: domain.ChatRequestSourceShortcut, Personal: true, FormMessageID: action.PersonalMessage}
	if title == "" {
		draft.Text = description
	}
	return s.chatCreateRequest(ctx, record, binding, identity, metadata, job, draft)
}

// chatRequestContent is the request's title and description from the message:
// its first line, and its text with a link back to the group.
func chatRequestContent(record domain.ChatIntegrationRecord, binding domain.ChatBinding, draft chatRequestDraft, language string) (string, string) {
	if draft.Title != "" {
		return draft.Title, draft.Description
	}
	text := strings.TrimSpace(draft.Text)
	title := text
	if index := strings.IndexByte(title, '\n'); index >= 0 {
		title = title[:index]
	}
	if runes := []rune(strings.TrimSpace(title)); len(runes) > 200 {
		title = string(runes[:200])
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Request from " + binding.ChatName
		if language == "zh" {
			title = "来自「" + binding.ChatName + "」的需求"
		}
		title = strings.TrimSpace(chatTruncateText(title, 200))
	}
	source := "Source message: "
	if language == "zh" {
		source = "来源消息："
	}
	link := source + chatChatLink(record.Provider, draft.ChatID)
	description := []rune(text)
	if len(description) > 9000 {
		description = append(description[:8999], '…')
	}
	if len(description) == 0 {
		return title, link
	}
	return title, string(description) + "\n\n" + link
}

// chatRequestState is where requests land: the team's triage when it uses
// triage, otherwise the template's or the team's default state (nil).
func chatRequestState(metadata domain.Bootstrap, teamID string) *string {
	if !teamSettings(&metadata, teamID).TriageEnabled {
		return nil
	}
	if state := stateForTeamByType(&metadata, teamID, "backlog"); state != nil {
		id := state.ID
		return &id
	}
	return nil
}

// chatRequestProject keeps a project-scoped binding's request in its scope:
// the template's project when the binding covers it, otherwise the binding's
// first project.
func chatRequestProject(metadata domain.Bootstrap, binding domain.ChatBinding) *string {
	ids := chatBindingProjects(binding)
	if len(ids) == 0 {
		return nil
	}
	if index := slices.IndexFunc(metadata.IssueTemplates, func(template domain.IssueTemplate) bool { return template.ID == binding.Requests.TemplateID }); index >= 0 && slices.Contains(ids, metadata.IssueTemplates[index].ProjectID) {
		return nil
	}
	return &ids[0]
}

func chatRequestIssueURL(s *server, record domain.ChatIntegrationRecord, issue domain.Issue) string {
	return fmt.Sprintf("%s/%s/issue/%s", s.chatAppOrigin(), url.PathEscape(record.WorkspaceKey), url.PathEscape(issue.Identifier))
}

// chatCreateRequest reserves the message, creates the request's issue and
// request record as the member, claims the message's thread and queues the
// confirmation.
func (s *server) chatCreateRequest(ctx context.Context, record domain.ChatIntegrationRecord, binding domain.ChatBinding, identity domain.ChatIdentity, metadata domain.Bootstrap, job domain.ChatJob, draft chatRequestDraft) error {
	language := chatLanguage(metadata, identity.UserID, record.Provider)
	fail := func(code string) error { return s.chatSendActionError(ctx, record, metadata, identity, job, code) }
	if !chatValidPlatformID(draft.MessageID, "om_") || draft.ChatID != binding.ChatID {
		return fail("chat_message_unavailable")
	}
	issueID := "issue_chat_" + chatKey(record.ConnectionID, "request", draft.MessageID)
	reservation := domain.ChatIntakeRequest{ConnectionID: record.ConnectionID, ChatID: binding.ChatID, MessageID: draft.MessageID, BindingID: binding.ID, IssueID: issueID, RequesterID: identity.UserID, RequesterOpenID: identity.OpenID, Source: draft.Source, Status: domain.ChatRequestPending}
	request, reserved, err := s.store.ReserveChatIntakeRequest(ctx, record.WorkspaceKey, reservation)
	if err != nil {
		return err
	}
	if !reserved {
		switch {
		case request.Status == domain.ChatRequestCreated:
			return s.chatRequestExists(ctx, record, identity, metadata, job, request, draft)
		case request.Kind == "issue":
			return errors.New("message conversion in progress")
		case request.RequesterID == identity.UserID:
			// This requester's earlier attempt stopped: resume it.
		case time.Since(request.UpdatedAt) > chatRequestReservationTimeout:
			if err := s.store.DeleteChatIntakeRequest(ctx, record.WorkspaceKey, record.ConnectionID, request.ID); err != nil {
				return err
			}
			if request, reserved, err = s.store.ReserveChatIntakeRequest(ctx, record.WorkspaceKey, reservation); err != nil || !reserved {
				return err
			}
		default:
			// Another member is making this request right now.
			return nil
		}
	}
	release := func() error {
		if _, err := s.store.IssueRecord(ctx, record.WorkspaceKey, request.IssueID); !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		return s.store.DeleteChatIntakeRequest(ctx, record.WorkspaceKey, record.ConnectionID, request.ID)
	}
	if !slices.ContainsFunc(chatCreatableTeams(metadata), func(team domain.Team) bool { return team.ID == binding.TeamID }) {
		if err := release(); err != nil {
			return err
		}
		return fail("chat_request_forbidden")
	}
	if templateID := binding.Requests.TemplateID; templateID != "" && !chatBindingTemplatePermitted(metadata, binding, templateID) {
		if err := release(); err != nil {
			return err
		}
		return fail("chat_team_unavailable")
	}
	title, description := chatRequestContent(record, binding, draft, language)
	if title == "" || utf8.RuneCountInString(title) > 1000 {
		if err := release(); err != nil {
			return err
		}
		return fail("chat_request_empty")
	}
	input := domain.IssueCreateInput{Title: title, Description: description, TeamID: binding.TeamID, TemplateID: binding.Requests.TemplateID, StateID: chatRequestState(metadata, binding.TeamID), ProjectID: chatRequestProject(metadata, binding)}
	if priority := binding.Requests.Priority; priority != nil && *priority >= 0 && *priority <= 4 {
		value := *priority
		input.Priority = &value
	}
	var issue domain.Issue
	if err := s.chatAuthorizedMutation(store.WithIssueCreationKey(ctx, issueID), record.WorkspaceKey, identity.UserID, http.MethodPost, "/api/issue-records", input, &issue); err != nil {
		code, err := chatMutationCode(err, "chat_action_failed")
		if err != nil {
			return err
		}
		if code == "chat_action_forbidden" {
			code = "chat_request_forbidden"
		}
		if err := release(); err != nil {
			return err
		}
		return fail(code)
	}
	askID, err := s.chatRecordRequestAsk(ctx, record, identity.UserID, issue, binding.Requests.TemplateID)
	if err != nil {
		return err
	}
	// The message's thread carries the request; the confirmation card is its
	// first reply. A message inside another resource's thread cannot give
	// the request a thread of its own: the confirmation then starts one.
	sourceMessage := draft.MessageID
	claimed, err := s.chatClaimMessageThread(ctx, record, binding, issue.ID, firstNonEmpty(draft.RootID, draft.MessageID), "request")
	if err != nil {
		return err
	}
	if !claimed {
		if thread, err := s.store.ChatThread(ctx, record.WorkspaceKey, record.ConnectionID, binding.ChatID, domain.ChatThreadResourceIssue, issue.ID); err != nil || thread.Origin != "request" {
			sourceMessage = ""
		}
	}
	result := chatNotificationJob{BindingID: binding.ID, BindingScope: chatBindingFingerprint(binding), IssueID: issue.ID, SourceMessageID: sourceMessage, Kind: chatRequestResultKind}
	if draft.Personal {
		result.PersonalOpenID = identity.OpenID
	}
	payload, _ := json.Marshal(result)
	_, err = s.store.EnqueueChatJob(ctx, domain.ChatJob{ID: "chat_result_" + job.DedupKey, WorkspaceKey: record.WorkspaceKey, ConnectionID: record.ConnectionID, Kind: "card", DedupKey: chatKey(job.DedupKey, "result"), Payload: payload, RunAt: time.Now().UTC()})
	if err != nil {
		return err
	}
	request.IssueID, request.AskID, request.Status = issue.ID, askID, domain.ChatRequestCreated
	if err := s.store.UpdateChatIntakeRequest(ctx, record.WorkspaceKey, request); err != nil {
		return err
	}
	return s.chatUpdateMessageFormResult(ctx, record, draft.FormMessageID, issue, false)
}

// chatRequestExists answers a member asking again for a message that is
// already a request: privately, and only when they asked explicitly.
func (s *server) chatRequestExists(ctx context.Context, record domain.ChatIntegrationRecord, identity domain.ChatIdentity, metadata domain.Bootstrap, job domain.ChatJob, request domain.ChatIntakeRequest, draft chatRequestDraft) error {
	if draft.Source == domain.ChatRequestSourceReaction {
		return nil
	}
	return s.chatMessageIntakeExists(ctx, record, identity, metadata, job, request, draft.FormMessageID)
}

// chatClaimMessageThread records the message's thread as the issue's thread
// in the group, before any card about the issue is posted there, so every
// later message about the issue continues it. It does nothing (false) when
// the issue already has a thread in the group or the thread carries another
// resource.
func (s *server) chatClaimMessageThread(ctx context.Context, record domain.ChatIntegrationRecord, binding domain.ChatBinding, issueID, root, origin string) (bool, error) {
	if !chatValidPlatformID(root, "om_") {
		return false, nil
	}
	if _, err := s.store.ChatThread(ctx, record.WorkspaceKey, record.ConnectionID, binding.ChatID, domain.ChatThreadResourceIssue, issueID); !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if _, err := s.store.ChatThreadForRoot(ctx, record.WorkspaceKey, record.ConnectionID, root); !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	_, err := s.store.PutChatThread(ctx, record.WorkspaceKey, domain.ChatThread{ConnectionID: record.ConnectionID, ChatID: binding.ChatID, ResourceType: domain.ChatThreadResourceIssue, ResourceID: issueID, RootMessageID: root, BindingID: binding.ID, Origin: origin, SyncFrom: time.Now().UTC()})
	return err == nil, err
}

// chatRecordRequestAsk records the request like Flow's other requests: an
// Ask from the chat platform, by the member, already in triage as its issue.
func (s *server) chatRecordRequestAsk(ctx context.Context, record domain.ChatIntegrationRecord, userID string, issue domain.Issue, templateID string) (string, error) {
	user, err := s.store.UserByID(ctx, userID)
	if err != nil {
		return "", err
	}
	askID := "ask_chat_" + chatKey(record.ConnectionID, "ask", issue.ID)[:32]
	err = s.store.MutateWorkspaceWithAggregate(store.ContextWithActor(ctx, user), record.WorkspaceKey, "ask.created", map[string]string{"source": record.Provider, "issueId": issue.ID}, func(data *domain.Bootstrap) (string, error) {
		if slices.ContainsFunc(data.Asks, func(ask domain.Ask) bool { return ask.ID == askID || ask.IssueID == issue.ID }) {
			return askID, store.ErrNoMutation
		}
		requester := user
		if index := slices.IndexFunc(data.Users, func(item domain.User) bool { return item.ID == userID }); index >= 0 {
			requester = data.Users[index]
		}
		now := time.Now().UTC()
		data.Asks = append([]domain.Ask{{ID: askID, Title: issue.Title, Body: issue.Description, Source: record.Provider, Requester: requester, TeamID: issue.Team.ID, TemplateID: templateID, Status: "approved", IssueID: issue.ID, Approvals: []domain.AskApproval{}, CreatedAt: now, UpdatedAt: now}}, data.Asks...)
		appendAudit(data, "created", "ask", askID, map[string]any{"source": record.Provider, "issueId": issue.ID})
		return askID, nil
	})
	if errors.Is(err, store.ErrNoMutation) {
		err = nil
	}
	return askID, err
}

// chatSendRequestResult confirms a new request in the message's thread with
// the request's card, and privately to a member who used the form.
func (s *server) chatSendRequestResult(ctx context.Context, record domain.ChatIntegrationRecord, binding domain.ChatBinding, issue domain.Issue, job domain.ChatJob, payload chatNotificationJob) error {
	if err := s.chatSendIssueCardFrom(ctx, record, binding, issue, job.DedupKey, payload.SourceMessageID, chatRequestResultKind, time.Time{}); err != nil {
		return err
	}
	if payload.PersonalOpenID == "" {
		return nil
	}
	identity, err := s.store.ChatIdentityForOpenID(ctx, record.WorkspaceKey, record.ConnectionID, payload.PersonalOpenID)
	if err != nil || identity.TenantKey != record.TenantKey {
		return nil
	}
	data, _ := s.store.WorkspaceMetadataFields(record.WorkspaceKey, "userSettings")
	text := "Request saved in Flow: " + issue.Identifier + " " + chatRequestIssueURL(s, record, issue)
	if chatLanguage(data, identity.UserID, record.Provider) == "zh" {
		text = "需求已保存到 Flow：" + issue.Identifier + " " + chatRequestIssueURL(s, record, issue)
	}
	return s.chatSendPersonalText(ctx, record, identity.OpenID, text, chatKey(job.DedupKey, "success"))
}

// chatRequestForIssue is the issue's request made in the binding's group.
func (s *server) chatRequestForIssue(ctx context.Context, record domain.ChatIntegrationRecord, chatID, issueID string) (domain.ChatIntakeRequest, bool, error) {
	requests, err := s.store.ChatIntakeRequestsForIssue(ctx, record.WorkspaceKey, record.ConnectionID, issueID)
	if err != nil {
		return domain.ChatIntakeRequest{}, false, err
	}
	for _, request := range requests {
		if request.ChatID == chatID && request.Status == domain.ChatRequestCreated {
			return request, true, nil
		}
	}
	return domain.ChatIntakeRequest{}, false, nil
}

func chatRequestClosed(stateType string) bool {
	return stateType == "completed" || stateType == "canceled"
}

// chatSendRequestStatus tells the request's thread about a status change: a
// short note, or the outcome when the request is closed (once per closing;
// reopening and closing again replies again).
func (s *server) chatSendRequestStatus(ctx context.Context, record domain.ChatIntegrationRecord, binding domain.ChatBinding, issue domain.Issue, job domain.ChatJob) error {
	request, found, err := s.chatRequestForIssue(ctx, record, binding.ChatID, issue.ID)
	if err != nil || !found {
		return err
	}
	outcome, reason := issue.State.Type, ""
	if data, ok := s.store.WorkspaceMetadataFields(record.WorkspaceKey, "asks"); ok {
		if index := slices.IndexFunc(data.Asks, func(ask domain.Ask) bool { return ask.ID == request.AskID }); index >= 0 && data.Asks[index].Status == "rejected" {
			outcome = "rejected"
			for _, approval := range slices.Backward(data.Asks[index].Approvals) {
				if approval.Decision == "rejected" {
					reason = strings.TrimSpace(approval.Note)
					break
				}
			}
		}
	}
	closed := chatRequestClosed(outcome) || outcome == "rejected"
	if closed && request.Outcome == outcome {
		return nil
	}
	label := issue.Identifier + " " + issue.Title
	var en, zh string
	switch {
	case outcome == "rejected":
		en, zh = "Request rejected: "+label, "需求已拒绝："+label
		if reason != "" {
			reason = chatTruncateText(reason, 2000)
			en, zh = en+"\nReason: "+reason, zh+"\n原因："+reason
		}
	case outcome == "completed":
		en, zh = "Request completed: "+label, "需求已完成："+label
	case outcome == "canceled":
		en, zh = "Request closed ("+issue.State.Name+"): "+label, "需求已关闭（"+issue.State.Name+"）："+label
	case request.Outcome != "":
		en, zh = "Request reopened: "+issue.State.Name, "需求已重新打开："+issue.State.Name
	default:
		en, zh = "Status changed to "+issue.State.Name, "状态已更新为："+issue.State.Name
	}
	elements := []map[string]any{chatCardI18nText(en, zh)}
	if closed {
		elements = append(elements, map[string]any{"tag": "action", "actions": []map[string]any{{"tag": "button", "text": chatI18nPlain("Open in Flow", "在 Flow 中打开"), "url": chatRequestIssueURL(s, record, issue), "type": "default"}}})
	}
	card := map[string]any{"config": map[string]bool{"wide_screen_mode": true, "update_multi": true}, "elements": elements}
	if _, err := s.store.ChatThread(ctx, record.WorkspaceKey, record.ConnectionID, binding.ChatID, domain.ChatThreadResourceIssue, issue.ID); errors.Is(err, sql.ErrNoRows) {
		// The thread is gone: a new one starts with the request's card.
		if err := s.chatSendIssueCardFrom(ctx, record, binding, issue, chatKey(job.DedupKey, "card"), "", chatRequestStatusKind, time.Time{}); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if _, err := s.chatSendInIssueThread(ctx, record, binding, issue.ID, chatKey(job.DedupKey, "status"), "", chatInteractive(card), time.Time{}); err != nil {
		return err
	}
	request.Outcome = ""
	if closed {
		request.Outcome = outcome
	}
	return s.store.UpdateChatIntakeRequest(ctx, record.WorkspaceKey, request)
}
