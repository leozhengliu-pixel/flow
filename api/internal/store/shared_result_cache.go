package store

import (
	"context"
	"encoding/json"
	"errors"
)

// sharedResultCache is the cross-replica result cache of the coordinator
// (coordination.SharedCache, docs/redis-cache.md). An entry's key embeds the
// versions its value was computed from; a value is always recomputable and
// the cache never fails a read (Redis errors and timeouts compute locally).
type sharedResultCache interface {
	SharedCacheEnabled() bool
	SharedCacheViewerEnabled() bool
	SharedCacheSearchReusable(workspace, user, credential string, terms []string) bool
	SharedCacheKey(workspace, kind, version string, material ...string) string
	SharedCacheDo(ctx context.Context, key string, compute func(context.Context) ([]byte, error)) ([]byte, bool, error)
}

func (s *SQLiteStore) sharedResults(ctx context.Context) sharedResultCache {
	// Do not pair a borrowed snapshot with versions read from live SQL.
	if _, borrowed := ctx.Value(workspaceReadKey{}).(workspaceRead); borrowed {
		return nil
	}
	cache, ok := s.coordinator.(sharedResultCache)
	if !ok || !cache.SharedCacheEnabled() {
		return nil
	}
	return cache
}

var errSharedResultUncacheable = errors.New("shared result could not be cached")

// sharedJSONResult keeps results computed across a version change local to
// their original caller. A waiter computes its own result on that path.
func sharedJSONResult[T any](ctx context.Context, cache sharedResultCache, key string, compute func(context.Context) (T, error), current func(context.Context) bool) (T, error) {
	var result T
	computed := false
	raw, _, err := cache.SharedCacheDo(ctx, key, func(ctx context.Context) ([]byte, error) {
		var err error
		result, err = compute(ctx)
		computed = true
		if err != nil {
			return nil, err
		}
		if !current(ctx) {
			return nil, errSharedResultUncacheable
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return nil, errSharedResultUncacheable
		}
		return raw, nil
	})
	if computed {
		if errors.Is(err, errSharedResultUncacheable) {
			err = nil
		}
		return result, err
	}
	if errors.Is(err, errSharedResultUncacheable) || err == nil && json.Unmarshal(raw, &result) != nil {
		return compute(ctx)
	}
	return result, err
}
