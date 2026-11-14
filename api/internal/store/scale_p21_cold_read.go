package store

import (
	"context"
	"reflect"
	"slices"
	"sync"

	"flow/api/internal/domain"
)

type coldMetadataReadKey struct{}

type coldMetadataRead struct {
	mu     sync.Mutex
	owner  *metadataResidency
	facade *SQLiteStore
}

// withColdMetadataRead lends a legacy read facade only for run's lifetime,
// including projection and encoding. Callbacks must not write, Close the
// facade, or retain its graph. Cold work deliberately has no HOT page/work
// ceiling; one replica-wide slot covers the complete graph and its consumers.
// This pins a callback snapshot; it is not general cold routing that refreshes
// each access after a write. Existing caller snapshot contexts remain intact.
func (s *SQLiteStore) withColdMetadataRead(ctx context.Context, run func(context.Context, *SQLiteStore) error) error {
	if s.metadataResidency == nil {
		return run(ctx, s)
	}
	r := s.metadataResidency
	if nested, ok := ctx.Value(coldMetadataReadKey{}).(*coldMetadataRead); ok && nested.owner == r {
		nested.mu.Lock()
		facade := nested.facade
		nested.mu.Unlock()
		if facade != nil {
			if err := ctx.Err(); err != nil {
				return err
			}
			return run(ctx, facade)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.coldOnce.Do(func() { r.coldSlot = make(chan struct{}, 1) })
	select {
	case r.coldSlot <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-r.coldSlot }()
	if err := ctx.Err(); err != nil {
		return err
	}

	facade, sources, err := s.pinColdMetadataRead(ctx)
	defer func() {
		for _, source := range sources {
			source.Release()
		}
	}()
	if err != nil {
		return err
	}
	defer func() {
		facade.mu.Lock()
		facade.workspaces, facade.viewer = nil, domain.User{}
		facade.lastWorkspaceKey = ""
		facade.mu.Unlock()
	}()
	for workspace, source := range sources {
		var data domain.Bootstrap
		value := reflect.ValueOf(&data).Elem()
		index := 0
		err := source.VisitFields(ctx, func(_ string, node metadataLogicalNode) error {
			// Value's cap is solely an integer representability check here.
			// The cold slot, rather than HOT admission, owns the decoded graph.
			decoded, err := node.Value(ctx, int(^uint(0)>>1))
			if err != nil {
				return err
			}
			value.Field(index).Set(decoded)
			index++
			return nil
		})
		if err != nil {
			return err
		}
		domain.RebuildTeamDirectory(&data)
		facade.workspaces[workspace] = data
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	read := &coldMetadataRead{owner: r, facade: facade}
	defer func() {
		read.mu.Lock()
		read.facade = nil
		read.mu.Unlock()
	}()
	return run(context.WithValue(ctx, coldMetadataReadKey{}, read), facade)
}

// Both registry identity and every logical generation are captured under the
// same owner locks used by installation. A missing source fails closed rather
// than substituting SQL's potentially normalized generation.
func (s *SQLiteStore) pinColdMetadataRead(ctx context.Context) (*SQLiteStore, map[string]*metadataLogicalSource, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r := s.metadataResidency
	r.mu.Lock()
	defer r.mu.Unlock()
	facade := &SQLiteStore{
		db: s.db, dialect: s.dialect,
		workspaces:         make(map[string]domain.Bootstrap, len(s.workspaces)),
		lastWorkspaceKey:   s.lastWorkspaceKey,
		viewer:             s.viewer,
		coordinator:        s.coordinator,
		fixtureProfile:     s.fixtureProfile,
		fixturePassword:    s.fixturePassword,
		maxStateBytes:      s.maxStateBytes,
		lifecycle:          s.lifecycle,
		startupLock:        s.startupLock,
		startupLockTimeout: s.startupLockTimeout,
	}
	// Fresh locks, counters and caches belong to the facade. Service pointers
	// are loaded explicitly; copying SQLiteStore would also copy live locks.
	facade.realtimeSink.Store(s.realtimeSink.Load())
	facade.webhookSink.Store(s.webhookSink.Load())
	facade.accountSink.Store(s.accountSink.Load())
	facade.agentReplies.Store(s.agentReplies.Load())
	facade.keyObjects.Store(s.keyObjects.Load())
	facade.changeLogMinRows.Store(s.changeLogMinRows.Load())
	facade.viewer.AppScopes = slices.Clone(s.viewer.AppScopes)
	facade.viewer.AppTeamIDs = slices.Clone(s.viewer.AppTeamIDs)
	if s.viewer.OutOfOfficeUntil != nil {
		stamp := *s.viewer.OutOfOfficeUntil
		facade.viewer.OutOfOfficeUntil = &stamp
	}
	if s.viewer.Via != nil {
		via := *s.viewer.Via
		facade.viewer.Via = &via
	}
	sources := make(map[string]*metadataLogicalSource, len(s.workspaces))
	for workspace := range s.workspaces {
		if err := ctx.Err(); err != nil {
			return facade, sources, err
		}
		source := r.logical[workspace]
		if source == nil {
			return facade, sources, ErrBoundedMetadataCoverage
		}
		if err := source.Retain(); err != nil {
			return facade, sources, err
		}
		sources[workspace] = source
	}
	return facade, sources, nil
}
