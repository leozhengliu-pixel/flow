package opensandbox

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/config"
	"flow/api/internal/sandbox"
)

const testKey = "test-api-key"

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type harness struct {
	t        *testing.T
	server   *fakeServer
	provider *Provider
	clock    *testClock
	coding   config.CodingConfig
	sleeps   []time.Duration
	mu       sync.Mutex
}

func testConfig(url string) config.CodingConfig {
	return config.CodingConfig{
		Enabled: true, Provider: config.CodingProviderOpenSandbox, InstanceID: "flow-test",
		RuntimeURL: "http://flow.flow.svc.cluster.local:8090", Image: "ghcr.io/example/flow-sandbox:test",
		Sizes: map[string]config.SandboxSize{
			"small":  {CPU: "1", Memory: "8Gi", EphemeralStorage: "20Gi", PIDs: 4096},
			"medium": {CPU: "2", Memory: "16Gi", EphemeralStorage: "40Gi", PIDs: 8192},
			"large":  {CPU: "4", Memory: "32Gi", EphemeralStorage: "80Gi", PIDs: 16384},
		},
		CPURequestRatio: 1,
		Timeouts:        config.CodingTimeouts{Start: 10 * time.Minute, StopGrace: time.Minute, MaxSession: 4 * time.Hour, DebugTerminal: 20 * time.Minute, TokenGrace: 5 * time.Minute},
		Retries:         config.CodingRetries{Start: 2},
		Snapshot:        config.CodingSnapshotConfig{Strategy: sandbox.StrategyArchive, Timeout: 30 * time.Minute},
		Egress:          config.CodingEgressConfig{ProxyURL: "http://flow-egress.flow.svc.cluster.local:3128"},
		OpenSandbox: config.OpenSandboxConfig{
			URL: url, APIKey: testKey, RequestTimeout: 2 * time.Second, CreateTimeout: 5 * time.Second, Retries: 2,
			ExpiryWindow: time.Hour, ExecdAccess: config.OpenSandboxExecdProxy, RunAsUID: 1000, TokenPath: "/tmp/flow/token",
			EgressPolicy: config.OpenSandboxEgressOff,
		},
	}
}

func newHarness(t *testing.T, mutate func(*config.CodingConfig)) *harness {
	t.Helper()
	clock := &testClock{now: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)}
	server := newFakeServer(testKey, clock.Now)
	t.Cleanup(server.Close)
	coding := testConfig(server.URL())
	if mutate != nil {
		mutate(&coding)
	}
	h := &harness{t: t, server: server, clock: clock, coding: coding}
	client, err := NewClient(coding.OpenSandbox)
	if err != nil {
		t.Fatal(err)
	}
	client.sleep = func(ctx context.Context, d time.Duration) error {
		h.mu.Lock()
		h.sleeps = append(h.sleeps, d)
		h.mu.Unlock()
		return ctx.Err()
	}
	provider, err := NewWithClient(sandbox.Dependencies{Config: coding, Now: clock.Now}, client)
	if err != nil {
		t.Fatal(err)
	}
	provider.readyPoll = time.Millisecond
	h.provider = provider
	return h
}

func (h *harness) spec(id string) sandbox.SandboxSpec {
	return sandbox.SandboxSpec{ID: id, Kind: sandbox.KindSession, WorkspaceKey: "ws-1", SessionID: "sess" + id, Resources: sandbox.Resources{Size: "small"}, Token: "fcs_token_" + id, Env: map[string]string{"FOO": "bar"}, ProxyURL: h.coding.Egress.ProxyURL}
}

// start starts a sandbox and waits for the background create.
func (h *harness) start(spec sandbox.SandboxSpec) sandbox.SandboxStatus {
	h.t.Helper()
	status, err := h.provider.StartSandbox(context.Background(), spec)
	if err != nil {
		h.t.Fatalf("start: %v", err)
	}
	if status.Phase != sandbox.PhasePending {
		h.t.Fatalf("a new sandbox must be pending, got %s", status.Phase)
	}
	h.provider.Wait()
	return status
}

func (h *harness) status(id string) sandbox.SandboxStatus {
	h.t.Helper()
	status, err := h.provider.Status(context.Background(), id)
	if err != nil {
		h.t.Fatalf("status: %v", err)
	}
	return status
}

func (h *harness) only() *fakeSandbox {
	h.t.Helper()
	all := h.server.list()
	if len(all) != 1 {
		h.t.Fatalf("expected one OpenSandbox sandbox, got %d", len(all))
	}
	return all[0]
}

func TestBuildCreate(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*config.CodingConfig)
		spec   func(sandbox.SandboxSpec) sandbox.SandboxSpec
		check  func(*testing.T, CreateRequest)
	}{
		{name: "defaults", check: func(t *testing.T, req CreateRequest) {
			if req.Image == nil || req.Image.URI != "ghcr.io/example/flow-sandbox:test" || req.Image.Auth != nil {
				t.Fatalf("image: %+v", req.Image)
			}
			if strings.Join(req.Entrypoint, " ") != "tail -f /dev/null" {
				t.Fatalf("entrypoint: %v", req.Entrypoint)
			}
			if req.ResourceLimits["cpu"] != "1" || req.ResourceLimits["memory"] != "8Gi" || len(req.ResourceLimits) != 2 || req.ResourceRequests != nil {
				t.Fatalf("resources: %v %v", req.ResourceLimits, req.ResourceRequests)
			}
			if *req.Timeout != 3600 {
				t.Fatalf("timeout must be the expiry window, got %d", *req.Timeout)
			}
			for key, want := range map[string]string{
				sandbox.LabelManagedBy: "flow", sandbox.LabelInstance: "flow-test", sandbox.LabelSandboxID: "sb1",
				sandbox.LabelKind: "session", sandbox.LabelSessionID: "sesssb1", sandbox.LabelSize: "small",
			} {
				if req.Metadata[key] != want {
					t.Fatalf("metadata %s = %q, want %q (%v)", key, req.Metadata[key], want, req.Metadata)
				}
			}
			// Deadline: 4h session + 1m grace + 30s wrap-up + 10m margin.
			want := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC).Add(4*time.Hour + 90*time.Second + 10*time.Minute).Unix()
			if req.Metadata[MetaDeadline] != strconv.FormatInt(want, 10) {
				t.Fatalf("deadline: %q, want %d", req.Metadata[MetaDeadline], want)
			}
			if req.Env[sandbox.EnvTokenFile] != "/tmp/flow/token" || req.Env["FOO"] != "bar" || req.Env[sandbox.EnvSandboxID] != "sb1" || req.Env["HTTPS_PROXY"] == "" {
				t.Fatalf("env: %v", req.Env)
			}
			for key := range req.Env {
				if strings.Contains(strings.ToLower(req.Env[key]), "fcs_token") {
					t.Fatalf("the sandbox token must never be an env var (%s)", key)
				}
			}
			if req.NetworkPolicy != nil || req.Extensions != nil {
				t.Fatalf("no policy or extensions by default: %+v %v", req.NetworkPolicy, req.Extensions)
			}
		}},
		{name: "request ratio, ephemeral storage, image auth, extra metadata", mutate: func(c *config.CodingConfig) {
			c.CPURequestRatio = 0.5
			c.OpenSandbox.EphemeralStorage = true
			c.OpenSandbox.ImageUsername, c.OpenSandbox.ImagePassword = "robot", "pull-secret"
			c.OpenSandbox.ExtraMetadata = map[string]string{"team": "platform", "owner": "y"}
			c.OpenSandbox.Extensions = map[string]string{"access.renew.extend.seconds": "600"}
		}, check: func(t *testing.T, req CreateRequest) {
			if req.ResourceRequests["cpu"] != "500m" || req.ResourceRequests["memory"] != "8Gi" || req.ResourceLimits["ephemeral-storage"] != "20Gi" {
				t.Fatalf("resources: %v %v", req.ResourceLimits, req.ResourceRequests)
			}
			if req.Image.Auth == nil || req.Image.Auth.Username != "robot" {
				t.Fatalf("image auth: %+v", req.Image)
			}
			if req.Metadata["team"] != "platform" || req.Metadata[sandbox.LabelInstance] != "flow-test" || req.Extensions["access.renew.extend.seconds"] != "600" {
				t.Fatalf("metadata/extensions: %v %v", req.Metadata, req.Extensions)
			}
		}},
		{name: "size override", mutate: func(c *config.CodingConfig) {
			c.OpenSandbox.SizeResources = map[string]map[string]string{"small": {"cpu": "750m", "memory": "3Gi", "gpu": "1"}}
		}, check: func(t *testing.T, req CreateRequest) {
			if req.ResourceLimits["cpu"] != "750m" || req.ResourceLimits["gpu"] != "1" || req.ResourceRequests != nil {
				t.Fatalf("resources: %v", req.ResourceLimits)
			}
		}},
		{name: "pool", mutate: func(c *config.CodingConfig) { c.OpenSandbox.Pools = map[string]string{"small": "flow-small"} },
			check: func(t *testing.T, req CreateRequest) {
				if req.Extensions["poolRef"] != "flow-small" || req.Image != nil || req.ResourceLimits != nil || req.NetworkPolicy != nil {
					t.Fatalf("pool request: %+v", req)
				}
			}},
		{name: "egress policy", mutate: func(c *config.CodingConfig) {
			c.OpenSandbox.EgressPolicy = config.OpenSandboxEgressProxy
			c.OpenSandbox.EgressAllow = []string{"pypi.org", "10.0.0.1"}
		}, check: func(t *testing.T, req CreateRequest) {
			if req.NetworkPolicy == nil || req.NetworkPolicy.DefaultAction != "deny" {
				t.Fatalf("policy: %+v", req.NetworkPolicy)
			}
			targets := []string{}
			for _, rule := range req.NetworkPolicy.Egress {
				targets = append(targets, rule.Action+":"+rule.Target)
			}
			if got := strings.Join(targets, ","); got != "allow:flow.flow.svc.cluster.local,allow:flow-egress.flow.svc.cluster.local,allow:pypi.org" {
				t.Fatalf("rules: %s", got)
			}
		}},
		{name: "short deadline caps the timeout", spec: func(s sandbox.SandboxSpec) sandbox.SandboxSpec {
			s.Kind, s.Deadline = sandbox.KindDebug, time.Minute
			return s
		}, check: func(t *testing.T, req CreateRequest) {
			// 1m deadline + 1m30s grace + 10m margin.
			if *req.Timeout != 750 {
				t.Fatalf("timeout: %d", *req.Timeout)
			}
		}},
		{name: "archive restore", spec: func(s sandbox.SandboxSpec) sandbox.SandboxSpec {
			s.Workspace.ArchiveURL = "http://flow/internal/coding-runtime/snapshots/s1/archive"
			return s
		}, check: func(t *testing.T, req CreateRequest) {
			if req.Metadata[MetaRestore] != "1" || req.Env[sandbox.EnvArchiveURL] == "" {
				t.Fatalf("restore: %v", req.Metadata)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, tc.mutate)
			spec := h.spec("sb1")
			if tc.spec != nil {
				spec = tc.spec(spec)
			}
			req, err := h.provider.BuildCreate(spec)
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, req)
		})
	}
}

func TestStartCreatesSandboxAndLaunchesRunner(t *testing.T) {
	h := newHarness(t, nil)
	h.start(h.spec("sb1"))
	sb := h.only()
	if string(sb.Files["/tmp/flow/token"]) != "fcs_token_sb1" || sb.Modes["/tmp/flow/token"] != 600 {
		t.Fatalf("token not uploaded: %q mode %d", sb.Files["/tmp/flow/token"], sb.Modes["/tmp/flow/token"])
	}
	runner := h.server.runner(sb.ID)
	if runner == nil || !runner.Request.Background || runner.Request.UID == nil || *runner.Request.UID != 1000 || *runner.Request.GID != 1000 {
		t.Fatalf("runner: %+v", runner)
	}
	if runner.Request.Argv[0] != "/bin/sh" || runner.Request.Argv[3] != "session" || runner.Request.Cwd != sandbox.WorkspacePath || runner.Request.Envs[sandbox.EnvTokenFile] != "/tmp/flow/token" {
		t.Fatalf("runner command: %+v", runner.Request)
	}
	if sb.Metadata[MetaRunner] != runner.ID {
		t.Fatalf("runner id not recorded: %v", sb.Metadata)
	}
	status := h.status("sb1")
	if status.Phase != sandbox.PhaseRunning || status.ProviderRef != sb.ID || status.StartedAt == nil || status.Kind != sandbox.KindSession {
		t.Fatalf("status: %+v", status)
	}
	// Every lifecycle request carried the API key.
	h.server.mu.Lock()
	defer h.server.mu.Unlock()
	for i, header := range h.server.headers {
		if strings.HasPrefix(h.server.requests[i], "GET /health") {
			continue
		}
		if header.Get(HeaderAPIKey) != testKey {
			t.Fatalf("request %s without the API key", h.server.requests[i])
		}
	}
}

func TestStartIsIdempotentAcrossRetriesAndRestarts(t *testing.T) {
	h := newHarness(t, nil)
	block := make(chan struct{})
	h.server.createHook = func(CreateRequest) *fakeError { <-block; return nil }
	spec := h.spec("sb1")
	first, err := h.provider.StartSandbox(context.Background(), spec)
	if err != nil || first.Phase != sandbox.PhasePending {
		t.Fatalf("first start: %+v %v", first, err)
	}
	// While the create is in flight: pending, no second create.
	again, err := h.provider.StartSandbox(context.Background(), spec)
	if err != nil || again.Phase != sandbox.PhasePending {
		t.Fatalf("second start: %+v %v", again, err)
	}
	if status := h.status("sb1"); status.Phase != sandbox.PhasePending {
		t.Fatalf("in-flight status: %+v", status)
	}
	close(block)
	h.provider.Wait()
	if n := h.server.exact("POST /v1/sandboxes"); n != 1 {
		t.Fatalf("creates: %d", n)
	}
	// A retry with a fresh token rewrites it and keeps the runner.
	spec.Token = "fcs_rotated"
	status, err := h.provider.StartSandbox(context.Background(), spec)
	if err != nil || status.Phase != sandbox.PhaseRunning {
		t.Fatalf("adopt: %+v %v", status, err)
	}
	sb := h.only()
	if string(sb.Files["/tmp/flow/token"]) != "fcs_rotated" {
		t.Fatalf("token not rewritten: %q", sb.Files["/tmp/flow/token"])
	}
	// A restarted Flow (new provider, same server) adopts the same sandbox.
	client, _ := NewClient(h.coding.OpenSandbox)
	restarted, err := NewWithClient(sandbox.Dependencies{Config: h.coding, Now: h.clock.Now}, client)
	if err != nil {
		t.Fatal(err)
	}
	status, err = restarted.StartSandbox(context.Background(), spec)
	if err != nil || status.Phase != sandbox.PhaseRunning || status.ProviderRef != sb.ID {
		t.Fatalf("restart adopt: %+v %v", status, err)
	}
	launches := 0
	for _, cmd := range h.only().Commands {
		if cmd.Request.Background {
			launches++
		}
	}
	if launches != 1 || h.server.exact("POST /v1/sandboxes") != 1 {
		t.Fatalf("runner launched %d times, %d creates", launches, h.server.exact("POST /v1/sandboxes"))
	}
}

func TestRunnerLaunchFailureIsRetriedByAdoption(t *testing.T) {
	h := newHarness(t, nil)
	// The background launch fails every time (retries included).
	h.server.failNext["POST /v1/sandboxes/sbx-001/proxy/44772/command"] = []fakeError{{Status: 400, Code: "INVALID", Message: "boom"}}
	h.start(h.spec("sb1"))
	// mkdir is the first command and failed → token never written, no runner.
	if status := h.status("sb1"); status.Phase != sandbox.PhaseMissing || status.Message == "" {
		t.Fatalf("a runner-less sandbox must look missing so the reconciler restarts it: %+v", status)
	}
	if _, err := h.provider.StartSandbox(context.Background(), h.spec("sb1")); err == nil {
		t.Fatal("the background failure must be reported once")
	}
	status, err := h.provider.StartSandbox(context.Background(), h.spec("sb1"))
	if err != nil || status.Phase != sandbox.PhaseRunning {
		t.Fatalf("adoption must launch the runner: %+v %v", status, err)
	}
	if n := h.server.exact("POST /v1/sandboxes"); n != 1 {
		t.Fatalf("no second sandbox: %d creates", n)
	}
}

func TestDuplicateSandboxesAreRemoved(t *testing.T) {
	h := newHarness(t, nil)
	meta := func() map[string]string {
		return map[string]string{sandbox.LabelManagedBy: "flow", sandbox.LabelInstance: "flow-test", sandbox.LabelSandboxID: "sb1", sandbox.LabelKind: "session"}
	}
	older := h.server.seed(Sandbox{Status: Status{State: StateRunning}, Metadata: meta(), CreatedAt: h.clock.Now().Add(-time.Minute)})
	h.server.seed(Sandbox{Status: Status{State: StateRunning}, Metadata: meta(), CreatedAt: h.clock.Now()})
	foreign := meta()
	foreign[sandbox.LabelInstance] = "other"
	h.server.seed(Sandbox{Status: Status{State: StateRunning}, Metadata: foreign, CreatedAt: h.clock.Now()})
	status := h.status("sb1")
	if status.ProviderRef != older.ID {
		t.Fatalf("oldest must win: %+v", status)
	}
	if n := len(h.server.list()); n != 2 {
		t.Fatalf("the duplicate must be deleted and the foreign one kept, have %d", n)
	}
}

func TestCreateErrors(t *testing.T) {
	cases := []struct {
		name  string
		fail  fakeError
		is    error
		check func(*testing.T, error)
	}{
		{name: "quota", fail: fakeError{Status: 403, Code: "KUBERNETES::QUOTA_EXCEEDED", Message: "exceeded quota: flow"}, is: sandbox.ErrQuota},
		{name: "pool capacity", fail: fakeError{Status: 429, Code: "KUBERNETES::POOL_CAPACITY_EXHAUSTED", Message: "no capacity", RetryAfter: 5}, is: sandbox.ErrQuota},
		{name: "image pull", fail: fakeError{Status: 500, Code: "KUBERNETES::POD_FAILED", Message: "Back-off pulling image: ErrImagePull"}, is: sandbox.ErrImagePull},
		{name: "ready timeout", fail: fakeError{Status: 500, Code: "KUBERNETES::POD_READY_TIMEOUT", Message: "Timeout waiting"}, is: sandbox.ErrTransient},
		{name: "bad request", fail: fakeError{Status: 400, Code: "SANDBOX::INVALID_PARAMETER", Message: "timeout exceeds max"}, check: func(t *testing.T, err error) {
			if sandbox.IsTransient(err) || errors.Is(err, sandbox.ErrImagePull) {
				t.Fatalf("a bad request is permanent: %v", err)
			}
			if !strings.Contains(err.Error(), "SANDBOX::INVALID_PARAMETER") || !strings.Contains(err.Error(), "req-test") {
				t.Fatalf("the error must keep the server code and request id: %v", err)
			}
		}},
		{name: "unauthorized", fail: fakeError{Status: 401, Code: "INVALID_API_KEY", Message: "bad key"}, check: func(t *testing.T, err error) {
			if sandbox.IsTransient(err) || !strings.Contains(err.Error(), "FLOW_CODING_OPENSANDBOX_API_KEY") {
				t.Fatalf("auth errors name the setting: %v", err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, nil)
			h.server.createHook = func(CreateRequest) *fakeError { e := tc.fail; return &e }
			h.start(h.spec("sb1"))
			if status := h.status("sb1"); status.Phase != sandbox.PhaseMissing {
				t.Fatalf("a failed create leaves nothing: %+v", status)
			}
			_, err := h.provider.StartSandbox(context.Background(), h.spec("sb1"))
			if err == nil {
				t.Fatal("expected the create error")
			}
			if tc.is != nil && !errors.Is(err, tc.is) {
				t.Fatalf("want %v, got %v", tc.is, err)
			}
			if tc.check != nil {
				tc.check(t, err)
			}
			if h.server.exact("POST /v1/sandboxes") != 1 {
				t.Fatal("create must not be retried blindly")
			}
		})
	}
}

func TestStatusMapping(t *testing.T) {
	exit := func(code int) *int { return &code }
	cases := []struct {
		name    string
		state   Status
		runner  *fakeCommand // nil = no runner recorded
		restore bool
		phase   sandbox.Phase
		reason  sandbox.Reason
		code    *int
	}{
		{name: "pending", state: Status{State: StatePending, Reason: "BATCHSANDBOX_PENDING"}, phase: sandbox.PhaseStarting},
		{name: "allocated", state: Status{State: StateAllocated}, phase: sandbox.PhaseStarting},
		{name: "pool capacity", state: Status{State: StatePending, Reason: "POOL_CAPACITY_EXHAUSTED"}, phase: sandbox.PhasePending, reason: sandbox.ReasonQuota},
		{name: "pending image pull", state: Status{State: StatePending, Message: "ImagePullBackOff"}, phase: sandbox.PhaseStarting, reason: sandbox.ReasonImagePull},
		{name: "failed image pull", state: Status{State: StateFailed, Reason: "FAILED", Message: "container failed: ErrImagePull"}, phase: sandbox.PhaseFailed, reason: sandbox.ReasonImagePull},
		{name: "failed unschedulable", state: Status{State: StateFailed, Reason: "POD_PLATFORM_UNSCHEDULABLE", Message: "0/3 nodes"}, phase: sandbox.PhaseFailed, reason: sandbox.ReasonUnschedulable},
		{name: "failed quota", state: Status{State: StateFailed, Message: "exceeded quota"}, phase: sandbox.PhaseFailed, reason: sandbox.ReasonQuota},
		{name: "failed task", state: Status{State: StateFailed, Reason: "TASK_FAILED"}, phase: sandbox.PhaseFailed, reason: sandbox.ReasonExitNonZero},
		{name: "failed other", state: Status{State: StateFailed, Reason: "FAILED", Message: "Operation failed"}, phase: sandbox.PhaseFailed, reason: sandbox.ReasonStartFailed},
		{name: "running without runner", state: Status{State: StateRunning}, phase: sandbox.PhaseMissing},
		{name: "runner running", state: Status{State: StateRunning}, runner: &fakeCommand{Running: true}, phase: sandbox.PhaseRunning},
		{name: "paused", state: Status{State: StatePaused}, runner: &fakeCommand{Running: true}, phase: sandbox.PhaseRunning},
		{name: "runner succeeded", state: Status{State: StateRunning}, runner: &fakeCommand{ExitCode: exit(0)}, phase: sandbox.PhaseSucceeded, code: exit(0)},
		{name: "runner failed", state: Status{State: StateRunning}, runner: &fakeCommand{ExitCode: exit(2), Error: "exit status 2"}, phase: sandbox.PhaseFailed, reason: sandbox.ReasonExitNonZero, code: exit(2)},
		{name: "restore failed", state: Status{State: StateRunning}, runner: &fakeCommand{ExitCode: exit(sandbox.RestoreExitCode)}, restore: true, phase: sandbox.PhaseFailed, reason: sandbox.ReasonInitFailed, code: exit(86)},
		{name: "oom", state: Status{State: StateRunning}, runner: &fakeCommand{ExitCode: exit(137), Error: "signal: killed (OOM)"}, phase: sandbox.PhaseFailed, reason: sandbox.ReasonOOMKilled, code: exit(137)},
		{name: "runner lost", state: Status{State: StateRunning}, runner: &fakeCommand{ID: "gone"}, phase: sandbox.PhaseFailed, reason: sandbox.ReasonEvicted},
		{name: "stopping", state: Status{State: StateStopping, Reason: "user_delete"}, phase: sandbox.PhaseFailed, reason: sandbox.ReasonEvicted},
		{name: "expired", state: Status{State: StateTerminated, Reason: "ttl_expiry"}, phase: sandbox.PhaseMissing, reason: sandbox.ReasonDeadlineExceeded},
		{name: "unknown state", state: Status{State: "Hibernating"}, phase: sandbox.PhaseStarting},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, nil)
			meta := map[string]string{sandbox.LabelManagedBy: "flow", sandbox.LabelInstance: "flow-test", sandbox.LabelSandboxID: "sb1", sandbox.LabelKind: "session"}
			if tc.restore {
				meta[MetaRestore] = "1"
			}
			sb := h.server.seed(Sandbox{Status: tc.state, Metadata: meta, CreatedAt: h.clock.Now()})
			h.server.mu.Lock()
			if tc.runner != nil {
				id := tc.runner.ID
				if id == "" {
					id = "cmd-runner"
					tc.runner.ID = id
					tc.runner.Request.Background = true
					tc.runner.StartedAt = h.clock.Now()
					sb.Commands[id] = tc.runner
					sb.Order = append(sb.Order, id)
				}
				sb.Metadata[MetaRunner] = id
			}
			h.server.mu.Unlock()
			status := h.status("sb1")
			if status.Phase != tc.phase || status.Reason != tc.reason {
				t.Fatalf("got %s/%s (%s), want %s/%s", status.Phase, status.Reason, status.Message, tc.phase, tc.reason)
			}
			if (tc.code == nil) != (status.ExitCode == nil) || (tc.code != nil && *tc.code != *status.ExitCode) {
				t.Fatalf("exit code %v, want %v", status.ExitCode, tc.code)
			}
			if code := sandbox.ErrorCode(sandbox.KindSession, status.Phase, status.Reason); tc.phase == sandbox.PhaseFailed && code == "" {
				t.Fatal("failures must map to a session error code")
			}
		})
	}
	t.Run("missing", func(t *testing.T) {
		h := newHarness(t, nil)
		if status := h.status("nope"); status.Phase != sandbox.PhaseMissing {
			t.Fatalf("%+v", status)
		}
	})
}

func TestExpiryRenewal(t *testing.T) {
	h := newHarness(t, nil)
	h.start(h.spec("sb1"))
	sb := h.only()
	initial := *sb.ExpiresAt
	h.status("sb1")
	if len(h.server.renewalsCopy()) != 0 {
		t.Fatal("no renewal while more than half the window is left")
	}
	h.clock.Advance(40 * time.Minute)
	h.status("sb1")
	if renewals := h.server.renewalsCopy(); len(renewals) != 1 || !renewals[0].Equal(h.clock.Now().Add(time.Hour)) || !h.only().ExpiresAt.After(initial) {
		t.Fatalf("renewals: %v", renewals)
	}
	// Renewals stop at Flow's deadline (+ grace and margin).
	for i := 0; i < 12; i++ {
		h.clock.Advance(40 * time.Minute)
		h.status("sb1")
	}
	deadline := h.only().Metadata[MetaDeadline]
	renewals := h.server.renewalsCopy()
	last := renewals[len(renewals)-1]
	if !last.Equal(time.Unix(mustInt(t, deadline), 0)) {
		t.Fatalf("the last renewal must reach the deadline exactly: %v vs %s", last, deadline)
	}
}

func mustInt(t *testing.T, raw string) int64 {
	t.Helper()
	var n int64
	for _, c := range raw {
		if c < '0' || c > '9' {
			t.Fatalf("not a number: %q", raw)
		}
		n = n*10 + int64(c-'0')
	}
	return n
}

func TestStopSandbox(t *testing.T) {
	t.Run("graceful", func(t *testing.T) {
		h := newHarness(t, nil)
		h.start(h.spec("sb1"))
		if err := h.provider.StopSandbox(context.Background(), "sb1", sandbox.StopOptions{Grace: time.Minute}); err != nil {
			t.Fatal(err)
		}
		sb := h.only()
		if sb.Metadata[MetaStopAt] == "" {
			t.Fatalf("stop request not recorded: %v", sb.Metadata)
		}
		// The fake runner exits on SIGTERM; the reconciler's next call deletes.
		if status := h.status("sb1"); status.Phase != sandbox.PhaseFailed || *status.ExitCode != 143 {
			t.Fatalf("after SIGTERM: %+v", status)
		}
		if err := h.provider.StopSandbox(context.Background(), "sb1", sandbox.StopOptions{Grace: time.Minute}); err != nil {
			t.Fatal(err)
		}
		if status := h.status("sb1"); status.Phase != sandbox.PhaseMissing {
			t.Fatalf("after delete: %+v", status)
		}
	})
	t.Run("grace expires", func(t *testing.T) {
		h := newHarness(t, nil)
		h.server.setForeground(func(sb *fakeSandbox, req RunCommandRequest) ([]string, []string, int) { return nil, nil, 0 })
		h.start(h.spec("sb1"))
		// A runner that ignores SIGTERM: emulate by restarting it.
		if err := h.provider.StopSandbox(context.Background(), "sb1", sandbox.StopOptions{Grace: time.Minute}); err != nil {
			t.Fatal(err)
		}
		h.server.mu.Lock()
		for _, cmd := range h.only_locked().Commands {
			if cmd.Request.Background {
				cmd.Running, cmd.ExitCode = true, nil
			}
		}
		h.server.mu.Unlock()
		if err := h.provider.StopSandbox(context.Background(), "sb1", sandbox.StopOptions{Grace: time.Minute}); err != nil {
			t.Fatal(err)
		}
		if len(h.server.list()) != 1 {
			t.Fatal("deleted before the grace ran out")
		}
		h.clock.Advance(2 * time.Minute)
		if err := h.provider.StopSandbox(context.Background(), "sb1", sandbox.StopOptions{Grace: time.Minute}); err != nil {
			t.Fatal(err)
		}
		if len(h.server.list()) != 0 {
			t.Fatal("not deleted after the grace")
		}
	})
	t.Run("forced and stopping", func(t *testing.T) {
		h := newHarness(t, nil)
		h.server.deleteToStopping = true
		h.start(h.spec("sb1"))
		if err := h.provider.StopSandbox(context.Background(), "sb1", sandbox.StopOptions{Grace: time.Second}); err != nil {
			t.Fatal(err)
		}
		if status := h.status("sb1"); status.Phase == sandbox.PhaseMissing {
			t.Fatalf("a stopping sandbox is not gone yet: %+v", status)
		}
		if err := h.provider.StopSandbox(context.Background(), "sb1", sandbox.StopOptions{Grace: time.Second}); err != nil {
			t.Fatal(err)
		}
		if err := h.provider.StopSandbox(context.Background(), "missing", sandbox.StopOptions{}); err != nil {
			t.Fatalf("stopping a missing sandbox is success: %v", err)
		}
	})
}

func (h *harness) only_locked() *fakeSandbox {
	for _, sb := range h.server.sandboxes {
		return sb
	}
	return nil
}

func TestExecStreamsOutput(t *testing.T) {
	h := newHarness(t, nil)
	h.start(h.spec("sb1"))
	var seenMu sync.Mutex
	var seen RunCommandRequest
	h.server.setForeground(func(sb *fakeSandbox, req RunCommandRequest) ([]string, []string, int) {
		if len(req.Argv) > 0 && req.Argv[len(req.Argv)-1] == "-s" {
			seenMu.Lock()
			defer seenMu.Unlock()
			seen = req
			return []string{"line one", "line two"}, []string{"warning"}, 7
		}
		return nil, nil, 0
	})
	var stdout, stderr bytes.Buffer
	result, err := h.provider.Exec(context.Background(), "sb1", sandbox.ExecRequest{Command: []string{"bash", "-s"}, Stdin: strings.NewReader("echo hi\n"), Stdout: &stdout, Stderr: &stderr})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 7 || stdout.String() != "line one\nline two\n" || stderr.String() != "warning\n" {
		t.Fatalf("exec: %d %q %q", result.ExitCode, stdout.String(), stderr.String())
	}
	seenMu.Lock()
	defer seenMu.Unlock()
	if seen.UID == nil || *seen.UID != 1000 || seen.Cwd != sandbox.WorkspacePath || seen.Argv[0] != "/bin/sh" {
		t.Fatalf("exec request: %+v", seen)
	}
	stdinFile := seen.Argv[3]
	if got := string(h.only().Files[stdinFile]); got != "echo hi\n" {
		t.Fatalf("stdin not uploaded to %s: %q", stdinFile, got)
	}
	if _, err := h.provider.Exec(context.Background(), "nope", sandbox.ExecRequest{Command: []string{"true"}}); !errors.Is(err, sandbox.ErrNotFound) {
		t.Fatalf("missing sandbox: %v", err)
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestExecTTY(t *testing.T) {
	h := newHarness(t, nil)
	h.start(h.spec("sb1"))
	stdinReader, stdinWriter := io.Pipe()
	resize := make(chan sandbox.TerminalSize, 1)
	resize <- sandbox.TerminalSize{Width: 120, Height: 40}
	var out syncBuffer
	done := make(chan struct{})
	var result sandbox.ExecResult
	var err error
	go func() {
		defer close(done)
		result, err = h.provider.Exec(context.Background(), "sb1", sandbox.ExecRequest{Command: []string{"bash", "-l"}, TTY: true, Stdin: stdinReader, Stdout: &out, Resize: resize})
	}()
	_, _ = stdinWriter.Write([]byte("ls\n"))
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(out.String(), "echo:ls") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	_, _ = stdinWriter.Write([]byte("\x04"))
	<-done
	if err != nil || result.ExitCode != 3 {
		t.Fatalf("tty: %+v %v", result, err)
	}
	if !strings.HasPrefix(out.String(), "$ ") || !strings.Contains(out.String(), "echo:ls\n") {
		t.Fatalf("tty output: %q", out.String())
	}
	h.server.mu.Lock()
	defer h.server.mu.Unlock()
	if len(h.server.resizes) != 1 || h.server.resizes[0] != [2]int{120, 40} {
		t.Fatalf("resizes: %v", h.server.resizes)
	}
	if len(h.only_locked().PTYs) != 0 {
		t.Fatal("the PTY session must be deleted")
	}
}

func TestTTYCommand(t *testing.T) {
	h := newHarness(t, nil)
	got := h.provider.ttyCommand([]string{"bash", "-l"})
	if !strings.Contains(got, "setpriv --reuid=1000 --regid=1000 --init-groups -- 'bash' '-l'") || !strings.Contains(got, "else exec 'bash' '-l'") {
		t.Fatalf("tty command: %s", got)
	}
	h.provider.os.RunAsUID = 0
	if got := h.provider.ttyCommand([]string{"sh", "-c", "it's"}); got != `exec 'sh' '-c' 'it'"'"'s'` {
		t.Fatalf("quoting: %s", got)
	}
}

func TestLogs(t *testing.T) {
	h := newHarness(t, nil)
	h.start(h.spec("sb1"))
	sb := h.only()
	h.server.mu.Lock()
	h.server.sandboxes[sb.ID].Commands[sb.Metadata[MetaRunner]].Output = []string{"one", "two", "three"}
	h.server.mu.Unlock()
	read := func(options sandbox.LogOptions) string {
		stream, err := h.provider.Logs(context.Background(), "sb1", options)
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Close()
		raw, err := io.ReadAll(stream)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	if got := read(sandbox.LogOptions{}); got != "one\ntwo\nthree\n" {
		t.Fatalf("logs: %q", got)
	}
	if got := read(sandbox.LogOptions{TailLines: 2}); got != "two\nthree\n" {
		t.Fatalf("tail: %q", got)
	}
	h.server.finishRunner(sb.ID, 0, "four")
	if got := read(sandbox.LogOptions{Follow: true}); got != "one\ntwo\nthree\nfour\n" {
		t.Fatalf("follow: %q", got)
	}
	if _, err := h.provider.Logs(context.Background(), "nope", sandbox.LogOptions{}); !errors.Is(err, sandbox.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

func TestListAndDeleteManaged(t *testing.T) {
	h := newHarness(t, nil)
	h.start(h.spec("sb1"))
	debug := h.spec("sb2")
	debug.Kind = sandbox.KindDebug
	h.start(debug)
	h.server.seed(Sandbox{Status: Status{State: StateRunning}, Metadata: map[string]string{sandbox.LabelManagedBy: "flow", sandbox.LabelInstance: "other", sandbox.LabelSandboxID: "x"}})
	all, err := h.provider.ListManaged(context.Background(), sandbox.ListFilter{})
	if err != nil || len(all) != 2 {
		t.Fatalf("list: %v %v", all, err)
	}
	debugOnly, err := h.provider.ListManaged(context.Background(), sandbox.ListFilter{Kind: sandbox.KindDebug})
	if err != nil || len(debugOnly) != 1 || debugOnly[0].Labels[sandbox.LabelSandboxID] != "sb2" || debugOnly[0].Type != TypeSandbox {
		t.Fatalf("filtered: %v %v", debugOnly, err)
	}
	// Foreign objects are never deleted.
	if err := h.provider.DeleteManaged(context.Background(), sandbox.ManagedObject{Type: TypeSandbox, Name: "sbx-003", Labels: map[string]string{sandbox.LabelManagedBy: "flow", sandbox.LabelInstance: "other"}}); err != nil {
		t.Fatal(err)
	}
	if err := h.provider.DeleteManaged(context.Background(), debugOnly[0]); err != nil {
		t.Fatal(err)
	}
	if err := h.provider.DeleteManaged(context.Background(), debugOnly[0]); err != nil {
		t.Fatalf("deleting twice is success: %v", err)
	}
	if n := len(h.server.list()); n != 2 {
		t.Fatalf("remaining: %d", n)
	}
}

func TestExecdEndpointAccess(t *testing.T) {
	h := newHarness(t, func(c *config.CodingConfig) {
		c.OpenSandbox.ExecdAccess = config.OpenSandboxExecdEndpoint
		c.OpenSandbox.ExecdToken = "execd-secret"
	})
	h.start(h.spec("sb1"))
	if status := h.status("sb1"); status.Phase != sandbox.PhaseRunning {
		t.Fatalf("status: %+v", status)
	}
	h.server.mu.Lock()
	defer h.server.mu.Unlock()
	direct := 0
	for i, r := range h.server.requests {
		if strings.Contains(r, "/proxy/") {
			t.Fatalf("endpoint mode must not use the proxy: %s", r)
		}
		if strings.HasPrefix(strings.SplitN(r, " ", 2)[1], "/execd/") {
			direct++
			if h.server.headers[i].Get(HeaderExecdToken) != "execd-secret" || h.server.headers[i].Get("X-Endpoint-Route") == "" {
				t.Fatalf("endpoint headers missing on %s", r)
			}
		}
	}
	endpoints := 0
	for _, r := range h.server.requests {
		if r == "GET /v1/sandboxes/sbx-001/endpoints/44772" {
			endpoints++
		}
	}
	if direct == 0 || endpoints != 1 {
		t.Fatalf("endpoint not resolved once (direct=%d)", direct)
	}
}

func TestArchiveRestoreScript(t *testing.T) {
	h := newHarness(t, nil)
	spec := h.spec("sb1")
	spec.Workspace.ArchiveURL = "http://flow/internal/coding-runtime/snapshots/s1/archive"
	h.start(spec)
	runner := h.server.runner(h.only().ID)
	script := runner.Request.Argv[2]
	if !strings.Contains(script, "flow-runner restore || { echo 86 > '/tmp/flow'/exit; exit 86; }") || runner.Request.Envs[sandbox.EnvArchiveURL] == "" {
		t.Fatalf("restore step: %s", script)
	}
	h.server.finishRunner(h.only().ID, sandbox.RestoreExitCode)
	status := h.status("sb1")
	if status.Reason != sandbox.ReasonInitFailed || sandbox.ErrorCode(sandbox.KindSession, status.Phase, status.Reason) == "" {
		t.Fatalf("restore failure: %+v", status)
	}
	if strings.Contains(RunnerScript("/tmp/flow", false), "restore") {
		t.Fatal("no restore step without an archive")
	}
}

func TestVolumesAndSnapshotsUnsupported(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	if _, err := h.provider.EnsureVolume(ctx, sandbox.VolumeSpec{ID: "v"}); !errors.Is(err, sandbox.ErrUnsupported) {
		t.Fatal(err)
	}
	if _, err := h.provider.CreateSnapshot(ctx, sandbox.SnapshotSpec{ID: "s"}); !errors.Is(err, sandbox.ErrUnsupported) {
		t.Fatal(err)
	}
	spec := h.spec("sb1")
	spec.Workspace.VolumeID = "vol"
	if _, err := h.provider.StartSandbox(ctx, spec); !errors.Is(err, sandbox.ErrUnsupported) {
		t.Fatal(err)
	}
	if err := h.provider.DeleteVolume(ctx, "v"); err != nil {
		t.Fatal(err)
	}
	coding := h.coding
	coding.Snapshot.Strategy = sandbox.StrategyVolumeSnapshot
	if _, err := NewWithClient(sandbox.Dependencies{Config: coding}, h.provider.client); err == nil {
		t.Fatal("volumesnapshot must be rejected")
	}
}

func TestPreflight(t *testing.T) {
	h := newHarness(t, func(c *config.CodingConfig) { c.RuntimeURL = "http://localhost:8080" })
	results := h.provider.Preflight(context.Background())
	byName := map[string]sandbox.PreflightResult{}
	for _, result := range results {
		byName[result.Name] = result
	}
	if !byName["opensandbox"].OK || !byName["auth"].OK || byName["runtime-url"].OK || !byName["egress"].Warning {
		t.Fatalf("preflight: %+v", results)
	}
	bad := newHarness(t, func(c *config.CodingConfig) { c.OpenSandbox.APIKey = "wrong" })
	for _, result := range bad.provider.Preflight(context.Background()) {
		if result.Name == "auth" && (result.OK || !strings.Contains(result.Message, "FLOW_CODING_OPENSANDBOX_API_KEY")) {
			t.Fatalf("auth: %+v", result)
		}
	}
	down := newHarness(t, nil)
	down.server.Close()
	if results := down.provider.Preflight(context.Background()); results[0].OK {
		t.Fatalf("unreachable: %+v", results)
	}
}

func TestProviderRegistered(t *testing.T) {
	if !strings.Contains(strings.Join(sandbox.Providers(), ","), config.CodingProviderOpenSandbox) {
		t.Fatalf("providers: %v", sandbox.Providers())
	}
	h := newHarness(t, nil)
	provider, err := sandbox.Open(sandbox.Dependencies{Config: h.coding})
	if err != nil || provider.Name() != config.CodingProviderOpenSandbox || !provider.Capabilities().TTY || provider.Capabilities().PersistentVolumes {
		t.Fatalf("open: %v %v", provider, err)
	}
}

var _ = http.StatusOK
