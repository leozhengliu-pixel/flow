package main

import (
	"context"
	"time"
)

// generateRecurringWorkspaceTick is the ticker's workspace pass. With bounded
// metadata the occurrences it creates stay owned until the pass (and every
// source/sub-issue publication in it) has finished.
func (s *server) generateRecurringWorkspaceTick(ctx context.Context, key string, now time.Time) error {
	if s.store.BoundedMetadataEnabled() {
		var closeOutputs func()
		ctx, closeOutputs = beginIndependentMetadataOutputReads(ctx)
		defer closeOutputs()
	}
	_, err := s.generateWorkspaceRecurringIssues(ctx, key, now)
	return err
}
