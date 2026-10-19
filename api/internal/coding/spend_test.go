package coding

import (
	"context"
	"errors"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/llmproxy"
)

func intp(value int) *int { return &value }

func TestWindowStart(t *testing.T) {
	created := time.Date(2025, 3, 31, 9, 0, 0, 0, time.UTC) // monthly default: the 28th (≤ 28)
	// Saturday 2026-10-17 06:00 UTC is the default weekly reset before Sunday 2026-10-18 12:00.
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		reset  string
		limits domain.SpendLimitSettings
		now    time.Time
		want   time.Time
	}{
		{"daily after reset hour", "daily", domain.SpendLimitSettings{}, now, time.Date(2026, 10, 18, 6, 0, 0, 0, time.UTC)},
		{"daily before reset hour", "daily", domain.SpendLimitSettings{}, time.Date(2026, 10, 18, 5, 0, 0, 0, time.UTC), time.Date(2026, 10, 17, 6, 0, 0, 0, time.UTC)},
		{"daily custom hour", "daily", domain.SpendLimitSettings{DailyResetHour: intp(13)}, now, time.Date(2026, 10, 17, 13, 0, 0, 0, time.UTC)},
		{"weekly default Saturday", "weekly", domain.SpendLimitSettings{}, now, time.Date(2026, 10, 17, 6, 0, 0, 0, time.UTC)},
		{"empty reset is weekly", "", domain.SpendLimitSettings{}, now, time.Date(2026, 10, 17, 6, 0, 0, 0, time.UTC)},
		{"weekly on Monday", "weekly", domain.SpendLimitSettings{WeeklyResetDay: intp(1)}, now, time.Date(2026, 10, 12, 6, 0, 0, 0, time.UTC)},
		{"weekly reset day today, before the hour", "weekly", domain.SpendLimitSettings{WeeklyResetDay: intp(0), DailyResetHour: intp(13)}, now, time.Date(2026, 10, 11, 13, 0, 0, 0, time.UTC)},
		{"monthly default from workspace creation", "monthly", domain.SpendLimitSettings{}, now, time.Date(2026, 9, 28, 6, 0, 0, 0, time.UTC)},
		{"monthly explicit day", "monthly", domain.SpendLimitSettings{MonthlyResetDay: intp(3)}, now, time.Date(2026, 10, 3, 6, 0, 0, 0, time.UTC)},
		{"monthly across the year", "monthly", domain.SpendLimitSettings{MonthlyResetDay: intp(15)}, time.Date(2027, 1, 2, 0, 0, 0, 0, time.UTC), time.Date(2026, 12, 15, 6, 0, 0, 0, time.UTC)},
	}
	for _, test := range cases {
		if got := WindowStart(test.reset, test.limits, created, nil, test.now); !got.Equal(test.want) {
			t.Errorf("%s: got %s want %s", test.name, got, test.want)
		}
	}
}

type fakeSpend struct {
	byScope map[string]int64 // "", "user:<id>", "loop:<id>"
	queries []SpendQuery
}

func (f *fakeSpend) CodingSpendMicros(_ context.Context, query SpendQuery) (int64, error) {
	f.queries = append(f.queries, query)
	switch {
	case query.LoopID != "":
		return f.byScope["loop:"+query.LoopID], nil
	case query.UserID != "":
		return f.byScope["user:"+query.UserID], nil
	}
	return f.byScope[""], nil
}

type fakeWorkspaces struct{ info WorkspaceInfo }

func (f fakeWorkspaces) CodingWorkspace(context.Context, string) (WorkspaceInfo, error) {
	return f.info, nil
}

func TestSpendLimiterEnforcesWorkspaceUserAndLoopLimits(t *testing.T) {
	limits := domain.SpendLimitSettings{
		WorkspaceLimit: 100, UserLimit: 10, LoopLimit: 5, UserReset: "daily", LoopReset: "monthly",
		UserOverrides: map[string]float64{"vip": 50, "inherits": 0}, LoopOverrides: map[string]float64{"big": 20},
	}
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		spend  map[string]int64
		user   string
		loop   string
		scope  string
		limits domain.SpendLimitSettings
	}{
		{name: "under every limit", spend: map[string]int64{"": 1e6, "user:u": 1e6}, user: "u"},
		{name: "user limit reached", spend: map[string]int64{"user:u": 10e6}, user: "u", scope: SpendScopeUser},
		{name: "user override raises the limit", spend: map[string]int64{"user:vip": 40e6}, user: "vip"},
		{name: "user override reached", spend: map[string]int64{"user:vip": 50e6}, user: "vip", scope: SpendScopeUser},
		{name: "zero override inherits the default", spend: map[string]int64{"user:inherits": 10e6}, user: "inherits", scope: SpendScopeUser},
		{name: "workspace limit reached", spend: map[string]int64{"": 100e6}, user: "u", scope: SpendScopeWorkspace},
		{name: "loop limit reached", spend: map[string]int64{"loop:l": 5e6}, user: "u", loop: "l", scope: SpendScopeLoop},
		{name: "loop sessions skip the user limit", spend: map[string]int64{"user:u": 99e6, "loop:l": 1e6}, user: "u", loop: "l"},
		{name: "loop override", spend: map[string]int64{"loop:big": 19e6}, user: "u", loop: "big"},
		{name: "no limits configured", spend: map[string]int64{"": 1e12, "user:u": 1e12}, user: "u", limits: domain.SpendLimitSettings{}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			settings := limits
			if test.limits.WorkspaceReset != "" || test.name == "no limits configured" {
				settings = test.limits
			}
			spend := &fakeSpend{byScope: test.spend}
			guard := &SpendLimiter{Usage: spend, Workspaces: fakeWorkspaces{WorkspaceInfo{Settings: domain.WorkspaceSettings{SpendLimits: settings}}}, Now: func() time.Time { return now }}
			err := guard.Check(t.Context(), "w", test.user, test.loop)
			var quota *QuotaError
			if test.scope == "" {
				if err != nil {
					t.Fatalf("unexpected %v", err)
				}
				return
			}
			if !errors.As(err, &quota) || quota.Scope != test.scope || !errors.Is(err, ErrQuotaExceeded) {
				t.Fatalf("got %v, want %s quota", err, test.scope)
			}
		})
	}
	// Windows follow each limit's reset frequency.
	spend := &fakeSpend{byScope: map[string]int64{}}
	guard := &SpendLimiter{Usage: spend, Workspaces: fakeWorkspaces{WorkspaceInfo{Settings: domain.WorkspaceSettings{SpendLimits: limits}}}, Now: func() time.Time { return now }}
	_ = guard.Check(t.Context(), "w", "u", "")
	if len(spend.queries) != 2 || !spend.queries[0].Since.Equal(time.Date(2026, 10, 17, 6, 0, 0, 0, time.UTC)) || !spend.queries[1].Since.Equal(time.Date(2026, 10, 18, 6, 0, 0, 0, time.UTC)) {
		t.Fatalf("windows: %+v", spend.queries)
	}
}

type countingGuard struct {
	calls int
	err   error
}

func (g *countingGuard) Check(context.Context, string, string, string) error {
	g.calls++
	return g.err
}

func TestCachedSpendGuard(t *testing.T) {
	now := t0
	inner := &countingGuard{err: &QuotaError{Scope: SpendScopeUser}}
	cache := &CachedSpendGuard{Guard: inner, TTL: 30 * time.Second, Now: func() time.Time { return now }}
	for range 3 {
		if !errors.Is(cache.Check(t.Context(), "w", "u", ""), ErrQuotaExceeded) {
			t.Fatal("quota must be cached")
		}
	}
	if inner.calls != 1 {
		t.Fatalf("calls %d", inner.calls)
	}
	now = now.Add(31 * time.Second)
	inner.err = nil
	if cache.Check(t.Context(), "w", "u", "") != nil || inner.calls != 2 {
		t.Fatal("expired entries are re-checked")
	}
	inner.err = errors.New("db down")
	cache.Invalidate("w")
	if cache.Check(t.Context(), "w", "u", "") == nil || cache.Check(t.Context(), "w", "u", "") == nil || inner.calls != 4 {
		t.Fatal("store errors are not cached")
	}
}

func TestRuntimeAndLLMCost(t *testing.T) {
	if got := RuntimeCostMicros(map[string]float64{"medium": 0.20}, "medium", 1800); got != 100000 {
		t.Fatalf("half an hour of medium: %d", got)
	}
	if USDToMicros(12.345678) != 12345678 {
		t.Fatal("usd micros")
	}
}

func TestQuotaErrorMatchesTheProxyContract(t *testing.T) {
	var err error = &QuotaError{Scope: SpendScopeWorkspace}
	if !errors.Is(err, llmproxy.ErrSpendLimit) || !errors.Is(err, ErrQuotaExceeded) {
		t.Fatal("a quota error must match llmproxy.ErrSpendLimit and ErrQuotaExceeded")
	}
	var coded interface{ CodingErrorCode() string }
	if !errors.As(err, &coded) || coded.CodingErrorCode() != "quotaExceeded" {
		t.Fatal("CodingErrorCode must be quotaExceeded")
	}
}
