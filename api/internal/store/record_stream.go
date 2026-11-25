package store

import (
	"context"
	"io"
)

// StreamCanonicalMetadataRecord copies stored JSON, including offloaded body
// references, in fixed chunks. It is a trusted storage primitive: callers
// authorize the record and reserve transport space before opening it. A private
// spool detaches SQL before writing to the destination while its budget allows;
// direct fallback holds the read snapshot until the stream finishes.
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
	file, err := NewRecordSpool()
	if err != nil {
		_, err = io.CopyBuffer(dst, reader, make([]byte, 32<<10))
		return err
	}
	defer file.Close()
	buffer := make([]byte, 32<<10)
	for {
		n, readErr := reader.Read(buffer)
		if n > 0 {
			written, writeErr := file.Write(buffer[:n])
			if writeErr != nil {
				if _, err := file.Seek(0, io.SeekStart); err != nil {
					return err
				}
				if _, err := io.CopyBuffer(dst, offloadedContextReader{ctx, file}, make([]byte, 32<<10)); err != nil {
					return err
				}
				_ = file.Close()
				copied, err := dst.Write(buffer[written:n])
				if err != nil {
					return err
				}
				if copied != n-written {
					return io.ErrShortWrite
				}
				if readErr != nil {
					if readErr == io.EOF {
						return nil
					}
					return readErr
				}
				_, err = io.CopyBuffer(dst, reader, buffer)
				return err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
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
