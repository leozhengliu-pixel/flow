package main

import (
	"net/http"
	"slices"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func (s *server) createDraftWithStore(w http.ResponseWriter, r *http.Request, repo *store.SQLiteStore, input draftInput) {
	validate, err := s.draftRecordValidator(r, repo)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	if input.ClientKey != nil && len(*input.ClientKey) > 200 {
		writeError(w, http.StatusBadRequest, "draft client key is too long")
		return
	}
	var created domain.Draft
	status := http.StatusCreated
	err = repo.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "draft.created", input, func(data *domain.Bootstrap) (string, error) {
		now := time.Now().UTC()
		kind := "issue"
		if input.Type != nil && *input.Type != "" {
			kind = *input.Type
		}
		created = domain.Draft{ID: idgen.New("draft_"), UserID: data.Viewer.ID, Type: kind, ContentData: input.ContentData, Metadata: input.Metadata, CreatedAt: now, UpdatedAt: now}
		applyDraftInput(&created, input)
		if err := validate(data, created); err != nil {
			return "", err
		}
		// A create repeated with the key of a draft it already made (a retry whose answer was lost) is that draft again.
		if index := draftWithClientKey(data.Drafts, created); index >= 0 {
			status = http.StatusOK
			candidate := data.Drafts[index]
			applyDraftInput(&candidate, input)
			if unchangedEdit(data.Drafts[index], candidate) {
				created = data.Drafts[index]
				return "", store.ErrNoMutation
			}
			candidate.UpdatedAt = now
			data.Drafts[index], created = candidate, candidate
			return created.ID, nil
		}
		data.Drafts = append([]domain.Draft{created}, data.Drafts...)
		return created.ID, nil
	})
	respondMutation(w, err, status, created)
}

func (s *server) updateDraftWithStore(w http.ResponseWriter, r *http.Request, repo *store.SQLiteStore, input draftInput) {
	validate, err := s.draftRecordValidator(r, repo)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	id := r.PathValue("id")
	var updated domain.Draft
	err = repo.MutateWorkspace(r.Context(), workspaceKey(r), "draft.updated", id, input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.Drafts, func(item domain.Draft) bool { return item.ID == id && item.UserID == data.Viewer.ID })
		if index < 0 {
			return errNotFound
		}
		candidate := data.Drafts[index]
		applyDraftInput(&candidate, input)
		if err := validate(data, candidate); err != nil {
			return err
		}
		if unchangedEdit(data.Drafts[index], candidate) {
			updated = data.Drafts[index]
			return store.ErrNoMutation
		}
		candidate.UpdatedAt = time.Now().UTC()
		data.Drafts[index], updated = candidate, candidate
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}
