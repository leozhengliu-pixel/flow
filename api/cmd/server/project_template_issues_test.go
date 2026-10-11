package main

import (
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type projectTemplateFixture struct {
	handler   http.Handler
	store     *store.SQLiteStore
	bootstrap domain.Bootstrap
	teamA     domain.Team
	teamB     domain.Team
	user      domain.User
	label     domain.IssueLabel
	blocker   domain.Project
	blocked   domain.Project
}

func newProjectTemplateFixture(t *testing.T) projectTemplateFixture {
	t.Helper()
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	fixture := projectTemplateFixture{handler: handler, store: repository, bootstrap: bootstrap}
	active := []domain.Team{}
	for _, team := range bootstrap.Teams {
		if team.ArchivedAt == nil && team.RetiredAt == nil {
			active = append(active, team)
		}
	}
	if len(active) < 2 {
		created := requestJSON[domain.Team](t, handler, http.MethodPost, "/api/workspaces/"+bootstrap.Workspace.URLKey+"/teams", map[string]any{"name": "Template second team", "key": "TPLB"}, http.StatusCreated)
		active = append(active, created)
	}
	fixture.teamA, fixture.teamB = active[0], active[1]
	fixture.user = bootstrap.Users[len(bootstrap.Users)-1]
	fixture.label = requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/labels", map[string]any{"name": "Template label", "resourceType": "issue"}, http.StatusCreated)
	fixture.blocker = requestJSON[domain.Project](t, handler, http.MethodPost, "/api/projects", map[string]any{"name": "Template blocker", "teamIds": []string{fixture.teamA.ID}}, http.StatusCreated)
	fixture.blocked = requestJSON[domain.Project](t, handler, http.MethodPost, "/api/projects", map[string]any{"name": "Template blocked", "teamIds": []string{fixture.teamA.ID}}, http.StatusCreated)
	return fixture
}

func (fixture projectTemplateFixture) stored(t *testing.T) domain.Bootstrap {
	t.Helper()
	data, ok := fixture.store.BootstrapFor(fixture.bootstrap.Workspace.URLKey)
	if !ok {
		t.Fatal("workspace missing")
	}
	return data
}

func TestProjectTemplateIssuesAndDependencyRelationsValidation(t *testing.T) {
	fixture := newProjectTemplateFixture(t)
	call := func(method, path string, body map[string]any, status int) domain.ProjectTemplate {
		t.Helper()
		return requestJSON[domain.ProjectTemplate](t, fixture.handler, method, path, body, status)
	}
	milestones := []map[string]any{{"id": "milestone_beta", "name": "Beta"}, {"id": "milestone_ga", "name": "GA"}}
	invalid := map[string]map[string]any{
		"empty title":         {"issues": []map[string]any{{"title": "  ", "priority": 0}}},
		"priority":            {"issues": []map[string]any{{"title": "A", "priority": 5}}},
		"unknown team":        {"issues": []map[string]any{{"title": "A", "teamId": "team_missing"}}},
		"unknown assignee":    {"issues": []map[string]any{{"title": "A", "assigneeId": "user_missing"}}},
		"unknown label":       {"issues": []map[string]any{{"title": "A", "labelIds": []string{"label_missing"}}}},
		"unknown milestone":   {"milestones": milestones, "issues": []map[string]any{{"title": "A", "milestoneId": "milestone_missing"}}},
		"milestone not kept":  {"issues": []map[string]any{{"title": "A", "milestoneId": "milestone_beta"}}},
		"unknown parent":      {"issues": []map[string]any{{"id": "a", "title": "A", "parentId": "missing"}}},
		"self parent":         {"issues": []map[string]any{{"id": "a", "title": "A", "parentId": "a"}}},
		"parent cycle":        {"issues": []map[string]any{{"id": "a", "title": "A", "parentId": "b"}, {"id": "b", "title": "B", "parentId": "a"}}},
		"duplicate ids":       {"issues": []map[string]any{{"id": "a", "title": "A"}, {"id": "a", "title": "B"}}},
		"relation type":       {"dependencyRelations": []map[string]any{{"projectId": fixture.blocker.ID, "type": "related"}}},
		"relation project":    {"dependencyRelations": []map[string]any{{"projectId": "project_missing", "type": "blocks"}}},
		"relation duplicated": {"dependencyRelations": []map[string]any{{"projectId": fixture.blocker.ID, "type": "blocks"}, {"projectId": fixture.blocker.ID, "type": "blocked_by"}}},
	}
	tooMany := make([]map[string]any, maxProjectTemplateIssues+1)
	for index := range tooMany {
		tooMany[index] = map[string]any{"title": fmt.Sprintf("Issue %d", index)}
	}
	invalid["too many issues"] = map[string]any{"issues": tooMany}
	for name, body := range invalid {
		body["name"] = "Invalid " + name
		call(http.MethodPost, "/api/project-templates", body, http.StatusBadRequest)
	}

	template := call(http.MethodPost, "/api/project-templates", map[string]any{
		"name": "Launch", "milestones": milestones,
		"issues": []map[string]any{
			{"id": "parent", "title": " Plan launch ", "teamId": fixture.teamB.ID, "priority": 2, "assigneeId": fixture.user.ID, "labelIds": []string{fixture.label.ID}, "milestoneId": "milestone_beta"},
			{"title": "Write announcement", "parentId": "parent", "milestoneId": "milestone_ga"},
		},
		"dependencyRelations": []map[string]any{{"projectId": fixture.blocker.ID, "type": "blocked_by"}, {"projectId": fixture.blocked.ID, "type": "blocks"}},
	}, http.StatusCreated)
	if len(template.Issues) != 2 || template.Issues[0].Title != "Plan launch" || template.Issues[1].ID == "" || template.Issues[1].ParentID != "parent" || template.Issues[0].MilestoneID != "milestone_beta" {
		t.Fatalf("template issues were not normalized: %#v", template.Issues)
	}
	if !slices.Equal(template.DependencyIDs, []string{fixture.blocker.ID}) || len(template.DependencyRelations) != 2 {
		t.Fatalf("dependency relations did not keep dependencyIds in sync: %#v %#v", template.DependencyIDs, template.DependencyRelations)
	}

	// Dropping a milestone clears the template issues that pointed at it.
	template = call(http.MethodPatch, "/api/project-templates/"+template.ID, map[string]any{"milestones": []map[string]any{{"id": "milestone_beta", "name": "Beta"}}}, http.StatusOK)
	if template.Issues[0].MilestoneID != "milestone_beta" || template.Issues[1].MilestoneID != "" {
		t.Fatalf("template issue milestones were not pruned: %#v", template.Issues)
	}
	// Legacy writers replace the blocked-by list and keep blocking relations.
	template = call(http.MethodPatch, "/api/project-templates/"+template.ID, map[string]any{"dependencyIds": []string{}}, http.StatusOK)
	if len(template.DependencyIDs) != 0 || len(template.DependencyRelations) != 1 || template.DependencyRelations[0].Type != "blocks" || template.DependencyRelations[0].ProjectID != fixture.blocked.ID {
		t.Fatalf("legacy dependencyIds update lost blocking relations: %#v", template.DependencyRelations)
	}
	// Label merges and deletions reach template issue labels.
	source := requestJSON[domain.IssueLabel](t, fixture.handler, http.MethodPost, "/api/labels", map[string]any{"name": "Template source label", "resourceType": "issue"}, http.StatusCreated)
	call(http.MethodPatch, "/api/project-templates/"+template.ID, map[string]any{"issues": []map[string]any{{"id": "labelled", "title": "Labelled", "labelIds": []string{source.ID}}}}, http.StatusOK)
	requestJSON[domain.IssueLabel](t, fixture.handler, http.MethodPost, "/api/labels/merge", map[string]any{"toLabelId": fixture.label.ID, "fromLabelIds": []string{source.ID}}, http.StatusOK)
	templateIssueLabels := func() []string {
		data := fixture.stored(t)
		index := slices.IndexFunc(data.ProjectTemplates, func(item domain.ProjectTemplate) bool { return item.ID == template.ID })
		return data.ProjectTemplates[index].Issues[0].LabelIDs
	}
	if labels := templateIssueLabels(); !slices.Equal(labels, []string{fixture.label.ID}) {
		t.Fatalf("label merge did not reach template issues: %v", labels)
	}
	requestJSON[any](t, fixture.handler, http.MethodDelete, "/api/labels/"+fixture.label.ID, nil, http.StatusNoContent)
	if labels := templateIssueLabels(); len(labels) != 0 {
		t.Fatalf("label deletion did not reach template issues: %v", labels)
	}
	template = call(http.MethodPatch, "/api/project-templates/"+template.ID, map[string]any{"dependencyRelations": []map[string]any{}, "issues": []map[string]any{}}, http.StatusOK)
	if len(template.DependencyRelations) != 0 || len(template.DependencyIDs) != 0 || len(template.Issues) != 0 {
		t.Fatalf("clearing template issues and dependencies failed: %#v", template)
	}
}

func TestCreateProjectFromTemplateCreatesIssuesAndDependencies(t *testing.T) {
	fixture := newProjectTemplateFixture(t)
	template := requestJSON[domain.ProjectTemplate](t, fixture.handler, http.MethodPost, "/api/project-templates", map[string]any{
		"name": "Launch", "projectName": "Launch project", "teamIds": []string{fixture.teamA.ID},
		"milestones": []map[string]any{{"id": "milestone_beta", "name": "Beta", "description": "Beta criteria"}, {"id": "milestone_ga", "name": "GA"}},
		"issues": []map[string]any{
			{"id": "parent", "title": "Plan launch", "description": "Plan it", "teamId": fixture.teamB.ID, "priority": 2, "assigneeId": fixture.user.ID, "labelIds": []string{fixture.label.ID}, "milestoneId": "milestone_beta"},
			{"id": "child", "title": "Write announcement", "parentId": "parent", "priority": 3, "milestoneId": "milestone_ga"},
			{"id": "loose", "title": "Retro"},
		},
		"dependencyRelations": []map[string]any{{"projectId": fixture.blocker.ID, "type": "blocked_by"}, {"projectId": fixture.blocked.ID, "type": "blocks"}},
	}, http.StatusCreated)
	before := fixture.stored(t)
	firstNumber := nextIssueNumber(before.Issues)

	project := requestJSON[domain.Project](t, fixture.handler, http.MethodPost, "/api/projects", map[string]any{"templateId": template.ID}, http.StatusCreated)
	if project.Name != "Launch project" || len(project.Milestones) != 2 || !slices.Equal(project.DependencyIDs, []string{fixture.blocker.ID}) {
		t.Fatalf("template project defaults were not applied: %#v", project)
	}
	data := fixture.stored(t)
	created := map[string]domain.Issue{}
	for _, issue := range data.Issues {
		if issue.Project != nil && issue.Project.ID == project.ID {
			created[issue.Title] = issue
		}
	}
	if len(created) != 3 {
		t.Fatalf("template issues were not created in the project: %#v", created)
	}
	parent, child, loose := created["Plan launch"], created["Write announcement"], created["Retro"]
	if parent.Number != firstNumber || child.Number != firstNumber+1 || loose.Number != firstNumber+2 || parent.Identifier != fmt.Sprintf("%s-%d", fixture.teamB.Key, firstNumber) {
		t.Fatalf("template issues were not numbered in order from %d: %d %d %d (%s)", firstNumber, parent.Number, child.Number, loose.Number, parent.Identifier)
	}
	if parent.Team.ID != fixture.teamB.ID || child.Team.ID != fixture.teamA.ID || loose.Team.ID != fixture.teamA.ID {
		t.Fatalf("template issue teams were not resolved: %s %s %s", parent.Team.ID, child.Team.ID, loose.Team.ID)
	}
	if defaultState := stateForTeam(&data, fixture.teamB.ID, teamSettings(&data, fixture.teamB.ID).DefaultStateID); defaultState != nil && parent.State.ID != defaultState.ID {
		t.Fatalf("template issue did not take the team default state: %s want %s", parent.State.ID, defaultState.ID)
	}
	if parent.Priority != 2 || parent.Description != "Plan it" || parent.Assignee == nil || parent.Assignee.ID != fixture.user.ID || len(parent.Labels) != 1 || parent.Labels[0].ID != fixture.label.ID {
		t.Fatalf("template issue properties were not applied: %#v", parent)
	}
	milestoneID := func(name string) string {
		index := slices.IndexFunc(project.Milestones, func(item domain.ProjectMilestone) bool { return item.Name == name })
		if index < 0 {
			t.Fatalf("project milestone %q missing: %#v", name, project.Milestones)
		}
		return project.Milestones[index].ID
	}
	if parent.ProjectMilestoneID == nil || *parent.ProjectMilestoneID != milestoneID("Beta") || child.ProjectMilestoneID == nil || *child.ProjectMilestoneID != milestoneID("GA") || loose.ProjectMilestoneID != nil {
		t.Fatalf("template issue milestones were not mapped: %v %v %v", parent.ProjectMilestoneID, child.ProjectMilestoneID, loose.ProjectMilestoneID)
	}
	if child.ParentID == nil || *child.ParentID != parent.ID || !slices.Equal(parent.SubIssueIDs, []string{child.ID}) || loose.ParentID != nil {
		t.Fatalf("template sub-issues were not linked: child parent %v, parent subs %v", child.ParentID, parent.SubIssueIDs)
	}
	if !slices.ContainsFunc(data.Activities[child.ID], func(event domain.ActivityEvent) bool { return event.Type == "issue.created" }) {
		t.Fatal("template issue creation activity is missing")
	}
	hasRelation := func(relatedID, relationType string) bool {
		return slices.ContainsFunc(data.ProjectRelations, func(relation domain.ProjectRelation) bool {
			return relation.ProjectID == project.ID && relation.RelatedProjectID == relatedID && relation.Type == relationType
		})
	}
	if !hasRelation(fixture.blocker.ID, "blocked_by") || !hasRelation(fixture.blocked.ID, "blocks") {
		t.Fatalf("template dependency relations were not applied in both directions: %#v", data.ProjectRelations)
	}

	// The workspace issue sequence continues after the template issues.
	next := requestJSON[domain.Issue](t, fixture.handler, http.MethodPost, "/api/issues", map[string]any{"title": "After template", "teamId": fixture.teamA.ID}, http.StatusCreated)
	if next.Number != firstNumber+3 {
		t.Fatalf("issue numbering did not continue after template issues: %d want %d", next.Number, firstNumber+3)
	}

	// The dialog sends the template milestones itself (milestoneDetails with
	// templateMilestoneId) and an explicit dependency list.
	fromDialog := requestJSON[domain.Project](t, fixture.handler, http.MethodPost, "/api/projects", map[string]any{
		"templateId": template.ID, "name": "Dialog launch", "teamIds": []string{fixture.teamB.ID}, "dependencyRelations": []map[string]any{},
		"milestoneDetails": []map[string]any{{"name": "Beta renamed", "templateMilestoneId": "milestone_beta"}, {"name": "Extra"}},
	}, http.StatusCreated)
	if len(fromDialog.Milestones) != 2 || fromDialog.Milestones[0].Name != "Beta renamed" || fromDialog.Milestones[1].Name != "Extra" || len(fromDialog.DependencyIDs) != 0 {
		t.Fatalf("milestoneDetails should replace the template milestones: %#v", fromDialog.Milestones)
	}
	data = fixture.stored(t)
	dialogIssues := map[string]domain.Issue{}
	for _, issue := range data.Issues {
		if issue.Project != nil && issue.Project.ID == fromDialog.ID {
			dialogIssues[issue.Title] = issue
		}
	}
	if len(dialogIssues) != 3 || dialogIssues["Plan launch"].ProjectMilestoneID == nil || *dialogIssues["Plan launch"].ProjectMilestoneID != fromDialog.Milestones[0].ID || dialogIssues["Write announcement"].ProjectMilestoneID != nil {
		t.Fatalf("template issue milestones were not mapped through templateMilestoneId: %#v", dialogIssues)
	}
	if dialogIssues["Retro"].Team.ID != fixture.teamB.ID {
		t.Fatalf("template issue without a team should use the project's first team: %s", dialogIssues["Retro"].Team.ID)
	}
	if slices.ContainsFunc(data.ProjectRelations, func(relation domain.ProjectRelation) bool { return relation.ProjectID == fromDialog.ID }) {
		t.Fatal("an explicit empty dependency list should not take the template's relations")
	}
}

func TestProjectTemplateIssueTeamFallback(t *testing.T) {
	now := time.Now()
	data := &domain.Bootstrap{Teams: []domain.Team{
		{ID: "archived", ArchivedAt: &now},
		{ID: "retired", RetiredAt: &now},
		{ID: "first"},
		{ID: "own"},
	}}
	project := domain.Project{TeamIDs: []string{"retired", "first"}}
	cases := map[string]string{"own": "own", "archived": "first", "retired": "first", "": "first"}
	for teamID, want := range cases {
		team, ok := projectTemplateIssueTeam(data, domain.ProjectTemplateIssue{TeamID: teamID}, project)
		if !ok || team.ID != want {
			t.Fatalf("team %q resolved to %q, want %q", teamID, team.ID, want)
		}
	}
	if team, ok := projectTemplateIssueTeam(data, domain.ProjectTemplateIssue{}, domain.Project{TeamIDs: []string{"archived"}}); !ok || team.ID != "first" {
		t.Fatalf("workspace fallback resolved to %q", team.ID)
	}
}

func TestCreateProjectFromTemplateCreatesIssuesOnFullMutationPath(t *testing.T) {
	restore := store.ForceFullMutationsForTesting(true)
	defer restore()
	TestCreateProjectFromTemplateCreatesIssuesAndDependencies(t)
}
