package store

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21SW29LogicalExternalHydrationDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	ctx, workspace := t.Context(), f.data.Workspace.URLKey
	warm, found := f.storedWorkspace(workspace)
	if !found {
		t.Fatal("fixture workspace missing")
	}
	warm = cloneBootstrap(warm)
	now := time.Date(2026, 11, 13, 2, 3, 4, 5, time.UTC)
	stale := warm.Viewer
	stale.Name = "stale external copy"
	issue := domain.Issue{
		ID: idgen.New("issue_"), Identifier: "SW29-1", Number: 1, Title: "external",
		Description: strings.Repeat("x", 8<<20), Team: warm.Teams[0], State: warm.States[0],
		Creator: stale, Assignee: &stale, Delegate: &stale, CreatedAt: now, UpdatedAt: now,
	}
	jobID, migrationID := idgen.New("import_"), idgen.New("migration_")
	warm.ImportJobs = []domain.ImportJob{{ID: jobID, Status: "pending", CreatedAt: now}}
	warm.MigrationJobs = []domain.MigrationJob{{ID: migrationID, CreatedAt: now}}
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := f.writeIssueRecord(ctx, tx, workspace, issue); err != nil {
		t.Fatal(err)
	}
	for i, resource := range []string{"z-resource", "", "a-resource", "a-resource"} {
		created := now.Add(time.Duration(3-i) * time.Second)
		if err := writeContentRecord(ctx, tx, workspace, "comment", resource, domain.Comment{ID: idgen.New("comment_"), Body: resource, User: stale, CreatedAt: created}); err != nil {
			t.Fatal(err)
		}
		if err := writeContentRecord(ctx, tx, workspace, "activity", resource, domain.ActivityEvent{ID: idgen.New("activity_"), Actor: stale, CreatedAt: created}); err != nil {
			t.Fatal(err)
		}
		if err := writeContentRecord(ctx, tx, workspace, "notification", resource, domain.Notification{ID: idgen.New("notification_"), Actor: stale, RecipientID: warm.Viewer.ID, CreatedAt: created, UpdatedAt: created}); err != nil {
			t.Fatal(err)
		}
		if err := writeContentRecord(ctx, tx, workspace, "delivery", resource, domain.NotificationDelivery{ID: idgen.New("delivery_"), RecipientID: warm.Viewer.ID, CreatedAt: created, UpdatedAt: created}); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeContentRecord(ctx, tx, workspace, "import_input", jobID, importInput{ID: idgen.New("input_"), Rows: []map[string]string{{"original": "rows"}}, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := writeContentRecord(ctx, tx, workspace, "migration_input", migrationID, importInput{ID: idgen.New("input_"), Bundle: json.RawMessage(`{"version":1}`), CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	expected := cloneBootstrap(warm)
	expected.Issues, err = f.readIssueRecords(ctx, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.hydrateContentRecords(ctx, workspace, &expected); err != nil {
		t.Fatal(err)
	}
	source, err := captureMetadataLogicalSource(ctx, warm, copyStamp{warm.Workspace.ID, -1})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	read, err := f.db.BeginTx(ctx, metadataReadOptions(f.dialect))
	if err != nil {
		t.Fatal(err)
	}
	defer read.Rollback()
	bodyQueries := 0
	ctx = WithSQLStatementObserver(ctx, func(query string) {
		if strings.Contains(query, "SUBSTR(") && strings.Contains(query, "FROM issue_records") {
			bodyQueries++
		}
	})
	hydrated, err := f.hydrateMetadataLogicalSource(ctx, read, source, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer hydrated.Release()
	if bodyQueries < 1 || bodyQueries > 9 {
		t.Fatalf("8 MiB issue hydration used %d body queries; want at most nine bounded 1 MiB transfers", bodyQueries)
	}
	t.Logf("8 MiB external issue body SELECTs=%d, transfer chunk=%d, parser allowance=%d", bodyQueries, metadataLogicalExternalReadChunk, metadataLogicalExternalWork)
	fields := []string{"issues", "comments", "activities", "notifications", "notificationDeliveries", "importJobs", "migrationJobs"}
	got, err := hydrated.Select(ctx, MetadataRecordScope{Fields: fields}, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range fields {
		index, _, _ := metadataLogicalField(name)
		if !reflect.DeepEqual(reflect.ValueOf(got).Field(index).Interface(), reflect.ValueOf(expected).Field(index).Interface()) {
			t.Errorf("external field %s differs from literal legacy hydration", name)
		}
	}
	refreshed, err := refreshMetadataLogicalIssueReferences(ctx, hydrated)
	if err != nil {
		t.Fatal(err)
	}
	defer refreshed.Release()
	refreshIssueReferences(&expected)
	resolved, err := refreshed.Select(ctx, MetadataRecordScope{Fields: []string{"issues"}}, 32<<20)
	if err != nil || !reflect.DeepEqual(resolved.Issues, expected.Issues) {
		t.Fatalf("external issue reference refresh mismatch: %v", err)
	}
}

func TestScaleP21SW29LogicalExternalUnrelatedInputError(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	ctx, workspace := t.Context(), f.data.Workspace.URLKey
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	id := idgen.New("bad_input_")
	if err := writeContentRecord(ctx, tx, workspace, "import_input", "unrelated", importInput{ID: id, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	var valid []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM workspace_content_records WHERE workspace_key=? AND kind='import_input' AND id=?`, workspace, id).Scan(&valid); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE workspace_content_records SET data=? WHERE workspace_key=? AND kind='import_input' AND id=?`, "{", workspace, id); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if _, err := f.db.ExecContext(cleanup, `UPDATE workspace_content_records SET data=? WHERE workspace_key=? AND kind='import_input' AND id=?`, valid, workspace, id); err != nil {
			t.Errorf("restore owned malformed input fixture: %v", err)
		}
	})
	warm, _ := f.WorkspaceMetadata(workspace)
	literal := cloneBootstrap(warm)
	if err := f.hydrateContentRecords(ctx, workspace, &literal); err == nil {
		t.Fatal("legacy hydrate unexpectedly accepted malformed unrelated input")
	}
	source, err := captureMetadataLogicalSource(ctx, collectionMetadata(warm), copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
	read, err := f.db.BeginTx(ctx, metadataReadOptions(f.dialect))
	if err != nil {
		t.Fatal(err)
	}
	defer read.Rollback()
	if result, err := f.hydrateMetadataLogicalSource(ctx, read, source, workspace); err == nil || result != nil {
		if result != nil {
			result.Release()
		}
		t.Fatalf("bounded hydration changed malformed-input boundary: %v", err)
	}
	if metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
		t.Fatal("failed external hydration leaked a source")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := f.hydrateMetadataLogicalSource(cancelled, read, source, workspace); err != context.Canceled {
		t.Fatalf("external cancellation: %v", err)
	}
}
