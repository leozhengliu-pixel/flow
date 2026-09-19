package coordination

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	issueCacheTTL = 15 * time.Minute
	queryCacheTTL = 60 * time.Second
)

func (r *Redis) CacheGet(ctx context.Context, key string) ([]byte, error) {
	raw, err := r.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	return raw, err
}

func (r *Redis) CacheSet(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if ttl <= 0 {
		ttl = issueCacheTTL
	}
	return r.client.Set(ctx, key, value, ttl).Err()
}

func (r *Redis) CacheDel(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	return r.client.Del(ctx, keys...).Err()
}

func (r *Redis) CacheIncr(ctx context.Context, key string) (int64, error) {
	return r.client.Incr(ctx, key).Result()
}

func (r *Redis) CacheHGetAll(ctx context.Context, key string) (map[string]string, error) {
	result, err := r.client.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (r *Redis) CacheHSet(ctx context.Context, key, field string, value []byte) error {
	return r.client.HSet(ctx, key, field, value).Err()
}

func (r *Redis) CacheHSetMap(ctx context.Context, key string, fields map[string][]byte) error {
	if len(fields) == 0 {
		return nil
	}
	pipe := r.client.Pipeline()
	for field, value := range fields {
		pipe.HSet(ctx, key, field, value)
	}
	_, err := pipe.Exec(ctx)
	return err
}

func (r *Redis) CacheHDel(ctx context.Context, key string, fields ...string) error {
	if len(fields) == 0 {
		return nil
	}
	return r.client.HDel(ctx, key, fields...).Err()
}

func (r *Redis) CacheKey(workspace, kind, suffix string) string {
	if suffix == "" {
		return r.key("c", slot(workspace)+":"+kind)
	}
	return r.key("c", slot(workspace)+":"+kind+":"+suffix)
}

func QueryCacheTTL() time.Duration { return queryCacheTTL }
func IssueCacheTTL() time.Duration { return issueCacheTTL }
