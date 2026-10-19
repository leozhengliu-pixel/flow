package main

import (
	"context"
	"reflect"
	"testing"
	"time"

	"flow/api/internal/codehost"
	githubhost "flow/api/internal/codehost/github"
	"flow/api/internal/codehost/gitlab"
	"flow/api/internal/coding"
	"flow/api/internal/codingstore"
	"flow/api/internal/domain"
	"flow/api/internal/objectstore"
	"flow/api/internal/sandbox"
)

// Integration contracts between the coding-session tracks.
//
// Several tracks reach other tracks' code through interfaces that are
// resolved at runtime (a type assertion on the store or a host, a hook
// field, a package variable). A missing method there does not fail the
// build: the hook silently falls back (503, "not available", no-op). This
// file pins every one of them, at compile time where the value's type is
// known and at test time for hooks that are wired in initCoding.

// Store views, compile time: the codingstore adapter (server.codingStore())
// must satisfy every interface a track asserts on it.
var (
	// Session core (track B) and its collaborators.
	_ coding.CoreStore           = codingstore.Store{}
	_ coding.AnchorStore         = codingstore.Store{}
	_ coding.EnvironmentReader   = codingstore.Store{}
	_ coding.PullRequestStore    = codingstore.Store{}
	_ coding.ArtifactReader      = codingstore.Store{}
	_ coding.SigningKeys         = codingstore.Store{}
	_ coding.SpendReader         = codingstore.Store{}
	_ coding.SnapshotLookupStore = codingstore.Store{}
	// Sandbox reconciler, warm pool, GC (track A).
	_ sandbox.ReconcilerStore  = codingstore.Store{}
	_ sandbox.MaintenanceStore = codingstore.Store{}
	_ sandbox.TokenRevoker     = codingstore.Store{}
	_ sandbox.UsageRecorder    = codingstore.Store{}
	// Environments (track K): RecordLauncher and the runtime routes.
	_ coding.SandboxRecordStore      = codingstore.Store{}
	_ codingEnvironmentAuthenticator = codingstore.Store{}
	_ codingEnvironmentSessionLookup = codingstore.Store{}
	// LLM proxy and signing (track E).
	_ codingSandboxAuthStore   = codingstore.Store{}
	_ codingSandboxListStore   = codingstore.Store{}
	_ codingSessionUpdateStore = codingstore.Store{}
	_ coding.SigningKeyStore   = codingstore.Store{}
	// Artifacts (track I).
	_ codingSandboxTokenAuth  = codingstore.Store{}
	_ codingSessionReader     = codingstore.Store{}
	_ codingPullRequestReader = codingstore.Store{}
	// GitLab token sweep (track J).
	_ codingEnvironmentLister = codingstore.Store{}
	// Egress checks (track H).
	_ codingEgressStore = codingstore.Store{}
	// Triggers and webhook dispatch (track L).
	_ coding.StarterSessions          = codingstore.Store{}
	_ coding.TriggerSessions          = codingstore.Store{}
	_ coding.TriggerEnvironments      = codingstore.Store{}
	_ coding.WebhookSessions          = codingstore.Store{}
	_ coding.StarterPullRequests      = codingstore.Store{}
	_ codingWorkspacesForInstallation = codingstore.Store{}
	_ coding.SessionStarter           = coding.ServiceStarter{}
	_ coding.ArchiveStore             = (*sandbox.ArchiveStrategy)(nil)
	_ coding.ChangeRequestBodyHost    = (*githubhost.Host)(nil)
	_ coding.ChangeRequestBodyHost    = (*gitlab.Host)(nil)
	_ coding.SessionCredentialRevoker = (*gitlab.Host)(nil)
	_ codehost.ErrorDescriber         = (*gitlab.Host)(nil)
	_ codingCredentialModeChecker     = (*gitlab.Host)(nil)
	_ codehost.Host                   = (*githubhost.Host)(nil)
	_ codehost.Host                   = (*gitlab.Host)(nil)
)

// The anonymous interfaces track K asserts in coding_environments_api.go.
type codingEnvironmentAuthenticator interface {
	AuthenticateSandboxToken(ctx context.Context, token string) (coding.SandboxGrant, error)
}

type codingEnvironmentSessionLookup interface {
	CodingSession(ctx context.Context, workspaceKey, id string) (domain.CodingSession, error)
}

// TestCodingRuntimeHooksAreWired checks, on a server built the production way
// (initCoding with the fake provider), that every hook another track left for
// integration is set, and that runtime type assertions on the store and the
// hosts succeed.
func TestCodingRuntimeHooksAreWired(t *testing.T) {
	s, _ := codingTestServer(t, "")
	if !s.codingEnabled() {
		t.Fatal("coding must be enabled with the fake provider")
	}
	service := s.coding.service
	hooks := map[string]any{
		"SnapshotBootstrap (K)":       service.SnapshotBootstrap,
		"BrowserGuidance (I)":         service.BrowserGuidance,
		"HostGuidance (J)":            service.HostGuidance,
		"DecoratePullRequestBody (I)": service.DecoratePullRequestBody,
		"ResolveCommitSigning (E)":    service.ResolveCommitSigning,
		"RestoreSnapshot (A)":         service.RestoreSnapshot,
	}
	for name, hook := range hooks {
		if reflect.ValueOf(hook).IsNil() {
			t.Errorf("Service.%s is not wired", name)
		}
	}
	if s.coding.meter == nil || s.coding.guard == nil {
		t.Error("the Meter and the spend guard are not built")
	}
	if s.coding.llmSpendChecker() == nil {
		t.Error("the LLM proxy has no spend checker (track E)")
	}
	if codingWebhookDispatch == nil {
		t.Error("codingWebhookDispatch (track D's GitHub App hook) is not assigned by track L")
	}

	// Hosts: the concrete types other code asserts on.
	if _, ok := s.coding.hosts[domain.CodeHostGitLab].(*gitlab.Host); !ok {
		t.Errorf("GitLab host is %T", s.coding.hosts[domain.CodeHostGitLab])
	}
	if _, ok := s.coding.hosts[domain.CodeHostGitHub].(*githubhost.Host); !ok {
		t.Errorf("GitHub host is %T", s.coding.hosts[domain.CodeHostGitHub])
	}
	if _, ok := s.coding.hosts[domain.CodeHostGitLab].(coding.SessionCredentialRevoker); !ok {
		t.Error("the GitLab host does not revoke session credentials")
	}

	// Triggers (track L): every store view resolves and the session core is used.
	stores, ok := s.codingTriggerStores()
	if !ok || stores.webhooks == nil || stores.pullRequests == nil || stores.spend == nil {
		t.Fatalf("trigger store views: ok=%v %+v", ok, stores)
	}
	workspace := s.store.WorkspaceKeys()[0]
	triggers, ok := s.codingTriggers(workspace)
	if !ok {
		t.Fatal("triggers are not available")
	}
	if _, ok := triggers.Starter.(coding.ServiceStarter); !ok {
		t.Errorf("triggers start sessions with %T, not the session core", triggers.Starter)
	}
	if triggers.Approver == nil {
		t.Error("triggers do not approve through the session state machine")
	}
	if _, ok := s.codingWebhookDispatcher(); !ok {
		t.Error("the webhook dispatcher is not available")
	}
	if _, ok := codingTriggerStore(s).(codingWorkspacesForInstallation); !ok {
		t.Error("GitHub App installations cannot be mapped to workspaces")
	}

	// Environments (track K): rows the sandbox reconciler owns, track A's strategy.
	if _, ok := s.codingEnvironmentLauncher().(coding.RecordLauncher); !ok {
		t.Errorf("environment sandboxes use %T, not coding_sandboxes rows", s.codingEnvironmentLauncher())
	}
	objects, err := objectstore.Open(context.Background(), objectstore.Config{Driver: "local", LocalPath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	s.objectStore = objects
	if _, ok := s.codingSnapshotStrategy().(*sandbox.ArchiveStrategy); !ok {
		t.Errorf("snapshot strategy is %T, not track A's archive strategy", s.codingSnapshotStrategy())
	}
}

// TestCodingSandboxLoopsShareTheAdapter starts the sandbox loops and checks
// that the reconciler bills through the session Meter and that the artifact
// sweeper is registered with the GC.
func TestCodingSandboxLoopsShareTheAdapter(t *testing.T) {
	s, _ := codingTestServer(t, "")
	ctx, cancel := context.WithCancel(context.Background())
	s.runCodingSandboxes(ctx)
	cancel()
	time.Sleep(10 * time.Millisecond)
	if s.coding.sandboxReconciler == nil || s.coding.sandboxGC == nil {
		t.Fatal("sandbox loops did not start")
	}
	if _, ok := s.coding.sandboxReconciler.Usage.(codingMeterUsage); !ok {
		t.Errorf("runtime usage goes to %T, not the session Meter", s.coding.sandboxReconciler.Usage)
	}
	if _, ok := s.coding.sandboxReconciler.Store.(codingstore.Store); !ok {
		t.Errorf("the sandbox reconciler uses %T", s.coding.sandboxReconciler.Store)
	}
}
