package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// These bodies run on the store withColdMetadataRequest lends them: flag off
// the server's own store, literally; bounded the registered COLD facade,
// whose legacy accessors read the complete installed workspace.

// coldWorkspaceData is workspaceData on the borrowed store, for the
// registered COLD settings routes that read it (API keys, OAuth
// applications). Flag off it is workspaceData itself. Bounded it follows
// workspaceData's branches with the refreshed facade in place of the live
// store: a metadata read (every /api/api-keys request) takes the projection
// authorization built on the same facade (handed off to GETs), else reads a
// fresh PagedWorkspaceMetadata; any other route reads BootstrapForUser. The
// views, issue-record authorization and paged realtime branches belong to no
// cold route and are refused.
func (s *server) coldWorkspaceData(r *http.Request, repo *store.SQLiteStore) (domain.Bootstrap, error) {
	if !s.store.BoundedMetadataEnabled() {
		return s.workspaceData(r), nil
	}
	key := workspaceKey(r)
	if _, issueAuthorization := r.Context().Value(issueRecordAuthorizationContext{}).(domain.Bootstrap); r.URL.Path == "/api/views" || strings.HasPrefix(r.URL.Path, "/api/views/") || issueAuthorization || pagedRealtimeRequest(r) {
		return domain.Bootstrap{}, store.ErrBoundedMetadataCoverage
	}
	if metadataReadRequest(r) {
		if s.authDisabled {
			if err := repo.RefreshColdMetadataRead(r.Context(), key); err != nil {
				return domain.Bootstrap{}, err
			}
			data, _ := repo.WorkspaceMetadata(key)
			data.ViewerRole = "admin"
			return data, nil
		}
		data, handedOff := takePagedMetadata(r, key, authUser(r).ID)
		if !handedOff {
			// Bounded with the other projections; this caller cannot answer 503, so it waits.
			if !acquireProjectionWaiting(r) {
				// The client left while waiting: nothing reads the answer.
				return domain.Bootstrap{}, nil
			}
			if err := repo.RefreshColdMetadataRead(r.Context(), key); err != nil {
				return domain.Bootstrap{}, err
			}
			data, _ = repo.PagedWorkspaceMetadata(r.Context(), key, authUser(r).ID)
		}
		filterBootstrapForAPIKey(&data, r)
		return data, nil
	}
	if err := repo.RefreshColdMetadataRead(r.Context(), key); err != nil {
		return domain.Bootstrap{}, err
	}
	if !s.authDisabled {
		data, _, _ := repo.BootstrapForUser(r.Context(), key, authUser(r).ID)
		filterBootstrapForAPIKey(&data, r)
		return data, nil
	}
	data, _ := repo.BootstrapFor(key)
	data.ViewerRole = "admin"
	return data, nil
}

// listAPIKeysWithStore is listAPIKeys with the repository explicit.
func (s *server) listAPIKeysWithStore(w http.ResponseWriter, r *http.Request, repo *store.SQLiteStore) {
	actor := requestActor(s, r)
	data, err := s.coldWorkspaceData(r, repo)
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	result := []domain.APIKey{}
	admin := strings.EqualFold(data.ViewerRole, "admin") || strings.EqualFold(data.ViewerRole, "owner")
	apiKeyID := ""
	if token, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok {
		// A token-authenticated request must not enumerate the creator's other
		// credentials, even when that creator is a workspace administrator.
		apiKeyID = token.ID
	}
	for _, key := range data.APIKeys {
		if key.RevokedAt == nil && (apiKeyID != "" && key.ID == apiKeyID || apiKeyID == "" && (admin || key.CreatorID == actor.ID)) {
			result = append(result, publicAPIKey(key))
		}
	}
	writeJSON(w, http.StatusOK, result)
}

// createAPIKeyWithStore is createAPIKey after its decode, name validation and
// secret generation.
func (s *server) createAPIKeyWithStore(w http.ResponseWriter, r *http.Request, repo *store.SQLiteStore, input apiKeyCreateInput, secret string, actor domain.User) {
	var created domain.APIKey
	err := repo.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "api_key.created", input, func(data *domain.Bootstrap) (string, error) {
		if s.authDisabled {
			data.ViewerRole = "admin"
		}
		if strings.EqualFold(data.ViewerRole, "guest") {
			return "", fmt.Errorf("%w: guest users cannot create personal API keys", store.ErrAuthForbidden)
		}
		if !roleSatisfiesWorkspacePermission(data.ViewerRole, data.WorkspaceSettings.APIKeyPermission) {
			return "", fmt.Errorf("%w: API key creation is limited by workspace security policy", store.ErrAuthForbidden)
		}
		for _, existing := range data.APIKeys {
			if existing.RevokedAt == nil && existing.CreatorID == actor.ID && strings.EqualFold(strings.TrimSpace(existing.Name), input.Name) {
				return "", fmt.Errorf("%w: API key name already exists", errInvalid)
			}
		}
		scopes := normalizeAPIKeyScopes(input.Scopes)
		if err := validateAPIKeyScopes(data, scopes); err != nil {
			return "", err
		}
		teamIDs := normalizedStrings(input.TeamIDs)
		for _, id := range teamIDs {
			if !teamExists(data, id) {
				return "", fmt.Errorf("%w: unknown team", errInvalid)
			}
		}
		restriction := strings.ToLower(strings.TrimSpace(input.TeamRestriction))
		if restriction == "" {
			if len(teamIDs) > 0 {
				restriction = "selected"
			} else {
				restriction = "all"
			}
		}
		if restriction != "all" && restriction != "selected" {
			return "", fmt.Errorf("%w: teamRestriction must be all or selected", errInvalid)
		}
		if restriction == "selected" && len(teamIDs) == 0 {
			return "", fmt.Errorf("%w: selected team access requires at least one team", errInvalid)
		}
		if restriction == "all" {
			teamIDs = nil
		}
		var expiresAt *time.Time
		if input.ExpiresAt != nil {
			var parseErr error
			expiresAt, parseErr = parseAPIKeyExpiry(input.ExpiresAt)
			if parseErr != nil {
				return "", parseErr
			}
		}
		created = domain.APIKey{ID: idgen.New("api_key_"), Name: input.Name, Prefix: secret[:min(len(secret), 17)], SecretHash: secretHash(secret), CreatorID: actor.ID, Scopes: scopes, TeamIDs: teamIDs, TeamRestriction: restriction, CreatedAt: time.Now().UTC(), ExpiresAt: expiresAt}
		data.APIKeys = append([]domain.APIKey{created}, data.APIKeys...)
		appendAudit(data, "created", "api_key", created.ID, map[string]any{"name": created.Name, "prefix": created.Prefix, "scopes": created.Scopes, "teamRestriction": created.TeamRestriction})
		return created.ID, nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusCreated, nil)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"key": publicAPIKey(created), "secret": secret})
}

// apiKeyUpdate is updateAPIKey's decoded and validated input.
type apiKeyUpdate struct {
	id, name                   string
	nameSet                    bool
	scopes, teamIDs            []string
	scopesSet, teamIDsSet      bool
	restrictionValue           string
	restrictionSet, expiresSet bool
	expiresAt                  *time.Time
}

// updateAPIKeyWithStore is updateAPIKey after its decode and validation.
func (s *server) updateAPIKeyWithStore(w http.ResponseWriter, r *http.Request, repo *store.SQLiteStore, input apiKeyUpdate, actor domain.User) {
	id, name, scopes, teamIDs, restrictionValue, expiresAt := input.id, input.name, input.scopes, input.teamIDs, input.restrictionValue, input.expiresAt
	var updated domain.APIKey
	err := repo.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "api_key.updated", map[string]any{
		"id": id, "name": name, "scopes": scopes, "teamIds": teamIDs, "teamRestriction": restrictionValue, "expiresAt": expiresAt,
	}, func(data *domain.Bootstrap) (string, error) {
		if s.authDisabled {
			data.ViewerRole = "admin"
		}
		index := slices.IndexFunc(data.APIKeys, func(key domain.APIKey) bool {
			return key.ID == id && key.RevokedAt == nil && (key.CreatorID == actor.ID || workspaceAdminRole(data.ViewerRole))
		})
		if index < 0 {
			return "", errNotFound
		}
		current := &data.APIKeys[index]
		before := *current
		if input.nameSet {
			for _, existing := range data.APIKeys {
				if existing.ID != id && existing.RevokedAt == nil && existing.CreatorID == current.CreatorID && strings.EqualFold(strings.TrimSpace(existing.Name), name) {
					return "", fmt.Errorf("%w: API key name already exists", errInvalid)
				}
			}
			current.Name = name
		}
		if input.scopesSet {
			if err := validateAPIKeyScopes(data, scopes); err != nil {
				return "", err
			}
			current.Scopes = scopes
		}
		if input.teamIDsSet || input.restrictionSet {
			ids := slices.Clone(current.TeamIDs)
			restriction := strings.ToLower(strings.TrimSpace(current.TeamRestriction))
			if input.teamIDsSet {
				ids = teamIDs
			}
			if input.restrictionSet {
				restriction = strings.ToLower(restrictionValue)
			}
			if restriction == "" {
				if len(ids) > 0 {
					restriction = "selected"
				} else {
					restriction = "all"
				}
			}
			if restriction != "all" && restriction != "selected" {
				return "", fmt.Errorf("%w: teamRestriction must be all or selected", errInvalid)
			}
			for _, teamID := range ids {
				if !teamExists(data, teamID) {
					return "", fmt.Errorf("%w: unknown team", errInvalid)
				}
			}
			if restriction == "selected" && len(ids) == 0 {
				return "", fmt.Errorf("%w: selected team access requires at least one team", errInvalid)
			}
			if restriction == "all" {
				ids = nil
			}
			current.TeamIDs, current.TeamRestriction = ids, restriction
		}
		if input.expiresSet {
			current.ExpiresAt = expiresAt
		}
		updated = publicAPIKey(*current)
		if unchangedEdit(before, *current) {
			return id, store.ErrNoMutation
		}
		return id, nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// rotateAPIKeySecretWithStore is rotateAPIKeySecret after its secret
// generation.
func (s *server) rotateAPIKeySecretWithStore(w http.ResponseWriter, r *http.Request, repo *store.SQLiteStore, id, secret string, actor domain.User) {
	var rotated domain.APIKey
	err := repo.MutateWorkspaceScoped(r.Context(), workspaceKey(r), "api_key.secret_rotated", id, nil, store.MetadataRecordScope{Write: []store.RecordKey{{Kind: "apiKeys", ID: id}}}, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.APIKeys, func(key domain.APIKey) bool {
			return key.ID == id && key.RevokedAt == nil && (key.CreatorID == actor.ID || strings.EqualFold(data.ViewerRole, "admin") || strings.EqualFold(data.ViewerRole, "owner"))
		})
		if index < 0 {
			return errNotFound
		}
		data.APIKeys[index].SecretHash = secretHash(secret)
		data.APIKeys[index].Prefix = secret[:min(len(secret), 17)]
		rotated = publicAPIKey(data.APIKeys[index])
		appendAudit(data, "secret_rotated", "api_key", id, map[string]any{"name": rotated.Name, "prefix": rotated.Prefix})
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusNotFound, nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": rotated, "secret": secret})
}

// revokeAPIKeyWithStore is revokeAPIKey with the repository explicit: the
// viewer's role from the workspace projection, then the revocation.
func (s *server) revokeAPIKeyWithStore(w http.ResponseWriter, r *http.Request, repo *store.SQLiteStore) {
	id, actor := r.PathValue("id"), requestActor(s, r)
	viewerRole := "admin"
	if !s.store.BoundedMetadataEnabled() || !s.authDisabled {
		data, err := s.coldWorkspaceData(r, repo)
		if err != nil {
			respondMutation(w, err, http.StatusNoContent, nil)
			return
		}
		viewerRole = data.ViewerRole
	}
	err := repo.MutateWorkspaceScoped(r.Context(), workspaceKey(r), "api_key.revoked", id, nil, store.MetadataRecordScope{Write: []store.RecordKey{{Kind: "apiKeys", ID: id}}}, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.APIKeys, func(key domain.APIKey) bool {
			return key.ID == id && (key.CreatorID == actor.ID || workspaceAdminRole(viewerRole))
		})
		if index < 0 {
			return errNotFound
		}
		now := time.Now().UTC()
		data.APIKeys[index].RevokedAt = &now
		appendAudit(data, "revoked", "api_key", id, map[string]any{"name": data.APIKeys[index].Name, "prefix": data.APIKeys[index].Prefix, "ownerId": data.APIKeys[index].CreatorID})
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusNoContent, nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// revokeMCPAuthorizationsWithStore is revokeMCPAuthorizations after its
// workspace access check. The grants and installations are read on the
// borrowed store; each revocation (RevokeOAuthAuthorizationRecords, the
// bounded OAuth metadata transaction) and its audit entry are written
// through the server's store, as legacy.
func (s *server) revokeMCPAuthorizationsWithStore(w http.ResponseWriter, r *http.Request, repo *store.SQLiteStore, actor domain.User, access domain.Bootstrap) {
	key := access.Workspace.URLKey
	if s.store.BoundedMetadataEnabled() {
		// Authorization may have pinned the facade before this handler; the
		// legacy revocation reads the workspace now.
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
	admin := workspaceAdminRole(access.ViewerRole)
	revoked := 0
	for _, authorization := range metadata.OAuthAuthorizations {
		if !mcpOAuthAuthorization(authorization, installed) || !admin && authorization.UserID != actor.ID {
			continue
		}
		err := s.store.RevokeOAuthAuthorizationRecords(r.Context(), key, authorization.ID, actor.ID)
		if store.IsOAuthNotFound(err) {
			continue
		}
		if errors.Is(err, store.ErrAuthForbidden) {
			// The viewer's role changed since the workspace was read.
			writeError(w, http.StatusForbidden, "You don't have permission to perform this action")
			return
		}
		if err != nil {
			respondMutation(w, err, http.StatusOK, nil)
			return
		}
		s.recordAudit(r.Context(), key, newAuditEntry(actor, "revoked", "oauth_authorization", authorization.ID, map[string]any{"clientId": authorization.ClientID, "clientName": authorization.ClientName, "userId": authorization.UserID}))
		revoked++
	}
	writeJSON(w, http.StatusOK, map[string]int{"revoked": revoked})
}

// listOAuthApplicationsWithStore is listOAuthApplications with the
// repository explicit.
func (s *server) listOAuthApplicationsWithStore(w http.ResponseWriter, r *http.Request, repo *store.SQLiteStore) {
	data, err := s.coldWorkspaceData(r, repo)
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	result := data.OAuthApplications
	for index := range result {
		result[index].ClientSecret = ""
	}
	writeJSON(w, http.StatusOK, result)
}

// getOAuthApplicationWithStore is getOAuthApplication with the repository
// explicit.
func (s *server) getOAuthApplicationWithStore(w http.ResponseWriter, r *http.Request, repo *store.SQLiteStore) {
	data, err := s.coldWorkspaceData(r, repo)
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	index := slices.IndexFunc(data.OAuthApplications, func(app domain.OAuthApplication) bool { return app.ID == r.PathValue("id") })
	if index < 0 {
		writeError(w, http.StatusNotFound, "Application not found")
		return
	}
	app := data.OAuthApplications[index]
	app.ClientSecret = ""
	writeJSON(w, http.StatusOK, app)
}

// createOAuthApplicationWithStore is createOAuthApplication after its
// decode, validation, logo check and credential generation. The
// oauth_application.created writer runs on the borrowed store; the webhook
// reconcile and the credential audit use the server's store.
func (s *server) createOAuthApplicationWithStore(w http.ResponseWriter, r *http.Request, repo *store.SQLiteStore, input oauthInput, clientID, secret string, actor domain.User) {
	var created domain.OAuthApplication
	err := repo.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "oauth_application.created", input, func(data *domain.Bootstrap) (string, error) {
		created = domain.OAuthApplication{ID: idgen.New("oauth_app_"), Name: strings.TrimSpace(*input.Name), ClientID: clientID, ClientSecret: secret, CreatorID: actor.ID, CredentialsAuthorizedBy: actor.ID, RedirectURIs: []string{}, Scopes: []string{"read"}, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		applyOAuthInput(&created, input)
		if err := checkGitHubUsernameAvailable(data, created.ID, created.GitHubUsername); err != nil {
			return "", err
		}
		data.OAuthApplications = append([]domain.OAuthApplication{created}, data.OAuthApplications...)
		return created.ID, nil
	})
	if err == nil {
		// Keep each workspace's managed app webhook in step, as manifest saves do.
		go s.reconcileAllApplicationWebhooks(context.Background())
		// The response carries the client secret.
		s.auditAPIKeyCredential(r, "client_secret_issued", created)
		w.Header().Set("Cache-Control", "no-store")
	}
	respondMutation(w, err, http.StatusCreated, created)
}

// deleteOAuthApplicationWithStore is deleteOAuthApplication with the
// repository explicit: the oauth_application.deleted writer runs on the
// borrowed store; the uninstalls, webhook reconcile and the creator's email
// use the server's store.
func (s *server) deleteOAuthApplicationWithStore(w http.ResponseWriter, r *http.Request, repo *store.SQLiteStore) {
	id := r.PathValue("id")
	clientID := ""
	var deleted domain.OAuthApplication
	deletedFrom := ""
	err := repo.MutateWorkspaceScoped(r.Context(), workspaceKey(r), "oauth_application.deleted", id, nil, store.MetadataRecordScope{Write: []store.RecordKey{{Kind: "oauthApplications", ID: id}}}, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.OAuthApplications, func(app domain.OAuthApplication) bool { return app.ID == id })
		if index < 0 {
			return errNotFound
		}
		clientID = data.OAuthApplications[index].ClientID
		deleted, deletedFrom = data.OAuthApplications[index], data.Workspace.Name
		data.OAuthApplications = slices.Delete(data.OAuthApplications, index, index+1)
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusNoContent, nil)
		return
	}
	// Like the reference app, a deleted application leaves every workspace it was
	// installed in: its app users are listed as "Left workspace".
	if clientID != "" {
		installations, err := s.store.ApplicationInstallationsByClient(r.Context(), clientID)
		if err != nil {
			respondMutation(w, err, http.StatusNoContent, nil)
			return
		}
		for _, installation := range installations {
			if _, err := s.store.UninstallApplication(r.Context(), installation.WorkspaceKey, installation.ID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					continue
				}
				respondMutation(w, err, http.StatusNoContent, nil)
				return
			}
			s.reconcileApplicationWebhooks(context.WithoutCancel(r.Context()), installation.WorkspaceKey)
		}
	}
	// The application's tokens are gone with it; its creator hears so by email.
	s.notifyApplicationRevoked(context.WithoutCancel(r.Context()), deleted, deletedFrom)
	w.WriteHeader(http.StatusNoContent)
}
