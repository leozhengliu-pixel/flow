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
	part  legacySessionJSONPart
	split bool
	spool *RecordSpool
}

func (p *legacySessionRootPatch) Close() {
	if p != nil && p.spool != nil {
		p.spool.Close()
		p.spool = nil
	}
}

func legacySessionRootPatchFrom(ctx context.Context) *legacySessionRootPatch {
	if !legacySessionLifecycleWrite(ctx) {
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
	if session.split {
		shapes["agentSessions"] = "array"
	} else {
		delete(shapes, "agentSessions")
	}
	manifest, err := json.Marshal(shapes)
	if err != nil {
		return err
	}
	fields[metadataCollectionsKey] = legacySessionJSONPart{bytes.NewReader(manifest), 0, int64(len(manifest))}
	fields["agentSessions"] = session.part
	replacement, err := newMetadataLogicalRecordSpool(ctx)
	if err != nil {
		return err
	}
	defer replacement.Close()
	written := &metadataInlineRootSpoolWriter{spool: replacement, limit: int64(s.maxStateBytes)}
	if err := legacySessionWriteJSONObject(ctx, owner, fields, written); err != nil {
		return err
	}
	if same, err := legacySessionRawEqual(ctx, legacySessionJSONPart{root.source, 0, root.source.size}, legacySessionSpoolPart(replacement)); err != nil || same {
		return err
	}
	updatedAt := time.Now().UTC().Format(time.RFC3339Nano)
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
