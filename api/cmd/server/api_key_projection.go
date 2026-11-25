package main

import (
	"context"
	"maps"
	"net/http"
	"slices"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type credentialReadPrincipalKey struct{}

func credentialResourceExists(data *domain.Bootstrap, kind, id string) bool {
	if kind == "review" {
		return slices.ContainsFunc(data.Reviews, func(item domain.CodeReview) bool { return item.ID == id || item.SlugID == id })
	}
	return resourceExists(data, kind, id)
}

func credentialReadPrincipal(ctx context.Context, key domain.APIKey) string {
	if principal, ok := ctx.Value(credentialReadPrincipalKey{}).(string); ok && principal != "" {
		return principal
	}
	if user, ok := ctx.Value(authUserContextKey{}).(domain.User); ok && user.ID != "" {
		return user.ID
	}
	return key.SubjectID()
}

func redactPulseReferences(diff *domain.PulseDiff, projectVisible, initiativeVisible func(string) bool, partial bool) *domain.PulseDiff {
	if diff == nil {
		return nil
	}
	result := *diff
	refs := func(change *domain.PulseProjectsChange, visible func(string) bool) *domain.PulseProjectsChange {
		if change == nil {
			return nil
		}
		copy := *change
		copy.Added = slices.DeleteFunc(slices.Clone(copy.Added), func(ref domain.PulseRef) bool { return !visible(ref.ID) })
		copy.Removed = slices.DeleteFunc(slices.Clone(copy.Removed), func(ref domain.PulseRef) bool { return !visible(ref.ID) })
		return &copy
	}
	result.Projects, result.Initiatives = refs(result.Projects, projectVisible), refs(result.Initiatives, initiativeVisible)
	if partial {
		result.ProgressSince, result.Milestones = nil, nil
	}
	return &result
}

func (s *server) scopedNotificationUnreadCount(ctx context.Context, key domain.APIKey, data domain.Bootstrap, query store.NotificationQuery) (int, error) {
	query.Cursor, query.Limit, query.Read, query.SkipUnreadCount = "", 500, new(bool), true
	request := (&http.Request{}).WithContext(context.WithValue(ctx, apiKeyContextKey{}, key))
	count := 0
	for {
		page, err := s.store.QueryNotifications(ctx, query)
		if err != nil {
			return 0, err
		}
		view := data
		view.Notifications = page.Notifications
		if err := s.filterPreferenceIssueTeams(request, &view); err != nil {
			return 0, err
		}
		count += len(view.Notifications)
		if page.NextCursor == "" {
			return count, nil
		}
		query.Cursor = page.NextCursor
	}
}

func (s *server) queryAPIKeyNotifications(ctx context.Context, key domain.APIKey, data domain.Bootstrap, query store.NotificationQuery) (store.NotificationPage, error) {
	result := store.NotificationPage{Notifications: []domain.Notification{}}
	limit := query.Limit
	if limit <= 0 {
		limit = 100
	}
	limit = min(limit, 500)
	scanned := query
	scanned.Limit, scanned.SkipUnreadCount = 500, true
	request := (&http.Request{}).WithContext(context.WithValue(ctx, apiKeyContextKey{}, key))
	for {
		page, err := s.store.QueryNotifications(ctx, scanned)
		if err != nil {
			return result, err
		}
		view := data
		view.Notifications = page.Notifications
		if err := s.filterPreferenceIssueTeams(request, &view); err != nil {
			return result, err
		}
		result.Notifications = append(result.Notifications, view.Notifications...)
		if len(result.Notifications) > limit || page.NextCursor == "" {
			break
		}
		scanned.Cursor = page.NextCursor
	}
	if len(result.Notifications) > limit {
		result.Notifications = result.Notifications[:limit]
		result.HasMore = true
		result.NextCursor = store.NotificationCursor(result.Notifications[limit-1])
	}
	for i := range result.Notifications {
		store.RedactUserTeams(&result.Notifications[i].Actor, func(id string) bool { return slices.Contains(key.TeamIDs, id) })
	}
	var err error
	result.UnreadCount, err = s.scopedNotificationUnreadCount(ctx, key, data, query)
	return result, err
}

// Record filtering must precede reference filtering and count derivation.
// This runs only on credential projections; cookie projections keep their
// existing contract, including the paged metadata's zero issue counts.
func redactAPIKeyReferences(data *domain.Bootstrap, key domain.APIKey) {
	allowed := func(id string) bool { return slices.Contains(key.TeamIDs, id) }
	users := []*domain.User{&data.Viewer}
	for i := range data.Users {
		users = append(users, &data.Users[i])
	}
	for i := range data.Members {
		users = append(users, &data.Members[i].User)
	}
	for i := range data.Issues {
		item := &data.Issues[i]
		users = append(users, &item.Creator, item.Assignee, item.Delegate)
	}
	projects, initiatives, labels, cycles, issues := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, item := range data.Issues {
		issues[item.ID] = true
	}
	for _, item := range data.Projects {
		projects[item.ID] = true
	}
	data.Initiatives = slices.DeleteFunc(data.Initiatives, func(item domain.Initiative) bool {
		return item.LeadTeamID != "" && !allowed(item.LeadTeamID)
	})
	for _, item := range data.Initiatives {
		initiatives[item.ID] = true
	}
	for _, item := range data.Labels {
		labels[item.ID] = true
	}
	for _, item := range data.Cycles {
		cycles[item.ID] = true
	}
	for i := range data.Projects {
		item := &data.Projects[i]
		users = append(users, item.Creator, item.Lead)
		for j := range item.Comments {
			users = append(users, &item.Comments[j].User)
		}
		for j := range item.DescriptionRevisions {
			users = append(users, &item.DescriptionRevisions[j].Author)
		}
		item.DependencyIDs = slices.DeleteFunc(item.DependencyIDs, func(id string) bool { return !projects[id] })
		item.Initiatives = slices.DeleteFunc(item.Initiatives, func(id string) bool { return !initiatives[id] })
		item.LabelIDs = slices.DeleteFunc(item.LabelIDs, func(id string) bool { return !labels[id] })
		for j := range item.Resources {
			item.Resources[j].PinnedTeamIDs = slices.DeleteFunc(item.Resources[j].PinnedTeamIDs, func(id string) bool { return !allowed(id) })
		}
	}
	for i := range data.Initiatives {
		item := &data.Initiatives[i]
		users = append(users, &item.Creator, item.Owner)
		for j := range item.Comments {
			users = append(users, &item.Comments[j].User)
		}
		for j := range item.DescriptionHistory {
			users = append(users, &item.DescriptionHistory[j].Editor)
		}
		item.ProjectIDs = slices.DeleteFunc(item.ProjectIDs, func(id string) bool { return !projects[id] })
		item.ContributingTeamIDs = slices.DeleteFunc(item.ContributingTeamIDs, func(id string) bool { return !allowed(id) })
		item.ParentInitiativeIDs = slices.DeleteFunc(item.ParentInitiativeIDs, func(id string) bool { return !initiatives[id] })
		item.LabelIDs = slices.DeleteFunc(item.LabelIDs, func(id string) bool { return !labels[id] })
	}
	data.InitiativeRelations = slices.DeleteFunc(data.InitiativeRelations, func(item domain.InitiativeRelation) bool {
		return !initiatives[item.InitiativeID] || !initiatives[item.RelatedInitiativeID]
	})
	for id := range data.InitiativeUpdates {
		if !initiatives[id] {
			delete(data.InitiativeUpdates, id)
		}
	}
	for id := range data.ProjectUpdates {
		if !projects[id] {
			delete(data.ProjectUpdates, id)
		}
	}
	for id, items := range data.ProjectUpdates {
		items = slices.Clone(items)
		for i := range items {
			items[i].Diff = redactPulseReferences(items[i].Diff, func(id string) bool { return projects[id] }, func(id string) bool { return initiatives[id] }, false)
		}
		data.ProjectUpdates[id] = items
	}
	for id, items := range data.InitiativeUpdates {
		items = slices.Clone(items)
		for i := range items {
			items[i].Diff = redactPulseReferences(items[i].Diff, func(id string) bool { return projects[id] }, func(id string) bool { return initiatives[id] }, true)
		}
		data.InitiativeUpdates[id] = items
	}
	for i := range data.Documents {
		item := &data.Documents[i]
		users = append(users, &item.Creator)
		item.TeamIDs = slices.DeleteFunc(item.TeamIDs, func(id string) bool { return !allowed(id) })
		item.InheritedTeamIDs = slices.DeleteFunc(item.InheritedTeamIDs, func(id string) bool { return !allowed(id) })
		item.ProjectIDs = slices.DeleteFunc(item.ProjectIDs, func(id string) bool { return !projects[id] })
		if !allowed(item.IssueTeamID) {
			item.IssueID, item.IssueTeamID = "", ""
		}
		item.Permissions = slices.DeleteFunc(item.Permissions, func(permission domain.DocumentPermission) bool {
			return permission.SubjectType == "team" && !allowed(permission.SubjectID)
		})
	}
	for i := range data.Issues {
		item := &data.Issues[i]
		if item.CycleID != nil && !cycles[*item.CycleID] {
			item.CycleID = nil
		}
		if item.Project == nil {
			item.ProjectMilestoneID = nil
		}
		item.SuggestedLabelIDs = slices.DeleteFunc(item.SuggestedLabelIDs, func(id string) bool { return !labels[id] })
		item.SuggestedProjectIDs = slices.DeleteFunc(item.SuggestedProjectIDs, func(id string) bool { return !projects[id] })
		item.SuggestedTeamIDs = slices.DeleteFunc(item.SuggestedTeamIDs, func(id string) bool { return !allowed(id) })
		item.SuggestedDuplicateIDs = slices.DeleteFunc(item.SuggestedDuplicateIDs, func(id string) bool { return !issues[id] })
		item.SuggestedRelatedIDs = slices.DeleteFunc(item.SuggestedRelatedIDs, func(id string) bool { return !issues[id] })
	}
	for id, settings := range data.TeamSettings {
		if settings.ParentTeamID != "" && !allowed(settings.ParentTeamID) {
			settings.ParentTeamID = ""
			data.TeamSettings[id] = settings
		}
	}
	for i := range data.ProjectTemplates {
		data.ProjectTemplates[i].TeamIDs = slices.DeleteFunc(data.ProjectTemplates[i].TeamIDs, func(id string) bool { return !allowed(id) })
	}
	for id := range data.Activities {
		if !slices.ContainsFunc(data.Issues, func(item domain.Issue) bool { return item.ID == id }) {
			delete(data.Activities, id)
		}
	}
	for i := range data.Reviews {
		visible := func(id string) bool { return slices.Contains(data.Reviews[i].IssueIDs, id) }
		data.Reviews[i].IssueLinkTypes = maps.Clone(data.Reviews[i].IssueLinkTypes)
		maps.DeleteFunc(data.Reviews[i].IssueLinkTypes, func(id, _ string) bool { return !visible(id) })
	}
	for _, items := range data.Comments {
		for i := range items {
			users = append(users, &items[i].User)
		}
	}
	for _, items := range data.Activities {
		for i := range items {
			users = append(users, &items[i].Actor)
		}
	}
	for _, items := range data.ProjectUpdates {
		for i := range items {
			users = append(users, &items[i].User)
		}
	}
	for _, items := range data.InitiativeUpdates {
		for i := range items {
			users = append(users, &items[i].User)
		}
	}
	for i := range data.Notifications {
		users = append(users, &data.Notifications[i].Actor)
	}
	for _, user := range users {
		if user != nil {
			store.RedactUserTeams(user, allowed)
		}
	}
	store.RefreshResourceCounts(data)
}
