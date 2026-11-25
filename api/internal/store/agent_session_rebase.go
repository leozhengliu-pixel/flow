package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"time"

	"flow/api/internal/domain"
)

func legacySessionLifecycleWrite(ctx context.Context) bool {
	enabled, _ := ctx.Value(agentSessionLifecycleWriteKey{}).(bool)
	return enabled
}

func legacySessionLifecycleOnly(ctx context.Context, before, next metadataLogicalNode) (bool, error) {
	if before.typ != reflect.TypeFor[domain.AgentSession]() || next.typ != before.typ {
		return false, nil
	}
	closed := true
	err := before.Children(ctx, func(key reflect.Value, left metadataLogicalNode) error {
		switch key.String() {
		case "Messages", "UpdatedAt", "LastReadAt":
			return nil
		}
		right, err := issueHotField(ctx, next, key.String())
		if err != nil {
			return err
		}
		equal, err := legacyMetadataWriteEqual(ctx, left, right, 0)
		closed = closed && equal
		return err
	})
	return closed, err
}

func legacySessionSmallString(ctx context.Context, owner *ResourceRead, part legacySessionJSONPart) (string, error) {
	if part.input == nil {
		return "", nil
	}
	if part.size < 0 || uint64(part.size) > (metadataWorkBytes-1024)/8 {
		return "", ErrMetadataWorkBudget
	}
	if err := owner.reserve(ctx, uint64(part.size)*8+1024); err != nil {
		return "", err
	}
	raw := make([]byte, int(part.size))
	if _, err := part.input.ReadAt(raw, part.start); err != nil {
		return "", err
	}
	var value string
	err := json.Unmarshal(raw, &value)
	return value, err
}

type legacySessionMessageIndex struct {
	values map[string]legacySessionJSONPart
	order  []string
}

// Match mergeMetadataLists exactly: first occurrence determines order, last
// occurrence determines the value. Empty IDs refuse the concurrent list merge.
func legacySessionIndexMessages(ctx context.Context, owner *ResourceRead, part legacySessionJSONPart) (legacySessionMessageIndex, bool, error) {
	result := legacySessionMessageIndex{values: map[string]legacySessionJSONPart{}}
	if part.input == nil {
		return result, true, nil
	}
	items, err := legacySessionJSONArray(ctx, owner, part)
	if err != nil {
		return result, false, err
	}
	for _, item := range items {
		fields, err := legacySessionJSONObject(ctx, owner, item)
		if err != nil {
			return result, false, err
		}
		id, err := legacySessionSmallString(ctx, owner, fields["id"])
		if err != nil {
			return result, false, err
		}
		if id == "" {
			return result, false, nil
		}
		if err := owner.reserve(ctx, uint64(len(id))*8+512); err != nil {
			return result, false, err
		}
		if _, seen := result.values[id]; !seen {
			result.order = append(result.order, id)
		}
		result.values[id] = item
	}
	return result, true, nil
}

func legacySessionMergeMessage(ctx context.Context, store *SQLiteStore, base, ours, theirs legacySessionJSONPart, output *RecordSpool) (legacySessionJSONPart, bool, error) {
	owner := &ResourceRead{store: store}
	defer owner.Close()
	var values [3]json.RawMessage
	for index, part := range []legacySessionJSONPart{base, ours, theirs} {
		if part.input == nil {
			continue
		}
		raw, err := legacySessionJSONBytes(ctx, owner, part)
		if err != nil {
			return legacySessionJSONPart{}, false, err
		}
		values[index] = raw
	}
	merged, ok := mergeMetadataValues(values[0], values[1], values[2])
	if !ok {
		return legacySessionJSONPart{}, false, nil
	}
	start := output.bytes
	if _, err := output.Write(merged); err != nil {
		return legacySessionJSONPart{}, false, err
	}
	return legacySessionJSONPart{output, start, output.bytes - start}, true, nil
}

func legacySessionMergeMessages(ctx context.Context, owner *ResourceRead, base, ours, theirs legacySessionJSONPart, output *RecordSpool) (bool, error) {
	var lists [3]legacySessionMessageIndex
	for index, part := range []legacySessionJSONPart{base, ours, theirs} {
		list, ok, err := legacySessionIndexMessages(ctx, owner, part)
		if err != nil || !ok {
			return false, err
		}
		lists[index] = list
	}
	before, mine, stored := lists[0], lists[1], lists[2]
	selected := make([]legacySessionJSONPart, 0, len(stored.order)+len(mine.order))
	mergedItems, err := newMetadataLogicalRecordSpool(ctx)
	if err != nil {
		return false, err
	}
	defer mergedItems.Close()
	equal := func(a, b legacySessionJSONPart) (bool, error) {
		return legacySessionValueEqual(ctx, owner, a, b)
	}
	for _, id := range stored.order {
		baseItem, inBase := before.values[id]
		ourItem, inOurs := mine.values[id]
		theirItem := stored.values[id]
		switch {
		case inBase && !inOurs:
			same, err := equal(theirItem, baseItem)
			if err != nil || !same {
				return false, err
			}
			continue
		case inOurs && inBase:
			unchanged, err := equal(ourItem, baseItem)
			if err != nil {
				return false, err
			}
			if !unchanged {
				unchanged, err := equal(theirItem, baseItem)
				if err != nil {
					return false, err
				}
				if unchanged {
					theirItem = ourItem
				} else {
					part, ok, err := legacySessionMergeMessage(ctx, owner.store, baseItem, ourItem, theirItem, mergedItems)
					if err != nil || !ok {
						return false, err
					}
					theirItem = part
				}
			}
		case inOurs && !inBase:
			same, err := equal(ourItem, theirItem)
			if err != nil {
				return false, err
			}
			if !same {
				part, ok, err := legacySessionMergeMessage(ctx, owner.store, legacySessionJSONPart{}, ourItem, theirItem, mergedItems)
				if err != nil || !ok {
					return false, err
				}
				theirItem = part
			}
		}
		selected = append(selected, theirItem)
	}
	for _, id := range mine.order {
		if _, found := stored.values[id]; found {
			continue
		}
		if baseItem, found := before.values[id]; found {
			same, err := equal(mine.values[id], baseItem)
			if err != nil || !same {
				return false, err
			}
			continue
		}
		selected = append(selected, mine.values[id])
	}
	if _, err := io.WriteString(output, "["); err != nil {
		return false, err
	}
	for index, item := range selected {
		if index != 0 {
			if _, err := io.WriteString(output, ","); err != nil {
				return false, err
			}
		}
		if err := legacySessionCopyJSON(ctx, owner, item, output); err != nil {
			return false, err
		}
	}
	_, err = io.WriteString(output, "]")
	return err == nil, err
}

func (p *legacyMetadataWritePlan) rebaseAgentSessionLifecycleRow(ctx context.Context, field *legacyMetadataWriteField, index int) (bool, error) {
	if !legacySessionLifecycleWrite(ctx) || field.field.name != "agentSessions" {
		return false, nil
	}
	row := &field.rows[index]
	closed, err := legacySessionLifecycleOnly(ctx, row.before, row.next)
	if err != nil || !closed {
		return false, err
	}
	key := metadataRecordKey{"agentSessions", row.id}
	if p.tx.rebasedMetadata[key] {
		return true, nil
	}
	owner := &ResourceRead{store: p.store}
	defer owner.Close()
	if err := owner.reserve(ctx, 2<<20); err != nil {
		return true, err
	}
	lock := ""
	if p.tx.dialect != "sqlite" {
		lock = " FOR UPDATE"
	}
	var size, order int64
	err = p.tx.QueryRowContext(ctx, "SELECT "+owner.byteLength("data")+",collection_order FROM workspace_metadata_records WHERE workspace_key=? AND field='agentSessions' AND record_key=?"+lock,
		p.workspace, row.id).Scan(&size, &order)
	if errors.Is(err, sql.ErrNoRows) {
		backed, err := metadataFieldRecordBacked(ctx, p.tx, p.workspace, "agentSessions", field.before.typ)
		if err != nil {
			return true, err
		}
		if !backed {
			// Inline native collections have no canonical row to rebase.
			// The literal path leaves this occurrence untouched; do not
			// send its complete transcript through the small-row decoder.
			return true, nil
		}
		if p.tx.rebasedMetadata == nil {
			p.tx.rebasedMetadata = map[metadataRecordKey]bool{}
		}
		p.tx.rebasedMetadata[key], row.deleted = true, true
		return true, nil
	}
	if err != nil {
		return true, err
	}
	if p.tx.dialect == "sqlite" && p.store.db.journalReady.Load() {
		p.tx.journalOrder = recordJournalOrder{p.workspace, "agentSessions", row.id, order, true}
	}
	if size < 0 || size > metadataLogicalDiskBytes {
		return true, ErrMetadataWorkBudget
	}
	// Keep the already-proven generic row kernel for small native and
	// canonical inputs. The locked size probe is still necessary: a peer
	// may have grown this session after the native snapshot was captured.
	if size <= MetadataRecordPageMaxBytes && row.before.cost <= 64<<10 && row.next.cost <= 64<<10 {
		return false, nil
	}
	canonical, err := newMetadataLogicalRecordSpool(ctx)
	if err != nil {
		return true, err
	}
	defer canonical.Close()
	if !canonical.CanAppend(size) {
		return true, ErrRecordSpoolBudget
	}
	reader := &canonicalMetadataReader{ctx: ctx, tx: p.tx, workspace: p.workspace, key: RecordKey{"agentSessions", row.id}, size: size}
	if count, err := io.CopyBuffer(canonical, reader, make([]byte, metadataRecordChunkBytes)); err != nil || count != size {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return true, err
	}
	base, err := legacySessionNodeSpool(ctx, owner, row.before)
	if err != nil {
		return true, err
	}
	defer base.Close()
	ours, err := legacySessionNodeSpool(ctx, owner, row.next)
	if err != nil {
		return true, err
	}
	defer ours.Close()
	baseFields, err := legacySessionJSONObject(ctx, owner, legacySessionSpoolPart(base))
	if err != nil {
		return true, err
	}
	rawFields, err := legacySessionJSONObject(ctx, owner, legacySessionSpoolPart(canonical))
	if err != nil {
		return true, err
	}
	if p.tx.rebasedMetadata == nil {
		p.tx.rebasedMetadata = map[metadataRecordKey]bool{}
	}
	p.tx.rebasedMetadata[key] = true
	if same, err := legacySessionObjectsEqual(ctx, owner, rawFields, baseFields); err != nil || same {
		return true, err
	}
	marker, err := legacySessionHasMarker(ctx, legacySessionSpoolPart(canonical))
	if err != nil {
		return true, err
	}
	if marker && p.tx.database != nil && p.tx.database.content != nil {
		storedBase, err := newMetadataLogicalRecordSpool(ctx)
		if err != nil {
			return true, err
		}
		defer storedBase.Close()
		if err := legacySessionStoredForm(ctx, owner, p.tx, p.workspace, legacySessionSpoolPart(base), storedBase, false); err != nil {
			return true, err
		}
		storedFields, err := legacySessionJSONObject(ctx, owner, legacySessionSpoolPart(storedBase))
		if err != nil {
			return true, err
		}
		if same, err := legacySessionObjectsEqual(ctx, owner, rawFields, storedFields); err != nil || same {
			return true, err
		}
	}
	// AgentSession has no per-record load normalization. This typed decode
	// matches normalizedMetadataRecord for the loaded/theirs comparison.
	decoded, err := legacySessionCanonicalRecord(ctx, p.store, p.frames, legacySessionSpoolPart(canonical))
	if err != nil {
		return true, err
	}
	theirs, err := legacySessionNodeSpool(ctx, owner, decoded)
	if err != nil {
		return true, err
	}
	defer theirs.Close()
	theirFields, err := legacySessionJSONObject(ctx, owner, legacySessionSpoolPart(theirs))
	if err != nil {
		return true, err
	}
	if same, err := legacySessionObjectsEqual(ctx, owner, theirFields, baseFields); err != nil || same {
		return true, err
	}
	ourFields, err := legacySessionJSONObject(ctx, owner, legacySessionSpoolPart(ours))
	if err != nil {
		return true, err
	}
	mergedFields := make(map[string]legacySessionJSONPart, len(theirFields))
	for name, part := range theirFields {
		mergedFields[name] = part
	}
	names := map[string]bool{}
	for name := range baseFields {
		names[name] = true
	}
	for name := range ourFields {
		names[name] = true
	}
	messageMerge, err := newMetadataLogicalRecordSpool(ctx)
	if err != nil {
		return true, err
	}
	defer messageMerge.Close()
	conflict := func() (bool, error) {
		return true, &metadataConflictError{keys: []metadataRecordKey{key}}
	}
	for name := range names {
		a, b, c := baseFields[name], ourFields[name], theirFields[name]
		if same, err := legacySessionValueEqual(ctx, owner, a, b); err != nil {
			return true, err
		} else if same {
			continue
		}
		if same, err := legacySessionValueEqual(ctx, owner, c, a); err != nil {
			return true, err
		} else if same {
			if b.input == nil {
				delete(mergedFields, name)
			} else {
				mergedFields[name] = b
			}
			continue
		}
		switch name {
		case "updatedAt":
			var mine, current time.Time
			oursRaw, err := legacySessionJSONBytes(ctx, owner, b)
			if err != nil {
				return true, err
			}
			theirsRaw, err := legacySessionJSONBytes(ctx, owner, c)
			if err != nil {
				return true, err
			}
			if json.Unmarshal(oursRaw, &mine) != nil || json.Unmarshal(theirsRaw, &current) != nil {
				return conflict()
			}
			if mine.After(current) {
				mergedFields[name] = b
			}
		case "messages":
			ok, err := legacySessionMergeMessages(ctx, owner, a, b, c, messageMerge)
			if err != nil {
				return true, err
			}
			if !ok {
				return conflict()
			}
			mergedFields[name] = legacySessionSpoolPart(messageMerge)
		case "lastReadAt":
			if same, err := legacySessionValueEqual(ctx, owner, b, c); err != nil {
				return true, err
			} else if same {
				if b.input == nil {
					delete(mergedFields, name)
				} else {
					mergedFields[name] = b
				}
			} else {
				return conflict()
			}
		default:
			// The native lifecycle closure forbids edits to these fields.
			return true, ErrBoundedMetadataCoverage
		}
	}
	merged, err := newMetadataLogicalRecordSpool(ctx)
	if err != nil {
		return true, err
	}
	defer merged.Close()
	if err := legacySessionWriteJSONObject(ctx, owner, mergedFields, merged); err != nil {
		return true, err
	}
	postimage, err := legacySessionCanonicalRecord(ctx, p.store, p.frames, legacySessionSpoolPart(merged))
	if err != nil {
		return true, err
	}
	row.before, row.next = decoded, postimage
	idNode, err := issueHotField(ctx, postimage, "ID")
	if err != nil {
		return true, err
	}
	value, err := owner.node(ctx, idNode, false)
	if err != nil {
		return true, err
	}
	row.id = value.String()
	return true, nil
}
