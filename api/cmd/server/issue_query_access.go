package main

import (
	"net/http"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// These query helpers read only store and authDisabled from server. A cold
// caller supplies its retained legacy facade without copying server locks or
// changing the request's existing access-cache semantics.
func (s *server) issueRecordsQueryWithStore(r *http.Request, repo *store.SQLiteStore) (domain.Bootstrap, store.IssueRecordQuery, error) {
	if repo == s.store {
		return s.issueRecordsQuery(r)
	}
	scoped := &server{store: repo, authDisabled: s.authDisabled}
	return scoped.issueRecordsQuery(r)
}

func (s *server) requestIssueQueryAccessWithStore(r *http.Request, repo *store.SQLiteStore) (domain.Bootstrap, store.IssueRecordAccess, error) {
	if repo == s.store {
		return s.requestIssueQueryAccess(r)
	}
	scoped := &server{store: repo, authDisabled: s.authDisabled}
	return scoped.requestIssueQueryAccess(r)
}
