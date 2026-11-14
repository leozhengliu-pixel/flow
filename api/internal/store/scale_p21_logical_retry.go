package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"

	"flow/api/internal/domain"
)

type metadataLogicalRecordPatch struct {
	key metadataRecordKey
	raw []byte
}

type metadataLogicalPatchedCollection struct {
	base  metadataAggregateNode
	shape metadataLogicalNode
	key   string
	value metadataAggregateNode
}

func (n metadataLogicalPatchedCollection) nodeType() reflect.Type           { return n.base.nodeType() }
func (n metadataLogicalPatchedCollection) logicalBase() metadataLogicalNode { return n.shape }
func (n metadataLogicalPatchedCollection) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func metadataLogicalMapEntries(ctx context.Context, node metadataAggregateNode, visit func(metadataAggregateNode, metadataAggregateNode) error) error {
	if stored, ok := node.(metadataAggregateStoredNode); ok {
		return stored.value.MapEntries(ctx, func(key, value metadataLogicalNode) error {
			return visit(metadataAggregateStoredNode{key}, metadataAggregateStoredNode{value})
		})
	}
	if mapped, ok := node.(metadataLogicalTransformMap); ok {
		return mapped.logicalMapEntries(ctx, visit)
	}
	return ErrMetadataRecordQuery
}
func (n metadataLogicalPatchedCollection) logicalMapEntries(ctx context.Context, visit func(metadataAggregateNode, metadataAggregateNode) error) error {
	found := false
	if err := metadataLogicalMapEntries(ctx, n.base, func(key, value metadataAggregateNode) error {
		match, err := metadataAggregateStringEqual(ctx, key, n.key)
		if err != nil {
			return err
		}
		if match {
			found = true
			if n.value == nil {
				return nil
			}
			value = n.value
		}
		return visit(key, value)
	}); err != nil {
		return err
	}
	if !found && n.value != nil {
		return visit(metadataAggregateNativeNode{reflect.ValueOf(n.key).Convert(n.nodeType().Key())}, n.value)
	}
	return nil
}
func (n metadataLogicalPatchedCollection) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	if n.nodeType().Kind() == reflect.Map {
		return n.logicalMapEntries(ctx, func(key, value metadataAggregateNode) error {
			decoded, err := key.nodeValue(ctx, MetadataRecordPageMaxBytes)
			if err != nil {
				return err
			}
			return visit(decoded, value)
		})
	}
	found, ordinal := false, 0
	id, _ := n.nodeType().Elem().FieldByName("ID")
	if err := n.base.nodeChildren(ctx, func(_ reflect.Value, value metadataAggregateNode) error {
		if !found {
			idNode, err := metadataAggregateIDNode(ctx, value, id.Index)
			if err != nil {
				return err
			}
			match, err := metadataAggregateStringEqual(ctx, idNode, n.key)
			if err != nil {
				return err
			}
			if match {
				found = true
				if n.value == nil {
					return nil
				}
				value = n.value
			}
		}
		err := visit(reflect.ValueOf(ordinal), value)
		ordinal++
		return err
	}); err != nil {
		return err
	}
	if !found && n.value != nil {
		return visit(reflect.ValueOf(ordinal), n.value)
	}
	return nil
}

type metadataLogicalRetainedRecord struct {
	fresh metadataLogicalNode
	keep  map[string]metadataAggregateNode
}

func (n metadataLogicalRetainedRecord) nodeType() reflect.Type { return n.fresh.typ }
func (n metadataLogicalRetainedRecord) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n metadataLogicalRetainedRecord) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	return n.fresh.Children(ctx, func(key reflect.Value, value metadataLogicalNode) error {
		child := metadataAggregateNode(metadataAggregateStoredNode{value})
		if old, held := n.keep[key.String()]; held {
			child = old
		}
		return visit(key, child)
	})
}

func metadataLogicalFirstRecord(ctx context.Context, collection metadataAggregateNode, key string) (metadataAggregateNode, error) {
	var result metadataAggregateNode
	var err error
	if collection.nodeType().Kind() == reflect.Map {
		err = metadataLogicalMapEntries(ctx, collection, func(id, value metadataAggregateNode) error {
			match, err := metadataAggregateStringEqual(ctx, id, key)
			if err != nil {
				return err
			}
			if match {
				result = value
				return errMetadataAggregateStop
			}
			return nil
		})
	} else {
		id, _ := collection.nodeType().Elem().FieldByName("ID")
		err = collection.nodeChildren(ctx, func(_ reflect.Value, value metadataAggregateNode) error {
			child, err := metadataAggregateIDNode(ctx, value, id.Index)
			if err != nil {
				return err
			}
			match, err := metadataAggregateStringEqual(ctx, child, key)
			if err != nil {
				return err
			}
			if match {
				result = value
				return errMetadataAggregateStop
			}
			return nil
		})
	}
	if errors.Is(err, errMetadataAggregateStop) {
		err = nil
	}
	return result, err
}

func metadataLogicalRetryRecord(ctx context.Context, builder *metadataLogicalBuilder, field string, kind reflect.Type, raw []byte, old metadataAggregateNode) (metadataAggregateNode, error) {
	// The existing marker resolver only expands current-document references.
	// Reuse its audited realtime preflight before that bounded expansion.
	if (field == "documents" || field == "trash") && bytes.Contains(raw, documentMarkerBytes) {
		document := raw
		if field == "trash" {
			var entry struct {
				Payload json.RawMessage `json:"payload"`
			}
			if err := json.Unmarshal(raw, &entry); err != nil {
				return nil, err
			}
			document = entry.Payload
		}
		if _, err := DecodeRealtimeDocument(document); err != nil {
			return nil, err
		}
	}
	resolved, err := resolveDocumentMarkers(field, raw)
	if err != nil {
		return nil, err
	}
	if len(resolved) > 2<<20 {
		return nil, ErrMetadataPageBudget
	}
	record, err := decodeMetadataRecord(kind, resolved, reflect.Value{})
	if err != nil {
		return nil, err
	}
	keep := map[string]metadataAggregateNode{}
	if old != nil && kind.Kind() == reflect.Struct {
		if err := old.nodeChildren(ctx, func(key reflect.Value, value metadataAggregateNode) error {
			info, exists := kind.FieldByName(key.String())
			if !exists || info.PkgPath != "" {
				return nil
			}
			retain := info.Tag.Get("json") == "-"
			retain = retain || field == "importJobs" && info.Name == "Rows" && record.Field(info.Index[0]).IsNil()
			retain = retain || field == "migrationJobs" && info.Name == "Bundle" && record.Field(info.Index[0]).IsNil()
			if retain {
				keep[info.Name] = value
				record.Field(info.Index[0]).SetZero()
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	if err := builder.auditJSONLocations(record); err != nil {
		return nil, err
	}
	fresh, err := builder.writeValue(record, 0, false)
	if err != nil {
		return nil, err
	}
	if len(keep) == 0 {
		return metadataAggregateStoredNode{fresh}, nil
	}
	return metadataLogicalRetainedRecord{fresh, keep}, nil
}

// Replacements use the old collection's first-occurrence/set/remove/append
// rules. Untouched frames and private/input subtrees are never JSON encoded.
func metadataLogicalPatchRecords(ctx context.Context, source *metadataLogicalSource, patches []metadataLogicalRecordPatch) (*metadataLogicalSource, error) {
	if len(patches) == 0 {
		return source, source.Retain()
	}
	if len(patches) > MetadataRecordBatchMaxKeys {
		return nil, ErrMetadataPageBudget
	}
	bytes := 0
	for _, patch := range patches {
		bytes += len(patch.raw) + len(patch.key.key)
	}
	if bytes > MetadataRecordPageMaxBytes {
		return nil, ErrMetadataPageBudget
	}
	fresh, err := newMetadataLogicalBuilder(metadataLogicalSourceScratchContext(ctx, source))
	if err != nil {
		return nil, err
	}
	defer fresh.Close()
	return source.Transform(ctx, func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
		current := metadataAggregateNode(metadataAggregateStoredNode{node})
		var changes []metadataLogicalRecordPatch
		for _, patch := range patches {
			if patch.key.field == name {
				changes = append(changes, patch)
			}
		}
		if len(changes) == 0 {
			return current, false, nil
		}
		_, kind, ok := metadataRecordType(name)
		if !ok {
			return current, false, nil
		}
		shape := node
		if node.typ.Kind() == reflect.Map {
			shape, err = fresh.writeValue(reflect.MakeMap(node.typ), 0, false)
			if err != nil {
				return nil, false, err
			}
		}
		for _, patch := range changes {
			var replacement metadataAggregateNode
			if patch.raw != nil {
				old, err := metadataLogicalFirstRecord(ctx, current, patch.key.key)
				if err != nil {
					return nil, false, err
				}
				replacement, err = metadataLogicalRetryRecord(ctx, fresh, name, kind, patch.raw, old)
				if err != nil {
					return nil, false, err
				}
			}
			current = metadataLogicalPatchedCollection{current, shape, patch.key.key, replacement}
		}
		return current, true, nil
	})
}

func (s *SQLiteStore) refreshConflictedLogicalMetadata(ctx context.Context, workspace string) func(error) error {
	return func(conflict error) error {
		keys, ok := conflictedMetadataKeys(conflict)
		if !ok {
			return nil
		}
		if len(keys) > MetadataRecordBatchMaxKeys {
			return ErrMetadataPageBudget
		}
		ctx = metadataLogicalUnpinnedContext(ctx)
		ctx, release, err := s.tryAdmitAdditionalMetadataWork(ctx, 32<<20)
		if err != nil {
			return err
		}
		defer release()
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		source, err := s.retainMetadataLogicalSource(ctx, workspace)
		if err != nil {
			return err
		}
		defer source.Release()
		byField := map[string][]string{}
		weight := 0
		for _, key := range keys {
			weight += len(key.key)
			if _, _, ok := metadataRecordType(key.field); ok {
				byField[key.field] = append(byField[key.field], key.key)
			}
		}
		if weight > MetadataRecordPageMaxBytes {
			return ErrMetadataPageBudget
		}
		loaded := map[metadataRecordKey][]byte{}
		for field, ids := range byField {
			clause, args := bindList("record_key", ids)
			rows, err := s.db.QueryContext(ctx, `SELECT record_key,LENGTH(data),CASE WHEN LENGTH(data)<=? THEN data ELSE NULL END FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND `+clause,
				append([]any{MetadataRecordPageMaxBytes, workspace, field}, args...)...)
			if err != nil {
				return err
			}
			for rows.Next() {
				var key string
				var size int
				var raw []byte
				if err := rows.Scan(&key, &size, &raw); err != nil {
					rows.Close()
					return err
				}
				if !slices.Contains(ids, key) {
					continue
				}
				weight += len(raw)
				if raw == nil || size > MetadataRecordPageMaxBytes || weight > MetadataRecordPageMaxBytes {
					rows.Close()
					return ErrMetadataPageBudget
				}
				loaded[metadataRecordKey{field, key}] = raw
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
		}
		var patches []metadataLogicalRecordPatch
		for _, key := range keys {
			if _, _, ok := metadataRecordType(key.field); ok {
				patches = append(patches, metadataLogicalRecordPatch{key, loaded[key]})
			}
		}
		var candidate *metadataLogicalSource
		if len(patches) == 0 {
			candidate, err = source.Transform(ctx, func(_ string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
				return metadataAggregateStoredNode{node}, false, nil
			})
		} else {
			candidate, err = metadataLogicalPatchRecords(ctx, source, patches)
		}
		if err != nil {
			return err
		}
		defer func() {
			if candidate != nil {
				candidate.Release()
			}
		}()
		stamp := copyStamp{workspaceID: source.Stamp().workspaceID, revision: -1}
		if err := candidate.setStamp(stamp); err != nil {
			return err
		}
		policy, cost, err := s.metadataLogicalPolicy(ctx, candidate)
		if err != nil {
			return err
		}
		if err := s.installResidentPolicy(workspace, policy, stamp, 0, cost, candidate); err != nil {
			return err
		}
		candidate = nil
		return nil
	}
}

func metadataLogicalMergedPostimage(ctx context.Context, source *metadataLogicalSource, changes []metadataRecordChange) (*metadataLogicalSource, error) {
	var patches []metadataLogicalRecordPatch
	for _, change := range changes {
		if change.skipped {
			patches = append(patches, metadataLogicalRecordPatch{key: metadataRecordKey{change.field, change.key}})
		} else if change.merged && !change.drop {
			patches = append(patches, metadataLogicalRecordPatch{metadataRecordKey{change.field, change.key}, change.raw})
		}
	}
	return metadataLogicalPatchRecords(ctx, source, patches)
}

// The legacy delta writer may merge or delete records in complete fields.
// Its target owns the callback's native values plus fresh standard-JSON
// decodes. Existing location receipts cover the former; the latter are audited
// before their typed values are framed. No native value is JSON round-tripped.
// The preceding candidate.Fork already validated every callback-native value,
// so only fresh canonical decoded Locations can require additional receipts.
func metadataLogicalCompletePostimage(ctx context.Context, source *metadataLogicalSource, before, after domain.Bootstrap, scope MetadataRecordScope) (*metadataLogicalSource, error) {
	left, right := reflect.ValueOf(before), reflect.ValueOf(after)
	var builders []*metadataLogicalBuilder
	defer func() {
		for _, builder := range builders {
			builder.Close()
		}
	}()
	return source.Transform(ctx, func(name string, original metadataLogicalNode) (metadataAggregateNode, bool, error) {
		index, _, ok := metadataLogicalField(name)
		if !ok || name != "viewer" && !slices.Contains(scope.Fields, name) ||
			metadataValueEqual(left.Field(index), right.Field(index)) {
			return metadataAggregateStoredNode{original}, false, nil
		}
		builder, err := newMetadataLogicalBuilder(metadataLogicalSourceScratchContext(ctx, source))
		if err != nil {
			return nil, false, err
		}
		builders = append(builders, builder)
		builder.storage.types = slices.Clone(original.source.types)
		builder.storage.times = slices.Clone(original.source.times)
		if err := builder.inheritLocations(original.source); err != nil {
			return nil, false, err
		}
		if err := builder.inheritTimestampInputs(ctx); err != nil {
			return nil, false, err
		}
		value := right.Field(index)
		if err := builder.auditJSONLocations(value); err != nil {
			return nil, false, err
		}
		node, err := builder.writeValue(value, 0, false)
		if err != nil {
			return nil, false, err
		}
		return metadataAggregateStoredNode{node}, true, nil
	})
}
