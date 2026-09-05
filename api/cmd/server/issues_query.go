package main

// The issue query endpoint keeps large workspaces from having to download the
// complete bootstrap payload just to render a list.  Filters are intentionally
// represented as a small JSON AST so clients can compose nested AND/OR groups
// without inventing a new query parameter for every property.

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
)

type issueQueryResponse struct {
	Items      []domain.Issue `json:"items"`
	NextCursor string         `json:"nextCursor,omitempty"`
	HasMore    bool           `json:"hasMore"`
	Total      int            `json:"total"`
}

type issueQueryNode struct {
	And      []issueQueryNode `json:"and,omitempty"`
	Or       []issueQueryNode `json:"or,omitempty"`
	Field    string           `json:"field,omitempty"`
	Operator string           `json:"operator,omitempty"`
	Value    json.RawMessage  `json:"value,omitempty"`
	Values   json.RawMessage  `json:"values,omitempty"`
}

func (s *server) listIssues(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	query := r.URL.Query()

	root, err := decodeIssueQuery(query.Get("filter"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid filter expression")
		return
	}

	items := make([]domain.Issue, 0, len(data.Issues))
	for _, issue := range data.Issues {
		if !issueArchiveMatches(issue, query.Get("archived")) || !issueScopeMatches(issue, query) || !issueTextMatches(issue, query.Get("q")) {
			continue
		}
		if !root.empty() && !root.matches(issue, data) {
			continue
		}
		items = append(items, issue)
	}

	sortIssues(items, query.Get("sort"), query.Get("direction"))
	total := len(items)
	offset := decodeCursor(query.Get("cursor"))
	if offset > len(items) {
		offset = len(items)
	}
	limit, _ := strconv.Atoi(query.Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	page := items[offset:end]
	response := issueQueryResponse{Items: page, Total: total, HasMore: end < total}
	if response.HasMore {
		response.NextCursor = encodeCursor(end)
	}
	writeJSON(w, http.StatusOK, response)
}

func decodeIssueQuery(raw string) (issueQueryNode, error) {
	if strings.TrimSpace(raw) == "" {
		return issueQueryNode{}, nil
	}
	var node issueQueryNode
	if err := json.Unmarshal([]byte(raw), &node); err != nil {
		return issueQueryNode{}, err
	}
	return node, nil
}

func (node issueQueryNode) empty() bool {
	return node.Field == "" && len(node.And) == 0 && len(node.Or) == 0
}

func (node issueQueryNode) matches(issue domain.Issue, data domain.Bootstrap) bool {
	if len(node.And) > 0 {
		for _, child := range node.And {
			if !child.matches(issue, data) {
				return false
			}
		}
	}
	if len(node.Or) > 0 {
		matched := false
		for _, child := range node.Or {
			if child.matches(issue, data) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if node.Field == "" {
		return true
	}
	values := node.values()
	operator := strings.ToLower(strings.TrimSpace(node.Operator))
	if operator == "" {
		operator = "is"
	}
	actual := issueFieldValues(issue, node.Field, data)
	if operator == "isempty" || operator == "isnotempty" {
		empty := len(actual) == 0 || (len(actual) == 1 && strings.TrimSpace(actual[0]) == "")
		return empty == (operator == "isempty")
	}
	if strings.HasPrefix(operator, "within") || operator == "before" || operator == "after" || operator == "between" {
		return dateMatch(actual, operator, values)
	}
	if operator == "contains" || operator == "doesnotcontain" {
		matched := false
		for _, left := range actual {
			for _, right := range values {
				if strings.Contains(strings.ToLower(left), strings.ToLower(right)) {
					matched = true
				}
			}
		}
		return matched == (operator == "contains")
	}
	matched := false
	if strings.EqualFold(node.Field, "content") {
		for _, right := range values {
			if strings.HasPrefix(strings.ToLower(right), "query:") {
				matched = strings.Contains(strings.ToLower(issue.Title+" "+issue.Description), strings.ToLower(strings.TrimPrefix(right, "query:")))
				if matched {
					break
				}
			}
		}
		if operator == "isnot" || operator == "notin" {
			return !matched
		}
		return matched
	}
	for _, left := range actual {
		for _, right := range values {
			if strings.HasPrefix(strings.ToLower(left), "project-milestone-name-contains:") && strings.HasPrefix(strings.ToLower(right), "project-milestone-name-contains:") {
				matched = strings.Contains(strings.TrimPrefix(strings.ToLower(left), "project-milestone-name-contains:"), strings.TrimPrefix(strings.ToLower(right), "project-milestone-name-contains:"))
			} else if strings.EqualFold(left, right) {
				matched = true
			}
			if matched {
				break
			}
		}
		if matched {
			break
		}
	}
	if operator == "isnot" || operator == "notin" {
		return !matched
	}
	return matched
}

func (node issueQueryNode) values() []string {
	if len(node.Values) > 0 && string(node.Values) != "null" {
		var list []string
		if json.Unmarshal(node.Values, &list) == nil {
			return list
		}
		// Saved-view filters carry rich option objects. Prefer their stable id,
		// then value/label for clients that persist display data only.
		var options []map[string]any
		if json.Unmarshal(node.Values, &options) == nil {
			result := make([]string, 0, len(options))
			for _, option := range options {
				for _, key := range []string{"id", "value", "label"} {
					if value, ok := option[key].(string); ok {
						result = append(result, value)
						break
					}
				}
			}
			return result
		}
	}
	if len(node.Value) == 0 || string(node.Value) == "null" {
		return nil
	}
	var list []string
	if json.Unmarshal(node.Value, &list) == nil {
		return list
	}
	var scalar string
	if json.Unmarshal(node.Value, &scalar) == nil {
		return []string{scalar}
	}
	var number float64
	if json.Unmarshal(node.Value, &number) == nil {
		return []string{strconv.FormatFloat(number, 'f', -1, 64)}
	}
	return nil
}

func issueFieldValues(issue domain.Issue, field string, data domain.Bootstrap) []string {
	switch strings.ToLower(strings.TrimSpace(field)) {
	case "id":
		return []string{issue.ID}
	case "identifier":
		return []string{issue.Identifier}
	case "title":
		return []string{issue.Title}
	case "content", "description":
		return []string{issue.Title, issue.Description}
	case "team", "teamid":
		return []string{issue.Team.ID, issue.Team.Name, issue.Team.Key}
	case "status", "state", "stateid":
		return []string{issue.State.ID, issue.State.Name, issue.State.Type}
	case "priority":
		return []string{strconv.Itoa(issue.Priority), issue.PriorityLabel}
	case "assignee", "assigneeid":
		if issue.Assignee == nil {
			return nil
		}
		return []string{issue.Assignee.ID, issue.Assignee.DisplayName, issue.Assignee.Email}
	case "creator", "creatorid":
		return []string{issue.Creator.ID, issue.Creator.DisplayName, issue.Creator.Email}
	case "project", "projectid":
		if issue.Project == nil {
			return nil
		}
		values := []string{issue.Project.ID, issue.Project.Name}
		if project := findProject(data, issue.Project.ID); project != nil {
			values = append(values, "project-status:"+project.Status.ID, "project-status-type:"+project.Status.Type,
				"project-priority:"+strconv.Itoa(project.Priority), "project-milestone-name-contains:")
			if project.Lead != nil {
				values = append(values, "project-lead:"+project.Lead.ID)
			} else {
				values = append(values, "project-lead:")
			}
			for _, labelID := range project.LabelIDs {
				values = append(values, "project-label:"+labelID)
			}
			for _, milestone := range project.Milestones {
				values = append(values, "project-milestone-name-contains:"+strings.ToLower(milestone.Name))
			}
		}
		return values
	case "cycle", "cycleid":
		if issue.CycleID == nil {
			return nil
		}
		return []string{*issue.CycleID}
	case "initiative", "initiativeid":
		if issue.Project == nil {
			return nil
		}
		if project := findProject(data, issue.Project.ID); project != nil {
			return project.Initiatives
		}
		return nil
	case "release", "releaseid":
		values := []string{}
		for _, release := range data.Releases {
			if issueContainsString(release.IssueIDs, issue.ID) {
				values = append(values, "release:"+release.ID, "release-pipeline:"+release.PipelineID, "release-stage:"+release.Stage, "release-stage-type:"+release.Status)
			}
		}
		if len(values) == 0 {
			return []string{"no-releases"}
		}
		return values
	case "addedtocycle":
		return []string{issue.AddedToCycle}
	case "subscriber", "subscriberid":
		return issue.SubscriberIDs
	case "suggestedlabel", "suggestedlabelid":
		return issue.SuggestedLabelIDs
	case "agent", "agentid":
		if issue.Delegate == nil {
			return nil
		}
		return []string{"*", issue.Delegate.ID, issue.Delegate.DisplayName}
	case "agentsession", "agentsessionid":
		if issue.AgentSessionID == "" {
			return nil
		}
		return []string{"*", issue.AgentSessionID}
	case "ai":
		values := []string{}
		if issue.Assignee != nil {
			values = append(values, "assigned-to-me:"+issue.Assignee.ID)
			if issue.Assignee.ID == data.Viewer.ID {
				values = append(values, "assigned-to-me")
			}
		}
		if issue.CompletedAt != nil && issue.CompletedAt.After(time.Now().UTC().AddDate(0, 0, -30)) {
			values = append(values, "completed-last-month")
		}
		if issue.DueDate != nil {
			values = append(values, "due-next-two-weeks")
		}
		return values
	case "advanced":
		values := []string{"status:" + issue.State.ID, "status:" + issue.State.Type, "priority:" + strconv.Itoa(issue.Priority)}
		if issue.Assignee == nil {
			values = append(values, "assignee:")
		} else {
			values = append(values, "assignee:"+issue.Assignee.ID)
		}
		for _, label := range issue.Labels {
			values = append(values, "labels:"+label.ID)
		}
		if issue.Project != nil {
			values = append(values, "project:"+issue.Project.ID)
		}
		return values
	case "relation", "relations":
		values := make([]string, 0, len(issue.Relations))
		for _, relation := range issue.Relations {
			values = append(values, relation.Type)
		}
		return values
	case "links":
		if len(issue.Attachments) > 0 {
			return []string{"has-links"}
		}
		return []string{"no-links"}
	case "label", "labels", "labelid":
		result := make([]string, 0, len(issue.Labels)*2)
		for _, label := range issue.Labels {
			result = append(result, label.ID, label.Name)
		}
		return result
	case "archived":
		if issue.ArchivedAt != nil {
			return []string{"true"}
		}
		return []string{"false"}
	case "external", "externalsource":
		return []string{issue.ExternalSource}
	case "duedate":
		if issue.DueDate == nil {
			return nil
		}
		return []string{*issue.DueDate}
	case "datefilter", "dates":
		return issueDateFilterValues(issue)
	case "autoclosed":
		if issue.AutoClosed {
			return []string{"true"}
		}
		return []string{"false"}
	case "template", "templateid":
		return []string{issue.TemplateID}
	case "createdat":
		return []string{issue.CreatedAt.UTC().Format(time.RFC3339)}
	case "updatedat":
		return []string{issue.UpdatedAt.UTC().Format(time.RFC3339)}
	default:
		_ = data // reserved for relationship-backed fields added by clients
		return nil
	}
}

func dateMatch(actual []string, operator string, values []string) bool {
	if len(actual) == 0 || len(values) == 0 {
		return false
	}
	date, err := parseQueryDate(actual[0])
	if err != nil {
		return false
	}
	now := time.Now().UTC()
	if strings.HasPrefix(operator, "within") {
		days := 0
		if len(values) > 0 {
			days, _ = strconv.Atoi(strings.TrimSuffix(strings.ToLower(values[0]), "d"))
		}
		if days <= 0 {
			days = 7
		}
		return !date.Before(now.AddDate(0, 0, -days)) && !date.After(now.AddDate(0, 0, days))
	}
	bound, err := parseQueryDate(values[0])
	if err != nil {
		return false
	}
	switch operator {
	case "before":
		return date.Before(bound)
	case "after":
		return date.After(bound)
	case "between":
		if len(values) < 2 {
			return false
		}
		end, parseErr := parseQueryDate(values[1])
		return parseErr == nil && !date.Before(bound) && !date.After(end)
	default:
		return date.Equal(bound)
	}
}

func parseQueryDate(value string) (time.Time, error) {
	value = strings.TrimSpace(strings.ToLower(value))
	now := time.Now().UTC()
	switch value {
	case "today":
		y, m, d := now.Date()
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC), nil
	case "yesterday":
		return now.AddDate(0, 0, -1), nil
	case "tomorrow":
		return now.AddDate(0, 0, 1), nil
	}
	if date, err := time.Parse(time.RFC3339, value); err == nil {
		return date, nil
	}
	return time.Parse("2006-01-02", value)
}

func issueArchiveMatches(issue domain.Issue, archived string) bool {
	switch strings.ToLower(strings.TrimSpace(archived)) {
	case "true", "archived":
		return issue.ArchivedAt != nil
	case "all":
		return true
	default:
		return issue.ArchivedAt == nil
	}
}

func issueScopeMatches(issue domain.Issue, query url.Values) bool {
	if teams := splitQueryValues(query.Get("teamId")); len(teams) > 0 && !issueContainsFold(teams, issue.Team.ID, issue.Team.Key, issue.Team.Name) {
		return false
	}
	if states := splitQueryValues(query.Get("stateId")); len(states) > 0 && !issueContainsFold(states, issue.State.ID, issue.State.Type, issue.State.Name) {
		return false
	}
	if projects := splitQueryValues(query.Get("projectId")); len(projects) > 0 {
		if issue.Project == nil || !issueContainsFold(projects, issue.Project.ID, issue.Project.Name) {
			return false
		}
	}
	return true
}

func issueTextMatches(issue domain.Issue, query string) bool {
	query = strings.TrimSpace(strings.ToLower(query))
	if query == "" {
		return true
	}
	haystack := strings.ToLower(strings.Join([]string{issue.Identifier, issue.Title, issue.Description}, " "))
	return strings.Contains(haystack, query)
}

func splitQueryValues(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}

func issueContainsFold(values []string, candidates ...string) bool {
	for _, value := range values {
		for _, candidate := range candidates {
			if strings.EqualFold(value, candidate) {
				return true
			}
		}
	}
	return false
}

func sortIssues(items []domain.Issue, field, direction string) {
	field = strings.ToLower(strings.TrimSpace(field))
	desc := strings.EqualFold(direction, "desc")
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		if desc {
			left, right = right, left
		}
		cmp := 0
		switch field {
		case "priority":
			cmp = compareInt(left.Priority, right.Priority)
		case "createdat":
			cmp = compareTime(left.CreatedAt, right.CreatedAt)
		case "updatedat":
			cmp = compareTime(left.UpdatedAt, right.UpdatedAt)
		case "title":
			cmp = strings.Compare(strings.ToLower(left.Title), strings.ToLower(right.Title))
		default:
			cmp = compareFloat(left.SortOrder, right.SortOrder)
		}
		if cmp != 0 {
			return cmp < 0
		}
		// Every page needs a deterministic tie breaker; otherwise inserts or
		// concurrent updates can make offset cursors skip or duplicate rows.
		if left.ID == right.ID {
			return false
		}
		return left.ID < right.ID
	})
}

func compareInt(left, right int) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

func compareFloat(left, right float64) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

func compareTime(left, right time.Time) int {
	if left.Before(right) {
		return -1
	}
	if left.After(right) {
		return 1
	}
	return 0
}

func findProject(data domain.Bootstrap, id string) *domain.Project {
	for index := range data.Projects {
		if data.Projects[index].ID == id {
			return &data.Projects[index]
		}
	}
	return nil
}

func issueContainsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func issueDateFilterValues(issue domain.Issue) []string {
	now := time.Now().UTC()
	values := []string{}
	if !issue.CreatedAt.IsZero() {
		if !issue.CreatedAt.Before(now.Add(-24 * time.Hour)) {
			values = append(values, "created-past-day")
		}
		if !issue.CreatedAt.Before(now.AddDate(0, 0, -7)) {
			values = append(values, "created-past-week")
		}
		if !issue.CreatedAt.Before(now.AddDate(0, 0, -30)) {
			values = append(values, "created-past-month")
		}
	}
	if !issue.UpdatedAt.IsZero() {
		if !issue.UpdatedAt.Before(now.Add(-24 * time.Hour)) {
			values = append(values, "updated-past-day")
		}
		if !issue.UpdatedAt.Before(now.AddDate(0, 0, -7)) {
			values = append(values, "updated-past-week")
		}
		if !issue.UpdatedAt.Before(now.AddDate(0, 0, -30)) {
			values = append(values, "updated-past-month")
		}
	}
	if issue.StartedAt != nil {
		values = append(values, "started-any")
	}
	if issue.CompletedAt != nil {
		values = append(values, "completed-any")
	}
	if issue.AutoClosedAt != nil {
		values = append(values, "auto-closed-any")
	}
	if issue.TriagedAt != nil {
		values = append(values, "triaged-any")
	}
	if issue.StatusChangedAt != nil && issue.StatusChangedAt.Before(now.AddDate(0, 0, -7)) {
		values = append(values, "status-over-week")
	}
	if issue.DueDate == nil || strings.TrimSpace(*issue.DueDate) == "" {
		values = append(values, "no-due-date")
		return values
	}
	values = append(values, "has-due-date")
	dueValue := strings.TrimSpace(*issue.DueDate)
	if len(dueValue) > 10 {
		dueValue = dueValue[:10]
	}
	due, err := time.Parse("2006-01-02", dueValue)
	if err != nil {
		return values
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if due.Before(today) {
		values = append(values, "overdue")
	}
	if due.Equal(today) {
		values = append(values, "today")
	}
	if !due.Before(today) && !due.After(today.AddDate(0, 0, 7)) {
		values = append(values, "next-week")
	}
	return values
}

func encodeCursor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

func decodeCursor(cursor string) int {
	if cursor == "" {
		return 0
	}
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		decoded = []byte(cursor)
	}
	offset, _ := strconv.Atoi(string(decoded))
	if offset < 0 {
		return 0
	}
	return offset
}
