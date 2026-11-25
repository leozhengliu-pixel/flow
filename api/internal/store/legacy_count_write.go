package store

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"

	"flow/api/internal/domain"
)

type legacyCountWriteHeader struct {
	id           string
	before, next metadataLogicalNode
	changed      bool
	deleted      bool
}

type legacyCountWriteField struct {
	name          string
	before, next  metadataLogicalNode
	headers       []legacyCountWriteHeader
	changed, full bool
}

// persistLegacyResourceCounts persists the already-computed count pass. All
// rebases finish before any write, matching the full delta's conflict boundary.
// The caller owns the transaction and subsequent publication.
func (s *SQLiteStore) persistLegacyResourceCounts(ctx context.Context, tx *sqlTx, workspace string, before, after, directory *metadataLogicalSource) (_ *metadataLogicalSource, err error) {
	if s == nil || !s.BoundedMetadataEnabled() || tx == nil || tx.database != s.db || tx.dialect != s.dialect ||
		before == nil || after == nil || directory == nil {
		return nil, ErrBoundedMetadataCoverage
	}
	for _, source := range []*metadataLogicalSource{before, after, directory} {
		if err := source.Retain(); err != nil {
			return nil, err
		}
		defer source.Release()
	}
	ctx = metadataLogicalSourceScratchContext(metadataLogicalUnpinnedContext(ctx), after)
	work := &ResourceRead{store: s}
	defer work.Close()
	if uint64(len(workspace)) > (metadataWorkBytes-(256<<10))/16 {
		return nil, ErrMetadataWorkBudget
	}
	if err := work.reserve(ctx, uint64(len(workspace))*16+256<<10); err != nil {
		return nil, err
	}
	stamp, err := readMetadataStamp(ctx, tx, workspace)
	if err != nil {
		return nil, err
	}
	workspaceIndex, _, _ := metadataLogicalField("workspace")
	for _, source := range []*metadataLogicalSource{before, after, directory} {
		if stamp.workspaceID == "" || source.Stamp().workspaceID != stamp.workspaceID {
			return nil, ErrMetadataRecordQuery
		}
		id, err := issueHotField(ctx, source.fields[workspaceIndex], "ID")
		if err != nil {
			return nil, err
		}
		matched, err := id.StringEqual(ctx, stamp.workspaceID)
		if err != nil {
			return nil, err
		}
		if !matched {
			return nil, ErrMetadataRecordQuery
		}
	}
	fields := [2]legacyCountWriteField{{name: "labels"}, {name: "projects"}}
	anyChanged := false
	for i := range fields {
		field := &fields[i]
		index, _, _ := metadataLogicalField(field.name)
		field.before, field.next = before.fields[index], after.fields[index]
		leftNil, err := field.before.isNil(ctx)
		if err != nil {
			return nil, err
		}
		rightNil, err := field.next.isNil(ctx)
		if err != nil {
			return nil, err
		}
		if leftNil != rightNil {
			return nil, ErrMetadataRecordQuery
		}
		if err := metadataLogicalPairs(ctx, field.before, field.next, func(_ metadataLogicalNode, old, next metadataLogicalNode) error {
			changed, err := legacyCountWriteChanged(ctx, old, next)
			field.changed = field.changed || changed
			return err
		}); err != nil {
			return nil, err
		}
		anyChanged = anyChanged || field.changed
	}
	if !anyChanged {
		if err := after.Retain(); err != nil {
			return nil, err
		}
		return after, nil
	}
	shapes, err := s.legacyCountWriteRoot(ctx, tx, workspace, work)
	if err != nil {
		return nil, err
	}
	frames, err := newMetadataLogicalBuilder(ctx)
	if err != nil {
		return nil, err
	}
	defer frames.Close()
	for _, source := range []*metadataLogicalSource{before, after, directory} {
		for _, storage := range source.stores {
			if err := frames.inheritLocations(storage); err != nil {
				return nil, err
			}
		}
	}
	var conflicts []metadataRecordKey
	for i := range fields {
		field := &fields[i]
		if !field.changed {
			continue
		}
		if err := field.readHeaders(ctx, work); err != nil {
			return nil, err
		}
		shape, err := field.shape()
		if err != nil {
			return nil, err
		}
		field.full = shapes[field.name] != "array" || shape != "array"
		err = s.legacyCountWriteRebase(ctx, tx, workspace, directory, field, frames, work)
		if keys, ok := conflictedMetadataKeys(err); ok {
			conflicts = append(conflicts, keys...)
		} else if err != nil {
			return nil, err
		}
	}
	if len(conflicts) != 0 {
		return nil, &metadataConflictError{keys: conflicts}
	}
	// A canonical rebase may alter an ID and make a formerly split field
	// inline. Admit that complete field before entering the write phase.
	for i := range fields {
		field := &fields[i]
		if !field.changed || field.full {
			continue
		}
		shape, err := field.shape()
		if err != nil {
			return nil, err
		}
		if shape != "array" {
			left, err := frames.writeAggregateNode(legacyCountWriteCollection{base: field.before, headers: field.headers}, 0)
			if err != nil {
				return nil, err
			}
			right, err := frames.writeAggregateNode(legacyCountWriteCollection{base: field.next, headers: field.headers, next: true}, 0)
			if err != nil {
				return nil, err
			}
			field.before, field.next, field.full = left, right, true
		}
	}
	for i := range fields {
		if fields[i].changed {
			if err := s.legacyCountWriteReplay(ctx, tx, workspace, directory, &fields[i], stamp.workspaceID); err != nil {
				return nil, err
			}
		}
	}
	result, err := after.Transform(ctx, func(name string, original metadataLogicalNode) (metadataAggregateNode, bool, error) {
		for i := range fields {
			field := &fields[i]
			if field.name == name && field.changed {
				if field.full {
					return metadataAggregateStoredNode{value: field.next}, true, nil
				}
				return legacyCountWriteCollection{base: original, headers: field.headers, next: true}, true, nil
			}
		}
		return metadataAggregateStoredNode{value: original}, false, nil
	})
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		result.Release()
		return nil, err
	}
	return result, nil
}

func legacyCountWriteChanged(ctx context.Context, before, after metadataLogicalNode) (bool, error) {
	old, err := issueHotField(ctx, before, "IssueCount")
	if err != nil {
		return false, err
	}
	next, err := issueHotField(ctx, after, "IssueCount")
	if err != nil {
		return false, err
	}
	left, err := old.Value(ctx, 4096)
	if err != nil {
		return false, err
	}
	right, err := next.Value(ctx, 4096)
	return err == nil && left.Int() != right.Int(), err
}

func (f *legacyCountWriteField) readHeaders(ctx context.Context, work *ResourceRead) error {
	count, cost := 0, uint64(64<<10)
	err := metadataLogicalPairs(ctx, f.before, f.next, func(_ metadataLogicalNode, old, next metadataLogicalNode) error {
		id, err := issueHotField(ctx, old, "ID")
		if err != nil {
			return err
		}
		rightID, err := issueHotField(ctx, next, "ID")
		if err != nil {
			return err
		}
		equal, err := metadataAggregateStringsEqual(ctx, metadataAggregateStoredNode{value: id}, metadataAggregateStoredNode{value: rightID})
		if err != nil {
			return err
		}
		if !equal {
			return ErrMetadataRecordQuery
		}
		if cost > metadataWorkBytes-8192 || id.cost > (metadataWorkBytes-cost-8192)/64 {
			return ErrMetadataWorkBudget
		}
		cost += id.cost*64 + 8192
		count++
		return nil
	})
	if err != nil {
		return err
	}
	if err := work.reserve(ctx, cost); err != nil {
		return err
	}
	f.headers = make([]legacyCountWriteHeader, 0, count)
	return metadataLogicalPairs(ctx, f.before, f.next, func(_ metadataLogicalNode, old, next metadataLogicalNode) error {
		id, err := issueHotField(ctx, old, "ID")
		if err != nil {
			return err
		}
		value, err := id.Value(ctx, metadataWorkBytes)
		if err != nil {
			return err
		}
		changed, err := legacyCountWriteChanged(ctx, old, next)
		if err != nil {
			return err
		}
		f.headers = append(f.headers, legacyCountWriteHeader{id: value.String(), before: old, next: next, changed: changed})
		return nil
	})
}

func (f *legacyCountWriteField) skeleton() reflect.Value {
	value := reflect.MakeSlice(f.before.typ, 0, len(f.headers))
	for _, header := range f.headers {
		if header.deleted {
			continue
		}
		item := reflect.New(value.Type().Elem()).Elem()
		item.FieldByName("ID").SetString(header.id)
		value = reflect.Append(value, item)
	}
	return value
}

func (f *legacyCountWriteField) shape() (string, error) {
	raw, err := json.Marshal(map[string]any{f.name: f.skeleton().Interface()})
	if err != nil {
		return "", err
	}
	_, shapes, _, err := splitWorkspaceMetadata(raw)
	return shapes[f.name], err
}

func (s *SQLiteStore) legacyCountWriteRebase(ctx context.Context, tx *sqlTx, workspace string, directory *metadataLogicalSource, field *legacyCountWriteField, frames *metadataLogicalBuilder, headers *ResourceRead) error {
	if field.full {
		return s.legacyCountWriteRebasePart(ctx, tx, workspace, directory, field, frames, headers, -1)
	}
	order := make([]int, 0, len(field.headers))
	for i, header := range field.headers {
		if header.changed {
			order = append(order, i)
		}
	}
	sort.Slice(order, func(i, j int) bool { return field.headers[order[i]].id < field.headers[order[j]].id })
	var conflicts []metadataRecordKey
	for _, ordinal := range order {
		err := s.legacyCountWriteRebasePart(ctx, tx, workspace, directory, field, frames, headers, ordinal)
		if keys, ok := conflictedMetadataKeys(err); ok {
			conflicts = append(conflicts, keys...)
		} else if err != nil {
			return err
		}
	}
	if len(conflicts) != 0 {
		return &metadataConflictError{keys: conflicts}
	}
	return nil
}

func (s *SQLiteStore) legacyCountWriteRebasePart(ctx context.Context, tx *sqlTx, workspace string, directory *metadataLogicalSource, field *legacyCountWriteField, frames *metadataLogicalBuilder, headers *ResourceRead, ordinal int) error {
	work := &ResourceRead{store: s}
	defer work.Close()
	leftNode, rightNode := field.before, field.next
	if ordinal >= 0 {
		leftNode, rightNode = field.headers[ordinal].before, field.headers[ordinal].next
	}
	for _, node := range []metadataLogicalNode{leftNode, rightNode} {
		if err := legacyActorWriteNodeWork(ctx, work, node, 64); err != nil {
			return err
		}
	}
	left, err := leftNode.Value(ctx, metadataWorkBytes)
	if err != nil {
		return err
	}
	right, err := rightNode.Value(ctx, metadataWorkBytes)
	if err != nil {
		return err
	}
	originalLeft, originalRight := left, right
	if ordinal >= 0 {
		left = reflect.Append(reflect.MakeSlice(field.before.typ, 0, 1), left)
		right = reflect.Append(reflect.MakeSlice(field.next.typ, 0, 1), right)
	} else {
		originalLeft, originalRight = cloneMetadataContainer(left), cloneMetadataContainer(right)
	}
	users, err := legacyCountWriteDirectory(ctx, work, directory, field.name, left, right)
	if err != nil {
		return err
	}
	oldData := domain.Bootstrap{Users: users}
	nextData := oldData
	metadata, _ := metadataFieldByName(field.name)
	reflect.ValueOf(&oldData).Elem().Field(metadata.index).Set(left)
	reflect.ValueOf(&nextData).Elem().Field(metadata.index).Set(right)
	_, _, err = s.rebaseMetadata(withLegacyMetadataDeltaReadBudget(ctx, work), tx, workspace, &oldData, &nextData, []metadataField{metadata}, nil)
	if err != nil {
		return err
	}
	left = reflect.ValueOf(oldData).Field(metadata.index)
	right = reflect.ValueOf(nextData).Field(metadata.index)
	if ordinal >= 0 {
		if left.Len() == 0 && right.Len() == 0 {
			field.headers[ordinal].deleted = true
			return nil
		}
		if left.Len() != 1 || right.Len() != 1 || left.Index(0).FieldByName("ID").String() != right.Index(0).FieldByName("ID").String() {
			return ErrBoundedMetadataCoverage
		}
		left, right = left.Index(0), right.Index(0)
		id := right.FieldByName("ID").String()
		if id != field.headers[ordinal].id {
			if uint64(len(id)) > (metadataWorkBytes-8192)/64 {
				return ErrMetadataWorkBudget
			}
			if err := headers.reserve(ctx, uint64(len(id))*64+8192); err != nil {
				return err
			}
			field.headers[ordinal].id = id
		}
	}
	store := func(original metadataLogicalNode, native, actual reflect.Value) (metadataLogicalNode, error) {
		if reflect.DeepEqual(native.Interface(), actual.Interface()) {
			return original, nil
		}
		if err := frames.auditJSONLocations(actual); err != nil {
			return metadataLogicalNode{}, err
		}
		if actual.Kind() == reflect.Slice {
			return frames.writeAggregateNode(legacyCountWriteNativeCollection{base: original, value: actual}, 0)
		}
		return frames.writeAggregateNode(legacyCountWriteNativeRecord{value: actual}, 0)
	}
	preparedLeft, err := store(leftNode, originalLeft, left)
	if err != nil {
		return err
	}
	preparedRight, err := store(rightNode, originalRight, right)
	if err != nil {
		return err
	}
	if ordinal < 0 {
		field.before, field.next = preparedLeft, preparedRight
	} else {
		field.headers[ordinal].before, field.headers[ordinal].next = preparedLeft, preparedRight
	}
	return nil
}

// Only count-changing Projects need their Lead/Creator directory entries.
// JSON string normalization matches canonicalDisplayData's decoded IDs.
func legacyCountWriteDirectory(ctx context.Context, work *ResourceRead, directory *metadataLogicalSource, name string, before, after reflect.Value) ([]domain.User, error) {
	if name != "projects" {
		return nil, nil
	}
	seen := map[string]bool{}
	var users []domain.User
	for i := 0; i < before.Len() && i < after.Len(); i++ {
		if before.Index(i).FieldByName("IssueCount").Int() == after.Index(i).FieldByName("IssueCount").Int() {
			continue
		}
		for _, item := range []reflect.Value{before.Index(i), after.Index(i)} {
			for _, name := range []string{"Lead", "Creator"} {
				user := item.FieldByName(name)
				if user.IsNil() {
					continue
				}
				raw, err := json.Marshal(user.Elem().FieldByName("ID").String())
				if err != nil {
					return nil, err
				}
				var id string
				if err := json.Unmarshal(raw, &id); err != nil {
					return nil, err
				}
				if seen[id] {
					continue
				}
				seen[id] = true
				found, err := metadataAggregateLookupUser(ctx, directory, metadataAggregateNativeNode{value: reflect.ValueOf(id)})
				if err != nil {
					return nil, err
				}
				if found == nil {
					continue
				}
				node := found.(metadataAggregateStoredNode).value
				if err := legacyActorWriteNodeWork(ctx, work, node, 64); err != nil {
					return nil, err
				}
				value, err := node.Value(ctx, metadataWorkBytes)
				if err != nil {
					return nil, err
				}
				users = append(users, value.Interface().(domain.User))
			}
		}
	}
	return users, nil
}

func (s *SQLiteStore) legacyCountWriteReplay(ctx context.Context, tx *sqlTx, workspace string, directory *metadataLogicalSource, field *legacyCountWriteField, workspaceID string) error {
	replay := func(ordinal int) error {
		work := &ResourceRead{store: s}
		defer work.Close()
		var left, right reflect.Value
		if ordinal < 0 {
			for _, node := range []metadataLogicalNode{field.before, field.next} {
				if err := legacyActorWriteNodeWork(ctx, work, node, 64); err != nil {
					return err
				}
			}
			var err error
			left, err = field.before.Value(ctx, metadataWorkBytes)
			if err != nil {
				return err
			}
			right, err = field.next.Value(ctx, metadataWorkBytes)
			if err != nil {
				return err
			}
		} else {
			left, right = field.skeleton(), field.skeleton()
			position := 0
			for i, header := range field.headers {
				if header.deleted {
					continue
				}
				if i == ordinal {
					for _, node := range []metadataLogicalNode{header.before, header.next} {
						if err := legacyActorWriteNodeWork(ctx, work, node, 64); err != nil {
							return err
						}
					}
					old, err := header.before.Value(ctx, metadataWorkBytes)
					if err != nil {
						return err
					}
					next, err := header.next.Value(ctx, metadataWorkBytes)
					if err != nil {
						return err
					}
					left.Index(position).Set(old)
					right.Index(position).Set(next)
				}
				position++
			}
		}
		users, err := legacyCountWriteDirectory(ctx, work, directory, field.name, left, right)
		if err != nil {
			return err
		}
		oldData := domain.Bootstrap{Workspace: domain.Workspace{ID: workspaceID}, Users: users}
		nextData := oldData
		metadata, _ := metadataFieldByName(field.name)
		reflect.ValueOf(&oldData).Elem().Field(metadata.index).Set(left)
		reflect.ValueOf(&nextData).Elem().Field(metadata.index).Set(right)
		// Rebasing has already happened once, before every write. Temporary
		// aliases also cover canonical records whose decoded ID changed.
		var added []metadataRecordKey
		if tx.rebasedMetadata == nil {
			tx.rebasedMetadata = map[metadataRecordKey]bool{}
		}
		for _, values := range []reflect.Value{left, right} {
			for i := 0; i < values.Len(); i++ {
				key := metadataRecordKey{field.name, values.Index(i).FieldByName("ID").String()}
				if !tx.rebasedMetadata[key] {
					tx.rebasedMetadata[key] = true
					added = append(added, key)
				}
			}
		}
		defer func() {
			for _, key := range added {
				delete(tx.rebasedMetadata, key)
			}
		}()
		_, err = s.writeWorkspaceMetadataDelta(withLegacyMetadataDeltaReadBudget(ctx, work), tx, workspace, oldData, &nextData)
		return err
	}
	if field.full {
		return replay(-1)
	}
	for ordinal, header := range field.headers {
		if header.changed && !header.deleted {
			if err := replay(ordinal); err != nil {
				return err
			}
		}
	}
	// If every changed record was deleted, both rebased sides are equal and
	// the literal delta leaves canonical rows and root shapes alone.
	return nil
}

func (s *SQLiteStore) legacyCountWriteRoot(ctx context.Context, tx *sqlTx, workspace string, work *ResourceRead) (map[string]string, error) {
	length := work.byteLength("data")
	lock := ""
	if tx.dialect != "sqlite" {
		lock = " FOR UPDATE"
	}
	var size int64
	if err := tx.QueryRowContext(ctx, "SELECT "+length+" FROM workspace_states WHERE workspace_key=?"+lock, workspace).Scan(&size); err != nil {
		return nil, err
	}
	if size < 0 || size > MetadataRecordPageMaxBytes || uint64(size) > (metadataWorkBytes-(64<<10))/128 {
		return nil, ErrMetadataWorkBudget
	}
	if err := work.reserve(ctx, uint64(size)*128+64<<10); err != nil {
		return nil, err
	}
	var raw []byte
	if err := tx.QueryRowContext(ctx, "SELECT CASE WHEN "+length+"<=? THEN data ELSE NULL END FROM workspace_states WHERE workspace_key=?"+lock, size, workspace).Scan(&raw); err != nil {
		return nil, err
	}
	if raw == nil || int64(len(raw)) != size {
		return nil, ErrConcurrentWrite
	}
	var root struct {
		Shapes map[string]string `json:"_flowCollections"`
	}
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	return root.Shapes, nil
}

type legacyCountWriteCollection struct {
	base    metadataLogicalNode
	headers []legacyCountWriteHeader
	next    bool
}

func (n legacyCountWriteCollection) nodeType() reflect.Type           { return n.base.typ }
func (n legacyCountWriteCollection) logicalBase() metadataLogicalNode { return n.base }
func (n legacyCountWriteCollection) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n legacyCountWriteCollection) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	ordinal := 0
	for _, header := range n.headers {
		if header.deleted {
			continue
		}
		value := header.before
		if n.next {
			value = header.next
		}
		if err := visit(reflect.ValueOf(ordinal), metadataAggregateStoredNode{value: value}); err != nil {
			return err
		}
		ordinal++
	}
	return nil
}

type legacyCountWriteNativeCollection struct {
	base  metadataLogicalNode
	value reflect.Value
}

func (n legacyCountWriteNativeCollection) nodeType() reflect.Type           { return n.value.Type() }
func (n legacyCountWriteNativeCollection) logicalBase() metadataLogicalNode { return n.base }
func (n legacyCountWriteNativeCollection) nodeValue(context.Context, int) (reflect.Value, error) {
	return n.value, nil
}
func (n legacyCountWriteNativeCollection) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	for i := 0; i < n.value.Len(); i++ {
		if err := visit(reflect.ValueOf(i), legacyCountWriteNativeRecord{value: n.value.Index(i)}); err != nil {
			return err
		}
	}
	return nil
}

// Project directory refreshes can carry request-scoped User.Via. Preserve it
// on untouched native Lead/Creator values; a canonical JSON rebase loses it
// exactly as decodeMetadataRecord does in the original writer.
type legacyCountWriteNativeRecord struct{ value reflect.Value }

func (n legacyCountWriteNativeRecord) nodeType() reflect.Type { return n.value.Type() }
func (n legacyCountWriteNativeRecord) nodeValue(context.Context, int) (reflect.Value, error) {
	return n.value, nil
}
func (n legacyCountWriteNativeRecord) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	for i := 0; i < n.value.NumField(); i++ {
		value := n.value.Field(i)
		var child metadataAggregateNode = metadataAggregateNativeNode{value: value}
		name := n.value.Type().Field(i).Name
		if name == "Lead" || name == "Creator" {
			child = legacyCountWriteUserPointer{value: value}
		}
		if err := visit(reflect.ValueOf(name), child); err != nil {
			return err
		}
	}
	return nil
}

type legacyCountWriteUserPointer struct{ value reflect.Value }

func (n legacyCountWriteUserPointer) nodeType() reflect.Type { return n.value.Type() }
func (n legacyCountWriteUserPointer) nodeValue(context.Context, int) (reflect.Value, error) {
	return n.value, nil
}
func (n legacyCountWriteUserPointer) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	if n.value.IsNil() {
		return nil
	}
	return visit(reflect.Value{}, legacyActorUserNode{value: n.value.Elem()})
}
