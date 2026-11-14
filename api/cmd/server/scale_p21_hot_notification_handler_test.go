package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type scaleP21NotificationWriter struct {
	*httptest.ResponseRecorder
	write func()
}

func (w *scaleP21NotificationWriter) Write(data []byte) (int, error) {
	if w.write != nil {
		w.write()
	}
	return w.ResponseRecorder.Write(data)
}

func TestScaleP21HotNotificationHandler(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	repo, err := store.OpenDatabase(scaleP03DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	fixture := apiKeyScopeFixture(t, repo)
	workspace, user := fixture.data.Workspace.URLKey, fixture.data.Viewer
	now := time.Now().UTC().Truncate(time.Millisecond)
	err = repo.MutateWorkspace(t.Context(), workspace, "scale_p21.notification_handler_seed", "", nil, func(data *domain.Bootstrap) error {
		for _, item := range []domain.Notification{
			{ID: "handler-visible", IssueID: "scope-issue"},
			{ID: "handler-hidden", IssueID: "scope-hidden-issue"},
			{ID: "handler-unreferenced"},
		} {
			item.RecipientID, item.CreatedAt, item.UpdatedAt = user.ID, now, now
			data.Notifications = append(data.Notifications, item)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	view, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	for _, restriction := range []string{"selected", "unrestricted"} {
		for _, teams := range [][]string{nil, {"scope-public"}, {"scope-private"}} {
			key := domain.APIKey{CreatorID: user.ID, Actor: "user", TeamRestriction: restriction, TeamIDs: teams, Scopes: []string{"read"}}
			ctx := context.WithValue(t.Context(), authUserContextKey{}, user)
			ctx = context.WithValue(ctx, credentialReadPrincipalKey{}, user.ID)
			ctx = context.WithValue(ctx, apiKeyContextKey{}, key)
			ctx = context.WithValue(ctx, workspaceKeyContextKey{}, workspace)
			request := httptest.NewRequest(http.MethodGet, "/api/notifications?limit=2", nil).WithContext(ctx)
			old := httptest.NewRecorder()
			fixture.service.listNotifications(old, request.Clone(ctx))
			writes := 0
			current := &scaleP21NotificationWriter{ResponseRecorder: httptest.NewRecorder(), write: func() {
				writes++
				if restriction == "selected" && view.MetadataWorkUsedForTesting() == 0 {
					t.Error("notification response lost its admission before encoding")
				}
			}}
			(&server{store: view}).listNotifications(current, request.Clone(ctx))
			if old.Code != http.StatusOK || current.Code != old.Code || !reflect.DeepEqual(current.Body.Bytes(), old.Body.Bytes()) {
				t.Fatalf("restriction=%s teams=%v old=%d %s new=%d %s", restriction, teams, old.Code, old.Body, current.Code, current.Body)
			}
			if writes == 0 || view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
				t.Fatalf("response writes=%d leaked=%d decodes=%d", writes, view.MetadataWorkUsedForTesting(), view.WorkspaceMetadataDecodes())
			}
		}
	}
}
