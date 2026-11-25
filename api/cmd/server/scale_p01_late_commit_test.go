//go:build loadtest

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/store"
)

func TestScaleP01TimedOutWriteStillContributesItsCommit(t *testing.T) {
	for name, value := range map[string]string{"FLOW_LOAD_USERS": "1", "FLOW_LOAD_UNIQUE_USERS": "1", "FLOW_LOAD_METADATA_COUNT_SCALE": "0.002", "FLOW_LOAD_METADATA_BODY_SCALE": "1", "FLOW_LOAD_DURATION": "2s", "FLOW_LOAD_CHANGE_EVERY": "1s", "FLOW_LOAD_REQUEST_TIMEOUT": "500ms", "FLOW_LOAD_DELIVERY_TIMEOUT": "500ms", "FLOW_LOAD_READ_RPS": "0", "FLOW_LOAD_ISSUE_WRITE_RPS": "0", "FLOW_LOAD_METADATA_WRITE_RPS": "0"} {
		t.Setenv(name, value)
	}
	path := filepath.Join(t.TempDir(), "late.json")
	t.Setenv("FLOW_LOAD_REPORT", path)
	peers := scaleP01NewPeers(t)
	fixture := scaleP01SeedFixture(t, peers)
	value, _ := scaleP01Probes.Load(peers.stores[1])
	probe := value.(*store.ScaleP01Probe)
	// An admitted handler may finish after its caller gives up. Force a real
	// commit after the load window, while retaining the production SSE path.
	inner := peers.second.Config.Handler
	delayed := httptest.NewServer(scaleP01Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch && r.URL.Path == "/api/documents/"+fixture.DocumentID {
			time.Sleep(1250 * time.Millisecond)
			r = r.WithContext(context.WithoutCancel(r.Context()))
		}
		inner.ServeHTTP(w, r)
	}), probe))
	defer delayed.Close()
	first, _ := scaleP01Probes.Load(peers.stores[0])
	scaleP01Run(t, []string{peers.first.URL, delayed.URL}, fixture, []scaleP01MetricSource{scaleP01LocalMetricSource(first.(*store.ScaleP01Probe)), scaleP01LocalMetricSource(probe)}, false)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		WriteErrors int                                `json:"writeErrors"`
		Outstanding int                                `json:"outstandingServerWrites"`
		Apply       struct{ Expected, Deliveries int } `json:"commitToClientApply"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result.WriteErrors < 1 || result.Apply.Expected < 1 || result.Apply.Deliveries < 1 || result.Outstanding != 0 {
		t.Fatalf("late commit disappeared: %+v", result)
	}
}
