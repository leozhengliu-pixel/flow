package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"sync/atomic"

	"flow/api/internal/domain"
)

const metadataCollectionsKey = "_flowCollections"

// metadataGroupEvaluations counts record group lookups; tests use it to check
// that a record sync stays linear in the number of records.
var metadataGroupEvaluations atomic.Int64

type metadataRecordKey struct{ field, key string }
type metadataRecord struct {
	data  json.RawMessage
	order int
}

type metadataUpdate struct {
	Parent string          `json:"parent"`
	Item   json.RawMessage `json:"item,omitempty"`
	Null   bool            `json:"null,omitempty"`
}

func splitUpdateMetadata(field string, items map[string]json.RawMessage) (map[metadataRecordKey]metadataRecord, bool) {
	result := map[metadataRecordKey]metadataRecord{}
	for parent, raw := range items {
		var updates []json.RawMessage
		if json.Unmarshal(raw, &updates) != nil {
			return nil, false
		}
		marker, _ := json.Marshal(metadataUpdate{Parent: parent, Null: bytes.Equal(raw, []byte("null"))})
		result[metadataRecordKey{field, fmt.Sprintf("%x", sha256.Sum256([]byte("parent:"+parent)))}] = metadataRecord{marker, 0}
		seen := map[string]bool{}
		for index, update := range updates {
			var identity struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(update, &identity) != nil || identity.ID == "" || seen[identity.ID] {
				return nil, false
			}
			seen[identity.ID] = true
			key, _ := json.Marshal([]string{parent, identity.ID})
			value, _ := json.Marshal(metadataUpdate{Parent: parent, Item: update})
			result[metadataRecordKey{field, fmt.Sprintf("%x", sha256.Sum256(key))}] = metadataRecord{value, index}
		}
	}
	return result, true
}

func (s *SQLiteStore) migrateWorkspaceMetadataRecords(ctx context.Context) error {
	for key, data := range s.workspaces {
		var raw []byte
		if err := s.db.QueryRowContext(ctx, "SELECT data FROM workspace_states WHERE workspace_key=?", key).Scan(&raw); err != nil {
			return err
		}
		var root map[string]json.RawMessage
		if err := json.Unmarshal(raw, &root); err != nil {
			return err
		}
		if _, ok := root[metadataCollectionsKey]; !ok {
			if err := s.persistWorkspace(ctx, key, data, nil); err != nil {
				return err
			}
		}
		s.workspaces[key] = collectionMetadata(data)
	}
	return nil
}

func (s *SQLiteStore) ensureWorkspaceMetadataRecords(ctx context.Context) error {
	blob := "BLOB"
	keyType := "VARCHAR(191)"
	if s.dialect == "mysql" {
		blob = "LONGBLOB"
		keyType = "VARBINARY(191)"
	}
	if s.dialect == "postgres" {
		blob = "BYTEA"
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS workspace_metadata_records(workspace_key VARCHAR(191) NOT NULL,field VARCHAR(191) NOT NULL,record_key `+keyType+` NOT NULL,collection_order BIGINT NOT NULL,data `+blob+` NOT NULL,PRIMARY KEY(workspace_key,field,record_key))`); err != nil {
		return err
	}
	// Incremental writers append after the highest ordinal per collection.
	// Without this index that lookup scans every row of the collection.
	index := `CREATE INDEX IF NOT EXISTS workspace_metadata_order_idx ON workspace_metadata_records(workspace_key,field,collection_order)`
	if s.dialect == "mysql" {
		index = `CREATE INDEX workspace_metadata_order_idx ON workspace_metadata_records(workspace_key,field,collection_order)`
	}
	if _, err := s.db.ExecContext(ctx, index); err != nil && !(s.dialect == "mysql" && isDuplicateIndex(err)) {
		return err
	}
	return nil
}

// Arrays of entities and dictionaries are stored per entry. The small root
// carries scalars and collection shapes so nil, empty, order and map keys survive.
func splitWorkspaceMetadata(raw []byte) (map[string]json.RawMessage, map[string]string, map[metadataRecordKey]metadataRecord, error) {
	root := map[string]json.RawMessage{}
	shapes := map[string]string{}
	records := map[metadataRecordKey]metadataRecord{}
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, nil, nil, err
	}
	for field, value := range root {
		if len(value) == 0 {
			continue
		}
		if value[0] == '[' {
			var items []json.RawMessage
			if err := json.Unmarshal(value, &items); err != nil {
				return nil, nil, nil, err
			}
			ids := make([]string, len(items))
			seen := map[string]bool{}
			valid := true
			for i, item := range items {
				var entity struct {
					ID string `json:"id"`
				}
				if json.Unmarshal(item, &entity) != nil || entity.ID == "" || len(entity.ID) > 191 || seen[entity.ID] {
					valid = false
					break
				}
				ids[i] = entity.ID
				seen[entity.ID] = true
			}
			if !valid {
				continue
			}
			shapes[field] = "array"
			root[field] = json.RawMessage(`[]`)
			for i, item := range items {
				records[metadataRecordKey{field, ids[i]}] = metadataRecord{item, i}
			}
		} else if value[0] == '{' {
			// Workspace and viewer are scalar records, not dynamic dictionaries.
			if field == "workspace" || field == "viewer" {
				continue
			}
			var items map[string]json.RawMessage
			if err := json.Unmarshal(value, &items); err != nil {
				return nil, nil, nil, err
			}
			if field == "projectUpdates" || field == "initiativeUpdates" {
				if updates, ok := splitUpdateMetadata(field, items); ok {
					shapes[field] = "updates"
					root[field] = json.RawMessage(`{}`)
					for key, value := range updates {
						records[key] = value
					}
					continue
				}
			}
			valid := true
			for key := range items {
				if len(key) > 191 {
					valid = false
					break
				}
			}
			if !valid {
				continue
			}
			shapes[field] = "map"
			root[field] = json.RawMessage(`{}`)
			for key, item := range items {
				records[metadataRecordKey{field, key}] = metadataRecord{item, 0}
			}
		}
	}
	return root, shapes, records, nil
}

func writeWorkspaceMetadataRecords(ctx context.Context, tx *sqlTx, workspace string, raw []byte) ([]byte, error) {
	var identities struct {
		Users []domain.User `json:"users"`
	}
	if err := json.Unmarshal(raw, &identities); err != nil {
		return nil, err
	}
	refs := newIssueReferences(domain.Bootstrap{Users: identities.Users})
	root, shapes, wanted, err := splitWorkspaceMetadata(raw)
	if err != nil {
		return nil, err
	}
	if err := syncMetadataRecordSet(ctx, tx, workspace, refs, shapes, wanted, nil); err != nil {
		return nil, err
	}
	root[metadataCollectionsKey], err = json.Marshal(shapes)
	if err != nil {
		return nil, err
	}
	return json.Marshal(root)
}

// syncMetadataRecordSet makes the stored records of fields (every field when
// fields is nil) equal wanted, rewriting only rows whose order or data changed.
func syncMetadataRecordSet(ctx context.Context, tx *sqlTx, workspace string, refs issueReferences, shapes map[string]string, wanted map[metadataRecordKey]metadataRecord, fields []string) error {
	return syncMetadataRecords(ctx, tx, workspace, refs, shapes, wanted, fields, nil)
}

// syncMetadataRecords is syncMetadataRecordSet with an optional in-memory copy
// of the stored records (known). With it only keys and orders are read from
// the database: stored payloads are taken from known, and fetched only for
// rows known does not have.
func syncMetadataRecords(ctx context.Context, tx *sqlTx, workspace string, refs issueReferences, shapes map[string]string, wanted map[metadataRecordKey]metadataRecord, fields []string, known map[metadataRecordKey]metadataRecord) error {
	previous := map[metadataRecordKey]metadataRecord{}
	columns := `field,record_key,collection_order,data`
	if known != nil {
		columns = `field,record_key,collection_order`
	}
	query, args := `SELECT `+columns+` FROM workspace_metadata_records WHERE workspace_key=?`, []any{workspace}
	if fields != nil {
		if len(fields) == 0 {
			return nil
		}
		clause, fieldArgs := bindList("field", fields)
		query += ` AND ` + clause
		args = append(args, fieldArgs...)
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	missing := []metadataRecordKey{}
	for rows.Next() {
		var key metadataRecordKey
		var value metadataRecord
		if known != nil {
			err = rows.Scan(&key.field, &key.key, &value.order)
			if stored, ok := known[key]; ok {
				value.data = stored.data
			} else {
				missing = append(missing, key)
			}
		} else {
			err = rows.Scan(&key.field, &key.key, &value.order, &value.data)
		}
		if err != nil {
			rows.Close()
			return err
		}
		previous[key] = value
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, key := range missing {
		value := previous[key]
		if err := tx.QueryRowContext(ctx, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`, workspace, key.field, key.key).Scan(&value.data); err != nil {
			return err
		}
		previous[key] = value
	}
	return applyMetadataRecords(ctx, tx, workspace, refs, shapes, wanted, previous)
}

// metadataRecordGroup names the ordered collection a record belongs to: the
// field of an entity list, or field:parent for one project's or initiative's
// updates. Map entries and update parent markers have no order group.
func metadataRecordGroup(shapes map[string]string, key metadataRecordKey, data []byte) string {
	metadataGroupEvaluations.Add(1)
	switch shapes[key.field] {
	case "array":
		return key.field
	case "updates":
		if parent, item := metadataUpdateParent(data); item {
			return key.field + ":" + parent
		}
	}
	return ""
}

// metadataUpdateParent reads the parent of an update record and whether it
// holds an item (rather than being the parent's marker). Records are written
// by json.Marshal(metadataUpdate), so the parent is the first member and the
// item, when present, the second: both are read from the prefix without
// decoding the update itself. Anything else falls back to a full decode.
func metadataUpdateParent(data []byte) (string, bool) {
	const prefix = `{"parent":"`
	if rest, ok := bytes.CutPrefix(data, []byte(prefix)); ok {
		// Parents with escaped characters take the decode below.
		if end := bytes.IndexAny(rest, "\"\\"); end >= 0 && rest[end] == '"' {
			parent, tail := string(rest[:end]), rest[end+1:]
			if bytes.HasPrefix(tail, []byte(`,"item":`)) {
				return parent, true
			}
			if bytes.HasPrefix(tail, []byte(`}`)) || bytes.HasPrefix(tail, []byte(`,"null":`)) {
				return parent, false
			}
		}
	}
	var update metadataUpdate
	if json.Unmarshal(data, &update) != nil {
		return "", false
	}
	return update.Parent, len(update.Item) > 0
}

// applyMetadataRecords writes wanted over previous (the stored rows of the
// same records): it assigns collection orders that keep stored positions,
// upserts rows whose order or data changed and deletes rows wanted lacks.
// Each record's group is computed once, so the cost is linear in the number
// of records.
func applyMetadataRecords(ctx context.Context, tx *sqlTx, workspace string, refs issueReferences, shapes map[string]string, wanted, previous map[metadataRecordKey]metadataRecord) error {
	groups := map[string][]metadataRecordKey{}
	for key, value := range wanted {
		if name := metadataRecordGroup(shapes, key, value.data); name != "" {
			groups[name] = append(groups[name], key)
		}
	}
	positions := make(map[string]map[string]int, len(groups))
	for name := range groups {
		positions[name] = map[string]int{}
	}
	if len(groups) > 0 {
		for key, value := range previous {
			kind := shapes[key.field]
			if kind != "array" && kind != "updates" {
				continue
			}
			if kind == "array" {
				if group, ok := positions[key.field]; ok {
					group[key.key] = value.order
				}
				continue
			}
			if group, ok := positions[metadataRecordGroup(shapes, key, value.data)]; ok {
				group[key.key] = value.order
			}
		}
	}
	for name, keys := range groups {
		items := make([]domain.Issue, 0, len(keys))
		for _, key := range keys {
			items = append(items, domain.Issue{ID: key.key, Number: wanted[key].order})
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Number < items[j].Number })
		order := issueCollectionOrder(items, positions[name])
		for _, key := range keys {
			value := wanted[key]
			value.order = order[key.key]
			wanted[key] = value
		}
	}
	for key, value := range wanted {
		old, exists := previous[key]
		if !exists || old.order != value.order || !refs.equalDisplayData(old.data, value.data) {
			if err := writeMetadataRecordRow(ctx, tx, workspace, key, value, old.data); err != nil {
				return err
			}
		}
		delete(previous, key)
	}
	for key := range previous {
		if err := deleteMetadataRecordRow(ctx, tx, workspace, key); err != nil {
			return err
		}
	}
	return nil
}

// writeMetadataRecordRow upserts one metadata record and its derived rows
// (API key lookup, application mentions, customer filters, search index).
// previous is the stored payload, when the record existed.
func writeMetadataRecordRow(ctx context.Context, tx *sqlTx, workspace string, key metadataRecordKey, value metadataRecord, previous []byte) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,?,?,?,?) ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET collection_order=excluded.collection_order,data=excluded.data`, workspace, key.field, key.key, value.order, []byte(value.data)); err != nil {
		return err
	}
	if key.field == "apiKeys" {
		if err := upsertAPIKeyLookup(ctx, tx, workspace, key.key, apiKeyLookupHashFromRecord(value.data)); err != nil {
			return err
		}
	}
	if key.field == "projects" {
		var next, prior struct {
			Comments []domain.Comment `json:"comments"`
		}
		if err := json.Unmarshal(value.data, &next); err != nil {
			return err
		}
		if len(previous) > 0 {
			if err := json.Unmarshal(previous, &prior); err != nil {
				return err
			}
		}
		seen := map[string]bool{}
		for _, comment := range prior.Comments {
			seen[comment.ID] = true
		}
		for _, comment := range next.Comments {
			if !seen[comment.ID] {
				raw, err := json.Marshal(comment)
				if err != nil {
					return err
				}
				if err := syncApplicationMentions(ctx, tx, workspace, key.key, raw); err != nil {
					return err
				}
			}
		}
	}
	if err := writeCustomerFilterRecord(ctx, tx, workspace, key.field, key.key, value.data); err != nil {
		return err
	}
	return syncMetadataSearchDocument(ctx, tx, workspace, key.field, key.key, value.data)
}

func deleteMetadataRecordRow(ctx context.Context, tx *sqlTx, workspace string, key metadataRecordKey) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`, workspace, key.field, key.key); err != nil {
		return err
	}
	if key.field == "apiKeys" {
		if err := deleteAPIKeyLookup(ctx, tx, workspace, key.key); err != nil {
			return err
		}
	}
	if err := writeCustomerFilterRecord(ctx, tx, workspace, key.field, key.key, nil); err != nil {
		return err
	}
	return syncMetadataSearchDocument(ctx, tx, workspace, key.field, key.key, nil)
}

type metadataReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func metadataReadOptions(dialect string) *sql.TxOptions {
	isolation := sql.LevelRepeatableRead
	if dialect == "sqlite" {
		isolation = sql.LevelSerializable
	}
	return &sql.TxOptions{ReadOnly: true, Isolation: isolation}
}

func (s *SQLiteStore) expandWorkspaceMetadata(ctx context.Context, workspace string, raw []byte, readers ...metadataReader) ([]byte, error) {
	var reader metadataReader = s.db
	if len(readers) > 0 {
		reader = readers[0]
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	manifest, ok := root[metadataCollectionsKey]
	if !ok {
		return raw, nil
	}
	var shapes map[string]string
	if err := json.Unmarshal(manifest, &shapes); err != nil {
		return nil, err
	}
	arrays := map[string][]json.RawMessage{}
	objects := map[string]map[string]json.RawMessage{}
	updates := map[string]map[string][]json.RawMessage{}
	for field, shape := range shapes {
		switch shape {
		case "array":
			arrays[field] = []json.RawMessage{}
		case "map":
			objects[field] = map[string]json.RawMessage{}
		case "updates":
			updates[field] = map[string][]json.RawMessage{}
		default:
			return nil, fmt.Errorf("unknown workspace collection shape %q", shape)
		}
	}
	rows, err := reader.QueryContext(ctx, `SELECT field,record_key,data FROM workspace_metadata_records WHERE workspace_key=? ORDER BY field,collection_order,record_key`, workspace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var field, key string
		var value json.RawMessage
		if err := rows.Scan(&field, &key, &value); err != nil {
			return nil, err
		}
		if shapes[field] == "array" {
			arrays[field] = append(arrays[field], value)
		} else if shapes[field] == "map" {
			objects[field][key] = value
		} else if shapes[field] == "updates" {
			var update metadataUpdate
			if err := json.Unmarshal(value, &update); err != nil {
				return nil, err
			}
			if len(update.Item) > 0 {
				updates[field][update.Parent] = append(updates[field][update.Parent], update.Item)
			} else if _, ok := updates[field][update.Parent]; !ok {
				if update.Null {
					updates[field][update.Parent] = nil
				} else {
					updates[field][update.Parent] = []json.RawMessage{}
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for field, items := range arrays {
		root[field], err = json.Marshal(items)
		if err != nil {
			return nil, err
		}
	}
	for field, items := range objects {
		root[field], err = json.Marshal(items)
		if err != nil {
			return nil, err
		}
	}
	for field, items := range updates {
		root[field], err = json.Marshal(items)
		if err != nil {
			return nil, err
		}
	}
	delete(root, metadataCollectionsKey)
	return json.Marshal(root)
}
