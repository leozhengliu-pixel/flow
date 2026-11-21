package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
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
	hits            map[string][]string
	catalogFields   map[string]bool
}

type searchMetadataTimingKey struct{}

// WithSearchMetadataTiming observes request-local search phases without
// recording search terms, keys or result data.
func WithSearchMetadataTiming(ctx context.Context, observe func(string, time.Duration)) context.Context {
	return context.WithValue(ctx, searchMetadataTimingKey{}, observe)
}

func startSearchMetadataPhase(ctx context.Context, phase string) func() {
	if observe, ok := ctx.Value(searchMetadataTimingKey{}).(func(string, time.Duration)); ok {
		start := time.Now()
		return func() { observe(phase, time.Since(start)) }
	}
	return func() {}
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
	cache := s.newSearchMetadataCache(ctx, policy, q)
	// Release catalog snapshots before waiting on another cache fill. MySQL
	// retains the existing batched-hit snapshot.
	perKindSnapshot := s.dialect != "mysql" && cache.cache != nil
	var releaseSearch func()
	var searchCtx context.Context
	prepareSearch := func(runCtx context.Context) (context.Context, error) {
		if searchCtx != nil {
			return searchCtx, nil
		}
		var err error
		searchCtx, releaseSearch, err = s.beginMetadataSearch(runCtx, &q, !perKindSnapshot)
		return searchCtx, err
	}
	defer func() {
		if releaseSearch != nil {
			releaseSearch()
		}
	}()
	for _, kind := range searchMetadataKinds {
		if !q.Types[kind.kind] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return result, err
		}
		prepare := prepareSearch
		if perKindSnapshot && kind.kind != "project" && kind.kind != "document" {
			prepare = func(ctx context.Context) (context.Context, error) { return ctx, nil }
		}
		collection, err := cache.read(ctx, kind, prepare, func(searchCtx context.Context) (searchMetadataProjection, error) {
			return s.searchMetadataCollection(searchCtx, policy, q, allowed, linkScope, kind, limit)
		})
		if perKindSnapshot && releaseSearch != nil {
			releaseSearch()
			searchCtx, releaseSearch = nil, nil
		}
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
	records, column := "workspace_metadata_records", "data"
	textField := ""
	useCatalog := q.catalogFields[kind.field]
	if useCatalog {
		// The catalog supplies filters, sort keys and visibility fields. Only
		// the text of accepted shells needs the canonical record.
		records, column = "metadata_catalog", "summary"
		textField = "description"
		if kind.kind == "document" {
			textField = "content"
		}
	}
	source := records
	if useCatalog {
		source = "(SELECT workspace_key,field,record_key,summary AS data FROM metadata_catalog) matching_records"
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
			return result, nil
		}
		clause, values := bindList("record_key", ids)
		where += " AND " + clause
		args = append(args, values...)
	} else if q.hits != nil {
		ids := q.hits[kind.field]
		if len(ids) == 0 {
			return result, nil
		}
		clause, values := bindList("record_key", ids)
		where += " AND " + clause
		args = append(args, values...)
	} else {
		matches := []string{}
		var matchArgs []any
		for _, term := range q.Terms {
			selection, value := s.metadataTermSelection(term)
			matches = append(matches, selection)
			matchArgs = append(matchArgs, q.Scope.Workspace, kind.field, value)
		}
		selection := strings.Join(matches, " UNION ")
		if s.dialect == "mysql" {
			// Materialize the unique matched keys before sorting. MySQL's
			// semijoin plan for an IN subquery can discard full-text matches.
			source += " JOIN (SELECT DISTINCT record_key AS match_key FROM (" + selection + ") search_hits) hits ON " + s.metadataRecordKeyEquals("record_key", "hits.match_key")
			args = append(matchArgs, args...)
		} else {
			where += " AND record_key IN (" + selection + ")"
			args = append(args, matchArgs...)
		}
	}
	fields := append([]string{"id", "slugId", "createdAt", "updatedAt", "archivedAt"}, kind.fields...)
	if useCatalog {
		fields = slices.DeleteFunc(fields, func(field string) bool { return field == textField })
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
	archived, orderValue := s.jsonText("data", "archivedAt"), s.jsonText("data", sortField)
	pageSize := 64
	if s.dialect == "mysql" && s.BoundedMetadataEnabled() && kind.kind == "project" {
		teams := "JSON_EXTRACT(CONVERT(data USING utf8mb4),'$.teamIds')"
		if !useCatalog {
			// Share one parse when the canonical project is needed here.
			source += " JOIN JSON_TABLE(CONVERT(data USING utf8mb4),'$' COLUMNS (archived JSON PATH '$.archivedAt' ERROR ON ERROR, ordered JSON PATH '$." + sortField + "' ERROR ON ERROR, teams JSON PATH '$.teamIds' ERROR ON ERROR)) candidate ON TRUE"
			archived, orderValue = mysqlSearchJSONText("candidate.archived"), mysqlSearchJSONText("candidate.ordered")
			teams = "candidate.teams"
		}
		teamClause, teamArgs := mysqlSearchProjectTeams(teams, allowed, q.Scope.AllowedTeamIDs == nil)
		where += " AND " + teamClause
		args = append(args, teamArgs...)
		// The SQL team predicate covers project visibility. Avoid sorting the
		// full hit set again for each 64-row page of visible projects.
		pageSize = min(limit, 200)
	}
	if q.Scope.Archived != "all" && q.Scope.Archived != "true" {
		where += " AND " + archived + " IS NULL"
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
	orderExpr := "COALESCE(" + orderValue + ",'')"
	if q.Scope.Sort == "title" {
		orderExpr = "LOWER(" + orderExpr + ")"
	}
	lastValue, lastID := "", ""
	accepted := 0
	var acceptedKeys []string
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
		// columns for that page only; the planner would otherwise evaluate
		// every jsonText column for every matching row before sorting.
		page := "SELECT record_key," + orderExpr + " AS sort_value FROM " + source + " WHERE " + pageWhere + " ORDER BY " + orderExpr + " " + direction + ",record_key ASC LIMIT " + strconv.Itoa(pageSize)
		pageDone := startSearchMetadataPhase(ctx, "page_"+kind.kind)
		rows, err := s.readSearchMetadataPage(ctx, s.searchMetadataPageSQL(page, direction, records, column, fields, pageSize), append(pageArgs, q.Scope.Workspace, kind.field), len(fields))
		pageDone()
		if err != nil {
			return result, err
		}
		encodeDone := startSearchMetadataPhase(ctx, "shell_encode")
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
				encodeDone()
				return result, err
			}
			batch = append(batch, raw)
		}
		encodeDone()
		visibilityDone := startSearchMetadataPhase(ctx, "visibility")
		for i, raw := range batch {
			ok, err := s.appendSearchMetadata(ctx, &result, policy, q.Scope, allowed, linkScope, kind.kind, raw)
			if err != nil {
				visibilityDone()
				return result, err
			}
			if ok {
				accepted++
				if useCatalog {
					acceptedKeys = append(acceptedKeys, rows[i].Key)
				}
			}
			if accepted >= limit {
				break
			}
		}
		visibilityDone()
		if len(batch) < pageSize {
			break
		}
	}
	if len(acceptedKeys) > 0 {
		textDone := startSearchMetadataPhase(ctx, "text_"+kind.kind)
		text, err := s.searchMetadataText(ctx, q.Scope.Workspace, kind.field, textField, acceptedKeys)
		textDone()
		if err != nil {
			return result, err
		}
		for i, key := range acceptedKeys {
			if kind.kind == "document" {
				result.Documents[i].Content = text[key]
			} else {
				result.Projects[i].Description = text[key]
			}
		}
	}
	return result, nil
}

func (s *SQLiteStore) searchMetadataText(ctx context.Context, workspace, collection, field string, keys []string) (map[string]string, error) {
	clause, args := bindList("record_key", keys)
	rows, err := s.db.QueryContext(ctx, "SELECT record_key,SUBSTR("+s.jsonText("data", field)+",1,2048) FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND "+clause, append([]any{workspace, collection}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	text := make(map[string]string, len(keys))
	for rows.Next() {
		var key string
		var value sql.NullString
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		text[key] = value.String
	}
	return text, rows.Err()
}

func mysqlSearchJSONText(value string) string {
	return "NULLIF(JSON_UNQUOTE(" + value + "),'null')"
}

func mysqlSearchProjectTeams(teamIDs string, allowed map[string]bool, allowTeamless bool) (string, []any) {
	teams := make([]string, 0, len(allowed))
	for id := range allowed {
		teams = append(teams, id)
	}
	slices.Sort(teams)
	encoded, _ := json.Marshal(teams)
	// JSON string comparison is bytewise, like the Go authorization check.
	// Leave non-array legacy values to the original Go decoder.
	predicate := "JSON_TYPE(" + teamIDs + ")<>'ARRAY' OR "
	if !allowTeamless {
		predicate = "(JSON_TYPE(" + teamIDs + ") NOT IN ('ARRAY','NULL')) OR "
	}
	predicate += "JSON_OVERLAPS(" + teamIDs + ",CAST(? AS JSON))"
	if allowTeamless {
		predicate += " OR " + teamIDs + " IS NULL OR JSON_LENGTH(" + teamIDs + ")=0"
	}
	return "(" + predicate + ")", []any{string(encoded)}
}

func (s *SQLiteStore) searchMetadataPageSQL(page, direction, table, column string, fields []string, pageSize int) string {
	from := "(" + page + ") p JOIN " + table + " r ON r.workspace_key=? AND r.field=? AND " + s.metadataRecordKeyEquals("r.record_key", "p.record_key")
	column = "r." + column
	var nativeJSON string
	if s.BoundedMetadataEnabled() {
		switch s.dialect {
		case "mysql":
			nativeJSON = "CAST(CONVERT(" + column + " USING utf8mb4) AS JSON)"
		case "postgres":
			nativeJSON = "flow_utf8(" + column + ")::jsonb"
		}
	}
	if nativeJSON != "" {
		// The second LIMIT keeps the planner from merging the derived table
		// and parsing the original record once per output field.
		from = "(SELECT p.record_key,p.sort_value," + nativeJSON + " AS doc FROM " + from + " LIMIT " + strconv.Itoa(pageSize) + ") p"
	}
	values := make([]string, len(fields))
	for i, field := range fields {
		switch {
		case nativeJSON == "":
			values[i] = s.jsonText(column, field)
		case s.dialect == "mysql":
			values[i] = mysqlSearchJSONText("JSON_EXTRACT(p.doc,'$." + field + "')")
		case s.dialect == "postgres":
			values[i] = "(p.doc ->> '" + field + "')"
		}
		if field == "description" || field == "content" {
			values[i] = "SUBSTR(" + values[i] + ",1,2048)"
		}
	}
	return "SELECT p.record_key,p.sort_value," + strings.Join(values, ",") + " FROM " + from + " ORDER BY p.sort_value " + direction + ",p.record_key ASC"
}

const mysqlMetadataTermMatch = "MATCH(s.content) AGAINST (? IN BOOLEAN MODE)"

// Prepare the candidate sources lazily, so an all-cache-hit request never
// opens a transaction. The catalog proof and candidate and shell reads must
// use the same snapshot.
func (s *SQLiteStore) beginMetadataSearch(ctx context.Context, q *SearchMetadataQuery, allowBatch bool) (context.Context, func(), error) {
	if len(q.Terms) == 0 || !s.BoundedMetadataEnabled() {
		return ctx, nil, nil
	}
	batchHits := false
	if allowBatch && len(q.Terms) == 1 {
		runes := utf8.RuneCountInString(q.Terms[0])
		switch s.dialect {
		case "mysql":
			batchHits = runes >= 2
		case "postgres", "sqlite":
			batchHits = runes >= 3
		}
	}
	// TeamIDs also looks at the unprojected singular teamId field. Keep
	// that predicate on canonical JSON even for types that store teamIds.
	useCatalog := len(q.Scope.TeamIDs) == 0 &&
		(q.Types["project"] || q.Types["document"]) && recordJournalMode() == "on" && s.RecordJournalReady()
	if !batchHits && !useCatalog {
		return ctx, nil, nil
	}
	release := func() {}
	if existing, ok := ctx.Value(workspaceReadKey{}).(workspaceRead); !ok || existing.database != s.db {
		tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
		if err != nil {
			return ctx, nil, err
		}
		release = func() { _ = tx.Rollback() }
		ctx = context.WithValue(ctx, workspaceReadKey{}, workspaceRead{database: s.db, workspace: q.Scope.Workspace, reader: tx})
	}
	if useCatalog {
		var err error
		q.catalogFields, err = s.metadataSearchCatalogFields(ctx, q.Scope.Workspace)
		if err != nil {
			release()
			return ctx, nil, err
		}
	}
	if batchHits {
		const maxHits = 4096
		fields := make([]string, 0, len(searchMetadataKinds))
		for _, kind := range searchMetadataKinds {
			if q.Types[kind.kind] {
				fields = append(fields, kind.field)
			}
		}
		// A broad search falls back to per-kind queries with the full hit set.
		var err error
		q.hits, err = s.metadataSearchHits(ctx, q.Scope.Workspace, q.Terms[0], fields, maxHits)
		if err != nil {
			release()
			return ctx, nil, err
		}
	}
	return ctx, release, nil
}

func (s *SQLiteStore) metadataSearchCatalogFields(ctx context.Context, workspace string) (map[string]bool, error) {
	defer startSearchMetadataPhase(ctx, "catalog")()
	if err := checkRecordJournalCoverage(ctx, s.db, workspace); err != nil {
		if errors.Is(err, ErrRecordJournalCoverage) {
			return nil, nil
		}
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT field FROM metadata_catalog_collections WHERE workspace_key=? AND field IN ('projects','documents') AND phase=2 AND omissions=0`, workspace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	fields := make(map[string]bool, 2)
	for rows.Next() {
		var field string
		if err := rows.Scan(&field); err != nil {
			return nil, err
		}
		fields[field] = true
	}
	return fields, rows.Err()
}

func (s *SQLiteStore) metadataSearchHits(ctx context.Context, workspace, term string, fields []string, maxHits int) (map[string][]string, error) {
	defer startSearchMetadataPhase(ctx, "match")()
	// Let SQL classify each field using its collation, just as in the
	// per-kind lookup. Keys remain byte strings in the canonical table.
	clause, fieldsArgs := bindList("s.field", fields)
	fieldIndex := "CASE s.field"
	args := []any{}
	for i, field := range fields {
		fieldIndex += " WHEN ? THEN " + strconv.Itoa(i)
		args = append(args, field)
	}
	fieldIndex += " END"
	args = append(args, workspace)
	args = append(args, fieldsArgs...)
	source, match, value := s.metadataTermMatch(term)
	args = append(args, value, maxHits+1)
	rows, err := s.db.QueryContext(ctx, "SELECT "+fieldIndex+",s.record_key FROM "+source+" WHERE s.workspace_key=? AND "+clause+" AND "+match+" LIMIT ?", args...)
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
	source, match, value := s.metadataTermMatch(term)
	return "SELECT s.record_key FROM " + source + " WHERE s.workspace_key=? AND s.field=? AND " + match, value
}

func (s *SQLiteStore) metadataTermMatch(term string) (source, match, value string) {
	source = "metadata_search_documents s"
	value = "%" + escapeIssueLike(strings.ToLower(term)) + "%"
	match = "LOWER(s.content) LIKE ? ESCAPE '!'"
	if s.dialect == "sqlite" && utf8.RuneCountInString(term) >= 3 {
		value = "\"" + strings.ReplaceAll(term, "\"", "\"\"") + "\""
		source = "metadata_search_fts JOIN metadata_search_documents s ON metadata_search_fts.rowid=s.rowid"
		match = "metadata_search_fts MATCH ?"
	}
	if s.dialect == "mysql" && utf8.RuneCountInString(term) >= 2 {
		match = mysqlMetadataTermMatch
		value = mysqlBooleanPhrase(term)
	}
	if s.dialect == "postgres" {
		match = "s.content ILIKE ? ESCAPE '!'"
	}
	return source, match, value
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
