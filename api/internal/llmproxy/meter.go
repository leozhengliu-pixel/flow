package llmproxy

import (
	"context"
	"errors"
	"sync"
	"time"

	"flow/api/internal/domain"
)

// UsageRecorder is the part of track B's coding.UsageStore the proxy writes to.
type UsageRecorder interface {
	RecordCodingUsage(ctx context.Context, record domain.CodingUsageRecord) error
}

// SpendChecker is track B's coding.SpendGuard (§20): it returns an error when
// the workspace, user or loop limit for the current window is reached. A
// limit error must satisfy IsSpendLimit; any other error is treated as the
// check being unavailable.
type SpendChecker interface {
	Check(ctx context.Context, workspaceKey, userID, loopID string) error
}

// SpendCheckerFunc adapts a function to SpendChecker.
type SpendCheckerFunc func(ctx context.Context, workspaceKey, userID, loopID string) error

// Check calls f.
func (f SpendCheckerFunc) Check(ctx context.Context, workspaceKey, userID, loopID string) error {
	return f(ctx, workspaceKey, userID, loopID)
}

// IsSpendLimit reports whether err means "over the spend limit": ErrSpendLimit
// itself, or an error carrying the quotaExceeded coding error code through a
// CodingErrorCode() or Code() method.
func IsSpendLimit(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrSpendLimit) {
		return true
	}
	var coded interface{ CodingErrorCode() domain.CodingErrorCode }
	if errors.As(err, &coded) && coded.CodingErrorCode() == domain.CodingErrQuotaExceeded {
		return true
	}
	var code interface{ Code() domain.CodingErrorCode }
	return errors.As(err, &code) && code.Code() == domain.CodingErrQuotaExceeded
}

// DefaultSpendCacheTTL is how long a spend decision is reused (§12: "a 30s
// cached sum"), so the proxy does not sum usage on every request.
const DefaultSpendCacheTTL = 30 * time.Second

// GuardedMeter is the Meter Flow wires: usage goes to the usage store and spend
// is checked through the spend guard, with decisions cached per
// (workspace, user, loop) for CacheTTL. Transient guard failures are never
// cached.
type GuardedMeter struct {
	Usage    UsageRecorder
	Guard    SpendChecker // nil: spend is not enforced
	CacheTTL time.Duration
	Now      func() time.Time

	mu    sync.Mutex
	cache map[string]spendDecision
}

type spendDecision struct {
	err     error
	expires time.Time
}

func (m *GuardedMeter) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

// CheckSpend returns an error wrapping ErrSpendLimit when over the limit, nil
// when allowed, and any other error when the check could not be made.
func (m *GuardedMeter) CheckSpend(ctx context.Context, caller Caller) error {
	if m.Guard == nil {
		return nil
	}
	key := caller.WorkspaceKey + "\x00" + caller.UserID + "\x00" + caller.LoopID
	now := m.now()
	m.mu.Lock()
	if decision, ok := m.cache[key]; ok && now.Before(decision.expires) {
		m.mu.Unlock()
		return decision.err
	}
	m.mu.Unlock()
	err := m.Guard.Check(ctx, caller.WorkspaceKey, caller.UserID, caller.LoopID)
	if err != nil && !IsSpendLimit(err) {
		return err
	}
	if err != nil && !errors.Is(err, ErrSpendLimit) {
		err = errors.Join(ErrSpendLimit, err)
	}
	ttl := m.CacheTTL
	if ttl <= 0 {
		ttl = DefaultSpendCacheTTL
	}
	m.mu.Lock()
	if m.cache == nil {
		m.cache = map[string]spendDecision{}
	}
	if len(m.cache) > 10000 {
		for k, decision := range m.cache {
			if !now.Before(decision.expires) {
				delete(m.cache, k)
			}
		}
	}
	m.cache[key] = spendDecision{err: err, expires: now.Add(ttl)}
	m.mu.Unlock()
	return err
}

// RecordUsage writes the usage record.
func (m *GuardedMeter) RecordUsage(ctx context.Context, record domain.CodingUsageRecord) error {
	if m.Usage == nil {
		return errors.New("llmproxy: no usage store")
	}
	return m.Usage.RecordCodingUsage(ctx, record)
}
