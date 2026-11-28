package telemetry

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	logservice "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	"google.golang.org/protobuf/proto"
)

func TestTelemetryIntervalPreservesSpike(t *testing.T) {
	r, err := New(Options{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int64{20, 90, 30} {
		var sample readings
		sample.set(heapLive, n)
		sample.set(workUsed, n*10)
		r.memory.window.add(sample)
	}
	w := r.memory.takeWindow()
	if w.Last.Values[heapLive] != 30 || w.Min.Values[heapLive] != 20 || w.Max.Values[heapLive] != 90 {
		t.Fatal("sampling window lost its extrema")
	}
	found := false
	for _, metric := range r.metricsBatch(time.Now(), w).list {
		if metric.Name == "flow.metadata.work.used_bytes_max" {
			found = metric.GetGauge().DataPoints[0].GetAsInt() == 900
		}
	}
	if !found {
		t.Fatal("the interval spike was not emitted as a gauge")
	}
	if got := r.memory.window.Max.Values[workUsed]; got != 300 {
		t.Fatalf("new window max %d", got)
	}
	r.memory.restoreWindow(w)
	if r.memory.window.Max.Values[workUsed] != 900 || r.memory.window.Last.Values[workUsed] != 300 {
		t.Fatal("a failed export lost the previous window or the latest sample")
	}
}

func TestTelemetryThresholdsAndSnapshotCadence(t *testing.T) {
	var reading readings
	reading.set(goLimit, 800)
	reading.set(cgroupLimit, 1000)
	reading.set(goManaged, 200)
	reading.set(cgroupCurrent, 760)
	reading.set(workLimit, 100)
	reading.set(workUsed, 80)
	if got := pressure(reading); got != [2]float64{.95, .8} {
		t.Fatalf("pressure ratios: %v", got)
	}
	var thresholds thresholdState
	at := time.Now()
	advance := func(ratio, work float64, after time.Duration, want int, danger bool) {
		t.Helper()
		level, urgent := thresholds.update(at.Add(after), [2]float64{ratio, work}, [3]float64{.7, .85, .95}, .8)
		if level != want || urgent != danger {
			t.Fatalf("at %s got level %d danger=%v, want %d %v", after, level, urgent, want, danger)
		}
	}
	advance(.69, 0, 0, -1, false)
	advance(.72, 0, time.Second, 0, true)
	advance(.69, 0, 2*time.Second, -1, true)
	advance(.64, 0, 3*time.Second, -1, false)
	advance(.73, 0, 4*time.Second, -1, true)
	advance(.99, 0, 5*time.Second, 2, true)
	advance(.61, .81, 6*time.Second, 3, true)
	advance(.59, .70, 7*time.Second, -1, false)
	advance(.74, 0, 6*time.Minute, 0, true)
	if snapshotCadence(false) != 12*time.Second || snapshotCadence(true) != time.Second {
		t.Fatal("snapshot cadence did not switch with pressure")
	}
	queue := make(chan snapshot, 1)
	offerSnapshot(queue, snapshot{At: at})
	offerSnapshot(queue, snapshot{At: at.Add(time.Second)})
	if latest := <-queue; !latest.At.Equal(at.Add(time.Second)) {
		t.Fatal("a slow disk write held up the latest sample")
	}
}

func TestTelemetryPreviousProcessIsExportedOnStartup(t *testing.T) {
	if !snapshotLockSupported {
		t.Skip("snapshot locking unavailable")
	}
	dir := t.TempDir()
	writer := openSnapshot(dir)
	if writer == nil {
		t.Fatal("could not open snapshot")
	}
	var data readings
	data.set(workUsed, 203)
	writer.write(snapshot{At: time.Now(), Metrics: extrema{Last: data}})
	writer.close()

	received := make(chan *logservice.ExportLogsServiceRequest, 1)
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		raw, err := io.ReadAll(req.Body)
		record := &logservice.ExportLogsServiceRequest{}
		if err != nil || proto.Unmarshal(raw, record) != nil {
			w.WriteHeader(400)
			return
		}
		select {
		case received <- record:
		default:
		}
	}))
	defer endpoint.Close()
	recorder, err := New(Options{Enabled: true, Interval: time.Hour, SnapshotDir: dir, Targets: [3]Target{logs: {URL: endpoint.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	recorder.Start()
	defer recorder.Close(context.Background())
	select {
	case record := <-received:
		events := record.ResourceLogs[0].ScopeLogs[0].LogRecords
		if len(events) != 1 || events[0].EventName != "flow.memory.previous_process_unclean" {
			t.Fatal("expected the previous process event")
		}
		found := false
		for _, attr := range events[0].Attributes {
			found = found || attr.Key == "flow.metadata.work.used_bytes" && attr.Value.GetIntValue() == 203
		}
		if !found {
			t.Fatal("previous process event omitted the saved work usage")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("previous process event was not sent immediately")
	}
	recorder.Close(context.Background())
	writer = openSnapshot(dir)
	if writer == nil {
		t.Fatal("recorder did not release the snapshot lock")
	}
	defer writer.close()
	if previous, ok := writer.previous(); !ok || !previous.Clean {
		t.Fatal("clean shutdown marker was not persisted")
	}
}

func TestTelemetryPreviousSnapshotSurvivesFailedAckAndRestart(t *testing.T) {
	if !snapshotLockSupported {
		t.Skip("snapshot locking unavailable")
	}
	dir := t.TempDir()
	writer := openSnapshot(dir)
	if writer == nil {
		t.Fatal("cannot open snapshot directory")
	}
	at := time.Now().Add(-time.Minute)
	writer.write(snapshot{At: at})
	writer.close()

	var healthy atomic.Bool
	received := make(chan time.Time, 8)
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		available := healthy.Load()
		raw, _ := io.ReadAll(req.Body)
		batch := &logservice.ExportLogsServiceRequest{}
		if proto.Unmarshal(raw, batch) != nil {
			t.Error("invalid OTLP logs")
			w.WriteHeader(400)
			return
		}
		for _, scope := range batch.ResourceLogs[0].ScopeLogs {
			for _, record := range scope.LogRecords {
				if record.EventName == "flow.memory.previous_process_unclean" {
					for _, attr := range record.Attributes {
						if attr.Key == "flow.snapshot.time_unix_ms" {
							select {
							case received <- time.UnixMilli(attr.Value.GetIntValue()):
							default:
							}
						}
					}
				}
			}
		}
		if !available {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer collector.Close()
	start := func() *Recorder {
		t.Helper()
		r, err := New(Options{Enabled: true, Interval: 25 * time.Millisecond, SnapshotDir: dir,
			Targets: [3]Target{logs: {URL: collector.URL}}})
		if err != nil {
			t.Fatal(err)
		}
		r.Start()
		t.Cleanup(func() { r.Close(context.Background()) })
		return r
	}
	await := func() {
		t.Helper()
		select {
		case got := <-received:
			if got.UnixMilli() != at.UnixMilli() {
				t.Fatal("retry exported a different snapshot")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no previous snapshot export")
		}
	}
	path := filepath.Join(dir, "memory.previous-0.json")
	first := start()
	await()
	first.Close(context.Background())
	if saved, ok := readSnapshot(path); !ok || !saved.At.Equal(at) {
		t.Fatal("collector downtime replaced the previous snapshot")
	}
	second := start()
	await()
	healthy.Store(true)
	await()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("acknowledged snapshot was not removed")
		}
		time.Sleep(5 * time.Millisecond)
	}
	second.Close(context.Background())
}

func TestTelemetryPreviousSnapshotFullArchiveAndPartialAck(t *testing.T) {
	if !snapshotLockSupported {
		t.Skip("snapshot locking unavailable")
	}
	dir := t.TempDir()
	writer := openSnapshot(dir)
	if writer == nil {
		t.Fatal("cannot open snapshot directory")
	}
	t.Cleanup(writer.close)
	now := time.Now()
	for i := range pendingSnapshotLimit {
		writer.write(snapshot{At: now.Add(time.Duration(i-pendingSnapshotLimit) * time.Minute)})
		if err := os.Rename(filepath.Join(dir, "memory.json"), filepath.Join(dir, fmt.Sprintf("memory.previous-%d.json", i))); err != nil {
			t.Fatal(err)
		}
	}
	writer.write(snapshot{At: now})
	writer.retainPrevious()
	if !writer.blocked.Load() || len(writer.pending) != pendingSnapshotLimit+1 {
		t.Fatal("the full archive did not preserve the rolling crash snapshot")
	}
	writer.write(snapshot{At: now.Add(time.Second), Clean: true})
	if saved, ok := writer.previous(); !ok || !saved.At.Equal(now) {
		t.Fatal("the current writer overwrote the rolling crash snapshot")
	}

	var rejectRolling atomic.Bool
	rejectRolling.Store(true)
	received := make(chan struct{}, pendingSnapshotLimit+2)
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		raw, _ := io.ReadAll(req.Body)
		batch := &logservice.ExportLogsServiceRequest{}
		if proto.Unmarshal(raw, batch) != nil {
			t.Error("invalid OTLP logs")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		for _, resource := range batch.ResourceLogs {
			for _, scope := range resource.ScopeLogs {
				for _, record := range scope.LogRecords {
					for _, attr := range record.Attributes {
						if attr.Key == "flow.snapshot.time_unix_ms" && attr.Value.GetIntValue() == now.UnixMilli() && rejectRolling.Load() {
							reply := &logservice.ExportLogsServiceResponse{PartialSuccess: &logservice.ExportLogsPartialSuccess{RejectedLogRecords: 1}}
							encoded, _ := proto.Marshal(reply)
							_, _ = w.Write(encoded)
						}
					}
				}
			}
		}
		select {
		case received <- struct{}{}:
		default:
			t.Error("unexpected duplicate snapshot")
		}
	}))
	t.Cleanup(collector.Close)
	r, err := New(Options{Enabled: true, Targets: [3]Target{logs: {URL: collector.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	r.snapshots = writer
	r.flushPrevious(t.Context(), now)
	r.flushPrevious(t.Context(), now.Add(time.Minute))
	if len(received) != pendingSnapshotLimit+1 {
		t.Fatal("partial acknowledgment retried the rejected record")
	}
	if saved, ok := writer.previous(); !ok || !saved.At.Equal(now) {
		t.Fatal("partial acknowledgment removed the rolling crash snapshot")
	}
	// A subsequent startup reloads the unacknowledged file from disk.
	writer = &snapshotWriter{dir: dir, lock: writer.lock}
	writer.retainPrevious()
	r.snapshots = writer
	rejectRolling.Store(false)
	r.flushPrevious(t.Context(), now.Add(time.Minute))
	if len(received) != pendingSnapshotLimit+2 || len(writer.pending) != 0 || writer.blocked.Load() {
		t.Fatal("the collector's acknowledgment did not clear the pending snapshot")
	}
	writer.write(snapshot{At: now.Add(time.Second)})
	if saved, ok := writer.previous(); !ok || !saved.At.Equal(now.Add(time.Second)) {
		t.Fatal("the rolling file did not resume after the collector acknowledged the snapshot")
	}
}

func TestTelemetryThresholdExportsHeldRequest(t *testing.T) {
	events := make(chan *logsv1.LogRecord, 4)
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		raw, err := io.ReadAll(req.Body)
		batch := &logservice.ExportLogsServiceRequest{}
		if err != nil || proto.Unmarshal(raw, batch) != nil {
			w.WriteHeader(400)
			return
		}
		for _, resource := range batch.ResourceLogs {
			for _, scope := range resource.ScopeLogs {
				for _, event := range scope.LogRecords {
					select {
					case events <- event:
					default:
					}
				}
			}
		}
	}))
	defer endpoint.Close()
	recorder, err := New(Options{Enabled: true, Interval: time.Hour, SampleEvery: 10 * time.Millisecond,
		Targets: [3]Target{logs: {URL: endpoint.URL}}, Routes: []string{"/api/documents/{id}"}})
	if err != nil {
		t.Fatal(err)
	}
	var pressure atomic.Bool
	recorder.SetSource(func(bool) LocalStats {
		var used int64
		if pressure.Load() {
			used = 90
		}
		return LocalStats{Work: Budget{Limit: 100, Used: used}}
	})
	recorder.Start()
	defer recorder.Close(context.Background())
	ctx := recorder.BeginRequest(t.Context(), "/api/documents/{id}", "PATCH")
	recorder.BodyBytes(ctx, 32)
	admission := recorder.BeginAdmission(WithWork(ctx, WorkDocumentBody))
	admission.Charge(90)
	admission.End()
	defer func() {
		admission.Charge(-90)
		recorder.EndRequest(ctx, 200, false, 0)
	}()
	pressure.Store(true)
	deadline := time.After(3 * time.Second)
	for {
		select {
		case event := <-events:
			if event.EventName != "flow.memory.high_water" {
				continue
			}
			strings := map[string]string{}
			numbers := map[string]int64{}
			for _, attr := range event.Attributes {
				strings[attr.Key] = attr.Value.GetStringValue()
				numbers[attr.Key] = attr.Value.GetIntValue()
			}
			if numbers["flow.metadata.work.used_bytes"] == 0 {
				continue
			}
			if strings["flow.request.1.route"] != "/api/documents/{id}" || strings["flow.request.1.method"] != "PATCH" ||
				strings["flow.holder.1.kind"] != "document_body" || numbers["flow.request.1.work_bytes"] != 90 ||
				numbers["flow.request.1.body_bytes"] != 32 || numbers["flow.metadata.work.used_bytes_max"] != 90 {
				t.Fatal("threshold event omitted the held request or interval peak")
			}
			return
		case <-deadline:
			t.Fatal("threshold event was not exported promptly")
		}
	}
}

func TestTelemetryCgroupFixtures(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "flow")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	fixture := func(directory string, entries map[string]string) {
		t.Helper()
		for name, data := range entries {
			if err := os.WriteFile(filepath.Join(directory, name), []byte(data), 0o400); err != nil {
				t.Fatal(err)
			}
		}
	}
	fixture(dir, map[string]string{"memory.max": "max\n", "memory.current": "100\n", "memory.peak": "120\n"})
	fixture(root, map[string]string{
		"memory.current": "1800\n", "memory.peak": "1900\n", "memory.high": "1800\n", "memory.max": "2000\n",
		"memory.events":   "low 54\nhigh 1\nmax 2\noom 3\noom_kill 4\n",
		"memory.pressure": "some avg10=1.25 avg60=0.10 avg300=0.01 total=900\nfull avg10=0.75 total=450\n",
	})
	check := func(group, mount string, version int) *cgroupReader {
		t.Helper()
		got, parent, detected := locateCgroup(group, "21 4 0:19 /parent "+root+" rw - "+mount)
		if got != dir || parent != root || detected != version {
			t.Fatalf("cgroup version %d was not detected", version)
		}
		r := openCgroup(got, parent, detected)
		t.Cleanup(r.close)
		return r
	}
	v2 := check("0::/parent/flow\n", "cgroup2 cgroup rw", 2)
	var s readings
	v2.fastSample(&s)
	some, full := v2.slowSample(&s)
	if s.Values[cgroupCurrent] != 1800 || s.Values[cgroupLimit] != 2000 || s.Values[cgroupHigh] != 1800 ||
		s.Values[cgroupOOM] != 3 || s.Values[cgroupOOMKill] != 4 || s.Values[cgroupEventsHigh] != 1 ||
		s.Values[cgroupEventsMax] != 2 || s.Values[cgroupSomeTotal] != 900 || some != 1.25 || full != .75 {
		t.Fatal("v2 usage, limit, events and PSI did not describe the same ancestor")
	}
	if got := pressure(s)[0]; got != .9 {
		t.Fatal("threshold sampler missed shared parent pressure")
	}
	_ = v2.high.Close()
	v2.high, _ = os.Open(filepath.Join(root, "memory.peak"))
	v2.resettable = true // a rejected write must fall back to high-water mode
	if peak, reset := v2.peak(true); peak != 1900 || reset != 0 {
		t.Fatalf("read-only peak=%d reset=%d", peak, reset)
	}
	peakFile := filepath.Join(root, "memory.peak")
	if err := os.Chmod(peakFile, 0o600); err != nil {
		t.Fatal(err)
	}
	writable := openCgroup(dir, root, 2)
	defer writable.close()
	if peak, reset := writable.peak(true); peak != 1900 || reset != 1 {
		t.Fatalf("writable peak=%d reset=%d", peak, reset)
	}
	if raw, err := os.ReadFile(peakFile); err != nil || string(raw) != "0900\n" {
		t.Fatal("expected a reset write at the start of the open peak file")
	}
	fixture(dir, map[string]string{"memory.usage_in_bytes": "100\n", "memory.limit_in_bytes": "3200\n"})
	fixture(root, map[string]string{
		"memory.usage_in_bytes": "2200\n", "memory.max_usage_in_bytes": "2300\n", "memory.limit_in_bytes": "2400\n",
		"memory.soft_limit_in_bytes": "1900\n", "memory.failcnt": "12\n", "memory.oom_control": "under_oom 0\noom_kill 2\n",
	})
	v1 := check("6:memory:/parent/flow\n", "cgroup cgroup rw,memory", 1)
	s = readings{}
	v1.fastSample(&s)
	some, full = v1.slowSample(&s)
	peak, reset := v1.peak(true)
	if s.Values[cgroupCurrent] != 2200 || s.Values[cgroupLimit] != 2400 || s.Values[cgroupHigh] != 1900 ||
		s.Values[cgroupEventsMax] != 12 || s.Values[cgroupOOMKill] != 2 || peak != 2300 || reset != 0 ||
		!math.IsNaN(some) || !math.IsNaN(full) {
		t.Fatal("v1 limits or counters were not parsed")
	}
}

func TestTelemetryCgroupNamespaceAndHybrid(t *testing.T) {
	mounts := "21 4 0:19 /docker/container /sys/fs/cgroup rw - cgroup2 cgroup rw\n" +
		"22 4 0:20 / /sys/fs/cgroup/memory rw - cgroup cgroup rw,memory"
	if got, root, version := locateCgroup("0::/\n", mounts); got != "/sys/fs/cgroup" || root != got || version != 2 {
		t.Fatal("container cgroup root did not resolve in a private namespace")
	}
	if got, _, version := locateCgroup("0::/\n6:memory:/flow\n", mounts); got != "/sys/fs/cgroup/memory/flow" || version != 1 {
		t.Fatal("hybrid hierarchy did not prefer the explicit memory controller")
	}
}

func TestTelemetryQueueAndBackoff(t *testing.T) {
	var calls atomic.Int64
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer endpoint.Close()
	r, err := New(Options{Enabled: true, Targets: [3]Target{{URL: endpoint.URL}, {URL: endpoint.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	for range cap(r.traceQueue) + 20 {
		r.queueTrace(traceRecord{})
	}
	if len(r.traceQueue) != cap(r.traceQueue) || r.recordDrops[traces].Load() != 20 {
		t.Fatal("queue did not drop when full")
	}
	select {
	case <-r.urgentTrace:
	default:
		t.Fatal("trace queue did not request an early flush")
	}
	r.flushSignal(context.Background(), metrics, time.Now())
	r.flushSignal(context.Background(), traces, time.Now())
	r.queueTrace(traceRecord{})
	r.flushSignal(context.Background(), metrics, time.Now())
	r.flushSignal(context.Background(), traces, time.Now())
	if calls.Load() != 2 || r.batchDrops[metrics].Load() != 2 || r.batchDrops[traces].Load() != 2 {
		t.Fatal("failed batches did not enter independent backoff")
	}
}

func BenchmarkTelemetrySampler(b *testing.B) {
	benchmarkTelemetrySampler(b, nil)
}

func BenchmarkTelemetrySamplerKernel(b *testing.B) {
	if runtime.GOOS != "linux" {
		b.Skip("requires Linux cgroup and procfs")
	}
	cgroup := platformCgroup()
	defer cgroup.close()
	if cgroup.current == nil || cgroup.statm == nil {
		b.Skip("cgroup memory controller or procfs unavailable")
	}
	var sample readings
	cgroup.fastSample(&sample)
	if !sample.has(cgroupCurrent) || !sample.has(rss) {
		b.Fatal("could not read cgroup usage and current RSS")
	}
	benchmarkTelemetrySampler(b, cgroup)
}

func benchmarkTelemetrySampler(b *testing.B, cgroup *cgroupReader) {
	reader := newRuntimeSampler()
	var window extrema
	thresholds := [3]float64{.7, .85, .95}
	var state thresholdState
	b.ReportAllocs()
	for b.Loop() {
		s := reader.read()
		if cgroup != nil {
			cgroup.fastSample(&s)
		}
		s.set(workUsed, 24<<20)
		s.set(workLimit, 64<<20)
		s.set(workWaiting, 1)
		window.add(s)
		state.update(time.Now(), pressure(s), thresholds, .8)
	}
}
