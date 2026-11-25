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

// MetadataWorkPeakForTesting returns the highest admitted metadata work
// since the previous call and restarts the measurement from the current use.
func (s *SQLiteStore) MetadataWorkPeakForTesting() int {
	if !testing.Testing() || s == nil || s.metadataResidency == nil {
		return 0
	}
	work := s.metadataResidency.work
	work.mu.Lock()
	defer work.mu.Unlock()
	peak := work.peak
	work.peak = work.used
	return peak
}

// SetMetadataWorkLimitForTesting replaces the metadata work limit until the
// returned restore runs.
func (s *SQLiteStore) SetMetadataWorkLimitForTesting(limit int) func() {
	if !testing.Testing() || s == nil || s.metadataResidency == nil {
		return func() {}
	}
	work := s.metadataResidency.work
	work.mu.Lock()
	previous := work.limit
	work.limit = limit
	work.mu.Unlock()
	return func() {
		work.mu.Lock()
		work.limit = previous
		work.mu.Unlock()
	}
}

func (r *ResourceRead) SourceRetainedForTesting() bool {
	if !testing.Testing() || r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.closed && r.source != nil
}
