package store

import (
	"context"
	"encoding/json"
	"io"
	"reflect"

	"flow/api/internal/domain"
)

type legacyIssueCanonicalStruct struct {
	header metadataLogicalNode
	fields map[string]metadataLogicalNode
}

func (n legacyIssueCanonicalStruct) nodeType() reflect.Type { return n.header.typ }
func (n legacyIssueCanonicalStruct) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n legacyIssueCanonicalStruct) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	return n.header.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
		if replacement, present := n.fields[key.String()]; present {
			child = replacement
		}
		return visit(key, metadataAggregateStoredNode{child})
	})
}

// The header remains the ordinary typed decoder's input, in source order.
// Opaque fields are replaced with small values and restored from typed frames.
func legacyIssueCanonicalHeader(ctx context.Context, owner *ResourceRead, kind reflect.Type, part legacySessionJSONPart, opaque func(reflect.StructField, legacySessionJSONPart) (string, bool, error)) ([]byte, error) {
	header := &legacyProjectAdmittedBuffer{ctx: ctx, owner: owner}
	scanner := legacySessionJSONScanner(ctx, part)
	if err := scanner.spaces(); err != nil {
		return nil, err
	}
	prefix, err := scanner.peek()
	if err != nil {
		return nil, err
	}
	if prefix == 'n' {
		if err := scanner.literal("null"); err != nil {
			return nil, err
		}
		if err := scanner.end(); err != nil {
			return nil, err
		}
		return []byte("null"), nil
	}
	if prefix != '{' {
		if _, err := scanner.value(0); err != nil {
			return nil, err
		}
		if err := scanner.end(); err != nil {
			return nil, err
		}
		value := "number"
		switch prefix {
		case '[':
			value = "array"
		case '"':
			value = "string"
		case 't', 'f':
			value = "bool"
		}
		return nil, &json.UnmarshalTypeError{Value: value, Type: kind}
	}
	if _, err := header.Write([]byte("{")); err != nil {
		return nil, err
	}
	first := true
	err = scanner.object(func(key, value metadataCollectionRange) error {
		keyOwner := &ResourceRead{store: owner.store}
		defer keyOwner.Close()
		rawKey, err := legacySessionJSONBytes(ctx, keyOwner, legacySessionJSONPart{part.input, key.offset, key.size})
		if err != nil {
			return err
		}
		var name string
		if err := json.Unmarshal(rawKey, &name); err != nil {
			return err
		}
		field, known := resourceWindowJSONField(kind, name)
		if !known {
			// object already validated the entire unknown value, including
			// nested syntax. Its body never enters the typed header.
			return nil
		}
		replacement, handled, err := opaque(field, legacySessionJSONPart{part.input, value.offset, value.size})
		if err != nil {
			return err
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
		if handled {
			_, err = header.Write([]byte(replacement))
		} else {
			_, err = io.CopyBuffer(header, io.NewSectionReader(part.input, value.offset, value.size), make([]byte, metadataRecordChunkBytes))
		}
		return err
	}, 0)
	if err != nil {
		return nil, err
	}
	if err := scanner.end(); err != nil {
		return nil, err
	}
	if _, err := header.Write([]byte("}")); err != nil {
		return nil, err
	}
	return header.data.Bytes(), nil
}

func legacyIssueCanonicalPrefix(ctx context.Context, part legacySessionJSONPart) (byte, error) {
	scanner := legacySessionJSONScanner(ctx, part)
	if err := scanner.spaces(); err != nil {
		return 0, err
	}
	return scanner.peek()
}

// legacyIssueCanonicalRecord is the canonical SQL loader's typed Issue image.
// normalizeIssueRecord is intentional; SQL storage stripping/column projection
// belongs to the caller, as in the original readIssueRecordsWithBase path.
func legacyIssueCanonicalRecord(ctx context.Context, s *SQLiteStore, frames *metadataLogicalBuilder, part legacySessionJSONPart) (metadataLogicalNode, error) {
	owner := &ResourceRead{store: s}
	defer owner.Close()
	if err := owner.reserve(ctx, logicalJSONStreamWork); err != nil {
		return metadataLogicalNode{}, err
	}
	description, err := frames.writeValue(reflect.ValueOf(""), 0, false)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	state := description
	document, err := frames.writeValue(reflect.Zero(reflect.TypeFor[*domain.DocumentContent]()), 0, false)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	header, err := legacyIssueCanonicalHeader(ctx, owner, reflect.TypeFor[domain.Issue](), part,
		func(field reflect.StructField, value legacySessionJSONPart) (string, bool, error) {
			switch field.Name {
			case "Description", "DescriptionState":
				prefix, err := legacyIssueCanonicalPrefix(ctx, value)
				if err != nil {
					return "", false, err
				}
				if prefix != '"' && prefix != 'n' {
					// The standard header decoder supplies the original typed
					// error after the complete object has been scanned.
					return "", false, nil
				}
				target := &description
				if field.Name == "DescriptionState" {
					target = &state
				}
				*target, err = legacyProjectFrameString(ctx, frames, value.reader(), *target)
				return `""`, true, err
			case "DocumentContent":
				prefix, err := legacyIssueCanonicalPrefix(ctx, value)
				if err != nil {
					return "", false, err
				}
				if prefix != '{' && prefix != 'n' {
					return "", false, nil
				}
				document, err = legacyIssueCanonicalDocument(ctx, s, frames, value, document)
				return "null", true, err
			}
			return "", false, nil
		})
	if err != nil {
		return metadataLogicalNode{}, err
	}
	work, err := resourceWindowTypedJSONWork(ctx, header, reflect.TypeFor[domain.Issue]())
	if err != nil {
		return metadataLogicalNode{}, err
	}
	if err := owner.reserve(ctx, work); err != nil {
		return metadataLogicalNode{}, err
	}
	var issue domain.Issue
	if err := json.Unmarshal(header, &issue); err != nil {
		return metadataLogicalNode{}, err
	}
	normalizeIssueRecord(&issue)
	if err := frames.auditJSONLocations(reflect.ValueOf(issue)); err != nil {
		return metadataLogicalNode{}, err
	}
	node, err := frames.writeValue(reflect.ValueOf(issue), 0, false)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	return frames.writeAggregateNode(legacyIssueCanonicalStruct{
		header: node,
		fields: map[string]metadataLogicalNode{"Description": description, "DescriptionState": state, "DocumentContent": document},
	}, 0)
}

func legacyIssueCanonicalDocument(ctx context.Context, s *SQLiteStore, frames *metadataLogicalBuilder, part legacySessionJSONPart, previous metadataLogicalNode) (metadataLogicalNode, error) {
	owner := &ResourceRead{store: s}
	defer owner.Close()
	if err := owner.reserve(ctx, logicalJSONStreamWork); err != nil {
		return metadataLogicalNode{}, err
	}
	prefix, err := legacyIssueCanonicalPrefix(ctx, part)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	if prefix == 'n' {
		scanner := legacySessionJSONScanner(ctx, part)
		if err := scanner.spaces(); err != nil {
			return metadataLogicalNode{}, err
		}
		if err := scanner.literal("null"); err != nil {
			return metadataLogicalNode{}, err
		}
		if err := scanner.end(); err != nil {
			return metadataLogicalNode{}, err
		}
		return frames.writeValue(reflect.Zero(reflect.TypeFor[*domain.DocumentContent]()), 0, false)
	}
	var document domain.DocumentContent
	content, err := frames.writeValue(reflect.ValueOf(""), 0, false)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	state := content
	data, err := frames.writeValue(reflect.ValueOf(map[string]any(nil)), 0, false)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	absent, err := previous.isNil(ctx)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	if !absent {
		var body metadataLogicalNode
		if err := previous.Children(ctx, func(_ reflect.Value, node metadataLogicalNode) error {
			body = node
			return nil
		}); err != nil {
			return metadataLogicalNode{}, err
		}
		if body.typ != reflect.TypeFor[domain.DocumentContent]() {
			return metadataLogicalNode{}, ErrBoundedMetadataCoverage
		}
		header, err := owner.projectNode(ctx, body, "ID", "Version", "UpdatedAt")
		if err != nil {
			return metadataLogicalNode{}, err
		}
		document = header.Interface().(domain.DocumentContent)
		for name, target := range map[string]*metadataLogicalNode{"Content": &content, "ContentState": &state, "ContentData": &data} {
			*target, err = issueHotField(ctx, body, name)
			if err != nil {
				return metadataLogicalNode{}, err
			}
		}
	}
	header, err := legacyIssueCanonicalHeader(ctx, owner, reflect.TypeFor[domain.DocumentContent](), part,
		func(field reflect.StructField, value legacySessionJSONPart) (string, bool, error) {
			switch field.Name {
			case "Content", "ContentState":
				prefix, err := legacyIssueCanonicalPrefix(ctx, value)
				if err != nil {
					return "", false, err
				}
				if prefix != '"' && prefix != 'n' {
					return "", false, nil
				}
				target := &content
				if field.Name == "ContentState" {
					target = &state
				}
				*target, err = legacyProjectFrameString(ctx, frames, value.reader(), *target)
				return `""`, true, err
			case "ContentData":
				prefix, err := legacyIssueCanonicalPrefix(ctx, value)
				if err != nil {
					return "", false, err
				}
				if prefix != '{' && prefix != 'n' {
					return "", false, nil
				}
				data, err = legacyIssueCanonicalContentData(ctx, s, frames, value, data)
				return "null", true, err
			}
			return "", false, nil
		})
	if err != nil {
		return metadataLogicalNode{}, err
	}
	work, err := resourceWindowTypedJSONWork(ctx, header, reflect.TypeFor[domain.DocumentContent]())
	if err != nil {
		return metadataLogicalNode{}, err
	}
	if err := owner.reserve(ctx, work); err != nil {
		return metadataLogicalNode{}, err
	}
	if err := json.Unmarshal(header, &document); err != nil {
		return metadataLogicalNode{}, err
	}
	if err := frames.auditJSONLocations(reflect.ValueOf(document)); err != nil {
		return metadataLogicalNode{}, err
	}
	node, err := frames.writeValue(reflect.ValueOf(document), 0, false)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	return frames.writeAggregateNode(metadataAggregatePointerNode{
		typ: reflect.TypeFor[*domain.DocumentContent](),
		child: legacyIssueCanonicalStruct{header: node, fields: map[string]metadataLogicalNode{
			"Content": content, "ContentState": state, "ContentData": data,
		}},
	}, 0)
}

// SQL ContentData is a normalized generic JSON leaf. Native warm callbacks
// retain their existing typed representation outside this loader boundary.
func legacyIssueCanonicalContentData(ctx context.Context, s *SQLiteStore, frames *metadataLogicalBuilder, part legacySessionJSONPart, previous metadataLogicalNode) (metadataLogicalNode, error) {
	fresh, err := frames.writeGenericJSON(ctx, s, reflect.TypeFor[map[string]any](), part)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	return legacyIssueCanonicalMapUnion(ctx, s, frames, previous, fresh)
}

type legacyIssueCanonicalMapFrames struct {
	stage  *sw26MigrationStage
	frames *metadataLogicalBuilder
	shape  metadataLogicalNode
}

func (n legacyIssueCanonicalMapFrames) nodeType() reflect.Type           { return n.shape.typ }
func (n legacyIssueCanonicalMapFrames) logicalBase() metadataLogicalNode { return n.shape }
func (n legacyIssueCanonicalMapFrames) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n legacyIssueCanonicalMapFrames) nodeChildren(context.Context, func(reflect.Value, metadataAggregateNode) error) error {
	return ErrMetadataRecordQuery
}
func (n legacyIssueCanonicalMapFrames) logicalMapEntries(ctx context.Context, visit func(metadataAggregateNode, metadataAggregateNode) error) error {
	rows, err := n.stage.db.QueryContext(ctx, `SELECT key_offset,key_size,key_cost,key_unavailable,key_blocked,value_offset,value_size,value_cost,value_unavailable,value_blocked FROM issue_content_data ORDER BY key`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		key := metadataLogicalNode{source: n.frames.storage, typ: reflect.TypeFor[string]()}
		value := metadataLogicalNode{source: n.frames.storage, typ: reflect.TypeFor[any]()}
		if err := rows.Scan(&key.offset, &key.size, &key.cost, &key.unavailable, &key.blocked,
			&value.offset, &value.size, &value.cost, &value.unavailable, &value.blocked); err != nil {
			return err
		}
		key.generic, value.generic = key.cost&metadataLogicalGenericJSON != 0, value.cost&metadataLogicalGenericJSON != 0
		key.cost, value.cost = key.cost&metadataLogicalCostMask, value.cost&metadataLogicalCostMask
		if err := visit(metadataAggregateStoredNode{key}, metadataAggregateStoredNode{value}); err != nil {
			return err
		}
	}
	return rows.Err()
}

// Repeated typed map properties shallow-merge keys; each new generic value
// replaces its old value wholesale. The existing SQLite stage sorts and
// collapses keys without a retained Go key set or per-key prior-map copies.
func legacyIssueCanonicalMapUnion(ctx context.Context, s *SQLiteStore, frames *metadataLogicalBuilder, previous, fresh metadataLogicalNode) (metadataLogicalNode, error) {
	nilFresh, err := fresh.isNil(ctx)
	if err != nil || nilFresh {
		return fresh, err
	}
	nilPrevious, err := previous.isNil(ctx)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	if nilPrevious {
		return fresh, nil
	}
	empty := false
	if fresh.generic {
		empty, err = fresh.genericEmpty(ctx, false)
	} else {
		cursor := metadataLogicalCursor{fresh, fresh.offset + 17, ctx}
		var count uint64
		count, err = cursor.uint()
		empty = count == 0
	}
	if err != nil {
		return metadataLogicalNode{}, err
	}
	if empty {
		// A repeated empty object keeps the existing map without copying
		// its potentially large retained values into another frame.
		return previous, nil
	}
	owner := &ResourceRead{store: s}
	defer owner.Close()
	if err := owner.reserve(ctx, logicalJSONStreamWork); err != nil {
		return metadataLogicalNode{}, err
	}
	stage, err := sw26NewMigrationStage(ctx)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	defer stage.Close()
	if _, err := stage.exec(ctx, 4096, `CREATE TABLE issue_content_data(key BLOB PRIMARY KEY,key_offset INTEGER,key_size INTEGER,key_cost INTEGER,key_unavailable INTEGER,key_blocked INTEGER,value_offset INTEGER,value_size INTEGER,value_cost INTEGER,value_unavailable INTEGER,value_blocked INTEGER)`); err != nil {
		return metadataLogicalNode{}, err
	}
	keys := &logicalJSONStream{ctx: ctx, owner: owner}
	for _, source := range []metadataLogicalNode{previous, fresh} {
		if err := source.MapEntries(ctx, func(key, value metadataLogicalNode) error {
			if key.source != frames.storage || value.source != frames.storage {
				return ErrBoundedMetadataCoverage
			}
			// Borrowed raw ranges are not independently framed receipts.
			// Serialize their header before persisting the stage descriptor.
			if key.genericRange {
				key, err = frames.copyNode(key)
				if err != nil {
					return err
				}
			}
			if value.genericRange {
				value, err = frames.copyNode(value)
				if err != nil {
					return err
				}
			}
			return keys.mapKey(key, func(name string) error {
				keyCost, valueCost := key.cost, value.cost
				if key.generic {
					keyCost |= metadataLogicalGenericJSON
				}
				if value.generic {
					valueCost |= metadataLogicalGenericJSON
				}
				_, err := stage.exec(ctx, int64(len(name)+256),
					`INSERT INTO issue_content_data VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(key) DO UPDATE SET key_offset=excluded.key_offset,key_size=excluded.key_size,key_cost=excluded.key_cost,key_unavailable=excluded.key_unavailable,key_blocked=excluded.key_blocked,value_offset=excluded.value_offset,value_size=excluded.value_size,value_cost=excluded.value_cost,value_unavailable=excluded.value_unavailable,value_blocked=excluded.value_blocked`,
					[]byte(name), key.offset, key.size, keyCost, key.unavailable, key.blocked,
					value.offset, value.size, valueCost, value.unavailable, value.blocked)
				return err
			})
		}); err != nil {
			return metadataLogicalNode{}, err
		}
	}
	return frames.writeAggregateNode(legacyIssueCanonicalMapFrames{stage, frames, fresh}, 0)
}
