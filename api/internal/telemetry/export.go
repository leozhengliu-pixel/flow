package telemetry

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	logservice "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metricservice "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	traceservice "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	metricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

type Target struct {
	URL, CAFile, CertFile, KeyFile string
	Headers                        map[string]string
	Timeout, Interval              time.Duration
	Gzip, Legacy                   bool
}

type Options struct {
	Enabled                      bool
	Frontend                     bool
	Targets                      [3]Target
	Interval, SlowRequest        time.Duration
	SampleRate                   float64
	SampleEvery                  time.Duration
	Thresholds                   [3]float64
	WorkHighWater                float64
	SnapshotDir, HeapProfileDir  string
	HeapProfileMaxBytes          int64
	Routes, Tools, Assets, Hosts []string
	Revision                     string
	Environment                  string
	DatabaseDriver               string
	LegacyMetricsTemporality     string
	LegacyMetricsHistogram       string
}

type signal uint8

const (
	metrics signal = iota
	traces
	logs
	signalCount
)

func (s signal) String() string { return [...]string{"metrics", "traces", "logs"}[s] }

type target struct {
	url     string
	client  *http.Client
	headers map[string]string
	gzip    bool
	retryAt time.Time
	backoff time.Duration
}

type flight struct {
	value atomic.Int64
	seen  atomic.Bool
}

type requestKeyLabels struct{ route, method string }

type Recorder struct {
	options     Options
	started     time.Time
	targets     [signalCount]target
	resource    *resourcev1.Resource
	routes      map[string]string
	tools       map[string]string
	assets      map[string]string
	hosts       map[string]string
	inFlight    map[requestKeyLabels]*flight
	bodyBytes   atomic.Int64
	instruments instruments
	compat      *sdkmetric.ManualReader
	legacyHTTP  *legacyHTTP
	memory      memorySampler
	snapshots   *snapshotWriter
	source      atomic.Pointer[func(bool) LocalStats]
	activeMu    sync.Mutex
	active      map[*request]struct{}
	workHolders map[workOwner]int64
	workKinds   [WorkKindCount]workStats
	browser     *browserRecorder
	traceQueue  chan traceRecord
	eventQueue  chan event
	urgentTrace chan struct{}
	urgentLogs  chan struct{}
	recordDrops [signalCount]atomic.Int64
	batchDrops  [signalCount]atomic.Int64
	stop        context.CancelFunc
	done        chan struct{}
}

var startedRecorders atomic.Int32

func Enabled() bool { return startedRecorders.Load() > 0 }

func safeSet(values []string) map[string]string {
	set := make(map[string]string, len(values))
	for _, v := range values {
		if clean(v) {
			set[v] = v
		}
	}
	return set
}

func New(o Options) (*Recorder, error) {
	if !o.Enabled {
		return nil, nil
	}
	if o.Interval <= 0 {
		o.Interval = 30 * time.Second
	}
	if o.SlowRequest <= 0 {
		o.SlowRequest = time.Second
	}
	if o.SampleEvery <= 0 {
		o.SampleEvery = 200 * time.Millisecond
	}
	if o.Thresholds == [3]float64{} {
		o.Thresholds = [3]float64{.70, .85, .95}
	}
	if o.WorkHighWater <= 0 {
		o.WorkHighWater = .8
	}
	r := &Recorder{
		options: o, started: time.Now(), routes: safeSet(o.Routes), tools: safeSet(o.Tools),
		assets: safeSet(o.Assets), hosts: safeSet(o.Hosts), inFlight: map[requestKeyLabels]*flight{},
		instruments: newInstruments(maxSeries),
		traceQueue:  make(chan traceRecord, 96), eventQueue: make(chan event, 128),
		urgentTrace: make(chan struct{}, 1), urgentLogs: make(chan struct{}, 1), done: make(chan struct{}),
		active: make(map[*request]struct{}), workHolders: make(map[workOwner]int64),
		resource: &resourcev1.Resource{Attributes: []*common.KeyValue{strAttr("service.name", "flow-api")}},
	}
	// This random identifier denotes a process lifetime, never a person,
	// device or installation. It also keeps cumulative series of replicas apart.
	var instance [8]byte
	_, _ = rand.Read(instance[:])
	r.resource.Attributes = append(r.resource.Attributes, &common.KeyValue{
		Key: "service.instance.id", Value: &common.AnyValue{Value: &common.AnyValue_StringValue{StringValue: hexID(instance[:])}},
	})
	if revisionID.MatchString(o.Revision) {
		r.resource.Attributes = append(r.resource.Attributes, strAttr("service.version", o.Revision))
	}
	if clean(o.Environment) && environmentName.MatchString(o.Environment) {
		r.resource.Attributes = append(r.resource.Attributes,
			strAttr("deployment.environment", o.Environment),
			strAttr("deployment.environment.name", o.Environment))
	}
	r.routes[Unmatched] = Unmatched
	for route := range r.routes {
		for _, method := range [...]string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "OTHER"} {
			r.inFlight[requestKeyLabels{route, method}] = &flight{}
		}
	}
	for i, config := range o.Targets {
		t, err := newTarget(config)
		if err != nil {
			return nil, err
		}
		r.targets[i] = t
	}
	if o.Frontend {
		browser, err := newBrowserRecorder(o.Targets[metrics], o.Targets[logs])
		if err != nil {
			return nil, err
		}
		r.browser = browser
	}
	return r, nil
}

var revisionID = regexp.MustCompile(`^[a-f0-9]{7,40}$`)
var environmentName = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{0,39}$`)

func (r *Recorder) safeRoute(v string) string {
	if route := r.routes[v]; route != "" {
		return route
	}
	return Unmatched
}

func (r *Recorder) safeTool(v string) string {
	if tool := r.tools[v]; tool != "" {
		return tool
	}
	return "other"
}

func (r *Recorder) safeHost(v string) string {
	if host := r.hosts[strings.ToLower(v)]; host != "" {
		return safeHost(host)
	}
	return ""
}

func newTarget(c Target) (target, error) {
	if c.URL == "" {
		return target{}, nil
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	transport := &http.Transport{
		ForceAttemptHTTP2: true, MaxConnsPerHost: 1, MaxIdleConns: 1,
		MaxResponseHeaderBytes: 16 << 10, IdleConnTimeout: 30 * time.Second,
		TLSHandshakeTimeout: min(timeout, 5*time.Second),
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
	}
	if c.Legacy {
		transport.Proxy = http.ProxyFromEnvironment
	}
	if c.CAFile != "" {
		data, err := os.ReadFile(c.CAFile)
		certs := x509.NewCertPool()
		if err != nil || !certs.AppendCertsFromPEM(data) {
			return target{}, errors.New("telemetry: invalid collector CA file")
		}
		transport.TLSClientConfig.RootCAs = certs
	}
	if c.CertFile != "" || c.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
		if err != nil {
			return target{}, errors.New("telemetry: invalid collector client certificate")
		}
		transport.TLSClientConfig.Certificates = []tls.Certificate{cert}
	}
	return target{
		url: c.URL, headers: c.Headers, gzip: c.Gzip,
		client: &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
	}, nil
}

func (r *Recorder) Start() {
	ctx, stop := context.WithCancel(context.Background())
	r.stop = stop
	startedRecorders.Add(1)
	go r.run(ctx)
}

func (r *Recorder) Close(ctx context.Context) {
	if r == nil || r.stop == nil {
		return
	}
	r.stop()
	select {
	case <-r.done:
	case <-ctx.Done():
	}
}

func (r *Recorder) run(ctx context.Context) {
	defer close(r.done)
	defer startedRecorders.Add(-1)
	persist := openSnapshot(r.options.SnapshotDir)
	if persist != nil {
		defer persist.close()
		persist.retainPrevious()
		r.snapshots = persist
	}
	var workers sync.WaitGroup
	workers.Go(func() { r.sampleLoop(ctx, persist) })
	for s, t := range r.targets {
		if t.client != nil {
			workers.Go(func() { r.runExporter(ctx, signal(s)) })
		}
	}
	if r.browser != nil {
		workers.Go(func() { r.runBrowserExporter(ctx) })
	}
	workers.Wait()
}

func (r *Recorder) runExporter(ctx context.Context, s signal) {
	defer r.targets[s].client.CloseIdleConnections()
	var urgent <-chan struct{}
	switch s {
	case traces:
		urgent = r.urgentTrace
	case logs:
		urgent = r.urgentLogs
		r.flushPrevious(ctx, time.Now())
	}
	interval := r.options.Targets[s].Interval
	if interval <= 0 {
		interval = r.options.Interval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			final, cancel := context.WithTimeout(context.Background(), time.Second)
			r.flushSignal(final, s, time.Now())
			cancel()
			return
		case <-urgent:
			now := time.Now()
			if s != traces || !now.Before(r.targets[s].retryAt) {
				r.flushSignal(ctx, s, now)
			}
		case at := <-ticker.C:
			r.flushSignal(ctx, s, at)
		}
	}
}

func (r *Recorder) SetSource(f func(bool) LocalStats) {
	if f == nil {
		r.source.Store(nil)
		return
	}
	r.source.Store(&f)
}

func (r *Recorder) flushSignal(ctx context.Context, s signal, at time.Time) {
	switch s {
	case metrics:
		var window extrema
		sent := r.targets[s].send(ctx, s, at, &r.batchDrops[s], func() proto.Message {
			window = r.memory.takeWindow()
			return r.metricData(ctx, at, window)
		})
		if sent != exportAccepted && window.Last.Valid != 0 {
			r.memory.restoreWindow(window)
		}
	case logs:
		r.flushPrevious(ctx, at)
		flushQueue(ctx, &r.targets[s], s, at, r.eventQueue, &r.batchDrops[s], &r.recordDrops[s], r.logData)
	case traces:
		flushQueue(ctx, &r.targets[s], s, at, r.traceQueue, &r.batchDrops[s], &r.recordDrops[s], r.traceData)
	}
}

func (r *Recorder) flushPrevious(ctx context.Context, at time.Time) {
	if r.snapshots == nil {
		return
	}
	w := r.snapshots
	t := &r.targets[logs]
	for len(w.pending) > 0 && !at.Before(t.retryAt) {
		pending := w.pending[0]
		outcome := t.send(ctx, logs, at, &r.batchDrops[logs], func() proto.Message {
			return r.logData([]event{{kind: previousProcess, diagnostic: &diagnosticEvent{snapshot: pending.data, level: -1}}})
		})
		switch outcome {
		case exportAccepted:
			w.acknowledge(pending)
		case exportRetry:
			return
		case exportRejected:
			// Keep the file for the next startup. OTLP forbids retrying a partial success.
			slog.Warn("telemetry collector rejected a crash snapshot")
		}
		w.pending = w.pending[1:]
	}
}

func (r *Recorder) metricData(ctx context.Context, at time.Time, window extrema) proto.Message {
	scopes := []*metricsv1.ScopeMetrics{{Scope: scope, Metrics: r.metricsBatch(at, window).list}}
	scopes = append(scopes, r.collectCompatibilityMetrics(ctx)...)
	return &metricservice.ExportMetricsServiceRequest{ResourceMetrics: []*metricsv1.ResourceMetrics{{
		Resource: r.resource, ScopeMetrics: scopes,
	}}}
}

func flushQueue[T any](ctx context.Context, t *target, s signal, at time.Time, queue chan T, batchDrops, recordDrops *atomic.Int64, encode func([]T) proto.Message) {
	// Each signal has one consumer. Arrivals during this flush stay queued.
	n := len(queue)
	if n == 0 {
		return
	}
	if at.Before(t.retryAt) {
		for range n {
			<-queue
		}
		batchDrops.Add(1)
		recordDrops.Add(int64(n))
		return
	}
	batch := make([]T, n)
	for i := range batch {
		batch[i] = <-queue
	}
	if t.send(ctx, s, at, batchDrops, func() proto.Message { return encode(batch) }) != exportAccepted {
		recordDrops.Add(int64(n))
	}
}

const maxExportBytes = 4 << 20

type exportOutcome uint8

const (
	exportAccepted exportOutcome = iota
	exportRetry
	exportRejected
)

func (t *target) send(ctx context.Context, s signal, at time.Time, batchDrops *atomic.Int64, build func() proto.Message) exportOutcome {
	if t.url == "" {
		return exportRejected
	}
	if at.Before(t.retryAt) {
		batchDrops.Add(1)
		return exportRetry
	}
	outcome := t.post(ctx, s, build())
	if outcome != exportAccepted {
		batchDrops.Add(1)
		if t.backoff == 0 {
			t.backoff = 2 * time.Second
		} else {
			t.backoff = min(t.backoff*2, 2*time.Minute)
		}
		t.retryAt = time.Now().Add(t.backoff)
	} else {
		t.retryAt, t.backoff = time.Time{}, 0
	}
	return outcome
}

func (t *target) post(ctx context.Context, s signal, message proto.Message) exportOutcome {
	if proto.Size(message) > maxExportBytes {
		return exportRejected
	}
	body, err := proto.Marshal(message)
	if err != nil {
		return exportRejected
	}
	if t.gzip {
		var compressed bytes.Buffer
		writer := gzip.NewWriter(&compressed)
		_, _ = writer.Write(body)
		_ = writer.Close()
		body = compressed.Bytes()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(body))
	if err != nil {
		return exportRejected
	}
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	if t.gzip {
		req.Header.Set("Content-Encoding", "gzip")
	}
	req.Header.Set("User-Agent", "Flow-OTLP")
	resp, err := t.client.Do(req)
	if err != nil {
		return exportRetry
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return exportRetry
	default:
		return exportRejected
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<10+1))
	if err != nil {
		return exportRetry
	}
	if len(raw) > 16<<10 {
		return exportRejected
	}
	var accepted bool
	switch s {
	case metrics:
		reply := &metricservice.ExportMetricsServiceResponse{}
		accepted = proto.Unmarshal(raw, reply) == nil && reply.GetPartialSuccess().GetRejectedDataPoints() == 0
	case traces:
		reply := &traceservice.ExportTraceServiceResponse{}
		accepted = proto.Unmarshal(raw, reply) == nil && reply.GetPartialSuccess().GetRejectedSpans() == 0
	default:
		reply := &logservice.ExportLogsServiceResponse{}
		accepted = proto.Unmarshal(raw, reply) == nil && reply.GetPartialSuccess().GetRejectedLogRecords() == 0
	}
	if accepted {
		return exportAccepted
	}
	return exportRejected
}

func (r *Recorder) queueTrace(t traceRecord) {
	select {
	case r.traceQueue <- t:
		if len(r.traceQueue) >= cap(r.traceQueue)/2 {
			select {
			case r.urgentTrace <- struct{}{}:
			default:
			}
		}
	default:
		r.dropRecords(traces, 1)
	}
}

func (r *Recorder) queueEvent(e event) {
	if r.targets[logs].url == "" {
		return
	}
	select {
	case r.eventQueue <- e:
		if e.kind == highWater {
			select {
			case r.urgentLogs <- struct{}{}:
			default:
			}
		}
	default:
		r.dropRecords(logs, 1)
	}
}

func (r *Recorder) dropRecords(s signal, n int) { r.recordDrops[s].Add(int64(n)) }

type traceRecord struct {
	labels
	start, end        time.Time
	id                [16]byte
	root              [8]byte
	stages            [stageCount]stageSample
	failed, tail, job bool
}

var scope = &common.InstrumentationScope{Name: "flow.telemetry", Version: "1"}

func (r *Recorder) traceData(records []traceRecord) proto.Message {
	var spans []*tracev1.Span
	for _, t := range records {
		root := &tracev1.Span{
			TraceId: t.id[:], SpanId: t.root[:], Name: "HTTP request", Kind: tracev1.Span_SPAN_KIND_SERVER,
			StartTimeUnixNano: nano(t.start), EndTimeUnixNano: nano(t.end), Attributes: t.labels.attrs(), Flags: 1,
		}
		if r.options.Targets[traces].Legacy {
			root.Name = "flow.http"
		}
		if t.job {
			root.Name, root.Kind = "Background job", tracev1.Span_SPAN_KIND_INTERNAL
		}
		if t.tail {
			root.Attributes = append(root.Attributes, strAttr("flow.trace.selection", "tail"))
		}
		if t.failed {
			root.Status = &tracev1.Status{Code: tracev1.Status_STATUS_CODE_ERROR}
		}
		spans = append(spans, root)
		for i, stage := range t.stages {
			if stage.count == 0 {
				continue
			}
			var id [8]byte
			_, _ = rand.Read(id[:])
			child := &tracev1.Span{
				TraceId: t.id[:], SpanId: id[:], ParentSpanId: t.root[:], Name: stageNames[i],
				Kind: tracev1.Span_SPAN_KIND_INTERNAL, StartTimeUnixNano: nano(stage.start),
				EndTimeUnixNano: nano(stage.start.Add(stage.longest)), Flags: 1,
				Attributes: []*common.KeyValue{intAttr("flow.stage.count", stage.count), intAttr("flow.stage.total_us", stage.total.Microseconds())},
			}
			if clean(stage.kind) && stage.kind != "" {
				child.Attributes = append(child.Attributes, strAttr("flow.kind", stage.kind))
			}
			if clean(stage.detail) && stage.detail != "" {
				switch Stage(i) {
				case SQL:
					child.Attributes = append(child.Attributes, strAttr("db.collection.name", stage.detail))
				case External:
					child.Attributes = append(child.Attributes, strAttr("server.address", stage.detail))
				}
			}
			spans = append(spans, child)
		}
	}
	return &traceservice.ExportTraceServiceRequest{ResourceSpans: []*tracev1.ResourceSpans{{
		Resource: r.resource, ScopeSpans: []*tracev1.ScopeSpans{{Scope: scope, Spans: spans}},
	}}}
}

type eventKind uint8

const (
	slowRequest eventKind = iota
	highWater
	previousProcess
	browserError
)

var eventNames = [...]string{"flow.request.slow", "flow.memory.high_water", "flow.memory.previous_process_unclean", "flow.browser.js_error"}

type event struct {
	kind       eventKind
	trace      traceRecord
	diagnostic *diagnosticEvent
	browser    *browserErrorRecord
}

func (r *Recorder) logData(events []event) proto.Message {
	out := make([]*logsv1.LogRecord, 0, len(events))
	for _, e := range events {
		name := eventNames[e.kind]
		record := &logsv1.LogRecord{
			TimeUnixNano: nano(time.Now()), EventName: name, Body: strValue(name),
			SeverityNumber: logsv1.SeverityNumber_SEVERITY_NUMBER_WARN,
		}
		switch e.kind {
		case slowRequest:
			t := e.trace
			record.TimeUnixNano = nano(t.end)
			if t.id != [16]byte{} {
				record.TraceId, record.SpanId = t.id[:], t.root[:]
			}
			record.Attributes = append(t.labels.attrs(), intAttr("flow.duration_ms", t.end.Sub(t.start).Milliseconds()))
			for i, stage := range t.stages {
				if stage.count > 0 {
					record.Attributes = append(record.Attributes, intAttr("flow.stage."+stageNames[i]+"_us", stage.total.Microseconds()))
				}
			}
		case highWater, previousProcess:
			if e.kind == highWater {
				record.TimeUnixNano = nano(e.diagnostic.At)
			}
			record.Attributes = e.diagnostic.attributes(r)
		case browserError:
			record.Attributes = r.browserErrorAttributes(e.browser)
		}
		out = append(out, record)
	}
	return &logservice.ExportLogsServiceRequest{ResourceLogs: []*logsv1.ResourceLogs{{
		Resource: r.resource, ScopeLogs: []*logsv1.ScopeLogs{{Scope: scope, LogRecords: out}},
	}}}
}
