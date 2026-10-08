package main

import (
	"net/http"
	"path/filepath"
	"slices"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestMergeLabelsRepointsResourcesAndDeletesSources(t *testing.T) {
	for _, forceFull := range []bool{false, true} {
		name := "scoped"
		if forceFull {
			name = "full"
		}
		t.Run(name, func(t *testing.T) {
			restore := store.ForceFullMutationsForTesting(forceFull)
			defer restore()
			repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer repository.Close()
			handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})

			target := requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/labels", map[string]any{"name": "Bug", "resourceType": "issue"}, http.StatusCreated)
			source := requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/labels", map[string]any{"name": "Defect", "resourceType": "issue"}, http.StatusCreated)
			other := requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/labels", map[string]any{"name": "Regression", "resourceType": "issue"}, http.StatusCreated)
			both := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Both labels", "labelIds": []string{target.ID, source.ID, other.ID}}, http.StatusCreated)
			onlySource := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Source only", "labelIds": []string{source.ID}}, http.StatusCreated)
			rule := requestJSON[domain.SLARule](t, handler, http.MethodPost, "/api/sla-rules", map[string]any{"name": "Defects", "targetMinutes": 60, "filters": map[string]any{"label": source.ID}}, http.StatusCreated)

			projectTarget := requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/labels", map[string]any{"name": "Platform", "resourceType": "project"}, http.StatusCreated)
			projectSource := requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/labels", map[string]any{"name": "Infra", "resourceType": "project"}, http.StatusCreated)
			project := requestJSON[domain.Project](t, handler, http.MethodPost, "/api/projects", map[string]any{"name": "Merge project", "labelIds": []string{projectSource.ID}}, http.StatusCreated)

			merged := requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/labels/merge", map[string]any{"toLabelId": target.ID, "fromLabelIds": []string{source.ID}}, http.StatusOK)
			if merged.ID != target.ID || merged.Name != "Bug" {
				t.Fatalf("merge returned %#v", merged)
			}
			requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/labels/merge", map[string]any{"toLabelId": projectTarget.ID, "fromLabelIds": []string{projectSource.ID}}, http.StatusOK)

			after := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
			if slices.ContainsFunc(after.Labels, func(label domain.IssueLabel) bool { return label.ID == source.ID || label.ID == projectSource.ID }) {
				t.Fatal("merged labels still exist")
			}
			ids := func(issue domain.Issue) []string {
				result := []string{}
				for _, label := range issue.Labels {
					result = append(result, label.ID)
				}
				return result
			}
			if got := ids(findIssue(t, after.Issues, both.ID)); !slices.Equal(got, []string{target.ID, other.ID}) {
				t.Fatalf("issue with both labels = %v, want target once and the other label kept", got)
			}
			if got := ids(findIssue(t, after.Issues, onlySource.ID)); !slices.Equal(got, []string{target.ID}) {
				t.Fatalf("issue with the merged label = %v, want the target", got)
			}
			slaIndex := slices.IndexFunc(after.SLARules, func(item domain.SLARule) bool { return item.ID == rule.ID })
			if slaIndex < 0 || after.SLARules[slaIndex].Filters["label"] != target.ID {
				t.Fatalf("SLA rule was not re-pointed: %#v", after.SLARules)
			}
			projectIndex := slices.IndexFunc(after.Projects, func(item domain.Project) bool { return item.ID == project.ID })
			if projectIndex < 0 || !slices.Equal(after.Projects[projectIndex].LabelIDs, []string{projectTarget.ID}) {
				t.Fatalf("project labels were not re-pointed: %#v", after.Projects[projectIndex].LabelIDs)
			}
		})
	}
}

func TestMergeLabelsRejectsIncompatibleLabels(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	issueLabel := requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/labels", map[string]any{"name": "Issue label", "resourceType": "issue"}, http.StatusCreated)
	projectLabel := requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/labels", map[string]any{"name": "Project label", "resourceType": "project"}, http.StatusCreated)
	group := requestJSON[domain.LabelGroup](t, handler, http.MethodPost, "/api/label-groups", map[string]any{"name": "Type", "resourceType": "issue"}, http.StatusCreated)
	grouped := requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/labels", map[string]any{"name": "Grouped", "resourceType": "issue", "groupId": group.ID}, http.StatusCreated)

	requestJSON[any](t, handler, http.MethodPost, "/api/labels/merge", map[string]any{"toLabelId": issueLabel.ID, "fromLabelIds": []string{projectLabel.ID}}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodPost, "/api/labels/merge", map[string]any{"toLabelId": issueLabel.ID, "fromLabelIds": []string{grouped.ID}}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodPost, "/api/labels/merge", map[string]any{"toLabelId": issueLabel.ID, "fromLabelIds": []string{}}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodPost, "/api/labels/merge", map[string]any{"toLabelId": issueLabel.ID, "fromLabelIds": []string{"label_missing"}}, http.StatusNotFound)

	after := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	for _, id := range []string{issueLabel.ID, projectLabel.ID, grouped.ID} {
		if !slices.ContainsFunc(after.Labels, func(label domain.IssueLabel) bool { return label.ID == id }) {
			t.Fatalf("rejected merge deleted label %s", id)
		}
	}
}

func TestDeleteTeamLabelGroupRemovesItsLabels(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	before := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	teamID := before.Teams[0].ID
	group := requestJSON[domain.LabelGroup](t, handler, http.MethodPost, "/api/label-groups", map[string]any{"name": "Team type", "resourceType": "issue", "scope": teamID}, http.StatusCreated)
	label := requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/teams/"+teamID+"/labels", map[string]any{"name": "Team child", "color": "#123456", "groupId": group.ID}, http.StatusCreated)

	// The workspace route keeps team scope; the team route deletes it.
	requestJSON[any](t, handler, http.MethodDelete, "/api/label-groups/"+group.ID, nil, http.StatusNotFound)
	requestJSON[any](t, handler, http.MethodDelete, "/api/teams/other-team/label-groups/"+group.ID, nil, http.StatusNotFound)
	requestJSON[any](t, handler, http.MethodDelete, "/api/teams/"+teamID+"/label-groups/"+group.ID, nil, http.StatusNoContent)

	after := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if slices.ContainsFunc(after.LabelGroups, func(item domain.LabelGroup) bool { return item.ID == group.ID }) {
		t.Fatal("team label group was not deleted")
	}
	if slices.ContainsFunc(after.Labels, func(item domain.IssueLabel) bool { return item.ID == label.ID }) {
		t.Fatal("team label group's labels were not deleted")
	}
}
