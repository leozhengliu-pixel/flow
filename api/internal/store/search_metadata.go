package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strings"
	"unicode/utf8"

	"flow/api/internal/domain"
)

type SearchMetadataQuery struct {
	Scope  IssueRecordQuery
	Types  map[string]bool
	Terms  []string
	Recent []domain.RecentResource
	Limit  int
}

type searchMetadataKind struct {
	kind, field  string
	fields, text []string
}

var searchMetadataKinds = []searchMetadataKind{
	{"project", "projects", []string{"name", "summary", "description", "icon", "color", "status", "lead", "teamIds"}, []string{"name", "summary", "description"}},
	{"document", "documents", []string{"title", "content", "icon", "color", "creator", "teamIds", "projectIds", "permissions"}, []string{"title", "content"}},
	{"initiative", "initiatives", []string{"name", "summary", "description", "icon", "color", "status", "health", "creator", "owner", "leadTeamId", "contributingTeamIds", "projectIds"}, []string{"name", "summary", "description"}},
	{"member", "users", []string{"name", "displayName", "email", "active"}, []string{"name", "displayName", "email"}},
	{"customer", "customers", []string{"name", "domains", "ownerId", "status", "tier"}, []string{"name", "domains", "status", "tier"}},
	{"release", "releases", []string{"name", "version", "description", "status", "creator", "pipelineId", "projectIds", "issueIds"}, []string{"name", "version", "description", "status"}},
	{"view", "savedViews", []string{"name", "description", "icon", "color", "scope", "resource", "teamId", "projectId", "ownerId"}, []string{"name", "description", "scope", "resource"}},
}

// Search reads only matching, authorized shells in keyset batches. Large editor
// state, revisions, discussions and project resource collections never leave SQL.
func (s *SQLiteStore) SearchMetadata(ctx context.Context, policy domain.Bootstrap, q SearchMetadataQuery) (domain.Bootstrap, error) {
	result := domain.Bootstrap{Workspace: policy.Workspace, Viewer: policy.Viewer, ViewerRole: policy.ViewerRole}
	allowed := map[string]bool{}
	for _, team := range policy.Teams {
		if (q.Scope.Access == nil || q.Scope.Access.Admin || slices.Contains(q.Scope.Access.VisibleTeamIDs, team.ID)) && (q.Scope.AllowedTeamIDs == nil || slices.Contains(q.Scope.AllowedTeamIDs, team.ID)) {
			allowed[team.ID] = true
			result.Teams = append(result.Teams, team)
		}
	}
	limit := q.Limit
	if limit < 1 || limit > 500 {
		limit = 100
	}
	for _, kind := range searchMetadataKinds {
		if !q.Types[kind.kind] {
			continue
		}
		where, args := "workspace_key=? AND field=?", []any{q.Scope.Workspace, kind.field}
		if len(q.Terms) == 0 {
			ids := []string{}
			for _, recent := range q.Recent {
				if recent.ResourceType == kind.kind {
					ids = append(ids, recent.ResourceID)
				}
			}
			if len(ids) == 0 {
				continue
			}
			clause, values := bindList("record_key", ids)
			where += " AND " + clause
			args = append(args, values...)
		} else {
			matches := []string{}
			for _, term := range q.Terms {
				join, match, value := "", "LOWER(s.content) LIKE ? ESCAPE '!'", "%"+escapeIssueLike(strings.ToLower(term))+"%"
				if s.dialect == "sqlite" && utf8.RuneCountInString(term) >= 3 {
					join = " JOIN metadata_search_fts ON metadata_search_fts.rowid=s.rowid"
					match = "metadata_search_fts MATCH ?"
					value = "\"" + strings.ReplaceAll(term, "\"", "\"\"") + "\""
				}
				if s.dialect == "mysql" && utf8.RuneCountInString(term) >= 2 {
					match = "MATCH(s.content) AGAINST (? IN BOOLEAN MODE)"
					value = "\"" + strings.ReplaceAll(term, "\"", " ") + "\""
				}
				if s.dialect == "postgres" {
					match = "s.content ILIKE ? ESCAPE '!'"
				}
				matches = append(matches, "SELECT s.record_key FROM metadata_search_documents s"+join+" WHERE s.workspace_key=? AND s.field=? AND "+match)
				args = append(args, q.Scope.Workspace, kind.field, value)
			}
			where += " AND record_key IN (" + strings.Join(matches, " UNION ") + ")"
		}
		if q.Scope.Archived != "all" && q.Scope.Archived != "true" {
			where += " AND " + s.jsonText("data", "archivedAt") + " IS NULL"
		}
		if len(q.Scope.TeamIDs) > 0 {
			clauses := []string{}
			for _, id := range q.Scope.TeamIDs {
				for _, field := range []string{"teamIds", "teamId", "leadTeamId", "contributingTeamIds"} {
					if field == "teamId" || field == "leadTeamId" {
						clauses = append(clauses, s.jsonText("data", field)+"=?")
						args = append(args, id)
					} else {
						clauses = append(clauses, s.searchJSONArrayMember("data", field))
						args = append(args, id)
					}
				}
			}
			where += " AND (" + strings.Join(clauses, " OR ") + ")"
		}
		clause, values, err := s.searchMetadataFilter(kind.kind, q.Scope.Filter, 0)
		if err != nil {
			return result, err
		}
		where += " AND " + clause
		args = append(args, values...)
		fields := append([]string{"id", "slugId", "createdAt", "updatedAt", "archivedAt"}, kind.fields...)
		columns := make([]string, len(fields))
		for i, field := range fields {
			columns[i] = s.jsonText("data", field)
			if field == "description" || field == "content" {
				columns[i] = "SUBSTR(" + columns[i] + ",1,2048)"
			}
		}
		sortField, direction := "updatedAt", "DESC"
		if q.Scope.Sort == "createdAt" {
			sortField = "createdAt"
		}
		if q.Scope.Sort == "title" {
			sortField = "name"
			if kind.kind == "document" {
				sortField = "title"
			}
			if kind.kind == "member" {
				sortField = "displayName"
			}
			direction = "ASC"
		}
		if q.Scope.Direction == "asc" {
			direction = "ASC"
		}
		orderExpr := "COALESCE(" + s.jsonText("data", sortField) + ",'')"
		if q.Scope.Sort == "title" {
			orderExpr = "LOWER(" + orderExpr + ")"
		}
		lastValue, lastID := "", ""
		accepted := 0
		for accepted < limit {
			pageWhere, pageArgs := where, slices.Clone(args)
			if lastID != "" {
				op := "<"
				if direction == "ASC" {
					op = ">"
				}
				pageWhere += " AND (" + orderExpr + op + "? OR (" + orderExpr + "=? AND record_key>?))"
				pageArgs = append(pageArgs, lastValue, lastValue, lastID)
			}
			rows, err := s.db.QueryContext(ctx, "SELECT record_key,"+orderExpr+","+strings.Join(columns, ",")+" FROM workspace_metadata_records WHERE "+pageWhere+" ORDER BY "+orderExpr+" "+direction+",record_key ASC LIMIT 64", pageArgs...)
			if err != nil {
				return result, err
			}
			batch := [][]byte{}
			for rows.Next() {
				values := make([]sql.NullString, len(fields))
				dest := []any{&lastID, &lastValue}
				for i := range values {
					dest = append(dest, &values[i])
				}
				if err := rows.Scan(dest...); err != nil {
					rows.Close()
					return result, err
				}
				object := map[string]json.RawMessage{}
				for i, value := range values {
					if !value.Valid {
						continue
					}
					field := fields[i]
					if field == "active" && (value.String == "1" || value.String == "0") {
						if value.String == "1" {
							value.String = "true"
						} else {
							value.String = "false"
						}
					}
					if searchMetadataJSONField(field, kind.kind) {
						object[field] = json.RawMessage(value.String)
					} else {
						raw, _ := json.Marshal(value.String)
						object[field] = raw
					}
				}
				raw, err := json.Marshal(object)
				if err != nil {
					rows.Close()
					return result, err
				}
				batch = append(batch, raw)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return result, err
			}
			for _, raw := range batch {
				ok, err := s.appendSearchMetadata(ctx, &result, policy, q.Scope, allowed, kind.kind, raw)
				if err != nil {
					return result, err
				}
				if ok {
					accepted++
				}
				if accepted >= limit {
					break
				}
			}
			if len(batch) < 64 {
				break
			}
		}
	}
	return result, nil
}

func searchMetadataJSONField(field, kind string) bool {
	return slices.Contains([]string{"teamIds", "projectIds", "issueIds", "contributingTeamIds", "permissions", "domains", "creator", "lead", "owner", "active"}, field) || field == "status" && kind == "project"
}

func (s *SQLiteStore) searchJSONArrayMember(column, field string) string {
	switch s.dialect {
	case "mysql":
		return "JSON_CONTAINS(COALESCE(" + s.jsonText(column, field) + ",'[]'),JSON_QUOTE(?))"
	case "postgres":
		return "EXISTS(SELECT 1 FROM jsonb_array_elements_text(COALESCE((" + s.jsonText(column, field) + ")::jsonb,'[]'::jsonb)) team(value) WHERE team.value=?)"
	default:
		return "EXISTS(SELECT 1 FROM json_each(" + column + ",'$." + field + "') team WHERE team.value=?)"
	}
}

func (s *SQLiteStore) searchMetadataFilter(kind string, node IssueFilter, depth int) (string, []any, error) {
	if field, ok := map[string]string{"state": "status", "stateid": "stateId", "statustype": "statusType", "assigneeid": "assigneeId", "creatorid": "creatorId", "createdat": "createdAt", "updatedat": "updatedAt"}[strings.ToLower(node.Field)]; ok {
		node.Field = field
	}
	if depth > 8 {
		return "", nil, ErrIssueQuery
	}
	clauses, args := []string{}, []any{}
	for _, group := range []struct {
		nodes []IssueFilter
		join  string
	}{{node.And, " AND "}, {node.Or, " OR "}} {
		parts := []string{}
		for _, child := range group.nodes {
			clause, values, err := s.searchMetadataFilter(kind, child, depth+1)
			if err != nil {
				return "", nil, err
			}
			parts = append(parts, clause)
			args = append(args, values...)
		}
		if len(parts) > 0 {
			clauses = append(clauses, "("+strings.Join(parts, group.join)+")")
		}
	}
	if node.Field != "" {
		switch node.Field {
		case "status", "stateId", "statusType":
			if kind != "project" {
				return "0=1", nil, nil
			}
		case "assignee", "assigneeId":
			if kind != "project" && kind != "initiative" && kind != "customer" {
				return "0=1", nil, nil
			}
		case "creator", "creatorId":
			if kind != "document" && kind != "initiative" && kind != "release" {
				return "0=1", nil, nil
			}
		}
		for i, value := range node.Values {
			if value == "none" && slices.Contains([]string{"assignee", "assigneeId", "creator", "creatorId"}, node.Field) {
				node.Values = slices.Clone(node.Values)
				node.Values[i] = ""
			}
		}
		field := map[string]string{"id": "id", "title": "name", "createdAt": "createdAt", "updatedAt": "updatedAt", "creator": "creator.id", "creatorId": "creator.id", "assignee": "lead.id", "assigneeId": "lead.id", "status": "status.id", "stateId": "status.id", "statusType": "status.type"}[node.Field]
		if field == "" {
			return "0=1", nil, nil
		}
		if field == "lead.id" && kind == "initiative" {
			field = "owner.id"
		}
		if field == "lead.id" && kind == "customer" {
			field = "ownerId"
		}
		if field == "name" && kind == "document" {
			field = "title"
		}
		if field == "name" && kind == "member" {
			field = "displayName"
		}
		column := s.jsonText("data", field)
		dateField := field == "createdAt" || field == "updatedAt"
		if dateField {
			column = "NULLIF(NULLIF(" + column + ",''),'0001-01-01T00:00:00Z')"
		}
		op := strings.ToLower(node.Operator)
		if op == "" {
			op = "is"
		}
		switch op {
		case "is", "in", "isnot", "notin":
			clause, values := bindList("COALESCE("+column+",'')", node.Values)
			if op == "isnot" || op == "notin" {
				clause = "NOT (" + clause + ")"
			}
			clauses = append(clauses, clause)
			if dateField {
				clauses = append(clauses, column+" IS NOT NULL")
			}
			args = append(args, values...)
		case "before", "after", "gte", "lte", "gt", "lt":
			if len(node.Values) != 1 {
				return "", nil, ErrIssueQuery
			}
			operator := map[string]string{"before": "<", "after": ">", "gte": ">=", "lte": "<=", "gt": ">", "lt": "<"}[op]
			clauses = append(clauses, column+operator+"?")
			args = append(args, node.Values[0])
		case "isempty":
			clauses = append(clauses, "COALESCE("+column+",'')=''")
		case "isnotempty":
			clauses = append(clauses, "COALESCE("+column+",'')<>''")
		default:
			return "", nil, ErrIssueQuery
		}
	}
	if len(clauses) == 0 {
		return "1=1", nil, nil
	}
	return "(" + strings.Join(clauses, " AND ") + ")", args, nil
}

func (s *SQLiteStore) appendSearchMetadata(ctx context.Context, result *domain.Bootstrap, policy domain.Bootstrap, q IssueRecordQuery, allowed map[string]bool, kind string, raw []byte) (bool, error) {
	teamAllowed := func(ids []string) bool {
		return len(ids) == 0 && q.AllowedTeamIDs == nil || slices.ContainsFunc(ids, func(id string) bool { return allowed[id] })
	}
	projectAllowed := func(ids []string) (bool, error) {
		refs := []domain.Issue{}
		for _, id := range ids {
			refs = append(refs, domain.Issue{Project: &domain.ProjectSummary{ID: id}})
		}
		data, err := s.IssueReferenceMetadata(ctx, q, refs)
		return len(data.Projects) == len(ids), err
	}
	admin := q.Access == nil || q.Access.Admin
	switch kind {
	case "project":
		var item domain.Project
		if err := json.Unmarshal(raw, &item); err != nil {
			return false, err
		}
		if !teamAllowed(item.TeamIDs) {
			return false, nil
		}
		result.Projects = append(result.Projects, item)
	case "document":
		var item domain.Document
		if err := json.Unmarshal(raw, &item); err != nil {
			return false, err
		}
		memberTeams := map[string]bool{}
		for _, member := range policy.TeamMembers {
			if member.UserID == policy.Viewer.ID {
				memberTeams[member.TeamID] = true
			}
		}
		if !admin && item.Creator.ID != policy.Viewer.ID && !(len(item.Permissions) == 0 && (len(item.TeamIDs) == 0 || slices.ContainsFunc(item.TeamIDs, func(id string) bool { return memberTeams[id] })) || documentPermissionAllows(&policy, item, memberTeams)) {
			return false, nil
		}
		if q.AllowedTeamIDs != nil && len(item.TeamIDs) > 0 && !teamAllowed(item.TeamIDs) {
			return false, nil
		}
		result.Documents = append(result.Documents, item)
	case "initiative":
		if policy.ViewerRole == "guest" {
			return false, nil
		}
		var item domain.Initiative
		if err := json.Unmarshal(raw, &item); err != nil {
			return false, err
		}
		if item.LeadTeamID != "" && !allowed[item.LeadTeamID] {
			return false, nil
		}
		visible := item.LeadTeamID != "" || slices.ContainsFunc(item.ContributingTeamIDs, func(id string) bool { return allowed[id] })
		if !visible && len(item.ProjectIDs) > 0 {
			for _, id := range item.ProjectIDs {
				ok, err := projectAllowed([]string{id})
				if err != nil {
					return false, err
				}
				if ok {
					visible = true
					break
				}
			}
		}
		if !visible && (len(item.ContributingTeamIDs) > 0 || len(item.ProjectIDs) > 0) {
			return false, nil
		}
		result.Initiatives = append(result.Initiatives, item)
	case "member":
		var item domain.User
		if err := json.Unmarshal(raw, &item); err != nil {
			return false, err
		}
		if policy.ViewerRole == "guest" && item.ID != policy.Viewer.ID {
			teams := []string{}
			for id := range allowed {
				teams = append(teams, id)
			}
			where, args := bindList("team_id", teams)
			args = append([]any{policy.Workspace.ID, item.ID}, args...)
			var visible int
			if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM team_memberships WHERE workspace_id=? AND user_id=? AND "+where, args...).Scan(&visible); err != nil {
				return false, err
			}
			if visible == 0 {
				return false, nil
			}
		}
		result.Users = append(result.Users, item)
	case "customer":
		if policy.ViewerRole == "guest" {
			return false, nil
		}
		var item domain.Customer
		if err := json.Unmarshal(raw, &item); err != nil {
			return false, err
		}
		result.Customers = append(result.Customers, item)
	case "release":
		var item domain.Release
		if err := json.Unmarshal(raw, &item); err != nil {
			return false, err
		}
		ok, err := projectAllowed(item.ProjectIDs)
		if err != nil || !ok {
			return false, err
		}
		if item.PipelineID != "" {
			var value string
			err := s.db.QueryRowContext(ctx, "SELECT "+s.jsonText("data", "teamIds")+" FROM workspace_metadata_records WHERE workspace_key=? AND field='releasePipelines' AND record_key=?", q.Workspace, item.PipelineID).Scan(&value)
			if err == sql.ErrNoRows {
				return false, nil
			}
			if err != nil {
				return false, err
			}
			var ids []string
			if err = json.Unmarshal([]byte(value), &ids); err != nil {
				return false, err
			}
			if !teamAllowed(ids) {
				return false, nil
			}
		}
		visible, err := s.VisibleIssueRecordIDs(ctx, q, item.IssueIDs)
		if err != nil || len(visible) != len(item.IssueIDs) {
			return false, err
		}
		result.Releases = append(result.Releases, item)
	case "view":
		var item domain.SavedView
		if err := json.Unmarshal(raw, &item); err != nil {
			return false, err
		}
		if item.Scope == "team" && !allowed[item.TeamID] || item.Scope == "personal" && item.OwnerID != policy.Viewer.ID {
			return false, nil
		}
		if item.ProjectID != "" {
			ok, err := projectAllowed([]string{item.ProjectID})
			if err != nil || !ok {
				return false, err
			}
		}
		result.SavedViews = append(result.SavedViews, item)
	}
	return true, nil
}
