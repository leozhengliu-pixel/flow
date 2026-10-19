package llmproxy

import (
	"context"
	"strconv"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Instruments of meter flow/coding (design §21). They are created lazily from
// the global MeterProvider, which telemetry.go installs when OTLP is on.
type instruments struct {
	requests metric.Int64Counter
	tokens   metric.Int64Counter
	cost     metric.Int64Counter
	unpriced metric.Int64Counter
}

var (
	instrumentsOnce sync.Once
	proxyMetrics    instruments
)

func metrics() *instruments {
	instrumentsOnce.Do(func() {
		meter := otel.Meter("flow/coding")
		proxyMetrics.requests, _ = meter.Int64Counter("flow_coding_llm_requests_total", metric.WithDescription("LLM proxy requests by upstream and status"))
		proxyMetrics.tokens, _ = meter.Int64Counter("flow_coding_llm_tokens_total", metric.WithDescription("Tokens metered by the LLM proxy"))
		proxyMetrics.cost, _ = meter.Int64Counter("flow_coding_cost_micros_total", metric.WithDescription("Metered coding cost in micro-USD"))
		proxyMetrics.unpriced, _ = meter.Int64Counter("flow_coding_llm_unpriced_total", metric.WithDescription("LLM requests recorded at zero cost because the model has no configured price"))
	})
	return &proxyMetrics
}

func countRequest(ctx context.Context, upstream string, status int) {
	if m := metrics(); m.requests != nil {
		m.requests.Add(ctx, 1, metric.WithAttributes(attribute.String("upstream", upstream), attribute.String("status", strconv.Itoa(status))))
	}
}

func countUsage(ctx context.Context, model string, usage Usage, costMicros int64, priced bool) {
	m := metrics()
	if m.tokens != nil {
		for direction, count := range map[string]int64{"input": usage.InputTokens, "output": usage.OutputTokens, "cacheRead": usage.CacheReadTokens, "cacheWrite": usage.CacheWriteTokens} {
			if count > 0 {
				m.tokens.Add(ctx, count, metric.WithAttributes(attribute.String("model", model), attribute.String("direction", direction)))
			}
		}
	}
	if m.cost != nil && costMicros > 0 {
		m.cost.Add(ctx, costMicros, metric.WithAttributes(attribute.String("kind", "llm")))
	}
	if m.unpriced != nil && !priced {
		m.unpriced.Add(ctx, 1, metric.WithAttributes(attribute.String("model", model)))
	}
}
