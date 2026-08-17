package store

import (
	"fmt"
	"strings"
	"time"

	"flow/api/internal/domain"
)

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
		Viewer:    viewer, Users: []domain.User{viewer}, Teams: []domain.Team{team}, Customers: []domain.Customer{}, States: canonicalWorkflowStates(), Labels: canonicalLabels(),
		Issues: []domain.Issue{}, Cycles: []domain.Cycle{}, CycleSettings: map[string]domain.CycleSettings{}, Projects: []domain.Project{},
		ProjectStatuses: canonicalProjectStatuses(), ProjectUpdates: map[string][]domain.ProjectUpdate{}, Initiatives: []domain.Initiative{},
		InitiativeUpdates: map[string][]domain.InitiativeUpdate{}, Comments: map[string][]domain.Comment{}, Activities: map[string][]domain.ActivityEvent{},
		SavedViews: []domain.SavedView{}, Notifications: []domain.Notification{},
	}
}

func Seed() domain.Bootstrap {
	now := time.Now().UTC()
	viewer := domain.User{ID: "usr_zheng", Name: "zheng liu", DisplayName: "zheng liu", Email: "leo.zheng.liu@example.com", Active: true}
	creator := domain.User{ID: "usr_jiaozongben", Name: "jiaozongben", DisplayName: "jiaozongben@gmail.com", Email: "jiaozongben@gmail.com", Active: true}
	team := domain.Team{ID: "team_cleantrack", Name: "Cleantrack", Key: "CLE", Color: "#5E6AD2"}
	states := canonicalWorkflowStates()
	projectStatuses := canonicalProjectStatuses()
	labels := canonicalLabels()
	cruise := domain.ProjectSummary{ID: "project_cruise", Name: "Cruise", Color: "#5E6AD2", Icon: "C"}
	aut := domain.ProjectSummary{ID: "project_aut", Name: "AUT AI 质量闭环 80%", Color: "#D97757", Icon: "A"}
	issue := func(id string, number int, title, description string, priority int, state domain.WorkflowState, created time.Time, issueLabels []domain.IssueLabel, project *domain.ProjectSummary) domain.Issue {
		return domain.Issue{ID: id, Identifier: "CLE-" + itoa(number), Number: number, Title: title, Description: description, Priority: priority, PriorityLabel: priorityLabel(priority), SortOrder: float64(number), CreatedAt: created, UpdatedAt: now, Team: team, State: state, Assignee: &viewer, Creator: creator, Labels: issueLabels, Project: project, SubscriberIDs: []string{viewer.ID, creator.ID}, Reactions: map[string][]string{}, SubIssueIDs: []string{}, Relations: []domain.IssueRelation{}, Attachments: []domain.Attachment{}}
	}
	issues := []domain.Issue{
		issue("issue_33", 33, "[Power Export] 生产排查：LR(tenant_id=2) 文书扫描 AI 创建批次后继续扫描时 TC Number 反复回填为 5T00833758", "生产环境出现问题：文书扫描在 AI 创建批次后，继续扫描新的收货单时，TC Number 会反复出现同一个值。\n\n当前排查重点是 OCR prompt 样例污染与 draft 生命周期。", 2, states[2], now.AddDate(0, -4, -10), labels[:2], &cruise),
		issue("issue_20", 20, "生产环境清洁任务 34865 的 after 房号照片拍成 2053，且 after 阶段未触发房号 OCR 校验", "检查 after 阶段 OCR 校验链路，并补充房号不一致时的阻断与提示。", 2, states[2], now.AddDate(0, -5, -8), []domain.IssueLabel{labels[0]}, nil),
		issue("issue_25", 25, "Web: supervisor inspection page sporadically shows Task not found due to stale cache or outdated task route", "Inspection route can reference an outdated task after deployment. Add route recovery and revalidation.", 3, states[0], now.AddDate(0, -4, -13), []domain.IssueLabel{labels[2]}, &cruise),
		issue("issue_26", 26, "Backend: supervisor cleaner detail can expose stale ASSIGNED taskId", "The aggregated room status and task detail may disagree. Recompute the active task reference from authoritative state.", 3, states[1], now.AddDate(0, -4, -12), []domain.IssueLabel{labels[0]}, &aut),
		issue("issue_24", 24, "RN Web: optional ISSUES step still forces jump to Report Issue in production", "Optional issue reporting must preserve the next workflow step when skipped.", 4, states[1], now.AddDate(0, -4, -14), []domain.IssueLabel{labels[2]}, &aut),
	}
	currentStart := cycleWeekStart(now)
	currentEnd := currentStart.AddDate(0, 0, 13)
	cycles := []domain.Cycle{
		{ID: "cycle_47", Number: 47, Name: "Cycle 47", TeamID: team.ID, StartsAt: currentStart.AddDate(0, 0, -14), EndsAt: currentStart.AddDate(0, 0, -1), Status: "completed", Capacity: 4, CreatedAt: currentStart.AddDate(0, 0, -42), UpdatedAt: now},
		{ID: "cycle_48", Number: 48, Name: "Cycle 48", TeamID: team.ID, StartsAt: currentStart, EndsAt: currentEnd, Status: "current", Capacity: 4, Favorite: true, CreatedAt: currentStart.AddDate(0, 0, -28), UpdatedAt: now},
		{ID: "cycle_49", Number: 49, Name: "Cycle 49", TeamID: team.ID, StartsAt: currentStart.AddDate(0, 0, 14), EndsAt: currentStart.AddDate(0, 0, 27), Status: "upcoming", Capacity: 4, CreatedAt: currentStart.AddDate(0, 0, -14), UpdatedAt: now},
		{ID: "cycle_50", Number: 50, Name: "Cycle 50", TeamID: team.ID, StartsAt: currentStart.AddDate(0, 0, 28), EndsAt: currentStart.AddDate(0, 0, 41), Status: "upcoming", Capacity: 4, CreatedAt: now, UpdatedAt: now},
	}
	issues[0].CycleID = stringPointer(cycles[1].ID)
	issues[1].CycleID = stringPointer(cycles[1].ID)
	issues[2].CycleID = stringPointer(cycles[1].ID)
	issues[3].CycleID = stringPointer(cycles[0].ID)
	issues[4].CycleID = stringPointer(cycles[2].ID)
	start := now.AddDate(0, -5, 0).Format("2006-01-02")
	target := now.AddDate(0, 2, 0).Format("2006-01-02")
	projects := []domain.Project{
		{ID: cruise.ID, Name: cruise.Name, SlugID: "cruise-01890584afbc", Summary: "Ship the inspection and workflow reliability improvements", Description: "Cruise groups the active production workflow reliability work.", Icon: cruise.Icon, Color: cruise.Color, Priority: 2, PriorityLabel: "High", Progress: .34, Health: "onTrack", Status: projectStatuses[2], Lead: &viewer, MemberIDs: []string{viewer.ID, creator.ID}, TeamIDs: []string{team.ID}, StartDate: &start, TargetDate: &target, IssueCount: 6, CreatedAt: now.AddDate(0, -5, 0), UpdatedAt: now},
		{ID: aut.ID, Name: aut.Name, SlugID: "aut-ai-quality", Summary: "95% - 质量 Agent 工作台试点化", Description: "Deliver the quality loop from issue capture through agent-assisted verification.", Icon: aut.Icon, Color: aut.Color, Priority: 2, PriorityLabel: "High", Progress: .67, Health: "noUpdate", Status: projectStatuses[2], Lead: &creator, MemberIDs: []string{creator.ID}, TeamIDs: []string{team.ID}, TargetDate: &target, IssueCount: 49, CreatedAt: now.AddDate(0, -6, 0), UpdatedAt: now},
		{ID: "project_test", Name: "TEST PROJECT", SlugID: "test-project", Summary: "test milestone 1", Icon: "T", Color: "#4CB782", Priority: 0, PriorityLabel: "No priority", Progress: 0, Health: "noUpdate", Status: projectStatuses[0], MemberIDs: []string{}, TeamIDs: []string{team.ID}, IssueCount: 0, CreatedAt: now.AddDate(0, -3, 0), UpdatedAt: now},
	}
	initiativeTarget := now.AddDate(0, 2, 0).Format("2006-01-02")
	initiatives := []domain.Initiative{{
		ID: "initiative_operational_excellence", Name: "Operational excellence", SlugID: "operational-excellence",
		Summary: "Make core workflows dependable at production scale", Description: "Coordinate the active reliability projects and keep their outcomes visible across the workspace.",
		Icon: "Initiative", Color: "#d15f64", Status: "active", Priority: 2, PriorityLabel: "High", Health: "onTrack",
		Owner: &viewer, LabelIDs: []string{}, ProjectIDs: []string{cruise.ID}, Resources: []domain.InitiativeResource{}, Comments: []domain.Comment{},
		TargetDate: &initiativeTarget, CreatedAt: now.AddDate(0, -2, 0), UpdatedAt: now,
	}}
	projects[0].Initiatives = []string{initiatives[0].ID}
	comments := map[string][]domain.Comment{"issue_33": {{ID: "comment_1", Body: "已完成修复并补充验证。生产只读查询确认固定 TC Number 来自 OCR prompt 中的示例值污染，draft 生命周期风险也已一并修复。", CreatedAt: now.AddDate(0, -4, -9), User: creator}}}
	activities := map[string][]domain.ActivityEvent{}
	for _, item := range issues {
		activities[item.ID] = []domain.ActivityEvent{{ID: "activity_" + item.ID, Type: "issue.created", CreatedAt: item.CreatedAt, Actor: creator, Metadata: map[string]string{}}}
	}
	projectUpdates := map[string][]domain.ProjectUpdate{
		cruise.ID: {{ID: "project_update_cruise_1", ProjectID: cruise.ID, Body: "The core workflow reliability work is progressing. We have narrowed the recurring TC number regression to the OCR prompt examples and are validating the production-safe fix.", Health: "onTrack", CreatedAt: now.AddDate(0, 0, -2), User: viewer}},
	}
	initiativeUpdates := map[string][]domain.InitiativeUpdate{
		initiatives[0].ID: {{ID: "initiative_update_operational_excellence_1", InitiativeID: initiatives[0].ID, Body: "The reliability program is moving forward with the current project scope and target intact.", Health: "onTrack", CreatedAt: now.AddDate(0, 0, -3), User: viewer, Comments: []domain.Comment{}, Reactions: map[string][]string{}}},
	}
	data := domain.Bootstrap{Workspace: domain.Workspace{ID: "workspace_cleantrack", Name: "cleantrack", URLKey: "cleantrack"}, Viewer: viewer, Users: []domain.User{viewer, creator}, Teams: []domain.Team{team}, Customers: []domain.Customer{}, States: states, Labels: labels, Issues: issues, Cycles: cycles, CycleSettings: map[string]domain.CycleSettings{team.ID: {Enabled: true, DurationWeeks: 2, CooldownWeeks: 0, StartsOn: 1, UpcomingCount: 2}}, Projects: projects, ProjectStatuses: projectStatuses, ProjectUpdates: projectUpdates, Initiatives: initiatives, InitiativeUpdates: initiativeUpdates, Comments: comments, Activities: activities, SavedViews: []domain.SavedView{}, Notifications: []domain.Notification{}}
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
		{ID: "ps_backlog", Name: "Backlog", Color: "#6B6F76", Type: "backlog"},
		{ID: "ps_planned", Name: "Planned", Color: "#D6B326", Type: "planned"},
		{ID: "ps_progress", Name: "In Progress", Color: "#5E8FD8", Type: "started"},
		{ID: "ps_completed", Name: "Completed", Color: "#5E6AD2", Type: "completed"},
		{ID: "ps_canceled", Name: "Canceled", Color: "#77777C", Type: "canceled"},
	}
}

func canonicalWorkflowStates() []domain.WorkflowState {
	return []domain.WorkflowState{
		{ID: "state_backlog", Name: "Backlog", Color: "#6B6F76", Type: "backlog", Position: 0, Default: true},
		{ID: "state_todo", Name: "Todo", Color: "#E2E2E2", Type: "unstarted", Position: 1},
		{ID: "state_progress", Name: "In Progress", Color: "#F2C94C", Type: "started", Position: 2},
		{ID: "state_done", Name: "Done", Color: "#5E6AD2", Type: "completed", Position: 3},
		{ID: "state_canceled", Name: "Canceled", Color: "#A8B2C1", Type: "canceled", Position: 4},
		{ID: "state_duplicate", Name: "Duplicate", Color: "#A8B2C1", Type: "canceled", Position: 5, Reserved: true},
	}
}

func canonicalLabels() []domain.IssueLabel {
	return []domain.IssueLabel{
		{ID: "label_bug", Name: "Bug", Color: "#F15B61", Description: "Issues that do not work as intended", IssueCount: 14, Scope: "Workspace"},
		{ID: "label_power", Name: "Power Export", Color: "#4C70F0", Description: "Work related to Power Export", IssueCount: 9, Scope: "Workspace"},
		{ID: "label_aut_ai", Name: "AUT-AI", Color: "#3B82F6", Description: "AUT-AI workflow and quality automation", IssueCount: 12, Scope: "Workspace"},
		{ID: "label_claude", Name: "Claude", Color: "#8B5CF6", Description: "Implementation lane intended for Claude Code", IssueCount: 27, Scope: "Workspace"},
		{ID: "label_codex", Name: "Codex", Color: "#18B99A", Description: "Implementation lane intended for Codex", IssueCount: 18, Scope: "Workspace"},
		{ID: "label_cruise", Name: "Cruise", Color: "#F4C21A", Description: "Work tracked by the Cruise project", IssueCount: 6, Scope: "Workspace"},
		{ID: "label_external_truth", Name: "External-Truth", Color: "#F6A11A", Description: "Evidence verified against an external source", IssueCount: 8, Scope: "Workspace"},
		{ID: "label_feature", Name: "Feature", Color: "#A56BEA", Description: "New product capability", IssueCount: 11, Scope: "Workspace"},
		{ID: "label_improvement", Name: "Improvement", Color: "#4AA3F7", Description: "Incremental product improvement", IssueCount: 16, Scope: "Workspace"},
		{ID: "label_live_evidence", Name: "Live-Evidence", Color: "#22A9E0", Description: "Evidence collected from a live environment", IssueCount: 10, Scope: "Workspace"},
		{ID: "label_milestone_90", Name: "Milestone-90", Color: "#667085", Description: "Milestone 90 delivery tracking", IssueCount: 5, Scope: "Workspace"},
		{ID: "label_no_regression", Name: "No-Regression", Color: "#84CC16", Description: "Regression prevention and verification", IssueCount: 13, Scope: "Workspace"},
		{ID: "label_pattern_reuse", Name: "Pattern-Reuse", Color: "#A855F7", Description: "Reusable implementation pattern", IssueCount: 7, Scope: "Workspace"},
		{ID: "label_release_gate", Name: "Release-Gate", Color: "#F05252", Description: "Release readiness gate", IssueCount: 4, Scope: "Workspace"},
		{ID: "label_ui_evidence", Name: "UI-Evidence", Color: "#21B5AB", Description: "UI behavior supported by visual evidence", IssueCount: 9, Scope: "Workspace"},
	}
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
	pos := len(digits)
	for value > 0 {
		pos--
		digits[pos] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[pos:])
}
