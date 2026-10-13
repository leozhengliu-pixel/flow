package main

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/publicapi"
)

// The connection form of GET /api/issue-records: Relay-style first/after and
// last/before paging over the same indexed keyset query as the legacy
// limit/cursor form. The cursor is the store's opaque token, bound to the
// filter and ordering it was issued for; pageInfo.startCursor and endCursor
// serve before and after alike.

const maxIssuePageSize = publicapi.MaxIssuePageSize

// issueOrderings are the orderBy values of the connection form.
var issueOrderings = []string{"createdAt", "updatedAt", "priority", "title", "sortOrder"}

type issueConnectionArgs struct {
	Size      int
	Backward  bool
	Cursor    string
	Sort      string
	Direction string
	Archived  string
}

func parseIssueConnectionArgs(r *http.Request) (issueConnectionArgs, error) {
	query := r.URL.Query()
	args := issueConnectionArgs{Size: defaultPageSize}
	size := func(names ...string) (int, bool, error) {
		for _, name := range names {
			raw := strings.TrimSpace(query.Get(name))
			if raw == "" {
				continue
			}
			value, err := strconv.Atoi(raw)
			if err != nil || value < 1 || value > maxIssuePageSize {
				return 0, true, fmt.Errorf("%w: %s must be between 1 and %d", errPageArgs, name, maxIssuePageSize)
			}
			return value, true, nil
		}
		return 0, false, nil
	}
	first, hasFirst, err := size("first", "limit")
	if err != nil {
		return args, err
	}
	last, hasLast, err := size("last")
	if err != nil {
		return args, err
	}
	after, before := query.Get("after"), query.Get("before")
	if after == "" {
		after = query.Get("cursor")
	}
	switch {
	case hasFirst && hasLast:
		return args, fmt.Errorf("%w: use either first or last, not both", errPageArgs)
	case after != "" && before != "":
		return args, fmt.Errorf("%w: use either after or before, not both", errPageArgs)
	case hasLast && after != "":
		return args, fmt.Errorf("%w: last pages backward from before, not after", errPageArgs)
	case hasFirst && before != "":
		return args, fmt.Errorf("%w: first pages forward from after, not before", errPageArgs)
	}
	switch {
	case hasLast:
		args.Size, args.Backward = last, true
	case hasFirst:
		args.Size = first
	case before != "":
		args.Backward = true
	}
	args.Cursor = after
	if before != "" {
		args.Cursor = before
	}
	args.Sort = strings.TrimSpace(query.Get("orderBy"))
	if args.Sort == "" {
		args.Sort = strings.TrimSpace(query.Get("sort"))
	}
	if args.Sort == "" {
		args.Sort = "createdAt"
	}
	if !slices.Contains(issueOrderings, args.Sort) {
		return args, fmt.Errorf("%w: orderBy must be one of %s", errPageArgs, strings.Join(issueOrderings, ", "))
	}
	args.Direction = strings.ToLower(strings.TrimSpace(query.Get("orderDirection")))
	if args.Direction == "" {
		args.Direction = strings.ToLower(strings.TrimSpace(query.Get("direction")))
	}
	switch args.Direction {
	case "asc", "desc":
	case "":
		// Timestamps are newest first like every connection; the other
		// orderings keep the issue query defaults.
		if args.Sort == "createdAt" || args.Sort == "updatedAt" {
			args.Direction = "desc"
		} else {
			args.Direction = "asc"
		}
	default:
		return args, fmt.Errorf("%w: orderDirection must be asc or desc", errPageArgs)
	}
	args.Archived = query.Get("archived")
	switch args.Archived {
	case "", "true", "false", "all":
	default:
		return args, fmt.Errorf("%w: archived must be true, false or all", errPageArgs)
	}
	if raw := strings.TrimSpace(query.Get("includeArchived")); raw != "" {
		include, err := strconv.ParseBool(raw)
		if err != nil {
			return args, fmt.Errorf("%w: includeArchived must be true or false", errPageArgs)
		}
		if include && args.Archived == "" {
			args.Archived = "all"
		}
	}
	return args, nil
}

func (s *server) listIssueRecordsConnection(w http.ResponseWriter, r *http.Request) {
	args, err := parseIssueConnectionArgs(r)
	if err != nil {
		writePageArgsError(w, err)
		return
	}
	metadata, query, err := s.issueRecordsQuery(r)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	query.Sort, query.Direction, query.Limit, query.Archived = args.Sort, args.Direction, args.Size, args.Archived
	query.Cursor, query.Reverse, query.Cursors = args.Cursor, args.Backward, true
	page, err := s.store.QueryIssueRecords(r.Context(), query)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	page.Items, err = s.projectIssueRecordReferences(r, metadata, query, page.Items)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	result := publicapi.Connection[domain.Issue]{Nodes: page.Items, PageInfo: pageInfo{HasNextPage: page.HasMore, HasPreviousPage: page.HasPrevious}}
	if result.Nodes == nil {
		result.Nodes = []domain.Issue{}
	}
	if page.StartCursor != "" {
		start, end := page.StartCursor, page.EndCursor
		result.PageInfo.StartCursor, result.PageInfo.EndCursor = &start, &end
	}
	if query.IncludeTotal && page.Total >= 0 {
		total := page.Total
		result.TotalCount = &total
	}
	writeJSON(w, http.StatusOK, result)
}
