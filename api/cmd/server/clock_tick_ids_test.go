package main

import (
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Values that used to be built from the clock second (or a past timestamp)
// stay distinct when the same thing happens twice in one second.
func TestClockTickIDsStayDistinct(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	service := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true}
	data := repository.Bootstrap()
	workspace := data.Workspace.URLKey

	t.Run("webhook disabled twice in one second", func(t *testing.T) {
		const webhookID = "webhook-twice-disabled"
		if err := repository.MutateWorkspace(t.Context(), workspace, "test.webhook", webhookID, nil, func(next *domain.Bootstrap) error {
			next.Webhooks = append(next.Webhooks, domain.Webhook{ID: webhookID, Name: "Flaky", URL: "https://hooks.example.test/flaky", Enabled: true, CreatorID: next.Viewer.ID})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC().Truncate(time.Second)
		service.disableFailingWebhook(workspace, webhookID, now)
		// Disabling an already disabled webhook notifies nobody.
		service.disableFailingWebhook(workspace, webhookID, now)
		if err := repository.MutateWorkspace(t.Context(), workspace, "test.webhook", webhookID, nil, func(next *domain.Bootstrap) error {
			index := slices.IndexFunc(next.Webhooks, func(item domain.Webhook) bool { return item.ID == webhookID })
			next.Webhooks[index].Enabled = true
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		service.disableFailingWebhook(workspace, webhookID, now.Add(300*time.Millisecond))
		ids := []string{}
		for _, item := range repository.Bootstrap().Notifications {
			if item.SourceType == "webhook" && item.SourceID == webhookID {
				ids = append(ids, item.ID)
			}
		}
		if len(ids) != 2 || ids[0] == ids[1] || !strings.HasPrefix(ids[0], "notification_webhook_disabled_"+webhookID+"_") {
			t.Fatalf("disabled notifications = %q, want two distinct", ids)
		}
	})

	t.Run("document baselines saved at the same instant", func(t *testing.T) {
		at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		before := domain.Document{ID: "doc-a", Title: "A", Content: "Body", CreatedAt: at, UpdatedAt: at}
		first, second := before, before
		second.ID = "doc-b"
		keepDocumentBaseline(&first, before)
		keepDocumentBaseline(&second, second)
		if len(first.Revisions) != 1 || len(second.Revisions) != 1 || first.Revisions[0].ID == second.Revisions[0].ID || !first.Revisions[0].CreatedAt.Equal(at) {
			t.Fatalf("baselines = %#v / %#v", first.Revisions, second.Revisions)
		}
	})

	t.Run("custom intake address reset twice in one second", func(t *testing.T) {
		handler := newHandler(service)
		base := "/api/teams/" + data.Teams[0].ID + "/email-intake-addresses"
		created := requestJSON[intakeCreateResponse](t, handler, http.MethodPost, base, map[string]any{"type": "team", "localPart": "support", "domain": "acme.test"}, http.StatusCreated)
		type rotateResponse struct {
			Address emailIntakeAddressView `json:"address"`
		}
		seen := map[string]bool{created.Address.Address: true}
		for range 2 {
			rotated := requestJSON[rotateResponse](t, handler, http.MethodPost, base+"/"+created.Address.ID+"/rotate", nil, http.StatusOK)
			if seen[rotated.Address.Address] || !strings.HasPrefix(rotated.Address.Address, "support-") {
				t.Fatalf("reset reused or misshaped the address: %q (seen %v)", rotated.Address.Address, seen)
			}
			seen[rotated.Address.Address] = true
		}
	})
}
