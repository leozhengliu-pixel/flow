package main

import (
	"errors"
	"net/http"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// getTeamSettingsBounded is getTeamSettings with bounded metadata on. The
// literal body derives teamSettings from workspaceData's metadata branch: the
// paged viewer projection (or development mode's unprojected metadata),
// narrowed by the request's API key. teamSettings reads only the projected
// team settings, the parent chain's settings and, for a team without
// settings, the workflow states, so the reviewed BeginTeamSettingsRead
// projection of those fields, narrowed by the same filterBootstrapForAPIKey,
// answers the same settings. A projection that cannot run now answers busy;
// one the store refuses answers as the literal empty projection does.
func (s *server) getTeamSettingsBounded(w http.ResponseWriter, r *http.Request) {
	userID := ""
	if !s.authDisabled {
		userID = authUser(r).ID
		if !acquireProjection(r) {
			if retryAfter, refused := projectionRefused(r); refused {
				writeProjectionBusy(w, retryAfter)
			}
			return
		}
	}
	data := domain.Bootstrap{}
	read, found, err := s.store.BeginTeamSettingsRead(r.Context(), workspaceKey(r), userID)
	if err != nil && !errors.Is(err, store.ErrAuthForbidden) {
		publicResourceDirectoryError(w, err)
		return
	}
	if err == nil && found {
		defer read.Close()
		data = read.Data
		if s.authDisabled {
			data.ViewerRole = "admin"
		} else {
			filterBootstrapForAPIKey(&data, r)
		}
	}
	writeJSON(w, http.StatusOK, teamSettings(&data, r.PathValue("id")))
}
