package main

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP08AuthDisabledRealtimeRetainsViewer(t *testing.T) {
	for name, repository := range favoriteTabTestStores(t) {
		t.Run(name, func(t *testing.T) {
			data := repository.Bootstrap()
			viewer := data.Viewer
			now := time.Now().UTC()
			notificationID := fmt.Sprintf("scale-p08-notification-%d", now.UnixNano())
			if err := repository.MutateWorkspace(t.Context(), data.Workspace.URLKey, "scale_p08.fixture", notificationID, nil, func(next *domain.Bootstrap) error {
				next.Notifications = append(next.Notifications, domain.Notification{ID: notificationID, RecipientID: viewer.ID, Type: "issueReminder", Actor: viewer, CreatedAt: now, UpdatedAt: now})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			s := &server{store: repository, authDisabled: true, uploadPath: t.TempDir()}
			newHandler(s)
			request := httptest.NewRequest("GET", "/api/realtime/events?workspace="+data.Workspace.URLKey, nil)
			request = request.WithContext(context.WithValue(request.Context(), authUserContextKey{}, viewer))
			metadata, _, err := s.requestIssueQueryAccess(request)
			if err != nil || metadata.Viewer.ID != viewer.ID {
				t.Fatalf("realtime viewer=%q, want authenticated viewer %q: %v", metadata.Viewer.ID, viewer.ID, err)
			}
			for _, event := range []domain.RealtimeEvent{
				{ID: "scale-p08-notification", Type: "notification.unsnoozed", AggregateID: notificationID, ActorID: viewer.ID},
				{ID: "scale-p08-agent", Type: "agent.message_created", AggregateID: "scale-p08-session", ActorID: viewer.ID},
			} {
				if _, visible, err := s.pagedRealtimeEvent(request, event); err != nil || !visible {
					t.Errorf("own %s event visible=%v: %v", event.Type, visible, err)
				}
			}
		})
	}
}
