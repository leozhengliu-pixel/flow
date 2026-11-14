package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21SW28AggregateLateCancellationRetainsPublicationWork(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, id := f.data.Workspace.URLKey, idgen.New("nested_sw28_publication_")
	body := strings.Repeat("legal publication winner ", 18000)
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "settings", "documents", "webhooks"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
		data.Settings["sw28-publication"] = "before"
		data.Documents = []domain.Document{{
			ID: "document_sw28_publication", ContentData: map[string]any{
				"container": map[string]any{"id": id, "body": body},
			},
		}}
		data.Webhooks = []domain.Webhook{{ID: "hook_sw28_publication", URL: "https://example.test/sw28-publication", Enabled: true}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	repo, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var webhook, realtime bool
	held := func() int {
		repo.metadataResidency.work.mu.Lock()
		defer repo.metadataResidency.work.mu.Unlock()
		return repo.metadataResidency.work.used
	}
	checkWinner := func(raw json.RawMessage) {
		t.Helper()
		var envelope struct {
			Entity struct {
				ID   string `json:"id"`
				Body string `json:"body"`
			} `json:"entity"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Entity.ID != id || envelope.Entity.Body != body {
			t.Errorf("publication lost nested winner: bytes=%d err=%v", len(raw), err)
		}
		if held() <= 8<<20 {
			t.Error("publication released winner reservation before sink returned")
		}
	}
	repo.SetWebhookSink(func(_ string, event domain.DomainEvent) {
		webhook = true
		checkWinner(event.Snapshot)
		// The transaction is already committed. Cancellation here must not
		// discard its realtime publication or release owned bytes early.
		cancel()
	})
	repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
		realtime = true
		if ctx.Err() != context.Canceled {
			t.Error("realtime sink did not observe late cancellation")
		}
		checkWinner(event.Payload)
	})
	if err := repo.MutateWorkspaceScoped(ctx, workspace, "application_policy.updated", id, nil,
		MetadataRecordScope{Write: []RecordKey{{Kind: "settings", ID: "sw28-publication"}}},
		func(data *domain.Bootstrap) error {
			data.Settings["sw28-publication"] = "after"
			return nil
		}); err != nil {
		t.Fatal(err)
	}
	if !webhook || !realtime || held() != 0 {
		t.Fatalf("late publication webhook=%t realtime=%t retainedWork=%d", webhook, realtime, held())
	}
	var stored []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='settings' AND record_key='sw28-publication'`, workspace).Scan(&stored); err != nil || string(stored) != `"after"` {
		t.Fatalf("late cancellation changed committed target: %s err=%v", stored, err)
	}
}
