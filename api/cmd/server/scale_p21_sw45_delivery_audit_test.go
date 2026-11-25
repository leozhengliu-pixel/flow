package main

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW45DeliveryAuditDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		target := idgen.New("sw45-hook-")
		fixed := time.Unix(1700000000, 0).UTC()
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, operation := range []string{"cursor", "audit"} {
				t.Run(operation, func(t *testing.T) {
					var outcomes []any
					for _, source := range []*store.SQLiteStore{repo, f.probe} {
						repo.SetWebhookSink(nil)
						repo.SetRealtimeSink(nil)
						if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw45.reset", "", nil, func(data *domain.Bootstrap) error {
							data.Webhooks = []domain.Webhook{{ID: target, AuditLog: true, Enabled: true, CreatedAt: fixed, UpdatedAt: fixed}}
							data.Drafts = []domain.Draft{{ID: target + "-draft", ContentData: map[string]any{
								"id": target, "marker": "earlier nested aggregate", "hiddenCount": 37,
							}}}
							return nil
						}); err != nil {
							t.Fatal(err)
						}
						if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
							t.Fatal(err)
						}
						var webhook domain.DomainEvent
						var realtime domain.RealtimeEvent
						webhookCount, realtimeCount := 0, 0
						source.SetWebhookSink(func(_ string, event domain.DomainEvent) {
							webhook, webhookCount = event, webhookCount+1
						})
						source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
							realtime, realtimeCount = event, realtimeCount+1
						})
						service := &server{store: source}
						started := time.Now().UTC()
						switch operation {
						case "cursor":
							service.mutateAuditStream(workspace, target, func(item *domain.Webhook) {
								item.AuditCursorID, item.AuditCursorSeq = "exact-cursor", 7
								item.NextAttemptAt = &fixed
							})
						case "audit":
							ctx := f.literalRequest(user, key, nil).Context()
							ctx, cancel := context.WithCancel(ctx)
							cancel()
							service.auditChatIntegrationContext(ctx, workspace, "sw45", "chat_binding", target, map[string]any{"team": f.public})
						}
						if webhookCount != 1 || realtimeCount != 1 || webhook.Type == "" || realtime.Type == "" {
							t.Fatal("delivery helper did not publish its original effects", webhookCount, realtimeCount)
						}
						var payload struct {
							Entity struct {
								Marker      string `json:"marker"`
								HiddenCount int    `json:"hiddenCount"`
							} `json:"entity"`
						}
						if err := json.Unmarshal(realtime.Payload, &payload); err != nil || payload.Entity.Marker != "earlier nested aggregate" || payload.Entity.HiddenCount != 37 {
							t.Fatalf("nested aggregate precedence changed: %s (%v)", realtime.Payload, err)
						}
						audits, err := repo.ListAuditEntries(t.Context(), f.data.Workspace.ID, store.AuditQuery{Since: started, ResourceID: target})
						if err != nil {
							t.Fatal(err)
						}
						expectedAudits := 0
						if operation == "audit" {
							expectedAudits = 1
						}
						if len(audits) != expectedAudits {
							t.Fatalf("audits=%d want=%d", len(audits), expectedAudits)
						}
						for i := range audits {
							audits[i].ID, audits[i].Hash, audits[i].Seq, audits[i].CreatedAt = "", "", 0, time.Time{}
						}
						webhook.ID, webhook.CreatedAt = "", time.Time{}
						realtime.ID, realtime.Seq, realtime.CreatedAt = "", 0, time.Time{}
						outcomes = append(outcomes, []any{
							scaleP21M2Canonical(t, repo, workspace, store.RecordKey{Kind: "webhooks", ID: target}),
							webhook, realtime, audits,
						})
						source.SetWebhookSink(nil)
						source.SetRealtimeSink(nil)
						if source.MetadataWorkUsedForTesting() != 0 {
							t.Fatal("delivery helper retained work after publication")
						}
					}
					if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
						t.Fatalf("delivery helper changed: %s", scaleP21M2Diff(outcomes[0], outcomes[1]))
					}
				})
			}
		})
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("delivery helper decoded full metadata")
		}
	}
}
