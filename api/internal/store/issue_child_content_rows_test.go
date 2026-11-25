package store

import (
	"bytes"
	"context"
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

func TestScaleP21SW67IssueChildContentRows(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, issue := f.data.Workspace.URLKey, idgen.New("sw67-child-")
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	f.db.SetMaxOpenConns(1)
	at := time.Date(2026, 11, 14, 1, 2, 3, 0, time.UTC)
	large := strings.Repeat("Complete selected issue child body. ", 1<<15)
	begin := func(t *testing.T) *sqlTx {
		t.Helper()
		tx, err := f.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = tx.Rollback() })
		return tx
	}
	usable := func(t *testing.T, tx *sqlTx) {
		t.Helper()
		var one int
		if err := tx.QueryRowContext(t.Context(), `SELECT 1`).Scan(&one); err != nil || one != 1 {
			t.Fatal("child content loader consumed the caller transaction", err)
		}
	}
	put := func(t *testing.T, tx *sqlTx, ws, kind, resource, physical, parent string, created time.Time, value any) {
		t.Helper()
		raw, direct := value.([]byte)
		if !direct {
			var err error
			raw, err = json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
		}
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO workspace_content_records(workspace_key,kind,resource_id,id,parent_id,status,created_at,data) VALUES(?,?,?,?,?,'read',?,?)`,
			ws, kind, resource, physical, parent, created.UTC().Format(issueRecordTimestamp), raw); err != nil {
			t.Fatal(err)
		}
	}
	literal := func(ctx context.Context, tx *sqlTx, selected string) (domain.Bootstrap, error) {
		var data domain.Bootstrap
		var err error
		data.Comments, err = scopedContent[domain.Comment](ctx, tx, workspace, "comment", []string{selected})
		if err != nil {
			return data, err
		}
		data.Activities, err = scopedContent[domain.ActivityEvent](ctx, tx, workspace, "activity", []string{selected})
		if err != nil {
			return data, err
		}
		notifications, err := scopedContent[domain.Notification](ctx, tx, workspace, "notification", []string{selected})
		if err != nil {
			return data, err
		}
		data.Notifications = []domain.Notification{}
		for _, values := range notifications {
			data.Notifications = append(data.Notifications, values...)
		}
		data.NotificationDeliveries, err = issueNotificationDeliveries(ctx, tx, workspace, data.Notifications)
		return data, err
	}
	seed := func(t *testing.T, tx *sqlTx, selected, variant string, count int) {
		t.Helper()
		for index := count - 1; index >= 0; index-- {
			created := at.Add(time.Duration(index%2) * time.Second)
			suffix := fmt.Sprintf("%04d", index)
			notificationID := "notification-json-" + suffix
			for _, kind := range []string{"comment", "activity", "notification"} {
				if variant == "comments-only" && kind != "comment" ||
					variant == "activities-only" && kind != "activity" ||
					variant == "notifications-only" && kind != "notification" {
					continue
				}
				decoded := kind + "-json-" + fmt.Sprintf("%04d", count-index)
				if variant == "duplicate-json-ids" {
					decoded = kind + "-duplicate"
				}
				var value any
				switch kind {
				case "comment":
					value = domain.Comment{ID: decoded, Body: "Comment " + suffix, User: f.data.Viewer, CreatedAt: at.Add(time.Duration(count-index) * time.Hour),
						ParentID: new("unrelated-parent"), Reactions: map[string][]string{"like": {"actor", "actor"}},
						BodyData: map[string]any{"nested": []any{"complete", float64(index)}}}
					if variant == "large" {
						comment := value.(domain.Comment)
						comment.Body = large
						value = comment
					}
				case "activity":
					value = domain.ActivityEvent{ID: decoded, Type: "issue.updated", Actor: f.data.Viewer, CreatedAt: at,
						Metadata: domain.ActivityMetadata{"title": "Activity " + suffix, "extra": "Complete metadata"}}
					if variant == "large" {
						event := value.(domain.ActivityEvent)
						event.Metadata["extra"] = large
						value = event
					}
				case "notification":
					notificationID = decoded
					value = domain.Notification{ID: decoded, IssueID: "different-json-resource", RecipientID: "recipient", Actor: f.data.Viewer,
						Title: "Notification " + suffix, Text: "Full notification body", GroupKey: "group", OccurrenceCount: index + 1,
						LatestActorIDs: []string{"actor", "actor"}, ReadAt: &at, UpdatedAt: at.Add(-72 * time.Hour), CreatedAt: at.Add(-time.Hour)}
					if variant == "large" {
						notification := value.(domain.Notification)
						notification.Text = large
						value = notification
					}
				}
				if variant == "null" {
					value = []byte("null")
					if kind == "notification" {
						notificationID = ""
					}
				} else if variant == "malformed-"+kind && index == count-1 {
					value = []byte(`{"id":`)
				}
				put(t, tx, workspace, kind, selected, kind+"-physical-"+suffix, "", created, value)
			}
			if variant != "comments-only" && variant != "activities-only" && (index == 0 || index == count-1) {
				resource := ""
				if index == 0 {
					resource = "nonempty-delivery-resource"
				}
				var delivery any = domain.NotificationDelivery{ID: "delivery-json-" + suffix, NotificationID: "different-json-notification",
					RecipientID: "recipient", Channel: "chat", Status: "pending", Error: "Complete delivery", Attempts: index + 1, CreatedAt: at}
				if variant == "large" {
					value := delivery.(domain.NotificationDelivery)
					value.Error = large
					delivery = value
				} else if variant == "null" {
					delivery = []byte("null")
				} else if variant == "malformed-delivery" && index == count-1 {
					delivery = []byte(`{"id":`)
				}
				put(t, tx, workspace, "delivery", resource, "delivery-physical-"+suffix, notificationID, at, delivery)
			}
		}
		for _, kind := range []string{"comment", "activity", "notification"} {
			put(t, tx, workspace+"-foreign", kind, selected, "foreign", "", at, []byte(`{"id":`))
			put(t, tx, workspace, kind, selected+"-unrelated", "unrelated", "", at, []byte(`{"id":`))
		}
		put(t, tx, workspace, "delivery", "", "unrelated-delivery", "unselected-notification", at, []byte(`{"id":`))
		put(t, tx, workspace+"-foreign", "delivery", "", "foreign-delivery", "notification-json-0001", at, []byte(`{"id":`))
	}
	for _, test := range []struct {
		variant string
		count   int
		failed  bool
	}{
		{variant: "empty"}, {variant: "one", count: 1}, {variant: "ordered", count: 4},
		{variant: "comments-only", count: 1}, {variant: "activities-only", count: 1}, {variant: "notifications-only", count: 1},
		{variant: "empty-issue-key", count: 1}, {variant: "duplicate-json-ids", count: 3},
		{variant: "over50", count: 57}, {variant: "uncapped-history", count: 1003},
		{variant: "large", count: 1}, {variant: "null", count: 1},
		{variant: "malformed-comment", count: 2, failed: true}, {variant: "malformed-activity", count: 2, failed: true},
		{variant: "malformed-notification", count: 2, failed: true}, {variant: "malformed-delivery", count: 2, failed: true},
	} {
		t.Run(test.variant, func(t *testing.T) {
			tx := begin(t)
			selected := issue
			if test.variant == "empty-issue-key" {
				selected = ""
			}
			seed(t, tx, selected, test.variant, test.count)
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			writes := 0
			ctx = WithSQLStatementObserver(ctx, func(query string) {
				upper := strings.ToUpper(strings.TrimSpace(query))
				if strings.HasPrefix(upper, "INSERT ") || strings.HasPrefix(upper, "UPDATE ") || strings.HasPrefix(upper, "DELETE ") {
					writes++
				}
			})
			want, wantErr := literal(ctx, tx, selected)
			read := view.newIssueChildContentRows(tx, workspace)
			defer read.Close()
			got, gotErr := read.load(ctx, selected)
			if reflect.TypeOf(wantErr) != reflect.TypeOf(gotErr) || wantErr != nil && wantErr.Error() != gotErr.Error() {
				t.Fatalf("literal/bounded child content error differs: %T %v / %T %v", wantErr, wantErr, gotErr, gotErr)
			}
			if (wantErr != nil) != test.failed || writes != 0 {
				t.Fatal("child content error or no-write behavior differs", wantErr, writes)
			}
			// Callers return on error before using abandoned partial values.
			if wantErr == nil {
				if !reflect.DeepEqual(want, got) {
					t.Fatal("full child values, physical-key shapes, ordering or matching deliveries differ")
				}
				if got.Comments == nil || got.Activities == nil || got.Notifications == nil || got.NotificationDeliveries == nil {
					t.Fatal("empty child collection shapes changed")
				}
				expectedComments, expectedActivities, expectedNotifications := test.count, test.count, test.count
				switch test.variant {
				case "comments-only":
					expectedActivities, expectedNotifications = 0, 0
				case "activities-only":
					expectedComments, expectedNotifications = 0, 0
				case "notifications-only":
					expectedComments, expectedActivities = 0, 0
				}
				if len(got.Comments[selected]) != expectedComments || len(got.Activities[selected]) != expectedActivities || len(got.Notifications) != expectedNotifications {
					t.Fatal("full issue child history was capped or filtered")
				}
				if test.count > 0 && view.MetadataWorkUsedForTesting() == 0 {
					t.Fatal("returned child content has no live owner")
				}
			}
			read.Close()
			read.Close()
			if len(got.Comments) != 0 || len(got.Activities) != 0 || view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("child content close retained maps or work")
			}
			for _, values := range []any{got.Notifications, got.NotificationDeliveries} {
				slice := reflect.ValueOf(values)
				for index := 0; index < slice.Len(); index++ {
					if !slice.Index(index).IsZero() {
						t.Fatal("child content close retained a returned backing value")
					}
				}
			}
			usable(t, tx)
		})
	}
	t.Run("fresh-loads-cancel-and-replaced-outputs", func(t *testing.T) {
		tx := begin(t)
		seed(t, tx, issue, "one", 1)
		read := view.newIssueChildContentRows(tx, workspace)
		defer read.Close()
		first, err := read.load(t.Context(), issue)
		if err != nil {
			t.Fatal(err)
		}
		put(t, tx, workspace, "comment", issue, "new-comment", "", at.Add(time.Hour), domain.Comment{ID: "new-comment", Body: "Fresh same-tx comment"})
		second, err := read.load(t.Context(), issue)
		if err != nil || len(first.Comments[issue]) != 1 || len(second.Comments[issue]) != 2 {
			t.Fatal("child loader cached or changed an earlier owned result", err)
		}
		aliases := []any{first.Comments[issue], first.Activities[issue], first.Notifications, first.NotificationDeliveries,
			second.Comments[issue], second.Activities[issue], second.Notifications, second.NotificationDeliveries}
		before, _ := json.Marshal([]domain.Bootstrap{first, second})
		held := view.MetadataWorkUsedForTesting()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := read.load(ctx, issue); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled child content load succeeded", err)
		}
		after, _ := json.Marshal([]domain.Bootstrap{first, second})
		if !bytes.Equal(before, after) || view.MetadataWorkUsedForTesting() < held {
			t.Fatal("cancellation discarded still-owned child content")
		}
		budget, limit := view.metadataResidency.work, view.metadataResidency.work.limit
		budget.limit = view.MetadataWorkUsedForTesting() + 1
		_, err = read.load(t.Context(), issue)
		budget.limit = limit
		if !errors.Is(err, ErrMetadataWorkBudget) {
			t.Fatal("child content escaped exhausted admission", err)
		}
		after, _ = json.Marshal([]domain.Bootstrap{first, second})
		if !bytes.Equal(before, after) {
			t.Fatal("failed child admission changed prior outputs")
		}
		comments, activities := first.Comments, first.Activities
		delete(comments, issue)
		activities[issue] = nil
		first.Comments, first.Activities = map[string][]domain.Comment{"replacement": nil}, nil
		first.Notifications, first.NotificationDeliveries = []domain.Notification{{ID: "replacement"}}, nil
		read.Close()
		if len(comments) != 0 || len(activities) != 0 || len(second.Comments) != 0 || len(second.Activities) != 0 || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("close lost ownership after map/field replacement")
		}
		for _, values := range aliases {
			slice := reflect.ValueOf(values)
			for index := 0; index < slice.Len(); index++ {
				if !slice.Index(index).IsZero() {
					t.Fatal("close retained replaced child slice backing values")
				}
			}
		}
		if _, err := read.load(t.Context(), issue); !errors.Is(err, errMetadataLogicalClosed) {
			t.Fatal("closed child content owner accepted a new load", err)
		}
		usable(t, tx)
	})
}
