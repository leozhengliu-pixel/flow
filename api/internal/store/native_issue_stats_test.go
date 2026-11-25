package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21SW19NativeIssueStatsMigration(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := f.MutateWorkspace(WithoutIssueRecordMutations(t.Context()), workspace, "scale_p21_sw19.stats_seed", "", nil, func(data *domain.Bootstrap) error {
		for index := range 120 {
			state := domain.WorkflowState{ID: fmt.Sprintf("state_sw19_%d", index%3), Type: []string{"started", "completed", "canceled"}[index%3]}
			issue := domain.Issue{
				ID: idgen.New("issue_sw19_"), Identifier: fmt.Sprintf("SW19-%d", index+1), Number: index + 1,
				Title: "Stats fixture", Team: domain.Team{ID: f.data.Teams[0].ID}, State: state, Priority: index % 5,
				Creator: f.data.Viewer, CreatedAt: stamp, UpdatedAt: stamp,
			}
			if index%2 == 0 {
				issue.Assignee = &f.data.Viewer
			}
			if index%4 == 0 {
				issue.ArchivedAt = &stamp
			}
			if index == 0 {
				issue.Description = strings.Repeat("unrelated metadata body ", 20000)
			}
			data.Issues = append(data.Issues, issue)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	type counter struct {
		Scope, ID, State, Type string
		Priority, Archived     int
		Total                  int64
	}
	read := func() []counter {
		t.Helper()
		rows, err := f.db.QueryContext(t.Context(), `SELECT scope_type,scope_id,state_id,state_type,priority,archived,total FROM issue_scope_counts WHERE workspace_key=? ORDER BY scope_type,scope_id,state_id,state_type,priority,archived`, workspace)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		result := []counter{}
		for rows.Next() {
			var item counter
			if err := rows.Scan(&item.Scope, &item.ID, &item.State, &item.Type, &item.Priority, &item.Archived, &item.Total); err != nil {
				t.Fatal(err)
			}
			result = append(result, item)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return result
	}
	var legacy []counter
	run := func(source *SQLiteStore, ctx context.Context) error {
		source.mu.Lock()
		catalog := source.workspaces
		source.workspaces = map[string]domain.Bootstrap{workspace: catalog[workspace]}
		source.mu.Unlock()
		defer func() {
			source.mu.Lock()
			source.workspaces = catalog
			source.mu.Unlock()
		}()
		return source.migrateIssueStats(ctx)
	}
	for _, bounded := range []bool{false, true} {
		source := f.SQLiteStore
		if bounded {
			var err error
			source, err = f.BoundedMetadataViewForTesting(t.Context())
			if err != nil {
				t.Fatal(err)
			}
		}
		if _, err := f.db.ExecContext(t.Context(), `DELETE FROM issue_stats_migrations WHERE workspace_key=?`, workspace); err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.ExecContext(t.Context(), `DELETE FROM issue_scope_counts WHERE workspace_key=?`, workspace); err != nil {
			t.Fatal(err)
		}
		published := 0
		source.SetWebhookSink(func(string, domain.DomainEvent) { published++ })
		source.SetRealtimeSink(func(string, domain.RealtimeEvent) { published++ })
		if err := run(source, t.Context()); err != nil {
			t.Fatal(err)
		}
		current := read()
		total := int64(0)
		for _, count := range current {
			if count.Scope == "workspace" {
				total += count.Total
			}
		}
		if total != 120 || published != 0 {
			t.Fatalf("native counters=%d events=%d", total, published)
		}
		if bounded {
			if !reflect.DeepEqual(legacy, current) || source.WorkspaceMetadataDecodes() != 0 || source.metadataResidency.work.used != 0 {
				t.Fatal("native SQL migration changed counters, decoded bodies or leaked admission")
			}
		} else {
			legacy = current
		}
		if err := run(source, t.Context()); err != nil || !reflect.DeepEqual(current, read()) {
			t.Fatal("native stats migration was not idempotent", err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := run(source, ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled stats migration: %v", err)
		}
	}
}
