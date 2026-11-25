package coordination

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	miniserver "github.com/alicebob/miniredis/v2/server"
)

func TestSharedCacheRoundTripCompressionAndBounds(t *testing.T) {
	server := miniredis.RunT(t)
	cache := NewSharedCache(openTestRedis(t, server.Addr()), SharedCacheConfig{MaxEntryBytes: 4 << 10, CompressAboveBytes: 64, OpTimeout: time.Second})
	ctx := t.Context()
	key := cache.Key("acme", "unit", "7.3", "user_1", "secret-token-hash")
	if strings.Contains(key, "user_1") || strings.Contains(key, "secret") || !strings.HasPrefix(key, "test:sc:{") || !strings.Contains(key, ":unit:7.3:") {
		t.Fatalf("key %q leaks material or lacks its namespace/version", key)
	}
	if other := cache.Key("acme", "unit", "7.3", "user_1secret-token-hash"); other == key {
		t.Fatal("material boundaries must be part of the digest")
	}
	if _, ok := cache.Get(ctx, key); ok {
		t.Fatal("empty cache hit")
	}
	small := []byte(`{"a":1}`)
	cache.Set(ctx, key, small, time.Minute)
	if got, ok := cache.Get(ctx, key); !ok || !bytes.Equal(got, small) {
		t.Fatalf("small round trip = %q, %v", got, ok)
	}
	large := bytes.Repeat([]byte(`{"title":"repeated issue title","state":"started"},`), 200)
	cache.Set(ctx, key+"l", large, time.Minute)
	bucket, field := cache.entry(key + "l")
	stored := server.HGet(bucket, field)
	if len(stored) < 2 {
		t.Fatal("compressed entry was not stored")
	}
	if len(stored) >= len(large)/4 || stored[1] != sharedCacheDeflate {
		t.Fatalf("large value stored with %d bytes (of %d), format %d", len(stored), len(large), stored[1])
	}
	if got, ok := cache.Get(ctx, key+"l"); !ok || !bytes.Equal(got, large) {
		t.Fatal("compressed round trip differs")
	}
	if ttl := server.TTL(bucket); ttl <= 0 || ttl > time.Minute {
		t.Fatalf("ttl = %s", ttl)
	}
	random := make([]byte, 8<<10)
	_, _ = rand.Read(random)
	cache.Set(ctx, key+"big", random, time.Minute)
	if server.HGet(bucket, strings.TrimSuffix(field, "l")+"big") != "" || cache.Stats().Oversize.Load() != 1 {
		t.Fatal("an entry above the size cap must be refused")
	}
	// A foreign or truncated value is a miss, never an error to the caller.
	server.HSet(bucket, strings.TrimSuffix(field, "l")+"bad", "xx")
	if _, ok := cache.Get(ctx, key+"bad"); ok {
		t.Fatal("undecodable value served")
	}
}

func TestSharedCacheBudgetAndExpiryAcrossReplicas(t *testing.T) {
	server := miniredis.RunT(t)
	config := SharedCacheConfig{OpTimeout: time.Second, MaxBytes: 512 * sharedCacheShards}
	first, second := NewSharedCache(openTestRedis(t, server.Addr()), config), NewSharedCache(openTestRedis(t, server.Addr()), config)
	key := first.Key("acme", "unit", "1", "q")
	data := bytes.Repeat([]byte("x"), 200)
	first.Set(t.Context(), key, data, time.Minute)
	server.FastForward(30 * time.Second)
	second.Set(t.Context(), key+"other", data, time.Minute)
	if _, ok := second.Get(t.Context(), key+"other"); ok || second.Stats().BudgetRejected.Load() != 1 {
		t.Fatal("second replica exceeded its shard's budget")
	}
	second.Set(t.Context(), key, []byte("replacement"), time.Minute)
	if got, _ := first.Get(t.Context(), key); !bytes.Equal(got, data) {
		t.Fatal("an immutable entry was replaced")
	}
	server.FastForward(30 * time.Second)
	if _, ok := first.Get(t.Context(), key); ok {
		t.Fatal("an admission attempt extended the original lifetime")
	}
	second.Set(t.Context(), key+"other", data, time.Minute)
	if _, ok := first.Get(t.Context(), key+"other"); !ok {
		t.Fatal("expiry did not recover the budget")
	}
}

func TestSharedCacheSlowRedisHonorsDeadline(t *testing.T) {
	server := miniredis.RunT(t)
	client, err := Open(t.Context(), Config{Mode: "standalone", Addrs: []string{server.Addr()}, Cache: SharedCacheConfig{OpTimeout: 15 * time.Millisecond}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	key := client.SharedCacheKey("acme", "unit", "1", "q")
	client.shared.Set(t.Context(), key, []byte("stored"), time.Minute)
	release := make(chan struct{})
	defer close(release)
	server.Server().SetPreHook(func(_ *miniserver.Peer, command string, _ ...string) bool {
		if strings.EqualFold(command, "HGET") {
			<-release
		}
		return false
	})
	started := time.Now()
	got, hit, err := client.SharedCacheDo(t.Context(), key, func(context.Context) ([]byte, error) { return []byte("local"), nil })
	if err != nil || hit || string(got) != "local" || time.Since(started) > 500*time.Millisecond {
		t.Fatalf("slow Redis lookup = %q, hit=%v, err=%v, elapsed=%s", got, hit, err, time.Since(started))
	}
}

func TestSharedCacheDoComputesOncePerReplica(t *testing.T) {
	server := miniredis.RunT(t)
	cache := NewSharedCache(openTestRedis(t, server.Addr()), SharedCacheConfig{OpTimeout: time.Second})
	key := cache.Key("acme", "unit", "1", "q")
	var computes atomic.Int64
	release := make(chan struct{})
	var wait sync.WaitGroup
	results := make([][]byte, 8)
	for i := range results {
		wait.Add(1)
		go func() {
			defer wait.Done()
			value, _, err := cache.Do(t.Context(), key, time.Minute, func(context.Context) ([]byte, error) {
				computes.Add(1)
				<-release
				return []byte("value"), nil
			})
			if err != nil {
				t.Error(err)
			}
			results[i] = value
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wait.Wait()
	if computes.Load() != 1 {
		t.Fatalf("computed %d times", computes.Load())
	}
	for _, value := range results {
		if string(value) != "value" {
			t.Fatalf("result %q", value)
		}
	}
	// A second replica reads the stored value without computing.
	other := NewSharedCache(openTestRedis(t, server.Addr()), SharedCacheConfig{OpTimeout: time.Second})
	value, hit, err := other.Do(t.Context(), key, time.Minute, func(context.Context) ([]byte, error) { return nil, errors.New("must not compute") })
	if err != nil || !hit || string(value) != "value" {
		t.Fatalf("peer read = %q, %v, %v", value, hit, err)
	}
	// Errors are not stored.
	failing := cache.Key("acme", "unit", "1", "failing")
	if _, _, err := cache.Do(t.Context(), failing, time.Minute, func(context.Context) ([]byte, error) { return nil, errors.New("boom") }); err == nil {
		t.Fatal("compute error lost")
	}
	bucket, field := cache.entry(failing)
	if server.HGet(bucket, field) != "" {
		t.Fatal("a failed compute was stored")
	}
}

func TestSharedCacheDegradesWhenRedisIsDown(t *testing.T) {
	server := miniredis.RunT(t)
	cache := NewSharedCache(openTestRedis(t, server.Addr()), SharedCacheConfig{OpTimeout: 20 * time.Millisecond, BreakerFailures: 2, BreakerCooldown: time.Hour})
	key := cache.Key("acme", "unit", "1", "q")
	cache.Set(t.Context(), key, []byte("cached"), time.Minute)
	address := server.Addr()
	server.Close()
	started := time.Now()
	for range 5 {
		value, hit, err := cache.Do(t.Context(), key, time.Minute, func(context.Context) ([]byte, error) { return []byte("computed"), nil })
		if err != nil || hit || string(value) != "computed" {
			t.Fatalf("degraded read = %q, %v, %v", value, hit, err)
		}
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("degraded reads took %s", elapsed)
	}
	if cache.Stats().Skipped.Load() == 0 {
		t.Fatal("the breaker never opened")
	}
	// A Redis that comes back empty (restart, eviction) is a plain miss.
	restarted := miniredis.NewMiniRedis()
	if err := restarted.StartAddr(address); err != nil {
		t.Skip("address not reusable:", err)
	}
	defer restarted.Close()
	cache.openUntil.Store(0)
	value, hit, err := cache.Do(t.Context(), key, time.Minute, func(context.Context) ([]byte, error) { return []byte("recomputed"), nil })
	if err != nil || hit || string(value) != "recomputed" {
		t.Fatalf("after restart = %q, %v, %v", value, hit, err)
	}
}

func TestSharedCacheNilIsComputeOnly(t *testing.T) {
	var cache *SharedCache
	value, hit, err := cache.Do(t.Context(), "k", time.Minute, func(context.Context) ([]byte, error) { return []byte("v"), nil })
	if err != nil || hit || string(value) != "v" {
		t.Fatalf("nil cache = %q, %v, %v", value, hit, err)
	}
	if _, ok := cache.Get(t.Context(), "k"); ok {
		t.Fatal("nil cache hit")
	}
	cache.Set(t.Context(), "k", []byte("v"), time.Minute)
}
