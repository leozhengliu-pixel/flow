package coding

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// Loops (docs/coding-sessions-design.md §15, track L). The "Autofix bugs"
// template's "Start a coding session" action may start sessions only on the
// repositories the workspace opened to Loops ("Repository access for Loops"),
// and the loop's spend is capped by SpendLimitSettings.LoopLimit and
// LoopOverrides.

// Loop repository access values (CodingAgentSettings.LoopRepositoryAccess).
const (
	LoopRepositoryAccessNone     = "none"
	LoopRepositoryAccessAll      = "all"
	LoopRepositoryAccessSelected = "selected"
)

// LoopEnvironmentAllowed reports whether a Loop may start a session on the
// environment. "" is read as none (the default).
func LoopEnvironmentAllowed(settings domain.CodingAgentSettings, environmentID string) bool {
	switch strings.TrimSpace(settings.LoopRepositoryAccess) {
	case LoopRepositoryAccessAll:
		return true
	case LoopRepositoryAccessSelected:
		return environmentID != "" && slices.Contains(settings.LoopRepositories, environmentID)
	default:
		return false
	}
}

// LoopEnvironments keeps the environments Loops may use.
func LoopEnvironments(settings domain.CodingAgentSettings, environments []domain.CodingEnvironment) []domain.CodingEnvironment {
	return FilterEnvironments(environments, func(environment domain.CodingEnvironment) bool {
		return LoopEnvironmentAllowed(settings, environment.ID)
	})
}

// LoopSpendLimitMicros is the loop's spend cap in micro-USD: the per-loop
// override, else the workspace's loop limit; ok is false when unlimited.
func LoopSpendLimitMicros(limits domain.SpendLimitSettings, loopID string) (int64, bool) {
	limit := limits.LoopLimit
	if override, found := limits.LoopOverrides[loopID]; found {
		limit = override
	}
	if limit <= 0 {
		return 0, false
	}
	return int64(limit * 1_000_000), true
}

// SpendWindowStart is the start of the current spend window for a reset
// period: daily (at DailyResetHour, UTC), weekly (WeeklyResetDay, 0 =
// Sunday), or monthly (MonthlyResetDay, clamped to the month's length). An
// unknown or empty period is monthly, Flow's Usage & limits default.
func SpendWindowStart(reset string, limits domain.SpendLimitSettings, now time.Time) time.Time {
	now = now.UTC()
	hour := 0
	if limits.DailyResetHour != nil && *limits.DailyResetHour >= 0 && *limits.DailyResetHour < 24 {
		hour = *limits.DailyResetHour
	}
	switch strings.ToLower(strings.TrimSpace(reset)) {
	case "daily", "day":
		start := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, time.UTC)
		if start.After(now) {
			start = start.AddDate(0, 0, -1)
		}
		return start
	case "weekly", "week":
		day := 0
		if limits.WeeklyResetDay != nil && *limits.WeeklyResetDay >= 0 && *limits.WeeklyResetDay < 7 {
			day = *limits.WeeklyResetDay
		}
		start := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, time.UTC)
		start = start.AddDate(0, 0, -((int(now.Weekday()) - day + 7) % 7))
		if start.After(now) {
			start = start.AddDate(0, 0, -7)
		}
		return start
	default:
		day := 1
		if limits.MonthlyResetDay != nil && *limits.MonthlyResetDay >= 1 && *limits.MonthlyResetDay <= 31 {
			day = *limits.MonthlyResetDay
		}
		start := monthDay(now.Year(), now.Month(), day, hour)
		if start.After(now) {
			previous := now.AddDate(0, 0, -now.Day()) // last day of the previous month
			start = monthDay(previous.Year(), previous.Month(), day, hour)
		}
		return start
	}
}

func monthDay(year int, month time.Month, day, hour int) time.Time {
	last := time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
	return time.Date(year, month, min(day, last), hour, 0, 0, 0, time.UTC)
}

// CheckLoopSpend fails with quotaExceeded when the loop has used its limit
// in the current window. A nil reader or an unlimited loop passes.
func CheckLoopSpend(ctx context.Context, reader SpendReader, workspaceKey, loopID string, limits domain.SpendLimitSettings, now time.Time) error {
	if reader == nil || loopID == "" {
		return nil
	}
	limit, limited := LoopSpendLimitMicros(limits, loopID)
	if !limited {
		return nil
	}
	spent, err := reader.CodingSpendMicros(ctx, SpendQuery{WorkspaceKey: workspaceKey, LoopID: loopID, Since: SpendWindowStart(limits.LoopReset, limits, now)})
	if err != nil {
		return fmt.Errorf("coding: loop spend: %w", err)
	}
	if spent >= limit {
		return &TriggerError{Code: domain.CodingErrQuotaExceeded, Message: fmt.Sprintf("This loop reached its coding spend limit (%.2f USD).", float64(limit)/1_000_000)}
	}
	return nil
}
