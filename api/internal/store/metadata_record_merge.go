package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"reflect"
	"slices"
	"sort"
	"strconv"
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
// changed, with nested objects merged key by key, lists of ids merged as sets
// and lists of id'd entries merged by entry (its additions and removals on
// top of theirs; an entry both changed merges key by key). The copy's record
// becomes the row as stored, and the written record the merged one, so the
// rest of the write (and the snapshot installed afterwards) carries both
// changes. updatedAt takes the later time; additive counters apply deltas.
// A value both writes changed differently where they cannot be combined (a
// string, a round-robin cursor both advanced from the same value, a list without
// ids) is a conflict: the write fails with ErrConcurrentWrite, and every
// writer reruns on fresh records (concurrentWriteAttempts, with jittered
// backoff) before it reports the 409. No writer keeps its own value over
// another's.
//
// What still merges as a whole: scalars (strings, numbers, booleans), lists
// whose entries have no "id" (progress history points, lists of lists), and
// an entry or value one side removed while the other changed it (a
// conflict). A record another write deleted is not brought back.
//
// Writers that replace records whole from a copy (team writers, user
// settings, team deletion) go through the same merge per record
// (rebaseMetadataRecordWrite), as do project and initiative updates, which
// are stored one update per row.
//
// Derived values are not merged but recomputed on the locked row: an issue
// write locks the rows of the projects whose progress it changes before it
// computes the progress (rebaseIssueMetadata), so two issue writes in one
// project add up instead of the later one replacing the earlier one's history.
// Full mutations do the same inside their transaction (persistWorkspaceTx).
//
// Rows are locked in one order: issue number sequence, issue rows, content
// rows, metadata rows by field then key, then the workspace document.

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
		value, ok := mergeMetadataMember(name, baseValue, ourValue, theirValue)
		if !ok {
			conflict = true
			value = ourValue
		}
		setMember(merged, name, value)
	}
	encoded, err := json.Marshal(merged)
	return encoded, conflict, err
}

// Only additive counters use deltas. Positions, priorities, derived totals
// and round-robin cursors (which also choose an assignee) remain scalar CAS.
func mergeMetadataMember(name string, base, ours, theirs json.RawMessage) (json.RawMessage, bool) {
	switch name {
	case "updatedAt":
		var mine, their time.Time
		if json.Unmarshal(ours, &mine) == nil && json.Unmarshal(theirs, &their) == nil {
			if mine.After(their) {
				return ours, true
			}
			return theirs, true
		}
	case "imported", "retryCount", "occurrenceCount", "version", "personalSettingsVersion":
		count := func(raw json.RawMessage) (int64, error) {
			if emptyMember(raw) {
				return 0, nil
			}
			return strconv.ParseInt(string(raw), 10, 64)
		}
		before, baseErr := count(base)
		mine, ourErr := count(ours)
		their, theirErr := count(theirs)
		if baseErr == nil && ourErr == nil && theirErr == nil && before >= 0 && mine >= before && their >= before {
			delta := mine - before
			if their <= math.MaxInt64-delta {
				return json.RawMessage(strconv.FormatInt(their+delta, 10)), true
			}
		}
	}
	return mergeMetadataValues(base, ours, theirs)
}

// mergeMetadataValues merges a value both sides changed from base: objects
// key by key (recursively), lists by entry (mergeMetadataLists). Anything
// else, and a number both moved to the same value (each counted one step from
// base, so the result is not their agreement), does not merge.
func mergeMetadataValues(base, ours, theirs json.RawMessage) (json.RawMessage, bool) {
	if jsonEqual(ours, base) {
		return theirs, true
	}
	if jsonEqual(theirs, base) {
		return ours, true
	}
	switch jsonKind(ours) | jsonKind(theirs) | jsonKind(base) {
	case jsonObject, jsonObject | jsonAbsent:
		return mergeMetadataObjects(base, ours, theirs)
	case jsonArray, jsonArray | jsonAbsent:
		return mergeMetadataLists(base, ours, theirs)
	}
	if jsonEqual(theirs, ours) {
		if number := bytes.TrimSpace(ours); len(number) > 0 && (number[0] == '-' || number[0] >= '0' && number[0] <= '9') {
			return nil, false
		}
		return ours, true
	}
	return nil, false
}

const (
	jsonAbsent = 1 << iota
	jsonObject
	jsonArray
	jsonScalar
)

// jsonKind classifies a member value; an absent, null or empty value merges
// with either container shape.
func jsonKind(value json.RawMessage) int {
	trimmed := bytes.TrimSpace(value)
	switch {
	case emptyJSONValue(trimmed), bytes.Equal(trimmed, []byte("{}")), bytes.Equal(trimmed, []byte("[]")):
		return jsonAbsent
	case trimmed[0] == '{':
		return jsonObject
	case trimmed[0] == '[':
		return jsonArray
	}
	return jsonScalar
}

// mergeMetadataObjects merges two changes to a JSON object key by key: keys
// one side added, removed or changed apply on top of the other's, and a key
// both changed merges its values (mergeMetadataValues).
func mergeMetadataObjects(base, ours, theirs json.RawMessage) (json.RawMessage, bool) {
	var baseKeys, ourKeys, theirKeys map[string]json.RawMessage
	for _, part := range []struct {
		raw  json.RawMessage
		keys *map[string]json.RawMessage
	}{{base, &baseKeys}, {ours, &ourKeys}, {theirs, &theirKeys}} {
		if emptyJSONValue(bytes.TrimSpace(part.raw)) {
			continue
		}
		if json.Unmarshal(part.raw, part.keys) != nil {
			return nil, false
		}
	}
	merged := make(map[string]json.RawMessage, len(theirKeys)+len(ourKeys))
	for key, value := range theirKeys {
		merged[key] = value
	}
	keys := map[string]bool{}
	for key := range baseKeys {
		keys[key] = true
	}
	for key := range ourKeys {
		keys[key] = true
	}
	for key := range keys {
		baseValue, ourValue, theirValue := baseKeys[key], ourKeys[key], theirKeys[key]
		_, inBase := baseKeys[key]
		_, inOurs := ourKeys[key]
		_, inTheirs := theirKeys[key]
		switch {
		case inBase && jsonEqual(baseValue, ourValue):
			continue
		case inBase && !inOurs:
			// An absent membership list is an empty set, not a scalar
			// deletion: removing its last member keeps a peer's additions.
			if jsonKind(baseValue) == jsonArray && jsonKind(theirValue) == jsonArray {
				value, ok := mergeMetadataLists(baseValue, nil, theirValue)
				if !ok {
					return nil, false
				}
				merged[key] = value
				continue
			}
			if inTheirs && !bytes.Equal(bytes.TrimSpace(theirValue), bytes.TrimSpace(baseValue)) {
				return nil, false
			}
			delete(merged, key)
			continue
		case inBase && !inTheirs:
			if jsonKind(baseValue) == jsonArray && jsonKind(ourValue) == jsonArray {
				value, ok := mergeMetadataLists(baseValue, ourValue, nil)
				if !ok {
					return nil, false
				}
				merged[key] = value
				continue
			}
			return nil, false
		}
		value, ok := mergeMetadataMember(key, baseValue, ourValue, theirValue)
		if !ok {
			return nil, false
		}
		merged[key] = value
	}
	encoded, err := json.Marshal(merged)
	return encoded, err == nil
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
		case inOurs && inBase && !jsonEqual(ourItem, baseItem):
			if jsonEqual(theirItem, baseItem) {
				theirItem = ourItem
				break
			}
			// Both changed the entry: merge it key by key.
			merged, ok := mergeMetadataValues(baseItem, ourItem, theirItem)
			if !ok {
				return nil, false
			}
			theirItem = merged
		case inOurs && !inBase && !jsonEqual(ourItem, theirItem):
			// Both added an entry with this id: merge the two.
			merged, ok := mergeMetadataValues(nil, ourItem, theirItem)
			if !ok {
				return nil, false
			}
			theirItem = merged
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
// initiative updates (a dictionary of lists) are rebased per update
// (rebaseUpdateRecords). Import and migration jobs are rebased as stored,
// without their inputs (compactImportInputs), which stay as the writer has
// them (keepImportInputs).
func rebaseableMetadataField(field metadataField, kind reflect.Type) bool {
	switch field.name {
	case "projectUpdates", "initiativeUpdates", "workspace", "viewer":
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
	for _, name := range names {
		field := fields[name]
		beforeValue, afterValue := beforeRoot.Field(field.index), afterRoot.Field(field.index)
		if updateMetadataField(field.name) {
			if refs == nil {
				current := newIssueReferences(domain.Bootstrap{Users: after.Users})
				refs = &current
			}
			result, err := rebaseUpdateRecords(ctx, tx, workspace, *refs, field, beforeValue, afterValue)
			if err != nil {
				return nil, false, err
			}
			conflicts = append(conflicts, result.conflicts...)
			touched = touched || result.touched
			if result.replacedAfter {
				replaced = append(replaced, field)
			}
			continue
		}
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
			ours, err := json.Marshal(mine.Interface())
			if err != nil {
				return nil, false, err
			}
			result, err := mergeWithStoredRecord(*refs, field, kind, key, base, ours, row)
			if err != nil {
				return nil, false, err
			}
			if result.unchanged {
				// The row only lacks what the load fills in: no other write
				// changed it.
				continue
			}
			if result.conflict {
				conflicts = append(conflicts, recordKey)
				continue
			}
			theirs, merged := result.theirs, result.merged
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

// updateMetadataField reports a per-parent update collection (parent id ->
// updates), stored one update per row (splitUpdateMetadata).
func updateMetadataField(name string) bool {
	return name == "projectUpdates" || name == "initiativeUpdates"
}

// updateRecordKey is the row key of one update of parent.
func updateRecordKey(parent, id string) string {
	key, _ := json.Marshal([]string{parent, id})
	return fmt.Sprintf("%x", sha256.Sum256(key))
}

type updateRebase struct {
	conflicts     []metadataRecordKey
	touched       bool
	replacedAfter bool
}

// rebaseUpdateRecords is rebaseMetadata for a per-parent update collection
// (a project's or initiative's status updates): every update after changed
// from before is a record of its own. It locks their rows and, where another
// write changed one since before was read (a comment, a reaction, an edit),
// makes before's update the stored one and after's the merge of both
// changes; an update deleted meanwhile is left out of both. Updates another
// write added are rows the delta writer does not touch, so they are kept.
func rebaseUpdateRecords(ctx context.Context, tx *sqlTx, workspace string, refs issueReferences, field metadataField, beforeValue, afterValue reflect.Value) (updateRebase, error) {
	var result updateRebase
	if beforeValue.Kind() != reflect.Map || afterValue.Kind() != reflect.Map || beforeValue.IsNil() || afterValue.IsNil() {
		return result, nil
	}
	listType := afterValue.Type().Elem()
	if listType.Kind() != reflect.Slice || listType.Elem().Kind() != reflect.Struct {
		return result, nil
	}
	kind := listType.Elem()
	idIndex := idFieldIndex(kind)
	type position struct {
		parent        string
		before, after int
	}
	positions := map[string]position{}
	keys := []string{}
	iter := afterValue.MapRange()
	for iter.Next() {
		parent := iter.Key().String()
		previous := beforeValue.MapIndex(iter.Key())
		if !previous.IsValid() || metadataValueEqual(addressable(previous), addressable(iter.Value())) {
			continue
		}
		index := map[string]int{}
		for i := 0; i < previous.Len(); i++ {
			index[previous.Index(i).Field(idIndex).String()] = i
		}
		list := iter.Value()
		for i := 0; i < list.Len(); i++ {
			id := list.Index(i).Field(idIndex).String()
			old, existed := index[id]
			if !existed || metadataValueEqual(addressable(previous.Index(old)), addressable(list.Index(i))) {
				continue
			}
			key := updateRecordKey(parent, id)
			if tx.rebasedMetadata[metadataRecordKey{field.name, key}] {
				continue
			}
			positions[key] = position{parent, old, i}
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return result, nil
	}
	sort.Strings(keys)
	stored, err := lockMetadataRecords(ctx, tx, workspace, field.name, keys)
	if err != nil {
		return result, err
	}
	if tx.rebasedMetadata == nil {
		tx.rebasedMetadata = map[metadataRecordKey]bool{}
	}
	// Lists are replaced, never changed in place: before's are readers'
	// snapshot, and after's may share their backing arrays.
	beforeLists, afterLists := map[string]reflect.Value{}, map[string]reflect.Value{}
	list := func(lists map[string]reflect.Value, value reflect.Value, parent string) reflect.Value {
		if copied, ok := lists[parent]; ok {
			return copied
		}
		original := value.MapIndex(reflect.ValueOf(parent).Convert(value.Type().Key()))
		copied := reflect.MakeSlice(original.Type(), original.Len(), original.Len())
		reflect.Copy(copied, original)
		lists[parent] = copied
		return copied
	}
	removed := map[string]map[string]bool{}
	for _, key := range keys {
		tx.rebasedMetadata[metadataRecordKey{field.name, key}] = true
		at := positions[key]
		old := beforeValue.MapIndex(reflect.ValueOf(at.parent).Convert(beforeValue.Type().Key())).Index(at.before)
		mine := afterValue.MapIndex(reflect.ValueOf(at.parent).Convert(afterValue.Type().Key())).Index(at.after)
		id := old.Field(idIndex).String()
		row, exists := stored[key]
		if !exists {
			// Deleted by another write: ours does not bring it back.
			if removed[at.parent] == nil {
				removed[at.parent] = map[string]bool{}
			}
			removed[at.parent][id] = true
			result.touched = true
			continue
		}
		var update metadataUpdate
		if err := json.Unmarshal(row, &update); err != nil {
			return result, err
		}
		base, err := json.Marshal(old.Interface())
		if err != nil {
			return result, err
		}
		if len(update.Item) == 0 || refs.equalDisplayData(update.Item, base) {
			continue
		}
		storedValue, err := decodeMetadataRecord(kind, update.Item, old)
		if err != nil {
			return result, fmt.Errorf("rebase %s record %s: %w", field.name, key, err)
		}
		theirs, err := json.Marshal(storedValue.Interface())
		if err != nil {
			return result, err
		}
		if refs.equalDisplayData(refs.canonicalDisplayData(theirs), base) {
			continue
		}
		ours, err := json.Marshal(mine.Interface())
		if err != nil {
			return result, err
		}
		merged, conflict, err := mergeMetadataRecord(base, ours, theirs, theirs)
		if err != nil {
			return result, err
		}
		if conflict {
			result.conflicts = append(result.conflicts, metadataRecordKey{field.name, key})
			continue
		}
		mergedValue, err := decodeMetadataRecord(kind, merged, mine)
		if err != nil {
			return result, err
		}
		result.touched = true
		list(beforeLists, beforeValue, at.parent).Index(at.before).Set(storedValue)
		list(afterLists, afterValue, at.parent).Index(at.after).Set(mergedValue)
	}
	if !result.touched {
		return result, nil
	}
	drop := func(lists map[string]reflect.Value, value reflect.Value) {
		for parent, ids := range removed {
			current := list(lists, value, parent)
			kept := reflect.MakeSlice(current.Type(), 0, current.Len())
			for i := 0; i < current.Len(); i++ {
				if !ids[current.Index(i).Field(idIndex).String()] {
					kept = reflect.Append(kept, current.Index(i))
				}
			}
			lists[parent] = kept
		}
	}
	drop(beforeLists, beforeValue)
	drop(afterLists, afterValue)
	install := func(lists map[string]reflect.Value, value reflect.Value) {
		copied := cloneMetadataContainer(value)
		for parent, items := range lists {
			copied.SetMapIndex(reflect.ValueOf(parent).Convert(value.Type().Key()), items)
		}
		value.Set(copied)
	}
	install(beforeLists, beforeValue)
	install(afterLists, afterValue)
	result.replacedAfter = true
	return result, nil
}

// storedMerge is a writer's change merged onto a record as stored.
type storedMerge struct {
	// theirs is the stored record re-encoded from its type; merged is ours'
	// change applied to it.
	theirs, merged []byte
	// unchanged: no other write changed the record since base.
	unchanged bool
	// conflict: both changed a value that does not merge.
	conflict bool
}

// mergeWithStoredRecord compares row (a record of field as stored) with base
// (the record the writer changed into ours) and, when another write changed
// the row since, merges both changes (mergeMetadataRecord).
func mergeWithStoredRecord(refs issueReferences, field metadataField, kind reflect.Type, key string, base, ours, row []byte) (storedMerge, error) {
	resolved, err := resolveDocumentMarkers(field.name, row)
	if err != nil {
		return storedMerge{}, err
	}
	storedValue, err := decodeMetadataRecord(kind, resolved, reflect.Value{})
	if err != nil {
		return storedMerge{}, fmt.Errorf("rebase %s record %s: %w", field.name, key, err)
	}
	theirs, err := json.Marshal(storedValue.Interface())
	if err != nil {
		return storedMerge{}, err
	}
	loaded, err := normalizedMetadataRecord(field, key, storedValue)
	if err != nil {
		return storedMerge{}, err
	}
	loaded = refs.canonicalDisplayData(loaded)
	if refs.equalDisplayData(loaded, base) {
		return storedMerge{theirs: theirs, merged: ours, unchanged: true}, nil
	}
	merged, conflict, err := mergeMetadataRecord(base, ours, theirs, loaded)
	if err != nil {
		return storedMerge{}, err
	}
	return storedMerge{theirs: theirs, merged: merged, conflict: conflict}, nil
}

// keepImportInputs gives an import or migration job decoded from its stored
// row (which lacks the job's input rows or bundle, stored apart; see
// compactImportInputs) the input of old, the copy's record.
func keepImportInputs(field string, record, old reflect.Value) {
	if !old.IsValid() || !record.CanSet() {
		return
	}
	var input string
	switch field {
	case "importJobs":
		input = "Rows"
	case "migrationJobs":
		input = "Bundle"
	default:
		return
	}
	if value := record.FieldByName(input); value.IsValid() && value.IsNil() {
		value.Set(old.FieldByName(input))
	}
}

// metadataRecordType returns the record type of a record-backed metadata
// field (the element of a list, the value of a dictionary) and whether its
// records merge field by field.
func metadataRecordType(name string) (metadataField, reflect.Type, bool) {
	field, ok := metadataFieldByName(name)
	if !ok {
		return field, nil, false
	}
	container := reflect.TypeFor[domain.Bootstrap]().Field(field.index).Type
	if !rebaseableMetadataField(field, container) {
		return field, nil, false
	}
	return field, container.Elem(), true
}

// rebaseMetadataRecordWrite prepares a whole-record write (ours) of a writer
// that read the record as base (nil: it saw no record) from its copy. It
// locks the record's row and returns what to write: ours when the row is
// still base, ours' change merged onto the row when another write changed it
// since, and write false when another write deleted it meanwhile (the write
// must not bring it back). A value both changed that does not merge, and a
// record both created differently, is a conflict (*metadataConflictError).
func rebaseMetadataRecordWrite(ctx context.Context, tx *sqlTx, workspace string, refs issueReferences, name, key string, base, ours []byte) (result []byte, write bool, err error) {
	field, kind, ok := metadataRecordType(name)
	if !ok {
		return ours, true, nil
	}
	stored, err := lockMetadataRecords(ctx, tx, workspace, name, []string{key})
	if err != nil {
		return nil, false, err
	}
	row, exists := stored[key]
	conflict := &metadataConflictError{keys: []metadataRecordKey{{name, key}}}
	switch {
	case !exists && base != nil:
		return nil, false, nil
	case !exists:
		return ours, true, nil
	case base == nil:
		if storedRecordEqual(tx, refs, name, row, ours) {
			return ours, true, nil
		}
		// Created on both sides: merge the two records from nothing.
		result, err := mergeWithStoredRecord(refs, field, kind, key, []byte("{}"), ours, row)
		if err != nil {
			return nil, false, err
		}
		if result.conflict {
			return nil, false, conflict
		}
		return result.merged, true, nil
	case storedRecordEqual(tx, refs, name, row, base):
		return ours, true, nil
	}
	result, err = func() ([]byte, error) {
		merge, err := mergeWithStoredRecord(refs, field, kind, key, base, ours, row)
		if err != nil {
			return nil, err
		}
		if merge.conflict {
			return nil, conflict
		}
		return merge.merged, nil
	}()
	return result, err == nil, err
}

// setMetadataRecords installs records (JSON as stored) into data's
// collections in place, replacing the records with the same keys, adding
// the others and dropping deleted records (nil). Writers use it to keep the
// copy they install equal to what a merge wrote.
func setMetadataRecords(data *domain.Bootstrap, records map[metadataRecordKey][]byte) error {
	root := reflect.ValueOf(data).Elem()
	for key, raw := range records {
		field, kind, ok := metadataRecordType(key.field)
		if !ok {
			continue
		}
		value := root.Field(field.index)
		if value.Kind() == reflect.Map && value.IsNil() {
			value.Set(reflect.MakeMap(value.Type()))
		}
		collection := newMetadataCollection(field, value)
		if raw == nil {
			value.Set(collection.remove(key.key))
			continue
		}
		old, _ := collection.record(key.key)
		record, err := decodeMetadataRecord(kind, raw, old)
		if err != nil {
			return err
		}
		keepImportInputs(key.field, record, old)
		switch {
		case value.Kind() == reflect.Map:
			value.SetMapIndex(reflect.ValueOf(key.key).Convert(value.Type().Key()), record)
		case old.IsValid():
			collection.set(key.key, record)
		default:
			value.Set(reflect.Append(value, record))
		}
	}
	return nil
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

// RefreshMetadataRecord replaces one record of the workspace's in-memory
// copy (field by its JSON name, such as "agentSessions") with the stored one,
// for a request that must act on what another server committed rather than
// on a copy its peer event has not updated yet.
func (s *SQLiteStore) RefreshMetadataRecord(ctx context.Context, workspace, field, key string) error {
	return s.refreshMetadataRecords(ctx, workspace, []metadataRecordKey{{field, key}})
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
			keepImportInputs(records.field.name, record, old)
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
// other than a concurrent write, or runs out of attempts; then the concurrent
// write is the error (a 409 to API clients), never a silent overwrite.
// Before rerunning it waits a jittered, growing backoff (so two servers
// racing on one record do not collide again in step), and refresh brings the
// conflicting records up to date.
func mutationAttempts(ctx context.Context, attempts int, attempt func(context.Context) error, refresh func(error) error) error {
	var err error
	for number := 1; ; number++ {
		err = attempt(ctx)
		if !errors.Is(err, ErrConcurrentWrite) || number >= attempts {
			return err
		}
		if refresh != nil {
			if refreshErr := refresh(err); refreshErr != nil {
				return refreshErr
			}
		}
		traceMark(ctx, "retry")
		if waitErr := concurrentWriteBackoff(ctx, number); waitErr != nil {
			return err
		}
	}
}

// concurrentWriteBackoff waits before rerun number+1 of a write that lost a
// race: about 5ms doubling per rerun up to 160ms, each between half and one
// and a half times that.
func concurrentWriteBackoff(ctx context.Context, number int) error {
	step := 5 * time.Millisecond << min(number-1, 5)
	timer := time.NewTimer(step/2 + time.Duration(rand.Int64N(int64(step))))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// refreshConflictedMetadata is the refresh for mutationAttempts: it reloads
// the records a metadata conflict names into the workspace's copy.
func (s *SQLiteStore) refreshConflictedMetadata(ctx context.Context, workspace string) func(error) error {
	return func(err error) error {
		if keys, ok := conflictedMetadataKeys(err); ok {
			return s.refreshMetadataRecords(ctx, workspace, keys)
		}
		return nil
	}
}

// backgroundWriteAttempts bounds the reruns of a background write (a startup
// migration, a progress backfill) that keeps losing races to other servers.
const backgroundWriteAttempts = 25

// rerunBackgroundWrite runs a background write until it no longer loses a
// race (bounded), refreshing the records it conflicted on and logging each
// rerun. Background writes have no client to report a 409 to.
func (s *SQLiteStore) rerunBackgroundWrite(ctx context.Context, name, workspace string, write func() error) error {
	refresh := s.refreshConflictedMetadata(ctx, workspace)
	attempt := 0
	return mutationAttempts(ctx, backgroundWriteAttempts, func(context.Context) error {
		attempt++
		if attempt > 1 {
			slog.Info("rerunning a background write that raced another server", "write", name, "workspace", workspace, "attempt", attempt)
		}
		return write()
	}, refresh)
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
