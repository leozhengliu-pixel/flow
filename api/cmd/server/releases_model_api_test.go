package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestReleasePipelineAndReleaseAPILifecycle(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(bootstrap.Teams) == 0 || len(bootstrap.Issues) < 2 {
		t.Fatal("seed must include a team and at least two issues")
	}

	pipeline := requestJSON[domain.ReleasePipeline](t, handler, http.MethodPost, "/api/release-pipelines", map[string]any{
		"name":                        "Web production",
		"teamIds":                     []string{bootstrap.Teams[0].ID},
		"type":                        "scheduled",
		"production":                  true,
		"stages":                      []string{"Planning", "Deploy"},
		"stageStatuses":               map[string]string{"Planning": "planned", "Deploy": "released"},
		"pathFilters":                 []string{"web/**", "api/*.go"},
		"releaseNotesTemplate":        "## Changes\n{{issues}}",
		"autoGenerateReleaseNotes":    true,
		"moveOpenIssuesToNextRelease": false,
	}, http.StatusCreated)
	if pipeline.Position != 0 || pipeline.SlugID != "web-production" || !slices.Equal(pipeline.PathFilters, []string{"web/**", "api/*.go"}) || pipeline.StageStatuses["Planning"] != "planned" || pipeline.StageStatuses["Deploy"] != "released" || !pipeline.AutoGenerateReleaseNotes || pipeline.ReleaseNotesTemplate == "" || pipeline.MoveOpenIssuesToNextRelease == nil || *pipeline.MoveOpenIssuesToNextRelease {
		t.Fatalf("pipeline settings were not persisted: %#v", pipeline)
	}
	secondPipeline := requestJSON[domain.ReleasePipeline](t, handler, http.MethodPost, "/api/release-pipelines", map[string]any{
		"name": "Mobile production", "teamIds": []string{bootstrap.Teams[0].ID}, "type": "continuous",
	}, http.StatusCreated)
	if secondPipeline.Position <= pipeline.Position {
		t.Fatalf("pipeline position = %v, want after %v", secondPipeline.Position, pipeline.Position)
	}
	requestJSON[any](t, handler, http.MethodPost, "/api/releases", map[string]any{
		"name": "Manual continuous release", "pipelineId": secondPipeline.ID,
	}, http.StatusConflict)

	reorderedPipelines := requestJSON[[]domain.ReleasePipeline](t, handler, http.MethodPost, "/api/release-pipelines/reorder", map[string]any{
		"ids": []string{secondPipeline.ID, pipeline.ID},
	}, http.StatusOK)
	if len(reorderedPipelines) != 2 || reorderedPipelines[0].ID != secondPipeline.ID || reorderedPipelines[0].Position != 0 {
		t.Fatalf("pipeline reorder failed: %#v", reorderedPipelines)
	}
	requestJSON[any](t, handler, http.MethodPost, "/api/release-pipelines/reorder", map[string]any{
		"ids": []string{pipeline.ID},
	}, http.StatusBadRequest)

	firstKey := requestJSON[releasePipelineAccessKey](t, handler, http.MethodPost, "/api/release-pipelines/"+pipeline.ID+"/access-key", nil, http.StatusCreated)
	secondKey := requestJSON[releasePipelineAccessKey](t, handler, http.MethodPost, "/api/release-pipelines/"+pipeline.ID+"/access-key", nil, http.StatusCreated)
	if firstKey.Secret == "" || firstKey.Prefix == "" || secondKey.Secret == firstKey.Secret {
		t.Fatalf("access key rotation did not return one-time credentials: first=%#v second=%#v", firstKey, secondKey)
	}
	internal, ok := repository.BootstrapFor(bootstrap.Workspace.URLKey)
	if !ok {
		t.Fatal("workspace disappeared")
	}
	internalPipeline := releasePipelineByID(&internal, pipeline.ID)
	if internalPipeline == nil || internalPipeline.AccessKeyHash == "" || internalPipeline.AccessKeyHash == secondKey.Secret {
		t.Fatalf("access key was not stored as a hash: %#v", internalPipeline)
	}
	publicPipeline := requestJSON[domain.ReleasePipeline](t, handler, http.MethodGet, "/api/release-pipelines/"+pipeline.ID, nil, http.StatusOK)
	if publicPipeline.AccessKeyHash != "" || publicPipeline.AccessKeyPrefix != secondKey.Prefix || publicPipeline.AccessKeyCreatedAt == nil || !slices.Equal(publicPipeline.PathFilters, pipeline.PathFilters) || publicPipeline.ReleaseNotesTemplate != pipeline.ReleaseNotesTemplate || publicPipeline.StageStatuses["Deploy"] != "released" {
		t.Fatalf("public pipeline leaked or omitted access-key metadata: %#v", publicPipeline)
	}
	publicBootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	publicPipeline = *releasePipelineByID(&publicBootstrap, pipeline.ID)
	if publicPipeline.AccessKeyHash != "" || publicPipeline.StageStatuses["Planning"] != "planned" || publicPipeline.StageStatuses["Deploy"] != "released" {
		t.Fatalf("bootstrap leaked a key or lost stage statuses: %#v", publicPipeline)
	}
	invalidEvent := httptest.NewRequest(http.MethodPost, "/api/release-pipelines/"+pipeline.ID+"/events", strings.NewReader(`{"version":"1.2.3","stage":"Deploy"}`))
	invalidEvent.Header.Set("Authorization", "Bearer "+firstKey.Secret)
	invalidEvent.Header.Set("Content-Type", "application/json")
	invalidResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidResponse, invalidEvent)
	if invalidResponse.Code != http.StatusUnauthorized {
		t.Fatalf("rotated access key status=%d body=%s", invalidResponse.Code, invalidResponse.Body.String())
	}
	ciEvent := httptest.NewRequest(http.MethodPost, "/api/release-pipelines/"+pipeline.ID+"/events", strings.NewReader(`{"version":"1.2.3","commitSha":"abc123","stage":"Deploy"}`))
	ciEvent.Header.Set("Authorization", "Bearer "+secondKey.Secret)
	ciEvent.Header.Set("Content-Type", "application/json")
	ciResponse := httptest.NewRecorder()
	handler.ServeHTTP(ciResponse, ciEvent)
	if ciResponse.Code != http.StatusCreated {
		t.Fatalf("CI release status=%d body=%s", ciResponse.Code, ciResponse.Body.String())
	}
	var ciRelease domain.Release
	if err := json.NewDecoder(ciResponse.Body).Decode(&ciRelease); err != nil {
		t.Fatal(err)
	}
	if ciRelease.PipelineID != pipeline.ID || ciRelease.Version != "1.2.3" || ciRelease.Status != "released" || ciRelease.ReleasedAt == nil || ciRelease.CommitSHA != "abc123" {
		t.Fatalf("CI release=%#v", ciRelease)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/releases/"+ciRelease.ID, nil, http.StatusNoContent)
	requestJSON[any](t, handler, http.MethodPost, "/api/release-pipelines", map[string]any{
		"name": "Invalid statuses", "stages": []string{"准备", "上线"}, "stageStatuses": map[string]string{"准备": "planned", "上线": "done"},
	}, http.StatusBadRequest)
	favorite := requestJSON[domain.Favorite](t, handler, http.MethodPut, "/api/favorites/release_pipeline/"+pipeline.ID, nil, http.StatusOK)
	if favorite.ResourceType != "release_pipeline" || favorite.ResourceID != pipeline.ID {
		t.Fatalf("release pipeline favorite failed: %#v", favorite)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/favorites/release_pipeline/"+pipeline.ID, nil, http.StatusNoContent)

	firstIssue, secondIssue := bootstrap.Issues[0], bootstrap.Issues[1]
	release := requestJSON[domain.Release](t, handler, http.MethodPost, "/api/releases", map[string]any{
		"name":         "2026.09 Web",
		"pipelineId":   pipeline.ID,
		"stage":        "Planning",
		"status":       "inProgress",
		"commitSha":    "abc123def456",
		"releaseNotes": "Initial notes",
		"targetDate":   "2026-09-15",
		"issueIds":     []string{firstIssue.ID},
		"resources":    []map[string]any{{"type": "link", "title": "Runbook", "url": "https://example.com/runbook"}},
	}, http.StatusCreated)
	if release.SlugID == "" || release.PipelineID != pipeline.ID || release.Stage != "Planning" || release.CommitSHA != "abc123def456" || release.ReleaseNotes != "Initial notes" || len(release.Resources) != 1 || release.Resources[0].Title != "Runbook" || release.StartedAt == nil || release.ReleasedAt != nil {
		t.Fatalf("release business fields were not initialized: %#v", release)
	}
	persistedRelease := requestJSON[domain.Release](t, handler, http.MethodGet, "/api/releases/"+release.ID, nil, http.StatusOK)
	if persistedRelease.PipelineID != pipeline.ID || persistedRelease.CommitSHA != release.CommitSHA || persistedRelease.StartedAt == nil {
		t.Fatalf("release did not survive the read round trip: %#v", persistedRelease)
	}
	requestJSON[any](t, handler, http.MethodPost, "/api/releases", map[string]any{
		"name": "Invalid stage", "pipelineId": pipeline.ID, "stage": "QA",
	}, http.StatusBadRequest)

	secondRelease := requestJSON[domain.Release](t, handler, http.MethodPost, "/api/releases", map[string]any{
		"name": "2026.10 Web", "pipelineId": pipeline.ID, "stage": "Planning",
	}, http.StatusCreated)
	if secondRelease.Position <= release.Position {
		t.Fatalf("release position = %v, want after %v", secondRelease.Position, release.Position)
	}
	issueReleases := requestJSON[[]domain.Release](t, handler, http.MethodPut, "/api/issues/"+secondIssue.ID+"/releases", map[string]any{"releaseIds": []string{release.ID, secondRelease.ID}}, http.StatusOK)
	if len(issueReleases) != 2 || !slices.Contains(issueReleases[0].IssueIDs, secondIssue.ID) || !slices.Contains(issueReleases[1].IssueIDs, secondIssue.ID) {
		t.Fatalf("issue release association failed: %#v", issueReleases)
	}
	issueReleases = requestJSON[[]domain.Release](t, handler, http.MethodPut, "/api/issues/"+secondIssue.ID+"/releases", map[string]any{"releaseIds": []string{secondRelease.ID}}, http.StatusOK)
	if len(issueReleases) != 1 || issueReleases[0].ID != secondRelease.ID {
		t.Fatalf("issue release removal failed: %#v", issueReleases)
	}
	filtered := requestJSON[[]domain.Release](t, handler, http.MethodGet, "/api/releases?pipelineId="+pipeline.ID+"&status=planned", nil, http.StatusOK)
	if len(filtered) != 1 || filtered[0].ID != secondRelease.ID {
		t.Fatalf("release filters returned %#v", filtered)
	}
	reorderedReleases := requestJSON[[]domain.Release](t, handler, http.MethodPost, "/api/releases/reorder", map[string]any{
		"pipelineId": pipeline.ID, "ids": []string{secondRelease.ID, release.ID},
	}, http.StatusOK)
	if reorderedReleases[0].ID != secondRelease.ID || reorderedReleases[0].Position != 0 {
		t.Fatalf("release reorder failed: %#v", reorderedReleases)
	}

	release = requestJSON[domain.Release](t, handler, http.MethodPatch, "/api/releases/"+release.ID, map[string]any{"stageFrozen": true}, http.StatusOK)
	if release.StageFrozenAt == nil {
		t.Fatal("release stage was not frozen")
	}
	requestJSON[any](t, handler, http.MethodPatch, "/api/releases/"+release.ID, map[string]any{
		"issueIds": []string{firstIssue.ID, secondIssue.ID},
	}, http.StatusConflict)
	release = requestJSON[domain.Release](t, handler, http.MethodPatch, "/api/releases/"+release.ID, map[string]any{
		"issueIds": []string{},
	}, http.StatusOK)
	if len(release.IssueIDs) != 0 {
		t.Fatalf("frozen stage should allow issue removal: %#v", release.IssueIDs)
	}
	release = requestJSON[domain.Release](t, handler, http.MethodPatch, "/api/releases/"+release.ID, map[string]any{"status": "released"}, http.StatusOK)
	if release.ReleasedAt == nil || release.StartedAt == nil {
		t.Fatalf("release timestamps were not recorded: %#v", release)
	}
	requestJSON[any](t, handler, http.MethodPatch, "/api/release-pipelines/"+pipeline.ID, map[string]any{
		"stages": []string{"Deploy"},
	}, http.StatusConflict)

	requestJSON[any](t, handler, http.MethodDelete, "/api/release-pipelines/"+pipeline.ID, nil, http.StatusNoContent)
	afterDelete := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if slices.ContainsFunc(afterDelete.Releases, func(item domain.Release) bool { return item.PipelineID == pipeline.ID }) || len(afterDelete.Trash) < 3 {
		t.Fatalf("pipeline deletion did not move its releases to trash: releases=%#v trash=%#v", afterDelete.Releases, afterDelete.Trash)
	}
	trashIndex := slices.IndexFunc(afterDelete.Trash, func(item domain.TrashEntry) bool {
		return item.ResourceType == "release_pipeline" && item.ResourceID == pipeline.ID
	})
	if trashIndex < 0 || strings.Contains(string(afterDelete.Trash[trashIndex].Payload), "accessKeyHash") {
		t.Fatalf("pipeline trash entry missing or leaked access-key hash: %#v", afterDelete.Trash)
	}
	restored := requestJSON[domain.ReleasePipeline](t, handler, http.MethodPost, "/api/trash/"+afterDelete.Trash[trashIndex].ID+"/restore", nil, http.StatusOK)
	if restored.ID != pipeline.ID || restored.AccessKeyHash != "" {
		t.Fatalf("pipeline restore failed: %#v", restored)
	}

}
