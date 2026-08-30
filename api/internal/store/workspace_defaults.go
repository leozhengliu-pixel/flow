package store

import (
	"fmt"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// EmptyWorkspace creates the system defaults for a newly created workspace.
// It intentionally contains no sample issues, projects, members, or activity.
func EmptyWorkspace(name, urlKey, region string, viewer domain.User) domain.Bootstrap {
	now := time.Now().UTC()
	key := strings.ToUpper(urlKey)
	if len(key) > 3 {
		key = key[:3]
	}
	if key == "" {
		key = "NEW"
	}
	workspaceID := fmt.Sprintf("workspace_%d", now.UnixNano())
	team := domain.Team{ID: fmt.Sprintf("team_%d", now.UnixNano()), Name: name, Key: key, Color: "#5E6AD2"}
	data := domain.Bootstrap{
		Workspace: domain.Workspace{ID: workspaceID, Name: name, URLKey: urlKey, Color: "#5E6AD2", Region: region, CreatedAt: now},
		Viewer:    viewer, Users: []domain.User{viewer}, Teams: []domain.Team{team}, Customers: []domain.Customer{}, States: canonicalWorkflowStates(), Labels: []domain.IssueLabel{}, LabelGroups: []domain.LabelGroup{},
		Issues: []domain.Issue{}, Cycles: []domain.Cycle{}, CycleSettings: map[string]domain.CycleSettings{}, Projects: []domain.Project{},
		ProjectStatuses: canonicalProjectStatuses(), ProjectUpdates: map[string][]domain.ProjectUpdate{}, Initiatives: []domain.Initiative{},
		InitiativeUpdates: map[string][]domain.InitiativeUpdate{}, Comments: map[string][]domain.Comment{}, Activities: map[string][]domain.ActivityEvent{},
		SavedViews: []domain.SavedView{}, Notifications: []domain.Notification{}, Loops: []domain.Loop{}, CustomEmojis: []domain.CustomEmoji{}, AgentSessions: []domain.AgentSession{}, AgentSkills: []domain.PersonalAgentSkill{},
	}
	normalize(&data)
	return data
}

func localSQLiteFixture() domain.Bootstrap {
	now := time.Now().UTC()
	viewer := domain.User{ID: "usr_admin", Name: "Test admin", DisplayName: "Test admin", Email: "admin@example.test", Active: true, EmailVerified: true}
	other := domain.User{ID: "usr_member", Name: "Test member", DisplayName: "Test member", Email: "member@example.test", Active: true, EmailVerified: true}
	team := domain.Team{ID: "team_test", Name: "Test team", Key: "TST", Color: "#5E6AD2"}
	states := canonicalWorkflowStates()
	labels := canonicalLabels()
	project := domain.Project{ID: "project_aut", Name: "Test project", SlugID: "test-project", Summary: "Test project", Icon: "Project", Color: "#5E6AD2", Priority: 2, PriorityLabel: "High", Progress: .5, Health: "onTrack", Status: canonicalProjectStatuses()[2], Lead: &viewer, TeamIDs: []string{team.ID}, MemberIDs: []string{viewer.ID}, LabelIDs: []string{"label_product"}, CreatedAt: now, UpdatedAt: now, Milestones: []domain.ProjectMilestone{}}
	projectCruise := project
	projectCruise.ID, projectCruise.Name, projectCruise.SlugID = "project_cruise", "Test project two", "test-project-two"
	issue := func(id string, number int, title string, state domain.WorkflowState) domain.Issue {
		return domain.Issue{ID: id, Version: 1, Identifier: fmt.Sprintf("TST-%d", number), Number: number, Title: title, Description: "Test issue", Priority: 2, PriorityLabel: "High", SortOrder: float64(number), CreatedAt: now, UpdatedAt: now, Team: team, State: state, Assignee: &viewer, Creator: other, Labels: []domain.IssueLabel{labels[0]}, SubscriberIDs: []string{viewer.ID, other.ID}, SubIssueIDs: []string{}, Relations: []domain.IssueRelation{}, Attachments: []domain.Attachment{}}
	}
	issues := []domain.Issue{issue("issue_1", 1, "Test requirement", states[1]), issue("issue_2", 2, "Test task", states[2]), issue("issue_33", 33, "Test legacy issue", states[1]), issue("issue_53156", 53156, "Test delivery task", states[1]), issue("issue_105130", 105130, "Test release task", states[1])}
	for index := range issues {
		issues[index].Project = &domain.ProjectSummary{ID: project.ID, Name: project.Name, Color: project.Color, Icon: project.Icon}
	}
	data := EmptyWorkspace("Test workspace", "test-workspace", "us", viewer)
	data.Workspace.ID = "workspace_test"
	data.Users = []domain.User{viewer, other}
	data.Teams = []domain.Team{team}
	data.Labels = labels
	data.LabelGroups = canonicalLabelGroups()
	data.Issues = issues
	data.Projects = []domain.Project{project, projectCruise}
	data.Cycles = []domain.Cycle{{ID: "cycle_48", Number: 48, Name: "Cycle 48", TeamID: team.ID, StartsAt: now.AddDate(0, 0, -7), EndsAt: now.AddDate(0, 0, 6), Status: "current", Capacity: 4, CreatedAt: now, UpdatedAt: now}, {ID: "cycle_49", Number: 49, Name: "Cycle 49", TeamID: team.ID, StartsAt: now.AddDate(0, 0, 7), EndsAt: now.AddDate(0, 0, 20), Status: "upcoming", Capacity: 4, CreatedAt: now, UpdatedAt: now}, {ID: "cycle_50", Number: 50, Name: "Cycle 50", TeamID: team.ID, StartsAt: now.AddDate(0, 0, 21), EndsAt: now.AddDate(0, 0, 34), Status: "upcoming", Capacity: 4, CreatedAt: now, UpdatedAt: now}}
	data.CycleSettings = map[string]domain.CycleSettings{team.ID: {Enabled: true, DurationWeeks: 2, StartsOn: 1, UpcomingCount: 2, Capacity: 4, AutoCreate: true, AutoMigrate: true}}
	data.Issues[0].CycleID = stringPointer("cycle_48")
	data.Issues[1].CycleID = stringPointer("cycle_48")
	data.Issues[2].CycleID = stringPointer("cycle_49")
	data.Issues[3].CycleID = stringPointer("cycle_50")
	data.Activities = map[string][]domain.ActivityEvent{}
	for _, item := range data.Issues {
		data.Activities[item.ID] = []domain.ActivityEvent{{ID: "activity_" + item.ID, Type: "issue.created", CreatedAt: item.CreatedAt, Actor: other, Metadata: map[string]string{}}}
	}
	data.Notifications = []domain.Notification{{ID: "notification-1", RecipientID: viewer.ID, Type: "issue.created", SourceType: "issue", SourceID: issues[0].ID, IssueID: issues[0].ID, Actor: other, Category: "activity", GroupKey: viewer.ID + ":" + issues[0].ID + ":activity", OccurrenceCount: 1, CreatedAt: now, UpdatedAt: now}}
	opened := now.Add(-time.Hour)
	data.Reviews = []domain.CodeReview{{
		ID: "review-1", SlugID: "test-review-1", Provider: "github", ExternalID: "pr-1", Number: 1,
		Title: "Test pull request", Status: "open", RepositoryOwner: "example", RepositoryName: "repository", URL: "https://example.test/repository/pull/1", Author: other,
		ReviewerIDs: []string{viewer.ID}, TeamReviewers: []string{}, IssueIDs: []string{issues[0].ID}, BaseBranch: "main", HeadBranch: "test-branch", BranchState: "behind",
		Checks: []domain.ReviewCheck{}, Files: []domain.ReviewFile{}, Events: []domain.ReviewEvent{{ID: "review-event-opened", Type: "opened", Actor: other, CreatedAt: opened}, {ID: "review-event-requested", Type: "review_requested", Actor: other, CreatedAt: opened.Add(time.Second)}},
		CreatedAt: opened, UpdatedAt: now,
	}}
	return data
}

func cycleWeekStart(value time.Time) time.Time {
	day := time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
	daysSinceMonday := (int(day.Weekday()) + 6) % 7
	return day.AddDate(0, 0, -daysSinceMonday)
}

func stringPointer(value string) *string { return &value }

func canonicalProjectStatuses() []domain.ProjectStatus {
	return []domain.ProjectStatus{
		{ID: "ps_backlog", Name: "Backlog", Color: "#E79D4F", Type: "backlog"},
		{ID: "ps_planned", Name: "Planned", Color: "#A8A8AA", Type: "planned"},
		{ID: "ps_progress", Name: "In Progress", Color: "#E2B714", Type: "started"},
		{ID: "ps_completed", Name: "Completed", Color: "#5E6AD2", Type: "completed"},
		{ID: "ps_canceled", Name: "Canceled", Color: "#8A8F98", Type: "canceled"},
	}
}

func canonicalWorkflowStates() []domain.WorkflowState {
	return []domain.WorkflowState{
		{ID: "state_backlog", Name: "Backlog", Color: "#6B6F76", Type: "backlog", Position: 0, Default: true},
		{ID: "state_todo", Name: "Todo", Color: "#E2E2E2", Type: "unstarted", Position: 1},
		{ID: "state_progress", Name: "In Progress", Color: "#F2C94C", Type: "started", Position: 2},
		{ID: "state_done", Name: "Done", Color: "lch(48% 59.31 288.43)", Type: "completed", Position: 3},
		{ID: "state_canceled", Name: "Canceled", Color: "#95A2B3", Type: "canceled", Position: 4},
		{ID: "state_duplicate", Name: "Duplicate", Color: "#95A2B3", Type: "canceled", Position: 5, Reserved: true},
	}
}

func canonicalLabels() []domain.IssueLabel {
	labels := []domain.IssueLabel{
		{ID: "label_type_requirement", Name: "Requirement", Color: "#5E6AD2", Description: "Product or business work that has not yet been split into implementation tasks", Scope: "Workspace", GroupID: "label_group_work_item_type"},
		{ID: "label_type_development", Name: "Development", Color: "#4AA3F7", Description: "Implementation or validation work owned by the delivery team", Scope: "Workspace", GroupID: "label_group_work_item_type"},
		{ID: "label_type_defect", Name: "Defect", Color: "#F15B61", Description: "A problem that requires investigation, correction, and verification", Scope: "Workspace", GroupID: "label_group_work_item_type"},
		{ID: "label_product", Name: "Product", Color: "#18B99A", Description: "Product planning and experience improvements", Scope: "Workspace", ResourceType: "project", GroupID: "label_group_project_value"},
		{ID: "label_delivery", Name: "Delivery", Color: "#D97757", Description: "Cross-team delivery work that needs additional visibility", Scope: "Workspace", ResourceType: "project", GroupID: "label_group_project_delivery"},
	}
	now := time.Now().UTC()
	for index := range labels {
		if labels[index].ResourceType == "" {
			labels[index].ResourceType = "issue"
		}
		labels[index].CreatedAt = now.AddDate(0, 0, -(len(labels) - index + 7))
	}
	return labels
}

func canonicalLabelGroups() []domain.LabelGroup {
	now := time.Now().UTC()
	return []domain.LabelGroup{
		{ID: "label_group_work_item_type", Name: "Issue type", Color: "#5E6AD2", Description: "Issue classification", Scope: "Workspace", ResourceType: "issue", CreatedAt: now},
		{ID: "label_group_project_value", Name: "Project value", Color: "#18B99A", Description: "Business value and strategic attributes", Scope: "Workspace", ResourceType: "project", CreatedAt: now},
		{ID: "label_group_project_delivery", Name: "Project delivery", Color: "#D97757", Description: "Delivery characteristics and attention level", Scope: "Workspace", ResourceType: "project", CreatedAt: now},
	}
}
