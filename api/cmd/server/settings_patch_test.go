package main

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestSettingsPatchPreservesIndependentFields(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	first := requestJSON[domain.UserSettings](t, handler, http.MethodPatch, "/api/account/settings", map[string]any{"pointerCursor": true, "autoAssign": true}, http.StatusOK)
	second := requestJSON[domain.UserSettings](t, handler, http.MethodPatch, "/api/account/settings", map[string]any{"firstDay": "Sunday"}, http.StatusOK)
	if !second.PointerCursor || !second.AutoAssign || second.FirstDay != "Sunday" || second.HomeView != first.HomeView {
		t.Fatalf("unrelated fields reset: %#v", second)
	}
	third := requestJSON[domain.UserSettings](t, handler, http.MethodPatch, "/api/account/settings", map[string]any{"autoAssign": false}, http.StatusOK)
	if third.AutoAssign || !third.PointerCursor {
		t.Fatal("false was not applied independently")
	}
	requestJSON[domain.WorkspaceSettings](t, handler, http.MethodPatch, "/api/workspace/preferences", map[string]any{"featureFlags": map[string]bool{"initiatives": false, "loops": true}, "featureSettings": map[string]string{"customerRevenueCurrency": "CNY"}}, http.StatusOK)
	ws := requestJSON[domain.WorkspaceSettings](t, handler, http.MethodPatch, "/api/workspace/preferences", map[string]any{"featureFlags": map[string]bool{"loops": false}, "welcomeMessage": "Welcome"}, http.StatusOK)
	if ws.FeatureFlags["initiatives"] || ws.FeatureFlags["loops"] || ws.FeatureSettings.CustomerRevenueCurrency != "CNY" || ws.WelcomeMessage != "Welcome" {
		t.Fatalf("nested patch reset unrelated values: %#v", ws)
	}
	schedule := requestJSON[domain.WorkspaceSettings](t, handler, http.MethodPatch, "/api/workspace/preferences", map[string]any{"featureSettings": map[string]any{
		"initiativeUpdateSchedule": "biweekly", "initiativeUpdateFrequencyWeeks": 2, "initiativeUpdateWeekday": 4, "initiativeUpdateHour": 14,
	}}, http.StatusOK)
	if schedule.FeatureSettings.InitiativeUpdateFrequencyWeeks != 2 || schedule.FeatureSettings.InitiativeUpdateWeekday != 4 || schedule.FeatureSettings.InitiativeUpdateHour != 14 {
		t.Fatalf("initiative schedule was not persisted: %#v", schedule.FeatureSettings)
	}
	requestJSON[domain.WorkspaceSettings](t, handler, http.MethodPatch, "/api/workspace/preferences", map[string]any{"featureSettings": map[string]any{"initiativeUpdateFrequencyWeeks": 9}}, http.StatusBadRequest)
	requestJSON[domain.WorkspaceSettings](t, handler, http.MethodPatch, "/api/workspace/preferences", map[string]any{"featureSettings": map[string]any{"initiativeUpdateWeekday": 7}}, http.StatusBadRequest)
	requestJSON[domain.WorkspaceSettings](t, handler, http.MethodPatch, "/api/workspace/preferences", map[string]any{"featureSettings": map[string]any{"initiativeUpdateHour": 24}}, http.StatusBadRequest)
}

func TestSettingsPatchProtectsIdentityAndRejectsInvalidShapes(t *testing.T) {
	current := domain.UserSettings{UserID: "original", AutoAssign: true, CommitSigningKey: &domain.CommitSigningKey{Name: "original"}}
	patch := map[string]json.RawMessage{"userId": json.RawMessage(`"other"`), "commitSigningKey": json.RawMessage(`{"name":"forged"}`), "autoAssign": json.RawMessage(`false`)}
	result, err := mergeSettingsPatch(current, patch, "userId", "commitSigningKey")
	if err != nil || result.UserID != "original" || result.CommitSigningKey.Name != "original" || result.AutoAssign {
		t.Fatalf("protected fields changed: %#v %v", result, err)
	}
	for _, value := range []string{`null`, `"true"`, `{}`} {
		if _, err := mergeSettingsPatch(current, map[string]json.RawMessage{"autoAssign": json.RawMessage(value)}); err == nil {
			t.Errorf("accepted invalid boolean %s", value)
		}
	}
}
