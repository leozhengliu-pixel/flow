package store

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21InlinePostimageUnicodeBytes(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	target, duplicate := idgen.New("unicode-hook-"), idgen.New("unicode-duplicate-")
	at := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	failure := domain.WebhookFailureEvent{
		ID: idgen.New("unicode-new-"), WebhookID: target, ExecutionID: "execution",
		ResponseOrError: "\u4e2d\u6587 \u00e9", CreatedAt: at,
	}
	var expected []byte
	for _, bounded := range []bool{false, true} {
		f := scaleP09Database(t)
		workspace := f.data.Workspace.URLKey
		if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "webhooks", "webhookFailureEvents"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
			data.Webhooks = []domain.Webhook{{ID: target, URL: "https://example.test/hook", Enabled: true}}
			item := domain.WebhookFailureEvent{ID: duplicate, WebhookID: target, ResponseOrError: "\u4e2d\u6587", CreatedAt: at}
			data.WebhookFailureEvents = []domain.WebhookFailureEvent{item, item}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if f.dialect == "sqlite" {
			if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_states SET data=CAST(data AS TEXT) WHERE workspace_key=?`, workspace); err != nil {
				t.Fatal(err)
			}
			var characters, octets int
			if err := f.db.QueryRowContext(t.Context(), `SELECT LENGTH(data),LENGTH(CAST(data AS BLOB)) FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&characters, &octets); err != nil || characters >= octets {
				t.Fatal("Unicode TEXT fixture did not distinguish bytes from characters", characters, octets, err)
			}
		}
		var actual []domain.WebhookFailureEvent
		if !bounded {
			if err := scaleP21WebhookFailureLiteral(t.Context(), f.SQLiteStore, workspace, failure, 502); err != nil {
				t.Fatal(err)
			}
			data, _ := f.WorkspaceMetadata(workspace)
			actual = data.WebhookFailureEvents
		} else {
			view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			if err := view.RecordWebhookDeliveryFailure(t.Context(), workspace, failure, 502); err != nil {
				t.Fatal("Unicode root byte preflight rejected the legacy write", err)
			}
			actual = scaleP21WebhookFailureSource(t, view, workspace)
			if view.metadataResidency.work.used != 0 {
				t.Fatal("Unicode inline write leaked work admission")
			}
		}
		raw, err := json.Marshal(actual)
		if err != nil {
			t.Fatal(err)
		}
		if !bounded {
			expected = raw
		} else if !bytes.Equal(expected, raw) {
			t.Fatalf("Unicode inline postimage differs\nlegacy=%s\nbounded=%s", expected, raw)
		}
	}
}
