package main

import (
	"net/http"
	"strings"
)

func metadataAuthorizationRequest(r *http.Request) bool {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 2 {
		return false
	}
	switch parts[1] {
	case "projects", "views", "initiatives", "drafts":
		return true
	}
	return r.URL.Path == "/api/workspace/preferences" || r.URL.Path == "/api/workspace/project-display-default" || r.URL.Path == "/api/account/settings"
}
