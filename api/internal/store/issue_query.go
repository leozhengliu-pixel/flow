package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
)

var ErrIssueQuery = errors.New("invalid issue query")

type IssueFilter struct {
	And []IssueFilter `json:"and,omitempty"`
	Or  []IssueFilter `json:"or,omitempty"`
	// Not negates a compound child (for example "is not" on a translated filter).
	Not      *IssueFilter `json:"not,omitempty"`
	Field    string       `json:"field,omitempty"`
	Operator string       `json:"operator,omitempty"`
	Values   []string     `json:"values,omitempty"`
}

type IssueRecordQuery struct {
	Workspace          string
	Filter             IssueFilter
	TeamIDs            []string
	ProjectIDs         []string
	StateIDs           []string
	ReleaseIDs         []string
	IssueIDs           []string
	RestrictToIssueIDs bool
	AllowedTeamIDs     []string // nil is unrestricted; empty is denied.
	Access             *IssueRecordAccess
	Text               string
	SearchText         string // Indexed title, identifier and description search.
	Archived           string
	Sort               string
	Direction          string
	Cursor             string
	Limit              int
	GroupBy            string
	GroupValue         *string
	// GroupValues pages one group made of several values of a column
	// grouping (a status that several team workflows share by name and
	// type); it is used instead of GroupValue.
	GroupValues []string `json:",omitempty"`
	// GroupCases defines the groups of GroupBy "cases": each row is in the
	// group of the first case whose filter it matches (a row matching none
	// is in the "" group). GroupValue then names a case's Value.
	GroupCases         []IssueGroupCase `json:",omitempty"`
	IncludeTotal       bool
	Summary            bool
	IncludeDescription bool
	// Reverse pages backward: Cursor is then the first row of the page that
	// follows the wanted one, and the page holds the Limit rows that precede
	// it (the last Limit rows of the whole result without a Cursor). Rows
	// keep the requested order. Reverse is not part of a cursor's scope, so
	// the cursors of one page serve both directions.
	Reverse bool `json:",omitempty"`
	// Cursors fills IssueRecordPage.StartCursor, EndCursor and HasPrevious
	// (connection responses). Legacy responses leave them out.
	Cursors bool `json:",omitempty"`
	// Nested lists sub-issues under their parent ("Nested sub-issues"): a row
	// whose parent also matches the query (GroupValue aside) is not a row of
	// its own; pages carry the matching descendants of their rows instead
	// (IssueRecordPage.Descendants) and group counts include them under the
	// group of their top-level row.
	Nested bool `json:",omitempty"`
}

type IssueRecordAccess struct {
	UserID         string
	WorkspaceID    string
	VisibleTeamIDs []string
	// Admin skips the team filter: the viewer may read every team's content.
	// A workspace admin gets it only while no private team is closed to them.
	Admin bool
	// WorkspaceAdmin is the viewer's workspace role. Admins manage every
	// team's settings, but read only the content listed in VisibleTeamIDs.
	WorkspaceAdmin bool
	// ArchivedTeamIDs are deleted teams in their restoration window. Their
	// issues stay hidden from everyone, including admins.
	ArchivedTeamIDs []string
}

// ArchivedTeamIDs lists deleted teams that are still restorable.
func ArchivedTeamIDs(data domain.Bootstrap) []string {
	var ids []string
	for _, team := range data.Teams {
		if team.ArchivedAt != nil {
			ids = append(ids, team.ID)
		}
	}
	return ids
}

func (s *SQLiteStore) PagedWorkspaceMetadata(ctx context.Context, workspace, userID string) (domain.Bootstrap, error) {
	_, data, ok := s.workspaceReadSource(ctx, workspace)
	if !ok {
		return data, ErrAuthForbidden
	}
	if err := s.mergeTeamDefaultFavorites(ctx, &data, userID); err != nil {
		return data, err
	}
	favorites, subscriptions := slices.Clone(data.Favorites), slices.Clone(data.Subscriptions)
	releases := slices.Clone(data.Releases)
	for i := range releases {
		releases[i].IssueIDs = slices.Clone(releases[i].IssueIDs)
		releases[i].ProjectIDs = slices.Clone(releases[i].ProjectIDs)
	}
	reviews := slices.Clone(data.Reviews)
	for i := range reviews {
		reviews[i].IssueIDs = slices.Clone(reviews[i].IssueIDs)
		reviews[i].TeamReviewers = slices.Clone(reviews[i].TeamReviewers)
	}
	// Customer requests linked to issues: the team filter below only sees the
	// (empty) paged issue list, so their issues are checked by id instead.
	customerRequests := slices.Clone(data.CustomerRequests)
	data, ok, err := s.projectBootstrapForUser(ctx, data, userID)
	if err != nil {
		return data, err
	}
	if !ok {
		return data, ErrAuthForbidden
	}
	ids := []string{}
	for _, release := range releases {
		ids = append(ids, release.IssueIDs...)
	}
	for _, review := range reviews {
		ids = append(ids, review.IssueIDs...)
	}
	for _, item := range favorites {
		if item.UserID == userID && item.ResourceType == "issue" {
			ids = append(ids, item.ResourceID)
		}
	}
	for _, item := range subscriptions {
		if item.UserID == userID && item.ResourceType == "issue" {
			ids = append(ids, item.ResourceID)
		}
	}
	for _, item := range customerRequests {
		if item.IssueID != "" {
			ids = append(ids, item.IssueID)
		}
	}
	if len(ids) > 0 {
		_, access, err := s.IssueQueryAccess(ctx, data.Workspace.URLKey, userID)
		if err != nil {
			return data, err
		}
		visible, err := s.VisibleIssueRecordIDs(ctx, IssueRecordQuery{Workspace: data.Workspace.URLKey, Access: &access}, ids)
		if err != nil {
			return data, err
		}
		data.Reviews = []domain.CodeReview{}
		data.Releases = []domain.Release{}
		visibleProjects := make(map[string]bool, len(data.Projects))
		for _, project := range data.Projects {
			visibleProjects[project.ID] = true
		}
		guest := data.ViewerRole == "guest"
		data.CustomerRequests = slices.DeleteFunc(customerRequests, func(item domain.CustomerRequest) bool {
			return guest || (item.IssueID != "" && !visible[item.IssueID]) || (item.ProjectID != "" && !visibleProjects[item.ProjectID])
		})
		for _, release := range releases {
			if release.PipelineID != "" && !slices.ContainsFunc(data.ReleasePipelines, func(p domain.ReleasePipeline) bool { return p.ID == release.PipelineID }) {
				continue
			}
			release.ProjectIDs = slices.DeleteFunc(release.ProjectIDs, func(id string) bool { return !visibleProjects[id] })
			release.IssueIDs = slices.DeleteFunc(release.IssueIDs, func(id string) bool { return !visible[id] })
			data.Releases = append(data.Releases, release)
		}
		for _, review := range reviews {
			if len(review.IssueIDs) > 0 && !slices.ContainsFunc(review.IssueIDs, func(id string) bool { return visible[id] }) {
				continue
			}
			review.IssueIDs = slices.DeleteFunc(review.IssueIDs, func(id string) bool { return !visible[id] })
			review.TeamReviewers = slices.DeleteFunc(review.TeamReviewers, func(id string) bool {
				return !slices.ContainsFunc(data.Teams, func(team domain.Team) bool { return team.ID == id })
			})
			review.Favorite = slices.ContainsFunc(favorites, func(item domain.Favorite) bool {
				return item.UserID == userID && item.ResourceType == "review" && (item.ResourceID == review.ID || item.ResourceID == review.SlugID)
			})
			data.Reviews = append(data.Reviews, review)
		}
		data.Favorites = slices.DeleteFunc(data.Favorites, func(item domain.Favorite) bool { return item.ResourceType == "issue" })
		data.Subscriptions = slices.DeleteFunc(data.Subscriptions, func(item domain.Subscription) bool { return item.ResourceType == "issue" })
		for _, item := range favorites {
			if item.UserID == userID && item.ResourceType == "issue" && visible[item.ResourceID] {
				data.Favorites = append(data.Favorites, item)
			}
		}
		for _, item := range subscriptions {
			if item.UserID == userID && item.ResourceType == "issue" && visible[item.ResourceID] {
				data.Subscriptions = append(data.Subscriptions, item)
			}
		}
	}
	data.Favorites = slices.DeleteFunc(data.Favorites, func(item domain.Favorite) bool { return item.ResourceType == "review" })
	data.Subscriptions = slices.DeleteFunc(data.Subscriptions, func(item domain.Subscription) bool { return item.ResourceType == "review" })
	reviewVisible := func(id string) bool {
		return slices.ContainsFunc(data.Reviews, func(review domain.CodeReview) bool { return review.ID == id || review.SlugID == id })
	}
	for _, item := range favorites {
		if item.UserID == userID && item.ResourceType == "review" && reviewVisible(item.ResourceID) {
			data.Favorites = append(data.Favorites, item)
		}
	}
	for _, item := range subscriptions {
		if item.UserID == userID && item.ResourceType == "review" && reviewVisible(item.ResourceID) {
			data.Subscriptions = append(data.Subscriptions, item)
		}
	}
	data.IssueCollectionPaged = true
	data.Favorites = slices.DeleteFunc(data.Favorites, func(item domain.Favorite) bool { return item.ResourceType == "release" })
	data.Subscriptions = slices.DeleteFunc(data.Subscriptions, func(item domain.Subscription) bool { return item.ResourceType == "release" })
	releaseVisible := func(id string) bool {
		return slices.ContainsFunc(data.Releases, func(release domain.Release) bool { return release.ID == id })
	}
	for _, item := range favorites {
		if item.UserID == userID && item.ResourceType == "release" && releaseVisible(item.ResourceID) {
			data.Favorites = append(data.Favorites, item)
		}
	}
	for _, item := range subscriptions {
		if item.UserID == userID && item.ResourceType == "release" && releaseVisible(item.ResourceID) {
			data.Subscriptions = append(data.Subscriptions, item)
		}
	}
	return data, nil
}

func (s *SQLiteStore) IssueQueryAccess(ctx context.Context, workspace, userID string) (domain.Bootstrap, IssueRecordAccess, error) {
	data, ok := s.IssueAccessMetadata(ctx, workspace)
	if !ok {
		return data, IssueRecordAccess{}, ErrAuthForbidden
	}
	role, status, err := s.WorkspaceRole(ctx, data.Workspace.ID, userID)
	if err != nil || status != "active" {
		return data, IssueRecordAccess{}, ErrAuthForbidden
	}
	user, err := s.authUserByID(ctx, userID)
	if err != nil {
		return data, IssueRecordAccess{}, err
	}
	data.Viewer = user
	data.ViewerRole = role
	rows, err := s.db.QueryContext(ctx, `SELECT team_id,role FROM team_memberships WHERE workspace_id=? AND user_id=?`, data.Workspace.ID, userID)
	if err != nil {
		return data, IssueRecordAccess{}, err
	}
	defer rows.Close()
	for rows.Next() {
		member := domain.TeamMember{UserID: userID}
		if err := rows.Scan(&member.TeamID, &member.Role); err != nil {
			return data, IssueRecordAccess{}, err
		}
		data.TeamMembers = append(data.TeamMembers, member)
	}
	if err := rows.Err(); err != nil {
		return data, IssueRecordAccess{}, err
	}
	visible := visibleIssueTeams(data, userID, role)
	active := 0
	for _, team := range data.Teams {
		if team.ArchivedAt == nil {
			active++
		}
	}
	admin := isWorkspaceAdminRole(role)
	access := IssueRecordAccess{UserID: userID, WorkspaceID: data.Workspace.ID, Admin: admin && len(visible) == active, WorkspaceAdmin: admin, VisibleTeamIDs: visible, ArchivedTeamIDs: ArchivedTeamIDs(data)}
	return data, access, nil
}

func issueAccessCTE(query IssueRecordQuery) (string, []any) {
	access := query.Access
	if access == nil || access.Admin {
		return "", nil
	}
	team, teamArgs := bindList("p.subject_id", access.VisibleTeamIDs)
	prefix := `WITH RECURSIVE shared_issues(id) AS (
	 SELECT p.issue_id FROM issue_permission_records p WHERE p.workspace_key=? AND (
	 (p.subject_type='user' AND p.subject_id=?) OR
	 (p.subject_type='workspace' AND p.subject_id IN ('',?,?)) OR
	 (p.subject_type='team' AND ` + team + `))
	 UNION SELECT child.id FROM issue_records child JOIN shared_issues parent ON child.parent_id=parent.id WHERE child.workspace_key=?
	) `
	args := []any{query.Workspace, access.UserID, access.WorkspaceID, query.Workspace}
	args = append(args, teamArgs...)
	args = append(args, query.Workspace)
	return prefix, args
}

type IssueRecordPage struct {
	Items      []domain.Issue `json:"items"`
	NextCursor string         `json:"nextCursor,omitempty"`
	HasMore    bool           `json:"hasMore"`
	Total      int64          `json:"total"`
	// StartCursor and EndCursor address the first and last item of the page
	// (pass as before / after); HasPrevious reports rows before the page.
	// Set only for queries with Cursors.
	StartCursor string `json:"startCursor,omitempty"`
	EndCursor   string `json:"endCursor,omitempty"`
	HasPrevious bool   `json:"hasPrevious,omitempty"`
	// Descendants are the sub-issues (all levels) of the page's items that
	// match the query, in the page's order; set only for nested queries.
	// DescendantsTruncated reports that more than 1000 matched (only the
	// first 1000 are returned).
	Descendants          []domain.Issue `json:"descendants,omitempty"`
	DescendantsTruncated bool           `json:"descendantsTruncated,omitempty"`
}

type IssueRecordGroup struct {
	Value string `json:"value"`
	Count int64  `json:"count"`
}

// IssueGroupCase is one group of a "cases" grouping (for example a Focus
// bucket): the rows that match Filter and no earlier case.
type IssueGroupCase struct {
	Value  string      `json:"value"`
	Filter IssueFilter `json:"filter"`
}

// MaxIssueGroupCases bounds the cases of one grouping.
const MaxIssueGroupCases = 64

// issueGroupCaseExpression is the case index (0-based, -1 for no case) of
// alias i's row. Case values never reach the SQL: groups map the index back.
func issueGroupCaseExpression(cases []IssueGroupCase) (string, []any, error) {
	if len(cases) == 0 || len(cases) > MaxIssueGroupCases {
		return "", nil, ErrIssueQuery
	}
	budget := 400
	seen := make(map[string]bool, len(cases))
	var expression strings.Builder
	var args []any
	expression.WriteString("(CASE")
	for index, item := range cases {
		if item.Value == "" || len(item.Value) > 191 || seen[item.Value] {
			return "", nil, ErrIssueQuery
		}
		seen[item.Value] = true
		clause, values, err := compileIssueFilter(item.Filter, 0, &budget)
		if err != nil {
			return "", nil, err
		}
		expression.WriteString(" WHEN " + clause + " THEN " + strconv.Itoa(index))
		args = append(args, values...)
	}
	expression.WriteString(" ELSE -1 END)")
	return expression.String(), args, nil
}

// issueGroupCaseValue is the group value of a case index scanned from SQL.
func issueGroupCaseValue(cases []IssueGroupCase, index string) (string, bool) {
	position, err := strconv.Atoi(index)
	if err != nil || position >= len(cases) {
		return "", false
	}
	if position < 0 {
		return "", true
	}
	return cases[position].Value, true
}

// withoutGroupValue is the query with no group selected, for counts and the
// conditions every group shares.
func (query IssueRecordQuery) withoutGroupValue() IssueRecordQuery {
	query.GroupValue, query.GroupValues = nil, nil
	return query
}

type issueRecordCursor struct {
	Version int    `json:"v"`
	Scope   string `json:"scope"`
	Value   string `json:"value"`
	ID      string `json:"id"`
}

var issueQueryColumns = map[string]string{
	"id": "id", "identifier": "identifier", "team": "team_id", "teamId": "team_id",
	"status": "state_id", "stateId": "state_id", "statusType": "state_type",
	"project": "project_id", "projectId": "project_id", "assignee": "assignee_id",
	"creator": "creator_id", "cycle": "cycle_id", "parent": "parent_id",
	"priority": "priority", "createdAt": "created_at", "updatedAt": "updated_at",
	"title": "title", "sortOrder": "sort_order",
}

func bindList(column string, values []string) (string, []any) {
	if len(values) == 0 {
		return "1=0", nil
	}
	args := make([]any, len(values))
	for i, v := range values {
		args[i] = v
	}
	return column + " IN (" + strings.TrimSuffix(strings.Repeat("?,", len(values)), ",") + ")", args
}

// issueFilterFieldAliases maps a filter field's other names (lower case) to
// the name compileIssueFilter compiles.
var issueFilterFieldAliases = map[string]string{"state": "status", "stateid": "status", "teamid": "team", "projectid": "project", "assigneeid": "assignee", "creatorid": "creator", "cycleid": "cycle", "label": "labels", "labelid": "labels", "subscriber": "subscribers", "subscriberid": "subscribers", "createdat": "createdAt", "updatedat": "updatedAt", "sortorder": "sortOrder", "myactivity": "myActivity", "statustype": "statusType"}

func compileIssueFilter(node IssueFilter, depth int, remaining *int) (string, []any, error) {
	return compileIssueFilterScoped(node, depth, remaining, nil)
}

func compileIssueFilterScoped(node IssueFilter, depth int, remaining *int, query *IssueRecordQuery) (string, []any, error) {
	if field, ok := issueFilterFieldAliases[strings.ToLower(node.Field)]; ok {
		node.Field = field
	}
	*remaining--
	if depth > 8 || *remaining < 0 || len(node.Values) > 1000 {
		return "", nil, ErrIssueQuery
	}
	var clauses []string
	var args []any
	for _, group := range []struct {
		nodes []IssueFilter
		join  string
	}{{node.And, " AND "}, {node.Or, " OR "}} {
		var children []string
		for _, child := range group.nodes {
			sql, values, err := compileIssueFilterScoped(child, depth+1, remaining, query)
			if err != nil {
				return "", nil, err
			}
			children = append(children, sql)
			args = append(args, values...)
		}
		if len(children) > 0 {
			clauses = append(clauses, "("+strings.Join(children, group.join)+")")
		}
	}
	if node.Not != nil {
		sql, values, err := compileIssueFilterScoped(*node.Not, depth+1, remaining, query)
		if err != nil {
			return "", nil, err
		}
		clauses = append(clauses, "NOT ("+sql+")")
		args = append(args, values...)
	}
	if node.Field != "" {
		if node.Field == "sharedWith" {
			// Issues explicitly shared with these users (issue permission grants).
			if len(node.Values) == 0 || len(node.Values) > 100 {
				return "", nil, ErrIssueQuery
			}
			users, values := bindList("p.subject_id", node.Values)
			clauses = append(clauses, "EXISTS (SELECT 1 FROM issue_permission_records p WHERE p.workspace_key=i.workspace_key AND p.issue_id=i.id AND p.subject_type='user' AND "+users+")")
			args = append(args, values...)
			return "(" + strings.Join(clauses, " AND ") + ")", args, nil
		}
		if node.Field == "customerId" || node.Field == "customers" {
			clause, values, err := compileCustomerFilter(node)
			if err != nil {
				return "", nil, err
			}
			clauses = append(clauses, clause)
			args = append(args, values...)
			return "(" + strings.Join(clauses, " AND ") + ")", args, nil
		}
		if node.Field == "agentSessionState" {
			clause, values, err := compileAgentSessionState(node)
			if err != nil {
				return "", nil, err
			}
			clauses = append(clauses, clause)
			args = append(args, values...)
			return "(" + strings.Join(clauses, " AND ") + ")", args, nil
		}
		if node.Field == "hasSubIssues" {
			op := strings.ToLower(node.Operator)
			if op == "" {
				op = "is"
			}
			if op != "isempty" && op != "isnotempty" {
				return "", nil, fmt.Errorf("%w: unsupported operator %s", ErrIssueQuery, node.Operator)
			}
			childScope := "child.workspace_key=i.workspace_key AND child.parent_id=i.id AND child.archived=0"
			if query != nil {
				visibility, values := issueRecordVisibilityClause(*query, "child")
				if visibility != "" {
					childScope += " AND " + visibility
					args = append(args, values...)
				}
			}
			clause := "EXISTS (SELECT 1 FROM issue_records child WHERE " + childScope + ")"
			if op == "isempty" {
				clause = "NOT (" + clause + ")"
			}
			clauses = append(clauses, clause)
			return "(" + strings.Join(clauses, " AND ") + ")", args, nil
		}
		if isReleaseFilterField(node.Field) {
			clause, values, err := compileReleaseFilter(node)
			if err != nil {
				return "", nil, err
			}
			clauses = append(clauses, clause)
			args = append(args, values...)
			return "(" + strings.Join(clauses, " AND ") + ")", args, nil
		}
		if isIssueAttributeField(node.Field) {
			clause, values, err := compileIssueAttribute(node)
			if err != nil {
				return "", nil, err
			}
			clauses = append(clauses, clause)
			args = append(args, values...)
			return "(" + strings.Join(clauses, " AND ") + ")", args, nil
		}
		if node.Field == "createdAt" || node.Field == "updatedAt" {
			values := make([]string, len(node.Values))
			for i, value := range node.Values {
				date, err := time.Parse(time.RFC3339Nano, value)
				if err != nil {
					date, err = time.Parse("2006-01-02", value)
				}
				if err != nil {
					return "", nil, ErrIssueQuery
				}
				values[i] = date.UTC().Format(issueRecordTimestamp)
			}
			node.Values = values
		}
		column, ok := issueQueryColumns[node.Field]
		if node.Field == "labels" || node.Field == "subscribers" || node.Field == "myActivity" {
			column = "label_id"
			ok = true
		}
		if !ok {
			return "", nil, fmt.Errorf("%w: unsupported field %s", ErrIssueQuery, node.Field)
		}
		op := strings.ToLower(node.Operator)
		if op == "" {
			op = "is"
		}
		var clause string
		var values []any
		relationTable := map[string]string{"labels": "issue_label_records", "subscribers": "issue_subscriber_records", "myActivity": "issue_actor_records"}[node.Field]
		switch op {
		case "is", "in", "isnot", "notin":
			clause, values = bindList("i."+column, node.Values)
			if node.Field == "status" {
				types, typeArgs := bindList("i.state_type", node.Values)
				clause = "(" + clause + " OR " + types + ")"
				values = append(values, typeArgs...)
			}
			if node.Field == "labels" {
				clause, values = bindList("l.label_id", node.Values)
				clause = "EXISTS (SELECT 1 FROM issue_label_records l WHERE l.workspace_key=i.workspace_key AND l.issue_id=i.id AND " + clause + ")"
			}
			if node.Field == "subscribers" || node.Field == "myActivity" {
				table := "issue_subscriber_records"
				if node.Field == "myActivity" {
					table = "issue_actor_records"
				}
				clause, values = bindList("l.user_id", node.Values)
				clause = "EXISTS (SELECT 1 FROM " + table + " l WHERE l.workspace_key=i.workspace_key AND l.issue_id=i.id AND " + clause + ")"
			}
			if relationTable != "" && slices.Contains(node.Values, "") {
				clause = "(" + clause + " OR NOT EXISTS (SELECT 1 FROM " + relationTable + " l WHERE l.workspace_key=i.workspace_key AND l.issue_id=i.id))"
			}
			if op == "isnot" || op == "notin" {
				clause = "NOT (" + clause + ")"
			}
		case "includesall", "excludesall":
			// The reference app's "include all of": one indexed EXISTS per value, all required.
			if relationTable == "" || len(node.Values) == 0 || len(node.Values) > 100 {
				return "", nil, ErrIssueQuery
			}
			valueColumn := "l.user_id"
			if node.Field == "labels" {
				valueColumn = "l.label_id"
			}
			parts := make([]string, 0, len(node.Values))
			for _, value := range node.Values {
				if value == "" {
					parts = append(parts, "NOT EXISTS (SELECT 1 FROM "+relationTable+" l WHERE l.workspace_key=i.workspace_key AND l.issue_id=i.id)")
					continue
				}
				parts = append(parts, "EXISTS (SELECT 1 FROM "+relationTable+" l WHERE l.workspace_key=i.workspace_key AND l.issue_id=i.id AND "+valueColumn+"=?)")
				values = append(values, value)
			}
			clause = "(" + strings.Join(parts, " AND ") + ")"
			if op == "excludesall" {
				clause = "NOT " + clause
			}
		case "isempty", "isnotempty":
			clause = "i." + column + "=''"
			if node.Field == "labels" {
				clause = "NOT EXISTS (SELECT 1 FROM issue_label_records l WHERE l.workspace_key=i.workspace_key AND l.issue_id=i.id)"
			}
			if relationTable != "" {
				clause = "NOT EXISTS (SELECT 1 FROM " + relationTable + " l WHERE l.workspace_key=i.workspace_key AND l.issue_id=i.id)"
			}
			if op == "isnotempty" {
				clause = "NOT (" + clause + ")"
			}
		case "before", "after", "gt", "gte", "lt", "lte":
			if len(node.Values) != 1 || relationTable != "" {
				return "", nil, ErrIssueQuery
			}
			operator := map[string]string{"before": "<", "after": ">", "gt": ">", "gte": ">=", "lt": "<", "lte": "<="}[op]
			clause = "i." + column + operator + "?"
			values = []any{node.Values[0]}
		case "contains", "doesnotcontain":
			if node.Field != "title" || len(node.Values) != 1 {
				return "", nil, ErrIssueQuery
			}
			clause = "LOWER(i.title) LIKE ? ESCAPE '!'"
			values = []any{"%" + escapeIssueLike(strings.ToLower(node.Values[0])) + "%"}
			if op == "doesnotcontain" {
				clause = "NOT (" + clause + ")"
			}
		default:
			return "", nil, fmt.Errorf("%w: unsupported operator %s", ErrIssueQuery, node.Operator)
		}
		clauses = append(clauses, clause)
		args = append(args, values...)
	}
	if len(clauses) == 0 {
		return "1=1", nil, nil
	}
	return "(" + strings.Join(clauses, " AND ") + ")", args, nil
}

func ValidateIssueFilter(filter IssueFilter) error {
	budget := 256
	_, _, err := compileIssueFilter(filter, 0, &budget)
	return err
}

func escapeIssueLike(value string) string {
	return strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(value)
}

// issueRecordVisibilityClause is the content scope shared by the main issue
// query and relationship probes such as hasSubIssues. The alias is supplied
// by the caller so correlated child rows use exactly the same permission
// model as the outer issue row.
func issueRecordVisibilityClause(query IssueRecordQuery, alias string) (string, []any) {
	clauses := []string{}
	args := []any{}
	if query.Access != nil && !query.Access.Admin {
		teams, values := bindList(alias+".team_id", query.Access.VisibleTeamIDs)
		clauses = append(clauses, "("+teams+" OR "+alias+".id IN (SELECT id FROM shared_issues))")
		args = append(args, values...)
	}
	if query.Access != nil && len(query.Access.ArchivedTeamIDs) > 0 {
		archivedTeams, values := bindList(alias+".team_id", query.Access.ArchivedTeamIDs)
		clauses = append(clauses, "NOT "+archivedTeams)
		args = append(args, values...)
	}
	if query.AllowedTeamIDs != nil {
		teams, values := bindList(alias+".team_id", query.AllowedTeamIDs)
		clauses = append(clauses, teams)
		args = append(args, values...)
	}
	return strings.Join(clauses, " AND "), args
}

func issueRecordWhere(query IssueRecordQuery) (string, []any, error) {
	if query.Workspace == "" {
		return "", nil, ErrIssueQuery
	}
	clauses := []string{"i.workspace_key=?"}
	args := []any{query.Workspace}
	if query.Access != nil && !query.Access.Admin && query.Access.UserID == "" {
		return "", nil, ErrIssueQuery
	}
	if visibility, values := issueRecordVisibilityClause(query, "i"); visibility != "" {
		clauses = append(clauses, visibility)
		args = append(args, values...)
	}
	if query.Archived != "all" {
		archived := 0
		if query.Archived == "true" {
			archived = 1
		}
		clauses = append(clauses, "i.archived=?")
		args = append(args, archived)
	}
	for _, item := range []struct {
		column   string
		values   []string
		required bool
	}{{"team_id", query.TeamIDs, false}, {"project_id", query.ProjectIDs, false}, {"state_id", query.StateIDs, false}, {"id", query.IssueIDs, query.RestrictToIssueIDs}} {
		if len(item.values) > 0 || item.required {
			clause, values := bindList("i."+item.column, item.values)
			clauses = append(clauses, clause)
			args = append(args, values...)
		}
	}
	if query.Text != "" {
		clauses = append(clauses, "(LOWER(i.title) LIKE ? ESCAPE '!' OR LOWER(i.identifier) LIKE ? ESCAPE '!')")
		value := "%" + escapeIssueLike(strings.ToLower(query.Text)) + "%"
		args = append(args, value, value)
	}
	budget := 100
	filter, values, err := compileIssueFilterScoped(query.Filter, 0, &budget, &query)
	if err != nil {
		return "", nil, err
	}
	clauses = append(clauses, filter)
	args = append(args, values...)
	if query.GroupBy == "cases" {
		// Validated even when no group is selected, so counts reject bad cases too.
		expression, values, err := issueGroupCaseExpression(query.GroupCases)
		if err != nil || len(query.GroupValues) > 0 {
			return "", nil, ErrIssueQuery
		}
		if query.GroupValue != nil {
			index := slices.IndexFunc(query.GroupCases, func(item IssueGroupCase) bool { return item.Value == *query.GroupValue })
			if index < 0 && *query.GroupValue != "" {
				clauses = append(clauses, "1=0")
			} else {
				clauses = append(clauses, expression+"="+strconv.Itoa(index))
				args = append(args, values...)
			}
		}
		return strings.Join(clauses, " AND "), args, nil
	}
	if len(query.GroupValues) > 0 {
		// One group of several values: only column groupings (status) take one.
		column, err := issueGroupColumn(query.GroupBy)
		if err != nil || query.GroupValue != nil || len(query.GroupValues) > 1000 {
			return "", nil, ErrIssueQuery
		}
		clause, values := bindList("i."+column, query.GroupValues)
		clauses = append(clauses, clause)
		args = append(args, values...)
		return strings.Join(clauses, " AND "), args, nil
	}
	if query.GroupValue != nil && query.GroupBy == "customer" {
		// Customer groups are multi-membership: an issue is in every requesting customer's group.
		clause, values := customerGroupClause(*query.GroupValue)
		clauses = append(clauses, clause)
		args = append(args, values...)
		return strings.Join(clauses, " AND "), args, nil
	}
	if query.GroupValue != nil && query.GroupBy != "none" {
		if field := issueGroupAttribute(query.GroupBy); field != "" {
			clause, values, err := compileIssueAttribute(IssueFilter{Field: field, Values: []string{*query.GroupValue}})
			if err != nil {
				return "", nil, err
			}
			clauses = append(clauses, clause)
			args = append(args, values...)
			return strings.Join(clauses, " AND "), args, nil
		}
		column, err := issueGroupColumn(query.GroupBy)
		if err != nil {
			return "", nil, err
		}
		clauses = append(clauses, "i."+column+"=?")
		args = append(args, *query.GroupValue)
	}
	return strings.Join(clauses, " AND "), args, nil
}

func issueGroupColumn(group string) (string, error) {
	switch group {
	case "status":
		return "state_id", nil
	case "priority":
		return "priority", nil
	case "assignee":
		return "assignee_id", nil
	case "creator":
		return "creator_id", nil
	case "team":
		return "team_id", nil
	case "project":
		return "project_id", nil
	case "cycle":
		return "cycle_id", nil
	}
	return "", fmt.Errorf("%w: unsupported grouping", ErrIssueQuery)
}

func issueGroupAttribute(group string) string {
	switch group {
	case "label":
		return "firstLabel"
	case "milestone":
		return "projectMilestoneId"
	case "estimate":
		return "estimate"
	}
	return ""
}

// Release-scoped queries resolve the current associated issue IDs and constrain
// the row set to those IDs, not the whole workspace collection. The resolved IDs
// participate in the cursor scope hash so pages cannot cross another release.
func (s *SQLiteStore) applyIssueRecordScope(ctx context.Context, query *IssueRecordQuery) error {
	if len(query.ReleaseIDs) == 0 {
		return nil
	}
	ids, err := s.releaseAssociatedIssueIDs(ctx, query.Workspace, query.ReleaseIDs)
	if err != nil {
		return err
	}
	query.IssueIDs = ids
	query.RestrictToIssueIDs = true
	return nil
}

func (s *SQLiteStore) releaseAssociatedIssueIDs(ctx context.Context, workspace string, releaseIDs []string) ([]string, error) {
	ids := []string{}
	_, data, ok := s.workspaceReadSource(ctx, workspace)
	if !ok {
		return ids, nil
	}
	wanted := make(map[string]bool, len(releaseIDs))
	for _, id := range releaseIDs {
		if id != "" {
			wanted[id] = true
		}
	}
	if len(wanted) == 0 {
		return ids, nil
	}
	seen := map[string]bool{}
	for _, release := range data.Releases {
		if !wanted[release.ID] && !wanted[release.SlugID] {
			continue
		}
		for _, issueID := range release.IssueIDs {
			if issueID == "" || seen[issueID] {
				continue
			}
			seen[issueID] = true
			ids = append(ids, issueID)
		}
	}
	slices.Sort(ids)
	return ids, nil
}

func (s *SQLiteStore) QueryIssueRecords(ctx context.Context, query IssueRecordQuery) (IssueRecordPage, error) {
	page := IssueRecordPage{Items: []domain.Issue{}, Total: -1}
	if err := s.applyIssueRecordScope(ctx, &query); err != nil {
		return page, err
	}
	cached, cacheKey, ok := s.cacheGetIssueQuery(ctx, query)
	if ok {
		if err := s.resolveIssueReferences(ctx, query.Workspace, cached.Items); err != nil {
			return cached, err
		}
		if err := s.resolveIssueReferences(ctx, query.Workspace, cached.Descendants); err != nil {
			return cached, err
		}
		return cached, nil
	}
	page, err := s.queryIssueRecordsSQL(ctx, query)
	if err == nil {
		s.cacheSetIssueQuery(ctx, cacheKey, page)
	}
	if err != nil {
		return page, err
	}
	if err := s.resolveIssueReferences(ctx, query.Workspace, page.Items); err != nil {
		return page, err
	}
	if err := s.resolveIssueReferences(ctx, query.Workspace, page.Descendants); err != nil {
		return page, err
	}
	return page, nil
}

// issueRecordSelection builds the row source, visibility CTE and predicates
// shared by the paged query and the plain COUNT(*). Keeping them in one place
// makes issueRecordTotal's equivalence test check the same SQL the fallback
// runs.
func (s *SQLiteStore) issueRecordSelection(query IssueRecordQuery) (string, []any, string, []any, error) {
	where, args, err := issueRecordWhere(query)
	if err != nil {
		return "", nil, "", nil, err
	}
	if query.SearchText != "" {
		selection, values := s.issueTextSelection(query.Workspace, query.SearchText)
		where += " AND i.id IN (" + selection + ")"
		args = append(args, values...)
	}
	if query.Nested {
		root, values, err := s.issueNestedRootClause(query)
		if err != nil {
			return "", nil, "", nil, err
		}
		where += " AND " + root
		args = append(args, values...)
	}
	prefix, prefixArgs := issueAccessCTE(query)
	return prefix, prefixArgs, where, args, nil
}

// countIssueRecordsSQL is the exact COUNT(*)* row count issueRecordTotal
// falls back to.
func (s *SQLiteStore) countIssueRecordsSQL(ctx context.Context, query IssueRecordQuery) (int64, error) {
	prefix, prefixArgs, where, args, err := s.issueRecordSelection(query)
	if err != nil {
		return 0, err
	}
	var total int64
	err = s.db.QueryRowContext(ctx, prefix+"SELECT COUNT(*) FROM issue_records i WHERE "+where, append(prefixArgs, args...)...).Scan(&total)
	return total, err
}

func (s *SQLiteStore) queryIssueRecordsSQL(ctx context.Context, query IssueRecordQuery) (IssueRecordPage, error) {
	page := IssueRecordPage{Items: []domain.Issue{}, Total: -1}
	prefix, prefixArgs, where, args, err := s.issueRecordSelection(query)
	if err != nil {
		return page, err
	}
	column := "sort_order"
	expression := ""
	switch query.Sort {
	case "", "sortOrder":
	case "priority":
		// The reference app orders "No priority" (0) after Low (4).
		column = "priority"
		expression = "(CASE WHEN i.priority=0 THEN 5 ELSE i.priority END)"
	case "createdAt":
		column = "created_at"
	case "updatedAt":
		column = "updated_at"
	case "title":
		column = "title"
	case "customerCount", "customerImportantCount", "customerRevenue":
		// The reference app's customer orderings: computed per row from the customer request index.
		column = "customer"
		expression = customerSortExpression(query.Sort)
	default:
		return page, ErrIssueQuery
	}
	if expression == "" {
		expression = "i." + column
	}
	direction := "ASC"
	if query.Direction == "desc" {
		direction = "DESC"
	} else if query.Direction != "" && query.Direction != "asc" {
		return page, ErrIssueQuery
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	scope := query
	scope.Cursor = ""
	scope.Limit = 0
	scope.IncludeTotal = false
	scope.Reverse, scope.Cursors = false, false
	raw, _ := json.Marshal(scope)
	hash := fmt.Sprintf("%x", sha256.Sum256(raw))
	if query.IncludeTotal {
		if total, ok, err := s.issueRecordTotal(ctx, query); err != nil {
			return page, err
		} else if ok {
			page.Total = total
		} else if page.Total, err = s.countIssueRecordsSQL(ctx, query); err != nil {
			return page, err
		}
	}
	if query.Cursor != "" {
		if len(query.Cursor) > 4096 {
			return page, ErrIssueQuery
		}
		raw, err := base64.RawURLEncoding.DecodeString(query.Cursor)
		var cursor issueRecordCursor
		if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.Version != 1 || cursor.Scope != hash || cursor.ID == "" {
			return page, ErrIssueQuery
		}
		var value any = cursor.Value
		if column == "priority" || column == "sort_order" || column == "customer" {
			number, err := strconv.ParseFloat(cursor.Value, 64)
			if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
				return page, ErrIssueQuery
			}
			value = number
		}
		operator := ">"
		if direction == "DESC" {
			operator = "<"
		}
		if query.Reverse {
			// Backward: the rows that sort before the cursor row.
			operator = map[string]string{">": "<", "<": ">"}[operator]
		}
		where += " AND (" + expression + operator + "? OR (" + expression + "=? AND i.id" + operator + "?))"
		args = append(args, value, value, cursor.ID)
	}
	payload := "i.data"
	if query.Summary {
		payload = "COALESCE(i.list_data,i.data)"
	}
	descriptionColumn := "''"
	if query.Summary && query.IncludeDescription {
		descriptionColumn = "COALESCE(" + s.jsonText("i.data", "description") + ", '')"
	}
	scanDirection := direction
	if query.Reverse {
		// The index is read from the far end; the page is turned around below.
		scanDirection = map[string]string{"ASC": "DESC", "DESC": "ASC"}[direction]
	}
	from := "issue_records i"
	if s.dialect == "mysql" && len(query.TeamIDs) > 0 && query.Sort == "updatedAt" {
		// Read the team's rows in updatedAt order from the matching index;
		// otherwise MySQL scans the whole workspace in updatedAt order and
		// filters the team afterwards (slow for a saved view with no matches).
		from = "issue_records i FORCE INDEX (issue_records_team_updated_idx)"
	}
	rows, err := s.db.QueryContext(ctx, prefix+"SELECT "+payload+","+expression+","+descriptionColumn+" FROM "+from+" WHERE "+where+" ORDER BY "+expression+" "+scanDirection+",i.id "+scanDirection+" LIMIT ?", append(append(prefixArgs, args...), limit+1)...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	lastValue := ""
	pageBytes := 0
	sortValues := []string{}
	for rows.Next() {
		var raw []byte
		var sortValue string
		var description string
		if err := rows.Scan(&raw, &sortValue, &description); err != nil {
			return page, err
		}
		if len(page.Items) == limit || query.Summary && len(page.Items) > 0 && pageBytes+len(raw)+len(description) > 4<<20 {
			page.HasMore = true
			break
		}
		var issue domain.Issue
		if err := json.Unmarshal(raw, &issue); err != nil {
			return page, err
		}
		normalizeIssueRecord(&issue)
		if query.Summary {
			issue = issueListProjection(issue)
			if query.IncludeDescription {
				issue.Description = description
			}
		}
		page.Items = append(page.Items, issue)
		sortValues = append(sortValues, sortValue)
		pageBytes += len(raw) + len(description)
		lastValue = sortValue
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	rows.Close()
	if query.Nested && len(page.Items) > 0 {
		page.Descendants, page.DescendantsTruncated, err = s.issueNestedDescendants(ctx, query, page.Items, expression+" "+direction+",i.id "+direction, payload, descriptionColumn)
		if err != nil {
			return page, err
		}
	}
	encode := func(index int) string {
		raw, _ := json.Marshal(issueRecordCursor{Version: 1, Scope: hash, Value: sortValues[index], ID: page.Items[index].ID})
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	if query.Reverse {
		// More rows beyond the page's far edge mean earlier rows exist; a
		// cursor row (that existed when the cursor was issued) follows it.
		hasPrevious := page.HasMore
		slices.Reverse(page.Items)
		slices.Reverse(sortValues)
		page.HasMore, page.HasPrevious = query.Cursor != "", hasPrevious
	} else {
		page.HasPrevious = query.Cursor != ""
		if page.HasMore {
			raw, _ := json.Marshal(issueRecordCursor{Version: 1, Scope: hash, Value: lastValue, ID: page.Items[len(page.Items)-1].ID})
			page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
		}
	}
	if query.Cursors && len(page.Items) > 0 {
		page.StartCursor, page.EndCursor = encode(0), encode(len(page.Items)-1)
	}
	if !query.Cursors {
		page.HasPrevious = false
	}
	return page, nil
}

// issueRecordTotal answers a query's total from the maintained issue
// counters when the query is a plain scope count (a workspace or team list
// with no other predicate). The counters are transactionally maintained by
// every issue write, so this stays correct while avoiding a COUNT(*) over
// the whole workspace. Queries with any other row predicate return ok=false;
// the caller then runs countIssueRecordsSQL.
func (s *SQLiteStore) issueRecordTotal(ctx context.Context, query IssueRecordQuery) (int64, bool, error) {
	if !issueFilterEmpty(query.Filter) || query.Text != "" || query.SearchText != "" ||
		len(query.ProjectIDs) > 0 || len(query.StateIDs) > 0 || len(query.ReleaseIDs) > 0 ||
		len(query.IssueIDs) > 0 || query.RestrictToIssueIDs || query.Nested ||
		query.GroupBy != "" || query.GroupValue != nil || len(query.GroupValues) > 0 || len(query.GroupCases) > 0 {
		return 0, false, nil
	}
	access := query.Access
	var archivedTeams []string
	if access != nil {
		archivedTeams = access.ArchivedTeamIDs
	}
	if len(query.TeamIDs) == 0 {
		// A workspace-wide count must not include teams the viewer cannot see.
		if access != nil && !access.Admin {
			return 0, false, nil
		}
		if query.AllowedTeamIDs != nil {
			return 0, false, nil
		}
		total, ok, err := s.sumIssueCounters(ctx, query, "workspace", []string{""})
		if err != nil || !ok {
			return total, ok, err
		}
		// Deleted teams in their restore window are hidden from everyone,
		// admins included; the workspace counter still includes their issues.
		if len(archivedTeams) > 0 {
			hidden, ok, err := s.sumIssueCounters(ctx, query, "team", archivedTeams)
			if err != nil || !ok {
				return 0, false, err
			}
			total -= hidden
			if total < 0 {
				total = 0
			}
		}
		return total, true, nil
	}
	teams := slices.Clone(query.TeamIDs)
	if access != nil && !access.Admin {
		for _, team := range teams {
			if !slices.Contains(access.VisibleTeamIDs, team) {
				return 0, false, nil
			}
		}
	}
	if query.AllowedTeamIDs != nil {
		for _, team := range teams {
			if !slices.Contains(query.AllowedTeamIDs, team) {
				return 0, false, nil
			}
		}
	}
	// Archived teams are hidden from every viewer (issueRecordVisibilityClause).
	teams = slices.DeleteFunc(teams, func(team string) bool { return slices.Contains(archivedTeams, team) })
	if len(teams) == 0 {
		return 0, true, nil
	}
	return s.sumIssueCounters(ctx, query, "team", teams)
}

func (s *SQLiteStore) sumIssueCounters(ctx context.Context, query IssueRecordQuery, scopeType string, ids []string) (int64, bool, error) {
	clause, args := bindList("scope_id", ids)
	where := []any{query.Workspace, scopeType}
	statement := "SELECT COALESCE(SUM(total),0) FROM issue_scope_counts WHERE workspace_key=? AND scope_type=? AND " + clause
	args = append(where, args...)
	switch query.Archived {
	case "", "false":
		statement += " AND archived=0"
	case "true":
		statement += " AND archived=1"
	case "all":
	default:
		return 0, false, nil
	}
	var total int64
	if err := s.db.QueryRowContext(ctx, statement, args...).Scan(&total); err != nil {
		return 0, false, err
	}
	return total, true, nil
}

func issueFilterEmpty(filter IssueFilter) bool {
	return filter.Field == "" && len(filter.Values) == 0 && len(filter.And) == 0 && len(filter.Or) == 0 && filter.Not == nil
}

func (s *SQLiteStore) QueryIssueGroups(ctx context.Context, query IssueRecordQuery) ([]IssueRecordGroup, error) {
	query = query.withoutGroupValue()
	if err := s.applyIssueRecordScope(ctx, &query); err != nil {
		return nil, err
	}
	if _, _, err := issueRecordWhere(query); err != nil {
		return nil, err
	}
	// Both caches are keyed by the revision read before counting.
	revision, err := s.issueCountRevision(ctx, query.Workspace)
	if err != nil {
		return nil, err
	}
	cached, cacheKey, ok := s.cacheGetIssueGroups(ctx, query, revision)
	if ok {
		return cached, nil
	}
	key, now := issueGroupCountKey(query, revision), time.Now()
	if groups, ok := s.issueGroupCounts.get(key, now); ok {
		return groups, nil
	}
	counted := withoutRedundantIssueAccess(query)
	var groups []IssueRecordGroup
	if query.Nested {
		groups, err = s.nestedIssueGroups(ctx, counted)
	} else {
		groups, err = s.flatIssueGroups(ctx, counted)
	}
	if err == nil {
		s.issueGroupCounts.put(key, query.Workspace, groups, now)
		s.cacheSetIssueGroups(ctx, cacheKey, groups)
	}
	return groups, err
}

// withoutRedundantIssueAccess drops a viewer's team restriction from a query
// whose teams are all visible to the viewer (and none deleted): the teams
// filter already implies it, and without it the counts can come from the
// precomputed counters and skip the shared-issue scan.
func withoutRedundantIssueAccess(query IssueRecordQuery) IssueRecordQuery {
	access := query.Access
	if access == nil || access.Admin || len(query.TeamIDs) == 0 {
		return query
	}
	for _, id := range query.TeamIDs {
		if !slices.Contains(access.VisibleTeamIDs, id) || slices.Contains(access.ArchivedTeamIDs, id) {
			return query
		}
	}
	query.Access = nil
	return query
}

// flatIssueGroups counts the query's rows, each in its own group. Nested
// counts correct these, so for them the condition is the nested base (the
// indexed text search included).
func (s *SQLiteStore) flatIssueGroups(ctx context.Context, query IssueRecordQuery) ([]IssueRecordGroup, error) {
	query.Cursor = ""
	query = query.withoutGroupValue()
	search := query.Nested && query.SearchText != ""
	if !search {
		if groups, handled, err := s.issueGroupsFromStats(ctx, query); handled {
			return groups, err
		}
		if groups, handled, err := s.memberIssueGroupsFromStats(ctx, query); handled {
			return groups, err
		}
		// Team views the counters do not serve (by assignee or label, OR
		// and label terms, cases) count in memory (issue_forest_groups.go).
		if len(query.TeamIDs) > 0 && preferIssueForest(query) {
			if groups, handled, err := s.issueForestGroups(ctx, query, false); handled {
				return groups, err
			}
		}
	}
	return s.flatIssueGroupsBySQL(ctx, query)
}

// flatIssueGroupsBySQL is flatIssueGroups counted from the rows in SQL.
func (s *SQLiteStore) flatIssueGroupsBySQL(ctx context.Context, query IssueRecordQuery) ([]IssueRecordGroup, error) {
	query.Cursor = ""
	query = query.withoutGroupValue()
	search := query.Nested && query.SearchText != ""
	where, args, err := issueRecordWhere(query)
	if search {
		where, args, err = s.issueNestedBase(query)
	}
	if err != nil {
		return nil, err
	}
	prefix, prefixArgs := issueAccessCTE(query)
	if query.GroupBy == "cases" {
		return s.caseIssueGroups(ctx, query, where, args)
	}
	if query.GroupBy == "none" {
		var total int64
		if err := s.db.QueryRowContext(ctx, prefix+"SELECT COUNT(*) FROM issue_records i WHERE "+where, append(prefixArgs, args...)...).Scan(&total); err != nil {
			return nil, err
		}
		if total == 0 {
			return []IssueRecordGroup{}, nil
		}
		return []IssueRecordGroup{{Value: "all", Count: total}}, nil
	}
	if query.GroupBy == "customer" {
		return s.customerIssueGroups(ctx, query, where, args)
	}
	column, err := issueGroupColumn(query.GroupBy)
	from := "issue_records i"
	if attribute := issueGroupAttribute(query.GroupBy); attribute != "" {
		// The field comes from the fixed allowlist above, never from input SQL.
		from += " LEFT JOIN issue_attribute_records g ON g.workspace_key=i.workspace_key AND g.issue_id=i.id AND g.field='" + attribute + "'"
		column = "COALESCE(g.value,'')"
	} else if err != nil {
		return nil, err
	} else {
		column = "i." + column
	}
	statement := prefix + "SELECT " + column + ",COUNT(*) FROM " + from + " WHERE " + where + " GROUP BY " + column + " ORDER BY " + column
	if query.Nested {
		// Nested counts read the result again to move its sub-issues, so
		// the result is read through the index its condition picks, not
		// the grouping column's index (which serves the order but then
		// reads every row of the workspace).
		selection := "SELECT " + column + " AS k FROM " + from + " WHERE " + where
		if s.dialect == "mysql" {
			statement = prefix + "SELECT /*+ NO_MERGE(g) */ g.k,COUNT(*) FROM (" + selection + ") g GROUP BY g.k ORDER BY g.k"
		} else {
			statement = withIssueCTE(prefix, "nest_flat(k) AS MATERIALIZED ("+selection+")") + "SELECT g.k,COUNT(*) FROM nest_flat g GROUP BY g.k ORDER BY g.k"
		}
	}
	rows, err := s.db.QueryContext(ctx, statement, append(prefixArgs, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := []IssueRecordGroup{}
	for rows.Next() {
		var group IssueRecordGroup
		if err := rows.Scan(&group.Value, &group.Count); err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

// caseIssueGroups counts the rows of each case of a "cases" grouping, in the
// cases' order (empty cases are left out).
func (s *SQLiteStore) caseIssueGroups(ctx context.Context, query IssueRecordQuery, where string, args []any) ([]IssueRecordGroup, error) {
	expression, expressionArgs, err := issueGroupCaseExpression(query.GroupCases)
	if err != nil {
		return nil, err
	}
	prefix, prefixArgs := issueAccessCTE(query)
	values := append(append(append([]any{}, prefixArgs...), expressionArgs...), args...)
	rows, err := s.db.QueryContext(ctx, prefix+"SELECT grouped.case_index,COUNT(*) FROM (SELECT "+expression+" AS case_index FROM issue_records i WHERE "+where+") grouped GROUP BY grouped.case_index", values...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanIssueCaseGroups(rows, query.GroupCases)
}

// scanIssueCaseGroups reads (case index, count) rows as groups in the cases' order.
func scanIssueCaseGroups(rows *sql.Rows, cases []IssueGroupCase) ([]IssueRecordGroup, error) {
	counts := map[string]int64{}
	for rows.Next() {
		var index string
		var count int64
		if err := rows.Scan(&index, &count); err != nil {
			return nil, err
		}
		value, ok := issueGroupCaseValue(cases, index)
		if !ok {
			return nil, ErrIssueQuery
		}
		counts[value] += count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	groups := []IssueRecordGroup{}
	for _, item := range append(slices.Clone(cases), IssueGroupCase{}) {
		if count := counts[item.Value]; count > 0 {
			groups = append(groups, IssueRecordGroup{Value: item.Value, Count: count})
		}
	}
	return groups, nil
}

func (s *SQLiteStore) IssueRecord(ctx context.Context, workspace, id string) (domain.Issue, error) {
	if raw, ok := s.cacheGetIssue(ctx, workspace, id); ok {
		issue, err := unmarshalIssueRecord(raw)
		var version int64
		// Serve the cached copy only while it is the stored version.
		if err == nil {
			err = s.db.QueryRowContext(ctx, `SELECT version FROM issue_records WHERE workspace_key=? AND id=?`, workspace, issue.ID).Scan(&version)
		}
		if err == nil && version == issue.Version {
			items := []domain.Issue{issue}
			err = s.resolveIssueReferences(ctx, workspace, items)
			return items[0], err
		}
	}
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT data FROM issue_records WHERE workspace_key=? AND id=?`, workspace, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		err = s.db.QueryRowContext(ctx, `SELECT data FROM issue_records WHERE workspace_key=? AND identifier=?`, workspace, id).Scan(&raw)
	}
	if errors.Is(err, sql.ErrNoRows) {
		// An identifier the issue had before it moved to another team.
		err = s.db.QueryRowContext(ctx, `SELECT i.data FROM issue_identifier_aliases a JOIN issue_records i ON i.workspace_key=a.workspace_key AND i.id=a.issue_id WHERE a.workspace_key=? AND a.identifier=? LIMIT 1`, workspace, strings.ToUpper(id)).Scan(&raw)
	}
	if err != nil {
		return domain.Issue{}, err
	}
	s.cacheSetIssue(ctx, workspace, id, raw)
	issue, err := unmarshalIssueRecord(raw)
	if err == nil {
		items := []domain.Issue{issue}
		err = s.resolveIssueReferences(ctx, workspace, items)
		issue = items[0]
	}
	return issue, err
}
