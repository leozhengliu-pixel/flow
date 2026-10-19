// Package opensandbox is the OpenSandbox sandbox provider
// (FLOW_CODING_PROVIDER=opensandbox): each Flow sandbox is one OpenSandbox
// sandbox created through the lifecycle server
// (https://github.com/alibaba/OpenSandbox, API specs pinned to tag
// release-1.1.0: specs/sandbox-lifecycle.yml 0.1.0, specs/execd-api.yaml
// 1.0.0). Design notes: docs/coding-sessions-design.md §9.1.
//
// How the Provider contract maps onto OpenSandbox:
//
//   - Identity. OpenSandbox assigns sandbox ids, so Flow finds its sandboxes
//     by metadata (the flow.dev/* labels every provider sets, which
//     OpenSandbox stores as Kubernetes labels) instead of deterministic names.
//     Creation is idempotent through a lookup before every create; duplicates
//     from a crash or a lease hand-over are removed (oldest wins).
//   - Start. POST /sandboxes blocks server-side until the Pod is running
//     (image pulls included), longer than a reconcile pass, so the create runs
//     in the background and StartSandbox reports pending; a failed create is
//     returned by the next StartSandbox with the usual error classes
//     (ErrQuota, ErrImagePull, ErrTransient). The sandbox's entrypoint only
//     keeps it alive; once execd answers Flow uploads the sandbox token
//     (FLOW_CODING_OPENSANDBOX_TOKEN_PATH, never an env var) and launches
//     flow-runner as a background execd command whose id is stored in the
//     metadata (flow.dev/runner). Its status and exit code are the sandbox's
//     phase; its output is the runner log.
//   - Stop. SIGTERM to the runner, then DELETE after the grace (the request
//     time is kept in metadata so retries and other replicas agree).
//   - Expiry. Sandboxes are created with a timeout of at most
//     FLOW_CODING_OPENSANDBOX_EXPIRY_WINDOW and renewed while Flow observes
//     them, up to Flow's own deadline, so OpenSandbox removes orphans of a
//     Flow that is gone.
//   - Snapshots use the archive strategy (restore runs inline before the
//     runner, like the docker provider); there are no Flow volumes.
package opensandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"

	"flow/api/internal/config"
	"flow/api/internal/sandbox"
)

func init() { sandbox.Register(config.CodingProviderOpenSandbox, New) }

// Metadata keys Flow adds besides sandbox.ObjectLabels.
const (
	// MetaRunner is the execd command id of the launched runner.
	MetaRunner = "flow.dev/runner"
	// MetaDeadline is the latest expiry (unix seconds) renewals may reach.
	MetaDeadline = "flow.dev/deadline"
	// MetaStopAt is when a graceful stop was requested (unix seconds).
	MetaStopAt = "flow.dev/stop-requested"
	// MetaRestore marks sandboxes whose runner restores an archive first.
	MetaRestore = "flow.dev/restore"
	// TypeSandbox is the ManagedObject type of OpenSandbox sandboxes.
	TypeSandbox = "sandbox"
)

// keepAlive is the sandbox entrypoint: the runner is an execd command.
var keepAlive = []string{"tail", "-f", "/dev/null"}

// expiryMargin is added to Flow's deadline for the hard expiry cap.
const expiryMargin = 10 * time.Minute

// Provider drives sandboxes through an OpenSandbox lifecycle server.
type Provider struct {
	coding config.CodingConfig
	os     config.OpenSandboxConfig
	client *Client
	now    func() time.Time

	mu       sync.Mutex
	creating map[string]bool  // sandbox id → background create in flight
	failed   map[string]error // sandbox id → error of the last background create
	execd    map[string]*Execd
	bg       context.Context
	wg       sync.WaitGroup
	// readyPoll is the execd readiness poll interval (tests shorten it).
	readyPoll time.Duration
	readyWait time.Duration
}

// New builds the provider from FLOW_CODING_OPENSANDBOX_*.
func New(deps sandbox.Dependencies) (sandbox.Provider, error) {
	client, err := NewClient(deps.Config.OpenSandbox)
	if err != nil {
		return nil, fmt.Errorf("opensandbox sandbox provider: %w", err)
	}
	return NewWithClient(deps, client)
}

// NewWithClient builds the provider over a client (tests).
func NewWithClient(deps sandbox.Dependencies, client *Client) (*Provider, error) {
	c := deps.Config
	if c.InstanceID == "" {
		c.InstanceID = "flow"
	}
	if c.Snapshot.Strategy == sandbox.StrategyVolumeSnapshot {
		return nil, errors.New("opensandbox sandbox provider: FLOW_CODING_SNAPSHOT_STRATEGY=volumesnapshot is not supported (use archive)")
	}
	if err := c.OpenSandbox.Validate(c.Sizes); err != nil {
		return nil, fmt.Errorf("opensandbox sandbox provider: %w", err)
	}
	for name := range c.Sizes {
		if _, _, err := (&Provider{coding: c, os: c.OpenSandbox}).resources(sandbox.ResourcesFor(c, name)); err != nil {
			return nil, fmt.Errorf("opensandbox sandbox provider: FLOW_CODING_SIZE_%s: %w", strings.ToUpper(name), err)
		}
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	return &Provider{
		coding: c, os: c.OpenSandbox, client: client, now: now,
		creating: map[string]bool{}, failed: map[string]error{}, execd: map[string]*Execd{},
		bg: context.Background(), readyPoll: time.Second, readyWait: 2 * time.Minute,
	}, nil
}

func (p *Provider) Name() string { return config.CodingProviderOpenSandbox }

func (p *Provider) Capabilities() sandbox.Capabilities {
	return sandbox.Capabilities{Exec: true, TTY: true, NetworkPolicy: p.os.EgressPolicy == config.OpenSandboxEgressProxy}
}

// Wait blocks until background creates finished (tests and shutdown).
func (p *Provider) Wait() { p.wg.Wait() }

// wrap maps client errors to the sandbox contract.
func wrap(op string, err error) error {
	if err == nil {
		return nil
	}
	if IsNotFound(err) {
		return fmt.Errorf("%s: %w: %v", op, sandbox.ErrNotFound, err)
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.Status == 0 && errors.Is(apiErr.Err, context.Canceled):
			return fmt.Errorf("%s: %w", op, err)
		case apiErr.Status == http.StatusUnauthorized:
			return fmt.Errorf("%s: check FLOW_CODING_OPENSANDBOX_API_KEY: %w", op, err)
		case apiErr.Retryable():
			return fmt.Errorf("%s: %w: %v", op, sandbox.ErrTransient, err)
		}
		return fmt.Errorf("%s: %w", op, err)
	}
	return fmt.Errorf("%s: %w: %v", op, sandbox.ErrTransient, err)
}

// createError classifies a failed POST /sandboxes (design §22 error codes).
func createError(err error) error {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return fmt.Errorf("create sandbox: %w: %v", sandbox.ErrTransient, err)
	}
	text := strings.ToLower(apiErr.Code + " " + apiErr.Message)
	switch {
	case strings.Contains(text, "quota_exceeded") || strings.Contains(text, "exceeded quota") || apiErr.Status == http.StatusTooManyRequests || strings.Contains(text, "pool_capacity_exhausted"):
		return fmt.Errorf("create sandbox: %w: %v", sandbox.ErrQuota, err)
	case strings.Contains(text, "imagepull") || strings.Contains(text, "errimage") || strings.Contains(text, "image_pull") || strings.Contains(text, "invalidimagename"):
		return fmt.Errorf("create sandbox: %w: %v", sandbox.ErrImagePull, err)
	}
	return wrap("create sandbox", err)
}

// identity is the metadata filter of one sandbox.
func (p *Provider) identity(sandboxID string) map[string]string {
	return map[string]string{sandbox.LabelManagedBy: sandbox.ManagedByValue, sandbox.LabelInstance: p.coding.InstanceID, sandbox.LabelSandboxID: sandboxID}
}

// find returns the sandbox of a Flow sandbox id (nil when there is none).
// Duplicates (a create raced a crash or a lease hand-over) are deleted,
// keeping the oldest.
func (p *Provider) find(ctx context.Context, sandboxID string) (*Sandbox, error) {
	items, err := p.client.List(ctx, p.identity(sandboxID))
	if err != nil {
		return nil, wrap("list sandboxes", err)
	}
	live := []Sandbox{}
	for _, item := range items {
		// Defensive: a server that ignores the filter must not make Flow
		// adopt someone else's sandbox.
		if item.Metadata[sandbox.LabelSandboxID] != sandboxID || item.Metadata[sandbox.LabelInstance] != p.coding.InstanceID {
			continue
		}
		live = append(live, item)
	}
	if len(live) == 0 {
		return nil, nil
	}
	sort.SliceStable(live, func(i, j int) bool {
		// Prefer a sandbox with a runner, then the oldest.
		ri, rj := live[i].Metadata[MetaRunner] != "", live[j].Metadata[MetaRunner] != ""
		if ri != rj {
			return ri
		}
		if !live[i].CreatedAt.Equal(live[j].CreatedAt) {
			return live[i].CreatedAt.Before(live[j].CreatedAt)
		}
		return live[i].ID < live[j].ID
	})
	for _, duplicate := range live[1:] {
		if duplicate.Status.State == StateTerminated || duplicate.Status.State == StateStopping {
			continue
		}
		_ = p.client.Delete(ctx, duplicate.ID)
	}
	found := live[0]
	return &found, nil
}

// resources are the resourceLimits and resourceRequests of a size.
func (p *Provider) resources(r sandbox.Resources) (limits, requests map[string]string, err error) {
	if override, ok := p.os.SizeResources[r.Size]; ok {
		return maps.Clone(override), nil, nil
	}
	limits = map[string]string{}
	if r.CPU != "" {
		if _, err := resource.ParseQuantity(r.CPU); err != nil {
			return nil, nil, fmt.Errorf("cpu %q: %w", r.CPU, err)
		}
		limits["cpu"] = r.CPU
	}
	if r.Memory != "" {
		if _, err := resource.ParseQuantity(r.Memory); err != nil {
			return nil, nil, fmt.Errorf("memory %q: %w", r.Memory, err)
		}
		limits["memory"] = r.Memory
	}
	if p.os.EphemeralStorage && r.EphemeralStorage != "" {
		if _, err := resource.ParseQuantity(r.EphemeralStorage); err != nil {
			return nil, nil, fmt.Errorf("ephemeral storage %q: %w", r.EphemeralStorage, err)
		}
		limits["ephemeral-storage"] = r.EphemeralStorage
	}
	if ratio := p.coding.CPURequestRatio; ratio > 0 && ratio < 1 && r.CPU != "" {
		quantity := resource.MustParse(r.CPU)
		milli := max(int64(float64(quantity.MilliValue())*ratio), 1)
		requests = map[string]string{"cpu": strconv.FormatInt(milli, 10) + "m"}
		if r.Memory != "" {
			requests["memory"] = r.Memory
		}
	}
	return limits, requests, nil
}

// env is the sandbox environment: sandbox.RunnerEnv with the token at
// FLOW_CODING_OPENSANDBOX_TOKEN_PATH.
func (p *Provider) env(spec sandbox.SandboxSpec) map[string]string {
	env := map[string]string{}
	for _, item := range sandbox.RunnerEnv(p.coding, spec, sandbox.Mode(spec.Kind)) {
		env[item.Name] = item.Value
	}
	env[sandbox.EnvTokenFile] = p.os.TokenPath
	return env
}

// stateDir holds the token, the runner's pid and exit code.
func (p *Provider) stateDir() string { return path.Dir(p.os.TokenPath) }

// networkPolicy is the OpenSandbox egress policy (FLOW_CODING_OPENSANDBOX_EGRESS_POLICY=proxy).
func (p *Provider) networkPolicy(spec sandbox.SandboxSpec) *NetworkPolicy {
	if p.os.EgressPolicy != config.OpenSandboxEgressProxy {
		return nil
	}
	policy := &NetworkPolicy{DefaultAction: "deny"}
	seen := map[string]bool{}
	add := func(host string) {
		host = strings.ToLower(strings.TrimSpace(host))
		// The egress sidecar matches domains only (no IPs or CIDRs).
		if host == "" || seen[host] || strings.Trim(host, "0123456789.:[]") == "" {
			return
		}
		seen[host] = true
		policy.Egress = append(policy.Egress, NetworkRule{Action: "allow", Target: host})
	}
	add(sandbox.RuntimeHost(p.coding.RuntimeURL))
	add(sandbox.RuntimeHost(spec.ProxyURL))
	for _, host := range p.os.EgressAllow {
		add(host)
	}
	return policy
}

// BuildCreate is the create request of a sandbox (pure, deterministic).
func (p *Provider) BuildCreate(spec sandbox.SandboxSpec) (CreateRequest, error) {
	spec = sandbox.CompleteSpec(p.coding, spec)
	now := p.now()
	metadata := maps.Clone(p.os.ExtraMetadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	maps.Copy(metadata, sandbox.ObjectLabels(p.coding, spec))
	hardCap := spec.Deadline + sandbox.TerminationGrace(p.coding.Timeouts.StopGrace) + expiryMargin
	metadata[MetaDeadline] = strconv.FormatInt(now.Add(hardCap).Unix(), 10)
	if spec.Workspace.ArchiveURL != "" {
		metadata[MetaRestore] = "1"
	}
	timeout := int(min(p.os.ExpiryWindow, hardCap) / time.Second)
	timeout = max(timeout, 60) // the API minimum
	create := CreateRequest{
		Timeout:    &timeout,
		Env:        p.env(spec),
		Metadata:   metadata,
		Entrypoint: slices.Clone(keepAlive),
		Extensions: maps.Clone(p.os.Extensions),
	}
	if pool := p.os.Pools[spec.Resources.Size]; pool != "" {
		if create.Extensions == nil {
			create.Extensions = map[string]string{}
		}
		create.Extensions["poolRef"] = pool
		return create, nil
	}
	limits, requests, err := p.resources(spec.Resources)
	if err != nil {
		return CreateRequest{}, err
	}
	create.Image = &ImageSpec{URI: spec.Image}
	if p.os.ImageUsername != "" {
		create.Image.Auth = &ImageAuth{Username: p.os.ImageUsername, Password: p.os.ImagePassword}
	}
	create.ResourceLimits, create.ResourceRequests = limits, requests
	create.NetworkPolicy = p.networkPolicy(spec)
	return create, nil
}

// StartSandbox finds or creates the sandbox and launches its runner. A new
// sandbox is created in the background; until then it reports pending.
func (p *Provider) StartSandbox(ctx context.Context, spec sandbox.SandboxSpec) (sandbox.SandboxStatus, error) {
	if !sandbox.ValidID(spec.ID) {
		return sandbox.SandboxStatus{}, fmt.Errorf("opensandbox: invalid sandbox id %q", spec.ID)
	}
	if spec.Workspace.VolumeID != "" {
		return sandbox.SandboxStatus{}, fmt.Errorf("opensandbox: %w: workspace volumes (use the archive snapshot strategy)", sandbox.ErrUnsupported)
	}
	p.mu.Lock()
	if p.creating[spec.ID] {
		p.mu.Unlock()
		return p.pending(spec, "creating the OpenSandbox sandbox"), nil
	}
	if err := p.failed[spec.ID]; err != nil {
		delete(p.failed, spec.ID)
		p.mu.Unlock()
		return sandbox.SandboxStatus{}, err
	}
	p.mu.Unlock()
	existing, err := p.find(ctx, spec.ID)
	if err != nil {
		return sandbox.SandboxStatus{}, err
	}
	if existing != nil {
		if existing.Status.State == StateRunning && spec.Token != "" {
			// Adopt: rewrite the token (a fresh one was minted for this
			// attempt) and launch the runner if no earlier attempt did.
			updated, err := p.ensureRunner(ctx, *existing, spec)
			if err != nil {
				return sandbox.SandboxStatus{}, err
			}
			existing = &updated
		}
		return p.status(ctx, *existing), nil
	}
	if spec.Token == "" {
		return sandbox.SandboxStatus{}, fmt.Errorf("opensandbox: sandbox %s has no token", spec.ID)
	}
	create, err := p.BuildCreate(spec)
	if err != nil {
		return sandbox.SandboxStatus{}, err
	}
	p.mu.Lock()
	if p.creating[spec.ID] {
		p.mu.Unlock()
		return p.pending(spec, "creating the OpenSandbox sandbox"), nil
	}
	p.creating[spec.ID] = true
	p.wg.Add(1)
	p.mu.Unlock()
	go p.create(spec, create)
	return p.pending(spec, "creating the OpenSandbox sandbox"), nil
}

func (p *Provider) pending(spec sandbox.SandboxSpec, message string) sandbox.SandboxStatus {
	labels := sandbox.ObjectLabels(p.coding, sandbox.CompleteSpec(p.coding, spec))
	return sandbox.SandboxStatus{ID: spec.ID, Kind: spec.Kind, Phase: sandbox.PhasePending, Message: message, Region: spec.Region, Labels: labels, CreatedAt: p.now()}
}

// create runs POST /sandboxes and the runner launch in the background.
func (p *Provider) create(spec sandbox.SandboxSpec, create CreateRequest) {
	defer p.wg.Done()
	err := func() error {
		ctx, cancel := context.WithTimeout(p.bg, p.client.createTimeout+p.readyWait+2*p.client.requestTimeout)
		defer cancel()
		created, err := p.client.Create(ctx, create)
		if err != nil {
			// The server may have finished despite a lost response.
			if existing, findErr := p.find(ctx, spec.ID); findErr == nil && existing != nil {
				created = *existing
			} else {
				return createError(err)
			}
		}
		if created.Status.State != StateRunning {
			// Created but not running (rare: the server answers when
			// ready); the next StartSandbox adopts it once it is.
			return nil
		}
		if created.Metadata == nil {
			created.Metadata = create.Metadata
		}
		_, err = p.ensureRunner(ctx, created, spec)
		return err
	}()
	p.mu.Lock()
	delete(p.creating, spec.ID)
	if err != nil {
		p.failed[spec.ID] = err
	}
	p.mu.Unlock()
}

// execdFor returns the execd client of a sandbox.
func (p *Provider) execdFor(ctx context.Context, id string) (*Execd, error) {
	p.mu.Lock()
	cached := p.execd[id]
	p.mu.Unlock()
	if cached != nil {
		return cached, nil
	}
	execd := &Execd{client: p.client, headers: map[string]string{}}
	if p.os.ExecdToken != "" {
		execd.headers[HeaderExecdToken] = p.os.ExecdToken
	}
	switch p.os.ExecdAccess {
	case config.OpenSandboxExecdEndpoint:
		endpoint, err := p.client.Endpoint(ctx, id, DefaultExecdPort)
		if err != nil {
			return nil, wrap("execd endpoint", err)
		}
		base := strings.TrimRight(endpoint.Endpoint, "/")
		if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
			base = "http://" + base
		}
		execd.base = base
		maps.Copy(execd.headers, endpoint.Headers)
	default:
		execd.base = p.client.ProxyURL(id, DefaultExecdPort)
	}
	p.mu.Lock()
	p.execd[id] = execd
	p.mu.Unlock()
	return execd, nil
}

func (p *Provider) forgetExecd(id string) {
	p.mu.Lock()
	delete(p.execd, id)
	p.mu.Unlock()
}

// runAs is the uid/gid of runner commands (nil = execd's user).
func (p *Provider) runAs() *int {
	if p.os.RunAsUID <= 0 {
		return nil
	}
	uid := p.os.RunAsUID
	return &uid
}

// waitExecd polls execd until it answers.
func (p *Provider) waitExecd(ctx context.Context, execd *Execd) error {
	deadline := p.now().Add(p.readyWait)
	for {
		err := execd.Ping(ctx)
		if err == nil {
			return nil
		}
		if p.now().After(deadline) {
			return fmt.Errorf("execd not ready: %w: %v", sandbox.ErrTransient, err)
		}
		if err := p.client.sleep(ctx, p.readyPoll); err != nil {
			return err
		}
	}
}

// shellQuote quotes a string for POSIX sh.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

// foreground runs a short command as execd's user and fails on a non-zero exit.
func (p *Provider) foreground(ctx context.Context, execd *Execd, argv []string, uid *int) error {
	ctx, cancel := context.WithTimeout(ctx, p.client.requestTimeout)
	defer cancel()
	result, err := execd.Run(ctx, RunCommandRequest{Argv: argv, UID: uid, GID: uid, TimeoutMS: p.client.requestTimeout.Milliseconds()}, nil)
	if err != nil {
		return wrap("execd command", err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("opensandbox: %s exited %d: %s", strings.Join(argv, " "), result.ExitCode, result.Error)
	}
	return nil
}

// writeToken uploads the sandbox token (0600, owned by the runner user).
func (p *Provider) writeToken(ctx context.Context, execd *Execd, token string) error {
	dir := p.stateDir()
	if err := p.foreground(ctx, execd, []string{"/bin/sh", "-c", `mkdir -p "$0" && chmod 0700 "$0"`, dir}, nil); err != nil {
		return err
	}
	if err := execd.Upload(ctx, p.os.TokenPath, []byte(token), 600); err != nil {
		return wrap("upload token", err)
	}
	owner := `chmod 0600 "$1"`
	if uid := p.runAs(); uid != nil {
		// Only effective when execd runs as root; harmless otherwise.
		owner = fmt.Sprintf(`chown %d:%d "$0" "$1" 2>/dev/null; chmod 0600 "$1"`, *uid, *uid)
	}
	return p.foreground(ctx, execd, []string{"/bin/sh", "-c", owner, dir, p.os.TokenPath}, nil)
}

// RunnerScript is the runner's wrapper: the first launch records the
// runner's pid and exit code in the state directory; a relaunch (after a
// crash between launch and recording the command id) waits for the runner
// already running and reports its exit code instead of starting a second one.
func RunnerScript(stateDir string, restore bool) string {
	d := shellQuote(stateDir)
	restoreStep := ""
	if restore {
		restoreStep = fmt.Sprintf(`flow-runner restore || { echo %d > %s/exit; exit %d; }; `, sandbox.RestoreExitCode, d, sandbox.RestoreExitCode)
	}
	return fmt.Sprintf(`if mkdir %[1]s/launched 2>/dev/null; then `+
		`%[2]sflow-runner "$0" & echo $! > %[1]s/pid; wait $!; rc=$?; echo $rc > %[1]s/exit; exit $rc; `+
		`else while [ ! -f %[1]s/exit ]; do if ! kill -0 "$(cat %[1]s/pid 2>/dev/null)" 2>/dev/null; then sleep 1; [ -f %[1]s/exit ] || exit 137; fi; sleep 2; done; exit "$(cat %[1]s/exit)"; fi`,
		d, restoreStep)
}

// ensureRunner writes the token and launches the runner unless it runs.
func (p *Provider) ensureRunner(ctx context.Context, item Sandbox, spec sandbox.SandboxSpec) (Sandbox, error) {
	execd, err := p.execdFor(ctx, item.ID)
	if err != nil {
		return item, err
	}
	if err := p.waitExecd(ctx, execd); err != nil {
		p.forgetExecd(item.ID)
		return item, err
	}
	if spec.Token != "" {
		if err := p.writeToken(ctx, execd, spec.Token); err != nil {
			return item, err
		}
	}
	if item.Metadata[MetaRunner] != "" {
		return item, nil
	}
	spec = sandbox.CompleteSpec(p.coding, spec)
	script := RunnerScript(p.stateDir(), spec.Workspace.ArchiveURL != "" || item.Metadata[MetaRestore] == "1")
	uid := p.runAs()
	runCtx, cancel := context.WithTimeout(ctx, p.client.requestTimeout)
	result, err := execd.Run(runCtx, RunCommandRequest{
		Argv: []string{"/bin/sh", "-c", script, sandbox.Mode(spec.Kind)}, Cwd: sandbox.WorkspacePath,
		Background: true, UID: uid, GID: uid, Envs: p.env(spec),
	}, nil)
	cancel()
	if err != nil {
		return item, wrap("launch runner", err)
	}
	updated, err := p.client.PatchMetadata(ctx, item.ID, map[string]*string{MetaRunner: &result.ID})
	if err != nil {
		return item, wrap("record runner", err)
	}
	if updated.Metadata == nil {
		updated = item
		updated.Metadata = maps.Clone(item.Metadata)
		updated.Metadata[MetaRunner] = result.ID
	}
	return updated, nil
}

// Status reports a sandbox's phase (and renews its expiry while alive).
func (p *Provider) Status(ctx context.Context, sandboxID string) (sandbox.SandboxStatus, error) {
	p.mu.Lock()
	creating := p.creating[sandboxID]
	_, failed := p.failed[sandboxID]
	p.mu.Unlock()
	item, err := p.find(ctx, sandboxID)
	if err != nil {
		return sandbox.SandboxStatus{}, err
	}
	if item == nil {
		if creating {
			return sandbox.SandboxStatus{ID: sandboxID, Phase: sandbox.PhasePending, Message: "creating the OpenSandbox sandbox"}, nil
		}
		return sandbox.SandboxStatus{ID: sandboxID, Phase: sandbox.PhaseMissing}, nil
	}
	status := p.status(ctx, *item)
	if status.Phase == sandbox.PhaseStarting && item.Status.State == StateRunning && item.Metadata[MetaRunner] == "" && !creating {
		// Running without a runner and nobody launching one (a create
		// whose runner launch failed, or Flow restarted mid-create):
		// report it missing so the reconciler starts it again, which
		// adopts the sandbox with a fresh token.
		status.Phase = sandbox.PhaseMissing
		if failed {
			status.Message = "the runner could not be launched"
		}
	}
	return status, nil
}

// status maps an OpenSandbox sandbox to a Flow status.
func (p *Provider) status(ctx context.Context, item Sandbox) sandbox.SandboxStatus {
	labels := maps.Clone(item.Metadata)
	status := sandbox.SandboxStatus{
		ID: labels[sandbox.LabelSandboxID], Kind: sandbox.Kind(labels[sandbox.LabelKind]), ProviderRef: item.ID,
		Region: labels[sandbox.LabelRegion], Labels: labels, CreatedAt: item.CreatedAt, Message: item.Status.Message,
	}
	switch item.Status.State {
	case StatePending, StateAllocated:
		status.Phase = sandbox.PhaseStarting
		if strings.Contains(strings.ToUpper(item.Status.Reason), "POOL_CAPACITY") {
			status.Phase, status.Reason = sandbox.PhasePending, sandbox.ReasonQuota
		}
		if reason := failureReason(item.Status); reason == sandbox.ReasonImagePull || reason == sandbox.ReasonUnschedulable {
			status.Reason = reason
		}
	case StateRunning, StatePausing, StatePaused, StateResuming:
		p.renew(ctx, item)
		p.runnerStatus(ctx, item, &status)
		if item.Status.State != StateRunning && status.Phase == sandbox.PhaseRunning {
			status.Message = "OpenSandbox reports the sandbox " + strings.ToLower(item.Status.State)
		}
	case StateStopping, StateTerminated:
		status.Phase = sandbox.PhaseFailed
		if item.Status.State == StateTerminated {
			status.Phase = sandbox.PhaseMissing
		}
		status.Reason = sandbox.ReasonEvicted
		if strings.Contains(strings.ToLower(item.Status.Reason), "ttl") || strings.Contains(strings.ToLower(item.Status.Reason), "expir") {
			status.Reason = sandbox.ReasonDeadlineExceeded
		}
		if status.Message == "" {
			status.Message = "OpenSandbox reports the sandbox " + strings.ToLower(item.Status.State)
		}
		finished := item.CreatedAt
		if item.Status.LastTransitionAt != nil {
			finished = *item.Status.LastTransitionAt
		}
		status.FinishedAt = &finished
	case StateFailed:
		status.Phase, status.Reason = sandbox.PhaseFailed, failureReason(item.Status)
		if status.Message == "" {
			status.Message = item.Status.Reason
		}
	default:
		status.Phase = sandbox.PhaseStarting
		status.Message = strings.TrimSpace("OpenSandbox state " + item.Status.State + ": " + item.Status.Message)
	}
	return status
}

// failureReason maps OpenSandbox failure reasons and messages to Flow reasons.
func failureReason(s Status) sandbox.Reason {
	text := strings.ToLower(s.Reason + " " + s.Message)
	switch {
	case strings.Contains(text, "imagepull") || strings.Contains(text, "errimage") || strings.Contains(text, "image_pull") || strings.Contains(text, "invalidimagename"):
		return sandbox.ReasonImagePull
	case strings.Contains(text, "unschedulable") || strings.Contains(text, "insufficient"):
		return sandbox.ReasonUnschedulable
	case strings.Contains(text, "quota"):
		return sandbox.ReasonQuota
	case strings.Contains(text, "oomkilled") || strings.Contains(text, "out of memory"):
		return sandbox.ReasonOOMKilled
	case strings.Contains(text, "evict"):
		return sandbox.ReasonEvicted
	case strings.Contains(text, "task_failed"):
		return sandbox.ReasonExitNonZero
	}
	if strings.TrimSpace(text) == "" {
		return sandbox.ReasonUnknown
	}
	return sandbox.ReasonStartFailed
}

// runnerStatus derives the phase of a running sandbox from its runner command.
func (p *Provider) runnerStatus(ctx context.Context, item Sandbox, status *sandbox.SandboxStatus) {
	id := item.Metadata[MetaRunner]
	if id == "" {
		status.Phase = sandbox.PhaseStarting
		if status.Message == "" {
			status.Message = "launching the runner"
		}
		return
	}
	execd, err := p.execdFor(ctx, item.ID)
	if err == nil {
		var command CommandStatus
		command, err = execd.Status(ctx, id)
		if err == nil {
			status.Message = ""
			if command.StartedAt != nil {
				status.StartedAt = command.StartedAt
			}
			if command.Running {
				status.Phase = sandbox.PhaseRunning
				return
			}
			code := 0
			if command.ExitCode != nil {
				code = *command.ExitCode
			}
			status.ExitCode = &code
			status.FinishedAt = command.FinishedAt
			switch {
			case code == 0:
				status.Phase = sandbox.PhaseSucceeded
			case code == sandbox.RestoreExitCode && item.Metadata[MetaRestore] == "1":
				status.Phase, status.Reason, status.Message = sandbox.PhaseFailed, sandbox.ReasonInitFailed, "restoring the snapshot archive failed"
			case code == 137 && strings.Contains(strings.ToLower(command.Error), "oom"):
				status.Phase, status.Reason, status.Message = sandbox.PhaseFailed, sandbox.ReasonOOMKilled, "the runner ran out of memory; use a larger sandbox size"
			default:
				status.Phase, status.Reason, status.Message = sandbox.PhaseFailed, sandbox.ReasonExitNonZero, command.Error
			}
			return
		}
		if IsNotFound(err) {
			// execd no longer knows the runner: the container restarted.
			status.Phase, status.Reason, status.Message = sandbox.PhaseFailed, sandbox.ReasonEvicted, "the runner process was lost (the sandbox container restarted)"
			return
		}
	}
	p.forgetExecd(item.ID)
	// execd unreachable: keep reporting running; the heartbeat timeout
	// and deadline catch a sandbox that stays unreachable.
	status.Phase = sandbox.PhaseRunning
	status.Message = "execd unreachable: " + err.Error()
}

// renew keeps the OpenSandbox expiry FLOW_CODING_OPENSANDBOX_EXPIRY_WINDOW
// ahead, never past the sandbox's Flow deadline (best effort).
func (p *Provider) renew(ctx context.Context, item Sandbox) {
	if item.ExpiresAt == nil {
		return
	}
	deadline, err := strconv.ParseInt(item.Metadata[MetaDeadline], 10, 64)
	if err != nil {
		return
	}
	now := p.now()
	if item.ExpiresAt.Sub(now) > p.os.ExpiryWindow/2 {
		return
	}
	target := now.Add(p.os.ExpiryWindow)
	if hardCap := time.Unix(deadline, 0); target.After(hardCap) {
		target = hardCap
	}
	if !target.After(item.ExpiresAt.Add(time.Minute)) {
		return
	}
	_, _ = p.client.Renew(ctx, item.ID, target)
}

// StopSandbox stops the runner gracefully (SIGTERM, then DELETE after the
// grace). It returns while the grace runs; the reconciler calls it again
// until the sandbox is gone.
func (p *Provider) StopSandbox(ctx context.Context, sandboxID string, options sandbox.StopOptions) error {
	item, err := p.find(ctx, sandboxID)
	if err != nil || item == nil {
		return err
	}
	grace := options.Grace
	if grace <= 0 {
		grace = p.coding.Timeouts.StopGrace
	}
	grace = sandbox.TerminationGrace(grace)
	if item.Status.State == StateStopping || item.Status.State == StateTerminated {
		return nil
	}
	if grace > time.Second && item.Status.State == StateRunning && item.Metadata[MetaRunner] != "" && p.runnerRunning(ctx, *item) {
		requested, err := strconv.ParseInt(item.Metadata[MetaStopAt], 10, 64)
		if err != nil {
			if err := p.signalRunner(ctx, *item); err == nil {
				at := strconv.FormatInt(p.now().Unix(), 10)
				if _, err := p.client.PatchMetadata(ctx, item.ID, map[string]*string{MetaStopAt: &at}); err != nil {
					return wrap("record stop", err)
				}
				return nil
			}
			// The runner could not be signalled: delete right away.
		} else if p.now().Before(time.Unix(requested, 0).Add(grace)) {
			return nil
		}
	}
	p.forgetExecd(item.ID)
	if err := p.client.Delete(ctx, item.ID); err != nil {
		return wrap("delete sandbox", err)
	}
	return nil
}

func (p *Provider) runnerRunning(ctx context.Context, item Sandbox) bool {
	execd, err := p.execdFor(ctx, item.ID)
	if err != nil {
		return false
	}
	status, err := execd.Status(ctx, item.Metadata[MetaRunner])
	return err == nil && status.Running
}

func (p *Provider) signalRunner(ctx context.Context, item Sandbox) error {
	execd, err := p.execdFor(ctx, item.ID)
	if err != nil {
		return err
	}
	return p.foreground(ctx, execd, []string{"/bin/sh", "-c", `kill -TERM "$(cat "$0"/pid)"`, p.stateDir()}, p.runAs())
}

// Exec runs a command in a running sandbox: a PTY session for TTYs, a
// foreground execd command otherwise (stdin is uploaded to a file first).
func (p *Provider) Exec(ctx context.Context, sandboxID string, request sandbox.ExecRequest) (sandbox.ExecResult, error) {
	if len(request.Command) == 0 {
		return sandbox.ExecResult{}, errors.New("opensandbox: exec needs a command")
	}
	item, err := p.find(ctx, sandboxID)
	if err != nil {
		return sandbox.ExecResult{}, err
	}
	if item == nil {
		return sandbox.ExecResult{}, sandbox.ErrNotFound
	}
	if item.Status.State != StateRunning {
		return sandbox.ExecResult{}, fmt.Errorf("opensandbox: sandbox %s is not running (%s)", sandboxID, item.Status.State)
	}
	execd, err := p.execdFor(ctx, item.ID)
	if err != nil {
		return sandbox.ExecResult{}, err
	}
	if request.TTY {
		return p.execTTY(ctx, execd, request)
	}
	argv := request.Command
	if request.Stdin != nil {
		input, err := io.ReadAll(io.LimitReader(request.Stdin, maxExecStdin+1))
		if err != nil {
			return sandbox.ExecResult{}, err
		}
		if len(input) > maxExecStdin {
			return sandbox.ExecResult{}, fmt.Errorf("opensandbox: exec stdin exceeds %d bytes", maxExecStdin)
		}
		file := path.Join(p.stateDir(), "stdin-"+sandbox.NewID(p.now()))
		if err := execd.Upload(ctx, file, input, 600); err != nil {
			return sandbox.ExecResult{}, wrap("upload stdin", err)
		}
		argv = append([]string{"/bin/sh", "-c", `f="$0"; trap 'rm -f "$f"' EXIT; "$@" < "$f"`, file}, argv...)
		if uid := p.runAs(); uid != nil {
			if err := p.foreground(ctx, execd, []string{"chown", fmt.Sprintf("%d:%d", *uid, *uid), file}, nil); err != nil {
				return sandbox.ExecResult{}, err
			}
		}
	}
	stdout, stderr := request.Stdout, request.Stderr
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	uid := p.runAs()
	result, err := execd.Run(ctx, RunCommandRequest{Argv: argv, Cwd: sandbox.WorkspacePath, UID: uid, GID: uid}, func(event StreamEvent) error {
		var err error
		switch event.Type {
		case "stdout":
			_, err = io.WriteString(stdout, withNewline(event.Text))
		case "stderr":
			_, err = io.WriteString(stderr, withNewline(event.Text))
		}
		return err
	})
	if err != nil {
		if ctx.Err() != nil {
			return sandbox.ExecResult{}, ctx.Err()
		}
		return sandbox.ExecResult{}, wrap("exec", err)
	}
	return sandbox.ExecResult{ExitCode: result.ExitCode}, nil
}

// maxExecStdin bounds the stdin of non-TTY exec (scripts).
const maxExecStdin = 8 << 20

// withNewline restores the line break execd strips from each output line.
func withNewline(text string) string {
	if strings.HasSuffix(text, "\n") {
		return text
	}
	return text + "\n"
}

// ttyCommand runs the command as the runner user when execd is root.
func (p *Provider) ttyCommand(command []string) string {
	quoted := make([]string, len(command))
	for i, part := range command {
		quoted[i] = shellQuote(part)
	}
	line := strings.Join(quoted, " ")
	uid := p.runAs()
	if uid == nil {
		return "exec " + line
	}
	return fmt.Sprintf(`if [ "$(id -u)" = 0 ]; then exec setpriv --reuid=%[1]d --regid=%[1]d --init-groups -- %[2]s; else exec %[2]s; fi`, *uid, line)
}

func (p *Provider) execTTY(ctx context.Context, execd *Execd, request sandbox.ExecRequest) (sandbox.ExecResult, error) {
	id, err := execd.CreatePTY(ctx, sandbox.WorkspacePath, p.ttyCommand(request.Command))
	if err != nil {
		return sandbox.ExecResult{}, wrap("create pty", err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), p.client.requestTimeout)
		defer cancel()
		_ = execd.DeletePTY(cleanup, id)
	}()
	var resize chan [2]int
	if request.Resize != nil {
		resize = make(chan [2]int)
		go func() {
			defer close(resize)
			for {
				select {
				case <-ctx.Done():
					return
				case size, ok := <-request.Resize:
					if !ok {
						return
					}
					select {
					case resize <- [2]int{int(size.Width), int(size.Height)}:
					case <-ctx.Done():
						return
					}
				}
			}
		}()
	}
	code, err := execd.AttachPTY(ctx, id, TerminalIO{Stdin: request.Stdin, Stdout: request.Stdout, Resize: resize})
	if err != nil {
		if ctx.Err() != nil {
			return sandbox.ExecResult{}, ctx.Err()
		}
		return sandbox.ExecResult{}, wrap("pty", err)
	}
	return sandbox.ExecResult{ExitCode: code}, nil
}

// Logs returns the runner's combined output (restore output included).
func (p *Provider) Logs(ctx context.Context, sandboxID string, options sandbox.LogOptions) (io.ReadCloser, error) {
	item, err := p.find(ctx, sandboxID)
	if err != nil {
		return nil, err
	}
	if item == nil || item.Metadata[MetaRunner] == "" {
		return nil, sandbox.ErrNotFound
	}
	execd, err := p.execdFor(ctx, item.ID)
	if err != nil {
		return nil, err
	}
	runner := item.Metadata[MetaRunner]
	text, cursor, err := execd.Logs(ctx, runner, nil)
	if err != nil {
		return nil, wrap("runner logs", err)
	}
	if options.TailLines > 0 {
		text = tailLines(text, options.TailLines)
	}
	if !options.Follow {
		return io.NopCloser(strings.NewReader(text)), nil
	}
	reader, writer := io.Pipe()
	go func() {
		if _, err := io.WriteString(writer, text); err != nil {
			return
		}
		next := cursor
		for {
			if err := p.client.sleep(ctx, time.Second); err != nil {
				writer.CloseWithError(err)
				return
			}
			more, after, err := execd.Logs(ctx, runner, &next)
			if err != nil {
				writer.CloseWithError(wrap("runner logs", err))
				return
			}
			if more != "" {
				if _, err := io.WriteString(writer, more); err != nil {
					return
				}
			}
			next = after
			status, err := execd.Status(ctx, runner)
			if err != nil || !status.Running {
				// Drain what arrived after the last read, then end.
				if rest, _, err := execd.Logs(ctx, runner, &next); err == nil && rest != "" {
					_, _ = io.WriteString(writer, rest)
				}
				writer.Close()
				return
			}
		}
	}()
	return reader, nil
}

func tailLines(text string, n int64) string {
	lines := strings.SplitAfter(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if int64(len(lines)) <= n {
		return text
	}
	return strings.Join(lines[int64(len(lines))-n:], "")
}

func (p *Provider) filter(filter sandbox.ListFilter) map[string]string {
	metadata := map[string]string{sandbox.LabelManagedBy: sandbox.ManagedByValue, sandbox.LabelInstance: p.coding.InstanceID}
	if filter.Kind != "" {
		metadata[sandbox.LabelKind] = string(filter.Kind)
	}
	if filter.SandboxID != "" {
		metadata[sandbox.LabelSandboxID] = filter.SandboxID
	}
	if filter.SessionID != "" {
		metadata[sandbox.LabelSessionID] = filter.SessionID
	}
	return metadata
}

// ListManaged lists this instance's OpenSandbox sandboxes (for orphan GC).
func (p *Provider) ListManaged(ctx context.Context, filter sandbox.ListFilter) ([]sandbox.ManagedObject, error) {
	metadata := p.filter(filter)
	items, err := p.client.List(ctx, metadata)
	if err != nil {
		return nil, wrap("list sandboxes", err)
	}
	result := []sandbox.ManagedObject{}
	for _, item := range items {
		match := true
		for key, value := range metadata {
			if item.Metadata[key] != value {
				match = false
			}
		}
		if !match || item.Status.State == StateTerminated {
			continue
		}
		result = append(result, sandbox.ManagedObject{Type: TypeSandbox, Name: item.ID, Labels: maps.Clone(item.Metadata), CreatedAt: item.CreatedAt})
	}
	return result, nil
}

// DeleteManaged deletes one listed sandbox of this instance.
func (p *Provider) DeleteManaged(ctx context.Context, object sandbox.ManagedObject) error {
	if object.Type != TypeSandbox || object.Labels[sandbox.LabelManagedBy] != sandbox.ManagedByValue || object.Labels[sandbox.LabelInstance] != p.coding.InstanceID {
		return nil
	}
	p.forgetExecd(object.Name)
	if err := p.client.Delete(ctx, object.Name); err != nil {
		return wrap("delete sandbox "+object.Name, err)
	}
	return nil
}

// Volumes and native snapshots are not supported: use the archive strategy.
func (p *Provider) EnsureVolume(context.Context, sandbox.VolumeSpec) (sandbox.VolumeStatus, error) {
	return sandbox.VolumeStatus{}, fmt.Errorf("opensandbox: %w: workspace volumes (use the archive strategy)", sandbox.ErrUnsupported)
}

func (p *Provider) RestoreWorkspace(context.Context, sandbox.RestoreSpec) (sandbox.VolumeStatus, error) {
	return sandbox.VolumeStatus{}, fmt.Errorf("opensandbox: %w: VolumeSnapshots (use the archive strategy)", sandbox.ErrUnsupported)
}

func (p *Provider) VolumeStatus(context.Context, string) (sandbox.VolumeStatus, error) {
	return sandbox.VolumeStatus{}, fmt.Errorf("opensandbox: %w: workspace volumes", sandbox.ErrUnsupported)
}

func (p *Provider) DeleteVolume(context.Context, string) error { return nil }

func (p *Provider) CreateSnapshot(context.Context, sandbox.SnapshotSpec) (sandbox.SnapshotStatus, error) {
	return sandbox.SnapshotStatus{}, fmt.Errorf("opensandbox: %w: VolumeSnapshots (use the archive strategy)", sandbox.ErrUnsupported)
}

func (p *Provider) SnapshotStatus(context.Context, string) (sandbox.SnapshotStatus, error) {
	return sandbox.SnapshotStatus{}, fmt.Errorf("opensandbox: %w: VolumeSnapshots", sandbox.ErrUnsupported)
}

func (p *Provider) DeleteSnapshot(context.Context, string) error { return nil }

// Preflight checks the server, the API key and the settings OpenSandbox
// owns server-side.
func (p *Provider) Preflight(ctx context.Context) []sandbox.PreflightResult {
	results := []sandbox.PreflightResult{}
	if err := p.client.Health(ctx); err != nil {
		return append(results, sandbox.PreflightResult{Name: "opensandbox", OK: false, Message: "cannot reach the OpenSandbox server at " + p.os.URL + ": " + err.Error()})
	}
	results = append(results, sandbox.PreflightResult{Name: "opensandbox", OK: true, Message: "OpenSandbox server at " + p.os.URL + " is reachable"})
	if _, err := p.client.List(ctx, p.filter(sandbox.ListFilter{Kind: "preflight"})); err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusUnauthorized {
			results = append(results, sandbox.PreflightResult{Name: "auth", OK: false, Message: "the OpenSandbox API key is missing or wrong (FLOW_CODING_OPENSANDBOX_API_KEY): " + err.Error()})
		} else {
			results = append(results, sandbox.PreflightResult{Name: "auth", OK: false, Message: "listing sandboxes failed: " + err.Error()})
		}
	} else {
		results = append(results, sandbox.PreflightResult{Name: "auth", OK: true, Message: "the API key can list sandboxes"})
	}
	if host := sandbox.RuntimeHost(p.coding.RuntimeURL); host == "localhost" || strings.HasPrefix(host, "127.") || host == "::1" {
		results = append(results, sandbox.PreflightResult{Name: "runtime-url", OK: false, Message: "FLOW_CODING_RUNTIME_URL points at " + host + ", which sandboxes cannot reach; use Flow's in-cluster Service URL"})
	}
	results = append(results, sandbox.PreflightResult{Name: "runtime", OK: true, Warning: true,
		Message: "the container runtime (gVisor, Kata) is configured in OpenSandbox's [secure_runtime] settings, not by Flow"})
	switch {
	case p.os.EgressPolicy == config.OpenSandboxEgressProxy:
		results = append(results, sandbox.PreflightResult{Name: "egress", OK: true, Message: "sandboxes get a deny-by-default OpenSandbox network policy that only allows Flow and the egress proxy (needs the OpenSandbox egress sidecar; use [egress] mode dns+nft to also block direct IPs)"})
	case p.coding.Egress.ProxyURL == "":
		results = append(results, sandbox.PreflightResult{Name: "egress", OK: true, Warning: true, Message: "no egress proxy and no OpenSandbox network policy: sandboxes have the network access OpenSandbox's namespace allows"})
	default:
		results = append(results, sandbox.PreflightResult{Name: "egress", OK: true, Warning: true, Message: "sandboxes use the egress proxy through HTTP(S)_PROXY only; set FLOW_CODING_OPENSANDBOX_EGRESS_POLICY=proxy or a NetworkPolicy to enforce it"})
	}
	return results
}
