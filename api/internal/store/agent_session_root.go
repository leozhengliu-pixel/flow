package store

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"time"
)

type legacySessionRootPatchKey struct{}

type legacySessionRootPatch struct {
	field     string
	part      legacySessionJSONPart
	split     bool
	spool     *RecordSpool
	next      *legacySessionRootPatch
	canonical bool
}

func (p *legacySessionRootPatch) Close() {
	for p != nil {
		next := p.next
		p.next = nil
		if p.spool != nil {
			p.spool.Close()
			p.spool = nil
		}
		p = next
	}
}

func (p *legacySessionRootPatch) visit(visit func(*legacySessionRootPatch) error) error {
	seen := map[*legacySessionRootPatch]bool{}
	for p != nil {
		if seen[p] {
			return ErrBoundedMetadataCoverage
		}
		seen[p] = true
		if err := visit(p); err != nil {
			return err
		}
		p = p.next
	}
	return nil
}

func legacySessionRootPatchFrom(ctx context.Context) *legacySessionRootPatch {
	event := integrationDeliveryStreamEvent(ctx)
	if !legacySessionLifecycleWrite(ctx) && !workflowRunStreamSelected(ctx) &&
		event != "integration_delivery.claimed" && event != "integration_delivery.retry" {
		return nil
	}
	patch, _ := ctx.Value(legacySessionRootPatchKey{}).(*legacySessionRootPatch)
	return patch
}

// Fold the session field into the ordinary slow-root patch. Actor/Viewer's
// other slow fields and the session array publish in ONE original root UPDATE,
// preserving its transaction-byte bill and clock/revision behavior.
func (s *SQLiteStore) writeAgentSessionLifecycleRootPatch(ctx context.Context, tx *sqlTx, workspace, workspaceID string, root *metadataInlineRoot, ordinary map[string]json.RawMessage, owner *ResourceRead, touch bool, session *legacySessionRootPatch) error {
	if !touch || session == nil || session.part.input == nil {
		return ErrBoundedMetadataCoverage
	}
	var fields map[string]legacySessionJSONPart
	var err error
	if integrationDeliveryStreamEvent(ctx) != "" {
		fields, err = root.legacySessionFields(ctx, owner)
	} else {
		fields, err = legacySessionJSONObject(ctx, owner, legacySessionJSONPart{root.source, 0, root.source.size})
	}
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
	seen := map[string]bool{}
	canonical := map[string]legacySessionJSONPart{}
	if err := session.visit(func(part *legacySessionRootPatch) error {
		kind := part.field
		if kind == "" {
			kind = "agentSessions"
		}
		if part.part.input == nil || seen[kind] || !legacyMetadataLifecycleStreamSelected(ctx, kind) {
			return ErrBoundedMetadataCoverage
		}
		seen[kind] = true
		if part.split {
			shapes[kind] = "array"
		} else {
			delete(shapes, kind)
		}
		fields[kind] = part.part
		if part.canonical {
			canonical[kind] = part.part
		}
		return nil
	}); err != nil {
		return err
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
	// As the delta writer's root, NUL is dropped (PostgreSQL refuses \u0000).
	written := &jsonNULStripWriter{dst: &metadataInlineRootSpoolWriter{spool: replacement, limit: int64(s.maxStateBytes)}}
	if err := legacySessionWriteJSONObjectWithCanonical(ctx, owner, fields, canonical, written); err != nil {
		return err
	}
	if err := written.Finish(); err != nil {
		return err
	}
	if same, err := legacySessionRawEqual(ctx, legacySessionJSONPart{root.source, 0, root.source.size}, legacySessionSpoolPart(replacement)); err != nil || same {
		return err
	}
	updatedAt := time.Now().UTC().Format(time.RFC3339Nano)
	if integrationDeliveryStreamEvent(ctx) != "" {
		if raw, binary, err := integrationDeliveryBinarySpool(ctx, owner, replacement); err != nil {
			return err
		} else if binary {
			tx.metadataWritten = true
			_, err := tx.ExecContext(ctx, metadataSQLStageLegacyRootSQL, workspaceID, raw, updatedAt, workspace)
			return err
		}
	}
	stage, err := beginMetadataSQLWriteStage(ctx, tx, metadataSQLStageTarget{
		workspaceState: true, workspace: workspace, workspaceID: workspaceID,
		updatedAt: updatedAt, dataBytes: int(replacement.bytes), searchBytes: -1,
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
