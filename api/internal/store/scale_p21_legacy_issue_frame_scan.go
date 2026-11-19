package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"reflect"
	"strings"
)

// A frame is borrowed only until the row callback returns. The physical SQL ID
// and raw receipt stay separate from the ID inside the decoded Issue.
type legacyIssueFrame struct {
	PhysicalID string
	StoredHash [32]byte
	Node       metadataLogicalNode
}

// This is the existing issue authorization reader's fixed-chunk SQL query,
// retaining the scanner's SQLite TEXT/BLOB locator. The caller owns its fixed
// transfer buffers before opening the reader.
type legacyIssueFrameReaderAt struct {
	ctx       context.Context
	tx        *sqlTx
	workspace string
	header    legacyIssueScanHeader
	length    string
}

func (r *legacyIssueFrameReaderAt) ReadAt(out []byte, offset int64) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if offset < 0 {
		return 0, ErrMetadataRecordQuery
	}
	if len(out) == 0 {
		return 0, nil
	}
	if offset >= r.header.size {
		return 0, io.EOF
	}
	data, id := "data", any(r.header.id)
	if r.tx.dialect == "sqlite" {
		data = "CAST(data AS BLOB)"
		if r.header.blob {
			id = []byte(r.header.id)
		}
	}
	read := 0
	for read < len(out) && offset+int64(read) < r.header.size {
		if err := r.ctx.Err(); err != nil {
			return read, err
		}
		size := min(int64(metadataRecordChunkBytes), int64(len(out)-read), r.header.size-offset-int64(read))
		var chunk []byte
		var actual int64
		query := "SELECT SUBSTR(" + data + ",?,?)," + r.length + " FROM issue_records WHERE workspace_key=? AND id=?"
		if err := r.tx.QueryRowContext(r.ctx, query, offset+int64(read)+1, size, r.workspace, id).Scan(&chunk, &actual); err != nil {
			return read, err
		}
		// A new statement must not silently use only the old prefix after
		// a READ COMMITTED row grows, or accept a shortened body.
		if actual != r.header.size || int64(len(chunk)) != size {
			return read, io.ErrUnexpectedEOF
		}
		read += copy(out[read:], chunk)
	}
	if read != len(out) {
		return read, io.EOF
	}
	return read, nil
}

// Only the legacy logical Issue image uses this path. Other callers retain
// scanLegacyIssueRows and its existing native-row contract and admission.
func (s *SQLiteStore) scanLegacyIssueFrames(ctx context.Context, tx *sqlTx, workspace string, visit func(legacyIssueFrame) error) error {
	if s == nil || !s.BoundedMetadataEnabled() || tx == nil || tx.database != s.db || tx.dialect != s.dialect {
		return ErrBoundedMetadataCoverage
	}
	if visit == nil {
		return ErrMetadataRecordQuery
	}
	if uint64(len(workspace)) > (metadataWorkBytes-(64<<10))/8 {
		return ErrMetadataWorkBudget
	}
	cursor := &ResourceRead{store: s}
	defer cursor.Close()
	if err := cursor.reserve(ctx, uint64(len(workspace))*8+64<<10); err != nil {
		return err
	}
	var maxID, maxBody int64
	query := "SELECT COALESCE(MAX(" + cursor.byteLength("id") + "),0),COALESCE(MAX(" + cursor.byteLength("data") + "),0) FROM issue_records WHERE workspace_key=?"
	if err := tx.QueryRowContext(ctx, query, workspace).Scan(&maxID, &maxBody); err != nil {
		return err
	}
	// maxStateBytes limits the root after Issues are removed, not these
	// record rows. Body bytes use the existing logical-spool envelope.
	if maxID < 0 || maxBody < 0 || maxBody > metadataLogicalDiskBytes || maxID > (metadataWorkBytes-4096)/8 {
		return ErrMetadataWorkBudget
	}
	perHeader := uint64(maxID)*8 + 512
	pageRows := min(legacyIssueScanPageRows, max(1, int((256<<10)/perHeader)))
	headerWork := uint64(pageRows)*perHeader + uint64(maxID)*8 + 4096
	if headerWork > metadataWorkBytes {
		return ErrMetadataWorkBudget
	}
	if err := cursor.reserve(ctx, headerWork); err != nil {
		return err
	}
	lastID, lastOrder, lastBlob, started := "", int64(0), false, false
	for {
		headers, err := s.legacyIssueScanHeaders(ctx, tx, workspace, maxID, maxBody, lastID, lastOrder, lastBlob, started, pageRows)
		if err != nil {
			return err
		}
		if len(headers) == 0 {
			return ctx.Err()
		}
		for start := 0; start < len(headers); {
			if headers[start].size > MetadataRecordPageMaxBytes {
				if err := s.visitLegacyIssueFrame(ctx, tx, workspace, headers[start], visit); err != nil {
					return err
				}
				start++
				continue
			}
			end, size := start+1, headers[start].size
			for end < len(headers) && headers[end].size <= MetadataRecordPageMaxBytes &&
				size <= issueMutationRowsBatchBytes && headers[end].size <= issueMutationRowsBatchBytes-size {
				size += headers[end].size
				end++
			}
			if err := s.visitLegacyIssueBatch(ctx, tx, workspace, headers[start:end], size, func(row *legacyIssueRow) error {
				return s.visitLegacyIssueNativeFrame(ctx, row, visit)
			}); err != nil {
				return err
			}
			start = end
		}
		last := headers[len(headers)-1]
		lastID, lastOrder, lastBlob, started = strings.Clone(last.id), last.order, last.blob, true
		if len(headers) < pageRows {
			return ctx.Err()
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}

// Small rows retain the proven batched native decoder and the original image
// capture's exact audit/admission lifetime. The batch still owns row/raw.
func (s *SQLiteStore) visitLegacyIssueNativeFrame(ctx context.Context, row *legacyIssueRow, visit func(legacyIssueFrame) error) error {
	value := reflect.ValueOf(row.Record.issue)
	cost, err := metadataAggregateNativeCost(ctx, value, 0)
	if err != nil {
		return err
	}
	if cost > (metadataWorkBytes-(64<<10))/2 {
		return ErrMetadataWorkBudget
	}
	work := &ResourceRead{store: s}
	defer work.Close()
	if err := work.reserve(ctx, cost*2+64<<10); err != nil {
		return err
	}
	frames, err := newMetadataLogicalBuilder(ctx)
	if err != nil {
		return err
	}
	defer frames.Close()
	if err := frames.auditJSONLocations(value); err != nil {
		return err
	}
	node, err := frames.writeValue(value, 0, false)
	if err != nil {
		return err
	}
	if node.unavailable || node.blocked {
		return ErrBoundedMetadataCoverage
	}
	return visit(legacyIssueFrame{PhysicalID: row.PhysicalID, StoredHash: sha256.Sum256(row.Record.raw), Node: node})
}

func (s *SQLiteStore) visitLegacyIssueFrame(ctx context.Context, tx *sqlTx, workspace string, header legacyIssueScanHeader, visit func(legacyIssueFrame) error) (err error) {
	work := &ResourceRead{store: s}
	defer work.Close()
	if err := work.reserve(ctx, logicalJSONStreamWork); err != nil {
		return err
	}
	if header.size < 0 || header.size > metadataLogicalDiskBytes {
		return ErrMetadataWorkBudget
	}
	spool, err := newMetadataLogicalRecordSpool(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, spool.Close()) }()
	if !spool.CanAppend(header.size) {
		return ErrMetadataWorkBudget
	}
	reader := &legacyIssueFrameReaderAt{ctx: ctx, tx: tx, workspace: workspace, header: header, length: work.byteLength("data")}
	hash := sha256.New()
	count, err := io.CopyBuffer(io.MultiWriter(spool, hash), io.NewSectionReader(reader, 0, header.size), make([]byte, metadataRecordChunkBytes))
	if err != nil {
		return err
	}
	if count != header.size {
		return io.ErrUnexpectedEOF
	}
	frames, err := newMetadataLogicalBuilder(ctx)
	if err != nil {
		return err
	}
	defer frames.Close()
	node, err := legacyIssueCanonicalRecord(ctx, s, frames, legacySessionSpoolPart(spool))
	if err != nil {
		return err
	}
	if node.unavailable || node.blocked {
		return ErrBoundedMetadataCoverage
	}
	var stored [32]byte
	copy(stored[:], hash.Sum(nil))
	if err := visit(legacyIssueFrame{PhysicalID: header.id, StoredHash: stored, Node: node}); err != nil {
		return err
	}
	return ctx.Err()
}
