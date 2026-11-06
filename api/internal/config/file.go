package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	// The container image is built FROM scratch without a zoneinfo database;
	// team timezones in the file (and members' browser timezones) are checked
	// against the embedded copy.
	_ "time/tzdata"

	"go.yaml.in/yaml/v3"

	"flow/api/internal/domain"
)

// ConfigFileEnv names the YAML configuration file (docs/configuration.md,
// "Configuration file"); the --config flag wins over it.
const ConfigFileEnv = "FLOW_CONFIG_FILE"

// SupportedLanguages are the interface languages of the web app (AppLocale in
// web/src/i18n/locale.ts).
var SupportedLanguages = []string{"en-US", "zh-CN"}

// File is a parsed configuration file.
//
// Its `server:` section (and the few defaults that already have an
// environment variable) become environment values: ApplyEnvironment sets each
// one that the environment does not set, so the precedence is built-in
// default < file < environment and every existing reader keeps working.
// Its `defaults:` section becomes the creation defaults for new workspaces,
// teams and members' settings.
type File struct {
	// Path is the file the settings came from ("" when there is no file).
	Path string
	// Env maps environment variable names to the values the file gives them.
	Env map[string]string
	// Defaults are the creation defaults (domain.SetCreationDefaults).
	Defaults domain.CreationDefaults
	// DefaultLanguage is defaults.user.language: the language of the sign-in
	// pages for a browser that has not chosen one, and of new members'
	// personal settings. Empty keeps the browser's language.
	DefaultLanguage string
	// Applied and Overridden list the variables LoadFileFromArgs set from the
	// file and the ones it left because the environment sets them.
	Applied, Overridden []string
}

// LoadFileFromArgs reads the configuration file named by --config PATH
// (or --config=PATH) in args, or else by FLOW_CONFIG_FILE, and applies its
// environment values. Without either it returns an empty File.
func LoadFileFromArgs(args []string) (*File, error) {
	path, err := configPathFromArgs(args)
	if err != nil {
		return nil, err
	}
	if path == "" {
		path = strings.TrimSpace(os.Getenv(ConfigFileEnv))
	}
	if path == "" {
		return &File{Env: map[string]string{}}, nil
	}
	file, err := ReadFile(path)
	if err != nil {
		return nil, err
	}
	file.Applied, file.Overridden = file.ApplyEnvironment(os.LookupEnv, os.Setenv)
	return file, nil
}

func configPathFromArgs(args []string) (string, error) {
	for index := 0; index < len(args); index++ {
		arg := args[index]
		for _, flag := range []string{"--config", "-config"} {
			if value, ok := strings.CutPrefix(arg, flag+"="); ok {
				if strings.TrimSpace(value) == "" {
					return "", fmt.Errorf("%s needs a file path", flag)
				}
				return strings.TrimSpace(value), nil
			}
			if arg == flag {
				if index+1 >= len(args) || strings.TrimSpace(args[index+1]) == "" {
					return "", fmt.Errorf("%s needs a file path", flag)
				}
				return strings.TrimSpace(args[index+1]), nil
			}
		}
	}
	return "", nil
}

// ReadFile reads and validates a configuration file.
func ReadFile(path string) (*File, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config file %s: %w", path, err)
	}
	return ParseFile(path, content)
}

// ParseFile validates a configuration file's content. Unknown keys, values of
// the wrong type, values outside their allowed set and secrets are errors
// naming the file, the line and the key.
func ParseFile(path string, content []byte) (*File, error) {
	file := &File{Path: path, Env: map[string]string{}}
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		if errors.Is(err, io.EOF) {
			return file, nil // an empty file configures nothing
		}
		return nil, fmt.Errorf("config file %s: %w", path, err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("config file %s: only one YAML document is allowed", path)
	}
	root := &document
	if root.Kind == yaml.DocumentNode && len(root.Content) == 1 {
		root = root.Content[0]
	}
	if root.Kind == yaml.ScalarNode && root.Tag == "!!null" {
		return file, nil
	}
	parser := fileParser{path: path, file: file}
	if err := parser.section(root, "", fileSchema); err != nil {
		return nil, err
	}
	return file, nil
}

// ApplyEnvironment sets each environment value of the file that lookup does
// not already find (with a non-blank value). It returns the variables it set
// and the ones the environment overrides, both sorted.
func (f *File) ApplyEnvironment(lookup func(string) (string, bool), setenv func(string, string) error) (applied, overridden []string) {
	if f == nil {
		return nil, nil
	}
	for name, value := range f.Env {
		if current, ok := lookup(name); ok && strings.TrimSpace(current) != "" {
			overridden = append(overridden, name)
			continue
		}
		if err := setenv(name, value); err == nil {
			applied = append(applied, name)
		}
	}
	sort.Strings(applied)
	sort.Strings(overridden)
	return applied, overridden
}

type valueKind int

const (
	kindSection valueKind = iota
	kindString
	kindBool
	kindInt
	kindDuration
	kindList
	kindStringMap
	kindBoolMap
	kindEnvMap
)

// field describes one key of the file.
type field struct {
	kind     valueKind
	children map[string]*field
	// env is the environment variable the value sets.
	env string
	// enum lists the allowed (case-sensitive) values of a string.
	enum []string
	// min and max bound an integer when limited.
	min, max int
	limited  bool
	// separator joins a list for its environment variable (default ",").
	separator string
	// onOff renders a boolean as on/off instead of true/false.
	onOff bool
	// check validates a string value or each list entry.
	check func(string) error
	// keys limits the keys of a boolean map.
	keys []string
	// apply stores the decoded value into the creation defaults.
	apply func(*File, any)
}

func section(children map[string]*field) *field {
	return &field{kind: kindSection, children: children}
}
func envString(env string, enum ...string) *field {
	return &field{kind: kindString, env: env, enum: enum}
}
func envBool(env string) *field { return &field{kind: kindBool, env: env} }
func envInt(env string, min, max int) *field {
	return &field{kind: kindInt, env: env, min: min, max: max, limited: true}
}
func envDuration(env string) *field { return &field{kind: kindDuration, env: env} }
func envList(env string) *field     { return &field{kind: kindList, env: env} }

func defaultString(apply func(*domain.CreationDefaults, string), enum ...string) *field {
	return &field{kind: kindString, enum: enum, apply: func(f *File, value any) { apply(&f.Defaults, value.(string)) }}
}
func defaultMapped(mapping map[string]string, apply func(*domain.CreationDefaults, string)) *field {
	return &field{kind: kindString, enum: sortedKeys(mapping), apply: func(f *File, value any) { apply(&f.Defaults, mapping[value.(string)]) }}
}
func defaultBool(apply func(*domain.CreationDefaults, bool)) *field {
	return &field{kind: kindBool, apply: func(f *File, value any) { apply(&f.Defaults, value.(bool)) }}
}
func defaultInt(min, max int, apply func(*domain.CreationDefaults, int)) *field {
	return &field{kind: kindInt, min: min, max: max, limited: true, apply: func(f *File, value any) { apply(&f.Defaults, value.(int)) }}
}

func pointer[T any](value T) *T { return &value }

func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

var (
	permissionValues = []string{"members", "owners_and_admins", "owners", "admins"}
	estimateTypes    = []string{"notUsed", "exponential", "fibonacci", "flow", "tShirt"}
	months           = []string{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"}
	weekdays         = map[string]int{"sunday": 0, "monday": 1, "tuesday": 2, "wednesday": 3, "thursday": 4, "friday": 5, "saturday": 6}
	priorities       = map[string]int{"none": 0, "urgent": 1, "high": 2, "medium": 3, "low": 4}
	// workspaceFeatureFlags are the workspace feature switches a new
	// workspace's defaults may set (WorkspaceSettings.FeatureFlags).
	workspaceFeatureFlags = []string{"ai", "ai-agent", "asks", "audit-log", "coding-sessions", "customer-requests", "dashboards", "documents", "emojis", "initiatives", "loops", "pulse", "recently-deleted", "releases", "triage-intelligence"}
	workspaceRegions      = []string{"us", "eu", "uk", "ca", "cn", "asia", "jp", "sg", "in", "au", "br"}
)

// secretFields are the file keys of secrets Flow reads, with the variable that
// carries each one instead.
var secretFields = map[string]string{
	"server.database.url":                "FLOW_DATABASE_URL",
	"server.redis.url":                   "FLOW_REDIS_URL",
	"server.redis.username":              "FLOW_REDIS_USERNAME",
	"server.redis.password":              "FLOW_REDIS_PASSWORD",
	"server.storage.s3.accessKeyId":      "FLOW_S3_ACCESS_KEY_ID",
	"server.storage.s3.secretAccessKey":  "FLOW_S3_SECRET_ACCESS_KEY",
	"server.storage.s3.sessionToken":     "FLOW_S3_SESSION_TOKEN",
	"server.auth.google.clientSecret":    "FLOW_GOOGLE_CLIENT_SECRET",
	"server.auth.oidc.clientSecret":      "FLOW_OIDC_CLIENT_SECRET",
	"server.auth.saml.spPrivateKey":      "FLOW_SAML_SP_PRIVATE_KEY",
	"server.agent.apiKey":                "FLOW_AGENT_API_KEY",
	"server.agent.workerSecret":          "FLOW_AGENT_WORKER_SECRET",
	"server.agent.media.apiKey":          "FLOW_AGENT_MEDIA_API_KEY",
	"server.tts.apiKey":                  "FLOW_TTS_API_KEY",
	"server.webSearch.apiKey":            "FLOW_WEB_SEARCH_API_KEY",
	"server.smtp.password":               "FLOW_SMTP_PASSWORD",
	"server.connectorSecretKey":          "FLOW_CONNECTOR_SECRET_KEY",
	"server.coding.secretKey":            "FLOW_CODING_SECRET_KEY",
	"server.coding.githubApp.privateKey": "FLOW_GITHUB_APP_PRIVATE_KEY",
}

// secretKeyWords mark a key as a secret wherever it appears in the file.
var secretKeyWords = []string{"password", "passwd", "secret", "apikey", "privatekey", "accesskey", "credential", "signingkey"}

func secretKeyName(key string) bool {
	normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "", ".", "").Replace(key))
	if strings.HasSuffix(normalized, "file") || strings.HasSuffix(normalized, "secrets") {
		return false // a path to a secret file, or a list of Kubernetes Secret names
	}
	for _, word := range secretKeyWords {
		if strings.Contains(normalized, word) {
			return true
		}
	}
	return strings.HasSuffix(normalized, "token") || strings.HasSuffix(normalized, "key") || strings.HasSuffix(normalized, "keyid")
}

// secretEnvNames are variables that carry credentials although their name
// does not end like a secret.
var secretEnvNames = []string{"FLOW_DATABASE_URL", "FLOW_REDIS_URL", "FLOW_REDIS_USERNAME", "FLOW_MCP_CREDENTIALS", "FLOW_SAML_METADATA_XML"}

// SecretEnvName reports whether an environment variable carries a secret, so
// it must come from a Secret, not from the configuration file. The *_FILE
// variants name a file and are allowed.
func SecretEnvName(name string) bool {
	name = strings.ToUpper(strings.TrimSpace(name))
	if strings.HasSuffix(name, "_FILE") {
		return false
	}
	if slices.Contains(secretEnvNames, name) {
		return true
	}
	for _, suffix := range []string{"_KEY", "_KEY_ID", "_SECRET", "_TOKEN", "_PASSWORD", "_CREDENTIALS"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return strings.Contains(name, "PASSWORD")
}

var fileSchema = map[string]*field{
	"version": {kind: kindInt, min: 1, max: 1, limited: true},
	"server": section(map[string]*field{
		"httpAddr":    envString("FLOW_HTTP_ADDR"),
		"appUrl":      {kind: kindString, env: "FLOW_APP_URL", check: httpURL},
		"staticPath":  envString("FLOW_STATIC_PATH"),
		"environment": envString("FLOW_ENV"),
		"log": section(map[string]*field{
			"level":  envString("FLOW_LOG_LEVEL", "debug", "info", "warn", "error"),
			"format": envString("FLOW_LOG_FORMAT", "text", "json"),
		}),
		"assets": section(map[string]*field{
			"previousPath":       envString("FLOW_ASSET_PREVIOUS_PATH"),
			"sharedPath":         envString("FLOW_ASSET_SHARED_PATH"),
			"sharedKeepReleases": envInt("FLOW_ASSET_SHARED_KEEP_RELEASES", 2, 1000),
			"peers":              envList("FLOW_ASSET_PEERS"),
			"fallbackUrls":       {kind: kindList, env: "FLOW_ASSET_FALLBACK_URL", check: httpURL},
		}),
		"database": section(map[string]*field{
			"driver":                      envString("FLOW_DATABASE_DRIVER", "sqlite", "postgres", "postgresql", "mysql"),
			"path":                        envString("FLOW_DATABASE_PATH"),
			"maxOpenConns":                envInt("FLOW_DATABASE_MAX_OPEN_CONNS", 0, 100000),
			"maxIdleConns":                envInt("FLOW_DATABASE_MAX_IDLE_CONNS", 0, 100000),
			"connMaxLifetime":             envDuration("FLOW_DATABASE_CONN_MAX_LIFETIME"),
			"migrationLockTimeout":        envDuration("FLOW_DATABASE_MIGRATION_LOCK_TIMEOUT"),
			"maxTransactionBytes":         envInt("FLOW_DATABASE_MAX_TRANSACTION_BYTES", 1<<20, 1<<40),
			"workspaceStateMaxBytes":      envInt("FLOW_WORKSPACE_STATE_MAX_BYTES", 1<<20, 1<<40),
			"contentCacheBytes":           envInt("FLOW_CONTENT_CACHE_BYTES", 0, 1<<40),
			"searchIndexMaxChars":         envInt("FLOW_SEARCH_INDEX_MAX_CHARS", 0, 1<<30),
			"agentTranscriptOffloadBytes": envInt("FLOW_AGENT_TRANSCRIPT_OFFLOAD_BYTES", 1024, 1<<30),
		}),
		"redis": section(map[string]*field{
			"mode":           envString("FLOW_REDIS_MODE", "disabled", "standalone", "cluster"),
			"addrs":          envList("FLOW_REDIS_ADDRS"),
			"db":             envInt("FLOW_REDIS_DB", 0, 15),
			"tls":            envBool("FLOW_REDIS_TLS"),
			"prefix":         envString("FLOW_REDIS_PREFIX"),
			"poolSize":       envInt("FLOW_REDIS_POOL_SIZE", 1, 100000),
			"minIdleConns":   envInt("FLOW_REDIS_MIN_IDLE_CONNS", 0, 100000),
			"dialTimeout":    envDuration("FLOW_REDIS_DIAL_TIMEOUT"),
			"readTimeout":    envDuration("FLOW_REDIS_READ_TIMEOUT"),
			"writeTimeout":   envDuration("FLOW_REDIS_WRITE_TIMEOUT"),
			"connectTimeout": envDuration("FLOW_REDIS_CONNECT_TIMEOUT"),
			"lockTtl":        envDuration("FLOW_REDIS_LOCK_TTL"),
			"lockWait":       envDuration("FLOW_REDIS_LOCK_WAIT"),
		}),
		"storage": section(map[string]*field{
			"driver":    envString("FLOW_STORAGE_DRIVER", "local", "s3"),
			"localPath": envString("FLOW_STORAGE_LOCAL_PATH"),
			"s3": section(map[string]*field{
				"bucket":          envString("FLOW_S3_BUCKET"),
				"region":          envString("FLOW_S3_REGION"),
				"endpoint":        envString("FLOW_S3_ENDPOINT"),
				"pathStyle":       envBool("FLOW_S3_PATH_STYLE"),
				"prefix":          envString("FLOW_S3_PREFIX"),
				"contentPrefix":   envString("FLOW_S3_CONTENT_PREFIX"),
				"validateOnStart": envBool("FLOW_S3_VALIDATE_ON_START"),
			}),
		}),
		"retention": section(map[string]*field{
			"domainEventDays":  envInt("FLOW_DOMAIN_EVENT_RETENTION_DAYS", 0, 36500),
			"trashDays":        envInt("FLOW_TRASH_RETENTION_DAYS", 1, 36500),
			"changeLogMinRows": envInt("FLOW_CHANGE_LOG_MIN_ROWS", 0, 1<<30),
		}),
		"auth": section(map[string]*field{
			"disabled":       envBool("FLOW_AUTH_DISABLED"),
			"autoProvision":  envBool("FLOW_AUTH_AUTO_PROVISION"),
			"allowedDomains": {kind: kindList, env: "FLOW_AUTH_ALLOWED_DOMAINS", check: emailDomain},
			"email":          section(map[string]*field{"enabled": envBool("FLOW_AUTH_EMAIL_ENABLED")}),
			"google": section(map[string]*field{
				"enabled":     envBool("FLOW_AUTH_GOOGLE_ENABLED"),
				"clientId":    envString("FLOW_GOOGLE_CLIENT_ID"),
				"redirectUrl": {kind: kindString, env: "FLOW_GOOGLE_REDIRECT_URL", check: httpURL},
			}),
			"oidc": section(map[string]*field{
				"enabled":       envBool("FLOW_AUTH_OIDC_ENABLED"),
				"issuerUrl":     {kind: kindString, env: "FLOW_OIDC_ISSUER_URL", check: httpURL},
				"clientId":      envString("FLOW_OIDC_CLIENT_ID"),
				"redirectUrl":   {kind: kindString, env: "FLOW_OIDC_REDIRECT_URL", check: httpURL},
				"scopes":        {kind: kindList, env: "FLOW_OIDC_SCOPES", separator: " "},
				"displayName":   envString("FLOW_OIDC_DISPLAY_NAME"),
				"identityClaim": envString("FLOW_OIDC_IDENTITY_CLAIM"),
				"roleClaim":     envString("FLOW_OIDC_ROLE_CLAIM"),
				"roleMapping":   {kind: kindStringMap, env: "FLOW_OIDC_ROLE_MAPPING", enum: []string{"owner", "admin", "member", "guest"}},
				"defaultRole":   envString("FLOW_OIDC_DEFAULT_ROLE", "owner", "admin", "member", "guest"),
			}),
			"saml": section(map[string]*field{
				"enabled":     envBool("FLOW_AUTH_SAML_ENABLED"),
				"metadataUrl": {kind: kindString, env: "FLOW_SAML_METADATA_URL", check: httpURL},
				"entityId":    envString("FLOW_SAML_ENTITY_ID"),
				"acsUrl":      {kind: kindString, env: "FLOW_SAML_ACS_URL", check: httpURL},
				"displayName": envString("FLOW_SAML_DISPLAY_NAME"),
			}),
		}),
		"agent": section(map[string]*field{
			"enabled":              envBool("FLOW_AGENT_ENABLED"),
			"protocol":             envString("FLOW_AGENT_PROTOCOL", "openai-responses", "anthropic-messages", "openai-chat-completions"),
			"baseUrl":              {kind: kindString, env: "FLOW_AGENT_BASE_URL", check: httpURL},
			"model":                envString("FLOW_AGENT_MODEL"),
			"timeout":              envDuration("FLOW_AGENT_TIMEOUT"),
			"maxOutputTokens":      envInt("FLOW_AGENT_MAX_OUTPUT_TOKENS", 1, 131072),
			"anthropicVersion":     envString("FLOW_AGENT_ANTHROPIC_VERSION"),
			"toolsEnabled":         envBool("FLOW_AGENT_TOOLS_ENABLED"),
			"writeTools":           envBool("FLOW_AGENT_WRITE_TOOLS"),
			"reasoningEffort":      envString("FLOW_AGENT_REASONING_EFFORT", "none", "minimal", "low", "medium", "high", "xhigh"),
			"nativeWebSearch":      envBool("FLOW_AGENT_NATIVE_WEB_SEARCH"),
			"runtime":              envString("FLOW_AGENT_RUNTIME", AgentRuntimeInProcess, AgentRuntimeWorker),
			"workspaceConcurrency": envInt("FLOW_AGENT_WORKSPACE_CONCURRENCY", 1, 1000),
			"workerClaimTimeout":   envDuration("FLOW_AGENT_WORKER_CLAIM_TIMEOUT"),
			"maxToolTurns":         envInt("FLOW_AGENT_MAX_TOOL_TURNS", 4, 200),
			"maxToolCalls":         envInt("FLOW_AGENT_MAX_TOOL_CALLS", 4, 2000),
			"media": section(map[string]*field{
				"baseUrl":         {kind: kindString, env: "FLOW_AGENT_MEDIA_BASE_URL", check: httpURL},
				"transcribeModel": envString("FLOW_AGENT_MEDIA_TRANSCRIBE_MODEL"),
				"videoModel":      envString("FLOW_AGENT_MEDIA_VIDEO_MODEL"),
				"timeout":         envDuration("FLOW_AGENT_MEDIA_TIMEOUT"),
			}),
		}),
		"tts": section(map[string]*field{
			"enabled": envBool("FLOW_TTS_ENABLED"),
			"baseUrl": {kind: kindString, env: "FLOW_TTS_BASE_URL", check: httpURL},
			"model":   envString("FLOW_TTS_MODEL"),
			"voice":   envString("FLOW_TTS_VOICE"),
			"timeout": envDuration("FLOW_TTS_TIMEOUT"),
		}),
		"webSearch": section(map[string]*field{
			"provider": envString("FLOW_WEB_SEARCH_PROVIDER", "tavily", "brave", "searxng"),
			"url":      {kind: kindString, env: "FLOW_WEB_SEARCH_URL", check: httpURL},
		}),
		"telemetry": section(map[string]*field{
			"enabled":         envBool("FLOW_TELEMETRY_ENABLED"),
			"serviceName":     envString("OTEL_SERVICE_NAME"),
			"environment":     envString("FLOW_ENVIRONMENT"),
			"endpoint":        {kind: kindString, env: "OTEL_EXPORTER_OTLP_ENDPOINT", check: httpURL},
			"tracesEndpoint":  {kind: kindString, env: "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", check: httpURL},
			"metricsEndpoint": {kind: kindString, env: "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", check: httpURL},
		}),
		"smtp": section(map[string]*field{
			"host":     envString("FLOW_SMTP_HOST"),
			"port":     envInt("FLOW_SMTP_PORT", 1, 65535),
			"from":     envString("FLOW_SMTP_FROM"),
			"username": envString("FLOW_SMTP_USERNAME"),
		}),
		"proxy": section(map[string]*field{
			"trustHeaders":   envBool("FLOW_TRUST_PROXY_HEADERS"),
			"trustedProxies": envList("FLOW_TRUSTED_PROXIES"),
		}),
		"outbound": section(map[string]*field{
			"trustedPrivateEndpoints": envList("FLOW_TRUSTED_PRIVATE_ENDPOINTS"),
			"trustedPrivateNetworks":  envList("FLOW_TRUSTED_PRIVATE_NETWORKS"),
			"trustedCaFile":           envString("FLOW_TRUSTED_CA_FILE"),
		}),
		"workspaces": section(map[string]*field{
			"regionSelector": envBool("FLOW_WORKSPACE_REGION_SELECTOR_ENABLED"),
		}),
		// Any other non-secret FLOW_* or OTEL_* variable, by name.
		"env": {kind: kindEnvMap},
	}),
	"defaults": section(map[string]*field{
		"workspace": section(map[string]*field{
			"region":        envString("FLOW_WORKSPACE_DEFAULT_REGION", workspaceRegions...),
			"tutorial":      {kind: kindBool, env: "FLOW_WORKSPACE_TUTORIAL", onOff: true},
			"fiscalMonth":   defaultString(func(d *domain.CreationDefaults, v string) { d.Workspace.FiscalMonth = &v }, months...),
			"guestsAllowed": defaultBool(func(d *domain.CreationDefaults, v bool) { d.Workspace.GuestsAllowed = &v }),
			"sessionDurationDays": defaultInt(1, 365, func(d *domain.CreationDefaults, v int) {
				d.Workspace.SessionDurationDays = &v
			}),
			"invitePermission": defaultString(func(d *domain.CreationDefaults, v string) { d.Workspace.InvitePermission = &v }, permissionValues...),
			"teamCreatePermission": defaultString(func(d *domain.CreationDefaults, v string) {
				d.Workspace.TeamCreatePermission = &v
			}, permissionValues...),
			"inviteLinksEnabled": defaultBool(func(d *domain.CreationDefaults, v bool) { d.Workspace.InviteLinksEnabled = &v }),
			"allowedDomains": {kind: kindList, check: emailDomain, apply: func(f *File, value any) {
				domains := []string{}
				for _, item := range value.([]string) {
					domains = append(domains, strings.ToLower(strings.TrimPrefix(item, "@")))
				}
				f.Defaults.Workspace.AllowedDomains = domains
			}},
			"features": {kind: kindBoolMap, keys: workspaceFeatureFlags, apply: func(f *File, value any) {
				f.Defaults.Workspace.FeatureFlags = value.(map[string]bool)
			}},
		}),
		"team": section(map[string]*field{
			"timezone": {kind: kindString, check: timezone, apply: func(f *File, value any) {
				zone := value.(string)
				f.Defaults.Team.Timezone = &zone
			}},
			"defaultPriority": {kind: kindString, enum: sortedKeys(priorities), apply: func(f *File, value any) {
				f.Defaults.Team.DefaultPriority = pointer(priorities[value.(string)])
			}},
			"detailedHistory": defaultBool(func(d *domain.CreationDefaults, v bool) { d.Team.DetailedHistory = &v }),
			"estimates": section(map[string]*field{
				"type":             defaultString(func(d *domain.CreationDefaults, v string) { d.Team.EstimateType = &v }, estimateTypes...),
				"allowZero":        defaultBool(func(d *domain.CreationDefaults, v bool) { d.Team.EstimateAllowZero = &v }),
				"extended":         defaultBool(func(d *domain.CreationDefaults, v bool) { d.Team.EstimateExtended = &v }),
				"countUnestimated": defaultBool(func(d *domain.CreationDefaults, v bool) { d.Team.EstimateCountUnestimated = &v }),
			}),
			"triage": section(map[string]*field{
				"enabled":         defaultBool(func(d *domain.CreationDefaults, v bool) { d.Team.TriageEnabled = &v }),
				"requirePriority": defaultBool(func(d *domain.CreationDefaults, v bool) { d.Team.TriageRequirePriority = &v }),
			}),
			"autoClose": section(map[string]*field{
				"parentIssues": defaultBool(func(d *domain.CreationDefaults, v bool) { d.Team.AutoCloseParents = &v }),
				"subIssues":    defaultBool(func(d *domain.CreationDefaults, v bool) { d.Team.AutoCloseSubIssues = &v }),
				"staleIssues":  defaultBool(func(d *domain.CreationDefaults, v bool) { d.Team.AutoCloseStale = &v }),
				"staleMonths":  defaultInt(1, 24, func(d *domain.CreationDefaults, v int) { d.Team.StaleMonths = &v }),
			}),
			"autoArchiveMonths": defaultInt(1, 24, func(d *domain.CreationDefaults, v int) { d.Team.AutoArchiveMonths = &v }),
			"cycles": section(map[string]*field{
				"enabled":       defaultBool(func(d *domain.CreationDefaults, v bool) { d.Team.Cycles.Enabled = &v }),
				"durationWeeks": defaultInt(1, 8, func(d *domain.CreationDefaults, v int) { d.Team.Cycles.DurationWeeks = &v }),
				"cooldownWeeks": defaultInt(0, 8, func(d *domain.CreationDefaults, v int) { d.Team.Cycles.CooldownWeeks = &v }),
				"startDay": {kind: kindString, enum: sortedKeys(weekdays), apply: func(f *File, value any) {
					f.Defaults.Team.Cycles.StartsOn = pointer(weekdays[value.(string)])
				}},
				"upcomingCycles":   defaultInt(1, 15, func(d *domain.CreationDefaults, v int) { d.Team.Cycles.UpcomingCount = &v }),
				"autoCreate":       defaultBool(func(d *domain.CreationDefaults, v bool) { d.Team.Cycles.AutoCreate = &v }),
				"autoAddStarted":   defaultBool(func(d *domain.CreationDefaults, v bool) { d.Team.Cycles.AutoAddStarted = &v }),
				"autoAddCompleted": defaultBool(func(d *domain.CreationDefaults, v bool) { d.Team.Cycles.AutoAddCompleted = &v }),
				"moveUnfinished":   defaultBool(func(d *domain.CreationDefaults, v bool) { d.Team.Cycles.AutoMigrate = &v }),
			}),
		}),
		"user": section(map[string]*field{
			"language": {kind: kindString, enum: SupportedLanguages, apply: func(f *File, value any) {
				language := value.(string)
				f.Defaults.User.Language, f.DefaultLanguage = &language, language
			}},
			"theme": defaultMapped(map[string]string{"system": "System preference", "light": "Light", "dark": "Dark"}, func(d *domain.CreationDefaults, v string) {
				d.User.InterfaceTheme = &v
			}),
			"firstDayOfWeek": defaultMapped(map[string]string{"locale": "", "monday": "Monday", "tuesday": "Tuesday", "wednesday": "Wednesday", "thursday": "Thursday", "friday": "Friday", "saturday": "Saturday", "sunday": "Sunday"}, func(d *domain.CreationDefaults, v string) {
				d.User.FirstDay = &v
			}),
			"homeView": defaultMapped(map[string]string{"agent": "Flow Agent (default)", "inbox": "Inbox", "myIssues": "My issues"}, func(d *domain.CreationDefaults, v string) {
				d.User.HomeView = &v
			}),
			"displayNames": defaultMapped(map[string]string{"fullName": "Full name", "firstName": "First name", "username": "Username"}, func(d *domain.CreationDefaults, v string) {
				d.User.DisplayNames = &v
			}),
			"fontSize": defaultMapped(map[string]string{"small": "Small", "default": "Default", "large": "Large"}, func(d *domain.CreationDefaults, v string) {
				d.User.FontSize = &v
			}),
			"notifications": section(map[string]*field{
				"email":       defaultBool(func(d *domain.CreationDefaults, v bool) { d.User.EmailNotifications = &v }),
				"desktop":     defaultBool(func(d *domain.CreationDefaults, v bool) { d.User.DesktopNotifications = &v }),
				"emailFormat": defaultString(func(d *domain.CreationDefaults, v string) { d.User.EmailFormat = &v }, "digest", "immediate"),
			}),
		}),
	}),
}

type fileParser struct {
	path string
	file *File
}

func (p fileParser) errorf(node *yaml.Node, key, format string, args ...any) error {
	location := p.path
	if node != nil && node.Line > 0 {
		location = fmt.Sprintf("%s:%d", p.path, node.Line)
	}
	if key == "" {
		return fmt.Errorf("config file %s: %s", location, fmt.Sprintf(format, args...))
	}
	return fmt.Errorf("config file %s: %s: %s", location, key, fmt.Sprintf(format, args...))
}

func join(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

func (p fileParser) section(node *yaml.Node, prefix string, children map[string]*field) error {
	if isNull(node) {
		return nil
	}
	if node.Kind != yaml.MappingNode {
		return p.errorf(node, prefix, "must be a mapping of keys (got %s)", describe(node))
	}
	seen := map[string]bool{}
	for index := 0; index+1 < len(node.Content); index += 2 {
		keyNode, valueNode := node.Content[index], node.Content[index+1]
		if keyNode.Kind != yaml.ScalarNode {
			return p.errorf(keyNode, prefix, "keys must be plain names")
		}
		key := keyNode.Value
		path := join(prefix, key)
		if seen[key] {
			return p.errorf(keyNode, path, "is set twice")
		}
		seen[key] = true
		if env, ok := secretFields[path]; ok {
			return p.errorf(keyNode, path, "is a secret and is not read from the config file; set %s (or %s_FILE) from a Kubernetes Secret or the environment", env, env)
		}
		if secretKeyName(key) {
			return p.errorf(keyNode, path, "looks like a secret and is not read from the config file; pass secrets as environment variables from a Kubernetes Secret")
		}
		spec, ok := children[key]
		if !ok {
			return p.errorf(keyNode, path, "unknown key (allowed here: %s)", strings.Join(sortedKeys(children), ", "))
		}
		if err := p.value(valueNode, path, spec); err != nil {
			return err
		}
	}
	return nil
}

func isNull(node *yaml.Node) bool {
	return node == nil || node.Kind == yaml.ScalarNode && node.Tag == "!!null"
}

func describe(node *yaml.Node) string {
	switch node.Kind {
	case yaml.MappingNode:
		return "a mapping"
	case yaml.SequenceNode:
		return "a list"
	case yaml.AliasNode:
		return "an alias"
	}
	switch node.Tag {
	case "!!bool":
		return "boolean " + node.Value
	case "!!int":
		return "number " + node.Value
	case "!!float":
		return "number " + node.Value
	}
	return strconv.Quote(node.Value)
}

func (p fileParser) value(node *yaml.Node, path string, spec *field) error {
	if spec.kind == kindSection {
		return p.section(node, path, spec.children)
	}
	if isNull(node) {
		return nil // an empty value keeps the default
	}
	var decoded any
	var env string
	switch spec.kind {
	case kindString:
		text, err := p.scalarString(node, path)
		if err != nil {
			return err
		}
		if err := p.checkString(node, path, spec, text); err != nil {
			return err
		}
		decoded, env = text, text
	case kindBool:
		if node.Kind != yaml.ScalarNode || node.Tag != "!!bool" {
			return p.errorf(node, path, "must be true or false (got %s)", describe(node))
		}
		value := node.Value == "true"
		decoded, env = value, strconv.FormatBool(value)
		if spec.onOff {
			env = map[bool]string{true: "on", false: "off"}[value]
		}
	case kindInt:
		if node.Kind != yaml.ScalarNode || node.Tag != "!!int" {
			return p.errorf(node, path, "must be a whole number (got %s)", describe(node))
		}
		value, err := strconv.Atoi(node.Value)
		if err != nil {
			return p.errorf(node, path, "must be a whole number (got %s)", describe(node))
		}
		if spec.limited && spec.min == spec.max && value != spec.min {
			return p.errorf(node, path, "must be %d (got %d)", spec.min, value)
		}
		if spec.limited && (value < spec.min || value > spec.max) {
			return p.errorf(node, path, "must be between %d and %d (got %d)", spec.min, spec.max, value)
		}
		decoded, env = value, strconv.Itoa(value)
	case kindDuration:
		if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
			return p.errorf(node, path, "must be a duration such as 30s, 5m or 2h (got %s)", describe(node))
		}
		parsed, err := time.ParseDuration(strings.TrimSpace(node.Value))
		if err != nil || parsed <= 0 {
			return p.errorf(node, path, "must be a positive duration such as 30s, 5m or 2h (got %s)", describe(node))
		}
		decoded, env = strings.TrimSpace(node.Value), strings.TrimSpace(node.Value)
	case kindList:
		if node.Kind != yaml.SequenceNode {
			return p.errorf(node, path, "must be a list (got %s)", describe(node))
		}
		items := []string{}
		for index, item := range node.Content {
			itemPath := fmt.Sprintf("%s[%d]", path, index)
			text, err := p.scalarString(item, itemPath)
			if err != nil {
				return err
			}
			if text == "" {
				return p.errorf(item, itemPath, "must not be empty")
			}
			separator := spec.separator
			if separator == "" {
				separator = ","
			}
			if strings.Contains(text, separator) {
				return p.errorf(item, itemPath, "must be one value per list entry")
			}
			if err := p.checkString(item, itemPath, spec, text); err != nil {
				return err
			}
			items = append(items, text)
		}
		separator := spec.separator
		if separator == "" {
			separator = ","
		}
		decoded, env = items, strings.Join(items, separator)
	case kindStringMap:
		values, err := p.stringMap(node, path)
		if err != nil {
			return err
		}
		pairs := []string{}
		for _, key := range sortedKeys(values) {
			if len(spec.enum) > 0 && !slices.Contains(spec.enum, values[key].text) {
				return p.errorf(values[key].node, join(path, key), "must be one of %s (got %q)", strings.Join(spec.enum, ", "), values[key].text)
			}
			if strings.ContainsAny(key, ",=:") {
				return p.errorf(values[key].node, join(path, key), "must not contain , = or :")
			}
			pairs = append(pairs, key+"="+values[key].text)
		}
		decoded, env = values, strings.Join(pairs, ",")
	case kindBoolMap:
		if node.Kind != yaml.MappingNode {
			return p.errorf(node, path, "must be a mapping of names to true or false (got %s)", describe(node))
		}
		flags := map[string]bool{}
		for index := 0; index+1 < len(node.Content); index += 2 {
			keyNode, valueNode := node.Content[index], node.Content[index+1]
			key := keyNode.Value
			if !slices.Contains(spec.keys, key) {
				return p.errorf(keyNode, join(path, key), "unknown key (allowed here: %s)", strings.Join(spec.keys, ", "))
			}
			if _, dup := flags[key]; dup {
				return p.errorf(keyNode, join(path, key), "is set twice")
			}
			if valueNode.Kind != yaml.ScalarNode || valueNode.Tag != "!!bool" {
				return p.errorf(valueNode, join(path, key), "must be true or false (got %s)", describe(valueNode))
			}
			flags[key] = valueNode.Value == "true"
		}
		decoded = flags
	case kindEnvMap:
		return p.envMap(node, path)
	}
	if spec.env != "" {
		p.file.Env[spec.env] = env
	}
	if spec.apply != nil {
		spec.apply(p.file, decoded)
	}
	return nil
}

type mapEntry struct {
	text string
	node *yaml.Node
}

func (p fileParser) stringMap(node *yaml.Node, path string) (map[string]mapEntry, error) {
	if node.Kind != yaml.MappingNode {
		return nil, p.errorf(node, path, "must be a mapping (got %s)", describe(node))
	}
	values := map[string]mapEntry{}
	for index := 0; index+1 < len(node.Content); index += 2 {
		keyNode, valueNode := node.Content[index], node.Content[index+1]
		key := keyNode.Value
		if _, dup := values[key]; dup {
			return nil, p.errorf(keyNode, join(path, key), "is set twice")
		}
		text, err := p.scalarString(valueNode, join(path, key))
		if err != nil {
			return nil, err
		}
		values[key] = mapEntry{text: text, node: valueNode}
	}
	return values, nil
}

// envMap reads server.env: other FLOW_* and OTEL_* variables by name.
func (p fileParser) envMap(node *yaml.Node, path string) error {
	if node.Kind != yaml.MappingNode {
		return p.errorf(node, path, "must be a mapping of FLOW_* variable names to values (got %s)", describe(node))
	}
	for index := 0; index+1 < len(node.Content); index += 2 {
		keyNode, valueNode := node.Content[index], node.Content[index+1]
		name := keyNode.Value
		key := join(path, name)
		if name != strings.ToUpper(name) || !(strings.HasPrefix(name, "FLOW_") || strings.HasPrefix(name, "OTEL_")) {
			return p.errorf(keyNode, key, "must be a FLOW_* or OTEL_* variable name in capitals")
		}
		if name == ConfigFileEnv {
			return p.errorf(keyNode, key, "cannot name the config file from inside it")
		}
		if SecretEnvName(name) {
			return p.errorf(keyNode, key, "is a secret and is not read from the config file; set %s (or %s_FILE) from a Kubernetes Secret or the environment", name, name)
		}
		if _, dup := p.file.Env[name]; dup {
			return p.errorf(keyNode, key, "is already set by another key of the file")
		}
		if valueNode.Kind != yaml.ScalarNode || isNull(valueNode) {
			return p.errorf(valueNode, key, "must be a single value (got %s)", describe(valueNode))
		}
		p.file.Env[name] = valueNode.Value
	}
	return nil
}

func (p fileParser) scalarString(node *yaml.Node, path string) (string, error) {
	if node.Kind != yaml.ScalarNode || isNull(node) {
		return "", p.errorf(node, path, "must be a text value (got %s)", describe(node))
	}
	if node.Tag == "!!bool" {
		return "", p.errorf(node, path, "must be a text value (got %s); quote it if you mean the text", describe(node))
	}
	return strings.TrimSpace(node.Value), nil
}

func (p fileParser) checkString(node *yaml.Node, path string, spec *field, text string) error {
	if len(spec.enum) > 0 && spec.kind != kindStringMap && !slices.Contains(spec.enum, text) {
		return p.errorf(node, path, "must be one of %s (got %q)", strings.Join(spec.enum, ", "), text)
	}
	if spec.check != nil {
		if err := spec.check(text); err != nil {
			return p.errorf(node, path, "%v (got %q)", err, text)
		}
	}
	return nil
}

func httpURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return errors.New("must be an http(s) URL")
	}
	if parsed.User != nil {
		return errors.New("must not carry credentials (user:password@); pass them from a Secret")
	}
	return nil
}

func emailDomain(value string) error {
	value = strings.TrimPrefix(value, "@")
	if !strings.Contains(value, ".") || strings.ContainsAny(value, " /@") {
		return errors.New("must be an email domain such as example.com")
	}
	return nil
}

func timezone(value string) error {
	if value == "" || len(value) > 64 {
		return errors.New("must be an IANA time zone such as Asia/Shanghai")
	}
	if _, err := time.LoadLocation(value); err != nil {
		return errors.New("must be an IANA time zone such as Asia/Shanghai")
	}
	return nil
}
