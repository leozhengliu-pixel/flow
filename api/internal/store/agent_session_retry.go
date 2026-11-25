package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"reflect"
	"strings"
)

// The generic retry adapter has a page-sized raw-row transfer limit. Session
// retries keep its first-occurrence replacement/deletion and unknown-stamp
// installation semantics while framing the selected canonical row from disk.
func (s *SQLiteStore) refreshAgentSessionLifecycleConflict(ctx context.Context, workspace string) func(error) error {
	return s.refreshMetadataLifecycleConflict(ctx, workspace, "agentSessions", legacySessionCanonicalRecord)
}

func (s *SQLiteStore) refreshMetadataLifecycleConflict(ctx context.Context, workspace, kind string, decode func(context.Context, *SQLiteStore, *metadataLogicalBuilder, legacySessionJSONPart) (metadataLogicalNode, error)) func(error) error {
	ordinary := s.refreshConflictedLogicalMetadata(ctx, workspace)
	return func(conflict error) error {
		keys, found := conflictedMetadataKeys(conflict)
		if !found {
			return ordinary(conflict)
		}
		var sessions, other []metadataRecordKey
		for _, key := range keys {
			if key.field == kind {
				sessions = append(sessions, key)
			} else {
				other = append(other, key)
			}
		}
		if len(sessions) == 0 {
			return ordinary(conflict)
		}
		if len(other) != 0 {
			if err := ordinary(&metadataConflictError{keys: other}); err != nil {
				return err
			}
		}
		if len(sessions) > MetadataRecordBatchMaxKeys {
			return ErrMetadataPageBudget
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
		replacements := map[string]metadataLogicalNode{}
		// behind: some conflicted row is not the stored form of the record
		// this generation holds, so another server committed it.
		behind := false
		for _, key := range sessions {
			if err := owner.reserve(ctx, uint64(len(key.key))*8+512); err != nil {
				return err
			}
			if s.dialect == "postgres" && strings.IndexByte(key.key, 0) >= 0 {
				// The literal refresh (refreshMetadataRecords) matches the
				// stored keys it reads with the conflicted one. PostgreSQL
				// stores this record without NUL, so the literal copy finds no
				// row under its key and drops the record, while SQL keeps the
				// row: the installed image drops it too.
				replacements[key.key] = metadataLogicalNode{}
				behind = true
				continue
			}
			var size int64
			err := tx.QueryRowContext(ctx, "SELECT "+owner.byteLength("data")+" FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?",
				workspace, kind, key.key).Scan(&size)
			if errors.Is(err, sql.ErrNoRows) {
				replacements[key.key] = metadataLogicalNode{}
				behind = true
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
				reader := &canonicalMetadataReader{ctx: ctx, tx: tx, workspace: workspace, key: RecordKey{kind, key.key}, size: size}
				if count, err := io.CopyBuffer(spool, reader, make([]byte, metadataRecordChunkBytes)); err != nil || count != size {
					if err == nil {
						err = io.ErrUnexpectedEOF
					}
					return err
				}
				node, err := decode(ctx, s, frames, legacySessionSpoolPart(spool))
				if err != nil {
					return err
				}
				replacements[key.key] = node
				if !behind {
					stored, err := legacySessionInstalledRowStored(ctx, owner, tx, workspace, source, kind, key.key, spool)
					if err != nil {
						if ctx.Err() != nil {
							return ctx.Err()
						}
						stored = false
					}
					behind = !stored
				}
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
			if name != kind {
				return current, false, nil
			}
			for _, key := range sessions {
				var value metadataAggregateNode
				if replacement := replacements[key.key]; replacement.typ != nil {
					value = metadataAggregateStoredNode{value: replacement}
				}
				current = metadataLogicalPatchedCollection{base: current, shape: original, key: key.key, value: value}
			}
			return current, true, nil
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
		// Only the conflicted records are refreshed: the rest of the peer's
		// commit is still missing (installedGenerationBehind). A conflict
		// whose rows SQL holds exactly as this generation stores its own
		// records is no evidence of a peer: the installed session differs
		// from its row only by what persisting drops (NUL, the references of
		// bodies that are loaded), as after any write of this server, whose
		// candidate the next write keeps (as the literal path keeps it).
		candidate.behind.Store(behind || source.behind.Load())
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

// legacySessionInstalledRowStored reports whether row, a record row as SQL
// holds it, is exactly what the record of source (the installed generation)
// under key stores as: the persist path's NUL strip and, with a transaction,
// the session offload stored form (writeAgentSessionLifecycleNativeRow),
// compared as the rebase compares stored rows. A missing installed record is
// not. The general conflict refresh (refreshConflictedLogicalMetadata) shares
// it without a transaction: no offload form, so a row whose body references
// differ is conservatively not stored.
func legacySessionInstalledRowStored(ctx context.Context, owner *ResourceRead, tx *sqlTx, workspace string, source *metadataLogicalSource, field, key string, row *RecordSpool) (bool, error) {
	index, _, ok := metadataLogicalField(field)
	if !ok || index >= len(source.fields) || source.fields[index].typ == nil {
		return false, nil
	}
	found, err := metadataLogicalFirstRecord(ctx, metadataAggregateStoredNode{value: source.fields[index]}, key)
	if err != nil {
		return false, err
	}
	record, ok := found.(metadataAggregateStoredNode)
	if !ok || record.value.typ == nil {
		return false, nil
	}
	native, err := legacySessionNodeSpool(ctx, owner, record.value)
	if err != nil {
		return false, err
	}
	defer native.Close()
	stripped, err := newMetadataLogicalRecordSpool(ctx)
	if err != nil {
		return false, err
	}
	defer stripped.Close()
	if err := legacyProjectCopyStoredJSON(ctx, stripped, legacySessionSpoolPart(native).reader()); err != nil {
		return false, err
	}
	stored, err := newMetadataLogicalRecordSpool(ctx)
	if err != nil {
		return false, err
	}
	defer stored.Close()
	if tx != nil {
		if err := legacySessionStoredForm(ctx, owner, tx, workspace, legacySessionSpoolPart(stripped), stored, false); err != nil {
			return false, err
		}
	} else if _, err := io.CopyBuffer(stored, legacySessionSpoolPart(stripped).reader(), make([]byte, metadataRecordChunkBytes)); err != nil {
		return false, err
	}
	storedFields, err := legacySessionJSONObject(ctx, owner, legacySessionSpoolPart(stored))
	if err != nil {
		return false, err
	}
	rowFields, err := legacySessionJSONObject(ctx, owner, legacySessionSpoolPart(row))
	if err != nil {
		return false, err
	}
	equal, err := legacySessionObjectsEqual(ctx, owner, rowFields, storedFields)
	if err != nil || equal {
		return equal, err
	}
	return legacyInstalledRecordValueStored(ctx, owner, field, key, legacySessionSpoolPart(stored), legacySessionSpoolPart(row))
}

// legacyInstalledRecordValueStored compares a small row with the installed
// record's stored form as the literal rebase does (mergeWithStoredRecord's
// unchanged case): both decoded as the record type and normalized. The bytes
// alone are not the record: the offload stored form re-encodes a session
// whose body references it drops with sorted keys, while the same session
// decoded from that row and installed (an earlier conflict refresh) encodes
// in field order and stores as is. Byte inequality there is no evidence of
// another server's commit; treating it as one reloaded the whole generation
// from SQL after this server's own write (installedGenerationBehind). A
// larger record stays conservatively not stored.
func legacyInstalledRecordValueStored(ctx context.Context, owner *ResourceRead, name, key string, installed, row legacySessionJSONPart) (bool, error) {
	if installed.size > MetadataRecordPageMaxBytes || row.size > MetadataRecordPageMaxBytes {
		return false, nil
	}
	field, kind, ok := metadataRecordType(name)
	if !ok {
		return false, nil
	}
	work := &ResourceRead{store: owner.store}
	defer work.Close()
	var normalized [2][]byte
	for index, part := range []legacySessionJSONPart{installed, row} {
		raw, err := legacySessionJSONBytes(ctx, work, part)
		if err != nil {
			return false, err
		}
		value, err := decodeMetadataRecord(kind, raw, reflect.Value{})
		if err != nil {
			return false, nil
		}
		if normalized[index], err = normalizedMetadataRecord(field, key, value); err != nil {
			return false, err
		}
	}
	return bytes.Equal(normalized[0], normalized[1]), nil
}
