package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

func newAuthRateLimiter() *authRateLimiter {
	return &authRateLimiter{windows: map[string]authRateWindow{}}
}

func (s *server) limitAuth(action string, limit int, window time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		allowed, retryAfter := s.authLimiter.allow(action+":"+authClientAddress(r), limit, window)
		if !allowed {
			seconds := max(1, int(retryAfter.Round(time.Second)/time.Second))
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			writeError(w, http.StatusTooManyRequests, "Too many attempts. Try again later.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (l *authRateLimiter) allow(key string, limit int, duration time.Duration) (bool, time.Duration) {
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
		return true, 0
	}
	if item.count >= limit {
		return false, time.Until(item.reset)
	}
	item.count++
	l.windows[key] = item
	return true, 0
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

func (s *server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.authDisabled || publicAuthPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		user, apiKey := s.authenticateAPIKey(r)
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

func (s *server) authenticateAPIKey(r *http.Request) (domain.User, *domain.APIKey) {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return domain.User{}, nil
	}
	secret := strings.TrimSpace(header[len("Bearer "):])
	data, ok := s.store.BootstrapFor(workspaceKey(r))
	if !ok {
		return domain.User{}, nil
	}
	hash := secretHash(secret)
	for _, key := range data.APIKeys {
		if key.SecretHash != hash || key.RevokedAt != nil {
			continue
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !slices.Contains(key.Scopes, "write") {
			return domain.User{}, nil
		}
		for _, user := range data.Users {
			if user.ID == key.CreatorID {
				now := time.Now().UTC()
				_ = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "api_key.used", key.ID, nil, func(next *domain.Bootstrap) error {
					if index := slices.IndexFunc(next.APIKeys, func(item domain.APIKey) bool { return item.ID == key.ID }); index >= 0 {
						next.APIKeys[index].LastUsedAt = &now
					}
					return nil
				})
				return user, &key
			}
		}
	}
	return domain.User{}, nil
}

func publicAuthPath(path string) bool {
	return path == "/api/health" || path == "/api/oauth/token" || path == "/api/auth/register" || path == "/api/auth/verify-email" || path == "/api/auth/resend-verification" || path == "/api/auth/login" || path == "/api/auth/logout" || path == "/api/auth/session" || path == "/api/auth/forgot-password" || path == "/api/auth/reset-password" || strings.HasPrefix(path, "/api/invitations/preview/")
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
	if adminOnlyRequest(r) && role != "admin" {
		writeError(w, http.StatusForbidden, "Workspace admin access required")
		return false
	}
	if teamManagementRequest(r) && role != "admin" {
		teamID := teamIDFromWorkspacePath(r.URL.Path)
		teamRole, err := s.store.TeamRole(r.Context(), data.Workspace.ID, teamID, user.ID)
		if err != nil || teamRole != "owner" {
			writeError(w, http.StatusForbidden, "Team owner access required")
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
	if feature := featureForPath(r.URL.Path); feature != "" && data.WorkspaceSettings.FeatureFlags != nil && !data.WorkspaceSettings.FeatureFlags[feature] {
		writeError(w, http.StatusForbidden, "This workspace feature is disabled")
		return false
	}
	if !s.resourceAllowed(r, key, user.ID) {
		writeError(w, http.StatusForbidden, "This resource is outside your teams")
		return false
	}
	return true
}

func featureForPath(path string) string {
	for prefix, feature := range map[string]string{"/api/documents": "documents", "/api/customers": "customer-requests", "/api/customer-requests": "customer-requests", "/api/releases": "releases", "/api/asks": "asks", "/api/initiatives": "initiatives"} {
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
	if role == "admin" {
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
	return strings.HasPrefix(path, "/api/initiatives") || strings.HasPrefix(path, "/api/customers") || strings.HasPrefix(path, "/api/views")
}

func (s *server) resourceAllowed(r *http.Request, workspace string, userID string) bool {
	data, ok, err := s.store.BootstrapForUser(r.Context(), workspace, userID)
	if err != nil || !ok {
		return false
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	teamAllowed := func(teamID string) bool {
		if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok && len(key.TeamIDs) > 0 && !slices.Contains(key.TeamIDs, teamID) {
			return false
		}
		return slices.ContainsFunc(data.Teams, func(item domain.Team) bool { return item.ID == teamID })
	}
	issueAllowed := func(issueID string) bool {
		return slices.ContainsFunc(data.Issues, func(item domain.Issue) bool { return item.ID == issueID && teamAllowed(item.Team.ID) })
	}
	projectAllowed := func(projectID string) bool {
		return slices.ContainsFunc(data.Projects, func(item domain.Project) bool {
			return item.ID == projectID && (len(item.TeamIDs) == 0 || slices.ContainsFunc(item.TeamIDs, teamAllowed))
		})
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
		if len(parts) >= 4 && parts[3] == "relations" && r.Method == http.MethodPost {
			var input struct {
				RelatedIssueID string `json:"relatedIssueId"`
			}
			return peekRequestJSON(r, &input) && issueAllowed(input.RelatedIssueID)
		}
		if r.Method == http.MethodPatch {
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
		if !projectAllowed(parts[2]) {
			return false
		}
		if r.Method == http.MethodPatch {
			var input domain.ProjectMutationInput
			return peekRequestJSON(r, &input) && projectMutationAllowed(input)
		}
		return true
	case "cycles":
		if len(parts) < 3 {
			return true
		}
		return slices.ContainsFunc(data.Cycles, func(item domain.Cycle) bool { return item.ID == parts[2] })
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
	if input.Role == "guest" && !data.WorkspaceSettings.GuestsAllowed {
		writeError(w, http.StatusForbidden, "Guest accounts are disabled for this workspace")
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
	err := s.store.RevokeInvitation(r.Context(), data.Workspace.ID, r.PathValue("invitationId"))
	respondMutation(w, err, http.StatusNoContent, nil)
}

func (s *server) resendInvitation(w http.ResponseWriter, r *http.Request) {
	data, ok := s.store.BootstrapFor(r.PathValue("workspaceKey"))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
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
		Role string `json:"role"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	err := s.store.UpdateMemberRole(r.Context(), data.Workspace.ID, r.PathValue("userId"), input.Role)
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	members, _ := s.store.ListMembers(r.Context(), data.Workspace.ID)
	for _, member := range members {
		if member.User.ID == r.PathValue("userId") {
			writeJSON(w, http.StatusOK, member)
			return
		}
	}
	writeError(w, http.StatusNotFound, "member not found")
}

func (s *server) suspendMember(w http.ResponseWriter, r *http.Request) {
	data, ok := s.store.BootstrapFor(r.PathValue("workspaceKey"))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	if r.PathValue("userId") == authUser(r).ID {
		writeError(w, http.StatusBadRequest, "You cannot suspend yourself")
		return
	}
	err := s.store.SuspendMember(r.Context(), data.Workspace.ID, r.PathValue("userId"))
	respondMutation(w, err, http.StatusNoContent, nil)
}

func (s *server) removeMember(w http.ResponseWriter, r *http.Request) {
	data, ok := s.store.BootstrapFor(r.PathValue("workspaceKey"))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	if r.PathValue("userId") == authUser(r).ID {
		writeError(w, http.StatusBadRequest, "You cannot remove yourself")
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
	err := s.store.SetTeamMembership(r.Context(), data.Workspace.ID, r.PathValue("teamId"), r.PathValue("userId"), input.Role, input.Member)
	respondMutation(w, err, http.StatusNoContent, nil)
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: token, Path: "/", HttpOnly: true, Secure: secureCookie(r), SameSite: http.SameSiteLaxMode, Expires: expires, MaxAge: int(time.Until(expires).Seconds())})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", HttpOnly: true, Secure: secureCookie(r), SameSite: http.SameSiteLaxMode, Expires: time.Unix(1, 0), MaxAge: -1})
}

func devAuthTokens() bool { return os.Getenv("FLOW_DEV_AUTH_TOKENS") != "false" }
func secureCookie(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") || strings.EqualFold(os.Getenv("FLOW_COOKIE_SECURE"), "true")
}
