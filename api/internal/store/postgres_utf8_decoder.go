package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// flow_utf8(bytea) is the one way PostgreSQL reads a stored record body as
// text. Record bodies are raw bytes: encoding/json accepts invalid UTF-8
// inside strings, so an opaque payload (an inline delivery, a view filter, a
// widget config) is stored byte for byte, and a plain convert_from(data,'UTF8')
// refuses such a row (SQLSTATE 22021), failing every read, filter and write
// that touches it. SQLite and MySQL read the same bytes without complaint.
//
// PostgreSQL has no error-free UTF-8 check, and every decoder that can repair
// a row costs on every call: a PL/pgSQL function is never inlined (its
// EXCEPTION block also enters a subtransaction per call; it assigns no
// subtransaction ID, but costs about 0.5us more), and a regular-expression
// validity check is 5-10x slower than convert_from. Being opaque to the
// planner it also changes plans (default COST 100). So flow_utf8 has two
// definitions, and the database holds the one its rows need:
//
//   - fast: an inlinable SQL function, convert_from(d,'UTF8'). The planner
//     replaces each call with convert_from itself, so a database whose bodies
//     are all valid UTF-8 (nearly every one) plans and reads exactly as
//     before.
//   - repair: convert_from, and on SQLSTATE 22021 flow_utf8_repair(d), which
//     decodes every maximal valid run as UTF-8 and every other byte as
//     U+FFFD, which is what encoding/json yields for those bytes, so keys,
//     ids and every valid string read the same on all dialects.
//
// The switch only goes from fast to repair, before the first raw body is
// stored: the statement wrappers call flow_utf8_enable_repair() ahead of any
// write whose arguments hold JSON with invalid UTF-8 (in the same
// transaction, so the switch commits with the row), and the first install
// scans existing bytea columns for such a body. Replacing the function
// invalidates every cached plan that inlined it.
const (
	postgresUTF8FastSource   = `SELECT convert_from(d,'UTF8')`
	postgresUTF8RepairSource = `
BEGIN
	RETURN convert_from(d,'UTF8');
EXCEPTION WHEN character_not_in_repertoire THEN
	RETURN flow_utf8_repair(d);
END
`
	// STABLE like convert_from: an IMMUTABLE SQL function wrapping a
	// STABLE call is not inlined.
	postgresUTF8Fast   = `CREATE OR REPLACE FUNCTION flow_utf8(d bytea) RETURNS text AS $$` + postgresUTF8FastSource + `$$ LANGUAGE sql STABLE PARALLEL SAFE`
	postgresUTF8Repair = `CREATE OR REPLACE FUNCTION flow_utf8(d bytea) RETURNS text AS $$` + postgresUTF8RepairSource + `$$ LANGUAGE plpgsql STABLE PARALLEL SAFE`
	// postgresUTF8Repairer decodes a body that is not valid UTF-8 (see
	// above). It is only called for such bodies.
	postgresUTF8Repairer = `CREATE OR REPLACE FUNCTION flow_utf8_repair(d bytea) RETURNS text AS $$
	SELECT COALESCE(string_agg(CASE WHEN r.m[1] IS NOT NULL THEN convert_from(convert_to(r.m[1],'LATIN1'),'UTF8') ELSE chr(65533) END,'' ORDER BY r.ord),'')
		FROM regexp_matches(convert_from(d,'LATIN1'),'(([\x01-\x7f]|[\xc2-\xdf][\x80-\xbf]|\xe0[\xa0-\xbf][\x80-\xbf]|[\xe1-\xec\xee\xef][\x80-\xbf][\x80-\xbf]|\xed[\x80-\x9f][\x80-\xbf]|\xf0[\x90-\xbf][\x80-\xbf][\x80-\xbf]|[\xf1-\xf3][\x80-\xbf][\x80-\xbf][\x80-\xbf]|\xf4[\x80-\x8f][\x80-\xbf][\x80-\xbf])+)|[\x80-\xff]','g') WITH ORDINALITY AS r(m,ord)
$$ LANGUAGE sql STABLE PARALLEL SAFE`
	// postgresUTF8Lock serialises the switch and the install (the startup
	// lock's class, a key of its own).
	postgresUTF8Lock = `SELECT pg_advisory_xact_lock(1181511543,2)`
	// postgresUTF8Mode reads the installed definition's source.
	postgresUTF8Mode = `SELECT COALESCE((SELECT prosrc FROM pg_proc WHERE oid=to_regprocedure('flow_utf8(bytea)')),'')`
	// postgresUTF8RawJSON reports whether a value is JSON whose bytes are
	// not valid UTF-8 (the first install's scan, on bodies that refused a
	// UTF-8 decode). Ciphertexts and compressed blobs are not JSON.
	postgresUTF8RawJSON = `CREATE OR REPLACE FUNCTION flow_utf8_install_raw_json(d bytea) RETURNS boolean AS $$
BEGIN
	PERFORM convert_from(d,'UTF8');
	RETURN false;
EXCEPTION WHEN character_not_in_repertoire THEN
	BEGIN
		PERFORM flow_utf8_repair(d)::jsonb;
		RETURN true;
	EXCEPTION WHEN OTHERS THEN
		RETURN false;
	END;
END
$$ LANGUAGE plpgsql`
	// postgresUTF8Scan reports whether any bytea column of the schema holds
	// such a body. Each column is first decoded in one statement; only a
	// column holding some invalid UTF-8 is checked row by row.
	postgresUTF8Scan = `CREATE OR REPLACE FUNCTION flow_utf8_install_scan() RETURNS boolean AS $$
DECLARE
	c record;
	found boolean;
BEGIN
	FOR c IN SELECT k.table_name, k.column_name FROM information_schema.columns k JOIN information_schema.tables t ON t.table_schema=k.table_schema AND t.table_name=k.table_name
		WHERE k.table_schema=current_schema() AND k.data_type='bytea' AND t.table_type='BASE TABLE' ORDER BY 1,2 LOOP
		BEGIN
			EXECUTE format('SELECT count(*) FROM %I WHERE convert_from(%I,''UTF8'') IS NULL', c.table_name, c.column_name);
			CONTINUE;
		EXCEPTION WHEN character_not_in_repertoire THEN
			NULL;
		END;
		EXECUTE format('SELECT EXISTS (SELECT 1 FROM %I WHERE flow_utf8_install_raw_json(%I))', c.table_name, c.column_name) INTO found;
		IF found THEN
			RETURN true;
		END IF;
	END LOOP;
	RETURN false;
END
$$ LANGUAGE plpgsql`
)

// postgresUTF8EnableRepair installs the repair decoder unless it is already
// installed. It runs in the caller's transaction.
var postgresUTF8EnableRepair = `CREATE OR REPLACE FUNCTION flow_utf8_enable_repair() RETURNS void AS $f$
BEGIN
	PERFORM pg_advisory_xact_lock(1181511543,2);
	IF (SELECT prosrc FROM pg_proc WHERE oid=to_regprocedure('flow_utf8(bytea)')) IS DISTINCT FROM ` + postgresQuote(postgresUTF8RepairSource) + ` THEN
		EXECUTE ` + postgresQuote(postgresUTF8Repair) + `;
	END IF;
END
$f$ LANGUAGE plpgsql`

func postgresQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// ensurePostgresUTF8Decoder installs flow_utf8 on PostgreSQL. A database that
// has one keeps it; on the first install the fast decoder is chosen unless an
// existing bytea column holds JSON with invalid UTF-8.
func (s *SQLiteStore) ensurePostgresUTF8Decoder(ctx context.Context) error {
	if s.dialect != "postgres" {
		return nil
	}
	repair, err := installPostgresUTF8Decoder(ctx, s.db.DB)
	if err != nil {
		return fmt.Errorf("install flow_utf8: %w", err)
	}
	s.db.utf8Repair.Store(repair)
	return nil
}

func installPostgresUTF8Decoder(ctx context.Context, db *sql.DB) (repair bool, err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	for _, statement := range []string{postgresUTF8Lock, postgresUTF8Repairer, postgresUTF8EnableRepair} {
		if _, err = tx.ExecContext(ctx, statement); err != nil {
			return false, err
		}
	}
	var source string
	if err = tx.QueryRowContext(ctx, postgresUTF8Mode).Scan(&source); err != nil {
		return false, err
	}
	switch source {
	case postgresUTF8RepairSource:
		repair = true
	case postgresUTF8FastSource:
	default:
		// First install (or an earlier definition): scan once.
		for _, statement := range []string{postgresUTF8RawJSON, postgresUTF8Scan} {
			if _, err = tx.ExecContext(ctx, statement); err != nil {
				return false, err
			}
		}
		if err = tx.QueryRowContext(ctx, `SELECT flow_utf8_install_scan()`).Scan(&repair); err != nil {
			return false, err
		}
		definition := postgresUTF8Fast
		if repair {
			definition = postgresUTF8Repair
		}
		for _, statement := range []string{definition, `DROP FUNCTION flow_utf8_install_scan()`, `DROP FUNCTION flow_utf8_install_raw_json(bytea)`} {
			if _, err = tx.ExecContext(ctx, statement); err != nil {
				return false, err
			}
		}
	}
	return repair, tx.Commit()
}

// rawUTF8JSON reports whether a statement argument is JSON whose bytes are
// not valid UTF-8: a record body the fast decoder would refuse.
func rawUTF8JSON(args []any) bool {
	for _, arg := range args {
		var value []byte
		switch typed := arg.(type) {
		case []byte:
			value = typed
		case json.RawMessage:
			value = typed
		default:
			continue
		}
		if !utf8.Valid(value) && json.Valid(value) {
			return true
		}
	}
	return false
}

type sqlExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// admitRawUTF8 switches PostgreSQL to the repair decoder before a write
// stores a body the fast decoder would refuse, and reports whether it did. On
// a transaction the switch commits or rolls back with the write; outside one
// it commits first.
func (d *sqlDatabase) admitRawUTF8(ctx context.Context, execer sqlExecer, inTransaction bool, args []any) (bool, error) {
	if d == nil || d.dialect != "postgres" || d.utf8Repair.Load() || !rawUTF8JSON(args) {
		return false, nil
	}
	if _, err := execer.ExecContext(ctx, `SELECT flow_utf8_enable_repair()`); err != nil {
		return false, errors.Join(errors.New("enable flow_utf8 repair"), err)
	}
	if !inTransaction {
		d.utf8Repair.Store(true)
	}
	return true, nil
}
