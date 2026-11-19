package main

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// scaleP21SW580Tables are the issue side tables a workflow run may change
// besides the issue, content and metadata rows SW500 already compares.
var scaleP21SW580Tables = []string{
	"issue_records", "issue_label_records", "issue_subscriber_records", "issue_actor_records",
	"issue_permission_records", "issue_identifier_aliases", "issue_attribute_records",
	"issue_attachment_records", "issue_search_documents", "issue_scope_counts",
	"issue_number_sequences", "issue_collection_counts", "issue_records_migrations",
}

// scaleP21SW580Rows dumps every row of those tables for the workspace, in a
// stable order, with the writer's own clock values (inside [started,
// finished], also inside JSON columns) replaced by a marker.
func scaleP21SW580Rows(t *testing.T, database *scaleP04Database, started, finished time.Time) map[string][]string {
	t.Helper()
	low, high := started, finished
	var normalize func(any) any
	normalize = func(value any) any {
		switch item := value.(type) {
		case string:
			if clock, err := time.Parse(time.RFC3339Nano, item); err == nil && !clock.Before(low) && !clock.After(high) {
				return "<now>"
			}
			if trimmed := strings.TrimSpace(item); strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
				var decoded any
				if json.Unmarshal([]byte(trimmed), &decoded) == nil {
					return normalize(decoded)
				}
			}
		case map[string]any:
			for key, child := range item {
				item[key] = normalize(child)
			}
		case []any:
			for index, child := range item {
				item[index] = normalize(child)
			}
		}
		return value
	}
	result := map[string][]string{}
	for _, table := range scaleP21SW580Tables {
		rows := database.query(t, `SELECT * FROM `+table+` WHERE workspace_key=?`, database.key)
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		var dumped []string
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for index := range values {
				pointers[index] = &values[index]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			row := map[string]any{}
			for index, column := range columns {
				value := values[index]
				if raw, ok := value.([]byte); ok {
					value = string(raw)
				}
				// Storage CAS revisions and the first-migration time (an
				// earlier mode's write may have set it) are not part of the
				// contract; the row's presence is.
				if column == "record_version" || column == "migrated_at" {
					continue
				}
				row[column] = normalize(value)
			}
			raw, err := json.Marshal(row)
			if err != nil {
				t.Fatal(err)
			}
			dumped = append(dumped, string(raw))
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		sort.Strings(dumped)
		result[table] = dumped
	}
	return result
}

// scaleP21SW580Installed reads the bounded source's installed workflow and
// user fields, then reloads it from SQL and requires the same. Labels are
// compared through their SQL rows (SW500 metadata): a bounded SQL load derives
// IssueCount without the issue records, so it never equals a warm image.
func scaleP21SW580Installed(t *testing.T, env *scaleP21SW500Env, source *store.SQLiteStore) {
	t.Helper()
	fields := []string{"workflowDefinitions", "workflowRuns", "users", "viewer"}
	installed := scaleP21WriterInstalled(t, env.workspace, source, fields)
	scaleP21SW201ReloadSource(t, env.database, source)
	reloaded := scaleP21WriterInstalled(t, env.workspace, source, fields)
	nul := strings.NewReplacer(`\u0000`, "")
	if a, b := nul.Replace(string(installed)), nul.Replace(string(reloaded)); a != b {
		t.Fatalf("installed image differs from the SQL reload: %s", scaleP21M2Diff(a, b))
	}
}

// TestScaleP21SW580WorkflowPointWriterBounded: the bounded workflow-run
// writer's SQL work does not grow with the workspace's issues and content,
// and it never falls back to the full-image writer for ordinary runs, while
// the result stays the literal writer's (SW500 compares every row).
func TestScaleP21SW580WorkflowPointWriterBounded(t *testing.T) {
	env := scaleP21SW500Setup(t)
	cases := []scaleP21SW500Case{
		{name: "schedule-notify", trigger: "schedule", dueOnly: true, defTrigger: "schedule", schedule: "* * * * *",
			actions: scaleP21SW500Actions(scaleP21SW500Action("notify")), want: "succeeded"},
		{name: "retry-issue-multi-action", trigger: "retry", dueOnly: true, defTrigger: "issueCreated", issue: true,
			actions: scaleP21SW500AllActions, seed: func(env *scaleP21SW500Env, data *domain.Bootstrap) {
				data.WorkflowRuns = []domain.WorkflowRun{env.priorRun("due", env.workflowID, "failed", 1, &env.past)}
			}, want: "succeeded"},
	}
	// extra seeds n unrelated issues, each with a comment and a notification.
	extra := func(n int) func(*domain.Bootstrap) {
		return func(data *domain.Bootstrap) {
			number := 0
			for _, issue := range data.Issues {
				number = max(number, issue.Number)
			}
			template := data.Issues[0]
			for index := range n {
				number++
				issue := template
				issue.ID, issue.Number = fmt.Sprintf("%sbulk-%04d", env.prefix, index), number
				issue.Identifier = fmt.Sprintf("%sBULK-%d", env.prefix, index)
				issue.Title = fmt.Sprintf("Bulk %d", index)
				issue.Description = strings.Repeat("bulk ", 200)
				data.Issues = append(data.Issues, issue)
				data.Comments[issue.ID] = append(data.Comments[issue.ID], domain.Comment{ID: issue.ID + "-comment",
					Body: "bulk comment", User: env.viewer, Reactions: map[string][]string{}, CreatedAt: env.fixtureClock})
				data.Notifications = append(data.Notifications, domain.Notification{ID: issue.ID + "-notification", RecipientID: env.viewer.ID,
					Type: "comment", IssueID: issue.ID, Actor: env.viewer, CreatedAt: env.fixtureClock, UpdatedAt: env.fixtureClock})
			}
		}
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			statements := map[int]int{}
			for _, size := range []int{0, 400} {
				test := test
				seed := test.seed
				test.seed = func(env *scaleP21SW500Env, data *domain.Bootstrap) {
					extra(size)(data)
					if seed != nil {
						seed(env, data)
					}
				}
				env.reset(t, test, nil)
				source := env.source(t, "bounded-warm")
				resourceType, resourceID := "", ""
				if test.issue {
					resourceType, resourceID = "issue", env.issue.ID
				}
				service := &server{store: source}
				fallbacks := store.WorkflowRunFullImageAttemptsForTesting()
				result := env.outcome(t, source, scaleP21SW500Call{aggregates: []string{env.workflowID}, schedules: []string{test.schedule},
					run: func(ctx context.Context) (domain.WorkflowRun, error) {
						return service.runWorkflow(ctx, env.workspace, env.workflowID, test.trigger, resourceType, resourceID, test.dueOnly)
					}})
				if result.err != nil || result.run.Status != "succeeded" || result.events != 1 {
					t.Fatal("bounded workflow run failed", size, result.err, result.run.Status, result.run.Error, result.events)
				}
				if store.WorkflowRunFullImageAttemptsForTesting() != fallbacks {
					t.Fatal("ordinary workflow run fell back to the full-image writer", size)
				}
				statements[size] = result.statements
				t.Logf("workflow point case=%s extra issues=%d SQL=%d", test.name, size, result.statements)
			}
			if statements[400] != statements[0] {
				t.Fatalf("workflow point writer SQL grew with the workspace: %v", statements)
			}
		})
	}
}

// scaleP21SW580Equal reports the first differing table between two dumps.
func scaleP21SW580Equal(want, got map[string][]string) string {
	if reflect.DeepEqual(want, got) {
		return ""
	}
	for _, table := range scaleP21SW580Tables {
		if !reflect.DeepEqual(want[table], got[table]) {
			a, _ := json.Marshal(want[table])
			b, _ := json.Marshal(got[table])
			return fmt.Sprintf("%s\nlegacy:  %.3000s\nbounded: %.3000s", table,
				scaleP21SW500FirstDifference(a, b), scaleP21SW500FirstDifference(b, a))
		}
	}
	return "table set differs"
}
