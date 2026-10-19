package coding

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"flow/api/internal/domain"
)

// Metrics are the session core's OpenTelemetry instruments (meter
// flow/coding, design §21). A nil *Metrics records nothing.
type Metrics struct {
	phaseSeconds  metric.Float64Histogram
	errors        metric.Int64Counter
	reconcile     metric.Float64Histogram
	costTotal     metric.Int64Counter
	unpriced      metric.Int64Counter
	transitions   metric.Int64Counter
	events        metric.Int64Counter
	quotaRejected metric.Int64Counter
}

// NewMetrics registers the instruments on the global meter provider.
func NewMetrics() *Metrics {
	meter := otel.Meter("flow/coding")
	m := &Metrics{}
	m.phaseSeconds, _ = meter.Float64Histogram("flow_coding_session_phase_seconds", metric.WithUnit("s"), metric.WithDescription("Time spent in a coding session phase"))
	m.errors, _ = meter.Int64Counter("flow_coding_session_errors_total", metric.WithDescription("Coding sessions that ended with an error code"))
	m.reconcile, _ = meter.Float64Histogram("flow_coding_reconcile_seconds", metric.WithUnit("s"), metric.WithDescription("Duration of one reconcile pass"))
	m.costTotal, _ = meter.Int64Counter("flow_coding_cost_micros_total", metric.WithDescription("Metered coding spend in micro-USD"))
	m.unpriced, _ = meter.Int64Counter("flow_coding_llm_unpriced_total", metric.WithDescription("LLM usage recorded for a model without a configured price"))
	m.transitions, _ = meter.Int64Counter("flow_coding_session_transitions_total", metric.WithDescription("Coding session phase transitions"))
	m.events, _ = meter.Int64Counter("flow_coding_runner_events_total", metric.WithDescription("Runner events ingested"))
	m.quotaRejected, _ = meter.Int64Counter("flow_coding_quota_rejections_total", metric.WithDescription("Sessions or turns refused by a spend limit"))
	return m
}

func (m *Metrics) transition(ctx context.Context, from, to domain.CodingSessionPhase, spent time.Duration, code domain.CodingErrorCode) {
	if m == nil || from == to {
		return
	}
	outcome := "ok"
	if code != "" {
		outcome = "error"
	}
	m.phaseSeconds.Record(ctx, spent.Seconds(), metric.WithAttributes(attribute.String("phase", string(from)), attribute.String("outcome", outcome)))
	m.transitions.Add(ctx, 1, metric.WithAttributes(attribute.String("from", string(from)), attribute.String("to", string(to))))
	if to == domain.CodingPhaseDone && code != "" {
		m.errors.Add(ctx, 1, metric.WithAttributes(attribute.String("code", string(code))))
		if code == domain.CodingErrQuotaExceeded {
			m.quotaRejected.Add(ctx, 1)
		}
	}
}

func (m *Metrics) reconciled(ctx context.Context, spent time.Duration) {
	if m != nil {
		m.reconcile.Record(ctx, spent.Seconds(), metric.WithAttributes(attribute.String("loop", "session")))
	}
}

func (m *Metrics) cost(ctx context.Context, record domain.CodingUsageRecord) {
	if m != nil && record.CostMicros > 0 {
		m.costTotal.Add(ctx, record.CostMicros, metric.WithAttributes(attribute.String("kind", record.Kind)))
	}
}

func (m *Metrics) unpricedModel(ctx context.Context, model string) {
	if m != nil {
		m.unpriced.Add(ctx, 1, metric.WithAttributes(attribute.String("model", model)))
	}
}

func (m *Metrics) event(ctx context.Context, kind string, duplicate bool) {
	if m != nil {
		m.events.Add(ctx, 1, metric.WithAttributes(attribute.String("type", kind), attribute.Bool("duplicate", duplicate)))
	}
}
