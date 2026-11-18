package store

import (
	"context"
	"sync"
	"time"

	"flow/api/internal/domain"
)

// ColdMetadataRead owns the existing cold facade through a containing output
// lifetime. Store and Context are borrowed until Close. Nested handles borrow
// the same state and closing them does not release the outer replica slot.
type ColdMetadataRead struct {
	context     context.Context
	literal     *SQLiteStore
	state       *coldMetadataRead
	releaseSlot func()
	once        sync.Once
}

func (r *ColdMetadataRead) Context() context.Context { return r.context }

func (r *ColdMetadataRead) Store() *SQLiteStore {
	if r == nil {
		return nil
	}
	if r.state == nil {
		return r.literal
	}
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	return r.state.facade
}

func (r *ColdMetadataRead) Close() {
	if r == nil || r.releaseSlot == nil {
		return
	}
	r.once.Do(func() {
		// Preserve the old callback cleanup order: clear native graph,
		// release installed generations, then return the replica slot.
		r.state.mu.Lock()
		facade := r.state.facade
		r.state.mu.Unlock()
		if facade != nil {
			facade.mu.Lock()
			facade.workspaces, facade.viewer = nil, domain.User{}
			facade.lastWorkspaceKey = ""
			facade.coldParent = nil
			facade.mu.Unlock()
		}
		r.state.mu.Lock()
		r.state.facade = nil
		sources := r.state.sources
		retired := r.state.retired
		r.state.sources = nil
		r.state.retired = nil
		r.state.mu.Unlock()
		for _, source := range sources {
			source.Release()
		}
		for _, generation := range retired {
			for _, source := range generation {
				source.Release()
			}
		}
		r.releaseSlot()
	})
}

func (r *ColdMetadataRead) Refresh(ctx context.Context, workspace string) error {
	facade := r.Store()
	if facade == nil {
		return errMetadataLogicalClosed
	}
	return facade.RefreshColdMetadataRead(ctx, workspace)
}

// RefreshColdMetadataRead marks a fresh legacy accessor inside a borrowed
// facade. Callers must consume a Paged handoff first, without refreshing it.
// An ordinary off-mode store keeps its literal accessor behavior and context.
func (s *SQLiteStore) RefreshColdMetadataRead(ctx context.Context, workspace string) error {
	if s.coldParent == nil {
		if s.BoundedMetadataEnabled() {
			return ErrBoundedMetadataCoverage
		}
		return nil
	}
	return s.refreshColdMetadataRead(ctx, workspace)
}

// BeginColdMetadataRead is the owned form of WithColdMetadataRead. Its cold
// registration, installed-copy capture, decoding and nesting are identical;
// only the containing lifetime chooses when cleanup runs.
func (s *SQLiteStore) BeginColdMetadataRead(ctx context.Context, consumer string) (*ColdMetadataRead, error) {
	if s.coldParent != nil {
		return s.coldParent.BeginColdMetadataRead(ctx, consumer)
	}
	if !s.BoundedMetadataEnabled() {
		return &ColdMetadataRead{context: ctx, literal: s}, nil
	}
	// A default COLD request (no registered consumer) borrows its scope's
	// facade; the route table decided it runs COLD.
	if consumer != "" || !InColdScope(ctx) {
		if err := validateColdMetadataDispatch(consumer); err != nil {
			return nil, err
		}
	}
	return s.beginColdMetadataRead(ctx)
}

// beginColdMetadataRead is the single existing pin/decode/cleanup lifecycle,
// factored out of withColdMetadataRead for callers whose output outlives auth.
func (s *SQLiteStore) beginColdMetadataRead(ctx context.Context) (read *ColdMetadataRead, err error) {
	if s.metadataResidency == nil {
		return &ColdMetadataRead{context: ctx, literal: s}, nil
	}
	r := s.metadataResidency
	if nested, ok := ctx.Value(coldMetadataReadKey{}).(*coldMetadataRead); ok && nested.owner == r {
		nested.mu.Lock()
		facade := nested.facade
		nested.mu.Unlock()
		if facade != nil {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return &ColdMetadataRead{context: ctx, state: nested}, nil
		}
	}
	// A COLD scope owns its slot and lends one facade to all its needs.
	if read, selected, err := s.beginScopedColdMetadataRead(ctx); selected || err != nil {
		return read, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.coldOnce.Do(func() { r.coldSlot = make(chan struct{}, 1) })
	select {
	case r.coldSlot <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	coldSlotHolders.Add(1)
	coldSlotAcquisitions.Add(1)
	return s.decodeOwnedColdMetadataRead(ctx, func() {
		coldSlotHolders.Add(-1)
		<-r.coldSlot
	})
}

// decodeOwnedColdMetadataRead pins and decodes a facade for a caller that
// holds the COLD slot; releaseSlot returns it when the read closes (also on
// failure).
func (s *SQLiteStore) decodeOwnedColdMetadataRead(ctx context.Context, releaseSlot func()) (read *ColdMetadataRead, err error) {
	r := s.metadataResidency
	owned := &ColdMetadataRead{
		state:       &coldMetadataRead{owner: r},
		releaseSlot: releaseSlot,
	}
	defer func() {
		if err != nil {
			owned.Close()
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	decodeStarted := time.Now()
	facade, sources, err := s.pinColdMetadataRead(ctx)
	owned.state.facade, owned.state.sources = facade, sources
	if err != nil {
		return nil, err
	}
	if err := facade.decodeColdMetadataRead(ctx, sources); err != nil {
		return nil, err
	}
	coldFacadeDecodes.Add(1)
	coldFacadeDecodeNanos.Add(int64(time.Since(decodeStarted)))
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	owned.context = context.WithValue(ctx, coldMetadataReadKey{}, owned.state)
	return owned, nil
}
