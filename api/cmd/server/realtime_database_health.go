package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"flow/api/internal/store"
)

type realtimeDatabaseHealth struct {
	mu        sync.Mutex
	checkedAt time.Time
	err       error
}

// Idle SSE streams share one probe per server rather than checking SQL on
// every heartbeat for every subscriber.
func (s *server) realtimeDatabaseError() error {
	health := &s.realtime.databaseHealth
	health.mu.Lock()
	defer health.mu.Unlock()
	if time.Since(health.checkedAt) >= 5*time.Second {
		ctx, cancel := context.WithTimeout(context.Background(), busyDeadlineRetryAfter)
		defer cancel()
		health.err = s.store.CheckDatabaseConnection(ctx)
		health.checkedAt = time.Now()
	}
	return health.err
}

// writeRealtimeRetry is used after the SSE response has started. The retry
// field carries no event or ID, so it leaves the resume cursor alone.
func writeRealtimeRetry(w http.ResponseWriter, err error) bool {
	// A policy source that kept moving while access was resolved is no
	// denial either: the client reconnects from its cursor and resolves again.
	if !databaseReadUnavailable(err) && !errors.Is(err, store.ErrEventAccessStale) {
		return false
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(realtimeWriteTimeout))
	if _, err := fmt.Fprintf(w, "retry: %d\n\n", busyDeadlineRetryAfter.Milliseconds()); err == nil {
		flushRealtime(w)
	}
	return true
}

func (client *realtimeSocketClient) retryForDatabase(err error) bool {
	if !databaseReadUnavailable(err) {
		return false
	}
	if client.closeForRetry != nil {
		client.closeForRetry()
	}
	return true
}
