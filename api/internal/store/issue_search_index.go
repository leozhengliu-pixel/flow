package store

import (
	"context"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"flow/api/internal/domain"
)

func (s *SQLiteStore) ensureIssueSearchIndex(ctx context.Context) error {
	textType := "TEXT"
	if s.dialect == "mysql" {
		textType = "MEDIUMTEXT"
	}
	statements := []string{`CREATE TABLE IF NOT EXISTS issue_search_documents(workspace_key VARCHAR(191) NOT NULL,issue_id VARCHAR(191) NOT NULL,content ` + textType + ` NOT NULL,PRIMARY KEY(workspace_key,issue_id))`,
		`CREATE TABLE IF NOT EXISTS issue_search_migrations(workspace_key VARCHAR(191) PRIMARY KEY,last_id VARCHAR(191) NOT NULL,complete INTEGER NOT NULL)`}
	switch s.dialect {
	case "sqlite":
		statements = append(statements,
			`CREATE VIRTUAL TABLE IF NOT EXISTS issue_search_fts USING fts5(content,content='issue_search_documents',content_rowid='rowid',tokenize='trigram')`,
			`CREATE TRIGGER IF NOT EXISTS issue_search_insert AFTER INSERT ON issue_search_documents BEGIN INSERT INTO issue_search_fts(rowid,content) VALUES(new.rowid,new.content); END`,
			`CREATE TRIGGER IF NOT EXISTS issue_search_delete AFTER DELETE ON issue_search_documents BEGIN INSERT INTO issue_search_fts(issue_search_fts,rowid,content) VALUES('delete',old.rowid,old.content); END`,
			`CREATE TRIGGER IF NOT EXISTS issue_search_update AFTER UPDATE ON issue_search_documents BEGIN INSERT INTO issue_search_fts(issue_search_fts,rowid,content) VALUES('delete',old.rowid,old.content); INSERT INTO issue_search_fts(rowid,content) VALUES(new.rowid,new.content); END`)
	case "mysql":
		statements = append(statements, `CREATE FULLTEXT INDEX issue_search_fulltext ON issue_search_documents(content) WITH PARSER ngram`)
	case "postgres":
		statements = append(statements, `CREATE EXTENSION IF NOT EXISTS pg_trgm`, `CREATE INDEX IF NOT EXISTS issue_search_trigram ON issue_search_documents USING gin(content gin_trgm_ops)`)
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil && !(s.dialect == "mysql" && isDuplicateIndex(err)) {
			return err
		}
	}
	return nil
}

func writeIssueSearchIndex(ctx context.Context, tx *sqlTx, workspace string, issues []domain.Issue) error {
	ids := []string{}
	rows := [][]string{}
	for _, issue := range issues {
		ids = append(ids, issue.ID)
		rows = append(rows, []string{issue.ID, issue.Identifier + "\n" + issue.Title + "\n" + issue.Description})
	}
	return syncIssueIndexRows(ctx, tx, workspace, "issue_search_documents", []string{"issue_id", "content"}, 1, ids, rows)
}

func (s *SQLiteStore) migrateIssueSearchIndex(ctx context.Context) error {
	for workspace := range s.workspaces {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO issue_search_migrations(workspace_key,last_id,complete) VALUES(?,'',0) ON CONFLICT DO NOTHING`, workspace); err != nil {
			return err
		}
		for {
			var last string
			var done int
			if err := s.db.QueryRowContext(ctx, `SELECT last_id,complete FROM issue_search_migrations WHERE workspace_key=?`, workspace).Scan(&last, &done); err != nil {
				return err
			}
			if done != 0 {
				break
			}
			tx, err := s.db.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			err = func() error {
				rows, err := tx.QueryContext(ctx, `SELECT data FROM issue_records WHERE workspace_key=? AND id>? ORDER BY id LIMIT 64`, workspace, last)
				if err != nil {
					return err
				}
				batch := []domain.Issue{}
				for rows.Next() {
					var raw []byte
					var issue domain.Issue
					if err := rows.Scan(&raw); err != nil {
						rows.Close()
						return err
					}
					if err := json.Unmarshal(raw, &issue); err != nil {
						rows.Close()
						return err
					}
					batch = append(batch, issue)
				}
				err = rows.Err()
				rows.Close()
				if err != nil {
					return err
				}
				if err := writeIssueSearchIndex(ctx, tx, workspace, batch); err != nil {
					return err
				}
				if len(batch) > 0 {
					last = batch[len(batch)-1].ID
				}
				if len(batch) < 64 {
					done = 1
				}
				_, err = tx.ExecContext(ctx, `UPDATE issue_search_migrations SET last_id=?,complete=? WHERE workspace_key=?`, last, done, workspace)
				return err
			}()
			if err != nil {
				tx.Rollback()
				return err
			}
			if err := tx.Commit(); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *SQLiteStore) SearchIssueCandidates(ctx context.Context, q IssueRecordQuery, text string, labelIDs []string, limit int, visit func(domain.Issue) error) error {
	q.Text = ""
	q.Filter = IssueFilter{}
	where, args, err := issueRecordWhere(q)
	if err != nil {
		return err
	}
	prefix, prefixArgs := issueAccessCTE(q)
	join := ""
	match := "LOWER(s.content) LIKE ? ESCAPE '!'"
	searchArgs := []any{"%" + escapeIssueLike(strings.ToLower(text)) + "%"}
	if s.dialect == "sqlite" && utf8.RuneCountInString(text) >= 3 {
		join = " JOIN issue_search_fts ON issue_search_fts.rowid=s.rowid"
		match = "issue_search_fts MATCH ?"
		searchArgs = []any{"\"" + strings.ReplaceAll(text, "\"", "\"\"") + "\""}
	}
	if s.dialect == "mysql" && utf8.RuneCountInString(text) >= 2 {
		match = "MATCH(s.content) AGAINST (? IN BOOLEAN MODE)"
		searchArgs = []any{"\"" + strings.ReplaceAll(text, "\"", " ") + "\""}
	}
	if s.dialect == "postgres" {
		match = "s.content ILIKE ? ESCAPE '!'"
	}
	// Use a subquery so SQLite FTS MATCH remains in a supported conjunctive
	// context when label matches are included alongside textual matches.
	textMatch := `i.id IN (SELECT s.issue_id FROM issue_search_documents s` + join + ` WHERE s.workspace_key=? AND ` + match + `)`
	textArgs := append([]any{q.Workspace}, searchArgs...)
	if len(labelIDs) > 0 {
		clause, values := bindList("l.label_id", labelIDs)
		textMatch = "(" + textMatch + " OR EXISTS (SELECT 1 FROM issue_label_records l WHERE l.workspace_key=i.workspace_key AND l.issue_id=i.id AND " + clause + "))"
		textArgs = append(textArgs, values...)
	}
	if limit < 1 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	inner := `SELECT i.id FROM issue_records i WHERE ` + where + ` AND ` + textMatch + ` ORDER BY CASE WHEN i.identifier=? THEN 0 WHEN LOWER(i.title)=LOWER(?) THEN 1 ELSE 2 END,i.updated_at DESC,i.id LIMIT ?`
	params := append(prefixArgs, args...)
	params = append(params, textArgs...)
	params = append(params, text, text, limit, q.Workspace)
	metadata, ok := s.WorkspaceMetadata(q.Workspace)
	if !ok {
		return ErrAuthForbidden
	}
	refs := newIssueReferences(metadata)
	rows, err := s.db.QueryContext(ctx, prefix+`SELECT output.data FROM (`+inner+`) candidates JOIN issue_records output ON output.id=candidates.id AND output.workspace_key=?`, params...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var issue domain.Issue
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &issue); err != nil {
			return err
		}
		normalizeIssueRecord(&issue)
		if err := visit(refs.resolve(issue)); err != nil {
			return err
		}
	}
	return rows.Err()
}
