package main

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// readLeafBusy reports a bounded read refusal. The legacy accessors had no
// budget; every other failure keeps the literal handler's response.
func readLeafBusy(w http.ResponseWriter, err error) bool {
	if errors.Is(err, store.ErrMetadataWorkBudget) || errors.Is(err, store.ErrMetadataPageBudget) {
		w.Header().Set("Retry-After", "2")
		writeError(w, http.StatusServiceUnavailable, "The workspace is busy loading for other people; retry in a moment")
		return true
	}
	return false
}

// getBoundedSharedIssue owns the selected issue, its comments and the
// workspace through the final write. The public token is the only authority:
// no session or credential participates, and every failure other than a
// bounded refusal keeps BootstrapFor's indistinguishable 404.
func (s *server) getBoundedSharedIssue(w http.ResponseWriter, r *http.Request) {
	workspace := strings.TrimSpace(r.URL.Query().Get("workspace"))
	if workspace == "" {
		writeError(w, http.StatusBadRequest, "workspace is required")
		return
	}
	read, err := s.store.BeginSharedIssueRead(r.Context(), workspace, r.PathValue("token"))
	if err != nil {
		if errors.Is(err, context.Canceled) || readLeafBusy(w, err) {
			return
		}
		writeSharedResourceNotFound(w)
		return
	}
	defer read.Close()
	if !read.Found {
		writeSharedResourceNotFound(w)
		return
	}
	item, comments := read.Issue, read.Comments
	// Shared links intentionally return a narrow read-only projection. The
	// token itself is not echoed back and private team membership is omitted.
	item.ShareToken, item.SharedAt = "", nil
	item.Team, item.Assignee, item.Delegate = domain.Team{}, nil, nil
	item.Creator.Email = ""
	if comments == nil {
		comments = []domain.Comment{}
	}
	for index := range comments {
		comments[index].User.Email = ""
	}
	result := map[string]any{"issue": item, "comments": comments, "workspace": read.Workspace}
	if err := read.ReserveOutput(r.Context(), result); err != nil {
		if errors.Is(err, context.Canceled) || readLeafBusy(w, err) {
			return
		}
		writeSharedResourceNotFound(w)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// boundedFilterSuggestions counts facets over every issue the literal
// workspaceData projection keeps. The accumulator, ordering and top-50 cut
// are the literal handler's; only the issue source is bounded.
func (s *server) boundedFilterSuggestions(w http.ResponseWriter, r *http.Request) {
	userID := ""
	var credential *domain.APIKey
	if !s.authDisabled {
		userID = authUser(r).ID
		if userID == "" {
			// BootstrapForUser refuses a missing principal; the literal
			// handler then counts an empty projection.
			writeJSON(w, http.StatusOK, []domain.FilterSuggestion{})
			return
		}
		if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok {
			credential = &key
		}
	}
	read, err := s.store.BeginIssueFacetRead(r.Context(), workspaceKey(r), userID, credential)
	if err != nil {
		if errors.Is(err, context.Canceled) || readLeafBusy(w, err) {
			return
		}
		// workspaceData ignores its accessor's failure and counts nothing.
		writeJSON(w, http.StatusOK, []domain.FilterSuggestion{})
		return
	}
	defer read.Close()
	query := normalizeSearch(r.URL.Query().Get("q"))
	field := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("field")))
	values := map[string]map[string]*domain.FilterSuggestion{}
	if err := read.ReserveOutput(r.Context(), 64<<10); err != nil {
		if !errors.Is(err, context.Canceled) && !readLeafBusy(w, err) {
			writeJSON(w, http.StatusOK, []domain.FilterSuggestion{})
		}
		return
	}
	entry := uint64(reflect.TypeFor[domain.FilterSuggestion]().Size())*8 + 2048
	err = read.Visit(r.Context(), func(key, id, label string) error {
		if field != "" && field != key || query != "" && !strings.Contains(normalizeSearch(label), query) {
			return nil
		}
		if values[key] == nil {
			values[key] = map[string]*domain.FilterSuggestion{}
		}
		if values[key][id] == nil {
			if err := read.ReserveOutput(r.Context(), uint64(len(id)+len(label))*16+entry); err != nil {
				return err
			}
			id, label = strings.Clone(id), strings.Clone(label)
			values[key][id] = &domain.FilterSuggestion{Field: key, Value: id, Label: label}
		}
		values[key][id].Count++
		return nil
	})
	if err != nil {
		if !errors.Is(err, context.Canceled) && !readLeafBusy(w, err) {
			writeJSON(w, http.StatusOK, []domain.FilterSuggestion{})
		}
		return
	}
	result := []domain.FilterSuggestion{}
	for _, items := range values {
		for _, item := range items {
			result = append(result, *item)
		}
	}
	sortFilterSuggestions(result)
	if len(result) > 50 {
		result = result[:50]
	}
	if err := read.ReserveValue(r.Context(), result); err != nil {
		if !errors.Is(err, context.Canceled) && !readLeafBusy(w, err) {
			writeJSON(w, http.StatusOK, []domain.FilterSuggestion{})
		}
		return
	}
	writeJSON(w, http.StatusOK, result)
}
