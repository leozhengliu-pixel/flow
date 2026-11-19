package main

import (
	"context"
	"net/http"
	"strings"

	"flow/api/internal/domain"
)

// scaleP21SW490Decision names the literal resource decision that
// resourceAllowedLegacy applies after its whole-workspace projection
// succeeds, for a request that reaches that projection.
type scaleP21SW490Decision int

const (
	// The request keeps the literal projection (cold route, or a predicate
	// that reads projected records without a reviewed bounded reader).
	scaleP21SW490Literal scaleP21SW490Decision = iota
	// return true: projection success alone.
	scaleP21SW490Membership
	// return data.ViewerRole != "guest".
	scaleP21SW490NonGuest
	// projectAllowed / initiativeAllowed (parts[2]), composed with the
	// relation predicate for .../relations/{relationId}.
	scaleP21SW490Point
	// teamRowAllowed(ResolveTeamID(ref)).
	scaleP21SW490TeamRow
	// teamRowAllowed && teamAllowed, with the GET resources handoff.
	scaleP21SW490TeamContent
	// preferenceReviewVisible(parts[2]) for an embedded review favorite.
	scaleP21SW490ReviewPreference
	// A per-record predicate over one metadata kind (SW492+).
	scaleP21SW490Record
)

type scaleP21SW490Shape struct {
	decision  scaleP21SW490Decision
	kind, ref string
	output    bool
	// target is a favorite/subscription reference's resource kind.
	target string
}

// scaleP21SW490FallbackShape mirrors, from the decoded path and method only,
// the branch of resourceAllowedLegacy's final switch that a request reaching
// the whole-workspace projection executes. Every branch whose decision reads
// projected records other than projects, initiatives and team rows, or the
// request body, stays literal.
func scaleP21SW490FallbackShape(r *http.Request) scaleP21SW490Shape {
	literal := scaleP21SW490Shape{decision: scaleP21SW490Literal}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) > 1 && parts[1] == "issue-records" {
		parts[1] = "issues"
	}
	if len(parts) >= 5 && parts[1] == "documents" && parts[3] == "comments" && (r.Method == http.MethodPatch || r.Method == http.MethodDelete) {
		return literal // The literal comment read precedes the switch.
	}
	if len(parts) < 2 {
		return scaleP21SW490Shape{decision: scaleP21SW490Membership}
	}
	createRoot := len(parts) == 2 && r.Method == http.MethodPost
	switch parts[1] {
	case "views", "dashboards", "releases", "release-pipelines", "asks":
		kind := map[string]string{"views": "savedViews", "dashboards": "dashboards", "releases": "releases", "release-pipelines": "releasePipelines", "asks": "asks"}[parts[1]]
		if createRoot {
			return scaleP21SW490Shape{decision: scaleP21SW490Record, kind: kind, output: true}
		}
		if len(parts) >= 3 {
			return scaleP21SW490Shape{decision: scaleP21SW490Record, kind: kind, ref: parts[2]}
		}
		return scaleP21SW490Shape{decision: scaleP21SW490Membership}
	case "issues":
		if createRoot || len(parts) >= 3 {
			return literal
		}
		return scaleP21SW490Shape{decision: scaleP21SW490Membership}
	case "projects", "initiatives":
		body := parts[1] + "Body"
		if createRoot {
			return scaleP21SW490Shape{decision: scaleP21SW490Record, kind: body, output: true}
		}
		if len(parts) < 3 {
			if parts[1] == "initiatives" {
				return scaleP21SW490Shape{decision: scaleP21SW490NonGuest}
			}
			return scaleP21SW490Shape{decision: scaleP21SW490Membership}
		}
		if len(parts) >= 4 && parts[3] == "relations" && r.Method == http.MethodPost {
			return scaleP21SW490Shape{decision: scaleP21SW490Record, kind: body, ref: parts[2]}
		}
		if !(len(parts) >= 5 && parts[3] == "relations") && r.Method == http.MethodPatch {
			return scaleP21SW490Shape{decision: scaleP21SW490Record, kind: body, ref: parts[2]}
		}
		return scaleP21SW490Shape{decision: scaleP21SW490Point, kind: parts[1], ref: parts[2]}
	case "customers":
		if len(parts) < 3 {
			return scaleP21SW490Shape{decision: scaleP21SW490NonGuest}
		}
		return scaleP21SW490Shape{decision: scaleP21SW490Record, kind: "customers", ref: parts[2]}
	case "customer-requests":
		if createRoot {
			return scaleP21SW490Shape{decision: scaleP21SW490Record, kind: "customerRequests", output: true}
		}
		if len(parts) >= 3 {
			return scaleP21SW490Shape{decision: scaleP21SW490Record, kind: "customerRequests", ref: parts[2]}
		}
		return scaleP21SW490Shape{decision: scaleP21SW490NonGuest}
	case "favorites", "subscriptions":
		if len(parts) < 4 {
			return scaleP21SW490Shape{decision: scaleP21SW490Membership}
		}
		return scaleP21SW490Shape{decision: scaleP21SW490Record, kind: "references", ref: parts[3], target: strings.ToLower(strings.TrimSpace(parts[2]))}
	case "reviews":
		if embeddedFavoriteRequest(r) {
			return scaleP21SW490Shape{decision: scaleP21SW490ReviewPreference, ref: parts[2]}
		}
		if len(parts) < 3 {
			return scaleP21SW490Shape{decision: scaleP21SW490Membership}
		}
		return scaleP21SW490Shape{decision: scaleP21SW490Record, kind: "reviews", ref: parts[2]}
	case "documents":
		if len(parts) == 2 && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			return scaleP21SW490Shape{decision: scaleP21SW490Membership}
		}
		if createRoot {
			return scaleP21SW490Shape{decision: scaleP21SW490Record, kind: "documentCreate", output: true}
		}
		return literal
	case "cycles":
		if len(parts) < 3 {
			return scaleP21SW490Shape{decision: scaleP21SW490Membership}
		}
		return scaleP21SW490Shape{decision: scaleP21SW490Record, kind: "cycles", ref: parts[2]}
	case "teams":
		if len(parts) < 3 {
			return scaleP21SW490Shape{decision: scaleP21SW490Membership}
		}
		if len(parts) >= 4 && (parts[3] == "resources" || parts[3] == "resource-sections") {
			output := r.Method == http.MethodGet && len(parts) == 4 && parts[3] == "resources"
			return scaleP21SW490Shape{decision: scaleP21SW490TeamContent, ref: parts[2], output: output}
		}
		return scaleP21SW490Shape{decision: scaleP21SW490TeamRow, ref: parts[2]}
	case "notifications":
		return literal // Decided before the projection.
	case "workspaces":
		if len(parts) >= 5 && parts[3] == "teams" {
			return scaleP21SW490Shape{decision: scaleP21SW490TeamRow, ref: parts[4]}
		}
		return scaleP21SW490Shape{decision: scaleP21SW490Membership}
	}
	return scaleP21SW490Shape{decision: scaleP21SW490Membership}
}

// boundedFallbackAuthorization replaces the whole-workspace projection of
// resourceAllowedLegacy with the reviewed bounded readers for every shape
// above that has one. Registered COLD routes keep their cold facade.
func (s *server) boundedFallbackAuthorization(r *http.Request, workspace, userID string) (allowed, handled bool) {
	if !s.store.BoundedMetadataEnabled() {
		return false, false
	}
	if _, cold := scaleP21ColdAuthorizationRoute(r); cold {
		return false, false
	}
	shape := scaleP21SW490FallbackShape(r)
	switch shape.decision {
	case scaleP21SW490Membership, scaleP21SW490NonGuest, scaleP21SW490ReviewPreference:
		data, err := s.scaleP21SW490MembershipAccess(r, workspace, userID)
		if err != nil {
			return false, true
		}
		switch shape.decision {
		case scaleP21SW490NonGuest:
			return data.ViewerRole != "guest", true
		case scaleP21SW490ReviewPreference:
			return s.preferenceReviewVisible(r, shape.ref), true
		}
		return true, true
	case scaleP21SW490Point:
		return s.boundedPointResourceDecision(r, workspace, userID, shape.kind, shape.ref), true
	case scaleP21SW490TeamRow:
		return s.boundedTeamRowAuthorization(r, workspace, userID, shape.ref), true
	case scaleP21SW490TeamContent:
		return s.boundedTeamResourceDecision(r, workspace, userID, shape.ref, shape.output), true
	case scaleP21SW490Record:
		if kind, decide := s.scaleP21SW490RecordPredicate(r, workspace, userID, shape); decide != nil {
			return s.boundedRecordDecision(r, workspace, userID, kind, shape.ref, decide), true
		}
	}
	return false, false
}

// The literal projection binds the wrapper's explicit workspace and viewer,
// even when the containing request names others (as SW253/SW306 do).
func (s *server) scaleP21SW490MembershipAccess(r *http.Request, workspace, userID string) (domain.Bootstrap, error) {
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
