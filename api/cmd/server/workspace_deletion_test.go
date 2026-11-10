package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type workspaceDeletionFixture struct {
	server     *server
	handler    http.Handler
	repository *store.SQLiteStore
	uploads    string
	workspace  domain.Workspace
	attachment string
}

func newWorkspaceDeletionFixture(t *testing.T) workspaceDeletionFixture {
	t.Helper()
	t.Setenv("FLOW_CONNECTOR_SECRET_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	uploads := t.TempDir()
	s := &server{store: repository, uploadPath: uploads, authDisabled: true}
	handler := newHandler(s)
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Customer escalation", "teamId": bootstrap.Teams[0].ID}, http.StatusCreated)
	attachment := uploadIssueAttachmentForTest(t, handler, issue.ID, "contract.pdf", "signed contract")
	return workspaceDeletionFixture{server: s, handler: handler, repository: repository, uploads: uploads, workspace: bootstrap.Workspace, attachment: strings.TrimPrefix(attachment.URL, "/uploads/")}
}

func (f workspaceDeletionFixture) fileExists(name string) bool {
	_, err := os.Stat(filepath.Join(f.uploads, name))
	return err == nil
}

func TestScheduledWorkspaceDeletionPurgesAfterTheGracePeriod(t *testing.T) {
	ctx := context.Background()
	f := newWorkspaceDeletionFixture(t)
	key := f.workspace.URLKey
	// A provider credential sealed for one of the workspace's integrations.
	if err := f.repository.MutateWorkspace(ctx, key, "integration.connected", "integration_gl", nil, func(data *domain.Bootstrap) error {
		data.IntegrationConnections = append(data.IntegrationConnections, domain.IntegrationConnection{ID: "integration_gl", Provider: "gitlab", Status: "connected", Config: map[string]string{}, Scopes: []string{}, Channels: []string{}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	credential := connectorSecretID(key, "provider", "integration_gl")
	if err := f.repository.PutConnectorSecret(ctx, credential, []byte(`{"token":"secret"}`), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	// Another workspace and a file it is not involved with stay.
	other, err := f.repository.CreateWorkspace(ctx, "Other", "other-space", "us")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.uploads, "unrelated.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	scheduled := requestJSON[map[string]any](t, f.handler, http.MethodDelete, "/api/workspaces/"+key, nil, http.StatusOK)
	requested, _ := time.Parse(time.RFC3339Nano, scheduled["deletionRequestedAt"].(string))
	purgeAt, _ := time.Parse(time.RFC3339Nano, scheduled["deletionScheduledFor"].(string))
	if !purgeAt.Equal(requested.Add(workspaceDeletionGrace)) {
		t.Fatalf("deletionScheduledFor = %v, want %v", purgeAt, requested.Add(workspaceDeletionGrace))
	}

	// Within the grace period nothing is deleted.
	f.server.purgeDeletedWorkspaces(ctx, time.Now().UTC())
	if _, ok := f.repository.BootstrapFor(key); !ok || !f.fileExists(f.attachment) {
		t.Fatal("workspace purged during its grace period")
	}

	f.server.purgeDeletedWorkspaces(ctx, time.Now().UTC().Add(workspaceDeletionGrace+time.Hour))
	if _, ok := f.repository.BootstrapFor(key); ok {
		t.Fatal("workspace still exists after the grace period")
	}
	if f.fileExists(f.attachment) {
		t.Fatal("attachment file left behind")
	}
	if !f.fileExists("unrelated.txt") {
		t.Fatal("a file the workspace does not use was deleted")
	}
	if _, err := f.repository.ReadConnectorSecret(ctx, credential, false); err == nil {
		t.Fatal("integration credential left behind")
	}
	if _, ok := f.repository.BootstrapFor(other.Workspace.URLKey); !ok {
		t.Fatal("other workspace was deleted")
	}
	receipt, found, err := f.repository.WorkspaceDeletionReceipt(ctx, f.workspace.ID)
	if err != nil || !found {
		t.Fatalf("receipt found=%v err=%v", found, err)
	}
	if receipt.Status != store.WorkspacePurgeCompleted || receipt.CompletedAt == nil || receipt.WorkspaceKey != key || receipt.RequestRef == "" {
		t.Fatalf("receipt = %#v", receipt)
	}
	for _, name := range []string{"objects.uploads", "issue_records", "workspace_states", "workspace_memberships", "connector_secrets", "domain_events"} {
		if receipt.Counts[name] <= 0 {
			t.Errorf("receipt count %s = %d", name, receipt.Counts[name])
		}
	}
	raw, _ := json.Marshal(receipt)
	for _, personal := range []string{"Customer escalation", "contract", "@"} {
		if strings.Contains(string(raw), personal) {
			t.Errorf("receipt carries %q: %s", personal, raw)
		}
	}
	// A later pass has nothing left to do.
	f.server.purgeDeletedWorkspaces(ctx, time.Now().UTC().Add(workspaceDeletionGrace+2*time.Hour))
	if pending, _ := f.repository.WorkspaceDeletionReceipts(ctx, store.WorkspacePurgePurging, 10); len(pending) != 0 {
		t.Fatalf("pending purges = %#v", pending)
	}
}

func TestCanceledWorkspaceDeletionIsNotPurged(t *testing.T) {
	ctx := context.Background()
	f := newWorkspaceDeletionFixture(t)
	key := f.workspace.URLKey
	requestJSON[map[string]any](t, f.handler, http.MethodDelete, "/api/workspaces/"+key, nil, http.StatusOK)
	canceled := requestJSON[map[string]any](t, f.handler, http.MethodPost, "/api/workspaces/"+key+"/cancel-deletion", nil, http.StatusOK)
	if canceled["deletionRequestedAt"] != nil || canceled["deletionScheduledFor"] != nil {
		t.Fatalf("cancel response = %#v", canceled)
	}
	f.server.purgeDeletedWorkspaces(ctx, time.Now().UTC().Add(workspaceDeletionGrace+time.Hour))
	if _, ok := f.repository.BootstrapFor(key); !ok || !f.fileExists(f.attachment) {
		t.Fatal("canceled deletion was purged")
	}
}

func TestWorkspaceDeletionCannotBeCanceledOncePurging(t *testing.T) {
	ctx := context.Background()
	f := newWorkspaceDeletionFixture(t)
	key := f.workspace.URLKey
	requestJSON[map[string]any](t, f.handler, http.MethodDelete, "/api/workspaces/"+key, nil, http.StatusOK)
	if _, started, err := f.repository.BeginWorkspacePurge(ctx, key, time.Now().UTC()); err != nil || !started {
		t.Fatalf("begin purge started=%v err=%v", started, err)
	}
	requestJSON[map[string]any](t, f.handler, http.MethodPost, "/api/workspaces/"+key+"/cancel-deletion", nil, http.StatusConflict)
}

// A purge interrupted after the workspace left the catalog (a crash or a
// restart) is finished by the next maintenance pass, from its receipt.
func TestInterruptedWorkspacePurgeFinishesOnTheNextPass(t *testing.T) {
	ctx := context.Background()
	f := newWorkspaceDeletionFixture(t)
	key := f.workspace.URLKey
	requestJSON[map[string]any](t, f.handler, http.MethodDelete, "/api/workspaces/"+key, nil, http.StatusOK)
	if _, started, err := f.repository.BeginWorkspacePurge(ctx, key, time.Now().UTC()); err != nil || !started {
		t.Fatalf("begin purge started=%v err=%v", started, err)
	}
	if err := f.repository.RemoveWorkspaceFromCatalog(ctx, key, nil); err != nil {
		t.Fatal(err)
	}
	if !f.fileExists(f.attachment) {
		t.Fatal("setup: attachment already gone")
	}
	// The workspace is no longer due (it left the catalog): only the receipt
	// brings the purge back.
	f.server.purgeDeletedWorkspaces(ctx, time.Now().UTC())
	if f.fileExists(f.attachment) {
		t.Fatal("resumed purge left the attachment file")
	}
	receipt, found, err := f.repository.WorkspaceDeletionReceipt(ctx, f.workspace.ID)
	if err != nil || !found || receipt.Status != store.WorkspacePurgeCompleted {
		t.Fatalf("receipt = %#v found=%v err=%v", receipt, found, err)
	}
	if receipt.Counts["issue_records"] <= 0 {
		t.Fatalf("resumed purge counts = %v", receipt.Counts)
	}
}
