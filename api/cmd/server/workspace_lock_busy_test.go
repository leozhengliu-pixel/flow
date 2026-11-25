package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"flow/api/internal/coordination"
)

// A busy distributed workspace lock is temporary unavailability (503 with
// Retry-After) on the shared mutation and internal-error paths, not a 500.
func TestBusyWorkspaceLockAnswers503WithRetryAfter(t *testing.T) {
	busy := fmt.Errorf("workspace %q is busy: %w", "acme", coordination.ErrWorkspaceLockBusy)
	paths := map[string]func(w http.ResponseWriter, r *http.Request){
		"respondMutation": func(w http.ResponseWriter, r *http.Request) { respondMutation(w, busy, http.StatusOK, nil) },
		"writeErrorFor":   func(w http.ResponseWriter, r *http.Request) { writeErrorFor(w, r, http.StatusBadRequest, busy) },
	}
	for name, handle := range paths {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			handle(w, httptest.NewRequest(http.MethodPatch, "/api/workspaces/acme/documents/d1", nil))
			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503: %s", w.Code, w.Body)
			}
			if w.Header().Get("Retry-After") == "" {
				t.Fatal("missing Retry-After")
			}
		})
	}
}
