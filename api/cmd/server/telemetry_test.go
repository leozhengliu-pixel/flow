package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	appconfig "flow/api/internal/config"
)

func TestTelemetryDisabledIsTransparent(t *testing.T) {
	shutdown, err := configureTelemetry(context.Background(), appconfig.TelemetryConfig{Enabled: false})
	if err != nil || shutdown == nil {
		t.Fatalf("telemetry shutdown callback missing, err=%v", err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	called := false
	handler := telemetryHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { called = true; w.WriteHeader(http.StatusNoContent) }), false)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if !called || response.Code != http.StatusNoContent {
		t.Fatalf("called=%v status=%d", called, response.Code)
	}
}
