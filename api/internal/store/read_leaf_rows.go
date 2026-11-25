package store

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
)

func readLeafAggregate(ctx context.Context, owner *ResourceRead, node metadataAggregateNode) (reflect.Value, error) {
	if owner.source != nil {
		ctx = metadataLogicalSourceScratchContext(ctx, owner.source)
	}
	builder, err := newMetadataLogicalBuilder(ctx)
	if err != nil {
		return reflect.Value{}, err
	}
	defer builder.Close()
	value, err := builder.writeAggregateNode(node, 0)
	if err != nil {
		return reflect.Value{}, err
	}
	return owner.node(ctx, value, false)
}

func readLeafBegin(ctx context.Context, s *SQLiteStore, workspace string) (_ *InstalledMetadataRead, _ *ResourceRead, err error) {
	if !s.BoundedMetadataEnabled() {
		return nil, nil, ErrBoundedMetadataCoverage
	}
	ctx = context.WithValue(metadataLogicalUnpinnedContext(ctx), workspaceReadKey{}, workspaceRead{})
	installed, found, err := s.BeginInstalledMetadataRead(ctx, workspace)
	if err != nil || !found {
		if err == nil {
			err = sql.ErrNoRows
		}
		return nil, nil, err
	}
	source, err := installed.retainSource(ctx, installed.workspace)
	if err != nil {
		installed.Close()
		return nil, nil, err
	}
	owner := &ResourceRead{store: s, source: source}
	if err := owner.reserve(ctx, 128<<10); err != nil {
		installed.Close()
		owner.Close()
		return nil, nil, err
	}
	return installed, owner, nil
}

func readLeafIteration(err error) error {
	if errors.Is(err, ErrMetadataIterationDone) {
		return nil
	}
	return err
}
