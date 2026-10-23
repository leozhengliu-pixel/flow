package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/coordination"
	"flow/api/internal/objectstore"
	"flow/api/internal/outbound"
	"flow/api/internal/store"
)

type Config struct {
	HTTPAddr                       string
	StaticPath                     string
	AppURL                         string
	AuthDisabled                   bool
	WorkspaceRegionSelectorEnabled bool
	WorkspaceDefaultRegion         string
	// WorkspaceTutorial seeds the "Getting started" tutorial issues into
	// workspaces created from the app; FLOW_WORKSPACE_TUTORIAL=off disables it.
	WorkspaceTutorial bool
	Database          store.DatabaseConfig
	Redis             coordination.Config
	Storage           objectstore.Config
	// ContentStoragePrefix is the object key prefix for offloaded database
	// content when FLOW_STORAGE_DRIVER=s3 (FLOW_S3_CONTENT_PREFIX).
	ContentStoragePrefix string
	Auth                 AuthConfig
	Agent                AgentConfig
	TTS                  TTSConfig
	WebSearch            WebSearchConfig
	Telemetry            TelemetryConfig
	Retention            RetentionConfig
	// Coding configures coding sessions (coding.go).
	Coding CodingConfig
	// DevAllowLoopbackCodeHosts lets the workspace GitLab integration and
	// members' personal GitLab links use plain-HTTP loopback URLs
	// (http://127.0.0.1, http://localhost) while authentication stays on
	// (FLOW_DEV_ALLOW_LOOPBACK_CODE_HOSTS). Development only: Validate
	// refuses it when the deployment looks like production.
	DevAllowLoopbackCodeHosts bool
	// Outbound is the operator's trusted private endpoints and CA bundle for
	// server-side requests (FLOW_TRUSTED_PRIVATE_ENDPOINTS,
	// FLOW_TRUSTED_PRIVATE_NETWORKS, FLOW_TRUSTED_CA_FILE; outbound package).
	Outbound outbound.Config
	// DeploymentEnvironment is FLOW_ENV, or else FLOW_ENVIRONMENT, as set by
	// the operator (empty when neither is set). Development-only switches
	// refuse to turn on when it names production.
	DeploymentEnvironment string
	// Warnings lists settings Load adjusted (for example a feature turned
	// off for a missing credential); the server logs them at startup.
	Warnings []string
}

// RetentionConfig bounds how long Flow keeps history it no longer serves.
type RetentionConfig struct {
	// DomainEvents is how long the domain_events log keeps an event
	// (FLOW_DOMAIN_EVENT_RETENTION_DAYS, default 90); 0 keeps every event.
	DomainEvents time.Duration
	// Trash is how long deleted items stay restorable before they are purged
	// (FLOW_TRASH_RETENTION_DAYS, default 30).
	Trash time.Duration
	// ChangeLogMinRows is how many of each workspace's newest changes pruning
	// always keeps, whatever their age (FLOW_CHANGE_LOG_MIN_ROWS, default 10000).
	ChangeLogMinRows int
}

type AgentConfig struct {
	Enabled          bool
	Protocol         string
	BaseURL          string
	APIKey           string
	Model            string
	Timeout          time.Duration
	MaxOutputTokens  int
	AnthropicVersion string
	ToolsEnabled     bool
	WriteTools       bool
	// ReasoningEffort is the reasoning effort sent to OpenAI-compatible models
	// (none|minimal|low|medium|high|xhigh); empty leaves the provider default.
	ReasoningEffort string
	// NativeWebSearch lets chat search the web with the model provider's hosted search tool (OpenAI Responses
	// web_search, Anthropic web_search) when no FLOW_WEB_SEARCH_PROVIDER is configured. On by default.
	NativeWebSearch bool
	// Runtime selects where built-in agent runs execute: "inprocess" (the API's own tool loop, the default) or
	// "worker" (the agent-worker sidecar, claiming runs through the internal runtime API). FLOW_AGENT_RUNTIME.
	Runtime string
	// WorkerSecret authenticates agent workers on the internal runtime API (/internal/agent-runtime/*);
	// FLOW_AGENT_WORKER_SECRET. The internal API is off while it is empty.
	WorkerSecret string
	// WorkspaceRunConcurrency caps the agent runs one workspace holds at a time on the worker runtime;
	// FLOW_AGENT_WORKSPACE_CONCURRENCY.
	WorkspaceRunConcurrency int
	// Media configures the transcribe_media chat tool for audio and video attachments.
	Media AgentMediaConfig
}

// AgentMediaConfig points transcribe_media at an OpenAI-compatible API. BaseURL defaults to the Agent provider's;
// the Agent key is reused only when BaseURL is the Agent base URL, so it is never sent to another host.
type AgentMediaConfig struct {
	BaseURL string
	APIKey  string
	// TranscribeModel serves POST {BaseURL}/audio/transcriptions (audio); empty turns audio transcription off.
	TranscribeModel string
	// VideoModel is a multimodal model reached through POST {BaseURL}/chat/completions with the video as a
	// video_url content part; empty turns video transcription off.
	VideoModel string
	Timeout    time.Duration
}

// Agent runtimes (AgentConfig.Runtime).
const (
	AgentRuntimeInProcess = "inprocess"
	AgentRuntimeWorker    = "worker"
)

// TTSConfig configures text-to-speech for Pulse summaries ("Listen"). It
// calls an OpenAI-compatible POST {BaseURL}/audio/speech endpoint. The base
// URL defaults to the Agent provider's; the Agent key is reused only when
// the speech endpoint is the Agent provider itself.
type TTSConfig struct {
	Enabled bool
	BaseURL string
	APIKey  string
	Model   string
	Voice   string
	Timeout time.Duration
}

// WebSearchConfig selects the web search provider loops and Flow Agent use.
// An empty provider disables web search.
type WebSearchConfig struct {
	Provider string // tavily | brave | searxng | ""
	APIKey   string
	URL      string // searxng base URL, or an API base override for tavily/brave
}

// Enabled reports whether a provider is configured.
func (c WebSearchConfig) Enabled() bool { return c.Provider != "" }

type AuthConfig struct {
	EmailEnabled   bool
	Google         OAuthProvider
	OIDC           OIDCProvider
	SAML           SAMLProvider
	AutoProvision  bool
	AllowedDomains []string
}

type OAuthProvider struct {
	Enabled      bool
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

type OIDCProvider struct {
	Enabled       bool
	IssuerURL     string
	ClientID      string
	ClientSecret  string
	RedirectURL   string
	Scopes        []string
	DisplayName   string
	IdentityClaim string
	RoleClaim     string
	RoleMapping   map[string]string
	DefaultRole   string
}

type SAMLProvider struct {
	Enabled       bool
	MetadataURL   string
	MetadataXML   string
	EntityID      string
	ACSURL        string
	SPPrivateKey  string
	SPCertificate string
	DisplayName   string
}

type TelemetryConfig struct {
	Enabled        bool
	ServiceName    string
	Environment    string
	Endpoint       string
	TraceEndpoint  string
	MetricEndpoint string
}

func Load() (Config, error) {
	appURL := strings.TrimRight(value("FLOW_APP_URL", "http://localhost:5173"), "/")
	config := Config{
		HTTPAddr:                       value("FLOW_HTTP_ADDR", ":8080"),
		StaticPath:                     value("FLOW_STATIC_PATH", ""),
		AppURL:                         appURL,
		AuthDisabled:                   boolean("FLOW_AUTH_DISABLED", false),
		DevAllowLoopbackCodeHosts:      boolean("FLOW_DEV_ALLOW_LOOPBACK_CODE_HOSTS", false),
		DeploymentEnvironment:          strings.ToLower(value("FLOW_ENV", value("FLOW_ENVIRONMENT", ""))),
		WorkspaceRegionSelectorEnabled: boolean("FLOW_WORKSPACE_REGION_SELECTOR_ENABLED", true),
		WorkspaceDefaultRegion:         strings.ToLower(value("FLOW_WORKSPACE_DEFAULT_REGION", "us")),
		WorkspaceTutorial:              !switchedOff("FLOW_WORKSPACE_TUTORIAL"),
		Database: store.DatabaseConfig{
			Driver:               value("FLOW_DATABASE_DRIVER", "sqlite"),
			URL:                  secret("FLOW_DATABASE_URL"),
			Path:                 value("FLOW_DATABASE_PATH", value("FLOW_DB_PATH", "data/flow.db")),
			MaxOpenConns:         integer("FLOW_DATABASE_MAX_OPEN_CONNS", 0),
			MaxIdleConns:         integer("FLOW_DATABASE_MAX_IDLE_CONNS", 0),
			ConnMaxLifetime:      duration("FLOW_DATABASE_CONN_MAX_LIFETIME", 30*time.Minute),
			MaxStateBytes:        integer("FLOW_WORKSPACE_STATE_MAX_BYTES", 64<<20),
			MaxTransactionBytes:  integer("FLOW_DATABASE_MAX_TRANSACTION_BYTES", 32<<20),
			AgentOffloadBytes:    integer("FLOW_AGENT_TRANSCRIPT_OFFLOAD_BYTES", 8<<10),
			ContentCacheBytes:    integer("FLOW_CONTENT_CACHE_BYTES", 128<<20),
			SearchIndexMaxChars:  integer("FLOW_SEARCH_INDEX_MAX_CHARS", 20000),
			MigrationLockTimeout: duration("FLOW_DATABASE_MIGRATION_LOCK_TIMEOUT", store.DefaultStartupLockTimeout),
		},
		Retention: RetentionConfig{
			DomainEvents:     time.Duration(integer("FLOW_DOMAIN_EVENT_RETENTION_DAYS", 90)) * 24 * time.Hour,
			Trash:            time.Duration(integer("FLOW_TRASH_RETENTION_DAYS", 30)) * 24 * time.Hour,
			ChangeLogMinRows: integer("FLOW_CHANGE_LOG_MIN_ROWS", store.DefaultChangeLogMinRows),
		},
		Redis: coordination.Config{
			Mode: value("FLOW_REDIS_MODE", "disabled"), URL: secret("FLOW_REDIS_URL"), Addrs: csvRaw(value("FLOW_REDIS_ADDRS", "")),
			Username: secret("FLOW_REDIS_USERNAME"), Password: secret("FLOW_REDIS_PASSWORD"), DB: integer("FLOW_REDIS_DB", 0), TLS: boolean("FLOW_REDIS_TLS", false),
			Prefix: value("FLOW_REDIS_PREFIX", "flow"), PoolSize: integer("FLOW_REDIS_POOL_SIZE", 40), MinIdleConns: integer("FLOW_REDIS_MIN_IDLE_CONNS", 5),
			DialTimeout: duration("FLOW_REDIS_DIAL_TIMEOUT", 5*time.Second), ReadTimeout: duration("FLOW_REDIS_READ_TIMEOUT", 3*time.Second), WriteTimeout: duration("FLOW_REDIS_WRITE_TIMEOUT", 3*time.Second),
			ConnectTimeout: duration("FLOW_REDIS_CONNECT_TIMEOUT", 15*time.Second), LockTTL: duration("FLOW_REDIS_LOCK_TTL", 30*time.Second), LockWait: duration("FLOW_REDIS_LOCK_WAIT", 5*time.Second),
		},
		ContentStoragePrefix: value("FLOW_S3_CONTENT_PREFIX", "content"),
		Storage: objectstore.Config{
			Driver:          value("FLOW_STORAGE_DRIVER", "local"),
			LocalPath:       value("FLOW_STORAGE_LOCAL_PATH", value("FLOW_UPLOAD_PATH", "data/uploads")),
			Bucket:          value("FLOW_S3_BUCKET", value("AWS_S3_BUCKET_NAME", "")),
			Region:          value("FLOW_S3_REGION", value("AWS_REGION", "")),
			Endpoint:        value("FLOW_S3_ENDPOINT", value("AWS_S3_ENDPOINT_URL", "")),
			AccessKeyID:     secretWithFallback("FLOW_S3_ACCESS_KEY_ID", "AWS_ACCESS_KEY_ID"),
			SecretAccessKey: secretWithFallback("FLOW_S3_SECRET_ACCESS_KEY", "AWS_SECRET_ACCESS_KEY"),
			SessionToken:    secretWithFallback("FLOW_S3_SESSION_TOKEN", "AWS_SESSION_TOKEN"),
			PathStyle:       boolean("FLOW_S3_PATH_STYLE", false),
			Prefix:          value("FLOW_S3_PREFIX", "uploads"),
			Validate:        boolean("FLOW_S3_VALIDATE_ON_START", true),
		},
		Auth: AuthConfig{
			EmailEnabled:  boolean("FLOW_AUTH_EMAIL_ENABLED", true),
			Google:        OAuthProvider{Enabled: boolean("FLOW_AUTH_GOOGLE_ENABLED", false), ClientID: secret("FLOW_GOOGLE_CLIENT_ID"), ClientSecret: secret("FLOW_GOOGLE_CLIENT_SECRET"), RedirectURL: value("FLOW_GOOGLE_REDIRECT_URL", appURL+"/api/auth/google/callback")},
			OIDC:          OIDCProvider{Enabled: boolean("FLOW_AUTH_OIDC_ENABLED", false), IssuerURL: value("FLOW_OIDC_ISSUER_URL", ""), ClientID: secret("FLOW_OIDC_CLIENT_ID"), ClientSecret: secret("FLOW_OIDC_CLIENT_SECRET"), RedirectURL: value("FLOW_OIDC_REDIRECT_URL", appURL+"/api/auth/oidc/callback"), Scopes: fields(value("FLOW_OIDC_SCOPES", "openid profile email")), DisplayName: value("FLOW_OIDC_DISPLAY_NAME", "OpenID Connect"), IdentityClaim: value("FLOW_OIDC_IDENTITY_CLAIM", "sub"), RoleClaim: value("FLOW_OIDC_ROLE_CLAIM", ""), RoleMapping: roleMapping(value("FLOW_OIDC_ROLE_MAPPING", "")), DefaultRole: value("FLOW_OIDC_DEFAULT_ROLE", "")},
			SAML:          SAMLProvider{Enabled: boolean("FLOW_AUTH_SAML_ENABLED", false), MetadataURL: value("FLOW_SAML_METADATA_URL", ""), MetadataXML: secret("FLOW_SAML_METADATA_XML"), EntityID: value("FLOW_SAML_ENTITY_ID", appURL), ACSURL: value("FLOW_SAML_ACS_URL", appURL+"/api/auth/saml/acs"), SPPrivateKey: secret("FLOW_SAML_SP_PRIVATE_KEY"), SPCertificate: secret("FLOW_SAML_SP_CERTIFICATE"), DisplayName: value("FLOW_SAML_DISPLAY_NAME", "SAML")},
			AutoProvision: boolean("FLOW_AUTH_AUTO_PROVISION", false), AllowedDomains: csv(value("FLOW_AUTH_ALLOWED_DOMAINS", "")),
		},
		Agent: AgentConfig{
			Enabled: boolean("FLOW_AGENT_ENABLED", false), Protocol: strings.ToLower(value("FLOW_AGENT_PROTOCOL", "openai-responses")),
			BaseURL: value("FLOW_AGENT_BASE_URL", "https://api.openai.com/v1"), APIKey: secret("FLOW_AGENT_API_KEY"), Model: value("FLOW_AGENT_MODEL", "gpt-5-mini"),
			Timeout: duration("FLOW_AGENT_TIMEOUT", 60*time.Second), MaxOutputTokens: integer("FLOW_AGENT_MAX_OUTPUT_TOKENS", 4096),
			AnthropicVersion: value("FLOW_AGENT_ANTHROPIC_VERSION", "2023-06-01"), ToolsEnabled: boolean("FLOW_AGENT_TOOLS_ENABLED", true), WriteTools: boolean("FLOW_AGENT_WRITE_TOOLS", false),
			ReasoningEffort: strings.ToLower(value("FLOW_AGENT_REASONING_EFFORT", "")), NativeWebSearch: boolean("FLOW_AGENT_NATIVE_WEB_SEARCH", true),
			Runtime: strings.ToLower(value("FLOW_AGENT_RUNTIME", AgentRuntimeInProcess)), WorkerSecret: secret("FLOW_AGENT_WORKER_SECRET"),
			WorkspaceRunConcurrency: integer("FLOW_AGENT_WORKSPACE_CONCURRENCY", 4),
			Media: AgentMediaConfig{
				BaseURL: value("FLOW_AGENT_MEDIA_BASE_URL", ""), APIKey: secret("FLOW_AGENT_MEDIA_API_KEY"),
				TranscribeModel: value("FLOW_AGENT_MEDIA_TRANSCRIBE_MODEL", ""), VideoModel: value("FLOW_AGENT_MEDIA_VIDEO_MODEL", ""),
				Timeout: duration("FLOW_AGENT_MEDIA_TIMEOUT", 5*time.Minute),
			},
		},
		TTS: TTSConfig{
			Enabled: boolean("FLOW_TTS_ENABLED", false), BaseURL: strings.TrimRight(value("FLOW_TTS_BASE_URL", value("FLOW_AGENT_BASE_URL", "https://api.openai.com/v1")), "/"),
			APIKey: secret("FLOW_TTS_API_KEY"), Model: value("FLOW_TTS_MODEL", "gpt-4o-mini-tts"), Voice: value("FLOW_TTS_VOICE", "alloy"),
			Timeout: duration("FLOW_TTS_TIMEOUT", 60*time.Second),
		},
		WebSearch: WebSearchConfig{Provider: strings.ToLower(value("FLOW_WEB_SEARCH_PROVIDER", "")), APIKey: secret("FLOW_WEB_SEARCH_API_KEY"), URL: strings.TrimRight(value("FLOW_WEB_SEARCH_URL", ""), "/")},
		Telemetry: TelemetryConfig{Enabled: boolean("FLOW_TELEMETRY_ENABLED", false) && !boolean("OTEL_SDK_DISABLED", false), ServiceName: value("OTEL_SERVICE_NAME", "flow-api"), Environment: value("FLOW_ENVIRONMENT", "production"), Endpoint: value("OTEL_EXPORTER_OTLP_ENDPOINT", ""), TraceEndpoint: value("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", ""), MetricEndpoint: value("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")},
	}
	config.Coding = loadCoding(appURL)
	config.Outbound = outbound.LoadConfig()
	config.resolveTTSCredential()
	config.resolveAgentMedia()
	config.clampRetention()
	return config, config.Validate()
}

// resolveTTSCredential reuses the Agent API key for speech only when the
// speech base URL is the Agent base URL, so the Agent credential is never
// sent to another host. A speech endpoint elsewhere needs FLOW_TTS_API_KEY;
// without it Pulse audio is turned off with a startup warning.
func (c *Config) resolveTTSCredential() {
	if c.TTS.APIKey != "" {
		return
	}
	if strings.TrimRight(c.TTS.BaseURL, "/") == strings.TrimRight(c.Agent.BaseURL, "/") {
		c.TTS.APIKey = c.Agent.APIKey
		return
	}
	if c.TTS.Enabled {
		c.TTS.Enabled = false
		c.Warnings = append(c.Warnings, "FLOW_TTS_ENABLED=true but FLOW_TTS_API_KEY is not set and FLOW_TTS_BASE_URL is not FLOW_AGENT_BASE_URL; Pulse audio is disabled")
	}
}

// resolveAgentMedia defaults the media endpoint to the Agent provider and lends it the Agent key only there.
// clampRetention keeps the change log's minimum window: domain events are what
// clients resume from, so they are never pruned sooner than seven days.
func (c *Config) clampRetention() {
	if c.Retention.DomainEvents > 0 && c.Retention.DomainEvents < store.MinChangeLogRetention {
		c.Retention.DomainEvents = store.MinChangeLogRetention
		c.Warnings = append(c.Warnings, "FLOW_DOMAIN_EVENT_RETENTION_DAYS raised to 7: the change log keeps at least seven days")
	}
}

func (c *Config) resolveAgentMedia() {
	media := &c.Agent.Media
	media.BaseURL = strings.TrimRight(media.BaseURL, "/")
	if media.BaseURL == "" {
		media.BaseURL = strings.TrimRight(c.Agent.BaseURL, "/")
	}
	if media.APIKey == "" && media.BaseURL == strings.TrimRight(c.Agent.BaseURL, "/") {
		media.APIKey = c.Agent.APIKey
	}
}

func (c Config) Validate() error {
	if err := c.validateDevLoopbackCodeHosts(); err != nil {
		return err
	}
	if !validWorkspaceRegion(c.WorkspaceDefaultRegion) {
		return fmt.Errorf("FLOW_WORKSPACE_DEFAULT_REGION must be one of us, eu, uk, ca, cn, asia, jp, sg, in, au, br")
	}
	driver := strings.ToLower(c.Database.Driver)
	if driver != "sqlite" && driver != "postgres" && driver != "postgresql" && driver != "mysql" {
		return fmt.Errorf("FLOW_DATABASE_DRIVER must be sqlite, postgres, or mysql")
	}
	if driver != "sqlite" && c.Database.URL == "" {
		return fmt.Errorf("FLOW_DATABASE_URL is required when FLOW_DATABASE_DRIVER=%s", driver)
	}
	if c.Database.MaxStateBytes < 1<<20 {
		return fmt.Errorf("FLOW_WORKSPACE_STATE_MAX_BYTES must be at least 1048576")
	}
	if c.Database.MaxTransactionBytes < 1<<20 {
		return fmt.Errorf("FLOW_DATABASE_MAX_TRANSACTION_BYTES must be at least 1048576")
	}
	if c.Database.AgentOffloadBytes < 1024 {
		return fmt.Errorf("FLOW_AGENT_TRANSCRIPT_OFFLOAD_BYTES must be at least 1024")
	}
	if c.Database.ContentCacheBytes < 0 {
		return fmt.Errorf("FLOW_CONTENT_CACHE_BYTES must not be negative")
	}
	if c.Database.MigrationLockTimeout <= 0 {
		return fmt.Errorf("FLOW_DATABASE_MIGRATION_LOCK_TIMEOUT must be a positive Go duration such as 10m")
	}
	if c.Database.SearchIndexMaxChars != 0 && c.Database.SearchIndexMaxChars < 1000 {
		return fmt.Errorf("FLOW_SEARCH_INDEX_MAX_CHARS must be 0 (no limit) or at least 1000")
	}
	if c.Retention.DomainEvents < 0 {
		return fmt.Errorf("FLOW_DOMAIN_EVENT_RETENTION_DAYS must be 0 (keep all) or more")
	}
	if c.Retention.ChangeLogMinRows < 0 {
		return fmt.Errorf("FLOW_CHANGE_LOG_MIN_ROWS must be 0 or more")
	}
	if c.Retention.Trash < 24*time.Hour {
		return fmt.Errorf("FLOW_TRASH_RETENTION_DAYS must be at least 1")
	}
	redisMode := strings.ToLower(strings.TrimSpace(c.Redis.Mode))
	if redisMode != "disabled" && redisMode != "standalone" && redisMode != "cluster" {
		return fmt.Errorf("FLOW_REDIS_MODE must be disabled, standalone, or cluster")
	}
	if redisMode != "disabled" && c.Redis.URL == "" && len(c.Redis.Addrs) == 0 {
		return fmt.Errorf("FLOW_REDIS_URL or FLOW_REDIS_ADDRS is required when Redis is enabled")
	}
	if redisMode == "cluster" && c.Redis.DB != 0 {
		return fmt.Errorf("FLOW_REDIS_DB must be 0 in cluster mode")
	}
	if redisMode != "disabled" && driver == "sqlite" {
		return fmt.Errorf("Redis multi-instance coordination requires PostgreSQL or MySQL; SQLite is single-instance only")
	}
	storage := strings.ToLower(c.Storage.Driver)
	if storage != "local" && storage != "s3" {
		return fmt.Errorf("FLOW_STORAGE_DRIVER must be local or s3")
	}
	if storage == "s3" && (c.Storage.Bucket == "" || c.Storage.Region == "") {
		return fmt.Errorf("FLOW_S3_BUCKET and FLOW_S3_REGION are required when FLOW_STORAGE_DRIVER=s3")
	}
	if c.Auth.Google.Enabled && (c.Auth.Google.ClientID == "" || c.Auth.Google.ClientSecret == "") {
		return fmt.Errorf("FLOW_GOOGLE_CLIENT_ID and FLOW_GOOGLE_CLIENT_SECRET are required when Google OAuth is enabled")
	}
	if c.Auth.OIDC.Enabled && (c.Auth.OIDC.IssuerURL == "" || c.Auth.OIDC.ClientID == "" || c.Auth.OIDC.ClientSecret == "") {
		return fmt.Errorf("FLOW_OIDC_ISSUER_URL, FLOW_OIDC_CLIENT_ID, and FLOW_OIDC_CLIENT_SECRET are required when OIDC is enabled")
	}
	if c.Auth.SAML.Enabled && c.Auth.SAML.MetadataURL == "" && c.Auth.SAML.MetadataXML == "" {
		return fmt.Errorf("FLOW_SAML_METADATA_URL or FLOW_SAML_METADATA_XML is required when SAML is enabled")
	}
	if c.Auth.SAML.Enabled && (c.Auth.SAML.SPPrivateKey == "" || c.Auth.SAML.SPCertificate == "") {
		return fmt.Errorf("FLOW_SAML_SP_PRIVATE_KEY and FLOW_SAML_SP_CERTIFICATE are required when SAML is enabled")
	}
	if c.Auth.SAML.Enabled {
		acsURL := strings.TrimRight(c.AppURL, "/") + "/api/auth/saml/acs"
		if c.Auth.SAML.ACSURL != acsURL {
			return fmt.Errorf("FLOW_SAML_ACS_URL must be %s", acsURL)
		}
	}
	for source, target := range c.Auth.OIDC.RoleMapping {
		if strings.TrimSpace(source) == "" || !validExternalRole(target) {
			return fmt.Errorf("FLOW_OIDC_ROLE_MAPPING contains an invalid role mapping")
		}
	}
	if c.Auth.OIDC.DefaultRole != "" && !validExternalRole(c.Auth.OIDC.DefaultRole) {
		return fmt.Errorf("FLOW_OIDC_DEFAULT_ROLE must be owner, admin, member, or guest")
	}
	if !c.AuthDisabled && !c.Auth.EmailEnabled && !c.Auth.Google.Enabled && !c.Auth.OIDC.Enabled && !c.Auth.SAML.Enabled {
		return fmt.Errorf("at least one authentication provider must be enabled")
	}
	if c.Agent.Enabled && (c.Agent.BaseURL == "" || c.Agent.Model == "") {
		return fmt.Errorf("FLOW_AGENT_BASE_URL and FLOW_AGENT_MODEL are required when Flow Agent is enabled")
	}
	switch c.Agent.ReasoningEffort {
	case "", "none", "minimal", "low", "medium", "high", "xhigh":
	default:
		return fmt.Errorf("FLOW_AGENT_REASONING_EFFORT must be none, minimal, low, medium, high or xhigh")
	}
	if c.Agent.Protocol != "openai-responses" && c.Agent.Protocol != "anthropic-messages" && c.Agent.Protocol != "openai-chat-completions" {
		return fmt.Errorf("FLOW_AGENT_PROTOCOL must be openai-responses, anthropic-messages, or openai-chat-completions")
	}
	if c.Agent.MaxOutputTokens < 1 || c.Agent.MaxOutputTokens > 131072 {
		return fmt.Errorf("FLOW_AGENT_MAX_OUTPUT_TOKENS must be between 1 and 131072")
	}
	if err := c.Agent.validateRuntime(); err != nil {
		return err
	}
	if c.TTS.Enabled && (c.TTS.BaseURL == "" || c.TTS.Model == "" || c.TTS.Voice == "") {
		return fmt.Errorf("FLOW_TTS_BASE_URL, FLOW_TTS_MODEL and FLOW_TTS_VOICE are required when FLOW_TTS_ENABLED=true")
	}
	if c.TTS.Enabled && !strings.HasPrefix(c.TTS.BaseURL, "http://") && !strings.HasPrefix(c.TTS.BaseURL, "https://") {
		return fmt.Errorf("FLOW_TTS_BASE_URL must be an http(s) URL")
	}
	if err := c.WebSearch.Validate(); err != nil {
		return err
	}
	if err := c.Coding.Validate(); err != nil {
		return err
	}
	if err := outbound.Validate(c.Outbound); err != nil {
		return err
	}
	if c.Telemetry.Enabled && c.Telemetry.Endpoint == "" && (c.Telemetry.TraceEndpoint == "" || c.Telemetry.MetricEndpoint == "") {
		return fmt.Errorf("OTEL_EXPORTER_OTLP_ENDPOINT or both signal-specific trace and metric endpoints are required when telemetry is enabled")
	}
	return nil
}

// validateDevLoopbackCodeHosts refuses FLOW_DEV_ALLOW_LOOPBACK_CODE_HOSTS on
// anything that looks like a production deployment: FLOW_ENV or
// FLOW_ENVIRONMENT naming production, or an app URL that is not a loopback
// address.
func (c Config) validateDevLoopbackCodeHosts() error {
	if !c.DevAllowLoopbackCodeHosts {
		return nil
	}
	switch c.DeploymentEnvironment {
	case "prod", "production":
		return fmt.Errorf("FLOW_DEV_ALLOW_LOOPBACK_CODE_HOSTS is a development-only setting and cannot be enabled when FLOW_ENV/FLOW_ENVIRONMENT=%s", c.DeploymentEnvironment)
	}
	if !loopbackURL(c.AppURL) {
		return fmt.Errorf("FLOW_DEV_ALLOW_LOOPBACK_CODE_HOSTS is a development-only setting and requires FLOW_APP_URL on localhost or a loopback address")
	}
	return nil
}

func loopbackURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// validateRuntime checks the agent runtime settings: the worker runtime needs a worker secret, and a secret must
// be long enough to resist guessing (it guards an API that acts for any user).
func (c AgentConfig) validateRuntime() error {
	switch c.Runtime {
	case "", AgentRuntimeInProcess, AgentRuntimeWorker:
	default:
		return fmt.Errorf("FLOW_AGENT_RUNTIME must be inprocess or worker")
	}
	if c.Runtime == AgentRuntimeWorker && c.WorkerSecret == "" {
		return fmt.Errorf("FLOW_AGENT_WORKER_SECRET is required when FLOW_AGENT_RUNTIME=worker")
	}
	if c.WorkerSecret != "" && len(c.WorkerSecret) < 32 {
		return fmt.Errorf("FLOW_AGENT_WORKER_SECRET must be at least 32 characters")
	}
	if c.WorkspaceRunConcurrency < 0 || c.WorkspaceRunConcurrency > 1000 {
		return fmt.Errorf("FLOW_AGENT_WORKSPACE_CONCURRENCY must be between 1 and 1000")
	}
	return nil
}

// Validate checks the web search provider settings.
func (c WebSearchConfig) Validate() error {
	switch c.Provider {
	case "":
		return nil
	case "tavily", "brave":
		if c.APIKey == "" {
			return fmt.Errorf("FLOW_WEB_SEARCH_API_KEY is required when FLOW_WEB_SEARCH_PROVIDER=%s", c.Provider)
		}
	case "searxng":
		if c.URL == "" {
			return fmt.Errorf("FLOW_WEB_SEARCH_URL is required when FLOW_WEB_SEARCH_PROVIDER=searxng")
		}
	default:
		return fmt.Errorf("FLOW_WEB_SEARCH_PROVIDER must be tavily, brave, searxng, or empty")
	}
	if c.URL != "" && !strings.HasPrefix(c.URL, "http://") && !strings.HasPrefix(c.URL, "https://") {
		return fmt.Errorf("FLOW_WEB_SEARCH_URL must be an http(s) URL")
	}
	return nil
}

func validWorkspaceRegion(region string) bool {
	switch strings.ToLower(strings.TrimSpace(region)) {
	case "us", "eu", "uk", "ca", "cn", "asia", "jp", "sg", "in", "au", "br":
		return true
	default:
		return false
	}
}

func value(name, fallback string) string {
	if result := strings.TrimSpace(os.Getenv(name)); result != "" {
		return result
	}
	return fallback
}

func secret(name string) string {
	if path := strings.TrimSpace(os.Getenv(name + "_FILE")); path != "" {
		content, err := os.ReadFile(path)
		if err == nil {
			return strings.TrimSpace(string(content))
		}
	}
	return strings.TrimSpace(os.Getenv(name))
}

func secretWithFallback(primary, fallback string) string {
	if result := secret(primary); result != "" {
		return result
	}
	return secret(fallback)
}

func boolean(name string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return strings.EqualFold(value, "true") || value == "1"
}

// switchedOff reports whether an on-by-default switch is turned off
// (off, false, 0, no or disabled).
func switchedOff(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "off", "false", "0", "no", "disabled":
		return true
	}
	return false
}

func integer(name string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil {
		return fallback
	}
	return value
}

// integer64 is integer for values that may exceed 32 bits (byte sizes on
// 32-bit platforms).
func integer64(name string, fallback int64) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(os.Getenv(name)), 10, 64)
	if err != nil {
		return fallback
	}
	return value
}

func duration(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func fields(value string) []string { return strings.Fields(value) }

func roleMapping(value string) map[string]string {
	result := map[string]string{}
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		parts := strings.FieldsFunc(item, func(r rune) bool { return r == '=' || r == ':' })
		if len(parts) != 2 {
			continue
		}
		source, target := strings.ToLower(strings.TrimSpace(parts[0])), strings.ToLower(strings.TrimSpace(parts[1]))
		if source != "" && target != "" {
			result[source] = target
		}
	}
	return result
}

func validExternalRole(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "owner", "admin", "member", "guest":
		return true
	default:
		return false
	}
}

func csv(value string) []string {
	result := []string{}
	for _, item := range strings.Split(value, ",") {
		if item = strings.ToLower(strings.TrimSpace(item)); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func csvRaw(value string) []string {
	result := []string{}
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}
