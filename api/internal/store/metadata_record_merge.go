package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"time"

	"flow/api/internal/domain"
)

// Workspace metadata is stored one record per row (a project, a release, a
// user's settings; see splitWorkspaceMetadata). Writers change it from their
// server's in-memory copy, which can be behind the database: issue writes
// (creation, scoped issue writes, comments) run in their own record
// transactions without the workspace lock, and locked writers reload before
// such a transaction on another server commits. Writing a changed record
// whole from that copy would undo what another write changed in it meanwhile
// (a project's name, the progress an issue added, an issue added to a
// release).
//
// So a writer that changes a record first locks its row (rebaseMetadata),
// and when the row no longer matches the copy the change was made on, it
// applies only its own change to the row: the record's top-level fields it
// changed, with lists of ids and of id'd entries merged by entry (its
// additions and removals on top of theirs). The copy's record becomes the row
// as stored, and the written record the merged one, so the rest of the write
// (and the snapshot installed afterwards) carries both changes. updatedAt
// takes the later time. A field both writes changed to different values, or a
// number both moved (a round-robin cursor both advanced from the same value),
// is a conflict: writers that can rerun on fresh records (see
// withStrictMetadataMerge) get ErrConcurrentWrite and rerun; the others keep
// their own value for that field.
//
// Derived values are not merged but recomputed on the locked row: an issue
// write locks the rows of the projects whose progress it changes before it
// computes the progress (rebaseIssueMetadata), so two issue writes in one
// project add up instead of the later one replacing the earlier one's history.
//
// Rows are locked in one order: issue number sequence, issue rows, content
// rows, metadata rows by field then key, then the workspace document.

type strictMetadataMergeKey struct{}

// withStrictMetadataMerge makes metadata merges in ctx report a field both
// writes changed as a conflict instead of keeping the writer's value. Only
// mutations that rerun on ErrConcurrentWrite set it, and only before their
// last attempt.
func withStrictMetadataMerge(ctx context.Context, strict bool) context.Context {
	return context.WithValue(ctx, strictMetadataMergeKey{}, strict)
}

func strictMetadataMerge(ctx context.Context) bool {
	strict, _ := ctx.Value(strictMetadataMergeKey{}).(bool)
	return strict
}

// metadataConflictError is ErrConcurrentWrite for metadata records another
// write changed in the same field. Keys names them, so a writer working from
// its in-memory copy can refresh just those records before it reruns.
type metadataConflictError struct {
	keys []metadataRecordKey
}

func (err *metadataConflictError) Error() string { return ErrConcurrentWrite.Error() }
func (err *metadataConflictError) Unwrap() error { return ErrConcurrentWrite }

// conflictedMetadataKeys returns the records a metadata conflict names.
func conflictedMetadataKeys(err error) ([]metadataRecordKey, bool) {
	var conflict *metadataConflictError
	if errors.As(err, &conflict) {
		return conflict.keys, true
	}
	return nil, false
}

// mergeMetadataRecord applies ours' change to a record (from base, the copy
// it was made on) to theirs, the record as stored now. loaded is theirs as a
// load presents it (see normalizedMetadataRecord): a field counts as changed
// by the other write only where loaded differs from base, so a field the load
// fills in is not taken for another write's change. All are JSON objects
// encoded from the record's type. Fields ours did not change keep theirs'
// stored value. It returns the merged record and whether a field both sides
// changed could not be merged (the merged record then holds ours' value).
func mergeMetadataRecord(base, ours, theirs, loaded []byte) ([]byte, bool, error) {
	var baseFields, ourFields, theirFields, loadedFields map[string]json.RawMessage
	for _, part := range []struct {
		raw    []byte
		fields *map[string]json.RawMessage
	}{{base, &baseFields}, {ours, &ourFields}, {theirs, &theirFields}, {loaded, &loadedFields}} {
		if err := json.Unmarshal(part.raw, part.fields); err != nil {
			return nil, false, err
		}
	}
	merged := make(map[string]json.RawMessage, len(theirFields)+len(ourFields))
	for name, value := range theirFields {
		merged[name] = value
	}
	conflict := false
	names := map[string]bool{}
	for name := range baseFields {
		names[name] = true
	}
	for name := range ourFields {
		names[name] = true
	}
	for name := range names {
		baseValue, ourValue, theirValue := baseFields[name], ourFields[name], loadedFields[name]
		if jsonEqual(baseValue, ourValue) {
			continue
		}
		if jsonEqual(theirValue, baseValue) {
			setMember(merged, name, ourValue)
			continue
		}
		if name == "updatedAt" {
			// Both changed the record: it was updated at the later time.
			var ourTime, theirTime time.Time
			if json.Unmarshal(ourValue, &ourTime) == nil && json.Unmarshal(theirValue, &theirTime) == nil {
				if ourTime.After(theirTime) {
					setMember(merged, name, ourValue)
				}
				continue
			}
		}
		if jsonEqual(theirValue, ourValue) {
			// Both moved a number (a cursor, a count) from the same value:
			// each counted one step, so the result is not their agreement.
			if number := bytes.TrimSpace(ourValue); len(number) > 0 && (number[0] == '-' || number[0] >= '0' && number[0] <= '9') {
				conflict = true
			}
			setMember(merged, name, ourValue)
			continue
		}
		value, ok := mergeMetadataLists(baseValue, ourValue, theirValue)
		if !ok {
			conflict = true
			value = ourValue
		}
		setMember(merged, name, value)
	}
	encoded, err := json.Marshal(merged)
	return encoded, conflict, err
}

// normalizedMetadataRecord returns a stored record of field as loading
// presents it: run through the load normalization (normalize) on its own.
// Normalizations that need the rest of the workspace do not apply; the
// result is only compared, never written.
func normalizedMetadataRecord(field metadataField, key string, record reflect.Value) ([]byte, error) {
	var probe domain.Bootstrap
	target := reflect.ValueOf(&probe).Elem().Field(field.index)
	if target.Kind() == reflect.Map {
		target.Set(reflect.MakeMapWithSize(target.Type(), 1))
		target.SetMapIndex(reflect.ValueOf(key).Convert(target.Type().Key()), record)
	} else {
		target.Set(reflect.Append(reflect.MakeSlice(target.Type(), 0, 1), record))
	}
	normalize(&probe)
	collection := newMetadataCollection(field, reflect.ValueOf(&probe).Elem().Field(field.index))
	if normalized, ok := collection.record(key); ok {
		return json.Marshal(normalized.Interface())
	}
	return json.Marshal(record.Interface())
}

func setMember(fields map[string]json.RawMessage, name string, value json.RawMessage) {
	if value == nil {
		delete(fields, name)
		return
	}
	fields[name] = value
}

func jsonEqual(a, b json.RawMessage) bool {
	if bytes.Equal(a, b) {
		return true
	}
	// An absent field and an empty one are the same record.
	return emptyMember(a) && emptyMember(b)
}

func emptyMember(value json.RawMessage) bool {
	trimmed := bytes.TrimSpace(value)
	return emptyJSONValue(trimmed) || bytes.Equal(trimmed, []byte("[]")) || bytes.Equal(trimmed, []byte("{}"))
}

// mergeMetadataLists merges a list field both sides changed: lists of
// scalars (ids) as sets, lists of objects with an "id" by entry. Each keeps
// theirs' entries and order, drops what ours removed and appends what ours
// added; an entry both changed differently does not merge.
func mergeMetadataLists(base, ours, theirs json.RawMessage) (json.RawMessage, bool) {
	var baseItems, ourItems, theirItems []json.RawMessage
	for _, list := range []struct {
		raw   json.RawMessage
		items *[]json.RawMessage
	}{{base, &baseItems}, {ours, &ourItems}, {theirs, &theirItems}} {
		if emptyJSONValue(list.raw) {
			continue
		}
		if json.Unmarshal(list.raw, list.items) != nil {
			return nil, false
		}
	}
	identity := func(item json.RawMessage) (string, bool) {
		trimmed := bytes.TrimSpace(item)
		if len(trimmed) == 0 {
			return "", false
		}
		switch trimmed[0] {
		case '{':
			var entry struct {
				ID *string `json:"id"`
			}
			if json.Unmarshal(trimmed, &entry) != nil || entry.ID == nil || *entry.ID == "" {
				return "", false
			}
			return "id:" + *entry.ID, true
		case '[':
			return "", false
		default:
			return "value:" + string(trimmed), true
		}
	}
	index := func(items []json.RawMessage) (map[string]json.RawMessage, []string, bool) {
		byID := make(map[string]json.RawMessage, len(items))
		order := make([]string, 0, len(items))
		for _, item := range items {
			id, ok := identity(item)
			if !ok {
				return nil, nil, false
			}
			if _, seen := byID[id]; !seen {
				order = append(order, id)
			}
			byID[id] = item
		}
		return byID, order, true
	}
	baseByID, _, ok := index(baseItems)
	if !ok {
		return nil, false
	}
	ourByID, ourOrder, ok := index(ourItems)
	if !ok {
		return nil, false
	}
	theirByID, theirOrder, ok := index(theirItems)
	if !ok {
		return nil, false
	}
	result := make([]json.RawMessage, 0, len(theirOrder)+len(ourOrder))
	for _, id := range theirOrder {
		baseItem, inBase := baseByID[id]
		ourItem, inOurs := ourByID[id]
		theirItem := theirByID[id]
		switch {
		case inBase && !inOurs:
			// Ours removed it.
			if !jsonEqual(theirItem, baseItem) {
				return nil, false
			}
			continue
		case inOurs && inBase && !jsonEqual(ourItem, baseItem) && !jsonEqual(theirItem, ourItem):
			if !jsonEqual(theirItem, baseItem) {
				return nil, false
			}
			theirItem = ourItem
		}
		result = append(result, theirItem)
	}
	for _, id := range ourOrder {
		if _, inTheirs := theirByID[id]; inTheirs {
			continue
		}
		if _, inBase := baseByID[id]; inBase {
			// Theirs removed it; ours still has it (changed or not).
			if !jsonEqual(ourByID[id], baseByID[id]) {
				return nil, false
			}
			continue
		}
		result = append(result, ourByID[id])
	}
	encoded, err := json.Marshal(result)
	return encoded, err == nil
}

// metadataCollection is a metadata field stored one row per record: a list
// of entities with string ids or a dictionary.
type metadataCollection struct {
	field metadataField
	value reflect.Value
	// ids maps each record key to its index in a list, or to 0 in a map.
	ids map[string]int
}

func (collection metadataCollection) isMap() bool { return collection.value.Kind() == reflect.Map }

// rebaseableMetadataField reports whether field's records can be rebased: a
// slice of structs with a string ID, or a map with string keys. Project and
// initiative updates (a dictionary of lists) and import jobs (compacted on
// write) are only written under the workspace lock and keep whole-record
// writes.
func rebaseableMetadataField(field metadataField, kind reflect.Type) bool {
	switch field.name {
	case "projectUpdates", "initiativeUpdates", "importJobs", "workspace", "viewer":
		return false
	}
	switch kind.Kind() {
	case reflect.Slice:
		elem := kind.Elem()
		if elem.Kind() != reflect.Struct {
			return false
		}
		id, ok := elem.FieldByName("ID")
		return ok && id.Type.Kind() == reflect.String && len(id.Index) == 1
	case reflect.Map:
		return kind.Key().Kind() == reflect.String
	}
	return false
}

func newMetadataCollection(field metadataField, value reflect.Value) metadataCollection {
	collection := metadataCollection{field: field, value: value, ids: map[string]int{}}
	if value.Kind() == reflect.Map {
		iter := value.MapRange()
		for iter.Next() {
			collection.ids[iter.Key().String()] = 0
		}
		return collection
	}
	idIndex := idFieldIndex(value.Type().Elem())
	for i := 0; i < value.Len(); i++ {
		id := value.Index(i).Field(idIndex).String()
		if _, seen := collection.ids[id]; !seen {
			collection.ids[id] = i
		}
	}
	return collection
}

func idFieldIndex(elem reflect.Type) int {
	field, _ := elem.FieldByName("ID")
	return field.Index[0]
}

// record returns the record with key (an addressable copy for maps).
func (collection metadataCollection) record(key string) (reflect.Value, bool) {
	if _, ok := collection.ids[key]; !ok {
		return reflect.Value{}, false
	}
	if collection.isMap() {
		return addressable(collection.value.MapIndex(reflect.ValueOf(key).Convert(collection.value.Type().Key()))), true
	}
	return collection.value.Index(collection.ids[key]), true
}

// set stores value as the record with key, which the collection holds.
func (collection metadataCollection) set(key string, value reflect.Value) {
	if collection.isMap() {
		collection.value.SetMapIndex(reflect.ValueOf(key).Convert(collection.value.Type().Key()), value)
		return
	}
	collection.value.Index(collection.ids[key]).Set(value)
}

// remove deletes the record with key and returns the collection without it.
func (collection metadataCollection) remove(key string) reflect.Value {
	if _, ok := collection.ids[key]; !ok {
		return collection.value
	}
	if collection.isMap() {
		collection.value.SetMapIndex(reflect.ValueOf(key).Convert(collection.value.Type().Key()), reflect.Value{})
		return collection.value
	}
	index, length := collection.ids[key], collection.value.Len()
	kept := reflect.MakeSlice(collection.value.Type(), 0, length-1)
	kept = reflect.AppendSlice(kept, collection.value.Slice(0, index))
	return reflect.AppendSlice(kept, collection.value.Slice(index+1, length))
}

// cloneMetadataContainer returns a shallow copy of a slice or map, so its
// records can be replaced without touching a snapshot readers share.
func cloneMetadataContainer(value reflect.Value) reflect.Value {
	if value.Kind() == reflect.Map {
		if value.IsNil() {
			return value
		}
		copied := reflect.MakeMapWithSize(value.Type(), value.Len())
		iter := value.MapRange()
		for iter.Next() {
			copied.SetMapIndex(iter.Key(), iter.Value())
		}
		return copied
	}
	if value.IsNil() {
		return value
	}
	copied := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
	reflect.Copy(copied, value)
	return copied
}

func sameContainer(a, b reflect.Value) bool {
	if a.Kind() != b.Kind() || a.IsNil() || b.IsNil() {
		return false
	}
	return a.Pointer() == b.Pointer()
}

// decodeMetadataRecord decodes raw as a record of type kind. Fields the
// record does not persist (json:"-") are taken from keep when it is valid.
func decodeMetadataRecord(kind reflect.Type, raw []byte, keep reflect.Value) (reflect.Value, error) {
	value := reflect.New(kind)
	if err := json.Unmarshal(raw, value.Interface()); err != nil {
		return reflect.Value{}, err
	}
	value = value.Elem()
	if keep.IsValid() && kind.Kind() == reflect.Struct {
		for i := 0; i < kind.NumField(); i++ {
			field := kind.Field(i)
			if field.PkgPath == "" && field.Tag.Get("json") == "-" {
				value.Field(i).Set(keep.Field(i))
			}
		}
	}
	return value, nil
}

func metadataFieldByName(name string) (metadataField, bool) {
	for _, field := range persistedMetadataFields() {
		if field.name == name {
			return field, true
		}
	}
	return metadataField{}, false
}

// rebaseMetadata locks the stored rows of the records after changes from
// before (plus the extra records named by field and key) and, for each row
// another write changed since before was read, makes before's record the
// stored one and after's the merge of both changes (see mergeMetadataRecord).
// A row deleted meanwhile is left out of both, so the write does not bring it
// back. Rows are locked in one order (field, then key) for every writer. It
// changes after's collections in place when they are its own and replaces
// before's. changed lists the fields after changes (nil: compare them here).
// It returns the fields whose collection it replaced in after, and whether
// it changed before or after at all.
func (s *SQLiteStore) rebaseMetadata(ctx context.Context, tx *sqlTx, workspace string, before, after *domain.Bootstrap, changed []metadataField, extra map[string][]string) ([]metadataField, bool, error) {
	if changed == nil {
		changed = changedMetadataFields(*before, *after)
	}
	touched := false
	fields := map[string]metadataField{}
	for _, field := range changed {
		fields[field.name] = field
	}
	for name := range extra {
		if field, ok := metadataFieldByName(name); ok {
			fields[name] = field
		}
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	beforeRoot, afterRoot := reflect.ValueOf(before).Elem(), reflect.ValueOf(after).Elem()
	var refs *issueReferences
	replaced := []metadataField{}
	var conflicts []metadataRecordKey
	strict := strictMetadataMerge(ctx)
	for _, name := range names {
		field := fields[name]
		beforeValue, afterValue := beforeRoot.Field(field.index), afterRoot.Field(field.index)
		if !rebaseableMetadataField(field, beforeValue.Type()) {
			continue
		}
		previous, next := newMetadataCollection(field, beforeValue), newMetadataCollection(field, afterValue)
		keys := []string{}
		for key := range next.ids {
			if tx.rebasedMetadata[metadataRecordKey{name, key}] {
				continue
			}
			old, existed := previous.record(key)
			if !existed {
				continue
			}
			current, _ := next.record(key)
			if !metadataValueEqual(old, current) {
				keys = append(keys, key)
			}
		}
		for _, key := range extra[name] {
			_, inBefore := previous.ids[key]
			_, inAfter := next.ids[key]
			if inBefore && inAfter && !tx.rebasedMetadata[metadataRecordKey{name, key}] && !slices.Contains(keys, key) {
				keys = append(keys, key)
			}
		}
		if len(keys) == 0 {
			continue
		}
		sort.Strings(keys)
		stored, err := lockMetadataRecords(ctx, tx, workspace, name, keys)
		if err != nil {
			return nil, false, err
		}
		if refs == nil {
			current := newIssueReferences(domain.Bootstrap{Users: after.Users})
			refs = &current
		}
		// after's collection is changed in place when it is the mutation's
		// own; one it shares with before (readers' snapshot) is copied first.
		ownAfter := !sameContainer(beforeValue, afterValue)
		clonedBefore, replacedAfter := false, false
		kind := beforeValue.Type().Elem()
		for _, key := range keys {
			recordKey := metadataRecordKey{name, key}
			if tx.rebasedMetadata == nil {
				tx.rebasedMetadata = map[metadataRecordKey]bool{}
			}
			tx.rebasedMetadata[recordKey] = true
			old, _ := previous.record(key)
			mine, _ := next.record(key)
			base, err := json.Marshal(old.Interface())
			if err != nil {
				return nil, false, err
			}
			row, exists := stored[key]
			if exists && storedRecordEqual(tx, *refs, name, row, base) {
				continue
			}
			touched = true
			if !clonedBefore {
				beforeValue.Set(cloneMetadataContainer(beforeValue))
				previous = newMetadataCollection(field, beforeValue)
				clonedBefore = true
			}
			if !ownAfter && !replacedAfter {
				afterValue.Set(cloneMetadataContainer(afterValue))
				next = newMetadataCollection(field, afterValue)
				replacedAfter = true
			}
			if !exists {
				// Deleted by another write: ours does not bring it back.
				beforeValue.Set(previous.remove(key))
				previous = newMetadataCollection(field, beforeValue)
				afterValue.Set(next.remove(key))
				next = newMetadataCollection(field, afterValue)
				replacedAfter = true
				continue
			}
			resolved, err := resolveDocumentMarkers(name, row)
			if err != nil {
				return nil, false, err
			}
			storedValue, err := decodeMetadataRecord(kind, resolved, reflect.Value{})
			if err != nil {
				return nil, false, fmt.Errorf("rebase %s record %s: %w", name, key, err)
			}
			theirs, err := json.Marshal(storedValue.Interface())
			if err != nil {
				return nil, false, err
			}
			loaded, err := normalizedMetadataRecord(field, key, storedValue)
			if err != nil {
				return nil, false, err
			}
			loaded = refs.canonicalDisplayData(loaded)
			if refs.equalDisplayData(loaded, base) {
				// The row only lacks what the load fills in: no other write
				// changed it.
				continue
			}
			ours, err := json.Marshal(mine.Interface())
			if err != nil {
				return nil, false, err
			}
			merged, conflict, err := mergeMetadataRecord(base, ours, theirs, loaded)
			if err != nil {
				return nil, false, err
			}
			if conflict && strict {
				conflicts = append(conflicts, recordKey)
				continue
			}
			theirValue, err := decodeMetadataRecord(kind, theirs, old)
			if err != nil {
				return nil, false, err
			}
			mergedValue, err := decodeMetadataRecord(kind, merged, mine)
			if err != nil {
				return nil, false, err
			}
			previous.set(key, theirValue)
			next.set(key, mergedValue)
		}
		if replacedAfter {
			replaced = append(replaced, field)
		}
	}
	if len(conflicts) > 0 {
		return nil, false, &metadataConflictError{keys: conflicts}
	}
	return replaced, touched, nil
}

// lockMetadataRecords reads (and on MySQL and PostgreSQL locks) the stored
// rows of one field's records, in key order.
func lockMetadataRecords(ctx context.Context, tx *sqlTx, workspace, field string, keys []string) (map[string][]byte, error) {
	lock := ""
	if tx.dialect != "sqlite" {
		lock = " FOR UPDATE"
	}
	stored := make(map[string][]byte, len(keys))
	for start := 0; start < len(keys); start += 500 {
		clause, args := bindList("record_key", keys[start:min(start+500, len(keys))])
		rows, err := tx.QueryContext(ctx, `SELECT record_key,data FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND `+clause+` ORDER BY record_key`+lock, append([]any{workspace, field}, args...)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var key string
			var raw []byte
			if err := rows.Scan(&key, &raw); err != nil {
				rows.Close()
				return nil, err
			}
			stored[key] = raw
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return stored, nil
}

// refreshMetadataRecords replaces the named records of the workspace's
// in-memory copy with the stored ones (dropping those no longer stored), so a
// write that conflicted on them reruns on what the database holds.
func (s *SQLiteStore) refreshMetadataRecords(ctx context.Context, workspace string, keys []metadataRecordKey) error {
	byField := map[string][]string{}
	for _, key := range keys {
		byField[key.field] = append(byField[key.field], key.key)
	}
	type storedRecords struct {
		field metadataField
		rows  map[string][]byte
		keys  []string
	}
	loaded := []storedRecords{}
	for name, ids := range byField {
		field, ok := metadataFieldByName(name)
		if !ok {
			continue
		}
		rows := map[string][]byte{}
		for start := 0; start < len(ids); start += 500 {
			clause, args := bindList("record_key", ids[start:min(start+500, len(ids))])
			result, err := s.db.QueryContext(ctx, `SELECT record_key,data FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND `+clause, append([]any{workspace, name}, args...)...)
			if err != nil {
				return err
			}
			for result.Next() {
				var key string
				var raw []byte
				if err := result.Scan(&key, &raw); err != nil {
					result.Close()
					return err
				}
				if raw, err = resolveDocumentMarkers(name, raw); err != nil {
					result.Close()
					return err
				}
				rows[key] = raw
			}
			err = result.Err()
			result.Close()
			if err != nil {
				return err
			}
		}
		loaded = append(loaded, storedRecords{field, rows, ids})
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	stored, ok := s.storedWorkspace(workspace)
	if !ok {
		return nil
	}
	root := reflect.ValueOf(&stored).Elem()
	for _, records := range loaded {
		value := root.Field(records.field.index)
		if !rebaseableMetadataField(records.field, value.Type()) {
			continue
		}
		value.Set(cloneMetadataContainer(value))
		if value.Kind() == reflect.Map && value.IsNil() {
			value.Set(reflect.MakeMap(value.Type()))
		}
		kind := value.Type().Elem()
		for _, key := range records.keys {
			collection := newMetadataCollection(records.field, value)
			raw, exists := records.rows[key]
			if !exists {
				value.Set(collection.remove(key))
				continue
			}
			old, _ := collection.record(key)
			record, err := decodeMetadataRecord(kind, raw, old)
			if err != nil {
				return err
			}
			if _, held := collection.ids[key]; held {
				collection.set(key, record)
			} else if value.Kind() == reflect.Map {
				value.SetMapIndex(reflect.ValueOf(key).Convert(value.Type().Key()), record)
			} else {
				value.Set(reflect.Append(value, record))
			}
		}
	}
	s.installWorkspace(workspace, stored, false)
	return nil
}

// mutationAttempts runs attempt until it succeeds, fails with something
// other than a concurrent write, or runs out of attempts. Each attempt but
// the last reports conflicting metadata fields (withStrictMetadataMerge);
// before rerunning, refresh brings the conflicting records up to date.
func mutationAttempts(ctx context.Context, attempts int, attempt func(context.Context) error, refresh func(error) error) error {
	var err error
	for number := 1; ; number++ {
		err = attempt(withStrictMetadataMerge(ctx, number < attempts))
		if !errors.Is(err, ErrConcurrentWrite) || number >= attempts {
			return err
		}
		if refresh != nil {
			if refreshErr := refresh(err); refreshErr != nil {
				return refreshErr
			}
		}
		traceMark(ctx, "retry")
	}
}

// recordWriteTxOptions is the isolation of record transactions that write
// metadata (issue creation, scoped issue writes). On MySQL they read
// committed rows, as PostgreSQL does by default: a project's progress rebuilt
// in the transaction must count the issue rows another server committed
// after it began. The rows these transactions change they lock.
var recordWriteTxOptions = &sql.TxOptions{Isolation: sql.LevelReadCommitted}

func (s *SQLiteStore) recordTxOptions() *sql.TxOptions {
	if s.dialect == "mysql" {
		return recordWriteTxOptions
	}
	return nil
}
