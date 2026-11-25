package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21SW31AuditNativeAggregateID(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, id, templateID := f.data.Workspace.URLKey, idgen.New("emoji_sw31_audit_"), idgen.New("template_sw31_audit_")
	type outcome struct {
		row      string
		event    domain.DomainEvent
		realtime domain.RealtimeEvent
	}
	var results []outcome
	for _, bounded := range []bool{false, true} {
		f.SetWebhookSink(nil)
		f.SetRealtimeSink(nil)
		if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "issueTemplates", "customEmojis", "webhooks"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
			data.IssueTemplates = []domain.IssueTemplate{{ID: templateID, Name: "Unrelated", FormFields: []domain.TemplateFormField{{ID: strings.Repeat("x", 8192)}}}}
			data.CustomEmojis = []domain.CustomEmoji{{ID: id, Name: "Before"}}
			data.Webhooks = []domain.Webhook{{ID: "hook_sw31_audit", URL: "https://example.test/sw31", Enabled: true}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		repo := f.SQLiteStore
		if bounded {
			var err error
			repo, err = f.BoundedMetadataViewForTesting(t.Context())
			if err != nil {
				t.Fatal(err)
			}
		}
		result := outcome{}
		repo.SetWebhookSink(func(_ string, event domain.DomainEvent) { result.event = event })
		repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { result.realtime = event })
		err := repo.MutateWorkspaceScoped(context.Background(), workspace, "custom_emoji.updated", id, nil,
			MetadataRecordScope{Read: []RecordKey{{Kind: "issueTemplates", ID: templateID}}, Write: []RecordKey{{Kind: "customEmojis", ID: id}}},
			func(data *domain.Bootstrap) error {
				data.CustomEmojis[0].Name = "After"
				return nil
			})
		if err != nil {
			t.Fatalf("bounded=%t: %v", bounded, err)
		}
		var raw []byte
		if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='customEmojis' AND record_key=?`, workspace, id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		result.row = string(raw)
		results = append(results, result)
	}
	if results[0].row != results[1].row || !jsonEqual(results[0].event.PreviousValues, results[1].event.PreviousValues) ||
		!jsonEqual(results[0].event.Snapshot, results[1].event.Snapshot) || !jsonEqual(results[0].realtime.Payload, results[1].realtime.Payload) {
		left, _ := json.Marshal(results[0])
		right, _ := json.Marshal(results[1])
		t.Fatalf("producer outcomes differ: %s / %s", left, right)
	}
}

func TestScaleP21SW31AuditSynchronousSinkMutation(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, bounded := range []bool{false, true} {
		f := scaleP09Database(t)
		workspace := f.data.Workspace.URLKey
		repo := f.SQLiteStore
		if bounded {
			var err error
			repo, err = f.BoundedMetadataViewForTesting(t.Context())
			if err != nil {
				t.Fatal(err)
			}
		}
		called := false
		var nestedErr error
		repo.SetWebhookSink(func(_ string, _ domain.DomainEvent) {
			called = true
			repo.SetWebhookSink(nil)
			nestedErr = repo.MutateWorkspaceScoped(context.Background(), workspace, "application_policy.updated", "workspace", nil,
				MetadataRecordScope{Fields: []string{"workspaceSettings"}}, func(data *domain.Bootstrap) error {
					data.WorkspaceSettings.AgentInstructions = "nested"
					return nil
				})
		})
		err := repo.MutateWorkspaceScoped(ContextWithActor(context.Background(), f.data.Viewer), workspace, "application_policy.updated", "sw31-outer", nil,
			MetadataRecordScope{Write: []RecordKey{{Kind: "settings", ID: "sw31-outer"}}}, func(data *domain.Bootstrap) error {
				data.Settings["sw31-outer"] = "after"
				return nil
			})
		if err != nil || nestedErr != nil || !called {
			t.Fatalf("bounded=%t outer=%v nested=%v called=%t", bounded, err, nestedErr, called)
		}
	}
}
