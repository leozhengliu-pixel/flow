package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func auditWrites(t *testing.T, repo *SQLiteStore) func() map[string]int {
	t.Helper()
	ctx := context.Background()
	if _, err := repo.db.ExecContext(ctx, `CREATE TABLE write_audit (table_name TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"issue_records", "issue_label_records", "issue_permission_records", "issue_subscriber_records", "issue_attribute_records", "workspace_metadata_records", "workspace_states", "workspace_content_records"} {
		for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
			if _, err := repo.db.ExecContext(ctx, fmt.Sprintf("CREATE TRIGGER audit_%s_%s AFTER %s ON %s BEGIN INSERT INTO write_audit VALUES('%s'); END", table, op, op, table, table)); err != nil {
				t.Fatal(err)
			}
		}
	}
	return func() map[string]int {
		t.Helper()
		rows, err := repo.db.QueryContext(ctx, "SELECT table_name,COUNT(*) FROM write_audit GROUP BY table_name")
		if err != nil {
			t.Fatal(err)
		}
		result := map[string]int{}
		for rows.Next() {
			var key string
			var n int
			if err := rows.Scan(&key, &n); err != nil {
				t.Fatal(err)
			}
			result[key] = n
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if _, err := repo.db.ExecContext(ctx, "DELETE FROM write_audit"); err != nil {
			t.Fatal(err)
		}
		return result
	}
}

func TestUnchangedImportAndIndexDeltasDoNotRewriteRows(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx := context.Background()
	data := repo.Bootstrap()
	key := data.Workspace.URLKey
	issues := make([]domain.Issue, 100)
	for i := range issues {
		issue := data.Issues[0]
		issue.ID = fmt.Sprintf("write-%03d", i)
		issue.Identifier = fmt.Sprintf("WRITE-%d", i)
		issue.SubscriberIDs = []string{data.Viewer.ID}
		issue.Labels = []domain.IssueLabel{{ID: "one", Name: "One"}}
		issue.Permissions = []domain.IssuePermission{{SubjectType: "user", SubjectID: data.Viewer.ID, Role: "viewer"}}
		issues[i] = issue
	}
	if err := repo.ImportIssues(ctx, key, issues); err != nil {
		t.Fatal(err)
	}
	writes := auditWrites(t, repo)
	if err := repo.ImportIssues(ctx, key, issues); err != nil {
		t.Fatal(err)
	}
	if changes := writes(); len(changes) != 0 {
		t.Fatalf("no-op import wrote rows: %+v", changes)
	}
	issues[0].Title = "Only the title changes"
	if err := repo.ImportIssues(ctx, key, issues); err != nil {
		t.Fatal(err)
	}
	if changes := writes(); !reflect.DeepEqual(changes, map[string]int{"issue_records": 1}) {
		t.Fatalf("title edit rewrote indexes: %+v", changes)
	}
	issues[0].Labels = append(issues[0].Labels, domain.IssueLabel{ID: "two", Name: "Two"})
	if err := repo.ImportIssues(ctx, key, issues[:1]); err != nil {
		t.Fatal(err)
	}
	if changes := writes(); !reflect.DeepEqual(changes, map[string]int{"issue_records": 1, "issue_label_records": 1}) {
		t.Fatalf("adding label rewrote unchanged indexes: %+v", changes)
	}
}

func TestMetadataPersistsOnlyChangedEntitiesAndPreservesReloadOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flow.db")
	repo, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { repo.Close() }()
	ctx := context.Background()
	data := repo.Bootstrap()
	key := data.Workspace.URLKey
	if err := repo.MutateWorkspace(ctx, key, "project.created", "large-metadata", nil, func(data *domain.Bootstrap) error {
		for i := 0; i < 100; i++ {
			project := data.Projects[0]
			project.ID = fmt.Sprintf("project-%03d", i)
			project.Description = strings.Repeat("p", 2048)
			data.Projects = append(data.Projects, project)
		}
		data.ImportJobs = append(data.ImportJobs, domain.ImportJob{ID: "input", Status: "mapping", Rows: []map[string]string{{"title": strings.Repeat("r", 20000)}}, CreatedAt: time.Now().UTC()})
		data.ProjectUpdates["project-000"] = []domain.ProjectUpdate{{ID: "old", Body: "Old update", CreatedAt: time.Now().UTC()}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	writes := auditWrites(t, repo)
	if err := repo.MutateWorkspace(ctx, key, "project.updated", "project-000", nil, func(data *domain.Bootstrap) error {
		for i := range data.Projects {
			if data.Projects[i].ID == "project-000" {
				data.Projects[i].Name = "Only one changed"
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	changes := writes()
	if changes["workspace_metadata_records"] != 1 || changes["workspace_states"] != 0 || changes["workspace_content_records"] != 0 || changes["issue_records"] != 0 {
		t.Fatalf("project edit amplified: %+v", changes)
	}
	if err := repo.MutateWorkspace(ctx, key, "project.update.created", "new", nil, func(data *domain.Bootstrap) error {
		data.ProjectUpdates["project-000"] = append([]domain.ProjectUpdate{{ID: "new", Body: "New update", CreatedAt: time.Now().UTC()}}, data.ProjectUpdates["project-000"]...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if changes := writes(); changes["workspace_metadata_records"] != 1 || changes["workspace_states"] != 0 {
		t.Fatalf("adding update rewrote history: %+v", changes)
	}
	before := repo.Bootstrap()
	if err := repo.ReloadAllWorkspaces(ctx); err != nil {
		t.Fatal(err)
	}
	after := repo.Bootstrap()
	if !reflect.DeepEqual(before.Projects, after.Projects) || !reflect.DeepEqual(before.ProjectUpdates, after.ProjectUpdates) || len(after.ImportJobs[0].Rows) != 1 {
		t.Fatal("reload lost ordered metadata or input")
	}
	repo.Close()
	repo, err = OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	after = repo.Bootstrap()
	if !reflect.DeepEqual(before.Projects, after.Projects) || !reflect.DeepEqual(before.ProjectUpdates, after.ProjectUpdates) || len(after.ImportJobs[0].Rows) != 1 {
		t.Fatal("restart lost metadata or import input")
	}
	var raw []byte
	if err := repo.db.QueryRowContext(ctx, "SELECT data FROM workspace_states WHERE workspace_key=?", key).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if len(raw) > 10000 || strings.Contains(string(raw), strings.Repeat("r", 100)) {
		t.Fatal("uploaded source remains inside root metadata")
	}
	if err := repo.MutateWorkspace(ctx, key, "import.committed", "input", nil, func(data *domain.Bootstrap) error {
		data.ImportJobs[0].Status = "completed"
		data.ImportJobs[0].Rows = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var inputs int
	if err := repo.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM workspace_content_records WHERE kind='import_input'").Scan(&inputs); err != nil {
		t.Fatal(err)
	}
	if inputs != 0 {
		t.Fatal("completed CSV import retained its discarded source file")
	}
}

func TestLegacyPrependDoesNotRenumberExistingIssueRows(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx := context.Background()
	data := repo.Bootstrap()
	key := data.Workspace.URLKey
	writes := auditWrites(t, repo)
	issue := data.Issues[0]
	issue.ID = "prepended"
	issue.Identifier = "PRE-10000"
	issue.Number = 10000
	if err := repo.MutateWorkspace(ctx, key, "issue.created", issue.ID, nil, func(data *domain.Bootstrap) error {
		data.Issues = append([]domain.Issue{issue}, data.Issues...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if changes := writes(); changes["issue_records"] > 2 {
		t.Fatalf("prepend rewrote existing rows: %+v", changes)
	}
	actual := repo.Bootstrap()
	if actual.Issues[0].ID != issue.ID {
		t.Fatal("prepend order changed")
	}
	if err := repo.MutateWorkspace(ctx, key, "issue.deleted", issue.ID, nil, func(data *domain.Bootstrap) error { data.Issues = data.Issues[1:]; return nil }); err != nil {
		t.Fatal(err)
	}
	if changes := writes(); changes["issue_records"] != 1 {
		t.Fatalf("delete renumbered existing rows: %+v", changes)
	}
}

func TestTransactionWriteBudgetRollsBackEvenWhenCallerIgnoresFailure(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx := context.Background()
	if _, err := repo.db.ExecContext(ctx, "CREATE TABLE budget_probe(value TEXT)"); err != nil {
		t.Fatal(err)
	}
	tx, err := repo.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	tx.maxTransactionBytes = 1024
	if _, err := tx.ExecContext(ctx, "INSERT INTO budget_probe(value) VALUES(?)", "small"); err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(ctx, "UPDATE budget_probe SET value=?", strings.Repeat("x", 2048))
	if !errors.Is(err, ErrTransactionWriteBudget) {
		t.Fatal(err)
	}
	if err := tx.Commit(); !errors.Is(err, ErrTransactionWriteBudget) {
		t.Fatalf("oversized transaction committed: %v", err)
	}
	var count int
	if err := repo.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM budget_probe").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("partial transaction survived")
	}
}

func TestMetadataMigrationRestoresLegacyJSONWithoutMutatingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flow.db")
	repo, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	data := repo.Bootstrap()
	data = collectionMetadata(data)
	data.ImportJobs = []domain.ImportJob{{ID: "legacy-input", Status: "mapping", Rows: []map[string]string{{"Title": "Legacy"}}, CreatedAt: time.Now().UTC()}}
	data.MigrationJobs = []domain.MigrationJob{{ID: "legacy-migration", Status: "mapping", Bundle: json.RawMessage(`{"version":1}`), CreatedAt: time.Now().UTC()}}
	raw, _ := json.Marshal(data)
	if _, err := repo.db.ExecContext(ctx, "DELETE FROM workspace_metadata_records"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(ctx, "UPDATE workspace_states SET data=?", raw); err != nil {
		t.Fatal(err)
	}
	repo.Close()
	repo, err = OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	actual := repo.Bootstrap()
	if !reflect.DeepEqual(actual.Projects, data.Projects) || !reflect.DeepEqual(actual.Teams, data.Teams) {
		t.Fatal("legacy migration lost entities")
	}
	if len(actual.ImportJobs[0].Rows) != 1 || string(actual.MigrationJobs[0].Bundle) != `{"version":1}` {
		t.Fatal("legacy migration lost uploaded source")
	}
}

func TestMetadataRawObjectRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "raw-metadata.db")
	repo, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	key := repo.Bootstrap().Workspace.URLKey
	values := map[string]any{}
	for i := 0; i < 200; i++ {
		values[fmt.Sprintf("field-%03d", i)] = map[string]any{"visible": i%2 == 0, "width": i + 1}
	}
	raw, _ := json.Marshal(values)
	err = repo.MutateWorkspace(t.Context(), key, "project_display_default.updated", "defaults", nil, func(data *domain.Bootstrap) error {
		data.ProjectDisplayDefault = raw
		return nil
	})
	repo.Close()
	if err != nil {
		t.Fatal(err)
	}
	repo, err = OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	metadata, ok := repo.WorkspaceMetadata(key)
	if !ok || string(metadata.ProjectDisplayDefault) != string(raw) {
		t.Fatal("split raw object did not survive restart")
	}
}

func TestImportBoundsRejectBeforeWriting(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx := context.Background()
	data := repo.Bootstrap()
	issue := data.Issues[0]
	writes := auditWrites(t, repo)
	for _, batch := range [][]domain.Issue{{issue, issue}, make([]domain.Issue, 1001), {{ID: ""}}} {
		if err := repo.ImportIssues(ctx, data.Workspace.URLKey, batch); err == nil {
			t.Fatal("invalid import accepted")
		}
	}
	issue.Description = strings.Repeat("x", 1<<20)
	if err := repo.ImportIssues(ctx, data.Workspace.URLKey, []domain.Issue{issue}); err == nil {
		t.Fatal("oversized issue accepted")
	}
	if changed := writes(); len(changed) != 0 {
		t.Fatalf("rejected imports wrote rows: %+v", changed)
	}
}

func TestDisplayChangesDoNotRewriteReferencedIssuesOrComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flow.db")
	repo, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { repo.Close() }()
	ctx := context.Background()
	data := repo.Bootstrap()
	key := data.Workspace.URLKey
	issue := data.Issues[0]
	if err := repo.MutateWorkspace(ctx, key, "comment.created", "display-comment", nil, func(data *domain.Bootstrap) error {
		data.Comments[issue.ID] = append(data.Comments[issue.ID], domain.Comment{ID: "display-comment", Body: "Preserved body", User: issue.Creator, CreatedAt: time.Now().UTC()})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	writes := auditWrites(t, repo)
	if err := repo.MutateWorkspace(ctx, key, "user.updated", issue.Creator.ID, nil, func(data *domain.Bootstrap) error {
		for i := range data.Users {
			if data.Users[i].ID == issue.Creator.ID {
				data.Users[i].DisplayName = "Renamed person"
			}
		}
		for i := range data.Teams {
			if data.Teams[i].ID == issue.Team.ID {
				data.Teams[i].Name = "Renamed team"
			}
		}
		for i := range data.States {
			if data.States[i].ID == issue.State.ID {
				data.States[i].Name = "Renamed status"
			}
		}
		for i := range data.Labels {
			if data.Labels[i].ID == issue.Labels[0].ID {
				data.Labels[i].Name = "Renamed label"
			}
		}
		refreshIssueReferences(data)
		refreshDisplayReferences(data)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if changed := writes(); changed["issue_records"] != 0 || changed["workspace_content_records"] != 0 {
		t.Fatalf("display update cascaded to stored history: %+v", changed)
	}
	verify := func() {
		t.Helper()
		current, err := repo.IssueRecord(ctx, key, issue.ID)
		if err != nil || current.Creator.DisplayName != "Renamed person" || current.Team.Name != "Renamed team" || current.State.Name != "Renamed status" || current.Labels[0].Name != "Renamed label" {
			t.Fatalf("stale references: %+v %v", current, err)
		}
		comments, _, err := repo.IssueContent(ctx, key, issue.ID)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, comment := range comments {
			if comment.ID == "display-comment" {
				found = comment.User.DisplayName == "Renamed person" && comment.Body == "Preserved body"
			}
		}
		if !found {
			t.Fatal("comment author display did not refresh")
		}
	}
	verify()
	repo.Close()
	repo, err = OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	verify()
}

func TestTitleMutationDoesNotCopyUnchangedDescriptionIntoDomainEvent(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx := context.Background()
	data := repo.Bootstrap()
	issue := data.Issues[0]
	issue.Description = strings.Repeat("large description", 4096)
	if err := repo.ImportIssues(ctx, data.Workspace.URLKey, []domain.Issue{issue}); err != nil {
		t.Fatal(err)
	}
	var event domain.DomainEvent
	repo.SetWebhookSink(func(_ string, value domain.DomainEvent) { event = value })
	result, err := repo.UpdateIssueRecord(ctx, data.Workspace.URLKey, issue.ID, nil, IssueMutationScope{}, func(_ *domain.Bootstrap, current *domain.Issue) error { current.Title = "Changed title"; return nil })
	if err != nil || result.Description != issue.Description {
		t.Fatal("title update lost description")
	}
	if len(event.Payload) > 1024 || len(event.PreviousValues) != 0 {
		t.Fatalf("title update copied an unchanged document into the event: payload=%d previous=%d", len(event.Payload), len(event.PreviousValues))
	}
	var changes map[string]any
	if json.Unmarshal(event.Payload, &changes) != nil || changes["title"] != "Changed title" {
		t.Fatal("event lost the actual change")
	}
}
