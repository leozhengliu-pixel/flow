package store

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"sync"
	"time"

	"flow/api/internal/domain"
)

type metadataInlineRootPage struct {
	offset int64
	data   []byte
}

// Root scanning rereads short keys after skipping each value. Keep two fixed
// pages so those lookups do not add one SQL statement per metadata property.
type metadataInlineRootReader struct {
	ctx    context.Context
	source io.ReaderAt
	size   int64
	pages  [2]metadataInlineRootPage
	next   int
}

func (r *metadataInlineRootReader) ReadAt(out []byte, offset int64) (int, error) {
	if r.ctx != nil {
		if err := r.ctx.Err(); err != nil {
			return 0, err
		}
	}
	if offset < 0 {
		return 0, ErrMetadataRecordQuery
	}
	read := 0
	for len(out) > 0 && offset < r.size {
		if r.ctx != nil {
			if err := r.ctx.Err(); err != nil {
				return read, err
			}
		}
		start := offset / metadataRecordChunkBytes * metadataRecordChunkBytes
		var page []byte
		for _, cached := range r.pages {
			if cached.offset == start && cached.data != nil {
				page = cached.data
				break
			}
		}
		if page == nil {
			slot := &r.pages[r.next]
			r.next = (r.next + 1) % len(r.pages)
			count := int(min(int64(metadataRecordChunkBytes), r.size-start))
			if cap(slot.data) < count {
				slot.data = make([]byte, count)
			} else {
				slot.data = slot.data[:count]
			}
			slot.offset = -1
			if _, err := r.source.ReadAt(slot.data, start); err != nil {
				return read, err
			}
			slot.offset, page = start, slot.data
		}
		count := copy(out, page[offset-start:])
		offset, read, out = offset+int64(count), read+count, out[count:]
	}
	if len(out) != 0 {
		return read, io.EOF
	}
	return read, nil
}

type metadataInlineRoot struct {
	ctx       context.Context
	source    *metadataInlineRootReader
	fields    map[string]metadataCollectionRange
	store     *SQLiteStore
	tx        *sqlTx
	workspace string
	spool     *RecordSpool
	work      *ResourceRead
	closeOnce sync.Once
	closed    bool
}

func (r *metadataInlineRoot) Close() {
	if r == nil {
		return
	}
	r.closeOnce.Do(func() {
		r.closed = true
		r.source, r.fields = nil, nil
		if r.spool != nil {
			_ = r.spool.Close()
			r.spool = nil
		}
		if r.work != nil {
			r.work.Close()
			r.work = nil
		}
		r.ctx, r.store, r.tx, r.workspace = nil, nil, nil, ""
	})
}

type metadataInlineRootCaptureKey struct{}

// The owner keeps root alive and guarantees no workspace-state write between
// capture and the borrowing call. Row-only fast writes may precede that call.
func withMetadataInlineRootCapture(ctx context.Context, root *metadataInlineRoot) context.Context {
	return context.WithValue(ctx, metadataInlineRootCaptureKey{}, root)
}

func (r *metadataInlineRoot) borrow(ctx context.Context) (_ *metadataInlineRoot, err error) {
	borrowed := &metadataInlineRoot{
		ctx: ctx, fields: r.fields, store: r.store, tx: r.tx, workspace: r.workspace,
		work: &ResourceRead{store: r.store},
	}
	// The shared map is immutable and remains charged to the original owner.
	// Each synchronous borrower owns its descriptor and two cached pages.
	if err := borrowed.work.reserve(ctx, 64<<10); err != nil {
		borrowed.Close()
		return nil, err
	}
	borrowed.source = &metadataInlineRootReader{ctx: ctx, source: r.spool, size: r.source.size}
	return borrowed, nil
}

func (r *metadataInlineRoot) scanner(part metadataCollectionRange) *metadataCollectionScanner {
	return &metadataCollectionScanner{
		ctx: r.ctx, offset: part.offset,
		input: bufio.NewReaderSize(io.NewSectionReader(r.source, part.offset, part.size), metadataRecordChunkBytes),
	}
}

func (r *metadataInlineRoot) shortString(part metadataCollectionRange) (string, error) {
	if part.size > 4096 {
		return "", ErrBoundedMetadataCoverage
	}
	raw := make([]byte, int(part.size))
	if _, err := r.source.ReadAt(raw, part.offset); err != nil {
		return "", err
	}
	var value string
	err := json.Unmarshal(raw, &value)
	return value, err
}

func (s *SQLiteStore) scanMetadataInlineRoot(ctx context.Context, tx *sqlTx, workspace string, size int64) (_ *metadataInlineRoot, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil || tx == nil || tx.database != s.db || tx.dialect != s.dialect || workspace == "" || size < 0 {
		return nil, ErrMetadataRecordQuery
	}
	if s.maxStateBytes > 0 && size > int64(s.maxStateBytes) {
		return nil, fmt.Errorf("workspace state exceeds %d bytes", s.maxStateBytes)
	}
	if captured, _ := ctx.Value(metadataInlineRootCaptureKey{}).(*metadataInlineRoot); captured != nil {
		if captured.closed || captured.spool == nil || captured.source == nil ||
			captured.store != s || captured.tx != tx || captured.workspace != workspace || captured.source.size != size {
			return nil, ErrMetadataRecordQuery
		}
		return captured.borrow(ctx)
	}
	root := &metadataInlineRoot{
		ctx: ctx, store: s, tx: tx, workspace: workspace,
		fields: map[string]metadataCollectionRange{}, work: &ResourceRead{store: s},
	}
	defer func() {
		if err != nil {
			root.Close()
		}
	}()
	// Fixed copy/parser scratch, two cached pages and the closed root-name
	// range map stay admitted until the captured snapshot is released.
	pageBytes := metadataRecordChunkBytes
	event := integrationDeliveryStreamEvent(ctx)
	if (event == "integration_delivery.claimed" || event == "integration_delivery.retry") && size > metadataRecordChunkBytes {
		pageBytes = int(min(int64(MetadataRecordPageMaxBytes), size))
	}
	// Larger SQL reads keep their driver/Scan copies and copy buffer owned;
	// the two file-backed parser pages remain the ordinary fixed size.
	scratch := uint64(128<<10) + uint64(pageBytes-metadataRecordChunkBytes)*4
	if err := root.work.reserve(ctx, scratch); err != nil {
		return nil, err
	}
	root.spool, err = newMetadataLogicalRecordSpool(s.metadataLogicalScratchContext(ctx, workspace))
	if err != nil {
		return nil, err
	}
	if !root.spool.CanAppend(size) {
		return nil, ErrRecordSpoolBudget
	}
	snapshot := &metadataCollectionSnapshot{store: s, tx: tx, workspace: workspace}
	source := &metadataSnapshotReaderAt{ctx: ctx, snapshot: snapshot, size: size, pageBytes: pageBytes}
	count, err := io.CopyBuffer(root.spool, io.NewSectionReader(source, 0, size), make([]byte, pageBytes))
	if err != nil {
		return nil, err
	}
	if count != size {
		return nil, io.ErrUnexpectedEOF
	}
	traceMark(ctx, "inline-root-copy")
	root.source = &metadataInlineRootReader{ctx: ctx, source: root.spool, size: size}
	if err := root.scan(); err != nil {
		return nil, err
	}
	traceMark(ctx, "inline-root-scan")
	return root, nil
}

func (root *metadataInlineRoot) scan() error {
	size := root.source.size
	scanner := root.scanner(metadataCollectionRange{size: size})
	if err := scanner.spaces(); err != nil {
		return err
	}
	if err := scanner.object(func(key, value metadataCollectionRange) error {
		name, err := root.shortString(key)
		if err != nil {
			return err
		}
		if _, known := metadataResidencyFields()[name]; !known && name != metadataCollectionsKey {
			return fmt.Errorf("%w: unknown field %q", ErrBoundedMetadataCoverage, name)
		}
		root.fields[name] = value
		return nil
	}, 0); err != nil {
		return err
	}
	if err := scanner.end(); err != nil {
		return err
	}
	return nil
}

func (r *metadataInlineRoot) shapes() (map[string]string, error) {
	shapes := map[string]string{}
	part, exists := r.fields[metadataCollectionsKey]
	if !exists {
		return shapes, nil
	}
	if part.size > MetadataRecordPageMaxBytes {
		return nil, ErrMetadataPageBudget
	}
	raw := make([]byte, int(part.size))
	if _, err := r.source.ReadAt(raw, part.offset); err != nil {
		return nil, err
	}
	budget := newMetadataHydrationBudget()
	if err := budget.consume(raw); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &shapes); err != nil {
		return nil, err
	}
	if shapes == nil {
		shapes = map[string]string{}
	}
	return shapes, nil
}

func (r *metadataInlineRoot) legacySessionFields(ctx context.Context, owner *ResourceRead) (map[string]legacySessionJSONPart, error) {
	if r == nil || r.closed || r.source == nil || owner == nil || owner.store != r.store {
		return nil, ErrMetadataRecordQuery
	}
	if err := legacySessionCanonicalOwnerOpen(ctx, owner); err != nil {
		return nil, err
	}
	if err := owner.reserve(ctx, uint64(len(r.fields))*512+64<<10); err != nil {
		return nil, err
	}
	fields := make(map[string]legacySessionJSONPart, len(r.fields))
	for name, part := range r.fields {
		fields[name] = legacySessionJSONPart{r.source, part.offset, part.size}
	}
	return fields, ctx.Err()
}

// Match json.Marshal's RawMessage compaction without retaining the raw value.
func compactMetadataInlineJSON(ctx context.Context, dst *bufio.Writer, input io.Reader) error {
	reader := bufio.NewReaderSize(input, 4096)
	quoted, escaped := false, false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		char, err := reader.ReadByte()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if !quoted && (char == ' ' || char == '\n' || char == '\r' || char == '\t') {
			continue
		}
		if escaped {
			escaped = false
		} else if quoted && char == '\\' {
			escaped = true
		} else if char == '"' {
			quoted = !quoted
		} else if quoted {
			switch char {
			case '<', '>', '&':
				if _, err := dst.WriteString(logicalJSONASCIIQuotes[char]); err != nil {
					return err
				}
				continue
			case 0xe2:
				next, _ := reader.Peek(2)
				if len(next) == 2 && next[0] == 0x80 && (next[1] == 0xa8 || next[1] == 0xa9) {
					_, _ = reader.Discard(2)
					escape := `\u2028`
					if next[1] == 0xa9 {
						escape = `\u2029`
					}
					if _, err := dst.WriteString(escape); err != nil {
						return err
					}
					continue
				}
			}
		}
		if err := dst.WriteByte(char); err != nil {
			return err
		}
	}
}

type metadataInlineRootSpoolWriter struct {
	spool *RecordSpool
	size  int64
	limit int64
}

func (w *metadataInlineRootSpoolWriter) Write(raw []byte) (int, error) {
	if int64(len(raw)) > w.limit-w.size {
		return 0, fmt.Errorf("workspace state exceeds %d bytes", w.limit)
	}
	n, err := w.spool.Write(raw)
	w.size += int64(n)
	return n, err
}

// writeStreamedMetadataRootPatch preserves the delta writer's sorted map,
// last-exact-key and RawMessage semantics. Only the final encoded SQL argument
// is materialized, under its own allowance; unrelated values are never decoded.
func (s *SQLiteStore) writeStreamedMetadataRootPatch(ctx context.Context, tx *sqlTx, workspace, workspaceID string, root *metadataInlineRoot, patch map[string]json.RawMessage, owner *ResourceRead, touch bool) error {
	if inline := legacyInlineFieldsPatchFrom(ctx); inline != nil {
		return s.writeLegacyInlineFieldsRootPatch(ctx, tx, workspace, workspaceID, root, patch, owner, touch, inline)
	}
	if session := legacySessionRootPatchFrom(ctx); session != nil {
		return s.writeAgentSessionLifecycleRootPatch(ctx, tx, workspace, workspaceID, root, patch, owner, touch, session)
	}
	spool, err := newMetadataLogicalRecordSpool(ctx)
	if err != nil {
		return err
	}
	defer spool.Close()
	written := &metadataInlineRootSpoolWriter{spool: spool, limit: int64(s.maxStateBytes)}
	// As the delta writer's root, NUL is dropped (PostgreSQL refuses \u0000).
	stripped := &jsonNULStripWriter{dst: written}
	out := bufio.NewWriterSize(stripped, metadataRecordChunkBytes)
	names := make([]string, 0, len(root.fields)+len(patch))
	for name := range root.fields {
		if raw, replaced := patch[name]; replaced && raw == nil {
			continue
		}
		names = append(names, name)
	}
	for name, raw := range patch {
		if _, exists := root.fields[name]; !exists && raw != nil {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	if err := out.WriteByte('{'); err != nil {
		return err
	}
	for index, name := range names {
		if index != 0 {
			if err := out.WriteByte(','); err != nil {
				return err
			}
		}
		key, err := json.Marshal(name)
		if err != nil {
			return err
		}
		if _, err := out.Write(key); err != nil {
			return err
		}
		if err := out.WriteByte(':'); err != nil {
			return err
		}
		var input io.Reader
		if raw, replaced := patch[name]; replaced {
			input = bytes.NewReader(raw)
		} else {
			part := root.fields[name]
			input = io.NewSectionReader(root.source, part.offset, part.size)
		}
		if err := compactMetadataInlineJSON(ctx, out, input); err != nil {
			return err
		}
	}
	if err := out.WriteByte('}'); err != nil {
		return err
	}
	if err := out.Flush(); err != nil {
		return err
	}
	if err := stripped.Finish(); err != nil {
		return err
	}
	if err := owner.reserve(ctx, uint64(written.size)*3+64<<10); err != nil {
		return err
	}
	raw := make([]byte, int(written.size))
	if _, err := spool.ReadAt(raw, 0); err != nil {
		return err
	}
	same := root.source.size == written.size
	var chunk [metadataRecordChunkBytes]byte
	for offset := int64(0); same && offset < written.size; offset += int64(len(chunk)) {
		size := min(int64(len(chunk)), written.size-offset)
		if _, err := root.source.ReadAt(chunk[:size], offset); err != nil {
			return err
		}
		same = bytes.Equal(raw[offset:offset+size], chunk[:size])
	}
	if same {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	tx.metadataWritten = true
	if !touch {
		_, err = tx.ExecContext(ctx, `UPDATE workspace_states SET data=? WHERE workspace_key=?`, raw, workspace)
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE workspace_states SET workspace_id=?,data=?,updated_at=? WHERE workspace_key=?`,
		workspaceID, raw, time.Now().UTC().Format(time.RFC3339Nano), workspace)
	return err
}

func (s *SQLiteStore) writeStreamedInlineMetadataField(ctx context.Context, tx *sqlTx, workspace, name string, size int64, before domain.Bootstrap, target *domain.Bootstrap) error {
	owner := &ResourceRead{store: s}
	defer owner.Close()
	if err := owner.reserve(ctx, 2<<20); err != nil {
		return err
	}
	root, err := s.scanMetadataInlineRoot(ctx, tx, workspace, size)
	if err != nil {
		return err
	}
	defer root.Close()
	shapes, err := root.shapes()
	if err != nil {
		return err
	}
	if shapes[name] != "" {
		return fmt.Errorf("%w: %s is a split field, not an inline field", ErrBoundedMetadataCoverage, name)
	}
	var splitRows int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM workspace_metadata_records WHERE workspace_key=? AND field=?", workspace, name).Scan(&splitRows); err != nil {
		return err
	}
	if splitRows != 0 {
		return fmt.Errorf("%w: inline %s has unexpected split rows", ErrBoundedMetadataCoverage, name)
	}
	after := *target
	replaced, _, err := s.rebaseMetadata(withLegacyMetadataDeltaReadBudget(ctx, owner), tx, workspace, &before, &after, changedMetadataFields(before, after), nil)
	if err != nil {
		return err
	}
	for _, field := range replaced {
		setRebasedField(field, reflect.ValueOf(target).Elem().Field(field.index), reflect.ValueOf(after).Field(field.index))
	}
	changed := changedMetadataFields(before, after)
	if len(changed) == 0 {
		return nil
	}
	if len(changed) != 1 || changed[0].name != name {
		return ErrMetadataRecordQuery
	}
	refs := newIssueReferences(domain.Bootstrap{Users: after.Users})
	return s.writeStreamedMetadataSlowFieldsRoot(ctx, tx, workspace, before, after, []string{name}, refs, root, owner)
}

func (s *SQLiteStore) writeStreamedMetadataSlowFields(ctx context.Context, tx *sqlTx, workspace string, before, target domain.Bootstrap, names []string, refs issueReferences, size int64) (bool, error) {
	owner := &ResourceRead{store: s}
	defer owner.Close()
	if err := owner.reserve(ctx, 2<<20); err != nil {
		return false, err
	}
	root, err := s.scanMetadataInlineRoot(ctx, tx, workspace, size)
	if err != nil {
		return false, err
	}
	defer root.Close()
	if err := s.writeStreamedMetadataSlowFieldsRoot(ctx, tx, workspace, before, target, names, refs, root, owner); err != nil {
		return false, err
	}
	return true, nil
}

func (s *SQLiteStore) writeStreamedMetadataSlowFieldsRoot(ctx context.Context, tx *sqlTx, workspace string, before, target domain.Bootstrap, names []string, refs issueReferences, root *metadataInlineRoot, owner *ResourceRead) error {
	shapes, err := root.shapes()
	if err != nil {
		return err
	}
	partial := reflect.New(reflect.TypeFor[domain.Bootstrap]()).Elem()
	prior := reflect.New(reflect.TypeFor[domain.Bootstrap]()).Elem()
	source, original := reflect.ValueOf(target), reflect.ValueOf(before)
	selected := make(map[string]bool, len(names))
	for _, name := range names {
		field, ok := metadataFieldByName(name)
		if !ok || selected[name] {
			return ErrMetadataRecordQuery
		}
		partial.Field(field.index).Set(source.Field(field.index))
		prior.Field(field.index).Set(original.Field(field.index))
		selected[name] = true
	}
	encoded, err := json.Marshal(partial.Interface())
	if err != nil {
		return err
	}
	partRoot, partShapes, partRecords, err := splitWorkspaceMetadata(encoded)
	if err != nil {
		return err
	}
	encodedBefore, err := json.Marshal(prior.Interface())
	if err != nil {
		return err
	}
	_, _, knownRecords, err := splitWorkspaceMetadata(encodedBefore)
	if err != nil {
		return err
	}
	wanted := map[metadataRecordKey]metadataRecord{}
	for key, value := range partRecords {
		if selected[key.field] {
			wanted[key] = value
		}
	}
	selectedShapes := map[string]string{}
	patch := map[string]json.RawMessage{}
	for _, name := range names {
		patch[name] = partRoot[name]
		if shape, exists := partShapes[name]; exists {
			selectedShapes[name], shapes[name] = shape, shape
		} else {
			delete(shapes, name)
		}
	}
	syncWanted, syncNames := largeRowSyncFields(ctx, wanted, names)
	if err := syncMetadataRecords(withLegacyMetadataDeltaReadBudget(ctx, owner), tx, workspace, refs, selectedShapes, syncWanted, syncNames, knownRecords); err != nil {
		return err
	}
	manifest, err := json.Marshal(shapes)
	if err != nil {
		return err
	}
	patch[metadataCollectionsKey] = manifest
	return s.writeStreamedMetadataRootPatch(ctx, tx, workspace, target.Workspace.ID, root, patch, owner, true)
}
