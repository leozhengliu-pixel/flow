package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"sync"
	"time"
)

type legacyInlineFieldsPatchKey struct{}

// Complete inline values stay in owned spools until the one root write ends.
// The native logical sources are borrowed only while preparing these parts.
type legacyInlineFieldsPatch struct {
	parts  map[string]legacySessionJSONPart
	spools []*RecordSpool
	owner  *ResourceRead
	once   sync.Once
}

func (p *legacyInlineFieldsPatch) Close() {
	if p == nil {
		return
	}
	p.once.Do(func() {
		p.parts = nil
		for _, spool := range p.spools {
			spool.Close()
		}
		p.spools = nil
		if p.owner != nil {
			p.owner.Close()
		}
	})
}

func legacyInlineFieldsPatchFrom(ctx context.Context) *legacyInlineFieldsPatch {
	patch, _ := ctx.Value(legacyInlineFieldsPatchKey{}).(*legacyInlineFieldsPatch)
	return patch
}

var errLegacyInlineFieldsShape = errors.New("inline metadata array shape determined")

// inlineFieldReady accepts only an already-inline collection with no canonical
// rows, whose target also remains inline under splitWorkspaceMetadata. A shape
// transition or any canonical row keeps the existing full-field algorithm.
func (p *legacyMetadataWritePlan) inlineFieldReady(ctx context.Context, field *legacyMetadataWriteField, shapes map[string]string) (bool, error) {
	if field == nil || field.next.typ == nil || field.next.typ.Kind() != reflect.Slice ||
		field.next.typ.Elem().Kind() != reflect.Struct || shapes[field.field.name] != "" {
		return false, nil
	}
	id, found := field.next.typ.Elem().FieldByName("ID")
	if !found || id.Type.Kind() != reflect.String || id.PkgPath != "" || len(id.Index) != 1 {
		return false, nil
	}
	// Supported domain producers expose ID as id. An ignored or differently
	// named field must continue through the literal splitter.
	tag := id.Tag.Get("json")
	if tag != "" && tag != "id" && tag != "id,omitempty" {
		return false, nil
	}
	backed, err := metadataFieldRecordBacked(ctx, p.tx, p.workspace, field.field.name, field.next.typ)
	if err != nil || backed {
		return false, err
	}
	var rows int
	if err := p.tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM workspace_metadata_records WHERE workspace_key=? AND field=?", p.workspace, field.field.name).Scan(&rows); err != nil {
		return false, err
	}
	if rows != 0 {
		return false, nil
	}
	return p.inlineTargetShape(ctx, field.next)
}

func (p *legacyMetadataWritePlan) inlineTargetShape(ctx context.Context, target metadataLogicalNode) (bool, error) {
	if absent, err := target.isNil(ctx); err != nil || absent {
		return false, err
	}
	owner := &ResourceRead{store: p.store}
	defer owner.Close()
	if err := owner.reserve(ctx, 64<<10); err != nil {
		return false, err
	}
	seen := map[string]bool{}
	inline := false
	err := target.Children(ctx, func(_ reflect.Value, item metadataLogicalNode) error {
		id, err := issueHotField(ctx, item, "ID")
		if err != nil {
			return err
		}
		// JSON's invalid-UTF8 replacement cannot shorten a native string.
		// Read at most 192 bytes; every longer ID is already known inline.
		var small [192]byte
		var count int
		err = id.StreamBytes(ctx, func(input io.Reader) error {
			n, err := io.ReadFull(input, small[:])
			count = n
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			}
			return err
		})
		if err != nil {
			return err
		}
		if count == len(small) {
			inline = true
			return errLegacyInlineFieldsShape
		}
		// This is deliberately the stdlib scalar round trip used by the
		// literal splitter, including replacement collisions and byte width.
		encoded, err := json.Marshal(string(small[:count]))
		if err != nil {
			return err
		}
		var normalized string
		if err := json.Unmarshal(encoded, &normalized); err != nil {
			return err
		}
		if normalized == "" || len(normalized) > 191 || seen[normalized] {
			inline = true
			return errLegacyInlineFieldsShape
		}
		if err := owner.reserve(ctx, uint64(len(normalized))*8+1024); err != nil {
			return err
		}
		seen[normalized] = true
		return nil
	})
	if errors.Is(err, errLegacyInlineFieldsShape) {
		err = nil
	}
	return inline, err
}

func (p *legacyMetadataWritePlan) prepareInlineFields(ctx context.Context, fields []*legacyMetadataWriteField) (_ *legacyInlineFieldsPatch, err error) {
	patch := &legacyInlineFieldsPatch{
		parts: map[string]legacySessionJSONPart{},
		owner: &ResourceRead{store: p.store},
	}
	defer func() {
		if err != nil {
			patch.Close()
		}
	}()
	for _, field := range fields {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if field == nil || field.field.name == "" || field.next.typ == nil {
			return nil, ErrMetadataRecordQuery
		}
		if _, duplicate := patch.parts[field.field.name]; duplicate {
			return nil, ErrMetadataRecordQuery
		}
		if err := patch.owner.reserve(ctx, uint64(len(field.field.name))*8+1024); err != nil {
			return nil, err
		}
		spool, err := newMetadataLogicalRecordSpool(ctx)
		if err != nil {
			return nil, err
		}
		patch.spools = append(patch.spools, spool)
		// The proven stream's parser/sort scratch belongs to this one field,
		// rather than accumulating across the complete patch.
		err = func() error {
			scratch := &ResourceRead{store: p.store}
			defer scratch.Close()
			if err := scratch.reserve(ctx, logicalJSONStreamWork); err != nil {
				return err
			}
			output := &metadataInlineRootSpoolWriter{spool: spool, limit: int64(p.store.maxStateBytes)}
			return legacyProjectStreamNode(ctx, scratch, field.next, output)
		}()
		if err != nil {
			return nil, err
		}
		patch.parts[field.field.name] = legacySessionSpoolPart(spool)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return patch, nil
}

// Inline field parts, ordinary slow fields and an optional session patch share
// one original root UPDATE and its exact metadataSQLWriteStage byte bill.
func (s *SQLiteStore) writeLegacyInlineFieldsRootPatch(ctx context.Context, tx *sqlTx, workspace, workspaceID string, root *metadataInlineRoot, ordinary map[string]json.RawMessage, owner *ResourceRead, touch bool, inline *legacyInlineFieldsPatch) error {
	if !touch || inline == nil || len(inline.parts) == 0 {
		return ErrBoundedMetadataCoverage
	}
	fields, err := legacySessionJSONObject(ctx, owner, legacySessionJSONPart{root.source, 0, root.source.size})
	if err != nil {
		return err
	}
	for name, raw := range ordinary {
		if raw == nil {
			delete(fields, name)
		} else {
			fields[name] = legacySessionJSONPart{bytes.NewReader(raw), 0, int64(len(raw))}
		}
	}
	var shapes map[string]string
	if manifest, found := ordinary[metadataCollectionsKey]; found {
		if err := json.Unmarshal(manifest, &shapes); err != nil {
			return err
		}
	} else {
		shapes, err = root.shapes()
		if err != nil {
			return err
		}
	}
	if shapes == nil {
		shapes = map[string]string{}
	}
	for name, part := range inline.parts {
		if part.input == nil {
			return ErrBoundedMetadataCoverage
		}
		fields[name] = part
		delete(shapes, name)
	}
	if session := legacySessionRootPatchFrom(ctx); session != nil {
		if session.part.input == nil {
			return ErrBoundedMetadataCoverage
		}
		if _, duplicate := inline.parts["agentSessions"]; duplicate {
			return ErrBoundedMetadataCoverage
		}
		if session.split {
			shapes["agentSessions"] = "array"
		} else {
			delete(shapes, "agentSessions")
		}
		fields["agentSessions"] = session.part
	}
	manifest, err := json.Marshal(shapes)
	if err != nil {
		return err
	}
	fields[metadataCollectionsKey] = legacySessionJSONPart{bytes.NewReader(manifest), 0, int64(len(manifest))}
	replacement, err := newMetadataLogicalRecordSpool(ctx)
	if err != nil {
		return err
	}
	defer replacement.Close()
	output := &metadataInlineRootSpoolWriter{spool: replacement, limit: int64(s.maxStateBytes)}
	if err := legacySessionWriteJSONObject(ctx, owner, fields, output); err != nil {
		return err
	}
	if same, err := legacySessionRawEqual(ctx, legacySessionJSONPart{root.source, 0, root.source.size}, legacySessionSpoolPart(replacement)); err != nil || same {
		return err
	}
	stage, err := beginMetadataSQLWriteStage(ctx, tx, metadataSQLStageTarget{
		workspaceState: true, workspace: workspace, workspaceID: workspaceID,
		updatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		dataBytes: int(replacement.bytes), searchBytes: -1,
	})
	if err != nil {
		return err
	}
	defer stage.Close(context.WithoutCancel(ctx))
	writer, err := stage.writer(ctx, "data")
	if err != nil {
		return err
	}
	if _, err := io.CopyBuffer(writer, legacySessionSpoolPart(replacement).reader(), make([]byte, metadataRecordChunkBytes)); err != nil {
		return err
	}
	if err := writer.flush(true); err != nil {
		return err
	}
	if err := stage.writeWorkspaceState(ctx); err != nil {
		return err
	}
	return stage.Close(ctx)
}
