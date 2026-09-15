package main

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func projectListBootstrapRequested(r *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Flow-Projection")), "project-list") || r.URL.Query().Get("projection") == "project-list"
}

func (s *server) listProjectRecords(w http.ResponseWriter, r *http.Request) {
	metadata, access, err := s.requestIssueQueryAccess(r)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	filters := []store.ProjectDirectoryFilter{}
	if raw := r.URL.Query().Get("filter"); raw != "" && json.Unmarshal([]byte(raw), &filters) != nil {
		writeError(w, http.StatusBadRequest, "invalid project filter")
		return
	}
	allowedFilters := map[string]bool{"status": true, "priority": true, "lead": true, "members": true, "health": true, "dates": true, "milestones": true, "labels": true, "teams": true, "project": true}
	for _, filter := range filters {
		if !allowedFilters[filter.Field] || len(filter.Values) == 0 || len(filter.Values) > 100 || filter.Operator != "is" && filter.Operator != "isNot" {
			writeError(w, http.StatusBadRequest, "invalid project filter")
			return
		}
	}
	query := store.ProjectRecordQuery{
		Workspace:    metadata.Workspace.URLKey,
		TeamIDs:      splitQueryValues(r.URL.Query().Get("teamId")),
		Search:       r.URL.Query().Get("q"),
		Archived:     r.URL.Query().Get("archived"),
		Cursor:       r.URL.Query().Get("cursor"),
		Limit:        limit,
		IncludeTotal: r.URL.Query().Get("includeTotal") == "true",
		Admin:        s.authDisabled || access.Admin,
		Filters:      filters,
	}
	if !query.Admin {
		query.AllowedTeamIDs = access.VisibleTeamIDs
		if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok && apiKeyTeamRestrictionSelected(key) {
			query.AllowedTeamIDs = slices.DeleteFunc(query.AllowedTeamIDs, func(id string) bool { return !slices.Contains(key.TeamIDs, id) })
		}
	}
	page, err := s.store.QueryProjectDirectory(r.Context(), query)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}
