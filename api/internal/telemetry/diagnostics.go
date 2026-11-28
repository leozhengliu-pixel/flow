package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	common "go.opentelemetry.io/proto/otlp/common/v1"
)

const captureCooldown = 5 * time.Minute

type thresholdState struct {
	active [4]bool
	last   [4]time.Time
}

// The fourth threshold is relative to the work budget. Only the highest
// eligible crossing in one sample is captured (including crossed lower tiers).
func (s *thresholdState) update(at time.Time, ratios [2]float64, thresholds [3]float64, work float64) (int, bool) {
	selected := -1
	for i, limit := range [...]float64{thresholds[0], thresholds[1], thresholds[2], work} {
		ratio := ratios[0]
		if i == 3 {
			ratio = ratios[1]
		}
		switch {
		case ratio < limit-min(.05, limit/2):
			s.active[i] = false
		case ratio >= limit && !s.active[i]:
			s.active[i] = true
			if s.last[i].IsZero() || at.Sub(s.last[i]) >= captureCooldown {
				s.last[i] = at
				if selected < 0 || i != 3 {
					selected = i
				}
			}
		}
	}
	return selected, s.active[0] || s.active[3]
}

func pressure(s readings) [2]float64 {
	var limit int64
	if s.has(goLimit) {
		limit = s.Values[goLimit]
	}
	if s.has(cgroupLimit) && (limit <= 0 || s.Values[cgroupLimit] < limit) {
		limit = s.Values[cgroupLimit]
	}
	var ratios [2]float64
	if limit > 0 {
		// The cgroup charge can exceed managed Go bytes because it includes
		// page cache and native memory.
		used := max(s.Values[goManaged], s.Values[cgroupCurrent])
		ratios[0] = float64(used) / float64(limit)
	}
	if s.Values[workLimit] > 0 {
		ratios[1] = float64(s.Values[workUsed]) / float64(s.Values[workLimit])
	}
	return ratios
}

func snapshotCadence(danger bool) time.Duration {
	if danger {
		return time.Second
	}
	return 12 * time.Second
}

type snapshot struct {
	At      time.Time
	Clean   bool
	Metrics extrema
}

type frameCount struct {
	Name  string
	Count int64
}

type diagnosticEvent struct {
	snapshot
	level    int
	holders  []WorkHolder
	requests []requestHolder
	frames   []frameCount
}

func (d *diagnosticEvent) attributes(r *Recorder) []*common.KeyValue {
	attrs := []*common.KeyValue{intAttr("flow.snapshot.time_unix_ms", d.At.UnixMilli())}
	level := [...]string{"warning", "critical", "emergency", "work"}
	if d.level >= 0 && d.level < len(level) {
		attrs = append(attrs, strAttr("flow.memory.threshold", level[d.level]))
	}
	for k, spec := range memorySpecs {
		if d.Metrics.Last.has(field(k)) {
			attrs = append(attrs, intAttr(spec.name, d.Metrics.Last.Values[k]))
		}
		if d.Metrics.Max.has(field(k)) {
			attrs = append(attrs, intAttr(spec.name+"_max", d.Metrics.Max.Values[k]))
		}
	}
	for i, h := range d.holders {
		prefix := fmt.Sprintf("flow.holder.%d.", i+1)
		attrs = append(attrs, strAttr(prefix+"kind", h.Kind.String()), strAttr(prefix+"route", r.safeRoute(h.Route)), intAttr(prefix+"bytes", h.Bytes))
	}
	for i, h := range d.requests {
		prefix := fmt.Sprintf("flow.request.%d.", i+1)
		attrs = append(attrs, strAttr(prefix+"route", r.safeRoute(h.Route)), strAttr(prefix+"method", method(h.Method)),
			intAttr(prefix+"work_bytes", h.WorkBytes), intAttr(prefix+"body_bytes", h.BodyBytes), intAttr(prefix+"elapsed_ms", h.ElapsedMillis))
	}
	for i, frame := range d.frames {
		if frameName(frame.Name) || frame.Name == "other" {
			prefix := fmt.Sprintf("flow.goroutine.%d.", i+1)
			attrs = append(attrs, strAttr(prefix+"frame", frame.Name), intAttr(prefix+"count", frame.Count))
		}
	}
	return attrs
}

func frameName(s string) bool {
	if !clean(s) || s == "" {
		return false
	}
	return strings.HasPrefix(s, "runtime.") || strings.HasPrefix(s, "internal/poll.") ||
		strings.HasPrefix(s, "sync.") || strings.HasPrefix(s, "database/sql.") ||
		strings.HasPrefix(s, "net/http.") || applicationFrame(s)
}

func applicationFrame(s string) bool {
	return strings.HasPrefix(s, "flow/api/") || strings.HasPrefix(s, "main.")
}

func topFrames() []frameCount {
	// A hard buffer bound applies even when the goroutine count spikes.
	if runtime.NumGoroutine() > 2048 {
		return nil
	}
	buf := make([]runtime.StackRecord, 2048)
	n, ok := runtime.GoroutineProfile(buf)
	if !ok {
		return nil
	}
	counts := make(map[string]int64)
	for _, record := range buf[:n] {
		frames := runtime.CallersFrames(record.Stack())
		name := "other"
		for {
			frame, more := frames.Next()
			if frameName(frame.Function) {
				if applicationFrame(frame.Function) {
					name = frame.Function
					break
				}
				if name == "other" && !strings.HasPrefix(frame.Function, "runtime.") {
					name = frame.Function
				}
			}
			if !more {
				break
			}
		}
		counts[name]++
	}
	top := make([]frameCount, 0, 8)
	for name, count := range counts {
		top = keepTop(top, frameCount{name, count}, count, func(x frameCount) int64 { return x.Count })
	}
	return top
}

func keepTop[T any](items []T, next T, value int64, rank func(T) int64) []T {
	i := 0
	for i < len(items) && rank(items[i]) >= value {
		i++
	}
	if i == cap(items) {
		return items
	}
	if len(items) < cap(items) {
		items = append(items, next)
	}
	copy(items[i+1:], items[i:])
	items[i] = next
	return items
}

func (r *Recorder) runSampler(ctx context.Context, runtimeMetrics runtimeSampler, cgroup *cgroupReader, persist *snapshotWriter) {
	m := &r.memory
	m.mu.Lock()
	m.cgroup, m.psiSome, m.psiFull = cgroup, math.NaN(), math.NaN()
	m.mu.Unlock()
	if cgroup != nil {
		defer func() {
			m.mu.Lock()
			m.cgroup = nil
			cgroup.close()
			m.mu.Unlock()
		}()
	}
	var snapshots chan snapshot
	if persist != nil {
		snapshots = make(chan snapshot, 1)
		done := make(chan struct{})
		go func() {
			defer close(done)
			for latest := range snapshots {
				persist.write(latest)
			}
		}()
		defer func() { close(snapshots); <-done }()
	}
	var captures chan *diagnosticEvent
	if r.targets[logs].url != "" || r.options.HeapProfileDir != "" {
		captures = make(chan *diagnosticEvent, 1)
		done := make(chan struct{})
		go func() {
			defer close(done)
			r.captureLoop(captures)
		}()
		defer func() { close(captures); <-done }()
	}
	ticker := time.NewTicker(r.options.SampleEvery)
	defer ticker.Stop()
	var state thresholdState
	var lastSnapshot, lastSlowSample time.Time
	var kernel readings
	var snapshotWindow extrema
	some, full := math.NaN(), math.NaN()
	at := time.Now()
	for {
		s := runtimeMetrics.read()
		fullSample := at.Sub(lastSlowSample) >= time.Second
		if cgroup != nil {
			if fullSample {
				kernel = readings{}
				some, full = cgroup.slowSample(&kernel)
			}
			for k := cgroupLimit; k <= cgroupFullTotal; k++ {
				if kernel.has(k) {
					s.set(k, kernel.Values[k])
				}
			}
			cgroup.fastSample(&s)
		}
		var local LocalStats
		if source := r.source.Load(); source != nil {
			local = (*source)(fullSample)
		}
		if local.Work.Limit > 0 {
			s.set(workUsed, local.Work.Used)
			s.set(workWaiting, local.Work.Waiting)
			s.set(workLimit, local.Work.Limit)
			s.set(workPeak, local.Work.Peak)
		}
		s.set(bodyBytes, r.bodyBytes.Load())
		m.mu.Lock()
		m.window.add(s)
		if fullSample {
			lastSlowSample = at
			m.local = local
		}
		m.psiSome, m.psiFull = some, full
		m.mu.Unlock()
		snapshotWindow.add(s)
		current := snapshot{At: at, Metrics: snapshotWindow}
		capture, danger := state.update(at, pressure(s), r.options.Thresholds, r.options.WorkHighWater)
		due := persist != nil && !persist.blocked.Load() && (capture >= 0 || at.Sub(lastSnapshot) >= snapshotCadence(danger))
		if cgroup != nil && (due || capture >= 0) {
			m.mu.Lock()
			peak, _ := cgroup.peak(false)
			m.mu.Unlock()
			if peak >= 0 {
				current.Metrics.Last.set(cgroupPeak, peak)
				current.Metrics.Max.set(cgroupPeak, peak)
			}
		}
		if capture >= 0 && captures != nil {
			item := &diagnosticEvent{snapshot: current, level: capture, requests: r.topRequests(at), holders: r.topWork()}
			select {
			case captures <- item:
			default:
				r.dropRecords(logs, 1)
			}
		}
		if due {
			offerSnapshot(snapshots, current)
			lastSnapshot = at
			snapshotWindow = extrema{}
			snapshotWindow.add(s)
		}
		select {
		case at = <-ticker.C:
		case <-ctx.Done():
			if persist != nil {
				current.Clean = true
				offerSnapshot(snapshots, current)
			}
			return
		}
	}
}

func offerSnapshot(queue chan snapshot, latest snapshot) {
	// A single producer replaces a stale pending write while disk I/O runs.
	select {
	case queue <- latest:
		return
	default:
	}
	select {
	case <-queue:
	default:
	}
	queue <- latest
}

func (r *Recorder) captureLoop(captures <-chan *diagnosticEvent) {
	profileSlot := 0
	for capture := range captures {
		capture.frames = topFrames()
		r.queueEvent(event{kind: highWater, diagnostic: capture})
		dir := r.options.HeapProfileDir
		if dir == "" {
			continue
		}
		maxSize := r.options.HeapProfileMaxBytes
		if maxSize == 0 {
			maxSize = 8 << 20
		}
		writeProfile(dir, "heap", profileSlot, maxSize)
		profileSlot = (profileSlot + 1) % 3
		if capture.level == 2 && runtime.NumGoroutine() <= 4096 {
			writeProfile(dir, "goroutine", 0, maxSize)
		}
	}
}

type snapshotWriter struct {
	dir     string
	lock    *os.File
	pending []pendingSnapshot // owned by the logs exporter after startup
	blocked atomic.Bool       // the rolling file itself awaits acknowledgment
}

func openSnapshot(dir string) *snapshotWriter {
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err == nil {
		file, err := os.OpenFile(filepath.Join(dir, "flow.lock"), os.O_CREATE|os.O_RDWR, 0o600)
		if err == nil {
			if lockSnapshot(file) {
				return &snapshotWriter{dir: dir, lock: file}
			}
			file.Close()
		}
	}
	slog.Warn("telemetry snapshot directory unavailable")
	return nil
}

func (w *snapshotWriter) close() { _ = w.lock.Close() }

func (w *snapshotWriter) previous() (snapshot, bool) {
	return readSnapshot(filepath.Join(w.dir, "memory.json"))
}

func readSnapshot(path string) (snapshot, bool) {
	f, err := os.Open(path)
	if err != nil {
		return snapshot{}, false
	}
	defer f.Close()
	var s snapshot
	dec := json.NewDecoder(io.LimitReader(f, 4096))
	if dec.Decode(&s) != nil || s.At.IsZero() {
		return snapshot{}, false
	}
	return s, true
}

type pendingSnapshot struct {
	path    string
	data    snapshot
	rolling bool
}

const pendingSnapshotLimit = 4

func (w *snapshotWriter) retainPrevious() {
	var free string
	for i := range pendingSnapshotLimit {
		path := filepath.Join(w.dir, fmt.Sprintf("memory.previous-%d.json", i))
		if data, ok := readSnapshot(path); ok && !data.Clean {
			w.pending = append(w.pending, pendingSnapshot{path: path, data: data})
		} else if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) && free == "" {
			free = path
		}
	}
	rolling := filepath.Join(w.dir, "memory.json")
	if data, ok := readSnapshot(rolling); ok && !data.Clean {
		pending := pendingSnapshot{path: rolling, data: data, rolling: true}
		if free != "" && os.Rename(rolling, free) == nil {
			pending.path, pending.rolling = free, false
		}
		w.pending = append(w.pending, pending)
		w.blocked.Store(pending.rolling)
	}
	slices.SortFunc(w.pending, func(a, b pendingSnapshot) int { return a.data.At.Compare(b.data.At) })
}

func (w *snapshotWriter) acknowledge(p pendingSnapshot) {
	if err := os.Remove(p.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("telemetry could not remove an acknowledged crash snapshot")
		return
	}
	if p.rolling {
		w.blocked.Store(false)
	}
}

func (w *snapshotWriter) write(s snapshot) {
	if w.blocked.Load() {
		return
	}
	replaceFile(w.dir, "memory.json", func(dst io.Writer) error {
		return json.NewEncoder(dst).Encode(s)
	})
}

type cappedWriter struct {
	io.Writer
	left int64
	err  error
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.left {
		w.err = errors.New("profile exceeds size cap")
		return 0, w.err
	}
	n, err := w.Writer.Write(p)
	if err != nil {
		w.err = err
	}
	w.left -= int64(n)
	return n, err
}

func writeProfile(dir, kind string, slot int, size int64) {
	if os.MkdirAll(dir, 0o700) != nil {
		return
	}
	replaceFile(dir, fmt.Sprintf("%s-%d.pprof", kind, slot), func(w io.Writer) error {
		capped := &cappedWriter{Writer: w, left: size}
		err := pprof.Lookup(kind).WriteTo(capped, 0)
		return errors.Join(err, capped.err)
	})
}

func replaceFile(dir, name string, write func(io.Writer) error) {
	// A fixed temporary name also bounds disk usage across process deaths.
	tmp := filepath.Join(dir, "."+name+".tmp")
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return
	}
	file, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer os.Remove(tmp)
	err = write(file)
	if err == nil && name == "memory.json" {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil && closeErr == nil {
		_ = os.Rename(tmp, filepath.Join(dir, name))
	}
}
