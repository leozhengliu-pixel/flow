package main

import (
	"bufio"
	"context"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
	"flow/api/internal/telemetry"
	"github.com/coder/websocket"
	logs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	traces "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestTelemetryDisabledIsTransparent(t *testing.T) {
	var calls atomic.Int64
	collector := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer collector.Close()
	rec, err := configureTelemetry(appconfig.Config{Telemetry: appconfig.TelemetryConfig{
		Targets: [3]appconfig.TelemetryTarget{{URL: collector.URL}},
	}})
	if err != nil || rec != nil {
		t.Fatal("disabled telemetry constructed a recorder")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	wrapped := telemetryHandler(rec, mux, mux, false)
	if wrapped != mux {
		t.Fatal("disabled telemetry wrapped the handler")
	}
	response := httptest.NewRecorder()
	wrapped.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if response.Code != http.StatusNoContent || calls.Load() != 0 || telemetry.Enabled() {
		t.Fatalf("status=%d collector_calls=%d recorder_started=%v", response.Code, calls.Load(), telemetry.Enabled())
	}
}

func TestTelemetryLegacyTransport(t *testing.T) {
	type export struct{ path, encoding string }
	loaded := make(chan export, 16)
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/x-protobuf" ||
			r.Header.Get("Authorization") != "Bearer fixture-legacy" {
			t.Error("legacy transport did not send its configured credentials and protocol")
		}
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case loaded <- export{r.URL.Path, r.Header.Get("Content-Encoding")}:
		default:
		}
	}))
	defer collector.Close()
	t.Setenv("FLOW_TELEMETRY_ENABLED", "true")
	t.Setenv("OTEL_SDK_DISABLED", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", collector.URL+"/prefix")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", collector.URL+"/custom-traces/")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "authorization=Bearer%20fixture-legacy")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_HEADERS", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_HEADERS", "")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_COMPRESSION", "gzip")
	t.Setenv("OTEL_METRIC_EXPORT_INTERVAL", "25")
	t.Setenv("OTEL_BSP_SCHEDULE_DELAY", "25")
	t.Setenv("OTEL_TRACES_SAMPLER", "always_on")
	cfg, err := appconfig.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Telemetry.SnapshotDisabled = true
	rec, err := configureTelemetry(cfg)
	if err != nil || rec == nil {
		t.Fatal("legacy configuration failed to start telemetry")
	}
	rec.Start()
	defer rec.Close(t.Context())
	ctx := rec.BeginRequest(t.Context(), "/api/health", http.MethodGet)
	rec.EndRequest(ctx, http.StatusOK, false, 0)
	pending := map[string]bool{"/prefix/v1/metrics": true, "/custom-traces/": true}
	deadline := time.After(3 * time.Second)
	for len(pending) > 0 {
		select {
		case got := <-loaded:
			if got.path == "/prefix/v1/metrics" && got.encoding != "gzip" {
				t.Fatal("legacy metric compression did not apply")
			}
			delete(pending, got.path)
		case <-deadline:
			t.Fatalf("legacy destinations not observed: %v", pending)
		}
	}
}

func TestTelemetryLegacyTLSRefusesDowngrade(t *testing.T) {
	tlsCollector := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer tlsCollector.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: tlsCollector.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	plain := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer plain.Close()
	t.Setenv("FLOW_TELEMETRY_ENABLED", "true")
	t.Setenv("OTEL_SDK_DISABLED", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", tlsCollector.URL)
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "https"+strings.TrimPrefix(plain.URL, "http"))
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_INSECURE", "true")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_CERTIFICATE", ca)
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_HEADERS", "authorization=Bearer%20fixture-key")
	if _, err := appconfig.Load(); err == nil || !strings.Contains(err.Error(), "insecure flag") {
		t.Fatal("contradictory TLS settings were accepted")
	}
	if calls.Load() != 0 {
		t.Fatal("legacy collector credentials reached plaintext")
	}
}

func TestTelemetryLegacyOverridesDoNotStartLogs(t *testing.T) {
	var staleCalls atomic.Int64
	stale := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { staleCalls.Add(1) }))
	defer stale.Close()
	metricLabels := make(chan map[string]string, 1)
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-key" {
			t.Error("missing legacy header on the existing destination")
		}
		if r.URL.Path != "/metrics" {
			return
		}
		batch := &metrics.ExportMetricsServiceRequest{}
		raw, _ := io.ReadAll(r.Body)
		if proto.Unmarshal(raw, batch) != nil || len(batch.ResourceMetrics) == 0 {
			t.Error("invalid legacy metrics export")
			return
		}
		attrs := make(map[string]string)
		for _, attr := range batch.ResourceMetrics[0].Resource.Attributes {
			attrs[attr.Key] = attr.Value.GetStringValue()
		}
		select {
		case metricLabels <- attrs:
		default:
		}
	}))
	defer collector.Close()
	t.Setenv("FLOW_TELEMETRY_ENABLED", "true")
	t.Setenv("OTEL_SDK_DISABLED", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", stale.URL)
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", collector.URL+"/metrics")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", collector.URL+"/traces")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "authorization=Bearer%20fixture-key")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_COMPRESSION", "")
	t.Setenv("OTEL_METRIC_EXPORT_INTERVAL", "30")
	t.Setenv("FLOW_ENVIRONMENT", "staging")
	cfg, err := appconfig.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Telemetry.SnapshotDisabled = true
	cfg.Telemetry.SampleEvery = time.Millisecond
	rec, err := configureTelemetry(cfg)
	if err != nil {
		t.Fatal(err)
	}
	sampled := make(chan struct{}, 1)
	rec.SetSource(func(bool) telemetry.LocalStats {
		select {
		case sampled <- struct{}{}:
		default:
		}
		return telemetry.LocalStats{Work: telemetry.Budget{Limit: 100, Used: 90}}
	})
	rec.Start()
	defer rec.Close(t.Context())
	select {
	case attrs := <-metricLabels:
		if attrs["deployment.environment"] != "staging" || attrs["deployment.environment.name"] != "staging" {
			t.Fatal("legacy deployment environment missing from the payload")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("metrics export did not arrive")
	}
	select {
	case <-sampled:
	default:
		t.Fatal("diagnostic sampler did not observe the work crossing")
	}
	rec.Close(t.Context())
	if cfg.Telemetry.Targets[2].URL != "" || staleCalls.Load() != 0 {
		t.Fatal("unused legacy shared endpoint received a new signal")
	}
}

func telemetryTestSession(t *testing.T) (*store.SQLiteStore, *http.Cookie) {
	t.Helper()
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "telemetry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	_, token, err := repo.Login(t.Context(), "admin@example.test", "test-password")
	if err != nil {
		t.Fatal(err)
	}
	return repo, &http.Cookie{Name: sessionCookieName, Value: token}
}

func TestTelemetryExportPrivacyAndSlowTail(t *testing.T) {
	received := make(chan struct{ path, json string }, 32)
	var authSeen atomic.Bool
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authSeen.Store(r.Header.Get("Authorization") == "Bearer fixture-collector-key")
		var request proto.Message
		switch r.URL.Path {
		case "/v1/metrics":
			request = &metrics.ExportMetricsServiceRequest{}
		case "/v1/traces":
			request = &traces.ExportTraceServiceRequest{}
		case "/v1/logs":
			request = &logs.ExportLogsServiceRequest{}
		default:
			t.Error("unexpected OTLP endpoint")
			w.WriteHeader(404)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		if err := proto.Unmarshal(raw, request); err != nil {
			t.Error("invalid OTLP protobuf")
		}
		json, _ := protojson.Marshal(request)
		select {
		case received <- struct{ path, json string }{r.URL.Path, string(json)}:
		default:
		}
	}))
	defer collector.Close()
	target := func(s string) telemetry.Target {
		return telemetry.Target{URL: collector.URL + "/v1/" + s, Headers: map[string]string{"Authorization": "Bearer fixture-collector-key"}}
	}
	rec, err := telemetry.New(telemetry.Options{
		Enabled: true, Frontend: true, Interval: 20 * time.Millisecond, SlowRequest: time.Millisecond, SampleRate: 0,
		Targets: [3]telemetry.Target{target("metrics"), target("traces"), target("logs")},
		Routes:  []string{"/api/issues/{id}", telemetryPath}, Assets: []string{"index-0abc1234.js"}, DatabaseDriver: "sqlite",
	})
	if err != nil {
		t.Fatal(err)
	}
	rec.Start()
	defer rec.Close(t.Context())
	repo, session := telemetryTestSession(t)
	s := &server{store: repo, telemetry: rec, browserTelemetry: newBrowserTelemetry(1)}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/telemetry", s.ingestTelemetry)
	mux.HandleFunc("PATCH /api/issues/{id}", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		admission := rec.BeginAdmission(r.Context())
		admission.Charge(32768)
		defer admission.Charge(-32768)
		admission.Refused(true)
		defer admission.End()
		defer telemetry.BeginMutation(r.Context(), "fixture-title-secret").End()
		defer telemetry.BeginSQL(r.Context(), `SELECT 'private@example.test' FROM issue_records WHERE id = 'fixture-api-key'`).End()
		defer telemetry.BeginExternal(r.Context(), "webhook", "private-tenant.example.test").End()
		telemetry.DatabaseRetry(r.Context(), "", "concurrent_write", true)
		time.Sleep(2 * time.Millisecond)
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "fixture-private-response")
	})
	handler := telemetryHandler(rec, mux, mux, true)
	r := httptest.NewRequest(http.MethodPatch, "/api/issues/904ffdd9-cd7a-410f-a718-88cc70e3dcb5?token=fixture-api-key", strings.NewReader("fixture-private-body"))
	r.Header.Set("Cookie", "session=fixture-cookie")
	r.Header.Set("Authorization", "Bearer fixture-api-key")
	r.Header.Set("User-Agent", "fixture-ua")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, r)
	if response.Code != 503 || response.Header().Get("Flow-Telemetry-Route") != "/api/issues/{id}" {
		t.Fatal("request metrics did not use the registered route")
	}
	body := `[
		{"kind":"api","route":"/api/issues/fixture-id","method":"fixture-api-key","status":"5xx","email":"private@example.test"},
		{"kind":"navigation","route":"https://private-tenant.example.test/path?fixture-api-key","value":1},
		{"kind":"js_error","route":"/{workspace}/issue/{id}","hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		 "frames":["index-0abc1234.js:21","index-0abc1234.js:21?fixture-api-key","private@example.test:22","fake-12345678.js:42"],
		 "message":"fixture-private-response"},
		{"kind":"js_error","hash":"fixture-api-key"},
		{"kind":"private@example.test","value":4}
	]`
	r = httptest.NewRequest("POST", "http://flow.test/api/telemetry", strings.NewReader(body))
	r.Header.Set("Origin", "http://flow.test")
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(session)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, r)
	if response.Code != http.StatusNoContent {
		t.Fatalf("browser intake status %d", response.Code)
	}
	seen := map[string]bool{}
	deadline := time.After(3 * time.Second)
	for len(seen) < 5 {
		select {
		case batch := <-received:
			for _, bad := range []string{
				"fixture-api-key", "fixture-collector-key", "fixture-title-secret", "fixture-private-response", "fixture-private-body",
				"fixture-cookie", session.Value, "fixture-ua", "private@example.test", "private-tenant.example.test", "904ffdd9", "/api/issues/fixture-id",
				"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "fake-12345678.js",
			} {
				if strings.Contains(batch.json, bad) {
					t.Errorf("privacy allowlist failed for %s", batch.path)
				}
			}
			switch batch.path {
			case "/v1/metrics":
				if strings.Contains(batch.json, "flow.http.server.busy") && strings.Contains(batch.json, "flow.db.statement.duration") &&
					strings.Contains(batch.json, "flow.db.transaction.retries") && strings.Contains(batch.json, "concurrent_write") {
					seen["server metrics"] = true
				}
				if strings.Contains(batch.json, "flow.browser.api.requests") {
					seen["browser metrics"] = true
				}
			case "/v1/traces":
				if strings.Contains(batch.json, "flow.trace.selection") && strings.Contains(batch.json, "issue_records") {
					seen["traces"] = true
				}
			case "/v1/logs":
				if strings.Contains(batch.json, "flow.request.slow") && strings.Contains(batch.json, "flow.stage.db.statement_us") {
					seen["server logs"] = true
				}
				if strings.Contains(batch.json, "index-0abc1234.js:21") {
					seen["browser logs"] = true
				}
			}
		case <-deadline:
			t.Fatalf("missing expected telemetry signals: %v", seen)
		}
	}
	if !authSeen.Load() {
		t.Fatal("configured collector header not sent")
	}
}

func TestTelemetrySSEFrameMetricsMatchWire(t *testing.T) {
	received := make(chan *metrics.ExportMetricsServiceRequest, 1)
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		batch := &metrics.ExportMetricsServiceRequest{}
		raw, err := io.ReadAll(r.Body)
		if err != nil || proto.Unmarshal(raw, batch) != nil {
			http.Error(w, "invalid metric batch", http.StatusBadRequest)
			return
		}
		select {
		case received <- batch:
		default:
		}
	}))
	defer collector.Close()
	rec, err := telemetry.New(telemetry.Options{
		Enabled: true, Interval: 20 * time.Millisecond,
		Targets: [3]telemetry.Target{{URL: collector.URL + "/v1/metrics"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rec.Close(t.Context())
	budget := newRealtimeBudget()
	sse := httptest.NewRecorder()
	writer := &realtimeBudgetWriter{ResponseWriter: sse, budget: budget, telemetry: rec}
	event := domain.RealtimeEvent{ID: "event", Type: "document.updated", Payload: []byte("{\n\"data\":1}")}
	if !writeSSE(writer, event, "node") || !flushRealtime(writer) || budget.used.Load() != 0 {
		t.Fatal("SSE write failed or leaked its frame lease")
	}
	sync := httptest.NewRecorder()
	writer = &realtimeBudgetWriter{ResponseWriter: sync, budget: budget, telemetry: rec}
	frame, err := syncTransactionFrame(t.Context(), budget, emptySyncTransaction("transaction", "1"), "epoch")
	if err != nil {
		t.Fatal(err)
	}
	wrote := frame.WriteSSE(writer, "node") && flushRealtime(writer)
	frame.Close()
	if !wrote || budget.used.Load() != 0 {
		t.Fatal("Sync write failed or leaked its encoding or frame lease")
	}
	rec.Start()
	select {
	case batch := <-received:
		want := map[[2]string]int{{"sse", "document.updated"}: sse.Body.Len(), {"sync", "sync.delta"}: sync.Body.Len()}
		for _, resource := range batch.ResourceMetrics {
			for _, scope := range resource.ScopeMetrics {
				for _, metric := range scope.Metrics {
					if metric.Name != "flow.realtime.frame.size" {
						continue
					}
					for _, point := range metric.GetHistogram().GetDataPoints() {
						var labels [2]string
						for _, attr := range point.Attributes {
							switch attr.Key {
							case "flow.method":
								labels[0] = attr.Value.GetStringValue()
							case "flow.kind":
								labels[1] = attr.Value.GetStringValue()
							}
						}
						if size, ok := want[labels]; !ok || point.Count != 1 || point.GetSum() != float64(size) {
							t.Errorf("frame %v exported %d samples totaling %g bytes, want one sample of %d bytes", labels, point.Count, point.GetSum(), size)
						}
						delete(want, labels)
					}
				}
			}
		}
		if len(want) != 0 {
			t.Fatalf("missing frame measurements: %v", want)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("collector did not receive frame metrics")
	}
}

func TestTelemetryIngestLimits(t *testing.T) {
	rec, err := telemetry.New(telemetry.Options{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	repo, session := telemetryTestSession(t)
	s := &server{store: repo, telemetry: rec, browserTelemetry: newBrowserTelemetry(1)}
	send := func(origin, body string) int {
		r := httptest.NewRequest("POST", "http://flow.test/api/telemetry", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", origin)
		r.AddCookie(session)
		response := httptest.NewRecorder()
		s.ingestTelemetry(response, r)
		return response.Code
	}
	for _, check := range []struct {
		origin, body string
		want         int
	}{
		{"https://elsewhere.test", "[]", 403},
		{"http://flow.test", strings.Repeat(" ", telemetry.BrowserBatchBytes+1), 413},
		{"http://flow.test", `[{"value":"secret"}]`, 400},
		{"http://flow.test", `[` + strings.Repeat(`{},`, telemetry.BrowserBatchRecords) + `{}]`, 400},
		{"http://flow.test", "[] {}", 400},
		{"http://flow.test", "[]", 204},
	} {
		if status := send(check.origin, check.body); status != check.want {
			t.Errorf("got %d want %d", status, check.want)
		}
	}
	for range cap(s.browserTelemetry.slots) {
		s.browserTelemetry.slots <- struct{}{}
	}
	if got := send("http://flow.test", "[]"); got != 429 {
		t.Fatalf("at concurrency cap: %d", got)
	}
	s.browserTelemetry = nil
	if got := send("http://flow.test", "[]"); got != 404 {
		t.Fatalf("disabled ingest: %d", got)
	}
}

func TestTelemetryIngestAuthenticatesAndUsesProxyOrigin(t *testing.T) {
	repo, session := telemetryTestSession(t)
	rec, err := telemetry.New(telemetry.Options{Enabled: true, Frontend: true})
	if err != nil {
		t.Fatal(err)
	}
	security := appconfig.DefaultSecurityHeaders()
	security.TrustProxyHeaders = true
	security.TrustedProxies = []string{"10.0.0.0/8"}
	s := &server{
		store: repo, telemetry: rec, browserTelemetry: newBrowserTelemetry(1),
		securityHeaders: &security, allowedOrigin: "https://flow.example.test",
	}
	handler := newHandler(s)
	send := func(path, peer string, cookie *http.Cookie, authorization string) *httptest.ResponseRecorder {
		method := http.MethodPost
		if path != telemetryPath {
			method = http.MethodGet
		}
		r := httptest.NewRequest(method, "http://upstream.test"+path, strings.NewReader(`[]`))
		r.RemoteAddr = peer
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "https://alias.example.test")
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		r.Header.Set("X-Forwarded-Host", "ignored.test, alias.example.test")
		r.Header.Set("X-Forwarded-Proto", "http, https")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		if authorization != "" {
			r.Header.Set("Authorization", authorization)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	before := s.browserTelemetry.limiter.Tokens()
	for _, tc := range []struct {
		peer, auth string
		cookie     *http.Cookie
		want       int
	}{
		{"10.0.0.1:9000", "", nil, 401},
		{"10.0.0.1:9000", "", &http.Cookie{Name: sessionCookieName, Value: "invalid"}, 401},
		{"10.0.0.1:9000", "Bearer fixture-key", session, 401},
		{"203.0.113.4:9000", "", session, 403},
	} {
		if w := send(telemetryPath, tc.peer, tc.cookie, tc.auth); w.Code != tc.want {
			t.Errorf("got %d, want %d", w.Code, tc.want)
		}
	}
	if s.browserTelemetry.limiter.Tokens() < before {
		t.Fatal("unauthenticated requests spent the browser report budget")
	}
	if w := send(telemetryPath, "10.0.0.1:9000", session, ""); w.Code != 204 {
		t.Fatalf("authenticated report through a trusted proxy returned %d", w.Code)
	}
	s.browserTelemetry.limiter.SetBurst(0)
	if w := send(telemetryPath, "10.0.0.1:9000", session, ""); w.Code != 429 {
		t.Fatalf("intake did not enforce its own rate limit: %d", w.Code)
	}
	for _, tc := range []struct {
		path, want string
		cookie     *http.Cookie
	}{
		{"/api/auth/providers", "", nil},
		{"/api/auth/session", "", nil},
		{"/api/auth/session", telemetryPath, session},
	} {
		if w := send(tc.path, "10.0.0.1:9000", tc.cookie, ""); w.Header().Get("Flow-Telemetry") != tc.want {
			t.Errorf("unexpected browser configuration on %s", tc.path)
		}
	}
}

func TestTelemetryDisabledIntakeIs404BeforeMiddleware(t *testing.T) {
	repo, _ := telemetryTestSession(t)
	handler := newHandler(&server{store: repo})
	for _, method := range []string{http.MethodGet, http.MethodOptions, http.MethodPost} {
		r := httptest.NewRequest(method, telemetryPath, strings.NewReader("[]"))
		r.Header.Set("Content-Type", "application/json")
		r.ContentLength = maxJSONRequestBytes + 1
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s got %d, want 404", method, w.Code)
		}
	}
}

func TestTelemetryRouteRedirectDoesNotBecomeAPattern(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/issues/{id}/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	rec, err := telemetry.New(telemetry.Options{Enabled: true, Routes: []string{"/api/issues/{id}/"}})
	if err != nil {
		t.Fatal(err)
	}
	rec.Start()
	defer rec.Close(context.Background())
	response := httptest.NewRecorder()
	telemetryHandler(rec, mux, mux, true).ServeHTTP(response, httptest.NewRequest("GET", "/api//issues/private-id", nil))
	if response.Code/100 != 3 || response.Header().Get("Flow-Telemetry-Route") != "" {
		t.Fatalf("unexpected redirect status or browser route label: %d", response.Code)
	}
}

func TestTelemetryStreamingPreservesInterfaces(t *testing.T) {
	rec, err := telemetry.New(telemetry.Options{Enabled: true, Routes: []string{"/api/realtime/events", "/api/realtime/socket"}})
	if err != nil {
		t.Fatal(err)
	}
	rec.Start()
	defer rec.Close(context.Background())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/realtime/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {}\n\n")
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Error("stream flush failed")
			return
		}
		<-r.Context().Done()
	})
	mux.HandleFunc("GET /api/realtime/socket", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error("socket upgrade failed")
			return
		}
		defer conn.CloseNow()
		if err := conn.Write(r.Context(), websocket.MessageBinary, []byte{42}); err != nil {
			t.Error("socket write failed")
		}
	})
	server := httptest.NewServer(telemetryHandler(rec, mux, mux, false))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	stream, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/realtime/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := server.Client().Do(stream)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || line != "data: {}\n" {
		t.Fatal("stream was not delivered before the handler returned")
	}
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/api/realtime/socket", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	kind, data, err := conn.Read(ctx)
	if err != nil || kind != websocket.MessageBinary || len(data) != 1 || data[0] != 42 {
		t.Fatal("upgraded connection did not deliver the binary frame")
	}
}
