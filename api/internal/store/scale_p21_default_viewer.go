package store

import "flow/api/internal/domain"

// DefaultViewer returns the same shallow user value as Account().Viewer.
func (s *SQLiteStore) DefaultViewer() domain.User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.viewer
}
