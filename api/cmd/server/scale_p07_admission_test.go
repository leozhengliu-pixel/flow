package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func scaleP07DatabaseConfig(t *testing.T) store.DatabaseConfig {
	t.Helper()
	driver, databaseURL := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if driver == "" && databaseURL == "" {
		driver, databaseURL = os.Getenv("FLOW_MCP_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_MCP_TEST_DATABASE_URL")
	}
	if driver == "" && databaseURL == "" {
		return store.DatabaseConfig{Driver: "sqlite", Path: filepath.Join(t.TempDir(), "p07.db"), MaxOpenConns: 1}
	}
	if driver == "" || databaseURL == "" {
		t.Fatal("both test database driver and URL are required")
	}
	config := store.DatabaseConfig{Driver: driver, URL: databaseURL, MaxOpenConns: 4}
	if driver == "sqlite" {
		config.Path, config.MaxOpenConns = databaseURL, 1
	}
	return config
}

func scaleP07Fixture(t *testing.T) *mcpContractFixture {
	t.Helper()
	config := scaleP07DatabaseConfig(t)
	if config.Driver == "sqlite" {
		config.URL = config.Path
	}
	return scaleP07ExternalFixture(t, config.Driver, config.URL)
}

func scaleP07Wait(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for !condition() {
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("admission did not reach the expected state")
		}
	}
}

func TestScaleP07ExistingLimits(t *testing.T) {
	for _, name := range []string{"FLOW_METADATA_READ_CONCURRENCY", "FLOW_METADATA_READ_QUEUE_LIMIT", "FLOW_METADATA_READ_QUEUE_TIMEOUT", "FLOW_RESOURCE_READ_CONCURRENCY", "FLOW_RESOURCE_READ_QUEUE_LIMIT", "FLOW_RESOURCE_READ_QUEUE_TIMEOUT"} {
		t.Setenv(name, "")
	}
	metadata, projection := metadataReadGateFromEnv(), projectionGateFromEnv()
	if cap(metadata.slots) != 4 || metadata.maxQueue != 64 || metadata.wait != 20*time.Second || cap(projection.slots) != 6 || projection.maxQueue != 128 || projection.wait != 15*time.Second {
		t.Fatal("existing admission limits changed")
	}
}

func TestScaleP07RecordReadsBypassSaturatedProjectionGate(t *testing.T) {
	f := scaleP07Fixture(t)
	t.Setenv("FLOW_RECORD_ACCESS", "on")
	gate := f.service.projectionGate
	servedBefore := gate.served.Load()
	gate.wait = time.Millisecond
	gate.maxQueue = 0
	for range cap(gate.slots) {
		gate.slots <- struct{}{}
	}
	client := &http.Client{Transport: issueReadKeyTransport{secret: f.secret}}
	for _, path := range []string{"/api/projects/" + f.data.Projects[0].ID, "/api/initiatives/" + f.data.Initiatives[0].ID} {
		authRequest[any](t, client, "GET", f.host.URL+path, nil, f.data.Workspace.URLKey, http.StatusOK)
	}
	authRequest[any](t, client, "GET", f.host.URL+"/api/projects?limit=1", nil, f.data.Workspace.URLKey, http.StatusServiceUnavailable)
	for range cap(gate.slots) {
		<-gate.slots
	}
	if gate.served.Load() != servedBefore || gate.rejected.Load() != 1 {
		t.Fatal("record reads entered the full projection gate")
	}
}

func TestScaleP07ExistingGateCancellation(t *testing.T) {
	for _, projection := range []bool{false, true} {
		name := "bootstrap"
		if projection {
			name = "projection"
		}
		t.Run(name, func(t *testing.T) {
			gate := newMetadataReadGate(1, 4, time.Second, 0)
			var calls atomic.Int64
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
			var handler http.Handler = gate.wrap(next)
			if projection {
				handler = gate.holdProjections(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if acquireProjection(r) {
						next.ServeHTTP(w, r)
					}
				}))
			}
			gate.slots <- struct{}{}
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan struct{})
			go func() {
				defer close(done)
				handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/projects/p07", nil).WithContext(ctx))
			}()
			scaleP07Wait(t, func() bool { return gate.queued.Load() == 1 })
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("cancelled waiter did not exit")
			}
			if calls.Load() != 0 || gate.queued.Load() != 0 || gate.cancelled.Load() != 1 || len(gate.slots) != 1 {
				t.Fatal("cancelled waiter retained work")
			}
			<-gate.slots
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/projects/p07", nil))
			if calls.Load() != 1 || len(gate.slots) != 0 {
				t.Fatal("next request did not release its slot")
			}
		})
	}
}

func TestScaleP07ExistingGatePanicAndNesting(t *testing.T) {
	for _, projection := range []bool{false, true} {
		gate := newMetadataReadGate(1, 4, time.Second, 0)
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if projection && (!acquireProjection(r) || !acquireProjectionWaiting(r)) {
				t.Fatal("nested projection did not reuse its slot")
			}
			panic(http.ErrAbortHandler)
		})
		var handler http.Handler = gate.wrap(next)
		if projection {
			handler = gate.holdProjections(next)
		}
		for range 3 {
			func() {
				defer func() {
					if recover() != http.ErrAbortHandler {
						t.Fatal("unexpected panic")
					}
				}()
				handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/projects/p07", nil))
			}()
			if len(gate.slots) != 0 || gate.queued.Load() != 0 {
				t.Fatal("panic leaked an admission slot")
			}
		}
	}
}

func TestScaleP07ExistingMCPLimiterCancellationAndNesting(t *testing.T) {
	limiter := newToolCallLimiter(1)
	ctx, release, err := limiter.acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, nestedRelease, err := limiter.acquire(ctx)
	if err != nil || len(limiter.slots) != 1 {
		t.Fatal("nested MCP call reserved a second slot")
	}
	nestedRelease()
	waiter, cancel := context.WithCancel(t.Context())
	cancel()
	_, _, err = limiter.acquire(waiter)
	if !errors.Is(err, context.Canceled) || len(limiter.slots) != 1 {
		t.Fatal("cancelled MCP waiter retained a slot")
	}
	release()
	if len(limiter.slots) != 0 {
		t.Fatal("MCP caller retained a slot")
	}
}

func TestScaleP07PermissionMatrix(t *testing.T) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	scaleP01PermissionMatrix(t, scaleP07DatabaseConfig(t))
}

func TestScaleP07FixturePreservesUnrelatedAccount(t *testing.T) {
	config := scaleP07DatabaseConfig(t)
	repo, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	const password = "p07-unrelated-password"
	user, token, err := repo.Register(t.Context(), "Unrelated P07 account", idgen.New("p07-unrelated-")+"@example.test", password)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.VerifyEmail(t.Context(), token); err != nil {
		t.Fatal(err)
	}
	user.EmailVerified = true
	data, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), user), "Unrelated P07 workspace", idgen.New("p07-unrelated-"), "us")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = repo.DeleteWorkspace(context.Background(), data.Workspace.URLKey)
		_ = repo.EraseAccount(context.Background(), user.ID)
	})
	var owned domain.Bootstrap
	t.Run("owned-fixture", func(t *testing.T) {
		url := config.URL
		if config.Driver == "sqlite" {
			url = config.Path
		}
		f := scaleP07ExternalFixture(t, config.Driver, url)
		owned = f.data
		if owned.Viewer.ID == user.ID || owned.Workspace.URLKey == data.Workspace.URLKey {
			t.Fatal("fixture reused an unrelated principal")
		}
		client := &http.Client{Transport: issueReadKeyTransport{secret: f.secret}}
		authRequest[domain.Project](t, client, "GET", f.host.URL+"/api/projects/"+f.data.Projects[0].ID, nil, f.data.Workspace.URLKey, http.StatusOK)
	})
	if _, _, err = repo.Login(t.Context(), owned.Viewer.Email, scaleP01FixturePassword); err == nil {
		t.Fatal("owned fixture account survived cleanup")
	}
	if session, _, err := repo.Login(t.Context(), user.Email, password); err != nil || session.User.ID != user.ID {
		t.Fatal("fixture changed unrelated credentials")
	}
	if err = repo.ReloadAllWorkspaces(t.Context()); err != nil {
		t.Fatal(err)
	}
	if repo.HasWorkspace(owned.Workspace.URLKey) || !repo.HasWorkspace(data.Workspace.URLKey) {
		t.Fatal("fixture cleanup changed the wrong workspace")
	}
}
