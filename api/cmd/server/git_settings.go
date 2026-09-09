package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"flow/api/internal/domain"
)

func reviewAutomationEvent(review domain.CodeReview, action string) string {
	if review.Status == "merged" {
		return "merged"
	}
	switch action {
	case "review_requested", "review_request_removed", "submitted", "reviewed", "approved":
		return "reviewActivity"
	case "ready_for_review", "ready_for_merge":
		return "ready"
	}
	if review.Draft {
		return "draft"
	}
	if action == "opened" || action == "open" || action == "reopened" {
		return "opened"
	}
	return ""
}

func branchMatches(pattern, kind, branch string) bool {
	if pattern == "" {
		return true
	}
	if kind == "regex" {
		r, err := regexp.Compile(pattern)
		return err == nil && r.MatchString(branch)
	}
	matched, err := filepath.Match(pattern, branch)
	return err == nil && matched
}

func applyGitSettingAutomations(data *domain.Bootstrap, review domain.CodeReview, event string, now time.Time) error {
	if event == "" {
		return nil
	}
	repository := review.RepositoryOwner + "/" + review.RepositoryName
	for _, issueID := range review.IssueIDs {
		issue, err := issueByID(data, issueID)
		if err != nil {
			continue
		}
		stateID := ""
		for _, rule := range data.GitAutomationStates {
			if rule.Enabled && rule.TeamID == issue.Team.ID && rule.Event == event && (rule.Repository == "*" || strings.EqualFold(rule.Repository, repository)) {
				if stateID == "" || rule.Repository != "*" {
					stateID = rule.WorkflowStateID
				}
			}
		}
		if stateID == "" {
			stateID = data.TeamSettings[issue.Team.ID].PRAutomations[event]
		}
		for _, branch := range data.TargetBranches {
			if branch.TeamID == issue.Team.ID && branchMatches(branch.Branch, branch.Repository, review.BaseBranch) {
				if override, found := branch.AutomationStates[event]; found {
					stateID = override
					break
				}
			}
		}
		if stateID == "" || stateID == issue.State.ID {
			continue
		}
		if stateForTeam(data, issue.Team.ID, stateID) == nil {
			return fmt.Errorf("%w: invalid PR automation status", errInvalid)
		}
		changes, err := applyUpdate(data, issue, domain.IssueUpdateInput{StateID: &stateID})
		if err != nil {
			return err
		}
		issue.UpdatedAt = now
		changes["reviewId"], changes["automation"] = review.ID, event
		activity := appendActivity(data, issue.ID, "issue.updated", data.Viewer, changes)
		appendIssueNotifications(data, *issue, activity, nil)
	}
	return nil
}
