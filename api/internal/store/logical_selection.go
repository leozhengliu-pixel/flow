package store

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"

	"flow/api/internal/domain"
)

// The logical representation changes the source, not the generic adapter's
// input contract. Large/full scans use its explicitly owned streaming API.
func (s *SQLiteStore) validateMetadataLogicalSelection(ctx context.Context, data domain.Bootstrap, scope MetadataRecordScope) error {
	work := &ResourceRead{store: s}
	defer work.Close()
	count := 0
	_, shapes, records, err := splitMetadataImage(ctx, work, reflect.ValueOf(data), true)
	if err != nil {
		return err
	}
	for key := range records {
		if metadataRecordSelected(scope, key.field, key.key, false) {
			count++
		}
	}
	value := reflect.ValueOf(data)
	for _, name := range metadataRecordFields(scope) {
		field, ok := metadataFieldByName(name)
		if !ok {
			return ErrMetadataRecordQuery
		}
		if shapes[name] != "" {
			continue
		}
		items := value.Field(field.index)
		switch items.Kind() {
		case reflect.Slice, reflect.Map:
			count += items.Len()
		case reflect.Struct:
			if name == "workspaceSettings" {
				if slices.Contains(scope.Fields, name) {
					var object map[string]json.RawMessage
					encoded, err := encodeOwnedMetadataRecord(ctx, work, items)
					if err != nil {
						return err
					}
					if err := reserveLegacyMetadataSplit(ctx, work, encoded); err != nil {
						return err
					}
					if err := json.Unmarshal(encoded, &object); err != nil {
						return err
					}
					count += len(object)
				} else {
					for _, key := range append(slices.Clone(scope.Read), scope.Write...) {
						if key.Kind == name {
							count++
						}
					}
				}
			}
		}
		if count > MetadataRecordBatchMaxKeys {
			return ErrMetadataPageBudget
		}
	}
	if count > MetadataRecordBatchMaxKeys {
		return ErrMetadataPageBudget
	}
	return nil
}
