package store

import (
	"bytes"
	"context"
	"io"
)

func legacySessionRawEqual(ctx context.Context, left, right legacySessionJSONPart) (bool, error) {
	if left.size != right.size {
		return false, nil
	}
	var buffers [2][metadataRecordChunkBytes]byte
	for offset := int64(0); offset < left.size; offset += metadataRecordChunkBytes {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		size := min(int64(metadataRecordChunkBytes), left.size-offset)
		for index, part := range []legacySessionJSONPart{left, right} {
			if _, err := part.input.ReadAt(buffers[index][:size], part.start+offset); err != nil {
				return false, err
			}
		}
		if !bytes.Equal(buffers[0][:size], buffers[1][:size]) {
			return false, nil
		}
	}
	return true, ctx.Err()
}

func (p *legacyMetadataWritePlan) writeAgentSessionLifecycleRow(ctx context.Context, field *legacyMetadataWriteField, row *legacyMetadataWriteRow, order int) (bool, error) {
	return p.writeAgentSessionLifecycleNativeRow(ctx, field, row, order, false)
}

func (p *legacyMetadataWritePlan) writeAgentSessionLifecycleNativeRow(ctx context.Context, field *legacyMetadataWriteField, row *legacyMetadataWriteRow, order int, force bool) (bool, error) {
	if !legacySessionLifecycleWrite(ctx) || field.field.name != "agentSessions" || row.before.typ == nil || row.next.typ == nil {
		return false, nil
	}
	closed, err := legacySessionLifecycleOnly(ctx, row.before, row.next)
	if err != nil || !closed {
		return false, err
	}
	if !force && row.before.cost <= 64<<10 && row.next.cost <= 64<<10 {
		return false, nil
	}
	owner := &ResourceRead{store: p.store}
	defer owner.Close()
	if err := owner.reserve(ctx, 2<<20); err != nil {
		return true, err
	}
	before, err := legacySessionNodeSpool(ctx, owner, row.before)
	if err != nil {
		return true, err
	}
	defer before.Close()
	next, err := legacySessionNodeSpool(ctx, owner, row.next)
	if err != nil {
		return true, err
	}
	defer next.Close()
	beforeFields, err := legacySessionJSONObject(ctx, owner, legacySessionSpoolPart(before))
	if err != nil {
		return true, err
	}
	nextFields, err := legacySessionJSONObject(ctx, owner, legacySessionSpoolPart(next))
	if err != nil {
		return true, err
	}
	// Session records have no display User objects. This is exactly the
	// canonicalDisplayData equality used by the ordinary row writer.
	if !force {
		if same, err := legacySessionObjectsEqual(ctx, owner, beforeFields, nextFields); err != nil || same {
			return true, err
		}
	}
	stripped, err := newMetadataLogicalRecordSpool(ctx)
	if err != nil {
		return true, err
	}
	defer stripped.Close()
	if _, err := next.Seek(0, io.SeekStart); err != nil {
		return true, err
	}
	if err := legacyProjectCopyStoredJSON(ctx, stripped, next); err != nil {
		return true, err
	}
	stored, err := newMetadataLogicalRecordSpool(ctx)
	if err != nil {
		return true, err
	}
	defer stored.Close()
	if err := legacySessionStoredForm(ctx, owner, p.tx, p.workspace, legacySessionSpoolPart(stripped), stored, true); err != nil {
		return true, err
	}
	stage, err := beginMetadataSQLWriteStage(ctx, p.tx, metadataSQLStageTarget{
		workspace: p.workspace, field: "agentSessions", key: row.id, order: order,
		dataBytes: int(stored.bytes), searchBytes: -1,
	})
	if err != nil {
		return true, err
	}
	defer stage.Close(context.WithoutCancel(ctx))
	writer, err := stage.growingWriter(ctx, "data", p.store)
	if err != nil {
		return true, err
	}
	if _, err := io.CopyBuffer(writer, legacySessionSpoolPart(stored).reader(), make([]byte, metadataRecordChunkBytes)); err != nil {
		return true, err
	}
	if err := writer.flush(true); err != nil {
		return true, err
	}
	if err := stage.writeMetadataRow(ctx); err != nil {
		return true, err
	}
	// These are currently no-ops for agentSessions. SW198 pins that closed
	// hook contract; future session side indexes must extend this writer.
	if err := writeCustomerFilterRecord(ctx, p.tx, p.workspace, "agentSessions", row.id, nil); err != nil {
		return true, err
	}
	if err := writeReleaseIssueRecords(ctx, p.tx, p.workspace, "agentSessions", row.id, nil); err != nil {
		return true, err
	}
	if err := syncMetadataSearchDocument(ctx, p.tx, p.workspace, "agentSessions", row.id, nil); err != nil {
		return true, err
	}
	return true, stage.Close(ctx)
}

// Inline session collections have no row rebase: the literal writer retains
// every native occurrence and replaces that field in the locked root. Merely
// finding no selected row must never be interpreted as a concurrent deletion.
func (p *legacyMetadataWritePlan) agentSessionLifecycleInlineReady(ctx context.Context, field *legacyMetadataWriteField, _ map[string]string) (bool, error) {
	if !legacySessionLifecycleWrite(ctx) || field.field.name != "agentSessions" {
		return false, nil
	}
	if !field.full {
		// The ordinary slow tail materializes these rebased collections.
		// Frame them instead before marking the field handled by this tail.
		before, err := p.frames.writeAggregateNode(legacyMetadataWriteCollection{field: field}, 0)
		if err != nil {
			return false, err
		}
		next, err := p.frames.writeAggregateNode(legacyMetadataWriteCollection{field: field, next: true}, 0)
		if err != nil {
			return false, err
		}
		field.before, field.next, field.full = before, next, true
	}
	return true, nil
}

func (p *legacyMetadataWritePlan) prepareAgentSessionLifecycleRoot(ctx context.Context, field *legacyMetadataWriteField) (*legacySessionRootPatch, error) {
	owner := &ResourceRead{store: p.store}
	defer owner.Close()
	if err := owner.reserve(ctx, 2<<20); err != nil {
		return nil, err
	}
	beforeSplit, err := legacySessionSplit(ctx, owner, field.before)
	if err != nil {
		return nil, err
	}
	nextSplit, err := legacySessionSplit(ctx, owner, field.next)
	if err != nil {
		return nil, err
	}
	// Preserve the slow tail's complete-field sync, including its known-row
	// deletion and missing-known-row rules, before updating the root shape.
	if err := p.syncAgentSessionLifecycleCollection(ctx, owner, field, beforeSplit, nextSplit); err != nil {
		return nil, err
	}
	if nextSplit.valid {
		return &legacySessionRootPatch{
			split: true, part: legacySessionJSONPart{bytes.NewReader([]byte("[]")), 0, 2},
		}, nil
	}
	// Inline arrays never pass through offloadRecord. Keep the requested
	// native field as a streamed root value, including all duplicate entries.
	next, err := legacySessionNodeSpool(ctx, owner, field.next)
	if err != nil {
		return nil, err
	}
	return &legacySessionRootPatch{part: legacySessionSpoolPart(next), spool: next}, nil
}
