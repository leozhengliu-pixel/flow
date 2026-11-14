package main

import (
	"errors"
	"net/http"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func (s *server) boundedExportJobAccess(r *http.Request) (string, string, store.AccessContext, *store.InstalledWorkspaceIdentityRead, error) {
	read, found, err := s.store.BeginInstalledWorkspaceIdentityRead(r.Context(), workspaceKey(r))
	if err != nil || !found {
		return "", "", store.AccessContext{}, nil, err
	}
	// The legacy export collection belongs to the selected installed map
	// entry. Its physical key can differ from the entry's logical URLKey.
	workspace := read.Identity.RegistryKey
	if s.authDisabled {
		return workspace, "", store.AccessContext{}, read, nil
	}
	var key *domain.APIKey
	if credential, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok {
		key = &credential
	}
	access, err := s.store.ResolveAccess(r.Context(), workspace, authUser(r).ID, key)
	if err != nil {
		read.Close()
		return "", "", store.AccessContext{}, nil, err
	}
	return workspace, access.PrincipalID, access, read, nil
}

func scaleP21SW24ExportReadError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrMetadataPageBudget) || errors.Is(err, store.ErrMetadataWorkBudget) {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	issueRecordsError(w, err)
}
