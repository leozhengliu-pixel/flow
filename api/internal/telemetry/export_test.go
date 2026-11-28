package telemetry

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type stallFirstHandshake struct {
	net.Listener
	stalled atomic.Bool
	closed  chan struct{}
}

func (l *stallFirstHandshake) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil || l.stalled.Swap(true) {
		return conn, err
	}
	go func() {
		defer close(l.closed)
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(4 * time.Second))
		_, _ = io.Copy(io.Discard, conn)
	}()
	return l.Listener.Accept()
}

func TestTelemetryStalledTLSHandshakeReleasesConnection(t *testing.T) {
	var delivered atomic.Int64
	collector := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { delivered.Add(1) }))
	listener := &stallFirstHandshake{Listener: collector.Listener, closed: make(chan struct{})}
	collector.Listener = listener
	collector.StartTLS()
	defer collector.Close()
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: collector.Certificate().Raw})
	if roots := x509.NewCertPool(); !roots.AppendCertsFromPEM(cert) {
		t.Fatal("test collector certificate is invalid")
	}
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, cert, 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := New(Options{Enabled: true, Targets: [3]Target{metrics: {URL: collector.URL, CAFile: ca, Timeout: 200 * time.Millisecond}}})
	if err != nil {
		t.Fatal(err)
	}
	r.flushSignal(t.Context(), metrics, time.Now())
	if delivered.Load() != 0 || r.batchDrops[metrics].Load() != 1 {
		t.Fatal("the stalled handshake was not recorded as a failed export")
	}
	select {
	case <-listener.closed:
	case <-time.After(time.Second):
		t.Fatal("the timed-out handshake still held the transport slot")
	}
	r.flushSignal(t.Context(), metrics, r.targets[metrics].retryAt)
	if delivered.Load() != 1 {
		t.Fatal("the next export could not dial a fresh connection")
	}
}

func TestTelemetryBrowserSaturationKeepsServerExports(t *testing.T) {
	var httpMetric, dbMetric, diagnostic, browserMetric, browserLog atomic.Bool
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		raw, _ := io.ReadAll(req.Body)
		body := string(raw)
		isBrowserMetric := strings.Contains(body, "flow.browser.api.requests")
		isBrowserLog := strings.Contains(body, "flow.browser.js_error")
		if isBrowserMetric || isBrowserLog {
			browserMetric.Store(browserMetric.Load() || isBrowserMetric)
			browserLog.Store(browserLog.Load() || isBrowserLog)
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		if strings.Contains(body, "flow.http.server.duration") && strings.Contains(body, "/api/issues") {
			httpMetric.Store(true)
		}
		if strings.Contains(body, "flow.db.statement.duration") && strings.Contains(body, "SELECT") {
			dbMetric.Store(true)
		}
		if strings.Contains(body, "flow.memory.high_water") {
			diagnostic.Store(true)
		}
		if strings.Contains(body, "private-route") {
			t.Error("unregistered browser route reached the collector")
		}
	}))
	defer collector.Close()
	routes := []string{"/api/issues"}
	for i := range 60 {
		routes = append(routes, fmt.Sprintf("/api/registered-%d", i))
	}
	r, err := New(Options{Enabled: true, Frontend: true, Routes: routes,
		Targets: [3]Target{metrics: {URL: collector.URL}, logs: {URL: collector.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range append([]string{"/api/private-route"}, routes[1:]...) {
		for _, verb := range []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "OTHER"} {
			for _, status := range []string{"2xx", "3xx", "4xx", "5xx", "network"} {
				r.RecordBrowser(BrowserRecord{Kind: "api", Route: route, Method: verb, Status: status})
			}
		}
	}
	for range cap(r.browser.events) + 1 {
		r.RecordBrowser(BrowserRecord{Kind: "js_error", Hash: strings.Repeat("a", 64)})
	}
	if len(r.browser.instruments.count) != maxBrowserSeries || r.browser.instruments.dropped.Load() == 0 ||
		r.browser.recordDrops.Load() != 1 || len(r.eventQueue) != 0 {
		t.Fatal("browser saturation did not stay within its own limits")
	}
	if r.browser.instruments.count[seriesKey{browserAPI, labels{route: Unmatched, method: "GET", status: "2xx"}}] != 1 {
		t.Fatal("unknown route was not clamped to the fallback")
	}
	r.flushBrowser(t.Context(), time.Now())
	if !browserMetric.Load() || !browserLog.Load() || r.browser.metricDrops.Load() != 1 || r.browser.logDrops.Load() != 1 {
		t.Fatal("the rejecting collector did not exercise both independent browser exporters")
	}
	r.requests(labels{route: "/api/issues", method: "GET", status: "2xx"}, time.Millisecond, 0, 0, false, false)
	r.observe(sqlDuration, labels{route: "/api/issues", kind: "SELECT"}, .001)
	r.queueEvent(event{kind: highWater, diagnostic: &diagnosticEvent{snapshot: snapshot{At: time.Now()}, level: 0}})
	r.flushSignal(t.Context(), metrics, time.Now())
	r.flushSignal(t.Context(), logs, time.Now())
	if !httpMetric.Load() || !dbMetric.Load() || !diagnostic.Load() ||
		r.batchDrops[metrics].Load() != 0 || r.batchDrops[logs].Load() != 0 {
		t.Fatal("browser queue pressure or backoff affected a server export")
	}
}
