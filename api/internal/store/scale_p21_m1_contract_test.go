package store

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// Deferred audits describe an already committed operation. Legacy callers
// retain the original actor even if membership changes before the audit runs.
func TestScaleP21M1DeferredAuditSharedPrerequisite(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		for _, table := range []string{"audit_events", "audit_chain_heads"} {
			if _, err := f.db.ExecContext(ctx, `DELETE FROM `+table+` WHERE workspace_id=?`, f.data.Workspace.ID); err != nil {
				t.Errorf("clean up owned audit fixture: %v", err)
			}
		}
	})
	probe, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_memberships SET status='suspended' WHERE workspace_id=? AND user_id=?`, f.data.Workspace.ID, f.data.Viewer.ID); err != nil {
		t.Fatal(err)
	}
	ctx := WithMetadataFields(ContextWithActor(t.Context(), f.data.Viewer), "auditLog")
	called := false
	entryID := "m1-deferred-old-" + f.data.Workspace.ID
	appendEntry := func(data *domain.Bootstrap) error {
		called = true
		data.AuditLog = append(data.AuditLog, domain.AuditLogEntry{ID: entryID, Actor: data.Viewer, Action: "m1.deferred", ResourceType: "account", ResourceID: f.data.Viewer.ID})
		return nil
	}
	if err := f.MutateWorkspaceScoped(ctx, workspace, "audit.workspace_member", f.data.Viewer.ID, nil, MetadataRecordScope{}, appendEntry); err != nil || !called {
		t.Fatalf("legacy deferred audit: called=%v err=%v", called, err)
	}
	called = false
	entryID = "m1-deferred-bounded-" + f.data.Workspace.ID
	if err := probe.MutateWorkspaceScoped(ctx, workspace, "audit.workspace_member", f.data.Viewer.ID, nil, MetadataRecordScope{}, appendEntry); err != nil || !called {
		t.Fatalf("bounded deferred audit: called=%v err=%v", called, err)
	}
}

func TestScaleP21M1ActorDirectorySharedPrerequisite(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	var original []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='users' AND record_key=?`, workspace, f.data.Viewer.ID).Scan(&original); err != nil {
		t.Fatal(err)
	}
	actor := f.data.Viewer
	actor.DisplayName = "Current account display name"
	ctx := ContextWithActor(t.Context(), actor)
	scope := MetadataRecordScope{Write: []RecordKey{{Kind: "settings", ID: "m1-target"}}}
	callback := func(data *domain.Bootstrap) error {
		data.Settings["m1-target"] = "updated"
		return nil
	}
	preimage, _ := f.storedWorkspace(workspace)
	preimage = cloneBootstrap(preimage)
	if err := f.MutateWorkspaceScoped(ctx, workspace, "application_policy.updated", "m1-target", nil, scope, callback); err != nil {
		t.Fatal(err)
	}
	var legacy []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='users' AND record_key=?`, workspace, actor.ID).Scan(&legacy); err != nil {
		t.Fatal(err)
	}
	var got domain.User
	if err := json.Unmarshal(legacy, &got); err != nil || got.DisplayName != actor.DisplayName {
		t.Fatalf("legacy actor refresh changed: %#v err=%v", got, err)
	}
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='users' AND record_key=?`, original, workspace, actor.ID); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.setWorkspaceLocked(workspace, preimage)
	f.mu.Unlock()
	probe, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := probe.MutateWorkspaceScoped(ctx, workspace, "application_policy.updated", "m1-target", nil, scope, callback); err != nil {
		t.Fatal(err)
	}
	var bounded []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='users' AND record_key=?`, workspace, actor.ID).Scan(&bounded); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(legacy, bounded) {
		t.Fatal("bounded actor-directory refresh differs from the legacy writer")
	}
}
