package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func scaleP21SW23SettingsAliasKey(value string, bits int) string {
	var result strings.Builder
	for index, character := range value {
		if bits&(1<<index) != 0 && character >= 'a' && character <= 'z' {
			character -= 'a' - 'A'
		}
		result.WriteRune(character)
	}
	return result.String()
}

func TestScaleP21SW23WorkspaceSettingsAliasOrder(t *testing.T) {
	f, view, tx := scaleP21SW0ResidentScannerFixture(t)
	workspace := f.data.Workspace.URLKey
	if _, err := tx.ExecContext(t.Context(), `DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field='workspaceSettings'`, workspace); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := tx.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	root["workspaceSettings"] = json.RawMessage(`{"welcomeMessage":"inline","allowedDomains":["inline"],"featureFlags":{"inline":true},"agentWebAccess":true}`)
	raw, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(t.Context(), `UPDATE workspace_states SET data=? WHERE workspace_key=?`, raw, workspace); err != nil {
		t.Fatal(err)
	}
	for index := range 80 {
		key := scaleP21SW23SettingsAliasKey("welcomeMessage", index)
		body := []byte(fmt.Sprintf(`"value-%03d"`, index))
		if index%11 == 0 {
			body = []byte("null")
		}
		var storedKey, storedBody any = key, body
		if f.dialect == "sqlite" {
			if index%3 == 0 {
				storedKey = []byte(key)
			}
			if index%2 == 0 {
				storedBody = string(body)
			}
		}
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'workspaceSettings',?,?,?)`, workspace, storedKey, index%7-4, storedBody); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []struct {
		key, body string
		order     int
	}{
		{"HIPAACOMPLIANCE", "true", -10},
		{"hipaaCompliance", "false", -9},
		{"hipaaCOMPLIANCE", "null", 15},
		{"FEATUREFLAGS", `{"first":true,"inline":false}`, -7},
		{"featureFlags", `{"second":true}`, 8},
		{"ALLOWEDDOMAINS", `["canonical"]`, -6},
		{"allowedDomains", "null", 16},
		{"AGENTWEBACCESS", "false", -8},
		{"agentWebAccess", "null", 17},
		{"api\u212aeyPermission", `"unicode-kelvin"`, 17},
		{"agentWeb\u017fearchDisabled", "true", 17},
		{"featureSettings", `{"unknownNestedProperty":"legacy ignores nested unknowns"}`, 18},
	} {
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'workspaceSettings',?,?,?)`, workspace, row.key, row.order, []byte(row.body)); err != nil {
			t.Fatal(err)
		}
	}
	f.dropMetadataCache(t.Context(), workspace)
	want, err := f.decodeWorkspaceMetadata(t.Context(), workspace, raw, tx)
	if err != nil {
		t.Fatal(err)
	}
	if want.WorkspaceSettings.WelcomeMessage == "inline" || want.WorkspaceSettings.HIPAACompliance ||
		want.WorkspaceSettings.AllowedDomains != nil || want.WorkspaceSettings.AgentWebAccess != nil ||
		!want.WorkspaceSettings.FeatureFlags["second"] || want.WorkspaceSettings.FeatureFlags["inline"] ||
		want.WorkspaceSettings.APIKeyPermission != "unicode-kelvin" || !want.WorkspaceSettings.AgentWebSearchDisabled {
		t.Fatal("typed order/null/map fixture lost its distinguishing effects")
	}
	var cost int
	for pass := range 3 {
		if pass == 2 {
			view.metadataResidency.mu.Lock()
			index := 0
			for key, element := range view.metadataResidency.cache {
				if key.field == "workspaceSettings" {
					if index%2 == 0 {
						view.metadataResidency.removeCacheEntry(element)
					}
					index++
				}
			}
			view.metadataResidency.mu.Unlock()
		}
		got, _, currentCost, err := view.readResidentWorkspace(t.Context(), tx, workspace)
		if err != nil || !reflect.DeepEqual(got.WorkspaceSettings, want.WorkspaceSettings) {
			t.Fatalf("cold/warm/mixed alias pass=%d mismatch: got=%+v want=%+v err=%v", pass, got.WorkspaceSettings, want.WorkspaceSettings, err)
		}
		if pass > 0 && currentCost != cost {
			t.Fatal("cache pass changed policy accounting")
		}
		cost = currentCost
	}
	view.metadataResidency.policyLimit = cost - 1
	if _, _, _, err := view.readResidentWorkspace(t.Context(), tx, workspace); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("required alias policy exceeded resident budget", err)
	}
	view.metadataResidency.policyLimit = metadataPolicyBytes
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, _, err := view.readResidentWorkspace(cancelled, tx, workspace); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled alias recovery", err)
	}
	if view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 {
		t.Fatal("alias reader decoded whole metadata or leaked work")
	}
}

func TestScaleP21SW23WorkspaceSettingsAliasTypedErrors(t *testing.T) {
	for _, row := range []metadataCacheRow{
		{Field: "workspaceSettings", Key: "HIPAACOMPLIANCE", Data: []byte(`"bad bool"`)},
		{Field: "workspaceSettings", Key: "AGENTWEBACCESS", Data: []byte(`[]`)},
		{Field: "workspaceSettings", Key: "ALLOWEDDOMAINS", Data: []byte(`42`)},
	} {
		data := domain.Bootstrap{}
		raw, _ := json.Marshal(map[string]json.RawMessage{row.Key: row.Data})
		wantErr := json.Unmarshal(raw, &data.WorkspaceSettings)
		data = domain.Bootstrap{}
		err := appendResidentMetadataRow(&data, row, "map")
		if wantErr == nil || err == nil {
			t.Fatal("typed alias error accepted", row.Key, err, wantErr)
		}
	}
}
