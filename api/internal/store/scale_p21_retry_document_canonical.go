package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"reflect"

	"flow/api/internal/domain"
)

// SQL retry images have the typed decoder's defaults, not resident document
// normalization. Body strings/maps remain immutable children of the header.
func metadataRetryDocumentCanonicalRecord(ctx context.Context, s *SQLiteStore, frames *metadataLogicalBuilder, part legacySessionJSONPart) (metadataLogicalNode, error) {
	ctx = metadataLogicalSourceScratchContext(ctx, frames.source)
	node, err := metadataRetryDocumentStruct(ctx, s, frames, reflect.TypeFor[domain.Document](), part, metadataLogicalNode{})
	if err != nil {
		return metadataLogicalNode{}, err
	}
	if err := ctx.Err(); err != nil {
		return metadataLogicalNode{}, err
	}
	return node, nil
}

func metadataRetryDocumentStruct(ctx context.Context, s *SQLiteStore, frames *metadataLogicalBuilder, kind reflect.Type, part legacySessionJSONPart, previous metadataLogicalNode, replay ...*metadataRetryRevisionHeaderReplay) (metadataLogicalNode, error) {
	if len(replay) > 1 {
		return metadataLogicalNode{}, ErrMetadataRecordQuery
	}
	var history *metadataRetryRevisionHeaderReplay
	if len(replay) == 1 {
		history = replay[0]
	}
	owner := &ResourceRead{store: s}
	defer owner.Close()
	if err := owner.reserve(ctx, logicalJSONStreamWork); err != nil {
		return metadataLogicalNode{}, err
	}
	empty, err := frames.writeValue(reflect.ValueOf(""), 0, false)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	data, err := frames.writeValue(reflect.ValueOf(map[string]any(nil)), 0, false)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	fields := map[string]metadataLogicalNode{"Content": empty, "ContentState": empty, "ContentData": data}
	headerValue := reflect.New(kind).Elem()
	if previous.typ != nil {
		for name := range fields {
			fields[name], err = issueHotField(ctx, previous, name)
			if err != nil {
				return metadataLogicalNode{}, err
			}
		}
		if history == nil {
			zeros := map[string]metadataLogicalNode{"Content": empty, "ContentState": empty, "ContentData": data}
			header, err := frames.writeAggregateNode(legacyIssueCanonicalStruct{header: previous, fields: zeros}, 0)
			if err != nil {
				return metadataLogicalNode{}, err
			}
			headerValue, err = owner.node(ctx, header, false)
			if err != nil {
				return metadataLogicalNode{}, err
			}
		}
	}
	var revisions *metadataRetryDocumentRevisions
	if kind == reflect.TypeFor[domain.Document]() {
		revisions = &metadataRetryDocumentRevisions{store: s, frames: frames}
		defer revisions.Close()
	}
	raw, err := legacyIssueCanonicalHeader(ctx, owner, kind, part, func(field reflect.StructField, value legacySessionJSONPart) (string, bool, error) {
		switch field.Name {
		case "Content", "ContentState":
			prefix, err := legacyIssueCanonicalPrefix(ctx, value)
			if err != nil {
				return "", false, err
			}
			if prefix != '"' && prefix != 'n' {
				return "", false, nil
			}
			fields[field.Name], err = legacyProjectFrameString(ctx, frames, value.reader(), fields[field.Name])
			return `""`, true, err
		case "ContentData":
			prefix, err := legacyIssueCanonicalPrefix(ctx, value)
			if err != nil {
				return "", false, err
			}
			if prefix != '{' && prefix != 'n' {
				return "", false, nil
			}
			fields[field.Name], err = legacyIssueCanonicalContentData(ctx, s, frames, value, fields[field.Name])
			return "null", true, err
		case "Revisions":
			if revisions == nil {
				return "", false, nil
			}
			prefix, err := legacyIssueCanonicalPrefix(ctx, value)
			if err != nil {
				return "", false, err
			}
			if prefix != '[' && prefix != 'n' {
				return "", false, nil
			}
			return "null", true, revisions.decode(ctx, value, prefix)
		}
		return "", false, nil
	})
	if err != nil {
		return metadataLogicalNode{}, err
	}
	if history != nil {
		raw, err = history.appendAndReplay(ctx, owner, raw)
		if err != nil {
			return metadataLogicalNode{}, err
		}
	}
	work, err := resourceWindowTypedJSONWork(ctx, raw, kind)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	if err := owner.reserve(ctx, work); err != nil {
		return metadataLogicalNode{}, err
	}
	if err := json.Unmarshal(raw, headerValue.Addr().Interface()); err != nil {
		return metadataLogicalNode{}, err
	}
	if err := frames.auditJSONLocations(headerValue); err != nil {
		return metadataLogicalNode{}, err
	}
	header, err := frames.writeValue(headerValue, 0, false)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	if revisions != nil {
		value := reflect.Zero(revisions.nodeType())
		if revisions.present {
			value = reflect.MakeSlice(revisions.nodeType(), 0, 0)
		}
		revisions.shape, err = frames.writeValue(value, 0, false)
		if err != nil {
			return metadataLogicalNode{}, err
		}
		fields["Revisions"], err = frames.writeAggregateNode(revisions, 0)
		if err != nil {
			return metadataLogicalNode{}, err
		}
	}
	return frames.writeAggregateNode(legacyIssueCanonicalStruct{header: header, fields: fields}, 0)
}

// Keep backing slots after a shorter repeated array, as encoding/json does.
// Empty arrays and null discard those slots. Disk slots avoid a native slice
// of every decoded revision or retained per-revision body.
type metadataRetryDocumentRevisions struct {
	store   *SQLiteStore
	frames  *metadataLogicalBuilder
	stage   *sw26MigrationStage
	owner   *ResourceRead
	length  int
	present bool
	shape   metadataLogicalNode
	headers *RecordSpool
	next    int64
}

func (r *metadataRetryDocumentRevisions) Close() {
	if r.headers != nil {
		r.headers.Close()
		r.headers = nil
	}
	if r.stage != nil {
		r.stage.Close()
		r.stage = nil
	}
	if r.owner != nil {
		r.owner.Close()
		r.owner = nil
	}
}

func (r *metadataRetryDocumentRevisions) initialize(ctx context.Context) error {
	if r.stage != nil {
		return nil
	}
	r.owner = &ResourceRead{store: r.store}
	if err := r.owner.reserve(ctx, 512<<10); err != nil {
		return err
	}
	var err error
	r.stage, err = sw26NewMigrationStage(ctx)
	if err != nil {
		return err
	}
	if _, err := r.stage.exec(ctx, 4096, `CREATE TABLE revision_slots(ordinal INTEGER PRIMARY KEY,frame_offset INTEGER,frame_size INTEGER,frame_cost INTEGER,unavailable INTEGER,blocked INTEGER)`); err != nil {
		return err
	}
	_, err = r.stage.exec(ctx, 4096, `CREATE TABLE revision_headers(ordinal INTEGER,sequence INTEGER,header_offset INTEGER,header_size INTEGER,PRIMARY KEY(ordinal,sequence))`)
	return err
}

func (r *metadataRetryDocumentRevisions) reset(ctx context.Context) error {
	if _, err := r.stage.exec(ctx, 64, `DELETE FROM revision_slots`); err != nil {
		return err
	}
	if _, err := r.stage.exec(ctx, 64, `DELETE FROM revision_headers`); err != nil {
		return err
	}
	if r.headers != nil {
		r.headers.Close()
		r.headers = nil
	}
	r.next = 0
	return nil
}

func (r *metadataRetryDocumentRevisions) decode(ctx context.Context, part legacySessionJSONPart, prefix byte) error {
	if err := r.initialize(ctx); err != nil {
		return err
	}
	r.length, r.present = 0, prefix == '['
	if prefix == 'n' {
		scanner := legacySessionJSONScanner(ctx, part)
		if err := scanner.spaces(); err != nil {
			return err
		}
		if err := scanner.literal("null"); err != nil {
			return err
		}
		if err := scanner.end(); err != nil {
			return err
		}
		return r.reset(ctx)
	}
	scanner := legacySessionJSONScanner(ctx, part)
	if err := scanner.spaces(); err != nil {
		return err
	}
	err := scanner.array(func(value metadataCollectionRange) error {
		previous := metadataLogicalNode{source: r.frames.storage, typ: reflect.TypeFor[domain.DocumentRevision]()}
		err := r.stage.db.QueryRowContext(ctx, `SELECT frame_offset,frame_size,frame_cost,unavailable,blocked FROM revision_slots WHERE ordinal=?`, r.length).
			Scan(&previous.offset, &previous.size, &previous.cost, &previous.unavailable, &previous.blocked)
		if errors.Is(err, sql.ErrNoRows) {
			previous = metadataLogicalNode{}
		} else if err != nil {
			return err
		}
		node, err := metadataRetryDocumentStruct(ctx, r.store, r.frames, reflect.TypeFor[domain.DocumentRevision](),
			legacySessionJSONPart{part.input, value.offset, value.size}, previous, &metadataRetryRevisionHeaderReplay{revisions: r, ordinal: r.length})
		if err != nil {
			return err
		}
		if _, err := r.stage.exec(ctx, 256,
			`INSERT INTO revision_slots VALUES(?,?,?,?,?,?) ON CONFLICT(ordinal) DO UPDATE SET frame_offset=excluded.frame_offset,frame_size=excluded.frame_size,frame_cost=excluded.frame_cost,unavailable=excluded.unavailable,blocked=excluded.blocked`,
			r.length, node.offset, node.size, node.cost, node.unavailable, node.blocked); err != nil {
			return err
		}
		r.length++
		return nil
	}, 0)
	if err != nil {
		return err
	}
	if err := scanner.end(); err != nil {
		return err
	}
	if r.length == 0 {
		err = r.reset(ctx)
	}
	return err
}

// A typed frame preserves a slice's visible values, not hidden backing slots.
// Replay each slot's non-body JSON members in original order so the standard
// decoder preserves every nested header slice/struct/pointer across repeated
// revision arrays. Bodies stay in their separate immutable frames.
type metadataRetryRevisionHeaderReplay struct {
	revisions *metadataRetryDocumentRevisions
	ordinal   int
}

func (h *metadataRetryRevisionHeaderReplay) appendAndReplay(ctx context.Context, owner *ResourceRead, raw []byte) ([]byte, error) {
	r := h.revisions
	if !bytes.Equal(raw, []byte("null")) {
		if len(raw) < 2 || raw[0] != '{' || raw[len(raw)-1] != '}' {
			return nil, ErrMetadataRecordQuery
		}
		members := raw[1 : len(raw)-1]
		if len(members) != 0 {
			if r.headers == nil {
				var err error
				r.headers, err = newMetadataLogicalRecordSpool(ctx)
				if err != nil {
					return nil, err
				}
			}
			offset := r.headers.bytes
			if _, err := r.headers.Write(members); err != nil {
				return nil, err
			}
			if _, err := r.stage.exec(ctx, 256,
				`INSERT INTO revision_headers VALUES(?,?,?,?)`, h.ordinal, r.next, offset, len(members)); err != nil {
				return nil, err
			}
			r.next++
		}
	}
	output := &legacyProjectAdmittedBuffer{ctx: ctx, owner: owner}
	if _, err := output.Write([]byte("{")); err != nil {
		return nil, err
	}
	rows, err := r.stage.db.QueryContext(ctx, `SELECT header_offset,header_size FROM revision_headers WHERE ordinal=? ORDER BY sequence`, h.ordinal)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	first := true
	for rows.Next() {
		var offset, size int64
		if err := rows.Scan(&offset, &size); err != nil {
			return nil, err
		}
		if !first {
			if _, err := output.Write([]byte(",")); err != nil {
				return nil, err
			}
		}
		first = false
		if r.headers == nil {
			return nil, ErrMetadataRecordQuery
		}
		if _, err := io.CopyBuffer(output, io.NewSectionReader(r.headers, offset, size), make([]byte, metadataRecordChunkBytes)); err != nil {
			return nil, err
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if _, err := output.Write([]byte("}")); err != nil {
		return nil, err
	}
	return output.data.Bytes(), nil
}

func (r *metadataRetryDocumentRevisions) nodeType() reflect.Type {
	return reflect.TypeFor[[]domain.DocumentRevision]()
}
func (r *metadataRetryDocumentRevisions) logicalBase() metadataLogicalNode {
	return r.shape
}
func (r *metadataRetryDocumentRevisions) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, r, limit)
}
func (r *metadataRetryDocumentRevisions) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	if r.stage == nil || !r.present {
		return nil
	}
	rows, err := r.stage.db.QueryContext(ctx, `SELECT ordinal,frame_offset,frame_size,frame_cost,unavailable,blocked FROM revision_slots WHERE ordinal<? ORDER BY ordinal`, r.length)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var ordinal int
		node := metadataLogicalNode{source: r.frames.storage, typ: reflect.TypeFor[domain.DocumentRevision]()}
		if err := rows.Scan(&ordinal, &node.offset, &node.size, &node.cost, &node.unavailable, &node.blocked); err != nil {
			return err
		}
		if err := visit(reflect.ValueOf(ordinal), metadataAggregateStoredNode{value: node}); err != nil {
			return err
		}
	}
	return rows.Err()
}
