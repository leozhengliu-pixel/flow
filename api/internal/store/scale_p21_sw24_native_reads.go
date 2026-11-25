package store

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

// These are the runes accepted by Go's unicode.IsSpace. Removing all
// of them tests TrimSpace(value)=="" without transferring a large SQL string.
var scaleP21SW24Spaces = []rune{
	'\t', '\n', '\v', '\f', '\r', ' ', '\u0085', '\u00a0', '\u1680',
	'\u2000', '\u2001', '\u2002', '\u2003', '\u2004', '\u2005', '\u2006',
	'\u2007', '\u2008', '\u2009', '\u200a', '\u2028', '\u2029', '\u202f',
	'\u205f', '\u3000',
}

func (s *SQLiteStore) scaleP21SW24NonWhitespace(column string) (string, []any) {
	value := "COALESCE(" + column + ",'')"
	if s.dialect == "mysql" {
		value = "CAST(" + value + " AS BINARY)"
	}
	args := make([]any, 0, len(scaleP21SW24Spaces))
	for _, space := range scaleP21SW24Spaces {
		value = "REPLACE(" + value + ",?,'')"
		args = append(args, string(space))
	}
	if s.dialect == "sqlite" {
		return "LENGTH(CAST(" + value + " AS BLOB))>0", args
	}
	return "OCTET_LENGTH(" + value + ")>0", args
}

// ClassicGitHubSyncConfigured is a trusted configuration predicate. Only a
// boolean crosses SQL even when a connection or its secret is very large.
func (s *SQLiteStore) ClassicGitHubSyncConfigured(ctx context.Context, workspace string) (bool, error) {
	s.mu.RLock()
	if workspace == "" {
		workspace = s.lastWorkspaceKey
	}
	_, loaded := s.workspaces[workspace]
	s.mu.RUnlock()
	if !loaded {
		return false, nil
	}
	ctx, release, err := s.admitMetadataWork(ctx, 2<<20)
	if err != nil {
		return false, err
	}
	defer release()
	secret, secretArgs := s.scaleP21SW24NonWhitespace(s.jsonText("data", "config.webhookSecret"))
	env, envArgs := s.scaleP21SW24NonWhitespace(s.jsonText("data", "config.webhookSecretEnv"))
	provider, status := s.jsonText("data", "provider"), s.jsonText("data", "status")
	query := `SELECT CASE WHEN EXISTS(SELECT 1 FROM workspace_metadata_records
		WHERE workspace_key=? AND field='integrationConnections' AND ` +
		s.metadataRecordKeyEquals(provider, "?") + ` AND (` +
		s.metadataRecordKeyEquals(status, "?") + ` OR ` +
		s.metadataRecordKeyEquals(status, "?") + `) AND (` + secret + ` OR ` + env + `)) THEN 1 ELSE 0 END`
	args := []any{workspace, domain.CodeHostGitHub, "configured", "connected"}
	args = append(append(args, secretArgs...), envArgs...)
	var configured int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&configured); err != nil {
		return false, err
	}
	return configured != 0, nil
}

// ExportJobRecord reads an owned job's metadata. Export bytes are not a JSON
// field of ExportJob, so this never loads the exported artifact.
func (s *SQLiteStore) ExportJobRecord(ctx context.Context, workspace, owner, id string, allOwners bool) (domain.ExportJob, bool, error) {
	if len(id) > 191 || strings.ContainsRune(id, '\x00') {
		return domain.ExportJob{}, false, nil
	}
	jobs, _, err := s.readExportJobPage(ctx, workspace, owner, allOwners, id, true, 0, 1)
	if err != nil || len(jobs) == 0 {
		return domain.ExportJob{}, false, err
	}
	return jobs[0], true, nil
}

// ExportJobPage preserves the job monitor's canonical ordering and offset
// cursor contract. A count is independent of the bounded page of bodies.
func (s *SQLiteStore) ExportJobPage(ctx context.Context, workspace, owner string, allOwners bool, offset, limit int) ([]domain.ExportJob, int, error) {
	return s.readExportJobPage(ctx, workspace, owner, allOwners, "", false, offset, limit)
}

func (s *SQLiteStore) readExportJobPage(ctx context.Context, workspace, owner string, allOwners bool, id string, point bool, offset, limit int) ([]domain.ExportJob, int, error) {
	ctx, release, err := s.admitMetadataWork(ctx, 4<<20)
	if err != nil {
		return nil, 0, err
	}
	defer release()
	s.mu.RLock()
	if workspace == "" {
		workspace = s.lastWorkspaceKey
	}
	_, loaded := s.workspaces[workspace]
	s.mu.RUnlock()
	jobs := []domain.ExportJob{}
	if !loaded {
		return jobs, 0, nil
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	where, args := "workspace_key=? AND field='exportJobs'", []any{workspace}
	if !allOwners {
		where += " AND " + s.metadataRecordKeyEquals("COALESCE("+s.jsonText("data", "userId")+",'')", "?")
		args = append(args, owner)
	}
	if point {
		where += " AND " + s.metadataRecordKeyEquals("record_key", "?")
		args = append(args, id)
	}
	tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	var total int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM workspace_metadata_records WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if offset < 0 || offset > total {
		offset = 0
	}
	pageArgs := append(slices.Clone(args), limit, offset)
	page := " FROM workspace_metadata_records WHERE " + where + " ORDER BY collection_order,record_key LIMIT ? OFFSET ?"
	rows, err := tx.QueryContext(ctx, "SELECT LENGTH(data)"+page, pageArgs...)
	if err != nil {
		return nil, 0, err
	}
	remaining := MetadataRecordPageMaxBytes - metadataPageOverhead
	for rows.Next() {
		var size int
		if err := rows.Scan(&size); err != nil {
			rows.Close()
			return nil, 0, err
		}
		remaining -= size + 1
		if remaining < 0 {
			rows.Close()
			return nil, 0, ErrMetadataPageBudget
		}
	}
	rowErr := rows.Err()
	rows.Close()
	if rowErr != nil {
		return nil, 0, rowErr
	}
	// Both queries share a repeatable snapshot, so the approved page cannot
	// grow between its header check and body transfer.
	rows, err = tx.QueryContext(ctx, "SELECT data"+page, pageArgs...)
	if err != nil {
		return nil, 0, err
	}
	budget := newMetadataHydrationBudget()
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return nil, 0, err
		}
		if err := budget.consume(raw); err != nil {
			rows.Close()
			return nil, 0, err
		}
		var job domain.ExportJob
		if err := json.Unmarshal(raw, &job); err != nil {
			rows.Close()
			return nil, 0, err
		}
		jobs = append(jobs, job)
	}
	rowErr = rows.Err()
	rows.Close()
	if rowErr != nil {
		return nil, 0, rowErr
	}
	encoded, err := json.Marshal(jobs)
	if err != nil {
		return nil, 0, err
	}
	if len(encoded) > MetadataRecordPageMaxBytes {
		return nil, 0, ErrMetadataPageBudget
	}
	return jobs, total, nil
}
