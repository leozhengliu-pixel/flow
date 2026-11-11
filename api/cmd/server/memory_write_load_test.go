//go:build loadtest

package main

// Write-path cost in a large workspace: writes on one replica while the
// other serves requests. It reports, per write, the workspace metadata
// decodes and the bytes allocated by the writing replica and by the other
// replica's next request (which reloads after the peer's change).
//
//	go test -tags loadtest -run TestLoadWriteDecodes -v ./cmd/server/
//
// Knobs (env): FLOW_LOAD_WRITES (50), FLOW_LOAD_REPORT (path for a JSON report).

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"testing"

	"flow/api/internal/domain"
)

type writeCost struct {
	decodes int64
	bytes   uint64
}

func measureWrite(t *testing.T, peers peerServers, step func()) writeCost {
	t.Helper()
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	decodes := peers.metadataDecodes()
	step()
	runtime.ReadMemStats(&after)
	return writeCost{peers.metadataDecodes() - decodes, after.TotalAlloc - before.TotalAlloc}
}

// writeLoadFixture is two replicas on the incident-size synthetic
// workspace, with a team and an issue written through B. A no longer listens
// for B's events: it catches up when its next request asks.
func writeLoadFixture(t *testing.T) (peers peerServers, adminA, adminB *http.Client, team domain.Team, issue domain.Issue) {
	t.Helper()
	peers = scaleP01NewPeers(t)
	first, second := peers.first.URL, peers.second.URL
	adminA, adminB = peerAdmin(t, first), peerAdmin(t, second)
	team = authRequest[domain.Team](t, adminB, http.MethodPost, second+"/api/workspaces/test-workspace/teams", map[string]any{"name": "Write Load", "key": "WLD"}, "", http.StatusCreated)
	issue = authRequest[domain.Issue](t, adminB, http.MethodPost, second+"/api/issues", map[string]any{"title": "Write load issue", "teamId": team.ID}, "test-workspace", http.StatusCreated)
	spec := scaleP01MetadataSpec(t, []string{team.ID}, nil)
	spec.Relations = false
	if err := peers.stores[1].SeedSyntheticMetadata(context.Background(), "test-workspace", spec); err != nil {
		t.Fatal(err)
	}
	for _, repository := range peers.stores {
		if err := repository.ReloadWorkspace(context.Background(), "test-workspace"); err != nil {
			t.Fatal(err)
		}
	}
	peers.stopListening(0)
	authRequest[domain.Issue](t, adminA, http.MethodGet, first+"/api/issue-records/"+issue.ID, nil, "test-workspace", http.StatusOK)
	return peers, adminA, adminB, team, issue
}

// TestLoadWriteProfile writes allocation profiles around issue title edits
// and comments on B (FLOW_PROFILE_DIR/<phase>-before.pprof and -after.pprof):
//
//	go tool pprof -sample_index=alloc_space -diff_base before.pprof after.pprof
func TestLoadWriteProfile(t *testing.T) {
	dir := os.Getenv("FLOW_PROFILE_DIR")
	if dir == "" {
		t.Skip("FLOW_PROFILE_DIR is not set")
	}
	writes := loadEnvInt("FLOW_LOAD_WRITES", 10)
	peers, _, adminB, _, issue := writeLoadFixture(t)
	peers.stopListening(1)
	second := peers.second.URL
	for _, phase := range []struct {
		name  string
		write func(index int)
	}{
		{"issue-title", func(index int) {
			authRequest[domain.Issue](t, adminB, http.MethodPatch, second+"/api/issues/"+issue.ID, map[string]any{"title": fmt.Sprintf("Profiled issue %d", index)}, "test-workspace", http.StatusOK)
		}},
		{"comment", func(index int) {
			authRequest[domain.Comment](t, adminB, http.MethodPost, second+"/api/issues/"+issue.ID+"/comments", map[string]any{"body": fmt.Sprintf("Profiled comment %d", index)}, "test-workspace", http.StatusCreated)
		}},
	} {
		phase.write(-1) // warm up
		profile := func(suffix string) {
			runtime.GC()
			file, err := os.Create(filepath.Join(dir, phase.name+"-"+suffix+".pprof"))
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			if err := pprof.Lookup("allocs").WriteTo(file, 0); err != nil {
				t.Fatal(err)
			}
		}
		profile("before")
		for index := range writes {
			phase.write(index)
		}
		profile("after")
	}
}

func TestLoadWriteDecodes(t *testing.T) {
	writes := loadEnvInt("FLOW_LOAD_WRITES", 50)
	if writes < 1 {
		t.Fatal("FLOW_LOAD_WRITES must be positive")
	}
	peers, adminA, adminB, team, issue := writeLoadFixture(t)
	first, second := peers.first.URL, peers.second.URL
	teamsURL := "/api/workspaces/test-workspace/teams"

	type phase struct {
		name  string
		write func(index int)
	}
	phases := []phase{
		{"issue title on B", func(index int) {
			authRequest[domain.Issue](t, adminB, http.MethodPatch, second+"/api/issues/"+issue.ID, map[string]any{"title": fmt.Sprintf("Write load issue %d", index)}, "test-workspace", http.StatusOK)
		}},
		{"comment on B", func(index int) {
			authRequest[domain.Comment](t, adminB, http.MethodPost, second+"/api/issue-records/"+issue.ID+"/comments", map[string]any{"body": fmt.Sprintf("Comment %d", index)}, "test-workspace", http.StatusCreated)
		}},
		{"team name on B", func(index int) {
			authRequest[domain.Team](t, adminB, http.MethodPatch, second+teamsURL+"/"+team.ID, map[string]any{"name": fmt.Sprintf("Write Load %d", index)}, "", http.StatusOK)
		}},
	}
	report := map[string]map[string]any{}
	for _, phase := range phases {
		var writer, reader scaleP01WriteCost
		for index := range writes {
			cost := scaleP01MeasureWrite(t, peers, func() { phase.write(index) })
			writer.add(cost)
			cost = scaleP01MeasureWrite(t, peers, func() {
				authRequest[domain.Issue](t, adminA, http.MethodGet, first+"/api/issue-records/"+issue.ID, nil, "test-workspace", http.StatusOK)
			})
			reader.add(cost)
		}
		n := float64(writes)
		report[phase.name] = map[string]any{
			"writes":                     writes,
			"writerDecodesPerWrite":      float64(writer.decodes) / n,
			"writerMiBPerWrite":          float64(writer.bytes) / n / (1 << 20),
			"peerRequestDecodesPerWrite": float64(reader.decodes) / n,
			"peerRequestMiBPerWrite":     float64(reader.bytes) / n / (1 << 20),
			"writerCost":                 scaleP01WriteMetrics(writer, writes),
			"peerRequestCost":            scaleP01WriteMetrics(reader, writes),
		}
	}
	// Both replicas write the same team in turn: each write follows the
	// other replica's, so both decode.
	var alternating scaleP01WriteCost
	for index := range writes {
		base, client := second, adminB
		if index%2 == 1 {
			base, client = first, adminA
		}
		cost := scaleP01MeasureWrite(t, peers, func() {
			authRequest[domain.Team](t, client, http.MethodPatch, base+teamsURL+"/"+team.ID, map[string]any{"name": fmt.Sprintf("Write Load Alt %d", index)}, "", http.StatusOK)
		})
		alternating.add(cost)
	}
	report["team name alternating A/B"] = map[string]any{"writes": writes, "decodesPerWrite": float64(alternating.decodes) / float64(writes), "MiBPerWrite": float64(alternating.bytes) / float64(writes) / (1 << 20), "cost": scaleP01WriteMetrics(alternating, writes)}
	scaleP01SaveReport(t, report)
}
