package main

import (
	"net/http"
	"slices"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func (s *server) updatePortfolioProject(w http.ResponseWriter, r *http.Request) {
	if !s.store.BoundedMetadataEnabled() {
		s.updateProject(w, r)
		return
	}
	var input domain.ProjectMutationInput
	if !decodeJSON(w, r, &input) {
		return
	}
	id, workspace := r.PathValue("id"), workspaceKey(r)
	var updated domain.Project
	mutate := projectEditCallback(id, input, &updated)
	scopeInput := input
	scopeInput.Archived = nil // Suggestion cleanup runs after the project commit.
	scope, simple := boundedProjectEditScope(id, scopeInput)
	var err error
	if simple && !slices.Contains(scope.Fields, "projects") {
		ctx := store.WithScopedSelectionWrite(r.Context())
		err = s.store.MutateWorkspaceScoped(ctx, workspace, "project.updated", id, input, scope, mutate)
	} else {
		ctx, closeOutputs := beginMetadataOutputReads(r.Context())
		defer closeOutputs()
		err = s.store.UpdatePortfolioProject(ctx, workspace, id, input, mutate)
	}
	if err == nil && input.Archived != nil && *input.Archived {
		s.pruneIssueSuggestionTargets(r.Context(), workspace, func(item domain.IssueSuggestion) bool {
			return item.Type == "project" && item.SuggestedProjectID == id
		})
	}
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) createPortfolioDocument(w http.ResponseWriter, r *http.Request) {
	if !s.store.BoundedMetadataEnabled() {
		s.createDocument(w, r)
		return
	}
	var input documentInput
	if !s.decodeDocumentBody(w, r, &input) {
		return
	}
	var created domain.Document
	var viewer templateAccess
	var selection store.PortfolioDocumentCreate
	if input.TemplateID != nil && *input.TemplateID != "" {
		var err error
		viewer, err = s.requestTemplateAccess(r)
		if err != nil {
			respondMutation(w, err, http.StatusCreated, nil)
			return
		}
		selection.TemplateID = *input.TemplateID
	}
	selection.IssueIDs = scopeIDs(input.IssueID)
	if input.ProjectIDs != nil {
		selection.ProjectIDs = normalizedStrings(*input.ProjectIDs)
	}
	if input.SubscriberIDs != nil {
		selection.SubscriberIDs = *input.SubscriberIDs
	}
	ctx, closeOutputs := beginMetadataOutputReads(r.Context())
	defer closeOutputs()
	err := s.store.CreatePortfolioDocument(ctx, workspaceKey(r), selection, input, documentCreateCallback(input, viewer, &created))
	respondMutation(w, err, http.StatusCreated, created)
}

func (s *server) deletePortfolioDocument(w http.ResponseWriter, r *http.Request) {
	if !s.store.BoundedMetadataEnabled() {
		s.deleteLegacyDocument(w, r)
		return
	}
	id := r.PathValue("id")
	workspace, removedID := workspaceKey(r), id
	directory := s.documentMemberDirectory(r)
	ctx, closeOutputs := beginMetadataOutputReads(r.Context())
	defer closeOutputs()
	err := s.store.DeletePortfolioDocument(ctx, workspace, id, func(data *domain.Bootstrap, resolvedID string) error {
		return s.documentDeleteCallback(resolvedID, directory, &workspace, &removedID)(data)
	})
	s.finishDocumentDelete(w, r, err, workspace, removedID)
}
