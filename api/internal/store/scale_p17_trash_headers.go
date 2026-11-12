package store

import (
	"reflect"

	"flow/api/internal/domain"
)

// TrashRecordHeader never copies a restore payload or its private links.
func (s *SQLiteStore) TrashRecordHeader(workspace, id string) (domain.TrashEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, ok := s.workspaces[workspace]
	if !ok {
		return domain.TrashEntry{}, false
	}
	for _, entry := range data.Trash {
		if entry.ID == id {
			entry.Payload, entry.Links = nil, nil
			return cloneSnapshotValue(reflect.ValueOf(entry)).Interface().(domain.TrashEntry), true
		}
	}
	return domain.TrashEntry{}, false
}
