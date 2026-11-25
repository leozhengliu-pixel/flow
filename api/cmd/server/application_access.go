package main

import (
	"context"
	"slices"
	"sync"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func applicationOutputContext(ctx context.Context) (context.Context, func()) {
	var mu sync.Mutex
	var releases []func()
	closed := false
	ctx = store.WithMetadataOutputOwner(ctx, func(close func()) bool {
		mu.Lock()
		defer mu.Unlock()
		if closed {
			return false
		}
		releases = append(releases, close)
		return true
	})
	return ctx, func() {
		mu.Lock()
		if closed {
			mu.Unlock()
			return
		}
		closed = true
		owned := releases
		releases = nil
		mu.Unlock()
		for _, close := range owned {
			close()
		}
	}
}

func (s *server) applicationAccessRead(ctx context.Context, userID, projection string) (domain.ApplicationInstallation, func(), error) {
	if !s.store.BoundedMetadataEnabled() {
		app, err := s.store.ApplicationByUser(ctx, userID)
		return app, func() {}, err
	}
	read, err := s.store.BeginApplicationAccessRead(ctx, userID, projection)
	if err != nil {
		return domain.ApplicationInstallation{}, func() {}, err
	}
	return read.Installation, read.Close, nil
}

func (s *server) applicationTaskAccessActive(ctx context.Context, task domain.AgentTask) bool {
	current, close, err := s.applicationAccessRead(ctx, task.AppUserID, "user")
	defer close()
	return err == nil && current.Active && slices.Contains(current.AccessTeamIDs(), task.TeamID)
}
