package main

import (
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func resourcePreferenceRequest(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, "/api/favorites/") || strings.HasPrefix(r.URL.Path, "/api/subscriptions/") || embeddedFavoriteRequest(r)
}

func embeddedFavoriteRequest(r *http.Request) bool {
	if r.Method != "PATCH" {
		return false
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 3 || !slices.Contains([]string{"cycles", "initiatives", "reviews", "documents"}, parts[1]) {
		return false
	}
	var fields map[string]json.RawMessage
	if !peekRequestJSON(r, &fields) || len(fields) != 1 {
		return false
	}
	var value *bool
	return json.Unmarshal(fields["favorite"], &value) == nil && value != nil
}

func favoriteMutationEvent(fallback string, input any) string {
	raw, err := json.Marshal(input)
	var fields map[string]json.RawMessage
	if err != nil || json.Unmarshal(raw, &fields) != nil || len(fields) != 1 {
		return fallback
	}
	var favorite *bool
	if json.Unmarshal(fields["favorite"], &favorite) != nil || favorite == nil {
		return fallback
	}
	if *favorite {
		return "favorite.added"
	}
	return "favorite.removed"
}

func (s *server) preferenceReviewVisible(r *http.Request, id string) bool {
	data, query, err := s.issueRecordsQuery(r)
	if err != nil {
		return false
	}
	// The general metadata projection omits reviews whose linked issues were
	// not loaded. Resolve just these links through the indexed access query.
	raw, ok := s.store.WorkspaceMetadata(data.Workspace.URLKey)
	if !ok {
		return false
	}
	for _, review := range raw.Reviews {
		if review.ID == id || review.SlugID == id {
			if len(review.IssueIDs) == 0 {
				return true
			}
			visible, err := s.store.VisibleIssueRecordIDs(r.Context(), query, review.IssueIDs)
			return err == nil && len(visible) > 0
		}
	}
	return false
}

// Issue existence/visibility is checked by primary key before the metadata
// transaction. Never attach the issue to its snapshot: a non-nil Issues slice
// signals a complete collection to the compatibility persistence layer.
func (s *server) checkPreferenceIssue(r *http.Request) error {
	if r.PathValue("type") != "issue" {
		return nil
	}
	_, query, err := s.issueRecordsQuery(r)
	if err != nil {
		return err
	}
	visible, err := s.store.VisibleIssueRecordIDs(r.Context(), query, []string{r.PathValue("id")})
	if err != nil {
		return err
	}
	if !visible[r.PathValue("id")] {
		return errNotFound
	}
	return nil
}

func (s *server) preferenceMetadata(r *http.Request) (domain.Bootstrap, error) {
	key := workspaceKey(r)
	if s.authDisabled {
		data, ok := s.store.WorkspaceMetadata(key)
		if !ok {
			return data, store.ErrAuthForbidden
		}
		data.ViewerRole = "admin"
		return data, s.store.ApplyTeamDefaultFavorites(r.Context(), &data, data.Viewer.ID)
	}
	data, err := s.store.PagedWorkspaceMetadata(r.Context(), key, authUser(r).ID)
	if err != nil {
		return data, err
	}
	filterBootstrapForAPIKey(&data, r)
	return data, nil
}

func (s *server) resourcePreferences(w http.ResponseWriter, r *http.Request) {
	data, err := s.preferenceMetadata(r)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	if err := s.filterPreferenceIssueTeams(r, &data); err != nil {
		issueRecordsError(w, err)
		return
	}
	documentSubscriptions := map[string]bool{}
	for _, document := range data.Documents {
		documentSubscriptions[document.ID] = slices.Contains(document.SubscriberIDs, data.Viewer.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"favorites": data.Favorites, "favoriteFolders": data.FavoriteFolders, "subscriptions": data.Subscriptions, "documentSubscriptions": documentSubscriptions})
}

func (s *server) filterPreferenceIssueTeams(r *http.Request, data *domain.Bootstrap) error {
	key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey)
	if !ok || !apiKeyTeamRestrictionSelected(key) {
		return nil
	}
	ids := []string{}
	for _, item := range data.Favorites {
		if item.ResourceType == "issue" {
			ids = append(ids, item.ResourceID)
		}
	}
	for _, item := range data.Subscriptions {
		if item.ResourceType == "issue" {
			ids = append(ids, item.ResourceID)
		}
	}
	for _, item := range data.Documents {
		if item.IssueID != "" {
			ids = append(ids, item.IssueID)
		}
	}
	for _, item := range data.Notifications {
		if item.IssueID != "" {
			ids = append(ids, item.IssueID)
		}
	}
	for _, item := range data.Reviews {
		ids = append(ids, item.IssueIDs...)
	}
	for _, item := range data.Releases {
		ids = append(ids, item.IssueIDs...)
	}
	for _, item := range data.CustomerRequests {
		if item.IssueID != "" {
			ids = append(ids, item.IssueID)
		}
	}
	allowed := slices.Clone(key.TeamIDs)
	if allowed == nil {
		allowed = []string{}
	}
	visible := map[string]bool{}
	if len(ids) > 0 {
		_, access, err := s.store.IssueQueryAccess(r.Context(), data.Workspace.URLKey, credentialReadPrincipal(r.Context(), key))
		if err != nil {
			return err
		}
		visible, err = s.store.VisibleIssueRecordIDs(r.Context(), store.IssueRecordQuery{Workspace: data.Workspace.URLKey, Access: &access, AllowedTeamIDs: allowed}, ids)
		if err != nil {
			return err
		}
	}
	canRead := func(kind, id string) bool {
		if kind == "issue" {
			return visible[id]
		}
		return credentialResourceExists(data, kind, id)
	}
	data.Favorites = slices.DeleteFunc(data.Favorites, func(item domain.Favorite) bool { return !canRead(item.ResourceType, item.ResourceID) })
	data.Subscriptions = slices.DeleteFunc(data.Subscriptions, func(item domain.Subscription) bool { return !canRead(item.ResourceType, item.ResourceID) })
	for i := range data.Documents {
		if data.Documents[i].IssueID != "" && !visible[data.Documents[i].IssueID] {
			data.Documents[i].IssueID, data.Documents[i].IssueTeamID = "", ""
		}
	}
	data.Notifications = slices.DeleteFunc(data.Notifications, func(item domain.Notification) bool {
		if item.IssueID != "" {
			return !visible[item.IssueID]
		}
		if item.ProjectID != "" {
			return !canRead("project", item.ProjectID)
		}
		if item.ReviewID != "" {
			return !canRead("review", item.ReviewID)
		}
		switch item.SourceType {
		case "project", "initiative", "document", "cycle", "team":
			return !canRead(item.SourceType, item.SourceID)
		}
		return false
	})
	data.NotificationDeliveries = slices.DeleteFunc(data.NotificationDeliveries, func(item domain.NotificationDelivery) bool {
		return !slices.ContainsFunc(data.Notifications, func(notification domain.Notification) bool { return notification.ID == item.NotificationID })
	})
	data.Reviews = slices.DeleteFunc(data.Reviews, func(item domain.CodeReview) bool {
		return len(item.IssueIDs) > 0 && !slices.ContainsFunc(item.IssueIDs, func(id string) bool { return visible[id] })
	})
	for i := range data.Reviews {
		item := &data.Reviews[i]
		item.IssueIDs = slices.DeleteFunc(item.IssueIDs, func(id string) bool { return !visible[id] })
		item.IssueLinkTypes = maps.Clone(item.IssueLinkTypes)
		maps.DeleteFunc(item.IssueLinkTypes, func(id, _ string) bool { return !visible[id] })
	}
	for i := range data.Releases {
		data.Releases[i].IssueIDs = slices.DeleteFunc(data.Releases[i].IssueIDs, func(id string) bool { return !visible[id] })
	}
	data.CustomerRequests = slices.DeleteFunc(data.CustomerRequests, func(item domain.CustomerRequest) bool { return item.IssueID != "" && !visible[item.IssueID] })
	return nil
}
