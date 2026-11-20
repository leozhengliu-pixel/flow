package main

import (
	"net/http"
	"strings"
	"time"

	"flow/api/internal/domain"
)

const (
	// A registered factor may be added or removed only shortly after the
	// session proved it holds one.
	factorChangeVerificationWindow = 10 * time.Minute
	// The first factor of an account is trusted on first use, so only a
	// session that signed in just now may enrol it.
	firstFactorSessionWindow = 15 * time.Minute
)

type accountFactorChange int

const (
	notFactorChange accountFactorChange = iota
	factorRegistration
	factorRename
	factorRemoval
)

func accountFactorRoute(r *http.Request) accountFactorChange {
	const passkeys = "/api/account/passkeys/"
	switch {
	case r.Method == http.MethodPost && (r.URL.Path == passkeys+"register/start" || r.URL.Path == passkeys+"register/finish"):
		return factorRegistration
	case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, passkeys) && len(r.URL.Path) > len(passkeys):
		return factorRename
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, passkeys) && len(r.URL.Path) > len(passkeys):
		return factorRemoval
	}
	return notFactorChange
}

// accountRequiresTwoFactor reports whether any workspace the user belongs to
// requires a second factor. Account routes carry no workspace header the
// server could trust, so the strictest workspace decides.
func (s *server) accountRequiresTwoFactor(r *http.Request, userID string) (bool, bool) {
	account, err := s.store.OAuthAccountForUser(r.Context(), userID)
	if err != nil {
		return false, false
	}
	for _, membership := range account.Workspaces {
		data, ok := s.store.WorkspaceSettingsMetadata(membership.Workspace.URLKey)
		if !ok {
			return false, false
		}
		if data.WorkspaceSettings.RequireTwoFactor {
			return true, true
		}
	}
	return false, true
}

func (s *server) refuseAccountFactor(w http.ResponseWriter, code, message string) bool {
	writeJSON(w, http.StatusForbidden, map[string]string{"error": message, "code": code})
	return false
}

// authorizeAccountFactorChange guards the routes that enrol, rename or remove
// the account's second factors. They are account-scoped, so the workspace
// header (absent or chosen by the caller) never decides:
//   - with an enrolled factor, the session must hold a verification (any
//     workspace requiring two-factor makes that mandatory even for renames),
//     and adding or removing a factor needs one made in the last 10 minutes;
//   - the first factor is trusted on first use, but only from a session that
//     was created in the last 15 minutes.
func (s *server) authorizeAccountFactorChange(w http.ResponseWriter, r *http.Request, user domain.User) bool {
	kind := accountFactorRoute(r)
	if kind == notFactorChange || s.authDisabled {
		return true
	}
	if _, apiKey := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); apiKey {
		return true
	}
	principal := s.policyPrincipalFor(r.Context(), r)
	if principal.err != nil {
		writeError(w, http.StatusServiceUnavailable, "could not validate authentication policy")
		return false
	}
	if !principal.found || principal.chat {
		return s.refuseAccountFactor(w, "mfa_required", "Verify a second authentication factor to change it")
	}
	required, ok := s.accountRequiresTwoFactor(r, user.ID)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "could not validate authentication policy")
		return false
	}
	enrolled := false
	for _, passkey := range s.accountPasskeys(user.ID) {
		if passkey.key.UserID == user.ID {
			enrolled = true
			break
		}
	}
	now := time.Now()
	if !enrolled {
		if kind != factorRegistration {
			return true
		}
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil {
			return s.refuseAccountFactor(w, "session_reauth_required", "Sign in again to add your first passkey")
		}
		created, err := s.store.SessionCreatedAt(r.Context(), cookie.Value)
		if err != nil || now.After(created.Add(firstFactorSessionWindow)) {
			return s.refuseAccountFactor(w, "session_reauth_required", "Sign in again to add your first passkey")
		}
		return true
	}
	verified := principal.auth.MFAVerifiedAt
	if verified == nil && required {
		return s.refuseAccountFactor(w, "mfa_required", "Verify a second authentication factor to access this workspace")
	}
	if kind != factorRename && (verified == nil || now.After(verified.Add(factorChangeVerificationWindow))) {
		return s.refuseAccountFactor(w, "mfa_reauth_required", "Verify your passkey again to change your sign-in factors")
	}
	return true
}
