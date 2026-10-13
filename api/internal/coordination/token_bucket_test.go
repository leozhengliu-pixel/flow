package coordination

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func TestRedisTokenBucketIsSharedAndRefills(t *testing.T) {
	redisServer := miniredis.RunT(t)
	first := openTestRedis(t, redisServer.Addr())
	second := openTestRedis(t, redisServer.Addr())
	defer first.Close()
	defer second.Close()
	ctx := t.Context()
	now := time.UnixMilli(1_700_000_000_000)

	allowed, remaining, err := first.TakeTokens(ctx, "api:user_1", 3, time.Hour, 2, now)
	if err != nil || !allowed || remaining != 1 {
		t.Fatalf("first take = %v %v %v", allowed, remaining, err)
	}
	allowed, remaining, err = second.TakeTokens(ctx, "api:user_1", 3, time.Hour, 2, now)
	if err != nil || allowed || remaining != 1 {
		t.Fatalf("second instance must share the bucket: %v %v %v", allowed, remaining, err)
	}
	// A third of the period refills one token (capacity 3 per hour).
	allowed, remaining, err = second.TakeTokens(ctx, "api:user_1", 3, time.Hour, 2, now.Add(20*time.Minute))
	if err != nil || !allowed || remaining != 0 {
		t.Fatalf("refilled take = %v %v %v", allowed, remaining, err)
	}
	// Zero cost reads the level without consuming.
	allowed, remaining, err = first.TakeTokens(ctx, "api:user_1", 3, time.Hour, 0, now.Add(20*time.Minute))
	if err != nil || !allowed || remaining != 0 {
		t.Fatalf("peek = %v %v %v", allowed, remaining, err)
	}
	if allowed, remaining, err = first.TakeTokens(ctx, "api:user_2", 3, time.Hour, 1, now); err != nil || !allowed || remaining != 2 {
		t.Fatalf("separate key = %v %v %v", allowed, remaining, err)
	}
}
