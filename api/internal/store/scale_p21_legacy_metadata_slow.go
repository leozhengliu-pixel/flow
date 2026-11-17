package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"flow/api/internal/domain"
)

// writeLegacyMetadataSlowFields is the existing delta writer's slow tail.
// The caller has already rebased the entire union and handled fast fields.
func (s *SQLiteStore) writeLegacyMetadataSlowFields(ctx context.Context, tx *sqlTx, workspace string, before, target domain.Bootstrap, names []string, refs issueReferences) (bool, error) {
	if !s.BoundedMetadataEnabled() || tx == nil || tx.database != s.db {
		return false, ErrBoundedMetadataCoverage
	}
	work := &ResourceRead{store: s}
	defer work.Close()
	lock := ""
	if tx.dialect != "sqlite" {
		lock = " FOR UPDATE"
	}
	var rootBytes int64
	if err := tx.QueryRowContext(ctx, "SELECT "+work.byteLength("data")+" FROM workspace_states WHERE workspace_key=?"+lock, workspace).Scan(&rootBytes); err != nil {
		return false, err
	}
	if rootBytes < 0 {
		return false, ErrMetadataWorkBudget
	}
	if rootBytes > MetadataRecordPageMaxBytes || legacySessionRootPatchFrom(ctx) != nil || legacyInlineFieldsPatchFrom(ctx) != nil {
		return s.writeStreamedMetadataSlowFields(ctx, tx, workspace, before, target, names, refs, rootBytes)
	}
	if err := work.reserve(ctx, uint64(rootBytes)*128+256<<10); err != nil {
		return false, err
	}
	readRoot := func(lock bool) (string, []byte, error) {
		suffix := ""
		if lock && tx.dialect != "sqlite" {
			suffix = " FOR UPDATE"
		}
		if lock {
			// Shape writes earlier in this transaction can grow the root.
			// Admit that growth before transferring its replacement bytes.
			var current int64
			if err := tx.QueryRowContext(ctx, "SELECT "+work.byteLength("data")+
				" FROM workspace_states WHERE workspace_key=?"+suffix, workspace).Scan(&current); err != nil {
				return "", nil, err
			}
			if current < 0 || current > MetadataRecordPageMaxBytes {
				return "", nil, ErrMetadataWorkBudget
			}
			if current > rootBytes {
				if err := work.reserve(ctx, uint64(current-rootBytes)*128); err != nil {
					return "", nil, err
				}
				rootBytes = current
			}
		}
		var id string
		var raw []byte
		if err := tx.QueryRowContext(ctx, "SELECT workspace_id,CASE WHEN "+work.byteLength("data")+
			"<=? THEN data ELSE NULL END FROM workspace_states WHERE workspace_key=?"+suffix,
			rootBytes, workspace).Scan(&id, &raw); err != nil {
			return "", nil, err
		}
		if raw == nil {
			return "", nil, ErrMetadataWorkBudget
		}
		return id, raw, nil
	}
	previousID, previousRoot, err := readRoot(false)
	if err != nil {
		return false, err
	}
	if len(names) == 0 && previousID == target.Workspace.ID {
		return false, nil
	}
	root := map[string]json.RawMessage{}
	if err := json.Unmarshal(previousRoot, &root); err != nil {
		return false, err
	}
	storedShapes := map[string]string{}
	if manifest := root[metadataCollectionsKey]; manifest != nil {
		if err := json.Unmarshal(manifest, &storedShapes); err != nil {
			return false, err
		}
	}
	partial := reflect.New(reflect.TypeFor[domain.Bootstrap]()).Elem()
	prior := reflect.New(reflect.TypeFor[domain.Bootstrap]()).Elem()
	source, original := reflect.ValueOf(&target).Elem(), reflect.ValueOf(&before).Elem()
	selected := make(map[string]bool, len(names))
	for _, name := range names {
		field, ok := metadataFieldByName(name)
		if !ok || selected[name] {
			return false, ErrMetadataRecordQuery
		}
		partial.Field(field.index).Set(source.Field(field.index))
		prior.Field(field.index).Set(original.Field(field.index))
		selected[name] = true
	}
	encoded, err := json.Marshal(partial.Interface())
	if err != nil {
		return false, err
	}
	partRoot, partShapes, partRecords, err := splitWorkspaceMetadata(encoded)
	if err != nil {
		return false, err
	}
	encodedBefore, err := json.Marshal(prior.Interface())
	if err != nil {
		return false, err
	}
	_, _, knownRecords, err := splitWorkspaceMetadata(encodedBefore)
	if err != nil {
		return false, err
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
	if err := syncMetadataRecords(withLegacyMetadataDeltaReadBudget(ctx, work), tx, workspace, refs, shapes, wanted, names, knownRecords); err != nil {
		return false, err
	}
	patch := func(root map[string]json.RawMessage, storedShapes map[string]string) ([]byte, error) {
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
		manifest, err := json.Marshal(storedShapes)
		if err != nil {
			return nil, err
		}
		root[metadataCollectionsKey] = manifest
		return json.Marshal(root)
	}
	raw, err := patch(root, storedShapes)
	if err != nil {
		return false, err
	}
	if previousID == target.Workspace.ID && bytes.Equal(previousRoot, raw) {
		return true, nil
	}
	previousID, previousRoot, err = readRoot(true)
	if err != nil {
		return false, err
	}
	root, storedShapes = map[string]json.RawMessage{}, map[string]string{}
	if err := json.Unmarshal(previousRoot, &root); err != nil {
		return false, err
	}
	if manifest := root[metadataCollectionsKey]; manifest != nil {
		if err := json.Unmarshal(manifest, &storedShapes); err != nil {
			return false, err
		}
	}
	raw, err = patch(root, storedShapes)
	if err != nil {
		return false, err
	}
	if len(raw) > s.maxStateBytes {
		return false, fmt.Errorf("workspace state exceeds %d bytes", s.maxStateBytes)
	}
	if previousID == target.Workspace.ID && bytes.Equal(previousRoot, raw) {
		return true, nil
	}
	tx.metadataWritten = true
	_, err = tx.ExecContext(ctx, `UPDATE workspace_states SET workspace_id=?,data=?,updated_at=? WHERE workspace_key=?`,
		target.Workspace.ID, raw, time.Now().UTC().Format(time.RFC3339Nano), workspace)
	return true, err
}
