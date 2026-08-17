package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
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
}

func (s *server) listWorkspaceLabels(w http.ResponseWriter, r *http.Request) {
	resource := r.URL.Query().Get("resourceType")
	result := []domain.IssueLabel{}
	for _, label := range s.workspaceData(r).Labels {
		if label.Scope == "" && (resource == "" || label.ResourceType == resource) {
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
		resource := "issue"
		if input.ResourceType != nil {
			resource = *input.ResourceType
		}
		if resource != "issue" && resource != "project" {
			return "", errInvalid
		}
		created = domain.IssueLabel{ID: fmt.Sprintf("label_%d", time.Now().UnixNano()), Name: strings.TrimSpace(*input.Name), Color: "#5E6AD2", ResourceType: resource, CreatorID: actor.ID, CreatedAt: time.Now().UTC()}
		applyLabelInput(&created, input)
		if created.GroupID != "" && !slices.ContainsFunc(data.LabelGroups, func(group domain.LabelGroup) bool {
			return group.ID == created.GroupID && group.ResourceType == resource
		}) {
			return "", errInvalid
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
		index := slices.IndexFunc(data.Labels, func(label domain.IssueLabel) bool { return label.ID == id && label.Scope == "" })
		if index < 0 {
			return errNotFound
		}
		applyLabelInput(&data.Labels[index], input)
		updated = data.Labels[index]
		cascadeLabel(data, updated)
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
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
		label.ResourceType = *input.ResourceType
	}
	if input.GroupID != nil {
		label.GroupID = *input.GroupID
	}
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

func (s *server) deleteWorkspaceLabel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "label.deleted", id, nil, func(data *domain.Bootstrap) error {
		before := len(data.Labels)
		data.Labels = slices.DeleteFunc(data.Labels, func(label domain.IssueLabel) bool { return label.ID == id && label.Scope == "" })
		if before == len(data.Labels) {
			return errNotFound
		}
		for index := range data.Issues {
			data.Issues[index].Labels = slices.DeleteFunc(data.Issues[index].Labels, func(label domain.IssueLabel) bool { return label.ID == id })
		}
		for index := range data.Projects {
			data.Projects[index].LabelIDs = removeString(data.Projects[index].LabelIDs, id)
		}
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
	ResourceType *string `json:"resourceType,omitempty"`
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
			resource = *input.ResourceType
		}
		created = domain.LabelGroup{ID: fmt.Sprintf("label_group_%d", time.Now().UnixNano()), Name: strings.TrimSpace(*input.Name), Color: "#8b8d98", ResourceType: resource, CreatedAt: time.Now().UTC()}
		if input.Color != nil {
			created.Color = *input.Color
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
		for index := range data.Labels {
			if data.Labels[index].GroupID == id {
				data.Labels[index].GroupID = ""
			}
		}
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusNoContent, nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type projectStatusInput struct {
	Name  *string `json:"name,omitempty"`
	Color *string `json:"color,omitempty"`
	Type  *string `json:"type,omitempty"`
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
		if input.Color != nil {
			created.Color = *input.Color
		}
		data.ProjectStatuses = append(data.ProjectStatuses, created)
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
		if input.Color != nil {
			data.ProjectStatuses[index].Color = *input.Color
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
		if len(input.IDs) != len(data.ProjectStatuses) {
			return errInvalid
		}
		ordered := make([]domain.ProjectStatus, 0, len(input.IDs))
		for position, id := range input.IDs {
			index := slices.IndexFunc(data.ProjectStatuses, func(status domain.ProjectStatus) bool { return status.ID == id })
			if index < 0 {
				return errInvalid
			}
			status := data.ProjectStatuses[index]
			status.Position = float64(position)
			ordered = append(ordered, status)
		}
		data.ProjectStatuses, updated = ordered, ordered
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) listWorkspaceIssueTemplates(w http.ResponseWriter, r *http.Request) {
	result := []domain.IssueTemplate{}
	for _, template := range s.workspaceData(r).IssueTemplates {
		if template.Scope == "workspace" || template.TeamID == "" {
			template.TeamID = ""
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
			return template.ID == id && (template.Scope == "workspace" || template.TeamID == "")
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
			return template.ID == id && (template.Scope == "workspace" || template.TeamID == "")
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
	writeJSON(w, http.StatusOK, s.workspaceData(r).IntegrationConnections)
}

func (s *server) connectIntegration(w http.ResponseWriter, r *http.Request) {
	provider := strings.ToLower(r.PathValue("provider"))
	if !slices.Contains([]string{"github", "slack", "figma", "google"}, provider) {
		writeError(w, http.StatusBadRequest, "unsupported integration")
		return
	}
	var input struct {
		Name   string            `json:"name"`
		Config map[string]string `json:"config"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	actor := requestActor(s, r)
	var updated domain.IntegrationConnection
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "integration.connected", provider, input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.IntegrationConnections, func(item domain.IntegrationConnection) bool { return item.Provider == provider })
		updated = domain.IntegrationConnection{ID: "integration_" + provider, Provider: provider, Name: strings.TrimSpace(input.Name), Status: "configured", Config: input.Config, ConnectedBy: actor.ID, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		if updated.Name == "" {
			updated.Name = strings.ToUpper(provider[:1]) + provider[1:]
		}
		if index >= 0 {
			updated.CreatedAt = data.IntegrationConnections[index].CreatedAt
			data.IntegrationConnections[index] = updated
		} else {
			data.IntegrationConnections = append(data.IntegrationConnections, updated)
		}
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
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
	writeJSON(w, http.StatusOK, map[string]any{"plan": data.WorkspaceSettings.Plan, "members": len(data.Members), "issues": len(data.Issues), "storageBytes": storage, "limits": limits})
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
