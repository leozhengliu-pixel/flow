package main

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// Run only on the completion edge. Replayed CI events and later metadata edits
// must not move an issue back after a person has changed its status.
//
// release must point at the release being completed; completion may append a
// follow-up release to data.Releases, so callers must not keep using the
// pointer afterwards (indexes stay valid because new releases are appended).
func applyReleaseSettingAutomations(data *domain.Bootstrap, before string, release *domain.Release, now time.Time) error {
	if before == "released" || release.Status != "released" || release.ArchivedAt != nil {
		return nil
	}
	pipeline := releasePipelineByID(data, release.PipelineID)
	if pipeline == nil {
		return nil
	}
	// Status automations run first (shipped issues move to their done state);
	// notes then describe what shipped, and issues still open roll over.
	if err := applyReleaseStatusAutomations(data, pipeline, release, now); err != nil {
		return err
	}
	if pipeline.AutoGenerateReleaseNotes {
		generateReleaseNotesOnCompletion(data, pipeline, release, now)
	}
	if pipeline.Type == "scheduled" && (pipeline.MoveOpenIssuesToNextRelease == nil || *pipeline.MoveOpenIssuesToNextRelease) {
		rolloverOpenReleaseIssues(data, pipeline, release, now)
	}
	return nil
}

// applyReleaseStatusAutomations applies the teams' release automation rules to
// a completed production release's issues.
func applyReleaseStatusAutomations(data *domain.Bootstrap, pipeline *domain.ReleasePipeline, release *domain.Release, now time.Time) error {
	if !pipeline.Production {
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

func issueOpen(issue *domain.Issue) bool {
	return issue.ArchivedAt == nil && issue.State.Type != "completed" && issue.State.Type != "canceled"
}

// rolloverOpenReleaseIssues implements Linear's "Move open issues to the next
// release": open issues leave the completed release for the next eligible
// release in the pipeline, which is created when none exists. Completed and
// canceled issues stay.
func rolloverOpenReleaseIssues(data *domain.Bootstrap, pipeline *domain.ReleasePipeline, release *domain.Release, now time.Time) {
	open := []string{}
	for _, id := range release.IssueIDs {
		if issue, err := issueByID(data, id); err == nil && issueOpen(issue) {
			open = append(open, id)
		}
	}
	if len(open) == 0 {
		return
	}
	release.IssueIDs = slices.DeleteFunc(slices.Clone(release.IssueIDs), func(id string) bool { return slices.Contains(open, id) })
	release.UpdatedAt = now
	completedID, completedName, completedVersion := release.ID, release.Name, release.Version

	candidates := []int{}
	for index, item := range data.Releases {
		if item.PipelineID == pipeline.ID && item.ID != completedID && item.ArchivedAt == nil && (item.Status == "planned" || item.Status == "inProgress") && !releaseStageFrozen(data, item) {
			candidates = append(candidates, index)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := data.Releases[candidates[i]], data.Releases[candidates[j]]
		if (a.TargetDate == nil) != (b.TargetDate == nil) {
			return a.TargetDate != nil
		}
		if a.TargetDate != nil && *a.TargetDate != *b.TargetDate {
			return *a.TargetDate < *b.TargetDate
		}
		return releaseLess(a, b)
	})
	history := func(releaseID, action string, metadata map[string]any) {
		data.ReleaseHistory = append(data.ReleaseHistory, domain.ReleaseHistory{ID: fmt.Sprintf("release_history_%d_%s", now.UnixNano(), action), ReleaseID: releaseID, Actor: data.Viewer, Action: action, Metadata: metadata, CreatedAt: now})
	}
	if len(candidates) > 0 {
		next := &data.Releases[candidates[0]]
		for _, id := range open {
			if !slices.Contains(next.IssueIDs, id) {
				next.IssueIDs = append(next.IssueIDs, id)
			}
		}
		next.UpdatedAt = now
		history(completedID, "issues_rolled_over", map[string]any{"issueIds": open, "toReleaseId": next.ID})
		history(next.ID, "issues_rolled_in", map[string]any{"issueIds": open, "fromReleaseId": completedID})
		return
	}
	stage := firstReleaseStage(pipeline, "planned", false)
	status := "planned"
	if stage == "" {
		stage, status = firstReleaseStage(pipeline, "inProgress", true), "inProgress"
	}
	version := nextReleaseVersion(completedVersion)
	name := version
	if name == "" || completedName != completedVersion {
		name = strings.TrimSpace(completedName + " (next)")
	}
	created := domain.Release{ID: fmt.Sprintf("release_%d_next", now.UnixNano()), SlugID: uniqueReleaseSlug(data, name, now), Name: name, Version: version, PipelineID: pipeline.ID, Stage: stage, Status: status, Position: nextReleasePosition(data, pipeline.ID), ProjectIDs: []string{}, IssueIDs: open, SubscriberIDs: []string{data.Viewer.ID}, Resources: []domain.ReleaseResource{}, Creator: data.Viewer, CreatedAt: now, UpdatedAt: now}
	if status == "inProgress" {
		created.StartedAt = &now
	}
	data.Releases = append(data.Releases, created)
	history(completedID, "issues_rolled_over", map[string]any{"issueIds": open, "toReleaseId": created.ID})
	history(created.ID, "created", map[string]any{"name": created.Name, "fromReleaseId": completedID})
}

// firstReleaseStage returns the pipeline's first stage of a status, optionally
// skipping frozen stages.
func firstReleaseStage(pipeline *domain.ReleasePipeline, status string, skipFrozen bool) string {
	for _, stage := range pipeline.Stages {
		if pipeline.StageStatuses[stage] == status && (!skipFrozen || !slices.Contains(pipeline.FrozenStages, stage)) {
			return stage
		}
	}
	return ""
}

var releaseVersionPattern = regexp.MustCompile(`^(.*?)(\d+)$`)

// nextReleaseVersion bumps the last number of a version ("1.4.0" -> "1.4.1",
// "v12" -> "v13"); versions without a trailing number get no successor.
func nextReleaseVersion(version string) string {
	match := releaseVersionPattern.FindStringSubmatch(strings.TrimSpace(version))
	if match == nil {
		return ""
	}
	number, err := strconv.Atoi(match[2])
	if err != nil {
		return ""
	}
	return match[1] + strconv.Itoa(number+1)
}

// generateReleaseNotesOnCompletion fills in release notes from the pipeline
// template and the release's completed issues when a release completes
// without notes ("Auto-generate on completion"). A {{issues}} placeholder in the
// template receives the issue list; otherwise the list follows the template.
func generateReleaseNotesOnCompletion(data *domain.Bootstrap, pipeline *domain.ReleasePipeline, release *domain.Release, now time.Time) {
	if strings.TrimSpace(release.ReleaseNotes) != "" || slices.ContainsFunc(data.ReleaseNotes, func(note domain.ReleaseNote) bool {
		return note.ReleaseID == release.ID && strings.TrimSpace(note.Body) != ""
	}) {
		return
	}
	lines := []string{}
	for _, id := range release.IssueIDs {
		issue, err := issueByID(data, id)
		if err != nil || issue.ArchivedAt != nil || issue.State.Type != "completed" {
			continue
		}
		lines = append(lines, "- "+strings.TrimSpace(issue.Identifier+" "+issue.Title))
	}
	if len(lines) == 0 {
		return
	}
	list := strings.Join(lines, "\n")
	template := strings.TrimSpace(pipeline.ReleaseNotesTemplate)
	body := list
	switch {
	case strings.Contains(template, "{{issues}}"):
		body = strings.ReplaceAll(template, "{{issues}}", list)
	case template != "":
		body = template + "\n\n" + list
	}
	setReleaseNotes(data, release, body, now, "note_generated")
}

// setReleaseNotes stores notes on the release and its note record so the
// release page and changelog show the same content.
func setReleaseNotes(data *domain.Bootstrap, release *domain.Release, body string, now time.Time, action string) {
	release.ReleaseNotes = body
	release.UpdatedAt = now
	index := slices.IndexFunc(data.ReleaseNotes, func(note domain.ReleaseNote) bool { return note.ReleaseID == release.ID })
	noteID := ""
	if index >= 0 {
		data.ReleaseNotes[index].Body, data.ReleaseNotes[index].BodyData, data.ReleaseNotes[index].UpdatedAt = body, nil, now
		noteID = data.ReleaseNotes[index].ID
	} else {
		note := domain.ReleaseNote{ID: fmt.Sprintf("release_note_%d", now.UnixNano()), ReleaseID: release.ID, Title: release.Name, Body: body, Creator: data.Viewer, CreatedAt: now, UpdatedAt: now}
		data.ReleaseNotes = append(data.ReleaseNotes, note)
		noteID = note.ID
	}
	data.ReleaseHistory = append(data.ReleaseHistory, domain.ReleaseHistory{ID: fmt.Sprintf("release_history_%d_%s", now.UnixNano(), action), ReleaseID: release.ID, Actor: data.Viewer, Action: action, Metadata: map[string]any{"noteId": noteID}, CreatedAt: now})
}
