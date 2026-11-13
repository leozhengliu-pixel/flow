package main

import (
	"errors"
	"net/http"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// workspaceDataScoped keeps the exact legacy accessor when residency is off.
// Bounded callers check ok before using data; errors never become empty state.
func (s *server) workspaceDataScoped(w http.ResponseWriter, r *http.Request, scope store.MetadataRecordScope) (domain.Bootstrap, bool) {
	if !s.store.BoundedMetadataEnabled() {
		return s.workspaceData(r), true
	}
	var data domain.Bootstrap
	var err error
	if s.authDisabled {
		var ok bool
		data, ok, err = s.store.WorkspaceMetadataScoped(r.Context(), workspaceKey(r), scope)
		if err == nil && !ok {
			writeError(w, http.StatusNotFound, "workspace not found")
			return data, false
		}
		data.ViewerRole = "admin"
	} else {
		var key *domain.APIKey
		if credential, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok {
			key = &credential
		}
		data, err = s.store.ProjectScopedMetadata(r.Context(), workspaceKey(r), authUser(r).ID, key, scope)
	}
	if err != nil {
		if errors.Is(err, store.ErrMetadataPageBudget) {
			writeError(w, http.StatusServiceUnavailable, err.Error())
			return domain.Bootstrap{}, false
		}
		issueRecordsError(w, err)
		return domain.Bootstrap{}, false
	}
	filterBootstrapForAPIKey(&data, r)
	return data, true
}
