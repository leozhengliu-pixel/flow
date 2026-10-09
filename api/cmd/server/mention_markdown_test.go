package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func mentionFixture() (*domain.Bootstrap, []domain.Issue) {
	data := &domain.Bootstrap{
		Workspace: domain.Workspace{URLKey: "dev"},
		Teams:     []domain.Team{{ID: "team_1", Key: "DEV", Name: "Developers"}},
		Users: []domain.User{
			{ID: "user_ada", Name: "Ada Lovelace", DisplayName: "Ada", Username: "ada", Active: true},
			{ID: "user_bot", Name: "Build Bot", DisplayName: "Build Bot", App: true, Active: true},
			{ID: "user_sam", Name: "Sam Gone", DisplayName: "Sam Gone", Username: "sam"},
		},
		Members: []domain.WorkspaceMember{
			{User: domain.User{ID: "user_ada"}, Status: "active"},
			{User: domain.User{ID: "user_sam"}, Status: "suspended"},
		},
		Projects:         []domain.Project{{ID: "project_1", SlugID: "compare-test-abc", Name: "Compare Test", Milestones: []domain.ProjectMilestone{{ID: "milestone_1", Name: "Beta"}}}},
		Initiatives:      []domain.Initiative{{ID: "initiative_1", SlugID: "growth-1", Name: "Growth"}},
		Documents:        []domain.Document{{ID: "document_1", SlugID: "spec-1", Title: "Spec"}},
		Cycles:           []domain.Cycle{{ID: "cycle_1", TeamID: "team_1", Number: 3}},
		Labels:           []domain.IssueLabel{{ID: "label_1", Name: "Bug"}, {ID: "label_2", Name: "Roadmap", ResourceType: "project"}},
		Customers:        []domain.Customer{{ID: "customer_0123456789abcdef", Name: "Acme Corp"}},
		ReleasePipelines: []domain.ReleasePipeline{{ID: "pipeline_1", SlugID: "web", Name: "Web"}},
		Releases:         []domain.Release{{ID: "release_1", SlugID: "v1-2", Name: "v1.2", PipelineID: "pipeline_1"}},
		SavedViews:       []domain.SavedView{{ID: "view_1", SlugID: "my-view-1", Name: "My view"}},
		Reviews:          []domain.CodeReview{{ID: "review_1", SlugID: "pr-9", Title: "Fix login", Number: 9, RepositoryOwner: "acme", RepositoryName: "flow"}},
	}
	issues := []domain.Issue{{ID: "issue_4", Identifier: "DEV-4", Title: "Import your data!"}}
	return data, issues
}

func TestMentionMarkdownConvertsEveryKind(t *testing.T) {
	data, issues := mentionFixture()
	issueHref := "/dev/issue/DEV-4/import-your-data"
	cases := []struct {
		name, markdown               string
		kind, id, label, title, href string
	}{
		{"issue link", "See [the import](/dev/issue/DEV-4).", "issue", "issue_4", "DEV-4", "Import your data!", issueHref},
		{"absolute issue link", "[x](https://flow.example.com/dev/issue/DEV-4/old-title)", "issue", "issue_4", "DEV-4", "Import your data!", issueHref},
		{"bare identifier", "Blocked by DEV-4, sadly.", "issue", "issue_4", "DEV-4", "Import your data!", issueHref},
		{"lower-case identifier", "see dev-4", "issue", "issue_4", "DEV-4", "Import your data!", issueHref},
		{"bare issue url", "Open http://localhost:5173/dev/issue/DEV-4.", "issue", "issue_4", "DEV-4", "Import your data!", issueHref},
		{"project tab link", "[p](/dev/project/compare-test-abc/issues)", "project", "project_1", "Compare Test", "", "/dev/project/compare-test-abc/overview"},
		{"bare project path", "Track /dev/project/project_1 here", "project", "project_1", "Compare Test", "", "/dev/project/compare-test-abc/overview"},
		{"milestone", "[m](/dev/project/compare-test-abc/overview#milestone-milestone_1)", "milestone", "milestone_1", "Beta", "", "/dev/project/compare-test-abc/overview#milestone-milestone_1"},
		{"initiative", "[i](/dev/initiative/growth-1)", "initiative", "initiative_1", "Growth", "", "/dev/initiative/growth-1/overview"},
		{"document", "[d](/dev/document/spec-1)", "document", "document_1", "Spec", "", "/dev/document/spec-1"},
		{"profile link", "[a](/dev/profiles/ada)", "user", "user_ada", "Ada", "", ""},
		{"@display name", "Ping @Ada about it", "user", "user_ada", "Ada", "", ""},
		{"@full name, longest first", "(@ada lovelace)", "user", "user_ada", "Ada", "", ""},
		{"@username", "cc @ada", "user", "user_ada", "Ada", "", ""},
		{"team", "[t](/dev/team/DEV)", "team", "team_1", "Developers", "", "/dev/team/DEV/overview"},
		{"cycle", "[c](/dev/team/dev/cycle/3)", "cycle", "cycle_1", "Cycle 3", "", "/dev/team/DEV/cycle/3"},
		{"issue label", "[l](/dev/issue-label/bug)", "label", "label_1", "Bug", "", "/dev/issue-label/Bug"},
		{"project label", "[l](/dev/project-label/Roadmap)", "label", "label_2", "Roadmap", "", "/dev/project-label/Roadmap"},
		{"customer", "[c](/dev/customer/acme-456789abcdef)", "customer", "customer_0123456789abcdef", "Acme Corp", "", "/dev/customer/acme-corp-456789abcdef"},
		{"release", "[r](/dev/pipeline/web/release/v1-2/release-notes)", "release", "release_1", "v1.2", "", "/dev/pipeline/web/release/v1-2/issues"},
		{"workspace view", "[v](/dev/view/view_1)", "view", "view_1", "My view", "", "/dev/view/my-view-1"},
		{"team view", "[v](/dev/team/DEV/view/my-view-1)", "view", "view_1", "My view", "", "/dev/view/my-view-1"},
		{"review", "[r](/dev/review/pr-9/changes)", "review", "review_1", "Fix login", "", "/dev/review/pr-9"},
		{"pull request url", "Merged https://github.com/acme/flow/pull/9", "review", "review_1", "Fix login", "", "/dev/review/pr-9"},
		{"update link", "[Weekly note](/dev/project/compare-test-abc/overview#update-u1)", "update", "/dev/project/compare-test-abc/overview#update-u1", "Weekly note", "", "/dev/project/compare-test-abc/overview#update-u1"},
		{"comment link", "[that comment](/dev/issue/DEV-4#comment-c1)", "comment", "/dev/issue/DEV-4#comment-c1", "that comment", "", "/dev/issue/DEV-4#comment-c1"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			document := mentionMarkdownDocument(data, issues, test.markdown)
			mentions := loopMentionsIn(document)
			raw, _ := json.Marshal(document)
			if len(mentions) != 1 {
				t.Fatalf("mentions = %v\n%s", mentions, raw)
			}
			got := mentions[0]
			want := map[string]any{"mentionType": test.kind, "id": test.id, "label": test.label, "title": test.title, "href": test.href}
			for key, value := range want {
				if got[key] != value {
					t.Fatalf("%s = %q, want %q\n%s", key, got[key], value, raw)
				}
			}
		})
	}
}

func TestMentionMarkdownLeavesOtherTextAlone(t *testing.T) {
	data, issues := mentionFixture()
	cases := map[string]string{
		"inline code":            "Run `DEV-4` and `/dev/issue/DEV-4`.",
		"code block":             "```\nDEV-4 @Ada /dev/project/compare-test-abc\n```",
		"code link":              "[`DEV-4`](/dev/issue/DEV-4)",
		"other workspace":        "[x](/other/issue/DEV-4) and https://flow.example.com/other/project/compare-test-abc",
		"other host":             "[docs](https://example.com/docs/DEV-4) https://github.com/acme/flow/pull/10",
		"unknown team key":       "UTF-8 and ISO-8601 and ABC-12",
		"unknown issue":          "DEV-999 [x](/dev/issue/DEV-999)",
		"inaccessible issue":     "DEV-5 and /dev/issue/DEV-5",
		"inside words and paths": "xDEV-4 DEV-4x /DEV-4 #DEV-4 DEV-4-1 a@DEV-4",
		"app and suspended":      "@Build Bot and @Sam Gone and @sam",
		"not a mention":          "mail ada@Ada.com or @Adam or @Ada-team",
		"bare update url":        "https://flow.example.com/dev/project/compare-test-abc/overview#update-u1",
		"unknown resources":      "[x](/dev/project/missing) [y](/dev/document/missing) [z](/dev/project/compare-test-abc/overview#milestone-missing)",
		"mailto":                 "[mail](mailto:dev@example.com)",
	}
	for name, markdown := range cases {
		t.Run(name, func(t *testing.T) {
			document := mentionMarkdownDocument(data, issues, markdown)
			if mentions := loopMentionsIn(document); len(mentions) != 0 {
				raw, _ := json.Marshal(document)
				t.Fatalf("unexpected mentions %v\n%s", mentions, raw)
			}
			plain, _ := json.Marshal(mcpMarkdownDocument(markdown))
			raw, _ := json.Marshal(document)
			// Only bare external URLs may gain a link mark.
			if !strings.Contains(markdown, "https://") && string(raw) != string(plain) {
				t.Fatalf("document changed:\n%s\n%s", raw, plain)
			}
		})
	}
}

func TestMentionMarkdownKeepsSurroundingTextAndMarks(t *testing.T) {
	data, issues := mentionFixture()
	document := mentionMarkdownDocument(data, issues, "**Fix DEV-4 for @Ada** then see https://example.com/x.")
	raw, _ := json.Marshal(document)
	for _, want := range []string{`{"marks":[{"type":"bold"}],"text":"Fix ","type":"text"}`, `{"marks":[{"type":"bold"}],"text":" for ","type":"text"}`, `"href":"https://example.com/x"`, `"text":"."`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("missing %s in %s", want, raw)
		}
	}
	if mentions := loopMentionsIn(document); len(mentions) != 2 {
		t.Fatalf("mentions = %v", mentions)
	}
}

func TestMentionConversionIsIdempotent(t *testing.T) {
	data, issues := mentionFixture()
	resolver := newMentionResolver(data, issues)
	document := resolver.markdownDocument("# Plan\n\n- DEV-4 with @Ada\n- [Compare Test](/dev/project/compare-test-abc/overview) and `DEV-4`\n\n> see /dev/document/spec-1")
	first, _ := json.Marshal(document)
	var again map[string]any
	if err := json.Unmarshal(first, &again); err != nil {
		t.Fatal(err)
	}
	second, _ := json.Marshal(linkBareURLs(resolver.convert(again)))
	if string(first) != string(second) {
		t.Fatalf("second pass changed the document:\n%s\n%s", first, second)
	}
	if mentions := loopMentionsIn(document); len(mentions) != 4 {
		t.Fatalf("mentions = %v", mentions)
	}
}

func TestMentionCompanionDocument(t *testing.T) {
	data, issues := mentionFixture()
	resolver := newMentionResolver(data, issues)
	if resolver.companionDocument("Plain words with https://example.com") != nil {
		t.Fatal("a body without references needs no document")
	}
	if resolver.companionDocument("| a | b |\n|---|---|\n| DEV-4 | x |") != nil {
		t.Fatal("tables stay markdown")
	}
	if resolver.companionDocument("- DEV-4\n  - nested") != nil {
		t.Fatal("nested lists stay markdown")
	}
	if resolver.companionDocument("![shot](/uploads/a.png) DEV-4") != nil {
		t.Fatal("images stay markdown")
	}
	document := resolver.companionDocument("Fixed DEV-4.\n\n```\n| not | a table |\n```")
	if mentions := loopMentionsIn(document); len(mentions) != 1 || mentions[0]["id"] != "issue_4" {
		t.Fatalf("companion = %v", document)
	}
}

func TestMentionIssueReferencesUseTeamKeys(t *testing.T) {
	data, _ := mentionFixture()
	got := mentionIssueReferences(data, "DEV-4, dev-7, UTF-8, [x](/dev/issue/DEV-9/title) and /other/issue/DEV-10 `DEV-11` ABC-1")
	if strings.Join(got, ",") != "DEV-4,DEV-7,DEV-11,DEV-9,DEV-10" {
		t.Fatalf("identifiers = %v", got)
	}
	many := []string{}
	for index := 100; index < 180; index++ {
		many = append(many, "DEV-"+strconv.Itoa(index))
	}
	if got := mentionIssueReferences(data, strings.Join(many, " ")); len(got) != 50 {
		t.Fatalf("references are not capped: %d", len(got))
	}
}

func TestMentionRouteHelpersMatchTheWeb(t *testing.T) {
	for input, want := range map[string]string{"Import your data!": "import-your-data", "": "issue", "  --  ": "issue", "Ｆｕｌｌ width": "full-width", "Café au lait": "café-au-lait", strings.Repeat("a", 90): strings.Repeat("a", 80)} {
		if got := routeSlug(input); got != want {
			t.Fatalf("routeSlug(%q) = %q, want %q", input, got, want)
		}
	}
	if got := encodeURIComponent("a b/é(x)!~*'"); got != "a%20b%2F%C3%A9(x)!~*'" {
		t.Fatalf("encodeURIComponent = %q", got)
	}
}

func TestNotificationMentionCollectorsIgnoreResourceMentions(t *testing.T) {
	document := map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "content": []any{
		map[string]any{"type": "mention", "attrs": map[string]any{"mentionType": "issue", "id": "issue_4", "label": "DEV-4"}},
		map[string]any{"type": "mention", "attrs": map[string]any{"mentionType": "project", "id": "user_lookalike", "label": "Project"}},
		map[string]any{"type": "mention", "attrs": map[string]any{"mentionType": "user", "id": "user_ada", "label": "Ada"}},
		map[string]any{"type": "mention", "attrs": map[string]any{"id": "user_legacy", "label": "Legacy"}},
		map[string]any{"type": "userMention", "attrs": map[string]any{"userId": "user_old"}},
	}}}}
	ids := []string{}
	collectMentionIDs(document, &ids)
	if strings.Join(ids, ",") != "user_ada,user_legacy,user_old" {
		t.Fatalf("collectMentionIDs = %v", ids)
	}
	raw, _ := json.Marshal(document)
	ids = []string{}
	collectMentionIDs(json.RawMessage(raw), &ids)
	if strings.Join(ids, ",") != "user_ada,user_legacy,user_old" {
		t.Fatalf("collectMentionIDs(raw) = %v", ids)
	}
	if got := store.PulseMentions("", document, nil); strings.Join(got, ",") != "user_ada,user_legacy,user_old" {
		t.Fatalf("PulseMentions = %v", got)
	}
	data := &domain.Bootstrap{Users: []domain.User{{ID: "issue_4", Name: "Not a person"}}}
	comment := domain.Comment{Body: "[DEV-4](/dev/issue/DEV-4/x)", BodyData: document}
	if got := mentionedUserIDs(data, domain.Issue{}, domain.ActivityEvent{}, &comment); strings.Join(got, ",") != "user_ada,user_legacy,user_old" {
		t.Fatalf("mentionedUserIDs = %v", got)
	}
}
