package main

import (
	"net/http"
	"strings"

	"flow/api/internal/domain"
)

func customerDomainMatches(value string, entries []string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, entry := range entries {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "" {
			continue
		}
		if value == entry {
			return true
		}
		if !strings.Contains(entry, "@") {
			domain := strings.TrimLeft(entry, "@.")
			candidate := value
			if at := strings.LastIndex(candidate, "@"); at >= 0 {
				candidate = candidate[at+1:]
			}
			if candidate == domain || strings.HasSuffix(candidate, "."+domain) {
				return true
			}
		}
	}
	return false
}

func (s *server) allowManualCustomerEdit(w http.ResponseWriter, r *http.Request) bool {
	if _, apiKey := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); apiKey {
		return true
	}
	data, ok := s.store.WorkspaceMetadata(workspaceKey(r))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return false
	}
	if !data.WorkspaceSettings.FeatureSettings.CustomerManualEdits {
		writeError(w, http.StatusForbidden, "manual customer attribute edits are disabled")
		return false
	}
	return true
}
