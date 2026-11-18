package main

import (
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// dynamicOAuthClientPrefix starts every client ID that dynamic client
// registration (POST /oauth/register, the MCP OAuth flow) mints. Registered
// OAuth applications use flow_client_ IDs instead.
const dynamicOAuthClientPrefix = "flow_mcp_"

// mcpApplicationID is the client ID of the single "MCP" entry that folds
// every MCP client's member authorizations together. No real client can use
// it: dynamic clients start with dynamicOAuthClientPrefix and registered
// applications with flow_client_.
const mcpApplicationID = "mcp"

// mcpActiveUseWindow is how recently an MCP authorization must have been used
// (its lastUsedAt, recorded at most once a minute per token) for the
// permissions it grants to be shown as "Actively used".
const mcpActiveUseWindow = 30 * 24 * time.Hour

// authorizedApplication is one third-party application with access to the
// workspace, as Settings › Applications lists it ("Authorized applications"):
// its public details and the members' active user-actor authorizations.
// actor=app installations are listed by /api/application-installations.
type authorizedApplication struct {
	ClientID       string                      `json:"clientId"`
	Name           string                      `json:"name"`
	Description    string                      `json:"description,omitempty"`
	LogoURL        string                      `json:"logoUrl,omitempty"`
	DeveloperName  string                      `json:"developerName,omitempty"`
	DeveloperURL   string                      `json:"developerUrl,omitempty"`
	Authorizations []domain.OAuthAuthorization `json:"authorizations"`
	// Realtime is set when the application has a webhook: the page then lists
	// "Receive realtime updates about your workspace" among the permissions.
	Realtime bool `json:"realtime,omitempty"`
	// RedirectURIs are the client's registered redirect URIs, so the web app
	// can recognise well-known clients (Claude, Cursor, VS Code, …) by their
	// app-owned redirect host or URL scheme and show their logo.
	RedirectURIs []string `json:"redirectUris,omitempty"`
	// Kind is "mcp" for the entry that folds together every dynamically
	// registered MCP client (Claude, Cursor, ChatGPT, …): one application,
	// "MCP" by Flow, however many clients members authorized.
	Kind string `json:"kind,omitempty"`
	// ClientIDs are the MCP clients folded into the "mcp" entry.
	ClientIDs []string `json:"clientIds,omitempty"`
	// Members summarises the "mcp" entry per authorizing member, across all
	// of that member's MCP clients.
	Members []authorizedApplicationMember `json:"members,omitempty"`
	// Permissions are the "mcp" entry's granted scopes (the union across its
	// authorizations), each marked when it was used recently.
	Permissions []authorizedApplicationPermission `json:"permissions,omitempty"`
}

type authorizedApplicationMember struct {
	UserID string `json:"userId"`
	// AuthorizedAt is the member's first authorization of any MCP client.
	AuthorizedAt time.Time `json:"authorizedAt"`
	// LastUsedAt is the latest use of any of the member's MCP authorizations.
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
	Scopes     []string   `json:"scopes"`
}

type authorizedApplicationPermission struct {
	Scope string `json:"scope"`
	// ActivelyUsed: an authorization granting the scope was used within
	// mcpActiveUseWindow.
	ActivelyUsed bool `json:"activelyUsed"`
}

// mcpOAuthAuthorization reports whether a member's authorization belongs to
// the folded "MCP" entry: a user-actor grant to a dynamically registered
// client that is not installed in the workspace as an app user (an installed
// client keeps its own row with its app user).
func mcpOAuthAuthorization(authorization domain.OAuthAuthorization, installed map[string]bool) bool {
	return authorization.RevokedAt == nil && authorization.Actor != "app" && strings.HasPrefix(authorization.ClientID, dynamicOAuthClientPrefix) && !installed[authorization.ClientID]
}

// installedApplicationClients is the set of client IDs installed as an app
// user in the workspace.
func installedApplicationClients(installations []domain.ApplicationInstallation) map[string]bool {
	installed := map[string]bool{}
	for _, installation := range installations {
		if !installation.Builtin && installation.Active && installation.ClientID != "" {
			installed[installation.ClientID] = true
		}
	}
	return installed
}

// mcpDeveloperURL is Flow's own website for the "MCP" entry: the configured
// app URL, else the allowed browser origin, else the request's host.
func (s *server) mcpDeveloperURL(r *http.Request) string {
	origin := strings.TrimSpace(s.allowedOrigin)
	if origin == "*" {
		origin = ""
	}
	return strings.TrimRight(firstNonEmpty(strings.TrimSpace(os.Getenv("FLOW_APP_URL")), origin, externalBaseURL(r)), "/")
}

// mcpAuthorizedApplication folds the MCP authorizations into the "MCP"
// entry: distinct members with their first authorization and last use, and
// the union of granted scopes with their recent use.
func mcpAuthorizedApplication(authorizations []domain.OAuthAuthorization, developerURL string, now time.Time) authorizedApplication {
	item := authorizedApplication{ClientID: mcpApplicationID, Kind: "mcp", Name: "MCP", DeveloperName: "Flow", DeveloperURL: developerURL, Authorizations: slices.Clone(authorizations), ClientIDs: []string{}, Members: []authorizedApplicationMember{}, Permissions: []authorizedApplicationPermission{}}
	members := map[string]int{}
	active := map[string]bool{}
	for _, authorization := range authorizations {
		if !slices.Contains(item.ClientIDs, authorization.ClientID) {
			item.ClientIDs = append(item.ClientIDs, authorization.ClientID)
		}
		used := authorization.LastUsedAt != nil && now.Sub(*authorization.LastUsedAt) <= mcpActiveUseWindow
		for _, scope := range authorization.Scopes {
			if index := slices.IndexFunc(item.Permissions, func(permission authorizedApplicationPermission) bool { return permission.Scope == scope }); index < 0 {
				item.Permissions = append(item.Permissions, authorizedApplicationPermission{Scope: scope})
			}
			active[scope] = active[scope] || used
		}
		index, ok := members[authorization.UserID]
		if !ok {
			index = len(item.Members)
			members[authorization.UserID] = index
			item.Members = append(item.Members, authorizedApplicationMember{UserID: authorization.UserID, AuthorizedAt: authorization.CreatedAt, Scopes: []string{}})
		}
		member := &item.Members[index]
		if authorization.CreatedAt.Before(member.AuthorizedAt) {
			member.AuthorizedAt = authorization.CreatedAt
		}
		if authorization.LastUsedAt != nil && (member.LastUsedAt == nil || authorization.LastUsedAt.After(*member.LastUsedAt)) {
			last := *authorization.LastUsedAt
			member.LastUsedAt = &last
		}
		for _, scope := range authorization.Scopes {
			if !slices.Contains(member.Scopes, scope) {
				member.Scopes = append(member.Scopes, scope)
			}
		}
	}
	for index := range item.Permissions {
		item.Permissions[index].ActivelyUsed = active[item.Permissions[index].Scope]
	}
	slices.SortStableFunc(item.Members, func(a, b authorizedApplicationMember) int { return a.AuthorizedAt.Compare(b.AuthorizedAt) })
	return item
}

// GET /api/oauth/authorized-applications lists, for workspace admins, every
// application that members authorized or that is installed as an app user,
// with all members' active authorizations (the bootstrap only carries the
// viewer's own). Every dynamically registered MCP client is folded into one
// "MCP" entry (clientId "mcp", kind "mcp").
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
	s.withColdMetadataRequest(w, r, "server/oauth_authorized_applications.go:listAuthorizedApplications", "", func(repo *store.SQLiteStore, request *http.Request) {
		s.listAuthorizedApplicationsWithStore(w, request, repo, key)
	})
}

// DELETE /api/oauth/authorized-applications/mcp revokes MCP access: every
// MCP client's authorizations and their tokens. Admins revoke every member's;
// anyone else revokes only their own (the rule of DELETE
// /api/oauth/authorizations/{id}, which each revocation still enforces).
func (s *server) revokeMCPAuthorizations(w http.ResponseWriter, r *http.Request) {
	actor := requestActor(s, r)
	access, ok := s.oauthWorkspaceForRequest(w, r, workspaceKey(r), actor)
	if !ok {
		return
	}
	s.withColdMetadataRequest(w, r, "server/oauth_authorized_applications.go:revokeMCPAuthorizations", "", func(repo *store.SQLiteStore, request *http.Request) {
		s.revokeMCPAuthorizationsWithStore(w, request, repo, actor, access)
	})
}
