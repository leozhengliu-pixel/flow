package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// Recurring issue lookups (the scheduler's due scan and the team's Recurring
// issues list) read the sparse nextOccurrenceAt attribute through its index,
// never issue payloads, and need no sort; archived issues are skipped through
// a primary key lookup of their record.
func TestRecurringIssueIDsUseTheAttributeIndex(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "recurring.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	for _, dueBy := range []bool{false, true} {
		args := []any{"test-workspace"}
		if dueBy {
			args = append(args, "2026-10-01T00:00:00.000000000Z")
		}
		rows, err := repo.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+recurringIssueIDQuery(dueBy), append(args, 100)...)
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
		rows.Close()
		if !strings.Contains(plan, "COVERING INDEX issue_attributes_value_idx") || strings.Contains(plan, "TEMP B-TREE") || strings.Contains(plan, "SCAN") ||
			strings.Count(plan, "issue_records") != 1 || !strings.Contains(plan, "sqlite_autoindex_issue_records_1 (workspace_key=? AND id=?)") {
			t.Fatalf("recurring lookup plan (dueBy=%v): %s", dueBy, plan)
		}
	}
	// Only issues with a schedule are listed, earliest next occurrence first.
	key := repo.Bootstrap().Workspace.URLKey
	late, early := time.Date(2026, 10, 9, 0, 1, 0, 0, time.UTC), time.Date(2026, 10, 2, 0, 1, 0, 0, time.UTC)
	var lateID, earlyID string
	if err := repo.MutateWorkspace(t.Context(), key, "test.recurring", "recurring", nil, func(data *domain.Bootstrap) error {
		data.Issues[0].Recurrence, data.Issues[0].NextOccurrenceAt = "weekly", &late
		data.Issues[1].Recurrence, data.Issues[1].NextOccurrenceAt = "daily", &early
		lateID, earlyID = data.Issues[0].ID, data.Issues[1].ID
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ids, err := repo.RecurringIssueIDs(t.Context(), key, time.Time{}, 0)
	if err != nil || strings.Join(ids, ",") != earlyID+","+lateID {
		t.Fatalf("recurring ids = %v, %v", ids, err)
	}
	ids, err = repo.RecurringIssueIDs(t.Context(), key, time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC), 10)
	if err != nil || strings.Join(ids, ",") != earlyID {
		t.Fatalf("due recurring ids = %v, %v", ids, err)
	}
	// An archived schedule never takes a slot of the due window.
	archivedAt := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if err := repo.MutateWorkspace(t.Context(), key, "test.recurring", "recurring", nil, func(data *domain.Bootstrap) error {
		data.Issues[1].ArchivedAt = &archivedAt
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ids, err = repo.RecurringIssueIDs(t.Context(), key, time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC), 1)
	if err != nil || strings.Join(ids, ",") != lateID {
		t.Fatalf("recurring ids with an archived schedule = %v, %v", ids, err)
	}
}

// Series instances are indexed by (series, occurrence date), and instances
// written before the attribute existed are backfilled once.
func TestRecurrenceInstanceAttribute(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "instances.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	key := repo.Bootstrap().Workspace.URLKey
	var id string
	if err := repo.MutateWorkspace(t.Context(), key, "test.recurring", "recurring", nil, func(data *domain.Bootstrap) error {
		data.Issues[0].RecurrenceSeriesID, data.Issues[0].RecurrenceOccurrence = "issue_series", "2026-10-10"
		id = data.Issues[0].ID
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	lookup := func() string {
		var found string
		err := repo.db.QueryRowContext(t.Context(), `SELECT issue_id FROM issue_attribute_records WHERE workspace_key=? AND field='recurrenceInstance' AND value=?`, key, RecurrenceInstanceKey("issue_series", "2026-10-10")).Scan(&found)
		if err != nil {
			return ""
		}
		return found
	}
	if got := lookup(); got != id {
		t.Fatalf("instance attribute = %q, want %q", got, id)
	}
	// Simulate a record indexed before the attribute existed.
	if _, err := repo.db.ExecContext(t.Context(), `DELETE FROM issue_attribute_records WHERE workspace_key=? AND field='recurrenceInstance'`, key); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(t.Context(), `DELETE FROM recurrence_instance_migrations WHERE workspace_key=?`, key); err != nil {
		t.Fatal(err)
	}
	if err := repo.migrateRecurrenceInstances(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := lookup(); got != id {
		t.Fatalf("backfilled instance attribute = %q, want %q", got, id)
	}
}
