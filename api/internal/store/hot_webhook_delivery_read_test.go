package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func scaleP21WebhookRoutingProjection(data domain.Bootstrap) []domain.Webhook {
	var result []domain.Webhook
	for _, hook := range data.Webhooks {
		if !hook.Enabled || hook.AuditLog || hook.URL == "" {
			continue
		}
		result = append(result, domain.Webhook{
			ID: hook.ID, URL: hook.URL, ResourceTypes: hook.ResourceTypes, TeamIDs: hook.TeamIDs,
			TeamRestriction: hook.TeamRestriction, Enabled: hook.Enabled, AuditLog: hook.AuditLog,
			PayloadVersion: hook.PayloadVersion, ManagedBy: hook.ManagedBy, ApplicationClientID: hook.ApplicationClientID,
		})
	}
	return result
}

func TestScaleP21HotWebhookDeliveryNativeProjection(t *testing.T) {
	until := time.Date(2026, 11, 14, 1, 2, 3, 4, time.FixedZone("native-zone", 7*3600+123))
	wide := strings.Repeat("unrelated webhook metadata ", 200000)
	data := domain.Bootstrap{
		Workspace: domain.Workspace{ID: "organization", URLKey: "logical", Name: wide},
		Webhooks: []domain.Webhook{
			{ID: "disabled", URL: wide, Name: wide},
			{ID: "audit", URL: "https://example.test/audit", Enabled: true, AuditLog: true, Name: wide},
			{ID: "empty-url", Enabled: true, Name: wide},
			{ID: "duplicate", URL: "https://example.test/first", Enabled: true, PayloadVersion: 1, Name: wide, SecretHash: wide},
			{ID: "duplicate", URL: " \x00 ", Enabled: true, PayloadVersion: 2, TeamRestriction: "selected", TeamIDs: []string{}, ResourceTypes: []string{"Issue"}, ManagedBy: "application", ApplicationClientID: "client\x00id"},
			{ID: "", URL: "https://example.test/empty-id", Enabled: true, TeamRestriction: "selected", TeamIDs: []string{"private"}},
		},
		Users: []domain.User{
			{ID: "actor", Name: "First actor", DisplayName: "Display", Username: "handle\x00", Email: "native@example.test", App: true, JobTitle: wide, OutOfOfficeUntil: &until},
			{ID: "actor", Name: "Wrong duplicate"},
			{ID: "subject", Name: "Member", JobTitle: "Complete member subject", AvatarURL: "avatar", AppScopes: []string{"read"}, OutOfOfficeUntil: &until},
			{ID: "unrelated", Name: wide, JobTitle: wide},
		},
		Documents: []domain.Document{{ID: "ignored", Content: wide}},
	}
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": data})
	view.lastWorkspaceKey = "physical"
	for _, key := range []string{"physical", ""} {
		sql := 0
		read, found, err := view.BeginWebhookDeliveryRead(WithSQLStatementObserver(t.Context(), func(string) { sql++ }), key, true)
		if err != nil || !found {
			t.Fatal("native routing read failed", key, found, err)
		}
		if !reflect.DeepEqual(read.Data.Webhooks, scaleP21WebhookRoutingProjection(data)) || read.Data.Workspace.ID != data.Workspace.ID || read.Data.Workspace.URLKey != data.Workspace.URLKey || read.Data.Workspace.Name != "" {
			t.Fatal("routing/identity projection changed", read.Data.Webhooks)
		}
		if err := read.LoadUsers(t.Context(), "actor", "subject", true); err != nil {
			t.Fatal(err)
		}
		if len(read.Data.Users) != 2 || read.Data.Users[0].Name != "First actor" || read.Data.Users[0].JobTitle != "" || read.Data.Users[0].OutOfOfficeUntil != nil {
			t.Fatal("first actor/scalar selection changed", read.Data.Users)
		}
		want, _ := json.Marshal(data.Users[2])
		got, err := json.Marshal(read.Data.Users[1])
		if err != nil || !bytes.Equal(got, want) || !read.Data.Users[1].OutOfOfficeUntil.Equal(until) {
			t.Fatal("complete member subject wire output/instant changed", err)
		}
		if read.Data.Documents != nil || sql != 0 || view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used == 0 {
			t.Fatal("unrelated rows leaked or read lacks ownership", sql)
		}
		read.Close()
		read.Close()
		if !reflect.DeepEqual(read.Data, domain.Bootstrap{}) || view.metadataResidency.work.used != 0 {
			t.Fatal("read did not clear/release")
		}
		if err := read.LoadUsers(t.Context(), "actor", "", false); !errors.Is(err, errMetadataLogicalClosed) {
			t.Fatal("closed read accepted reuse", err)
		}
	}
	for _, key := range []string{"logical", "missing", "physical "} {
		if _, found, err := view.BeginWebhookDeliveryRead(t.Context(), key, true); err != nil || found {
			t.Fatal("physical/missing registry selection changed", key, found, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := view.BeginWebhookDeliveryRead(ctx, "physical", true); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored", err)
	}
	oldLimit := view.metadataResidency.work.limit
	for _, limit := range []int{1, 140 << 10} {
		view.metadataResidency.work.limit = limit
		if _, _, err := view.BeginWebhookDeliveryRead(t.Context(), "physical", true); !errors.Is(err, ErrMetadataWorkBudget) {
			t.Fatal("partial routing read escaped quota", limit, err)
		}
		if view.metadataResidency.work.used != 0 {
			t.Fatal("partial routing read leaked allowance", limit)
		}
	}
	view.metadataResidency.work.limit = oldLimit
}

func TestScaleP21HotWebhookDeliveryWarmReloadBodyBound(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	expectedWork := map[bool]int{}
	for _, size := range []int{10000, 100000} {
		if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "webhooks", "users"), workspace, "scale_p21.webhook_delivery_seed", "", nil, func(data *domain.Bootstrap) error {
			data.Webhooks = []domain.Webhook{{ID: "routing", URL: "https://example.test/hook", Enabled: true, PayloadVersion: 2, ResourceTypes: []string{"issues"}, TeamRestriction: "all", Name: strings.Repeat("unrelated ", size)}}
			for index := range data.Users {
				data.Users[index].JobTitle = strings.Repeat("unrelated job ", size)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for _, reload := range []bool{false, true} {
			if reload {
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
				t.Fatal(err)
			}
			want, _ := f.WorkspaceMetadataFields(workspace, "webhooks", "users")
			sql := 0
			ctx := WithSQLStatementObserver(t.Context(), func(string) { sql++ })
			routing, found, err := view.BeginWebhookDeliveryRead(ctx, workspace, true)
			if err != nil || !found || !reflect.DeepEqual(routing.Data.Webhooks, scaleP21WebhookRoutingProjection(want)) {
				t.Fatal("warm/reloaded routing changed", reload, found, err)
			}
			actor, found, err := view.BeginWebhookDeliveryRead(ctx, workspace, false)
			if err != nil || !found {
				t.Fatal(err)
			}
			if err := actor.LoadUsers(ctx, f.data.Viewer.ID, "", false); err != nil {
				t.Fatal(err)
			}
			if len(actor.Data.Users) != 1 || actor.Data.Users[0].ID != f.data.Viewer.ID || actor.Data.Users[0].JobTitle != "" {
				t.Fatal("actor-only view changed")
			}
			held := view.metadataResidency.work.used
			if expectedWork[reload] == 0 {
				expectedWork[reload] = held
			}
			if held != expectedWork[reload] || sql != 0 || view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("unrelated body increased reader cost", size, reload, held, expectedWork, sql)
			}
			actor.Close()
			routing.Close()
			if view.metadataResidency.work.used != 0 {
				t.Fatal("two accessor lifetimes leaked")
			}
			t.Logf("body_repetitions=%d reload=%t held_work=%d SQL=%d", size, reload, held, sql)
		}
	}
}
