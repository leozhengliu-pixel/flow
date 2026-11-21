package coordination

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSharedCacheSearchReuseWindow(t *testing.T) {
	var reuse sharedSearchReuse
	now := time.Now()
	observe := func(after time.Duration, user string) bool {
		return reuse.observe(now.Add(after), "workspace", user, "", []string{"one", "two"})
	}
	if observe(0, "viewer") || observe(time.Second, "viewer") || !observe(2*time.Second, "viewer") {
		t.Fatal("the third recent search should be the first admitted")
	}
	if observe(sharedSearchReuseWindow+2*time.Second, "viewer") {
		t.Fatal("expired observations should not admit a search")
	}
	if observe(sharedSearchReuseWindow+3*time.Second, "other") {
		t.Fatal("one viewer's reuse should not admit a different viewer")
	}
}

func TestSharedCacheSearchReuseConcurrent(t *testing.T) {
	var reuse sharedSearchReuse
	var admitted atomic.Int64
	var users sync.WaitGroup
	now := time.Now()
	for range 24 {
		users.Go(func() {
			if reuse.observe(now, "workspace", "viewer", "credential", []string{"search"}) {
				admitted.Add(1)
			}
		})
	}
	users.Wait()
	if got := admitted.Load(); got != 22 {
		t.Fatalf("admitted %d concurrent requests, want 22", got)
	}
}

func BenchmarkSharedCacheSearchAdmission(b *testing.B) {
	terms := make([][]string, 16<<10)
	for i := range terms {
		terms[i] = []string{"release", strconv.Itoa(i)}
	}
	for _, tc := range []struct {
		name  string
		cache *SharedCache
	}{
		{"off", nil},
		{"reuse-gate", &SharedCache{}},
	} {
		b.Run(tc.name, func(b *testing.B) {
			r := Redis{shared: tc.cache}
			b.ReportAllocs()
			i := 0
			for b.Loop() {
				r.SharedCacheSearchReusable("workspace", "viewer", "credential", terms[i])
				i = (i + 1) % len(terms)
			}
		})
	}
}
