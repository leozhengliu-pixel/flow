package main

import (
	"bytes"
	"encoding/json"
)

// Merge objects recursively so independent preference updates do not reset
// unrelated fields. Arrays remain atomic selections rather than index patches.
func mergeSettingsPatch[T any](current T, patch map[string]json.RawMessage, protected ...string) (T, error) {
	for _, key := range protected {
		delete(patch, key)
	}
	encoded, err := json.Marshal(current)
	if err != nil {
		return current, err
	}
	merged, err := mergeSettingsObject(encoded, patch)
	if err != nil {
		return current, err
	}
	var result T
	decoder := json.NewDecoder(bytes.NewReader(merged))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return current, errInvalid
	}
	return result, nil
}

func mergeSettingsObject(current []byte, patch map[string]json.RawMessage) ([]byte, error) {
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(current, &fields); err != nil {
		return nil, err
	}
	for key, value := range patch {
		value = bytes.TrimSpace(value)
		if bytes.Equal(value, []byte("null")) || len(value) == 0 {
			return nil, errInvalid
		}
		if value[0] == '{' {
			child := map[string]json.RawMessage{}
			if err := json.Unmarshal(value, &child); err != nil {
				return nil, errInvalid
			}
			base := fields[key]
			if len(base) == 0 || bytes.Equal(base, []byte("null")) {
				base = []byte("{}")
			}
			merged, err := mergeSettingsObject(base, child)
			if err != nil {
				return nil, err
			}
			fields[key] = merged
		} else {
			fields[key] = value
		}
	}
	return json.Marshal(fields)
}
