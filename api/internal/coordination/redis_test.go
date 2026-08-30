package coordination

import (
	"context"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"

	"github.com/alicebob/miniredis/v2"
	redisclient "github.com/redis/go-redis/v9"
)

func TestRedisSharedRateLimitPresenceAndEvents(t *testing.T) {
	redisServer := miniredis.RunT(t)
	first := openTestRedis(t, redisServer.Addr())
	second := openTestRedis(t, redisServer.Addr())
	defer first.Close()
	defer second.Close()
	ctx := t.Context()

	for index := 0; index < 2; index++ {
		allowed, _, err := first.Allow(ctx, "login:127.0.0.1", 2, time.Minute)
		if err != nil || !allowed {
			t.Fatalf("rate limit attempt %d = %v, %v", index, allowed, err)
		}
	}
	allowed, retry, err := second.Allow(ctx, "login:127.0.0.1", 2, time.Minute)
	if err != nil || allowed || retry <= 0 {
		t.Fatalf("shared rate limit = allowed %v, retry %v, err %v", allowed, retry, err)
	}

	now := time.Now().UTC()
	viewer := domain.User{ID: "user_1", DisplayName: "User"}
	if _, err := first.UpdatePresence(ctx, "workspace", "client_1", domain.Presence{ClientID: "client_1", User: viewer, LastSeenAt: now}, time.Minute); err != nil {
		t.Fatal(err)
	}
	presence, err := second.UpdatePresence(ctx, "workspace", "client_2", domain.Presence{ClientID: "client_2", User: viewer, LastSeenAt: now.Add(time.Millisecond)}, time.Minute)
	if err != nil || len(presence) != 2 || presence[0].ClientID != "client_1" || presence[1].ClientID != "client_2" {
		t.Fatalf("shared presence = %#v, %v", presence, err)
	}
	presence, err = first.RemovePresence(ctx, "workspace", "client_1", time.Minute)
	if err != nil || len(presence) != 1 || presence[0].ClientID != "client_2" {
		t.Fatalf("presence removal = %#v, %v", presence, err)
	}
	if _, err := first.UpdatePresence(ctx, "workspace", "stale", domain.Presence{ClientID: "stale", User: viewer, LastSeenAt: now}, time.Minute); err != nil {
		t.Fatal(err)
	}
	seenKey, _ := first.presenceKeys("workspace")
	redisServer.ZAdd(seenKey, float64(now.Add(-2*time.Minute).UnixMilli()), "stale")
	presence, changed, err := second.CleanupPresence(ctx, "workspace", time.Minute)
	if err != nil || !changed || slices.ContainsFunc(presence, func(item domain.Presence) bool { return item.ClientID == "stale" }) {
		t.Fatalf("presence cleanup = %#v, changed %v, err %v", presence, changed, err)
	}

	received := make(chan EventEnvelope, 1)
	listenCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	listenErr := make(chan error, 1)
	go func() { listenErr <- second.Listen(listenCtx, func(event EventEnvelope) { received <- event }) }()
	time.Sleep(20 * time.Millisecond)
	want := domain.RealtimeEvent{ID: "event_1", Type: "issue.updated", AggregateID: "issue_1", CreatedAt: now}
	if err := first.Publish(ctx, "workspace", want); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-received:
		if event.Workspace != "workspace" || event.Event.ID != want.ID || event.Source != first.InstanceID() {
			t.Fatalf("received event = %#v", event)
		}
	case err := <-listenErr:
		t.Fatalf("listener stopped: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Redis event")
	}
}

func TestRedisWorkspaceLockSerializesClients(t *testing.T) {
	redisServer := miniredis.RunT(t)
	first := openTestRedis(t, redisServer.Addr())
	second := openTestRedis(t, redisServer.Addr())
	defer first.Close()
	defer second.Close()

	entered := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- first.WithWorkspaceLock(t.Context(), "workspace", func() error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	var secondEntered atomic.Bool
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- second.WithWorkspaceLock(t.Context(), "workspace", func() error {
			secondEntered.Store(true)
			return nil
		})
	}()
	time.Sleep(80 * time.Millisecond)
	if secondEntered.Load() {
		t.Fatal("second client entered while the workspace lock was held")
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-secondDone:
		if err != nil || !secondEntered.Load() {
			t.Fatalf("second lock = entered %v, err %v", secondEntered.Load(), err)
		}
	case <-time.After(time.Second):
		t.Fatal("second client did not acquire the released lock")
	}
}

func TestRedisLeaderLockElectsSingleClient(t *testing.T) {
	redisServer := miniredis.RunT(t)
	first, second := openTestRedis(t, redisServer.Addr()), openTestRedis(t, redisServer.Addr())
	defer first.Close()
	defer second.Close()
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := first.WithLeaderLock(t.Context(), "deliveries", func() error { close(entered); <-release; return nil })
		done <- err
	}()
	<-entered
	secondRan := false
	acquired, err := second.WithLeaderLock(t.Context(), "deliveries", func() error { secondRan = true; return nil })
	if err != nil || acquired || secondRan {
		t.Fatalf("second leader = acquired %v ran %v err %v", acquired, secondRan, err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestClusterModeBuildsClusterClient(t *testing.T) {
	client, err := newClient(Config{Mode: "cluster", Addrs: []string{"redis-1:6379", "redis-2:6379"}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, ok := client.(*redisclient.ClusterClient); !ok {
		t.Fatalf("cluster mode client = %T", client)
	}
}

func openTestRedis(t *testing.T, address string) *Redis {
	t.Helper()
	client, err := Open(t.Context(), Config{Mode: "standalone", Addrs: []string{address}, Prefix: "test", ConnectTimeout: time.Second, LockTTL: time.Second, LockWait: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return client
}
