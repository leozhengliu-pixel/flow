package main

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21HotTeamMaintenanceIdleAndAutomation(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	repo, err := store.OpenDatabase(scaleP03DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	f := scaleP03Fixture(t, repo)
	workspace := f.data.Workspace.URLKey
	probe, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	public := f.data.Teams[0].ID
	private := idgen.New("hot-maintenance-private-")
	prefix := idgen.New("hot-maintenance-")
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	old, recent := now.AddDate(0, -3, 0), now.Add(-time.Hour)
	backlog := domain.WorkflowState{ID: prefix + "backlog", Name: "Backlog", Type: "backlog", TeamID: public}
	privateBacklog := domain.WorkflowState{ID: prefix + "private-backlog", Name: "Backlog", Type: "backlog", TeamID: private}
	done := domain.WorkflowState{ID: prefix + "done", Name: "Done", Type: "completed", TeamID: public}
	cancel := domain.WorkflowState{ID: prefix + "cancel", Name: "Canceled", Type: "canceled", TeamID: public}
	privateCancel := domain.WorkflowState{ID: prefix + "private-cancel", Name: "Canceled", Type: "canceled", TeamID: private}
	projectID := prefix + "completed-project"
	seed := func(active bool) {
		t.Helper()
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "teams", "teamSettings", "states", "issues", "projects", "activities", "notifications"), workspace, "scale_p21.team_maintenance_seed", "", nil, func(data *domain.Bootstrap) error {
			if !slicesContainsTeam(data.Teams, private) {
				data.Teams = append(data.Teams, domain.Team{ID: private, Key: "HMP", Private: true})
			}
			for _, team := range data.Teams {
				settings := data.TeamSettings[team.ID]
				settings.AutoCloseStale, settings.StaleMonths, settings.AutoArchiveMonths = false, -1, -1
				settings.ProgressOrder = "noAction"
				data.TeamSettings[team.ID] = settings
			}
			for _, id := range []string{public, private} {
				settings := data.TeamSettings[id]
				settings.AgentGuidance = strings.Repeat("unrelated settings body ", 50000)
				if id == private {
					settings.Access = "private"
				}
				if active {
					settings.AutoCloseStale, settings.StaleMonths, settings.AutoArchiveMonths = true, 1, 1
				}
				if id == public {
					settings.StaleStatusID = cancel.ID
				}
				data.TeamSettings[id] = settings
			}
			data.States = []domain.WorkflowState{backlog, privateBacklog, done, cancel, privateCancel}
			project := domain.Project{ID: projectID, Name: "Completed project", TeamIDs: []string{public}, Status: domain.ProjectStatus{Type: "completed"}, UpdatedAt: old, Progress: .75, IssueCount: 1}
			data.Projects = []domain.Project{project}
			data.Issues = []domain.Issue{
				{ID: prefix + "stale", Identifier: "HMT-1", Number: 1, Title: "stale public", Team: domain.Team{ID: public}, State: backlog, CreatedAt: old, UpdatedAt: old},
				{ID: prefix + "private-stale", Identifier: "HMP-1", Number: 1, Title: "stale private", Team: domain.Team{ID: private}, State: privateBacklog, CreatedAt: old, UpdatedAt: old},
				{ID: prefix + "archive", Identifier: "HMT-2", Number: 2, Title: "old completed", Team: domain.Team{ID: public}, State: done, Project: &domain.ProjectSummary{ID: project.ID, Name: project.Name}, CreatedAt: old, UpdatedAt: old, CompletedAt: &old},
				{ID: prefix + "recent", Identifier: "HMT-3", Number: 3, Title: "recent", Team: domain.Team{ID: public}, State: backlog, CreatedAt: recent, UpdatedAt: recent},
			}
			data.Activities = map[string][]domain.ActivityEvent{}
			data.Notifications = nil
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, reload := range []bool{false, true} {
		seed(false)
		if reload {
			if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
		}
		if err := probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
		var idleCounts []int
		for _, source := range []*store.SQLiteStore{repo, probe} {
			sqlCount := 0
			ctx := store.WithSQLStatementObserver(t.Context(), func(string) { sqlCount++ })
			if err := (&server{store: source}).maintainTeamSettings(ctx, workspace, now); err != nil {
				t.Fatal("idle sweep failed", reload, source.BoundedMetadataEnabled(), sqlCount, err)
			}
			idleCounts = append(idleCounts, sqlCount)
			t.Logf("idle reload=%t bounded=%t SQL=%d", reload, source.BoundedMetadataEnabled(), sqlCount)
		}
		if idleCounts[0] != idleCounts[1] || !reload && idleCounts[0] != 0 {
			t.Fatal("idle sweep SQL differs from literal installed defaults", reload, idleCounts)
		}
	}
	type outcome struct {
		StateID         string
		State           string
		AutoClosed      bool
		Archived        bool
		Touched         bool
		ArchivedAt      *time.Time
		StatusChangedAt *time.Time
		CanceledAt      *time.Time
		CompletedAt     *time.Time
	}
	type projectOutcome struct {
		ID, Name string
		Progress float64
		Count    int
		Archived bool
	}
	for _, reload := range []bool{false, true} {
		var expected map[string]outcome
		var expectedActivities map[string][]domain.ActivityEvent
		var expectedProject projectOutcome
		var expectedPageQueries int
		for _, bounded := range []bool{false, true} {
			seed(true)
			if reload {
				if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			source := repo
			if bounded {
				if err := probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
					t.Fatal(err)
				}
				source = probe
			}
			pageQueries, totalSQL := 0, 0
			ctx := store.WithSQLStatementObserver(t.Context(), func(query string) {
				totalSQL++
				if strings.Contains(query, "SELECT i.data,i.updated_at,") && strings.Contains(query, "ORDER BY i.updated_at ASC") {
					pageQueries++
				}
			})
			if err := (&server{store: source}).maintainTeamSettings(ctx, workspace, now); err != nil {
				t.Fatalf("automation sweep reload=%t bounded=%t failed: %v", reload, bounded, err)
			}
			if bounded && source.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("bounded automation decoded workspace metadata")
			}
			got := map[string]outcome{}
			for _, id := range []string{prefix + "stale", prefix + "private-stale", prefix + "archive", prefix + "recent"} {
				issue, err := repo.IssueRecord(t.Context(), workspace, id)
				if err != nil {
					t.Fatal(err)
				}
				if id == prefix+"recent" && !issue.UpdatedAt.Equal(recent) {
					t.Fatal("recent issue timestamp changed", issue.UpdatedAt)
				}
				got[id] = outcome{
					StateID: issue.State.ID, State: issue.State.Type, AutoClosed: issue.AutoClosed,
					Archived: issue.ArchivedAt != nil, Touched: !issue.UpdatedAt.Equal(old),
					ArchivedAt:      issue.ArchivedAt,
					StatusChangedAt: issue.StatusChangedAt, CanceledAt: issue.CanceledAt, CompletedAt: issue.CompletedAt,
				}
			}
			if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			data, _ := repo.WorkspaceMetadata(workspace)
			if len(data.Projects) != 1 {
				t.Fatalf("linked issue sweep changed project registry reload=%t bounded=%t: projects=%d issue_outcomes=%v", reload, bounded, len(data.Projects), got)
			}
			project := data.Projects[0]
			projectState := projectOutcome{project.ID, project.Name, project.Progress, project.IssueCount, project.ArchivedAt != nil}
			activities := map[string][]domain.ActivityEvent{}
			for _, suffix := range []string{"stale", "private-stale", "archive", "recent"} {
				id := prefix + suffix
				history, err := repo.IssueHistoryPage(t.Context(), workspace, id, "", "")
				if err != nil {
					t.Fatal(err)
				}
				for _, activity := range history.Activities {
					activity.ID, activity.CreatedAt = "", time.Time{}
					if activity.UpdatedAt != nil {
						zero := time.Time{}
						activity.UpdatedAt = &zero
					}
					activities[id] = append(activities[id], activity)
				}
			}
			if expected == nil {
				expected = got
				expectedProject = projectState
				expectedActivities = activities
				expectedPageQueries = pageQueries
				if !got[prefix+"stale"].AutoClosed || !reload && !got[prefix+"private-stale"].AutoClosed || !got[prefix+"archive"].Archived || got[prefix+"recent"].AutoClosed {
					t.Fatal("literal automation fixture did not exercise expected outcomes", got)
				}
				for _, suffix := range []string{"stale", "private-stale", "archive"} {
					events := activities[prefix+suffix]
					if suffix == "private-stale" && !got[prefix+suffix].AutoClosed {
						if len(events) != 0 {
							t.Fatal("unchanged private issue acquired automation activity", events)
						}
						continue
					}
					wantType, wantAutomation := "issue.updated", "autoCloseStale"
					if suffix == "archive" {
						wantType, wantAutomation = "issue.archived", "autoArchive"
					}
					if len(events) != 1 || events[0].Type != wantType || events[0].Metadata["automation"] != wantAutomation || events[0].Actor.ID != f.data.Viewer.ID {
						t.Fatal("literal automation activity missing or changed", suffix, events)
					}
				}
				if len(activities[prefix+"recent"]) != 0 || len(data.Notifications) != 0 {
					t.Fatal("automation produced an unrelated activity or recipient", activities, data.Notifications)
				}
			} else if !reflect.DeepEqual(got, expected) {
				t.Fatalf("automation differs reload=%t bounded=%t\nliteral=%v\nbounded=%v\nliteral_project=%+v bounded_project=%+v", reload, bounded, expected, got, expectedProject, projectState)
			}
			if !reflect.DeepEqual(activities, expectedActivities) || len(data.Notifications) != 0 {
				t.Fatalf("automation effects differ reload=%t bounded=%t\nliteral=%v\nbounded=%v notifications=%v", reload, bounded, expectedActivities, activities, data.Notifications)
			}
			if project.ID != projectID || project.ArchivedAt != nil || projectState != expectedProject {
				t.Fatal("omitted project collection changed or linked progress diverged", expectedProject, projectState)
			}
			if pageQueries != expectedPageQueries || !reload && pageQueries != 2 {
				t.Fatal("grouped stale/archive queries changed", expectedPageQueries, pageQueries)
			}
			t.Logf("reload=%t bounded=%t grouped_queries=%d total_SQL=%d", reload, bounded, pageQueries, totalSQL)
		}
	}
	ctx, cancelContext := context.WithCancel(t.Context())
	cancelContext()
	if err := (&server{store: probe}).maintainTeamSettings(ctx, workspace, now); err != context.Canceled {
		t.Fatal("canceled sweep did not propagate", err)
	}
}

func slicesContainsTeam(teams []domain.Team, id string) bool {
	for _, team := range teams {
		if team.ID == id {
			return true
		}
	}
	return false
}
