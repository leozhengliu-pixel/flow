package store

import (
	"encoding/json"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestProjectReminderFlagsBatch(t *testing.T) {
	f := journalScaleDatabase(t)
	workspace := f.data.Workspace.URLKey
	now := time.Now().UTC()
	for index, row := range []struct {
		resource  string
		project   string
		kind      string
		status    string
		deletedAt *time.Time
	}{
		{"one", "one", "projectUpdateReminder", "unread", nil},
		{"", "one", "projectUpdateDueReminder", "read", nil},
		{"two", "two", "projectUpdateDueReminder", "read", nil},
		{"two", "two", "projectUpdateReminder", "read", &now},
		{"two", "one", "projectUpdateReminder", "read", nil},
		{"three", "three", "projectUpdateReminder", "archived", nil},
		{"other", "other", "projectUpdateReminder", "unread", nil},
	} {
		raw, err := json.Marshal(domain.Notification{ProjectID: row.project, Type: row.kind, DeletedAt: row.deletedAt})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.ExecContext(t.Context(), `INSERT INTO workspace_content_records(workspace_key,kind,resource_id,id,created_at,data,status) VALUES(?,'notification',?,?,?,?,?)`,
			workspace, row.resource, string(rune('a'+index)), now.Format(time.RFC3339Nano), raw, row.status); err != nil {
			t.Fatal(err)
		}
	}
	queries := 0
	ctx := WithSQLStatementObserver(t.Context(), func(string) { queries++ })
	flags, err := f.ProjectReminderFlags(ctx, workspace, []string{"one", "two", "three"})
	if err != nil {
		t.Fatal(err)
	}
	if queries != 1 || len(flags) != 3 || flags["one"] != (ProjectUpdateReminderFlags{Missing: true, Due: true}) ||
		flags["two"] != (ProjectUpdateReminderFlags{Due: true}) || flags["three"] != (ProjectUpdateReminderFlags{}) {
		t.Fatalf("expected both indexed and legacy reminders in one statement; statements=%d", queries)
	}
}
