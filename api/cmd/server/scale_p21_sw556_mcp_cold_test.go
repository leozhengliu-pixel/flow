package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// SW556: MCP is a COLD route (SW550 resolves its local handler to mcpHTTP,
// whose tool dispatch reaches COLD consumers), but a COLD scope holds nothing
// by itself, so MCP traffic is serialized only by the tools that need legacy
// state, per operation:
//
//   - a read tool takes no COLD slot and answers while another flow holds
//     it: the issue tools (list_issues, get_issue) read the issue query
//     access and only the named fields through a field-only legacy view
//     (users, members, states, labels ...); the other tools (list_teams,
//     list_documents, get_workspace) read the legacy projection from a
//     legacy view per call;
//   - a write tool no migrated writer serves (save_label) takes the slot for
//     its own write only.
//
// Every answer equals flag off.
func TestScaleP21SW556MCPToolsColdPerOperation(t *testing.T) {
	if scaleP21RouteHot("POST /mcp") || scaleP21RouteTemperature["POST /mcp"] == "cold: unresolved-handler" {
		t.Fatalf("SW556 POST /mcp is %q", scaleP21RouteTemperature["POST /mcp"])
	}
	f := newMCPContractFixture(t)
	workspace := f.data.Workspace.URLKey
	probe, err := f.repository.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := probe.RefreshInstalledMetadataViewForTesting(t.Context(), f.repository, workspace); err != nil {
		t.Fatal(err)
	}
	var observed []string
	probe.SetSkeletalReadObserverForTesting(func(accessor string) { observed = append(observed, accessor+" from "+sw530Caller()) })
	t.Cleanup(func() { probe.SetSkeletalReadObserverForTesting(nil) })
	legacy := f.host.Config.Handler
	bounded := newHandler(&server{store: probe, uploadPath: t.TempDir()})
	call := func(handler http.Handler, id int, name string, args map[string]any) string {
		t.Helper()
		w := mcpProtocolRequest(t, handler, http.MethodPost, f.secret, map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
		if w.Code != http.StatusOK {
			t.Fatalf("SW556 %s answered %d: %s", name, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	// Another flow holds the COLD slot for the whole read phase.
	holding, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- probe.MutateWorkspace(store.WithColdWork(context.Background(), "sw556"), workspace, "scale_p21_sw556.hold", "", nil, func(*domain.Bootstrap) error {
			close(holding)
			<-release
			return store.ErrNoMutation
		})
	}()
	<-holding
	for index, tool := range []struct {
		name string
		args map[string]any
	}{
		{"list_issues", map[string]any{}},
		{"get_issue", map[string]any{"id": f.data.Issues[0].ID}},
		{"list_teams", map[string]any{}},
		{"list_documents", map[string]any{}},
		{"get_workspace", map[string]any{}},
	} {
		want := call(legacy, index, tool.name, tool.args)
		slots, views := store.ColdSlotAcquisitionsForTesting(), store.ColdAccessorReadsForTesting()
		answered := make(chan string, 1)
		go func() { answered <- call(bounded, index, tool.name, tool.args) }()
		var got string
		select {
		case got = <-answered:
		case <-time.After(10 * time.Second):
			t.Fatalf("SW556 %s waited for the COLD slot", tool.name)
		}
		if got != want {
			t.Fatalf("SW556 %s answers differ:\n legacy=%s\nbounded=%s", tool.name, want, got)
		}
		slots, views = store.ColdSlotAcquisitionsForTesting()-slots, store.ColdAccessorReadsForTesting()-views
		if slots != 0 {
			t.Fatalf("SW556 %s took %d COLD slots", tool.name, slots)
		}
		t.Logf("SW556 %s: no COLD slot, %d legacy view(s): %v", tool.name, views, observed)
		observed = nil
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// A legacy write tool takes the slot for its write only.
	slots := store.ColdSlotAcquisitionsForTesting()
	want := call(legacy, 90, "save_label", map[string]any{"name": "SW556 legacy", "color": "#ff0000"})
	got := call(bounded, 90, "save_label", map[string]any{"name": "SW556 bounded", "color": "#ff0000"})
	if store.ColdSlotAcquisitionsForTesting()-slots < 1 || store.ColdSlotsHeldForTesting() != 0 {
		t.Fatal("SW556 the bounded save_label did not run COLD per operation")
	}
	labels, _ := probe.WorkspaceMetadataFields(workspace, "labels")
	found := false
	for _, label := range labels.Labels {
		found = found || label.Name == "SW556 bounded"
	}
	if !found || len(got) == 0 || len(want) == 0 {
		t.Fatalf("SW556 the bounded save_label is missing from the bounded image: %s", got)
	}
}
