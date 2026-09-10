package main

import (
	"database/sql"
	"errors"
	"net/http"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func issueDetailReadError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "issue not found")
		return
	}
	issueRecordsError(w, err)
}

func (s *server) getIssueRecordHistory(w http.ResponseWriter, r *http.Request) {
	_, query, err := s.issueRecordsQuery(r)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	id, err := s.store.AuthorizedIssueRecordID(r.Context(), query, r.PathValue("id"))
	if err != nil {
		issueDetailReadError(w, err)
		return
	}
	history, err := s.store.IssueHistoryPage(r.Context(), query.Workspace, id, r.URL.Query().Get("commentsCursor"), r.URL.Query().Get("activitiesCursor"))
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, history)
}

func (s *server) getIssueRecordRelated(w http.ResponseWriter, r *http.Request) {
	metadata, query, err := s.issueRecordsQuery(r)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	issue, err := s.store.AuthorizedIssueRecord(r.Context(), query, r.PathValue("id"))
	if err != nil {
		issueDetailReadError(w, err)
		return
	}
	ids := append([]string(nil), issue.SubIssueIDs...)
	if issue.ParentID != nil {
		ids = append(ids, *issue.ParentID)
	}
	if len(ids) == 0 {
		writeJSON(w, http.StatusOK, []domain.Issue{})
		return
	}
	if len(ids) > 500 {
		ids = ids[:500]
	}
	query.Filter = store.IssueFilter{Field: "id", Values: ids}
	query.Archived = "all"
	query.Limit = 500
	query.Summary = true
	page, err := s.store.QueryIssueRecords(r.Context(), query)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	items, err := s.projectIssueRecordReferences(r, metadata, query, page.Items)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *server) issueRecordVisibility(w http.ResponseWriter, r *http.Request) {
	var input struct {
		IDs []string `json:"ids"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if len(input.IDs) > 2000 {
		writeError(w, http.StatusBadRequest, "too many issue IDs")
		return
	}
	_, query, err := s.issueRecordsQuery(r)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	visible, err := s.store.VisibleIssueRecordIDs(r.Context(), query, input.IDs)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	ids := []string{}
	for _, id := range input.IDs {
		if visible[id] {
			ids = append(ids, id)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ids": ids})
}
