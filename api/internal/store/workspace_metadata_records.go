package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"

	"flow/api/internal/domain"
)

const metadataCollectionsKey = "_flowCollections"

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
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS workspace_metadata_records(workspace_key VARCHAR(191) NOT NULL,field VARCHAR(191) NOT NULL,record_key `+keyType+` NOT NULL,collection_order BIGINT NOT NULL,data `+blob+` NOT NULL,PRIMARY KEY(workspace_key,field,record_key))`)
	return err
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
	previous := map[metadataRecordKey]metadataRecord{}
	rows, err := tx.QueryContext(ctx, `SELECT field,record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=?`, workspace)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var key metadataRecordKey
		var value metadataRecord
		if err := rows.Scan(&key.field, &key.key, &value.order, &value.data); err != nil {
			rows.Close()
			return nil, err
		}
		previous[key] = value
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	groups := map[string][]metadataRecordKey{}
	group := func(key metadataRecordKey, value metadataRecord) string {
		if shapes[key.field] == "array" {
			return key.field
		}
		if shapes[key.field] == "updates" {
			var update metadataUpdate
			if json.Unmarshal(value.data, &update) == nil && len(update.Item) > 0 {
				return key.field + ":" + update.Parent
			}
		}
		return ""
	}
	for key, value := range wanted {
		if name := group(key, value); name != "" {
			groups[name] = append(groups[name], key)
		}
	}
	for name, keys := range groups {
		var items []domain.Issue
		positions := map[string]int{}
		for _, key := range keys {
			items = append(items, domain.Issue{ID: key.key, Number: wanted[key].order})
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Number < items[j].Number })
		for key, value := range previous {
			if group(key, value) == name {
				positions[key.key] = value.order
			}
		}
		order := issueCollectionOrder(items, positions)
		for _, key := range keys {
			value := wanted[key]
			value.order = order[key.key]
			wanted[key] = value
		}
	}
	for key, value := range wanted {
		old, exists := previous[key]
		if !exists || old.order != value.order || !refs.equalDisplayData(old.data, value.data) {
			if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,?,?,?,?) ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET collection_order=excluded.collection_order,data=excluded.data`, workspace, key.field, key.key, value.order, []byte(value.data)); err != nil {
				return nil, err
			}
			if key.field == "apiKeys" {
				if err := upsertAPIKeyLookup(ctx, tx, workspace, key.key, apiKeyLookupHashFromRecord(value.data)); err != nil {
					return nil, err
				}
			}
		}
		delete(previous, key)
	}
	for key := range previous {
		if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`, workspace, key.field, key.key); err != nil {
			return nil, err
		}
		if key.field == "apiKeys" {
			if err := deleteAPIKeyLookup(ctx, tx, workspace, key.key); err != nil {
				return nil, err
			}
		}
	}
	root[metadataCollectionsKey], err = json.Marshal(shapes)
	if err != nil {
		return nil, err
	}
	return json.Marshal(root)
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
