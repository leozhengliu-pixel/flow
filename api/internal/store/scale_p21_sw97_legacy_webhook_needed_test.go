package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21SW97WebhookView(t *testing.T, workspace string, data domain.Bootstrap) (*SQLiteStore, *metadataLogicalSource) {
	t.Helper()
	source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	view := &SQLiteStore{
		metadataResidency: newMetadataResidency(),
		workspaces:        map[string]domain.Bootstrap{workspace: {Workspace: data.Workspace}},
		lastWorkspaceKey:  workspace,
	}
	view.chatSnapshotFlags.Store(workspace, false)
	view.installMetadataLogicalSource(workspace, source)
	t.Cleanup(view.releaseMetadataLogicalSources)
	return view, source
}

func scaleP21SW97WebhookReleased(t *testing.T, view *SQLiteStore, source *metadataLogicalSource, wantRefs int) {
	t.Helper()
	source.mu.Lock()
	refs := source.refs
	source.mu.Unlock()
	if refs != wantRefs || view.metadataResidency.work.used != 0 {
		t.Fatalf("webhook owner leaked: refs=%d want=%d work=%d", refs, wantRefs, view.metadataResidency.work.used)
	}
}

func TestScaleP21SW97LegacyWebhookPredicateDifferential(t *testing.T) {
	for _, item := range []struct {
		name string
		data domain.Bootstrap
	}{
		{"empty", domain.Bootstrap{}},
		{"enabled", domain.Bootstrap{Webhooks: []domain.Webhook{{Enabled: true, URL: "https://example.test"}}}},
		{"audit-log", domain.Bootstrap{Webhooks: []domain.Webhook{{Enabled: true, AuditLog: true, URL: "https://example.test"}}}},
		{"disabled-large", domain.Bootstrap{Webhooks: []domain.Webhook{{URL: strings.Repeat("x", 2<<20)}}}},
		{"unicode-space", domain.Bootstrap{Webhooks: []domain.Webhook{{Enabled: true, URL: "\u2000\u00a0\u0085"}}}},
		{"invalid-byte", domain.Bootstrap{Webhooks: []domain.Webhook{{Enabled: true, URL: "\xff"}}}},
		{"truncated-rune", domain.Bootstrap{Webhooks: []domain.Webhook{{Enabled: true, URL: "\xe2\x80"}}}},
		{"zero-width", domain.Bootstrap{Webhooks: []domain.Webhook{{Enabled: true, URL: "\u200b"}}}},
		{"nul-url", domain.Bootstrap{Webhooks: []domain.Webhook{{Enabled: true, URL: "\x00"}}}},
		{"large-space", domain.Bootstrap{Webhooks: []domain.Webhook{{Enabled: true, URL: strings.Repeat(" ", 2<<20)}}}},
		{"large-url", domain.Bootstrap{Webhooks: []domain.Webhook{{Enabled: true, URL: strings.Repeat(" ", 2<<20) + "https://example.test"}}}},
		{"disabled-loop", domain.Bootstrap{Loops: []domain.Loop{{Status: "published", TriggerType: "event"}}}},
		{"draft-loop", domain.Bootstrap{Loops: []domain.Loop{{Enabled: true, Status: "draft", TriggerType: "event"}}}},
		{"empty-trigger", domain.Bootstrap{Loops: []domain.Loop{{Enabled: true, Status: "published"}}}},
		{"schedule", domain.Bootstrap{Loops: []domain.Loop{{Enabled: true, Status: "published", TriggerType: "schedule"}}}},
		{"event-loop", domain.Bootstrap{Loops: []domain.Loop{{Enabled: true, Status: "published", TriggerType: "event"}}}},
		{"warm-draft-nul", domain.Bootstrap{Loops: []domain.Loop{{Enabled: true, Status: "dra\x00ft", TriggerType: "event"}}}},
		{"warm-schedule-nul", domain.Bootstrap{Loops: []domain.Loop{{Enabled: true, Status: "published", TriggerType: "sche\x00dule"}}}},
		{"feishu", domain.Bootstrap{IntegrationConnections: []domain.IntegrationConnection{{Provider: "feishu", Status: "connected"}}}},
		{"lark", domain.Bootstrap{IntegrationConnections: []domain.IntegrationConnection{{Provider: "lark", Status: "connected"}}}},
		{"provider-case", domain.Bootstrap{IntegrationConnections: []domain.IntegrationConnection{{Provider: "Feishu", Status: "connected"}}}},
		{"provider-nul", domain.Bootstrap{IntegrationConnections: []domain.IntegrationConnection{{Provider: "fei\x00shu", Status: "connected"}}}},
		{"status-nul", domain.Bootstrap{IntegrationConnections: []domain.IntegrationConnection{{Provider: "lark", Status: "con\x00nected"}}}},
	} {
		t.Run(item.name, func(t *testing.T) {
			const workspace = "sw97-explicit"
			item.data.Settings = map[string]any{"ignored": strings.Repeat("settings ", 1<<18)}
			item.data.Projects = []domain.Project{{Description: strings.Repeat("body ", 1<<18)}}
			off := &SQLiteStore{workspaces: map[string]domain.Bootstrap{workspace: item.data}}
			view, source := scaleP21SW97WebhookView(t, workspace, item.data)
			queries := 0
			ctx := WithSQLStatementObserver(t.Context(), func(string) { queries++ })
			for _, chat := range []bool{false, true} {
				off.chatSnapshotFlags.Store(workspace, chat)
				view.chatSnapshotFlags.Store(workspace, chat)
				want := off.webhookNeeded(ctx, workspace)
				got, err := view.boundedLegacyWebhookNeeded(ctx, workspace)
				if err != nil || got != want {
					t.Fatalf("bounded=%v literal=%v chat=%v err=%v", got, want, chat, err)
				}
				literal, err := off.boundedLegacyWebhookNeeded(ctx, workspace)
				if err != nil || literal != want {
					t.Fatalf("off helper changed literal=%v want=%v err=%v", literal, want, err)
				}
				scaleP21SW97WebhookReleased(t, view, source, 1)
			}
			if queries != 0 || view.WorkspaceMetadataDecodes() != 0 {
				t.Fatalf("installed predicate performed SQL/full hydration: queries=%d decodes=%d", queries, view.WorkspaceMetadataDecodes())
			}
		})
	}
}

func TestScaleP21SW97LegacyWebhookIdentityPinsAndFailures(t *testing.T) {
	workspace := "physical\x00key"
	data := domain.Bootstrap{Workspace: domain.Workspace{URLKey: "logical"}, Webhooks: []domain.Webhook{{Enabled: true, AuditLog: true, URL: "url"}}}
	view, source := scaleP21SW97WebhookView(t, workspace, data)
	foreign, err := captureMetadataLogicalSource(t.Context(), domain.Bootstrap{}, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer foreign.Release()
	ctx := t.Context()
	for _, pinned := range []string{"foreign", workspace} {
		pinnedCtx := context.WithValue(ctx, metadataLogicalReadKey{}, metadataLogicalRead{workspace: pinned, source: foreign})
		if got, err := view.boundedLegacyWebhookNeeded(pinnedCtx, workspace); err != nil || !got {
			t.Fatalf("foreign/stale pin %q changed explicit installed decision: %v %v", pinned, got, err)
		}
	}
	for _, key := range []string{"", "logical", strings.ToUpper(workspace), workspace + "-missing"} {
		for _, chat := range []bool{false, true} {
			view.chatSnapshotFlags.Store(key, chat)
			if got, err := view.boundedLegacyWebhookNeeded(ctx, key); err != nil || got != chat {
				t.Fatalf("exact missing key %q chat=%v: %v %v", key, chat, got, err)
			}
		}
	}
	scaleP21SW97WebhookReleased(t, view, source, 1)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := view.boundedLegacyWebhookNeeded(canceled, workspace); !errors.Is(err, context.Canceled) {
		t.Fatalf("bounded cancellation lost: %v", err)
	}
	off := &SQLiteStore{workspaces: map[string]domain.Bootstrap{workspace: data}}
	if got, err := off.boundedLegacyWebhookNeeded(canceled, workspace); err != nil || !got {
		t.Fatalf("off cancellation changed original decision: %v %v", got, err)
	}
	view.metadataResidency.work.limit = 1
	if _, err := view.boundedLegacyWebhookNeeded(ctx, workspace); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatalf("admission refusal lost: %v", err)
	}
	view.metadataResidency.work.limit = metadataWorkBytes
	scaleP21SW97WebhookReleased(t, view, source, 1)
	view.metadataResidency.mu.Lock()
	delete(view.metadataResidency.logical, workspace)
	view.metadataResidency.mu.Unlock()
	if _, err := view.boundedLegacyWebhookNeeded(ctx, workspace); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatalf("missing installed source silently used policy: %v", err)
	}
	view.metadataResidency.mu.Lock()
	view.metadataResidency.logical[workspace] = source
	view.metadataResidency.mu.Unlock()
	index, _, _ := metadataLogicalField("webhooks")
	original := source.fields[index]
	source.fields[index].blocked = true
	_, err = view.boundedLegacyWebhookNeeded(ctx, workspace)
	source.fields[index] = original
	if !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatalf("blocked source silently succeeded: %v", err)
	}
	scaleP21SW97WebhookReleased(t, view, source, 1)
}

func TestScaleP21SW97LegacyWebhookAuthoritativeChatFallback(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	workspace, connection := idgen.New("sw97-chat-workspace-"), idgen.New("sw97-chat-")
	off := &SQLiteStore{db: f.db, dialect: f.dialect, workspaces: map[string]domain.Bootstrap{}}
	view := &SQLiteStore{db: f.db, dialect: f.dialect, metadataResidency: newMetadataResidency(), workspaces: map[string]domain.Bootstrap{}}
	t.Cleanup(func() {
		if err := f.DeleteChatIntegration(context.Background(), workspace, connection); err != nil {
			t.Error(err)
		}
	})
	for _, connected := range []bool{false, true} {
		if err := f.PutChatIntegration(t.Context(), domain.ChatIntegrationRecord{
			WorkspaceKey: workspace, Provider: "feishu", ConnectionID: connection,
			AppID: connection, TenantKey: connection, Connected: connected,
		}); err != nil {
			t.Fatal(err)
		}
		off.forgetChatSnapshotFlag(workspace)
		view.forgetChatSnapshotFlag(workspace)
		offQueries, onQueries := 0, 0
		offCtx := WithSQLStatementObserver(t.Context(), func(string) { offQueries++ })
		onCtx := WithSQLStatementObserver(t.Context(), func(string) { onQueries++ })
		want := off.webhookNeeded(offCtx, workspace)
		got, err := view.boundedLegacyWebhookNeeded(onCtx, workspace)
		if err != nil || want != connected || got != want || offQueries != 1 || onQueries != 1 {
			t.Fatalf("uncached chat fallback connected=%v got=%v literal=%v SQL=%d/%d err=%v", connected, got, want, offQueries, onQueries, err)
		}
		if got, err := view.boundedLegacyWebhookNeeded(onCtx, workspace); err != nil || got != want || onQueries != 1 {
			t.Fatalf("chat fallback lost original cache: got=%v SQL=%d err=%v", got, onQueries, err)
		}
		if view.metadataResidency.work.used != 0 {
			t.Fatal("missing-workspace chat fallback leaked work")
		}
	}
}

func TestScaleP21SW97LegacyWebhookRetainedGeneration(t *testing.T) {
	for _, phase := range []string{"replace", "cancel"} {
		t.Run(phase, func(t *testing.T) {
			const workspace = "sw97-generation"
			view, source := scaleP21SW97WebhookView(t, workspace, domain.Bootstrap{Loops: []domain.Loop{{Enabled: true, Status: "dra\x00ft", TriggerType: "event"}}})
			replacement, err := captureMetadataLogicalSource(t.Context(), domain.Bootstrap{}, copyStamp{})
			if err != nil {
				t.Fatal(err)
			}
			defer replacement.Release()
			parent, cancel := context.WithCancel(t.Context())
			defer cancel()
			entered := false
			ctx := scaleP21HotWebhookContext{Context: parent, check: func() {
				source.mu.Lock()
				refs := source.refs
				source.mu.Unlock()
				if entered || refs < 3 {
					return
				}
				entered = true
				if phase == "cancel" {
					cancel()
					return
				}
				if err := replacement.Retain(); err != nil {
					t.Fatal(err)
				}
				view.installMetadataLogicalSource(workspace, replacement)
			}}
			got, err := view.boundedLegacyWebhookNeeded(ctx, workspace)
			if !entered {
				t.Fatal("predicate did not traverse its retained generation")
			}
			if phase == "cancel" {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("late cancellation lost: %v", err)
				}
				scaleP21SW97WebhookReleased(t, view, source, 1)
			} else {
				if err != nil || !got {
					t.Fatalf("predicate changed installed generation: %v %v", got, err)
				}
				scaleP21SW97WebhookReleased(t, view, source, 0)
			}
		})
	}
}

func TestScaleP21SW97LegacyWebhookWarmAndReloaded(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	for _, item := range []struct {
		name string
		data domain.Bootstrap
	}{
		{"audit", domain.Bootstrap{Webhooks: []domain.Webhook{{ID: idgen.New("sw97-hook-"), Enabled: true, AuditLog: true, URL: "url"}}}},
		{"warm-nul", domain.Bootstrap{Loops: []domain.Loop{{ID: idgen.New("sw97-loop-"), Enabled: true, Status: "dra\x00ft", TriggerType: "event"}}}},
		{"lark", domain.Bootstrap{IntegrationConnections: []domain.IntegrationConnection{{ID: idgen.New("sw97-connection-"), Provider: "lark", Status: "connected"}}}},
		{"empty", domain.Bootstrap{}},
	} {
		t.Run(item.name, func(t *testing.T) {
			if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "webhooks", "loops", "integrationConnections"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
				data.Webhooks, data.Loops, data.IntegrationConnections = item.data.Webhooks, item.data.Loops, item.data.IntegrationConnections
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			for _, state := range []string{"warm", "reloaded"} {
				if state == "reloaded" {
					f.mu.Lock()
					f.forgetCopyRevisionLocked(workspace)
					f.mu.Unlock()
					if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
						t.Fatal(err)
					}
				}
				f.chatSnapshotFlags.Store(workspace, false)
				want := f.webhookNeeded(t.Context(), workspace)
				view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
				if err != nil {
					t.Fatal(err)
				}
				view.chatSnapshotFlags.Store(workspace, false)
				got, err := view.boundedLegacyWebhookNeeded(t.Context(), workspace)
				if err != nil || got != want {
					t.Fatalf("%s bounded=%v literal=%v err=%v", state, got, want, err)
				}
				if view.metadataResidency.work.used != 0 {
					t.Fatal("warm/reloaded predicate leaked work")
				}
				view.releaseMetadataLogicalSources()
			}
		})
	}
}
