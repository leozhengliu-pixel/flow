package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"time"
)

// workspaceKeyRepairMigrationVersion records the one-time repair of rows that
// workspace renames left under a previous URL key before renames moved them.
const workspaceKeyRepairMigrationVersion = 1040

// migrateRenamedWorkspaceKeys repairs earlier renames:
//
//  1. Previous keys are recovered from the domain events that recorded a
//     workspace's key (workspace.created and workspace.updated carry the
//     urlKey, a deletion request names it) and written to
//     workspace_key_renames. Events older than the event retention are gone:
//     renames recorded only there cannot be recovered this way (see
//     docs/operations.md for what operators can do).
//  2. Rows under a previous key are moved to the workspace's current key
//     (AdoptRenamedWorkspaceRows), unless another workspace had the key too.
//  3. Offloaded bodies a workspace's records refer to but that are stored
//     only under a key no workspace has are copied to the workspace. Their
//     keys name the document or conversation and end in a hash of the body,
//     so this needs no rename history.
//
// Every step is idempotent.
func (s *SQLiteStore) migrateRenamedWorkspaceKeys(ctx context.Context) error {
	recorded, err := s.seedWorkspaceKeyRenames(ctx)
	if err != nil {
		return fmt.Errorf("previous keys: %w", err)
	}
	report, err := s.AdoptRenamedWorkspaceRows(ctx, time.Time{})
	if err != nil {
		return err
	}
	recovered := 0
	for _, key := range s.WorkspaceKeys() {
		count, err := s.recoverWorkspaceContent(ctx, key, nil, nil)
		recovered += count
		if err != nil {
			return fmt.Errorf("workspace %q content: %w", key, err)
		}
	}
	if recorded+int(report.Moved)+report.Ambiguous+recovered > 0 {
		slog.Info("repaired data left under previous workspace keys", "previous_keys", recorded, "rows_moved", report.Moved, "ambiguous_keys", report.Ambiguous, "bodies_recovered", recovered)
	}
	return nil
}

// seedWorkspaceKeyRenames records, for every workspace the domain events
// name, the keys it had other than its current one (every key, for a
// workspace that no longer exists, so its keys count as taken by it). It
// returns how many keys it recorded.
func (s *SQLiteStore) seedWorkspaceKeyRenames(ctx context.Context) (int, error) {
	if s.BoundedMetadataEnabled() {
		return s.seedWorkspaceKeyRenamesBounded(ctx)
	}
	s.mu.RLock()
	live := map[string]string{}
	for key, data := range s.workspaces {
		live[data.Workspace.ID] = key
	}
	s.mu.RUnlock()
	type seen struct{ at string }
	keys := map[[2]string]seen{}
	last := ""
	for {
		rows, err := s.db.QueryContext(ctx, `SELECT id,event_type,aggregate_id,workspace_id,payload,created_at FROM domain_events WHERE event_type IN ('workspace.created','workspace.updated','workspace.deletion_scheduled','workspace.deletion_canceled') AND id>? ORDER BY id LIMIT 500`, last)
		if err != nil {
			return 0, err
		}
		count := 0
		for rows.Next() {
			var kind, aggregate, created string
			var workspace sql.NullString
			var payload []byte
			if err := rows.Scan(&last, &kind, &aggregate, &workspace, &payload, &created); err != nil {
				rows.Close()
				return 0, err
			}
			count++
			id, key := workspace.String, ""
			switch kind {
			case "workspace.created", "workspace.updated":
				if id == "" {
					id = aggregate
				}
				var value struct {
					URLKey string `json:"urlKey"`
				}
				_ = json.Unmarshal(payload, &value)
				key = value.URLKey
			default:
				key = aggregate
			}
			if id == "" || key == "" || len(key) > 191 || len(id) > 191 {
				continue
			}
			pair := [2]string{id, key}
			if previous, ok := keys[pair]; !ok || created > previous.at {
				keys[pair] = seen{at: created}
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return 0, err
		}
		if count < 500 {
			break
		}
	}
	pairs := make([][2]string, 0, len(keys))
	for pair := range keys {
		pairs = append(pairs, pair)
	}
	slices.SortFunc(pairs, func(a, b [2]string) int {
		if a[0] != b[0] {
			if a[0] < b[0] {
				return -1
			}
			return 1
		}
		if a[1] < b[1] {
			return -1
		}
		if a[1] > b[1] {
			return 1
		}
		return 0
	})
	recorded := 0
	for _, pair := range pairs {
		current, isLive := live[pair[0]]
		if isLive && current == pair[1] {
			continue
		}
		at := keys[pair].at
		if _, err := time.Parse(time.RFC3339Nano, at); err != nil {
			at = time.Now().UTC().Format(time.RFC3339Nano)
		}
		result, err := s.db.ExecContext(ctx, `INSERT INTO workspace_key_renames(workspace_id,old_key,new_key,renamed_at) VALUES(?,?,?,?) ON CONFLICT DO NOTHING`, pair[0], pair[1], current, at)
		if err != nil {
			return recorded, err
		}
		recorded += int(rowsAffected(result))
	}
	return recorded, nil
}

// recoverWorkspaceContent copies to workspace the offloaded bodies its
// records refer to (records) that it does not hold (present) but a key no
// workspace has does. records and present are read when nil. It returns how
// many bodies it copied.
func (s *SQLiteStore) recoverWorkspaceContent(ctx context.Context, workspace string, records, present map[string]bool) (int, error) {
	if s.db.content == nil {
		return 0, nil
	}
	if records == nil {
		var err error
		if records, err = s.offloadedRecordReferences(ctx, workspace); err != nil {
			return 0, err
		}
	}
	if len(records) == 0 {
		return 0, nil
	}
	if present == nil {
		keys, err := s.workspaceStrings(ctx, `SELECT blob_key FROM metadata_blobs WHERE workspace_key=?`, workspace)
		if err != nil {
			return 0, err
		}
		present = map[string]bool{}
		for _, key := range keys {
			present[key] = true
		}
	}
	missing := []string{}
	for key := range records {
		if !present[key] {
			missing = append(missing, key)
		}
	}
	if len(missing) == 0 {
		return 0, nil
	}
	slices.Sort(missing)
	recovered := 0
	for start := 0; start < len(missing); start += 100 {
		clause, args := bindList("blob_key", missing[start:min(start+100, len(missing))])
		rows, err := s.db.QueryContext(ctx, `SELECT workspace_key,blob_key,size,data,created_at FROM metadata_blobs WHERE workspace_key<>? AND `+clause+` ORDER BY blob_key,workspace_key`, append([]any{workspace}, args...)...)
		if err != nil {
			return recovered, err
		}
		type source struct {
			key, blob string
			size      int64
			data      []byte
			created   int64
		}
		sources := []source{}
		for rows.Next() {
			var item source
			if err := rows.Scan(&item.key, &item.blob, &item.size, &item.data, &item.created); err != nil {
				rows.Close()
				return recovered, err
			}
			sources = append(sources, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return recovered, err
		}
		done := map[string]bool{}
		for _, item := range sources {
			if done[item.blob] || s.HasWorkspace(item.key) {
				// A live workspace's body stays its own.
				continue
			}
			if item.data == nil {
				objects := s.db.content.objects
				if objects == nil {
					continue
				}
				reader, _, _, err := objects.Open(ctx, offloadedObjectKey(item.key, item.blob))
				if err != nil {
					continue
				}
				body, err := io.ReadAll(reader)
				reader.Close()
				if err != nil {
					return recovered, err
				}
				if _, err := objects.Put(ctx, offloadedObjectKey(workspace, item.blob), bytes.NewReader(body), "application/gzip"); err != nil {
					return recovered, fmt.Errorf("copy offloaded content: %w", err)
				}
			}
			var data any
			if item.data != nil {
				data = item.data
			}
			result, err := s.db.ExecContext(ctx, `INSERT INTO metadata_blobs(workspace_key,blob_key,size,data,created_at,orphaned_at) VALUES(?,?,?,?,?,0) ON CONFLICT DO NOTHING`, workspace, item.blob, item.size, data, item.created)
			if err != nil {
				return recovered, err
			}
			if rowsAffected(result) > 0 {
				recovered++
			}
			done[item.blob] = true
		}
	}
	if recovered > 0 {
		slog.Info("recovered offloaded bodies stored under a previous workspace key", "workspace", workspace, "bodies", recovered)
	}
	return recovered, nil
}

// offloadedRecordReferences lists the body keys a workspace's documents,
// trash entries and agent conversations refer to.
func (s *SQLiteStore) offloadedRecordReferences(ctx context.Context, workspace string) (map[string]bool, error) {
	referenced := map[string]bool{}
	last := ""
	for {
		rows, err := s.db.QueryContext(ctx, `SELECT record_key,data FROM workspace_metadata_records WHERE workspace_key=? AND field IN ('documents','trash','agentSessions') AND record_key>? ORDER BY record_key LIMIT 64`, workspace, last)
		if err != nil {
			return nil, err
		}
		count := 0
		for rows.Next() {
			var data []byte
			if err := rows.Scan(&last, &data); err != nil {
				rows.Close()
				return nil, err
			}
			offloadedKeys(data, referenced)
			count++
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		if count < 64 {
			return referenced, nil
		}
	}
}
