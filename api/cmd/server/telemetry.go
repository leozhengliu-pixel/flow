package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/telemetry"

	"github.com/felixge/httpsnoop"
	"golang.org/x/time/rate"
)

const telemetryPath = "/api/telemetry"

func (s *server) telemetryStats(full bool) telemetry.LocalStats {
	stats := s.store.TelemetryStats(full)
	if full {
		stats.RequestBodyLimit = s.documentBodyLimit()
		if s.realtime != nil && s.realtime.budget != nil {
			b := s.realtime.budget
			stats.Realtime = telemetry.Budget{Used: b.used.Load(), Limit: b.limit, Peak: b.peak.Load()}
		}
	}
	return stats
}

func configureTelemetry(cfg appconfig.Config) (*telemetry.Recorder, error) {
	t := cfg.Telemetry
	if !t.Enabled {
		return nil, nil
	}
	o := telemetry.Options{
		Enabled: true, Frontend: t.Frontend && !cfg.AuthDisabled, Interval: t.Interval, SlowRequest: t.SlowRequest, SampleRate: t.SampleRate,
		SampleEvery: t.SampleEvery, Thresholds: t.Thresholds, WorkHighWater: t.HighWater,
		SnapshotDir: t.SnapshotDir, HeapProfileDir: t.HeapProfileDir, HeapProfileMaxBytes: t.HeapProfileMaxBytes,
		Revision: commit, Environment: t.Environment, DatabaseDriver: strings.ToLower(strings.TrimSpace(cfg.Database.Driver)),
		LegacyMetricsTemporality: t.LegacyMetricsTemporality, LegacyMetricsHistogram: t.LegacyMetricsHistogram,
	}
	if o.Revision == "" || o.Revision == "unknown" {
		if build, ok := debug.ReadBuildInfo(); ok {
			for _, setting := range build.Settings {
				if setting.Key == "vcs.revision" {
					o.Revision = setting.Value
				}
			}
		}
	}
	if o.SnapshotDir == "" && !t.SnapshotDisabled {
		dir := filepath.Dir(cfg.Database.Path)
		o.SnapshotDir, _ = filepath.Abs(filepath.Join(dir, "telemetry"))
	}
	for i, target := range t.Targets {
		o.Targets[i] = telemetry.Target(target)
	}
	for pattern := range scaleRouteTemperature {
		o.Routes = append(o.Routes, telemetryRoute(pattern))
	}
	for _, tool := range flowToolContract() {
		o.Tools = append(o.Tools, tool.Name)
	}
	if endpoint, err := url.Parse(cfg.Agent.BaseURL); err == nil {
		o.Hosts = append(o.Hosts, strings.ToLower(endpoint.Hostname()))
	}
	for _, host := range cfg.Outbound.TrustedHosts {
		o.Hosts = append(o.Hosts, strings.ToLower(host))
	}
	if o.Frontend && cfg.StaticPath != "" {
		o.Assets = bundledTelemetryAssets(cfg.StaticPath)
	}
	return telemetry.New(o)
}

var executableAsset = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,100}-[A-Za-z0-9_-]{8,32}\.m?js$`)

func bundledTelemetryAssets(root string) []string {
	dir, err := os.Open(filepath.Join(root, "assets"))
	if err != nil {
		return nil
	}
	defer dir.Close()
	var assets []string
	for range 32 {
		entries, _ := dir.ReadDir(128)
		for _, entry := range entries {
			if entry.Type().IsRegular() && executableAsset.MatchString(entry.Name()) {
				assets = append(assets, entry.Name())
			}
		}
		if len(entries) < 128 {
			break
		}
	}
	return assets
}

func telemetryRoute(pattern string) string {
	if _, route, ok := strings.Cut(pattern, " "); ok {
		return route
	}
	return pattern
}

type telemetryBody struct {
	io.ReadCloser
	rec *telemetry.Recorder
	ctx context.Context
}

func (b telemetryBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.rec.BodyBytes(b.ctx, int64(n))
	}
	return n, err
}

func telemetryHandler(rec *telemetry.Recorder, mux *http.ServeMux, next http.Handler, browser bool) http.Handler {
	if rec == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == telemetryPath {
			next.ServeHTTP(w, r)
			return
		}
		_, pattern := mux.Handler(r)
		route := telemetry.Unmatched
		// A redirect can return a literal request path as its pattern.
		if _, registered := scaleRouteTemperature[pattern]; registered {
			route = telemetryRoute(pattern)
		}
		if browser && route != telemetry.Unmatched {
			w.Header().Set("Flow-Telemetry-Route", route)
		}
		ctx := rec.BeginRequest(r.Context(), route, r.Method)
		code, written := 0, int64(0)
		busy := false
		writeHeader := func(status int) {
			if code == 0 && (status >= 200 || status == http.StatusSwitchingProtocols) {
				code = status
				busy = status == http.StatusServiceUnavailable && w.Header().Get("Retry-After") != ""
			}
		}
		writer := httpsnoop.Wrap(w, httpsnoop.Hooks{
			Flush: func(next httpsnoop.FlushFunc) httpsnoop.FlushFunc {
				return func() {
					writeHeader(http.StatusOK)
					next()
				}
			},
			Hijack: func(next httpsnoop.HijackFunc) httpsnoop.HijackFunc {
				return func() (net.Conn, *bufio.ReadWriter, error) {
					conn, rw, err := next()
					if err == nil {
						writeHeader(http.StatusSwitchingProtocols)
					}
					return conn, rw, err
				}
			},
			WriteHeader: func(next httpsnoop.WriteHeaderFunc) httpsnoop.WriteHeaderFunc {
				return func(status int) {
					writeHeader(status)
					next(status)
				}
			},
			Write: func(next httpsnoop.WriteFunc) httpsnoop.WriteFunc {
				return func(p []byte) (int, error) {
					writeHeader(http.StatusOK)
					n, err := next(p)
					written += int64(n)
					return n, err
				}
			},
			ReadFrom: func(next httpsnoop.ReadFromFunc) httpsnoop.ReadFromFunc {
				return func(src io.Reader) (int64, error) {
					writeHeader(http.StatusOK)
					n, err := next(src)
					written += n
					return n, err
				}
			},
		})
		defer func() {
			if code == 0 {
				code = http.StatusOK
			}
			rec.EndRequest(ctx, code, busy, written)
		}()
		r = r.WithContext(ctx)
		if r.Body != nil && r.Body != http.NoBody {
			r.Body = telemetryBody{ReadCloser: r.Body, rec: rec, ctx: ctx}
		}
		next.ServeHTTP(writer, r)
	})
}

type browserTelemetry struct {
	sampleRate float64
	limiter    *rate.Limiter
	slots      chan struct{}
}

func newBrowserTelemetry(ratePerPage float64) *browserTelemetry {
	return &browserTelemetry{ratePerPage, rate.NewLimiter(25, 75), make(chan struct{}, 8)}
}

func (s *server) advertiseBrowserTelemetry(w http.ResponseWriter) {
	if s.browserTelemetry != nil {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Flow-Telemetry", telemetryPath)
		w.Header().Set("Flow-Telemetry-Sample-Rate", strconv.FormatFloat(s.browserTelemetry.sampleRate, 'f', -1, 64))
	}
}

// The intake owns its concurrency and rate limits, including the session
// lookup. It never borrows the normal API rate or request telemetry budgets.
func (s *server) routeBrowserTelemetry(mux *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != telemetryPath {
			next.ServeHTTP(w, r)
			return
		}
		if s.browserTelemetry == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *server) ingestTelemetry(w http.ResponseWriter, r *http.Request) {
	b := s.browserTelemetry
	if b == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if !browserTelemetryOrigin(r, s.allowedOrigin) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if cookie, err := r.Cookie(sessionCookieName); err != nil || cookie.Value == "" || r.Header.Get("Authorization") != "" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" || unsupportedJSONEncoding(r) {
		w.WriteHeader(http.StatusUnsupportedMediaType)
		return
	}
	if r.ContentLength > telemetry.BrowserBatchBytes {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		return
	}
	select {
	case b.slots <- struct{}{}:
		defer func() { <-b.slots }()
	default:
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	if _, ok := s.authenticateSession(w, r); !ok {
		return
	}
	if !b.limiter.Allow() {
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	controller := http.NewResponseController(w)
	if controller.SetReadDeadline(time.Now().Add(2*time.Second)) == nil {
		defer controller.SetReadDeadline(time.Time{})
	}
	body := http.MaxBytesReader(w, r.Body, telemetry.BrowserBatchBytes)
	defer body.Close()
	dec := json.NewDecoder(body)
	token, err := dec.Token()
	if err != nil || token != json.Delim('[') {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var records [telemetry.BrowserBatchRecords]telemetry.BrowserRecord
	n := 0
	for dec.More() {
		if n == len(records) || dec.Decode(&records[n]) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		n++
	}
	if token, err := dec.Token(); err != nil || token != json.Delim(']') || dec.Decode(new(json.RawMessage)) != io.EOF {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	for _, row := range records[:n] {
		s.telemetry.RecordBrowser(row)
	}
	w.WriteHeader(http.StatusNoContent)
}

func browserTelemetryOrigin(r *http.Request, configured string) bool {
	site := r.Header.Get("Sec-Fetch-Site")
	if site != "" && site != "same-origin" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return site == "same-origin"
	}
	return origin == externalBaseURL(r) || configured != "" && origin == strings.TrimSuffix(configured, "/")
}
