package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// SW552: a loop run's tool call that no migrated writer serves (save_label,
// a label.created metadata write) succeeds with bounded metadata on, as it
// does flag off: the loop work context is COLD work, so the write runs its
// literal legacy dispatch on a COLD facade and publishes to the bounded
// image. Both runs finish with the same status, output and tool result; the
// label each wrote is in SQL and in the bounded store's installed image with
// the same fields. Runs are read back through each handler's own
// GET /api/loops/{id}/runs (this harness disables authentication; bounded
// metadata needs record hydration on, as production activation requires).
func TestScaleP21SW552LoopRunColdToolWrite(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	provider := &fakeLoopProvider{}
	upstream := httptest.NewServer(http.HandlerFunc(provider.serve))
	t.Cleanup(upstream.Close)
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	probe, err := repository.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	workspace := repository.Bootstrap().Workspace.URLKey
	agent := appconfig.AgentConfig{Enabled: true, Protocol: "openai-responses", BaseURL: upstream.URL, Model: "flow-test", MaxOutputTokens: 256, ToolsEnabled: true, WriteTools: true}
	service := func(repo *store.SQLiteStore) http.Handler {
		return newHandler(&server{store: repo, uploadPath: t.TempDir(), authDisabled: true, agent: agent, agentClient: upstream.Client()})
	}
	legacy, bounded := service(repository), service(probe)
	loop := requestJSON[domain.Loop](t, legacy, http.MethodPost, "/api/loops", map[string]any{"name": "SW552 labeller", "instructions": "Add the label.", "status": "published"}, http.StatusCreated)
	run := func(handler http.Handler, name string) (domain.LoopRun, domain.IssueLabel) {
		t.Helper()
		provider.mu.Lock()
		provider.replies = []string{
			`tool:save_label {"name":"` + name + `","color":"#ff0000"}`,
			`tool:finish_run {"status":"done","summary":"Labelled."}`,
		}
		before := len(provider.inputs)
		provider.mu.Unlock()
		requestJSON[domain.LoopRun](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs", nil, http.StatusAccepted)
		finished := waitForLoopRun(t, handler, loop.ID)
		provider.mu.Lock()
		result := provider.inputs[len(provider.inputs)-1]
		calls := len(provider.inputs) - before
		provider.mu.Unlock()
		if calls != 2 || !strings.Contains(result, name) {
			t.Fatalf("SW552 %s: the save_label result did not reach the model (%d calls): %s", name, calls, result)
		}
		if err := repository.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
		data, ok := repository.WorkspaceMetadataFields(workspace, "labels")
		index := slices.IndexFunc(data.Labels, func(label domain.IssueLabel) bool { return label.Name == name })
		if !ok || index < 0 {
			t.Fatalf("SW552 %s: the label is not in SQL", name)
		}
		return finished, data.Labels[index]
	}
	legacyRun, legacyLabel := run(legacy, "SW552 legacy")
	if err := probe.RefreshInstalledMetadataViewForTesting(t.Context(), repository, workspace); err != nil {
		t.Fatal(err)
	}
	boundedRun, boundedLabel := run(bounded, "SW552 bounded")
	if legacyRun.Status != "completed" || boundedRun.Status != legacyRun.Status || boundedRun.Output != legacyRun.Output {
		t.Fatalf("SW552 runs differ: legacy %s %q, bounded %s %q (%s)", legacyRun.Status, legacyRun.Output, boundedRun.Status, boundedRun.Output, boundedRun.Error)
	}
	installed, ok := probe.WorkspaceMetadataFields(workspace, "labels")
	if !ok || !slices.ContainsFunc(installed.Labels, func(label domain.IssueLabel) bool { return label.ID == boundedLabel.ID }) {
		t.Fatal("SW552 the bounded run's label is missing from the bounded image")
	}
	legacyLabel.ID, legacyLabel.Name, boundedLabel.ID, boundedLabel.Name = "", "", "", ""
	legacyLabel.CreatedAt = boundedLabel.CreatedAt
	if !reflect.DeepEqual(legacyLabel, boundedLabel) {
		t.Fatalf("SW552 labels differ: legacy %+v, bounded %+v", legacyLabel, boundedLabel)
	}
}
