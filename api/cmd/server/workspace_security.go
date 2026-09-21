package main

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

func deriveAllowedAuthServices(settings domain.WorkspaceSettings, providers []domain.IdentityProvider) []string {
	if len(settings.AllowedAuthServices) > 0 {
		return append([]string{}, settings.AllowedAuthServices...)
	}
	services := []string{}
	if settings.GoogleAuthEnabled {
		services = append(services, "google")
	}
	if settings.EmailAuthEnabled {
		services = append(services, "email")
	}
	for _, provider := range providers {
		if !provider.Enabled {
			continue
		}
		if provider.Type == "saml" && !slices.Contains(services, "saml") {
			services = append(services, "saml")
		}
		if provider.Type == "oidc" && !slices.Contains(services, "saml") {
			// OIDC enterprise IdP is surfaced as SAML/SSO in allowed-auth copy.
			services = append(services, "saml")
		}
	}
	return services
}

func syncAuthServiceFlags(input *domain.WorkspaceSettings) {
	if input == nil {
		return
	}
	if len(input.AllowedAuthServices) == 0 {
		services := []string{}
		if input.GoogleAuthEnabled {
			services = append(services, "google")
		}
		if input.EmailAuthEnabled {
			services = append(services, "email")
		}
		input.AllowedAuthServices = services
		return
	}
	normalized := make([]string, 0, len(input.AllowedAuthServices))
	seen := map[string]bool{}
	for _, service := range input.AllowedAuthServices {
		value := strings.ToLower(strings.TrimSpace(service))
		if value == "" || seen[value] {
			continue
		}
		switch value {
		case "google", "email", "passkey", "saml", "appuser", "appUser":
			if value == "appuser" {
				value = "appUser"
			}
			seen[value] = true
			normalized = append(normalized, value)
		}
	}
	input.AllowedAuthServices = normalized
	input.GoogleAuthEnabled = slices.Contains(normalized, "google")
	input.EmailAuthEnabled = slices.Contains(normalized, "email") || slices.Contains(normalized, "passkey")
}

func authServiceLabels(services []string) []string {
	labels := make([]string, 0, len(services))
	for _, service := range services {
		switch service {
		case "google":
			labels = append(labels, "Google")
		case "email":
			labels = append(labels, "email & passkey")
		case "passkey":
			labels = append(labels, "passkey")
		case "saml":
			labels = append(labels, "SAML/SSO")
		case "appUser":
			labels = append(labels, "app users")
		default:
			labels = append(labels, service)
		}
	}
	return labels
}

func (s *server) deleteWorkspace(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("workspaceKey")
	updated, err := s.store.ScheduleWorkspaceDeletion(r.Context(), key)
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) cancelWorkspaceDeletion(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("workspaceKey")
	updated, err := s.store.CancelWorkspaceDeletion(r.Context(), key)
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) workspaceAccessStatus(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("workspaceKey")
	user := authUser(r)
	userID := ""
	if user.ID != "" {
		userID = user.ID
	}
	exists, hasMembership, role, data, ok := s.store.WorkspaceAccessStatus(r.Context(), key, userID)
	if !ok || !exists {
		writeJSON(w, http.StatusOK, map[string]any{
			"exists":              false,
			"hasMembership":       false,
			"reason":              "not_found",
			"allowedAuthServices": []string{},
			"allowedAuthLabels":   []string{},
		})
		return
	}
	services := deriveAllowedAuthServices(data.WorkspaceSettings, data.IdentityProviders)
	reason := "ok"
	authRestricted := false
	if hasMembership {
		cookie, err := r.Cookie(sessionCookieName)
		if err == nil {
			auth, authErr := s.store.SessionAuthentication(r.Context(), cookie.Value)
			if authErr == nil && !allowedAuthenticationMethod(data, role, auth) {
				reason = "auth_restricted"
				authRestricted = true
			}
		}
	} else {
		reason = "no_access"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"exists":              true,
		"hasMembership":       hasMembership,
		"authRestricted":      authRestricted,
		"reason":              reason,
		"allowedAuthServices": services,
		"allowedAuthLabels":   authServiceLabels(services),
		"workspace": map[string]any{
			"name":                 data.Workspace.Name,
			"urlKey":               data.Workspace.URLKey,
			"deletionRequestedAt":  data.Workspace.DeletionRequestedAt,
		},
	})
}


func ensureAllowedAuthServices(input *domain.WorkspaceSettings, previous domain.WorkspaceSettings, patch map[string]json.RawMessage) {
	if input == nil {
		return
	}
	if _, ok := patch["allowedAuthServices"]; ok {
		syncAuthServiceFlags(input)
		return
	}
	if _, hasGoogle := patch["googleAuthEnabled"]; hasGoogle {
		// fallthrough
	} else if _, hasEmail := patch["emailAuthEnabled"]; !hasEmail {
		if len(input.AllowedAuthServices) == 0 {
			syncAuthServiceFlags(input)
		}
		return
	}
	services := []string{}
	if input.GoogleAuthEnabled {
		services = append(services, "google")
	}
	if input.EmailAuthEnabled {
		services = append(services, "email")
	}
	for _, service := range previous.AllowedAuthServices {
		if service == "passkey" || service == "saml" || service == "appUser" {
			if !slices.Contains(services, service) {
				services = append(services, service)
			}
		}
	}
	input.AllowedAuthServices = services
}
