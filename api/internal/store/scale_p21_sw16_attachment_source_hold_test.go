package store

import (
	"reflect"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// Keep this accepted producer regression: the legacy decoded identity can
// differ from the physical SQL row key after the public typed importer.
func TestScaleP21SW16AttachmentImportedIdentitySourceHold(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	if f.dialect != "sqlite" {
		t.Skip("SQLite preserves a NUL identity column while canonical JSON is sanitized")
	}
	workspace, id := f.data.Workspace.URLKey, idgen.New("sw16-imported-attachment-")
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	const url = "https://example.test/owned-attachment"
	issue := domain.Issue{
		ID: id + "\x00", Identifier: "SW16-1", Title: "Accepted imported identity",
		Team: domain.Team{ID: id + "-team", Key: "SW16"}, State: domain.WorkflowState{ID: id + "-state"},
		Creator: f.data.Viewer, CreatedAt: stamp, UpdatedAt: stamp,
		Attachments: []domain.Attachment{{ID: id + "-attachment", URL: url}},
	}
	if err := f.ImportIssues(t.Context(), workspace, []domain.Issue{issue}); err != nil {
		t.Fatal("public typed importer rejected the source witness", err)
	}
	probe, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []*SQLiteStore{f.SQLiteStore, probe} {
		source.mu.Lock()
		source.workspaces = map[string]domain.Bootstrap{workspace: source.workspaces[workspace]}
		source.mu.Unlock()
	}
	index := func() map[string][]string {
		t.Helper()
		rows, err := f.db.QueryContext(t.Context(), `SELECT issue_id,url_hash FROM issue_attachment_records WHERE workspace_key=? ORDER BY issue_id,url_hash`, workspace)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		result := map[string][]string{}
		for rows.Next() {
			var issueID, hash string
			if err := rows.Scan(&issueID, &hash); err != nil {
				t.Fatal(err)
			}
			result[issueID] = append(result[issueID], hash)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return result
	}
	results := []map[string][]string{}
	checkpoints := []string{}
	for _, source := range []*SQLiteStore{f.SQLiteStore, probe} {
		for _, table := range []string{"issue_attachment_records", "issue_attachment_migrations"} {
			if _, err := f.db.ExecContext(t.Context(), "DELETE FROM "+table+" WHERE workspace_key=?", workspace); err != nil {
				t.Fatal(err)
			}
		}
		if err := source.migrateAttachmentIndex(t.Context()); err != nil {
			t.Fatal(err)
		}
		results = append(results, index())
		var last string
		var complete int
		if err := f.db.QueryRowContext(t.Context(), `SELECT last_id,complete FROM issue_attachment_migrations WHERE workspace_key=?`, workspace).Scan(&last, &complete); err != nil {
			t.Fatal(err)
		}
		if complete != 1 {
			t.Fatal("accepted importer migration did not complete")
		}
		checkpoints = append(checkpoints, last)
	}
	legacy := map[string][]string{id: {attachmentKey(url)}}
	if !reflect.DeepEqual(results[0], legacy) || !reflect.DeepEqual(results[1], legacy) || checkpoints[0] != id || checkpoints[1] != id {
		t.Fatalf("accepted identity source witness changed: legacy=%#v bounded=%#v", results[0], results[1])
	}
	if probe.metadataResidency.work.used != 0 {
		t.Fatal("completed backfill retained its work allowance")
	}
}
