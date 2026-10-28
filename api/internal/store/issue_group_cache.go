package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"time"
)

// Group counts of issue lists are read on every list open and refresh, and a
// nested count of a large result walks its sub-issues. The server keeps recent
// counts in process, keyed by the whole query (the viewer's access included)
// and the workspace's issue revision: a counter in the database that every
// transaction writing issue records, their label, attribute and permission
// indexes, or the customer request index advances (writeIssueStats,
// writeCustomerFilterRecord), as its last statement. Reading it is one
// primary-key lookup, and every server sees a write once it commits, so a
// cached count is never served after the data it counts changed; the TTL only
// bounds memory. A reader reads the revision before it counts: a write that
// commits in between leaves its counts under the older revision, which no
// later reader asks for.

const (
	issueGroupCountTTL     = 30 * time.Second
	issueGroupCountEntries = 1024
)

type issueGroupCountCache struct {
	mu      sync.Mutex
	entries map[[32]byte]issueGroupCountEntry
}

type issueGroupCountEntry struct {
	workspace string
	groups    []IssueRecordGroup
	expires   time.Time
}

func (c *issueGroupCountCache) get(key [32]byte, now time.Time) ([]IssueRecordGroup, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || now.After(entry.expires) {
		return nil, false
	}
	return slices.Clone(entry.groups), true
}

func (c *issueGroupCountCache) put(key [32]byte, workspace string, groups []IssueRecordGroup, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[[32]byte]issueGroupCountEntry{}
	}
	if len(c.entries) >= issueGroupCountEntries {
		for key, entry := range c.entries {
			if now.After(entry.expires) {
				delete(c.entries, key)
			}
		}
		// Still full: drop an arbitrary entry.
		for key := range c.entries {
			if len(c.entries) < issueGroupCountEntries {
				break
			}
			delete(c.entries, key)
		}
	}
	c.entries[key] = issueGroupCountEntry{workspace: workspace, groups: slices.Clone(groups), expires: now.Add(issueGroupCountTTL)}
}

// drop forgets a workspace's counts (its revision restarts when it is deleted).
func (c *issueGroupCountCache) drop(workspace string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, entry := range c.entries {
		if entry.workspace == workspace {
			delete(c.entries, key)
		}
	}
}

// issueGroupCountKey identifies a count: the query as resolved (release scope
// applied, group selection cleared) with the viewer's access, at a revision.
func issueGroupCountKey(query IssueRecordQuery, revision int64) [32]byte {
	// The query's encoding carries its Access (user, teams, roles).
	raw, _ := json.Marshal(struct {
		Query    IssueRecordQuery
		Revision int64
	}{query, revision})
	return sha256.Sum256(raw)
}

// issueCountRevision reads the workspace's issue revision (0 before its first
// issue write).
func (s *SQLiteStore) issueCountRevision(ctx context.Context, workspace string) (int64, error) {
	var revision int64
	err := s.db.QueryRowContext(ctx, `SELECT revision FROM issue_count_revisions WHERE workspace_key=?`, workspace).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return revision, err
}

// queueIssueCountRevision advances the workspace's issue revision when the
// writing transaction commits, so the new data and its revision commit
// together. Every writer in the workspace updates the same revision row, so
// it is the transaction's last statement (flushIssueCounts): writers queue on
// it only while its holder commits, and a holder waits for nothing else, so
// the row cannot take part in a deadlock.
func queueIssueCountRevision(ctx context.Context, tx *sqlTx, workspace string) {
	pending := tx.pendingIssueCounts(ctx)
	pending.revisions[workspace] = true
}

type pendingIssueCounts struct {
	ctx       context.Context
	revisions map[string]bool
	// deltas: counter deltas by workspace (writeIssueStats).
	deltas map[string]map[issueStatsKey]int64
}

func (tx *sqlTx) pendingIssueCounts(ctx context.Context) *pendingIssueCounts {
	if tx.issueCounts == nil {
		tx.issueCounts = &pendingIssueCounts{ctx: ctx, revisions: map[string]bool{}, deltas: map[string]map[issueStatsKey]int64{}}
	}
	return tx.issueCounts
}

// flushIssueCounts writes what the transaction queued; Commit calls it just
// before committing.
func (tx *sqlTx) flushIssueCounts() error {
	pending := tx.issueCounts
	if pending == nil {
		return nil
	}
	tx.issueCounts = nil
	workspaces := make([]string, 0, len(pending.revisions))
	for workspace := range pending.revisions {
		workspaces = append(workspaces, workspace)
	}
	slices.Sort(workspaces)
	for _, workspace := range workspaces {
		if err := applyIssueStats(pending.ctx, tx, workspace, pending.deltas[workspace]); err != nil {
			return err
		}
	}
	for _, workspace := range workspaces {
		if _, err := tx.ExecContext(pending.ctx, `INSERT INTO issue_count_revisions(workspace_key,revision) VALUES(?,1) ON CONFLICT(workspace_key) DO UPDATE SET revision=issue_count_revisions.revision+1`, workspace); err != nil {
			return err
		}
	}
	return nil
}
