package store

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"flow/api/internal/domain"
)

const cacheKindSearchIndex = "search-index"

type searchMetadataCache struct {
	store                  *SQLiteStore
	cache                  sharedResultCache
	workspace, workspaceID string
	material               string
	stamp                  searchMetadataStamp
}

type searchMetadataStamp struct {
	generation    string
	epoch, repair int64
	versions      map[string]int64
}

func (s *SQLiteStore) newSearchMetadataCache(ctx context.Context, policy domain.Bootstrap, q SearchMetadataQuery) searchMetadataCache {
	cache := s.sharedResults(ctx)
	if cache == nil || !s.RecordJournalReady() || q.Scope.Workspace == "" || policy.Workspace.ID == "" || len(q.Terms) == 0 || q.SkipSharedCache || q.Scope.Access == nil {
		return searchMetadataCache{}
	}
	fields := make([]string, 0, len(searchMetadataKinds))
	for _, kind := range searchMetadataKinds {
		if q.Types[kind.kind] && kind.kind != "release" {
			fields = append(fields, kind.field)
		}
	}
	if len(fields) == 0 {
		return searchMetadataCache{}
	}
	if !q.Types["project"] && (q.Types["initiative"] || q.Types["view"]) {
		fields = append(fields, "projects")
	}
	stamp := s.readSearchMetadataStamp(ctx, q.Scope.Workspace, policy.Workspace.ID, fields)
	if stamp.generation == "" {
		return searchMetadataCache{}
	}
	// Include the actual in-memory input as well as the live epoch. A request
	// may have loaded its team policy just before a concurrent update.
	members := make(map[string]bool, len(policy.TeamMembers))
	for _, member := range policy.TeamMembers {
		if member.UserID == policy.Viewer.ID {
			members[member.TeamID] = true
		}
	}
	raw, err := json.Marshal(struct {
		Viewer, Role, Credential string
		Members                  map[string]bool
		Teams                    []domain.Team
		Settings                 map[string]domain.TeamSettings
		Scope                    IssueRecordQuery
		Terms                    []string
		Limit                    int
	}{policy.Viewer.ID, policy.ViewerRole, q.CredentialID, members, policy.Teams, policy.TeamSettings, q.Scope, q.Terms, q.Limit})
	if err != nil {
		return searchMetadataCache{}
	}
	return searchMetadataCache{s, cache, q.Scope.Workspace, policy.Workspace.ID, string(raw), stamp}
}

func (c searchMetadataCache) read(ctx context.Context, kind searchMetadataKind, prepare func(context.Context) (context.Context, error), compute func(context.Context) (searchMetadataProjection, error)) (searchMetadataProjection, error) {
	var sourceCtx context.Context
	fill := func(ctx context.Context) (searchMetadataProjection, error) {
		var err error
		sourceCtx, err = prepare(ctx)
		if err != nil {
			return searchMetadataProjection{}, err
		}
		return compute(sourceCtx)
	}
	// Releases depend on live issue visibility and pipeline records as well.
	if c.cache == nil || kind.kind == "release" {
		return fill(ctx)
	}
	fields := []string{kind.field}
	if kind.kind == "initiative" || kind.kind == "view" {
		fields = append(fields, "projects")
	}
	version := c.stamp.version(fields)
	key := c.cache.SharedCacheKey(c.workspace, "search-meta", version, c.workspaceID, c.stamp.generation, c.store.dialect, strconv.Itoa(c.store.db.searchMaxChars), kind.kind, c.material)
	return sharedJSONResult(ctx, c.cache, key, fill, func(context.Context) bool {
		// Check the version against the rows' snapshot. A batched MySQL
		// fill already holds a connection until the search ends.
		current := c.store.readSearchMetadataStamp(sourceCtx, c.workspace, c.workspaceID, fields)
		return current.generation == c.stamp.generation && current.version(fields) == version
	})
}

func (stamp searchMetadataStamp) version(fields []string) string {
	parts := []string{"2", strconv.FormatInt(stamp.epoch, 10), strconv.FormatInt(stamp.repair, 10)}
	for _, field := range fields {
		parts = append(parts, strconv.FormatInt(stamp.versions[field], 10))
	}
	return strings.Join(parts, ".")
}

// Versions (including tombstones) move with the canonical rows. Index-only
// repairs move a separate revision in the transaction that rewrites the index.
func (s *SQLiteStore) readSearchMetadataStamp(ctx context.Context, workspace, workspaceID string, fields []string) searchMetadataStamp {
	var stamp searchMetadataStamp
	columns := []string{
		"m.generation",
		"COALESCE((SELECT a.epoch FROM workspace_authz_epochs a WHERE a.workspace_id=" + recordJournalIdentitySQL(s.dialect, "w.workspace_id") + "),0)",
		"(SELECT COALESCE(SUM(r.revision),0) FROM workspace_cache_revisions r WHERE r.workspace_key=? AND r.kind='search-index')",
	}
	args := []any{workspace}
	sums := make([]int64, len(fields))
	dest := []any{&stamp.generation, &stamp.epoch, &stamp.repair}
	for i, field := range fields {
		columns = append(columns, "(SELECT COALESCE(SUM(v.version),0) FROM record_versions v WHERE v.workspace_key=? AND v.field=?)")
		args = append(args, workspace, field)
		dest = append(dest, &sums[i])
	}
	args = append(args, workspace, workspaceID)
	if err := s.db.QueryRowContext(ctx, "SELECT "+strings.Join(columns, ",")+" FROM workspace_states w CROSS JOIN record_journal_mode m WHERE w.workspace_key=? AND w.workspace_id=? AND m.id=1 AND m.enabled=1", args...).Scan(dest...); err != nil {
		return searchMetadataStamp{}
	}
	stamp.versions = make(map[string]int64, len(fields))
	for i, field := range fields {
		stamp.versions[field] = sums[i]
	}
	return stamp
}
