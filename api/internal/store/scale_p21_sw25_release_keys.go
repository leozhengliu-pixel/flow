package store

import (
	"context"
	"database/sql"
	"time"
)

// ReleasePipelineForKeyHash reads only key decisions and expiry scalars. The
// canonical pipeline body, including legal large path and stage metadata, is
// never transferred. Callers retain workspace selection and key hashing.
func (s *SQLiteStore) ReleasePipelineForKeyHash(ctx context.Context, workspace, hash, pipelineID string, now time.Time) (string, bool, error) {
	if hash == "" {
		return "", false, nil
	}
	ctx, release, err := s.admitMetadataWork(ctx, 2<<20)
	if err != nil {
		return "", false, err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	current := s.metadataRecordKeyEquals(s.jsonText("data", "accessKeyHash"), "?")
	previous := s.metadataRecordKeyEquals(s.jsonText("data", "previousAccessKeyHash"), "?")
	revoked, expires := s.jsonText("data", "accessKeyRevokedAt"), s.jsonText("data", "previousAccessKeyExpiresAt")
	order, id, continued := int64(0), "", false
	for {
		where := `workspace_key=? AND field='releasePipelines' AND (` + current + ` OR ` + previous + `)`
		args := []any{hash, hash, workspace, hash, hash}
		if pipelineID != "" {
			where += " AND " + s.metadataRecordKeyEquals("record_key", "?")
			args = append(args, pipelineID)
		}
		if continued {
			where += ` AND (collection_order>? OR (collection_order=? AND record_key>?))`
			args = append(args, order, order, id)
		}
		args = append(args, MetadataRecordBatchMaxKeys)
		rows, err := tx.QueryContext(ctx, `SELECT record_key,collection_order,CASE WHEN `+current+` THEN 1 ELSE 0 END,CASE WHEN `+previous+` THEN 1 ELSE 0 END,
			CASE WHEN LENGTH(`+revoked+`)<=64 THEN `+revoked+` ELSE NULL END,
			CASE WHEN LENGTH(`+expires+`)<=64 THEN `+expires+` ELSE NULL END,
			CASE WHEN LENGTH(`+revoked+`)>64 OR LENGTH(`+expires+`)>64 THEN 1 ELSE 0 END
			FROM workspace_metadata_records WHERE `+where+` ORDER BY collection_order,record_key LIMIT ?`, args...)
		if err != nil {
			return "", false, err
		}
		count := 0
		for rows.Next() {
			var currentMatch, previousMatch, oversized int
			var revokedAt, expiresAt sql.NullString
			if err := rows.Scan(&id, &order, &currentMatch, &previousMatch, &revokedAt, &expiresAt, &oversized); err != nil {
				rows.Close()
				return "", false, err
			}
			if oversized != 0 {
				rows.Close()
				return "", false, ErrMetadataPageBudget
			}
			active := !revokedAt.Valid
			if revokedAt.Valid {
				at, err := time.Parse(time.RFC3339Nano, revokedAt.String)
				if err != nil {
					rows.Close()
					return "", false, err
				}
				active = now.Before(at)
			}
			grace := false
			if expiresAt.Valid {
				at, err := time.Parse(time.RFC3339Nano, expiresAt.String)
				if err != nil {
					rows.Close()
					return "", false, err
				}
				grace = now.Before(at)
			}
			if currentMatch != 0 && active || previousMatch != 0 && grace {
				rows.Close()
				return id, true, nil
			}
			count++
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return "", false, err
		}
		if count < MetadataRecordBatchMaxKeys {
			return "", false, nil
		}
		continued = true
	}
}
