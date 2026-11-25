package store

// ReferenceVisible exposes the existing selected-credential reference policy
// for project and initiative output. Keep the read open through encoding.
func (r *NotificationVisibilityRead) ReferenceVisible(kind, ref string) (bool, error) {
	if r == nil || r.tx == nil || r.used || r.policy == nil || r.policy.key == nil || !r.policy.selected() {
		return false, ErrMetadataRecordQuery
	}
	if kind != "project" && kind != "initiative" {
		return false, ErrMetadataRecordQuery
	}
	return r.referenceVisible(kind, ref)
}

// FinishReferences closes the extraction snapshot and validates live authority.
// The caller must still Close after consuming its projected output.
func (r *NotificationVisibilityRead) FinishReferences() error {
	if r == nil {
		return ErrMetadataRecordQuery
	}
	return r.finish()
}
