package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/coding"
	"flow/api/internal/domain"
	"flow/api/internal/objectstore"
	"flow/api/internal/store"
)

// Renaming a workspace through the API keeps its members' connector
// credentials, provider credentials and coding harness state readable under
// the new key, and leaves nothing named after the old one.
func TestWorkspaceRenameKeepsCredentialsAndHarnessState(t *testing.T) {
	ctx := context.Background()
	t.Setenv("FLOW_CONNECTOR_SECRET_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	dbPath := filepath.Join(t.TempDir(), "flow.db")
	repository, err := store.OpenSQLiteTestFixture(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	objects, err := objectstore.Open(ctx, objectstore.Config{Driver: "local", LocalPath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	repository.SetWorkspaceKeyObjects(codingHarnessKeyObjects{objects: objects})
	s := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true, objectStore: objects}
	handler := newHandler(s)
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	oldKey, newKey := bootstrap.Workspace.URLKey, "renamed-space"
	member := bootstrap.Viewer.ID
	if err := repository.MutateWorkspace(ctx, oldKey, "integration.connected", "integration_gl", nil, func(data *domain.Bootstrap) error {
		data.IntegrationConnections = append(data.IntegrationConnections, domain.IntegrationConnection{ID: "integration_gl", Provider: "gitlab", Status: "connected", Config: map[string]string{}, Scopes: []string{}, Channels: []string{}})
		setApplicationPolicies(data, []applicationPolicy{{ID: "connector_mcp", Name: "MCP", Kind: "mcp", OwnerID: member, Status: "approved"}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	credentials := map[[2]string][]byte{
		{"provider", "integration_gl"}:            []byte(`{"token":"provider"}`),
		{member, "connector_mcp"}:                 []byte(`{"token":"member"}`),
		{"gitlab-member-oauth", "integration_gl"}: []byte(`{"secret":"app"}`),
	}
	for pair, secret := range credentials {
		if err := repository.PutConnectorSecret(ctx, connectorSecretID(oldKey, pair[0], pair[1]), secret, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	// The store lists the same credential ids the server writes.
	ids, err := repository.WorkspaceCredentialSecretIDs(ctx, oldKey)
	if err != nil {
		t.Fatal(err)
	}
	for pair := range credentials {
		if !slices.Contains(ids, connectorSecretID(oldKey, pair[0], pair[1])) {
			t.Fatalf("store credential ids miss %v", pair)
		}
	}
	const session = "cs_rename"
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `INSERT INTO coding_sessions(id,workspace_key,agent_session_id,creator_id,source,state,phase,phase_started_at,data,created_at,updated_at) VALUES(?,?,'a1','u1','issue','complete','idle',1,'{}',1,1)`, session, oldKey); err != nil {
		t.Fatal(err)
	}
	if _, err := objects.Put(ctx, coding.HarnessStateObjectKey(oldKey, session), bytes.NewReader([]byte("harness")), "application/zstd"); err != nil {
		t.Fatal(err)
	}

	requestJSON[domain.Bootstrap](t, handler, http.MethodPatch, "/api/workspaces/"+oldKey, map[string]any{"urlKey": newKey}, http.StatusOK)

	for pair, secret := range credentials {
		got, err := repository.ReadConnectorSecret(ctx, connectorSecretID(newKey, pair[0], pair[1]), false)
		if err != nil || !bytes.Equal(got, secret) {
			t.Errorf("credential %v under the new key: %q (%v)", pair, got, err)
		}
		if _, err := repository.ReadConnectorSecret(ctx, connectorSecretID(oldKey, pair[0], pair[1]), false); err == nil {
			t.Errorf("credential %v still under the old key", pair)
		}
	}
	reader, _, _, err := objects.Open(ctx, coding.HarnessStateObjectKey(newKey, session))
	if err != nil {
		t.Fatalf("harness state under the new key: %v", err)
	}
	state, _ := io.ReadAll(reader)
	reader.Close()
	if string(state) != "harness" {
		t.Fatalf("harness state = %q", state)
	}
	if _, _, _, err := objects.Open(ctx, coding.HarnessStateObjectKey(oldKey, session)); !objectMissing(err) {
		t.Fatalf("harness state left under the old key: %v", err)
	}
}
