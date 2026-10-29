package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
)

type hotCache interface {
	CacheGet(ctx context.Context, key string) ([]byte, error)
	CacheSet(ctx context.Context, key string, value []byte, ttl time.Duration) error
	CacheDel(ctx context.Context, keys ...string) error
	CacheIncr(ctx context.Context, key string) (int64, error)
	CacheHGetAll(ctx context.Context, key string) (map[string]string, error)
	CacheHSet(ctx context.Context, key, field string, value []byte) error
	CacheHSetMap(ctx context.Context, key string, fields map[string][]byte) error
	CacheHDel(ctx context.Context, key string, fields ...string) error
	CacheKey(workspace, kind, suffix string) string
}

func (s *SQLiteStore) hotCache() hotCache {
	cache, _ := s.coordinator.(hotCache)
	return cache
}

func (s *SQLiteStore) issueQueryGenKey(workspace string) string {
	if cache := s.hotCache(); cache != nil {
		return cache.CacheKey(workspace, "igen", "")
	}
	return ""
}

func (s *SQLiteStore) projectQueryGenKey(workspace string) string {
	if cache := s.hotCache(); cache != nil {
		return cache.CacheKey(workspace, "pgen", "")
	}
	return ""
}

func (s *SQLiteStore) metadataReadyKey(workspace string) string {
	if cache := s.hotCache(); cache != nil {
		return cache.CacheKey(workspace, "mrok", "")
	}
	return ""
}

func (s *SQLiteStore) metadataFieldKey(workspace, field string) string {
	if cache := s.hotCache(); cache != nil {
		return cache.CacheKey(workspace, "mr", field)
	}
	return ""
}

func (s *SQLiteStore) cacheGetIssue(ctx context.Context, workspace, id string) ([]byte, bool) {
	cache := s.hotCache()
	if cache == nil || workspace == "" || id == "" {
		return nil, false
	}
	raw, err := cache.CacheGet(ctx, cache.CacheKey(workspace, "issue", id))
	if err != nil || len(raw) == 0 {
		return nil, false
	}
	return raw, true
}

func (s *SQLiteStore) cacheSetIssue(ctx context.Context, workspace, id string, raw []byte) {
	cache := s.hotCache()
	if cache == nil || workspace == "" || len(raw) == 0 {
		return
	}
	keys := []string{}
	if id != "" {
		keys = append(keys, id)
	}
	if issue, err := unmarshalIssueRecord(raw); err == nil {
		if issue.ID != "" {
			keys = append(keys, issue.ID)
		}
		if issue.Identifier != "" {
			keys = append(keys, issue.Identifier)
		}
	}
	seen := map[string]bool{}
	for _, key := range keys {
		if seen[key] {
			continue
		}
		seen[key] = true
		_ = cache.CacheSet(ctx, cache.CacheKey(workspace, "issue", key), raw, 15*time.Minute)
	}
}

func (s *SQLiteStore) cacheDropIssue(ctx context.Context, workspace, id string) {
	cache := s.hotCache()
	if cache == nil || workspace == "" || id == "" {
		return
	}
	keys := []string{cache.CacheKey(workspace, "issue", id)}
	raw, err := cache.CacheGet(ctx, keys[0])
	if err != nil || len(raw) == 0 {
		raw, _ = cache.CacheGet(ctx, cache.CacheKey(workspace, "issue", strings.ToUpper(id)))
	}
	if len(raw) > 0 {
		if issue, err := unmarshalIssueRecord(raw); err == nil {
			if issue.ID != "" {
				keys = append(keys, cache.CacheKey(workspace, "issue", issue.ID))
			}
			if issue.Identifier != "" {
				keys = append(keys, cache.CacheKey(workspace, "issue", issue.Identifier))
			}
		}
	}
	_ = cache.CacheDel(ctx, keys...)
}

func unmarshalIssueRecord(raw []byte) (domain.Issue, error) {
	var issue domain.Issue
	err := json.Unmarshal(raw, &issue)
	if err == nil {
		normalizeIssueRecord(&issue)
	}
	return issue, err
}

func (s *SQLiteStore) cacheGeneration(ctx context.Context, key string) int64 {
	cache := s.hotCache()
	if cache == nil || key == "" {
		return 0
	}
	raw, err := cache.CacheGet(ctx, key)
	if err != nil || len(raw) == 0 {
		return 0
	}
	n, _ := strconv.ParseInt(string(raw), 10, 64)
	return n
}

// The query page, group count and project directory caches are keyed by a
// workspace cache revision (cache_revisions.go) as well as the Redis
// generation writers advance after they commit. The revision moves on inside
// the writing transaction, so a reader that reads it after a write committed
// never uses an entry made before the write, even in the moment before the
// writer advances the generation. Explicit invalidation also retires entries
// through the generation. A reader that misses stores what it then
// reads under the key it missed, read before it read the data: if a write
// commits meanwhile, the result (which may predate it) lands under a key
// readers after the write no longer use.

// issueQueryScope is what identifies an issue query in a cache key: the query
// without its access, which is named by user, role and visible teams.
func issueQueryScope(query IssueRecordQuery, gen, revision int64) []byte {
	scope := query
	scope.Access = nil
	raw, _ := json.Marshal(struct {
		Query    IssueRecordQuery `json:"q"`
		User     string           `json:"u,omitempty"`
		Admin    bool             `json:"a,omitempty"`
		Teams    []string         `json:"t,omitempty"`
		Gen      int64            `json:"g"`
		Revision int64            `json:"r"`
	}{Query: scope, User: accessUser(query.Access), Admin: accessAdmin(query.Access), Teams: accessTeams(query.Access), Gen: gen, Revision: revision})
	return raw
}

// issueQueryCacheKey is the page cache key of query at the workspace's issue
// revision; "" when there is no shared cache.
func (s *SQLiteStore) issueQueryCacheKey(ctx context.Context, query IssueRecordQuery, revision int64) string {
	cache := s.hotCache()
	if cache == nil {
		return ""
	}
	gen := s.cacheGeneration(ctx, s.issueQueryGenKey(query.Workspace))
	sum := sha256.Sum256(issueQueryScope(query, gen, revision))
	return cache.CacheKey(query.Workspace, "iq", fmt.Sprintf("%d:%d:%x", gen, revision, sum[:16]))
}

func accessUser(access *IssueRecordAccess) string {
	if access == nil {
		return ""
	}
	return access.UserID
}

func accessAdmin(access *IssueRecordAccess) bool {
	return access != nil && access.Admin
}

func accessTeams(access *IssueRecordAccess) []string {
	if access == nil {
		return nil
	}
	return access.VisibleTeamIDs
}

// cacheGetIssueQuery returns a cached page, or on a miss the key to store
// the page read next under (cacheSetIssueQuery).
func (s *SQLiteStore) cacheGetIssueQuery(ctx context.Context, query IssueRecordQuery) (IssueRecordPage, string, bool) {
	cache := s.hotCache()
	if cache == nil {
		return IssueRecordPage{}, "", false
	}
	revision, err := s.issueCountRevision(ctx, query.Workspace)
	if err != nil {
		return IssueRecordPage{}, "", false
	}
	key := s.issueQueryCacheKey(ctx, query, revision)
	if key == "" {
		return IssueRecordPage{}, "", false
	}
	raw, err := cache.CacheGet(ctx, key)
	if err != nil || len(raw) == 0 {
		return IssueRecordPage{}, key, false
	}
	var page IssueRecordPage
	if json.Unmarshal(raw, &page) != nil {
		return IssueRecordPage{}, key, false
	}
	if page.Items == nil {
		page.Items = []domain.Issue{}
	}
	return page, key, true
}

// issueGroupCacheKey is the group count cache key of query at the
// workspace's issue revision; "" when there is no shared cache.
func (s *SQLiteStore) issueGroupCacheKey(ctx context.Context, query IssueRecordQuery, revision int64) string {
	cache := s.hotCache()
	if cache == nil {
		return ""
	}
	gen := s.cacheGeneration(ctx, s.issueQueryGenKey(query.Workspace))
	sum := sha256.Sum256(issueQueryScope(query, gen, revision))
	return cache.CacheKey(query.Workspace, "ig", fmt.Sprintf("%d:%d:%x", gen, revision, sum[:16]))
}

// cacheGetIssueGroups returns cached counts of query at the workspace's issue
// revision, or on a miss the key to store the counts read next under
// (cacheSetIssueGroups).
func (s *SQLiteStore) cacheGetIssueGroups(ctx context.Context, query IssueRecordQuery, revision int64) ([]IssueRecordGroup, string, bool) {
	cache := s.hotCache()
	key := s.issueGroupCacheKey(ctx, query, revision)
	if cache == nil || key == "" {
		return nil, "", false
	}
	raw, err := cache.CacheGet(ctx, key)
	if err != nil || len(raw) == 0 {
		return nil, key, false
	}
	var groups []IssueRecordGroup
	if json.Unmarshal(raw, &groups) != nil {
		return nil, key, false
	}
	if groups == nil {
		groups = []IssueRecordGroup{}
	}
	return groups, key, true
}

func (s *SQLiteStore) cacheSetIssueGroups(ctx context.Context, key string, groups []IssueRecordGroup) {
	cache := s.hotCache()
	if cache == nil || key == "" {
		return
	}
	raw, err := json.Marshal(groups)
	if err != nil {
		return
	}
	_ = cache.CacheSet(ctx, key, raw, 60*time.Second)
}

func (s *SQLiteStore) cacheSetIssueQuery(ctx context.Context, key string, page IssueRecordPage) {
	cache := s.hotCache()
	if cache == nil || key == "" {
		return
	}
	raw, err := json.Marshal(page)
	if err != nil {
		return
	}
	_ = cache.CacheSet(ctx, key, raw, 60*time.Second)
}

// projectQueryCacheKey is the project directory cache key of query at the
// workspace's metadata revision (projects are metadata records).
func (s *SQLiteStore) projectQueryCacheKey(ctx context.Context, query ProjectRecordQuery, revision int64) string {
	cache := s.hotCache()
	if cache == nil {
		return ""
	}
	gen := s.cacheGeneration(ctx, s.projectQueryGenKey(query.Workspace))
	raw, _ := json.Marshal(struct {
		Query    ProjectRecordQuery `json:"q"`
		Gen      int64              `json:"g"`
		Revision int64              `json:"r"`
	}{Query: query, Gen: gen, Revision: revision})
	sum := sha256.Sum256(raw)
	return cache.CacheKey(query.Workspace, "pq", fmt.Sprintf("%d:%d:%x", gen, revision, sum[:16]))
}

// cacheGetProjectQuery returns a cached directory page, or on a miss the key
// to store the page read next under (cacheSetProjectQuery).
func (s *SQLiteStore) cacheGetProjectQuery(ctx context.Context, query ProjectRecordQuery) (ProjectRecordPage, string, bool) {
	cache := s.hotCache()
	if cache == nil {
		return ProjectRecordPage{}, "", false
	}
	revision, err := s.cacheRevision(ctx, s.db, query.Workspace, cacheKindMetadata)
	if err != nil {
		return ProjectRecordPage{}, "", false
	}
	key := s.projectQueryCacheKey(ctx, query, revision)
	if key == "" {
		return ProjectRecordPage{}, "", false
	}
	raw, err := cache.CacheGet(ctx, key)
	if err != nil || len(raw) == 0 {
		return ProjectRecordPage{}, key, false
	}
	var page ProjectRecordPage
	if json.Unmarshal(raw, &page) != nil {
		return ProjectRecordPage{}, key, false
	}
	if page.Items == nil {
		page.Items = []domain.Project{}
	}
	return page, key, true
}

func (s *SQLiteStore) cacheSetProjectQuery(ctx context.Context, key string, page ProjectRecordPage) {
	cache := s.hotCache()
	if cache == nil || key == "" {
		return
	}
	raw, err := json.Marshal(page)
	if err != nil {
		return
	}
	_ = cache.CacheSet(ctx, key, raw, 60*time.Second)
}

type metadataCacheRow struct {
	Field string
	Key   string
	Order int
	Data  []byte
}

func encodeMetadataCacheValue(order int, data []byte) []byte {
	return []byte(strconv.Itoa(order) + "\x00" + string(data))
}

func decodeMetadataCacheValue(raw string) (int, []byte) {
	order, rest, ok := strings.Cut(raw, "\x00")
	if !ok {
		return 0, []byte(raw)
	}
	n, _ := strconv.Atoi(order)
	return n, []byte(rest)
}

// The metadata record cache holds a copy of the workspace's metadata rows,
// stamped with the workspace's metadata revision (cache_revisions.go) and
// metadata generation it was read at. The revision moves on inside every
// transaction that writes the workspace document or a metadata record, so a
// reader whose read (or read snapshot) sees a write never uses a copy read
// before it, even before the writer drops the copy after commit. The
// generation, which the writer moves on when it drops the copy, retires a
// copy a filler that read before the drop is still writing (it would
// otherwise hold some fields from before the write and lack the ones the drop
// removed).
func (s *SQLiteStore) metadataGenerationKey(workspace string) string {
	if cache := s.hotCache(); cache != nil {
		return cache.CacheKey(workspace, "mgen", "")
	}
	return ""
}

// metadataCacheGeneration is the generation a metadata cache fill starts at.
func (s *SQLiteStore) metadataCacheGeneration(ctx context.Context, workspace string) int64 {
	return s.cacheGeneration(ctx, s.metadataGenerationKey(workspace))
}

// metadataCacheStamp identifies the metadata a copy holds.
type metadataCacheStamp struct {
	generation, revision int64
}

func (stamp metadataCacheStamp) String() string {
	return strconv.FormatInt(stamp.generation, 10) + ":" + strconv.FormatInt(stamp.revision, 10)
}

// metadataStamp is the stamp of the metadata reader sees: the current
// generation and the revision in reader's snapshot. Read it before the rows.
func (s *SQLiteStore) metadataStamp(ctx context.Context, reader metadataReader, workspace string) (metadataCacheStamp, bool) {
	if s.hotCache() == nil || workspace == "" {
		return metadataCacheStamp{}, false
	}
	generation := s.metadataCacheGeneration(ctx, workspace)
	revision, err := s.cacheRevision(ctx, reader, workspace, cacheKindMetadata)
	return metadataCacheStamp{generation, revision}, err == nil
}

func (s *SQLiteStore) metadataRecordsFromCache(ctx context.Context, workspace string, stamp metadataCacheStamp, fields map[string]string) ([]metadataCacheRow, bool) {
	cache := s.hotCache()
	if cache == nil || workspace == "" || len(fields) == 0 {
		return nil, false
	}
	ready, err := cache.CacheGet(ctx, s.metadataReadyKey(workspace))
	if err != nil || string(ready) != stamp.String() {
		return nil, false
	}
	rows := []metadataCacheRow{}
	for field := range fields {
		values, err := cache.CacheHGetAll(ctx, s.metadataFieldKey(workspace, field))
		if err != nil {
			return nil, false
		}
		for key, raw := range values {
			order, data := decodeMetadataCacheValue(raw)
			rows = append(rows, metadataCacheRow{Field: field, Key: key, Order: order, Data: data})
		}
	}
	// A write dropped the copy while it was read: some fields may be gone.
	if s.metadataCacheGeneration(ctx, workspace) != stamp.generation {
		return nil, false
	}
	return rows, true
}

func (s *SQLiteStore) metadataFieldsListKey(workspace string) string {
	if cache := s.hotCache(); cache != nil {
		return cache.CacheKey(workspace, "mrfields", "")
	}
	return ""
}

// dropMetadataCache discards the workspace's metadata record cache after a
// write committed, and retires copies being filled from earlier reads.
func (s *SQLiteStore) dropMetadataCache(ctx context.Context, workspace string) {
	cache := s.hotCache()
	if cache == nil || workspace == "" {
		return
	}
	_, _ = cache.CacheIncr(ctx, s.metadataGenerationKey(workspace))
	s.deleteMetadataCache(ctx, workspace)
}

func (s *SQLiteStore) deleteMetadataCache(ctx context.Context, workspace string) {
	cache := s.hotCache()
	fields := []string{"teams", "teamSettings", "cycleSettings", "states", "labels", "cycles", "projects", "users"}
	if raw, err := cache.CacheGet(ctx, s.metadataFieldsListKey(workspace)); err == nil && len(raw) > 0 {
		var listed []string
		if json.Unmarshal(raw, &listed) == nil {
			fields = listed
		}
	}
	keys := []string{s.metadataReadyKey(workspace), s.metadataFieldsListKey(workspace)}
	for _, field := range fields {
		keys = append(keys, s.metadataFieldKey(workspace, field))
	}
	_ = cache.CacheDel(ctx, keys...)
}

func (s *SQLiteStore) fillMetadataRecordsCache(ctx context.Context, workspace string, rows []metadataCacheRow) {
	if stamp, ok := s.metadataStamp(ctx, s.db, workspace); ok {
		s.fillMetadataRecordsCacheAt(ctx, workspace, rows, stamp)
	}
}

// fillMetadataRecordsCacheAt caches rows, read from the database at stamp.
func (s *SQLiteStore) fillMetadataRecordsCacheAt(ctx context.Context, workspace string, rows []metadataCacheRow, stamp metadataCacheStamp) {
	cache := s.hotCache()
	if cache == nil || workspace == "" {
		return
	}
	if s.metadataCacheGeneration(ctx, workspace) != stamp.generation {
		return
	}
	// Two fills at one stamp must not interleave: the second's delete would
	// remove fields the first already wrote, and readers would take the
	// first's ready mark for a whole copy. One fill per stamp claims it (the
	// claim is not released: rows read at one stamp are the same copy, and a
	// later fill at it has nothing to add).
	if claimer, ok := cache.(hotCacheClaimer); ok {
		claimed, err := claimer.CacheSetNX(ctx, s.metadataFillKey(workspace, stamp), []byte("1"), metadataFillClaimTTL)
		if err != nil || !claimed {
			return
		}
	}
	s.deleteMetadataCache(ctx, workspace)
	byField := map[string]map[string][]byte{}
	for _, row := range rows {
		if byField[row.Field] == nil {
			byField[row.Field] = map[string][]byte{}
		}
		byField[row.Field][row.Key] = encodeMetadataCacheValue(row.Order, row.Data)
	}
	fields := make([]string, 0, len(byField))
	for field, values := range byField {
		fields = append(fields, field)
		_ = cache.CacheHSetMap(ctx, s.metadataFieldKey(workspace, field), values)
	}
	if listed, err := json.Marshal(fields); err == nil {
		_ = cache.CacheSet(ctx, s.metadataFieldsListKey(workspace), listed, 24*time.Hour)
	}
	// A write committed while the copy was stored: it is already stale.
	if s.metadataCacheGeneration(ctx, workspace) != stamp.generation {
		return
	}
	_ = cache.CacheSet(ctx, s.metadataReadyKey(workspace), []byte(stamp.String()), 24*time.Hour)
}

// hotCacheClaimer is a hot cache that can claim a key (Redis SET NX).
type hotCacheClaimer interface {
	CacheSetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error)
}

// metadataFillClaimTTL bounds how long a fill that died midway keeps other
// fills at its stamp out.
const metadataFillClaimTTL = time.Minute

// metadataFillKey is the claim of the metadata cache fill at stamp.
func (s *SQLiteStore) metadataFillKey(workspace string, stamp metadataCacheStamp) string {
	return s.hotCache().CacheKey(workspace, "mfill", stamp.String())
}

func (s *SQLiteStore) invalidateDeletedTeamCache(ctx context.Context, workspace string, issues, projects bool) {
	cache := s.hotCache()
	if cache == nil || workspace == "" {
		return
	}
	if issues {
		_, _ = cache.CacheIncr(ctx, s.issueQueryGenKey(workspace))
	}
	if projects {
		_, _ = cache.CacheIncr(ctx, s.projectQueryGenKey(workspace))
	}
}

func (s *SQLiteStore) invalidateHotCache(ctx context.Context, workspace, eventType, aggregateID string) {
	cache := s.hotCache()
	if cache == nil || workspace == "" {
		return
	}
	issueEntity, issueQuery, projectQuery, meta := hotCacheTouch(eventType)
	if issueEntity && aggregateID != "" {
		s.cacheDropIssue(ctx, workspace, aggregateID)
	}
	if issueQuery {
		_, _ = cache.CacheIncr(ctx, s.issueQueryGenKey(workspace))
	}
	if projectQuery {
		_, _ = cache.CacheIncr(ctx, s.projectQueryGenKey(workspace))
	}
	if meta {
		s.dropMetadataCache(ctx, workspace)
	}
}

func hotCacheTouch(eventType string) (issueEntity, issueQuery, projectQuery, meta bool) {
	switch {
	case strings.HasPrefix(eventType, "issue."), strings.HasPrefix(eventType, "comment."), strings.Contains(eventType, "attachment"), strings.HasPrefix(eventType, "relation."):
		return true, true, false, false
	case eventType == "team.deleted":
		return false, false, false, false
	case strings.HasPrefix(eventType, "release.") && !strings.HasPrefix(eventType, "release.note_"), eventType == "release_pipeline.deleted":
		// Release membership and dates feed issue filters (release_issue_filter_records).
		return false, true, false, false
	case strings.HasPrefix(eventType, "project."), strings.HasPrefix(eventType, "label."), eventType == "alm.projects_imported":
		return false, true, true, true
	case strings.HasPrefix(eventType, "team."), eventType == "alm.org_teams_imported", eventType == "alm.users_imported":
		return false, true, true, false
	default:
		return false, false, false, false
	}
}
