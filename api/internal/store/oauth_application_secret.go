package store

import (
	"slices"

	"flow/api/internal/domain"
)

// OAuthApplicationSecret returns the client secret of a workspace's OAuth
// application. Workspace projections strip secrets, so the settings page's
// copy button reads it here.
func (s *SQLiteStore) OAuthApplicationSecret(workspaceKey, applicationID string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, ok := s.workspaces[workspaceKey]
	if !ok {
		return "", false
	}
	for _, app := range data.OAuthApplications {
		if app.ID == applicationID {
			return app.ClientSecret, true
		}
	}
	return "", false
}

// OAuthApplicationByClientID returns a registered OAuth application (from any
// workspace) by client ID, without its secret: Settings › Applications shows
// an installed application's public details (description, developer).
func (s *SQLiteStore) OAuthApplicationByClientID(clientID string) (domain.OAuthApplication, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, data := range s.workspaces {
		for _, app := range data.OAuthApplications {
			if app.ClientID == clientID {
				app.ClientSecret = ""
				app.RedirectURIs = slices.Clone(app.RedirectURIs)
				app.Scopes = slices.Clone(app.Scopes)
				return app, true
			}
		}
	}
	return domain.OAuthApplication{}, false
}
