package main

import (
	"net/http"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

// Single-record reads of the public REST API: GET /api/<resource>/{id} for
// teams, users, workflow states, labels, cycles, projects, initiatives and
// customers. They apply the visibility of the matching list endpoint (the
// caller's projected workspace metadata narrowed by API key team
// restrictions and application scopes); a record the caller cannot see is a
// 404. Archived records are returned, unlike the lists, which hide them
// unless includeArchived is set.

func writePublicRecordNotFound(w http.ResponseWriter, name string) {
	writeAPIError(w, http.StatusNotFound, apiErrorNotFound, name+" not found")
}

// GET /api/teams/{id}: the id or the team key (ENG).
func (s *server) getTeamRecord(w http.ResponseWriter, r *http.Request) {
	data, ok := s.publicMetadata(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	index := slices.IndexFunc(data.Teams, func(team domain.Team) bool { return team.ID == id })
	if index < 0 {
		index = slices.IndexFunc(data.Teams, func(team domain.Team) bool { return strings.EqualFold(team.Key, id) })
	}
	if index < 0 {
		writePublicRecordNotFound(w, "team")
		return
	}
	writeJSON(w, http.StatusOK, data.Teams[index])
}

// GET /api/users/{id}: the id, or "me" for the caller.
func (s *server) getUserRecord(w http.ResponseWriter, r *http.Request) {
	data, ok := s.publicMetadata(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if id == "me" {
		id = data.Viewer.ID
	}
	index := slices.IndexFunc(data.Users, func(user domain.User) bool { return user.ID == id })
	if index < 0 {
		writePublicRecordNotFound(w, "user")
		return
	}
	user := data.Users[index]
	user.AppScopes = nil
	writeJSON(w, http.StatusOK, user)
}

// GET /api/workflow-states/{id}
func (s *server) getWorkflowStateRecord(w http.ResponseWriter, r *http.Request) {
	data, ok := s.publicMetadata(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	for _, team := range data.Teams {
		for _, state := range statesForTeam(&data, team.ID) {
			if state.ID == id {
				writeJSON(w, http.StatusOK, state)
				return
			}
		}
	}
	writePublicRecordNotFound(w, "workflow state")
}

// GET /api/issue-labels/{id}
func (s *server) getIssueLabelRecord(w http.ResponseWriter, r *http.Request) {
	data, ok := s.publicMetadata(w, r)
	if !ok {
		return
	}
	teams := visibleTeamSet(data)
	id := r.PathValue("id")
	index := slices.IndexFunc(data.Labels, func(label domain.IssueLabel) bool {
		if label.ID != id {
			return false
		}
		if labelScopeIsWorkspace(label.Scope) {
			return true
		}
		_, visible := teams[label.Scope]
		return visible
	})
	if index < 0 {
		writePublicRecordNotFound(w, "label")
		return
	}
	writeJSON(w, http.StatusOK, data.Labels[index])
}

// GET /api/cycles/{id}
func (s *server) getCycleRecord(w http.ResponseWriter, r *http.Request) {
	data, ok := s.publicMetadata(w, r)
	if !ok {
		return
	}
	teams := visibleTeamSet(data)
	id := r.PathValue("id")
	index := slices.IndexFunc(data.Cycles, func(cycle domain.Cycle) bool {
		_, visible := teams[cycle.TeamID]
		return cycle.ID == id && visible
	})
	if index < 0 {
		writePublicRecordNotFound(w, "cycle")
		return
	}
	if err := s.decorateCycleCapacity(r.Context(), &data); err != nil {
		writeAPIError(w, http.StatusInternalServerError, apiErrorInternal, "Could not load the cycle")
		return
	}
	cycle := data.Cycles[index]
	cycle.CalendarToken = ""
	writeJSON(w, http.StatusOK, cycle)
}

// GET /api/projects/{id}: the id or the slugId (a stale-prefix or legacy
// name-only slug resolves too).
func (s *server) getProjectRecord(w http.ResponseWriter, r *http.Request) {
	data, ok := s.publicMetadata(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	index := domain.ProjectIndexBySlugRef(data.Projects, id)
	if index < 0 {
		writePublicRecordNotFound(w, "project")
		return
	}
	writeJSON(w, http.StatusOK, data.Projects[index])
}

// GET /api/initiatives/{id}: the id or the slugId.
func (s *server) getInitiativeRecord(w http.ResponseWriter, r *http.Request) {
	data, ok := s.publicMetadata(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	index := domain.InitiativeIndexBySlugRef(data.Initiatives, id)
	if index < 0 {
		writePublicRecordNotFound(w, "initiative")
		return
	}
	writeJSON(w, http.StatusOK, data.Initiatives[index])
}

// GET /api/customers/{id}
func (s *server) getCustomerRecord(w http.ResponseWriter, r *http.Request) {
	data, ok := s.publicMetadata(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	index := slices.IndexFunc(data.Customers, func(customer domain.Customer) bool { return customer.ID == id })
	if index < 0 {
		writePublicRecordNotFound(w, "customer")
		return
	}
	writeJSON(w, http.StatusOK, data.Customers[index])
}
