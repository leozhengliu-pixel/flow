package store

import (
	"slices"
	"strings"

	"flow/api/internal/domain"
)

// DocumentLinkScope decides the scope a document inherits from the issue and
// projects it is attached to (domain.Document.InheritedTeamIDs). Only the
// inherited teams whose content is closed to workspace members who have not
// joined them scope the document: private and restricted teams, teams that
// take members only by invitation, and teams inside a private team. A team
// missing from the graph counts as closed, since a viewer's projection leaves
// out the private teams they cannot read.
//
// Privacy is read from the team graph at authorization time, so making a
// team private or public applies to its documents at once, without
// rewriting them.
type DocumentLinkScope struct {
	teams    []domain.Team
	settings map[string]domain.TeamSettings
	byID     map[string]domain.Team
}

// NewDocumentLinkScope prepares the team graph once for checking many
// documents.
func NewDocumentLinkScope(teams []domain.Team, settings map[string]domain.TeamSettings) *DocumentLinkScope {
	return &DocumentLinkScope{teams: teams, settings: settings}
}

// ClosedTeams returns the document's inherited teams that scope it.
func (scope *DocumentLinkScope) ClosedTeams(document domain.Document) []string {
	if len(document.InheritedTeamIDs) == 0 {
		return nil
	}
	if scope.byID == nil {
		scope.byID = make(map[string]domain.Team, len(scope.teams))
		for _, team := range scope.teams {
			scope.byID[team.ID] = team
		}
	}
	closed := []string{}
	for _, teamID := range document.InheritedTeamIDs {
		if scope.teamClosed(teamID) {
			closed = append(closed, teamID)
		}
	}
	return closed
}

// Open reports whether a viewer passes the document's inherited scope: no
// inherited team is closed, or the viewer can read one of the closed teams
// (readable reports the viewer's team content access).
func (scope *DocumentLinkScope) Open(document domain.Document, readable func(teamID string) bool) bool {
	closed := scope.ClosedTeams(document)
	return len(closed) == 0 || slices.ContainsFunc(closed, readable)
}

// documentScopes returns stored with the inherited scope fields (the only
// ones a bounded issue write changes on documents) taken from documents, and
// whether any changed. Stored is not modified.
func documentScopes(stored, documents []domain.Document) ([]domain.Document, bool) {
	updated := make(map[string]domain.Document, len(documents))
	for _, document := range documents {
		updated[document.ID] = document
	}
	result := stored
	changed := false
	for index, document := range stored {
		value, ok := updated[document.ID]
		if !ok || slices.Equal(value.InheritedTeamIDs, document.InheritedTeamIDs) && value.IssueTeamID == document.IssueTeamID {
			continue
		}
		if !changed {
			result = slices.Clone(stored)
			changed = true
		}
		result[index].InheritedTeamIDs = slices.Clone(value.InheritedTeamIDs)
		result[index].IssueTeamID = value.IssueTeamID
	}
	return result, changed
}

func (scope *DocumentLinkScope) teamClosed(teamID string) bool {
	seen := map[string]bool{}
	for current := teamID; current != "" && !seen[current]; current = scope.settings[current].ParentTeamID {
		seen[current] = true
		team, ok := scope.byID[current]
		if !ok {
			return true
		}
		policy := scope.settings[current]
		if current == teamID {
			// The non-member rule of visibleIssueTeams.
			access := strings.ToLower(strings.TrimSpace(policy.Access))
			if team.Private || access == "private" || access == "restricted" || strings.EqualFold(policy.MembershipRestriction, "members") || strings.EqualFold(policy.MembershipRestriction, "owners") {
				return true
			}
			continue
		}
		if privateTeamBoundary(team, policy) {
			return true
		}
	}
	return false
}
