package store

import "context"

// Two 191-character content IDs can need 2,299 bytes after Go JSON escaping.
// The composite MySQL keys still fit its 3,072-byte index limit at 2,304.
func (s *SQLiteStore) widenRecordJournalKeys(ctx context.Context) error {
	for _, table := range []string{"record_versions", "record_journal_dirty"} {
		schema := "current_schema()"
		if s.dialect == "mysql" {
			schema = "DATABASE()"
		}
		var size int
		if err := s.db.QueryRowContext(ctx, `SELECT character_maximum_length FROM information_schema.columns WHERE table_schema=`+schema+` AND table_name=? AND column_name='record_key'`, table).Scan(&size); err != nil {
			return err
		}
		if size >= 2304 {
			continue
		}
		query := `ALTER TABLE ` + table + ` ALTER COLUMN record_key TYPE VARCHAR(2304) COLLATE "C"`
		if s.dialect == "mysql" {
			query = `ALTER TABLE ` + table + ` MODIFY record_key VARBINARY(2304) NOT NULL`
		}
		if _, err := s.db.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	return nil
}
