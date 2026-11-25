package store

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21ResidentPointWriterMatchesLegacyRowsAndHooks(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_NARROW_MUTATION_VERIFY", "off")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.seed", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, domain.Project{ID: "p21-edit", Name: "Before", Description: "kept", TeamIDs: []string{data.Teams[0].ID}}, domain.Project{ID: "p21-unrelated", Description: strings.Repeat("unrelated", 100000)})
		data.Webhooks = append(data.Webhooks, domain.Webhook{ID: "p21-hook", URL: "https://example.test/hook", Enabled: true})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	row := func(id string) []byte {
		t.Helper()
		var raw []byte
		if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='projects' AND record_key=?`, workspace, id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	original, unrelated := row("p21-edit"), row("p21-unrelated")
	var legacyHook, residentHook domain.DomainEvent
	var legacyRealtime, residentRealtime domain.RealtimeEvent
	f.SetWebhookSink(func(_ string, event domain.DomainEvent) { legacyHook = event })
	f.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { legacyRealtime = event })
	mutate := func(data *domain.Bootstrap) error {
		if len(data.Projects) != 1 || data.Projects[0].ID != "p21-edit" {
			t.Fatal("point callback received unrelated bodies")
		}
		data.Projects[0].Name = "After"
		data.AuditLog = append(data.AuditLog, domain.AuditLogEntry{ID: idgen.New("p21-audit-"), Action: "p21.point", ResourceType: "project", ResourceID: "p21-edit", Actor: f.data.Viewer, CreatedAt: time.Now().UTC()})
		return nil
	}
	scope := MetadataRecordScope{Write: []RecordKey{{"projects", "p21-edit"}}}
	ctx := WithMetadataRecords(t.Context(), scope)
	if err := f.MutateWorkspace(ctx, workspace, "project.updated", "p21-edit", map[string]string{"name": "After"}, mutate); err != nil {
		t.Fatal(err)
	}
	want := row("p21-edit")
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='projects' AND record_key='p21-edit'`, original, workspace); err != nil {
		t.Fatal(err)
	}
	probe := scaleP21ResidentProbe(f.SQLiteStore)
	probe.viewer = f.viewer
	if err := probe.loadResidentWorkspaces(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := probe.captureLogicalTestView(t.Context(), f.SQLiteStore, []string{workspace}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(probe.releaseMetadataLogicalSources)
	probe.SetWebhookSink(func(_ string, event domain.DomainEvent) { residentHook = event })
	probe.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { residentRealtime = event })
	if err := probe.MutateWorkspace(ctx, workspace, "project.updated", "p21-edit", map[string]string{"name": "After"}, mutate); err != nil {
		t.Fatal(err)
	}
	var before, after any
	if json.Unmarshal(want, &before) != nil || json.Unmarshal(row("p21-edit"), &after) != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("resident point writer changed the canonical result")
	}
	if string(unrelated) != string(row("p21-unrelated")) {
		t.Fatal("resident point writer changed an unrelated row")
	}
	if !reflect.DeepEqual(legacyHook.Payload, residentHook.Payload) || !reflect.DeepEqual(legacyHook.PreviousValues, residentHook.PreviousValues) || !reflect.DeepEqual(legacyRealtime.Payload, residentRealtime.Payload) {
		t.Fatalf("resident point writer changed webhook or realtime payloads: payload=%s/%s previous=%s/%s realtime=%s/%s", legacyHook.Payload, residentHook.Payload, legacyHook.PreviousValues, residentHook.PreviousValues, legacyRealtime.Payload, residentRealtime.Payload)
	}
	var audits int
	if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM audit_events WHERE workspace_id=? AND action='p21.point'`, f.data.Workspace.ID).Scan(&audits); err != nil || audits != 2 {
		t.Fatalf("audit side effects differ: count=%d error=%v", audits, err)
	}
	if probe.WorkspaceMetadataDecodes() != 0 || probe.metadataResidency.work.used != 0 {
		t.Fatal("resident point writer decoded full metadata or leaked work admission")
	}
	installed, _ := probe.storedWorkspace(workspace)
	if len(installed.Projects) != 0 {
		t.Fatal("point writer installed a body collection")
	}
	if err := probe.MutateWorkspace(ctx, workspace, "project.updated", "p21-edit", nil, func(data *domain.Bootstrap) error {
		data.Projects = nil
		return nil
	}); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatalf("unported deletion was accepted: %v", err)
	}
	if string(row("p21-edit")) != string(want) {
		t.Fatal("rejected point deletion committed a partial mutation")
	}
	called := false
	document := WithDocumentRecordSelection(WithMutationScope(t.Context(), MutationScope{}), "on", func(domain.Bootstrap, string, any) (MetadataRecordScope, bool) {
		return MetadataRecordScope{Fields: []string{"documents"}}, true
	})
	if err := probe.MutateWorkspace(document, workspace, "document.deleted", "unported", nil, func(*domain.Bootstrap) error { called = true; return nil }); !errors.Is(err, ErrBoundedMetadataCoverage) || called {
		t.Fatalf("unported P17 selector bypassed the activation coverage: called=%v error=%v", called, err)
	}
}

func TestScaleP21ConversationReadsAndBackgroundWritesShareAdmission(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_NARROW_MUTATION_VERIFY", "off")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.seed", "", nil, func(data *domain.Bootstrap) error {
		data.AIConversations = append(data.AIConversations,
			domain.AIConversation{ID: "p21-conversation", UserID: f.data.Viewer.ID, Title: "Before"},
			domain.AIConversation{ID: "p21-large-context", UserID: f.data.Viewer.ID, Context: map[string]any{"text": strings.Repeat("x", 500000)}})
		data.AgentSessions = append(data.AgentSessions, domain.AgentSession{ID: "p21-session", UserID: f.data.Viewer.ID, Title: "Owned"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	probe := scaleP21ResidentProbe(f.SQLiteStore)
	probe.viewer = f.viewer
	if err := probe.loadResidentWorkspaces(t.Context()); err != nil {
		t.Fatal(err)
	}
	access, err := probe.ResolveAccess(t.Context(), workspace, f.data.Viewer.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := probe.AIConversationRecord(t.Context(), access, "p21-large-context"); !errors.Is(err, ErrMetadataPageBudget) {
		t.Fatalf("oversized conversation bypassed admission: %v", err)
	}
	if item, err := probe.AgentConversationByID(t.Context(), access, "p21-session", false); err != nil || item.Title != "Owned" {
		t.Fatalf("native worker lookup failed: %v", err)
	}
	ctx := WithMetadataRecords(t.Context(), MetadataRecordScope{Write: []RecordKey{{"aiConversations", "p21-conversation"}}})
	if err := probe.MutateWorkspace(ctx, workspace, "ai.conversation_updated", "p21-conversation", nil, func(data *domain.Bootstrap) error {
		if len(data.AIConversations) != 1 {
			t.Fatal("background write hydrated unrelated contexts")
		}
		data.AIConversations[0].Title = "Background result"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	access, err = probe.ResolveAccess(t.Context(), workspace, f.data.Viewer.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if item, err := probe.AIConversationRecord(t.Context(), access, "p21-conversation"); err != nil || item.Title != "Background result" {
		t.Fatalf("native read lost background output: %v", err)
	}
	probe.metadataResidency.work.limit = 1
	called := false
	if err := probe.MutateWorkspace(ctx, workspace, "ai.conversation_updated", "p21-conversation", nil, func(*domain.Bootstrap) error { called = true; return nil }); !errors.Is(err, ErrMetadataWorkBudget) || called {
		t.Fatalf("internal write bypassed admission: called=%v error=%v", called, err)
	}
	if probe.WorkspaceMetadataDecodes() != 0 || probe.metadataResidency.work.used != 0 {
		t.Fatal("native reads/background writes decoded full metadata or leaked admission")
	}
}
