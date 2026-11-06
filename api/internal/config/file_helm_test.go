package config

import (
	"os"
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
