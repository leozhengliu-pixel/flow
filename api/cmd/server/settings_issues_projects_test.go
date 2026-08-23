package main

import (
	"net/http"
	"path/filepath"
	"slices"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestSettingsIssueAndProjectTemplateRoundTrips(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	team := bootstrap.Teams[0]

	issueTemplate := requestJSON[domain.IssueTemplate](t, handler, http.MethodPost, "/api/issue-templates", map[string]any{
		"name": "Bug report", "title": "Investigate reported bug", "body": "Steps to reproduce", "teamId": team.ID,
		"icon": "Page", "color": "#bec2c8", "templateType": "customForm",
		"formFields": []map[string]any{{"id": "field_text", "label": "Reproduction steps", "type": "longText", "required": true}, {"id": "field_due", "label": "", "type": "dueDate", "required": false}},
		"subIssues":  []map[string]any{{"id": "template_child", "title": "Verify regression", "description": "Run regression coverage", "teamId": team.ID, "priority": 2, "labelIds": []string{}}},
	}, http.StatusCreated)
	issueTemplate = requestJSON[domain.IssueTemplate](t, handler, http.MethodPatch, "/api/issue-templates/"+issueTemplate.ID, map[string]any{
		"title": "Triage reported bug",
	}, http.StatusOK)
	if issueTemplate.Title != "Triage reported bug" || issueTemplate.Icon != "Page" || len(issueTemplate.FormFields) != 2 || len(issueTemplate.SubIssues) != 1 {
		t.Fatalf("issue template title was not updated: %#v", issueTemplate)
	}
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{
		"teamId": team.ID, "templateId": issueTemplate.ID,
	}, http.StatusCreated)
	if issue.Title != issueTemplate.Title || issue.Description != issueTemplate.Body || len(issue.SubIssueIDs) != 1 {
		t.Fatalf("issue template defaults were not applied: %#v", issue)
	}
	bootstrapAfterIssue := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if !slices.ContainsFunc(bootstrapAfterIssue.Issues, func(item domain.Issue) bool {
		return item.ParentID != nil && *item.ParentID == issue.ID && item.Title == "Verify regression"
	}) {
		t.Fatalf("template sub-issue was not instantiated: %#v", bootstrapAfterIssue.Issues[:min(4, len(bootstrapAfterIssue.Issues))])
	}

	projectTemplate := requestJSON[domain.ProjectTemplate](t, handler, http.MethodPost, "/api/project-templates", map[string]any{
		"name": "Launch template", "projectName": "Product launch", "templateDescription": "Reusable launch configuration", "teamIds": []string{team.ID},
		"visibilityTeamId": team.ID, "milestones": []map[string]any{{"name": "Beta", "description": "Beta exit criteria"}},
	}, http.StatusCreated)
	projectTemplate = requestJSON[domain.ProjectTemplate](t, handler, http.MethodPatch, "/api/project-templates/"+projectTemplate.ID, map[string]any{
		"projectName": "Regional product launch", "templateDescription": "Regional reusable launch configuration",
		"milestones": []map[string]any{{"name": "Beta", "description": "Updated criteria"}, {"name": "GA", "description": "Launch checklist"}},
	}, http.StatusOK)
	if projectTemplate.ProjectName != "Regional product launch" || projectTemplate.TemplateDescription != "Regional reusable launch configuration" || projectTemplate.VisibilityTeamID != team.ID || projectTemplate.Visibility != "teams" || len(projectTemplate.Milestones) != 2 || projectTemplate.Milestones[0].ID == "" {
		t.Fatalf("project template fields were not updated: %#v", projectTemplate)
	}
	project := requestJSON[domain.Project](t, handler, http.MethodPost, "/api/projects", map[string]any{
		"templateId": projectTemplate.ID,
	}, http.StatusCreated)
	if project.Name != projectTemplate.ProjectName || len(project.Milestones) != 2 || project.Milestones[1].Name != "GA" || project.Milestones[1].ProjectID != project.ID {
		t.Fatalf("project template name and milestones were not instantiated: %#v", project)
	}

	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if !slices.ContainsFunc(bootstrap.IssueTemplates, func(item domain.IssueTemplate) bool {
		return item.ID == issueTemplate.ID && item.Title == issueTemplate.Title
	}) ||
		!slices.ContainsFunc(bootstrap.ProjectTemplates, func(item domain.ProjectTemplate) bool {
			return item.ID == projectTemplate.ID && item.ProjectName == projectTemplate.ProjectName && len(item.Milestones) == 2
		}) {
		t.Fatal("template additions did not survive bootstrap")
	}
}

func TestProjectStatusDescriptionAndSLASettingsRoundTrip(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})

	status := requestJSON[domain.ProjectStatus](t, handler, http.MethodPost, "/api/project-statuses", map[string]any{
		"name": "Validating", "description": "Confirm launch readiness", "type": "started", "color": "#5e6ad2",
	}, http.StatusCreated)
	status = requestJSON[domain.ProjectStatus](t, handler, http.MethodPatch, "/api/project-statuses/"+status.ID, map[string]any{
		"description": "Confirm launch and rollback readiness",
	}, http.StatusOK)
	if status.Description != "Confirm launch and rollback readiness" {
		t.Fatalf("project status description was not updated: %#v", status)
	}
	requestJSON[map[string]any](t, handler, http.MethodPut, "/api/sla-settings", map[string]any{"enabled": true}, http.StatusOK)

	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if !slices.ContainsFunc(bootstrap.ProjectStatuses, func(item domain.ProjectStatus) bool {
		return item.ID == status.ID && item.Description == status.Description
	}) {
		t.Fatal("project status description did not survive bootstrap")
	}
	sla, ok := bootstrap.Settings["sla"].(map[string]any)
	if !ok || sla["enabled"] != true {
		t.Fatalf("SLA settings did not survive bootstrap: %#v", bootstrap.Settings["sla"])
	}
}

func TestProjectStatusesMatchLinearEditDeleteAndReorderRules(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	canonical := bootstrap.ProjectStatuses[0]
	updated := requestJSON[domain.ProjectStatus](t, handler, http.MethodPatch, "/api/project-statuses/"+canonical.ID, map[string]any{"name": "Changed"}, http.StatusOK)
	if updated.Name != "Changed" {
		t.Fatalf("canonical project status was not editable: %#v", updated)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/project-statuses/"+canonical.ID, nil, http.StatusBadRequest)
	started := slices.IndexFunc(bootstrap.ProjectStatuses, func(status domain.ProjectStatus) bool { return status.ID == "ps_progress" })
	if started < 0 {
		t.Fatalf("default in-progress status must remain editable: %#v", bootstrap.ProjectStatuses)
	}

	custom := requestJSON[domain.ProjectStatus](t, handler, http.MethodPost, "/api/project-statuses", map[string]any{
		"name": "Queued", "type": canonical.Type,
	}, http.StatusCreated)
	ids := make([]string, 0, len(bootstrap.ProjectStatuses)+1)
	ids = append(ids, custom.ID)
	for _, status := range bootstrap.ProjectStatuses {
		ids = append(ids, status.ID)
	}
	reordered := requestJSON[[]domain.ProjectStatus](t, handler, http.MethodPost, "/api/project-statuses/reorder", map[string]any{"ids": ids}, http.StatusOK)
	if reordered[0].ID != custom.ID {
		t.Fatalf("project statuses were not reordered within the type: %#v", reordered)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/project-statuses/"+custom.ID, nil, http.StatusNoContent)
}
