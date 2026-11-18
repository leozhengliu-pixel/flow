package store

import (
	"context"
	"database/sql"
	"errors"
	"io"
)

type integrationDeliveryStreamKey struct{}

func withIntegrationDeliveryStream(ctx context.Context, event string) context.Context {
	if event != "integration_delivery.claimed" && event != "integration_delivery.retry" {
		return ctx
	}
	return context.WithValue(ctx, integrationDeliveryStreamKey{}, event)
}

func integrationDeliveryStreamEvent(ctx context.Context) string {
	event, _ := ctx.Value(integrationDeliveryStreamKey{}).(string)
	if event != "integration_delivery.claimed" && event != "integration_delivery.retry" {
		return ""
	}
	return event
}

// Both integration kinds share one canonical refresh. Sending either kind
// through the ordinary page-sized fallback would reject its large peer row.
func (s *SQLiteStore) refreshIntegrationDeliveryConflict(ctx context.Context, workspace string) func(error) error {
	ordinary := s.refreshConflictedLogicalMetadata(ctx, workspace)
	return func(conflict error) error {
		keys, found := conflictedMetadataKeys(conflict)
		if !found {
			return ordinary(conflict)
		}
		var selected, other []metadataRecordKey
		for _, key := range keys {
			if integrationCanonicalDecoder(key.field) != nil {
				selected = append(selected, key)
			} else {
				other = append(other, key)
			}
		}
		if len(selected) == 0 {
			return ordinary(conflict)
		}
		if len(keys) > MetadataRecordBatchMaxKeys {
			return ErrMetadataPageBudget
		}
		if len(other) != 0 {
			if err := ordinary(&metadataConflictError{keys: other}); err != nil {
				return err
			}
		}
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		ctx := metadataLogicalUnpinnedContext(ctx)
		owner := &ResourceRead{store: s}
		defer owner.Close()
		if err := owner.reserve(ctx, 2<<20); err != nil {
			return err
		}
		source, err := s.retainMetadataLogicalSource(ctx, workspace)
		if err != nil {
			return err
		}
		owner.source = source
		frames, err := newMetadataLogicalBuilder(metadataLogicalSourceScratchContext(ctx, source))
		if err != nil {
			return err
		}
		defer frames.Close()
		for _, storage := range source.stores {
			if err := frames.inheritLocations(storage); err != nil {
				return err
			}
		}
		tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
		if err != nil {
			return err
		}
		defer tx.Rollback()
		replacements := map[metadataRecordKey]metadataLogicalNode{}
		for _, key := range selected {
			if err := owner.reserve(ctx, uint64(len(key.key))*8+512); err != nil {
				return err
			}
			var size int64
			err := tx.QueryRowContext(ctx, "SELECT "+owner.byteLength("data")+" FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?",
				workspace, key.field, key.key).Scan(&size)
			if errors.Is(err, sql.ErrNoRows) {
				replacements[key] = metadataLogicalNode{}
				continue
			}
			if err != nil {
				return err
			}
			if size < 0 || size > metadataLogicalDiskBytes {
				return ErrMetadataWorkBudget
			}
			err = func() error {
				spool, err := newMetadataLogicalRecordSpool(ctx)
				if err != nil {
					return err
				}
				defer spool.Close()
				reader := &canonicalMetadataReader{ctx: ctx, tx: tx, workspace: workspace, key: RecordKey{key.field, key.key}, size: size}
				if count, err := io.CopyBuffer(spool, reader, make([]byte, metadataRecordChunkBytes)); err != nil || count != size {
					if err == nil {
						err = io.ErrUnexpectedEOF
					}
					return err
				}
				node, err := integrationCanonicalDecoder(key.field)(ctx, s, frames, legacySessionSpoolPart(spool))
				if err != nil {
					return err
				}
				replacements[key] = node
				return nil
			}()
			if err != nil {
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		candidate, err := source.Transform(ctx, func(name string, original metadataLogicalNode) (metadataAggregateNode, bool, error) {
			var current metadataAggregateNode = metadataAggregateStoredNode{value: original}
			if integrationCanonicalDecoder(name) == nil {
				return current, false, nil
			}
			changed := false
			for _, key := range selected {
				if key.field != name {
					continue
				}
				var value metadataAggregateNode
				if replacement := replacements[key]; replacement.typ != nil {
					value = metadataAggregateStoredNode{value: replacement}
				}
				current = metadataLogicalPatchedCollection{base: current, shape: original, key: key.key, value: value}
				changed = true
			}
			return current, changed, nil
		})
		if err != nil {
			return err
		}
		defer func() {
			if candidate != nil {
				candidate.Release()
			}
		}()
		stamp := copyStamp{workspaceID: source.Stamp().workspaceID, revision: -1}
		if err := candidate.setStamp(stamp); err != nil {
			return err
		}
		policy, cost, err := s.metadataLogicalPolicy(ctx, candidate)
		if err != nil {
			return err
		}
		if err := s.installResidentPolicy(workspace, policy, stamp, 0, cost, candidate); err != nil {
			return err
		}
		candidate = nil
		return nil
	}
}
