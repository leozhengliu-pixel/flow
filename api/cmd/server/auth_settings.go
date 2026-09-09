package main

import (
	"net/http"
	"strings"

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

func (s *server) authorizeAuthenticationPolicy(w http.ResponseWriter, r *http.Request, data domain.Bootstrap, role string) bool {
	if _, apiKey := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); apiKey {
		return true
	}
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return false
	}
	auth, err := s.store.SessionAuthentication(r.Context(), cookie.Value)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "could not validate authentication policy")
		return false
	}
	if !allowedAuthenticationMethod(data, role, auth) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Sign in using an authentication method allowed by this workspace", "code": "authentication_method_required"})
		return false
	}
	if strings.HasPrefix(r.URL.Path, "/api/account/mfa") || (r.Method == http.MethodGet && r.URL.Path == "/api/account/passkeys") {
		return true
	}
	if strings.HasPrefix(r.URL.Path, "/api/account/passkeys/register/") {
		enrolled := false
		for _, passkey := range s.accountPasskeys(authUser(r).ID) {
			if passkey.key.UserID == authUser(r).ID {
				enrolled = true
				break
			}
		}
		if !enrolled {
			return true
		}
	}
	if data.WorkspaceSettings.RequireTwoFactor && auth.MFAVerifiedAt == nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Verify a second authentication factor to access this workspace", "code": "mfa_required"})
		return false
	}
	return true
}
