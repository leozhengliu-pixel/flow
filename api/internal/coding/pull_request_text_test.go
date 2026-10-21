package coding

import (
	"context"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestPullRequestTitleRewritesTheSessionTitle(t *testing.T) {
	cases := map[string]string{
		"QA coding session: add a farewell function next to greet": "Add a farewell function next to greet",
		"QA delegation: add farewell(name) to greet.js":            "Add farewell(name) to greet.js",
		"fix the login redirect.":                                  "Fix the login redirect",
		"Fix: handle http://x timeouts":                            "Handle http://x timeouts",
		"Add retry to the sync worker":                             "Add retry to the sync worker",
		"one two three four five: not a label":                     "One two three four five: not a label",
		"":                                                         "Flow coding session",
		"QA:":                                                      "QA:",
	}
	for in, want := range cases {
		if got := pullRequestTitle(domain.CodingSession{DisplayTitle: in}); got != want {
			t.Errorf("pullRequestTitle(%q) = %q, want %q", in, got, want)
		}
	}
	long := pullRequestTitle(domain.CodingSession{DisplayTitle: strings.Repeat("word ", 40)})
	if len([]rune(long)) > 72 {
		t.Errorf("title is %d runes", len([]rune(long)))
	}
}

func TestPullRequestBodyIsOneSummaryLine(t *testing.T) {
	s := &Service{}
	session := domain.CodingSession{DisplayTitle: "QA: add farewell", Summary: "Add `farewell(name)` to return `Goodbye, <name>!`.\n\nDetails on a second paragraph."}
	if got := s.pullRequestBody(context.Background(), session); got != "Add `farewell(name)` to return `Goodbye, <name>!`." {
		t.Errorf("body = %q", got)
	}
	if got := s.pullRequestBody(context.Background(), domain.CodingSession{DisplayTitle: "QA: add farewell"}); got != "Add farewell" {
		t.Errorf("without a summary the body falls back to the title, got %q", got)
	}
	s.DecoratePullRequestBody = func(_ context.Context, _ domain.CodingSession, body string) string {
		return body + "\n\n## Screenshots"
	}
	if got := s.pullRequestBody(context.Background(), session); !strings.HasSuffix(got, "\n\n## Screenshots") || strings.Contains(got, "Opened by a Flow coding session") {
		t.Errorf("decorated body = %q", got)
	}
}
