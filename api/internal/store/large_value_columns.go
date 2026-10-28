package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

// largeValueColumns are the MySQL columns whose values can outgrow TEXT
// (64 KiB). Postgres TEXT and SQLite TEXT have no such limit, so only MySQL
// changes. Each type is the smallest one no accepted write can overflow:
//
//   - domain_events.previous_values holds the pre-edit fields of the changed
//     entity: a document's whole body (content, collaborative state, rich
//     JSON), each up to the 16 MiB request limit and together more, so
//     LONGTEXT (4 GiB).
//   - auth_identities.claims_json holds an identity provider's claims, or a
//     SCIM user's profile from a request of up to 16 MiB that JSON escaping
//     can grow past 16 MiB, so LONGTEXT.
//   - code_host_accounts.scopes holds the scopes a code host grants, read
//     from a response capped at 2 MiB, so MEDIUMTEXT (16 MiB).
var largeValueColumns = []struct{ table, column, kind string }{
	{"domain_events", "previous_values", "longtext"},
	{"auth_identities", "claims_json", "longtext"},
	{"code_host_accounts", "scopes", "mediumtext"},
}

var mysqlTextCapacity = map[string]int{"tinytext": 1, "text": 2, "mediumtext": 3, "longtext": 4}

// widenLargeValueColumns changes each large value column that is smaller
// than its type on MySQL. Rows are kept (MODIFY copies them); a column that
// already has the type or a larger one (a fresh install, a re-run, or an
// operator who ran the ALTER ahead of the upgrade) is left alone, as is a
// table that does not exist yet (it is created with the new type).
//
// MySQL cannot change a column type in place: the table is rebuilt
// (ALGORITHM=COPY). LOCK=SHARED keeps the table readable meanwhile; writes to
// it wait until the copy is done.
func (s *SQLiteStore) widenLargeValueColumns(ctx context.Context) error {
	if s.dialect != "mysql" {
		return nil
	}
	for _, target := range largeValueColumns {
		var dataType, nullable, charset, collation string
		var defaultValue sql.NullString
		err := s.db.QueryRowContext(ctx, `SELECT DATA_TYPE,IS_NULLABLE,COLUMN_DEFAULT,CHARACTER_SET_NAME,COLLATION_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND COLUMN_NAME=?`, target.table, target.column).Scan(&dataType, &nullable, &defaultValue, &charset, &collation)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read %s.%s: %w", target.table, target.column, err)
		}
		current, known := mysqlTextCapacity[dataType]
		if !known {
			return fmt.Errorf("%s.%s is %s, expected a text type", target.table, target.column, dataType)
		}
		if current >= mysqlTextCapacity[target.kind] {
			continue
		}
		if defaultValue.Valid {
			// MODIFY would drop it; none of these columns has one.
			return fmt.Errorf("%s.%s has a default value; change its type by hand", target.table, target.column)
		}
		null := "NULL"
		if nullable == "NO" {
			null = "NOT NULL"
		}
		slog.Info("widening database column", "table", target.table, "column", target.column, "from", dataType, "to", target.kind)
		statement := fmt.Sprintf("ALTER TABLE `%s` MODIFY COLUMN `%s` %s CHARACTER SET %s COLLATE %s %s, ALGORITHM=COPY, LOCK=SHARED", target.table, target.column, target.kind, charset, collation, null)
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("widen %s.%s to %s: %w", target.table, target.column, target.kind, err)
		}
	}
	return nil
}

// widerKeyColumns are VARCHAR columns created narrower than the values
// written to them. Each is widened to length characters on MySQL and
// Postgres (SQLite does not enforce VARCHAR sizes):
//
//   - issue_attribute_records.field was VARCHAR(32), but holds
//     "suggestedLabel:<id>" and the other per-target Triage Intelligence
//     fields (40 to 60 characters with Flow's ids). 191 keeps both of its
//     MySQL indexes under the 3,072-byte InnoDB key limit in utf8mb4: the
//     primary key (workspace_key,issue_id,field) is 573 characters (2,292
//     bytes) and issue_attributes_value_idx (workspace_key,field,value,
//     issue_id) 764 characters (3,056 bytes). Longer fields are stored
//     through indexKey.
var widerKeyColumns = []struct {
	table, column string
	length        int
}{
	{"issue_attribute_records", "field", 191},
}

// widenKeyColumns changes each widerKeyColumns column that is shorter than
// its length. Rows, NOT NULL, the character set and collation are kept; a
// column that is already as wide or wider (a fresh install, a re-run, an
// operator who ran the ALTER ahead of the upgrade) is left alone, as is a
// table that does not exist yet (it is created with the new width).
//
// MySQL: a VARCHAR whose length prefix grows from one byte to two (past 255
// bytes) cannot change in place, so the table and its indexes are rebuilt
// (ALGORITHM=COPY); LOCK=SHARED keeps it readable meanwhile, and writes to
// it wait until the copy is done. Postgres: widening a VARCHAR changes only
// the catalog (no table rewrite, no index rebuild), but takes an ACCESS
// EXCLUSIVE lock for that moment, so it first waits for transactions
// already using the table.
func (s *SQLiteStore) widenKeyColumns(ctx context.Context) error {
	if s.dialect != "mysql" && s.dialect != "postgres" {
		return nil
	}
	for _, target := range widerKeyColumns {
		var dataType, nullable string
		var length sql.NullInt64
		var defaultValue, charset, collation sql.NullString
		query := `SELECT DATA_TYPE,CHARACTER_MAXIMUM_LENGTH,IS_NULLABLE,COLUMN_DEFAULT,CHARACTER_SET_NAME,COLLATION_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND COLUMN_NAME=?`
		if s.dialect == "postgres" {
			query = `SELECT data_type,character_maximum_length,is_nullable,column_default,character_set_name,collation_name FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=? AND column_name=?`
		}
		err := s.db.QueryRowContext(ctx, query, target.table, target.column).Scan(&dataType, &length, &nullable, &defaultValue, &charset, &collation)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read %s.%s: %w", target.table, target.column, err)
		}
		switch strings.ToLower(dataType) {
		case "varchar", "character varying":
		case "text", "mediumtext", "longtext":
			continue // unbounded enough already (an operator's own change)
		default:
			return fmt.Errorf("%s.%s is %s, expected VARCHAR", target.table, target.column, dataType)
		}
		if !length.Valid || length.Int64 >= int64(target.length) {
			continue
		}
		slog.Info("widening database column", "table", target.table, "column", target.column, "from", fmt.Sprintf("varchar(%d)", length.Int64), "to", fmt.Sprintf("varchar(%d)", target.length))
		// ALTER COLUMN TYPE keeps NOT NULL, the default and the indexes.
		statement := fmt.Sprintf(`ALTER TABLE %s ALTER COLUMN %s TYPE VARCHAR(%d)`, target.table, target.column, target.length)
		if s.dialect == "mysql" {
			if defaultValue.Valid {
				// MODIFY would drop it; these columns have none.
				return fmt.Errorf("%s.%s has a default value; change its width by hand", target.table, target.column)
			}
			null := "NULL"
			if nullable == "NO" {
				null = "NOT NULL"
			}
			statement = fmt.Sprintf("ALTER TABLE `%s` MODIFY COLUMN `%s` VARCHAR(%d) CHARACTER SET %s COLLATE %s %s, ALGORITHM=COPY, LOCK=SHARED", target.table, target.column, target.length, charset.String, collation.String, null)
		}
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("widen %s.%s to VARCHAR(%d): %w", target.table, target.column, target.length, err)
		}
	}
	return nil
}
