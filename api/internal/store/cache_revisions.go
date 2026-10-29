package store

import (
	"context"
	"math/rand/v2"
	"regexp"
	"slices"
	"strings"
)

// A workspace cache revision counts the committed transactions that changed
// one class of the workspace's data. It lives in the database and moves on in
// the writing transaction, so a reader that reads it sees exactly the writes
// committed before its read, on whatever server they ran. The shared caches
// (hot_cache.go, issue_group_cache.go) key or stamp their entries with it: a
// write is never served from an entry made before it, even in the moment
// between its commit and the writer dropping the entries it retired.
//
// Every writer of the class bumps it, so it is split into shards: a
// transaction adds one to a random shard as one of its last statements
// (sqlTx.Commit), and the revision is the shards' sum, read in one statement.
// Writers then rarely wait for each other on it. Snapshots taken later see a
// superset of the transactions an earlier one saw, and every transaction adds
// one, so two reads return the same sum only when they saw the same writes.
const (
	// cacheKindIssues: issue records and their indexes (writeIssueStats,
	// writeCustomerFilterRecord). The issue query page and group count
	// caches are keyed by it.
	cacheKindIssues = "issues"
	// cacheKindMetadata: the workspace document and its metadata records
	// (any statement metadataWriteWorkspace recognises). The metadata record
	// cache and the project directory cache are keyed by it.
	cacheKindMetadata = "metadata"
)

const cacheRevisionShards = 16

type cacheRevisionKey struct {
	workspace, kind string
}

func (s *SQLiteStore) createCacheRevisionSchema(ctx context.Context) error {
	return s.applyMigrationStatements(ctx, []string{
		`CREATE TABLE IF NOT EXISTS workspace_cache_revisions (workspace_key VARCHAR(191) NOT NULL, kind VARCHAR(32) NOT NULL, shard INTEGER NOT NULL, revision BIGINT NOT NULL, PRIMARY KEY(workspace_key,kind,shard))`,
	})
}

// queueCacheRevision advances the workspace's revision of kind when the
// transaction commits.
func (tx *sqlTx) queueCacheRevision(workspace, kind string) {
	if workspace == "" {
		return
	}
	if tx.cacheRevisions == nil {
		tx.cacheRevisions = map[cacheRevisionKey]bool{}
	}
	tx.cacheRevisions[cacheRevisionKey{workspace, kind}] = true
}

// Event-driven invalidation must retire the same caches inside the write,
// including discussion and release changes that write no issue record.
func (tx *sqlTx) queueEventCacheRevisions(ctx context.Context, row domainEventRow) error {
	_, issues, projects, metadata := hotCacheTouch(row.Type)
	if !issues && !projects && !metadata || row.WorkspaceKey == "" && row.WorkspaceID == "" {
		return nil
	}
	workspace := row.WorkspaceKey
	if workspace == "" {
		if err := tx.QueryRowContext(ctx, `SELECT workspace_key FROM workspace_states WHERE workspace_id=?`, row.WorkspaceID).Scan(&workspace); err != nil {
			return err
		}
	}
	if issues {
		queueIssueCountRevision(ctx, tx, workspace)
	}
	if projects || metadata {
		tx.queueCacheRevision(workspace, cacheKindMetadata)
	}
	return nil
}

// flushCacheRevisions writes the queued revisions, in key order so two
// writers never take shards in opposite orders. A shard row is updated in
// place once it exists.
func (tx *sqlTx) flushCacheRevisions() error {
	if len(tx.cacheRevisions) == 0 {
		return nil
	}
	keys := make([]cacheRevisionKey, 0, len(tx.cacheRevisions))
	for key := range tx.cacheRevisions {
		keys = append(keys, key)
	}
	tx.cacheRevisions = nil
	slices.SortFunc(keys, func(a, b cacheRevisionKey) int {
		if c := strings.Compare(a.workspace, b.workspace); c != 0 {
			return c
		}
		return strings.Compare(a.kind, b.kind)
	})
	ctx := tx.context()
	for _, key := range keys {
		shard := rand.IntN(cacheRevisionShards)
		result, err := tx.Tx.ExecContext(ctx, rewriteSQL(`UPDATE workspace_cache_revisions SET revision=revision+1 WHERE workspace_key=? AND kind=? AND shard=?`, tx.dialect), key.workspace, key.kind, shard)
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil || n > 0 {
			if err != nil {
				return err
			}
			continue
		}
		// The upsert locks a row another writer created meanwhile
		// exclusively at once (an insert that ignores the conflict and
		// updates after would hold a shared lock first, which two writers
		// can deadlock on).
		if _, err := tx.Tx.ExecContext(ctx, rewriteSQL(`INSERT INTO workspace_cache_revisions(workspace_key,kind,shard,revision) VALUES(?,?,?,1) ON CONFLICT(workspace_key,kind,shard) DO UPDATE SET revision=workspace_cache_revisions.revision+1`, tx.dialect), key.workspace, key.kind, shard); err != nil {
			return err
		}
	}
	return nil
}

// cacheRevision reads the workspace's revision of kind through reader (a
// read snapshot, or the database). The issue revision also counts the single
// row older servers advance (issue_count_revisions), so writes from servers
// of either version retire the caches during a rolling upgrade.
func (s *SQLiteStore) cacheRevision(ctx context.Context, reader metadataReader, workspace, kind string) (int64, error) {
	query := `SELECT revision FROM workspace_cache_revisions WHERE workspace_key=? AND kind=?`
	args := []any{workspace, kind}
	if kind == cacheKindIssues {
		query += ` UNION ALL SELECT revision FROM issue_count_revisions WHERE workspace_key=?`
		args = append(args, workspace)
	}
	rows, err := reader.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var sum int64
	for rows.Next() {
		var revision int64
		if err := rows.Scan(&revision); err != nil {
			return 0, err
		}
		sum += revision
	}
	return sum, rows.Err()
}

// metadataWriteStatement matches a statement that writes the workspace
// document or its metadata records.
var metadataWriteStatement = regexp.MustCompile(`(?is)^\s*(?:INSERT\s+(?:IGNORE\s+)?INTO\s+(?:workspace_metadata_records|workspace_states)\s*\(\s*([a-z_]+)|(?:UPDATE|DELETE\s+FROM)\s+(?:workspace_metadata_records|workspace_states)\b)`)

var workspaceKeyPredicate = regexp.MustCompile(`(?i)\bworkspace_key\s*=\s*\?`)

// metadataWriteWorkspace reports the workspace a statement writes metadata
// of. Every write of the workspace document or its metadata records goes
// through a transaction's ExecContext, which queues the workspace's metadata
// revision here, so no write path can forget to retire the metadata caches.
// The workspace is the first bound value of an insert whose first column is
// workspace_key, or the value bound to "workspace_key=?" in an update's or
// delete's condition.
func metadataWriteWorkspace(query string, args []any) (string, bool) {
	if !strings.Contains(query, "workspace_metadata_records") && !strings.Contains(query, "workspace_states") {
		return "", false
	}
	match := metadataWriteStatement.FindStringSubmatchIndex(query)
	if match == nil {
		return "", false
	}
	index := -1
	if match[2] >= 0 {
		if strings.EqualFold(query[match[2]:match[3]], "workspace_key") {
			index = 0
		}
	} else if predicate := workspaceKeyPredicate.FindStringIndex(query); predicate != nil {
		index = strings.Count(query[:predicate[1]], "?") - 1
	}
	if index < 0 || index >= len(args) {
		return "", false
	}
	workspace, ok := args[index].(string)
	return workspace, ok && workspace != ""
}
