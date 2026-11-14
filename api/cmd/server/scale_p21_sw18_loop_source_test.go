package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW18LoopSettingsSourceHold(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace := f.data.Workspace.URLKey
		appID := idgen.New("sw18-loop-app-")
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "members", "integrationConnections"), workspace, "scale_p21_sw18.loop_source", "", nil, func(data *domain.Bootstrap) error {
			data.Members = append(data.Members, domain.WorkspaceMember{
				User: domain.User{ID: appID, Name: "warm\x00app"}, Role: "app", Status: "active",
			})
			data.IntegrationConnections = append(data.IntegrationConnections,
				domain.IntegrationConnection{ID: idgen.New("sw18-loop-connection-"), Provider: "custom", Name: "warm\x00integration", Status: "connected"},
				domain.IntegrationConnection{ID: idgen.New("sw18-loop-connection-"), Provider: "github", Status: "con\x00nected"},
			)
			return nil
		}); err != nil {
			t.Fatal("accepted trusted producer", err)
		}
		// A normal full workspace edit strips the inline Members JSON while
		// retaining its exact installed values, as it does for record rows.
		identity := f.data.Workspace
		identity.Name += " source rewrite"
		if _, err := repo.UpdateWorkspace(t.Context(), workspace, identity); err != nil {
			t.Fatal("actual full rewrite producer", err)
		}
		canonical := domain.Bootstrap{WorkspaceSettings: f.data.WorkspaceSettings}
		if _, err := probe.StreamMetadataArray(t.Context(), workspace, "members", func(_ context.Context, row store.MetadataArrayRecord) error {
			var member domain.WorkspaceMember
			if err := json.NewDecoder(row.Reader).Decode(&member); err != nil {
				return err
			}
			canonical.Members = append(canonical.Members, member)
			return nil
		}); err != nil {
			t.Fatal("canonical member comparison source", err)
		}
		if _, err := probe.StreamMetadataArray(t.Context(), workspace, "integrationConnections", func(_ context.Context, row store.MetadataArrayRecord) error {
			var connection domain.IntegrationConnection
			if err := json.NewDecoder(row.Reader).Decode(&connection); err != nil {
				return err
			}
			canonical.IntegrationConnections = append(canonical.IntegrationConnections, connection)
			return nil
		}); err != nil {
			t.Fatal("canonical connection comparison source", err)
		}
		service := &server{store: repo, authDisabled: true}
		request := httptest.NewRequest(http.MethodGet, "/api/workspace/loop-settings", nil)
		request.Header.Set("X-Workspace-Key", workspace)
		response := httptest.NewRecorder()
		service.getLoopConfig(response, request)
		var actual loopConfigResponse
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &actual) != nil {
			t.Fatal("actual settings reader", response.Code, response.Body.String())
		}
		has := func(options []loopTrustedSourceOption, key, label string) bool {
			for _, option := range options {
				if option.Key == key && option.Label == label {
					return true
				}
			}
			return false
		}
		if !has(actual.TrustedSourceOptions, "appUser:"+appID, "warm\x00app") ||
			!has(actual.TrustedSourceOptions, "integration:custom", "warm\x00integration") ||
			has(actual.TrustedSourceOptions, "integration:github", "GitHub") {
			t.Fatal("legacy consumer no longer uses exact installed source")
		}
		cold := service.loopConfig(canonical)
		if !has(cold.TrustedSourceOptions, "appUser:"+appID, "warmapp") ||
			!has(cold.TrustedSourceOptions, "integration:custom", "warmintegration") ||
			!has(cold.TrustedSourceOptions, "integration:github", "GitHub") ||
			reflect.DeepEqual(actual, cold) {
			t.Fatalf("source divergence witness changed: warm=%+v canonical=%+v", actual.TrustedSourceOptions, cold.TrustedSourceOptions)
		}
		update := httptest.NewRequest(http.MethodPatch, "/api/workspace/loop-settings", strings.NewReader(`{"externalLoopTriggers":true}`))
		update.Header.Set("X-Workspace-Key", workspace)
		updated := httptest.NewRecorder()
		service.updateLoopSettings(updated, update)
		var result loopConfigResponse
		if updated.Code != http.StatusOK || json.Unmarshal(updated.Body.Bytes(), &result) != nil ||
			!result.ExternalLoopTriggers || !reflect.DeepEqual(result.TrustedSourceOptions, actual.TrustedSourceOptions) {
			t.Fatal("actual settings mutation response lost preserved warm inputs", updated.Code, updated.Body.String())
		}
		t.Log("getLoopConfig and updateLoopSettings require installed Members/IntegrationConnections, including labels and exact status predicates")
	}
}

func TestScaleP21SW18LoopVersionsSourceHold(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace, id := f.data.Workspace.URLKey, idgen.New("sw18-loop-version-")
		now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		first := domain.LoopVersion{
			ID: loopVersionID(id, 1), LoopID: id, Version: 1, PublishedAt: now, PublishedBy: f.data.Viewer,
			Definition: domain.LoopDefinition{Name: "restore\x00name", Instructions: "restore\x00instructions", Level: "workspace", TriggerType: "schedule", TeamAccess: "allPublic"},
		}
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "loops", "loopVersions"), workspace, "scale_p21_sw18.loop_versions", id, nil, func(data *domain.Bootstrap) error {
			data.Loops = []domain.Loop{{
				ID: id, Name: "current\x00name", Instructions: "current\x00instructions", Status: "published",
				Level: "workspace", TriggerType: "schedule", TeamAccess: "allPublic",
				OwnerID: f.data.Viewer.ID, Creator: f.data.Viewer, CreatedAt: now, UpdatedAt: now,
			}}
			data.LoopVersions = []domain.LoopVersion{first}
			return nil
		}); err != nil {
			t.Fatal("accepted trusted producer", err)
		}
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		hydrated, err := probe.HydrateWorkspaceMetadata(t.Context(), workspace, store.MetadataRecordScope{Fields: []string{"loops", "loopVersions", "users"}})
		if err != nil {
			t.Fatal("canonical comparison source", err)
		}
		canonical := hydrated.Data
		service := &server{store: repo, authDisabled: true}
		request := func() *http.Request {
			r := httptest.NewRequest(http.MethodGet, "/api/loops/"+id+"/versions", nil)
			r.Header.Set("X-Workspace-Key", workspace)
			r.SetPathValue("id", id)
			r.SetPathValue("versionId", first.ID)
			return r
		}
		listed := httptest.NewRecorder()
		service.listLoopVersions(listed, request())
		var versions []domain.LoopVersion
		if listed.Code != http.StatusOK || json.Unmarshal(listed.Body.Bytes(), &versions) != nil ||
			len(versions) != 1 || versions[0].Definition.Name != first.Definition.Name {
			t.Fatal("actual versions reader lost logical source", listed.Code, listed.Body.String())
		}
		one := httptest.NewRecorder()
		service.getLoopVersion(one, request())
		var selected domain.LoopVersion
		if one.Code != http.StatusOK || json.Unmarshal(one.Body.Bytes(), &selected) != nil || selected.Definition.Name != first.Definition.Name {
			t.Fatal("actual selected version reader lost logical source", one.Code, one.Body.String())
		}
		cold := presentLoopVersions(&canonical, canonical.Loops[0])
		if len(cold) != 1 || cold[0].Definition.Name != "restorename" || reflect.DeepEqual(cold, versions) {
			t.Fatal("version source divergence witness changed")
		}
		restored := httptest.NewRecorder()
		service.restoreLoopVersion(restored, request())
		var actual domain.Loop
		if restored.Code != http.StatusOK || json.Unmarshal(restored.Body.Bytes(), &actual) != nil ||
			actual.Name != first.Definition.Name || actual.Instructions != first.Definition.Instructions || actual.Version != 2 {
			t.Fatal("actual restore did not preserve source version", restored.Code, restored.Body.String())
		}
		stored, err := probe.HydrateWorkspaceMetadata(t.Context(), workspace, store.MetadataRecordScope{Fields: []string{"loops"}})
		if err != nil || len(stored.Data.Loops) != 1 || stored.Data.Loops[0].Name != "restorename" {
			t.Fatal("restored canonical witness changed", err)
		}
		t.Log("list/get/restore require exact installed version bodies; canonical history strips accepted NUL")
	}
}
