package store

import (
	"context"
	"time"
)

const recordJournalAccountWrite = `INSERT INTO account_state(id,last_workspace_key,viewer,updated_at) VALUES(1,?,?,?) ON CONFLICT(id) DO UPDATE SET last_workspace_key=excluded.last_workspace_key,viewer=excluded.viewer,updated_at=excluded.updated_at WHERE account_state.last_workspace_key<>excluded.last_workspace_key OR CAST(account_state.viewer AS BLOB)<>CAST(excluded.viewer AS BLOB)`

func recordJournalWriteAccount(ctx context.Context, tx *sqlTx, key string, viewer []byte) (bool, error) {
	if tx.journalToken == "" || tx.dialect != "sqlite" || len(viewer) == 0 {
		return false, nil
	}
	statement := tx.database.journalWrites[recordJournalAccountWrite]
	if statement == nil {
		return false, nil
	}
	if tx.writeErr != nil {
		return true, tx.writeErr
	}
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	bound := tx.Tx.StmtContext(ctx, statement)
	observeSQLStatement(ctx, recordJournalAccountWrite)
	result, err := bound.ExecContext(ctx, key, viewer, stamp)
	_ = bound.Close()
	if err != nil {
		return true, err
	}
	if changed, countErr := result.RowsAffected(); countErr == nil && changed == 0 {
		return true, nil
	}
	// A real change follows the original statement and byte-budget check.
	// The no-op path writes nothing and consumes no canonical write budget.
	_, err = tx.ExecContext(ctx, `INSERT INTO account_state(id,last_workspace_key,viewer,updated_at) VALUES(1,?,?,?) ON CONFLICT(id) DO UPDATE SET last_workspace_key=excluded.last_workspace_key,viewer=excluded.viewer,updated_at=excluded.updated_at`, key, viewer, stamp)
	return true, err
}
