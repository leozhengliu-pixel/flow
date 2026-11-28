package telemetry

import (
	"context"
	"math"
	runtimemetrics "runtime/metrics"
	"sync"

	metricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
)

type Budget struct{ Used, Limit, Peak, Waiting int64 }

type Pool struct {
	Open, InUse, Idle, MaxOpen, WaitCount, WaitNanos int64
}

type LocalStats struct {
	Work, Policy, Rows, Images, Sources, Spool, Realtime Budget
	RequestBodyLimit                                     int64
	DB                                                   [3]Pool // main, snapshots, standalone reads
}

type field uint8

const (
	heapLive field = iota
	heapInUse
	heapReleased
	goTotal
	goManaged
	goLimit
	goHeadroom
	goroutines
	stackBytes
	gcCycles
	rss
	cgroupCurrent
	cgroupPeak
	cgroupLimit
	cgroupHigh
	cgroupEventsHigh
	cgroupEventsMax
	cgroupOOM
	cgroupOOMKill
	cgroupSomeTotal
	cgroupFullTotal
	workUsed
	workWaiting
	workLimit
	workPeak
	bodyBytes
	fieldCount
)

var memorySpecs = [fieldCount]metricSpec{
	heapLive:         {"flow.process.heap.live_bytes", "By", gauge},
	heapInUse:        {"flow.process.heap.inuse_bytes", "By", gauge},
	heapReleased:     {"flow.process.heap.released_bytes", "By", gauge},
	goTotal:          {"flow.process.go.total_bytes", "By", gauge},
	goManaged:        {"flow.process.go.managed_bytes", "By", gauge},
	goLimit:          {"flow.process.go.limit_bytes", "By", gauge},
	goHeadroom:       {"flow.process.go.headroom_bytes", "By", gauge},
	goroutines:       {"flow.process.goroutines", "{goroutine}", gauge},
	stackBytes:       {"flow.process.stack_bytes", "By", gauge},
	gcCycles:         {"flow.process.gc.cycles", "{cycle}", counter},
	rss:              {"flow.process.rss_bytes", "By", gauge},
	cgroupCurrent:    {"flow.cgroup.memory.current_bytes", "By", gauge},
	cgroupPeak:       {"flow.cgroup.memory.peak_bytes", "By", gauge},
	cgroupLimit:      {"flow.cgroup.memory.max_bytes", "By", gauge},
	cgroupHigh:       {"flow.cgroup.memory.high_bytes", "By", gauge},
	cgroupEventsHigh: {"flow.cgroup.memory.events.high", "{event}", counter},
	cgroupEventsMax:  {"flow.cgroup.memory.events.max", "{event}", counter},
	cgroupOOM:        {"flow.cgroup.memory.events.oom", "{event}", counter},
	cgroupOOMKill:    {"flow.cgroup.memory.events.oom_kill", "{event}", counter},
	cgroupSomeTotal:  {"flow.cgroup.memory.psi.some_us", "us", counter},
	cgroupFullTotal:  {"flow.cgroup.memory.psi.full_us", "us", counter},
	workUsed:         {"flow.metadata.work.used_bytes", "By", gauge},
	workWaiting:      {"flow.metadata.work.waiting", "{operation}", gauge},
	workLimit:        {"flow.metadata.work.limit_bytes", "By", gauge},
	workPeak:         {"flow.metadata.work.peak_bytes", "By", gauge},
	bodyBytes:        {"flow.http.server.body_in_flight_bytes", "By", gauge},
}

var peakFields = [...]field{heapLive, heapInUse, goTotal, goManaged, goHeadroom, goroutines, rss, cgroupCurrent, cgroupPeak, workUsed, workWaiting, bodyBytes}

type readings struct {
	Values [fieldCount]int64
	Valid  uint64
}

func (s *readings) set(k field, v int64) {
	s.Values[k] = v
	s.Valid |= 1 << k
}

func (s readings) has(k field) bool { return s.Valid&(1<<k) != 0 }

type extrema struct {
	Last, Min, Max readings
}

func (e *extrema) add(s readings) {
	e.Last = s
	for _, k := range peakFields {
		if !s.has(k) {
			continue
		}
		v := s.Values[k]
		if !e.Min.has(k) || v < e.Min.Values[k] {
			e.Min.set(k, v)
		}
		if !e.Max.has(k) || v > e.Max.Values[k] {
			e.Max.set(k, v)
		}
	}
}

type runtimeSampler struct {
	samples [10]runtimemetrics.Sample
}

func newRuntimeSampler() runtimeSampler {
	var sampler runtimeSampler
	for i, name := range [...]string{
		"/gc/heap/live:bytes", "/memory/classes/heap/objects:bytes", "/memory/classes/heap/unused:bytes",
		"/memory/classes/heap/released:bytes", "/memory/classes/total:bytes", "/gc/gomemlimit:bytes",
		"/sched/goroutines:goroutines", "/memory/classes/heap/stacks:bytes", "/memory/classes/os-stacks:bytes",
		"/gc/cycles/total:gc-cycles",
	} {
		sampler.samples[i].Name = name
	}
	return sampler
}

func (s *runtimeSampler) read() readings {
	runtimemetrics.Read(s.samples[:])
	u := func(i int) int64 { return int64(s.samples[i].Value.Uint64()) }
	var sample readings
	sample.set(heapLive, u(0))
	sample.set(heapInUse, u(1)+u(2))
	sample.set(heapReleased, u(3))
	sample.set(goTotal, u(4))
	sample.set(goManaged, max(u(4)-u(3), 0))
	if limit := u(5); limit > 0 && limit < math.MaxInt64 {
		sample.set(goLimit, limit)
		sample.set(goHeadroom, limit-sample.Values[goManaged])
	}
	sample.set(goroutines, u(6))
	sample.set(stackBytes, u(7)+u(8))
	sample.set(gcCycles, u(9))
	return sample
}

type memorySampler struct {
	mu               sync.Mutex
	window           extrema
	local            LocalStats
	psiSome, psiFull float64
	cgroup           *cgroupReader
	peakReset        int
}

func (m *memorySampler) takeWindow() extrema {
	m.mu.Lock()
	defer m.mu.Unlock()
	window := m.window
	m.window = extrema{}
	m.window.add(window.Last)
	if m.cgroup != nil {
		if peak, reset := m.cgroup.peak(true); peak >= 0 {
			window.Last.set(cgroupPeak, peak)
			window.Max.set(cgroupPeak, max(peak, window.Max.Values[cgroupPeak]))
			m.peakReset = reset
		}
	}
	return window
}

func (m *memorySampler) restoreWindow(previous extrema) {
	m.mu.Lock()
	defer m.mu.Unlock()
	last := m.window.Last
	m.window.add(previous.Min)
	m.window.add(previous.Max)
	m.window.Last = last
}

func (m *memorySampler) export(b *metricBatch, window extrema) {
	m.mu.Lock()
	local, some, full, reset := m.local, m.psiSome, m.psiFull, m.peakReset
	m.mu.Unlock()
	if window.Last.has(cgroupPeak) {
		b.number(metricSpec{"flow.cgroup.memory.peak_reset", "1", gauge}, labels{}, int64(reset))
	}
	for k, spec := range memorySpecs {
		if window.Last.has(field(k)) {
			b.number(spec, labels{}, window.Last.Values[k])
		}
	}
	for _, k := range peakFields {
		if window.Max.has(k) {
			spec := memorySpecs[k]
			spec.name += "_max"
			b.number(spec, labels{}, window.Max.Values[k])
			if window.Min.has(k) {
				spec.name = memorySpecs[k].name + "_min"
				b.number(spec, labels{}, window.Min.Values[k])
			}
		}
	}
	b.real(metricSpec{"flow.cgroup.memory.psi.some_avg10", "%", gauge}, some)
	b.real(metricSpec{"flow.cgroup.memory.psi.full_avg10", "%", gauge}, full)
	b.localStats(local)
	var samples = [1]runtimemetrics.Sample{{Name: "/sched/pauses/total/gc:seconds"}}
	runtimemetrics.Read(samples[:])
	if samples[0].Value.Kind() == runtimemetrics.KindFloat64Histogram {
		b.gcPause(samples[0].Value.Float64Histogram())
	}
}

func (b *metricBatch) localStats(s LocalStats) {
	for _, pair := range []struct {
		name   string
		budget Budget
	}{
		{"policy", s.Policy}, {"rows", s.Rows}, {"images", s.Images},
		{"source_descriptors", s.Sources}, {"source_disk", s.Spool}, {"realtime", s.Realtime},
	} {
		if pair.budget.Limit <= 0 {
			continue
		}
		l := labels{kind: pair.name}
		b.number(metricSpec{"flow.memory.budget.used_bytes", "By", gauge}, l, pair.budget.Used)
		b.number(metricSpec{"flow.memory.budget.limit_bytes", "By", gauge}, l, pair.budget.Limit)
		if pair.budget.Peak > 0 {
			b.number(metricSpec{"flow.memory.budget.peak_bytes", "By", gauge}, l, pair.budget.Peak)
		}
	}
	if s.RequestBodyLimit > 0 {
		b.number(metricSpec{"flow.document.request.limit_bytes", "By", gauge}, labels{}, s.RequestBodyLimit)
	}
	for i, name := range [...]string{"main", "snapshots", "reads"} {
		p := s.DB[i]
		if p == (Pool{}) {
			continue
		}
		l := labels{kind: name}
		for _, v := range []struct {
			name  string
			value int64
		}{{"open", p.Open}, {"in_use", p.InUse}, {"idle", p.Idle}, {"max_open", p.MaxOpen}} {
			b.number(metricSpec{"flow.db.pool." + v.name, "{connection}", gauge}, l, v.value)
		}
		b.number(metricSpec{"flow.db.pool.waits", "{wait}", counter}, l, p.WaitCount)
		b.number(metricSpec{"flow.db.pool.wait_ns", "ns", counter}, l, p.WaitNanos)
	}
}

func (b *metricBatch) gcPause(h *runtimemetrics.Float64Histogram) {
	var bounds []float64
	var counts []uint64
	var count uint64
	for i, n := range h.Counts {
		count += n
		right := h.Buckets[i+1]
		if math.IsInf(right, 0) {
			counts = append(counts, n)
		} else {
			bounds = append(bounds, right)
			counts = append(counts, n)
		}
	}
	if len(counts) == len(bounds) {
		counts = append(counts, 0)
	}
	m := b.metric(metricSpec{"flow.process.gc.pause", "s", timeHistogram})
	m.GetHistogram().DataPoints = append(m.GetHistogram().DataPoints, &metricsv1.HistogramDataPoint{
		StartTimeUnixNano: b.start, TimeUnixNano: b.at, Count: count, ExplicitBounds: bounds, BucketCounts: counts,
	})
}

func (r *Recorder) sampleLoop(ctx context.Context, persist *snapshotWriter) {
	r.runSampler(ctx, newRuntimeSampler(), platformCgroup(), persist)
}
