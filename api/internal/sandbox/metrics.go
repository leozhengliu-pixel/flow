package sandbox

import (
	"context"
	"strconv"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Metrics are the sandbox instruments of the flow/coding meter (design §21).
// Flow exports them over OTLP (telemetry.go); without a configured exporter
// the global meter is a no-op.
type Metrics struct {
	startSeconds     metric.Float64Histogram
	sandboxes        metric.Int64Gauge
	warmAvailable    metric.Int64Gauge
	reconcileSeconds metric.Float64Histogram
	providerErrors   metric.Int64Counter
	orphansDeleted   metric.Int64Counter
	costMicros       metric.Int64Counter
}

var (
	metricsOnce sync.Once
	metrics     *Metrics
)

// DefaultMetrics returns the process-wide instruments.
func DefaultMetrics() *Metrics {
	metricsOnce.Do(func() { metrics = NewMetrics(otel.Meter("flow/coding")) })
	return metrics
}

// NewMetrics creates the instruments on a meter (instrument errors leave
// that instrument nil, which every recorder tolerates).
func NewMetrics(meter metric.Meter) *Metrics {
	m := &Metrics{}
	m.startSeconds, _ = meter.Float64Histogram("flow_coding_sandbox_start_seconds", metric.WithUnit("s"), metric.WithDescription("Time from sandbox record creation to a running runner"))
	m.sandboxes, _ = meter.Int64Gauge("flow_coding_sandboxes", metric.WithDescription("Sandboxes by kind and observed phase"))
	m.warmAvailable, _ = meter.Int64Gauge("flow_coding_warm_pool_available", metric.WithDescription("Unbound warm sandboxes by size and region"))
	m.reconcileSeconds, _ = meter.Float64Histogram("flow_coding_reconcile_seconds", metric.WithUnit("s"), metric.WithDescription("Duration of one reconcile pass"))
	m.providerErrors, _ = meter.Int64Counter("flow_coding_provider_errors_total", metric.WithDescription("Sandbox provider errors by operation"))
	m.orphansDeleted, _ = meter.Int64Counter("flow_coding_orphans_deleted_total", metric.WithDescription("Orphaned provider objects deleted by GC"))
	m.costMicros, _ = meter.Int64Counter("flow_coding_cost_micros_total", metric.WithDescription("Metered coding cost in micro-USD"))
	return m
}

func (m *Metrics) sandboxStarted(kind Kind, size string, warm bool, seconds float64) {
	if m == nil || m.startSeconds == nil {
		return
	}
	m.startSeconds.Record(context.Background(), seconds, metric.WithAttributes(attribute.String("kind", string(kind)), attribute.String("size", size), attribute.String("warm", strconv.FormatBool(warm))))
}

func (m *Metrics) sandboxCount(kind Kind, phase Phase, n int) {
	if m == nil || m.sandboxes == nil {
		return
	}
	m.sandboxes.Record(context.Background(), int64(n), metric.WithAttributes(attribute.String("kind", string(kind)), attribute.String("phase", string(phase))))
}

func (m *Metrics) warmPool(size, region string, n int) {
	if m == nil || m.warmAvailable == nil {
		return
	}
	m.warmAvailable.Record(context.Background(), int64(n), metric.WithAttributes(attribute.String("size", size), attribute.String("region", region)))
}

func (m *Metrics) reconciled(loop string, started time.Time) {
	if m == nil || m.reconcileSeconds == nil {
		return
	}
	m.reconcileSeconds.Record(context.Background(), time.Since(started).Seconds(), metric.WithAttributes(attribute.String("loop", loop)))
}

func (m *Metrics) providerError(op string, err error) {
	if m == nil || m.providerErrors == nil || err == nil {
		return
	}
	m.providerErrors.Add(context.Background(), 1, metric.WithAttributes(attribute.String("op", op), attribute.String("transient", strconv.FormatBool(IsTransient(err)))))
}

func (m *Metrics) orphanDeleted(objectType string) {
	if m == nil || m.orphansDeleted == nil {
		return
	}
	m.orphansDeleted.Add(context.Background(), 1, metric.WithAttributes(attribute.String("type", objectType)))
}

func (m *Metrics) cost(kind string, micros int64) {
	if m == nil || m.costMicros == nil || micros <= 0 {
		return
	}
	m.costMicros.Add(context.Background(), micros, metric.WithAttributes(attribute.String("kind", kind)))
}
