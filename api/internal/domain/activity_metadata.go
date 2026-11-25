package domain

import (
	"encoding/json"
	"fmt"
)

// ActivityMetadata keeps scalar readers compatible while label deltas use
// arrays on the wire and in stored JSON.
type ActivityMetadata map[string]string

func (m ActivityMetadata) MarshalJSON() ([]byte, error) {
	if m == nil {
		return []byte("null"), nil
	}
	values := make(map[string]any, len(m))
	for key, value := range m {
		if key == "addedLabelIds" || key == "removedLabelIds" {
			ids := []string{}
			if err := json.Unmarshal([]byte(value), &ids); err != nil {
				return nil, err
			}
			values[key] = ids
		} else {
			values[key] = value
		}
	}
	return json.Marshal(values)
}

func (m *ActivityMetadata) UnmarshalJSON(raw []byte) error {
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return err
	}
	if values == nil {
		*m = nil
		return nil
	}
	result := ActivityMetadata{}
	for key, value := range values {
		var scalar string
		if err := json.Unmarshal(value, &scalar); err == nil {
			result[key] = scalar
			continue
		}
		if key != "addedLabelIds" && key != "removedLabelIds" {
			return fmt.Errorf("activity metadata %s must be a string", key)
		}
		var ids []string
		if err := json.Unmarshal(value, &ids); err != nil {
			return err
		}
		encoded, _ := json.Marshal(ids)
		result[key] = string(encoded)
	}
	*m = result
	return nil
}
