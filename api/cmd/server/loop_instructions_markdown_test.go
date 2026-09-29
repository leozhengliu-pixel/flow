package main

import (
	"encoding/json"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func loopMentionsIn(document map[string]any) []map[string]any {
	found := []map[string]any{}
	var walk func(value any)
	walk = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			if typed["type"] == "mention" {
				attrs, _ := typed["attrs"].(map[string]any)
				found = append(found, attrs)
				return
			}
			for _, child := range typed {
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(document)
	return found
}

func TestLoopMarkdownDocumentTurnsReferencesIntoChips(t *testing.T) {
	data := &domain.Bootstrap{
		Workspace: domain.Workspace{URLKey: "dev"},
		Issues:    []domain.Issue{{ID: "issue_4", Identifier: "DEV-4", Title: "Import your data"}},
		Projects:  []domain.Project{{ID: "project_1", SlugID: "compare-test", Name: "Compare Test"}},
		Users:     []domain.User{{ID: "user_1", Name: "Dev User"}},
	}
	document := loopMarkdownDocument(data, "Summarize the week for **Compare Test**.\n\n1. Link duplicates of DEV-4 and ask @Dev User.\n2. Post on [Compare Test](/dev/project/compare-test/overview). Ignore DEV-999 and `DEV-4`.")
	mentions := loopMentionsIn(document)
	kinds := []string{}
	for _, mention := range mentions {
		kinds = append(kinds, mention["mentionType"].(string)+":"+mention["id"].(string))
	}
	if strings.Join(kinds, ",") != "issue:issue_4,user:user_1,project:project_1" {
		raw, _ := json.Marshal(document)
		t.Fatalf("mentions = %v\n%s", kinds, raw)
	}
	raw, _ := json.Marshal(document)
	for _, want := range []string{`"orderedList"`, `"bold"`, `DEV-999`, `"code"`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("document lost %s: %s", want, raw)
		}
	}
}

func TestLatestLoopVersionGetsGeneratedDescription(t *testing.T) {
	data := &domain.Bootstrap{LoopVersions: []domain.LoopVersion{
		{LoopID: "loop_1", Version: 1, Definition: domain.LoopDefinition{Instructions: "old"}},
		{LoopID: "loop_1", Version: 2, Definition: domain.LoopDefinition{Instructions: "new"}},
		{LoopID: "loop_2", Version: 3, Definition: domain.LoopDefinition{Instructions: "new"}},
	}}
	describeLatestLoopVersion(data, "loop_1", "new", "Posts a weekly summary.")
	if data.LoopVersions[1].Definition.Description != "Posts a weekly summary." || data.LoopVersions[0].Definition.Description != "" || data.LoopVersions[2].Definition.Description != "" {
		t.Fatalf("versions = %#v", data.LoopVersions)
	}
	describeLatestLoopVersion(data, "loop_1", "changed since", "Stale.")
	if data.LoopVersions[1].Definition.Description != "Posts a weekly summary." {
		t.Fatal("a description for other instructions overwrote the version")
	}
}
