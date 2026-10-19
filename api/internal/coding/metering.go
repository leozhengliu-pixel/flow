package coding

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"time"

	"flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/llmproxy"
	"flow/api/internal/sandbox"
)

// Metering (design §20): LLM tokens are recorded per proxied request (track
// E calls Meter through llmproxy.Meter), sandbox runtime in 5-minute
// increments guarded by coding_sandboxes.billed_seconds. After each record
// the spend limits are checked again; a session over its limit is ended with
// quotaExceeded (the sandbox is stopped by the session reconciler).

// Usage kinds.
const (
	UsageLLM     = "llm"
	UsageRuntime = "runtime"
)

// RuntimeBillingIncrement is how often a running sandbox is billed.
const RuntimeBillingIncrement = 5 * time.Minute

// Meter records usage and enforces limits after each record.
type Meter struct {
	Store  CoreStore
	Guard  SpendGuard
	Config config.CodingConfig
	Now    func() time.Time
	// OnQuotaExceeded ends the session (Service.FailSession); nil skips it.
	OnQuotaExceeded func(ctx context.Context, workspaceKey, sessionID string, err error)
	Metrics         *Metrics
}

func (m *Meter) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

// LLMCostMicros prices a model's tokens (unknown models cost 0).
func LLMCostMicros(llm config.CodingLLMConfig, model string, input, output, cacheRead, cacheWrite int64) (int64, bool) {
	price, ok := llm.Prices[model]
	if !ok {
		return 0, false
	}
	return llmproxy.CostMicros(price, input, output, cacheRead, cacheWrite), true
}

// RuntimeCostMicros prices sandbox seconds at FLOW_CODING_RUNTIME_PRICES[size] per hour.
func RuntimeCostMicros(prices map[string]float64, size string, seconds int64) int64 {
	return int64(math.Round(prices[size] * 1e6 * float64(seconds) / 3600))
}

// Record stores one usage record (cost filled in when zero) and re-checks spend.
func (m *Meter) Record(ctx context.Context, record domain.CodingUsageRecord) error {
	if record.ID == "" {
		record.ID = NewID()
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = m.now().UTC()
	}
	if record.CostMicros == 0 {
		switch record.Kind {
		case UsageLLM:
			cost, known := LLMCostMicros(m.Config.LLM, record.Model, record.InputTokens, record.OutputTokens, record.CacheReadTokens, record.CacheWriteTokens)
			if !known && record.Model != "" {
				m.Metrics.unpricedModel(ctx, record.Model)
			}
			record.CostMicros = cost
		case UsageRuntime:
			record.CostMicros = RuntimeCostMicros(m.Config.RuntimePricesUSD, record.Size, record.RuntimeSeconds)
		}
	}
	if err := m.Store.RecordCodingUsage(ctx, record); err != nil {
		return err
	}
	m.Metrics.cost(ctx, record)
	m.enforce(ctx, record.WorkspaceKey, record.SessionID, record.UserID, record.LoopID)
	return nil
}

func (m *Meter) enforce(ctx context.Context, workspaceKey, sessionID, userID, loopID string) {
	if m.Guard == nil || sessionID == "" || m.OnQuotaExceeded == nil {
		return
	}
	if err := m.Guard.Check(ctx, workspaceKey, userID, loopID); errors.Is(err, ErrQuotaExceeded) {
		m.OnQuotaExceeded(ctx, workspaceKey, sessionID, err)
	} else if err != nil {
		slog.Warn("coding spend check failed", "session", sessionID, "error", err)
	}
}

// BillSandbox bills a session sandbox's runtime up to now: whole 5-minute
// increments while it runs and the exact remainder once it ended. The
// billed_seconds CAS makes concurrent or repeated calls bill each second once.
// Warm-pool sandboxes are never billed.
func (m *Meter) BillSandbox(ctx context.Context, record sandbox.Record, userID, loopID string) error {
	if record.Kind == sandbox.KindWarm || record.StartedAt == nil {
		return nil
	}
	end := m.now()
	ended := record.EndedAt != nil
	if ended {
		end = *record.EndedAt
	}
	elapsed := int64(end.Sub(*record.StartedAt) / time.Second)
	if elapsed <= 0 {
		return nil
	}
	target := elapsed
	if !ended {
		step := int64(RuntimeBillingIncrement / time.Second)
		target = elapsed / step * step
	}
	billed, err := m.Store.CodingSandboxBilledSeconds(ctx, record.ID)
	if err != nil || target <= billed {
		return err
	}
	ok, err := m.Store.AdvanceCodingSandboxBilled(ctx, record.ID, billed, target)
	if err != nil || !ok {
		return err // another biller advanced it first
	}
	return m.Record(ctx, domain.CodingUsageRecord{WorkspaceKey: record.WorkspaceKey, SessionID: record.SessionID, SandboxID: record.ID, UserID: userID, LoopID: loopID, Kind: UsageRuntime, RuntimeSeconds: target - billed, Size: record.Size})
}

// LLMMeter adapts the Meter and a SpendGuard to llmproxy.Meter (track E).
type LLMMeter struct {
	Meter *Meter
	Guard SpendGuard
}

var _ llmproxy.Meter = (*LLMMeter)(nil)

// CheckSpend answers llmproxy.ErrSpendLimit when the caller is over a limit.
func (l *LLMMeter) CheckSpend(ctx context.Context, caller llmproxy.Caller) error {
	if l.Guard == nil {
		return nil
	}
	err := l.Guard.Check(ctx, caller.WorkspaceKey, caller.UserID, caller.LoopID)
	if errors.Is(err, ErrQuotaExceeded) {
		return errors.Join(llmproxy.ErrSpendLimit, err)
	}
	return err
}

// RecordUsage records an LLM usage record.
func (l *LLMMeter) RecordUsage(ctx context.Context, record domain.CodingUsageRecord) error {
	if record.Kind == "" {
		record.Kind = UsageLLM
	}
	return l.Meter.Record(ctx, record)
}
