package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21SW66IssueUpdateContentRows(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, issue := f.data.Workspace.URLKey, idgen.New("sw66-issue-")
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	f.db.SetMaxOpenConns(1)
	at := time.Date(2026, 11, 14, 1, 2, 3, 400, time.UTC)
	large := strings.Repeat("Complete selected update content. ", 1<<15)
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
			t.Fatal("update content reader consumed its borrowed transaction", err)
		}
	}
	put := func(t *testing.T, tx *sqlTx, ws, kind, resource, physical, status string, created time.Time, value any) {
		t.Helper()
		raw, direct := value.([]byte)
		if !direct {
			var err error
			raw, err = json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
		}
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO workspace_content_records(workspace_key,kind,resource_id,id,status,created_at,data) VALUES(?,?,?,?,?,?,?)`,
			ws, kind, resource, physical, status, created.UTC().Format(issueRecordTimestamp), raw); err != nil {
			t.Fatal(err)
		}
	}
	literalNotifications := func(ctx context.Context, tx *sqlTx) ([]domain.Notification, error) {
		result := []domain.Notification{}
		rows, err := tx.QueryContext(ctx, `SELECT data FROM workspace_content_records WHERE workspace_key=? AND kind='notification' AND resource_id=? ORDER BY created_at DESC LIMIT 1000`, workspace, issue)
		if err != nil {
			return result, err
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			var notification domain.Notification
			if err := rows.Scan(&raw); err != nil {
				return result, err
			}
			if err := json.Unmarshal(raw, &notification); err != nil {
				return result, err
			}
			result = append(result, notification)
		}
		return result, rows.Err()
	}
	for _, test := range []struct {
		kind, variant string
		count         int
	}{
		{"notifications", "empty", 0}, {"notifications", "ordered-unfiltered", 3},
		{"notifications", "created-time-ties", 3}, {"notifications", "limit1000", 1000},
		{"notifications", "malformed-outside-limit", 1000}, {"notifications", "huge", 3},
		{"notifications", "null", 1}, {"notifications", "malformed", -1}, {"notifications", "typed-error", -1},
		{"heads", "empty", 0}, {"heads", "activity-only", 1}, {"heads", "comment-only", 1},
		{"heads", "latest-physical-ties", 2}, {"heads", "huge", 2}, {"heads", "null", 2},
		{"heads", "malformed-activity", -1}, {"heads", "malformed-comment", -1},
		{"heads", "older-malformed-ignored", 2},
	} {
		t.Run(test.kind+"/"+test.variant, func(t *testing.T) {
			tx := begin(t)
			if test.kind == "notifications" {
				// Loading notifications must not eagerly inspect timeline heads.
				put(t, tx, workspace, "activity", issue, "lazy-bad-head", "", at, []byte(`{"id":`))
				put(t, tx, workspace, "comment", issue, "lazy-bad-head", "", at, []byte(`{"id":`))
				count := 3
				if test.variant == "empty" {
					count = 0
				} else if test.count == 1000 {
					count = 1002
				} else if test.variant == "null" {
					count = 1
				}
				for index := range count {
					status := []string{"read", "archived", "deleted"}[index%3]
					notification := domain.Notification{ID: fmt.Sprintf("json-notification-%04d", index), Title: "Full title",
						Text: "Full body", Actor: f.data.Viewer, ReadAt: &at, ArchivedAt: &at, UpdatedAt: at.Add(-48 * time.Hour),
						CreatedAt: at.Add(time.Duration(count-index) * time.Hour), GroupKey: "aggregation", OccurrenceCount: index + 1,
						LatestActorIDs: []string{"first", "first", "second"}}
					var value any = notification
					created := at.Add(time.Duration(index) * time.Second)
					if test.variant == "created-time-ties" {
						created = at
					} else if test.variant == "huge" {
						notification.Text = large
						value = notification
					} else if test.variant == "null" {
						value = []byte("null")
					} else if test.variant == "malformed" && index == 2 || test.variant == "malformed-outside-limit" && index == 0 {
						value = []byte(`{"id":`)
					} else if test.variant == "typed-error" && index == 2 {
						value = []byte(`{"id":"bad","occurrenceCount":"bad"}`)
					}
					put(t, tx, workspace, "notification", issue, fmt.Sprintf("physical-%04d", index), status, created, value)
				}
				put(t, tx, workspace+"-foreign", "notification", issue, "foreign", "", at.Add(time.Hour), []byte(`{"id":`))
				put(t, tx, workspace, "notification", issue+"-untouched", "untouched", "", at.Add(time.Hour), []byte(`{"id":`))
			} else {
				for _, kind := range []string{"activity", "comment"} {
					put(t, tx, workspace+"-foreign", kind, issue, "foreign", "", at.Add(time.Hour), []byte(`{"id":`))
					put(t, tx, workspace, kind, issue+"-untouched", "untouched", "", at.Add(time.Hour), []byte(`{"id":`))
					if test.variant == "empty" || test.variant == "activity-only" && kind == "comment" || test.variant == "comment-only" && kind == "activity" {
						continue
					}
					for _, physical := range []string{"a", "z"} {
						var value any = domain.ActivityEvent{ID: "json-activity-" + physical, Type: "issue.updated", Actor: f.data.Viewer,
							CreatedAt: at.Add(-time.Hour), Metadata: domain.ActivityMetadata{"title": "Complete selected field", "nested": "<native>"}}
						if kind == "comment" {
							value = domain.Comment{ID: "json-comment-" + physical, Body: "Complete comment", User: f.data.Viewer,
								CreatedAt: at.Add(-2 * time.Hour), BodyData: map[string]any{"nested": []any{"complete", float64(7)}}, Reactions: map[string][]string{"like": {"actor"}}}
						}
						if test.variant == "huge" {
							if kind == "activity" {
								event := value.(domain.ActivityEvent)
								event.Metadata["title"] = large
								value = event
							} else {
								comment := value.(domain.Comment)
								comment.Body = large
								value = comment
							}
						} else if test.variant == "null" && physical == "z" {
							value = []byte("null")
						} else if test.variant == "malformed-activity" && kind == "activity" && physical == "z" ||
							test.variant == "malformed-comment" && kind == "comment" && physical == "z" ||
							test.variant == "older-malformed-ignored" && physical == "a" {
							value = []byte(`{"id":`)
						}
						put(t, tx, workspace, kind, issue, physical, "", at, value)
					}
				}
			}
			writes, timelineQueries := 0, 0
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			ctx = WithSQLStatementObserver(ctx, func(query string) {
				upper := strings.ToUpper(strings.TrimSpace(query))
				if strings.HasPrefix(upper, "INSERT ") || strings.HasPrefix(upper, "UPDATE ") || strings.HasPrefix(upper, "DELETE ") {
					writes++
				}
				if strings.Contains(query, "kind='activity'") || strings.Contains(query, "kind='comment'") {
					timelineQueries++
				}
			})
			read := view.newIssueUpdateContentRows(tx, workspace)
			defer read.Close()
			var want, got any
			var wantErr, gotErr error
			if test.kind == "notifications" {
				want, wantErr = literalNotifications(ctx, tx)
				got, gotErr = read.loadNotifications(ctx, issue)
				if timelineQueries != 0 {
					t.Fatal("notification load eagerly read lazy history heads")
				}
			} else {
				want, wantErr = loadIssueHistoryHead(ctx, tx, workspace, issue)
				got, gotErr = read.loadHistoryHead(ctx, issue)
			}
			if reflect.TypeOf(wantErr) != reflect.TypeOf(gotErr) || wantErr != nil && wantErr.Error() != gotErr.Error() {
				t.Fatalf("literal/bounded content error differs: %T %v / %T %v", wantErr, wantErr, gotErr, gotErr)
			}
			// UpdateIssueRecord returns on error before consuming partial values.
			if wantErr == nil && !reflect.DeepEqual(want, got) {
				t.Fatal("literal/bounded full values, physical ordering or nil shape differs")
			}
			if wantErr == nil && test.kind == "notifications" {
				for _, initial := range [][]domain.Notification{nil, {}, {{ID: "existing"}}} {
					expected := append(slices.Clone(initial), want.([]domain.Notification)...)
					actual := append(slices.Clone(initial), got.([]domain.Notification)...)
					if !reflect.DeepEqual(expected, actual) {
						t.Fatal("transport append changed the caller's nil/empty/existing notification shape")
					}
				}
			}
			if (wantErr != nil) != (test.count < 0) || writes != 0 {
				t.Fatal("fixture error or loader no-write behavior changed", wantErr, writes)
			}
			if test.count >= 0 {
				count := 0
				if test.kind == "notifications" {
					count = len(got.([]domain.Notification))
				} else {
					snapshot := got.(domain.ActivityHistorySnapshot)
					if snapshot.Activity != nil {
						count++
					}
					if snapshot.Comment != nil {
						count++
					}
				}
				if count != test.count || count > 0 && view.MetadataWorkUsedForTesting() == 0 {
					t.Fatal("selected content count or live ownership differs", count, test.count)
				}
			}
			read.Close()
			if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("update content read leaked work or decoded workspace metadata")
			}
			switch value := got.(type) {
			case []domain.Notification:
				for _, notification := range value {
					if !reflect.DeepEqual(notification, domain.Notification{}) {
						t.Fatal("close retained a returned notification backing value")
					}
				}
			case domain.ActivityHistorySnapshot:
				if value.Activity != nil && !reflect.DeepEqual(*value.Activity, domain.ActivityEvent{}) ||
					value.Comment != nil && !reflect.DeepEqual(*value.Comment, domain.Comment{}) {
					t.Fatal("close retained a returned history pointee")
				}
			}
			usable(t, tx)
		})
	}
	t.Run("fresh-lazy-heads-and-explicit-lifetime", func(t *testing.T) {
		tx := begin(t)
		put(t, tx, workspace, "notification", issue, "notification", "", at, domain.Notification{ID: "notification", Text: "Owned notification"})
		put(t, tx, workspace, "activity", issue, "activity", "", at, domain.ActivityEvent{ID: "before", Type: "issue.updated", Metadata: domain.ActivityMetadata{"title": "Before"}})
		put(t, tx, workspace, "comment", issue, "comment", "", at, domain.Comment{ID: "comment", Body: "Owned comment"})
		read := view.newIssueUpdateContentRows(tx, workspace)
		defer read.Close()
		notifications, err := read.loadNotifications(t.Context(), issue)
		if err != nil || len(notifications) != 1 {
			t.Fatal("missing owned notification fixture", len(notifications), err)
		}
		first, err := read.loadHistoryHead(t.Context(), issue)
		if err != nil {
			t.Fatal(err)
		}
		put(t, tx, workspace, "activity", issue, "new-activity", "", at.Add(time.Second), domain.ActivityEvent{ID: "after", Type: "issue.updated"})
		second, err := read.loadHistoryHead(t.Context(), issue)
		if err != nil || first.Activity == nil || first.Comment == nil || second.Activity == nil || second.Comment == nil || first.Activity.ID != "before" || second.Activity.ID != "after" {
			t.Fatal("lazy history load cached or invalidated an earlier owned head", err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		held := view.MetadataWorkUsedForTesting()
		if _, err := read.loadHistoryHead(ctx, issue); !errors.Is(err, context.Canceled) || first.Activity.ID != "before" || notifications[0].ID != "notification" || view.MetadataWorkUsedForTesting() < held {
			t.Fatal("cancellation discarded live update content", err)
		}
		budget, limit := view.metadataResidency.work, view.metadataResidency.work.limit
		budget.limit = view.MetadataWorkUsedForTesting() + 1
		_, err = read.loadNotifications(t.Context(), issue)
		budget.limit = limit
		if !errors.Is(err, ErrMetadataWorkBudget) || first.Activity.ID != "before" || notifications[0].ID != "notification" {
			t.Fatal("later admission failure discarded prior update content", err)
		}
		read.Close()
		read.Close()
		if !reflect.DeepEqual(notifications[0], domain.Notification{}) || !reflect.DeepEqual(*first.Activity, domain.ActivityEvent{}) ||
			!reflect.DeepEqual(*first.Comment, domain.Comment{}) || !reflect.DeepEqual(*second.Activity, domain.ActivityEvent{}) ||
			!reflect.DeepEqual(*second.Comment, domain.Comment{}) || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("explicit close retained owned slices, head pointees or work")
		}
		if _, err := read.loadNotifications(t.Context(), issue); !errors.Is(err, errMetadataLogicalClosed) {
			t.Fatal("closed update content reader accepted a new load", err)
		}
		usable(t, tx)
	})
}
