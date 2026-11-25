package main

import (
	"bufio"
	"bytes"
	"container/list"
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestBootstrapEncodingCacheVisibilityAndFreshValues(t *testing.T) {
	renderer := newBootstrapEncodingRenderer(1 << 20)
	key := bootstrapEncodingKey{generation: 7, principal: "admin"}
	encode := func(ctx context.Context, key bootstrapEncodingKey, description string) string {
		t.Helper()
		return encodeBootstrapProjectForTest(t, ctx, renderer, key, description)
	}
	first := encode(t.Context(), key, "original")
	hits := bootstrapEncodingHits.Load()
	if got := encode(t.Context(), key, "original"); got != first || bootstrapEncodingHits.Load() != hits+1 {
		t.Fatal("same finalized field was not reused byte-for-byte")
	}
	for _, other := range []bootstrapEncodingKey{
		{generation: key.generation, principal: "member"},
		{generation: key.generation, principal: "admin", credential: "scoped"},
		{generation: key.generation, principal: "admin", projection: 1},
		{generation: 8, principal: "admin"},
	} {
		hits = bootstrapEncodingHits.Load()
		if got := encode(t.Context(), other, "original"); got != first || bootstrapEncodingHits.Load() != hits {
			t.Fatal("another generation or visibility scope reused the encoding")
		}
	}
	hits = bootstrapEncodingHits.Load()
	if got := encode(t.Context(), key, "current"); !strings.Contains(got, "current") || bootstrapEncodingHits.Load() != hits {
		t.Fatal("a changed projection reused the cached bytes")
	}
}

func TestBootstrapEncodingCacheSignsPerRequest(t *testing.T) {
	renderer := newBootstrapEncodingRenderer(1 << 20)
	key := bootstrapEncodingKey{principal: "admin"}
	const uploaded = "/uploads/test.png"
	encode := func(signing *hydratedURLSigning) string {
		return encodeBootstrapProjectForTest(t, context.WithValue(t.Context(), hydratedURLSigningKey{}, signing), renderer, key, uploaded)
	}
	unsigned := encode(nil)
	signed := &hydratedURLSigning{key: []byte("unit-test-signing-key"), base: "https://example.test", expires: time.Unix(100000, 0)}
	first := encode(signed)
	signed.expires = time.Unix(200000, 0)
	second := encode(signed)
	if unsigned == first || first == second || signed.permits.Load() != 2 || !strings.Contains(unsigned, uploaded) {
		t.Fatal("a file URL did not go through the active signer on each request")
	}
	if _, ok := renderer.cache.get(key); !ok {
		t.Fatal("unsigned fields with file URLs can be reused for unsigned requests")
	}
	key.signed = true
	if _, ok := renderer.cache.get(key); ok {
		t.Fatal("cached a field that checked URL signing authority")
	}
	signed.allow = func(string) bool { return false }
	if denied := encode(signed); denied != unsigned {
		t.Fatal("a denied file URL was rewritten")
	}
	if _, ok := renderer.cache.get(key); ok {
		t.Fatal("cached the outcome of a denied file check")
	}
}

func TestBootstrapEncodingCacheOwnsRecycledSlice(t *testing.T) {
	renderer := newBootstrapEncodingRenderer(4 << 20)
	ctx := context.WithValue(t.Context(), hydratedLegacyDownloadKey{}, true)
	key := bootstrapEncodingKey{generation: 1, principal: "admin"}
	projects := make([]domain.Project, 128)
	projects[0] = domain.Project{ID: "original", Description: "/uploads/example.png"}
	encode := func(projects []domain.Project) string {
		var body bytes.Buffer
		writer := bufio.NewWriter(&body)
		if err := renderer.encode(ctx, writer, key, reflect.ValueOf(projects), 1); err != nil {
			t.Fatal(err)
		}
		if err := writer.Flush(); err != nil {
			t.Fatal(err)
		}
		return body.String()
	}
	first := encode(projects)
	replacement := append([]domain.Project(nil), projects...)
	clear(projects)
	hits := bootstrapEncodingHits.Load()
	if got := encode(replacement); got != first || bootstrapEncodingHits.Load() != hits+1 {
		t.Fatal("the admitted download did not reuse its encoding after the original slice was recycled")
	}
}

func TestBootstrapEncodingCacheLRUAndConcurrentReaders(t *testing.T) {
	cache := &memoryBootstrapEncodings{entries: make(map[bootstrapEncodingKey]*list.Element), limit: 1200}
	entry := bootstrapEncoding{value: "test", json: []byte(`"test"`), bytes: 10}
	a, b, c := bootstrapEncodingKey{field: 1}, bootstrapEncodingKey{field: 2}, bootstrapEncodingKey{field: 3}
	cache.put(a, entry)
	cache.put(b, entry)
	cache.get(a)
	cache.put(c, entry)
	if _, ok := cache.get(b); ok {
		t.Fatal("least recently used entry survived")
	}
	var readers sync.WaitGroup
	for range 8 {
		readers.Go(func() {
			for range 32 {
				cache.put(c, entry)
				if out, ok := cache.get(a); !ok || !bytes.Equal(out.json, entry.json) {
					t.Error("concurrent access lost or changed the current entry")
				}
			}
		})
	}
	readers.Wait()
	if cache.used > cache.limit || len(cache.entries) != 2 {
		t.Fatalf("cache retained %d entries and %d bytes under the %d byte limit", len(cache.entries), cache.used, cache.limit)
	}
}

func encodeBootstrapProjectForTest(t *testing.T, ctx context.Context, renderer *bootstrapEncodingRenderer, key bootstrapEncodingKey, description string) string {
	t.Helper()
	var body bytes.Buffer
	writer := bufio.NewWriter(&body)
	projects := []domain.Project{{ID: "project", Description: description}}
	if err := renderer.encode(ctx, writer, key, reflect.ValueOf(projects), 1); err != nil {
		t.Fatal(err)
	}
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
	return body.String()
}
