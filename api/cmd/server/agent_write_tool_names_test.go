package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The web app words a failed write call as what did not happen ("Couldn't update issue") and a failed read as an
// ordinary step, using this list of the Flow tools that change data: the same classification the server's write
// ledger and per-call idempotency use (agentChangeToolAccess, behind agentLedgerTool). Regenerate it with
// FLOW_UPDATE_FIXTURES=1.
func TestAgentWriteToolNamesFixtureCurrent(t *testing.T) {
	path := filepath.Join(webSourceDir, "components", "agent", "agent-write-tool-names.json")
	want := []string{}
	for name, access := range agentChangeToolAccess() {
		if access == "write" {
			want = append(want, name)
		}
	}
	slices.Sort(want)
	if os.Getenv("FLOW_UPDATE_FIXTURES") == "1" {
		raw, _ := json.MarshalIndent(want, "", "  ")
		if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("%s is out of date with the server's write tools; run FLOW_UPDATE_FIXTURES=1 go test ./cmd/server -run TestAgentWriteToolNamesFixtureCurrent", path)
	}
	for _, name := range got {
		if _, write := agentLedgerTool(t.Context(), name); !write {
			t.Errorf("%s is listed as a write tool but agentLedgerTool does not count it as one", name)
		}
	}
}
