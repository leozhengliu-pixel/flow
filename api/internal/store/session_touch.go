package store

import (
	"context"
	"os"
	"strings"
	"time"
)

func sessionTouchInterval() time.Duration {
	value := strings.TrimSpace(os.Getenv("FLOW_SESSION_TOUCH_INTERVAL"))
	if value == "0" {
		return 0
	}
	interval, err := time.ParseDuration(value)
	if err != nil || interval <= 0 || interval > time.Minute {
		return time.Minute
	}
	return interval
}

func sessionTouchDue(lastSeenRaw string, now time.Time, interval time.Duration) bool {
	lastSeen, err := time.Parse(time.RFC3339Nano, lastSeenRaw)
	return interval == 0 || err != nil || now.Sub(lastSeen) >= interval
}

func (s *SQLiteStore) touchSession(ctx context.Context, hash, lastSeenRaw string, now time.Time) {
	interval := sessionTouchInterval()
	if !sessionTouchDue(lastSeenRaw, now, interval) {
		return
	}
	// Activity is best effort, as before. Compare the value read by authentication
	// so only one replica can touch it; compare elapsed time in Go, not in SQL.
	if interval == 0 {
		_, _ = s.db.ExecContext(ctx, `UPDATE auth_sessions SET last_seen_at=? WHERE token_hash=?`, now.Format(time.RFC3339Nano), hash)
		return
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE auth_sessions SET last_seen_at=? WHERE token_hash=? AND last_seen_at=?`, now.Format(time.RFC3339Nano), hash, lastSeenRaw)
}
