package coding

import (
	"testing"

	"flow/api/internal/config"
)

func TestModelOptionsGroupsAndDescriptions(t *testing.T) {
	llm := config.CodingLLMConfig{
		CodexModels:      []string{"gpt-5-codex", "gpt-5", "acme-small", "acme-legacy", "gpt-4.1"},
		ClaudeCodeModels: []string{"claude-sonnet-4-5", "claude-opus-4-1"},
		OpenSourceModels: []string{"glm-4.6"},
		PiModels:         []string{"qwen3-coder"},
		ModelInfo: map[string]config.ModelInfo{
			"acme-small":  {Label: "Acme Small", Group: config.ModelGroupOther},
			"acme-legacy": {Group: config.ModelGroupFrontier, Description: "Pinned for old repos"},
		},
	}
	type row struct{ id, label, group, description string }
	var codex, open, pi []row
	for _, option := range ModelOptions(llm) {
		r := row{option.ID, option.Label, option.Group, option.Description}
		switch option.Harness {
		case "codex":
			codex = append(codex, r)
		case "openSource":
			open = append(open, r)
		case "pi":
			pi = append(pi, r)
		}
	}
	if len(pi) != 2 || pi[0] != (row{"auto", "Auto (qwen3-coder)", ModelGroupAuto, AutoModelDescription}) || pi[1].id != "qwen3-coder" {
		t.Fatalf("pi options: %+v", pi)
	}
	want := []row{
		{"auto", "Auto (gpt-5-codex)", ModelGroupAuto, AutoModelDescription},
		{"gpt-5-codex", "GPT-5 Codex", ModelGroupFrontier, "Strong reasoning for multi-file changes"},
		{"gpt-5", "GPT-5", ModelGroupFrontier, "Most capable model for complex tasks"},
		{"acme-legacy", "acme-legacy", ModelGroupFrontier, "Pinned for old repos"},
		{"acme-small", "Acme Small", ModelGroupOther, ""},
		{"gpt-4.1", "GPT-4.1", ModelGroupOther, ""},
	}
	if len(codex) != len(want) {
		t.Fatalf("codex options: %+v", codex)
	}
	for index := range want {
		if codex[index] != want[index] {
			t.Errorf("codex[%d] = %+v, want %+v", index, codex[index], want[index])
		}
	}
	// Unknown models without metadata: id as label, frontier by position, no description.
	if len(open) != 2 || open[1] != (row{"glm-4.6", "glm-4.6", ModelGroupFrontier, ""}) {
		t.Fatalf("open source options: %+v", open)
	}
}

func TestDescribeModelPositionalGroup(t *testing.T) {
	llm := config.CodingLLMConfig{}
	for index, want := range []string{ModelGroupFrontier, ModelGroupFrontier, ModelGroupFrontier, ModelGroupOther} {
		if got := DescribeModel(llm, "custom", index).Group; got != want {
			t.Errorf("index %d: group %q, want %q", index, got, want)
		}
	}
}
