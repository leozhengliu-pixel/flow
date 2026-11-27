package main

import (
	"context"
	"net/http"
	"strings"

	"flow/api/internal/domain"
)

// fallbackDecision names the literal resource decision that
// resourceAllowedLegacy applies after its whole-workspace projection
// succeeds, for a request that reaches that projection.
type fallbackDecision int

const (
	// The request keeps the literal projection (cold route, or a predicate
	// that reads projected records without a reviewed bounded reader).
	fallbackLiteral fallbackDecision = iota
	// return true: projection success alone.
	fallbackMembership
	// return data.ViewerRole != "guest".
	fallbackNonGuest
	// projectAllowed / initiativeAllowed (parts[2]), composed with the
	// relation predicate for .../relations/{relationId}.
	fallbackPoint
	// teamRowAllowed(ResolveTeamID(ref)).
	fallbackTeamRow
	// teamRowAllowed && teamAllowed, with the GET resources handoff.
	fallbackTeamContent
	// preferenceReviewVisible(parts[2]) for an embedded review favorite.
	fallbackReviewPreference
	// A per-record predicate over one metadata kind (record fallback authorization).
	fallbackRecord
)

type fallbackRouteShape struct {
	decision  fallbackDecision
	kind, ref string
	output    bool
	// target is a favorite/subscription reference's resource kind.
	target string
}

// fallbackShape mirrors, from the decoded path and method only,
// the branch of resourceAllowedLegacy's final switch that a request reaching
// the whole-workspace projection executes. Every branch whose decision reads
// projected records other than projects, initiatives and team rows, or the
// request body, stays literal.
func fallbackShape(r *http.Request) fallbackRouteShape {
	literal := fallbackRouteShape{decision: fallbackLiteral}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) > 1 && parts[1] == "issue-records" {
		parts[1] = "issues"
	}
	if len(parts) >= 5 && parts[1] == "documents" && parts[3] == "comments" && (r.Method == http.MethodPatch || r.Method == http.MethodDelete) {
		return literal // The literal comment read precedes the switch.
	}
	if len(parts) < 2 {
		return fallbackRouteShape{decision: fallbackMembership}
	}
	createRoot := len(parts) == 2 && r.Method == http.MethodPost
	switch parts[1] {
	case "views", "dashboards", "releases", "release-pipelines", "asks":
		kind := map[string]string{"views": "savedViews", "dashboards": "dashboards", "releases": "releases", "release-pipelines": "releasePipelines", "asks": "asks"}[parts[1]]
		if createRoot {
			return fallbackRouteShape{decision: fallbackRecord, kind: kind, output: true}
		}
		if len(parts) >= 3 {
			return fallbackRouteShape{decision: fallbackRecord, kind: kind, ref: parts[2]}
		}
		return fallbackRouteShape{decision: fallbackMembership}
	case "issues":
		if createRoot || len(parts) >= 3 {
			return literal
		}
		return fallbackRouteShape{decision: fallbackMembership}
	case "projects", "initiatives":
		body := parts[1] + "Body"
		if createRoot {
			return fallbackRouteShape{decision: fallbackRecord, kind: body, output: true}
		}
		if len(parts) < 3 {
			if parts[1] == "initiatives" {
				return fallbackRouteShape{decision: fallbackNonGuest}
			}
			return fallbackRouteShape{decision: fallbackMembership}
		}
		if len(parts) >= 4 && parts[3] == "relations" && r.Method == http.MethodPost {
			return fallbackRouteShape{decision: fallbackRecord, kind: body, ref: parts[2]}
		}
		if !(len(parts) >= 5 && parts[3] == "relations") && r.Method == http.MethodPatch {
			return fallbackRouteShape{decision: fallbackRecord, kind: body, ref: parts[2]}
		}
		return fallbackRouteShape{decision: fallbackPoint, kind: parts[1], ref: parts[2]}
	case "customers":
		if len(parts) < 3 {
			return fallbackRouteShape{decision: fallbackNonGuest}
		}
		return fallbackRouteShape{decision: fallbackRecord, kind: "customers", ref: parts[2]}
	case "customer-requests":
		if createRoot {
			return fallbackRouteShape{decision: fallbackRecord, kind: "customerRequests", output: true}
		}
		if len(parts) >= 3 {
			return fallbackRouteShape{decision: fallbackRecord, kind: "customerRequests", ref: parts[2]}
		}
		return fallbackRouteShape{decision: fallbackNonGuest}
	case "favorites", "subscriptions":
		if len(parts) < 4 {
			return fallbackRouteShape{decision: fallbackMembership}
		}
		return fallbackRouteShape{decision: fallbackRecord, kind: "references", ref: parts[3], target: strings.ToLower(strings.TrimSpace(parts[2]))}
	case "reviews":
		if embeddedFavoriteRequest(r) {
			return fallbackRouteShape{decision: fallbackReviewPreference, ref: parts[2]}
		}
		if len(parts) < 3 {
			return fallbackRouteShape{decision: fallbackMembership}
		}
		return fallbackRouteShape{decision: fallbackRecord, kind: "reviews", ref: parts[2]}
	case "documents":
		if len(parts) == 2 && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			return fallbackRouteShape{decision: fallbackMembership}
		}
		if createRoot {
			return fallbackRouteShape{decision: fallbackRecord, kind: "documentCreate", output: true}
		}
		return literal
	case "cycles":
		if len(parts) < 3 {
			return fallbackRouteShape{decision: fallbackMembership}
		}
		return fallbackRouteShape{decision: fallbackRecord, kind: "cycles", ref: parts[2]}
	case "teams":
		if len(parts) < 3 {
			return fallbackRouteShape{decision: fallbackMembership}
		}
		if len(parts) >= 4 && (parts[3] == "resources" || parts[3] == "resource-sections") {
			output := r.Method == http.MethodGet && len(parts) == 4 && parts[3] == "resources"
			return fallbackRouteShape{decision: fallbackTeamContent, ref: parts[2], output: output}
		}
		return fallbackRouteShape{decision: fallbackTeamRow, ref: parts[2]}
	case "notifications":
		return literal // Decided before the projection.
	case "workspaces":
		if len(parts) >= 5 && parts[3] == "teams" {
			return fallbackRouteShape{decision: fallbackTeamRow, ref: parts[4]}
		}
		return fallbackRouteShape{decision: fallbackMembership}
	}
	return fallbackRouteShape{decision: fallbackMembership}
}

// boundedFallbackAuthorization replaces the whole-workspace projection of
// resourceAllowedLegacy with the reviewed bounded readers for every shape
// above that has one. Registered COLD routes keep their cold facade.
func (s *server) boundedFallbackAuthorization(r *http.Request, workspace, userID string) (allowed, handled bool) {
	if !s.store.BoundedMetadataEnabled() {
		return false, false
	}
	if r.URL.Path == "/api/loop-config" || r.URL.Path == "/api/workspace/loop-settings" || r.URL.Path == "/api/api-keys" || strings.HasPrefix(r.URL.Path, "/api/api-keys/") {
		data, ok := s.store.WorkspaceSettingsMetadata(workspace)
		if !ok {
			return false, true
		}
		_, status, err := s.store.WorkspaceRole(r.Context(), data.Workspace.ID, userID)
		noteAuthorizationBusy(r, err)
		return err == nil && status == "active", true
	}
	if _, cold := scaleColdAuthorizationRoute(r); cold {
		return false, false
	}
	shape := fallbackShape(r)
	if shape.kind == "cycles" && r.Method == http.MethodPatch && strings.Count(strings.Trim(r.URL.Path, "/"), "/") == 2 {
		// Keep the legacy favorite PATCH's complete-body requirement. The
		// bounded cycle reader already applies team visibility to all writes.
		var input domain.CycleMutationInput
		if !peekRequestJSON(r, &input) {
			return false, true
		}
	}
	if authorizationBodyError(r) != nil {
		return false, true
	}
	switch shape.decision {
	case fallbackMembership, fallbackNonGuest, fallbackReviewPreference:
		data, err := s.membershipAccess(r, workspace, userID)
		if err != nil {
			return false, true
		}
		switch shape.decision {
		case fallbackNonGuest:
			return data.ViewerRole != "guest", true
		case fallbackReviewPreference:
			return s.preferenceReviewVisible(r, shape.ref), true
		}
		return true, true
	case fallbackPoint:
		return s.boundedPointResourceDecision(r, workspace, userID, shape.kind, shape.ref), true
	case fallbackTeamRow:
		return s.boundedTeamRowAuthorization(r, workspace, userID, shape.ref), true
	case fallbackTeamContent:
		return s.boundedTeamResourceDecision(r, workspace, userID, shape.ref, shape.output), true
	case fallbackRecord:
		if kind, decide := s.recordShapePredicate(r, workspace, userID, shape); decide != nil {
			return s.boundedRecordDecision(r, workspace, userID, kind, shape.ref, decide), true
		}
	}
	return false, false
}

// The literal projection binds the wrapper's explicit workspace and viewer,
// even when the containing request names others (as collection authorization/membership authorization do).
func (s *server) membershipAccess(r *http.Request, workspace, userID string) (domain.Bootstrap, error) {
	request := r
	if workspaceKey(r) != workspace || authUser(r).ID != userID {
		viewer := authUser(r)
		viewer.ID = userID
		ctx := context.WithValue(r.Context(), workspaceKeyContextKey{}, workspace)
		ctx = context.WithValue(ctx, authUserContextKey{}, viewer)
		request = r.Clone(ctx)
		request.Header.Set("X-Workspace-Key", workspace)
		query := request.URL.Query()
		query.Del("workspace")
		request.URL.RawQuery = query.Encode()
	}
	data, _, err := s.requestIssueQueryAccess(request)
	return data, err
}
