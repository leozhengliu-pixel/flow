package store

import (
	"context"
	"encoding/json"
	"maps"
	"slices"

	"flow/api/internal/domain"
)

// teamDeletionSnapshot is the pre-image of one team. Deletes are keyed from
// this copy so the SQL write stays proportional to that team, not the catalog.
type teamDeletionSnapshot struct {
	teamID        string
	teams         []domain.Team
	states        []domain.WorkflowState
	labels        []domain.IssueLabel
	cycles        []domain.Cycle
	templates     []domain.IssueTemplate
	members       []domain.TeamMember
	sections      []domain.TeamResourceSection
	resources     []domain.TeamPinnedResource
	favorites     []domain.Favorite
	subscriptions []domain.Subscription
	projects      []domain.Project
	pipelines     []domain.ReleasePipeline
	settings      map[string]domain.TeamSettings
	cycleSettings map[string]domain.CycleSettings
}

func snapshotTeamDeletion(teamID string, data *domain.Bootstrap) *teamDeletionSnapshot {
	if teamID == "" || domain.TeamIndex(data, teamID) < 0 {
		return nil
	}
	return &teamDeletionSnapshot{
		teamID:        teamID,
		teams:         slices.Clone(data.Teams),
		states:        slices.Clone(data.States),
		labels:        slices.Clone(data.Labels),
		cycles:        slices.Clone(data.Cycles),
		templates:     slices.Clone(data.IssueTemplates),
		members:       slices.Clone(data.TeamMembers),
		sections:      slices.Clone(data.TeamResourceSections),
		resources:     slices.Clone(data.TeamPinnedResources),
		favorites:     slices.Clone(data.Favorites),
		subscriptions: slices.Clone(data.Subscriptions),
		projects:      cloneProjects(data.Projects),
		pipelines:     clonePipelines(data.ReleasePipelines),
		settings:      maps.Clone(data.TeamSettings),
		cycleSettings: maps.Clone(data.CycleSettings),
	}
}

func cloneProjects(items []domain.Project) []domain.Project {
	out := slices.Clone(items)
	for i := range out {
		out[i].TeamIDs = slices.Clone(items[i].TeamIDs)
	}
	return out
}

func clonePipelines(items []domain.ReleasePipeline) []domain.ReleasePipeline {
	out := slices.Clone(items)
	for i := range out {
		out[i].TeamIDs = slices.Clone(items[i].TeamIDs)
	}
	return out
}

func restoreTeamDeletion(next *domain.Bootstrap, snap *teamDeletionSnapshot) bool {
	if snap == nil {
		return false
	}
	next.Teams = snap.teams
	next.States = snap.states
	next.Labels = snap.labels
	next.Cycles = snap.cycles
	next.IssueTemplates = snap.templates
	next.TeamMembers = snap.members
	next.TeamResourceSections = snap.sections
	next.TeamPinnedResources = snap.resources
	next.Favorites = snap.favorites
	next.Subscriptions = snap.subscriptions
	next.Projects = snap.projects
	next.ReleasePipelines = snap.pipelines
	next.TeamSettings = snap.settings
	next.CycleSettings = snap.cycleSettings
	domain.RebuildTeamDirectory(next)
	return true
}

func collectTeamDeletionRecords(snap *teamDeletionSnapshot, next domain.Bootstrap) []metadataRecordChange {
	changes := []metadataRecordChange{}
	drop := func(field, key string) {
		if key == "" {
			return
		}
		changes = append(changes, metadataRecordChange{field: field, key: key, drop: true})
	}
	drop("teams", snap.teamID)
	if _, ok := snap.settings[snap.teamID]; ok {
		drop("teamSettings", snap.teamID)
	}
	if _, ok := snap.cycleSettings[snap.teamID]; ok {
		drop("cycleSettings", snap.teamID)
	}
	cycleIDs := map[string]bool{}
	for _, state := range snap.states {
		if state.TeamID == snap.teamID {
			drop("states", state.ID)
		}
	}
	for _, label := range snap.labels {
		if label.Scope == snap.teamID {
			drop("labels", label.ID)
		}
	}
	for _, cycle := range snap.cycles {
		if cycle.TeamID == snap.teamID {
			cycleIDs[cycle.ID] = true
			drop("cycles", cycle.ID)
		}
	}
	for _, template := range snap.templates {
		if template.TeamID == snap.teamID {
			drop("issueTemplates", template.ID)
		}
	}
	for _, section := range snap.sections {
		if section.TeamID == snap.teamID {
			drop("teamResourceSections", section.ID)
		}
	}
	for _, resource := range snap.resources {
		if resource.TeamID == snap.teamID {
			drop("teamPinnedResources", resource.ID)
		}
	}
	for _, favorite := range snap.favorites {
		if favorite.ResourceType == "team" && favorite.ResourceID == snap.teamID || favorite.ResourceType == "cycle" && cycleIDs[favorite.ResourceID] {
			drop("favorites", favorite.ID)
		}
	}
	for _, subscription := range snap.subscriptions {
		if subscription.ResourceType == "team" && subscription.ResourceID == snap.teamID || subscription.ResourceType == "cycle" && cycleIDs[subscription.ResourceID] {
			drop("subscriptions", subscription.ID)
		}
	}
	for id, old := range snap.settings {
		if id == snap.teamID || old.ParentTeamID != snap.teamID {
			continue
		}
		current, ok := next.TeamSettings[id]
		if !ok || metadataTeamSettingsEqual(old, current) {
			continue
		}
		raw, err := json.Marshal(current)
		if err != nil {
			continue
		}
		changes = append(changes, metadataRecordChange{field: "teamSettings", key: id, raw: raw})
	}
	previousProjects := map[string]domain.Project{}
	for _, project := range snap.projects {
		if slices.Contains(project.TeamIDs, snap.teamID) {
			previousProjects[project.ID] = project
		}
	}
	for _, project := range next.Projects {
		previous, ok := previousProjects[project.ID]
		if !ok || slices.Equal(previous.TeamIDs, project.TeamIDs) {
			continue
		}
		raw, err := json.Marshal(project)
		if err != nil {
			continue
		}
		changes = append(changes, metadataRecordChange{field: "projects", key: project.ID, raw: raw})
	}
	previousPipelines := map[string]domain.ReleasePipeline{}
	for _, pipeline := range snap.pipelines {
		if slices.Contains(pipeline.TeamIDs, snap.teamID) {
			previousPipelines[pipeline.ID] = pipeline
		}
	}
	for _, pipeline := range next.ReleasePipelines {
		previous, ok := previousPipelines[pipeline.ID]
		if !ok || slices.Equal(previous.TeamIDs, pipeline.TeamIDs) {
			continue
		}
		raw, err := json.Marshal(pipeline)
		if err != nil {
			continue
		}
		changes = append(changes, metadataRecordChange{field: "releasePipelines", key: pipeline.ID, raw: raw})
	}
	return changes
}

func deleteTeamOwnedIssueRecords(ctx context.Context, tx *sqlTx, workspace, teamID string) error {
	if teamID == "" {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT project_id,assignee_id,state_id,state_type,priority,archived,COUNT(*) FROM issue_records WHERE workspace_key=? AND team_id=? GROUP BY project_id,assignee_id,state_id,state_type,priority,archived`, workspace, teamID)
	if err != nil {
		return err
	}
	deltas := map[issueStatsKey]int64{}
	var removed int64
	for rows.Next() {
		var project, assignee, state, stateType string
		var priority, archived int
		var count int64
		if err := rows.Scan(&project, &assignee, &state, &stateType, &priority, &archived, &count); err != nil {
			rows.Close()
			return err
		}
		removed += count
		addIssueStats(deltas, &issueStatsDimensions{Team: teamID, Project: project, Assignee: assignee, State: state, Type: stateType, Priority: priority, Archived: archived}, -count)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || removed == 0 {
		return err
	}
	if err := writeIssueStats(ctx, tx, workspace, deltas); err != nil {
		return err
	}
	for _, table := range []string{"issue_label_records", "issue_permission_records", "issue_subscriber_records", "issue_actor_records", "issue_attribute_records", "issue_attachment_records", "issue_search_documents"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE workspace_key=? AND issue_id IN (SELECT id FROM issue_records WHERE workspace_key=? AND team_id=?)`, workspace, workspace, teamID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_content_records WHERE workspace_key=? AND resource_id IN (SELECT id FROM issue_records WHERE workspace_key=? AND team_id=?)`, workspace, workspace, teamID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM issue_records WHERE workspace_key=? AND team_id=?`, workspace, teamID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE issue_collection_counts SET total=total-? WHERE workspace_key=? AND total>=?`, removed, workspace, removed)
	return err
}
