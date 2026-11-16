package store

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"unicode/utf8"
)

// These are the literal writeMetadataRecordRow and
// syncMetadataSearchDocument statements. The boundary differential compares
// their actual charges; staged SELECT expressions do not change this bill.
const metadataSQLStageLegacyRowSQL = `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,?,?,?,?) ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET collection_order=excluded.collection_order,data=excluded.data`
const metadataSQLStageLegacyRootSQL = `UPDATE workspace_states SET workspace_id=?,data=?,updated_at=? WHERE workspace_key=?`

func metadataSQLStageLegacySearchSQL(dialect string) string {
	if dialect == "mysql" {
		return `INSERT INTO metadata_search_documents(workspace_key,field,record_key,content) VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE content=IF(content<>VALUES(content),VALUES(content),content)`
	}
	return `INSERT INTO metadata_search_documents(workspace_key,field,record_key,content) VALUES(?,?,?,?) ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET content=excluded.content WHERE metadata_search_documents.content<>excluded.content`
}

type metadataSQLStageTarget struct {
	workspace, field, key  string
	workspaceID, updatedAt string
	workspaceState         bool
	order                  int
	dataBytes              int
	// A negative value means this record has no metadata search hook.
	searchBytes int
}

func (t metadataSQLStageTarget) valid() bool {
	if t.workspace == "" || t.dataBytes < 0 || t.searchBytes < -1 {
		return false
	}
	if t.workspaceState {
		return t.workspaceID != "" && t.updatedAt != "" && t.field == "" &&
			t.key == "" && t.order == 0 && t.searchBytes == -1
	}
	if t.key == "" || t.workspaceID != "" || t.updatedAt != "" {
		return false
	}
	switch t.field {
	case "projects":
		return t.searchBytes >= 0
	case "agentSessions", "projectUpdates":
		return t.searchBytes == -1
	}
	return false
}

func (tx *sqlTx) admitStatementWriteBytes(bytes int) error {
	if tx.writeErr != nil {
		return tx.writeErr
	}
	if bytes < 0 || tx.maxTransactionBytes > 0 && bytes > tx.maxTransactionBytes-tx.writtenBytes {
		tx.writeErr = fmt.Errorf("%w: maximum %d bytes; split the import into smaller batches", ErrTransactionWriteBudget, tx.maxTransactionBytes)
		return tx.writeErr
	}
	tx.writtenBytes += bytes
	return nil
}

func (s *metadataSQLWriteStage) open() bool {
	return s != nil && !s.closed && s.tx != nil && s.tx.metadataSQLStageOwners[s.id] == s &&
		(!s.admitted || s.target == s.admittedTarget)
}

func (s *metadataSQLWriteStage) admitTarget() error {
	if !s.open() || !s.target.valid() {
		return ErrBoundedMetadataCoverage
	}
	if s.target.workspaceState {
		size := len(metadataSQLStageLegacyRootSQL) + len(s.target.workspaceID) +
			s.target.dataBytes + len(s.target.updatedAt) + len(s.target.workspace)
		if err := s.tx.admitStatementWriteBytes(size); err != nil {
			return err
		}
		s.admittedTarget, s.admitted = s.target, true
		return nil
	}
	// Exactly the original argument costs: strings/payload use byte length,
	// collection_order uses ExecContext's ordinary scalar charge of 16.
	row := len(metadataSQLStageLegacyRowSQL) + len(s.target.workspace) +
		len(s.target.field) + len(s.target.key) + 16 + s.target.dataBytes
	if err := s.tx.admitStatementWriteBytes(row); err != nil {
		return err
	}
	if s.target.searchBytes >= 0 {
		search := len(metadataSQLStageLegacySearchSQL(s.tx.dialect)) +
			len(s.target.workspace) + len(s.target.field) + len(s.target.key) + s.target.searchBytes
		if err := s.tx.admitStatementWriteBytes(search); err != nil {
			return err
		}
	}
	s.admittedTarget, s.admitted = s.target, true
	return nil
}

type metadataSQLStageOperation uint8

const (
	metadataSQLStageInsert metadataSQLStageOperation = iota + 1
	metadataSQLStageAppendData
	metadataSQLStageAppendSearch
	metadataSQLStageDelete
	metadataSQLStageWriteRow
	metadataSQLStageWriteSearch
	metadataSQLStageWriteRoot
)

type metadataSQLStageStatementKey struct{}

type metadataSQLStageStatement struct {
	stage     *metadataSQLWriteStage
	operation metadataSQLStageOperation
}

func (s *metadataSQLWriteStage) statementQuery(operation metadataSQLStageOperation) (string, error) {
	switch operation {
	case metadataSQLStageInsert:
		return "INSERT INTO bounded_metadata_write_stage(operation_id,data,content) VALUES(?,'','')", nil
	case metadataSQLStageAppendData, metadataSQLStageAppendSearch:
		column := "data"
		if operation == metadataSQLStageAppendSearch {
			column = "content"
		}
		expression := column + " || ?"
		if s.tx.dialect == "mysql" {
			// Interpret the original UTF-8 parameter bytes explicitly;
			// the connection/database defaults must not transcode them.
			expression = "CONCAT(" + column + ",CONVERT(CAST(? AS BINARY) USING utf8mb4))"
		}
		return "UPDATE bounded_metadata_write_stage SET " + column + "=" + expression + " WHERE operation_id=?", nil
	case metadataSQLStageDelete:
		return "DELETE FROM bounded_metadata_write_stage WHERE operation_id=?", nil
	case metadataSQLStageWriteRow:
		value, err := s.valueSQL("data", true)
		if err != nil {
			return "", err
		}
		return "INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,?,?,?," +
			value + ") ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET collection_order=excluded.collection_order,data=excluded.data", nil
	case metadataSQLStageWriteSearch:
		value, err := s.valueSQL("content", false)
		if err != nil {
			return "", err
		}
		tail := " ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET content=excluded.content WHERE metadata_search_documents.content<>excluded.content"
		if s.tx.dialect == "mysql" {
			tail = " ON DUPLICATE KEY UPDATE content=IF(content<>VALUES(content),VALUES(content),content)"
		}
		return "INSERT INTO metadata_search_documents(workspace_key,field,record_key,content) VALUES(?,?,?," + value + ")" + tail, nil
	case metadataSQLStageWriteRoot:
		value, err := s.valueSQL("data", true)
		if err != nil {
			return "", err
		}
		return "UPDATE workspace_states SET workspace_id=?,data=" + value + ",updated_at=? WHERE workspace_key=?", nil
	}
	return "", ErrBoundedMetadataCoverage
}

// This is the only accounting override. It is private, requires an admitted
// stage registered to this exact transaction, and accepts only fixed SQL
// shapes whose complete arguments are checked against that stage's target.
func metadataSQLStageStatementInContext(ctx context.Context, tx *sqlTx, query string, args []any) (*metadataSQLStageStatement, error) {
	statement, present := ctx.Value(metadataSQLStageStatementKey{}).(*metadataSQLStageStatement)
	if !present {
		return nil, nil
	}
	if statement == nil {
		return nil, ErrBoundedMetadataCoverage
	}
	s := statement.stage
	if !s.open() || s.tx != tx || !s.admitted {
		return nil, ErrBoundedMetadataCoverage
	}
	want, err := s.statementQuery(statement.operation)
	if err != nil || want != query {
		return nil, ErrBoundedMetadataCoverage
	}
	var expected []any
	switch statement.operation {
	case metadataSQLStageInsert:
		if s.inserted {
			return nil, ErrBoundedMetadataCoverage
		}
		expected = []any{s.id}
	case metadataSQLStageDelete:
		expected = []any{s.id}
	case metadataSQLStageAppendData, metadataSQLStageAppendSearch:
		if !s.inserted || len(args) != 2 || args[1] != s.id {
			return nil, ErrBoundedMetadataCoverage
		}
		part, ok := args[0].(string)
		if !ok || len(part) == 0 || len(part) > metadataRecordChunkBytes || !utf8.ValidString(part) || strings.IndexByte(part, 0) >= 0 {
			return nil, ErrBoundedMetadataCoverage
		}
		column := 0
		if statement.operation == metadataSQLStageAppendSearch {
			column = 1
		}
		if s.consumed[column] || len(part) > s.expectedBytes(column)-s.written[column] {
			return nil, ErrBoundedMetadataCoverage
		}
		return statement, nil
	case metadataSQLStageWriteRow:
		if s.target.workspaceState || !s.inserted || s.consumed[0] || s.written[0] != s.target.dataBytes {
			return nil, ErrBoundedMetadataCoverage
		}
		expected = []any{s.target.workspace, s.target.field, s.target.key, s.target.order, s.id}
	case metadataSQLStageWriteSearch:
		if s.target.workspaceState || !s.inserted || s.target.searchBytes < 0 || s.consumed[1] || !s.consumed[0] ||
			s.written[1] != s.target.searchBytes {
			return nil, ErrBoundedMetadataCoverage
		}
		expected = []any{s.target.workspace, s.target.field, s.target.key, s.id}
	case metadataSQLStageWriteRoot:
		if !s.target.workspaceState || !s.inserted || s.consumed[0] || s.written[0] != s.target.dataBytes {
			return nil, ErrBoundedMetadataCoverage
		}
		expected = []any{s.target.workspaceID, s.id, s.target.updatedAt, s.target.workspace}
	}
	if !reflect.DeepEqual(expected, args) {
		return nil, ErrBoundedMetadataCoverage
	}
	return statement, nil
}

func (s *metadataSQLWriteStage) exec(ctx context.Context, operation metadataSQLStageOperation, args ...any) (sql.Result, error) {
	if !s.open() {
		return nil, ErrBoundedMetadataCoverage
	}
	query, err := s.statementQuery(operation)
	if err != nil {
		return nil, err
	}
	claim := &metadataSQLStageStatement{stage: s, operation: operation}
	ctx = context.WithValue(ctx, metadataSQLStageStatementKey{}, claim)
	return s.tx.ExecContext(ctx, query, args...)
}

func (statement *metadataSQLStageStatement) recordSQLInput(tx *sqlTx, query string, args []any) {
	statement.stage.statements++
	size := len(rewriteSQL(query, tx.dialect))
	for _, arg := range textArgs(tx.dialect, args) {
		switch value := arg.(type) {
		case string:
			size += len(value)
		case []byte:
			size += len(value)
		default:
			size += 16
		}
	}
	// This is driver SQL text plus argument bytes, excluding protocol framing.
	// Successful differential runs report it separately from the legacy bill.
	statement.stage.inputBytes += size
}

func (s *metadataSQLWriteStage) expectedBytes(column int) int {
	if column == 0 {
		return s.target.dataBytes
	}
	return s.target.searchBytes
}

func (s *metadataSQLWriteStage) writeMetadataRow(ctx context.Context) error {
	if !s.open() {
		return ErrBoundedMetadataCoverage
	}
	s.tx.metadataWritten = true
	if _, err := s.exec(ctx, metadataSQLStageWriteRow, s.target.workspace, s.target.field, s.target.key, s.target.order, s.id); err != nil {
		return err
	}
	s.consumed[0] = true
	return nil
}

func (s *metadataSQLWriteStage) writeSearchDocument(ctx context.Context) error {
	if !s.open() {
		return ErrBoundedMetadataCoverage
	}
	if _, err := s.exec(ctx, metadataSQLStageWriteSearch, s.target.workspace, s.target.field, s.target.key, s.id); err != nil {
		return err
	}
	s.consumed[1] = true
	return nil
}

func (s *metadataSQLWriteStage) writeWorkspaceState(ctx context.Context) error {
	if !s.open() {
		return ErrBoundedMetadataCoverage
	}
	s.tx.metadataWritten = true
	if _, err := s.exec(ctx, metadataSQLStageWriteRoot, s.target.workspaceID, s.id, s.target.updatedAt, s.target.workspace); err != nil {
		return err
	}
	s.consumed[0] = true
	return nil
}
