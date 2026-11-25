package main

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21M1LoopFinishDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace, loopID, runID := f.data.Workspace.URLKey, idgen.New("m1-loop-"), idgen.New("m1-run-")
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.seed", "", nil, func(data *domain.Bootstrap) error {
			data.Loops = append(data.Loops, domain.Loop{ID: loopID, OwnerID: f.data.Viewer.ID, Name: "Finish", Status: "published"})
			for i := range 55 {
				instructions := "Unrelated"
				if i == 0 {
					instructions = strings.Repeat("unrelated ", 30000)
				}
				data.Loops = append(data.Loops, domain.Loop{ID: idgen.New("m1-unrelated-"), Instructions: instructions})
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		initial := domain.LoopRun{ID: runID, LoopID: loopID, Status: "running", StartedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
		if err := repo.CreateLoopRun(t.Context(), workspace, initial, store.LoopRunLease{Owner: workerLoopLeaseOwner("m1-worker"), ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, status := range []string{store.AgentRunFailed, store.AgentRunCanceled, store.AgentRunInterrupted, store.AgentRunDone} {
			var results []domain.LoopRun
			var eventResults [][]store.LoopRunEvent
			for _, source := range []*store.SQLiteStore{repo, probe} {
				if _, err := repo.UpdateLoopRun(t.Context(), workspace, runID, nil, func(run *domain.LoopRun) error {
					*run = initial
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				s := &server{store: source}
				run := store.AgentRun{ID: "m1-worker", WorkspaceKey: workspace, SessionID: runID, Kind: store.AgentRunKindLoop}
				previous, err := repo.LoopRunEvents(t.Context(), workspace, runID, 0)
				if err != nil {
					t.Fatal(err)
				}
				seq := int64(0)
				if len(previous) > 0 {
					seq = previous[len(previous)-1].Seq
				}
				// Finalization deliberately outlives a cancelled request.
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				s.finishWorkerLoopRun(ctx, run, workerLoopRunInfo{LoopID: loopID, LoopRunID: runID}, status, "provider failed")
				s.finishWorkerLoopRun(ctx, run, workerLoopRunInfo{LoopID: loopID, LoopRunID: runID}, status, "provider failed")
				result, err := repo.LoopRun(t.Context(), workspace, runID)
				if err != nil || result.Status == "running" {
					t.Fatalf("worker finalization failed: %#v err=%v", result, err)
				}
				result.FinishedAt = nil
				results = append(results, result)
				events, err := repo.LoopRunEvents(t.Context(), workspace, runID, seq)
				if err != nil || len(events) < 2 || events[len(events)-1].Kind != "finished" {
					t.Fatalf("loop finish events=%d err=%v", len(events), err)
				}
				finished, timings := 0, 0
				for i := range events {
					if events[i].Kind == "finished" {
						finished++
					}
					if events[i].Kind == "timing" {
						timings++
					}
					events[i].Seq, events[i].CreatedAt = 0, time.Time{}
				}
				if finished != 1 || timings != 1 {
					t.Fatal("repeated finalization duplicated terminal events")
				}
				eventResults = append(eventResults, events)
			}
			if !reflect.DeepEqual(results[0], results[1]) || !reflect.DeepEqual(eventResults[0], eventResults[1]) {
				t.Fatalf("worker finalization changed: %#v/%#v", results[0], results[1])
			}
		}
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.large_target", "", nil, func(data *domain.Bootstrap) error {
			for i := range data.Loops {
				if data.Loops[i].ID == loopID {
					data.Loops[i].Instructions = strings.Repeat("large target ", 30000)
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.UpdateLoopRun(t.Context(), workspace, runID, nil, func(run *domain.LoopRun) error { *run = initial; return nil }); err != nil {
			t.Fatal(err)
		}
		(&server{store: probe}).finishWorkerLoopRun(t.Context(), store.AgentRun{ID: "m1-worker", WorkspaceKey: workspace, SessionID: runID}, workerLoopRunInfo{LoopID: loopID, LoopRunID: runID}, store.AgentRunFailed, "provider failed")
		if result, err := repo.LoopRun(t.Context(), workspace, runID); err != nil || result.Status == "running" {
			t.Fatal("finalization unnecessarily loaded the oversized loop body")
		}
		if probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("worker finalization decoded complete metadata")
		}
	}
}
