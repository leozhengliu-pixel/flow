//go:build integration

package coordination

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestExternalRedisSharedCacheRoundTrip(t *testing.T) {
	address := os.Getenv("FLOW_TEST_REDIS_ADDR")
	if address == "" {
		t.Skip("external Redis address is not set")
	}
	config := Config{Mode: "standalone", Addrs: []string{address}, Prefix: "flow-integration-" + randomID(), Cache: SharedCacheConfig{OpTimeout: time.Second}}
	open := func() *Redis {
		client, err := Open(t.Context(), config)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = client.Close() })
		return client
	}
	first, peer := open(), open()
	key := first.SharedCacheKey("workspace", "viewer", "1.8", "viewer-one")
	want := bytes.Repeat([]byte("owned projected data\n"), 512)
	first.shared.Set(t.Context(), key, want, time.Second)
	if got, ok := peer.shared.Get(t.Context(), key); !ok || !bytes.Equal(got, want) {
		t.Fatal("peer did not read the exact encoded projection")
	}
	if _, ok := peer.shared.Get(t.Context(), peer.SharedCacheKey("workspace", "viewer", "1.9", "viewer-one")); ok {
		t.Fatal("a newer epoch reused an older entry")
	}
	bucket, _ := first.shared.entry(key)
	// Redis evicts the exact key that contains both the entries and their budget.
	if err := first.client.Del(t.Context(), bucket).Err(); err != nil {
		t.Fatal(err)
	}
	if _, ok := peer.shared.Get(t.Context(), key); ok {
		t.Fatal("the peer retained an evicted entry")
	}
}

func TestExternalRedisCoordinationRoundTrip(t *testing.T) {
	address := os.Getenv("FLOW_TEST_REDIS_ADDR")
	if address == "" {
		t.Skip("external Redis address is not set")
	}
	client, err := Open(t.Context(), Config{Mode: "standalone", Addrs: []string{address}, Prefix: "flow-integration", ConnectTimeout: 5 * time.Second, LockTTL: 5 * time.Second, LockWait: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Ping(t.Context()); err != nil {
		t.Fatal(err)
	}
	allowed, _, err := client.Allow(t.Context(), "integration-rate", 2, time.Minute)
	if err != nil || !allowed {
		t.Fatalf("rate limit: allowed=%v err=%v", allowed, err)
	}
	ran := false
	acquired, err := client.WithLeaderLock(t.Context(), "integration-scheduler", func() error { ran = true; return nil })
	if err != nil || !acquired || !ran {
		t.Fatalf("leader lock: acquired=%v ran=%v err=%v", acquired, ran, err)
	}
	user := domain.User{ID: "integration-user", DisplayName: "Integration User"}
	presenceValue := domain.Presence{ClientID: "client-1", User: user, IssueID: "issue-1", Route: "/issue/1", LastSeenAt: time.Now().UTC()}
	if _, err := client.UpdatePresence(t.Context(), "integration-workspace", "client-1", presenceValue, time.Minute); err != nil {
		t.Fatal(err)
	}
	presence, err := client.Presence(t.Context(), "integration-workspace", time.Minute)
	if err != nil || len(presence) != 1 || presence[0].User.ID != user.ID {
		t.Fatalf("presence=%#v err=%v", presence, err)
	}
	if _, err := client.RemovePresence(context.Background(), "integration-workspace", "client-1", time.Minute); err != nil {
		t.Fatal(err)
	}
}
