package store

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
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
		Viewer:    viewer, Users: []domain.User{viewer}, Teams: []domain.Team{team}, Customers: []domain.Customer{}, States: canonicalWorkflowStates(), Labels: canonicalLabels(), LabelGroups: canonicalLabelGroups(),
		Issues: []domain.Issue{}, Cycles: []domain.Cycle{}, CycleSettings: map[string]domain.CycleSettings{}, Projects: []domain.Project{},
		ProjectStatuses: canonicalProjectStatuses(), ProjectUpdates: map[string][]domain.ProjectUpdate{}, Initiatives: []domain.Initiative{},
		InitiativeUpdates: map[string][]domain.InitiativeUpdate{}, Comments: map[string][]domain.Comment{}, Activities: map[string][]domain.ActivityEvent{},
		SavedViews: []domain.SavedView{}, Notifications: []domain.Notification{},
	}
}

func Seed() domain.Bootstrap {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("FLOW_SEED_PROFILE")), "zentao-demo") {
		return zentaoDemoSeed()
	}
	return baseSeed()
}

func baseSeed() domain.Bootstrap {
	now := time.Now().UTC()
	viewer := domain.User{ID: "usr_zheng", Name: "zheng liu", DisplayName: "zheng liu", Email: "leo.zheng.liu@example.com", Active: true}
	creator := domain.User{ID: "usr_jiaozongben", Name: "jiaozongben", DisplayName: "jiaozongben@gmail.com", Email: "jiaozongben@gmail.com", Active: true}
	team := domain.Team{ID: "team_cleantrack", Name: "Cleantrack", Key: "CLE", Color: "#5E6AD2"}
	states := canonicalWorkflowStates()
	projectStatuses := canonicalProjectStatuses()
	labels := append(canonicalLabels(), make([]domain.IssueLabel, 3)...)
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
	applyDeliveryLabelTaxonomy(issues, labels)
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
	data := domain.Bootstrap{Workspace: domain.Workspace{ID: "workspace_cleantrack", Name: "cleantrack", URLKey: "cleantrack"}, Viewer: viewer, Users: []domain.User{viewer, creator}, Teams: []domain.Team{team}, Customers: []domain.Customer{}, States: states, Labels: canonicalLabels(), LabelGroups: canonicalLabelGroups(), Issues: issues, Cycles: cycles, CycleSettings: map[string]domain.CycleSettings{team.ID: {Enabled: true, DurationWeeks: 2, CooldownWeeks: 0, StartsOn: 1, UpcomingCount: 2}}, Projects: projects, ProjectStatuses: projectStatuses, ProjectUpdates: projectUpdates, Initiatives: initiatives, InitiativeUpdates: initiativeUpdates, Comments: comments, Activities: activities, SavedViews: []domain.SavedView{}, Notifications: []domain.Notification{}}
	data.Notifications = projectNotifications(&data)
	return data
}

func zentaoDemoSeed() domain.Bootstrap {
	now := time.Now().UTC()
	viewer := domain.User{ID: "usr_zheng", Name: "刘峥", DisplayName: "刘峥", Email: "leo.zheng.liu@example.com", Active: true, EmailVerified: true}
	guan := domain.User{ID: "usr_jiaozongben", Name: "管献甫", DisplayName: "管献甫", Email: "demo+guan@example.com", Active: true, EmailVerified: true}
	liquan := domain.User{ID: "usr_liquan", Name: "李全", DisplayName: "李全", Email: "demo+liquan@example.com", Active: true, EmailVerified: true}
	jianyan := domain.User{ID: "usr_jianyan", Name: "简雁", DisplayName: "简雁", Email: "demo+jianyan@example.com", Active: true, EmailVerified: true}
	liyanlu := domain.User{ID: "usr_liyanlu", Name: "李言路", DisplayName: "李言路", Email: "demo+liyanlu@example.com", Active: true, EmailVerified: true}
	lijiangtao := domain.User{ID: "usr_lijiangtao", Name: "李江涛", DisplayName: "李江涛", Email: "demo+lijiangtao@example.com", Active: true, EmailVerified: true}
	luoyuying := domain.User{ID: "usr_luoyuying", Name: "罗玉莹", DisplayName: "罗玉莹", Email: "demo+luoyuying@example.com", Active: true, EmailVerified: true}
	yuxiangyang := domain.User{ID: "usr_yuxiangyang", Name: "于向阳", DisplayName: "于向阳", Email: "demo+yuxiangyang@example.com", Active: true, EmailVerified: true}
	yumingyang := domain.User{ID: "usr_yumingyang", Name: "于洺洋", DisplayName: "于洺洋", Email: "demo+yumingyang@example.com", Active: true, EmailVerified: true}
	zhangqun := domain.User{ID: "usr_zhangqun", Name: "张群", DisplayName: "张群", Email: "demo+zhangqun@example.com", Active: true, EmailVerified: true}
	jiangyaling := domain.User{ID: "usr_jiangyaling", Name: "姜亚令", DisplayName: "姜亚令", Email: "demo+jiangyaling@example.com", Active: true, EmailVerified: true}
	dingyu := domain.User{ID: "usr_dingyu", Name: "丁宇", DisplayName: "丁宇", Email: "demo+dingyu@example.com", Active: true, EmailVerified: true}
	users := []domain.User{viewer, guan, liquan, jianyan, liyanlu, lijiangtao, luoyuying, yuxiangyang, yumingyang, zhangqun, jiangyaling, dingyu}
	team := domain.Team{ID: "team_cleantrack", Name: "智能印控产品", Key: "CLE", Color: "#5E6AD2", Icon: "印"}
	deliveryTeam := domain.Team{ID: "team_delivery", Name: "车商城交付", Key: "CAR", Color: "#2F80ED", Icon: "车"}
	qualityTeam := domain.Team{ID: "team_quality", Name: "质量与测试", Key: "QA", Color: "#2D9D78", Icon: "测"}
	teams := []domain.Team{team, deliveryTeam, qualityTeam}
	states := canonicalWorkflowStates()
	projectStatuses := canonicalProjectStatuses()
	labels := append(canonicalLabels(), make([]domain.IssueLabel, 3)...)
	label := func(id string) domain.IssueLabel {
		for _, item := range labels {
			if item.ID == id {
				return item
			}
		}
		return domain.IssueLabel{}
	}
	sealPlatform := domain.ProjectSummary{ID: "project_cruise", Name: "智能印控平台(S04763)", Color: "#5E6AD2", Icon: "印"}
	carMall := domain.ProjectSummary{ID: "project_aut", Name: "汽车之家车商城项目2026", Color: "#2F80ED", Icon: "车"}
	issue := func(id string, number int, title, description string, priority int, issueTeam domain.Team, state domain.WorkflowState, assignee, creator domain.User, issueLabels []domain.IssueLabel, project *domain.ProjectSummary, created time.Time) domain.Issue {
		item := domain.Issue{ID: id, Version: 1, Identifier: issueTeam.Key + "-" + itoa(number), Number: number, Title: title, Description: description, Priority: priority, PriorityLabel: priorityLabel(priority), SortOrder: float64(number), CreatedAt: created, UpdatedAt: now, Team: issueTeam, State: state, Assignee: &assignee, Creator: creator, Labels: issueLabels, Project: project, SubscriberIDs: []string{viewer.ID, assignee.ID, creator.ID}, Reactions: map[string][]string{}, SubIssueIDs: []string{}, Relations: []domain.IssueRelation{}, Attachments: []domain.Attachment{}}
		if state.Type == "completed" {
			completed := created.AddDate(0, 0, 5)
			item.CompletedAt = &completed
		}
		return item
	}
	issues := []domain.Issue{
		issue("issue_33", 112329, "【印章外带刻制】流程被驳回后，待办当前节点应展示已驳回", "来源于禅道研发需求 #112329。统一驳回状态在待办列表与流程详情中的展示，并补充回归验证。", 3, team, states[1], liyanlu, liquan, []domain.IssueLabel{labels[1], labels[5], label("label_team_cleantrack")}, &sealPlatform, now.AddDate(0, 0, -12)),
		issue("issue_20", 112091, "【印章内部交接】被驳回后，待办当前流程节点应展示被驳回", "来源于禅道研发需求 #112091。交接流程驳回后需要同步节点文案和状态。", 3, team, states[2], liquan, lijiangtao, []domain.IssueLabel{labels[1], labels[5]}, &sealPlatform, now.AddDate(0, 0, -18)),
		issue("issue_25", 112029, "【印章内部交接】重新发起时仍应展示公议流程", "公议过程中被驳回后，发起人重新发起时应保留公议流程入口。来源于禅道研发需求 #112029。", 3, team, states[0], liquan, lijiangtao, []domain.IssueLabel{labels[1], labels[5]}, &sealPlatform, now.AddDate(0, -1, -2)),
		issue("issue_26", 100718, "印签管理中台 SIT/UAT 测试服务", "覆盖测试用例、接口自动化、功能测试、系统测试、压力测试及测试报告输出。来源于禅道研发需求 #100718。", 1, qualityTeam, states[2], jianyan, viewer, []domain.IssueLabel{labels[2], labels[6]}, &sealPlatform, now.AddDate(0, -1, -10)),
		issue("issue_24", 93544, "承诺书管理及签署功能", "完成承诺书模板、发起、签署、归档和查询闭环。来源于禅道研发需求 #93544。", 3, team, states[1], jianyan, liquan, []domain.IssueLabel{labels[1], labels[3]}, &sealPlatform, now.AddDate(0, -2, -4)),
		issue("issue_56329", 56329, "【印章刻制】历史印章停用后未生成新的印章编码", "刻制完成后历史印章已停用，但新印章记录未生成，需检查编码后缀生成与事务提交。来源于禅道 Bug #56329。", 2, team, states[2], liyanlu, liquan, []domain.IssueLabel{labels[0], labels[5]}, &sealPlatform, now.AddDate(0, -1, -14)),
		issue("issue_55316", 55316, "【流程查询】有权限人员只能看到申请信息", "流程查询页应允许有权限人员查看各节点页面，但不可处理。来源于禅道 Bug #55316。", 2, team, states[3], liquan, liquan, []domain.IssueLabel{labels[0], labels[4]}, &sealPlatform, now.AddDate(0, -2, -18)),
		issue("issue_55092", 55092, "【印章外部交接】已交接印章仍出现在可选列表", "已完成外部交接的印章再次发起时仍可被选中。来源于禅道 Bug #55092。", 3, team, states[3], jianyan, jianyan, []domain.IssueLabel{labels[0], labels[5]}, &sealPlatform, now.AddDate(0, -2, -22)),
		issue("issue_54647", 54647, "【印章外带刻制】撤回确认后无法再次发起申请", "接收撤回确认后，印章状态未恢复到可再次发起外带刻制的状态。来源于禅道 Bug #54647。", 3, team, states[1], jianyan, jianyan, []domain.IssueLabel{labels[0], labels[5]}, &sealPlatform, now.AddDate(0, -3, -8)),
		issue("issue_54561", 54561, "【印章销毁】法务审核通过后节点仍显示未处理", "法务审核通过后流程节点状态未刷新。来源于禅道 Bug #54561。", 3, team, states[3], liquan, liquan, []domain.IssueLabel{labels[0], labels[4]}, &sealPlatform, now.AddDate(0, -3, -15)),
		issue("issue_53156", 53156, "【销单暂存】按揭单暂存时提示首付不能为 0", "预定转出售第一步暂存时不应触发提交态校验。来源于汽车之家车商城项目 Bug #53156。", 2, deliveryTeam, states[3], luoyuying, guan, []domain.IssueLabel{labels[0], labels[7]}, &carMall, now.AddDate(0, -4, -20)),
		issue("issue_53063", 53063, "【白名单状态流转】提报成功后页面持续重复加载", "MMC 提报成功后状态轮询未正确退出，页面持续加载。来源于汽车之家车商城项目 Bug #53063。", 1, deliveryTeam, states[2], yuxiangyang, luoyuying, []domain.IssueLabel{labels[0], labels[4]}, &carMall, now.AddDate(0, -4, -21)),
		issue("issue_100417", 100417, "【前端-PC】Image 图片预览组件封装", "沉淀统一图片预览组件，支持缩放、旋转、下载和键盘切换。来源于禅道任务 #100417。", 3, deliveryTeam, states[3], yumingyang, guan, []domain.IssueLabel{labels[3], labels[7], label("label_dev_task")}, &carMall, now.AddDate(0, -5, -2)),
		issue("issue_100879", 100879, "【测试】验证门店与固定金融产品关联", "覆盖门店白名单、产品关联和异常配置场景。来源于禅道任务 #100879。", 3, qualityTeam, states[3], jianyan, guan, []domain.IssueLabel{labels[2], labels[7]}, &carMall, now.AddDate(0, -5, -16)),
		issue("issue_105130", 105130, "车商城二期-订单流程", "来源于禅道研发需求 #105130。作为订单流程需求主项，向下拆解开发、测试和缺陷任务。", 2, deliveryTeam, states[3], luoyuying, guan, []domain.IssueLabel{label("label_story"), label("label_business"), label("label_product_view"), label("label_car_mall"), label("label_team_delivery")}, &carMall, now.AddDate(0, -6, -2)),
		issue("issue_108415", 108415, "【业务需求】车商城 C 端增加无报告车源外展", "来源于禅道研发需求 #108415。业务诉求经产品澄清后进入交付，覆盖无报告车源展示与转化链路。", 2, deliveryTeam, states[2], luoyuying, guan, []domain.IssueLabel{label("label_story"), label("label_business"), label("label_product_view"), label("label_car_mall")}, &carMall, now.AddDate(0, -5, -8)),
		issue("issue_100474", 100474, "【事务】316 车商城延保订单禅道问题日清", "来源于禅道任务 #100474。每日收敛延保订单问题，协调开发、测试与线上反馈，任务已关闭。", 3, deliveryTeam, states[3], yumingyang, guan, []domain.IssueLabel{label("label_ops_task"), label("label_car_mall")}, &carMall, now.AddDate(0, -4, -18)),
		issue("issue_100880", 100880, "【测试】压测接口参数整理", "来源于禅道任务 #100880。整理压力测试接口、参数、数据准备和验收口径。", 3, qualityTeam, states[3], dingyu, guan, []domain.IssueLabel{label("label_test"), label("label_test_plan"), label("label_car_mall")}, &carMall, now.AddDate(0, -5, -18)),
		issue("issue_101479", 101479, "【测试】添加白名单后订单状态自动流转回归", "来源于禅道任务 #101479。验证白名单配置、订单状态自动流转及已修复问题。", 2, qualityTeam, states[3], jiangyaling, guan, []domain.IssueLabel{label("label_test"), label("label_uat"), label("label_car_mall")}, &carMall, now.AddDate(0, -4, -23)),
		issue("issue_49219", 49219, "【测试用例】外带刻制申请-公议审批节点", "来源于禅道测试用例 #49219，优先级 3，创建人简雁；抓取时状态为未执行。", 3, qualityTeam, states[0], jianyan, jianyan, []domain.IssueLabel{label("label_test_case"), label("label_uat"), label("label_seal")}, &sealPlatform, now.AddDate(0, 0, -9)),
		issue("issue_49216", 49216, "【测试用例】印章内部交接-终审", "来源于禅道测试用例 #49216，覆盖内部交接终审节点；抓取时状态为未执行。", 3, qualityTeam, states[0], jianyan, jianyan, []domain.IssueLabel{label("label_test_case"), label("label_seal")}, &sealPlatform, now.AddDate(0, 0, -9)),
		issue("issue_49215", 49215, "【测试用例】印章内部交接-公议流程", "来源于禅道测试用例 #49215，覆盖公议流程；抓取时状态为未执行。", 3, qualityTeam, states[0], jianyan, jianyan, []domain.IssueLabel{label("label_test_case"), label("label_seal")}, &sealPlatform, now.AddDate(0, 0, -9)),
		issue("issue_52526", 52526, "【测试】【车智汇发车】车辆详情组件与图片加载慢", "来源于禅道 Bug #52526。车辆详情页组件和图片加载慢，影响用户体验；项目动态显示已关闭。", 3, qualityTeam, states[3], zhangqun, jiangyaling, []domain.IssueLabel{label("label_bug"), label("label_test"), label("label_car_mall")}, &carMall, now.AddDate(0, -4, -2)),
		issue("issue_test_plan", 120001, "智能印控平台 SIT/UAT 测试方案", "平台特性维度：测试方案。Flow 当前没有独立测试计划模型，本项以 Issue 承载计划状态，并关联测试方案文档和禅道用例。", 1, qualityTeam, states[2], jianyan, viewer, []domain.IssueLabel{label("label_test"), label("label_test_plan"), label("label_uat"), label("label_release_gate"), label("label_team_quality")}, &sealPlatform, now.AddDate(0, 0, -7)),
		issue("issue_test_report", 120002, "智能印控平台 2026.08 测试报告", "平台特性维度：测试报告。禅道产品共有 11 个测试用例，抓取时已执行 0 个；本项用于跟踪报告产出，不代表已通过测试。", 1, qualityTeam, states[1], jianyan, viewer, []domain.IssueLabel{label("label_test"), label("label_test_report"), label("label_uat"), label("label_risk")}, &sealPlatform, now.AddDate(0, 0, -4)),
		issue("issue_release_review", 120003, "智能印控平台 2026.08 上线评审", "平台特性维度：上线评审。关联评审报告、测试报告、版本范围和未完成门禁。Flow 当前以 Issue 模拟评审单。", 1, qualityTeam, states[1], viewer, liquan, []domain.IssueLabel{label("label_release_gate"), label("label_audit"), label("label_risk")}, &sealPlatform, now.AddDate(0, 0, -3)),
		issue("issue_delivery_metrics", 120004, "收集车商城项目交付过程数据", "平台特性维度：交付过程数据收集。禅道快照：64 人、累计消耗 5121.5h、剩余需求 15、剩余任务 4、剩余 Bug 1。", 2, deliveryTeam, states[2], viewer, guan, []domain.IssueLabel{label("label_delivery_data"), label("label_business"), label("label_car_mall")}, &carMall, now.AddDate(0, 0, -5)),
		issue("issue_ai_enablement", 120005, "AI 原生交付人员能力转型试点", "平台特性维度：人员转型。通过需求结构化、测试辅助和交付数据分析验证 AI 转型效果。", 3, deliveryTeam, states[1], viewer, guan, []domain.IssueLabel{label("label_ai_transform"), label("label_strategy")}, &carMall, now.AddDate(0, 0, -2)),
		issue("issue_audit_gate", 120006, "版本交付合规检查：流程、标准与安全", "平台特性维度：审计视角。按订单、评估、竞价、执行、验收、评价、付款检查交付证据；预算与费用仅能记录在文档中。", 2, qualityTeam, states[1], viewer, liquan, []domain.IssueLabel{label("label_audit"), label("label_release_gate")}, &sealPlatform, now.AddDate(0, 0, -2)),
	}
	applyDeliveryLabelTaxonomy(issues, labels)
	parent := func(childID, parentID string) {
		for index := range issues {
			if issues[index].ID == childID {
				issues[index].ParentID = stringPointer(parentID)
			}
			if issues[index].ID == parentID {
				issues[index].SubIssueIDs = append(issues[index].SubIssueIDs, childID)
			}
		}
	}
	parent("issue_100417", "issue_105130")
	parent("issue_100879", "issue_105130")
	parent("issue_53156", "issue_105130")
	parent("issue_53063", "issue_105130")
	parent("issue_100880", "issue_26")
	parent("issue_test_plan", "issue_26")
	parent("issue_49219", "issue_33")
	parent("issue_49216", "issue_20")
	parent("issue_49215", "issue_20")
	parent("issue_test_report", "issue_test_plan")
	parent("issue_release_review", "issue_test_report")
	currentStart := cycleWeekStart(now)
	currentEnd := currentStart.AddDate(0, 0, 13)
	cycles := []domain.Cycle{
		{ID: "cycle_47", Number: 47, Name: "Cycle 47", TeamID: team.ID, StartsAt: currentStart.AddDate(0, 0, -14), EndsAt: currentStart.AddDate(0, 0, -1), Status: "completed", Capacity: 4, CreatedAt: currentStart.AddDate(0, 0, -42), UpdatedAt: now},
		{ID: "cycle_48", Number: 48, Name: "Cycle 48", TeamID: team.ID, StartsAt: currentStart, EndsAt: currentEnd, Status: "current", Capacity: 4, Favorite: true, CreatedAt: currentStart.AddDate(0, 0, -28), UpdatedAt: now},
		{ID: "cycle_49", Number: 49, Name: "Cycle 49", TeamID: team.ID, StartsAt: currentStart.AddDate(0, 0, 14), EndsAt: currentStart.AddDate(0, 0, 27), Status: "upcoming", Capacity: 4, CreatedAt: currentStart.AddDate(0, 0, -14), UpdatedAt: now},
		{ID: "cycle_50", Number: 50, Name: "Cycle 50", TeamID: team.ID, StartsAt: currentStart.AddDate(0, 0, 28), EndsAt: currentStart.AddDate(0, 0, 41), Status: "upcoming", Capacity: 4, CreatedAt: now, UpdatedAt: now},
		{ID: "cycle_car_phase1", Number: 1, Name: "车商城一期迭代", Description: "禅道快照：计划完成 2026-07-10，预计 519h，消耗 591h，进度 100%，状态为已延期。", TeamID: deliveryTeam.ID, StartsAt: time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC), EndsAt: time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC), Status: "completed", Capacity: 64, CreatedAt: time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC), UpdatedAt: now},
		{ID: "cycle_car_316", Number: 316, Name: "车商城316迭代", Description: "禅道快照：计划完成 2026-08-01，预计 3885.5h，消耗 3973h，进度 100%，状态为已延期。", TeamID: deliveryTeam.ID, StartsAt: time.Date(2026, 3, 16, 0, 0, 0, 0, time.UTC), EndsAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), Status: "completed", Capacity: 64, Favorite: true, CreatedAt: time.Date(2026, 3, 16, 0, 0, 0, 0, time.UTC), UpdatedAt: now},
		{ID: "cycle_quality_uat", Number: 8, Name: "智能印控 2026.08 UAT", Description: "测试方案、11 个禅道用例、测试报告与上线评审的演示周期。", TeamID: qualityTeam.ID, StartsAt: currentStart, EndsAt: currentEnd, Status: "current", Capacity: 8, Favorite: true, CreatedAt: currentStart.AddDate(0, 0, -14), UpdatedAt: now},
	}
	for index := range issues {
		if issues[index].Team.ID == team.ID && index < 6 {
			issues[index].CycleID = stringPointer(cycles[1].ID)
		}
		if issues[index].Project != nil && issues[index].Project.ID == carMall.ID {
			issues[index].CycleID = stringPointer("cycle_car_316")
		}
		if issues[index].Team.ID == qualityTeam.ID && issues[index].Project != nil && issues[index].Project.ID == sealPlatform.ID {
			issues[index].CycleID = stringPointer("cycle_quality_uat")
		}
	}
	start := now.AddDate(0, -5, 0).Format("2006-01-02")
	target := now.AddDate(0, 3, 0).Format("2006-01-02")
	carStart := "2026-01-04"
	carTarget := "2027-01-03"
	projects := []domain.Project{
		{ID: sealPlatform.ID, Name: sealPlatform.Name, SlugID: "smart-seal-platform", Summary: "覆盖印章全生命周期与流程协同", Description: "禅道样本产品，包含印章刻制、补录、停启用、销毁、内外交接、承诺书和流程查询等模块。", Icon: sealPlatform.Icon, Color: sealPlatform.Color, Priority: 2, PriorityLabel: "High", Progress: .71, Health: "atRisk", Status: projectStatuses[2], Lead: &liquan, MemberIDs: []string{liquan.ID, jianyan.ID, liyanlu.ID, lijiangtao.ID, viewer.ID}, TeamIDs: []string{team.ID, qualityTeam.ID}, LabelIDs: []string{"label_product"}, StartDate: &start, TargetDate: &target, IssueCount: 10, CreatedAt: now.AddDate(0, -8, 0), UpdatedAt: now},
		{ID: carMall.ID, Name: carMall.Name, SlugID: "autohome-car-mall-2026", Summary: "车商城二期订单、交付与金融流程迭代", Description: "来源于禅道项目 #10155。原项目包含 21 条研发需求、628 个任务和 606 个 Bug；管理快照为 64 人、累计消耗 5121.5h、剩余需求 15、任务 4、Bug 1。本地仅导入代表性事项。", Icon: carMall.Icon, Color: carMall.Color, Priority: 2, PriorityLabel: "High", Progress: .82, Health: "atRisk", Status: projectStatuses[2], Lead: &guan, MemberIDs: []string{guan.ID, luoyuying.ID, yuxiangyang.ID, yumingyang.ID, zhangqun.ID, jiangyaling.ID, dingyu.ID, viewer.ID}, TeamIDs: []string{deliveryTeam.ID, qualityTeam.ID}, LabelIDs: []string{"label_delivery"}, StartDate: &carStart, TargetDate: &carTarget, IssueCount: 4, CreatedAt: time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC), UpdatedAt: now},
		{ID: "project_it_community", Name: "IT技术社区", SlugID: "it-community", Summary: "技术内容、活动与研发协作社区", Icon: "社", Color: "#9B51E0", Priority: 3, PriorityLabel: "Medium", Progress: .58, Health: "onTrack", Status: projectStatuses[2], Lead: &viewer, MemberIDs: []string{viewer.ID, guan.ID}, TeamIDs: []string{deliveryTeam.ID}, IssueCount: 8, CreatedAt: now.AddDate(0, -7, 0), UpdatedAt: now},
		{ID: "project_etl", Name: "自动ETL采集实现", SlugID: "automated-etl", Summary: "统一数据采集、校验和任务监控", Icon: "数", Color: "#2D9D78", Priority: 2, PriorityLabel: "High", Progress: .64, Health: "onTrack", Status: projectStatuses[2], Lead: &yuxiangyang, MemberIDs: []string{yuxiangyang.ID, lijiangtao.ID}, TeamIDs: []string{deliveryTeam.ID}, IssueCount: 12, CreatedAt: now.AddDate(0, -6, 0), UpdatedAt: now},
		{ID: "project_audit", Name: "稽核中心", SlugID: "audit-center", Summary: "稽核规则、任务和问题闭环", Icon: "稽", Color: "#D97757", Priority: 2, PriorityLabel: "High", Progress: .76, Health: "atRisk", Status: projectStatuses[2], Lead: &guan, MemberIDs: []string{guan.ID, jianyan.ID}, TeamIDs: []string{qualityTeam.ID}, IssueCount: 7, CreatedAt: now.AddDate(0, -10, 0), UpdatedAt: now},
		{ID: "project_test", Name: "高管工作台体验迭代", SlugID: "executive-workbench", Summary: "关键经营指标与待办体验优化", Icon: "高", Color: "#4CB782", Priority: 3, PriorityLabel: "Medium", Progress: .43, Health: "noUpdate", Status: projectStatuses[1], Lead: &viewer, MemberIDs: []string{viewer.ID, luoyuying.ID}, TeamIDs: []string{deliveryTeam.ID}, IssueCount: 3, CreatedAt: now.AddDate(0, -3, 0), UpdatedAt: now},
	}
	for index := range projects {
		count := 0
		for _, item := range issues {
			if item.Project != nil && item.Project.ID == projects[index].ID {
				count++
			}
		}
		projects[index].IssueCount = count
	}
	initiativeTarget := now.AddDate(0, 4, 0).Format("2006-01-02")
	initiatives := []domain.Initiative{{
		ID: "initiative_operational_excellence", Name: "企业流程体验升级", SlugID: "enterprise-process-experience",
		Summary: "从企业流程体验战略分解业务价值与交付目标", Description: "战略层承接行业与企业目标，分解提效、降本、增收、节奏、质量和 AI 转型指标；聚合印控、车商城、稽核和数据采集项目，形成诉求、需求、任务、测试、发布、审计和运营改进闭环。",
		Icon: "Initiative", Color: "#d15f64", Status: "active", Priority: 2, PriorityLabel: "High", Health: "onTrack",
		Owner: &viewer, LabelIDs: []string{}, ProjectIDs: []string{sealPlatform.ID, carMall.ID, "project_audit"}, Resources: []domain.InitiativeResource{}, Comments: []domain.Comment{},
		TargetDate: &initiativeTarget, CreatedAt: now.AddDate(0, -2, 0), UpdatedAt: now,
	}}
	projects[0].Initiatives = []string{initiatives[0].ID}
	projects[1].Initiatives = []string{initiatives[0].ID}
	projects[4].Initiatives = []string{initiatives[0].ID}
	comments := map[string][]domain.Comment{
		"issue_56329":            {{ID: "comment_1", Version: 1, Body: "已复现：历史印章停用成功，但新编码生成事务被提前返回。修复后补充刻制、撤回和重试场景回归。", Reactions: map[string][]string{}, CreatedAt: now.AddDate(0, 0, -2), User: liyanlu}},
		"issue_test_plan":        {{ID: "comment_test_plan_1", Version: 1, Body: "已从禅道导入 11 个智能印控测试用例，当前执行数为 0。先完成公议流程、终审和外带刻制申请 3 个高关联用例。", Reactions: map[string][]string{"eyes": {viewer.ID}}, CreatedAt: now.AddDate(0, 0, -2), User: jianyan}},
		"issue_test_report":      {{ID: "comment_test_report_1", Version: 1, Body: "报告暂不满足上线条件：11 个用例尚未执行，且仍有高优缺陷处理中。", Reactions: map[string][]string{}, CreatedAt: now.AddDate(0, 0, -1), User: jianyan}},
		"issue_release_review":   {{ID: "comment_release_review_1", Version: 1, Body: "评审结论待定。需补齐测试执行结果、回滚方案和代码仓关联后再做上线决策。", Reactions: map[string][]string{}, CreatedAt: now.AddDate(0, 0, -1), User: viewer}},
		"issue_delivery_metrics": {{ID: "comment_delivery_metrics_1", Version: 1, Body: "禅道快照显示两个迭代均为 100% 但已延期，实际消耗分别高于预计 87.5h 和 72h，建议纳入节奏与承载改进清单。", Reactions: map[string][]string{}, CreatedAt: now.AddDate(0, 0, -2), User: guan}},
	}
	activities := map[string][]domain.ActivityEvent{}
	for _, item := range issues {
		activities[item.ID] = []domain.ActivityEvent{{ID: "activity_" + item.ID, Type: "issue.created", CreatedAt: item.CreatedAt, Actor: item.Creator, Metadata: map[string]string{"source": "ZenTao demo sample"}}}
	}
	activities["issue_101479"] = append(activities["issue_101479"],
		domain.ActivityEvent{ID: "activity_issue_101479_assigned", Type: "issue.updated", CreatedAt: now.AddDate(0, -4, -23).Add(2 * time.Minute), Actor: jiangyaling, Metadata: map[string]string{"assignee": jiangyaling.DisplayName}},
		domain.ActivityEvent{ID: "activity_issue_101479_completed", Type: "issue.updated", CreatedAt: now.AddDate(0, -4, -22), Actor: jiangyaling, Metadata: map[string]string{"state": "Done"}},
	)
	activities["issue_100474"] = append(activities["issue_100474"], domain.ActivityEvent{ID: "activity_issue_100474_closed", Type: "issue.updated", CreatedAt: now.AddDate(0, -4, -3), Actor: yumingyang, Metadata: map[string]string{"state": "Done"}})
	activities["issue_52526"] = append(activities["issue_52526"], domain.ActivityEvent{ID: "activity_issue_52526_closed", Type: "issue.updated", CreatedAt: now.AddDate(0, -3, -25), Actor: zhangqun, Metadata: map[string]string{"state": "Done"}})
	projectUpdates := map[string][]domain.ProjectUpdate{
		sealPlatform.ID: {
			{ID: "project_update_cruise_1", ProjectID: sealPlatform.ID, Body: "本周聚焦驳回状态一致性和印章刻制数据问题。产品共有 11 个测试用例，当前执行数为 0；上线评审保持待定。", Health: "atRisk", CreatedAt: now.AddDate(0, 0, -2), User: liquan},
			{ID: "project_update_cruise_2", ProjectID: sealPlatform.ID, Body: "测试方案已关联公议流程、内部交接终审和外带刻制申请用例。待补齐测试结果、代码仓证据和回滚方案。", Health: "atRisk", CreatedAt: now.AddDate(0, 0, -1), User: jianyan},
		},
		carMall.ID: {
			{ID: "project_update_car_1", ProjectID: carMall.ID, Body: "禅道交付快照：64 人、累计消耗 5121.5h、剩余需求 15、任务 4、Bug 1。车商城一期与 316 迭代均已 100% 完成但延期。", Health: "atRisk", CreatedAt: now.AddDate(0, 0, -3), User: guan},
			{ID: "project_update_car_2", ProjectID: carMall.ID, Body: "运营改进建议：围绕迭代估算偏差、缺陷日清和图片加载体验建立下一轮需求，并持续收集节奏与质量数据。", Health: "onTrack", CreatedAt: now.AddDate(0, 0, -1), User: viewer},
		},
	}
	initiativeUpdates := map[string][]domain.InitiativeUpdate{
		initiatives[0].ID: {{ID: "initiative_update_operational_excellence_1", InitiativeID: initiatives[0].ID, Body: "已按战略、业务、产品、管理、开发、测试、审计和运营视角组织交付数据。当前价值验证重点是节奏、质量与 AI 转型；提效、降本、增收和 ROI 仍缺少结构化指标模型。", Health: "onTrack", CreatedAt: now.AddDate(0, 0, -3), User: viewer, Comments: []domain.Comment{}, Reactions: map[string][]string{}}},
	}
	releaseOneTarget := now.AddDate(0, 0, 12).Format("2006-01-02")
	releaseTwoTarget := now.AddDate(0, 0, -18).Format("2006-01-02")
	releaseThreeTarget := now.AddDate(0, 1, 5).Format("2006-01-02")
	releases := []domain.Release{
		{ID: "release_seal_uat", Name: "智能印控平台 2026.08 UAT", Version: "2026.08-rc1", Description: "演示版本：聚合印章交接、驳回状态、刻制稳定性、测试方案、测试报告和上线评审。禅道当前产品无版本/发布记录，因此该版本按现有需求结构补充。", Status: "inProgress", TargetDate: &releaseOneTarget, ProjectIDs: []string{sealPlatform.ID}, IssueIDs: []string{"issue_33", "issue_20", "issue_56329", "issue_54647", "issue_test_plan", "issue_test_report", "issue_release_review", "issue_audit_gate"}, SubscriberIDs: []string{viewer.ID, liquan.ID, jianyan.ID}, Creator: liquan, CreatedAt: now.AddDate(0, 0, -8), UpdatedAt: now},
		{ID: "release_seal_stable", Name: "智能印控平台 2026.07 稳定版", Version: "2026.07", Description: "演示版本：覆盖流程查询、外部交接与销毁节点修复。", Status: "released", TargetDate: &releaseTwoTarget, ProjectIDs: []string{sealPlatform.ID}, IssueIDs: []string{"issue_55316", "issue_55092", "issue_54561"}, SubscriberIDs: []string{viewer.ID, liquan.ID}, Creator: liquan, CreatedAt: now.AddDate(0, -1, -6), UpdatedAt: now.AddDate(0, 0, -18)},
		{ID: "release_car_phase2", Name: "车商城二期订单流程", Version: "v2.6.0", Description: "演示版本：关联父需求、开发任务、测试任务和缺陷，覆盖订单、白名单状态流转和图片预览体验。", Status: "planned", TargetDate: &releaseThreeTarget, ProjectIDs: []string{carMall.ID}, IssueIDs: []string{"issue_105130", "issue_53156", "issue_53063", "issue_100417", "issue_100879", "issue_101479"}, SubscriberIDs: []string{viewer.ID, guan.ID, luoyuying.ID}, Creator: guan, CreatedAt: now.AddDate(0, 0, -5), UpdatedAt: now},
	}
	documents := []domain.Document{
		{ID: "document_strategy", SlugID: "strategy-business-goals", Title: "企业流程体验升级：战略与业务目标", Icon: "Target", Content: "# 战略与业务目标\n\n## 战略分析\n统一关键业务流程的需求、交付、测试和发布协作。\n\n## 业务价值\n- 提效：缩短需求到交付的信息流转\n- 降本：减少重复录入和人工汇总\n- 增收：通过业务诉求闭环改善转化体验\n\n## 交付价值\n- 节奏：版本与迭代周期可视\n- 质量：需求、用例、缺陷和评审关联\n- AI 转型：结构化需求与交付数据支持智能分析", Creator: viewer, ProjectIDs: []string{sealPlatform.ID, carMall.ID}, TeamIDs: []string{team.ID, deliveryTeam.ID, qualityTeam.ID}, SubscriberIDs: []string{viewer.ID, guan.ID, liquan.ID}, CreatedAt: now.AddDate(0, 0, -8), UpdatedAt: now, Revisions: []domain.DocumentRevision{}},
		{ID: "document_product_spec", SlugID: "car-mall-order-flow-spec", Title: "车商城二期订单流程 SPEC", Icon: "FileText", Content: "# 车商城二期订单流程 SPEC\n\n来源：禅道研发需求 #105130。\n\n## 用户故事\n作为车商城业务人员，我希望订单、交付、金融和白名单状态能够一致流转，以便减少人工核对与异常处理。\n\n## 需求拆解\n- Image 图片预览组件 #100417\n- 门店与固定金融产品测试 #100879\n- 销单暂存缺陷 #53156\n- 白名单状态流转缺陷 #53063", Creator: luoyuying, ProjectIDs: []string{carMall.ID}, TeamIDs: []string{deliveryTeam.ID}, IssueID: "issue_105130", SubscriberIDs: []string{guan.ID, luoyuying.ID}, CreatedAt: now.AddDate(0, -5, -20), UpdatedAt: now.AddDate(0, 0, -2), Revisions: []domain.DocumentRevision{}},
		{ID: "document_test_plan", SlugID: "seal-uat-test-plan", Title: "智能印控平台 2026.08 SIT/UAT 测试方案", Icon: "ClipboardCheck", Content: "# SIT/UAT 测试方案\n\n## 范围\n印章内部交接、公议流程、外带刻制申请、驳回状态和刻制编码。\n\n## 禅道用例快照\n产品共有 11 个用例，抓取时已执行 0 个。代表用例：#49219、#49216、#49215。\n\n## 准出条件\n- 高优缺陷关闭\n- 关键用例执行通过\n- 回滚方案完成\n- 上线评审通过", Creator: jianyan, ProjectIDs: []string{sealPlatform.ID}, TeamIDs: []string{qualityTeam.ID}, IssueID: "issue_test_plan", SubscriberIDs: []string{viewer.ID, liquan.ID, jianyan.ID}, CreatedAt: now.AddDate(0, 0, -7), UpdatedAt: now, Revisions: []domain.DocumentRevision{}},
		{ID: "document_test_report", SlugID: "seal-uat-test-report", Title: "智能印控平台 2026.08 测试报告", Icon: "FileCheck", Content: "# 测试报告\n\n状态：未完成。\n\n- 用例总数：11\n- 已执行：0\n- 代表性未执行用例：3\n- 高优缺陷：处理中\n\n结论：当前不满足上线准出条件。", Creator: jianyan, ProjectIDs: []string{sealPlatform.ID}, TeamIDs: []string{qualityTeam.ID}, IssueID: "issue_test_report", SubscriberIDs: []string{viewer.ID, liquan.ID}, CreatedAt: now.AddDate(0, 0, -4), UpdatedAt: now, Revisions: []domain.DocumentRevision{}},
		{ID: "document_release_review", SlugID: "seal-release-review", Title: "评审-2026-08-001 智能印控上线评审报告", Icon: "ShieldCheck", Content: "# 上线评审报告\n\n评审号：REV-2026-08-001\n\n## 结论\n待定。\n\n## 未满足门禁\n- 测试用例尚未执行\n- 代码仓与提交记录未关联\n- 部署及回滚证据缺失\n\nFlow 当前没有独立评审单模型，本报告与评审 Issue 共同承载。", Creator: viewer, ProjectIDs: []string{sealPlatform.ID}, TeamIDs: []string{qualityTeam.ID}, IssueID: "issue_release_review", SubscriberIDs: []string{viewer.ID, liquan.ID, jianyan.ID}, CreatedAt: now.AddDate(0, 0, -3), UpdatedAt: now, Revisions: []domain.DocumentRevision{}},
		{ID: "document_delivery_data", SlugID: "car-mall-delivery-data", Title: "车商城交付数据与改进清单", Icon: "ChartNoAxesCombined", Content: "# 交付过程数据\n\n## 项目快照\n- 人员：64\n- 累计消耗：5121.5h\n- 剩余需求：15\n- 剩余任务：4\n- 剩余 Bug：1\n\n## 迭代偏差\n- 车商城316迭代：预计 3885.5h，消耗 3973h，偏差 +87.5h\n- 车商城一期迭代：预计 519h，消耗 591h，偏差 +72h\n\n## 改进清单\n1. 拆分超大迭代并设置阶段门禁\n2. 建立估算偏差复盘\n3. 保持缺陷日清\n4. 将图片加载体验反馈反哺为产品需求", Creator: guan, ProjectIDs: []string{carMall.ID}, TeamIDs: []string{deliveryTeam.ID, qualityTeam.ID}, IssueID: "issue_delivery_metrics", SubscriberIDs: []string{viewer.ID, guan.ID}, CreatedAt: now.AddDate(0, 0, -5), UpdatedAt: now, Revisions: []domain.DocumentRevision{}},
		{ID: "document_audit", SlugID: "delivery-audit-checklist", Title: "交付合规审计清单", Icon: "Shield", Content: "# 交付合规审计\n\n审计链路：订单 → 评估 → 竞价 → 执行 → 验收 → 评价 → 付款。\n\n## 检查维度\n- 流程：状态与审批证据完整\n- 标准：需求、测试、发布产物齐备\n- 安全：权限、代码与上线检查\n- 预算与费用：当前 Flow 无结构化字段，仅在本清单记录", Creator: viewer, ProjectIDs: []string{sealPlatform.ID, carMall.ID}, TeamIDs: []string{qualityTeam.ID}, IssueID: "issue_audit_gate", SubscriberIDs: []string{viewer.ID, guan.ID, liquan.ID}, CreatedAt: now.AddDate(0, 0, -2), UpdatedAt: now, Revisions: []domain.DocumentRevision{}},
		{ID: "document_model_mapping", SlugID: "ai-delivery-flow-mapping", Title: "AI 原生交付特性与 Flow 模型映射", Icon: "Network", Content: "# 模型映射\n\n- 战略与业务目标：Initiative、Project Update、Document\n- 业务诉求：Ask、Customer Request\n- 产品需求：Issue、Parent/Sub-issue、Document\n- 项目与版本：Project、Cycle、Release\n- 开发与测试：Issue、Label、Comment、Attachment\n- 产物：Document、Project Resource\n- 审计与系统日志：Audit Log、Activity\n- 运营改进：Project Update、Issue、Document\n\n## 当前缺口\n产品、测试计划/用例/执行、代码仓/提交、CI/CD、部署/回滚、工时日志、预算成本、ROI、Token、人天与价值指标均无独立结构化模型。", Creator: viewer, ProjectIDs: []string{sealPlatform.ID, carMall.ID}, TeamIDs: []string{team.ID, deliveryTeam.ID, qualityTeam.ID}, SubscriberIDs: []string{viewer.ID}, CreatedAt: now.AddDate(0, 0, -1), UpdatedAt: now, Revisions: []domain.DocumentRevision{}},
	}
	projects[0].Resources = []domain.ProjectResource{
		{ID: "resource_seal_source", ProjectID: sealPlatform.ID, Type: "link", Title: "禅道：智能印控平台(S04763)", URL: "https://chandao.haier.net/product-index-3124.html", CreatedAt: now.AddDate(0, 0, -9)},
		{ID: "document_test_plan", ProjectID: sealPlatform.ID, Type: "document", Title: "智能印控平台 2026.08 SIT/UAT 测试方案", URL: "/cleantrack/document/seal-uat-test-plan", CreatedAt: now.AddDate(0, 0, -7)},
		{ID: "document_test_report", ProjectID: sealPlatform.ID, Type: "document", Title: "智能印控平台 2026.08 测试报告", URL: "/cleantrack/document/seal-uat-test-report", CreatedAt: now.AddDate(0, 0, -4)},
		{ID: "document_release_review", ProjectID: sealPlatform.ID, Type: "document", Title: "智能印控上线评审报告", URL: "/cleantrack/document/seal-release-review", CreatedAt: now.AddDate(0, 0, -3)},
	}
	projects[0].Milestones = []domain.ProjectMilestone{
		{ID: "milestone_seal_test", ProjectID: sealPlatform.ID, Name: "关键用例执行完成", TargetDate: &releaseOneTarget, CreatedAt: now.AddDate(0, 0, -7), UpdatedAt: now},
		{ID: "milestone_seal_review", ProjectID: sealPlatform.ID, Name: "上线评审通过", TargetDate: &releaseOneTarget, CreatedAt: now.AddDate(0, 0, -6), UpdatedAt: now},
	}
	projects[1].Resources = []domain.ProjectResource{
		{ID: "resource_car_source", ProjectID: carMall.ID, Type: "link", Title: "禅道：汽车之家车商城项目2026", URL: "https://chandao.haier.net/project-index-10155.html", CreatedAt: now.AddDate(0, 0, -9)},
		{ID: "document_product_spec", ProjectID: carMall.ID, Type: "document", Title: "车商城二期订单流程 SPEC", URL: "/cleantrack/document/car-mall-order-flow-spec", CreatedAt: now.AddDate(0, -5, -20)},
		{ID: "document_delivery_data", ProjectID: carMall.ID, Type: "document", Title: "车商城交付数据与改进清单", URL: "/cleantrack/document/car-mall-delivery-data", CreatedAt: now.AddDate(0, 0, -5)},
	}
	projects[1].Milestones = []domain.ProjectMilestone{
		{ID: "milestone_car_phase1", ProjectID: carMall.ID, Name: "车商城一期迭代完成", TargetDate: stringPointer("2026-07-10"), CreatedAt: time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC), UpdatedAt: now},
		{ID: "milestone_car_316", ProjectID: carMall.ID, Name: "车商城316迭代完成", TargetDate: stringPointer("2026-08-01"), CreatedAt: time.Date(2026, 3, 16, 0, 0, 0, 0, time.UTC), UpdatedAt: now},
	}
	customers := []domain.Customer{
		{ID: "customer_seal_ops", Name: "集团法务与印控运营", OwnerID: liquan.ID, Status: "active", Tier: "Enterprise", Size: 120, Domains: []string{"seal-operations.demo"}, CreatedAt: now.AddDate(-1, 0, 0), UpdatedAt: now},
		{ID: "customer_auto_retail", Name: "汽车新零售业务", OwnerID: guan.ID, Status: "active", Tier: "Strategic", Size: 80, Domains: []string{"auto-retail.demo"}, CreatedAt: now.AddDate(-1, 0, 0), UpdatedAt: now},
	}
	approvedAt := now.AddDate(0, -5, -10)
	rejectedAt := now.AddDate(0, 0, -6)
	asks := []domain.Ask{
		{ID: "ask_business_approved", Title: "车商城 C 端增加无报告车源外展", Body: "业务方希望扩大无报告车源的可见范围并跟踪转化效果。", Source: "业务门户", Requester: luoyuying, TeamID: deliveryTeam.ID, Status: "approved", IssueID: "issue_108415", Approvals: []domain.AskApproval{{ID: "ask_approval_business", AskID: "ask_business_approved", Approver: guan, Decision: "approved", Note: "业务价值明确，进入产品澄清与交付。", DecidedAt: &approvedAt}}, CreatedAt: now.AddDate(0, -5, -12), UpdatedAt: approvedAt},
		{ID: "ask_release_rejected", Title: "智能印控平台跳过 UAT 直接上线", Body: "希望提前发布驳回状态修复。", Source: "项目群", Requester: liquan, TeamID: qualityTeam.ID, Status: "rejected", Approvals: []domain.AskApproval{{ID: "ask_approval_release", AskID: "ask_release_rejected", Approver: viewer, Decision: "rejected", Note: "11 个用例尚未执行，缺少测试报告和回滚证据。", DecidedAt: &rejectedAt}}, CreatedAt: now.AddDate(0, 0, -7), UpdatedAt: rejectedAt},
		{ID: "ask_ops_improvement", Title: "运营反哺：优化迭代估算偏差", Body: "两个已完成迭代实际消耗分别超出预计 87.5h 和 72h，建议创建估算改进需求。", Source: "交付数据分析", Requester: viewer, TeamID: deliveryTeam.ID, Status: "pending", Approvals: []domain.AskApproval{}, CreatedAt: now.AddDate(0, 0, -1), UpdatedAt: now.AddDate(0, 0, -1)},
	}
	customerRequests := []domain.CustomerRequest{
		{ID: "customer_request_slow_images", CustomerID: "customer_auto_retail", Body: "车辆详情页组件和图片加载慢，影响车况查看体验。", Source: "运营反馈", SourceURL: "https://chandao.haier.net/bug-view-52526.html", Creator: jiangyaling, IssueID: "issue_52526", ProjectID: carMall.ID, Attachments: []domain.Attachment{}, CreatedAt: now.AddDate(0, -4, -2), UpdatedAt: now.AddDate(0, -3, -25)},
		{ID: "customer_request_seal_rejection", CustomerID: "customer_seal_ops", Body: "流程驳回后，待办必须准确展示当前节点和驳回状态。", Source: "业务诉求", SourceURL: "https://chandao.haier.net/story-view-112329.html", Creator: liquan, IssueID: "issue_33", ProjectID: sealPlatform.ID, Attachments: []domain.Attachment{}, CreatedAt: now.AddDate(0, 0, -12), UpdatedAt: now},
	}
	auditLog := []domain.AuditLogEntry{
		{ID: "audit_review_pending", Actor: viewer, Action: "release_review_pending", ResourceType: "issue", ResourceID: "issue_release_review", Metadata: map[string]any{"reviewId": "REV-2026-08-001", "reason": "test evidence incomplete"}, CreatedAt: now.AddDate(0, 0, -1)},
		{ID: "audit_ask_rejected", Actor: viewer, Action: "ask_rejected", ResourceType: "ask", ResourceID: "ask_release_rejected", Metadata: map[string]any{"reason": "11 test cases not executed"}, CreatedAt: rejectedAt},
		{ID: "audit_test_report_created", Actor: jianyan, Action: "document_created", ResourceType: "document", ResourceID: "document_test_report", Metadata: map[string]any{"status": "incomplete"}, CreatedAt: now.AddDate(0, 0, -4)},
		{ID: "audit_requirement_accepted", Actor: guan, Action: "ask_approved", ResourceType: "ask", ResourceID: "ask_business_approved", Metadata: map[string]any{"issueId": "issue_108415"}, CreatedAt: approvedAt},
		{ID: "audit_delivery_snapshot", Actor: guan, Action: "delivery_data_collected", ResourceType: "project", ResourceID: carMall.ID, Metadata: map[string]any{"people": 64, "spentHours": 5121.5}, CreatedAt: now.AddDate(0, 0, -5)},
	}
	viewFilter := func(labelID, labelName, color string) json.RawMessage {
		encoded, _ := json.Marshal([]map[string]any{{
			"id": "labels-" + labelID, "field": "labels", "fieldLabel": "Labels", "operator": "is",
			"value": labelID, "valueLabel": labelName, "color": color,
			"values": []map[string]string{{"value": labelID, "valueLabel": labelName, "color": color}},
		}})
		return encoded
	}
	demandView := viewFilter("label_type_requirement", "原始需求", "#5E6AD2")
	taskView := viewFilter("label_type_development", "开发任务", "#4AA3F7")
	issueDisplay := json.RawMessage(`{"layout":"list","ordering":"updatedAt","direction":"desc","grouping":"status","properties":["id","status","priority","assignee","labels","project"]}`)
	savedViews := []domain.SavedView{
		{ID: "view_strategy", Name: "原始需求", Description: "从待承接到已交付的原始需求", Icon: "Target", Color: "#5E6AD2", Resource: "issues", Scope: "workspace", OwnerID: viewer.ID, Favorite: true, View: "all", Filters: demandView, Display: issueDisplay, CreatedAt: now.AddDate(0, 0, -8), UpdatedAt: now},
		{ID: "view_business", Name: "待实施需求", Description: "通过状态筛选查看已承接并等待实施的需求", Icon: "MessageCircleQuestion", Color: "#5E6AD2", Resource: "issues", Scope: "workspace", OwnerID: viewer.ID, Favorite: true, View: "all", Filters: demandView, Display: issueDisplay, CreatedAt: now.AddDate(0, 0, -8), UpdatedAt: now},
		{ID: "view_product", Name: "待验收需求", Description: "通过状态筛选查看实施完成并等待验收的需求", Icon: "FileText", Color: "#D6B326", Resource: "issues", Scope: "workspace", OwnerID: viewer.ID, View: "all", Filters: demandView, Display: issueDisplay, CreatedAt: now.AddDate(0, 0, -7), UpdatedAt: now},
		{ID: "view_development", Name: "开发任务", Description: "开发与测试任务的完整执行流程", Icon: "Code2", Color: "#4AA3F7", Resource: "issues", Scope: "workspace", OwnerID: viewer.ID, Favorite: true, View: "all", Filters: taskView, Display: issueDisplay, CreatedAt: now.AddDate(0, 0, -7), UpdatedAt: now},
		{ID: "view_testing", Name: "测试任务", Description: "开发任务中由测试角色执行的工作", Icon: "ClipboardCheck", Color: "#2D9D78", Resource: "issues", Scope: "workspace", OwnerID: viewer.ID, Favorite: true, View: "all", Filters: taskView, Display: issueDisplay, CreatedAt: now.AddDate(0, 0, -7), UpdatedAt: now},
		{ID: "view_operations", Name: "执行中任务", Description: "通过状态筛选查看正在执行的开发任务", Icon: "ChartNoAxesCombined", Color: "#4AA3F7", Resource: "issues", Scope: "workspace", OwnerID: viewer.ID, Favorite: true, View: "all", Filters: taskView, Display: issueDisplay, CreatedAt: now.AddDate(0, 0, -5), UpdatedAt: now},
		{ID: "view_management", Name: "管理驾驶舱", Description: "项目进度、质量、资源与风险概览", Icon: "LayoutDashboard", Color: "#5E6AD2", Resource: "projects", Scope: "workspace", OwnerID: viewer.ID, Favorite: true, View: "active", Filters: json.RawMessage(`[]`), Display: json.RawMessage(`{"layout":"list","ordering":"updatedAt","direction":"desc","grouping":"status","properties":["status","health","progress","lead","targetDate"]}`), CreatedAt: now.AddDate(0, 0, -8), UpdatedAt: now},
	}
	members := make([]domain.WorkspaceMember, 0, len(users))
	for index, user := range users {
		role := "Member"
		if index == 0 {
			role = "Admin"
		}
		members = append(members, domain.WorkspaceMember{User: user, Role: role, Status: "active", JoinedAt: now.AddDate(0, -8, index)})
	}
	teamMembers := []domain.TeamMember{
		{TeamID: team.ID, UserID: viewer.ID, Role: "lead", JoinedAt: now.AddDate(0, -8, 0)}, {TeamID: team.ID, UserID: liquan.ID, Role: "member", JoinedAt: now.AddDate(0, -8, 1)}, {TeamID: team.ID, UserID: jianyan.ID, Role: "member", JoinedAt: now.AddDate(0, -7, 1)}, {TeamID: team.ID, UserID: liyanlu.ID, Role: "member", JoinedAt: now.AddDate(0, -7, 2)}, {TeamID: team.ID, UserID: lijiangtao.ID, Role: "member", JoinedAt: now.AddDate(0, -7, 3)},
		{TeamID: deliveryTeam.ID, UserID: viewer.ID, Role: "lead", JoinedAt: now.AddDate(0, -7, 0)}, {TeamID: deliveryTeam.ID, UserID: guan.ID, Role: "member", JoinedAt: now.AddDate(0, -7, 1)}, {TeamID: deliveryTeam.ID, UserID: luoyuying.ID, Role: "member", JoinedAt: now.AddDate(0, -6, 1)}, {TeamID: deliveryTeam.ID, UserID: yuxiangyang.ID, Role: "member", JoinedAt: now.AddDate(0, -6, 2)}, {TeamID: deliveryTeam.ID, UserID: yumingyang.ID, Role: "member", JoinedAt: now.AddDate(0, -6, 3)}, {TeamID: deliveryTeam.ID, UserID: zhangqun.ID, Role: "member", JoinedAt: now.AddDate(0, -5, 1)}, {TeamID: deliveryTeam.ID, UserID: jiangyaling.ID, Role: "member", JoinedAt: now.AddDate(0, -5, 2)},
		{TeamID: qualityTeam.ID, UserID: viewer.ID, Role: "lead", JoinedAt: now.AddDate(0, -6, 0)}, {TeamID: qualityTeam.ID, UserID: jianyan.ID, Role: "member", JoinedAt: now.AddDate(0, -6, 1)}, {TeamID: qualityTeam.ID, UserID: guan.ID, Role: "member", JoinedAt: now.AddDate(0, -6, 2)}, {TeamID: qualityTeam.ID, UserID: zhangqun.ID, Role: "member", JoinedAt: now.AddDate(0, -5, 1)}, {TeamID: qualityTeam.ID, UserID: jiangyaling.ID, Role: "member", JoinedAt: now.AddDate(0, -5, 2)}, {TeamID: qualityTeam.ID, UserID: dingyu.ID, Role: "member", JoinedAt: now.AddDate(0, -5, 3)},
	}
	data := domain.Bootstrap{Workspace: domain.Workspace{ID: "workspace_cleantrack", Name: "海尔数字化交付", URLKey: "cleantrack", Color: "#5E6AD2", Region: "cn", CreatedAt: now.AddDate(-1, 0, 0)}, Viewer: viewer, Users: users, Teams: teams, Customers: customers, CustomerRequests: customerRequests, States: states, Labels: canonicalLabels(), LabelGroups: canonicalLabelGroups(), Issues: issues, Cycles: cycles, CycleSettings: map[string]domain.CycleSettings{team.ID: {Enabled: true, DurationWeeks: 2, CooldownWeeks: 0, StartsOn: 1, UpcomingCount: 2, Capacity: 12}, deliveryTeam.ID: {Enabled: true, DurationWeeks: 2, StartsOn: 1, UpcomingCount: 2, Capacity: 64}, qualityTeam.ID: {Enabled: true, DurationWeeks: 2, StartsOn: 1, UpcomingCount: 2, Capacity: 8}}, Projects: projects, ProjectStatuses: projectStatuses, ProjectUpdates: projectUpdates, Initiatives: initiatives, InitiativeUpdates: initiativeUpdates, Comments: comments, Activities: activities, Documents: documents, Releases: releases, Asks: asks, AuditLog: auditLog, Members: members, TeamMembers: teamMembers, SavedViews: savedViews, Notifications: []domain.Notification{}}
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
	labels := []domain.IssueLabel{
		{ID: "label_type_requirement", Name: "原始需求", Color: "#5E6AD2", Description: "尚未拆分为执行任务的业务或产品需求", Scope: "Workspace", GroupID: "label_group_work_item_type"},
		{ID: "label_type_development", Name: "开发任务", Color: "#4AA3F7", Description: "开发或测试角色执行的交付任务", Scope: "Workspace", GroupID: "label_group_work_item_type"},
		{ID: "label_type_defect", Name: "缺陷", Color: "#F15B61", Description: "需要定位、修复与回归检查的缺陷", Scope: "Workspace", GroupID: "label_group_work_item_type"},
		{ID: "label_product", Name: "产品", Color: "#18B99A", Description: "产品规划与体验改进", IssueCount: 5, Scope: "Workspace", ResourceType: "project", GroupID: "label_group_project_value"},
		{ID: "label_delivery", Name: "重点交付", Color: "#D97757", Description: "需要跨团队关注的重点交付", IssueCount: 3, Scope: "Workspace", ResourceType: "project", GroupID: "label_group_project_delivery"},
	}
	now := time.Now().UTC()
	for index := range labels {
		if labels[index].ResourceType == "" {
			labels[index].ResourceType = "issue"
		}
		labels[index].CreatedAt = now.AddDate(0, 0, -(len(labels) - index + 7))
		if labels[index].IssueCount > 0 {
			applied := now.Add(-time.Duration(index+1) * time.Hour)
			labels[index].LastAppliedAt = &applied
		}
	}
	return labels
}

func canonicalLabelGroups() []domain.LabelGroup {
	now := time.Now().UTC()
	return []domain.LabelGroup{
		{ID: "label_group_work_item_type", Name: "工作项类型", Color: "#5E6AD2", Description: "原始需求、开发任务与缺陷", Scope: "Workspace", ResourceType: "issue", CreatedAt: now},
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
	pos := len(digits)
	for value > 0 {
		pos--
		digits[pos] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[pos:])
}
