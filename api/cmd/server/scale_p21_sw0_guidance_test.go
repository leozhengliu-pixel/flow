package main

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestScaleP21SW0GuidanceProperties(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		reset := func() {
			t.Helper()
			repo.SetRealtimeSink(nil)
			f.probe.SetRealtimeSink(nil)
			if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw0.guidance", "", nil, func(data *domain.Bootstrap) error {
				data.WorkspaceSettings.AgentInstructions = "before"
				data.WorkspaceSettings.AgentInstructionsEditedByID = ""
				data.WorkspaceSettings.AgentInstructionsEditedAt = nil
				data.WorkspaceSettings.WelcomeMessage = strings.Repeat("welcome ", 18000)
				data.WorkspaceSettings.TrustedSourcesAllowlist = []string{strings.Repeat("source ", 21000)}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, instructions := range []string{"before", " after "} {
				var outcomes []any
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					reset()
					var realtime []domain.RealtimeEvent
					source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
						realtime = append(realtime, event)
					})
					w := httptest.NewRecorder()
					scaleP21M3Call(&server{store: source}, w, f.request(user, key, map[string]string{"instructions": instructions}), (*server).updateWorkspaceAgentGuidance)
					if w.Code != http.StatusOK && w.Code != http.StatusForbidden {
						t.Fatalf("guidance status=%d body=%s", w.Code, w.Body.String())
					}
					data, ok := repo.WorkspaceMetadata(workspace)
					if !source.BoundedMetadataEnabled() && !ok {
						t.Fatal("missing legacy workspace")
					}
					if source.BoundedMetadataEnabled() {
						if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
							t.Fatal(err)
						}
						data, ok = repo.WorkspaceMetadata(workspace)
					}
					if !ok || data.WorkspaceSettings.WelcomeMessage != strings.Repeat("welcome ", 18000) ||
						!reflect.DeepEqual(data.WorkspaceSettings.TrustedSourcesAllowlist, []string{strings.Repeat("source ", 21000)}) {
						t.Fatal("guidance changed unrelated large properties")
					}
					data.WorkspaceSettings.AgentInstructionsEditedAt = nil
					events := []any{}
					for _, event := range realtime {
						events = append(events, []any{event.Type, event.AggregateID, event.ActorID, event.Payload})
					}
					outcomes = append(outcomes, []any{w.Code, scaleP21M3JSON(t, w.Body.Bytes()), data.WorkspaceSettings, events})
				}
				if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
					t.Fatalf("guidance property differential: %s", scaleP21M2Diff(outcomes[0], outcomes[1]))
				}
			}
		})
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			reset()
			service := &server{store: source}
			// Reset is outside measurement; unchanged guidance publishes nothing.
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			for range 5 {
				w := httptest.NewRecorder()
				service.updateWorkspaceAgentGuidance(w, f.request(f.data.Viewer, nil, map[string]string{"instructions": "before"}))
				if w.Code != http.StatusOK {
					t.Fatal(w.Code, w.Body.String())
				}
			}
			runtime.ReadMemStats(&after)
			t.Logf("bounded=%t large-properties guidance no-op bytes/op=%d allocs/op=%d", source.BoundedMetadataEnabled(), (after.TotalAlloc-before.TotalAlloc)/5, (after.Mallocs-before.Mallocs)/5)
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("guidance decoded full metadata")
		}
	}
}
