package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
)

// The full-text search copies (issue_search_documents and
// metadata_search_documents) and their ngram/trigram indexes cost several
// times the text they hold. FLOW_SEARCH_INDEX_MAX_CHARS caps the text indexed
// per issue and per document: text past the cap is still stored and shown,
// but search does not find it.

// capSearchText keeps the first limit characters of text (all of it when
// limit is 0).
func capSearchText(text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	count := 0
	for index := range text {
		if count == limit {
			return text[:index]
		}
		count++
	}
	return text
}

// searchCapExpression applies the cap to a SQL text expression, as
// capSearchText does in Go.
func (s *SQLiteStore) searchCapExpression(expression string) string {
	limit := s.db.searchMaxChars
	if limit <= 0 {
		return expression
	}
	switch s.dialect {
	case "sqlite":
		return "substr(" + expression + ",1," + strconv.Itoa(limit) + ")"
	default:
		return "LEFT(" + expression + "," + strconv.Itoa(limit) + ")"
	}
}

// applySearchIndexCap brings stored search copies in line when the cap
// changed since the last start: the metadata triggers (which embed the cap)
// are recreated by ensureMetadataSearchIndex and the metadata copies are
// recomputed by its startup repair; issue copies are truncated in place when
// the cap shrinks and rebuilt by migrateIssueSearchIndex when it grows.
func (s *SQLiteStore) applySearchIndexCap(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS search_index_settings(name VARCHAR(191) PRIMARY KEY,value VARCHAR(191) NOT NULL)`); err != nil {
		return err
	}
	current := s.db.searchMaxChars
	previous := 0
	var stored string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM search_index_settings WHERE name='max_chars'`).Scan(&stored)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		previous, _ = strconv.Atoi(stored)
	}
	if previous == current {
		if err == nil {
			return nil
		}
		// First start with this setting and nothing to change.
		_, err = s.db.ExecContext(ctx, `INSERT INTO search_index_settings(name,value) VALUES('max_chars',?) ON CONFLICT(name) DO UPDATE SET value=excluded.value`, strconv.Itoa(current))
		return err
	}
	switch s.dialect {
	case "sqlite", "mysql":
		for _, trigger := range []string{"metadata_records_search_insert", "metadata_records_search_update"} {
			if _, err := s.db.ExecContext(ctx, `DROP TRIGGER IF EXISTS `+trigger); err != nil && !(s.dialect == "mysql" && isMySQLSearchPermissionError(err)) {
				return err
			}
		}
	}
	if current > 0 && (previous == 0 || current < previous) {
		length := "char_length"
		if s.dialect == "sqlite" {
			length = "length"
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE issue_search_documents SET content=`+s.searchCapExpression("content")+` WHERE `+length+`(content)>?`, current); err != nil {
			return err
		}
	} else if _, err := s.db.ExecContext(ctx, `UPDATE issue_search_migrations SET last_id='',complete=0`); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO search_index_settings(name,value) VALUES('max_chars',?) ON CONFLICT(name) DO UPDATE SET value=excluded.value`, strconv.Itoa(current))
	return err
}
