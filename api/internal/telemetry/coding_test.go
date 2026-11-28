package telemetry

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	metricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestTelemetryCodingExportIsAllowlisted(t *testing.T) {
	r, err := New(Options{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	provider, reader := r.compatibilityProvider()
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	r.compat = reader
	m := provider.Meter(codingScope)
	tokens, _ := m.Int64Counter("flow_coding_llm_tokens_total")
	tokens.Add(t.Context(), 10, metric.WithAttributes(attribute.String("model", "owner@example.test"), attribute.String("direction", "input")))
	tokens.Add(t.Context(), 12, metric.WithAttributes(attribute.String("model", "fixture-token"), attribute.String("direction", "input")))
	tokens.Add(t.Context(), 3, metric.WithAttributes(attribute.Int("direction", 6), attribute.String("url", "https://example.test/secret")))
	gauge, _ := m.Int64Gauge("flow_coding_sandboxes")
	gauge.Record(t.Context(), 2, metric.WithAttributes(attribute.String("phase", "running"), attribute.String("region", "fixture-region")))
	unknown, _ := m.Int64Counter("flow_coding_fixture_user_id")
	unknown.Add(t.Context(), 1)
	other, _ := provider.Meter("fixture-private-scope").Int64Counter("flow_coding_llm_tokens_total")
	other.Add(t.Context(), 1)
	out, err := protojson.Marshal(r.metricData(t.Context(), time.Now(), extrema{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"owner@", "fixture-", "example.test", "flow_coding_fixture_user_id", `"region"`, `"model"`} {
		if strings.Contains(string(out), bad) {
			t.Fatal("coding export retained an undeclared field")
		}
	}
	for _, want := range []string{"flow_coding_llm_tokens_total", `"input"`, `"22"`, `"running"`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("coding export lost the allowed value %q", want)
		}
	}
}

func TestTelemetryLegacyHTTPMetricsRetainNamesAndPreferences(t *testing.T) {
	r, err := New(Options{
		Enabled: true, Targets: [3]Target{metrics: {Legacy: true}},
		Routes:                   []string{"/api/issues/{id}"},
		LegacyMetricsTemporality: "delta",
		LegacyMetricsHistogram:   "base2_exponential_bucket_histogram",
	})
	if err != nil {
		t.Fatal(err)
	}
	provider, reader := r.compatibilityProvider()
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	r.compat = reader
	meter := provider.Meter(legacyHTTPScope)
	r.legacyHTTP = newLegacyHTTP(meter)
	ctx := r.BeginRequest(t.Context(), "/api/issues/{id}", "PATCH")
	r.EndRequest(ctx, 503, true, 10)
	// The SDK view and the encoder both apply the typed allowlist.
	r.legacyHTTP.duration.Record(ctx, 1, metric.WithAttributes(
		attribute.String("http.route", "/api/issues/fixture-private"),
		attribute.String("http.response.status_code", "fixture-secret"),
		attribute.String("server.address", "private@example.test"),
	))
	var seen int
	scopes := r.collectCompatibilityMetrics(ctx)
	for _, scope := range scopes {
		for _, m := range scope.Metrics {
			h := m.GetExponentialHistogram()
			if h == nil || h.AggregationTemporality != metricsv1.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA {
				t.Fatal("legacy metric aggregation options did not apply")
			}
			switch m.Name {
			case legacyHTTPDuration, legacyHTTPRead, legacyHTTPWritten:
				seen++
			}
		}
		encoded, _ := protojson.Marshal(scope)
		for _, bad := range []string{"fixture-", "private@", "server.address"} {
			if strings.Contains(string(encoded), bad) {
				t.Fatal("legacy metrics retained a private value")
			}
		}
	}
	if seen != 3 {
		t.Fatal("missing legacy HTTP metric")
	}
}
