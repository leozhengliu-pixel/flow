package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
)

// rebaseLargeRow is rebaseRow's rebaseMetadata kernel for one large changed
// entity row. It locks and streams the canonical row, then decides in order:
//
//  1. storedRecordEqual's display comparison of the canonical row and the
//     encoded base, streamed member by member (no other write changed it);
//  2. for offloaded records, storedRecordEqual's stored-form exit and
//     mergeWithStoredRecord's loaded exit on the complete encodings, each
//     in its own admitted phase (no typed images of the writer);
//  3. otherwise the original comparison and merge on typed headers: every
//     large top-level string is framed (canonical and native), compared by
//     its encoded bytes and spliced back afterwards, as the project progress
//     kernel does for descriptions. A string the writer replaced merges as
//     mergeMetadataRecord merges a string: ours when the canonical body is
//     still the base (or already ours), otherwise a conflict.
//
// Missing rows, inline fields, conflicts, the canonical ID and the
// rebased-key bookkeeping follow rebaseRow/rebaseMetadata. It reports false
// (leaving the generic kernel to decide) only when load normalization reads
// or rewrites a framed body, which no current record type does.
func (p *legacyMetadataWritePlan) rebaseLargeRow(ctx context.Context, f *legacyMetadataWriteField, index int) (bool, error) {
	kind, ok := largeRowKind(f)
	row := &f.rows[index]
	if !ok || row.before.typ == nil || row.next.typ == nil || !largeRowSelected(row.before, row.next) {
		return false, nil
	}
	name := f.field.name
	key := metadataRecordKey{name, row.id}
	if p.tx.rebasedMetadata[key] {
		return true, nil
	}
	mark := func() {
		if p.tx.rebasedMetadata == nil {
			p.tx.rebasedMetadata = map[metadataRecordKey]bool{}
		}
		p.tx.rebasedMetadata[key] = true
	}
	owner := &ResourceRead{store: p.store}
	defer owner.Close()
	if err := owner.reserve(ctx, 512<<10); err != nil {
		return true, err
	}
	lock := ""
	if p.tx.dialect != "sqlite" {
		lock = " FOR UPDATE"
	}
	remember := p.tx.dialect == "sqlite" && p.tx.database != nil && p.tx.database.journalReady.Load()
	if remember {
		p.tx.journalOrder.valid = false
	}
	var size, order int64
	err := p.tx.QueryRowContext(ctx, "SELECT "+owner.byteLength("data")+",collection_order FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?"+lock,
		p.workspace, name, row.id).Scan(&size, &order)
	if errors.Is(err, sql.ErrNoRows) {
		backed, err := metadataFieldRecordBacked(ctx, p.tx, p.workspace, name, f.before.typ)
		if err != nil {
			return true, err
		}
		if !backed {
			// An inline field has no rows; rebaseMetadata leaves it as is.
			return true, nil
		}
		// Deleted by another write: ours does not bring it back.
		mark()
		row.deleted = true
		return true, nil
	}
	if err != nil {
		return true, err
	}
	if remember {
		p.tx.journalOrder = recordJournalOrder{p.workspace, name, row.id, order, true}
	}
	if size < 0 || size > metadataLogicalDiskBytes {
		return true, ErrMetadataWorkBudget
	}
	canonical, err := newMetadataLogicalRecordSpool(ctx)
	if err != nil {
		return true, err
	}
	defer canonical.Close()
	if !canonical.CanAppend(size) {
		return true, ErrRecordSpoolBudget
	}
	reader := &canonicalMetadataReader{ctx: ctx, tx: p.tx, workspace: p.workspace, key: RecordKey{name, row.id}, size: size}
	if copied, err := io.CopyBuffer(canonical, reader, make([]byte, metadataRecordChunkBytes)); err != nil || copied != size {
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
	same := false
	if err := largeRowPhase(ctx, p.store, func(scratch *ResourceRead) error {
		equal, decided, err := largeRowDisplayEqual(ctx, scratch, p.requested, legacySessionSpoolPart(canonical), legacySessionSpoolPart(base))
		same = decided && equal
		return err
	}); err != nil {
		return true, err
	}
	if same {
		mark()
		return true, nil
	}
	offloadMarked := false
	if offloadFields[name] && p.tx.database != nil && p.tx.database.content != nil {
		// storedRecordEqual's offload exit: the canonical row keeps
		// version bodies as references. It needs both complete encodings
		// (no typed header), each admitted for the stored-form rewrite.
		marked, err := largeRowContains(ctx, legacySessionSpoolPart(canonical).reader(), offloadMarkerBytes)
		if err != nil {
			return true, err
		}
		offloadMarked = marked
		if marked {
			same := false
			if err := largeRowPhase(ctx, p.store, func(scratch *ResourceRead) error {
				stored, err := largeRowPartBytes(ctx, scratch, legacySessionSpoolPart(canonical), 2)
				if err != nil {
					return err
				}
				encoded, err := largeRowPartBytes(ctx, scratch, legacySessionSpoolPart(base), 4)
				if err != nil {
					return err
				}
				refs, err := p.references(ctx, p.requested, scratch, nil, nil, stored, encoded)
				if err != nil {
					return err
				}
				same = storedRecordEqual(p.tx, refs, name, stored, encoded)
				return nil
			}); err != nil {
				return true, err
			}
			if same {
				mark()
				return true, nil
			}
		}
	}
	if offloadFields[name] {
		// mergeWithStoredRecord's first exit without the writer's typed
		// images: the canonical row as a load presents it (markers
		// resolved, typed, normalized, display users current) equals the
		// encoded base. A decode error is left to the header kernel, which
		// reports it as the original does.
		same := false
		if err := largeRowPhase(ctx, p.store, func(scratch *ResourceRead) error {
			stored, err := largeRowPartBytes(ctx, scratch, legacySessionSpoolPart(canonical), 1)
			if err != nil {
				return err
			}
			encoded, err := largeRowPartBytes(ctx, scratch, legacySessionSpoolPart(base), 3)
			if err != nil {
				return err
			}
			expanded := uint64(bytes.Count(stored, documentMarkerBytes)+1) * uint64(len(stored))
			if expanded > metadataWorkBytes/4 {
				return ErrMetadataWorkBudget
			}
			if err := scratch.reserve(ctx, expanded*2+4096); err != nil {
				return err
			}
			resolved, err := resolveDocumentMarkers(name, stored)
			if err != nil {
				return nil
			}
			typed, err := resourceWindowTypedJSONWork(ctx, resolved, kind)
			if err != nil {
				return nil
			}
			if err := scratch.reserve(ctx, typed+uint64(len(resolved))*3+4096); err != nil {
				return err
			}
			value, err := decodeMetadataRecord(kind, resolved, reflect.Value{})
			if err != nil {
				return nil
			}
			loaded, err := normalizedMetadataRecord(f.field, row.id, value)
			if err != nil {
				return nil
			}
			refs, err := p.references(ctx, p.requested, scratch, nil, nil, stored, encoded)
			if err != nil {
				return err
			}
			same = refs.equalDisplayData(refs.canonicalDisplayData(loaded), encoded)
			return nil
		}); err != nil {
			return true, err
		}
		if same {
			mark()
			return true, nil
		}
	}
	var bodies []largeRowBody
	if !offloadFields[name] {
		// Offloaded records compare revision bodies with the record's own
		// content; their complete members stay in the header.
		if bodies, err = largeRowBodies(ctx, kind, row.before, row.next); err != nil {
			return true, err
		}
	}
	placeholder := largeRowPlaceholders[0]
	left, nativeBodies, err := largeRowHeader(ctx, owner, row.before, bodies, placeholder)
	if err != nil {
		return true, err
	}
	right, oursBodies, err := largeRowHeader(ctx, owner, row.next, bodies, placeholder)
	if err != nil {
		return true, err
	}
	raw, canonicalBodies, rawBodies, err := largeRowCanonicalHeader(ctx, owner, p.frames, canonical, size, kind, bodies, placeholder)
	if err != nil {
		return true, err
	}
	sameStored := true
	for index := range bodies {
		same, err := legacyProjectStoredBodyEqual(ctx, owner, nativeBodies[index], canonical, rawBodies[index])
		if err != nil {
			return true, err
		}
		sameStored = sameStored && same
	}
	// The typed kernel keeps both encodings, the canonical header, the
	// load-normalized and display copies, the four member maps of the merge
	// and its result: four copies of each encoding beyond the encodings.
	baseRaw, ours := []byte(nil), []byte(nil)
	for index, value := range []reflect.Value{left, right} {
		encoding, err := metadataAggregatePayloadEncodingWork(ctx, value, 0)
		if err != nil {
			return true, err
		}
		if err := owner.reserve(ctx, uint64(encoding)+64<<10); err != nil {
			return true, err
		}
		encoded, err := json.Marshal(value.Interface())
		if err != nil {
			return true, err
		}
		if index == 0 {
			baseRaw = encoded
		} else {
			ours = encoded
		}
	}
	if total := uint64(len(baseRaw)) + uint64(len(ours)) + uint64(len(raw)); total > metadataWorkBytes/4 {
		return true, ErrMetadataWorkBudget
	} else if err := owner.reserve(ctx, total*4+64<<10); err != nil {
		return true, err
	}
	refs, err := p.references(ctx, p.requested, owner, left.Interface(), right.Interface(), raw)
	if err != nil {
		return true, err
	}
	if offloadMarked {
		// The complete-row offload comparison above already decided
		// storedRecordEqual; header bodies cannot stand in for it.
		sameStored = false
	}
	if sameStored && storedRecordEqual(p.tx, refs, name, raw, baseRaw) {
		mark()
		return true, nil
	}
	resolved, err := resolveDocumentMarkers(name, raw)
	if err != nil {
		return true, err
	}
	if err := owner.reserve(ctx, uint64(len(resolved))*4+1024); err != nil {
		return true, err
	}
	stored, err := decodeMetadataRecord(kind, resolved, reflect.Value{})
	if err != nil {
		return true, fmt.Errorf("rebase %s record %s: %w", name, row.id, err)
	}
	theirs, err := json.Marshal(stored.Interface())
	if err != nil {
		return true, err
	}
	loaded, normalized, err := largeRowNormalized(owner, ctx, f.field, row.id, stored, bodies)
	if err != nil {
		return true, err
	}
	if !normalized {
		return false, nil
	}
	mark()
	loaded = refs.canonicalDisplayData(loaded)
	// sameLoaded: every canonical body loads as the base body (encoded);
	// keptBodies: every body the writer left unchanged does.
	// mergeMetadataRecord skips a member whose encoding the writer kept.
	for index, body := range bodies {
		if !body.changed {
			continue
		}
		same, err := legacyProjectBodyEqual(ctx, owner, nativeBodies[index], oursBodies[index])
		if err != nil {
			return true, err
		}
		bodies[index].changed = !same
	}
	sameLoaded, keptBodies := true, true
	theirsChanged := make([]bool, len(bodies))
	for index, body := range bodies {
		same, err := legacyProjectBodyEqual(ctx, owner, nativeBodies[index], canonicalBodies[index])
		if err != nil {
			return true, err
		}
		sameLoaded = sameLoaded && same
		keptBodies = keptBodies && (same || body.changed)
		theirsChanged[index] = !same
	}
	if sameLoaded && refs.equalDisplayData(loaded, baseRaw) {
		// The row only lacks what the load fills in: no other write
		// changed it.
		return true, nil
	}
	merged, conflict, err := mergeMetadataRecord(baseRaw, ours, theirs, loaded)
	if err != nil {
		return true, err
	}
	// A body the writer replaced is a string member mergeMetadataRecord
	// decides by equality: ours when the canonical body is still the base
	// (or already ours), otherwise a conflict. Encoded equality is
	// jsonEqual here: an absent string loads as the empty string.
	for index, body := range bodies {
		if conflict || !body.changed || !theirsChanged[index] {
			continue
		}
		same, err := legacyProjectBodyEqual(ctx, owner, oursBodies[index], canonicalBodies[index])
		if err != nil {
			return true, err
		}
		conflict = !same
	}
	if conflict {
		return true, &metadataConflictError{keys: []metadataRecordKey{key}}
	}
	actualLeft, err := decodeMetadataRecord(kind, theirs, left)
	if err != nil {
		return true, err
	}
	actualRight, err := decodeMetadataRecord(kind, merged, right)
	if err != nil {
		return true, err
	}
	// rebaseRow keeps an image the merge reproduced exactly; the framed
	// bodies are the writer's own when every canonical body loads equal.
	mergedBodies := make([]metadataLogicalNode, len(bodies))
	for index, body := range bodies {
		mergedBodies[index] = canonicalBodies[index]
		if body.changed {
			mergedBodies[index] = oursBodies[index]
		}
	}
	if !sameLoaded || !reflect.DeepEqual(left.Interface(), actualLeft.Interface()) {
		if row.before, err = p.stageLargeRow(actualLeft, bodies, canonicalBodies); err != nil {
			return true, err
		}
	}
	if !keptBodies || !reflect.DeepEqual(right.Interface(), actualRight.Interface()) {
		if row.next, err = p.stageLargeRow(actualRight, bodies, mergedBodies); err != nil {
			return true, err
		}
	}
	if id := actualRight.FieldByName("ID").String(); id != row.id {
		if uint64(len(id)) > (metadataWorkBytes-8192)/64 {
			return true, ErrMetadataWorkBudget
		}
		if err := p.work.reserve(ctx, uint64(len(id))*64+8192); err != nil {
			return true, err
		}
		row.id = id
	}
	return true, nil
}
