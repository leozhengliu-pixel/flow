package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	"flow/api/internal/domain"
)

func removeTestLabel(id string) func(*domain.Bootstrap) error {
	return func(data *domain.Bootstrap) error {
		if len(data.Labels) != 1 || data.Labels[0].ID != id {
			return errors.New("label not found")
		}
		if len(data.Issues) > 1 || len(data.Projects) > 1 || len(data.Comments)+len(data.Activities)+len(data.Users) != 0 {
			return errors.New("unbounded label projection")
		}
		data.Labels = nil
		for i := range data.Issues {
			data.Issues[i].Labels = slices.DeleteFunc(data.Issues[i].Labels, func(label domain.IssueLabel) bool { return label.ID == id })
			data.Issues[i].SuggestedLabelIDs = slices.DeleteFunc(data.Issues[i].SuggestedLabelIDs, func(value string) bool { return value == id })
		}
		for i := range data.Projects {
			data.Projects[i].LabelIDs = slices.DeleteFunc(data.Projects[i].LabelIDs, func(value string) bool { return value == id })
		}
		data.Favorites = slices.DeleteFunc(data.Favorites, func(value domain.Favorite) bool { return value.ResourceType == "label" && value.ResourceID == id })
		return nil
	}
}

func addDeletionLabel(t testing.TB, repo *SQLiteStore, resource string) domain.IssueLabel {
	t.Helper()
	label := domain.IssueLabel{ID: "delete_target", Name: "Unused label", ResourceType: resource, Scope: "Workspace"}
	if err := repo.MutateWorkspace(context.Background(), "test-workspace", "label.created", label.ID, nil, func(data *domain.Bootstrap) error { data.Labels = append(data.Labels, label); return nil }); err != nil {
		t.Fatal(err)
	}
	return label
}

func TestUnusedProjectLabelDeletionDoesNotReadOrRewriteUnrelatedCollections(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "labels.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	label := addDeletionLabel(t, repo, "project")
	// Unrelated corrupt bodies make accidental workspace hydration fail loudly.
	for _, query := range []string{
		`UPDATE issue_records SET data='invalid issue JSON'`,
		`UPDATE workspace_content_records SET data='invalid discussion JSON'`,
		`UPDATE workspace_metadata_records SET data='invalid project JSON' WHERE field='projects'`,
	} {
		if _, err := repo.db.ExecContext(t.Context(), query); err != nil {
			t.Fatal(err)
		}
	}
	writes := auditWrites(t, repo)
	if err := repo.MutateLabelDeletion(t.Context(), "test-workspace", "label.deleted", label.ID, false, removeTestLabel(label.ID)); err != nil {
		t.Fatal(err)
	}
	changed := writes()
	if changed["workspace_metadata_records"] != 1 || len(changed) != 1 {
		t.Fatalf("unused label touched unrelated rows: %v", changed)
	}
	metadata, _ := repo.WorkspaceMetadata("test-workspace")
	if slices.ContainsFunc(metadata.Labels, func(value domain.IssueLabel) bool { return value.ID == label.ID }) {
		t.Fatal("deleted label remained in cache")
	}
	if err := repo.MutateLabelDeletion(t.Context(), "test-workspace", "label.deleted", label.ID, false, removeTestLabel(label.ID)); err == nil || err.Error() != "label not found" {
		t.Fatalf("missing label did not retain handler validation: %v", err)
	}
}

func TestLabelDeletionUpdatesOnlyAssociatedEntitiesAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "labels.db")
	repo, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { repo.Close() }()
	label := addDeletionLabel(t, repo, "issue")
	data := repo.Bootstrap()
	issue := data.Issues[0]
	issue.ID = "associated-issue"
	issue.Identifier = "DEL-1"
	issue.Labels = []domain.IssueLabel{label}
	issue.SuggestedLabelIDs = []string{label.ID, "keep"}
	suggested := issue
	suggested.ID = "suggested-only"
	suggested.Identifier = "DEL-2"
	suggested.Labels = nil
	if err := repo.ImportIssues(t.Context(), "test-workspace", []domain.Issue{issue, suggested}); err != nil {
		t.Fatal(err)
	}
	project := data.Projects[0]
	project.ID = "associated-project"
	project.LabelIDs = []string{label.ID, "keep"}
	if err := repo.MutateWorkspace(t.Context(), "test-workspace", "project.updated", project.ID, nil, func(next *domain.Bootstrap) error {
		next.Projects = append(next.Projects, project)
		next.Favorites = append(next.Favorites, domain.Favorite{ID: "label-favorite", ResourceType: "label", ResourceID: label.ID})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	writes := auditWrites(t, repo)
	if err := repo.MutateLabelDeletion(t.Context(), "test-workspace", "label.deleted", label.ID, false, removeTestLabel(label.ID)); err != nil {
		t.Fatal(err)
	}
	changed := writes()
	if changed["issue_records"] != 2 || changed["workspace_metadata_records"] != 3 || changed["workspace_states"] != 0 || changed["workspace_content_records"] != 0 {
		t.Fatalf("unexpected cascade write amplification: %v", changed)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	repo, err = OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := repo.Bootstrap()
	for _, item := range updated.Issues {
		if item.ID == issue.ID || item.ID == suggested.ID {
			if len(item.Labels) != 0 || !slices.Equal(item.SuggestedLabelIDs, []string{"keep"}) || item.Version != issue.Version+1 {
				t.Fatalf("stale issue after restart: %s labels=%v suggestions=%v version=%d", item.ID, item.Labels, item.SuggestedLabelIDs, item.Version)
			}
		}
	}
	for _, item := range updated.Projects {
		if item.ID == project.ID && !slices.Equal(item.LabelIDs, []string{"keep"}) {
			t.Fatal("project cascade was not persisted")
		}
	}
	if slices.ContainsFunc(updated.Favorites, func(item domain.Favorite) bool { return item.ID == "label-favorite" }) {
		t.Fatal("favorite survived deletion")
	}
}

func TestLabelDeletionRollbackLeavesAllRowsAndCacheUntouched(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "labels.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	label := addDeletionLabel(t, repo, "project")
	data := repo.Bootstrap()
	project := data.Projects[0]
	project.LabelIDs = []string{label.ID}
	if err := repo.MutateWorkspace(t.Context(), "test-workspace", "project.updated", project.ID, nil, func(next *domain.Bootstrap) error { next.Projects[0] = project; return nil }); err != nil {
		t.Fatal(err)
	}
	validation := errors.New("injected cascade failure")
	err = repo.MutateLabelDeletion(t.Context(), "test-workspace", "label.deleted", label.ID, false, func(next *domain.Bootstrap) error {
		if len(next.Projects) > 0 {
			return validation
		}
		return removeTestLabel(label.ID)(next)
	})
	if !errors.Is(err, validation) {
		t.Fatalf("got %v", err)
	}
	updated := repo.Bootstrap()
	if !slices.ContainsFunc(updated.Labels, func(item domain.IssueLabel) bool { return item.ID == label.ID }) || !slices.Contains(updated.Projects[0].LabelIDs, label.ID) {
		t.Fatal("failed cascade changed state")
	}
}

func BenchmarkUnusedProjectLabelDeletion(b *testing.B) {
	for _, count := range []int{0, 75000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			repo, err := OpenSQLiteTestFixture(filepath.Join(b.TempDir(), "labels.db"))
			if err != nil {
				b.Fatal(err)
			}
			defer repo.Close()
			label := addDeletionLabel(b, repo, "project")
			// Deliberately tiny synthetic rows model cardinality without large disk use.
			if _, err := repo.db.ExecContext(context.Background(), `WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<?) INSERT INTO issue_records(workspace_key,id,identifier,team_id,state_id,state_type,priority,assignee_id,project_id,creator_id,cycle_id,parent_id,sort_order,title,archived,version,created_at,updated_at,collection_order,data) SELECT 'test-workspace','scale-'||x,'S-'||x,'','','',0,'','','','','',0,'',0,0,'','',x,'{}' FROM n WHERE x<=?`, count, count); err != nil {
				b.Fatal(err)
			}
			raw, _ := json.Marshal(label)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				if _, err := repo.db.ExecContext(context.Background(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES('test-workspace','labels',?,0,?) ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET data=excluded.data`, label.ID, raw); err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
				if err := repo.MutateLabelDeletion(context.Background(), "test-workspace", "label.deleted", label.ID, false, removeTestLabel(label.ID)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
