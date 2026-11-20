package store

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
)

// A spool group is the lineage of one workspace's installed source: the
// storages every generation derived from it writes (Fork, Transform, COLD
// incremental publication, external hydration). It holds the one descriptor
// charge of that lineage (metadataLogicalDescriptorBytes against
// FLOW_METADATA_SOURCE_BYTES) and what the charge pays for: one read cache
// shared by the group's storages and one write buffer's worth of in-memory
// spools.
//
// Each storage used to carry the charge and a cache of its own. A publication
// writes every rewritten field to a storage of its own (its registry keeps
// copied frames verbatim) and shares the storages of unchanged fields, so an
// installed source came to hold one storage per field rewritten since its last
// full load: a workspace created at runtime held 12 at once and about 20 after
// a few writes, and around 25 such workspaces used the whole default budget
// that the documented sizing gave 384 to 512 workspaces.
//
// Charges:
//   - A storage that starts a group (newMetadataLogicalBuilder) reserves the
//     group's descriptor charge; the group returns it when its last storage is
//     released.
//   - A storage that joins a group reserves its write buffer
//     (metadataLogicalWriteBytes) until it settles or is released.
//   - A settled storage that never needed a file keeps its bytes in memory
//     only within the group's resident allowance (metadataLogicalWriteBytes);
//     beyond it, settle writes the spool to its file.
//
// So a resident workspace holds exactly one charge however many storages its
// installed source shares, and its spool memory stays within that charge:
// the shared cache (metadataLogicalReadSlots blocks) plus the resident
// allowance.
type metadataLogicalSpoolGroup struct {
	mu       sync.Mutex
	refs     int   // live storages
	resident int64 // in-memory bytes of the group's settled file-less storages
	cache    metadataLogicalBlockCache
}

// metadataLogicalBlockCache holds the cached blocks of a group's storages,
// each tagged with the storage it was read from.
type metadataLogicalBlockCache struct {
	blocks [metadataLogicalReadSlots]atomic.Pointer[metadataLogicalBlock]
	victim atomic.Uint32
}

func metadataLogicalSourceBudgetError(limit int64) error {
	return fmt.Errorf("%w: installed workspace sources use all of FLOW_METADATA_SOURCE_BYTES (%d bytes, %d KiB per workspace)", ErrMetadataWorkBudget, limit, metadataLogicalDescriptorBytes>>10)
}

// newMetadataLogicalStorage creates an empty storage in group, or in a new
// group of its own when group is nil or already released.
func newMetadataLogicalStorage(ctx context.Context, group *metadataLogicalSpoolGroup) (*metadataLogicalStorage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	config, _ := ctx.Value(metadataLogicalScratchKey{}).(metadataLogicalScratch)
	storage := &metadataLogicalStorage{directory: metadataSpoolDirectory(config.directory), refs: 1}
	limit := metadataLogicalDescriptorCap()
	if group != nil {
		if !reserveMetadataLogicalBytes(&metadataLogicalDescriptorsUsed, metadataLogicalWriteBytes, limit) {
			return nil, metadataLogicalSourceBudgetError(limit)
		}
		if group.join() {
			storage.group, storage.writeCharge = group, metadataLogicalWriteBytes
			return storage, nil
		}
		metadataLogicalDescriptorsUsed.Add(-metadataLogicalWriteBytes)
	}
	if !reserveMetadataLogicalBytes(&metadataLogicalDescriptorsUsed, metadataLogicalDescriptorBytes, limit) {
		return nil, metadataLogicalSourceBudgetError(limit)
	}
	storage.group = &metadataLogicalSpoolGroup{refs: 1}
	return storage, nil
}

func (g *metadataLogicalSpoolGroup) join() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.refs == 0 {
		return false
	}
	g.refs++
	return true
}

// admitResident reports whether size more bytes of settled spools may stay in
// memory, and counts them when they may.
func (g *metadataLogicalSpoolGroup) admitResident(size int64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if size > metadataLogicalWriteBytes-g.resident {
		return false
	}
	g.resident += size
	return true
}

// leave drops a released storage: its resident bytes and cached blocks, and
// with the last storage the group's descriptor charge.
func (g *metadataLogicalSpoolGroup) leave(resident int64) {
	g.mu.Lock()
	g.resident -= resident
	g.refs--
	last := g.refs == 0
	g.mu.Unlock()
	if last {
		for index := range g.cache.blocks {
			g.cache.blocks[index].Store(nil)
		}
		metadataLogicalDescriptorsUsed.Add(-metadataLogicalDescriptorBytes)
	}
}

// spoolGroup is the group the source's storages share: a generation derived
// from the source writes its storages there.
func (s *metadataLogicalSource) spoolGroup() *metadataLogicalSpoolGroup {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, storage := range s.stores {
		if storage.group != nil {
			return storage.group
		}
	}
	return nil
}

// startPart moves the builder to a new storage of its group: a rewritten field
// gets its own registry, so frames copied from the field's previous storage
// keep their dynamic type and time IDs.
func (b *metadataLogicalBuilder) startPart(ctx context.Context) error {
	storage, err := newMetadataLogicalStorage(ctx, b.storage.group)
	if err != nil {
		return err
	}
	b.source.stores = append(b.source.stores, storage)
	b.storage, b.offset = storage, 0
	return nil
}

// blockCache is the cache the storage's reads use: its group's, or one of its
// own for a storage made outside a builder (tests).
func (s *metadataLogicalStorage) blockCache() *metadataLogicalBlockCache {
	if s.group != nil {
		return &s.group.cache
	}
	if cache := s.ownCache.Load(); cache != nil {
		return cache
	}
	s.ownCache.CompareAndSwap(nil, &metadataLogicalBlockCache{})
	return s.ownCache.Load()
}

// dropCachedBlocks drops every cached block read from the storage.
func (s *metadataLogicalStorage) dropCachedBlocks() {
	cache := s.blockCache()
	for index := range cache.blocks {
		slot := &cache.blocks[index]
		if cached := slot.Load(); cached != nil && cached.owner == s {
			slot.CompareAndSwap(cached, nil)
		}
	}
}
