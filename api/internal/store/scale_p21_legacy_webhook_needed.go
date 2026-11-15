package store

import "context"

// boundedLegacyWebhookNeeded samples the explicit installed generation before
// the legacy writer waits or applies its callback. Request pins are unrelated
// to that decision and must not change it.
func (s *SQLiteStore) boundedLegacyWebhookNeeded(ctx context.Context, workspace string) (bool, error) {
	if !s.BoundedMetadataEnabled() {
		return s.webhookNeeded(ctx, workspace), nil
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	ctx = context.WithValue(metadataLogicalUnpinnedContext(ctx), metadataWorkLeaseKey{}, nil)
	owner := &ResourceRead{store: s}
	defer owner.Close()
	s.mu.RLock()
	_, found := s.workspaces[workspace]
	s.metadataResidency.mu.Lock()
	source := s.metadataResidency.logical[workspace]
	var err error
	if found {
		if source == nil {
			err = ErrBoundedMetadataCoverage
		} else {
			err = source.Retain()
			if err == nil {
				owner.source = source
			}
		}
	}
	s.metadataResidency.mu.Unlock()
	s.mu.RUnlock()
	if err != nil {
		return false, err
	}
	if !found {
		needed := s.chatSnapshotFlag(ctx, workspace)
		return needed, ctx.Err()
	}
	if err := owner.reserve(ctx, 128<<10); err != nil {
		return false, err
	}
	needed, err := s.logicalWebhookNeeded(ctx, owner.source, workspace)
	if err != nil {
		return false, err
	}
	return needed, ctx.Err()
}
