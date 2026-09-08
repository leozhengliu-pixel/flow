package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type issueRecordAuthorizationContext struct{}

func issueRecordQueryOnly(r *http.Request) bool {
	if pagedRealtimeRequest(r) {
		return true
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if r.Method == http.MethodGet && (len(parts) == 3 || len(parts) == 4 && parts[3] == "context") {
		return true
	}
	return r.URL.Path == "/api/issue-records" || r.URL.Path == "/api/issue-records/groups" || r.URL.Path == "/api/issue-records/bootstrap"
}

func (s *server) issueRecordAuthorizationData(r *http.Request) (domain.Bootstrap, error) {
	metadata, query, err := s.issueRecordsQuery(r)
	if err != nil {
		return metadata, err
	}
	if !s.authDisabled {
		metadata, err = s.store.PagedWorkspaceMetadata(r.Context(), query.Workspace, authUser(r).ID)
		if err != nil {
			return metadata, err
		}
		filterBootstrapForAPIKey(&metadata, r)
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	ids := []string{}
	if len(parts) > 2 {
		if parts[2] != "batch" {
			ids = append(ids, parts[2])
		}
	}
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		var input map[string]json.RawMessage
		if peekRequestJSON(r, &input) {
			var batchIDs []string
			if json.Unmarshal(input["issueIds"], &batchIDs) == nil {
				ids = append(ids, batchIDs...)
			}
			for _, field := range []string{"parentId", "relatedIssueId"} {
				var id string
				if json.Unmarshal(input[field], &id) == nil && id != "" {
					ids = append(ids, id)
				}
			}
		}
	}
	query.Filter = store.IssueFilter{Field: "id", Values: ids}
	query.Archived = "all"
	if len(ids) > 1000 {
		return metadata, store.ErrIssueQuery
	}
	query.Limit = 500
	query.Cursor = ""
	query.GroupValue = nil
	query.TeamIDs = nil
	query.ProjectIDs = nil
	query.StateIDs = nil
	query.Text = ""
	page, err := s.store.QueryIssueRecords(r.Context(), query)
	if err != nil {
		return metadata, err
	}
	metadata.Issues = page.Items
	if page.HasMore {
		query.Cursor = page.NextCursor
		next, err := s.store.QueryIssueRecords(r.Context(), query)
		if err != nil {
			return metadata, err
		}
		metadata.Issues = append(metadata.Issues, next.Items...)
	}
	seen := map[string]bool{}
	for _, issue := range page.Items {
		seen[issue.ID] = true
		parent := issue.ParentID
		for parent != nil && *parent != "" && !seen[*parent] && len(seen) < 64 {
			ancestor, err := s.store.IssueRecord(r.Context(), query.Workspace, *parent)
			if err != nil {
				break
			}
			seen[ancestor.ID] = true
			metadata.Issues = append(metadata.Issues, ancestor)
			parent = ancestor.ParentID
		}
	}
	if len(parts) > 2 {
		comments, activities, err := s.store.IssueContent(r.Context(), query.Workspace, parts[2])
		if err != nil {
			return metadata, err
		}
		metadata.Comments[parts[2]] = comments
		metadata.Activities[parts[2]] = activities
	}
	return metadata, nil
}

func (s *server) issueRecordAlias(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := s.issueRecordAuthorizationData(r)
		if err != nil {
			issueRecordsError(w, err)
			return
		}
		ids := []string{}
		if id := r.PathValue("id"); id != "" {
			ids = append(ids, id)
		}
		if relationID := r.PathValue("relationId"); relationID != "" {
			for _, issue := range data.Issues {
				if issue.ID == r.PathValue("id") {
					for _, relation := range issue.Relations {
						if relation.ID == relationID {
							ids = append(ids, relation.RelatedIssueID)
						}
					}
				}
			}
		}
		if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
			var input map[string]json.RawMessage
			if peekRequestJSON(r, &input) {
				var batchIDs []string
				if json.Unmarshal(input["issueIds"], &batchIDs) == nil {
					ids = append(ids, batchIDs...)
				}
				for _, field := range []string{"relatedIssueId"} {
					var id string
					if json.Unmarshal(input[field], &id) == nil && id != "" {
						ids = append(ids, id)
					}
				}
			}
		}
		ctx := store.WithIssueRecordMutations(r.Context(), ids...)
		ctx = context.WithValue(ctx, issueRecordAuthorizationContext{}, data)
		handler(w, r.WithContext(ctx))
	}
}
