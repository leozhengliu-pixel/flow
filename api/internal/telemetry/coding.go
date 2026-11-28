package telemetry

import (
	"context"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/exemplar"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	metricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
)

const codingScope = "flow/coding"

const (
	legacyHTTPScope    = "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	legacyHTTPDuration = "http.server.request.duration"
	legacyHTTPRead     = "http.server.request.body.size"
	legacyHTTPWritten  = "http.server.response.body.size"
)

type codingMetric struct {
	kind  sdkmetric.InstrumentKind
	attrs map[attribute.Key]string // pipe-separated enum, or a typed rule
}

const (
	codingCounter = sdkmetric.InstrumentKindCounter
	codingGauge   = sdkmetric.InstrumentKindGauge
	codingHist    = sdkmetric.InstrumentKindHistogram
	sessionPhases = "awaitingApproval|queued|provisioning|starting|setup|running|awaitingUser|publishing|idle|stopping|done"
	sandboxKinds  = "session|snapshot|debug|warm"
)

var codingMetrics = map[string]codingMetric{
	"flow_coding_session_phase_seconds":     {codingHist, map[attribute.Key]string{"phase": sessionPhases, "outcome": "ok|error"}},
	"flow_coding_session_transitions_total": {codingCounter, map[attribute.Key]string{"from": sessionPhases, "to": sessionPhases}},
	"flow_coding_session_errors_total":      {codingCounter, map[attribute.Key]string{"code": "noCodeAccess|noGitHubConnection|noGitLabConnection|missingCommitSigningKey|noPolicyRepos|personalAccessDenied|githubTransientError|gitlabTransientError|repoNotInPolicy|requiresGithubConnection|requiresGitlabConnection|quotaExceeded|permissionDenied|approvalExpired|capacityUnavailable|sandboxDisabled|sandboxStartFailed|imagePullFailed|unschedulable|environmentSetupFailed|snapshotFailed|runnerLost|sandboxLost|turnTimeout|sessionTimeout|harnessFailed|modelUnavailable|pushFailed|pullRequestFailed|signingFailed|internal"}},
	"flow_coding_reconcile_seconds":         {codingHist, map[attribute.Key]string{"loop": "session|sandbox|warmpool|gc"}},
	"flow_coding_cost_micros_total":         {codingCounter, map[attribute.Key]string{"kind": "llm|runtime"}},
	"flow_coding_llm_unpriced_total":        {kind: codingCounter},
	"flow_coding_quota_rejections_total":    {kind: codingCounter},
	"flow_coding_runner_events_total":       {codingCounter, map[attribute.Key]string{"type": "thought|action|response|elicitation|error|plan|status|diff|usage", "duplicate": ":bool"}},
	"flow_coding_sandbox_start_seconds":     {codingHist, map[attribute.Key]string{"kind": sandboxKinds, "warm": "true|false"}},
	"flow_coding_sandboxes":                 {codingGauge, map[attribute.Key]string{"kind": sandboxKinds, "phase": "pending|starting|running|succeeded|failed|missing"}},
	"flow_coding_warm_pool_available":       {kind: codingGauge},
	"flow_coding_provider_errors_total":     {codingCounter, map[attribute.Key]string{"op": "start|stop|status|gc-delete", "transient": "false|true"}},
	"flow_coding_orphans_deleted_total":     {codingCounter, map[attribute.Key]string{"type": "pod|secret|pvc|volumesnapshot|container|volume|process|sandbox"}},
	"flow_coding_llm_requests_total":        {codingCounter, map[attribute.Key]string{"status": ":status"}},
	"flow_coding_llm_tokens_total":          {codingCounter, map[attribute.Key]string{"direction": "input|output|cacheRead|cacheWrite"}},
	"flow_coding_codehost_requests_total": {codingCounter, map[attribute.Key]string{"host": "github", "status": ":status",
		"op": "branch_head|check_runs|close_pull_request|comment|commit_status|compare|create_pull_request|find_pull_request|get_installation|get_pull_request|get_repository|get_user|label_pull_request|list_repositories|list_user_installations|mint_token|oauth_exchange|oauth_refresh|repo_installation|review_graphql|review_sync|revoke_token|update_pull_request|user_permission"}},
}

func (r *Recorder) allowed(m codingMetric, kv attribute.KeyValue) bool {
	rule, ok := m.attrs[kv.Key]
	if !ok {
		return false
	}
	if rule == ":bool" {
		return kv.Value.Type() == attribute.BOOL
	}
	if rule == ":status-code" {
		return kv.Value.Type() == attribute.INT64 && kv.Value.AsInt64() >= 100 && kv.Value.AsInt64() < 600
	}
	if kv.Value.Type() != attribute.STRING || !clean(kv.Value.AsString()) {
		return false
	}
	value := kv.Value.AsString()
	if rule == ":route" {
		return r.routes[value] != ""
	}
	if rule == ":status" {
		n, err := strconv.Atoi(value)
		return value == "error" || (err == nil && n >= 100 && n < 600 && strconv.Itoa(n) == value)
	}
	for item := range strings.SplitSeq(rule, "|") {
		if value == item {
			return true
		}
	}
	return false
}

var legacyHTTPSchema = codingMetric{codingHist, map[attribute.Key]string{
	"http.route":                ":route",
	"http.request.method":       "GET|HEAD|POST|PUT|PATCH|DELETE|OPTIONS|_OTHER",
	"http.response.status_code": ":status-code",
}}

func (r *Recorder) compatibilityRule(scope, name string) (codingMetric, bool) {
	switch scope {
	case codingScope:
		rule, ok := codingMetrics[name]
		return rule, ok
	case legacyHTTPScope:
		if r.options.Targets[metrics].Legacy {
			switch name {
			case legacyHTTPDuration, legacyHTTPRead, legacyHTTPWritten:
				return legacyHTTPSchema, true
			}
		}
	}
	return codingMetric{}, false
}

func (r *Recorder) compatibilityProvider() (*sdkmetric.MeterProvider, *sdkmetric.ManualReader) {
	var opts []sdkmetric.ManualReaderOption
	if r.options.Targets[metrics].Legacy {
		switch r.options.LegacyMetricsTemporality {
		case "delta":
			opts = append(opts, sdkmetric.WithTemporalitySelector(sdkmetric.DeltaTemporalitySelector))
		case "lowmemory":
			opts = append(opts, sdkmetric.WithTemporalitySelector(sdkmetric.LowMemoryTemporalitySelector))
		}
		if r.options.LegacyMetricsHistogram == "base2_exponential_bucket_histogram" {
			opts = append(opts, sdkmetric.WithAggregationSelector(func(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
				if kind == codingHist {
					return sdkmetric.AggregationBase2ExponentialHistogram{MaxSize: 160, MaxScale: 20}
				}
				return sdkmetric.DefaultAggregationSelector(kind)
			}))
		}
	}
	reader := sdkmetric.NewManualReader(opts...)
	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(reader), sdkmetric.WithResource(resource.Empty()),
		sdkmetric.WithExemplarFilter(exemplar.AlwaysOffFilter), sdkmetric.WithCardinalityLimit(128),
		sdkmetric.WithView(func(i sdkmetric.Instrument) (sdkmetric.Stream, bool) {
			rule, ok := r.compatibilityRule(i.Scope.Name, i.Name)
			if !ok || i.Kind != rule.kind {
				return sdkmetric.Stream{Name: i.Name, Aggregation: sdkmetric.AggregationDrop{}}, true
			}
			return sdkmetric.Stream{Name: i.Name, Unit: i.Unit, AttributeFilter: func(kv attribute.KeyValue) bool {
				return r.allowed(rule, kv)
			}}, true
		}),
	)
	return provider, reader
}

// InstallCompatibilityMetrics connects the existing instruments at startup.
func (r *Recorder) InstallCompatibilityMetrics() {
	provider, reader := r.compatibilityProvider()
	r.compat = reader
	otel.SetMeterProvider(provider)
	if r.options.Targets[metrics].Legacy {
		m := provider.Meter(legacyHTTPScope, otelmetric.WithInstrumentationVersion("0.72.0"))
		r.legacyHTTP = newLegacyHTTP(m)
	}
}

type legacyHTTP struct {
	duration    otelmetric.Float64Histogram
	read, write otelmetric.Int64Histogram
}

func newLegacyHTTP(m otelmetric.Meter) *legacyHTTP {
	duration, _ := m.Float64Histogram(legacyHTTPDuration, otelmetric.WithUnit("s"),
		otelmetric.WithExplicitBucketBoundaries(.005, .01, .025, .05, .075, .1, .25, .5, .75, 1, 2.5, 5, 7.5, 10))
	read, _ := m.Int64Histogram(legacyHTTPRead, otelmetric.WithUnit("By"))
	write, _ := m.Int64Histogram(legacyHTTPWritten, otelmetric.WithUnit("By"))
	return &legacyHTTP{duration, read, write}
}

func (m *legacyHTTP) record(ctx context.Context, l labels, code int, elapsed time.Duration, read, written int64) {
	if l.method == "OTHER" {
		l.method = "_OTHER"
	}
	attrs := otelmetric.WithAttributeSet(attribute.NewSet(
		attribute.String("http.route", l.route),
		attribute.String("http.request.method", l.method),
		attribute.Int("http.response.status_code", code),
	))
	m.duration.Record(ctx, elapsed.Seconds(), attrs)
	m.read.Record(ctx, read, attrs)
	m.write.Record(ctx, written, attrs)
}

func (r *Recorder) compatibilityAttributes(set attribute.Set, rule codingMetric) []*common.KeyValue {
	var out []*common.KeyValue
	for iter := set.Iter(); iter.Next(); {
		kv := iter.Attribute()
		if !r.allowed(rule, kv) && !(kv.Key == "otel.metric.overflow" && kv.Value.Type() == attribute.BOOL && kv.Value.AsBool()) {
			continue
		}
		switch kv.Value.Type() {
		case attribute.BOOL:
			out = append(out, &common.KeyValue{
				Key: string(kv.Key), Value: &common.AnyValue{Value: &common.AnyValue_BoolValue{BoolValue: kv.Value.AsBool()}},
			})
		case attribute.INT64:
			out = append(out, intAttr(string(kv.Key), kv.Value.AsInt64()))
		case attribute.STRING:
			out = append(out, strAttr(string(kv.Key), kv.Value.AsString()))
		}
	}
	return out
}

func (r *Recorder) collectCompatibilityMetrics(ctx context.Context) []*metricsv1.ScopeMetrics {
	var batch metricdata.ResourceMetrics
	if r.compat == nil || r.compat.Collect(ctx, &batch) != nil {
		return nil
	}
	var scopes []*metricsv1.ScopeMetrics
	for _, scope := range batch.ScopeMetrics {
		out := &metricsv1.ScopeMetrics{Scope: &common.InstrumentationScope{Name: scope.Scope.Name}}
		if scope.Scope.Name == legacyHTTPScope {
			out.Scope.Version = "0.72.0"
		}
		for _, m := range scope.Metrics {
			rule, ok := r.compatibilityRule(scope.Scope.Name, m.Name)
			if !ok {
				continue
			}
			row := &metricsv1.Metric{Name: m.Name}
			switch data := m.Data.(type) {
			case metricdata.Gauge[int64]:
				row.Data = &metricsv1.Metric_Gauge{Gauge: &metricsv1.Gauge{
					DataPoints: r.compatibilityPoints(data.DataPoints, rule, false),
				}}
			case metricdata.Sum[int64]:
				row.Data = &metricsv1.Metric_Sum{Sum: &metricsv1.Sum{
					IsMonotonic: data.IsMonotonic, AggregationTemporality: temporality(data.Temporality),
					DataPoints: r.compatibilityPoints(data.DataPoints, rule, true),
				}}
			case metricdata.Histogram[float64]:
				row.Unit = "s"
				row.Data = &metricsv1.Metric_Histogram{Histogram: compatibilityHistogram(r, data, rule)}
			case metricdata.Histogram[int64]:
				row.Unit = "By"
				row.Data = &metricsv1.Metric_Histogram{Histogram: compatibilityHistogram(r, data, rule)}
			case metricdata.ExponentialHistogram[float64]:
				row.Unit = "s"
				row.Data = &metricsv1.Metric_ExponentialHistogram{ExponentialHistogram: compatibilityExponential(r, data, rule)}
			case metricdata.ExponentialHistogram[int64]:
				row.Unit = "By"
				row.Data = &metricsv1.Metric_ExponentialHistogram{ExponentialHistogram: compatibilityExponential(r, data, rule)}
			default:
				continue
			}
			out.Metrics = append(out.Metrics, row)
		}
		if len(out.Metrics) > 0 {
			scopes = append(scopes, out)
		}
	}
	return scopes
}

func temporality(t metricdata.Temporality) metricsv1.AggregationTemporality {
	if t == metricdata.DeltaTemporality {
		return metricsv1.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA
	}
	return metricsv1.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE
}

func (r *Recorder) compatibilityPoints(points []metricdata.DataPoint[int64], rule codingMetric, sum bool) []*metricsv1.NumberDataPoint {
	out := make([]*metricsv1.NumberDataPoint, 0, len(points))
	for _, point := range points {
		p := &metricsv1.NumberDataPoint{
			Attributes: r.compatibilityAttributes(point.Attributes, rule), TimeUnixNano: nano(point.Time),
			Value: &metricsv1.NumberDataPoint_AsInt{AsInt: point.Value},
		}
		if sum {
			p.StartTimeUnixNano = nano(point.StartTime)
		}
		out = append(out, p)
	}
	return out
}

func compatibilityHistogram[N int64 | float64](r *Recorder, data metricdata.Histogram[N], rule codingMetric) *metricsv1.Histogram {
	out := &metricsv1.Histogram{AggregationTemporality: temporality(data.Temporality)}
	for _, point := range data.DataPoints {
		sum := float64(point.Sum)
		if math.IsNaN(sum) || math.IsInf(sum, 0) {
			continue
		}
		out.DataPoints = append(out.DataPoints, &metricsv1.HistogramDataPoint{
			Attributes: r.compatibilityAttributes(point.Attributes, rule), StartTimeUnixNano: nano(point.StartTime), TimeUnixNano: nano(point.Time),
			Count: point.Count, Sum: &sum, ExplicitBounds: slices.Clone(point.Bounds), BucketCounts: slices.Clone(point.BucketCounts),
		})
	}
	return out
}

func compatibilityExponential[N int64 | float64](r *Recorder, data metricdata.ExponentialHistogram[N], rule codingMetric) *metricsv1.ExponentialHistogram {
	out := &metricsv1.ExponentialHistogram{AggregationTemporality: temporality(data.Temporality)}
	buckets := func(bucket metricdata.ExponentialBucket) *metricsv1.ExponentialHistogramDataPoint_Buckets {
		return &metricsv1.ExponentialHistogramDataPoint_Buckets{Offset: bucket.Offset, BucketCounts: slices.Clone(bucket.Counts)}
	}
	for _, point := range data.DataPoints {
		sum := float64(point.Sum)
		if math.IsNaN(sum) || math.IsInf(sum, 0) {
			continue
		}
		out.DataPoints = append(out.DataPoints, &metricsv1.ExponentialHistogramDataPoint{
			Attributes: r.compatibilityAttributes(point.Attributes, rule), StartTimeUnixNano: nano(point.StartTime), TimeUnixNano: nano(point.Time),
			Count: point.Count, Sum: &sum, Scale: point.Scale, ZeroCount: point.ZeroCount, ZeroThreshold: point.ZeroThreshold,
			Positive: buckets(point.PositiveBucket), Negative: buckets(point.NegativeBucket),
		})
	}
	return out
}
