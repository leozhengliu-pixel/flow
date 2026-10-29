package store

import (
	"errors"
	"path/filepath"
	"testing"
)

// Two connections to one SQLite file (two servers in tests, a tool next to
// the server): a transaction that read before the other connection wrote
// cannot become a write. That is a lost race, reported as ErrConcurrentWrite
// so writers rerun on fresh data, not as a database failure.
func TestSQLiteWriteAfterAnotherConnectionWroteIsAConcurrentWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conflict.db")
	var repos [2]*SQLiteStore
	for index := range repos {
		repo, err := OpenSQLiteTestFixture(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { repo.Close() })
		repos[index] = repo
	}
	ctx := t.Context()
	if _, err := repos[0].db.ExecContext(ctx, `CREATE TABLE conflict_probe(id INTEGER PRIMARY KEY, value INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := repos[0].db.ExecContext(ctx, `INSERT INTO conflict_probe(id,value) VALUES(1,0)`); err != nil {
		t.Fatal(err)
	}
	tx, err := repos[0].db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var value int
	if err := tx.QueryRowContext(ctx, `SELECT value FROM conflict_probe WHERE id=1`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if _, err := repos[1].db.ExecContext(ctx, `UPDATE conflict_probe SET value=value+1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(ctx, `UPDATE conflict_probe SET value=? WHERE id=1`, value+10)
	if !errors.Is(err, ErrConcurrentWrite) || !IsDatabaseError(err) {
		t.Fatalf("write after the other connection wrote = %v, want ErrConcurrentWrite keeping the driver error", err)
	}
}
