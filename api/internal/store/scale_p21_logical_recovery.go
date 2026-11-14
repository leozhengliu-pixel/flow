package store

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"

	"flow/api/internal/domain"
)

const metadataLogicalRecoveryRecordBytes = 28 << 20

type metadataLogicalRecovery struct {
	store             *SQLiteStore
	ctx               context.Context
	tx                *sqlTx
	workspace         string
	read              *MetadataCollectionRead
	stage             *sw26MigrationStage
	builder           *metadataLogicalBuilder
	typed             *metadataLogicalBuilder
	manifest          map[string]string
	hasRows           bool
	workBytes         int
	work              []func()
	currentValueBytes int64
	currentValueWork  int
}

// readRawMetadataLogicalSource preserves decodeWorkspaceMetadata's stored
// values before normalization. Its caller owns the repeatable-read transaction
// and a live parser lease; the returned immutable source owns no SQL snapshot.
func (s *SQLiteStore) readRawMetadataLogicalSource(ctx context.Context, tx *sqlTx, workspace string) (_ *metadataLogicalSource, err error) {
	return s.readRawMetadataLogicalSourceRoot(ctx, tx, workspace, false)
}

func (s *SQLiteStore) readRawMetadataLogicalSourceValidated(ctx context.Context, tx *sqlTx, workspace string) (_ *metadataLogicalSource, err error) {
	return s.readRawMetadataLogicalSourceRoot(ctx, tx, workspace, true)
}

func (s *SQLiteStore) readRawMetadataLogicalSourceRoot(ctx context.Context, tx *sqlTx, workspace string, validateRoot bool) (_ *metadataLogicalSource, err error) {
	ctx = s.metadataLogicalScratchContext(ctx, workspace)
	read, err := s.metadataRootCursorInSnapshot(ctx, tx, workspace)
	if err != nil {
		return nil, err
	}
	defer read.Close()
	if read.rootSize > int64(s.maxStateBytes) {
		return nil, fmt.Errorf("workspace state exceeds %d bytes", s.maxStateBytes)
	}
	stamp, err := readMetadataStamp(ctx, tx, workspace)
	if err != nil {
		return nil, err
	}
	root, err := newMetadataLogicalRecordSpool(ctx)
	if err != nil {
		return nil, err
	}
	if !root.CanAppend(read.rootSize) {
		root.Close()
		return nil, ErrRecordSpoolBudget
	}
	if _, err := io.CopyBuffer(root, io.NewSectionReader(read.rootSource(), 0, read.rootSize), make([]byte, metadataRecordChunkBytes)); err != nil {
		root.Close()
		return nil, err
	}
	read.root, read.snapshot = root, nil
	if err := read.scanRoot(nil); err != nil {
		return nil, err
	}
	if validateRoot {
		if err := s.validateMetadataLogicalRootRead(ctx, tx, workspace, read); err != nil {
			return nil, err
		}
	}
	stage, err := sw26NewMigrationStage(ctx)
	if err != nil {
		return nil, err
	}
	defer stage.Close()
	for _, query := range []string{
		`CREATE TABLE canonical(seq INTEGER PRIMARY KEY,field TEXT NOT NULL,key BLOB NOT NULL,physical INTEGER NOT NULL,position INTEGER NOT NULL,size INTEGER NOT NULL,data BLOB)`,
		`CREATE INDEX canonical_field_order ON canonical(field,position,key,seq)`,
		`CREATE TABLE values_stage(key BLOB NOT NULL,ordinal INTEGER NOT NULL,offset INTEGER NOT NULL,size INTEGER NOT NULL,cost INTEGER NOT NULL,unavailable INTEGER NOT NULL,blocked INTEGER NOT NULL,PRIMARY KEY(key,ordinal))`,
	} {
		if _, err := stage.exec(ctx, 4096, query); err != nil {
			return nil, err
		}
	}
	builder, err := newMetadataLogicalBuilder(ctx)
	if err != nil {
		return nil, err
	}
	typed, err := newMetadataLogicalBuilder(ctx)
	if err != nil {
		builder.Close()
		return nil, err
	}
	defer typed.Close()
	success := false
	defer func() {
		if !success {
			builder.Close()
		}
	}()
	recovery := &metadataLogicalRecovery{store: s, ctx: ctx, tx: tx, workspace: workspace, read: read, stage: stage, builder: builder, typed: typed, manifest: map[string]string{}}
	defer recovery.releaseWork()
	if err := recovery.readManifest(); err != nil {
		return nil, err
	}
	if recovery.hasRows {
		if err := recovery.readCanonical(); err != nil {
			return nil, err
		}
		recovery.releaseWork()
	}
	bootstrap := reflect.TypeFor[domain.Bootstrap]()
	for index := 0; index < bootstrap.NumField(); index++ {
		field := bootstrap.Field(index)
		name := jsonFieldName(field)
		if name == "-" {
			continue
		}
		if _, err := stage.exec(ctx, 0, `DELETE FROM values_stage`); err != nil {
			return nil, err
		}
		switch {
		case field.Type.Kind() == reflect.Slice && field.Type.Elem().Kind() != reflect.Uint8:
			err = recovery.array(name, field.Type)
		case field.Type.Kind() == reflect.Map && field.Type.Key().Kind() == reflect.String:
			err = recovery.object(name, field.Type)
		default:
			err = recovery.scalar(name, field.Type)
		}
		if err != nil {
			return nil, fmt.Errorf("recover logical %s: %w", name, err)
		}
		recovery.releaseWork()
	}
	source, err := builder.Finish(stamp)
	if err != nil {
		return nil, err
	}
	success = true
	return source, nil
}

func (r *metadataLogicalRecovery) rootOccurrences(name string, visit func(metadataCollectionRange) error) error {
	scanner := r.read.scanner(metadataCollectionRange{size: r.read.rootSize})
	if err := scanner.spaces(); err != nil {
		return err
	}
	if first, _ := scanner.peek(); first == 'n' {
		return nil
	}
	return scanner.object(func(key, value metadataCollectionRange) error {
		actual, err := r.read.shortString(key)
		if err != nil {
			return err
		}
		if strings.EqualFold(actual, name) {
			return visit(value)
		}
		return nil
	}, 0)
}

func (r *metadataLogicalRecovery) readManifest() error {
	return r.rootOccurrences(metadataCollectionsKey, func(part metadataCollectionRange) error {
		input := r.read.scanner(part)
		first, err := input.peek()
		if err != nil {
			return err
		}
		if first == 'n' {
			clear(r.manifest)
			r.hasRows = false
			return nil
		}
		return input.object(func(key, value metadataCollectionRange) error {
			name, err := r.read.shortString(key)
			if err != nil {
				return err
			}
			var shape string
			if value.size > metadataLogicalRecoveryRecordBytes {
				return ErrMetadataWorkBudget
			}
			if err := json.NewDecoder(io.NewSectionReader(r.read.root, value.offset, value.size)).Decode(&shape); err != nil {
				return err
			}
			r.hasRows = true
			if _, _, recognized := metadataLogicalField(name); recognized {
				r.manifest[name] = shape
			}
			return nil
		}, 0)
	})
}

func (r *metadataLogicalRecovery) readCanonical() error {
	bodyLength, physical := "LENGTH(data)", "0"
	keyLength, fieldLength := "LENGTH(record_key)", "LENGTH(field)"
	if r.store.dialect == "sqlite" {
		bodyLength, physical = "LENGTH(CAST(data AS BLOB))", "CASE WHEN typeof(record_key)='blob' THEN 1 ELSE 0 END"
		keyLength, fieldLength = "LENGTH(CAST(record_key AS BLOB))", "LENGTH(CAST(field AS BLOB))"
	} else if r.store.dialect == "postgres" {
		bodyLength = "OCTET_LENGTH(data)"
		keyLength, fieldLength = "OCTET_LENGTH(record_key)", "OCTET_LENGTH(field)"
	}
	if _, _, err := r.recordWork(metadataLogicalRecoveryRecordBytes); err != nil {
		return err
	}
	headerSize := "(" + keyLength + "+" + fieldLength + ")"
	rows, err := r.tx.QueryContext(r.ctx, `SELECT CASE WHEN `+headerSize+`<=? THEN field ELSE NULL END,CASE WHEN `+headerSize+`<=? THEN record_key ELSE NULL END,collection_order,`+bodyLength+`,`+physical+` FROM workspace_metadata_records WHERE workspace_key=? ORDER BY field,record_key`,
		metadataLogicalRecoveryRecordBytes, metadataLogicalRecoveryRecordBytes, r.workspace)
	if err != nil {
		return err
	}
	sequence := int64(0)
	for rows.Next() {
		var field, key sql.NullString
		var position, size int64
		var physical int
		if err := rows.Scan(&field, &key, &position, &size, &physical); err != nil {
			rows.Close()
			return err
		}
		if !field.Valid || !key.Valid {
			rows.Close()
			return ErrMetadataWorkBudget
		}
		sequence++
		if _, err := r.stage.exec(r.ctx, int64(len(field.String)+len(key.String)+128), `INSERT INTO canonical(seq,field,key,physical,position,size) VALUES(?,?,?,?,?,?)`,
			sequence, field.String, []byte(key.String), physical, position, size); err != nil {
			rows.Close()
			return err
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	previousField, previousPosition, previousSequence := "", int64(0), int64(0)
	var previousKey []byte
	started := false
	for {
		var field string
		var key []byte
		var size, position, sequence int64
		var physical int
		query, args := `SELECT seq,field,key,physical,size,position FROM canonical ORDER BY field,position,key,seq LIMIT 1`, []any{}
		if started {
			query, args = `SELECT seq,field,key,physical,size,position FROM canonical WHERE (field,position,key,seq)>(?,?,CAST(? AS BLOB),?) ORDER BY field,position,key,seq LIMIT 1`,
				[]any{previousField, previousPosition, string(previousKey), previousSequence}
		}
		err := r.stage.db.QueryRowContext(r.ctx, query, args...).Scan(&sequence, &field, &key, &physical, &size, &position)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		ctx, release, err := r.recordWork(size)
		if err != nil {
			return err
		}
		var recordKey any = string(key)
		if physical == 1 || r.store.dialect == "mysql" {
			recordKey = key
		}
		data := "data"
		if r.store.dialect == "sqlite" {
			data = "CAST(data AS BLOB)"
		}
		raw := make([]byte, int(size))
		for offset := int64(0); offset < size; {
			length := min(int64(metadataRecordChunkBytes), size-offset)
			var chunk []byte
			err = r.tx.QueryRowContext(ctx, `SELECT SUBSTR(`+data+`,?,?) FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`,
				offset+1, length, r.workspace, field, recordKey).Scan(&chunk)
			if err != nil || int64(len(chunk)) != length {
				if err == nil {
					err = io.ErrUnexpectedEOF
				}
				break
			}
			copy(raw[int(offset):], chunk)
			offset += length
		}
		if err == nil {
			row := []metadataCacheRow{{Field: field, Key: string(key), Data: raw}}
			err = r.store.inlineOffloadedRows(ctx, r.workspace, r.tx, row)
			raw = row[0].Data
		}
		if err == nil && len(raw) > metadataLogicalRecoveryRecordBytes {
			err = ErrMetadataWorkBudget
		}
		if err == nil {
			_, err = r.stage.exec(ctx, int64(len(raw)), `UPDATE canonical SET data=? WHERE seq=?`, raw, sequence)
		}
		release()
		if err != nil {
			return err
		}
		previousField, previousPosition, previousKey, previousSequence, started = field, position, key, sequence, true
	}
}

func (r *metadataLogicalRecovery) recordWork(size int64) (context.Context, func(), error) {
	if size < 0 || size > metadataLogicalRecoveryRecordBytes {
		return r.ctx, nil, ErrMetadataWorkBudget
	}
	return r.holdWork(int(size)*8 + 128<<10)
}

func (r *metadataLogicalRecovery) holdWork(wanted int) (context.Context, func(), error) {
	if wanted < 0 || wanted > metadataWorkBytes {
		return r.ctx, nil, ErrMetadataWorkBudget
	}
	if lease, ok := r.ctx.Value(metadataWorkLeaseKey{}).(*metadataWorkLease); ok {
		lease.budget.mu.Lock()
		parentBytes := 0
		if lease.live && r.store.metadataResidency != nil && lease.budget == r.store.metadataResidency.work {
			parentBytes = lease.bytes
		}
		lease.budget.mu.Unlock()
		wanted -= parentBytes
		if wanted <= 0 {
			return r.ctx, func() {}, r.ctx.Err()
		}
	}
	wanted = ((wanted + (1 << 20) - 1) / (1 << 20)) * (1 << 20)
	if wanted > r.workBytes {
		_, release, err := r.store.tryAdmitAdditionalMetadataWork(r.ctx, wanted-r.workBytes)
		if err != nil {
			return r.ctx, nil, err
		}
		r.work = append(r.work, release)
		r.workBytes = wanted
	}
	return r.ctx, func() {}, r.ctx.Err()
}

func (r *metadataLogicalRecovery) releaseWork() {
	for _, release := range r.work {
		release()
	}
	r.work, r.workBytes, r.currentValueBytes, r.currentValueWork = nil, 0, 0, 0
}

func (r *metadataLogicalRecovery) decode(part metadataCollectionRange, destination reflect.Value, done func() error) error {
	return r.decodeAt(r.read.root, part, destination, done)
}

func (r *metadataLogicalRecovery) decodeAt(input io.ReaderAt, part metadataCollectionRange, destination reflect.Value, done func() error) error {
	ctx, release, err := r.recordWork(part.size + r.currentValueBytes)
	if err != nil {
		return err
	}
	defer release()
	raw := make([]byte, int(part.size))
	if _, err := input.ReadAt(raw, part.offset); err != nil {
		return err
	}
	return r.decodeBytes(ctx, raw, destination, done)
}

func (r *metadataLogicalRecovery) decodeBytes(ctx context.Context, raw []byte, destination reflect.Value, done func() error) error {
	budget := metadataHydrationBudget{bytes: metadataLogicalRecoveryRecordBytes, work: metadataWorkBytes - 128<<10}
	initialWork := budget.work
	if err := budget.consume(raw); err != nil {
		return err
	}
	required := initialWork - budget.work
	if _, _, err := r.holdWork(required + r.currentValueWork + 128<<10); err != nil {
		return err
	}
	if err := json.Unmarshal(raw, destination.Addr().Interface()); err != nil {
		return err
	}
	r.currentValueWork += required
	if err := ctx.Err(); err != nil {
		return err
	}
	return done()
}

func (r *metadataLogicalRecovery) put(key string, ordinal int64, value reflect.Value) error {
	if err := r.typed.auditJSONLocations(value); err != nil {
		return err
	}
	node, err := r.typed.writeValue(value, 0, false)
	if err != nil {
		return err
	}
	_, err = r.stage.exec(r.ctx, int64(len(key)+128), `INSERT INTO values_stage(key,ordinal,offset,size,cost,unavailable,blocked) VALUES(?,?,?,?,?,?,?) ON CONFLICT(key,ordinal) DO UPDATE SET offset=excluded.offset,size=excluded.size,cost=excluded.cost,unavailable=excluded.unavailable,blocked=excluded.blocked`,
		[]byte(key), ordinal, node.offset, node.size, int64(node.cost), node.unavailable, node.blocked)
	return err
}

func (r *metadataLogicalRecovery) get(key string, ordinal int64, target reflect.Value) (bool, error) {
	node := metadataLogicalNode{source: r.typed.storage, typ: target.Type()}
	var cost int64
	err := r.stage.db.QueryRowContext(r.ctx, `SELECT offset,size,cost,unavailable,blocked FROM values_stage WHERE key=? AND ordinal=?`, []byte(key), ordinal).Scan(&node.offset, &node.size, &cost, &node.unavailable, &node.blocked)
	if errors.Is(err, sql.ErrNoRows) {
		r.currentValueBytes = 0
		r.currentValueWork = 0
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if cost < 0 || cost > metadataWorkBytes/2 {
		return true, ErrMetadataWorkBudget
	}
	node.cost = uint64(cost)
	ctx, release, err := r.holdWork(int(cost)*2 + 128<<10)
	if err != nil {
		return true, err
	}
	defer release()
	r.currentValueBytes = 0
	r.currentValueWork = int(cost) * 2
	value, err := node.Value(ctx, int(cost))
	if err != nil {
		return true, err
	}
	target.Set(value)
	return true, nil
}

func (r *metadataLogicalRecovery) rows(name string, visit func(string, []byte) error) error {
	previousPosition, previousSequence := int64(-1<<63), int64(-1)
	previousKey := []byte{}
	for {
		var key []byte
		var position, sequence, size int64
		err := r.stage.db.QueryRowContext(r.ctx, `SELECT key,position,seq,LENGTH(data) FROM canonical WHERE field=? AND (position>? OR (position=? AND (key>CAST(? AS BLOB) OR (key=CAST(? AS BLOB) AND seq>?)))) ORDER BY position,key,seq LIMIT 1`,
			name, previousPosition, previousPosition, string(previousKey), string(previousKey), previousSequence).Scan(&key, &position, &sequence, &size)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		ctx, release, err := r.recordWork(size)
		if err != nil {
			return err
		}
		var raw []byte
		err = r.stage.db.QueryRowContext(ctx, `SELECT data FROM canonical WHERE seq=?`, sequence).Scan(&raw)
		if err == nil {
			r.currentValueWork = 0
			r.currentValueBytes = 0
			err = visit(string(key), raw)
		}
		release()
		if err != nil {
			return err
		}
		previousKey, previousPosition, previousSequence = key, position, sequence
	}
}

func (r *metadataLogicalRecovery) array(name string, typ reflect.Type) error {
	length, nilValue := int64(0), true
	err := r.rootOccurrences(name, func(part metadataCollectionRange) error {
		input := r.read.scanner(part)
		first, err := input.peek()
		if err != nil {
			return err
		}
		length = 0
		if first == 'n' {
			nilValue = true
			_, err := r.stage.exec(r.ctx, 0, `DELETE FROM values_stage`)
			return err
		}
		nilValue = false
		if err := input.array(func(item metadataCollectionRange) error {
			value := reflect.New(typ.Elem()).Elem()
			if _, err := r.get("", length, value); err != nil {
				return err
			}
			if err := r.decode(item, value, func() error { return r.put("", length, value) }); err != nil {
				return err
			}
			length++
			return nil
		}, 0); err != nil {
			return err
		}
		if length == 0 {
			_, err := r.stage.exec(r.ctx, 0, `DELETE FROM values_stage`)
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	if shape, manifested := r.manifest[name]; manifested {
		if err := r.rows(name, func(_ string, raw []byte) error {
			if shape != "array" {
				return fmt.Errorf("invalid array field %s shape %q", name, shape)
			}
			value := reflect.New(typ.Elem()).Elem()
			if err := r.decodeBytes(r.ctx, raw, value, func() error { return r.put("", length, value) }); err != nil {
				return err
			}
			length++
			nilValue = false
			return nil
		}); err != nil {
			return err
		}
	}
	if err := r.builder.BeginCollection(name, nilValue); err != nil {
		return err
	}
	for ordinal := int64(0); ordinal < length; ordinal++ {
		value := reflect.New(typ.Elem()).Elem()
		if _, err := r.get("", ordinal, value); err != nil {
			return err
		}
		if err := r.builder.auditJSONLocations(value); err != nil {
			return err
		}
		if err := r.builder.Append(reflect.Value{}, value); err != nil {
			return err
		}
	}
	return r.builder.EndCollection()
}

func (r *metadataLogicalRecovery) scalar(name string, typ reflect.Type) error {
	value := reflect.New(typ).Elem()
	encodedTotal := int64(0)
	mergedWork := 0
	if err := r.rootOccurrences(name, func(part metadataCollectionRange) error {
		encodedTotal += part.size
		if _, _, err := r.recordWork(encodedTotal); err != nil {
			return err
		}
		if err := r.decode(part, value, func() error { return nil }); err != nil {
			return err
		}
		mergedWork = r.currentValueWork
		return nil
	}); err != nil {
		return err
	}
	if typ == reflect.TypeFor[json.RawMessage]() {
		return r.rawObject(name, value)
	}
	if shape, manifested := r.manifest[name]; manifested {
		if err := r.rows(name, func(key string, raw []byte) error {
			if shape != "map" || typ.Kind() != reflect.Struct && !(typ.Kind() == reflect.Pointer && typ.Elem().Kind() == reflect.Struct) && typ != reflect.TypeFor[json.RawMessage]() {
				return fmt.Errorf("invalid map field %s shape %q", name, shape)
			}
			wrapped, err := json.Marshal(map[string]json.RawMessage{key: raw})
			if err != nil {
				return err
			}
			encodedTotal += int64(len(wrapped))
			if _, _, err := r.recordWork(encodedTotal); err != nil {
				return err
			}
			r.currentValueWork = mergedWork
			if err := r.decodeBytes(r.ctx, wrapped, value, func() error { return nil }); err != nil {
				return err
			}
			mergedWork = r.currentValueWork
			return nil
		}); err != nil {
			return err
		}
	}
	return r.field(name, value)
}

func (r *metadataLogicalRecovery) field(name string, value reflect.Value) error {
	if err := r.builder.auditJSONLocations(value); err != nil {
		return err
	}
	return r.builder.Field(name, value)
}

func (r *metadataLogicalRecovery) rawObject(name string, value reflect.Value) error {
	shape, manifested := r.manifest[name]
	if !manifested {
		return r.field(name, value)
	}
	initialized := false
	if err := r.rows(name, func(key string, raw []byte) error {
		if shape != "map" {
			return fmt.Errorf("invalid raw map field %s shape %q", name, shape)
		}
		if !initialized {
			initialized = true
			initial := value.Bytes()
			if len(initial) != 0 {
				source := bytes.NewReader(initial)
				scanner := metadataCollectionScanner{ctx: r.ctx, input: bufio.NewReaderSize(source, metadataRecordChunkBytes)}
				if err := scanner.spaces(); err != nil {
					return err
				}
				if first, _ := scanner.peek(); first != 'n' {
					if err := scanner.object(func(keyPart, part metadataCollectionRange) error {
						var key string
						r.currentValueWork, r.currentValueBytes = 0, 0
						if err := r.decodeAt(source, keyPart, reflect.ValueOf(&key).Elem(), func() error { return nil }); err != nil {
							return err
						}
						item := reflect.New(reflect.TypeFor[json.RawMessage]()).Elem()
						r.currentValueWork, r.currentValueBytes = 0, 0
						return r.decodeAt(source, part, item, func() error { return r.put(key, -1, item) })
					}, 0); err != nil {
						return err
					}
				}
			}
		}
		item := reflect.ValueOf(json.RawMessage(raw))
		return r.put(key, -1, item)
	}); err != nil {
		return err
	}
	if !initialized {
		return r.field(name, value)
	}
	spool, err := NewRecordSpool()
	if err != nil {
		return err
	}
	defer spool.Close()
	if _, err := io.WriteString(spool, "{"); err != nil {
		return err
	}
	first := true
	var previous []byte
	started := false
	for {
		var key []byte
		query, args := `SELECT key FROM values_stage WHERE ordinal=-1 ORDER BY key LIMIT 1`, []any{}
		if started {
			query, args = `SELECT key FROM values_stage WHERE ordinal=-1 AND key>CAST(? AS BLOB) ORDER BY key LIMIT 1`, []any{string(previous)}
		}
		if err := r.stage.db.QueryRowContext(r.ctx, query, args...).Scan(&key); errors.Is(err, sql.ErrNoRows) {
			break
		} else if err != nil {
			return err
		}
		encoded, err := json.Marshal(string(key))
		if err != nil {
			return err
		}
		if !first {
			if _, err := io.WriteString(spool, ","); err != nil {
				return err
			}
		}
		first = false
		if _, err := spool.Write(encoded); err != nil {
			return err
		}
		if _, err := io.WriteString(spool, ":"); err != nil {
			return err
		}
		var raw json.RawMessage
		if _, err := r.get(string(key), -1, reflect.ValueOf(&raw).Elem()); err != nil {
			return err
		}
		encodedValue, err := json.Marshal(raw)
		if err != nil {
			return err
		}
		if _, err := spool.Write(encodedValue); err != nil {
			return err
		}
		previous = key
		started = true
	}
	if _, err := io.WriteString(spool, "}"); err != nil {
		return err
	}
	info, err := spool.Stat()
	if err != nil {
		return err
	}
	ctx, release, err := r.recordWork(info.Size())
	if err != nil {
		return err
	}
	defer release()
	raw := make([]byte, int(info.Size()))
	if _, err := spool.ReadAt(raw, 0); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	value.SetBytes(raw)
	return r.field(name, value)
}

func (r *metadataLogicalRecovery) object(name string, typ reflect.Type) error {
	nilValue := true
	err := r.rootOccurrences(name, func(part metadataCollectionRange) error {
		input := r.read.scanner(part)
		first, err := input.peek()
		if err != nil {
			return err
		}
		if first == 'n' {
			nilValue = true
			_, err := r.stage.exec(r.ctx, 0, `DELETE FROM values_stage`)
			return err
		}
		nilValue = false
		return input.object(func(keyPart, valuePart metadataCollectionRange) error {
			var key string
			if err := r.decode(keyPart, reflect.ValueOf(&key).Elem(), func() error { return nil }); err != nil {
				return err
			}
			return r.mapValue(typ.Elem(), key, valuePart)
		}, 0)
	})
	if err != nil {
		return err
	}
	if shape, manifested := r.manifest[name]; manifested {
		if err := r.rows(name, func(key string, raw []byte) error {
			if shape != "map" && (shape != "updates" || typ.Elem().Kind() != reflect.Slice) {
				return fmt.Errorf("invalid map field %s shape %q", name, shape)
			}
			nilValue = false
			if shape == "updates" {
				var update metadataUpdate
				if err := json.Unmarshal(raw, &update); err != nil {
					return err
				}
				return r.updateValue(typ.Elem(), update)
			}
			if typ.Elem().Kind() == reflect.Slice && typ.Elem().Elem().Kind() != reflect.Uint8 {
				return r.mapSlice(typ.Elem(), key, bytes.NewReader(raw), metadataCollectionRange{size: int64(len(raw))})
			}
			value := reflect.New(typ.Elem()).Elem()
			return r.decodeBytes(r.ctx, raw, value, func() error { return r.put(key, -1, value) })
		}); err != nil {
			return err
		}
	}
	if err := r.builder.BeginCollection(name, nilValue); err != nil {
		return err
	}
	var previous []byte
	started := false
	for {
		var key []byte
		query, args := `SELECT key FROM values_stage WHERE ordinal=-1 ORDER BY key LIMIT 1`, []any{}
		if started {
			query, args = `SELECT key FROM values_stage WHERE ordinal=-1 AND key>CAST(? AS BLOB) ORDER BY key LIMIT 1`, []any{string(previous)}
		}
		if err := r.stage.db.QueryRowContext(r.ctx, query, args...).Scan(&key); errors.Is(err, sql.ErrNoRows) {
			break
		} else if err != nil {
			return err
		}
		value := reflect.New(typ.Elem()).Elem()
		if typ.Elem().Kind() == reflect.Slice && typ.Elem().Elem().Kind() != reflect.Uint8 {
			if err := r.appendMapSlice(string(key), typ.Elem(), reflect.ValueOf(string(key)).Convert(typ.Key())); err != nil {
				return err
			}
			previous = key
			started = true
			continue
		}
		if _, err := r.get(string(key), -1, value); err != nil {
			return err
		}
		if err := r.builder.auditJSONLocations(value); err != nil {
			return err
		}
		if err := r.builder.Append(reflect.ValueOf(string(key)).Convert(typ.Key()), value); err != nil {
			return err
		}
		previous = key
		started = true
	}
	return r.builder.EndCollection()
}

func (r *metadataLogicalRecovery) mapValue(typ reflect.Type, key string, part metadataCollectionRange) error {
	if typ.Kind() == reflect.Slice && typ.Elem().Kind() != reflect.Uint8 {
		return r.mapSlice(typ, key, r.read.root, part)
	}
	r.currentValueBytes = 0
	r.currentValueWork = 0
	value := reflect.New(typ).Elem()
	return r.decode(part, value, func() error { return r.put(key, -1, value) })
}

func (r *metadataLogicalRecovery) updateValue(typ reflect.Type, update metadataUpdate) error {
	header, exists, err := r.sliceHeader(update.Parent)
	if err != nil {
		return err
	}
	if len(update.Item) > 0 {
		item := reflect.New(typ.Elem()).Elem()
		if err := json.Unmarshal(update.Item, item.Addr().Interface()); err != nil {
			return err
		}
		if err := r.put(update.Parent, header.Length, item); err != nil {
			return err
		}
		header.Length++
		header.Nil = false
		return r.putSliceHeader(update.Parent, header)
	}
	if !exists {
		return r.putSliceHeader(update.Parent, metadataLogicalRecoverySlice{Nil: update.Null})
	}
	return nil
}

type metadataLogicalRecoverySlice struct {
	Length int64
	Nil    bool
}

func (r *metadataLogicalRecovery) sliceHeader(key string) (metadataLogicalRecoverySlice, bool, error) {
	var header metadataLogicalRecoverySlice
	value := reflect.ValueOf(&header).Elem()
	exists, err := r.get(key, -1, value)
	return header, exists, err
}

func (r *metadataLogicalRecovery) putSliceHeader(key string, header metadataLogicalRecoverySlice) error {
	return r.put(key, -1, reflect.ValueOf(header))
}

func (r *metadataLogicalRecovery) mapSlice(typ reflect.Type, key string, source io.ReaderAt, part metadataCollectionRange) error {
	if _, err := r.stage.exec(r.ctx, 0, `DELETE FROM values_stage WHERE key=?`, []byte(key)); err != nil {
		return err
	}
	scanner := &metadataCollectionScanner{ctx: r.ctx, input: bufio.NewReaderSize(io.NewSectionReader(source, part.offset, part.size), metadataRecordChunkBytes), offset: part.offset}
	if err := scanner.spaces(); err != nil {
		return err
	}
	first, err := scanner.peek()
	if err != nil {
		return err
	}
	header := metadataLogicalRecoverySlice{Nil: first == 'n'}
	if !header.Nil {
		if err := scanner.array(func(item metadataCollectionRange) error {
			r.currentValueBytes = 0
			r.currentValueWork = 0
			value := reflect.New(typ.Elem()).Elem()
			if err := r.decodeAt(source, item, value, func() error { return r.put(key, header.Length, value) }); err != nil {
				return err
			}
			header.Length++
			return nil
		}, 0); err != nil {
			return err
		}
	}
	return r.putSliceHeader(key, header)
}

// Build a map's slice value from individually staged elements without retaining
// its potentially unbounded history as one decoded Go slice.
func (r *metadataLogicalRecovery) appendMapSlice(key string, typ reflect.Type, mapKey reflect.Value) error {
	header, exists, err := r.sliceHeader(key)
	if err != nil || !exists {
		return errors.Join(err, ErrMetadataRecordQuery)
	}
	b := r.builder
	keyNode, err := b.writeValue(mapKey, 0, true)
	if err != nil {
		return err
	}
	node := metadataLogicalNode{source: b.storage, offset: b.offset, typ: typ, cost: uint64(typ.Size())*2 + 128}
	if err := b.write(make([]byte, 16)); err != nil {
		return err
	}
	present := byte(1)
	if header.Nil {
		present = 0
	}
	if err := b.write([]byte{present}); err != nil {
		return err
	}
	if !header.Nil {
		if err := b.writeUint(uint64(header.Length)); err != nil {
			return err
		}
		for index := int64(0); index < header.Length; index++ {
			item := reflect.New(typ.Elem()).Elem()
			if _, err := r.get(key, index, item); err != nil {
				return err
			}
			if err := b.auditJSONLocations(item); err != nil {
				return err
			}
			child, err := b.writeValue(item, 0, false)
			if err != nil {
				return err
			}
			node.cost += child.cost + uint64(typ.Elem().Size())*2 + 64
			node.unavailable = node.unavailable || child.unavailable
		}
	}
	node.size = b.offset - node.offset
	if err := b.patch(node); err != nil {
		return err
	}
	b.cost += keyNode.cost + node.cost + uint64(typ.Size())*2 + 128
	b.unavailable = b.unavailable || keyNode.unavailable || node.unavailable
	b.count++
	return nil
}
