package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"flow/api/internal/domain"
)

func recordAccessMode() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FLOW_RECORD_ACCESS"))) {
	case "shadow":
		return "shadow"
	case "off", "0", "false":
		return "off"
	default:
		return "on"
	}
}

func recordAccessRoute(r *http.Request) (kind, ref string, ok bool) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return "", "", false
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 3 || parts[0] != "api" || parts[2] == "" || parts[1] != "projects" && parts[1] != "initiatives" {
		return "", "", false
	}
	return parts[1], parts[2], true
}

func (s *server) requestResourceRead(r *http.Request, workspace, userID string) *resourceReadHandoff {
	kind, ref, _ := recordAccessRoute(r)
	var key *domain.APIKey
	if credential, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok {
		key = &credential
	}
	if cached, ok := r.Context().Value(resourceReadHandoffKey{}).(*resourceReadHandoff); ok && cached.workspace == workspace && cached.userID == userID && cached.kind == kind && cached.ref == ref && cached.access.MatchesCredential(key) {
		return cached
	}
	result := &resourceReadHandoff{workspace: workspace, userID: userID, kind: kind, ref: ref}
	result.access, result.err = s.store.ResolveAccess(r.Context(), workspace, userID, key)
	if result.err == nil {
		result.data, result.allowed, result.err = s.store.ResourceReadMetadata(r.Context(), result.access, kind, ref)
	}
	handOffResourceRead(r, result)
	return result
}

func logRecordAccessDifference(r *http.Request, stage string) {
	// Do not log bodies, identifiers, actors, scopes or credential material.
	kind, _, _ := recordAccessRoute(r)
	slog.Warn("record access shadow difference", "kind", kind, "stage", stage)
}

func (s *server) shadowResourceAuthorization(r *http.Request, workspace, userID string, allowed bool) bool {
	result := s.requestResourceRead(r, workspace, userID)
	if result.err != nil || result.allowed != allowed {
		logRecordAccessDifference(r, "authorization")
	}
	return allowed
}

// The legacy handler remains the shadow oracle, including wire null/empty
// distinctions. Auth-disabled fixtures retain their existing unfiltered path.
func (s *server) serveResourceRead(w http.ResponseWriter, r *http.Request, kind string) bool {
	if s.authDisabled || recordAccessMode() == "off" {
		return false
	}
	if _, _, ok := recordAccessRoute(r); !ok {
		return false
	}
	result := s.requestResourceRead(r, workspaceKey(r), authUser(r).ID)
	if recordAccessMode() == "shadow" {
		return false
	}
	if result.err != nil {
		writeAPIError(w, http.StatusForbidden, apiErrorForbidden, "You don't have access to this workspace")
		return true
	}
	var item any
	if kind == "projects" && len(result.data.Projects) == 1 {
		item = result.data.Projects[0]
	}
	if kind == "initiatives" && len(result.data.Initiatives) == 1 {
		item = result.data.Initiatives[0]
	}
	if item == nil {
		writePublicRecordNotFound(w, strings.TrimSuffix(kind, "s"))
	} else {
		writeJSON(w, http.StatusOK, item)
	}
	return true
}

func (s *server) shadowResourceRecord(r *http.Request, kind string, item any) {
	if s.authDisabled || recordAccessMode() != "shadow" {
		return
	}
	if _, _, ok := recordAccessRoute(r); !ok {
		return
	}
	result := s.requestResourceRead(r, workspaceKey(r), authUser(r).ID)
	var candidate any
	if kind == "projects" && len(result.data.Projects) == 1 {
		candidate = result.data.Projects[0]
	}
	if kind == "initiatives" && len(result.data.Initiatives) == 1 {
		candidate = result.data.Initiatives[0]
	}
	// JSON omits Go-only fields, sorts map keys and preserves null/empty.
	legacy, legacyErr := json.Marshal(item)
	current, currentErr := json.Marshal(candidate)
	if result.err != nil || legacyErr != nil || currentErr != nil || !bytes.Equal(legacy, current) {
		logRecordAccessDifference(r, "record")
	}
}
