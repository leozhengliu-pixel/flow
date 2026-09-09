package main

import (
	"flow/api/internal/domain"
	"flow/api/internal/store"
	"fmt"
	"net/http"
)

func (s *server) agentIssueContext(r *http.Request, ids []string) (domain.Bootstrap, error) {
	data, q, err := s.pagedRealtimeMetadata(r)
	if err != nil {
		return data, err
	}
	ids = uniqueAgentIDs(ids)
	if len(ids) > 25 {
		return data, fmt.Errorf("no more than 25 issue IDs are allowed")
	}
	if len(ids) == 0 {
		return data, nil
	}
	q.Filter = store.IssueFilter{Field: "id", Values: ids}
	q.Archived = "all"
	q.Limit = 25
	q.Text = ""
	q.Cursor = ""
	page, err := s.store.QueryIssueRecords(r.Context(), q)
	if err != nil {
		return data, err
	}
	if len(page.Items) != len(ids) {
		return data, fmt.Errorf("one or more selected issues were not found")
	}
	data.Issues, err = s.projectIssueRecordReferences(r, data, q, page.Items)
	return data, err
}
