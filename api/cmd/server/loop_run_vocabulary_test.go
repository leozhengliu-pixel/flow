package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The web app translates the run vocabulary the server stores in English
// (tool labels, trigger reasons, output kinds, failure reasons). These tests
// fail when a label is added here without its zh-CN entry or UI mirror.

const webSourceDir = "../../../web/src"

// webTranslationKeys are the keys of every web/src/i18n/translations*.ts table.
func webTranslationKeys(t *testing.T) map[string]bool {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(webSourceDir, "i18n", "translations*.ts"))
	if err != nil || len(files) == 0 {
		t.Fatalf("translation files not found: %v", err)
	}
	keys := map[string]bool{}
	doubleQuoted := regexp.MustCompile(`(?m)^\s*("(?:[^"\\]|\\.)*")\s*:`)
	singleQuoted := regexp.MustCompile(`(?m)^\s*'((?:[^'\\]|\\.)*)'\s*:`)
	for _, file := range files {
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range doubleQuoted.FindAllStringSubmatch(string(source), -1) {
			if key, err := strconv.Unquote(match[1]); err == nil {
				keys[key] = true
			}
		}
		for _, match := range singleQuoted.FindAllStringSubmatch(string(source), -1) {
			keys[strings.ReplaceAll(match[1], `\'`, `'`)] = true
		}
	}
	return keys
}

func readWebSource(t *testing.T, path string) string {
	t.Helper()
	source, err := os.ReadFile(filepath.Join(webSourceDir, path))
	if err != nil {
		t.Fatal(err)
	}
	return string(source)
}

func flowToolNames(t *testing.T) []string {
	t.Helper()
	var inventory []flowMCPTool
	if err := json.Unmarshal(flowMCPToolInventory, &inventory); err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, tool := range inventory {
		if name := strings.TrimPrefix(tool.Name, "mcp__flow."); !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

func TestLoopRunVocabularyTranslated(t *testing.T) {
	keys := webTranslationKeys(t)
	require := func(key, why string) {
		t.Helper()
		if !keys[key] {
			t.Errorf("missing zh-CN entry %q (%s) in web/src/i18n/translations-agent-steps.ts", key, why)
		}
	}

	// Tool labels: every fixed label, and every template and subject a Flow,
	// web or connector tool can produce.
	for name, label := range loopToolLabels {
		require(label, name)
	}
	for name, labels := range loopToolSaveLabels {
		require(labels[0], name)
		require(labels[1], name)
	}
	for _, template := range loopToolVerbTemplates {
		require(template, "verb template")
	}
	require(loopExternalToolTemplate, "connector tools")
	require(loopOtherToolTemplate, "other tools")
	for _, name := range append(flowToolNames(t), webSearchToolName, fetchURLToolName, "external_github_search_code") {
		for _, args := range []map[string]any{nil, {"id": "x"}} {
			template, subject := loopToolLabelTemplate(name, args)
			require(template, name)
			if subject != "" && template != loopExternalToolTemplate && template != loopOtherToolTemplate {
				require("subject:"+subject, name)
			}
		}
	}

	// Trigger reasons: mirrored by TRIGGER_REASON_TEXTS and translated per code.
	labels := readWebSource(t, "components/loops/loop-run-labels.ts")
	for code, text := range loopTriggerReasonTexts {
		if !strings.Contains(labels, "  "+code+": "+strconv.Quote(text)+",") {
			t.Errorf("TRIGGER_REASON_TEXTS in loop-run-labels.ts lacks %s: %q", code, text)
		}
		require("Triggered by {name} "+text, "trigger reason "+code)
	}
	for code, none := range loopTriggerReasonNone {
		if !strings.Contains(labels, code+": "+strconv.Quote(none)) {
			t.Errorf("TRIGGER_REASON_NONE in loop-run-labels.ts lacks %s: %q", code, none)
		}
		require(none, "cleared "+code)
	}

	// Output kinds the acceptance check names in no_output and tool_error.
	for kind, label := range loopOutputLabels {
		if !strings.Contains(labels, "  "+kind+": "+strconv.Quote(label)+",") {
			t.Errorf("OUTPUT_LABELS in loop-run-labels.ts lacks %s: %q", kind, label)
		}
		require(label, "output "+kind)
	}

	// Failure reasons: each has a label on the run page.
	status := readWebSource(t, "components/loops/loop-run-status.ts")
	for _, reason := range loopFailureReasons {
		if !regexp.MustCompile(`(?m)^\s+` + reason + `: "`).MatchString(status) {
			t.Errorf("REASON_LABELS in loop-run-status.ts lacks failure reason %q", reason)
		}
	}
}

// The web app checks every Flow tool has a translated chat step label using
// this list of tool names. Regenerate it with FLOW_UPDATE_FIXTURES=1.
func TestAgentToolNamesFixtureCurrent(t *testing.T) {
	path := filepath.Join(webSourceDir, "components", "agent", "agent-tool-names.json")
	want := flowToolNames(t)
	if os.Getenv("FLOW_UPDATE_FIXTURES") == "1" {
		raw, _ := json.MarshalIndent(want, "", "  ")
		if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("%s is out of date with flow_mcp_tools.json; run FLOW_UPDATE_FIXTURES=1 go test ./cmd/server -run TestAgentToolNamesFixtureCurrent", path)
	}
}

func TestLoopTriggerReasonLabels(t *testing.T) {
	cases := []struct {
		reason loopTriggerReason
		want   string
	}{
		{loopTriggerReason{Code: "triage"}, "Triggered by DEV-24 entering triage"},
		{loopTriggerReason{Code: "status", Value: "In Progress"}, "Triggered by DEV-24 status → In Progress"},
		{loopTriggerReason{Code: "assignee"}, "Triggered by DEV-24 assignee → No assignee"},
		{loopTriggerReason{Code: "label", Value: "Bug"}, "Triggered by DEV-24 label Bug added"},
		{loopTriggerReason{Code: "statusChanged"}, "Triggered by DEV-24 status changed"},
	}
	for _, item := range cases {
		if got := item.reason.label("DEV-24"); got != item.want {
			t.Errorf("label(%+v) = %q, want %q", item.reason, got, item.want)
		}
	}
	if got := loopToolLabel("list_release_notes", nil); got != "Listed release notes" {
		t.Errorf("generic label = %q", got)
	}
	if got := loopToolLabel("save_issue", map[string]any{"id": "i1"}); got != "Updated issue" {
		t.Errorf("update label = %q", got)
	}
	if got := loopToolLabel("external_github_search", nil); got != "Used github search" {
		t.Errorf("connector label = %q", got)
	}
}
