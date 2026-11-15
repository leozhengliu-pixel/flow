package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
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
	if s.store.BoundedMetadataEnabled() {
		workspace := workspaceKey(r)
		var item domain.Team
		var err error
		if s.authDisabled {
			var workspaceExists bool
			item, workspaceExists, err = s.store.TeamRecordForWorkspace(r.Context(), workspace, r.PathValue("id"))
			if err == nil && !workspaceExists {
				writeAPIError(w, http.StatusNotFound, apiErrorNotFound, "workspace not found")
				return
			}
		} else {
			if workspace == "" {
				writeAPIError(w, http.StatusBadRequest, apiErrorInvalidInput, "Select a workspace with the X-Workspace-Key header (API keys and OAuth tokens select it automatically)")
				return
			}
			var credential *domain.APIKey
			if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok {
				credential = &key
			}
			var access store.AccessContext
			access, err = s.store.ResolveAccess(r.Context(), workspace, authUser(r).ID, credential)
			if errors.Is(err, sql.ErrNoRows) {
				err = store.ErrAuthForbidden
			}
			if err == nil {
				item, err = s.store.TeamRecordForAccess(r.Context(), access, r.PathValue("id"))
			}
		}
		if errors.Is(err, sql.ErrNoRows) {
			writePublicRecordNotFound(w, "team")
			return
		}
		if err != nil {
			if errors.Is(err, store.ErrAuthForbidden) {
				writeAPIError(w, http.StatusForbidden, apiErrorForbidden, "You don't have access to this workspace")
			} else if errors.Is(err, store.ErrMetadataPageBudget) || errors.Is(err, store.ErrMetadataWorkBudget) {
				writeAPIError(w, http.StatusServiceUnavailable, apiErrorUnavailable, err.Error())
			} else {
				writeAPIError(w, http.StatusInternalServerError, apiErrorInternal, "Could not load workspace data")
			}
			return
		}
		writeJSON(w, http.StatusOK, item)
		return
	}
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
	if s.store.BoundedMetadataEnabled() {
		workspace := workspaceKey(r)
		if workspace == "" && !s.authDisabled {
			writeAPIError(w, http.StatusBadRequest, apiErrorInvalidInput, "Select a workspace with the X-Workspace-Key header (API keys and OAuth tokens select it automatically)")
			return
		}
		ctx, userID := r.Context(), ""
		var credential *domain.APIKey
		var handed *domain.Bootstrap
		if s.authDisabled {
			ctx = context.WithoutCancel(ctx)
		} else {
			userID = authUser(r).ID
			if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok {
				credential = &key
			}
			if data, ok := takePagedMetadata(r, workspace, userID); ok {
				handed = &data
			} else if !acquireProjection(r) {
				if retryAfter, refused := projectionRefused(r); refused {
					writeProjectionBusy(w, retryAfter)
				}
				return
			}
		}
		fail := func(err error) {
			if errors.Is(err, store.ErrAuthForbidden) {
				writeAPIError(w, http.StatusForbidden, apiErrorForbidden, "You don't have access to this workspace")
			} else {
				writeAPIError(w, http.StatusInternalServerError, apiErrorInternal, "Could not load workspace data")
			}
		}
		read, err := s.store.BeginResourceDirectoryRead(ctx, workspace, userID, credential, "labels", handed)
		if err != nil {
			if s.authDisabled && errors.Is(err, store.ErrAuthForbidden) && !errors.Is(err, store.ErrResourceAccessChanged) {
				writeAPIError(w, http.StatusNotFound, apiErrorNotFound, "workspace not found")
			} else {
				fail(err)
			}
			return
		}
		defer read.Close()
		if !s.authDisabled {
			filterBootstrapForAPIKey(&read.Data, r)
			if err := s.filterPreferenceIssueTeams(r, &read.Data); err != nil {
				issueRecordsError(w, err)
				return
			}
		}
		teams, id := visibleTeamSet(read.Data), r.PathValue("id")
		var selected store.ResourceDirectoryToken
		found := false
		err = read.Visit(ctx, func(value *store.ResourceDirectoryValue) error {
			label := value.Label
			if label.ID != id {
				return nil
			}
			if !labelScopeIsWorkspace(label.Scope) {
				if _, visible := teams[label.Scope]; !visible {
					return nil
				}
			}
			selected, found = value.Token, true
			return store.ErrMetadataIterationDone
		})
		if err != nil && !errors.Is(err, store.ErrMetadataIterationDone) {
			fail(err)
			return
		}
		if !found {
			writePublicRecordNotFound(w, "label")
			return
		}
		value, err := read.Load(ctx, selected)
		if err != nil {
			fail(err)
			return
		}
		defer value.Close()
		writeJSON(w, http.StatusOK, value.Label)
		return
	}
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
	if s.serveResourceRead(w, r, "projects") {
		return
	}
	var data domain.Bootstrap
	if s.store.BoundedMetadataEnabled() {
		if s.authDisabled {
			read, found, err := s.store.BeginInstalledResourcePointRead(context.WithoutCancel(r.Context()), workspaceKey(r), "projects", r.PathValue("id"))
			if err != nil {
				writeAPIError(w, http.StatusInternalServerError, apiErrorInternal, "Could not load workspace data")
				return
			}
			if !found {
				writeAPIError(w, http.StatusNotFound, apiErrorNotFound, "workspace not found")
				return
			}
			defer read.Close()
			data = read.Data
		} else {
			if workspaceKey(r) == "" {
				writeAPIError(w, http.StatusBadRequest, apiErrorInvalidInput, "Select a workspace with the X-Workspace-Key header (API keys and OAuth tokens select it automatically)")
				return
			}
			var handedOff bool
			data, handedOff = takePagedMetadata(r, workspaceKey(r), authUser(r).ID)
			if handedOff {
				filterBootstrapForAPIKey(&data, r)
				if err := s.filterPreferenceIssueTeams(r, &data); err != nil {
					issueRecordsError(w, err)
					return
				}
			} else {
				if !acquireProjection(r) {
					if retryAfter, refused := projectionRefused(r); refused {
						writeProjectionBusy(w, retryAfter)
					}
					return
				}
				var credential *domain.APIKey
				if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok {
					credential = &key
				}
				access, err := s.store.ResolveAccess(r.Context(), workspaceKey(r), authUser(r).ID, credential)
				var read *store.ResourceRead
				if err == nil {
					read, err = s.store.BeginProjectedInstalledResourcePointRead(r.Context(), access, "projects", r.PathValue("id"))
				}
				if err != nil {
					if errors.Is(err, store.ErrAuthForbidden) || errors.Is(err, sql.ErrNoRows) {
						writeAPIError(w, http.StatusForbidden, apiErrorForbidden, "You don't have access to this workspace")
					} else {
						writeAPIError(w, http.StatusInternalServerError, apiErrorInternal, "Could not load workspace data")
					}
					return
				}
				defer read.Close()
				data = read.Data
			}
		}
	} else {
		var ok bool
		data, ok = s.publicMetadata(w, r)
		if !ok {
			return
		}
	}
	id := r.PathValue("id")
	index := domain.ProjectIndexBySlugRef(data.Projects, id)
	if index < 0 {
		s.shadowResourceRecord(r, "projects", nil)
		writePublicRecordNotFound(w, "project")
		return
	}
	s.shadowResourceRecord(r, "projects", data.Projects[index])
	writeJSON(w, http.StatusOK, data.Projects[index])
}

// GET /api/initiatives/{id}: the id or the slugId.
func (s *server) getInitiativeRecord(w http.ResponseWriter, r *http.Request) {
	if s.serveResourceRead(w, r, "initiatives") {
		return
	}
	var data domain.Bootstrap
	if s.store.BoundedMetadataEnabled() {
		if s.authDisabled {
			read, found, err := s.store.BeginInstalledResourcePointRead(context.WithoutCancel(r.Context()), workspaceKey(r), "initiatives", r.PathValue("id"))
			if err != nil {
				writeAPIError(w, http.StatusInternalServerError, apiErrorInternal, "Could not load workspace data")
				return
			}
			if !found {
				writeAPIError(w, http.StatusNotFound, apiErrorNotFound, "workspace not found")
				return
			}
			defer read.Close()
			data = read.Data
		} else {
			if workspaceKey(r) == "" {
				writeAPIError(w, http.StatusBadRequest, apiErrorInvalidInput, "Select a workspace with the X-Workspace-Key header (API keys and OAuth tokens select it automatically)")
				return
			}
			var handedOff bool
			data, handedOff = takePagedMetadata(r, workspaceKey(r), authUser(r).ID)
			if handedOff {
				filterBootstrapForAPIKey(&data, r)
				if err := s.filterPreferenceIssueTeams(r, &data); err != nil {
					issueRecordsError(w, err)
					return
				}
			} else {
				if !acquireProjection(r) {
					if retryAfter, refused := projectionRefused(r); refused {
						writeProjectionBusy(w, retryAfter)
					}
					return
				}
				var credential *domain.APIKey
				if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok {
					credential = &key
				}
				access, err := s.store.ResolveAccess(r.Context(), workspaceKey(r), authUser(r).ID, credential)
				var read *store.ResourceRead
				if err == nil {
					read, err = s.store.BeginProjectedInstalledResourcePointRead(r.Context(), access, "initiatives", r.PathValue("id"))
				}
				if err != nil {
					if errors.Is(err, store.ErrAuthForbidden) || errors.Is(err, sql.ErrNoRows) {
						writeAPIError(w, http.StatusForbidden, apiErrorForbidden, "You don't have access to this workspace")
					} else {
						writeAPIError(w, http.StatusInternalServerError, apiErrorInternal, "Could not load workspace data")
					}
					return
				}
				defer read.Close()
				data = read.Data
			}
		}
	} else {
		var ok bool
		data, ok = s.publicMetadata(w, r)
		if !ok {
			return
		}
	}
	id := r.PathValue("id")
	index := domain.InitiativeIndexBySlugRef(data.Initiatives, id)
	if index < 0 {
		s.shadowResourceRecord(r, "initiatives", nil)
		writePublicRecordNotFound(w, "initiative")
		return
	}
	s.shadowResourceRecord(r, "initiatives", data.Initiatives[index])
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
