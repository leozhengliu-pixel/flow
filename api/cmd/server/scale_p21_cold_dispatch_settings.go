package main

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// These bodies borrow the legacy facade only for the duration of their
// synchronous route. The server receiver remains the original instance.

func (s *server) createProjectStatusWithStore(w http.ResponseWriter, r *http.Request, repo *store.SQLiteStore, input projectStatusInput) {
	var created domain.ProjectStatus
	err := repo.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "project_status.created", input, func(data *domain.Bootstrap) (string, error) {
		statusType := "planned"
		if input.Type != nil {
			statusType = *input.Type
		}
		if !slices.Contains([]string{"backlog", "planned", "started", "completed", "canceled"}, statusType) {
			return "", errInvalid
		}
		created = domain.ProjectStatus{ID: idgen.New("project_status_"), Name: strings.TrimSpace(*input.Name), Color: "#8b8d98", Type: statusType, Position: float64(len(data.ProjectStatuses))}
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

func (s *server) updateProjectStatusWithStore(w http.ResponseWriter, r *http.Request, repo *store.SQLiteStore, input projectStatusInput) {
	id := r.PathValue("id")
	var updated domain.ProjectStatus
	err := repo.MutateWorkspace(r.Context(), workspaceKey(r), "project_status.updated", id, input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.ProjectStatuses, func(status domain.ProjectStatus) bool { return status.ID == id })
		if index < 0 {
			return errNotFound
		}
		before := data.ProjectStatuses[index]
		if input.Name != nil {
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
		changed := !unchangedEdit(before, updated)
		for projectIndex := range data.Projects {
			if data.Projects[projectIndex].Status.ID == id {
				changed = changed || !unchangedEdit(data.Projects[projectIndex].Status, updated)
				data.Projects[projectIndex].Status = updated
			}
		}
		if !changed {
			return store.ErrNoMutation
		}
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteProjectStatusWithStore(w http.ResponseWriter, r *http.Request, repo *store.SQLiteStore) {
	id := r.PathValue("id")
	err := repo.MutateWorkspace(r.Context(), workspaceKey(r), "project_status.deleted", id, nil, func(data *domain.Bootstrap) error {
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
			return fmt.Errorf("%w: the status has projects assigned; archive or move them before deleting the status", errInvalid)
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

func (s *server) reorderProjectStatusesWithStore(w http.ResponseWriter, r *http.Request, repo *store.SQLiteStore, input struct {
	IDs []string `json:"ids"`
}) {
	var updated []domain.ProjectStatus
	err := repo.MutateWorkspace(r.Context(), workspaceKey(r), "project_status.reordered", "workspace", input, func(data *domain.Bootstrap) error {
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

func (s *server) createWorkspaceIssueTemplateWithStore(w http.ResponseWriter, r *http.Request, repo *store.SQLiteStore, input domain.IssueTemplateMutationInput, actor domain.User) {
	var created domain.IssueTemplate
	err := repo.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "issue_template.created", input, func(data *domain.Bootstrap) (string, error) {
		now := time.Now().UTC()
		created = domain.IssueTemplate{ID: idgen.New("template_"), Scope: "workspace", TemplateType: "standard", LabelIDs: []string{}, FormFields: []domain.TemplateFormField{}, Creator: actor, CreatedAt: now, UpdatedAt: now}
		if err := applyIssueTemplate(data, &created, input); err != nil {
			return "", err
		}
		data.IssueTemplates = append([]domain.IssueTemplate{created}, data.IssueTemplates...)
		return created.ID, nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

func (s *server) updateWorkspaceIssueTemplateWithStore(w http.ResponseWriter, r *http.Request, repo *store.SQLiteStore, input domain.IssueTemplateMutationInput) {
	id := r.PathValue("id")
	var updated domain.IssueTemplate
	err := repo.MutateWorkspace(r.Context(), workspaceKey(r), "issue_template.updated", id, input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.IssueTemplates, func(template domain.IssueTemplate) bool {
			return template.ID == id && (template.Scope == "workspace" || template.VisibilityTeamID == "")
		})
		if index < 0 {
			return errNotFound
		}
		before := data.IssueTemplates[index]
		if err := applyIssueTemplate(data, &data.IssueTemplates[index], input); err != nil {
			return err
		}
		if unchangedEdit(before, data.IssueTemplates[index]) {
			updated = before
			return store.ErrNoMutation
		}
		data.IssueTemplates[index].UpdatedAt = time.Now().UTC()
		updated = data.IssueTemplates[index]
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteWorkspaceIssueTemplateWithStore(w http.ResponseWriter, r *http.Request, repo *store.SQLiteStore) {
	id := r.PathValue("id")
	err := repo.MutateWorkspaceScoped(r.Context(), workspaceKey(r), "issue_template.deleted", id, nil, store.MetadataRecordScope{Write: []store.RecordKey{{Kind: "issueTemplates", ID: id}}}, func(data *domain.Bootstrap) error {
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

func (s *server) createDocumentTemplateWithStore(w http.ResponseWriter, r *http.Request, repo *store.SQLiteStore, input documentTemplateInput, actor domain.User) {
	var created domain.DocumentTemplate
	err := repo.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "document_template.created", input, func(data *domain.Bootstrap) (string, error) {
		now := time.Now().UTC()
		created = domain.DocumentTemplate{ID: idgen.New("document_template_"), Creator: actor, ContentData: map[string]any{"type": "doc", "content": []any{}}, CreatedAt: now, UpdatedAt: now}
		if err := applyDocumentTemplateInput(data, &created, input); err != nil {
			return "", err
		}
		data.DocumentTemplates = append([]domain.DocumentTemplate{created}, data.DocumentTemplates...)
		return created.ID, nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

func (s *server) updateDocumentTemplateWithStore(w http.ResponseWriter, r *http.Request, repo *store.SQLiteStore, input documentTemplateInput) {
	id := r.PathValue("id")
	var updated domain.DocumentTemplate
	scope := store.MetadataRecordScope{Write: []store.RecordKey{{Kind: "documentTemplates", ID: id}}}
	if input.TeamID != nil && strings.TrimSpace(*input.TeamID) != "" {
		scope.Read = []store.RecordKey{{Kind: "teams", ID: strings.TrimSpace(*input.TeamID)}}
	}
	err := repo.MutateWorkspaceScoped(r.Context(), workspaceKey(r), "document_template.updated", id, input, scope, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.DocumentTemplates, func(item domain.DocumentTemplate) bool { return item.ID == id })
		if index < 0 {
			return errNotFound
		}
		before := data.DocumentTemplates[index]
		if err := applyDocumentTemplateInput(data, &data.DocumentTemplates[index], input); err != nil {
			return err
		}
		updated = data.DocumentTemplates[index]
		if unchangedEdit(before, updated) {
			updated = before
			return store.ErrNoMutation
		}
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteDocumentTemplateWithStore(w http.ResponseWriter, r *http.Request, repo *store.SQLiteStore) {
	id := r.PathValue("id")
	err := repo.MutateWorkspaceScoped(r.Context(), workspaceKey(r), "document_template.deleted", id, nil, store.MetadataRecordScope{Write: []store.RecordKey{{Kind: "documentTemplates", ID: id}}}, func(data *domain.Bootstrap) error {
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
