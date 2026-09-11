package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func workflowDeletionFixture(t testing.TB, affected int) (*SQLiteStore, domain.WorkflowState, domain.WorkflowState) {
	t.Helper()
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "state-delete.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	data := repo.Bootstrap()
	old := domain.WorkflowState{ID: "state-delete", Name: "Remove", TeamID: data.Teams[0].ID, Type: "started"}
	next := domain.WorkflowState{ID: "state-replace", Name: "Replacement", TeamID: old.TeamID, Type: "unstarted"}
	if err := repo.MutateWorkspace(context.Background(), data.Workspace.URLKey, "workflow_state.created", old.ID, nil, func(metadata *domain.Bootstrap) error {
		metadata.States = append(metadata.States, old, next)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	items := []domain.Issue{}
	for i := 0; i < affected; i++ {
		issue := data.Issues[0]
		issue.ID = fmt.Sprintf("replace-%04d", i)
		issue.Identifier = fmt.Sprintf("REPLACE-%d", i)
		issue.State = old
		issue.SortOrder = float64(i / 3)
		items = append(items, issue)
	}
	if len(items) > 0 {
		if err := repo.ImportIssues(context.Background(), data.Workspace.URLKey, items); err != nil {
			t.Fatal(err)
		}
	}
	return repo, old, next
}

func workflowDeletionPlan(old, next domain.WorkflowState) func(*domain.Bootstrap, bool) (domain.WorkflowState, *domain.WorkflowState, error) {
	return func(data *domain.Bootstrap, inUse bool) (domain.WorkflowState, *domain.WorkflowState, error) {
		if len(data.Issues)+len(data.Projects)+len(data.Users)+len(data.Comments)+len(data.Activities) > 0 {
			return old, nil, errors.New("unbounded workflow projection")
		}
		if !slices.ContainsFunc(data.States, func(state domain.WorkflowState) bool { return state.ID == old.ID }) {
			return old, nil, errors.New("state not found")
		}
		return old, &next, nil
	}
}

func TestWorkflowDeletionUpdatesOnlyAffectedIssuesAcrossBatches(t *testing.T) {
	repo, old, next := workflowDeletionFixture(t, 225)
	if _, err := repo.db.ExecContext(t.Context(), `UPDATE issue_records SET data='corrupt unrelated issue' WHERE state_id<>?`, old.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(t.Context(), `UPDATE workspace_content_records SET data='corrupt unrelated history'`); err != nil {
		t.Fatal(err)
	}
	writes := auditWrites(t, repo)
	if err := repo.DeleteWorkflowStateRecords(t.Context(), "test-workspace", old.TeamID, workflowDeletionPlan(old, next)); err != nil {
		t.Fatal(err)
	}
	changes := writes()
	if changes["issue_records"] != 225 || changes["workspace_metadata_records"] != 1 || changes["workspace_states"] != 0 || changes["workspace_content_records"] != 0 {
		t.Fatalf("unrelated workflow write amplification: %v", changes)
	}
	var count int
	if err := repo.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM issue_records WHERE workspace_key='test-workspace' AND team_id=? AND state_id=?`, old.TeamID, old.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("old state retained: count=%d err=%v", count, err)
	}
	for _, id := range []string{"replace-0000", "replace-0100", "replace-0224"} {
		issue, err := repo.IssueRecord(t.Context(), "test-workspace", id)
		if err != nil || issue.State.ID != next.ID || issue.Version != 2 {
			t.Fatalf("batch record %s not migrated: state=%s version=%d err=%v", id, issue.State.ID, issue.Version, err)
		}
	}
	metadata, _ := repo.WorkspaceMetadata("test-workspace")
	if slices.ContainsFunc(metadata.States, func(state domain.WorkflowState) bool { return state.ID == old.ID }) {
		t.Fatal("deleted state remained in cache")
	}
}

func TestWorkflowDeletionRollsBackPriorBatches(t *testing.T) {
	repo, old, next := workflowDeletionFixture(t, 125)
	if _, err := repo.db.ExecContext(t.Context(), `CREATE TRIGGER fail_second_batch BEFORE UPDATE ON issue_records WHEN OLD.id='replace-0120' BEGIN SELECT RAISE(ABORT,'injected second batch failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteWorkflowStateRecords(t.Context(), "test-workspace", old.TeamID, workflowDeletionPlan(old, next)); err == nil {
		t.Fatal("injected failure ignored")
	}
	var count int
	if err := repo.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM issue_records WHERE workspace_key='test-workspace' AND team_id=? AND state_id=?`, old.TeamID, old.ID).Scan(&count); err != nil || count != 125 {
		t.Fatalf("partial migration: count=%d err=%v", count, err)
	}
	metadata, _ := repo.WorkspaceMetadata("test-workspace")
	if !slices.ContainsFunc(metadata.States, func(state domain.WorkflowState) bool { return state.ID == old.ID }) {
		t.Fatal("failed deletion changed cached state")
	}
}

func TestWorkflowDeletionDoesNotTouchAnotherTeamUsingSameStateID(t *testing.T) {
	repo, old, next := workflowDeletionFixture(t, 1)
	if _, err := repo.db.ExecContext(t.Context(), `UPDATE issue_records SET team_id='other-team',data='invalid other-team JSON' WHERE state_id<>?`, old.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(t.Context(), `UPDATE issue_records SET state_id=? WHERE team_id='other-team'`, old.ID); err != nil {
		t.Fatal(err)
	}
	writes := auditWrites(t, repo)
	if err := repo.DeleteWorkflowStateRecords(t.Context(), "test-workspace", old.TeamID, workflowDeletionPlan(old, next)); err != nil {
		t.Fatal(err)
	}
	if got := writes()["issue_records"]; got != 1 {
		t.Fatalf("other team rewritten: %d writes", got)
	}
}

func TestWorkflowDeletionUsesTeamStateIndex(t *testing.T) {
	repo, old, _ := workflowDeletionFixture(t, 1)
	for _, query := range []string{`SELECT COUNT(*) FROM issue_records WHERE workspace_key=? AND team_id=? AND state_id=?`, `SELECT data,sort_order,id FROM issue_records WHERE workspace_key=? AND team_id=? AND state_id=? ORDER BY sort_order,id LIMIT 100`} {
		rows, err := repo.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+query, "test-workspace", old.TeamID, old.ID)
		if err != nil {
			t.Fatal(err)
		}
		plan := ""
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			plan += detail + "\n"
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(plan, "issue_records_team_state_order_idx") || !strings.Contains(plan, "workspace_key=? AND team_id=? AND state_id=?") || strings.Contains(plan, "TEMP B-TREE") {
			t.Fatalf("unbounded workflow SQL plan: %s", plan)
		}
	}
}

func TestWorkflowDeletionMaintainsLifecycleDates(t *testing.T) {
	for _, targetType := range []string{"started", "completed", "canceled", "unstarted", "backlog"} {
		t.Run(targetType, func(t *testing.T) {
			repo, old, replacement := workflowDeletionFixture(t, 2)
			replacement.Type = targetType
			if err := repo.MutateWorkspace(t.Context(), "test-workspace", "workflow_state.updated", replacement.ID, nil, func(data *domain.Bootstrap) error {
				for i := range data.States {
					if data.States[i].ID == replacement.ID {
						data.States[i] = replacement
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			before := time.Now().UTC().Add(-24 * time.Hour)
			for _, id := range []string{"replace-0000", "replace-0001"} {
				issue, err := repo.IssueRecord(t.Context(), "test-workspace", id)
				if err != nil {
					t.Fatal(err)
				}
				issue.CompletedAt, issue.CanceledAt, issue.StatusChangedAt = &before, &before, &before
				issue.StartedAt = nil
				if id == "replace-0001" {
					issue.StartedAt = &before
				}
				if err := repo.ImportIssues(t.Context(), "test-workspace", []domain.Issue{issue}); err != nil {
					t.Fatal(err)
				}
			}
			writes := auditWrites(t, repo)
			if err := repo.DeleteWorkflowStateRecords(t.Context(), "test-workspace", old.TeamID, workflowDeletionPlan(old, replacement)); err != nil {
				t.Fatal(err)
			}
			if changed := writes(); changed["issue_records"] != 2 || changed["workspace_metadata_records"] != 1 || changed["workspace_states"] != 0 || changed["workspace_content_records"] != 0 {
				t.Fatalf("lifecycle fix broadened writes: %v", changed)
			}
			for _, id := range []string{"replace-0000", "replace-0001"} {
				issue, err := repo.IssueRecord(t.Context(), "test-workspace", id)
				if err != nil {
					t.Fatal(err)
				}
				if issue.State.ID != replacement.ID || issue.State.Type != targetType {
					t.Fatal("replacement state does not match its lifecycle dates")
				}
				if issue.StatusChangedAt == nil || !issue.StatusChangedAt.Equal(issue.UpdatedAt) {
					t.Fatal("missing consistent status transition time")
				}
				if id == "replace-0001" {
					if issue.StartedAt == nil || !issue.StartedAt.Equal(before) {
						t.Fatal("replacement discarded original start date")
					}
				} else if targetType == "started" {
					if issue.StartedAt == nil || !issue.StartedAt.Equal(issue.UpdatedAt) {
						t.Fatal("entering started did not set start date")
					}
				} else if issue.StartedAt != nil {
					t.Fatal("replacement fabricated start date")
				}
				if targetType == "completed" {
					if issue.CompletedAt == nil || !issue.CompletedAt.Equal(issue.UpdatedAt) || issue.CanceledAt != nil {
						t.Fatal("completion dates inconsistent")
					}
				} else if targetType == "canceled" {
					if issue.CanceledAt == nil || !issue.CanceledAt.Equal(issue.UpdatedAt) || issue.CompletedAt != nil {
						t.Fatal("cancellation dates inconsistent")
					}
				} else if issue.CompletedAt != nil || issue.CanceledAt != nil {
					t.Fatal("reopened issue kept terminal dates")
				}
			}
		})
	}
}

func BenchmarkUnusedWorkflowStateDeletion(b *testing.B) {
	for _, count := range []int{0, 75000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			repo, old, next := workflowDeletionFixture(b, 0)
			if _, err := repo.db.ExecContext(context.Background(), `WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<?) INSERT INTO issue_records(workspace_key,id,identifier,team_id,state_id,state_type,priority,assignee_id,project_id,creator_id,cycle_id,parent_id,sort_order,title,archived,version,created_at,updated_at,collection_order,data) SELECT 'test-workspace','scale-'||x,'S-'||x,'team_test','unrelated','',0,'','','','','',0,'',0,0,'','',x,'{}' FROM n WHERE x<=?`, count, count); err != nil {
				b.Fatal(err)
			}
			plan := workflowDeletionPlan(old, next)
			raw, _ := json.Marshal(old)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				if _, err := repo.db.ExecContext(context.Background(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES('test-workspace','states',?,0,?) ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET data=excluded.data`, old.ID, raw); err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
				if err := repo.DeleteWorkflowStateRecords(context.Background(), "test-workspace", old.TeamID, plan); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
