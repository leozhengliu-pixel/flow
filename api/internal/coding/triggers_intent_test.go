package coding

import (
	"context"
	"errors"
	"testing"
)

// fakeIntentModel answers from a table and counts calls.
type fakeIntentModel struct {
	answers map[string]CodingIntent
	err     error
	calls   int
}

func (m *fakeIntentModel) ClassifyCodingIntent(_ context.Context, request string) (CodingIntent, error) {
	m.calls++
	if m.err != nil {
		return "", m.err
	}
	if intent, ok := m.answers[request]; ok {
		return intent, nil
	}
	return IntentUnclear, nil
}

func TestClassifyCodingIntentRules(t *testing.T) {
	cases := []struct {
		request string
		want    CodingIntent
		rule    string
	}{
		// Reference: these start coding immediately.
		{"Fix this bug", IntentCode, "codeVerb"},
		{"Can you fix the login redirect?", IntentCode, "codeVerb"},
		{"Open a PR that bumps the timeout to 30s", IntentCode, "codeVerb"},
		{"please create a pull request for this", IntentCode, "codeVerb"},
		{"Implement the CSV export described above", IntentCode, "codeVerb"},
		{"Investigate and fix the flaky test", IntentCode, "codeVerb"},
		{"Plan and implement the migration", IntentCode, "codeVerb"},
		{"Add tests for the parser", IntentCode, "codeVerb"},
		{"Refactor the settings page", IntentCode, "codeVerb"},
		{"Address the review comments", IntentCode, "codeVerb"},
		{"Make the tests pass", IntentCode, "codeVerb"},
		{"Apply the fix you suggested", IntentCode, "codeVerb"},
		{"rebase onto main", IntentCode, "codeVerb"},
		{"修复这个问题", IntentCode, "codeVerb"},
		{"排查并修复登录问题", IntentCode, "codeVerb"},
		{"帮我提个PR", IntentCode, "codeVerb"},
		// Reference: these do not.
		{"Investigate why the export is slow", IntentNoCode, "investigateVerb"},
		{"Look into the crash reports", IntentNoCode, "investigateVerb"},
		{"Plan the fix for the login bug", IntentNoCode, "investigateVerb"},
		{"Propose a fix but don't change any code", IntentNoCode, "explicitNoCode"},
		{"Create tasks to fix the onboarding bugs", IntentNoCode, "explicitNoCode"},
		{"Break this down into sub-issues", IntentNoCode, "explicitNoCode"},
		{"Write a plan for the auth rewrite", IntentNoCode, "explicitNoCode"},
		{"Fix it later; for now don't open a PR", IntentNoCode, "explicitNoCode"},
		{"Just investigate, we'll fix it next sprint", IntentNoCode, "explicitNoCode"},
		{"What causes the 500 on /api/export?", IntentNoCode, "investigateVerb"},
		{"Summarize the discussion", IntentNoCode, "investigateVerb"},
		{"调查一下导出慢的原因", IntentNoCode, "investigateVerb"},
		{"把这个拆分成子任务", IntentNoCode, "explicitNoCode"},
		{"不要修改代码，只分析", IntentNoCode, "explicitNoCode"},
		// Nothing matches and there is no model.
		{"The dashboard header", IntentUnclear, "default"},
		{"", IntentUnclear, "default"},
	}
	for _, c := range cases {
		got := ClassifyCodingIntent(context.Background(), c.request, nil)
		if got.Intent != c.want || got.Rule != c.rule {
			t.Errorf("%q: got %s/%s (%q), want %s/%s", c.request, got.Intent, got.Rule, got.Match, c.want, c.rule)
		}
	}
}

func TestClassifyCodingIntentModelFallback(t *testing.T) {
	model := &fakeIntentModel{answers: map[string]CodingIntent{
		"The dashboard header overlaps the sidebar on mobile": IntentCode,
		"Thoughts on the header?":                             IntentNoCode,
		"hmm":                                                 "nonsense",
	}}
	cases := []struct {
		request string
		want    CodingIntent
		rule    string
	}{
		{"The dashboard header overlaps the sidebar on mobile", IntentCode, "model"},
		{"Thoughts on the header?", IntentNoCode, "model"},
		{"hmm", IntentUnclear, "default"}, // an invalid answer is ignored
		{"Fix the header", IntentCode, "codeVerb"},
	}
	for _, c := range cases {
		got := ClassifyCodingIntent(context.Background(), c.request, model)
		if got.Intent != c.want || got.Rule != c.rule {
			t.Errorf("%q: got %s/%s, want %s/%s", c.request, got.Intent, got.Rule, c.want, c.rule)
		}
	}
	if model.calls != 3 {
		t.Fatalf("the model must only see requests the rules cannot classify: %d calls", model.calls)
	}

	failing := &fakeIntentModel{err: errors.New("upstream down")}
	got := ClassifyCodingIntent(context.Background(), "The dashboard header", failing)
	if got.Intent != IntentUnclear || !got.StartsCoding() {
		t.Fatalf("a model error must not block: %+v", got)
	}
}

func TestIntentDecisionStartsCoding(t *testing.T) {
	if (IntentDecision{Intent: IntentNoCode}).StartsCoding() {
		t.Fatal("noCode must not start")
	}
	for _, intent := range []CodingIntent{IntentCode, IntentUnclear} {
		if !(IntentDecision{Intent: intent}).StartsCoding() {
			t.Fatalf("%s must start", intent)
		}
	}
	if guidance := NoCodeGuidance(IntentDecision{Intent: IntentNoCode, Match: "investigate"}); guidance == "" {
		t.Fatal("guidance is empty")
	}
}
