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
	"strings"
	"time"
	"unicode/utf8"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Issue threads: everything a group hears about one issue goes into one
// message thread. The first message about the issue (its card, or the
// member's create command it answered) is the thread's root; later
// notifications reply to it. Flow comments on the issue are mirrored into the
// thread, edits and deletions are reflected on the mirrored message, and a
// linked member's reply in the thread becomes a Flow comment written as them,
// with their own permissions, when the group's binding allows updates.
//
// The mapping is generic (resource type and ID ↔ root message; item ↔
// message, store/chat_threads.go): every item records the side it started
// on, so a reply imported from the thread is never posted back to it, and the
// bot never imports its own messages (they are not sent by a user).

const (
	chatThreadReplyKind    = "thread_reply"
	chatThreadItemComment  = "comment"
	chatThreadItemLinkCard = "link_prompt"
	// chatThreadSyncLimit bounds the messages one sync posts; the next event
	// continues.
	chatThreadSyncLimit = 20
	// chatThreadCommentLimit bounds a comment imported from a reply.
	chatThreadCommentLimit = 10000
)

type chatThreadMessageResult struct {
	Data struct {
		MessageID string `json:"message_id"`
		RootID    string `json:"root_id"`
		ThreadID  string `json:"thread_id"`
		ChatID    string `json:"chat_id"`
	} `json:"data"`
}

// chatMessageGone: the platform refused a reply or an update because the
// message was recalled or deleted (or cannot carry a thread).
func chatMessageGone(err error) bool {
	var platform *chatPlatformError
	if !errors.As(err, &platform) {
		return false
	}
	switch platform.Code {
	case 230011, 230110, 230054:
		return true
	}
	return platform.Status == http.StatusNotFound
}

func (s *server) chatPostMessage(ctx context.Context, record domain.ChatIntegrationRecord, path string, body map[string]any) (chatThreadMessageResult, error) {
	var result chatThreadMessageResult
	if err := s.chatWorkflowRequest(ctx, record, http.MethodPost, path, body, &result); err != nil {
		return result, err
	}
	if result.Data.MessageID == "" {
		return result, errorsChatResponse()
	}
	return result, nil
}

func chatReplyPath(messageID string) string {
	return "/open-apis/im/v1/messages/" + url.PathEscape(messageID) + "/reply"
}

// chatSendInIssueThread posts a message about an issue to the binding's group,
// in the issue's thread: as a reply to the thread's root when there is one; to
// the member's source message when the issue answers it; or as a new message
// that starts the thread. A root that is gone starts a new thread. message is
// the platform body without its addressing (msg_type, content).
func (s *server) chatSendInIssueThread(ctx context.Context, record domain.ChatIntegrationRecord, binding domain.ChatBinding, issueID, key, sourceMessage string, message map[string]any, syncFrom time.Time) (string, error) {
	thread, err := s.store.ChatThread(ctx, record.WorkspaceKey, record.ConnectionID, binding.ChatID, domain.ChatThreadResourceIssue, issueID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if err == nil {
		body := chatMergeBody(message, map[string]any{"reply_in_thread": true, "uuid": chatKey(key, "thread", thread.RootMessageID)[:32]})
		result, sendErr := s.chatPostMessage(ctx, record, chatReplyPath(thread.RootMessageID), body)
		if sendErr == nil {
			return result.Data.MessageID, nil
		}
		if !chatMessageGone(sendErr) {
			return "", sendErr
		}
		// The root was recalled or deleted: the next message starts a new
		// thread, and items of the old one stay with it.
		if err := s.store.DeleteChatThread(ctx, record.WorkspaceKey, record.ConnectionID, thread.ID); err != nil {
			return "", err
		}
		sourceMessage = ""
		key = chatKey(key, "restart", thread.RootMessageID)
	}
	var result chatThreadMessageResult
	root := ""
	if sourceMessage != "" {
		body := chatMergeBody(message, map[string]any{"reply_in_thread": true, "uuid": chatKey(key, "send")[:32]})
		result, err = s.chatPostMessage(ctx, record, chatReplyPath(sourceMessage), body)
		if err != nil && !chatMessageGone(err) {
			return "", err
		}
		if err == nil {
			root = firstNonEmpty(result.Data.RootID, sourceMessage)
		}
	}
	if root == "" {
		body := chatMergeBody(message, map[string]any{"receive_id": binding.ChatID, "uuid": chatKey(key, "send")[:32]})
		if sourceMessage != "" {
			body["uuid"] = chatKey(key, "send", "root")[:32]
		}
		result, err = s.chatPostMessage(ctx, record, "/open-apis/im/v1/messages?receive_id_type=chat_id", body)
		if err != nil {
			return "", err
		}
		root = result.Data.MessageID
	}
	if syncFrom.IsZero() {
		syncFrom = time.Now().UTC()
	}
	_, err = s.store.PutChatThread(ctx, record.WorkspaceKey, domain.ChatThread{ConnectionID: record.ConnectionID, ChatID: binding.ChatID, ResourceType: domain.ChatThreadResourceIssue, ResourceID: issueID, RootMessageID: root, PlatformThreadID: result.Data.ThreadID, BindingID: binding.ID, SyncFrom: syncFrom})
	return result.Data.MessageID, err
}

func chatMergeBody(base, extra map[string]any) map[string]any {
	result := make(map[string]any, len(base)+len(extra))
	for key, value := range base {
		result[key] = value
	}
	for key, value := range extra {
		result[key] = value
	}
	return result
}

// chatCommentCard is a Flow comment as a thread message: its author and text.
// Only the comment itself is shown; the card is updated in place on edits
// and deletions.
func chatCommentCard(comment domain.Comment, note string) map[string]any {
	author := firstNonEmpty(comment.User.DisplayName, comment.User.Name, "Flow")
	if comment.BotActor != nil && comment.BotActor.Name != "" {
		author = comment.BotActor.Name
	}
	heading := author + " commented"
	if comment.ParentID != nil {
		heading = author + " replied"
	}
	elements := []map[string]any{chatCardText(heading), chatCardText(chatTruncateText(strings.TrimSpace(comment.Body), 2000))}
	if note != "" {
		elements = append(elements, chatCardText(note))
	}
	return map[string]any{"config": map[string]bool{"wide_screen_mode": true, "update_multi": true}, "elements": elements}
}

func chatNoticeCard(text string) map[string]any {
	return map[string]any{"config": map[string]bool{"wide_screen_mode": true, "update_multi": true}, "elements": []map[string]any{chatCardText(text)}}
}

func chatInteractive(card map[string]any) map[string]any {
	content, _ := json.Marshal(card)
	return map[string]any{"msg_type": "interactive", "content": string(content)}
}

// chatSyncIssueComments mirrors the issue's Flow comments into its thread in
// the binding's group: new comments are posted, edits update the posted
// message, deletions are noted. A comment that came from this thread is not
// posted back. A new comment on an issue without a thread starts one with the
// issue's card.
func (s *server) chatSyncIssueComments(ctx context.Context, record domain.ChatIntegrationRecord, binding domain.ChatBinding, issue domain.Issue, job domain.ChatJob, payload chatNotificationJob) error {
	thread, err := s.store.ChatThread(ctx, record.WorkspaceKey, record.ConnectionID, binding.ChatID, domain.ChatThreadResourceIssue, issue.ID)
	if errors.Is(err, sql.ErrNoRows) {
		if payload.Event != "" && payload.Event != "comment.created" {
			return nil
		}
		syncFrom := payload.EventAt.Add(-5 * time.Second)
		if payload.EventAt.IsZero() {
			syncFrom = time.Now().UTC().Add(-time.Minute)
		}
		if err := s.chatSendIssueCardFrom(ctx, record, binding, issue, job.DedupKey, "", "comment.created", syncFrom); err != nil {
			return err
		}
		thread, err = s.store.ChatThread(ctx, record.WorkspaceKey, record.ConnectionID, binding.ChatID, domain.ChatThreadResourceIssue, issue.ID)
	}
	if err != nil {
		return err
	}
	comments, _, err := s.store.ResourceCommentsPage(ctx, record.WorkspaceKey, issue.ID, "", 100)
	if err != nil {
		return err
	}
	items, err := s.store.ChatThreadItems(ctx, record.WorkspaceKey, record.ConnectionID, thread.ID)
	if err != nil {
		return err
	}
	mirrored := map[string]domain.ChatThreadItem{}
	for _, item := range items {
		if item.ItemType == chatThreadItemComment {
			mirrored[item.ItemID] = item
		}
	}
	slices.SortStableFunc(comments, func(a, b domain.Comment) int { return a.CreatedAt.Compare(b.CreatedAt) })
	sent, present := 0, map[string]bool{}
	for _, comment := range comments {
		present[comment.ID] = true
		item, known := mirrored[comment.ID]
		if !known && comment.CreatedAt.Before(thread.SyncFrom) || strings.TrimSpace(comment.Body) == "" {
			continue
		}
		if sent >= chatThreadSyncLimit {
			break
		}
		switch {
		case !known:
			messageID, err := s.chatReplyToThread(ctx, record, &thread, binding, issue, chatInteractive(chatCommentCard(comment, "")), chatKey(thread.ID, "comment", comment.ID))
			if err != nil {
				return err
			}
			sent++
			if _, err := s.store.InsertChatThreadItem(ctx, record.WorkspaceKey, record.ConnectionID, domain.ChatThreadItem{ThreadID: thread.ID, ItemType: chatThreadItemComment, ItemID: comment.ID, MessageID: messageID, Origin: domain.ChatThreadOriginFlow, Version: comment.Version, CreatedAt: comment.CreatedAt}); err != nil {
				return err
			}
		case !item.Deleted && item.Version != comment.Version:
			card := chatCommentCard(comment, "Edited in Flow")
			if item.Origin == domain.ChatThreadOriginFlow && s.chatUpdateCard(ctx, record, item.MessageID, card) == nil {
				item.Version = comment.Version
			} else {
				// A member's own message (or one past its update window)
				// cannot be edited by the bot: the edit is posted as a
				// new message, which later edits update.
				messageID, err := s.chatReplyToThread(ctx, record, &thread, binding, issue, chatInteractive(card), chatKey(thread.ID, "comment", comment.ID, fmt.Sprint(comment.Version)))
				if err != nil {
					return err
				}
				sent++
				item.MessageID, item.Origin, item.Version = messageID, domain.ChatThreadOriginFlow, comment.Version
			}
			if err := s.store.UpdateChatThreadItem(ctx, record.WorkspaceKey, record.ConnectionID, item); err != nil {
				return err
			}
		}
	}
	for _, item := range items {
		if item.ItemType != chatThreadItemComment || item.Deleted || present[item.ItemID] || sent >= chatThreadSyncLimit {
			continue
		}
		// Not on the latest page: an older comment, or a deleted one.
		if _, err := s.store.ResourceComment(ctx, record.WorkspaceKey, issue.ID, item.ItemID); !errors.Is(err, sql.ErrNoRows) {
			if err != nil {
				return err
			}
			continue
		}
		note := "This comment was deleted in Flow."
		if item.Origin != domain.ChatThreadOriginFlow || s.chatUpdateCard(ctx, record, item.MessageID, chatNoticeCard(note)) != nil {
			if _, err := s.chatReplyToThread(ctx, record, &thread, binding, issue, chatInteractive(chatNoticeCard("A comment in this thread was deleted in Flow.")), chatKey(thread.ID, "deleted", item.ItemID)); err != nil {
				return err
			}
			sent++
		}
		item.Deleted = true
		if err := s.store.UpdateChatThreadItem(ctx, record.WorkspaceKey, record.ConnectionID, item); err != nil {
			return err
		}
	}
	return nil
}

// chatReplyToThread replies in the thread; when its root is gone, a new
// thread starts with the issue's card and the reply goes there.
func (s *server) chatReplyToThread(ctx context.Context, record domain.ChatIntegrationRecord, thread *domain.ChatThread, binding domain.ChatBinding, issue domain.Issue, message map[string]any, key string) (string, error) {
	body := chatMergeBody(message, map[string]any{"reply_in_thread": true, "uuid": chatKey(key, thread.RootMessageID)[:32]})
	result, err := s.chatPostMessage(ctx, record, chatReplyPath(thread.RootMessageID), body)
	if err == nil {
		return result.Data.MessageID, nil
	}
	if !chatMessageGone(err) {
		return "", err
	}
	if err := s.store.DeleteChatThread(ctx, record.WorkspaceKey, record.ConnectionID, thread.ID); err != nil {
		return "", err
	}
	if err := s.chatSendIssueCardFrom(ctx, record, binding, issue, chatKey(key, "restart", thread.RootMessageID), "", "comment.created", time.Now().UTC()); err != nil {
		return "", err
	}
	next, err := s.store.ChatThread(ctx, record.WorkspaceKey, record.ConnectionID, binding.ChatID, domain.ChatThreadResourceIssue, issue.ID)
	if err != nil {
		return "", err
	}
	*thread = next
	body["uuid"] = chatKey(key, next.RootMessageID)[:32]
	result, err = s.chatPostMessage(ctx, record, chatReplyPath(next.RootMessageID), body)
	return result.Data.MessageID, err
}

func (s *server) chatUpdateCard(ctx context.Context, record domain.ChatIntegrationRecord, messageID string, card map[string]any) error {
	content, _ := json.Marshal(card)
	return s.chatWorkflowRequest(ctx, record, http.MethodPatch, "/open-apis/im/v1/messages/"+url.PathEscape(messageID), map[string]string{"content": string(content)}, nil)
}

// chatThreadReplyCommand recognizes a member's reply in an issue thread. A
// create command addressed to the bot keeps its own meaning.
func (s *server) chatThreadReplyCommand(ctx context.Context, record domain.ChatIntegrationRecord, envelope chatWebhookEnvelope) (chatInboundCommand, string, bool) {
	event := envelope.Event
	message := event.Message
	if event.Sender.SenderType != "user" || event.Sender.TenantKey != record.TenantKey || event.Sender.SenderID.OpenID == "" || event.Sender.SenderID.OpenID == record.BotOpenID || message.MessageID == "" || message.ChatID == "" || message.ChatType != "group" || message.RootID == "" || message.RootID == message.MessageID {
		return chatInboundCommand{}, "", false
	}
	text, mentionedBot, ok := chatThreadReplyText(record, envelope)
	if !ok || strings.TrimSpace(text) == "" {
		return chatInboundCommand{}, "", false
	}
	if _, create := chatCreateTitle(text); create && mentionedBot {
		return chatInboundCommand{}, "", false
	}
	// So does a request ("@Flow 提需求 …", chat_requests.go).
	if _, request := chatRequestText(text); request && mentionedBot {
		return chatInboundCommand{}, "", false
	}
	thread, err := s.store.ChatThreadForRoot(ctx, record.WorkspaceKey, record.ConnectionID, message.RootID)
	if err != nil || thread.ChatID != message.ChatID || thread.ResourceType != domain.ChatThreadResourceIssue {
		return chatInboundCommand{}, "", false
	}
	return chatInboundCommand{Kind: chatThreadReplyKind, OpenID: event.Sender.SenderID.OpenID, ChatID: message.ChatID, MessageID: message.MessageID, RootMessageID: message.RootID, Text: text}, chatKey(record.ConnectionID, "message", message.MessageID), true
}

// chatThreadReplyText reads a text or rich text reply, with mentions written
// as names and the bot's own mention removed.
func chatThreadReplyText(record domain.ChatIntegrationRecord, envelope chatWebhookEnvelope) (string, bool, bool) {
	message := envelope.Event.Message
	var text string
	switch message.MessageType {
	case "text":
		var content struct {
			Text string `json:"text"`
		}
		if json.Unmarshal([]byte(message.Content), &content) != nil {
			return "", false, false
		}
		text = content.Text
	case "post":
		var content struct {
			Title   string `json:"title"`
			Content [][]struct {
				Tag      string `json:"tag"`
				Text     string `json:"text"`
				Href     string `json:"href"`
				UserID   string `json:"user_id"`
				UserName string `json:"user_name"`
			} `json:"content"`
		}
		if json.Unmarshal([]byte(message.Content), &content) != nil {
			return "", false, false
		}
		lines := []string{}
		if content.Title != "" {
			lines = append(lines, content.Title)
		}
		for _, paragraph := range content.Content {
			var line strings.Builder
			for _, element := range paragraph {
				switch element.Tag {
				case "text", "a":
					line.WriteString(element.Text)
				case "at":
					line.WriteString(firstNonEmpty(element.UserName, element.UserID))
				}
			}
			lines = append(lines, line.String())
		}
		text = strings.Join(lines, "\n")
	default:
		return "", false, false
	}
	mentionedBot := false
	for _, mention := range message.Mentions {
		if mention.Key == "" {
			continue
		}
		if record.BotOpenID != "" && mention.ID.OpenID == record.BotOpenID {
			mentionedBot = true
			text = strings.ReplaceAll(text, mention.Key, "")
			continue
		}
		name := mention.Name
		if name == "" {
			name = "someone"
		}
		text = strings.ReplaceAll(text, mention.Key, "@"+name)
	}
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) > chatThreadCommentLimit {
		text = string([]rune(text)[:chatThreadCommentLimit])
	}
	return text, mentionedBot, true
}

// processChatThreadReply turns a member's reply in an issue thread into a
// Flow comment written as their linked Flow account. The group's binding must
// allow updates; an unlinked member gets one link prompt per thread.
func (s *server) processChatThreadReply(ctx context.Context, record domain.ChatIntegrationRecord, job domain.ChatJob, command chatInboundCommand) error {
	thread, err := s.store.ChatThreadForRoot(ctx, record.WorkspaceKey, record.ConnectionID, command.RootMessageID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if thread.ChatID != command.ChatID || thread.ResourceType != domain.ChatThreadResourceIssue {
		return nil
	}
	// A message that already carries an item (a mirrored comment) is never
	// imported again.
	if _, err := s.store.ChatThreadItemForMessage(ctx, record.WorkspaceKey, record.ConnectionID, command.MessageID); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	binding, err := s.chatBindingByID(ctx, record, thread.BindingID)
	if err != nil {
		binding, err = s.chatBindingForChat(ctx, record, command.ChatID)
	}
	if err != nil {
		var permanent chatPermanentError
		if errors.As(err, &permanent) || errors.Is(err, store.ErrAuthForbidden) {
			return nil
		}
		return err
	}
	if !binding.AllowUpdate || binding.ChatID != command.ChatID {
		// Discussion in a thread of a group that does not update Flow
		// stays in the group.
		return nil
	}
	identity, err := s.store.ChatIdentityForOpenID(ctx, record.WorkspaceKey, record.ConnectionID, command.OpenID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if errors.Is(err, sql.ErrNoRows) || identity.TenantKey != record.TenantKey {
		return s.chatSendThreadLinkPrompt(ctx, record, thread, command)
	}
	ctx = withChatIdentityAuthentication(ctx, identity)
	var metadata domain.Bootstrap
	if err := s.chatAuthorizedMutation(ctx, record.WorkspaceKey, identity.UserID, http.MethodGet, "/api/issue-records/bootstrap", nil, &metadata); err != nil {
		return s.chatMutationFailure(ctx, record, job, command.OpenID, err, "Your Flow workspace access has changed. Sign in to Flow and check your access.")
	}
	var issue domain.Issue
	if err := s.chatAuthorizedMutation(ctx, record.WorkspaceKey, identity.UserID, http.MethodGet, "/api/issue-records/"+url.PathEscape(thread.ResourceID), nil, &issue); err != nil {
		return s.chatMutationFailure(ctx, record, job, command.OpenID, err, "You no longer have access to this issue.")
	}
	if !chatIssueMatchesBinding(issue, binding) {
		return s.chatSendCommandError(ctx, record, job, command.OpenID, "This issue is no longer available in this group scope.")
	}
	if issuePermissionRank(issueRole(s, metadata, issue)) < issuePermissionRank("commenter") {
		return s.chatSendCommandError(ctx, record, job, command.OpenID, "Your Flow account does not have permission to comment on this issue.")
	}
	commentID := "comment_chat_" + chatKey(record.ConnectionID, "thread", command.MessageID)
	// Recorded before the comment exists, so the comment's own event never
	// posts it back to this thread.
	item := domain.ChatThreadItem{ThreadID: thread.ID, ItemType: chatThreadItemComment, ItemID: commentID, MessageID: command.MessageID, Origin: domain.ChatThreadOriginChat, Version: 1}
	if _, err := s.store.InsertChatThreadItem(ctx, record.WorkspaceKey, record.ConnectionID, item); err != nil {
		return err
	}
	var comment domain.Comment
	err = s.chatAuthorizedMutation(store.WithCommentCreationKey(ctx, commentID), record.WorkspaceKey, identity.UserID, http.MethodPost, "/api/issue-records/"+url.PathEscape(issue.ID)+"/comments", domain.CommentCreateInput{Body: command.Text}, &comment)
	if err != nil {
		var failure *chatMutationError
		if errors.As(err, &failure) && failure.Status >= 400 && failure.Status < 500 {
			_ = s.store.DeleteChatThreadItem(ctx, record.WorkspaceKey, record.ConnectionID, store.ChatThreadItemID(thread.ID, chatThreadItemComment, commentID))
		}
		return s.chatMutationFailure(ctx, record, job, command.OpenID, err, "Flow could not add your reply as a comment. Open Flow to check your permissions.")
	}
	return nil
}

// chatSendThreadLinkPrompt answers an unlinked member's reply in the thread
// with a card to connect their Flow account, once per member and thread.
func (s *server) chatSendThreadLinkPrompt(ctx context.Context, record domain.ChatIntegrationRecord, thread domain.ChatThread, command chatInboundCommand) error {
	items, err := s.store.ChatThreadItems(ctx, record.WorkspaceKey, record.ConnectionID, thread.ID)
	if err != nil {
		return err
	}
	if slices.ContainsFunc(items, func(item domain.ChatThreadItem) bool {
		return item.ItemType == chatThreadItemLinkCard && item.ItemID == command.OpenID
	}) {
		return nil
	}
	link := fmt.Sprintf("%s/%s/settings/account/connections", s.chatAppOrigin(), url.PathEscape(record.WorkspaceKey))
	card := chatCard("Connect your Flow account", chatCardText("Replies in this thread become Flow comments once you connect your Flow account."), map[string]any{"tag": "action", "actions": []map[string]any{{"tag": "button", "text": chatPlain("Connect account"), "url": link, "type": "primary"}}})
	body := chatMergeBody(chatInteractive(card), map[string]any{"reply_in_thread": true, "uuid": chatKey(thread.ID, "link", command.OpenID)[:32]})
	result, err := s.chatPostMessage(ctx, record, chatReplyPath(command.MessageID), body)
	if err != nil {
		if chatMessageGone(err) {
			return nil
		}
		return err
	}
	_, err = s.store.InsertChatThreadItem(ctx, record.WorkspaceKey, record.ConnectionID, domain.ChatThreadItem{ThreadID: thread.ID, ItemType: chatThreadItemLinkCard, ItemID: command.OpenID, MessageID: result.Data.MessageID, Origin: domain.ChatThreadOriginFlow})
	return err
}
