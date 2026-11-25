package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/coordination"
	"flow/api/internal/domain"
	"flow/api/internal/store"

	"github.com/alicebob/miniredis/v2"
)

func TestScaleP09ActivateB1MixedBinaryRollback(t *testing.T) {
	scaleActivateB1MixedBinaryRollback(t, true)
}

func TestScaleP09ActivateB1JournalMixedBinaryRollback(t *testing.T) {
	scaleActivateB1MixedBinaryRollback(t, false)
}

func scaleActivateB1MixedBinaryRollback(t *testing.T, consumers bool) {
	t.Helper()
	binary := os.Getenv("SCALE_ACTIVATE_B1_OLD_API")
	if binary == "" {
		t.Skip("set SCALE_ACTIVATE_B1_OLD_API to the saved starting-main test binary")
	}
	for _, flag := range []string{"FLOW_RECORD_JOURNAL", "FLOW_RECORD_PEER_SYNC", "FLOW_SYNC_V2", "FLOW_SYNC_MANIFEST", "FLOW_INDEXED_READS"} {
		mode := "off"
		if flag == "FLOW_RECORD_JOURNAL" || consumers {
			mode = "on"
		}
		t.Setenv(flag, mode)
	}
	t.Setenv("FLOW_RECORD_HYDRATION", "off")
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	config := scaleP03DatabaseConfig(t)
	if config.Driver != "sqlite" {
		t.Skip("local mixed-binary fixture shares its owned SQLite file")
	}
	repo, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	redis := miniredis.RunT(t)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	coordinator, err := coordination.Open(ctx, coordination.Config{Mode: "standalone", Addrs: []string{redis.Addr()}, Prefix: "scale-activate-a", ConnectTimeout: time.Second, LockTTL: 5 * time.Second, LockWait: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = coordinator.Close() })
	f := apiKeyScopeFixture(t, repo)
	credentials := []string{}
	for _, teams := range [][]string{{}, {"scope-public"}} {
		token, _ := apiKeyScopeCredential(t, f, "user", "selected", teams, []string{"read"})
		credentials = append(credentials, token)
	}
	repo.SetWorkspaceCoordinator(coordinator)
	service := &server{store: repo, uploadPath: t.TempDir(), coordinator: coordinator, coordinationContext: ctx}
	host := httptest.NewServer(newHandler(service))
	t.Cleanup(host.Close)
	if err := service.stopDeliveryScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.service, f.host = service, host
	workspace := f.data.Workspace.URLKey
	directory := t.TempDir()
	command := exec.Command(binary, "-test.run=^TestScaleActivateAReplicaProcess$", "-test.timeout=3m")
	command.Dir = directory
	command.Env = append(os.Environ(),
		"SCALE_ACTIVATE_A_CHILD=1", "SCALE_ACTIVATE_A_DATABASE="+config.Path,
		"SCALE_ACTIVATE_A_REDIS="+redis.Addr(), "SCALE_ACTIVATE_A_DIRECTORY="+directory,
		"FLOW_RECORD_JOURNAL=off", "FLOW_RECORD_PEER_SYNC=off", "FLOW_SYNC_V2=off",
		"FLOW_SYNC_MANIFEST=off", "FLOW_INDEXED_READS=off", "FLOW_RECORD_HYDRATION=off",
		"FLOW_BOUNDED_METADATA=off", "GOMAXPROCS=4", "GOMEMLIMIT=1536MiB")
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	t.Cleanup(func() {
		_ = command.Process.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = command.Process.Kill()
			<-done
		}
	})
	oldURL := ""
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		if raw, err := os.ReadFile(filepath.Join(directory, "url")); err == nil {
			oldURL = string(raw)
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if oldURL == "" {
		t.Fatal("old API did not become healthy; process output suppressed")
	}
	project := authRequest[domain.Project](t, f.admin, http.MethodPost, oldURL+"/api/projects", map[string]any{"name": "B1 old writer project", "teamIds": []string{"scope-public"}}, workspace, http.StatusCreated)
	for index := range 6 {
		writer, reader := oldURL, host.URL
		if index%2 == 1 {
			writer, reader = reader, writer
		}
		name := fmt.Sprintf("B1 mixed writer %d", index)
		authRequest[domain.Project](t, f.admin, http.MethodPatch, writer+"/api/projects/"+project.ID, map[string]any{"name": name}, workspace, http.StatusOK)
		current := authRequest[domain.Project](t, f.admin, http.MethodGet, reader+"/api/projects/"+project.ID, nil, workspace, http.StatusOK)
		if current.Name != name {
			t.Fatal("mixed binary project write was lost")
		}
	}
	document := authRequest[domain.Document](t, f.admin, http.MethodPost, host.URL+"/api/documents", map[string]any{"title": "B1 document", "content": "Canonical B1 body", "teamIds": []string{"scope-public"}}, workspace, http.StatusCreated)
	authRequest[domain.Document](t, f.admin, http.MethodPatch, oldURL+"/api/documents/"+document.ID, map[string]any{"title": "B1 old document edit"}, workspace, http.StatusOK)
	current := authRequest[domain.Document](t, f.admin, http.MethodGet, host.URL+"/api/documents/"+document.ID, nil, workspace, http.StatusOK)
	if current.Content != "Canonical B1 body" || current.Title != "B1 old document edit" {
		t.Fatal("old writer lost the complete canonical document")
	}
	for _, base := range []string{oldURL, host.URL} {
		for _, token := range credentials {
			status, body := scaleP01PermissionRead(t, scaleP01PermissionViewer{client: f.admin, secret: token}, base, workspace, "/api/projects/scope-hidden-project")
			if status != http.StatusForbidden || bytes.Contains(body, []byte("Hidden project")) {
				t.Fatal("mixed replica leaked a hidden record")
			}
		}
	}
	for _, flag := range []string{"FLOW_RECORD_JOURNAL", "FLOW_RECORD_PEER_SYNC", "FLOW_SYNC_V2", "FLOW_SYNC_MANIFEST", "FLOW_INDEXED_READS"} {
		t.Setenv(flag, "off")
	}
	if service.recordPeerSyncMode() != "off" || syncV2Enabled() {
		t.Fatal("explicit rollback did not restore legacy coordination")
	}
	rolledBack := authRequest[domain.Document](t, f.admin, http.MethodGet, host.URL+"/api/documents/"+document.ID, nil, workspace, http.StatusOK)
	if rolledBack.Title != current.Title || rolledBack.Content != current.Content {
		t.Fatal("rollback changed the canonical response")
	}
}
