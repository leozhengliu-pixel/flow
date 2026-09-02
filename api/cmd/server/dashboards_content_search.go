package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"flow/api/internal/domain"
)

const (
	dashboardsSettingsKey = "dashboards.v1"
	postsSettingsKey      = "posts.v1"
	feedSettingsKey       = "feedItems.v1"
	meetingsSettingsKey   = "meetings.v1"
)

type dashboardInput struct {
	Name        *string                   `json:"name,omitempty"`
	Description *string                   `json:"description,omitempty"`
	OwnerID     *string                   `json:"ownerId,omitempty"`
	Visibility  *string                   `json:"visibility,omitempty"`
	TeamIDs     *[]string                 `json:"teamIds,omitempty"`
	Filters     *map[string][]string      `json:"filters,omitempty"`
	HideFilters *bool                     `json:"hideFilters,omitempty"`
	Widgets     *[]domain.DashboardWidget `json:"widgets,omitempty"`
}

type postInput struct {
	Title         *string   `json:"title,omitempty"`
	Body          *string   `json:"body,omitempty"`
	TeamIDs       *[]string `json:"teamIds,omitempty"`
	ProjectID     *string   `json:"projectId,omitempty"`
	InitiativeID  *string   `json:"initiativeId,omitempty"`
	SubscriberIDs *[]string `json:"subscriberIds,omitempty"`
	Archived      *bool     `json:"archived,omitempty"`
}

type meetingInput struct {
	Title         *string    `json:"title,omitempty"`
	Description   *string    `json:"description,omitempty"`
	AttendeeIDs   *[]string  `json:"attendeeIds,omitempty"`
	TeamIDs       *[]string  `json:"teamIds,omitempty"`
	ProjectIDs    *[]string  `json:"projectIds,omitempty"`
	IssueIDs      *[]string  `json:"issueIds,omitempty"`
	StartsAt      *time.Time `json:"startsAt,omitempty"`
	DurationMins  *int       `json:"durationMinutes,omitempty"`
	URL           *string    `json:"url,omitempty"`
	Notes         *string    `json:"notes,omitempty"`
	Transcript    *string    `json:"transcript,omitempty"`
	SubscriberIDs *[]string  `json:"subscriberIds,omitempty"`
}

func settingCollection[T any](data domain.Bootstrap, key string) []T {
	if data.Settings == nil || data.Settings[key] == nil {
		return []T{}
	}
	raw, err := json.Marshal(data.Settings[key])
	if err != nil {
		return []T{}
	}
	result := []T{}
	if json.Unmarshal(raw, &result) != nil {
		return []T{}
	}
	return result
}

func saveSettingCollection[T any](data *domain.Bootstrap, key string, values []T) {
	if data.Settings == nil {
		data.Settings = map[string]any{}
	}
	data.Settings[key] = values
}

func opaqueID(prefix string) string {
	var value [12]byte
	if _, err := rand.Read(value[:]); err == nil {
		return prefix + base64.RawURLEncoding.EncodeToString(value[:])
	}
	return fmt.Sprintf("%s%d", prefix, time.Now().UnixNano())
}

func contentPageBounds(r *http.Request, total int) (int, int) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 30
	}
	offset := 0
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		if raw, err := base64.RawURLEncoding.DecodeString(cursor); err == nil {
			offset, _ = strconv.Atoi(string(raw))
		}
	}
	if offset < 0 || offset > total {
		offset = 0
	}
	return offset, min(total, offset+limit)
}

func writeContentPage[T any](w http.ResponseWriter, r *http.Request, values []T) {
	start, end := contentPageBounds(r, len(values))
	next := ""
	if end < len(values) {
		next = base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(end)))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": values[start:end], "nextCursor": next, "hasMore": end < len(values), "total": len(values)})
}

func viewerID(s *server, data domain.Bootstrap, r *http.Request) string {
	if s.authDisabled {
		return data.Viewer.ID
	}
	return authUser(r).ID
}

func allowContentMutation(w http.ResponseWriter, data domain.Bootstrap) bool {
	if data.ViewerRole == "guest" {
		writeError(w, http.StatusForbidden, "Guests cannot modify workspace content")
		return false
	}
	return true
}

func teamSet(data domain.Bootstrap) map[string]bool {
	result := make(map[string]bool, len(data.Teams))
	for _, team := range data.Teams {
		result[team.ID] = true
	}
	return result
}

func scopedTeamsVisible(data domain.Bootstrap, ids []string) bool {
	if workspaceAdminRole(data.ViewerRole) || len(ids) == 0 {
		return true
	}
	allowed := teamSet(data)
	return slices.ContainsFunc(ids, func(id string) bool { return allowed[id] })
}

func dashboardVisible(data domain.Bootstrap, viewer string, item domain.Dashboard) bool {
	switch item.Visibility {
	case "private":
		return item.OwnerID == viewer || workspaceAdminRole(data.ViewerRole)
	case "team":
		return scopedTeamsVisible(data, item.TeamIDs)
	default:
		return data.ViewerRole != "guest"
	}
}

func validateDashboard(data domain.Bootstrap, value *domain.Dashboard) error {
	value.Name = strings.TrimSpace(value.Name)
	if value.Name == "" {
		return fmt.Errorf("%w: dashboard name is required", errInvalid)
	}
	if !slices.Contains([]string{"workspace", "team", "private"}, value.Visibility) {
		return fmt.Errorf("%w: invalid dashboard visibility", errInvalid)
	}
	if value.OwnerID == "" || userByID(&data, value.OwnerID) == nil {
		return fmt.Errorf("%w: dashboard owner is required", errInvalid)
	}
	value.TeamIDs = normalizedStrings(value.TeamIDs)
	if value.Visibility == "team" && len(value.TeamIDs) == 0 {
		return fmt.Errorf("%w: team dashboard requires a team", errInvalid)
	}
	if !validateResourceIDs(&data, "team", value.TeamIDs) {
		return fmt.Errorf("%w: unknown team", errInvalid)
	}
	value.Filters = normalizedDashboardFilters(value.Filters)
	if !validateDashboardFilters(data, value.Filters) {
		return fmt.Errorf("%w: dashboard filter references an unknown resource", errInvalid)
	}
	allowedWidgets := []string{"insight", "issue_count", "status_breakdown", "assignee_workload", "cycle_progress", "project_progress", "sla_health", "throughput"}
	seen := map[string]bool{}
	for index := range value.Widgets {
		widget := &value.Widgets[index]
		if !slices.Contains(allowedWidgets, widget.Type) {
			return fmt.Errorf("%w: unsupported widget type %q", errInvalid, widget.Type)
		}
		if len(widget.Config) == 0 || string(widget.Config) == "null" {
			widget.Config = json.RawMessage(`{}`)
		}
		if !json.Valid(widget.Config) || widget.Config[0] != '{' {
			return fmt.Errorf("%w: widget config must be an object", errInvalid)
		}
		if widget.Type == "insight" {
			config := dashboardInsightConfig{}
			if err := json.Unmarshal(widget.Config, &config); err != nil || !config.valid() {
				return fmt.Errorf("%w: invalid insight configuration", errInvalid)
			}
			if !validateDashboardFilters(data, map[string][]string{"teamIds": config.TeamIDs, "stateIds": config.StateIDs, "assigneeIds": config.AssigneeIDs, "labelIds": config.LabelIDs}) {
				return fmt.Errorf("%w: insight filter references an unknown resource", errInvalid)
			}
		}
		if widget.ID == "" {
			widget.ID = opaqueID("widget_")
		}
		if seen[widget.ID] {
			return fmt.Errorf("%w: duplicate widget id", errInvalid)
		}
		seen[widget.ID] = true
		widget.Title = strings.TrimSpace(widget.Title)
		if widget.Title == "" {
			widget.Title = strings.ReplaceAll(strings.Title(strings.ReplaceAll(widget.Type, "_", " ")), "  ", " ") //nolint:staticcheck
		}
		widget.Position = index
		if widget.Width != 1 && widget.Width != 2 {
			widget.Width = 1
		}
	}
	return nil
}

type dashboardInsightConfig struct {
	Display         string   `json:"display"`
	Measure         string   `json:"measure"`
	Aggregation     string   `json:"aggregation"`
	Slice           string   `json:"slice"`
	Segment         string   `json:"segment"`
	DateAggregation string   `json:"dateAggregation"`
	TeamIDs         []string `json:"teamIds"`
	StateIDs        []string `json:"stateIds"`
	AssigneeIDs     []string `json:"assigneeIds"`
	LabelIDs        []string `json:"labelIds"`
	SinceDays       int      `json:"sinceDays"`
}

func (c *dashboardInsightConfig) defaults() {
	if c.Display == "" {
		c.Display = "chart"
	}
	if c.Measure == "" {
		c.Measure = "issue_count"
	}
	if c.Aggregation == "" {
		c.Aggregation = "count"
	}
	if c.Slice == "" {
		c.Slice = "status"
	}
	if c.Segment == "" {
		c.Segment = "none"
	}
	if c.DateAggregation == "" {
		c.DateAggregation = "month"
	}
}

func (c dashboardInsightConfig) valid() bool {
	c.defaults()
	return slices.Contains([]string{"chart", "table", "metric"}, c.Display) &&
		slices.Contains([]string{"issue_count", "estimate", "cycle_time", "lead_time", "sla_breaches"}, c.Measure) &&
		slices.Contains([]string{"count", "sum", "average", "minimum", "maximum"}, c.Aggregation) &&
		slices.Contains([]string{"none", "status", "team", "assignee", "label", "project", "cycle", "priority", "created_at", "completed_at"}, c.Slice) &&
		slices.Contains([]string{"none", "status", "team", "assignee", "project", "priority"}, c.Segment) &&
		slices.Contains([]string{"day", "week", "month", "quarter", "year"}, c.DateAggregation)
}

func normalizedDashboardFilters(filters map[string][]string) map[string][]string {
	result := map[string][]string{}
	for _, key := range []string{"teamIds", "stateIds", "assigneeIds", "labelIds"} {
		if values := normalizedStrings(filters[key]); len(values) > 0 {
			result[key] = values
		}
	}
	return result
}

func validateDashboardFilters(data domain.Bootstrap, filters map[string][]string) bool {
	for _, id := range filters["teamIds"] {
		if !slices.ContainsFunc(data.Teams, func(item domain.Team) bool { return item.ID == id }) {
			return false
		}
	}
	for _, id := range filters["stateIds"] {
		if !slices.ContainsFunc(data.States, func(item domain.WorkflowState) bool { return item.ID == id }) {
			return false
		}
	}
	for _, id := range filters["assigneeIds"] {
		if userByID(&data, id) == nil {
			return false
		}
	}
	for _, id := range filters["labelIds"] {
		if !labelExistsForResource(&data, id, "issue") {
			return false
		}
	}
	return true
}

func (s *server) listDashboards(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	viewer := viewerID(s, data, r)
	items := settingCollection[domain.Dashboard](data, dashboardsSettingsKey)
	items = slices.DeleteFunc(items, func(item domain.Dashboard) bool { return !dashboardVisible(data, viewer, item) })
	slices.SortStableFunc(items, func(a, b domain.Dashboard) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	writeContentPage(w, r, items)
}

func (s *server) createDashboard(w http.ResponseWriter, r *http.Request) {
	var input dashboardInput
	if !decodeJSON(w, r, &input) || input.Name == nil {
		return
	}
	data := s.workspaceData(r)
	if !allowContentMutation(w, data) {
		return
	}
	now := time.Now().UTC()
	created := domain.Dashboard{ID: opaqueID("dashboard_"), Name: *input.Name, OwnerID: viewerID(s, data, r), Visibility: "private", TeamIDs: []string{}, Filters: map[string][]string{}, Widgets: []domain.DashboardWidget{}, SubscriberIDs: []string{}, CreatedAt: now, UpdatedAt: now}
	applyDashboardInput(&created, input)
	if err := validateDashboard(data, &created); err != nil {
		respondMutation(w, err, http.StatusCreated, nil)
		return
	}
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "dashboard.created", created.ID, input, func(next *domain.Bootstrap) error {
		items := settingCollection[domain.Dashboard](*next, dashboardsSettingsKey)
		items = append(items, created)
		saveSettingCollection(next, dashboardsSettingsKey, items)
		appendContentFeed(next, domain.FeedItem{ID: opaqueID("feed_"), Type: "dashboard.created", ActorID: created.OwnerID, ResourceType: "dashboard", ResourceID: created.ID, TeamIDs: created.TeamIDs, Title: created.Name, CreatedAt: now})
		return nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

func applyDashboardInput(value *domain.Dashboard, input dashboardInput) {
	if input.Name != nil {
		value.Name = *input.Name
	}
	if input.Description != nil {
		value.Description = strings.TrimSpace(*input.Description)
	}
	if input.OwnerID != nil {
		value.OwnerID = strings.TrimSpace(*input.OwnerID)
	}
	if input.Visibility != nil {
		value.Visibility = strings.ToLower(strings.TrimSpace(*input.Visibility))
	}
	if input.TeamIDs != nil {
		value.TeamIDs = normalizedStrings(*input.TeamIDs)
	}
	if input.Filters != nil {
		value.Filters = normalizedDashboardFilters(*input.Filters)
	}
	if input.HideFilters != nil {
		value.HideFilters = *input.HideFilters
	}
	if input.Widgets != nil {
		value.Widgets = slices.Clone(*input.Widgets)
	}
}

func (s *server) updateDashboard(w http.ResponseWriter, r *http.Request) {
	var input dashboardInput
	if !decodeJSON(w, r, &input) {
		return
	}
	data := s.workspaceData(r)
	if !allowContentMutation(w, data) {
		return
	}
	viewer := viewerID(s, data, r)
	var updated domain.Dashboard
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "dashboard.updated", r.PathValue("id"), input, func(next *domain.Bootstrap) error {
		items := settingCollection[domain.Dashboard](*next, dashboardsSettingsKey)
		index := slices.IndexFunc(items, func(item domain.Dashboard) bool { return item.ID == r.PathValue("id") })
		if index < 0 {
			return errNotFound
		}
		if items[index].OwnerID != viewer && !workspaceAdminRole(next.ViewerRole) {
			return errNotFound
		}
		applyDashboardInput(&items[index], input)
		items[index].UpdatedAt = time.Now().UTC()
		if err := validateDashboard(*next, &items[index]); err != nil {
			return err
		}
		updated = items[index]
		saveSettingCollection(next, dashboardsSettingsKey, items)
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteDashboard(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	if !allowContentMutation(w, data) {
		return
	}
	viewer := viewerID(s, data, r)
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "dashboard.deleted", r.PathValue("id"), nil, func(next *domain.Bootstrap) error {
		items := settingCollection[domain.Dashboard](*next, dashboardsSettingsKey)
		index := slices.IndexFunc(items, func(item domain.Dashboard) bool { return item.ID == r.PathValue("id") })
		if index < 0 {
			return errNotFound
		}
		if items[index].OwnerID != viewer && !workspaceAdminRole(next.ViewerRole) {
			return errNotFound
		}
		removedID := items[index].ID
		items = append(items[:index], items[index+1:]...)
		removeResourcePreferences(next, "dashboard", removedID)
		saveSettingCollection(next, dashboardsSettingsKey, items)
		return nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
}

func (s *server) subscribeDashboard(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	if !allowContentMutation(w, data) {
		return
	}
	viewer := viewerID(s, data, r)
	var updated domain.Dashboard
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "dashboard.subscription.updated", r.PathValue("id"), nil, func(next *domain.Bootstrap) error {
		items := settingCollection[domain.Dashboard](*next, dashboardsSettingsKey)
		index := slices.IndexFunc(items, func(item domain.Dashboard) bool { return item.ID == r.PathValue("id") })
		if index < 0 || !dashboardVisible(*next, viewer, items[index]) {
			return errNotFound
		}
		if r.Method == http.MethodPut && !slices.Contains(items[index].SubscriberIDs, viewer) {
			items[index].SubscriberIDs = append(items[index].SubscriberIDs, viewer)
		}
		if r.Method == http.MethodDelete {
			items[index].SubscriberIDs = slices.DeleteFunc(items[index].SubscriberIDs, func(id string) bool { return id == viewer })
		}
		items[index].UpdatedAt = time.Now().UTC()
		updated = items[index]
		saveSettingCollection(next, dashboardsSettingsKey, items)
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) shareDashboard(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	if !allowContentMutation(w, data) {
		return
	}
	viewer := viewerID(s, data, r)
	var updated domain.Dashboard
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "dashboard.share.updated", r.PathValue("id"), nil, func(next *domain.Bootstrap) error {
		items := settingCollection[domain.Dashboard](*next, dashboardsSettingsKey)
		index := slices.IndexFunc(items, func(item domain.Dashboard) bool { return item.ID == r.PathValue("id") })
		if index < 0 {
			return errNotFound
		}
		if items[index].OwnerID != viewer && !workspaceAdminRole(next.ViewerRole) {
			return errNotFound
		}
		if r.Method == http.MethodPost {
			items[index].ShareToken = opaqueID("dsh_")
			now := time.Now().UTC()
			items[index].SharedAt = &now
		} else {
			items[index].ShareToken = ""
			items[index].SharedAt = nil
		}
		items[index].UpdatedAt = time.Now().UTC()
		updated = items[index]
		saveSettingCollection(next, dashboardsSettingsKey, items)
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) getSharedDashboard(w http.ResponseWriter, r *http.Request) {
	data, ok := s.store.BootstrapFor(workspaceKey(r))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	items := settingCollection[domain.Dashboard](data, dashboardsSettingsKey)
	index := slices.IndexFunc(items, func(item domain.Dashboard) bool {
		return item.ShareToken != "" && item.ShareToken == r.PathValue("token")
	})
	if index < 0 {
		writeError(w, http.StatusNotFound, "dashboard not found")
		return
	}
	// Shared links are unauthenticated. Evaluate them against a public
	// projection so private-team issues and metadata never escape the
	// workspace boundary.
	publicTeams := map[string]bool{}
	for _, team := range data.Teams {
		if !team.Private {
			publicTeams[team.ID] = true
		}
	}
	data.ViewerRole = "guest"
	data.Teams = slices.DeleteFunc(data.Teams, func(team domain.Team) bool { return !publicTeams[team.ID] })
	data.Issues = slices.DeleteFunc(data.Issues, func(issue domain.Issue) bool { return !publicTeams[issue.Team.ID] })
	data.Projects = slices.DeleteFunc(data.Projects, func(project domain.Project) bool {
		if len(project.TeamIDs) == 0 {
			return false
		}
		return !slices.ContainsFunc(project.TeamIDs, func(id string) bool { return publicTeams[id] })
	})
	for index := range data.Projects {
		data.Projects[index].TeamIDs = slices.DeleteFunc(data.Projects[index].TeamIDs, func(id string) bool { return !publicTeams[id] })
	}
	shared := items[index]
	shared.TeamIDs = slices.DeleteFunc(shared.TeamIDs, func(id string) bool { return !publicTeams[id] })
	writeJSON(w, http.StatusOK, map[string]any{"dashboard": shared, "results": calculateDashboard(data, shared)})
}

func (s *server) dashboardResults(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	if !allowContentMutation(w, data) {
		return
	}
	viewer := viewerID(s, data, r)
	items := settingCollection[domain.Dashboard](data, dashboardsSettingsKey)
	index := slices.IndexFunc(items, func(item domain.Dashboard) bool {
		return item.ID == r.PathValue("id") && dashboardVisible(data, viewer, item)
	})
	if index < 0 {
		writeError(w, http.StatusNotFound, "dashboard not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"dashboard": items[index], "results": calculateDashboard(data, items[index])})
}

func (s *server) previewDashboardWidget(w http.ResponseWriter, r *http.Request) {
	var widget domain.DashboardWidget
	if !decodeJSON(w, r, &widget) {
		return
	}
	data := s.workspaceData(r)
	viewer := viewerID(s, data, r)
	items := settingCollection[domain.Dashboard](data, dashboardsSettingsKey)
	index := slices.IndexFunc(items, func(item domain.Dashboard) bool {
		return item.ID == r.PathValue("id") && dashboardVisible(data, viewer, item)
	})
	if index < 0 {
		writeError(w, http.StatusNotFound, "dashboard not found")
		return
	}
	dashboard := items[index]
	dashboard.Widgets = []domain.DashboardWidget{widget}
	if err := validateDashboard(data, &dashboard); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	results := calculateDashboard(data, dashboard)
	writeJSON(w, http.StatusOK, results[0])
}

func widgetIssueFilter(data domain.Bootstrap, widget domain.DashboardWidget) []*domain.Issue {
	config := dashboardInsightConfig{}
	_ = json.Unmarshal(widget.Config, &config)
	cutoff := time.Time{}
	if config.SinceDays > 0 {
		cutoff = time.Now().UTC().AddDate(0, 0, -config.SinceDays)
	}
	teamSet, stateSet, assigneeSet, labelSet := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, id := range config.TeamIDs {
		teamSet[id] = true
	}
	for _, id := range config.StateIDs {
		stateSet[id] = true
	}
	for _, id := range config.AssigneeIDs {
		assigneeSet[id] = true
	}
	for _, id := range config.LabelIDs {
		labelSet[id] = true
	}
	result := make([]*domain.Issue, 0, min(len(data.Issues), 256))
	for index := range data.Issues {
		issue := &data.Issues[index]
		if !cutoff.IsZero() && issue.CreatedAt.Before(cutoff) {
			continue
		}
		if len(teamSet) > 0 && !teamSet[issue.Team.ID] {
			continue
		}
		if len(stateSet) > 0 && !stateSet[issue.State.ID] {
			continue
		}
		assignee := ""
		if issue.Assignee != nil {
			assignee = issue.Assignee.ID
		}
		if len(assigneeSet) > 0 && !assigneeSet[assignee] {
			continue
		}
		if len(labelSet) > 0 && !slices.ContainsFunc(issue.Labels, func(label domain.IssueLabel) bool { return labelSet[label.ID] }) {
			continue
		}
		result = append(result, issue)
	}
	return result
}

func dashboardIssueFilter(data domain.Bootstrap, dashboard domain.Dashboard, widget domain.DashboardWidget) []*domain.Issue {
	issues := widgetIssueFilter(data, widget)
	filters := normalizedDashboardFilters(dashboard.Filters)
	if len(filters) == 0 {
		return issues
	}
	teamSet, stateSet, assigneeSet, labelSet := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, id := range filters["teamIds"] {
		teamSet[id] = true
	}
	for _, id := range filters["stateIds"] {
		stateSet[id] = true
	}
	for _, id := range filters["assigneeIds"] {
		assigneeSet[id] = true
	}
	for _, id := range filters["labelIds"] {
		labelSet[id] = true
	}
	return slices.DeleteFunc(issues, func(issue *domain.Issue) bool {
		if len(teamSet) > 0 && !teamSet[issue.Team.ID] {
			return true
		}
		if len(stateSet) > 0 && !stateSet[issue.State.ID] {
			return true
		}
		assignee := ""
		if issue.Assignee != nil {
			assignee = issue.Assignee.ID
		}
		if len(assigneeSet) > 0 && !assigneeSet[assignee] {
			return true
		}
		return len(labelSet) > 0 && !slices.ContainsFunc(issue.Labels, func(label domain.IssueLabel) bool { return labelSet[label.ID] })
	})
}

func calculateDashboard(data domain.Bootstrap, dashboard domain.Dashboard) []domain.DashboardWidgetResult {
	results := make([]domain.DashboardWidgetResult, 0, len(dashboard.Widgets))
	for _, widget := range dashboard.Widgets {
		issues := dashboardIssueFilter(data, dashboard, widget)
		var value any
		switch widget.Type {
		case "insight":
			value = calculateGenericInsight(data, issues, widget)
		case "issue_count":
			value = map[string]any{"count": len(issues)}
		case "status_breakdown":
			counts := map[string]int{}
			for _, issue := range issues {
				counts[issue.State.Name]++
			}
			value = counts
		case "assignee_workload":
			counts := map[string]int{}
			for _, issue := range issues {
				name := "Unassigned"
				if issue.Assignee != nil {
					name = issue.Assignee.DisplayName
				}
				counts[name]++
			}
			value = counts
		case "cycle_progress":
			rows := []map[string]any{}
			for _, cycle := range data.Cycles {
				total, done := 0, 0
				for _, issue := range issues {
					if issue.CycleID != nil && *issue.CycleID == cycle.ID {
						total++
						if issue.CompletedAt != nil {
							done++
						}
					}
				}
				rows = append(rows, map[string]any{"id": cycle.ID, "name": cycle.Name, "completed": done, "total": total, "capacity": cycle.Capacity})
			}
			value = rows
		case "project_progress":
			rows := []map[string]any{}
			for _, project := range data.Projects {
				rows = append(rows, map[string]any{"id": project.ID, "name": project.Name, "progress": project.Progress, "issueCount": project.IssueCount})
			}
			value = rows
		case "sla_health":
			breached, atRisk := 0, 0
			now := time.Now().UTC()
			for _, issue := range issues {
				if issue.SLABreachesAt != nil {
					if issue.SLABreachesAt.Before(now) {
						breached++
					} else if issue.SLABreachesAt.Before(now.Add(24 * time.Hour)) {
						atRisk++
					}
				}
			}
			value = map[string]int{"breached": breached, "atRisk": atRisk, "healthy": max(0, len(issues)-breached-atRisk)}
		case "throughput":
			buckets := map[string]int{}
			for _, issue := range issues {
				if issue.CompletedAt != nil {
					buckets[issue.CompletedAt.UTC().Format("2006-01-02")]++
				}
			}
			value = buckets
		}
		results = append(results, domain.DashboardWidgetResult{Widget: widget, Value: value})
	}
	return results
}

func calculateGenericInsight(data domain.Bootstrap, issues []*domain.Issue, widget domain.DashboardWidget) any {
	config := dashboardInsightConfig{}
	_ = json.Unmarshal(widget.Config, &config)
	config.defaults()
	values := map[string][]float64{}
	for _, issue := range issues {
		measure, ok := dashboardMeasureValue(issue, config.Measure)
		if !ok {
			continue
		}
		slicesForIssue := dashboardDimensionValues(data, issue, config.Slice, config.DateAggregation)
		segmentsForIssue := dashboardDimensionValues(data, issue, config.Segment, config.DateAggregation)
		for _, sliceLabel := range slicesForIssue {
			for _, segmentLabel := range segmentsForIssue {
				key := sliceLabel
				if config.Segment != "none" {
					key += " · " + segmentLabel
				}
				values[key] = append(values[key], measure)
			}
		}
	}
	result := map[string]float64{}
	for key, samples := range values {
		result[key] = aggregateDashboardValues(samples, config.Aggregation, config.Measure)
	}
	if len(result) == 0 && config.Slice == "none" {
		result["Total"] = 0
	}
	return result
}

func dashboardMeasureValue(issue *domain.Issue, measure string) (float64, bool) {
	switch measure {
	case "estimate":
		if issue.Estimate == nil {
			return 0, false
		}
		return *issue.Estimate, true
	case "cycle_time":
		if issue.StartedAt == nil || issue.CompletedAt == nil {
			return 0, false
		}
		return issue.CompletedAt.Sub(*issue.StartedAt).Hours(), true
	case "lead_time":
		if issue.CompletedAt == nil {
			return 0, false
		}
		return issue.CompletedAt.Sub(issue.CreatedAt).Hours(), true
	case "sla_breaches":
		if issue.SLABreachesAt != nil && issue.SLABreachesAt.Before(time.Now().UTC()) {
			return 1, true
		}
		return 0, true
	default:
		return 1, true
	}
}

func dashboardDimensionValues(data domain.Bootstrap, issue *domain.Issue, dimension, dateAggregation string) []string {
	switch dimension {
	case "status":
		return []string{issue.State.Name}
	case "team":
		return []string{issue.Team.Name}
	case "assignee":
		if issue.Assignee == nil {
			return []string{"Unassigned"}
		}
		return []string{issue.Assignee.DisplayName}
	case "label":
		if len(issue.Labels) == 0 {
			return []string{"No label"}
		}
		labels := make([]string, 0, len(issue.Labels))
		for _, label := range issue.Labels {
			labels = append(labels, label.Name)
		}
		return labels
	case "project":
		if issue.Project == nil {
			return []string{"No project"}
		}
		return []string{issue.Project.Name}
	case "cycle":
		if issue.CycleID == nil {
			return []string{"No cycle"}
		}
		for _, cycle := range data.Cycles {
			if cycle.ID == *issue.CycleID {
				return []string{cycle.Name}
			}
		}
		return []string{"No cycle"}
	case "priority":
		return []string{issue.PriorityLabel}
	case "created_at":
		return []string{dashboardDateBucket(issue.CreatedAt, dateAggregation)}
	case "completed_at":
		if issue.CompletedAt == nil {
			return []string{"Not completed"}
		}
		return []string{dashboardDateBucket(*issue.CompletedAt, dateAggregation)}
	default:
		return []string{"Total"}
	}
}

func dashboardDateBucket(value time.Time, aggregation string) string {
	value = value.UTC()
	switch aggregation {
	case "day":
		return value.Format("2006-01-02")
	case "week":
		year, week := value.ISOWeek()
		return fmt.Sprintf("%d-W%02d", year, week)
	case "quarter":
		return fmt.Sprintf("%d Q%d", value.Year(), (int(value.Month())-1)/3+1)
	case "year":
		return value.Format("2006")
	default:
		return value.Format("2006-01")
	}
}

func aggregateDashboardValues(values []float64, aggregation, measure string) float64 {
	if len(values) == 0 {
		return 0
	}
	if aggregation == "count" || measure == "issue_count" {
		return float64(len(values))
	}
	total, minimum, maximum := 0.0, values[0], values[0]
	for _, value := range values {
		total += value
		minimum = min(minimum, value)
		maximum = max(maximum, value)
	}
	switch aggregation {
	case "average":
		return total / float64(len(values))
	case "minimum":
		return minimum
	case "maximum":
		return maximum
	default:
		return total
	}
}

func (s *server) exportDashboard(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	viewer := viewerID(s, data, r)
	items := settingCollection[domain.Dashboard](data, dashboardsSettingsKey)
	index := slices.IndexFunc(items, func(item domain.Dashboard) bool {
		return item.ID == r.PathValue("id") && dashboardVisible(data, viewer, item)
	})
	if index < 0 {
		writeError(w, http.StatusNotFound, "dashboard not found")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="dashboard-%s.csv"`, items[index].ID))
	writer := csv.NewWriter(w)
	_ = writer.Write([]string{"widget_id", "widget_type", "widget_title", "value_json"})
	for _, result := range calculateDashboard(data, items[index]) {
		raw, _ := json.Marshal(result.Value)
		_ = writer.Write([]string{result.Widget.ID, result.Widget.Type, result.Widget.Title, string(raw)})
	}
	writer.Flush()
}

func appendContentFeed(data *domain.Bootstrap, item domain.FeedItem) {
	items := settingCollection[domain.FeedItem](*data, feedSettingsKey)
	items = append([]domain.FeedItem{item}, items...)
	if len(items) > 5000 {
		items = items[:5000]
	}
	saveSettingCollection(data, feedSettingsKey, items)
}

func validatePost(data domain.Bootstrap, post *domain.Post) error {
	post.Body = strings.TrimSpace(post.Body)
	post.Title = strings.TrimSpace(post.Title)
	if post.Body == "" {
		return fmt.Errorf("%w: post body is required", errInvalid)
	}
	post.TeamIDs = normalizedStrings(post.TeamIDs)
	post.SubscriberIDs = normalizedStrings(post.SubscriberIDs)
	if !validateResourceIDs(&data, "team", post.TeamIDs) || !validateResourceIDs(&data, "user", post.SubscriberIDs) {
		return fmt.Errorf("%w: unknown team or subscriber", errInvalid)
	}
	if post.ProjectID != "" && !validateResourceIDs(&data, "project", []string{post.ProjectID}) {
		return fmt.Errorf("%w: unknown project", errInvalid)
	}
	if post.InitiativeID != "" && !slices.ContainsFunc(data.Initiatives, func(item domain.Initiative) bool { return item.ID == post.InitiativeID }) {
		return fmt.Errorf("%w: unknown initiative", errInvalid)
	}
	return nil
}

func applyPostInput(item *domain.Post, input postInput) {
	if input.Title != nil {
		item.Title = *input.Title
	}
	if input.Body != nil {
		item.Body = *input.Body
	}
	if input.TeamIDs != nil {
		item.TeamIDs = normalizedStrings(*input.TeamIDs)
	}
	if input.ProjectID != nil {
		item.ProjectID = strings.TrimSpace(*input.ProjectID)
	}
	if input.InitiativeID != nil {
		item.InitiativeID = strings.TrimSpace(*input.InitiativeID)
	}
	if input.SubscriberIDs != nil {
		item.SubscriberIDs = normalizedStrings(*input.SubscriberIDs)
	}
	if input.Archived != nil {
		if *input.Archived {
			now := time.Now().UTC()
			item.ArchivedAt = &now
		} else {
			item.ArchivedAt = nil
		}
	}
}

func (s *server) listPosts(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	items := settingCollection[domain.Post](data, postsSettingsKey)
	includeArchived := r.URL.Query().Get("archived") == "all"
	items = slices.DeleteFunc(items, func(item domain.Post) bool {
		return !scopedTeamsVisible(data, item.TeamIDs) || !includeArchived && item.ArchivedAt != nil
	})
	slices.SortStableFunc(items, func(a, b domain.Post) int { return b.CreatedAt.Compare(a.CreatedAt) })
	writeContentPage(w, r, items)
}

func (s *server) createPost(w http.ResponseWriter, r *http.Request) {
	var input postInput
	if !decodeJSON(w, r, &input) || input.Body == nil {
		return
	}
	data := s.workspaceData(r)
	if !allowContentMutation(w, data) {
		return
	}
	now := time.Now().UTC()
	item := domain.Post{ID: opaqueID("post_"), CreatorID: viewerID(s, data, r), TeamIDs: []string{}, SubscriberIDs: []string{}, CreatedAt: now, UpdatedAt: now}
	applyPostInput(&item, input)
	if err := validatePost(data, &item); err != nil {
		respondMutation(w, err, http.StatusCreated, nil)
		return
	}
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "post.created", item.ID, input, func(next *domain.Bootstrap) error {
		values := settingCollection[domain.Post](*next, postsSettingsKey)
		values = append(values, item)
		saveSettingCollection(next, postsSettingsKey, values)
		title := item.Title
		if title == "" {
			title = "New post"
		}
		appendContentFeed(next, domain.FeedItem{ID: opaqueID("feed_"), Type: "post.created", ActorID: item.CreatorID, ResourceType: "post", ResourceID: item.ID, TeamIDs: item.TeamIDs, Title: title, Body: item.Body, CreatedAt: now})
		return nil
	})
	respondMutation(w, err, http.StatusCreated, item)
}

func (s *server) updatePost(w http.ResponseWriter, r *http.Request) {
	var input postInput
	if !decodeJSON(w, r, &input) {
		return
	}
	data := s.workspaceData(r)
	viewer := viewerID(s, data, r)
	var updated domain.Post
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "post.updated", r.PathValue("id"), input, func(next *domain.Bootstrap) error {
		items := settingCollection[domain.Post](*next, postsSettingsKey)
		i := slices.IndexFunc(items, func(v domain.Post) bool { return v.ID == r.PathValue("id") })
		if i < 0 {
			return errNotFound
		}
		if items[i].CreatorID != viewer && !workspaceAdminRole(next.ViewerRole) {
			return errNotFound
		}
		applyPostInput(&items[i], input)
		items[i].UpdatedAt = time.Now().UTC()
		if err := validatePost(*next, &items[i]); err != nil {
			return err
		}
		updated = items[i]
		saveSettingCollection(next, postsSettingsKey, items)
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deletePost(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	if !allowContentMutation(w, data) {
		return
	}
	viewer := viewerID(s, data, r)
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "post.deleted", r.PathValue("id"), nil, func(next *domain.Bootstrap) error {
		items := settingCollection[domain.Post](*next, postsSettingsKey)
		i := slices.IndexFunc(items, func(v domain.Post) bool { return v.ID == r.PathValue("id") })
		if i < 0 {
			return errNotFound
		}
		if items[i].CreatorID != viewer && !workspaceAdminRole(next.ViewerRole) {
			return errNotFound
		}
		items = append(items[:i], items[i+1:]...)
		saveSettingCollection(next, postsSettingsKey, items)
		return nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
}

func validateMeeting(data domain.Bootstrap, item *domain.Meeting) error {
	item.Title = strings.TrimSpace(item.Title)
	if item.Title == "" {
		return fmt.Errorf("%w: meeting title is required", errInvalid)
	}
	if item.StartsAt.IsZero() {
		return fmt.Errorf("%w: meeting start time is required", errInvalid)
	}
	if item.DurationMins < 5 || item.DurationMins > 1440 {
		return fmt.Errorf("%w: duration must be between 5 and 1440 minutes", errInvalid)
	}
	item.TeamIDs = normalizedStrings(item.TeamIDs)
	item.AttendeeIDs = normalizedStrings(item.AttendeeIDs)
	item.ProjectIDs = normalizedStrings(item.ProjectIDs)
	item.IssueIDs = normalizedStrings(item.IssueIDs)
	item.SubscriberIDs = normalizedStrings(item.SubscriberIDs)
	if !validateResourceIDs(&data, "team", item.TeamIDs) || !validateResourceIDs(&data, "user", item.AttendeeIDs) || !validateResourceIDs(&data, "user", item.SubscriberIDs) || !validateResourceIDs(&data, "project", item.ProjectIDs) || !validateResourceIDs(&data, "issue", item.IssueIDs) {
		return fmt.Errorf("%w: meeting references an unknown resource", errInvalid)
	}
	return nil
}

func applyMeetingInput(item *domain.Meeting, input meetingInput) {
	if input.Title != nil {
		item.Title = *input.Title
	}
	if input.Description != nil {
		item.Description = strings.TrimSpace(*input.Description)
	}
	if input.AttendeeIDs != nil {
		item.AttendeeIDs = normalizedStrings(*input.AttendeeIDs)
	}
	if input.TeamIDs != nil {
		item.TeamIDs = normalizedStrings(*input.TeamIDs)
	}
	if input.ProjectIDs != nil {
		item.ProjectIDs = normalizedStrings(*input.ProjectIDs)
	}
	if input.IssueIDs != nil {
		item.IssueIDs = normalizedStrings(*input.IssueIDs)
	}
	if input.StartsAt != nil {
		item.StartsAt = input.StartsAt.UTC()
	}
	if input.DurationMins != nil {
		item.DurationMins = *input.DurationMins
	}
	if input.URL != nil {
		item.URL = strings.TrimSpace(*input.URL)
	}
	if input.Notes != nil {
		item.Notes = *input.Notes
	}
	if input.Transcript != nil {
		item.Transcript = *input.Transcript
	}
	if input.SubscriberIDs != nil {
		item.SubscriberIDs = normalizedStrings(*input.SubscriberIDs)
	}
}

func (s *server) listMeetings(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	items := settingCollection[domain.Meeting](data, meetingsSettingsKey)
	items = slices.DeleteFunc(items, func(item domain.Meeting) bool { return !scopedTeamsVisible(data, item.TeamIDs) })
	slices.SortStableFunc(items, func(a, b domain.Meeting) int { return b.StartsAt.Compare(a.StartsAt) })
	writeContentPage(w, r, items)
}

func (s *server) createMeeting(w http.ResponseWriter, r *http.Request) {
	var input meetingInput
	if !decodeJSON(w, r, &input) || input.Title == nil {
		return
	}
	data := s.workspaceData(r)
	if !allowContentMutation(w, data) {
		return
	}
	now := time.Now().UTC()
	item := domain.Meeting{ID: opaqueID("meeting_"), OrganizerID: viewerID(s, data, r), AttendeeIDs: []string{}, TeamIDs: []string{}, ProjectIDs: []string{}, IssueIDs: []string{}, SubscriberIDs: []string{}, DurationMins: 30, CreatedAt: now, UpdatedAt: now}
	applyMeetingInput(&item, input)
	if err := validateMeeting(data, &item); err != nil {
		respondMutation(w, err, http.StatusCreated, nil)
		return
	}
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "meeting.created", item.ID, input, func(next *domain.Bootstrap) error {
		values := settingCollection[domain.Meeting](*next, meetingsSettingsKey)
		values = append(values, item)
		saveSettingCollection(next, meetingsSettingsKey, values)
		appendContentFeed(next, domain.FeedItem{ID: opaqueID("feed_"), Type: "meeting.created", ActorID: item.OrganizerID, ResourceType: "meeting", ResourceID: item.ID, TeamIDs: item.TeamIDs, Title: item.Title, Body: item.Description, CreatedAt: now})
		return nil
	})
	respondMutation(w, err, http.StatusCreated, item)
}

func (s *server) updateMeeting(w http.ResponseWriter, r *http.Request) {
	var input meetingInput
	if !decodeJSON(w, r, &input) {
		return
	}
	data := s.workspaceData(r)
	if !allowContentMutation(w, data) {
		return
	}
	viewer := viewerID(s, data, r)
	var updated domain.Meeting
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "meeting.updated", r.PathValue("id"), input, func(next *domain.Bootstrap) error {
		items := settingCollection[domain.Meeting](*next, meetingsSettingsKey)
		i := slices.IndexFunc(items, func(v domain.Meeting) bool { return v.ID == r.PathValue("id") })
		if i < 0 {
			return errNotFound
		}
		if items[i].OrganizerID != viewer && !workspaceAdminRole(next.ViewerRole) {
			return errNotFound
		}
		applyMeetingInput(&items[i], input)
		items[i].UpdatedAt = time.Now().UTC()
		if err := validateMeeting(*next, &items[i]); err != nil {
			return err
		}
		updated = items[i]
		saveSettingCollection(next, meetingsSettingsKey, items)
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteMeeting(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	if !allowContentMutation(w, data) {
		return
	}
	viewer := viewerID(s, data, r)
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "meeting.deleted", r.PathValue("id"), nil, func(next *domain.Bootstrap) error {
		items := settingCollection[domain.Meeting](*next, meetingsSettingsKey)
		i := slices.IndexFunc(items, func(v domain.Meeting) bool { return v.ID == r.PathValue("id") })
		if i < 0 {
			return errNotFound
		}
		if items[i].OrganizerID != viewer && !workspaceAdminRole(next.ViewerRole) {
			return errNotFound
		}
		items = append(items[:i], items[i+1:]...)
		saveSettingCollection(next, meetingsSettingsKey, items)
		return nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
}

func (s *server) listFeedItems(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	items := settingCollection[domain.FeedItem](data, feedSettingsKey)
	types := strings.Split(r.URL.Query().Get("types"), ",")
	teamID := strings.TrimSpace(r.URL.Query().Get("teamId"))
	items = slices.DeleteFunc(items, func(item domain.FeedItem) bool {
		if !scopedTeamsVisible(data, item.TeamIDs) {
			return true
		}
		if teamID != "" && !slices.Contains(item.TeamIDs, teamID) {
			return true
		}
		return len(types) > 0 && types[0] != "" && !slices.Contains(types, item.Type)
	})
	slices.SortStableFunc(items, func(a, b domain.FeedItem) int { return b.CreatedAt.Compare(a.CreatedAt) })
	writeContentPage(w, r, items)
}

type semanticResult struct {
	domain.SearchResult
	SemanticScore int      `json:"semanticScore"`
	MatchedTerms  []string `json:"matchedTerms"`
}

var semanticSynonyms = map[string][]string{"bug": {"defect", "error", "broken", "故障", "缺陷"}, "feature": {"improvement", "enhancement", "功能", "需求"}, "urgent": {"critical", "blocker", "紧急", "阻塞"}, "document": {"doc", "spec", "文档", "规范"}, "project": {"initiative", "项目", "计划"}, "customer": {"client", "用户", "客户"}}

func semanticTerms(value string) []string {
	terms := strings.Fields(normalizeSearch(value))
	result := append([]string{}, terms...)
	for _, term := range terms {
		if values := semanticSynonyms[term]; len(values) > 0 {
			result = append(result, values...)
		}
		for canonical, values := range semanticSynonyms {
			if slices.Contains(values, term) {
				result = append(result, canonical)
			}
		}
	}
	return normalizedStrings(result)
}

func semanticTextScore(query string, fields ...string) (int, []string) {
	terms := semanticTerms(query)
	if len(terms) == 0 {
		return 1, nil
	}
	haystack := normalizeSearch(strings.Join(fields, " "))
	score := 0
	matched := []string{}
	for _, term := range terms {
		if strings.Contains(haystack, normalizeSearch(term)) {
			weight := 70
			if strings.Contains(normalizeSearch(strings.Join(fields[:min(1, len(fields))], " ")), normalizeSearch(term)) {
				weight = 140
			}
			score += weight
			matched = append(matched, term)
		}
	}
	return score, normalizedStrings(matched)
}

func (s *server) semanticSearch(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	types := searchTypes(r.URL.Query().Get("types"))
	results := []semanticResult{}
	add := func(item domain.SearchResult, fields ...string) {
		score, matched := semanticTextScore(query, fields...)
		if score > 0 {
			item.Score = score
			results = append(results, semanticResult{SearchResult: item, SemanticScore: score, MatchedTerms: matched})
		}
	}
	if types["issue"] {
		for _, item := range data.Issues {
			if value := r.URL.Query().Get("teamId"); value != "" && item.Team.ID != value {
				continue
			}
			if value := r.URL.Query().Get("stateId"); value != "" && item.State.ID != value {
				continue
			}
			labels := []string{}
			for _, label := range item.Labels {
				labels = append(labels, label.Name)
			}
			add(domain.SearchResult{ID: item.ID, Type: "issue", Title: item.Title, Subtitle: item.Team.Name, Identifier: item.Identifier, Color: item.State.Color, UpdatedAt: item.UpdatedAt}, item.Title, item.Description, item.Identifier, item.Team.Name, item.State.Name, strings.Join(labels, " "))
		}
	}
	if types["project"] {
		for _, item := range data.Projects {
			add(domain.SearchResult{ID: item.ID, Type: "project", Title: item.Name, Subtitle: item.Summary, Icon: item.Icon, Color: item.Color, UpdatedAt: item.UpdatedAt}, item.Name, item.Summary, item.Description, item.Status.Name)
		}
	}
	if types["document"] {
		for _, item := range data.Documents {
			if item.ArchivedAt == nil && documentVisibleToViewer(s, data, item) {
				add(domain.SearchResult{ID: item.ID, Type: "document", Title: item.Title, Subtitle: "Document", Icon: item.Icon, Color: item.Color, UpdatedAt: item.UpdatedAt}, item.Title, item.Content)
			}
		}
	}
	if types["initiative"] {
		for _, item := range data.Initiatives {
			add(domain.SearchResult{ID: item.ID, Type: "initiative", Title: item.Name, Subtitle: item.Summary, Icon: item.Icon, Color: item.Color, UpdatedAt: item.UpdatedAt}, item.Name, item.Summary, item.Description, item.Status, item.Health)
		}
	}
	if types["member"] {
		for _, item := range data.Users {
			add(domain.SearchResult{ID: item.ID, Type: "member", Title: item.DisplayName, Subtitle: item.Email, Email: item.Email}, item.DisplayName, item.Name, item.Email)
		}
	}
	if types["customer"] {
		for _, item := range data.Customers {
			subtitle := strings.Join(item.Domains, ", ")
			owner := ""
			if index := slices.IndexFunc(data.Users, func(user domain.User) bool { return user.ID == item.OwnerID }); index >= 0 {
				owner = data.Users[index].DisplayName
			}
			add(domain.SearchResult{ID: item.ID, Type: "customer", Title: item.Name, Subtitle: subtitle, UpdatedAt: item.UpdatedAt}, item.Name, subtitle, item.Status, item.Tier, owner)
		}
	}
	if types["release"] {
		for _, item := range data.Releases {
			if item.ArchivedAt != nil {
				continue
			}
			subtitle := strings.TrimSpace(strings.Join([]string{item.Version, item.Status}, " "))
			add(domain.SearchResult{ID: item.ID, Type: "release", Title: item.Name, Subtitle: subtitle, UpdatedAt: item.UpdatedAt}, item.Name, item.Version, item.Description, item.Status, item.ReleaseNotes)
		}
	}
	if types["view"] {
		for _, item := range data.SavedViews {
			subtitle := strings.TrimSpace(strings.Join([]string{item.Scope, item.Resource}, " "))
			add(domain.SearchResult{ID: item.ID, Type: "view", Title: item.Name, Subtitle: subtitle, Icon: item.Icon, Color: item.Color, UpdatedAt: item.UpdatedAt}, item.Name, item.Description, item.Scope, item.Resource)
		}
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].SemanticScore == results[j].SemanticScore {
			return results[i].UpdatedAt.After(results[j].UpdatedAt)
		}
		return results[i].SemanticScore > results[j].SemanticScore
	})
	facets := semanticFacets(data, results)
	start, end := contentPageBounds(r, len(results))
	next := ""
	if end < len(results) {
		next = base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(end)))
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results[start:end], "facets": facets, "nextCursor": next, "hasMore": end < len(results), "total": len(results)})
}

func semanticFacets(data domain.Bootstrap, results []semanticResult) map[string][]domain.SemanticSearchFacet {
	issueIDs := map[string]bool{}
	for _, result := range results {
		if result.Type == "issue" {
			issueIDs[result.ID] = true
		}
	}
	facets := map[string]map[string]*domain.SemanticSearchFacet{"team": {}, "status": {}, "label": {}, "assignee": {}}
	add := func(key, value, label string) {
		if value == "" {
			return
		}
		if facets[key][value] == nil {
			facets[key][value] = &domain.SemanticSearchFacet{Key: key, Value: value, Label: label}
		}
		facets[key][value].Count++
	}
	for _, issue := range data.Issues {
		if !issueIDs[issue.ID] {
			continue
		}
		add("team", issue.Team.ID, issue.Team.Name)
		add("status", issue.State.ID, issue.State.Name)
		if issue.Assignee != nil {
			add("assignee", issue.Assignee.ID, issue.Assignee.DisplayName)
		}
		for _, label := range issue.Labels {
			add("label", label.ID, label.Name)
		}
	}
	result := map[string][]domain.SemanticSearchFacet{}
	for key, values := range facets {
		for _, value := range values {
			result[key] = append(result[key], *value)
		}
		sort.Slice(result[key], func(i, j int) bool { return result[key][i].Count > result[key][j].Count })
	}
	return result
}

func (s *server) filterSuggestions(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	query := normalizeSearch(r.URL.Query().Get("q"))
	field := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("field")))
	values := map[string]map[string]*domain.FilterSuggestion{}
	add := func(key, id, label string) {
		if field != "" && field != key || query != "" && !strings.Contains(normalizeSearch(label), query) {
			return
		}
		if values[key] == nil {
			values[key] = map[string]*domain.FilterSuggestion{}
		}
		if values[key][id] == nil {
			values[key][id] = &domain.FilterSuggestion{Field: key, Value: id, Label: label}
		}
		values[key][id].Count++
	}
	for _, issue := range data.Issues {
		add("team", issue.Team.ID, issue.Team.Name)
		add("status", issue.State.ID, issue.State.Name)
		if issue.Assignee != nil {
			add("assignee", issue.Assignee.ID, issue.Assignee.DisplayName)
		}
		for _, label := range issue.Labels {
			add("label", label.ID, label.Name)
		}
		if issue.Project != nil {
			add("project", issue.Project.ID, issue.Project.Name)
		}
		if issue.CycleID != nil {
			if cycle, err := cycleByID(&data, *issue.CycleID); err == nil {
				add("cycle", cycle.ID, cycle.Name)
			}
		}
	}
	result := []domain.FilterSuggestion{}
	for _, items := range values {
		for _, item := range items {
			result = append(result, *item)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Count == result[j].Count {
			return result[i].Label < result[j].Label
		}
		return result[i].Count > result[j].Count
	})
	if len(result) > 50 {
		result = result[:50]
	}
	writeJSON(w, http.StatusOK, result)
}

// Keep unicode import meaningful and make token normalization explicit for
// callers that supply punctuation-only queries.
func hasSemanticRune(value string) bool {
	return strings.ContainsFunc(value, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) })
}
