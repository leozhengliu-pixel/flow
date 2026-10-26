package main

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// issueIdentifierPattern matches an issue identifier such as ENG-42.
var issueIdentifierPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*-[0-9]+$`)

// maxPreviousIdentifiers bounds the identifiers an issue keeps answering to
// after repeated team moves; the oldest are dropped first.
const maxPreviousIdentifiers = 50

// moveIssueToTeam moves an issue into another team. Every entry point moves
// issues this way: the team picker and "Move to team" in the app, PATCH
// /api/issues/{id} (API, SDK, MCP save_issue), batch updates and accepted team
// suggestions. Like the reference app:
//
//   - the issue gets the target team's key and the next number of the
//     workspace sequence (the store must have read it, see
//     store.WithIssueRenumbering); the old identifier keeps resolving;
//   - its status becomes the target team's status of the same name and type,
//     else the first of the same type, else the team's default (state, when
//     not nil, is used instead: a triage suggestion lands in the team's triage);
//   - it leaves its cycle (cycles belong to one team);
//   - labels the target team cannot use are replaced by its label of the same
//     name, or dropped;
//   - the estimate is cleared when the target team does not use estimates, and
//     a delegated agent that cannot work in the target team is removed;
//   - assignee, project, milestone, parent and sub-issues stay as they are
//     (sub-issues keep their own teams).
//
// The changes are recorded in changes for the issue history, notifications
// and webhooks.
func moveIssueToTeam(data *domain.Bootstrap, issue *domain.Issue, team domain.Team, state *domain.WorkflowState, changes map[string]string) {
	now := time.Now().UTC()
	changes["teamBefore"], changes["teamBeforeName"] = issue.Team.ID, issue.Team.Name
	changes["team"], changes["teamName"] = team.ID, team.Name

	number := max(data.NextIssueNumber, nextIssueNumber(data.Issues))
	data.NextIssueNumber = number + 1
	if issue.Identifier != "" && !slices.ContainsFunc(issue.PreviousIdentifiers, func(value string) bool { return strings.EqualFold(value, issue.Identifier) }) {
		issue.PreviousIdentifiers = append(issue.PreviousIdentifiers, issue.Identifier)
	}
	if extra := len(issue.PreviousIdentifiers) - maxPreviousIdentifiers; extra > 0 {
		issue.PreviousIdentifiers = slices.Clone(issue.PreviousIdentifiers[extra:])
	}
	changes["identifierBefore"] = issue.Identifier
	issue.Number = number
	issue.Identifier = fmt.Sprintf("%s-%d", team.Key, number)
	changes["identifier"] = issue.Identifier
	issue.Team = team
	// Documents attached to the issue follow it into the new team's scope.
	moveIssueDocuments(data, issue.ID, team.ID)

	if state == nil {
		state = teamMoveState(data, issue.State, team.ID)
	}
	if state != nil && state.ID != issue.State.ID {
		previous := issue.State
		changes["stateBefore"], changes["stateBeforeId"], changes["stateBeforeType"] = previous.Name, previous.ID, previous.Type
		changes["state"], changes["stateId"], changes["stateType"] = state.Name, state.ID, state.Type
		issue.State = *state
		if state.Type != previous.Type {
			issue.StatusChangedAt = &now
			if state.Type == "started" && issue.StartedAt == nil {
				issue.StartedAt = &now
			}
			switch state.Type {
			case "completed":
				issue.CompletedAt, issue.CanceledAt = &now, nil
			case "canceled":
				issue.CanceledAt, issue.CompletedAt = &now, nil
			default:
				issue.CompletedAt, issue.CanceledAt = nil, nil
			}
		}
	}

	if issue.CycleID != nil {
		changes["cycleBefore"], changes["cycle"] = *issue.CycleID, ""
		issue.CycleID = nil
	}
	issue.AddedToCycle = ""

	if labels, changed := teamMoveLabels(data, issue.Labels, team.ID); changed {
		before := make([]string, 0, len(issue.Labels))
		for _, label := range issue.Labels {
			before = append(before, label.ID)
		}
		after := make([]string, 0, len(labels))
		for _, label := range labels {
			after = append(after, label.ID)
		}
		changes["labelsBefore"], changes["labels"] = strings.Join(before, ","), strings.Join(after, ",")
		issue.Labels = labels
	}

	if settings, ok := data.TeamSettings[team.ID]; ok && settings.EstimateType == "notUsed" && issue.Estimate != nil {
		changes["estimateBefore"], changes["estimate"] = strconv.FormatFloat(*issue.Estimate, 'f', -1, 64), "0"
		issue.Estimate = nil
	}

	if issue.Delegate != nil && !issue.Delegate.CanDelegateToTeam(team) {
		changes["previousDelegate"], changes["delegate"] = issue.Delegate.ID, ""
		issue.Delegate = nil
		issue.AgentSessionID = ""
	}
}

// teamMoveState is the status an issue in current gets in teamID.
func teamMoveState(data *domain.Bootstrap, current domain.WorkflowState, teamID string) *domain.WorkflowState {
	states := statesForTeam(data, teamID)
	if index := slices.IndexFunc(states, func(state domain.WorkflowState) bool { return state.ID == current.ID }); index >= 0 {
		// Teams sharing workflow states (inherited or workspace states).
		return &states[index]
	}
	if index := slices.IndexFunc(states, func(state domain.WorkflowState) bool {
		return state.Type == current.Type && !state.Reserved && strings.EqualFold(strings.TrimSpace(state.Name), strings.TrimSpace(current.Name))
	}); index >= 0 {
		return &states[index]
	}
	if state := stateForTeamByType(data, teamID, current.Type); state != nil {
		return state
	}
	if state := stateForTeam(data, teamID, teamSettings(data, teamID).DefaultStateID); state != nil {
		return state
	}
	if len(states) > 0 {
		return &states[0]
	}
	return nil
}

// teamMoveLabels keeps the labels teamID can use and replaces the others by
// its label of the same name (dropping those without one). It reports
// whether anything changed.
func teamMoveLabels(data *domain.Bootstrap, labels []domain.IssueLabel, teamID string) ([]domain.IssueLabel, bool) {
	result := make([]domain.IssueLabel, 0, len(labels))
	changed := false
	for _, label := range labels {
		if labelsAvailableToTeam(data, []domain.IssueLabel{label}, teamID) {
			result = append(result, label)
			continue
		}
		changed = true
		index := slices.IndexFunc(data.Labels, func(candidate domain.IssueLabel) bool {
			return candidate.ID != label.ID && strings.EqualFold(strings.TrimSpace(candidate.Name), strings.TrimSpace(label.Name)) &&
				labelAvailableForResource(data, candidate, "issue") && labelsAvailableToTeam(data, []domain.IssueLabel{candidate}, teamID)
		})
		if index >= 0 && !slices.ContainsFunc(result, func(item domain.IssueLabel) bool { return item.ID == data.Labels[index].ID }) {
			result = append(result, data.Labels[index])
		}
	}
	if !changed {
		return labels, false
	}
	// A mapped label may share a group with one the issue kept: keep the first.
	groups := map[string]bool{}
	result = slices.DeleteFunc(result, func(label domain.IssueLabel) bool {
		if label.GroupID == "" {
			return false
		}
		if groups[label.GroupID] {
			return true
		}
		groups[label.GroupID] = true
		return false
	})
	return result, true
}

// issueTeamMoveForbidden says why the viewer of data (the request's
// projected workspace) may not move issues into teamID, or "" when they may.
// Moving needs the issue's editor access (checked by the caller) and the
// target team's content: a private team only for its members, never for
// guests.
func issueTeamMoveForbidden(s *server, data domain.Bootstrap, teamID string) string {
	if s.authDisabled {
		return ""
	}
	if strings.EqualFold(data.ViewerRole, "guest") {
		return "Guests cannot move issues to another team"
	}
	if !teamContentVisible(data, teamID) {
		return "Team is outside your teams"
	}
	return ""
}
