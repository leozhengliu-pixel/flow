package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// A team settings write whose callback fails leaves the copy exactly as it
// was, the cascades included: sub-teams' settings (a retired one too),
// statuses and issue templates changed in place, and cycles changed, deleted
// (which moves the cycles after them in the shared backing array) and added.
// The issue records the write loaded for its cascades are released with its
// transaction: the next write runs (SQLite holds one connection) and the
// records are unchanged.
func TestTeamSettingsCascadeRollsBackOnCallbackError(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx := context.Background()
	key := repo.Bootstrap().Workspace.URLKey
	retired := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	day := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	if err := repo.MutateWorkspace(ctx, key, "test.cascade_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: "cascade-parent", Key: "CPA", Name: "Parent"}, domain.Team{ID: "cascade-child", Key: "CCH", Name: "Child"},
			domain.Team{ID: "cascade-retired", Key: "CRE", Name: "Retired", RetiredAt: &retired})
		data.TeamSettings["cascade-parent"] = bulkTeamSettings("cascade-parent")
		child := bulkTeamSettings("cascade-child")
		child.ParentTeamID = "cascade-parent"
		data.TeamSettings["cascade-child"] = child
		gone := bulkTeamSettings("cascade-retired")
		gone.ParentTeamID = "cascade-child"
		data.TeamSettings["cascade-retired"] = gone
		data.States = append(data.States,
			domain.WorkflowState{ID: "cascade-backlog", TeamID: "cascade-parent", Name: "Backlog", Type: "backlog", Default: true},
			domain.WorkflowState{ID: "cascade-todo", TeamID: "cascade-parent", Name: "Todo", Type: "unstarted"})
		data.IssueTemplates = append(data.IssueTemplates, domain.IssueTemplate{ID: "cascade-template", TeamID: "cascade-parent", Name: "Bug", StateID: "cascade-todo", LabelIDs: []string{"a"}})
		for index, id := range []string{"cascade-c1", "cascade-c2", "cascade-c3"} {
			data.Cycles = append(data.Cycles, domain.Cycle{ID: id, TeamID: "cascade-child", Number: index + 1, StartsAt: day.AddDate(0, 0, 7*index), Status: "upcoming"})
		}
		data.Cycles = append(data.Cycles, domain.Cycle{ID: "cascade-other", TeamID: data.Teams[0].ID, Number: 9, StartsAt: day, Status: "current"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		data, ok := repo.WorkspaceMetadata(key)
		if !ok {
			t.Fatal("workspace missing")
		}
		raw, err := json.Marshal([]any{data.TeamSettings, data.States, data.IssueTemplates, data.Cycles, data.Labels, data.Teams})
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	issues := func() string {
		rows, err := repo.db.QueryContext(ctx, `SELECT id,state_id,assignee_id,version,data FROM issue_records WHERE workspace_key=? ORDER BY id`, key)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var result []string
		for rows.Next() {
			var id, state, assignee string
			var version int64
			var data []byte
			if err := rows.Scan(&id, &state, &assignee, &version, &data); err != nil {
				t.Fatal(err)
			}
			result = append(result, id+state+assignee+string(data))
		}
		raw, _ := json.Marshal(result)
		return string(raw)
	}
	before, storedIssues := snapshot(), issues()
	loaded := 0
	cascade := WithTeamIssueCascade(ctx, func(data *domain.Bootstrap) []string { return []string{data.Teams[0].ID} })
	err = repo.MutateWorkspace(cascade, key, "team.settings_updated", "cascade-parent", nil, func(data *domain.Bootstrap) error {
		loaded = len(data.Issues)
		for index := range data.Issues {
			data.Issues[index].Assignee = nil
			data.Issues[index].Title = "changed"
		}
		for _, id := range []string{"cascade-parent", "cascade-child", "cascade-retired"} {
			settings := data.TeamSettings[id]
			settings.SettingsPermission = "owners"
			data.TeamSettings[id] = settings
		}
		for index := range data.States {
			if data.States[index].TeamID == "cascade-parent" {
				data.States[index].Default = data.States[index].ID == "cascade-todo"
			}
		}
		data.States = append(data.States, domain.WorkflowState{ID: "cascade-new", TeamID: "cascade-parent", Name: "New", Type: "started"})
		for index := range data.IssueTemplates {
			if data.IssueTemplates[index].ID == "cascade-template" {
				data.IssueTemplates[index].StateID = "cascade-backlog"
				data.IssueTemplates[index].LabelIDs[0] = "b"
			}
		}
		for index := range data.Cycles {
			if data.Cycles[index].ID == "cascade-c3" {
				data.Cycles[index].Status = "completed"
			}
		}
		data.Cycles = slices.DeleteFunc(data.Cycles, func(cycle domain.Cycle) bool { return cycle.ID == "cascade-c1" })
		data.Cycles = append(data.Cycles, domain.Cycle{ID: "cascade-c4", TeamID: "cascade-child", Number: 4, Status: "upcoming"})
		return errors.New("abort")
	})
	if err == nil || err.Error() != "abort" {
		t.Fatalf("failed callback reported %v", err)
	}
	if loaded == 0 {
		t.Fatal("the cascade loaded no issue of the declared team")
	}
	if after := snapshot(); after != before {
		at := 0
		for at < len(before) && at < len(after) && before[at] == after[at] {
			at++
		}
		start := max(0, at-200)
		t.Fatalf("rollback leaked into the copy at byte %d:\nbefore %s\nafter  %s", at, before[start:min(at+200, len(before))], after[start:min(at+200, len(after))])
	}
	// The connection the cascade's transaction held is free again.
	done := make(chan error, 1)
	go func() {
		done <- repo.MutateWorkspace(ctx, key, "team.settings_updated", "cascade-child", nil, func(data *domain.Bootstrap) error {
			settings := data.TeamSettings["cascade-child"]
			settings.Description = "after"
			data.TeamSettings["cascade-child"] = settings
			return nil
		})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the next write waited for the failed write's transaction")
	}
	if after := issues(); after != storedIssues {
		t.Fatal("a failed write changed issue records")
	}
}
