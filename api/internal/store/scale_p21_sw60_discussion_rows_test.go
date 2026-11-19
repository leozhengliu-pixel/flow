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

func TestScaleP21SW60DiscussionRows(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, issue := f.data.Workspace.URLKey, idgen.New("sw60-issue-")
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	f.db.SetMaxOpenConns(1)
	now := time.Now().UTC()
	at, large := now.Add(-time.Hour), strings.Repeat("Complete selected discussion body. ", 1<<15)
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
			t.Fatal("discussion loader consumed the borrowed transaction", err)
		}
	}
	put := func(t *testing.T, tx *sqlTx, ws, kind, resource, physical, parent, status string, created time.Time, value any) {
		t.Helper()
		raw, rawValue := value.([]byte)
		if !rawValue {
			var err error
			raw, err = json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
		}
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO workspace_content_records(workspace_key,kind,resource_id,id,parent_id,status,created_at,data) VALUES(?,?,?,?,?,?,?,?)`,
			ws, kind, resource, physical, parent, status, created.UTC().Format(issueRecordTimestamp), raw); err != nil {
			t.Fatal(err)
		}
	}
	comment := func(id string, parent *string) domain.Comment {
		return domain.Comment{ID: id, ParentID: parent, Version: 3, Body: "Full <comment> " + id, User: f.data.Viewer,
			BodyData:  map[string]any{"nested": map[string]any{"keep": []any{"complete", float64(7)}}},
			Reactions: map[string][]string{"like": {"first", "second"}}, CreatedAt: at, EditedAt: &at}
	}
	seedComments := func(t *testing.T, tx *sqlTx, variant string) string {
		t.Helper()
		if variant == "ancestor-cap" {
			for index := range 20 {
				id := fmt.Sprintf("chain-%02d", index)
				var parent *string
				if index < 19 {
					parent = new(fmt.Sprintf("chain-%02d", index+1))
				}
				parentID := ""
				if parent != nil {
					parentID = *parent
				}
				put(t, tx, workspace, "comment", issue, id, parentID, "", at, comment(id, parent))
			}
			return "chain-00"
		}
		if variant == "bfs-cap" {
			put(t, tx, workspace, "comment", issue, "root", "", "", at, comment("root", nil))
			for index := range 1002 {
				id := fmt.Sprintf("child-%04d", index)
				put(t, tx, workspace, "comment", issue, id, "root", "", at, comment(id, new("root")))
			}
			return "root"
		}
		if variant == "null" {
			put(t, tx, workspace, "comment", issue, "leaf", "unused", "", at, []byte("null"))
			return "leaf"
		}
		rootPhysical, rootID, midPhysical, midID, leafPhysical, leafID := "root", "root", "mid", "mid", "leaf", "leaf"
		if variant == "physical-json-ids" {
			rootPhysical, rootID, midPhysical, midID, leafPhysical, leafID = "root-physical", "root-json", "mid-physical", "mid-json", "leaf-physical", "leaf-json"
		}
		root, mid, leaf := comment(rootID, nil), comment(midID, &rootPhysical), comment(leafID, &midPhysical)
		if variant == "missing-parent" {
			leaf.ParentID = new("missing-parent")
		}
		if variant == "huge" {
			leaf.Body = large
		}
		var rootValue, midValue, leafValue any = root, mid, leaf
		if variant == "malformed-target" {
			leafValue = []byte(`{"id":`)
		}
		if variant == "malformed-parent" {
			midValue = []byte(`{"id":"mid","version":"bad"}`)
		}
		put(t, tx, workspace, "comment", issue, rootPhysical, "", "", at, rootValue)
		put(t, tx, workspace, "comment", issue, midPhysical, rootID, "", at, midValue)
		put(t, tx, workspace, "comment", issue, leafPhysical, midID, "", at, leafValue)
		siblingPhysical, sibling := "sibling", any(comment("sibling", &rootID))
		if variant == "duplicate-json-ids" {
			siblingPhysical = "sibling-a"
		}
		if variant == "malformed-reply" {
			sibling = []byte(`[]`)
		}
		put(t, tx, workspace, "comment", issue, siblingPhysical, rootID, "", at, sibling)
		if variant == "duplicate-json-ids" {
			second := comment("sibling", &rootID)
			second.Body = "Later duplicate must not replace the first occurrence"
			put(t, tx, workspace, "comment", issue, "sibling-b", rootID, "", at, second)
		}
		put(t, tx, workspace, "comment", issue, "grand", "sibling", "", at, comment("grand", new("sibling")))
		put(t, tx, workspace+"-foreign", "comment", issue, "foreign-only", "", "", at, comment("foreign", nil))
		put(t, tx, workspace, "comment", issue+"-foreign", "foreign-only", "", "", at, comment("foreign", nil))
		switch variant {
		case "empty-id":
			return ""
		case "missing":
			return "missing-only"
		case "foreign-id":
			return "foreign-only"
		case "root":
			return rootPhysical
		}
		return leafPhysical
	}
	seedNotifications := func(t *testing.T, tx *sqlTx, variant string) {
		t.Helper()
		notification := domain.Notification{ID: "notify-json", RecipientID: "recipient", Type: "issueComment", IssueID: "payload-issue",
			Actor: f.data.Viewer, GroupKey: "aggregation", OccurrenceCount: 7, LatestActorIDs: []string{"first", "first", "second"},
			Title: "Full title", Text: "Full notification body", CreatedAt: at, UpdatedAt: now.Add(-time.Hour)}
		if variant == "empty" || variant == "foreign-id" {
			put(t, tx, workspace+"-foreign", "notification", issue, "physical-a", "", "unread", at, notification)
			put(t, tx, workspace, "notification", issue+"-foreign", "physical-a", "", "unread", at, notification)
			return
		}
		if variant == "null" {
			put(t, tx, workspace, "notification", issue, "physical-a", "", "unread", at, []byte("null"))
			return
		}
		if variant == "huge" {
			notification.Text = large
		}
		put(t, tx, workspace, "notification", issue, "physical-a", "", "unread", at, notification)
		notification.Text, notification.UpdatedAt = "Second same-JSON-ID aggregate", now.Add(-5*time.Hour-59*time.Minute)
		put(t, tx, workspace, "notification", issue, "physical-b", "", "unread", at, notification)
		notification.ID, notification.ReadAt, notification.UpdatedAt = "notify-other", &now, now
		put(t, tx, workspace, "notification", issue, "physical-c", "", "unread", at.Add(time.Second), notification)
		notification.ID, notification.ReadAt, notification.UpdatedAt = "notify-future", nil, now.Add(time.Hour)
		put(t, tx, workspace, "notification", issue, "physical-d", "", "unread", at.Add(2*time.Second), notification)
		notification.ID, notification.UpdatedAt = "too-old", now.Add(-6*time.Hour-time.Minute)
		put(t, tx, workspace, "notification", issue, "old", "", "unread", at, notification)
		notification.ID, notification.UpdatedAt = "indexed-read", now
		put(t, tx, workspace, "notification", issue, "read", "", "read", at, notification)
		if variant == "typed-partial" || variant == "malformed" {
			raw := []byte(fmt.Sprintf(`{"id":"bad","updatedAt":%q,"occurrenceCount":"bad"}`, now.Format(time.RFC3339Nano)))
			if variant == "malformed" {
				raw = []byte(`{"updatedAt":`)
			}
			put(t, tx, workspace, "notification", issue, "last-bad", "", "unread", at.Add(time.Hour), raw)
		}
	}
	seedDeliveries := func(t *testing.T, tx *sqlTx, variant string) []domain.Notification {
		t.Helper()
		for index, row := range []struct{ resource, physical, parent string }{
			{"", "physical-b", "notify-json"}, {"nonempty-resource", "physical-a", "notify-json"},
			{"", "physical-c", "notify-other"}, {"", "orphan", ""},
		} {
			delivery := domain.NotificationDelivery{ID: fmt.Sprintf("delivery-json-%d", index), NotificationID: "payload-diverges",
				RecipientID: "recipient", Channel: "chat", Status: "pending", Attempts: 3, Error: "Complete delivery detail", CreatedAt: at, UpdatedAt: now, NextAttemptAt: &now}
			var value any = delivery
			if index == 0 {
				switch variant {
				case "huge":
					delivery.Error = large
					value = delivery
				case "null":
					value = []byte("null")
				case "malformed":
					value = []byte(`{"id":`)
				}
			}
			put(t, tx, workspace, "delivery", row.resource, row.physical, row.parent, "", at, value)
		}
		put(t, tx, workspace+"-foreign", "delivery", "", "foreign", "foreign-notification", "", at, domain.NotificationDelivery{ID: "foreign"})
		switch variant {
		case "empty":
			return nil
		case "empty-id":
			return []domain.Notification{{ID: ""}}
		case "foreign-id":
			return []domain.Notification{{ID: "foreign-notification"}}
		case "duplicate-ids":
			return []domain.Notification{{ID: "notify-json"}, {ID: "notify-json"}}
		case "over500":
			result := []domain.Notification{{ID: "notify-json"}}
			for index := range 499 {
				result = append(result, domain.Notification{ID: fmt.Sprintf("missing-%03d", index)})
			}
			return append(result, domain.Notification{ID: "notify-json"})
		}
		return []domain.Notification{{ID: "notify-json"}, {ID: "notify-other"}}
	}
	invoke := func(ctx context.Context, tx *sqlTx, read *issueMutationRows, kind, target string, notifications []domain.Notification) (any, error) {
		switch kind {
		case "comments":
			if read == nil {
				return loadDiscussionComment(ctx, tx, workspace, issue, target)
			}
			return read.loadDiscussionComment(ctx, issue, target)
		case "notifications":
			if read == nil {
				return f.activeDiscussionNotifications(ctx, tx, workspace, issue)
			}
			return read.activeDiscussionNotifications(ctx, issue)
		default:
			if read == nil {
				return issueNotificationDeliveries(ctx, tx, workspace, notifications)
			}
			return read.issueNotificationDeliveries(ctx, notifications)
		}
	}
	for _, test := range []struct {
		kind, variant string
		count         int
	}{
		{"comments", "thread", 5}, {"comments", "root", 5}, {"comments", "missing-parent", 1},
		{"comments", "empty-id", 0}, {"comments", "missing", 0}, {"comments", "foreign-id", 0},
		{"comments", "physical-json-ids", 5}, {"comments", "duplicate-json-ids", 5},
		{"comments", "ancestor-cap", 17}, {"comments", "bfs-cap", 1001}, {"comments", "huge", 5},
		{"comments", "null", 1}, {"comments", "malformed-target", -1}, {"comments", "malformed-parent", -1}, {"comments", "malformed-reply", -1},
		{"notifications", "aggregates", 4}, {"notifications", "empty", 0}, {"notifications", "foreign-id", 0},
		{"notifications", "huge", 4}, {"notifications", "null", 0}, {"notifications", "typed-partial", -1}, {"notifications", "malformed", -1},
		{"deliveries", "notification-keys", 3}, {"deliveries", "empty", 0}, {"deliveries", "empty-id", 1}, {"deliveries", "foreign-id", 0},
		{"deliveries", "duplicate-ids", 2}, {"deliveries", "over500", 4}, {"deliveries", "huge", 3}, {"deliveries", "null", 3}, {"deliveries", "malformed", -1},
	} {
		t.Run(test.kind+"/"+test.variant, func(t *testing.T) {
			tx := begin(t)
			target, selected := "", []domain.Notification(nil)
			switch test.kind {
			case "comments":
				target = seedComments(t, tx, test.variant)
			case "notifications":
				seedNotifications(t, tx, test.variant)
			case "deliveries":
				selected = seedDeliveries(t, tx, test.variant)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			writes := 0
			ctx = WithSQLStatementObserver(ctx, func(query string) {
				for _, verb := range []string{"INSERT", "UPDATE", "DELETE", "REPLACE"} {
					if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(query)), verb+" ") {
						writes++
					}
				}
			})
			// A failed PostgreSQL statement aborts the transaction; the bounded
			// reader must meet the same rows, not the literal reader's abort.
			if f.dialect == "postgres" {
				if _, err := tx.ExecContext(t.Context(), `SAVEPOINT sw60_literal`); err != nil {
					t.Fatal(err)
				}
			}
			want, wantErr := invoke(ctx, tx, nil, test.kind, target, selected)
			if f.dialect == "postgres" && wantErr != nil {
				if _, err := tx.ExecContext(t.Context(), `ROLLBACK TO SAVEPOINT sw60_literal`); err != nil {
					t.Fatal(err)
				}
			}
			read := view.newIssueMutationRows(tx, workspace)
			defer read.Close()
			got, gotErr := invoke(ctx, tx, read, test.kind, target, selected)
			if f.dialect == "postgres" && gotErr != nil {
				// Restore the borrowed transaction for the usability check below.
				if _, err := tx.ExecContext(t.Context(), `ROLLBACK TO SAVEPOINT sw60_literal`); err != nil {
					t.Fatal(err)
				}
			}
			if reflect.TypeOf(wantErr) != reflect.TypeOf(gotErr) || wantErr != nil && wantErr.Error() != gotErr.Error() {
				t.Fatalf("literal/bounded discussion error differs: %T %v / %T %v", wantErr, wantErr, gotErr, gotErr)
			}
			// Both writer callers return on error before consuming partial values.
			if wantErr == nil && !reflect.DeepEqual(want, got) {
				t.Fatal("literal/bounded complete data or occurrence order differs")
			}
			if writes != 0 {
				t.Fatal("discussion preimage loading wrote to the borrowed transaction")
			}
			if (wantErr != nil) != (test.count < 0) {
				t.Fatal("fixture did not exercise its expected error", wantErr)
			}
			if test.count >= 0 {
				count := reflect.ValueOf(got).Len()
				if test.kind == "comments" {
					count = len(got.(map[string][]domain.Comment)[issue])
				} else if test.kind == "notifications" {
					count = len(got.(map[string][]domain.Notification)[issue])
				}
				if count != test.count || count > 0 && view.MetadataWorkUsedForTesting() == 0 {
					t.Fatal("selected discussion count or retained ownership differs", count, test.count)
				}
			}
			read.Close()
			read.Close()
			if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("discussion rows leaked admission or decoded workspace metadata")
			}
			cleared := reflect.ValueOf(got)
			if cleared.Kind() == reflect.Map && cleared.Len() != 0 {
				t.Fatal("close retained a returned discussion map")
			}
			if cleared.Kind() == reflect.Slice {
				for index := 0; index < cleared.Len(); index++ {
					if !cleared.Index(index).IsZero() {
						t.Fatal("close retained a delivery backing value")
					}
				}
			}
			usable(t, tx)
		})
	}
	t.Run("composed-owner-lifetime", func(t *testing.T) {
		tx := begin(t)
		target := seedComments(t, tx, "thread")
		seedNotifications(t, tx, "aggregates")
		seedDeliveries(t, tx, "notification-keys")
		read := view.newIssueMutationRows(tx, workspace)
		defer read.Close()
		comments, err := read.loadDiscussionComment(t.Context(), issue, target)
		if err != nil {
			t.Fatal(err)
		}
		notifications, err := read.activeDiscussionNotifications(t.Context(), issue)
		if err != nil {
			t.Fatal(err)
		}
		deliveries, err := read.issueNotificationDeliveries(t.Context(), notifications[issue])
		if err != nil || len(deliveries) != 3 {
			t.Fatal("delivery selection lost returned JSON notification keys", err)
		}
		aliases := []any{comments[issue], notifications[issue], deliveries}
		before, _ := json.Marshal([]any{comments, notifications, deliveries})
		held := view.MetadataWorkUsedForTesting()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		for _, kind := range []string{"comments", "notifications", "deliveries"} {
			if _, err := invoke(ctx, tx, read, kind, target, notifications[issue]); !errors.Is(err, context.Canceled) {
				t.Fatal("canceled discussion load succeeded", kind, err)
			}
		}
		after, _ := json.Marshal([]any{comments, notifications, deliveries})
		if !bytes.Equal(before, after) || view.MetadataWorkUsedForTesting() < held {
			t.Fatal("cancellation released still-used discussion preimages")
		}
		budget, limit := view.metadataResidency.work, view.metadataResidency.work.limit
		budget.limit = view.MetadataWorkUsedForTesting() + 1
		_, err = read.loadDiscussionComment(t.Context(), issue, target)
		budget.limit = limit
		if !errors.Is(err, ErrMetadataWorkBudget) {
			t.Fatal("later load escaped exhausted admission", err)
		}
		after, _ = json.Marshal([]any{comments, notifications, deliveries})
		if !bytes.Equal(before, after) {
			t.Fatal("failed admission cleared prior discussion results")
		}
		delete(comments, issue)
		notifications[issue] = nil
		read.Close()
		if len(comments) != 0 || len(notifications) != 0 || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("explicit close retained discussion maps or work")
		}
		for _, alias := range aliases {
			values := reflect.ValueOf(alias)
			for index := 0; index < values.Len(); index++ {
				if !values.Index(index).IsZero() {
					t.Fatal("explicit close retained a returned slice backing value")
				}
			}
		}
		for _, kind := range []string{"comments", "notifications", "deliveries"} {
			if _, err := invoke(t.Context(), tx, read, kind, target, nil); !errors.Is(err, errMetadataLogicalClosed) {
				t.Fatal("closed owner accepted discussion load", kind, err)
			}
		}
		usable(t, tx)
	})
	for _, kind := range []string{"comments", "notifications", "deliveries"} {
		t.Run("admission/"+kind, func(t *testing.T) {
			tx := begin(t)
			target, selected := "", []domain.Notification(nil)
			switch kind {
			case "comments":
				target = seedComments(t, tx, "huge")
			case "notifications":
				seedNotifications(t, tx, "huge")
			case "deliveries":
				selected = seedDeliveries(t, tx, "huge")
			}
			budget, limit := view.metadataResidency.work, view.metadataResidency.work.limit
			defer func() { budget.limit = limit }()
			for _, ceiling := range []int{1, 256 << 10} {
				budget.limit = ceiling
				read := view.newIssueMutationRows(tx, workspace)
				_, err := invoke(t.Context(), tx, read, kind, target, selected)
				read.Close()
				if !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("discussion admission boundary escaped or leaked", ceiling, err)
				}
				usable(t, tx)
			}
		})
	}
}
