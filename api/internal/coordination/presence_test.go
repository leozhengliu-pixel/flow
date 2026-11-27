package coordination

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"

	"github.com/alicebob/miniredis/v2"
	redisclient "github.com/redis/go-redis/v9"
)

type scaleRedisHook struct{ snapshots atomic.Int64 }

func (h *scaleRedisHook) DialHook(next redisclient.DialHook) redisclient.DialHook { return next }
func (h *scaleRedisHook) ProcessPipelineHook(next redisclient.ProcessPipelineHook) redisclient.ProcessPipelineHook {
	return next
}
func (h *scaleRedisHook) ProcessHook(next redisclient.ProcessHook) redisclient.ProcessHook {
	return func(ctx context.Context, command redisclient.Cmder) error {
		args := command.Args()
		if len(args) > 1 && fmt.Sprint(args[1]) == presenceSnapshotScript.Hash() {
			h.snapshots.Add(1)
		}
		return next(ctx, command)
	}
}

func TestScaleRedisMinimalHeartbeatDoesNotReadRoster(t *testing.T) {
	r := openTestRedis(t, miniredis.RunT(t).Addr())
	defer r.Close()
	hook := &scaleRedisHook{}
	r.client.AddHook(hook)
	ctx, now := t.Context(), time.Now().UTC()
	for i := range 2000 {
		id := fmt.Sprintf("client_%d", i)
		if changed, err := r.TouchPresence(ctx, "w", id, domain.Presence{ClientID: id, User: domain.User{ID: "user"}, LastSeenAt: now}, time.Minute); err != nil || !changed {
			t.Fatalf("join %d changed=%v err=%v", i, changed, err)
		}
	}
	for i := range 100 {
		value := domain.Presence{ClientID: "client_0", User: domain.User{ID: "user", AppTeamIDs: []string{}, AppScopes: []string{}}, LastSeenAt: now.Add(time.Duration(i+1) * time.Millisecond)}
		if changed, err := r.TouchPresence(ctx, "w", value.ClientID, value, time.Minute); err != nil || changed {
			t.Fatalf("heartbeat %d changed=%v err=%v", i, changed, err)
		}
	}
	if hook.snapshots.Load() != 0 {
		t.Fatal("minimal heartbeat read the full roster")
	}
	values, err := r.Presence(ctx, "w", time.Minute)
	if err != nil || len(values) != 2000 || hook.snapshots.Load() == 0 {
		t.Fatalf("legacy roster len=%d err=%v", len(values), err)
	}
	if changed, err := r.DeletePresence(ctx, "w", "client_0"); err != nil || !changed {
		t.Fatalf("leave changed=%v err=%v", changed, err)
	}
	if changed, err := r.DeletePresence(ctx, "w", "client_0"); err != nil || changed {
		t.Fatal("duplicate leave changed presence")
	}
}
