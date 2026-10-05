package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"
	"unsafe"

	"flow/api/internal/domain"
)

type metadataField struct {
	index int
	name  string
}

var (
	metadataFieldsOnce sync.Once
	metadataFieldList  []metadataField
)

// persistedMetadataFields lists the Bootstrap fields stored in the workspace
// metadata document (root scalars plus record-backed collections). Issue and
// discussion collections live in their own tables.
func persistedMetadataFields() []metadataField {
	metadataFieldsOnce.Do(func() {
		kind := reflect.TypeFor[domain.Bootstrap]()
		for i := 0; i < kind.NumField(); i++ {
			field := kind.Field(i)
			name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			if field.PkgPath != "" || name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			switch name {
			case "issues", "activities", "comments", "notifications", "notificationDeliveries":
				continue
			}
			metadataFieldList = append(metadataFieldList, metadataField{index: i, name: name})
		}
	})
	return metadataFieldList
}

// changedMetadataFields compares two in-memory metadata snapshots field by
// field. It costs a walk of each collection but no JSON encoding and no
// database reads, so unchanged collections of any size stay untouched.
func changedMetadataFields(before, after domain.Bootstrap) []metadataField {
	left, right := reflect.ValueOf(&before).Elem(), reflect.ValueOf(&after).Elem()
	changed := []metadataField{}
	for _, field := range persistedMetadataFields() {
		a, b := left.Field(field.index), right.Field(field.index)
		if sameMetadataValue(a, b) {
			continue
		}
		changed = append(changed, field)
	}
	return changed
}

// sameMetadataValue short-circuits collections that still share storage (a
// field the mutation never replaced) before falling back to a deep compare.
func sameMetadataValue(a, b reflect.Value) bool {
	switch a.Kind() {
	case reflect.Slice:
		if a.IsNil() != b.IsNil() || a.Len() != b.Len() {
			return false
		}
		if a.Len() == 0 || a.Pointer() == b.Pointer() {
			return true
		}
	case reflect.Map:
		if a.IsNil() != b.IsNil() || a.Len() != b.Len() {
			return false
		}
		if a.Len() == 0 || a.Pointer() == b.Pointer() {
			return true
		}
	}
	return metadataValueEqual(a, b)
}

// structEqualPlan lists the byte ranges of a struct that a value copy
// reproduces exactly (every field except deep exported fields and json:"-"
// fields), and the deep fields to compare recursively.
type structEqualPlan struct {
	ranges [][2]uintptr
	deep   []int
}

var structEqualPlans sync.Map

func structEqualPlanFor(t reflect.Type) *structEqualPlan {
	if cached, ok := structEqualPlans.Load(t); ok {
		return cached.(*structEqualPlan)
	}
	plan := &structEqualPlan{}
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath == "" {
			if field.Tag.Get("json") == "-" {
				continue
			}
			if !typeCloneFlat(field.Type) {
				plan.deep = append(plan.deep, i)
				continue
			}
		}
		start, end := field.Offset, field.Offset+field.Type.Size()
		if last := len(plan.ranges) - 1; last >= 0 && plan.ranges[last][1] == start {
			plan.ranges[last][1] = end
		} else if end > start {
			plan.ranges = append(plan.ranges, [2]uintptr{start, end})
		}
	}
	actual, _ := structEqualPlans.LoadOrStore(t, plan)
	return actual.(*structEqualPlan)
}

func valueBytes(value reflect.Value, offset, size uintptr) []byte {
	return unsafe.Slice((*byte)(unsafe.Add(unsafe.Pointer(value.UnsafeAddr()), offset)), size)
}

// metadataValueEqual reports whether two metadata values persist the same
// way. Snapshots are value copies of each other, so flat element runs are
// compared as memory and only reference fields are walked; any byte
// difference falls back to reflect.DeepEqual. It never reports different
// values as equal.
func metadataValueEqual(a, b reflect.Value) bool {
	switch a.Kind() {
	case reflect.Slice:
		if a.IsNil() != b.IsNil() || a.Len() != b.Len() {
			return false
		}
		if a.Len() == 0 || a.Pointer() == b.Pointer() {
			return true
		}
		elem := a.Type().Elem()
		if typeCloneFlat(elem) {
			size := elem.Size() * uintptr(a.Len())
			if size > 0 && bytes.Equal(valueBytes(a.Index(0), 0, size), valueBytes(b.Index(0), 0, size)) {
				return true
			}
			return reflect.DeepEqual(a.Interface(), b.Interface())
		}
		for i := 0; i < a.Len(); i++ {
			if !metadataValueEqual(a.Index(i), b.Index(i)) {
				return false
			}
		}
		return true
	case reflect.Struct:
		if !a.CanAddr() || !b.CanAddr() {
			return reflect.DeepEqual(a.Interface(), b.Interface())
		}
		plan := structEqualPlanFor(a.Type())
		for _, span := range plan.ranges {
			if !bytes.Equal(valueBytes(a, span[0], span[1]-span[0]), valueBytes(b, span[0], span[1]-span[0])) {
				return reflect.DeepEqual(a.Interface(), b.Interface())
			}
		}
		for _, index := range plan.deep {
			if !metadataValueEqual(a.Field(index), b.Field(index)) {
				return false
			}
		}
		return true
	case reflect.Map:
		if a.IsNil() != b.IsNil() || a.Len() != b.Len() {
			return false
		}
		if a.Len() == 0 || a.Pointer() == b.Pointer() {
			return true
		}
		if a.Type().Key().Kind() != reflect.String {
			return reflect.DeepEqual(a.Interface(), b.Interface())
		}
		// Per-key compare so slices of structs (project updates by project)
		// take the memory fast path instead of a deep walk.
		iter := a.MapRange()
		for iter.Next() {
			other := b.MapIndex(iter.Key())
			if !other.IsValid() || !metadataValueEqual(addressable(iter.Value()), addressable(other)) {
				return false
			}
		}
		return true
	case reflect.Pointer:
		if a.IsNil() || b.IsNil() {
			return a.IsNil() == b.IsNil()
		}
		return a.Pointer() == b.Pointer() || metadataValueEqual(a.Elem(), b.Elem())
	case reflect.String:
		return a.String() == b.String()
	default:
		return reflect.DeepEqual(a.Interface(), b.Interface())
	}
}

// writeWorkspaceMetadataDelta persists after as the workspace metadata when
// before is the stored state. It produces the same rows and root document as
// writeWorkspaceMetadata(after) but only encodes, reads and rewrites the
// top-level fields that differ. It reports whether anything was written.
func (s *SQLiteStore) writeWorkspaceMetadataDelta(ctx context.Context, tx *sqlTx, workspace string, before, after domain.Bootstrap) (bool, error) {
	before, after = compactImportInputs(collectionMetadata(before)), compactImportInputs(collectionMetadata(after))
	changed := changedMetadataFields(before, after)
	var previousID string
	var previousRoot []byte
	err := tx.QueryRowContext(ctx, `SELECT workspace_id,data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&previousID, &previousRoot)
	if errors.Is(err, sql.ErrNoRows) {
		// No stored document to patch: write the whole snapshot.
		raw, err := s.encodeWorkspaceMetadata(after)
		if err != nil {
			return false, err
		}
		return true, writeWorkspaceMetadata(ctx, tx, workspace, after.Workspace.ID, raw)
	}
	if err != nil {
		return false, err
	}
	if len(changed) == 0 && previousID == after.Workspace.ID {
		return false, nil
	}
	root := map[string]json.RawMessage{}
	if err := json.Unmarshal(previousRoot, &root); err != nil {
		return false, err
	}
	storedShapes := map[string]string{}
	if manifest, ok := root[metadataCollectionsKey]; ok {
		if err := json.Unmarshal(manifest, &storedShapes); err != nil {
			return false, err
		}
	}
	refs := newIssueReferences(domain.Bootstrap{Users: after.Users})
	// Record-backed entity lists whose edit keeps the stored order (in-place
	// edits, removals, additions at either end) are written element by
	// element; everything else goes through the per-field record sync.
	slow := changed[:0:0]
	for _, field := range changed {
		if storedShapes[field.name] == "updates" {
			done, err := writeUpdatesFieldDelta(ctx, tx, workspace, refs, field, reflect.ValueOf(&before).Elem().Field(field.index), reflect.ValueOf(&after).Elem().Field(field.index))
			if err != nil {
				return false, err
			}
			if done {
				continue
			}
		}
		if storedShapes[field.name] == "array" {
			done, err := writeArrayFieldDelta(ctx, tx, workspace, refs, field, reflect.ValueOf(&before).Elem().Field(field.index), reflect.ValueOf(&after).Elem().Field(field.index))
			if err != nil {
				return false, err
			}
			if done {
				continue
			}
		}
		slow = append(slow, field)
	}
	changed = slow
	partial := reflect.New(reflect.TypeFor[domain.Bootstrap]()).Elem()
	prior := reflect.New(reflect.TypeFor[domain.Bootstrap]()).Elem()
	source, original := reflect.ValueOf(&after).Elem(), reflect.ValueOf(&before).Elem()
	names := make([]string, 0, len(changed))
	for _, field := range changed {
		partial.Field(field.index).Set(source.Field(field.index))
		prior.Field(field.index).Set(original.Field(field.index))
		names = append(names, field.name)
	}
	encoded, err := json.Marshal(partial.Interface())
	if err != nil {
		return false, err
	}
	partRoot, partShapes, partRecords, err := splitWorkspaceMetadata(encoded)
	if err != nil {
		return false, err
	}
	// The stored rows of these fields are the before snapshot's records, so
	// the database only needs to supply keys and orders.
	encodedBefore, err := json.Marshal(prior.Interface())
	if err != nil {
		return false, err
	}
	_, _, knownRecords, err := splitWorkspaceMetadata(encodedBefore)
	if err != nil {
		return false, err
	}
	selected := map[string]bool{}
	for _, name := range names {
		selected[name] = true
	}
	wanted := map[metadataRecordKey]metadataRecord{}
	for key, value := range partRecords {
		if selected[key.field] {
			wanted[key] = value
		}
	}
	shapes := map[string]string{}
	for name, shape := range partShapes {
		if selected[name] {
			shapes[name] = shape
		}
	}
	if err := syncMetadataRecords(ctx, tx, workspace, refs, shapes, wanted, names, knownRecords); err != nil {
		return false, err
	}
	for _, name := range names {
		if value, ok := partRoot[name]; ok {
			root[name] = value
		} else {
			delete(root, name)
		}
		if shape, ok := shapes[name]; ok {
			storedShapes[name] = shape
		} else {
			delete(storedShapes, name)
		}
	}
	root[metadataCollectionsKey], err = json.Marshal(storedShapes)
	if err != nil {
		return false, err
	}
	raw, err := json.Marshal(root)
	if err != nil {
		return false, err
	}
	if len(raw) > s.maxStateBytes {
		return false, fmt.Errorf("workspace state exceeds %d bytes", s.maxStateBytes)
	}
	if previousID == after.Workspace.ID && bytes.Equal(previousRoot, raw) {
		return true, nil
	}
	_, err = tx.ExecContext(ctx, `UPDATE workspace_states SET workspace_id=?,data=?,updated_at=? WHERE workspace_key=?`, after.Workspace.ID, raw, time.Now().UTC().Format(time.RFC3339Nano), workspace)
	return true, err
}

// persistMetadataTx writes after's metadata as a delta from before (the
// stored state). ForceFullMutationsForTesting switches it to the full write so
// tests can compare both.
func (s *SQLiteStore) persistMetadataTx(ctx context.Context, tx *sqlTx, workspace string, before, after domain.Bootstrap) error {
	if fullMutationsForced.Load() {
		raw, err := s.encodeWorkspaceMetadata(after)
		if err != nil {
			return err
		}
		return writeWorkspaceMetadata(ctx, tx, workspace, after.Workspace.ID, raw)
	}
	_, err := s.writeWorkspaceMetadataDelta(ctx, tx, workspace, before, after)
	return err
}

// WorkspaceMetadataFields clones only the named top-level metadata fields
// (JSON names) plus the workspace record. Background checks that look at one
// collection (webhooks, loops, integration deliveries) use it instead of
// copying every record in the workspace under the read lock.
func (s *SQLiteStore) WorkspaceMetadataFields(workspace string, fields ...string) (domain.Bootstrap, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if workspace == "" {
		workspace = s.lastWorkspaceKey
	}
	data, ok := s.workspaces[workspace]
	if !ok {
		return domain.Bootstrap{}, false
	}
	result := domain.Bootstrap{Workspace: data.Workspace}
	source, target := reflect.ValueOf(&data).Elem(), reflect.ValueOf(&result).Elem()
	for _, field := range persistedMetadataFields() {
		for _, name := range fields {
			if name == field.name {
				target.Field(field.index).Set(source.Field(field.index))
			}
		}
	}
	return cloneBootstrap(result), true
}

// writeArrayFieldDelta persists a changed record-backed entity list without
// encoding or reading the unchanged entries. It handles lists whose surviving
// entries keep their relative order and whose new entries sit before the first
// or after the last surviving entry; the assigned orders then equal what
// issueCollectionOrder gives the full write. It reports false (and writes
// nothing) for any other shape of change.
func writeArrayFieldDelta(ctx context.Context, tx *sqlTx, workspace string, refs issueReferences, field metadataField, before, after reflect.Value) (bool, error) {
	if before.Kind() != reflect.Slice || before.Type().Elem().Kind() != reflect.Struct || after.IsNil() || before.IsNil() {
		return false, nil
	}
	idField, ok := before.Type().Elem().FieldByName("ID")
	if !ok || idField.Type.Kind() != reflect.String || len(idField.Index) != 1 {
		return false, nil
	}
	idOf := func(list reflect.Value, i int) string { return list.Index(i).Field(idField.Index[0]).String() }
	positions := make(map[string]int, before.Len())
	for i := 0; i < before.Len(); i++ {
		positions[idOf(before, i)] = i
	}
	seen := make(map[string]bool, after.Len())
	first, last, lastPosition, tail := -1, -1, -1, false
	for i := 0; i < after.Len(); i++ {
		id := idOf(after, i)
		if id == "" || len(id) > 191 || seen[id] {
			return false, nil
		}
		seen[id] = true
		position, existed := positions[id]
		if !existed {
			// New entries before the first surviving entry form the head; any
			// after it start the tail.
			tail = tail || first >= 0
			continue
		}
		if tail || position <= lastPosition {
			return false, nil
		}
		if first < 0 {
			first = i
		}
		last, lastPosition = i, position
	}
	if first < 0 {
		return false, nil
	}
	type pending struct {
		index int
		key   metadataRecordKey
	}
	changed := []pending{}
	for i := first; i <= last; i++ {
		if !metadataValueEqual(before.Index(positions[idOf(after, i)]), after.Index(i)) {
			changed = append(changed, pending{i, metadataRecordKey{field.name, idOf(after, i)}})
		}
	}
	lookup := []string{idOf(after, first), idOf(after, last)}
	for _, item := range changed {
		lookup = append(lookup, item.key.key)
	}
	clause, args := bindList("record_key", lookup)
	rows, err := tx.QueryContext(ctx, `SELECT record_key,collection_order FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND `+clause, append([]any{workspace, field.name}, args...)...)
	if err != nil {
		return false, err
	}
	orders := map[string]int{}
	for rows.Next() {
		var key string
		var order int
		if err := rows.Scan(&key, &order); err != nil {
			rows.Close()
			return false, err
		}
		orders[key] = order
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	for _, key := range lookup {
		if _, ok := orders[key]; !ok {
			// The stored rows differ from the snapshot; let the full sync fix it.
			return false, nil
		}
	}
	firstOrder, lastOrder := orders[idOf(after, first)], orders[idOf(after, last)]
	write := func(i, order int, previous []byte) error {
		raw, err := json.Marshal(after.Index(i).Interface())
		if err != nil {
			return err
		}
		if previous != nil && refs.equalDisplayData(previous, raw) {
			return nil
		}
		return writeMetadataRecordRow(ctx, tx, workspace, metadataRecordKey{field.name, idOf(after, i)}, metadataRecord{data: raw, order: order}, previous)
	}
	for i := 0; i < first; i++ {
		if err := write(i, firstOrder-first+i, nil); err != nil {
			return false, err
		}
	}
	for i := last + 1; i < after.Len(); i++ {
		if err := write(i, lastOrder+i-last, nil); err != nil {
			return false, err
		}
	}
	for _, item := range changed {
		previous, err := json.Marshal(before.Index(positions[item.key.key]).Interface())
		if err != nil {
			return false, err
		}
		if err := write(item.index, orders[item.key.key], previous); err != nil {
			return false, err
		}
	}
	for i := 0; i < before.Len(); i++ {
		if id := idOf(before, i); !seen[id] {
			if err := deleteMetadataRecordRow(ctx, tx, workspace, metadataRecordKey{field.name, id}); err != nil {
				return false, err
			}
		}
	}
	return true, nil
}

// addressable returns value itself when it can be addressed (slice elements,
// struct fields) or an addressable copy (map values), so metadataValueEqual
// can compare its memory.
func addressable(value reflect.Value) reflect.Value {
	if value.CanAddr() {
		return value
	}
	copied := reflect.New(value.Type()).Elem()
	copied.Set(value)
	return copied
}

// writeUpdatesFieldDelta persists a changed per-parent update collection
// (projectUpdates, initiativeUpdates: parent id -> updates) by rewriting only
// the parents whose update list changed. Only those parents' updates are
// encoded, and only their stored rows (whose keys derive from the parent and
// update ids) are read, so a comment on one project's update costs the same
// however many updates the workspace holds. It reports false (and writes
// nothing) when the stored rows differ from the before snapshot, leaving the
// full field sync to repair them.
func writeUpdatesFieldDelta(ctx context.Context, tx *sqlTx, workspace string, refs issueReferences, field metadataField, before, after reflect.Value) (bool, error) {
	if before.Kind() != reflect.Map || before.Type().Key().Kind() != reflect.String || before.IsNil() || after.IsNil() {
		return false, nil
	}
	partialBefore, partialAfter := reflect.MakeMap(before.Type()), reflect.MakeMap(after.Type())
	changed := 0
	iter := after.MapRange()
	for iter.Next() {
		previous := before.MapIndex(iter.Key())
		if previous.IsValid() && metadataValueEqual(addressable(previous), addressable(iter.Value())) {
			continue
		}
		partialAfter.SetMapIndex(iter.Key(), iter.Value())
		if previous.IsValid() {
			partialBefore.SetMapIndex(iter.Key(), previous)
		}
		changed++
	}
	iter = before.MapRange()
	for iter.Next() {
		if !after.MapIndex(iter.Key()).IsValid() {
			partialBefore.SetMapIndex(iter.Key(), iter.Value())
			changed++
		}
	}
	if changed == 0 {
		return true, nil
	}
	split := func(value reflect.Value) (map[metadataRecordKey]metadataRecord, bool, error) {
		raw, err := json.Marshal(value.Interface())
		if err != nil {
			return nil, false, err
		}
		var items map[string]json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, false, err
		}
		records, ok := splitUpdateMetadata(field.name, items)
		return records, ok, nil
	}
	wanted, ok, err := split(partialAfter)
	if err != nil || !ok {
		return false, err
	}
	known, ok, err := split(partialBefore)
	if err != nil || !ok {
		return false, err
	}
	lookup := make([]string, 0, len(wanted)+len(known))
	for key := range known {
		lookup = append(lookup, key.key)
	}
	for key := range wanted {
		if _, ok := known[key]; !ok {
			lookup = append(lookup, key.key)
		}
	}
	previous := make(map[metadataRecordKey]metadataRecord, len(known))
	for start := 0; start < len(lookup); start += 500 {
		chunk := lookup[start:min(start+500, len(lookup))]
		clause, args := bindList("record_key", chunk)
		rows, err := tx.QueryContext(ctx, `SELECT record_key,collection_order FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND `+clause, append([]any{workspace, field.name}, args...)...)
		if err != nil {
			return false, err
		}
		for rows.Next() {
			key := metadataRecordKey{field: field.name}
			var value metadataRecord
			if err := rows.Scan(&key.key, &value.order); err != nil {
				rows.Close()
				return false, err
			}
			if stored, ok := known[key]; ok {
				value.data = stored.data
			}
			previous[key] = value
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return false, err
		}
	}
	for key := range known {
		if _, ok := previous[key]; !ok {
			// The stored rows differ from the snapshot; let the full sync fix it.
			return false, nil
		}
	}
	return true, applyMetadataRecords(ctx, tx, workspace, refs, map[string]string{field.name: "updates"}, wanted, previous)
}
