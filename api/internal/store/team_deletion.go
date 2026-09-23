package store

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

var (
	ErrTeamNotFound      = errors.New("team not found")
	ErrLastWorkspaceTeam = errors.New("a workspace needs at least one team")
)

// teamDeletionSnapshot keeps only the deleted team's own rows and the records
// that directly reference it. It does not clone the rest of the catalog.
type teamDeletionSnapshot struct {
	teamID          string
	team            domain.Team
	hasSettings     bool
	ownSettings     domain.TeamSettings
	hasCycle        bool
	ownCycle        domain.CycleSettings
	childSettings   map[string]domain.TeamSettings
	states          []domain.WorkflowState
	labels          []domain.IssueLabel
	cycles          []domain.Cycle
	templates       []domain.IssueTemplate
	sections        []domain.TeamResourceSection
	resources       []domain.TeamPinnedResource
	members         []domain.TeamMember
	favorites       []domain.Favorite
	subscriptions   []domain.Subscription
	projects        []domain.Project
	pipelines       []domain.ReleasePipeline
	removedIssues   int64
	projectsTouched bool
}

func snapshotTeamDeletion(teamID string, data *domain.Bootstrap) *teamDeletionSnapshot {
	if teamID == "" || data == nil || domain.TeamIndex(data, teamID) < 0 {
		return nil
	}
	snap := &teamDeletionSnapshot{teamID: teamID, team: data.Teams[domain.TeamIndex(data, teamID)]}
	if settings, ok := data.TeamSettings[teamID]; ok {
		snap.hasSettings = true
		snap.ownSettings = settings
	}
	if settings, ok := data.CycleSettings[teamID]; ok {
		snap.hasCycle = true
		snap.ownCycle = settings
	}
	snap.childSettings = map[string]domain.TeamSettings{}
	for _, childID := range data.TeamChildren[teamID] {
		if settings, ok := data.TeamSettings[childID]; ok && settings.ParentTeamID == teamID {
			snap.childSettings[childID] = settings
		}
	}
	for _, state := range data.States {
		if state.TeamID == teamID {
			snap.states = append(snap.states, state)
		}
	}
	for _, label := range data.Labels {
		if label.Scope == teamID {
			snap.labels = append(snap.labels, label)
		}
	}
	for _, cycle := range data.Cycles {
		if cycle.TeamID == teamID {
			snap.cycles = append(snap.cycles, cycle)
		}
	}
	for _, template := range data.IssueTemplates {
		if template.TeamID == teamID {
			snap.templates = append(snap.templates, template)
		}
	}
	for _, section := range data.TeamResourceSections {
		if section.TeamID == teamID {
			snap.sections = append(snap.sections, section)
		}
	}
	for _, resource := range data.TeamPinnedResources {
		if resource.TeamID == teamID {
			snap.resources = append(snap.resources, resource)
		}
	}
	for _, member := range data.TeamMembers {
		if member.TeamID == teamID {
			snap.members = append(snap.members, member)
		}
	}
	cycleIDs := map[string]bool{}
	for _, cycle := range snap.cycles {
		cycleIDs[cycle.ID] = true
	}
	for _, favorite := range data.Favorites {
		if referencesDeletedTeam(favorite.ResourceType, favorite.ResourceID, teamID, cycleIDs) {
			snap.favorites = append(snap.favorites, favorite)
		}
	}
	for _, subscription := range data.Subscriptions {
		if referencesDeletedTeam(subscription.ResourceType, subscription.ResourceID, teamID, cycleIDs) {
			snap.subscriptions = append(snap.subscriptions, subscription)
		}
	}
	for _, project := range data.Projects {
		if slices.Contains(project.TeamIDs, teamID) {
			project.TeamIDs = slices.Clone(project.TeamIDs)
			snap.projects = append(snap.projects, project)
		}
	}
	for _, pipeline := range data.ReleasePipelines {
		if slices.Contains(pipeline.TeamIDs, teamID) {
			pipeline.TeamIDs = slices.Clone(pipeline.TeamIDs)
			snap.pipelines = append(snap.pipelines, pipeline)
		}
	}
	return snap
}

func referencesDeletedTeam(kind, id, teamID string, cycleIDs map[string]bool) bool {
	return kind == "team" && id == teamID || kind == "cycle" && cycleIDs[id]
}

func restoreTeamDeletion(next *domain.Bootstrap, snap *teamDeletionSnapshot) bool {
	if snap == nil {
		return false
	}
	if domain.TeamIndex(next, snap.teamID) >= 0 {
		return true
	}
	next.Teams = append(next.Teams, snap.team)
	if next.TeamSettings == nil {
		next.TeamSettings = map[string]domain.TeamSettings{}
	}
	if snap.hasSettings {
		next.TeamSettings[snap.teamID] = snap.ownSettings
	}
	if next.CycleSettings == nil {
		next.CycleSettings = map[string]domain.CycleSettings{}
	}
	if snap.hasCycle {
		next.CycleSettings[snap.teamID] = snap.ownCycle
	}
	for id, settings := range snap.childSettings {
		next.TeamSettings[id] = settings
	}
	next.States = append(next.States, snap.states...)
	next.Labels = append(next.Labels, snap.labels...)
	next.Cycles = append(next.Cycles, snap.cycles...)
	next.IssueTemplates = append(next.IssueTemplates, snap.templates...)
	next.TeamResourceSections = append(next.TeamResourceSections, snap.sections...)
	next.TeamPinnedResources = append(next.TeamPinnedResources, snap.resources...)
	next.TeamMembers = append(next.TeamMembers, snap.members...)
	next.Favorites = append(next.Favorites, snap.favorites...)
	next.Subscriptions = append(next.Subscriptions, snap.subscriptions...)
	restoreProjectTeams(next.Projects, snap.projects)
	restorePipelineTeams(next.ReleasePipelines, snap.pipelines)
	domain.RebuildTeamDirectory(next)
	return true
}

func restoreProjectTeams(current, previous []domain.Project) {
	previousByID := make(map[string]domain.Project, len(previous))
	for _, project := range previous {
		previousByID[project.ID] = project
	}
	for i := range current {
		if old, ok := previousByID[current[i].ID]; ok {
			current[i].TeamIDs = slices.Clone(old.TeamIDs)
		}
	}
}

func restorePipelineTeams(current, previous []domain.ReleasePipeline) {
	previousByID := make(map[string]domain.ReleasePipeline, len(previous))
	for _, pipeline := range previous {
		previousByID[pipeline.ID] = pipeline
	}
	for i := range current {
		if old, ok := previousByID[current[i].ID]; ok {
			current[i].TeamIDs = slices.Clone(old.TeamIDs)
		}
	}
}

// ApplyTeamDeletion removes one team and the rows that point at it.
// Issue rows are deleted later by team_id, not by scanning data.Issues.
func ApplyTeamDeletion(data *domain.Bootstrap, teamID string) error {
	if data == nil || teamID == "" {
		return ErrTeamNotFound
	}
	domain.EnsureTeamDirectory(data)
	if len(data.Teams) <= 1 {
		return ErrLastWorkspaceTeam
	}
	index := domain.TeamIndex(data, teamID)
	if index < 0 {
		return ErrTeamNotFound
	}
	children := append([]string(nil), data.TeamChildren[teamID]...)
	domain.NoteTeamRemoved(data, index)
	delete(data.TeamSettings, teamID)
	delete(data.CycleSettings, teamID)
	for _, childID := range children {
		settings, ok := data.TeamSettings[childID]
		if !ok || settings.ParentTeamID != teamID {
			continue
		}
		settings.ParentTeamID = ""
		data.TeamSettings[childID] = settings
	}
	data.States = dropMatching(data.States, func(state domain.WorkflowState) bool { return state.TeamID == teamID })
	removeTeamLabels(data, teamID)
	var cycleIDs map[string]bool
	data.Cycles, cycleIDs = dropCycles(data.Cycles, teamID)
	data.IssueTemplates = dropMatching(data.IssueTemplates, func(template domain.IssueTemplate) bool { return template.TeamID == teamID })
	data.TeamResourceSections = dropMatching(data.TeamResourceSections, func(section domain.TeamResourceSection) bool { return section.TeamID == teamID })
	data.TeamPinnedResources = dropMatching(data.TeamPinnedResources, func(resource domain.TeamPinnedResource) bool { return resource.TeamID == teamID })
	data.TeamMembers = dropMatching(data.TeamMembers, func(member domain.TeamMember) bool { return member.TeamID == teamID })
	data.Favorites = dropMatching(data.Favorites, func(favorite domain.Favorite) bool {
		return referencesDeletedTeam(favorite.ResourceType, favorite.ResourceID, teamID, cycleIDs)
	})
	data.Subscriptions = dropMatching(data.Subscriptions, func(subscription domain.Subscription) bool {
		return referencesDeletedTeam(subscription.ResourceType, subscription.ResourceID, teamID, cycleIDs)
	})
	for i := range data.Projects {
		if slices.Contains(data.Projects[i].TeamIDs, teamID) {
			data.Projects[i].TeamIDs = removeID(data.Projects[i].TeamIDs, teamID)
		}
	}
	for i := range data.ReleasePipelines {
		if slices.Contains(data.ReleasePipelines[i].TeamIDs, teamID) {
			data.ReleasePipelines[i].TeamIDs = removeID(data.ReleasePipelines[i].TeamIDs, teamID)
		}
	}
	return nil
}

func dropMatching[T any](items []T, drop func(T) bool) []T {
	for i := 0; i < len(items); {
		if !drop(items[i]) {
			i++
			continue
		}
		last := len(items) - 1
		items[i] = items[last]
		items = items[:last]
	}
	return items
}

func dropCycles(items []domain.Cycle, teamID string) ([]domain.Cycle, map[string]bool) {
	ids := map[string]bool{}
	for i := 0; i < len(items); {
		if items[i].TeamID != teamID {
			i++
			continue
		}
		ids[items[i].ID] = true
		last := len(items) - 1
		items[i] = items[last]
		items = items[:last]
	}
	return items, ids
}

func removeTeamLabels(data *domain.Bootstrap, teamID string) {
	if data.LabelIndex == nil {
		data.Labels = dropMatching(data.Labels, func(label domain.IssueLabel) bool { return label.Scope == teamID })
		return
	}
	indexes := append([]int(nil), data.LabelIndex[teamID]...)
	slices.Sort(indexes)
	for i := len(indexes) - 1; i >= 0; i-- {
		index := indexes[i]
		if index < 0 || index >= len(data.Labels) {
			continue
		}
		last := len(data.Labels) - 1
		if index != last {
			moved := data.Labels[last]
			data.Labels[index] = moved
			if moved.Scope != "" {
				refs := data.LabelIndex[moved.Scope]
				for j, ref := range refs {
					if ref == last {
						refs[j] = index
						break
					}
				}
			}
		}
		data.Labels = data.Labels[:last]
	}
	delete(data.LabelIndex, teamID)
}

func removeID(items []string, target string) []string {
	kept := items[:0]
	for _, item := range items {
		if item != target {
			kept = append(kept, item)
		}
	}
	return kept
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
	if snap.hasSettings {
		drop("teamSettings", snap.teamID)
	}
	if snap.hasCycle {
		drop("cycleSettings", snap.teamID)
	}
	for _, state := range snap.states {
		drop("states", state.ID)
	}
	for _, label := range snap.labels {
		drop("labels", label.ID)
	}
	for _, cycle := range snap.cycles {
		drop("cycles", cycle.ID)
	}
	for _, template := range snap.templates {
		drop("issueTemplates", template.ID)
	}
	for _, section := range snap.sections {
		drop("teamResourceSections", section.ID)
	}
	for _, resource := range snap.resources {
		drop("teamPinnedResources", resource.ID)
	}
	for _, favorite := range snap.favorites {
		drop("favorites", favorite.ID)
	}
	for _, subscription := range snap.subscriptions {
		drop("subscriptions", subscription.ID)
	}
	for id, old := range snap.childSettings {
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
	if len(snap.projects) > 0 {
		previous := make(map[string]domain.Project, len(snap.projects))
		for _, project := range snap.projects {
			previous[project.ID] = project
		}
		for _, project := range next.Projects {
			old, ok := previous[project.ID]
			if !ok || slices.Equal(old.TeamIDs, project.TeamIDs) {
				continue
			}
			raw, err := json.Marshal(project)
			if err != nil {
				continue
			}
			snap.projectsTouched = true
			changes = append(changes, metadataRecordChange{field: "projects", key: project.ID, raw: raw})
		}
	}
	if len(snap.pipelines) > 0 {
		previous := make(map[string]domain.ReleasePipeline, len(snap.pipelines))
		for _, pipeline := range snap.pipelines {
			previous[pipeline.ID] = pipeline
		}
		for _, pipeline := range next.ReleasePipelines {
			old, ok := previous[pipeline.ID]
			if !ok || slices.Equal(old.TeamIDs, pipeline.TeamIDs) {
				continue
			}
			raw, err := json.Marshal(pipeline)
			if err != nil {
				continue
			}
			changes = append(changes, metadataRecordChange{field: "releasePipelines", key: pipeline.ID, raw: raw})
		}
	}
	return changes
}

func deleteTeamOwnedIssueRecords(ctx context.Context, tx *sqlTx, workspace, teamID string) (int64, error) {
	if teamID == "" {
		return 0, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,project_id,assignee_id,state_id,state_type,priority,archived FROM issue_records WHERE workspace_key=? AND team_id=?`, workspace, teamID)
	if err != nil {
		return 0, err
	}
	ids := []string{}
	deltas := map[issueStatsKey]int64{}
	for rows.Next() {
		var id, project, assignee, state, stateType string
		var priority, archived int
		if err := rows.Scan(&id, &project, &assignee, &state, &stateType, &priority, &archived); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
		addIssueStats(deltas, &issueStatsDimensions{Team: teamID, Project: project, Assignee: assignee, State: state, Type: stateType, Priority: priority, Archived: archived}, -1)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	if err := writeIssueStats(ctx, tx, workspace, deltas); err != nil {
		return 0, err
	}
	for _, table := range []string{"issue_label_records", "issue_permission_records", "issue_subscriber_records", "issue_actor_records", "issue_attribute_records", "issue_attachment_records", "issue_search_documents"} {
		if err := deleteRowsByIDs(ctx, tx, table, "issue_id", workspace, ids); err != nil {
			return 0, err
		}
	}
	if err := deleteRowsByIDs(ctx, tx, "workspace_content_records", "resource_id", workspace, ids); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM issue_records WHERE workspace_key=? AND team_id=?`, workspace, teamID); err != nil {
		return 0, err
	}
	removed := int64(len(ids))
	if _, err := tx.ExecContext(ctx, `UPDATE issue_collection_counts SET total=total-? WHERE workspace_key=? AND total>=?`, removed, workspace, removed); err != nil {
		return 0, err
	}
	return removed, nil
}

func deleteRowsByIDs(ctx context.Context, tx *sqlTx, table, column, workspace string, ids []string) error {
	const batch = 500
	for start := 0; start < len(ids); start += batch {
		chunk := ids[start:min(start+batch, len(ids))]
		args := make([]any, 0, len(chunk)+1)
		args = append(args, workspace)
		placeholders := make([]string, len(chunk))
		for i, id := range chunk {
			placeholders[i] = "?"
			args = append(args, id)
		}
		query := `DELETE FROM ` + table + ` WHERE workspace_key=? AND ` + column + ` IN (` + strings.Join(placeholders, ",") + `)`
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return err
		}
	}
	return nil
}
