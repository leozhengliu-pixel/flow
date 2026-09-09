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
	switch p[1] {
	case "account", "workspace", "api-keys", "oauth", "notification-preferences", "push-subscriptions", "agent-skills", "agent":
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
