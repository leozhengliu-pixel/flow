package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"time"
)

// Do not update timestamps on identical metadata: under ROW/FULL binlogging
// even a timestamp-only update copies the entire JSON before/after image.
func writeWorkspaceMetadata(ctx context.Context, tx *sqlTx, key, id string, raw []byte) error {
	var err error
	raw, err = writeWorkspaceMetadataRecords(ctx, tx, key, raw)
	if err != nil {
		return err
	}
	var previous []byte
	var previousID string
	err = tx.QueryRowContext(ctx, "SELECT workspace_id,data FROM workspace_states WHERE workspace_key=?", key).Scan(&previousID, &previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && previousID == id && bytes.Equal(previous, raw) {
		return nil
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO workspace_states(workspace_key,workspace_id,data,updated_at) VALUES(?,?,?,?) ON CONFLICT(workspace_key) DO UPDATE SET workspace_id=excluded.workspace_id,data=excluded.data,updated_at=excluded.updated_at`, key, id, raw, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func writeAccountMetadata(ctx context.Context, tx *sqlTx, key string, viewer []byte) error {
	var previous []byte
	var previousKey string
	err := tx.QueryRowContext(ctx, "SELECT last_workspace_key,viewer FROM account_state WHERE id=1").Scan(&previousKey, &previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && key == previousKey && bytes.Equal(viewer, previous) {
		return nil
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO account_state(id,last_workspace_key,viewer,updated_at) VALUES(1,?,?,?) ON CONFLICT(id) DO UPDATE SET last_workspace_key=excluded.last_workspace_key,viewer=excluded.viewer,updated_at=excluded.updated_at`, key, viewer, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}
