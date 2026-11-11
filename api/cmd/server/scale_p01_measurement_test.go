//go:build loadtest

package main

import (
	"encoding/json"
	"math"
	"net/http"
	"os"
	"runtime"
	"slices"
	"sync"
	"syscall"
	"testing"
	"time"

	"flow/api/internal/store"
)

// Failed operations sort after every finite latency. JSON null means the
// requested percentile is censored, not zero or a success-only percentile.
func scaleP01Quantiles(values []time.Duration, failures int, unit time.Duration) map[string]any {
	ordered := slices.Clone(values)
	slices.Sort(ordered)
	n := len(ordered) + failures
	out := map[string]any{"denominator": n, "completed": len(ordered), "failed": failures, "failureTreatment": "+infinity"}
	for _, q := range []struct {
		name string
		p    float64
	}{{"p50", .5}, {"p95", .95}, {"p99", .99}, {"max", 1}} {
		var value any
		if n > 0 {
			index := max(0, int(math.Ceil(float64(n)*q.p))-1)
			if index < len(ordered) {
				value = float64(ordered[index]) / float64(unit)
			}
		}
		out[q.name] = value
	}
	return out
}

type scaleP01RouteStats struct {
	mu       sync.Mutex
	values   map[string][]time.Duration
	success  map[string][]time.Duration
	statuses map[string]map[int]int
	failed   map[string]int
	timeouts map[string]int
}

func newScaleP01RouteStats() *scaleP01RouteStats {
	return &scaleP01RouteStats{values: map[string][]time.Duration{}, success: map[string][]time.Duration{}, statuses: map[string]map[int]int{}, failed: map[string]int{}, timeouts: map[string]int{}}
}

func (s *scaleP01RouteStats) add(name string, code int, elapsed time.Duration, err error, timeout bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[name] = append(s.values[name], elapsed)
	if s.statuses[name] == nil {
		s.statuses[name] = map[int]int{}
	}
	s.statuses[name][code]++
	if err != nil || code < 200 || code >= 300 {
		s.failed[name]++
	} else {
		s.success[name] = append(s.success[name], elapsed)
	}
	if timeout {
		s.timeouts[name]++
	}
}

func (s *scaleP01RouteStats) report() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]any{}
	for name, values := range s.values {
		out[name] = map[string]any{
			"count": len(values), "statuses": s.statuses[name], "errors": s.failed[name], "timeouts": s.timeouts[name],
			"p50ms": percentile(slices.Clone(values), .5).Milliseconds(), "p95ms": percentile(slices.Clone(values), .95).Milliseconds(), "maxms": percentile(slices.Clone(values), 1).Milliseconds(),
			"sloLatencyMs": scaleP01Quantiles(s.success[name], s.failed[name], time.Millisecond),
		}
	}
	return out
}

type scaleP01Ack struct {
	EventID  string    `json:"eventId"`
	ClientID int       `json:"clientId"`
	Applied  time.Time `json:"appliedAt"`
}

type scaleP01Delivery struct {
	commit   store.ScaleP01Commit
	kind     string
	expected []bool
}

type scaleP01Ledger struct {
	mu                  sync.Mutex
	changes             map[string]scaleP01Delivery
	received            map[string]map[int]time.Time
	applied             map[string]map[int]time.Time
	browser             map[string]map[int]time.Time
	duplicates, invalid int
	rollups             map[string]map[string]*scaleP01DeliverySummary
	retired             map[string]bool
}

func newScaleP01Ledger() *scaleP01Ledger {
	return &scaleP01Ledger{changes: map[string]scaleP01Delivery{}, received: map[string]map[int]time.Time{}, applied: map[string]map[int]time.Time{}, browser: map[string]map[int]time.Time{}, rollups: map[string]map[string]*scaleP01DeliverySummary{}, retired: map[string]bool{}}
}

func (l *scaleP01Ledger) commit(event store.ScaleP01Commit, kind string, expected []bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, exists := l.changes[event.ID]; !exists {
		l.changes[event.ID] = scaleP01Delivery{commit: event, kind: kind, expected: slices.Clone(expected)}
	}
}

func (l *scaleP01Ledger) ack(ack scaleP01Ack, phase string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.retired[ack.EventID] {
		return
	}
	if ack.EventID == "" || ack.ClientID < 0 || ack.Applied.IsZero() {
		l.invalid++
		return
	}
	target := l.applied
	if phase == "received" {
		target = l.received
	} else if phase == "browser" {
		target = l.browser
	}
	if target[ack.EventID] == nil {
		target[ack.EventID] = map[int]time.Time{}
	}
	if _, exists := target[ack.EventID][ack.ClientID]; exists {
		l.duplicates++
		return
	}
	// A stream may beat the HTTP response that registers the commit. Keep
	// that acknowledgement and join by event ID, never by "latest kind".
	target[ack.EventID][ack.ClientID] = ack.Applied
}

func (l *scaleP01Ledger) report(now time.Time, timeout time.Duration, phase string) map[string]any {
	l.mu.Lock()
	defer l.mu.Unlock()
	target := l.target(phase)
	buckets := map[string]*scaleP01DeliverySummary{}
	for kind, value := range l.rollups[phase] {
		copy := *value
		buckets[kind] = &copy
	}
	for id, delivery := range l.changes {
		b := buckets[delivery.kind]
		if b == nil {
			b = &scaleP01DeliverySummary{}
			buckets[delivery.kind] = b
		}
		b.observe(delivery, target[id], l.received[id], now, timeout)
	}
	all := &scaleP01DeliverySummary{}
	byKind := map[string]any{}
	for kind, b := range buckets {
		byKind[kind] = b.report()
		all.merge(b)
	}
	out := all.report()
	out["byKind"], out["duplicateAcks"], out["rejectedAcks"] = byKind, l.duplicates, l.invalid
	return out
}

type scaleP01CPU struct {
	UserSeconds, SystemSeconds float64
}

func scaleP01CPUTime() scaleP01CPU {
	var usage syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &usage) != nil {
		return scaleP01CPU{}
	}
	return scaleP01CPU{UserSeconds: float64(usage.Utime.Sec) + float64(usage.Utime.Usec)/1e6, SystemSeconds: float64(usage.Stime.Sec) + float64(usage.Stime.Usec)/1e6}
}

func scaleP01CPUReport(before, after scaleP01CPU, elapsed time.Duration, users int) map[string]any {
	user, system := after.UserSeconds-before.UserSeconds, after.SystemSeconds-before.SystemSeconds
	return map[string]any{"userSeconds": user, "systemSeconds": system, "percentOfFourCPUs": (user + system) / max(elapsed.Seconds()*4, 1e-9) * 100, "secondsPerUser": (user + system) / float64(max(users, 1)), "gomaxprocs": runtime.GOMAXPROCS(0)}
}

func scaleP01SQLDelta(before, after store.ScaleP01SQL, requests int) map[string]any {
	n := after.Statements - before.Statements
	var p95 any
	remaining := int64(math.Ceil(float64(n) * .95))
	if n > 0 {
		for i, count := range after.ExecutionBuckets {
			remaining -= count - before.ExecutionBuckets[i]
			if remaining <= 0 {
				p95 = map[string]float64{"lowerMs": math.Ldexp(1, i) / 1e6, "upperMs": math.Ldexp(1, i+1) / 1e6}
				break
			}
		}
	}
	return map[string]any{"statements": n, "reads": after.Reads - before.Reads, "writes": after.Writes - before.Writes, "errors": after.Errors - before.Errors, "commits": after.Commits - before.Commits, "executeSeconds": after.ExecuteSeconds - before.ExecuteSeconds, "executeP95Bounds": p95, "statementsPerRequest": float64(n) / float64(max(requests, 1)), "scope": "driver executions including background work; excludes prepare/begin/commit; execution time excludes row iteration", "rowsExamined": nil, "databaseCPU": nil, "databaseTelemetry": "requires a database-side collector; driver execution timing is not database statement latency"}
}

func scaleP01SaveReport(t *testing.T, report any) {
	t.Helper()
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("load report:\n%s", raw)
	if path := os.Getenv("FLOW_LOAD_REPORT"); path != "" {
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestScaleP01FailureDenominators(t *testing.T) {
	routes := newScaleP01RouteStats()
	routes.add("read", http.StatusOK, 10*time.Millisecond, nil, false)
	routes.add("read", http.StatusServiceUnavailable, time.Millisecond, nil, false)
	routes.add("read", -1, time.Second, os.ErrDeadlineExceeded, true)
	read := routes.report()["read"].(map[string]any)
	slo := read["sloLatencyMs"].(map[string]any)
	if read["count"] != 3 || read["errors"] != 2 || read["timeouts"] != 1 || slo["denominator"] != 3 || slo["p95"] != nil {
		t.Fatalf("503/timeout vanished: %+v", read)
	}
	ledger := newScaleP01Ledger()
	now := time.Now()
	ledger.ack(scaleP01Ack{"early", 0, now.Add(time.Second)}, "applied")
	ledger.ack(scaleP01Ack{"early", 0, now.Add(2 * time.Second)}, "applied")
	ledger.commit(store.ScaleP01Commit{ID: "early", At: now}, "document", []bool{true, true, false})
	ledger.commit(store.ScaleP01Commit{ID: "later", At: now.Add(time.Second)}, "document", []bool{true})
	ledger.ack(scaleP01Ack{"later", 0, now.Add(3 * time.Second)}, "received")
	ledger.ack(scaleP01Ack{"later", 0, now.Add(500 * time.Millisecond)}, "applied")
	ledger.ack(scaleP01Ack{"early", 0, now.Add(4 * time.Second)}, "browser")
	result := ledger.report(now.Add(30*time.Second), 10*time.Second, "applied")
	if result["expected"] != 3 || result["deliveries"] != 1 || result["unappliedOrLate"] != 2 || result["invalidAcks"] != 1 || result["sloLatencySeconds"].(map[string]any)["p95"] != nil || result["p95s"].(float64) < 10 {
		t.Fatalf("incorrect delivery accounting: %+v", result)
	}
	if browser := ledger.report(now.Add(30*time.Second), 10*time.Second, "browser"); browser["deliveries"] != 1 || browser["expected"] != 3 {
		t.Fatalf("browser apply was confused with receipt: %+v", browser)
	}
}
