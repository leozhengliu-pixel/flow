package coordination

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
)

func TestScaleP21SW94CacheBytes(t *testing.T) {
	server := miniredis.RunT(t)
	cache := openTestRedis(t, server.Addr())
	t.Cleanup(func() { _ = cache.Close() })
	key := cache.CacheKey("owned-workspace", "owned-kind", "owned-value")
	for _, test := range []struct {
		name    string
		value   []byte
		missing bool
		limit   int64
		want    []byte
	}{
		{name: "missing", missing: true, limit: 100},
		{name: "empty", value: []byte{}, limit: 100},
		{name: "length-only", value: []byte("full value"), limit: 0},
		{name: "exact", value: []byte("full value"), limit: 10, want: []byte("full value")},
		{name: "below-limit", value: []byte("abc"), limit: 4, want: []byte("abc")},
		{name: "over-limit", value: []byte("full value"), limit: 9},
		{name: "binary-byte-length", value: []byte{0, 255, 'a', 0}, limit: 4, want: []byte{0, 255, 'a', 0}},
		{name: "utf8-byte-length", value: []byte("\u4e2d\u6587"), limit: 5},
	} {
		t.Run(test.name, func(t *testing.T) {
			server.Del(key)
			if !test.missing {
				if err := server.Set(key, string(test.value)); err != nil {
					t.Fatal(err)
				}
			}
			raw, size, err := cache.CacheGetBounded(t.Context(), key, test.limit)
			if err != nil || size != int64(len(test.value)) || !bytes.Equal(raw, test.want) {
				t.Fatalf("bounded cache value=%q size=%d error=%v; want=%q size=%d", raw, size, err, test.want, len(test.value))
			}
			if test.want == nil && raw != nil {
				t.Fatal("length-only or omitted result allocated an empty payload")
			}
			if len(raw) != 0 {
				raw[0] ^= 1
			}
			if !test.missing {
				stored, err := server.Get(key)
				if err != nil || stored != string(test.value) {
					t.Fatal("returned cache bytes mutated Redis", err)
				}
			}
		})
	}
	t.Run("growth-after-length-preflight", func(t *testing.T) {
		if err := server.Set(key, "old"); err != nil {
			t.Fatal(err)
		}
		raw, admitted, err := cache.CacheGetBounded(t.Context(), key, 0)
		if err != nil || raw != nil || admitted != 3 {
			t.Fatal("length-only preflight failed", err, admitted)
		}
		grown := strings.Repeat("grown value ", 1<<16)
		if err := server.Set(key, grown); err != nil {
			t.Fatal(err)
		}
		raw, actual, err := cache.CacheGetBounded(t.Context(), key, admitted)
		if err != nil || raw != nil || actual != int64(len(grown)) {
			t.Fatal("growth escaped the atomic read cap", err, actual, len(raw))
		}
	})
	t.Run("negative-limit", func(t *testing.T) {
		if raw, size, err := cache.CacheGetBounded(t.Context(), key, -1); err == nil || raw != nil || size != 0 {
			t.Fatal("negative byte limit accepted", err, size, len(raw))
		}
	})
	t.Run("wrong-type-error", func(t *testing.T) {
		server.Del(key)
		if _, err := server.Lpush(key, "list-value"); err != nil {
			t.Fatal(err)
		}
		if raw, _, err := cache.CacheGetBounded(t.Context(), key, 100); err == nil || raw != nil {
			t.Fatal("Redis type error became a cache value", err)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		if raw, _, err := cache.CacheGetBounded(cancelled, key, 100); raw != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("canceled Redis read returned a value or changed its error", err)
		}
	})
}
