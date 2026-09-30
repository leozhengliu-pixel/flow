package main

import (
	"net/http"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func newResourceGapsHandler(t *testing.T) http.Handler {
	t.Helper()
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	return newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
}

func TestResourceLinkNameMatchesWebClient(t *testing.T) {
	for input, want := range map[string]string{
		"https://github.com/flow/app/pull/1":  "GitHub",
		"https://www.example.com/path":        "Example",
		"https://acme.atlassian.net/browse/X": "Jira",
		"https://docs.google.com/document/d":  "Google Docs",
		"https://sub.vercel.com":              "Vercel",
		"not a url":                           "not a url",
	} {
		if got := resourceLinkName(input); got != want {
			t.Errorf("resourceLinkName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestDocumentReminderCreatesSnoozedInboxNotification(t *testing.T) {
	handler := newResourceGapsHandler(t)
	document := requestJSON[domain.Document](t, handler, http.MethodPost, "/api/documents", map[string]any{"title": "Reminder doc"}, http.StatusCreated)
	remindAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	reminder := requestJSON[domain.Notification](t, handler, http.MethodPost, "/api/documents/"+document.ID+"/reminders", map[string]any{"remindAt": remindAt.Format(time.RFC3339)}, http.StatusCreated)
	if reminder.Type != "documentReminder" || reminder.SourceType != "document" || reminder.SourceID != document.ID || reminder.SnoozedUntil == nil || !reminder.SnoozedUntil.Equal(remindAt) || reminder.Category != "reminders" {
		t.Fatalf("document reminder = %#v", reminder)
	}
	requestJSON[any](t, handler, http.MethodPost, "/api/documents/"+document.ID+"/reminders", map[string]any{"remindAt": time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodPost, "/api/documents/missing/reminders", map[string]any{"remindAt": remindAt.Format(time.RFC3339)}, http.StatusNotFound)
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if !slices.ContainsFunc(bootstrap.Notifications, func(item domain.Notification) bool { return item.ID == reminder.ID }) {
		t.Fatal("document reminder was not persisted to the inbox")
	}
}

func TestWorkspaceScopedDocumentTemplate(t *testing.T) {
	handler := newResourceGapsHandler(t)
	template := requestJSON[domain.DocumentTemplate](t, handler, http.MethodPost, "/api/document-templates", map[string]any{"name": "Workspace record", "title": "Record"}, http.StatusCreated)
	if template.ID == "" || template.TeamID != "" {
		t.Fatalf("workspace template = %#v", template)
	}
	requestJSON[any](t, handler, http.MethodPost, "/api/document-templates", map[string]any{"name": "Bad", "teamId": "missing-team"}, http.StatusBadRequest)
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if !slices.ContainsFunc(bootstrap.DocumentTemplates, func(item domain.DocumentTemplate) bool { return item.ID == template.ID }) {
		t.Fatal("workspace template missing from bootstrap")
	}
	document := requestJSON[domain.Document](t, handler, http.MethodPost, "/api/documents", map[string]any{"templateId": template.ID}, http.StatusCreated)
	if document.Title != "Record" || len(document.TeamIDs) != 0 {
		t.Fatalf("workspace template document = %#v", document)
	}
	// A team template can be moved to the workspace scope and back.
	team := bootstrap.Teams[0]
	template = requestJSON[domain.DocumentTemplate](t, handler, http.MethodPatch, "/api/document-templates/"+template.ID, map[string]any{"teamId": team.ID}, http.StatusOK)
	if template.TeamID != team.ID {
		t.Fatalf("template team = %q", template.TeamID)
	}
	template = requestJSON[domain.DocumentTemplate](t, handler, http.MethodPatch, "/api/document-templates/"+template.ID, map[string]any{"teamId": ""}, http.StatusOK)
	if template.TeamID != "" {
		t.Fatalf("template was not moved to the workspace: %q", template.TeamID)
	}
}

func TestUntitledLinkResourcesUseSiteName(t *testing.T) {
	handler := newResourceGapsHandler(t)
	project := requestJSON[domain.Project](t, handler, http.MethodPost, "/api/projects", map[string]any{"name": "Links"}, http.StatusCreated)
	resource := requestJSON[domain.ProjectResource](t, handler, http.MethodPost, "/api/projects/"+project.ID+"/resources", map[string]any{"url": "https://github.com/flow/app"}, http.StatusCreated)
	if resource.Title != "GitHub" {
		t.Fatalf("untitled link title = %q", resource.Title)
	}
	titled := requestJSON[domain.ProjectResource](t, handler, http.MethodPost, "/api/projects/"+project.ID+"/resources", map[string]any{"url": "https://example.com", "title": "Spec"}, http.StatusCreated)
	if titled.Title != "Spec" {
		t.Fatalf("titled link title = %q", titled.Title)
	}
	initiative := requestJSON[domain.Initiative](t, handler, http.MethodPost, "/api/initiatives", map[string]any{"name": "Links initiative"}, http.StatusCreated)
	initiativeResource := requestJSON[domain.InitiativeResource](t, handler, http.MethodPost, "/api/initiatives/"+initiative.ID+"/resources", map[string]any{"url": "https://www.example.com/a"}, http.StatusCreated)
	if initiativeResource.Title != "Example" {
		t.Fatalf("untitled initiative link title = %q", initiativeResource.Title)
	}
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	pinned := requestJSON[domain.TeamPinnedResource](t, handler, http.MethodPost, "/api/teams/"+bootstrap.Teams[0].ID+"/resources", map[string]any{"resourceType": "link", "url": "https://figma.com/file/x", "title": "https://figma.com/file/x"}, http.StatusCreated)
	if pinned.Title != "Figma" {
		t.Fatalf("untitled team link title = %q", pinned.Title)
	}
}

func TestMilestoneDescriptionHistory(t *testing.T) {
	handler := newResourceGapsHandler(t)
	project := requestJSON[domain.Project](t, handler, http.MethodPost, "/api/projects", map[string]any{"name": "Milestones"}, http.StatusCreated)
	milestone := requestJSON[domain.ProjectMilestone](t, handler, http.MethodPost, "/api/projects/"+project.ID+"/milestones", map[string]any{"name": "Beta"}, http.StatusCreated)
	path := "/api/projects/" + project.ID + "/milestones/" + milestone.ID
	milestone = requestJSON[domain.ProjectMilestone](t, handler, http.MethodPatch, path, map[string]any{"description": "First"}, http.StatusOK)
	milestone = requestJSON[domain.ProjectMilestone](t, handler, http.MethodPatch, path, map[string]any{"description": "Second"}, http.StatusOK)
	milestone = requestJSON[domain.ProjectMilestone](t, handler, http.MethodPatch, path, map[string]any{"description": "Second", "name": "Beta 2"}, http.StatusOK)
	if milestone.Description != "Second" || len(milestone.DescriptionRevisions) != 2 || milestone.DescriptionRevisions[0].Description != "First" || milestone.DescriptionRevisions[1].Description != "" {
		t.Fatalf("milestone description history = %#v", milestone.DescriptionRevisions)
	}
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	projectIndex := slices.IndexFunc(bootstrap.Projects, func(item domain.Project) bool { return item.ID == project.ID })
	if projectIndex < 0 {
		t.Fatal("project missing from bootstrap")
	}
	reloaded := bootstrap.Projects[projectIndex]
	index := slices.IndexFunc(reloaded.Milestones, func(item domain.ProjectMilestone) bool { return item.ID == milestone.ID })
	if index < 0 || len(reloaded.Milestones[index].DescriptionRevisions) != 2 {
		t.Fatalf("milestone description history was not persisted: %#v", reloaded.Milestones)
	}
}
