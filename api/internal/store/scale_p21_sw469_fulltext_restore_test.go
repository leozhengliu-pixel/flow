package store

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// Releases before the upsert fixes lost full-text index entries on MySQL:
// a multi-row INSERT ... ON DUPLICATE KEY UPDATE that changes rows drops
// them from the InnoDB FULLTEXT index while their text stays correct, so no
// repair rewrites them. Startup rebuilds both search indexes once
// (issueSearchRestoreMigrationVersion, metadataSearchRestoreMigrationVersion)
// and every document is found again; a later startup rebuilds nothing.
func TestScaleP21SW469StartupRestoresLostFullTextEntries(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	if f.dialect != "mysql" {
		t.Skip("the restore rebuilds MySQL FULLTEXT indexes only")
	}
	ctx, workspace, prefix := t.Context(), f.data.Workspace.URLKey, idgen.New("sw469-")
	at := time.Date(2026, 11, 18, 1, 2, 3, 0, time.UTC)
	var issues []domain.Issue
	var issueIDs, projectIDs []string
	for i := range 12 {
		issues = append(issues, domain.Issue{
			ID: fmt.Sprintf("%s%02d", prefix, i), Identifier: fmt.Sprintf("SWR-%d", 6000+i), Number: 6000 + i, Version: 1,
			Title: fmt.Sprintf("Kestrel02 Falcon02 note%04d", i), Team: f.data.Teams[0],
			State: domain.WorkflowState{ID: "state_backlog", Name: "Backlog", Type: "backlog"}, Creator: f.data.Viewer,
			CreatedAt: at, UpdatedAt: at.Add(time.Duration(i) * time.Second),
		})
		issueIDs = append(issueIDs, issues[i].ID)
	}
	if err := f.ImportIssues(ctx, workspace, issues); err != nil {
		t.Fatal(err)
	}
	if err := f.MutateWorkspace(WithMetadataFields(ctx, "projects"), workspace, "scale_p21_sw469.search", "", nil, func(data *domain.Bootstrap) error {
		for i := range 12 {
			id := fmt.Sprintf("%sp%02d", prefix, i)
			projectIDs = append(projectIDs, id)
			data.Projects = append(data.Projects, domain.Project{ID: id, Name: fmt.Sprintf("Kestrel02 Falcon02 plan%04d", i)})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	foundIssues := func() []string {
		t.Helper()
		var ids []string
		if err := f.SearchIssueCandidateTerms(ctx, IssueRecordQuery{Workspace: workspace, Archived: "all"}, []string{"Kestrel02"}, nil, 100, func(issue domain.Issue) error {
			ids = append(ids, issue.ID)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		slices.Sort(ids)
		return ids
	}
	foundProjects := func() []string {
		t.Helper()
		selection, value := f.metadataTermSelection("Kestrel02")
		rows, err := f.db.QueryContext(ctx, selection, workspace, "projects", value)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(id, prefix) {
				ids = append(ids, id)
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		slices.Sort(ids)
		return ids
	}
	all := func(stage string) {
		t.Helper()
		if issues, projects := foundIssues(), foundProjects(); !slices.Equal(issues, issueIDs) || !slices.Equal(projects, projectIDs) {
			t.Fatalf("%s: found %d of %d issues and %d of %d projects", stage, len(issues), len(issueIDs), len(projects), len(projectIDs))
		}
	}
	all("written")
	// An earlier release's writer: one multi-row upsert changes several
	// documents back to their correct text, which drops them from the index.
	lose := func() {
		t.Helper()
		for _, table := range []struct{ name, key, field string }{{"issue_search_documents", "issue_id", ""}, {"metadata_search_documents", "record_key", "projects"}} {
			keys := []string{}
			for i := 1; i < 12; i += 3 {
				if table.field == "" {
					keys = append(keys, issueIDs[i])
				} else {
					keys = append(keys, projectIDs[i])
				}
			}
			filter := "workspace_key=?"
			args := []any{workspace}
			if table.field != "" {
				filter += " AND field=?"
				args = append(args, table.field)
			}
			clause, values := bindList(table.key, keys)
			rows, err := f.db.QueryContext(ctx, `SELECT `+table.key+`,content FROM `+table.name+` WHERE `+filter+` AND `+clause, append(append([]any{}, args...), values...)...)
			if err != nil {
				t.Fatal(err)
			}
			tuples, upsertArgs := []string{}, []any{}
			for rows.Next() {
				var key, content string
				if err := rows.Scan(&key, &content); err != nil {
					t.Fatal(err)
				}
				if table.field == "" {
					tuples, upsertArgs = append(tuples, "(?,?,?)"), append(upsertArgs, workspace, key, content)
				} else {
					tuples, upsertArgs = append(tuples, "(?,?,?,?)"), append(upsertArgs, workspace, table.field, key, content)
				}
			}
			rows.Close()
			if len(tuples) != len(keys) {
				t.Fatalf("%s: read %d of %d documents", table.name, len(tuples), len(keys))
			}
			if _, err := f.db.ExecContext(ctx, `UPDATE `+table.name+` SET content=CONCAT(content,' interim') WHERE `+filter+` AND `+clause, append(append([]any{}, args...), values...)...); err != nil {
				t.Fatal(err)
			}
			columns := "workspace_key," + table.key + ",content"
			if table.field != "" {
				columns = "workspace_key,field," + table.key + ",content"
			}
			if _, err := f.db.ExecContext(ctx, `INSERT INTO `+table.name+`(`+columns+`) VALUES `+strings.Join(tuples, ",")+` ON DUPLICATE KEY UPDATE content=VALUES(content)`, upsertArgs...); err != nil {
				t.Fatal(err)
			}
		}
		if issues, projects := foundIssues(), foundProjects(); len(issues) == len(issueIDs) || len(projects) == len(projectIDs) {
			t.Fatalf("the multi-row upsert kept every entry (%d issues, %d projects): nothing to restore", len(issues), len(projects))
		}
	}
	lose()
	start := func() {
		t.Helper()
		repo, err := OpenDatabase(DatabaseConfig{Driver: "mysql", URL: os.Getenv("FLOW_TEST_DATABASE_URL"), MaxOpenConns: 4})
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.Close(); err != nil {
			t.Fatal(err)
		}
	}
	recorded := func() int {
		t.Helper()
		var count int
		if err := f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version IN (?,?)`, issueSearchRestoreMigrationVersion, metadataSearchRestoreMigrationVersion).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	// A database of an earlier release has not recorded the restore.
	if _, err := f.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version IN (?,?)`, issueSearchRestoreMigrationVersion, metadataSearchRestoreMigrationVersion); err != nil {
		t.Fatal(err)
	}
	start()
	if count := recorded(); count != 2 {
		t.Fatalf("startup recorded %d of 2 restore rebuilds", count)
	}
	all("restored by startup")
	// A later startup does not rebuild again: entries lost now stay lost.
	lose()
	start()
	if count := recorded(); count != 2 {
		t.Fatalf("second startup left %d restore records", count)
	}
	if issues, projects := foundIssues(), foundProjects(); len(issues) == len(issueIDs) || len(projects) == len(projectIDs) {
		t.Fatalf("second startup rebuilt an index: found %d issues and %d projects", len(issues), len(projects))
	}
}
