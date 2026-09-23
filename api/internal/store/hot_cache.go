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

func (s *SQLiteStore) issueQueryCacheKey(ctx context.Context, query IssueRecordQuery) string {
	cache := s.hotCache()
	if cache == nil {
		return ""
	}
	gen := s.cacheGeneration(ctx, s.issueQueryGenKey(query.Workspace))
	scope := query
	scope.Access = nil
	raw, _ := json.Marshal(struct {
		Query IssueRecordQuery `json:"q"`
		User  string           `json:"u,omitempty"`
		Admin bool             `json:"a,omitempty"`
		Teams []string         `json:"t,omitempty"`
		Gen   int64            `json:"g"`
	}{Query: scope, User: accessUser(query.Access), Admin: accessAdmin(query.Access), Teams: accessTeams(query.Access), Gen: gen})
	sum := sha256.Sum256(raw)
	return cache.CacheKey(query.Workspace, "iq", fmt.Sprintf("%d:%x", gen, sum[:16]))
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

func (s *SQLiteStore) cacheGetIssueQuery(ctx context.Context, query IssueRecordQuery) (IssueRecordPage, bool) {
	cache := s.hotCache()
	key := s.issueQueryCacheKey(ctx, query)
	if cache == nil || key == "" {
		return IssueRecordPage{}, false
	}
	raw, err := cache.CacheGet(ctx, key)
	if err != nil || len(raw) == 0 {
		return IssueRecordPage{}, false
	}
	var page IssueRecordPage
	if json.Unmarshal(raw, &page) != nil {
		return IssueRecordPage{}, false
	}
	if page.Items == nil {
		page.Items = []domain.Issue{}
	}
	return page, true
}

func (s *SQLiteStore) issueGroupCacheKey(ctx context.Context, query IssueRecordQuery) string {
	cache := s.hotCache()
	if cache == nil {
		return ""
	}
	gen := s.cacheGeneration(ctx, s.issueQueryGenKey(query.Workspace))
	scope := query
	scope.Access = nil
	raw, _ := json.Marshal(struct {
		Query IssueRecordQuery `json:"q"`
		User  string           `json:"u,omitempty"`
		Admin bool             `json:"a,omitempty"`
		Teams []string         `json:"t,omitempty"`
		Gen   int64            `json:"g"`
	}{Query: scope, User: accessUser(query.Access), Admin: accessAdmin(query.Access), Teams: accessTeams(query.Access), Gen: gen})
	sum := sha256.Sum256(raw)
	return cache.CacheKey(query.Workspace, "ig", fmt.Sprintf("%d:%x", gen, sum[:16]))
}

func (s *SQLiteStore) cacheGetIssueGroups(ctx context.Context, query IssueRecordQuery) ([]IssueRecordGroup, bool) {
	cache := s.hotCache()
	key := s.issueGroupCacheKey(ctx, query)
	if cache == nil || key == "" {
		return nil, false
	}
	raw, err := cache.CacheGet(ctx, key)
	if err != nil || len(raw) == 0 {
		return nil, false
	}
	var groups []IssueRecordGroup
	if json.Unmarshal(raw, &groups) != nil {
		return nil, false
	}
	if groups == nil {
		groups = []IssueRecordGroup{}
	}
	return groups, true
}

func (s *SQLiteStore) cacheSetIssueGroups(ctx context.Context, query IssueRecordQuery, groups []IssueRecordGroup) {
	cache := s.hotCache()
	key := s.issueGroupCacheKey(ctx, query)
	if cache == nil || key == "" {
		return
	}
	raw, err := json.Marshal(groups)
	if err != nil {
		return
	}
	_ = cache.CacheSet(ctx, key, raw, 60*time.Second)
}

func (s *SQLiteStore) cacheSetIssueQuery(ctx context.Context, query IssueRecordQuery, page IssueRecordPage) {
	cache := s.hotCache()
	key := s.issueQueryCacheKey(ctx, query)
	if cache == nil || key == "" {
		return
	}
	raw, err := json.Marshal(page)
	if err != nil {
		return
	}
	_ = cache.CacheSet(ctx, key, raw, 60*time.Second)
}

func (s *SQLiteStore) projectQueryCacheKey(query ProjectRecordQuery) string {
	cache := s.hotCache()
	if cache == nil {
		return ""
	}
	gen := s.cacheGeneration(context.Background(), s.projectQueryGenKey(query.Workspace))
	raw, _ := json.Marshal(struct {
		Query ProjectRecordQuery `json:"q"`
		Gen   int64              `json:"g"`
	}{Query: query, Gen: gen})
	sum := sha256.Sum256(raw)
	return cache.CacheKey(query.Workspace, "pq", fmt.Sprintf("%d:%x", gen, sum[:16]))
}

func (s *SQLiteStore) cacheGetProjectQuery(ctx context.Context, query ProjectRecordQuery) (ProjectRecordPage, bool) {
	cache := s.hotCache()
	key := s.projectQueryCacheKey(query)
	if cache == nil || key == "" {
		return ProjectRecordPage{}, false
	}
	raw, err := cache.CacheGet(ctx, key)
	if err != nil || len(raw) == 0 {
		return ProjectRecordPage{}, false
	}
	var page ProjectRecordPage
	if json.Unmarshal(raw, &page) != nil {
		return ProjectRecordPage{}, false
	}
	if page.Items == nil {
		page.Items = []domain.Project{}
	}
	return page, true
}

func (s *SQLiteStore) cacheSetProjectQuery(ctx context.Context, query ProjectRecordQuery, page ProjectRecordPage) {
	cache := s.hotCache()
	key := s.projectQueryCacheKey(query)
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

func (s *SQLiteStore) metadataRecordsFromCache(ctx context.Context, workspace string, fields map[string]string) ([]metadataCacheRow, bool) {
	cache := s.hotCache()
	if cache == nil || workspace == "" || len(fields) == 0 {
		return nil, false
	}
	ready, err := cache.CacheGet(ctx, s.metadataReadyKey(workspace))
	if err != nil || string(ready) != "1" {
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
	return rows, true
}

func (s *SQLiteStore) metadataFieldsListKey(workspace string) string {
	if cache := s.hotCache(); cache != nil {
		return cache.CacheKey(workspace, "mrfields", "")
	}
	return ""
}

func (s *SQLiteStore) dropMetadataCache(ctx context.Context, workspace string) {
	cache := s.hotCache()
	if cache == nil || workspace == "" {
		return
	}
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
	cache := s.hotCache()
	if cache == nil || workspace == "" {
		return
	}
	s.dropMetadataCache(ctx, workspace)
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
	_ = cache.CacheSet(ctx, s.metadataReadyKey(workspace), []byte("1"), 24*time.Hour)
}

func (s *SQLiteStore) cacheMetadataUpserts(ctx context.Context, workspace string, changes []metadataRecordChange) {
	cache := s.hotCache()
	if cache == nil || workspace == "" {
		return
	}
	for _, change := range changes {
		if change.drop {
			continue
		}
		_ = cache.CacheHSet(ctx, s.metadataFieldKey(workspace, change.field), change.key, encodeMetadataCacheValue(change.order, change.raw))
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
		return false, true, true, true
	case strings.HasPrefix(eventType, "project."), strings.HasPrefix(eventType, "label."), eventType == "alm.projects_imported":
		return false, true, true, true
	case strings.HasPrefix(eventType, "team."), eventType == "alm.org_teams_imported", eventType == "alm.users_imported":
		return false, true, true, false
	default:
		return false, false, false, false
	}
}
