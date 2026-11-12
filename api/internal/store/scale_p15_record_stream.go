package store

import (
	"context"
	"io"
	"os"
)

// StreamCanonicalMetadataRecord copies stored JSON, including offloaded body
// references, in fixed chunks. It is a trusted storage primitive: callers
// authorize the record and reserve transport space before opening it. A private
// spool detaches the SQL snapshot before a potentially slow destination write.
func (s *SQLiteStore) StreamCanonicalMetadataRecord(ctx context.Context, workspace string, key RecordKey, dst io.Writer) error {
	if workspace == "" || !validMetadataKey(key) {
		return ErrMetadataRecordQuery
	}
	tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var size int64
	if err := tx.QueryRowContext(ctx, `SELECT LENGTH(data) FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`, workspace, key.Kind, key.ID).Scan(&size); err != nil {
		return err
	}
	reader := &canonicalMetadataReader{ctx: ctx, tx: tx, workspace: workspace, key: key, size: size}
	file, err := os.CreateTemp("", "flow-p15-record-*")
	if err != nil {
		return err
	}
	defer func() {
		_ = file.Close()
		_ = os.Remove(file.Name())
	}()
	buffer := make([]byte, 32<<10)
	if _, err := io.CopyBuffer(file, reader, buffer); err != nil {
		return err
	}
	if err := tx.Rollback(); err != nil {
		return err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	_, err = io.CopyBuffer(dst, offloadedContextReader{ctx, file}, buffer)
	return err
}
