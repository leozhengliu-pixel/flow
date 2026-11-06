package config

import (
	"os"
	"regexp"
	"testing"

	"go.yaml.in/yaml/v3"
)

// The Helm chart's config-file scenario renders a file Flow accepts.
func TestHelmScenarioConfigFileParses(t *testing.T) {
	raw, err := os.ReadFile("../../../deploy/helm/flow/tests/scenarios/config-file.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var values struct {
		Config struct {
			File map[string]any `yaml:"file"`
		} `yaml:"config"`
	}
	if err := yaml.Unmarshal(raw, &values); err != nil {
		t.Fatal(err)
	}
	rendered, err := yaml.Marshal(values.Config.File)
	if err != nil {
		t.Fatal(err)
	}
	file, err := ParseFile("/etc/flow/config.yaml", rendered)
	if err != nil {
		t.Fatal(err)
	}
	if file.DefaultLanguage != "zh-CN" || file.Env["FLOW_WORKSPACE_DEFAULT_REGION"] != "eu" || *file.Defaults.Team.EstimateType != "fibonacci" {
		t.Fatalf("scenario file = %+v", file)
	}
}

// helmDefine reads a define of the chart's _config-file.tpl as YAML.
func helmDefine(t *testing.T, source, name string, into any) {
	t.Helper()
	match := regexp.MustCompile(`(?s)\{\{- define "` + regexp.QuoteMeta(name) + `" -\}\}\n(.*?)\{\{- end -\}\}`).FindStringSubmatch(source)
	if match == nil {
		t.Fatalf("define %s not found", name)
	}
	if err := yaml.Unmarshal([]byte(match[1]), into); err != nil {
		t.Fatalf("define %s: %v", name, err)
	}
}

// The chart writes a config.file server: value into its own variable of the
// same setting, so its key list must match the file's keys, and every
// variable the chart sets that a server: key also sets must be listed (as
// overridable or as managed by the chart).
func TestHelmChartKnowsTheFileKeysOfItsVariables(t *testing.T) {
	raw, err := os.ReadFile("../../../deploy/helm/flow/templates/_config-file.tpl")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	overridable := map[string]string{}
	helmDefine(t, source, "flow.config.fileServerKeys", &overridable)
	owned := map[string]struct{ Key, Use string }{}
	helmDefine(t, source, "flow.config.chartOwnedKeys", &owned)

	serverKeys := map[string]string{} // variable -> key under server
	var walk func(prefix string, children map[string]*field)
	walk = func(prefix string, children map[string]*field) {
		for key, spec := range children {
			if spec.kind == kindSection {
				walk(join(prefix, key), spec.children)
			} else if spec.env != "" {
				serverKeys[spec.env] = join(prefix, key)
			}
		}
	}
	walk("", fileSchema["server"].children)
	for name, key := range overridable {
		if serverKeys[name] != key {
			t.Errorf("chart maps %s to server.%s; the file sets it from server.%s", name, key, serverKeys[name])
		}
		if _, both := owned[name]; both {
			t.Errorf("%s is both overridable and managed by the chart", name)
		}
	}
	for name, entry := range owned {
		if serverKeys[name] != entry.Key || entry.Use == "" {
			t.Errorf("chart-managed %s maps to server.%s; the file sets it from server.%s", name, entry.Key, serverKeys[name])
		}
	}
	template, err := os.ReadFile("../../../deploy/helm/flow/templates/_configmap-env.tpl")
	if err != nil {
		t.Fatal(err)
	}
	rendered := regexp.MustCompile(`(?m)^  ((?:FLOW|OTEL)_[A-Z0-9_]+):`).FindAllStringSubmatch(string(template), -1)
	if len(rendered) < 50 {
		t.Fatalf("only %d chart variables found", len(rendered))
	}
	for _, match := range rendered {
		name := match[1]
		_, isOverridable := overridable[name]
		_, isOwned := owned[name]
		if key, typed := serverKeys[name]; typed && !isOverridable && !isOwned && name != ConfigFileEnv {
			t.Errorf("the chart sets %s, which server.%s also sets; list it in _config-file.tpl", name, key)
		}
	}
}

// The chart's server: scenario renders a file Flow accepts.
func TestHelmScenarioConfigFileServerParses(t *testing.T) {
	raw, err := os.ReadFile("../../../deploy/helm/flow/tests/scenarios/config-file-server.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var values struct {
		Config struct {
			File map[string]any `yaml:"file"`
		} `yaml:"config"`
	}
	if err := yaml.Unmarshal(raw, &values); err != nil {
		t.Fatal(err)
	}
	rendered, err := yaml.Marshal(values.Config.File)
	if err != nil {
		t.Fatal(err)
	}
	file, err := ParseFile("/etc/flow/config.yaml", rendered)
	if err != nil {
		t.Fatal(err)
	}
	// The values the chart writes into its variables are the ones Flow
	// derives from the file, so Flow does not log them as overrides.
	for name, want := range map[string]string{"FLOW_APP_URL": "https://flow.example.com", "FLOW_LOG_LEVEL": "debug", "FLOW_REDIS_TLS": "true", "FLOW_REDIS_PREFIX": "flow-prod", "FLOW_CHAT_PLATFORM": "lark", "FLOW_ASSET_FALLBACK_URL": "https://cdn.example.com/flow,https://cdn2.example.com/flow", "FLOW_DATABASE_MAX_OPEN_CONNS": "40"} {
		if file.Env[name] != want {
			t.Errorf("%s = %q, want %q", name, file.Env[name], want)
		}
	}
}
