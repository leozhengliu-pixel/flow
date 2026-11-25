//go:build loadtest

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"runtime/pprof"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type scaleP01Metrics struct {
	SQL                  store.ScaleP01SQL
	Commits              []store.ScaleP01Commit
	Evicted              int64
	CPU                  scaleP01CPU
	PID                  int
	HeapAlloc, HeapInuse uint64
	MaxRSS               int64
	ActiveWrites         int64
	RequestTracking      bool
}

type scaleP01MetricSource func(context.Context) (scaleP01Metrics, error)

func scaleP01LocalMetricSource(probe *store.ScaleP01Probe) scaleP01MetricSource {
	return func(context.Context) (scaleP01Metrics, error) {
		sql, commits, evicted := probe.Snapshot()
		var memory runtime.MemStats
		runtime.ReadMemStats(&memory)
		return scaleP01Metrics{SQL: sql, Commits: commits, Evicted: evicted, CPU: scaleP01CPUTime(), PID: os.Getpid(), HeapAlloc: memory.HeapAlloc, HeapInuse: memory.HeapInuse, MaxRSS: maxRSSBytes(), ActiveWrites: probe.ActiveLoadWrites(), RequestTracking: true}, nil
	}
}

func scaleP01RunLocal(t *testing.T) {
	t.Helper()
	peers := scaleP01NewPeers(t)
	fixture := scaleP01SeedFixture(t, peers)
	sources := []scaleP01MetricSource{}
	for _, repo := range peers.stores {
		probe, _ := scaleP01Probes.Load(repo)
		sources = append(sources, scaleP01LocalMetricSource(probe.(*store.ScaleP01Probe)))
	}
	scaleP01Run(t, []string{peers.first.URL, peers.second.URL}, fixture, sources, false)
}

func scaleP01Run(t *testing.T, bases []string, fixture scaleP01Fixture, sources []scaleP01MetricSource, external bool) {
	t.Helper()
	users := loadEnvInt("FLOW_LOAD_USERS", 100)
	duration := loadEnvDuration("FLOW_LOAD_DURATION", 2*time.Minute)
	changeEvery := loadEnvDuration("FLOW_LOAD_CHANGE_EVERY", 10*time.Second)
	accessEvery := loadEnvInt("FLOW_LOAD_ACCESS_EVERY", 10)
	timeout := loadEnvDuration("FLOW_LOAD_REQUEST_TIMEOUT", 2*time.Minute)
	deliveryTimeout := loadEnvDuration("FLOW_LOAD_DELIVERY_TIMEOUT", 15*time.Second)
	applyDelay := loadEnvDuration("FLOW_LOAD_APPLY_DELAY", 0)
	if users < 1 || duration <= 0 || changeEvery < 0 || accessEvery < 1 || timeout <= 0 || deliveryTimeout <= 0 || applyDelay < 0 {
		t.Fatal("invalid P01 load settings")
	}
	transport := &http.Transport{MaxIdleConnsPerHost: users * 2}
	defer transport.CloseIdleConnections()
	stats, ledger := newScaleP01RouteStats(), newScaleP01Ledger()
	streamCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	workCtx, stopWork := context.WithTimeout(streamCtx, duration)
	defer stopWork()
	identities := fixture.Identities
	clients := make([]*http.Client, users)
	expectedByKind := map[string][]bool{}
	for _, kind := range []string{"document", "comment", "team", "issue", "access"} {
		expectedByKind[kind] = make([]bool, users)
	}
	for index := range users {
		identity := identities[index%len(identities)]
		clients[index] = scaleP01Client(identity, bases, transport, timeout)
		for kind, expected := range expectedByKind {
			expected[index] = identity.ExpectedKinds[kind]
		}
	}
	admin := scaleP01Client(fixture.Admin, bases, transport, timeout)
	var requestCount, writeCount, writeFailures, streamAttempts, streamFailures atomic.Int64
	var requestID atomic.Int64
	var inFlight, maxInFlight atomic.Int64
	do := func(ctx context.Context, client *http.Client, name, method, base, path string, body []byte, retain bool) (int, []byte) {
		began := time.Now()
		request, err := http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(body))
		if err != nil {
			stats.add(name, -1, time.Since(began), err, false)
			return 0, nil
		}
		request.Header.Set("X-Workspace-Key", fixture.Workspace)
		request.Header.Set("X-Scale-P01-Request", fmt.Sprintf("%d-%d", os.Getpid(), requestID.Add(1)))
		if body != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		current := inFlight.Add(1)
		for seen := maxInFlight.Load(); current > seen && !maxInFlight.CompareAndSwap(seen, current); seen = maxInFlight.Load() {
		}
		response, err := client.Do(request)
		code := -1
		var raw []byte
		if err == nil {
			code = response.StatusCode
			if retain && code >= 200 && code < 300 {
				raw, err = io.ReadAll(response.Body)
			} else {
				_, err = io.Copy(io.Discard, response.Body)
			}
			closeErr := response.Body.Close()
			if err == nil {
				err = closeErr
			}
		}
		inFlight.Add(-1)
		requestCount.Add(1)
		var netErr net.Error
		timedOut := errors.Is(err, context.DeadlineExceeded) || errors.As(err, &netErr) && netErr.Timeout()
		stats.add(name, code, time.Since(began), err, timedOut)
		if err != nil {
			return 0, nil
		}
		return code, raw
	}
	bootstrap := func(ctx context.Context, client *http.Client, base string) bool {
		for attempt := 1; attempt <= 5 && ctx.Err() == nil; attempt++ {
			code, raw := do(ctx, client, "bootstrap", http.MethodGet, base, "/api/issue-records/bootstrap", nil, true)
			if code == http.StatusOK {
				// Apply means parsed and installed, not bytes discarded from the socket.
				var state domain.Bootstrap
				return json.Unmarshal(raw, &state) == nil && state.Workspace.ID == fixture.WorkspaceID
			}
			if code != http.StatusServiceUnavailable || !scaleP01Pause(ctx, time.Duration(float64(2*time.Second)*(0.75+0.5*float64(attempt%3)/2))) {
				return false
			}
		}
		return false
	}
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	start, cpuBefore := time.Now(), scaleP01CPUTime()
	metricsBefore := make([]scaleP01Metrics, len(sources))
	metricsAfter := make([]scaleP01Metrics, len(sources))
	apiHeapPeak := make([]uint64, len(sources))
	browserLog := scaleP01BrowserLog{path: os.Getenv("FLOW_LOAD_BROWSER_ACKS")}
	metricErrors := []string{}
	for i, source := range sources {
		value, err := source(streamCtx)
		if err != nil {
			t.Fatal("P01 metrics unavailable before run (details suppressed)")
		}
		metricsBefore[i] = value
		metricsAfter[i] = value
		apiHeapPeak[i] = value.HeapInuse
	}
	if external && len(sources) == 2 && (metricsBefore[0].PID == 0 || metricsBefore[1].PID == 0 || metricsBefore[0].PID == metricsBefore[1].PID || metricsBefore[0].PID == os.Getpid() || metricsBefore[1].PID == os.Getpid()) {
		t.Fatal("external metric sources must identify two separate API processes")
	}
	seenCommits := map[string]bool{}
	for _, value := range metricsBefore {
		for _, event := range value.Commits {
			seenCommits[event.ID] = true
		}
	}
	var lastCommit time.Time
	var metricMu sync.Mutex
	pollMetrics := func() {
		metricMu.Lock()
		defer metricMu.Unlock()
		for i, source := range sources {
			value, err := source(streamCtx)
			if err != nil {
				metricErrors = append(metricErrors, "metrics request failed")
				continue
			}
			metricsAfter[i] = value
			apiHeapPeak[i] = max(apiHeapPeak[i], value.HeapInuse)
			for _, event := range value.Commits {
				if seenCommits[event.ID] {
					continue
				}
				seenCommits[event.ID] = true
				kind := ""
				switch {
				case event.Type == "document.updated" && event.AggregateID == fixture.DocumentID:
					kind = "document"
				case event.Type == "project.commented" && event.AggregateID == fixture.ProjectID:
					kind = "comment"
				case event.Type == "team.updated" && event.AggregateID == fixture.TeamIDs[0]:
					kind = "team"
				case strings.HasPrefix(event.Type, "team_member."):
					kind = "access"
				case event.Type == "issue.updated" && slices.Contains(fixture.IssueIDs, event.AggregateID):
					kind = "issue"
				}
				if kind != "" {
					ledger.commit(event, kind, expectedByKind[kind])
					lastCommit = maxTime(lastCommit, event.At)
				}
			}
		}
		if err := browserLog.poll(ledger); err != nil {
			metricErrors = append(metricErrors, "browser apply log failed")
		}
		ledger.compact(time.Now(), deliveryTimeout)
	}
	peak := &loadMemPeak{}
	var background sync.WaitGroup
	background.Add(1)
	go func() {
		defer background.Done()
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		profiled := uint64(0)
		for tick := 0; ; tick++ {
			select {
			case <-streamCtx.Done():
				return
			case <-ticker.C:
				peak.sample(start)
				if tick%40 == 0 {
					pollMetrics()
				}
				if path := os.Getenv("FLOW_LOAD_PEAK_PROFILE"); path != "" && tick%40 == 0 {
					peak.mu.Lock()
					current := peak.heapInuse
					peak.mu.Unlock()
					if current > profiled+profiled/10 {
						profiled = current
						file, err := os.Create(path)
						if err == nil {
							err = pprof.Lookup("heap").WriteTo(file, 0)
							file.Close()
						}
						if err != nil {
							t.Errorf("peak profile: %v", err)
						}
					}
				}
			}
		}
	}()
	var eventMu sync.Mutex
	events, actions := map[string]int{}, map[string]int{}
	visibility, _ := json.Marshal(map[string]any{"ids": fixture.IssueIDs})
	var workers, streams sync.WaitGroup
	for index := range users {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if !scaleP01Pause(workCtx, time.Duration(float64(duration)*.2*float64(index)/float64(users))) {
				return
			}
			client, base := clients[index], bases[0]
			if external {
				base = bases[index%2]
			}
			bootstrap(workCtx, client, base)
			do(workCtx, client, "notifications", http.MethodGet, base, "/api/notifications?limit=50", nil, false)
			do(workCtx, client, "issues", http.MethodGet, base, "/api/issue-records?limit=50", nil, false)
			reloads := make(chan domain.RealtimeEvent, 256)
			streams.Add(2)
			go func() {
				defer streams.Done()
				for {
					select {
					case <-streamCtx.Done():
						return
					case first := <-reloads:
						if !scaleP01Pause(streamCtx, time.Duration(index%1000)*time.Millisecond) {
							return
						}
						batch := []domain.RealtimeEvent{first}
						for n := len(reloads); n > 0; n-- {
							batch = append(batch, <-reloads)
						}
						ok := bootstrap(streamCtx, client, base)
						code, _ := do(streamCtx, client, "visibility", http.MethodPost, base, "/api/issue-records/visibility", visibility, false)
						if ok && code == http.StatusOK && scaleP01Pause(streamCtx, applyDelay) {
							for _, event := range batch {
								ledger.ack(scaleP01Ack{event.ID, index, time.Now()}, "applied")
							}
						}
					}
				}
			}()
			go func() {
				defer streams.Done()
				streamClient := &http.Client{Jar: client.Jar, Transport: transport}
				appliedState := map[string]json.RawMessage{}
				scaleP01Stream(streamCtx, streamClient, base, fixture.Workspace, &streamAttempts, &streamFailures, func(event domain.RealtimeEvent) {
					ledger.ack(scaleP01Ack{event.ID, index, time.Now()}, "received")
					kind, action := loadClientAction(loadRealtimeEvent{Type: event.Type, AggregateID: event.AggregateID, Payload: event.Payload})
					if strings.HasPrefix(event.Type, "issue.") || strings.HasPrefix(event.Type, "comment.") {
						kind, action = "issue", "apply"
						if pagedIssuePermissionEvent(event.Type) {
							kind, action = "access", "reload"
						}
					}
					eventMu.Lock()
					events[event.Type]++
					actions[action]++
					eventMu.Unlock()
					if action == "apply" {
						var payload struct {
							Entity json.RawMessage `json:"entity"`
						}
						if kind == "team" && event.AggregateID != "" && json.Valid(event.Payload) {
							appliedState[event.AggregateID] = bytes.Clone(event.Payload)
							if scaleP01Pause(streamCtx, applyDelay) {
								ledger.ack(scaleP01Ack{event.ID, index, time.Now()}, "applied")
							}
						} else if json.Unmarshal(event.Payload, &payload) != nil || len(payload.Entity) == 0 || string(payload.Entity) == "null" {
							action = "reload"
						} else {
							var entity map[string]any
							if json.Unmarshal(payload.Entity, &entity) == nil && entity["id"] == event.AggregateID && scaleP01Pause(streamCtx, applyDelay) {
								appliedState[event.AggregateID] = bytes.Clone(payload.Entity)
								ledger.ack(scaleP01Ack{event.ID, index, time.Now()}, "applied")
							}
						}
					}
					if action == "reload" && kind != "" {
						select {
						case reloads <- event:
						default: // The missing apply stays in the denominator.
						}
					}
				})
			}()
			heartbeat := time.NewTicker(20 * time.Second)
			navigate := time.NewTicker(time.Duration(30+index%30) * time.Second)
			defer heartbeat.Stop()
			defer navigate.Stop()
			for {
				select {
				case <-workCtx.Done():
					return
				case <-heartbeat.C:
					body, _ := json.Marshal(map[string]any{"clientId": fmt.Sprintf("load-%d", index), "route": "/" + fixture.Workspace + "/team/x/active", "active": true})
					do(workCtx, client, "presence", http.MethodPost, base, "/api/realtime/presence", body, false)
				case <-navigate.C:
					countScale := fixture.Spec.CountScale
					if countScale == 0 {
						countScale = 1
					}
					do(workCtx, client, "project", http.MethodGet, base, fmt.Sprintf("/api/projects/lt-project-%05d", index%max(1, int(2321*countScale))), nil, false)
					do(workCtx, client, "issues", http.MethodGet, base, "/api/issue-records?limit=50", nil, false)
				}
			}
		}()
	}
	var writeIndex atomic.Int64
	var accessMu sync.Mutex
	joined := true // principal zero starts in the first private team.
	write := func(issueOnly bool) {
		n := int(writeIndex.Add(1)) - 1
		kind := []string{"document", "comment", "team"}[n%3]
		if issueOnly {
			kind = "issue"
		} else if n%accessEvery == accessEvery-1 && len(identities) > 1 {
			kind = "access"
		}
		method, path := http.MethodPatch, ""
		var input any
		switch kind {
		case "document":
			path, input = "/api/documents/"+fixture.DocumentID, map[string]any{"content": fmt.Sprintf("P01 change %d\n%s", n, strings.Repeat("Body text. ", 200))}
		case "comment":
			method, path, input = http.MethodPost, "/api/projects/"+fixture.ProjectID+"/comments", map[string]any{"body": fmt.Sprintf("P01 comment %d", n)}
		case "team":
			path, input = "/api/workspaces/"+fixture.Workspace+"/teams/"+fixture.TeamIDs[0], map[string]any{"name": fmt.Sprintf("P01 team %d", n)}
		case "issue":
			path, input = "/api/issue-records/"+fixture.IssueIDs[0], map[string]any{"title": fmt.Sprintf("P01 issue change %d", n)}
		case "access":
			accessMu.Lock()
			defer accessMu.Unlock()
			path = "/api/teams/" + fixture.TeamIDs[2] + "/members/" + identities[1].ID
			method, input = http.MethodPut, map[string]any{"role": "member"}
			if joined {
				method, input = http.MethodDelete, nil
			}
			joined = !joined
		}
		raw, _ := json.Marshal(input)
		code, _ := do(workCtx, admin, "write-"+kind, method, bases[1], path, raw, false)
		writeCount.Add(1)
		if code < 200 || code >= 300 {
			writeFailures.Add(1)
		}
		pollMetrics()
	}
	// Open-loop schedules retain overload in the denominator. A full generator
	// worker budget records a failed scheduled operation instead of blocking.
	schedule := func(every time.Duration, name string, operation func()) {
		if every <= 0 {
			return
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			ticker := time.NewTicker(every)
			defer ticker.Stop()
			budget := make(chan struct{}, 64)
			var pending sync.WaitGroup
			defer pending.Wait()
			for {
				select {
				case <-workCtx.Done():
					return
				case <-ticker.C:
					select {
					case budget <- struct{}{}:
						pending.Add(1)
						go func() { defer pending.Done(); defer func() { <-budget }(); operation() }()
					default:
						requestCount.Add(1)
						if strings.Contains(name, "write") {
							writeCount.Add(1)
							writeFailures.Add(1)
						}
						stats.add(name, -2, 0, errors.New("generator saturated"), false)
					}
				}
			}
		}()
	}
	readRPS, issueRPS, metadataRPS := loadEnvInt("FLOW_LOAD_READ_RPS", 0), loadEnvInt("FLOW_LOAD_ISSUE_WRITE_RPS", 0), loadEnvInt("FLOW_LOAD_METADATA_WRITE_RPS", 0)
	if readRPS < 0 || issueRPS < 0 || metadataRPS < 0 {
		t.Fatal("RPS settings cannot be negative")
	}
	if metadataRPS == 0 {
		schedule(changeEvery, "write-scheduled", func() { write(false) })
	}
	if metadataRPS > 0 {
		schedule(time.Second/time.Duration(metadataRPS), "write-scheduled", func() { write(false) })
	}
	if issueRPS > 0 {
		schedule(time.Second/time.Duration(issueRPS), "issue-write-scheduled", func() { write(true) })
	}
	var readIndex atomic.Int64
	if readRPS > 0 {
		schedule(time.Second/time.Duration(readRPS), "read-scheduled", func() {
			i := int(readIndex.Add(1)) - 1
			do(workCtx, clients[i%users], "scheduled-read", http.MethodGet, bases[i%2], "/api/issue-records?limit=50", nil, false)
		})
	}
	workers.Wait()
	pollMetrics()
	// A client deadline does not prove the API rolled back. Observe late
	// commits while the server finishes its already admitted write handlers.
	drainDeadline := time.Now().Add(timeout + deliveryTimeout)
	outstandingWrites := int64(0)
	for {
		metricMu.Lock()
		outstandingWrites = 0
		tracking := len(sources) > 0
		for _, value := range metricsAfter {
			outstandingWrites += value.ActiveWrites
			tracking = tracking && value.RequestTracking
		}
		ready := outstandingWrites == 0 && (lastCommit.IsZero() || time.Since(lastCommit) >= deliveryTimeout)
		if !tracking && writeFailures.Load() > 0 {
			metricErrors = append(metricErrors, "write outcomes cannot be confirmed without server request tracking")
			ready = true
		}
		metricMu.Unlock()
		if ready {
			break
		}
		if time.Now().After(drainDeadline) {
			metricMu.Lock()
			metricErrors = append(metricErrors, "server write handlers did not drain before the observation deadline")
			metricMu.Unlock()
			break
		}
		if !scaleP01Pause(streamCtx, 100*time.Millisecond) {
			break
		}
		pollMetrics()
	}
	pollMetrics()
	cancel()
	streams.Wait()
	background.Wait()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	cpuAfter, ended := scaleP01CPUTime(), time.Now()
	runtime.GC()
	var settled runtime.MemStats
	runtime.ReadMemStats(&settled)
	apply := ledger.report(ended, deliveryTimeout, "applied")
	unaccounted := max(0, int(writeCount.Load()-writeFailures.Load())-apply["changes"].(int))
	report := map[string]any{
		"users": users, "uniqueUsers": min(users, len(identities)), "configuredPrincipals": len(identities), "durationSeconds": duration.Seconds(), "elapsedSeconds": ended.Sub(start).Seconds(), "changeEvery": changeEvery.String(), "changes": apply["changes"],
		"mode": "in-process-two-replica", "clientMode": "simulated-go-json-apply", "databaseDriver": "sqlite", "permissionMode": os.Getenv("FLOW_LOAD_PERMISSION_MODE"),
		"metadataCountScale": fixture.Spec.CountScale, "metadataBodyScale": fixture.Spec.BodyScale, "sizes": fixture.Sizes,
		"peakHeapInuseMiB": peak.heapInuse >> 20, "maxRSSMiB": maxRSSBytes() >> 20, "baselineHeapInuseMiB": before.HeapInuse >> 20, "settledHeapInuseMiB": settled.HeapInuse >> 20,
		"totalAllocGiB": float64(after.TotalAlloc-before.TotalAlloc) / (1 << 30), "numGC": after.NumGC - before.NumGC, "gcPauseTotalMs": float64(after.PauseTotalNs-before.PauseTotalNs) / 1e6, "maxInFlight": maxInFlight.Load(),
		"freshnessP50s": apply["p50s"], "freshnessP95s": apply["p95s"], "freshnessMaxs": apply["maxs"], "refreshes": apply["deliveries"], "freshnessByKind": apply["byKind"],
		"commitToClientApply": apply, "commitToReceive": ledger.report(ended, deliveryTimeout, "received"), "commitToBrowserApply": ledger.report(ended, deliveryTimeout, "browser"),
		"browserApplySource":     "unavailable unless FLOW_LOAD_BROWSER_ACKS supplies instrumented browser apply acknowledgements; receiving an SSE event is insufficient",
		"commitMetricsAvailable": len(sources) > 0, "browserMetricsAvailable": browserLog.path != "", "metricsComplete": len(sources) > 0 && len(metricErrors) == 0 && unaccounted == 0, "unaccountedSuccessfulWrites": unaccounted, "apiProcessIsolationVerified": external && len(sources) == 2,
		"outstandingServerWrites": outstandingWrites,
		"writes":                  writeCount.Load(), "writeErrors": writeFailures.Load(), "events": events, "clientActions": actions, "routes": stats.report(), "streamAttempts": streamAttempts.Load(), "streamErrors": streamFailures.Load(),
		"processCPU": scaleP01CPUReport(cpuBefore, cpuAfter, ended.Sub(start), users), "memoryScope": "generator and both APIs in the same process", "metricErrors": metricErrors,
	}
	if driver := os.Getenv("FLOW_TEST_DATABASE_DRIVER"); driver != "" {
		report["databaseDriver"] = driver
	}
	if external {
		report["mode"], report["memoryScope"] = "external-two-replica", "load generator only; API metrics are separate"
	}
	if len(sources) == 0 {
		report["sql"], report["commitClock"] = nil, "unavailable: external APIs have no P01 metric source; no commit latency can be claimed"
	} else {
		sqlBefore, sqlAfter := store.ScaleP01SQL{}, store.ScaleP01SQL{}
		replicas := []any{}
		for i := range sources {
			a, b := metricsBefore[i], metricsAfter[i]
			sqlBefore.Statements += a.SQL.Statements
			sqlBefore.Reads += a.SQL.Reads
			sqlBefore.Writes += a.SQL.Writes
			sqlBefore.Errors += a.SQL.Errors
			sqlBefore.Commits += a.SQL.Commits
			sqlBefore.ExecuteSeconds += a.SQL.ExecuteSeconds
			sqlAfter.Statements += b.SQL.Statements
			sqlAfter.Reads += b.SQL.Reads
			sqlAfter.Writes += b.SQL.Writes
			sqlAfter.Errors += b.SQL.Errors
			sqlAfter.Commits += b.SQL.Commits
			sqlAfter.ExecuteSeconds += b.SQL.ExecuteSeconds
			for bucket := range a.SQL.ExecutionBuckets {
				sqlBefore.ExecutionBuckets[bucket] += a.SQL.ExecutionBuckets[bucket]
				sqlAfter.ExecutionBuckets[bucket] += b.SQL.ExecutionBuckets[bucket]
			}
			replicas = append(replicas, map[string]any{"pid": b.PID, "sql": scaleP01SQLDelta(a.SQL, b.SQL, int(requestCount.Load())), "cpu": scaleP01CPUReport(a.CPU, b.CPU, ended.Sub(start), users), "commitRingEvictions": b.Evicted - a.Evicted, "baselineHeapInuseMiB": a.HeapInuse >> 20, "sampledPeakHeapInuseMiB": apiHeapPeak[i] >> 20, "finalHeapInuseMiB": b.HeapInuse >> 20, "maxRSSMiB": b.MaxRSS >> 20})
		}
		report["sql"], report["replicaMetrics"], report["commitClock"] = scaleP01SQLDelta(sqlBefore, sqlAfter, int(requestCount.Load())), replicas, "driver commit return, before sequencing and publication; same-host wall clock"
	}
	scaleP01SaveReport(t, report)
}

func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

func scaleP01Pause(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(max(duration, 0))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func scaleP01Stream(ctx context.Context, client *http.Client, base, workspace string, attempts, failures *atomic.Int64, handle func(domain.RealtimeEvent)) {
	cursor := ""
	for ctx.Err() == nil {
		attempts.Add(1)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/realtime/events", nil)
		req.Header.Set("X-Workspace-Key", workspace)
		if cursor != "" {
			req.Header.Set("Last-Event-ID", cursor)
		}
		response, err := client.Do(req)
		if err == nil {
			if response.StatusCode == http.StatusOK {
				scanner := bufio.NewScanner(response.Body)
				scanner.Buffer(make([]byte, 16<<10), 16<<20)
				for scanner.Scan() {
					line := scanner.Bytes()
					if !bytes.HasPrefix(line, []byte("data: ")) {
						continue
					}
					var event domain.RealtimeEvent
					if json.Unmarshal(line[6:], &event) != nil {
						failures.Add(1)
						continue
					}
					if event.Type == "" || event.Type == "connected" || event.Type == "presence.updated" {
						continue
					}
					handle(event)
					if event.ID != "" {
						cursor = event.ID
					}
				}
				if ctx.Err() == nil {
					failures.Add(1)
				}
			} else {
				failures.Add(1)
			}
			response.Body.Close()
		} else if ctx.Err() == nil {
			failures.Add(1)
		}
		if !scaleP01Pause(ctx, time.Second) {
			return
		}
	}
}
