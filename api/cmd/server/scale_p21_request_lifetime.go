package main

import (
	"context"
	"errors"
	"net/http"

	"flow/api/internal/domain"
)

// withMetadataLifetime is for finite requests only. Streaming routes use an
// explicit lifetime for each bounded batch instead of their whole connection.
func (s *server) withMetadataLifetime(next http.Handler) http.Handler {
	if !s.store.BoundedMetadataEnabled() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, lifetime, err := s.store.BeginMetadataLifetime(r.Context())
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				writeError(w, http.StatusServiceUnavailable, err.Error())
			}
			return
		}
		defer lifetime.Close()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// A matching handoff is consumed by the unchanged legacy take before any
// current-source read. found remains true on ownership error: never fall back.
func (s *server) takeOwnedPagedMetadata(r *http.Request, workspace, userID string) (domain.Bootstrap, bool, error) {
	return s.takeOwnedMetadata(r, pagedMetadataHandoffKey{}, workspace, userID)
}

func (s *server) takeOwnedIssueWriteMetadata(r *http.Request, workspace, userID string) (domain.Bootstrap, bool, error) {
	return s.takeOwnedMetadata(r, issueWriteMetadataKey{}, workspace, userID)
}

func (s *server) takeOwnedMetadata(r *http.Request, key any, workspace, userID string) (domain.Bootstrap, bool, error) {
	data, found := takeMetadata(r, key, workspace, userID)
	if !found || !s.store.BoundedMetadataEnabled() {
		return data, found, nil
	}
	lifetime, err := s.store.MetadataLifetime(r.Context())
	if err != nil {
		return domain.Bootstrap{}, true, err
	}
	if _, err := lifetime.RetainValue(r.Context(), data); err != nil {
		return domain.Bootstrap{}, true, err
	}
	return data, true, nil
}
