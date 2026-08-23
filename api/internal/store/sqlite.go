package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"
)

type SQLiteStore struct {
	db               *sqlDatabase
	dialect          string
	mu               sync.RWMutex
	workspaces       map[string]domain.Bootstrap
	lastWorkspaceKey string
	viewer           domain.User
	realtimeSink     func(string, domain.RealtimeEvent)
}

func OpenSQLite(path string) (*SQLiteStore, error) {
	return OpenDatabase(DatabaseConfig{Driver: "sqlite", Path: path, MaxOpenConns: 1})
}

func (s *SQLiteStore) Close() error { return s.db.Close() }

func (s *SQLiteStore) SetRealtimeSink(sink func(string, domain.RealtimeEvent)) {
	s.mu.Lock()
	s.realtimeSink = sink
	s.mu.Unlock()
}

func (s *SQLiteStore) migrate(ctx context.Context) error {
	for _, statement := range databaseMigrations(s.dialect) {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			if s.dialect == "mysql" && strings.Contains(strings.ToLower(err.Error()), "duplicate key name") {
				continue
			}
			return fmt.Errorf("database migration: %w", err)
		}
	}
	return nil
}

func (s *SQLiteStore) loadOrSeed(ctx context.Context) error {
	s.workspaces = map[string]domain.Bootstrap{}
	rows, err := s.db.QueryContext(ctx, `SELECT workspace_key,data FROM workspace_states ORDER BY updated_at ASC`)
	if err != nil {
		return err
	}
	changedWorkspaces := map[string]domain.Bootstrap{}
	for rows.Next() {
		var key string
		var raw []byte
		if err := rows.Scan(&key, &raw); err != nil {
			rows.Close()
			return err
		}
		var data domain.Bootstrap
		if err := json.Unmarshal(raw, &data); err != nil {
			rows.Close()
			return err
		}
		changed := ensureCanonicalLabelGroups(&data)
		if ensureCanonicalLabels(&data) {
			changed = true
		}
		if ensureCanonicalSavedViewNames(&data) {
			changed = true
		}
		if ensureCanonicalSavedViewDisplays(&data) {
			changed = true
		}
		if ensureCanonicalSavedViewFilters(&data) {
			changed = true
		}
		if ensureProjectMilestoneFields(&data) {
			changed = true
		}
		if ensureCarMallReleaseManagement(&data) {
			changed = true
		}
		normalize(&data)
		s.workspaces[key] = data
		if changed {
			changedWorkspaces[key] = data
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for key, data := range changedWorkspaces {
		if err := s.persistWorkspace(ctx, key, data, nil); err != nil {
			return err
		}
	}
	if len(s.workspaces) > 0 {
		var viewerRaw []byte
		_ = s.db.QueryRowContext(ctx, `SELECT last_workspace_key,viewer FROM account_state WHERE id = 1`).Scan(&s.lastWorkspaceKey, &viewerRaw)
		_ = json.Unmarshal(viewerRaw, &s.viewer)
		if _, ok := s.workspaces[s.lastWorkspaceKey]; !ok {
			s.lastWorkspaceKey = firstWorkspaceKey(s.workspaces)
		}
		if s.viewer.ID == "" {
			s.viewer = s.workspaces[s.lastWorkspaceKey].Viewer
		}
		return nil
	}

	var raw []byte
	err = s.db.QueryRowContext(ctx, `SELECT data FROM workspace_state WHERE id = 1`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		data := Seed()
		normalize(&data)
		s.workspaces[data.Workspace.URLKey] = data
		s.lastWorkspaceKey = data.Workspace.URLKey
		s.viewer = data.Viewer
		return s.persistWorkspace(ctx, data.Workspace.URLKey, data, nil)
	}
	if err != nil {
		return err
	}
	var data domain.Bootstrap
	if err := json.Unmarshal(raw, &data); err != nil {
		return err
	}
	ensureCanonicalWorkflowStates(&data)
	ensureCanonicalLabelGroups(&data)
	ensureCanonicalLabels(&data)
	ensureCanonicalSavedViewNames(&data)
	ensureCanonicalSavedViewDisplays(&data)
	ensureCanonicalSavedViewFilters(&data)
	ensureProjectMilestoneFields(&data)
	ensureCarMallReleaseManagement(&data)
	ensureCanonicalNotifications(&data)
	ensureCanonicalInitiatives(&data)
	ensureCanonicalCycles(&data)
	normalize(&data)
	s.workspaces[data.Workspace.URLKey] = data
	s.lastWorkspaceKey = data.Workspace.URLKey
	s.viewer = data.Viewer
	return s.persistWorkspace(ctx, data.Workspace.URLKey, data, nil)
}

func ensureProjectMilestoneFields(data *domain.Bootstrap) bool {
	changed := false
	validMilestones := map[string]bool{}
	descriptions := map[string]string{
		"milestone_seal_test":   "完成高关联测试用例并补齐执行结果。",
		"milestone_seal_review": "测试报告、回滚方案和代码证据满足上线门禁。",
	}
	for projectIndex := range data.Projects {
		if data.Projects[projectIndex].ID == "project_aut" && data.Projects[projectIndex].Name == "汽车之家车商城项目2026" {
			data.Projects[projectIndex].Name = "[Flow 对比演示] 汽车之家车商城项目 2026"
			changed = true
		}
		for milestoneIndex := range data.Projects[projectIndex].Milestones {
			milestone := &data.Projects[projectIndex].Milestones[milestoneIndex]
			validMilestones[milestone.ID] = true
			if milestone.Description == "" && descriptions[milestone.ID] != "" {
				milestone.Description = descriptions[milestone.ID]
				changed = true
			}
		}
	}
	assignments := map[string]string{
		"issue_49219": "milestone_seal_test", "issue_49216": "milestone_seal_test", "issue_49215": "milestone_seal_test", "issue_test_plan": "milestone_seal_test", "issue_test_report": "milestone_seal_test",
		"issue_release_review": "milestone_seal_review", "issue_audit_gate": "milestone_seal_review",
	}
	for issueIndex := range data.Issues {
		milestoneID := assignments[data.Issues[issueIndex].ID]
		if validMilestones[milestoneID] && data.Issues[issueIndex].ProjectMilestoneID == nil {
			data.Issues[issueIndex].ProjectMilestoneID = stringPointer(milestoneID)
			changed = true
		}
	}
	return changed
}

func ensureCarMallReleaseManagement(data *domain.Bootstrap) bool {
	projectIndex := slices.IndexFunc(data.Projects, func(project domain.Project) bool { return project.ID == "project_aut" })
	if projectIndex < 0 {
		return false
	}

	const pipelineID = "release_pipeline_car_mall"
	versionMilestones := map[string]bool{"milestone_car_phase1": true, "milestone_car_316": true}
	issueIDsByMilestone := map[string][]string{}
	changed := false
	for issueIndex := range data.Issues {
		milestoneID := ""
		if data.Issues[issueIndex].ProjectMilestoneID != nil {
			milestoneID = *data.Issues[issueIndex].ProjectMilestoneID
		}
		if !versionMilestones[milestoneID] {
			continue
		}
		issueIDsByMilestone[milestoneID] = append(issueIDsByMilestone[milestoneID], data.Issues[issueIndex].ID)
		data.Issues[issueIndex].ProjectMilestoneID = nil
		changed = true
	}

	project := &data.Projects[projectIndex]
	milestones := project.Milestones[:0]
	for _, milestone := range project.Milestones {
		if versionMilestones[milestone.ID] {
			changed = true
			continue
		}
		milestones = append(milestones, milestone)
	}
	project.Milestones = milestones

	now := time.Now().UTC()
	pipelineIndex := slices.IndexFunc(data.ReleasePipelines, func(pipeline domain.ReleasePipeline) bool { return pipeline.ID == pipelineID })
	if pipelineIndex < 0 {
		data.ReleasePipelines = append(data.ReleasePipelines, domain.ReleasePipeline{
			ID: pipelineID, Name: "车商城交付发布管线", TeamIDs: slices.Clone(project.TeamIDs), Type: "scheduled", Production: true,
			Stages: []string{"待规划", "进行中", "已发布", "已取消"}, StageStatuses: map[string]string{"待规划": "planned", "进行中": "inProgress", "已发布": "released", "已取消": "canceled"},
			Position: float64(len(data.ReleasePipelines)), PathFilters: []string{}, CreatedAt: now, UpdatedAt: now,
		})
		changed = true
	} else {
		pipeline := &data.ReleasePipelines[pipelineIndex]
		if pipeline.Name != "车商城交付发布管线" || !slices.Equal(pipeline.TeamIDs, project.TeamIDs) {
			pipeline.Name = "车商城交付发布管线"
			pipeline.TeamIDs = slices.Clone(project.TeamIDs)
			pipeline.UpdatedAt = now
			changed = true
		}
	}

	creator := data.Viewer
	if project.Lead != nil {
		creator = *project.Lead
	}
	phaseOneTarget, phase316Target := "2026-07-10", "2026-08-01"
	phaseOneReleasedAt := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	phase316ReleasedAt := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	changed = ensureCarMallRelease(data, domain.Release{
		ID: "release_car_phase1", Name: "车商城一期迭代", Version: "一期", Description: "由车商城一期版本里程碑迁移，覆盖订单流程交付与缺陷收敛。",
		Status: "released", PipelineID: pipelineID, Stage: "已发布", Position: 0, TargetDate: &phaseOneTarget, ProjectIDs: []string{project.ID},
		IssueIDs: issueIDsByMilestone["milestone_car_phase1"], SubscriberIDs: slices.Clone(project.MemberIDs), Creator: creator, ReleasedAt: &phaseOneReleasedAt, CreatedAt: phaseOneReleasedAt, UpdatedAt: now,
	}) || changed
	changed = ensureCarMallRelease(data, domain.Release{
		ID: "release_car_316", Name: "车商城 316 迭代", Version: "316", Description: "由车商城 316 版本里程碑迁移，覆盖 316 版本范围和估算偏差复盘。",
		Status: "released", PipelineID: pipelineID, Stage: "已发布", Position: 1, TargetDate: &phase316Target, ProjectIDs: []string{project.ID},
		IssueIDs: issueIDsByMilestone["milestone_car_316"], SubscriberIDs: slices.Clone(project.MemberIDs), Creator: creator, ReleasedAt: &phase316ReleasedAt, CreatedAt: phase316ReleasedAt, UpdatedAt: now,
	}) || changed

	if releaseIndex := slices.IndexFunc(data.Releases, func(release domain.Release) bool { return release.ID == "release_car_phase2" }); releaseIndex >= 0 {
		release := &data.Releases[releaseIndex]
		if release.PipelineID != pipelineID || release.Stage != "待规划" || release.Position != 2 {
			release.PipelineID, release.Stage, release.Position, release.UpdatedAt = pipelineID, "待规划", 2, now
			changed = true
		}
	}
	return changed
}

func ensureCarMallRelease(data *domain.Bootstrap, canonical domain.Release) bool {
	index := slices.IndexFunc(data.Releases, func(release domain.Release) bool { return release.ID == canonical.ID })
	if index < 0 {
		canonical.IssueIDs = uniqueStrings(canonical.IssueIDs)
		data.Releases = append(data.Releases, canonical)
		return true
	}
	release := &data.Releases[index]
	issueIDs := uniqueStrings(append(append([]string{}, release.IssueIDs...), canonical.IssueIDs...))
	if release.PipelineID == canonical.PipelineID && release.Stage == canonical.Stage && release.Position == canonical.Position && slices.Equal(release.ProjectIDs, canonical.ProjectIDs) && slices.Equal(release.IssueIDs, issueIDs) {
		return false
	}
	release.PipelineID, release.Stage, release.Position = canonical.PipelineID, canonical.Stage, canonical.Position
	release.ProjectIDs, release.IssueIDs, release.UpdatedAt = slices.Clone(canonical.ProjectIDs), issueIDs, time.Now().UTC()
	return true
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func ensureCanonicalCycles(data *domain.Bootstrap) bool {
	if len(data.Cycles) > 0 || len(data.Teams) == 0 || len(data.Issues) == 0 {
		return false
	}
	now := time.Now().UTC()
	start := cycleWeekStart(now)
	teamID := data.Teams[0].ID
	data.Cycles = []domain.Cycle{
		{ID: "cycle_47", Number: 47, Name: "Cycle 47", TeamID: teamID, StartsAt: start.AddDate(0, 0, -14), EndsAt: start.AddDate(0, 0, -1), Status: "completed", Capacity: 4, CreatedAt: start.AddDate(0, 0, -42), UpdatedAt: now},
		{ID: "cycle_48", Number: 48, Name: "Cycle 48", TeamID: teamID, StartsAt: start, EndsAt: start.AddDate(0, 0, 13), Status: "current", Capacity: 4, Favorite: true, CreatedAt: start.AddDate(0, 0, -28), UpdatedAt: now},
		{ID: "cycle_49", Number: 49, Name: "Cycle 49", TeamID: teamID, StartsAt: start.AddDate(0, 0, 14), EndsAt: start.AddDate(0, 0, 27), Status: "upcoming", Capacity: 4, CreatedAt: start.AddDate(0, 0, -14), UpdatedAt: now},
		{ID: "cycle_50", Number: 50, Name: "Cycle 50", TeamID: teamID, StartsAt: start.AddDate(0, 0, 28), EndsAt: start.AddDate(0, 0, 41), Status: "upcoming", Capacity: 4, CreatedAt: now, UpdatedAt: now},
	}
	data.CycleSettings = map[string]domain.CycleSettings{teamID: {Enabled: true, DurationWeeks: 2, StartsOn: 1, UpcomingCount: 2}}
	assignments := []string{"cycle_48", "cycle_48", "cycle_48", "cycle_47", "cycle_49"}
	for index := range data.Issues {
		if index >= len(assignments) {
			break
		}
		if data.Issues[index].CycleID == nil {
			data.Issues[index].CycleID = stringPointer(assignments[index])
		}
	}
	return true
}

func ensureCanonicalInitiatives(data *domain.Bootstrap) bool {
	if len(data.Initiatives) > 0 || len(data.Projects) == 0 {
		return false
	}
	now := time.Now().UTC()
	target := now.AddDate(0, 2, 0).Format("2006-01-02")
	project := &data.Projects[0]
	initiative := domain.Initiative{
		ID: "initiative_operational_excellence", Name: "Operational excellence", SlugID: "operational-excellence",
		Summary: "Make core workflows dependable at production scale", Description: "Coordinate the active reliability projects and keep their outcomes visible across the workspace.",
		Icon: "Initiative", Color: "#d15f64", Status: "active", Priority: 2, PriorityLabel: "High", Health: "onTrack",
		Owner: &data.Viewer, ProjectIDs: []string{project.ID}, LabelIDs: []string{}, Resources: []domain.InitiativeResource{}, Comments: []domain.Comment{},
		TargetDate: &target, CreatedAt: now.AddDate(0, -2, 0), UpdatedAt: now,
	}
	data.Initiatives = []domain.Initiative{initiative}
	project.Initiatives = append(project.Initiatives, initiative.ID)
	if data.InitiativeUpdates == nil {
		data.InitiativeUpdates = map[string][]domain.InitiativeUpdate{}
	}
	data.InitiativeUpdates[initiative.ID] = []domain.InitiativeUpdate{{
		ID: "initiative_update_operational_excellence_1", InitiativeID: initiative.ID,
		Body: "The reliability program is moving forward with the current project scope and target intact.", Health: "onTrack",
		CreatedAt: now.AddDate(0, 0, -3), User: data.Viewer, Comments: []domain.Comment{}, Reactions: map[string][]string{},
	}}
	return true
}

func ensureCanonicalWorkflowStates(data *domain.Bootstrap) bool {
	existing := make(map[string]struct{}, len(data.States))
	for _, state := range data.States {
		existing[state.ID] = struct{}{}
	}
	changed := false
	for _, state := range canonicalWorkflowStates() {
		if _, ok := existing[state.ID]; ok {
			continue
		}
		data.States = append(data.States, state)
		changed = true
	}
	return changed
}

func ensureCanonicalLabels(data *domain.Bootstrap) bool {
	hadObsolete := slices.ContainsFunc(data.Labels, func(label domain.IssueLabel) bool { return obsoleteDeliveryLabelIDs[label.ID] }) ||
		slices.ContainsFunc(data.Issues, func(issue domain.Issue) bool {
			return slices.ContainsFunc(issue.Labels, func(label domain.IssueLabel) bool { return obsoleteDeliveryLabelIDs[label.ID] })
		})
	if hadObsolete {
		applyDeliveryLabelTaxonomy(data.Issues, canonicalLabels())
		data.Labels = slices.DeleteFunc(data.Labels, func(label domain.IssueLabel) bool { return obsoleteDeliveryLabelIDs[label.ID] })
		migrateDeliverySavedViews(data)
	}
	existing := make(map[string]domain.IssueLabel, len(data.Labels))
	for _, label := range data.Labels {
		existing[label.ID] = label
	}
	changed := hadObsolete
	canonical := canonicalLabels()
	for _, label := range canonical {
		current, ok := existing[label.ID]
		if !ok {
			data.Labels = append(data.Labels, label)
			existing[label.ID] = label
			changed = true
			continue
		}
		if label.ID == "label_type_requirement" && current.Name == "原始需求" {
			for i := range data.Labels {
				if data.Labels[i].ID == label.ID {
					data.Labels[i].Name = label.Name
					cascadeBootstrapLabel(data, data.Labels[i])
				}
			}
			renameSavedViewLabelReferences(data, label.ID, current.Name, label.Name)
			current.Name = label.Name
			changed = true
		}
		if current.Description == "" || current.IssueCount == 0 || current.Scope == "" || current.GroupID == "" || current.ResourceType == "" || current.CreatedAt.IsZero() || current.LastAppliedAt == nil {
			for i := range data.Labels {
				if data.Labels[i].ID == label.ID {
					data.Labels[i].Description = label.Description
					data.Labels[i].IssueCount = label.IssueCount
					data.Labels[i].Scope = label.Scope
					if data.Labels[i].GroupID == "" {
						data.Labels[i].GroupID = label.GroupID
					}
					if data.Labels[i].ResourceType == "" {
						data.Labels[i].ResourceType = canonicalLabelResourceType(label)
					}
					if data.Labels[i].CreatedAt.IsZero() {
						data.Labels[i].CreatedAt = label.CreatedAt
					}
					if data.Labels[i].LastAppliedAt == nil && data.Labels[i].IssueCount > 0 {
						data.Labels[i].LastAppliedAt = label.LastAppliedAt
					}
					cascadeBootstrapLabel(data, data.Labels[i])
				}
			}
			changed = true
		}
	}
	return changed
}

func renameSavedViewLabelReferences(data *domain.Bootstrap, labelID, oldName, newName string) {
	for viewIndex := range data.SavedViews {
		var filters []map[string]any
		if len(data.SavedViews[viewIndex].Filters) == 0 || json.Unmarshal(data.SavedViews[viewIndex].Filters, &filters) != nil {
			continue
		}
		changed := false
		for _, filter := range filters {
			if field, _ := filter["field"].(string); field != "labels" {
				continue
			}
			if value, _ := filter["value"].(string); value == labelID {
				if valueLabel, _ := filter["valueLabel"].(string); valueLabel == oldName {
					filter["valueLabel"] = newName
					changed = true
				}
			}
			values, _ := filter["values"].([]any)
			for _, rawValue := range values {
				value, _ := rawValue.(map[string]any)
				if id, _ := value["value"].(string); id != labelID {
					continue
				}
				if valueLabel, _ := value["valueLabel"].(string); valueLabel == oldName {
					value["valueLabel"] = newName
					changed = true
				}
			}
		}
		if !changed {
			continue
		}
		encoded, err := json.Marshal(filters)
		if err == nil {
			data.SavedViews[viewIndex].Filters = encoded
		}
	}
}

func migrateDeliverySavedViews(data *domain.Bootstrap) {
	display := json.RawMessage(`{"layout":"list","grouping":"status","groupOrder":"asc","subGrouping":"none","ordering":"priority","completedWindow":"all","orderCompletedByRecency":false,"showSubIssues":true,"showEmptyGroups":false,"nestedSubIssues":false,"properties":["id","status","priority","assignee","labels","project","created"]}`)
	filter := func(ids ...string) json.RawMessage {
		labels := make(map[string]domain.IssueLabel, len(data.Labels)+len(canonicalLabels()))
		for _, label := range append(append([]domain.IssueLabel{}, data.Labels...), canonicalLabels()...) {
			labels[label.ID] = label
		}
		values := make([]map[string]string, 0, len(ids))
		for _, id := range ids {
			label := labels[id]
			values = append(values, map[string]string{"value": id, "valueLabel": label.Name, "color": label.Color})
		}
		encoded, _ := json.Marshal([]map[string]any{{
			"id": "labels-delivery-taxonomy", "field": "labels", "fieldLabel": "Labels", "operator": "is",
			"value": ids[0], "valueLabel": labels[ids[0]].Name, "color": labels[ids[0]].Color, "values": values,
		}})
		return encoded
	}
	data.SavedViews = slices.DeleteFunc(data.SavedViews, func(view domain.SavedView) bool {
		return view.ID == "view_release_gate" || view.ID == "view_audit"
	})
	for index := range data.SavedViews {
		switch data.SavedViews[index].ID {
		case "view_strategy", "view_business", "view_product":
			data.SavedViews[index].Filters = filter("label_type_requirement")
			data.SavedViews[index].Display = slices.Clone(display)
		case "view_development", "view_testing", "view_operations":
			data.SavedViews[index].Filters = filter("label_type_development")
			data.SavedViews[index].Display = slices.Clone(display)
		}
	}
}

func ensureCanonicalSavedViewNames(data *domain.Bootstrap) bool {
	changed := false
	for index := range data.SavedViews {
		if data.SavedViews[index].Name == "原始需求" && savedViewFiltersByLabel(data.SavedViews[index], "label_type_requirement") {
			data.SavedViews[index].Name = "IT需求池"
			changed = true
		}
	}
	return changed
}

func savedViewFiltersByLabel(view domain.SavedView, labelID string) bool {
	var filters []map[string]any
	if len(view.Filters) == 0 || json.Unmarshal(view.Filters, &filters) != nil {
		return false
	}
	for _, filter := range filters {
		if field, _ := filter["field"].(string); field != "labels" {
			continue
		}
		if value, _ := filter["value"].(string); value == labelID {
			return true
		}
		values, _ := filter["values"].([]any)
		for _, rawValue := range values {
			value, _ := rawValue.(map[string]any)
			if id, _ := value["value"].(string); id == labelID {
				return true
			}
		}
	}
	return false
}

func ensureCanonicalSavedViewDisplays(data *domain.Bootstrap) bool {
	display := json.RawMessage(`{"layout":"list","grouping":"status","groupOrder":"asc","subGrouping":"none","ordering":"priority","completedWindow":"all","orderCompletedByRecency":false,"showSubIssues":true,"showEmptyGroups":false,"nestedSubIssues":false,"properties":["id","status","priority","assignee","labels","project","created"]}`)
	builtIn := map[string]bool{
		"view_strategy": true, "view_business": true, "view_product": true,
		"view_development": true, "view_testing": true, "view_operations": true,
	}
	changed := false
	for index := range data.SavedViews {
		if !builtIn[data.SavedViews[index].ID] {
			continue
		}
		var current map[string]any
		if json.Unmarshal(data.SavedViews[index].Display, &current) != nil {
			continue
		}
		if _, legacy := current["direction"]; !legacy {
			continue
		}
		data.SavedViews[index].Display = slices.Clone(display)
		changed = true
	}
	return changed
}

func ensureCanonicalSavedViewFilters(data *domain.Bootstrap) bool {
	changed := false
	for viewIndex := range data.SavedViews {
		var filters []map[string]any
		if len(data.SavedViews[viewIndex].Filters) == 0 || json.Unmarshal(data.SavedViews[viewIndex].Filters, &filters) != nil {
			continue
		}
		viewChanged := false
		for _, filter := range filters {
			field, _ := filter["field"].(string)
			rawValues, _ := filter["values"].([]any)
			values := make([]map[string]string, 0, len(rawValues))
			for _, rawValue := range rawValues {
				switch value := rawValue.(type) {
				case string:
					values = append(values, savedViewFilterValue(data, field, value))
					viewChanged = true
				case map[string]any:
					id, _ := value["value"].(string)
					if id == "" {
						continue
					}
					canonical := savedViewFilterValue(data, field, id)
					for _, key := range []string{"valueLabel", "color"} {
						if current, ok := value[key].(string); ok && current != "" {
							canonical[key] = current
						} else if canonical[key] != "" {
							viewChanged = true
						}
					}
					values = append(values, canonical)
				}
			}
			if len(values) == 0 {
				if value, ok := filter["value"].(string); ok && value != "" {
					values = append(values, savedViewFilterValue(data, field, value))
					viewChanged = true
				} else {
					continue
				}
			}
			filter["values"] = values
			first := values[0]
			for key, value := range map[string]string{
				"id":         firstNonEmptyString(filter["id"], field+"-"+first["value"]),
				"fieldLabel": firstNonEmptyString(filter["fieldLabel"], savedViewFilterFieldLabel(field)),
				"value":      firstNonEmptyString(filter["value"], first["value"]),
				"valueLabel": firstNonEmptyString(filter["valueLabel"], first["valueLabel"]),
			} {
				if current, _ := filter[key].(string); current != value {
					filter[key] = value
					viewChanged = true
				}
			}
			if _, ok := filter["color"].(string); !ok && first["color"] != "" {
				filter["color"] = first["color"]
				viewChanged = true
			}
		}
		if !viewChanged {
			continue
		}
		encoded, err := json.Marshal(filters)
		if err != nil {
			continue
		}
		data.SavedViews[viewIndex].Filters = encoded
		changed = true
	}
	return changed
}

func savedViewFilterValue(data *domain.Bootstrap, field, id string) map[string]string {
	value := map[string]string{"value": id, "valueLabel": id}
	switch field {
	case "labels":
		if labelIndex := slices.IndexFunc(data.Labels, func(label domain.IssueLabel) bool { return label.ID == id }); labelIndex >= 0 {
			value["valueLabel"] = data.Labels[labelIndex].Name
			value["color"] = data.Labels[labelIndex].Color
		}
	case "project":
		if projectIndex := slices.IndexFunc(data.Projects, func(project domain.Project) bool { return project.ID == id }); projectIndex >= 0 {
			value["valueLabel"] = data.Projects[projectIndex].Name
			value["color"] = data.Projects[projectIndex].Color
		}
	case "status":
		if stateIndex := slices.IndexFunc(data.States, func(state domain.WorkflowState) bool { return state.ID == id }); stateIndex >= 0 {
			value["valueLabel"] = data.States[stateIndex].Name
			value["color"] = data.States[stateIndex].Color
		}
	case "assignee", "creator", "subscribers":
		if userIndex := slices.IndexFunc(data.Users, func(user domain.User) bool { return user.ID == id }); userIndex >= 0 {
			value["valueLabel"] = data.Users[userIndex].DisplayName
		}
	case "priority":
		priorities := map[string]string{"0": "No priority", "1": "Urgent", "2": "High", "3": "Medium", "4": "Low"}
		if label := priorities[id]; label != "" {
			value["valueLabel"] = label
		}
	}
	return value
}

func savedViewFilterFieldLabel(field string) string {
	labels := map[string]string{"status": "Status", "priority": "Priority", "assignee": "Assignee", "creator": "Creator", "labels": "Labels", "project": "Project", "dates": "Dates", "subscribers": "Subscribers", "relations": "Relations", "links": "Links"}
	if label := labels[field]; label != "" {
		return label
	}
	return field
}

func firstNonEmptyString(value any, fallback string) string {
	if current, ok := value.(string); ok && current != "" {
		return current
	}
	return fallback
}

func canonicalLabelResourceType(label domain.IssueLabel) string {
	if label.ResourceType != "" {
		return label.ResourceType
	}
	return "issue"
}

func cascadeBootstrapLabel(data *domain.Bootstrap, label domain.IssueLabel) {
	for issueIndex := range data.Issues {
		for labelIndex := range data.Issues[issueIndex].Labels {
			if data.Issues[issueIndex].Labels[labelIndex].ID == label.ID {
				data.Issues[issueIndex].Labels[labelIndex] = label
			}
		}
	}
}

func ensureCanonicalLabelGroups(data *domain.Bootstrap) bool {
	legacyGroups := map[string]bool{
		"label_group_requirement": true, "label_group_delivery": true, "label_group_quality": true, "label_group_release": true, "label_group_value": true,
		"label_group_original_requirement": true, "label_group_development_task": true, "label_group_version": true, "label_group_defect": true,
	}
	before := len(data.LabelGroups)
	data.LabelGroups = slices.DeleteFunc(data.LabelGroups, func(group domain.LabelGroup) bool { return legacyGroups[group.ID] })
	existing := make(map[string]int, len(data.LabelGroups))
	for index, group := range data.LabelGroups {
		existing[group.ID] = index
	}
	changed := len(data.LabelGroups) != before
	for _, group := range canonicalLabelGroups() {
		index, ok := existing[group.ID]
		if !ok {
			data.LabelGroups = append(data.LabelGroups, group)
			changed = true
			continue
		}
		if data.LabelGroups[index].Scope == "" || data.LabelGroups[index].ResourceType == "" || data.LabelGroups[index].Description == "" {
			if data.LabelGroups[index].Scope == "" {
				data.LabelGroups[index].Scope = group.Scope
			}
			if data.LabelGroups[index].ResourceType == "" {
				data.LabelGroups[index].ResourceType = group.ResourceType
			}
			if data.LabelGroups[index].Description == "" {
				data.LabelGroups[index].Description = group.Description
			}
			changed = true
		}
	}
	return changed
}

// ensureCanonicalNotifications provides the one-time Inbox projection for
// persisted workspaces created before Notification existed. IDs are derived
// from a real source record, so subsequent application starts are idempotent
// and never overwrite a user's notification lifecycle state.
func ensureCanonicalNotifications(data *domain.Bootstrap) bool {
	canonical := projectNotifications(data)
	existing := make(map[string]int, len(data.Notifications))
	for i, notification := range data.Notifications {
		existing[notification.ID] = i
	}

	changed := false
	for _, expected := range canonical {
		index, ok := existing[expected.ID]
		if !ok {
			data.Notifications = append(data.Notifications, expected)
			changed = true
			continue
		}
		if reconcileNotification(&data.Notifications[index], expected) {
			changed = true
		}
	}
	return changed
}

func reconcileNotification(current *domain.Notification, expected domain.Notification) bool {
	changed := false
	setString := func(target *string, value string) {
		if *target == "" && value != "" {
			*target = value
			changed = true
		}
	}
	setString(&current.RecipientID, expected.RecipientID)
	setString(&current.Type, expected.Type)
	setString(&current.SourceType, expected.SourceType)
	setString(&current.SourceID, expected.SourceID)
	setString(&current.IssueID, expected.IssueID)
	setString(&current.CommentID, expected.CommentID)
	setString(&current.ActivityID, expected.ActivityID)
	if current.Actor.ID == "" && expected.Actor.ID != "" {
		current.Actor = expected.Actor
		changed = true
	}
	if current.CreatedAt.IsZero() {
		current.CreatedAt = expected.CreatedAt
		changed = true
	}
	if current.UpdatedAt.IsZero() {
		current.UpdatedAt = expected.UpdatedAt
		changed = true
	}
	return changed
}

func projectNotifications(data *domain.Bootstrap) []domain.Notification {
	notifications := make([]domain.Notification, 0)
	for _, issue := range data.Issues {
		if issue.ArchivedAt != nil {
			continue
		}
		comments := data.Comments[issue.ID]
		for _, comment := range comments {
			notifications = append(notifications, domain.Notification{
				ID:          "notification_comment_" + comment.ID,
				RecipientID: data.Viewer.ID,
				Type:        "comment",
				SourceType:  "comment",
				SourceID:    comment.ID,
				IssueID:     issue.ID,
				CommentID:   comment.ID,
				Actor:       comment.User,
				CreatedAt:   comment.CreatedAt,
				UpdatedAt:   comment.CreatedAt,
			})
		}
		for _, activity := range data.Activities[issue.ID] {
			if activity.Type == "comment.created" && hasComment(comments, activity.Metadata["commentId"]) {
				continue
			}
			notifications = append(notifications, domain.Notification{
				ID:          "notification_activity_" + activity.ID,
				RecipientID: data.Viewer.ID,
				Type:        activityNotificationType(activity),
				SourceType:  "activity",
				SourceID:    activity.ID,
				IssueID:     issue.ID,
				ActivityID:  activity.ID,
				Actor:       activity.Actor,
				CreatedAt:   activity.CreatedAt,
				UpdatedAt:   activity.CreatedAt,
			})
		}
	}
	return notifications
}

func hasComment(comments []domain.Comment, id string) bool {
	for _, comment := range comments {
		if comment.ID == id {
			return true
		}
	}
	return false
}

func activityNotificationType(activity domain.ActivityEvent) string {
	if activity.Type == "comment.created" {
		return "comment"
	}
	if activity.Type == "issue.updated" && activity.Metadata["assignee"] != "" {
		return "assignment"
	}
	return "activity"
}

func legacyReleaseStageStatus(stage string) string {
	switch strings.ToLower(strings.TrimSpace(stage)) {
	case "in progress":
		return "inProgress"
	case "released":
		return "released"
	case "canceled", "cancelled":
		return "canceled"
	default:
		return "planned"
	}
}

func legacyReleaseSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.Trim(strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, value), "-")
}

func normalize(data *domain.Bootstrap) {
	for projectIndex := range data.Projects {
		for resourceIndex := range data.Projects[projectIndex].Resources {
			if data.Projects[projectIndex].Resources[resourceIndex].PinnedTeamIDs == nil {
				data.Projects[projectIndex].Resources[resourceIndex].PinnedTeamIDs = []string{}
			}
		}
	}
	for index := range data.States {
		if data.States[index].TeamID == "" && data.States[index].ID == "state_backlog" {
			data.States[index].Default = true
		}
		if data.States[index].TeamID == "" && data.States[index].ID == "state_duplicate" {
			data.States[index].Reserved = true
		}
	}
	if data.Settings == nil {
		data.Settings = map[string]any{}
	}
	if data.UserSettings == nil {
		data.UserSettings = map[string]domain.UserSettings{}
	}
	for _, user := range data.Users {
		if _, ok := data.UserSettings[user.ID]; !ok {
			data.UserSettings[user.ID] = defaultUserSettings(user.ID)
		}
	}
	for userID, settings := range data.UserSettings {
		if settings.PersonalSettingsVersion < 1 {
			settings.PersonalSettingsVersion = 1
			settings.CodeReviewsEnabled = true
			settings.MergeStrategy = "Squash and merge"
			settings.CodeTheme = "Flow Light"
			settings.CodeFont = "12px, Regular, Default"
			settings.ReviewCommentsFilter = "Exclude Bots"
			settings.ReviewRequests = true
			settings.GithubTeamReviewRequests = true
			settings.ChecksMergeQueue = true
			settings.GitAttachmentFormat = "Title"
			settings.GitBranchMoveStarted = true
			settings.CodingToolMoveStarted = true
			settings.ChangelogUpdates = true
			settings.InviteAcceptedUpdates = true
			settings.PrivacyUpdates = true
			data.UserSettings[userID] = settings
		}
	}
	if data.WorkspaceSettings.SessionDurationDays == 0 {
		data.WorkspaceSettings = defaultWorkspaceSettings(data)
	}
	if data.WorkspaceSettings.AllowedDomains == nil {
		data.WorkspaceSettings.AllowedDomains = []string{}
	}
	if data.WorkspaceSettings.AICreditReloadThresholdCents == 0 {
		data.WorkspaceSettings.AICreditReloadThresholdCents = 500
	}
	if data.WorkspaceSettings.AICreditReloadAmountCents == 0 {
		data.WorkspaceSettings.AICreditReloadAmountCents = 2000
	}
	if data.LabelGroups == nil {
		data.LabelGroups = []domain.LabelGroup{}
	}
	if data.APIKeys == nil {
		data.APIKeys = []domain.APIKey{}
	}
	if data.OAuthApplications == nil {
		data.OAuthApplications = []domain.OAuthApplication{}
	}
	if data.OAuthAuthorizations == nil {
		data.OAuthAuthorizations = []domain.OAuthAuthorization{}
	}
	if data.IntegrationConnections == nil {
		data.IntegrationConnections = []domain.IntegrationConnection{}
	}
	if data.Reviews == nil {
		data.Reviews = defaultCodeReviews(data)
	}
	for index := range data.Initiatives {
		if data.Initiatives[index].ParentInitiativeIDs == nil {
			data.Initiatives[index].ParentInitiativeIDs = []string{}
		}
	}
	for index := range data.Labels {
		if data.Labels[index].ResourceType == "" {
			data.Labels[index].ResourceType = "issue"
		}
	}
	for index := range data.IssueTemplates {
		if data.IssueTemplates[index].Scope == "" {
			data.IssueTemplates[index].Scope = "team"
		}
		if data.IssueTemplates[index].TemplateType == "" {
			data.IssueTemplates[index].TemplateType = "standard"
		}
	}
	if data.Members == nil {
		data.Members = []domain.WorkspaceMember{}
	}
	if data.TeamMembers == nil {
		data.TeamMembers = []domain.TeamMember{}
	}
	if data.Invitations == nil {
		data.Invitations = []domain.Invitation{}
	}
	if data.Cycles == nil {
		data.Cycles = []domain.Cycle{}
	}
	for index := range data.Cycles {
		if data.Cycles[index].Resources == nil {
			data.Cycles[index].Resources = []domain.CycleResource{}
		}
		if data.Cycles[index].Insight == nil {
			data.Cycles[index].Insight = map[string]string{"measure": "Issue count", "slice": "Status", "segment": "Priority"}
		}
	}
	if data.CycleSettings == nil {
		data.CycleSettings = map[string]domain.CycleSettings{}
	}
	if data.TeamSettings == nil {
		data.TeamSettings = map[string]domain.TeamSettings{}
	}
	if data.IssueTemplates == nil {
		data.IssueTemplates = []domain.IssueTemplate{}
	}
	if data.ProjectTemplates == nil {
		data.ProjectTemplates = []domain.ProjectTemplate{}
	}
	if data.DocumentTemplates == nil {
		data.DocumentTemplates = []domain.DocumentTemplate{}
	}
	if data.Documents == nil {
		data.Documents = []domain.Document{}
	}
	if data.CustomerRequests == nil {
		data.CustomerRequests = []domain.CustomerRequest{}
	}
	if data.Releases == nil {
		data.Releases = []domain.Release{}
	}
	for index := range data.Releases {
		if data.Releases[index].SlugID == "" {
			base := legacyReleaseSlug(data.Releases[index].Name)
			if base == "" {
				base = "release"
			}
			data.Releases[index].SlugID = fmt.Sprintf("%s-%x", base, data.Releases[index].CreatedAt.UnixNano()&0xffffffffffff)
		}
		if data.Releases[index].Resources == nil {
			data.Releases[index].Resources = []domain.ReleaseResource{}
		}
	}
	if data.ReleasePipelines == nil {
		data.ReleasePipelines = []domain.ReleasePipeline{}
	}
	for index := range data.ReleasePipelines {
		if data.ReleasePipelines[index].SlugID == "" {
			base := legacyReleaseSlug(data.ReleasePipelines[index].Name)
			if base == "" {
				base = fmt.Sprintf("pipeline-%d", index+1)
			}
			candidate := base
			for suffix := 2; slices.ContainsFunc(data.ReleasePipelines[:index], func(item domain.ReleasePipeline) bool { return item.SlugID == candidate }); suffix++ {
				candidate = fmt.Sprintf("%s-%d", base, suffix)
			}
			data.ReleasePipelines[index].SlugID = candidate
		}
		if data.ReleasePipelines[index].StageStatuses == nil {
			data.ReleasePipelines[index].StageStatuses = map[string]string{}
		}
		for _, stage := range data.ReleasePipelines[index].Stages {
			if _, ok := data.ReleasePipelines[index].StageStatuses[stage]; !ok {
				data.ReleasePipelines[index].StageStatuses[stage] = legacyReleaseStageStatus(stage)
			}
		}
	}
	if data.Asks == nil {
		data.Asks = []domain.Ask{}
	}
	if data.SLARules == nil {
		data.SLARules = []domain.SLARule{}
	}
	if data.IssueSLAs == nil {
		data.IssueSLAs = []domain.IssueSLA{}
	}
	if data.SLAEvents == nil {
		data.SLAEvents = []domain.SLAEvent{}
	}
	if data.Drafts == nil {
		data.Drafts = []domain.Draft{}
	}
	if data.Favorites == nil {
		data.Favorites = []domain.Favorite{}
	}
	if data.Subscriptions == nil {
		data.Subscriptions = []domain.Subscription{}
	}
	if data.AuditLog == nil {
		data.AuditLog = []domain.AuditLogEntry{}
	}
	if data.Trash == nil {
		data.Trash = []domain.TrashEntry{}
	}
	if data.ImportJobs == nil {
		data.ImportJobs = []domain.ImportJob{}
	}
	if data.ExportJobs == nil {
		data.ExportJobs = []domain.ExportJob{}
	}
	if data.Webhooks == nil {
		data.Webhooks = []domain.Webhook{}
	}
	for _, team := range data.Teams {
		settings := data.TeamSettings[team.ID]
		if settings.TeamID == "" {
			settings = domain.TeamSettings{TeamID: team.ID, Timezone: "Etc/UTC", EstimateType: "notUsed", DefaultStateID: defaultStateID(data, team.ID)}
		}
		if settings.Access == "" {
			settings.Access = "public"
		}
		if settings.MembershipRestriction == "" {
			settings.MembershipRestriction = "open"
		}
		if settings.SettingsPermission == "" {
			settings.SettingsPermission = "allMembers"
		}
		if settings.LabelPermission == "" {
			settings.LabelPermission = "allMembers"
		}
		if settings.TemplatePermission == "" {
			settings.TemplatePermission = "allMembers"
		}
		if settings.AgentSkillPermission == "" {
			settings.AgentSkillPermission = "allMembers"
		}
		if settings.LoopPermission == "" {
			settings.LoopPermission = "allMembers"
		}
		if settings.MemberPermission == "" {
			settings.MemberPermission = "allMembers"
		}
		if settings.SlackNotifications == nil {
			settings.SlackNotifications = map[string]bool{}
		}
		if settings.PRAutomations == nil {
			settings.PRAutomations = map[string]string{}
		}
		if settings.StaleMonths == 0 {
			settings.StaleMonths = 6
		}
		if settings.AutoArchiveMonths == 0 {
			settings.AutoArchiveMonths = 6
		}
		if settings.ProgressOrder == "" {
			settings.ProgressOrder = "first"
		}
		if settings.TriageAction == "" {
			settings.TriageAction = "none"
		}
		if settings.ReleaseAutomations == nil {
			settings.ReleaseAutomations = []domain.TeamAutomationRule{}
		}
		if settings.TriageRules == nil {
			settings.TriageRules = []domain.TeamAutomationRule{}
		}
		if settings.AgentSkills == nil {
			settings.AgentSkills = []domain.TeamAgentSkill{}
		}
		data.TeamSettings[team.ID] = settings
		cycle := data.CycleSettings[team.ID]
		if cycle.DurationWeeks > 0 && cycle.Capacity == 0 {
			cycle.Capacity = 4
			cycle.AutoCreate = true
			cycle.AutoMigrate = true
			data.CycleSettings[team.ID] = cycle
		}
	}
	if len(data.ProjectStatuses) == 0 {
		data.ProjectStatuses = canonicalProjectStatuses()
	}
	for index := range data.ProjectStatuses {
		switch data.ProjectStatuses[index].ID {
		case "ps_backlog":
			data.ProjectStatuses[index].Color = "#E79D4F"
		case "ps_planned":
			data.ProjectStatuses[index].Color = "#A8A8AA"
		case "ps_progress":
			data.ProjectStatuses[index].Color = "#E2B714"
		case "ps_completed":
			data.ProjectStatuses[index].Color = "#5E6AD2"
		case "ps_canceled":
			data.ProjectStatuses[index].Color = "#8A8F98"
		}
		if data.ProjectStatuses[index].Position == 0 && index > 0 {
			data.ProjectStatuses[index].Position = float64(index)
		}
	}
	if data.SavedViews == nil {
		data.SavedViews = []domain.SavedView{}
	}
	if data.Comments == nil {
		data.Comments = map[string][]domain.Comment{}
	}
	if data.Activities == nil {
		data.Activities = map[string][]domain.ActivityEvent{}
	}
	if data.Notifications == nil {
		data.Notifications = []domain.Notification{}
	}
	if data.NotificationPreferences == nil {
		data.NotificationPreferences = map[string]domain.NotificationPreferences{}
	}
	if data.NotificationDeliveries == nil {
		data.NotificationDeliveries = []domain.NotificationDelivery{}
	}
	for _, user := range data.Users {
		if _, ok := data.NotificationPreferences[user.ID]; !ok {
			data.NotificationPreferences[user.ID] = defaultNotificationPreferences(user.ID)
		}
	}
	if data.Initiatives == nil {
		data.Initiatives = []domain.Initiative{}
	}
	if data.InitiativeUpdates == nil {
		data.InitiativeUpdates = map[string][]domain.InitiativeUpdate{}
	}
	for i := range data.Initiatives {
		if data.Initiatives[i].Creator.ID == "" {
			data.Initiatives[i].Creator = data.Viewer
		}
		if data.Initiatives[i].ContributingTeamIDs == nil {
			data.Initiatives[i].ContributingTeamIDs = []string{}
		}
		if data.Initiatives[i].LabelIDs == nil {
			data.Initiatives[i].LabelIDs = []string{}
		}
		if data.Initiatives[i].ProjectIDs == nil {
			data.Initiatives[i].ProjectIDs = []string{}
		}
		if data.Initiatives[i].Resources == nil {
			data.Initiatives[i].Resources = []domain.InitiativeResource{}
		}
		if data.Initiatives[i].Comments == nil {
			data.Initiatives[i].Comments = []domain.Comment{}
		}
		if data.Initiatives[i].DescriptionHistory == nil {
			data.Initiatives[i].DescriptionHistory = []domain.InitiativeDescriptionRevision{}
		}
		if data.Initiatives[i].UpdateSchedule.Cadence == "" {
			data.Initiatives[i].UpdateSchedule = domain.InitiativeUpdateSchedule{Cadence: "none", Weekday: 1, TimeRange: "09:00-12:00"}
		}
		if !data.Initiatives[i].NotificationRules.DescriptionChanges && !data.Initiatives[i].NotificationRules.NewUpdate && !data.Initiatives[i].NotificationRules.AllProjectUpdates {
			data.Initiatives[i].NotificationRules = domain.InitiativeNotificationRules{DescriptionChanges: true, NewUpdate: true}
		}
	}
	for initiativeID := range data.InitiativeUpdates {
		for i := range data.InitiativeUpdates[initiativeID] {
			if data.InitiativeUpdates[initiativeID][i].Comments == nil {
				data.InitiativeUpdates[initiativeID][i].Comments = []domain.Comment{}
			}
			if data.InitiativeUpdates[initiativeID][i].Reactions == nil {
				data.InitiativeUpdates[initiativeID][i].Reactions = map[string][]string{}
			}
		}
	}
	for i := range data.Notifications {
		if data.Notifications[i].OccurrenceCount < 1 {
			data.Notifications[i].OccurrenceCount = 1
		}
		if data.Notifications[i].Category == "" {
			data.Notifications[i].Category = legacyNotificationCategory(data.Notifications[i].Type)
		}
		if data.Notifications[i].GroupKey == "" {
			data.Notifications[i].GroupKey = data.Notifications[i].RecipientID + ":" + data.Notifications[i].IssueID + ":" + data.Notifications[i].Category
		}
		if len(data.Notifications[i].LatestActorIDs) == 0 && data.Notifications[i].Actor.ID != "" {
			data.Notifications[i].LatestActorIDs = []string{data.Notifications[i].Actor.ID}
		}
		if data.Notifications[i].FavoritedAt != nil {
			data.Notifications[i].Favorite = true
		}
		if data.Notifications[i].Favorite && data.Notifications[i].FavoritedAt == nil {
			now := time.Now().UTC()
			data.Notifications[i].FavoritedAt = &now
		}
	}
	for issueID := range data.Comments {
		for i := range data.Comments[issueID] {
			if data.Comments[issueID][i].Version < 1 {
				data.Comments[issueID][i].Version = 1
			}
			if data.Comments[issueID][i].Reactions == nil {
				data.Comments[issueID][i].Reactions = map[string][]string{}
			}
		}
	}
	for i := range data.Issues {
		if data.Issues[i].Version < 1 {
			data.Issues[i].Version = 1
		}
		if data.Issues[i].Reactions == nil {
			data.Issues[i].Reactions = map[string][]string{}
		}
		if data.Issues[i].Labels == nil {
			data.Issues[i].Labels = []domain.IssueLabel{}
		}
		if data.Issues[i].SubscriberIDs == nil {
			data.Issues[i].SubscriberIDs = []string{}
		}
		if data.Issues[i].SubIssueIDs == nil {
			data.Issues[i].SubIssueIDs = []string{}
		}
		if data.Issues[i].Relations == nil {
			data.Issues[i].Relations = []domain.IssueRelation{}
		}
		if data.Issues[i].Attachments == nil {
			data.Issues[i].Attachments = []domain.Attachment{}
		}
	}
	issueIndexes := make(map[string]int, len(data.Issues))
	for i := range data.Issues {
		issueIndexes[data.Issues[i].ID] = i
	}
	for i := range data.Issues {
		if data.Issues[i].ParentID == nil {
			continue
		}
		parentIndex, ok := issueIndexes[*data.Issues[i].ParentID]
		if !ok || parentIndex == i {
			data.Issues[i].ParentID = nil
			continue
		}
		if !slices.Contains(data.Issues[parentIndex].SubIssueIDs, data.Issues[i].ID) {
			data.Issues[parentIndex].SubIssueIDs = append(data.Issues[parentIndex].SubIssueIDs, data.Issues[i].ID)
		}
	}
}

func defaultCodeReviews(data *domain.Bootstrap) []domain.CodeReview {
	if len(data.Users) == 0 {
		return []domain.CodeReview{}
	}
	now := time.Now().UTC()
	author := data.Users[0]
	if len(data.Users) > 1 {
		author = data.Users[1]
	}
	issueIDs := []string{}
	if len(data.Issues) > 0 {
		issueIDs = []string{data.Issues[0].ID}
	}
	opened := now.AddDate(0, 0, -5)
	return []domain.CodeReview{{
		ID: "review_flow_keyboard", SlugID: "improve-release-keyboard-navigation-a81f2c9d", Provider: "github", ExternalID: "flow-pr-42", Number: 42,
		Title: "Improve release association keyboard navigation", Description: "Align the release picker keyboard flow with the rest of the application and add regression coverage.", Status: "open",
		RepositoryOwner: "heliumlabz", RepositoryName: "flow", URL: "https://github.com/heliumlabz/flow/pull/42", Author: author,
		ReviewerIDs: []string{data.Viewer.ID}, TeamReviewers: []string{"frontend"}, IssueIDs: issueIDs, BaseBranch: "main", HeadBranch: "reviews/release-keyboard", BranchState: "behind",
		Additions: 184, Deletions: 37, CommitCount: 3, Favorite: false, Draft: false, QuickToReview: true,
		Checks:    []domain.ReviewCheck{{ID: "check_review_build", Name: "Web build", Status: "passed"}, {ID: "check_review_api", Name: "API tests", Status: "passed"}, {ID: "check_review_lint", Name: "Lint", Status: "passed"}},
		Files:     []domain.ReviewFile{{Path: "web/src/components/issue/issue-release-picker.tsx", Status: "modified", Additions: 121, Deletions: 21, Patch: "@@ Release picker keyboard handling\n+ Support nested Escape navigation\n+ Keep checkbox focus stable"}, {Path: "api/cmd/server/releases_model_api_test.go", Status: "modified", Additions: 63, Deletions: 16, Patch: "@@ Release association tests\n+ Verify add and remove round trip\n+ Reject frozen release stages"}},
		Events:    []domain.ReviewEvent{{ID: "review_event_opened", Type: "opened", Actor: author, CreatedAt: opened}, {ID: "review_event_requested", Type: "review_requested", Actor: author, Body: data.Viewer.DisplayName, CreatedAt: opened.Add(time.Second)}},
		CreatedAt: opened, UpdatedAt: now.Add(-2 * time.Hour),
	}}
}

func defaultUserSettings(userID string) domain.UserSettings {
	return domain.UserSettings{UserID: userID, Language: "en-US", HomeView: "Flow Agent (default)", DisplayNames: "Full name", FirstDay: "Monday", Emoticons: true, SendComments: "Enter", FontSize: "Default", InterfaceTheme: "System preference", LightTheme: "Light", DarkTheme: "Dark", ReviewAutoAssign: true, BranchFormat: "{identifier}-{title}", PersonalSettingsVersion: 1, CodeReviewsEnabled: true, MergeStrategy: "Squash and merge", CodeTheme: "Flow Light", CodeFont: "12px, Regular, Default", ReviewCommentsFilter: "Exclude Bots", ReviewRequests: true, GithubTeamReviewRequests: true, ChecksMergeQueue: true, GitAttachmentFormat: "Title", GitBranchMoveStarted: true, CodingToolMoveStarted: true, ChangelogUpdates: true, InviteAcceptedUpdates: true, PrivacyUpdates: true, AgentEnabled: true, UpdatedAt: time.Now().UTC()}
}

func defaultWorkspaceSettings(data *domain.Bootstrap) domain.WorkspaceSettings {
	return domain.WorkspaceSettings{FiscalMonth: "January", GuestsAllowed: true, SessionDurationDays: 30, InvitePermission: "admins", TeamCreatePermission: "members", LabelPermission: "members", TemplatePermission: "members", APIKeyPermission: "members", FeatureFlags: map[string]bool{"ai": true, "initiatives": true, "documents": true, "customer-requests": true, "releases": true, "pulse": true, "asks": true, "library": true, "sidebar-teams": true, "sidebar-try": true, "recently-deleted": true, "audit-log": true, "emojis": true}, FeatureSettings: domain.FeatureSettings{InitiativeUpdateSchedule: "none", CustomerRevenueFormat: "annual", CustomerRevenueCurrency: "USD", CustomerManualEdits: true, CustomerStatuses: []domain.FeatureOption{{ID: "active", Name: "Active", Color: "#4cb782"}, {ID: "prospect", Name: "Prospect", Color: "#5e6ad2"}, {ID: "churned", Name: "Churned", Color: "#f2c94c"}, {ID: "lost", Name: "Lost", Color: "#eb5757"}}, CustomerTiers: []domain.FeatureOption{}, CustomerExcludedDomains: []string{}, CustomerGenericDomains: []string{}, PulseWorkspaceSchedule: "daily", AsksEmailAddresses: []string{}}, BillingEmail: data.Viewer.Email, Plan: "free", GoogleAuthEnabled: true, EmailAuthEnabled: true, InitiativePermission: "members", LoopPermission: "members", AgentGuidancePermission: "admins", AICreditReloadThresholdCents: 500, AICreditReloadAmountCents: 2000, UpdatedAt: time.Now().UTC()}
}

func defaultStateID(data *domain.Bootstrap, teamID string) string {
	for _, state := range data.States {
		if (state.TeamID == "" || state.TeamID == teamID) && state.Default {
			return state.ID
		}
	}
	for _, state := range data.States {
		if (state.TeamID == "" || state.TeamID == teamID) && state.Type == "backlog" {
			return state.ID
		}
	}
	return ""
}

func defaultNotificationPreferences(userID string) domain.NotificationPreferences {
	categories := map[string]bool{"assignments": true, "statusChanges": true, "comments": true, "mentions": true, "reactions": true, "subscriptions": true, "documents": true, "updates": true, "reminders": true, "loops": true, "integrations": true, "billing": true, "customerRequests": true, "triage": true}
	clone := func() map[string]bool {
		result := make(map[string]bool, len(categories))
		for key, value := range categories {
			result[key] = value
		}
		return result
	}
	return domain.NotificationPreferences{UserID: userID, Inbox: domain.NotificationChannelPreferences{Enabled: true, Categories: clone()}, Email: domain.NotificationChannelPreferences{Enabled: true, Categories: clone()}, Desktop: domain.NotificationChannelPreferences{Enabled: true, Categories: clone()}, EmailFormat: "digest", DelayLowPriority: true, ImmediateUrgent: true, SoundEnabled: true, UpdatedAt: time.Now().UTC()}
}

func legacyNotificationCategory(value string) string {
	switch value {
	case "assignment":
		return "assignments"
	case "mention":
		return "mentions"
	case "comment":
		return "comments"
	default:
		return "statusChanges"
	}
}

func (s *SQLiteStore) Bootstrap() domain.Bootstrap {
	data, _ := s.BootstrapFor("")
	return data
}

func (s *SQLiteStore) BootstrapFor(workspaceKey string) (domain.Bootstrap, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if workspaceKey == "" {
		workspaceKey = s.lastWorkspaceKey
	}
	data, ok := s.workspaces[workspaceKey]
	if !ok {
		return domain.Bootstrap{}, false
	}
	raw, _ := json.Marshal(data)
	var clone domain.Bootstrap
	_ = json.Unmarshal(raw, &clone)
	return clone, true
}

func (s *SQLiteStore) CycleForCalendar(id, token string) (domain.Cycle, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, data := range s.workspaces {
		for _, cycle := range data.Cycles {
			if cycle.ID == id && cycle.CalendarToken != "" && cycle.CalendarToken == token {
				return cycle, true
			}
		}
	}
	return domain.Cycle{}, false
}

func (s *SQLiteStore) Account() domain.AccountBootstrap {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := domain.AccountBootstrap{Viewer: s.viewer, Workspaces: []domain.WorkspaceMembership{}, LastWorkspaceKey: s.lastWorkspaceKey}
	for _, data := range s.workspaces {
		joined := data.Workspace.CreatedAt
		if joined.IsZero() {
			joined = time.Now().UTC()
		}
		result.Workspaces = append(result.Workspaces, domain.WorkspaceMembership{Workspace: data.Workspace, Role: "Admin", JoinedAt: joined, IssueCount: len(data.Issues)})
	}
	slices.SortFunc(result.Workspaces, func(a, b domain.WorkspaceMembership) int {
		return strings.Compare(strings.ToLower(a.Workspace.Name), strings.ToLower(b.Workspace.Name))
	})
	return result
}

func (s *SQLiteStore) Mutate(ctx context.Context, eventType, aggregateID string, payload any, mutate func(*domain.Bootstrap) error) error {
	return s.MutateWorkspace(ctx, "", eventType, aggregateID, payload, mutate)
}

func (s *SQLiteStore) MutateWorkspace(ctx context.Context, workspaceKey, eventType, aggregateID string, payload any, mutate func(*domain.Bootstrap) error) error {
	return s.MutateWorkspaceWithAggregate(ctx, workspaceKey, eventType, payload, func(data *domain.Bootstrap) (string, error) {
		return aggregateID, mutate(data)
	})
}

// MutateWithAggregate lets create operations derive their aggregate ID inside
// the same serialized transaction that allocates the entity ID.
func (s *SQLiteStore) MutateWithAggregate(ctx context.Context, eventType string, payload any, mutate func(*domain.Bootstrap) (string, error)) error {
	return s.MutateWorkspaceWithAggregate(ctx, "", eventType, payload, mutate)
}

func (s *SQLiteStore) MutateWorkspaceWithAggregate(ctx context.Context, workspaceKey, eventType string, payload any, mutate func(*domain.Bootstrap) (string, error)) error {
	s.mu.Lock()
	var event domain.DomainEvent
	err := func() error {
		defer s.mu.Unlock()
		if workspaceKey == "" {
			workspaceKey = s.lastWorkspaceKey
		}
		current, ok := s.workspaces[workspaceKey]
		if !ok {
			return fmt.Errorf("workspace %q: %w", workspaceKey, errors.New("not found"))
		}
		raw, _ := json.Marshal(current)
		var next domain.Bootstrap
		if err := json.Unmarshal(raw, &next); err != nil {
			return err
		}
		if actor, ok := actorFromContext(ctx); ok {
			next.Viewer = actor
			if index := slices.IndexFunc(next.Users, func(user domain.User) bool { return user.ID == actor.ID }); index >= 0 {
				next.Users[index] = actor
			} else {
				next.Users = append(next.Users, actor)
			}
		}
		aggregateID, err := mutate(&next)
		if err != nil {
			return err
		}
		payloadRaw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		event = domain.DomainEvent{ID: fmt.Sprintf("evt_%d", time.Now().UnixNano()), Type: eventType, AggregateID: aggregateID, Payload: payloadRaw, CreatedAt: time.Now().UTC()}
		if err := s.persistWorkspace(ctx, workspaceKey, next, &event); err != nil {
			return err
		}
		s.workspaces[workspaceKey] = next
		s.lastWorkspaceKey = workspaceKey
		return nil
	}()
	if err != nil {
		return err
	}
	if s.realtimeSink != nil {
		actor, _ := actorFromContext(ctx)
		s.realtimeSink(workspaceKey, domain.RealtimeEvent{ID: event.ID, Type: event.Type, AggregateID: event.AggregateID, ActorID: actor.ID, ClientID: realtimeClientFromContext(ctx), Payload: event.Payload, CreatedAt: event.CreatedAt})
	}
	return nil
}

func (s *SQLiteStore) persist(ctx context.Context, data domain.Bootstrap, event *domain.DomainEvent) error {
	return s.persistWorkspace(ctx, data.Workspace.URLKey, data, event)
}

func (s *SQLiteStore) persistWorkspace(ctx context.Context, workspaceKey string, data domain.Bootstrap, event *domain.DomainEvent) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_states(workspace_key,workspace_id,data,updated_at) VALUES(?,?,?,?) ON CONFLICT(workspace_key) DO UPDATE SET workspace_id=excluded.workspace_id,data=excluded.data,updated_at=excluded.updated_at`, workspaceKey, data.Workspace.ID, raw, now); err != nil {
		return err
	}
	viewerRaw, _ := json.Marshal(s.viewer)
	if len(viewerRaw) == 0 || string(viewerRaw) == "{}" {
		viewerRaw, _ = json.Marshal(data.Viewer)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO account_state(id,last_workspace_key,viewer,updated_at) VALUES(1,?,?,?) ON CONFLICT(id) DO UPDATE SET last_workspace_key=excluded.last_workspace_key,viewer=excluded.viewer,updated_at=excluded.updated_at`, workspaceKey, viewerRaw, now); err != nil {
		return err
	}
	if event != nil {
		if _, err := tx.ExecContext(ctx, `INSERT INTO domain_events(id,event_type,aggregate_id,payload,created_at) VALUES(?,?,?,?,?)`, event.ID, event.Type, event.AggregateID, []byte(event.Payload), event.CreatedAt.Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func firstWorkspaceKey(workspaces map[string]domain.Bootstrap) string {
	keys := make([]string, 0, len(workspaces))
	for key := range workspaces {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}

func (s *SQLiteStore) CreateWorkspace(ctx context.Context, name, urlKey, region string) (domain.Bootstrap, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.workspaces[urlKey]; exists {
		return domain.Bootstrap{}, fmt.Errorf("workspace key already exists")
	}
	viewer := s.viewer
	if actor, ok := actorFromContext(ctx); ok {
		viewer = actor
	}
	data := EmptyWorkspace(name, urlKey, region, viewer)
	event := &domain.DomainEvent{ID: fmt.Sprintf("evt_%d", time.Now().UnixNano()), Type: "workspace.created", AggregateID: data.Workspace.ID, Payload: json.RawMessage(fmt.Sprintf(`{"urlKey":%q}`, urlKey)), CreatedAt: time.Now().UTC()}
	if err := s.persistWorkspace(ctx, urlKey, data, event); err != nil {
		return domain.Bootstrap{}, err
	}
	s.workspaces[urlKey] = data
	s.lastWorkspaceKey = urlKey
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, _ = s.db.ExecContext(ctx, `INSERT INTO workspace_memberships(workspace_id,user_id,role,status,joined_at,last_seen_at) VALUES(?,?,?,?,?,?) ON CONFLICT(workspace_id,user_id) DO UPDATE SET role=excluded.role,status=excluded.status,joined_at=excluded.joined_at,last_seen_at=excluded.last_seen_at`, data.Workspace.ID, viewer.ID, "admin", "active", now, now)
	_, _ = s.db.ExecContext(ctx, `INSERT INTO auth_account_state(user_id,last_workspace_key,updated_at) VALUES(?,?,?) ON CONFLICT(user_id) DO UPDATE SET last_workspace_key=excluded.last_workspace_key,updated_at=excluded.updated_at`, viewer.ID, urlKey, now)
	if len(data.Teams) > 0 {
		_, _ = s.db.ExecContext(ctx, `INSERT INTO team_memberships(workspace_id,team_id,user_id,role,joined_at) VALUES(?,?,?,?,?) ON CONFLICT(workspace_id,team_id,user_id) DO UPDATE SET role=excluded.role,joined_at=excluded.joined_at`, data.Workspace.ID, data.Teams[0].ID, viewer.ID, "owner", now)
	}
	return data, nil
}

func (s *SQLiteStore) UpdateWorkspace(ctx context.Context, workspaceKey string, workspace domain.Workspace) (domain.Bootstrap, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.workspaces[workspaceKey]
	if !ok {
		return domain.Bootstrap{}, fmt.Errorf("workspace not found")
	}
	if workspace.URLKey == "" {
		workspace.URLKey = workspaceKey
	}
	if workspace.URLKey != workspaceKey {
		if _, exists := s.workspaces[workspace.URLKey]; exists {
			return domain.Bootstrap{}, fmt.Errorf("workspace key already exists")
		}
	}
	workspace.ID = data.Workspace.ID
	workspace.CreatedAt = data.Workspace.CreatedAt
	data.Workspace = workspace
	for index := range data.Teams {
		if len(data.Teams) == 1 && strings.EqualFold(data.Teams[index].Name, s.workspaces[workspaceKey].Workspace.Name) {
			data.Teams[index].Name = workspace.Name
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Bootstrap{}, err
	}
	defer tx.Rollback()
	raw, _ := json.Marshal(data)
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_states WHERE workspace_key = ?`, workspaceKey); err != nil {
		return domain.Bootstrap{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_states(workspace_key,workspace_id,data,updated_at) VALUES(?,?,?,?)`, workspace.URLKey, workspace.ID, raw, now.Format(time.RFC3339Nano)); err != nil {
		return domain.Bootstrap{}, err
	}
	viewerRaw, _ := json.Marshal(s.viewer)
	if _, err := tx.ExecContext(ctx, `INSERT INTO account_state(id,last_workspace_key,viewer,updated_at) VALUES(1,?,?,?) ON CONFLICT(id) DO UPDATE SET last_workspace_key=excluded.last_workspace_key,viewer=excluded.viewer,updated_at=excluded.updated_at`, workspace.URLKey, viewerRaw, now.Format(time.RFC3339Nano)); err != nil {
		return domain.Bootstrap{}, err
	}
	payload, _ := json.Marshal(workspace)
	if _, err := tx.ExecContext(ctx, `INSERT INTO domain_events(id,event_type,aggregate_id,payload,created_at) VALUES(?,?,?,?,?)`, fmt.Sprintf("evt_%d", now.UnixNano()), "workspace.updated", workspace.ID, payload, now.Format(time.RFC3339Nano)); err != nil {
		return domain.Bootstrap{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Bootstrap{}, err
	}
	if workspace.URLKey != workspaceKey {
		_, _ = s.db.ExecContext(ctx, `UPDATE auth_account_state SET last_workspace_key=?,updated_at=? WHERE last_workspace_key=?`, workspace.URLKey, time.Now().UTC().Format(time.RFC3339Nano), workspaceKey)
	}
	delete(s.workspaces, workspaceKey)
	s.workspaces[workspace.URLKey] = data
	s.lastWorkspaceKey = workspace.URLKey
	return data, nil
}

func (s *SQLiteStore) DeleteWorkspace(ctx context.Context, workspaceKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.workspaces[workspaceKey]
	if !ok {
		return fmt.Errorf("workspace not found")
	}
	delete(s.workspaces, workspaceKey)
	s.lastWorkspaceKey = firstWorkspaceKey(s.workspaces)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_states WHERE workspace_key = ?`, workspaceKey); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM team_memberships WHERE workspace_id = ?`, data.Workspace.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_memberships WHERE workspace_id = ?`, data.Workspace.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_invitations WHERE workspace_id = ?`, data.Workspace.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE auth_account_state SET last_workspace_key='',updated_at=? WHERE last_workspace_key=?`, time.Now().UTC().Format(time.RFC3339Nano), workspaceKey); err != nil {
		return err
	}
	now := time.Now().UTC()
	viewerRaw, _ := json.Marshal(s.viewer)
	if _, err := tx.ExecContext(ctx, `INSERT INTO account_state(id,last_workspace_key,viewer,updated_at) VALUES(1,?,?,?) ON CONFLICT(id) DO UPDATE SET last_workspace_key=excluded.last_workspace_key,viewer=excluded.viewer,updated_at=excluded.updated_at`, s.lastWorkspaceKey, viewerRaw, now.Format(time.RFC3339Nano)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO domain_events(id,event_type,aggregate_id,payload,created_at) VALUES(?,?,?,?,?)`, fmt.Sprintf("evt_%d", now.UnixNano()), "workspace.deleted", data.Workspace.ID, []byte(`{}`), now.Format(time.RFC3339Nano)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) Events(ctx context.Context, aggregateID string) ([]domain.DomainEvent, error) {
	query := `SELECT id,event_type,aggregate_id,payload,created_at FROM domain_events`
	args := []any{}
	if aggregateID != "" {
		query += ` WHERE aggregate_id = ?`
		args = append(args, aggregateID)
	}
	query += ` ORDER BY created_at ASC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []domain.DomainEvent
	for rows.Next() {
		var event domain.DomainEvent
		var created string
		if err := rows.Scan(&event.ID, &event.Type, &event.AggregateID, &event.Payload, &created); err != nil {
			return nil, err
		}
		event.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		events = append(events, event)
	}
	return events, rows.Err()
}
