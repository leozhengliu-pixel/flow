package main

import (
	"context"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/coordination"
	"flow/api/internal/store"
)

func TestScaleActivateAReplicaProcess(t *testing.T) {
	if os.Getenv("SCALE_ACTIVATE_A_CHILD") != "1" {
		t.Skip("activation compatibility subprocess")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	repo, err := store.OpenDatabase(store.DatabaseConfig{Driver: "sqlite", Path: os.Getenv("SCALE_ACTIVATE_A_DATABASE"), MaxOpenConns: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	coordinator, err := coordination.Open(ctx, coordination.Config{Mode: "standalone", Addrs: []string{os.Getenv("SCALE_ACTIVATE_A_REDIS")}, Prefix: "scale-activate-a", ConnectTimeout: time.Second, LockTTL: 5 * time.Second, LockWait: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer coordinator.Close()
	repo.SetWorkspaceCoordinator(coordinator)
	service := &server{store: repo, uploadPath: os.Getenv("SCALE_ACTIVATE_A_DIRECTORY"), coordinator: coordinator, coordinationContext: ctx}
	host := httptest.NewServer(newHandler(service))
	defer host.Close()
	if err := service.stopDeliveryScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(os.Getenv("SCALE_ACTIVATE_A_DIRECTORY"), "url"), []byte(host.URL), 0o600); err != nil {
		t.Fatal(err)
	}
	<-ctx.Done()
}
