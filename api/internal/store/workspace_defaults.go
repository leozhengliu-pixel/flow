package store

import (
	"fmt"
	"slices"
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
	return domain.Bootstrap{
		Workspace: domain.Workspace{ID: workspaceID, Name: name, URLKey: urlKey, Color: "#5E6AD2", Region: region, CreatedAt: now},
		Viewer:    viewer, Users: []domain.User{viewer}, Teams: []domain.Team{team}, Customers: []domain.Customer{}, States: canonicalWorkflowStates(), Labels: canonicalLabels(), LabelGroups: canonicalLabelGroups(),
		Issues: []domain.Issue{}, Cycles: []domain.Cycle{}, CycleSettings: map[string]domain.CycleSettings{}, Projects: []domain.Project{},
		ProjectStatuses: canonicalProjectStatuses(), ProjectUpdates: map[string][]domain.ProjectUpdate{}, Initiatives: []domain.Initiative{},
		InitiativeUpdates: map[string][]domain.InitiativeUpdate{}, Comments: map[string][]domain.Comment{}, Activities: map[string][]domain.ActivityEvent{},
		SavedViews: []domain.SavedView{}, Notifications: []domain.Notification{}, Loops: []domain.Loop{},
	}
}

func localSQLiteFixture() domain.Bootstrap {
	now := time.Now().UTC()
	viewer := domain.User{ID: "usr_zheng", Name: "zheng liu", DisplayName: "zheng liu", Email: "leo.zheng.liu@example.com", Active: true, EmailVerified: true}
	other := domain.User{ID: "usr_jiaozongben", Name: "other user", DisplayName: "other user", Email: "jiaozongben@gmail.com", Active: true, EmailVerified: true}
	team := domain.Team{ID: "team_cleantrack", Name: "Test team", Key: "CLE", Color: "#5E6AD2"}
	states := canonicalWorkflowStates()
	labels := canonicalLabels()
	project := domain.Project{ID: "project_aut", Name: "Test project", SlugID: "test-project", Summary: "Test project", Icon: "Project", Color: "#5E6AD2", Priority: 2, PriorityLabel: "High", Progress: .5, Health: "onTrack", Status: canonicalProjectStatuses()[2], Lead: &viewer, TeamIDs: []string{team.ID}, MemberIDs: []string{viewer.ID}, LabelIDs: []string{"label_product"}, CreatedAt: now, UpdatedAt: now, Milestones: []domain.ProjectMilestone{}}
	projectCruise := project
	projectCruise.ID, projectCruise.Name, projectCruise.SlugID = "project_cruise", "Test project two", "test-project-two"
	issue := func(id string, number int, title string, state domain.WorkflowState) domain.Issue {
		return domain.Issue{ID: id, Version: 1, Identifier: fmt.Sprintf("CLE-%d", number), Number: number, Title: title, Description: "Test issue", Priority: 2, PriorityLabel: "High", SortOrder: float64(number), CreatedAt: now, UpdatedAt: now, Team: team, State: state, Assignee: &viewer, Creator: other, Labels: []domain.IssueLabel{labels[0]}, SubscriberIDs: []string{viewer.ID, other.ID}, SubIssueIDs: []string{}, Relations: []domain.IssueRelation{}, Attachments: []domain.Attachment{}}
	}
	issues := []domain.Issue{issue("issue_1", 1, "Test requirement", states[1]), issue("issue_2", 2, "Test task", states[2]), issue("issue_33", 33, "Test legacy issue", states[1]), issue("issue_53156", 53156, "Test delivery task", states[1]), issue("issue_105130", 105130, "Test release task", states[1])}
	for index := range issues {
		issues[index].Project = &domain.ProjectSummary{ID: project.ID, Name: project.Name, Color: project.Color, Icon: project.Icon}
	}
	data := EmptyWorkspace("Test workspace", "cleantrack", "us", viewer)
	data.Workspace.ID = "workspace_cleantrack"
	data.Users = []domain.User{viewer, other}
	data.Teams = []domain.Team{team}
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
	data.Notifications = projectNotifications(&data)
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
		{ID: "label_type_requirement", Name: "IT需求", Color: "#5E6AD2", Description: "尚未拆分为执行任务的业务或产品需求", Scope: "Workspace", GroupID: "label_group_work_item_type"},
		{ID: "label_type_development", Name: "开发任务", Color: "#4AA3F7", Description: "开发或测试角色执行的交付任务", Scope: "Workspace", GroupID: "label_group_work_item_type"},
		{ID: "label_type_defect", Name: "缺陷", Color: "#F15B61", Description: "需要定位、修复与回归检查的缺陷", Scope: "Workspace", GroupID: "label_group_work_item_type"},
		{ID: "label_product", Name: "产品", Color: "#18B99A", Description: "产品规划与体验改进", Scope: "Workspace", ResourceType: "project", GroupID: "label_group_project_value"},
		{ID: "label_delivery", Name: "重点交付", Color: "#D97757", Description: "需要跨团队关注的重点交付", Scope: "Workspace", ResourceType: "project", GroupID: "label_group_project_delivery"},
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
		{ID: "label_group_work_item_type", Name: "工作项类型", Color: "#5E6AD2", Description: "工作项分类", Scope: "Workspace", ResourceType: "issue", CreatedAt: now},
		{ID: "label_group_project_value", Name: "Project value", Color: "#18B99A", Description: "项目业务价值与战略属性", Scope: "Workspace", ResourceType: "project", CreatedAt: now},
		{ID: "label_group_project_delivery", Name: "Project delivery", Color: "#D97757", Description: "项目交付特征与关注级别", Scope: "Workspace", ResourceType: "project", CreatedAt: now},
	}
}

func applyDeliveryLabelTaxonomy(issues []domain.Issue, labels []domain.IssueLabel) {
	byID := make(map[string]domain.IssueLabel, len(labels))
	for _, label := range labels {
		byID[label.ID] = label
	}
	for index := range issues {
		preserved := slices.DeleteFunc(append([]domain.IssueLabel{}, issues[index].Labels...), func(label domain.IssueLabel) bool {
			return label.ID == "" || label.ResourceType == "project" || obsoleteDeliveryLabelIDs[label.ID] || deliveryTaxonomyLabelIDs[label.ID]
		})
		typeID := deliveryTaxonomyForIssue(issues[index])
		if label, ok := byID[typeID]; ok {
			preserved = append(preserved, label)
		}
		issues[index].Labels = preserved
	}
}

func deliveryTaxonomyForIssue(issue domain.Issue) string {
	text := strings.ToLower(issue.Title + " " + issue.Description)
	legacy := func(ids ...string) bool {
		return slices.ContainsFunc(issue.Labels, func(label domain.IssueLabel) bool { return slices.Contains(ids, label.ID) })
	}
	if legacy("label_bug", "label_type_defect", "label_defect_created", "label_defect_assigned", "label_defect_fixing", "label_defect_checking", "label_defect_closed") || strings.Contains(text, " bug #") || strings.Contains(text, "缺陷") {
		return "label_type_defect"
	}
	if legacy("label_test", "label_test_case", "label_test_plan", "label_test_report", "label_uat", "label_dev_task", "label_ops_task", "label_backend", "label_type_development",
		"label_task_assigned", "label_task_implementing", "label_task_ready_for_test", "label_task_testing", "label_task_completed", "label_task_type_development", "label_task_type_testing",
		"label_version_created", "label_version_linking", "label_version_locked", "label_version_implementing", "label_version_delivered", "label_release_gate", "label_audit") ||
		strings.Contains(text, "任务 #") || strings.Contains(text, "测试") || strings.Contains(text, "uat") || strings.Contains(text, "前端") || strings.Contains(text, "backend") || strings.Contains(text, "web:") || strings.Contains(text, "production") || strings.Contains(text, "生产环境") || strings.Contains(text, "版本") || strings.Contains(text, "上线") || strings.Contains(text, "发布") || strings.Contains(text, "交付合规") {
		return "label_type_development"
	}
	return "label_type_requirement"
}

var obsoleteDeliveryLabelIDs = map[string]bool{
	"label_bug": true, "label_story": true, "label_test": true, "label_feature": true, "label_backend": true, "label_seal": true, "label_uat": true, "label_car_mall": true,
	"label_release_gate": true, "label_strategy": true, "label_business": true, "label_product_view": true, "label_dev_task": true, "label_ops_task": true,
	"label_test_case": true, "label_test_plan": true, "label_test_report": true, "label_delivery_data": true, "label_audit": true, "label_risk": true, "label_ai_transform": true,
	"label_team_cleantrack": true, "label_team_delivery": true, "label_team_quality": true,
	"label_req_pending_acceptance": true, "label_req_pending_implementation": true, "label_req_pending_verification": true, "label_req_pending_release": true, "label_req_delivered": true, "label_req_terminated": true,
	"label_task_assigned": true, "label_task_implementing": true, "label_task_ready_for_test": true, "label_task_testing": true, "label_task_completed": true, "label_task_type_development": true, "label_task_type_testing": true,
	"label_version_created": true, "label_version_linking": true, "label_version_locked": true, "label_version_implementing": true, "label_version_delivered": true,
	"label_defect_created": true, "label_defect_assigned": true, "label_defect_fixing": true, "label_defect_checking": true, "label_defect_closed": true,
}

var deliveryTaxonomyLabelIDs = map[string]bool{
	"label_type_requirement": true, "label_type_development": true, "label_type_defect": true,
}

func priorityLabel(priority int) string {
	switch priority {
	case 1:
		return "Urgent"
	case 2:
		return "High"
	case 3:
		return "Medium"
	case 4:
		return "Low"
	default:
		return "No priority"
	}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := [20]byte{}
	position := len(digits)
	for value > 0 {
		position--
		digits[position] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[position:])
}
