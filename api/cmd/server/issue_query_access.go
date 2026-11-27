package main

import (
	"net/http"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Issue query helpers may read a request-scoped legacy facade. The reader
// cannot start workers or register callbacks on the facade's owning store.
type issueQueryReader struct {
	store        *store.SQLiteStore
	authDisabled bool
}

func (s *server) issueRecordsQueryWithStore(r *http.Request, repo *store.SQLiteStore) (domain.Bootstrap, store.IssueRecordQuery, error) {
	return (issueQueryReader{store: repo, authDisabled: s.authDisabled}).issueRecordsQuery(r)
}

func (s *server) requestIssueQueryAccessWithStore(r *http.Request, repo *store.SQLiteStore) (domain.Bootstrap, store.IssueRecordAccess, error) {
	return (issueQueryReader{store: repo, authDisabled: s.authDisabled}).requestIssueQueryAccess(r)
}
