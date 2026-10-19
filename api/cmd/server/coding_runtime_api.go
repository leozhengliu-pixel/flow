package main

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/coding"
)

// Runner-facing API (/internal/coding-runtime/*), authenticated with the
// sandbox token (Authorization: Bearer fcs_…). Wire types:
// api/internal/coding/protocol.go. These paths skip browser authentication
// (publicAuthPath); every handler checks the token, and the session core
// checks that the token's sandbox is the session's current sandbox.
//
// Owner: track B (session core). Keep the route list in sync with publicapi.InternalRoutes.
func (s *server) registerCodingRuntimeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /internal/coding-runtime/bootstrap", s.codingRuntimeBootstrap)
	mux.HandleFunc("GET /internal/coding-runtime/setup-secrets", s.codingRuntimeSetupSecrets)
	mux.HandleFunc("POST /internal/coding-runtime/heartbeat", s.codingRuntimeHeartbeat)
	mux.HandleFunc("POST /internal/coding-runtime/events", s.codingRuntimeEvents)
	mux.HandleFunc("GET /internal/coding-runtime/inbox", s.codingRuntimeInbox)
	mux.HandleFunc("POST /internal/coding-runtime/turns/{turnId}/complete", s.codingRuntimeCompleteTurn)
	mux.HandleFunc("POST /internal/coding-runtime/credentials", s.codingRuntimeCredentials)
	mux.HandleFunc("GET /internal/coding-runtime/harness-state", s.codingRuntimeGetHarnessState)
	mux.HandleFunc("PUT /internal/coding-runtime/harness-state", s.codingRuntimePutHarnessState)
}

// harnessStateMaxBytes caps saved harness state (tar.zst).
const harnessStateMaxBytes = 64 << 20

// codingRuntimeToken returns the bearer sandbox token, or answers 401/404.
func (s *server) codingRuntimeToken(w http.ResponseWriter, r *http.Request) (string, bool) {
	if !s.codingEnabled() || s.coding.service == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Coding sessions are disabled", "code": "sandboxDisabled"})
		return "", false
	}
	header := r.Header.Get("Authorization")
	token, found := strings.CutPrefix(header, "Bearer ")
	token = strings.TrimSpace(token)
	if !found || !strings.HasPrefix(token, coding.SandboxTokenPrefix) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "A sandbox token is required", "code": "tokenInvalid"})
		return "", false
	}
	return token, true
}

func waitParam(r *http.Request, fallback, limit time.Duration) time.Duration {
	raw := r.URL.Query().Get("wait")
	if raw == "" {
		return fallback
	}
	wait, err := time.ParseDuration(raw)
	if err != nil {
		if seconds, convErr := strconv.Atoi(raw); convErr == nil {
			wait = time.Duration(seconds) * time.Second
		} else {
			return fallback
		}
	}
	return max(0, min(wait, limit))
}

func (s *server) codingRuntimeBootstrap(w http.ResponseWriter, r *http.Request) {
	token, ok := s.codingRuntimeToken(w, r)
	if !ok {
		return
	}
	response, err := s.coding.service.Bootstrap(r.Context(), token, waitParam(r, 0, 60*time.Second))
	if errors.Is(err, coding.ErrNotBound) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		writeCodingError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *server) codingRuntimeSetupSecrets(w http.ResponseWriter, r *http.Request) {
	token, ok := s.codingRuntimeToken(w, r)
	if !ok {
		return
	}
	response, err := s.coding.service.SetupSecrets(r.Context(), token)
	if err != nil {
		writeCodingError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, response)
}

func (s *server) codingRuntimeHeartbeat(w http.ResponseWriter, r *http.Request) {
	token, ok := s.codingRuntimeToken(w, r)
	if !ok {
		return
	}
	var request coding.HeartbeatRequest
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if !decodeOptionalCodingJSON(w, r, &request) {
		return
	}
	response, err := s.coding.service.Heartbeat(r.Context(), token, request)
	if err != nil {
		writeCodingError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *server) codingRuntimeEvents(w http.ResponseWriter, r *http.Request) {
	token, ok := s.codingRuntimeToken(w, r)
	if !ok {
		return
	}
	var request coding.EventsRequest
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	if !decodeJSON(w, r, &request) {
		return
	}
	result, err := s.coding.service.Events(r.Context(), token, request)
	if err != nil {
		writeCodingError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) codingRuntimeInbox(w http.ResponseWriter, r *http.Request) {
	token, ok := s.codingRuntimeToken(w, r)
	if !ok {
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	response, err := s.coding.service.Inbox(r.Context(), token, max(0, after), waitParam(r, 25*time.Second, 30*time.Second))
	if err != nil {
		writeCodingError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *server) codingRuntimeCompleteTurn(w http.ResponseWriter, r *http.Request) {
	token, ok := s.codingRuntimeToken(w, r)
	if !ok {
		return
	}
	var request coding.TurnCompleteRequest
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	if !decodeJSON(w, r, &request) {
		return
	}
	session, err := s.coding.service.CompleteTurn(r.Context(), token, r.PathValue("turnId"), request)
	if err != nil {
		writeCodingError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessionId": session.ID, "phase": session.Phase, "state": session.State})
}

func (s *server) codingRuntimeCredentials(w http.ResponseWriter, r *http.Request) {
	token, ok := s.codingRuntimeToken(w, r)
	if !ok {
		return
	}
	var request coding.CredentialRequest
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if !decodeJSON(w, r, &request) {
		return
	}
	response, err := s.coding.service.Credentials(r.Context(), token, request)
	if err != nil {
		writeCodingError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, response)
}

func (s *server) codingRuntimeGetHarnessState(w http.ResponseWriter, r *http.Request) {
	token, ok := s.codingRuntimeToken(w, r)
	if !ok {
		return
	}
	key, err := s.coding.service.HarnessStateKey(r.Context(), token)
	if err != nil {
		writeCodingError(w, err)
		return
	}
	if s.objectStore == nil {
		writeError(w, http.StatusServiceUnavailable, "Object storage is not configured")
		return
	}
	reader, _, size, err := s.objectStore.Open(r.Context(), key)
	if err != nil {
		w.WriteHeader(http.StatusNoContent) // nothing saved yet
		return
	}
	defer reader.Close()
	w.Header().Set("Content-Type", "application/zstd")
	if size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	_, _ = io.Copy(w, reader)
}

func (s *server) codingRuntimePutHarnessState(w http.ResponseWriter, r *http.Request) {
	token, ok := s.codingRuntimeToken(w, r)
	if !ok {
		return
	}
	key, err := s.coding.service.HarnessStateKey(r.Context(), token)
	if err != nil {
		writeCodingError(w, err)
		return
	}
	if s.objectStore == nil {
		writeError(w, http.StatusServiceUnavailable, "Object storage is not configured")
		return
	}
	if r.ContentLength > harnessStateMaxBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "Harness state is limited to 64 MiB")
		return
	}
	body := http.MaxBytesReader(w, r.Body, harnessStateMaxBytes)
	written, err := s.objectStore.Put(r.Context(), key, body, "application/zstd")
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			_ = s.objectStore.Delete(r.Context(), key)
			writeError(w, http.StatusRequestEntityTooLarge, "Harness state is limited to 64 MiB")
			return
		}
		writeCodingError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"sizeBytes": written})
}
