package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func verifiedMFAClaim(claims map[string]any) bool {
	values, _ := claims["amr"].([]any)
	for _, value := range values {
		if value == "mfa" {
			return true
		}
	}
	return false
}

func allowedAuthenticationMethod(data domain.Bootstrap, role string, auth store.SessionAuthentication) bool {
	if workspaceAdminRole(role) && !data.WorkspaceSettings.DisableAdminBypass {
		return true
	}
	if auth.Provider == "legacy" && (!data.WorkspaceSettings.EmailAuthEnabled || !data.WorkspaceSettings.GoogleAuthEnabled) {
		return false
	}
	if auth.Provider == "password" && !data.WorkspaceSettings.EmailAuthEnabled {
		return false
	}
	if auth.Provider == "google" && !data.WorkspaceSettings.GoogleAuthEnabled {
		return false
	}
	enforced := false
	for _, provider := range data.IdentityProviders {
		if !provider.Enabled || !provider.Enforced {
			continue
		}
		enforced = true
		if auth.Provider == "enterprise:"+provider.ID || ((auth.Provider == "oidc" || auth.Provider == "saml") && strings.TrimRight(auth.Issuer, "/") == strings.TrimRight(provider.Issuer, "/")) {
			return true
		}
	}
	return !enforced
}

func allowedAuthenticationMethodPolicy(data domain.Bootstrap, role string, auth store.SessionAuthentication, enforced, matched bool) bool {
	if workspaceAdminRole(role) && !data.WorkspaceSettings.DisableAdminBypass {
		return true
	}
	if auth.Provider == "legacy" && (!data.WorkspaceSettings.EmailAuthEnabled || !data.WorkspaceSettings.GoogleAuthEnabled) {
		return false
	}
	if auth.Provider == "password" && !data.WorkspaceSettings.EmailAuthEnabled {
		return false
	}
	if auth.Provider == "google" && !data.WorkspaceSettings.GoogleAuthEnabled {
		return false
	}
	return !enforced || matched
}

// authPolicyFailure is a refused authentication-policy check. A zero status
// refuses without writing a response (the request carried no session).
type authPolicyFailure struct {
	status int
	body   map[string]string
}

func (f *authPolicyFailure) write(w http.ResponseWriter) {
	if f.status != 0 {
		writeJSON(w, f.status, f.body)
	}
}

// policyPrincipal resolves the Flow authentication of a request once: the
// verified chat identity, or the browser session cookie. found is false for a
// request with neither (API keys are filtered out by the callers).
type policyPrincipal struct {
	auth  store.SessionAuthentication
	chat  bool
	found bool
	err   error
}

func (s *server) policyPrincipalFor(ctx context.Context, r *http.Request) policyPrincipal {
	if auth, chat := chatIdentityAuthentication(r); chat {
		return policyPrincipal{auth: auth, chat: true, found: true}
	}
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return policyPrincipal{}
	}
	auth, err := s.store.SessionAuthentication(ctx, cookie.Value)
	return policyPrincipal{auth: auth, found: err == nil, err: err}
}

// workspaceAuthenticationFailure checks a principal against one workspace's
// sign-in methods and second-factor requirement. The workspace's identity
// providers are always read here: the metadata handed in by the request path
// carries settings only.
func (s *server) workspaceAuthenticationFailure(ctx context.Context, workspace string, data domain.Bootstrap, role string, principal policyPrincipal, mfaExempt bool) *authPolicyFailure {
	if principal.err != nil {
		return &authPolicyFailure{http.StatusServiceUnavailable, map[string]string{"error": "could not validate authentication policy"}}
	}
	if !principal.found {
		return &authPolicyFailure{}
	}
	auth := principal.auth
	enforced, matched := false, false
	bounded := s.store.BoundedMetadataEnabled()
	if bounded {
		var err error
		enforced, matched, err = s.store.AuthenticationProviderPolicy(ctx, workspace, auth)
		if errors.Is(err, sql.ErrNoRows) {
			return &authPolicyFailure{http.StatusForbidden, map[string]string{"error": "Workspace authentication policy is unavailable"}}
		}
		if err != nil {
			return &authPolicyFailure{http.StatusServiceUnavailable, map[string]string{"error": "could not validate authentication policy"}}
		}
	} else {
		policy, ok := s.store.WorkspaceMetadataFields(workspace, "identityProviders")
		if !ok {
			return &authPolicyFailure{http.StatusForbidden, map[string]string{"error": "Workspace authentication policy is unavailable"}}
		}
		data.IdentityProviders = policy.IdentityProviders
	}
	if principal.chat && auth.MFAVerifiedAt != nil && !time.Now().Before(auth.MFAVerifiedAt.Add(workspaceSessionDuration(data.WorkspaceSettings))) {
		// Second-factor evidence ages out with the session it came from.
		auth.MFAVerifiedAt = nil
	}
	allowed := false
	if bounded {
		allowed = allowedAuthenticationMethodPolicy(data, role, auth, enforced, matched)
	} else {
		allowed = allowedAuthenticationMethod(data, role, auth)
	}
	if !allowed {
		return &authPolicyFailure{http.StatusForbidden, map[string]string{"error": "Sign in using an authentication method allowed by this workspace", "code": "authentication_method_required"}}
	}
	if !mfaExempt && data.WorkspaceSettings.RequireTwoFactor && auth.MFAVerifiedAt == nil {
		return &authPolicyFailure{http.StatusForbidden, map[string]string{"error": "Verify a second authentication factor to access this workspace", "code": "mfa_required"}}
	}
	return nil
}

func (s *server) authorizeAuthenticationPolicy(w http.ResponseWriter, r *http.Request, data domain.Bootstrap, role string) bool {
	if s.authDisabled {
		return true
	}
	if _, apiKey := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); apiKey {
		return true
	}
	mfaExempt := strings.HasPrefix(r.URL.Path, "/api/account/mfa") || (r.Method == http.MethodGet && r.URL.Path == "/api/account/passkeys")
	if !mfaExempt && strings.HasPrefix(r.URL.Path, "/api/account/passkeys/register/") {
		// A first enrolment is how a locked-out account gets its second factor.
		mfaExempt = len(s.accountPasskeys(authUser(r).ID)) == 0
	}
	failure := s.workspaceAuthenticationFailure(r.Context(), workspaceKey(r), data, role, s.policyPrincipalFor(r.Context(), r), mfaExempt)
	if failure != nil {
		failure.write(w)
		return false
	}
	return true
}

// sessionPolicyChecker answers, for one browser request, whether its sign-in
// satisfies a workspace's authentication policy. Browser-session reads that
// are not scoped to one workspace (uploads, signed URLs, sockets) apply it
// to every workspace they would otherwise reach. API keys and OAuth bearer
// tokens carry their own authorization and pass, as on the workspace routes.
type sessionPolicyChecker func(ctx context.Context, workspace, role string) bool

type sessionPolicyKey struct{}

func (s *server) sessionPolicyChecker(r *http.Request) sessionPolicyChecker {
	if s.authDisabled {
		return func(context.Context, string, string) bool { return true }
	}
	if _, apiKey := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); apiKey {
		return func(context.Context, string, string) bool { return true }
	}
	var once sync.Once
	var principal policyPrincipal
	return func(ctx context.Context, workspace, role string) bool {
		once.Do(func() { principal = s.policyPrincipalFor(ctx, r) })
		data, ok := s.store.WorkspaceSettingsMetadata(workspace)
		if !ok {
			return false
		}
		return principal.found && s.workspaceAuthenticationFailure(ctx, workspace, data, role, principal, false) == nil
	}
}

// sessionSatisfiesWorkspacePolicy reports whether the request's sign-in method
// and second factor satisfy the named workspace's policy for a member of role.
func (s *server) sessionSatisfiesWorkspacePolicy(r *http.Request, workspace, role string) bool {
	return s.sessionPolicyChecker(r)(r.Context(), workspace, role)
}

// withSessionPolicy lets the membership loops of the file reads skip the
// workspaces this request may not enter.
func (s *server) withSessionPolicy(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), sessionPolicyKey{}, s.sessionPolicyChecker(r)))
}

// workspacePolicyAllows is the ctx form of the check. A context without a
// checker belongs to a caller that is not a browser request.
func workspacePolicyAllows(ctx context.Context, workspace, role string) bool {
	check, ok := ctx.Value(sessionPolicyKey{}).(sessionPolicyChecker)
	return !ok || check(ctx, workspace, role)
}
