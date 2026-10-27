package coding

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestParseTurnScopeTrickyPhrasings(t *testing.T) {
	cases := []struct {
		request string
		scope   string
		sure    bool
	}{
		// Negation reaches only its own verb; targets, specific objects and
		// conditions the turn meets keep a development request a write turn.
		{"修复登录 bug，不要提交到 main，改完推到分支", TurnScopeWrite, true},
		{"实现导出功能。不要直接推 main，推到当前分支就行", TurnScopeWrite, true},
		{"Fix the flaky test but don't commit the debug logs.", TurnScopeWrite, true},
		{"Don't push until the tests pass; fix the lint errors first.", TurnScopeWrite, true},
		{"测试通过前不要推送，先修复失败的测试", TurnScopeWrite, true},
		{"Never force-push; rebase onto main and push the branch.", TurnScopeWrite, true},
		{"Don't modify the tests; fix the implementation instead.", TurnScopeWrite, true},
		{"不要修改测试，修复实现", TurnScopeWrite, true},
		{"保持接口不变，重构这个模块", TurnScopeWrite, true},
		{"Rename the API but keep the return type unchanged", TurnScopeWrite, true},
		{"Continue with the dark mode toggle and add tests.", TurnScopeWrite, true},
		{"不要提交 .env 文件，把配置读取改成环境变量", TurnScopeWrite, true},
		{"继续", TurnScopeWrite, true},
		{"LGTM, go ahead.", TurnScopeWrite, true},
		// Changes allowed, nothing committed or pushed.
		{"先改一下，但不要提交。", TurnScopeNoPublish, true},
		{"Try the refactor locally but do not commit or push yet.", TurnScopeNoPublish, true},
		{"Fix it, but don't push until I review.", TurnScopeNoPublish, true},
		{"在我确认之前不要推送，先把按钮改成蓝色", TurnScopeNoPublish, true},
		{"改完先别提交", TurnScopeNoPublish, true},
		// The repository and the PR stay as they are.
		{"Verify the restored state. Keep the repo and the existing PR unchanged.", TurnScopeReadOnly, true},
		{"Please leave the branch and the pull request as is and just report what you see.", TurnScopeReadOnly, true},
		{"Don't modify anything, just tell me how you would fix it.", TurnScopeReadOnly, true},
		{"Run the checks and report back without making any changes.", TurnScopeReadOnly, true},
		{"只验证恢复后的状态，保持仓库和现有 PR 不变。", TurnScopeReadOnly, true},
		{"检查一下测试是否通过，不要做任何修改。", TurnScopeReadOnly, true},
		{"只做验证，不要改代码", TurnScopeReadOnly, true},
		{"Only verify that the build passes.", TurnScopeReadOnly, true},
		{"Don't touch the existing PR, explain why CI is red.", TurnScopeReadOnly, true},
		// Unsure: the model decides when there is one.
		{"Why does the build fail on CI?", TurnScopeWrite, false},
		{"不要直接推到 main", TurnScopeWrite, false},
		{"Just check the README and fix the typos.", TurnScopeNoPublish, false},
		{"Don't open a PR yet, just commit locally.", TurnScopeNoPublish, false},
		{"Keep the repo unchanged, but fix the failing test.", TurnScopeReadOnly, false},
	}
	for _, c := range cases {
		got := ParseTurnScope(c.request)
		if got.Scope != c.scope || got.Sure != c.sure {
			t.Errorf("%q: got %s (sure %t, rule %s %q), want %s (sure %t)", c.request, got.Scope, got.Sure, got.Rule, got.Match, c.scope, c.sure)
		}
	}
}

type fakeScopeModel struct {
	answer string
	err    error
	delay  time.Duration
	calls  int
	seen   string
}

func (m *fakeScopeModel) ClassifyTurnScope(ctx context.Context, request string) (string, error) {
	m.calls++
	m.seen = request
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return m.answer, m.err
}

func TestResolveTurnScopePrecedenceAndFallback(t *testing.T) {
	ctx := context.Background()

	// An explicit scope wins over the text, the kind and the model.
	model := &fakeScopeModel{answer: "readOnly"}
	if got := ResolveTurnScope(ctx, TurnFollowUp, "write", "Keep the repo unchanged.", model); got.Scope != TurnScopeWrite || got.Source != TurnScopeSourceUser || model.calls != 0 {
		t.Fatalf("explicit write: %+v, %d model calls", got, model.calls)
	}
	if got := ResolveTurnScope(ctx, TurnFollowUp, "readOnly", "Fix the login bug and push.", model); got.Scope != TurnScopeReadOnly || got.Source != TurnScopeSourceUser {
		t.Fatalf("explicit readOnly: %+v", got)
	}
	if got := ResolveTurnScope(ctx, TurnKindRebase, "", "Keep the branch unchanged otherwise.", model); got.Scope != TurnScopeWrite || got.Source != TurnScopeSourceKind {
		t.Fatalf("rebase turn: %+v", got)
	}
	if got := ResolveTurnScope(ctx, TurnKindRebase, "noPublish", "Rebase.", model); got.Scope != TurnScopeNoPublish || got.Source != TurnScopeSourceUser {
		t.Fatalf("explicit beats kind: %+v", got)
	}
	// "auto" infers; a sure parse never calls the model.
	if got := ResolveTurnScope(ctx, TurnFollowUp, "auto", "先改一下，但不要提交。", model); got.Scope != TurnScopeNoPublish || got.Source != TurnScopeSourceRules || model.calls != 0 {
		t.Fatalf("sure parse: %+v, %d model calls", got, model.calls)
	}
	// An unsure parse asks the model once.
	if got := ResolveTurnScope(ctx, TurnFollowUp, "", "Why does the build fail on CI?", model); got.Scope != TurnScopeReadOnly || got.Source != TurnScopeSourceModel || model.calls != 1 {
		t.Fatalf("model decides: %+v, %d model calls", got, model.calls)
	}
	// Model failures keep the parser's answer.
	for name, failing := range map[string]*fakeScopeModel{
		"error":   {err: errors.New("provider down")},
		"garbage": {answer: "I think it should be write-ish"},
		"empty":   {answer: ""},
	} {
		got := ResolveTurnScope(ctx, TurnFollowUp, "", "Just check the README and fix the typos.", failing)
		if got.Scope != TurnScopeNoPublish || got.Source != TurnScopeSourceRules || failing.calls != 1 {
			t.Fatalf("%s: %+v", name, got)
		}
	}
	if got := ResolveTurnScope(ctx, TurnFollowUp, "", "Why does the build fail on CI?", nil); got.Scope != TurnScopeWrite || got.Source != TurnScopeSourceRules {
		t.Fatalf("no model: %+v", got)
	}
	// A slow model is cut off by the caller's deadline and falls back.
	slow := &fakeScopeModel{answer: "readOnly", delay: time.Second}
	short, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if got := ResolveTurnScope(short, TurnFollowUp, "", "Why does the build fail on CI?", slow); got.Source != TurnScopeSourceRules || got.Scope != TurnScopeWrite {
		t.Fatalf("slow model: %+v", got)
	}
	// The input the model sees is bounded.
	long := &fakeScopeModel{answer: "write"}
	request := "Why? " + string(make([]rune, 5000))
	ResolveTurnScope(ctx, TurnFollowUp, "", request, long)
	if n := len([]rune(long.seen)); n > turnScopeModelInput {
		t.Fatalf("model input not bounded: %d runes", n)
	}
}

func TestNormalizeTurnScope(t *testing.T) {
	for in, want := range map[string]string{"": "", "auto": "", "write": TurnScopeWrite, "noPublish": TurnScopeNoPublish, "readOnly": TurnScopeReadOnly, "read-only": TurnScopeReadOnly} {
		if got, err := NormalizeTurnScope(in); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	if _, err := NormalizeTurnScope("publish"); !errors.Is(err, ErrInvalidTurnScope) {
		t.Fatalf("invalid scope accepted: %v", err)
	}
}

func TestParseIncludeLeftovers(t *testing.T) {
	cases := []struct {
		request string
		want    bool
	}{
		// Explicit requests to commit the earlier uncommitted changes too.
		{"把之前未提交的改动也提交", true},
		{"把之前未提交的改动也提交了吧", true},
		{"修复登录 bug，然后把之前没提交的修改一起提交", true},
		{"之前未提交的改动，也一起提交", true},
		{"提交时包括之前未提交的改动", true},
		{"把遗留的改动也提交上去", true},
		{"全部提交", true},
		{"所有改动都提交", true},
		{"提交所有改动", true},
		{"不要修改测试，把之前未提交的改动也提交", true},
		{"Include the earlier uncommitted changes.", true},
		{"Fix the typo and include the earlier uncommitted changes in the commit.", true},
		{"Please commit the leftover changes too.", true},
		{"Commit the previous uncommitted edits as well", true},
		{"Commit everything.", true},
		{"Fix the lint errors and commit everything", true},
		{"commit all the changes", true},
		{"Don't forget to commit the earlier uncommitted changes.", true},
		{"别忘了把之前未提交的改动也提交", true},
		// Negations keep them out.
		{"不要提交之前的改动", false},
		{"不要提交之前未提交的改动", false},
		{"之前未提交的改动不要提交", false},
		{"修复 bug，但不要提交之前的改动", false},
		{"别把之前的改动提交了", false},
		{"Don't commit the earlier uncommitted changes.", false},
		{"Fix it but do not include the earlier uncommitted changes.", false},
		{"Never commit everything at once.", false},
		{"Keep the earlier uncommitted changes out of the commit.", false},
		{"Commit everything except the .env file.", false},
		// No request about them at all.
		{"", false},
		{"Fix the login bug and push.", false},
		{"修复登录 bug，改完推到分支", false},
		{"提交之前先跑一下测试", false},
		{"所有测试通过后提交", false},
		{"提交所有测试文件", false},
		{"Add tests for the previous changes.", false},
		{"Revert the earlier changes to the theme.", false},
		{"Why are there uncommitted changes in the workspace?", false},
	}
	for _, c := range cases {
		if got := ParseIncludeLeftovers(c.request); got != c.want {
			t.Errorf("%q: got %t, want %t", c.request, got, c.want)
		}
	}
}
