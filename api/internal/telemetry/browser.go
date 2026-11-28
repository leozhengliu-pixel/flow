package telemetry

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	metricservice "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	metricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"
)

const (
	BrowserBatchBytes   = 16 << 10
	BrowserBatchRecords = 32
)

type BrowserRecord struct {
	Kind   string   `json:"kind"`
	Route  string   `json:"route"`
	Method string   `json:"method"`
	Status string   `json:"status"`
	Value  float64  `json:"value"`
	Hash   string   `json:"hash"`
	Frames []string `json:"frames"`
}

type browserErrorRecord struct {
	route, hash string
	frames      []string
}

const maxBrowserSeries = 1024

type browserRecorder struct {
	instruments instruments
	events      chan event
	key         [32]byte
	metrics     target
	logs        target
	recordDrops atomic.Int64
	metricDrops atomic.Int64
	logDrops    atomic.Int64
}

func newBrowserRecorder(metricConfig, logConfig Target) (*browserRecorder, error) {
	m, err := newTarget(metricConfig)
	if err != nil {
		return nil, err
	}
	l, err := newTarget(logConfig)
	if err != nil {
		return nil, err
	}
	b := &browserRecorder{instruments: newInstruments(maxBrowserSeries), events: make(chan event, 32), metrics: m, logs: l}
	_, _ = rand.Read(b.key[:])
	return b, nil
}

func (r *Recorder) runBrowserExporter(ctx context.Context) {
	b := r.browser
	for _, client := range []*target{&b.metrics, &b.logs} {
		if client.client != nil {
			defer client.client.CloseIdleConnections()
		}
	}
	ticker := time.NewTicker(r.options.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			final, cancel := context.WithTimeout(context.Background(), time.Second)
			r.flushBrowser(final, time.Now())
			cancel()
			return
		case at := <-ticker.C:
			r.flushBrowser(ctx, at)
		}
	}
}

func (r *Recorder) flushBrowser(ctx context.Context, at time.Time) {
	b := r.browser
	if b.metrics.client != nil {
		b.instruments.mu.Lock()
		hasSeries := len(b.instruments.hist)+len(b.instruments.count) > 0
		b.instruments.mu.Unlock()
		if hasSeries {
			b.metrics.send(ctx, metrics, at, &b.metricDrops, func() proto.Message {
				batch := &metricBatch{byName: map[string]*metricsv1.Metric{}, start: nano(r.started), at: nano(at)}
				batch.addInstruments(&b.instruments)
				return &metricservice.ExportMetricsServiceRequest{ResourceMetrics: []*metricsv1.ResourceMetrics{{
					Resource: r.resource, ScopeMetrics: []*metricsv1.ScopeMetrics{{Scope: scope, Metrics: batch.list}},
				}}}
			})
		}
	}
	flushQueue(ctx, &b.logs, logs, at, b.events, &b.logDrops, &b.recordDrops, r.logData)
}

var browserPages = safeSet([]string{
	"/", "/login", "/settings", "/{workspace}", "/{workspace}/issue/{id}",
	"/{workspace}/my-issues", "/{workspace}/team/{team}/issues", "/{workspace}/issues",
	"/{workspace}/document/{id}", "/{workspace}/project/{id}", "/{workspace}/inbox",
	"/{workspace}/projects", "/{workspace}/documents", "/{workspace}/initiatives", "/{workspace}/initiative/{id}", "/{workspace}/pulse",
	"/{workspace}/search", "/{workspace}/settings", "/{workspace}/views", "/{workspace}/agent",
})

func browserPage(route string) string {
	if page := browserPages[route]; page != "" {
		return page
	}
	return Unmatched
}

var browserMeasures = map[string]struct {
	id  measure
	max float64
}{
	"lcp":        {browserLCP, 3600},
	"inp":        {browserINP, 3600},
	"cls":        {browserCLS, 100},
	"ttfb":       {browserTTFB, 3600},
	"navigation": {browserNavigation, 3600},
	"long_task":  {browserTask, 3600},
	"memory":     {browserMemory, 1 << 40},
}

// RecordBrowser rebuilds the record from the schema. In particular, no
// arbitrary attribute map, URL, or error string can reach the OTLP encoder.
func (r *Recorder) RecordBrowser(input BrowserRecord) bool {
	if r == nil || r.browser == nil {
		return false
	}
	b := r.browser
	observe := func(m measure, l labels, v float64) { b.instruments.observeValue(m, l, v) }
	add := func(m measure, l labels, v int64) { b.instruments.addValue(m, l, v) }
	page := browserPage(input.Route)
	if m, ok := browserMeasures[input.Kind]; ok {
		if math.IsNaN(input.Value) || input.Value < 0 || input.Value > m.max {
			return false
		}
		observe(m.id, labels{route: page}, input.Value)
		return true
	}
	switch input.Kind {
	case "api":
		switch input.Status {
		case "2xx", "3xx", "4xx", "5xx", "network":
			add(browserAPI, labels{route: r.safeRoute(input.Route), method: method(input.Method), status: input.Status}, 1)
			return true
		}
	case "reconnect":
		add(browserReconnect, labels{kind: enum(input.Status, []string{"events", "sync", "collaboration"})}, 1)
		return true
	case "storage":
		add(browserStorage, labels{kind: enum(input.Status, []string{"open", "read", "write"})}, 1)
		return true
	case "dropped":
		if input.Value > 0 && input.Value <= 10000 && input.Value == math.Trunc(input.Value) {
			add(browserDropped, labels{}, int64(input.Value))
			return true
		}
	case "js_error":
		if b.logs.url == "" || len(input.Hash) != 64 {
			return false
		}
		digest, err := hex.DecodeString(input.Hash)
		if err != nil {
			return false
		}
		// Rekey client digests so the collector cannot receive arbitrary
		// client-supplied hex IDs or compare digests across process lifetimes.
		mac := hmac.New(sha256.New, b.key[:])
		mac.Write(digest)
		record := &browserErrorRecord{route: page, hash: hexID(mac.Sum(nil))}
		for _, frame := range input.Frames[:min(5, len(input.Frames))] {
			if normal := r.browserFrame(frame); normal != "" {
				record.frames = append(record.frames, normal)
			}
		}
		select {
		case b.events <- event{kind: browserError, browser: record}:
			return true
		default:
			b.recordDrops.Add(1)
		}
	}
	return false
}

func (r *Recorder) browserFrame(frame string) string {
	name, number, ok := strings.Cut(frame, ":")
	if !ok || len(number) > 7 || r.assets[name] == "" || !clean(name) {
		return ""
	}
	line, err := strconv.Atoi(number)
	if err != nil || line < 1 || line > 1000000 || strconv.Itoa(line) != number {
		return ""
	}
	return name + ":" + strconv.Itoa(line)
}

func (r *Recorder) browserErrorAttributes(e *browserErrorRecord) []*common.KeyValue {
	attrs := []*common.KeyValue{
		strAttr("http.route", browserPage(e.route)),
		strAttr("flow.error.hash", e.hash),
	}
	if len(e.frames) > 0 {
		values := make([]*common.AnyValue, 0, len(e.frames))
		for _, frame := range e.frames {
			if v := r.browserFrame(frame); v != "" {
				values = append(values, strValue(v))
			}
		}
		attrs = append(attrs, &common.KeyValue{
			Key: "flow.error.frames",
			Value: &common.AnyValue{Value: &common.AnyValue_ArrayValue{
				ArrayValue: &common.ArrayValue{Values: values},
			}},
		})
	}
	return attrs
}
