package store

import (
	"context"
	"os"
	"path/filepath"
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
			config.directory = filepath.Dir(source.stores[0].file.Name())
		} else if source.refs != 0 && source.units != nil {
			config.directory = filepath.Dir(source.units.arena.index.stage.path)
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

func newMetadataLogicalRecordSpool(ctx context.Context) (*RecordSpool, error) {
	if metadataLogicalDiskUsed.Load() >= metadataLogicalDiskBytes {
		return nil, ErrRecordSpoolBudget
	}
	config, _ := ctx.Value(metadataLogicalScratchKey{}).(metadataLogicalScratch)
	file, err := os.CreateTemp(config.directory, "flow-p21-root-*")
	if err != nil {
		return nil, err
	}
	return &RecordSpool{file: file, counter: &metadataLogicalDiskUsed, limit: metadataLogicalDiskBytes}, nil
}
