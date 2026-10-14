package main

import (
	"net/http"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

// authorizedApplication is one third-party application with access to the
// workspace, as Settings › Applications lists it (the reference app's "Authorized
// applications"): its public details and the members' active user-actor
// authorizations. actor=app installations are listed by
// /api/application-installations.
type authorizedApplication struct {
	ClientID       string                      `json:"clientId"`
	Name           string                      `json:"name"`
	Description    string                      `json:"description,omitempty"`
	LogoURL        string                      `json:"logoUrl,omitempty"`
	DeveloperName  string                      `json:"developerName,omitempty"`
	DeveloperURL   string                      `json:"developerUrl,omitempty"`
	Authorizations []domain.OAuthAuthorization `json:"authorizations"`
	// Realtime is set when the application has a webhook: the reference app then lists
	// "Receive realtime updates about your workspace" among the permissions.
	Realtime bool `json:"realtime,omitempty"`
}

// GET /api/oauth/authorized-applications lists, for workspace admins, every
// application that members authorized or that is installed as an app user,
// with all members' active authorizations (the bootstrap only carries the
// viewer's own).
func (s *server) listAuthorizedApplications(w http.ResponseWriter, r *http.Request) {
	access, ok := s.oauthWorkspaceForRequest(w, r, workspaceKey(r), s.oauthRequestUser(r))
	if !ok {
		return
	}
	if !workspaceAdminRole(access.ViewerRole) {
		writeError(w, http.StatusForbidden, "Only workspace admins can manage applications")
		return
	}
	key := access.Workspace.URLKey
	metadata, _ := s.store.WorkspaceMetadata(key)
	byClient := map[string]*authorizedApplication{}
	order := []string{}
	entry := func(clientID, name string) *authorizedApplication {
		if item, ok := byClient[clientID]; ok {
			return item
		}
		item := &authorizedApplication{ClientID: clientID, Name: name, Authorizations: []domain.OAuthAuthorization{}}
		byClient[clientID] = item
		order = append(order, clientID)
		return item
	}
	for _, authorization := range metadata.OAuthAuthorizations {
		if authorization.RevokedAt != nil || authorization.Actor == "app" {
			continue
		}
		item := entry(authorization.ClientID, authorization.ClientName)
		item.Authorizations = append(item.Authorizations, authorization)
	}
	installations, err := s.store.ListApplications(r.Context(), key)
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	for _, installation := range installations {
		if installation.Builtin || !installation.Active || installation.ClientID == "" {
			continue
		}
		entry(installation.ClientID, installation.Name)
	}
	result := make([]authorizedApplication, 0, len(order))
	for _, clientID := range order {
		item := byClient[clientID]
		if app, ok := s.store.OAuthApplicationByClientID(clientID); ok {
			item.Name, item.Description, item.LogoURL, item.DeveloperName, item.DeveloperURL = app.Name, app.Description, app.LogoURL, app.DeveloperName, app.DeveloperURL
			item.Realtime = app.WebhookEnabled && app.WebhookURL != ""
		} else if client, err := s.store.OAuthClient(r.Context(), clientID); err == nil {
			item.Name, item.LogoURL, item.DeveloperURL = client.ClientName, client.LogoURI, client.ClientURI
		}
		result = append(result, *item)
	}
	slices.SortStableFunc(result, func(a, b authorizedApplication) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	writeJSON(w, http.StatusOK, result)
}
