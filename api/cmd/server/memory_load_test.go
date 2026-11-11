//go:build loadtest

package main

// Load test for the large-workspace memory incident: two replicas on one
// database with Redis coordination, a synthetic workspace near the incident's
// metadata size, several members (private teams, a guest) and concurrent
// bootstrap, notification, visibility, issue-list and presence reads while
// another replica keeps committing changes (cross-replica reloads).
//
//	go test -tags loadtest -run TestLoadLargeWorkspace -v ./cmd/server/ -timeout 60m
//
// Knobs (env): FLOW_LOAD_CONCURRENCY (40), FLOW_LOAD_DURATION (60s),
// FLOW_LOAD_WRITE_EVERY (2s, 0 = no writes), FLOW_LOAD_SCALE (1.0),
// FLOW_LOAD_SLOW_READERS (0: workers that read responses slowly),
// FLOW_LOAD_REPORT (path for a JSON report).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func loadEnvInt(name string, fallback int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil {
		return v
	}
	return fallback
}

func loadEnvDuration(name string, fallback time.Duration) time.Duration {
	if v, err := time.ParseDuration(os.Getenv(name)); err == nil {
		return v
	}
	return fallback
}

type loadMemPeak struct {
	mu                                    sync.Mutex
	heapAlloc, heapInuse, sys, stackInuse uint64
	samples                               int
	series                                [][3]uint64 // ms, heapInuse, heapAlloc
}

func (p *loadMemPeak) sample(start time.Time) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.heapAlloc = max(p.heapAlloc, m.HeapAlloc)
	p.heapInuse = max(p.heapInuse, m.HeapInuse)
	p.sys = max(p.sys, m.Sys)
	p.stackInuse = max(p.stackInuse, m.StackInuse)
	p.samples++
	if p.samples%20 == 0 {
		p.series = append(p.series, [3]uint64{uint64(time.Since(start).Milliseconds()), m.HeapInuse >> 20, m.HeapAlloc >> 20})
	}
}

func maxRSSBytes() int64 {
	var usage syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &usage) != nil {
		return 0
	}
	if runtime.GOOS == "darwin" {
		return usage.Maxrss // bytes on macOS
	}
	return usage.Maxrss * 1024
}

type loadRoute struct {
	name   string
	method string
	path   string
	body   func() []byte
}

type loadResult struct {
	route    string
	status   int
	duration time.Duration
	err      bool
}

func percentile(values []time.Duration, p float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	slices.Sort(values)
	index := int(float64(len(values)-1) * p)
	return values[index]
}

func TestLoadLargeWorkspace(t *testing.T) {
	concurrency := loadEnvInt("FLOW_LOAD_CONCURRENCY", 40)
	duration := loadEnvDuration("FLOW_LOAD_DURATION", time.Minute)
	writeEvery := loadEnvDuration("FLOW_LOAD_WRITE_EVERY", 2*time.Second)
	slowReaders := loadEnvInt("FLOW_LOAD_SLOW_READERS", 0)
	scale := 1.0
	if v, err := strconv.ParseFloat(os.Getenv("FLOW_LOAD_SCALE"), 64); err == nil && v > 0 {
		scale = v
	}

	peers := newPeerServers(t)
	base, other := peers.first.URL, peers.second.URL
	admin := peerAdmin(t, base)
	suffix := fmt.Sprint(time.Now().UnixNano() % 100000)
	teamsURL := base + "/api/workspaces/test-workspace/teams"
	teams := []domain.Team{}
	for index, spec := range []struct {
		name    string
		private bool
	}{{"Load Public A", false}, {"Load Public B", false}, {"Load Private P", true}, {"Load Private Q", true}} {
		teams = append(teams, authRequest[domain.Team](t, admin, http.MethodPost, teamsURL, map[string]any{"name": spec.name + " " + suffix, "key": fmt.Sprintf("L%c%s", 'A'+index, suffix[len(suffix)-3:]), "private": spec.private}, "", http.StatusCreated))
	}
	teamIDs := []string{teams[0].ID, teams[1].ID, teams[2].ID, teams[3].ID}

	type member struct {
		client *http.Client
		user   domain.User
	}
	members := []member{{client: admin}}
	// Registration is rate limited per address (5 per window): four invited
	// people. Two members belong to a private team, one does not, one is a guest.
	for index := range 4 {
		role, joined := "member", []string{teams[index%2].ID}
		switch index {
		case 0:
			joined = append(joined, teams[2].ID)
		case 1:
			joined = append(joined, teams[3].ID)
		case 3:
			role, joined = "guest", []string{teams[1].ID}
		}
		client, user := joinByInvitation(t, base, admin, fmt.Sprintf("Load User %d", index), fmt.Sprintf("load-user-%d-%s@example.com", index, suffix), role, joined...)
		members = append(members, member{client: client, user: user})
	}

	issueIDs := []string{}
	for index := range 80 {
		issue := authRequest[domain.Issue](t, admin, http.MethodPost, base+"/api/issues", map[string]any{"title": fmt.Sprintf("Load issue %d", index), "teamId": teamIDs[index%len(teamIDs)]}, "test-workspace", http.StatusCreated)
		issueIDs = append(issueIDs, issue.ID)
	}

	spec := store.SyntheticMetadata{
		Projects: int(2321 * scale), ProjectBytes: 12000,
		Trash: int(210 * scale), TrashBytes: 41000,
		Documents: int(78 * scale), DocumentBytes: 64000,
		Conversations: int(400 * scale), ConversationBytes: 2000,
		TeamIDs: teamIDs,
	}
	seedStarted := time.Now()
	if err := peers.stores[1].SeedSyntheticMetadata(context.Background(), "test-workspace", spec); err != nil {
		t.Fatal(err)
	}
	// The seed went through replica 2; replica 1 learns of it like of any peer change.
	if err := peers.stores[0].ReloadWorkspace(context.Background(), "test-workspace"); err != nil {
		t.Fatal(err)
	}
	sizes, err := peers.stores[0].WorkspaceStateBytes(context.Background(), "test-workspace")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("seeded in %s: %v", time.Since(seedStarted).Round(time.Millisecond), sizes)

	routes := []loadRoute{
		{name: "bootstrap", method: http.MethodGet, path: "/api/issue-records/bootstrap"},
		{name: "notifications", method: http.MethodGet, path: "/api/notifications?limit=50"},
		{name: "visibility", method: http.MethodPost, path: "/api/issue-records/visibility", body: func() []byte {
			raw, _ := json.Marshal(map[string]any{"ids": issueIDs})
			return raw
		}},
		{name: "issues", method: http.MethodGet, path: "/api/issue-records?limit=50"},
		{name: "presence", method: http.MethodGet, path: "/api/realtime/presence"},
		// A resource route authorized through the viewer's workspace projection.
		{name: "project", method: http.MethodGet, path: "/api/projects/lt-project-00004"},
	}
	weights := []int{0, 0, 0, 1, 1, 1, 2, 2, 3, 4, 5} // bootstrap, notifications, visibility, issues, presence, project

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	t.Logf("baseline after seed+GC: HeapInuse=%dMiB HeapAlloc=%dMiB Sys=%dMiB maxRSS=%dMiB", before.HeapInuse>>20, before.HeapAlloc>>20, before.Sys>>20, maxRSSBytes()>>20)

	start := time.Now()
	peak := &loadMemPeak{}
	samplerDone := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	go func() {
		defer close(samplerDone)
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				peak.sample(start)
			case <-ctx.Done():
				peak.sample(start)
				return
			}
		}
	}()

	var inFlight, maxInFlight atomic.Int64
	results := make(chan loadResult, 1<<16)
	var wg sync.WaitGroup
	transport := &http.Transport{MaxIdleConnsPerHost: concurrency * 2}
	for worker := range concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			who := members[worker%len(members)]
			client := &http.Client{Jar: who.client.Jar, Transport: transport, Timeout: 120 * time.Second}
			slow := worker < slowReaders
			for n := 0; ctx.Err() == nil; n++ {
				route := routes[weights[(worker+n)%len(weights)]]
				var body io.Reader
				if route.body != nil {
					body = bytes.NewReader(route.body())
				}
				request, _ := http.NewRequestWithContext(context.Background(), route.method, base+route.path, body)
				request.Header.Set("X-Workspace-Key", "test-workspace")
				if route.body != nil {
					request.Header.Set("Content-Type", "application/json")
				}
				current := inFlight.Add(1)
				for {
					seen := maxInFlight.Load()
					if current <= seen || maxInFlight.CompareAndSwap(seen, current) {
						break
					}
				}
				began := time.Now()
				response, err := client.Do(request)
				result := loadResult{route: route.name, err: err != nil}
				if err == nil {
					result.status = response.StatusCode
					if slow {
						buffer := make([]byte, 32<<10)
						for {
							if _, err := response.Body.Read(buffer); err != nil {
								break
							}
							time.Sleep(20 * time.Millisecond)
						}
					} else {
						_, _ = io.Copy(io.Discard, response.Body)
					}
					response.Body.Close()
				}
				result.duration = time.Since(began)
				inFlight.Add(-1)
				results <- result
			}
		}()
	}

	writes, writeErrors := 0, 0
	if writeEvery > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ticker := time.NewTicker(writeEvery)
			defer ticker.Stop()
			for n := 0; ; n++ {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
				raw, _ := json.Marshal(map[string]any{"title": fmt.Sprintf("Load issue renamed %d", n)})
				request, _ := http.NewRequest(http.MethodPatch, other+"/api/issues/"+issueIDs[n%len(issueIDs)], bytes.NewReader(raw))
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("X-Workspace-Key", "test-workspace")
				response, err := admin.Do(request)
				writes++
				if err != nil || response.StatusCode >= 400 {
					writeErrors++
				}
				if err == nil {
					_, _ = io.Copy(io.Discard, response.Body)
					response.Body.Close()
				}
				// Every other write changes metadata (a team description) so the
				// reading replica must reload the workspace state.
				raw, _ = json.Marshal(map[string]any{"description": fmt.Sprintf("Load description %d", n)})
				request, _ = http.NewRequest(http.MethodPatch, other+"/api/workspaces/test-workspace/teams/"+teamIDs[0], bytes.NewReader(raw))
				request.Header.Set("Content-Type", "application/json")
				response, err = admin.Do(request)
				writes++
				if err != nil || response.StatusCode >= 400 {
					writeErrors++
				}
				if err == nil {
					_, _ = io.Copy(io.Discard, response.Body)
					response.Body.Close()
				}
			}
		}()
	}

	collected := map[string][]time.Duration{}
	statuses := map[string]map[int]int{}
	errorsByRoute := map[string]int{}
	collectDone := make(chan struct{})
	go func() {
		defer close(collectDone)
		for result := range results {
			collected[result.route] = append(collected[result.route], result.duration)
			if statuses[result.route] == nil {
				statuses[result.route] = map[int]int{}
			}
			statuses[result.route][result.status]++
			if result.err || result.status >= 500 {
				errorsByRoute[result.route]++
			}
		}
	}()
	wg.Wait()
	close(results)
	<-collectDone
	<-samplerDone

	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	runtime.GC()
	time.Sleep(2 * time.Second)
	runtime.GC()
	var settled runtime.MemStats
	runtime.ReadMemStats(&settled)

	report := map[string]any{
		"concurrency": concurrency, "durationSeconds": duration.Seconds(), "writeEvery": writeEvery.String(), "slowReaders": slowReaders,
		"sizes": sizes, "maxInFlight": maxInFlight.Load(),
		"peakHeapInuseMiB": peak.heapInuse >> 20, "peakHeapAllocMiB": peak.heapAlloc >> 20, "peakSysMiB": peak.sys >> 20,
		"maxRSSMiB": maxRSSBytes() >> 20, "baselineHeapInuseMiB": before.HeapInuse >> 20,
		"settledHeapInuseMiB": settled.HeapInuse >> 20, "settledHeapAllocMiB": settled.HeapAlloc >> 20,
		"totalAllocGiB": float64(after.TotalAlloc-before.TotalAlloc) / (1 << 30), "numGC": after.NumGC - before.NumGC,
		"gcPauseTotalMs": float64(after.PauseTotalNs-before.PauseTotalNs) / 1e6,
		"writes":         writes, "writeErrors": writeErrors, "heapSeriesMiB": peak.series,
	}
	routeReport := map[string]any{}
	for name, values := range collected {
		routeReport[name] = map[string]any{
			"count": len(values), "errors": errorsByRoute[name], "statuses": statuses[name],
			"p50ms": percentile(values, 0.5).Milliseconds(), "p95ms": percentile(values, 0.95).Milliseconds(), "maxms": percentile(values, 1).Milliseconds(),
		}
	}
	report["routes"] = routeReport
	raw, _ := json.MarshalIndent(report, "", "  ")
	t.Logf("load report:\n%s", raw)
	if path := os.Getenv("FLOW_LOAD_REPORT"); path != "" {
		_ = os.WriteFile(path, raw, 0o644)
	}
}

// TestLoadPerRequestAllocations measures bytes allocated per request, one
// request at a time, per route and per viewer kind (admin, member, guest).
func TestLoadPerRequestAllocations(t *testing.T) {
	peers := newPeerServers(t)
	base := peers.first.URL
	admin := peerAdmin(t, base)
	suffix := fmt.Sprint(time.Now().UnixNano() % 100000)
	teamsURL := base + "/api/workspaces/test-workspace/teams"
	public := authRequest[domain.Team](t, admin, http.MethodPost, teamsURL, map[string]any{"name": "Alloc Public " + suffix, "key": "AP" + suffix[len(suffix)-3:]}, "", http.StatusCreated)
	private := authRequest[domain.Team](t, admin, http.MethodPost, teamsURL, map[string]any{"name": "Alloc Private " + suffix, "key": "AQ" + suffix[len(suffix)-3:], "private": true}, "", http.StatusCreated)
	member, _ := joinByInvitation(t, base, admin, "Alloc Member", "alloc-member-"+suffix+"@example.com", "member", public.ID)
	guest, _ := joinByInvitation(t, base, admin, "Alloc Guest", "alloc-guest-"+suffix+"@example.com", "guest", public.ID)
	issue := authRequest[domain.Issue](t, admin, http.MethodPost, base+"/api/issues", map[string]any{"title": "Alloc issue", "teamId": public.ID}, "test-workspace", http.StatusCreated)
	if err := peers.stores[1].SeedSyntheticMetadata(context.Background(), "test-workspace", store.SyntheticMetadata{
		Projects: 2321, ProjectBytes: 12000, Trash: 210, TrashBytes: 41000, Documents: 78, DocumentBytes: 64000, Conversations: 400, ConversationBytes: 2000,
		TeamIDs: []string{public.ID, private.ID},
	}); err != nil {
		t.Fatal(err)
	}
	if err := peers.stores[0].ReloadWorkspace(context.Background(), "test-workspace"); err != nil {
		t.Fatal(err)
	}
	visibility, _ := json.Marshal(map[string]any{"ids": []string{issue.ID}})
	routes := []struct {
		name, method, path string
		body               []byte
	}{
		{"bootstrap", http.MethodGet, "/api/issue-records/bootstrap", nil},
		{"notifications", http.MethodGet, "/api/notifications?limit=50", nil},
		{"visibility", http.MethodPost, "/api/issue-records/visibility", visibility},
		{"issues", http.MethodGet, "/api/issue-records?limit=50", nil},
		{"presence", http.MethodGet, "/api/realtime/presence", nil},
	}
	results := map[string]map[string]any{}
	for _, viewer := range []struct {
		name   string
		client *http.Client
	}{{"admin", admin}, {"member", member}, {"guest", guest}} {
		for _, route := range routes {
			do := func() (int, int) {
				var body io.Reader
				if route.body != nil {
					body = bytes.NewReader(route.body)
				}
				request, _ := http.NewRequest(route.method, base+route.path, body)
				request.Header.Set("X-Workspace-Key", "test-workspace")
				if route.body != nil {
					request.Header.Set("Content-Type", "application/json")
				}
				response, err := viewer.client.Do(request)
				if err != nil {
					t.Fatal(err)
				}
				n, _ := io.Copy(io.Discard, response.Body)
				response.Body.Close()
				return response.StatusCode, int(n)
			}
			do() // warm up
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			const n = 5
			status, size := 0, 0
			for range n {
				status, size = do()
			}
			runtime.ReadMemStats(&after)
			results[viewer.name+" "+route.name] = map[string]any{"status": status, "responseKiB": size >> 10, "allocMiBPerRequest": float64(after.TotalAlloc-before.TotalAlloc) / n / (1 << 20)}
		}
	}
	raw, _ := json.MarshalIndent(results, "", "  ")
	t.Logf("per-request allocations:\n%s", raw)
	if path := os.Getenv("FLOW_ALLOC_REPORT"); path != "" {
		_ = os.WriteFile(path, raw, 0o644)
	}
}

// TestLoadBootstrapProfile issues paged bootstraps for an admin and a member
// one at a time; run with -memprofile to attribute the allocations.
func TestLoadBootstrapProfile(t *testing.T) {
	peers := newPeerServers(t)
	base := peers.first.URL
	admin := peerAdmin(t, base)
	suffix := fmt.Sprint(time.Now().UnixNano() % 100000)
	public := authRequest[domain.Team](t, admin, http.MethodPost, base+"/api/workspaces/test-workspace/teams", map[string]any{"name": "Profile Public " + suffix, "key": "PP" + suffix[len(suffix)-3:]}, "", http.StatusCreated)
	member, _ := joinByInvitation(t, base, admin, "Profile Member", "profile-member-"+suffix+"@example.com", "member", public.ID)
	if err := peers.stores[1].SeedSyntheticMetadata(context.Background(), "test-workspace", store.SyntheticMetadata{
		Projects: 2321, ProjectBytes: 12000, Trash: 210, TrashBytes: 41000, Documents: 78, DocumentBytes: 64000, Conversations: 400, ConversationBytes: 2000,
		TeamIDs: []string{public.ID},
	}); err != nil {
		t.Fatal(err)
	}
	if err := peers.stores[0].ReloadWorkspace(context.Background(), "test-workspace"); err != nil {
		t.Fatal(err)
	}
	runtime.MemProfileRate = 64 << 10
	for range 10 {
		for _, client := range []*http.Client{admin, member} {
			request, _ := http.NewRequest(http.MethodGet, base+"/api/issue-records/bootstrap", nil)
			request.Header.Set("X-Workspace-Key", "test-workspace")
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
		}
	}
}

// TestLoadUsers models users of the web client against one reading replica
// while another replica commits metadata changes: page load (bootstrap,
// notifications, issue list), a presence heartbeat every 20s, a navigation
// read now and then, and one coalesced metadata reload (0-1s spread, plus a
// visibility check) per change the writer makes. It reports memory, latency,
// errors, busy (503) answers and how long a change takes to reach every user.
//
// FLOW_LOAD_USERS (100), FLOW_LOAD_DURATION (2m), FLOW_LOAD_CHANGE_EVERY (10s).
func TestLoadUsers(t *testing.T) {
	users := loadEnvInt("FLOW_LOAD_USERS", 100)
	duration := loadEnvDuration("FLOW_LOAD_DURATION", 2*time.Minute)
	changeEvery := loadEnvDuration("FLOW_LOAD_CHANGE_EVERY", 10*time.Second)
	peers := newPeerServers(t)
	base, other := peers.first.URL, peers.second.URL
	admin := peerAdmin(t, base)
	suffix := fmt.Sprint(time.Now().UnixNano() % 100000)
	teamsURL := base + "/api/workspaces/test-workspace/teams"
	teams := []domain.Team{}
	for index, private := range []bool{false, false, true, true} {
		teams = append(teams, authRequest[domain.Team](t, admin, http.MethodPost, teamsURL, map[string]any{"name": fmt.Sprintf("Users Team %d %s", index, suffix), "key": fmt.Sprintf("U%c%s", 'A'+index, suffix[len(suffix)-3:]), "private": private}, "", http.StatusCreated))
	}
	teamIDs := []string{teams[0].ID, teams[1].ID, teams[2].ID, teams[3].ID}
	accounts := []*http.Client{admin}
	for index := range 4 {
		role, joined := "member", []string{teams[index%2].ID}
		switch index {
		case 0:
			joined = append(joined, teams[2].ID)
		case 1:
			joined = append(joined, teams[3].ID)
		case 3:
			role, joined = "guest", []string{teams[1].ID}
		}
		client, _ := joinByInvitation(t, base, admin, fmt.Sprintf("Users Person %d", index), fmt.Sprintf("users-person-%d-%s@example.com", index, suffix), role, joined...)
		accounts = append(accounts, client)
	}
	issueIDs := []string{}
	for index := range 60 {
		issue := authRequest[domain.Issue](t, admin, http.MethodPost, base+"/api/issues", map[string]any{"title": fmt.Sprintf("Users issue %d", index), "teamId": teamIDs[index%len(teamIDs)]}, "test-workspace", http.StatusCreated)
		issueIDs = append(issueIDs, issue.ID)
	}
	if err := peers.stores[1].SeedSyntheticMetadata(context.Background(), "test-workspace", store.SyntheticMetadata{
		Projects: 2321, ProjectBytes: 12000, Trash: 210, TrashBytes: 41000, Documents: 78, DocumentBytes: 64000, Conversations: 400, ConversationBytes: 2000, TeamIDs: teamIDs,
	}); err != nil {
		t.Fatal(err)
	}
	if err := peers.stores[0].ReloadWorkspace(context.Background(), "test-workspace"); err != nil {
		t.Fatal(err)
	}
	visibilityBody, _ := json.Marshal(map[string]any{"ids": issueIDs})
	presenceBody := []byte(`{"clientId":"load","route":"/test-workspace/team/x/active","active":true}`)

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	peak := &loadMemPeak{}
	samplerDone := make(chan struct{})
	go func() {
		defer close(samplerDone)
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				peak.sample(start)
			case <-ctx.Done():
				return
			}
		}
	}()

	transport := &http.Transport{MaxIdleConnsPerHost: users * 2, MaxConnsPerHost: 0}
	results := make(chan loadResult, 1<<16)
	do := func(client *http.Client, name, method, path string, body []byte) int {
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		request, _ := http.NewRequest(method, base+path, reader)
		request.Header.Set("X-Workspace-Key", "test-workspace")
		if body != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		began := time.Now()
		response, err := client.Do(request)
		result := loadResult{route: name, err: err != nil}
		if err == nil {
			result.status = response.StatusCode
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
		}
		result.duration = time.Since(began)
		results <- result
		return result.status
	}
	// Bootstrap the way the web client does: retry a busy answer after its Retry-After.
	bootstrap := func(client *http.Client) bool {
		for attempt := 1; attempt <= 5 && ctx.Err() == nil; attempt++ {
			status := do(client, "bootstrap", http.MethodGet, "/api/issue-records/bootstrap", nil)
			if status == http.StatusOK {
				return true
			}
			if status != http.StatusServiceUnavailable {
				return false
			}
			time.Sleep(time.Duration(float64(2*time.Second) * (0.75 + 0.5*float64(attempt%3)/2)))
		}
		return false
	}

	// Changes fan out to every user like realtime events do.
	type change struct {
		at time.Time
		n  int
	}
	subscribers := make([]chan change, users)
	for index := range subscribers {
		subscribers[index] = make(chan change, 64)
	}
	var freshMu sync.Mutex
	freshness := []time.Duration{}
	var wg sync.WaitGroup
	for index := range users {
		wg.Add(1)
		go func() {
			defer wg.Done()
			jar := accounts[index%len(accounts)].Jar
			client := &http.Client{Jar: jar, Transport: transport, Timeout: 2 * time.Minute}
			// Users arrive over the first 20% of the run (a morning ramp).
			select {
			case <-time.After(time.Duration(float64(duration) * 0.2 * float64(index) / float64(users))):
			case <-ctx.Done():
				return
			}
			bootstrap(client)
			do(client, "notifications", http.MethodGet, "/api/notifications?limit=50", nil)
			do(client, "issues", http.MethodGet, "/api/issue-records?limit=50", nil)
			heartbeat := time.NewTicker(20 * time.Second)
			defer heartbeat.Stop()
			navigate := time.NewTicker(time.Duration(30+index%30) * time.Second)
			defer navigate.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-heartbeat.C:
					do(client, "presence", http.MethodPost, "/api/realtime/presence", presenceBody)
				case <-navigate.C:
					do(client, "project", http.MethodGet, fmt.Sprintf("/api/projects/lt-project-%05d", index%2321), nil)
					do(client, "issues", http.MethodGet, "/api/issue-records?limit=50", nil)
				case event := <-subscribers[index]:
					// Coalesce: drop events already queued behind this one.
					for drained := false; !drained; {
						select {
						case newer := <-subscribers[index]:
							event = newer
						default:
							drained = true
						}
					}
					time.Sleep(time.Duration(int64(time.Second) * int64(index%1000) / 1000))
					ok := bootstrap(client)
					do(client, "visibility", http.MethodPost, "/api/issue-records/visibility", visibilityBody)
					if ok {
						freshMu.Lock()
						freshness = append(freshness, time.Since(event.at))
						freshMu.Unlock()
					}
					do(client, "notifications", http.MethodGet, "/api/notifications?limit=50", nil)
				}
			}
		}()
	}
	changes := 0
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(changeEvery)
		defer ticker.Stop()
		for n := 0; ; n++ {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			raw, _ := json.Marshal(map[string]any{"description": fmt.Sprintf("Users change %d", n)})
			request, _ := http.NewRequest(http.MethodPatch, other+"/api/workspaces/test-workspace/teams/"+teamIDs[0], bytes.NewReader(raw))
			request.Header.Set("Content-Type", "application/json")
			if response, err := admin.Do(request); err == nil {
				_, _ = io.Copy(io.Discard, response.Body)
				response.Body.Close()
			}
			changes++
			event := change{at: time.Now(), n: n}
			for _, subscriber := range subscribers {
				select {
				case subscriber <- event:
				default:
				}
			}
		}
	}()

	collected := map[string][]time.Duration{}
	statuses := map[string]map[int]int{}
	collectDone := make(chan struct{})
	go func() {
		defer close(collectDone)
		for result := range results {
			collected[result.route] = append(collected[result.route], result.duration)
			if statuses[result.route] == nil {
				statuses[result.route] = map[int]int{}
			}
			code := result.status
			if result.err {
				code = -1
			}
			statuses[result.route][code]++
		}
	}()
	wg.Wait()
	close(results)
	<-collectDone
	<-samplerDone
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	runtime.GC()
	time.Sleep(2 * time.Second)
	runtime.GC()
	var settled runtime.MemStats
	runtime.ReadMemStats(&settled)
	report := map[string]any{
		"users": users, "durationSeconds": duration.Seconds(), "changeEvery": changeEvery.String(), "changes": changes,
		"peakHeapInuseMiB": peak.heapInuse >> 20, "maxRSSMiB": maxRSSBytes() >> 20, "baselineHeapInuseMiB": before.HeapInuse >> 20,
		"settledHeapInuseMiB": settled.HeapInuse >> 20, "totalAllocGiB": float64(after.TotalAlloc-before.TotalAlloc) / (1 << 30),
		"numGC": after.NumGC - before.NumGC, "gcPauseTotalMs": float64(after.PauseTotalNs-before.PauseTotalNs) / 1e6,
		"freshnessP50s": percentile(freshness, 0.5).Seconds(), "freshnessP95s": percentile(freshness, 0.95).Seconds(), "freshnessMaxs": percentile(freshness, 1).Seconds(), "refreshes": len(freshness),
	}
	routeReport := map[string]any{}
	for name, values := range collected {
		routeReport[name] = map[string]any{"count": len(values), "statuses": statuses[name], "p50ms": percentile(values, 0.5).Milliseconds(), "p95ms": percentile(values, 0.95).Milliseconds(), "maxms": percentile(values, 1).Milliseconds()}
	}
	report["routes"] = routeReport
	raw, _ := json.MarshalIndent(report, "", "  ")
	t.Logf("users report:\n%s", raw)
	if path := os.Getenv("FLOW_LOAD_REPORT"); path != "" {
		_ = os.WriteFile(path, raw, 0o644)
	}
}
