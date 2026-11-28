package telemetry

import (
	"encoding/json"
	"os"
	"regexp"
	"testing"
	"time"
)

func TestTelemetryStreamsAreExcludedFromSlowEventsAndDashboard(t *testing.T) {
	sse := "/api/agent/sessions/{id}/messages/{messageId}/stream"
	terminal := "/api/coding/environments/{id}/terminal"
	ordinary := "/api/issues/{id}"
	r, err := New(Options{Enabled: true, SlowRequest: time.Millisecond, Routes: []string{sse, terminal, ordinary},
		Targets: [3]Target{logs: {URL: "http://127.0.0.1"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []struct {
		name, method string
		status       int
	}{{sse, "PATCH", 200}, {terminal, "GET", 101}, {ordinary, "PATCH", 200}} {
		ctx := r.BeginRequest(t.Context(), route.name, route.method)
		request := ctx.Value(requestKey{}).(*request)
		request.start = time.Now().Add(-time.Second)
		r.EndRequest(ctx, route.status, false, 0)
	}
	if len(r.eventQueue) != 1 || (<-r.eventQueue).trace.route != ordinary {
		t.Fatal("healthy streams produced a slow event or an ordinary slow request was missed")
	}

	raw, err := os.ReadFile("../../../deploy/observability/grafana/dashboards/flow-performance.json")
	if err != nil {
		t.Fatal(err)
	}
	var dashboard struct {
		Panels []struct {
			ID      int
			Targets []struct{ Expr string }
		}
	}
	if err := json.Unmarshal(raw, &dashboard); err != nil {
		t.Fatal(err)
	}
	matcher := regexp.MustCompile(`http_route!~"([^"]+)"`)
	checked := 0
	for _, panel := range dashboard.Panels {
		if panel.ID != 1 && panel.ID != 2 {
			continue
		}
		part := matcher.FindStringSubmatch(panel.Targets[0].Expr)
		if len(part) != 2 {
			t.Fatalf("panel %d is missing its route exclusion", panel.ID)
		}
		exclude := regexp.MustCompile("^(?:" + part[1] + ")$")
		if !exclude.MatchString(sse) || !exclude.MatchString(terminal) || exclude.MatchString(ordinary) {
			t.Fatalf("panel %d route filter does not match the streaming exclusions", panel.ID)
		}
		checked++
	}
	if checked != 2 {
		t.Fatal("expected both p95 panels")
	}
}
