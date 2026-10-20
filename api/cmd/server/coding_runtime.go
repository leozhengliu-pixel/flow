package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"flow/api/internal/codehost"
	"flow/api/internal/codehost/gitlab"
	"flow/api/internal/coding"
	"flow/api/internal/codingstore"
	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/llmproxy"
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
	// Track B: the session core, its metering and spend limits.
	service   *coding.Service
	meter     *coding.Meter
	guard     coding.SpendGuard
	preflight []sandbox.PreflightResult
	// coding sessions (track A): the sandbox reconciler (Wake after writing
	// desired state) and the orphan GC (AddSweeper for extra sweeps).
	sandboxReconciler *sandbox.Reconciler
	sandboxGC         *sandbox.GC
}

// codingStore is the one coding-store adapter (internal/codingstore) over the
// SQL store. Every coding hook that needs a store (session core, sandbox
// loops, environments, LLM proxy, artifacts, egress, GitLab sweeps) uses it,
// never a type assertion on s.store; coding_integration_contracts_test.go
// asserts it satisfies each of their interfaces.
func (s *server) codingStore() codingstore.Store { return codingstore.New(s.store) }

// initCoding builds the provider and code hosts. A provider that cannot be
// built disables coding sessions with a startup warning rather than stopping
// Flow, so a misconfigured cluster never takes the tracker down.
func (s *server) initCoding(config appconfig.CodingConfig) {
	services := &codingServices{config: config, sandbox: sandbox.Disabled{}, hosts: map[codehost.Kind]codehost.Host{}}
	s.coding = services
	if hosts, err := codehost.OpenAll(codehost.Dependencies{Config: config, HTTPClient: &http.Client{Timeout: 30 * time.Second}, Secrets: s.store, Installations: s.store /* coding sessions (track D) */, Now: time.Now}); err != nil {
		log.Printf("WARNING: coding sessions: code hosts: %v", err)
	} else {
		services.hosts = hosts
	}
	s.wireGitLabCodingHost() // coding sessions (track J), before the core reads the hosts
	s.initCodingCore(services)
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
	services.preflight = provider.Preflight(context.Background())
	for _, check := range services.preflight {
		if !check.OK || check.Warning {
			log.Printf("coding sessions preflight %s: ok=%t %s", check.Name, check.OK, check.Message)
		}
	}
}

// initCodingCore builds the session core (track B). Collaborators other
// tracks implement on the store are picked up when their methods exist.
func (s *server) initCodingCore(services *codingServices) {
	store := s.codingStore()
	metrics := coding.NewMetrics()
	workspaces := codingWorkspaces{s}
	guard := &coding.SpendLimiter{Usage: store, Workspaces: workspaces}
	options := coding.Options{
		Config: services.config, Store: store, Anchors: store, Workspaces: workspaces, Directory: codingDirectory{s},
		Hosts: services.hosts, Guard: guard, Metrics: metrics,
		ProviderReady: func() bool {
			return services.config.Enabled && services.sandbox.Name() != appconfig.CodingProviderDisabled
		},
	}
	// Collaborators of tracks K, D, I and E, all on the codingstore adapter
	// and assigned statically: a missing method is a compile error.
	options.Environments = store
	options.Snapshots = coding.StoreSnapshotResolver{Store: store, Image: services.config.Image}
	options.PullRequests = store
	options.Artifacts = store
	options.SigningKeys = store
	services.service = coding.NewService(options)
	// Track I: browser-testing guidance in the bootstrap (when browser
	// testing is on) and the screenshots section of the PR/MR body.
	services.service.BrowserGuidance = func() string { return coding.BrowserGuidance }
	services.service.DecoratePullRequestBody = func(ctx context.Context, session domain.CodingSession, body string) string {
		section, err := s.CodingArtifactMarkdown(ctx, session.WorkspaceKey, session.ID, "")
		if err != nil {
			log.Printf("coding sessions: screenshots for the PR body of %s: %v", session.ID, err)
			return body
		}
		return coding.MergeArtifactSection(body, section)
	}
	// Members' linked GitHub / GitLab accounts: push, commit and open the
	// PR/MR as the session's creator (code_host_accounts_coding.go).
	services.service.Members = codingMemberHosts{s: s}
	// Track J: GitLab guidance (glab usage; MR notes only when the sandbox
	// token can write them). Session credentials are revoked through
	// coding.SessionCredentialRevoker, which *gitlab.Host implements, and host
	// errors map through codehost.ErrorDescriber (gitlab.ErrorCode/ErrorMessage;
	// gitlab.ErrRejected falls back to pullRequestFailed).
	services.service.HostGuidance = func(session domain.CodingSession) string {
		if session.CodeHost != domain.CodeHostGitLab {
			return ""
		}
		host, ok := services.hosts[domain.CodeHostGitLab].(*gitlab.Host)
		if !ok {
			return ""
		}
		return host.SessionGuidance(session.HostName)
	}
	// Approved workspace MCP connectors behind Flow's MCP gateway
	// (coding_mcp_gateway.go), listed in the runner bootstrap.
	services.service.MCPServers = s.codingMCPServers
	// Track A: volumesnapshot restores (archive restores need no hook). The
	// strategy is resolved per call because the provider opens after this.
	services.service.RestoreSnapshot = func(ctx context.Context, storageRef, sandboxID, workspaceKey string) (sandbox.VolumeSource, error) {
		strategy := s.codingSnapshotStrategy()
		if strategy == nil {
			return sandbox.VolumeSource{}, errors.New("coding sessions: no snapshot strategy is configured")
		}
		return strategy.Restore(ctx, storageRef, sandboxID, workspaceKey)
	}
	// Track K: bootstrap of snapshot-build and debug-terminal sandboxes.
	services.service.SnapshotBootstrap = s.codingEnvironmentBootstrap
	// Track L: the "approval expired" notification. The session reconciler
	// owns the expiry transition (PhasePolicies); this posts L's notice once.
	services.service.ApprovalExpired = s.notifyCodingApprovalExpired
	// Track E: commit signing. *coding.CommitSigningError carries
	// missingCommitSigningKey, which fails provisioning without retry.
	services.service.ResolveCommitSigning = func(ctx context.Context, workspaceKey, userID string) (string, string, error) {
		resolution, err := s.resolveCodingCommitSigning(ctx, workspaceKey, userID)
		return resolution.Format, resolution.PublicKey, err
	}
	services.guard = guard
	services.meter = &coding.Meter{Store: store, Guard: guard, Config: services.config, Metrics: metrics, OnQuotaExceeded: func(ctx context.Context, workspaceKey, sessionID string, err error) {
		if _, failErr := services.service.FailSession(ctx, workspaceKey, sessionID, domain.CodingErrQuotaExceeded, err.Error()); failErr != nil {
			log.Printf("coding sessions: end session %s over its spend limit: %v", sessionID, failErr)
		}
	}}
}

// codingLeaseOwner names this process for coding leases.
func codingLeaseOwner() string {
	host, _ := os.Hostname()
	buffer := make([]byte, 4)
	_, _ = rand.Read(buffer)
	return "flow-" + host + "-" + hex.EncodeToString(buffer)
}

// codingEnabled reports whether the coding runtime is on for this server.
func (s *server) codingEnabled() bool { return s.coding != nil && s.coding.config.Enabled }

// runCoding starts the session reconciler, sandbox reconciler, snapshot
// reconciler, warm pool and orphan GC (tracks A, B, K) until ctx ends.
func (s *server) runCoding(ctx context.Context) {
	if !s.codingEnabled() {
		return
	}
	// Track B: the session reconciler.
	reconciler := &coding.Reconciler{Service: s.coding.service, Owner: codingLeaseOwner(), Meter: s.coding.meter, ProcessStart: time.Now()}
	go reconciler.Run(ctx)
	s.runCodingSandboxes(ctx)    // coding sessions (track A): sandbox reconciler, warm pool, orphan GC
	go s.runCodingSnapshots(ctx) // coding sessions (track K): snapshot reconciler
	go s.runGitLabCoding(ctx)    // coding sessions (track J): GitLab project token sweep
	// Approval timeouts are the session reconciler's (PhasePolicies,
	// approvalExpired); Service.ApprovalExpired posts track L's notification.
	<-ctx.Done()
}

// registerCodingRoutes registers every coding-session route.
func (s *server) registerCodingRoutes(mux *http.ServeMux) {
	s.registerCodingSessionRoutes(mux)
	s.registerCodingRuntimeRoutes(mux)
	s.registerCodingGitRelayRoutes(mux)
	s.registerCodingMCPGatewayRoutes(mux)
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

// llmSpendChecker is the spend guard the LLM proxy checks per request
// (llmproxy.GuardedMeter caches decisions for 30s). Over-limit errors are
// *coding.QuotaError, which match llmproxy.ErrSpendLimit and
// coding.ErrQuotaExceeded and report CodingErrorCode() == quotaExceeded.
func (c *codingServices) llmSpendChecker() llmproxy.SpendChecker {
	if c == nil || c.guard == nil {
		return nil
	}
	return c.guard
}

// codingRuntimePath reports whether a path is runner-facing (runtime API or LLM proxy).
func codingRuntimePath(path string) bool {
	return strings.HasPrefix(path, "/internal/coding-runtime/") || strings.HasPrefix(path, "/internal/llm-proxy/")
}

// codingRuntimeOnly serves only the runner-facing routes (the
// FLOW_CODING_RUNTIME_ADDR listener, which NetworkPolicy exposes to sandboxes).
func codingRuntimeOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !codingRuntimePath(r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// codingMainHandler wraps the main listener's handler: with a dedicated
// runtime listener configured, runner-facing routes are only served there.
func (s *server) codingMainHandler(next http.Handler) http.Handler {
	if s.coding == nil || strings.TrimSpace(s.coding.config.RuntimeAddr) == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if codingRuntimePath(r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// serveCodingRuntime starts the FLOW_CODING_RUNTIME_ADDR listener (when set)
// with the runner-facing routes of handler, until ctx ends. It returns the
// bound address ("" when no listener is configured).
func (s *server) serveCodingRuntime(ctx context.Context, handler http.Handler) (string, error) {
	if s.coding == nil || strings.TrimSpace(s.coding.config.RuntimeAddr) == "" {
		return "", nil
	}
	listener, err := net.Listen("tcp", s.coding.config.RuntimeAddr)
	if err != nil {
		return "", err
	}
	server := &http.Server{Handler: codingRuntimeOnly(handler), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("coding runtime listener: %v", err)
		}
	}()
	log.Printf("Flow coding runtime listening on %s", listener.Addr())
	return listener.Addr().String(), nil
}
