package store

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"slices"

	"flow/api/internal/domain"
)

func (s *SQLiteStore) beginIssueCandidateQueryWork(ctx context.Context, q IssueRecordQuery, terms, labels []string) (_ *ResourceRead, err error) {
	q.Text = ""
	owner := &ResourceRead{store: s}
	defer func() {
		if err != nil {
			owner.Close()
		}
	}()
	value := struct {
		Query         IssueRecordQuery
		Terms, Labels []string
	}{q, terms, labels}
	work, err := metadataAggregateNativeCost(ctx, reflect.ValueOf(value), 0)
	if err != nil {
		return nil, err
	}
	if work > (metadataWorkBytes-(128<<10))/8 {
		return nil, ErrMetadataWorkBudget
	}
	if err := owner.reserve(ctx, work*8+128<<10); err != nil {
		return nil, err
	}
	return owner, nil
}

type issueCandidateSQLRow struct {
	id                   string
	list                 []byte
	listBytes, dataBytes int64
	blob                 int
}

func (s *SQLiteStore) searchIssueCandidateRows(ctx context.Context, workspace, prefix, inner string, prefixCount int, params []any, visit func(domain.Issue) error) error {
	held := &ResourceRead{store: s}
	defer held.Close()
	wire := &ResourceRead{store: s}
	defer wire.Close()
	tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return err
	}
	defer tx.Rollback()
	from := " FROM (" + inner + ") candidates JOIN issue_records output ON output.id=candidates.id AND output.workspace_key=?"
	length := wire.byteLength
	var count, listBytes, maximumList, maximumID int64
	if err := tx.QueryRowContext(ctx, prefix+"SELECT COUNT(*),COALESCE(SUM("+length("output.list_data")+"),0),"+
		"COALESCE(MAX("+length("output.list_data")+"),0),COALESCE(MAX("+length("output.id")+"),0)"+from, params...).
		Scan(&count, &listBytes, &maximumList, &maximumID); err != nil {
		return err
	}
	if count < 0 || count > 500 || listBytes < 0 || maximumList < 0 || maximumID < 0 ||
		listBytes > metadataWorkBytes/3 || maximumID > metadataWorkBytes/16 {
		return ErrMetadataWorkBudget
	}
	cost := uint64(listBytes)*3 + uint64(count)*512 + 64<<10
	if cost > metadataWorkBytes || uint64(maximumID) > (metadataWorkBytes-cost)/uint64(max(count, 1))/8 {
		return ErrMetadataWorkBudget
	}
	cost += uint64(maximumID) * uint64(count) * 8
	if err := wire.reserve(ctx, cost); err != nil {
		return err
	}
	if err := held.reserve(ctx, uint64(count)*(uint64(reflect.TypeFor[domain.Issue]().Size())*4+512)+64<<10); err != nil {
		return err
	}
	storage := "0"
	if s.dialect == "sqlite" {
		storage = "CASE WHEN typeof(output.id)='blob' THEN 1 ELSE 0 END"
	}
	query := prefix + "SELECT CASE WHEN " + length("output.list_data") + "<=? THEN output.list_data ELSE NULL END," +
		"output.id,COALESCE(" + length("output.list_data") + ",0)," + length("output.data") + "," + storage + from
	// Prefix arguments (e.g. PostgreSQL's search CTE) precede this SELECT's
	// size guard. Count their placeholders before the inner query parameters.
	values := append(slices.Clone(params[:prefixCount]), maximumList)
	values = append(values, params[prefixCount:]...)
	rows, err := tx.QueryContext(ctx, query, values...)
	if err != nil {
		return err
	}
	var bodies []issueCandidateSQLRow
	var transferred int64
	for rows.Next() {
		var row issueCandidateSQLRow
		if err := rows.Scan(&row.list, &row.id, &row.listBytes, &row.dataBytes, &row.blob); err != nil {
			rows.Close()
			return err
		}
		transferred += row.listBytes
		if int64(len(bodies)) >= count || row.listBytes < 0 || row.listBytes > maximumList || transferred > listBytes ||
			int64(len(row.list)) != row.listBytes || int64(len(row.id)) > maximumID || row.dataBytes < 0 {
			rows.Close()
			return ErrConcurrentWrite
		}
		bodies = append(bodies, row)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	if int64(len(bodies)) != count || transferred != listBytes {
		return ErrConcurrentWrite
	}
	items := []domain.Issue{}
	seen := map[string]bool{}
	for _, row := range bodies {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := func() error {
			work := &ResourceRead{store: s}
			defer work.Close()
			raw := row.list
			if !json.Valid(raw) {
				if row.dataBytes > metadataWorkBytes/3 {
					return ErrMetadataWorkBudget
				}
				if err := work.reserve(ctx, uint64(row.dataBytes)*3+64<<10); err != nil {
					return err
				}
				id := issueAuthorizationLocator(s, row.id, row.blob)
				var actual int64
				if err := tx.QueryRowContext(ctx, "SELECT CASE WHEN "+length("data")+"<=? THEN data ELSE NULL END,"+length("data")+
					" FROM issue_records WHERE workspace_key=? AND id=?", row.dataBytes, workspace, id).Scan(&raw, &actual); err != nil {
					return err
				}
				if actual != row.dataBytes || int64(len(raw)) != actual {
					return ErrConcurrentWrite
				}
			}
			if !json.Valid(raw) {
				return nil
			}
			decode, err := metadataAggregatePayloadJSONWork(ctx, raw)
			if err != nil {
				return err
			}
			if err := work.reserve(ctx, decode+uint64(reflect.TypeFor[domain.Issue]().Size())*4+64<<10); err != nil {
				return err
			}
			var issue domain.Issue
			if json.Unmarshal(raw, &issue) != nil {
				return nil
			}
			normalizeIssueRecord(&issue)
			if issue.ID == "" || seen[issue.ID] {
				return nil
			}
			issue = issueListProjection(issue)
			native, err := metadataAggregateNativeCost(ctx, reflect.ValueOf(issue), 0)
			if err != nil || native > (metadataWorkBytes-512)/2 {
				if err == nil {
					err = ErrMetadataWorkBudget
				}
				return err
			}
			if err := held.reserve(ctx, native*2+512); err != nil {
				return err
			}
			seen[issue.ID] = true
			items = append(items, issue)
			return nil
		}()
		if err != nil {
			return err
		}
	}
	clear(bodies)
	bodies = nil
	wire.Close()
	if err := tx.Rollback(); err != nil {
		return err
	}
	if err := s.resolveIssueCandidateReferences(ctx, workspace, held, items); err != nil {
		return err
	}
	for _, issue := range items {
		if err := visit(issue); err != nil {
			return err
		}
	}
	return nil
}

var issueCandidateReferenceFields = func() map[reflect.Type][]string {
	fields := map[reflect.Type][]string{}
	for _, typ := range []reflect.Type{
		reflect.TypeFor[domain.Team](), reflect.TypeFor[domain.User](), reflect.TypeFor[domain.WorkflowState](),
		reflect.TypeFor[domain.IssueLabel](), reflect.TypeFor[domain.ProjectSummary](),
	} {
		fields[typ] = directoryOutputFields(typ)
	}
	return fields
}()

func (s *SQLiteStore) resolveIssueCandidateReferences(ctx context.Context, workspace string, held *ResourceRead, issues []domain.Issue) error {
	var ids []string
	seen := map[string]bool{}
	add := func(id string) error {
		if id == "" || seen[id] {
			return nil
		}
		if err := held.reserve(ctx, 256); err != nil {
			return err
		}
		seen[id], ids = true, append(ids, id)
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
	refs := newIssueReferences(domain.Bootstrap{})
	for start := 0; start < len(ids); start += 250 {
		if err := s.issueCandidateReferenceBatch(ctx, workspace, held, &refs, ids[start:min(start+250, len(ids))]); err != nil {
			return err
		}
	}
	for i, issue := range issues {
		issues[i] = refs.resolve(issue)
	}
	return nil
}

type issueCandidateReferenceRow struct {
	field, key string
	raw        []byte
	size       int64
	blob       int
}

func (s *SQLiteStore) issueCandidateReferenceBatch(ctx context.Context, workspace string, held *ResourceRead, refs *issueReferences, ids []string) error {
	tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return err
	}
	defer tx.Rollback()
	wire := &ResourceRead{store: s}
	defer wire.Close()
	length := wire.byteLength
	where, args := bindList("record_key", ids)
	where = " FROM workspace_metadata_records WHERE workspace_key=? AND field IN ('teams','states','users','labels','projects') AND " + where
	args = append([]any{workspace}, args...)
	var count, keyBytes, smallBytes int64
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*),COALESCE(SUM("+length("record_key")+"),0),"+
		"COALESCE(SUM(CASE WHEN "+length("data")+"<=? THEN "+length("data")+" ELSE 0 END),0)"+where,
		append([]any{taskIssueBatchBytes}, args...)...).Scan(&count, &keyBytes, &smallBytes); err != nil {
		return err
	}
	if count < 0 || count > 1250 || keyBytes < 0 || keyBytes > metadataWorkBytes/16 || smallBytes < 0 || smallBytes > metadataWorkBytes/3 {
		return ErrMetadataWorkBudget
	}
	if err := wire.reserve(ctx, uint64(count)*512+uint64(keyBytes)*16+uint64(smallBytes)*3+64<<10); err != nil {
		return err
	}
	storage := "0"
	if s.dialect == "sqlite" {
		storage = "CASE WHEN typeof(record_key)='blob' THEN 1 ELSE 0 END"
	}
	rows, err := tx.QueryContext(ctx, "SELECT field,record_key,CASE WHEN "+length("data")+"<=? THEN data ELSE NULL END,"+
		length("data")+","+storage+where, append([]any{taskIssueBatchBytes}, args...)...)
	if err != nil {
		return err
	}
	var values []issueCandidateReferenceRow
	var transferred, transferredKeys int64
	for rows.Next() {
		var row issueCandidateReferenceRow
		if err := rows.Scan(&row.field, &row.key, &row.raw, &row.size, &row.blob); err != nil {
			rows.Close()
			return err
		}
		transferredKeys += int64(len(row.key))
		transferred += int64(len(row.raw))
		if int64(len(values)) >= count || row.size < 0 || transferredKeys > keyBytes || transferred > smallBytes ||
			row.size <= taskIssueBatchBytes && int64(len(row.raw)) != row.size {
			rows.Close()
			return ErrConcurrentWrite
		}
		values = append(values, row)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	if int64(len(values)) != count || transferredKeys != keyBytes || transferred != smallBytes {
		return ErrConcurrentWrite
	}
	for _, row := range values {
		var target any
		switch row.field {
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
		if err := s.issueCandidateReferenceValue(ctx, tx, workspace, held, row, target); err != nil {
			return err
		}
		switch value := target.(type) {
		case *domain.Team:
			refs.teams[value.ID] = *value
		case *domain.WorkflowState:
			refs.states[value.ID] = *value
		case *domain.User:
			refs.users[value.ID] = *value
		case *domain.IssueLabel:
			refs.labels[value.ID] = *value
		case *domain.ProjectSummary:
			refs.projects[value.ID] = *value
		}
	}
	return nil
}

func (s *SQLiteStore) issueCandidateReferenceValue(ctx context.Context, tx *sqlTx, workspace string, held *ResourceRead, row issueCandidateReferenceRow, target any) error {
	if row.size <= taskIssueBatchBytes {
		decode, err := metadataAggregatePayloadJSONWork(ctx, row.raw)
		if err != nil {
			return err
		}
		_, release, err := s.tryAdmitAdditionalMetadataWork(ctx, int(decode)+64<<10)
		if err != nil {
			return err
		}
		defer release()
		if err := json.Unmarshal(row.raw, target); err != nil {
			return err
		}
		work, err := metadataAggregateNativeCost(ctx, reflect.ValueOf(target), 0)
		if err != nil {
			return err
		}
		return held.reserve(ctx, work+1024)
	}
	scratch := &ResourceRead{store: s}
	defer scratch.Close()
	if err := scratch.reserve(ctx, taskIssueChunkBytes*4+64<<10); err != nil {
		return err
	}
	spool, err := newMetadataLogicalRecordSpool(s.metadataLogicalScratchContext(ctx, workspace))
	if err != nil {
		return taskIssueSpoolError(err)
	}
	defer spool.Close()
	if !spool.CanAppend(row.size) {
		return ErrMetadataWorkBudget
	}
	payload := "data"
	if s.dialect == "sqlite" {
		payload = "CAST(data AS BLOB)"
	}
	for offset := int64(0); offset < row.size; {
		size := min(int64(taskIssueChunkBytes), row.size-offset)
		var chunk []byte
		if err := tx.QueryRowContext(ctx, "SELECT CASE WHEN "+scratch.byteLength("data")+"=? THEN SUBSTR("+payload+",?,?) ELSE NULL END"+
			" FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?", row.size, offset+1, size, workspace,
			row.field, issueAuthorizationLocator(s, row.key, row.blob)).Scan(&chunk); err != nil {
			return err
		}
		if int64(len(chunk)) != size {
			return io.ErrUnexpectedEOF
		}
		if _, err := spool.Write(chunk); err != nil {
			return taskIssueSpoolError(err)
		}
		offset += size
	}
	part := metadataCollectionRange{size: row.size}
	typ := reflect.TypeOf(target).Elem()
	parser := taskIssueJSON{ctx: ctx, input: spool, owner: held, fields: issueCandidateReferenceFields}
	if err := parser.validate(part, typ, 0); err != nil {
		return err
	}
	if err := held.reserve(ctx, uint64(typ.Size())*2+4096); err != nil {
		return err
	}
	return parser.project(part, reflect.ValueOf(target).Elem(), 0)
}
