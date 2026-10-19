package config

import (
	"strings"
	"testing"
	"time"
)

func TestCodingDefaults(t *testing.T) {
	c := loadCoding("https://flow.example")
	if c.Enabled || c.Provider != CodingProviderDisabled {
		t.Fatalf("coding must be off by default: %+v", c)
	}
	if c.RuntimeURL != "https://flow.example" || c.Snapshot.Strategy != "archive" || c.Kubernetes.Namespace != "flow-sandboxes" || !c.Kubernetes.HostUsers {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	if c.Sizes["small"].CPU != "1" || c.Sizes["medium"].Memory != "16Gi" || c.Sizes["large"].PIDs != 16384 {
		t.Fatalf("sizes: %+v", c.Sizes)
	}
	if c.Timeouts.DebugTerminal != 20*time.Minute || c.Timeouts.Turn != time.Hour {
		t.Fatalf("timeouts: %+v", c.Timeouts)
	}
	if c.GitHubApp.APIURL != "https://api.github.com" || c.GitLab.CredentialMode != "project-token" {
		t.Fatalf("code hosts: %+v %+v", c.GitHubApp, c.GitLab)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("defaults must validate: %v", err)
	}
}

func TestCodingParsing(t *testing.T) {
	t.Setenv("FLOW_CODING_ENABLED", "true")
	t.Setenv("FLOW_CODING_PROVIDER", "kubernetes")
	t.Setenv("FLOW_CODING_SECRET_KEY", "a2V5")
	t.Setenv("FLOW_CODING_K8S_NODE_SELECTOR", "flow.dev/pool=sandbox, kubernetes.io/arch=amd64")
	t.Setenv("FLOW_CODING_K8S_TOLERATIONS", "flow.dev/sandbox=true:NoSchedule")
	t.Setenv("FLOW_CODING_K8S_HOST_USERS", "false")
	t.Setenv("FLOW_CODING_K8S_RUNTIME_CLASS", "gvisor")
	t.Setenv("FLOW_CODING_WARM_POOL", "small=2,medium=1")
	t.Setenv("FLOW_CODING_MODEL_PRICES", `{"gpt-5-codex":{"input":1.25,"output":10}}`)
	t.Setenv("FLOW_CODING_MODEL_INFO", `{"gpt-5-codex":{"label":"GPT-5 Codex","group":"frontier","description":"Fast"}}`)
	t.Setenv("FLOW_GITHUB_APP_HOST", "github.acme.dev")
	c := loadCoding("http://localhost:5173")
	if c.Kubernetes.NodeSelector["flow.dev/pool"] != "sandbox" || c.Kubernetes.NodeSelector["kubernetes.io/arch"] != "amd64" {
		t.Fatalf("node selector: %v", c.Kubernetes.NodeSelector)
	}
	if c.Kubernetes.HostUsers || c.Kubernetes.RuntimeClassName != "gvisor" || len(c.Kubernetes.Tolerations) != 1 {
		t.Fatalf("kubernetes: %+v", c.Kubernetes)
	}
	if c.WarmPool["small"] != 2 || c.WarmPool["medium"] != 1 {
		t.Fatalf("warm pool: %v", c.WarmPool)
	}
	if c.LLM.Prices["gpt-5-codex"].Output != 10 {
		t.Fatalf("prices: %v", c.LLM.Prices)
	}
	if info := c.LLM.ModelInfo["gpt-5-codex"]; info.Group != ModelGroupFrontier || info.Description != "Fast" {
		t.Fatalf("model info: %v", c.LLM.ModelInfo)
	}
	if c.GitHubApp.APIURL != "https://github.acme.dev/api/v3" {
		t.Fatalf("GHE api url: %s", c.GitHubApp.APIURL)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestCodingValidation(t *testing.T) {
	base := func() CodingConfig {
		c := loadCoding("http://localhost:5173")
		c.Enabled, c.Provider, c.SecretKey = true, CodingProviderKubernetes, "a2V5"
		return c
	}
	cases := map[string]func(*CodingConfig){
		"provider":              func(c *CodingConfig) { c.Provider = "nomad" },
		"enabled without one":   func(c *CodingConfig) { c.Provider = CodingProviderDisabled },
		"secret key":            func(c *CodingConfig) { c.SecretKey = "" },
		"strategy":              func(c *CodingConfig) { c.Snapshot.Strategy = "zfs" },
		"volumesnapshot+docker": func(c *CodingConfig) { c.Snapshot.Strategy, c.Provider = "volumesnapshot", CodingProviderDocker },
		"volumesnapshot+emptyDir": func(c *CodingConfig) {
			c.Snapshot.Strategy, c.Kubernetes.WorkspaceVolume = "volumesnapshot", "emptyDir"
		},
		"warm pool size": func(c *CodingConfig) { c.WarmPool = map[string]int{"huge": 1} },
		"default region": func(c *CodingConfig) { c.Regions, c.DefaultRegion = []string{"us"}, "eu" },
		"gitlab mode":    func(c *CodingConfig) { c.GitLab.CredentialMode = "pat" },
		"github app":     func(c *CodingConfig) { c.GitHubApp.AppID = "1" },
		"runtime url":    func(c *CodingConfig) { c.RuntimeURL = "flow" },
		"cpu ratio":      func(c *CodingConfig) { c.CPURequestRatio = 2 },
	}
	for name, mutate := range cases {
		c := base()
		mutate(&c)
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "FLOW_") {
			t.Errorf("%s: expected a FLOW_* validation error, got %v", name, err)
		}
	}
}

func TestCodingModelInfoValidation(t *testing.T) {
	t.Setenv("FLOW_CODING_ENABLED", "true")
	t.Setenv("FLOW_CODING_PROVIDER", "fake")
	t.Setenv("FLOW_CODING_SECRET_KEY", "a2V5")
	for raw, ok := range map[string]bool{
		``:                         true,
		`{"m":{"group":"other"}}`:  true,
		`{"m":{"group":"legacy"}}`: false,
		`not json`:                 false,
	} {
		t.Setenv("FLOW_CODING_MODEL_INFO", raw)
		err := loadCoding("http://localhost:5173").Validate()
		if (err == nil) != ok {
			t.Errorf("FLOW_CODING_MODEL_INFO=%q: err %v", raw, err)
		}
	}
}
