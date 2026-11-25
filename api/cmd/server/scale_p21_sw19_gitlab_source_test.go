package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW19GitLabInstalledSourceHold(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace := f.data.Workspace.URLKey
		for _, spec := range []struct {
			name, provider, status string
			warm, canonical        int
		}{
			{"provider-NUL", "git\x00lab", "connected", 0, 1},
			{"status-NUL", "gitlab", "con\x00nected", 0, 1},
			{"config-NUL", "gitlab", "connected", 1, 1},
		} {
			t.Run(spec.name, func(t *testing.T) {
				id := idgen.New("sw19-gitlab-source-")
				if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "integrationConnections"), workspace, "scale_p21_sw19.gitlab_source", "", nil, func(data *domain.Bootstrap) error {
					data.IntegrationConnections = []domain.IntegrationConnection{{
						ID: id, Provider: spec.provider, Status: spec.status,
						Config: map[string]string{"username": "before\x00after", "rotationEnabled": "tr\x00ue"},
					}}
					return nil
				}); err != nil {
					t.Fatal("accepted typed producer", err)
				}
				service := &server{store: repo}
				_, warm := (gitlabCodingSource{s: service}).workspaceConnections(workspace)
				if len(warm) != spec.warm {
					t.Fatalf("actual installed selector returned %d want %d", len(warm), spec.warm)
				}
				probe, err := repo.BoundedMetadataViewForTesting(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				hydrated, err := probe.HydrateWorkspaceMetadata(t.Context(), workspace, store.MetadataRecordScope{
					Read: []store.RecordKey{{Kind: "integrationConnections", ID: id}},
				})
				if err != nil || len(hydrated.Data.IntegrationConnections) != 1 {
					t.Fatal("canonical candidate", err)
				}
				data := hydrated.Data
				selected := 0
				for _, item := range data.IntegrationConnections {
					if item.Provider == "gitlab" && (item.Status == "configured" || item.Status == "connected") {
						selected++
					}
				}
				if selected != spec.canonical {
					t.Fatal("canonical selector witness changed")
				}
				if spec.name == "config-NUL" {
					old, err := (gitlabCodingSource{s: service}).GitLabConnections(t.Context(), workspace)
					if err != nil || len(old) != 1 || old[0].Username != "before\x00after" ||
						data.IntegrationConnections[0].Config["username"] != "beforeafter" {
						t.Fatal("actual coding output/canonical config source witness changed", err)
					}
					request := httptest.NewRequest(http.MethodPost, "/api/integrations/gitlab/"+id+"/token", nil)
					request.Header.Set("X-Workspace-Key", workspace)
					_, connection := (&server{store: repo, authDisabled: true}).gitlabConnectionSnapshot(request, id)
					if connection == nil || connection.Config["rotationEnabled"] != "tr\x00ue" ||
						data.IntegrationConnections[0].Config["rotationEnabled"] != "true" {
						t.Fatal("actual token snapshot/rotation decision source witness changed")
					}
				}
				if err := repo.RefreshMetadataRecord(t.Context(), workspace, "integrationConnections", id); err != nil {
					t.Fatal(err)
				}
				if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
				_, reloaded := (gitlabCodingSource{s: service}).workspaceConnections(workspace)
				if len(reloaded) != spec.canonical {
					t.Fatal("reload canonical control changed")
				}
			})
		}
	}
}

func TestScaleP21SW19GitLabAggregateSourceHold(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace, id := f.data.Workspace.URLKey, idgen.New("sw19-gitlab-aggregate-")
		reset := func() {
			t.Helper()
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw19.gitlab_collision", "", nil, func(data *domain.Bootstrap) error {
				data.Documents = []domain.Document{{
					ID:          idgen.New("sw19-gitlab-document-"),
					ContentData: map[string]any{"id": id, "name": "Earlier unrelated aggregate"},
				}}
				data.IntegrationConnections = []domain.IntegrationConnection{{
					ID: id, Provider: "gitlab", Status: "connected", Name: "Connection",
					Config: map[string]string{"rotationEnabled": "false"},
				}}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		var payloads []json.RawMessage
		for _, bounded := range []bool{false, true} {
			reset()
			source := repo
			if bounded {
				var err error
				source, err = repo.BoundedMetadataViewForTesting(t.Context())
				if err != nil {
					t.Fatal(err)
				}
			}
			var event domain.RealtimeEvent
			source.SetRealtimeSink(func(_ string, actual domain.RealtimeEvent) { event = actual })
			updated, err := (&server{store: source}).saveGitLabConnection(context.Background(), workspace, id, "integration.token_rotation_updated", func(connection *domain.IntegrationConnection, _ time.Time) {
				connection.Config["rotationEnabled"] = "true"
			})
			source.SetRealtimeSink(nil)
			if err != nil || updated.Config["rotationEnabled"] != "true" {
				t.Fatal("actual connection update failed", err)
			}
			var payload map[string]any
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			entity, ok := payload["entity"].(map[string]any)
			if !ok || entity["id"] != id {
				t.Fatal("actual event entity missing", string(event.Payload))
			}
			if entity["name"] != "Earlier unrelated aggregate" {
				t.Fatalf("aggregate collision witness changed: bounded=%v entity=%v", bounded, entity)
			}
			canonical, err := source.HydrateWorkspaceMetadata(t.Context(), workspace, store.MetadataRecordScope{
				Read: []store.RecordKey{{Kind: "integrationConnections", ID: id}},
			})
			if err != nil || len(canonical.Data.IntegrationConnections) != 1 || canonical.Data.IntegrationConnections[0].Config["rotationEnabled"] != "true" {
				t.Fatal("canonical connection update changed", err)
			}
			payloads = append(payloads, event.Payload)
		}
		if !reflect.DeepEqual(payloads[0], payloads[1]) {
			t.Fatalf("aggregate effects differ: legacy=%s bounded=%s", payloads[0], payloads[1])
		}
	}
}
