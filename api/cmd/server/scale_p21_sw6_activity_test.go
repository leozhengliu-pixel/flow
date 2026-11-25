package main

import (
	"context"
	"crypto/sha256"
	"io"
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

func scaleP21SW6RecordHash(t *testing.T, repo *store.SQLiteStore, workspace, kind, id string) [32]byte {
	t.Helper()
	hash := sha256.New()
	found := false
	err := repo.StreamMetadataRecords(t.Context(), workspace, kind, func(key store.RecordKey, reader io.Reader) error {
		if key.ID != id {
			return nil
		}
		found = true
		_, err := io.Copy(hash, reader)
		return err
	})
	if err != nil || !found {
		t.Fatalf("read owned %s/%s: found=%v err=%v", kind, id, found, err)
	}
	return [32]byte(hash.Sum(nil))
}

func TestScaleP21SW6AgentActivityDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		targetID, otherID, foreignID := idgen.New("sw6-activity-"), idgen.New("sw6-other-"), idgen.New("sw6-foreign-")
		now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		target := domain.AgentActivity{
			ID: targetID, SessionID: idgen.New("sw6-session-"), ActorID: f.data.Viewer.ID,
			Type: "tool", Status: "running", Body: "Before",
			Metadata:  map[string]any{"reference": "retained-private-reference", "count": 7},
			CreatedAt: now, UpdatedAt: now,
		}
		unrelated := domain.AgentActivity{
			ID: otherID, SessionID: idgen.New("sw6-other-session-"), Type: "thought", Status: "running",
			Body: strings.Repeat("oversized unrelated activity ", 15000), CreatedAt: now, UpdatedAt: now,
		}
		activities := []domain.AgentActivity{target, unrelated}
		for range 51 {
			activities = append(activities, domain.AgentActivity{ID: idgen.New("sw6-retained-"), SessionID: "retained", Type: "thought", Status: "completed", CreatedAt: now, UpdatedAt: now})
		}
		reset := func() {
			t.Helper()
			repo.SetWebhookSink(nil)
			repo.SetRealtimeSink(nil)
			f.probe.SetWebhookSink(nil)
			f.probe.SetRealtimeSink(nil)
			if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "agentActivities"), workspace, "agent.activity_updated", targetID, nil, func(data *domain.Bootstrap) error {
				data.AgentActivities = activities
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		reset()
		otherHash := scaleP21SW6RecordHash(t, repo, workspace, "agentActivities", otherID)
		projectHash := scaleP21SW6RecordHash(t, repo, workspace, "projects", "m2-unrelated")

		foreign, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "SW6 other workspace", idgen.New("sw6-workspace-"), "us")
		if err != nil {
			t.Fatal(err)
		}
		f.resources.workspaces = append(f.resources.workspaces, foreign.Workspace.URLKey)
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "agentActivities"), foreign.Workspace.URLKey, "agent.activity_created", foreignID, nil, func(data *domain.Bootstrap) error {
			data.AgentActivities = []domain.AgentActivity{{ID: foreignID, SessionID: "other", Body: "Foreign", CreatedAt: now, UpdatedAt: now}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		foreignHash := scaleP21SW6RecordHash(t, repo, foreign.Workspace.URLKey, "agentActivities", foreignID)

		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, spec := range []struct {
				name, id string
				body     any
				cancel   bool
				status   int
				events   int
			}{
				{"changed", targetID, map[string]any{"status": "completed", "body": "After", "metadata": map[string]any{"reference": "new-private-reference", "count": 11}}, false, http.StatusOK, 1},
				{"noop", targetID, map[string]any{"status": target.Status, "body": target.Body}, false, http.StatusOK, 0},
				{"missing", targetID + "-old-slug", map[string]any{"body": "No target"}, false, http.StatusNotFound, 0},
				{"cross-workspace", foreignID, map[string]any{"body": "No foreign write"}, false, http.StatusNotFound, 0},
				{"cancelled", targetID, map[string]any{"body": "Cancelled"}, true, http.StatusInternalServerError, 0},
			} {
				t.Run(spec.name, func(t *testing.T) {
					type result struct {
						Response any
						Rows     any
						Events   any
					}
					var results []result
					for _, source := range []*store.SQLiteStore{repo, f.probe} {
						reset()
						before := scaleP21M2Canonical(t, repo, workspace, store.RecordKey{Kind: "agentActivities", ID: targetID})
						var events []domain.DomainEvent
						var realtime []domain.RealtimeEvent
						source.SetWebhookSink(func(_ string, event domain.DomainEvent) { events = append(events, event) })
						source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { realtime = append(realtime, event) })
						r := f.request(user, key, spec.body)
						r.SetPathValue("id", spec.id)
						r = r.WithContext(store.ContextWithRealtimeClient(r.Context(), "sw6-client"))
						if spec.cancel {
							ctx, cancel := context.WithCancel(r.Context())
							cancel()
							r = r.WithContext(ctx)
						}
						w := httptest.NewRecorder()
						(&server{store: source}).updateAgentActivity(w, r)
						if w.Code != spec.status || len(events) != spec.events || len(realtime) != spec.events {
							t.Fatalf("status=%d events=%d/%d; want=%d events=%d body=%s", w.Code, len(events), len(realtime), spec.status, spec.events, w.Body.String())
						}
						rows := scaleP21M2Canonical(t, repo, workspace,
							store.RecordKey{Kind: "agentActivities", ID: targetID},
							store.RecordKey{Kind: "users", ID: user.ID})
						if spec.events == 0 {
							after := scaleP21M2Canonical(t, repo, workspace, store.RecordKey{Kind: "agentActivities", ID: targetID})
							if !reflect.DeepEqual(before, after) {
								t.Fatal("failed or no-op update changed canonical activity")
							}
						}
						effects := []any{}
						for _, event := range events {
							effects = append(effects, []any{event.Type, event.AggregateID, event.Payload, event.PreviousValues})
						}
						for _, event := range realtime {
							effects = append(effects, []any{event.Type, event.AggregateID, event.ActorID, event.ClientID, event.Payload})
						}
						results = append(results, result{
							Response: []any{w.Code, scaleP21M2Response(t, w, false)},
							Rows:     rows,
							Events:   scaleP21M2JSON(t, effects, false),
						})
						if scaleP21SW6RecordHash(t, repo, workspace, "agentActivities", otherID) != otherHash ||
							scaleP21SW6RecordHash(t, repo, workspace, "projects", "m2-unrelated") != projectHash ||
							scaleP21SW6RecordHash(t, repo, foreign.Workspace.URLKey, "agentActivities", foreignID) != foreignHash {
							t.Fatal("activity update changed unrelated or foreign canonical bytes")
						}
					}
					if !reflect.DeepEqual(results[0], results[1]) {
						t.Fatalf("activity differential: %s", scaleP21M2Diff(results[0], results[1]))
					}
				})
			}
		})

		reset()
		before := scaleP21M2Canonical(t, repo, workspace, store.RecordKey{Kind: "agentActivities", ID: targetID})
		var events, realtime int
		f.probe.SetWebhookSink(func(string, domain.DomainEvent) { events++ })
		f.probe.SetRealtimeSink(func(string, domain.RealtimeEvent) { realtime++ })
		r := f.request(f.data.Viewer, nil, map[string]any{"body": strings.Repeat("large target output ", 20000)})
		r.SetPathValue("id", targetID)
		w := httptest.NewRecorder()
		(&server{store: f.probe}).updateAgentActivity(w, r)
		if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), store.ErrMetadataPageBudget.Error()) || events != 0 || realtime != 0 {
			t.Fatalf("oversized output was not an explicit atomic budget failure: status=%d events=%d/%d body=%s", w.Code, events, realtime, w.Body.String())
		}
		after := scaleP21M2Canonical(t, repo, workspace, store.RecordKey{Kind: "agentActivities", ID: targetID})
		if !reflect.DeepEqual(before, after) {
			t.Fatal("oversized output escaped transaction rollback")
		}
		r = f.request(f.data.Viewer, nil, map[string]any{"body": "After budget failure"})
		r.SetPathValue("id", targetID)
		w = httptest.NewRecorder()
		(&server{store: f.probe}).updateAgentActivity(w, r)
		if w.Code != http.StatusOK || events != 1 || realtime != 1 {
			t.Fatal("budget failure prevented a subsequent bounded activity update")
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("activity update decoded complete workspace metadata")
		}
	}
}
