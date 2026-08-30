package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthcheckCommandStatus(t *testing.T) {
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer healthy.Close()
	t.Setenv("FLOW_HEALTHCHECK_URL", healthy.URL)
	if code := runHealthcheck(); code != 0 {
		t.Fatalf("healthy status code=%d", code)
	}
	unhealthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer unhealthy.Close()
	t.Setenv("FLOW_HEALTHCHECK_URL", unhealthy.URL)
	if code := runHealthcheck(); code == 0 {
		t.Fatal("unhealthy endpoint passed")
	}
}
