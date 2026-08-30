package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

func requestActor(s *server, r *http.Request) domain.User {
	if user := authUser(r); user.ID != "" {
		return user
	}
	return s.workspaceData(r).Viewer
}

func (s *server) getUserSettings(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	writeJSON(w, http.StatusOK, data.UserSettings[requestActor(s, r).ID])
}

func (s *server) updateUserSettings(w http.ResponseWriter, r *http.Request) {
	var input domain.UserSettings
	if !decodeJSON(w, r, &input) {
		return
	}
	actor := requestActor(s, r)
	var updated domain.UserSettings
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "user_settings.updated", actor.ID, input, func(data *domain.Bootstrap) error {
		current := data.UserSettings[actor.ID]
		input.UserID = actor.ID
		input.UpdatedAt = time.Now().UTC()
		if input.Language == "" {
			input.Language = current.Language
		}
		if input.HomeView == "" {
			input.HomeView = current.HomeView
		}
		if input.FirstDay == "" {
			input.FirstDay = current.FirstDay
		}
		if input.BranchFormat == "" {
			input.BranchFormat = current.BranchFormat
		}
		if input.PersonalSettingsVersion == 0 {
			input.PersonalSettingsVersion = current.PersonalSettingsVersion
		}
		if input.MergeStrategy == "" {
			input.MergeStrategy = current.MergeStrategy
		}
		if input.CodeTheme == "" {
			input.CodeTheme = current.CodeTheme
		}
		if input.CodeFont == "" {
			input.CodeFont = current.CodeFont
		}
		if input.ReviewCommentsFilter == "" {
			input.ReviewCommentsFilter = current.ReviewCommentsFilter
		}
		if input.GitAttachmentFormat == "" {
			input.GitAttachmentFormat = current.GitAttachmentFormat
		}
		if input.PulseSchedule == "" {
			input.PulseSchedule = current.PulseSchedule
		}
		if !slices.Contains([]string{"daily", "weekly", "never"}, input.PulseSchedule) {
			return errInvalid
		}
		data.UserSettings[actor.ID] = input
		updated = input
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) updateAccountProfile(w http.ResponseWriter, r *http.Request) {
	var input struct {
		DisplayName string `json:"displayName"`
		Username    string `json:"username"`
		AvatarURL   string `json:"avatarUrl"`
		JobTitle    string `json:"jobTitle"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	actor := requestActor(s, r)
	user := actor
	var err error
	if !s.authDisabled {
		user, err = s.store.UpdateProfile(r.Context(), actor.ID, input.DisplayName, input.Username, input.AvatarURL)
		if err != nil {
			respondMutation(w, err, http.StatusOK, nil)
			return
		}
	} else {
		user.DisplayName, user.Name, user.AvatarURL = input.DisplayName, input.Username, input.AvatarURL
	}
	err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "account.profile_updated", actor.ID, input, func(data *domain.Bootstrap) error {
		if index := slices.IndexFunc(data.Users, func(item domain.User) bool { return item.ID == actor.ID }); index >= 0 {
			data.Users[index] = user
		}
		if data.Viewer.ID == actor.ID {
			data.Viewer = user
		}
		settings := data.UserSettings[actor.ID]
		settings.UserID, settings.JobTitle, settings.Username, settings.UpdatedAt = actor.ID, strings.TrimSpace(input.JobTitle), user.Name, time.Now().UTC()
		data.UserSettings[actor.ID] = settings
		return nil
	})
	respondMutation(w, err, http.StatusOK, user)
}

func (s *server) listAccountSessions(w http.ResponseWriter, r *http.Request) {
	if s.authDisabled {
		writeJSON(w, http.StatusOK, []domain.AccountSession{{ID: "development", Current: true, CreatedAt: time.Now().UTC(), LastSeenAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(30 * 24 * time.Hour)}})
		return
	}
	cookie, _ := r.Cookie(sessionCookieName)
	items, err := s.store.ListSessions(r.Context(), authUser(r).ID, cookie.Value)
	respondMutation(w, err, http.StatusOK, items)
}

func (s *server) revokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	if s.authDisabled {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	cookie, _ := r.Cookie(sessionCookieName)
	err := s.store.RevokeOtherSessions(r.Context(), authUser(r).ID, cookie.Value)
	if err != nil {
		respondMutation(w, err, http.StatusNoContent, nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) changeAccountPassword(w http.ResponseWriter, r *http.Request) {
	var input struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if s.authDisabled {
		writeJSON(w, http.StatusOK, map[string]bool{"changed": true})
		return
	}
	err := s.store.ChangePassword(r.Context(), authUser(r).ID, input.CurrentPassword, input.NewPassword)
	respondMutation(w, err, http.StatusOK, map[string]bool{"changed": err == nil})
}

func (s *server) getWorkspacePreferences(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.workspaceData(r).WorkspaceSettings)
}

func (s *server) updateWorkspacePreferences(w http.ResponseWriter, r *http.Request) {
	var input domain.WorkspaceSettings
	if !decodeJSON(w, r, &input) {
		return
	}
	var updated domain.WorkspaceSettings
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "workspace_preferences.updated", "workspace", input, func(data *domain.Bootstrap) error {
		if input.SessionDurationDays < 1 || input.SessionDurationDays > 365 {
			return errInvalid
		}
		input.AllowedDomains = normalizedStrings(input.AllowedDomains)
		for index := range input.AllowedDomains {
			input.AllowedDomains[index] = strings.ToLower(strings.TrimPrefix(input.AllowedDomains[index], "@"))
			if !strings.Contains(input.AllowedDomains[index], ".") {
				return errInvalid
			}
		}
		if input.FeatureFlags == nil {
			input.FeatureFlags = data.WorkspaceSettings.FeatureFlags
		}
		if input.FeatureSettings.InitiativeUpdateSchedule == "" {
			input.FeatureSettings.InitiativeUpdateSchedule = "none"
		}
		if input.FeatureSettings.CustomerRevenueFormat == "" {
			input.FeatureSettings.CustomerRevenueFormat = "annual"
		}
		if input.FeatureSettings.CustomerRevenueCurrency == "" {
			input.FeatureSettings.CustomerRevenueCurrency = "USD"
		}
		if input.FeatureSettings.PulseWorkspaceSchedule == "" {
			input.FeatureSettings.PulseWorkspaceSchedule = "daily"
		}
		input.FeatureSettings.CustomerExcludedDomains = normalizedStrings(input.FeatureSettings.CustomerExcludedDomains)
		input.FeatureSettings.CustomerGenericDomains = normalizedStrings(input.FeatureSettings.CustomerGenericDomains)
		input.FeatureSettings.AsksEmailAddresses = normalizedStrings(input.FeatureSettings.AsksEmailAddresses)
		input.UpdatedAt = time.Now().UTC()
		data.WorkspaceSettings = input
		updated = input
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

type labelInput struct {
	Name         *string `json:"name,omitempty"`
	Description  *string `json:"description,omitempty"`
	Color        *string `json:"color,omitempty"`
	ResourceType *string `json:"resourceType,omitempty"`
	GroupID      *string `json:"groupId,omitempty"`
	ArchivedAt   *string `json:"archivedAt,omitempty"`
}

func (s *server) listWorkspaceLabels(w http.ResponseWriter, r *http.Request) {
	resource := r.URL.Query().Get("resourceType")
	result := []domain.IssueLabel{}
	for _, label := range s.workspaceData(r).Labels {
		if labelScopeIsWorkspace(label.Scope) && (resource == "" || labelResourceType(label) == resource) {
			result = append(result, label)
		}
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) createWorkspaceLabel(w http.ResponseWriter, r *http.Request) {
	var input labelInput
	if !decodeJSON(w, r, &input) || input.Name == nil || strings.TrimSpace(*input.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	actor := requestActor(s, r)
	var created domain.IssueLabel
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "label.created", input, func(data *domain.Bootstrap) (string, error) {
		var err error
		created, err = newLabel(data, actor.ID, "Workspace", input)
		if err != nil {
			return "", err
		}
		data.Labels = append(data.Labels, created)
		return created.ID, nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

func (s *server) updateWorkspaceLabel(w http.ResponseWriter, r *http.Request) {
	var input labelInput
	if !decodeJSON(w, r, &input) {
		return
	}
	id := r.PathValue("id")
	var updated domain.IssueLabel
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "label.updated", id, input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.Labels, func(label domain.IssueLabel) bool { return label.ID == id && labelScopeIsWorkspace(label.Scope) })
		if index < 0 {
			return errNotFound
		}
		resource := labelResourceType(data.Labels[index])
		if input.ResourceType != nil {
			nextResource := strings.TrimSpace(*input.ResourceType)
			if !validLabelResourceType(nextResource) {
				return errInvalid
			}
			resource = nextResource
		}
		groupID := data.Labels[index].GroupID
		if input.GroupID != nil {
			groupID = strings.TrimSpace(*input.GroupID)
		}
		if !validLabelGroup(data, resource, groupID) {
			return errInvalid
		}
		applyLabelInput(&data.Labels[index], input)
		updated = data.Labels[index]
		cascadeLabel(data, updated)
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func newLabel(data *domain.Bootstrap, actorID, scope string, input labelInput) (domain.IssueLabel, error) {
	resource := "issue"
	if input.ResourceType != nil {
		resource = strings.TrimSpace(*input.ResourceType)
	}
	if !validLabelResourceType(resource) || resource == "initiative" && !labelScopeIsWorkspace(scope) {
		return domain.IssueLabel{}, errInvalid
	}
	created := domain.IssueLabel{ID: fmt.Sprintf("label_%d", time.Now().UnixNano()), Name: strings.TrimSpace(*input.Name), Color: "#5E6AD2", Scope: scope, ResourceType: resource, CreatorID: actorID, CreatedAt: time.Now().UTC()}
	applyLabelInput(&created, input)
	if !validLabelGroup(data, resource, created.GroupID) {
		return domain.IssueLabel{}, errInvalid
	}
	return created, nil
}

func validLabelResourceType(resource string) bool {
	return resource == "issue" || resource == "project" || resource == "initiative"
}

func validLabelGroup(data *domain.Bootstrap, resource, groupID string) bool {
	if groupID == "" {
		return true
	}
	if resource == "initiative" {
		return false
	}
	return slices.ContainsFunc(data.LabelGroups, func(group domain.LabelGroup) bool {
		return group.ID == groupID && group.ResourceType == resource && group.ArchivedAt == nil
	})
}

func applyLabelInput(label *domain.IssueLabel, input labelInput) {
	if input.Name != nil && strings.TrimSpace(*input.Name) != "" {
		label.Name = strings.TrimSpace(*input.Name)
	}
	if input.Description != nil {
		label.Description = strings.TrimSpace(*input.Description)
	}
	if input.Color != nil && strings.TrimSpace(*input.Color) != "" {
		label.Color = *input.Color
	}
	if input.ResourceType != nil {
		label.ResourceType = strings.TrimSpace(*input.ResourceType)
	}
	if input.GroupID != nil {
		label.GroupID = strings.TrimSpace(*input.GroupID)
	}
	if input.ArchivedAt != nil {
		value := strings.TrimSpace(*input.ArchivedAt)
		if value == "" {
			label.ArchivedAt = nil
		} else if parsed, err := time.Parse(time.RFC3339, value); err == nil {
			label.ArchivedAt = &parsed
		}
	}
}

func labelScopeIsWorkspace(scope string) bool {
	return scope == "" || strings.EqualFold(scope, "workspace")
}

func cascadeLabel(data *domain.Bootstrap, label domain.IssueLabel) {
	for issueIndex := range data.Issues {
		for labelIndex := range data.Issues[issueIndex].Labels {
			if data.Issues[issueIndex].Labels[labelIndex].ID == label.ID {
				data.Issues[issueIndex].Labels[labelIndex] = label
			}
		}
	}
}

func (s *server) moveWorkspaceLabelToTeams(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	moved := []domain.IssueLabel{}
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "label.moved_to_teams", id, func(data *domain.Bootstrap) (string, error) {
		index := slices.IndexFunc(data.Labels, func(label domain.IssueLabel) bool { return label.ID == id && labelScopeIsWorkspace(label.Scope) })
		if index < 0 {
			return "", errNotFound
		}
		source := data.Labels[index]
		if labelResourceType(source) != "issue" {
			return "", errInvalid
		}
		usedByTeam := map[string]bool{}
		for _, issue := range data.Issues {
			if slices.ContainsFunc(issue.Labels, func(label domain.IssueLabel) bool { return label.ID == id }) {
				usedByTeam[issue.Team.ID] = true
			}
		}
		if len(usedByTeam) == 0 {
			return "", errInvalid
		}
		for teamIndex, team := range data.Teams {
			if !usedByTeam[team.ID] {
				continue
			}
			teamLabel := source
			teamLabel.ID = fmt.Sprintf("label_%d_%d", time.Now().UnixNano(), teamIndex)
			teamLabel.Scope = team.ID
			teamLabel.GroupID = ""
			moved = append(moved, teamLabel)
			data.Labels = append(data.Labels, teamLabel)
			for issueIndex := range data.Issues {
				if data.Issues[issueIndex].Team.ID != team.ID {
					continue
				}
				for labelIndex := range data.Issues[issueIndex].Labels {
					if data.Issues[issueIndex].Labels[labelIndex].ID == id {
						data.Issues[issueIndex].Labels[labelIndex] = teamLabel
					}
				}
			}
		}
		data.Labels = slices.Delete(data.Labels, index, index+1)
		return id, nil
	})
	respondMutation(w, err, http.StatusOK, moved)
}

func (s *server) deleteWorkspaceLabel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "label.deleted", id, nil, func(data *domain.Bootstrap) error {
		before := len(data.Labels)
		data.Labels = slices.DeleteFunc(data.Labels, func(label domain.IssueLabel) bool { return label.ID == id && labelScopeIsWorkspace(label.Scope) })
		if before == len(data.Labels) {
			return errNotFound
		}
		removeLabelReferences(data, map[string]struct{}{id: {}})
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusNoContent, nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type labelGroupInput struct {
	Name         *string `json:"name,omitempty"`
	Color        *string `json:"color,omitempty"`
	Description  *string `json:"description,omitempty"`
	ResourceType *string `json:"resourceType,omitempty"`
	ArchivedAt   *string `json:"archivedAt,omitempty"`
}

func (s *server) createLabelGroup(w http.ResponseWriter, r *http.Request) {
	var input labelGroupInput
	if !decodeJSON(w, r, &input) || input.Name == nil || strings.TrimSpace(*input.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	var created domain.LabelGroup
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "label_group.created", input, func(data *domain.Bootstrap) (string, error) {
		resource := "issue"
		if input.ResourceType != nil {
			resource = strings.TrimSpace(*input.ResourceType)
		}
		if resource != "issue" && resource != "project" {
			return "", errInvalid
		}
		created = domain.LabelGroup{ID: fmt.Sprintf("label_group_%d", time.Now().UnixNano()), Name: strings.TrimSpace(*input.Name), Color: "#8b8d98", Scope: "Workspace", ResourceType: resource, CreatedAt: time.Now().UTC()}
		if input.Color != nil {
			created.Color = *input.Color
		}
		if input.Description != nil {
			created.Description = strings.TrimSpace(*input.Description)
		}
		data.LabelGroups = append(data.LabelGroups, created)
		return created.ID, nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

func (s *server) updateLabelGroup(w http.ResponseWriter, r *http.Request) {
	var input labelGroupInput
	if !decodeJSON(w, r, &input) {
		return
	}
	id := r.PathValue("id")
	var updated domain.LabelGroup
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "label_group.updated", id, input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.LabelGroups, func(group domain.LabelGroup) bool { return group.ID == id })
		if index < 0 {
			return errNotFound
		}
		if input.Name != nil && strings.TrimSpace(*input.Name) != "" {
			data.LabelGroups[index].Name = strings.TrimSpace(*input.Name)
		}
		if input.Color != nil {
			data.LabelGroups[index].Color = *input.Color
		}
		if input.Description != nil {
			data.LabelGroups[index].Description = strings.TrimSpace(*input.Description)
		}
		if input.ArchivedAt != nil {
			value := strings.TrimSpace(*input.ArchivedAt)
			if value == "" {
				data.LabelGroups[index].ArchivedAt = nil
			} else {
				parsed, parseErr := time.Parse(time.RFC3339, value)
				if parseErr != nil {
					return errInvalid
				}
				data.LabelGroups[index].ArchivedAt = &parsed
			}
			for labelIndex := range data.Labels {
				if data.Labels[labelIndex].GroupID != id {
					continue
				}
				data.Labels[labelIndex].ArchivedAt = data.LabelGroups[index].ArchivedAt
				cascadeLabel(data, data.Labels[labelIndex])
			}
		}
		updated = data.LabelGroups[index]
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteLabelGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "label_group.deleted", id, nil, func(data *domain.Bootstrap) error {
		before := len(data.LabelGroups)
		data.LabelGroups = slices.DeleteFunc(data.LabelGroups, func(group domain.LabelGroup) bool { return group.ID == id })
		if before == len(data.LabelGroups) {
			return errNotFound
		}
		childIDs := map[string]struct{}{}
		for _, label := range data.Labels {
			if label.GroupID == id {
				childIDs[label.ID] = struct{}{}
			}
		}
		if len(childIDs) > 0 {
			data.Labels = slices.DeleteFunc(data.Labels, func(label domain.IssueLabel) bool {
				_, remove := childIDs[label.ID]
				return remove
			})
			removeLabelReferences(data, childIDs)
		}
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusNoContent, nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func removeLabelReferences(data *domain.Bootstrap, ids map[string]struct{}) {
	removeIDs := func(values []string) []string {
		return slices.DeleteFunc(values, func(id string) bool {
			_, remove := ids[id]
			return remove
		})
	}
	for index := range data.Issues {
		data.Issues[index].Labels = slices.DeleteFunc(data.Issues[index].Labels, func(label domain.IssueLabel) bool {
			_, remove := ids[label.ID]
			return remove
		})
		data.Issues[index].SuggestedLabelIDs = removeIDs(data.Issues[index].SuggestedLabelIDs)
	}
	for index := range data.Projects {
		data.Projects[index].LabelIDs = removeIDs(data.Projects[index].LabelIDs)
	}
	for index := range data.Initiatives {
		data.Initiatives[index].LabelIDs = removeIDs(data.Initiatives[index].LabelIDs)
	}
	for index := range data.IssueTemplates {
		data.IssueTemplates[index].LabelIDs = removeIDs(data.IssueTemplates[index].LabelIDs)
		for childIndex := range data.IssueTemplates[index].SubIssues {
			data.IssueTemplates[index].SubIssues[childIndex].LabelIDs = removeIDs(data.IssueTemplates[index].SubIssues[childIndex].LabelIDs)
		}
	}
	for index := range data.ProjectTemplates {
		data.ProjectTemplates[index].LabelIDs = removeIDs(data.ProjectTemplates[index].LabelIDs)
	}
	for index := range data.TriageRoutingRules {
		data.TriageRoutingRules[index].LabelIDs = removeIDs(data.TriageRoutingRules[index].LabelIDs)
	}
	for index := range data.SavedViews {
		data.SavedViews[index].Filters = removeLabelReferencesFromJSON(data.SavedViews[index].Filters, ids)
	}
}

func removeLabelReferencesFromJSON(raw json.RawMessage, ids map[string]struct{}) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return raw
	}
	cleaned, remove := cleanLabelReferenceValue(value, ids)
	if remove {
		cleaned = []any{}
	}
	encoded, err := json.Marshal(cleaned)
	if err != nil {
		return raw
	}
	return encoded
}

func cleanLabelReferenceValue(value any, ids map[string]struct{}) (any, bool) {
	if text, ok := value.(string); ok {
		return value, deletedLabelReference(text, ids)
	}
	if values, ok := value.([]any); ok {
		cleaned := make([]any, 0, len(values))
		for _, item := range values {
			next, remove := cleanLabelReferenceValue(item, ids)
			if !remove {
				cleaned = append(cleaned, next)
			}
		}
		return cleaned, false
	}
	if object, ok := value.(map[string]any); ok {
		for _, key := range []string{"id", "value"} {
			if text, stringValue := object[key].(string); stringValue && deletedLabelReference(text, ids) {
				return nil, true
			}
		}
		cleaned := make(map[string]any, len(object))
		for key, item := range object {
			next, remove := cleanLabelReferenceValue(item, ids)
			if !remove {
				cleaned[key] = next
			}
		}
		return cleaned, false
	}
	return value, false
}

func deletedLabelReference(value string, ids map[string]struct{}) bool {
	if _, remove := ids[value]; remove {
		return true
	}
	for _, prefix := range []string{"label:", "labels:", "project-label:"} {
		if _, remove := ids[strings.TrimPrefix(value, prefix)]; strings.HasPrefix(value, prefix) && remove {
			return true
		}
	}
	return false
}

type projectStatusInput struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Color       *string `json:"color,omitempty"`
	Type        *string `json:"type,omitempty"`
}

func (s *server) createProjectStatus(w http.ResponseWriter, r *http.Request) {
	var input projectStatusInput
	if !decodeJSON(w, r, &input) || input.Name == nil || strings.TrimSpace(*input.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	var created domain.ProjectStatus
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "project_status.created", input, func(data *domain.Bootstrap) (string, error) {
		statusType := "planned"
		if input.Type != nil {
			statusType = *input.Type
		}
		if !slices.Contains([]string{"backlog", "planned", "started", "completed", "canceled"}, statusType) {
			return "", errInvalid
		}
		created = domain.ProjectStatus{ID: fmt.Sprintf("project_status_%d", time.Now().UnixNano()), Name: strings.TrimSpace(*input.Name), Color: "#8b8d98", Type: statusType, Position: float64(len(data.ProjectStatuses))}
		if input.Description != nil {
			created.Description = strings.TrimSpace(*input.Description)
		}
		if input.Color != nil {
			created.Color = *input.Color
		}
		data.ProjectStatuses = append(data.ProjectStatuses, created)
		normalizeProjectStatusPositions(data)
		if index := slices.IndexFunc(data.ProjectStatuses, func(status domain.ProjectStatus) bool { return status.ID == created.ID }); index >= 0 {
			created = data.ProjectStatuses[index]
		}
		return created.ID, nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

func (s *server) updateProjectStatus(w http.ResponseWriter, r *http.Request) {
	var input projectStatusInput
	if !decodeJSON(w, r, &input) {
		return
	}
	id := r.PathValue("id")
	var updated domain.ProjectStatus
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "project_status.updated", id, input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.ProjectStatuses, func(status domain.ProjectStatus) bool { return status.ID == id })
		if index < 0 {
			return errNotFound
		}
		if input.Name != nil && strings.TrimSpace(*input.Name) != "" {
			data.ProjectStatuses[index].Name = strings.TrimSpace(*input.Name)
		}
		if input.Description != nil {
			data.ProjectStatuses[index].Description = strings.TrimSpace(*input.Description)
		}
		if input.Color != nil {
			data.ProjectStatuses[index].Color = *input.Color
		}
		if input.Type != nil && *input.Type != data.ProjectStatuses[index].Type {
			return fmt.Errorf("%w: project status type cannot be changed", errInvalid)
		}
		if input.Type != nil {
			data.ProjectStatuses[index].Type = *input.Type
		}
		updated = data.ProjectStatuses[index]
		for projectIndex := range data.Projects {
			if data.Projects[projectIndex].Status.ID == id {
				data.Projects[projectIndex].Status = updated
			}
		}
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteProjectStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "project_status.deleted", id, nil, func(data *domain.Bootstrap) error {
		statusIndex := slices.IndexFunc(data.ProjectStatuses, func(status domain.ProjectStatus) bool { return status.ID == id })
		if statusIndex < 0 {
			return errNotFound
		}
		statusType, typeCount := data.ProjectStatuses[statusIndex].Type, 0
		for _, status := range data.ProjectStatuses {
			if status.Type == statusType {
				typeCount++
			}
		}
		if typeCount == 1 {
			return fmt.Errorf("%w: can't delete the last status of a type", errInvalid)
		}
		if slices.ContainsFunc(data.Projects, func(project domain.Project) bool { return project.Status.ID == id }) {
			return errors.New("status is used by projects")
		}
		before := len(data.ProjectStatuses)
		data.ProjectStatuses = slices.DeleteFunc(data.ProjectStatuses, func(status domain.ProjectStatus) bool { return status.ID == id })
		if before == len(data.ProjectStatuses) {
			return errNotFound
		}
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusNoContent, nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) reorderProjectStatuses(w http.ResponseWriter, r *http.Request) {
	var input struct {
		IDs []string `json:"ids"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	var updated []domain.ProjectStatus
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "project_status.reordered", "workspace", input, func(data *domain.Bootstrap) error {
		if len(input.IDs) != len(data.ProjectStatuses) || !allUniqueStrings(input.IDs) {
			return errInvalid
		}
		ordered := make([]domain.ProjectStatus, 0, len(input.IDs))
		lastRank := -1
		for position, id := range input.IDs {
			index := slices.IndexFunc(data.ProjectStatuses, func(status domain.ProjectStatus) bool { return status.ID == id })
			if index < 0 {
				return errInvalid
			}
			status := data.ProjectStatuses[index]
			rank := projectStatusTypeRank(status.Type)
			if rank < lastRank {
				return errInvalid
			}
			lastRank = rank
			status.Position = float64(position)
			ordered = append(ordered, status)
		}
		data.ProjectStatuses, updated = ordered, ordered
		for projectIndex := range data.Projects {
			if statusIndex := slices.IndexFunc(ordered, func(status domain.ProjectStatus) bool { return status.ID == data.Projects[projectIndex].Status.ID }); statusIndex >= 0 {
				data.Projects[projectIndex].Status = ordered[statusIndex]
			}
		}
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func projectStatusTypeRank(statusType string) int {
	return map[string]int{"backlog": 0, "planned": 1, "started": 2, "completed": 3, "canceled": 4}[statusType]
}

func normalizeProjectStatusPositions(data *domain.Bootstrap) {
	slices.SortStableFunc(data.ProjectStatuses, func(left, right domain.ProjectStatus) int {
		if rank := projectStatusTypeRank(left.Type) - projectStatusTypeRank(right.Type); rank != 0 {
			return rank
		}
		if left.Position < right.Position {
			return -1
		}
		if left.Position > right.Position {
			return 1
		}
		return strings.Compare(left.Name, right.Name)
	})
	for index := range data.ProjectStatuses {
		data.ProjectStatuses[index].Position = float64(index)
	}
}

func (s *server) listWorkspaceIssueTemplates(w http.ResponseWriter, r *http.Request) {
	result := []domain.IssueTemplate{}
	for _, template := range s.workspaceData(r).IssueTemplates {
		if template.Scope == "workspace" || template.VisibilityTeamID == "" {
			result = append(result, template)
		}
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) createWorkspaceIssueTemplate(w http.ResponseWriter, r *http.Request) {
	var input domain.IssueTemplateMutationInput
	if !decodeJSON(w, r, &input) || input.Name == nil || strings.TrimSpace(*input.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	actor := requestActor(s, r)
	var created domain.IssueTemplate
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "issue_template.created", input, func(data *domain.Bootstrap) (string, error) {
		now := time.Now().UTC()
		created = domain.IssueTemplate{ID: fmt.Sprintf("template_%d", now.UnixNano()), Scope: "workspace", TemplateType: "standard", LabelIDs: []string{}, FormFields: []domain.TemplateFormField{}, Creator: actor, CreatedAt: now, UpdatedAt: now}
		if err := applyIssueTemplate(data, &created, input); err != nil {
			return "", err
		}
		data.IssueTemplates = append([]domain.IssueTemplate{created}, data.IssueTemplates...)
		return created.ID, nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

func (s *server) updateWorkspaceIssueTemplate(w http.ResponseWriter, r *http.Request) {
	var input domain.IssueTemplateMutationInput
	if !decodeJSON(w, r, &input) {
		return
	}
	id := r.PathValue("id")
	var updated domain.IssueTemplate
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "issue_template.updated", id, input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.IssueTemplates, func(template domain.IssueTemplate) bool {
			return template.ID == id && (template.Scope == "workspace" || template.VisibilityTeamID == "")
		})
		if index < 0 {
			return errNotFound
		}
		if err := applyIssueTemplate(data, &data.IssueTemplates[index], input); err != nil {
			return err
		}
		data.IssueTemplates[index].UpdatedAt = time.Now().UTC()
		updated = data.IssueTemplates[index]
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteWorkspaceIssueTemplate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "issue_template.deleted", id, nil, func(data *domain.Bootstrap) error {
		before := len(data.IssueTemplates)
		data.IssueTemplates = slices.DeleteFunc(data.IssueTemplates, func(template domain.IssueTemplate) bool {
			return template.ID == id && (template.Scope == "workspace" || template.VisibilityTeamID == "")
		})
		if before == len(data.IssueTemplates) {
			return errNotFound
		}
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusNoContent, nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func randomSecret(prefix string) (string, error) {
	buffer := make([]byte, 24)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(buffer), nil
}

func secretHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func publicAPIKey(key domain.APIKey) domain.APIKey {
	key.SecretHash = ""
	return key
}

func (s *server) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	actor := requestActor(s, r)
	result := []domain.APIKey{}
	for _, key := range s.workspaceData(r).APIKeys {
		if key.CreatorID == actor.ID && key.RevokedAt == nil {
			result = append(result, publicAPIKey(key))
		}
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) createAPIKey(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name    string   `json:"name"`
		Scopes  []string `json:"scopes"`
		TeamIDs []string `json:"teamIds"`
	}
	if !decodeJSON(w, r, &input) || strings.TrimSpace(input.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	secret, err := randomSecret("flow_api_")
	if err != nil {
		respondMutation(w, err, http.StatusCreated, nil)
		return
	}
	actor := requestActor(s, r)
	var created domain.APIKey
	err = s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "api_key.created", input, func(data *domain.Bootstrap) (string, error) {
		if data.WorkspaceSettings.APIKeyPermission == "admins" && data.ViewerRole != "admin" {
			return "", errors.New("API key creation is limited to admins")
		}
		created = domain.APIKey{ID: fmt.Sprintf("api_key_%d", time.Now().UnixNano()), Name: strings.TrimSpace(input.Name), Prefix: secret[:min(len(secret), 17)], SecretHash: secretHash(secret), CreatorID: actor.ID, Scopes: normalizedStrings(input.Scopes), TeamIDs: normalizedStrings(input.TeamIDs), CreatedAt: time.Now().UTC()}
		if len(created.Scopes) == 0 {
			created.Scopes = []string{"read", "write"}
		}
		data.APIKeys = append([]domain.APIKey{created}, data.APIKeys...)
		return created.ID, nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusCreated, nil)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"key": publicAPIKey(created), "secret": secret})
}

func (s *server) revokeAPIKey(w http.ResponseWriter, r *http.Request) {
	id, actor := r.PathValue("id"), requestActor(s, r)
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "api_key.revoked", id, nil, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.APIKeys, func(key domain.APIKey) bool {
			return key.ID == id && (key.CreatorID == actor.ID || data.ViewerRole == "admin")
		})
		if index < 0 {
			return errNotFound
		}
		now := time.Now().UTC()
		data.APIKeys[index].RevokedAt = &now
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusNoContent, nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) listOAuthApplications(w http.ResponseWriter, r *http.Request) {
	result := s.workspaceData(r).OAuthApplications
	for index := range result {
		result[index].ClientSecret = ""
	}
	writeJSON(w, http.StatusOK, result)
}

type oauthInput struct {
	Name         *string   `json:"name,omitempty"`
	Description  *string   `json:"description,omitempty"`
	RedirectURIs *[]string `json:"redirectUris,omitempty"`
	Scopes       *[]string `json:"scopes,omitempty"`
}

func (s *server) createOAuthApplication(w http.ResponseWriter, r *http.Request) {
	var input oauthInput
	if !decodeJSON(w, r, &input) || input.Name == nil || strings.TrimSpace(*input.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	clientID, _ := randomSecret("flow_client_")
	secret, _ := randomSecret("flow_secret_")
	actor := requestActor(s, r)
	var created domain.OAuthApplication
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "oauth_application.created", input, func(data *domain.Bootstrap) (string, error) {
		created = domain.OAuthApplication{ID: fmt.Sprintf("oauth_app_%d", time.Now().UnixNano()), Name: strings.TrimSpace(*input.Name), ClientID: clientID, ClientSecret: secret, CreatorID: actor.ID, RedirectURIs: []string{}, Scopes: []string{"read"}, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		applyOAuthInput(&created, input)
		data.OAuthApplications = append([]domain.OAuthApplication{created}, data.OAuthApplications...)
		return created.ID, nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

func applyOAuthInput(app *domain.OAuthApplication, input oauthInput) {
	if input.Name != nil && strings.TrimSpace(*input.Name) != "" {
		app.Name = strings.TrimSpace(*input.Name)
	}
	if input.Description != nil {
		app.Description = strings.TrimSpace(*input.Description)
	}
	if input.RedirectURIs != nil {
		app.RedirectURIs = normalizedStrings(*input.RedirectURIs)
	}
	if input.Scopes != nil {
		app.Scopes = normalizedStrings(*input.Scopes)
	}
	app.UpdatedAt = time.Now().UTC()
}

func (s *server) updateOAuthApplication(w http.ResponseWriter, r *http.Request) {
	var input oauthInput
	if !decodeJSON(w, r, &input) {
		return
	}
	id := r.PathValue("id")
	var updated domain.OAuthApplication
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "oauth_application.updated", id, input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.OAuthApplications, func(app domain.OAuthApplication) bool { return app.ID == id })
		if index < 0 {
			return errNotFound
		}
		applyOAuthInput(&data.OAuthApplications[index], input)
		updated = data.OAuthApplications[index]
		updated.ClientSecret = ""
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteOAuthApplication(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "oauth_application.deleted", id, nil, func(data *domain.Bootstrap) error {
		before := len(data.OAuthApplications)
		data.OAuthApplications = slices.DeleteFunc(data.OAuthApplications, func(app domain.OAuthApplication) bool { return app.ID == id })
		if before == len(data.OAuthApplications) {
			return errNotFound
		}
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusNoContent, nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) exchangeOAuthToken(w http.ResponseWriter, r *http.Request) {
	var input struct {
		GrantType    string `json:"grant_type"`
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.GrantType != "client_credentials" {
		writeError(w, http.StatusBadRequest, "unsupported grant_type")
		return
	}
	data, ok := s.store.BootstrapFor(workspaceKey(r))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	index := slices.IndexFunc(data.OAuthApplications, func(app domain.OAuthApplication) bool {
		return app.ClientID == input.ClientID && subtle.ConstantTimeCompare([]byte(app.ClientSecret), []byte(input.ClientSecret)) == 1
	})
	if index < 0 {
		writeError(w, http.StatusUnauthorized, "invalid client credentials")
		return
	}
	app := data.OAuthApplications[index]
	secret, err := randomSecret("flow_oauth_")
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	key := domain.APIKey{ID: fmt.Sprintf("oauth_token_%d", time.Now().UnixNano()), Name: app.Name + " OAuth token", Prefix: secret[:min(len(secret), 19)], SecretHash: secretHash(secret), CreatorID: app.CreatorID, Scopes: app.Scopes, TeamIDs: []string{}, CreatedAt: time.Now().UTC()}
	err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "oauth_token.created", app.ID, nil, func(next *domain.Bootstrap) error {
		next.APIKeys = append([]domain.APIKey{key}, next.APIKeys...)
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"access_token": secret, "token_type": "Bearer", "scope": strings.Join(app.Scopes, " ")})
}

func (s *server) listIntegrations(w http.ResponseWriter, r *http.Request) {
	connections := s.workspaceData(r).IntegrationConnections
	for index := range connections {
		connections[index] = redactIntegrationConnection(connections[index])
	}
	writeJSON(w, http.StatusOK, connections)
}

func (s *server) connectIntegration(w http.ResponseWriter, r *http.Request) {
	provider := strings.ToLower(r.PathValue("provider"))
	if !slices.Contains([]string{"github", "gitlab", "slack", "figma", "google"}, provider) {
		writeError(w, http.StatusBadRequest, "unsupported integration")
		return
	}
	var input struct {
		Name            string            `json:"name"`
		Config          map[string]string `json:"config"`
		Scopes          []string          `json:"scopes"`
		Channels        []string          `json:"channels"`
		LinkbackEnabled bool              `json:"linkbackEnabled"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Config == nil {
		input.Config = map[string]string{}
	}
	secret := strings.TrimSpace(input.Config["apiToken"])
	if provider == "github" && strings.TrimSpace(input.Config["organization"]) == "" {
		writeError(w, http.StatusBadRequest, "organization is required")
		return
	}
	if provider == "gitlab" && secret == "" {
		writeError(w, http.StatusBadRequest, "API access token is required")
		return
	}
	delete(input.Config, "apiToken")
	if secret != "" {
		input.Config["tokenHint"] = secret[max(0, len(secret)-4):]
	}
	actor := requestActor(s, r)
	var updated domain.IntegrationConnection
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "integration.connected", provider, input, func(data *domain.Bootstrap) error {
		scope := strings.TrimSpace(input.Config["organization"])
		if scope == "" {
			scope = strings.TrimSpace(input.Config["host"])
		}
		index := slices.IndexFunc(data.IntegrationConnections, func(item domain.IntegrationConnection) bool {
			return item.Provider == provider && (scope == "" || item.Config["organization"] == scope || item.Config["host"] == scope)
		})
		now := time.Now().UTC()
		updated = domain.IntegrationConnection{ID: fmt.Sprintf("integration_%s_%d", provider, now.UnixNano()), Provider: provider, Name: strings.TrimSpace(input.Name), Status: "configured", Config: input.Config, ConnectedBy: actor.ID, CreatedAt: now, UpdatedAt: now, Scopes: uniqueLower(input.Scopes), Channels: uniqueTrimmed(input.Channels), LinkbackEnabled: input.LinkbackEnabled}
		if secret != "" {
			updated.SecretHash = secretHash(secret)
		}
		if updated.Name == "" {
			updated.Name = strings.ToUpper(provider[:1]) + provider[1:]
		}
		if index >= 0 {
			updated.CreatedAt = data.IntegrationConnections[index].CreatedAt
			updated.ID = data.IntegrationConnections[index].ID
			if updated.SecretHash == "" {
				updated.SecretHash = data.IntegrationConnections[index].SecretHash
			}
			data.IntegrationConnections[index] = updated
		} else {
			data.IntegrationConnections = append(data.IntegrationConnections, updated)
		}
		return nil
	})
	respondMutation(w, err, http.StatusOK, redactIntegrationConnection(updated))
}

// startIntegrationOAuth creates a short-lived, single-use state value and
// returns the provider authorization URL. Provider-specific token exchange is
// intentionally delegated to the configured callback endpoint; no provider
// credential is ever returned to the browser or persisted in Config.
func (s *server) startIntegrationOAuth(w http.ResponseWriter, r *http.Request) {
	provider := strings.ToLower(r.PathValue("provider"))
	if !slices.Contains([]string{"github", "gitlab", "slack", "figma", "google"}, provider) {
		writeError(w, http.StatusBadRequest, "unsupported integration")
		return
	}
	var result map[string]string
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "integration.oauth_started", provider, nil, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.IntegrationConnections, func(item domain.IntegrationConnection) bool { return item.Provider == provider })
		if index < 0 {
			return errNotFound
		}
		connection := &data.IntegrationConnections[index]
		authorizationURL := strings.TrimSpace(connection.Config["authorizationURL"])
		clientID := strings.TrimSpace(connection.Config["clientID"])
		redirectURI := strings.TrimSpace(connection.Config["redirectURI"])
		if authorizationURL == "" || clientID == "" || redirectURI == "" {
			connection.LastError = "OAuth authorizationURL, clientID, and redirectURI are required"
			connection.Status = "error"
			connection.UpdatedAt = time.Now().UTC()
			return errInvalid
		}
		state, err := randomSecret("flow_oauth_state_")
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		connection.OAuthState, connection.OAuthStartedAt, connection.LastError = state, &now, ""
		connection.Status, connection.UpdatedAt = "oauth_pending", now
		u, err := url.Parse(authorizationURL)
		if err != nil {
			return errInvalid
		}
		query := u.Query()
		query.Set("client_id", clientID)
		query.Set("redirect_uri", redirectURI)
		query.Set("response_type", "code")
		query.Set("state", state)
		if len(connection.Scopes) > 0 {
			query.Set("scope", strings.Join(connection.Scopes, " "))
		}
		u.RawQuery = query.Encode()
		result = map[string]string{"provider": provider, "connectionId": connection.ID, "state": state, "authorizationURL": u.String()}
		return nil
	})
	if err != nil {
		if err == errInvalid {
			writeError(w, http.StatusUnprocessableEntity, "OAuth configuration is incomplete")
			return
		}
		respondMutation(w, err, http.StatusNotFound, nil)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// finishIntegrationOAuth validates state and records the provider callback.
// A provider token exchange is deliberately not faked: deployments that need
// one should run their provider-specific exchange worker and then PATCH the
// connection status. This endpoint still gives operators durable pending,
// completed, and error states with replay protection.
func (s *server) finishIntegrationOAuth(w http.ResponseWriter, r *http.Request) {
	provider := strings.ToLower(r.PathValue("provider"))
	state, code := strings.TrimSpace(r.URL.Query().Get("state")), strings.TrimSpace(r.URL.Query().Get("code"))
	if state == "" {
		writeError(w, http.StatusBadRequest, "OAuth state is required")
		return
	}
	// Snapshot the credentials before the provider call. Network I/O must never
	// execute while MutateWorkspace holds the workspace write lock.
	data := s.workspaceData(r)
	index := slices.IndexFunc(data.IntegrationConnections, func(item domain.IntegrationConnection) bool {
		return item.Provider == provider && item.OAuthState == state
	})
	if index < 0 || data.IntegrationConnections[index].Status != "oauth_pending" {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}
	snapshot := data.IntegrationConnections[index]
	providerError := strings.TrimSpace(r.URL.Query().Get("error"))
	access, refresh, expiresIn := "", "", int64(0)
	if providerError == "" && code != "" {
		if tokenURL := strings.TrimSpace(snapshot.Config["tokenURL"]); tokenURL != "" {
			var exchangeErr error
			access, refresh, expiresIn, exchangeErr = exchangeIntegrationToken(r.Context(), tokenURL, snapshot.Config, code, s.authDisabled)
			if exchangeErr != nil {
				providerError = exchangeErr.Error()
			}
		}
	} else if providerError == "" {
		providerError = "OAuth provider did not return a code"
	}
	var result map[string]string
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "integration.oauth_completed", provider, nil, func(next *domain.Bootstrap) error {
		currentIndex := slices.IndexFunc(next.IntegrationConnections, func(item domain.IntegrationConnection) bool {
			return item.ID == snapshot.ID && item.OAuthState == state && item.Status == "oauth_pending"
		})
		if currentIndex < 0 {
			return errInvalid
		}
		connection := &next.IntegrationConnections[currentIndex]
		now := time.Now().UTC()
		if providerError != "" {
			connection.Status, connection.LastError = "error", providerError
		} else {
			connection.OAuthAccessToken, connection.OAuthRefreshToken = access, refresh
			if expiresIn > 0 {
				expiry := now.Add(time.Duration(expiresIn) * time.Second)
				connection.OAuthExpiresAt = &expiry
			}
			connection.Status, connection.OAuthCompletedAt, connection.LastError = "configured", &now, ""
		}
		connection.OAuthState, connection.UpdatedAt = "", now
		result = map[string]string{"provider": provider, "connectionId": connection.ID, "status": connection.Status}
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusBadRequest, nil)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func exchangeIntegrationToken(ctx context.Context, tokenURL string, config map[string]string, code string, allowLocal bool) (string, string, int64, error) {
	local := allowLocal && safeLocalDevelopmentURL(tokenURL)
	if !local && !safeOutboundHTTPS(ctx, tokenURL) {
		return "", "", 0, errors.New("unsafe OAuth token endpoint")
	}
	values := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {config["clientID"]}, "redirect_uri": {config["redirectURI"]}}
	secret := config["clientSecret"]
	if envName := config["clientSecretEnv"]; strings.HasPrefix(envName, "FLOW_INTEGRATION_") {
		secret = os.Getenv(envName)
	}
	if secret != "" {
		values.Set("client_secret", secret)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return "", "", 0, fmt.Errorf("OAuth token exchange failed: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := secureOutboundClient(15 * time.Second)
	if local {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return "", "", 0, fmt.Errorf("OAuth token exchange failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", "", 0, fmt.Errorf("OAuth token exchange returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return "", "", 0, err
	}
	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.AccessToken == "" {
		return "", "", 0, errors.New("OAuth token response did not include access_token")
	}
	return payload.AccessToken, payload.RefreshToken, payload.ExpiresIn, nil
}

func (s *server) refreshIntegrationOAuth(w http.ResponseWriter, r *http.Request) {
	provider, id := strings.ToLower(r.PathValue("provider")), r.PathValue("id")
	data := s.workspaceData(r)
	index := slices.IndexFunc(data.IntegrationConnections, func(item domain.IntegrationConnection) bool { return item.ID == id && item.Provider == provider })
	if index < 0 {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}
	snapshot := data.IntegrationConnections[index]
	if snapshot.OAuthRefreshToken == "" || strings.TrimSpace(snapshot.Config["tokenURL"]) == "" {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if !safeOutboundHTTPS(r.Context(), snapshot.Config["tokenURL"]) {
		writeError(w, http.StatusUnprocessableEntity, "unsafe OAuth token endpoint")
		return
	}
	values := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {snapshot.OAuthRefreshToken}, "client_id": {snapshot.Config["clientID"]}}
	secret := snapshot.Config["clientSecret"]
	if envName := snapshot.Config["clientSecretEnv"]; strings.HasPrefix(envName, "FLOW_INTEGRATION_") {
		secret = os.Getenv(envName)
	}
	if secret != "" {
		values.Set("client_secret", secret)
	}
	request, requestErr := http.NewRequestWithContext(r.Context(), http.MethodPost, snapshot.Config["tokenURL"], strings.NewReader(values.Encode()))
	if requestErr != nil {
		writeError(w, http.StatusBadGateway, requestErr.Error())
		return
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, exchangeErr := secureOutboundClient(15 * time.Second).Do(request)
	if exchangeErr != nil {
		writeError(w, http.StatusBadGateway, exchangeErr.Error())
		return
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if readErr != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("OAuth refresh returned HTTP %d", response.StatusCode))
		return
	}
	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if json.Unmarshal(body, &payload) != nil || payload.AccessToken == "" {
		writeError(w, http.StatusBadGateway, "OAuth refresh response did not include access_token")
		return
	}
	var result domain.IntegrationConnection
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "integration.oauth_refreshed", id, nil, func(data *domain.Bootstrap) error {
		currentIndex := slices.IndexFunc(data.IntegrationConnections, func(item domain.IntegrationConnection) bool {
			return item.ID == id && item.Provider == provider && item.OAuthRefreshToken == snapshot.OAuthRefreshToken
		})
		if currentIndex < 0 {
			return errNotFound
		}
		connection := &data.IntegrationConnections[currentIndex]
		connection.OAuthAccessToken = payload.AccessToken
		if payload.RefreshToken != "" {
			connection.OAuthRefreshToken = payload.RefreshToken
		}
		if payload.ExpiresIn > 0 {
			expiry := time.Now().UTC().Add(time.Duration(payload.ExpiresIn) * time.Second)
			connection.OAuthExpiresAt = &expiry
		}
		connection.Status, connection.LastError, connection.UpdatedAt = "configured", "", time.Now().UTC()
		result = *connection
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusBadRequest, nil)
		return
	}
	result.OAuthAccessToken, result.OAuthRefreshToken = "", ""
	writeJSON(w, http.StatusOK, result)
}

func (s *server) revokeIntegrationOAuth(w http.ResponseWriter, r *http.Request) {
	provider, id := strings.ToLower(r.PathValue("provider")), r.PathValue("id")
	data := s.workspaceData(r)
	index := slices.IndexFunc(data.IntegrationConnections, func(item domain.IntegrationConnection) bool { return item.ID == id && item.Provider == provider })
	if index < 0 {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}
	snapshot := data.IntegrationConnections[index]
	if revokeURL := strings.TrimSpace(snapshot.Config["revokeURL"]); revokeURL != "" && snapshot.OAuthAccessToken != "" {
		if !safeOutboundHTTPS(r.Context(), revokeURL) {
			writeError(w, http.StatusUnprocessableEntity, "unsafe OAuth revocation endpoint")
			return
		}
		request, requestErr := http.NewRequestWithContext(r.Context(), http.MethodPost, revokeURL, strings.NewReader(url.Values{"token": {snapshot.OAuthAccessToken}}.Encode()))
		if requestErr != nil {
			writeError(w, http.StatusBadGateway, requestErr.Error())
			return
		}
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response, callErr := secureOutboundClient(15 * time.Second).Do(request)
		if callErr != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
			if response != nil {
				response.Body.Close()
			}
			writeError(w, http.StatusBadGateway, "OAuth provider rejected token revocation")
			return
		}
		response.Body.Close()
	}
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "integration.oauth_revoked", id, nil, func(data *domain.Bootstrap) error {
		currentIndex := slices.IndexFunc(data.IntegrationConnections, func(item domain.IntegrationConnection) bool {
			return item.ID == id && item.Provider == provider && item.OAuthAccessToken == snapshot.OAuthAccessToken
		})
		if currentIndex < 0 {
			return errNotFound
		}
		connection := &data.IntegrationConnections[currentIndex]
		connection.OAuthAccessToken, connection.OAuthRefreshToken, connection.OAuthState, connection.OAuthExpiresAt = "", "", "", nil
		connection.Status, connection.LastError, connection.UpdatedAt = "disconnected", "", time.Now().UTC()
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusBadRequest, nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) updateIntegration(w http.ResponseWriter, r *http.Request) {
	provider, id := strings.ToLower(r.PathValue("provider")), r.PathValue("id")
	var input struct {
		Name            *string           `json:"name"`
		Config          map[string]string `json:"config"`
		Status          *string           `json:"status"`
		Scopes          *[]string         `json:"scopes"`
		Channels        *[]string         `json:"channels"`
		LinkbackEnabled *bool             `json:"linkbackEnabled"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	var updated domain.IntegrationConnection
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "integration.updated", id, input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.IntegrationConnections, func(item domain.IntegrationConnection) bool { return item.ID == id && item.Provider == provider })
		if index < 0 {
			return errNotFound
		}
		updated = data.IntegrationConnections[index]
		if updated.Config == nil {
			updated.Config = map[string]string{}
		}
		if input.Name != nil && strings.TrimSpace(*input.Name) != "" {
			updated.Name = strings.TrimSpace(*input.Name)
		}
		for key, value := range input.Config {
			if key != "apiToken" {
				updated.Config[key] = strings.TrimSpace(value)
			}
		}
		if input.Status != nil && slices.Contains([]string{"configured", "paused"}, *input.Status) {
			updated.Status = *input.Status
		}
		if input.Scopes != nil {
			updated.Scopes = uniqueLower(*input.Scopes)
		}
		if input.Channels != nil {
			updated.Channels = uniqueTrimmed(*input.Channels)
		}
		if input.LinkbackEnabled != nil {
			updated.LinkbackEnabled = *input.LinkbackEnabled
		}
		updated.UpdatedAt = time.Now().UTC()
		data.IntegrationConnections[index] = updated
		return nil
	})
	respondMutation(w, err, http.StatusOK, redactIntegrationConnection(updated))
}

func (s *server) disconnectIntegrationConnection(w http.ResponseWriter, r *http.Request) {
	provider, id := strings.ToLower(r.PathValue("provider")), r.PathValue("id")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "integration.disconnected", id, nil, func(data *domain.Bootstrap) error {
		before := len(data.IntegrationConnections)
		data.IntegrationConnections = slices.DeleteFunc(data.IntegrationConnections, func(item domain.IntegrationConnection) bool { return item.ID == id && item.Provider == provider })
		if before == len(data.IntegrationConnections) {
			return errNotFound
		}
		return nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
}

func (s *server) disconnectIntegration(w http.ResponseWriter, r *http.Request) {
	provider := strings.ToLower(r.PathValue("provider"))
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "integration.disconnected", provider, nil, func(data *domain.Bootstrap) error {
		before := len(data.IntegrationConnections)
		data.IntegrationConnections = slices.DeleteFunc(data.IntegrationConnections, func(item domain.IntegrationConnection) bool { return item.Provider == provider })
		if before == len(data.IntegrationConnections) {
			return errNotFound
		}
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusNoContent, nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) getWorkspaceUsage(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	storage := int64(0)
	for _, issue := range data.Issues {
		for _, attachment := range issue.Attachments {
			storage += attachment.Size
		}
	}
	limits := map[string]int64{"members": 250, "issues": 250, "storageBytes": 100 * 1024 * 1024}
	if data.WorkspaceSettings.Plan == "business" {
		limits = map[string]int64{"members": 1000, "issues": 100000, "storageBytes": 100 * 1024 * 1024 * 1024}
	}
	s.syncUsageAlerts(r, data, storage, limits)
	data = s.workspaceData(r)
	events := make([]map[string]any, 0)
	for _, event := range data.AuditLog {
		feature := ""
		amount := int64(0)
		action := strings.ToLower(event.Action)
		if strings.Contains(action, "loop") {
			feature, amount = "loops", 10
		} else if strings.Contains(action, "agent") || strings.Contains(action, "coding") {
			feature, amount = "coding-sessions", 25
		}
		if feature != "" {
			events = append(events, map[string]any{"id": event.ID, "feature": feature, "userId": event.Actor.ID, "amountCents": amount, "createdAt": event.CreatedAt})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"plan": data.WorkspaceSettings.Plan, "members": len(data.Members), "issues": len(data.Issues), "storageBytes": storage, "limits": limits,
		"aiCredits": map[string]any{
			"balanceCents": data.WorkspaceSettings.AICreditBalanceCents, "autoReloadEnabled": data.WorkspaceSettings.AICreditAutoReload,
			"autoReloadThresholdCents": data.WorkspaceSettings.AICreditReloadThresholdCents, "autoReloadAmountCents": data.WorkspaceSettings.AICreditReloadAmountCents,
			"workspaceSpendLimitCents": data.WorkspaceSettings.AIWorkspaceSpendLimitCents,
		},
		"events": events,
	})
}

type documentTemplateInput struct {
	TeamID       *string        `json:"teamId,omitempty"`
	Name         *string        `json:"name,omitempty"`
	Description  *string        `json:"description,omitempty"`
	Title        *string        `json:"title,omitempty"`
	Icon         *string        `json:"icon,omitempty"`
	Content      *string        `json:"content,omitempty"`
	ContentState *string        `json:"contentState,omitempty"`
	ContentData  map[string]any `json:"contentData,omitempty"`
}

func applyDocumentTemplateInput(data *domain.Bootstrap, item *domain.DocumentTemplate, input documentTemplateInput) error {
	if input.TeamID != nil {
		if !slices.ContainsFunc(data.Teams, func(team domain.Team) bool { return team.ID == *input.TeamID }) {
			return errInvalid
		}
		item.TeamID = *input.TeamID
	}
	if input.Name != nil {
		if strings.TrimSpace(*input.Name) == "" {
			return errInvalid
		}
		item.Name = strings.TrimSpace(*input.Name)
	}
	if input.Description != nil {
		item.Description = strings.TrimSpace(*input.Description)
	}
	if input.Title != nil {
		item.Title = strings.TrimSpace(*input.Title)
	}
	if input.Icon != nil {
		item.Icon = *input.Icon
	}
	if input.Content != nil {
		item.Content = *input.Content
	}
	if input.ContentState != nil {
		item.ContentState = *input.ContentState
	}
	if input.ContentData != nil {
		item.ContentData = input.ContentData
	}
	item.UpdatedAt = time.Now().UTC()
	return nil
}

func (s *server) createDocumentTemplate(w http.ResponseWriter, r *http.Request) {
	var input documentTemplateInput
	if !decodeJSON(w, r, &input) || input.Name == nil || input.TeamID == nil {
		writeError(w, http.StatusBadRequest, "name and teamId are required")
		return
	}
	actor := requestActor(s, r)
	var created domain.DocumentTemplate
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "document_template.created", input, func(data *domain.Bootstrap) (string, error) {
		now := time.Now().UTC()
		created = domain.DocumentTemplate{ID: fmt.Sprintf("document_template_%d", now.UnixNano()), Creator: actor, ContentData: map[string]any{"type": "doc", "content": []any{}}, CreatedAt: now, UpdatedAt: now}
		if err := applyDocumentTemplateInput(data, &created, input); err != nil {
			return "", err
		}
		data.DocumentTemplates = append([]domain.DocumentTemplate{created}, data.DocumentTemplates...)
		return created.ID, nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

func (s *server) updateDocumentTemplate(w http.ResponseWriter, r *http.Request) {
	var input documentTemplateInput
	if !decodeJSON(w, r, &input) {
		return
	}
	id := r.PathValue("id")
	var updated domain.DocumentTemplate
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "document_template.updated", id, input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.DocumentTemplates, func(item domain.DocumentTemplate) bool { return item.ID == id })
		if index < 0 {
			return errNotFound
		}
		if err := applyDocumentTemplateInput(data, &data.DocumentTemplates[index], input); err != nil {
			return err
		}
		updated = data.DocumentTemplates[index]
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteDocumentTemplate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "document_template.deleted", id, nil, func(data *domain.Bootstrap) error {
		before := len(data.DocumentTemplates)
		data.DocumentTemplates = slices.DeleteFunc(data.DocumentTemplates, func(item domain.DocumentTemplate) bool { return item.ID == id })
		if before == len(data.DocumentTemplates) {
			return errNotFound
		}
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusNoContent, nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
