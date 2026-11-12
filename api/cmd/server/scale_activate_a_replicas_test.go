package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
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

func TestScaleActivateAOldAndNewBinariesShareCanonicalRecords(t *testing.T) {
	binary := os.Getenv("SCALE_ACTIVATE_A_OLD_API")
	if binary == "" {
		t.Skip("set SCALE_ACTIVATE_A_OLD_API to the API built from the starting main commit")
	}
	t.Setenv("FLOW_SYNC_MANIFEST", "on")
	t.Setenv("FLOW_INDEXED_READS", "on")
	t.Setenv("FLOW_SYNC_V2", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "off")
	config := scaleP03DatabaseConfig(t)
	if config.Driver != "sqlite" {
		t.Skip("the local binary compatibility fixture uses a shared SQLite database")
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
	repo.SetWorkspaceCoordinator(coordinator)
	f := apiKeyScopeFixture(t, repo)
	f.service.coordinator, f.service.coordinationContext = coordinator, ctx
	f.service.startCoordination()
	workspace := f.data.Workspace.URLKey
	directory := t.TempDir()
	command := exec.Command(binary, "-test.run=^TestScaleActivateAReplicaProcess$", "-test.timeout=3m")
	command.Dir = directory
	command.Env = append(os.Environ(),
		"SCALE_ACTIVATE_A_CHILD=1", "SCALE_ACTIVATE_A_DATABASE="+config.Path, "SCALE_ACTIVATE_A_REDIS="+redis.Addr(), "SCALE_ACTIVATE_A_DIRECTORY="+directory,
		"FLOW_SYNC_MANIFEST=off", "FLOW_INDEXED_READS=off", "FLOW_SYNC_V2=off", "FLOW_RECORD_HYDRATION=off",
		"GOMAXPROCS=4", "GOMEMLIMIT=1536MiB")
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
	ready := false
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		if raw, err := os.ReadFile(filepath.Join(directory, "url")); err == nil {
			oldURL, ready = string(raw), true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ready {
		t.Fatal("old API did not become healthy; process output suppressed")
	}
	project := authRequest[domain.Project](t, f.admin, http.MethodPost, oldURL+"/api/projects", map[string]any{"name": "Old writer project", "teamIds": []string{"scope-public"}}, workspace, http.StatusCreated)
	for index := range 4 {
		writer, reader := oldURL, f.host.URL
		if index%2 == 1 {
			writer, reader = reader, writer
		}
		name := fmt.Sprintf("Mixed writer project %d", index)
		authRequest[domain.Project](t, f.admin, http.MethodPatch, writer+"/api/projects/"+project.ID, map[string]any{"name": name}, workspace, http.StatusOK)
		current := authRequest[domain.Project](t, f.admin, http.MethodGet, reader+"/api/projects/"+project.ID, nil, workspace, http.StatusOK)
		if current.Name != name {
			t.Fatal("mixed binary project write was lost")
		}
	}
	document := authRequest[domain.Document](t, f.admin, http.MethodPost, f.host.URL+"/api/documents", map[string]any{"title": "New writer document", "content": "Canonical body", "teamIds": []string{"scope-public"}}, workspace, http.StatusCreated)
	authRequest[domain.Document](t, f.admin, http.MethodPatch, oldURL+"/api/documents/"+document.ID, map[string]any{"title": "Old writer document edit"}, workspace, http.StatusOK)
	current := authRequest[domain.Document](t, f.admin, http.MethodGet, f.host.URL+"/api/documents/"+document.ID, nil, workspace, http.StatusOK)
	if current.Title != "Old writer document edit" || current.Content != "Canonical body" {
		t.Fatal("mixed binary document edit lost the canonical body")
	}
	for _, base := range []string{oldURL, f.host.URL} {
		for _, restriction := range []struct {
			name  string
			teams []string
		}{{"selected-empty", []string{}}, {"selected-set", []string{"scope-public"}}} {
			token, _ := apiKeyScopeCredential(t, f, "user", "selected", restriction.teams, []string{"read"})
			viewer := scaleP01PermissionViewer{client: f.admin, secret: token}
			status, body := scaleP01PermissionRead(t, viewer, base, workspace, "/api/projects/scope-hidden-project")
			if status != http.StatusForbidden || bytes.Contains(body, []byte("Hidden project")) {
				t.Fatal("mixed binary credential scope changed")
			}
		}
	}
}
