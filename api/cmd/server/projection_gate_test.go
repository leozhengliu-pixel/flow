package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Resource reads that build the viewer's whole projection share a bounded
// gate of their own: when it is full a read gets 503 + Retry-After instead of
// one more projection, a write waits, routes that build none are not held up,
// and a request's slot is free again when it ends.
func TestResourceReadsAreBoundedByTheProjectionGate(t *testing.T) {
	t.Setenv("FLOW_RECORD_ACCESS", "off")
	repo := pulseTestRepo(t)
	gate := newMetadataReadGate(1, 0, 50*time.Millisecond, 0)
	host := httptest.NewServer(newHandler(&server{store: repo, uploadPath: t.TempDir(), projectionGate: gate}))
	t.Cleanup(host.Close)
	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, "POST", host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	get := func(path string) *http.Response {
		t.Helper()
		request, _ := http.NewRequest(http.MethodGet, host.URL+path, nil)
		request.Header.Set("X-Workspace-Key", "test-workspace")
		response, err := admin.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		return response
	}
	if response := get("/api/projects/project_aut"); response.StatusCode != http.StatusOK {
		t.Fatalf("project read = %d", response.StatusCode)
	}
	if len(gate.slots) != 0 {
		t.Fatal("the slot was not released when the request ended")
	}

	gate.slots <- struct{}{} // another request is building a projection
	if response := get("/api/projects/project_aut"); response.StatusCode != http.StatusServiceUnavailable || response.Header.Get("Retry-After") == "" {
		t.Fatalf("project read with the gate full = %d, Retry-After %q", response.StatusCode, response.Header.Get("Retry-After"))
	}
	if response := get("/api/initiatives"); response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("public records read with the gate full = %d", response.StatusCode)
	}
	if response := get("/api/notifications?limit=1"); response.StatusCode != http.StatusOK {
		t.Fatalf("a route without a projection waited for the gate: %d", response.StatusCode)
	}
	// A write waits for the slot instead of failing.
	written := make(chan int, 1)
	go func() {
		written <- authRequestStatus(t, admin, http.MethodPatch, host.URL+"/api/projects/project_aut", map[string]any{"summary": "Gated"}, "test-workspace")
	}()
	select {
	case status := <-written:
		t.Fatalf("the write did not wait for the slot: %d", status)
	case <-time.After(150 * time.Millisecond):
	}
	<-gate.slots
	select {
	case status := <-written:
		if status != http.StatusOK {
			t.Fatalf("write after the slot freed = %d", status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the write never got the slot")
	}
	if gate.rejected.Load() < 2 || len(gate.slots) != 0 {
		t.Fatalf("rejected=%d busy=%d", gate.rejected.Load(), len(gate.slots))
	}
}

// authRequestStatus sends a JSON request and returns its status (no t.Fatal: it runs off the test goroutine).
func authRequestStatus(t *testing.T, client *http.Client, method, url string, input any, workspaceKey string) int {
	raw, _ := json.Marshal(input)
	request, _ := http.NewRequest(method, url, bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Workspace-Key", workspaceKey)
	response, err := client.Do(request)
	if err != nil {
		return -1
	}
	response.Body.Close()
	return response.StatusCode
}

// A read that waits and whose client leaves gives the place up without a projection.
func TestProjectionGateLetsALeavingClientGo(t *testing.T) {
	gate := newMetadataReadGate(1, 4, time.Minute, 0)
	gate.slots <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, "/api/projects/x", nil).WithContext(ctx)
	hold := &projectionHold{gate: gate}
	request = request.WithContext(context.WithValue(request.Context(), projectionHoldKey{}, hold))
	done := make(chan bool, 1)
	go func() { done <- acquireProjection(request) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case admitted := <-done:
		if admitted || hold.held || gate.cancelled.Load() != 1 {
			t.Fatalf("admitted=%v held=%v cancelled=%d", admitted, hold.held, gate.cancelled.Load())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the waiting read did not notice its client left")
	}
}

// A handler that took a slot and then panicked (an aborted streamed response
// panics with http.ErrAbortHandler) gives the slot back: otherwise a few
// aborted reads would leave the gate full and answer every later read 503.
func TestProjectionGateReleasesTheSlotWhenTheHandlerPanics(t *testing.T) {
	gate := newMetadataReadGate(1, 4, time.Second, 0)
	handler := gate.holdProjections(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !acquireProjection(r) {
			t.Error("the free slot was refused")
		}
		panic(http.ErrAbortHandler)
	}))
	for range 3 {
		func() {
			defer func() {
				if recovered := recover(); recovered != http.ErrAbortHandler {
					t.Fatalf("recovered %v", recovered)
				}
			}()
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/projects/x", nil))
		}()
		if len(gate.slots) != 0 {
			t.Fatalf("slot still held after the handler panicked: %d", len(gate.slots))
		}
	}
}

func TestProjectionGateReleasesColdWriteAfterAuthorization(t *testing.T) {
	gate := newMetadataReadGate(1, 4, time.Second, 0)
	request := httptest.NewRequest(http.MethodPatch, "/api/projects/project", nil)
	ctx, scope := store.WithColdScope(request.Context(), "PATCH /api/projects/{id}")
	defer scope.Close()
	hold := &projectionHold{gate: gate}
	request = request.WithContext(context.WithValue(ctx, projectionHoldKey{}, hold))
	gate.slots <- struct{}{}
	if !acquireProjection(request) || hold.held || len(gate.slots) != 1 {
		t.Fatal("COLD write waited on the shared authorization projection")
	}
	hold.mu.Lock()
	hold.held = true
	hold.mu.Unlock()
	releaseColdWriteProjection(request)
	if hold.held || len(gate.slots) != 0 {
		t.Fatalf("COLD write retained its authorization projection: held=%v slots=%d", hold.held, len(gate.slots))
	}
	if !acquireProjection(request) {
		t.Fatal("COLD write was not admitted after authorization")
	}
	releaseProjection(request)
	if hold.held || len(gate.slots) != 0 {
		t.Fatalf("COLD write changed projection ownership after authorization: held=%v slots=%d", hold.held, len(gate.slots))
	}
}

func TestProjectionGateRegisteredColdRouteUsesFacadeAdmission(t *testing.T) {
	gate := newMetadataReadGate(1, 0, time.Second, 0)
	gate.slots <- struct{}{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/project-statuses", func(http.ResponseWriter, *http.Request) {})
	request := httptest.NewRequest(http.MethodPost, "/api/project-statuses", nil)
	ctx, scope := store.WithColdScope(request.Context(), "POST /api/project-statuses")
	defer scope.Close()
	ctx = context.WithValue(ctx, coldAuthorizationMuxKey{}, mux)
	hold := &projectionHold{gate: gate}
	ctx = context.WithValue(ctx, projectionHoldKey{}, hold)
	request = request.WithContext(ctx)
	if !acquireProjection(request) {
		t.Fatal("registered COLD route waited on the projection gate")
	}
	if len(gate.slots) != 1 {
		t.Fatalf("registered COLD route changed the projection gate: %d", len(gate.slots))
	}

	unregistered := httptest.NewRequest(http.MethodPatch, "/api/projects/project", nil)
	unregisteredContext, unregisteredScope := store.WithColdScope(unregistered.Context(), "PATCH /api/projects/{id}")
	defer unregisteredScope.Close()
	unregisteredContext = context.WithValue(unregisteredContext, projectionHoldKey{}, &projectionHold{gate: gate})
	unregistered = unregistered.WithContext(unregisteredContext)
	if !acquireProjection(unregistered) {
		t.Fatal("unregistered COLD write waited on the projection gate")
	}
	if len(gate.slots) != 1 {
		t.Fatalf("unregistered COLD write changed the projection gate: %d", len(gate.slots))
	}
}

func TestProjectionGateKeepsHotAndReadProjections(t *testing.T) {
	gate := newMetadataReadGate(2, 4, time.Second, 0)
	for _, test := range []struct {
		name   string
		method string
		cold   bool
	}{
		{name: "hot-write", method: http.MethodPatch},
		{name: "cold-read", method: http.MethodGet, cold: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, "/api/projects/project", nil)
			if test.cold {
				ctx, scope := store.WithColdScope(request.Context(), "GET /api/projects/{id}")
				defer scope.Close()
				request = request.WithContext(ctx)
			}
			hold := &projectionHold{gate: gate}
			request = request.WithContext(context.WithValue(request.Context(), projectionHoldKey{}, hold))
			if !acquireProjection(request) {
				t.Fatal("projection was not admitted")
			}
			releaseColdWriteProjection(request)
			if !hold.held || len(gate.slots) == 0 {
				t.Fatalf("projection was released for %s", test.name)
			}
			releaseProjection(request)
		})
	}
}
