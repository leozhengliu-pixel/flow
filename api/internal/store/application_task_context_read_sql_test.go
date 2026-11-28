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

func TestScaleApplicationTaskContextReadContentProducerDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := journalScaleDatabase(t)
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), f.data.Workspace.URLKey)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	at := time.Date(2026, 11, 27, 1, 2, 3, 4, time.FixedZone("offset", 3600))
	body := strings.Repeat("<>&\x00\xff\u2028\\u0000 ", metadataJSONLargeBody/8)
	owner := f.data.Viewer
	parent := "parent-comment"
	checkTaskContentProducer(t, f, view, "comment", domain.Comment{
		ID: "producer-comment", Body: body, BodyData: map[string]any{"raw": json.RawMessage(`{ "text": "<>&" }`)},
		ParentID: &parent, Reactions: map[string][]string{"ok": {owner.ID}}, CreatedAt: at, User: owner,
	})
	checkTaskContentProducer(t, f, view, "activity", domain.ActivityEvent{
		ID: "producer-activity", Actor: owner, CreatedAt: at,
		Metadata: domain.ActivityMetadata{"cycle": "cycle-after", "cycleBefore": "cycle-before", "detail": body},
	})
	checkTaskContentProducer(t, f, view, "notification", domain.Notification{
		ID: "producer-notification", RecipientID: owner.ID, Text: body, ReadAt: &at, SnoozedUntil: &at, CreatedAt: at,
	})
	checkTaskContentProducer(t, f, view, "delivery", domain.NotificationDelivery{
		ID: "producer-delivery", RecipientID: owner.ID, NotificationID: "producer-notification",
		Status: "pending", NextAttemptAt: &at, CreatedAt: at, Error: body,
	})
	if work := view.MetadataWorkUsedForTesting(); work != 0 {
		t.Fatalf("content producers left %d bytes of work", work)
	}
}

func checkTaskContentProducer[T any](t *testing.T, f scaleFixture, view *SQLiteStore, kind string, value T) {
	t.Helper()
	t.Run(kind, func(t *testing.T) {
		ctx := t.Context()
		workspace := f.data.Workspace.URLKey
		tx, err := f.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		for _, producer := range []struct {
			name  string
			write func(string) error
		}{
			{"point", func(resource string) error {
				return writeContentRecord(withIssueRecordWriteAdmission(ctx, view), tx, workspace, kind, resource, value)
			}},
			{"batch", func(resource string) error {
				row, err := contentRecordRowFor(workspace, kind, resource, value)
				if err != nil {
					return err
				}
				return writeContentRecordRows(ctx, tx, []contentRecordRow{row})
			}},
			{"sync", func(resource string) error {
				return syncContentRecordsFrom(ctx, tx, workspace, kind, map[string][]T{resource: {value}}, f.data, nil, []string{resource})
			}},
		} {
			resource := "producer-" + producer.name
			want, err := buildContentRecordRow(workspace, kind, resource, raw)
			if err != nil {
				t.Fatal(err)
			}
			if err := producer.write(resource); err != nil {
				t.Fatalf("%s write: %v", producer.name, err)
			}
			var got contentRecordRow
			var version int
			err = tx.QueryRowContext(ctx, `SELECT id,created_at,data,owner_id,parent_id,status,next_attempt_at,record_version
				FROM workspace_content_records WHERE workspace_key=? AND kind=? AND resource_id=?`, workspace, kind, resource).
				Scan(&got.id, &got.created, &got.raw, &got.owner, &got.parent, &got.status, &got.nextAttempt, &version)
			if err != nil {
				t.Fatalf("%s read: %v", producer.name, err)
			}
			if version != 1 || !bytes.Equal(got.raw, want.raw) || got.id != want.id || got.created != want.created ||
				got.owner != want.owner || got.parent != want.parent || got.status != want.status || got.nextAttempt != want.nextAttempt {
				t.Fatalf("%s stored JSON or index columns differ from json.Marshal", producer.name)
			}
		}
	})
}

func TestScaleApplicationTaskContextReadRawBatches(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := journalScaleDatabase(t)
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("p21-task-batches-")
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.task_batches", "", nil, func(data *domain.Bootstrap) error {
		data.Issues = nil
		for index := range 51 {
			data.Issues = append(data.Issues, domain.Issue{ID: fmt.Sprintf("%s%03d", prefix, index), Identifier: fmt.Sprintf("BATCH-%d", index+1), Team: data.Teams[0], State: data.States[0], Creator: data.Viewer})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	original, err := f.readIssueRecords(t.Context(), workspace)
	if err != nil || len(original) != 51 {
		t.Fatal("batch fixture", err)
	}
	f.db.SetMaxOpenConns(1)
	for _, fixture := range []struct {
		name  string
		sizes []int
		want  int
	}{
		{"fifty-one-small", make([]int, 51), 4},
		{"small-large-small", []int{128, taskIssueBatchBytes + 1, 128}, 13},
		{"exact-byte-cap", []int{taskIssueBatchBytes / 2, taskIssueBatchBytes / 2}, 3},
		{"cross-byte-cap", []int{taskIssueBatchBytes / 2, taskIssueBatchBytes/2 + 1}, 4},
		{"exact-row-cap", []int{taskIssueBatchBytes}, 3},
	} {
		// Preserve the already-admitted physical header order, while raw JSON
		// identities deliberately collide and differ from every physical key.
		for index, issue := range original {
			raw := []byte(`null`)
			if index < len(fixture.sizes) {
				size := max(128, fixture.sizes[index])
				base := `{"id":"duplicate-json-id","team":{"id":"team"},"project":{"id":"project"},"description":""}`
				raw = []byte(strings.Replace(base, `"description":""`, `"description":"`+strings.Repeat("x", size-len(base))+`"`, 1))
			}
			if _, err := f.db.ExecContext(t.Context(), `UPDATE issue_records SET data=? WHERE workspace_key=? AND id=?`, raw, workspace, issue.ID); err != nil {
				t.Fatal(err)
			}
		}
		headers := original[:len(fixture.sizes)]
		remaining := original[len(fixture.sizes):]
		for _, issue := range remaining {
			if _, err := f.db.ExecContext(t.Context(), `DELETE FROM issue_records WHERE workspace_key=? AND id=?`, workspace, issue.ID); err != nil {
				t.Fatal(err)
			}
		}
		queries := 0
		ctx := WithSQLStatementObserver(t.Context(), func(string) { queries++ })
		read := &ApplicationTaskContextRead{Data: domain.Bootstrap{Workspace: f.data.Workspace}, owner: &ResourceRead{store: view}}
		err := read.rawIssueInputs(ctx, workspace, true)
		if err != nil || len(read.Data.Issues) != len(headers) || queries != fixture.want {
			t.Fatalf("batch=%s rows=%d SQL=%d want_SQL=%d err=%v", fixture.name, len(read.Data.Issues), queries, fixture.want, err)
		}
		for _, issue := range read.Data.Issues {
			if issue.ID != "duplicate-json-id" || issue.Team.ID != "team" || issue.Project == nil || issue.Project.ID != "project" || issue.Description != "" {
				t.Fatal("batch altered raw identity or materialized an ignored body", fixture.name)
			}
		}
		read.Close()
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("batch retained admission")
		}
		t.Logf("batch=%s rows=%d raw_SQL=%d", fixture.name, len(headers), queries)
		// The next fixture only shortens the physical prefix.
		original = headers
	}
}

func TestScaleApplicationTaskContextReadPhysicalBatch(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := journalScaleDatabase(t)
	if f.dialect != "sqlite" {
		t.Skip("SQLite storage-class identity")
	}
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("p21-task-physical-")
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.task_physical_batch", "", nil, func(data *domain.Bootstrap) error {
		data.Issues = nil
		for index := range 5 {
			data.Issues = append(data.Issues, domain.Issue{ID: fmt.Sprintf("%s%d", prefix, index), Identifier: fmt.Sprintf("PHYS-%d", index+1), Team: data.Teams[0], State: data.States[0], Creator: data.Viewer})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	f.db.SetMaxOpenConns(1)
	// Keep the old/raw SQLite writer's BLOB keys and journal context entirely
	// inside a rolled-back transaction; the inline JSON journal rejects them.
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Tx.ExecContext(t.Context(), `UPDATE record_journal_context SET tx_id='' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	for index, id := range []any{"", prefix + "\x00text", []byte(prefix + "\x00blob"), prefix + "same", []byte(prefix + "same")} {
		raw := []byte(fmt.Sprintf(`{"id":"logical-%d","team":{"id":"team"},"project":{"id":"project"}}`, index))
		if _, err := tx.Tx.ExecContext(t.Context(), `UPDATE issue_records SET id=?,data=?,collection_order=0 WHERE workspace_key=? AND id=?`, id, raw, workspace, fmt.Sprintf("%s%d", prefix, index)); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: f.db, workspace: workspace, reader: tx})
	legacy, err := f.readIssueRecords(ctx, workspace)
	if err != nil {
		t.Fatal(err)
	}
	queries := 0
	ctx = WithSQLStatementObserver(ctx, func(string) { queries++ })
	read := &ApplicationTaskContextRead{Data: domain.Bootstrap{Workspace: f.data.Workspace}, owner: &ResourceRead{store: view}}
	if err := read.rawIssueInputs(ctx, workspace, true); err != nil || len(read.Data.Issues) != len(legacy) || queries != 3 {
		t.Fatal("physical batch read", len(read.Data.Issues), queries, err)
	}
	for index, issue := range read.Data.Issues {
		if issue.ID != legacy[index].ID {
			t.Fatal("physical TEXT/BLOB identity or collection order changed", index, issue.ID, legacy[index].ID)
		}
	}
	if _, err := tx.ExecContext(ctx, `SELECT 1`); err != nil {
		t.Fatal("raw batch closed caller transaction", err)
	}
	read.Close()
	limit := view.metadataResidency.work.limit
	view.metadataResidency.work.limit = 8<<20 + 32<<10
	queries = 0
	failed := &ApplicationTaskContextRead{Data: domain.Bootstrap{Workspace: f.data.Workspace}, owner: &ResourceRead{store: view}}
	err = failed.rawIssueInputs(ctx, workspace, true)
	failed.Close()
	view.metadataResidency.work.limit = limit
	if !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 || queries != 2 {
		t.Fatal("partial batch admission leaked", queries, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	failed = &ApplicationTaskContextRead{Data: domain.Bootstrap{Workspace: f.data.Workspace}, owner: &ResourceRead{store: view}}
	err = failed.rawIssueInputs(cancelled, workspace, true)
	failed.Close()
	if !errors.Is(err, context.Canceled) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("cancelled batch leaked", err)
	}
}

func TestScaleApplicationTaskContextReadSQLDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := journalScaleDatabase(t)
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("p21-task-count-hold-")
	at := time.Now().UTC()
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.task_count_sql_hold", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: prefix + "private", Private: true})
		data.TeamSettings[prefix+"private"] = domain.TeamSettings{Access: "private"}
		data.Projects = []domain.Project{{ID: prefix + "project", TeamIDs: []string{data.Teams[0].ID}}}
		data.Issues = []domain.Issue{
			{ID: prefix + "issue", Identifier: "CTX-31", Title: "Count raw JSON", Team: data.Teams[0], Project: &domain.ProjectSummary{ID: prefix + "project"}, State: data.States[0], Creator: data.Viewer, CreatedAt: at, UpdatedAt: at},
			{ID: prefix + "parent", Identifier: "CTX-32", Title: "Permission parent", Team: data.Teams[0], State: data.States[0], Creator: data.Viewer, CreatedAt: at, UpdatedAt: at},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// The literal full accessor reads data, not these indexed columns.
	if _, err := f.db.ExecContext(t.Context(), `UPDATE issue_records SET project_id=?,team_id=?,parent_id=?,archived=1 WHERE workspace_key=? AND id=?`, prefix+"indexed-other", prefix+"indexed-team", prefix+"indexed-parent", workspace, prefix+"issue"); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	legacy, ok, err := f.BootstrapForUser(t.Context(), workspace, f.data.Viewer.ID)
	if !ok || err != nil || len(legacy.Projects) != 1 {
		t.Fatal("literal SQL count fixture failed", err)
	}
	read, err := view.BeginApplicationTaskContextRead(t.Context(), workspace, f.data.Viewer.ID, nil, ApplicationTaskContextRequest{ResourceType: "project", ResourceID: prefix + "project"})
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Projects[0].IssueCount != 1 || read.ProjectIssueCount != legacy.Projects[0].IssueCount {
		t.Fatalf("SQL raw issue count differs: literal=%d bounded=%d", legacy.Projects[0].IssueCount, read.ProjectIssueCount)
	}
	read.Close()
	t.Logf("raw/index divergence: literal task issueCount=%d bounded raw-header count=%d", legacy.Projects[0].IssueCount, read.ProjectIssueCount)
	for _, fixture := range []struct {
		name, team string
		null       bool
		granted    bool
		inherited  bool
		want       int
	}{
		{name: "null", null: true},
		{name: "closed-private", team: prefix + "private"},
		{name: "private-user-grant", team: prefix + "private", granted: true, want: 1},
		{name: "admin-unknown-team", team: prefix + "unknown", want: 1},
		{name: "private-parent-grant", team: prefix + "private", inherited: true, want: 1},
	} {
		issue := domain.Issue{ID: prefix + "json-identity", Team: domain.Team{ID: fixture.team}, Project: &domain.ProjectSummary{ID: prefix + "project"}}
		parent := domain.Issue{ID: prefix + "json-parent"}
		grant := domain.IssuePermission{SubjectType: "user", SubjectID: f.data.Viewer.ID, Role: "viewer"}
		if fixture.granted {
			issue.Permissions = []domain.IssuePermission{grant}
		}
		if fixture.inherited {
			issue.ParentID = &parent.ID
			parent.Permissions = []domain.IssuePermission{grant}
		}
		raw, _ := json.Marshal(issue)
		if fixture.null {
			raw = []byte("null")
		}
		parentRaw, _ := json.Marshal(parent)
		for _, row := range []struct {
			id  string
			raw []byte
		}{{prefix + "issue", raw}, {prefix + "parent", parentRaw}} {
			if _, err := f.db.ExecContext(t.Context(), `UPDATE issue_records SET data=? WHERE workspace_key=? AND id=?`, row.raw, workspace, row.id); err != nil {
				t.Fatal(err)
			}
		}
		legacy, ok, err := f.BootstrapForUser(t.Context(), workspace, f.data.Viewer.ID)
		if err != nil || !ok {
			t.Fatal("raw count literal", fixture.name, err)
		}
		read, err := view.BeginApplicationTaskContextRead(t.Context(), workspace, f.data.Viewer.ID, nil, ApplicationTaskContextRequest{ResourceType: "project", ResourceID: prefix + "project"})
		if err != nil || read.ProjectIssueCount != legacy.Projects[0].IssueCount || read.ProjectIssueCount != fixture.want {
			t.Fatalf("raw policy count %s legacy=%d bounded=%v err=%v want=%d", fixture.name, legacy.Projects[0].IssueCount, read, err, fixture.want)
		}
		read.Close()
	}
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("raw SQL count retained admission")
	}
}

func TestScaleApplicationTaskContextReadRawCodec(t *testing.T) {
	view := scaleHotLogoStore(t, map[string]domain.Bootstrap{"physical": {}})
	for _, raw := range []string{
		` null `,
		` {"id":"one","id":null,"team":{"id":"team"},"project":{"id":"project"},"parentId":"parent","permissions":[{"subjectType":"user","subjectId":"user","role":"editor"}]} `,
		`{"Team":{"ID":"one"},"team":{"key":"unused"},"PROJECT":{"id":"p"},"project":{},"permissions":[{"subjectType":"user","subjectId":"u","role":"viewer"}],"permissions":[{"subjectId":"v"},null],"parentId":"a","parentId":null,"archivedAt":"2025-01-02T03:04:05Z","archivedAt":null}`,
		`{"team":{"id":"one"},"team":null,"project":{"id":"one"},"project":null,"project":{"name":"empty id"},"permissions":[{"subjectId":"u"}],"permissions":[],"permissions":[null]}`,
		`{"title":{},"project":{"id":"project"}}`,
		`{"title":12,"title":"overwritten"}`,
		`{"assignee":{"active":"invalid"}}`,
		`{"reactions":{"key":[1]}}`,
		`{"attachments":[{"metadata":{"large":1e300,"array":[null,true,"text",{}]}}]}`,
		`{"attachments":[{"metadata":{"overflow":1e999}}]}`,
		`{"unknown":{"overflow":1e999}}`,
		`{"createdAt":"2025-01-02T03:04:05.` + strings.Repeat("1234567890", 1000) + `Z"}`,
		`{"createdAt":"invalid"}`,
		`{"description":"` + strings.Repeat("unused body ", 10000) + `"}`,
		`{"id":"one"} trailing`,
	} {
		owner := &ResourceRead{store: view}
		parser := taskIssueJSON{ctx: t.Context(), input: bytes.NewReader([]byte(raw)), owner: owner}
		part := metadataCollectionRange{size: int64(len(raw))}
		var legacy domain.Issue
		legacyErr := json.Unmarshal([]byte(raw), &legacy)
		err := parser.validate(part, reflect.TypeFor[domain.Issue](), 0)
		if (err == nil) != (legacyErr == nil) {
			t.Fatalf("typed validation differs: legacy=%v bounded=%v raw=%.180s", legacyErr, err, raw)
		}
		if err == nil {
			var got domain.Issue
			if err := parser.project(part, reflect.ValueOf(&got).Elem(), 0); err != nil {
				t.Fatal(err)
			}
			want := domain.Issue{ID: legacy.ID, Team: domain.Team{ID: legacy.Team.ID}, ParentID: legacy.ParentID, ArchivedAt: legacy.ArchivedAt}
			if legacy.Project != nil {
				want.Project = &domain.ProjectSummary{ID: legacy.Project.ID}
			}
			if legacy.Permissions != nil {
				want.Permissions = []domain.IssuePermission{}
				for _, item := range legacy.Permissions {
					want.Permissions = append(want.Permissions, domain.IssuePermission{SubjectType: item.SubjectType, SubjectID: item.SubjectID, Role: item.Role})
				}
			}
			before, _ := json.Marshal(want)
			after, _ := json.Marshal(got)
			if !bytes.Equal(before, after) {
				t.Fatalf("typed header differs:\n%s\n%s", before, after)
			}
		}
		owner.Close()
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("raw issue header retained admission")
		}
	}
}
