package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"flow/api/internal/domain"
)

var issueAuthorizationFields = map[reflect.Type][]string{
	reflect.TypeFor[domain.Issue]():           {"ID", "Identifier", "PreviousIdentifiers", "Number", "Team", "Creator", "ParentID", "Permissions", "Relations", "ArchivedAt", "State", "Assignee", "Delegate", "Project", "Labels"},
	reflect.TypeFor[domain.Team]():            {"ID", "Key", "Private"},
	reflect.TypeFor[domain.User]():            {"ID"},
	reflect.TypeFor[domain.IssuePermission](): {"ID", "SubjectType", "SubjectID", "Role"},
	reflect.TypeFor[domain.IssueRelation]():   {"ID", "IssueID", "RelatedIssueID"},
	reflect.TypeFor[domain.Comment]():         {"ID", "User"},
	reflect.TypeFor[domain.WorkflowState]():   {"ID"},
	reflect.TypeFor[domain.ProjectSummary]():  {"ID"},
	reflect.TypeFor[domain.IssueLabel]():      {"ID"},
}

type issueAuthorizationRow struct {
	id, resource, sort    string
	size, descriptionSize int64
	idBlob, resourceBlob  int
}

func (r *IssueWriteAuthorizationRead) rowSnapshot(ctx context.Context) (context.Context, *sqlTx, func(), error) {
	s := r.owner.store
	if held, ok := ctx.Value(workspaceReadKey{}).(workspaceRead); ok && held.database == s.db && held.workspace == r.query.Workspace {
		if held.reader == nil {
			return ctx, nil, nil, ErrMetadataRecordQuery
		}
		return ctx, held.reader, func() {}, nil
	}
	tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return ctx, nil, nil, err
	}
	ctx = context.WithValue(ctx, workspaceReadKey{}, workspaceRead{database: s.db, workspace: r.query.Workspace, reader: tx})
	return ctx, tx, func() { _ = tx.Rollback() }, nil
}

func issueAuthorizationOrder(query IssueRecordQuery) (string, string, string, error) {
	column, expression := "sort_order", ""
	switch query.Sort {
	case "", "sortOrder":
	case "priority":
		column, expression = "priority", "(CASE WHEN i.priority=0 THEN 5 ELSE i.priority END)"
	case "createdAt":
		column = "created_at"
	case "updatedAt":
		column = "updated_at"
	case "title":
		column = "title"
	case "customerCount", "customerImportantCount", "customerRevenue":
		column, expression = "customer", customerSortExpression(query.Sort)
	default:
		return "", "", "", ErrIssueQuery
	}
	if expression == "" {
		expression = "i." + column
	}
	direction := "ASC"
	if query.Direction == "desc" {
		direction = "DESC"
	} else if query.Direction != "" && query.Direction != "asc" {
		return "", "", "", ErrIssueQuery
	}
	return column, expression, direction, nil
}

func (r *IssueWriteAuthorizationRead) loadIssueHeaders(ctx context.Context) error {
	query := r.query
	if err := r.applyReleaseScope(ctx, &query); err != nil {
		return err
	}
	ctx, tx, closeSnapshot, err := r.rowSnapshot(ctx)
	if err != nil {
		return err
	}
	defer closeSnapshot()
	_, expression, direction, err := issueAuthorizationOrder(query)
	if err != nil {
		return err
	}
	r.Data.Issues = []domain.Issue{}
	firstStart := len(r.Data.Issues)
	firstCount := 0
	for page := 0; page < 2; page++ {
		headers, err := r.issuePageRows(ctx, tx, query, expression, direction)
		if err != nil {
			return err
		}
		limit, bytesUsed := min(500, len(headers)), int64(0)
		if query.Summary {
			for index := 0; index < limit; index++ {
				if index > 0 && bytesUsed+headers[index].size+headers[index].descriptionSize > 4<<20 {
					limit = index
					break
				}
				bytesUsed += headers[index].size + headers[index].descriptionSize
			}
		}
		more := len(headers) > limit
		begin := len(r.Data.Issues)
		if err := r.issueRows(ctx, tx, headers[:limit], query.Summary); err != nil {
			return err
		}
		if err := r.refreshReferences(ctx, tx, r.Data.Issues[begin:]); err != nil {
			return err
		}
		if query.Reverse {
			slices.Reverse(r.Data.Issues[begin:])
		}
		if page == 0 {
			firstCount = len(r.Data.Issues) - firstStart
		}
		// Reverse's initial cursor is empty in the original helper, so its
		// first QueryIssueRecords result reports HasMore=false.
		if !more || limit == 0 || query.Reverse {
			break
		}
		last := r.Data.Issues[len(r.Data.Issues)-1]
		query.Cursor = ""
		// Keep cursor state internal, but use the same decoded JSON ID
		// (which need not equal the selected physical row's ID).
		query.Filter = r.query.Filter
		headersLast := headers[limit-1]
		headersLast.id = last.ID
		ctx = context.WithValue(ctx, issueAuthorizationCursorKey{}, headersLast)
	}
	if err := r.owner.reserve(ctx, uint64(firstCount)*128+4096); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, issue := range r.Data.Issues[firstStart : firstStart+firstCount] {
		seen[issue.ID] = true
		parent := issue.ParentID
		for parent != nil && *parent != "" && !seen[*parent] && len(seen) < 64 {
			ancestor, err := r.parentHeader(ctx, tx, *parent)
			if err != nil {
				if errors.Is(err, ErrMetadataWorkBudget) || errors.Is(err, ErrBoundedMetadataCoverage) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return err
				}
				break
			}
			seen[ancestor.ID] = true
			r.Data.Issues = append(r.Data.Issues, ancestor)
			parent = ancestor.ParentID
		}
	}
	return nil
}

type issueAuthorizationCursorKey struct{}

func (r *IssueWriteAuthorizationRead) applyReleaseScope(ctx context.Context, query *IssueRecordQuery) error {
	if len(query.ReleaseIDs) == 0 {
		return nil
	}
	if len(query.ReleaseIDs) > metadataWorkBytes/128 {
		return ErrMetadataWorkBudget
	}
	cost := uint64(len(query.ReleaseIDs))*128 + 4096
	for _, id := range query.ReleaseIDs {
		if cost > metadataWorkBytes || uint64(len(id)) > (metadataWorkBytes-cost)/16 {
			return ErrMetadataWorkBudget
		}
		cost += uint64(len(id)) * 16
	}
	if err := r.owner.reserve(ctx, cost); err != nil {
		return err
	}
	wanted, seen := map[string]bool{}, map[string]bool{}
	for _, id := range query.ReleaseIDs {
		if id != "" {
			wanted[id] = true
		}
	}
	ids := []string{}
	add := func(release domain.Release) error {
		if !wanted[release.ID] && !wanted[release.SlugID] {
			return nil
		}
		for _, id := range release.IssueIDs {
			if id == "" || seen[id] {
				continue
			}
			if err := r.owner.reserve(ctx, uint64(len(id))*16+256); err != nil {
				return err
			}
			owned := strings.Clone(id)
			seen[owned] = true
			ids = append(ids, owned)
		}
		return nil
	}
	if held, ok := ctx.Value(workspaceReadKey{}).(workspaceRead); ok && held.database == r.owner.store.db && held.workspace == query.Workspace {
		for _, release := range held.metadata.Releases {
			if err := add(release); err != nil {
				return err
			}
		}
	} else if len(wanted) != 0 {
		if err := r.owner.visitNative(ctx, "releases", func(_ int, node metadataLogicalNode) error {
			temporary := &ResourceRead{store: r.owner.store}
			defer temporary.Close()
			header := &DocumentDirectoryRead{owner: temporary}
			value, err := header.header(ctx, node, []string{"ID", "SlugID"})
			if err != nil {
				return err
			}
			release := value.Interface().(domain.Release)
			if !wanted[release.ID] && !wanted[release.SlugID] {
				return nil
			}
			value, err = header.header(ctx, node, []string{"IssueIDs"})
			if err != nil {
				return err
			}
			release.IssueIDs = value.Interface().(domain.Release).IssueIDs
			return add(release)
		}); err != nil {
			return err
		}
	}
	slices.Sort(ids)
	query.IssueIDs, query.RestrictToIssueIDs = ids, true
	return nil
}

func (r *IssueWriteAuthorizationRead) issuePageRows(ctx context.Context, tx *sqlTx, query IssueRecordQuery, expression, direction string) ([]issueAuthorizationRow, error) {
	s := r.owner.store
	prefix, prefixArgs, from, where, args, err := s.issueRecordSelection(ctx, query)
	if err != nil {
		return nil, err
	}
	if cursor, ok := ctx.Value(issueAuthorizationCursorKey{}).(issueAuthorizationRow); ok {
		if cursor.id == "" {
			return nil, ErrIssueQuery
		}
		var value any = cursor.sort
		column, _, _, _ := issueAuthorizationOrder(query)
		if column == "priority" || column == "sort_order" || column == "customer" {
			number, err := strconv.ParseFloat(cursor.sort, 64)
			if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
				return nil, ErrIssueQuery
			}
			value = number
		}
		operator := ">"
		if direction == "DESC" {
			operator = "<"
		}
		where += " AND (" + expression + operator + "? OR (" + expression + "=? AND i.id" + operator + "?))"
		args = append(args, value, value, cursor.id)
	}
	scanDirection := direction
	if query.Reverse {
		scanDirection = map[string]string{"ASC": "DESC", "DESC": "ASC"}[direction]
	}
	if s.dialect == "mysql" && from == "issue_records i" && len(query.TeamIDs) > 0 && query.Sort == "updatedAt" {
		from = "issue_records i FORCE INDEX (issue_records_team_updated_idx)"
	}
	payload, storage := "i.data", "0"
	if query.Summary {
		payload = "COALESCE(i.list_data,i.data)"
	}
	if s.dialect == "sqlite" {
		storage = "CASE WHEN typeof(i.id)='blob' THEN 1 ELSE 0 END"
	}
	order := " ORDER BY " + expression + " " + scanDirection + ",i.id " + scanDirection + " LIMIT 501"
	selected := " FROM " + from + " WHERE " + where + order
	values := append(slices.Clone(prefixArgs), args...)
	sortText := expression
	if s.dialect == "postgres" {
		sortText = "CAST(" + expression + " AS TEXT)"
	}
	sizeExpression := "COALESCE(" + r.owner.byteLength("i.id") + ",0)+COALESCE(" + r.owner.byteLength(sortText) + ",0)"
	var count, size int64
	if err := tx.QueryRowContext(ctx, prefix+"SELECT COUNT(*),COALESCE(SUM(header_bytes),0) FROM (SELECT "+sizeExpression+" AS header_bytes"+selected+") issue_authorization_headers", values...).Scan(&count, &size); err != nil {
		return nil, err
	}
	if count < 0 || count > 501 || size < 0 || size > metadataWorkBytes/16 || uint64(count)*512+uint64(size)*16 > metadataWorkBytes-(64<<10) {
		return nil, ErrMetadataWorkBudget
	}
	if err := r.owner.reserve(ctx, uint64(count)*512+uint64(size)*16+64<<10); err != nil {
		return nil, err
	}
	descriptionSize := "0"
	if query.Summary && query.IncludeDescription {
		descriptionSize = r.owner.byteLength("COALESCE(" + s.jsonText("i.data", "description") + ",'')")
	}
	rows, err := tx.QueryContext(ctx, prefix+"SELECT i.id,"+r.owner.byteLength(payload)+","+storage+","+expression+","+descriptionSize+selected, values...)
	if err != nil {
		return nil, err
	}
	headers := make([]issueAuthorizationRow, 0, count)
	for rows.Next() {
		var item issueAuthorizationRow
		var bodySize sql.NullInt64
		if err := rows.Scan(&item.id, &bodySize, &item.idBlob, &item.sort, &item.descriptionSize); err != nil {
			rows.Close()
			return nil, err
		}
		item.size = bodySize.Int64
		if int64(len(headers)) >= count || item.size < 0 || item.descriptionSize < 0 {
			rows.Close()
			return nil, ErrMetadataWorkBudget
		}
		headers = append(headers, item)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	return headers, nil
}

func issueAuthorizationLocator(s *SQLiteStore, value string, blob int) any {
	if s.dialect == "sqlite" && blob != 0 || s.dialect == "mysql" {
		return []byte(value)
	}
	return value
}

func (r *IssueWriteAuthorizationRead) issueRows(ctx context.Context, tx *sqlTx, headers []issueAuthorizationRow, summary bool) error {
	for start := 0; start < len(headers); {
		if headers[start].size > taskIssueBatchBytes {
			issue, err := r.issueRow(ctx, tx, headers[start], summary)
			if err != nil {
				return err
			}
			r.Data.Issues = append(r.Data.Issues, issue)
			start++
			continue
		}
		end, size := start, int64(0)
		for end < len(headers) && end-start < taskIssueBatchRows && headers[end].size <= taskIssueBatchBytes-size {
			size += headers[end].size
			end++
		}
		err := r.issueBatch(ctx, tx, headers[start:end], size, summary)
		if err != nil {
			return err
		}
		start = end
	}
	return nil
}

func (r *IssueWriteAuthorizationRead) issueBatch(ctx context.Context, tx *sqlTx, headers []issueAuthorizationRow, size int64, summary bool) error {
	_, release, err := r.owner.store.tryAdmitAdditionalMetadataWork(ctx, int(size)*3+len(headers)*512+64<<10)
	if err != nil {
		return err
	}
	defer release()
	payload := "data"
	if summary {
		payload = "COALESCE(list_data,data)"
	}
	queries, args := []string{}, []any{}
	for index, item := range headers {
		queries = append(queries, "SELECT "+strconv.Itoa(index)+" AS ordinal,CASE WHEN "+r.owner.byteLength(payload)+"<=? THEN "+payload+" ELSE NULL END AS data FROM issue_records WHERE workspace_key=? AND id=?")
		args = append(args, item.size, r.query.Workspace, issueAuthorizationLocator(r.owner.store, item.id, item.idBlob))
	}
	rows, err := tx.QueryContext(ctx, strings.Join(queries, " UNION ALL ")+" ORDER BY ordinal", args...)
	if err != nil {
		return err
	}
	bodies := make([][]byte, 0, len(headers))
	for rows.Next() {
		var ordinal int
		var body []byte
		if err := rows.Scan(&ordinal, &body); err != nil {
			rows.Close()
			return err
		}
		if ordinal != len(bodies) || ordinal >= len(headers) || int64(len(body)) != headers[ordinal].size {
			rows.Close()
			return io.ErrUnexpectedEOF
		}
		bodies = append(bodies, body)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	if len(bodies) != len(headers) {
		return io.ErrUnexpectedEOF
	}
	for _, body := range bodies {
		var issue domain.Issue
		if err := r.projectRow(ctx, bytes.NewReader(body), int64(len(body)), &issue); err != nil {
			return err
		}
		if issue.Relations == nil {
			issue.Relations = []domain.IssueRelation{}
		}
		r.Data.Issues = append(r.Data.Issues, issue)
	}
	return nil
}

func (r *IssueWriteAuthorizationRead) projectRow(ctx context.Context, input io.ReaderAt, size int64, target any) error {
	typ := reflect.TypeOf(target).Elem()
	part := metadataCollectionRange{size: size}
	parser := taskIssueJSON{ctx: ctx, input: input, owner: r.owner, fields: issueAuthorizationFields}
	if err := parser.validate(part, typ, 0); err != nil {
		return err
	}
	if err := r.owner.reserve(ctx, uint64(typ.Size())*2+4096); err != nil {
		return err
	}
	return parser.project(part, reflect.ValueOf(target).Elem(), 0)
}

func (r *IssueWriteAuthorizationRead) streamRow(ctx context.Context, tx *sqlTx, table, payload, where string, args []any, size int64, target any) error {
	_, release, err := r.owner.store.tryAdmitAdditionalMetadataWork(ctx, taskIssueChunkBytes*4+64<<10)
	if err != nil {
		return err
	}
	defer release()
	spool, err := newMetadataLogicalRecordSpool(metadataLogicalSourceScratchContext(ctx, r.owner.source))
	if err != nil {
		return taskIssueSpoolError(err)
	}
	defer spool.Close()
	if !spool.CanAppend(size) {
		return ErrMetadataWorkBudget
	}
	if r.owner.store.dialect == "sqlite" {
		payload = "CAST(" + payload + " AS BLOB)"
	}
	for offset := int64(0); offset < size; {
		length := min(int64(taskIssueChunkBytes), size-offset)
		values := append([]any{offset + 1, length}, args...)
		var chunk []byte
		if err := tx.QueryRowContext(ctx, "SELECT SUBSTR("+payload+",?,?) FROM "+table+" WHERE "+where, values...).Scan(&chunk); err != nil {
			return err
		}
		if int64(len(chunk)) != length {
			return io.ErrUnexpectedEOF
		}
		if _, err := spool.Write(chunk); err != nil {
			return taskIssueSpoolError(err)
		}
		offset += length
	}
	return r.projectRow(ctx, spool, size, target)
}

func (r *IssueWriteAuthorizationRead) issueRow(ctx context.Context, tx *sqlTx, header issueAuthorizationRow, summary bool) (domain.Issue, error) {
	var issue domain.Issue
	payload := "data"
	if summary {
		payload = "COALESCE(list_data,data)"
	}
	args := []any{r.query.Workspace, issueAuthorizationLocator(r.owner.store, header.id, header.idBlob)}
	if err := r.streamRow(ctx, tx, "issue_records", payload, "workspace_key=? AND id=?", args, header.size, &issue); err != nil {
		return issue, err
	}
	if issue.Relations == nil {
		issue.Relations = []domain.IssueRelation{}
	}
	return issue, nil
}

func (r *IssueWriteAuthorizationRead) parentHeader(ctx context.Context, tx *sqlTx, reference string) (domain.Issue, error) {
	for _, selection := range []struct {
		from, where string
		value       string
	}{
		{"issue_records i", "i.workspace_key=? AND i.id=?", reference},
		{"issue_records i", "i.workspace_key=? AND i.identifier=?", reference},
		{"issue_identifier_aliases a JOIN issue_records i ON i.workspace_key=a.workspace_key AND i.id=a.issue_id", "a.workspace_key=? AND a.identifier=?", strings.ToUpper(reference)},
	} {
		header, err := r.pointIssueRow(ctx, tx, selection.from, selection.where, selection.value)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return domain.Issue{}, err
		}
		issue, err := r.issueRow(ctx, tx, header, false)
		if err != nil {
			return issue, err
		}
		values := []domain.Issue{issue}
		if err := r.refreshReferences(ctx, tx, values); err != nil {
			return domain.Issue{}, err
		}
		return values[0], nil
	}
	return domain.Issue{}, sql.ErrNoRows
}

func (r *IssueWriteAuthorizationRead) pointIssueRow(ctx context.Context, tx *sqlTx, from, where, reference string) (issueAuthorizationRow, error) {
	var header issueAuthorizationRow
	storage := "0"
	if r.owner.store.dialect == "sqlite" {
		storage = "CASE WHEN typeof(i.id)='blob' THEN 1 ELSE 0 END"
	}
	args := []any{r.query.Workspace, reference}
	var size sql.NullInt64
	query := " FROM " + from + " WHERE " + where + " LIMIT 1"
	if err := tx.QueryRowContext(ctx, "SELECT "+r.owner.byteLength("i.id")+query, args...).Scan(&size); err != nil {
		return header, err
	}
	if !size.Valid || size.Int64 < 0 || size.Int64 > metadataWorkBytes/16 {
		return header, ErrMetadataWorkBudget
	}
	if err := r.owner.reserve(ctx, uint64(size.Int64)*16+4096); err != nil {
		return header, err
	}
	var bodySize sql.NullInt64
	if err := tx.QueryRowContext(ctx, "SELECT i.id,"+r.owner.byteLength("i.data")+","+storage+query, args...).Scan(&header.id, &bodySize, &header.idBlob); err != nil {
		return header, err
	}
	header.size = bodySize.Int64
	if header.size < 0 {
		return header, ErrMetadataRecordQuery
	}
	return header, nil
}

func (r *IssueWriteAuthorizationRead) loadCommentHeader(ctx context.Context, issueID, commentID string) error {
	if len(issueID) > metadataWorkBytes/32 || len(commentID) > metadataWorkBytes/32 || len(issueID)+len(commentID) > metadataWorkBytes/32 {
		return ErrMetadataWorkBudget
	}
	if err := r.owner.reserve(ctx, uint64(len(issueID)+len(commentID))*32+4096); err != nil {
		return err
	}
	ctx, tx, closeSnapshot, err := r.rowSnapshot(ctx)
	if err != nil {
		return err
	}
	defer closeSnapshot()
	args := []any{r.query.Workspace, issueID, commentID}
	where := "workspace_key=? AND kind='comment' AND resource_id=? AND id=?"
	var idSize, resourceSize int64
	err = tx.QueryRowContext(ctx, "SELECT "+r.owner.byteLength("id")+","+r.owner.byteLength("resource_id")+" FROM workspace_content_records WHERE "+where, args...).Scan(&idSize, &resourceSize)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if idSize < 0 || resourceSize < 0 || idSize > metadataWorkBytes/16 || resourceSize > metadataWorkBytes/16 || idSize+resourceSize > metadataWorkBytes/16 {
		return ErrMetadataWorkBudget
	}
	if err := r.owner.reserve(ctx, uint64(idSize+resourceSize)*16+4096); err != nil {
		return err
	}
	storageID, storageResource := "0", "0"
	if r.owner.store.dialect == "sqlite" {
		storageID, storageResource = "CASE WHEN typeof(id)='blob' THEN 1 ELSE 0 END", "CASE WHEN typeof(resource_id)='blob' THEN 1 ELSE 0 END"
	}
	var header issueAuthorizationRow
	var bodySize sql.NullInt64
	if err := tx.QueryRowContext(ctx, "SELECT id,resource_id,"+r.owner.byteLength("data")+","+storageID+","+storageResource+" FROM workspace_content_records WHERE "+where, args...).Scan(&header.id, &header.resource, &bodySize, &header.idBlob, &header.resourceBlob); err != nil {
		return err
	}
	header.size = bodySize.Int64
	if header.size < 0 {
		return ErrMetadataRecordQuery
	}
	args = []any{r.query.Workspace, issueAuthorizationLocator(r.owner.store, header.resource, header.resourceBlob), issueAuthorizationLocator(r.owner.store, header.id, header.idBlob)}
	var comment domain.Comment
	if err := r.streamRow(ctx, tx, "workspace_content_records", "data", where, args, header.size, &comment); err != nil {
		return err
	}
	if err := r.owner.reserve(ctx, uint64(len(issueID))*16+1024); err != nil {
		return err
	}
	if r.Data.Comments == nil {
		r.Data.Comments = map[string][]domain.Comment{}
	}
	r.Data.Comments[strings.Clone(issueID)] = []domain.Comment{comment}
	return nil
}

type issueAuthorizationReference struct {
	field string
	row   issueAuthorizationRow
}

// Match the legacy reference query's ID collection, 250-ID boundaries and
// physical-key-to-JSON-own-ID maps. Only Team and Creator fields survive.
func (r *IssueWriteAuthorizationRead) refreshReferences(ctx context.Context, tx *sqlTx, issues []domain.Issue) error {
	if len(issues) == 0 {
		return nil
	}
	ids := []string{}
	seen := map[string]bool{}
	add := func(id string) error {
		if id == "" || seen[id] {
			return nil
		}
		if err := r.owner.reserve(ctx, 192); err != nil {
			return err
		}
		ids = append(ids, id)
		seen[id] = true
		return nil
	}
	for _, issue := range issues {
		for _, id := range []string{issue.Team.ID, issue.State.ID, issue.Creator.ID} {
			if err := add(id); err != nil {
				return err
			}
		}
		for _, user := range []*domain.User{issue.Assignee, issue.Delegate} {
			if user != nil {
				if err := add(user.ID); err != nil {
					return err
				}
			}
		}
		if issue.Project != nil {
			if err := add(issue.Project.ID); err != nil {
				return err
			}
		}
		for _, label := range issue.Labels {
			if err := add(label.ID); err != nil {
				return err
			}
		}
	}
	teams, users := map[string]domain.Team{}, map[string]domain.User{}
	for start := 0; start < len(ids); start += 250 {
		where, values := bindList("record_key", ids[start:min(start+250, len(ids))])
		where = "workspace_key=? AND field IN ('teams','states','users','labels','projects') AND " + where
		values = append([]any{r.query.Workspace}, values...)
		var count, size int64
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*),COALESCE(SUM("+r.owner.byteLength("record_key")+"),0) FROM workspace_metadata_records WHERE "+where, values...).Scan(&count, &size); err != nil {
			return err
		}
		if count < 0 || size < 0 || count > metadataWorkBytes/512 || size > metadataWorkBytes/16 || uint64(count)*512+uint64(size)*16 > metadataWorkBytes-(64<<10) {
			return ErrMetadataWorkBudget
		}
		if err := r.owner.reserve(ctx, uint64(count)*512+uint64(size)*16+64<<10); err != nil {
			return err
		}
		storage := "0"
		if r.owner.store.dialect == "sqlite" {
			storage = "CASE WHEN typeof(record_key)='blob' THEN 1 ELSE 0 END"
		}
		rows, err := tx.QueryContext(ctx, "SELECT field,record_key,"+r.owner.byteLength("data")+","+storage+" FROM workspace_metadata_records WHERE "+where, values...)
		if err != nil {
			return err
		}
		headers := make([]issueAuthorizationReference, 0, count)
		for rows.Next() {
			var item issueAuthorizationReference
			var bodySize sql.NullInt64
			if err := rows.Scan(&item.field, &item.row.id, &bodySize, &item.row.idBlob); err != nil {
				rows.Close()
				return err
			}
			item.row.size = bodySize.Int64
			if int64(len(headers)) >= count || item.row.size < 0 {
				rows.Close()
				return ErrMetadataWorkBudget
			}
			headers = append(headers, item)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		for _, header := range headers {
			var target any
			switch header.field {
			case "teams":
				target = &domain.Team{}
			case "states":
				target = &domain.WorkflowState{}
			case "users":
				target = &domain.User{}
			case "labels":
				target = &domain.IssueLabel{}
			case "projects":
				target = &domain.ProjectSummary{}
			default:
				continue
			}
			args := []any{r.query.Workspace, header.field, issueAuthorizationLocator(r.owner.store, header.row.id, header.row.idBlob)}
			if err := r.referenceRow(ctx, tx, args, header.row.size, target); err != nil {
				return err
			}
			switch value := target.(type) {
			case *domain.Team:
				teams[value.ID] = *value
			case *domain.User:
				users[value.ID] = *value
			}
		}
	}
	for index := range issues {
		issue := &issues[index]
		if team, ok := teams[issue.Team.ID]; ok {
			issue.Team = team
		}
		if user, ok := users[issue.Creator.ID]; ok {
			issue.Creator = user
		}
		issue.State, issue.Assignee, issue.Delegate, issue.Project, issue.Labels = domain.WorkflowState{}, nil, nil, nil, nil
	}
	return nil
}

func (r *IssueWriteAuthorizationRead) referenceRow(ctx context.Context, tx *sqlTx, args []any, size int64, target any) error {
	const where = "workspace_key=? AND field=? AND record_key=?"
	if size > taskIssueBatchBytes {
		return r.streamRow(ctx, tx, "workspace_metadata_records", "data", where, args, size, target)
	}
	if size < 0 {
		return ErrMetadataRecordQuery
	}
	_, release, err := r.owner.store.tryAdmitAdditionalMetadataWork(ctx, int(size)*3+64<<10)
	if err != nil {
		return err
	}
	defer release()
	values := append([]any{size}, args...)
	var raw []byte
	if err := tx.QueryRowContext(ctx, "SELECT CASE WHEN "+r.owner.byteLength("data")+"<=? THEN data ELSE NULL END FROM workspace_metadata_records WHERE "+where, values...).Scan(&raw); err != nil {
		return err
	}
	if int64(len(raw)) != size {
		return io.ErrUnexpectedEOF
	}
	return r.projectRow(ctx, bytes.NewReader(raw), size, target)
}
