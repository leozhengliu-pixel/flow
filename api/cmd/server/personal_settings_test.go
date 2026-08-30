package main

import (
	"net/http"
	"path/filepath"
	"testing"

	"flow/api/internal/store"
)

func TestPersonalSettingsPersistence(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	defaults := requestJSON[map[string]any](t, handler, http.MethodGet, "/api/account/settings", nil, http.StatusOK)
	if defaults["personalSettingsVersion"] != float64(1) || defaults["codeReviewsEnabled"] != true || defaults["mergeStrategy"] != "Squash and merge" {
		t.Fatalf("personal settings defaults were not normalized: %#v", defaults)
	}

	want := map[string]any{
		"language": "en-US", "homeView": "Flow Agent (default)", "firstDay": "Monday",
		"branchFormat": "{identifier}-{title}", "codeReviewsEnabled": true,
		"autoConvertDrafts": true, "mergeStrategy": "Rebase and merge",
		"codeTheme": "Flow Dark", "codeFont": "13px, Regular, Default",
		"reviewCommentsFilter": "Exclude Bots", "reviewRequests": true,
		"githubTeamReviewRequests": true, "checksMergeQueue": true,
		"gitAttachmentFormat": "Title and URL", "gitBranchMoveStarted": true,
		"codingToolMoveStarted": true, "changelogUpdates": true,
		"changelogNewsletter": true, "privacyUpdates": true,
	}
	updated := requestJSON[map[string]any](t, handler, http.MethodPatch, "/api/account/settings", want, http.StatusOK)
	for key, expected := range want {
		if updated[key] != expected {
			t.Fatalf("updated %s = %#v, want %#v", key, updated[key], expected)
		}
	}
	persisted := requestJSON[map[string]any](t, handler, http.MethodGet, "/api/account/settings", nil, http.StatusOK)
	for key, expected := range want {
		if persisted[key] != expected {
			t.Fatalf("persisted %s = %#v, want %#v", key, persisted[key], expected)
		}
	}
}
