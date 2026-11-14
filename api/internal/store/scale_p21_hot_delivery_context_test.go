package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21HotDeliveryContextSourceAndCountBound(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	now := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	past, future := now.Add(-time.Minute), now.Add(time.Hour)
	issueID := idgen.New("issue_")
	held := 0
	for _, count := range []int{100, 1000} {
		if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.hot_delivery_seed", "", nil, func(data *domain.Bootstrap) error {
			data.Users = []domain.User{f.data.Viewer}
			data.Users[0].Email = "installed-recipient@example.test"
			data.WorkspaceSettings.HIPAACompliance = true
			data.Reviews = []domain.CodeReview{{ID: "delivery-review", SlugID: "delivery-review-slug", Title: "Installed review\x00title", Description: strings.Repeat("review body ", 100000)}}
			data.Projects = nil
			for index := range count {
				id := fmt.Sprintf("delivery-unrelated-%04d", index)
				data.Users = append(data.Users, domain.User{ID: id, Email: id + "@example.test", AvatarURL: strings.Repeat("unused", 100)})
				data.Reviews = append(data.Reviews, domain.CodeReview{ID: id, Title: "Unused", Description: strings.Repeat("unused", 100)})
				data.Projects = append(data.Projects, domain.Project{ID: id, Description: strings.Repeat("unused", 500)})
			}
			data.Issues = []domain.Issue{{ID: issueID, Identifier: "DEL-1", Team: data.Teams[0], State: data.States[0], Creator: data.Viewer, Title: "Native issue title", Description: strings.Repeat("issue body ", 100000), CreatedAt: now, UpdatedAt: now}}
			actor := domain.User{DisplayName: "Flow actor", AvatarURL: "https://example.test/avatar.png", BuiltinAgent: true}
			data.Notifications = []domain.Notification{
				{ID: "delivery-notification-title", RecipientID: data.Viewer.ID, Type: "pulse", Title: "Self title", Text: "Self text", Actor: actor, CreatedAt: now, UpdatedAt: now},
				{ID: "delivery-notification-review", RecipientID: data.Viewer.ID, ReviewID: "delivery-review-slug", Actor: actor, CreatedAt: now, UpdatedAt: now},
				{ID: "delivery-notification-issue", RecipientID: data.Viewer.ID, Type: "mention", IssueID: issueID, Actor: actor, CreatedAt: now, UpdatedAt: now},
			}
			data.NotificationDeliveries = nil
			for index, notification := range data.Notifications {
				data.NotificationDeliveries = append(data.NotificationDeliveries, domain.NotificationDelivery{ID: fmt.Sprintf("delivery-context-%d", index), NotificationID: notification.ID, RecipientID: data.Viewer.ID, Channel: "email", Status: "pending", CreatedAt: now, UpdatedAt: now})
			}
			data.NotificationDeliveries = append(data.NotificationDeliveries,
				domain.NotificationDelivery{ID: "delivery-context-missing", NotificationID: "missing", RecipientID: data.Viewer.ID, Channel: "email", Status: "pending", CreatedAt: now, UpdatedAt: now},
				domain.NotificationDelivery{ID: "delivery-context-recipient", NotificationID: data.Notifications[0].ID, RecipientID: "missing", Channel: "email", Status: "pending", CreatedAt: now, UpdatedAt: now},
			)
			payload, _ := json.Marshal(strings.Repeat("unused payload ", 50000))
			data.IntegrationDeliveries = []domain.IntegrationDelivery{
				{ID: "z-pending", Status: "pending", Payload: payload},
				{ID: "a-due", Status: "failed", NextAttemptAt: &past},
				{ID: "future", Status: "failed", NextAttemptAt: &future},
				{ID: "limited", Status: "pending", Attempts: 8},
				{ID: "no-backoff", Status: "failed"},
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		want, err := f.NotificationDeliverySnapshot(t.Context(), workspace, []string{"pending"}, now)
		if err != nil {
			t.Fatal(err)
		}
		read, err := view.BeginNotificationDeliveryContextRead(t.Context(), workspace, []string{"pending"}, now)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(scaleP21DeliveryConsumed(read.Data), scaleP21DeliveryConsumed(want)) {
			t.Fatalf("rendering inputs differ: got=%#v want=%#v", scaleP21DeliveryConsumed(read.Data), scaleP21DeliveryConsumed(want))
		}
		if count == 100 {
			held = view.metadataResidency.work.used
		} else if view.metadataResidency.work.used != held {
			t.Fatalf("100->1000 unrelated records grew work: %d->%d", held, view.metadataResidency.work.used)
		}
		t.Logf("unrelated=%d retained_delivery_context_bytes=%d", count, view.metadataResidency.work.used)
		read.Close()
		metadata, _ := f.WorkspaceMetadataFields(workspace, "integrationDeliveries")
		var expected []string
		for _, item := range metadata.IntegrationDeliveries {
			if item.Attempts < 8 && (item.Status == "pending" || item.Status == "failed" && item.NextAttemptAt != nil && !item.NextAttemptAt.After(now)) {
				expected = append(expected, item.ID)
			}
		}
		var selected []string
		if err := view.DueIntegrationDeliveryIDs(t.Context(), workspace, now, func(id string) error {
			selected = append(selected, id)
			return nil
		}); err != nil || !reflect.DeepEqual(selected, expected) || !reflect.DeepEqual(selected, []string{"z-pending", "a-due"}) {
			t.Fatalf("installed integration order=%v error=%v", selected, err)
		}
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		if read, err := view.BeginNotificationDeliveryContextRead(cancelled, workspace, []string{"pending"}, now); read != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled context=%v error=%v", read, err)
		}
		if view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 {
			t.Fatal("delivery context decoded full metadata or leaked work")
		}
		if count == 1000 {
			installed, _ := f.WorkspaceMetadata(workspace)
			legacy := &SQLiteStore{db: f.db, dialect: f.dialect, maxStateBytes: f.maxStateBytes, lastWorkspaceKey: workspace, workspaces: map[string]domain.Bootstrap{workspace: installed}}
			if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "users", "reviews", "workspaceSettings"), workspace, "scale_p21.hot_delivery_peer", "", nil, func(data *domain.Bootstrap) error {
				data.Users[0].Email = "canonical-recipient@example.test"
				data.Reviews[0].Title = "Canonical review"
				data.WorkspaceSettings.HIPAACompliance = false
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			want, err := legacy.NotificationDeliverySnapshot(t.Context(), workspace, []string{"pending"}, now)
			if err != nil {
				t.Fatal(err)
			}
			read, err := view.BeginNotificationDeliveryContextRead(t.Context(), workspace, []string{"pending"}, now)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(scaleP21DeliveryConsumed(read.Data), scaleP21DeliveryConsumed(want)) {
				t.Fatal("rendering silently switched the installed M to canonical rows")
			}
			read.Close()
		}
		view.releaseMetadataLogicalSources()
	}
}

func scaleP21DeliveryConsumed(data domain.Bootstrap) []any {
	result := []any{data.WorkspaceSettings.HIPAACompliance, data.NotificationDeliveries}
	for _, delivery := range data.NotificationDeliveries {
		row := []any{delivery.ID, "", ""}
		for _, user := range data.Users {
			if user.ID == delivery.RecipientID {
				row[1] = user.Email
				break
			}
		}
		for _, notification := range data.Notifications {
			if notification.ID != delivery.NotificationID {
				continue
			}
			actor := notification.Actor
			row[2] = []any{notification.ID, notification.Type, notification.IssueID, notification.ReviewID, notification.Title, notification.Text, actor.ID, actor.Name, actor.DisplayName, actor.Email, actor.AvatarURL, actor.App, actor.BuiltinAgent}
			for _, issue := range data.Issues {
				if notification.IssueID != "" && issue.ID == notification.IssueID {
					row = append(row, []any{issue.ID, issue.Identifier, issue.Title})
					break
				}
			}
			for _, review := range data.Reviews {
				if notification.ReviewID != "" && (review.ID == notification.ReviewID || review.SlugID == notification.ReviewID) {
					row = append(row, []any{review.ID, review.SlugID, review.Title})
					break
				}
			}
			break
		}
		result = append(result, row)
	}
	return result
}

func TestScaleP21HotDeliveryMutationAdmissionAndRollback(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace, now := f.data.Workspace.URLKey, time.Now().UTC()
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.hot_delivery_writer", "", nil, func(data *domain.Bootstrap) error {
		data.NotificationDeliveries = []domain.NotificationDelivery{{ID: "delivery-native-write", RecipientID: data.Viewer.ID, Channel: "email", Status: "pending", CreatedAt: now, UpdatedAt: now}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	stored := func() string {
		t.Helper()
		var raw []byte
		if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_content_records WHERE workspace_key=? AND kind='delivery' AND id='delivery-native-write'`, workspace).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	before, rejected := stored(), errors.New("reject delivery")
	if err := view.MutateNotificationDelivery(t.Context(), workspace, "delivery-native-write", func(item *domain.NotificationDelivery) error { item.Status = "delivered"; return rejected }); !errors.Is(err, rejected) || stored() != before {
		t.Fatal("delivery callback error did not roll back", err)
	}
	if err := view.MutateNotificationDelivery(t.Context(), workspace, "delivery-native-write", func(item *domain.NotificationDelivery) error {
		item.Error = strings.Repeat("large ", 3<<20)
		return nil
	}); !errors.Is(err, ErrMetadataWorkBudget) || stored() != before {
		t.Fatal("growing callback escaped encoding admission", err)
	}
	for _, key := range []string{workspace, workspace + "-foreign"} {
		id := "delivery-native-write"
		if key == workspace {
			id = "missing"
		}
		if err := view.MutateNotificationDelivery(t.Context(), key, id, func(*domain.NotificationDelivery) error { t.Fatal("missing/foreign callback ran"); return nil }); !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("missing delivery changed semantics", err)
		}
	}
	if err := view.MutateNotificationDelivery(t.Context(), workspace, "delivery-native-write", func(item *domain.NotificationDelivery) error {
		item.Status, item.Attempts, item.DeliveredAt, item.UpdatedAt = "delivered", item.Attempts+1, &now, now
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var updated domain.NotificationDelivery
	if err := json.Unmarshal([]byte(stored()), &updated); err != nil || updated.Status != "delivered" || updated.Attempts != 1 || updated.DeliveredAt == nil {
		t.Fatal("delivery update did not preserve native writer", err, updated)
	}
	if view.metadataResidency.work.used != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("native delivery mutation leaked work or decoded full metadata")
	}
}
