package store

import (
	"encoding/json"
	"reflect"
	"slices"

	"flow/api/internal/domain"
)

// The logical representation changes the source, not the generic adapter's
// input contract. Large/full scans use its explicitly owned streaming API.
func validateMetadataLogicalSelection(data domain.Bootstrap, scope MetadataRecordScope) error {
	count := 0
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	budget := newMetadataHydrationBudget()
	if err := budget.consume(raw); err != nil {
		return err
	}
	_, shapes, records, err := splitWorkspaceMetadata(raw)
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
					encoded, err := json.Marshal(items.Interface())
					if err != nil {
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
