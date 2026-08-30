//go:build integration

package coordination

import (
	"context"
	"os"
	"testing"
	"time"

	"flow/api/internal/domain"
)

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
