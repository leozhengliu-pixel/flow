package main

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func labelMoveFixture(t *testing.T) (*store.SQLiteStore, http.Handler, domain.Bootstrap) {
	t.Helper()
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	return repository, handler, requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
}

func labelMoveMetadata(t *testing.T, repository *store.SQLiteStore, key string) domain.Bootstrap {
	t.Helper()
	data, ok := repository.WorkspaceMetadata(key)
	if !ok {
		t.Fatal("workspace metadata missing")
	}
	return data
}

// teamLabelNamed is the label named name owned by scope.
func teamLabelNamed(t *testing.T, labels []domain.IssueLabel, scope, name string) domain.IssueLabel {
	t.Helper()
	found := []domain.IssueLabel{}
	for _, label := range labels {
		if label.Scope == scope && label.Name == name {
			found = append(found, label)
		}
	}
	if len(found) != 1 {
		t.Fatalf("team %s has %d labels named %s", scope, len(found), name)
	}
	return found[0]
}

func movedIssueLabelIDs(issue domain.Issue) []string {
	ids := []string{}
	for _, label := range issue.Labels {
		ids = append(ids, label.ID)
	}
	slices.Sort(ids)
	return ids
}

// Moving a workspace group re-points what referenced its labels to each
// team's copies (or removes the reference where no copy reaches), so
// templates, email intake, triage and SLA rules, views, subscriptions,
// trashed issues and the audit log stay consistent.
func TestMoveWorkspaceLabelGroupRepointsReferences(t *testing.T) {
	repository, handler, before := labelMoveFixture(t)
	key := before.Workspace.URLKey
	first := before.Issues[0]
	teamA := first.Team.ID
	teamB := requestJSON[domain.Team](t, handler, http.MethodPost, "/api/workspaces/test-workspace/teams", map[string]any{"name": "Second team", "key": "SEC"}, http.StatusCreated).ID
	teamD := requestJSON[domain.Team](t, handler, http.MethodPost, "/api/workspaces/test-workspace/teams", map[string]any{"name": "Third team", "key": "THD"}, http.StatusCreated).ID
	second := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Second team issue", "teamId": teamB}, http.StatusCreated)
	group := requestJSON[domain.LabelGroup](t, handler, http.MethodPost, "/api/label-groups", map[string]any{"name": "Stage", "resourceType": "issue", "multiSelect": true}, http.StatusCreated)
	alpha := requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/labels", map[string]any{"name": "Alpha", "resourceType": "issue", "groupId": group.ID}, http.StatusCreated)
	beta := requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/labels", map[string]any{"name": "Beta", "resourceType": "issue", "groupId": group.ID}, http.StatusCreated)
	requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+first.ID, map[string]any{"labelIds": []string{alpha.ID}}, http.StatusOK)
	requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+second.ID, map[string]any{"labelIds": []string{alpha.ID, beta.ID}}, http.StatusOK)

	template := requestJSON[domain.IssueTemplate](t, handler, http.MethodPost, "/api/teams/"+teamA+"/templates", map[string]any{"name": "Bug", "title": "From template", "labelIds": []string{alpha.ID}, "subIssues": []map[string]any{{"title": "Child", "labelIds": []string{beta.ID}}}}, http.StatusCreated)
	shared := requestJSON[domain.IssueTemplate](t, handler, http.MethodPost, "/api/issue-templates", map[string]any{"name": "Shared", "labelIds": []string{alpha.ID}}, http.StatusCreated)
	intake := requestJSON[intakeCreateResponse](t, handler, http.MethodPost, "/api/teams/"+teamA+"/email-intake-addresses", map[string]any{"type": "asks", "templateId": template.ID}, http.StatusCreated)
	// Trashed issues: one in a team that gets a copy, one in a team that does not.
	trashedA := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Trashed A", "teamId": teamA, "labelIds": []string{alpha.ID}}, http.StatusCreated)
	trashedD := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Trashed D", "teamId": teamD, "labelIds": []string{beta.ID}}, http.StatusCreated)
	requestJSON[any](t, handler, http.MethodDelete, "/api/issues/"+trashedA.ID, nil, http.StatusNoContent)
	requestJSON[any](t, handler, http.MethodDelete, "/api/issues/"+trashedD.ID, nil, http.StatusNoContent)

	viewer := before.Viewer.ID
	other := ""
	for _, user := range before.Users {
		if user.ID != viewer {
			other = user.ID
			break
		}
	}
	if other == "" {
		t.Fatal("the fixture needs a second user")
	}
	now := time.Now().UTC()
	if err := repository.MutateWorkspace(workspaceFixtureContext(t.Context()), key, "test.label_move_setup", group.ID, nil, func(data *domain.Bootstrap) error {
		// The viewer belongs to team A only; the other user to no team.
		data.TeamMembers = slices.DeleteFunc(data.TeamMembers, func(member domain.TeamMember) bool {
			return member.UserID == other || member.UserID == viewer
		})
		data.TeamMembers = append(data.TeamMembers, domain.TeamMember{TeamID: teamA, UserID: viewer, Role: "member", JoinedAt: now})
		data.TriageRoutingRules = append(data.TriageRoutingRules, domain.TriageRoutingRule{ID: "triage-move", TeamID: teamB, Name: "Alpha bugs", Enabled: true, Conditions: map[string]string{"labelId": alpha.ID}, ResponsibilityID: "none", LabelIDs: []string{beta.ID}, CreatedAt: now, UpdatedAt: now})
		data.SLARules = append(data.SLARules,
			domain.SLARule{ID: "sla-team", Name: "Team alpha", TeamIDs: []string{teamB}, Filters: map[string]any{"label": alpha.ID}, TargetMinutes: 60, Enabled: true, CreatedAt: now, UpdatedAt: now},
			domain.SLARule{ID: "sla-all", Name: "Any beta", Filters: map[string]any{"label": beta.ID}, TargetMinutes: 60, Enabled: true, CreatedAt: now, UpdatedAt: now})
		data.SavedViews = append(data.SavedViews,
			domain.SavedView{ID: "view-team", Name: "Team stage", Resource: "issues", Scope: "team", TeamID: teamA, View: "list", Filters: json.RawMessage(`{"labels":["` + alpha.ID + `"]}`), Display: json.RawMessage(`{"grouping":"Label group","labelGroupId":"` + group.ID + `"}`), CreatedAt: now, UpdatedAt: now},
			domain.SavedView{ID: "view-workspace", Name: "All stage", Resource: "issues", Scope: "workspace", View: "list", Filters: json.RawMessage(`{"labels":["` + beta.ID + `"],"priority":["1"]}`), Display: json.RawMessage(`{"grouping":"Label group","labelGroupId":"` + group.ID + `","subLabelGroupId":"` + group.ID + `"}`), CreatedAt: now, UpdatedAt: now})
		data.Subscriptions = append(data.Subscriptions,
			domain.Subscription{ID: "sub-viewer", UserID: viewer, ResourceType: "label", ResourceID: alpha.ID, CreatedAt: now},
			domain.Subscription{ID: "sub-other", UserID: other, ResourceType: "label", ResourceID: alpha.ID, CreatedAt: now})
		data.Favorites = append(data.Favorites, domain.Favorite{ID: "fav-viewer", UserID: viewer, ResourceType: "label", ResourceID: group.ID, Position: 1, CreatedAt: now})
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	moved := requestJSON[[]domain.LabelGroup](t, handler, http.MethodPost, "/api/label-groups/"+group.ID+"/move-to-teams", map[string]any{}, http.StatusOK)
	if len(moved) != 2 {
		t.Fatalf("moved groups %#v, want one for each team using the group", moved)
	}
	groups := map[string]string{}
	for _, item := range moved {
		groups[item.Scope] = item.ID
	}
	if groups[teamA] == "" || groups[teamB] == "" || groups[teamD] != "" {
		t.Fatalf("moved groups %#v, want teams A and B only", moved)
	}
	after := labelMoveMetadata(t, repository, key)
	alphaA, betaA := teamLabelNamed(t, after.Labels, teamA, "Alpha"), teamLabelNamed(t, after.Labels, teamA, "Beta")
	alphaB, betaB := teamLabelNamed(t, after.Labels, teamB, "Alpha"), teamLabelNamed(t, after.Labels, teamB, "Beta")
	if alphaA.GroupID != groups[teamA] || alphaB.GroupID != groups[teamB] {
		t.Fatalf("copies are not in their team's group: %#v %#v", alphaA, alphaB)
	}

	// Templates: the team template (and its sub-issue) use team A's copies;
	// the workspace template loses the label.
	for _, item := range after.IssueTemplates {
		switch item.ID {
		case template.ID:
			if !slices.Equal(item.LabelIDs, []string{alphaA.ID}) || len(item.SubIssues) != 1 || !slices.Equal(item.SubIssues[0].LabelIDs, []string{betaA.ID}) {
				t.Fatalf("team template not re-pointed: %#v", item)
			}
		case shared.ID:
			if len(item.LabelIDs) != 0 {
				t.Fatalf("workspace template kept a moved label: %#v", item.LabelIDs)
			}
		}
	}
	fromTemplate := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Templated", "teamId": teamA, "templateId": template.ID}, http.StatusCreated)
	if !slices.Equal(movedIssueLabelIDs(fromTemplate), []string{alphaA.ID}) {
		t.Fatalf("issue from the template got labels %v, want team A's Alpha", movedIssueLabelIDs(fromTemplate))
	}
	mailed := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/email-intake/"+intake.InboundToken+"/receive", map[string]any{"messageId": "move-1", "from": "Ada <ada@example.test>", "subject": "Mailed", "text": "Body"}, http.StatusCreated)
	if !slices.Equal(movedIssueLabelIDs(mailed), []string{alphaA.ID}) {
		t.Fatalf("email intake issue got labels %v, want team A's Alpha", movedIssueLabelIDs(mailed))
	}

	// Triage and SLA rules follow team B's copies; a rule over several teams
	// matches by name.
	rule := after.TriageRoutingRules[slices.IndexFunc(after.TriageRoutingRules, func(item domain.TriageRoutingRule) bool { return item.ID == "triage-move" })]
	if rule.Conditions["labelId"] != alphaB.ID || !slices.Equal(rule.LabelIDs, []string{betaB.ID}) {
		t.Fatalf("triage rule not re-pointed: %#v", rule)
	}
	for _, item := range after.SLARules {
		if item.ID == "sla-team" && item.Filters["label"] != alphaB.ID || item.ID == "sla-all" && item.Filters["label"] != "Beta" {
			t.Fatalf("SLA rule %s label filter %v", item.ID, item.Filters["label"])
		}
	}

	// Views: the team view filters and groups by team A's copies; the
	// workspace view loses the label filter and the group grouping.
	for _, view := range after.SavedViews {
		switch view.ID {
		case "view-team":
			if !strings.Contains(string(view.Filters), alphaA.ID) || strings.Contains(string(view.Filters), alpha.ID+`"`) || !strings.Contains(string(view.Display), `"labelGroupId":"`+groups[teamA]+`"`) {
				t.Fatalf("team view not re-pointed: %s %s", view.Filters, view.Display)
			}
		case "view-workspace":
			if strings.Contains(string(view.Filters), beta.ID) || !strings.Contains(string(view.Filters), "priority") || strings.Contains(string(view.Display), "abelGroupId") {
				t.Fatalf("workspace view kept the moved group: %s %s", view.Filters, view.Display)
			}
		}
	}

	// Subscriptions and favorites follow to the copies of the user's teams.
	subscribed := []string{}
	for _, item := range after.Subscriptions {
		if item.ResourceType == "label" && (item.UserID == viewer || item.UserID == other) {
			subscribed = append(subscribed, item.UserID+"/"+item.ResourceID)
		}
	}
	if !slices.Equal(subscribed, []string{viewer + "/" + alphaA.ID}) {
		t.Fatalf("label subscriptions %v, want the viewer on team A's Alpha only", subscribed)
	}
	favorites := []string{}
	for _, item := range after.Favorites {
		if item.ResourceType == "label" && item.UserID == viewer {
			favorites = append(favorites, item.ResourceID)
		}
	}
	if !slices.Equal(favorites, []string{groups[teamA]}) {
		t.Fatalf("label favorites %v, want team A's group", favorites)
	}

	// The move is audited.
	if !slices.ContainsFunc(auditLogOf(t, repository, key), func(entry domain.AuditLogEntry) bool {
		return entry.Action == "moved_to_teams" && entry.ResourceType == "label_group" && entry.ResourceID == group.ID
	}) {
		t.Fatal("the move has no audit entry")
	}

	// Restoring a trashed issue brings back its team's copy, or no label
	// where its team got none.
	for _, entry := range after.Trash {
		if entry.ResourceID == trashedA.ID || entry.ResourceID == trashedD.ID {
			requestJSON[any](t, handler, http.MethodPost, "/api/trash/"+entry.ID+"/restore", nil, http.StatusOK)
		}
	}
	restored := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if got := movedIssueLabelIDs(findIssue(t, restored.Issues, trashedA.ID)); !slices.Equal(got, []string{alphaA.ID}) {
		t.Fatalf("restored issue in team A has labels %v, want team A's Alpha", got)
	}
	if got := movedIssueLabelIDs(findIssue(t, restored.Issues, trashedD.ID)); len(got) != 0 {
		t.Fatalf("restored issue in team D has labels %v, want none", got)
	}
}

// Team labels in a workspace group stay in their team; parent and sub-team
// share one copy (the sub-team inherits it).
func TestMoveWorkspaceLabelGroupKeepsTeamLabelsAndSubtrees(t *testing.T) {
	repository, handler, before := labelMoveFixture(t)
	key := before.Workspace.URLKey
	teams := "/api/workspaces/test-workspace/teams"
	parent := requestJSON[domain.Team](t, handler, http.MethodPost, teams, map[string]any{"name": "Platform", "key": "PLT"}, http.StatusCreated).ID
	child := requestJSON[domain.Team](t, handler, http.MethodPost, teams, map[string]any{"name": "Runtime", "key": "RTM", "parentTeamId": parent}, http.StatusCreated).ID
	private := requestJSON[domain.Team](t, handler, http.MethodPost, teams, map[string]any{"name": "Vault", "key": "VLT", "private": true}, http.StatusCreated).ID
	group := requestJSON[domain.LabelGroup](t, handler, http.MethodPost, "/api/label-groups", map[string]any{"name": "Stage", "resourceType": "issue"}, http.StatusCreated)
	alpha := requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/labels", map[string]any{"name": "Alpha", "resourceType": "issue", "groupId": group.ID}, http.StatusCreated)
	delta := requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/teams/"+parent+"/labels", map[string]any{"name": "Delta", "groupId": group.ID}, http.StatusCreated)
	gamma := requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/teams/"+private+"/labels", map[string]any{"name": "Gamma", "groupId": group.ID}, http.StatusCreated)
	parentIssue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Parent", "teamId": parent, "labelIds": []string{alpha.ID}}, http.StatusCreated)
	childIssue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Child", "teamId": child, "labelIds": []string{alpha.ID}}, http.StatusCreated)
	privateIssue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Private", "teamId": private, "labelIds": []string{gamma.ID}}, http.StatusCreated)

	moved := requestJSON[[]domain.LabelGroup](t, handler, http.MethodPost, "/api/label-groups/"+group.ID+"/move-to-teams", map[string]any{}, http.StatusOK)
	groups := map[string]string{}
	for _, item := range moved {
		groups[item.Scope] = item.ID
	}
	if len(moved) != 2 || groups[parent] == "" || groups[private] == "" {
		t.Fatalf("moved groups %#v, want the parent's copy and the private team's own group", moved)
	}
	after := labelMoveMetadata(t, repository, key)
	if slices.ContainsFunc(after.LabelGroups, func(item domain.LabelGroup) bool { return item.Scope == child }) {
		t.Fatal("the sub-team got its own copy of the group")
	}
	alphaCopies := slices.DeleteFunc(slices.Clone(after.Labels), func(label domain.IssueLabel) bool { return label.Name != "Alpha" })
	if len(alphaCopies) != 1 || alphaCopies[0].Scope != parent || alphaCopies[0].GroupID != groups[parent] {
		t.Fatalf("Alpha copies %#v, want one in the parent's group", alphaCopies)
	}
	for _, id := range []string{parentIssue.ID, childIssue.ID} {
		issue := findIssue(t, requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK).Issues, id)
		if !slices.Equal(movedIssueLabelIDs(issue), []string{alphaCopies[0].ID}) {
			t.Fatalf("issue %s labels %v, want the parent's Alpha", id, movedIssueLabelIDs(issue))
		}
	}
	// The team labels stay in their teams: the parent's joins its copy of the
	// group, the private team's gets a group of its own; nothing is copied
	// into the private team.
	for _, label := range after.Labels {
		switch label.ID {
		case delta.ID:
			if label.Scope != parent || label.GroupID != groups[parent] {
				t.Fatalf("parent team label %#v", label)
			}
		case gamma.ID:
			if label.Scope != private || label.GroupID != groups[private] {
				t.Fatalf("private team label %#v", label)
			}
		}
		if label.Scope == private && label.ID != gamma.ID {
			t.Fatalf("label %#v was copied into the private team", label)
		}
	}
	issue := findIssue(t, requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK).Issues, privateIssue.ID)
	if len(issue.Labels) != 1 || issue.Labels[0].ID != gamma.ID || issue.Labels[0].GroupID != groups[private] {
		t.Fatalf("private issue labels %#v", issue.Labels)
	}
}

// A copy that would clash with a team's own label is refused; a team that
// already has the same group and label reuses them.
func TestMoveWorkspaceLabelGroupNameCollisions(t *testing.T) {
	repository, handler, before := labelMoveFixture(t)
	key := before.Workspace.URLKey
	first := before.Issues[0]
	teamA := first.Team.ID
	teamB := requestJSON[domain.Team](t, handler, http.MethodPost, "/api/workspaces/test-workspace/teams", map[string]any{"name": "Second team", "key": "SEC"}, http.StatusCreated).ID

	// Team A has an ungrouped label named like one of the group's labels.
	group := requestJSON[domain.LabelGroup](t, handler, http.MethodPost, "/api/label-groups", map[string]any{"name": "Stage", "resourceType": "issue"}, http.StatusCreated)
	alpha := requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/labels", map[string]any{"name": "Alpha", "resourceType": "issue", "groupId": group.ID}, http.StatusCreated)
	requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/teams/"+teamA+"/labels", map[string]any{"name": "alpha"}, http.StatusCreated)
	requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+first.ID, map[string]any{"labelIds": []string{alpha.ID}}, http.StatusOK)
	requestJSON[any](t, handler, http.MethodPost, "/api/label-groups/"+group.ID+"/move-to-teams", map[string]any{}, http.StatusConflict)
	if data := labelMoveMetadata(t, repository, key); !slices.ContainsFunc(data.LabelGroups, func(item domain.LabelGroup) bool { return item.ID == group.ID }) ||
		!slices.ContainsFunc(data.Labels, func(item domain.IssueLabel) bool { return item.ID == alpha.ID }) {
		t.Fatal("a refused move changed the group")
	}

	// Team B already has a "Stage" group with "Beta": it is reused.
	other := requestJSON[domain.LabelGroup](t, handler, http.MethodPost, "/api/label-groups", map[string]any{"name": "Review", "resourceType": "issue", "multiSelect": true}, http.StatusCreated)
	beta := requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/labels", map[string]any{"name": "Beta", "resourceType": "issue", "groupId": other.ID}, http.StatusCreated)
	gamma := requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/labels", map[string]any{"name": "Gamma", "resourceType": "issue", "groupId": other.ID}, http.StatusCreated)
	teamGroup := requestJSON[domain.LabelGroup](t, handler, http.MethodPost, "/api/label-groups", map[string]any{"name": "review", "resourceType": "issue", "scope": teamB, "multiSelect": true}, http.StatusCreated)
	teamBeta := requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/teams/"+teamB+"/labels", map[string]any{"name": "BETA", "groupId": teamGroup.ID}, http.StatusCreated)
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "B issue", "teamId": teamB, "labelIds": []string{beta.ID, gamma.ID}}, http.StatusCreated)
	moved := requestJSON[[]domain.LabelGroup](t, handler, http.MethodPost, "/api/label-groups/"+other.ID+"/move-to-teams", map[string]any{}, http.StatusOK)
	if len(moved) != 1 || moved[0].ID != teamGroup.ID {
		t.Fatalf("moved groups %#v, want team B's existing group", moved)
	}
	after := labelMoveMetadata(t, repository, key)
	gammaB := teamLabelNamed(t, after.Labels, teamB, "Gamma")
	if gammaB.GroupID != teamGroup.ID {
		t.Fatalf("new copy %#v is not in the reused group", gammaB)
	}
	got := movedIssueLabelIDs(findIssue(t, requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK).Issues, issue.ID))
	want := []string{teamBeta.ID, gammaB.ID}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("issue labels %v, want the reused Beta and the new Gamma %v", got, want)
	}

	// A same-named team group of the other type is refused.
	single := requestJSON[domain.LabelGroup](t, handler, http.MethodPost, "/api/label-groups", map[string]any{"name": "Kind", "resourceType": "issue", "multiSelect": true}, http.StatusCreated)
	kind := requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/labels", map[string]any{"name": "Feature", "resourceType": "issue", "groupId": single.ID}, http.StatusCreated)
	requestJSON[domain.LabelGroup](t, handler, http.MethodPost, "/api/label-groups", map[string]any{"name": "Kind", "resourceType": "issue", "scope": teamB}, http.StatusCreated)
	requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Kind issue", "teamId": teamB, "labelIds": []string{kind.ID}}, http.StatusCreated)
	requestJSON[any](t, handler, http.MethodPost, "/api/label-groups/"+single.ID+"/move-to-teams", map[string]any{}, http.StatusConflict)
}

// Moving a single label shares the group move's reference handling.
func TestMoveWorkspaceLabelToTeamsRepointsTemplates(t *testing.T) {
	repository, handler, before := labelMoveFixture(t)
	first := before.Issues[0]
	label := requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/labels", map[string]any{"name": "Solo", "resourceType": "issue"}, http.StatusCreated)
	requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+first.ID, map[string]any{"labelIds": []string{label.ID}}, http.StatusOK)
	template := requestJSON[domain.IssueTemplate](t, handler, http.MethodPost, "/api/teams/"+first.Team.ID+"/templates", map[string]any{"name": "Solo", "title": "Solo", "labelIds": []string{label.ID}}, http.StatusCreated)
	moved := requestJSON[[]domain.IssueLabel](t, handler, http.MethodPost, "/api/labels/"+label.ID+"/move-to-teams", nil, http.StatusOK)
	if len(moved) != 1 || moved[0].Scope != first.Team.ID {
		t.Fatalf("moved labels %#v", moved)
	}
	after := labelMoveMetadata(t, repository, before.Workspace.URLKey)
	if index := slices.IndexFunc(after.IssueTemplates, func(item domain.IssueTemplate) bool { return item.ID == template.ID }); index < 0 || !slices.Equal(after.IssueTemplates[index].LabelIDs, []string{moved[0].ID}) {
		t.Fatal("the team template was not re-pointed to the team's label")
	}
	created := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Templated", "teamId": first.Team.ID, "templateId": template.ID}, http.StatusCreated)
	if !slices.Equal(movedIssueLabelIDs(created), []string{moved[0].ID}) {
		t.Fatalf("issue from the template got labels %v", movedIssueLabelIDs(created))
	}
	if !slices.ContainsFunc(auditLogOf(t, repository, before.Workspace.URLKey), func(entry domain.AuditLogEntry) bool {
		return entry.Action == "moved_to_teams" && entry.ResourceType == "label" && entry.ResourceID == label.ID
	}) {
		t.Fatal("the label move has no audit entry")
	}
}

// A trashed issue restored after its label was merged carries the target.
func TestRestoreTrashedIssueAfterLabelMerge(t *testing.T) {
	repository, handler, before := labelMoveFixture(t)
	first := before.Issues[0]
	from := requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/labels", map[string]any{"name": "Old", "resourceType": "issue"}, http.StatusCreated)
	to := requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/labels", map[string]any{"name": "New", "resourceType": "issue"}, http.StatusCreated)
	gone := requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/labels", map[string]any{"name": "Gone", "resourceType": "issue"}, http.StatusCreated)
	trashed := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Trashed", "teamId": first.Team.ID, "labelIds": []string{from.ID, gone.ID}}, http.StatusCreated)
	requestJSON[any](t, handler, http.MethodDelete, "/api/issues/"+trashed.ID, nil, http.StatusNoContent)
	requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/labels/merge", map[string]any{"toLabelId": to.ID, "fromLabelIds": []string{from.ID}}, http.StatusOK)
	requestJSON[any](t, handler, http.MethodDelete, "/api/labels/"+gone.ID, nil, http.StatusNoContent)
	for _, entry := range labelMoveMetadata(t, repository, before.Workspace.URLKey).Trash {
		if entry.ResourceID == trashed.ID {
			requestJSON[any](t, handler, http.MethodPost, "/api/trash/"+entry.ID+"/restore", nil, http.StatusOK)
		}
	}
	restored := findIssue(t, requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK).Issues, trashed.ID)
	if !slices.Equal(movedIssueLabelIDs(restored), []string{to.ID}) {
		t.Fatalf("restored issue labels %v, want the merge target only", movedIssueLabelIDs(restored))
	}
}
