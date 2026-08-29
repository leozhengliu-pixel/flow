package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

func migrationByID(data *domain.Bootstrap, id string) (*domain.MigrationJob, error) {
	index := slices.IndexFunc(data.MigrationJobs, func(item domain.MigrationJob) bool { return item.ID == id })
	if index < 0 {
		return nil, errNotFound
	}
	return &data.MigrationJobs[index], nil
}

func publicMigration(job domain.MigrationJob) domain.MigrationJob { job.Bundle = nil; return job }

func (s *server) listMigrations(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	result := make([]domain.MigrationJob, 0, len(data.MigrationJobs))
	for _, job := range data.MigrationJobs {
		if s.authDisabled || job.UserID == data.Viewer.ID {
			result = append(result, publicMigration(job))
		}
	}
	writeArrayPage(w, r, result)
}

func (s *server) getMigration(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	job, err := migrationByID(&data, r.PathValue("id"))
	if err != nil || (!s.authDisabled && job.UserID != data.Viewer.ID) {
		writeError(w, http.StatusNotFound, "migration not found")
		return
	}
	writeJSON(w, http.StatusOK, publicMigration(*job))
}

func (s *server) downloadMigrationManifest(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	job, err := migrationByID(&data, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "migration not found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", job.ID+"-manifest.json"))
	_ = json.NewEncoder(w).Encode(map[string]any{"version": 1, "migration": publicMigration(*job), "limitations": map[string]string{"identifier": "Target workspaces assign their own human-readable issue identifiers; sourceId is retained in mappings and metadata comments.", "updatedAt": "Linear public API does not accept updatedAt; the original value is retained in the metadata comment.", "startedAt": "Linear public API does not accept startedAt; the original value is retained in the metadata comment.", "sla": "Linear internal SLA timestamps are retained as import metadata when the target API rejects them."}})
}

func (s *server) previewMigration(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(128 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid migration bundle")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "migration bundle is required")
		return
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, 128<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read migration bundle")
		return
	}
	var bundle domain.MigrationBundle
	if json.Unmarshal(raw, &bundle) != nil || bundle.Workspace.ID == "" {
		writeError(w, http.StatusBadRequest, "unsupported migration bundle")
		return
	}
	if bundle.Version == 0 {
		bundle.Version = 1
	}
	if bundle.Source == "" {
		bundle.Source = "flow"
	}
	if bundle.ExportedAt.IsZero() {
		bundle.ExportedAt = time.Now().UTC()
	}
	normalizedRaw, _ := json.Marshal(bundle)
	data := s.workspaceData(r)
	now := time.Now().UTC()
	job := domain.MigrationJob{ID: fmt.Sprintf("migration_%d", now.UnixNano()), UserID: data.Viewer.ID, Filename: header.Filename, Source: bundle.Source, Target: "flow", Status: "mapping", Phase: "review", Counts: migrationCounts(bundle), Mappings: buildMigrationMappings(bundle, data), Errors: []string{}, Bundle: normalizedRaw, CreatedAt: now, UpdatedAt: now}
	err = s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "migration.previewed", map[string]any{"filename": header.Filename, "source": bundle.Source}, func(next *domain.Bootstrap) (string, error) {
		next.MigrationJobs = append([]domain.MigrationJob{job}, next.MigrationJobs...)
		appendAudit(next, "previewed", "migration", job.ID, migrationCountsMetadata(job.Counts))
		return job.ID, nil
	})
	respondMutation(w, err, http.StatusCreated, publicMigration(job))
}

func migrationCounts(bundle domain.MigrationBundle) map[string]int {
	comments, relations, attachments := 0, 0, 0
	projectUpdates, initiativeUpdates := 0, 0
	for _, values := range bundle.Comments {
		comments += len(values)
	}
	for _, values := range bundle.ProjectUpdates {
		projectUpdates += len(values)
	}
	for _, values := range bundle.InitiativeUpdates {
		initiativeUpdates += len(values)
	}
	for _, issue := range bundle.Issues {
		relations += len(issue.Relations)
		attachments += len(issue.Attachments)
	}
	return map[string]int{"users": len(bundle.Users), "teams": len(bundle.Teams), "projects": len(bundle.Projects), "projectUpdates": projectUpdates, "cycles": len(bundle.Cycles), "initiatives": len(bundle.Initiatives), "initiativeUpdates": initiativeUpdates, "issues": len(bundle.Issues), "comments": comments, "attachments": attachments, "relations": relations, "releases": len(bundle.Releases), "customers": len(bundle.Customers), "customerRequests": len(bundle.CustomerRequests), "slaRules": len(bundle.SLARules)}
}

func migrationMapping(entityType, sourceID, sourceName, targetID, targetName, action string) domain.MigrationEntityMapping {
	return domain.MigrationEntityMapping{ID: fmt.Sprintf("migration_mapping_%d", time.Now().UnixNano()), EntityType: entityType, SourceID: sourceID, SourceName: sourceName, TargetID: targetID, TargetName: targetName, Action: action, Status: "pending", Metadata: map[string]any{}}
}

func buildMigrationMappings(bundle domain.MigrationBundle, target domain.Bootstrap) []domain.MigrationEntityMapping {
	result := []domain.MigrationEntityMapping{}
	for _, user := range bundle.Users {
		index := slices.IndexFunc(target.Users, func(item domain.User) bool {
			return user.Email != "" && strings.EqualFold(item.Email, user.Email) || strings.EqualFold(item.DisplayName, user.DisplayName)
		})
		if index >= 0 {
			result = append(result, migrationMapping("user", user.ID, user.DisplayName, target.Users[index].ID, target.Users[index].DisplayName, "map"))
		} else {
			result = append(result, migrationMapping("user", user.ID, user.DisplayName, "", "", "review"))
		}
	}
	for _, team := range bundle.Teams {
		index := slices.IndexFunc(target.Teams, func(item domain.Team) bool {
			return strings.EqualFold(item.Key, team.Key) || strings.EqualFold(item.Name, team.Name)
		})
		if index >= 0 {
			result = append(result, migrationMapping("team", team.ID, team.Name, target.Teams[index].ID, target.Teams[index].Name, "map"))
		} else {
			result = append(result, migrationMapping("team", team.ID, team.Name, "", "", "create"))
		}
	}
	for _, project := range bundle.Projects {
		index := slices.IndexFunc(target.Projects, func(item domain.Project) bool { return strings.EqualFold(item.Name, project.Name) })
		if index >= 0 {
			result = append(result, migrationMapping("project", project.ID, project.Name, target.Projects[index].ID, target.Projects[index].Name, "map"))
		} else {
			result = append(result, migrationMapping("project", project.ID, project.Name, "", "", "create"))
		}
	}
	for _, item := range bundle.Cycles {
		result = append(result, migrationMapping("cycle", item.ID, item.Name, "", "", "create"))
	}
	for _, item := range bundle.Initiatives {
		result = append(result, migrationMapping("initiative", item.ID, item.Name, "", "", "create"))
	}
	for _, item := range bundle.Releases {
		result = append(result, migrationMapping("release", item.ID, item.Name, "", "", "create"))
	}
	for _, item := range bundle.Customers {
		result = append(result, migrationMapping("customer", item.ID, item.Name, "", "", "create"))
	}
	return result
}

func (s *server) updateMigrationMappings(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Mappings     []domain.MigrationEntityMapping `json:"mappings"`
		Target       string                          `json:"target"`
		TargetTeamID string                          `json:"targetTeamId"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	id := r.PathValue("id")
	var updated domain.MigrationJob
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "migration.mappings_updated", id, input, func(data *domain.Bootstrap) error {
		job, err := migrationByID(data, id)
		if err != nil {
			return err
		}
		if job.Status != "mapping" && job.Status != "failed" {
			return errInvalid
		}
		for _, patch := range input.Mappings {
			index := slices.IndexFunc(job.Mappings, func(item domain.MigrationEntityMapping) bool {
				return item.EntityType == patch.EntityType && item.SourceID == patch.SourceID
			})
			if index < 0 {
				continue
			}
			if patch.TargetID != "" {
				job.Mappings[index].TargetID = patch.TargetID
				job.Mappings[index].TargetName = patch.TargetName
			}
			if slices.Contains([]string{"map", "create", "invite", "skip", "metadata"}, patch.Action) {
				job.Mappings[index].Action = patch.Action
			}
			job.Mappings[index].Error = ""
			job.Mappings[index].Status = "pending"
		}
		if input.Target != "" {
			if input.Target == "linear" && job.Target != "linear" {
				for index := range job.Mappings {
					if slices.Contains([]string{"user", "team", "project"}, job.Mappings[index].EntityType) {
						job.Mappings[index].TargetID = ""
						job.Mappings[index].TargetName = ""
						if job.Mappings[index].EntityType == "user" {
							job.Mappings[index].Action = "review"
						} else {
							job.Mappings[index].Action = "create"
						}
					}
				}
			}
			job.Target = input.Target
		}
		if input.TargetTeamID != "" {
			job.TargetTeamID = input.TargetTeamID
		}
		job.UpdatedAt = time.Now().UTC()
		updated = *job
		return nil
	})
	respondMutation(w, err, http.StatusOK, publicMigration(updated))
}

func (s *server) inviteMigrationUsers(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	data := s.workspaceData(r)
	job, err := migrationByID(&data, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "migration not found")
		return
	}
	var bundle domain.MigrationBundle
	if json.Unmarshal(job.Bundle, &bundle) != nil {
		writeError(w, http.StatusBadRequest, "migration bundle is unavailable")
		return
	}
	sourceUsers := map[string]domain.User{}
	for _, user := range bundle.Users {
		sourceUsers[user.ID] = user
	}
	teamIDs := []string{}
	if job.TargetTeamID != "" {
		teamIDs = []string{job.TargetTeamID}
	} else if len(data.Teams) > 0 {
		teamIDs = []string{data.Teams[0].ID}
	}
	updatedMappings := []domain.MigrationEntityMapping{}
	for _, mapping := range job.Mappings {
		if mapping.EntityType != "user" || mapping.Action != "invite" || mapping.Status == "invited" {
			continue
		}
		source := sourceUsers[mapping.SourceID]
		if strings.TrimSpace(source.Email) == "" {
			mapping.Status = "failed"
			mapping.Error = "source user has no email"
			updatedMappings = append(updatedMappings, mapping)
			continue
		}
		invitation, inviteErr := s.store.Invite(r.Context(), data.Workspace.ID, data.Viewer.ID, source.Email, "member", teamIDs)
		if inviteErr != nil {
			mapping.Status = "failed"
			mapping.Error = inviteErr.Error()
		} else {
			mapping.TargetID = invitation.ID
			mapping.TargetName = invitation.Email
			mapping.Status = "invited"
			if s.mailer != nil {
				if mailErr := s.mailer.sendInvitation(invitation.Email, data.Workspace.Name, invitation.Token); mailErr != nil {
					mapping.Error = "invitation created but email failed: " + mailErr.Error()
				}
			}
		}
		updatedMappings = append(updatedMappings, mapping)
	}
	if len(updatedMappings) > 0 {
		_ = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "migration.users_invited", id, nil, func(next *domain.Bootstrap) error {
			current, findErr := migrationByID(next, id)
			if findErr != nil {
				return findErr
			}
			for _, patch := range updatedMappings {
				index := slices.IndexFunc(current.Mappings, func(item domain.MigrationEntityMapping) bool {
					return item.EntityType == patch.EntityType && item.SourceID == patch.SourceID
				})
				if index >= 0 {
					current.Mappings[index] = patch
				}
			}
			current.UpdatedAt = time.Now().UTC()
			return nil
		})
	}
	fresh := s.workspaceData(r)
	current, _ := migrationByID(&fresh, id)
	writeJSON(w, http.StatusOK, publicMigration(*current))
}

func (s *server) executeMigration(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Target       string `json:"target"`
		TargetTeamID string `json:"targetTeamId"`
		APIToken     string `json:"apiToken"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	id := r.PathValue("id")
	data := s.workspaceData(r)
	job, err := migrationByID(&data, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "migration not found")
		return
	}
	if input.Target == "" {
		input.Target = job.Target
	}
	if input.Target == "linear" {
		s.executeLinearMigration(w, r, *job, input.TargetTeamID, input.APIToken)
		return
	}
	var updated domain.MigrationJob
	err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "migration.executed", id, map[string]any{"target": "flow"}, func(next *domain.Bootstrap) error {
		current, err := migrationByID(next, id)
		if err != nil {
			return err
		}
		if current.Status != "mapping" && current.Status != "failed" {
			return errInvalid
		}
		var bundle domain.MigrationBundle
		if json.Unmarshal(current.Bundle, &bundle) != nil {
			return errInvalid
		}
		current.Status, current.Phase, current.Progress = "running", "entities", 5
		if err := applyFlowMigration(next, current, bundle, input.TargetTeamID); err != nil {
			current.Status = "failed"
			migrationErrorSummary(current, err)
			return err
		}
		now := time.Now().UTC()
		current.Status, current.Phase, current.Progress, current.CompletedAt, current.UpdatedAt = "completed", "complete", 100, &now, now
		updated = *current
		appendAudit(next, "completed", "migration", id, migrationCountsMetadata(current.Counts))
		return nil
	})
	respondMutation(w, err, http.StatusOK, publicMigration(updated))
}

func migrationCountsMetadata(counts map[string]int) map[string]any {
	result := map[string]any{}
	for key, value := range counts {
		result[key] = value
	}
	return result
}

func migrationErrorSummary(job *domain.MigrationJob, err error) {
	job.Errors = append(job.Errors, err.Error())
	job.UpdatedAt = time.Now().UTC()
}

func mappingTarget(job *domain.MigrationJob, entityType, sourceID string) string {
	index := slices.IndexFunc(job.Mappings, func(item domain.MigrationEntityMapping) bool {
		return item.EntityType == entityType && item.SourceID == sourceID
	})
	if index < 0 {
		return ""
	}
	return job.Mappings[index].TargetID
}
func completeMapping(job *domain.MigrationJob, entityType, sourceID, targetID, targetName string) {
	index := slices.IndexFunc(job.Mappings, func(item domain.MigrationEntityMapping) bool {
		return item.EntityType == entityType && item.SourceID == sourceID
	})
	if index < 0 {
		job.Mappings = append(job.Mappings, migrationMapping(entityType, sourceID, sourceID, targetID, targetName, "create"))
		index = len(job.Mappings) - 1
	}
	job.Mappings[index].TargetID = targetID
	job.Mappings[index].TargetName = targetName
	job.Mappings[index].Status = "completed"
}
func migrationID(kind string) string {
	return fmt.Sprintf("%s_migration_%d", kind, time.Now().UnixNano())
}

func applyFlowMigration(data *domain.Bootstrap, job *domain.MigrationJob, bundle domain.MigrationBundle, fallbackTeamID string) error {
	for _, mapping := range job.Mappings {
		if mapping.EntityType == "user" && mapping.Action == "review" {
			return fmt.Errorf("user %q still needs a mapping", mapping.SourceName)
		}
	}
	teamMap, userMap, stateMap, labelGroupMap, labelMap, projectMap, milestoneMap, cycleMap, initiativeMap, issueMap, customerMap, releaseMap, pipelineMap, slaRuleMap, issueSLAMap := map[string]string{}, map[string]string{}, map[string]string{}, map[string]string{}, map[string]string{}, map[string]string{}, map[string]string{}, map[string]string{}, map[string]string{}, map[string]string{}, map[string]string{}, map[string]string{}, map[string]string{}, map[string]string{}, map[string]string{}
	for _, mapping := range job.Mappings {
		switch mapping.EntityType {
		case "user":
			if mapping.Action == "map" {
				userMap[mapping.SourceID] = mapping.TargetID
			}
		case "team":
			if mapping.Action == "map" {
				teamMap[mapping.SourceID] = mapping.TargetID
			}
		case "project":
			if mapping.Action == "map" {
				projectMap[mapping.SourceID] = mapping.TargetID
			}
		}
	}
	if fallbackTeamID == "" && len(data.Teams) > 0 {
		fallbackTeamID = data.Teams[0].ID
	}
	for _, source := range bundle.Teams {
		if teamMap[source.ID] != "" {
			continue
		}
		oldID := source.ID
		id := migrationID("team")
		source.ID = id
		source.Key = uniqueMigratedTeamKey(data, source.Key)
		data.Teams = append(data.Teams, source)
		teamMap[oldID] = id
		completeMapping(job, "team", oldID, id, source.Name)
	}
	for _, source := range bundle.States {
		targetTeamID := mapOne(source.TeamID, teamMap, fallbackTeamID)
		index := slices.IndexFunc(data.States, func(item domain.WorkflowState) bool {
			return strings.EqualFold(item.Name, source.Name) && (item.TeamID == targetTeamID || item.TeamID == "")
		})
		if index >= 0 {
			stateMap[source.ID] = data.States[index].ID
			completeMapping(job, "state", source.ID, data.States[index].ID, data.States[index].Name)
			if mapping := findMigrationMapping(job, "state", source.ID); mapping != nil {
				mapping.Action = "map"
			}
			continue
		}
		oldID := source.ID
		source.ID = migrationID("state")
		source.TeamID = targetTeamID
		source.Default = false
		source.Reserved = false
		data.States = append(data.States, source)
		stateMap[oldID] = source.ID
		completeMapping(job, "state", oldID, source.ID, source.Name)
	}
	for _, source := range bundle.LabelGroups {
		index := slices.IndexFunc(data.LabelGroups, func(item domain.LabelGroup) bool {
			return item.ResourceType == source.ResourceType && strings.EqualFold(item.Name, source.Name)
		})
		if index >= 0 {
			labelGroupMap[source.ID] = data.LabelGroups[index].ID
			completeMapping(job, "labelGroup", source.ID, data.LabelGroups[index].ID, data.LabelGroups[index].Name)
			if mapping := findMigrationMapping(job, "labelGroup", source.ID); mapping != nil {
				mapping.Action = "map"
			}
			continue
		}
		oldID := source.ID
		source.ID = migrationID("label_group")
		source.Scope = fallbackTeamID
		data.LabelGroups = append(data.LabelGroups, source)
		labelGroupMap[oldID] = source.ID
		completeMapping(job, "labelGroup", oldID, source.ID, source.Name)
	}
	for _, source := range bundle.Labels {
		index := slices.IndexFunc(data.Labels, func(item domain.IssueLabel) bool {
			return item.ResourceType == source.ResourceType && strings.EqualFold(item.Name, source.Name)
		})
		if index >= 0 {
			labelMap[source.ID] = data.Labels[index].ID
			completeMapping(job, "label", source.ID, data.Labels[index].ID, data.Labels[index].Name)
			if mapping := findMigrationMapping(job, "label", source.ID); mapping != nil {
				mapping.Action = "map"
			}
			continue
		}
		oldID := source.ID
		source.ID = migrationID("label")
		source.Scope = fallbackTeamID
		source.GroupID = labelGroupMap[source.GroupID]
		source.CreatorID = data.Viewer.ID
		data.Labels = append(data.Labels, source)
		labelMap[oldID] = source.ID
		completeMapping(job, "label", oldID, source.ID, source.Name)
	}
	for _, source := range bundle.Projects {
		if projectMap[source.ID] != "" {
			continue
		}
		oldID := source.ID
		source.ID = migrationID("project")
		source.SlugID = slug(source.Name) + fmt.Sprintf("-%x", time.Now().UnixNano()&0xffffff)
		source.TeamIDs = mapIDs(source.TeamIDs, teamMap, fallbackTeamID)
		source.LabelIDs = mapIDs(source.LabelIDs, labelMap, "")
		source.MemberIDs = mapIDs(source.MemberIDs, userMap, "")
		if source.Lead != nil {
			if id := userMap[source.Lead.ID]; id != "" {
				source.Lead = userByID(data, id)
			} else {
				source.Lead = nil
			}
		}
		for i := range source.Milestones {
			oldMilestoneID := source.Milestones[i].ID
			source.Milestones[i].ID = migrationID("milestone")
			source.Milestones[i].ProjectID = source.ID
			milestoneMap[oldMilestoneID] = source.Milestones[i].ID
			completeMapping(job, "milestone", oldMilestoneID, source.Milestones[i].ID, source.Milestones[i].Name)
		}
		for i := range source.Comments {
			source.Comments[i].ID = migrationID("comment")
			source.Comments[i].User = data.Viewer
		}
		data.Projects = append(data.Projects, source)
		projectMap[oldID] = source.ID
		completeMapping(job, "project", oldID, source.ID, source.Name)
	}
	for sourceID, updates := range bundle.ProjectUpdates {
		targetID := projectMap[sourceID]
		if targetID == "" {
			continue
		}
		for i := range updates {
			updates[i].ID = migrationID("project_update")
			updates[i].ProjectID = targetID
			updates[i].User = data.Viewer
			for j := range updates[i].Comments {
				updates[i].Comments[j].ID = migrationID("comment")
				updates[i].Comments[j].User = data.Viewer
			}
			for j := range updates[i].Attachments {
				updates[i].Attachments[j].ID = migrationID("attachment")
				updates[i].Attachments[j].Creator = data.Viewer
			}
		}
		data.ProjectUpdates[targetID] = append(data.ProjectUpdates[targetID], updates...)
	}
	for _, source := range bundle.Cycles {
		oldID := source.ID
		source.ID = migrationID("cycle")
		source.TeamID = mapOne(source.TeamID, teamMap, fallbackTeamID)
		data.Cycles = append(data.Cycles, source)
		cycleMap[oldID] = source.ID
		completeMapping(job, "cycle", oldID, source.ID, source.Name)
	}
	for _, source := range bundle.Initiatives {
		oldID := source.ID
		source.ID = migrationID("initiative")
		source.ProjectIDs = mapIDs(source.ProjectIDs, projectMap, "")
		source.LeadTeamID = mapOne(source.LeadTeamID, teamMap, "")
		source.ContributingTeamIDs = mapIDs(source.ContributingTeamIDs, teamMap, "")
		source.LabelIDs = mapIDs(source.LabelIDs, labelMap, "")
		source.Creator = data.Viewer
		data.Initiatives = append(data.Initiatives, source)
		initiativeMap[oldID] = source.ID
		completeMapping(job, "initiative", oldID, source.ID, source.Name)
	}
	for sourceID, updates := range bundle.InitiativeUpdates {
		targetID := initiativeMap[sourceID]
		if targetID == "" {
			continue
		}
		for i := range updates {
			updates[i].ID = migrationID("initiative_update")
			updates[i].InitiativeID = targetID
			updates[i].User = data.Viewer
			for j := range updates[i].Comments {
				updates[i].Comments[j].ID = migrationID("comment")
				updates[i].Comments[j].User = data.Viewer
			}
			for j := range updates[i].Attachments {
				updates[i].Attachments[j].ID = migrationID("attachment")
				updates[i].Attachments[j].Creator = data.Viewer
			}
		}
		data.InitiativeUpdates[targetID] = append(data.InitiativeUpdates[targetID], updates...)
	}
	for _, source := range bundle.Projects {
		if targetID := projectMap[source.ID]; targetID != "" {
			if index := slices.IndexFunc(data.Projects, func(item domain.Project) bool { return item.ID == targetID }); index >= 0 {
				data.Projects[index].Initiatives = mapIDs(source.Initiatives, initiativeMap, "")
			}
		}
	}
	for _, source := range bundle.Customers {
		oldID := source.ID
		source.ID = migrationID("customer")
		source.OwnerID = mapOne(source.OwnerID, userMap, "")
		data.Customers = append(data.Customers, source)
		customerMap[oldID] = source.ID
		completeMapping(job, "customer", oldID, source.ID, source.Name)
	}
	for _, source := range bundle.ReleasePipelines {
		oldID := source.ID
		source.ID = migrationID("release_pipeline")
		source.TeamIDs = mapIDs(source.TeamIDs, teamMap, fallbackTeamID)
		data.ReleasePipelines = append(data.ReleasePipelines, source)
		pipelineMap[oldID] = source.ID
		completeMapping(job, "releasePipeline", oldID, source.ID, source.Name)
	}
	for _, source := range bundle.Releases {
		oldID := source.ID
		source.ID = migrationID("release")
		source.PipelineID = mapOne(source.PipelineID, pipelineMap, "")
		source.ProjectIDs = mapIDs(source.ProjectIDs, projectMap, "")
		source.IssueIDs = []string{}
		source.Creator = data.Viewer
		data.Releases = append(data.Releases, source)
		releaseMap[oldID] = source.ID
		completeMapping(job, "release", oldID, source.ID, source.Name)
	}
	nextNumber := nextIssueNumber(data.Issues)
	for _, source := range bundle.Issues {
		oldID := source.ID
		source.ID = migrationID("issue")
		source.Team = teamForMigration(data, mapOne(source.Team.ID, teamMap, fallbackTeamID))
		source.Number = nextNumber
		source.Identifier = fmt.Sprintf("%s-%d", source.Team.Key, nextNumber)
		nextNumber++
		if id := stateMap[source.State.ID]; id != "" {
			if index := slices.IndexFunc(data.States, func(item domain.WorkflowState) bool { return item.ID == id }); index >= 0 {
				source.State = data.States[index]
			}
		}
		mappedLabels := []domain.IssueLabel{}
		for _, label := range source.Labels {
			if id := labelMap[label.ID]; id != "" {
				if index := slices.IndexFunc(data.Labels, func(item domain.IssueLabel) bool { return item.ID == id }); index >= 0 {
					mappedLabels = append(mappedLabels, data.Labels[index])
				}
			}
		}
		source.Labels = mappedLabels
		source.Creator = data.Viewer
		if source.Assignee != nil {
			source.Assignee = userByID(data, userMap[source.Assignee.ID])
		}
		if source.Project != nil {
			if id := projectMap[source.Project.ID]; id != "" {
				if p, err := fullProjectByID(data, id); err == nil {
					source.Project = &domain.ProjectSummary{ID: p.ID, Name: p.Name, Icon: p.Icon, Color: p.Color}
				}
			} else {
				source.Project = nil
			}
		}
		if source.CycleID != nil {
			if id := cycleMap[*source.CycleID]; id != "" {
				source.CycleID = &id
			} else {
				source.CycleID = nil
			}
		}
		if source.ProjectMilestoneID != nil {
			if id := milestoneMap[*source.ProjectMilestoneID]; id != "" {
				source.ProjectMilestoneID = &id
			} else {
				source.ProjectMilestoneID = nil
			}
		}
		source.ParentID = nil
		source.SubIssueIDs = []string{}
		source.Relations = []domain.IssueRelation{}
		for attachmentIndex := range source.Attachments {
			source.Attachments[attachmentIndex].ID = migrationID("attachment")
			source.Attachments[attachmentIndex].IssueID = source.ID
			source.Attachments[attachmentIndex].Creator = data.Viewer
		}
		source.SubscriberIDs = mapIDs(source.SubscriberIDs, userMap, data.Viewer.ID)
		source.ExternalSource = "migration:" + job.ID + ":" + oldID
		data.Issues = append(data.Issues, source)
		issueMap[oldID] = source.ID
		completeMapping(job, "issue", oldID, source.ID, source.Identifier)
	}
	for _, source := range bundle.Issues {
		targetID := issueMap[source.ID]
		index := slices.IndexFunc(data.Issues, func(item domain.Issue) bool { return item.ID == targetID })
		if index < 0 {
			continue
		}
		if source.ParentID != nil {
			if id := issueMap[*source.ParentID]; id != "" {
				data.Issues[index].ParentID = &id
			}
		}
		for _, relation := range source.Relations {
			if related := issueMap[relation.RelatedIssueID]; related != "" {
				relation.ID = migrationID("relation")
				relation.IssueID = targetID
				relation.RelatedIssueID = related
				data.Issues[index].Relations = append(data.Issues[index].Relations, relation)
			}
		}
		if comments := bundle.Comments[source.ID]; len(comments) > 0 {
			commentMap := map[string]string{}
			for i := range comments {
				oldCommentID := comments[i].ID
				comments[i].ID = migrationID("comment")
				comments[i].User = data.Viewer
				commentMap[oldCommentID] = comments[i].ID
				completeMapping(job, "comment", oldCommentID, comments[i].ID, comments[i].Body)
			}
			for i := range comments {
				if comments[i].ParentID != nil {
					if id := commentMap[*comments[i].ParentID]; id != "" {
						comments[i].ParentID = &id
					} else {
						comments[i].ParentID = nil
					}
				}
			}
			data.Comments[targetID] = append(data.Comments[targetID], comments...)
		}
		if events := bundle.Activities[source.ID]; len(events) > 0 {
			for eventIndex := range events {
				events[eventIndex].ID = migrationID("activity")
				events[eventIndex].Actor = data.Viewer
			}
			data.Activities[targetID] = append(data.Activities[targetID], events...)
		}
	}
	for _, source := range bundle.Releases {
		if index := slices.IndexFunc(data.Releases, func(item domain.Release) bool { return item.ID == releaseMap[source.ID] }); index >= 0 {
			data.Releases[index].IssueIDs = mapIDs(source.IssueIDs, issueMap, "")
		}
	}
	for _, source := range bundle.CustomerRequests {
		oldID := source.ID
		source.ID = migrationID("customer_request")
		source.CustomerID = customerMap[source.CustomerID]
		source.IssueID = issueMap[source.IssueID]
		source.ProjectID = projectMap[source.ProjectID]
		data.CustomerRequests = append(data.CustomerRequests, source)
		completeMapping(job, "customerRequest", oldID, source.ID, source.Body)
	}
	for _, source := range bundle.SLARules {
		oldID := source.ID
		source.ID = migrationID("sla_rule")
		source.TeamIDs = mapIDs(source.TeamIDs, teamMap, fallbackTeamID)
		data.SLARules = append(data.SLARules, source)
		slaRuleMap[oldID] = source.ID
		completeMapping(job, "slaRule", oldID, source.ID, source.Name)
	}
	for _, source := range bundle.IssueSLAs {
		if id := issueMap[source.IssueID]; id != "" {
			oldID := source.ID
			source.ID = migrationID("issue_sla")
			source.IssueID = id
			source.RuleID = mapOne(source.RuleID, slaRuleMap, "")
			data.IssueSLAs = append(data.IssueSLAs, source)
			issueSLAMap[oldID] = source.ID
			completeMapping(job, "issueSLA", oldID, source.ID, source.Status)
		}
	}
	for _, source := range bundle.SLAEvents {
		if id := issueMap[source.IssueID]; id != "" {
			oldID := source.ID
			source.ID = migrationID("sla_event")
			source.IssueID = id
			source.SLAID = issueSLAMap[source.SLAID]
			data.SLAEvents = append(data.SLAEvents, source)
			completeMapping(job, "slaEvent", oldID, source.ID, source.Type)
		}
	}
	for _, source := range bundle.Subscriptions {
		oldID := source.ID
		if source.ResourceType == "issue" {
			source.ResourceID = issueMap[source.ResourceID]
		} else if source.ResourceType == "project" {
			source.ResourceID = projectMap[source.ResourceID]
		}
		if source.ResourceID == "" {
			continue
		}
		source.ID = migrationID("subscription")
		source.UserID = mapOne(source.UserID, userMap, data.Viewer.ID)
		data.Subscriptions = append(data.Subscriptions, source)
		completeMapping(job, "subscription", oldID, source.ID, source.ResourceType)
	}
	job.Progress = 95
	job.Phase = "relations"
	return nil
}

func mapOne(id string, mapping map[string]string, fallback string) string {
	if value := mapping[id]; value != "" {
		return value
	}
	return fallback
}
func mapIDs(ids []string, mapping map[string]string, fallback string) []string {
	result := []string{}
	for _, id := range ids {
		if value := mapOne(id, mapping, fallback); value != "" && !slices.Contains(result, value) {
			result = append(result, value)
		}
	}
	return result
}
func bundleTeamSourceID(bundle domain.MigrationBundle, name string) string {
	for _, item := range bundle.Teams {
		if item.Name == name {
			return item.ID
		}
	}
	return name
}
func teamForMigration(data *domain.Bootstrap, id string) domain.Team {
	for _, item := range data.Teams {
		if item.ID == id {
			return item
		}
	}
	if len(data.Teams) > 0 {
		return data.Teams[0]
	}
	return domain.Team{}
}
func uniqueMigratedTeamKey(data *domain.Bootstrap, key string) string {
	base := strings.ToUpper(strings.TrimSpace(key))
	if base == "" {
		base = "IMP"
	}
	if len(base) > 5 {
		base = base[:5]
	}
	candidate := base
	for suffix := 2; slices.ContainsFunc(data.Teams, func(item domain.Team) bool { return strings.EqualFold(item.Key, candidate) }); suffix++ {
		candidate = fmt.Sprintf("%s%d", base, suffix)
	}
	return candidate
}
func (s *server) rollbackMigration(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	snapshot := s.workspaceData(r)
	existing, findErr := migrationByID(&snapshot, id)
	if findErr != nil {
		writeError(w, http.StatusNotFound, "migration not found")
		return
	}
	if existing.Target == "linear" {
		var input struct {
			APIToken string `json:"apiToken"`
		}
		if !decodeJSON(w, r, &input) {
			return
		}
		s.rollbackLinearMigration(w, r, *existing, input.APIToken)
		return
	}
	var updated domain.MigrationJob
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "migration.rolled_back", id, nil, func(data *domain.Bootstrap) error {
		job, err := migrationByID(data, id)
		if err != nil {
			return err
		}
		if job.Status != "completed" && job.Status != "failed" {
			return errInvalid
		}
		created := func(kind, id string) bool {
			return slices.ContainsFunc(job.Mappings, func(item domain.MigrationEntityMapping) bool {
				return item.EntityType == kind && item.TargetID == id && item.Action == "create" && item.Status == "completed"
			})
		}
		data.Issues = slices.DeleteFunc(data.Issues, func(item domain.Issue) bool { return created("issue", item.ID) })
		data.States = slices.DeleteFunc(data.States, func(item domain.WorkflowState) bool { return created("state", item.ID) })
		data.Labels = slices.DeleteFunc(data.Labels, func(item domain.IssueLabel) bool { return created("label", item.ID) })
		data.LabelGroups = slices.DeleteFunc(data.LabelGroups, func(item domain.LabelGroup) bool { return created("labelGroup", item.ID) })
		data.Projects = slices.DeleteFunc(data.Projects, func(item domain.Project) bool { return created("project", item.ID) })
		data.Cycles = slices.DeleteFunc(data.Cycles, func(item domain.Cycle) bool { return created("cycle", item.ID) })
		data.Initiatives = slices.DeleteFunc(data.Initiatives, func(item domain.Initiative) bool { return created("initiative", item.ID) })
		data.Releases = slices.DeleteFunc(data.Releases, func(item domain.Release) bool { return created("release", item.ID) })
		data.ReleasePipelines = slices.DeleteFunc(data.ReleasePipelines, func(item domain.ReleasePipeline) bool { return created("releasePipeline", item.ID) })
		data.Customers = slices.DeleteFunc(data.Customers, func(item domain.Customer) bool { return created("customer", item.ID) })
		data.CustomerRequests = slices.DeleteFunc(data.CustomerRequests, func(item domain.CustomerRequest) bool { return created("customerRequest", item.ID) })
		data.SLARules = slices.DeleteFunc(data.SLARules, func(item domain.SLARule) bool { return created("slaRule", item.ID) })
		data.IssueSLAs = slices.DeleteFunc(data.IssueSLAs, func(item domain.IssueSLA) bool { return created("issueSLA", item.ID) })
		data.SLAEvents = slices.DeleteFunc(data.SLAEvents, func(item domain.SLAEvent) bool { return created("slaEvent", item.ID) })
		data.Subscriptions = slices.DeleteFunc(data.Subscriptions, func(item domain.Subscription) bool { return created("subscription", item.ID) })
		for projectID := range data.ProjectUpdates {
			if created("project", projectID) {
				delete(data.ProjectUpdates, projectID)
			}
		}
		for initiativeID := range data.InitiativeUpdates {
			if created("initiative", initiativeID) {
				delete(data.InitiativeUpdates, initiativeID)
			}
		}
		for targetID := range data.Comments {
			if created("issue", targetID) {
				delete(data.Comments, targetID)
			}
		}
		job.Status, job.Phase, job.Progress = "rolled_back", "complete", 100
		job.UpdatedAt = time.Now().UTC()
		updated = *job
		appendAudit(data, "rolled_back", "migration", id, nil)
		return nil
	})
	respondMutation(w, err, http.StatusOK, publicMigration(updated))
}

func (s *server) downloadMigrationBundle(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	bundle := domain.MigrationBundle{Version: 1, Source: "flow", ExportedAt: time.Now().UTC(), Workspace: data.Workspace, Users: data.Users, Teams: data.Teams, States: data.States, Labels: data.Labels, LabelGroups: data.LabelGroups, Projects: data.Projects, ProjectUpdates: data.ProjectUpdates, Cycles: data.Cycles, Initiatives: data.Initiatives, InitiativeUpdates: data.InitiativeUpdates, Issues: data.Issues, Comments: data.Comments, Activities: data.Activities, Releases: data.Releases, ReleasePipelines: data.ReleasePipelines, Customers: data.Customers, CustomerRequests: data.CustomerRequests, SLARules: data.SLARules, IssueSLAs: data.IssueSLAs, SLAEvents: data.SLAEvents, Subscriptions: data.Subscriptions}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", data.Workspace.URLKey+"-migration-bundle.json"))
	_ = json.NewEncoder(w).Encode(bundle)
}
