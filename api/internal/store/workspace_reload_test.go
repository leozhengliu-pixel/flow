package store

import (
	"context"
	"fmt"
	"testing"
)

func TestWorkspaceReloadKeepsDeltaAfterInstallRaces(t *testing.T) {
	f, view, workspace := coldIncrementalFixture(t)
	change := func(version int) {
		t.Helper()
		raw := fmt.Appendf(nil, `{"id":"ci-key-1","name":"Peer %d","prefix":"flw_1","scopes":["read"],"teamIds":[]}`, version)
		if _, err := f.db.ExecContext(t.Context(),
			`UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='apiKeys' AND record_key='ci-key-1'`, raw, workspace); err != nil {
			t.Fatal(err)
		}
	}
	change(0)
	updates := 0
	hook := func() {
		updates++
		if updates <= 2 {
			change(updates)
		}
	}
	residentReloadBeforeInstall.Store(&hook)
	defer residentReloadBeforeInstall.Store(nil)

	var trace WorkspaceReloadTrace
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := view.ReloadWorkspace(WithWorkspaceReloadTrace(ctx, &trace), workspace); err != nil {
		t.Fatal(err)
	}
	if trace.DeltaAttempts != 2 || trace.DeltaRaces != 2 || trace.FullAttempts != 0 || updates != 2 {
		t.Fatalf("reload did not keep the incremental path: %+v (hook calls %d)", trace, updates)
	}
	coldIncrementalAssertExact(t, f, view, workspace, "after two install races")
}
