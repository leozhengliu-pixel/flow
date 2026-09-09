package main

import (
	"slices"
	"time"

	"flow/api/internal/domain"
)

// Run only on the completion edge. Replayed CI events and later metadata edits
// must not move an issue back after a person has changed its status.
func applyReleaseSettingAutomations(data *domain.Bootstrap, before string, release domain.Release, now time.Time) error {
	if before == "released" || release.Status != "released" || release.ArchivedAt != nil {
		return nil
	}
	pipeline := releasePipelineByID(data, release.PipelineID)
	if pipeline == nil || !pipeline.Production {
		return nil
	}
	for _, id := range release.IssueIDs {
		issue, err := issueByID(data, id)
		if err != nil || issue.ArchivedAt != nil || len(pipeline.TeamIDs) > 0 && !slices.Contains(pipeline.TeamIDs, issue.Team.ID) {
			continue
		}
		stateID := ""
		for _, rule := range data.TeamSettings[issue.Team.ID].ReleaseAutomations {
			if !rule.Enabled {
				continue
			}
			if rule.Trigger == pipeline.ID {
				stateID = rule.Action
				break
			}
			if rule.Trigger == "" || rule.Trigger == "*" {
				stateID = rule.Action
			}
		}
		if stateID == "" || stateID == issue.State.ID || stateForTeam(data, issue.Team.ID, stateID) == nil {
			continue
		}
		changes, err := applyUpdate(data, issue, domain.IssueUpdateInput{StateID: &stateID})
		if err != nil {
			return err
		}
		issue.UpdatedAt = now
		changes["releaseId"], changes["automation"] = release.ID, "release.completed"
		activity := appendActivity(data, issue.ID, "issue.updated", data.Viewer, changes)
		appendIssueNotifications(data, *issue, activity, nil)
	}
	return nil
}
