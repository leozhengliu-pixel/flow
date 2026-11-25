package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"

	"flow/api/internal/domain"
)

type metadataBoundedCache interface {
	CacheGetBounded(context.Context, string, int64) ([]byte, int64, error)
}

func (s *SQLiteStore) cacheGetProjectQueryOwned(ctx context.Context, query ProjectRecordQuery, owner *ResourceRead) (ProjectRecordPage, string, bool, error) {
	cache := s.hotCache()
	if cache == nil {
		return ProjectRecordPage{}, "", false, nil
	}
	bounded, ok := cache.(metadataBoundedCache)
	if !ok || owner == nil || owner.store != s {
		return ProjectRecordPage{}, "", false, ErrBoundedMetadataCoverage
	}
	revision, err := s.cacheRevision(ctx, s.db, query.Workspace, cacheKindMetadata)
	if err != nil {
		return ProjectRecordPage{}, "", false, nil
	}
	// The only supported producer of this value is Redis INCR, whose
	// complete signed int64 representation fits twenty bytes.
	if err := owner.reserve(ctx, 512); err != nil {
		return ProjectRecordPage{}, "", false, err
	}
	var generation int64
	rawGeneration, _, generationErr := bounded.CacheGetBounded(ctx, s.projectQueryGenKey(query.Workspace), 20)
	if generationErr == nil {
		generation, _ = strconv.ParseInt(string(rawGeneration), 10, 64)
	}
	input := struct {
		Query    ProjectRecordQuery `json:"q"`
		Gen      int64              `json:"g"`
		Revision int64              `json:"r"`
	}{query, generation, revision}
	cost, err := metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(input), 0)
	if err != nil {
		return ProjectRecordPage{}, "", false, err
	}
	if err := owner.reserve(ctx, uint64(cost)); err != nil {
		return ProjectRecordPage{}, "", false, err
	}
	rawKey, _ := json.Marshal(input)
	sum := sha256.Sum256(rawKey)
	key := cache.CacheKey(query.Workspace, "pq", fmt.Sprintf("%d:%d:%x", generation, revision, sum[:16]))
	_, size, err := bounded.CacheGetBounded(ctx, key, 0)
	if err != nil || size == 0 {
		return ProjectRecordPage{}, key, false, nil
	}
	if size < 0 || size > metadataWorkBytes/32 {
		return ProjectRecordPage{}, key, false, ErrMetadataWorkBudget
	}
	// Redis's reply string and the returned byte slice coexist. The
	// server-side length guard also covers growth after this preflight.
	if err := owner.reserve(ctx, uint64(size)*2+64<<10); err != nil {
		return ProjectRecordPage{}, key, false, err
	}
	raw, actual, err := bounded.CacheGetBounded(ctx, key, size)
	if err != nil || actual == 0 {
		return ProjectRecordPage{}, key, false, nil
	}
	if actual > size || int64(len(raw)) != actual {
		return ProjectRecordPage{}, key, false, ErrMetadataWorkBudget
	}
	decoded, err := metadataAggregatePayloadJSONWork(ctx, raw)
	if err != nil {
		return ProjectRecordPage{}, key, false, err
	}
	if err := owner.reserve(ctx, decoded); err != nil {
		return ProjectRecordPage{}, key, false, err
	}
	var page ProjectRecordPage
	if json.Unmarshal(raw, &page) != nil {
		return ProjectRecordPage{}, key, false, nil
	}
	if page.Items == nil {
		page.Items = []domain.Project{}
	}
	return page, key, true, nil
}

func (s *SQLiteStore) cacheSetProjectQueryOwned(ctx context.Context, key string, page ProjectRecordPage, owner *ResourceRead) error {
	if s.hotCache() == nil || key == "" {
		return nil
	}
	if owner == nil || owner.store != s {
		return ErrBoundedMetadataCoverage
	}
	cost, err := metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(page), 0)
	if err != nil {
		return err
	}
	if err := owner.reserve(ctx, uint64(cost)); err != nil {
		return err
	}
	s.cacheSetProjectQuery(ctx, key, page)
	return nil
}
