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

// accountCollections is store.AccountCollections that is exact with bounded
// metadata on: AccountCollections reads the installed in-memory image, which
// a bounded store holds without user settings and passkeys, so it answered
// empty settings (GET /api/account/settings, SW530). Bounded, the reviewed
// installed-source reader answers the same values (the workspace's existence,
// the user's settings and, when asked, their passkeys in source order); its
// values are copied out before the read closes. A bounded read that fails
// reports the workspace as missing.
func (s *server) accountCollections(ctx context.Context, workspace, userID string, passkeys bool) ([]domain.Passkey, domain.UserSettings, bool) {
	if !s.store.BoundedMetadataEnabled() {
		return s.store.AccountCollections(workspace, userID)
	}
	read, found, err := s.store.BeginAccountCollectionRead(ctx, workspace, userID, passkeys)
	if err != nil || !found {
		return nil, domain.UserSettings{}, false
	}
	defer read.Close()
	return read.Passkeys, read.Settings, true
}
