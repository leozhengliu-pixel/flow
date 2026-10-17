package main

import (
	"net/http"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestSpendLimitsPersistAndValidate(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "spend-limits.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})

	saved := requestJSON[domain.WorkspaceSettings](t, handler, http.MethodPatch, "/api/workspace/preferences", map[string]any{
		"spendLimits": map[string]any{"workspaceLimit": 250.5, "userReset": "daily", "weeklyResetDay": 0, "userOverrides": map[string]any{"user-1": 40}},
	}, http.StatusOK)
	limits := saved.SpendLimits
	if limits.WorkspaceLimit != 250.5 || limits.UserReset != "daily" || limits.WorkspaceReset != "weekly" || limits.LoopReset != "weekly" {
		t.Fatalf("spend limits were not saved with defaults: %#v", limits)
	}
	if limits.WeeklyResetDay == nil || *limits.WeeklyResetDay != 0 || limits.UserOverrides["user-1"] != 40 {
		t.Fatalf("reset day and overrides were not kept: %#v", limits)
	}

	// An unrelated preference update keeps the limits, and a nested patch merges into them.
	merged := requestJSON[domain.WorkspaceSettings](t, handler, http.MethodPatch, "/api/workspace/preferences", map[string]any{
		"fiscalMonth": "March", "spendLimits": map[string]any{"loopLimit": 10, "userOverrides": map[string]any{"user-2": 5}},
	}, http.StatusOK)
	if merged.SpendLimits.WorkspaceLimit != 250.5 || merged.SpendLimits.LoopLimit != 10 || len(merged.SpendLimits.UserOverrides) != 2 {
		t.Fatalf("spend limits were not merged: %#v", merged.SpendLimits)
	}

	for name, body := range map[string]map[string]any{
		"negative":  {"spendLimits": map[string]any{"workspaceLimit": -1}},
		"frequency": {"spendLimits": map[string]any{"userReset": "hourly"}},
		"hour":      {"spendLimits": map[string]any{"dailyResetHour": 24}},
		"month day": {"spendLimits": map[string]any{"monthlyResetDay": 31}},
		"override":  {"spendLimits": map[string]any{"loopOverrides": map[string]any{"loop-1": -5}}},
	} {
		t.Run(name, func(t *testing.T) {
			requestJSON[map[string]any](t, handler, http.MethodPatch, "/api/workspace/preferences", body, http.StatusBadRequest)
		})
	}
}
