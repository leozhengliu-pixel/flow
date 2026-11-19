package main

import (
	"errors"
	"net/http"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// withOAuthApplicationsBounded hands answer the oauthApplications field the
// literal GET /api/oauth-applications(/{id}) bodies read from workspaceData's
// BootstrapForUser, with bounded metadata on: the viewer projection leaves
// the field as stored and filterBootstrapForAPIKey empties it for a key
// without the admin scope, so the reviewed installed-field read hands the
// same applications (source order, nil versus empty) without a projection.
// answer writes the response while the read is open.
func (s *server) withOAuthApplicationsBounded(w http.ResponseWriter, r *http.Request, answer func([]domain.OAuthApplication)) {
	if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok && !s.authDisabled && !apiKeyHasScope(key, "admin") {
		answer([]domain.OAuthApplication{})
		return
	}
	read, found, err := s.store.BeginOAuthApplicationsRead(r.Context(), workspaceKey(r))
	if err == nil && !found {
		err = store.ErrAuthForbidden
	}
	if err != nil {
		if errors.Is(err, store.ErrMetadataWorkBudget) {
			writeAPIError(w, http.StatusServiceUnavailable, apiErrorInternal, "Workspace snapshot is busy; retry later")
		} else {
			respondMutation(w, err, http.StatusOK, nil)
		}
		return
	}
	defer read.Close()
	answer(read.Applications)
}
