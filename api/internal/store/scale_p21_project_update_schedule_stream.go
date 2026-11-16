package store

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"reflect"

	"flow/api/internal/domain"
)

// Decode the original metadataUpdate wrapper with its Item replaced by null,
// retaining the last folded Item property's exact range separately.
func legacyScheduleUpdateItem(ctx context.Context, owner *ResourceRead, input io.ReaderAt, size int64) (metadataCollectionRange, error) {
	item := metadataCollectionRange{offset: -1}
	output := &legacyProjectAdmittedBuffer{ctx: ctx, owner: owner}
	scanner := metadataCollectionScanner{ctx: ctx, input: bufio.NewReaderSize(io.NewSectionReader(input, 0, size), metadataRecordChunkBytes)}
	if err := scanner.spaces(); err != nil {
		return item, err
	}
	if prefix, err := scanner.peek(); err != nil {
		return item, err
	} else if prefix == 'n' {
		if err := scanner.literal("null"); err != nil {
			return item, err
		}
		return item, scanner.end()
	}
	if _, err := output.Write([]byte("{")); err != nil {
		return item, err
	}
	first := true
	err := scanner.object(func(key, value metadataCollectionRange) error {
		if err := owner.reserve(ctx, uint64(key.size)*8+128); err != nil {
			return err
		}
		rawKey := make([]byte, int(key.size))
		if _, err := input.ReadAt(rawKey, key.offset); err != nil {
			return err
		}
		var name string
		if err := json.Unmarshal(rawKey, &name); err != nil {
			return err
		}
		if !first {
			if _, err := output.Write([]byte(",")); err != nil {
				return err
			}
		}
		first = false
		if _, err := output.Write(rawKey); err != nil {
			return err
		}
		if _, err := output.Write([]byte(":")); err != nil {
			return err
		}
		field, found := resourceWindowJSONField(reflect.TypeFor[metadataUpdate](), name)
		if found && field.Name == "Item" {
			item = value
			_, err := output.Write([]byte("null"))
			return err
		}
		_, err := io.CopyBuffer(output, io.NewSectionReader(input, value.offset, value.size), make([]byte, metadataRecordChunkBytes))
		return err
	}, 0)
	if err != nil {
		return item, err
	}
	if err := scanner.end(); err != nil {
		return item, err
	}
	if _, err := output.Write([]byte("}")); err != nil {
		return item, err
	}
	var wrapper metadataUpdate
	if err := json.Unmarshal(output.data.Bytes(), &wrapper); err != nil {
		return item, err
	}
	return item, nil
}

func (p *legacyMetadataWritePlan) rebaseProjectUpdateScheduleRow(ctx context.Context, f *legacyMetadataWriteField, index int) (bool, error) {
	if f.field.name != "projectUpdates" || !advancedScheduleStreamSelected(ctx) {
		return false, nil
	}
	row := &f.rows[index]
	closed, err := legacyProjectBodyPatchOnly(ctx, row.before, row.next, false)
	if err != nil || !closed {
		return false, err
	}
	key := metadataRecordKey{"projectUpdates", updateRecordKey(row.parent, row.id)}
	if p.tx.rebasedMetadata[key] {
		return true, nil
	}
	owner := &ResourceRead{store: p.store}
	defer owner.Close()
	if err := owner.reserve(ctx, 512<<10); err != nil {
		return true, err
	}
	left, nativeBody, err := legacyProjectProgressHeader(ctx, owner, row.before)
	if err != nil {
		return true, err
	}
	right, _, err := legacyProjectProgressHeader(ctx, owner, row.next)
	if err != nil {
		return true, err
	}
	lock := ""
	if p.tx.dialect != "sqlite" {
		lock = " FOR UPDATE"
	}
	var size, order int64
	err = p.tx.QueryRowContext(ctx, "SELECT "+owner.byteLength("data")+",collection_order FROM workspace_metadata_records WHERE workspace_key=? AND field='projectUpdates' AND record_key=?"+lock,
		p.workspace, key.key).Scan(&size, &order)
	if errors.Is(err, sql.ErrNoRows) {
		backed, readErr := metadataFieldRecordBacked(ctx, p.tx, p.workspace, f.field.name, f.before.typ)
		if readErr != nil {
			return true, readErr
		}
		if !backed {
			return false, nil
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
		p.tx.journalOrder = recordJournalOrder{p.workspace, f.field.name, key.key, order, true}
	}
	if size < 0 || size > metadataLogicalDiskBytes {
		return true, ErrMetadataWorkBudget
	}
	spool, err := newMetadataLogicalRecordSpool(ctx)
	if err != nil {
		return true, err
	}
	defer spool.Close()
	if !spool.CanAppend(size) {
		return true, ErrRecordSpoolBudget
	}
	reader := &canonicalMetadataReader{ctx: ctx, tx: p.tx, workspace: p.workspace, key: RecordKey{f.field.name, key.key}, size: size}
	if copied, err := io.CopyBuffer(spool, reader, make([]byte, metadataRecordChunkBytes)); err != nil || copied != size {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return true, err
	}
	item, err := legacyScheduleUpdateItem(ctx, owner, spool, size)
	if err != nil {
		return true, err
	}
	if p.tx.rebasedMetadata == nil {
		p.tx.rebasedMetadata = map[metadataRecordKey]bool{}
	}
	p.tx.rebasedMetadata[key] = true
	if item.offset < 0 {
		return true, nil
	}
	itemReader := io.NewSectionReader(spool, item.offset, item.size)
	var rawBody metadataCollectionRange
	var raw []byte
	var canonicalBody metadataLogicalNode
	var null [4]byte
	if item.size == 4 {
		_, err = itemReader.ReadAt(null[:], 0)
	}
	if item.size == 4 && bytes.Equal(null[:], []byte("null")) {
		raw = []byte("null")
		rawBody.offset = -1
		canonicalBody, err = p.frames.writeValue(reflect.ValueOf(""), 0, false)
	} else {
		raw, canonicalBody, err = legacyProjectCanonicalHeaderFor(ctx, owner, p.frames, itemReader, item.size, reflect.TypeFor[domain.ProjectUpdate](), &rawBody)
	}
	if err != nil {
		return true, err
	}
	sameStoredBody, err := legacyProjectStoredBodyEqual(ctx, owner, nativeBody, itemReader, rawBody)
	if err != nil {
		return true, err
	}
	for _, value := range []reflect.Value{left, right} {
		work, err := metadataAggregatePayloadEncodingWork(ctx, value, 0)
		if err != nil {
			return true, err
		}
		if err := owner.reserve(ctx, uint64(work)*4+64<<10); err != nil {
			return true, err
		}
	}
	refs, err := p.references(ctx, p.requested, owner, left.Interface(), right.Interface(), raw)
	if err != nil {
		return true, err
	}
	base, err := json.Marshal(left.Interface())
	if err != nil {
		return true, err
	}
	if sameStoredBody && refs.equalDisplayData(raw, base) {
		return true, nil
	}
	stored, err := decodeMetadataRecord(left.Type(), raw, left)
	if err != nil {
		return true, err
	}
	theirs, err := json.Marshal(stored.Interface())
	if err != nil {
		return true, err
	}
	sameLoadedBody, err := legacyProjectBodyEqual(ctx, owner, nativeBody, canonicalBody)
	if err != nil {
		return true, err
	}
	if sameLoadedBody && refs.equalDisplayData(refs.canonicalDisplayData(theirs), base) {
		return true, nil
	}
	ours, err := json.Marshal(right.Interface())
	if err != nil {
		return true, err
	}
	merged, conflict, err := mergeMetadataRecord(base, ours, theirs, theirs)
	if err != nil {
		return true, err
	}
	if conflict {
		return true, &metadataConflictError{keys: []metadataRecordKey{key}}
	}
	actualRight, err := decodeMetadataRecord(right.Type(), merged, right)
	if err != nil {
		return true, err
	}
	row.before, err = p.stageProjectProgress(ctx, stored, canonicalBody)
	if err == nil {
		row.next, err = p.stageProjectProgress(ctx, actualRight, canonicalBody)
	}
	return true, err
}

func legacyScheduleUpdateStream(ctx context.Context, owner *ResourceRead, parent string, item metadataLogicalNode, output io.Writer) error {
	encoded, err := json.Marshal(parent)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(output, `{"parent":`); err != nil {
		return err
	}
	if _, err := output.Write(encoded); err != nil {
		return err
	}
	if _, err := io.WriteString(output, `,"item":`); err != nil {
		return err
	}
	if err := legacyProjectStreamNode(ctx, owner, item, output); err != nil {
		return err
	}
	_, err = io.WriteString(output, "}")
	return err
}

func (p *legacyMetadataWritePlan) writeProjectUpdateScheduleRow(ctx context.Context, f *legacyMetadataWriteField, row *legacyMetadataWriteRow) (bool, error) {
	if f.field.name != "projectUpdates" || !advancedScheduleStreamSelected(ctx) || row.before.typ == nil || row.next.typ == nil {
		return false, nil
	}
	closed, err := legacyProjectBodyPatchOnly(ctx, row.before, row.next, false)
	if err != nil || !closed {
		return false, err
	}
	owner := &ResourceRead{store: p.store}
	defer owner.Close()
	if err := owner.reserve(ctx, 512<<10); err != nil {
		return true, err
	}
	var values [2][]byte
	for index, node := range []metadataLogicalNode{row.before, row.next} {
		header, _, err := legacyProjectProgressHeader(ctx, owner, node)
		if err != nil {
			return true, err
		}
		work, err := metadataAggregatePayloadEncodingWork(ctx, header, 0)
		if err != nil {
			return true, err
		}
		if err := owner.reserve(ctx, uint64(work)*4+4096); err != nil {
			return true, err
		}
		values[index], err = json.Marshal(header.Interface())
		if err != nil {
			return true, err
		}
	}
	refs, err := p.references(ctx, p.actual, owner, nil, nil, values[0], values[1])
	if err != nil {
		return true, err
	}
	if refs.equalDisplayData(values[0], values[1]) {
		return true, nil
	}
	key := updateRecordKey(row.parent, row.id)
	orders, err := p.orders(ctx, f.field.name, []string{key})
	if err != nil {
		return true, err
	}
	order, found := orders[key]
	if !found {
		return true, ErrConcurrentWrite
	}
	spool, err := newMetadataLogicalRecordSpool(ctx)
	if err != nil {
		return true, err
	}
	defer spool.Close()
	if err := legacyScheduleUpdateStream(ctx, owner, row.parent, row.next, spool); err != nil {
		return true, err
	}
	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		return true, err
	}
	var count legacyProjectByteCounter
	if err := legacyProjectCopyStoredJSON(ctx, &count, spool); err != nil {
		return true, err
	}
	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		return true, err
	}
	stage, err := beginMetadataSQLWriteStage(ctx, p.tx, metadataSQLStageTarget{
		workspace: p.workspace, field: f.field.name, key: key, order: order,
		dataBytes: int(count), searchBytes: -1,
	})
	if err != nil {
		return true, err
	}
	defer stage.Close(context.WithoutCancel(ctx))
	writer, err := stage.writer(ctx, "data")
	if err != nil {
		return true, err
	}
	if err := legacyProjectCopyStoredJSON(ctx, writer, spool); err != nil {
		return true, err
	}
	if err := writer.flush(true); err != nil {
		return true, err
	}
	if err := stage.writeMetadataRow(ctx); err != nil {
		return true, err
	}
	var wrapped [2][]byte
	for index, value := range values {
		wrapped[index], err = json.Marshal(metadataUpdate{Parent: row.parent, Item: value})
		if err != nil {
			return true, err
		}
	}
	if err := syncProjectUpdateMentions(ctx, p.tx, p.workspace, key, StripJSONNUL(wrapped[1]), wrapped[0]); err != nil {
		return true, err
	}
	return true, stage.Close(ctx)
}
