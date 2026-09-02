package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

const sessionCookieName = "flow_clone_session"

type authRateWindow struct {
	count int
	reset time.Time
}

type authRateLimiter struct {
	mu      sync.Mutex
	windows map[string]authRateWindow
}

type authRateLimitBackend interface {
	Allow(context.Context, string, int, time.Duration) (bool, time.Duration, error)
}

func newAuthRateLimiter() *authRateLimiter {
	return &authRateLimiter{windows: map[string]authRateWindow{}}
}

func (s *server) limitAuth(action string, limit int, window time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		allowed, retryAfter, err := s.authLimiter.Allow(r.Context(), action+":"+authClientAddress(r), limit, window)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "Authentication rate limiting is temporarily unavailable")
			return
		}
		if !allowed {
			seconds := max(1, int(retryAfter.Round(time.Second)/time.Second))
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			writeError(w, http.StatusTooManyRequests, "Too many attempts. Try again later.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (l *authRateLimiter) Allow(_ context.Context, key string, limit int, duration time.Duration) (bool, time.Duration, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	for itemKey, item := range l.windows {
		if now.After(item.reset) {
			delete(l.windows, itemKey)
		}
	}
	item, ok := l.windows[key]
	if !ok {
		l.windows[key] = authRateWindow{count: 1, reset: now.Add(duration)}
		return true, 0, nil
	}
	if item.count >= limit {
		return false, time.Until(item.reset), nil
	}
	item.count++
	l.windows[key] = item
	return true, 0, nil
}

func authClientAddress(r *http.Request) string {
	if strings.EqualFold(os.Getenv("FLOW_TRUST_PROXY_HEADERS"), "true") {
		if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]); forwarded != "" {
			return forwarded
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

type authUserContextKey struct{}
type apiKeyContextKey struct{}
type workspaceKeyContextKey struct{}

func (s *server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.authDisabled || publicAuthPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		user, apiKey, apiWorkspace := s.authenticateAPIKey(r)
		if apiKey == nil {
			cookie, err := r.Cookie(sessionCookieName)
			if err != nil || cookie.Value == "" {
				writeError(w, http.StatusUnauthorized, "Sign in required")
				return
			}
			user, err = s.store.AuthenticateSession(r.Context(), cookie.Value)
			if err != nil {
				clearSessionCookie(w, r)
				writeError(w, http.StatusUnauthorized, "Your session has expired")
				return
			}
		}
		ctx := context.WithValue(r.Context(), authUserContextKey{}, user)
		if apiKey != nil {
			ctx = context.WithValue(ctx, apiKeyContextKey{}, *apiKey)
			ctx = context.WithValue(ctx, workspaceKeyContextKey{}, apiWorkspace)
		}
		ctx = store.ContextWithActor(ctx, user)
		ctx = store.ContextWithRealtimeClient(ctx, r.Header.Get("X-Client-ID"))
		r = r.WithContext(ctx)
		if !s.authorizeWorkspaceRequest(w, r, user) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) authenticateAPIKey(r *http.Request) (domain.User, *domain.APIKey, string) {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return domain.User{}, nil, ""
	}
	secret := strings.TrimSpace(header[len("Bearer "):])
	key := workspaceKey(r)
	data, ok := s.store.BootstrapFor(key)
	if !ok {
		if resolved, _, found := s.store.FindAPIKey(secretHash(secret)); found {
			key = resolved
			data, ok = s.store.BootstrapFor(key)
		}
	}
	if !ok {
		return domain.User{}, nil, ""
	}
	hash := secretHash(secret)
	for _, key := range data.APIKeys {
		if key.SecretHash != hash || key.RevokedAt != nil || key.ExpiresAt != nil && time.Now().UTC().After(*key.ExpiresAt) {
			continue
		}
		if !apiKeyAllowsRequest(r, key) {
			return domain.User{}, nil, ""
		}
		if user, err := s.store.UserByID(r.Context(), key.CreatorID); err == nil {
			now := time.Now().UTC()
			_ = s.store.MutateWorkspace(r.Context(), data.Workspace.URLKey, "api_key.used", key.ID, nil, func(next *domain.Bootstrap) error {
				if index := slices.IndexFunc(next.APIKeys, func(item domain.APIKey) bool { return item.ID == key.ID }); index >= 0 {
					next.APIKeys[index].LastUsedAt = &now
				}
				if key.AuthorizationID != "" {
					if index := slices.IndexFunc(next.OAuthAuthorizations, func(item domain.OAuthAuthorization) bool { return item.ID == key.AuthorizationID }); index >= 0 {
						next.OAuthAuthorizations[index].LastUsedAt = &now
					}
				}
				return nil
			})
			return user, &key, data.Workspace.URLKey
		}
	}
	return domain.User{}, nil, ""
}

// apiKeyAllowsRequest supports granular write scopes while retaining the
// legacy read/write scopes. A key with no write scope is
// read-only; admin implies every operation.
func apiKeyAllowsRequest(r *http.Request, key domain.APIKey) bool {
	if adminOnlyRequest(r) && !slices.Contains(key.Scopes, "admin") {
		return false
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
		return true
	}
	if slices.Contains(key.Scopes, "admin") || slices.Contains(key.Scopes, "write") {
		return true
	}
	path := r.URL.Path
	if slices.Contains(key.Scopes, "create_issues") && r.Method == http.MethodPost && (path == "/api/issues" || strings.HasSuffix(path, "/issues")) {
		return true
	}
	if slices.Contains(key.Scopes, "create_comments") && r.Method == http.MethodPost && strings.Contains(path, "/comments") {
		return true
	}
	return false
}

func workspaceAdminRole(role string) bool {
	role = strings.ToLower(strings.TrimSpace(role))
	return role == "admin" || role == "owner"
}

// applyAPIKeyProjection applies team restrictions after the normal user
// projection. API keys are often narrower than the creator's membership, so
// returning the unrestricted bootstrap would expose unrelated resources.
func applyAPIKeyProjection(data *domain.Bootstrap, key domain.APIKey) {
	if len(key.TeamIDs) == 0 {
		return
	}
	allowed := make(map[string]bool, len(key.TeamIDs))
	for _, id := range key.TeamIDs {
		allowed[id] = true
	}
	data.Teams = slices.DeleteFunc(data.Teams, func(team domain.Team) bool { return !allowed[team.ID] })
	data.Issues = slices.DeleteFunc(data.Issues, func(issue domain.Issue) bool { return !allowed[issue.Team.ID] })
	data.Cycles = slices.DeleteFunc(data.Cycles, func(cycle domain.Cycle) bool { return !allowed[cycle.TeamID] })
	data.Projects = slices.DeleteFunc(data.Projects, func(project domain.Project) bool {
		if len(project.TeamIDs) == 0 {
			return true
		}
		return !slices.ContainsFunc(project.TeamIDs, func(id string) bool { return allowed[id] })
	})
	for index := range data.Projects {
		data.Projects[index].TeamIDs = slices.DeleteFunc(data.Projects[index].TeamIDs, func(id string) bool { return !allowed[id] })
	}
	data.ReleasePipelines = slices.DeleteFunc(data.ReleasePipelines, func(pipeline domain.ReleasePipeline) bool {
		return len(pipeline.TeamIDs) > 0 && !slices.ContainsFunc(pipeline.TeamIDs, func(id string) bool { return allowed[id] })
	})
	for index := range data.ReleasePipelines {
		data.ReleasePipelines[index].TeamIDs = slices.DeleteFunc(data.ReleasePipelines[index].TeamIDs, func(id string) bool { return !allowed[id] })
	}
	visibleProjects, visibleIssues := map[string]bool{}, map[string]bool{}
	for _, project := range data.Projects {
		visibleProjects[project.ID] = true
	}
	for _, issue := range data.Issues {
		visibleIssues[issue.ID] = true
	}
	data.Releases = slices.DeleteFunc(data.Releases, func(release domain.Release) bool {
		if release.PipelineID != "" && !slices.ContainsFunc(data.ReleasePipelines, func(p domain.ReleasePipeline) bool { return p.ID == release.PipelineID }) {
			return true
		}
		return slices.ContainsFunc(release.ProjectIDs, func(id string) bool { return !visibleProjects[id] }) || slices.ContainsFunc(release.IssueIDs, func(id string) bool { return !visibleIssues[id] })
	})
	data.Documents = slices.DeleteFunc(data.Documents, func(document domain.Document) bool {
		return len(document.TeamIDs) > 0 && !slices.ContainsFunc(document.TeamIDs, func(id string) bool { return allowed[id] })
	})
	data.SavedViews = slices.DeleteFunc(data.SavedViews, func(view domain.SavedView) bool {
		return view.Scope == "team" && !allowed[view.TeamID] || view.ProjectID != "" && !visibleProjects[view.ProjectID]
	})
	if data.Settings != nil {
		dashboards := settingCollection[domain.Dashboard](*data, dashboardsSettingsKey)
		for index := range dashboards {
			dashboards[index].TeamIDs = slices.DeleteFunc(dashboards[index].TeamIDs, func(id string) bool { return !allowed[id] })
		}
		dashboards = slices.DeleteFunc(dashboards, func(item domain.Dashboard) bool { return item.Visibility == "team" && len(item.TeamIDs) == 0 })
		saveSettingCollection(data, dashboardsSettingsKey, dashboards)
		posts := settingCollection[domain.Post](*data, postsSettingsKey)
		posts = slices.DeleteFunc(posts, func(item domain.Post) bool {
			return len(item.TeamIDs) > 0 && !slices.ContainsFunc(item.TeamIDs, func(id string) bool { return allowed[id] })
		})
		saveSettingCollection(data, postsSettingsKey, posts)
		meetings := settingCollection[domain.Meeting](*data, meetingsSettingsKey)
		meetings = slices.DeleteFunc(meetings, func(item domain.Meeting) bool {
			return len(item.TeamIDs) > 0 && !slices.ContainsFunc(item.TeamIDs, func(id string) bool { return allowed[id] }) || slices.ContainsFunc(item.IssueIDs, func(id string) bool { return !visibleIssues[id] })
		})
		saveSettingCollection(data, meetingsSettingsKey, meetings)
	}
}

// A member may join or leave their own public/open team. Changes involving a
// different user or assigning the owner role remain owner/admin operations.
func membershipSelfServiceRequest(r *http.Request, userID string) bool {
	if r.Method != http.MethodPut || !strings.HasPrefix(r.URL.Path, "/api/workspaces/") || !strings.Contains(r.URL.Path, "/teams/") || !strings.HasSuffix(r.URL.Path, "/members/"+userID) {
		return false
	}
	var input struct {
		Role   string `json:"role"`
		Member *bool  `json:"member"`
	}
	if !peekRequestJSON(r, &input) || input.Member == nil {
		return false
	}
	return strings.TrimSpace(input.Role) == "" || strings.EqualFold(strings.TrimSpace(input.Role), "member")
}

func publicAuthPath(path string) bool {
	if strings.HasPrefix(path, "/scim/") {
		// SCIM uses its own bearer token validator in scim.go; do not require a
		// browser session for IdP provisioning requests.
		return true
	}
	if strings.HasPrefix(path, "/api/release-pipelines/") && strings.HasSuffix(path, "/events") {
		return true
	}
	if path == "/mcp" || path == "/mcp/readonly" || path == "/oauth/register" || path == "/oauth/token" || path == "/oauth/revoke" || strings.HasPrefix(path, "/.well-known/oauth-") || strings.HasPrefix(path, "/api/mcp/uploads/") {
		return true
	}
	return path == "/api/health" || path == "/api/oauth/token" || path == "/api/auth/register" || path == "/api/auth/verify-email" || path == "/api/auth/resend-verification" || path == "/api/auth/login" || path == "/api/auth/logout" || path == "/api/auth/session" || path == "/api/auth/forgot-password" || path == "/api/auth/reset-password" || path == "/api/auth/providers" || path == "/api/auth/discovery" || strings.HasPrefix(path, "/api/auth/enterprise/") || strings.HasPrefix(path, "/api/auth/google/") || strings.HasPrefix(path, "/api/auth/oidc/") || strings.HasPrefix(path, "/api/auth/saml/") || strings.HasPrefix(path, "/api/invitations/preview/") || strings.HasPrefix(path, "/api/calendar/cycles/") || strings.HasPrefix(path, "/api/email-intake/") || strings.HasPrefix(path, "/api/integrations/") && (strings.HasSuffix(path, "/webhook") || strings.HasSuffix(path, "/oauth/callback")) || strings.HasPrefix(path, "/api/shared/views/") || strings.HasPrefix(path, "/api/shared/dashboards/")
}

func (s *server) authorizeWorkspaceRequest(w http.ResponseWriter, r *http.Request, user domain.User) bool {
	key := workspaceKey(r)
	if strings.HasPrefix(r.URL.Path, "/api/workspaces/") {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) >= 3 {
			key = parts[2]
		}
	}
	if key == "" || r.URL.Path == "/api/account/bootstrap" || r.URL.Path == "/api/invitations/accept" || (r.Method == http.MethodPost && r.URL.Path == "/api/workspaces") {
		return true
	}
	data, ok := s.store.BootstrapFor(key)
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return false
	}
	if _, apiAuthenticated := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); !apiAuthenticated {
		cookie, err := r.Cookie(sessionCookieName)
		durationDays := data.WorkspaceSettings.SessionDurationDays
		if durationDays < 1 {
			durationDays = 30
		}
		if err != nil || !s.store.EnforceSessionDuration(r.Context(), cookie.Value, durationDays) {
			clearSessionCookie(w, r)
			writeError(w, http.StatusUnauthorized, "Your workspace session has expired")
			return false
		}
	}
	role, status, err := s.store.WorkspaceRole(r.Context(), data.Workspace.ID, user.ID)
	if err != nil || status != "active" {
		writeError(w, http.StatusForbidden, "You don't have access to this workspace")
		return false
	}
	trashResourceType := trashRestoreResourceType(data, r)
	if (adminOnlyRequest(r) || trashResourceType == "release_pipeline") && !workspaceAdminRole(role) {
		writeError(w, http.StatusForbidden, "Workspace admin access required")
		return false
	}
	if teamManagementRequest(r) && !workspaceAdminRole(role) && !membershipSelfServiceRequest(r, user.ID) {
		teamID := teamIDFromWorkspacePath(r.URL.Path)
		teamRole, err := s.store.TeamRole(r.Context(), data.Workspace.ID, teamID, user.ID)
		if err != nil || teamRole != "owner" {
			writeError(w, http.StatusForbidden, "Team owner access required")
			return false
		}
	}
	if membershipSelfServiceRequest(r, user.ID) {
		teamID := teamIDFromWorkspacePath(r.URL.Path)
		settings := data.TeamSettings[teamID]
		if role == "guest" || strings.EqualFold(settings.Access, "private") || strings.EqualFold(settings.Access, "restricted") || strings.EqualFold(settings.MembershipRestriction, "members") || strings.EqualFold(settings.MembershipRestriction, "owners") {
			writeError(w, http.StatusForbidden, "This team requires an invitation")
			return false
		}
	}
	if role == "guest" && guestRestrictedPath(r.URL.Path) {
		writeError(w, http.StatusForbidden, "Guests cannot access this workspace resource")
		return false
	}
	if permission := permissionForRequest(r); permission != "" && !workspacePermissionAllows(data.WorkspaceSettings, permission, role) {
		writeError(w, http.StatusForbidden, "Your workspace role cannot perform this action")
		return false
	}
	feature := featureForPath(r.URL.Path)
	if feature == "" && (trashResourceType == "release" || trashResourceType == "release_pipeline") {
		feature = "releases"
	}
	if feature != "" && data.WorkspaceSettings.FeatureFlags != nil {
		if enabled, configured := data.WorkspaceSettings.FeatureFlags[feature]; configured && !enabled {
			writeError(w, http.StatusForbidden, "This workspace feature is disabled")
			return false
		}
	}
	if !s.resourceAllowed(r, key, user.ID) {
		writeError(w, http.StatusForbidden, "This resource is outside your teams")
		return false
	}
	return true
}

func trashRestoreResourceType(data domain.Bootstrap, r *http.Request) string {
	if r.Method != http.MethodPost {
		return ""
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 4 || parts[0] != "api" || parts[1] != "trash" || parts[3] != "restore" {
		return ""
	}
	for _, item := range data.Trash {
		if item.ID == parts[2] {
			return item.ResourceType
		}
	}
	return ""
}

func featureForPath(path string) string {
	if strings.HasPrefix(path, "/api/issues/") && strings.HasSuffix(path, "/releases") {
		return "releases"
	}
	for prefix, feature := range map[string]string{"/api/documents": "documents", "/api/customers": "customer-requests", "/api/customer-requests": "customer-requests", "/api/releases": "releases", "/api/release-pipelines": "releases", "/api/asks": "asks", "/api/initiatives": "initiatives", "/api/dashboards": "dashboards"} {
		if strings.HasPrefix(path, prefix) {
			return feature
		}
	}
	return ""
}

func adminOnlyRequest(r *http.Request) bool {
	path := r.URL.Path
	if strings.Contains(path, "/members/") && !strings.Contains(path, "/teams/") || path == "/api/events" {
		return true
	}
	if strings.HasPrefix(path, "/api/workspace/") {
		return true
	}
	if strings.HasPrefix(path, "/api/oauth-applications") || strings.HasPrefix(path, "/api/project-statuses") {
		return true
	}
	if strings.HasPrefix(path, "/api/identity-providers") || strings.HasPrefix(path, "/api/git-automations") || strings.HasPrefix(path, "/api/target-branches") || strings.HasPrefix(path, "/api/integration-deliveries") {
		return true
	}
	if strings.HasPrefix(path, "/api/webhooks") {
		return true
	}
	if strings.HasPrefix(path, "/api/workflows") || strings.HasPrefix(path, "/api/workflow-runs") {
		return true
	}
	if strings.HasPrefix(path, "/api/migrations") && r.Method != http.MethodGet && r.Method != http.MethodHead {
		return true
	}
	if strings.HasPrefix(path, "/api/release-pipelines") && r.Method != http.MethodGet && r.Method != http.MethodHead {
		return true
	}
	if strings.HasPrefix(path, "/api/workspaces/") && (r.Method == http.MethodPatch || r.Method == http.MethodDelete) && !strings.Contains(path, "/teams/") {
		return true
	}
	return false
}

func permissionForRequest(r *http.Request) string {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return ""
	}
	path := r.URL.Path
	switch {
	case strings.Contains(path, "/invitations"):
		return "invite"
	case strings.HasPrefix(path, "/api/workspaces/") && strings.HasSuffix(path, "/teams") && r.Method == http.MethodPost:
		return "team"
	case strings.HasPrefix(path, "/api/labels"), strings.HasPrefix(path, "/api/label-groups"), strings.Contains(path, "/labels/") || strings.HasSuffix(path, "/labels"):
		return "label"
	case strings.HasPrefix(path, "/api/issue-templates"), strings.HasPrefix(path, "/api/project-templates"), strings.HasPrefix(path, "/api/document-templates"), strings.Contains(path, "/templates/") || strings.HasSuffix(path, "/templates"):
		return "template"
	case strings.HasPrefix(path, "/api/api-keys"):
		return "apiKey"
	default:
		return ""
	}
}

func workspacePermissionAllows(settings domain.WorkspaceSettings, permission, role string) bool {
	if workspaceAdminRole(role) {
		return true
	}
	if role == "guest" {
		return false
	}
	value := "admins"
	switch permission {
	case "invite":
		value = settings.InvitePermission
	case "team":
		value = settings.TeamCreatePermission
	case "label":
		value = settings.LabelPermission
	case "template":
		value = settings.TemplatePermission
	case "apiKey":
		value = settings.APIKeyPermission
	}
	return value == "members" || value == "everyone"
}

func teamManagementRequest(r *http.Request) bool {
	path := r.URL.Path
	return (strings.HasPrefix(path, "/api/workspaces/") && strings.Contains(path, "/teams/") || strings.HasPrefix(path, "/api/teams/")) && r.Method != http.MethodGet
}

func teamIDFromWorkspacePath(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) >= 3 && parts[1] == "teams" {
		return parts[2]
	}
	if len(parts) >= 5 && parts[1] == "workspaces" && parts[3] == "teams" {
		return parts[4]
	}
	return ""
}

func guestRestrictedPath(path string) bool {
	return strings.HasPrefix(path, "/api/initiatives") || strings.HasPrefix(path, "/api/customers") || strings.HasPrefix(path, "/api/customer-requests") || strings.HasPrefix(path, "/api/views") || strings.HasPrefix(path, "/api/analytics") || strings.HasPrefix(path, "/api/dashboards")
}

func (s *server) resourceAllowed(r *http.Request, workspace string, userID string) bool {
	data, ok, err := s.store.BootstrapForUser(r.Context(), workspace, userID)
	if err != nil || !ok {
		return false
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	teamAllowed := func(teamID string) bool {
		if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok && key.TeamRestriction == "selected" && !slices.Contains(key.TeamIDs, teamID) {
			return false
		}
		return slices.ContainsFunc(data.Teams, func(item domain.Team) bool { return item.ID == teamID })
	}
	issueAllowed := func(issueID string) bool {
		return slices.ContainsFunc(data.Issues, func(item domain.Issue) bool {
			if item.ID != issueID {
				return false
			}
			return teamAllowed(item.Team.ID) || issueRole(s, data, item) != "none"
		})
	}
	projectAllowed := func(projectID string) bool {
		return slices.ContainsFunc(data.Projects, func(item domain.Project) bool {
			return item.ID == projectID && (len(item.TeamIDs) == 0 || slices.ContainsFunc(item.TeamIDs, teamAllowed))
		})
	}
	initiativeAllowed := func(initiativeID string) bool {
		return slices.ContainsFunc(data.Initiatives, func(item domain.Initiative) bool {
			if item.ID != initiativeID {
				return false
			}
			if len(item.ProjectIDs) == 0 && len(item.ContributingTeamIDs) == 0 && item.LeadTeamID == "" {
				return data.ViewerRole != "guest"
			}
			if item.LeadTeamID != "" && teamAllowed(item.LeadTeamID) {
				return true
			}
			if slices.ContainsFunc(item.ContributingTeamIDs, teamAllowed) {
				return true
			}
			return slices.ContainsFunc(item.ProjectIDs, projectAllowed)
		})
	}
	viewAllowed := func(viewID string) bool {
		return slices.ContainsFunc(data.SavedViews, func(item domain.SavedView) bool {
			if item.ID != viewID && item.SlugID != viewID {
				return false
			}
			if item.Scope == "personal" {
				return item.OwnerID == userID || workspaceAdminRole(data.ViewerRole)
			}
			if item.Scope == "team" && item.TeamID != "" && !teamAllowed(item.TeamID) {
				return false
			}
			return item.ProjectID == "" || projectAllowed(item.ProjectID)
		})
	}
	dashboardAllowed := func(dashboardID string) bool {
		items := settingCollection[domain.Dashboard](data, dashboardsSettingsKey)
		return slices.ContainsFunc(items, func(item domain.Dashboard) bool {
			return item.ID == dashboardID && dashboardVisible(data, userID, item)
		})
	}
	cycleAllowed := func(cycleID string) bool {
		return slices.ContainsFunc(data.Cycles, func(item domain.Cycle) bool { return item.ID == cycleID && teamAllowed(item.TeamID) })
	}
	customerAllowed := func(customerID string) bool {
		// Customer records are workspace-level, but guests and API keys scoped to
		// teams must not access customer data outside their visible issues.
		if data.ViewerRole == "guest" {
			return false
		}
		return slices.ContainsFunc(data.Customers, func(item domain.Customer) bool { return item.ID == customerID })
	}
	customerRequestAllowed := func(requestID string) bool {
		return slices.ContainsFunc(data.CustomerRequests, func(item domain.CustomerRequest) bool {
			if item.ID != requestID || !customerAllowed(item.CustomerID) {
				return false
			}
			if item.IssueID != "" && !issueAllowed(item.IssueID) {
				return false
			}
			if item.ProjectID != "" && !projectAllowed(item.ProjectID) {
				return false
			}
			return true
		})
	}
	askAllowed := func(askID string) bool {
		return slices.ContainsFunc(data.Asks, func(item domain.Ask) bool {
			return item.ID == askID && (item.TeamID == "" || teamAllowed(item.TeamID))
		})
	}
	pipelineAllowed := func(pipelineID string) bool {
		return slices.ContainsFunc(data.ReleasePipelines, func(item domain.ReleasePipeline) bool { return item.ID == pipelineID })
	}
	releaseAllowed := func(releaseID string) bool {
		return slices.ContainsFunc(data.Releases, func(item domain.Release) bool { return item.ID == releaseID })
	}
	releaseMutationAllowed := func(input releaseInput) bool {
		if input.PipelineID != nil && strings.TrimSpace(*input.PipelineID) != "" && !pipelineAllowed(strings.TrimSpace(*input.PipelineID)) {
			return false
		}
		if input.ProjectIDs != nil && slices.ContainsFunc(*input.ProjectIDs, func(id string) bool { return strings.TrimSpace(id) != "" && !projectAllowed(strings.TrimSpace(id)) }) {
			return false
		}
		if input.IssueIDs != nil && slices.ContainsFunc(*input.IssueIDs, func(id string) bool { return strings.TrimSpace(id) != "" && !issueAllowed(strings.TrimSpace(id)) }) {
			return false
		}
		return true
	}
	pipelineMutationAllowed := func(input releasePipelineInput) bool {
		return input.TeamIDs == nil || !slices.ContainsFunc(*input.TeamIDs, func(id string) bool { return strings.TrimSpace(id) != "" && !teamAllowed(strings.TrimSpace(id)) })
	}
	mutationAllowed := func(input domain.IssueUpdateInput) bool {
		if input.ProjectID != nil && *input.ProjectID != "" && !projectAllowed(*input.ProjectID) {
			return false
		}
		if input.CycleID != nil && *input.CycleID != "" && !slices.ContainsFunc(data.Cycles, func(item domain.Cycle) bool { return item.ID == *input.CycleID }) {
			return false
		}
		if input.AssigneeID != nil && *input.AssigneeID != "" && !slices.ContainsFunc(data.Users, func(item domain.User) bool { return item.ID == *input.AssigneeID }) {
			return false
		}
		if input.ParentID != nil && *input.ParentID != "" && !issueAllowed(*input.ParentID) {
			return false
		}
		return true
	}
	projectMutationAllowed := func(input domain.ProjectMutationInput) bool {
		if slices.ContainsFunc(input.TeamIDs, func(id string) bool { return !teamAllowed(id) }) || slices.ContainsFunc(input.DependencyIDs, func(id string) bool { return !projectAllowed(id) }) {
			return false
		}
		if input.LeadID != nil && *input.LeadID != "" && !slices.ContainsFunc(data.Users, func(item domain.User) bool { return item.ID == *input.LeadID }) {
			return false
		}
		if slices.ContainsFunc(input.MemberIDs, func(id string) bool {
			return !slices.ContainsFunc(data.Users, func(item domain.User) bool { return item.ID == id })
		}) || slices.ContainsFunc(input.Initiatives, func(id string) bool {
			return !slices.ContainsFunc(data.Initiatives, func(item domain.Initiative) bool { return item.ID == id })
		}) || slices.ContainsFunc(input.Customers, func(id string) bool {
			return !slices.ContainsFunc(data.Customers, func(item domain.Customer) bool { return item.ID == id })
		}) {
			return false
		}
		return true
	}
	resourceReferenceAllowed := func(kind, id string) bool {
		kind = strings.ToLower(strings.TrimSpace(kind))
		switch kind {
		case "issue":
			return issueAllowed(id)
		case "project":
			return projectAllowed(id)
		case "initiative":
			return initiativeAllowed(id)
		case "view":
			return viewAllowed(id)
		case "dashboard":
			return dashboardAllowed(id)
		case "cycle":
			return cycleAllowed(id)
		case "release":
			return releaseAllowed(id)
		case "document":
			return slices.ContainsFunc(data.Documents, func(item domain.Document) bool {
				return (item.ID == id || item.SlugID == id) && documentRole(s, data, item) != "none"
			})
		case "customer":
			return customerAllowed(id)
		case "customer_request":
			return customerRequestAllowed(id)
		case "ask":
			return askAllowed(id)
		default:
			return false
		}
	}
	if len(parts) < 2 {
		return true
	}
	switch parts[1] {
	case "issues":
		if len(parts) == 2 && r.Method == http.MethodPost {
			var input domain.IssueCreateInput
			if !peekRequestJSON(r, &input) {
				return false
			}
			if input.TeamID != "" && !teamAllowed(input.TeamID) || input.ParentID != nil && !issueAllowed(*input.ParentID) {
				return false
			}
			return mutationAllowed(domain.IssueUpdateInput{ProjectID: input.ProjectID, CycleID: input.CycleID, AssigneeID: input.AssigneeID})
		}
		if len(parts) < 3 {
			return true
		}
		if parts[2] == "batch" {
			var input domain.BatchIssueUpdateInput
			if !peekRequestJSON(r, &input) || slices.ContainsFunc(input.IssueIDs, func(id string) bool { return !issueAllowed(id) }) {
				return false
			}
			return mutationAllowed(input.Update)
		}
		if !issueAllowed(parts[2]) {
			return false
		}
		issueIndex := slices.IndexFunc(data.Issues, func(item domain.Issue) bool { return item.ID == parts[2] })
		if issueIndex < 0 {
			return false
		}
		issue := data.Issues[issueIndex]
		roleRank := issuePermissionRank(issueRole(s, data, issue))
		canComment := roleRank >= issuePermissionRank("commenter")
		if !canComment && roleRank >= issuePermissionRank("viewer") && data.ViewerRole != "guest" {
			// Public teams are readable by every workspace member, and those
			// members can participate in the discussion even when they are not
			// explicitly listed as team members. Explicitly shared private issues
			// remain read-only unless granted commenter access.
			settings := data.TeamSettings[issue.Team.ID]
			canComment = !issue.Team.Private && !strings.EqualFold(settings.Access, "private") && !strings.EqualFold(settings.Access, "restricted")
		}
		// Issue collaborators need at least commenter access to mutate a
		// discussion. Viewers may still read the issue and its comments, but
		// cannot create, edit, or delete comments or reactions.
		if len(parts) >= 4 && parts[3] == "comments" {
			if len(parts) == 4 && r.Method == http.MethodPost {
				return canComment
			}
			if len(parts) >= 5 {
				if len(parts) == 6 && parts[5] == "reactions" && r.Method == http.MethodPost {
					return canComment
				}
				commentIndex := slices.IndexFunc(data.Comments[parts[2]], func(comment domain.Comment) bool { return comment.ID == parts[4] })
				if commentIndex < 0 {
					return false
				}
				if r.Method == http.MethodPatch || r.Method == http.MethodDelete {
					comment := data.Comments[parts[2]][commentIndex]
					return roleRank >= issuePermissionRank("editor") || comment.User.ID == userID
				}
			}
		}
		if len(parts) >= 4 && parts[3] == "attachments" && (r.Method == http.MethodPost || r.Method == http.MethodDelete) {
			return roleRank >= issuePermissionRank("editor")
		}
		if len(parts) >= 4 && parts[3] == "links" && r.Method == http.MethodPost {
			return roleRank >= issuePermissionRank("editor")
		}
		if len(parts) >= 4 && parts[3] == "relations" {
			if roleRank < issuePermissionRank("editor") {
				return false
			}
			if r.Method == http.MethodPost {
				var input struct {
					RelatedIssueID string `json:"relatedIssueId"`
				}
				return peekRequestJSON(r, &input) && issueAllowed(input.RelatedIssueID)
			}
			return true
		}
		if len(parts) >= 4 && parts[3] == "releases" && r.Method == http.MethodPut {
			if roleRank < issuePermissionRank("editor") {
				return false
			}
			var input issueReleasesInput
			return peekRequestJSON(r, &input) && !slices.ContainsFunc(input.ReleaseIDs, func(id string) bool { return !releaseAllowed(id) })
		}
		if len(parts) >= 4 && (parts[3] == "reminders" || parts[3] == "loop-runs") && r.Method == http.MethodPost {
			return roleRank >= issuePermissionRank("editor")
		}
		if len(parts) == 3 && r.Method == http.MethodDelete {
			return roleRank >= issuePermissionRank("editor")
		}
		if r.Method == http.MethodPatch {
			if issueIndex := slices.IndexFunc(data.Issues, func(item domain.Issue) bool { return item.ID == parts[2] }); issueIndex >= 0 && issuePermissionRank(issueRole(s, data, data.Issues[issueIndex])) < issuePermissionRank("editor") {
				return false
			}
			var input domain.IssueUpdateInput
			return peekRequestJSON(r, &input) && mutationAllowed(input)
		}
		return true
	case "projects":
		if r.Method == http.MethodPost && len(parts) == 2 {
			var input domain.ProjectMutationInput
			if !peekRequestJSON(r, &input) {
				return false
			}
			return projectMutationAllowed(input)
		}
		if len(parts) < 3 {
			return true
		}
		if !projectAllowed(parts[2]) {
			return false
		}
		if len(parts) >= 4 && parts[3] == "relations" && r.Method == http.MethodPost {
			var input domain.ProjectRelation
			return peekRequestJSON(r, &input) && projectAllowed(input.RelatedProjectID)
		}
		if len(parts) >= 5 && parts[3] == "relations" {
			return slices.ContainsFunc(data.ProjectRelations, func(relation domain.ProjectRelation) bool {
				return relation.ID == parts[4] && relation.ProjectID == parts[2] && projectAllowed(relation.RelatedProjectID)
			})
		}
		if r.Method == http.MethodPatch {
			var input domain.ProjectMutationInput
			return peekRequestJSON(r, &input) && projectMutationAllowed(input)
		}
		return true
	case "initiatives":
		if len(parts) == 2 && r.Method == http.MethodPost {
			return data.ViewerRole != "guest"
		}
		if len(parts) < 3 {
			return data.ViewerRole != "guest"
		}
		if !initiativeAllowed(parts[2]) {
			return false
		}
		if len(parts) >= 4 && parts[3] == "relations" && r.Method == http.MethodPost {
			var input domain.InitiativeRelation
			return peekRequestJSON(r, &input) && initiativeAllowed(input.RelatedInitiativeID)
		}
		if len(parts) >= 5 && parts[3] == "relations" {
			return slices.ContainsFunc(data.InitiativeRelations, func(relation domain.InitiativeRelation) bool {
				return relation.ID == parts[4] && relation.InitiativeID == parts[2] && initiativeAllowed(relation.RelatedInitiativeID)
			})
		}
		if r.Method == http.MethodPatch {
			var input domain.InitiativeMutationInput
			if !peekRequestJSON(r, &input) {
				return false
			}
			if input.LeadTeamID != nil && *input.LeadTeamID != "" && !teamAllowed(*input.LeadTeamID) {
				return false
			}
			if input.ContributingTeamIDs != nil && slices.ContainsFunc(*input.ContributingTeamIDs, func(id string) bool { return !teamAllowed(id) }) {
				return false
			}
			if input.ProjectIDs != nil && slices.ContainsFunc(*input.ProjectIDs, func(id string) bool { return !projectAllowed(id) }) {
				return false
			}
		}
		return true
	case "views":
		if len(parts) == 2 && r.Method == http.MethodPost {
			if data.ViewerRole == "guest" {
				return false
			}
			var input domain.SavedViewMutationInput
			if !peekRequestJSON(r, &input) {
				return false
			}
			if input.TeamID != nil && *input.TeamID != "" && !teamAllowed(*input.TeamID) {
				return false
			}
			if input.ProjectID != nil && *input.ProjectID != "" && !projectAllowed(*input.ProjectID) {
				return false
			}
			return true
		}
		if len(parts) < 3 {
			return true
		}
		if !viewAllowed(parts[2]) {
			return false
		}
		if r.Method == http.MethodPatch || r.Method == http.MethodDelete || strings.HasSuffix(r.URL.Path, "/share") {
			item, err := savedViewByID(&data, parts[2])
			if err != nil || (item.OwnerID != userID && !workspaceAdminRole(data.ViewerRole)) {
				return false
			}
			if r.Method == http.MethodPatch {
				var input domain.SavedViewMutationInput
				if !peekRequestJSON(r, &input) {
					return false
				}
				if input.OwnerID != nil && *input.OwnerID != "" && *input.OwnerID != item.OwnerID && !workspaceAdminRole(data.ViewerRole) {
					return false
				}
				if input.TeamID != nil && *input.TeamID != "" && !teamAllowed(*input.TeamID) {
					return false
				}
				if input.ProjectID != nil && *input.ProjectID != "" && !projectAllowed(*input.ProjectID) {
					return false
				}
			}
			return true
		}
		return true
	case "dashboards":
		if len(parts) == 2 && r.Method == http.MethodPost {
			if data.ViewerRole == "guest" {
				return false
			}
			var input dashboardInput
			if !peekRequestJSON(r, &input) {
				return false
			}
			if input.TeamIDs != nil && slices.ContainsFunc(*input.TeamIDs, func(id string) bool { return !teamAllowed(id) }) {
				return false
			}
			return true
		}
		if len(parts) < 3 {
			return true
		}
		if !dashboardAllowed(parts[2]) {
			return false
		}
		if r.Method == http.MethodPatch || r.Method == http.MethodDelete || strings.HasSuffix(r.URL.Path, "/share") {
			items := settingCollection[domain.Dashboard](data, dashboardsSettingsKey)
			if !slices.ContainsFunc(items, func(item domain.Dashboard) bool {
				return item.ID == parts[2] && (item.OwnerID == userID || workspaceAdminRole(data.ViewerRole))
			}) {
				return false
			}
			if r.Method == http.MethodPatch {
				var input dashboardInput
				if !peekRequestJSON(r, &input) {
					return false
				}
				if input.OwnerID != nil && *input.OwnerID != "" && *input.OwnerID != userID && !workspaceAdminRole(data.ViewerRole) {
					return false
				}
				if input.TeamIDs != nil && slices.ContainsFunc(*input.TeamIDs, func(id string) bool { return !teamAllowed(id) }) {
					return false
				}
			}
			return true
		}
		return true
	case "customers":
		if len(parts) < 3 {
			return data.ViewerRole != "guest"
		}
		return customerAllowed(parts[2])
	case "customer-requests":
		if len(parts) == 2 && r.Method == http.MethodPost {
			if data.ViewerRole == "guest" {
				return false
			}
			var input customerRequestInput
			if !peekRequestJSON(r, &input) {
				return false
			}
			if input.CustomerID != "" && !customerAllowed(input.CustomerID) || input.IssueID != nil && *input.IssueID != "" && !issueAllowed(*input.IssueID) || input.ProjectID != nil && *input.ProjectID != "" && !projectAllowed(*input.ProjectID) {
				return false
			}
			return true
		}
		if len(parts) < 3 {
			return data.ViewerRole != "guest"
		}
		return customerRequestAllowed(parts[2])
	case "asks":
		if len(parts) == 2 && r.Method == http.MethodPost {
			if data.ViewerRole == "guest" {
				return false
			}
			var input domain.Ask
			if !peekRequestJSON(r, &input) {
				return false
			}
			return input.TeamID == "" || teamAllowed(input.TeamID)
		}
		if len(parts) < 3 {
			return true
		}
		return askAllowed(parts[2])
	case "favorites", "subscriptions":
		if len(parts) < 4 {
			return true
		}
		return resourceReferenceAllowed(parts[2], parts[3])
	case "reviews":
		if len(parts) < 3 {
			return true
		}
		return slices.ContainsFunc(data.Reviews, func(item domain.CodeReview) bool {
			if item.ID != parts[2] {
				return false
			}
			return len(item.IssueIDs) == 0 || slices.ContainsFunc(item.IssueIDs, issueAllowed)
		})
	case "documents":
		// Documents inherit visibility from their team scope. Keep both the
		// document itself and any team/project bindings inside the viewer's
		// allowed projection; this also protects comment and restore routes.
		documentAllowed := func(id string) bool {
			return slices.ContainsFunc(data.Documents, func(item domain.Document) bool {
				if item.ID != id && item.SlugID != id {
					return false
				}
				return len(item.TeamIDs) == 0 || slices.ContainsFunc(item.TeamIDs, teamAllowed) || documentRole(s, data, item) != "none"
			})
		}
		if len(parts) == 2 && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			return true
		}
		if len(parts) == 2 && r.Method == http.MethodPost {
			var input documentInput
			if !peekRequestJSON(r, &input) {
				return false
			}
			if input.TeamIDs != nil && slices.ContainsFunc(*input.TeamIDs, func(id string) bool { return !teamAllowed(strings.TrimSpace(id)) }) {
				return false
			}
			if input.ProjectIDs != nil && slices.ContainsFunc(*input.ProjectIDs, func(id string) bool { return !projectAllowed(strings.TrimSpace(id)) }) {
				return false
			}
			if input.IssueID != nil && strings.TrimSpace(*input.IssueID) != "" && !issueAllowed(strings.TrimSpace(*input.IssueID)) {
				return false
			}
			return true
		}
		if len(parts) < 3 || !documentAllowed(parts[2]) {
			return false
		}
		if r.Method == http.MethodPatch {
			var input documentInput
			if !peekRequestJSON(r, &input) {
				return false
			}
			if input.TeamIDs != nil && slices.ContainsFunc(*input.TeamIDs, func(id string) bool { return !teamAllowed(strings.TrimSpace(id)) }) {
				return false
			}
			if input.ProjectIDs != nil && slices.ContainsFunc(*input.ProjectIDs, func(id string) bool { return !projectAllowed(strings.TrimSpace(id)) }) {
				return false
			}
		}
		return true
	case "releases":
		if len(parts) == 2 && r.Method == http.MethodPost {
			var input releaseInput
			return peekRequestJSON(r, &input) && releaseMutationAllowed(input)
		}
		if len(parts) < 3 {
			return true
		}
		if parts[2] == "reorder" {
			var input reorderInput
			return peekRequestJSON(r, &input) && (input.PipelineID == "" || pipelineAllowed(input.PipelineID)) && !slices.ContainsFunc(input.IDs, func(id string) bool { return !releaseAllowed(id) })
		}
		if !releaseAllowed(parts[2]) {
			return false
		}
		if r.Method == http.MethodPatch {
			var input releaseInput
			return peekRequestJSON(r, &input) && releaseMutationAllowed(input)
		}
		return true
	case "release-pipelines":
		if len(parts) == 2 && r.Method == http.MethodPost {
			var input releasePipelineInput
			return peekRequestJSON(r, &input) && pipelineMutationAllowed(input)
		}
		if len(parts) < 3 {
			return true
		}
		if parts[2] == "reorder" {
			var input reorderInput
			return peekRequestJSON(r, &input) && !slices.ContainsFunc(input.IDs, func(id string) bool { return !pipelineAllowed(id) })
		}
		if !pipelineAllowed(parts[2]) {
			return false
		}
		if r.Method == http.MethodPatch {
			var input releasePipelineInput
			return peekRequestJSON(r, &input) && pipelineMutationAllowed(input)
		}
		return true
	case "cycles":
		if len(parts) < 3 {
			return true
		}
		return cycleAllowed(parts[2])
	case "teams":
		if len(parts) < 3 {
			return true
		}
		return teamAllowed(parts[2])
	case "notifications":
		if len(parts) < 3 {
			return true
		}
		if parts[2] == "batch" {
			return true
		}
		return slices.ContainsFunc(data.Notifications, func(item domain.Notification) bool { return item.ID == parts[2] })
	case "workspaces":
		if len(parts) >= 5 && parts[3] == "teams" {
			return teamAllowed(parts[4])
		}
	}
	return true
}

func peekRequestJSON(r *http.Request, value any) bool {
	if r.Body == nil {
		return false
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	return json.Unmarshal(raw, value) == nil
}

func authUser(r *http.Request) domain.User {
	user, _ := r.Context().Value(authUserContextKey{}).(domain.User)
	return user
}

func (s *server) register(w http.ResponseWriter, r *http.Request) {
	var input struct{ Name, Email, Password string }
	if !decodeJSON(w, r, &input) {
		return
	}
	user, token, err := s.store.Register(r.Context(), input.Name, input.Email, input.Password)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, store.ErrAuthConflict) {
			status = http.StatusConflict
		}
		writeError(w, status, err.Error())
		return
	}
	response := map[string]any{"user": user, "verificationRequired": true}
	if s.mailer != nil {
		if err := s.mailer.sendVerification(user.Email, token); err != nil {
			writeError(w, http.StatusBadGateway, "Account created, but the verification email could not be sent")
			return
		}
	}
	if devAuthTokens() {
		response["verificationToken"] = token
	}
	writeJSON(w, http.StatusCreated, response)
}

func (s *server) resendVerification(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email string `json:"email"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	token, err := s.store.RequestEmailVerification(r.Context(), input.Email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not create verification request")
		return
	}
	if token != "" && s.mailer != nil {
		if err := s.mailer.sendVerification(input.Email, token); err != nil {
			writeError(w, http.StatusBadGateway, "Could not send verification email")
			return
		}
	}
	response := map[string]any{"sent": true}
	if token != "" && devAuthTokens() {
		response["verificationToken"] = token
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *server) verifyEmail(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Token string `json:"token"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := s.store.VerifyEmail(r.Context(), input.Token); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"verified": true})
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	var input struct{ Email, Password string }
	if !decodeJSON(w, r, &input) {
		return
	}
	session, token, err := s.store.Login(r.Context(), input.Email, input.Password)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	setSessionCookie(w, r, token, session.ExpiresAt)
	writeJSON(w, http.StatusOK, session)
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		_ = s.store.Logout(r.Context(), cookie.Value)
	}
	clearSessionCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) authSession(w http.ResponseWriter, r *http.Request) {
	if s.authDisabled {
		account := s.store.Account()
		writeJSON(w, http.StatusOK, domain.AuthSession{User: account.Viewer, Memberships: account.Workspaces, ExpiresAt: time.Now().UTC().Add(24 * time.Hour)})
		return
	}
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Sign in required")
		return
	}
	session, err := s.store.Session(r.Context(), cookie.Value)
	if err != nil {
		clearSessionCookie(w, r)
		writeError(w, http.StatusUnauthorized, "Your session has expired")
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (s *server) setLastWorkspace(w http.ResponseWriter, r *http.Request) {
	var input struct {
		WorkspaceKey string `json:"workspaceKey"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	err := s.store.SetLastWorkspace(r.Context(), authUser(r).ID, strings.TrimSpace(input.WorkspaceKey))
	respondMutation(w, err, http.StatusNoContent, nil)
}

func (s *server) forgotPassword(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email string `json:"email"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	token, err := s.store.RequestPasswordReset(r.Context(), input.Email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not create reset request")
		return
	}
	response := map[string]any{"sent": true}
	if token != "" && s.mailer != nil {
		if err := s.mailer.sendPasswordReset(input.Email, token); err != nil {
			writeError(w, http.StatusBadGateway, "Could not send password reset email")
			return
		}
	}
	if token != "" && devAuthTokens() {
		response["resetToken"] = token
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *server) resetPassword(w http.ResponseWriter, r *http.Request) {
	var input struct{ Token, Password string }
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := s.store.ResetPassword(r.Context(), input.Token, input.Password); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	clearSessionCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]bool{"reset": true})
}

func (s *server) createInvitation(w http.ResponseWriter, r *http.Request) {
	data, ok := s.store.BootstrapFor(r.PathValue("workspaceKey"))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	var input struct {
		Emails  []string `json:"emails"`
		Role    string   `json:"role"`
		TeamIDs []string `json:"teamIds"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Role == "" {
		input.Role = "member"
	}
	if strings.EqualFold(input.Role, "owner") && !s.authDisabled {
		role, _, _ := s.store.WorkspaceRole(r.Context(), data.Workspace.ID, authUser(r).ID)
		if !workspaceAdminRole(role) {
			writeError(w, http.StatusForbidden, "Only workspace owners and admins can invite owners")
			return
		}
	}
	if input.Role == "guest" && !data.WorkspaceSettings.GuestsAllowed {
		writeError(w, http.StatusForbidden, "Guest accounts are disabled for this workspace")
		return
	}
	if s.authDisabled {
		var created []domain.Invitation
		err := s.store.MutateWorkspace(r.Context(), r.PathValue("workspaceKey"), "workspace_invitations.created", strings.Join(input.Emails, ","), input, func(workspace *domain.Bootstrap) error {
			materializeDevelopmentMembers(workspace)
			if !validMemberRole(input.Role) || input.Role == "guest" && len(input.TeamIDs) == 0 || slices.ContainsFunc(input.TeamIDs, func(teamID string) bool {
				return !slices.ContainsFunc(workspace.Teams, func(team domain.Team) bool { return team.ID == teamID })
			}) {
				return errInvalid
			}
			seen := map[string]bool{}
			for _, rawEmail := range input.Emails {
				email := strings.ToLower(strings.TrimSpace(rawEmail))
				if !strings.Contains(email, "@") || seen[email] || slices.ContainsFunc(workspace.Members, func(member domain.WorkspaceMember) bool { return strings.EqualFold(member.User.Email, email) }) || slices.ContainsFunc(workspace.Invitations, func(invitation domain.Invitation) bool {
					return invitation.Status == "pending" && strings.EqualFold(invitation.Email, email)
				}) {
					return errInvalid
				}
				seen[email] = true
			}
			now := time.Now().UTC()
			for _, rawEmail := range input.Emails {
				email := strings.ToLower(strings.TrimSpace(rawEmail))
				invitation := domain.Invitation{ID: fmt.Sprintf("invite_%d", time.Now().UnixNano()), WorkspaceID: workspace.Workspace.ID, Email: email, Role: input.Role, TeamIDs: slices.Clone(input.TeamIDs), Status: "pending", InviterID: workspace.Viewer.ID, Token: randomURLToken(24), ExpiresAt: now.Add(7 * 24 * time.Hour), CreatedAt: now}
				workspace.Invitations = append(workspace.Invitations, invitation)
				created = append(created, invitation)
			}
			return nil
		})
		respondMutation(w, err, http.StatusCreated, created)
		return
	}
	result := make([]domain.Invitation, 0, len(input.Emails))
	for _, email := range input.Emails {
		email = strings.ToLower(strings.TrimSpace(email))
		if len(data.WorkspaceSettings.AllowedDomains) > 0 {
			parts := strings.Split(email, "@")
			if len(parts) != 2 || !slices.Contains(data.WorkspaceSettings.AllowedDomains, parts[1]) {
				writeError(w, http.StatusForbidden, "Email domain is not approved for this workspace")
				return
			}
		}
		item, err := s.store.Invite(r.Context(), data.Workspace.ID, authUser(r).ID, email, input.Role, input.TeamIDs)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if s.mailer != nil {
			if err := s.mailer.sendInvitation(item.Email, data.Workspace.Name, item.Token); err != nil {
				writeError(w, http.StatusBadGateway, "Invitation created, but the email could not be sent")
				return
			}
		}
		if !devAuthTokens() {
			item.Token = ""
		}
		result = append(result, item)
	}
	writeJSON(w, http.StatusCreated, result)
}

func (s *server) invitationPreview(w http.ResponseWriter, r *http.Request) {
	if s.authDisabled {
		for _, key := range s.store.WorkspaceKeys() {
			data, ok := s.store.BootstrapFor(key)
			if !ok {
				continue
			}
			if index := slices.IndexFunc(data.Invitations, func(item domain.Invitation) bool {
				return item.Token == r.PathValue("token") && item.Status == "pending" && item.ExpiresAt.After(time.Now())
			}); index >= 0 {
				item := data.Invitations[index]
				writeJSON(w, http.StatusOK, map[string]any{"id": item.ID, "email": item.Email, "role": item.Role, "teamIds": item.TeamIDs, "expiresAt": item.ExpiresAt, "workspace": data.Workspace})
				return
			}
		}
		writeError(w, http.StatusNotFound, "This invitation is invalid or has expired")
		return
	}
	invitation, workspace, err := s.store.InvitationPreview(r.Context(), r.PathValue("token"))
	if err != nil {
		writeError(w, http.StatusNotFound, "This invitation is invalid or has expired")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": invitation.ID, "email": invitation.Email, "role": invitation.Role,
		"teamIds": invitation.TeamIDs, "expiresAt": invitation.ExpiresAt, "workspace": workspace,
	})
}

func (s *server) revokeInvitation(w http.ResponseWriter, r *http.Request) {
	data, ok := s.store.BootstrapFor(r.PathValue("workspaceKey"))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	if s.authDisabled {
		invitationID := r.PathValue("invitationId")
		err := s.store.MutateWorkspace(r.Context(), r.PathValue("workspaceKey"), "workspace_invitation.revoked", invitationID, nil, func(workspace *domain.Bootstrap) error {
			index := slices.IndexFunc(workspace.Invitations, func(item domain.Invitation) bool { return item.ID == invitationID && item.Status == "pending" })
			if index < 0 {
				return errNotFound
			}
			workspace.Invitations[index].Status = "revoked"
			return nil
		})
		respondMutation(w, err, http.StatusNoContent, nil)
		return
	}
	err := s.store.RevokeInvitation(r.Context(), data.Workspace.ID, r.PathValue("invitationId"))
	respondMutation(w, err, http.StatusNoContent, nil)
}

func (s *server) resendInvitation(w http.ResponseWriter, r *http.Request) {
	data, ok := s.store.BootstrapFor(r.PathValue("workspaceKey"))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	if s.authDisabled {
		invitationID := r.PathValue("invitationId")
		var resent domain.Invitation
		err := s.store.MutateWorkspace(r.Context(), r.PathValue("workspaceKey"), "workspace_invitation.resent", invitationID, nil, func(workspace *domain.Bootstrap) error {
			index := slices.IndexFunc(workspace.Invitations, func(item domain.Invitation) bool { return item.ID == invitationID && item.Status == "pending" })
			if index < 0 {
				return errNotFound
			}
			now := time.Now().UTC()
			workspace.Invitations[index].Token, workspace.Invitations[index].CreatedAt, workspace.Invitations[index].ExpiresAt = randomURLToken(24), now, now.Add(7*24*time.Hour)
			resent = workspace.Invitations[index]
			return nil
		})
		respondMutation(w, err, http.StatusOK, resent)
		return
	}
	invitation, err := s.store.ResendInvitation(r.Context(), data.Workspace.ID, r.PathValue("invitationId"))
	if err == nil && s.mailer != nil {
		err = s.mailer.sendInvitation(invitation.Email, data.Workspace.Name, invitation.Token)
	}
	if !devAuthTokens() {
		invitation.Token = ""
	}
	respondMutation(w, err, http.StatusOK, invitation)
}

func (s *server) acceptInvitation(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Token string `json:"token"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if s.authDisabled {
		for _, key := range s.store.WorkspaceKeys() {
			var membership domain.WorkspaceMembership
			found := false
			err := s.store.MutateWorkspace(r.Context(), key, "workspace_invitation.accepted", input.Token, nil, func(workspace *domain.Bootstrap) error {
				materializeDevelopmentMembers(workspace)
				index := slices.IndexFunc(workspace.Invitations, func(item domain.Invitation) bool {
					return item.Token == input.Token && item.Status == "pending" && item.ExpiresAt.After(time.Now())
				})
				if index < 0 {
					return errNotFound
				}
				invitation := &workspace.Invitations[index]
				userIndex := slices.IndexFunc(workspace.Users, func(user domain.User) bool { return strings.EqualFold(user.Email, invitation.Email) })
				var user domain.User
				if userIndex >= 0 {
					user = workspace.Users[userIndex]
				} else {
					name := strings.Split(invitation.Email, "@")[0]
					user = domain.User{ID: fmt.Sprintf("user_%d", time.Now().UnixNano()), Name: name, DisplayName: name, Email: invitation.Email, Active: true, EmailVerified: true}
					workspace.Users = append(workspace.Users, user)
				}
				now := time.Now().UTC()
				workspace.Members = append(workspace.Members, domain.WorkspaceMember{User: user, Role: invitation.Role, Status: "active", JoinedAt: now, LastSeenAt: &now})
				for _, teamID := range invitation.TeamIDs {
					teamRole := "member"
					if invitation.Role == "owner" {
						teamRole = "owner"
					}
					workspace.TeamMembers = append(workspace.TeamMembers, domain.TeamMember{TeamID: teamID, UserID: user.ID, Role: teamRole, JoinedAt: now})
				}
				invitation.Status, invitation.AcceptedAt = "accepted", &now
				membership = domain.WorkspaceMembership{Workspace: workspace.Workspace, Role: invitation.Role, JoinedAt: now, IssueCount: len(workspace.Issues)}
				found = true
				return nil
			})
			if err == nil && found {
				writeJSON(w, http.StatusOK, membership)
				return
			}
		}
		writeError(w, http.StatusBadRequest, "This invitation is invalid or has expired")
		return
	}
	membership, err := s.store.AcceptInvitation(r.Context(), input.Token, authUser(r).ID)
	respondMutation(w, err, http.StatusOK, membership)
}

func (s *server) updateMemberRole(w http.ResponseWriter, r *http.Request) {
	data, ok := s.store.BootstrapFor(r.PathValue("workspaceKey"))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	var input struct {
		Role        string `json:"role"`
		DisplayName string `json:"displayName"`
		Username    string `json:"username"`
		Email       string `json:"email"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	userID := r.PathValue("userId")
	if s.authDisabled {
		var updated domain.WorkspaceMember
		err := s.store.MutateWorkspace(r.Context(), r.PathValue("workspaceKey"), "workspace_member.updated", userID, input, func(workspace *domain.Bootstrap) error {
			materializeDevelopmentMembers(workspace)
			index := slices.IndexFunc(workspace.Members, func(member domain.WorkspaceMember) bool { return member.User.ID == userID })
			if index < 0 {
				return errNotFound
			}
			member := &workspace.Members[index]
			if input.Role != "" {
				if !validMemberRole(input.Role) || workspaceAdminRole(member.Role) && !workspaceAdminRole(input.Role) && activeAdminCount(workspace) <= 1 {
					return errInvalid
				}
				member.Role = input.Role
			}
			if input.DisplayName != "" || input.Username != "" || input.Email != "" {
				user := member.User
				if input.DisplayName != "" {
					user.DisplayName = strings.TrimSpace(input.DisplayName)
				}
				if input.Username != "" {
					user.Name = strings.TrimSpace(input.Username)
				}
				if input.Email != "" {
					user.Email = strings.ToLower(strings.TrimSpace(input.Email))
				}
				if user.DisplayName == "" || user.Name == "" || !strings.Contains(user.Email, "@") || slices.ContainsFunc(workspace.Members, func(other domain.WorkspaceMember) bool {
					return other.User.ID != user.ID && strings.EqualFold(other.User.Email, user.Email)
				}) {
					return errInvalid
				}
				member.User = user
				cascadeUserIdentity(workspace, user)
			}
			updated = *member
			return nil
		})
		respondMutation(w, err, http.StatusOK, updated)
		return
	}
	if input.Role != "" {
		if err := s.store.UpdateMemberRole(r.Context(), data.Workspace.ID, userID, input.Role); err != nil {
			respondMutation(w, err, http.StatusOK, nil)
			return
		}
	}
	if input.DisplayName != "" || input.Username != "" || input.Email != "" {
		members, _ := s.store.ListMembers(r.Context(), data.Workspace.ID)
		index := slices.IndexFunc(members, func(member domain.WorkspaceMember) bool { return member.User.ID == userID })
		if index < 0 {
			writeError(w, http.StatusNotFound, "member not found")
			return
		}
		current := members[index].User
		displayName, username, email := input.DisplayName, input.Username, input.Email
		if displayName == "" {
			displayName = current.DisplayName
		}
		if username == "" {
			username = current.Name
		}
		if email == "" {
			email = current.Email
		}
		if _, err := s.store.UpdateMemberIdentity(r.Context(), userID, displayName, username, email); err != nil {
			respondMutation(w, err, http.StatusOK, nil)
			return
		}
	}
	members, _ := s.store.ListMembers(r.Context(), data.Workspace.ID)
	for _, member := range members {
		if member.User.ID == userID {
			_ = s.store.MutateWorkspace(r.Context(), r.PathValue("workspaceKey"), "workspace_member.identity_cascaded", userID, nil, func(workspace *domain.Bootstrap) error { cascadeUserIdentity(workspace, member.User); return nil })
			writeJSON(w, http.StatusOK, member)
			return
		}
	}
	writeError(w, http.StatusNotFound, "member not found")
}

func materializeDevelopmentMembers(data *domain.Bootstrap) {
	if len(data.Members) == 0 {
		joined := data.Workspace.CreatedAt
		if joined.IsZero() {
			joined = time.Now().UTC()
			for _, issue := range data.Issues {
				if issue.CreatedAt.Before(joined) {
					joined = issue.CreatedAt
				}
			}
		}
		for _, user := range data.Users {
			role := "member"
			if user.ID == data.Viewer.ID {
				role = "admin"
			}
			member := domain.WorkspaceMember{User: user, Role: role, Status: "active", JoinedAt: joined}
			if user.ID == data.Viewer.ID {
				now := time.Now().UTC()
				member.LastSeenAt = &now
			}
			data.Members = append(data.Members, member)
		}
	}
	if len(data.TeamMembers) == 0 {
		joined := data.Workspace.CreatedAt
		if joined.IsZero() {
			joined = time.Now().UTC()
		}
		for _, team := range data.Teams {
			for _, member := range data.Members {
				if member.Status == "active" {
					data.TeamMembers = append(data.TeamMembers, domain.TeamMember{TeamID: team.ID, UserID: member.User.ID, Role: "member", JoinedAt: joined})
				}
			}
		}
	}
}

func validMemberRole(role string) bool {
	return role == "owner" || role == "admin" || role == "member" || role == "guest"
}

func validWorkspaceRole(role string) bool {
	return role == "owner" || role == "admin" || role == "member" || role == "guest"
}

func activeAdminCount(data *domain.Bootstrap) int {
	count := 0
	for _, member := range data.Members {
		if workspaceAdminRole(member.Role) && member.Status == "active" {
			count++
		}
	}
	return count
}

func cascadeUserIdentity(data *domain.Bootstrap, user domain.User) {
	if index := slices.IndexFunc(data.Users, func(item domain.User) bool { return item.ID == user.ID }); index >= 0 {
		data.Users[index] = user
	}
	if data.Viewer.ID == user.ID {
		data.Viewer = user
	}
	for index := range data.Members {
		if data.Members[index].User.ID == user.ID {
			data.Members[index].User = user
		}
	}
	for index := range data.Issues {
		if data.Issues[index].Creator.ID == user.ID {
			data.Issues[index].Creator = user
		}
		if data.Issues[index].Assignee != nil && data.Issues[index].Assignee.ID == user.ID {
			copy := user
			data.Issues[index].Assignee = &copy
		}
		if data.Issues[index].Delegate != nil && data.Issues[index].Delegate.ID == user.ID {
			copy := user
			data.Issues[index].Delegate = &copy
		}
	}
	for index := range data.Projects {
		if data.Projects[index].Lead != nil && data.Projects[index].Lead.ID == user.ID {
			copy := user
			data.Projects[index].Lead = &copy
		}
	}
	for index := range data.Initiatives {
		if data.Initiatives[index].Creator.ID == user.ID {
			data.Initiatives[index].Creator = user
		}
		if data.Initiatives[index].Owner != nil && data.Initiatives[index].Owner.ID == user.ID {
			copy := user
			data.Initiatives[index].Owner = &copy
		}
	}
	for issueID := range data.Comments {
		for index := range data.Comments[issueID] {
			if data.Comments[issueID][index].User.ID == user.ID {
				data.Comments[issueID][index].User = user
			}
		}
	}
	for projectID := range data.ProjectUpdates {
		for index := range data.ProjectUpdates[projectID] {
			if data.ProjectUpdates[projectID][index].User.ID == user.ID {
				data.ProjectUpdates[projectID][index].User = user
			}
		}
	}
	for initiativeID := range data.InitiativeUpdates {
		for index := range data.InitiativeUpdates[initiativeID] {
			if data.InitiativeUpdates[initiativeID][index].User.ID == user.ID {
				data.InitiativeUpdates[initiativeID][index].User = user
			}
		}
	}
}

func (s *server) suspendMember(w http.ResponseWriter, r *http.Request) {
	data, ok := s.store.BootstrapFor(r.PathValue("workspaceKey"))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	actorID := authUser(r).ID
	if s.authDisabled {
		actorID = data.Viewer.ID
	}
	if r.PathValue("userId") == actorID {
		writeError(w, http.StatusBadRequest, "You cannot suspend yourself")
		return
	}
	if s.authDisabled {
		userID := r.PathValue("userId")
		err := s.store.MutateWorkspace(r.Context(), r.PathValue("workspaceKey"), "workspace_member.suspended", userID, nil, func(workspace *domain.Bootstrap) error {
			materializeDevelopmentMembers(workspace)
			index := slices.IndexFunc(workspace.Members, func(member domain.WorkspaceMember) bool { return member.User.ID == userID })
			if index < 0 {
				return errNotFound
			}
			if workspaceAdminRole(workspace.Members[index].Role) && activeAdminCount(workspace) <= 1 {
				return errInvalid
			}
			workspace.Members[index].Status = "suspended"
			return nil
		})
		if err != nil {
			respondMutation(w, err, http.StatusNoContent, nil)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	err := s.store.SuspendMember(r.Context(), data.Workspace.ID, r.PathValue("userId"))
	respondMutation(w, err, http.StatusNoContent, nil)
}

func (s *server) resumeMember(w http.ResponseWriter, r *http.Request) {
	data, ok := s.store.BootstrapFor(r.PathValue("workspaceKey"))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	userID := r.PathValue("userId")
	if s.authDisabled {
		err := s.store.MutateWorkspace(r.Context(), r.PathValue("workspaceKey"), "workspace_member.resumed", userID, nil, func(workspace *domain.Bootstrap) error {
			materializeDevelopmentMembers(workspace)
			index := slices.IndexFunc(workspace.Members, func(member domain.WorkspaceMember) bool { return member.User.ID == userID })
			if index < 0 || workspace.Members[index].Status != "suspended" {
				return errNotFound
			}
			workspace.Members[index].Status = "active"
			return nil
		})
		if err != nil {
			respondMutation(w, err, http.StatusNoContent, nil)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	err := s.store.ResumeMember(r.Context(), data.Workspace.ID, userID)
	respondMutation(w, err, http.StatusNoContent, nil)
}

func (s *server) removeMember(w http.ResponseWriter, r *http.Request) {
	data, ok := s.store.BootstrapFor(r.PathValue("workspaceKey"))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	actorID := authUser(r).ID
	if s.authDisabled {
		actorID = data.Viewer.ID
	}
	if r.PathValue("userId") == actorID {
		writeError(w, http.StatusBadRequest, "You cannot remove yourself")
		return
	}
	if s.authDisabled {
		userID := r.PathValue("userId")
		err := s.store.MutateWorkspace(r.Context(), r.PathValue("workspaceKey"), "workspace_member.removed", userID, nil, func(workspace *domain.Bootstrap) error {
			materializeDevelopmentMembers(workspace)
			index := slices.IndexFunc(workspace.Members, func(member domain.WorkspaceMember) bool { return member.User.ID == userID })
			if index < 0 {
				return errNotFound
			}
			if workspaceAdminRole(workspace.Members[index].Role) && workspace.Members[index].Status == "active" && activeAdminCount(workspace) <= 1 {
				return errInvalid
			}
			workspace.Members = slices.Delete(workspace.Members, index, index+1)
			workspace.TeamMembers = slices.DeleteFunc(workspace.TeamMembers, func(member domain.TeamMember) bool { return member.UserID == userID })
			return nil
		})
		if err != nil {
			respondMutation(w, err, http.StatusNoContent, nil)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	err := s.store.RemoveMember(r.Context(), data.Workspace.ID, r.PathValue("userId"))
	respondMutation(w, err, http.StatusNoContent, nil)
}

func (s *server) updateTeamMember(w http.ResponseWriter, r *http.Request) {
	data, ok := s.store.BootstrapFor(r.PathValue("workspaceKey"))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	var input struct {
		Role   string `json:"role"`
		Member bool   `json:"member"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Role == "" {
		input.Role = "member"
	}
	if s.authDisabled {
		teamID, userID := r.PathValue("teamId"), r.PathValue("userId")
		err := s.store.MutateWorkspace(r.Context(), r.PathValue("workspaceKey"), "team_member.updated", userID, input, func(workspace *domain.Bootstrap) error {
			materializeDevelopmentMembers(workspace)
			if !slices.ContainsFunc(workspace.Teams, func(team domain.Team) bool { return team.ID == teamID }) || !slices.ContainsFunc(workspace.Members, func(member domain.WorkspaceMember) bool { return member.User.ID == userID && member.Status == "active" }) || input.Role != "member" && input.Role != "owner" {
				return errInvalid
			}
			index := slices.IndexFunc(workspace.TeamMembers, func(member domain.TeamMember) bool { return member.TeamID == teamID && member.UserID == userID })
			if input.Member {
				if index >= 0 {
					workspace.TeamMembers[index].Role = input.Role
				} else {
					workspace.TeamMembers = append(workspace.TeamMembers, domain.TeamMember{TeamID: teamID, UserID: userID, Role: input.Role, JoinedAt: time.Now().UTC()})
				}
			} else if index >= 0 {
				workspace.TeamMembers = slices.Delete(workspace.TeamMembers, index, index+1)
			}
			return nil
		})
		if err != nil {
			respondMutation(w, err, http.StatusNoContent, nil)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	err := s.store.SetTeamMembership(r.Context(), data.Workspace.ID, r.PathValue("teamId"), r.PathValue("userId"), input.Role, input.Member)
	respondMutation(w, err, http.StatusNoContent, nil)
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: token, Path: "/", HttpOnly: true, Secure: secureCookie(r), SameSite: http.SameSiteLaxMode, Expires: expires, MaxAge: int(time.Until(expires).Seconds())})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", HttpOnly: true, Secure: secureCookie(r), SameSite: http.SameSiteLaxMode, Expires: time.Unix(1, 0), MaxAge: -1})
}

func devAuthTokens() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("FLOW_DEV_AUTH_TOKENS")), "true")
}
func secureCookie(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") || strings.EqualFold(os.Getenv("FLOW_COOKIE_SECURE"), "true")
}
