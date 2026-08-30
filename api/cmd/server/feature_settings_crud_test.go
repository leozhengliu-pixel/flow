package main

import (
	"net/http"
	"path/filepath"
	"slices"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestFeatureSettingsAndReleasePipelinePersistence(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	bootstrap.WorkspaceSettings.FeatureSettings.PulseWorkspaceSchedule = "weekly"
	bootstrap.WorkspaceSettings.FeatureSettings.CustomerRevenueCurrency = "CNY"
	updated := requestJSON[domain.WorkspaceSettings](t, handler, http.MethodPatch, "/api/workspace/preferences", bootstrap.WorkspaceSettings, http.StatusOK)
	if updated.FeatureSettings.PulseWorkspaceSchedule != "weekly" || updated.FeatureSettings.CustomerRevenueCurrency != "CNY" {
		t.Fatalf("feature settings did not update: %#v", updated.FeatureSettings)
	}

	pipeline := requestJSON[domain.ReleasePipeline](t, handler, http.MethodPost, "/api/release-pipelines", map[string]any{
		"name": "Mobile production", "teamIds": []string{bootstrap.Teams[0].ID}, "type": "scheduled", "production": true,
	}, http.StatusCreated)
	if pipeline.Name != "Mobile production" || len(pipeline.Stages) != 4 {
		t.Fatalf("release pipeline was not initialized: %#v", pipeline)
	}
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if !slices.ContainsFunc(bootstrap.ReleasePipelines, func(item domain.ReleasePipeline) bool { return item.ID == pipeline.ID }) {
		t.Fatalf("release pipeline did not survive bootstrap: %#v", bootstrap.ReleasePipelines)
	}
}

func TestCustomEmojiCreateAndArchive(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	emoji := requestJSON[domain.CustomEmoji](t, handler, http.MethodPost, "/api/custom-emojis", map[string]any{
		"name": ":ship-it:", "imageUrl": "data:image/png;base64,iVBORw0KGgo=",
	}, http.StatusCreated)
	if emoji.Name != "ship-it" || emoji.Creator.ID == "" {
		t.Fatalf("custom emoji was not normalized: %#v", emoji)
	}
	emoji = requestJSON[domain.CustomEmoji](t, handler, http.MethodPatch, "/api/custom-emojis/"+emoji.ID, map[string]any{"archived": true}, http.StatusOK)
	if emoji.ArchivedAt == nil {
		t.Fatal("custom emoji was not archived")
	}
}
