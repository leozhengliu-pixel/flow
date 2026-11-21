package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// errDocumentDeliveryNoAccess is the final reason of a delivery that was
// dropped because its recipient can no longer open the document.
const errDocumentDeliveryNoAccess = "recipient cannot open the document"

func documentNotification(notification domain.Notification) bool {
	return notification.SourceType == "document" && notification.SourceID != ""
}

// documentNotificationDeliverable is the delivery-time recheck of a document
// notification: the recipient must still be an active member who can open the
// document as the document API projects it for them (private teams, grants,
// admin-closed teams). Membership is read fresh, never from the cached member
// directory. A deleted document is still announced to the recipients who got
// the notice. Notifications of other sources are not judged here.
func (s *server) documentNotificationDeliverable(ctx context.Context, key string, notification domain.Notification) (bool, error) {
	if !documentNotification(notification) || s.authDisabled {
		return true, nil
	}
	// The caller often holds a delivery read whose snapshot belongs to no
	// recipient; judge the recipient from their own fresh read.
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// The recipient's own socket-style read: the bounded header read when
	// bounded metadata is on, the recipient's paged projection otherwise.
	request := (&http.Request{Method: http.MethodGet, URL: &url.URL{Path: "/"}}).WithContext(context.WithValue(ctx, authUserContextKey{}, domain.User{ID: notification.RecipientID}))
	role, _, err := s.socketDocumentRole(request, key, notification.SourceID)
	switch {
	case errors.Is(err, store.ErrAuthForbidden):
		return false, nil
	case errors.Is(err, errNotFound):
		// Gone, or outside the recipient's reach. A deletion is still announced
		// to the people who got the notice.
		return notification.Type == notificationDocumentDeleted, nil
	case err != nil:
		return false, err
	}
	return role != "none", nil
}

// dropDocumentDelivery ends a delivery for good: no retry, no later digest.
func (s *server) dropDocumentDelivery(ctx context.Context, key, id string) {
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = s.store.MutateNotificationDelivery(persistCtx, key, id, func(item *domain.NotificationDelivery) error {
		if item.Status != "delivering" {
			return nil
		}
		item.Attempts++
		item.Status, item.Error, item.NextAttemptAt, item.UpdatedAt = "failed", errDocumentDeliveryNoAccess, nil, time.Now().UTC()
		return nil
	})
}

func documentDeliveryError(err error) error {
	return fmt.Errorf("document access check: %w", err)
}
