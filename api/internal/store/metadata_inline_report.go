package store

import (
	"encoding/json"
	"reflect"
	"sort"

	"flow/api/internal/domain"
)

// InlineMetadataField describes a workspace metadata collection whose stored
// root does not keep one row per entry, because an entry's ID (or a map key)
// is shared, empty or longer than a row key. Writes before the inline
// metadata fixes could lose edits to such a field.
type InlineMetadataField struct {
	Field string `json:"field"`
	// Kind is list, map or updates (a per-project update collection).
	Kind string `json:"kind"`
	// Stored is inline (the whole field in the root document) or
	// perParent (an update collection stored one row per project).
	Stored       string `json:"stored"`
	Entries      int    `json:"entries"`
	DuplicateIDs int    `json:"duplicateIds"`
	EmptyIDs     int    `json:"emptyIds"`
	OverlongIDs  int    `json:"overlongIds"`
	Unreadable   int    `json:"unreadable"`
}

// InlineMetadataFields reads one stored workspace root (workspace_states.data)
// and lists its non-empty collections that are not kept one row per entry,
// with the entries that keep them so. An update collection stored per
// project keeps its lists in rows: perParent returns them (record_key ->
// data of workspace_metadata_records for that field). It only reads.
func InlineMetadataFields(root []byte, perParent func(field string) (map[string]json.RawMessage, error)) ([]InlineMetadataField, error) {
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(root, &fields); err != nil {
		return nil, err
	}
	shapes := map[string]string{}
	if manifest := fields[metadataCollectionsKey]; len(manifest) > 0 {
		if err := json.Unmarshal(manifest, &shapes); err != nil {
			return nil, err
		}
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	var result []InlineMetadataField
	for _, name := range names {
		value := fields[name]
		if name == metadataCollectionsKey || name == "workspace" || name == "viewer" || len(value) == 0 || value[0] != '[' && value[0] != '{' {
			continue
		}
		kind, want := "map", "map"
		if value[0] == '[' {
			kind, want = "list", "array"
		}
		if updateMetadataField(name) {
			kind, want = "updates", "updates"
		} else if field, ok := metadataFieldByName(name); ok {
			if reflect.TypeFor[domain.Bootstrap]().Field(field.index).Type.Kind() == reflect.Slice {
				kind, want = "list", "array"
			}
		}
		if shapes[name] == want {
			continue
		}
		report := InlineMetadataField{Field: name, Kind: kind, Stored: "inline"}
		if kind == "updates" && shapes[name] == "map" {
			report.Stored = "perParent"
		}
		countIDs := func(items []json.RawMessage, overlong bool) {
			seen := map[string]bool{}
			for _, item := range items {
				report.Entries++
				var entity struct {
					ID *string `json:"id"`
				}
				if json.Unmarshal(item, &entity) != nil {
					report.Unreadable++
					continue
				}
				id := ""
				if entity.ID != nil {
					id = *entity.ID
				}
				switch {
				case id == "":
					report.EmptyIDs++
				case seen[id]:
					report.DuplicateIDs++
				case overlong && len(id) > 191:
					report.OverlongIDs++
				}
				seen[id] = true
			}
		}
		switch kind {
		case "list":
			var items []json.RawMessage
			if json.Unmarshal(value, &items) != nil {
				report.Unreadable++
				break
			}
			if len(items) == 0 {
				continue
			}
			countIDs(items, true)
		default:
			var items map[string]json.RawMessage
			if report.Stored == "perParent" {
				if perParent == nil {
					continue
				}
				var err error
				if items, err = perParent(name); err != nil {
					return nil, err
				}
			} else if json.Unmarshal(value, &items) != nil {
				report.Unreadable++
				break
			}
			if len(items) == 0 {
				continue
			}
			keys := make([]string, 0, len(items))
			for key := range items {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				if len(key) > 191 {
					report.OverlongIDs++
				}
				if kind != "updates" {
					report.Entries++
					continue
				}
				var updates []json.RawMessage
				if string(items[key]) == "null" {
					continue
				}
				if json.Unmarshal(items[key], &updates) != nil {
					report.Unreadable++
					continue
				}
				countIDs(updates, false)
			}
		}
		result = append(result, report)
	}
	return result, nil
}
