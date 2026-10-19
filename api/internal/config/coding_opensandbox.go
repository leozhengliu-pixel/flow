package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

// CodingProviderOpenSandbox runs sandboxes through an OpenSandbox lifecycle
// server (https://github.com/alibaba/OpenSandbox, api/internal/sandbox/opensandbox).
const CodingProviderOpenSandbox = "opensandbox"

// DefaultOpenSandboxURL is the in-cluster Service of the opensandbox-server
// Helm chart (namespace opensandbox-system, Service port 80): Flow and
// OpenSandbox are expected to run in the same cluster.
const DefaultOpenSandboxURL = "http://opensandbox-server.opensandbox-system.svc.cluster.local"

// OpenSandbox execd access modes (FLOW_CODING_OPENSANDBOX_EXECD_ACCESS).
const (
	// OpenSandboxExecdProxy reaches execd through the lifecycle server's
	// /sandboxes/{id}/proxy/{port} route (one URL for NetworkPolicies).
	OpenSandboxExecdProxy = "proxy"
	// OpenSandboxExecdEndpoint resolves execd's endpoint with
	// GET /sandboxes/{id}/endpoints/44772 and calls it directly.
	OpenSandboxExecdEndpoint = "endpoint"
)

// OpenSandbox egress policies (FLOW_CODING_OPENSANDBOX_EGRESS_POLICY).
const (
	// OpenSandboxEgressOff sends no networkPolicy; sandboxes rely on Flow's
	// egress proxy env and the cluster's NetworkPolicies.
	OpenSandboxEgressOff = "off"
	// OpenSandboxEgressProxy sends a deny-by-default OpenSandbox networkPolicy
	// that only allows the Flow runtime host, the egress proxy host and
	// FLOW_CODING_OPENSANDBOX_EGRESS_ALLOW, so all other traffic must go
	// through Flow's egress proxy (which applies the allowlist and ACLs).
	OpenSandboxEgressProxy = "proxy"
)

// OpenSandboxConfig configures the opensandbox provider
// (FLOW_CODING_OPENSANDBOX_*).
type OpenSandboxConfig struct {
	// URL is the lifecycle server's base URL; "/v1" is appended unless present.
	URL string
	// APIKey is sent as OPEN-SANDBOX-API-KEY (secret; *_FILE supported).
	APIKey string
	// RequestTimeout bounds every lifecycle and execd call except create.
	RequestTimeout time.Duration
	// CreateTimeout bounds POST /sandboxes, which waits server-side until the
	// sandbox is running (image pulls included). Creates run in the
	// background, so this may exceed the reconcile pass.
	CreateTimeout time.Duration
	// Retries is how often an idempotent call is retried on 429/5xx/network errors.
	Retries int
	// CAFile adds a PEM CA bundle for an https URL; InsecureSkipVerify turns
	// certificate verification off (development only).
	CAFile             string
	InsecureSkipVerify bool
	// ExtraMetadata is added to every sandbox's metadata (Flow's keys win).
	ExtraMetadata map[string]string
	// Extensions is passed through as the create request's extensions.
	Extensions map[string]string
	// Pools maps a size to an OpenSandbox Pool (extensions.poolRef); pooled
	// sizes take image and resources from the Pool template.
	Pools map[string]string
	// SizeResources overrides the resourceLimits of a size verbatim
	// (FLOW_CODING_OPENSANDBOX_SIZE_<SIZE>=cpu=2,memory=8Gi,gpu=1).
	SizeResources map[string]map[string]string
	// EphemeralStorage also sends the size's ephemeral storage as the
	// "ephemeral-storage" resource (Kubernetes runtimes only).
	EphemeralStorage bool
	// ExpiryWindow is how far ahead the sandbox's OpenSandbox expiration is
	// kept while Flow considers it alive; Flow renews it at half the window,
	// so an orphan whose Flow is gone expires on its own within the window.
	ExpiryWindow time.Duration
	// ExecdAccess is proxy (default) or endpoint.
	ExecdAccess string
	// ExecdToken is sent as X-EXECD-ACCESS-TOKEN when execd requires one (secret).
	ExecdToken string
	// RunAsUID is the uid (and gid) the runner, exec and the debug terminal
	// run as; 0 keeps execd's own user.
	RunAsUID int
	// TokenPath is where Flow writes the sandbox token in the sandbox
	// (FLOW_TOKEN_FILE); OpenSandbox has no projected volumes.
	TokenPath string
	// EgressPolicy is off (default) or proxy; EgressAllow lists extra domains.
	EgressPolicy string
	EgressAllow  []string
	// ImageUsername and ImagePassword authenticate private sandbox image pulls (image.auth).
	ImageUsername string
	ImagePassword string
}

func loadOpenSandbox() OpenSandboxConfig {
	sizes := map[string]map[string]string{}
	for _, size := range []string{"small", "medium", "large"} {
		if raw := value("FLOW_CODING_OPENSANDBOX_SIZE_"+strings.ToUpper(size), ""); raw != "" {
			sizes[size] = stringMap(raw)
		}
	}
	pools := map[string]string{}
	for key, val := range stringMap(value("FLOW_CODING_OPENSANDBOX_POOLS", "")) {
		if val != "" {
			pools[strings.ToLower(key)] = val
		}
	}
	return OpenSandboxConfig{
		URL:                strings.TrimRight(value("FLOW_CODING_OPENSANDBOX_URL", DefaultOpenSandboxURL), "/"),
		APIKey:             secret("FLOW_CODING_OPENSANDBOX_API_KEY"),
		RequestTimeout:     duration("FLOW_CODING_OPENSANDBOX_REQUEST_TIMEOUT", 30*time.Second),
		CreateTimeout:      duration("FLOW_CODING_OPENSANDBOX_CREATE_TIMEOUT", 10*time.Minute),
		Retries:            integer("FLOW_CODING_OPENSANDBOX_RETRIES", 3),
		CAFile:             value("FLOW_CODING_OPENSANDBOX_CA_FILE", ""),
		InsecureSkipVerify: boolean("FLOW_CODING_OPENSANDBOX_INSECURE_SKIP_VERIFY", false),
		ExtraMetadata:      stringMap(value("FLOW_CODING_OPENSANDBOX_EXTRA_METADATA", "")),
		Extensions:         stringMap(value("FLOW_CODING_OPENSANDBOX_EXTENSIONS", "")),
		Pools:              pools,
		SizeResources:      sizes,
		EphemeralStorage:   boolean("FLOW_CODING_OPENSANDBOX_EPHEMERAL_STORAGE", false),
		ExpiryWindow:       duration("FLOW_CODING_OPENSANDBOX_EXPIRY_WINDOW", time.Hour),
		ExecdAccess:        strings.ToLower(value("FLOW_CODING_OPENSANDBOX_EXECD_ACCESS", OpenSandboxExecdProxy)),
		ExecdToken:         secret("FLOW_CODING_OPENSANDBOX_EXECD_TOKEN"),
		RunAsUID:           integer("FLOW_CODING_OPENSANDBOX_RUN_AS_UID", 1000),
		TokenPath:          value("FLOW_CODING_OPENSANDBOX_TOKEN_PATH", "/tmp/flow/token"),
		EgressPolicy:       strings.ToLower(value("FLOW_CODING_OPENSANDBOX_EGRESS_POLICY", OpenSandboxEgressOff)),
		EgressAllow:        csv(value("FLOW_CODING_OPENSANDBOX_EGRESS_ALLOW", "")),
		ImageUsername:      value("FLOW_CODING_OPENSANDBOX_IMAGE_USERNAME", ""),
		ImagePassword:      secret("FLOW_CODING_OPENSANDBOX_IMAGE_PASSWORD"),
	}
}

// Validate checks the opensandbox provider settings.
func (o OpenSandboxConfig) Validate(sizes map[string]SandboxSize) error {
	parsed, err := url.Parse(o.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("FLOW_CODING_OPENSANDBOX_URL must be an http(s) URL such as %s", DefaultOpenSandboxURL)
	}
	if o.RequestTimeout < time.Second || o.CreateTimeout < time.Second {
		return fmt.Errorf("FLOW_CODING_OPENSANDBOX_REQUEST_TIMEOUT and FLOW_CODING_OPENSANDBOX_CREATE_TIMEOUT must be at least 1s")
	}
	if o.Retries < 0 || o.Retries > 10 {
		return fmt.Errorf("FLOW_CODING_OPENSANDBOX_RETRIES must be between 0 and 10")
	}
	if o.ExpiryWindow < 5*time.Minute {
		return fmt.Errorf("FLOW_CODING_OPENSANDBOX_EXPIRY_WINDOW must be at least 5m")
	}
	switch o.ExecdAccess {
	case OpenSandboxExecdProxy, OpenSandboxExecdEndpoint:
	default:
		return fmt.Errorf("FLOW_CODING_OPENSANDBOX_EXECD_ACCESS must be proxy or endpoint")
	}
	switch o.EgressPolicy {
	case OpenSandboxEgressOff, OpenSandboxEgressProxy:
	default:
		return fmt.Errorf("FLOW_CODING_OPENSANDBOX_EGRESS_POLICY must be off or proxy")
	}
	if o.RunAsUID < 0 {
		return fmt.Errorf("FLOW_CODING_OPENSANDBOX_RUN_AS_UID must be 0 (execd's user) or a uid")
	}
	if !strings.HasPrefix(o.TokenPath, "/") || strings.HasSuffix(o.TokenPath, "/") || strings.Contains(o.TokenPath, "..") {
		return fmt.Errorf("FLOW_CODING_OPENSANDBOX_TOKEN_PATH must be an absolute file path")
	}
	if o.CAFile != "" {
		if _, err := os.Stat(o.CAFile); err != nil {
			return fmt.Errorf("FLOW_CODING_OPENSANDBOX_CA_FILE: %v", err)
		}
	}
	for size := range o.Pools {
		if _, ok := sizes[size]; !ok {
			return fmt.Errorf("FLOW_CODING_OPENSANDBOX_POOLS names unknown size %q", size)
		}
	}
	if len(o.Pools) > 0 && o.EgressPolicy != OpenSandboxEgressOff {
		// OpenSandbox rejects networkPolicy together with extensions.poolRef.
		return fmt.Errorf("FLOW_CODING_OPENSANDBOX_POOLS cannot be combined with FLOW_CODING_OPENSANDBOX_EGRESS_POLICY=proxy")
	}
	for size, resources := range o.SizeResources {
		if resources["cpu"] == "" || resources["memory"] == "" {
			return fmt.Errorf("FLOW_CODING_OPENSANDBOX_SIZE_%s must set cpu and memory", strings.ToUpper(size))
		}
	}
	for key := range o.ExtraMetadata {
		if strings.HasPrefix(key, "opensandbox.io/") || strings.HasPrefix(key, "flow.dev/") {
			return fmt.Errorf("FLOW_CODING_OPENSANDBOX_EXTRA_METADATA may not set reserved key %q", key)
		}
	}
	if (o.ImageUsername == "") != (o.ImagePassword == "") {
		return fmt.Errorf("FLOW_CODING_OPENSANDBOX_IMAGE_USERNAME and FLOW_CODING_OPENSANDBOX_IMAGE_PASSWORD must be set together")
	}
	return nil
}
