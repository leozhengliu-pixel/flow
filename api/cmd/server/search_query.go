package main

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func (s *server) searchQuery(r *http.Request) (domain.Bootstrap, store.IssueRecordQuery, error) {
	if len(r.URL.Query().Get("q")) > 1024 {
		return domain.Bootstrap{}, store.IssueRecordQuery{}, store.ErrIssueQuery
	}
	data, q, err := s.issueRecordsQuery(r)
	if err != nil {
		return data, q, err
	}
	if s.authDisabled {
		if viewer, ok := s.store.WorkspaceSearchViewer(q.Workspace); ok {
			data.Viewer = viewer
		}
	}
	q.Archived = "false"
	if r.URL.Query().Get("includeArchived") == "true" {
		q.Archived = "all"
	}
	nodes := []store.IssueFilter{q.Filter}
	if len(q.StateIDs) > 0 {
		nodes = append(nodes, store.IssueFilter{Field: "stateId", Values: q.StateIDs})
	}
	for _, item := range []struct{ parameter, field string }{{"statusType", "statusType"}, {"assigneeId", "assignee"}, {"creatorId", "creator"}} {
		if value := r.URL.Query().Get(item.parameter); value != "" {
			nodes = append(nodes, store.IssueFilter{Field: item.field, Values: splitQueryValues(value)})
		}
	}
	for _, item := range []struct{ parameter, field, operator string }{{"createdAfter", "createdAt", "gte"}, {"createdBefore", "createdAt", "lte"}, {"updatedAfter", "updatedAt", "gte"}, {"updatedBefore", "updatedAt", "lte"}} {
		if value := r.URL.Query().Get(item.parameter); value != "" {
			date, err := time.Parse(time.RFC3339Nano, value)
			if err != nil {
				date, err = time.Parse("2006-01-02", value)
			}
			if err != nil {
				return data, q, store.ErrIssueQuery
			}
			if len(value) == 10 && item.operator == "lte" {
				date = date.Add(24*time.Hour - time.Nanosecond)
			}
			nodes = append(nodes, store.IssueFilter{Field: item.field, Operator: item.operator, Values: []string{date.UTC().Format(time.RFC3339Nano)}})
		}
	}
	q.Filter = store.IssueFilter{And: nodes}
	var normalize func(*store.IssueFilter)
	var invalidDate bool
	normalize = func(node *store.IssueFilter) {
		for i, value := range node.Values {
			if value == "none" && slices.Contains([]string{"assignee", "assigneeId", "creator", "creatorId", "project", "projectId", "cycle", "cycleId", "parent"}, node.Field) {
				node.Values[i] = ""
			}
			if node.Field == "createdAt" || node.Field == "updatedAt" {
				date, err := time.Parse(time.RFC3339Nano, value)
				if err != nil {
					date, err = time.Parse("2006-01-02", value)
				}
				if err != nil {
					invalidDate = true
				} else {
					node.Values[i] = date.UTC().Format(time.RFC3339Nano)
				}
			}
		}
		for i := range node.And {
			normalize(&node.And[i])
		}
		for i := range node.Or {
			normalize(&node.Or[i])
		}
	}
	normalize(&q.Filter)
	if invalidDate {
		return data, q, store.ErrIssueQuery
	}
	if err := store.ValidateIssueFilter(q.Filter); err != nil {
		return data, q, err
	}
	q.Sort = r.URL.Query().Get("sort")
	if q.Sort == "relevance" {
		q.Sort = ""
	}
	if q.Sort != "" && !slices.Contains([]string{"title", "createdAt", "updatedAt"}, q.Sort) {
		return data, q, store.ErrIssueQuery
	}
	q.Direction = "desc"
	if q.Sort == "title" {
		q.Direction = "asc"
	}
	return data, q, nil
}

func searchResultOrder(results []domain.SearchResult, q store.IssueRecordQuery) {
	if q.Sort == "" {
		sortSearchResults(results)
		return
	}
	slices.SortStableFunc(results, func(a, b domain.SearchResult) int {
		if q.Sort == "title" {
			return strings.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title))
		}
		if q.Sort == "createdAt" {
			return b.CreatedAt.Compare(a.CreatedAt)
		}
		return b.UpdatedAt.Compare(a.UpdatedAt)
	})
}

func enrichSearchResults(results []domain.SearchResult, data domain.Bootstrap) {
	issues := map[string]domain.Issue{}
	projects := map[string]domain.Project{}
	created := map[string]time.Time{}
	for _, item := range data.Documents {
		created[item.ID] = item.CreatedAt
	}
	for _, item := range data.Initiatives {
		created[item.ID] = item.CreatedAt
	}
	for _, item := range data.Releases {
		created[item.ID] = item.CreatedAt
	}
	for _, item := range data.SavedViews {
		created[item.ID] = item.CreatedAt
	}
	for _, item := range data.Customers {
		created[item.ID] = item.CreatedAt
	}
	for _, issue := range data.Issues {
		issues[issue.ID] = issue
	}
	for _, project := range data.Projects {
		projects[project.ID] = project
	}
	for i := range results {
		r := &results[i]
		r.CreatedAt = created[r.ID]
		if issue, ok := issues[r.ID]; r.Type == "issue" && ok {
			state := issue.State
			r.State = &state
			r.StatusType = state.Type
			r.StatusName = state.Name
			r.CreatedAt = issue.CreatedAt
		}
		if project, ok := projects[r.ID]; r.Type == "project" && ok {
			status := project.Status
			r.ProjectStatus = &status
			r.StatusType = status.Type
			r.StatusName = status.Name
			r.CreatedAt = project.CreatedAt
		}
	}
}

func (s *server) attachSearchResultLinks(r *http.Request, results []domain.SearchResult, data domain.Bootstrap) {
	root := "/" + url.PathEscape(data.Workspace.URLKey)
	for i := range results {
		result := &results[i]
		slug := result.ID
		switch result.Type {
		case "issue":
			result.URL = root + "/issue/" + url.PathEscape(result.Identifier) + "/" + url.PathEscape(result.Title)
		case "project":
			for _, item := range data.Projects {
				if item.ID == result.ID {
					slug = item.SlugID
					result.CreatedAt = item.CreatedAt
					break
				}
			}
			if slug == "" {
				slug = result.ID
			}
			result.SlugID = slug
			result.URL = root + "/project/" + url.PathEscape(slug) + "/overview"
		case "initiative":
			for _, item := range data.Initiatives {
				if item.ID == result.ID {
					slug = item.SlugID
					result.CreatedAt = item.CreatedAt
					break
				}
			}
			if slug == "" {
				slug = result.ID
			}
			result.SlugID = slug
			result.URL = root + "/initiative/" + url.PathEscape(slug) + "/overview"
		case "document":
			for _, item := range data.Documents {
				if item.ID == result.ID {
					slug = item.SlugID
					result.CreatedAt = item.CreatedAt
					break
				}
			}
			if slug == "" {
				slug = result.ID
			}
			result.SlugID = slug
			result.URL = root + "/document/" + url.PathEscape(slug)
		case "view":
			for _, item := range data.SavedViews {
				if item.ID != result.ID {
					continue
				}
				slug = item.SlugID
				if slug == "" {
					slug = item.ID
				}
				result.SlugID = slug
				result.CreatedAt = item.CreatedAt
				prefix := root
				if item.Resource == "projects" {
					prefix += "/projects"
				}
				if item.TeamID != "" {
					for _, team := range data.Teams {
						if team.ID == item.TeamID {
							prefix = root + "/team/" + url.PathEscape(team.Key)
							if item.Resource == "projects" {
								prefix += "/projects"
							}
						}
					}
				}
				if item.ProjectID != "" {
					if project, err := s.store.SearchResourceSlug(r.Context(), data.Workspace.URLKey, "projects", item.ProjectID); err == nil {
						prefix = root + "/project/" + url.PathEscape(project)
					}
				}
				result.URL = prefix + "/view/" + url.PathEscape(slug)
				break
			}
		case "release":
			for _, item := range data.Releases {
				if item.ID != result.ID {
					continue
				}
				slug = item.SlugID
				if slug == "" {
					slug = item.ID
				}
				result.SlugID = slug
				result.CreatedAt = item.CreatedAt
				if pipeline, err := s.store.SearchResourceSlug(r.Context(), data.Workspace.URLKey, "releasePipelines", item.PipelineID); err == nil {
					result.URL = root + "/pipeline/" + url.PathEscape(pipeline) + "/release/" + url.PathEscape(slug) + "/issues"
				}
				break
			}
		}
	}
}
