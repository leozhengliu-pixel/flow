package telemetry

import (
	"math"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	metricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
)

type measure uint8

const (
	httpDuration measure = iota
	httpRequestSize
	httpResponseSize
	httpBusy
	httpUnavailable
	httpInFlight
	documentSize
	sqlDuration
	dbRetries
	dbFailures
	mcpDuration
	jobDuration
	externalDuration
	realtimeFrames
	browserLCP
	browserINP
	browserCLS
	browserTTFB
	browserNavigation
	browserTask
	browserMemory
	browserAPI
	browserReconnect
	browserStorage
	browserDropped
	droppedRecords
	droppedBatches
	droppedSeries
	workRefusals
)

type aggregation uint8

const (
	gauge aggregation = iota
	counter
	timeHistogram
	sizeHistogram
	scoreHistogram
)

type metricSpec struct {
	name, unit string
	form       aggregation
}

var measurements = [...]metricSpec{
	httpDuration:      {"flow.http.server.duration", "s", timeHistogram},
	httpRequestSize:   {"flow.http.server.request.size", "By", sizeHistogram},
	httpResponseSize:  {"flow.http.server.response.size", "By", sizeHistogram},
	httpBusy:          {"flow.http.server.busy", "{response}", counter},
	httpUnavailable:   {"flow.http.server.unavailable", "{response}", counter},
	httpInFlight:      {"flow.http.server.in_flight", "{request}", gauge},
	documentSize:      {"flow.document.save.size", "By", sizeHistogram},
	sqlDuration:       {"flow.db.statement.duration", "s", timeHistogram},
	dbRetries:         {"flow.db.transaction.retries", "{retry}", counter},
	dbFailures:        {"flow.db.transaction.failures", "{failure}", counter},
	mcpDuration:       {"flow.mcp.tool.duration", "s", timeHistogram},
	jobDuration:       {"flow.background.job.duration", "s", timeHistogram},
	externalDuration:  {"flow.external.call.duration", "s", timeHistogram},
	realtimeFrames:    {"flow.realtime.frame.size", "By", sizeHistogram},
	browserLCP:        {"flow.browser.lcp", "s", timeHistogram},
	browserINP:        {"flow.browser.inp", "s", timeHistogram},
	browserCLS:        {"flow.browser.cls", "1", scoreHistogram},
	browserTTFB:       {"flow.browser.ttfb", "s", timeHistogram},
	browserNavigation: {"flow.browser.navigation.duration", "s", timeHistogram},
	browserTask:       {"flow.browser.long_task.duration", "s", timeHistogram},
	browserMemory:     {"flow.browser.heap.size", "By", sizeHistogram},
	browserAPI:        {"flow.browser.api.requests", "{request}", counter},
	browserReconnect:  {"flow.browser.realtime.reconnects", "{reconnect}", counter},
	browserStorage:    {"flow.browser.local_store.failures", "{failure}", counter},
	browserDropped:    {"flow.browser.dropped", "{record}", counter},
	droppedRecords:    {"flow.telemetry.dropped_records", "{record}", counter},
	droppedBatches:    {"flow.telemetry.dropped_batches", "{batch}", counter},
	droppedSeries:     {"flow.telemetry.dropped_series", "{measurement}", counter},
	workRefusals:      {"flow.metadata.work.refusals", "{refusal}", counter},
}

const bucketCount = 15

var durationBounds = [bucketCount - 1]float64{.001, .0025, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 15, 60}
var sizeBounds = [bucketCount - 1]float64{0, 256, 1024, 4096, 16 << 10, 64 << 10, 256 << 10, 1 << 20, 4 << 20, 16 << 20, 32 << 20, 64 << 20, 128 << 20, 512 << 20}
var scoreBounds = [bucketCount - 1]float64{0, .01, .025, .05, .075, .1, .15, .2, .25, .4, .6, 1, 2, 5}

func bounds(form aggregation) []float64 {
	switch form {
	case timeHistogram:
		return durationBounds[:]
	case sizeHistogram:
		return sizeBounds[:]
	default:
		return scoreBounds[:]
	}
}

type labels struct {
	route, method, status, kind string
}

type seriesKey struct {
	measure measure
	labels
}

type histogram struct {
	buckets [bucketCount]uint64
	count   uint64
	sum     float64
}

const maxSeries = 8192

type instruments struct {
	mu      sync.Mutex
	limit   int
	hist    map[seriesKey]*histogram
	count   map[seriesKey]int64
	dropped atomic.Int64
}

func newInstruments(limit int) instruments {
	return instruments{limit: limit, hist: map[seriesKey]*histogram{}, count: map[seriesKey]int64{}}
}

func (i *instruments) observe(key seriesKey, value float64) {
	h := i.hist[key]
	if h == nil {
		if len(i.hist)+len(i.count) >= i.limit {
			i.dropped.Add(1)
			return
		}
		h = &histogram{}
		i.hist[key] = h
	}
	bucket, _ := slices.BinarySearch(bounds(measurements[key.measure].form), value)
	h.buckets[bucket]++
	h.count++
	h.sum += value
}

func (i *instruments) add(key seriesKey, value int64) {
	if _, ok := i.count[key]; !ok && len(i.hist)+len(i.count) >= i.limit {
		i.dropped.Add(1)
		return
	}
	i.count[key] += value
}

func (r *Recorder) requests(l labels, elapsed time.Duration, read, written int64, unavailable, busy bool) {
	i := &r.instruments
	i.mu.Lock()
	defer i.mu.Unlock()
	i.observe(seriesKey{httpDuration, l}, elapsed.Seconds())
	i.observe(seriesKey{httpRequestSize, l}, float64(read))
	i.observe(seriesKey{httpResponseSize, l}, float64(written))
	if busy {
		i.add(seriesKey{httpBusy, l}, 1)
	}
	if unavailable {
		i.add(seriesKey{httpUnavailable, l}, 1)
	}
}

func (r *Recorder) observe(m measure, l labels, value float64) {
	r.instruments.observeValue(m, l, value)
}

func (i *instruments) observeValue(m measure, l labels, value float64) {
	i.mu.Lock()
	i.observe(seriesKey{m, l}, value)
	i.mu.Unlock()
}

func (r *Recorder) add(m measure, l labels, value int64) {
	r.instruments.addValue(m, l, value)
}

func (i *instruments) addValue(m measure, l labels, value int64) {
	i.mu.Lock()
	i.add(seriesKey{m, l}, value)
	i.mu.Unlock()
}

func (r *Recorder) WorkRefused(kind WorkKind, contended bool) {
	reason := "over_limit"
	if contended {
		reason = "contended"
	}
	r.add(workRefusals, labels{kind: kind.String(), status: reason}, 1)
}

type metricBatch struct {
	byName map[string]*metricsv1.Metric
	list   []*metricsv1.Metric
	start  uint64
	at     uint64
}

func (b *metricBatch) metric(spec metricSpec) *metricsv1.Metric {
	m := b.byName[spec.name]
	if m != nil {
		return m
	}
	m = &metricsv1.Metric{Name: spec.name, Unit: spec.unit}
	switch spec.form {
	case gauge:
		m.Data = &metricsv1.Metric_Gauge{Gauge: &metricsv1.Gauge{}}
	case counter:
		m.Data = &metricsv1.Metric_Sum{Sum: &metricsv1.Sum{IsMonotonic: true, AggregationTemporality: metricsv1.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE}}
	default:
		m.Data = &metricsv1.Metric_Histogram{Histogram: &metricsv1.Histogram{AggregationTemporality: metricsv1.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE}}
	}
	b.byName[spec.name] = m
	b.list = append(b.list, m)
	return m
}

func (b *metricBatch) number(spec metricSpec, l labels, value int64) {
	m := b.metric(spec)
	p := &metricsv1.NumberDataPoint{TimeUnixNano: b.at, Value: &metricsv1.NumberDataPoint_AsInt{AsInt: value}, Attributes: l.attrs()}
	if spec.form == gauge {
		m.GetGauge().DataPoints = append(m.GetGauge().DataPoints, p)
	} else {
		p.StartTimeUnixNano = b.start
		m.GetSum().DataPoints = append(m.GetSum().DataPoints, p)
	}
}

func (b *metricBatch) real(spec metricSpec, value float64) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return
	}
	m := b.metric(spec)
	m.GetGauge().DataPoints = append(m.GetGauge().DataPoints, &metricsv1.NumberDataPoint{
		TimeUnixNano: b.at, Value: &metricsv1.NumberDataPoint_AsDouble{AsDouble: value},
	})
}

type histogramPoint struct {
	key seriesKey
	histogram
}

func (b *metricBatch) addInstruments(i *instruments) {
	// Copy under the lock. Protobuf allocation and encoding happen outside it.
	i.mu.Lock()
	distributions := make([]histogramPoint, 0, len(i.hist))
	for k, v := range i.hist {
		distributions = append(distributions, histogramPoint{k, *v})
	}
	counts := make(map[seriesKey]int64, len(i.count))
	for k, v := range i.count {
		counts[k] = v
	}
	i.mu.Unlock()
	for _, p := range distributions {
		spec := measurements[p.key.measure]
		h := b.metric(spec).GetHistogram()
		h.DataPoints = append(h.DataPoints, &metricsv1.HistogramDataPoint{
			Attributes: p.key.labels.attrs(), StartTimeUnixNano: b.start, TimeUnixNano: b.at, Count: p.count,
			Sum: &p.sum, ExplicitBounds: bounds(spec.form), BucketCounts: p.buckets[:],
		})
	}
	for k, v := range counts {
		b.number(measurements[k.measure], k.labels, v)
	}
}

func (r *Recorder) metricsBatch(now time.Time, window extrema) *metricBatch {
	b := &metricBatch{byName: map[string]*metricsv1.Metric{}, start: nano(r.started), at: nano(now)}
	b.addInstruments(&r.instruments)
	b.number(measurements[droppedSeries], labels{}, r.instruments.dropped.Load())
	if browser := r.browser; browser != nil {
		b.number(measurements[droppedRecords], labels{kind: "browser_logs"}, browser.recordDrops.Load())
		b.number(measurements[droppedBatches], labels{kind: "browser_logs"}, browser.logDrops.Load())
		b.number(measurements[droppedBatches], labels{kind: "browser_metrics"}, browser.metricDrops.Load())
		b.number(measurements[droppedSeries], labels{kind: "browser"}, browser.instruments.dropped.Load())
	}
	for k, v := range r.inFlight {
		if v.seen.Load() {
			b.number(measurements[httpInFlight], labels{route: k.route, method: k.method}, v.value.Load())
		}
	}
	for s := range signalCount {
		l := labels{kind: signal(s).String()}
		b.number(measurements[droppedBatches], l, r.batchDrops[s].Load())
		b.number(measurements[droppedRecords], l, r.recordDrops[s].Load())
	}
	r.activeMu.Lock()
	stats := r.workKinds
	r.activeMu.Unlock()
	for i, k := range stats {
		if k.peak != 0 || k.waiting != 0 {
			l := labels{kind: WorkKind(i).String()}
			b.number(metricSpec{"flow.metadata.work.kind.used_bytes", "By", gauge}, l, k.used)
			b.number(metricSpec{"flow.metadata.work.kind.peak_bytes", "By", gauge}, l, k.peak)
			b.number(metricSpec{"flow.metadata.work.kind.waiting", "{operation}", gauge}, l, k.waiting)
		}
	}
	r.memory.export(b, window)
	return b
}
