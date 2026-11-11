package store

import (
	"encoding/json"
	"reflect"

	"flow/api/internal/domain"
)

// MetadataRecord encodes the record with the given id from one metadata
// collection (its JSON name, e.g. "savedViews"), read under the cache's read
// lock without copying the collection. Realtime authorizes an event against
// the record it names, once per event, and must stay cheap.
func (s *SQLiteStore) MetadataRecord(workspace, field, id string) (json.RawMessage, bool) {
	if id == "" {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if workspace == "" {
		workspace = s.lastWorkspaceKey
	}
	data, ok := s.workspaces[workspace]
	if !ok {
		return nil, false
	}
	for _, candidate := range persistedMetadataFields() {
		if candidate.name != field {
			continue
		}
		list := reflect.ValueOf(&data).Elem().Field(candidate.index)
		if list.Kind() != reflect.Slice || list.Type().Elem().Kind() != reflect.Struct {
			return nil, false
		}
		idField, ok := list.Type().Elem().FieldByName("ID")
		if !ok || idField.Type.Kind() != reflect.String || len(idField.Index) != 1 {
			return nil, false
		}
		for i := 0; i < list.Len(); i++ {
			if list.Index(i).Field(idField.Index[0]).String() == id {
				raw, err := json.Marshal(list.Index(i).Interface())
				return raw, err == nil
			}
		}
		return nil, false
	}
	return nil, false
}

// TeamRowVisible reports whether the viewer of data (its viewer, role and the
// viewer's team memberships) sees a team's row: its labels, statuses,
// templates and settings, as the bootstrap projection keeps them.
func TeamRowVisible(data domain.Bootstrap, teamID, userID, role string) bool {
	return teamVisibleToUser(data, teamID, userID, role)
}
