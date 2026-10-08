package main

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func projectListBootstrapRequested(r *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Flow-Projection")), "project-list") || r.URL.Query().Get("projection") == "project-list"
}

func (s *server) listProjectRecords(w http.ResponseWriter, r *http.Request) {
	metadata, access, err := s.requestIssueQueryAccess(r)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	filters := []store.ProjectDirectoryFilter{}
	if raw := r.URL.Query().Get("filter"); raw != "" && json.Unmarshal([]byte(raw), &filters) != nil {
		writeError(w, http.StatusBadRequest, "invalid project filter")
		return
	}
	allowedFilters := map[string]bool{"status": true, "priority": true, "lead": true, "members": true, "health": true, "dates": true, "milestones": true, "labels": true, "teams": true, "project": true, "customers": true}
	customerFilters := false
	for _, filter := range filters {
		// Linear's Customers blocks: names/owners/statuses/tiers use is / is not, the number blocks compare.
		numeric := filter.Field == "customers" && slices.Contains([]string{"gte", "lte", "eq", "neq"}, filter.Operator)
		if !allowedFilters[filter.Field] || len(filter.Values) == 0 || len(filter.Values) > 100 || filter.Operator != "is" && filter.Operator != "isNot" && !numeric {
			writeError(w, http.StatusBadRequest, "invalid project filter")
			return
		}
		if filter.Field == "customers" {
			if _, err := store.MatchProjectCustomerFilter(nil, filter.Operator, filter.Values); err != nil {
				writeError(w, http.StatusBadRequest, "invalid project filter")
				return
			}
			customerFilters = true
		}
	}
	query := store.ProjectRecordQuery{
		Workspace:    metadata.Workspace.URLKey,
		TeamIDs:      splitQueryValues(r.URL.Query().Get("teamId")),
		Search:       r.URL.Query().Get("q"),
		Archived:     r.URL.Query().Get("archived"),
		Cursor:       r.URL.Query().Get("cursor"),
		Limit:        limit,
		IncludeTotal: r.URL.Query().Get("includeTotal") == "true",
		Admin:        s.authDisabled || access.Admin,
		Filters:      filters,
	}
	if !query.Admin {
		query.AllowedTeamIDs = access.VisibleTeamIDs
		if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok && apiKeyTeamRestrictionSelected(key) {
			query.AllowedTeamIDs = slices.DeleteFunc(query.AllowedTeamIDs, func(id string) bool { return !slices.Contains(key.TeamIDs, id) })
		}
	}
	// An absent flag counts as enabled, like the web client.
	customersEnabled, flagged := metadata.WorkspaceSettings.FeatureFlags["customer-requests"]
	if (customersEnabled || !flagged) && metadata.ViewerRole != "guest" {
		summaries, err := s.store.ProjectCustomerSummaries(r.Context(), metadata.Workspace.URLKey)
		if err != nil {
			issueRecordsError(w, err)
			return
		}
		query.CustomerSummaries = summaries
	} else if customerFilters {
		writeError(w, http.StatusBadRequest, "invalid project filter")
		return
	}
	page, err := s.store.QueryProjectDirectory(r.Context(), query)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	if query.CustomerSummaries != nil {
		writeJSON(w, http.StatusOK, s.projectPageWithCustomers(r, metadata.Workspace.URLKey, page, query.CustomerSummaries))
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// projectCustomerPage adds Linear's per-project customer data (the Customers /
// Customer revenue properties and orderings) for the page's projects only.
type projectCustomerPage struct {
	store.ProjectRecordPage
	CustomerSummaries map[string]*store.ProjectCustomerSummary `json:"customerSummaries"`
	Customers         []domain.Customer                        `json:"customers"`
}

func (s *server) projectPageWithCustomers(r *http.Request, workspace string, page store.ProjectRecordPage, summaries map[string]*store.ProjectCustomerSummary) projectCustomerPage {
	result := projectCustomerPage{ProjectRecordPage: page, CustomerSummaries: map[string]*store.ProjectCustomerSummary{}, Customers: []domain.Customer{}}
	ids := []string{}
	for _, project := range page.Items {
		summary := summaries[project.ID]
		if summary == nil {
			continue
		}
		result.CustomerSummaries[project.ID] = summary
		for _, customer := range summary.Customers {
			if !slices.Contains(ids, customer.ID) {
				ids = append(ids, customer.ID)
			}
		}
	}
	if customers, err := s.store.CustomersByID(r.Context(), workspace, ids); err == nil {
		result.Customers = customers
	}
	return result
}
