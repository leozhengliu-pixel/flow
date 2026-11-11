//go:build loadtest

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"

	"flow/api/internal/domain"
)

// Keep both modes on one warmed writer without peer reads between requests;
// the original Lwrite includes asynchronous peer reload allocations.
func TestScaleP04HTTPAllocations(t *testing.T) {
	peers, _, client, team, issue := writeLoadFixture(t)
	peers.stopListening(1)
	writes := loadEnvInt("FLOW_LOAD_WRITES", 20)
	report := map[string]map[string]float64{}
	for _, kind := range []string{"issue", "comment", "team"} {
		report[kind] = map[string]float64{}
		for _, mode := range []string{"off", "on"} {
			t.Setenv("FLOW_NARROW_MUTATION_INPUTS", mode)
			write := func(index int) {
				text := fmt.Sprintf("P04 allocation %s %03d", mode, index)
				switch kind {
				case "issue":
					authRequest[domain.Issue](t, client, http.MethodPatch, peers.second.URL+"/api/issues/"+issue.ID, map[string]string{"title": text}, "test-workspace", http.StatusOK)
				case "comment":
					authRequest[domain.Comment](t, client, http.MethodPost, peers.second.URL+"/api/issue-records/"+issue.ID+"/comments", map[string]string{"body": text}, "test-workspace", http.StatusCreated)
				case "team":
					authRequest[domain.Team](t, client, http.MethodPatch, peers.second.URL+"/api/workspaces/test-workspace/teams/"+team.ID, map[string]string{"name": text}, "", http.StatusOK)
				}
			}
			for i := range 3 {
				write(-i - 1)
			}
			var bytes uint64
			for i := range writes {
				cost := measureWrite(t, peers, func() { write(i) })
				bytes += cost.bytes
			}
			report[kind][mode] = float64(bytes) / float64(writes)
		}
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("whole-request allocation bytes/op (%d writes/mode):\n%s", writes, raw)
	if path := os.Getenv("FLOW_P04_ALLOCATION_REPORT"); path != "" {
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
