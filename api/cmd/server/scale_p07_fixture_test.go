package main

import (
	"net/http"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// The CI database is shared: register an owned account and workspace through
// P01's isolated fixture, and clean them up without touching existing users.
func scaleP07ExternalFixture(t *testing.T, driver, databaseURL string) *mcpContractFixture {
	t.Helper()
	config := store.DatabaseConfig{Driver: driver, URL: databaseURL, MaxOpenConns: 4}
	if driver == "sqlite" {
		config.Path, config.MaxOpenConns = databaseURL, 1
	}
	repo, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	service, host, admin, data, _ := scaleP01OwnedWorkspace(t, repo)
	team := authRequest[domain.Team](t, admin, http.MethodPost, host.URL+"/api/workspaces/"+data.Workspace.URLKey+"/teams", map[string]any{"name": "P07 public", "key": "P07"}, "", http.StatusCreated)
	authRequest[domain.Project](t, admin, http.MethodPost, host.URL+"/api/projects", map[string]any{"name": "P07 project", "teamIds": []string{team.ID}}, data.Workspace.URLKey, http.StatusCreated)
	authRequest[domain.Initiative](t, admin, http.MethodPost, host.URL+"/api/initiatives", map[string]any{"name": "P07 initiative"}, data.Workspace.URLKey, http.StatusCreated)
	secret := idgen.New("p07-local-")
	keyID := idgen.New("p07-key-")
	if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "apiKeys"), data.Workspace.URLKey, "api_key.created", keyID, nil, func(next *domain.Bootstrap) error {
		next.APIKeys = append(next.APIKeys, domain.APIKey{ID: keyID, SecretHash: secretHash(secret), CreatorID: data.Viewer.ID, Scopes: []string{"read", "write"}, CreatedAt: time.Now().UTC()})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Settle asynchronous first-use telemetry before P03 checks the metadata
	// stamp. Authentication still reads the live credential on every request.
	if err := repo.RecordAPIKeyUse(t.Context(), data.Workspace.URLKey, keyID, ""); err != nil {
		t.Fatal(err)
	}
	scaleP07Wait(t, func() bool {
		metadata, ok := repo.WorkspaceMetadataFields(data.Workspace.URLKey, "apiKeys")
		for _, key := range metadata.APIKeys {
			if key.ID == keyID && key.LastUsedAt != nil {
				return ok
			}
		}
		return false
	})
	data, ok := repo.BootstrapFor(data.Workspace.URLKey)
	if !ok {
		t.Fatal("owned fixture workspace is missing")
	}
	return &mcpContractFixture{repository: repo, service: service, host: host, data: data, secret: secret}
}
