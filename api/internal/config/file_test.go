package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func parse(t *testing.T, content string) (*File, error) {
	t.Helper()
	return ParseFile("/etc/flow/config.yaml", []byte(content))
}

func mustParse(t *testing.T, content string) *File {
	t.Helper()
	file, err := parse(t, content)
	if err != nil {
		t.Fatal(err)
	}
	return file
}

func TestConfigFileValid(t *testing.T) {
	file := mustParse(t, `
version: 1
server:
  appUrl: https://flow.example.com
  httpAddr: ":9090"
  log: {level: debug, format: json}
  database: {driver: postgres, maxOpenConns: 20, connMaxLifetime: 10m}
  auth:
    allowedDomains: [example.com, "@example.org"]
    oidc: {enabled: true, scopes: [openid, email], roleMapping: {flow-admins: admin, staff: member}}
  workspaces: {regionSelector: false}
  env:
    FLOW_CODING_MAX_SESSIONS: 20
defaults:
  workspace:
    region: eu
    tutorial: false
    sessionDurationDays: 14
    invitePermission: members
    allowedDomains: ["@Example.com"]
    features: {initiatives: false, loops: true}
  team:
    timezone: Asia/Shanghai
    defaultPriority: high
    estimates: {type: fibonacci, allowZero: true, countUnestimated: false}
    triage: {enabled: true}
    cycles: {enabled: true, durationWeeks: 1, startDay: sunday, moveUnfinished: false}
  user:
    language: zh-CN
    theme: dark
    firstDayOfWeek: monday
    homeView: myIssues
    notifications: {email: false, emailFormat: immediate}
`)
	wantEnv := map[string]string{
		"FLOW_APP_URL": "https://flow.example.com", "FLOW_HTTP_ADDR": ":9090", "FLOW_LOG_LEVEL": "debug", "FLOW_LOG_FORMAT": "json",
		"FLOW_DATABASE_DRIVER": "postgres", "FLOW_DATABASE_MAX_OPEN_CONNS": "20", "FLOW_DATABASE_CONN_MAX_LIFETIME": "10m",
		"FLOW_AUTH_ALLOWED_DOMAINS": "example.com,@example.org", "FLOW_AUTH_OIDC_ENABLED": "true", "FLOW_OIDC_SCOPES": "openid email",
		"FLOW_OIDC_ROLE_MAPPING": "flow-admins=admin,staff=member", "FLOW_WORKSPACE_REGION_SELECTOR_ENABLED": "false",
		"FLOW_CODING_MAX_SESSIONS": "20", "FLOW_WORKSPACE_DEFAULT_REGION": "eu", "FLOW_WORKSPACE_TUTORIAL": "off",
	}
	for name, want := range wantEnv {
		if got := file.Env[name]; got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if len(file.Env) != len(wantEnv) {
		t.Errorf("env = %v", file.Env)
	}
	d := file.Defaults
	if *d.Workspace.SessionDurationDays != 14 || *d.Workspace.InvitePermission != "members" || !slices.Equal(d.Workspace.AllowedDomains, []string{"example.com"}) || d.Workspace.FeatureFlags["initiatives"] || !d.Workspace.FeatureFlags["loops"] || d.Workspace.GuestsAllowed != nil {
		t.Errorf("workspace defaults = %+v", d.Workspace)
	}
	if *d.Team.Timezone != "Asia/Shanghai" || *d.Team.DefaultPriority != 2 || *d.Team.EstimateType != "fibonacci" || !*d.Team.EstimateAllowZero || *d.Team.EstimateCountUnestimated || d.Team.EstimateExtended != nil || !*d.Team.TriageEnabled {
		t.Errorf("team defaults = %+v", d.Team)
	}
	if !*d.Team.Cycles.Enabled || *d.Team.Cycles.DurationWeeks != 1 || *d.Team.Cycles.StartsOn != 0 || *d.Team.Cycles.AutoMigrate || d.Team.Cycles.CooldownWeeks != nil {
		t.Errorf("cycle defaults = %+v", d.Team.Cycles)
	}
	if *d.User.Language != "zh-CN" || file.DefaultLanguage != "zh-CN" || *d.User.InterfaceTheme != "Dark" || *d.User.FirstDay != "Monday" || *d.User.HomeView != "My issues" || *d.User.EmailNotifications || *d.User.EmailFormat != "immediate" || d.User.DesktopNotifications != nil {
		t.Errorf("user defaults = %+v", d.User)
	}
}

func TestConfigFileEmptyAndNullValues(t *testing.T) {
	for _, content := range []string{"", "# nothing\n", "server:\n", "server:\n  appUrl:\ndefaults:\n  user:\n    language: ~\n"} {
		file := mustParse(t, content)
		if len(file.Env) != 0 || file.Defaults.User.Language != nil || file.DefaultLanguage != "" {
			t.Errorf("%q configured %+v", content, file)
		}
	}
}

func TestConfigFileErrors(t *testing.T) {
	for _, test := range []struct {
		name, content, want string
	}{
		{"unknown top-level key", "servers: {}\n", "config file /etc/flow/config.yaml:1: servers: unknown key (allowed here: defaults, server, version)"},
		{"unknown nested key", "defaults:\n  team:\n    estimate:\n      type: fibonacci\n", "config file /etc/flow/config.yaml:3: defaults.team.estimate: unknown key"},
		{"wrong type bool", "defaults:\n  team:\n    triage:\n      enabled: \"yes\"\n", `config file /etc/flow/config.yaml:4: defaults.team.triage.enabled: must be true or false (got "yes")`},
		{"wrong type int", "server:\n  database:\n    maxOpenConns: many\n", `/etc/flow/config.yaml:3: server.database.maxOpenConns: must be a whole number (got "many")`},
		{"int out of range", "defaults:\n  team:\n    cycles:\n      durationWeeks: 9\n", "defaults.team.cycles.durationWeeks: must be between 1 and 8 (got 9)"},
		{"duration", "server:\n  agent:\n    timeout: 60\n", "server.agent.timeout: must be a duration such as 30s, 5m or 2h (got number 60)"},
		{"bad duration", "server:\n  agent:\n    timeout: soon\n", "server.agent.timeout: must be a positive duration"},
		{"section not mapping", "defaults:\n  user: zh-CN\n", `defaults.user: must be a mapping of keys (got "zh-CN")`},
		{"list expected", "server:\n  auth:\n    allowedDomains: example.com\n", `server.auth.allowedDomains: must be a list (got "example.com")`},
		{"bad language", "defaults:\n  user:\n    language: fr-FR\n", `config file /etc/flow/config.yaml:3: defaults.user.language: must be one of en-US, zh-CN (got "fr-FR")`},
		{"language case", "defaults:\n  user:\n    language: zh-cn\n", `defaults.user.language: must be one of en-US, zh-CN (got "zh-cn")`},
		{"bad estimate type", "defaults:\n  team:\n    estimates:\n      type: points\n", `must be one of notUsed, exponential, fibonacci, flow, tShirt (got "points")`},
		{"bad timezone", "defaults:\n  team:\n    timezone: Mars/Olympus\n", `defaults.team.timezone: must be an IANA time zone such as Asia/Shanghai (got "Mars/Olympus")`},
		{"bad log level", "server:\n  log:\n    level: verbose\n", `server.log.level: must be one of debug, info, warn, error (got "verbose")`},
		{"bad url", "server:\n  appUrl: flow.example.com\n", `server.appUrl: must be an http(s) URL (got "flow.example.com")`},
		{"url credentials", "server:\n  agent:\n    baseUrl: https://user:pw@llm.example.com/v1\n", "server.agent.baseUrl: must not carry credentials"},
		{"unknown feature", "defaults:\n  workspace:\n    features:\n      library: true\n", "defaults.workspace.features.library: unknown key"},
		{"bad region", "defaults:\n  workspace:\n    region: mars\n", `defaults.workspace.region: must be one of us, eu`},
		{"duplicate key", "server:\n  appUrl: https://a.example\n  appUrl: https://b.example\n", "config file /etc/flow/config.yaml:3: server.appUrl: is set twice"},
		{"env lower case", "server:\n  env:\n    flow_x: 1\n", "server.env.flow_x: must be a FLOW_* or OTEL_* variable name in capitals"},
		{"env other prefix", "server:\n  env:\n    HOME: /root\n", "server.env.HOME: must be a FLOW_* or OTEL_* variable name"},
		{"env duplicates a key", "server:\n  appUrl: https://a.example\n  env:\n    FLOW_APP_URL: https://b.example\n", "server.env.FLOW_APP_URL: is already set by another key of the file"},
		{"key duplicates env", "server:\n  env:\n    FLOW_LOG_LEVEL: warn\n  log:\n    level: debug\n", "config file /etc/flow/config.yaml:5: server.log.level: sets FLOW_LOG_LEVEL, which server.env already sets"},
		{"two documents", "version: 1\n---\nversion: 1\n", "only one YAML document is allowed"},
		{"bad version", "version: 2\n", "version: must be 1 (got 2)"},
		{"not yaml", "server: [\n", "config file /etc/flow/config.yaml: yaml:"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := parse(t, test.content)
			if err == nil {
				t.Fatalf("parsed without error")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error %q does not contain %q", err, test.want)
			}
		})
	}
}

func TestConfigFileRejectsSecrets(t *testing.T) {
	for _, test := range []struct{ content, want string }{
		{"server:\n  database:\n    url: postgres://u:p@db/flow\n", "config file /etc/flow/config.yaml:3: server.database.url: is a secret and is not read from the config file; set FLOW_DATABASE_URL (or FLOW_DATABASE_URL_FILE) from a Kubernetes Secret"},
		{"server:\n  agent:\n    apiKey: sk-test\n", "server.agent.apiKey: is a secret and is not read from the config file; set FLOW_AGENT_API_KEY"},
		{"server:\n  auth:\n    oidc:\n      clientSecret: x\n", "server.auth.oidc.clientSecret: is a secret"},
		{"server:\n  redis:\n    password: x\n", "server.redis.password: is a secret"},
		{"server:\n  smtp:\n    password: x\n", "server.smtp.password: is a secret"},
		{"server:\n  storage:\n    s3:\n      secretAccessKey: x\n", "FLOW_S3_SECRET_ACCESS_KEY"},
		{"defaults:\n  user:\n    githubToken: x\n", "defaults.user.githubToken: looks like a secret and is not read from the config file"},
		{"server:\n  agent:\n    media:\n      api_key: x\n", "server.agent.media.api_key: looks like a secret"},
		{"server:\n  env:\n    FLOW_AGENT_API_KEY: sk-test\n", "server.env.FLOW_AGENT_API_KEY: is a secret and is not read from the config file"},
		{"server:\n  env:\n    FLOW_CONNECTOR_SECRET_KEY: x\n", "server.env.FLOW_CONNECTOR_SECRET_KEY: is a secret"},
		{"server:\n  env:\n    FLOW_REDIS_URL: redis://:p@redis:6379\n", "server.env.FLOW_REDIS_URL: is a secret"},
		{"server:\n  env:\n    FLOW_MCP_CREDENTIALS: '{}'\n", "server.env.FLOW_MCP_CREDENTIALS: is a secret"},
		{"server:\n  env:\n    FLOW_CODING_OPENSANDBOX_EXECD_TOKEN: x\n", "is a secret"},
	} {
		_, err := parse(t, test.content)
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%q: error %v, want %q", test.content, err, test.want)
		}
		if err != nil && (strings.Contains(err.Error(), "sk-test") || strings.Contains(err.Error(), "u:p@")) {
			t.Errorf("error repeats the secret value: %v", err)
		}
	}
	// Paths to secret files and non-secret look-alikes are allowed.
	file := mustParse(t, "server:\n  env:\n    FLOW_AGENT_API_KEY_FILE: /run/secrets/agent\n    FLOW_CODING_IMAGE_PULL_SECRETS: registry\n    FLOW_AGENT_MAX_OUTPUT_TOKENS: 8192\n    FLOW_CODING_TOKEN_GRACE: 5m\n")
	if len(file.Env) != 4 {
		t.Fatalf("env = %v", file.Env)
	}
}

func TestConfigFileSchemaHasNoSecretKeys(t *testing.T) {
	var walk func(prefix string, children map[string]*field)
	walk = func(prefix string, children map[string]*field) {
		for key, spec := range children {
			path := join(prefix, key)
			if secretKeyName(key) {
				t.Errorf("%s looks like a secret", path)
			}
			if _, secret := secretFields[path]; secret {
				t.Errorf("%s is listed as a secret", path)
			}
			if spec.env != "" && SecretEnvName(spec.env) {
				t.Errorf("%s sets secret variable %s", path, spec.env)
			}
			if spec.kind == kindSection {
				walk(path, spec.children)
			}
		}
	}
	walk("", fileSchema)
}

func TestConfigFileEnvironmentPrecedence(t *testing.T) {
	file := mustParse(t, "server:\n  appUrl: https://file.example\n  log:\n    level: debug\n  database:\n    path: /data/file.db\n")
	environment := map[string]string{"FLOW_APP_URL": "https://env.example", "FLOW_DATABASE_PATH": "  "}
	applied, overridden := file.ApplyEnvironment(func(name string) (string, bool) {
		value, ok := environment[name]
		return value, ok
	}, func(name, value string) error {
		environment[name] = value
		return nil
	})
	if environment["FLOW_APP_URL"] != "https://env.example" || environment["FLOW_LOG_LEVEL"] != "debug" || environment["FLOW_DATABASE_PATH"] != "/data/file.db" {
		t.Fatalf("environment = %v", environment)
	}
	if !slices.Equal(applied, []string{"FLOW_DATABASE_PATH", "FLOW_LOG_LEVEL"}) || !slices.Equal(overridden, []string{"FLOW_APP_URL"}) {
		t.Fatalf("applied %v overridden %v", applied, overridden)
	}
	// A variable that already carries the file's value (the Helm chart
	// renders it) is not reported as an override.
	environment = map[string]string{"FLOW_APP_URL": "https://file.example"}
	applied, overridden = file.ApplyEnvironment(func(name string) (string, bool) {
		value, ok := environment[name]
		return value, ok
	}, func(name, value string) error {
		environment[name] = value
		return nil
	})
	if !slices.Equal(applied, []string{"FLOW_APP_URL", "FLOW_DATABASE_PATH", "FLOW_LOG_LEVEL"}) || len(overridden) != 0 {
		t.Fatalf("applied %v overridden %v", applied, overridden)
	}
}

// Integer settings are read into an int, so the file never accepts a value
// the platform cannot hold (32-bit ARM builds of the image).
func TestConfigFileIntegerBoundsFitThePlatform(t *testing.T) {
	names := map[string]string{}
	var walk func(prefix string, children map[string]*field)
	walk = func(prefix string, children map[string]*field) {
		for key, spec := range children {
			path := join(prefix, key)
			if spec.kind == kindSection {
				walk(path, spec.children)
				continue
			}
			if spec.kind == kindInt && (!spec.limited || spec.max > maxPlatformInt || spec.min < -maxPlatformInt) {
				t.Errorf("%s: bounds %d..%d (limited %v) exceed an int", path, spec.min, spec.max, spec.limited)
			}
			if spec.env != "" {
				if other, dup := names[spec.env]; dup {
					t.Errorf("%s and %s both set %s", path, other, spec.env)
				}
				names[spec.env] = path
			}
		}
	}
	walk("", fileSchema)
	file := mustParse(t, fmt.Sprintf("server:\n  database:\n    contentCacheBytes: %d\n", byteLimit))
	if file.Env["FLOW_CONTENT_CACHE_BYTES"] != strconv.FormatInt(byteLimit, 10) {
		t.Fatalf("env = %v", file.Env)
	}
	if _, err := parse(t, fmt.Sprintf("server:\n  database:\n    contentCacheBytes: %d\n", byteLimit+1)); err == nil || !strings.Contains(err.Error(), "must be between 0 and") {
		t.Fatalf("over the limit: %v", err)
	}
}

func TestLoadFileFromArgsAndEnvironment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("server:\n  appUrl: https://file.example\n  log:\n    format: json\n  agent:\n    model: from-file\ndefaults:\n  team:\n    estimates:\n      type: tShirt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(ConfigFileEnv, "")
	t.Setenv("FLOW_APP_URL", "https://env.example")
	t.Setenv("FLOW_LOG_FORMAT", "")
	t.Setenv("FLOW_AGENT_MODEL", "")
	for _, args := range [][]string{{"--config", path}, {"--config=" + path}, {"-config", path}} {
		file, err := LoadFileFromArgs(args)
		if err != nil {
			t.Fatal(err)
		}
		if file.Path != path || *file.Defaults.Team.EstimateType != "tShirt" {
			t.Fatalf("%v: file = %+v", args, file)
		}
		loaded, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		// The environment wins over the file; the file wins over built-in defaults.
		if loaded.AppURL != "https://env.example" || loaded.Agent.Model != "from-file" || os.Getenv("FLOW_LOG_FORMAT") != "json" {
			t.Fatalf("%v: app URL %q, model %q, log format %q", args, loaded.AppURL, loaded.Agent.Model, os.Getenv("FLOW_LOG_FORMAT"))
		}
		t.Setenv("FLOW_LOG_FORMAT", "")
		t.Setenv("FLOW_AGENT_MODEL", "")
	}
	// FLOW_CONFIG_FILE names the file when no flag does.
	t.Setenv(ConfigFileEnv, path)
	if file, err := LoadFileFromArgs(nil); err != nil || file.Path != path {
		t.Fatalf("FLOW_CONFIG_FILE: %+v, %v", file, err)
	}
	// Without either, nothing is configured.
	t.Setenv(ConfigFileEnv, "")
	if file, err := LoadFileFromArgs(nil); err != nil || file.Path != "" || len(file.Env) != 0 {
		t.Fatalf("no file: %+v, %v", file, err)
	}
	// A named file that does not exist stops startup.
	missing := filepath.Join(dir, "missing.yaml")
	if _, err := LoadFileFromArgs([]string{"--config", missing}); err == nil || !strings.Contains(err.Error(), "config file "+missing) {
		t.Fatalf("missing file error = %v", err)
	}
	if _, err := LoadFileFromArgs([]string{"--config"}); err == nil {
		t.Fatal("--config without a path was accepted")
	}
}

// The documented example parses, and docs/configuration.md shows it verbatim.
func TestConfigFileExample(t *testing.T) {
	example, err := os.ReadFile("../../../deploy/helm/flow/examples/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	file, err := ParseFile("examples/config.yaml", example)
	if err != nil {
		t.Fatal(err)
	}
	if file.DefaultLanguage != "zh-CN" || *file.Defaults.Team.EstimateType != "fibonacci" {
		t.Fatalf("example defaults = %+v", file.Defaults)
	}
	docs, err := os.ReadFile("../../../docs/configuration.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(docs), "```yaml\n"+string(example)+"```") {
		t.Fatal("docs/configuration.md does not show deploy/helm/flow/examples/config.yaml verbatim")
	}
}

// SupportedLanguages matches the web app's AppLocale.
func TestSupportedLanguagesMatchWebLocales(t *testing.T) {
	source, err := os.ReadFile("../../../web/src/i18n/locale.ts")
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`export type AppLocale = ([^\n]+)`).FindStringSubmatch(string(source))
	if match == nil {
		t.Fatal("AppLocale not found in web/src/i18n/locale.ts")
	}
	locales := regexp.MustCompile(`'([^']+)'`).FindAllStringSubmatch(match[1], -1)
	web := []string{}
	for _, locale := range locales {
		web = append(web, locale[1])
	}
	slices.Sort(web)
	if !slices.Equal(web, SupportedLanguages) {
		t.Fatalf("web locales %v, SupportedLanguages %v", web, SupportedLanguages)
	}
}
