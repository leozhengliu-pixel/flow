package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// TestLoopRunAtScale measures one representative loop run (list_projects,
// list_issues ×2, a project status update, the final summary) against the
// team scale fixture (75,000 issues, 200,000 metadata records by default):
// peak heap, run duration, the longest workspace write-lock hold and the
// slowest concurrent GET /api/loops while the run works. The model is a local
// scripted stub, so the numbers are Flow's own overhead.
//
//	FLOW_SCALE_TEST=1 FLOW_SCALE_DATABASE_DRIVER=mysql \
//	FLOW_SCALE_DATABASE_URL='root:scale@tcp(127.0.0.1:23306)/flow_scale' \
//	go test ./cmd/server -run TestLoopRunAtScale -v -timeout 60m
//
// It shares the fixture variables of TestTeamMutationsAtScale (THE EXTERNAL
// DATABASE IS WIPED unless FLOW_SCALE_REUSE=1 finds a seeded fixture).
// FLOW_SCALE_LOOP_RUNS sets how many runs are timed (default 1).
func TestLoopRunAtScale(t *testing.T) {
	if os.Getenv("FLOW_SCALE_TEST") != "1" {
		t.Skip("set FLOW_SCALE_TEST=1 to run the loop run scale test")
	}
	// Report every workspace write with its lock hold time.
	t.Setenv("FLOW_SLOW_MUTATION_MS", "1")
	locks := &scaleLockRecorder{}
	previous := slog.Default()
	slog.SetDefault(slog.New(locks))
	defer slog.SetDefault(previous)

	driver := strings.ToLower(strings.TrimSpace(os.Getenv("FLOW_SCALE_DATABASE_DRIVER")))
	if driver == "" {
		driver = "sqlite"
	}
	url := os.Getenv("FLOW_SCALE_DATABASE_URL")
	config := store.DatabaseConfig{Driver: driver, URL: url, FixtureProfile: "test", FixturePassword: "test-password"}
	if driver == "sqlite" {
		config.Path = os.Getenv("FLOW_SCALE_SQLITE_PATH")
		if config.Path == "" {
			config.Path = filepath.Join(t.TempDir(), "flow.db")
		}
		config.MaxOpenConns = 1
		url = config.Path
	} else if url == "" {
		t.Fatal("FLOW_SCALE_DATABASE_URL is required for " + driver)
	}
	issues := scaleEnvInt("FLOW_SCALE_ISSUES", 75000)
	metadata := scaleEnvInt("FLOW_SCALE_METADATA", 200000)
	db := openScaleDatabase(t, driver, url)
	defer db.Close()
	seeded := false
	if os.Getenv("FLOW_SCALE_REUSE") == "1" {
		var count int
		if db.QueryRow(`SELECT COUNT(*) FROM issue_records WHERE workspace_key='test-workspace'`).Scan(&count) == nil && count >= issues {
			seeded = true
			t.Logf("reusing seeded %s fixture with %d issues", driver, count)
		}
	}
	if !seeded {
		started := time.Now()
		if driver != "sqlite" {
			resetScaleDatabase(t, db)
		}
		repository, err := store.OpenDatabase(config)
		if err != nil {
			t.Fatal(err)
		}
		if err := repository.Close(); err != nil {
			t.Fatal(err)
		}
		seedTeamScaleFixture(t, db, issues, metadata)
		t.Logf("seeded %s fixture in %s", driver, time.Since(started).Round(time.Millisecond))
	}
	repository, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	projects, _ := repository.WorkspaceMetadataFields("test-workspace", "projects")
	if len(projects.Projects) == 0 {
		t.Fatal("fixture has no projects")
	}
	projectID := projects.Projects[0].ID

	provider := &scaleLoopProvider{projectID: projectID}
	upstream := httptest.NewServer(http.HandlerFunc(provider.serve))
	defer upstream.Close()
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	srv := &server{store: repository, uploadPath: t.TempDir(), agent: appconfig.AgentConfig{Enabled: true, Protocol: "openai-responses", BaseURL: upstream.URL, Model: "flow-test", MaxOutputTokens: 256, ToolsEnabled: true, WriteTools: true}, agentClient: upstream.Client()}
	api := httptest.NewServer(newHandler(srv))
	defer api.Close()
	client := authClient(t)
	authRequest[domain.AuthSession](t, client, http.MethodPost, api.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	call := func(method, path string, input any, want int) []byte {
		t.Helper()
		var body io.Reader
		if input != nil {
			raw, _ := json.Marshal(input)
			body = strings.NewReader(string(raw))
		}
		request, _ := http.NewRequest(method, api.URL+path, body)
		request.Header.Set("X-Workspace-Key", "test-workspace")
		if input != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		if response.StatusCode != want {
			t.Fatalf("%s %s: status %d: %.300s", method, path, response.StatusCode, raw)
		}
		return raw
	}
	// Connected browsers: each holds an SSE stream that every realtime event
	// is projected for (FLOW_SCALE_SSE_CLIENTS, default 10).
	sseCtx, stopSSE := context.WithCancel(context.Background())
	defer stopSSE()
	var sseEvents atomic.Int64
	for index := 0; index < scaleEnvInt("FLOW_SCALE_SSE_CLIENTS", 10); index++ {
		request, _ := http.NewRequestWithContext(sseCtx, http.MethodGet, api.URL+"/api/realtime/events", nil)
		request.Header.Set("X-Workspace-Key", "test-workspace")
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			defer response.Body.Close()
			buffer := make([]byte, 32<<10)
			for {
				n, err := response.Body.Read(buffer)
				sseEvents.Add(int64(strings.Count(string(buffer[:n]), "id: ")))
				if err != nil {
					return
				}
			}
		}()
	}
	var loop domain.Loop
	_ = json.Unmarshal(call(http.MethodPost, "/api/loops", map[string]any{"name": "Flow 项目周更新", "instructions": "Post a weekly project update for each active project: list the projects and their in-progress issues, then post a status update on each project.", "status": "published"}, http.StatusCreated), &loop)

	for runIndex := 0; runIndex < scaleEnvInt("FLOW_SCALE_LOOP_RUNS", 1); runIndex++ {
		provider.reset()
		runtime.GC()
		var base runtime.MemStats
		runtime.ReadMemStats(&base)
		locks.reset()
		sampler := startHeapSampler(25 * time.Millisecond)
		probe := startLatencyProbe(func() { call(http.MethodGet, "/api/loops", nil, http.StatusOK) }, 200*time.Millisecond)
		begin := time.Now()
		var run domain.LoopRun
		_ = json.Unmarshal(call(http.MethodPost, "/api/loops/"+loop.ID+"/runs", nil, http.StatusAccepted), &run)
		accepted := time.Since(begin)
		deadline := time.Now().Add(15 * time.Minute)
		for run.Status == "running" && time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
			_ = json.Unmarshal(call(http.MethodGet, "/api/loops/"+loop.ID+"/runs/"+run.ID, nil, http.StatusOK), &run)
		}
		duration := time.Since(begin)
		slowest, probes := probe()
		peak := sampler()
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		mutating := 0
		for _, item := range run.ToolCalls {
			if item.Name == "save_status_update" && item.Status == "completed" {
				mutating++
			}
		}
		maxLock, mutations, progress := locks.summary()
		t.Logf("run %d: status=%s error=%q tool calls=%d status updates=%d", runIndex+1, run.Status, run.Error, len(run.ToolCalls), mutating)
		t.Logf("run %d: accepted in %s, finished in %s (provider turns %d)", runIndex+1, accepted.Round(time.Millisecond), duration.Round(time.Millisecond), provider.turns())
		t.Logf("run %d: heap before %.1f MiB; peak HeapInuse %.1f MiB (+%.1f), peak HeapAlloc %.1f MiB, peak Sys %.1f MiB, allocated %.1f MiB, GCs %d",
			runIndex+1, mib(base.HeapInuse), mib(peak.heapInuse), mib(peak.heapInuse)-mib(base.HeapInuse), mib(peak.heapAlloc), mib(peak.sys), mib(after.TotalAlloc-base.TotalAlloc), after.NumGC-base.NumGC)
		t.Logf("run %d: workspace writes %d (loop.run_progress %d), max lock hold %d ms; slowest concurrent GET /api/loops %s over %d probes; SSE events delivered so far %d",
			runIndex+1, mutations, progress, maxLock, slowest.Round(time.Millisecond), probes, sseEvents.Load())
	}
}

func mib(value uint64) float64 { return float64(value) / (1 << 20) }

type heapPeak struct{ heapInuse, heapAlloc, sys uint64 }

// startHeapSampler polls runtime.MemStats until the returned stop function is
// called and reports the highest values seen.
func startHeapSampler(interval time.Duration) func() heapPeak {
	var peak heapPeak
	var mu sync.Mutex
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			var stats runtime.MemStats
			runtime.ReadMemStats(&stats)
			mu.Lock()
			peak.heapInuse = max(peak.heapInuse, stats.HeapInuse)
			peak.heapAlloc = max(peak.heapAlloc, stats.HeapAlloc)
			peak.sys = max(peak.sys, stats.Sys)
			mu.Unlock()
			select {
			case <-done:
				return
			case <-ticker.C:
			}
		}
	}()
	return func() heapPeak {
		close(done)
		<-finished
		mu.Lock()
		defer mu.Unlock()
		return peak
	}
}

// startLatencyProbe calls probe repeatedly until stopped and reports the
// slowest call.
func startLatencyProbe(probe func(), interval time.Duration) func() (time.Duration, int) {
	var slowest atomic.Int64
	var count atomic.Int64
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for {
			began := time.Now()
			probe()
			elapsed := time.Since(began)
			count.Add(1)
			for {
				current := slowest.Load()
				if int64(elapsed) <= current || slowest.CompareAndSwap(current, int64(elapsed)) {
					break
				}
			}
			select {
			case <-done:
				return
			case <-time.After(interval):
			}
		}
	}()
	return func() (time.Duration, int) {
		close(done)
		<-finished
		return time.Duration(slowest.Load()), int(count.Load())
	}
}

// scaleLockRecorder is a slog handler that keeps the lock hold time of every
// "slow workspace mutation" record.
type scaleLockRecorder struct {
	mu        sync.Mutex
	maxLock   int64
	mutations int
	progress  int
}

func (r *scaleLockRecorder) reset() {
	r.mu.Lock()
	r.maxLock, r.mutations, r.progress = 0, 0, 0
	r.mu.Unlock()
}

func (r *scaleLockRecorder) summary() (int64, int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.maxLock, r.mutations, r.progress
}

func (r *scaleLockRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (r *scaleLockRecorder) WithAttrs([]slog.Attr) slog.Handler       { return r }
func (r *scaleLockRecorder) WithGroup(string) slog.Handler            { return r }
func (r *scaleLockRecorder) Handle(_ context.Context, record slog.Record) error {
	if record.Message != "slow workspace mutation" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mutations++
	record.Attrs(func(attr slog.Attr) bool {
		switch attr.Key {
		case "lock_held_ms":
			r.maxLock = max(r.maxLock, attr.Value.Int64())
		case "event":
			if attr.Value.String() == "loop.run_progress" {
				r.progress++
			}
		}
		return true
	})
	return nil
}

// scaleLoopProvider scripts the incident's weekly-update run: progress and
// list_projects, two list_issues pages, a project status update, the final
// summary tool and a closing answer.
type scaleLoopProvider struct {
	mu        sync.Mutex
	projectID string
	count     int
}

func (p *scaleLoopProvider) reset() {
	p.mu.Lock()
	p.count = 0
	p.mu.Unlock()
}

func (p *scaleLoopProvider) turns() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.count
}

func (p *scaleLoopProvider) serve(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
	p.mu.Lock()
	turn := p.count
	p.count++
	p.mu.Unlock()
	type call struct{ name, args string }
	var calls []call
	switch turn {
	case 0:
		calls = []call{{"report_progress", `{"title":"Gathering projects","message":"List projects, then their in-progress issues, then post updates."}`}, {"list_projects", `{"limit":50}`}}
	case 1:
		calls = []call{{"list_issues", `{"limit":100,"state":"started"}`}}
	case 2:
		calls = []call{{"list_issues", `{"limit":250}`}}
	case 3:
		calls = []call{{"save_status_update", `{"type":"project","project":"` + p.projectID + `","body":"Weekly update: work continues on the in-progress issues.","health":"onTrack"}`}}
	case 4:
		calls = []call{{loopFinishToolName, `{"status":"done","summary":"Posted one project update.","done":["Posted the weekly update on the project"],"notDone":[]}`}}
	}
	w.Header().Set("Content-Type", "text/event-stream")
	if len(calls) == 0 {
		_, _ = w.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Posted one project update.\"}\n\n" +
			"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"))
		return
	}
	var body strings.Builder
	for index, item := range calls {
		itemID := "item_" + strconv.Itoa(turn) + "_" + strconv.Itoa(index)
		quoted, _ := json.Marshal(item.args)
		body.WriteString("event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"" + itemID + "\",\"call_id\":\"call_" + itemID + "\",\"type\":\"function_call\",\"name\":\"" + item.name + "\",\"arguments\":\"\"}}\n\n")
		body.WriteString("event: response.function_call_arguments.done\ndata: {\"type\":\"response.function_call_arguments.done\",\"item_id\":\"" + itemID + "\",\"arguments\":" + string(quoted) + "}\n\n")
	}
	body.WriteString("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	_, _ = w.Write([]byte(body.String()))
}
