package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"flow/api/internal/codehost"
	appconfig "flow/api/internal/config"
	"flow/api/internal/sandbox"
)

// Coding sessions wiring (docs/coding-sessions-design.md). This file is the
// single registration point: main.go calls initCoding, runCoding and
// registerCodingRoutes once each, and every track adds its code in its own
// files (see the ownership table in the design). Owner: track B.

// codingServices holds the coding-session runtime. It is never nil after
// initCoding; with coding disabled every route answers 404/501 and the
// reconcilers do not start.
type codingServices struct {
	config  appconfig.CodingConfig
	sandbox sandbox.Provider
	hosts   map[codehost.Kind]codehost.Host
}

// initCoding builds the provider and code hosts. A provider that cannot be
// built disables coding sessions with a startup warning rather than stopping
// Flow, so a misconfigured cluster never takes the tracker down.
func (s *server) initCoding(config appconfig.CodingConfig) {
	services := &codingServices{config: config, sandbox: sandbox.Disabled{}, hosts: map[codehost.Kind]codehost.Host{}}
	s.coding = services
	if hosts, err := codehost.OpenAll(codehost.Dependencies{Config: config, HTTPClient: &http.Client{Timeout: 30 * time.Second}, Secrets: s.store, Now: time.Now}); err != nil {
		log.Printf("WARNING: coding sessions: code hosts: %v", err)
	} else {
		services.hosts = hosts
	}
	if !config.Enabled {
		return
	}
	provider, err := sandbox.Open(sandbox.Dependencies{Config: config, Now: time.Now})
	if err != nil {
		log.Printf("WARNING: coding sessions are disabled: %v", err)
		services.config.Enabled = false
		return
	}
	services.sandbox = provider
	for _, check := range provider.Preflight(context.Background()) {
		if !check.OK || check.Warning {
			log.Printf("coding sessions preflight %s: ok=%t %s", check.Name, check.OK, check.Message)
		}
	}
}

// codingEnabled reports whether the coding runtime is on for this server.
func (s *server) codingEnabled() bool { return s.coding != nil && s.coding.config.Enabled }

// runCoding starts the session reconciler, sandbox reconciler, snapshot
// reconciler, warm pool and orphan GC (tracks A, B, K) until ctx ends.
func (s *server) runCoding(ctx context.Context) {
	if !s.codingEnabled() {
		return
	}
	// TODO(track B): session reconciler; TODO(track A): sandbox reconciler,
	// warm pool and orphan GC; TODO(track K): snapshot reconciler.
	<-ctx.Done()
}

// registerCodingRoutes registers every coding-session route.
func (s *server) registerCodingRoutes(mux *http.ServeMux) {
	s.registerCodingSessionRoutes(mux)
	s.registerCodingRuntimeRoutes(mux)
	s.registerCodingEnvironmentRoutes(mux)
	s.registerCodingLLMProxyRoutes(mux)
	s.registerGitHubAppRoutes(mux)
	s.registerCodingTriggerRoutes(mux)
	s.registerCodingArtifactRoutes(mux)
	s.registerCodingEgressRoutes(mux)
}

// codingNotImplemented answers a route whose track has not landed yet.
func codingNotImplemented(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotImplemented, "Coding sessions: not implemented yet")
}
