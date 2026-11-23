package main

import (
	"context"
	"errors"
	"net/http"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type savedViewAuthorizationKey struct{}

func bindSavedViewAuthorization(r *http.Request, access store.AccessContext, ref string, data domain.Bootstrap) {
	proof := store.NewSavedViewAuthorization(access, ref, data.SavedViews)
	*r = *r.WithContext(context.WithValue(r.Context(), savedViewAuthorizationKey{}, proof))
}

func (s *server) mutateSavedView(r *http.Request, event, ref string, payload any, mutate func(*domain.Bootstrap) error) error {
	if !s.store.BoundedMetadataEnabled() {
		return s.store.MutateWorkspace(r.Context(), workspaceKey(r), event, ref, payload, mutate)
	}
	return s.mutateSavedViewWithAggregate(r, event, ref, payload, func(data *domain.Bootstrap) (string, error) {
		return ref, mutate(data)
	})
}

func (s *server) mutateSavedViewWithAggregate(r *http.Request, event, ref string, payload any, mutate func(*domain.Bootstrap) (string, error)) error {
	workspace := workspaceKey(r)
	if !s.store.BoundedMetadataEnabled() {
		return s.store.MutateWorkspaceWithAggregate(r.Context(), workspace, event, payload, mutate)
	}
	binding, _ := r.Context().Value(savedViewAuthorizationKey{}).(*store.SavedViewAuthorization)
	for attempt := 0; ; attempt++ {
		err := s.store.MutateSavedViewRecords(r.Context(), workspace, event, ref, payload, binding, mutate)
		if !errors.Is(err, store.ErrResourceAccessChanged) || binding == nil {
			return err
		}
		if attempt >= 2 {
			noteAuthorizationBusy(r, err)
			return store.ErrAuthForbidden
		}
		if _, keyed := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); keyed {
			user, key, selectedWorkspace, err := s.authenticateAPIKey(r)
			noteAuthorizationBusy(r, err)
			if key == nil || user.ID != binding.Access.PrincipalID || selectedWorkspace != workspace {
				return store.ErrAuthForbidden
			}
			*r = *r.WithContext(context.WithValue(r.Context(), apiKeyContextKey{}, *key))
		}
		kind := "savedViews"
		create := event == "view.created"
		if create {
			kind = ""
		}
		decide := s.boundedViewMutationPredicate(r, binding.Access.PrincipalID, binding.Ref, create, func(input *domain.SavedViewMutationInput) bool {
			var ok bool
			*input, ok = payload.(domain.SavedViewMutationInput)
			return ok
		})
		if !s.boundedRecordDecision(r, workspace, binding.Access.PrincipalID, kind, binding.Ref, decide) {
			return store.ErrAuthForbidden
		}
		binding, _ = r.Context().Value(savedViewAuthorizationKey{}).(*store.SavedViewAuthorization)
	}
}

func respondSavedViewMutation(w http.ResponseWriter, r *http.Request, err error, status int, value any) {
	if err != nil && writeProjectionRefusal(w, r) {
		return
	}
	respondMutation(w, err, status, value)
}

func (s *server) sharedViewInputs(r *http.Request, workspace string) (domain.Bootstrap, func(), bool) {
	if !s.store.BoundedMetadataEnabled() {
		data, ok := s.store.BootstrapFor(workspace)
		return data, func() {}, ok
	}
	data, release, err := s.store.BeginSharedSavedViewRead(r.Context(), workspace, r.PathValue("token"))
	if err != nil {
		return domain.Bootstrap{}, func() {}, false
	}
	return data, release, true
}
