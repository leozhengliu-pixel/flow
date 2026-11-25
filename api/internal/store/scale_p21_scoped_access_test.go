package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21ScopedMetadataLegacyBranch(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	expected, _ := f.WorkspaceMetadata(workspace)
	got, ok, err := f.WorkspaceMetadataScoped(t.Context(), workspace, MetadataRecordScope{Fields: []string{"invalid"}})
	if err != nil || !ok || !reflect.DeepEqual(got, expected) {
		t.Fatal("off mode did not call the exact legacy read")
	}
	fields, _ := f.WorkspaceMetadataFields(workspace, "workspaceSettings")
	got, ok, err = f.WorkspaceMetadataFieldsScoped(t.Context(), workspace, MetadataRecordScope{Fields: []string{"invalid"}}, "workspaceSettings")
	if err != nil || !ok || !reflect.DeepEqual(got, fields) {
		t.Fatal("off mode changed field-only read")
	}
	// Invalid bounded declarations are ignored on the old branch, including
	// with hydration disabled. Existing callbacks still receive legacy data.
	t.Setenv("FLOW_RECORD_HYDRATION", "off")
	err = f.MutateWorkspaceScoped(t.Context(), workspace, "scale_p21.legacy", "", nil, MetadataRecordScope{Fields: []string{"invalid"}}, func(data *domain.Bootstrap) error {
		if len(data.Teams) != len(expected.Teams) {
			t.Fatal("legacy mutation input was narrowed")
		}
		return ErrNoMutation
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestScaleP21ScopedMetadataWritesMatchCanonicalHooks(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	scope := MetadataRecordScope{Write: []RecordKey{{"settings", "p21-target"}}}
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.seed", "", nil, func(data *domain.Bootstrap) error {
		data.Settings["p21-target"] = map[string]any{"name": "before"}
		data.Settings["p21-unrelated"] = map[string]any{"body": strings.Repeat("unrelated ", 40000)}
		data.Webhooks = append(data.Webhooks, domain.Webhook{ID: "p21-hook", Enabled: true, URL: "https://example.test/hook"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	row := func(field, id string) []byte {
		t.Helper()
		var raw []byte
		if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`, workspace, field, id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	original, unrelated := row("settings", "p21-target"), row("settings", "p21-unrelated")
	var beforeEvent, afterEvent domain.DomainEvent
	var beforeRealtime, afterRealtime domain.RealtimeEvent
	f.SetWebhookSink(func(_ string, event domain.DomainEvent) { beforeEvent = event })
	f.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { beforeRealtime = event })
	callback := func(data *domain.Bootstrap) error {
		data.Settings["p21-target"] = map[string]any{"name": "after"}
		data.AuditLog = append(data.AuditLog, domain.AuditLogEntry{ID: "p21-audit-" + time.Now().Format("150405.000000000"), Action: "p21.scoped", ResourceType: "settings", ResourceID: "p21-target", Actor: f.data.Viewer, CreatedAt: time.Now().UTC()})
		return nil
	}
	preimage, _ := f.storedWorkspace(workspace)
	preimage = cloneBootstrap(preimage)
	if err := f.MutateWorkspaceScoped(t.Context(), workspace, "application_policy.updated", "p21-target", nil, scope, callback); err != nil {
		t.Fatal(err)
	}
	want := row("settings", "p21-target")
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='settings' AND record_key='p21-target'`, original, workspace); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.setWorkspaceLocked(workspace, preimage)
	f.mu.Unlock()
	probe, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	probe.SetWebhookSink(func(_ string, event domain.DomainEvent) { afterEvent = event })
	probe.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { afterRealtime = event })
	if err := probe.MutateWorkspaceScoped(t.Context(), workspace, "application_policy.updated", "p21-target", nil, scope, callback); err != nil {
		t.Fatal(err)
	}
	if string(want) != string(row("settings", "p21-target")) || string(unrelated) != string(row("settings", "p21-unrelated")) {
		t.Fatal("scoped write changed target semantics or an unrelated row")
	}
	if !reflect.DeepEqual(beforeEvent.Payload, afterEvent.Payload) || !reflect.DeepEqual(beforeEvent.PreviousValues, afterEvent.PreviousValues) || !reflect.DeepEqual(beforeRealtime.Payload, afterRealtime.Payload) {
		t.Fatal("scoped write changed hooks")
	}
	var audits int
	if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM audit_events WHERE workspace_id=? AND action='p21.scoped'`, f.data.Workspace.ID).Scan(&audits); err != nil || audits != 2 {
		t.Fatalf("audit count=%d error=%v", audits, err)
	}
	for _, phase := range []string{"insert", "delete"} {
		if err := probe.MutateWorkspaceScoped(t.Context(), workspace, "application_policy.updated", "p21-new", nil, MetadataRecordScope{Write: []RecordKey{{"settings", "p21-new"}}}, func(data *domain.Bootstrap) error {
			if phase == "insert" {
				data.Settings["p21-new"] = "new"
			} else {
				delete(data.Settings, "p21-new")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if string(unrelated) != string(row("settings", "p21-unrelated")) {
		t.Fatal("map insertion/deletion changed an unrelated row")
	}
	for _, mutate := range []func(*domain.Bootstrap) error{
		func(data *domain.Bootstrap) error { data.Settings["undeclared"] = "bad"; return nil },
		func(data *domain.Bootstrap) error { data.Projects = []domain.Project{{ID: "undeclared"}}; return nil },
		func(data *domain.Bootstrap) error { data.Notifications = []domain.Notification{}; return nil },
	} {
		if err := probe.MutateWorkspaceScoped(t.Context(), workspace, "application_policy.updated", "p21-target", nil, scope, mutate); !errors.Is(err, ErrMetadataRecordQuery) {
			t.Fatalf("undeclared mutation accepted: %v", err)
		}
	}
	if string(want) != string(row("settings", "p21-target")) {
		t.Fatal("rejected mutation changed canonical data")
	}
	if probe.WorkspaceMetadataDecodes() != 0 || probe.metadataResidency.work.used != 0 {
		t.Fatal("scoped write decoded workspace or leaked admission")
	}
}

func TestScaleP21ScopedMetadataCompleteFieldAndPolicy(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.restore_links", "", nil, func(data *domain.Bootstrap) error {
		data.Trash = append(data.Trash, domain.TrashEntry{ID: "p21-linked-trash", ResourceID: "p21-deleted", Payload: json.RawMessage(`{}`), Links: &domain.TrashLinks{}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	probe, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	trash, ok, err := probe.WorkspaceMetadataScoped(t.Context(), workspace, MetadataRecordScope{Read: []RecordKey{{"trash", "p21-linked-trash"}}})
	if err != nil || !ok || len(trash.Trash) != 1 || trash.Trash[0].Links == nil {
		t.Fatal("trusted selection lost server-side restore links")
	}
	for _, unsafe := range []MetadataRecordScope{
		{Fields: []string{"settings"}},
		{Read: []RecordKey{{"settings", "posts.v1"}}},
		{Read: []RecordKey{{"settings", "meetings.v1"}}},
		{Write: []RecordKey{{"settings", "dashboards.v1"}}},
		{Fields: []string{"projects"}},
	} {
		if _, err := probe.ProjectScopedMetadata(t.Context(), workspace, f.data.Viewer.ID, nil, unsafe); !errors.Is(err, ErrMetadataRecordQuery) {
			t.Fatalf("generic projection accepted a field needing family authorization: %v", err)
		}
	}
	scope := MetadataRecordScope{Fields: []string{"webhooks"}}
	if err := probe.MutateWorkspaceScopedWithAggregate(t.Context(), workspace, "webhook.created", nil, scope, func(data *domain.Bootstrap) (string, error) {
		data.Webhooks = append(data.Webhooks, domain.Webhook{ID: "p21-create", Name: "created"})
		return "p21-create", nil
	}); err != nil {
		t.Fatal(err)
	}
	data, _, err := probe.WorkspaceMetadataScoped(t.Context(), workspace, scope)
	if err != nil || !slices.ContainsFunc(data.Webhooks, func(item domain.Webhook) bool { return item.ID == "p21-create" }) {
		t.Fatalf("create lost: %v", err)
	}
	if err := probe.MutateWorkspaceScoped(t.Context(), workspace, "webhook.deleted", "p21-create", nil, MetadataRecordScope{Write: []RecordKey{{"webhooks", "p21-create"}}}, func(data *domain.Bootstrap) error {
		data.Webhooks = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := probe.MutateWorkspaceScoped(t.Context(), workspace, "workspace.settings_updated", "", nil, MetadataRecordScope{Fields: []string{"workspaceSettings"}}, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.RestrictFileUploads = true
		data.WorkspaceSettings.AllowedFileExtensions = []string{"txt"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	policy, ok := probe.WorkspaceSettingsMetadata(workspace)
	if !ok || !policy.WorkspaceSettings.RestrictFileUploads || !slices.Equal(policy.WorkspaceSettings.AllowedFileExtensions, []string{"txt"}) {
		t.Fatal("policy write left stale resident settings")
	}
	if probe.metadataResidency.work.used != 0 {
		t.Fatal("policy writer leaked admission")
	}
}

func TestScaleP21ScopedMetadataScanBoundsAndCancellation(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.scan", "", nil, func(data *domain.Bootstrap) error {
		for i := 0; i < 121; i++ {
			raw, _ := json.Marshal(i)
			data.CustomEmojis = append(data.CustomEmojis, domain.CustomEmoji{ID: "p21-emoji-" + string(raw), Name: "emoji"})
		}
		data.CustomEmojis[0].ImageURL = strings.Repeat("large canonical body ", 20000)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	probe, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	omitted := 0
	if err := probe.ScanMetadataRecords(t.Context(), workspace, "customEmojis", func(page RecordBatch) error {
		if len(page.Items) != 1 {
			t.Fatal("scanner accumulated bodies")
		}
		count++
		if page.Items[0].Omitted {
			omitted++
		}
		return nil
	}); err != nil || count != 121 || omitted != 1 {
		t.Fatalf("scan count=%d error=%v", count, err)
	}
	var streamed bytes.Buffer
	streamCount := 0
	if err := probe.StreamMetadataRecords(t.Context(), workspace, "customEmojis", func(key RecordKey, body io.Reader) error {
		streamCount++
		if key.ID == "p21-emoji-0" {
			_, err := io.Copy(&streamed, body)
			return err
		}
		_, err := io.Copy(io.Discard, body)
		return err
	}); err != nil || streamCount != 121 || !bytes.Contains(streamed.Bytes(), []byte(strings.Repeat("large canonical body ", 20000))) {
		t.Fatalf("full canonical stream lost oversized body: count=%d error=%v", streamCount, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	err = probe.ScanMetadataRecords(ctx, workspace, "customEmojis", func(page RecordBatch) error { cancel(); return ctx.Err() })
	if !errors.Is(err, context.Canceled) || probe.metadataResidency.work.used != 0 {
		t.Fatal("scan did not release cancelled work")
	}
	if _, _, err := probe.WorkspaceMetadataScoped(t.Context(), workspace, MetadataRecordScope{Fields: []string{"customEmojis"}}); !errors.Is(err, ErrMetadataPageBudget) {
		t.Fatal("whole-field selection bypassed row bound")
	}
}
