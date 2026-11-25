package store

import (
	"context"
	"reflect"

	"flow/api/internal/domain"
)

// prepareLegacyIssueImage owns a complete literal SQL issue image. Every
// other field retains its supplied source generation without normalization.
// The transaction is borrowed only until this synchronous transform returns.
func (s *SQLiteStore) prepareLegacyIssueImage(ctx context.Context, tx *sqlTx, workspace string, source *metadataLogicalSource) (_ *metadataLogicalSource, err error) {
	if s == nil || !s.BoundedMetadataEnabled() || source == nil || tx == nil || tx.database != s.db || tx.dialect != s.dialect {
		return nil, ErrBoundedMetadataCoverage
	}
	ctx = metadataLogicalSourceScratchContext(metadataLogicalUnpinnedContext(ctx), source)
	work := &ResourceRead{store: s}
	defer work.Close()
	if err := work.reserve(ctx, 64<<10); err != nil {
		return nil, err
	}
	stamp, err := readMetadataStamp(ctx, tx, workspace)
	if err != nil {
		return nil, err
	}
	if stamp.workspaceID == "" || source.Stamp().workspaceID != stamp.workspaceID {
		return nil, ErrMetadataRecordQuery
	}
	// readIssueRecordsWithBase starts with []Issue{}, even when SQL is empty.
	// Its shape therefore replaces the installed Issues field's nil flag.
	shape, err := newMetadataLogicalBuilder(ctx)
	if err != nil {
		return nil, err
	}
	defer shape.Close()
	empty, err := shape.writeValue(reflect.ValueOf([]domain.Issue{}), 0, false)
	if err != nil {
		return nil, err
	}
	result, err := source.Transform(ctx, func(name string, original metadataLogicalNode) (metadataAggregateNode, bool, error) {
		if name == "issues" {
			return legacyIssueImageNode{store: s, tx: tx, workspace: workspace, shape: empty}, true, nil
		}
		return metadataAggregateStoredNode{value: original}, false, nil
	})
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		result.Release()
		return nil, err
	}
	return result, nil
}

type legacyIssueImageNode struct {
	store     *SQLiteStore
	tx        *sqlTx
	workspace string
	shape     metadataLogicalNode
}

func (n legacyIssueImageNode) nodeType() reflect.Type           { return reflect.TypeFor[[]domain.Issue]() }
func (n legacyIssueImageNode) logicalBase() metadataLogicalNode { return n.shape }
func (n legacyIssueImageNode) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n legacyIssueImageNode) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	ordinal := 0
	return n.store.scanLegacyIssueFrames(ctx, n.tx, n.workspace, func(row legacyIssueFrame) error {
		if base := legacyRecordBaseCapture(ctx); base != nil {
			if err := base.RememberStoredHash(ctx, "issue", "", row.PhysicalID, row.StoredHash); err != nil {
				return err
			}
		}
		if err := visit(reflect.ValueOf(ordinal), metadataAggregateStoredNode{value: row.Node}); err != nil {
			return err
		}
		ordinal++
		return nil
	})
}
