package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"flow/api/internal/domain"
)

type SearchMetadataQuery struct {
	Scope           IssueRecordQuery
	Types           map[string]bool
	Terms           []string
	Recent          []domain.RecentResource
	Limit           int
	CredentialID    string // id of a verified personal key, never its secret
	SkipSharedCache bool   // application-key projections are finished by the caller
	mysqlHits       map[string][]string
}

type searchMetadataKind struct {
	kind, field  string
	fields, text []string
}

var searchMetadataKinds = []searchMetadataKind{
	{"project", "projects", []string{"name", "summary", "description", "icon", "color", "status", "lead", "teamIds"}, []string{"name", "summary", "description"}},
	{"document", "documents", []string{"title", "content", "icon", "color", "creator", "teamIds", "projectIds", "permissions", "inheritedTeamIds"}, []string{"title", "content"}},
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
	linkScope := NewDocumentLinkScope(policy.Teams, policy.TeamSettings)
	limit := q.Limit
	if limit < 1 || limit > 500 {
		limit = 100
	}
	q.Limit = limit
	q.Terms = normalizeSearchTerms(q.Terms)
	mysqlSearch := s.dialect == "mysql" && s.BoundedMetadataEnabled() && len(q.Terms) == 1 && utf8.RuneCountInString(q.Terms[0]) >= 2
	var mysqlRelease func()
	var mysqlCtx context.Context
	ensureMySQLSearch := func(runCtx context.Context) (context.Context, error) {
		if !mysqlSearch {
			return runCtx, nil
		}
		if mysqlCtx != nil {
			return mysqlCtx, nil
		}
		var err error
		mysqlCtx, q.mysqlHits, mysqlRelease, err = s.beginMySQLMetadataSearch(runCtx, q)
		return mysqlCtx, err
	}
	defer func() {
		if mysqlRelease != nil {
			mysqlRelease()
		}
	}()
	cache := s.newSearchMetadataCache(ctx, policy, q)
	for _, kind := range searchMetadataKinds {
		if !q.Types[kind.kind] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return result, err
		}
		collection, err := cache.read(ctx, kind, ensureMySQLSearch, func(searchCtx context.Context) (searchMetadataProjection, error) {
			return s.searchMetadataCollection(searchCtx, policy, q, allowed, linkScope, kind, limit)
		})
		result.Projects = append(result.Projects, collection.Projects...)
		result.Documents = append(result.Documents, collection.Documents...)
		result.Initiatives = append(result.Initiatives, collection.Initiatives...)
		result.Users = append(result.Users, collection.Users...)
		result.Customers = append(result.Customers, collection.Customers...)
		result.Releases = append(result.Releases, collection.Releases...)
		result.SavedViews = append(result.SavedViews, collection.SavedViews...)
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

type searchMetadataProjection struct {
	Projects    []domain.Project    `json:",omitempty"`
	Documents   []domain.Document   `json:",omitempty"`
	Initiatives []domain.Initiative `json:",omitempty"`
	Users       []domain.User       `json:",omitempty"`
	Customers   []domain.Customer   `json:",omitempty"`
	Releases    []domain.Release    `json:",omitempty"`
	SavedViews  []domain.SavedView  `json:",omitempty"`
}

func (s *SQLiteStore) searchMetadataCollection(ctx context.Context, policy domain.Bootstrap, q SearchMetadataQuery, allowed map[string]bool, linkScope *DocumentLinkScope, kind searchMetadataKind, limit int) (searchMetadataProjection, error) {
	var result searchMetadataProjection
	where, args := "workspace_key=? AND field=?", []any{q.Scope.Workspace, kind.field}
	if len(q.Terms) == 0 {
		ids := []string{}
		for _, recent := range q.Recent {
			if recent.ResourceType == kind.kind {
				ids = append(ids, recent.ResourceID)
			}
		}
		if len(ids) == 0 {
			return result, nil
		}
		clause, values := bindList("record_key", ids)
		where += " AND " + clause
		args = append(args, values...)
	} else if q.mysqlHits != nil {
		ids := q.mysqlHits[kind.field]
		if len(ids) == 0 {
			return result, nil
		}
		clause, values := bindList("record_key", ids)
		where += " AND " + clause
		args = append(args, values...)
	} else {
		matches := []string{}
		for _, term := range q.Terms {
			selection, value := s.metadataTermSelection(term)
			matches = append(matches, selection)
			args = append(args, q.Scope.Workspace, kind.field, value)
		}
		// Filter visibility before pagination; private records must not consume
		// the raw search-hit cap and hide an authorized match.
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
	columns, shellJoin := s.searchMetadataShell(fields)
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
		if err := ctx.Err(); err != nil {
			return result, err
		}
		pageWhere, pageArgs := where, slices.Clone(args)
		if lastID != "" {
			op := "<"
			if direction == "ASC" {
				op = ">"
			}
			pageWhere += " AND (" + orderExpr + op + "? OR (" + orderExpr + "=? AND record_key>?))"
			pageArgs = append(pageArgs, lastValue, lastValue, lastID)
		}
		// Pick the page by key and sort value first, then compute the output
		// columns for those 64 rows only; the planner would otherwise evaluate
		// every jsonText column for every matching row before sorting.
		page := "SELECT record_key," + orderExpr + " AS sort_value FROM workspace_metadata_records WHERE " + pageWhere + " ORDER BY " + orderExpr + " " + direction + ",record_key ASC LIMIT 64"
		rows, err := s.readSearchMetadataPage(ctx, "SELECT p.record_key,p.sort_value,"+columns+" FROM ("+page+") p JOIN workspace_metadata_records r ON r.workspace_key=? AND r.field=? AND "+s.metadataRecordKeyEquals("r.record_key", "p.record_key")+shellJoin+" ORDER BY p.sort_value "+direction+",p.record_key ASC", append(pageArgs, q.Scope.Workspace, kind.field), len(fields))
		if err != nil {
			return result, err
		}
		batch := [][]byte{}
		for _, row := range rows {
			lastID, lastValue = row.Key, row.Sort
			values := row.Values
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
				return result, err
			}
			batch = append(batch, raw)
		}
		for _, raw := range batch {
			ok, err := s.appendSearchMetadata(ctx, &result, policy, q.Scope, allowed, linkScope, kind.kind, raw)
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
	return result, nil
}

// MySQL's JSON_TABLE parses the BLOB once per selected row. Keep the column
// values as JSON so arrays, objects and scalar quoting match JSON_EXTRACT.
func (s *SQLiteStore) searchMetadataShell(fields []string) (columns, join string) {
	values := make([]string, len(fields))
	var paths []string
	if s.dialect == "mysql" && s.BoundedMetadataEnabled() {
		paths = make([]string, len(fields))
	}
	for i, field := range fields {
		if paths != nil {
			column := "v" + strconv.Itoa(i)
			paths[i] = column + " JSON PATH '$." + field + "' ERROR ON ERROR"
			values[i] = "NULLIF(JSON_UNQUOTE(shell." + column + "),'null')"
		} else {
			values[i] = s.jsonText("r.data", field)
		}
		if field == "description" || field == "content" {
			values[i] = "SUBSTR(" + values[i] + ",1,2048)"
		}
	}
	if paths != nil {
		join = " LEFT JOIN JSON_TABLE(CONVERT(r.data USING utf8mb4), '$' COLUMNS (" + strings.Join(paths, ",") + ")) shell ON TRUE"
	}
	return strings.Join(values, ","), join
}

const mysqlMetadataTermMatch = "MATCH(s.content) AGAINST (? IN BOOLEAN MODE)"

// beginMySQLMetadataSearch shares one ngram lookup across kinds and keyset
// pages. A broad query falls back to the paged plan with no partial hit set.
// The read transaction keeps hit selection and shell reads together, and
// limits the number of staged hit sets to the size of the SQL pool.
func (s *SQLiteStore) beginMySQLMetadataSearch(ctx context.Context, q SearchMetadataQuery) (context.Context, map[string][]string, func(), error) {
	const maxHits = 4096
	fields := make([]string, 0, len(searchMetadataKinds))
	for _, kind := range searchMetadataKinds {
		if q.Types[kind.kind] {
			fields = append(fields, kind.field)
		}
	}
	release := func() {}
	if len(fields) == 0 {
		return ctx, nil, release, nil
	}
	base := ctx
	// This SQL-only snapshot is local to SearchMetadata. The request's
	// authorization context and policy remain the caller's live inputs.
	if existing, ok := ctx.Value(workspaceReadKey{}).(workspaceRead); !ok || existing.database != s.db {
		tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
		if err != nil {
			return ctx, nil, release, err
		}
		release = func() { _ = tx.Rollback() }
		ctx = context.WithValue(ctx, workspaceReadKey{}, workspaceRead{database: s.db, workspace: q.Scope.Workspace, reader: tx})
	}
	hits, err := s.mysqlMetadataSearchHits(ctx, q.Scope.Workspace, q.Terms[0], fields, maxHits)
	if err != nil || hits == nil {
		release()
		return base, nil, func() {}, err
	}
	return ctx, hits, release, nil
}

func (s *SQLiteStore) mysqlMetadataSearchHits(ctx context.Context, workspace, term string, fields []string, maxHits int) (map[string][]string, error) {
	// Let SQL classify each field using its collation, just as in the
	// per-kind lookup. Keys remain byte strings in the canonical table.
	clause, fieldsArgs := bindList("s.field", fields)
	fieldIndex := "CASE s.field"
	args := []any{}
	for i, field := range fields {
		fieldIndex += " WHEN ? THEN ?"
		args = append(args, field, i)
	}
	fieldIndex += " END"
	args = append(args, workspace)
	args = append(args, fieldsArgs...)
	args = append(args, mysqlBooleanPhrase(term), maxHits+1)
	rows, err := s.db.QueryContext(ctx, "SELECT "+fieldIndex+",s.record_key FROM metadata_search_documents s WHERE s.workspace_key=? AND "+clause+" AND "+mysqlMetadataTermMatch+" LIMIT ?", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hits := make(map[string][]string, len(fields))
	for count := 0; rows.Next(); count++ {
		if count == maxHits {
			return nil, nil
		}
		var field int
		var key string
		if err := rows.Scan(&field, &key); err != nil {
			return nil, err
		}
		hits[fields[field]] = append(hits[fields[field]], key)
	}
	return hits, rows.Err()
}

// metadataTermSelection selects the record keys whose indexed text contains
// term; its placeholders are workspace, field and the returned value.
func (s *SQLiteStore) metadataTermSelection(term string) (string, string) {
	value := "%" + escapeIssueLike(strings.ToLower(term)) + "%"
	selection := "SELECT s.record_key FROM metadata_search_documents s WHERE s.workspace_key=? AND s.field=? AND LOWER(s.content) LIKE ? ESCAPE '!'"
	if s.dialect == "sqlite" && utf8.RuneCountInString(term) >= 3 {
		value = "\"" + strings.ReplaceAll(term, "\"", "\"\"") + "\""
		selection = "SELECT s.record_key FROM metadata_search_fts JOIN metadata_search_documents s ON metadata_search_fts.rowid=s.rowid WHERE s.workspace_key=? AND s.field=? AND metadata_search_fts MATCH ?"
	}
	if s.dialect == "mysql" && utf8.RuneCountInString(term) >= 2 {
		selection = "SELECT s.record_key FROM metadata_search_documents s WHERE s.workspace_key=? AND s.field=? AND MATCH(s.content) AGAINST (? IN BOOLEAN MODE)"
		value = mysqlBooleanPhrase(term)
	}
	if s.dialect == "postgres" {
		selection = "SELECT s.record_key FROM metadata_search_documents s WHERE s.workspace_key=? AND s.field=? AND s.content ILIKE ? ESCAPE '!'"
	}
	return selection, value
}

// SearchDocumentIDs returns the ids of documents whose indexed title and body
// contain every term (matchAll) or any of them, at most limit. It reads only
// record keys from the full-text index; callers check access and rank.
func (s *SQLiteStore) SearchDocumentIDs(ctx context.Context, workspace string, terms []string, matchAll bool, limit int) ([]string, error) {
	terms = normalizeSearchTerms(terms)
	if len(terms) == 0 {
		return []string{}, nil
	}
	matches, args := []string{}, []any{workspace, "documents"}
	for _, term := range terms {
		if s.dialect == "mysql" && strings.ContainsFunc(term, notCJK) {
			// InnoDB's default stopwords ("at", "in", "on", "as"…) drop ngrams of most
			// English words, so the ngram index misses them; match those terms on this
			// workspace's document rows instead.
			matches = append(matches, "LOWER(content) LIKE ? ESCAPE '!'")
			args = append(args, "%"+escapeIssueLike(strings.ToLower(term))+"%")
			continue
		}
		selection, value := s.metadataTermSelection(term)
		matches = append(matches, "record_key IN ("+selection+")")
		args = append(args, workspace, "documents", value)
	}
	join := " OR "
	if matchAll {
		join = " AND "
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, "SELECT record_key FROM metadata_search_documents WHERE workspace_key=? AND field=? AND ("+strings.Join(matches, join)+") LIMIT ?", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func notCJK(r rune) bool {
	return !unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}

func searchMetadataJSONField(field, kind string) bool {
	return slices.Contains([]string{"teamIds", "projectIds", "issueIds", "contributingTeamIds", "inheritedTeamIds", "permissions", "domains", "creator", "lead", "owner", "active"}, field) || field == "status" && kind == "project"
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
	if field, ok := map[string]string{"teamid": "team", "state": "status", "stateid": "stateId", "statustype": "statusType", "assigneeid": "assigneeId", "creatorid": "creatorId", "createdat": "createdAt", "updatedat": "updatedAt"}[strings.ToLower(node.Field)]; ok {
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
	if node.Not != nil {
		clause, values, err := s.searchMetadataFilter(kind, *node.Not, depth+1)
		if err != nil {
			return "", nil, err
		}
		clauses = append(clauses, "NOT ("+clause+")")
		args = append(args, values...)
	}
	if node.Field != "" {
		if node.Field == "team" {
			if err := ValidateIssueFilter(IssueFilter{Field: "team", Operator: node.Operator, Values: node.Values}); err != nil {
				return "", nil, err
			}
			if kind != "project" && kind != "document" && kind != "view" {
				return "0=1", nil, nil
			}
			parts := []string{}
			for _, id := range node.Values {
				if kind == "view" {
					parts = append(parts, "COALESCE("+s.jsonText("data", "teamId")+",'')=?")
				} else {
					parts = append(parts, s.searchJSONArrayMember("data", "teamIds"))
				}
				args = append(args, id)
			}
			clause := "(" + strings.Join(parts, " OR ") + ")"
			if strings.EqualFold(node.Operator, "isNot") || strings.EqualFold(node.Operator, "notIn") {
				clause = "NOT (" + clause + ")"
			}
			clauses = append(clauses, clause)
			return "(" + strings.Join(clauses, " AND ") + ")", args, nil
		}
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

func (s *SQLiteStore) appendSearchMetadata(ctx context.Context, result *searchMetadataProjection, policy domain.Bootstrap, q IssueRecordQuery, allowed map[string]bool, linkScope *DocumentLinkScope, kind string, raw []byte) (bool, error) {
	if !json.Valid(raw) {
		return false, nil
	}
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
		// A workspace admin reads every document except those only in private
		// teams they have not joined.
		// A document attached to a private team's issue or project is scoped
		// to that team (see DocumentLinkScope).
		linkOpen := admin || linkScope.Open(item, func(id string) bool { return allowed[id] })
		readable := admin || q.Access.WorkspaceAdmin && linkOpen && (len(item.TeamIDs) == 0 || slices.ContainsFunc(item.TeamIDs, func(id string) bool { return slices.Contains(q.Access.VisibleTeamIDs, id) }))
		if !readable && item.Creator.ID != policy.Viewer.ID && !(len(item.Permissions) == 0 && (len(item.TeamIDs) == 0 && linkOpen || slices.ContainsFunc(item.TeamIDs, func(id string) bool { return memberTeams[id] })) || documentGrantAllows(&policy, item, memberTeams, linkOpen)) {
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

// searchMetadataPageRow is one row of a metadata search page: its record key,
// sort value and output columns.
type searchMetadataPageRow struct {
	Key, Sort string
	Values    []sql.NullString
}

func (s *SQLiteStore) readSearchMetadataPage(ctx context.Context, statement string, args []any, width int) ([]searchMetadataPageRow, error) {
	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []searchMetadataPageRow
	for rows.Next() {
		row := searchMetadataPageRow{Values: make([]sql.NullString, width)}
		dest := append(make([]any, 0, width+2), &row.Key, &row.Sort)
		for i := range row.Values {
			dest = append(dest, &row.Values[i])
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}
