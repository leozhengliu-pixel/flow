package store

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21HotWebhookInstalledDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace, unrelated := f.data.Workspace.URLKey, idgen.New("hot-webhook-unrelated-")
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.hot_webhook_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, domain.Project{ID: unrelated, Description: strings.Repeat("unrelated body ", 30000)})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	row := func() []byte {
		t.Helper()
		var raw []byte
		if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='projects' AND record_key=?`, workspace, unrelated).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	before := row()
	for _, item := range []struct {
		name string
		data domain.Bootstrap
		want bool
	}{
		{"empty", domain.Bootstrap{}, false},
		{"unicode-space", domain.Bootstrap{Webhooks: []domain.Webhook{{Enabled: true, URL: "\u2000\u00a0\u0085"}}}, false},
		{"invalid-byte-url", domain.Bootstrap{Webhooks: []domain.Webhook{{Enabled: true, URL: "\xff"}}}, true},
		{"large-url", domain.Bootstrap{Webhooks: []domain.Webhook{{Enabled: true, URL: strings.Repeat(" ", 2<<20) + "https://example.test"}}}, true},
		{"draft-loop", domain.Bootstrap{Loops: []domain.Loop{{Enabled: true, Status: "draft", TriggerType: "event"}}}, false},
		{"warm-nul-loop", domain.Bootstrap{Loops: []domain.Loop{{Enabled: true, Status: "dra\x00ft", TriggerType: "event"}}}, true},
		{"schedule-loop", domain.Bootstrap{Loops: []domain.Loop{{Enabled: true, Status: "published", TriggerType: "schedule"}}}, false},
		{"warm-trigger", domain.Bootstrap{Loops: []domain.Loop{{Enabled: true, Status: "published", TriggerType: "sche\x00dule"}}}, true},
		{"connected", domain.Bootstrap{IntegrationConnections: []domain.IntegrationConnection{{Provider: "feishu", Status: "connected"}}}, true},
		{"warm-provider", domain.Bootstrap{IntegrationConnections: []domain.IntegrationConnection{{Provider: "fei\x00shu", Status: "connected"}}}, false},
		{"warm-status", domain.Bootstrap{IntegrationConnections: []domain.IntegrationConnection{{Provider: "lark", Status: "con\x00nected"}}}, false},
	} {
		t.Run(item.name, func(t *testing.T) {
			if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "webhooks", "loops", "integrationConnections"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
				data.Webhooks, data.Loops, data.IntegrationConnections = item.data.Webhooks, item.data.Loops, item.data.IntegrationConnections
				for i := range data.Webhooks {
					data.Webhooks[i].ID = idgen.New("hot-webhook-")
				}
				for i := range data.Loops {
					data.Loops[i].ID = idgen.New("hot-loop-")
				}
				for i := range data.IntegrationConnections {
					data.IntegrationConnections[i].ID = idgen.New("hot-connection-")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			f.chatSnapshotFlags.Store(workspace, false)
			want := f.webhookNeeded(t.Context(), workspace)
			if want != item.want {
				t.Fatalf("warm installed oracle=%v want=%v", want, item.want)
			}
			view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			defer view.releaseMetadataLogicalSources()
			view.chatSnapshotFlags.Store(workspace, false)
			queries := 0
			ctx := WithSQLStatementObserver(t.Context(), func(query string) {
				queries++
				t.Errorf("installed predicate unexpectedly queried SQL: %s", query)
			})
			got, err := view.residentWebhookNeeded(ctx, workspace)
			if err != nil || got != want {
				t.Fatalf("bounded predicate=%v literal=%v error=%v", got, want, err)
			}
			view.chatSnapshotFlags.Store(workspace, true)
			if got, err := view.residentWebhookNeeded(ctx, workspace); err != nil || !got {
				t.Fatalf("authoritative chat flag lost: %v %v", got, err)
			}
			if queries != 0 || view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 {
				t.Fatal("predicate queried SQL, decoded full metadata or leaked work")
			}
			for _, flag := range []bool{false, true} {
				missing := workspace + "-missing"
				f.chatSnapshotFlags.Store(missing, flag)
				view.chatSnapshotFlags.Store(missing, flag)
				want, offErr := f.residentWebhookNeeded(t.Context(), missing)
				got, err := view.residentWebhookNeeded(ctx, missing)
				if offErr != nil || err != nil || got != want || got != flag {
					t.Fatalf("missing workspace chat fallback changed: got=%v want=%v flag=%v errors=%v/%v", got, want, flag, err, offErr)
				}
			}
			// Off mode retains the canonical SQL predicate, including its
			// existing distinction from warm installed strings.
			offQueries := 0
			off := WithSQLStatementObserver(t.Context(), func(query string) {
				if strings.Contains(query, "SELECT CASE WHEN EXISTS(SELECT 1 FROM workspace_metadata_records") {
					offQueries++
				}
			})
			canonical, err := f.residentWebhookNeeded(off, workspace)
			if err != nil || offQueries != 1 {
				t.Fatalf("off predicate did not retain literal SQL: count=%d error=%v", offQueries, err)
			}
			if item.name == "warm-nul-loop" && canonical || item.name == "unicode-space" && f.dialect == "sqlite" && !canonical {
				t.Fatal("fixture no longer distinguishes warm and canonical predicates")
			}
			if !bytes.Equal(before, row()) {
				t.Fatal("predicate changed unrelated canonical bytes")
			}
		})
	}
}

func TestScaleP21HotWebhookIdentityAndLifetime(t *testing.T) {
	physical := "physical\x00key"
	data := domain.Bootstrap{Workspace: domain.Workspace{URLKey: "logical-key"}, Loops: []domain.Loop{{Enabled: true, Status: "dra\x00ft", TriggerType: "event"}}}
	source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	view := &SQLiteStore{metadataResidency: newMetadataResidency(), workspaces: map[string]domain.Bootstrap{physical: {}}, lastWorkspaceKey: physical}
	view.installMetadataLogicalSource(physical, source)
	defer view.releaseMetadataLogicalSources()
	view.chatSnapshotFlags.Store(physical, false)
	checkReleased := func() {
		t.Helper()
		source.mu.Lock()
		refs := source.refs
		source.mu.Unlock()
		if refs != 1 || view.metadataResidency.work.used != 0 {
			t.Fatalf("source/work lifetime leaked: refs=%d work=%d", refs, view.metadataResidency.work.used)
		}
	}
	for _, key := range []string{physical, ""} {
		if got, err := view.residentWebhookNeeded(t.Context(), key); err != nil || !got {
			t.Fatalf("physical/default key %q: %v %v", key, got, err)
		}
		checkReleased()
	}
	for _, key := range []string{"logical-key", strings.ToUpper(physical), physical + "-missing"} {
		for _, flag := range []bool{false, true} {
			view.chatSnapshotFlags.Store(key, flag)
			if got, err := view.residentWebhookNeeded(t.Context(), key); err != nil || got != flag {
				t.Fatalf("missing exact key %q flag=%v: %v %v", key, flag, got, err)
			}
		}
		checkReleased()
	}
	view.lastWorkspaceKey = "missing-default"
	for _, flag := range []bool{false, true} {
		view.chatSnapshotFlags.Store("missing-default", flag)
		if got, err := view.residentWebhookNeeded(t.Context(), ""); err != nil || got != flag {
			t.Fatalf("missing default chat flag=%v: %v %v", flag, got, err)
		}
		checkReleased()
	}
	view.lastWorkspaceKey = physical
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := view.residentWebhookNeeded(ctx, ""); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
	checkReleased()
	view.metadataResidency.work.limit = 1
	if _, err := view.residentWebhookNeeded(t.Context(), ""); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("descriptor admission bypassed", err)
	}
	view.metadataResidency.work.limit = metadataWorkBytes
	checkReleased()
	view.metadataResidency.mu.Lock()
	delete(view.metadataResidency.logical, physical)
	view.metadataResidency.mu.Unlock()
	if _, err := view.residentWebhookNeeded(t.Context(), ""); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("missing source silently succeeded", err)
	}
	view.metadataResidency.mu.Lock()
	view.metadataResidency.logical[physical] = source
	view.metadataResidency.mu.Unlock()
	checkReleased()
	index, _, _ := metadataLogicalField("loops")
	original := source.fields[index]
	source.fields[index].blocked = true
	_, err = view.residentWebhookNeeded(t.Context(), physical)
	source.fields[index] = original
	if !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("opaque logical predicate silently succeeded", err)
	}
	checkReleased()
}

type scaleP21HotWebhookContext struct {
	context.Context
	check func()
}

func (ctx scaleP21HotWebhookContext) Err() error {
	ctx.check()
	return ctx.Context.Err()
}

func TestScaleP21HotWebhookRetainedTraversal(t *testing.T) {
	for _, phase := range []string{"replace", "cancel"} {
		t.Run(phase, func(t *testing.T) {
			workspace := "hot-webhook-retained"
			data := domain.Bootstrap{Loops: []domain.Loop{{Enabled: true, Status: "dra\x00ft", TriggerType: "event"}}}
			source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{})
			if err != nil {
				t.Fatal(err)
			}
			view := &SQLiteStore{metadataResidency: newMetadataResidency(), workspaces: map[string]domain.Bootstrap{workspace: {}}, lastWorkspaceKey: workspace}
			view.installMetadataLogicalSource(workspace, source)
			defer view.releaseMetadataLogicalSources()
			view.chatSnapshotFlags.Store(workspace, false)
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
				// VisitFields has retained the source after the owned
				// handle. Change installation at its next checkpoint.
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
				view.mu.Lock()
				view.lastWorkspaceKey = "changed-default"
				view.mu.Unlock()
			}}
			got, err := view.residentWebhookNeeded(ctx, "")
			if !entered {
				t.Fatal("predicate did not reach retained traversal checkpoint")
			}
			if phase == "cancel" {
				if !errors.Is(err, context.Canceled) {
					t.Fatal("in-flight cancellation lost", err)
				}
			} else if err != nil || !got {
				t.Fatal("predicate switched generation after installation", got, err)
			}
			source.mu.Lock()
			refs := source.refs
			source.mu.Unlock()
			expected := 0
			if phase == "cancel" {
				expected = 1
			}
			if refs != expected || view.metadataResidency.work.used != 0 {
				t.Fatalf("retained traversal leaked: refs=%d expected=%d work=%d", refs, expected, view.metadataResidency.work.used)
			}
		})
	}
}

func TestScaleP21HotWebhookPointWriterWarmPreviousValues(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_NARROW_MUTATION_VERIFY", "off")
	f := scaleP09Database(t)
	workspace, target := f.data.Workspace.URLKey, idgen.New("hot-webhook-target-")
	loop := idgen.New("hot-webhook-loop-")
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "projects", "loops", "webhooks", "integrationConnections"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, domain.Project{ID: target, Name: "Before", TeamIDs: []string{f.data.Teams[0].ID}})
		data.Loops = []domain.Loop{{ID: loop, Enabled: true, Status: "dra\x00ft", TriggerType: "event"}}
		data.Webhooks, data.IntegrationConnections = nil, nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	preimage, found := f.WorkspaceMetadata(workspace)
	if !found {
		t.Fatal("owned workspace missing")
	}
	row := func(field, id string) []byte {
		t.Helper()
		var raw []byte
		if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`, workspace, field, id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	original, loopRow := row("projects", target), row("loops", loop)
	if canonical, err := f.residentWebhookNeeded(t.Context(), workspace); err != nil || canonical {
		t.Fatal("canonical fixture unexpectedly retained the warm loop predicate", canonical, err)
	}
	var legacy, bounded domain.DomainEvent
	f.SetWebhookSink(func(_ string, event domain.DomainEvent) { legacy = event })
	scope := MetadataRecordScope{Write: []RecordKey{{Kind: "projects", ID: target}}}
	ctx := WithMetadataRecords(t.Context(), scope)
	mutate := func(data *domain.Bootstrap) error {
		if len(data.Projects) != 1 || data.Projects[0].ID != target {
			t.Fatal("point writer loaded unrelated projects")
		}
		data.Projects[0].Name = "After"
		return nil
	}
	payload := map[string]string{"name": "After"}
	if err := f.MutateWorkspace(ctx, workspace, "project.updated", target, payload, mutate); err != nil {
		t.Fatal(err)
	}
	if len(legacy.PreviousValues) == 0 {
		t.Fatal("literal point writer did not emit previous values")
	}
	want := row("projects", target)
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='projects' AND record_key=?`, original, workspace, target); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.setWorkspaceLocked(workspace, preimage)
	f.mu.Unlock()
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	view.SetWebhookSink(func(_ string, event domain.DomainEvent) { bounded = event })
	view.chatSnapshotFlags.Store(workspace, false)
	observed := WithSQLStatementObserver(ctx, func(query string) {
		if strings.Contains(query, "SELECT CASE WHEN EXISTS(SELECT 1 FROM workspace_metadata_records") {
			t.Error("bounded point writer used canonical webhook predicate")
		}
	})
	if err := view.MutateWorkspace(observed, workspace, "project.updated", target, payload, mutate); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(legacy.PreviousValues, bounded.PreviousValues) || !bytes.Equal(legacy.Payload, bounded.Payload) ||
		!bytes.Equal(want, row("projects", target)) || !bytes.Equal(loopRow, row("loops", loop)) {
		t.Fatalf("point writer changed rows or publication: previous=%s/%s", legacy.PreviousValues, bounded.PreviousValues)
	}
	if view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 {
		t.Fatal("point writer decoded complete metadata or leaked predicate work")
	}
}

func TestScaleP21HotWebhookMissingApplicationFallback(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	if err := f.EnsureChatIntegrationsSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	workspace, connection := idgen.New("hot-webhook-orphan-"), idgen.New("hot-webhook-chat-")
	t.Cleanup(func() {
		if _, err := f.db.ExecContext(context.Background(), `DELETE FROM chat_integrations WHERE connection_id=?`, connection); err != nil {
			t.Error(err)
		}
	})
	view := &SQLiteStore{db: f.db, dialect: f.dialect, metadataResidency: newMetadataResidency(), workspaces: map[string]domain.Bootstrap{}}
	for _, connected := range []bool{false, true} {
		if err := f.PutChatIntegration(t.Context(), domain.ChatIntegrationRecord{
			WorkspaceKey: workspace, ConnectionID: connection,
			Provider: "feishu", AppID: connection, TenantKey: connection, Connected: connected,
		}); err != nil {
			t.Fatal(err)
		}
		f.forgetChatSnapshotFlag(workspace)
		view.forgetChatSnapshotFlag(workspace)
		want, offErr := f.residentWebhookNeeded(t.Context(), workspace)
		chatQueries := 0
		ctx := WithSQLStatementObserver(t.Context(), func(query string) {
			if strings.Contains(query, "workspace_metadata_records") {
				t.Error("bounded missing-key fallback queried canonical metadata")
			}
			if strings.Contains(query, "FROM chat_integrations") {
				chatQueries++
			}
		})
		got, err := view.residentWebhookNeeded(ctx, workspace)
		if offErr != nil || err != nil || got != want || got != connected || chatQueries != 1 {
			t.Fatalf("orphan application fallback: bounded=%v literal=%v connected=%v queries=%d errors=%v/%v", got, want, connected, chatQueries, err, offErr)
		}
	}
	if view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 {
		t.Fatal("missing-key fallback decoded metadata or retained work")
	}
}
