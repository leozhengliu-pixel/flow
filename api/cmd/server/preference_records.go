package main

import (
	"encoding/json"
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

func (s *server) resourcePreferences(w http.ResponseWriter, r *http.Request) {
	data, _, err := s.pagedRealtimeMetadata(r)
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
	allowed := slices.Clone(key.TeamIDs)
	if allowed == nil {
		allowed = []string{}
	}
	visible, err := s.store.VisibleIssueRecordIDs(r.Context(), store.IssueRecordQuery{Workspace: data.Workspace.URLKey, AllowedTeamIDs: allowed}, ids)
	if err != nil {
		return err
	}
	canRead := func(kind, id string) bool {
		if kind == "issue" {
			return visible[id]
		}
		return resourceExists(data, kind, id)
	}
	data.Favorites = slices.DeleteFunc(data.Favorites, func(item domain.Favorite) bool { return !canRead(item.ResourceType, item.ResourceID) })
	data.Subscriptions = slices.DeleteFunc(data.Subscriptions, func(item domain.Subscription) bool { return !canRead(item.ResourceType, item.ResourceID) })
	return nil
}
