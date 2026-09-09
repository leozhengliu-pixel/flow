package main

import (
	"net/http"
	"strings"
)

func metadataAuthorizationRequest(r *http.Request) bool {
	if embeddedFavoriteRequest(r) {
		return true
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 2 {
		return false
	}
	switch parts[1] {
	case "projects", "views", "initiatives", "drafts", "favorites", "favorite-folders", "subscriptions", "resource-preferences":
		return true
	}
	return r.URL.Path == "/api/workspace/preferences" || r.URL.Path == "/api/workspace/project-display-default" || r.URL.Path == "/api/account/settings"
}
