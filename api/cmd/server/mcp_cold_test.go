package main

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// MCP is a COLD route (route temperature guard resolves its local handler to mcpHTTP,
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
//   - save_view creates and edits through the scoped HTTP writer. Its
//     optional share and unshare still take the slot for their own writes.
//
// Every answer equals flag off.
func TestMCPToolsColdPerOperation(t *testing.T) {
	// A production bounded store requires both flags. The test view
	// bypasses activation, so set them before creating the fixture.
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	if scaleRouteHot("POST /mcp") || scaleRouteTemperature["POST /mcp"] == "cold: unresolved-handler" {
		t.Fatalf("SW556 POST /mcp is %q", scaleRouteTemperature["POST /mcp"])
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
	probe.SetSkeletalReadObserverForTesting(func(accessor string) { observed = append(observed, accessor+" from "+routeSweepCaller()) })
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
	stopHolding := sync.OnceFunc(func() { close(release) })
	defer stopHolding()
	go func() {
		done <- probe.MutateWorkspace(store.WithColdWork(context.Background(), "sw556"), workspace, "scale_p21_sw556.hold", "", nil, func(*domain.Bootstrap) error {
			close(holding)
			<-release
			return store.ErrNoMutation
		})
	}()
	select {
	case <-holding:
	case err := <-done:
		t.Fatalf("SW556 could not hold the COLD slot: %v", err)
	}
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
	stopHolding()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	saveView := func(handler http.Handler, repo *store.SQLiteStore) []string {
		var id, slug string
		replies := make([]string, 0, 4)
		for i, step := range []struct {
			label, name string
			args        map[string]any
			coldSlots   int64
			shared      bool
		}{
			{"create", "SW556 view", map[string]any{"name": "SW556 view", "team": f.data.Teams[0].Key, "filters": []map[string]any{{"field": "priority", "values": []string{"High"}}}}, 0, false},
			{"edit", "Renamed SW556 view", map[string]any{"name": "Renamed SW556 view"}, 0, false},
			{"share", "Renamed SW556 view", map[string]any{"shared": true}, 1, true},
			{"unshare", "Renamed SW556 view", map[string]any{"shared": false}, 1, false},
		} {
			if slug != "" {
				step.args["id"] = slug
			}
			slots := store.ColdSlotAcquisitionsForTesting()
			reply := call(handler, 90+i, "save_view", step.args)
			if repo.BoundedMetadataEnabled() {
				if gained, held := store.ColdSlotAcquisitionsForTesting()-slots, store.ColdSlotsHeldForTesting(); gained != step.coldSlots || held != 0 {
					t.Fatalf("SW556 save_view %s acquired %d COLD slots, want %d, and left %d held: %s", step.label, gained, step.coldSlots, held, reply)
				}
			}
			var response map[string]any
			if err := json.Unmarshal([]byte(reply), &response); err != nil {
				t.Fatal(err)
			}
			receipt, ok := mcpSuccess(t, response).(map[string]any)
			if !ok {
				t.Fatalf("SW556 save_view %s returned no receipt: %s", step.label, reply)
			}
			if i == 0 {
				id, slug = stringArg(receipt, "id"), stringArg(receipt, "slugId")
			}
			if id == "" || slug == "" || receipt["id"] != id || receipt["slugId"] != slug ||
				receipt["name"] != step.name || receipt["scope"] != "team" || receipt["shared"] != step.shared {
				t.Fatalf("SW556 save_view %s returned the wrong receipt: %s", step.label, reply)
			}
			data, _ := repo.WorkspaceMetadataFields(workspace, "savedViews")
			index := slices.IndexFunc(data.SavedViews, func(view domain.SavedView) bool { return view.ID == id })
			if index < 0 {
				t.Fatalf("SW556 save_view %s is missing from the installed view: %s", step.label, reply)
			}
			view := data.SavedViews[index]
			if view.Name != step.name || view.OwnerID != f.data.Viewer.ID || view.TeamID != f.data.Teams[0].ID || (view.ShareToken != "") != step.shared {
				t.Fatalf("SW556 save_view %s did not install the expected record", step.label)
			}
			replacements := []string{id, "<id>", slug, "<slug>"}
			if step.shared {
				shareURL := stringArg(receipt, "shareUrl")
				if !strings.Contains(shareURL, "/shared/views/"+view.ShareToken+"?") {
					t.Fatal("SW556 save_view returned the wrong share URL")
				}
				replacements = append(replacements, view.ShareToken, "<token>")
			}
			replies = append(replies, strings.NewReplacer(replacements...).Replace(reply))
		}
		return replies
	}

	want := saveView(legacy, f.repository)
	if err := probe.RefreshInstalledMetadataViewForTesting(t.Context(), f.repository, workspace); err != nil {
		t.Fatal(err)
	}
	got := saveView(bounded, probe)
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("SW556 save_view call %d answers differ:\n legacy=%s\nbounded=%s", i+1, want[i], got[i])
		}
	}
}
