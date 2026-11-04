package store

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestTeamFilterValidation(t *testing.T) {
	for _, field := range []string{"team", "teamId", "TEAMID"} {
		for _, op := range []string{"", "is", "isNot", "in", "notIn", "NOTIN"} {
			if err := ValidateIssueFilter(IssueFilter{Field: field, Operator: op, Values: []string{"a", "b"}}); err != nil {
				t.Fatalf("%s %s: %v", field, op, err)
			}
		}
	}
	for _, filter := range []IssueFilter{
		{Field: "team"}, {Field: "team", Values: []string{""}}, {Field: "teamId", Values: []string{"a", " "}},
		{Field: "team", Operator: "contains", Values: []string{"a"}}, {Field: "team", Operator: "isEmpty"},
		{Field: "team", Operator: "includesAll", Values: []string{"a"}}, {Field: "team", Values: make([]string, 1001)},
	} {
		if err := ValidateIssueFilter(IssueFilter{Not: &filter}); !errors.Is(err, ErrIssueQuery) {
			t.Fatalf("accepted invalid filter %+v: %v", filter, err)
		}
	}
}

func TestTeamFilterQueries(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "teams.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	testTeamFilterQueries(t, repo, repo.Bootstrap())
}

func testTeamFilterQueries(t *testing.T, repo *SQLiteStore, data domain.Bootstrap) {
	t.Helper()
	ctx := context.Background()
	parent, other := data.Teams[0], domain.Team{ID: "team-filter-unrelated", Key: "TFO", Name: "Unrelated"}
	child := domain.Team{ID: "team-filter-child", Key: "TFC", Name: "Child"}
	if err := repo.MutateWorkspace(ctx, data.Workspace.URLKey, "team.created", child.ID, nil, func(next *domain.Bootstrap) error {
		next.Teams = append(next.Teams, child, other)
		next.TeamSettings[child.ID] = domain.TeamSettings{TeamID: child.ID, ParentTeamID: parent.ID}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	issues := []domain.Issue{}
	for i, team := range []domain.Team{parent, child, other} {
		issue := domain.Issue{State: data.States[0], Creator: data.Viewer, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		issue.ID, issue.Identifier, issue.Title, issue.Team = []string{"team-filter-parent", "team-filter-child", "team-filter-other"}[i], []string{"TFA-1", "TFC-1", "TFO-1"}[i], "TeamFilterNeedle", team
		issues = append(issues, issue)
	}
	if err := repo.ImportIssues(ctx, data.Workspace.URLKey, issues); err != nil {
		t.Fatal(err)
	}
	if err := repo.MutateWorkspace(ctx, data.Workspace.URLKey, "project.created", "team-filter-projects", nil, func(next *domain.Bootstrap) error {
		for _, issue := range issues {
			next.Projects = append(next.Projects, domain.Project{ID: issue.ID + "-project", Name: "TeamFilterNeedle", TeamIDs: []string{issue.Team.ID}})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	policy, _ := repo.WorkspaceMetadata(data.Workspace.URLKey)
	for _, test := range []struct {
		name   string
		filter IssueFilter
		want   []string
	}{
		{"is exact parent", IssueFilter{Field: "team", Values: []string{parent.ID}}, []string{issues[0].ID}},
		{"is not", IssueFilter{Field: "teamId", Operator: "isNot", Values: []string{parent.ID}}, []string{issues[1].ID, issues[2].ID}},
		{"any of", IssueFilter{Field: "team", Operator: "in", Values: []string{parent.ID, child.ID}}, []string{issues[1].ID, issues[0].ID}},
		{"none of", IssueFilter{Field: "team", Operator: "notIn", Values: []string{parent.ID, child.ID}}, []string{issues[2].ID}},
		{"unknown", IssueFilter{Field: "team", Values: []string{"unknown"}}, []string{}},
		{"compound", IssueFilter{Or: []IssueFilter{{Field: "team", Values: []string{parent.ID}}, {Not: &IssueFilter{Field: "team", Operator: "in", Values: []string{parent.ID, child.ID}}}}}, []string{issues[2].ID, issues[0].ID}},
	} {
		t.Run(test.name, func(t *testing.T) {
			q := IssueRecordQuery{Workspace: data.Workspace.URLKey, SearchText: "TeamFilterNeedle", Filter: test.filter, IncludeTotal: true}
			page, err := repo.QueryIssueRecords(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			got := []string{}
			for _, issue := range page.Items {
				got = append(got, issue.ID)
			}
			slices.Sort(got)
			slices.Sort(test.want)
			if !reflect.DeepEqual(got, test.want) || page.Total != int64(len(test.want)) {
				t.Fatalf("got %v (%d), want %v", got, page.Total, test.want)
			}
			metadata, err := repo.SearchMetadata(ctx, policy, SearchMetadataQuery{Scope: q, Types: map[string]bool{"project": true}, Terms: []string{"TeamFilterNeedle"}})
			if err != nil {
				t.Fatal(err)
			}
			projectIDs, wantProjects := []string{}, []string{}
			for _, project := range metadata.Projects {
				projectIDs = append(projectIDs, project.ID)
			}
			for _, id := range test.want {
				wantProjects = append(wantProjects, id+"-project")
			}
			slices.Sort(projectIDs)
			slices.Sort(wantProjects)
			if !slices.Equal(projectIDs, wantProjects) {
				t.Fatalf("projects: got %v want %v", projectIDs, wantProjects)
			}
			q.AllowedTeamIDs = []string{parent.ID}
			page, err = repo.QueryIssueRecords(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			for _, issue := range page.Items {
				if issue.Team.ID != parent.ID {
					t.Fatal("team restriction broadened")
				}
			}
		})
	}
}

func TestTeamMetadataFilterDialects(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			repo := &SQLiteStore{dialect: dialect}
			for _, kind := range []string{"project", "document", "view"} {
				clause, args, err := repo.searchMetadataFilter(kind, IssueFilter{Field: "teamId", Operator: "notIn", Values: []string{"a", "b"}}, 0)
				if err != nil || !strings.Contains(clause, "NOT (") || !reflect.DeepEqual(args, []any{"a", "b"}) {
					t.Fatalf("%s: %s %v %v", kind, clause, args, err)
				}
			}
			clause, _, err := repo.searchMetadataFilter("member", IssueFilter{Field: "team", Operator: "isNot", Values: []string{"a"}}, 0)
			if err != nil || clause != "0=1" {
				t.Fatalf("unrelated type matched: %s %v", clause, err)
			}
		})
	}
}
