package store

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"flow/api/internal/domain"
)

const metadataLogicalInlineWorkLimit = 2 << 20

// persistMetadataLogicalInlineField uses the existing field-delta writer,
// preserving its root merge, null/empty shapes, timestamps and journal hooks.
// The current scoped producer already requires a root of at most 256 KiB.
// This adapter also preflights its decoded field and encoding work separately.
func (s *SQLiteStore) persistMetadataLogicalInlineField(ctx context.Context, tx *sqlTx, workspace, name string, before, after metadataLogicalNode) error {
	field, ok := metadataFieldByName(name)
	if !ok || before.typ != after.typ || before.typ != reflect.TypeFor[domain.Bootstrap]().Field(field.index).Type {
		return ErrMetadataRecordQuery
	}
	var rootBytes int64
	lock := ""
	if tx.dialect != "sqlite" {
		lock = " FOR UPDATE"
	}
	if err := tx.QueryRowContext(ctx, "SELECT LENGTH(data) FROM workspace_states WHERE workspace_key=?"+lock, workspace).Scan(&rootBytes); err != nil {
		return err
	}
	if rootBytes < 0 || rootBytes > MetadataRecordPageMaxBytes ||
		before.cost > metadataLogicalInlineWorkLimit || after.cost > metadataLogicalInlineWorkLimit {
		return ErrMetadataWorkBudget
	}
	required := (before.cost+after.cost)*8 + uint64(rootBytes)*12 + 64<<10
	if required > metadataLogicalInlineWorkLimit {
		return ErrMetadataWorkBudget
	}
	_, release, err := s.tryAdmitAdditionalMetadataWork(ctx, int(required))
	if err != nil {
		return err
	}
	defer release()
	var workspaceID string
	var raw []byte
	if err := tx.QueryRowContext(ctx, "SELECT workspace_id,CASE WHEN LENGTH(data)<=? THEN data ELSE NULL END FROM workspace_states WHERE workspace_key=?"+lock,
		MetadataRecordPageMaxBytes, workspace).Scan(&workspaceID, &raw); err != nil {
		return err
	}
	if raw == nil || int64(len(raw)) != rootBytes {
		return ErrConcurrentWrite
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return err
	}
	var shapes map[string]string
	if encoded := root[metadataCollectionsKey]; encoded != nil {
		if err := json.Unmarshal(encoded, &shapes); err != nil {
			return err
		}
	}
	if shapes[name] != "" {
		return fmt.Errorf("%w: %s is a split field, not an inline field", ErrBoundedMetadataCoverage, name)
	}
	var splitRows int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM workspace_metadata_records WHERE workspace_key=? AND field=?", workspace, name).Scan(&splitRows); err != nil {
		return err
	}
	if splitRows != 0 {
		return fmt.Errorf("%w: inline %s has unexpected split rows", ErrBoundedMetadataCoverage, name)
	}
	oldValue, err := before.Value(ctx, int(required))
	if err != nil {
		return err
	}
	nextValue, err := after.Value(ctx, int(required))
	if err != nil {
		return err
	}
	oldData := domain.Bootstrap{Workspace: domain.Workspace{ID: workspaceID}}
	nextData := oldData
	reflect.ValueOf(&oldData).Elem().Field(field.index).Set(oldValue)
	reflect.ValueOf(&nextData).Elem().Field(field.index).Set(nextValue)
	_, err = s.writeWorkspaceMetadataDelta(ctx, tx, workspace, oldData, &nextData)
	return err
}
