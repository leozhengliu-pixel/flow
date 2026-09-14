package main

import (
	"net/http"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

var defaultFavoriteTypes = []string{"issue", "project", "team", "document", "view"}

func (s *server) listTeamDefaultFavorites(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListTeamDefaultFavorites(r.Context(), workspaceKey(r), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load team defaults")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *server) replaceTeamDefaultFavorites(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Items []struct {
			ResourceType string `json:"resourceType"`
			ResourceID   string `json:"resourceId"`
		} `json:"items"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Items == nil {
		writeError(w, http.StatusBadRequest, "items must be an array of resource references")
		return
	}
	if len(input.Items) > 100 {
		writeError(w, http.StatusBadRequest, "up to 100 default favorites are allowed")
		return
	}
	teamID, workspace := r.PathValue("id"), workspaceKey(r)
	data, ok := s.store.WorkspaceMetadata(workspace)
	if !ok || !slices.ContainsFunc(data.Teams, func(team domain.Team) bool { return team.ID == teamID && team.RetiredAt == nil }) {
		writeError(w, http.StatusNotFound, "team not found")
		return
	}
	seen := map[string]bool{}
	items := make([]domain.TeamDefaultFavorite, 0, len(input.Items))
	for _, item := range input.Items {
		if !slices.Contains(defaultFavoriteTypes, item.ResourceType) || strings.TrimSpace(item.ResourceID) == "" {
			writeError(w, http.StatusBadRequest, "invalid default favorite resource")
			return
		}
		key := item.ResourceType + ":" + item.ResourceID
		if seen[key] {
			writeError(w, http.StatusBadRequest, "duplicate default favorite")
			return
		}
		seen[key] = true
		if item.ResourceType == "issue" {
			_, query, err := s.issueRecordsQuery(r)
			if err != nil {
				issueRecordsError(w, err)
				return
			}
			visible, err := s.store.VisibleIssueRecordIDs(r.Context(), query, []string{item.ResourceID})
			if err != nil || !visible[item.ResourceID] {
				writeError(w, http.StatusBadRequest, "issue is outside this workspace")
				return
			}
		} else if !resourceExists(&data, item.ResourceType, item.ResourceID) {
			writeError(w, http.StatusBadRequest, "resource is outside this workspace")
			return
		}
		items = append(items, domain.TeamDefaultFavorite{ResourceType: item.ResourceType, ResourceID: item.ResourceID})
	}
	if err := s.store.ReplaceTeamDefaultFavorites(r.Context(), workspace, teamID, items); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	items, err := s.store.ListTeamDefaultFavorites(r.Context(), workspace, teamID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not reload defaults")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
