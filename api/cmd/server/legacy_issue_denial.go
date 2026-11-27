package main

import (
	"net/http"
)

func emptyLegacyIssueRequest(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	tails := []string{"permissions", "suggestions"}
	if r.Method == http.MethodHead {
		tails = append(tails, "comment-actions")
	}
	for _, tail := range tails {
		if _, matched := scaleEscapedRouteParts(r, "/api/issues/{id}/"+tail); matched {
			return true
		}
	}
	return false
}

// Ordinary paged metadata has no Issues and fails the literal issue-index
// check. Nonempty borrowed sources retain the existing context-sensitive path.
func (s *server) boundedEmptyLegacyIssueDenial(r *http.Request, workspace string) (allowed, handled bool) {
	if !s.store.BoundedMetadataEnabled() || !emptyLegacyIssueRequest(r) || isIssueRecordsRequest(r) {
		return false, false
	}
	if s.store.BorrowedWorkspaceReadHasIssues(r.Context(), workspace) {
		return false, false
	}
	return false, true
}
