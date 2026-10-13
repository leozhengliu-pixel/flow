package main

import (
	"context"
	"encoding/json"
	"log"
	"slices"
	"time"

	"flow/api/internal/domain"
)

// Deleting a resource detaches it from other records (issues leave a deleted
// project, sub-initiatives lose their parent, a customer's requests go with
// it). The capture functions below record those links on the trash entry
// before the delete runs, and the restore functions re-attach every link
// whose records still exist, so a restored resource comes back exactly as it
// was (the reference app's trash keeps these links too).

// appendTrashWithLinks trashes a resource like appendTrash and keeps links on
// the new entry.
func appendTrashWithLinks(data *domain.Bootstrap, resourceType, resourceID, title string, value any, links domain.TrashLinks) error {
	if err := appendTrash(data, resourceType, resourceID, title, value); err != nil {
		return err
	}
	if raw, err := json.Marshal(links); err == nil && string(raw) != "{}" {
		data.Trash[0].Links = &links
	}
	return nil
}

// captureResourcePreferences records the favorites, subscriptions and drafts
// that deleting the resource removes.
func captureResourcePreferences(data *domain.Bootstrap, kind, id string, links *domain.TrashLinks) {
	for _, item := range data.Favorites {
		if item.ResourceType == kind && item.ResourceID == id {
			links.Favorites = append(links.Favorites, item)
		}
	}
	for _, item := range data.Subscriptions {
		if item.ResourceType == kind && item.ResourceID == id {
			links.Subscriptions = append(links.Subscriptions, item)
		}
	}
	for _, item := range data.Drafts {
		if draftBelongsToResource(item, kind, id) {
			links.Drafts = append(links.Drafts, item)
		}
	}
}

// restoreResourcePreferences puts back recorded favorites, subscriptions and
// drafts that are not present again (a favorite whose folder is gone moves to
// the top level).
func restoreResourcePreferences(data *domain.Bootstrap, links *domain.TrashLinks) {
	for _, item := range links.Favorites {
		if slices.ContainsFunc(data.Favorites, func(existing domain.Favorite) bool {
			return existing.ID == item.ID || existing.UserID == item.UserID && existing.ResourceType == item.ResourceType && existing.ResourceID == item.ResourceID && existing.Tab == item.Tab
		}) {
			continue
		}
		if item.FolderID != "" && !slices.ContainsFunc(data.FavoriteFolders, func(folder domain.FavoriteFolder) bool { return folder.ID == item.FolderID }) {
			item.FolderID = ""
		}
		data.Favorites = append(data.Favorites, item)
	}
	for _, item := range links.Subscriptions {
		if !slices.ContainsFunc(data.Subscriptions, func(existing domain.Subscription) bool {
			return existing.ID == item.ID || existing.UserID == item.UserID && existing.ResourceType == item.ResourceType && existing.ResourceID == item.ResourceID
		}) {
			data.Subscriptions = append(data.Subscriptions, item)
		}
	}
	for _, item := range links.Drafts {
		if !slices.ContainsFunc(data.Drafts, func(existing domain.Draft) bool { return existing.ID == item.ID }) {
			data.Drafts = append(data.Drafts, item)
		}
	}
}

func issueIndexByID(data *domain.Bootstrap, id string) int {
	return slices.IndexFunc(data.Issues, func(item domain.Issue) bool { return item.ID == id })
}

// captureIssueLinks records what deleteIssue removes from other records.
func captureIssueLinks(data *domain.Bootstrap, id string) domain.TrashLinks {
	links := domain.TrashLinks{}
	captureResourcePreferences(data, "issue", id, &links)
	for _, issue := range data.Issues {
		if issue.ID == id {
			continue
		}
		if issue.ParentID != nil && *issue.ParentID == id {
			links.ChildIssueIDs = append(links.ChildIssueIDs, issue.ID)
		}
		for _, relation := range issue.Relations {
			if relation.IssueID == id || relation.RelatedIssueID == id {
				links.InverseRelations = append(links.InverseRelations, relation)
			}
		}
	}
	for _, release := range data.Releases {
		if slices.Contains(release.IssueIDs, id) {
			links.ReleaseIDs = append(links.ReleaseIDs, release.ID)
		}
	}
	for _, template := range data.ProjectTemplates {
		if slices.Contains(template.IssueIDs, id) {
			links.ProjectTemplateIDs = append(links.ProjectTemplateIDs, template.ID)
		}
	}
	for _, request := range data.CustomerRequests {
		if request.IssueID == id {
			links.CustomerRequestIDs = append(links.CustomerRequestIDs, request.ID)
		}
	}
	for _, document := range data.Documents {
		if document.IssueID == id {
			links.DocumentIDs = append(links.DocumentIDs, document.ID)
		}
	}
	for _, ask := range data.Asks {
		if ask.IssueID == id {
			links.Asks = append(links.Asks, ask)
		}
	}
	for _, sla := range data.IssueSLAs {
		if sla.IssueID == id {
			links.IssueSLAs = append(links.IssueSLAs, sla)
		}
	}
	for _, event := range data.SLAEvents {
		if event.IssueID == id {
			links.SLAEvents = append(links.SLAEvents, event)
		}
	}
	notificationIDs := map[string]bool{}
	for _, notification := range data.Notifications {
		if notification.IssueID == id || notification.SourceType == "issue" && notification.SourceID == id {
			links.Notifications = append(links.Notifications, notification)
			notificationIDs[notification.ID] = true
		}
	}
	for _, delivery := range data.NotificationDeliveries {
		if notificationIDs[delivery.NotificationID] {
			links.NotificationDeliveries = append(links.NotificationDeliveries, delivery)
		}
	}
	return links
}

// trashIssue moves one issue to the trash (deleteIssue's per-issue work):
// its record, content, notifications, asks, SLAs and preferences go into the
// entry, and its links on other records are detached (and recorded so a
// restore re-attaches them). deletedWith names the issue whose deletion
// cascaded to this sub-issue; children lists the root's sub-issues trashed
// before it. It returns the issue's collaboration document id.
func trashIssue(data *domain.Bootstrap, id, deletedWith string, children []string) (string, error) {
	documentID := "document_content_" + id
	index := issueIndexByID(data, id)
	if index < 0 {
		return documentID, errNotFound
	}
	removed := data.Issues[index]
	if removed.DocumentContent != nil && removed.DocumentContent.ID != "" {
		documentID = removed.DocumentContent.ID
	}
	links := captureIssueLinks(data, id)
	links.DeletedWithIssueID = deletedWith
	if children != nil {
		links.ChildIssueIDs = normalizedStrings(append(links.ChildIssueIDs, children...))
	}
	if err := appendTrashWithLinks(data, "issue", removed.ID, removed.Identifier+" "+removed.Title, deletedIssuePayload{Issue: removed, Comments: data.Comments[id], Activities: data.Activities[id]}, links); err != nil {
		return documentID, err
	}
	data.Issues = slices.Delete(data.Issues, index, index+1)
	data.IssueSuggestions = slices.DeleteFunc(data.IssueSuggestions, func(item domain.IssueSuggestion) bool { return item.IssueID == id })
	delete(data.Comments, id)
	delete(data.Activities, id)
	removedNotificationIDs := map[string]bool{}
	for _, notification := range data.Notifications {
		if notification.IssueID == id || (notification.SourceType == "issue" && notification.SourceID == id) {
			removedNotificationIDs[notification.ID] = true
		}
	}
	data.Notifications = slices.DeleteFunc(data.Notifications, func(item domain.Notification) bool {
		return removedNotificationIDs[item.ID]
	})
	data.NotificationDeliveries = slices.DeleteFunc(data.NotificationDeliveries, func(item domain.NotificationDelivery) bool {
		return removedNotificationIDs[item.NotificationID]
	})
	data.Asks = slices.DeleteFunc(data.Asks, func(item domain.Ask) bool { return item.IssueID == id })
	data.IssueSLAs = slices.DeleteFunc(data.IssueSLAs, func(item domain.IssueSLA) bool { return item.IssueID == id })
	data.SLAEvents = slices.DeleteFunc(data.SLAEvents, func(item domain.SLAEvent) bool { return item.IssueID == id })
	data.Drafts = slices.DeleteFunc(data.Drafts, func(item domain.Draft) bool {
		return draftBelongsToResource(item, "issue", id)
	})
	data.Favorites = slices.DeleteFunc(data.Favorites, func(item domain.Favorite) bool {
		return item.ResourceType == "issue" && item.ResourceID == id
	})
	data.Subscriptions = slices.DeleteFunc(data.Subscriptions, func(item domain.Subscription) bool {
		return item.ResourceType == "issue" && item.ResourceID == id
	})
	for i := range data.Releases {
		data.Releases[i].IssueIDs = removeString(data.Releases[i].IssueIDs, id)
	}
	for i := range data.ProjectTemplates {
		data.ProjectTemplates[i].IssueIDs = removeString(data.ProjectTemplates[i].IssueIDs, id)
	}
	for i := range data.CustomerRequests {
		if data.CustomerRequests[i].IssueID == id {
			data.CustomerRequests[i].IssueID = ""
		}
	}
	for i := range data.Documents {
		if data.Documents[i].IssueID == id {
			data.Documents[i].IssueID = ""
		}
	}
	for i := range data.Issues {
		data.Issues[i].SubIssueIDs = removeString(data.Issues[i].SubIssueIDs, id)
		data.Issues[i].Relations = slices.DeleteFunc(data.Issues[i].Relations, func(rel domain.IssueRelation) bool { return rel.RelatedIssueID == id || rel.IssueID == id })
		if data.Issues[i].ParentID != nil && *data.Issues[i].ParentID == id {
			data.Issues[i].ParentID = nil
		}
	}
	return documentID, nil
}

// issueDescendants lists the sub-issues of an issue, recursively, parents
// before their children.
func issueDescendants(data *domain.Bootstrap, id string) []string {
	result := []string{}
	seen := map[string]bool{id: true}
	queue := []string{id}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		for _, issue := range data.Issues {
			if issue.ParentID != nil && *issue.ParentID == parent && !seen[issue.ID] {
				seen[issue.ID] = true
				result = append(result, issue.ID)
				queue = append(queue, issue.ID)
			}
		}
	}
	return result
}

// trashedIssueScopeIDs lists the issues restoring a deleted issue touches:
// the issue, its parent, its sub-issues and the issues it relates to.
func trashedIssueScopeIDs(issue domain.Issue, links *domain.TrashLinks) []string {
	ids := []string{issue.ID}
	if issue.ParentID != nil {
		ids = append(ids, *issue.ParentID)
	}
	ids = append(ids, issue.SubIssueIDs...)
	for _, relation := range issue.Relations {
		ids = append(ids, relation.IssueID, relation.RelatedIssueID)
	}
	if links != nil {
		ids = append(ids, links.ChildIssueIDs...)
		for _, relation := range links.InverseRelations {
			ids = append(ids, relation.IssueID, relation.RelatedIssueID)
		}
	}
	return normalizedStrings(ids)
}

// restoreIssueReferences drops references a restored issue can no longer
// keep (a deleted project, milestone or cycle). An issue whose project is in
// the trash is recorded on that project's entry, so restoring the project
// brings the issue back into it.
func restoreIssueReferences(data *domain.Bootstrap, issue *domain.Issue) {
	if issue.Project != nil {
		project, err := fullProjectByID(data, issue.Project.ID)
		if err != nil {
			milestone := ""
			if issue.ProjectMilestoneID != nil {
				milestone = *issue.ProjectMilestoneID
			}
			for index := range data.Trash {
				entry := &data.Trash[index]
				if entry.ResourceType != "project" || entry.ResourceID != issue.Project.ID {
					continue
				}
				if entry.Links == nil {
					entry.Links = &domain.TrashLinks{}
				}
				if !slices.ContainsFunc(entry.Links.ProjectIssues, func(item domain.TrashProjectIssue) bool { return item.IssueID == issue.ID }) {
					entry.Links.ProjectIssues = append(entry.Links.ProjectIssues, domain.TrashProjectIssue{IssueID: issue.ID, MilestoneID: milestone})
				}
			}
			issue.Project, issue.ProjectMilestoneID = nil, nil
		} else {
			issue.Project = &domain.ProjectSummary{ID: project.ID, Name: project.Name, Icon: project.Icon, Color: project.Color}
			if issue.ProjectMilestoneID != nil && !slices.ContainsFunc(project.Milestones, func(item domain.ProjectMilestone) bool { return item.ID == *issue.ProjectMilestoneID }) {
				issue.ProjectMilestoneID = nil
			}
		}
	} else {
		issue.ProjectMilestoneID = nil
	}
	if issue.CycleID != nil && !slices.ContainsFunc(data.Cycles, func(item domain.Cycle) bool { return item.ID == *issue.CycleID }) {
		issue.CycleID = nil
	}
}

// restoreIssueLinks re-attaches a restored issue (already in data.Issues) to
// its parent, sub-issues, relations, releases, requests and documents, and
// puts back its asks, SLAs, notifications and preferences.
func restoreIssueLinks(data *domain.Bootstrap, id string, links *domain.TrashLinks) {
	index := issueIndexByID(data, id)
	if index < 0 {
		return
	}
	restoreIssueReferences(data, &data.Issues[index])
	if parentID := data.Issues[index].ParentID; parentID != nil {
		if parent := issueIndexByID(data, *parentID); parent >= 0 {
			data.Issues[parent].SubIssueIDs = appendUnique(data.Issues[parent].SubIssueIDs, id)
		} else {
			data.Issues[index].ParentID = nil
		}
	}
	children := slices.Clone(data.Issues[index].SubIssueIDs)
	if links != nil {
		children = append(children, links.ChildIssueIDs...)
	}
	subIssues := []string{}
	for _, childID := range normalizedStrings(children) {
		child := issueIndexByID(data, childID)
		if child < 0 {
			continue
		}
		if data.Issues[child].ParentID == nil {
			parent := id
			data.Issues[child].ParentID = &parent
		}
		if *data.Issues[child].ParentID == id {
			subIssues = append(subIssues, childID)
		}
	}
	data.Issues[index].SubIssueIDs = subIssues
	data.Issues[index].Relations = slices.DeleteFunc(data.Issues[index].Relations, func(relation domain.IssueRelation) bool {
		other := relation.RelatedIssueID
		if other == id {
			other = relation.IssueID
		}
		return issueIndexByID(data, other) < 0
	})
	if links == nil {
		return
	}
	for _, relation := range links.InverseRelations {
		other := relation.IssueID
		if other == id {
			other = relation.RelatedIssueID
		}
		target := issueIndexByID(data, other)
		if target >= 0 && !slices.ContainsFunc(data.Issues[target].Relations, func(item domain.IssueRelation) bool { return item.ID == relation.ID }) {
			data.Issues[target].Relations = append(data.Issues[target].Relations, relation)
		}
	}
	for i := range data.Releases {
		if slices.Contains(links.ReleaseIDs, data.Releases[i].ID) {
			data.Releases[i].IssueIDs = appendUnique(data.Releases[i].IssueIDs, id)
		}
	}
	for i := range data.ProjectTemplates {
		if slices.Contains(links.ProjectTemplateIDs, data.ProjectTemplates[i].ID) {
			data.ProjectTemplates[i].IssueIDs = appendUnique(data.ProjectTemplates[i].IssueIDs, id)
		}
	}
	for i := range data.CustomerRequests {
		if slices.Contains(links.CustomerRequestIDs, data.CustomerRequests[i].ID) && data.CustomerRequests[i].IssueID == "" {
			data.CustomerRequests[i].IssueID = id
		}
	}
	for i := range data.Documents {
		if slices.Contains(links.DocumentIDs, data.Documents[i].ID) && data.Documents[i].IssueID == "" {
			data.Documents[i].IssueID = id
		}
	}
	for _, ask := range links.Asks {
		if !slices.ContainsFunc(data.Asks, func(item domain.Ask) bool { return item.ID == ask.ID }) {
			data.Asks = append(data.Asks, ask)
		}
	}
	for _, sla := range links.IssueSLAs {
		if !slices.ContainsFunc(data.IssueSLAs, func(item domain.IssueSLA) bool { return item.ID == sla.ID }) {
			data.IssueSLAs = append(data.IssueSLAs, sla)
		}
	}
	for _, event := range links.SLAEvents {
		if !slices.ContainsFunc(data.SLAEvents, func(item domain.SLAEvent) bool { return item.ID == event.ID }) {
			data.SLAEvents = append(data.SLAEvents, event)
		}
	}
	for _, notification := range links.Notifications {
		if !slices.ContainsFunc(data.Notifications, func(item domain.Notification) bool { return item.ID == notification.ID }) {
			data.Notifications = append(data.Notifications, notification)
		}
	}
	for _, delivery := range links.NotificationDeliveries {
		if !slices.ContainsFunc(data.NotificationDeliveries, func(item domain.NotificationDelivery) bool { return item.ID == delivery.ID }) {
			data.NotificationDeliveries = append(data.NotificationDeliveries, delivery)
		}
	}
	restoreResourcePreferences(data, links)
}

// restoreTrashedIssue puts a trashed issue back with its content and
// re-attaches its links. The caller removes the trash entry.
func restoreTrashedIssue(data *domain.Bootstrap, entry domain.TrashEntry) (domain.Issue, error) {
	var value deletedIssuePayload
	if json.Unmarshal(entry.Payload, &value) != nil || value.Issue.ID == "" {
		return domain.Issue{}, errInvalid
	}
	if issueIndexByID(data, value.Issue.ID) >= 0 {
		return domain.Issue{}, errConflict
	}
	data.Issues = append([]domain.Issue{value.Issue}, data.Issues...)
	data.Comments[value.Issue.ID], data.Activities[value.Issue.ID] = value.Comments, value.Activities
	restoreIssueLinks(data, value.Issue.ID, entry.Links)
	return data.Issues[issueIndexByID(data, value.Issue.ID)], nil
}

// captureProjectLinks records the project's issues (with their milestones),
// the projects depending on it, its relations and preferences.
func captureProjectLinks(data *domain.Bootstrap, id string) domain.TrashLinks {
	links := domain.TrashLinks{}
	captureResourcePreferences(data, "project", id, &links)
	for _, issue := range data.Issues {
		if issue.Project == nil || issue.Project.ID != id {
			continue
		}
		item := domain.TrashProjectIssue{IssueID: issue.ID, Archived: issue.ArchivedAt == nil}
		if issue.ProjectMilestoneID != nil {
			item.MilestoneID = *issue.ProjectMilestoneID
		}
		links.ProjectIssues = append(links.ProjectIssues, item)
	}
	for _, project := range data.Projects {
		if project.ID != id && slices.Contains(project.DependencyIDs, id) {
			links.DependentProjectIDs = append(links.DependentProjectIDs, project.ID)
		}
	}
	for _, relation := range data.ProjectRelations {
		if relation.ProjectID == id || relation.RelatedProjectID == id {
			links.ProjectRelations = append(links.ProjectRelations, relation)
		}
	}
	return links
}

// restoreProjectLinks puts the project's issues back into it (and their
// milestones), un-archives the ones the delete archived, and restores
// dependencies, relations and preferences. Issues moved to another project
// since stay where they are.
func restoreProjectLinks(data *domain.Bootstrap, project domain.Project, links *domain.TrashLinks) {
	if links == nil {
		return
	}
	summary := domain.ProjectSummary{ID: project.ID, Name: project.Name, Icon: project.Icon, Color: project.Color}
	for _, item := range links.ProjectIssues {
		index := issueIndexByID(data, item.IssueID)
		if index < 0 || data.Issues[index].Project != nil {
			continue
		}
		value := summary
		issue := &data.Issues[index]
		issue.Project = &value
		issue.ProjectMilestoneID = nil
		if item.MilestoneID != "" && slices.ContainsFunc(project.Milestones, func(milestone domain.ProjectMilestone) bool { return milestone.ID == item.MilestoneID }) {
			milestone := item.MilestoneID
			issue.ProjectMilestoneID = &milestone
		}
		if item.Archived && issue.ArchivedAt != nil {
			issue.ArchivedAt, issue.UpdatedAt = nil, time.Now().UTC()
			appendActivity(data, issue.ID, "issue.updated", data.Viewer, map[string]string{"archived": "false", "project": project.ID})
		}
	}
	for i := range data.Projects {
		if slices.Contains(links.DependentProjectIDs, data.Projects[i].ID) {
			data.Projects[i].DependencyIDs = appendUnique(data.Projects[i].DependencyIDs, project.ID)
		}
	}
	for _, relation := range links.ProjectRelations {
		other := relation.RelatedProjectID
		if other == project.ID {
			other = relation.ProjectID
		}
		if _, err := fullProjectByID(data, other); err != nil {
			continue
		}
		if !slices.ContainsFunc(data.ProjectRelations, func(item domain.ProjectRelation) bool { return item.ID == relation.ID }) {
			data.ProjectRelations = append(data.ProjectRelations, relation)
		}
	}
	restoreResourcePreferences(data, links)
}

// captureInitiativeLinks records the projects listing the initiative, its
// sub-initiatives, relations and preferences.
func captureInitiativeLinks(data *domain.Bootstrap, id string) domain.TrashLinks {
	links := domain.TrashLinks{}
	captureResourcePreferences(data, "initiative", id, &links)
	for _, project := range data.Projects {
		if slices.Contains(project.Initiatives, id) {
			links.ProjectIDs = append(links.ProjectIDs, project.ID)
		}
	}
	for _, initiative := range data.Initiatives {
		if initiative.ID != id && slices.Contains(initiative.ParentInitiativeIDs, id) {
			links.ChildInitiativeIDs = append(links.ChildInitiativeIDs, initiative.ID)
		}
	}
	for _, relation := range data.InitiativeRelations {
		if relation.InitiativeID == id || relation.RelatedInitiativeID == id {
			links.InitiativeRelations = append(links.InitiativeRelations, relation)
		}
	}
	return links
}

// restoreInitiativeLinks re-links a restored initiative's projects,
// sub-initiatives and parent initiatives, relations and preferences.
func restoreInitiativeLinks(data *domain.Bootstrap, id string, links *domain.TrashLinks) {
	initiative, err := initiativeByID(data, id)
	if err != nil {
		return
	}
	exists := func(other string) bool {
		_, err := initiativeByID(data, other)
		return err == nil
	}
	initiative.ParentInitiativeIDs = slices.DeleteFunc(initiative.ParentInitiativeIDs, func(parent string) bool { return !exists(parent) })
	syncInitiativeProjects(data, id, nil, initiative.ProjectIDs)
	if links == nil {
		return
	}
	for i := range data.Projects {
		if slices.Contains(links.ProjectIDs, data.Projects[i].ID) {
			data.Projects[i].Initiatives = appendUnique(data.Projects[i].Initiatives, id)
		}
	}
	for i := range data.Initiatives {
		if slices.Contains(links.ChildInitiativeIDs, data.Initiatives[i].ID) {
			data.Initiatives[i].ParentInitiativeIDs = appendUnique(data.Initiatives[i].ParentInitiativeIDs, id)
		}
	}
	for _, relation := range links.InitiativeRelations {
		other := relation.RelatedInitiativeID
		if other == id {
			other = relation.InitiativeID
		}
		if exists(other) && !slices.ContainsFunc(data.InitiativeRelations, func(item domain.InitiativeRelation) bool { return item.ID == relation.ID }) {
			data.InitiativeRelations = append(data.InitiativeRelations, relation)
		}
	}
	restoreResourcePreferences(data, links)
}

// restoreCustomerLinks brings back the customer's requests and project links.
func restoreCustomerLinks(data *domain.Bootstrap, id string, links *domain.TrashLinks) {
	if links == nil {
		return
	}
	for _, request := range links.CustomerRequests {
		if !slices.ContainsFunc(data.CustomerRequests, func(item domain.CustomerRequest) bool { return item.ID == request.ID }) {
			data.CustomerRequests = append(data.CustomerRequests, request)
		}
	}
	for i := range data.Projects {
		if slices.Contains(links.ProjectIDs, data.Projects[i].ID) {
			data.Projects[i].Customers = appendUnique(data.Projects[i].Customers, id)
		}
	}
	restoreResourcePreferences(data, links)
}

// captureDocumentLinks records the project resource rows pointing at the
// document and its preferences.
func captureDocumentLinks(data *domain.Bootstrap, id string) domain.TrashLinks {
	links := domain.TrashLinks{}
	captureResourcePreferences(data, "document", id, &links)
	for _, project := range data.Projects {
		for _, resource := range project.Resources {
			if resource.ID == id {
				links.ProjectResources = append(links.ProjectResources, resource)
			}
		}
	}
	return links
}

// restoreDocumentLinks puts the document's project resource rows back as they
// were (pins included) before syncDocumentProjectResources reconciles them.
func restoreDocumentLinks(data *domain.Bootstrap, id string, links *domain.TrashLinks) {
	if links == nil {
		return
	}
	for _, resource := range links.ProjectResources {
		project, err := fullProjectByID(data, resource.ProjectID)
		if err != nil || slices.ContainsFunc(project.Resources, func(item domain.ProjectResource) bool { return item.ID == id }) {
			continue
		}
		project.Resources = append(project.Resources, resource)
	}
	restoreResourcePreferences(data, links)
}

// trashedLoopIDs lists the loops among trash entries, so purging or expiring
// them also drops their run history.
func trashedLoopIDs(entries []domain.TrashEntry, keep func(domain.TrashEntry) bool) []string {
	ids := []string{}
	for _, entry := range entries {
		if entry.ResourceType == "loop" && keep(entry) {
			ids = append(ids, entry.ResourceID)
		}
	}
	return ids
}

// purgeLoopRuns deletes the run history of loops whose trash entries were
// purged or expired.
func (s *server) purgeLoopRuns(ctx context.Context, workspace string, loopIDs []string) {
	for _, id := range loopIDs {
		if err := s.store.DeleteLoopRuns(ctx, workspace, id); err != nil {
			log.Printf("Loop run cleanup workspace=%s loop=%s: %v", workspace, id, err)
		}
	}
}
