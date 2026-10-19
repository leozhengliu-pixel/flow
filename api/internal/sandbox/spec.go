package sandbox

import (
	"maps"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/config"
)

// Extra runner environment (track A additions to the contract above).
const (
	// EnvArchiveURL is the snapshot archive the restore step downloads.
	EnvArchiveURL = "FLOW_ARCHIVE_URL"
	// EnvPIDsLimit is the per-size process limit the runner applies with
	// `ulimit -u` before spawning the harness (design §9: PID limits are not
	// a Pod field).
	EnvPIDsLimit = "FLOW_PIDS_LIMIT"
	// EnvSandboxSize is the sandbox size name.
	EnvSandboxSize = "FLOW_SANDBOX_SIZE"
	// RestoreExitCode is the exit status of a failed restore step when a
	// provider without init containers (docker) runs it inline.
	RestoreExitCode = 86
	// RunnerUID is the uid and gid sandboxes run as.
	RunnerUID = 1000
)

// Mode is the runner mode of a sandbox kind (FLOW_RUNNER_MODE and the
// `flow-runner <mode>` argument).
func Mode(kind Kind) string {
	switch kind {
	case KindSnapshot:
		return "snapshot"
	case KindDebug:
		return "debug"
	case KindWarm:
		return "warm"
	default:
		return "session"
	}
}

// EnvVar is one environment variable.
type EnvVar struct{ Name, Value string }

// reservedEnv reports variables that environment configuration may not set
// because the platform owns them.
func reservedEnv(name string) bool {
	upper := strings.ToUpper(name)
	if strings.HasPrefix(upper, "FLOW_") || strings.HasPrefix(upper, "LINEAR_") {
		return true
	}
	switch upper {
	case "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY", "HOME":
		return true
	}
	return false
}

// RunnerEnv is the runner container's environment, in a deterministic order:
// the environment's own (non-secret) variables, then the platform variables,
// which always win. The token is never an env var; it is a file at
// TokenMountPath.
func RunnerEnv(c config.CodingConfig, spec SandboxSpec, mode string) []EnvVar {
	names := slices.Sorted(maps.Keys(spec.Env))
	env := make([]EnvVar, 0, len(names)+20)
	for _, name := range names {
		if name == "" || reservedEnv(name) {
			continue
		}
		env = append(env, EnvVar{name, spec.Env[name]})
	}
	env = append(env,
		EnvVar{EnvRuntimeURL, c.RuntimeURL},
		EnvVar{EnvTokenFile, TokenMountPath},
		EnvVar{EnvSandboxID, spec.ID},
		EnvVar{EnvMode, mode},
		EnvVar{EnvFlowSandbox, "1"},
		EnvVar{EnvLinearCompat, "1"},
		EnvVar{"HOME", HomePath},
	)
	if spec.Resources.Size != "" {
		env = append(env, EnvVar{EnvSandboxSize, spec.Resources.Size})
	}
	if spec.Resources.PIDs > 0 {
		env = append(env, EnvVar{EnvPIDsLimit, strconv.FormatInt(spec.Resources.PIDs, 10)})
	}
	if spec.Kind == KindSnapshot {
		env = append(env, EnvVar{EnvFlowSnapshot, "1"}, EnvVar{EnvSnapshotting, "1"})
	}
	if spec.Workspace.ArchiveURL != "" {
		env = append(env, EnvVar{EnvArchiveURL, spec.Workspace.ArchiveURL})
	}
	if spec.ProxyURL != "" {
		for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
			env = append(env, EnvVar{name, spec.ProxyURL})
		}
	}
	if noProxy := NoProxy(c, spec); noProxy != "" {
		env = append(env, EnvVar{"NO_PROXY", noProxy}, EnvVar{"no_proxy", noProxy})
	}
	return env
}

// RestoreEnv is the restore step's environment (it talks to Flow directly,
// never through the egress proxy).
func RestoreEnv(c config.CodingConfig, spec SandboxSpec) []EnvVar {
	return []EnvVar{
		{EnvRuntimeURL, c.RuntimeURL},
		{EnvTokenFile, TokenMountPath},
		{EnvSandboxID, spec.ID},
		{EnvMode, "restore"},
		{EnvArchiveURL, spec.Workspace.ArchiveURL},
		{"HOME", HomePath},
	}
}

// NoProxy is the NO_PROXY list: loopback, the Flow runtime host (always, so
// runtime and LLM proxy calls never go through the egress proxy), the
// configured extras and the spec's own.
func NoProxy(c config.CodingConfig, spec SandboxSpec) string {
	seen := map[string]bool{}
	list := []string{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			list = append(list, value)
		}
	}
	for _, value := range []string{"localhost", "127.0.0.1", "::1"} {
		add(value)
	}
	if host := RuntimeHost(c.RuntimeURL); host != "" {
		add(host)
	}
	for _, value := range c.Egress.NoProxy {
		add(value)
	}
	for _, value := range spec.NoProxy {
		add(value)
	}
	if spec.ProxyURL == "" && len(c.Egress.NoProxy) == 0 && len(spec.NoProxy) == 0 {
		// Without a proxy NO_PROXY is meaningless; keep the env minimal.
		return ""
	}
	return strings.Join(list, ",")
}

// RuntimeHost is the host name of a URL ("" when unparsable).
func RuntimeHost(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

// URLPort is the port of a URL, defaulting by scheme (0 when unknown).
func URLPort(raw string) int {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return 0
	}
	if port := parsed.Port(); port != "" {
		return parsePort(port)
	}
	switch parsed.Scheme {
	case "http":
		return 80
	case "https":
		return 443
	}
	return 0
}

// AddrPort is the port of a listen address such as ":8090" (0 when unset).
func AddrPort(addr string) int {
	if addr == "" {
		return 0
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	return parsePort(port)
}

// parsePort parses a TCP port, returning 0 for anything outside 0-65535.
func parsePort(port string) int {
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return 0
	}
	return int(n)
}

// DefaultDeadline is the lifetime of a sandbox kind when its record has no
// explicit deadline. Warm sandboxes must also cover the session they are
// later bound to, because a Pod's activeDeadlineSeconds cannot change.
func DefaultDeadline(c config.CodingConfig, kind Kind) time.Duration {
	switch kind {
	case KindDebug:
		return c.Timeouts.DebugTerminal
	case KindSnapshot:
		return c.Snapshot.Timeout
	case KindWarm:
		return c.WarmPoolMaxAge + c.Timeouts.MaxSession
	default:
		return c.Timeouts.MaxSession
	}
}

// ResourcesFor returns the configured resources of a size (falls back to
// medium, then to an empty spec).
func ResourcesFor(c config.CodingConfig, size string) Resources {
	if size == "" {
		size = "medium"
	}
	s, ok := c.Sizes[size]
	if !ok {
		s = c.Sizes["medium"]
	}
	return Resources{Size: size, CPU: s.CPU, Memory: s.Memory, EphemeralStorage: s.EphemeralStorage, PIDs: s.PIDs}
}

// mergeLabels returns base plus extra, where base always wins (platform
// labels cannot be overridden by configuration or callers).
func mergeLabels(base map[string]string, extras ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, extra := range extras {
		maps.Copy(out, extra)
	}
	maps.Copy(out, base)
	return out
}

// ObjectLabels are the labels every object of a sandbox carries.
func ObjectLabels(c config.CodingConfig, spec SandboxSpec) map[string]string {
	base := map[string]string{
		LabelManagedBy: ManagedByValue,
		LabelInstance:  c.InstanceID,
		LabelKind:      string(spec.Kind),
		LabelSandboxID: spec.ID,
	}
	if spec.SessionID != "" {
		base[LabelSessionID] = spec.SessionID
	}
	if spec.SnapshotID != "" {
		base[LabelSnapshotID] = spec.SnapshotID
	}
	if ws := WorkspaceLabel(spec.WorkspaceKey); ws != "" {
		base[LabelWorkspace] = ws
	}
	if spec.Resources.Size != "" {
		base[LabelSize] = spec.Resources.Size
	}
	if spec.Region != "" {
		base[LabelRegion] = spec.Region
	}
	return mergeLabels(base, spec.Labels)
}

// ManagedLabels are the labels of objects that belong to this Flow instance
// but not to one sandbox (volumes, snapshots, namespace policies).
func ManagedLabels(c config.CodingConfig, extra map[string]string) map[string]string {
	return mergeLabels(map[string]string{LabelManagedBy: ManagedByValue, LabelInstance: c.InstanceID}, extra)
}

// CompleteSpec fills what a spec leaves to configuration: the image, the
// size's resources (any field the spec does not set) and the deadline.
func CompleteSpec(c config.CodingConfig, spec SandboxSpec) SandboxSpec {
	if spec.Image == "" {
		spec.Image = c.Image
	}
	sized := ResourcesFor(c, spec.Resources.Size)
	if spec.Resources.Size == "" {
		spec.Resources.Size = sized.Size
	}
	if spec.Resources.CPU == "" {
		spec.Resources.CPU = sized.CPU
	}
	if spec.Resources.Memory == "" {
		spec.Resources.Memory = sized.Memory
	}
	if spec.Resources.EphemeralStorage == "" {
		spec.Resources.EphemeralStorage = sized.EphemeralStorage
	}
	if spec.Resources.PIDs == 0 {
		spec.Resources.PIDs = sized.PIDs
	}
	if spec.Deadline <= 0 {
		spec.Deadline = DefaultDeadline(c, spec.Kind)
	}
	return spec
}

// RunnerWrapUp is the time the runner needs after its harness stop grace
// (bootstrap limits.stopGraceSeconds = FLOW_CODING_STOP_GRACE) to commit and
// push work in progress and report the turn. Providers add it to the
// SIGTERM → SIGKILL grace so the kernel never kills the runner mid-push.
const RunnerWrapUp = 30 * time.Second

// TerminationGrace is the provider's SIGTERM → SIGKILL grace for a stop
// grace; forced stops (1s or less) stay forced.
func TerminationGrace(stopGrace time.Duration) time.Duration {
	if stopGrace <= time.Second {
		return stopGrace
	}
	return stopGrace + RunnerWrapUp
}
