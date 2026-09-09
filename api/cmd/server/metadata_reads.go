package main

import (
	"net/http"
	"strings"
)

func metadataReadRequest(r *http.Request) bool {
	p := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(p) < 2 {
		return false
	}
	if r.Method == http.MethodGet {
		switch r.URL.Path {
		case "/api/workflows", "/api/workflow-runs", "/api/dashboards", "/api/posts", "/api/meetings", "/api/ai/conversations", "/api/customer-taxonomy":
			return true
		}
		if p[1] == "imports" && len(p) <= 3 {
			return true
		}
		if p[1] == "teams" && len(p) == 4 && p[3] == "resources" {
			return true
		}
	}
	switch p[1] {
	case "account", "workspace", "application-policies", "api-keys", "oauth", "notification-preferences", "push-subscriptions", "agent-skills", "agent", "exports":
		return true
	case "teams":
		if len(p) >= 4 {
			switch p[3] {
			case "settings", "states", "triage-responsibilities", "triage-rules", "email-intake-addresses", "cycle-settings":
				return true
			}
		}
	}
	return false
}
