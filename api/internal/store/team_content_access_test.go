package store

import (
	"slices"
	"testing"

	"flow/api/internal/domain"
)

// sameTeamScope projects team rows and content alike (non-admin viewers).
func sameTeamScope(teams map[string]bool) bootstrapTeamScope {
	return bootstrapTeamScope{Teams: teams, Content: teams}
}

func privateTeamFixture(viewerRole string, viewerJoined bool) domain.Bootstrap {
	data := domain.Bootstrap{
		Viewer:     domain.User{ID: "viewer"},
		ViewerRole: viewerRole,
		Teams:      []domain.Team{{ID: "public"}, {ID: "private", Private: true}, {ID: "child"}},
		TeamSettings: map[string]domain.TeamSettings{
			"public":  {TeamID: "public", Access: "public"},
			"private": {TeamID: "private", Access: "private"},
			"child":   {TeamID: "child", Access: "public", ParentTeamID: "private"},
		},
		TeamMembers: []domain.TeamMember{{TeamID: "private", UserID: "owner", Role: "owner"}, {TeamID: "child", UserID: "owner", Role: "owner"}},
		Issues: []domain.Issue{
			{ID: "public-issue", Team: domain.Team{ID: "public"}},
			{ID: "private-issue", Team: domain.Team{ID: "private"}},
			{ID: "child-issue", Team: domain.Team{ID: "child"}},
			{ID: "shared-issue", Team: domain.Team{ID: "private"}, Permissions: []domain.IssuePermission{{SubjectType: "user", SubjectID: "viewer", Role: "viewer"}}},
		},
		Documents: []domain.Document{
			{ID: "public-doc", TeamIDs: []string{"public"}},
			{ID: "private-doc", TeamIDs: []string{"private"}},
			{ID: "workspace-doc"},
		},
		Projects: []domain.Project{
			{ID: "public-project", TeamIDs: []string{"public"}},
			{ID: "private-project", TeamIDs: []string{"private"}},
			{ID: "shared-project", TeamIDs: []string{"public", "private"}},
		},
		Cycles:     []domain.Cycle{{ID: "private-cycle", TeamID: "private"}},
		Labels:     []domain.IssueLabel{{ID: "private-label", Scope: "private"}},
		SavedViews: []domain.SavedView{{ID: "private-view", Scope: "team", TeamID: "private"}},
	}
	if viewerJoined {
		data.TeamMembers = append(data.TeamMembers, domain.TeamMember{TeamID: "private", UserID: "viewer", Role: "member"})
	}
	return data
}

func projectForViewer(data domain.Bootstrap) domain.Bootstrap {
	allowed := map[string]bool{}
	for _, team := range data.Teams {
		if teamVisibleToUser(data, team.ID, "viewer", data.ViewerRole) {
			allowed[team.ID] = true
		}
	}
	filterBootstrapTeams(&data, viewerTeamScope(data, allowed, "viewer", data.ViewerRole), data.ViewerRole == "guest")
	return data
}

func ids[T any](items []T, id func(T) string) []string {
	result := []string{}
	for _, item := range items {
		result = append(result, id(item))
	}
	slices.Sort(result)
	return result
}

func TestAdminSeesPrivateTeamRowsButNotTheirContent(t *testing.T) {
	data := projectForViewer(privateTeamFixture("admin", false))
	// The team row and its settings stay, so the admin can manage the team.
	if got := ids(data.Teams, func(team domain.Team) string { return team.ID }); !slices.Equal(got, []string{"child", "private", "public"}) {
		t.Fatalf("teams = %v, want every team row", got)
	}
	if got := ids(data.Labels, func(label domain.IssueLabel) string { return label.ID }); !slices.Equal(got, []string{"private-label"}) {
		t.Fatalf("labels = %v, want the private team's labels for its settings", got)
	}
	// Its content does not, including a public sub-team inside the private boundary.
	if got := ids(data.Issues, func(issue domain.Issue) string { return issue.ID }); !slices.Equal(got, []string{"public-issue", "shared-issue"}) {
		t.Fatalf("issues = %v, want public and explicitly shared issues only", got)
	}
	if got := ids(data.Documents, func(document domain.Document) string { return document.ID }); !slices.Equal(got, []string{"public-doc", "workspace-doc"}) {
		t.Fatalf("documents = %v", got)
	}
	if got := ids(data.Projects, func(project domain.Project) string { return project.ID }); !slices.Equal(got, []string{"public-project", "shared-project"}) {
		t.Fatalf("projects = %v", got)
	}
	if shared := data.Projects[slices.IndexFunc(data.Projects, func(project domain.Project) bool { return project.ID == "shared-project" })]; !slices.Equal(shared.TeamIDs, []string{"public"}) {
		t.Fatalf("shared project teams = %v, want the private team redacted", shared.TeamIDs)
	}
	if len(data.Cycles) != 0 || len(data.SavedViews) != 0 {
		t.Fatalf("cycles = %v views = %v, want none from the private team", data.Cycles, data.SavedViews)
	}
}

func TestAdminMemberReadsPrivateTeamContent(t *testing.T) {
	data := projectForViewer(privateTeamFixture("owner", true))
	if got := ids(data.Issues, func(issue domain.Issue) string { return issue.ID }); !slices.Equal(got, []string{"child-issue", "private-issue", "public-issue", "shared-issue"}) {
		t.Fatalf("issues = %v, want every issue", got)
	}
	if got := ids(data.Documents, func(document domain.Document) string { return document.ID }); !slices.Equal(got, []string{"private-doc", "public-doc", "workspace-doc"}) {
		t.Fatalf("documents = %v", got)
	}
	if len(data.Cycles) != 1 || len(data.SavedViews) != 1 {
		t.Fatalf("cycles = %v views = %v", data.Cycles, data.SavedViews)
	}
}

func TestMemberOutsidePrivateTeamSeesNeitherRowNorContent(t *testing.T) {
	data := projectForViewer(privateTeamFixture("member", false))
	if got := ids(data.Teams, func(team domain.Team) string { return team.ID }); !slices.Equal(got, []string{"public"}) {
		t.Fatalf("teams = %v", got)
	}
	if got := ids(data.Issues, func(issue domain.Issue) string { return issue.ID }); !slices.Equal(got, []string{"public-issue", "shared-issue"}) {
		t.Fatalf("issues = %v", got)
	}
	joined := projectForViewer(privateTeamFixture("member", true))
	if got := ids(joined.Teams, func(team domain.Team) string { return team.ID }); !slices.Equal(got, []string{"child", "private", "public"}) {
		t.Fatalf("member teams = %v", got)
	}
}

func TestTeamContentVisibleSplitsAdminRowsFromContent(t *testing.T) {
	for _, tc := range []struct {
		role   string
		joined bool
		team   string
		want   bool
	}{
		{"admin", false, "public", true},
		{"admin", false, "private", false},
		{"admin", false, "child", false},
		{"admin", true, "private", true},
		{"admin", true, "child", true},
		{"owner", false, "private", false},
		{"member", false, "private", false},
		{"member", true, "private", true},
		{"guest", false, "public", false},
	} {
		if got := TeamContentVisible(privateTeamFixture(tc.role, tc.joined), tc.team, "viewer", tc.role); got != tc.want {
			t.Errorf("role=%s joined=%v team=%s: got %v want %v", tc.role, tc.joined, tc.team, got, tc.want)
		}
	}
}
