package store

import "testing"

func (s *SQLiteStore) ForgetInstalledMetadataRevisionForTesting(workspace string) error {
	if !testing.Testing() || s == nil {
		return ErrBoundedMetadataCoverage
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.forgetCopyRevisionLocked(workspace)
	return nil
}

func (s *SQLiteStore) MetadataWorkUsedForTesting() int {
	if !testing.Testing() || s == nil || s.metadataResidency == nil {
		return 0
	}
	work := s.metadataResidency.work
	work.mu.Lock()
	defer work.mu.Unlock()
	return work.used
}

func (r *ResourceRead) SourceRetainedForTesting() bool {
	if !testing.Testing() || r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.closed && r.source != nil
}
