package store

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
)

type metadataLogicalScratchKey struct{}
type metadataLogicalScratch struct {
	logical   bool
	directory string
}

// Installed metadata has its own process disk allowance. Disabling optional
// compatibility-download spooling must not disable a committed native writer's
// logical reload. Reuse the existing source's writable directory on reload.
func metadataLogicalSourceScratchContext(ctx context.Context, source *metadataLogicalSource) context.Context {
	config := metadataLogicalScratch{logical: true}
	if source != nil {
		source.mu.Lock()
		if source.refs != 0 && len(source.stores) != 0 {
			config.directory = source.stores[0].Dir()
		}
		source.mu.Unlock()
	}
	return context.WithValue(ctx, metadataLogicalScratchKey{}, config)
}

func (s *SQLiteStore) metadataLogicalScratchContext(ctx context.Context, workspace string) context.Context {
	ctx = metadataLogicalUnpinnedContext(ctx)
	source, err := s.retainMetadataLogicalSource(ctx, workspace)
	if err != nil {
		return metadataLogicalSourceScratchContext(ctx, nil)
	}
	defer source.Release()
	return metadataLogicalSourceScratchContext(ctx, source)
}

func metadataLogicalDiskDemand(ctx context.Context) *atomic.Int64 {
	if demand := metadataWorkDemandFrom(ctx); demand != nil {
		return demand.disk
	}
	return nil
}

func metadataLogicalDiskContended(demand *atomic.Int64, size, limit int64) bool {
	return demand != nil && size > 0 && size <= limit-demand.Load()
}

func newMetadataLogicalRecordSpool(ctx context.Context) (*RecordSpool, error) {
	spool := &RecordSpool{
		counter: &metadataLogicalDiskUsed, limit: metadataLogicalDiskCap(),
		maxSize: metadataLogicalDiskBytes, demand: metadataLogicalDiskDemand(ctx),
	}
	if err := spool.checkAppend(1); err != nil {
		return nil, err
	}
	config, _ := ctx.Value(metadataLogicalScratchKey{}).(metadataLogicalScratch)
	file, err := os.CreateTemp(metadataSpoolDirectory(config.directory), "flow-p21-root-*")
	if err != nil {
		return nil, err
	}
	spool.file = file
	return spool, nil
}

// HoldMetadataLogicalDiskForTesting occupies the available quota without
// allocating a physical file, leaving free bytes of it. A hold leaving no
// bytes charges the whole quota on top of what is in use: scratch that
// concurrent work (a peer's background reload) held when the hold was taken
// and releases while it lasts must not reopen room under a full disk.
func HoldMetadataLogicalDiskForTesting(free int64) (func(), error) {
	if !testing.Testing() || free < 0 {
		return nil, ErrRecordSpoolBudget
	}
	limit := metadataLogicalDiskCap()
	if free == 0 {
		if limit <= 0 {
			return nil, ErrRecordSpoolBudget
		}
		metadataLogicalDiskUsed.Add(limit)
		var once sync.Once
		return func() { once.Do(func() { metadataLogicalDiskUsed.Add(-limit) }) }, nil
	}
	for {
		used := metadataLogicalDiskUsed.Load()
		bytes := limit - used - free
		if bytes <= 0 {
			return nil, ErrRecordSpoolBudget
		}
		if metadataLogicalDiskUsed.CompareAndSwap(used, used+bytes) {
			var once sync.Once
			return func() { once.Do(func() { metadataLogicalDiskUsed.Add(-bytes) }) }, nil
		}
	}
}
