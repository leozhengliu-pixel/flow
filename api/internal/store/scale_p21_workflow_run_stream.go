package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"

	"flow/api/internal/domain"
)

type workflowRunStreamKey struct{}

func workflowRunStreamSelected(ctx context.Context) bool {
	selected, _ := ctx.Value(workflowRunStreamKey{}).(bool)
	return selected
}

func legacyMetadataLifecycleStreamSelected(ctx context.Context, field string) bool {
	if field == "agentSessions" {
		return legacySessionLifecycleWrite(ctx)
	}
	if integrationDeliveryStreamType(field) != nil {
		event := integrationDeliveryStreamEvent(ctx)
		return event == "integration_delivery.claimed" || event == "integration_delivery.retry"
	}
	return field == "workflowRuns" && workflowRunStreamSelected(ctx)
}

func workflowRetryPatchOnly(ctx context.Context, before, next metadataLogicalNode) (bool, error) {
	if before.typ != reflect.TypeFor[domain.WorkflowRun]() || next.typ != before.typ {
		return false, nil
	}
	retry, err := issueHotField(ctx, next, "NextRetryAt")
	if err != nil {
		return false, err
	}
	cleared, err := retry.isNil(ctx)
	if err != nil || !cleared {
		return false, err
	}
	closed := true
	err = before.Children(ctx, func(key reflect.Value, left metadataLogicalNode) error {
		if key.String() == "NextRetryAt" {
			return nil
		}
		right, err := issueHotField(ctx, next, key.String())
		if err != nil {
			return err
		}
		same, err := legacyMetadataWriteEqual(ctx, left, right, 0)
		closed = closed && same
		return err
	})
	return closed, err
}

// Output keys live in the existing bounded disk stage. Only one admitted key
// crosses SQL at a time; its decoded string values remain typed disk frames.
type workflowOutputFrames struct {
	stage  *sw26MigrationStage
	frames *metadataLogicalBuilder
	shape  metadataLogicalNode
	owner  *ResourceRead
}

func (n workflowOutputFrames) nodeType() reflect.Type           { return n.shape.typ }
func (n workflowOutputFrames) logicalBase() metadataLogicalNode { return n.shape }
func (n workflowOutputFrames) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n workflowOutputFrames) logicalMapEntries(ctx context.Context, visit func(metadataAggregateNode, metadataAggregateNode) error) error {
	rows, err := n.stage.db.QueryContext(ctx, `SELECT key_offset,key_size,key_cost,value_offset,value_size,value_cost FROM workflow_output ORDER BY key`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		key := metadataLogicalNode{source: n.frames.storage, typ: reflect.TypeFor[string]()}
		value := key
		if err := rows.Scan(&key.offset, &key.size, &key.cost, &value.offset, &value.size, &value.cost); err != nil {
			return err
		}
		if err := visit(metadataAggregateStoredNode{key}, metadataAggregateStoredNode{value}); err != nil {
			return err
		}
	}
	return rows.Err()
}
func (n workflowOutputFrames) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	return n.logicalMapEntries(ctx, func(key, value metadataAggregateNode) error {
		work := &ResourceRead{store: n.owner.store}
		defer work.Close()
		node := key.(metadataAggregateStoredNode).value
		decoded, err := work.node(ctx, node, false)
		if err != nil {
			return err
		}
		return visit(decoded, value)
	})
}

func workflowFrameOutput(ctx context.Context, owner *ResourceRead, frames *metadataLogicalBuilder, stage *sw26MigrationStage, part legacySessionJSONPart, previousNil bool) (metadataLogicalNode, bool, error) {
	return workflowFrameStringMap(ctx, owner, frames, stage, part, previousNil, "WorkflowRun", "output")
}

func workflowFrameStringMap(ctx context.Context, owner *ResourceRead, frames *metadataLogicalBuilder, stage *sw26MigrationStage, part legacySessionJSONPart, previousNil bool, structName, fieldName string) (metadataLogicalNode, bool, error) {
	scanner := legacySessionJSONScanner(ctx, part)
	if err := scanner.spaces(); err != nil {
		return metadataLogicalNode{}, false, err
	}
	prefix, err := scanner.peek()
	if err != nil {
		return metadataLogicalNode{}, false, err
	}
	if prefix == 'n' {
		if err := scanner.literal("null"); err != nil {
			return metadataLogicalNode{}, false, err
		}
		if err := scanner.end(); err != nil {
			return metadataLogicalNode{}, false, err
		}
		if _, err := stage.exec(ctx, 0, `DELETE FROM workflow_output`); err != nil {
			return metadataLogicalNode{}, false, err
		}
		node, err := frames.writeValue(reflect.ValueOf(map[string]string(nil)), 0, false)
		return node, true, err
	}
	if prefix != '{' {
		return metadataLogicalNode{}, false, &json.UnmarshalTypeError{
			Value: workflowJSONValueKind(prefix), Type: reflect.TypeFor[map[string]string](),
			Struct: structName, Field: fieldName,
		}
	}
	if previousNil {
		if _, err := stage.exec(ctx, 0, `DELETE FROM workflow_output`); err != nil {
			return metadataLogicalNode{}, false, err
		}
	}
	empty, err := frames.writeValue(reflect.ValueOf(""), 0, false)
	if err != nil {
		return metadataLogicalNode{}, false, err
	}
	err = scanner.object(func(key, value metadataCollectionRange) error {
		work := &ResourceRead{store: owner.store}
		defer work.Close()
		if err := work.reserve(ctx, logicalJSONStreamWork); err != nil {
			return err
		}
		keyNode, err := legacyProjectFrameString(ctx, frames, io.NewSectionReader(part.input, key.offset, key.size), empty)
		if err != nil {
			return err
		}
		decoded, err := work.node(ctx, keyNode, false)
		if err != nil {
			return err
		}
		var first [1]byte
		if _, err := part.input.ReadAt(first[:], value.offset); err != nil {
			return err
		}
		if first[0] != '"' && first[0] != 'n' {
			return &json.UnmarshalTypeError{Value: workflowJSONValueKind(first[0]), Type: reflect.TypeFor[string](), Struct: structName, Field: fieldName}
		}
		// Each map element starts at its zero value, including a null that
		// overwrites a previous entry. Repeated Output objects merge keys.
		valueNode, err := legacyProjectFrameString(ctx, frames, io.NewSectionReader(part.input, value.offset, value.size), empty)
		if err != nil {
			return err
		}
		if err := work.reserve(ctx, keyNode.cost*8+1024); err != nil {
			return err
		}
		_, err = stage.exec(ctx, int64(len(decoded.String())+256),
			`INSERT INTO workflow_output VALUES(?,?,?,?,?,?,?) ON CONFLICT(key) DO UPDATE SET key_offset=excluded.key_offset,key_size=excluded.key_size,key_cost=excluded.key_cost,value_offset=excluded.value_offset,value_size=excluded.value_size,value_cost=excluded.value_cost`,
			[]byte(decoded.String()), keyNode.offset, keyNode.size, keyNode.cost, valueNode.offset, valueNode.size, valueNode.cost)
		return err
	}, 0)
	if err != nil {
		return metadataLogicalNode{}, false, err
	}
	if err := scanner.end(); err != nil {
		return metadataLogicalNode{}, false, err
	}
	shape, err := frames.writeValue(reflect.ValueOf(map[string]string{}), 0, false)
	if err != nil {
		return metadataLogicalNode{}, false, err
	}
	node, err := frames.writeAggregateNode(workflowOutputFrames{stage, frames, shape, owner}, 0)
	return node, false, err
}

func workflowJSONValueKind(prefix byte) string {
	switch prefix {
	case '{':
		return "object"
	case '[':
		return "array"
	case '"':
		return "string"
	case 't', 'f':
		return "bool"
	default:
		return "number"
	}
}

type workflowCanonicalNode struct {
	header, errorText, output metadataLogicalNode
}

func (n workflowCanonicalNode) nodeType() reflect.Type { return n.header.typ }
func (n workflowCanonicalNode) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n workflowCanonicalNode) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	return n.header.Children(ctx, func(key reflect.Value, value metadataLogicalNode) error {
		switch key.String() {
		case "Error":
			value = n.errorText
		case "Output":
			value = n.output
		}
		return visit(key, metadataAggregateStoredNode{value})
	})
}

// Stream the two unbounded fields through the existing string frame codec.
// All remaining fields use the original typed JSON decoder, including times.
func workflowRunCanonicalRecord(ctx context.Context, store *SQLiteStore, frames *metadataLogicalBuilder, part legacySessionJSONPart) (metadataLogicalNode, error) {
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
	errorText, err := frames.writeValue(reflect.ValueOf(""), 0, false)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	output, err := frames.writeValue(reflect.ValueOf(map[string]string(nil)), 0, false)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	outputNil := true
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
		return frames.writeValue(reflect.ValueOf(domain.WorkflowRun{}), 0, false)
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
		field, known := resourceWindowJSONField(reflect.TypeFor[domain.WorkflowRun](), name)
		if known && (field.Name == "Error" || field.Name == "Output") {
			var prefix [1]byte
			if _, err := part.input.ReadAt(prefix[:], value.offset); err != nil {
				return err
			}
			input := legacySessionJSONPart{part.input, value.offset, value.size}
			if field.Name == "Error" && (prefix[0] == '"' || prefix[0] == 'n') {
				errorText, err = legacyProjectFrameString(ctx, frames, input.reader(), errorText)
				if err == nil {
					_, err = header.Write([]byte(`""`))
				}
				return err
			}
			if field.Name == "Output" {
				output, outputNil, err = workflowFrameOutput(ctx, owner, frames, stage, input, outputNil)
				if err == nil {
					_, err = header.Write([]byte("null"))
				}
				return err
			}
		}
		_, err = io.CopyBuffer(header, io.NewSectionReader(part.input, value.offset, value.size), make([]byte, metadataRecordChunkBytes))
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
	work, err := resourceWindowTypedJSONWork(ctx, header.data.Bytes(), reflect.TypeFor[domain.WorkflowRun]())
	if err != nil {
		return metadataLogicalNode{}, err
	}
	if err := owner.reserve(ctx, work); err != nil {
		return metadataLogicalNode{}, err
	}
	var run domain.WorkflowRun
	if err := json.Unmarshal(header.data.Bytes(), &run); err != nil {
		return metadataLogicalNode{}, err
	}
	if err := frames.auditJSONLocations(reflect.ValueOf(run)); err != nil {
		return metadataLogicalNode{}, err
	}
	node, err := frames.writeValue(reflect.ValueOf(run), 0, false)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	return frames.writeAggregateNode(workflowCanonicalNode{node, errorText, output}, 0)
}

func (p *legacyMetadataWritePlan) rebaseWorkflowRunStreamRow(ctx context.Context, field *legacyMetadataWriteField, index int) (handled bool, err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("workflow streamed row rebase: %w", err)
		}
	}()
	if !workflowRunStreamSelected(ctx) || field.field.name != "workflowRuns" {
		return false, nil
	}
	row := &field.rows[index]
	closed, err := workflowRetryPatchOnly(ctx, row.before, row.next)
	if err != nil || !closed {
		return false, err
	}
	previousRetry, err := issueHotField(ctx, row.before, "NextRetryAt")
	if err != nil {
		return false, err
	}
	alreadyNil, err := previousRetry.isNil(ctx)
	if err != nil || alreadyNil {
		return false, err
	}
	key := metadataRecordKey{"workflowRuns", row.id}
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
	var size, order int64
	err = p.tx.QueryRowContext(ctx, "SELECT "+owner.byteLength("data")+",collection_order FROM workspace_metadata_records WHERE workspace_key=? AND field='workflowRuns' AND record_key=?"+lock, p.workspace, row.id).Scan(&size, &order)
	if errors.Is(err, sql.ErrNoRows) {
		backed, err := metadataFieldRecordBacked(ctx, p.tx, p.workspace, "workflowRuns", field.before.typ)
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
	if err != nil {
		return true, err
	}
	if p.tx.dialect == "sqlite" && p.store.db.journalReady.Load() {
		p.tx.journalOrder = recordJournalOrder{p.workspace, "workflowRuns", row.id, order, true}
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
	reader := &canonicalMetadataReader{ctx: ctx, tx: p.tx, workspace: p.workspace, key: RecordKey{"workflowRuns", row.id}, size: size}
	if n, err := io.CopyBuffer(stored, reader, make([]byte, metadataRecordChunkBytes)); err != nil || n != size {
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
	if p.tx.rebasedMetadata == nil {
		p.tx.rebasedMetadata = map[metadataRecordKey]bool{}
	}
	p.tx.rebasedMetadata[key] = true
	if same, err := legacySessionObjectsEqual(ctx, owner, rawFields, baseFields); err != nil || same {
		return true, err
	}
	decoded, err := workflowRunCanonicalRecord(ctx, p.store, p.frames, legacySessionSpoolPart(stored))
	if err != nil {
		return true, err
	}
	theirs, err := legacySessionNodeSpool(ctx, owner, decoded)
	if err != nil {
		return true, err
	}
	defer theirs.Close()
	theirFields, err := legacySessionJSONObject(ctx, owner, legacySessionSpoolPart(theirs))
	if err != nil {
		return true, err
	}
	if same, err := legacySessionObjectsEqual(ctx, owner, theirFields, baseFields); err != nil || same {
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
	// Only the retry scalar can differ in our closed native patch. Feed its
	// exact encoded members into the existing recursive merge implementation.
	var retry [3]json.RawMessage
	for index, fields := range []map[string]legacySessionJSONPart{baseFields, ourFields, theirFields} {
		if value := fields["nextRetryAt"]; value.input != nil {
			retry[index], err = legacySessionJSONBytes(ctx, owner, value)
			if err != nil {
				return true, err
			}
		}
	}
	value, ok := mergeMetadataMember("nextRetryAt", retry[0], retry[1], retry[2])
	if !ok {
		return true, &metadataConflictError{keys: []metadataRecordKey{key}}
	}
	// The requested clear stays a clear (including when the peer cleared it).
	// Any nonempty value here would escape this purpose-closed profile.
	if !emptyMember(value) {
		return true, ErrBoundedMetadataCoverage
	}
	next, err := p.frames.writeAggregateNode(legacyDiscussionNativeDelta{
		base: decoded, changed: map[string]reflect.Value{"NextRetryAt": reflect.ValueOf(domain.WorkflowRun{}).FieldByName("NextRetryAt")},
	}, 0)
	if err != nil {
		return true, err
	}
	row.before, row.next = decoded, next
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

func (p *legacyMetadataWritePlan) writeWorkflowRunStreamRow(ctx context.Context, field *legacyMetadataWriteField, row *legacyMetadataWriteRow, order int) (bool, error) {
	return p.writeWorkflowRunStreamNativeRow(ctx, field, row, order, false)
}

func (p *legacyMetadataWritePlan) writeWorkflowRunStreamNativeRow(ctx context.Context, field *legacyMetadataWriteField, row *legacyMetadataWriteRow, order int, force bool) (handled bool, err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("workflow streamed row write: %w", err)
		}
	}()
	if !workflowRunStreamSelected(ctx) || field.field.name != "workflowRuns" || row.next.typ != reflect.TypeFor[domain.WorkflowRun]() {
		return false, nil
	}
	if !force && row.before.typ != nil {
		closed, err := workflowRetryPatchOnly(ctx, row.before, row.next)
		if err != nil || !closed {
			return false, err
		}
	}
	if !force && row.next.cost <= 64<<10 && (row.before.typ == nil || row.before.cost <= 64<<10) {
		return false, nil
	}
	owner := &ResourceRead{store: p.store}
	defer owner.Close()
	if err := owner.reserve(ctx, logicalJSONStreamWork); err != nil {
		return true, err
	}
	next, err := legacySessionNodeSpool(ctx, owner, row.next)
	if err != nil {
		return true, err
	}
	defer next.Close()
	if row.before.typ != nil && !force {
		before, err := legacySessionNodeSpool(ctx, owner, row.before)
		if err != nil {
			return true, err
		}
		defer before.Close()
		a, err := legacySessionJSONObject(ctx, owner, legacySessionSpoolPart(before))
		if err != nil {
			return true, err
		}
		b, err := legacySessionJSONObject(ctx, owner, legacySessionSpoolPart(next))
		if err != nil {
			return true, err
		}
		if same, err := legacySessionObjectsEqual(ctx, owner, a, b); err != nil || same {
			return true, err
		}
	}
	if metadataSearchFieldSet["workflowRuns"] {
		return true, ErrBoundedMetadataCoverage
	}
	if _, err := next.Seek(0, io.SeekStart); err != nil {
		return true, err
	}
	var size legacyProjectByteCounter
	if err := legacyProjectCopyStoredJSON(ctx, &size, next); err != nil {
		return true, err
	}
	if _, err := next.Seek(0, io.SeekStart); err != nil {
		return true, err
	}
	stage, err := beginMetadataSQLWriteStage(ctx, p.tx, metadataSQLStageTarget{
		workspace: p.workspace, field: "workflowRuns", key: row.id, order: order, dataBytes: int(size), searchBytes: -1,
	})
	if err != nil {
		return true, err
	}
	defer stage.Close(context.WithoutCancel(ctx))
	writer, err := stage.writer(ctx, "data")
	if err != nil {
		return true, err
	}
	if err := legacyProjectCopyStoredJSON(ctx, writer, next); err != nil {
		return true, err
	}
	if err := writer.flush(true); err != nil {
		return true, err
	}
	if err := stage.writeMetadataRow(ctx); err != nil {
		return true, err
	}
	if err := writeCustomerFilterRecord(ctx, p.tx, p.workspace, "workflowRuns", row.id, nil); err != nil {
		return true, err
	}
	if err := writeReleaseIssueRecords(ctx, p.tx, p.workspace, "workflowRuns", row.id, nil); err != nil {
		return true, err
	}
	return true, stage.Close(ctx)
}

func (s *SQLiteStore) refreshWorkflowRunStreamConflict(ctx context.Context, workspace string) func(error) error {
	return s.refreshMetadataLifecycleConflict(ctx, workspace, "workflowRuns", workflowRunCanonicalRecord)
}
