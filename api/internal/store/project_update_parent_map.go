package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"sync"

	"flow/api/internal/domain"
)

// Only the closed advanced-schedule profile can retain this parent-row plan.
// Native postimages stay native; these normalized JSON parts are SQL inputs.
type legacyScheduleParentMap struct {
	owner        *ResourceRead
	before, next map[string]legacySessionJSONPart
	spools       []*RecordSpool
	once         sync.Once
}

func (p *legacyScheduleParentMap) Close() {
	if p == nil {
		return
	}
	p.once.Do(func() {
		p.before, p.next = nil, nil
		for _, spool := range p.spools {
			spool.Close()
		}
		p.spools = nil
		p.owner.Close()
	})
}

func (p *legacyScheduleParentMap) capture(ctx context.Context, node metadataLogicalNode) (map[string]legacySessionJSONPart, error) {
	spool, err := newMetadataLogicalRecordSpool(ctx)
	if err != nil {
		return nil, err
	}
	p.spools = append(p.spools, spool)
	scratch := &ResourceRead{store: p.owner.store}
	defer scratch.Close()
	if err := scratch.reserve(ctx, logicalJSONStreamWork); err != nil {
		return nil, err
	}
	if err := legacyProjectStreamNode(ctx, scratch, node, spool); err != nil {
		return nil, err
	}
	// This is the literal encoding/json object image, including replacement
	// of invalid UTF-8 keys and last-property precedence after collisions.
	return legacySessionJSONObject(ctx, p.owner, legacySessionSpoolPart(spool))
}

func legacyScheduleParentItems(ctx context.Context, owner *ResourceRead, list metadataLogicalNode) ([]metadataLogicalNode, error) {
	var items []metadataLogicalNode
	err := list.Children(ctx, func(_ reflect.Value, node metadataLogicalNode) error {
		if err := owner.reserve(ctx, uint64(reflect.TypeFor[metadataLogicalNode]().Size())*2+128); err != nil {
			return err
		}
		items = append(items, node)
		return nil
	})
	return items, err
}

func legacyScheduleParentID(ctx context.Context, owner *ResourceRead, item metadataLogicalNode) (string, error) {
	node, err := issueHotField(ctx, item, "ID")
	if err != nil {
		return "", err
	}
	if err := owner.reserve(ctx, node.cost+1024); err != nil {
		return "", err
	}
	value, err := node.Value(ctx, int(node.cost))
	if err != nil {
		return "", err
	}
	return value.String(), nil
}

// The literal rebaseUpdateRecords short-circuits parent-map fields whenever
// any requested hashed update row is missing. An ordinary parent name may
// itself equal such a hash, so prove that same short-circuit rather than
// assuming no key collision. A complete hashed match keeps the original path.
func (p *legacyMetadataWritePlan) scheduleParentRebaseNoop(ctx context.Context, f *legacyMetadataWriteField, owner *ResourceRead) (bool, error) {
	unique := map[string]bool{}
	total := 0
	for parent, before := range f.parentBefore {
		after := f.parentNext[parent]
		same, err := p.equal(ctx, before, after)
		if err != nil {
			return false, err
		}
		if same {
			continue
		}
		scratch := &ResourceRead{store: p.store}
		err = func() error {
			defer scratch.Close()
			left, err := legacyScheduleParentItems(ctx, scratch, before)
			if err != nil {
				return err
			}
			right, err := legacyScheduleParentItems(ctx, scratch, after)
			if err != nil {
				return err
			}
			last := map[string]metadataLogicalNode{}
			for _, item := range left {
				id, err := legacyScheduleParentID(ctx, scratch, item)
				if err != nil {
					return err
				}
				last[id] = item
			}
			for _, item := range right {
				id, err := legacyScheduleParentID(ctx, scratch, item)
				if err != nil {
					return err
				}
				old, found := last[id]
				if !found {
					continue
				}
				same, err := p.equal(ctx, old, item)
				if err != nil || same {
					if err != nil {
						return err
					}
					continue
				}
				key := updateRecordKey(parent, id)
				if p.tx.rebasedMetadata[metadataRecordKey{f.field.name, key}] {
					continue
				}
				total++
				if !unique[key] {
					if err := owner.reserve(ctx, uint64(len(key))*8+1024); err != nil {
						return err
					}
					unique[key] = true
				}
			}
			return nil
		}()
		if err != nil {
			return false, err
		}
	}
	if total == 0 || total > len(unique) {
		return true, nil
	}
	keys := sortedKeys(unique)
	found := 0
	for start := 0; start < len(keys); start += MetadataRecordBatchMaxKeys {
		clause, values := bindList("record_key", keys[start:min(start+MetadataRecordBatchMaxKeys, len(keys))])
		var count int
		if err := p.tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND "+clause,
			append([]any{p.workspace, f.field.name}, values...)...).Scan(&count); err != nil {
			return false, err
		}
		found += count
	}
	if found < total {
		return true, nil
	}
	for _, key := range keys {
		null, err := p.scheduleParentHashNull(ctx, key)
		if err != nil || !null {
			return false, err
		}
	}
	return true, nil
}

func (p *legacyMetadataWritePlan) scheduleParentHashNull(ctx context.Context, key string) (bool, error) {
	owner := &ResourceRead{store: p.store}
	defer owner.Close()
	if err := owner.reserve(ctx, 128<<10); err != nil {
		return false, err
	}
	lock := ""
	if p.tx.dialect != "sqlite" {
		lock = " FOR UPDATE"
	}
	var size int64
	if err := p.tx.QueryRowContext(ctx, "SELECT "+owner.byteLength("data")+" FROM workspace_metadata_records WHERE workspace_key=? AND field='projectUpdates' AND record_key=?"+lock,
		p.workspace, key).Scan(&size); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return true, nil
		}
		return false, err
	}
	if size < 0 || size > metadataLogicalDiskBytes {
		return false, ErrMetadataWorkBudget
	}
	spool, err := newMetadataLogicalRecordSpool(ctx)
	if err != nil {
		return false, err
	}
	defer spool.Close()
	reader := &canonicalMetadataReader{ctx: ctx, tx: p.tx, workspace: p.workspace, key: RecordKey{"projectUpdates", key}, size: size}
	if n, err := io.CopyBuffer(spool, reader, make([]byte, metadataRecordChunkBytes)); err != nil || n != size {
		if err != nil {
			return false, err
		}
		return false, io.ErrUnexpectedEOF
	}
	part := legacySessionSpoolPart(spool)
	scanner := legacySessionJSONScanner(ctx, part)
	if err := scanner.spaces(); err != nil {
		return false, err
	}
	first, err := scanner.peek()
	if err != nil {
		return false, err
	}
	if first == 'n' {
		if err := scanner.literal("null"); err != nil {
			return false, err
		}
		return true, scanner.end()
	}
	if first == '[' {
		if _, err := legacySessionJSONArray(ctx, owner, part); err != nil {
			return false, err
		}
		// The literal hashed-row rebaser decodes metadataUpdate, so a valid
		// parent array at that exact hash has this same typed JSON error.
		var wrapper metadataUpdate
		return false, json.Unmarshal([]byte("[]"), &wrapper)
	}
	// A wrapper/object at a parent-map key retains the original rebaser.
	return false, nil
}

func (p *legacyMetadataWritePlan) rebaseProjectUpdateScheduleParentMap(ctx context.Context, f *legacyMetadataWriteField) (handled bool, err error) {
	if !advancedScheduleStreamSelected(ctx) || f.field.name != "projectUpdates" ||
		f.before.typ != reflect.TypeFor[map[string][]domain.ProjectUpdate]() || f.next.typ != f.before.typ ||
		f.parentBefore == nil || len(f.parentBefore) != len(f.parentNext) {
		return false, nil
	}
	var shape sql.NullString
	if err := p.tx.QueryRowContext(ctx, "SELECT "+p.store.jsonText("data", metadataCollectionsKey+"."+f.field.name)+
		" FROM workspace_states WHERE workspace_key=?", p.workspace).Scan(&shape); err != nil {
		return false, err
	}
	if shape.String != "map" {
		return false, nil
	}
	state := &legacyScheduleParentMap{owner: &ResourceRead{store: p.store}}
	defer func() {
		if !handled {
			state.Close()
		}
	}()
	if err := state.owner.reserve(ctx, 128<<10); err != nil {
		return false, err
	}
	// The ticker cannot alter parent/list identity, ordinal order or bodies.
	for parent, before := range f.parentBefore {
		after, found := f.parentNext[parent]
		if !found {
			return false, nil
		}
		aNil, err := before.isNil(ctx)
		if err != nil {
			return false, err
		}
		bNil, err := after.isNil(ctx)
		if err != nil || aNil != bNil {
			return false, err
		}
		closed, err := func() (bool, error) {
			scratch := &ResourceRead{store: p.store}
			defer scratch.Close()
			left, err := legacyScheduleParentItems(ctx, scratch, before)
			if err != nil {
				return false, err
			}
			right, err := legacyScheduleParentItems(ctx, scratch, after)
			if err != nil || len(left) != len(right) {
				return false, err
			}
			for index := range left {
				closed, err := legacyProjectBodyPatchOnly(ctx, left[index], right[index], false)
				if err != nil || !closed {
					return false, err
				}
			}
			return true, nil
		}()
		if err != nil || !closed {
			return false, err
		}
	}
	if state.before, err = state.capture(ctx, f.before); err != nil {
		return false, err
	}
	if state.next, err = state.capture(ctx, f.next); err != nil {
		return false, err
	}
	if len(state.before) != len(state.next) {
		return false, nil
	}
	mapShape := false
	for parent, part := range state.next {
		if len(parent) > 191 {
			return false, nil
		}
		if _, found := state.before[parent]; !found {
			return false, nil
		}
		invalid, err := func() (bool, error) {
			owner := &ResourceRead{store: p.store}
			defer owner.Close()
			items, err := legacySessionJSONArray(ctx, owner, part)
			if err != nil {
				return false, err
			}
			seen := map[string]bool{}
			for _, item := range items {
				id, err := legacyScheduleParentJSONID(ctx, owner, item)
				if err != nil {
					return false, err
				}
				if id == "" || seen[id] {
					return true, nil
				}
				seen[id] = true
			}
			return false, nil
		}()
		if err != nil {
			return false, err
		}
		mapShape = mapShape || invalid
	}
	if !mapShape {
		// Unique IDs use hashed rows, even when the IDs are overlong.
		return false, nil
	}
	noRebase, err := p.scheduleParentRebaseNoop(ctx, f, state.owner)
	if err != nil || !noRebase {
		return false, err
	}
	f.scheduleParents, f.full = state, true
	return true, nil
}

func legacyScheduleParentJSONID(ctx context.Context, owner *ResourceRead, part legacySessionJSONPart) (string, error) {
	scratch := &ResourceRead{store: owner.store}
	defer scratch.Close()
	fields, err := legacySessionJSONObject(ctx, scratch, part)
	if err != nil {
		return "", err
	}
	// These parts originate from the native ProjectUpdate encoder, whose ID
	// field is exactly "id". Canonical SQL headers use the typed decoder below.
	id, err := legacySessionSmallString(ctx, scratch, fields["id"])
	if err != nil {
		return "", err
	}
	if err := owner.reserve(ctx, uint64(len(id))*2+1024); err != nil {
		return "", err
	}
	return id, nil
}

func legacyScheduleParentCanonicalHeader(ctx context.Context, owner *ResourceRead, frames *metadataLogicalBuilder, part legacySessionJSONPart) ([]byte, error) {
	raw, _, err := legacyProjectCanonicalHeaderFor(ctx, owner, frames, io.NewSectionReader(part.input, part.start, part.size), part.size, reflect.TypeFor[domain.ProjectUpdate]())
	return raw, err
}

func legacyScheduleParentMentions(ctx context.Context, p *legacyMetadataWritePlan, owner *ResourceRead, parent string, previous, next legacySessionJSONPart) error {
	old, err := legacySessionJSONArray(ctx, owner, previous)
	if err != nil {
		return err
	}
	items, err := legacySessionJSONArray(ctx, owner, next)
	if err != nil {
		return err
	}
	positions := map[string][]int{}
	for index, part := range old {
		var id string
		err := func() error {
			scratch := &ResourceRead{store: p.store}
			defer scratch.Close()
			header, err := legacyScheduleParentCanonicalHeader(ctx, scratch, p.frames, part)
			if err != nil {
				return err
			}
			var identity struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(header, &identity); err != nil {
				return err
			}
			if err := owner.reserve(ctx, uint64(len(identity.ID))*8+1024); err != nil {
				return err
			}
			id = identity.ID
			return nil
		}()
		if err != nil {
			return err
		}
		positions[id] = append(positions[id], index)
	}
	seen := map[string]int{}
	for _, part := range items {
		err := func() error {
			scratch := &ResourceRead{store: p.store}
			defer scratch.Close()
			header, err := legacyScheduleParentCanonicalHeader(ctx, scratch, p.frames, part)
			if err != nil {
				return err
			}
			var identity struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(header, &identity); err != nil {
				return err
			}
			if _, exists := seen[identity.ID]; !exists {
				if err := owner.reserve(ctx, uint64(len(identity.ID))*8+1024); err != nil {
					return err
				}
			}
			ordinal := seen[identity.ID]
			seen[identity.ID]++
			var prior []byte
			if at := positions[identity.ID]; ordinal < len(at) {
				prior, err = legacyScheduleParentCanonicalHeader(ctx, scratch, p.frames, old[at[ordinal]])
				if err != nil {
					return err
				}
			}
			return syncNewCommentMentions(ctx, p.tx, p.workspace, parent, header, prior)
		}()
		if err != nil {
			return err
		}
	}
	return nil
}

func (p *legacyMetadataWritePlan) writeProjectUpdateScheduleParentMap(ctx context.Context, f *legacyMetadataWriteField) error {
	state := f.scheduleParents
	if state == nil || !advancedScheduleStreamSelected(ctx) || f.field.name != "projectUpdates" {
		return ErrBoundedMetadataCoverage
	}
	for _, parent := range sortedKeys(state.next) {
		if err := p.writeProjectUpdateScheduleParent(ctx, f, parent, state.next[parent]); err != nil {
			return err
		}
	}
	return nil
}

func (p *legacyMetadataWritePlan) writeProjectUpdateScheduleParent(ctx context.Context, f *legacyMetadataWriteField, parent string, next legacySessionJSONPart) error {
	// A parent with NUL is stored without it on PostgreSQL, and the literal
	// delta matches known and wanted rows under that stored key
	// (storedMetadataRecordMap): the row is replaced like any other parent's,
	// under the key the literal writer sends.
	previous, found := f.scheduleParents.before[parent]
	key := parent
	if p.tx.dialect == "postgres" {
		key = StripNUL(parent)
	}
	if !found {
		return ErrBoundedMetadataCoverage
	}
	owner := &ResourceRead{store: p.store}
	defer owner.Close()
	if err := owner.reserve(ctx, 512<<10); err != nil {
		return err
	}
	lock := ""
	if p.tx.dialect != "sqlite" {
		lock = " FOR UPDATE"
	}
	var order int
	err := p.tx.QueryRowContext(ctx, "SELECT collection_order FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?"+lock,
		p.workspace, f.field.name, key).Scan(&order)
	if errors.Is(err, sql.ErrNoRows) {
		// Every target parent is known in before: the literal known-row delta
		// never recreates a parent another writer deleted.
		return nil
	}
	if err != nil {
		return err
	}
	// Known rows use the native before image, before stored NUL stripping.
	// Canonical SQL contributes only existence and collection order.
	stored, err := newMetadataLogicalRecordSpool(ctx)
	if err != nil {
		return err
	}
	defer stored.Close()
	if err := legacyProjectCopyStoredJSON(ctx, stored, next.reader()); err != nil {
		return err
	}
	same, err := legacySessionRawEqual(ctx, previous, next)
	if err != nil {
		return err
	}
	if same {
		if order == 0 {
			return nil
		}
		_, err := p.tx.ExecContext(ctx, "UPDATE workspace_metadata_records SET collection_order=? WHERE workspace_key=? AND field=? AND record_key=?",
			0, p.workspace, f.field.name, key)
		p.tx.metadataWritten = err == nil || p.tx.metadataWritten
		return err
	}
	if p.tx.dialect == "sqlite" && p.store.db.journalReady.Load() {
		p.tx.journalOrder = recordJournalOrder{p.workspace, f.field.name, parent, int64(order), true}
	}
	var shape sql.NullString
	if err := p.tx.QueryRowContext(ctx, "SELECT "+p.store.jsonText("data", metadataCollectionsKey+"."+f.field.name)+
		" FROM workspace_states WHERE workspace_key=?", p.workspace).Scan(&shape); err != nil {
		return err
	}
	if shape.String != "map" {
		return ErrConcurrentWrite
	}
	stage, err := beginMetadataSQLWriteStage(ctx, p.tx, metadataSQLStageTarget{
		workspace: p.workspace, field: f.field.name, key: key, order: 0,
		dataBytes: int(stored.bytes), searchBytes: -1, projectUpdateParent: true,
	})
	if err != nil {
		return err
	}
	defer stage.Close(context.WithoutCancel(ctx))
	writer, err := stage.writer(ctx, "data")
	if err != nil {
		return err
	}
	if _, err := io.CopyBuffer(writer, legacySessionSpoolPart(stored).reader(), make([]byte, metadataRecordChunkBytes)); err != nil {
		return err
	}
	if err := writer.flush(true); err != nil {
		return err
	}
	if err := stage.writeMetadataRow(ctx); err != nil {
		return err
	}
	if err := legacyScheduleParentMentions(ctx, p, owner, parent, previous, legacySessionSpoolPart(stored)); err != nil {
		return err
	}
	return stage.Close(ctx)
}
