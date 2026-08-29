package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestProjectUpdateReminderLifecycle(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	project := bootstrap.Projects[0]
	requestJSON[map[string]any](t, handler, http.MethodPut, "/api/project-update-settings", map[string]any{"cadenceDays": 1}, http.StatusOK)
	if err := repository.MutateWorkspace(context.Background(), bootstrap.Workspace.URLKey, "test.project_aged", project.ID, nil, func(data *domain.Bootstrap) error {
		for index := range data.Projects {
			if data.Projects[index].ID == project.ID {
				data.Projects[index].CreatedAt = time.Now().UTC().Add(-72 * time.Hour)
				data.Projects[index].Health = "onTrack"
			}
		}
		delete(data.ProjectUpdates, project.ID)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	reminders := slices.DeleteFunc(append([]domain.Notification{}, bootstrap.Notifications...), func(item domain.Notification) bool {
		return item.ProjectID != project.ID || item.Type != "projectUpdateReminder"
	})
	if len(reminders) == 0 || reminders[0].Category != "reminders" || reminders[0].ArchivedAt != nil {
		t.Fatalf("missing project update reminder was not generated: %#v", reminders)
	}
	reminderCount := len(reminders)
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if count := len(slices.DeleteFunc(append([]domain.Notification{}, bootstrap.Notifications...), func(item domain.Notification) bool {
		return item.ProjectID != project.ID || item.Type != "projectUpdateReminder"
	})); count != reminderCount {
		t.Fatalf("project update reminder was duplicated: %d", count)
	}
	requestJSON[domain.ProjectUpdate](t, handler, http.MethodPost, "/api/projects/"+project.ID+"/updates", map[string]any{"body": "Update posted after reminder"}, http.StatusCreated)
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	reminderIndex := slices.IndexFunc(bootstrap.Notifications, func(item domain.Notification) bool {
		return item.ProjectID == project.ID && item.Type == "projectUpdateReminder"
	})
	projectIndex := slices.IndexFunc(bootstrap.Projects, func(item domain.Project) bool { return item.ID == project.ID })
	if reminderIndex < 0 || projectIndex < 0 {
		t.Fatalf("posting a project update lost its records: reminder=%d project=%d", reminderIndex, projectIndex)
	}
	if bootstrap.Notifications[reminderIndex].ArchivedAt == nil || bootstrap.Projects[projectIndex].Health != "onTrack" {
		t.Fatalf("posting a project update did not resolve its reminder: notification=%#v project=%#v", bootstrap.Notifications[reminderIndex], bootstrap.Projects[projectIndex])
	}
}

func TestDocumentHistoryProjectAssociationAndTrashRestore(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	project := bootstrap.Projects[0]
	issue := bootstrap.Issues[0]

	document := requestJSON[domain.Document](t, handler, http.MethodPost, "/api/documents", map[string]any{
		"title": "Advanced document", "content": "First version", "projectIds": []string{project.ID}, "issueId": issue.ID,
	}, http.StatusCreated)
	if document.IssueID != issue.ID {
		t.Fatalf("document issue association = %q, want %q", document.IssueID, issue.ID)
	}
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if !slices.ContainsFunc(bootstrap.Documents, func(item domain.Document) bool { return item.ID == document.ID && item.IssueID == issue.ID }) {
		t.Fatal("document issue association did not survive bootstrap")
	}
	if !projectHasResource(bootstrap.Projects, project.ID, document.ID) {
		t.Fatal("creating an associated document did not add the project resource")
	}

	document = requestJSON[domain.Document](t, handler, http.MethodPatch, "/api/documents/"+document.ID, map[string]any{
		"title": "Renamed document", "content": "Second version", "projectIds": []string{},
	}, http.StatusOK)
	if len(document.Revisions) != 1 || document.Revisions[0].Content != "First version" {
		t.Fatalf("document revision was not recorded: %#v", document.Revisions)
	}
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if projectHasResource(bootstrap.Projects, project.ID, document.ID) {
		t.Fatal("removing a project association left a stale project resource")
	}

	document = requestJSON[domain.Document](t, handler, http.MethodPatch, "/api/documents/"+document.ID, map[string]any{"projectIds": []string{project.ID}}, http.StatusOK)
	requestJSON[any](t, handler, http.MethodDelete, "/api/documents/"+document.ID, nil, http.StatusNoContent)
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	trashIndex := slices.IndexFunc(bootstrap.Trash, func(item domain.TrashEntry) bool { return item.ResourceID == document.ID })
	if trashIndex < 0 {
		t.Fatal("deleted document was not retained in recently deleted")
	}
	requestJSON[domain.Document](t, handler, http.MethodPost, "/api/trash/"+bootstrap.Trash[trashIndex].ID+"/restore", nil, http.StatusOK)
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if !slices.ContainsFunc(bootstrap.Documents, func(item domain.Document) bool { return item.ID == document.ID }) || !projectHasResource(bootstrap.Projects, project.ID, document.ID) {
		t.Fatal("restoring a document did not restore the document and project resource")
	}
}

func TestInitiativeDocumentResourceCreatesRealDocumentBinding(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	var initiative domain.Initiative
	if len(bootstrap.Initiatives) > 0 {
		initiative = bootstrap.Initiatives[0]
	} else {
		initiative = requestJSON[domain.Initiative](t, handler, http.MethodPost, "/api/initiatives", map[string]any{"name": "Resource initiative"}, http.StatusCreated)
	}
	document := requestJSON[domain.Document](t, handler, http.MethodPost, "/api/documents", map[string]any{"title": "Initiative brief"}, http.StatusCreated)
	resource := requestJSON[domain.InitiativeResource](t, handler, http.MethodPost, "/api/initiatives/"+initiative.ID+"/resources", map[string]any{"type": "document", "documentId": document.ID}, http.StatusCreated)
	if resource.DocumentID != document.ID || resource.URL != "/"+bootstrap.Workspace.URLKey+"/document/"+document.SlugID || resource.Title != document.Title {
		t.Fatalf("document resource was not bound to the document: %#v", resource)
	}
}

func TestDocumentCommentThreads(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	document := requestJSON[domain.Document](t, handler, http.MethodPost, "/api/documents", map[string]any{"title": "Commented doc"}, http.StatusCreated)
	comment := requestJSON[domain.Comment](t, handler, http.MethodPost, "/api/documents/"+document.ID+"/comments", map[string]any{"body": "Top-level"}, http.StatusCreated)
	if comment.ParentID != nil || comment.Version != 1 {
		t.Fatalf("unexpected root comment: %#v", comment)
	}
	reply := requestJSON[domain.Comment](t, handler, http.MethodPost, "/api/documents/"+document.ID+"/comments", map[string]any{"body": "Reply", "parentId": comment.ID}, http.StatusCreated)
	if reply.ParentID == nil || *reply.ParentID != comment.ID {
		t.Fatalf("reply parent = %#v", reply.ParentID)
	}
	edited := requestJSON[domain.Comment](t, handler, http.MethodPatch, "/api/documents/"+document.ID+"/comments/"+comment.ID, map[string]any{"body": "Edited", "expectedVersion": comment.Version}, http.StatusOK)
	if edited.Body != "Edited" || edited.Version != comment.Version+1 {
		t.Fatalf("edited comment = %#v", edited)
	}
	reacted := requestJSON[domain.Comment](t, handler, http.MethodPost, "/api/documents/"+document.ID+"/comments/"+comment.ID+"/reactions", map[string]string{"emoji": "👍"}, http.StatusOK)
	if len(reacted.Reactions["👍"]) != 1 {
		t.Fatalf("reaction missing: %#v", reacted.Reactions)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/documents/"+document.ID+"/comments/"+comment.ID, nil, http.StatusNoContent)
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(bootstrap.Comments[document.ID]) != 0 {
		t.Fatalf("deleting root did not delete thread: %#v", bootstrap.Comments[document.ID])
	}
}

func TestDocumentIndexFiltersAndTeamBinding(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	team := bootstrap.Teams[0]
	visible := requestJSON[domain.Document](t, handler, http.MethodPost, "/api/documents", map[string]any{"title": "Team handbook", "teamIds": []string{team.ID}}, http.StatusCreated)
	requestJSON[domain.Document](t, handler, http.MethodPost, "/api/documents", map[string]any{"title": "Workspace notes"}, http.StatusCreated)
	requestJSON[domain.Document](t, handler, http.MethodPatch, "/api/documents/"+visible.ID, map[string]any{"archived": true}, http.StatusOK)
	var current []domain.Document
	current = requestJSON[[]domain.Document](t, handler, http.MethodGet, "/api/documents?teamId="+team.ID, nil, http.StatusOK)
	if len(current) != 0 {
		t.Fatalf("archived document leaked into default team index: %#v", current)
	}
	current = requestJSON[[]domain.Document](t, handler, http.MethodGet, "/api/documents?teamId="+team.ID+"&archived=all", nil, http.StatusOK)
	if len(current) != 1 || current[0].ID != visible.ID {
		t.Fatalf("team document archive filter = %#v", current)
	}
	current = requestJSON[[]domain.Document](t, handler, http.MethodGet, "/api/documents?q=workspace", nil, http.StatusOK)
	if len(current) != 1 || current[0].Title != "Workspace notes" {
		t.Fatalf("document search index = %#v", current)
	}
}

func TestDocumentTemplateCRUDAndApplication(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	team := bootstrap.Teams[0]
	template := requestJSON[domain.DocumentTemplate](t, handler, http.MethodPost, "/api/document-templates", map[string]any{
		"teamId": team.ID, "name": "Decision record", "title": "Architecture decision", "content": "Context\n\nDecision",
	}, http.StatusCreated)
	if template.ID == "" || template.TeamID != team.ID {
		t.Fatalf("document template was not created: %#v", template)
	}
	document := requestJSON[domain.Document](t, handler, http.MethodPost, "/api/documents", map[string]any{"templateId": template.ID}, http.StatusCreated)
	if document.Title != "Architecture decision" || document.Content != "Context\n\nDecision" || !slices.Contains(document.TeamIDs, team.ID) {
		t.Fatalf("document did not apply template defaults: %#v", document)
	}
	template = requestJSON[domain.DocumentTemplate](t, handler, http.MethodPatch, "/api/document-templates/"+template.ID, map[string]any{"name": "Updated decision record"}, http.StatusOK)
	if template.Name != "Updated decision record" {
		t.Fatalf("document template was not updated: %#v", template)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/document-templates/"+template.ID, nil, http.StatusNoContent)
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if slices.ContainsFunc(bootstrap.DocumentTemplates, func(item domain.DocumentTemplate) bool { return item.ID == template.ID }) {
		t.Fatal("document template was not deleted")
	}
}

func TestTemplatesAskApprovalSLAAndUnifiedUserState(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	team, project := bootstrap.Teams[0], bootstrap.Projects[0]
	issueLabelIndex := slices.IndexFunc(bootstrap.Labels, func(label domain.IssueLabel) bool { return labelResourceType(label) == "issue" })
	projectLabelIndex := slices.IndexFunc(bootstrap.Labels, func(label domain.IssueLabel) bool { return labelResourceType(label) == "project" })
	if issueLabelIndex < 0 || projectLabelIndex < 0 {
		t.Fatal("seed needs issue and project labels")
	}
	issueLabel, projectLabel := bootstrap.Labels[issueLabelIndex], bootstrap.Labels[projectLabelIndex]
	states := statesForTeam(&bootstrap, team.ID)
	if len(states) == 0 {
		t.Fatal("seed team has no workflow states")
	}
	state := states[0]

	projectTemplate := requestJSON[domain.ProjectTemplate](t, handler, http.MethodPost, "/api/project-templates", map[string]any{
		"name": "Launch template", "summary": "Template summary", "description": "Template body", "statusId": bootstrap.ProjectStatuses[0].ID,
		"priority": 2, "teamIds": []string{team.ID}, "labelIds": []string{projectLabel.ID}, "color": "#d15f5f", "icon": "◇",
	}, http.StatusCreated)
	createdProject := requestJSON[domain.Project](t, handler, http.MethodPost, "/api/projects", map[string]any{"templateId": projectTemplate.ID}, http.StatusCreated)
	if createdProject.Name != projectTemplate.Name || createdProject.Summary != projectTemplate.Summary || createdProject.Priority != 2 || !slices.Contains(createdProject.LabelIDs, projectLabel.ID) {
		t.Fatalf("project template was not applied: %#v", createdProject)
	}

	issueTemplate := requestJSON[domain.IssueTemplate](t, handler, http.MethodPost, "/api/teams/"+team.ID+"/templates", map[string]any{
		"name": "Approved request", "body": "Template issue body", "stateId": state.ID, "priority": 2, "projectId": project.ID, "labelIds": []string{issueLabel.ID},
	}, http.StatusCreated)
	ask := requestJSON[domain.Ask](t, handler, http.MethodPost, "/api/asks", map[string]any{
		"title": "Customer ask", "teamId": team.ID, "templateId": issueTemplate.ID,
	}, http.StatusCreated)
	ask = requestJSON[domain.Ask](t, handler, http.MethodPost, "/api/asks/"+ask.ID+"/decision", map[string]any{"decision": "approved", "note": "Ready"}, http.StatusOK)
	if ask.IssueID == "" || ask.Status != "approved" || len(ask.Approvals) != 1 {
		t.Fatalf("ask approval did not create an issue: %#v", ask)
	}
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	approvedIssue := findIssue(t, bootstrap.Issues, ask.IssueID)
	if approvedIssue.Description != issueTemplate.Body || approvedIssue.Project == nil || approvedIssue.Project.ID != project.ID || !slices.ContainsFunc(approvedIssue.Labels, func(item domain.IssueLabel) bool { return item.ID == issueLabel.ID }) {
		t.Fatalf("approved ask did not apply its issue template: %#v", approvedIssue)
	}

	rule := requestJSON[domain.SLARule](t, handler, http.MethodPost, "/api/sla-rules", map[string]any{
		"name": "Priority response", "teamIds": []string{team.ID}, "filters": map[string]any{"priority": "2"}, "targetMinutes": 60, "pauseStatuses": []string{state.ID},
	}, http.StatusCreated)
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	issueSLAIndex := slices.IndexFunc(bootstrap.IssueSLAs, func(item domain.IssueSLA) bool { return item.IssueID == ask.IssueID && item.RuleID == rule.ID })
	if issueSLAIndex < 0 || bootstrap.IssueSLAs[issueSLAIndex].Status != "paused" {
		t.Fatalf("matching SLA did not start paused for a pause status: %#v", bootstrap.IssueSLAs)
	}

	draft := requestJSON[domain.Draft](t, handler, http.MethodPost, "/api/drafts", map[string]any{"type": "issue", "title": "Cross-device draft", "body": "Draft body"}, http.StatusCreated)
	draft = requestJSON[domain.Draft](t, handler, http.MethodPatch, "/api/drafts/"+draft.ID, map[string]any{"body": "Updated draft body"}, http.StatusOK)
	if draft.Body != "Updated draft body" {
		t.Fatal("draft update did not persist")
	}
	favoriteA := requestJSON[domain.Favorite](t, handler, http.MethodPut, "/api/favorites/project/"+project.ID, nil, http.StatusOK)
	favoriteB := requestJSON[domain.Favorite](t, handler, http.MethodPut, "/api/favorites/project/"+project.ID, nil, http.StatusOK)
	if favoriteA.ID != favoriteB.ID {
		t.Fatal("favorite endpoint created duplicates")
	}
	subscriptionA := requestJSON[domain.Subscription](t, handler, http.MethodPut, "/api/subscriptions/project/"+project.ID, nil, http.StatusOK)
	subscriptionB := requestJSON[domain.Subscription](t, handler, http.MethodPut, "/api/subscriptions/project/"+project.ID, nil, http.StatusOK)
	if subscriptionA.ID != subscriptionB.ID {
		t.Fatal("subscription endpoint created duplicates")
	}
	subscriptionC := requestJSON[domain.Subscription](t, handler, http.MethodPut, "/api/subscriptions/project/"+project.ID, map[string]any{"events": []string{"project-added"}}, http.StatusOK)
	if subscriptionC.ID != subscriptionA.ID || !slices.Equal(subscriptionC.Events, []string{"project-added"}) {
		t.Fatalf("subscription event update = %#v", subscriptionC)
	}
	customer := requestJSON[domain.Customer](t, handler, http.MethodPost, "/api/customers", map[string]any{"name": "Attachment customer"}, http.StatusCreated)
	request := requestJSON[domain.CustomerRequest](t, handler, http.MethodPost, "/api/customer-requests", map[string]any{"customerId": customer.ID, "body": "Attachment request"}, http.StatusCreated)
	attachment := uploadCustomerRequestAttachmentForTest(t, handler, request.ID, "evidence.txt", "customer evidence")
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	requestIndex := slices.IndexFunc(bootstrap.CustomerRequests, func(item domain.CustomerRequest) bool { return item.ID == request.ID })
	if requestIndex < 0 || len(bootstrap.CustomerRequests[requestIndex].Attachments) != 1 || bootstrap.CustomerRequests[requestIndex].Attachments[0].ID != attachment.ID {
		t.Fatalf("customer request attachment was not persisted: %#v", bootstrap.CustomerRequests)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/customer-requests/"+request.ID+"/attachments/"+attachment.ID, nil, http.StatusNoContent)
}

func TestImportMappingAndBackgroundExport(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	team, project, label, user := bootstrap.Teams[0], bootstrap.Projects[0], bootstrap.Labels[0], bootstrap.Users[0]
	state := statesForTeam(&bootstrap, team.ID)[0]
	csvBody := "Title,Description,Priority,Status,Assignee,Labels,Project,Due Date\nImported workflow,Imported body,High," + state.Name + "," + user.Email + "," + label.Name + "," + project.Name + ",2026-09-30\n"
	job := previewImportRequest(t, handler, "issues.csv", csvBody)
	requestJSON[domain.ImportJob](t, handler, http.MethodPost, "/api/imports/"+job.ID+"/commit", map[string]any{
		"teamId":  team.ID,
		"mapping": map[string]string{"title": "Title", "description": "Description", "priority": "Priority", "status": "Status", "assignee": "Assignee", "labels": "Labels", "project": "Project", "dueDate": "Due Date"},
	}, http.StatusAccepted)
	job = waitForImport(t, handler, job.ID)
	if job.Status != "completed" || job.Imported != 1 || len(job.Errors) != 0 {
		t.Fatalf("import did not complete: %#v", job)
	}
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	importedIndex := slices.IndexFunc(bootstrap.Issues, func(item domain.Issue) bool { return item.Title == "Imported workflow" })
	if importedIndex < 0 {
		t.Fatal("imported issue was not persisted")
	}
	imported := bootstrap.Issues[importedIndex]
	if imported.Priority != 2 || imported.State.ID != state.ID || imported.Assignee == nil || imported.Assignee.ID != user.ID || imported.Project == nil || imported.Project.ID != project.ID || len(imported.Labels) != 1 || imported.DueDate == nil || *imported.DueDate != "2026-09-30" {
		t.Fatalf("mapped import fields were not applied: %#v", imported)
	}
	invalidJob := previewImportRequest(t, handler, "invalid.csv", "Title,Priority,Status,Assignee,Labels,Project,Due Date\nNeeds review,Maximum,Unknown state,nobody@example.com,Unknown label,Unknown project,09/30/2026\n")
	requestJSON[domain.ImportJob](t, handler, http.MethodPost, "/api/imports/"+invalidJob.ID+"/commit", map[string]any{
		"teamId":  team.ID,
		"mapping": map[string]string{"title": "Title", "priority": "Priority", "status": "Status", "assignee": "Assignee", "labels": "Labels", "project": "Project", "dueDate": "Due Date"},
	}, http.StatusAccepted)
	invalidJob = waitForImport(t, handler, invalidJob.ID)
	if invalidJob.Imported != 1 || len(invalidJob.Errors) != 4 {
		t.Fatalf("unmatched import values were not reported: %#v", invalidJob)
	}

	export := requestJSON[domain.ExportJob](t, handler, http.MethodPost, "/api/exports", map[string]any{"format": "json"}, http.StatusAccepted)
	deadline := time.Now().Add(2 * time.Second)
	for export.Status != "completed" && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
		bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
		index := slices.IndexFunc(bootstrap.ExportJobs, func(item domain.ExportJob) bool { return item.ID == export.ID })
		if index >= 0 {
			export = bootstrap.ExportJobs[index]
		}
	}
	if export.Status != "completed" || export.Filename == "" {
		t.Fatalf("background export did not complete: %#v", export)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/exports/"+export.ID+"/download", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Header().Get("Content-Disposition"), export.Filename) {
		t.Fatalf("export download failed: status=%d headers=%v body=%s", recorder.Code, recorder.Header(), recorder.Body.String())
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil || len(payload["issues"]) == 0 || len(payload["documents"]) == 0 {
		t.Fatalf("export package is incomplete: keys=%v error=%v", payload, err)
	}
}

func TestCSVExportUsesLinearIssueSchema(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	export := requestJSON[domain.ExportJob](t, handler, http.MethodPost, "/api/exports", map[string]any{"format": "csv"}, http.StatusAccepted)
	deadline := time.Now().Add(2 * time.Second)
	for export.Status != "completed" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
		if index := slices.IndexFunc(bootstrap.ExportJobs, func(item domain.ExportJob) bool { return item.ID == export.ID }); index >= 0 {
			export = bootstrap.ExportJobs[index]
		}
	}
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/exports/"+export.ID+"/download", nil)
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("CSV export failed: %d %s", recorder.Code, recorder.Body.String())
	}
	rows, err := csv.NewReader(strings.NewReader(recorder.Body.String())).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ID", "Team", "Title", "Description", "Status", "Estimate", "Priority", "Project ID", "Project", "Creator", "Assignee", "Labels", "Cycle Number", "Cycle Name", "Cycle Start", "Cycle End", "Created", "Updated", "Started", "Triaged", "Completed", "Canceled", "Archived", "Due Date", "Parent issue", "Initiatives", "Project Milestone ID", "Project Milestone", "SLA Status"}
	if len(rows) < 2 || !slices.Equal(rows[0], want) {
		t.Fatalf("unexpected Linear CSV schema: %#v", rows)
	}
}

func TestImportRetryAndIntegrationOAuthLifecycle(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	team := bootstrap.Teams[0]
	job := previewImportRequest(t, handler, "retry.csv", "Title,Description\n,missing title\n")
	requestJSON[domain.ImportJob](t, handler, http.MethodPost, "/api/imports/"+job.ID+"/commit", map[string]any{
		"teamId": team.ID, "mapping": map[string]string{"title": "Title"},
	}, http.StatusAccepted)
	job = waitForImport(t, handler, job.ID)
	if job.Status != "failed" || job.Progress != 100 || job.Error == "" {
		t.Fatalf("failed import did not expose recoverable state: %#v", job)
	}
	job = requestJSON[domain.ImportJob](t, handler, http.MethodPost, "/api/imports/"+job.ID+"/retry", nil, http.StatusOK)
	if job.Status != "mapping" || job.RetryCount != 1 || job.Progress != 0 {
		t.Fatalf("retry did not reset import state: %#v", job)
	}
	connection := requestJSON[domain.IntegrationConnection](t, handler, http.MethodPut, "/api/integrations/slack", map[string]any{
		"config": map[string]string{"authorizationURL": "https://idp.example.test/authorize", "clientID": "flow-client", "redirectURI": "https://flow.example.test/api/integrations/slack/oauth/callback"},
	}, http.StatusOK)
	started := requestJSON[map[string]string](t, handler, http.MethodPost, "/api/integrations/slack/oauth/start", nil, http.StatusOK)
	if started["state"] == "" || !strings.Contains(started["authorizationURL"], "client_id=flow-client") {
		t.Fatalf("OAuth start URL/state missing: %#v", started)
	}
	completed := requestJSON[map[string]string](t, handler, http.MethodGet, "/api/integrations/slack/oauth/callback?workspace="+url.QueryEscape(bootstrap.Workspace.URLKey)+"&state="+url.QueryEscape(started["state"])+"&code=test-code", nil, http.StatusOK)
	if completed["status"] != "configured" || completed["connectionId"] != connection.ID {
		t.Fatalf("OAuth callback did not complete: %#v", completed)
	}
	replayed := requestJSON[map[string]any](t, handler, http.MethodGet, "/api/integrations/slack/oauth/callback?workspace="+url.QueryEscape(bootstrap.Workspace.URLKey)+"&state="+url.QueryEscape(started["state"])+"&code=test-code", nil, http.StatusNotFound)
	_ = replayed
}

func previewImportRequest(t *testing.T, handler http.Handler, filename, content string) domain.ImportJob {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/imports/preview", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("preview import status %d: %s", recorder.Code, recorder.Body.String())
	}
	var job domain.ImportJob
	if err := json.Unmarshal(recorder.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	return job
}

func waitForImport(t *testing.T, handler http.Handler, id string) domain.ImportJob {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var job domain.ImportJob
	for time.Now().Before(deadline) {
		job = requestJSON[domain.ImportJob](t, handler, http.MethodGet, "/api/imports/"+id, nil, http.StatusOK)
		if job.Status == "completed" || job.Status == "failed" || job.Status == "cancelled" {
			return job
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("import job %s did not finish: %#v", id, job)
	return job
}

func uploadCustomerRequestAttachmentForTest(t *testing.T, handler http.Handler, requestID, filename, content string) domain.Attachment {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/customer-requests/"+requestID+"/attachments", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("customer request attachment status %d: %s", recorder.Code, recorder.Body.String())
	}
	var attachment domain.Attachment
	if err := json.Unmarshal(recorder.Body.Bytes(), &attachment); err != nil {
		t.Fatal(err)
	}
	return attachment
}

func projectHasResource(projects []domain.Project, projectID, resourceID string) bool {
	index := slices.IndexFunc(projects, func(item domain.Project) bool { return item.ID == projectID })
	return index >= 0 && slices.ContainsFunc(projects[index].Resources, func(item domain.ProjectResource) bool { return item.ID == resourceID })
}
