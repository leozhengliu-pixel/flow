//go:build loadtest

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flow/api/internal/coordination"
	"flow/api/internal/domain"
	"flow/api/internal/store"

	"github.com/alicebob/miniredis/v2"
)

// TestLoadExternalUsers preserves the invocation specified by the scale plan.
// The actual P01 entry point also has the package's required test prefix.
func TestLoadExternalUsers(t *testing.T) {
	if os.Getenv("FLOW_LOAD_BASE_URLS") == "" {
		t.Skip("set FLOW_LOAD_BASE_URLS and FLOW_LOAD_FIXTURE")
	}
	scaleP01External(t)
}

func TestScaleP01ExternalUsers(t *testing.T) {
	if os.Getenv("FLOW_LOAD_BASE_URLS") == "" {
		t.Skip("opt-in external load generator")
	}
	scaleP01External(t)
}

func scaleP01External(t *testing.T) {
	bases := scaleP01URLs(t, "FLOW_LOAD_BASE_URLS", true)
	fixture := scaleP01ReadFixture(t, os.Getenv("FLOW_LOAD_FIXTURE"))
	transport := &http.Transport{MaxIdleConnsPerHost: 4}
	defer transport.CloseIdleConnections()
	for _, identity := range append(append([]scaleP01Identity{}, fixture.Identities...), fixture.Admin) {
		client := scaleP01Client(identity, bases, transport, 10*time.Second)
		for _, base := range bases {
			request, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/api/auth/session", nil)
			response, err := client.Do(request)
			if err != nil {
				t.Fatal("external identity preflight failed (credential details suppressed)")
			}
			var session domain.AuthSession
			err = json.NewDecoder(response.Body).Decode(&session)
			response.Body.Close()
			if err != nil || response.StatusCode != http.StatusOK || session.User.ID != identity.ID {
				t.Fatal("external session does not match the fixture principal")
			}
		}
	}
	sources := []scaleP01MetricSource{}
	for _, endpoint := range scaleP01URLs(t, "FLOW_LOAD_METRICS_URLS", false) {
		sources = append(sources, scaleP01HTTPMetrics(endpoint))
	}
	scaleP01Run(t, bases, fixture, sources, true)
}

func scaleP01HTTPMetrics(endpoint string) scaleP01MetricSource {
	client := &http.Client{Timeout: 5 * time.Second}
	return func(ctx context.Context) (scaleP01Metrics, error) {
		var metrics scaleP01Metrics
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return metrics, err
		}
		response, err := client.Do(req)
		if err != nil {
			return metrics, err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return metrics, fmt.Errorf("P01 metric source status %d", response.StatusCode)
		}
		err = json.NewDecoder(response.Body).Decode(&metrics)
		return metrics, err
	}
}

func TestScaleP01ReplicaProcess(t *testing.T) {
	if os.Getenv("FLOW_SCALE_P01_CHILD") != "1" {
		t.Skip("P01 replica subprocess")
	}
	dir := os.Getenv("FLOW_SCALE_P01_CHILD_DIR")
	var config store.DatabaseConfig
	raw, err := os.ReadFile(filepath.Join(dir, "database.json"))
	if err != nil || json.Unmarshal(raw, &config) != nil {
		t.Fatal("invalid child database configuration")
	}
	repo, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	probe, err := repo.EnableScaleP01Probe(config)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	coord, err := coordination.Open(ctx, coordination.Config{Mode: "standalone", Addrs: []string{os.Getenv("FLOW_SCALE_P01_REDIS")}, Prefix: "scale-p01", LockWait: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer coord.Close()
	repo.SetWorkspaceCoordinator(coord)
	srv := &server{store: repo, uploadPath: dir, coordinator: coord, coordinationContext: ctx}
	// Metrics are on this test process's loopback server, never a production route.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /_scale-p01/metrics", func(w http.ResponseWriter, r *http.Request) {
		value, _ := scaleP01LocalMetricSource(probe)(r.Context())
		writeJSON(w, http.StatusOK, value)
	})
	mux.Handle("/", scaleP01Handler(newHandler(srv)))
	host := httptest.NewServer(mux)
	defer host.Close()
	if err := os.WriteFile(filepath.Join(dir, "url"), []byte(host.URL), 0o600); err != nil {
		t.Fatal(err)
	}
	<-ctx.Done()
}

// This opt-in integration test launches two API OS processes. The generator
// and Redis fixture stay in the parent; API allocations/CPU are separate.
func TestScaleP01TwoProcessLoad(t *testing.T) {
	if os.Getenv("FLOW_LOAD_TWO_PROCESS") != "1" {
		t.Skip("set FLOW_LOAD_TWO_PROCESS=1")
	}
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	root := t.TempDir()
	config := store.DatabaseConfig{Driver: "sqlite", Path: filepath.Join(root, "external.db"), FixtureProfile: "test", FixturePassword: "test-password", MaxOpenConns: 1}
	if driver := os.Getenv("FLOW_TEST_DATABASE_DRIVER"); driver != "" {
		config.Driver, config.URL, config.MaxOpenConns = driver, os.Getenv("FLOW_TEST_DATABASE_URL"), 20
		if config.URL == "" {
			t.Fatal("FLOW_TEST_DATABASE_URL is required")
		}
	}
	redisServer := miniredis.RunT(t)
	// Close the seeding handlers and their stores before starting the processes.
	var fixture scaleP01Fixture
	t.Run("seed", func(t *testing.T) { fixture = scaleP01SeedFixture(t, scaleP01Peers(t, config, redisServer.Addr())) })
	if t.Failed() {
		return
	}
	fixtureRaw, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	fixturePath := filepath.Join(root, "fixture.json")
	if err := scaleP01WriteCredentials(fixturePath, fixtureRaw); err != nil {
		t.Fatal(err)
	}
	var bases []string
	for index := range 2 {
		dir := filepath.Join(root, fmt.Sprintf("replica-%d", index))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(config)
		if err := os.WriteFile(filepath.Join(dir, "database.json"), raw, 0o600); err != nil {
			t.Fatal(err)
		}
		log, err := os.Create(filepath.Join(dir, "replica.log"))
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(os.Args[0], "-test.run=^TestScaleP01ReplicaProcess$", "-test.timeout=45m")
		cmd.Env = append(os.Environ(), "FLOW_SCALE_P01_CHILD=1", "FLOW_SCALE_P01_CHILD_DIR="+dir, "FLOW_SCALE_P01_REDIS="+redisServer.Addr())
		cmd.Stdout, cmd.Stderr = log, log
		if err := cmd.Start(); err != nil {
			log.Close()
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		t.Cleanup(func() {
			_ = cmd.Process.Signal(os.Interrupt)
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("replica %d exited unsuccessfully", index)
				}
			case <-time.After(10 * time.Second):
				_ = cmd.Process.Kill()
				<-done
				t.Errorf("replica %d required kill", index)
			}
			log.Close()
		})
		deadline := time.Now().Add(90 * time.Second)
		for {
			if raw, err := os.ReadFile(filepath.Join(dir, "url")); err == nil {
				bases = append(bases, string(raw))
				break
			}
			select {
			case err := <-done:
				done <- err
				t.Fatalf("replica %d failed startup", index)
			default:
			}
			if time.Now().After(deadline) {
				t.Fatalf("replica %d startup timed out", index)
			}
			if !scaleP01Pause(t.Context(), 20*time.Millisecond) {
				t.Fatal("startup canceled")
			}
		}
	}
	a, _ := scaleP01HTTPMetrics(bases[0] + "/_scale-p01/metrics")(t.Context())
	b, _ := scaleP01HTTPMetrics(bases[1] + "/_scale-p01/metrics")(t.Context())
	if a.PID == 0 || b.PID == 0 || a.PID == b.PID || a.PID == os.Getpid() || b.PID == os.Getpid() {
		t.Fatal("API processes must be distinct from each other and generator")
	}
	t.Setenv("FLOW_LOAD_BASE_URLS", strings.Join(bases, ","))
	t.Setenv("FLOW_LOAD_METRICS_URLS", bases[0]+"/_scale-p01/metrics,"+bases[1]+"/_scale-p01/metrics")
	t.Setenv("FLOW_LOAD_FIXTURE", fixturePath)
	scaleP01External(t)
}

// Prepare a credential fixture for APIs started separately by the operator.
// The opt-in test uses only the explicit throwaway test database configuration.
func TestScaleP01PrepareExternalFixture(t *testing.T) {
	if os.Getenv("FLOW_LOAD_PREPARE") != "1" {
		t.Skip("set FLOW_LOAD_PREPARE=1 and FLOW_LOAD_FIXTURE")
	}
	path := os.Getenv("FLOW_LOAD_FIXTURE")
	if path == "" || os.Getenv("FLOW_TEST_DATABASE_DRIVER") == "" || os.Getenv("FLOW_TEST_DATABASE_URL") == "" {
		t.Fatal("requires an explicit throwaway test database and fixture path")
	}
	fixture := scaleP01SeedFixture(t, scaleP01NewPeers(t))
	raw, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if err := scaleP01WriteCredentials(path, raw); err != nil {
		t.Fatal(err)
	}
}
