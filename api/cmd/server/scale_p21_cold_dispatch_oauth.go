package main

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// These bodies run on the store withColdMetadataRequest lends them: flag off
// the server's own store, literally; bounded the registered COLD facade,
// whose legacy accessors read the complete installed workspace.

// listAuthorizedApplicationsWithStore is listAuthorizedApplications after its
// workspace access and admin checks.
func (s *server) listAuthorizedApplicationsWithStore(w http.ResponseWriter, r *http.Request, repo *store.SQLiteStore, key string) {
	if s.store.BoundedMetadataEnabled() {
		// Authorization may have pinned the facade before this handler; the
		// legacy listing reads the workspace now.
		if err := repo.RefreshColdMetadataRead(r.Context(), key); err != nil {
			respondMutation(w, err, http.StatusOK, nil)
			return
		}
	}
	metadata, _ := repo.WorkspaceMetadata(key)
	installations, err := repo.ListApplications(r.Context(), key)
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	installed := installedApplicationClients(installations)
	byClient := map[string]*authorizedApplication{}
	order := []string{}
	mcp := []domain.OAuthAuthorization{}
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
		if mcpOAuthAuthorization(authorization, installed) {
			mcp = append(mcp, authorization)
			continue
		}
		item := entry(authorization.ClientID, authorization.ClientName)
		item.Authorizations = append(item.Authorizations, authorization)
	}
	for _, installation := range installations {
		if installation.Builtin || !installation.Active || installation.ClientID == "" {
			continue
		}
		entry(installation.ClientID, installation.Name)
	}
	result := make([]authorizedApplication, 0, len(order)+1)
	if len(mcp) > 0 {
		result = append(result, mcpAuthorizedApplication(mcp, s.mcpDeveloperURL(r), time.Now().UTC()))
	}
	for _, clientID := range order {
		item := byClient[clientID]
		if app, ok := repo.OAuthApplicationByClientID(clientID); ok {
			item.Name, item.Description, item.LogoURL, item.DeveloperName, item.DeveloperURL = app.Name, app.Description, app.LogoURL, app.DeveloperName, app.DeveloperURL
			item.Realtime = app.WebhookEnabled && app.WebhookURL != ""
			item.RedirectURIs = slices.Clone(app.RedirectURIs)
		} else if client, err := repo.OAuthClient(r.Context(), clientID); err == nil {
			item.Name, item.LogoURL, item.DeveloperURL = client.ClientName, client.LogoURI, client.ClientURI
			item.RedirectURIs = slices.Clone(client.RedirectURIs)
		}
		result = append(result, *item)
	}
	slices.SortStableFunc(result, func(a, b authorizedApplication) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	writeJSON(w, http.StatusOK, result)
}

// updateOAuthApplicationWithStore is updateOAuthApplication after its input
// decode and validation. The identity sync and webhook reconcile after the
// write use the server's store: the reconcile outlives the request.
func (s *server) updateOAuthApplicationWithStore(w http.ResponseWriter, r *http.Request, repo *store.SQLiteStore, input oauthInput) {
	id := r.PathValue("id")
	var updated domain.OAuthApplication
	changed := false
	err := repo.MutateWorkspace(r.Context(), workspaceKey(r), "oauth_application.updated", id, input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.OAuthApplications, func(app domain.OAuthApplication) bool { return app.ID == id })
		if index < 0 {
			return errNotFound
		}
		before := data.OAuthApplications[index]
		applyOAuthInput(&data.OAuthApplications[index], input)
		if err := checkGitHubUsernameAvailable(data, id, data.OAuthApplications[index].GitHubUsername); err != nil {
			return err
		}
		if input.Scopes != nil || input.SupportsClientCredentials != nil {
			data.OAuthApplications[index].CredentialsAuthorizedBy = requestActor(s, r).ID
		}
		updated = data.OAuthApplications[index]
		if unchangedEdit(before, data.OAuthApplications[index]) {
			updated = before
			updated.ClientSecret = ""
			return store.ErrNoMutation
		}
		updated.ClientSecret = ""
		changed = true
		return nil
	})
	if err == nil && changed {
		s.syncApplicationIdentity(r.Context(), updated.ClientID)
		// Keep each workspace's managed app webhook in step, as manifest saves do.
		go s.reconcileAllApplicationWebhooks(context.Background())
	}
	respondMutation(w, err, http.StatusOK, updated)
}
