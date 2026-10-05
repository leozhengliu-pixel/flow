package store

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestEnsureBuiltinApplicationIsIdempotentAndFollowsAgentConfig(t *testing.T) {
	s, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "builtin.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := t.Context()
	d := s.Bootstrap()
	workspace := d.Workspace.URLKey
	// Disabled with nothing installed: nothing to do.
	if changed, err := s.EnsureBuiltinApplication(ctx, workspace, false); err != nil || changed {
		t.Fatalf("disabled install: changed=%v err=%v", changed, err)
	}
	if changed, err := s.EnsureBuiltinApplication(ctx, workspace, true); err != nil || !changed {
		t.Fatalf("install: changed=%v err=%v", changed, err)
	}
	if changed, err := s.EnsureBuiltinApplication(ctx, workspace, true); err != nil || changed {
		t.Fatalf("second ensure must be a no-op: changed=%v err=%v", changed, err)
	}
	apps, err := s.ListApplications(ctx, workspace)
	if err != nil || len(apps) != 1 {
		t.Fatalf("installations: %+v %v", apps, err)
	}
	app := apps[0]
	if !app.Builtin || !app.Active || app.Name != "Flow" || app.ClientID != BuiltinApplicationClientID || !app.AllTeams || len(app.TeamIDs) != len(d.Teams) {
		t.Fatalf("built-in installation: %+v", app)
	}
	user, err := s.UserByID(ctx, app.UserID)
	if err != nil || !user.App || !user.BuiltinAgent || !slices.ContainsFunc(d.Teams, func(team domain.Team) bool { return user.CanDelegateTo(team.ID) }) {
		t.Fatalf("built-in principal: %+v %v", user, err)
	}
	// Turning the agent off suspends (keeps) the member; turning it back on restores it.
	if changed, err := s.EnsureBuiltinApplication(ctx, workspace, false); err != nil || !changed {
		t.Fatalf("suspend: changed=%v err=%v", changed, err)
	}
	if user, _ = s.UserByID(ctx, app.UserID); user.Active || user.CanDelegateTo(d.Teams[0].ID) {
		t.Fatalf("suspended built-in agent still delegatable: %+v", user)
	}
	if changed, _ := s.EnsureBuiltinApplication(ctx, workspace, false); changed {
		t.Fatal("second suspend must be a no-op")
	}
	if changed, err := s.EnsureBuiltinApplication(ctx, workspace, true); err != nil || !changed {
		t.Fatalf("restore: changed=%v err=%v", changed, err)
	}
	if user, _ = s.UserByID(ctx, app.UserID); !user.Active {
		t.Fatal("built-in agent was not restored")
	}
	// An administrator's own deactivation is respected.
	apps, _ = s.ListApplications(ctx, workspace)
	manual := apps[0]
	manual.Active, manual.InstalledBy = false, d.Viewer.ID
	if _, err := s.InstallApplication(ctx, workspace, manual, ""); err != nil {
		t.Fatal(err)
	}
	if changed, _ := s.EnsureBuiltinApplication(ctx, workspace, true); changed {
		t.Fatal("ensure re-enabled an agent an administrator turned off")
	}
}

func TestAgentSessionStateFilterUsesTaskStatus(t *testing.T) {
	s, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := t.Context()
	d := s.Bootstrap()
	workspace := d.Workspace.URLKey
	base := d.Issues[0]
	rows := []domain.Issue{}
	// "merged" is an active task whose issue's pull request merged.
	for index, status := range []string{"active", "error", "complete", "canceled", "", "merged"} {
		issue := base
		issue.ID, issue.Identifier, issue.State.ID = "session-issue-"+string(rune('a'+index)), "SES-"+string(rune('A'+index)), "session-state"
		issue.AgentSessionID = ""
		if status != "" {
			issue.AgentSessionID = "task-" + status
			taskStatus := status
			if status == "merged" {
				taskStatus = "active"
			}
			if _, err := s.db.ExecContext(ctx, `INSERT INTO application_agent_tasks(id,workspace_key,issue_id,app_user_id,status,version,data,created_at) VALUES(?,?,?,?,?,1,?,?)`, issue.AgentSessionID, workspace, issue.ID, "app_x", taskStatus, []byte(`{}`), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
				t.Fatal(err)
			}
		}
		rows = append(rows, issue)
	}
	if err := s.ImportIssues(ctx, workspace, rows); err != nil {
		t.Fatal(err)
	}
	// Only issues with a session are stamped (session-issue-e has none), once.
	first := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for _, at := range []time.Time{first, first.Add(time.Hour)} {
		if err := s.MarkAgentSessionsMerged(ctx, workspace, []string{"session-issue-f", "session-issue-e", "missing"}, at); err != nil {
			t.Fatal(err)
		}
	}
	var merged int
	var mergedAt string
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM application_agent_tasks WHERE workspace_key=? AND merged_at<>''`, workspace).Scan(&merged); err != nil || merged != 1 {
		t.Fatalf("merged sessions=%d err=%v", merged, err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT merged_at FROM application_agent_tasks WHERE id='task-merged'`).Scan(&mergedAt); err != nil || mergedAt != first.Format(time.RFC3339Nano) {
		t.Fatalf("merged_at=%q err=%v (must keep the first merge)", mergedAt, err)
	}
	for _, test := range []struct {
		filter IssueFilter
		want   int64
	}{
		{IssueFilter{Field: "agentSessionState", Values: []string{"pending", "active", "awaitingInput"}}, 1},
		{IssueFilter{Field: "agentSessionState", Values: []string{"error"}}, 1},
		{IssueFilter{Field: "agentSessionState", Values: []string{"canceled"}}, 1},
		{IssueFilter{Field: "agentSessionState", Values: []string{"complete", "canceled"}}, 2},
		{IssueFilter{Field: "agentSessionState", Values: []string{"merged"}}, 1},
		{IssueFilter{Field: "agentSessionState", Values: []string{"error", "merged"}}, 2},
		{IssueFilter{Field: "agentSessionState", Operator: "isNot", Values: []string{"error"}}, 5},
		{IssueFilter{Field: "agentSessionState", Operator: "isNot", Values: []string{"merged"}}, 5},
	} {
		page, err := s.QueryIssueRecords(ctx, IssueRecordQuery{Workspace: workspace, Filter: IssueFilter{And: []IssueFilter{{Field: "status", Values: []string{"session-state"}}, test.filter}}, IncludeTotal: true, Limit: 1})
		if err != nil || page.Total != test.want {
			t.Fatalf("%+v: total=%d err=%v", test.filter, page.Total, err)
		}
	}
	if _, err := s.QueryIssueRecords(ctx, IssueRecordQuery{Workspace: workspace, Filter: IssueFilter{Field: "agentSessionState", Values: []string{"bogus"}}, Limit: 1}); err == nil {
		t.Fatal("unknown session states must be rejected")
	}
	states, err := s.AgentSessionStates(ctx, workspace, []string{"task-error", "task-merged", "missing"})
	if err != nil || states["task-error"] != "error" || states["task-merged"] != "merged" || len(states) != 2 {
		t.Fatalf("states: %+v %v", states, err)
	}
	all, err := s.WorkspaceAgentSessionStates(ctx, workspace)
	if err != nil || all["task-merged"] != "merged" || all["task-active"] != "active" {
		t.Fatalf("workspace states: %+v %v", all, err)
	}
}
