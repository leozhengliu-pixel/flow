package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"
)

// The OAuth/application transactions write their metadata rows themselves
// (app users, API keys, grants, application policies) and mirror each write
// in the copy they were handed. Bounded, that copy is the resident policy
// copy: the callbacks declare the records they read with oauthMetadataSelect,
// which hands them the installed records (flag off it is a no-op: the copy
// already holds every record). The records the mirror changed are then read
// back from SQL in the same transaction and published as the installed image,
// so it is exactly what a reload reads, before the event is published.

// oauthMetadataBoundedFields are the record fields the callbacks write.
var oauthMetadataBoundedFields = []string{"users", "apiKeys", "oauthAuthorizations", "settings"}

type oauthMetadataBoundedTx struct {
	inputs *issueHotInputs
	// handed lists, per slice field, the installed ordinals of the records
	// handed to the callback, in the order of its copy.
	handed map[string][]int
	// before holds each handed record's JSON, to tell the mirror's changes.
	before map[metadataRecordKey][]byte
	// dropped lists the handed records the callback removed from its copy.
	dropped map[metadataRecordKey]bool
}

// The callbacks reach oauthMetadataSelect through their transaction only.
var oauthMetadataBoundedTxs sync.Map // *sqlTx -> *oauthMetadataBoundedTx

// oauthMetadataSelect hands an OAuth metadata callback the installed records
// of field whose Go field (one of names) equals one of values, merged in
// collection order with the records handed before. Call it before the
// callback reads or changes the field. Off mode it does nothing.
func oauthMetadataSelect(ctx context.Context, tx *sqlTx, current *domain.Bootstrap, field string, names []string, values ...string) error {
	state, found := oauthMetadataBoundedTxs.Load(tx)
	if !found {
		return nil
	}
	t := state.(*oauthMetadataBoundedTx)
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	keys, err := t.inputs.keys(ctx, values...)
	if err != nil {
		return err
	}
	return t.load(ctx, current, field, func(ctx context.Context, record metadataLogicalNode) (bool, error) {
		for _, name := range names {
			match, err := issueHotMatches(ctx, record, name, keys)
			if err != nil || match {
				return match, err
			}
		}
		return false, nil
	})
}

// oauthMetadataSelectWhere is oauthMetadataSelect for a callback whose
// records are not named by one field's values: match reads the installed
// record's fields (never decoding the record) and must select a superset of
// the records the callback reads or changes. Off mode it does nothing.
func oauthMetadataSelectWhere(ctx context.Context, tx *sqlTx, current *domain.Bootstrap, field string, match func(context.Context, metadataLogicalNode) (bool, error)) error {
	state, found := oauthMetadataBoundedTxs.Load(tx)
	if !found {
		return nil
	}
	return state.(*oauthMetadataBoundedTx).load(context.WithValue(ctx, metadataWorkLeaseKey{}, nil), current, field, match)
}

func (t *oauthMetadataBoundedTx) load(ctx context.Context, current *domain.Bootstrap, field string, match func(context.Context, metadataLogicalNode) (bool, error)) error {
	index, _, ok := metadataLogicalField(field)
	if !ok || !slices.Contains(oauthMetadataBoundedFields, field) {
		return ErrMetadataRecordQuery
	}
	target := reflect.ValueOf(current).Elem().Field(index)
	if target.Kind() != reflect.Slice {
		return ErrMetadataRecordQuery
	}
	handed := t.handed[field]
	if target.Len() != len(handed) {
		return fmt.Errorf("%w: %s changed before its records were selected", ErrBoundedMetadataCoverage, field)
	}
	merged := reflect.MakeSlice(target.Type(), 0, target.Len())
	ordinals := make([]int, 0, len(handed))
	ordinal, position := -1, 0
	err := t.inputs.source.fields[index].Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
		ordinal++
		if position < len(handed) && handed[position] == ordinal {
			merged = reflect.Append(merged, target.Index(position))
			ordinals = append(ordinals, ordinal)
			position++
			return nil
		}
		matched, err := match(ctx, record)
		if err != nil {
			return err
		}
		if !matched {
			return nil
		}
		cost, err := issueHotProjectionCost(ctx, record, nil)
		if err != nil {
			return err
		}
		if err := t.inputs.reserve(ctx, cost); err != nil {
			return err
		}
		value, err := record.Value(ctx, int(record.cost))
		if err != nil {
			return err
		}
		raw, err := json.Marshal(value.Interface())
		if err != nil {
			return err
		}
		if err := t.inputs.reserve(ctx, uint64(len(raw))+256); err != nil {
			return err
		}
		if id, valid := metadataRecordValueKey(value); valid {
			t.before[metadataRecordKey{field: field, key: id}] = raw
		}
		merged = reflect.Append(merged, value)
		ordinals = append(ordinals, ordinal)
		return nil
	})
	if err != nil {
		return err
	}
	if position != len(handed) {
		return fmt.Errorf("%w: %s handed records moved", ErrBoundedMetadataCoverage, field)
	}
	if merged.Len() > 0 || !target.IsNil() {
		target.Set(merged)
	}
	t.handed[field] = ordinals
	return nil
}

// changes lists, per field, the records next holds that the callback added
// or changed, in first-seen order.
func (t *oauthMetadataBoundedTx) changes(ctx context.Context, next domain.Bootstrap) (map[string][]string, error) {
	result := map[string][]string{}
	right := reflect.ValueOf(next)
	for _, field := range oauthMetadataBoundedFields {
		index, _, _ := metadataLogicalField(field)
		value := right.Field(index)
		changed := func(key string, item reflect.Value) error {
			raw, err := json.Marshal(item.Interface())
			if err != nil {
				return err
			}
			if err := t.inputs.reserve(ctx, uint64(len(raw))+256); err != nil {
				return err
			}
			if before, found := t.before[metadataRecordKey{field: field, key: key}]; found && bytes.Equal(before, raw) {
				return nil
			}
			if !slices.Contains(result[field], key) {
				result[field] = append(result[field], key)
			}
			return nil
		}
		switch value.Kind() {
		case reflect.Slice:
			kept := map[string]bool{}
			for i := 0; i < value.Len(); i++ {
				key, valid := metadataRecordValueKey(value.Index(i))
				if !valid {
					return nil, ErrMetadataRecordQuery
				}
				kept[key] = true
				if err := changed(key, value.Index(i)); err != nil {
					return nil, err
				}
			}
			// A handed record the callback dropped from its copy (a deleted
			// row) is read back too: candidate removes it when its row is gone.
			var dropped []string
			for handed := range t.before {
				if handed.field == field && !kept[handed.key] && !slices.Contains(result[field], handed.key) {
					dropped = append(dropped, handed.key)
				}
			}
			if len(dropped) != 0 {
				if t.dropped == nil {
					t.dropped = map[metadataRecordKey]bool{}
				}
				for _, key := range dropped {
					t.dropped[metadataRecordKey{field: field, key: key}] = true
				}
				slices.Sort(dropped)
				result[field] = append(result[field], dropped...)
			}
		case reflect.Map:
			keys := make([]string, 0, value.Len())
			for _, key := range value.MapKeys() {
				keys = append(keys, key.String())
			}
			slices.Sort(keys)
			for _, key := range keys {
				if err := changed(key, value.MapIndex(reflect.ValueOf(key))); err != nil {
					return nil, err
				}
			}
		}
	}
	return result, nil
}

type oauthMetadataRow struct {
	order int64
	raw   []byte
}

// oauthMetadataRows reads the named rows of one field in the transaction.
func (t *oauthMetadataBoundedTx) rows(ctx context.Context, tx *sqlTx, workspace, field string, keys []string) (map[string]oauthMetadataRow, error) {
	result := map[string]oauthMetadataRow{}
	for start := 0; start < len(keys); start += MetadataRecordBatchMaxKeys {
		batch := keys[start:min(start+MetadataRecordBatchMaxKeys, len(keys))]
		clause, args := bindList("record_key", batch)
		rows, err := tx.QueryContext(ctx, `SELECT record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND `+clause,
			append([]any{workspace, field}, args...)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var key string
			var row oauthMetadataRow
			if err := rows.Scan(&key, &row.order, &row.raw); err != nil {
				rows.Close()
				return nil, err
			}
			// A case-insensitive collation can match another spelling.
			if !slices.Contains(batch, key) {
				continue
			}
			if err := t.inputs.reserve(ctx, uint64(len(row.raw))*8+4096); err != nil {
				rows.Close()
				return nil, err
			}
			result[key] = row
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

// oauthMetadataRowsAfter counts the field's rows a load orders after (order, key).
func oauthMetadataRowsAfter(ctx context.Context, tx *sqlTx, workspace, field string, order int64, key string) (int, error) {
	var after int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND collection_order>?`, workspace, field, order).Scan(&after); err != nil {
		return 0, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT record_key FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND collection_order=?`, workspace, field, order)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var other string
		if err := rows.Scan(&other); err != nil {
			return 0, err
		}
		if strings.Compare(other, key) > 0 {
			after++
		}
	}
	return after, rows.Err()
}

type oauthMetadataInsert struct {
	index int
	node  metadataAggregateNode
}

// oauthMetadataRecordsCollection is a stored array with records replaced in
// place (first occurrence) and new records at their load positions.
type oauthMetadataRecordsCollection struct {
	base    metadataLogicalNode
	shape   metadataLogicalNode
	replace map[string]metadataAggregateNode
	inserts []oauthMetadataInsert
	// remove holds the records whose rows were deleted: every occurrence
	// is left out and later ordinals close up.
	remove map[string]bool
}

func (n oauthMetadataRecordsCollection) nodeType() reflect.Type           { return n.base.typ }
func (n oauthMetadataRecordsCollection) logicalBase() metadataLogicalNode { return n.shape }
func (n oauthMetadataRecordsCollection) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n oauthMetadataRecordsCollection) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	ordinal, next := 0, 0
	inserted := func() error {
		for next < len(n.inserts) && n.inserts[next].index <= ordinal {
			if err := visit(reflect.ValueOf(ordinal), n.inserts[next].node); err != nil {
				return err
			}
			ordinal++
			next++
		}
		return nil
	}
	replaced := map[string]bool{}
	if err := n.base.Children(ctx, func(_ reflect.Value, item metadataLogicalNode) error {
		if err := inserted(); err != nil {
			return err
		}
		value := metadataAggregateNode(metadataAggregateStoredNode{item})
		if len(n.remove) != 0 {
			id, err := issueHotField(ctx, item, "ID")
			if err != nil {
				return err
			}
			for key := range n.remove {
				match, err := id.StringEqual(ctx, key)
				if err != nil || match {
					return err
				}
			}
		}
		if len(replaced) < len(n.replace) {
			id, err := issueHotField(ctx, item, "ID")
			if err != nil {
				return err
			}
			for key, replacement := range n.replace {
				if replaced[key] {
					continue
				}
				match, err := id.StringEqual(ctx, key)
				if err != nil {
					return err
				}
				if match {
					replaced[key], value = true, replacement
					break
				}
			}
		}
		err := visit(reflect.ValueOf(ordinal), value)
		ordinal++
		return err
	}); err != nil {
		return err
	}
	for ; next < len(n.inserts); next++ {
		if err := visit(reflect.ValueOf(ordinal), n.inserts[next].node); err != nil {
			return err
		}
		ordinal++
	}
	return nil
}

// oauthMetadataLoaded is a record as a load reads it: decoded from its row
// (no hidden field survives) and normalized alone, as the load normalizes it.
func oauthMetadataLoaded(field string, kind reflect.Type, raw []byte, viewer domain.User) (reflect.Value, error) {
	value, err := decodeMetadataRecord(kind, raw, reflect.Value{})
	if err != nil {
		return reflect.Value{}, err
	}
	mini := domain.Bootstrap{Viewer: viewer}
	index, info, _ := metadataLogicalField(field)
	target := reflect.ValueOf(&mini).Elem().Field(index)
	if info.Type.Kind() == reflect.Map {
		return value, nil
	}
	target.Set(reflect.MakeSlice(info.Type, 1, 1))
	target.Index(0).Set(value)
	normalizeStoredMetadata(&mini)
	return reflect.ValueOf(mini).Field(index).Index(0), nil
}

// candidate is the installed image with the changed records as the
// transaction wrote them and a load reads them: replaced in place, inserted
// where a load orders them, and a new user's default personal and
// notification settings the load derives. Records the transaction did not
// write (an inline field's) keep their installed values, as a reload reads
// them.
func (t *oauthMetadataBoundedTx) candidate(ctx context.Context, tx *sqlTx, workspace string, viewer domain.User, changed map[string][]string) (_ *metadataLogicalSource, err error) {
	source := t.inputs.source
	fresh, err := newMetadataLogicalBuilder(metadataLogicalSourceScratchContext(ctx, source))
	if err != nil {
		return nil, err
	}
	defer fresh.Close()
	write := func(value reflect.Value) (metadataAggregateNode, error) {
		if err := fresh.auditJSONLocations(value); err != nil {
			return nil, err
		}
		node, err := fresh.writeValue(value, 0, false)
		return metadataAggregateStoredNode{node}, err
	}
	replacements := map[string]metadataAggregateNode{}
	mapPatch := func(name string, base metadataAggregateNode, key string, value metadataAggregateNode) (metadataAggregateNode, error) {
		index, _, _ := metadataLogicalField(name)
		if _, found := replacements[name]; !found {
			shape, err := fresh.writeValue(reflect.MakeMap(source.fields[index].typ), 0, false)
			if err != nil {
				return nil, err
			}
			return metadataLogicalPatchedCollection{base: base, shape: shape, key: key, value: value}, nil
		}
		previous := base.(metadataLogicalPatchedCollection)
		return metadataLogicalPatchedCollection{base: base, shape: previous.shape, key: key, value: value}, nil
	}
	var newUsers []string
	for _, field := range oauthMetadataBoundedFields {
		keys := changed[field]
		if len(keys) == 0 {
			continue
		}
		rows, err := t.rows(ctx, tx, workspace, field, keys)
		if err != nil {
			return nil, err
		}
		inline, err := t.inputs.store.inlineMetadataFields(ctx, tx, workspace, field)
		if err != nil {
			return nil, err
		}
		if len(inline) != 0 {
			if len(rows) != 0 {
				return nil, fmt.Errorf("%w: %s", ErrMetadataFieldInline, field)
			}
			continue
		}
		index, _, _ := metadataLogicalField(field)
		node := source.fields[index]
		_, kind, ok := metadataRecordType(field)
		if !ok {
			return nil, ErrMetadataRecordQuery
		}
		if node.typ.Kind() == reflect.Map {
			current := metadataAggregateNode(metadataAggregateStoredNode{node})
			for _, key := range keys {
				row, found := rows[key]
				if !found {
					continue
				}
				value, err := oauthMetadataLoaded(field, kind, row.raw, viewer)
				if err != nil {
					return nil, err
				}
				written, err := write(value)
				if err != nil {
					return nil, err
				}
				if current, err = mapPatch(field, current, key, written); err != nil {
					return nil, err
				}
				replacements[field] = current
			}
			continue
		}
		// A dropped record whose row the transaction deleted is not loaded.
		removed := map[string]bool{}
		for _, key := range keys {
			if _, written := rows[key]; !written && t.dropped[metadataRecordKey{field: field, key: key}] {
				removed[key] = true
			}
		}
		// Each written key's first installed occurrence, and the count of
		// the records a load reads.
		present := map[string]bool{}
		count := 0
		if err := node.Children(ctx, func(_ reflect.Value, item metadataLogicalNode) error {
			id, err := issueHotField(ctx, item, "ID")
			if err != nil {
				return err
			}
			for key := range removed {
				match, err := id.StringEqual(ctx, key)
				if err != nil || match {
					return err
				}
			}
			count++
			for _, key := range keys {
				if _, written := rows[key]; !written || present[key] {
					continue
				}
				match, err := id.StringEqual(ctx, key)
				if err != nil {
					return err
				}
				if match {
					present[key] = true
					break
				}
			}
			return nil
		}); err != nil {
			return nil, err
		}
		collection := oauthMetadataRecordsCollection{base: node, shape: node, replace: map[string]metadataAggregateNode{}, remove: removed}
		type pending struct {
			after int
			node  metadataAggregateNode
		}
		var added []pending
		for _, key := range keys {
			row, written := rows[key]
			if !written {
				continue
			}
			value, err := oauthMetadataLoaded(field, kind, row.raw, viewer)
			if err != nil {
				return nil, err
			}
			record, err := write(value)
			if err != nil {
				return nil, err
			}
			if present[key] {
				collection.replace[key] = record
				continue
			}
			after, err := oauthMetadataRowsAfter(ctx, tx, workspace, field, row.order, key)
			if err != nil {
				return nil, err
			}
			added = append(added, pending{after, record})
			if field == "users" {
				newUsers = append(newUsers, key)
			}
		}
		total := count + len(added)
		for _, item := range added {
			position := total - 1 - item.after
			if position < 0 || position > total-1 {
				return nil, fmt.Errorf("%w: %s load position", ErrBoundedMetadataCoverage, field)
			}
			collection.inserts = append(collection.inserts, oauthMetadataInsert{index: position, node: item.node})
		}
		slices.SortStableFunc(collection.inserts, func(a, b oauthMetadataInsert) int { return a.index - b.index })
		for i := 1; i < len(collection.inserts); i++ {
			if collection.inserts[i].index == collection.inserts[i-1].index {
				return nil, fmt.Errorf("%w: %s load positions collide", ErrBoundedMetadataCoverage, field)
			}
		}
		if len(collection.replace) == 0 && len(collection.inserts) == 0 && len(collection.remove) == 0 {
			continue
		}
		if nilValue, err := node.isNil(ctx); err != nil {
			return nil, err
		} else if nilValue && len(collection.inserts) != 0 {
			// A load reads the shaped (non-null) array the rows extend.
			if collection.shape, err = fresh.writeValue(reflect.MakeSlice(node.typ, 0, 0), 0, false); err != nil {
				return nil, err
			}
		}
		replacements[field] = collection
	}
	// The load gives every user without them default personal and
	// notification settings (normalize); a new user gets them here.
	for _, name := range []string{"userSettings", "notificationPreferences"} {
		index, _, _ := metadataLogicalField(name)
		node := source.fields[index]
		current := metadataAggregateNode(metadataAggregateStoredNode{node})
		for _, user := range newUsers {
			found := false
			if err := node.MapEntries(ctx, func(key, _ metadataLogicalNode) error {
				match, err := key.StringEqual(ctx, user)
				found = found || match
				return err
			}); err != nil {
				return nil, err
			}
			if found {
				continue
			}
			value := reflect.ValueOf(defaultUserSettings(user))
			if name == "notificationPreferences" {
				value = reflect.ValueOf(defaultNotificationPreferences(user))
			}
			written, err := write(value)
			if err != nil {
				return nil, err
			}
			if current, err = mapPatch(name, current, user, written); err != nil {
				return nil, err
			}
			replacements[name] = current
		}
	}
	updated, err := source.Transform(ctx, func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
		if replacement, found := replacements[name]; found {
			return replacement, true, nil
		}
		return metadataAggregateStoredNode{node}, false, nil
	})
	if err != nil {
		return nil, err
	}
	defer updated.Release()
	return metadataLogicalInstalledCandidate(ctx, updated)
}

func (t *oauthMetadataBoundedTx) close() {
	if t.inputs != nil {
		t.inputs.close()
	}
	t.handed, t.before, t.dropped = nil, nil, nil
}

// errOAuthMetadataStale reports a stale image found before the callback ran:
// the attempt has no side effects yet and may run again.
var errOAuthMetadataStale = fmt.Errorf("%w: installed OAuth metadata image is stale", ErrConcurrentWrite)

// boundedOAuthMetadataTransaction is oauthMetadataTransaction for the bounded
// store. The callback runs once at most: only a stale image found before it
// runs is retried.
func (s *SQLiteStore) boundedOAuthMetadataTransaction(ctx context.Context, workspace string, mutate func(*sqlTx, string, domain.Bootstrap) (domain.Bootstrap, error)) error {
	ctx = context.WithValue(metadataLogicalUnpinnedContext(ctx), metadataWorkLeaseKey{}, nil)
	apply := func() error {
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		policy, found := s.storedWorkspace(workspace)
		if !found {
			return ErrAuthForbidden
		}
		if s.coordinator != nil {
			if err := s.reloadResidentWorkspace(ctx, workspace); err != nil {
				return err
			}
			if policy, found = s.storedWorkspace(workspace); !found {
				return ErrAuthForbidden
			}
		}
		stored, err := s.retainMetadataLogicalSource(ctx, workspace)
		if err != nil {
			return err
		}
		state := &oauthMetadataBoundedTx{inputs: &issueHotInputs{store: s, source: stored}, handed: map[string][]int{}, before: map[metadataRecordKey][]byte{}}
		defer state.close()
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		lock := ""
		if s.dialect != "sqlite" {
			lock = " FOR UPDATE"
		}
		base, err := readMetadataStamp(ctx, tx, workspace)
		if err != nil {
			return err
		}
		exact := stored.Stamp() == base
		if s.coordinator != nil && !exact {
			return errOAuthMetadataStale
		}
		// The callback's copy: the resident policy copy without records; the
		// callback selects the records it reads.
		current := policy
		current.Users, current.APIKeys, current.OAuthAuthorizations, current.OAuthApplications, current.Settings = nil, nil, nil, nil, nil
		oauthMetadataBoundedTxs.Store(tx, state)
		next, err := mutate(tx, lock, current)
		oauthMetadataBoundedTxs.Delete(tx)
		if err != nil {
			return err
		}
		changed, err := state.changes(ctx, next)
		if err != nil {
			return err
		}
		var publication *legacyResidentPublication
		defer func() { publication.Close() }()
		if len(changed) != 0 {
			candidate, err := state.candidate(ctx, tx, workspace, policy.Viewer, changed)
			if err != nil {
				return err
			}
			publication, err = s.prepareLegacyResidentPublication(ctx, tx, workspace, candidate, base, exact, false)
			if err != nil {
				candidate.Release()
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		// Peers' shared copy of the rows is stale (see oauthMetadataTransaction).
		s.dropMetadataCache(ctx, workspace)
		if publication == nil {
			return nil
		}
		return publication.Install()
	}
	var err error
	for attempt := 1; ; attempt++ {
		if s.coordinator != nil {
			err = s.coordinator.WithWorkspaceLock(ctx, workspace, apply)
		} else {
			err = apply()
		}
		if !errors.Is(err, errOAuthMetadataStale) || attempt >= fullMutationAttempts {
			return err
		}
	}
}

// oauthExchangeExpiryMargin widens the token exchange's expired-key selection
// past its own clock: the exchange tests each key against a later time.Now,
// so a key that expires between selection and test is still handed.
const oauthExchangeExpiryMargin = 5 * time.Minute

// oauthExchangeKeySelection selects, from the installed API keys, a superset
// of the keys ExchangeOAuthGrant revokes or deletes: with supersede, the app
// user's own keys of the client (actor app, SubjectID the grant's user), and
// every OAuth key whose expiry is before expiring. The exchange filters the
// handed keys with its literal conditions. Only scalar fields are read.
func oauthExchangeKeySelection(grant domain.OAuthRefreshGrant, supersede bool, expiring time.Time) func(context.Context, metadataLogicalNode) (bool, error) {
	return func(ctx context.Context, record metadataLogicalNode) (bool, error) {
		client, err := issueHotField(ctx, record, "OAuthClientID")
		if err != nil {
			return false, err
		}
		if empty, err := client.StringEqual(ctx, ""); err != nil || empty {
			return false, err
		}
		if supersede {
			own, err := client.StringEqual(ctx, grant.ClientID)
			if err != nil {
				return false, err
			}
			app := false
			if own {
				actor, err := issueHotField(ctx, record, "Actor")
				if err != nil {
					return false, err
				}
				if app, err = actor.StringEqual(ctx, "app"); err != nil {
					return false, err
				}
			}
			if app {
				// domain.APIKey.SubjectID: SubjectUserID, else CreatorID.
				subject, err := issueHotField(ctx, record, "SubjectUserID")
				if err != nil {
					return false, err
				}
				if match, err := subject.StringEqual(ctx, grant.UserID); err != nil || match {
					return match, err
				}
				blank, err := subject.StringEqual(ctx, "")
				if err != nil {
					return false, err
				}
				if blank {
					creator, err := issueHotField(ctx, record, "CreatorID")
					if err != nil {
						return false, err
					}
					if match, err := creator.StringEqual(ctx, grant.UserID); err != nil || match {
						return match, err
					}
				}
			}
		}
		expires, err := issueHotField(ctx, record, "ExpiresAt")
		if err != nil {
			return false, err
		}
		if missing, err := expires.isNil(ctx); err != nil || missing {
			return false, err
		}
		value, err := expires.Value(ctx, int(expires.cost))
		if err != nil {
			return false, err
		}
		at, ok := value.Interface().(*time.Time)
		return ok && at != nil && at.Before(expiring), nil
	}
}
