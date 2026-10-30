// Command perf-seed fills a database with production-shaped data for the
// performance work: three workspaces, sixty members, seventy-five thousand
// issues with labels, projects, cycles, sub-issues, relations, comments,
// activity history and inbox notifications.
//
// It writes through the same store migrations the server runs:
//
//	FLOW_DATABASE_DRIVER=mysql \
//	FLOW_DATABASE_URL=mysql://root:flowperf@127.0.0.1:13306/flow \
//	go run ./cmd/perf-seed -issues 75000
//
// The seed is idempotent: workspaces are created once and every record is
// upserted by id, so a rerun refreshes the same data instead of growing it.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"

	_ "github.com/go-sql-driver/mysql"
)

const (
	mainWorkspace   = "perf-main"
	secondWorkspace = "perf-two"
	thirdWorkspace  = "perf-three"

	teamCount     = 8
	memberCount   = 60
	labelCount    = 30
	projectCount  = 40
	documentCount = 200
	viewCount     = 30

	longTailIssues   = 12
	longTailComments = 180
)

var canonicalStates = []domain.WorkflowState{
	{ID: "state_backlog", Name: "Backlog", Color: "#6B6F76", Type: "backlog", Position: 0, Default: true},
	{ID: "state_todo", Name: "Todo", Color: "#E2E2E2", Type: "unstarted", Position: 1},
	{ID: "state_progress", Name: "In Progress", Color: "#F2C94C", Type: "started", Position: 2},
	{ID: "state_done", Name: "Done", Color: "lch(48% 59.31 288.43)", Type: "completed", Position: 3},
	{ID: "state_canceled", Name: "Canceled", Color: "#95A2B3", Type: "canceled", Position: 4},
	{ID: "state_duplicate", Name: "Duplicate", Color: "#95A2B3", Type: "canceled", Position: 5, Reserved: true},
}

var projectStatuses = []domain.ProjectStatus{
	{ID: "ps_backlog", Name: "Backlog", Color: "#F2994A", Type: "backlog"},
	{ID: "ps_planned", Name: "Planned", Color: "#D7D8DB", Type: "planned"},
	{ID: "ps_progress", Name: "In Progress", Color: "#F2C94C", Type: "started"},
	{ID: "ps_completed", Name: "Completed", Color: "#5E6AD2", Type: "completed"},
	{ID: "ps_canceled", Name: "Canceled", Color: "#8A8F98", Type: "canceled"},
}

func main() {
	issues := flag.Int("issues", 75000, "issues in the main workspace")
	otherIssues := flag.Int("other-issues", 2500, "issues in each secondary workspace")
	content := flag.Bool("content", true, "seed comments, activity and notifications")
	flag.Parse()

	driver := strings.ToLower(strings.TrimSpace(os.Getenv("FLOW_DATABASE_DRIVER")))
	if driver == "" {
		driver = "sqlite"
	}
	rawURL := strings.TrimSpace(os.Getenv("FLOW_DATABASE_URL"))
	dsn := rawURL
	if driver == "mysql" {
		var err error
		if dsn, err = mysqlDSN(rawURL); err != nil {
			log.Fatalf("database URL: %v", err)
		}
	}
	started := time.Now()
	s, err := store.OpenDatabase(store.DatabaseConfig{
		Driver:              driver,
		URL:                 rawURL,
		Path:                os.Getenv("FLOW_DATABASE_PATH"),
		MaxOpenConns:        12,
		MaxIdleConns:        12,
		MaxTransactionBytes: 32 << 20,
	})
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer s.Close()
	log.Printf("store opened in %s", time.Since(started).Round(time.Millisecond))

	ctx := context.Background()
	worlds := []seedWorld{
		{key: mainWorkspace, name: "Performance Main", issues: *issues, teams: teamCount, projects: projectCount, documents: documentCount, views: viewCount},
		{key: secondWorkspace, name: "Performance Two", issues: *otherIssues, teams: 2, projects: 4, documents: 10, views: 3},
		{key: thirdWorkspace, name: "Performance Three", issues: *otherIssues, teams: 2, projects: 4, documents: 10, views: 3},
	}
	seeded := make([]*seedWorld, 0, len(worlds))
	for index := range worlds {
		world := &worlds[index]
		if err := seedMetadata(ctx, s, world); err != nil {
			log.Fatalf("seed workspace %s: %v", world.key, err)
		}
		began := time.Now()
		if err := seedIssues(ctx, s, world); err != nil {
			log.Fatalf("seed issues %s: %v", world.key, err)
		}
		log.Printf("%s: %d issues in %s", world.key, world.issues, time.Since(began).Round(time.Second))
		seeded = append(seeded, world)
	}
	if *content {
		db, err := sql.Open(sqlDriver(driver), dsn)
		if err != nil {
			log.Fatalf("open content connection: %v", err)
		}
		defer db.Close()
		db.SetMaxOpenConns(4)
		for _, world := range seeded {
			if err := seedContent(ctx, db, driver, world); err != nil {
				log.Fatalf("seed content %s: %v", world.key, err)
			}
		}
		if driver == "mysql" {
			if _, err := db.ExecContext(ctx, "ANALYZE TABLE issue_records, issue_label_records, issue_attribute_records, issue_subscriber_records, issue_actor_records, workspace_content_records"); err != nil {
				log.Printf("analyze: %v", err)
			}
		}
	}
	log.Printf("seeded %d workspaces in %s", len(seeded), time.Since(started).Round(time.Second))
}

type seedWorld struct {
	key              string
	name             string
	issues           int
	teams            int
	projects         int
	documents        int
	views            int
	viewer           domain.User
	users            []domain.User
	teamsByID        []domain.Team
	labels           []domain.IssueLabel
	projectsByID     []domain.Project
	cyclesByTeam     map[string][]domain.Cycle
	childrenByParent map[int][]int
	longTail         map[int]bool
}

func seedMetadata(ctx context.Context, s *store.SQLiteStore, world *seedWorld) error {
	if _, ok := s.WorkspaceMetadata(world.key); ok {
		fillWorld(world)
		return nil
	}
	viewer := makeUser(0)
	now := time.Now().UTC()
	_, err := s.CreateWorkspaceWith(ctx, world.name, world.key, "us", func(data *domain.Bootstrap) error {
		data.Viewer = viewer
		data.Users = makeUsers(memberCount)
		if world.key == mainWorkspace {
			// The main workspace has the full cast; the others share it so
			// references stay resolvable across workspaces.
		}
		data.Members = makeMembers(data.Users, viewer.ID)
		data.Teams = makeTeams(world.teams)
		data.States = canonicalStates
		data.ProjectStatuses = projectStatuses
		data.Labels = makeLabels(world.key)
		data.TeamSettings = map[string]domain.TeamSettings{}
		data.CycleSettings = map[string]domain.CycleSettings{}
		for _, team := range data.Teams {
			data.TeamSettings[team.ID] = domain.TeamSettings{TeamID: team.ID, EstimateType: "fibonacci", Access: "public", DefaultStateID: "state_todo"}
			data.CycleSettings[team.ID] = domain.CycleSettings{Enabled: true, DurationWeeks: 2, UpcomingCount: 1, Capacity: 30, AutoCreate: true, AutoAddActive: true, AutoMigrate: true}
		}
		// Team memberships: 60 members spread over the teams.
		for index, user := range data.Users {
			team := data.Teams[index%len(data.Teams)]
			data.TeamMembers = append(data.TeamMembers, domain.TeamMember{TeamID: team.ID, UserID: user.ID, Role: "member", JoinedAt: now})
		}
		data.Projects = makeProjects(world.projects, data.Teams, data.Users, now)
		data.Cycles = makeCycles(data.Teams, now)
		data.Documents = makeDocuments(world.documents, data.Teams, data.Projects, viewer, now)
		data.SavedViews = makeViews(world.views, data.Teams, now)
		data.WorkspaceSettings = defaultWorkspaceSettings()
		return nil
	})
	if err != nil {
		return err
	}
	fillWorld(world)
	return nil
}

func fillWorld(world *seedWorld) {
	world.viewer = makeUser(0)
	world.users = makeUsers(memberCount)
	world.teamsByID = makeTeams(world.teams)
	world.labels = makeLabels(world.key)
	world.projectsByID = makeProjects(world.projects, world.teamsByID, world.users, time.Now().UTC())
	world.cyclesByTeam = makeCycleMap(world.teamsByID)
	world.longTail = map[int]bool{}
	for i := 0; i < longTailIssues && i < world.issues; i++ {
		world.longTail[(world.issues/2)+i*97] = true
	}
	world.childrenByParent = makeChildren(world.issues)
}

// makeChildren picks the sub-issue forest: parent indices 500..699 receive the
// children at indices divisible by ten, so roughly 10% of the issues are
// sub-issues and each parent has a realistic long tail of children.
func makeChildren(issues int) map[int][]int {
	children := map[int][]int{}
	for i := 500; i < issues; i += 10 {
		parent := 500 + (i % 200)
		if parent >= i {
			continue
		}
		children[parent] = append(children[parent], i)
	}
	return children
}

func seedIssues(ctx context.Context, s *store.SQLiteStore, world *seedWorld) error {
	const batchSize = 1000
	batch := make([]domain.Issue, 0, batchSize)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := s.ImportIssues(ctx, world.key, batch); err != nil {
			return err
		}
		batch = batch[:0]
		return nil
	}
	base := time.Now().UTC().Add(-24 * time.Hour)
	for i := 0; i < world.issues; i++ {
		issue := world.issueAt(i, base)
		batch = append(batch, issue)
		if len(batch) == batchSize {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	return flush()
}

func (world *seedWorld) issueAt(i int, base time.Time) domain.Issue {
	team := world.teamsByID[i%len(world.teamsByID)]
	state := canonicalStates[stateIndex(i)]
	number := i + 1
	created := base.Add(time.Duration(i) * time.Minute)
	updated := created.Add(time.Duration(i%240) * time.Minute)
	creator := world.users[(i*7)%len(world.users)]
	var assignee *domain.User
	if i%9 != 0 {
		user := world.users[(i*3)%len(world.users)]
		assignee = &user
	}
	priority := []int{0, 1, 2, 3, 4}[i%5]
	labels := make([]domain.IssueLabel, 0, 4)
	for n := 0; n < 1+i%4; n++ {
		label := world.labels[(i+(n*5))%len(world.labels)]
		labels = append(labels, label)
	}
	subscribers := []string{creator.ID}
	if assignee != nil {
		subscribers = append(subscribers, assignee.ID)
	}
	subscribers = append(subscribers, world.viewer.ID)
	reactions := map[string][]string{}
	if i%11 == 0 {
		reactions["thumbs_up"] = []string{world.users[(i+1)%len(world.users)].ID}
		reactions["heart"] = []string{world.users[(i+2)%len(world.users)].ID}
	}
	subIssues := []string{}
	for _, child := range world.childrenByParent[i] {
		subIssues = append(subIssues, issueID(world.key, child))
	}
	relations := []domain.IssueRelation{}
	if i%20 == 0 && i > 3 {
		relations = append(relations, domain.IssueRelation{ID: fmt.Sprintf("rel_%s_%d", world.key, i), Type: "related", IssueID: issueID(world.key, i), RelatedIssueID: issueID(world.key, i-3)})
	}
	var parentID *string
	if i >= 500 && i%10 == 0 {
		parent := 500 + (i % 200)
		if parent < i {
			id := issueID(world.key, parent)
			parentID = &id
		}
	}
	var dueDate *string
	if i%5 == 0 {
		date := base.Add(time.Duration(i%180) * 24 * time.Hour).Format("2006-01-02")
		dueDate = &date
	}
	var estimate *float64
	if i%4 != 0 {
		value := float64([]int{1, 2, 3, 5, 8}[i%5])
		estimate = &value
	}
	var project *domain.ProjectSummary
	var milestoneID *string
	if i%2 == 0 && len(world.projectsByID) > 0 {
		p := world.projectsByID[i%len(world.projectsByID)]
		project = &domain.ProjectSummary{ID: p.ID, Name: p.Name, Color: p.Color, Icon: p.Icon}
		if i%6 == 0 && len(p.Milestones) > 0 {
			id := p.Milestones[i%len(p.Milestones)].ID
			milestoneID = &id
		}
	}
	var cycleID *string
	if i%3 == 0 {
		if cycles := world.cyclesByTeam[team.ID]; len(cycles) > 0 {
			id := cycles[i%len(cycles)].ID
			cycleID = &id
		}
	}
	var archived *time.Time
	if i%25 == 0 {
		t := updated.Add(time.Hour)
		archived = &t
	}
	title := issueTitle(i)
	description := fmt.Sprintf("Seeded production-shaped issue %d for team %s. %s", i, team.Key, issueDescription(i))
	issue := domain.Issue{
		ID: issueID(world.key, i), Version: 1,
		Identifier: fmt.Sprintf("%s-%d", team.Key, number), Number: number,
		Title: title, Description: description,
		Priority: priority, PriorityLabel: priorityLabel(priority),
		SortOrder: float64(i), Estimate: estimate, DueDate: dueDate,
		CreatedAt: created, UpdatedAt: updated, ArchivedAt: archived,
		Team: team, State: state, Assignee: assignee, Creator: creator,
		Labels: labels, Project: project, ProjectMilestoneID: milestoneID, CycleID: cycleID,
		ParentID: parentID, SubIssueIDs: subIssues, Relations: relations,
		SubscriberIDs: subscribers, Reactions: reactions,
		Attachments: []domain.Attachment{},
	}
	if i%40 == 0 {
		issue.Permissions = []domain.IssuePermission{{ID: fmt.Sprintf("perm_%s_%d", world.key, i), IssueID: issue.ID, SubjectType: "team", SubjectID: world.teamsByID[(i+1)%len(world.teamsByID)].ID, Role: "viewer"}}
	}
	return issue
}

func stateIndex(i int) int {
	switch i % 10 {
	case 0, 1:
		return 0 // backlog
	case 2, 3:
		return 1 // todo
	case 4, 5, 6:
		return 2 // in progress
	case 7, 8:
		return 3 // done
	default:
		if i%2 == 0 {
			return 4 // canceled
		}
		return 5 // duplicate
	}
}

func issueTitle(i int) string {
	titles := []string{
		"Payment retry drops webhook delivery",
		"Search results ignore archived filter",
		"Import stalls on duplicate identifiers",
		"Notification grouping merges unrelated issues",
		"Board columns scroll past the viewport",
		"Cycle capacity ignores estimates",
		"Project milestone target date drifts",
		"Attachment upload retries forever",
		"Inbox unread count is stale after sync",
		"Comment thread pagination repeats a row",
	}
	return fmt.Sprintf("%s (#%d)", titles[i%len(titles)], i)
}

func issueDescription(i int) string {
	descriptions := []string{
		"The payment worker retries every request but the webhook is dropped when the queue is full.",
		"Search returns archived issues because the filter is applied after the candidate page.",
		"The import stops at the first duplicate identifier instead of continuing with the rest.",
		"Grouping notifications by group key merges activity from unrelated threads.",
		"Horizontal scrolling on the board keeps the header fixed above the columns.",
		"Cycle capacity counts unestimated issues as one point even when the team disabled that.",
		"Milestone target dates move whenever the project is saved with a new timezone.",
		"Attachment upload failures are retried with no backoff and never expire.",
		"The inbox badge keeps the previous count until a full reload finishes.",
		"A comment page boundary repeats the last row of the previous page.",
	}
	return descriptions[i%len(descriptions)]
}

func priorityLabel(priority int) string {
	labels := []string{"No priority", "Urgent", "High", "Medium", "Low"}
	if priority < 0 || priority >= len(labels) {
		return labels[0]
	}
	return labels[priority]
}

func makeUser(n int) domain.User {
	if n == 0 {
		return domain.User{ID: "usr_local", Name: "Flow user", DisplayName: "Flow user", Email: "flow-user@example.test", Active: true, EmailVerified: true}
	}
	return domain.User{
		ID: fmt.Sprintf("usr_%03d", n), Name: fmt.Sprintf("Member %03d", n),
		DisplayName: fmt.Sprintf("Member %03d", n), Email: fmt.Sprintf("member-%03d@example.test", n),
		Active: true, EmailVerified: true,
	}
}

func makeUsers(count int) []domain.User {
	users := make([]domain.User, 0, count)
	for i := 0; i < count; i++ {
		users = append(users, makeUser(i))
	}
	return users
}

func makeMembers(users []domain.User, viewerID string) []domain.WorkspaceMember {
	members := make([]domain.WorkspaceMember, 0, len(users))
	for _, user := range users {
		role := "member"
		if user.ID == viewerID {
			role = "owner"
		}
		members = append(members, domain.WorkspaceMember{User: user, Role: role, Status: "active", JoinedAt: time.Now().UTC().AddDate(0, -6, 0)})
	}
	return members
}

func makeTeams(count int) []domain.Team {
	names := []string{"Platform", "Web", "Mobile", "Data", "Infrastructure", "Growth", "Security", "Design"}
	teams := make([]domain.Team, 0, count)
	now := time.Now().UTC()
	for i := 0; i < count; i++ {
		name := names[i%len(names)]
		teams = append(teams, domain.Team{
			ID: fmt.Sprintf("team_perf_%d", i), Name: name, Key: fmt.Sprintf("T%d", i+1),
			Color: "#5E6AD2", Private: i == count-1, CreatedAt: &now, UpdatedAt: &now,
		})
	}
	return teams
}

func makeLabels(workspace string) []domain.IssueLabel {
	names := []string{"Bug", "Feature", "Improvement", "Customer", "Internal", "Regression", "Docs", "Performance", "Accessibility", "Design", "Backend", "Frontend", "Database", "Billing", "Search", "Mobile", "API", "Security", "Urgent", "Triage", "Blocked", "Waiting", "Ready", "Research", "Spike", "Tech debt", "Reliability", "Observability", "Localization", "Onboarding"}
	now := time.Now().UTC()
	labels := make([]domain.IssueLabel, 0, len(names))
	for i, name := range names {
		labels = append(labels, domain.IssueLabel{ID: fmt.Sprintf("label_perf_%s_%d", workspace, i), Name: name, Color: "#4EA7FC", Scope: "Workspace", ResourceType: "issue", CreatedAt: now})
	}
	return labels
}

func makeProjects(count int, teams []domain.Team, users []domain.User, now time.Time) []domain.Project {
	projects := make([]domain.Project, 0, count)
	for i := 0; i < count; i++ {
		team := teams[i%len(teams)]
		lead := users[(i+1)%len(users)]
		status := projectStatuses[i%len(projectStatuses)]
		milestones := make([]domain.ProjectMilestone, 0, 3)
		for m := 0; m < 2+i%3; m++ {
			target := now.AddDate(0, 0, 14*(m+1)).Format("2006-01-02")
			milestones = append(milestones, domain.ProjectMilestone{
				ID: fmt.Sprintf("milestone_perf_%d_%d", i, m), ProjectID: fmt.Sprintf("project_perf_%d", i),
				Name: fmt.Sprintf("Milestone %d.%d", i, m), TargetDate: &target, CreatedAt: now, UpdatedAt: now,
			})
		}
		projects = append(projects, domain.Project{
			ID: fmt.Sprintf("project_perf_%d", i), Name: fmt.Sprintf("Project %02d", i),
			SlugID: fmt.Sprintf("project-%02d-000000000000", i), Summary: "Seeded project",
			Description: "A seeded project used to measure list and detail reads at production scale.",
			Icon:        "Project", Color: "#5E6AD2", Priority: i % 5, PriorityLabel: priorityLabel(i % 5),
			Progress: 0.5, Health: "onTrack", Status: status, Lead: &lead, CreatorID: lead.ID,
			MemberIDs: []string{lead.ID}, TeamIDs: []string{team.ID}, Milestones: milestones,
			CreatedAt: now.AddDate(0, 0, -30), UpdatedAt: now,
		})
	}
	return projects
}

func makeCycles(teams []domain.Team, now time.Time) []domain.Cycle {
	cycles := []domain.Cycle{}
	for _, team := range teams {
		for n := 0; n < 3; n++ {
			start := now.AddDate(0, 0, -14*(2-n))
			status := "completed"
			if n == 1 {
				status = "current"
			} else if n == 2 {
				status = "upcoming"
			}
			cycles = append(cycles, domain.Cycle{
				ID: fmt.Sprintf("cycle_perf_%d_%s", n, team.ID), Number: n + 1, Name: fmt.Sprintf("Cycle %d", n+1),
				TeamID: team.ID, StartsAt: start, EndsAt: start.AddDate(0, 0, 14), Status: status,
				CreatedAt: start, UpdatedAt: start,
			})
		}
	}
	return cycles
}

func makeCycleMap(teams []domain.Team) map[string][]domain.Cycle {
	cycles := makeCycles(teams, time.Now().UTC())
	result := map[string][]domain.Cycle{}
	for _, cycle := range cycles {
		result[cycle.TeamID] = append(result[cycle.TeamID], cycle)
	}
	return result
}

func makeDocuments(count int, teams []domain.Team, projects []domain.Project, viewer domain.User, now time.Time) []domain.Document {
	documents := make([]domain.Document, 0, count)
	for i := 0; i < count; i++ {
		team := teams[i%len(teams)]
		var projectIDs []string
		if len(projects) > 0 && i%2 == 0 {
			projectIDs = []string{projects[i%len(projects)].ID}
		}
		documents = append(documents, domain.Document{
			ID: fmt.Sprintf("doc_perf_%d", i), SlugID: fmt.Sprintf("doc-%03d-000000000000", i),
			Title: fmt.Sprintf("Runbook %03d", i), Content: "Seeded document content for performance measurement.",
			Creator: viewer, ProjectIDs: projectIDs, TeamIDs: []string{team.ID},
			SubscriberIDs: []string{viewer.ID}, CreatedAt: now.AddDate(0, 0, -60), UpdatedAt: now.Add(-time.Duration(i) * time.Hour),
		})
	}
	return documents
}

func makeViews(count int, teams []domain.Team, now time.Time) []domain.SavedView {
	views := make([]domain.SavedView, 0, count)
	for i := 0; i < count; i++ {
		views = append(views, domain.SavedView{
			ID: fmt.Sprintf("view_perf_%d", i), SlugID: fmt.Sprintf("view-%03d", i), Name: fmt.Sprintf("Saved view %02d", i),
			Resource: "issue", Scope: "workspace", TeamID: teams[i%len(teams)].ID, View: "list",
			Filters: json.RawMessage(`{"priority":[1,2]}`), CreatedAt: now, UpdatedAt: now,
		})
	}
	return views
}

func issueID(workspace string, i int) string {
	return fmt.Sprintf("issue_%s_%d", workspace, i)
}

func sqlDriver(driver string) string {
	switch driver {
	case "postgres":
		return "pgx"
	case "mysql":
		return "mysql"
	default:
		return "sqlite"
	}
}

func mysqlDSN(raw string) (string, error) {
	if !strings.Contains(raw, "://") {
		separator := "?"
		if strings.Contains(raw, "?") {
			separator = "&"
		}
		return raw + separator + "parseTime=true&interpolateParams=true", nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "mysql" || parsed.Host == "" || strings.TrimPrefix(parsed.Path, "/") == "" {
		return "", fmt.Errorf("invalid mysql database URL")
	}
	password, _ := parsed.User.Password()
	credentials := parsed.User.Username()
	if password != "" {
		credentials += ":" + password
	}
	query := parsed.Query()
	query.Set("parseTime", "true")
	query.Set("interpolateParams", "true")
	return fmt.Sprintf("%s@tcp(%s)/%s?%s", credentials, parsed.Host, strings.TrimPrefix(parsed.Path, "/"), query.Encode()), nil
}

// seedContent writes comments, activity history, field history and inbox
// notifications directly into the content table. The rows match what the
// server writes (the same JSON shapes and index columns), so reads exercise
// the production queries.
func seedContent(ctx context.Context, db *sql.DB, driver string, world *seedWorld) error {
	began := time.Now()
	quote := func() string {
		if driver == "mysql" {
			return "INSERT INTO workspace_content_records(workspace_key,kind,resource_id,id,created_at,data,owner_id,parent_id,status,next_attempt_at,record_version) VALUES "
		}
		return "INSERT INTO workspace_content_records(workspace_key,kind,resource_id,id,created_at,data,owner_id,parent_id,status,next_attempt_at,record_version) VALUES "
	}()
	suffix := " ON CONFLICT(workspace_key,kind,resource_id,id) DO UPDATE SET created_at=excluded.created_at,data=excluded.data,owner_id=excluded.owner_id,parent_id=excluded.parent_id,status=excluded.status,next_attempt_at=excluded.next_attempt_at,record_version=excluded.record_version"
	if driver == "mysql" {
		suffix = " ON DUPLICATE KEY UPDATE created_at=VALUES(created_at),data=VALUES(data),owner_id=VALUES(owner_id),parent_id=VALUES(parent_id),status=VALUES(status),next_attempt_at=VALUES(next_attempt_at),record_version=VALUES(record_version)"
	}
	const tuple = "(?,?,?,?,?,?,?,?,?,?,?)"
	batch := 400
	insert := func(rows [][]any) error {
		if len(rows) == 0 {
			return nil
		}
		var builder strings.Builder
		builder.WriteString(quote)
		args := make([]any, 0, len(rows)*11)
		for i, row := range rows {
			if i > 0 {
				builder.WriteString(",")
			}
			builder.WriteString(tuple)
			args = append(args, row...)
		}
		builder.WriteString(suffix)
		_, err := db.ExecContext(ctx, builder.String(), args...)
		return err
	}
	rows := make([][]any, 0, batch)
	cycleRows := make([][]any, 0, batch)
	flushCycles := func() error {
		if len(cycleRows) == 0 {
			return nil
		}
		var builder strings.Builder
		builder.WriteString("INSERT INTO issue_activity_cycles(workspace_key,activity_id,cycle_id,issue_id) VALUES ")
		args := make([]any, 0, len(cycleRows)*4)
		for i, row := range cycleRows {
			if i > 0 {
				builder.WriteString(",")
			}
			builder.WriteString("(?,?,?,?)")
			args = append(args, row...)
		}
		if driver == "mysql" {
			builder.WriteString(" ON DUPLICATE KEY UPDATE activity_id=VALUES(activity_id)")
		} else {
			builder.WriteString(" ON CONFLICT DO NOTHING")
		}
		_, err := db.ExecContext(ctx, builder.String(), args...)
		cycleRows = cycleRows[:0]
		return err
	}
	flush := func() error {
		if err := insert(rows); err != nil {
			return err
		}
		if err := flushCycles(); err != nil {
			return err
		}
		rows = rows[:0]
		return nil
	}
	base := time.Now().UTC().Add(-24 * time.Hour)
	for i := 0; i < world.issues; i++ {
		issue := issueID(world.key, i)
		created := base.Add(time.Duration(i) * time.Minute)
		comments := i % 7
		if world.longTail[i] {
			comments = longTailComments
		}
		for c := 0; c < comments; c++ {
			user := world.users[(i+c)%len(world.users)]
			at := created.Add(time.Duration(c) * 3 * time.Minute).Format(time.RFC3339Nano)
			body := fmt.Sprintf("Comment %d on %s: %s", c, issue, issueDescription(i+c))
			data, _ := json.Marshal(domain.Comment{ID: fmt.Sprintf("comment_%s_%d", issue, c), Version: 1, Body: body, Reactions: map[string][]string{}, CreatedAt: created.Add(time.Duration(c) * 3 * time.Minute), User: user})
			rows = append(rows, []any{world.key, "comment", issue, fmt.Sprintf("comment_%s_%d", issue, c), at, data, "", "", "", "", 1})
			if len(rows) >= batch {
				if err := flush(); err != nil {
					return err
				}
			}
		}
		team := world.teamsByID[i%len(world.teamsByID)]
		cycleID := ""
		if i%3 == 0 {
			if cycles := world.cyclesByTeam[team.ID]; len(cycles) > 0 {
				cycleID = cycles[i%len(cycles)].ID
			}
		}
		for a := 0; a < 10; a++ {
			user := world.users[(i+a)%len(world.users)]
			at := created.Add(time.Duration(a) * time.Minute).Format(time.RFC3339Nano)
			metadata := map[string]string{"field": "status", "to": canonicalStates[stateIndex(i)].Name}
			refs := []string{}
			if cycleID != "" {
				if a == 0 {
					metadata["cycle"] = cycleID
					metadata["cycleBefore"] = ""
					refs = append(refs, cycleID)
				} else if a == 1 && i%6 == 0 {
					metadata["cycleBefore"] = cycleID
					refs = append(refs, cycleID)
				}
			}
			event := domain.ActivityEvent{ID: fmt.Sprintf("activity_%s_%d", issue, a), Type: "issue.updated", CreatedAt: created.Add(time.Duration(a) * time.Minute), Actor: user, Metadata: metadata}
			data, _ := json.Marshal(event)
			rows = append(rows, []any{world.key, "activity", issue, event.ID, at, data, "", "", "", "", 1})
			for _, cycle := range refs {
				cycleRows = append(cycleRows, []any{world.key, event.ID, cycle, issue})
			}
			if len(rows) >= batch {
				if err := flush(); err != nil {
					return err
				}
			}
		}
		if i%50 == 0 {
			at := created.Add(time.Hour).Format(time.RFC3339Nano)
			history := map[string]any{"id": issue, "createdAt": at, "version": 5, "baseVersion": 4, "fields": map[string]int64{"stateId": 5, "priority": 4}}
			data, _ := json.Marshal(history)
			rows = append(rows, []any{world.key, "issue_field_history", issue, issue, at, data, "", "", "", "", 1})
			if len(rows) >= batch {
				if err := flush(); err != nil {
					return err
				}
			}
		}
	}
	if err := flush(); err != nil {
		return err
	}
	if err := seedNotifications(ctx, db, driver, world, base); err != nil {
		return err
	}
	log.Printf("%s: content in %s", world.key, time.Since(began).Round(time.Second))
	return nil
}

func seedNotifications(ctx context.Context, db *sql.DB, driver string, world *seedWorld, base time.Time) error {
	if world.key != mainWorkspace {
		return nil
	}
	const total = 5000
	const batch = 400
	quote := "INSERT INTO workspace_content_records(workspace_key,kind,resource_id,id,created_at,data,owner_id,parent_id,status,next_attempt_at,record_version) VALUES "
	suffix := " ON CONFLICT(workspace_key,kind,resource_id,id) DO UPDATE SET created_at=excluded.created_at,data=excluded.data,owner_id=excluded.owner_id,parent_id=excluded.parent_id,status=excluded.status,next_attempt_at=excluded.next_attempt_at,record_version=excluded.record_version"
	if driver == "mysql" {
		suffix = " ON DUPLICATE KEY UPDATE created_at=VALUES(created_at),data=VALUES(data),owner_id=VALUES(owner_id),parent_id=VALUES(parent_id),status=VALUES(status),next_attempt_at=VALUES(next_attempt_at),record_version=VALUES(record_version)"
	}
	rows := make([][]any, 0, batch)
	flush := func() error {
		if len(rows) == 0 {
			return nil
		}
		var builder strings.Builder
		builder.WriteString(quote)
		args := make([]any, 0, len(rows)*11)
		for i, row := range rows {
			if i > 0 {
				builder.WriteString(",")
			}
			builder.WriteString("(?,?,?,?,?,?,?,?,?,?,?)")
			args = append(args, row...)
		}
		builder.WriteString(suffix)
		_, err := db.ExecContext(ctx, builder.String(), args...)
		rows = rows[:0]
		return err
	}
	for i := 0; i < total; i++ {
		issueIndex := (i * 13) % world.issues
		issue := issueID(world.key, issueIndex)
		actor := world.users[(i+1)%len(world.users)]
		created := base.Add(time.Duration(i) * 2 * time.Minute)
		notification := domain.Notification{
			ID: fmt.Sprintf("notification_perf_%d", i), RecipientID: world.viewer.ID,
			Type: "issue.comment", SourceType: "issue", SourceID: issue, IssueID: issue,
			CommentID: fmt.Sprintf("comment_%s_0", issue), Actor: actor, Category: "comments",
			GroupKey: issue + ":comments", OccurrenceCount: 1 + i%3,
			LatestActorIDs: []string{actor.ID}, CreatedAt: created, UpdatedAt: created,
		}
		if i%5 != 0 {
			read := created.Add(time.Hour)
			notification.ReadAt = &read
		}
		data, _ := json.Marshal(notification)
		rows = append(rows, []any{world.key, "notification", issue, notification.ID, created.Format(time.RFC3339Nano), data, world.viewer.ID, "", "", "", 1})
		if len(rows) >= batch {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	return flush()
}

func defaultWorkspaceSettings() domain.WorkspaceSettings {
	return domain.WorkspaceSettings{SessionDurationDays: 30, AllowedDomains: []string{}, FeatureFlags: map[string]bool{}}
}

func init() {
}
