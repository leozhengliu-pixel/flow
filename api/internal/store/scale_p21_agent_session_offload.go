package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// Stream the exact storedForm transcript rewrite, retaining large user text
// as raw disk ranges. The existing offloader handles one assistant message
// at a time, including its parts, references and content-addressed blob keys.
// If any assistant changes, the original offloader re-encodes every message
// object and the session object as sorted-key maps; otherwise it returns the
// original bytes unchanged.
func legacySessionStoredForm(ctx context.Context, owner *ResourceRead, tx *sqlTx, workspace string, input legacySessionJSONPart, output *RecordSpool, save bool) error {
	if tx.database == nil || tx.database.content == nil {
		_, err := io.CopyBuffer(output, input.reader(), make([]byte, metadataRecordChunkBytes))
		return err
	}
	fields, err := legacySessionJSONObject(ctx, owner, input)
	if err != nil {
		return err
	}
	messages, exists := fields["messages"]
	if !exists || legacySessionEmptyPart(messages) {
		_, err := io.CopyBuffer(output, input.reader(), make([]byte, metadataRecordChunkBytes))
		return err
	}
	items, err := legacySessionJSONArray(ctx, owner, messages)
	if err != nil {
		return err
	}
	idWork := &ResourceRead{store: owner.store}
	var sessionID string
	if id, exists := fields["id"]; exists {
		raw, err := legacySessionJSONBytes(ctx, idWork, id)
		if err != nil {
			idWork.Close()
			return err
		}
		_ = json.Unmarshal(raw, &sessionID)
	}
	if err := owner.reserve(ctx, uint64(len(sessionID))*8+1024); err != nil {
		idWork.Close()
		return err
	}
	idWork.Close()
	idRaw, err := json.Marshal(sessionID)
	if err != nil {
		return err
	}
	processed, err := newMetadataLogicalRecordSpool(ctx)
	if err != nil {
		return err
	}
	defer processed.Close()
	blobSpool, err := newMetadataLogicalRecordSpool(ctx)
	if err != nil {
		return err
	}
	defer blobSpool.Close()
	blobParts := map[string]legacySessionJSONPart{}
	var blobKeys []string
	changed := false
	for index, item := range items {
		// The role header is read independently from the complete message.
		work := &ResourceRead{store: owner.store}
		messageFields, parseErr := legacySessionJSONObject(ctx, work, item)
		if parseErr != nil {
			// Typed session nodes contain objects. SQL null messages are
			// decoded to a zero struct before this stored-form stage.
			work.Close()
			return parseErr
		}
		role := ""
		if part, found := messageFields["role"]; found {
			raw, err := legacySessionJSONBytes(ctx, work, part)
			if err != nil {
				work.Close()
				return err
			}
			_ = json.Unmarshal(raw, &role)
		}
		if role != "assistant" {
			work.Close()
			continue
		}
		err = func() error {
			defer work.Close()
			raw, err := legacySessionJSONBytes(ctx, work, item)
			if err != nil {
				return err
			}
			wrapper := make([]byte, 0, len(raw)+len(idRaw)+32)
			wrapper = append(wrapper, `{"id":`...)
			wrapper = append(wrapper, idRaw...)
			wrapper = append(wrapper, `,"messages":[`...)
			wrapper = append(wrapper, raw...)
			wrapper = append(wrapper, ']', '}')
			stored, blobs, err := tx.database.content.storedForm("agentSessions", wrapper)
			if err != nil {
				return err
			}
			if bytes.Equal(stored, wrapper) {
				return nil
			}
			changed = true
			if save {
				for _, blob := range blobs {
					if _, seen := blobParts[blob.key]; seen {
						continue
					}
					if err := owner.reserve(ctx, uint64(len(blob.key))*8+512); err != nil {
						return err
					}
					start := blobSpool.bytes
					if _, err := blobSpool.Write(blob.body); err != nil {
						return err
					}
					blobKeys = append(blobKeys, blob.key)
					blobParts[blob.key] = legacySessionJSONPart{blobSpool, start, blobSpool.bytes - start}
				}
			}
			var result struct {
				Messages []json.RawMessage `json:"messages"`
			}
			if err := json.Unmarshal(stored, &result); err != nil {
				return err
			}
			if len(result.Messages) != 1 {
				return ErrMetadataRecordQuery
			}
			start := processed.bytes
			if _, err := processed.Write(result.Messages[0]); err != nil {
				return err
			}
			items[index] = legacySessionJSONPart{processed, start, processed.bytes - start}
			return nil
		}()
		if err != nil {
			return err
		}
	}
	if !changed {
		_, err := io.CopyBuffer(output, input.reader(), make([]byte, metadataRecordChunkBytes))
		return err
	}
	array, err := newMetadataLogicalRecordSpool(ctx)
	if err != nil {
		return err
	}
	defer array.Close()
	if _, err := io.WriteString(array, "["); err != nil {
		return err
	}
	for index, item := range items {
		if index != 0 {
			if _, err := io.WriteString(array, ","); err != nil {
				return err
			}
		}
		work := &ResourceRead{store: owner.store}
		message, err := legacySessionJSONObject(ctx, work, item)
		if err == nil {
			err = legacySessionWriteJSONObject(ctx, work, message, array)
		}
		work.Close()
		if err != nil {
			return err
		}
	}
	if _, err := io.WriteString(array, "]"); err != nil {
		return err
	}
	fields["messages"] = legacySessionSpoolPart(array)
	if err := legacySessionWriteJSONObject(ctx, owner, fields, output); err != nil {
		return err
	}
	if save && len(blobKeys) != 0 {
		return legacySessionSaveBlobs(ctx, owner, tx, workspace, blobKeys, blobParts)
	}
	return nil
}

// Match contentOffload.save's first-key deduplication, 200-key lookup batches,
// captured clock, SQL and object/cache calls exactly. Body ownership is one
// missing blob at a time instead of every transcript body simultaneously.
func legacySessionSaveBlobs(ctx context.Context, owner *ResourceRead, tx *sqlTx, workspace string, keys []string, parts map[string]legacySessionJSONPart) error {
	for start := 0; start < len(keys); start += 200 {
		chunk := keys[start:min(start+200, len(keys))]
		clause, args := bindList("blob_key", chunk)
		rows, err := tx.QueryContext(ctx, `SELECT blob_key FROM metadata_blobs WHERE workspace_key=? AND orphaned_at=0 AND `+clause, append([]any{workspace}, args...)...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var key string
			if err := rows.Scan(&key); err != nil {
				rows.Close()
				return err
			}
			delete(parts, key)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	now := time.Now().UnixMilli()
	for _, key := range keys {
		part, exists := parts[key]
		if !exists {
			continue
		}
		err := func() error {
			work := &ResourceRead{store: owner.store}
			defer work.Close()
			body, err := legacySessionJSONBytes(ctx, work, part)
			if err != nil {
				return err
			}
			compressed := gzipBody(body)
			var data any = compressed
			content := tx.database.content
			if content.objects != nil {
				data = nil
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO metadata_blobs(workspace_key,blob_key,size,data,created_at,orphaned_at) VALUES(?,?,?,?,?,0) ON CONFLICT(workspace_key,blob_key) DO UPDATE SET orphaned_at=0`, workspace, key, len(body), data, now); err != nil {
				return err
			}
			if content.objects != nil {
				if _, err := content.objects.Put(ctx, offloadedObjectKey(workspace, key), bytes.NewReader(compressed), "application/gzip"); err != nil {
					return fmt.Errorf("store offloaded content: %w", err)
				}
			}
			content.cache.put(workspace+"/"+key, body)
			return nil
		}()
		if err != nil {
			return err
		}
	}
	return nil
}
