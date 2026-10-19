package main

import (
	"context"
	"net/http/httptest"
	"slices"
	"testing"

	"flow/api/internal/domain"
)

// A team-restricted application token must still see workspace label groups
// (whatever the stored casing of "workspace") and the groupId on their labels,
// while groups and labels of teams outside the allow-list stay hidden.
func TestScopedApplicationKeepsWorkspaceLabelGroups(t *testing.T) {
	data := domain.Bootstrap{
		Teams: []domain.Team{{ID: "team-allowed"}, {ID: "team-other"}},
		LabelGroups: []domain.LabelGroup{
			{ID: "group-upper", Name: "平台", Scope: "Workspace", ResourceType: "project"},
			{ID: "group-lower", Name: "小微", Scope: "workspace", ResourceType: "project"},
			{ID: "group-spaced", Name: "Spaced", Scope: "  WORKSPACE ", ResourceType: "project"},
			{ID: "group-empty", Name: "Empty", ResourceType: "project"},
			{ID: "group-allowed", Name: "Allowed team", Scope: "team-allowed", ResourceType: "issue"},
			{ID: "group-other", Name: "Other team", Scope: "team-other", ResourceType: "issue"},
		},
		Labels: []domain.IssueLabel{
			{ID: "label-upper", Name: "集团IT平台", Scope: "Workspace", GroupID: "group-upper"},
			{ID: "label-lower", Name: "小微A", Scope: "workspace", GroupID: "group-lower"},
			{ID: "label-spaced", Name: "Spaced", Scope: "  WORKSPACE ", GroupID: "group-spaced"},
			{ID: "label-empty", Name: "Empty", GroupID: "group-empty"},
			{ID: "label-allowed", Name: "Allowed", Scope: "team-allowed", GroupID: "group-allowed"},
			{ID: "label-workspace-in-other-group", Name: "Workspace in hidden group", Scope: "workspace", GroupID: "group-other"},
			{ID: "label-other", Name: "Other", Scope: "team-other", GroupID: "group-other"},
		},
	}
	key := domain.APIKey{ID: "key", Actor: "app", TeamRestriction: "selected", TeamIDs: []string{"team-allowed"}}
	request := httptest.NewRequest("GET", "/api/issue-records/bootstrap", nil)
	request = request.WithContext(context.WithValue(request.Context(), apiKeyContextKey{}, key))

	filterBootstrapForAPIKey(&data, request)

	groupIDs := make([]string, 0, len(data.LabelGroups))
	for _, group := range data.LabelGroups {
		groupIDs = append(groupIDs, group.ID)
	}
	wantGroups := []string{"group-upper", "group-lower", "group-spaced", "group-empty", "group-allowed"}
	if !slices.Equal(groupIDs, wantGroups) {
		t.Fatalf("label groups = %v, want %v", groupIDs, wantGroups)
	}

	labels := map[string]domain.IssueLabel{}
	for _, label := range data.Labels {
		labels[label.ID] = label
	}
	if _, ok := labels["label-other"]; ok {
		t.Fatal("label of an unauthorized team leaked")
	}
	for id, wantGroup := range map[string]string{
		"label-upper":   "group-upper",
		"label-lower":   "group-lower",
		"label-spaced":  "group-spaced",
		"label-empty":   "group-empty",
		"label-allowed": "group-allowed",
		// The label itself is workspace-wide, but its group belongs to a
		// team the key cannot read, so only the group reference is dropped.
		"label-workspace-in-other-group": "",
	} {
		label, ok := labels[id]
		if !ok {
			t.Fatalf("label %s was filtered out", id)
		}
		if label.GroupID != wantGroup {
			t.Fatalf("label %s groupId = %q, want %q", id, label.GroupID, wantGroup)
		}
	}
}

// Releases follow their pipeline's team access for team-restricted tokens:
// an empty release in a workspace or allowed-team pipeline is visible, a
// release in a hidden pipeline is not, and links to hidden projects or
// issues never leak.
func TestScopedApplicationKeepsPipelineReleases(t *testing.T) {
	data := domain.Bootstrap{
		Teams: []domain.Team{{ID: "team-allowed"}, {ID: "team-other"}},
		Projects: []domain.Project{
			{ID: "project-allowed", TeamIDs: []string{"team-allowed"}},
			{ID: "project-other", TeamIDs: []string{"team-other"}},
		},
		Issues: []domain.Issue{
			{ID: "issue-allowed", Team: domain.Team{ID: "team-allowed"}},
			{ID: "issue-other", Team: domain.Team{ID: "team-other"}},
		},
		ReleasePipelines: []domain.ReleasePipeline{
			{ID: "pipeline-workspace"},
			{ID: "pipeline-allowed", TeamIDs: []string{"team-allowed"}},
			{ID: "pipeline-shared", TeamIDs: []string{"team-allowed", "team-other"}},
			{ID: "pipeline-other", TeamIDs: []string{"team-other"}},
		},
		Releases: []domain.Release{
			{ID: "release-workspace-empty", PipelineID: "pipeline-workspace", ProjectIDs: []string{}, IssueIDs: []string{}},
			{ID: "release-allowed-empty", PipelineID: "pipeline-allowed", ProjectIDs: []string{}, IssueIDs: []string{}},
			{ID: "release-shared-mixed", PipelineID: "pipeline-shared", ProjectIDs: []string{"project-allowed", "project-other"}, IssueIDs: []string{"issue-other", "issue-allowed"}},
			{ID: "release-workspace-hidden-links", PipelineID: "pipeline-workspace", ProjectIDs: []string{"project-other"}, IssueIDs: []string{"issue-other"}},
			{ID: "release-other-empty", PipelineID: "pipeline-other", ProjectIDs: []string{}, IssueIDs: []string{}},
			{ID: "release-other-linked", PipelineID: "pipeline-other", ProjectIDs: []string{"project-allowed"}, IssueIDs: []string{"issue-allowed"}},
			{ID: "release-unpiped-hidden", ProjectIDs: []string{"project-other"}, IssueIDs: []string{}},
			{ID: "release-unpiped-visible", ProjectIDs: []string{"project-allowed", "project-other"}, IssueIDs: []string{}},
		},
	}
	key := domain.APIKey{ID: "key", Actor: "app", Scopes: []string{"read", "release:read"}, TeamRestriction: "selected", TeamIDs: []string{"team-allowed"}}
	request := httptest.NewRequest("GET", "/api/releases", nil)
	request = request.WithContext(context.WithValue(request.Context(), apiKeyContextKey{}, key))

	filterBootstrapForAPIKey(&data, request)

	pipelineIDs := []string{}
	for _, pipeline := range data.ReleasePipelines {
		pipelineIDs = append(pipelineIDs, pipeline.ID)
		if slices.Contains(pipeline.TeamIDs, "team-other") {
			t.Fatalf("pipeline %s leaks unauthorized team id: %v", pipeline.ID, pipeline.TeamIDs)
		}
	}
	if want := []string{"pipeline-workspace", "pipeline-allowed", "pipeline-shared"}; !slices.Equal(pipelineIDs, want) {
		t.Fatalf("pipelines = %v, want %v", pipelineIDs, want)
	}

	type links struct{ projects, issues []string }
	got := map[string]links{}
	for _, release := range data.Releases {
		got[release.ID] = links{release.ProjectIDs, release.IssueIDs}
	}
	want := map[string]links{
		"release-workspace-empty":        {[]string{}, []string{}},
		"release-allowed-empty":          {[]string{}, []string{}},
		"release-shared-mixed":           {[]string{"project-allowed"}, []string{"issue-allowed"}},
		"release-workspace-hidden-links": {[]string{}, []string{}},
		"release-unpiped-visible":        {[]string{"project-allowed"}, []string{}},
	}
	if len(got) != len(want) {
		t.Fatalf("releases = %v, want ids of %v", got, want)
	}
	for id, expected := range want {
		actual, ok := got[id]
		if !ok {
			t.Fatalf("release %s was filtered out", id)
		}
		if !slices.Equal(actual.projects, expected.projects) || !slices.Equal(actual.issues, expected.issues) {
			t.Fatalf("release %s links = %+v, want %+v", id, actual, expected)
		}
	}
}
