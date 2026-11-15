package main

import (
	"context"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func (s *server) requestAccountSettings(ctx context.Context, workspace, userID string) (domain.UserSettings, bool, error) {
	if !s.store.BoundedMetadataEnabled() {
		_, settings, found := s.store.AccountCollections(workspace, userID)
		return settings, found, nil
	}
	read, found, err := s.store.BeginAccountCollectionRead(ctx, workspace, userID, false)
	if err != nil || !found {
		return domain.UserSettings{}, found, err
	}
	if !registerRequestReadClose(ctx, read.Close) {
		read.Close()
		return domain.UserSettings{}, true, store.ErrBoundedMetadataCoverage
	}
	return read.Settings, true, nil
}
