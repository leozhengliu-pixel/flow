package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Personal chat notifications. A member who linked their Lark or Feishu
// account and turned on personal notifications (NotificationPreferences.Chat,
// with the same per-type choices as email and desktop) receives them as a
// direct message from the app's bot. Each notification gets a "chat"
// delivery, which the delivery scheduler hands to the chat outbox (kind
// "personal"): the outbox keeps the platform's rate limit, retries and error
// codes. The message is sent only while the integration is healthy (tenant
// authorized), the account is still linked and its member still active, and
// the member can still open what it links to.

const chatPersonalJobKind = "personal"

// Stable codes a personal chat delivery can end with (delivery.error).
const (
	chatPersonalNotLinked        = "chat_not_linked"
	chatPersonalUnavailable      = "chat_app_unavailable_to_user"
	chatPersonalNoAccess         = "chat_recipient_no_access"
	chatPersonalSharingBlocked   = "chat_sharing_blocked"
	chatPersonalDeliveryFailure  = "chat_delivery_failed"
	chatPersonalStateUnavailable = "unavailable"
)

type chatPersonalJob struct {
	DeliveryID     string `json:"deliveryId"`
	NotificationID string `json:"notificationId"`
	UserID         string `json:"userId"`
	OpenID         string `json:"openId"`
}

// chatPersonalProviders lists the providers to try, the deployment's chat
// platform first.
func chatPersonalProviders() []string {
	if chatPlatform() == "lark" {
		return []string{"lark", "feishu"}
	}
	return []string{"feishu", "lark"}
}

// dispatchChatNotifications hands pending personal chat deliveries to the
// chat outbox.
func (s *server) dispatchChatNotifications(ctx context.Context, key string) {
	pending, err := s.store.NotificationDeliveries(ctx, key, []string{"pending"}, time.Now().UTC())
	if err != nil {
		return
	}
	for _, delivery := range pending.NotificationDeliveries {
		if delivery.Channel != "chat" || delivery.Status != "pending" {
			continue
		}
		claimed := false
		if err := s.store.MutateNotificationDelivery(ctx, key, delivery.ID, func(next *domain.NotificationDelivery) error {
			if next.Status != "pending" {
				return nil
			}
			next.Status, next.UpdatedAt = "delivering", time.Now().UTC()
			delivery, claimed = *next, true
			return nil
		}); err != nil || !claimed {
			continue
		}
		if code := s.queueChatPersonalNotification(ctx, key, delivery); code != "" {
			s.finishChatPersonalDelivery(ctx, key, delivery.ID, "failed", code)
		}
	}
}

// queueChatPersonalNotification enqueues the delivery for the recipient's
// linked account, or returns why it cannot be sent.
func (s *server) queueChatPersonalNotification(ctx context.Context, key string, delivery domain.NotificationDelivery) string {
	code := chatPersonalNotLinked
	for _, provider := range chatPersonalProviders() {
		record, err := s.store.ChatIntegration(ctx, key, provider)
		if err != nil {
			continue
		}
		identity, err := s.store.ChatIdentityForUser(ctx, record.WorkspaceKey, record.ConnectionID, delivery.RecipientID)
		if err != nil {
			continue
		}
		if !record.Connected {
			code = chatErrorCode(chatTenantNotApprovedMessage)
			continue
		}
		dedup := chatKey(record.ConnectionID, delivery.ID)
		raw, _ := json.Marshal(chatPersonalJob{DeliveryID: delivery.ID, NotificationID: delivery.NotificationID, UserID: delivery.RecipientID, OpenID: identity.OpenID})
		if _, err := s.store.EnqueueChatJob(ctx, domain.ChatJob{ID: "chat_job_" + dedup, WorkspaceKey: record.WorkspaceKey, ConnectionID: record.ConnectionID, Kind: chatPersonalJobKind, DedupKey: dedup, Payload: raw, RunAt: time.Now().UTC()}); err != nil {
			log.Printf("Chat personal notification workspace=%s: %v", key, err)
			code = chatPersonalDeliveryFailure
			continue
		}
		return ""
	}
	return code
}

func (s *server) finishChatPersonalDelivery(ctx context.Context, key, id, status, code string) {
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	err := s.store.MutateNotificationDelivery(persistCtx, key, id, func(item *domain.NotificationDelivery) error {
		if item.Status != "delivering" {
			return nil
		}
		now := time.Now().UTC()
		item.Attempts++
		item.Status, item.Error, item.UpdatedAt = status, code, now
		if status == "delivered" {
			item.DeliveredAt = &now
		}
		return nil
	})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		log.Printf("Chat personal notification workspace=%s delivery=%s: %v", key, id, err)
	}
}

// chatJobFinal reports whether a failed chat job is not retried.
func chatJobFinal(err error, job domain.ChatJob) bool {
	var permanent chatPermanentError
	var platform *chatPlatformError
	return errors.As(err, &permanent) || errors.Is(err, store.ErrAuthForbidden) || errors.As(err, &platform) && !platform.Retryable || job.Attempts >= 8
}

// chatUnavailableToUser: the platform refuses to message this account because
// the app is not available to it (Lark and Feishu code 230013, "Bot has NO
// availability to this user").
func chatUnavailableToUser(err error) bool {
	var platform *chatPlatformError
	return errors.As(err, &platform) && platform.Code == 230013
}

func (s *server) processChatPersonalNotification(ctx context.Context, record domain.ChatIntegrationRecord, job domain.ChatJob) error {
	var payload chatPersonalJob
	if json.Unmarshal(job.Payload, &payload) != nil || payload.DeliveryID == "" {
		return chatInvalid("invalid stored personal notification")
	}
	fail := func(code string) error {
		s.finishChatPersonalDelivery(ctx, record.WorkspaceKey, payload.DeliveryID, "failed", code)
		return nil
	}
	if !record.Connected {
		return fail(chatErrorCode(chatTenantNotApprovedMessage))
	}
	// Unlinking (or linking another account) stops delivery.
	identity, err := s.store.ChatIdentityForUser(ctx, record.WorkspaceKey, record.ConnectionID, payload.UserID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (identity.OpenID != payload.OpenID || identity.TenantKey != record.TenantKey) {
		return fail(chatPersonalNotLinked)
	}
	if err != nil {
		return err
	}
	item, code, err := s.chatPersonalNotificationItem(ctx, record, payload)
	if err != nil {
		return err
	}
	if code != "" {
		return fail(code)
	}
	card := chatCard(item.Title, chatCardText(item.Line), map[string]any{"tag": "action", "actions": []map[string]any{{"tag": "button", "text": chatPlain("Open in Flow"), "url": item.URL}}})
	content, _ := json.Marshal(card)
	var result chatMessageResult
	err = s.chatWorkflowRequest(ctx, record, http.MethodPost, "/open-apis/im/v1/messages?receive_id_type=open_id", map[string]any{"receive_id": identity.OpenID, "msg_type": "interactive", "content": string(content), "uuid": chatKey(job.DedupKey, "personal")[:32]}, &result)
	if chatUnavailableToUser(err) {
		// The member's own state, not the connection's: shown on their
		// notification settings row.
		s.setChatPersonalDeliveryState(ctx, record, identity, chatPersonalStateUnavailable)
		return fail(chatPersonalUnavailable)
	}
	if err != nil {
		if chatJobFinal(err, job) {
			s.finishChatPersonalDelivery(ctx, record.WorkspaceKey, payload.DeliveryID, "failed", chatPersonalDeliveryFailure)
		}
		return err
	}
	s.setChatPersonalDeliveryState(ctx, record, identity, "")
	s.finishChatPersonalDelivery(ctx, record.WorkspaceKey, payload.DeliveryID, "delivered", "")
	return nil
}

func (s *server) setChatPersonalDeliveryState(ctx context.Context, record domain.ChatIntegrationRecord, identity domain.ChatIdentity, state string) {
	if identity.PersonalDelivery == state {
		return
	}
	identity.PersonalDelivery = state
	if err := s.store.PutChatIdentity(ctx, record.WorkspaceKey, record.ConnectionID, identity); err != nil {
		log.Printf("Chat personal notification workspace=%s: %v", record.WorkspaceKey, err)
	}
}

// chatPersonalNotificationItem is the message for the notification, or the
// code of why it is not sent: the member is no longer active, can no longer
// open the issue, or the workspace keeps content away from chat applications.
func (s *server) chatPersonalNotificationItem(ctx context.Context, record domain.ChatIntegrationRecord, payload chatPersonalJob) (notificationEmailItem, string, error) {
	data, ok := s.store.WorkspaceMetadata(record.WorkspaceKey)
	if !ok {
		return notificationEmailItem{}, "", chatInvalid("workspace unavailable")
	}
	if data.WorkspaceSettings.HIPAACompliance {
		return notificationEmailItem{}, chatPersonalSharingBlocked, nil
	}
	// Leaving the workspace, or a deactivated account, stops delivery. The
	// access is the member's own issue access, as the issue API reads it.
	user, err := s.store.UserByID(ctx, payload.UserID)
	if err != nil || !user.Active {
		return notificationEmailItem{}, chatPersonalNoAccess, nil
	}
	_, access, err := s.store.IssueQueryAccess(ctx, record.WorkspaceKey, payload.UserID)
	if errors.Is(err, store.ErrAuthForbidden) {
		return notificationEmailItem{}, chatPersonalNoAccess, nil
	}
	if err != nil {
		return notificationEmailItem{}, "", err
	}
	notification, err := s.store.NotificationRecord(ctx, record.WorkspaceKey, payload.UserID, payload.NotificationID)
	if errors.Is(err, sql.ErrNoRows) {
		return notificationEmailItem{}, chatPersonalNoAccess, nil
	}
	if err != nil {
		return notificationEmailItem{}, "", err
	}
	if notification.IssueID != "" && notification.ReviewID == "" {
		issue, err := s.store.IssueRecord(ctx, record.WorkspaceKey, notification.IssueID)
		if errors.Is(err, sql.ErrNoRows) {
			return notificationEmailItem{}, chatPersonalNoAccess, nil
		}
		if err != nil {
			return notificationEmailItem{}, "", err
		}
		visible, err := s.store.VisibleIssueRecordIDs(ctx, store.IssueRecordQuery{Workspace: record.WorkspaceKey, Access: &access}, []string{issue.ID})
		if err != nil {
			return notificationEmailItem{}, "", err
		}
		if !visible[issue.ID] {
			return notificationEmailItem{}, chatPersonalNoAccess, nil
		}
		data.Issues = append(data.Issues, issue)
	}
	item, err := notificationSummary(&data, s.chatAppOrigin()+"/"+url.PathEscape(record.WorkspaceKey), notification)
	if err != nil {
		return item, chatPersonalNoAccess, nil
	}
	return item, "", nil
}
