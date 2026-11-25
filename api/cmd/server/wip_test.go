package main

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func scaleP21SW1Error(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func TestScaleP21SW1SelectedConnectorFailuresDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	var calls atomic.Int32
	var failing atomic.Bool
	upstream := fakeRuntimeConnector(t, &calls, &failing)
	broken := newCountingMCP(t, "broken", countingMCPMode{status: http.StatusServiceUnavailable})
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace := f.data.Workspace.URLKey
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.seed", "", nil, func(data *domain.Bootstrap) error {
			data.Settings["applicationPolicies"] = []applicationPolicy{
				{ID: "sw1-shared", Name: "Shared", Kind: "mcp", Status: "approved", Shared: true, URL: upstream.URL},
				{ID: "sw1-rejected", Name: "Rejected", Kind: "mcp", Status: "rejected", Shared: true, URL: upstream.URL},
				{ID: "sw1-private", Name: "Private", Kind: "mcp", Status: "approved", OwnerID: "another-user", URL: upstream.URL},
				{ID: "sw1-broken", Name: "Broken", Kind: "mcp", Status: "approved", Shared: true, URL: broken.URL},
			}
			data.Projects = append(data.Projects, domain.Project{ID: "sw1-unrelated", Description: strings.Repeat("unrelated ", 40000)})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		loopID, runID, workerID := idgen.New("sw1-loop-"), idgen.New("sw1-run-"), idgen.New("sw1-worker-")
		initial := domain.LoopRun{ID: runID, LoopID: loopID, Status: "running", StartedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
		if err := repo.CreateLoopRun(t.Context(), workspace, initial, store.LoopRunLease{Owner: workerLoopLeaseOwner(workerID), ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		grant := &agentRunGrant{
			run: store.AgentRun{ID: workerID, WorkspaceKey: workspace, SessionID: runID, Kind: store.AgentRunKindLoop},
			principal: agentRunPrincipal{
				UserID:     f.data.Viewer.ID,
				Connectors: []string{"sw1-shared", "sw1-missing", "sw1-rejected", "sw1-private", "sw1-broken"},
			},
		}
		for _, enabled := range []bool{true, false} {
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.switch", "", nil, func(data *domain.Bootstrap) error {
				data.WorkspaceSettings.MCPConnectors = &enabled
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			// Each phase compares the same installed preimage; this view
			// does not automatically observe a separate owner's full write.
			if err := probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
				t.Fatal(err)
			}
			for _, policy := range []string{"partial", "required"} {
				t.Setenv("FLOW_LOOP_CONNECTOR_POLICY", policy)
				type result struct {
					tools   []connectorTool
					err     string
					notices []string
					run     domain.LoopRun
					events  []store.LoopRunEvent
				}
				var results []result
				for _, source := range []*store.SQLiteStore{repo, probe} {
					s := &server{store: source, authDisabled: true}
					s.agent.ToolsEnabled, s.agent.WriteTools = true, true
					if err := source.ReplaceAgentRunState(t.Context(), grant.run, agentRunNoticesKey, []byte("null")); err != nil {
						t.Fatal(err)
					}
					if _, err := repo.UpdateLoopRun(t.Context(), workspace, runID, nil, func(run *domain.LoopRun) error {
						*run = initial
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					prior, err := repo.LoopRunEvents(t.Context(), workspace, runID, 0)
					if err != nil {
						t.Fatal(err)
					}
					seq := int64(0)
					if len(prior) > 0 {
						seq = prior[len(prior)-1].Seq
					}
					tools, discoveryErr := s.discoverAgentRunConnectors(t.Context(), grant)
					again, againErr := s.discoverAgentRunConnectors(t.Context(), grant)
					if !reflect.DeepEqual(tools, again) || scaleP21SW1Error(discoveryErr) != scaleP21SW1Error(againErr) {
						t.Fatal("repeated discovery changed its result")
					}
					got := result{tools: tools, err: scaleP21SW1Error(discoveryErr), notices: s.agentRunNotices(t.Context(), workerID)}
					got.run, err = repo.LoopRun(t.Context(), workspace, runID)
					if err != nil {
						t.Fatal(err)
					}
					got.events, err = repo.LoopRunEvents(t.Context(), workspace, runID, seq)
					if err != nil {
						t.Fatal(err)
					}
					for i := range got.events {
						got.events[i].Seq, got.events[i].CreatedAt = 0, time.Time{}
					}
					if policy == "required" {
						if got.err == "" || len(got.notices) != 0 || len(got.events) != 0 {
							t.Fatalf("required discovery must fail without partial notices: %#v", got)
						}
					} else {
						wantTools, wantNotices := 0, 5
						if enabled {
							wantTools, wantNotices = 1, 4
						}
						if got.err != "" || len(got.tools) != wantTools || len(got.notices) != wantNotices || len(got.events) != wantNotices || !reflect.DeepEqual(got.notices, got.run.Notices) {
							t.Fatalf("partial discovery enabled=%v result=%#v", enabled, got)
						}
						text := strings.Join(got.notices, "\n")
						for _, message := range []string{"sw1-missing: the connection no longer exists", "Rejected: the connection is not available", "Private: the connection is not available", "Broken:"} {
							if !strings.Contains(text, message) {
								t.Fatalf("missing selected-connector notice %q: %s", message, text)
							}
						}
					}
					results = append(results, got)
				}
				if !reflect.DeepEqual(results[0], results[1]) {
					t.Fatalf("selected discovery enabled=%v policy=%s differs: %#v / %#v", enabled, policy, results[0], results[1])
				}
			}
		}
		if probe.WorkspaceMetadataDecodes() != 0 || calls.Load() != 0 {
			t.Fatal("discovery decoded full metadata or executed connector tools")
		}
	}
}

func TestScaleP21SW1LoopReplyAndTimeoutDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace := f.data.Workspace.URLKey
		loopID, runID, replyID, workerID := idgen.New("sw1-loop-"), idgen.New("sw1-run-"), idgen.New("sw1-reply-"), idgen.New("sw1-worker-")
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.seed", "", nil, func(data *domain.Bootstrap) error {
			data.Loops = append(data.Loops, domain.Loop{ID: loopID, OwnerID: f.data.Viewer.ID, Instructions: strings.Repeat("unused body ", 40000)})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		when := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		initial := domain.LoopRun{ID: runID, LoopID: loopID, Status: "completed", StartedAt: when, Output: "Original output", Replies: []domain.LoopRunReply{{ID: replyID, UserID: f.data.Viewer.ID, Body: "What is the status?", Status: "running", Output: " Reply output ", CreatedAt: when}}}
		if err := repo.CreateLoopRun(t.Context(), workspace, initial, store.LoopRunLease{Owner: workerLoopLeaseOwner(workerID), ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		for _, spec := range []struct {
			name, status, wantStatus, wantReason string
			state                                workerLoopState
			timeout                              bool
			reply                                bool
		}{
			{name: "reply-complete", status: store.AgentRunDone, wantStatus: "completed", state: workerLoopState{Finished: true}, reply: true},
			{name: "reply-cancel", status: store.AgentRunCanceled, wantStatus: "cancelled", wantReason: "cancelled", reply: true},
			{name: "reply-interrupt", status: store.AgentRunInterrupted, wantStatus: "interrupted", wantReason: "interrupted", reply: true},
			{name: "reply-timeout", status: store.AgentRunFailed, wantStatus: "failed", wantReason: "timeout", timeout: true, reply: true},
			{name: "reply-persisted-failure", status: store.AgentRunDone, wantStatus: "failed", wantReason: "budget_exhausted", state: workerLoopState{FailReason: "budget_exhausted", FailMessage: "Owned failure"}, reply: true},
			{name: "run-timeout", status: store.AgentRunFailed, wantStatus: "failed", wantReason: "timeout", timeout: true},
			{name: "run-persisted-failure", status: store.AgentRunDone, wantStatus: "failed", wantReason: "budget_exhausted", state: workerLoopState{FailReason: "budget_exhausted", FailMessage: "Owned failure"}},
		} {
			t.Run(spec.name, func(t *testing.T) {
				type result struct {
					run    domain.LoopRun
					events []store.LoopRunEvent
				}
				var results []result
				for _, source := range []*store.SQLiteStore{repo, probe} {
					reset := initial
					reset.Replies = append([]domain.LoopRunReply(nil), initial.Replies...)
					if !spec.reply {
						reset.Status, reset.Replies = "running", nil
					}
					if _, err := repo.UpdateLoopRun(t.Context(), workspace, runID, nil, func(run *domain.LoopRun) error {
						*run = reset
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					run := store.AgentRun{ID: workerID, WorkspaceKey: workspace, SessionID: runID, Kind: store.AgentRunKindLoop}
					raw, err := json.Marshal(spec.state)
					if err != nil {
						t.Fatal(err)
					}
					if err := source.ReplaceAgentRunState(t.Context(), run, workerLoopStateKey, raw); err != nil {
						t.Fatal(err)
					}
					info := workerLoopRunInfo{LoopID: loopID, LoopRunID: runID}
					if spec.reply {
						info.ReplyID = replyID
					}
					if spec.timeout {
						info.Started, info.TimeoutMS = time.Now().Add(-time.Hour).UnixMilli(), 1
					}
					prior, err := repo.LoopRunEvents(t.Context(), workspace, runID, 0)
					if err != nil {
						t.Fatal(err)
					}
					seq := int64(0)
					if len(prior) > 0 {
						seq = prior[len(prior)-1].Seq
					}
					s := &server{store: source}
					ctx, cancel := context.WithCancel(t.Context())
					cancel()
					for range 2 {
						s.finishWorkerLoopRun(ctx, run, info, spec.status, "provider failed")
					}
					got := result{}
					got.run, err = repo.LoopRun(t.Context(), workspace, runID)
					if err != nil {
						t.Fatal(err)
					}
					status, reason := got.run.Status, got.run.FailureReason
					got.run.FinishedAt = nil
					if spec.reply {
						reply := &got.run.Replies[0]
						status, reason = reply.Status, reply.FailureReason
						if got.run.Status != "completed" || got.run.Output != initial.Output || reply.Output != "Reply output" {
							t.Fatalf("reply finalization changed parent or output: %#v", got.run)
						}
						reply.FinishedAt = nil
					}
					if status != spec.wantStatus || reason != spec.wantReason {
						t.Fatalf("status/reason=%s/%s want=%s/%s", status, reason, spec.wantStatus, spec.wantReason)
					}
					got.events, err = repo.LoopRunEvents(t.Context(), workspace, runID, seq)
					if err != nil {
						t.Fatal(err)
					}
					terminal := "finished"
					if spec.reply {
						terminal = "reply_finished"
					}
					if len(got.events) != 2 || got.events[0].Kind != "timing" || got.events[1].Kind != terminal {
						t.Fatalf("repeated finalization event effects: %#v", got.events)
					}
					for i := range got.events {
						got.events[i].Seq, got.events[i].CreatedAt = 0, time.Time{}
					}
					results = append(results, got)
				}
				if !reflect.DeepEqual(results[0], results[1]) {
					t.Fatalf("finalization differs: %#v / %#v", results[0], results[1])
				}
			})
		}
		if probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("loop finalization decoded complete workspace")
		}
	}
}
