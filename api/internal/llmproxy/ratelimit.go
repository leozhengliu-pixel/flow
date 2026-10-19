package llmproxy

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// sessionLimiter rate-limits and caps in-flight requests per session. State is
// in memory per Flow replica: it protects upstream quotas and Flow itself from
// a runaway harness, while spend limits (persisted) bound cost.
type sessionLimiter struct {
	mu            sync.Mutex
	perMinute     int
	burst         int
	maxConcurrent int
	now           func() time.Time
	sessions      map[string]*sessionLimit
	lastSweep     time.Time
}

type sessionLimit struct {
	limiter  *rate.Limiter
	inFlight int
	lastSeen time.Time
}

const limiterIdleTTL = 15 * time.Minute

func newSessionLimiter(perMinute, burst, maxConcurrent int, now func() time.Time) *sessionLimiter {
	return &sessionLimiter{perMinute: perMinute, burst: burst, maxConcurrent: maxConcurrent, now: now, sessions: map[string]*sessionLimit{}}
}

// acquire takes a rate token and an in-flight slot. On success release must be
// called once the request ends. retryAfter is set when the request is refused.
func (l *sessionLimiter) acquire(sessionID string) (release func(), retryAfter time.Duration, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.sweep(now)
	entry := l.sessions[sessionID]
	if entry == nil {
		entry = &sessionLimit{limiter: rate.NewLimiter(rate.Limit(float64(l.perMinute)/60), l.burst)}
		l.sessions[sessionID] = entry
	}
	entry.lastSeen = now
	if entry.inFlight >= l.maxConcurrent {
		return nil, time.Second, false
	}
	reservation := entry.limiter.ReserveN(now, 1)
	if !reservation.OK() {
		return nil, time.Minute, false
	}
	if delay := reservation.DelayFrom(now); delay > 0 {
		reservation.CancelAt(now)
		return nil, delay, false
	}
	entry.inFlight++
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			entry.inFlight--
			entry.lastSeen = l.now()
			l.mu.Unlock()
		})
	}, 0, true
}

// sweep drops idle sessions so the map stays bounded; caller holds mu.
func (l *sessionLimiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < time.Minute {
		return
	}
	l.lastSweep = now
	for id, entry := range l.sessions {
		if entry.inFlight == 0 && now.Sub(entry.lastSeen) > limiterIdleTTL {
			delete(l.sessions, id)
		}
	}
}
