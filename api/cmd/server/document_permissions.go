package main

import (
	"net/http"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func (s *server) listDocumentPermissions(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	document, err := documentByID(&data, r.PathValue("id"))
	if err != nil || documentRole(s, data, *document) == "none" {
		writeError(w, http.StatusNotFound, "document not found")
		return
	}
	permissions := slices.Clone(document.Permissions)
	writeJSON(w, http.StatusOK, permissions)
}

func validDocumentPermission(data domain.Bootstrap, permission domain.DocumentPermission) bool {
	permission.SubjectType = strings.ToLower(strings.TrimSpace(permission.SubjectType))
	permission.Role = strings.ToLower(strings.TrimSpace(permission.Role))
	if permission.SubjectID == "" && permission.SubjectType != "workspace" {
		return false
	}
	if !slices.Contains([]string{"owner", "editor", "commenter", "viewer"}, permission.Role) {
		return false
	}
	switch permission.SubjectType {
	case "user":
		return slices.ContainsFunc(data.Users, func(user domain.User) bool { return user.ID == permission.SubjectID })
	case "team":
		return slices.ContainsFunc(data.Teams, func(team domain.Team) bool { return team.ID == permission.SubjectID })
	case "workspace":
		return permission.SubjectID == "" || permission.SubjectID == data.Workspace.ID || permission.SubjectID == data.Workspace.URLKey
	default:
		return false
	}
}

func (s *server) replaceDocumentPermissions(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Permissions []domain.DocumentPermission `json:"permissions"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	id := r.PathValue("id")
	var updated []domain.DocumentPermission
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "document.permissions_updated", id, input, func(data *domain.Bootstrap) error {
		document, err := documentByID(data, id)
		if err != nil {
			return err
		}
		if documentRole(s, *data, *document) != "owner" {
			return store.ErrAuthForbidden
		}
		now := time.Now().UTC()
		permissions := make([]domain.DocumentPermission, 0, len(input.Permissions)+1)
		seen := map[string]struct{}{}
		for _, permission := range input.Permissions {
			permission.DocumentID = document.ID
			permission.SubjectType = strings.ToLower(strings.TrimSpace(permission.SubjectType))
			permission.SubjectID = strings.TrimSpace(permission.SubjectID)
			permission.Role = strings.ToLower(strings.TrimSpace(permission.Role))
			key := permission.SubjectType + ":" + permission.SubjectID
			if _, exists := seen[key]; !validDocumentPermission(*data, permission) || permission.Role == "owner" && permission.SubjectType != "user" || key == ":" || exists {
				return errInvalid
			}
			seen[key] = struct{}{}
			permission.ID = "document_permission_" + strings.ReplaceAll(key, ":", "_")
			permission.CreatedAt, permission.UpdatedAt = now, now
			permissions = append(permissions, permission)
		}
		if !slices.ContainsFunc(permissions, func(permission domain.DocumentPermission) bool {
			return permission.SubjectType == "user" && permission.SubjectID == document.Creator.ID
		}) {
			permissions = append(permissions, domain.DocumentPermission{ID: "document_permission_" + document.Creator.ID, DocumentID: document.ID, SubjectType: "user", SubjectID: document.Creator.ID, Role: "owner", CreatedAt: now, UpdatedAt: now})
		}
		document.Permissions = permissions
		document.UpdatedAt = now
		updated = slices.Clone(permissions)
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) updateDocumentPermission(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Role *string `json:"role"`
	}
	if !decodeJSON(w, r, &input) || input.Role == nil {
		writeError(w, http.StatusBadRequest, "role is required")
		return
	}
	role := strings.ToLower(strings.TrimSpace(*input.Role))
	id, permissionID := r.PathValue("id"), r.PathValue("permissionId")
	var updated domain.DocumentPermission
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "document.permission_updated", id, input, func(data *domain.Bootstrap) error {
		document, err := documentByID(data, id)
		if err != nil {
			return err
		}
		if documentRole(s, *data, *document) != "owner" {
			return store.ErrAuthForbidden
		}
		index := slices.IndexFunc(document.Permissions, func(permission domain.DocumentPermission) bool { return permission.ID == permissionID })
		if index < 0 || !slices.Contains([]string{"owner", "editor", "commenter", "viewer"}, role) {
			return errInvalid
		}
		if role == "owner" && document.Permissions[index].SubjectType != "user" {
			return errInvalid
		}
		document.Permissions[index].Role = role
		document.Permissions[index].UpdatedAt = time.Now().UTC()
		document.UpdatedAt = document.Permissions[index].UpdatedAt
		updated = document.Permissions[index]
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteDocumentPermission(w http.ResponseWriter, r *http.Request) {
	id, permissionID := r.PathValue("id"), r.PathValue("permissionId")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "document.permission_deleted", id, map[string]string{"permissionId": permissionID}, func(data *domain.Bootstrap) error {
		document, err := documentByID(data, id)
		if err != nil {
			return err
		}
		if documentRole(s, *data, *document) != "owner" {
			return store.ErrAuthForbidden
		}
		index := slices.IndexFunc(document.Permissions, func(permission domain.DocumentPermission) bool { return permission.ID == permissionID })
		if index < 0 {
			return errNotFound
		}
		if document.Permissions[index].SubjectType == "user" && document.Permissions[index].SubjectID == document.Creator.ID {
			return errInvalid
		}
		document.Permissions = slices.Delete(document.Permissions, index, index+1)
		document.UpdatedAt = time.Now().UTC()
		return nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
}

func (s *server) listDocumentComments(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	document, err := documentByID(&data, r.PathValue("id"))
	if err != nil || documentRole(s, data, *document) == "none" {
		writeError(w, http.StatusNotFound, "document not found")
		return
	}
	comments := slices.Clone(data.Comments[document.ID])
	if comments == nil {
		comments = []domain.Comment{}
	}
	writeJSON(w, http.StatusOK, comments)
}
