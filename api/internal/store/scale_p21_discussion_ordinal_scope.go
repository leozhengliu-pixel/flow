package store

import (
	"errors"

	"flow/api/internal/domain"
)

// The issue writer already owns every native subscription occurrence for one
// issue. Its ordinal publisher handles scopes that cannot be represented by
// short unique point keys, without declaring that subset a complete field.
func discussionNeedsOrdinalScope(before, next domain.Bootstrap, issueID string, scope MetadataRecordScope, cause error) bool {
	if issueID == "" || len(scope.Read) != 0 || len(scope.Fields) != 0 {
		return false
	}
	invalidKey := false
	for _, key := range scope.Write {
		if key.Kind != "threadSubscriptions" {
			return false
		}
		invalidKey = invalidKey || !validMetadataKey(key)
	}
	if !(errors.Is(cause, ErrMetadataPageBudget) && len(scope.Write) > MetadataRecordBatchMaxKeys ||
		errors.Is(cause, ErrMetadataRecordQuery) && invalidKey) {
		return false
	}
	for _, field := range changedMetadataFields(before, next) {
		if field.name != "threadSubscriptions" {
			return false
		}
	}
	return validateDiscussionThreadEdits(before.ThreadSubscriptions, next.ThreadSubscriptions, issueID) == nil
}
