package store

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"flow/api/internal/domain"
)

const logicalJSONStreamWork = 2 << 20

// Keys are admitted separately before decoding, binding and cursor retention.
const logicalJSONMapKeyBytes = metadataWorkBytes

// StreamFieldJSON writes one installed field as json.Marshal would, without
// materializing its record bodies. The caller owns publication/error handling:
// a later encoding or destination error may follow an already-written prefix.
// The installed generation and independent scratch remain owned through Flush.
func (r *InstalledMetadataRead) StreamFieldJSON(ctx context.Context, field string, dst io.Writer) (err error) {
	if r == nil || dst == nil {
		return ErrBoundedMetadataCoverage
	}
	source, err := r.retainSource(ctx, r.workspace)
	if err != nil {
		return err
	}
	defer source.Release()
	index, _, found := metadataLogicalField(field)
	if !found || reflect.TypeFor[domain.Bootstrap]().Field(index).Tag.Get("json") == "-" {
		return ErrMetadataRecordQuery
	}
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	ctx = metadataLogicalSourceScratchContext(ctx, source)
	owner := &ResourceRead{store: r.store}
	defer owner.Close()
	if err := owner.reserve(ctx, logicalJSONStreamWork); err != nil {
		return err
	}
	stream := &logicalJSONStream{
		ctx: ctx, owner: owner, view: &WebhookAggregateRead{store: r.store},
		out: bufio.NewWriterSize(dst, 64<<10),
	}
	defer func() {
		for _, release := range stream.view.releases {
			release()
		}
		if stream.stage != nil {
			err = errors.Join(err, stream.stage.Close())
		}
	}()
	if err := stream.write(webhookJSONValue{node: source.fields[index]}, 0); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := stream.out.Flush(); err != nil {
		return err
	}
	return ctx.Err()
}

type logicalJSONStream struct {
	ctx                 context.Context
	owner               *ResourceRead
	view                *WebhookAggregateRead
	out                 *bufio.Writer
	stage               *sw26MigrationStage
	mapID               int64
	buffer              [32 << 10]byte
	replace             func(metadataLogicalNode) (metadataLogicalNode, error)
	rewrite             MetadataJSONRewriter
	classifyCodecErrors bool
}

func (s *logicalJSONStream) text(value string) error {
	_, err := s.out.WriteString(value)
	return err
}

func (s *logicalJSONStream) present(node metadataLogicalNode) (bool, error) {
	if node.generic {
		absent, err := node.isNil(s.ctx)
		return !absent, err
	}
	cursor := metadataLogicalCursor{node, node.offset + 16, s.ctx}
	flag, err := cursor.byte()
	return flag != 0, err
}

func (s *logicalJSONStream) write(value webhookJSONValue, depth int) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if depth >= metadataLogicalDepth {
		return ErrMetadataWorkBudget
	}
	var err error
	if s.replace != nil {
		value.node, err = s.replace(value.node)
		if err != nil {
			return err
		}
	}
	value, err = s.view.unwrap(s.ctx, value)
	if err != nil {
		return err
	}
	node := value.node
	if node.typ == nil {
		return s.text("null")
	}
	if node.blocked && node.typ != reflect.TypeFor[time.Time]() {
		return ErrBoundedMetadataCoverage
	}
	typ := node.typ
	if typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Map || typ.Kind() == reflect.Slice {
		present, err := s.present(node)
		if err != nil {
			return err
		}
		if !present {
			// Unsupported map key types fail even when the map itself is nil.
			if typ.Kind() == reflect.Map && !logicalJSONMapKeyType(typ.Key()) {
				return &json.UnsupportedTypeError{Type: typ}
			}
			return s.text("null")
		}
	}
	if value.quoted {
		return s.quoted(value, depth)
	}
	if s.rewrite != nil {
		if handled, err := s.rewriteNode(node); handled || err != nil {
			return err
		}
	}
	if node.generic {
		return s.genericWrite(node)
	}
	switch typ {
	case reflect.TypeFor[json.RawMessage]():
		return s.raw(node)
	case reflect.TypeFor[json.Number]():
		return s.number(node)
	case reflect.TypeFor[time.Time](), reflect.TypeFor[*time.Time](),
		reflect.TypeFor[domain.OptionalInt](), reflect.TypeFor[*domain.OptionalInt]():
		return s.scalar(node)
	case reflect.TypeFor[domain.AgentToolCall]():
		return s.object(node, depth, true)
	case reflect.TypeFor[*domain.AgentToolCall]():
		var child metadataLogicalNode
		err := node.Children(s.ctx, func(_ reflect.Value, value metadataLogicalNode) error {
			child = value
			return nil
		})
		if err != nil {
			return err
		}
		return s.object(child, depth+1, true)
	case reflect.TypeFor[domain.ActivityMetadata]():
		return s.mapping(node, depth, true)
	case reflect.TypeFor[*json.RawMessage](), reflect.TypeFor[*json.Number](), reflect.TypeFor[*domain.ActivityMetadata]():
		var child metadataLogicalNode
		err := node.Children(s.ctx, func(_ reflect.Value, value metadataLogicalNode) error {
			child = value
			return nil
		})
		if err != nil {
			return err
		}
		return s.write(webhookJSONValue{node: child}, depth+1)
	}
	if webhookJSONCodec(typ) || metadataLogicalWireCustomMarshaler(typ) {
		return fmt.Errorf("%w: logical JSON codec %s", ErrBoundedMetadataCoverage, typ)
	}
	switch typ.Kind() {
	case reflect.String:
		return node.StreamBytes(s.ctx, s.string)
	case reflect.Slice:
		if typ.Elem().Kind() == reflect.Uint8 {
			return node.StreamBytes(s.ctx, func(input io.Reader) error {
				if err := s.text(`"`); err != nil {
					return err
				}
				encoder := base64.NewEncoder(base64.StdEncoding, s.out)
				if _, err := io.CopyBuffer(encoder, input, s.buffer[:]); err != nil {
					return err
				}
				if err := encoder.Close(); err != nil {
					return err
				}
				return s.text(`"`)
			})
		}
		fallthrough
	case reflect.Array:
		if err := s.text("["); err != nil {
			return err
		}
		count := 0
		err := node.Children(s.ctx, func(_ reflect.Value, child metadataLogicalNode) error {
			if count > 0 {
				if err := s.text(","); err != nil {
					return err
				}
			}
			count++
			return s.write(webhookJSONValue{node: child}, depth+1)
		})
		if err != nil {
			return err
		}
		return s.text("]")
	case reflect.Struct:
		return s.object(node, depth, false)
	case reflect.Map:
		return s.mapping(node, depth, false)
	default:
		return s.scalar(node)
	}
}

func (s *logicalJSONStream) scalar(node metadataLogicalNode) error {
	var raw []byte
	var marshalErr error
	err := s.view.native(s.ctx, node, func(value reflect.Value) {
		raw, marshalErr = json.Marshal(value.Interface())
	})
	if err != nil {
		return err
	}
	if marshalErr != nil {
		return marshalErr
	}
	_, err = s.out.Write(raw)
	return err
}

func (s *logicalJSONStream) number(node metadataLogicalNode) error {
	empty, err := node.StringEqual(s.ctx, "")
	if err != nil {
		return err
	}
	if empty {
		return s.text("0")
	}
	err = node.StreamBytes(s.ctx, func(input io.Reader) error {
		if s.classifyCodecErrors {
			return metadataAggregateCodecValidation(input, func(input io.Reader) error {
				scanner := metadataCollectionScanner{ctx: s.ctx, input: bufio.NewReaderSize(input, metadataRecordChunkBytes)}
				if err := scanner.number(); err != nil {
					return err
				}
				if _, err := scanner.peek(); !errors.Is(err, io.EOF) {
					if err != nil {
						return err
					}
					return errMetadataCollectionJSON
				}
				return nil
			})
		}
		scanner := metadataCollectionScanner{ctx: s.ctx, input: bufio.NewReaderSize(input, metadataRecordChunkBytes)}
		if err := scanner.number(); err != nil {
			return err
		}
		if _, err := scanner.peek(); !errors.Is(err, io.EOF) {
			if err != nil {
				return err
			}
			return errMetadataCollectionJSON
		}
		return nil
	})
	if err != nil {
		return err
	}
	return node.StreamBytes(s.ctx, func(input io.Reader) error {
		_, err := io.CopyBuffer(s.out, input, s.buffer[:])
		return err
	})
}

func (s *logicalJSONStream) quoted(value webhookJSONValue, depth int) (err error) {
	spool, err := newMetadataLogicalRecordSpool(s.ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, spool.Close()) }()
	outer := s.out
	s.out = bufio.NewWriterSize(spool, 4096)
	value.quoted = false
	err = s.write(value, depth+1)
	if err == nil {
		err = s.out.Flush()
	}
	s.out = outer
	if err != nil {
		return err
	}
	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return s.string(offloadedContextReader{s.ctx, spool})
}

func (s *logicalJSONStream) object(node metadataLogicalNode, depth int, tool bool) error {
	plan, err := s.view.plan(s.ctx, node.typ)
	if err != nil {
		return err
	}
	if err := s.text("{"); err != nil {
		return err
	}
	first := true
	for _, field := range plan {
		child, err := s.view.field(s.ctx, webhookJSONValue{node: node}, field.index)
		if err != nil {
			return err
		}
		if child.typ == nil {
			continue
		}
		if s.replace != nil {
			child, err = s.replace(child)
			if err != nil {
				return err
			}
		}
		omit, err := s.view.omitted(s.ctx, child, field)
		if err != nil {
			return err
		}
		argument := tool && field.name == "arguments"
		if omit && !argument {
			continue
		}
		if !first {
			if err := s.text(","); err != nil {
				return err
			}
		}
		first = false
		if err := s.stringString(field.name); err != nil {
			return err
		}
		if err := s.text(":"); err != nil {
			return err
		}
		if argument || tool && field.name == "result" {
			err := child.StreamBytes(s.ctx, func(input io.Reader) error {
				return validateMetadataCollectionJSON(s.ctx, input)
			})
			if err != nil {
				if s.ctx.Err() != nil {
					return s.ctx.Err()
				}
				if !errors.Is(err, errMetadataCollectionJSON) && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
					return err
				}
				if argument {
					if err := s.text("{}"); err != nil {
						return err
					}
				} else if err := child.StreamBytes(s.ctx, s.string); err != nil {
					return err
				}
				continue
			}
		}
		if err := s.write(webhookJSONValue{node: child, quoted: field.quoted}, depth+1); err != nil {
			return err
		}
	}
	return s.text("}")
}

func logicalJSONMapKeyType(typ reflect.Type) bool {
	return typ.Kind() == reflect.String ||
		typ.Kind() >= reflect.Int && typ.Kind() <= reflect.Uintptr ||
		typ.Implements(reflect.TypeFor[encoding.TextMarshaler]())
}

func (s *logicalJSONStream) mapKey(node metadataLogicalNode, visit func(string) error) error {
	if node.typ.Kind() == reflect.String && !node.generic {
		cursor := metadataLogicalCursor{node, node.offset + 16, s.ctx}
		length, err := cursor.uint()
		if err != nil {
			return err
		}
		if length > logicalJSONMapKeyBytes {
			return ErrMetadataWorkBudget
		}
	}
	if node.cost > logicalJSONMapKeyBytes*4 {
		return ErrMetadataWorkBudget
	}
	_, release, err := s.owner.store.tryAdmitAdditionalMetadataWork(s.ctx, int(node.cost)*8+64<<10)
	if err != nil {
		return err
	}
	defer release()
	value, err := node.WireValue(s.ctx, int(node.cost))
	if err != nil {
		return err
	}
	var name string
	switch {
	case value.Kind() == reflect.String:
		name = value.String()
	case value.Type().Implements(reflect.TypeFor[encoding.TextMarshaler]()):
		if value.Kind() != reflect.Pointer || !value.IsNil() {
			raw, err := value.Interface().(encoding.TextMarshaler).MarshalText()
			if err != nil {
				return err
			}
			name = string(raw)
		}
	case value.Kind() >= reflect.Int && value.Kind() <= reflect.Int64:
		name = strconv.FormatInt(value.Int(), 10)
	default:
		name = strconv.FormatUint(value.Uint(), 10)
	}
	if len(name) > logicalJSONMapKeyBytes {
		return ErrMetadataWorkBudget
	}
	return visit(name)
}

func (s *logicalJSONStream) mapping(node metadataLogicalNode, depth int, activity bool) error {
	if !logicalJSONMapKeyType(node.typ.Key()) {
		return &json.UnsupportedTypeError{Type: node.typ}
	}
	keyType := node.typ.Key()
	if keyType.Kind() != reflect.String && keyType.Implements(reflect.TypeFor[encoding.TextMarshaler]()) &&
		keyType != reflect.TypeFor[time.Time]() && keyType != reflect.TypeFor[*time.Time]() {
		return fmt.Errorf("%w: logical JSON map-key codec %s", ErrBoundedMetadataCoverage, keyType)
	}
	if s.stage == nil {
		stage, err := sw26NewMigrationStage(s.ctx)
		if err != nil {
			return err
		}
		s.stage = stage
		if _, err := stage.exec(s.ctx, 4096, `CREATE TABLE json_keys(map_id INTEGER,key BLOB,ordinal INTEGER,offset INTEGER,size INTEGER,cost INTEGER,unavailable INTEGER,blocked INTEGER,PRIMARY KEY(map_id,key,ordinal))`); err != nil {
			return err
		}
	}
	s.mapID++
	id := s.mapID
	ordinal := 0
	err := node.MapEntries(s.ctx, func(key, value metadataLogicalNode) error {
		err := s.mapKey(key, func(name string) error {
			if keyType.Kind() != reflect.String && keyType.Implements(reflect.TypeFor[encoding.TextMarshaler]()) {
				// No known repo producer creates these ties. Their literal
				// order depends on fresh Go map traversal, not captured order.
				var exists int
				err := s.stage.db.QueryRowContext(s.ctx,
					`SELECT 1 FROM json_keys WHERE map_id=? AND key=CAST(? AS BLOB) LIMIT 1`, id, []byte(name)).Scan(&exists)
				if err == nil {
					return fmt.Errorf("%w: duplicate encoded text map key", ErrBoundedMetadataCoverage)
				}
				if !errors.Is(err, sql.ErrNoRows) {
					return err
				}
			}
			cost := value.cost
			if value.generic {
				cost |= metadataLogicalGenericJSON
			}
			_, err := s.stage.exec(s.ctx, int64(len(name)+256),
				`INSERT INTO json_keys VALUES(?,?,?,?,?,?,?,?)`, id, []byte(name), ordinal,
				value.offset, value.size, cost, value.unavailable, value.blocked)
			return err
		})
		ordinal++
		return err
	})
	if err != nil {
		return err
	}
	if err := s.text("{"); err != nil {
		return err
	}
	var previous []byte
	previousOrdinal := -1
	var previousRelease func()
	defer func() {
		if previousRelease != nil {
			previousRelease()
		}
	}()
	first := true
	for {
		var size int
		var err error
		// The empty first key must be selected too; a nil cursor compares as
		// SQL NULL, so the initial query has no continuation predicate.
		if first {
			err = s.stage.db.QueryRowContext(s.ctx, `SELECT LENGTH(key) FROM json_keys WHERE map_id=? ORDER BY key,ordinal LIMIT 1`, id).Scan(&size)
		} else {
			err = s.stage.db.QueryRowContext(s.ctx,
				`SELECT LENGTH(key) FROM json_keys WHERE map_id=? AND (key>CAST(? AS BLOB) OR (key=CAST(? AS BLOB) AND ordinal>?)) ORDER BY key,ordinal LIMIT 1`,
				id, previous, previous, previousOrdinal).Scan(&size)
		}
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return err
		}
		if size < 0 || size > logicalJSONMapKeyBytes {
			return ErrMetadataWorkBudget
		}
		_, release, err := s.owner.store.tryAdmitAdditionalMetadataWork(s.ctx, size*8+64<<10)
		if err != nil {
			return err
		}
		var key []byte
		var currentOrdinal int
		value := metadataLogicalNode{source: node.source, typ: node.typ.Elem()}
		query := `SELECT key,ordinal,offset,size,cost,unavailable,blocked FROM json_keys WHERE map_id=? AND (key>CAST(? AS BLOB) OR (key=CAST(? AS BLOB) AND ordinal>?)) ORDER BY key,ordinal LIMIT 1`
		args := []any{id, previous, previous, previousOrdinal}
		if first {
			query, args = `SELECT key,ordinal,offset,size,cost,unavailable,blocked FROM json_keys WHERE map_id=? ORDER BY key,ordinal LIMIT 1`, []any{id}
		}
		err = s.stage.db.QueryRowContext(s.ctx, query, args...).Scan(&key, &currentOrdinal, &value.offset, &value.size, &value.cost, &value.unavailable, &value.blocked)
		if err != nil {
			release()
			return err
		}
		value.generic = value.cost&metadataLogicalGenericJSON != 0
		value.cost &= metadataLogicalCostMask
		if previousRelease != nil {
			previousRelease()
		}
		if key == nil {
			key = []byte{}
		}
		previous, previousOrdinal, previousRelease = key, currentOrdinal, release
		if !first {
			if err := s.text(","); err != nil {
				return err
			}
		}
		first = false
		if err := s.stringBytes(key); err != nil {
			return err
		}
		if err := s.text(":"); err != nil {
			return err
		}
		if activity && (string(key) == "addedLabelIds" || string(key) == "removedLabelIds") {
			if err := s.activityIDs(value); err != nil {
				return err
			}
		} else if err := s.write(webhookJSONValue{node: value}, depth+1); err != nil {
			return err
		}
	}
	_, err = s.stage.exec(s.ctx, 0, `DELETE FROM json_keys WHERE map_id=?`, id)
	if err != nil {
		return err
	}
	return s.text("}")
}

func (s *logicalJSONStream) activityIDs(node metadataLogicalNode) error {
	cursor := metadataLogicalCursor{node, node.offset + 16, s.ctx}
	length, err := cursor.uint()
	if err != nil || length > uint64(node.offset+node.size-cursor.offset) {
		return ErrMetadataRecordQuery
	}
	part := metadataCollectionRange{offset: cursor.offset, size: int64(length)}
	parser := taskIssueJSON{ctx: s.ctx, input: node.source.file, owner: s.owner}
	if err := parser.validate(part, reflect.TypeFor[[]string](), 0); err != nil {
		return err
	}
	scanner := sw17CredentialScanner(s.ctx, node.source.file, part.offset, part.size)
	if err := scanner.spaces(); err != nil {
		return err
	}
	first, err := scanner.peek()
	if err != nil {
		return err
	}
	if first == 'n' {
		return s.text("null")
	}
	if err := s.text("["); err != nil {
		return err
	}
	count := 0
	err = scanner.array(func(value metadataCollectionRange) error {
		if count > 0 {
			if err := s.text(","); err != nil {
				return err
			}
		}
		count++
		if err := s.text(`"`); err != nil {
			return err
		}
		_, err := VisitMetadataJSONStringChunks(s.ctx, io.NewSectionReader(node.source.file, value.offset, value.size), func(chunk []byte) error {
			return s.stringContentBytes(chunk)
		})
		if err != nil {
			return err
		}
		return s.text(`"`)
	}, 0)
	if err != nil {
		return err
	}
	return s.text("]")
}

func (s *logicalJSONStream) raw(node metadataLogicalNode) error {
	if s.classifyCodecErrors {
		work, err := metadataAggregateRawValidationWork(s.ctx, node)
		if err != nil {
			return err
		}
		_, release, err := s.owner.store.tryAdmitAdditionalMetadataWork(s.ctx, int(work))
		if err != nil {
			return err
		}
		defer release()
	}
	if err := node.StreamBytes(s.ctx, func(input io.Reader) error {
		if s.classifyCodecErrors {
			return metadataAggregateCodecValidation(input, func(input io.Reader) error {
				return validateMetadataCollectionJSON(s.ctx, input)
			})
		}
		return validateMetadataCollectionJSON(s.ctx, input)
	}); err != nil {
		return err
	}
	return node.StreamBytes(s.ctx, func(input io.Reader) error {
		return s.compactRaw(input)
	})
}

// compactRaw writes validated raw JSON as json.Marshal does: insignificant
// whitespace dropped and <, >, &, U+2028 and U+2029 escaped inside strings.
// It works on buffered runs, copying unchanged bytes in bulk; cancellation is
// observed once per run.
func (s *logicalJSONStream) compactRaw(input io.Reader) error {
	reader := bufio.NewReaderSize(input, 64<<10)
	quoted, escaped := false, false
	for {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		// Three bytes keep a complete U+2028/U+2029 sequence in view unless
		// the input itself ends sooner.
		if head, err := reader.Peek(3); len(head) == 0 {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		chunk, _ := reader.Peek(reader.Buffered())
		start, index := 0, 0
		flush := func(end int) error {
			if end > start {
				if _, err := s.out.Write(chunk[start:end]); err != nil {
					return err
				}
			}
			return nil
		}
	scan:
		for index < len(chunk) {
			char := chunk[index]
			switch {
			case !quoted && (char == ' ' || char == '\n' || char == '\r' || char == '\t'):
				if err := flush(index); err != nil {
					return err
				}
				index++
				start = index
				continue
			case escaped:
				escaped = false
			case quoted && char == '\\':
				escaped = true
			case char == '"':
				quoted = !quoted
			case quoted && (char == '<' || char == '>' || char == '&'):
				if err := flush(index); err != nil {
					return err
				}
				if err := s.text(logicalJSONASCIIQuotes[char]); err != nil {
					return err
				}
				index++
				start = index
				continue
			case quoted && char == 0xe2:
				if index+2 >= len(chunk) && index > 0 {
					// Re-examine this byte with its successors buffered.
					break scan
				}
				if index+2 < len(chunk) && chunk[index+1] == 0x80 && (chunk[index+2] == 0xa8 || chunk[index+2] == 0xa9) {
					if err := flush(index); err != nil {
						return err
					}
					escape := `\u2028`
					if chunk[index+2] == 0xa9 {
						escape = `\u2029`
					}
					if err := s.text(escape); err != nil {
						return err
					}
					index += 3
					start = index
					continue
				}
			}
			index++
		}
		if err := flush(index); err != nil {
			return err
		}
		if _, err := reader.Discard(index); err != nil {
			return err
		}
	}
}

func (s *logicalJSONStream) string(input io.Reader) error {
	if err := s.text(`"`); err != nil {
		return err
	}
	if err := s.stringContent(input); err != nil {
		return err
	}
	return s.text(`"`)
}

func (s *logicalJSONStream) stringString(value string) error {
	if s.rewrite != nil && strings.Contains(value, "/uploads/") {
		source, err := newMetadataJSONStringSource(s.ctx, s.owner, strings.NewReader(value), int64(len(value)), metadataJSONStringQuoted)
		if err != nil {
			return err
		}
		defer source.Close()
		return s.rewrite(s.ctx, source, s.out)
	}
	return s.string(strings.NewReader(value))
}

func (s *logicalJSONStream) stringBytes(value []byte) error {
	return s.string(bytes.NewReader(value))
}

func (s *logicalJSONStream) stringContentBytes(value []byte) error {
	return s.stringContent(bytes.NewReader(value))
}

// Adapt encodeHydratedStringContent to borrowed frame bytes. ReadRune keeps
// UTF-8 boundaries across input chunks and matches encoding/json replacements.
func (s *logicalJSONStream) stringContent(input io.Reader) error {
	reader := bufio.NewReaderSize(input, 4096)
	for {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		char, size, err := reader.ReadRune()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		var escape string
		switch {
		case char < utf8.RuneSelf:
			escape = logicalJSONASCIIQuotes[char]
		case char == utf8.RuneError && size == 1:
			escape = `\ufffd`
		case char == '\u2028':
			escape = `\u2028`
		case char == '\u2029':
			escape = `\u2029`
		}
		if escape != "" {
			if err := s.text(escape); err != nil {
				return err
			}
		} else if _, err := s.out.WriteRune(char); err != nil {
			return err
		}
	}
}

var logicalJSONASCIIQuotes = func() [128]string {
	const hex = "0123456789abcdef"
	var escaped [128]string
	for char := range 32 {
		escaped[char] = string([]byte{'\\', 'u', '0', '0', hex[char>>4], hex[char&0xf]})
	}
	escaped['\\'], escaped['"'] = `\\`, `\"`
	escaped['\n'], escaped['\r'], escaped['\t'], escaped['\b'], escaped['\f'] = `\n`, `\r`, `\t`, `\b`, `\f`
	escaped['<'], escaped['>'], escaped['&'] = `\u003c`, `\u003e`, `\u0026`
	return escaped
}()
