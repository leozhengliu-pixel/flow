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
		// The cycle graph loads its issues through authorized record queries.
		if p[1] == "cycles" && len(p) == 4 && p[3] == "graph" {
			return true
		}
		// Releases hold issue ids; the paged projection filters them to the
		// viewer's visible issues and progress comes from an indexed query.
		if p[1] == "releases" && (len(p) <= 3 || len(p) == 4 && (p[3] == "history" || p[3] == "notes")) {
			return true
		}
		if p[1] == "release-pipelines" && len(p) <= 3 {
			return true
		}
		// Documents, their permissions, drafts and revisions are metadata;
		// comments are read from the document's own content records.
		if p[1] == "documents" && (len(p) == 2 || len(p) == 4 && (p[3] == "permissions" || p[3] == "comments" || p[3] == "drafts" || p[3] == "history")) {
			return true
		}
	}
	switch p[1] {
	case "account", "workspace", "application-policies", "api-keys", "oauth", "notification-preferences", "push-subscriptions", "agent-skills", "agent", "exports":
		return true
	case "teams":
		if len(p) >= 4 {
			switch p[3] {
			case "settings", "states", "triage-responsibilities", "triage-rules", "email-intake-addresses", "cycle-settings", "default-favorites":
				return true
			}
		}
	}
	return false
}
