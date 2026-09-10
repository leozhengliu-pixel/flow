package main

import (
	"net/http"
	"slices"
	"sort"

	"flow/api/internal/domain"
)

type teamResourcesReadKey struct{}
type teamResourcesRead struct {
	TeamID    string                       `json:"-"`
	Sections  []domain.TeamResourceSection `json:"sections"`
	Resources []domain.TeamPinnedResource  `json:"resources"`
}

func projectTeamResources(data domain.Bootstrap, teamID string) teamResourcesRead {
	result := teamResourcesRead{TeamID: teamID, Sections: []domain.TeamResourceSection{}, Resources: []domain.TeamPinnedResource{}}
	for _, item := range data.TeamResourceSections {
		if item.TeamID == teamID {
			result.Sections = append(result.Sections, item)
		}
	}
	for _, item := range data.TeamPinnedResources {
		if item.TeamID == teamID {
			result.Resources = append(result.Resources, item)
		}
	}
	sort.SliceStable(result.Sections, func(i, j int) bool { return result.Sections[i].Position < result.Sections[j].Position })
	sort.SliceStable(result.Resources, func(i, j int) bool { return result.Resources[i].Position < result.Resources[j].Position })
	return result
}

func (s *server) listTeamResources(w http.ResponseWriter, r *http.Request) {
	teamID := r.PathValue("id")
	if result, ok := r.Context().Value(teamResourcesReadKey{}).(teamResourcesRead); ok && result.TeamID == teamID {
		writeJSON(w, http.StatusOK, result)
		return
	}
	if !s.authDisabled {
		// Requests without an explicit workspace can skip the outer workspace
		// check. Resolve and authorize the default workspace before reading it.
		if s.resourceAllowed(r, workspaceKey(r), authUser(r).ID) {
			if result, ok := r.Context().Value(teamResourcesReadKey{}).(teamResourcesRead); ok && result.TeamID == teamID {
				writeJSON(w, http.StatusOK, result)
				return
			}
		}
		writeError(w, http.StatusForbidden, "Team access required")
		return
	}
	// Development mode skips authorization middleware. Read metadata explicitly
	// so this endpoint can never fall back to a legacy collection bootstrap.
	data, ok := s.store.WorkspaceMetadata(workspaceKey(r))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	if !slices.ContainsFunc(data.Teams, func(team domain.Team) bool { return team.ID == teamID }) {
		writeError(w, http.StatusNotFound, "team not found")
		return
	}
	writeJSON(w, http.StatusOK, projectTeamResources(data, teamID))
}
