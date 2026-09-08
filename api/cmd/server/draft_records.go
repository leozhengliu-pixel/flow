package main

import (
	"net/http"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func (s *server) draftRecordValidator(r *http.Request) (func(*domain.Bootstrap, domain.Draft) error, error) {
	metadata, query, err := s.issueRecordsQuery(r)
	if err != nil {
		return nil, err
	}
	if !s.authDisabled {
		metadata, err = s.store.PagedWorkspaceMetadata(r.Context(), query.Workspace, authUser(r).ID)
		if err != nil {
			return nil, err
		}
		filterBootstrapForAPIKey(&metadata, r)
	}
	return func(_ *domain.Bootstrap, draft domain.Draft) error {
		var queryError error
		err := validateDraftResource(&metadata, draft, func(data *domain.Bootstrap, kind, id string) bool {
			if kind != "issue" {
				return resourceExists(data, kind, id)
			}
			q := query
			q.Filter, q.Archived, q.Limit = store.IssueFilter{Field: "id", Values: []string{id}}, "all", 1
			page, err := s.store.QueryIssueRecords(r.Context(), q)
			queryError = err
			return err == nil && len(page.Items) == 1
		})
		if queryError != nil {
			return queryError
		}
		return err
	}, nil
}
