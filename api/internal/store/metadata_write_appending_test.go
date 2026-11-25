package store

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21SW28AppendingAggregateUsesCreatedRecords(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	firstID, secondID := idgen.New("conversation_sw28_first_"), idgen.New("conversation_sw28_second_")
	when := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	first := domain.AIConversation{ID: firstID, Title: "First created", UserID: f.data.Viewer.ID, CreatedAt: when, UpdatedAt: when}
	second := domain.AIConversation{ID: secondID, Title: "Second created", UserID: f.data.Viewer.ID, CreatedAt: when, UpdatedAt: when}
	type outcome struct {
		rows     []scaleP21SW0AppendRow
		webhook  domain.DomainEvent
		realtime domain.RealtimeEvent
	}
	var results []outcome
	for _, bounded := range []bool{false, true} {
		f.SetWebhookSink(nil)
		f.SetRealtimeSink(nil)
		if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
		if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "aiConversations", "webhooks"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
			data.AIConversations = []domain.AIConversation{{ID: "existing_sw28", Title: "Before"}}
			data.Webhooks = []domain.Webhook{{ID: "hook_sw28_append", URL: "https://example.test/sw28-append", Enabled: true}}
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
		got := outcome{}
		repo.SetWebhookSink(func(_ string, event domain.DomainEvent) { got.webhook = event })
		repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { got.realtime = event })
		err := repo.MutateWorkspaceAppendingWithAggregate(
			WithMetadataFields(t.Context(), "aiConversations"), workspace, "ai.conversation_created", nil,
			MetadataAppendScope{
				MetadataRecordScope: MetadataRecordScope{Write: []RecordKey{{Kind: "aiConversations", ID: "existing_sw28"}}},
				AppendKinds:         []string{"aiConversations"},
			},
			func(data *domain.Bootstrap) (string, error) {
				data.AIConversations[0].Title = "After"
				data.AIConversations = append(data.AIConversations, first, second)
				return firstID, nil
			})
		if err != nil {
			t.Fatal(err)
		}
		got.rows = scaleP21SW0AppendRows(t, repo, workspace)
		var envelope struct {
			Entity domain.AIConversation `json:"entity"`
		}
		if err := json.Unmarshal(got.realtime.Payload, &envelope); err != nil || envelope.Entity.ID != firstID {
			t.Fatalf("bounded=%t append publication omitted created aggregate: entity=%q payload=%s err=%v", bounded, envelope.Entity.ID, got.realtime.Payload, err)
		}
		if bounded && (repo.WorkspaceMetadataDecodes() != 0 || repo.metadataResidency.work.used != 0) {
			t.Fatal("append aggregate decoded full workspace or leaked work")
		}
		results = append(results, got)
	}
	if !reflect.DeepEqual(results[0].rows, results[1].rows) ||
		!jsonEqual(results[0].webhook.Payload, results[1].webhook.Payload) ||
		!jsonEqual(results[0].webhook.PreviousValues, results[1].webhook.PreviousValues) ||
		!jsonEqual(results[0].webhook.Snapshot, results[1].webhook.Snapshot) ||
		!jsonEqual(results[0].realtime.Payload, results[1].realtime.Payload) {
		t.Fatalf("append aggregate rows=%t previous=%s/%s snapshot=%s/%s realtime=%s/%s",
			reflect.DeepEqual(results[0].rows, results[1].rows),
			results[0].webhook.PreviousValues, results[1].webhook.PreviousValues,
			results[0].webhook.Snapshot, results[1].webhook.Snapshot,
			results[0].realtime.Payload, results[1].realtime.Payload)
	}
}
