package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestWorkspaceSettingsPersistence(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	want := map[string]any{
		"values": map[string]any{"firstDay": "Sunday", "emoticons": false},
		"lists":  map[string]any{"apiKeys": []any{map[string]any{"id": "key_1", "name": "Test"}}},
	}
	requestJSON[map[string]any](t, handler, http.MethodPut, "/api/workspace/settings", want, http.StatusOK)
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	values, ok := bootstrap.Settings["values"].(map[string]any)
	if !ok || values["firstDay"] != "Sunday" || values["emoticons"] != false {
		t.Fatalf("settings did not survive bootstrap round trip: %#v", bootstrap.Settings)
	}
}

func TestLabelGroupArchiveCascadesToChildLabels(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})

	group := requestJSON[domain.LabelGroup](t, handler, http.MethodPost, "/api/label-groups", map[string]any{
		"name": "Delivery stage", "resourceType": "issue",
	}, http.StatusCreated)
	label := requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/labels", map[string]any{
		"name": "Ready for QA", "resourceType": "issue", "groupId": group.ID,
	}, http.StatusCreated)
	archivedAt := time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)
	group = requestJSON[domain.LabelGroup](t, handler, http.MethodPatch, "/api/label-groups/"+group.ID, map[string]any{
		"archivedAt": archivedAt,
	}, http.StatusOK)
	if group.ArchivedAt == nil {
		t.Fatal("group was not archived")
	}
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	labelIndex := slices.IndexFunc(bootstrap.Labels, func(item domain.IssueLabel) bool { return item.ID == label.ID })
	if labelIndex < 0 || bootstrap.Labels[labelIndex].ArchivedAt == nil {
		t.Fatalf("child label was not archived with its group: %#v", bootstrap.Labels)
	}

	group = requestJSON[domain.LabelGroup](t, handler, http.MethodPatch, "/api/label-groups/"+group.ID, map[string]any{
		"archivedAt": "",
	}, http.StatusOK)
	if group.ArchivedAt != nil {
		t.Fatal("group was not restored")
	}
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	labelIndex = slices.IndexFunc(bootstrap.Labels, func(item domain.IssueLabel) bool { return item.ID == label.ID })
	if labelIndex < 0 || bootstrap.Labels[labelIndex].ArchivedAt != nil {
		t.Fatalf("child label was not restored with its group: %#v", bootstrap.Labels)
	}
}

func TestMoveWorkspaceLabelToTeamsPreservesIssueAssignments(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})

	before := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(before.Issues) < 1 {
		t.Fatal("seed must include issues")
	}
	first := before.Issues[0]
	secondTeam := requestJSON[domain.Team](t, handler, http.MethodPost, "/api/workspaces/cleantrack/teams", map[string]any{"name": "Second team", "key": "SEC"}, http.StatusCreated)
	second := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Second team issue", "description": "Move label coverage", "teamId": secondTeam.ID}, http.StatusCreated)
	label := requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/labels", map[string]any{
		"name": "Shared test label", "resourceType": "issue",
	}, http.StatusCreated)
	for _, issue := range []domain.Issue{first, second} {
		labelIDs := []string{label.ID}
		for _, existing := range issue.Labels {
			labelIDs = append(labelIDs, existing.ID)
		}
		requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+issue.ID, map[string]any{"labelIds": labelIDs}, http.StatusOK)
	}
	sourceID := label.ID
	before = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	usedByTeam := map[string]bool{}
	affectedIssues := map[string]string{}
	for _, issue := range before.Issues {
		if slices.ContainsFunc(issue.Labels, func(label domain.IssueLabel) bool { return label.ID == sourceID }) {
			usedByTeam[issue.Team.ID] = true
			affectedIssues[issue.ID] = issue.Team.ID
		}
	}
	if len(usedByTeam) != 2 {
		t.Fatalf("seed label must cover multiple teams, got %#v", usedByTeam)
	}

	moved := requestJSON[[]domain.IssueLabel](t, handler, http.MethodPost, "/api/labels/"+sourceID+"/move-to-teams", map[string]any{}, http.StatusOK)
	if len(moved) != len(usedByTeam) {
		t.Fatalf("moved labels = %d, want %d: %#v", len(moved), len(usedByTeam), moved)
	}
	for _, label := range moved {
		if !usedByTeam[label.Scope] || label.ID == sourceID || label.GroupID != "" {
			t.Fatalf("invalid team label: %#v", label)
		}
	}

	after := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if slices.ContainsFunc(after.Labels, func(label domain.IssueLabel) bool { return label.ID == sourceID }) {
		t.Fatal("workspace label still exists after moving to teams")
	}
	for issueID, teamID := range affectedIssues {
		issue := findIssue(t, after.Issues, issueID)
		if !slices.ContainsFunc(issue.Labels, func(label domain.IssueLabel) bool {
			return label.Name == "Shared test label" && label.Scope == teamID && label.ID != sourceID
		}) {
			t.Fatalf("issue %s did not receive its team label: %#v", issueID, issue.Labels)
		}
	}
}

func TestIssueLifecycle(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	s := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true}
	handler := newHandler(s)

	created := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{
		"title": "Issue engine test", "description": "Initial", "stateId": "state_todo", "priority": 2,
		"assigneeId": "usr_zheng", "projectId": "project_cruise", "dueDate": "2026-09-01", "labelIds": []string{"label_type_defect"},
		"descriptionState": `{"type":"doc","content":[{"type":"heading","attrs":{"level":2},"content":[{"type":"text","text":"Initial"}]}]}`,
		"descriptionData":  map[string]any{"type": "doc", "content": []any{map[string]any{"type": "heading", "attrs": map[string]any{"level": 2}}}}, "contentState": "CREATE_STATE",
	}, http.StatusCreated)
	if created.ID == "" || created.State.ID != "state_todo" || created.Priority != 2 || created.Project == nil || len(created.Labels) != 1 || created.DescriptionState == "" || created.DocumentContent == nil || created.DocumentContent.ContentState != "CREATE_STATE" || created.DocumentContent.ContentData["type"] != "doc" {
		t.Fatalf("create did not persist properties: %#v", created)
	}
	for _, stateID := range []string{"state_canceled", "state_duplicate"} {
		updatedState := requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+created.ID, map[string]any{"stateId": stateID}, http.StatusOK)
		if updatedState.State.ID != stateID {
			t.Fatalf("status update = %q, want %q", updatedState.State.ID, stateID)
		}
		bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
		if got := findIssue(t, bootstrap.Issues, created.ID).State.ID; got != stateID {
			t.Fatalf("persisted status = %q, want %q", got, stateID)
		}
	}
	bootstrapAfterStateChanges := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if !slices.ContainsFunc(bootstrapAfterStateChanges.Activities[created.ID], func(item domain.ActivityEvent) bool {
		return item.Metadata["state"] == "Canceled" && item.Metadata["stateBefore"] == "Todo"
	}) || !slices.ContainsFunc(bootstrapAfterStateChanges.Activities[created.ID], func(item domain.ActivityEvent) bool {
		return item.Metadata["state"] == "Duplicate" && item.Metadata["stateBefore"] == "Canceled"
	}) {
		t.Fatalf("state history did not retain previous states: %#v", bootstrapAfterStateChanges.Activities[created.ID])
	}

	updated := requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+created.ID, map[string]any{
		"title": "Autosaved title", "description": "Autosaved description", "subscriberIds": []string{"usr_zheng", "usr_jiaozongben"},
		"descriptionState": `{"type":"doc","content":[{"type":"paragraph"}]}`,
		"descriptionData":  map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph"}}}, "contentState": "AQID",
	}, http.StatusOK)
	if updated.Title != "Autosaved title" || updated.DescriptionState == "" || len(updated.SubscriberIDs) != 2 || updated.DocumentContent == nil || updated.DocumentContent.ContentState != "AQID" || updated.DocumentContent.Content != "Autosaved description" || updated.DocumentContent.ContentData["type"] != "doc" {
		t.Fatalf("update failed: %#v", updated)
	}
	bootstrapAfterSave := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	persisted := findIssue(t, bootstrapAfterSave.Issues, created.ID)
	if persisted.Description != "Autosaved description" || persisted.DescriptionState != updated.DescriptionState || persisted.DocumentContent == nil || persisted.DocumentContent.ContentState != "AQID" || persisted.DocumentContent.ContentData["type"] != "doc" {
		t.Fatalf("editor content did not survive bootstrap round trip: %#v", persisted)
	}
	reactedIssue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues/"+created.ID+"/reactions", map[string]string{"emoji": "🎉"}, http.StatusOK)
	if len(reactedIssue.Reactions["🎉"]) != 1 {
		t.Fatalf("issue reaction = %#v", reactedIssue.Reactions)
	}
	bootstrapAfterReaction := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(findIssue(t, bootstrapAfterReaction.Issues, created.ID).Reactions["🎉"]) != 1 {
		t.Fatal("issue reaction did not survive bootstrap round trip")
	}
	reactedIssue = requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues/"+created.ID+"/reactions", map[string]string{"emoji": "🎉"}, http.StatusOK)
	if len(reactedIssue.Reactions) != 0 {
		t.Fatalf("issue reaction toggle off = %#v", reactedIssue.Reactions)
	}

	child := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{
		"title": "Child issue", "parentId": created.ID,
	}, http.StatusCreated)
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	parent := findIssue(t, bootstrap.Issues, created.ID)
	if child.ParentID == nil || *child.ParentID != created.ID || !contains(parent.SubIssueIDs, child.ID) {
		t.Fatalf("parent linkage missing: parent=%#v child=%#v", parent.SubIssueIDs, child.ParentID)
	}
	requestJSON[any](t, handler, http.MethodPatch, "/api/issues/"+created.ID, map[string]any{"parentId": child.ID}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodPost, "/api/issues/"+child.ID+"/relations", map[string]any{
		"type": "parent_of", "relatedIssueId": created.ID,
	}, http.StatusBadRequest)

	relation := requestJSON[domain.IssueRelation](t, handler, http.MethodPost, "/api/issues/"+created.ID+"/relations", map[string]any{
		"type": "blocks", "relatedIssueId": child.ID,
	}, http.StatusCreated)
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	parent = findIssue(t, bootstrap.Issues, created.ID)
	child = findIssue(t, bootstrap.Issues, child.ID)
	if len(parent.Relations) != 1 || len(child.Relations) != 1 || child.Relations[0].Type != "blocked_by" {
		t.Fatalf("inverse relation missing: parent=%#v child=%#v", parent.Relations, child.Relations)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/issues/"+child.ID+"/relations/"+relation.ID, nil, http.StatusNoContent)

	comment := requestJSON[domain.Comment](t, handler, http.MethodPost, "/api/issues/"+created.ID+"/comments", map[string]string{"body": "A persisted comment"}, http.StatusCreated)
	if comment.Body != "A persisted comment" {
		t.Fatalf("comment = %#v", comment)
	}
	edited := requestJSON[domain.Comment](t, handler, http.MethodPatch, "/api/issues/"+created.ID+"/comments/"+comment.ID, map[string]any{"body": "Edited comment", "bodyData": map[string]any{"type": "doc"}}, http.StatusOK)
	if edited.Body != "Edited comment" || edited.EditedAt == nil {
		t.Fatalf("edited comment = %#v", edited)
	}
	reacted := requestJSON[domain.Comment](t, handler, http.MethodPost, "/api/issues/"+created.ID+"/comments/"+comment.ID+"/reactions", map[string]string{"emoji": "👍"}, http.StatusOK)
	if len(reacted.Reactions["👍"]) != 1 {
		t.Fatalf("reaction = %#v", reacted.Reactions)
	}
	reply := requestJSON[domain.Comment](t, handler, http.MethodPost, "/api/issues/"+created.ID+"/comments", map[string]any{"body": "Reply", "parentId": comment.ID}, http.StatusCreated)
	if reply.ParentID == nil || *reply.ParentID != comment.ID {
		t.Fatalf("reply = %#v", reply)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/issues/"+created.ID+"/comments/"+comment.ID, nil, http.StatusNoContent)

	attachment := upload(t, handler, created.ID, "proof.txt", []byte("proof"))
	if attachment.Size != 5 {
		t.Fatalf("attachment = %#v", attachment)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/issues/"+created.ID+"/attachments/"+attachment.ID, nil, http.StatusNoContent)

	batch := requestJSON[[]domain.Issue](t, handler, http.MethodPost, "/api/issues/batch", map[string]any{
		"issueIds": []string{created.ID, child.ID}, "update": map[string]any{"priority": 4, "archived": true},
	}, http.StatusOK)
	if len(batch) != 2 || batch[0].Priority != 4 || batch[0].ArchivedAt == nil {
		t.Fatalf("batch update failed: %#v", batch)
	}

	events := requestJSON[[]domain.DomainEvent](t, handler, http.MethodGet, "/api/events?aggregateId="+created.ID, nil, http.StatusOK)
	if len(events) == 0 || events[0].AggregateID != created.ID {
		t.Fatalf("events missing aggregate: %#v", events)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/issues/"+created.ID, nil, http.StatusNoContent)
}

func TestIssueOptionsPersistence(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	issue := bootstrap.Issues[0]
	previousDescription, previousState := issue.Description, issue.DescriptionState

	nextOccurrence := time.Now().UTC().Add(7 * 24 * time.Hour).Truncate(time.Second)
	updated := requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+issue.ID, map[string]any{
		"description": "Description with history", "descriptionState": `{"type":"doc","content":[]}`,
		"descriptionData": map[string]any{"type": "doc", "content": []any{}}, "contentState": "HISTORY_STATE",
		"recurrence": "weekly", "nextOccurrenceAt": nextOccurrence.Format(time.RFC3339),
	}, http.StatusOK)
	if updated.Recurrence != "weekly" || updated.NextOccurrenceAt == nil || !updated.NextOccurrenceAt.Equal(nextOccurrence) {
		t.Fatalf("recurrence was not returned: %#v", updated)
	}

	link := requestJSON[domain.Attachment](t, handler, http.MethodPost, "/api/issues/"+issue.ID+"/links", map[string]any{
		"url": "https://linear.app/docs", "title": "Linear docs",
	}, http.StatusCreated)
	if link.ContentType != "text/uri-list" || link.Title != "Linear docs" || link.URL != "https://linear.app/docs" {
		t.Fatalf("issue link = %#v", link)
	}

	remindAt := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second)
	reminder := requestJSON[domain.Notification](t, handler, http.MethodPost, "/api/issues/"+issue.ID+"/reminders", map[string]any{
		"remindAt": remindAt.Format(time.RFC3339),
	}, http.StatusCreated)
	if reminder.IssueID != issue.ID || reminder.Category != "reminders" || reminder.SnoozedUntil == nil || !reminder.SnoozedUntil.Equal(remindAt) {
		t.Fatalf("issue reminder = %#v", reminder)
	}

	loop := requestJSON[domain.Ask](t, handler, http.MethodPost, "/api/issues/"+issue.ID+"/loop-runs", map[string]any{
		"prompt": "Check the acceptance criteria",
	}, http.StatusCreated)
	if loop.IssueID != issue.ID || loop.Source != "loop" || loop.Status != "approved" || loop.Body != "Check the acceptance criteria" {
		t.Fatalf("issue loop run = %#v", loop)
	}

	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	persisted := findIssue(t, bootstrap.Issues, issue.ID)
	if persisted.Recurrence != "weekly" || persisted.NextOccurrenceAt == nil || !persisted.NextOccurrenceAt.Equal(nextOccurrence) || !slices.ContainsFunc(persisted.Attachments, func(item domain.Attachment) bool { return item.ID == link.ID }) {
		t.Fatalf("issue menu changes did not survive bootstrap: %#v", persisted)
	}
	if !slices.ContainsFunc(bootstrap.Activities[issue.ID], func(item domain.ActivityEvent) bool {
		return item.Metadata["descriptionBefore"] == previousDescription && item.Metadata["descriptionStateBefore"] == previousState
	}) {
		t.Fatalf("description history did not retain the previous version: %#v", bootstrap.Activities[issue.ID])
	}
	if !slices.ContainsFunc(bootstrap.Notifications, func(item domain.Notification) bool { return item.ID == reminder.ID }) {
		t.Fatal("issue reminder did not survive bootstrap")
	}
	if !slices.ContainsFunc(bootstrap.Asks, func(item domain.Ask) bool { return item.ID == loop.ID }) {
		t.Fatal("loop run did not survive bootstrap")
	}
	restored := requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+issue.ID, map[string]any{
		"description": previousDescription, "descriptionState": previousState,
	}, http.StatusOK)
	if restored.Description != previousDescription || restored.DocumentContent == nil || restored.DocumentContent.Content != previousDescription || restored.DocumentContent.ContentData != nil {
		t.Fatalf("description restore left stale structured content: %#v", restored.DocumentContent)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/issues/"+issue.ID+"/attachments/"+link.ID, nil, http.StatusNoContent)
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if slices.ContainsFunc(findIssue(t, bootstrap.Issues, issue.ID).Attachments, func(item domain.Attachment) bool { return item.ID == link.ID }) {
		t.Fatal("URL attachment was not deleted")
	}

	now := time.Now().UTC()
	err = repository.MutateWorkspace(context.Background(), bootstrap.Workspace.URLKey, "test.issue_delete_dependencies", issue.ID, nil, func(data *domain.Bootstrap) error {
		data.NotificationDeliveries = append(data.NotificationDeliveries, domain.NotificationDelivery{ID: "delivery-delete-test", NotificationID: reminder.ID, RecipientID: data.Viewer.ID, Channel: "desktop", Status: "pending", CreatedAt: now, UpdatedAt: now})
		data.Favorites = append(data.Favorites, domain.Favorite{ID: "favorite-delete-test", UserID: data.Viewer.ID, ResourceType: "issue", ResourceID: issue.ID, CreatedAt: now})
		data.Subscriptions = append(data.Subscriptions, domain.Subscription{ID: "subscription-delete-test", UserID: data.Viewer.ID, ResourceType: "issue", ResourceID: issue.ID, CreatedAt: now})
		data.Drafts = append(data.Drafts, domain.Draft{ID: "draft-delete-test", UserID: data.Viewer.ID, Type: "issue", ResourceID: issue.ID, CreatedAt: now, UpdatedAt: now})
		data.IssueSLAs = append(data.IssueSLAs, domain.IssueSLA{ID: "issue-sla-delete-test", IssueID: issue.ID, RuleID: "rule-delete-test", StartedAt: now, DueAt: now.Add(time.Hour), Status: "active"})
		data.SLAEvents = append(data.SLAEvents, domain.SLAEvent{ID: "sla-event-delete-test", IssueID: issue.ID, SLAID: "issue-sla-delete-test", Type: "started", CreatedAt: now})
		data.Releases = append(data.Releases, domain.Release{ID: "release-delete-test", Name: "Delete test", IssueIDs: []string{issue.ID}, CreatedAt: now, UpdatedAt: now})
		data.ProjectTemplates = append(data.ProjectTemplates, domain.ProjectTemplate{ID: "project-template-delete-test", Name: "Delete test", IssueIDs: []string{issue.ID}, CreatedAt: now, UpdatedAt: now})
		data.CustomerRequests = append(data.CustomerRequests, domain.CustomerRequest{ID: "customer-request-delete-test", IssueID: issue.ID, CreatedAt: now, UpdatedAt: now})
		data.Documents = append(data.Documents, domain.Document{ID: "document-delete-test", IssueID: issue.ID, CreatedAt: now, UpdatedAt: now})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/issues/"+issue.ID, nil, http.StatusNoContent)
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if slices.ContainsFunc(bootstrap.Notifications, func(item domain.Notification) bool { return item.IssueID == issue.ID }) ||
		slices.ContainsFunc(bootstrap.NotificationDeliveries, func(item domain.NotificationDelivery) bool { return item.NotificationID == reminder.ID }) ||
		slices.ContainsFunc(bootstrap.Asks, func(item domain.Ask) bool { return item.IssueID == issue.ID }) ||
		slices.ContainsFunc(bootstrap.IssueSLAs, func(item domain.IssueSLA) bool { return item.IssueID == issue.ID }) ||
		slices.ContainsFunc(bootstrap.SLAEvents, func(item domain.SLAEvent) bool { return item.IssueID == issue.ID }) ||
		slices.ContainsFunc(bootstrap.Drafts, func(item domain.Draft) bool { return item.Type == "issue" && item.ResourceID == issue.ID }) ||
		slices.ContainsFunc(bootstrap.Favorites, func(item domain.Favorite) bool { return item.ResourceType == "issue" && item.ResourceID == issue.ID }) ||
		slices.ContainsFunc(bootstrap.Subscriptions, func(item domain.Subscription) bool {
			return item.ResourceType == "issue" && item.ResourceID == issue.ID
		}) {
		t.Fatal("deleting an issue left issue-owned records behind")
	}
	if slices.ContainsFunc(bootstrap.Releases, func(item domain.Release) bool { return slices.Contains(item.IssueIDs, issue.ID) }) ||
		slices.ContainsFunc(bootstrap.ProjectTemplates, func(item domain.ProjectTemplate) bool { return slices.Contains(item.IssueIDs, issue.ID) }) {
		t.Fatal("deleting an issue left collection references behind")
	}
	if !slices.ContainsFunc(bootstrap.CustomerRequests, func(item domain.CustomerRequest) bool {
		return item.ID == "customer-request-delete-test" && item.IssueID == ""
	}) ||
		!slices.ContainsFunc(bootstrap.Documents, func(item domain.Document) bool { return item.ID == "document-delete-test" && item.IssueID == "" }) {
		t.Fatal("deleting an issue did not preserve and unlink related content")
	}
}

func TestSearchHistoryRecentResourcesAndVersionConflicts(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	issue := bootstrap.Issues[0]

	search := requestJSON[domain.SearchResponse](t, handler, http.MethodGet, "/api/search?q="+issue.Identifier, nil, http.StatusOK)
	if len(search.Results) == 0 || search.Results[0].ID != issue.ID || search.Results[0].Type != "issue" {
		t.Fatalf("issue search = %#v", search.Results)
	}
	if len(search.History) == 0 || search.History[0].Query != issue.Identifier {
		t.Fatalf("search history = %#v", search.History)
	}
	requestJSON[any](t, handler, http.MethodPost, "/api/recent", map[string]string{"type": "issue", "id": issue.ID}, http.StatusNoContent)
	recent := requestJSON[domain.SearchResponse](t, handler, http.MethodGet, "/api/search", nil, http.StatusOK)
	if len(recent.Results) == 0 || recent.Results[0].ID != issue.ID {
		t.Fatalf("recent resources = %#v", recent.Results)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/search/history", nil, http.StatusNoContent)
	cleared := requestJSON[domain.SearchResponse](t, handler, http.MethodGet, "/api/search", nil, http.StatusOK)
	if len(cleared.History) != 0 {
		t.Fatalf("search history was not cleared: %#v", cleared.History)
	}

	updated := requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+issue.ID, map[string]any{"title": "First writer", "expectedVersion": issue.Version}, http.StatusOK)
	if updated.Version != issue.Version+1 {
		t.Fatalf("issue version = %d, want %d", updated.Version, issue.Version+1)
	}
	conflict := requestJSON[map[string]any](t, handler, http.MethodPatch, "/api/issues/"+issue.ID, map[string]any{"title": "Stale writer", "expectedVersion": issue.Version}, http.StatusConflict)
	if conflict["code"] != "VERSION_CONFLICT" || conflict["current"] == nil {
		t.Fatalf("issue conflict payload = %#v", conflict)
	}

	comment := requestJSON[domain.Comment](t, handler, http.MethodPost, "/api/issues/"+issue.ID+"/comments", map[string]string{"body": "Versioned"}, http.StatusCreated)
	afterComment := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if !slices.ContainsFunc(afterComment.Notifications, func(notification domain.Notification) bool {
		return notification.CommentID == comment.ID && notification.RecipientID != afterComment.Viewer.ID && notification.ReadAt == nil
	}) {
		t.Fatalf("comment did not create an unread recipient notification: %#v", afterComment.Notifications)
	}
	edited := requestJSON[domain.Comment](t, handler, http.MethodPatch, "/api/issues/"+issue.ID+"/comments/"+comment.ID, map[string]any{"body": "First edit", "expectedVersion": comment.Version}, http.StatusOK)
	if edited.Version != comment.Version+1 {
		t.Fatalf("comment version = %d, want %d", edited.Version, comment.Version+1)
	}
	commentConflict := requestJSON[map[string]any](t, handler, http.MethodPatch, "/api/issues/"+issue.ID+"/comments/"+comment.ID, map[string]any{"body": "Stale edit", "expectedVersion": comment.Version}, http.StatusConflict)
	if commentConflict["code"] != "VERSION_CONFLICT" || commentConflict["current"] == nil {
		t.Fatalf("comment conflict payload = %#v", commentConflict)
	}
}

func TestWorkspaceSearchIndexesUnifiedResourceTypes(t *testing.T) {
	now := time.Now().UTC()
	data := domain.Bootstrap{
		Documents:  []domain.Document{{ID: "doc-1", Title: "Launch handbook", Content: "Customer rollout", UpdatedAt: now}},
		Customers:  []domain.Customer{{ID: "customer-1", Name: "Northstar Labs", Domains: []string{"northstar.example"}, Status: "active", UpdatedAt: now}},
		Releases:   []domain.Release{{ID: "release-1", Name: "Summer launch", Version: "v2.0", Status: "planned", UpdatedAt: now}},
		SavedViews: []domain.SavedView{{ID: "view-1", Name: "Escalated requests", Description: "High priority customer work", Resource: "issues", Scope: "workspace", UpdatedAt: now}},
	}

	for query, expected := range map[string]string{
		"Launch handbook":    "document",
		"northstar.example":  "customer",
		"Summer launch":      "release",
		"Escalated requests": "view",
	} {
		results := buildSearchResults(data, query, searchTypes(""))
		if len(results) != 1 || results[0].Type != expected {
			t.Fatalf("search %q = %#v, want one %s", query, results, expected)
		}
		if !searchResourceVisible(data, results[0].Type, results[0].ID) {
			t.Fatalf("search result %s:%s is not recordable as recent", results[0].Type, results[0].ID)
		}
	}
}

func TestRealtimeHubWorkspaceIsolationAndPresence(t *testing.T) {
	hub := newRealtimeHub()
	cleantrack, unsubscribeCleantrack := hub.subscribe("cleantrack")
	defer unsubscribeCleantrack()
	other, unsubscribeOther := hub.subscribe("other")
	defer unsubscribeOther()
	event := domain.RealtimeEvent{ID: "event_1", Type: "issue.updated", AggregateID: "issue_1", CreatedAt: time.Now().UTC()}
	hub.publish("cleantrack", event)
	select {
	case received := <-cleantrack:
		if received.ID != event.ID {
			t.Fatalf("received event = %#v", received)
		}
	case <-time.After(time.Second):
		t.Fatal("workspace subscriber did not receive event")
	}
	select {
	case leaked := <-other:
		t.Fatalf("event leaked across workspaces: %#v", leaked)
	case <-time.After(20 * time.Millisecond):
	}

	viewer := domain.User{ID: "user_1", DisplayName: "Viewer"}
	presence := hub.updatePresence("cleantrack", "client_1", viewer, "issue_1", "/cleantrack/issue/CLE-1")
	if len(presence) != 1 || presence[0].ClientID != "client_1" || presence[0].IssueID != "issue_1" {
		t.Fatalf("presence = %#v", presence)
	}
	if got := hub.snapshotPresence("other"); len(got) != 0 {
		t.Fatalf("presence leaked across workspaces: %#v", got)
	}
	if got := hub.removePresence("cleantrack", "client_1"); len(got) != 0 {
		t.Fatalf("presence was not removed: %#v", got)
	}
}

func TestCyclePlanningAndRollover(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(bootstrap.Cycles) < 3 || !bootstrap.CycleSettings["team_cleantrack"].Enabled {
		t.Fatalf("cycle bootstrap missing: cycles=%#v settings=%#v", bootstrap.Cycles, bootstrap.CycleSettings)
	}
	var current, upcoming domain.Cycle
	for _, cycle := range bootstrap.Cycles {
		if cycle.Status == "current" {
			current = cycle
		}
		if cycle.Status == "upcoming" && (upcoming.ID == "" || cycle.StartsAt.Before(upcoming.StartsAt)) {
			upcoming = cycle
		}
	}
	if current.ID == "" || upcoming.ID == "" {
		t.Fatalf("current/upcoming cycle missing: %#v", bootstrap.Cycles)
	}
	updated := requestJSON[domain.Cycle](t, handler, http.MethodPatch, "/api/cycles/"+upcoming.ID, map[string]any{"name": "Release cycle", "capacity": 8, "favorite": true}, http.StatusOK)
	if updated.Name != "Release cycle" || updated.Capacity != 8 || !updated.Favorite {
		t.Fatalf("cycle update failed: %#v", updated)
	}
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Rollover candidate", "stateId": "state_todo", "cycleId": current.ID}, http.StatusCreated)
	if issue.CycleID == nil || *issue.CycleID != current.ID {
		t.Fatalf("issue cycle assignment failed: %#v", issue.CycleID)
	}
	started := requestJSON[domain.Cycle](t, handler, http.MethodPost, "/api/cycles/"+upcoming.ID+"/start", nil, http.StatusOK)
	if started.Status != "current" {
		t.Fatalf("started cycle = %#v", started)
	}
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	rolled := findIssue(t, bootstrap.Issues, issue.ID)
	if rolled.CycleID == nil || *rolled.CycleID != upcoming.ID {
		t.Fatalf("issue did not roll over: %#v", rolled.CycleID)
	}
	settings := requestJSON[domain.CycleSettings](t, handler, http.MethodPatch, "/api/teams/team_cleantrack/cycle-settings", map[string]any{"durationWeeks": 3, "upcomingCount": 3, "autoAddStarted": true}, http.StatusOK)
	if settings.DurationWeeks != 3 || settings.UpcomingCount != 3 || !settings.AutoAddStarted {
		t.Fatalf("cycle settings update failed: %#v", settings)
	}
	requestJSON[domain.Cycle](t, handler, http.MethodPost, "/api/cycles/"+upcoming.ID+"/complete", nil, http.StatusOK)
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	currentCount, upcomingCount := 0, 0
	for _, cycle := range bootstrap.Cycles {
		if cycle.Status == "current" {
			currentCount++
		}
		if cycle.Status == "upcoming" {
			upcomingCount++
		}
	}
	if currentCount != 1 || upcomingCount < 3 {
		t.Fatalf("cycle recurrence failed: current=%d upcoming=%d cycles=%#v", currentCount, upcomingCount, bootstrap.Cycles)
	}
	events := requestJSON[[]domain.DomainEvent](t, handler, http.MethodGet, "/api/events?aggregateId="+upcoming.ID, nil, http.StatusOK)
	if len(events) < 3 || events[0].Type != "cycle.updated" || events[1].Type != "cycle.started" || events[2].Type != "cycle.completed" {
		t.Fatalf("cycle events = %#v", events)
	}
}

func TestProjectLifecycle(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})

	created := requestJSON[domain.Project](t, handler, http.MethodPost, "/api/projects", map[string]any{
		"name": "Project API test", "summary": "Initial", "priority": 2, "health": "onTrack",
		"leadId": "usr_zheng", "teamIds": []string{"team_cleantrack"},
	}, http.StatusCreated)
	if created.ID == "" || created.Name != "Project API test" || created.Priority != 2 || created.Lead == nil {
		t.Fatalf("project create failed: %#v", created)
	}

	updated := requestJSON[domain.Project](t, handler, http.MethodPatch, "/api/projects/"+created.ID, map[string]any{
		"name": "Updated project", "health": "atRisk", "targetDate": "2026-09-30", "statusId": "ps_planned",
		"memberIds": []string{"usr_zheng", "usr_jiaozongben"}, "labelIds": []string{"label_delivery"}, "description": "First project description", "updateCadence": "weekly",
	}, http.StatusOK)
	if updated.Name != "Updated project" || updated.Health != "atRisk" || updated.Status.ID != "ps_planned" || updated.TargetDate == nil || *updated.TargetDate != "2026-09-30" || !slices.Equal(updated.MemberIDs, []string{"usr_zheng", "usr_jiaozongben"}) || !slices.Equal(updated.LabelIDs, []string{"label_delivery"}) {
		t.Fatalf("project update failed: %#v", updated)
	}
	updated = requestJSON[domain.Project](t, handler, http.MethodPatch, "/api/projects/"+created.ID, map[string]any{"description": "Second project description"}, http.StatusOK)
	if updated.UpdateCadence != "weekly" || len(updated.DescriptionRevisions) < 1 || updated.DescriptionRevisions[0].Description != "First project description" {
		t.Fatalf("project cadence or description history was not persisted: %#v", updated)
	}
	reminder := requestJSON[domain.Notification](t, handler, http.MethodPost, "/api/projects/"+created.ID+"/reminders", map[string]any{"remindAt": time.Now().UTC().Add(time.Hour).Format(time.RFC3339)}, http.StatusCreated)
	if reminder.ProjectID != created.ID || reminder.Type != "projectReminder" || reminder.SnoozedUntil == nil {
		t.Fatalf("project reminder was not created: %#v", reminder)
	}
	requestJSON[any](t, handler, http.MethodPatch, "/api/projects/"+created.ID, map[string]any{"labelIds": []string{"label_type_defect"}}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodPatch, "/api/issues/issue_33", map[string]any{"labelIds": []string{"label_product"}}, http.StatusBadRequest)

	resource := requestJSON[domain.ProjectResource](t, handler, http.MethodPost, "/api/projects/"+created.ID+"/resources", map[string]any{
		"type": "link", "title": "Launch brief", "url": "https://example.com/brief",
	}, http.StatusCreated)
	if resource.ID == "" || resource.ProjectID != created.ID || resource.Title != "Launch brief" {
		t.Fatalf("project resource create failed: %#v", resource)
	}
	resource = requestJSON[domain.ProjectResource](t, handler, http.MethodPatch, "/api/projects/"+created.ID+"/resources/"+resource.ID, map[string]any{
		"title": "Edited brief", "url": "https://example.com/edited", "pinnedTeamIds": []string{"team_cleantrack"},
	}, http.StatusOK)
	if resource.Title != "Edited brief" || resource.URL != "https://example.com/edited" || !slices.Equal(resource.PinnedTeamIDs, []string{"team_cleantrack"}) {
		t.Fatalf("project resource update failed: %#v", resource)
	}
	requestJSON[any](t, handler, http.MethodPatch, "/api/projects/"+created.ID+"/resources/"+resource.ID, map[string]any{"pinnedTeamIds": []string{"missing-team"}}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodDelete, "/api/projects/"+created.ID+"/resources/"+resource.ID, nil, http.StatusNoContent)

	milestone := requestJSON[domain.ProjectMilestone](t, handler, http.MethodPost, "/api/projects/"+created.ID+"/milestones", map[string]any{
		"name": "Public beta", "description": "Invite the first customer cohort.", "targetDate": "2026-08-30",
	}, http.StatusCreated)
	if milestone.ID == "" || milestone.ProjectID != created.ID || milestone.Description != "Invite the first customer cohort." || milestone.TargetDate == nil || *milestone.TargetDate != "2026-08-30" {
		t.Fatalf("project milestone create failed: %#v", milestone)
	}
	milestone = requestJSON[domain.ProjectMilestone](t, handler, http.MethodPatch, "/api/projects/"+created.ID+"/milestones/"+milestone.ID, map[string]any{
		"name": "General availability", "description": "Launch to every workspace.", "targetDate": "2026-09-15",
	}, http.StatusOK)
	if milestone.Name != "General availability" || milestone.Description != "Launch to every workspace." || milestone.TargetDate == nil || *milestone.TargetDate != "2026-09-15" {
		t.Fatalf("project milestone update failed: %#v", milestone)
	}
	milestoneIssue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{
		"title": "Milestone-scoped work", "projectId": created.ID, "projectMilestoneId": milestone.ID,
	}, http.StatusCreated)
	if milestoneIssue.ProjectMilestoneID == nil || *milestoneIssue.ProjectMilestoneID != milestone.ID {
		t.Fatalf("issue milestone assignment failed: %#v", milestoneIssue)
	}
	requestJSON[any](t, handler, http.MethodPatch, "/api/issues/issue_33", map[string]any{"projectMilestoneId": milestone.ID}, http.StatusBadRequest)
	secondMilestone := requestJSON[domain.ProjectMilestone](t, handler, http.MethodPost, "/api/projects/"+created.ID+"/milestones", map[string]any{
		"name": "Public launch", "targetDate": "2026-10-01",
	}, http.StatusCreated)
	reordered := requestJSON[[]domain.ProjectMilestone](t, handler, http.MethodPost, "/api/projects/"+created.ID+"/milestones/reorder", map[string]any{
		"ids": []string{secondMilestone.ID, milestone.ID},
	}, http.StatusOK)
	if len(reordered) != 2 || reordered[0].ID != secondMilestone.ID || reordered[1].ID != milestone.ID {
		t.Fatalf("project milestone reorder failed: %#v", reordered)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/projects/"+created.ID+"/milestones/"+milestone.ID, nil, http.StatusNoContent)
	bootstrapAfterMilestoneDelete := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if assigned := findIssue(t, bootstrapAfterMilestoneDelete.Issues, milestoneIssue.ID).ProjectMilestoneID; assigned != nil {
		t.Fatalf("deleted milestone remained assigned to issue: %q", *assigned)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/projects/"+created.ID+"/milestones/"+secondMilestone.ID, nil, http.StatusNoContent)

	comment := requestJSON[domain.Comment](t, handler, http.MethodPost, "/api/projects/"+created.ID+"/comments", map[string]any{
		"body": "Project-level discussion persists.",
	}, http.StatusCreated)
	if comment.ID == "" || comment.Body != "Project-level discussion persists." || comment.User.ID != "usr_zheng" {
		t.Fatalf("project comment create failed: %#v", comment)
	}
	bootstrapAfterDetails := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	for _, persisted := range bootstrapAfterDetails.Projects {
		if persisted.ID == created.ID && (len(persisted.Resources) != 0 || len(persisted.Milestones) != 0 || len(persisted.Comments) != 1) {
			t.Fatalf("project detail collections did not persist: %#v", persisted)
		}
	}

	projectUpdate := requestJSON[domain.ProjectUpdate](t, handler, http.MethodPost, "/api/projects/"+created.ID+"/updates", map[string]any{
		"body": "The API-backed update stream persists.", "health": "onTrack",
	}, http.StatusCreated)
	if projectUpdate.ID == "" || projectUpdate.ProjectID != created.ID || projectUpdate.Health != "onTrack" {
		t.Fatalf("project update create failed: %#v", projectUpdate)
	}
	projectUpdate = requestJSON[domain.ProjectUpdate](t, handler, http.MethodPatch, "/api/projects/"+created.ID+"/updates/"+projectUpdate.ID, map[string]any{
		"body": "The persisted update was edited.", "health": "offTrack",
	}, http.StatusOK)
	if projectUpdate.Body != "The persisted update was edited." || projectUpdate.Health != "offTrack" || projectUpdate.EditedAt == nil {
		t.Fatalf("project update edit failed: %#v", projectUpdate)
	}
	projectUpdate = requestJSON[domain.ProjectUpdate](t, handler, http.MethodPost, "/api/projects/"+created.ID+"/updates/"+projectUpdate.ID+"/comments", map[string]any{
		"body": "Update comment",
	}, http.StatusCreated)
	if len(projectUpdate.Comments) != 1 || projectUpdate.Comments[0].Body != "Update comment" {
		t.Fatalf("project update comment failed: %#v", projectUpdate)
	}
	projectUpdate = requestJSON[domain.ProjectUpdate](t, handler, http.MethodPost, "/api/projects/"+created.ID+"/updates/"+projectUpdate.ID+"/reactions", map[string]any{
		"emoji": "👍",
	}, http.StatusOK)
	if !slices.Equal(projectUpdate.Reactions["👍"], []string{"usr_zheng"}) {
		t.Fatalf("project update reaction failed: %#v", projectUpdate)
	}
	bootstrapAfterUpdate := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(bootstrapAfterUpdate.ProjectStatuses) != 5 {
		t.Fatalf("project status dictionary missing: %#v", bootstrapAfterUpdate.ProjectStatuses)
	}
	if len(bootstrapAfterUpdate.ProjectUpdates[created.ID]) != 1 {
		t.Fatalf("project update was not persisted: %#v", bootstrapAfterUpdate.ProjectUpdates)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/projects/"+created.ID+"/updates/"+projectUpdate.ID, nil, http.StatusNoContent)
	bootstrapAfterDelete := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	for _, project := range bootstrapAfterDelete.Projects {
		if project.ID == created.ID && project.Health != "noUpdate" {
			t.Fatalf("project health after deleting last update = %s", project.Health)
		}
	}

	viewDefault := requestJSON[map[string]any](t, handler, http.MethodPut, "/api/workspace/project-display-default", map[string]any{
		"display": map[string]any{"layout": "board", "grouping": "Status", "properties": []string{"Summary", "Priority"}},
	}, http.StatusOK)
	if viewDefault["layout"] != "board" || viewDefault["grouping"] != "Status" {
		t.Fatalf("project display default = %#v", viewDefault)
	}
	bootstrapWithDefault := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	var persistedDefault map[string]any
	if err := json.Unmarshal(bootstrapWithDefault.ProjectDisplayDefault, &persistedDefault); err != nil || persistedDefault["layout"] != "board" {
		t.Fatalf("project display default did not persist: %s (%v)", bootstrapWithDefault.ProjectDisplayDefault, err)
	}

	requestJSON[any](t, handler, http.MethodDelete, "/api/projects/"+created.ID, nil, http.StatusNoContent)
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	for _, project := range bootstrap.Projects {
		if project.ID == created.ID {
			t.Fatalf("deleted project remains: %s", project.ID)
		}
	}
	events := requestJSON[[]domain.DomainEvent](t, handler, http.MethodGet, "/api/events?aggregateId="+created.ID, nil, http.StatusOK)
	expectedEvents := []string{"project.created", "project.updated", "project.updated", "project.reminder_created", "project.resource_created", "project.resource_updated", "project.resource_deleted", "project.milestone_created", "project.milestone_updated", "project.milestone_created", "project.milestones_reordered", "project.milestone_deleted", "project.milestone_deleted", "project.commented", "project.update_created", "project.update_updated", "project.update_commented", "project.update_reaction_toggled", "project.update_deleted", "project.deleted"}
	if len(events) != len(expectedEvents) {
		t.Fatalf("project events = %#v", events)
	}
	for index, eventType := range expectedEvents {
		if events[index].Type != eventType {
			t.Fatalf("project event %d = %q, want %q", index, events[index].Type, eventType)
		}
	}
}

func TestCustomerLifecycle(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})

	created := requestJSON[domain.Customer](t, handler, http.MethodPost, "/api/customers", map[string]any{
		"name": "Customer API test", "ownerId": "usr_zheng", "status": "active", "tier": "Enterprise",
		"annualRevenue": 250000, "size": 120, "domains": []string{"example.com", "example.org"},
	}, http.StatusCreated)
	if created.ID == "" || created.Name != "Customer API test" || created.OwnerID != "usr_zheng" || created.Status != "active" || created.Tier != "Enterprise" || created.AnnualRevenue != 250000 || created.Size != 120 || !slices.Equal(created.Domains, []string{"example.com", "example.org"}) {
		t.Fatalf("customer create failed: %#v", created)
	}

	updated := requestJSON[domain.Customer](t, handler, http.MethodPatch, "/api/customers/"+created.ID, map[string]any{
		"name": "Updated customer", "status": "inactive", "tier": "Mid-market", "domains": []string{"updated.example.com"},
	}, http.StatusOK)
	if updated.Name != "Updated customer" || updated.Status != "inactive" || updated.Tier != "Mid-market" || !slices.Equal(updated.Domains, []string{"updated.example.com"}) {
		t.Fatalf("customer update failed: %#v", updated)
	}

	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if !slices.ContainsFunc(bootstrap.Customers, func(customer domain.Customer) bool {
		return customer.ID == created.ID && customer.Name == updated.Name && customer.Status == updated.Status
	}) {
		t.Fatalf("customer did not survive bootstrap round trip: %#v", bootstrap.Customers)
	}

	requestJSON[any](t, handler, http.MethodDelete, "/api/customers/"+created.ID, nil, http.StatusNoContent)
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if slices.ContainsFunc(bootstrap.Customers, func(customer domain.Customer) bool { return customer.ID == created.ID }) {
		t.Fatalf("deleted customer remains: %#v", bootstrap.Customers)
	}
	events := requestJSON[[]domain.DomainEvent](t, handler, http.MethodGet, "/api/events?aggregateId="+created.ID, nil, http.StatusOK)
	expected := []string{"customer.created", "customer.updated", "customer.deleted"}
	if len(events) != len(expected) {
		t.Fatalf("customer events = %#v", events)
	}
	for index, eventType := range expected {
		if events[index].Type != eventType {
			t.Fatalf("customer event %d = %q, want %q", index, events[index].Type, eventType)
		}
	}
}

func TestInitiativeLifecycle(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})

	created := requestJSON[domain.Initiative](t, handler, http.MethodPost, "/api/initiatives", map[string]any{
		"name": "Initiative API test", "summary": "Initial", "status": "planned", "priority": 2,
		"ownerId": "usr_zheng", "targetDate": "2026-12-31", "projectIds": []string{"project_cruise"}, "leadTeamId": "team_cleantrack", "contributingTeamIds": []string{"team_cleantrack"},
	}, http.StatusCreated)
	if created.ID == "" || created.Status != "planned" || created.Priority != 2 || created.Owner == nil || created.LeadTeamID != "team_cleantrack" || !slices.Equal(created.ContributingTeamIDs, []string{"team_cleantrack"}) || !slices.Equal(created.ProjectIDs, []string{"project_cruise"}) {
		t.Fatalf("initiative create failed: %#v", created)
	}
	updated := requestJSON[domain.Initiative](t, handler, http.MethodPatch, "/api/initiatives/"+created.ID, map[string]any{
		"name": "Updated initiative", "description": "Persistent description", "status": "active", "health": "atRisk",
		"labelIds": []string{"label_type_defect"}, "favorite": true, "subscribed": true,
		"notificationRules": map[string]any{"descriptionChanges": true, "newUpdate": false, "allProjectUpdates": true},
		"updateSchedule":    map[string]any{"cadence": "weekly", "weekday": 2, "timeRange": "09:00-12:00"},
	}, http.StatusOK)
	if updated.Name != "Updated initiative" || updated.Status != "active" || updated.Health != "atRisk" || !updated.Favorite || !updated.Subscribed || len(updated.DescriptionHistory) != 1 || updated.NotificationRules.NewUpdate || updated.UpdateSchedule.Cadence != "weekly" || !slices.Equal(updated.LabelIDs, []string{"label_type_defect"}) {
		t.Fatalf("initiative update failed: %#v", updated)
	}
	resource := requestJSON[domain.InitiativeResource](t, handler, http.MethodPost, "/api/initiatives/"+created.ID+"/resources", map[string]any{"type": "link", "title": "Strategy", "url": "https://example.com/strategy"}, http.StatusCreated)
	if resource.ID == "" || resource.InitiativeID != created.ID {
		t.Fatalf("initiative resource failed: %#v", resource)
	}
	resource = requestJSON[domain.InitiativeResource](t, handler, http.MethodPatch, "/api/initiatives/"+created.ID+"/resources/"+resource.ID, map[string]any{"title": "Updated strategy", "url": "https://example.com/updated-strategy"}, http.StatusOK)
	if resource.Title != "Updated strategy" || resource.URL != "https://example.com/updated-strategy" {
		t.Fatalf("initiative resource update failed: %#v", resource)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/initiatives/"+created.ID+"/resources/"+resource.ID, nil, http.StatusNoContent)
	comment := requestJSON[domain.Comment](t, handler, http.MethodPost, "/api/initiatives/"+created.ID+"/comments", map[string]string{"body": "Persistent initiative comment"}, http.StatusCreated)
	if comment.Body != "Persistent initiative comment" {
		t.Fatalf("initiative comment failed: %#v", comment)
	}
	comment = requestJSON[domain.Comment](t, handler, http.MethodPatch, "/api/initiatives/"+created.ID+"/comments/"+comment.ID, map[string]string{"body": "Edited initiative comment"}, http.StatusOK)
	if comment.Body != "Edited initiative comment" || comment.EditedAt == nil {
		t.Fatalf("initiative comment update failed: %#v", comment)
	}
	comment = requestJSON[domain.Comment](t, handler, http.MethodPost, "/api/initiatives/"+created.ID+"/comments/"+comment.ID+"/reactions", map[string]string{"emoji": "thumbs-up"}, http.StatusOK)
	if !slices.Equal(comment.Reactions["thumbs-up"], []string{"usr_zheng"}) {
		t.Fatalf("initiative comment reaction failed: %#v", comment)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/initiatives/"+created.ID+"/comments/"+comment.ID, nil, http.StatusNoContent)
	initiativeUpdate := requestJSON[domain.InitiativeUpdate](t, handler, http.MethodPost, "/api/initiatives/"+created.ID+"/updates", map[string]string{"body": "Delivery remains on plan", "health": "onTrack"}, http.StatusCreated)
	initiativeUpdate = requestJSON[domain.InitiativeUpdate](t, handler, http.MethodPatch, "/api/initiatives/"+created.ID+"/updates/"+initiativeUpdate.ID, map[string]string{"body": "Edited delivery update", "health": "atRisk"}, http.StatusOK)
	if initiativeUpdate.Body != "Edited delivery update" || initiativeUpdate.EditedAt == nil {
		t.Fatalf("initiative update edit failed: %#v", initiativeUpdate)
	}
	initiativeUpdate = requestJSON[domain.InitiativeUpdate](t, handler, http.MethodPost, "/api/initiatives/"+created.ID+"/updates/"+initiativeUpdate.ID+"/comments", map[string]string{"body": "Pulse initiative comment"}, http.StatusCreated)
	if len(initiativeUpdate.Comments) != 1 || initiativeUpdate.Comments[0].Body != "Pulse initiative comment" {
		t.Fatalf("initiative update comment failed: %#v", initiativeUpdate)
	}
	initiativeUpdate = requestJSON[domain.InitiativeUpdate](t, handler, http.MethodPost, "/api/initiatives/"+created.ID+"/updates/"+initiativeUpdate.ID+"/reactions", map[string]string{"emoji": "thumbs-up"}, http.StatusOK)
	if !slices.Equal(initiativeUpdate.Reactions["thumbs-up"], []string{"usr_zheng"}) {
		t.Fatalf("initiative update reaction failed: %#v", initiativeUpdate)
	}
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(bootstrap.InitiativeUpdates[created.ID]) != 1 || len(bootstrap.InitiativeUpdates[created.ID][0].Comments) != 1 || !slices.Equal(bootstrap.InitiativeUpdates[created.ID][0].Reactions["thumbs-up"], []string{"usr_zheng"}) || !slices.ContainsFunc(bootstrap.Projects, func(project domain.Project) bool {
		return project.ID == "project_cruise" && slices.Contains(project.Initiatives, created.ID)
	}) {
		t.Fatalf("initiative bootstrap projection failed: %#v", bootstrap.InitiativeUpdates[created.ID])
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/initiatives/"+created.ID+"/updates/"+initiativeUpdate.ID, nil, http.StatusNoContent)
	reminder := requestJSON[domain.Notification](t, handler, http.MethodPost, "/api/initiatives/"+created.ID+"/reminders", map[string]string{"remindAt": time.Now().UTC().Add(time.Hour).Format(time.RFC3339)}, http.StatusCreated)
	if reminder.SourceType != "initiative" || reminder.SourceID != created.ID || reminder.SnoozedUntil == nil {
		t.Fatalf("initiative reminder failed: %#v", reminder)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/initiatives/"+created.ID, nil, http.StatusNoContent)
	deletedBootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	trashIndex := slices.IndexFunc(deletedBootstrap.Trash, func(entry domain.TrashEntry) bool {
		return entry.ResourceType == "initiative" && entry.ResourceID == created.ID
	})
	if trashIndex < 0 {
		t.Fatalf("deleted initiative was not retained in trash: %#v", deletedBootstrap.Trash)
	}
	restored := requestJSON[domain.Initiative](t, handler, http.MethodPost, "/api/trash/"+deletedBootstrap.Trash[trashIndex].ID+"/restore", nil, http.StatusOK)
	if restored.ID != created.ID || restored.LeadTeamID != "team_cleantrack" {
		t.Fatalf("initiative restore failed: %#v", restored)
	}
	events := requestJSON[[]domain.DomainEvent](t, handler, http.MethodGet, "/api/events?aggregateId="+created.ID, nil, http.StatusOK)
	expected := []string{"initiative.created", "initiative.updated", "initiative.resource_created", "initiative.resource_updated", "initiative.resource_deleted", "initiative.commented", "initiative.comment_updated", "initiative.comment_reaction_toggled", "initiative.comment_deleted", "initiative.update_created", "initiative.update_updated", "initiative.update_commented", "initiative.update_reaction_toggled", "initiative.update_deleted", "initiative.reminder_created", "initiative.deleted"}
	if len(events) != len(expected) {
		t.Fatalf("initiative events = %#v", events)
	}
	for index, eventType := range expected {
		if events[index].Type != eventType {
			t.Fatalf("initiative event %d = %q, want %q", index, events[index].Type, eventType)
		}
	}
}

func TestIssueBoardOrderAndSavedViewLifecycle(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})

	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	issue := bootstrap.Issues[0]
	updatedIssue := requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+issue.ID, map[string]any{
		"stateId": "state_progress", "sortOrder": 14.5,
	}, http.StatusOK)
	if updatedIssue.State.ID != "state_progress" || updatedIssue.SortOrder != 14.5 {
		t.Fatalf("board move = state %q order %v", updatedIssue.State.ID, updatedIssue.SortOrder)
	}
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	persistedIssue := findIssue(t, bootstrap.Issues, issue.ID)
	if persistedIssue.State.ID != "state_progress" || persistedIssue.SortOrder != 14.5 {
		t.Fatalf("persisted board move = state %q order %v", persistedIssue.State.ID, persistedIssue.SortOrder)
	}

	created := requestJSON[domain.SavedView](t, handler, http.MethodPost, "/api/views", map[string]any{
		"name": "Board triage", "description": "Urgent work", "resource": "projects", "scope": "team", "teamId": "team_cleantrack", "view": "active",
		"icon": "Rocket", "color": "#26b5ce",
		"filters":  []any{map[string]any{"field": "priority", "values": []any{map[string]any{"id": "1", "label": "Urgent"}}}},
		"display":  map[string]any{"layout": "board", "grouping": "status", "properties": []string{"id", "priority"}},
		"insights": map[string]any{"measure": "issueCount", "slice": "status", "segment": "priority"},
	}, http.StatusCreated)
	if created.ID == "" || created.Resource != "projects" || created.Scope != "team" || created.View != "active" || created.Icon != "Rocket" || created.Color != "#26b5ce" || string(created.Filters) == "[]" || string(created.Display) == "{}" || string(created.Insights) == "{}" {
		t.Fatalf("saved view create = %#v", created)
	}
	initiativeView := requestJSON[domain.SavedView](t, handler, http.MethodPost, "/api/views", map[string]any{
		"name": "Initiative roadmap", "resource": "initiativeProjects", "scope": "workspace", "view": "all",
		"filters": []any{map[string]any{"field": "initiative", "operator": "is", "values": []string{"initiative_1"}}},
		"display": map[string]any{"zoom": "Quarter", "query": "mobile", "properties": map[string]bool{"health": true, "priority": false, "lead": true}},
	}, http.StatusCreated)
	if initiativeView.Resource != "initiativeProjects" || string(initiativeView.Filters) == "[]" || string(initiativeView.Display) == "{}" {
		t.Fatalf("initiative saved view create = %#v", initiativeView)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/views/"+initiativeView.ID, nil, http.StatusNoContent)
	updatedView := requestJSON[domain.SavedView](t, handler, http.MethodPatch, "/api/views/"+created.ID, map[string]any{
		"name": "Board triage updated", "description": "Current urgent work", "scope": "personal", "teamId": "", "ownerId": "usr_jiaozongben", "favorite": true, "subscribed": true,
		"icon": "Face", "color": "#eb5757", "insights": map[string]any{"measure": "issueCount", "slice": "project", "segment": "none"},
	}, http.StatusOK)
	if updatedView.Name != "Board triage updated" || updatedView.Description != "Current urgent work" || updatedView.Icon != "Face" || updatedView.Color != "#eb5757" || updatedView.Scope != "personal" || updatedView.TeamID != "" || updatedView.OwnerID != "usr_jiaozongben" || !updatedView.Favorite || !updatedView.Subscribed || string(updatedView.Display) != string(created.Display) || string(updatedView.Insights) == string(created.Insights) {
		t.Fatalf("saved view update = %#v", updatedView)
	}
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(bootstrap.SavedViews) != 1 || bootstrap.SavedViews[0].Name != updatedView.Name {
		t.Fatalf("saved view bootstrap = %#v", bootstrap.SavedViews)
	}
	requestJSON[domain.Favorite](t, handler, http.MethodPut, "/api/favorites/view/"+created.ID, nil, http.StatusOK)
	requestJSON[domain.Subscription](t, handler, http.MethodPut, "/api/subscriptions/view/"+created.ID, nil, http.StatusOK)
	requestJSON[any](t, handler, http.MethodDelete, "/api/views/"+created.ID, nil, http.StatusNoContent)
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(bootstrap.SavedViews) != 0 {
		t.Fatalf("deleted view remains = %#v", bootstrap.SavedViews)
	}
	if slices.ContainsFunc(bootstrap.Favorites, func(item domain.Favorite) bool { return item.ResourceType == "view" && item.ResourceID == created.ID }) {
		t.Fatalf("deleted view favorite remains = %#v", bootstrap.Favorites)
	}
	if slices.ContainsFunc(bootstrap.Subscriptions, func(item domain.Subscription) bool {
		return item.ResourceType == "view" && item.ResourceID == created.ID
	}) {
		t.Fatalf("deleted view subscription remains = %#v", bootstrap.Subscriptions)
	}
	events := requestJSON[[]domain.DomainEvent](t, handler, http.MethodGet, "/api/events?aggregateId="+created.ID, nil, http.StatusOK)
	if len(events) != 5 || events[0].Type != "view.created" || events[1].Type != "view.updated" || events[2].Type != "favorite.added" || events[3].Type != "subscription.added" || events[4].Type != "view.deleted" {
		t.Fatalf("saved view events = %#v", events)
	}
}

func TestInboxNotificationLifecycle(t *testing.T) {
	repository, err := store.OpenSQLite(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})

	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(bootstrap.Notifications) == 0 {
		t.Fatal("bootstrap did not project inbox notifications")
	}
	notification := bootstrap.Notifications[0]
	if notification.ID == "" || notification.IssueID == "" || notification.SourceID == "" || notification.SourceType == "" {
		t.Fatalf("notification source mapping incomplete: %#v", notification)
	}

	listed := requestJSON[domain.NotificationList](t, handler, http.MethodGet, "/api/notifications", nil, http.StatusOK)
	if len(listed.Notifications) != len(bootstrap.Notifications) || listed.UnreadCount != len(bootstrap.Notifications) {
		t.Fatalf("default inbox list = %#v", listed)
	}

	updated := requestJSON[domain.Notification](t, handler, http.MethodPatch, "/api/notifications/"+notification.ID, map[string]any{"read": true}, http.StatusOK)
	if updated.ReadAt == nil {
		t.Fatalf("notification was not marked read: %#v", updated)
	}
	updated = requestJSON[domain.Notification](t, handler, http.MethodPatch, "/api/notifications/"+notification.ID, map[string]any{"favorite": true}, http.StatusOK)
	if !updated.Favorite || updated.FavoritedAt == nil {
		t.Fatalf("notification was not favorited: %#v", updated)
	}

	snoozedUntil := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	updated = requestJSON[domain.Notification](t, handler, http.MethodPatch, "/api/notifications/"+notification.ID, map[string]any{"snoozedUntil": snoozedUntil}, http.StatusOK)
	if updated.SnoozedUntil == nil {
		t.Fatalf("notification was not snoozed: %#v", updated)
	}
	listed = requestJSON[domain.NotificationList](t, handler, http.MethodGet, "/api/notifications", nil, http.StatusOK)
	if notificationInList(listed.Notifications, notification.ID) {
		t.Fatalf("snoozed notification remained in default list: %#v", listed)
	}
	listed = requestJSON[domain.NotificationList](t, handler, http.MethodGet, "/api/notifications?includeSnoozed=true&read=true", nil, http.StatusOK)
	if !notificationInList(listed.Notifications, notification.ID) {
		t.Fatalf("snoozed notification missing with includeSnoozed: %#v", listed)
	}
	updated = requestJSON[domain.Notification](t, handler, http.MethodPatch, "/api/notifications/"+notification.ID, map[string]any{"snoozedUntil": nil}, http.StatusOK)
	if updated.SnoozedUntil != nil {
		t.Fatalf("notification snooze was not cleared: %#v", updated)
	}

	updated = requestJSON[domain.Notification](t, handler, http.MethodPatch, "/api/notifications/"+notification.ID, map[string]any{"archived": true}, http.StatusOK)
	if updated.ArchivedAt == nil {
		t.Fatalf("notification was not archived: %#v", updated)
	}
	listed = requestJSON[domain.NotificationList](t, handler, http.MethodGet, "/api/notifications", nil, http.StatusOK)
	if notificationInList(listed.Notifications, notification.ID) {
		t.Fatalf("archived notification remained in default list: %#v", listed)
	}
	listed = requestJSON[domain.NotificationList](t, handler, http.MethodGet, "/api/notifications?includeArchived=true", nil, http.StatusOK)
	if !notificationInList(listed.Notifications, notification.ID) {
		t.Fatalf("archived notification missing with includeArchived: %#v", listed)
	}

	updated = requestJSON[domain.Notification](t, handler, http.MethodPatch, "/api/notifications/"+notification.ID, map[string]any{"deleted": true}, http.StatusOK)
	if updated.DeletedAt == nil {
		t.Fatalf("notification was not deleted: %#v", updated)
	}
	listed = requestJSON[domain.NotificationList](t, handler, http.MethodGet, "/api/notifications?includeArchived=true&includeDeleted=true", nil, http.StatusOK)
	if !notificationInList(listed.Notifications, notification.ID) {
		t.Fatalf("deleted notification missing with includeDeleted: %#v", listed)
	}

	events := requestJSON[[]domain.DomainEvent](t, handler, http.MethodGet, "/api/events?aggregateId="+notification.ID, nil, http.StatusOK)
	if len(events) != 6 || events[0].Type != "notification.read" || events[1].Type != "notification.favorited" || events[2].Type != "notification.snoozed" || events[3].Type != "notification.unsnoozed" || events[4].Type != "notification.archived" || events[5].Type != "notification.deleted" {
		t.Fatalf("notification events = %#v", events)
	}
}

func requestJSON[T any](t *testing.T, handler http.Handler, method, path string, input any, wantStatus int) T {
	t.Helper()
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, body)
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != wantStatus {
		t.Fatalf("%s %s status %d, want %d: %s", method, path, recorder.Code, wantStatus, recorder.Body.String())
	}
	var result T
	if recorder.Code != http.StatusNoContent {
		if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func upload(t *testing.T, handler http.Handler, issueID, name string, contents []byte) domain.Attachment {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(contents); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/issues/"+issueID+"/attachments", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("upload status %d: %s", recorder.Code, recorder.Body.String())
	}
	var attachment domain.Attachment
	if err := json.Unmarshal(recorder.Body.Bytes(), &attachment); err != nil {
		t.Fatal(err)
	}
	return attachment
}

func findIssue(t *testing.T, issues []domain.Issue, id string) domain.Issue {
	t.Helper()
	for _, issue := range issues {
		if issue.ID == id {
			return issue
		}
	}
	t.Fatalf("issue %s not found", id)
	return domain.Issue{}
}

func notificationInList(notifications []domain.Notification, id string) bool {
	for _, notification := range notifications {
		if notification.ID == id {
			return true
		}
	}
	return false
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
