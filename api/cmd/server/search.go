package main

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"flow/api/internal/domain"
)

func (s *server) searchWorkspace(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 30
	}
	types := searchTypes(r.URL.Query().Get("types"))
	results := buildSearchResultsLimited(data, query, types, limit)
	userID := authUser(r).ID
	if s.authDisabled {
		userID = data.Viewer.ID
	}
	if query != "" {
		_ = s.store.RecordSearch(r.Context(), data.Workspace.ID, userID, query)
	}
	history, _ := s.store.SearchHistory(r.Context(), data.Workspace.ID, userID, 8)
	recent, _ := s.store.RecentResources(r.Context(), data.Workspace.ID, userID, 12)
	if query == "" {
		results = resolveRecentResults(data, recent, types)
	}
	if len(results) > limit {
		results = results[:limit]
	}
	writeJSON(w, http.StatusOK, domain.SearchResponse{Results: results, History: history, Recent: recent})
}

func (s *server) clearSearchHistory(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	userID := authUser(r).ID
	if s.authDisabled {
		userID = data.Viewer.ID
	}
	respondMutation(w, s.store.ClearSearchHistory(r.Context(), data.Workspace.ID, userID), http.StatusNoContent, nil)
}

func (s *server) recordRecentResource(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Type, input.ID = strings.TrimSpace(input.Type), strings.TrimSpace(input.ID)
	data := s.workspaceData(r)
	if !searchResourceVisible(data, input.Type, input.ID) {
		writeError(w, http.StatusForbidden, "Resource is outside your workspace")
		return
	}
	userID := authUser(r).ID
	if s.authDisabled {
		userID = data.Viewer.ID
	}
	respondMutation(w, s.store.RecordRecent(r.Context(), data.Workspace.ID, userID, input.Type, input.ID), http.StatusNoContent, nil)
}

func buildSearchResults(data domain.Bootstrap, query string, types map[string]bool) []domain.SearchResult {
	return buildSearchResultsLimited(data, query, types, 0)
}

func buildSearchResultsLimited(data domain.Bootstrap, query string, types map[string]bool, limit int) []domain.SearchResult {
	results := []domain.SearchResult{}
	sortAndTrim := func() {
		slices.SortStableFunc(results, func(left, right domain.SearchResult) int {
			if left.Score != right.Score {
				return right.Score - left.Score
			}
			return right.UpdatedAt.Compare(left.UpdatedAt)
		})
		if limit > 0 && len(results) > limit {
			results = results[:limit]
		}
	}
	add := func(item domain.SearchResult, fields ...string) {
		item.Score = fuzzyScore(query, fields...)
		if item.Score > 0 {
			results = append(results, item)
			if limit > 0 && len(results) >= limit*2 {
				sortAndTrim()
			}
		}
	}
	if types["issue"] {
		for _, issue := range data.Issues {
			labels := make([]string, 0, len(issue.Labels))
			for _, label := range issue.Labels {
				labels = append(labels, label.Name)
			}
			add(domain.SearchResult{ID: issue.ID, Type: "issue", Title: issue.Title, Subtitle: issue.Team.Name, Identifier: issue.Identifier, Color: issue.State.Color, UpdatedAt: issue.UpdatedAt}, issue.Identifier, issue.Title, issue.Description, strings.Join(labels, " "))
		}
	}
	if types["project"] {
		for _, project := range data.Projects {
			add(domain.SearchResult{ID: project.ID, Type: "project", Title: project.Name, Subtitle: project.Summary, Icon: project.Icon, Color: project.Color, UpdatedAt: project.UpdatedAt}, project.Name, project.Summary, project.Description)
		}
	}
	if types["initiative"] {
		for _, initiative := range data.Initiatives {
			add(domain.SearchResult{ID: initiative.ID, Type: "initiative", Title: initiative.Name, Subtitle: initiative.Summary, Icon: initiative.Icon, Color: initiative.Color, UpdatedAt: initiative.UpdatedAt}, initiative.Name, initiative.Summary, initiative.Description)
		}
	}
	if types["document"] {
		indexed := map[string]bool{}
		for _, document := range data.Documents {
			if document.ArchivedAt != nil {
				continue
			}
			subtitle := "Document"
			if len(document.ProjectIDs) > 0 {
				if index := slices.IndexFunc(data.Projects, func(project domain.Project) bool { return project.ID == document.ProjectIDs[0] }); index >= 0 {
					subtitle = data.Projects[index].Name
				}
			}
			add(domain.SearchResult{ID: document.ID, Type: "document", Title: document.Title, Subtitle: subtitle, Icon: document.Icon, UpdatedAt: document.UpdatedAt}, document.Title, document.Content, subtitle)
			indexed[document.ID] = true
		}
		for _, project := range data.Projects {
			for _, resource := range project.Resources {
				if resource.Type == "document" && !indexed[resource.ID] {
					add(domain.SearchResult{ID: resource.ID, Type: "document", Title: resource.Title, Subtitle: project.Name, ParentID: project.ID, ParentType: "project", UpdatedAt: resource.CreatedAt}, resource.Title, resource.URL, project.Name)
					indexed[resource.ID] = true
				}
			}
		}
		for _, initiative := range data.Initiatives {
			for _, resource := range initiative.Resources {
				if resource.Type == "document" && !indexed[resource.ID] {
					add(domain.SearchResult{ID: resource.ID, Type: "document", Title: resource.Title, Subtitle: initiative.Name, ParentID: initiative.ID, ParentType: "initiative", UpdatedAt: resource.CreatedAt}, resource.Title, resource.URL, initiative.Name)
					indexed[resource.ID] = true
				}
			}
		}
	}
	if types["member"] {
		for _, user := range data.Users {
			add(domain.SearchResult{ID: user.ID, Type: "member", Title: user.DisplayName, Subtitle: user.Email, Email: user.Email}, user.DisplayName, user.Name, user.Email)
		}
	}
	if types["customer"] {
		for _, customer := range data.Customers {
			owner := ""
			if index := slices.IndexFunc(data.Users, func(user domain.User) bool { return user.ID == customer.OwnerID }); index >= 0 {
				owner = data.Users[index].DisplayName
			}
			subtitle := strings.Join(customer.Domains, ", ")
			add(domain.SearchResult{ID: customer.ID, Type: "customer", Title: customer.Name, Subtitle: subtitle, UpdatedAt: customer.UpdatedAt}, customer.Name, subtitle, customer.Tier, customer.Status, owner)
		}
	}
	if types["release"] {
		for _, release := range data.Releases {
			if release.ArchivedAt != nil {
				continue
			}
			subtitle := strings.TrimSpace(strings.Join([]string{release.Version, release.Status}, " "))
			add(domain.SearchResult{ID: release.ID, Type: "release", Title: release.Name, Subtitle: subtitle, UpdatedAt: release.UpdatedAt}, release.Name, release.Version, release.Description, release.Status)
		}
	}
	if types["view"] {
		for _, view := range data.SavedViews {
			subtitle := strings.TrimSpace(strings.Join([]string{view.Scope, view.Resource}, " "))
			add(domain.SearchResult{ID: view.ID, Type: "view", Title: view.Name, Subtitle: subtitle, Icon: view.Icon, Color: view.Color, UpdatedAt: view.UpdatedAt}, view.Name, view.Description, view.Scope, view.Resource)
		}
	}
	sortAndTrim()
	return results
}

func searchTypes(raw string) map[string]bool {
	result := map[string]bool{"issue": true, "project": true, "initiative": true, "document": true, "member": true, "customer": true, "release": true, "view": true}
	if strings.TrimSpace(raw) == "" {
		return result
	}
	clear(result)
	for _, value := range strings.Split(raw, ",") {
		value = strings.TrimSpace(strings.ToLower(value))
		if value == "issue" || value == "project" || value == "initiative" || value == "document" || value == "member" || value == "customer" || value == "release" || value == "view" {
			result[value] = true
		}
	}
	return result
}

func fuzzyScore(query string, fields ...string) int {
	query = normalizeSearch(query)
	if query == "" {
		return 1
	}
	best := 0
	for _, field := range fields {
		field = normalizeSearch(field)
		if field == "" {
			continue
		}
		score := 0
		switch {
		case field == query:
			score = 1200
		case strings.HasPrefix(field, query):
			score = 900 - min(200, len([]rune(field))-len([]rune(query)))
		case strings.Contains(field, query):
			score = 650 - min(200, strings.Index(field, query))
		case allTokensPresent(field, strings.Fields(query)):
			score = 500
		case isSubsequence([]rune(query), []rune(field)):
			score = 250
		}
		best = max(best, score)
	}
	return best
}

func normalizeSearch(value string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return unicode.ToLower(r)
		}
		return ' '
	}, value))
}

func allTokensPresent(value string, tokens []string) bool {
	if len(tokens) == 0 {
		return false
	}
	return !slices.ContainsFunc(tokens, func(token string) bool { return !strings.Contains(value, token) })
}

func isSubsequence(needle, value []rune) bool {
	if len(needle) < 2 {
		return false
	}
	index := 0
	for _, current := range value {
		if current == needle[index] {
			index++
			if index == len(needle) {
				return true
			}
		}
	}
	return false
}

func resolveRecentResults(data domain.Bootstrap, recent []domain.RecentResource, types map[string]bool) []domain.SearchResult {
	all := buildSearchResults(data, "", types)
	result := make([]domain.SearchResult, 0, len(recent))
	for _, item := range recent {
		if match := slices.IndexFunc(all, func(candidate domain.SearchResult) bool {
			return candidate.Type == item.ResourceType && candidate.ID == item.ResourceID
		}); match >= 0 {
			result = append(result, all[match])
		}
	}
	return result
}

func searchResourceVisible(data domain.Bootstrap, resourceType, id string) bool {
	switch resourceType {
	case "issue":
		return slices.ContainsFunc(data.Issues, func(item domain.Issue) bool { return item.ID == id })
	case "project":
		return slices.ContainsFunc(data.Projects, func(item domain.Project) bool { return item.ID == id })
	case "initiative":
		return slices.ContainsFunc(data.Initiatives, func(item domain.Initiative) bool { return item.ID == id })
	case "member":
		return slices.ContainsFunc(data.Users, func(item domain.User) bool { return item.ID == id })
	case "document":
		return slices.ContainsFunc(buildSearchResults(data, "", map[string]bool{"document": true}), func(item domain.SearchResult) bool { return item.ID == id })
	case "customer":
		return slices.ContainsFunc(data.Customers, func(item domain.Customer) bool { return item.ID == id })
	case "release":
		return slices.ContainsFunc(data.Releases, func(item domain.Release) bool { return item.ID == id && item.ArchivedAt == nil })
	case "view":
		return slices.ContainsFunc(data.SavedViews, func(item domain.SavedView) bool { return item.ID == id })
	default:
		return false
	}
}
