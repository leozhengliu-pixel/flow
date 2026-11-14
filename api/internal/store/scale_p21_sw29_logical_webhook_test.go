package store

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21SW29LogicalWebhookPredicates(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	for _, item := range []struct {
		name string
		data domain.Bootstrap
	}{
		{"empty", domain.Bootstrap{}},
		{"unicode-space", domain.Bootstrap{Webhooks: []domain.Webhook{{Enabled: true, URL: "\u2000\u00a0\u0085"}}}},
		{"invalid-byte-url", domain.Bootstrap{Webhooks: []domain.Webhook{{Enabled: true, URL: "\xff"}}}},
		{"large-url", domain.Bootstrap{Webhooks: []domain.Webhook{{Enabled: true, URL: strings.Repeat(" ", 2<<20) + "https://example.test"}}}},
		{"disabled-webhook", domain.Bootstrap{Webhooks: []domain.Webhook{{URL: "https://example.test"}}}},
		{"draft-loop", domain.Bootstrap{Loops: []domain.Loop{{Enabled: true, Status: "draft", TriggerType: "event"}}}},
		{"warm-nul-loop", domain.Bootstrap{Loops: []domain.Loop{{Enabled: true, Status: "dra\x00ft", TriggerType: "event"}}}},
		{"schedule-loop", domain.Bootstrap{Loops: []domain.Loop{{Enabled: true, Status: "published", TriggerType: "schedule"}}}},
		{"warm-trigger", domain.Bootstrap{Loops: []domain.Loop{{Enabled: true, Status: "published", TriggerType: "sche\x00dule"}}}},
		{"connected", domain.Bootstrap{IntegrationConnections: []domain.IntegrationConnection{{Provider: "feishu", Status: "connected"}}}},
		{"warm-provider", domain.Bootstrap{IntegrationConnections: []domain.IntegrationConnection{{Provider: "fei\x00shu", Status: "connected"}}}},
		{"warm-status", domain.Bootstrap{IntegrationConnections: []domain.IntegrationConnection{{Provider: "lark", Status: "con\x00nected"}}}},
	} {
		t.Run(item.name, func(t *testing.T) {
			repo := &SQLiteStore{db: f.db, dialect: f.dialect, workspaces: map[string]domain.Bootstrap{workspace: item.data}}
			repo.chatSnapshotFlags.Store(workspace, false)
			source, err := captureMetadataLogicalSource(t.Context(), item.data, copyStamp{})
			if err != nil {
				t.Fatal(err)
			}
			defer source.Release()
			want := repo.webhookNeeded(t.Context(), workspace)
			got, err := repo.logicalWebhookNeeded(t.Context(), source, workspace)
			if err != nil || got != want {
				t.Fatalf("logical webhook needed=%v want=%v err=%v", got, want, err)
			}
			repo.chatSnapshotFlags.Store(workspace, true)
			got, err = repo.logicalWebhookNeeded(t.Context(), source, workspace)
			if err != nil || !got {
				t.Fatalf("authoritative chat flag lost: %v %v", got, err)
			}
		})
	}
}

func TestScaleP21SW29LogicalWebhookWarmLoopProducer(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, target, loopID := f.data.Workspace.URLKey, idgen.New("sw29-target_"), idgen.New("loop_")
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "settings", "loops"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
		data.Settings[target] = map[string]any{"id": target, "name": "before"}
		data.Loops = []domain.Loop{{ID: loopID, Enabled: true, Status: "dra\x00ft", TriggerType: "event"}}
		data.Webhooks = nil
		data.IntegrationConnections = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	preimage, _ := f.WorkspaceMetadata(workspace)
	var original, loopRow []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='settings' AND record_key=?`, workspace, target).Scan(&original); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='loops' AND record_key=?`, workspace, loopID).Scan(&loopRow); err != nil {
		t.Fatal(err)
	}
	if !f.webhookNeeded(t.Context(), workspace) {
		t.Fatal("literal warm installed loop did not request previous values")
	}
	if canonical, err := f.residentWebhookNeeded(t.Context(), workspace); err != nil || canonical {
		t.Fatalf("fixture failed to distinguish canonical draft loop: %v %v", canonical, err)
	}
	var legacy, bounded domain.DomainEvent
	f.SetWebhookSink(func(_ string, event domain.DomainEvent) { legacy = event })
	scope := MetadataRecordScope{Write: []RecordKey{{Kind: "settings", ID: target}}}
	callback := func(data *domain.Bootstrap) error {
		data.Settings[target].(map[string]any)["name"] = "after"
		return nil
	}
	ctx := WithMetadataFields(t.Context(), "settings")
	if err := f.MutateWorkspaceScoped(ctx, workspace, "application_policy.updated", target, nil, scope, callback); err != nil {
		t.Fatal(err)
	}
	if len(legacy.PreviousValues) == 0 {
		t.Fatal("literal producer did not emit previous values")
	}
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='settings' AND record_key=?`, original, workspace, target); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.setWorkspaceLocked(workspace, preimage)
	f.mu.Unlock()
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	view.SetWebhookSink(func(_ string, event domain.DomainEvent) { bounded = event })
	if err := view.MutateWorkspaceScoped(ctx, workspace, "application_policy.updated", target, nil, scope, callback); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(legacy.PreviousValues, bounded.PreviousValues) || !reflect.DeepEqual(legacy.Payload, bounded.Payload) {
		t.Fatalf("warm loop changed producer previous values: old=%s bounded=%s", legacy.PreviousValues, bounded.PreviousValues)
	}
	var afterLoop []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='loops' AND record_key=?`, workspace, loopID).Scan(&afterLoop); err != nil || !bytes.Equal(afterLoop, loopRow) {
		t.Fatalf("unselected warm loop canonical row changed: %v", err)
	}
}
