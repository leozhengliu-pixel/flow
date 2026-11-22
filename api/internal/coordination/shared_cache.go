package coordination

import (
	"bytes"
	"compress/flate"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// SharedCache is the cross-replica cache tier (docs/redis-cache.md). Every
// entry is addressed by a key that embeds the immutable versions its value was
// computed from, so a hit is exactly what recomputing at those versions would
// produce and nothing is ever invalidated in place: a new version is a new key,
// and old keys age out by TTL (or the operator's maxmemory policy).
//
// The cache never fails a request. Each Redis operation runs under a short
// deadline; errors and timeouts count as misses, and repeated failures open a
// breaker that skips Redis entirely for a cooldown. Values above a size cap are
// refused, large values are compressed, and concurrent misses for one key on
// this replica compute once.
type SharedCache struct {
	redis  *Redis
	client redis.UniversalClient
	config SharedCacheConfig

	mu      sync.Mutex
	flights map[string]*sharedCacheFlight

	failures  atomic.Int64
	openUntil atomic.Int64

	stats SharedCacheStats
}

// SharedCacheConfig bounds the tier. Zero values take the defaults below.
type SharedCacheConfig struct {
	// Disabled turns the tier off (FLOW_REDIS_CACHE=off): every read computes.
	Disabled bool
	// TTL is the lifetime of an entry (default 5m). Entries are never updated
	// in place, so the TTL bounds how long superseded versions occupy memory.
	TTL time.Duration
	// OpTimeout bounds one Redis read or write (default 25ms).
	OpTimeout time.Duration
	// MaxEntryBytes refuses larger stored values, after compression (default 1 MiB).
	MaxEntryBytes int
	// MaxBytes is the namespace budget for encoded entries, keys and an
	// overhead allowance, divided across independent shards (default 64 MiB).
	MaxBytes int
	// CompressAboveBytes deflates values larger than this (default 2 KiB).
	CompressAboveBytes int
	// BreakerFailures consecutive failures skip Redis for BreakerCooldown
	// (defaults 3 and 2s).
	BreakerFailures int
	BreakerCooldown time.Duration
}

// SharedCacheStats are cumulative counters (load rig and tests).
type SharedCacheStats struct {
	Hits, Misses, Errors, Skipped, Sets, SetErrors, Oversize, Shared atomic.Int64
	BytesRead, BytesWritten, BudgetRejected                          atomic.Int64
}

type sharedCacheFlight struct {
	done  chan struct{}
	value []byte
	hit   bool
	err   error
}

const (
	sharedCacheMagic   = 'F'
	sharedCacheRaw     = 0
	sharedCacheDeflate = 1
	// sharedCacheMaxDecoded bounds a decompressed value independently of the
	// stored size (a deflate stream expands at most ~1000x).
	sharedCacheMaxDecoded = 16 << 20
	sharedCacheShards     = 32
	sharedCacheMaxFlights = 4096
)

// A shard is one Redis hash, so eviction removes the entries and their byte
// accounting together. Its TTL starts at the first fill and is never renewed.
// Fields are immutable logical keys and can be read with one HGET.
var sharedCacheAdmit = redis.NewScript(`
local size = tonumber(redis.call('HGET', KEYS[1], '@bytes') or '0')
local cost = string.len(ARGV[1]) + string.len(ARGV[2]) + 128
if size + cost > tonumber(ARGV[3]) then return -1 end
if redis.call('HEXISTS', KEYS[1], ARGV[1]) == 1 then return 0 end
redis.call('HSET', KEYS[1], ARGV[1], ARGV[2], '@bytes', size + cost)
if size == 0 then redis.call('PEXPIRE', KEYS[1], ARGV[4]) end
return 1`)

// ErrSharedCacheValue reports a stored value this build cannot decode; the
// caller treats it as a miss.
var ErrSharedCacheValue = errors.New("invalid shared cache value")

func NewSharedCache(r *Redis, config SharedCacheConfig) *SharedCache {
	if r == nil {
		return nil
	}
	if config.OpTimeout <= 0 {
		config.OpTimeout = 25 * time.Millisecond
	}
	if config.TTL <= 0 {
		config.TTL = 5 * time.Minute
	}
	if config.MaxEntryBytes <= 0 {
		config.MaxEntryBytes = 1 << 20
	}
	if config.MaxBytes <= 0 {
		config.MaxBytes = 64 << 20
	}
	if config.CompressAboveBytes <= 0 {
		config.CompressAboveBytes = 2 << 10
	}
	if config.BreakerFailures <= 0 {
		config.BreakerFailures = 3
	}
	if config.BreakerCooldown <= 0 {
		config.BreakerCooldown = 2 * time.Second
	}
	client := r.cacheClient
	if client == nil {
		client = r.client
	}
	return &SharedCache{redis: r, client: client, config: config, flights: map[string]*sharedCacheFlight{}}
}

func (c *SharedCache) Stats() *SharedCacheStats {
	if c == nil {
		return nil
	}
	return &c.stats
}

// sharedCacheTotals sums every SharedCache of the process (load rig metrics).
var sharedCacheTotals SharedCacheStats

type sharedCacheLookups struct {
	metric       string
	hits, misses atomic.Int64
}

var sharedCacheByKind = map[string]*sharedCacheLookups{
	"search-meta": {metric: "redisCacheSearch"},
	"viewer":      {metric: "redisCacheViewer"},
	"sync-page":   {metric: "redisCacheSync"},
}

// SharedCacheCounters reports the process-wide counters.
func SharedCacheCounters() map[string]int64 {
	t := &sharedCacheTotals
	result := map[string]int64{
		"redisCacheHits": t.Hits.Load(), "redisCacheMisses": t.Misses.Load(), "redisCacheErrors": t.Errors.Load(),
		"redisCacheSkipped": t.Skipped.Load(), "redisCacheSets": t.Sets.Load(), "redisCacheSetErrors": t.SetErrors.Load(),
		"redisCacheOversize": t.Oversize.Load(), "redisCacheShared": t.Shared.Load(),
		"redisCacheBytesRead": t.BytesRead.Load(), "redisCacheBytesWritten": t.BytesWritten.Load(),
		"redisCacheBudgetRejected": t.BudgetRejected.Load(),
	}
	for _, stats := range sharedCacheByKind {
		result[stats.metric+"Hits"] = stats.hits.Load()
		result[stats.metric+"Misses"] = stats.misses.Load()
	}
	return result
}

func (c *SharedCache) count(field func(*SharedCacheStats) *atomic.Int64, n int64) {
	field(&c.stats).Add(n)
	field(&sharedCacheTotals).Add(n)
}

// The coordinator exposes its tier through methods, so store code reaches it
// through an interface (store.sharedResultCache) like the other caches.

// SharedCacheEnabled reports whether the tier is on.
func (r *Redis) SharedCacheEnabled() bool { return r != nil && r.shared != nil }

// SharedCacheKey is SharedCache.Key on the coordinator's tier.
func (r *Redis) SharedCacheKey(workspace, kind, version string, material ...string) string {
	if r.shared == nil {
		return ""
	}
	return r.shared.Key(workspace, kind, version, material...)
}

// SharedCacheDo is SharedCache.Do with the configured TTL; without the tier
// it computes.
func (r *Redis) SharedCacheDo(ctx context.Context, key string, compute func(context.Context) ([]byte, error)) ([]byte, bool, error) {
	if r == nil || r.shared == nil || key == "" {
		value, err := compute(ctx)
		return value, false, err
	}
	return r.shared.Do(ctx, key, r.shared.config.TTL, compute)
}

// Key names one entry: prefix:sc:{shard}:kind:version:digest. The
// version is the readable immutable stamp (never a secret); the
// digest is SHA-256 over every other input (principal, scope, query), so
// identifiers, tokens and query text never appear in Redis.
func (c *SharedCache) Key(workspace, kind, version string, material ...string) string {
	sum := sha256.New()
	for _, part := range append([]string{workspace, kind, version}, material...) {
		var length [8]byte
		n := len(part)
		for i := range length {
			length[i] = byte(n >> (8 * i))
		}
		sum.Write(length[:])
		sum.Write([]byte(part))
	}
	digest := sum.Sum(nil)
	shard := strconv.Itoa(int(digest[0]) % sharedCacheShards)
	return c.redis.key("sc", "{result-"+shard+"}:"+kind+":"+strings.ReplaceAll(version, ":", ".")+":"+hex.EncodeToString(digest[:16]))
}

func (c *SharedCache) entry(key string) (bucket, field string) {
	prefix := c.redis.key("sc", "{result-")
	if rest, ok := strings.CutPrefix(key, prefix); ok {
		if slot, field, ok := strings.Cut(rest, "}:"); ok {
			return prefix + slot + "}", field
		}
	}
	return "", ""
}

func (c *SharedCache) available() bool {
	until := c.openUntil.Load()
	return until == 0 || time.Now().UnixNano() >= until
}

func (c *SharedCache) succeeded() {
	c.failures.Store(0)
	c.openUntil.Store(0)
}

func (c *SharedCache) failed() {
	if c.failures.Add(1) >= int64(c.config.BreakerFailures) {
		c.openUntil.Store(time.Now().Add(c.config.BreakerCooldown).UnixNano())
		c.failures.Store(0)
	}
}

// Get returns the stored value or ok=false on a miss, an error, a timeout or
// while the breaker is open.
func (c *SharedCache) Get(ctx context.Context, key string) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	if !c.available() {
		c.count(func(s *SharedCacheStats) *atomic.Int64 { return &s.Skipped }, 1)
		return nil, false
	}
	bucket, field := c.entry(key)
	if field == "" || ctx.Err() != nil {
		return nil, false
	}
	kind, _, _ := strings.Cut(field, ":")
	stats := sharedCacheByKind[kind]
	opCtx, cancel := context.WithTimeout(ctx, c.config.OpTimeout)
	defer cancel()
	raw, err := c.client.HGet(opCtx, bucket, field).Bytes()
	if errors.Is(err, redis.Nil) {
		c.succeeded()
		c.count(func(s *SharedCacheStats) *atomic.Int64 { return &s.Misses }, 1)
		if stats != nil {
			stats.misses.Add(1)
		}
		return nil, false
	}
	if err != nil {
		if ctx.Err() == nil {
			c.failed()
		}
		c.count(func(s *SharedCacheStats) *atomic.Int64 { return &s.Errors }, 1)
		return nil, false
	}
	c.succeeded()
	if len(raw) > c.config.MaxEntryBytes {
		return nil, false
	}
	value, err := decodeSharedCacheValue(raw)
	if err != nil {
		c.count(func(s *SharedCacheStats) *atomic.Int64 { return &s.Errors }, 1)
		return nil, false
	}
	c.count(func(s *SharedCacheStats) *atomic.Int64 { return &s.Hits }, 1)
	if stats != nil {
		stats.hits.Add(1)
	}
	c.count(func(s *SharedCacheStats) *atomic.Int64 { return &s.BytesRead }, int64(len(raw)))
	return value, true
}

// Set stores value under key for ttl. Oversized values are refused; errors
// are counted and otherwise ignored.
func (c *SharedCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) {
	if c == nil || ttl <= 0 || ctx.Err() != nil || !c.available() {
		return
	}
	bucket, field := c.entry(key)
	if field == "" {
		return
	}
	encoded, ok := c.encode(value)
	if !ok {
		c.count(func(s *SharedCacheStats) *atomic.Int64 { return &s.Oversize }, 1)
		return
	}
	opCtx, cancel := context.WithTimeout(ctx, c.config.OpTimeout)
	defer cancel()
	stored, err := sharedCacheAdmit.Run(opCtx, c.client, []string{bucket}, field, encoded, c.config.MaxBytes/sharedCacheShards, max(1, ttl.Milliseconds())).Int()
	if err != nil {
		if ctx.Err() == nil {
			c.failed()
		}
		c.count(func(s *SharedCacheStats) *atomic.Int64 { return &s.SetErrors }, 1)
		return
	}
	c.succeeded()
	if stored == -1 {
		c.count(func(s *SharedCacheStats) *atomic.Int64 { return &s.BudgetRejected }, 1)
	}
	if stored != 1 {
		return
	}
	c.count(func(s *SharedCacheStats) *atomic.Int64 { return &s.Sets }, 1)
	c.count(func(s *SharedCacheStats) *atomic.Int64 { return &s.BytesWritten }, int64(len(encoded)))
}

// Do returns the cached value of key or computes, stores and returns it.
// Concurrent callers for the same key on this replica wait for one compute
// (a waiter whose context ends returns its context error). A compute error is
// returned to every waiter and nothing is stored. Callers must not modify the
// returned slice: waiters share it.
func (c *SharedCache) Do(ctx context.Context, key string, ttl time.Duration, compute func(context.Context) ([]byte, error)) ([]byte, bool, error) {
	if c == nil {
		value, err := compute(ctx)
		return value, false, err
	}
	c.mu.Lock()
	if flight := c.flights[key]; flight != nil {
		c.mu.Unlock()
		select {
		case <-flight.done:
			if flight.err == nil {
				c.count(func(s *SharedCacheStats) *atomic.Int64 { return &s.Shared }, 1)
				return flight.value, flight.hit, nil
			}
			if ctx.Err() == nil && (errors.Is(flight.err, context.Canceled) || errors.Is(flight.err, context.DeadlineExceeded)) {
				// The leader's own request ended; this one computes for itself.
				value, err := compute(ctx)
				return value, false, err
			}
			return nil, false, flight.err
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
	}
	if len(c.flights) >= sharedCacheMaxFlights {
		c.mu.Unlock()
		value, err := compute(ctx)
		return value, false, err
	}
	flight := &sharedCacheFlight{done: make(chan struct{})}
	c.flights[key] = flight
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.flights, key)
		close(flight.done)
		c.mu.Unlock()
	}()
	if value, ok := c.Get(ctx, key); ok {
		flight.value, flight.hit = value, true
		return value, true, nil
	}
	flight.value, flight.err = compute(ctx)
	if flight.err == nil {
		c.Set(ctx, key, flight.value, ttl)
	}
	return flight.value, false, flight.err
}

func (c *SharedCache) encode(value []byte) ([]byte, bool) {
	if len(value) > sharedCacheMaxDecoded {
		return nil, false
	}
	if len(value)+2 <= c.config.CompressAboveBytes {
		if len(value)+2 > c.config.MaxEntryBytes {
			return nil, false
		}
		return append([]byte{sharedCacheMagic, sharedCacheRaw}, value...), true
	}
	var out bytes.Buffer
	out.Grow(len(value)/3 + 16)
	out.WriteByte(sharedCacheMagic)
	out.WriteByte(sharedCacheDeflate)
	writer, _ := flate.NewWriter(&out, flate.BestSpeed)
	_, _ = writer.Write(value)
	_ = writer.Close()
	if out.Len() > c.config.MaxEntryBytes {
		return nil, false
	}
	return out.Bytes(), true
}

func decodeSharedCacheValue(raw []byte) ([]byte, error) {
	if len(raw) < 2 || raw[0] != sharedCacheMagic {
		return nil, ErrSharedCacheValue
	}
	switch raw[1] {
	case sharedCacheRaw:
		return raw[2:], nil
	case sharedCacheDeflate:
		reader := flate.NewReader(bytes.NewReader(raw[2:]))
		defer reader.Close()
		value, err := io.ReadAll(io.LimitReader(reader, sharedCacheMaxDecoded+1))
		if err != nil || len(value) > sharedCacheMaxDecoded {
			return nil, ErrSharedCacheValue
		}
		return value, nil
	}
	return nil, ErrSharedCacheValue
}
