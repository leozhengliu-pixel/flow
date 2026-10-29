package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"slices"
	"sync"
	"time"
)

// Group counts of issue lists are read on every list open and refresh, and a
// nested count of a large result walks its sub-issues. The server keeps recent
// counts in process, keyed by the whole query (the viewer's access included)
// and the workspace's issue revision: a counter in the database that every
// transaction writing issue records, their label, attribute and permission
// indexes, or customer/release filter indexes advances. Cache-invalidating
// events also advance it when no issue record changes, among the transaction's
// last statements (cache_revisions.go).
// Reading it is one indexed read, and every server sees a write once it
// commits, so a cached count is never served after the data it counts
// changed; the TTL only bounds memory. A reader reads the revision before it counts: a write that
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
// issue write; cache_revisions.go).
func (s *SQLiteStore) issueCountRevision(ctx context.Context, workspace string) (int64, error) {
	return s.cacheRevision(ctx, s.db, workspace, cacheKindIssues)
}

// queueIssueCountRevision advances the workspace's issue revision when the
// writing transaction commits, so the new data and its revision commit
// together. Every writer in the workspace advances it, so it is among the
// transaction's last statements (sqlTx.Commit) and split into shards
// (cache_revisions.go): writers rarely share a row, and one that does waits
// only while its holder commits, which waits for nothing else then.
func queueIssueCountRevision(ctx context.Context, tx *sqlTx, workspace string) {
	pending := tx.pendingIssueCounts(ctx)
	pending.revisions[workspace] = true
	tx.queueCacheRevision(workspace, cacheKindIssues)
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

// flushIssueCounts writes the queued counter deltas; Commit calls it just
// before it advances the revisions (flushCacheRevisions) and commits.
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
	return nil
}
