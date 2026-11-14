package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW24WorkerDefinitionDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, targetID, trustedID := f.data.Workspace.URLKey, idgen.New("sw24-worker-loop-"), idgen.New("sw24-trusted-loop-")
		now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		target := domain.Loop{
			ID: targetID, Name: "Exact worker input", Status: "published",
			OwnerID: f.data.Viewer.ID, Creator: f.data.Viewer, Enabled: true,
			Instructions: "Inspect the selected records and report.",
			InstructionsData: map[string]any{"type": "doc", "content": []any{
				map[string]any{"type": "paragraph", "content": []any{
					map[string]any{"type": "text", "text": "Complete input"},
				}},
			}},
			Level: "team", TeamID: "hidden-team-reference",
			TeamAccess: "selected", ConnectorIDs: []string{"hidden-connector-reference"},
			TriggerType: "schedule", TriggerConfig: map[string]any{"timezone": "UTC"},
			TrustedSourceKeys: []string{}, ExpectedOutputs: []string{"none"},
			OutputContract: &domain.LoopOutputContract{
				Source: "person", ReadOnly: true, UpdatedBy: f.data.Viewer.ID,
				Outputs: []domain.LoopContractOutput{},
			},
			CreatedAt: now, UpdatedAt: now,
		}
		trusted := target
		trusted.ID, trusted.Name = trustedID, "Trusted worker input"
		trusted.TrustedSourceKeys = []string{"integration:slack", "appUser:bound"}
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "loops"), workspace, "scale_p21_sw24.worker_seed", "", nil, func(data *domain.Bootstrap) error {
			data.Loops = []domain.Loop{target, trusted}
			for range 70 {
				data.Loops = append(data.Loops, domain.Loop{ID: idgen.New("sw24-worker-unrelated-"), Name: "Unrelated"})
			}
			data.Loops[2].Instructions = strings.Repeat("legal unrelated loop body ", 18000)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		foreign, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "SW24 worker foreign", idgen.New("sw24-worker-foreign-"), "us")
		if err != nil {
			t.Fatal(err)
		}
		f.resources.workspaces = append(f.resources.workspaces, foreign.Workspace.URLKey)
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		before := scaleP21M3Fingerprint(t, repo, workspace, "loops")
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			ctx := f.request(user, key, nil).Context()
			for _, ref := range []string{
				targetID, trustedID, "", targetID + "-missing", domain.LoopSlugID(target),
				"old-name-" + domain.DerivedSlugSuffix(targetID),
				strings.Repeat("x", 192), targetID + "\x00",
			} {
				var results []*domain.Loop
				for _, source := range []*store.SQLiteStore{repo, probe} {
					got, exists, err := (&server{store: source}).workerLoopDefinition(ctx, workspace, ref)
					if err != nil || !exists {
						t.Fatalf("definition(%q): exists=%v err=%v", ref, exists, err)
					}
					if (got != nil) != (ref == targetID || ref == trustedID) {
						t.Fatalf("definition(%q) changed exact-ID selection", ref)
					}
					results = append(results, got)
				}
				leftJSON, leftErr := json.Marshal(results[0])
				rightJSON, rightErr := json.Marshal(results[1])
				if leftErr != nil || rightErr != nil || !bytes.Equal(leftJSON, rightJSON) {
					t.Fatalf("owned Loop semantic JSON changed for %q: %s", ref, scaleP21M2Diff(results[0], results[1]))
				}
				if results[0] != nil {
					left, right := reflect.ValueOf(*results[0]), reflect.ValueOf(*results[1])
					for index := range left.NumField() {
						if !reflect.DeepEqual(left.Field(index).Interface(), right.Field(index).Interface()) {
							if left.Type().Field(index).Name != "TrustedSourceKeys" ||
								len(results[0].TrustedSourceKeys) != 0 || len(results[1].TrustedSourceKeys) != 0 {
								t.Fatalf("consumed input field %s changed: legacy=%#v bounded=%#v", left.Type().Field(index).Name, left.Field(index).Interface(), right.Field(index).Interface())
							}
						}
					}
					if ref == targetID && (results[0].TrustedSourceKeys == nil || results[1].TrustedSourceKeys != nil) {
						t.Fatal("warm empty and canonical nil trusted-source representations were not exercised")
					}
					for _, enabled := range []bool{false, true} {
						for _, mode := range []string{"", "allowlist", "other"} {
							for _, source := range []string{"", "integration:slack", "appUser:bound", "integration:other"} {
								settings := domain.WorkspaceSettings{ExternalLoopTriggers: enabled, TrustedSourcesMode: mode, TrustedSourcesAllowlist: []string{"integration:other"}}
								want := enabled && (ref == trustedID && (source == "integration:slack" || source == "appUser:bound") || mode == "allowlist" && source == "integration:other")
								for _, input := range results {
									if got := loopSourceTrusted(settings, *input, source); got != want {
										t.Fatalf("trusted-source decision changed: ref=%q enabled=%v mode=%q source=%q got=%v want=%v", ref, enabled, mode, source, got, want)
									}
								}
							}
						}
					}
				}
				if ref == targetID {
					for index, source := range []*store.SQLiteStore{repo, probe} {
						results[index].ConnectorIDs[0] = "changed-return"
						results[index].InstructionsData["type"] = "changed-return"
						results[index].OutputContract.Source = "changed-return"
						got, _, err := (&server{store: source}).workerLoopDefinition(ctx, workspace, targetID)
						if err != nil || got.ConnectorIDs[0] != target.ConnectorIDs[0] ||
							got.InstructionsData["type"] != "doc" || got.OutputContract.Source != "person" {
							t.Fatal("returned selected input retained storage ownership", err)
						}
					}
				}
			}
		})
		for _, source := range []*store.SQLiteStore{repo, probe} {
			item, exists, err := (&server{store: source}).workerLoopDefinition(t.Context(), workspace+"-missing", targetID)
			if err != nil || exists || item != nil {
				t.Fatalf("missing workspace resolved owned loop: exists=%v err=%v", exists, err)
			}
			item, exists, err = (&server{store: source}).workerLoopDefinition(t.Context(), foreign.Workspace.URLKey, targetID)
			if err != nil || !exists || item != nil {
				t.Fatalf("foreign workspace resolved owned loop: exists=%v err=%v", exists, err)
			}
		}
		if before != scaleP21M3Fingerprint(t, repo, workspace, "loops") || probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("point input changed canonical bytes or decoded complete metadata")
		}
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		if _, _, err := (&server{store: probe}).workerLoopDefinition(cancelled, workspace, targetID); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled definition read: %v", err)
		}
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "loops"), workspace, "scale_p21_sw24.worker_large", "", nil, func(data *domain.Bootstrap) error {
			data.Loops[0].Instructions = strings.Repeat("legal selected loop body ", 18000)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		before = scaleP21M3Fingerprint(t, repo, workspace, "loops")
		if item, _, err := (&server{store: repo}).workerLoopDefinition(t.Context(), workspace, targetID); err != nil || item == nil || len(item.Instructions) < 256<<10 {
			t.Fatal("legacy selected input was truncated", err)
		}
		if _, _, err := (&server{store: probe}).workerLoopDefinition(t.Context(), workspace, targetID); !errors.Is(err, store.ErrMetadataPageBudget) {
			t.Fatalf("oversized selected input did not fail explicitly: %v", err)
		}
		if item, exists, err := (&server{store: probe}).workerLoopDefinition(t.Context(), workspace, targetID+"-missing"); err != nil || !exists || item != nil {
			t.Fatalf("selected budget leaked work admission: exists=%v err=%v", exists, err)
		}
		if before != scaleP21M3Fingerprint(t, repo, workspace, "loops") || probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("failed point input changed canonical bytes or decoded complete metadata")
		}
	}
}

func TestScaleP21SW24WorkerContextPreconditions(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace, loopID := f.data.Workspace.URLKey, idgen.New("sw24-precondition-loop-")
		ownerID := idgen.New("sw24-missing-owner-")
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "loops"), workspace, "scale_p21_sw24.worker_preconditions", "", nil, func(data *domain.Bootstrap) error {
			data.Loops = append(data.Loops, domain.Loop{ID: loopID, OwnerID: ownerID, Name: "Preconditions"})
			data.Loops = append(data.Loops, domain.Loop{ID: idgen.New("sw24-large-loop-"), Instructions: strings.Repeat("unrelated ", 50000)})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		before := scaleP21M3Fingerprint(t, repo, workspace, "loops")
		for _, spec := range []struct {
			name, status, replyStatus, want       string
			missingRun, missingLoop, missingReply bool
		}{
			{name: "missing-run", missingRun: true},
			{name: "completed-parent", status: "completed", want: "The loop run is no longer running"},
			{name: "cancelled-parent", status: "cancelled", want: "The loop run is no longer running"},
			{name: "missing-reply", status: "running", missingReply: true},
			{name: "terminal-reply", status: "running", replyStatus: "completed", want: "The loop run is no longer running"},
			{name: "missing-loop", status: "running", missingLoop: true, want: "The loop no longer exists"},
			{name: "owner-check-after-point-read", status: "running", want: "the loop owner is no longer in this workspace"},
		} {
			t.Run(spec.name, func(t *testing.T) {
				if spec.missingRun || spec.missingReply || spec.status != "running" || spec.replyStatus != "" {
					setInstructions := func(ctx context.Context, instructions string) error {
						return repo.MutateWorkspace(store.WithMetadataFields(ctx, "loops"), workspace, "scale_p21_sw24.worker_order", "", nil, func(data *domain.Bootstrap) error {
							for index := range data.Loops {
								if data.Loops[index].ID == loopID {
									data.Loops[index].Instructions = instructions
								}
							}
							return nil
						})
					}
					if err := setInstructions(t.Context(), strings.Repeat("unused selected input ", 20000)); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() {
						ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
						defer cancel()
						if err := setInstructions(ctx, ""); err != nil {
							t.Error(err)
						}
					})
				}
				runID, workerID := idgen.New("sw24-precondition-run-"), idgen.New("sw24-precondition-worker-")
				initial := domain.LoopRun{ID: runID, LoopID: loopID, Status: spec.status, StartedAt: time.Now().UTC()}
				info := workerLoopRunInfo{LoopRunID: runID, LoopID: loopID}
				if spec.missingLoop {
					info.LoopID += "-missing"
				}
				if spec.missingReply || spec.replyStatus != "" {
					info.ReplyID = idgen.New("sw24-precondition-reply-")
				}
				if spec.replyStatus != "" {
					initial.Replies = []domain.LoopRunReply{{ID: info.ReplyID, Status: spec.replyStatus}}
				}
				if !spec.missingRun {
					if err := repo.CreateLoopRun(t.Context(), workspace, initial, store.LoopRunLease{Owner: workerLoopLeaseOwner(workerID), ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
						t.Fatal(err)
					}
					var err error
					initial, err = repo.LoopRun(t.Context(), workspace, runID)
					if err != nil {
						t.Fatal(err)
					}
				}
				var results []string
				for _, source := range []*store.SQLiteStore{repo, probe} {
					s := &server{store: source}
					run := store.AgentRun{ID: workerID, WorkspaceKey: workspace, SessionID: runID, Kind: store.AgentRunKindLoop}
					got, err := s.workerLoopContext(t.Context(), run, info)
					if err == nil || got != nil {
						t.Fatal("worker precondition unexpectedly prepared a context")
					}
					results = append(results, err.Error())
					if spec.want != "" && err.Error() != spec.want {
						t.Fatalf("precondition error=%q want=%q", err, spec.want)
					}
					if _, _, found, err := repo.AgentRunState(t.Context(), workerID, workerLoopStateKey); err != nil || found {
						t.Fatal("read precondition wrote worker state", err)
					}
				}
				if results[0] != results[1] {
					t.Fatalf("worker precondition changed: %q/%q", results[0], results[1])
				}
				if !spec.missingRun {
					got, err := repo.LoopRun(t.Context(), workspace, runID)
					if err != nil || !reflect.DeepEqual(got, initial) {
						t.Fatalf("worker precondition changed native run: %+v err=%v", got, err)
					}
					events, err := repo.LoopRunEvents(t.Context(), workspace, runID, 0)
					if err != nil || len(events) != 0 {
						t.Fatal("worker precondition emitted events", err)
					}
				}
			})
		}
		if before != scaleP21M3Fingerprint(t, repo, workspace, "loops") || probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("worker preconditions changed canonical loops or decoded complete metadata")
		}
	}
}
