package main

import (
	"flow/api/internal/domain"
	"flow/api/internal/store"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

func pageBounds(r *http.Request, total int) (int, int) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	start, _ := strconv.Atoi(r.URL.Query().Get("cursor"))
	if start < 0 {
		start = 0
	}
	if start > total {
		start = total
	}
	end := min(total, start+limit)
	return start, end
}
func writePage[T any](w http.ResponseWriter, r *http.Request, items []T) {
	start, end := pageBounds(r, len(items))
	next := ""
	if end < len(items) {
		next = strconv.Itoa(end)
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": items[start:end], "nextCursor": next, "total": len(items)})
}
func parityID(kind string) string { return fmt.Sprintf("%s_%d", kind, time.Now().UnixNano()) }

func (s *server) listProjectRelations(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	id := r.PathValue("id")
	items := slices.DeleteFunc(slices.Clone(data.ProjectRelations), func(item domain.ProjectRelation) bool { return item.ProjectID != id && item.RelatedProjectID != id })
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	writePage(w, r, items)
}
func (s *server) createProjectRelation(w http.ResponseWriter, r *http.Request) {
	var input domain.ProjectRelation
	if !decodeJSON(w, r, &input) {
		return
	}
	input.ProjectID = r.PathValue("id")
	if input.RelatedProjectID == "" || input.RelatedProjectID == input.ProjectID || !slices.Contains([]string{"related", "blocks", "blocked_by", "dependency"}, input.Type) {
		writeError(w, http.StatusBadRequest, "invalid project relation")
		return
	}
	var created domain.ProjectRelation
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "project.relation_created", input.ProjectID, input, func(data *domain.Bootstrap) error {
		if _, err := fullProjectByID(data, input.ProjectID); err != nil {
			return err
		}
		if _, err := fullProjectByID(data, input.RelatedProjectID); err != nil {
			return err
		}
		if slices.ContainsFunc(data.ProjectRelations, func(item domain.ProjectRelation) bool {
			return item.ProjectID == input.ProjectID && item.RelatedProjectID == input.RelatedProjectID && item.Type == input.Type
		}) {
			return errConflict
		}
		now := time.Now().UTC()
		input.ID = parityID("project_relation")
		input.CreatedAt = now
		input.UpdatedAt = now
		data.ProjectRelations = append(data.ProjectRelations, input)
		created = input
		appendAudit(data, "relation_created", "project", input.ProjectID, map[string]any{"relationId": input.ID, "relatedProjectId": input.RelatedProjectID, "type": input.Type})
		return nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}
func (s *server) updateProjectRelation(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Type string `json:"type"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if !slices.Contains([]string{"related", "blocks", "blocked_by", "dependency"}, input.Type) {
		writeError(w, http.StatusBadRequest, "invalid project relation type")
		return
	}
	var updated domain.ProjectRelation
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "project.relation_updated", r.PathValue("relationId"), input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.ProjectRelations, func(item domain.ProjectRelation) bool {
			return item.ID == r.PathValue("relationId") && item.ProjectID == r.PathValue("id")
		})
		if index < 0 {
			return errNotFound
		}
		data.ProjectRelations[index].Type = input.Type
		data.ProjectRelations[index].UpdatedAt = time.Now().UTC()
		updated = data.ProjectRelations[index]
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}
func (s *server) deleteProjectRelation(w http.ResponseWriter, r *http.Request) {
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "project.relation_deleted", r.PathValue("relationId"), nil, func(data *domain.Bootstrap) error {
		before := len(data.ProjectRelations)
		data.ProjectRelations = slices.DeleteFunc(data.ProjectRelations, func(item domain.ProjectRelation) bool {
			return item.ID == r.PathValue("relationId") && item.ProjectID == r.PathValue("id")
		})
		if before == len(data.ProjectRelations) {
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
func (s *server) projectHistory(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	id := r.PathValue("id")
	items := slices.DeleteFunc(slices.Clone(data.AuditLog), func(item domain.AuditLogEntry) bool { return item.ResourceType != "project" || item.ResourceID != id })
	writePage(w, r, items)
}

func (s *server) listInitiativeRelations(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	id := r.PathValue("id")
	items := slices.DeleteFunc(slices.Clone(data.InitiativeRelations), func(item domain.InitiativeRelation) bool {
		return item.InitiativeID != id && item.RelatedInitiativeID != id
	})
	sort.Slice(items, func(i, j int) bool { return items[i].SortOrder < items[j].SortOrder })
	writePage(w, r, items)
}
func (s *server) createInitiativeRelation(w http.ResponseWriter, r *http.Request) {
	var input domain.InitiativeRelation
	if !decodeJSON(w, r, &input) {
		return
	}
	input.InitiativeID = r.PathValue("id")
	if input.RelatedInitiativeID == "" || input.RelatedInitiativeID == input.InitiativeID || !slices.Contains([]string{"related", "parent", "blocks", "blocked_by"}, input.Type) {
		writeError(w, http.StatusBadRequest, "invalid initiative relation")
		return
	}
	var created domain.InitiativeRelation
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "initiative.relation_created", input.InitiativeID, input, func(data *domain.Bootstrap) error {
		if _, err := initiativeByID(data, input.InitiativeID); err != nil {
			return err
		}
		if _, err := initiativeByID(data, input.RelatedInitiativeID); err != nil {
			return err
		}
		if slices.ContainsFunc(data.InitiativeRelations, func(item domain.InitiativeRelation) bool {
			return item.InitiativeID == input.InitiativeID && item.RelatedInitiativeID == input.RelatedInitiativeID && item.Type == input.Type
		}) {
			return errConflict
		}
		now := time.Now().UTC()
		input.ID = parityID("initiative_relation")
		input.CreatedAt = now
		input.UpdatedAt = now
		input.SortOrder = float64(len(data.InitiativeRelations))
		data.InitiativeRelations = append(data.InitiativeRelations, input)
		created = input
		return nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}
func (s *server) updateInitiativeRelation(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Type      *string  `json:"type"`
		SortOrder *float64 `json:"sortOrder"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	var updated domain.InitiativeRelation
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "initiative.relation_updated", r.PathValue("relationId"), input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.InitiativeRelations, func(item domain.InitiativeRelation) bool {
			return item.ID == r.PathValue("relationId") && item.InitiativeID == r.PathValue("id")
		})
		if index < 0 {
			return errNotFound
		}
		if input.Type != nil {
			if !slices.Contains([]string{"related", "parent", "blocks", "blocked_by"}, *input.Type) {
				return errInvalid
			}
			data.InitiativeRelations[index].Type = *input.Type
		}
		if input.SortOrder != nil {
			data.InitiativeRelations[index].SortOrder = *input.SortOrder
		}
		data.InitiativeRelations[index].UpdatedAt = time.Now().UTC()
		updated = data.InitiativeRelations[index]
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}
func (s *server) deleteInitiativeRelation(w http.ResponseWriter, r *http.Request) {
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "initiative.relation_deleted", r.PathValue("relationId"), nil, func(data *domain.Bootstrap) error {
		before := len(data.InitiativeRelations)
		data.InitiativeRelations = slices.DeleteFunc(data.InitiativeRelations, func(item domain.InitiativeRelation) bool {
			return item.ID == r.PathValue("relationId") && item.InitiativeID == r.PathValue("id")
		})
		if before == len(data.InitiativeRelations) {
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
func (s *server) initiativeHistory(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	id := r.PathValue("id")
	items := slices.DeleteFunc(slices.Clone(data.AuditLog), func(item domain.AuditLogEntry) bool {
		return item.ResourceType != "initiative" || item.ResourceID != id
	})
	writePage(w, r, items)
}

func (s *server) listDocumentDrafts(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	id := r.PathValue("id")
	document, err := documentByID(&data, id)
	if err != nil || documentRole(s, data, *document) == "none" {
		writeError(w, http.StatusNotFound, "document not found")
		return
	}
	items := slices.DeleteFunc(slices.Clone(data.DocumentContentDrafts), func(item domain.DocumentContentDraft) bool {
		return item.DocumentID != id || (!s.authDisabled && item.UserID != data.Viewer.ID)
	})
	writePage(w, r, items)
}
func (s *server) createDocumentDraft(w http.ResponseWriter, r *http.Request) {
	var input domain.DocumentContentDraft
	if !decodeJSON(w, r, &input) {
		return
	}
	var result domain.DocumentContentDraft
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "document.draft_created", r.PathValue("id"), nil, func(data *domain.Bootstrap) error {
		document, err := documentByID(data, r.PathValue("id"))
		if err != nil {
			return err
		}
		if !canEditDocument(documentRole(s, *data, *document)) {
			return store.ErrAuthForbidden
		}
		index := slices.IndexFunc(data.DocumentContentDrafts, func(item domain.DocumentContentDraft) bool {
			return item.DocumentID == r.PathValue("id") && item.UserID == data.Viewer.ID
		})
		now := time.Now().UTC()
		if index >= 0 {
			draft := &data.DocumentContentDrafts[index]
			draft.Content = input.Content
			draft.ContentState = input.ContentState
			draft.ContentData = input.ContentData
			draft.Version++
			draft.UpdatedAt = now
			result = *draft
			return nil
		}
		input.ID = parityID("document_draft")
		input.DocumentID = r.PathValue("id")
		input.UserID = data.Viewer.ID
		input.Version = 1
		input.CreatedAt = now
		input.UpdatedAt = now
		data.DocumentContentDrafts = append(data.DocumentContentDrafts, input)
		result = input
		return nil
	})
	respondMutation(w, err, http.StatusCreated, result)
}
func (s *server) updateDocumentDraft(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Content         *string        `json:"content"`
		ContentState    *string        `json:"contentState"`
		ContentData     map[string]any `json:"contentData"`
		ExpectedVersion *int64         `json:"expectedVersion"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	var result domain.DocumentContentDraft
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "document.draft_updated", r.PathValue("draftId"), input, func(data *domain.Bootstrap) error {
		document, docErr := documentByID(data, r.PathValue("id"))
		if docErr != nil {
			return docErr
		}
		if !canEditDocument(documentRole(s, *data, *document)) {
			return store.ErrAuthForbidden
		}
		index := slices.IndexFunc(data.DocumentContentDrafts, func(item domain.DocumentContentDraft) bool {
			return item.ID == r.PathValue("draftId") && item.DocumentID == r.PathValue("id") && item.UserID == data.Viewer.ID
		})
		if index < 0 {
			return errNotFound
		}
		draft := &data.DocumentContentDrafts[index]
		if input.ExpectedVersion != nil && *input.ExpectedVersion != draft.Version {
			return errConflict
		}
		if input.Content != nil {
			draft.Content = *input.Content
		}
		if input.ContentState != nil {
			draft.ContentState = *input.ContentState
		}
		if input.ContentData != nil {
			draft.ContentData = input.ContentData
		}
		draft.Version++
		draft.UpdatedAt = time.Now().UTC()
		result = *draft
		return nil
	})
	respondMutation(w, err, http.StatusOK, result)
}
func (s *server) publishDocumentDraft(w http.ResponseWriter, r *http.Request) {
	var result domain.Document
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "document.draft_published", r.PathValue("draftId"), nil, func(data *domain.Bootstrap) error {
		document, docErr := documentByID(data, r.PathValue("id"))
		if docErr != nil {
			return docErr
		}
		if !canEditDocument(documentRole(s, *data, *document)) {
			return store.ErrAuthForbidden
		}
		index := slices.IndexFunc(data.DocumentContentDrafts, func(item domain.DocumentContentDraft) bool {
			return item.ID == r.PathValue("draftId") && item.DocumentID == r.PathValue("id") && item.UserID == data.Viewer.ID
		})
		if index < 0 {
			return errNotFound
		}
		document, err := documentByID(data, r.PathValue("id"))
		if err != nil {
			return err
		}
		draft := data.DocumentContentDrafts[index]
		saveDocumentRevision(document, data.Viewer)
		document.Content = draft.Content
		document.ContentState = draft.ContentState
		document.ContentData = draft.ContentData
		document.UpdatedAt = time.Now().UTC()
		data.DocumentContentDrafts = slices.Delete(data.DocumentContentDrafts, index, index+1)
		result = *document
		return nil
	})
	respondMutation(w, err, http.StatusOK, result)
}
func (s *server) deleteDocumentDraft(w http.ResponseWriter, r *http.Request) {
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "document.draft_deleted", r.PathValue("draftId"), nil, func(data *domain.Bootstrap) error {
		document, docErr := documentByID(data, r.PathValue("id"))
		if docErr != nil {
			return docErr
		}
		if !canEditDocument(documentRole(s, *data, *document)) {
			return store.ErrAuthForbidden
		}
		before := len(data.DocumentContentDrafts)
		data.DocumentContentDrafts = slices.DeleteFunc(data.DocumentContentDrafts, func(item domain.DocumentContentDraft) bool {
			return item.ID == r.PathValue("draftId") && item.UserID == data.Viewer.ID
		})
		if before == len(data.DocumentContentDrafts) {
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
func (s *server) documentContentHistory(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	document, err := documentByID(&data, r.PathValue("id"))
	if err != nil || documentRole(s, data, *document) == "none" {
		writeError(w, http.StatusNotFound, "document not found")
		return
	}
	writePage(w, r, document.Revisions)
}

func (s *server) listCustomerTaxonomy(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	writeJSON(w, http.StatusOK, map[string]any{"statuses": data.CustomerStatuses, "tiers": data.CustomerTiers})
}
func customerTaxonomyKind(r *http.Request) string {
	if strings.Contains(r.URL.Path, "customer-tiers") {
		return "tier"
	}
	return "status"
}
func (s *server) createCustomerTaxonomy(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if !decodeJSON(w, r, &input) || strings.TrimSpace(input.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	kind := customerTaxonomyKind(r)
	var result any
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "customer_taxonomy.created", kind, input, func(data *domain.Bootstrap) error {
		now := time.Now().UTC()
		if kind == "tier" {
			item := domain.CustomerTier{ID: parityID("customer_tier"), Name: strings.TrimSpace(input.Name), Color: input.Color, Position: float64(len(data.CustomerTiers)), CreatedAt: now, UpdatedAt: now}
			data.CustomerTiers = append(data.CustomerTiers, item)
			result = item
		} else {
			item := domain.CustomerStatus{ID: parityID("customer_status"), Name: strings.TrimSpace(input.Name), Color: input.Color, Position: float64(len(data.CustomerStatuses)), CreatedAt: now, UpdatedAt: now}
			data.CustomerStatuses = append(data.CustomerStatuses, item)
			result = item
		}
		return nil
	})
	respondMutation(w, err, http.StatusCreated, result)
}
func (s *server) updateCustomerTaxonomy(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name     *string  `json:"name"`
		Color    *string  `json:"color"`
		Position *float64 `json:"position"`
		Archived *bool    `json:"archived"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	kind := customerTaxonomyKind(r)
	var result any
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "customer_taxonomy.updated", r.PathValue("id"), input, func(data *domain.Bootstrap) error {
		now := time.Now().UTC()
		if kind == "tier" {
			index := slices.IndexFunc(data.CustomerTiers, func(item domain.CustomerTier) bool { return item.ID == r.PathValue("id") })
			if index < 0 {
				return errNotFound
			}
			item := &data.CustomerTiers[index]
			if input.Name != nil {
				item.Name = strings.TrimSpace(*input.Name)
			}
			if input.Color != nil {
				item.Color = *input.Color
			}
			if input.Position != nil {
				item.Position = *input.Position
			}
			if input.Archived != nil {
				if *input.Archived {
					item.ArchivedAt = &now
				} else {
					item.ArchivedAt = nil
				}
			}
			item.UpdatedAt = now
			result = *item
		} else {
			index := slices.IndexFunc(data.CustomerStatuses, func(item domain.CustomerStatus) bool { return item.ID == r.PathValue("id") })
			if index < 0 {
				return errNotFound
			}
			item := &data.CustomerStatuses[index]
			if input.Name != nil {
				item.Name = strings.TrimSpace(*input.Name)
			}
			if input.Color != nil {
				item.Color = *input.Color
			}
			if input.Position != nil {
				item.Position = *input.Position
			}
			if input.Archived != nil {
				if *input.Archived {
					item.ArchivedAt = &now
				} else {
					item.ArchivedAt = nil
				}
			}
			item.UpdatedAt = now
			result = *item
		}
		return nil
	})
	respondMutation(w, err, http.StatusOK, result)
}
func (s *server) deleteCustomerTaxonomy(w http.ResponseWriter, r *http.Request) {
	kind := customerTaxonomyKind(r)
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "customer_taxonomy.deleted", r.PathValue("id"), nil, func(data *domain.Bootstrap) error {
		if kind == "tier" {
			before := len(data.CustomerTiers)
			data.CustomerTiers = slices.DeleteFunc(data.CustomerTiers, func(item domain.CustomerTier) bool { return item.ID == r.PathValue("id") })
			if before == len(data.CustomerTiers) {
				return errNotFound
			}
		} else {
			before := len(data.CustomerStatuses)
			data.CustomerStatuses = slices.DeleteFunc(data.CustomerStatuses, func(item domain.CustomerStatus) bool { return item.ID == r.PathValue("id") })
			if before == len(data.CustomerStatuses) {
				return errNotFound
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
func (s *server) archiveCustomerNeed(w http.ResponseWriter, r *http.Request) {
	archived := r.Method == http.MethodPost
	var result domain.CustomerRequest
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "customer_need.archive_toggled", r.PathValue("id"), nil, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.CustomerRequests, func(item domain.CustomerRequest) bool { return item.ID == r.PathValue("id") })
		if index < 0 {
			return errNotFound
		}
		if archived {
			now := time.Now().UTC()
			data.CustomerRequests[index].ArchivedAt = &now
		} else {
			data.CustomerRequests[index].ArchivedAt = nil
		}
		data.CustomerRequests[index].UpdatedAt = time.Now().UTC()
		result = data.CustomerRequests[index]
		return nil
	})
	respondMutation(w, err, http.StatusOK, result)
}

func (s *server) listReleaseNotes(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	items := slices.DeleteFunc(slices.Clone(data.ReleaseNotes), func(item domain.ReleaseNote) bool { return item.ReleaseID != r.PathValue("id") })
	writePage(w, r, items)
}
func (s *server) createReleaseNote(w http.ResponseWriter, r *http.Request) {
	var input domain.ReleaseNote
	if !decodeJSON(w, r, &input) {
		return
	}
	var result domain.ReleaseNote
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "release.note_created", r.PathValue("id"), input, func(data *domain.Bootstrap) error {
		if !slices.ContainsFunc(data.Releases, func(item domain.Release) bool { return item.ID == r.PathValue("id") }) {
			return errNotFound
		}
		now := time.Now().UTC()
		input.ID = parityID("release_note")
		input.ReleaseID = r.PathValue("id")
		input.Creator = data.Viewer
		input.CreatedAt = now
		input.UpdatedAt = now
		data.ReleaseNotes = append(data.ReleaseNotes, input)
		data.ReleaseHistory = append(data.ReleaseHistory, domain.ReleaseHistory{ID: parityID("release_history"), ReleaseID: input.ReleaseID, Actor: data.Viewer, Action: "note_created", Metadata: map[string]any{"noteId": input.ID}, CreatedAt: now})
		result = input
		return nil
	})
	respondMutation(w, err, http.StatusCreated, result)
}
func (s *server) updateReleaseNote(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Title     *string        `json:"title"`
		Body      *string        `json:"body"`
		BodyData  map[string]any `json:"bodyData"`
		Published *bool          `json:"published"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	var result domain.ReleaseNote
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "release.note_updated", r.PathValue("noteId"), input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.ReleaseNotes, func(item domain.ReleaseNote) bool {
			return item.ID == r.PathValue("noteId") && item.ReleaseID == r.PathValue("id")
		})
		if index < 0 {
			return errNotFound
		}
		item := &data.ReleaseNotes[index]
		if input.Title != nil {
			item.Title = *input.Title
		}
		if input.Body != nil {
			item.Body = *input.Body
		}
		if input.BodyData != nil {
			item.BodyData = input.BodyData
		}
		now := time.Now().UTC()
		if input.Published != nil {
			if *input.Published {
				item.PublishedAt = &now
			} else {
				item.PublishedAt = nil
			}
		}
		item.UpdatedAt = now
		result = *item
		return nil
	})
	respondMutation(w, err, http.StatusOK, result)
}
func (s *server) deleteReleaseNote(w http.ResponseWriter, r *http.Request) {
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "release.note_deleted", r.PathValue("noteId"), nil, func(data *domain.Bootstrap) error {
		before := len(data.ReleaseNotes)
		data.ReleaseNotes = slices.DeleteFunc(data.ReleaseNotes, func(item domain.ReleaseNote) bool {
			return item.ID == r.PathValue("noteId") && item.ReleaseID == r.PathValue("id")
		})
		if before == len(data.ReleaseNotes) {
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
func (s *server) releaseHistory(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	items := slices.DeleteFunc(slices.Clone(data.ReleaseHistory), func(item domain.ReleaseHistory) bool { return item.ReleaseID != r.PathValue("id") })
	writePage(w, r, items)
}

func (s *server) listTeamResources(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	teamID := r.PathValue("id")
	sections := slices.DeleteFunc(slices.Clone(data.TeamResourceSections), func(item domain.TeamResourceSection) bool { return item.TeamID != teamID })
	resources := slices.DeleteFunc(slices.Clone(data.TeamPinnedResources), func(item domain.TeamPinnedResource) bool { return item.TeamID != teamID })
	sort.Slice(sections, func(i, j int) bool { return sections[i].Position < sections[j].Position })
	sort.Slice(resources, func(i, j int) bool { return resources[i].Position < resources[j].Position })
	writeJSON(w, http.StatusOK, map[string]any{"sections": sections, "resources": resources})
}
func (s *server) createTeamResourceSection(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &input) || strings.TrimSpace(input.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	var item domain.TeamResourceSection
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "team.resource_section_created", r.PathValue("id"), input, func(data *domain.Bootstrap) error {
		if !validateResourceIDs(data, "team", []string{r.PathValue("id")}) {
			return errNotFound
		}
		now := time.Now().UTC()
		item = domain.TeamResourceSection{ID: parityID("team_resource_section"), TeamID: r.PathValue("id"), Name: strings.TrimSpace(input.Name), Position: float64(len(data.TeamResourceSections)), CreatedAt: now, UpdatedAt: now}
		data.TeamResourceSections = append(data.TeamResourceSections, item)
		return nil
	})
	respondMutation(w, err, http.StatusCreated, item)
}
func (s *server) updateTeamResourceSection(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name     *string  `json:"name"`
		Position *float64 `json:"position"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	var item domain.TeamResourceSection
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "team.resource_section_updated", r.PathValue("sectionId"), input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.TeamResourceSections, func(value domain.TeamResourceSection) bool {
			return value.ID == r.PathValue("sectionId") && value.TeamID == r.PathValue("id")
		})
		if index < 0 {
			return errNotFound
		}
		current := &data.TeamResourceSections[index]
		if input.Name != nil {
			name := strings.TrimSpace(*input.Name)
			if name == "" {
				return errInvalid
			}
			current.Name = name
		}
		if input.Position != nil {
			current.Position = *input.Position
		}
		current.UpdatedAt = time.Now().UTC()
		item = *current
		return nil
	})
	respondMutation(w, err, http.StatusOK, item)
}
func (s *server) deleteTeamResourceSection(w http.ResponseWriter, r *http.Request) {
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "team.resource_section_deleted", r.PathValue("sectionId"), nil, func(data *domain.Bootstrap) error {
		before := len(data.TeamResourceSections)
		data.TeamResourceSections = slices.DeleteFunc(data.TeamResourceSections, func(item domain.TeamResourceSection) bool {
			return item.ID == r.PathValue("sectionId") && item.TeamID == r.PathValue("id")
		})
		if len(data.TeamResourceSections) == before {
			return errNotFound
		}
		for index := range data.TeamPinnedResources {
			if data.TeamPinnedResources[index].TeamID == r.PathValue("id") && data.TeamPinnedResources[index].SectionID == r.PathValue("sectionId") {
				data.TeamPinnedResources[index].SectionID = ""
				data.TeamPinnedResources[index].UpdatedAt = time.Now().UTC()
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
func (s *server) createTeamPinnedResource(w http.ResponseWriter, r *http.Request) {
	var input domain.TeamPinnedResource
	if !decodeJSON(w, r, &input) {
		return
	}
	var item domain.TeamPinnedResource
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "team.resource_pinned", r.PathValue("id"), input, func(data *domain.Bootstrap) error {
		if input.Title == "" || input.ResourceType == "" {
			return errInvalid
		}
		if input.SectionID != "" && !slices.ContainsFunc(data.TeamResourceSections, func(section domain.TeamResourceSection) bool {
			return section.ID == input.SectionID && section.TeamID == r.PathValue("id")
		}) {
			return errInvalid
		}
		if input.ResourceID != "" && !validateResourceIDs(data, input.ResourceType, []string{input.ResourceID}) {
			return errInvalid
		}
		now := time.Now().UTC()
		input.ID = parityID("team_resource")
		input.TeamID = r.PathValue("id")
		input.Position = float64(len(data.TeamPinnedResources))
		input.CreatedAt = now
		input.UpdatedAt = now
		data.TeamPinnedResources = append(data.TeamPinnedResources, input)
		item = input
		return nil
	})
	respondMutation(w, err, http.StatusCreated, item)
}
func (s *server) updateTeamResource(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Title     *string  `json:"title"`
		SectionID *string  `json:"sectionId"`
		Position  *float64 `json:"position"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	var item domain.TeamPinnedResource
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "team.resource_updated", r.PathValue("resourceId"), input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.TeamPinnedResources, func(value domain.TeamPinnedResource) bool {
			return value.ID == r.PathValue("resourceId") && value.TeamID == r.PathValue("id")
		})
		if index < 0 {
			return errNotFound
		}
		current := &data.TeamPinnedResources[index]
		if input.Title != nil {
			current.Title = *input.Title
		}
		if input.SectionID != nil {
			current.SectionID = *input.SectionID
		}
		if input.Position != nil {
			current.Position = *input.Position
		}
		current.UpdatedAt = time.Now().UTC()
		item = *current
		return nil
	})
	respondMutation(w, err, http.StatusOK, item)
}
func (s *server) deleteTeamResource(w http.ResponseWriter, r *http.Request) {
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "team.resource_deleted", r.PathValue("resourceId"), nil, func(data *domain.Bootstrap) error {
		before := len(data.TeamPinnedResources)
		data.TeamPinnedResources = slices.DeleteFunc(data.TeamPinnedResources, func(item domain.TeamPinnedResource) bool {
			return item.ID == r.PathValue("resourceId") && item.TeamID == r.PathValue("id")
		})
		if before == len(data.TeamPinnedResources) {
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

func (s *server) listAgentActivities(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	items := slices.Clone(data.AgentActivities)
	if session := r.URL.Query().Get("sessionId"); session != "" {
		items = slices.DeleteFunc(items, func(item domain.AgentActivity) bool { return item.SessionID != session })
	}
	writePage(w, r, items)
}
func (s *server) createAgentActivity(w http.ResponseWriter, r *http.Request) {
	var input domain.AgentActivity
	if !decodeJSON(w, r, &input) {
		return
	}
	var item domain.AgentActivity
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "agent.activity_created", input.SessionID, input, func(data *domain.Bootstrap) error {
		if input.SessionID == "" || input.Type == "" {
			return errInvalid
		}
		now := time.Now().UTC()
		input.ID = parityID("agent_activity")
		input.Status = "running"
		input.CreatedAt = now
		input.UpdatedAt = now
		data.AgentActivities = append(data.AgentActivities, input)
		item = input
		return nil
	})
	respondMutation(w, err, http.StatusCreated, item)
}
func (s *server) updateAgentActivity(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Status   *string        `json:"status"`
		Body     *string        `json:"body"`
		Metadata map[string]any `json:"metadata"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	var item domain.AgentActivity
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "agent.activity_updated", r.PathValue("id"), input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.AgentActivities, func(value domain.AgentActivity) bool { return value.ID == r.PathValue("id") })
		if index < 0 {
			return errNotFound
		}
		current := &data.AgentActivities[index]
		if input.Status != nil {
			current.Status = *input.Status
		}
		if input.Body != nil {
			current.Body = *input.Body
		}
		if input.Metadata != nil {
			current.Metadata = input.Metadata
		}
		current.UpdatedAt = time.Now().UTC()
		item = *current
		return nil
	})
	respondMutation(w, err, http.StatusOK, item)
}
func (s *server) listAIConversations(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	items := slices.DeleteFunc(slices.Clone(data.AIConversations), func(item domain.AIConversation) bool { return !s.authDisabled && item.UserID != data.Viewer.ID })
	writePage(w, r, items)
}
func (s *server) createAIConversation(w http.ResponseWriter, r *http.Request) {
	var input domain.AIConversation
	if !decodeJSON(w, r, &input) {
		return
	}
	var item domain.AIConversation
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "ai.conversation_created", "ai", input, func(data *domain.Bootstrap) error {
		now := time.Now().UTC()
		input.ID = parityID("ai_conversation")
		input.UserID = data.Viewer.ID
		input.Status = "active"
		input.CreatedAt = now
		input.UpdatedAt = now
		data.AIConversations = append(data.AIConversations, input)
		item = input
		return nil
	})
	respondMutation(w, err, http.StatusCreated, item)
}
func (s *server) updateAIConversation(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Title   *string        `json:"title"`
		Status  *string        `json:"status"`
		Context map[string]any `json:"context"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	var item domain.AIConversation
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "ai.conversation_updated", r.PathValue("id"), input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.AIConversations, func(value domain.AIConversation) bool {
			return value.ID == r.PathValue("id") && value.UserID == data.Viewer.ID
		})
		if index < 0 {
			return errNotFound
		}
		current := &data.AIConversations[index]
		if input.Title != nil {
			current.Title = *input.Title
		}
		if input.Status != nil {
			current.Status = *input.Status
		}
		if input.Context != nil {
			current.Context = input.Context
		}
		current.UpdatedAt = time.Now().UTC()
		item = *current
		return nil
	})
	respondMutation(w, err, http.StatusOK, item)
}
func (s *server) createAIPromptProgress(w http.ResponseWriter, r *http.Request) {
	var input domain.AIPromptProgress
	if !decodeJSON(w, r, &input) {
		return
	}
	var item domain.AIPromptProgress
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "ai.prompt_progress_created", input.ConversationID, input, func(data *domain.Bootstrap) error {
		if !slices.ContainsFunc(data.AIConversations, func(value domain.AIConversation) bool {
			return value.ID == input.ConversationID && value.UserID == data.Viewer.ID
		}) {
			return errNotFound
		}
		now := time.Now().UTC()
		input.ID = parityID("ai_progress")
		input.Progress = max(0, min(100, input.Progress))
		input.CreatedAt = now
		input.UpdatedAt = now
		data.AIPromptProgress = append(data.AIPromptProgress, input)
		item = input
		return nil
	})
	respondMutation(w, err, http.StatusCreated, item)
}
func (s *server) listUsageAlerts(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	writePage(w, r, data.UsageAlerts)
}
func (s *server) upsertUsageAlert(w http.ResponseWriter, r *http.Request) {
	var input domain.UsageAlert
	if !decodeJSON(w, r, &input) || input.Type == "" {
		writeError(w, http.StatusBadRequest, "alert type is required")
		return
	}
	var item domain.UsageAlert
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "usage.alert_upserted", input.Type, input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.UsageAlerts, func(value domain.UsageAlert) bool { return value.Type == input.Type && value.ArchivedAt == nil })
		now := time.Now().UTC()
		input.Status = "active"
		if index >= 0 {
			input.ID = data.UsageAlerts[index].ID
			input.CreatedAt = data.UsageAlerts[index].CreatedAt
			data.UsageAlerts[index] = input
		} else {
			input.ID = parityID("usage_alert")
			input.CreatedAt = now
			data.UsageAlerts = append(data.UsageAlerts, input)
		}
		item = input
		return nil
	})
	respondMutation(w, err, http.StatusOK, item)
}
func (s *server) paidSubscription(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	if len(data.PaidSubscriptions) == 0 {
		writeJSON(w, http.StatusOK, nil)
		return
	}
	writeJSON(w, http.StatusOK, data.PaidSubscriptions[0])
}

func (s *server) syncUsageAlerts(r *http.Request, snapshot domain.Bootstrap, storage int64, limits map[string]int64) {
	values := map[string]struct{ current, threshold int64 }{"issues": {int64(len(snapshot.Issues)), limits["issues"]}, "storage": {storage, limits["storageBytes"]}}
	if snapshot.WorkspaceSettings.AICreditReloadThresholdCents > 0 {
		values["aiCredits"] = struct{ current, threshold int64 }{snapshot.WorkspaceSettings.AICreditBalanceCents, snapshot.WorkspaceSettings.AICreditReloadThresholdCents}
	}
	_ = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "usage.alerts_synced", "usage", nil, func(data *domain.Bootstrap) error {
		now := time.Now().UTC()
		for kind, value := range values {
			breached := value.current >= value.threshold
			if kind == "aiCredits" {
				breached = value.current <= value.threshold
			}
			index := slices.IndexFunc(data.UsageAlerts, func(item domain.UsageAlert) bool {
				return item.Type == kind && item.ArchivedAt == nil && item.ResolvedAt == nil
			})
			if breached && index < 0 {
				alert := domain.UsageAlert{ID: parityID("usage_alert"), Type: kind, Threshold: value.threshold, Current: value.current, Status: "active", CreatedAt: now}
				data.UsageAlerts = append(data.UsageAlerts, alert)
				data.Notifications = append(data.Notifications, domain.Notification{ID: parityID("notification_usage"), RecipientID: data.Viewer.ID, Type: "usageAlert", SourceType: "usageAlert", SourceID: alert.ID, Actor: data.Viewer, Category: "billing", GroupKey: "usage:" + kind, OccurrenceCount: 1, LatestActorIDs: []string{data.Viewer.ID}, CreatedAt: now, UpdatedAt: now})
			} else if breached && index >= 0 {
				data.UsageAlerts[index].Current = value.current
			} else if !breached && index >= 0 {
				data.UsageAlerts[index].Status = "resolved"
				data.UsageAlerts[index].ResolvedAt = &now
			}
		}
		return nil
	})
}
