package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
			RootID      string `json:"root_id"`
			ThreadID    string `json:"thread_id"`
			ParentID    string `json:"parent_id"`
			ChatID      string `json:"chat_id"`
			ChatType    string `json:"chat_type"`
			MessageType string `json:"message_type"`
			Content     string `json:"content"`
			Mentions    []struct {
				Key  string `json:"key"`
				Name string `json:"name"`
				ID   struct {
					OpenID string `json:"open_id"`
				} `json:"id"`
			} `json:"mentions"`
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
	} `json:"event"`
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
	record, err := s.store.ChatIntegrationByID(ctx, r.PathValue("provider"), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "chat integration not found")
		return
	}
	credentials, err := s.chatCredentials(ctx, record)
	if err != nil || credentials.EncryptKey == "" || credentials.VerificationToken == "" {
		writeError(w, http.StatusServiceUnavailable, "chat callback credentials unavailable")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 128<<10))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "chat callback too large")
		return
	}
	plaintext, err := chatCallbackPlaintext(body, credentials.EncryptKey)
	var envelope chatWebhookEnvelope
	if err != nil || json.Unmarshal(plaintext, &envelope) != nil {
		writeError(w, http.StatusUnauthorized, "invalid chat callback")
		return
	}
	if envelope.Type == "url_verification" {
		if envelope.Challenge == "" || len(envelope.Challenge) > 512 || subtle.ConstantTimeCompare([]byte(envelope.Token), []byte(credentials.VerificationToken)) != 1 {
			writeError(w, http.StatusUnauthorized, "invalid chat verification")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"challenge": envelope.Challenge})
		return
	}
	if !record.Connected {
		writeError(w, http.StatusForbidden, "chat integration is not connected")
		return
	}
	timestamp, err := strconv.ParseInt(r.Header.Get(chatCallbackTimestampHeader), 10, 64)
	if err != nil || time.Since(time.Unix(timestamp, 0)) > 5*time.Minute || time.Until(time.Unix(timestamp, 0)) > 5*time.Minute || r.Header.Get(chatCallbackNonceHeader) == "" || !chatCallbackSignatureValid(r.Header, body, credentials.EncryptKey) || subtle.ConstantTimeCompare([]byte(envelope.Header.Token), []byte(credentials.VerificationToken)) != 1 {
		writeError(w, http.StatusUnauthorized, "invalid chat signature")
		return
	}
	if envelope.Schema != "2.0" || envelope.Header.AppID != record.AppID || envelope.Header.TenantKey != record.TenantKey {
		writeError(w, http.StatusForbidden, "chat callback tenant mismatch")
		return
	}
	if envelope.Header.EventType == "url.preview.get" {
		s.writeChatLinkPreview(ctx, w, record, plaintext)
		return
	}
	command, dedup, accepted, err := s.chatCommandForEnvelope(ctx, record, envelope)
	if err != nil {
		// Invalid/expired actions do not enter the queue; this response is visible
		// only to the clicker and never changes a shared group card.
		writeJSON(w, http.StatusOK, map[string]any{"toast": map[string]string{"type": "error", "content": "This action has expired or is unavailable. Open Flow and try again."}})
		return
	}
	if accepted {
		payload, _ := json.Marshal(command)
		_, err = s.store.EnqueueChatJob(ctx, domain.ChatJob{ID: "chat_job_" + dedup, WorkspaceKey: record.WorkspaceKey, ConnectionID: record.ConnectionID, Kind: "inbound", DedupKey: dedup, Payload: payload, RunAt: time.Now().UTC()})
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "chat callback could not be saved")
			return
		}
	}
	if envelope.Header.EventType == "card.action.trigger" {
		writeJSON(w, http.StatusOK, s.chatCallbackToast(ctx, record, command))
	} else {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

func (s *server) chatCommandForEnvelope(ctx context.Context, record domain.ChatIntegrationRecord, envelope chatWebhookEnvelope) (chatInboundCommand, string, bool, error) {
	event := envelope.Event
	switch envelope.Header.EventType {
	case "im.message.receive_v1":
		if event.Message.ChatType == "p2p" {
			return chatDirectMessageCommand(record, envelope) // chat_dm_agent.go
		}
		if command, dedup, ok := s.chatThreadReplyCommand(ctx, record, envelope); ok {
			return command, dedup, true, nil
		}
		if event.Sender.SenderType != "user" || event.Sender.TenantKey != record.TenantKey || event.Sender.SenderID.OpenID == "" || event.Sender.SenderID.OpenID == record.BotOpenID || event.Message.MessageID == "" || event.Message.ChatID == "" || event.Message.ChatType != "group" || event.Message.MessageType != "text" {
			return chatInboundCommand{}, "", false, nil
		}
		var content struct {
			Text string `json:"text"`
		}
		if json.Unmarshal([]byte(event.Message.Content), &content) != nil {
			return chatInboundCommand{}, "", false, nil
		}
		mentioned := false
		for _, mention := range event.Message.Mentions {
			if mention.ID.OpenID == record.BotOpenID && record.BotOpenID != "" {
				mentioned = true
				content.Text = strings.ReplaceAll(content.Text, mention.Key, "")
			}
		}
		if !mentioned {
			return chatInboundCommand{}, "", false, nil
		}
		title, matched := chatCreateTitle(content.Text)
		if !matched {
			return chatInboundCommand{}, "", false, nil
		}
		return chatInboundCommand{Kind: "create_form", OpenID: event.Sender.SenderID.OpenID, ChatID: event.Message.ChatID, MessageID: event.Message.MessageID, Title: title}, chatKey(record.ConnectionID, "message", event.Message.MessageID), true, nil
	case "card.action.trigger":
		if event.Operator.TenantKey != record.TenantKey || event.Operator.OpenID == "" || event.Context.MessageID == "" || event.Context.ChatID == "" {
			return chatInboundCommand{}, "", false, chatInvalid("invalid card operator")
		}
		var actionID string
		if json.Unmarshal(event.Action.Value["actionId"], &actionID) != nil || actionID == "" || len(actionID) > 191 {
			return chatInboundCommand{}, "", false, chatInvalid("invalid card action")
		}
		stored, err := s.store.GetChatAction(ctx, record.WorkspaceKey, record.ConnectionID, actionID)
		var action chatCardAction
		if err != nil || stored.ExpiresAt.Before(time.Now()) || json.Unmarshal(stored.Payload, &action) != nil || !chatActionMatches(stored, action, event.Operator.OpenID, event.Context.ChatID, event.Context.MessageID) {
			return chatInboundCommand{}, "", false, chatInvalid("card action expired")
		}
		// Personal forms are single use even when a double click has another
		// callback ID. Shared buttons may open a fresh private form later.
		dedup := chatKey(record.ConnectionID, "action", actionID, event.Operator.OpenID)
		if stored.UserID == "" || chatQuickActionKind(action.Kind) {
			if envelope.Header.EventID == "" {
				return chatInboundCommand{}, "", false, chatInvalid("missing card callback identity")
			}
			dedup = chatKey(dedup, envelope.Header.EventID)
		}
		values := event.Action.FormValue
		if event.Action.Option != "" && len(event.Action.Option) <= 191 {
			values = map[string]json.RawMessage{"option": json.RawMessage(strconv.Quote(event.Action.Option))}
		}
		return chatInboundCommand{Kind: "card_action", OpenID: event.Operator.OpenID, ChatID: event.Context.ChatID, MessageID: event.Context.MessageID, ActionID: actionID, Values: values}, dedup, true, nil
	}
	return chatInboundCommand{}, "", false, nil
}

func chatActionMatches(stored domain.ChatAction, action chatCardAction, openID, chatID, messageID string) bool {
	if stored.UserID != "" {
		return action.OpenID == openID && action.PersonalChatID == chatID && action.PersonalMessage == messageID
	}
	return action.ChatID == chatID && action.MessageID == messageID && action.Kind != "create" && action.Kind != "state" && action.Kind != "assignee" && action.Kind != "comment"
}

func chatCreateTitle(text string) (string, bool) {
	text = strings.TrimSpace(text)
	for _, prefix := range []string{"创建事项", "create issue"} {
		if strings.HasPrefix(strings.ToLower(text), prefix) {
			title := strings.TrimSpace(text[len(prefix):])
			return title, len(title) <= 2048
		}
	}
	return "", false
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
	return nil
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
	if command.Kind == chatThreadReplyKind {
		return s.processChatThreadReply(ctx, record, job, command)
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
