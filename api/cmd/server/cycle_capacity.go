package main

import (
	"cmp"
	"context"
	"math"
	"net/http"
	"slices"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Cycle capacity.
//
// Capacity is in the unit of a cycle's scope: estimate points when the team
// uses estimates, issues otherwise. Capacity % = scope / capacity.
//
// A cycle's capacity is its override (typed in Edit cycle) when one is set,
// else the team's automatic capacity:
//
//   - velocity: the average completed scope of the team's last three completed
//     cycles (fewer when fewer exist). A cycle's completed scope is the sum,
//     over its unarchived issues in a completed state, of the points each issue
//     counts for in scope: its estimate; an unestimated issue counts as 1 point
//     unless "Count unestimated issues" is off (then 0); a 0 estimate counts as
//     0 only when zero estimates are allowed, else as unestimated. A team
//     without estimates counts every completed issue as 1.
//   - default, when the team has no completed cycle or completed nothing in
//     them: cycleCapacityPerMember × the team's active members (at least one).
//     A one-member team without history thus plans 30 points; 8 points of
//     scope read "26% of capacity" in the cycles list (rounded down) and 27% in
//     the cycle sidebar (rounded to nearest).
//
// Capacity is a team figure, so every cycle of a team without an override,
// completed ones included, reads the team's current automatic capacity. It is
// computed whenever cycles are read (bootstrap, cycle routes, public API, MCP)
// and is never stored.
const (
	cycleCapacityPerMember = 30
	cycleVelocityWindow    = 3
)

// cycleEstimateRules are the estimate options of a team that decide how much
// an issue counts for, following estimate inheritance from parent teams.
type cycleEstimateRules struct {
	estimates   bool
	allowZero   bool
	unestimated float64
}

func cycleEstimateRulesFor(data *domain.Bootstrap, teamID string) cycleEstimateRules {
	settings := data.TeamSettings[teamID]
	seen := map[string]bool{teamID: true}
	for settings.InheritIssueEstimation && settings.ParentTeamID != "" && !seen[settings.ParentTeamID] {
		seen[settings.ParentTeamID] = true
		parent, ok := data.TeamSettings[settings.ParentTeamID]
		if !ok {
			break
		}
		settings = parent
	}
	rules := cycleEstimateRules{estimates: settings.EstimateType != "" && settings.EstimateType != "notUsed", allowZero: settings.EstimateAllowZero, unestimated: 1}
	if settings.EstimateCountUnestimated != nil && !*settings.EstimateCountUnestimated {
		rules.unestimated = 0
	}
	return rules
}

// weight is what an issue with the estimate counts for in scope.
func (rules cycleEstimateRules) weight(estimate *float64) float64 {
	if !rules.estimates {
		return 1
	}
	if estimate == nil || *estimate < 0 || (*estimate == 0 && !rules.allowZero) {
		return rules.unestimated
	}
	return *estimate
}

// activeCycleTeamMembers counts the team's people for the default capacity:
// members who are not apps or agents and whose workspace membership is active.
func activeCycleTeamMembers(data *domain.Bootstrap, teamID string) int {
	excluded := map[string]bool{}
	for _, user := range data.Users {
		if user.App || user.BuiltinAgent {
			excluded[user.ID] = true
		}
	}
	for _, member := range data.Members {
		if member.Status != "" && member.Status != "active" {
			excluded[member.User.ID] = true
		}
	}
	seen := map[string]bool{}
	for _, member := range data.TeamMembers {
		if member.TeamID == teamID && !excluded[member.UserID] {
			seen[member.UserID] = true
		}
	}
	return max(len(seen), 1)
}

// velocityCycles returns, by team, the team's last completed cycles that
// make up its velocity, newest first.
func velocityCycles(cycles []domain.Cycle) map[string][]domain.Cycle {
	byTeam := map[string][]domain.Cycle{}
	for _, cycle := range cycles {
		if cycle.Status == "completed" {
			byTeam[cycle.TeamID] = append(byTeam[cycle.TeamID], cycle)
		}
	}
	for teamID, items := range byTeam {
		slices.SortFunc(items, func(a, b domain.Cycle) int {
			return cmp.Or(cmp.Compare(b.Number, a.Number), b.EndsAt.Compare(a.EndsAt), cmp.Compare(a.ID, b.ID))
		})
		byTeam[teamID] = items[:min(len(items), cycleVelocityWindow)]
	}
	return byTeam
}

func velocityCycleIDs(window map[string][]domain.Cycle) []string {
	ids := []string{}
	for _, items := range window {
		for _, cycle := range items {
			ids = append(ids, cycle.ID)
		}
	}
	slices.Sort(ids)
	return ids
}

// applyCycleCapacities fills capacity, capacityAutomatic and capacitySource
// of every cycle in data.Cycles, in place. completed holds the completed
// issues of the velocity cycles.
func applyCycleCapacities(data *domain.Bootstrap, completed []store.CycleCompletedIssue) {
	window := velocityCycles(data.Cycles)
	cycleTeam := map[string]string{}
	for teamID, items := range window {
		for _, cycle := range items {
			cycleTeam[cycle.ID] = teamID
		}
	}
	rules := map[string]cycleEstimateRules{}
	rulesFor := func(teamID string) cycleEstimateRules {
		if value, ok := rules[teamID]; ok {
			return value
		}
		value := cycleEstimateRulesFor(data, teamID)
		rules[teamID] = value
		return value
	}
	scopeDone := map[string]float64{}
	for _, issue := range completed {
		if teamID, ok := cycleTeam[issue.CycleID]; ok {
			scopeDone[issue.CycleID] += rulesFor(teamID).weight(issue.Estimate)
		}
	}
	type automatic struct {
		value  float64
		source string
	}
	teams := map[string]automatic{}
	automaticFor := func(teamID string) automatic {
		if value, ok := teams[teamID]; ok {
			return value
		}
		value := automatic{value: float64(cycleCapacityPerMember * activeCycleTeamMembers(data, teamID)), source: "default"}
		if items := window[teamID]; len(items) > 0 {
			total := 0.0
			for _, cycle := range items {
				total += scopeDone[cycle.ID]
			}
			if velocity := math.Round(total/float64(len(items))*10) / 10; velocity > 0 {
				value = automatic{value: velocity, source: "velocity"}
			}
		}
		teams[teamID] = value
		return value
	}
	for index := range data.Cycles {
		cycle := &data.Cycles[index]
		auto := automaticFor(cycle.TeamID)
		cycle.CapacityAutomatic = auto.value
		cycle.Capacity, cycle.CapacitySource = auto.value, auto.source
		if cycle.CapacityOverride != nil {
			cycle.Capacity, cycle.CapacitySource = float64(*cycle.CapacityOverride), "override"
		}
	}
}

// cycleCompletedIssuesIn is the completed issues of cycles among already
// loaded issues, for callers that hold the issue collection.
func cycleCompletedIssuesIn(issues []domain.Issue) []store.CycleCompletedIssue {
	result := []store.CycleCompletedIssue{}
	for _, issue := range issues {
		if issue.CycleID != nil && *issue.CycleID != "" && issue.ArchivedAt == nil && issue.State.Type == "completed" {
			result = append(result, store.CycleCompletedIssue{CycleID: *issue.CycleID, Estimate: issue.Estimate})
		}
	}
	return result
}

// decorateCycleCapacity computes the capacity of data.Cycles. The slice is
// replaced by a copy first: it may be shared with the store's snapshot.
func (s *server) decorateCycleCapacity(ctx context.Context, data *domain.Bootstrap) error {
	if len(data.Cycles) == 0 {
		return nil
	}
	workspace := data.Workspace.URLKey
	if workspace == "" {
		if metadata, ok := s.store.WorkspaceMetadataFields(""); ok {
			workspace = metadata.Workspace.URLKey
		}
	}
	completed, err := s.store.CycleCompletedIssues(ctx, workspace, velocityCycleIDs(velocityCycles(data.Cycles)))
	if err != nil {
		return err
	}
	data.Cycles = slices.Clone(data.Cycles)
	applyCycleCapacities(data, completed)
	return nil
}

// cycleWithCapacity is the cycle as a response returns it: with its
// computed capacity (from the workspace's stored metadata).
func (s *server) cycleWithCapacity(ctx context.Context, key string, cycle domain.Cycle) (domain.Cycle, error) {
	data, ok := s.store.WorkspaceMetadataFields(key, "cycles", "teamSettings", "teamMembers", "users", "members")
	if !ok {
		data = domain.Bootstrap{}
	}
	// The response's own record stands in for the stored one.
	data.Cycles = slices.Clone(data.Cycles)
	index := slices.IndexFunc(data.Cycles, func(item domain.Cycle) bool { return item.ID == cycle.ID })
	if index < 0 {
		data.Cycles = append(data.Cycles, cycle)
		index = len(data.Cycles) - 1
	}
	data.Cycles[index] = cycle
	if err := s.decorateCycleCapacity(ctx, &data); err != nil {
		return cycle, err
	}
	return data.Cycles[index], nil
}

// setCycleCapacityOverride stores a typed capacity; nil or 0 clears the
// override, so the cycle returns to the automatic capacity.
func setCycleCapacityOverride(cycle *domain.Cycle, value *int) error {
	if value == nil || *value == 0 {
		cycle.CapacityOverride = nil
		return nil
	}
	if *value < 0 || *value > 10000 {
		return errInvalid
	}
	override := *value
	cycle.CapacityOverride = &override
	return nil
}

// respondCycle writes a cycle mutation's result with its computed capacity.
func (s *server) respondCycle(w http.ResponseWriter, r *http.Request, err error, cycle domain.Cycle) {
	if err == nil {
		cycle, err = s.cycleWithCapacity(r.Context(), workspaceKey(r), cycle)
	}
	respondMutation(w, err, http.StatusOK, cycle)
}
