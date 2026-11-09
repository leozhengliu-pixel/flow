package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
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

// Incoming payloads are deliberately bounded and never written to application logs.
type chatWebhookEnvelope struct {
	Schema    string `json:"schema"`
	Type      string `json:"type"`
	Token     string `json:"token"`
	Challenge string `json:"challenge"`
	Header    struct {
		AppID     string `json:"app_id"`
		TenantKey string `json:"tenant_key"`
		Token     string `json:"token"`
		EventID   string `json:"event_id"`
		EventType string `json:"event_type"`
	} `json:"header"`
	Event struct {
		Sender struct {
			SenderType string `json:"sender_type"`
			TenantKey  string `json:"tenant_key"`
			SenderID   struct {
				OpenID string `json:"open_id"`
			} `json:"sender_id"`
		} `json:"sender"`
		Message struct {
			MessageID string `json:"message_id"`
			// RootID and ThreadID are set on a reply in a thread
			// (chat_issue_threads.go); ParentID on a reply (chat_dm_agent.go).
			RootID      string        `json:"root_id"`
			ThreadID    string        `json:"thread_id"`
			ParentID    string        `json:"parent_id"`
			ChatID      string        `json:"chat_id"`
			ChatType    string        `json:"chat_type"`
			MessageType string        `json:"message_type"`
			Content     string        `json:"content"`
			Mentions    []chatMention `json:"mentions"`
		} `json:"message"`
		Operator struct {
			TenantKey string `json:"tenant_key"`
			OpenID    string `json:"open_id"`
		} `json:"operator"`
		Action struct {
			Value     map[string]json.RawMessage `json:"value"`
			FormValue map[string]json.RawMessage `json:"form_value"`
			Option    string                     `json:"option"`
		} `json:"action"`
		Context struct {
			MessageID string `json:"open_message_id"`
			ChatID    string `json:"open_chat_id"`
		} `json:"context"`
		// A reaction added to a message (im.message.reaction.created_v1,
		// chat_requests.go).
		MessageID    string `json:"message_id"`
		OperatorType string `json:"operator_type"`
		ReactionType struct {
			EmojiType string `json:"emoji_type"`
		} `json:"reaction_type"`
		UserID struct {
			OpenID string `json:"open_id"`
		} `json:"user_id"`
	} `json:"event"`
	// legacyCard marks a click delivered as a legacy card request
	// (chat_callback.go), mapped to card.action.trigger.
	legacyCard bool
}

// chatMention is a mention in a received message: Key is its placeholder in
// the content (@_user_1), ID the mentioned member or bot as this app sees it.
type chatMention struct {
	Key           string `json:"key"`
	Name          string `json:"name"`
	MentionedType string `json:"mentioned_type"`
	ID            struct {
		OpenID string `json:"open_id"`
	} `json:"id"`
}

type chatInboundCommand struct {
	Kind      string                     `json:"kind"`
	OpenID    string                     `json:"openId"`
	ChatID    string                     `json:"chatId"`
	MessageID string                     `json:"messageId"`
	ActionID  string                     `json:"actionId,omitempty"`
	Title     string                     `json:"title,omitempty"`
	Values    map[string]json.RawMessage `json:"values,omitempty"`
	// RootMessageID and Text carry a reply in an issue thread.
	RootMessageID string `json:"rootMessageId,omitempty"`
	Text          string `json:"text,omitempty"`
}

type chatCardAction struct {
	Kind            string   `json:"kind"`
	BindingID       string   `json:"bindingId"`
	BindingScope    string   `json:"bindingScope"`
	ChatID          string   `json:"chatId"`
	MessageID       string   `json:"messageId,omitempty"`
	SourceMessageID string   `json:"sourceMessageId,omitempty"`
	PersonalChatID  string   `json:"personalChatId,omitempty"`
	PersonalMessage string   `json:"personalMessage,omitempty"`
	OpenID          string   `json:"openId,omitempty"`
	IssueID         string   `json:"issueId,omitempty"`
	ExpectedVersion int64    `json:"expectedVersion,omitempty"`
	Title           string   `json:"title,omitempty"`
	StateIDs        []string `json:"stateIds,omitempty"`
	AssigneeIDs     []string `json:"assigneeIds,omitempty"`
	// ProjectIDs are the projects a create form offers (a binding's set).
	ProjectIDs []string `json:"projectIds,omitempty"`
}

type chatPermanentError struct{ message string }

func (e chatPermanentError) Error() string { return e.message }

func chatInvalid(message string) error { return chatPermanentError{message: message} }

type chatDeliveryProgressKey struct{}
type chatDeliveryProgress struct{ sent bool }

func (s *server) chatWorkflowRequest(ctx context.Context, record domain.ChatIntegrationRecord, method, path string, body, result any) error {
	err := s.chatPlatformRequest(ctx, record, method, path, body, result)
	if err == nil && (method == http.MethodPost || method == http.MethodPatch) {
		if progress, ok := ctx.Value(chatDeliveryProgressKey{}).(*chatDeliveryProgress); ok {
			progress.sent = true
		}
	}
	return err
}

func (s *server) chatDeliveryHealth(ctx context.Context, job domain.ChatJob, failure *chatPlatformError, succeeded bool) error {
	if failure == nil && !succeeded {
		return nil
	}
	record, err := s.chatJobIntegration(ctx, job)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var deliveryErr error
	if failure != nil {
		deliveryErr = failure
	}
	return s.recordChatDeliveryResult(ctx, record, deliveryErr)
}

func (s *server) chatJobIntegration(ctx context.Context, job domain.ChatJob) (domain.ChatIntegrationRecord, error) {
	record, err := s.store.ChatIntegrationByID(ctx, "feishu", job.ConnectionID)
	if errors.Is(err, sql.ErrNoRows) {
		return s.store.ChatIntegrationByID(ctx, "lark", job.ConnectionID)
	}
	return record, err
}

func chatKey(parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = hash.Write([]byte(strconv.Itoa(len(part)) + ":" + part))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// chat_callback.go handles the platform's signature and encryption formats. The
// service additionally binds every authenticated event to this exact app and
// tenant.
func (s *server) handleChatWebhook(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2500*time.Millisecond)
	defer cancel()
	diagnostic := chatCallbackLog{RequestID: requestIDFrom(r.Context()), Provider: r.PathValue("provider"), ConnectionID: r.PathValue("id"), Format: chatCallbackFormatUnknown}
	record, err := s.store.ChatIntegrationByID(ctx, r.PathValue("provider"), r.PathValue("id"))
	if err != nil {
		diagnostic.rejected(ctx, "unknown_connection")
		writeError(w, http.StatusNotFound, "chat integration not found")
		return
	}
	diagnostic.AppID = record.AppID
	credentials, err := s.chatCredentials(ctx, record)
	if err != nil || credentials.EncryptKey == "" || credentials.VerificationToken == "" {
		diagnostic.rejected(ctx, "credentials_unavailable")
		writeError(w, http.StatusServiceUnavailable, "chat callback credentials unavailable")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 128<<10))
	if err != nil {
		diagnostic.rejected(ctx, "too_large")
		writeError(w, http.StatusRequestEntityTooLarge, "chat callback too large")
		return
	}
	callback, reason := chatVerifyCallback(r.Header, body, credentials, record, time.Now())
	envelope := callback.Envelope
	diagnostic.Format, diagnostic.Encrypted, diagnostic.Scheme = callback.Format, callback.Encrypted, callback.Scheme
	diagnostic.EventType = firstNonEmpty(envelope.Header.EventType, callback.Format)
	if callback.Format == chatCallbackFormatLegacyCard && envelope.Header.EventType == "card.action.trigger" {
		diagnostic.EventType = chatLegacyCardEventType
	}
	if envelope.Header.AppID != "" && reason != chatRejectAppMismatch {
		diagnostic.AppID = envelope.Header.AppID
	}
	if reason != "" {
		// The platform shows a member any answer to a card click other than
		// HTTP 200 with a valid body as a bare error code, so a refused card
		// click gets 200 and an error toast instead. Nothing is queued: the
		// answer only tells the clicker the click did nothing.
		if callback.Format == chatCallbackFormatLegacyCard || callback.Format == chatCallbackFormatEvent && envelope.Header.EventType == "card.action.trigger" {
			diagnostic.rejected(ctx, reason, append(callback.Timestamp.attrs(), "answer", "error_toast")...)
			writeJSON(w, http.StatusOK, map[string]any{"toast": map[string]any{"type": "error", "content": "This action could not be verified. Try again, or open Flow.", "i18n": map[string]string{"en_us": "This action could not be verified. Try again, or open Flow.", "zh_cn": "无法验证此操作，请重试或在 Flow 中操作。"}}})
			return
		}
		diagnostic.rejected(ctx, reason, callback.Timestamp.attrs()...)
		writeError(w, chatRejectStatus(reason), "invalid chat callback")
		return
	}
	if callback.Format == chatCallbackFormatChallenge {
		diagnostic.handled(ctx, "challenge", "")
		writeJSON(w, http.StatusOK, map[string]string{"challenge": envelope.Challenge})
		return
	}
	if envelope.Header.EventType == "url.preview.get" {
		diagnostic.handled(ctx, "link_preview", "")
		s.writeChatLinkPreview(ctx, w, record, callback.Plaintext)
		return
	}
	command, dedup, ignored, err := s.chatCommandForEnvelopeReason(ctx, record, envelope)
	var toast chatToastError
	if errors.As(err, &toast) {
		// Another member's card in a group thread (chat_group_agent.go).
		diagnostic.handled(ctx, "refused", ignored, chatEnvelopeLogAttrs(envelope)...)
		writeJSON(w, http.StatusOK, toast.toast())
		return
	}
	if err != nil {
		// Invalid/expired actions do not enter the queue; this response is visible
		// only to the clicker and never changes a shared group card.
		diagnostic.handled(ctx, "refused", ignored, chatEnvelopeLogAttrs(envelope)...)
		writeJSON(w, http.StatusOK, map[string]any{"toast": map[string]any{"type": "error", "content": "This action has expired or is unavailable. Open Flow and try again.", "i18n": map[string]string{"en_us": "This action has expired or is unavailable. Open Flow and try again.", "zh_cn": "此操作已过期或不可用，请打开 Flow 后重试。"}}})
		return
	}
	if ignored == "" {
		payload, _ := json.Marshal(command)
		_, err = s.store.EnqueueChatJob(ctx, domain.ChatJob{ID: "chat_job_" + dedup, WorkspaceKey: record.WorkspaceKey, ConnectionID: record.ConnectionID, Kind: "inbound", DedupKey: dedup, Payload: payload, RunAt: time.Now().UTC()})
		if err != nil {
			diagnostic.rejected(ctx, "queue_unavailable")
			writeError(w, http.StatusServiceUnavailable, "chat callback could not be saved")
			return
		}
		diagnostic.handled(ctx, "queued", command.Kind, append(chatEnvelopeLogAttrs(envelope), "job_id", "chat_job_"+dedup)...)
	} else {
		diagnostic.handled(ctx, "ignored", ignored, chatEnvelopeLogAttrs(envelope)...)
	}
	if envelope.Header.EventType == "card.action.trigger" {
		writeJSON(w, http.StatusOK, s.chatCallbackToast(ctx, record, command))
	} else {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

// chatCommandForEnvelope maps an authenticated event to its queued command;
// accepted is false when the event asks nothing of Flow.
func (s *server) chatCommandForEnvelope(ctx context.Context, record domain.ChatIntegrationRecord, envelope chatWebhookEnvelope) (chatInboundCommand, string, bool, error) {
	command, dedup, ignored, err := s.chatCommandForEnvelopeReason(ctx, record, envelope)
	return command, dedup, ignored == "" && err == nil, err
}

// chatSenderIgnoreReason is why a message's sender or IDs make it nothing
// for Flow: the bot itself, another app, another tenant, a malformed event.
func chatSenderIgnoreReason(record domain.ChatIntegrationRecord, envelope chatWebhookEnvelope) string {
	event := envelope.Event
	switch {
	case event.Sender.SenderType != "user":
		return "sender_not_user"
	case event.Sender.SenderID.OpenID == "":
		return "sender_missing"
	case event.Sender.SenderID.OpenID == record.BotOpenID:
		return "sender_is_bot"
	case event.Sender.TenantKey != record.TenantKey:
		return "sender_other_tenant"
	case !chatValidPlatformID(event.Message.MessageID, "om_"):
		return "message_id_invalid"
	case event.Message.ChatID == "":
		return "chat_id_missing"
	}
	return ""
}

// chatCommandForEnvelopeReason is chatCommandForEnvelope with the reason
// code of an event that yields no command (ignored) or of a refused card
// click (with err).
func (s *server) chatCommandForEnvelopeReason(ctx context.Context, record domain.ChatIntegrationRecord, envelope chatWebhookEnvelope) (chatInboundCommand, string, string, error) {
	event := envelope.Event
	switch envelope.Header.EventType {
	case "im.message.receive_v1":
		if event.Message.ChatType == "p2p" {
			command, dedup, ok, err := chatDirectMessageCommand(record, envelope) // chat_dm_agent.go
			if !ok && err == nil {
				return command, dedup, firstNonEmpty(chatSenderIgnoreReason(record, envelope), "dm_content_too_large"), nil
			}
			return command, dedup, "", err
		}
		if reason := chatSenderIgnoreReason(record, envelope); reason != "" {
			return chatInboundCommand{}, "", reason, nil
		}
		if event.Message.ChatType != "group" {
			return chatInboundCommand{}, "", "chat_type_unsupported", nil
		}
		command, dedup, threadReason := s.chatThreadReplyCommandReason(ctx, record, envelope)
		if threadReason == "" {
			return command, dedup, "", nil
		}
		text, mentioned, ok := chatThreadReplyText(record, envelope)
		// A reply in a thread the bot started continues its conversation,
		// with or without a mention (chat_group_agent.go).
		if ok && !mentioned && threadReason != chatThreadNotThread {
			if thread, found := s.chatGroupThreadFor(ctx, record, event.Message.RootID); found && thread.Started && thread.ChatID == event.Message.ChatID {
				if command, dedup, accepted := chatGroupAgentCommand(record, envelope, text, false); accepted {
					return command, dedup, "", nil
				}
				return chatInboundCommand{}, "", "agent_thread_reply_empty", nil
			}
		}
		switch {
		case !ok && event.Message.MessageType != "text" && event.Message.MessageType != "post":
			return chatInboundCommand{}, "", "message_type_unsupported", nil
		case !ok:
			return chatInboundCommand{}, "", "content_unreadable", nil
		case !mentioned && threadReason != chatThreadNotThread:
			// A reply in a thread that is not an issue's, or empty.
			return chatInboundCommand{}, "", threadReason, nil
		case !mentioned && slices.ContainsFunc(event.Message.Mentions, func(mention chatMention) bool { return mention.MentionedType == "bot" }):
			// Another bot, or this bot under an open_id other than the one
			// recorded for the app.
			return chatInboundCommand{}, "", "other_bot_mentioned", nil
		case !mentioned:
			return chatInboundCommand{}, "", "bot_not_mentioned", nil
		}
		// Text and rich text alike, with the bot's mention removed.
		if title, matched := chatCreateTitle(text); matched {
			return chatInboundCommand{Kind: "create_form", OpenID: event.Sender.SenderID.OpenID, ChatID: event.Message.ChatID, MessageID: event.Message.MessageID, Title: title}, chatKey(record.ConnectionID, "message", event.Message.MessageID), "", nil
		}
		// An explicit request (chat_requests.go).
		if command, dedup, ok := s.chatRequestMentionCommand(ctx, record, envelope, text); ok {
			return command, dedup, "", nil
		}
		// A bare mention, or "help", gets the group's help card privately.
		if strings.TrimSpace(text) == "" || chatDMCommandIs(text, "help", "帮助") {
			return chatInboundCommand{Kind: "group_help", OpenID: event.Sender.SenderID.OpenID, ChatID: event.Message.ChatID, MessageID: event.Message.MessageID}, chatKey(record.ConnectionID, "message", event.Message.MessageID), "", nil
		}
		// Anything else asks the Flow Agent (chat_group_agent.go).
		if command, dedup, accepted := chatGroupAgentCommand(record, envelope, text, true); accepted {
			return command, dedup, "", nil
		}
		return chatInboundCommand{}, "", "content_too_large", nil
	case "im.message.reaction.created_v1":
		command, dedup, ok := s.chatRequestReactionCommand(ctx, record, envelope)
		if !ok {
			return command, dedup, "reaction_not_request", nil
		}
		return command, dedup, "", nil
	case "card.action.trigger":
		// A legacy card request may not name the chat: the stored action
		// does, and the message ID alone identifies the card.
		if event.Operator.TenantKey != record.TenantKey || event.Operator.OpenID == "" || event.Context.MessageID == "" || event.Context.ChatID == "" && !envelope.legacyCard {
			return chatInboundCommand{}, "", "card_operator_invalid", chatInvalid("invalid card operator")
		}
		var actionID string
		if json.Unmarshal(event.Action.Value["actionId"], &actionID) != nil || actionID == "" || len(actionID) > 191 {
			return chatInboundCommand{}, "", "card_action_invalid", chatInvalid("invalid card action")
		}
		stored, err := s.store.GetChatAction(ctx, record.WorkspaceKey, record.ConnectionID, actionID)
		var action chatCardAction
		if err != nil || json.Unmarshal(stored.Payload, &action) != nil {
			return chatInboundCommand{}, "", "card_action_unknown", chatInvalid("card action expired")
		}
		if stored.ExpiresAt.Before(time.Now()) {
			return chatInboundCommand{}, "", "card_action_expired", chatInvalid("card action expired")
		}
		chatID := event.Context.ChatID
		if chatID == "" {
			chatID = action.ChatID
			if stored.UserID != "" {
				chatID = action.PersonalChatID
			}
		}
		if !chatActionMatches(stored, action, event.Operator.OpenID, chatID, event.Context.MessageID) {
			if refusal, ok := chatGroupActionRefusal(stored, event.Operator.OpenID, chatID, event.Context.MessageID); ok {
				return chatInboundCommand{}, "", "card_action_other_member", refusal
			}
			return chatInboundCommand{}, "", "card_action_mismatch", chatInvalid("card action expired")
		}
		// Personal forms are single use even when a double click has another
		// callback ID. Shared buttons may open a fresh private form later.
		dedup := chatKey(record.ConnectionID, "action", actionID, event.Operator.OpenID)
		if stored.UserID == "" || chatQuickActionKind(action.Kind) {
			if envelope.Header.EventID == "" {
				return chatInboundCommand{}, "", "card_event_id_missing", chatInvalid("missing card callback identity")
			}
			dedup = chatKey(dedup, envelope.Header.EventID)
		}
		values := event.Action.FormValue
		if event.Action.Option != "" && len(event.Action.Option) <= 191 {
			values = map[string]json.RawMessage{"option": json.RawMessage(strconv.Quote(event.Action.Option))}
		}
		return chatInboundCommand{Kind: "card_action", OpenID: event.Operator.OpenID, ChatID: chatID, MessageID: event.Context.MessageID, ActionID: actionID, Values: values}, dedup, "", nil
	}
	return chatInboundCommand{}, "", "event_type_unsupported", nil
}

func chatActionMatches(stored domain.ChatAction, action chatCardAction, openID, chatID, messageID string) bool {
	if stored.UserID != "" {
		return action.OpenID == openID && action.PersonalChatID == chatID && action.PersonalMessage == messageID
	}
	return action.ChatID == chatID && action.MessageID == messageID && action.Kind != "create" && action.Kind != "state" && action.Kind != "assignee" && action.Kind != "comment"
}

// chatCreateCommand is a create command after the bot's mention: 创建事项 /
// 新建事项 (spaces optional, the title may follow directly) or create issue /
// new issue (any case, any spacing, not "create issues"), optionally behind a
// slash, then an optional separator (half- or full-width colon or comma).
var chatCreateCommand = regexp.MustCompile(`(?is)^/?\s*(?:(?:创建|新建)\s*事项|(?:create|new)\s+issue\b)[\s:：,，、]*(.*)$`)

func chatCreateTitle(text string) (string, bool) {
	match := chatCreateCommand.FindStringSubmatch(strings.TrimSpace(text))
	if match == nil {
		return "", false
	}
	title := strings.TrimSpace(match[1])
	return title, len(title) <= 2048
}

func (s *server) processChatJobs(ctx context.Context, workspace string) error {
	for _, kind := range []string{"inbound", "card", "notification", chatPersonalJobKind} {
		for range 4 {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			jobs, err := s.store.ClaimChatJobs(ctx, workspace, kind, 1, time.Now().UTC())
			if err != nil {
				return err
			}
			if len(jobs) == 0 {
				break
			}
			job := jobs[0]
			jobCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
			progress := &chatDeliveryProgress{}
			jobCtx = context.WithValue(jobCtx, chatDeliveryProgressKey{}, progress)
			err = s.processChatJob(jobCtx, job)
			cancel()
			status, message, runAt := "completed", "", time.Time{}
			if err != nil {
				message = "Chat operation failed; check application permissions and connection health."
				var platform *chatPlatformError
				errors.As(err, &platform)
				if chatJobFinal(err, job) {
					status = "failed"
				} else {
					status = "pending"
					delay := time.Duration(1<<min(job.Attempts, 8))*5*time.Second + time.Duration(job.ID[len(job.ID)-1]%5)*time.Second
					if platform != nil && platform.RetryAfter > delay {
						delay = platform.RetryAfter
					}
					runAt = time.Now().UTC().Add(delay)
				}
			}
			var platformFailure *chatPlatformError
			if status == "failed" && errors.As(err, &platformFailure) {
				if healthErr := s.chatDeliveryHealth(ctx, job, platformFailure, false); healthErr != nil && !errors.Is(healthErr, store.ErrNoMutation) {
					return healthErr
				}
			} else if processing := chatProcessingErrorMessage(err); status == "failed" && processing != "" {
				// Failed inside Flow: the integration page shows it, the job
				// keeps the reason, the log names the job and the step.
				message = processing
				slog.WarnContext(ctx, "chat job failed", "job_id", job.ID, "kind", job.Kind, "connection_id", job.ConnectionID, "attempts", job.Attempts, "error_kind", chatFailureKind(err))
				if record, recordErr := s.chatJobIntegration(ctx, job); recordErr == nil {
					if healthErr := s.recordChatDeliveryResultLocked(ctx, record, err); healthErr != nil && !errors.Is(healthErr, store.ErrNoMutation) {
						return healthErr
					}
				}
			} else if err == nil && progress.sent {
				if healthErr := s.chatDeliveryHealth(ctx, job, nil, true); healthErr != nil && !errors.Is(healthErr, store.ErrNoMutation) {
					return healthErr
				}
			}
			if finishErr := s.store.FinishChatJob(ctx, job, status, runAt, message); finishErr != nil {
				return finishErr
			}
		}
	}
	return s.startChatDMReplyJobs(ctx, workspace)
}

func (s *server) processChatJob(ctx context.Context, job domain.ChatJob) error {
	record, err := s.chatJobIntegration(ctx, job)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && job.Kind == chatPersonalJobKind && record.WorkspaceKey == job.WorkspaceKey {
		// Ends its notification delivery with the reason, healthy or not.
		return s.processChatPersonalNotification(ctx, record, job)
	}
	if errors.Is(err, sql.ErrNoRows) || !record.Connected || record.WorkspaceKey != job.WorkspaceKey {
		return chatInvalid("chat integration disconnected")
	}
	if job.Kind == "notification" || job.Kind == "card" {
		return s.processChatNotification(ctx, record, job)
	}
	var command chatInboundCommand
	if json.Unmarshal(job.Payload, &command) != nil {
		return chatInvalid("invalid stored chat command")
	}
	err = s.processChatInbound(ctx, record, job, command)
	// A command that fails inside Flow for good answers its member (a
	// direct message answers in its own conversation, chatDMFailure).
	if err != nil && command.OpenID != "" && command.Kind != "dm_message" && command.Kind != chatGroupAgentKind && chatProcessingErrorMessage(err) != "" && chatJobFinal(err, job) {
		if noticeErr := s.chatSendPersonalText(ctx, record, command.OpenID, chatActionErrorText("chat_processing_failed", chatLanguage(domain.Bootstrap{}, "", record.Provider)), job.DedupKey+":processing"); noticeErr != nil {
			slog.WarnContext(ctx, "chat failure notice not delivered", "job_id", job.ID, "connection_id", job.ConnectionID, "error_kind", chatFailureKind(noticeErr))
		}
	}
	return err
}

// processChatInbound runs a queued inbound command.
func (s *server) processChatInbound(ctx context.Context, record domain.ChatIntegrationRecord, job domain.ChatJob, command chatInboundCommand) error {
	if command.Kind == chatThreadReplyKind {
		return s.processChatThreadReply(ctx, record, job, command)
	}
	if command.Kind == chatRequestKind {
		return s.processChatRequest(ctx, record, job, command)
	}
	if command.Kind == "group_help" {
		return s.chatSendGroupHelp(ctx, record, job, command)
	}
	// Direct messages with the bot and their cards (chat_dm_agent.go).
	if handled, err := s.processChatDirectMessage(ctx, record, job, command); handled {
		return err
	}
	identity, err := s.store.ChatIdentityForOpenID(ctx, record.WorkspaceKey, record.ConnectionID, command.OpenID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if errors.Is(err, sql.ErrNoRows) || identity.TenantKey != record.TenantKey {
		if command.Kind == chatMessageActionKind {
			return s.chatSendLinkAccountCard(ctx, record, job, command.OpenID)
		}
		return s.chatSendBindingLink(ctx, record, job, command.OpenID)
	}
	ctx = withChatIdentityAuthentication(ctx, identity)
	var metadata domain.Bootstrap
	if err := s.chatAuthorizedMutation(ctx, record.WorkspaceKey, identity.UserID, http.MethodGet, "/api/issue-records/bootstrap", nil, &metadata); err != nil {
		return s.chatMutationFailure(ctx, record, job, command.OpenID, err, "Your Flow workspace access has changed. Sign in to Flow and check your access.")
	}
	if command.Kind == chatMessageActionKind {
		return s.chatSendMessageActionForm(ctx, record, identity, metadata, job, command)
	}
	if command.Kind == "create_form" {
		binding, err := s.chatBindingForChat(ctx, record, command.ChatID)
		if err != nil {
			var permanent chatPermanentError
			if !errors.As(err, &permanent) && !errors.Is(err, store.ErrAuthForbidden) {
				return err
			}
		}
		if err != nil || !binding.AllowCreate {
			return s.chatSendCommandError(ctx, record, job, command.OpenID, "This group is not enabled for creating Flow issues.")
		}
		return s.chatSendCreateForm(ctx, record, binding, identity, metadata, job, command)
	}
	stored, err := s.store.GetChatAction(ctx, record.WorkspaceKey, record.ConnectionID, command.ActionID)
	var action chatCardAction
	if err != nil || time.Now().After(stored.ExpiresAt) || json.Unmarshal(stored.Payload, &action) != nil || !chatActionMatches(stored, action, command.OpenID, command.ChatID, command.MessageID) || stored.UserID != "" && stored.UserID != identity.UserID {
		return s.chatSendCommandError(ctx, record, job, command.OpenID, "This action has expired. Open a current issue card and try again.")
	}
	if handled, err := s.chatRunExtendedAction(ctx, record, identity, metadata, job, command, stored, action); handled {
		return err
	}
	binding, err := s.chatBindingByID(ctx, record, action.BindingID)
	if err != nil {
		return chatInvalid("group binding unavailable")
	}
	if action.BindingScope != chatBindingFingerprint(binding) || action.ChatID != binding.ChatID {
		return s.chatSendCommandError(ctx, record, job, command.OpenID, "This group's Flow scope changed after the card opened. Send a new command or open a current card before continuing.")
	}
	if strings.HasPrefix(action.Kind, "open_") {
		if !binding.AllowUpdate {
			return chatInvalid("group updates disabled")
		}
		return s.chatSendUpdateForm(ctx, record, binding, identity, metadata, job, command, action)
	}
	return s.chatExecuteAction(ctx, record, binding, identity, metadata, job, command, action)
}

func (s *server) chatBindingByID(ctx context.Context, record domain.ChatIntegrationRecord, id string) (domain.ChatBinding, error) {
	bindings, err := s.store.ChatBindings(ctx, record.WorkspaceKey, record.ConnectionID)
	if err != nil {
		return domain.ChatBinding{}, err
	}
	for _, binding := range bindings {
		if binding.ID == id && binding.Enabled && binding.RevokedAt == nil {
			if err := s.chatBindingPermitted(ctx, record, binding); err != nil {
				return domain.ChatBinding{}, err
			}
			return binding, nil
		}
	}
	return domain.ChatBinding{}, chatInvalid("chat binding not found")
}

func (s *server) chatBindingForChat(ctx context.Context, record domain.ChatIntegrationRecord, chatID string) (domain.ChatBinding, error) {
	bindings, err := s.store.ChatBindings(ctx, record.WorkspaceKey, record.ConnectionID)
	if err != nil {
		return domain.ChatBinding{}, err
	}
	var result *domain.ChatBinding
	for _, binding := range bindings {
		if binding.ChatID == chatID && binding.Enabled && binding.RevokedAt == nil {
			if result != nil {
				return domain.ChatBinding{}, chatInvalid("choose a single group create scope")
			}
			copy := binding
			result = &copy
		}
	}
	if result == nil {
		return domain.ChatBinding{}, chatInvalid("chat binding not found")
	}
	if err := s.chatBindingPermitted(ctx, record, *result); err != nil {
		return domain.ChatBinding{}, err
	}
	return *result, nil
}

func (s *server) chatSendBindingLink(ctx context.Context, record domain.ChatIntegrationRecord, job domain.ChatJob, openID string) error {
	link := fmt.Sprintf("%s/%s/settings/account/connections", s.chatAppOrigin(), url.PathEscape(record.WorkspaceKey))
	return s.chatSendPersonalText(ctx, record, openID, "Connect your Flow account before creating or updating issues: "+link, job.DedupKey)
}

func (s *server) chatSendCommandError(ctx context.Context, record domain.ChatIntegrationRecord, job domain.ChatJob, openID, text string) error {
	return s.chatSendPersonalText(ctx, record, openID, text, job.DedupKey)
}

func (s *server) chatMutationFailure(ctx context.Context, record domain.ChatIntegrationRecord, job domain.ChatJob, openID string, err error, message string) error {
	var failure *chatMutationError
	if errors.As(err, &failure) && failure.Status >= 400 && failure.Status < 500 {
		if failure.Status == http.StatusConflict {
			message = "This issue changed after the form opened. Open a current card and confirm your change again."
		}
		if failure.Message == chatReconnectMessage {
			message = chatReconnectMessage
		}
		return s.chatSendCommandError(ctx, record, job, openID, message)
	}
	return err
}

func (s *server) chatSendPersonalText(ctx context.Context, record domain.ChatIntegrationRecord, openID, text, key string) error {
	content, _ := json.Marshal(map[string]string{"text": text})
	var result chatMessageResult
	return s.chatWorkflowRequest(ctx, record, http.MethodPost, "/open-apis/im/v1/messages?receive_id_type=open_id", map[string]any{"receive_id": openID, "msg_type": "text", "content": string(content), "uuid": chatKey(key, "personal_text")[:32]}, &result)
}

func chatString(values map[string]json.RawMessage, key string) string {
	var result string
	_ = json.Unmarshal(values[key], &result)
	return strings.TrimSpace(result)
}

func chatContains(values []string, value string) bool { return slices.Contains(values, value) }

func chatCreateInput(binding domain.ChatBinding, action chatCardAction, values map[string]json.RawMessage) (domain.IssueCreateInput, error) {
	title := chatString(values, "title")
	if title == "" {
		title = action.Title
	}
	if title == "" || len(title) > 2048 || utf8.RuneCountInString(title) > 1000 {
		return domain.IssueCreateInput{}, chatInvalid("a title is required")
	}
	input := domain.IssueCreateInput{Title: title, TeamID: binding.TeamID, TemplateID: binding.TemplateID}
	if projectID, err := chatCreateProject(binding, action, values); err != nil {
		return input, err
	} else if projectID != "" {
		input.ProjectID = &projectID
	}
	stateID := chatString(values, "stateId")
	if stateID != "" {
		if !chatContains(action.StateIDs, stateID) {
			return input, chatInvalid("invalid team state")
		}
		input.StateID = &stateID
	}
	assigneeID := chatString(values, "assigneeId")
	if assigneeID == "__unassigned" {
		unassigned := ""
		input.AssigneeID = &unassigned
	} else if assigneeID != "" {
		if !chatContains(action.AssigneeIDs, assigneeID) {
			return input, chatInvalid("invalid assignee")
		}
		input.AssigneeID = &assigneeID
	}
	priority := chatString(values, "priority")
	if priority != "" {
		value, err := strconv.Atoi(priority)
		if err != nil || value < 0 || value > 4 {
			return input, chatInvalid("invalid priority")
		}
		input.Priority = &value
	}
	return input, nil
}

func (s *server) chatExecuteAction(ctx context.Context, record domain.ChatIntegrationRecord, binding domain.ChatBinding, identity domain.ChatIdentity, metadata domain.Bootstrap, job domain.ChatJob, command chatInboundCommand, action chatCardAction) error {
	var issue domain.Issue
	var err error
	if action.Kind == "create" {
		if !binding.AllowCreate {
			return chatInvalid("group creation disabled")
		}
		if _, validationErr := chatCreateDefaults(metadata, binding, action.Title); validationErr != nil {
			return s.chatSendCommandError(ctx, record, job, command.OpenID, "The group's team or template changed. Send a new create command and review the current form.")
		}
		input, inputErr := chatCreateInput(binding, action, command.Values)
		if inputErr != nil {
			return s.chatSendCommandError(ctx, record, job, command.OpenID, "The form has invalid fields. Send a new create command and try again.")
		}
		ctx = store.WithIssueCreationKey(ctx, "issue_chat_"+chatKey(record.ConnectionID, command.ActionID))
		err = s.chatAuthorizedMutation(ctx, record.WorkspaceKey, identity.UserID, http.MethodPost, "/api/issue-records", input, &issue)
	} else {
		if !binding.AllowUpdate {
			return chatInvalid("group updates disabled")
		}
		if err = s.chatAuthorizedMutation(ctx, record.WorkspaceKey, identity.UserID, http.MethodGet, "/api/issue-records/"+url.PathEscape(action.IssueID), nil, &issue); err != nil {
			return s.chatMutationFailure(ctx, record, job, command.OpenID, err, "You no longer have access to this issue.")
		}
		if !chatIssueMatchesBinding(issue, binding) {
			return chatInvalid("issue outside binding scope")
		}
		input := domain.IssueUpdateInput{ExpectedVersion: &action.ExpectedVersion}
		switch action.Kind {
		case "state":
			stateID := chatString(command.Values, "stateId")
			if !chatContains(action.StateIDs, stateID) {
				return chatInvalid("invalid team state")
			}
			input.StateID = &stateID
		case "assignee":
			assigneeID := chatString(command.Values, "assigneeId")
			if assigneeID == "__unassigned" {
				assigneeID = ""
			} else if !chatContains(action.AssigneeIDs, assigneeID) {
				return chatInvalid("invalid assignee")
			}
			input.AssigneeID = &assigneeID
		case "comment":
			body := chatString(command.Values, "comment")
			if body == "" || utf8.RuneCountInString(body) > 1000 {
				return chatInvalid("a comment is required")
			}
			ctx = store.WithCommentCreationKey(ctx, "comment_chat_"+chatKey(record.ConnectionID, command.ActionID))
			var comment domain.Comment
			err = s.chatAuthorizedMutation(ctx, record.WorkspaceKey, identity.UserID, http.MethodPost, "/api/issue-records/"+url.PathEscape(action.IssueID)+"/comments", domain.CommentCreateInput{Body: body}, &comment)
		default:
			return chatInvalid("unsupported card action")
		}
		if action.Kind != "comment" {
			ctx = store.WithChatMutationKey(ctx, chatKey(record.ConnectionID, command.ActionID))
			err = s.chatAuthorizedMutation(ctx, record.WorkspaceKey, identity.UserID, http.MethodPatch, "/api/issue-records/"+url.PathEscape(action.IssueID), input, &issue)
		}
	}
	if err != nil {
		return s.chatMutationFailure(ctx, record, job, command.OpenID, err, "Flow could not apply this change. Open Flow to check permissions, the current workflow, and any concurrent changes.")
	}
	// Persist a separate delivery before retiring the action. A crash between a
	// business commit and this enqueue is recovered using the stable mutation key.
	payload, _ := json.Marshal(chatNotificationJob{BindingID: binding.ID, BindingScope: chatBindingFingerprint(binding), IssueID: issue.ID, SourceMessageID: action.SourceMessageID, Kind: "result", Operation: action.Kind, PersonalOpenID: command.OpenID})
	_, err = s.store.EnqueueChatJob(ctx, domain.ChatJob{ID: "chat_result_" + job.DedupKey, WorkspaceKey: record.WorkspaceKey, ConnectionID: record.ConnectionID, Kind: "card", DedupKey: chatKey(job.DedupKey, "result"), Payload: payload, RunAt: time.Now().UTC()})
	return err
}

// chatIssueMatchesBinding: an issue of the bound team (and project), with no
// issue-level restrictions. A private team's issues match only a binding its
// owners authorized for the private team.
func chatIssueMatchesBinding(issue domain.Issue, binding domain.ChatBinding) bool {
	return issue.ID != "" && binding.RevokedAt == nil && (!issue.Team.Private || chatPrivateBinding(binding)) && len(issue.Permissions) == 0 && issue.Team.ArchivedAt == nil && issue.ArchivedAt == nil && issue.Team.ID == binding.TeamID && binding.CoversProject(chatIssueProjectID(issue))
}

func chatBindingFingerprint(binding domain.ChatBinding) string {
	return chatKey(binding.ID, binding.ChatID, binding.TeamID, chatBindingProjectKey(binding), binding.TemplateID)
}
