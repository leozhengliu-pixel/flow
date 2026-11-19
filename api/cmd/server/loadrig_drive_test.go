//go:build loadtest

package main

// TestLoadRigDrive drives a running Flow topology (one or two replicas) with
// a mixed workload and reports latency, throughput and errors per route
// class and per operation, plus per-replica resources and the bounded
// metadata counters from each replica's load-rig metrics listener
// (loadrig_metrics.go). scripts/loadrig/loadrig.sh runs it; see its help for
// the variables. Reports: FLOW_LOADRIG_OUT/<label>.json and <label>.md.
//
// TestLoadRigCompare puts two reports side by side (flag off vs on).

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// loadRigOp is one kind of request of the mix. Composite operations (create
// then delete) record each request under its own name.
type loadRigOp struct {
	name   string
	weight float64
	admin  bool // runs as the workspace admin (settings, admin, deletes)
	write  bool // members only (guests read)
	run    func(ctx context.Context, d *loadRigDriver, vu *loadRigVU)
}

// The class and route pattern of every recorded request name; the pattern
// names the route in the server's HOT/COLD table (scale_p21_route_temperature.go).
var loadRigRequests = map[string][2]string{
	"issue.list":            {"hot-read", "GET /api/issue-records"},
	"issue.detail":          {"hot-read", "GET /api/issue-records/{id}"},
	"issue.history":         {"hot-read", "GET /api/issue-records/{id}/history"},
	"issue.groups":          {"hot-read", "GET /api/issue-records/groups"},
	"notifications":         {"hot-read", "GET /api/notifications"},
	"project.list":          {"hot-read", "GET /api/projects"},
	"project.detail":        {"hot-read", "GET /api/projects/{id}"},
	"document.detail":       {"hot-read", "GET /api/documents/{id}"},
	"account.settings":      {"hot-read", "GET /api/account/settings"},
	"issue.update":          {"hot-write", "PATCH /api/issue-records/{id}"},
	"issue.comment":         {"hot-write", "POST /api/issue-records/{id}/comments"},
	"issue.create":          {"hot-write", "POST /api/issue-records"},
	"document.update":       {"hot-write", "PATCH /api/documents/{id}"},
	"search":                {"search", "GET /api/search"},
	"sync.changes":          {"sync", "GET /api/sync/changes"},
	"page.bootstrap":        {"page-load", "GET /api/issue-records/bootstrap"},
	"apikey.issue.list":     {"api-key", "GET /api/issue-records"},
	"team.settings":         {"cold-read", "GET /api/teams/{id}/settings"},
	"admin.api-keys":        {"cold-read", "GET /api/api-keys"},
	"admin.webhooks":        {"cold-read", "GET /api/webhooks"},
	"admin.oauth-apps":      {"cold-read", "GET /api/oauth-applications"},
	"loop.settings":         {"cold-read", "GET /api/workspace/loop-settings"},
	"loop.list":             {"cold-read", "GET /api/loops"},
	"workflow.runs":         {"cold-read", "GET /api/workflow-runs"},
	"project.update":        {"cold-write", "PATCH /api/projects/{id}"},
	"view.create":           {"cold-write", "POST /api/views"},
	"view.delete":           {"cold-write", "DELETE /api/views/{id}"},
	"label.create":          {"cold-write", "POST /api/labels"},
	"label.delete":          {"cold-write", "DELETE /api/labels/{id}"},
	"apikey.create":         {"cold-write", "POST /api/api-keys"},
	"apikey.revoke":         {"cold-write", "DELETE /api/api-keys/{id}"},
	"document.create":       {"cold-write", "POST /api/documents"},
	"document.delete":       {"cold-write", "DELETE /api/documents/{id}"},
	"issue.delete.create":   {"hot-write", "POST /api/issue-records"},
	"issue.delete":          {"cold-write", "DELETE /api/issue-records/{id}"},
	"loop.settings.update":  {"cold-write", "PATCH /api/workspace/loop-settings"},
	"agent.loop-run.start":  {"agent", "POST /api/loops/{id}/runs"},
	"agent.loop-run.poll":   {"agent", "GET /api/loops/{id}/runs/{runId}"},
	"agent.loop-run.finish": {"agent", "(run duration)"},
	"agent.chat":            {"agent", "POST /api/agent/chat"},
}

func loadRigOps() []loadRigOp {
	issue := func(vu *loadRigVU) loadRigIssueRef { return vu.issues[vu.random.IntN(len(vu.issues))] }
	return []loadRigOp{
		{name: "issue.list", weight: 16, run: func(ctx context.Context, d *loadRigDriver, vu *loadRigVU) {
			team := vu.identity.TeamIDs[vu.random.IntN(len(vu.identity.TeamIDs))]
			d.request(ctx, vu, vu.client, "issue.list", http.MethodGet, "/api/issue-records?projection=list&limit=50&teamId="+url.QueryEscape(team), nil, nil)
		}},
		{name: "issue.detail", weight: 16, run: func(ctx context.Context, d *loadRigDriver, vu *loadRigVU) {
			d.request(ctx, vu, vu.client, "issue.detail", http.MethodGet, "/api/issue-records/"+issue(vu).ID, nil, nil)
		}},
		{name: "issue.history", weight: 4, run: func(ctx context.Context, d *loadRigDriver, vu *loadRigVU) {
			d.request(ctx, vu, vu.client, "issue.history", http.MethodGet, "/api/issue-records/"+issue(vu).ID+"/history", nil, nil)
		}},
		{name: "issue.groups", weight: 2, run: func(ctx context.Context, d *loadRigDriver, vu *loadRigVU) {
			d.request(ctx, vu, vu.client, "issue.groups", http.MethodGet, "/api/issue-records/groups?groupBy=status", nil, nil)
		}},
		{name: "notifications", weight: 4, run: func(ctx context.Context, d *loadRigDriver, vu *loadRigVU) {
			d.request(ctx, vu, vu.client, "notifications", http.MethodGet, "/api/notifications", nil, nil)
		}},
		{name: "project.list", weight: 2, run: func(ctx context.Context, d *loadRigDriver, vu *loadRigVU) {
			d.request(ctx, vu, vu.client, "project.list", http.MethodGet, "/api/projects", nil, nil)
		}},
		{name: "project.detail", weight: 2, run: func(ctx context.Context, d *loadRigDriver, vu *loadRigVU) {
			d.request(ctx, vu, vu.client, "project.detail", http.MethodGet, "/api/projects/"+loadRigPickRef(vu, vu.projects), nil, nil)
		}},
		{name: "document.detail", weight: 2, run: func(ctx context.Context, d *loadRigDriver, vu *loadRigVU) {
			d.request(ctx, vu, vu.client, "document.detail", http.MethodGet, "/api/documents/"+loadRigPickRef(vu, vu.documents), nil, nil)
		}},
		{name: "account.settings", weight: 1, run: func(ctx context.Context, d *loadRigDriver, vu *loadRigVU) {
			d.request(ctx, vu, vu.client, "account.settings", http.MethodGet, "/api/account/settings", nil, nil)
		}},
		{name: "issue.update", weight: 8, write: true, run: func(ctx context.Context, d *loadRigDriver, vu *loadRigVU) {
			ref := issue(vu)
			d.sent.Store(ref.ID, time.Now().UnixNano())
			d.request(ctx, vu, vu.client, "issue.update", http.MethodPatch, "/api/issue-records/"+ref.ID, map[string]any{"priority": 1 + vu.random.IntN(4)}, nil)
		}},
		{name: "issue.comment", weight: 3, write: true, run: func(ctx context.Context, d *loadRigDriver, vu *loadRigVU) {
			d.request(ctx, vu, vu.client, "issue.comment", http.MethodPost, "/api/issue-records/"+issue(vu).ID+"/comments", map[string]any{"body": fmt.Sprintf("Load rig note %d", vu.random.IntN(1e6))}, nil)
		}},
		{name: "issue.create", weight: 1, write: true, run: func(ctx context.Context, d *loadRigDriver, vu *loadRigVU) {
			team := vu.identity.TeamIDs[vu.random.IntN(len(vu.identity.TeamIDs))]
			d.request(ctx, vu, vu.client, "issue.create", http.MethodPost, "/api/issue-records", map[string]any{"title": fmt.Sprintf("Load rig issue %d", vu.random.IntN(1e6)), "teamId": team, "description": loadRigFiller("New", 800)}, nil)
		}},
		{name: "document.update", weight: 1, write: true, run: func(ctx context.Context, d *loadRigDriver, vu *loadRigVU) {
			d.request(ctx, vu, vu.client, "document.update", http.MethodPatch, "/api/documents/"+loadRigPickRef(vu, vu.documents), map[string]any{"title": fmt.Sprintf("Load runbook (edit %d)", vu.random.IntN(1e6))}, nil)
		}},
		{name: "search", weight: 4, run: func(ctx context.Context, d *loadRigDriver, vu *loadRigVU) {
			d.request(ctx, vu, vu.client, "search", http.MethodGet, "/api/search?q="+url.QueryEscape(d.pick(vu, d.fixture.Words)), nil, nil)
		}},
		{name: "sync.changes", weight: 5, run: func(ctx context.Context, d *loadRigDriver, vu *loadRigVU) {
			var page struct {
				Next    string `json:"next"`
				HasMore bool   `json:"hasMore"`
			}
			if d.request(ctx, vu, vu.client, "sync.changes", http.MethodGet, "/api/sync/changes?limit=100&maxBytes=262144&after="+cmpOrString(vu.syncCursor, "0"), nil, &page) && page.Next != "" {
				vu.syncCursor = page.Next
			}
		}},
		{name: "page.bootstrap", weight: 0.5, run: func(ctx context.Context, d *loadRigDriver, vu *loadRigVU) {
			d.request(ctx, vu, vu.client, "page.bootstrap", http.MethodGet, "/api/issue-records/bootstrap", nil, nil)
		}},
		{name: "apikey.issue.list", weight: 3, run: func(ctx context.Context, d *loadRigDriver, vu *loadRigVU) {
			if len(d.fixture.APIKeys) == 0 {
				return
			}
			key := d.fixture.APIKeys[vu.random.IntN(len(d.fixture.APIKeys))]
			d.requestWith(ctx, vu, d.keyClient, "apikey.issue.list", http.MethodGet, "/api/issue-records?projection=list&limit=50", nil, nil, key.Secret)
		}},
		{name: "team.settings", weight: 1, run: func(ctx context.Context, d *loadRigDriver, vu *loadRigVU) {
			team := vu.identity.TeamIDs[vu.random.IntN(len(vu.identity.TeamIDs))]
			d.request(ctx, vu, vu.client, "team.settings", http.MethodGet, "/api/teams/"+team+"/settings", nil, nil)
		}},
		{name: "admin.api-keys", weight: 0.5, admin: true, run: func(ctx context.Context, d *loadRigDriver, vu *loadRigVU) {
			d.request(ctx, vu, d.admin, "admin.api-keys", http.MethodGet, "/api/api-keys", nil, nil)
		}},
		{name: "admin.webhooks", weight: 0.5, admin: true, run: func(ctx context.Context, d *loadRigDriver, vu *loadRigVU) {
			d.request(ctx, vu, d.admin, "admin.webhooks", http.MethodGet, "/api/webhooks", nil, nil)
		}},
		{name: "admin.oauth-apps", weight: 0.5, admin: true, run: func(ctx context.Context, d *loadRigDriver, vu *loadRigVU) {
			d.request(ctx, vu, d.admin, "admin.oauth-apps", http.MethodGet, "/api/oauth-applications", nil, nil)
		}},
		{name: "loop.settings", weight: 0.5, admin: true, run: func(ctx context.Context, d *loadRigDriver, vu *loadRigVU) {
			d.request(ctx, vu, d.admin, "loop.settings", http.MethodGet, "/api/workspace/loop-settings", nil, nil)
		}},
		{name: "loop.list", weight: 1, run: func(ctx context.Context, d *loadRigDriver, vu *loadRigVU) {
			d.request(ctx, vu, vu.client, "loop.list", http.MethodGet, "/api/loops", nil, nil)
		}},
		{name: "workflow.runs", weight: 0.5, admin: true, run: func(ctx context.Context, d *loadRigDriver, vu *loadRigVU) {
			d.request(ctx, vu, d.admin, "workflow.runs", http.MethodGet, "/api/workflow-runs", nil, nil)
		}},
		{name: "project.update", weight: 1, write: true, run: func(ctx context.Context, d *loadRigDriver, vu *loadRigVU) {
			d.request(ctx, vu, vu.client, "project.update", http.MethodPatch, "/api/projects/"+loadRigPickRef(vu, vu.projects), map[string]any{"summary": fmt.Sprintf("Load rig summary %d", vu.random.IntN(1e6))}, nil)
		}},
		{name: "view.create", weight: 0.3, admin: true, run: func(ctx context.Context, d *loadRigDriver, vu *loadRigVU) {
			var view domain.SavedView
			if d.request(ctx, vu, d.admin, "view.create", http.MethodPost, "/api/views", map[string]any{"name": fmt.Sprintf("Load rig view %d", vu.random.IntN(1e6)), "resource": "issues", "scope": "workspace"}, &view) && view.ID != "" {
				d.request(ctx, vu, d.admin, "view.delete", http.MethodDelete, "/api/views/"+view.ID, nil, nil)
			}
		}},
		{name: "label.create", weight: 0.3, admin: true, run: func(ctx context.Context, d *loadRigDriver, vu *loadRigVU) {
			var label domain.IssueLabel
			if d.request(ctx, vu, d.admin, "label.create", http.MethodPost, "/api/labels", map[string]any{"name": fmt.Sprintf("Load rig label %d", vu.random.IntN(1e9)), "color": "#4cb782"}, &label) && label.ID != "" {
				d.request(ctx, vu, d.admin, "label.delete", http.MethodDelete, "/api/labels/"+label.ID, nil, nil)
			}
		}},
		{name: "apikey.create", weight: 0.2, admin: true, run: func(ctx context.Context, d *loadRigDriver, vu *loadRigVU) {
			var key struct {
				ID  string `json:"id"`
				Key struct {
					ID string `json:"id"`
				} `json:"key"`
			}
			if d.request(ctx, vu, d.admin, "apikey.create", http.MethodPost, "/api/api-keys", map[string]any{"name": fmt.Sprintf("Load rig key %d", vu.random.IntN(1e6)), "scopes": []string{"read"}}, &key) {
				if id := cmpOrString(key.Key.ID, key.ID); id != "" {
					d.request(ctx, vu, d.admin, "apikey.revoke", http.MethodDelete, "/api/api-keys/"+id, nil, nil)
				}
			}
		}},
		{name: "document.create", weight: 0.3, admin: true, run: func(ctx context.Context, d *loadRigDriver, vu *loadRigVU) {
			var document domain.Document
			team := d.fixture.Teams[vu.random.IntN(len(d.fixture.Teams))].ID
			if d.request(ctx, vu, d.admin, "document.create", http.MethodPost, "/api/documents", map[string]any{"title": "Load rig scratch", "content": loadRigFiller("Scratch", 16<<10), "teamIds": []string{team}}, &document) && document.ID != "" {
				d.request(ctx, vu, d.admin, "document.delete", http.MethodDelete, "/api/documents/"+document.ID, nil, nil)
			}
		}},
		{name: "issue.delete", weight: 0.3, admin: true, run: func(ctx context.Context, d *loadRigDriver, vu *loadRigVU) {
			var created domain.Issue
			team := d.fixture.Teams[vu.random.IntN(len(d.fixture.Teams))].ID
			if d.request(ctx, vu, d.admin, "issue.delete.create", http.MethodPost, "/api/issue-records", map[string]any{"title": "Load rig short-lived issue", "teamId": team}, &created) && created.ID != "" {
				d.request(ctx, vu, d.admin, "issue.delete", http.MethodDelete, "/api/issue-records/"+created.ID, nil, nil)
			}
		}},
		{name: "loop.settings.update", weight: 0.1, admin: true, run: func(ctx context.Context, d *loadRigDriver, vu *loadRigVU) {
			d.request(ctx, vu, d.admin, "loop.settings.update", http.MethodPatch, "/api/workspace/loop-settings", map[string]any{"maxConcurrentRuns": 4}, nil)
		}},
	}
}

func loadRigPickRef(vu *loadRigVU, refs []loadRigRef) string {
	if len(refs) == 0 {
		return "missing"
	}
	return refs[vu.random.IntN(len(refs))].ID
}

func loadRigVisibleRefs(identity loadRigIdentity, refs []loadRigRef) []loadRigRef {
	result := []loadRigRef{}
	for _, ref := range refs {
		if slices.Contains(identity.TeamIDs, ref.TeamID) {
			result = append(result, ref)
		}
	}
	if len(result) == 0 {
		return refs
	}
	return result
}

func cmpOrString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

type loadRigVU struct {
	index      int
	identity   loadRigIdentity
	client     *http.Client
	base       string
	issues     []loadRigIssueRef
	projects   []loadRigRef
	documents  []loadRigRef
	random     *rand.Rand
	syncCursor string
	member     bool
}

type loadRigOpStats struct {
	mu        sync.Mutex
	count     int64
	codes     map[int]int64
	busy      map[string]int64
	network   int64
	timeouts  int64
	samples   []float64
	seen      int64
	examples  []string
	totalMs   float64
	maxMs     float64
	firstSeen time.Time
}

const loadRigReservoir = 100000

func (s *loadRigOpStats) add(code int, elapsed time.Duration, cause string, network, timeout bool, example string, random *rand.Rand) {
	ms := float64(elapsed) / float64(time.Millisecond)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.count++
	if s.codes == nil {
		s.codes, s.busy = map[int]int64{}, map[string]int64{}
	}
	switch {
	case timeout:
		s.timeouts++
	case network:
		s.network++
	default:
		s.codes[code]++
	}
	if cause != "" {
		s.busy[cause]++
	}
	if example != "" && len(s.examples) < 3 && !slices.Contains(s.examples, example) {
		s.examples = append(s.examples, example)
	}
	s.totalMs += ms
	s.maxMs = max(s.maxMs, ms)
	s.seen++
	if len(s.samples) < loadRigReservoir {
		s.samples = append(s.samples, ms)
	} else if j := random.Int64N(s.seen); j < loadRigReservoir {
		s.samples[j] = ms
	}
}

type loadRigDriver struct {
	fixture    loadRigFixture
	bases      []string
	admin      *http.Client
	keyClient  *http.Client
	recording  atomic.Bool
	startAt    atomic.Int64 // UnixNano of the measured window's start and end
	endAt      atomic.Int64
	statsMu    sync.Mutex
	stats      map[string]*loadRigOpStats
	sent       sync.Map // issue id -> UnixNano of the last write sent
	lagMu      sync.Mutex
	lags       []float64
	events     atomic.Int64
	eventTypes sync.Map
	streamsUp  atomic.Int64
	streamFail atomic.Int64
	streamConn atomic.Int64
	agentRuns  sync.Map // final status -> *atomic.Int64
	adminBase  atomic.Int64
}

func (d *loadRigDriver) pick(vu *loadRigVU, values []string) string {
	if len(values) == 0 {
		return "missing"
	}
	return values[vu.random.IntN(len(values))]
}

func (d *loadRigDriver) opStats(name string) *loadRigOpStats {
	d.statsMu.Lock()
	defer d.statsMu.Unlock()
	stats, ok := d.stats[name]
	if !ok {
		stats = &loadRigOpStats{firstSeen: time.Now()}
		d.stats[name] = stats
	}
	return stats
}

func (d *loadRigDriver) request(ctx context.Context, vu *loadRigVU, client *http.Client, name, method, path string, input, out any) bool {
	return d.requestWith(ctx, vu, client, name, method, path, input, out, "")
}

// requestWith sends one request to the VU's replica (admin requests rotate
// over the replicas) and records it. bearer is an API key secret.
func (d *loadRigDriver) requestWith(ctx context.Context, vu *loadRigVU, client *http.Client, name, method, path string, input, out any, bearer string) bool {
	var body io.Reader
	if input != nil {
		raw, _ := json.Marshal(input)
		body = bytes.NewReader(raw)
	}
	base := vu.base
	if client == d.admin {
		base = d.bases[int(d.adminBase.Add(1))%len(d.bases)]
	}
	request, err := http.NewRequestWithContext(ctx, method, base+path, body)
	if err != nil {
		return false
	}
	request.Header.Set("X-Workspace-Key", d.fixture.Workspace)
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	began := time.Now()
	response, err := client.Do(request)
	code, cause, example := 0, "", ""
	var raw []byte
	if err == nil {
		code = response.StatusCode
		if code >= 200 && code < 300 && out != nil {
			raw, err = io.ReadAll(io.LimitReader(response.Body, 256<<20))
		} else if code >= 300 {
			raw, _ = io.ReadAll(io.LimitReader(response.Body, 4<<10))
			_, _ = io.Copy(io.Discard, response.Body)
		} else {
			_, err = io.Copy(io.Discard, response.Body)
		}
		response.Body.Close()
	}
	elapsed := time.Since(began)
	var netErr net.Error
	timeout := err != nil && (errors.Is(err, context.DeadlineExceeded) || errors.As(err, &netErr) && netErr.Timeout())
	if code >= 300 {
		message := strings.TrimSpace(string(raw))
		if len(message) > 160 {
			message = message[:160]
		}
		example = fmt.Sprintf("%d %s", code, message)
		if code == http.StatusServiceUnavailable {
			switch {
			case strings.Contains(message, "busy with other requests"):
				cause = "cold-queue"
			case strings.Contains(message, "busy loading for other people"):
				cause = "projection-gate"
			case strings.Contains(message, "Workspace is temporarily unavailable"):
				cause = "workspace-freshness"
			case strings.Contains(message, "Changes are temporarily unavailable"):
				cause = "sync-changes"
			default:
				cause = "other-503"
			}
		}
	} else if err != nil && ctx.Err() == nil {
		example = "transport: " + strings.SplitN(err.Error(), ": ", 2)[0]
	}
	if ctx.Err() != nil && err != nil {
		// The driver was stopped while the request was in flight: not a measurement.
		return false
	}
	// A request counts when it started inside the measured window; one still
	// in flight at the end is waited for (up to its timeout) and counted.
	if start, end := d.startAt.Load(), d.endAt.Load(); start != 0 && began.UnixNano() >= start && (end == 0 || began.UnixNano() < end) {
		d.opStats(name).add(code, elapsed, cause, err != nil, timeout, example, vu.random)
	}
	if err != nil || code < 200 || code > 299 {
		return false
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out) == nil
	}
	return true
}

func loadRigClient(identity loadRigIdentity, bases []string, transport http.RoundTripper, timeout time.Duration) *http.Client {
	jar, _ := cookiejar.New(nil)
	for _, base := range bases {
		u, _ := url.Parse(base)
		jar.SetCookies(u, []*http.Cookie{{Name: sessionCookieName, Value: identity.Token, Path: "/"}})
	}
	return &http.Client{Jar: jar, Transport: transport, Timeout: timeout}
}

// loadRigMix applies FLOW_LOADRIG_MIX ("issue.list=20,search=0,...").
func loadRigMix(t *testing.T, ops []loadRigOp) []loadRigOp {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("FLOW_LOADRIG_MIX"))
	if raw == "" {
		return ops
	}
	for _, part := range strings.Split(raw, ",") {
		name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		weight, err := strconv.ParseFloat(value, 64)
		index := slices.IndexFunc(ops, func(op loadRigOp) bool { return op.name == name })
		if !ok || err != nil || weight < 0 || index < 0 {
			t.Fatalf("invalid FLOW_LOADRIG_MIX entry %q", part)
		}
		ops[index].weight = weight
	}
	return ops
}

type loadRigSample struct {
	At                time.Time `json:"at"`
	Replica           int       `json:"replica"`
	RSSBytes          int64     `json:"rssBytes"`
	CPUPercent        float64   `json:"cpuPercent"`
	HeapInuseBytes    uint64    `json:"heapInuseBytes"`
	Goroutines        int       `json:"goroutines"`
	ColdSlotWaiting   int64     `json:"coldSlotWaiting"`
	ColdSlotsHeld     int64     `json:"coldSlotsHeld"`
	CPUSecondsElapsed float64   `json:"cpuSeconds"`
}

func loadRigPS(pid string) (rss int64, cpu float64) {
	if pid == "" {
		return 0, 0
	}
	output, err := exec.Command("ps", "-o", "rss=,%cpu=", "-p", pid).Output()
	if err != nil {
		return 0, 0
	}
	fields := strings.Fields(string(output))
	if len(fields) >= 2 {
		kb, _ := strconv.ParseInt(fields[0], 10, 64)
		cpu, _ = strconv.ParseFloat(fields[1], 64)
		rss = kb * 1024
	}
	return rss, cpu
}

func loadRigMetrics(ctx context.Context, client *http.Client, endpoint string) (loadRigProcessMetrics, error) {
	var value loadRigProcessMetrics
	_, err := loadRigCall(ctx, client, http.MethodGet, endpoint+"/metrics", "", nil, &value)
	return value, err
}

func TestLoadRigDrive(t *testing.T) {
	out := os.Getenv("FLOW_LOADRIG_OUT")
	if os.Getenv("FLOW_LOADRIG_DRIVE") != "1" || out == "" {
		t.Skip("set FLOW_LOADRIG_DRIVE=1, FLOW_LOADRIG_OUT, FLOW_LOADRIG_FIXTURE and FLOW_LOADRIG_BASES")
	}
	label := loadRigEnv("FLOW_LOADRIG_LABEL", "run")
	raw, err := os.ReadFile(os.Getenv("FLOW_LOADRIG_FIXTURE"))
	if err != nil {
		t.Fatal("FLOW_LOADRIG_FIXTURE unreadable")
	}
	var fixture loadRigFixture
	if json.Unmarshal(raw, &fixture) != nil || fixture.Workspace == "" || fixture.Admin.Token == "" || len(fixture.Issues) == 0 {
		t.Fatal("invalid load rig fixture (contents suppressed)")
	}
	bases := strings.Split(os.Getenv("FLOW_LOADRIG_BASES"), ",")
	metricURLs := strings.Split(loadRigEnv("FLOW_LOADRIG_METRICS", ""), ",")
	pids := strings.Split(loadRigEnv("FLOW_LOADRIG_PIDS", ""), ",")
	if len(bases) == 0 || bases[0] == "" {
		t.Fatal("FLOW_LOADRIG_BASES is required")
	}
	vus := loadRigEnvInt(t, "FLOW_LOADRIG_VUS", 10)
	duration := loadRigEnvDuration(t, "FLOW_LOADRIG_DURATION", time.Minute)
	warmup := loadRigEnvDuration(t, "FLOW_LOADRIG_WARMUP", 5*time.Second)
	think := loadRigEnvDuration(t, "FLOW_LOADRIG_THINK", 300*time.Millisecond)
	streams := loadRigEnvInt(t, "FLOW_LOADRIG_STREAMS", max(1, vus/2))
	agentEvery := loadRigEnvDuration(t, "FLOW_LOADRIG_AGENT_EVERY", 20*time.Second)
	agentMax := loadRigEnvInt(t, "FLOW_LOADRIG_AGENT_MAX", 0)
	agentChat := loadRigEnv("FLOW_LOADRIG_AGENT_CHAT", "on") == "on"
	sampleEvery := loadRigEnvDuration(t, "FLOW_LOADRIG_SAMPLE", 2*time.Second)
	timeout := loadRigEnvDuration(t, "FLOW_LOADRIG_REQUEST_TIMEOUT", 30*time.Second)
	if vus < 1 || duration <= 0 {
		t.Fatal("FLOW_LOADRIG_VUS and FLOW_LOADRIG_DURATION must be positive")
	}
	transport := &http.Transport{MaxIdleConnsPerHost: vus*2 + streams + 8, MaxConnsPerHost: 0, IdleConnTimeout: 30 * time.Second}
	defer transport.CloseIdleConnections()
	d := &loadRigDriver{fixture: fixture, bases: bases, stats: map[string]*loadRigOpStats{}}
	d.admin = loadRigClient(fixture.Admin, bases, transport, timeout)
	d.keyClient = &http.Client{Transport: transport, Timeout: timeout}
	ops := loadRigMix(t, loadRigOps())
	totalWeight := 0.0
	for _, op := range ops {
		totalWeight += op.weight
	}
	members, readers := []loadRigIdentity{}, []loadRigIdentity{}
	for _, identity := range fixture.Identities {
		if len(identity.TeamIDs) == 0 {
			continue
		}
		readers = append(readers, identity)
		if identity.Role != "guest" {
			members = append(members, identity)
		}
	}
	if len(readers) == 0 {
		readers = []loadRigIdentity{fixture.Admin}
		members = readers
	}
	visible := func(identity loadRigIdentity) []loadRigIssueRef {
		result := []loadRigIssueRef{}
		for _, ref := range fixture.Issues {
			if slices.Contains(identity.TeamIDs, ref.TeamID) {
				result = append(result, ref)
			}
		}
		if len(result) == 0 {
			result = fixture.Issues
		}
		return result
	}
	newVU := func(index int, identity loadRigIdentity, member bool) *loadRigVU {
		return &loadRigVU{index: index, identity: identity, client: loadRigClient(identity, bases, transport, timeout), base: bases[index%len(bases)],
			issues: visible(identity), projects: loadRigVisibleRefs(identity, fixture.Projects), documents: loadRigVisibleRefs(identity, fixture.Documents), random: rand.New(rand.NewPCG(uint64(index)+1, uint64(time.Now().UnixNano()))), member: member}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	runCtx, stopRun := context.WithTimeout(ctx, warmup+duration)
	defer stopRun()

	metricsClient := &http.Client{Timeout: 5 * time.Second}
	samples := []loadRigSample{}
	first := make([]loadRigProcessMetrics, len(bases))
	last := make([]loadRigProcessMetrics, len(bases))
	var sampleMu sync.Mutex
	metricsOn := len(metricURLs) == len(bases) && metricURLs[0] != ""
	sample := func(record bool) {
		for index := range bases {
			entry := loadRigSample{At: time.Now().UTC(), Replica: index}
			if index < len(pids) {
				entry.RSSBytes, entry.CPUPercent = loadRigPS(pids[index])
			}
			if metricsOn {
				if value, err := loadRigMetrics(ctx, metricsClient, metricURLs[index]); err == nil {
					sampleMu.Lock()
					if record && first[index].PID == 0 {
						first[index] = value
					}
					last[index] = value
					sampleMu.Unlock()
					entry.HeapInuseBytes, entry.Goroutines = value.HeapInuseBytes, value.Goroutines
					entry.ColdSlotWaiting, entry.ColdSlotsHeld = value.Counters["coldSlotWaiting"], value.Counters["coldSlotsHeld"]
					entry.CPUSecondsElapsed = value.CPUUserSeconds + value.CPUSystemSeconds
				}
			}
			if record {
				sampleMu.Lock()
				samples = append(samples, entry)
				sampleMu.Unlock()
			}
		}
	}

	var wait sync.WaitGroup
	// Realtime streams.
	for index := range streams {
		identity := readers[index%len(readers)]
		client := loadRigClient(identity, bases, transport, 0)
		base := bases[index%len(bases)]
		wait.Add(1)
		go func() {
			defer wait.Done()
			d.stream(runCtx, client, base)
		}()
	}
	// Virtual users.
	for index := range vus {
		member := index%4 != 3 || len(members) == len(readers)
		identity := members[index%len(members)]
		if !member {
			identity = readers[index%len(readers)]
		}
		vu := newVU(index, identity, member)
		wait.Add(1)
		go func() {
			defer wait.Done()
			// Spread the start over the first second.
			if !scaleP01Pause(runCtx, time.Duration(vu.random.IntN(1000))*time.Millisecond) {
				return
			}
			for runCtx.Err() == nil {
				pick := vu.random.Float64() * totalWeight
				var op loadRigOp
				for _, candidate := range ops {
					if pick < candidate.weight {
						op = candidate
						break
					}
					pick -= candidate.weight
				}
				if op.run == nil {
					continue
				}
				if op.write && !vu.member {
					op = ops[1] // guests read an issue instead
				}
				op.run(ctx, d, vu)
				if !scaleP01Pause(runCtx, time.Duration(float64(think)*(0.5+vu.random.Float64()))) {
					return
				}
			}
		}()
	}
	// Agent traffic: loop runs and agent chat, behind the COLD per-replica limit.
	agentStarted := atomic.Int64{}
	if agentEvery > 0 && len(fixture.LoopIDs) > 0 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			var runs sync.WaitGroup
			defer runs.Wait()
			ticker := time.NewTicker(agentEvery)
			defer ticker.Stop()
			for turn := 0; ; turn++ {
				select {
				case <-runCtx.Done():
					return
				case <-ticker.C:
				}
				if agentMax > 0 && agentStarted.Load() >= int64(agentMax) {
					continue
				}
				agentStarted.Add(1)
				vu := newVU(100000+turn, fixture.Admin, true)
				vu.issues = fixture.Issues
				if agentChat && turn%2 == 1 {
					runs.Add(1)
					go func() {
						defer runs.Done()
						d.request(ctx, vu, d.admin, "agent.chat", http.MethodPost, "/api/agent/chat", map[string]any{"message": "Summarize this issue in one line.", "issueIds": []string{d.fixture.Issues[vu.random.IntN(len(d.fixture.Issues))].ID}}, nil)
					}()
					continue
				}
				runs.Add(1)
				go func() {
					defer runs.Done()
					d.loopRun(ctx, runCtx, vu)
				}()
			}
		}()
	}
	// Metrics sampling.
	wait.Add(1)
	go func() {
		defer wait.Done()
		ticker := time.NewTicker(sampleEvery)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				sample(d.recording.Load())
			}
		}
	}()
	if !scaleP01Pause(runCtx, warmup) {
		t.Fatal("run canceled during warmup")
	}
	sample(true)
	d.startAt.Store(time.Now().UnixNano())
	d.recording.Store(true)
	measuredStart := time.Now()
	<-runCtx.Done()
	measured := time.Since(measuredStart)
	d.endAt.Store(time.Now().UnixNano())
	sample(true)
	d.recording.Store(false)
	// Requests in flight finish (or time out) and are counted.
	done := make(chan struct{})
	go func() { wait.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(timeout + 10*time.Second):
		t.Log("in-flight requests did not finish within the request timeout")
	}
	cancel()
	<-done

	report := d.report(label, measured, vus, streams, bases, first, last, samples)
	report["settings"] = map[string]any{"vus": vus, "durationSeconds": duration.Seconds(), "warmupSeconds": warmup.Seconds(), "thinkMs": think.Milliseconds(),
		"streams": streams, "agentEverySeconds": agentEvery.Seconds(), "agentMax": agentMax, "agentStarted": agentStarted.Load(), "mix": os.Getenv("FLOW_LOADRIG_MIX"),
		"topology": os.Getenv("FLOW_LOADRIG_TOPOLOGY"), "flag": os.Getenv("FLOW_LOADRIG_FLAG"), "model": os.Getenv("FLOW_LOADRIG_MODEL"),
		"scale": fixture.Scale, "issues": fixture.IssueCount}
	if err := loadRigWriteJSON(filepath.Join(out, label+".json"), report, 0o644); err != nil {
		t.Fatal(err)
	}
	markdown := loadRigMarkdown(report)
	if err := os.WriteFile(filepath.Join(out, label+".md"), []byte(markdown), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("load rig %s: %s", label, filepath.Join(out, label+".md"))
}

// stream holds one realtime connection and measures how long a write the
// driver sent takes to arrive as an event.
func (d *loadRigDriver) stream(ctx context.Context, client *http.Client, base string) {
	for ctx.Err() == nil {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/realtime/events", nil)
		request.Header.Set("X-Workspace-Key", d.fixture.Workspace)
		d.streamConn.Add(1)
		response, err := client.Do(request)
		if err == nil && response.StatusCode == http.StatusOK {
			d.streamsUp.Add(1)
			scanner := bufio.NewScanner(response.Body)
			scanner.Buffer(make([]byte, 64<<10), 32<<20)
			for scanner.Scan() {
				line := scanner.Bytes()
				if !bytes.HasPrefix(line, []byte("data: ")) {
					continue
				}
				var event domain.RealtimeEvent
				if json.Unmarshal(line[6:], &event) != nil || event.Type == "" || event.Type == "connected" || event.Type == "presence.updated" {
					continue
				}
				if !d.recording.Load() {
					continue
				}
				d.events.Add(1)
				counter, _ := d.eventTypes.LoadOrStore(event.Type, &atomic.Int64{})
				counter.(*atomic.Int64).Add(1)
				if sent, ok := d.sent.Load(event.AggregateID); ok {
					lag := time.Since(time.Unix(0, sent.(int64)))
					if lag < 60*time.Second {
						d.lagMu.Lock()
						if len(d.lags) < loadRigReservoir {
							d.lags = append(d.lags, float64(lag)/float64(time.Millisecond))
						}
						d.lagMu.Unlock()
					}
				}
			}
			d.streamsUp.Add(-1)
			response.Body.Close()
			if ctx.Err() == nil {
				d.streamFail.Add(1)
			}
		} else {
			if err == nil {
				response.Body.Close()
			}
			if ctx.Err() == nil {
				d.streamFail.Add(1)
			}
		}
		if !scaleP01Pause(ctx, time.Second) {
			return
		}
	}
}

// loopRun starts one loop run on an issue and waits for it to finish.
// Requests use ctx; polling stops when the run window (window) ends.
func (d *loadRigDriver) loopRun(ctx, window context.Context, vu *loadRigVU) {
	loop := d.pick(vu, d.fixture.LoopIDs)
	ref := d.fixture.Issues[vu.random.IntN(len(d.fixture.Issues))]
	var run domain.LoopRun
	began := time.Now()
	if !d.request(ctx, vu, d.admin, "agent.loop-run.start", http.MethodPost, "/api/loops/"+loop+"/runs", map[string]any{"entityId": ref.Identifier}, &run) || run.ID == "" {
		d.countRun("start-failed")
		return
	}
	for window.Err() == nil {
		if !scaleP01Pause(window, time.Second) {
			break
		}
		var current domain.LoopRun
		if d.request(ctx, vu, d.admin, "agent.loop-run.poll", http.MethodGet, "/api/loops/"+loop+"/runs/"+run.ID, nil, &current) && current.Status != "" && current.Status != "running" && current.Status != "queued" {
			d.countRun(current.Status)
			if start := d.startAt.Load(); start != 0 && began.UnixNano() >= start {
				d.opStats("agent.loop-run.finish").add(http.StatusOK, time.Since(began), "", false, false, "", vu.random)
			}
			return
		}
	}
	d.countRun("unfinished-at-end")
}

func (d *loadRigDriver) countRun(status string) {
	counter, _ := d.agentRuns.LoadOrStore(status, &atomic.Int64{})
	counter.(*atomic.Int64).Add(1)
}

func loadRigPercentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	index := int(math.Ceil(p/100*float64(len(sorted)))) - 1
	return sorted[max(0, min(len(sorted)-1, index))]
}

type loadRigSummary struct {
	Count       int64            `json:"count"`
	RPS         float64          `json:"rps"`
	P50Ms       float64          `json:"p50Ms"`
	P95Ms       float64          `json:"p95Ms"`
	P99Ms       float64          `json:"p99Ms"`
	MaxMs       float64          `json:"maxMs"`
	MeanMs      float64          `json:"meanMs"`
	OK          int64            `json:"ok"`
	Status4xx   int64            `json:"status4xx"`
	Status5xx   int64            `json:"status5xx"`
	Busy503     map[string]int64 `json:"busy503,omitempty"`
	Network     int64            `json:"networkErrors"`
	Timeouts    int64            `json:"timeouts"`
	ErrorRate   float64          `json:"errorRate"`
	Class       string           `json:"class,omitempty"`
	Pattern     string           `json:"pattern,omitempty"`
	Temperature string           `json:"temperature,omitempty"`
	Codes       map[string]int64 `json:"codes,omitempty"`
	Examples    []string         `json:"examples,omitempty"`
}

func loadRigSummarize(parts []*loadRigOpStats, seconds float64) loadRigSummary {
	summary := loadRigSummary{Busy503: map[string]int64{}, Codes: map[string]int64{}}
	all := []float64{}
	total := 0.0
	for _, part := range parts {
		part.mu.Lock()
		summary.Count += part.count
		summary.Network += part.network
		summary.Timeouts += part.timeouts
		for code, n := range part.codes {
			summary.Codes[strconv.Itoa(code)] += n
			switch {
			case code >= 200 && code < 400:
				summary.OK += n
			case code >= 400 && code < 500:
				summary.Status4xx += n
			case code >= 500:
				summary.Status5xx += n
			}
		}
		for cause, n := range part.busy {
			summary.Busy503[cause] += n
		}
		all = append(all, part.samples...)
		total += part.totalMs
		summary.MaxMs = max(summary.MaxMs, part.maxMs)
		for _, example := range part.examples {
			if len(summary.Examples) < 4 {
				summary.Examples = append(summary.Examples, example)
			}
		}
		part.mu.Unlock()
	}
	sort.Float64s(all)
	summary.P50Ms, summary.P95Ms, summary.P99Ms = loadRigPercentile(all, 50), loadRigPercentile(all, 95), loadRigPercentile(all, 99)
	if summary.Count > 0 {
		summary.MeanMs = total / float64(summary.Count)
		summary.ErrorRate = float64(summary.Status5xx+summary.Network+summary.Timeouts) / float64(summary.Count)
	}
	summary.RPS = float64(summary.Count) / max(seconds, 1e-9)
	if len(summary.Busy503) == 0 {
		summary.Busy503 = nil
	}
	return summary
}

func (d *loadRigDriver) report(label string, measured time.Duration, vus, streams int, bases []string, first, last []loadRigProcessMetrics, samples []loadRigSample) map[string]any {
	seconds := measured.Seconds()
	ops := map[string]loadRigSummary{}
	classes := map[string][]*loadRigOpStats{}
	all := []*loadRigOpStats{}
	d.statsMu.Lock()
	names := make([]string, 0, len(d.stats))
	for name := range d.stats {
		names = append(names, name)
	}
	d.statsMu.Unlock()
	sort.Strings(names)
	for _, name := range names {
		stats := d.opStats(name)
		meta := loadRigRequests[name]
		summary := loadRigSummarize([]*loadRigOpStats{stats}, seconds)
		summary.Class, summary.Pattern = meta[0], meta[1]
		summary.Temperature = scaleP21RouteTemperature[meta[1]]
		if summary.Temperature == "" && strings.Contains(meta[1], " /") {
			summary.Temperature = "cold (not in the route table)"
		}
		ops[name] = summary
		classes[meta[0]] = append(classes[meta[0]], stats)
		if name != "agent.loop-run.finish" {
			all = append(all, stats)
		}
	}
	classSummaries := map[string]loadRigSummary{}
	for class, parts := range classes {
		summary := loadRigSummarize(parts, seconds)
		summary.Codes, summary.Examples = nil, nil
		classSummaries[class] = summary
	}
	overall := loadRigSummarize(all, seconds)
	overall.Codes, overall.Examples = nil, nil
	replicas := []map[string]any{}
	for index := range bases {
		peakRSS, peakHeap, peakWaiting, peakHeld, peakGoroutines := int64(0), uint64(0), int64(0), int64(0), 0
		cpuSum, cpuSamples := 0.0, 0
		endRSS := int64(0)
		for _, entry := range samples {
			if entry.Replica != index {
				continue
			}
			peakRSS, peakHeap = max(peakRSS, entry.RSSBytes), max(peakHeap, entry.HeapInuseBytes)
			peakWaiting, peakHeld, peakGoroutines = max(peakWaiting, entry.ColdSlotWaiting), max(peakHeld, entry.ColdSlotsHeld), max(peakGoroutines, entry.Goroutines)
			cpuSum += entry.CPUPercent
			cpuSamples++
			endRSS = entry.RSSBytes
		}
		replica := map[string]any{"base": loadRigPathOnly(bases[index]), "peakRSSMiB": float64(peakRSS) / (1 << 20), "endRSSMiB": float64(endRSS) / (1 << 20),
			"peakHeapInuseMiB": float64(peakHeap) / (1 << 20), "peakGoroutines": peakGoroutines, "peakColdSlotWaiting": peakWaiting, "peakColdSlotsHeld": peakHeld}
		if cpuSamples > 0 {
			replica["psCPUPercentAvg"] = cpuSum / float64(cpuSamples)
		}
		if first[index].PID != 0 {
			a, b := first[index], last[index]
			cpu := (b.CPUUserSeconds + b.CPUSystemSeconds) - (a.CPUUserSeconds + a.CPUSystemSeconds)
			replica["pid"] = b.PID
			replica["boundedMetadata"] = b.BoundedMetadata
			replica["cpuSeconds"] = cpu
			replica["cpuPercentOfOneCore"] = cpu / max(seconds, 1e-9) * 100
			replica["maxRSSMiB"] = float64(b.MaxRSSBytes) / (1 << 20)
			replica["endHeapInuseMiB"] = float64(b.HeapInuseBytes) / (1 << 20)
			replica["gcRuns"] = b.NumGC - a.NumGC
			delta := map[string]int64{}
			for key, value := range b.Counters {
				if strings.HasSuffix(key, "Max") || strings.HasSuffix(key, "MaxNanos") || key == "coldSlotWaiting" || key == "coldSlotsHeld" || key == "coldSlotQueues" {
					delta[key] = value // a gauge or a running maximum
					continue
				}
				delta[key] = value - a.Counters[key]
			}
			replica["counters"] = delta
			replica["bounded"] = loadRigBoundedDerived(delta)
		}
		replicas = append(replicas, replica)
	}
	d.lagMu.Lock()
	lags := slices.Clone(d.lags)
	d.lagMu.Unlock()
	sort.Float64s(lags)
	eventTypes := map[string]int64{}
	d.eventTypes.Range(func(key, value any) bool {
		eventTypes[key.(string)] = value.(*atomic.Int64).Load()
		return true
	})
	agentRuns := map[string]int64{}
	d.agentRuns.Range(func(key, value any) bool {
		agentRuns[key.(string)] = value.(*atomic.Int64).Load()
		return true
	})
	return map[string]any{
		"label": label, "measuredSeconds": seconds, "vus": vus, "replicas": replicas, "overall": overall, "classes": classSummaries, "ops": ops,
		"realtime": map[string]any{"streams": streams, "connects": d.streamConn.Load(), "dropsOrFailures": d.streamFail.Load(), "events": d.events.Load(),
			"eventsPerSecond": float64(d.events.Load()) / max(seconds, 1e-9), "lagSamples": len(lags), "lagP50Ms": loadRigPercentile(lags, 50),
			"lagP95Ms": loadRigPercentile(lags, 95), "lagP99Ms": loadRigPercentile(lags, 99), "eventTypes": eventTypes},
		"agentRuns": agentRuns, "samples": samples,
	}
}

func loadRigBoundedDerived(c map[string]int64) map[string]float64 {
	ms := func(nanos int64) float64 { return float64(nanos) / 1e6 }
	avg := func(nanos, count int64) float64 {
		if count == 0 {
			return 0
		}
		return ms(nanos) / float64(count)
	}
	hotWaits := c["writerLockWaits"] - c["coldWriterLockWaits"]
	hotNanos := c["writerLockWaitNanos"] - c["coldWriterWaitNanos"]
	return map[string]float64{
		"coldSlotAcquisitions": float64(c["coldSlotAcquisitions"]), "coldSlotRefusals": float64(c["coldSlotRefusals"]),
		"coldSlotWaits": float64(c["coldSlotWaits"]), "coldSlotWaitAvgMs": avg(c["coldSlotWaitNanos"], c["coldSlotWaits"]), "coldSlotWaitMaxMs": ms(c["coldSlotWaitMaxNanos"]),
		"coldFacadeDecodes": float64(c["coldFacadeDecodes"]), "coldFacadeDecodeAvgMs": avg(c["coldFacadeDecodeNanos"], c["coldFacadeDecodes"]),
		"coldViewDecodes": float64(c["coldViewReads"]), "coldViewDecodeAvgMs": avg(c["coldViewDecodeNanos"], c["coldViewReads"]), "coldViewFailures": float64(c["coldViewFailures"]),
		"coldWriteLockHolds": float64(c["coldWriteLockHolds"]), "coldWriteLockAvgMs": avg(c["coldWriteLockNanos"], c["coldWriteLockHolds"]), "coldWriteLockMaxMs": ms(c["coldWriteLockMaxNanos"]),
		"writerLockWaits": float64(c["writerLockWaits"]), "writerLockWaitAvgMs": avg(c["writerLockWaitNanos"], c["writerLockWaits"]), "writerLockWaitMaxMs": ms(c["writerLockWaitMax"]),
		"hotWriterLockWaits": float64(hotWaits), "hotWriterLockWaitAvgMs": avg(hotNanos, hotWaits),
		"coldWriterLockWaits": float64(c["coldWriterLockWaits"]), "coldWriterLockWaitAvgMs": avg(c["coldWriterWaitNanos"], c["coldWriterLockWaits"]),
	}
}

var loadRigClassOrder = []string{"hot-read", "hot-write", "search", "sync", "page-load", "api-key", "cold-read", "cold-write", "agent"}

func loadRigMarkdown(report map[string]any) string {
	var b strings.Builder
	raw, _ := json.Marshal(report)
	var r struct {
		Label    string                    `json:"label"`
		Seconds  float64                   `json:"measuredSeconds"`
		Settings map[string]any            `json:"settings"`
		Overall  loadRigSummary            `json:"overall"`
		Classes  map[string]loadRigSummary `json:"classes"`
		Ops      map[string]loadRigSummary `json:"ops"`
		Replicas []map[string]any          `json:"replicas"`
		Realtime map[string]any            `json:"realtime"`
		Agent    map[string]int64          `json:"agentRuns"`
	}
	_ = json.Unmarshal(raw, &r)
	fmt.Fprintf(&b, "# Load rig run %s\n\n", r.Label)
	settings, _ := json.Marshal(r.Settings)
	fmt.Fprintf(&b, "Measured %.0fs. Settings: `%s`\n\n", r.Seconds, settings)
	fmt.Fprintf(&b, "Overall: %d requests, %.1f req/s, p50 %.0f ms, p95 %.0f ms, p99 %.0f ms, error rate %.2f%% (5xx/transport), 4xx %d.\n\n",
		r.Overall.Count, r.Overall.RPS, r.Overall.P50Ms, r.Overall.P95Ms, r.Overall.P99Ms, r.Overall.ErrorRate*100, r.Overall.Status4xx)
	b.WriteString("## Route classes\n\n| class | requests | req/s | p50 ms | p95 ms | p99 ms | max ms | 4xx | 5xx | 503 causes | transport |\n|---|---:|---:|---:|---:|---:|---:|---:|---:|---|---:|\n")
	for _, class := range loadRigClassOrder {
		s, ok := r.Classes[class]
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "| %s | %d | %.2f | %.0f | %.0f | %.0f | %.0f | %d | %d | %s | %d |\n", class, s.Count, s.RPS, s.P50Ms, s.P95Ms, s.P99Ms, s.MaxMs, s.Status4xx, s.Status5xx, loadRigCauses(s.Busy503), s.Network+s.Timeouts)
	}
	b.WriteString("\n## Operations\n\n| op | route | temperature | n | p50 | p95 | p99 | codes | sample errors |\n|---|---|---|---:|---:|---:|---:|---|---|\n")
	names := make([]string, 0, len(r.Ops))
	for name := range r.Ops {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		s := r.Ops[name]
		codes, _ := json.Marshal(s.Codes)
		examples := strings.ReplaceAll(strings.Join(s.Examples, " / "), "|", "\\|")
		examples = strings.ReplaceAll(examples, "\n", " ")
		fmt.Fprintf(&b, "| %s | `%s` | %s | %d | %.0f | %.0f | %.0f | `%s` | %s |\n", name, s.Pattern, s.Temperature, s.Count, s.P50Ms, s.P95Ms, s.P99Ms, codes, examples)
	}
	b.WriteString("\n## Replicas\n\n")
	for index, replica := range r.Replicas {
		bounded, _ := json.Marshal(replica["bounded"])
		fmt.Fprintf(&b, "- replica %d (flag on: %v): peak RSS %.0f MiB (ps), end RSS %.0f MiB, max RSS %.0f MiB (getrusage), peak heap in use %.0f MiB, CPU %.1fs (%.0f%% of one core; ps avg %.0f%%), GC runs %v, peak goroutines %v, peak COLD queue %v, peak COLD held %v\n  - bounded: `%s`\n",
			index, replica["boundedMetadata"], loadRigNumber(replica["peakRSSMiB"]), loadRigNumber(replica["endRSSMiB"]), loadRigNumber(replica["maxRSSMiB"]), loadRigNumber(replica["peakHeapInuseMiB"]),
			loadRigNumber(replica["cpuSeconds"]), loadRigNumber(replica["cpuPercentOfOneCore"]), loadRigNumber(replica["psCPUPercentAvg"]), replica["gcRuns"], replica["peakGoroutines"], replica["peakColdSlotWaiting"], replica["peakColdSlotsHeld"], bounded)
	}
	realtime, _ := json.Marshal(r.Realtime)
	agent, _ := json.Marshal(r.Agent)
	fmt.Fprintf(&b, "\n## Realtime\n\n`%s`\n\n## Agent runs (final status)\n\n`%s`\n", realtime, agent)
	return b.String()
}

func loadRigNumber(value any) float64 {
	number, _ := value.(float64)
	return number
}

func loadRigCauses(causes map[string]int64) string {
	if len(causes) == 0 {
		return ""
	}
	parts := []string{}
	for cause, n := range causes {
		parts = append(parts, fmt.Sprintf("%s %d", cause, n))
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

// TestLoadRigCompare writes FLOW_LOADRIG_COMPARE_OUT (markdown) comparing
// the reports FLOW_LOADRIG_COMPARE_A (baseline, flag off) and _B (flag on).
func TestLoadRigCompare(t *testing.T) {
	a, b, outPath := os.Getenv("FLOW_LOADRIG_COMPARE_A"), os.Getenv("FLOW_LOADRIG_COMPARE_B"), os.Getenv("FLOW_LOADRIG_COMPARE_OUT")
	if a == "" || b == "" || outPath == "" {
		t.Skip("set FLOW_LOADRIG_COMPARE_A, _B and _OUT")
	}
	type report struct {
		Label    string                    `json:"label"`
		Overall  loadRigSummary            `json:"overall"`
		Classes  map[string]loadRigSummary `json:"classes"`
		Ops      map[string]loadRigSummary `json:"ops"`
		Replicas []map[string]any          `json:"replicas"`
		Realtime map[string]any            `json:"realtime"`
	}
	read := func(path string) report {
		raw, err := os.ReadFile(path)
		var value report
		if err != nil || json.Unmarshal(raw, &value) != nil {
			t.Fatalf("unreadable report %s", path)
		}
		return value
	}
	left, right := read(a), read(b)
	var md strings.Builder
	fmt.Fprintf(&md, "# %s vs %s\n\n| class | req/s | p50 ms | p95 ms | p99 ms | 5xx+transport | 503 causes |\n|---|---|---|---|---|---|---|\n", left.Label, right.Label)
	pair := func(x, y float64, format string) string {
		return fmt.Sprintf(format+" → "+format, x, y)
	}
	row := func(name string, x, y loadRigSummary) {
		fmt.Fprintf(&md, "| %s | %s | %s | %s | %s | %d → %d | %s → %s |\n", name, pair(x.RPS, y.RPS, "%.2f"), pair(x.P50Ms, y.P50Ms, "%.0f"), pair(x.P95Ms, y.P95Ms, "%.0f"), pair(x.P99Ms, y.P99Ms, "%.0f"),
			x.Status5xx+x.Network+x.Timeouts, y.Status5xx+y.Network+y.Timeouts, cmpOrString(loadRigCauses(x.Busy503), "-"), cmpOrString(loadRigCauses(y.Busy503), "-"))
	}
	row("overall", left.Overall, right.Overall)
	for _, class := range loadRigClassOrder {
		x, okX := left.Classes[class]
		y, okY := right.Classes[class]
		if okX || okY {
			row(class, x, y)
		}
	}
	md.WriteString("\n| op | p95 ms | 5xx |\n|---|---|---|\n")
	names := []string{}
	for name := range right.Ops {
		names = append(names, name)
	}
	for name := range left.Ops {
		if _, ok := right.Ops[name]; !ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		x, y := left.Ops[name], right.Ops[name]
		fmt.Fprintf(&md, "| %s | %s | %d → %d |\n", name, pair(x.P95Ms, y.P95Ms, "%.0f"), x.Status5xx, y.Status5xx)
	}
	md.WriteString("\n| replica | peak RSS MiB | peak heap MiB | CPU s |\n|---|---|---|---|\n")
	for index := range max(len(left.Replicas), len(right.Replicas)) {
		get := func(r report, key string) float64 {
			if index < len(r.Replicas) {
				return loadRigNumber(r.Replicas[index][key])
			}
			return 0
		}
		fmt.Fprintf(&md, "| %d | %s | %s | %s |\n", index, pair(get(left, "peakRSSMiB"), get(right, "peakRSSMiB"), "%.0f"), pair(get(left, "peakHeapInuseMiB"), get(right, "peakHeapInuseMiB"), "%.0f"), pair(get(left, "cpuSeconds"), get(right, "cpuSeconds"), "%.1f"))
	}
	fmt.Fprintf(&md, "\nRealtime lag p95: %.0f → %.0f ms; events/s %.1f → %.1f\n", loadRigNumber(left.Realtime["lagP95Ms"]), loadRigNumber(right.Realtime["lagP95Ms"]), loadRigNumber(left.Realtime["eventsPerSecond"]), loadRigNumber(right.Realtime["eventsPerSecond"]))
	if err := os.WriteFile(outPath, []byte(md.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}
