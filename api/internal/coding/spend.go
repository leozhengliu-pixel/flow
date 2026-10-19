package coding

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/llmproxy"
)

// Spend limits (design §20): Settings › Usage & limits stores workspace,
// per-user and per-loop limits in USD with a daily, weekly or monthly reset,
// plus per-user and per-loop overrides. SpendLimiter enforces them against
// the metered coding usage (coding_usage_records).

// Spend limit scopes reported in QuotaError.
const (
	SpendScopeWorkspace = "workspace"
	SpendScopeUser      = "user"
	SpendScopeLoop      = "loop"
)

// Reset defaults, as in the web app (spend-limits-model.ts): daily at 6 AM,
// weekly on Saturday, monthly on the day the workspace was created (≤ 28).
const (
	DefaultResetHour    = 6
	DefaultResetWeekday = time.Saturday
)

// QuotaError says which limit was reached; errors.Is(err, ErrQuotaExceeded).
type QuotaError struct {
	Scope       string
	LimitMicros int64
	SpentMicros int64
	WindowStart time.Time
}

func (e *QuotaError) Error() string {
	return fmt.Sprintf("coding: %s spend limit reached ($%.2f of $%.2f since %s)", e.Scope, float64(e.SpentMicros)/1e6, float64(e.LimitMicros)/1e6, e.WindowStart.UTC().Format(time.RFC3339))
}

func (e *QuotaError) Unwrap() error { return ErrQuotaExceeded }

// Is also matches llmproxy.ErrSpendLimit, so the LLM proxy (track E) maps a
// QuotaError to 402 quota_exceeded without knowing this package's errors.
func (e *QuotaError) Is(target error) bool {
	return target == ErrQuotaExceeded || target == llmproxy.ErrSpendLimit
}

// CodingErrorCode is the session error code of a spend limit.
func (e *QuotaError) CodingErrorCode() string { return string(domain.CodingErrQuotaExceeded) }

// SpendGuard is the generic spend check (chat and loops can adopt it too).
type SpendGuard interface {
	Check(ctx context.Context, workspaceKey, userID, loopID string) error
}

// SpendReader sums metered cost.
type SpendReader interface {
	CodingSpendMicros(ctx context.Context, query SpendQuery) (int64, error)
}

// SpendLimiter enforces SpendLimitSettings.
type SpendLimiter struct {
	Usage      SpendReader
	Workspaces WorkspaceSource
	Now        func() time.Time
}

// Check returns a *QuotaError when a limit that applies is reached. Loop
// sessions are charged to the loop limit; other sessions to the user limit;
// every session counts against the workspace limit.
func (g *SpendLimiter) Check(ctx context.Context, workspaceKey, userID, loopID string) error {
	info, err := g.Workspaces.CodingWorkspace(ctx, workspaceKey)
	if err != nil {
		return err
	}
	now := time.Now()
	if g.Now != nil {
		now = g.Now()
	}
	limits := info.Settings.SpendLimits
	type check struct {
		scope, reset string
		limit        float64
		query        SpendQuery
	}
	checks := []check{{SpendScopeWorkspace, limits.WorkspaceReset, limits.WorkspaceLimit, SpendQuery{WorkspaceKey: workspaceKey}}}
	if loopID != "" {
		checks = append(checks, check{SpendScopeLoop, limits.LoopReset, EffectiveLimit(limits.LoopLimit, limits.LoopOverrides, loopID), SpendQuery{WorkspaceKey: workspaceKey, LoopID: loopID}})
	} else if userID != "" {
		checks = append(checks, check{SpendScopeUser, limits.UserReset, EffectiveLimit(limits.UserLimit, limits.UserOverrides, userID), SpendQuery{WorkspaceKey: workspaceKey, UserID: userID}})
	}
	for _, item := range checks {
		if item.limit <= 0 {
			continue
		}
		start := WindowStart(item.reset, limits, info.CreatedAt, info.Location, now)
		item.query.Since = start
		spent, err := g.Usage.CodingSpendMicros(ctx, item.query)
		if err != nil {
			return err
		}
		limit := USDToMicros(item.limit)
		if spent >= limit {
			return &QuotaError{Scope: item.scope, LimitMicros: limit, SpentMicros: spent, WindowStart: start}
		}
	}
	return nil
}

// EffectiveLimit is the override for id when one is set (> 0), else the
// default. A zero override means "inherit" (the Usage & limits dialog clears
// an override by saving 0).
func EffectiveLimit(base float64, overrides map[string]float64, id string) float64 {
	if amount, ok := overrides[id]; ok && amount > 0 {
		return amount
	}
	return base
}

// USDToMicros converts dollars to micro-dollars.
func USDToMicros(usd float64) int64 { return int64(math.Round(usd * 1e6)) }

// WindowStart is the start of the current spend window for a reset
// frequency (daily, weekly or monthly; "" is weekly like the web app).
func WindowStart(reset string, limits domain.SpendLimitSettings, createdAt time.Time, location *time.Location, now time.Time) time.Time {
	if location == nil {
		location = time.UTC
	}
	now = now.In(location)
	hour := DefaultResetHour
	if limits.DailyResetHour != nil {
		hour = *limits.DailyResetHour
	}
	switch reset {
	case "daily":
		start := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, location)
		if start.After(now) {
			start = start.AddDate(0, 0, -1)
		}
		return start
	case "monthly":
		day := 1
		if limits.MonthlyResetDay != nil {
			day = *limits.MonthlyResetDay
		} else if !createdAt.IsZero() {
			day = min(createdAt.In(location).Day(), 28)
		}
		day = max(1, min(day, 28))
		start := time.Date(now.Year(), now.Month(), day, hour, 0, 0, 0, location)
		if start.After(now) {
			start = time.Date(now.Year(), now.Month()-1, day, hour, 0, 0, 0, location)
		}
		return start
	default:
		weekday := DefaultResetWeekday
		if limits.WeeklyResetDay != nil {
			weekday = time.Weekday(*limits.WeeklyResetDay)
		}
		back := (int(now.Weekday()) - int(weekday) + 7) % 7
		start := time.Date(now.Year(), now.Month(), now.Day()-back, hour, 0, 0, 0, location)
		if start.After(now) {
			start = start.AddDate(0, 0, -7)
		}
		return start
	}
}

// CachedSpendGuard caches decisions for TTL (the LLM proxy checks every
// request with a 30s cached sum, design §12).
type CachedSpendGuard struct {
	Guard SpendGuard
	TTL   time.Duration
	Now   func() time.Time

	mu      sync.Mutex
	entries map[string]cachedSpend
}

type cachedSpend struct {
	err     error
	expires time.Time
}

// Check answers from the cache when fresh.
func (c *CachedSpendGuard) Check(ctx context.Context, workspaceKey, userID, loopID string) error {
	now := time.Now()
	if c.Now != nil {
		now = c.Now()
	}
	key := workspaceKey + "\x00" + userID + "\x00" + loopID
	c.mu.Lock()
	if entry, ok := c.entries[key]; ok && now.Before(entry.expires) {
		c.mu.Unlock()
		return entry.err
	}
	c.mu.Unlock()
	err := c.Guard.Check(ctx, workspaceKey, userID, loopID)
	if err != nil && !errors.Is(err, ErrQuotaExceeded) {
		return err // store errors are not cached
	}
	ttl := c.TTL
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	c.mu.Lock()
	if c.entries == nil {
		c.entries = map[string]cachedSpend{}
	}
	c.entries[key] = cachedSpend{err: err, expires: now.Add(ttl)}
	c.mu.Unlock()
	return err
}

// Invalidate drops cached decisions of a workspace (after new usage is recorded).
func (c *CachedSpendGuard) Invalidate(workspaceKey string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key := range c.entries {
		if len(key) > len(workspaceKey) && key[:len(workspaceKey)+1] == workspaceKey+"\x00" {
			delete(c.entries, key)
		}
	}
}
