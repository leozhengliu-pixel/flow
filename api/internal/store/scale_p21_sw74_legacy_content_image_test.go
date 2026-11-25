package store

import (
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

func TestScaleP21SW74LegacyContentImage(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw74-content-")
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	f.db.SetMaxOpenConns(1)
	at, nativeAt := time.Date(2026, 11, 14, 1, 2, 3, 456, time.UTC), time.Now()
	stale := domain.User{ID: "content-actor", Name: "Literal SQL actor", AppScopes: []string{}, OutOfOfficeUntil: &nativeAt}
	fields := []string{"comments", "activities", "notifications", "notificationDeliveries", "issues", "users", "labels", "importJobs", "migrationJobs", "documents", "settings"}
	scope := MetadataRecordScope{Fields: fields}
	base := func() domain.Bootstrap {
		return domain.Bootstrap{
			Workspace: f.data.Workspace, Viewer: stale, ViewerRole: "literal-native-role",
			Users:         []domain.User{{ID: stale.ID, Name: "Directory actor must not replace the SQL actor"}},
			Labels:        []domain.IssueLabel{{ID: "label", Name: "Stale label", IssueCount: 741}},
			Issues:        []domain.Issue{{ID: "installed-issue", Title: "Preserve source issue", Labels: []domain.IssueLabel{{ID: "label", IssueCount: 742}}}},
			ImportJobs:    []domain.ImportJob{{ID: "inline-import", Rows: []map[string]string{{"literal": "rows"}}, CreatedAt: nativeAt}},
			MigrationJobs: []domain.MigrationJob{{ID: "inline-migration", Bundle: json.RawMessage(`{"literal":"bundle"}`), CreatedAt: nativeAt}},
			Documents:     []domain.Document{{ID: "unrelated-document", Content: strings.Repeat("Unrelated full document. ", 2000)}},
			Settings:      map[string]any{"native": map[string]any{"integer": int64(9007199254740993), "bytes": []byte{0, 255}, "nil": []string(nil), "empty": []string{}, "time": nativeAt}},
		}
	}
	supply := func(data *domain.Bootstrap, shape string) {
		if shape == "empty" {
			data.Comments, data.Activities = map[string][]domain.Comment{}, map[string][]domain.ActivityEvent{}
			data.Notifications, data.NotificationDeliveries = []domain.Notification{}, []domain.NotificationDelivery{}
		} else if shape == "nonempty" {
			data.Comments = map[string][]domain.Comment{"nil": nil, "empty": {}, "native": {{ID: "provided-comment", User: stale, EditedAt: &nativeAt,
				BodyData: map[string]any{"native-integer": int64(9007199254740993), "native-bytes": []byte{0, 255}}}}}
			data.Activities = map[string][]domain.ActivityEvent{"nil": nil, "empty": {}, "native": {{ID: "provided-activity", Actor: stale, UpdatedAt: &nativeAt, Metadata: domain.ActivityMetadata{"unchanged": "value"}}}}
			data.Notifications = []domain.Notification{{ID: "provided-notification", Actor: stale, CreatedAt: nativeAt, ReadAt: &nativeAt, LatestActorIDs: []string{}}}
			data.NotificationDeliveries = []domain.NotificationDelivery{{ID: "provided-delivery", CreatedAt: nativeAt, NextAttemptAt: &nativeAt}}
		}
	}
	begin := func(t *testing.T) (*sqlTx, context.Context) {
		t.Helper()
		tx, err := f.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = tx.Rollback() })
		if _, err := tx.ExecContext(t.Context(), `DELETE FROM workspace_content_records WHERE workspace_key=?`, workspace); err != nil {
			t.Fatal(err)
		}
		return tx, context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: f.db, workspace: workspace, reader: tx})
	}
	put := func(t *testing.T, tx *sqlTx, ws, kind string, resource any, physical, created string, value any) {
		t.Helper()
		raw, ok := value.([]byte)
		if !ok {
			var err error
			raw, err = json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
		}
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO workspace_content_records(workspace_key,kind,resource_id,id,created_at,data) VALUES(?,?,?,?,?,?)`,
			ws, kind, resource, prefix+physical, created, raw); err != nil {
			t.Fatal(err)
		}
	}
	seed := func(t *testing.T, tx *sqlTx, variant string) {
		t.Helper()
		rows := []struct {
			resource, physical string
			seconds            int
		}{
			{"a-group", "row-z", 1}, {"a-group", "row-b", 0}, {"a-group", "row-a", 0},
			{"A-group", "row-upper", 2}, {"z-group", "row-a", -1},
			{"\u00e9-group", "row-accent", 0}, {"\u4e2d-group", "row-unicode", 0}, {"", "row-empty", 3},
		}
		for _, kind := range []string{"activity", "comment", "notification", "delivery"} {
			for index, row := range rows {
				created, body := at.Add(time.Duration(row.seconds)*time.Second), "Full "+kind+" body "+row.physical
				if variant == "large" && index == 0 {
					body = strings.Repeat("Complete content body. ", 1<<15)
				}
				if variant == "legal-times" {
					created = time.Date(1, 1, 1, 0, 0, 0, index, time.FixedZone("legal-offset", 7*3600+13*60))
				}
				id := "duplicate-json-id"
				var value any
				switch kind {
				case "comment":
					value = domain.Comment{ID: id, Version: 74, Body: body, User: stale, CreatedAt: created, EditedAt: &created,
						ParentID: new("decoded-parent"), Reactions: map[string][]string{"like": {"one", "two"}}, BodyData: map[string]any{"complete": []any{"body", float64(7)}}}
				case "activity":
					value = domain.ActivityEvent{ID: id, Type: "literal-activity", Actor: stale, CreatedAt: created, UpdatedAt: &created,
						Metadata: domain.ActivityMetadata{"body": body, "issueId": "decoded-resource-must-not-regroup"}}
				case "notification":
					value = domain.Notification{ID: id, IssueID: "decoded-resource-must-not-regroup", SourceID: "decoded-source", Actor: stale,
						RecipientID: "recipient", OccurrenceCount: 7, LatestActorIDs: []string{"one", "one", "two"}, Text: body, CreatedAt: created, UpdatedAt: created}
				case "delivery":
					value = domain.NotificationDelivery{ID: id, NotificationID: "decoded-notification-must-not-join", RecipientID: "recipient",
						Channel: "chat", Status: "failed", Attempts: 3, Error: body, CreatedAt: created, UpdatedAt: created, NextAttemptAt: &created}
				}
				if variant == "null" && index == len(rows)-1 {
					value = []byte("null")
				}
				put(t, tx, workspace, kind, row.resource, row.physical, at.Add(time.Duration(row.seconds)*time.Second).Format(issueRecordTimestamp), value)
			}
			if variant == "sqlite-resources" {
				put(t, tx, workspace, kind, "a-group", "blob-resource", at.Add(-2*time.Second).Format(issueRecordTimestamp),
					map[string]any{"id": "duplicate-json-id", "body": "First BLOB resource body", "text": "First BLOB resource text", "error": "First BLOB resource error",
						"createdAt": at, "updatedAt": at, "user": stale, "actor": stale, "metadata": domain.ActivityMetadata{"body": "First BLOB resource activity"}})
				// The journal's JSON resource key rejects BLOBs even after overflow.
				// Its update predicate omits identity-only storage-class changes.
				if _, err := tx.ExecContext(t.Context(), `UPDATE workspace_content_records SET resource_id=CAST(resource_id AS BLOB) WHERE workspace_key=? AND kind=? AND id=?`,
					workspace, kind, prefix+"blob-resource"); err != nil {
					t.Fatal("set physical resource storage class", err)
				}
				var storage string
				if err := tx.QueryRowContext(t.Context(), `SELECT typeof(resource_id) FROM workspace_content_records WHERE workspace_key=? AND kind=? AND id=?`,
					workspace, kind, prefix+"blob-resource").Scan(&storage); err != nil || storage != "blob" {
					t.Fatal("physical BLOB resource witness missing", storage, err)
				}
			}
			put(t, tx, workspace+"-foreign", kind, "foreign", "foreign", at.Format(issueRecordTimestamp), []byte(`{"id":`))
		}
		for _, kind := range []string{"import_input", "migration_input"} {
			put(t, tx, workspace, kind, "unrelated-input", "unrelated-input", at.Format(issueRecordTimestamp), []byte(`{"id":`))
		}
	}
	literal := func(ctx context.Context, before domain.Bootstrap) (domain.Bootstrap, error) {
		next, base := before, newRecordBase()
		var err error
		if next.Activities == nil {
			next.Activities, err = readContentRecordsWithBase[domain.ActivityEvent](ctx, f.SQLiteStore, workspace, "activity", base)
			if err != nil {
				return next, err
			}
		}
		if next.Comments == nil {
			next.Comments, err = readContentRecordsWithBase[domain.Comment](ctx, f.SQLiteStore, workspace, "comment", base)
			if err != nil {
				return next, err
			}
		}
		if next.Notifications == nil {
			values, err := readContentRecordsWithBase[domain.Notification](ctx, f.SQLiteStore, workspace, "notification", base)
			if err != nil {
				return next, err
			}
			next.Notifications = []domain.Notification{}
			for _, key := range sortedKeys(values) {
				next.Notifications = append(next.Notifications, values[key]...)
			}
		}
		if next.NotificationDeliveries == nil {
			values, err := readContentRecordsWithBase[domain.NotificationDelivery](ctx, f.SQLiteStore, workspace, "delivery", base)
			if err != nil {
				return next, err
			}
			next.NotificationDeliveries = []domain.NotificationDelivery{}
			for _, key := range sortedKeys(values) {
				next.NotificationDeliveries = append(next.NotificationDeliveries, values[key]...)
			}
		}
		return next, nil
	}
	usable := func(t *testing.T, tx *sqlTx) {
		t.Helper()
		var one int
		if err := tx.QueryRowContext(t.Context(), `SELECT 1`).Scan(&one); err != nil || one != 1 {
			t.Fatal("content image consumed the borrowed transaction", err)
		}
	}
	check := func(t *testing.T, ctx context.Context, tx *sqlTx, source *metadataLogicalSource) *metadataLogicalSource {
		t.Helper()
		before, err := source.Select(t.Context(), scope, 64<<20)
		if err != nil {
			t.Fatal(err)
		}
		want, err := literal(ctx, before)
		if err != nil {
			t.Fatal("literal fixture failed", err)
		}
		contentQueries := 0
		observed := WithSQLStatementObserver(ctx, func(query string) {
			if strings.Contains(strings.ToLower(query), "from workspace_content_records") {
				contentQueries++
			}
		})
		result, err := view.prepareLegacyContentImage(observed, tx, workspace, source)
		if err != nil || result == nil {
			t.Fatal("legacy content image failed", err)
		}
		t.Cleanup(result.Release)
		got, err := result.Select(t.Context(), scope, 64<<20)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("literal full content images or native fields differ", err)
		}
		for index, node := range source.fields {
			name := reflect.TypeFor[domain.Bootstrap]().Field(index).Tag.Get("json")
			if name == "-" {
				continue
			}
			external := name == "comments" || name == "activities" || name == "notifications" || name == "notificationDeliveries"
			wasNil := false
			if external {
				wasNil, err = node.isNil(t.Context())
				if err != nil {
					t.Fatal(err)
				}
			}
			if (!external || !wasNil) && result.fields[index] != node {
				t.Fatal("content image rewrote a supplied field", name)
			}
		}
		if before.Comments != nil && before.Activities != nil && before.Notifications != nil && before.NotificationDeliveries != nil && contentQueries != 0 {
			t.Fatal("content image queried SQL for fully supplied content")
		}
		unchanged, err := source.Select(t.Context(), scope, 64<<20)
		if err != nil || !reflect.DeepEqual(before, unchanged) || result.Stamp() != source.Stamp() {
			t.Fatal("content image changed its input or stamp", err)
		}
		usable(t, tx)
		return result
	}
	for _, variant := range []string{"empty-sql", "nil", "null", "large", "legal-times", "sqlite-resources", "supplied-empty", "supplied-nonempty", "mixed"} {
		t.Run(variant, func(t *testing.T) {
			if variant == "sqlite-resources" && f.dialect != "sqlite" {
				t.Skip("SQLite TEXT/BLOB resource identity witness")
			}
			data := base()
			if variant == "supplied-empty" {
				supply(&data, "empty")
			}
			if variant == "supplied-nonempty" {
				supply(&data, "nonempty")
			}
			if variant == "mixed" {
				supply(&data, "nonempty")
				data.Activities, data.NotificationDeliveries = nil, nil
				data.Comments = map[string][]domain.Comment{}
			}
			disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
			source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{workspaceID: f.data.Workspace.ID, revision: -1})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(source.Release)
			tx, ctx := begin(t)
			if variant != "empty-sql" {
				seed(t, tx, variant)
			}
			for _, pair := range [][2]string{{"comments", "comment"}, {"activities", "activity"}, {"notifications", "notification"}, {"notificationDeliveries", "delivery"}} {
				index, _, _ := metadataLogicalField(pair[0])
				nilValue, err := source.fields[index].isNil(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if !nilValue {
					put(t, tx, workspace, pair[1], "ignored-malformed", "ignored-malformed", at.Format(issueRecordTimestamp), []byte(`{"id":`))
				}
			}
			result := check(t, ctx, tx, source)
			source.Release()
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if _, err := result.Select(t.Context(), scope, 64<<20); err != nil {
				t.Fatal("content image borrowed source or transaction lifetime", err)
			}
			result.Release()
			result.Release()
			if _, err := result.Select(t.Context(), scope, 64<<20); !errors.Is(err, errMetadataLogicalClosed) || view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("released content image retained output or work", err)
			}
			if metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors || view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("content image leaked storage or decoded workspace metadata")
			}
		})
	}
	t.Run("warm-and-reloaded", func(t *testing.T) {
		for _, reload := range []bool{false, true} {
			func() {
				if reload {
					if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
						t.Fatal(err)
					}
				}
				if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
					t.Fatal(err)
				}
				source, err := view.retainMetadataLogicalSource(t.Context(), workspace)
				if err != nil {
					t.Fatal(err)
				}
				defer source.Release()
				tx, ctx := begin(t)
				defer tx.Rollback()
				seed(t, tx, "nil")
				result := check(t, ctx, tx, source)
				result.Release()
				if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
					t.Fatal("installed content image leaked work or decoded metadata", reload)
				}
			}()
		}
	})
	for _, kind := range []string{"activity", "comment", "notification", "delivery", "same-kind-error-order", "field-error-order"} {
		t.Run("partial-malformed-"+kind, func(t *testing.T) {
			source, err := captureMetadataLogicalSource(t.Context(), base(), copyStamp{workspaceID: f.data.Workspace.ID, revision: -1})
			if err != nil {
				t.Fatal(err)
			}
			defer source.Release()
			tx, ctx := begin(t)
			seed(t, tx, "nil")
			switch kind {
			case "same-kind-error-order":
				put(t, tx, workspace, "comment", "z-first", "bad-first", at.Add(time.Hour).Format(issueRecordTimestamp), []byte(`{"id":"first","createdAt":42}`))
				put(t, tx, workspace, "comment", "a-later", "bad-later", at.Add(2*time.Hour).Format(issueRecordTimestamp), []byte(`{"id":`))
			case "field-error-order":
				put(t, tx, workspace, "activity", "last", "bad-activity", at.Add(time.Hour).Format(issueRecordTimestamp), []byte(`{"id":"activity-first","createdAt":42}`))
				put(t, tx, workspace, "comment", "last", "bad-comment", at.Add(time.Hour).Format(issueRecordTimestamp), []byte(`{"id":`))
			default:
				put(t, tx, workspace, kind, "last", "last-malformed", at.Add(time.Hour).Format(issueRecordTimestamp), []byte(fmt.Sprintf(`{"id":"bad-%s","createdAt":42}`, kind)))
			}
			before, err := source.Select(t.Context(), scope, 64<<20)
			if err != nil {
				t.Fatal(err)
			}
			_, wantErr := literal(ctx, before)
			disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
			result, gotErr := view.prepareLegacyContentImage(ctx, tx, workspace, source)
			if result != nil {
				result.Release()
			}
			if wantErr == nil || result != nil || reflect.TypeOf(wantErr) != reflect.TypeOf(gotErr) || wantErr.Error() != gotErr.Error() {
				t.Fatalf("literal/content image malformed boundary differs: %T %v / %T %v", wantErr, wantErr, gotErr, gotErr)
			}
			after, err := source.Select(t.Context(), scope, 64<<20)
			if err != nil || !reflect.DeepEqual(before, after) || metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors || view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("partial content failure changed its input or leaked", err)
			}
			usable(t, tx)
		})
	}
	t.Run("cancellation-and-admission", func(t *testing.T) {
		source, err := captureMetadataLogicalSource(t.Context(), base(), copyStamp{workspaceID: f.data.Workspace.ID, revision: -1})
		if err != nil {
			t.Fatal(err)
		}
		defer source.Release()
		tx, ctx := begin(t)
		seed(t, tx, "nil")
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
		if result, err := view.prepareLegacyContentImage(canceled, tx, workspace, source); result != nil || !errors.Is(err, context.Canceled) {
			if result != nil {
				result.Release()
			}
			t.Fatal("canceled content image escaped", err)
		}
		budget, limit := view.metadataResidency.work, view.metadataResidency.work.limit
		budget.limit = 1
		result, err := view.prepareLegacyContentImage(ctx, tx, workspace, source)
		budget.limit = limit
		if result != nil {
			result.Release()
		}
		if result != nil || !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 || metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
			t.Fatal("content image admission failure leaked", err)
		}
		usable(t, tx)
	})
}
