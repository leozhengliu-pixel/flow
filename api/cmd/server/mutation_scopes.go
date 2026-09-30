package main

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// withIssueScope runs a workspace mutation against just the named issue
// records (typically ids the callback validates) instead of loading every
// issue and content record in the workspace.
func withIssueScope(ctx context.Context, ids ...*string) context.Context {
	return store.WithMutationScope(ctx, store.MutationScope{IssueIDs: scopeIDs(ids...)})
}

func scopeIDs(ids ...*string) []string {
	result := []string{}
	for _, id := range ids {
		if id == nil {
			continue
		}
		for _, value := range []string{*id, strings.TrimSpace(*id)} {
			if value != "" && !slices.Contains(result, value) {
				result = append(result, value)
			}
		}
	}
	return result
}

// releaseMutationScope loads the issues a release write can touch: ids it
// validates, and — when the write can move the release to a released status —
// the release's current issues, which completion automations update.
func releaseMutationScope(ctx context.Context, input releaseInput, match func(domain.Release) bool) context.Context {
	scope := store.MutationScope{}
	if input.IssueIDs != nil {
		scope.IssueIDs = normalizedStrings(*input.IssueIDs)
	}
	if input.Status != nil || input.Stage != nil {
		scope.Resolve = func(data domain.Bootstrap) store.MutationScope {
			extra := store.MutationScope{}
			for _, release := range data.Releases {
				if match(release) {
					extra.IssueIDs = append(extra.IssueIDs, release.IssueIDs...)
				}
			}
			return extra
		}
	}
	return store.WithMutationScope(ctx, scope)
}

// documentContentScope loads just a document's comment thread (comments are
// content records owned by the document id; the route may use its slug).
func documentContentScope(ctx context.Context, id string) context.Context {
	return store.WithMutationScope(ctx, store.MutationScope{Resolve: func(data domain.Bootstrap) store.MutationScope {
		if document, err := documentByID(&data, id); err == nil {
			return store.MutationScope{Resources: []string{document.ID}}
		}
		return store.MutationScope{}
	}})
}

// projectIssueScope loads a project's issues (project deletion detaches them).
func projectIssueScope(ctx context.Context, projectID string) context.Context {
	return store.WithMutationScope(ctx, store.MutationScope{ProjectIssues: []string{projectID}})
}

// projectTemplateScope loads the issues a project template names (template
// edits validate them; creating a project from the template moves them).
func projectTemplateScope(ctx context.Context, issueIDs *[]string, templateID string) context.Context {
	scope := store.MutationScope{}
	if issueIDs != nil {
		scope.IssueIDs = normalizedStrings(*issueIDs)
	}
	if templateID != "" {
		scope.Resolve = func(data domain.Bootstrap) store.MutationScope {
			for _, template := range data.ProjectTemplates {
				if template.ID == templateID {
					return store.MutationScope{IssueIDs: slices.Clone(template.IssueIDs)}
				}
			}
			return store.MutationScope{}
		}
	}
	return store.WithMutationScope(ctx, scope)
}

// cycleIssueScope loads the issues in the listed teams' cycles: current and
// upcoming ones (cycle transitions migrate the previous cycle's open issues;
// inherited-cycle reconciliation clears removed upcoming cycles), or every
// cycle of the teams when allCycles is set.
func cycleIssueScope(ctx context.Context, allCycles bool, teams func(domain.Bootstrap) []string) context.Context {
	return store.WithMutationScope(ctx, store.MutationScope{Resolve: func(data domain.Bootstrap) store.MutationScope {
		wanted := map[string]bool{}
		for _, id := range teams(data) {
			wanted[id] = true
		}
		ids := []string{}
		for _, cycle := range data.Cycles {
			if wanted[cycle.TeamID] && (allCycles || cycle.Status == "current" || cycle.Status == "upcoming") {
				ids = append(ids, cycle.ID)
			}
		}
		if len(ids) == 0 {
			return store.MutationScope{}
		}
		return store.MutationScope{IssueColumns: []store.IssueColumnScope{{Column: "cycle_id", Values: ids}}}
	}})
}

func cycleTeam(id string) func(domain.Bootstrap) []string {
	return func(data domain.Bootstrap) []string {
		for _, cycle := range data.Cycles {
			if cycle.ID == id {
				return []string{cycle.TeamID}
			}
		}
		return nil
	}
}

func teamAndDescendants(teamID string) func(domain.Bootstrap) []string {
	return func(data domain.Bootstrap) []string {
		return append([]string{teamID}, teamDescendantIDs(&data, teamID)...)
	}
}

// workflowIssueScope loads the issues a workflow status write can re-point:
// the team's issues while its statuses are still the shared defaults (the
// write materializes team-owned copies), issues of descendant teams that
// inherit the workflow, and issues in the listed statuses.
func workflowIssueScope(ctx context.Context, teamID string, stateIDs ...string) context.Context {
	return store.WithMutationScope(ctx, store.MutationScope{Resolve: func(data domain.Bootstrap) store.MutationScope {
		teams := []string{}
		if !slices.ContainsFunc(data.States, func(state domain.WorkflowState) bool { return state.TeamID == teamID }) {
			teams = append(teams, teamID)
		}
		for _, id := range append([]string{teamID}, teamDescendantIDs(&data, teamID)...) {
			if settings := data.TeamSettings[id]; settings.InheritWorkflowStatuses && settings.ParentTeamID != "" {
				teams = append(teams, id)
			}
		}
		scope := store.MutationScope{}
		if len(teams) > 0 {
			scope.IssueColumns = append(scope.IssueColumns, store.IssueColumnScope{Column: "team_id", Values: teams})
		}
		if len(stateIDs) > 0 {
			scope.IssueColumns = append(scope.IssueColumns, store.IssueColumnScope{Column: "state_id", Values: stateIDs})
		}
		return scope
	}})
}

// slaRuleScope loads the issues applySLARules can change once the rule write
// lands: issues with an open SLA (non-matching ones are marked removed) and
// issues an enabled rule can match, found through the rule's team, project,
// label or status. A rule with none of those matches every issue and runs on
// the full workspace path.
func slaRuleScope(ctx context.Context, ruleID string, input slaRuleInput) context.Context {
	return store.WithMutationScope(ctx, store.MutationScope{Resolve: func(data domain.Bootstrap) store.MutationScope {
		if !slaEnabled(&data) {
			return store.MutationScope{}
		}
		rules := slices.Clone(data.SLARules)
		candidate := domain.SLARule{TeamIDs: []string{}, Filters: map[string]any{}, TargetMinutes: 1440, PauseStatuses: []string{}, Enabled: true}
		if ruleID != "" {
			index := slices.IndexFunc(rules, func(rule domain.SLARule) bool { return rule.ID == ruleID })
			if index < 0 {
				return store.MutationScope{}
			}
			candidate = rules[index]
			rules = slices.Delete(rules, index, index+1)
		}
		if applySLARuleInput(&data, &candidate, input) != nil {
			return store.MutationScope{}
		}
		return slaIssueScope(data, append(rules, candidate))
	}})
}

// slaIssueScope lists the issues applySLARules can change under rules.
func slaIssueScope(data domain.Bootstrap, rules []domain.SLARule) store.MutationScope {
	scope := store.MutationScope{IssueFilter: func(issue domain.Issue) bool {
		return slices.ContainsFunc(rules, func(rule domain.SLARule) bool { return slaMatches(rule, issue) })
	}}
	for _, sla := range data.IssueSLAs {
		if sla.Status != "completed" && sla.Status != "removed" {
			scope.IssueIDs = append(scope.IssueIDs, sla.IssueID)
		}
	}
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		value := func(key string) (string, bool) {
			raw, ok := rule.Filters[key]
			return fmt.Sprint(raw), ok
		}
		switch {
		case len(rule.TeamIDs) > 0:
			scope.IssueColumns = append(scope.IssueColumns, store.IssueColumnScope{Column: "team_id", Values: rule.TeamIDs})
		default:
			if project, ok := value("project"); ok {
				scope.IssueColumns = append(scope.IssueColumns, store.IssueColumnScope{Column: "project_id", Values: []string{project}})
			} else if label, ok := value("label"); ok {
				for _, item := range data.Labels {
					if item.ID == label || strings.EqualFold(item.Name, label) {
						scope.LabelIssues = append(scope.LabelIssues, item.ID)
					}
				}
				scope.LabelIssues = append(scope.LabelIssues, label)
			} else if status, ok := value("status"); ok {
				states := []string{status}
				for _, state := range data.States {
					if state.Type == status {
						states = append(states, state.ID)
					}
				}
				scope.IssueColumns = append(scope.IssueColumns, store.IssueColumnScope{Column: "state_id", Values: states})
			} else {
				scope.AllIssues = true
			}
		}
	}
	return scope
}

// trashRestoreScope lets restoring a deleted issue recreate its record and
// content; other trash entries are metadata only.
func trashRestoreScope(ctx context.Context, entryID string) context.Context {
	return store.WithMutationScope(ctx, store.MutationScope{Resolve: func(data domain.Bootstrap) store.MutationScope {
		for _, entry := range data.Trash {
			if entry.ID != entryID || entry.ResourceType != "issue" {
				continue
			}
			var value deletedIssuePayload
			if json.Unmarshal(entry.Payload, &value) != nil || value.Issue.ID == "" {
				return store.MutationScope{}
			}
			return store.MutationScope{IssueIDs: []string{value.Issue.ID}, Resources: []string{value.Issue.ID}, CreateIssues: true}
		}
		return store.MutationScope{}
	}})
}

// issueCreationScope lets a callback create issues (numbered from the
// workspace sequence) without loading existing ones.
func issueCreationScope(ctx context.Context, resolve func(domain.Bootstrap) store.MutationScope) context.Context {
	return store.WithMutationScope(ctx, store.MutationScope{CreateIssues: true, Resolve: resolve})
}

// recurringIssueScope loads a recurring issue and the other issues of its
// series (to skip an occurrence that already exists) and lets the callback
// create the next occurrence.
func recurringIssueScope(ctx context.Context, sourceID string) context.Context {
	return store.WithMutationScope(ctx, store.MutationScope{IssueIDs: []string{sourceID}, CreateIssues: true, Expand: func(issues []domain.Issue) store.MutationScope {
		series := sourceID
		for _, issue := range issues {
			if issue.ID == sourceID && issue.RecurrenceSeriesID != "" {
				series = issue.RecurrenceSeriesID
			}
		}
		fragment, _ := json.Marshal(series)
		return store.MutationScope{IssueDataContains: []string{`"recurrenceSeriesId":` + string(fragment)}}
	}})
}

// codeReviewScope loads the issues a pull/merge request webhook can link or
// move: the review's linked issues, issues whose identifier appears in the
// request (and in the stored review), their status-automation family, and
// the notifications keyed by the delivery id.
func codeReviewScope(ctx context.Context, provider, eventID string, event externalCodeReviewEvent) context.Context {
	raw, _ := json.Marshal(event)
	return store.WithMutationScope(ctx, store.MutationScope{Resources: []string{eventID}, StatusAutomation: true, Resolve: func(data domain.Bootstrap) store.MutationScope {
		scope := store.MutationScope{}
		haystack := []string{string(raw)}
		for _, review := range data.Reviews {
			if review.Provider == provider && review.ExternalID == event.ExternalID() || slices.ContainsFunc(review.Events, func(item domain.ReviewEvent) bool { return item.ID == eventID }) {
				scope.IssueIDs = append(scope.IssueIDs, review.IssueIDs...)
				haystack = append(haystack, review.Title, review.Description, review.HeadBranch)
			}
		}
		scope.IssueIdentifiers = identifierCandidates(strings.Join(haystack, " "))
		return scope
	}})
}

// identifierCandidates lists every string an issue identifier (KEY-123)
// contained in text could be: each suffix of a key-like run before a hyphen
// combined with each prefix of the digits after it, in upper case and as
// written.
func identifierCandidates(text string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range []string{strings.ToUpper(text), text} {
		for index := 0; index < len(value); index++ {
			if value[index] != '-' {
				continue
			}
			end := index + 1
			for end < len(value) && value[end] >= '0' && value[end] <= '9' {
				end++
			}
			if end == index+1 {
				continue
			}
			start := index
			for start > 0 && !unicode.IsSpace(rune(value[start-1])) && index-start < 64 {
				start--
			}
			for keyStart := start; keyStart < index; keyStart++ {
				for numberEnd := index + 2; numberEnd <= end && numberEnd-index <= 12; numberEnd++ {
					candidate := value[keyStart:numberEnd]
					if !seen[candidate] {
						seen[candidate] = true
						result = append(result, candidate)
					}
				}
			}
		}
	}
	return result
}

// reviewActivityScope loads the activity threads of the matching reviews'
// linked issues (deploy previews append to them) and no issue records.
func reviewActivityScope(ctx context.Context, match func(domain.CodeReview) bool) context.Context {
	return store.WithMutationScope(ctx, store.MutationScope{Resolve: func(data domain.Bootstrap) store.MutationScope {
		scope := store.MutationScope{}
		for _, review := range data.Reviews {
			if match(review) {
				scope.Resources = append(scope.Resources, review.IssueIDs...)
			}
		}
		return scope
	}})
}
