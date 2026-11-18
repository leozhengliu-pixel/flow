package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"

	"flow/api/internal/domain"
)

func integrationDeliveryStreamType(field string) reflect.Type {
	switch field {
	case "integrationDeliveries":
		return reflect.TypeFor[domain.IntegrationDelivery]()
	case "integrationConnections":
		return reflect.TypeFor[domain.IntegrationConnection]()
	}
	return nil
}

func integrationDeliveryStreamMembers(ctx context.Context, field string) map[string]bool {
	event := integrationDeliveryStreamEvent(ctx)
	if event != "integration_delivery.claimed" && event != "integration_delivery.retry" {
		return nil
	}
	switch field {
	case "integrationDeliveries":
		fields := map[string]bool{"Status": true, "UpdatedAt": true}
		if event == "integration_delivery.retry" {
			fields["Attempts"], fields["LastError"], fields["NextAttemptAt"] = true, true, true
		}
		return fields
	case "integrationConnections":
		if event == "integration_delivery.claimed" {
			return map[string]bool{}
		}
		return map[string]bool{"DeliveryAttempts": true, "LastDeliveryAt": true, "LastError": true}
	}
	return nil
}

func integrationDeliveryScalarPatchOnly(ctx context.Context, field string, before, next metadataLogicalNode) (bool, error) {
	allowed := integrationDeliveryStreamMembers(ctx, field)
	typ := integrationDeliveryStreamType(field)
	if allowed == nil || typ == nil || before.typ != typ || next.typ != typ {
		return false, nil
	}
	closed := true
	err := before.Children(ctx, func(key reflect.Value, left metadataLogicalNode) error {
		if allowed[key.String()] {
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

type integrationCanonicalNode struct {
	header metadataLogicalNode
	fields map[string]metadataLogicalNode
}

func (n integrationCanonicalNode) nodeType() reflect.Type { return n.header.typ }
func (n integrationCanonicalNode) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n integrationCanonicalNode) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	return n.header.Children(ctx, func(key reflect.Value, node metadataLogicalNode) error {
		if replacement, found := n.fields[key.String()]; found {
			node = replacement
		}
		return visit(key, metadataAggregateStoredNode{node})
	})
}

func integrationDeliveryCanonicalRecord(ctx context.Context, store *SQLiteStore, frames *metadataLogicalBuilder, part legacySessionJSONPart) (metadataLogicalNode, error) {
	return integrationCanonicalRecord(ctx, store, frames, part, reflect.TypeFor[domain.IntegrationDelivery]())
}

func integrationConnectionCanonicalRecord(ctx context.Context, store *SQLiteStore, frames *metadataLogicalBuilder, part legacySessionJSONPart) (metadataLogicalNode, error) {
	return integrationCanonicalRecord(ctx, store, frames, part, reflect.TypeFor[domain.IntegrationConnection]())
}

// Keep slice backing values across repeated properties, as encoding/json does.
// Null string elements reuse the previous value of that backing-array slot.
func integrationFrameStrings(ctx context.Context, owner *ResourceRead, frames *metadataLogicalBuilder, part legacySessionJSONPart, backing []metadataLogicalNode, kind reflect.Type, name string) (metadataLogicalNode, []metadataLogicalNode, error) {
	scanner := legacySessionJSONScanner(ctx, part)
	if err := scanner.spaces(); err != nil {
		return metadataLogicalNode{}, nil, err
	}
	prefix, err := scanner.peek()
	if err != nil {
		return metadataLogicalNode{}, nil, err
	}
	if prefix != 'n' && prefix != '[' {
		return metadataLogicalNode{}, nil, &json.UnmarshalTypeError{
			Value: workflowJSONValueKind(prefix), Type: reflect.TypeFor[[]string](), Struct: kind.Name(), Field: name,
		}
	}
	parts, err := legacySessionJSONArray(ctx, owner, part)
	if err != nil {
		return metadataLogicalNode{}, nil, err
	}
	var shapeValue []string
	if prefix == '[' {
		shapeValue = []string{}
	}
	shape, err := frames.writeValue(reflect.ValueOf(shapeValue), 0, false)
	if err != nil {
		return metadataLogicalNode{}, nil, err
	}
	if prefix == 'n' {
		return shape, nil, nil
	}
	if len(parts) == 0 {
		return shape, []metadataLogicalNode{}, nil
	}
	empty, err := frames.writeValue(reflect.ValueOf(""), 0, false)
	if err != nil {
		return metadataLogicalNode{}, nil, err
	}
	for index, part := range parts {
		var first [1]byte
		if _, err := part.input.ReadAt(first[:], part.start); err != nil {
			return metadataLogicalNode{}, nil, err
		}
		if first[0] != '"' && first[0] != 'n' {
			return metadataLogicalNode{}, nil, &json.UnmarshalTypeError{
				Value: workflowJSONValueKind(first[0]), Type: reflect.TypeFor[string](), Struct: kind.Name(), Field: name,
			}
		}
		previous := empty
		if index < len(backing) {
			previous = backing[index]
		}
		node, err := legacyProjectFrameString(ctx, frames, part.reader(), previous)
		if err != nil {
			return metadataLogicalNode{}, nil, err
		}
		if index < len(backing) {
			backing[index] = node
		} else {
			if err := owner.reserve(ctx, 256); err != nil {
				return metadataLogicalNode{}, nil, err
			}
			backing = append(backing, node)
		}
	}
	node, err := frames.writeAggregateNode(legacySessionMessagesNode{shape, backing[:len(parts)]}, 0)
	return node, backing, err
}

// This is the existing typed-header decoder with streamed replacements for
// every string, RawMessage, string map and string slice in these two types.
func integrationCanonicalRecord(ctx context.Context, store *SQLiteStore, frames *metadataLogicalBuilder, part legacySessionJSONPart, kind reflect.Type) (metadataLogicalNode, error) {
	if kind != reflect.TypeFor[domain.IntegrationDelivery]() && kind != reflect.TypeFor[domain.IntegrationConnection]() {
		return metadataLogicalNode{}, ErrBoundedMetadataCoverage
	}
	owner := &ResourceRead{store: store}
	defer owner.Close()
	if err := owner.reserve(ctx, logicalJSONStreamWork); err != nil {
		return metadataLogicalNode{}, err
	}
	stage, err := sw26NewMigrationStage(ctx)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	defer stage.Close()
	if _, err := stage.exec(ctx, 4096, `CREATE TABLE workflow_output(key BLOB PRIMARY KEY,key_offset INTEGER,key_size INTEGER,key_cost INTEGER,value_offset INTEGER,value_size INTEGER,value_cost INTEGER)`); err != nil {
		return metadataLogicalNode{}, err
	}
	replacements := map[string]metadataLogicalNode{}
	listBacking := map[string][]metadataLogicalNode{}
	for index := range kind.NumField() {
		field := kind.Field(index)
		if field.PkgPath != "" || field.Tag.Get("json") == "-" {
			continue
		}
		if field.Type.Kind() == reflect.String || field.Type == reflect.TypeFor[json.RawMessage]() ||
			field.Type == reflect.TypeFor[map[string]string]() || field.Type == reflect.TypeFor[[]string]() {
			node, err := frames.writeValue(reflect.Zero(field.Type), 0, false)
			if err != nil {
				return metadataLogicalNode{}, err
			}
			replacements[field.Name] = node
		}
	}
	configNil := true
	header := &legacyProjectAdmittedBuffer{ctx: ctx, owner: owner}
	scanner := legacySessionJSONScanner(ctx, part)
	if err := scanner.spaces(); err != nil {
		return metadataLogicalNode{}, err
	}
	if prefix, err := scanner.peek(); err != nil {
		return metadataLogicalNode{}, err
	} else if prefix == 'n' {
		if err := scanner.literal("null"); err != nil {
			return metadataLogicalNode{}, err
		}
		if err := scanner.end(); err != nil {
			return metadataLogicalNode{}, err
		}
		return frames.writeValue(reflect.Zero(kind), 0, false)
	}
	if _, err := header.Write([]byte("{")); err != nil {
		return metadataLogicalNode{}, err
	}
	first := true
	err = scanner.object(func(key, value metadataCollectionRange) error {
		rawKey, err := legacySessionJSONBytes(ctx, owner, legacySessionJSONPart{part.input, key.offset, key.size})
		if err != nil {
			return err
		}
		var name string
		if err := json.Unmarshal(rawKey, &name); err != nil {
			return err
		}
		field, known := resourceWindowJSONField(kind, name)
		if !known {
			// The scanner validates ignored fields without copying their bodies.
			return nil
		}
		jsonName, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if jsonName == "" {
			jsonName = field.Name
		}
		if !first {
			if _, err := header.Write([]byte(",")); err != nil {
				return err
			}
		}
		first = false
		if _, err := header.Write(rawKey); err != nil {
			return err
		}
		if _, err := header.Write([]byte(":")); err != nil {
			return err
		}
		input := legacySessionJSONPart{part.input, value.offset, value.size}
		switch {
		case field.Type.Kind() == reflect.String:
			var prefix [1]byte
			if _, err := part.input.ReadAt(prefix[:], value.offset); err != nil {
				return err
			}
			if prefix[0] != '"' && prefix[0] != 'n' {
				return &json.UnmarshalTypeError{Value: workflowJSONValueKind(prefix[0]), Type: field.Type, Struct: kind.Name(), Field: jsonName}
			}
			replacements[field.Name], err = legacyProjectFrameString(ctx, frames, input.reader(), replacements[field.Name])
			if err == nil {
				_, err = header.Write([]byte(`""`))
			}
		case field.Type == reflect.TypeFor[json.RawMessage]():
			replacements[field.Name], err = frames.writeRawMessageFrame(ctx, input.reader(), input.size)
			if err == nil {
				_, err = header.Write([]byte("null"))
			}
		case field.Type == reflect.TypeFor[map[string]string]():
			replacements[field.Name], configNil, err = workflowFrameStringMap(ctx, owner, frames, stage, input, configNil, kind.Name(), jsonName)
			if err == nil {
				_, err = header.Write([]byte("null"))
			}
		case field.Type == reflect.TypeFor[[]string]():
			replacements[field.Name], listBacking[field.Name], err = integrationFrameStrings(ctx, owner, frames, input, listBacking[field.Name], kind, jsonName)
			if err == nil {
				_, err = header.Write([]byte("null"))
			}
		default:
			_, err = io.CopyBuffer(header, input.reader(), make([]byte, metadataRecordChunkBytes))
		}
		return err
	}, 0)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	if err := scanner.end(); err != nil {
		return metadataLogicalNode{}, err
	}
	if _, err := header.Write([]byte("}")); err != nil {
		return metadataLogicalNode{}, err
	}
	work, err := resourceWindowTypedJSONWork(ctx, header.data.Bytes(), kind)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	if err := owner.reserve(ctx, work); err != nil {
		return metadataLogicalNode{}, err
	}
	value := reflect.New(kind)
	if err := json.Unmarshal(header.data.Bytes(), value.Interface()); err != nil {
		return metadataLogicalNode{}, err
	}
	if err := frames.auditJSONLocations(value.Elem()); err != nil {
		return metadataLogicalNode{}, err
	}
	node, err := frames.writeValue(value.Elem(), 0, false)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	return frames.writeAggregateNode(integrationCanonicalNode{node, replacements}, 0)
}

func integrationCanonicalDecoder(field string) func(context.Context, *SQLiteStore, *metadataLogicalBuilder, legacySessionJSONPart) (metadataLogicalNode, error) {
	if field == "integrationDeliveries" {
		return integrationDeliveryCanonicalRecord
	}
	if field == "integrationConnections" {
		return integrationConnectionCanonicalRecord
	}
	return nil
}

func integrationLoadedCanonical(ctx context.Context, frames *metadataLogicalBuilder, node metadataLogicalNode) (metadataLogicalNode, error) {
	if node.typ != reflect.TypeFor[domain.IntegrationConnection]() {
		return node, nil
	}
	fields := map[string]metadataLogicalNode{}
	for _, name := range []string{"Scopes", "Channels"} {
		value, err := issueHotField(ctx, node, name)
		if err != nil {
			return metadataLogicalNode{}, err
		}
		absent, err := value.isNil(ctx)
		if err != nil {
			return metadataLogicalNode{}, err
		}
		if absent {
			fields[name], err = frames.writeValue(reflect.ValueOf([]string{}), 0, false)
			if err != nil {
				return metadataLogicalNode{}, err
			}
		}
	}
	if len(fields) == 0 {
		return node, nil
	}
	return frames.writeAggregateNode(integrationCanonicalNode{node, fields}, 0)
}

func integrationKeepIgnored(ctx context.Context, frames *metadataLogicalBuilder, node, keep metadataLogicalNode) (metadataLogicalNode, error) {
	if node.typ != keep.typ {
		return metadataLogicalNode{}, ErrBoundedMetadataCoverage
	}
	fields := map[string]metadataLogicalNode{}
	for index := range node.typ.NumField() {
		field := node.typ.Field(index)
		if field.PkgPath != "" || field.Tag.Get("json") != "-" {
			continue
		}
		value, err := issueHotField(ctx, keep, field.Name)
		if err != nil {
			return metadataLogicalNode{}, err
		}
		fields[field.Name] = value
	}
	if len(fields) == 0 {
		return node, nil
	}
	return frames.writeAggregateNode(integrationCanonicalNode{node, fields}, 0)
}

func (p *legacyMetadataWritePlan) rebaseIntegrationDeliveryRow(ctx context.Context, field *legacyMetadataWriteField, index int) (bool, error) {
	row := &field.rows[index]
	closed, err := integrationDeliveryScalarPatchOnly(ctx, field.field.name, row.before, row.next)
	if err != nil || !closed {
		return false, err
	}
	key := metadataRecordKey{field.field.name, row.id}
	if p.tx.rebasedMetadata[key] {
		return true, nil
	}
	owner := &ResourceRead{store: p.store}
	defer owner.Close()
	if err := owner.reserve(ctx, logicalJSONStreamWork); err != nil {
		return true, err
	}
	lock := ""
	if p.tx.dialect != "sqlite" {
		lock = " FOR UPDATE"
	}
	rememberOrder := p.tx.dialect == "sqlite" && p.store.db.journalReady.Load()
	if rememberOrder {
		p.tx.journalOrder.valid = false
	}
	var size, order, keyBytes int64
	var physical sql.NullString
	err = p.tx.QueryRowContext(ctx, "SELECT "+owner.byteLength("record_key")+
		",CASE WHEN "+owner.byteLength("record_key")+"<=? THEN record_key ELSE NULL END,"+
		owner.byteLength("data")+",collection_order FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?"+lock,
		MetadataRecordPageMaxBytes, p.workspace, field.field.name, row.id).Scan(&keyBytes, &physical, &size, &order)
	missing := errors.Is(err, sql.ErrNoRows)
	if err != nil && !missing {
		return true, err
	}
	if !missing {
		if keyBytes < 0 || keyBytes > MetadataRecordPageMaxBytes || !physical.Valid {
			return true, ErrMetadataPageBudget
		}
		if rememberOrder {
			p.tx.journalOrder = recordJournalOrder{p.workspace, field.field.name, physical.String, order, true}
		}
		// lockMetadataRecords keys by the SQL-returned physical key. A
		// collation or textArgs alias must not become a native-key match.
		missing = physical.String != row.id
	}
	if missing {
		backed, err := metadataFieldRecordBacked(ctx, p.tx, p.workspace, field.field.name, field.before.typ)
		if err != nil {
			return true, err
		}
		if !backed {
			return true, nil
		}
		if p.tx.rebasedMetadata == nil {
			p.tx.rebasedMetadata = map[metadataRecordKey]bool{}
		}
		p.tx.rebasedMetadata[key], row.deleted = true, true
		return true, nil
	}
	if size < 0 || size > metadataLogicalDiskBytes {
		return true, ErrMetadataWorkBudget
	}
	if size <= MetadataRecordPageMaxBytes && row.before.cost <= 64<<10 && row.next.cost <= 64<<10 {
		return false, nil
	}
	stored, err := newMetadataLogicalRecordSpool(ctx)
	if err != nil {
		return true, err
	}
	defer stored.Close()
	reader := &canonicalMetadataReader{ctx: ctx, tx: p.tx, workspace: p.workspace, key: RecordKey{field.field.name, physical.String}, size: size, pageBytes: MetadataRecordPageMaxBytes}
	count, err := io.CopyBuffer(stored, reader, make([]byte, metadataRecordChunkBytes))
	traceMark(ctx, "integration-rebase-source")
	if err != nil || count != size {
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
	baseFields, err := legacySessionJSONObject(ctx, owner, legacySessionSpoolPart(base))
	if err != nil {
		return true, err
	}
	rawFields, err := legacySessionJSONObject(ctx, owner, legacySessionSpoolPart(stored))
	if err != nil {
		return true, err
	}
	traceMark(ctx, "integration-rebase-spools")
	if p.tx.rebasedMetadata == nil {
		p.tx.rebasedMetadata = map[metadataRecordKey]bool{}
	}
	p.tx.rebasedMetadata[key] = true
	rawEqual, err := integrationObjectsEqual(ctx, owner, rawFields, baseFields)
	traceMark(ctx, "integration-rebase-equality")
	if err != nil || rawEqual {
		return true, err
	}
	decode := integrationCanonicalDecoder(field.field.name)
	if decode == nil {
		return true, ErrBoundedMetadataCoverage
	}
	theirNode, err := decode(ctx, p.store, p.frames, legacySessionSpoolPart(stored))
	if err != nil {
		return true, err
	}
	loadedNode, err := integrationLoadedCanonical(ctx, p.frames, theirNode)
	if err != nil {
		return true, err
	}
	loaded, err := legacySessionNodeSpool(ctx, owner, loadedNode)
	if err != nil {
		return true, err
	}
	defer loaded.Close()
	loadedFields, err := legacySessionJSONObject(ctx, owner, legacySessionSpoolPart(loaded))
	if err != nil {
		return true, err
	}
	traceMark(ctx, "integration-rebase-loaded")
	loadedEqual, err := integrationObjectsEqual(ctx, owner, loadedFields, baseFields)
	traceMark(ctx, "integration-rebase-loaded-equality")
	if err != nil || loadedEqual {
		return true, err
	}
	theirs, err := legacySessionNodeSpool(ctx, owner, theirNode)
	if err != nil {
		return true, err
	}
	defer theirs.Close()
	theirFields, err := legacySessionJSONObject(ctx, owner, legacySessionSpoolPart(theirs))
	if err != nil {
		return true, err
	}
	ours, err := legacySessionNodeSpool(ctx, owner, row.next)
	if err != nil {
		return true, err
	}
	defer ours.Close()
	ourFields, err := legacySessionJSONObject(ctx, owner, legacySessionSpoolPart(ours))
	if err != nil {
		return true, err
	}
	names := map[string]bool{}
	for name := range baseFields {
		names[name] = true
	}
	for name := range ourFields {
		names[name] = true
	}
	merged := make(map[string]legacySessionJSONPart, len(theirFields))
	for name, part := range theirFields {
		merged[name] = part
	}
	for name := range names {
		equal, err := legacySessionValueEqual(ctx, owner, baseFields[name], ourFields[name])
		if err != nil {
			return true, err
		}
		if equal {
			continue
		}
		equal, err = legacySessionValueEqual(ctx, owner, loadedFields[name], baseFields[name])
		if err != nil {
			return true, err
		}
		value := ourFields[name]
		if !equal {
			member, known := resourceWindowJSONField(row.before.typ, name)
			if !known || !integrationDeliveryStreamMembers(ctx, field.field.name)[member.Name] {
				return true, ErrBoundedMetadataCoverage
			}
			if member.Type.Kind() == reflect.String {
				// These closed members are strings, never recursive maps or
				// additive counters. This is mergeMetadataMember's scalar
				// equality branch without materializing a large LastError.
				equal, err = legacySessionValueEqual(ctx, owner, value, loadedFields[name])
				if err != nil {
					return true, err
				}
				if !equal {
					return true, &metadataConflictError{keys: []metadataRecordKey{key}}
				}
			} else {
				var scalar [3]json.RawMessage
				for index, part := range []legacySessionJSONPart{baseFields[name], value, loadedFields[name]} {
					if part.input != nil {
						scalar[index], err = legacySessionJSONBytes(ctx, owner, part)
						if err != nil {
							return true, err
						}
					}
				}
				result, ok := mergeMetadataMember(name, scalar[0], scalar[1], scalar[2])
				if !ok {
					return true, &metadataConflictError{keys: []metadataRecordKey{key}}
				}
				if result == nil {
					value = legacySessionJSONPart{}
				} else {
					// These non-string members are typed ints or times, with
					// fixed-size native output from the existing decoder.
					if err := owner.reserve(ctx, uint64(len(result))*4+64); err != nil {
						return true, err
					}
					value = legacySessionJSONPart{strings.NewReader(string(result)), 0, int64(len(result))}
				}
			}
		}
		if value.input == nil {
			delete(merged, name)
		} else {
			merged[name] = value
		}
	}
	nextJSON, err := newMetadataLogicalRecordSpool(ctx)
	if err != nil {
		return true, err
	}
	defer nextJSON.Close()
	if err := legacySessionWriteJSONObject(ctx, owner, merged, nextJSON); err != nil {
		return true, err
	}
	next, err := decode(ctx, p.store, p.frames, legacySessionSpoolPart(nextJSON))
	if err != nil {
		return true, err
	}
	// mergeWithStoredRecord decodes its already-marshaled "theirs" into
	// before, so RawMessage bytes there are compacted/escaped as well.
	before, err := decode(ctx, p.store, p.frames, legacySessionSpoolPart(theirs))
	if err != nil {
		return true, err
	}
	before, err = integrationKeepIgnored(ctx, p.frames, before, row.before)
	if err != nil {
		return true, err
	}
	next, err = integrationKeepIgnored(ctx, p.frames, next, row.next)
	if err != nil {
		return true, err
	}
	row.before, row.next = before, next
	idNode, err := issueHotField(ctx, next, "ID")
	if err != nil {
		return true, err
	}
	id, err := owner.node(ctx, idNode, false)
	if err != nil {
		return true, err
	}
	row.id = id.String()
	return true, nil
}

func (p *legacyMetadataWritePlan) writeIntegrationDeliveryRow(ctx context.Context, field *legacyMetadataWriteField, row *legacyMetadataWriteRow, order int) (bool, error) {
	return p.writeIntegrationDeliveryNativeRow(ctx, field, row, order, false)
}

// Both maps must come from legacySessionJSONObject, which validates their JSON
// before identical bytes can bypass the canonical comparison.
func integrationObjectsEqual(ctx context.Context, owner *ResourceRead, left, right map[string]legacySessionJSONPart) (bool, error) {
	if len(left) != len(right) {
		return false, nil
	}
	for name, a := range left {
		b, found := right[name]
		if !found {
			return false, nil
		}
		if a.input != nil && b.input != nil {
			same, err := legacySessionRawEqual(ctx, a, b)
			if err != nil {
				return false, err
			}
			if same {
				continue
			}
		}
		same, err := legacySessionPartEqual(ctx, owner, a, b)
		if err != nil || !same {
			return same, err
		}
	}
	return true, nil
}

func integrationVisibleScalarChanged(ctx context.Context, owner *ResourceRead, before, next metadataLogicalNode) (bool, error) {
	if before.typ != next.typ {
		return false, nil
	}
	var names []string
	switch before.typ {
	case reflect.TypeFor[domain.IntegrationDelivery]():
		names = []string{"Attempts", "Status"}
	case reflect.TypeFor[domain.IntegrationConnection]():
		names = []string{"DeliveryAttempts"}
	default:
		return false, nil
	}
	for _, name := range names {
		var raw [2][]byte
		bounded := true
		for index, record := range []metadataLogicalNode{before, next} {
			node, err := issueHotField(ctx, record, name)
			if err != nil {
				return false, err
			}
			if node.cost > 4<<10 || node.typ != reflect.TypeFor[int]() && node.typ != reflect.TypeFor[string]() {
				bounded = false
				break
			}
			value, err := owner.node(ctx, node, false)
			if err != nil {
				return false, err
			}
			raw[index], err = json.Marshal(value.Interface())
			if err != nil {
				return false, err
			}
		}
		if bounded && !bytes.Equal(raw[0], raw[1]) {
			return true, nil
		}
	}
	return false, nil
}

func integrationCanSkipBeforeJSON(ctx context.Context, store *SQLiteStore, before, next metadataLogicalNode, force bool) (bool, error) {
	owner := &ResourceRead{store: store}
	defer owner.Close()
	changed := force
	var err error
	if !changed {
		changed, err = integrationVisibleScalarChanged(ctx, owner, before, next)
		if err != nil || !changed {
			return false, ctx.Err()
		}
	}
	var times []string
	switch before.typ {
	case reflect.TypeFor[domain.IntegrationDelivery]():
		times = []string{"UpdatedAt", "NextAttemptAt"}
	case reflect.TypeFor[domain.IntegrationConnection]():
		times = []string{"LastDeliveryAt"}
	default:
		return false, nil
	}
	// A callback can replace an invalid old time with a valid value. Preserve
	// the original before-spool failure even when a scalar proves a change.
	for _, name := range times {
		node, err := issueHotField(ctx, before, name)
		if err != nil || node.typ == nil || node.cost > 4<<10 {
			return false, ctx.Err()
		}
		value, err := owner.node(ctx, node, false)
		if err != nil {
			return false, ctx.Err()
		}
		if _, err := json.Marshal(value.Interface()); err != nil {
			return false, ctx.Err()
		}
	}
	return true, ctx.Err()
}

// Any candidate keeps the exact stripping path, including escaped backslashes.
func integrationStoredJSONIdentity(ctx context.Context, spool *RecordSpool) (bool, error) {
	const token = `\u0000`
	var buffer [metadataRecordChunkBytes + len(token) - 1]byte
	retained := 0
	for offset := int64(0); offset < spool.bytes; {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		size := int(min(int64(metadataRecordChunkBytes), spool.bytes-offset))
		if _, err := spool.ReadAt(buffer[retained:retained+size], offset); err != nil {
			return false, err
		}
		total := retained + size
		if bytes.Contains(buffer[:total], []byte(token)) {
			return false, nil
		}
		retained = min(len(token)-1, total)
		copy(buffer[:retained], buffer[total-retained:total])
		offset += int64(size)
	}
	return true, ctx.Err()
}

func (p *legacyMetadataWritePlan) writeIntegrationDeliveryNativeRow(ctx context.Context, field *legacyMetadataWriteField, row *legacyMetadataWriteRow, order int, force bool) (bool, error) {
	return p.writeIntegrationDeliveryNativeRowWithSpool(ctx, field, row, order, force, nil)
}

func (p *legacyMetadataWritePlan) writeIntegrationDeliveryNativeRowWithSpool(ctx context.Context, field *legacyMetadataWriteField, row *legacyMetadataWriteRow, order int, force bool, encoded *integrationDeliveryRowSpool) (bool, error) {
	closed, err := integrationDeliveryScalarPatchOnly(ctx, field.field.name, row.before, row.next)
	if err != nil || !closed {
		return false, err
	}
	if !force && row.before.cost <= 64<<10 && row.next.cost <= 64<<10 {
		return false, nil
	}
	if metadataSearchFieldSet[field.field.name] || offloadFields[field.field.name] {
		return true, ErrBoundedMetadataCoverage
	}
	owner := &ResourceRead{store: p.store}
	defer owner.Close()
	if err := owner.reserve(ctx, logicalJSONStreamWork); err != nil {
		return true, err
	}
	changed, err := integrationCanSkipBeforeJSON(ctx, p.store, row.before, row.next, force)
	if err != nil {
		return true, err
	}
	var before *RecordSpool
	if !changed {
		before, err = legacySessionNodeSpool(ctx, owner, row.before)
		if err != nil {
			return true, err
		}
		defer before.Close()
	}
	next := encoded.borrow(p, field, row)
	if next == nil {
		next, err = legacySessionNodeSpool(ctx, owner, row.next)
		if err != nil {
			return true, err
		}
		defer next.Close()
	}
	traceMark(ctx, "integration-row-spools")
	if !changed {
		beforeFields, err := legacySessionJSONObject(ctx, owner, legacySessionSpoolPart(before))
		if err != nil {
			return true, err
		}
		nextFields, err := legacySessionJSONObject(ctx, owner, legacySessionSpoolPart(next))
		if err != nil {
			return true, err
		}
		traceMark(ctx, "integration-row-scan")
		same, err := integrationObjectsEqual(ctx, owner, beforeFields, nextFields)
		traceMark(ctx, "integration-row-equality")
		if err != nil || same {
			return true, err
		}
	}
	identity, err := integrationStoredJSONIdentity(ctx, next)
	if err != nil {
		return true, err
	}
	size := int(next.bytes)
	if !identity {
		if _, err := next.Seek(0, io.SeekStart); err != nil {
			return true, err
		}
		var count legacyProjectByteCounter
		if err := legacyProjectCopyStoredJSON(ctx, &count, next); err != nil {
			return true, err
		}
		size = int(count)
	}
	traceMark(ctx, "integration-row-size")
	if raw, binary, err := integrationDeliveryBinarySpool(ctx, owner, next); err != nil {
		return true, err
	} else if binary {
		return true, writeMetadataRecordRow(ctx, p.tx, p.workspace, metadataRecordKey{field.field.name, row.id},
			metadataRecord{order: order, data: raw}, nil)
	}
	if _, err := next.Seek(0, io.SeekStart); err != nil {
		return true, err
	}
	stage, err := beginMetadataSQLWriteStage(ctx, p.tx, metadataSQLStageTarget{
		workspace: p.workspace, field: field.field.name, key: row.id, order: order, dataBytes: size, searchBytes: -1,
	})
	if err != nil {
		return true, err
	}
	defer stage.Close(context.WithoutCancel(ctx))
	writer, err := stage.writer(ctx, "data")
	if err != nil {
		return true, err
	}
	traceMark(ctx, "integration-row-stage")
	if identity {
		_, err = io.CopyBuffer(writer, next, make([]byte, metadataRecordChunkBytes))
	} else {
		err = legacyProjectCopyStoredJSON(ctx, writer, next)
	}
	traceMark(ctx, "integration-row-copy")
	if err != nil {
		return true, err
	}
	err = writer.flush(true)
	traceMark(ctx, "integration-row-flush")
	if err != nil {
		return true, err
	}
	err = stage.writeMetadataRow(ctx)
	traceMark(ctx, "integration-row-install")
	if err != nil {
		return true, err
	}
	if err := writeCustomerFilterRecord(ctx, p.tx, p.workspace, field.field.name, row.id, nil); err != nil {
		return true, err
	}
	if err := writeReleaseIssueRecords(ctx, p.tx, p.workspace, field.field.name, row.id, nil); err != nil {
		return true, err
	}
	if err := syncMetadataSearchDocument(ctx, p.tx, p.workspace, field.field.name, row.id, nil); err != nil {
		return true, err
	}
	err = stage.Close(ctx)
	traceMark(ctx, "integration-row-tail")
	return true, err
}
