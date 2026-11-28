package store

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"unicode/utf8"

	"flow/api/internal/domain"
)

// The only display fields changed by refreshLegacyDisplayImage's project
// branch are Lead and Creator. Progress writers may additionally edit the
// explicitly listed count/history fields. Description must remain unchanged.
var legacyProjectProgressFields = map[string]bool{
	"IssueCount": true, "Progress": true, "IssueCountHistory": true,
	"ScopeHistory": true, "CompletedScopeHistory": true,
	"InProgressScopeHistory": true, "ProgressHistory": true,
}

const legacyProjectDirectBytes = 256 << 10

type advancedScheduleStreamKey struct{}

func advancedScheduleStreamSelected(ctx context.Context) bool {
	selected, _ := ctx.Value(advancedScheduleStreamKey{}).(bool)
	return selected
}

func legacyProjectDisplayReferenceOnly(ctx context.Context, before, next metadataLogicalNode) (bool, error) {
	if before.typ == reflect.TypeFor[domain.User]() && next.typ == before.typ {
		left, err := issueHotField(ctx, before, "ID")
		if err != nil {
			return false, err
		}
		right, err := issueHotField(ctx, next, "ID")
		if err != nil {
			return false, err
		}
		return legacyMetadataWriteEqual(ctx, left, right, 0)
	}
	leftNil, err := before.isNil(ctx)
	if err != nil {
		return false, err
	}
	rightNil, err := next.isNil(ctx)
	if err != nil || leftNil != rightNil {
		return false, err
	}
	if leftNil {
		return true, nil
	}
	var users [2]metadataLogicalNode
	for index, pointer := range []metadataLogicalNode{before, next} {
		if err := pointer.Children(ctx, func(_ reflect.Value, user metadataLogicalNode) error {
			users[index] = user
			return nil
		}); err != nil {
			return false, err
		}
	}
	left, err := issueHotField(ctx, users[0], "ID")
	if err != nil {
		return false, err
	}
	right, err := issueHotField(ctx, users[1], "ID")
	if err != nil {
		return false, err
	}
	return legacyMetadataWriteEqual(ctx, left, right, 0)
}

func legacyProjectBodyPatchOnly(ctx context.Context, before, next metadataLogicalNode, progress bool) (bool, error) {
	update := before.typ == reflect.TypeFor[domain.ProjectUpdate]()
	if next.typ != before.typ || before.typ != reflect.TypeFor[domain.Project]() && !update {
		return false, nil
	}
	if update && !advancedScheduleStreamSelected(ctx) {
		return false, nil
	}
	if !before.unavailable && !next.unavailable && !before.blocked && !next.blocked &&
		metadataLogicalFrameOrigin(before) == metadataLogicalFrameOrigin(next) {
		return true, ctx.Err()
	}
	nextFields := make(map[string]metadataLogicalNode, next.typ.NumField())
	if err := next.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
		nextFields[key.String()] = child
		return nil
	}); err != nil {
		return false, err
	}
	allowed := true
	err := before.Children(ctx, func(key reflect.Value, left metadataLogicalNode) error {
		name := key.String()
		if advancedScheduleStreamSelected(ctx) &&
			(!update && name == "Health" || update && (name == "DueAt" || name == "Missing")) {
			return nil
		}
		if progress && legacyProjectProgressFields[name] {
			return nil
		}
		right := nextFields[name]
		if name == "Lead" || name == "Creator" || update && name == "User" {
			same, err := legacyProjectDisplayReferenceOnly(ctx, left, right)
			allowed = allowed && same
			return err
		}
		same, err := legacyMetadataWriteEqual(ctx, left, right, 0)
		allowed = allowed && same
		return err
	})
	return allowed, err
}

func legacyProjectOpaqueField(kind reflect.Type) string {
	if kind == reflect.TypeFor[domain.ProjectUpdate]() {
		return "Body"
	}
	return "Description"
}

func legacyProjectProgressHeader(ctx context.Context, owner *ResourceRead, node metadataLogicalNode) (reflect.Value, metadataLogicalNode, error) {
	bodyField := legacyProjectOpaqueField(node.typ)
	cost := uint64(node.typ.Size())*2 + 128
	type headerField struct {
		name string
		node metadataLogicalNode
	}
	fields := make([]headerField, 0, node.typ.NumField()-1)
	var body metadataLogicalNode
	if err := node.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
		if key.String() == bodyField {
			body = child
			return nil
		}
		fields = append(fields, headerField{key.String(), child})
		return bootstrapBodyAddWork(&cost, child.cost)
	}); err != nil {
		return reflect.Value{}, metadataLogicalNode{}, err
	}
	if err := owner.reserve(ctx, cost); err != nil {
		return reflect.Value{}, metadataLogicalNode{}, err
	}
	value := reflect.New(node.typ).Elem()
	for _, field := range fields {
		decoded, err := field.node.Value(ctx, int(field.node.cost))
		if err != nil {
			return reflect.Value{}, metadataLogicalNode{}, err
		}
		value.FieldByName(field.name).Set(decoded)
	}
	return value, body, nil
}

// Frame the string's decoded bytes directly; a multi-megabyte JSON token is
// never made into a Go string or byte slice. Null keeps the previous string,
// matching encoding/json's repeated-property behavior for a string field.
func legacyProjectFrameString(ctx context.Context, builder *metadataLogicalBuilder, input io.Reader, previous metadataLogicalNode) (metadataLogicalNode, error) {
	node := metadataLogicalNode{
		source: builder.storage, offset: builder.offset,
		typ: reflect.TypeFor[string](), cost: 2*uint64(reflect.TypeFor[string]().Size()) + 128 + 64,
	}
	if err := builder.write(make([]byte, 24)); err != nil {
		return metadataLogicalNode{}, err
	}
	length := uint64(0)
	null, err := VisitMetadataJSONStringChunks(ctx, input, func(part []byte) error {
		if uint64(len(part)) > (uint64(metadataWorkBytes())-node.cost)/2 {
			return ErrMetadataWorkBudget
		}
		if err := builder.write(part); err != nil {
			return err
		}
		length += uint64(len(part))
		node.cost += uint64(len(part)) * 2
		return nil
	})
	if err != nil {
		return metadataLogicalNode{}, err
	}
	if null {
		return previous, nil
	}
	var size [8]byte
	binary.LittleEndian.PutUint64(size[:], length)
	if err := builder.storage.writeAt(size[:], node.offset+16); err != nil {
		return metadataLogicalNode{}, err
	}
	node.size = builder.offset - node.offset
	return node, builder.patch(node)
}

type legacyProjectAdmittedBuffer struct {
	ctx   context.Context
	owner *ResourceRead
	data  bytes.Buffer
}

func (b *legacyProjectAdmittedBuffer) Write(part []byte) (int, error) {
	// Buffer growth and the later typed JSON decode coexist. Container work
	// is admitted separately after the complete small projection is parsed.
	if err := b.owner.reserve(b.ctx, uint64(len(part))*4+64); err != nil {
		return 0, err
	}
	return b.data.Write(part)
}

func legacyProjectCanonicalHeader(ctx context.Context, owner *ResourceRead, frames *metadataLogicalBuilder, input io.ReaderAt, size int64, storedBody ...*metadataCollectionRange) ([]byte, metadataLogicalNode, error) {
	return legacyProjectCanonicalHeaderFor(ctx, owner, frames, input, size, reflect.TypeFor[domain.Project](), storedBody...)
}

func legacyProjectCanonicalHeaderFor(ctx context.Context, owner *ResourceRead, frames *metadataLogicalBuilder, input io.ReaderAt, size int64, kind reflect.Type, storedBody ...*metadataCollectionRange) ([]byte, metadataLogicalNode, error) {
	bodyField := legacyProjectOpaqueField(kind)
	bodyKey := "description"
	if bodyField == "Body" {
		bodyKey = "body"
	}
	for _, output := range storedBody {
		*output = metadataCollectionRange{offset: -1}
	}
	empty, err := frames.writeValue(reflect.ValueOf(""), 0, false)
	if err != nil {
		return nil, metadataLogicalNode{}, err
	}
	body := empty
	output := &legacyProjectAdmittedBuffer{ctx: ctx, owner: owner}
	scanner := metadataCollectionScanner{
		ctx: ctx, input: bufio.NewReaderSize(io.NewSectionReader(input, 0, size), metadataRecordChunkBytes),
	}
	if err := scanner.spaces(); err != nil {
		return nil, body, err
	}
	if prefix, err := scanner.peek(); err != nil {
		return nil, body, err
	} else if prefix == 'n' {
		if err := scanner.literal("null"); err != nil {
			return nil, body, err
		}
		return []byte("null"), body, scanner.end()
	}
	if _, err := output.Write([]byte("{")); err != nil {
		return nil, body, err
	}
	first := true
	err = scanner.object(func(key, value metadataCollectionRange) error {
		// Project's supported producer keys are short. Admit arbitrary SQL
		// spellings before decoding them, preserving folded-field selection.
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
		if name == bodyKey {
			for _, output := range storedBody {
				*output = value
			}
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
		field, found := resourceWindowJSONField(kind, name)
		if found && field.Name == bodyField {
			var prefix [1]byte
			if _, err := input.ReadAt(prefix[:], value.offset); err != nil {
				return err
			}
			if prefix[0] == '"' || prefix[0] == 'n' {
				var err error
				body, err = legacyProjectFrameString(ctx, frames, io.NewSectionReader(input, value.offset, value.size), body)
				if err != nil {
					return err
				}
				_, err = output.Write([]byte(`""`))
				return err
			}
		}
		// Invalid description types pass through to the original typed
		// decoder, retaining its error instead of masking malformed SQL.
		_, err := io.CopyBuffer(output, io.NewSectionReader(input, value.offset, value.size), make([]byte, metadataRecordChunkBytes))
		return err
	}, 0)
	if err != nil {
		return nil, body, err
	}
	if err := scanner.end(); err != nil {
		return nil, body, err
	}
	if _, err := output.Write([]byte("}")); err != nil {
		return nil, body, err
	}
	raw := output.data.Bytes()
	typed, err := resourceWindowTypedJSONWork(ctx, raw, kind)
	if err != nil {
		return nil, body, err
	}
	if err := owner.reserve(ctx, typed); err != nil {
		return nil, body, err
	}
	return raw, body, nil
}

func legacyProjectStreamNode(ctx context.Context, owner *ResourceRead, node metadataLogicalNode, output io.Writer) (err error) {
	stream := &logicalJSONStream{
		ctx: ctx, owner: owner, view: &WebhookAggregateRead{store: owner.store},
		out: bufio.NewWriterSize(output, 64<<10),
	}
	defer func() {
		for _, release := range stream.view.releases {
			release()
		}
		if stream.stage != nil {
			err = errors.Join(err, stream.stage.Close())
		}
	}()
	if err := stream.write(webhookJSONValue{node: node}, 0); err != nil {
		return err
	}
	return stream.out.Flush()
}

// Equality is exact encoded-byte equality, not digest equality. Native invalid
// UTF-8 and alternate SQL escape spellings pass through the same JSON encoder.
func legacyProjectBodyEqual(ctx context.Context, owner *ResourceRead, left, right metadataLogicalNode) (bool, error) {
	var spools [2]*RecordSpool
	for index, node := range []metadataLogicalNode{left, right} {
		spool, err := newMetadataLogicalRecordSpool(ctx)
		if err != nil {
			return false, err
		}
		defer spool.Close()
		spools[index] = spool
		if err := legacyProjectStreamNode(ctx, owner, node, spool); err != nil {
			return false, err
		}
		if _, err := spool.Seek(0, io.SeekStart); err != nil {
			return false, err
		}
	}
	return legacyProjectEqualSpools(ctx, spools[0], spools[1])
}

// storedRecordEqual compares raw map members after RawMessage compaction,
// whereas normalizedMetadataRecord compares a freshly typed JSON encoding.
// Keep the raw escape spelling for this first early exit: invalid native
// UTF-8 and a valid decoded replacement rune need not encode identically.
func legacyProjectStoredBodyEqual(ctx context.Context, owner *ResourceRead, native metadataLogicalNode, input io.ReaderAt, part metadataCollectionRange) (bool, error) {
	if part.offset < 0 {
		return false, nil
	}
	left, err := newMetadataLogicalRecordSpool(ctx)
	if err != nil {
		return false, err
	}
	defer left.Close()
	right, err := newMetadataLogicalRecordSpool(ctx)
	if err != nil {
		return false, err
	}
	defer right.Close()
	if err := legacyProjectStreamNode(ctx, owner, native, left); err != nil {
		return false, err
	}
	output := bufio.NewWriterSize(right, 64<<10)
	if err := compactMetadataInlineJSON(ctx, output, io.NewSectionReader(input, part.offset, part.size)); err != nil {
		return false, err
	}
	if err := output.Flush(); err != nil {
		return false, err
	}
	for _, spool := range []*RecordSpool{left, right} {
		if _, err := spool.Seek(0, io.SeekStart); err != nil {
			return false, err
		}
	}
	return legacyProjectEqualSpools(ctx, left, right)
}

func legacyProjectEqualSpools(ctx context.Context, left, right io.Reader) (bool, error) {
	var buffers [2][metadataRecordChunkBytes]byte
	for {
		a, aErr := io.ReadFull(left, buffers[0][:])
		b, bErr := io.ReadFull(right, buffers[1][:])
		if a != b || !bytes.Equal(buffers[0][:a], buffers[1][:b]) {
			return false, nil
		}
		if aErr != nil || bErr != nil {
			if errors.Is(aErr, io.EOF) && errors.Is(bErr, io.EOF) {
				return true, nil
			}
			if errors.Is(aErr, io.ErrUnexpectedEOF) && errors.Is(bErr, io.ErrUnexpectedEOF) {
				return true, nil
			}
			return false, errors.Join(aErr, bErr)
		}
		if err := ctx.Err(); err != nil {
			return false, err
		}
	}
}

type legacyProjectProgressNode struct {
	header, description metadataLogicalNode
}

func (n legacyProjectProgressNode) nodeType() reflect.Type { return n.header.typ }
func (n legacyProjectProgressNode) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n legacyProjectProgressNode) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	return n.header.Children(ctx, func(key reflect.Value, node metadataLogicalNode) error {
		if key.String() == legacyProjectOpaqueField(n.header.typ) {
			node = n.description
		}
		return visit(key, metadataAggregateStoredNode{node})
	})
}

func (p *legacyMetadataWritePlan) stageProjectProgress(ctx context.Context, value reflect.Value, body metadataLogicalNode) (metadataLogicalNode, error) {
	if err := p.frames.auditJSONLocations(value); err != nil {
		return metadataLogicalNode{}, err
	}
	header, err := p.frames.writeValue(value, 0, false)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	return p.frames.writeAggregateNode(legacyProjectProgressNode{header, body}, 0)
}

func (p *legacyMetadataWritePlan) rebaseProjectProgressRow(ctx context.Context, f *legacyMetadataWriteField, index int) (bool, error) {
	if f.field.name == "projectUpdates" {
		return p.rebaseProjectUpdateScheduleRow(ctx, f, index)
	}
	if f.field.name != "projects" {
		return false, nil
	}
	row := &f.rows[index]
	closed, err := legacyProjectBodyPatchOnly(ctx, row.before, row.next, p.progress != nil || p.streamProjectBackfill(f))
	if err != nil || !closed {
		return false, err
	}
	key := metadataRecordKey{"projects", row.id}
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
	lock := ""
	if p.tx.dialect != "sqlite" {
		lock = " FOR UPDATE"
	}
	var size, order int64
	err = p.tx.QueryRowContext(ctx, "SELECT "+owner.byteLength("data")+",collection_order FROM workspace_metadata_records WHERE workspace_key=? AND field='projects' AND record_key=?"+lock,
		p.workspace, row.id).Scan(&size, &order)
	if errors.Is(err, sql.ErrNoRows) {
		backed, readErr := metadataFieldRecordBacked(ctx, p.tx, p.workspace, "projects", f.before.typ)
		if readErr != nil {
			return true, readErr
		}
		if !backed {
			return false, nil
		}
		if p.tx.rebasedMetadata == nil {
			p.tx.rebasedMetadata = map[metadataRecordKey]bool{}
		}
		p.tx.rebasedMetadata[key] = true
		row.deleted = true
		return true, nil
	}
	if err != nil {
		return true, err
	}
	if p.tx.dialect == "sqlite" && p.store.db.journalReady.Load() {
		p.tx.journalOrder = recordJournalOrder{p.workspace, "projects", row.id, order, true}
	}
	if size < 0 || size > metadataLogicalFrameBytes {
		return true, ErrMetadataWorkBudget
	}
	var input io.ReaderAt
	if size <= legacyProjectDirectBytes {
		if err := owner.reserve(ctx, uint64(size)*3); err != nil {
			return true, err
		}
		var raw []byte
		length := owner.byteLength("data")
		if err := p.tx.QueryRowContext(ctx, "SELECT CASE WHEN "+length+"=? THEN data ELSE NULL END FROM workspace_metadata_records WHERE workspace_key=? AND field='projects' AND record_key=?"+lock,
			size, p.workspace, row.id).Scan(&raw); err != nil {
			return true, err
		}
		if raw == nil || int64(len(raw)) != size {
			return true, ErrConcurrentWrite
		}
		// The ordinary rebase leaves an identical wire preimage alone,
		// including native UTF-8 and time values. Compare before parsing any
		// progress arrays or resolving display references from the SQL row.
		if nativeBody.cost <= 4<<10 {
			body, err := nativeBody.Value(ctx, 4<<10)
			if err != nil {
				return true, err
			}
			left.FieldByName("Description").SetString(body.String())
			base, err := legacyProjectMarshalHeader(ctx, owner, left)
			left.FieldByName("Description").SetString("")
			if err != nil {
				return true, err
			}
			if bytes.Equal(raw, base) {
				p.markProjectRebased(key)
				return true, nil
			}
		}
		input = bytes.NewReader(raw)
	} else {
		spool, err := newMetadataLogicalRecordSpool(ctx)
		if err != nil {
			return true, err
		}
		defer spool.Close()
		if err := spool.checkAppend(size); err != nil {
			return true, err
		}
		reader := &canonicalMetadataReader{ctx: ctx, tx: p.tx, workspace: p.workspace, key: RecordKey{"projects", row.id}, size: size}
		if copied, err := io.CopyBuffer(spool, reader, make([]byte, metadataRecordChunkBytes)); err != nil || copied != size {
			if err == nil {
				err = io.ErrUnexpectedEOF
			}
			return true, err
		}
		input = spool
	}
	var rawBody metadataCollectionRange
	raw, canonicalBody, err := legacyProjectCanonicalHeader(ctx, owner, p.frames, input, size, &rawBody)
	if err != nil {
		return true, err
	}
	sameStoredBody, err := legacyProjectStoredBodyEqual(ctx, owner, nativeBody, input, rawBody)
	if err != nil {
		return true, err
	}
	base, err := legacyProjectMarshalHeader(ctx, owner, left)
	if err != nil {
		return true, err
	}
	p.markProjectRebased(key)
	if sameStoredBody && bytes.Equal(base, raw) {
		return true, nil
	}
	refs, err := p.references(ctx, p.requested, owner, nil, nil, base, raw)
	if err != nil {
		return true, err
	}
	if sameStoredBody && storedRecordEqual(p.tx, refs, "projects", raw, base) {
		return true, nil
	}
	stored, err := decodeMetadataRecord(left.Type(), raw, reflect.Value{})
	if err != nil {
		return true, err
	}
	theirs, err := encodeOwnedMetadataRecord(ctx, owner, stored)
	if err != nil {
		return true, err
	}
	loaded, err := encodeOwnedMetadataRecord(ctx, owner, normalizedMetadataRecordValue(f.field, row.id, stored))
	if err != nil {
		return true, err
	}
	loaded = refs.canonicalDisplayData(loaded)
	sameLoadedBody, err := legacyProjectBodyEqual(ctx, owner, nativeBody, canonicalBody)
	if err != nil {
		return true, err
	}
	if sameLoadedBody && refs.equalDisplayData(loaded, base) {
		return true, nil
	}
	ours := base
	right := left
	if metadataLogicalFrameOrigin(row.before) != metadataLogicalFrameOrigin(row.next) {
		right, _, err = legacyProjectProgressHeader(ctx, owner, row.next)
		if err != nil {
			return true, err
		}
		ours, err = legacyProjectMarshalHeader(ctx, owner, right)
		if err != nil {
			return true, err
		}
	}
	merged, conflict, err := mergeMetadataRecord(base, ours, theirs, loaded)
	if err != nil {
		return true, err
	}
	if conflict {
		return true, &metadataConflictError{keys: []metadataRecordKey{key}}
	}
	actualLeft, err := decodeMetadataRecord(left.Type(), theirs, left)
	if err != nil {
		return true, err
	}
	actualRight, err := decodeMetadataRecord(right.Type(), merged, right)
	if err != nil {
		return true, err
	}
	// Once the original rebaser decodes a merged record, its unchanged body
	// also comes from the JSON-normalized canonical record. The early exits
	// above preserve native strings exactly when the original leaves them.
	row.before, err = p.stageProjectProgress(ctx, actualLeft, canonicalBody)
	if err != nil {
		return true, err
	}
	row.next, err = p.stageProjectProgress(ctx, actualRight, canonicalBody)
	if err != nil {
		return true, err
	}
	id := actualRight.FieldByName("ID").String()
	if id != row.id {
		if err := p.work.reserve(ctx, uint64(len(id))*2+128); err != nil {
			return true, err
		}
		row.id = id
	}
	return true, nil
}

func (p *legacyMetadataWritePlan) markProjectRebased(key metadataRecordKey) {
	if p.tx.rebasedMetadata == nil {
		p.tx.rebasedMetadata = map[metadataRecordKey]bool{}
	}
	p.tx.rebasedMetadata[key] = true
}

func legacyProjectMarshalHeader(ctx context.Context, owner *ResourceRead, value reflect.Value) ([]byte, error) {
	encoding, err := metadataRecordEncodingWork(ctx, value)
	if err != nil {
		return nil, err
	}
	if err := owner.reserve(ctx, uint64(encoding)*4+64<<10); err != nil {
		return nil, err
	}
	return marshalMetadataJSON(value.Interface())
}

// Strip exactly the same JSON string escape as StripJSONNUL, including when
// the escape crosses an input page or a SQL argument boundary. Bytes up to
// the next quote (or, inside a string, the next quote or backslash) are
// copied as one run; only escapes are examined byte by byte.
func legacyProjectCopyStoredJSON(ctx context.Context, output io.Writer, input io.Reader) error {
	reader := bufio.NewReaderSize(input, metadataRecordChunkBytes)
	writer := bufio.NewWriterSize(output, metadataRecordChunkBytes)
	inString := false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if reader.Buffered() == 0 {
			if _, err := reader.Peek(1); errors.Is(err, io.EOF) {
				return writer.Flush()
			} else if err != nil {
				return err
			}
		}
		chunk, _ := reader.Peek(reader.Buffered())
		run, escape := 0, false
		for run < len(chunk) {
			if !inString {
				next := bytes.IndexByte(chunk[run:], '"')
				if next < 0 {
					run = len(chunk)
					break
				}
				run, inString = run+next+1, true
				continue
			}
			next := bytes.IndexAny(chunk[run:], legacyProjectStringStops)
			if next < 0 {
				run = len(chunk)
				break
			}
			run += next
			if chunk[run] != '"' {
				escape = true
				break
			}
			run, inString = run+1, false
		}
		if _, err := writer.Write(chunk[:run]); err != nil {
			return err
		}
		if _, err := reader.Discard(run); err != nil {
			return err
		}
		if !escape {
			continue
		}
		char, err := reader.ReadByte()
		if err != nil {
			return err
		}
		next, err := reader.ReadByte()
		if err != nil {
			return err
		}
		if next == 'u' {
			suffix, _ := reader.Peek(4)
			if bytes.Equal(suffix, []byte("0000")) {
				_, _ = reader.Discard(4)
				continue
			}
		}
		if err := writer.WriteByte(char); err != nil {
			return err
		}
		if err := writer.WriteByte(next); err != nil {
			return err
		}
	}
}

// legacyProjectStringStops are the bytes that end a run inside a string: its
// closing quote and an escape.
const legacyProjectStringStops = `"\`

// Match metadataSearchText's map/interface round-trip and character cap.
// Description is streamed separately, including when search indexing is
// intentionally uncapped; nested status objects keep Marshal's sorted keys.
func legacyProjectSearchContent(ctx context.Context, owner *ResourceRead, header []byte, body metadataLogicalNode, output io.Writer) error {
	var fields struct {
		Name, Summary string
		Status        any
	}
	if err := json.Unmarshal(StripJSONNUL(header), &fields); err != nil {
		return err
	}
	limit, used := owner.store.db.searchMaxChars, 0
	emit := func(part []byte) error {
		if limit > 0 {
			remaining := limit - used
			if remaining <= 0 {
				return ctx.Err()
			}
			count := utf8.RuneCount(part)
			if count > remaining {
				count, cut := 0, 0
				for cut < len(part) && count < remaining {
					_, size := utf8.DecodeRune(part[cut:])
					cut += size
					count++
				}
				part = part[:cut]
				used = limit
			} else {
				used += count
			}
		}
		_, err := output.Write(part)
		return err
	}
	for index, name := range []string{"name", "title", "displayName", "email", "summary", "description", "content", "domains", "version", "status", "tier", "scope", "resource"} {
		if index > 0 {
			if err := emit([]byte(" ")); err != nil {
				return err
			}
		}
		switch name {
		case "name":
			if err := emit([]byte(fields.Name)); err != nil {
				return err
			}
		case "summary":
			if err := emit([]byte(fields.Summary)); err != nil {
				return err
			}
		case "description":
			if body.cost <= 4<<10 {
				value, err := body.Value(ctx, 4<<10)
				if err != nil {
					return err
				}
				raw, err := json.Marshal(value.String())
				if err != nil {
					return err
				}
				var text string
				if err := json.Unmarshal(StripJSONNUL(raw), &text); err != nil {
					return err
				}
				if err := emit([]byte(text)); err != nil {
					return err
				}
				continue
			}
			spool, err := newMetadataLogicalRecordSpool(ctx)
			if err != nil {
				return err
			}
			err = func() error {
				defer spool.Close()
				if err := legacyProjectStreamNode(ctx, owner, body, spool); err != nil {
					return err
				}
				if _, err := spool.Seek(0, io.SeekStart); err != nil {
					return err
				}
				_, err := VisitMetadataJSONStringChunks(ctx, spool, func(part []byte) error {
					for {
						at := bytes.IndexByte(part, 0)
						if at < 0 {
							return emit(part)
						}
						if err := emit(part[:at]); err != nil {
							return err
						}
						part = part[at+1:]
					}
				})
				return err
			}()
			if err != nil {
				return err
			}
		case "status":
			raw, err := json.Marshal(fields.Status)
			if err != nil {
				return err
			}
			if err := emit(raw); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

type legacyProjectByteCounter int

func (c *legacyProjectByteCounter) Write(part []byte) (int, error) {
	// This is a streamed SQL payload length, not a retained heap grant.
	// Spool limits and the original transaction admission own its bounds.
	if len(part) > int(^uint(0)>>1)-int(*c) {
		return 0, ErrMetadataWorkBudget
	}
	*c += legacyProjectByteCounter(len(part))
	return len(part), nil
}

func (p *legacyMetadataWritePlan) writeProjectProgressRow(ctx context.Context, f *legacyMetadataWriteField, row *legacyMetadataWriteRow, order int) (bool, error) {
	if f.field.name != "projects" || row.before.typ == nil || row.next.typ == nil {
		return false, nil
	}
	closed, err := legacyProjectBodyPatchOnly(ctx, row.before, row.next, p.progress != nil || p.streamProjectBackfill(f))
	if err != nil || !closed {
		return false, err
	}
	owner := &ResourceRead{store: p.store}
	defer owner.Close()
	if err := owner.reserve(ctx, 512<<10); err != nil {
		return true, err
	}
	right, body, err := legacyProjectProgressHeader(ctx, owner, row.next)
	if err != nil {
		return true, err
	}
	work, err := metadataAggregatePayloadEncodingWork(ctx, right, 0)
	if err != nil {
		return true, err
	}
	if err := owner.reserve(ctx, uint64(work)*2+4096); err != nil {
		return true, err
	}
	next, err := json.Marshal(right.Interface())
	if err != nil {
		return true, err
	}
	count, err := issueHotField(ctx, row.before, "IssueCount")
	if err != nil {
		return true, err
	}
	priorCount, err := count.Value(ctx, 4096)
	if err != nil {
		return true, err
	}
	changedCount := priorCount.Int() != right.FieldByName("IssueCount").Int()
	if !changedCount && p.progress != nil {
		// Issue writes also change the scope recorded in the latest history
		// point. Like IssueCount, that integer is independent of display
		// references. When it differs, skip decoding the old histories again.
		changedCount, err = legacyProjectProgressScopeChanged(ctx, row.before, right.Interface().(domain.Project).ProgressHistory)
		if err != nil {
			return true, err
		}
	}
	if !changedCount {
		left, _, err := legacyProjectProgressHeader(ctx, owner, row.before)
		if err != nil {
			return true, err
		}
		work, err := metadataAggregatePayloadEncodingWork(ctx, left, 0)
		if err != nil {
			return true, err
		}
		if err := owner.reserve(ctx, uint64(work)*2+4096); err != nil {
			return true, err
		}
		previous, err := json.Marshal(left.Interface())
		if err != nil {
			return true, err
		}
		refs, err := p.references(ctx, p.actual, owner, nil, nil, previous, next)
		if err != nil {
			return true, err
		}
		if refs.equalDisplayData(previous, next) {
			return true, nil
		}
	}
	data, dataBytes, closeData, err := legacyProjectProgressData(ctx, owner, next, body)
	if err != nil {
		return true, err
	}
	defer closeData()
	if dataBytes <= legacyProjectDirectBytes {
		return true, p.writeProjectProgressDirect(ctx, owner, row.id, order, data, dataBytes, next, body)
	}
	var searchBytes legacyProjectByteCounter
	if err := legacyProjectSearchContent(ctx, owner, next, body, &searchBytes); err != nil {
		return true, err
	}
	// Reserve the literal row and search SQL charges before growing staging
	// input. Counting uses fixed buffers and the immutable final body frames.
	stage, err := beginMetadataSQLWriteStage(ctx, p.tx, metadataSQLStageTarget{
		workspace: p.workspace, field: "projects", key: row.id, order: order,
		dataBytes: dataBytes, searchBytes: int(searchBytes),
	})
	if err != nil {
		return true, err
	}
	defer func() {
		_ = stage.Close(context.WithoutCancel(ctx))
	}()
	writer, err := stage.writer(ctx, "data")
	if err != nil {
		return true, err
	}
	if _, err := io.CopyBuffer(writer, data, make([]byte, metadataRecordChunkBytes)); err != nil {
		return true, err
	}
	if err := writer.flush(true); err != nil {
		return true, err
	}
	if err := stage.writeMetadataRow(ctx); err != nil {
		return true, err
	}
	// legacyProjectBodyPatchOnly proved the comments did not change.
	if err := writeCustomerFilterRecord(ctx, p.tx, p.workspace, "projects", row.id, next); err != nil {
		return true, err
	}
	if err := writeReleaseIssueRecords(ctx, p.tx, p.workspace, "projects", row.id, next); err != nil {
		return true, err
	}
	search, err := stage.writer(ctx, "content")
	if err != nil {
		return true, err
	}
	if err := legacyProjectSearchContent(ctx, owner, next, body, search); err != nil {
		return true, err
	}
	if err := search.flush(true); err != nil {
		return true, err
	}
	if err := stage.writeSearchDocument(ctx); err != nil {
		return true, err
	}
	return true, stage.Close(ctx)
}

func legacyProjectProgressScopeChanged(ctx context.Context, before metadataLogicalNode, next []domain.ProjectProgressHistoryPoint) (bool, error) {
	history, err := issueHotField(ctx, before, "ProgressHistory")
	if err != nil {
		return false, err
	}
	count := 0
	var last metadataLogicalNode
	err = history.Children(ctx, func(_ reflect.Value, point metadataLogicalNode) error {
		count++
		last = point
		return nil
	})
	if err != nil || count != len(next) || count == 0 {
		return count != len(next), err
	}
	scope, err := issueHotField(ctx, last, "ScopeCount")
	if err != nil {
		return false, err
	}
	value, err := scope.Value(ctx, 4096)
	return err == nil && value.Int() != int64(next[count-1].ScopeCount), err
}

func (p *legacyMetadataWritePlan) writeProjectProgressDirect(ctx context.Context, owner *ResourceRead, id string, order int, data io.Reader, size int, header []byte, body metadataLogicalNode) error {
	// The search projection is bounded by the encoded row plus its separators.
	// Admit the row, search buffer and the driver copies before materializing.
	if err := owner.reserve(ctx, uint64(size)*6+4096); err != nil {
		return err
	}
	raw := make([]byte, size)
	if _, err := io.ReadFull(data, raw); err != nil {
		return err
	}
	var search bytes.Buffer
	if err := legacyProjectSearchContent(ctx, owner, header, body, &search); err != nil {
		return err
	}
	if _, err := p.tx.ExecContext(ctx, metadataSQLStageLegacyRowSQL, p.workspace, "projects", id, order, raw); err != nil {
		return err
	}
	p.tx.metadataWritten = true
	return syncMetadataSearchContent(ctx, p.tx, p.workspace, "projects", id, search.String())
}

// The typed header already holds the final JSON for every field except
// description. Splice the streamed string into that encoding instead of
// visiting every history point again to produce the same JSON.
func legacyProjectProgressData(ctx context.Context, owner *ResourceRead, header []byte, body metadataLogicalNode) (io.Reader, int, func(), error) {
	const description = `,"description":`
	header = StripJSONNUL(header)
	at := bytes.Index(header, []byte(description+`"",`))
	if at < 0 {
		return nil, 0, nil, ErrBoundedMetadataCoverage
	}
	// The preceding Project fields are strings or slices of strings, so
	// the first unescaped occurrence is the top-level description.
	at += len(description)
	prefix, suffix := header[:at], header[at+2:]
	var text io.Reader
	var size int
	closeBody := func() {}
	if body.cost <= 4<<10 {
		value, err := body.Value(ctx, 4<<10)
		if err != nil {
			return nil, 0, nil, err
		}
		raw, err := json.Marshal(value.String())
		if err != nil {
			return nil, 0, nil, err
		}
		raw = StripJSONNUL(raw)
		text, size = bytes.NewReader(raw), len(raw)
	} else {
		spool, err := newMetadataLogicalRecordSpool(ctx)
		if err != nil {
			return nil, 0, nil, err
		}
		writer := &jsonNULStripWriter{dst: spool}
		err = legacyProjectStreamNode(ctx, owner, body, writer)
		if err == nil {
			err = writer.Finish()
		}
		if err != nil {
			spool.Close()
			return nil, 0, nil, err
		}
		end, err := spool.Seek(0, io.SeekCurrent)
		if err == nil {
			_, err = spool.Seek(0, io.SeekStart)
		}
		if err != nil {
			spool.Close()
			return nil, 0, nil, err
		}
		text, size = spool, int(end)
		closeBody = func() { _ = spool.Close() }
	}
	return io.MultiReader(bytes.NewReader(prefix), text, bytes.NewReader(suffix)), len(prefix) + size + len(suffix), closeBody, nil
}
