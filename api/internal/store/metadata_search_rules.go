package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
)

// metadataSearchRulesRevision names the Go side of the text a search document
// holds for a record (metadataSearchText, metadataSearchPart, capSearchText).
// Bump it with any change there so the next start reconciles every field once.
const metadataSearchRulesRevision = 1

// metadataSearchRules identifies everything that decides a search document's
// content: the Go extraction revision, the indexed fields and parts, the SQL
// expression the triggers use and the per-document cap. Every metadata write
// keeps the index current in its own transaction (triggers, and
// syncMetadataSearchDocument on the record write path), so a reconciliation
// that completed under the same rules does not need to run again.
func (s *SQLiteStore) metadataSearchRules() string {
	fields := make([]string, 0, len(searchMetadataKinds))
	for _, kind := range searchMetadataKinds {
		fields = append(fields, kind.field)
	}
	parts := []string{
		strconv.Itoa(metadataSearchRulesRevision),
		s.dialect,
		strings.Join(fields, ","),
		strings.Join(metadataSearchTextFields, ","),
		s.metadataSearchContent("NEW.data"),
		strconv.Itoa(s.db.searchMaxChars),
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

// metadataSearchFieldCurrent reports whether this field's last reconciliation
// completed under the current rules, so startup can skip it.
func (s *SQLiteStore) metadataSearchFieldCurrent(ctx context.Context, workspace, field string, complete int) (bool, error) {
	if complete == 0 {
		return false, nil
	}
	var rules string
	err := s.db.QueryRowContext(ctx, `SELECT rules FROM metadata_search_index_rules WHERE workspace_key=? AND field=?`, workspace, field).Scan(&rules)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return rules == s.metadataSearchRules(), nil
}

// recordMetadataSearchRules stores the rules a completed reconciliation used.
func (s *SQLiteStore) recordMetadataSearchRules(ctx context.Context, workspace, field string) error {
	statement := `INSERT INTO metadata_search_index_rules(workspace_key,field,rules) VALUES(?,?,?) ON CONFLICT(workspace_key,field) DO UPDATE SET rules=excluded.rules`
	if s.dialect == "mysql" {
		statement = `INSERT INTO metadata_search_index_rules(workspace_key,field,rules) VALUES(?,?,?) ON DUPLICATE KEY UPDATE rules=VALUES(rules)`
	}
	_, err := s.db.ExecContext(ctx, statement, workspace, field, s.metadataSearchRules())
	return err
}
