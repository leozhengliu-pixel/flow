package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW24LoopReadsDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, targetID, runID := f.data.Workspace.URLKey, idgen.New("loop_sw24_"), idgen.New("run_sw24_")
		now := time.Now().UTC()
		target := domain.Loop{
			ID: targetID, Name: "Target", Status: "published", Enabled: true, OwnerID: f.data.Viewer.ID,
			Level: "team", TeamID: "hidden-team-reference", TeamAccess: "selected", ConnectorIDs: []string{"hidden-connector-reference"},
			Instructions: "Read and report.", Creator: f.data.Viewer, CreatedAt: now, UpdatedAt: now,
		}
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "loops"), workspace, "scale_p21_sw24.seed", "", nil, func(data *domain.Bootstrap) error {
			data.Loops = []domain.Loop{target}
			for range 70 {
				data.Loops = append(data.Loops, domain.Loop{ID: idgen.New("sw24-unrelated-loop-"), Name: "Unrelated"})
			}
			data.Loops[1].Instructions = strings.Repeat("unrelated loop body ", 20000)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := repo.CreateLoopRun(t.Context(), workspace, domain.LoopRun{ID: runID, LoopID: targetID, Status: "completed", Trigger: "manual", StartedAt: now}, store.LoopRunLease{}); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.UpdateLoopRun(t.Context(), workspace, runID, []store.LoopRunEvent{
			{Kind: "notice", Data: json.RawMessage(`{"text":"first"}`), CreatedAt: now},
			{Kind: "finished", Data: json.RawMessage(`{"actorId":"hidden-actor","count":37}`), CreatedAt: now},
		}, func(*domain.LoopRun) error { return nil }); err != nil {
			t.Fatal(err)
		}
		before := scaleP21M3Fingerprint(t, repo, workspace, "loops")
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, spec := range []struct {
				name, id, run, query string
				events               bool
				want                 int
			}{
				{name: "loop", id: targetID, want: http.StatusOK},
				{name: "missing-loop", id: targetID + "-missing", want: http.StatusNotFound},
				{name: "slug-is-resolved-by-separate-middleware", id: domain.LoopSlugID(target), want: http.StatusNotFound},
				{name: "long-reference", id: strings.Repeat("x", 192), want: http.StatusNotFound},
				{name: "nul-reference", id: targetID + "\x00", want: http.StatusNotFound},
				{name: "events", id: targetID, run: runID, events: true, want: http.StatusOK},
				{name: "events-after", id: targetID, run: runID, query: "?after=1", events: true, want: http.StatusOK},
				{name: "events-wrong-loop", id: targetID + "-other", run: runID, events: true, want: http.StatusNotFound},
				{name: "events-missing-run", id: targetID, run: runID + "-missing", events: true, want: http.StatusNotFound},
			} {
				t.Run(spec.name, func(t *testing.T) {
					var results []any
					for _, source := range []*store.SQLiteStore{repo, f.probe} {
						r, w := f.request(user, key, nil), httptest.NewRecorder()
						r.URL.RawQuery = strings.TrimPrefix(spec.query, "?")
						r.SetPathValue("id", spec.id)
						r.SetPathValue("runId", spec.run)
						handler := (*server).getLoop
						if spec.events {
							handler = (*server).listLoopRunEvents
						}
						scaleP21M3Call(&server{store: source}, w, r, handler)
						if w.Code != spec.want {
							t.Fatalf("loop read status=%d want=%d body=%s", w.Code, spec.want, w.Body.String())
						}
						if !spec.events && spec.want == http.StatusOK {
							var got domain.Loop
							if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.RunCount30d != 1 ||
								got.TeamID != target.TeamID || !reflect.DeepEqual(got.ConnectorIDs, target.ConnectorIDs) || got.Creator.ID != target.Creator.ID {
								t.Fatal("loop presentation, count or existing references changed", err)
							}
						}
						results = append(results, []any{w.Code, scaleP21M3JSON(t, w.Body.Bytes())})
					}
					if !reflect.DeepEqual(results[0], results[1]) {
						t.Fatalf("loop read differential: %s", scaleP21M2Diff(results[0], results[1]))
					}
				})
			}
		})
		member := f.principals[2].user
		queued := f.request(member, nil, nil)
		queued.SetPathValue("id", targetID)
		queued.SetPathValue("runId", runID)
		if err := repo.SuspendMember(t.Context(), f.data.Workspace.ID, member.ID); err != nil {
			t.Fatal(err)
		}
		for _, phase := range []string{"queued", "replayed"} {
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				for _, handler := range []func(*server, http.ResponseWriter, *http.Request){(*server).getLoop, (*server).listLoopRunEvents} {
					w := httptest.NewRecorder()
					scaleP21M3Call(&server{store: source}, w, queued, handler)
					if w.Code != http.StatusForbidden {
						t.Fatalf("%s read reused revoked membership: %d", phase, w.Code)
					}
				}
			}
		}
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			r, w := f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
			r.Header.Set("X-Workspace-Key", workspace+"-missing")
			r.SetPathValue("workspaceKey", workspace+"-missing")
			(&server{store: source}).listLoopRunEvents(w, r)
			if w.Code != http.StatusNotFound {
				t.Fatal("missing workspace changed")
			}
		}
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		r, w := f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
		r.SetPathValue("id", targetID)
		(&server{store: f.probe}).getLoop(w, r.WithContext(cancelled))
		if w.Body.Len() != 0 {
			t.Fatal("cancelled scoped read released a loop")
		}
		if before != scaleP21M3Fingerprint(t, repo, workspace, "loops") || f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("loop read changed canonical state or decoded complete metadata")
		}
		foreign, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "SW24 loop foreign", idgen.New("sw24-loop-foreign-"), "us")
		if err != nil {
			t.Fatal(err)
		}
		f.resources.workspaces = append(f.resources.workspaces, foreign.Workspace.URLKey)
		f.probe, err = repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			for _, handler := range []func(*server, http.ResponseWriter, *http.Request){(*server).getLoop, (*server).listLoopRunEvents} {
				r, w := f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
				r.Header.Set("X-Workspace-Key", foreign.Workspace.URLKey)
				r.SetPathValue("workspaceKey", foreign.Workspace.URLKey)
				r.SetPathValue("id", targetID)
				r.SetPathValue("runId", runID)
				scaleP21M3Call(&server{store: source}, w, r, handler)
				if w.Code != http.StatusNotFound {
					t.Fatal("loop or run escaped its workspace")
				}
			}
		}
		// An oversized current loop does not enter the event-log identity read.
		if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "loops"), workspace, "scale_p21_sw24.large", "", nil, func(data *domain.Bootstrap) error {
			data.Loops[0].Instructions = strings.Repeat("large target loop ", 20000)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		r, w = f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
		r.SetPathValue("id", targetID)
		(&server{store: f.probe}).getLoop(w, r)
		if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), store.ErrMetadataPageBudget.Error()) {
			t.Fatalf("oversized loop did not return explicit budget error: %d %s", w.Code, w.Body.String())
		}
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			r, w := f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
			r.SetPathValue("id", targetID)
			r.SetPathValue("runId", runID)
			(&server{store: source}).listLoopRunEvents(w, r)
			if w.Code != http.StatusOK {
				t.Fatal("event read fetched unused oversized loop")
			}
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("loop event recovery decoded complete metadata")
		}
	}
}
