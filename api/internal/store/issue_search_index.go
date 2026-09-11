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
	return s.SearchIssueCandidateTerms(ctx, q, []string{text}, labelIDs, limit, visit)
}

// Retrieve a bounded union once: semantic synonyms must not independently
// decode the same issue, nor discard the caller's indexed facet predicates.
func (s *SQLiteStore) SearchIssueCandidateTerms(ctx context.Context, q IssueRecordQuery, terms []string, labelIDs []string, limit int, visit func(domain.Issue) error) error {
	q.Text = ""
	where, args, err := issueRecordWhere(q)
	if err != nil {
		return err
	}
	prefix, prefixArgs := issueAccessCTE(q)
	if len(terms) == 0 || len(terms) > 32 { return ErrIssueQuery }
	text := terms[0]
	// Use a subquery so SQLite FTS MATCH remains in a supported conjunctive
	// context when label matches are included alongside textual matches.
	var selections []string
	var textArgs []any
	for _, term := range terms {
		join, match := "", "LOWER(s.content) LIKE ? ESCAPE '!'"
		value := "%" + escapeIssueLike(strings.ToLower(term)) + "%"
		if s.dialect == "sqlite" && utf8.RuneCountInString(term) >= 3 { join = " JOIN issue_search_fts ON issue_search_fts.rowid=s.rowid"; match = "issue_search_fts MATCH ?"; value = "\"" + strings.ReplaceAll(term, "\"", "\"\"") + "\"" }
		if s.dialect == "mysql" && utf8.RuneCountInString(term) >= 2 { match = "MATCH(s.content) AGAINST (? IN BOOLEAN MODE)"; value = "\"" + strings.ReplaceAll(term, "\"", " ") + "\"" }
		if s.dialect == "postgres" { match = "s.content ILIKE ? ESCAPE '!'" }
		selections = append(selections, "SELECT s.issue_id FROM issue_search_documents s"+join+" WHERE s.workspace_key=? AND "+match)
		textArgs = append(textArgs, q.Workspace, value)
	}
	selection := strings.Join(selections, " UNION ")
	textMatch := "i.id IN (" + selection + ")"
	if s.dialect == "postgres" {
		cte := `search_text AS MATERIALIZED (` + selection + `) `
		if prefix == "" {
			prefix = "WITH " + cte
		} else {
			prefix = strings.TrimSpace(prefix) + ", " + cte
		}
		prefixArgs = append(prefixArgs, textArgs...)
		textArgs = nil
		textMatch = "i.id IN (SELECT issue_id FROM search_text)"
	}
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
	order := "CASE WHEN i.identifier=? THEN 0 WHEN LOWER(i.title)=LOWER(?) THEN 1 ELSE 2 END,i.updated_at DESC,i.id"
	orderArgs := []any{text, text}
	if q.Sort != "" && q.Sort != "sortOrder" {
		column := map[string]string{"createdAt":"created_at", "updatedAt":"updated_at", "title":"title", "priority":"priority"}[q.Sort]
		if column == "" { return ErrIssueQuery }
		direction := "DESC"; if q.Direction == "asc" { direction = "ASC" }
		order, orderArgs = "i."+column+" "+direction+",i.id", nil
	}
	inner := `SELECT i.id FROM issue_records i WHERE ` + where + ` AND ` + textMatch + ` ORDER BY `+order+` LIMIT ?`
	params := append(prefixArgs, args...)
	params = append(params, textArgs...)
	params = append(params, orderArgs...)
	params = append(params, limit, q.Workspace)
	rows, err := s.db.QueryContext(ctx, prefix+`SELECT COALESCE(output.list_data,output.data) FROM (`+inner+`) candidates JOIN issue_records output ON output.id=candidates.id AND output.workspace_key=?`, params...)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []domain.Issue{}
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
		items = append(items, issueListProjection(issue))
	}
	if err := rows.Err(); err != nil { return err }
	rows.Close()
	if err := s.resolveIssueReferences(ctx, q.Workspace, items); err != nil { return err }
	for _, issue := range items { if err := visit(issue); err != nil { return err } }
	return nil
}
