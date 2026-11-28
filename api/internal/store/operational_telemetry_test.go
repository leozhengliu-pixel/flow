package store

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/telemetry"

	metrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"
)

func TestTelemetryStaleScopedWriteReportsFailureAndRetry(t *testing.T) {
	f, view, workspace, _ := conflictSignalFixture(t)
	if err := f.MutateWorkspace(t.Context(), workspace, "view.created", "peer-view", nil, func(data *domain.Bootstrap) error {
		data.SavedViews = append(data.SavedViews, domain.SavedView{ID: "peer-view", Name: "Peer view", Scope: "workspace"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	const route = "/api/views"
	batches := make(chan *metrics.ExportMetricsServiceRequest, 8)
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		batch := &metrics.ExportMetricsServiceRequest{}
		if proto.Unmarshal(raw, batch) != nil {
			t.Error("invalid OTLP metrics")
			return
		}
		select {
		case batches <- batch:
		default:
		}
	}))
	defer collector.Close()
	rec, err := telemetry.New(telemetry.Options{Enabled: true, Interval: time.Hour, Routes: []string{route},
		DatabaseDriver: view.dialect, Targets: [3]telemetry.Target{{URL: collector.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	rec.Start()
	defer rec.Close(context.Background())
	ctx := rec.BeginRequest(t.Context(), route, "POST")
	callbacks := 0
	err = view.MutateSavedViewRecords(ctx, workspace, "view.created", "retry-view", nil, nil, func(data *domain.Bootstrap) (string, error) {
		callbacks++
		data.SavedViews = append(data.SavedViews, domain.SavedView{ID: "retry-view", Name: "Retry view", Scope: "workspace"})
		return "retry-view", nil
	})
	rec.EndRequest(ctx, http.StatusCreated, false, 0)
	if err != nil || callbacks != 1 {
		t.Fatalf("retried scoped write result %v, callbacks %d", err, callbacks)
	}
	rec.Close(context.Background())
	read := func(batch *metrics.ExportMetricsServiceRequest) map[string]int64 {
		counts := make(map[string]int64)
		for _, resource := range batch.ResourceMetrics {
			for _, scope := range resource.ScopeMetrics {
				for _, m := range scope.Metrics {
					for _, point := range m.GetSum().GetDataPoints() {
						attrs := make(map[string]string, len(point.Attributes))
						for _, attr := range point.Attributes {
							attrs[attr.Key] = attr.Value.GetStringValue()
						}
						if attrs["http.route"] == route && attrs["flow.status"] == "concurrent_write" && attrs["flow.kind"] == view.dialect {
							counts[m.Name] += point.GetAsInt()
						}
					}
				}
			}
		}
		return counts
	}
	select {
	case batch := <-batches:
		counts := read(batch)
		if counts["flow.db.transaction.failures"] != 1 || counts["flow.db.transaction.retries"] != 1 {
			t.Fatalf("stale-source rollback and retry must be counted separately: %v", counts)
		}
	default:
		t.Fatal("no metrics on shutdown")
	}
}

// This is the uncontended allocation path before the telemetry change. Keep
// the same lease, timer, release closure, and accounting objects as baseline.
func acquireBeforeTelemetry(b *metadataWorkBudget, ctx context.Context, bytes int, wait bool) (context.Context, func()) {
	if wait {
		timer := time.NewTimer(time.Duration(metadataWorkRetryWait.Load()))
		defer timer.Stop()
	}
	demand := metadataWorkDemandFrom(ctx)
	b.mu.Lock()
	demand.charge(bytes)
	b.used += bytes
	b.peak = max(b.peak, b.used)
	lease := &metadataWorkLease{b, bytes, true}
	b.mu.Unlock()
	var once sync.Once
	release := func() {
		once.Do(func() {
			b.mu.Lock()
			lease.live = false
			b.used -= bytes
			demand.charge(-bytes)
			close(b.changed)
			b.changed = make(chan struct{})
			b.mu.Unlock()
		})
	}
	return context.WithValue(ctx, metadataWorkLeaseKey{}, lease), release
}

func workAdmissionCases(t testing.TB, additional bool) (legacy, disabled func()) {
	budget := newMetadataWorkBudget(4096)
	s := SQLiteStore{metadataResidency: &metadataResidency{work: budget}}
	ctx := context.Background()
	return func() {
			_, release := acquireBeforeTelemetry(budget, ctx, 1024, !additional)
			release()
		}, func() {
			var release func()
			var err error
			if additional {
				_, release, err = s.tryAdmitAdditionalMetadataWork(ctx, 1024)
			} else {
				_, release, err = budget.acquire(ctx, 1024)
			}
			if err != nil {
				t.Fatal(err)
			}
			release()
		}
}

func TestTelemetryDisabledWorkAdmissionAllocations(t *testing.T) {
	for _, additional := range []bool{false, true} {
		legacy, disabled := workAdmissionCases(t, additional)
		before, after := testing.AllocsPerRun(100, legacy), testing.AllocsPerRun(100, disabled)
		if after > before {
			t.Errorf("additional=%v: disabled admission allocates %g objects per lease, baseline %g", additional, after, before)
		}
	}
}

func BenchmarkTelemetryWorkAdmissionOff(b *testing.B) {
	for _, tc := range []struct {
		name       string
		additional bool
	}{{"work", false}, {"additional", true}} {
		legacy, disabled := workAdmissionCases(b, tc.additional)
		for _, impl := range []struct {
			name string
			run  func()
		}{{"baseline", legacy}, {"off", disabled}} {
			b.Run(tc.name+"/"+impl.name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					impl.run()
				}
			})
		}
	}
}

func BenchmarkTelemetryWorkBudgetSnapshot(b *testing.B) {
	s := SQLiteStore{metadataResidency: &metadataResidency{work: newMetadataWorkBudget(64 << 20)}}
	b.ReportAllocs()
	for b.Loop() {
		if stats := s.TelemetryStats(false); stats.Work.Limit != 64<<20 {
			b.Fatal("unexpected work budget limit")
		}
	}
}
