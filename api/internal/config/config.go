package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/coordination"
	"flow/api/internal/objectstore"
	"flow/api/internal/store"
)

type Config struct {
	HTTPAddr                       string
	StaticPath                     string
	AppURL                         string
	AuthDisabled                   bool
	WorkspaceRegionSelectorEnabled bool
	WorkspaceDefaultRegion         string
	Database                       store.DatabaseConfig
	Redis                          coordination.Config
	Storage                        objectstore.Config
	Auth                           AuthConfig
	Agent                          AgentConfig
	Telemetry                      TelemetryConfig
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
}

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
		WorkspaceRegionSelectorEnabled: boolean("FLOW_WORKSPACE_REGION_SELECTOR_ENABLED", true),
		WorkspaceDefaultRegion:         strings.ToLower(value("FLOW_WORKSPACE_DEFAULT_REGION", "us")),
		Database: store.DatabaseConfig{
			Driver:          value("FLOW_DATABASE_DRIVER", "sqlite"),
			URL:             secret("FLOW_DATABASE_URL"),
			Path:            value("FLOW_DATABASE_PATH", value("FLOW_DB_PATH", "data/flow.db")),
			MaxOpenConns:    integer("FLOW_DATABASE_MAX_OPEN_CONNS", 0),
			MaxIdleConns:    integer("FLOW_DATABASE_MAX_IDLE_CONNS", 0),
			ConnMaxLifetime: duration("FLOW_DATABASE_CONN_MAX_LIFETIME", 30*time.Minute),
			MaxStateBytes:   integer("FLOW_WORKSPACE_STATE_MAX_BYTES", 64<<20),
		},
		Redis: coordination.Config{
			Mode: value("FLOW_REDIS_MODE", "disabled"), URL: secret("FLOW_REDIS_URL"), Addrs: csvRaw(value("FLOW_REDIS_ADDRS", "")),
			Username: secret("FLOW_REDIS_USERNAME"), Password: secret("FLOW_REDIS_PASSWORD"), DB: integer("FLOW_REDIS_DB", 0), TLS: boolean("FLOW_REDIS_TLS", false),
			Prefix: value("FLOW_REDIS_PREFIX", "flow"), PoolSize: integer("FLOW_REDIS_POOL_SIZE", 40), MinIdleConns: integer("FLOW_REDIS_MIN_IDLE_CONNS", 5),
			DialTimeout: duration("FLOW_REDIS_DIAL_TIMEOUT", 5*time.Second), ReadTimeout: duration("FLOW_REDIS_READ_TIMEOUT", 3*time.Second), WriteTimeout: duration("FLOW_REDIS_WRITE_TIMEOUT", 3*time.Second),
			ConnectTimeout: duration("FLOW_REDIS_CONNECT_TIMEOUT", 15*time.Second), LockTTL: duration("FLOW_REDIS_LOCK_TTL", 30*time.Second), LockWait: duration("FLOW_REDIS_LOCK_WAIT", 5*time.Second),
		},
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
			AutoProvision: boolean("FLOW_AUTH_AUTO_PROVISION", true), AllowedDomains: csv(value("FLOW_AUTH_ALLOWED_DOMAINS", "")),
		},
		Agent: AgentConfig{
			Enabled: boolean("FLOW_AGENT_ENABLED", false), Protocol: strings.ToLower(value("FLOW_AGENT_PROTOCOL", "openai-responses")),
			BaseURL: value("FLOW_AGENT_BASE_URL", "https://api.openai.com/v1"), APIKey: secret("FLOW_AGENT_API_KEY"), Model: value("FLOW_AGENT_MODEL", "gpt-5-mini"),
			Timeout: duration("FLOW_AGENT_TIMEOUT", 60*time.Second), MaxOutputTokens: integer("FLOW_AGENT_MAX_OUTPUT_TOKENS", 4096),
			AnthropicVersion: value("FLOW_AGENT_ANTHROPIC_VERSION", "2023-06-01"), ToolsEnabled: boolean("FLOW_AGENT_TOOLS_ENABLED", true), WriteTools: boolean("FLOW_AGENT_WRITE_TOOLS", false),
		},
		Telemetry: TelemetryConfig{Enabled: boolean("FLOW_TELEMETRY_ENABLED", false) && !boolean("OTEL_SDK_DISABLED", false), ServiceName: value("OTEL_SERVICE_NAME", "flow-api"), Environment: value("FLOW_ENVIRONMENT", "production"), Endpoint: value("OTEL_EXPORTER_OTLP_ENDPOINT", ""), TraceEndpoint: value("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", ""), MetricEndpoint: value("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")},
	}
	return config, config.Validate()
}

func (c Config) Validate() error {
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
	if c.Agent.Protocol != "openai-responses" && c.Agent.Protocol != "anthropic-messages" && c.Agent.Protocol != "openai-chat-completions" {
		return fmt.Errorf("FLOW_AGENT_PROTOCOL must be openai-responses, anthropic-messages, or openai-chat-completions")
	}
	if c.Agent.MaxOutputTokens < 1 || c.Agent.MaxOutputTokens > 131072 {
		return fmt.Errorf("FLOW_AGENT_MAX_OUTPUT_TOKENS must be between 1 and 131072")
	}
	if c.Telemetry.Enabled && c.Telemetry.Endpoint == "" && (c.Telemetry.TraceEndpoint == "" || c.Telemetry.MetricEndpoint == "") {
		return fmt.Errorf("OTEL_EXPORTER_OTLP_ENDPOINT or both signal-specific trace and metric endpoints are required when telemetry is enabled")
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

func integer(name string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
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
