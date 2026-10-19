package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// CodingConfig configures coding sessions (docs/coding-sessions-design.md §8).
// Every value comes from a FLOW_CODING_* variable (FLOW_GITHUB_APP_* for the
// GitHub App, which is a workspace integration beyond coding sessions).
type CodingConfig struct {
	// Enabled turns the coding-session runtime on (FLOW_CODING_ENABLED).
	// Workspaces still need the "coding-sessions" feature flag.
	Enabled bool
	// Provider is the sandbox provider: disabled | fake | docker | kubernetes |
	// local | opensandbox.
	Provider string
	// RuntimeURL is the base URL sandboxes call Flow at (runtime API and LLM
	// proxy); defaults to FLOW_APP_URL.
	RuntimeURL string
	// RuntimeAddr optionally serves only /internal/coding-runtime/* and
	// /internal/llm-proxy/* on a separate listener (e.g. ":8090"), so the
	// sandbox NetworkPolicy can allow just that port. Empty = main listener.
	RuntimeAddr string
	// InstanceID labels every object Flow creates (flow.dev/instance) so orphan
	// GC only touches this installation's objects.
	InstanceID string
	// SecretKey is the base64 32-byte AES-GCM key for environment secrets,
	// signing keys and minted tokens at rest; defaults to FLOW_CONNECTOR_SECRET_KEY.
	SecretKey string

	Image             string
	ImagePullPolicy   string
	ImagePullSecrets  []string
	BranchPrefix      string
	PRMention         string
	BrowserEnabled    bool
	DebugTerminal     bool
	ArtifactMaxBytes  int64
	MaxSessions       int
	WorkspaceSessions int
	ReconcileInterval time.Duration
	GCInterval        time.Duration
	OrphanGrace       time.Duration
	Sizes             map[string]SandboxSize
	WorkspaceSize     string
	CPURequestRatio   float64
	Regions           []string
	RegionLabel       string
	DefaultRegion     string
	WarmPool          map[string]int
	WarmPoolMaxAge    time.Duration
	Timeouts          CodingTimeouts
	Retries           CodingRetries
	Snapshot          CodingSnapshotConfig
	Kubernetes        KubernetesSandboxConfig
	Docker            DockerSandboxConfig
	Local             LocalSandboxConfig
	OpenSandbox       OpenSandboxConfig
	Egress            CodingEgressConfig
	LLM               CodingLLMConfig
	RuntimePricesUSD  map[string]float64 // per sandbox-hour by size
	GitHubApp         GitHubAppConfig
	GitLab            CodingGitLabConfig
}

// SandboxSize is one size's limits. CPU and memory are Kubernetes quantities.
type SandboxSize struct {
	CPU              string
	Memory           string
	EphemeralStorage string
	PIDs             int64
}

// CodingTimeouts are the per-phase deadlines (design §4).
type CodingTimeouts struct {
	Queue         time.Duration
	Approval      time.Duration
	Provision     time.Duration
	Start         time.Duration
	Setup         time.Duration
	Turn          time.Duration
	Heartbeat     time.Duration
	Publish       time.Duration
	Idle          time.Duration
	StopGrace     time.Duration
	MaxSession    time.Duration
	DebugTerminal time.Duration
	TokenGrace    time.Duration
}

// CodingRetries caps retries of the idempotent phases.
type CodingRetries struct {
	Provision int
	Start     int
	Publish   int
	Snapshot  int
}

// CodingSnapshotConfig selects how prepared workspaces are stored.
type CodingSnapshotConfig struct {
	Strategy            string // archive | volumesnapshot
	VolumeSnapshotClass string
	ArchivePrefix       string
	ArchiveMaxBytes     int64
	Timeout             time.Duration
	TTL                 time.Duration
	Retain              int
	RefreshInterval     time.Duration
}

// KubernetesSandboxConfig configures the kubernetes provider.
type KubernetesSandboxConfig struct {
	Kubeconfig          string
	Namespace           string
	RuntimeClassName    string
	HostUsers           bool
	StorageClass        string
	WorkspaceVolume     string // auto | pvc | emptyDir
	NodeSelector        map[string]string
	Tolerations         []string // key[=value]:Effect, as in kubectl taint syntax
	ServiceAccount      string
	PriorityClassName   string
	ExtraLabels         map[string]string
	ExtraAnnotations    map[string]string
	ManageNetworkPolicy bool
	FlowNamespace       string
	FlowPodSelector     map[string]string
	ProxyNamespace      string
	ProxyPodSelector    map[string]string
	QPS                 float64
	Burst               int
}

// DockerSandboxConfig configures the docker provider (local dev and CI).
type DockerSandboxConfig struct {
	Host    string
	Network string
	Runtime string
}

// LocalSandboxConfig configures the development-only local provider, which
// runs each sandbox as a host process through an isolating launcher.
type LocalSandboxConfig struct {
	Command string // FLOW_CODING_LOCAL_COMMAND: absolute path of the launcher
	Root    string // FLOW_CODING_LOCAL_ROOT: sandbox and workspace directories
}

// CodingEgressConfig configures the egress proxy that sandboxes must use.
type CodingEgressConfig struct {
	ProxyURL         string
	Allowlist        []string
	DefaultAllowlist bool
	NoProxy          []string
	ACLSecret        string
}

// CodingLLMConfig configures the LLM proxy upstreams and model catalog.
type CodingLLMConfig struct {
	AnthropicBaseURL string
	AnthropicAPIKey  string
	OpenAIBaseURL    string
	OpenAIAPIKey     string
	// OpenAIWireAPI is the protocol the OpenAI upstream speaks: "responses"
	// (default) or "chat" for Chat Completions-only providers, in which case
	// the proxy translates Codex's Responses requests (llmproxy/chatwire.go).
	OpenAIWireAPI     string
	OpenSourceBaseURL string
	OpenSourceAPIKey  string
	// Models per harness; the first entry is what Auto resolves to.
	ClaudeCodeModels []string
	CodexModels      []string
	OpenSourceModels []string
	// Prices maps a model to USD per million tokens.
	Prices map[string]ModelPrice
	// ModelInfo is optional display metadata (label, picker group, one-line
	// description) per model id, from FLOW_CODING_MODEL_INFO.
	ModelInfo    map[string]ModelInfo
	modelInfoErr error
	Timeout      time.Duration
}

// OpenAI upstream wire APIs (FLOW_CODING_LLM_OPENAI_WIRE_API).
const (
	OpenAIWireAPIResponses = "responses"
	OpenAIWireAPIChat      = "chat"
)

// Model picker groups (ModelInfo.Group).
const (
	ModelGroupFrontier = "frontier"
	ModelGroupOther    = "other"
)

// ModelInfo is display metadata for a catalog model. Empty fields fall back
// to Flow's built-in defaults.
type ModelInfo struct {
	Label       string `json:"label,omitempty"`
	Group       string `json:"group,omitempty"` // frontier | other
	Description string `json:"description,omitempty"`
}

// ModelPrice is USD per million tokens.
type ModelPrice struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
}

// GitHubAppConfig is the GitHub App Flow uses for code access and PRs.
type GitHubAppConfig struct {
	AppID         string
	Slug          string
	ClientID      string
	ClientSecret  string
	PrivateKey    string
	WebhookSecret string
	Host          string // github.com or a GitHub Enterprise Server host
	APIURL        string // derived: https://api.github.com or https://<host>/api/v3
}

// Configured reports whether the App credentials are present.
func (c GitHubAppConfig) Configured() bool {
	return c.AppID != "" && c.PrivateKey != "" && c.WebhookSecret != ""
}

// CodingGitLabConfig configures sandbox credentials for GitLab.
type CodingGitLabConfig struct {
	// CredentialMode: project-token (default) | oauth | connection.
	CredentialMode string
	TokenScopes    []string
}

// Coding providers.
const (
	CodingProviderDisabled   = "disabled"
	CodingProviderFake       = "fake"
	CodingProviderDocker     = "docker"
	CodingProviderKubernetes = "kubernetes"
	// CodingProviderLocal runs sandboxes as local processes (development only).
	CodingProviderLocal = "local"
)

func loadCoding(appURL string) CodingConfig {
	ghHost := strings.ToLower(value("FLOW_GITHUB_APP_HOST", "github.com"))
	ghAPI := "https://api.github.com"
	if ghHost != "github.com" {
		ghAPI = "https://" + ghHost + "/api/v3"
	}
	c := CodingConfig{
		Enabled:           boolean("FLOW_CODING_ENABLED", false),
		Provider:          strings.ToLower(value("FLOW_CODING_PROVIDER", CodingProviderDisabled)),
		RuntimeURL:        strings.TrimRight(value("FLOW_CODING_RUNTIME_URL", appURL), "/"),
		RuntimeAddr:       value("FLOW_CODING_RUNTIME_ADDR", ""),
		InstanceID:        value("FLOW_CODING_INSTANCE_ID", "flow"),
		SecretKey:         secretWithFallback("FLOW_CODING_SECRET_KEY", "FLOW_CONNECTOR_SECRET_KEY"),
		Image:             value("FLOW_CODING_IMAGE", "ghcr.io/leozhengliu-pixel/flow-sandbox:latest"),
		ImagePullPolicy:   value("FLOW_CODING_IMAGE_PULL_POLICY", "IfNotPresent"),
		ImagePullSecrets:  csvRaw(value("FLOW_CODING_IMAGE_PULL_SECRETS", "")),
		BranchPrefix:      value("FLOW_CODING_BRANCH_PREFIX", "flow/"),
		PRMention:         value("FLOW_CODING_PR_MENTION", "@flow"),
		BrowserEnabled:    !switchedOff("FLOW_CODING_BROWSER"),
		DebugTerminal:     !switchedOff("FLOW_CODING_DEBUG_TERMINAL"),
		ArtifactMaxBytes:  int64(integer("FLOW_CODING_ARTIFACT_MAX_BYTES", 10<<20)),
		MaxSessions:       integer("FLOW_CODING_MAX_SESSIONS", 20),
		WorkspaceSessions: integer("FLOW_CODING_WORKSPACE_SESSIONS", 5),
		ReconcileInterval: duration("FLOW_CODING_RECONCILE_INTERVAL", 5*time.Second),
		GCInterval:        duration("FLOW_CODING_GC_INTERVAL", 5*time.Minute),
		OrphanGrace:       duration("FLOW_CODING_ORPHAN_GRACE", 10*time.Minute),
		Sizes: map[string]SandboxSize{
			"small":  parseSandboxSize(value("FLOW_CODING_SIZE_SMALL", "cpu=1,memory=8Gi,ephemeral=20Gi,pids=4096")),
			"medium": parseSandboxSize(value("FLOW_CODING_SIZE_MEDIUM", "cpu=2,memory=16Gi,ephemeral=40Gi,pids=8192")),
			"large":  parseSandboxSize(value("FLOW_CODING_SIZE_LARGE", "cpu=4,memory=32Gi,ephemeral=80Gi,pids=16384")),
		},
		WorkspaceSize:   value("FLOW_CODING_WORKSPACE_SIZE", "20Gi"),
		CPURequestRatio: float(value("FLOW_CODING_CPU_REQUEST_RATIO", "1"), 1),
		Regions:         csvRaw(value("FLOW_CODING_REGIONS", "")),
		RegionLabel:     value("FLOW_CODING_REGION_LABEL", "topology.kubernetes.io/region"),
		DefaultRegion:   value("FLOW_CODING_DEFAULT_REGION", ""),
		WarmPool:        intMap(value("FLOW_CODING_WARM_POOL", "")),
		WarmPoolMaxAge:  duration("FLOW_CODING_WARM_POOL_MAX_AGE", time.Hour),
		Timeouts: CodingTimeouts{
			Queue:         duration("FLOW_CODING_QUEUE_TIMEOUT", 30*time.Minute),
			Approval:      duration("FLOW_CODING_APPROVAL_TIMEOUT", 7*24*time.Hour),
			Provision:     duration("FLOW_CODING_PROVISION_TIMEOUT", 5*time.Minute),
			Start:         duration("FLOW_CODING_START_TIMEOUT", 10*time.Minute),
			Setup:         duration("FLOW_CODING_SETUP_TIMEOUT", 20*time.Minute),
			Turn:          duration("FLOW_CODING_TURN_TIMEOUT", time.Hour),
			Heartbeat:     duration("FLOW_CODING_HEARTBEAT_TIMEOUT", 2*time.Minute),
			Publish:       duration("FLOW_CODING_PUBLISH_TIMEOUT", 5*time.Minute),
			Idle:          duration("FLOW_CODING_IDLE_TTL", 15*time.Minute),
			StopGrace:     duration("FLOW_CODING_STOP_GRACE", time.Minute),
			MaxSession:    duration("FLOW_CODING_MAX_SESSION_DURATION", 4*time.Hour),
			DebugTerminal: duration("FLOW_CODING_DEBUG_TERMINAL_TTL", 20*time.Minute),
			TokenGrace:    duration("FLOW_CODING_TOKEN_GRACE", 5*time.Minute),
		},
		Retries: CodingRetries{
			Provision: integer("FLOW_CODING_PROVISION_RETRIES", 3),
			Start:     integer("FLOW_CODING_START_RETRIES", 2),
			Publish:   integer("FLOW_CODING_PUBLISH_RETRIES", 5),
			Snapshot:  integer("FLOW_CODING_SNAPSHOT_RETRIES", 1),
		},
		Snapshot: CodingSnapshotConfig{
			Strategy:            strings.ToLower(value("FLOW_CODING_SNAPSHOT_STRATEGY", "archive")),
			VolumeSnapshotClass: value("FLOW_CODING_VOLUME_SNAPSHOT_CLASS", ""),
			ArchivePrefix:       value("FLOW_CODING_ARCHIVE_PREFIX", "coding-snapshots"),
			ArchiveMaxBytes:     integer64("FLOW_CODING_ARCHIVE_MAX_BYTES", 10<<30),
			Timeout:             duration("FLOW_CODING_SNAPSHOT_TIMEOUT", 30*time.Minute),
			TTL:                 duration("FLOW_CODING_SNAPSHOT_TTL", 7*24*time.Hour),
			Retain:              integer("FLOW_CODING_SNAPSHOT_RETAIN", 2),
			RefreshInterval:     duration("FLOW_CODING_SNAPSHOT_REFRESH_INTERVAL", 24*time.Hour),
		},
		Kubernetes: KubernetesSandboxConfig{
			Kubeconfig:          value("FLOW_CODING_K8S_KUBECONFIG", ""),
			Namespace:           value("FLOW_CODING_K8S_NAMESPACE", "flow-sandboxes"),
			RuntimeClassName:    value("FLOW_CODING_K8S_RUNTIME_CLASS", ""),
			HostUsers:           boolean("FLOW_CODING_K8S_HOST_USERS", true),
			StorageClass:        value("FLOW_CODING_K8S_STORAGE_CLASS", ""),
			WorkspaceVolume:     value("FLOW_CODING_K8S_WORKSPACE_VOLUME", "auto"),
			NodeSelector:        stringMap(value("FLOW_CODING_K8S_NODE_SELECTOR", "")),
			Tolerations:         csvRaw(value("FLOW_CODING_K8S_TOLERATIONS", "")),
			ServiceAccount:      value("FLOW_CODING_K8S_SERVICE_ACCOUNT", "flow-sandbox"),
			PriorityClassName:   value("FLOW_CODING_K8S_PRIORITY_CLASS", ""),
			ExtraLabels:         stringMap(value("FLOW_CODING_K8S_EXTRA_LABELS", "")),
			ExtraAnnotations:    stringMap(value("FLOW_CODING_K8S_EXTRA_ANNOTATIONS", "")),
			ManageNetworkPolicy: boolean("FLOW_CODING_K8S_NETWORK_POLICY", true),
			FlowNamespace:       value("FLOW_CODING_K8S_FLOW_NAMESPACE", "flow"),
			FlowPodSelector:     stringMap(value("FLOW_CODING_K8S_FLOW_POD_SELECTOR", "app.kubernetes.io/name=flow")),
			ProxyNamespace:      value("FLOW_CODING_K8S_PROXY_NAMESPACE", "flow-sandboxes"),
			ProxyPodSelector:    stringMap(value("FLOW_CODING_K8S_PROXY_POD_SELECTOR", "app.kubernetes.io/name=flow-egress")),
			QPS:                 float(value("FLOW_CODING_K8S_QPS", "20"), 20),
			Burst:               integer("FLOW_CODING_K8S_BURST", 40),
		},
		Docker: DockerSandboxConfig{
			Host:    value("FLOW_CODING_DOCKER_HOST", "unix:///var/run/docker.sock"),
			Network: value("FLOW_CODING_DOCKER_NETWORK", "flow-sandbox"),
			Runtime: value("FLOW_CODING_DOCKER_RUNTIME", ""),
		},
		Local: LocalSandboxConfig{
			Command: value("FLOW_CODING_LOCAL_COMMAND", ""),
			Root:    value("FLOW_CODING_LOCAL_ROOT", ""),
		},
		OpenSandbox: loadOpenSandbox(),
		Egress: CodingEgressConfig{
			ProxyURL:         value("FLOW_CODING_EGRESS_PROXY_URL", ""),
			Allowlist:        csv(value("FLOW_CODING_EGRESS_ALLOWLIST", "")),
			DefaultAllowlist: !switchedOff("FLOW_CODING_EGRESS_DEFAULT_ALLOWLIST"),
			NoProxy:          csvRaw(value("FLOW_CODING_EGRESS_NO_PROXY", "")),
			ACLSecret:        secret("FLOW_CODING_EGRESS_ACL_SECRET"),
		},
		LLM: CodingLLMConfig{
			AnthropicBaseURL:  strings.TrimRight(value("FLOW_CODING_LLM_ANTHROPIC_BASE_URL", "https://api.anthropic.com"), "/"),
			AnthropicAPIKey:   secret("FLOW_CODING_LLM_ANTHROPIC_API_KEY"),
			OpenAIBaseURL:     strings.TrimRight(value("FLOW_CODING_LLM_OPENAI_BASE_URL", "https://api.openai.com/v1"), "/"),
			OpenAIAPIKey:      secret("FLOW_CODING_LLM_OPENAI_API_KEY"),
			OpenAIWireAPI:     strings.ToLower(strings.TrimSpace(value("FLOW_CODING_LLM_OPENAI_WIRE_API", OpenAIWireAPIResponses))),
			OpenSourceBaseURL: strings.TrimRight(value("FLOW_CODING_LLM_OPENSOURCE_BASE_URL", ""), "/"),
			OpenSourceAPIKey:  secret("FLOW_CODING_LLM_OPENSOURCE_API_KEY"),
			ClaudeCodeModels:  csvRaw(value("FLOW_CODING_MODELS_CLAUDE_CODE", "claude-sonnet-4-5,claude-opus-4-1")),
			CodexModels:       csvRaw(value("FLOW_CODING_MODELS_CODEX", "gpt-5-codex,gpt-5")),
			OpenSourceModels:  csvRaw(value("FLOW_CODING_MODELS_OPEN_SOURCE", "")),
			Prices:            modelPrices(value("FLOW_CODING_MODEL_PRICES", "")),
			Timeout:           duration("FLOW_CODING_LLM_TIMEOUT", 10*time.Minute),
		},
		RuntimePricesUSD: floatMap(value("FLOW_CODING_RUNTIME_PRICES", "small=0.10,medium=0.20,large=0.40")),
		GitHubApp: GitHubAppConfig{
			AppID: value("FLOW_GITHUB_APP_ID", ""), Slug: value("FLOW_GITHUB_APP_SLUG", ""),
			ClientID: value("FLOW_GITHUB_APP_CLIENT_ID", ""), ClientSecret: secret("FLOW_GITHUB_APP_CLIENT_SECRET"),
			PrivateKey: secret("FLOW_GITHUB_APP_PRIVATE_KEY"), WebhookSecret: secret("FLOW_GITHUB_APP_WEBHOOK_SECRET"),
			Host: ghHost, APIURL: strings.TrimRight(value("FLOW_GITHUB_APP_API_URL", ghAPI), "/"),
		},
		GitLab: CodingGitLabConfig{
			CredentialMode: strings.ToLower(value("FLOW_CODING_GITLAB_CREDENTIAL_MODE", "project-token")),
			TokenScopes:    csv(value("FLOW_CODING_GITLAB_TOKEN_SCOPES", "read_repository,write_repository,read_api")),
		},
	}
	c.LLM.ModelInfo, c.LLM.modelInfoErr = modelInfo(value("FLOW_CODING_MODEL_INFO", ""))
	return c
}

// Validate checks the coding configuration. It is only strict when coding
// sessions are enabled, so an unused block never blocks startup.
func (c CodingConfig) Validate() error {
	switch c.Provider {
	case CodingProviderDisabled, CodingProviderFake, CodingProviderDocker, CodingProviderKubernetes, CodingProviderLocal, CodingProviderOpenSandbox:
	default:
		return fmt.Errorf("FLOW_CODING_PROVIDER must be disabled, fake, docker, kubernetes, local or opensandbox")
	}
	if !c.Enabled {
		return nil
	}
	if c.Provider == CodingProviderDisabled {
		return fmt.Errorf("FLOW_CODING_PROVIDER is required when FLOW_CODING_ENABLED=true")
	}
	if parsed, err := url.Parse(c.RuntimeURL); err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("FLOW_CODING_RUNTIME_URL must be an http(s) URL")
	}
	if err := c.LLM.validateModelInfo(); err != nil {
		return err
	}
	switch c.LLM.OpenAIWireAPI {
	case "", OpenAIWireAPIResponses, OpenAIWireAPIChat:
	default:
		return fmt.Errorf("FLOW_CODING_LLM_OPENAI_WIRE_API must be responses or chat")
	}
	if c.SecretKey == "" {
		return fmt.Errorf("FLOW_CODING_SECRET_KEY (or FLOW_CONNECTOR_SECRET_KEY) is required when coding sessions are enabled")
	}
	switch c.Snapshot.Strategy {
	case "archive", "volumesnapshot":
	default:
		return fmt.Errorf("FLOW_CODING_SNAPSHOT_STRATEGY must be archive or volumesnapshot")
	}
	if c.Snapshot.Strategy == "volumesnapshot" && c.Provider != CodingProviderKubernetes && c.Provider != CodingProviderFake {
		return fmt.Errorf("FLOW_CODING_SNAPSHOT_STRATEGY=volumesnapshot requires FLOW_CODING_PROVIDER=kubernetes")
	}
	switch c.Kubernetes.WorkspaceVolume {
	case "auto", "pvc", "emptyDir":
	default:
		return fmt.Errorf("FLOW_CODING_K8S_WORKSPACE_VOLUME must be auto, pvc or emptyDir")
	}
	if c.Provider == CodingProviderLocal && c.Local.Command == "" {
		return fmt.Errorf("FLOW_CODING_LOCAL_COMMAND is required with FLOW_CODING_PROVIDER=local")
	}
	if c.Provider == CodingProviderOpenSandbox {
		if err := c.OpenSandbox.Validate(c.Sizes); err != nil {
			return err
		}
	}
	if c.Snapshot.Strategy == "volumesnapshot" && c.Kubernetes.WorkspaceVolume == "emptyDir" {
		return fmt.Errorf("FLOW_CODING_SNAPSHOT_STRATEGY=volumesnapshot needs PVC workspaces (FLOW_CODING_K8S_WORKSPACE_VOLUME=auto or pvc)")
	}
	for name, size := range c.Sizes {
		if size.CPU == "" || size.Memory == "" {
			return fmt.Errorf("FLOW_CODING_SIZE_%s must set cpu and memory", strings.ToUpper(name))
		}
	}
	if c.CPURequestRatio <= 0 || c.CPURequestRatio > 1 {
		return fmt.Errorf("FLOW_CODING_CPU_REQUEST_RATIO must be in (0, 1]")
	}
	for size := range c.WarmPool {
		if _, ok := c.Sizes[size]; !ok {
			return fmt.Errorf("FLOW_CODING_WARM_POOL names unknown size %q", size)
		}
	}
	if c.DefaultRegion != "" && len(c.Regions) > 0 && !contains(c.Regions, c.DefaultRegion) {
		return fmt.Errorf("FLOW_CODING_DEFAULT_REGION must be one of FLOW_CODING_REGIONS")
	}
	if c.MaxSessions < 1 || c.WorkspaceSessions < 1 {
		return fmt.Errorf("FLOW_CODING_MAX_SESSIONS and FLOW_CODING_WORKSPACE_SESSIONS must be at least 1")
	}
	if c.ReconcileInterval < time.Second {
		return fmt.Errorf("FLOW_CODING_RECONCILE_INTERVAL must be at least 1s")
	}
	switch c.GitLab.CredentialMode {
	case "project-token", "oauth", "connection":
	default:
		return fmt.Errorf("FLOW_CODING_GITLAB_CREDENTIAL_MODE must be project-token, oauth or connection")
	}
	if c.Egress.ProxyURL != "" {
		if parsed, err := url.Parse(c.Egress.ProxyURL); err != nil || parsed.Host == "" {
			return fmt.Errorf("FLOW_CODING_EGRESS_PROXY_URL must be a URL such as http://flow-egress:3128")
		}
	}
	if c.GitHubApp.AppID != "" && !c.GitHubApp.Configured() {
		return fmt.Errorf("FLOW_GITHUB_APP_PRIVATE_KEY and FLOW_GITHUB_APP_WEBHOOK_SECRET are required with FLOW_GITHUB_APP_ID")
	}
	return nil
}

// ModelsFor returns the model catalog of a harness; the first is Auto's pick.
func (c CodingLLMConfig) ModelsFor(harness string) []string {
	switch harness {
	case "claudeCode":
		return c.ClaudeCodeModels
	case "openSource":
		return c.OpenSourceModels
	default:
		return c.CodexModels
	}
}

func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

// parseSandboxSize reads "cpu=1,memory=8Gi,ephemeral=20Gi,pids=4096".
func parseSandboxSize(raw string) SandboxSize {
	fields := stringMap(raw)
	pids, _ := strconv.ParseInt(fields["pids"], 10, 64)
	return SandboxSize{CPU: fields["cpu"], Memory: fields["memory"], EphemeralStorage: fields["ephemeral"], PIDs: pids}
}

// stringMap reads "k=v,k2=v2".
func stringMap(raw string) map[string]string {
	result := map[string]string{}
	for _, item := range strings.Split(raw, ",") {
		key, val, ok := strings.Cut(strings.TrimSpace(item), "=")
		if ok && strings.TrimSpace(key) != "" {
			result[strings.TrimSpace(key)] = strings.TrimSpace(val)
		}
	}
	return result
}

func intMap(raw string) map[string]int {
	result := map[string]int{}
	for key, val := range stringMap(raw) {
		if n, err := strconv.Atoi(val); err == nil && n >= 0 {
			result[strings.ToLower(key)] = n
		}
	}
	return result
}

func floatMap(raw string) map[string]float64 {
	result := map[string]float64{}
	for key, val := range stringMap(raw) {
		if n, err := strconv.ParseFloat(val, 64); err == nil && n >= 0 {
			result[strings.ToLower(key)] = n
		}
	}
	return result
}

func float(raw string, fallback float64) float64 {
	n, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return fallback
	}
	return n
}

// modelInfo reads a JSON object {"model":{"label":"…","group":"frontier","description":"…"}}.
func modelInfo(raw string) (map[string]ModelInfo, error) {
	result := map[string]ModelInfo{}
	if strings.TrimSpace(raw) == "" {
		return result, nil
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return map[string]ModelInfo{}, err
	}
	return result, nil
}

func (c CodingLLMConfig) validateModelInfo() error {
	if c.modelInfoErr != nil {
		return fmt.Errorf("FLOW_CODING_MODEL_INFO must be a JSON object of model ids: %v", c.modelInfoErr)
	}
	for model, info := range c.ModelInfo {
		switch info.Group {
		case "", ModelGroupFrontier, ModelGroupOther:
		default:
			return fmt.Errorf("FLOW_CODING_MODEL_INFO group of %q must be frontier or other", model)
		}
	}
	return nil
}

// modelPrices reads a JSON object {"model":{"input":3,"output":15,...}}.
func modelPrices(raw string) map[string]ModelPrice {
	result := map[string]ModelPrice{}
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &result)
	}
	return result
}
