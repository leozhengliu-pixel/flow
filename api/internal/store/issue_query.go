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
	And      []IssueFilter `json:"and,omitempty"`
	Or       []IssueFilter `json:"or,omitempty"`
	Field    string        `json:"field,omitempty"`
	Operator string        `json:"operator,omitempty"`
	Values   []string      `json:"values,omitempty"`
}

type IssueRecordQuery struct {
	Workspace      string
	Filter         IssueFilter
	TeamIDs        []string
	ProjectIDs     []string
	StateIDs       []string
	AllowedTeamIDs []string // nil is unrestricted; empty is denied.
	Access         *IssueRecordAccess
	Text           string
	Archived       string
	Sort           string
	Direction      string
	Cursor         string
	Limit          int
	GroupBy        string
	GroupValue     *string
	IncludeTotal   bool
}

type IssueRecordAccess struct {
	UserID         string
	WorkspaceID    string
	VisibleTeamIDs []string
	Admin          bool
}

func (s *SQLiteStore) PagedWorkspaceMetadata(ctx context.Context, workspace, userID string) (domain.Bootstrap, error) {
	data, ok := s.WorkspaceMetadata(workspace)
	if !ok {
		return data, ErrAuthForbidden
	}
	data, ok, err := s.projectBootstrapForUser(ctx, data, userID)
	if err != nil {
		return data, err
	}
	if !ok {
		return data, ErrAuthForbidden
	}
	data.IssueCollectionPaged = true
	return data, nil
}

func (s *SQLiteStore) IssueQueryAccess(ctx context.Context, workspace, userID string) (domain.Bootstrap, IssueRecordAccess, error) {
	data, ok := s.WorkspaceMetadata(workspace)
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
	data.TeamMembers, err = s.ListTeamMembers(ctx, data.Workspace.ID)
	if err != nil {
		return data, IssueRecordAccess{}, err
	}
	access := IssueRecordAccess{UserID: userID, WorkspaceID: data.Workspace.ID, Admin: isWorkspaceAdminRole(role), VisibleTeamIDs: []string{}}
	for _, team := range data.Teams {
		if teamVisibleToUser(data, team.ID, userID, role) {
			access.VisibleTeamIDs = append(access.VisibleTeamIDs, team.ID)
		}
	}
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
}

type IssueRecordGroup struct {
	Value string `json:"value"`
	Count int64  `json:"count"`
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

func compileIssueFilter(node IssueFilter, depth int, remaining *int) (string, []any, error) {
	aliases := map[string]string{"state": "status", "stateid": "status", "teamid": "team", "projectid": "project", "assigneeid": "assignee", "creatorid": "creator", "cycleid": "cycle", "label": "labels", "labelid": "labels", "subscriber": "subscribers", "subscriberid": "subscribers", "createdat": "createdAt", "updatedat": "updatedAt", "sortorder": "sortOrder", "myactivity": "myActivity", "statustype": "statusType"}
	if field, ok := aliases[strings.ToLower(node.Field)]; ok {
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
			sql, values, err := compileIssueFilter(child, depth+1, remaining)
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
	if node.Field != "" {
		if issueAttributeFields[node.Field] {
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

func escapeIssueLike(value string) string {
	return strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(value)
}

func issueRecordWhere(query IssueRecordQuery) (string, []any, error) {
	if query.Workspace == "" {
		return "", nil, ErrIssueQuery
	}
	clauses := []string{"i.workspace_key=?"}
	args := []any{query.Workspace}
	if query.Access != nil && !query.Access.Admin {
		if query.Access.UserID == "" {
			return "", nil, ErrIssueQuery
		}
		teams, values := bindList("i.team_id", query.Access.VisibleTeamIDs)
		clauses = append(clauses, "("+teams+" OR i.id IN (SELECT id FROM shared_issues))")
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
	}{{"team_id", query.TeamIDs, false}, {"project_id", query.ProjectIDs, false}, {"state_id", query.StateIDs, false}, {"team_id", query.AllowedTeamIDs, query.AllowedTeamIDs != nil}} {
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
	filter, values, err := compileIssueFilter(query.Filter, 0, &budget)
	if err != nil {
		return "", nil, err
	}
	clauses = append(clauses, filter)
	args = append(args, values...)
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

func (s *SQLiteStore) QueryIssueRecords(ctx context.Context, query IssueRecordQuery) (IssueRecordPage, error) {
	page := IssueRecordPage{Items: []domain.Issue{}, Total: -1}
	where, args, err := issueRecordWhere(query)
	if err != nil {
		return page, err
	}
	prefix, prefixArgs := issueAccessCTE(query)
	column := "sort_order"
	switch query.Sort {
	case "", "sortOrder":
	case "priority":
		column = "priority"
	case "createdAt":
		column = "created_at"
	case "updatedAt":
		column = "updated_at"
	case "title":
		column = "title"
	default:
		return page, ErrIssueQuery
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
	raw, _ := json.Marshal(scope)
	hash := fmt.Sprintf("%x", sha256.Sum256(raw))
	if query.IncludeTotal {
		if err := s.db.QueryRowContext(ctx, prefix+"SELECT COUNT(*) FROM issue_records i WHERE "+where, append(prefixArgs, args...)...).Scan(&page.Total); err != nil {
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
		if column == "priority" || column == "sort_order" {
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
		where += " AND (i." + column + operator + "? OR (i." + column + "=? AND i.id" + operator + "?))"
		args = append(args, value, value, cursor.ID)
	}
	rows, err := s.db.QueryContext(ctx, prefix+"SELECT i.data,i."+column+" FROM issue_records i WHERE "+where+" ORDER BY i."+column+" "+direction+",i.id "+direction+" LIMIT ?", append(append(prefixArgs, args...), limit+1)...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	lastValue := ""
	for rows.Next() {
		var raw []byte
		var sortValue string
		if err := rows.Scan(&raw, &sortValue); err != nil {
			return page, err
		}
		if len(page.Items) == limit {
			page.HasMore = true
			break
		}
		var issue domain.Issue
		if err := json.Unmarshal(raw, &issue); err != nil {
			return page, err
		}
		normalizeIssueRecord(&issue)
		page.Items = append(page.Items, issue)
		lastValue = sortValue
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if page.HasMore {
		raw, _ := json.Marshal(issueRecordCursor{Version: 1, Scope: hash, Value: lastValue, ID: page.Items[len(page.Items)-1].ID})
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return page, nil
}

func (s *SQLiteStore) QueryIssueGroups(ctx context.Context, query IssueRecordQuery) ([]IssueRecordGroup, error) {
	query.GroupValue = nil
	if _, _, err := issueRecordWhere(query); err != nil {
		return nil, err
	}
	if groups, handled, err := s.issueGroupsFromStats(ctx, query); handled {
		return groups, err
	}
	query.Cursor = ""
	query.GroupValue = nil
	if query.GroupBy == "none" {
		where, args, err := issueRecordWhere(query)
		if err != nil {
			return nil, err
		}
		prefix, prefixArgs := issueAccessCTE(query)
		var total int64
		if err := s.db.QueryRowContext(ctx, prefix+"SELECT COUNT(*) FROM issue_records i WHERE "+where, append(prefixArgs, args...)...).Scan(&total); err != nil {
			return nil, err
		}
		if total == 0 {
			return []IssueRecordGroup{}, nil
		}
		return []IssueRecordGroup{{Value: "all", Count: total}}, nil
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
	where, args, err := issueRecordWhere(query)
	if err != nil {
		return nil, err
	}
	prefix, prefixArgs := issueAccessCTE(query)
	rows, err := s.db.QueryContext(ctx, prefix+"SELECT "+column+",COUNT(*) FROM "+from+" WHERE "+where+" GROUP BY "+column+" ORDER BY "+column, append(prefixArgs, args...)...)
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

func (s *SQLiteStore) IssueRecord(ctx context.Context, workspace, id string) (domain.Issue, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT data FROM issue_records WHERE workspace_key=? AND id=?`, workspace, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		err = s.db.QueryRowContext(ctx, `SELECT data FROM issue_records WHERE workspace_key=? AND identifier=?`, workspace, id).Scan(&raw)
	}
	if err != nil {
		return domain.Issue{}, err
	}
	var issue domain.Issue
	err = json.Unmarshal(raw, &issue)
	normalizeIssueRecord(&issue)
	return issue, err
}
