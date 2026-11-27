//go:build loadtest

package main

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/coordination"
	"flow/api/internal/store"

	"github.com/alicebob/miniredis/v2"
)

func TestLoadRigMetricsDuringStartup(t *testing.T) {
	t.Setenv("FLOW_LOADRIG_METRICS_ADDR", "127.0.0.1:0")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_RECORD_PEER_SYNC", "on")
	if diagnoseWorkspaceReload == nil || diagnoseWorkspaceRefusal == nil {
		t.Fatal("freshness hooks were not installed during package init")
	}
	t.Cleanup(func() { loadRigGates.Store(nil) })
	addr, stop := startLoadRigMetrics()
	t.Cleanup(stop)
	if addr == "" {
		t.Fatal("metrics listener did not start")
	}
	client := &http.Client{Timeout: 5 * time.Second}
	t.Cleanup(client.CloseIdleConnections)
	metrics := func() loadRigProcessMetrics {
		t.Helper()
		resp, err := client.Get("http://" + addr + "/metrics")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var result loadRigProcessMetrics
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("metrics returned HTTP %d: %v", resp.StatusCode, err)
		}
		return result
	}
	if result := metrics(); result.PID == 0 {
		t.Fatal("process metrics unavailable before opening the database")
	}

	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "metrics.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	redis := miniredis.RunT(t)
	coord, err := coordination.Open(t.Context(), coordination.Config{
		Mode: "standalone", Addrs: []string{redis.Addr()}, Prefix: "metrics-startup", ConnectTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = coord.Close() })
	s := &server{store: repo, coordinator: coord,
		metadataGate:   newMetadataReadGate(2, 0, time.Second, time.Second),
		projectionGate: newMetadataReadGate(3, 0, time.Second, time.Second),
	}
	newTestHandler(t, s)
	observeLoadRigGates(s.metadataGate, s.projectionGate)
	if _, err := s.waitForPeerRefresh(t.Context(), "test-workspace"); err != nil {
		t.Fatalf("peer refresh during coordination start: %v", err)
	}
	result := metrics()
	if result.Counters["bootstrap.limit"] != 2 || result.Counters["resource.limit"] != 3 {
		t.Fatal("metrics response is missing the bootstrap and resource gate limits")
	}
}
