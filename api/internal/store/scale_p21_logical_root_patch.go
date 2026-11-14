package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

const metadataLogicalRootChunkBytes = 1 << 20

type metadataLogicalRootSnapshotKey struct{}

type metadataLogicalRootSnapshots struct {
	store     *SQLiteStore
	tx        *sqlTx
	workspace string
	read      *metadataLogicalRootSnapshot
	closed    bool
}

type metadataLogicalRootSnapshot struct {
	read   *MetadataCollectionRead
	fields map[string]metadataCollectionRange
	shapes map[string]string
	stamp  copyStamp
}

func (s *SQLiteStore) withMetadataLogicalRootSnapshots(ctx context.Context, tx *sqlTx, workspace string) (context.Context, func()) {
	cache := &metadataLogicalRootSnapshots{store: s, tx: tx, workspace: workspace}
	return context.WithValue(ctx, metadataLogicalRootSnapshotKey{}, cache), func() {
		if !cache.closed {
			cache.closed = true
			if cache.read != nil {
				_ = cache.read.read.Close()
				cache.read = nil
			}
		}
	}
}

func (s *SQLiteStore) metadataLogicalRootSnapshot(ctx context.Context, tx *sqlTx, workspace string) (*metadataLogicalRootSnapshot, func(), error) {
	if cache, ok := ctx.Value(metadataLogicalRootSnapshotKey{}).(*metadataLogicalRootSnapshots); ok {
		if cache.closed || cache.store != s || cache.tx != tx || cache.workspace != workspace {
			return nil, nil, ErrMetadataRecordQuery
		}
		if cache.read == nil {
			var err error
			cache.read, err = s.detachMetadataLogicalRoot(ctx, tx, workspace)
			if err != nil {
				return nil, nil, err
			}
		}
		return cache.read, func() {}, nil
	}
	read, err := s.detachMetadataLogicalRoot(ctx, tx, workspace)
	if err != nil {
		return nil, nil, err
	}
	return read, func() { _ = read.read.Close() }, nil
}

func invalidateMetadataLogicalRootSnapshot(ctx context.Context, tx *sqlTx) {
	if cache, ok := ctx.Value(metadataLogicalRootSnapshotKey{}).(*metadataLogicalRootSnapshots); ok && cache.tx == tx && cache.read != nil {
		_ = cache.read.read.Close()
		cache.read = nil
	}
}

type metadataLogicalRootReaderAt struct {
	ctx       context.Context
	tx        *sqlTx
	workspace string
	size      int64
}

func (r *metadataLogicalRootReaderAt) ReadAt(p []byte, offset int64) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if offset < 0 {
		return 0, ErrMetadataRecordQuery
	}
	if len(p) == 0 {
		return 0, nil
	}
	if offset >= r.size {
		return 0, io.EOF
	}
	body := "data"
	if r.tx.dialect == "sqlite" {
		body = "CAST(data AS BLOB)"
	}
	n := 0
	for n < len(p) && offset+int64(n) < r.size {
		size := min(int64(metadataLogicalRootChunkBytes), int64(len(p)-n), r.size-offset-int64(n))
		var chunk []byte
		if err := r.tx.QueryRowContext(r.ctx, "SELECT SUBSTR("+body+",?,?) FROM workspace_states WHERE workspace_key=?",
			offset+int64(n)+1, size, r.workspace).Scan(&chunk); err != nil {
			return n, err
		}
		if int64(len(chunk)) != size {
			return n, io.ErrUnexpectedEOF
		}
		n += copy(p[n:], chunk)
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (s *SQLiteStore) detachMetadataLogicalRoot(ctx context.Context, tx *sqlTx, workspace string) (_ *metadataLogicalRootSnapshot, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if tx == nil || tx.database != s.db || tx.dialect != s.dialect || !s.BoundedMetadataEnabled() {
		return nil, ErrMetadataRecordQuery
	}
	lease, _ := ctx.Value(metadataWorkLeaseKey{}).(*metadataWorkLease)
	if lease == nil || lease.budget != s.metadataResidency.work {
		return nil, ErrMetadataWorkBudget
	}
	lease.budget.mu.Lock()
	admitted := lease.live && lease.bytes >= 4<<20
	lease.budget.mu.Unlock()
	if !admitted {
		return nil, ErrMetadataWorkBudget
	}
	ctx = s.metadataLogicalScratchContext(ctx, workspace)
	lock, body := "", "data"
	if tx.dialect != "sqlite" {
		lock = " FOR UPDATE"
	} else {
		body = "CAST(data AS BLOB)"
	}
	var identity string
	var size int64
	if err := tx.QueryRowContext(ctx, "SELECT workspace_id,LENGTH("+body+") FROM workspace_states WHERE workspace_key=?"+lock,
		workspace).Scan(&identity, &size); err != nil {
		return nil, err
	}
	if size < 0 || size > int64(s.maxStateBytes) {
		return nil, fmt.Errorf("workspace state exceeds %d bytes", s.maxStateBytes)
	}
	read, err := s.metadataRootCursorInSnapshot(ctx, tx, workspace)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = read.Close()
		}
	}()
	if read.rootSize != size || read.stamp.workspaceID != identity {
		return nil, ErrConcurrentWrite
	}
	root, err := newMetadataLogicalRecordSpool(ctx)
	if err != nil {
		return nil, err
	}
	if !root.CanAppend(size) {
		_ = root.Close()
		return nil, ErrRecordSpoolBudget
	}
	input := &metadataLogicalRootReaderAt{ctx: ctx, tx: tx, workspace: workspace, size: size}
	if _, err := io.CopyBuffer(root, io.NewSectionReader(input, 0, size), make([]byte, metadataLogicalRootChunkBytes)); err != nil {
		_ = root.Close()
		return nil, err
	}
	read.root, read.snapshot = root, nil
	if err := read.scanRoot(nil); err != nil {
		return nil, err
	}
	probe := &SQLiteStore{db: s.db, dialect: s.dialect, maxStateBytes: s.maxStateBytes, metadataResidency: newMetadataResidency()}
	probe.metadataResidency.requireCoverage = false
	if err := probe.validateMetadataLogicalRootRead(ctx, tx, workspace, read); err != nil {
		return nil, err
	}
	result := &metadataLogicalRootSnapshot{read: read, fields: map[string]metadataCollectionRange{}, shapes: map[string]string{}, stamp: read.stamp}
	scanner := read.scanner(metadataCollectionRange{size: size})
	if err := scanner.spaces(); err != nil {
		return nil, err
	}
	if err := scanner.object(func(key, value metadataCollectionRange) error {
		name, err := read.shortString(key)
		if err == nil {
			result.fields[name] = value
		}
		return err
	}, 0); err != nil {
		return nil, err
	}
	if err := scanner.end(); err != nil {
		return nil, err
	}
	manifest := read.scanner(result.fields[metadataCollectionsKey])
	if err := manifest.spaces(); err != nil {
		return nil, err
	}
	first, err := manifest.peek()
	if err != nil {
		return nil, err
	}
	if first != 'n' {
		if err := manifest.object(func(key, value metadataCollectionRange) error {
			name, err := read.shortString(key)
			if err != nil {
				return err
			}
			if value.size > 4096 {
				return ErrMetadataRecordQuery
			}
			raw := make([]byte, int(value.size))
			if _, err := read.root.ReadAt(raw, value.offset); err != nil {
				return err
			}
			var shape string
			if err := json.Unmarshal(raw, &shape); err != nil {
				return err
			}
			result.shapes[name] = shape
			return nil
		}, 0); err != nil {
			return nil, err
		}
	} else {
		result.shapes = nil
	}
	return result, nil
}

func (s *SQLiteStore) readMetadataLogicalScopedRoot(ctx context.Context, tx *sqlTx, workspace string, scope MetadataRecordScope) (domain.Bootstrap, error) {
	root, closeRoot, err := s.metadataLogicalRootSnapshot(ctx, tx, workspace)
	if err != nil {
		return domain.Bootstrap{}, err
	}
	defer closeRoot()
	fields := append(metadataRecordFields(scope), "workspace", "viewer", "viewerRole")
	selected := map[string]json.RawMessage{}
	used := 0
	for _, name := range fields {
		if _, copied := selected[name]; copied {
			continue
		}
		part, found := root.fields[name]
		if !found {
			continue
		}
		if part.size > int64(MetadataRecordPageMaxBytes-used-len(name)-4) {
			return domain.Bootstrap{}, ErrMetadataPageBudget
		}
		raw := make([]byte, int(part.size))
		if _, err := root.read.root.ReadAt(raw, part.offset); err != nil {
			return domain.Bootstrap{}, err
		}
		selected[name] = raw
		used += len(name) + len(raw) + 4
	}
	selected[metadataCollectionsKey], err = json.Marshal(root.shapes)
	if err != nil {
		return domain.Bootstrap{}, err
	}
	raw, err := json.Marshal(selected)
	if err != nil {
		return domain.Bootstrap{}, err
	}
	return s.decodeWorkspaceMetadata(WithMetadataRecords(ctx, scope), workspace, raw, tx)
}

func (s *SQLiteStore) validateMetadataLogicalRootSnapshot(ctx context.Context, tx *sqlTx, workspace string) error {
	_, closeRoot, err := s.metadataLogicalRootSnapshot(ctx, tx, workspace)
	if err != nil {
		return err
	}
	closeRoot()
	return nil
}

type metadataLogicalRootReplacement struct {
	raw   json.RawMessage
	spool *RecordSpool
	size  int64
}

type metadataLogicalRootComparison struct {
	source io.ReaderAt
	offset int64
	same   bool
	err    error
	buffer [32 << 10]byte
}

func (c *metadataLogicalRootComparison) write(raw []byte) {
	for len(raw) != 0 && c.same && c.err == nil {
		part := raw[:min(len(raw), len(c.buffer))]
		n, err := c.source.ReadAt(c.buffer[:len(part)], c.offset)
		if err != nil && !errors.Is(err, io.EOF) {
			c.err = err
			break
		}
		c.same = n == len(part) && bytes.Equal(part, c.buffer[:n])
		c.offset += int64(len(part))
		raw = raw[len(part):]
	}
	c.offset += int64(len(raw))
}

func (c *metadataLogicalRootComparison) unchanged(part metadataCollectionRange) {
	if !c.same || part.offset == c.offset {
		c.offset += part.size
		return
	}
	for offset := int64(0); offset < part.size && c.same && c.err == nil; {
		length := min(int64(len(c.buffer)), part.size-offset)
		raw := make([]byte, int(length))
		_, c.err = c.source.ReadAt(raw, part.offset+offset)
		if c.err == nil {
			c.write(raw)
		}
		offset += length
	}
}

func metadataLogicalRootBytes(raw json.RawMessage) metadataLogicalRootReplacement {
	return metadataLogicalRootReplacement{raw: raw, size: int64(len(raw))}
}

func (s *SQLiteStore) reserveMetadataLogicalRootWork(ctx context.Context, bytes int) (func(), error) {
	lease, _ := ctx.Value(metadataWorkLeaseKey{}).(*metadataWorkLease)
	if lease != nil && lease.budget == s.metadataResidency.work {
		lease.budget.mu.Lock()
		covered := lease.live && bytes <= lease.bytes-(8<<20)
		lease.budget.mu.Unlock()
		if covered {
			return func() {}, nil
		}
	}
	_, release, err := s.tryAdmitAdditionalMetadataWork(ctx, bytes)
	return release, err
}

// Root SQL copies unchanged byte ranges inside the database. Changed values
// own their driver buffers, and transaction accounting includes the full root.
func (s *SQLiteStore) patchMetadataLogicalRoot(ctx context.Context, tx *sqlTx, workspace, identity string, changes map[string]metadataLogicalRootReplacement, touch bool) error {
	root, closeRoot, err := s.metadataLogicalRootSnapshot(ctx, tx, workspace)
	if err != nil {
		return err
	}
	defer closeRoot()
	for name, value := range changes {
		if name != metadataCollectionsKey {
			if _, known := metadataResidencyFields()[name]; !known {
				return ErrMetadataRecordQuery
			}
		}
		if value.size < 0 || value.size > int64(s.maxStateBytes) {
			return ErrMetadataWorkBudget
		}
		if value.spool == nil && value.raw != nil && !json.Valid(value.raw) {
			return ErrMetadataRecordQuery
		}
	}
	stamp, err := readMetadataStamp(ctx, tx, workspace)
	if err != nil {
		return err
	}
	if stamp != root.stamp {
		return ErrConcurrentWrite
	}
	fields := make(map[string]metadataCollectionRange, len(root.fields)+len(changes))
	for name, part := range root.fields {
		fields[name] = part
	}
	for name, value := range changes {
		if value.raw == nil && value.spool == nil {
			delete(fields, name)
		} else if _, found := fields[name]; !found {
			fields[name] = metadataCollectionRange{}
		}
	}
	changes, closeNormalized, err := s.normalizeMetadataLogicalRootValues(ctx, workspace, root, fields, changes)
	if err != nil {
		return err
	}
	defer closeNormalized()
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	slices.Sort(names)
	input, size := int64(0), int64(2)
	for index, name := range names {
		key, _ := json.Marshal(name)
		size += int64(len(key) + 1)
		if index != 0 {
			size++
		}
		if replacement, changed := changes[name]; changed {
			input += replacement.size
			size += replacement.size
		} else {
			size += fields[name].size
		}
		if size > int64(s.maxStateBytes) {
			return fmt.Errorf("workspace state exceeds %d bytes", s.maxStateBytes)
		}
	}
	release, err := s.reserveMetadataLogicalRootWork(ctx, int(input)*4+128<<10)
	if err != nil {
		return err
	}
	defer release()
	body := "data"
	if tx.dialect == "sqlite" {
		body = "CAST(data AS BLOB)"
	}
	parts, args := []string{}, []any{}
	comparison := metadataLogicalRootComparison{source: root.read.root, same: true}
	literal := func(raw []byte) {
		comparison.write(raw)
		expression := "?"
		if tx.dialect == "postgres" {
			expression = "CAST(? AS BYTEA)"
		}
		parts, args = append(parts, expression), append(args, raw)
	}
	literal([]byte("{"))
	for index, name := range names {
		key, _ := json.Marshal(name)
		prefix := append(key, ':')
		if index > 0 {
			prefix = append([]byte{','}, prefix...)
		}
		literal(prefix)
		if replacement, changed := changes[name]; changed {
			if replacement.spool == nil {
				literal(replacement.raw)
			} else {
				for offset := int64(0); offset < replacement.size; {
					length := min(int64(metadataLogicalRootChunkBytes), replacement.size-offset)
					raw := make([]byte, int(length))
					if _, err := replacement.spool.ReadAt(raw, offset); err != nil {
						return err
					}
					literal(raw)
					offset += length
				}
			}
		} else {
			part := fields[name]
			comparison.unchanged(part)
			parts = append(parts, "SUBSTR("+body+",?,?)")
			args = append(args, part.offset+1, part.size)
		}
	}
	literal([]byte("}"))
	if comparison.err != nil {
		return comparison.err
	}
	if comparison.same && comparison.offset == root.read.rootSize && identity == root.stamp.workspaceID {
		return nil
	}
	expression := strings.Join(parts, " || ")
	if tx.dialect == "mysql" {
		expression = "CONCAT(" + strings.Join(parts, ",") + ")"
	} else if tx.dialect == "sqlite" {
		expression = "CAST((" + expression + ") AS BLOB)"
	}
	query := "UPDATE workspace_states SET workspace_id=?,data=" + expression
	args = append([]any{identity}, args...)
	now := ""
	if touch {
		now = time.Now().UTC().Format(time.RFC3339Nano)
		query += ",updated_at=?"
		args = append(args, now)
	}
	query += " WHERE workspace_key=?"
	args = append(args, workspace)
	physical := len(query)
	for _, arg := range args {
		switch value := arg.(type) {
		case []byte:
			physical += len(value)
		case string:
			physical += len(value)
		default:
			physical += 16
		}
	}
	logical := int(size) + len(workspace) + len("UPDATE workspace_states SET data=? WHERE workspace_key=?")
	if touch {
		logical = int(size) + len(identity) + len(now) + len(workspace) +
			len("UPDATE workspace_states SET workspace_id=?,data=?,updated_at=? WHERE workspace_key=?")
	}
	if tx.maxTransactionBytes > 0 && logical > tx.maxTransactionBytes-tx.writtenBytes {
		tx.writeErr = fmt.Errorf("%w: maximum %d bytes; split the import into smaller batches", ErrTransactionWriteBudget, tx.maxTransactionBytes)
		return tx.writeErr
	}
	// Charge the original one-root UPDATE, including its exact SQL overhead.
	// Keep ExecContext's first-write, journal and revision hooks unchanged.
	prior, limit := tx.writtenBytes, tx.maxTransactionBytes
	if physical > logical && limit > 0 {
		tx.maxTransactionBytes += physical - logical
	}
	tx.metadataWritten = true
	_, err = tx.ExecContext(ctx, query, args...)
	tx.maxTransactionBytes = limit
	if tx.writtenBytes == prior+physical {
		tx.writtenBytes = prior + logical
	}
	if err != nil {
		return err
	}
	invalidateMetadataLogicalRootSnapshot(ctx, tx)
	return nil
}

func (s *SQLiteStore) writeMetadataLogicalDelta(ctx context.Context, tx *sqlTx, workspace string, previous domain.Bootstrap, target *domain.Bootstrap) (bool, error) {
	before, after := compactImportInputs(collectionMetadata(previous)), compactImportInputs(collectionMetadata(*target))
	changed := changedMetadataFields(before, after)
	if len(changed) != 0 {
		replaced, touched, err := s.rebaseMetadata(ctx, tx, workspace, &before, &after, changed, nil)
		if err != nil {
			return false, err
		}
		if touched {
			for _, field := range changed {
				if (field.name == "importJobs" || field.name == "migrationJobs") && !slices.Contains(replaced, field) {
					replaced = append(replaced, field)
				}
			}
		}
		source, destination := reflect.ValueOf(&after).Elem(), reflect.ValueOf(target).Elem()
		for _, field := range replaced {
			setRebasedField(field, destination.Field(field.index), source.Field(field.index))
		}
		if touched {
			changed = changedMetadataFields(before, after)
		}
	}
	root, closeRoot, err := s.metadataLogicalRootSnapshot(ctx, tx, workspace)
	if err != nil {
		return false, err
	}
	defer closeRoot()
	if len(changed) == 0 && root.stamp.workspaceID == after.Workspace.ID {
		return false, nil
	}
	var shapes map[string]string
	if root.shapes != nil {
		shapes = make(map[string]string, len(root.shapes))
		for name, shape := range root.shapes {
			shapes[name] = shape
		}
	}
	refs := newIssueReferences(domain.Bootstrap{Users: after.Users})
	slow := changed[:0:0]
	for _, field := range changed {
		if shapes[field.name] == "updates" {
			done, err := writeUpdatesFieldDelta(ctx, tx, workspace, refs, field, reflect.ValueOf(&before).Elem().Field(field.index), reflect.ValueOf(&after).Elem().Field(field.index))
			if err != nil {
				return false, err
			}
			if done {
				continue
			}
		}
		if shapes[field.name] == "array" {
			done, err := writeArrayFieldDelta(ctx, tx, workspace, refs, field, reflect.ValueOf(&before).Elem().Field(field.index), reflect.ValueOf(&after).Elem().Field(field.index))
			if err != nil {
				return false, err
			}
			if done {
				continue
			}
		}
		slow = append(slow, field)
	}
	partial, prior := reflect.New(reflect.TypeFor[domain.Bootstrap]()).Elem(), reflect.New(reflect.TypeFor[domain.Bootstrap]()).Elem()
	source, original := reflect.ValueOf(&after).Elem(), reflect.ValueOf(&before).Elem()
	names := make([]string, 0, len(slow))
	for _, field := range slow {
		partial.Field(field.index).Set(source.Field(field.index))
		prior.Field(field.index).Set(original.Field(field.index))
		names = append(names, field.name)
	}
	encoded, err := json.Marshal(partial.Interface())
	if err != nil {
		return false, err
	}
	partRoot, partShapes, partRecords, err := splitWorkspaceMetadata(encoded)
	if err != nil {
		return false, err
	}
	encodedBefore, err := json.Marshal(prior.Interface())
	if err != nil {
		return false, err
	}
	_, _, knownRecords, err := splitWorkspaceMetadata(encodedBefore)
	if err != nil {
		return false, err
	}
	selected := map[string]bool{}
	for _, name := range names {
		selected[name] = true
	}
	wanted := map[metadataRecordKey]metadataRecord{}
	for key, value := range partRecords {
		if selected[key.field] {
			wanted[key] = value
		}
	}
	newShapes := map[string]string{}
	for name, shape := range partShapes {
		if selected[name] {
			newShapes[name] = shape
		}
	}
	if err := syncMetadataRecords(ctx, tx, workspace, refs, newShapes, wanted, names, knownRecords); err != nil {
		return false, err
	}
	changes := map[string]metadataLogicalRootReplacement{}
	for _, name := range names {
		changes[name] = metadataLogicalRootBytes(partRoot[name])
		if shape, found := newShapes[name]; found {
			shapes[name] = shape
		} else {
			delete(shapes, name)
		}
	}
	manifest, err := json.Marshal(shapes)
	if err != nil {
		return false, err
	}
	changes[metadataCollectionsKey] = metadataLogicalRootBytes(manifest)
	if err := s.patchMetadataLogicalRoot(ctx, tx, workspace, after.Workspace.ID, changes, true); err != nil {
		return false, err
	}
	return true, nil
}

func (s *SQLiteStore) ensureMetadataLogicalShape(ctx context.Context, tx *sqlTx, workspace, field, shape string) error {
	root, closeRoot, err := s.metadataLogicalRootSnapshot(ctx, tx, workspace)
	if err != nil {
		return err
	}
	defer closeRoot()
	if root.shapes[field] == shape {
		return nil
	}
	shapes := make(map[string]string, len(root.shapes)+1)
	for name, current := range root.shapes {
		shapes[name] = current
	}
	shapes[field] = shape
	manifest, err := json.Marshal(shapes)
	if err != nil {
		return err
	}
	empty := json.RawMessage(`{}`)
	if shape == "array" {
		empty = json.RawMessage(`[]`)
	}
	return s.patchMetadataLogicalRoot(ctx, tx, workspace, root.stamp.workspaceID, map[string]metadataLogicalRootReplacement{
		field:                  metadataLogicalRootBytes(empty),
		metadataCollectionsKey: metadataLogicalRootBytes(manifest),
	}, false)
}
