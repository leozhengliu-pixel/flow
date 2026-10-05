package main

import (
	"net/http"
	"path/filepath"
	"slices"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestReleasePipelineStageColorsFreezeAndRename(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := repository.Bootstrap()
	pipeline := requestJSON[domain.ReleasePipeline](t, handler, http.MethodPost, "/api/release-pipelines", map[string]any{
		"name":          "Mobile",
		"stages":        []string{"Planned", "In Progress", "QA", "Released", "Canceled"},
		"stageStatuses": map[string]string{"Planned": "planned", "In Progress": "inProgress", "QA": "inProgress", "Released": "released", "Canceled": "canceled"},
		"stageColors":   map[string]string{"QA": "#4EA7FC"},
		"frozenStages":  []string{"QA"},
	}, http.StatusCreated)
	if pipeline.StageColors["QA"] != "#4ea7fc" || !slices.Equal(pipeline.FrozenStages, []string{"QA"}) {
		t.Fatalf("stage colors or frozen stages not stored: %#v", pipeline)
	}
	path := "/api/release-pipelines/" + pipeline.ID
	// At least one started stage must remain non-frozen; only started stages freeze.
	requestJSON[any](t, handler, http.MethodPatch, path, map[string]any{"frozenStages": []string{"QA", "In Progress"}}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodPatch, path, map[string]any{"frozenStages": []string{"Planned"}}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodPatch, path, map[string]any{"stageColors": map[string]string{"QA": "blue"}}, http.StatusBadRequest)

	release := requestJSON[domain.Release](t, handler, http.MethodPost, "/api/releases", map[string]any{"name": "1.0", "pipelineId": pipeline.ID, "stage": "QA", "status": "inProgress"}, http.StatusCreated)
	// Issues cannot be added to a release in a frozen stage.
	requestJSON[any](t, handler, http.MethodPut, "/api/issues/"+bootstrap.Issues[0].ID+"/releases", map[string]any{"releaseIds": []string{release.ID}}, http.StatusConflict)

	// Renaming a stage carries its releases, color and freeze with it.
	renamed := requestJSON[domain.ReleasePipeline](t, handler, http.MethodPatch, path, map[string]any{
		"stages":        []string{"Planned", "In Progress", "Testing", "Released", "Canceled"},
		"stageRenames":  map[string]string{"QA": "Testing"},
		"stageStatuses": map[string]string{"Planned": "planned", "In Progress": "inProgress", "Testing": "inProgress", "Released": "released", "Canceled": "canceled"},
	}, http.StatusOK)
	if renamed.StageColors["Testing"] != "#4ea7fc" || !slices.Equal(renamed.FrozenStages, []string{"Testing"}) || renamed.StageStatuses["Testing"] != "inProgress" {
		t.Fatalf("rename did not carry stage settings: %#v", renamed)
	}
	moved := releaseByIDForTest(repository.Bootstrap(), release.ID)
	if moved.Stage != "Testing" {
		t.Fatalf("release stage = %q, want Testing", moved.Stage)
	}
	// Removing a stage that still has releases is refused.
	requestJSON[any](t, handler, http.MethodPatch, path, map[string]any{"stages": []string{"Planned", "In Progress", "Released", "Canceled"}}, http.StatusConflict)
}

func releaseByIDForTest(data domain.Bootstrap, id string) domain.Release {
	for _, release := range data.Releases {
		if release.ID == id {
			return release
		}
	}
	return domain.Release{}
}
