package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestAuditLogStreamDeliversSignedEntriesAndDisablesOnFailure(t *testing.T) {
	var mu sync.Mutex
	var received []map[string]any
	fail := false
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mac := hmac.New(sha256.New, []byte("audit-secret-0123456789"))
		mac.Write(body)
		if r.Header.Get("X-Flow-Signature") != hex.EncodeToString(mac.Sum(nil)) {
			http.Error(w, "bad signature", http.StatusUnauthorized)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if fail {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		received = append(received, payload)
	}))
	defer receiver.Close()

	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	srv := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true}
	handler := newHandler(srv)
	created := requestJSON[map[string]any](t, handler, http.MethodPost, "/api/workspace/audit-log-stream", map[string]any{"url": receiver.URL, "secret": "audit-secret-0123456789"}, http.StatusCreated)
	if created["secret"] != "audit-secret-0123456789" || created["auditLog"] != true {
		t.Fatalf("created stream = %#v", created)
	}
	requestJSON[any](t, handler, http.MethodPost, "/api/workspace/audit-log-stream", map[string]any{"url": receiver.URL}, http.StatusConflict)
	if webhooks := requestJSON[[]domain.Webhook](t, handler, http.MethodGet, "/api/webhooks", nil, http.StatusOK); len(webhooks) != 0 {
		t.Fatal("the audit stream must not appear among regular webhooks")
	}

	time.Sleep(time.Millisecond)
	requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops", map[string]any{"name": "Audited"}, http.StatusCreated)
	now := time.Now().UTC()
	srv.streamAuditLog(context.Background(), "test-workspace", now)
	mu.Lock()
	if len(received) != 1 || received[0]["type"] != "AuditEntry" || received[0]["data"].(map[string]any)["resourceType"] != "loop" {
		t.Fatalf("received = %#v", received)
	}
	mu.Unlock()
	srv.streamAuditLog(context.Background(), "test-workspace", now)
	if len(received) != 1 {
		t.Fatal("an entry was delivered twice")
	}

	mu.Lock()
	fail = true
	mu.Unlock()
	requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops", map[string]any{"name": "Undeliverable"}, http.StatusCreated)
	clock := time.Now().UTC()
	for attempt := 0; attempt < auditStreamMaxAttempts; attempt++ {
		clock = clock.Add(2 * time.Hour)
		srv.streamAuditLog(context.Background(), "test-workspace", clock)
	}
	status := requestJSON[auditStreamStatus](t, handler, http.MethodGet, "/api/workspace/audit-log-stream", nil, http.StatusOK)
	if len(status.Failures) != 1 || status.Webhook == nil || status.Webhook.FailingSince == nil || !status.Webhook.Enabled {
		t.Fatalf("after exhausted retries: %#v", status)
	}

	requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops", map[string]any{"name": "Still failing"}, http.StatusCreated)
	clock = clock.Add(auditStreamDisableAfter)
	for attempt := 0; attempt < auditStreamMaxAttempts; attempt++ {
		clock = clock.Add(2 * time.Hour)
		srv.streamAuditLog(context.Background(), "test-workspace", clock)
	}
	status = requestJSON[auditStreamStatus](t, handler, http.MethodGet, "/api/workspace/audit-log-stream", nil, http.StatusOK)
	if status.Webhook.Enabled || status.Webhook.DisabledReason != "deliveryFailures" {
		t.Fatalf("stream should be disabled after a day of failures: %#v", status.Webhook)
	}
	enabled := requestJSON[domain.Webhook](t, handler, http.MethodPatch, "/api/workspace/audit-log-stream", map[string]any{"enabled": true}, http.StatusOK)
	if !enabled.Enabled || enabled.FailingSince != nil {
		t.Fatalf("re-enabled stream = %#v", enabled)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/workspace/audit-log-stream", nil, http.StatusNoContent)
	if status := requestJSON[auditStreamStatus](t, handler, http.MethodGet, "/api/workspace/audit-log-stream", nil, http.StatusOK); status.Webhook != nil {
		t.Fatal("stream was not deleted")
	}
}
