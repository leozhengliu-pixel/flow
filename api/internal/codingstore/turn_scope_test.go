package codingstore

import (
	"context"
	"strings"
	"testing"

	"flow/api/internal/coding"
	"flow/api/internal/domain"
)

// A follow-up turn's write scope reaches the publish stage: a read-only
// (or no-commit) turn on a session with a draft PR leaves the PR alone (no
// publish phase, no head check, no title change), while a development
// follow-up publishes as before.
func TestReadOnlyFollowUpDoesNotPublishButADevelopmentFollowUpDoes(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	session := h.start("Add dark mode")
	session = h.drive(session.ID, domain.CodingPhaseStarting)
	h.simulateSandboxes()
	token := h.tokens[session.SandboxID]
	boot, err := h.svc.Bootstrap(ctx, token, 0)
	if err != nil || boot.Turn == nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if _, err := h.svc.Heartbeat(ctx, token, coding.HeartbeatRequest{Phase: "running"}); err != nil {
		t.Fatal(err)
	}
	// The feature turn (a runner without the scope field): published as before.
	if session, err = h.svc.CompleteTurn(ctx, token, boot.Turn.ID, coding.TurnCompleteRequest{Status: coding.TurnSuccess, Pushed: true, HeadSHA: "abc123", DisplayTitle: "Add dark mode toggle", Commits: []domain.CodingCommit{{SHA: "abc123", Message: "Add dark mode"}}}); err != nil || session.Phase != domain.CodingPhasePublishing {
		t.Fatalf("feature turn: %s %v", session.Phase, err)
	}
	session = h.drive(session.ID, domain.CodingPhaseIdle)
	if session.LatestPullRequest == nil || h.host.created != 1 {
		t.Fatalf("the draft PR exists: %+v", session.LatestPullRequest)
	}
	checks, title, prHead := h.host.branchHeads, session.DisplayTitle, session.LatestPullRequest.HeadSHA

	follow := func(prompt string, after int64) (coding.Turn, int64) {
		t.Helper()
		if _, err := h.svc.Prompt(ctx, workspace, session.ID, coding.PromptInput{Prompt: prompt, AuthorID: "user1"}); err != nil {
			t.Fatal(err)
		}
		inbox, err := h.svc.Inbox(ctx, token, after, 0)
		if err != nil || len(inbox.Items) != 1 || inbox.Items[0].Turn == nil || inbox.Items[0].Turn.Prompt != prompt {
			t.Fatalf("inbox: %+v %v", inbox, err)
		}
		return *inbox.Items[0].Turn, inbox.Items[0].Seq
	}

	// Read-only verification turn: nothing pushed, scope reported.
	verify, seq := follow("Verify the restored state. Keep the repo and the existing PR unchanged.", 0)
	session, err = h.svc.CompleteTurn(ctx, token, verify.ID, coding.TurnCompleteRequest{Status: coding.TurnSuccess, Pushed: false, Scope: coding.TurnScopeReadOnly, Summary: "All checks pass.", DisplayTitle: "Verified the restored state"})
	if err != nil || session.Phase != domain.CodingPhaseIdle {
		t.Fatalf("a read-only turn goes straight to idle: %s %v", session.Phase, err)
	}
	session = h.drive(session.ID, domain.CodingPhaseIdle)
	if h.host.branchHeads != checks || h.host.created != 1 {
		t.Fatalf("no publish-stage call for a read-only turn: %d head checks (was %d), %d PRs", h.host.branchHeads, checks, h.host.created)
	}
	if session.DisplayTitle != title || session.HeadSHA != "abc123" || session.LatestPullRequest == nil || session.LatestPullRequest.HeadSHA != prHead {
		t.Fatalf("title, head and PR stay: %q %q %+v", session.DisplayTitle, session.HeadSHA, session.LatestPullRequest)
	}

	// A no-commit turn behaves the same.
	draft, seq := follow("Try it locally but do not commit or push.", seq)
	if session, err = h.svc.CompleteTurn(ctx, token, draft.ID, coding.TurnCompleteRequest{Status: coding.TurnSuccess, Scope: coding.TurnScopeNoPublish}); err != nil || session.Phase != domain.CodingPhaseIdle || h.host.branchHeads != checks {
		t.Fatalf("no-commit turn: %s %v %d", session.Phase, err, h.host.branchHeads)
	}

	// A development follow-up publishes: one publish stage, the PR head moves.
	dev, _ := follow("Continue: add the toggle to the settings page.", seq)
	h.host.mu.Lock()
	h.host.head = "def456"
	h.host.mu.Unlock()
	session, err = h.svc.CompleteTurn(ctx, token, dev.ID, coding.TurnCompleteRequest{Status: coding.TurnSuccess, Pushed: true, Scope: coding.TurnScopeWrite, HeadSHA: "def456", DisplayTitle: "Add the settings toggle", Commits: []domain.CodingCommit{{SHA: "def456", Message: "Add the settings toggle"}}})
	if err != nil || session.Phase != domain.CodingPhasePublishing {
		t.Fatalf("a development turn publishes: %s %v", session.Phase, err)
	}
	session = h.drive(session.ID, domain.CodingPhaseIdle)
	if h.host.branchHeads != checks+1 || h.host.created != 1 || session.LatestPullRequest == nil || session.LatestPullRequest.HeadSHA != "def456" || session.DisplayTitle != "Add the settings toggle" {
		t.Fatalf("published once onto the existing PR: %d checks, %d PRs, %+v %q", h.host.branchHeads-checks, h.host.created, session.LatestPullRequest, session.DisplayTitle)
	}
}

func TestGuidanceMakesTheIssueBackgroundToTheLatestRequest(t *testing.T) {
	guidance := coding.Guidance(coding.GuidanceInput{Issue: coding.IssueContext{Identifier: "ABC-1", Title: "Add dark mode", Description: "Add a toggle."}, BranchName: "flow/abc-1"})
	if !strings.Contains(guidance, "## Issue (background)") || !strings.Contains(guidance, "Each turn's goal is the latest user message") || strings.Contains(guidance, "Flow commits and pushes your work at the end of the turn") {
		t.Fatalf("guidance:\n%s", guidance)
	}
}

// The API decides a follow-up's write scope when it is queued and sends it
// with the turn: an explicit scope wins over the text, otherwise the request
// is classified (the model only for requests the parser is unsure about).
func TestFollowUpTurnsCarryTheirScope(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	session := h.start("Add dark mode")
	session = h.drive(session.ID, domain.CodingPhaseStarting)
	h.simulateSandboxes()
	token := h.tokens[session.SandboxID]
	boot, err := h.svc.Bootstrap(ctx, token, 0)
	if err != nil || boot.Turn == nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if _, err := h.svc.Heartbeat(ctx, token, coding.HeartbeatRequest{Phase: "running"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.CompleteTurn(ctx, token, boot.Turn.ID, coding.TurnCompleteRequest{Status: coding.TurnSuccess}); err != nil {
		t.Fatal(err)
	}
	asked := 0
	h.svc.TurnScopeModel = coding.TurnScopeModelFunc(func(context.Context, string) (string, error) {
		asked++
		return coding.TurnScopeReadOnly, nil
	})
	var after int64
	next := func(input coding.PromptInput) coding.Turn {
		t.Helper()
		input.AuthorID = "user1"
		if _, err := h.svc.Prompt(ctx, workspace, session.ID, input); err != nil {
			t.Fatal(err)
		}
		inbox, err := h.svc.Inbox(ctx, token, after, 0)
		if err != nil || len(inbox.Items) != 1 || inbox.Items[0].Turn == nil {
			t.Fatalf("inbox: %+v %v", inbox, err)
		}
		after = inbox.Items[0].Seq
		turn := *inbox.Items[0].Turn
		if _, err := h.svc.CompleteTurn(ctx, token, turn.ID, coding.TurnCompleteRequest{Status: coding.TurnSuccess, Scope: turn.Scope}); err != nil {
			t.Fatal(err)
		}
		return turn
	}
	for _, c := range []struct {
		input         coding.PromptInput
		scope, source string
	}{
		{coding.PromptInput{Prompt: "修复登录 bug，不要提交到 main，改完推到分支"}, coding.TurnScopeWrite, coding.TurnScopeSourceRules},
		{coding.PromptInput{Prompt: "Fix it, but don't push until I review."}, coding.TurnScopeNoPublish, coding.TurnScopeSourceRules},
		{coding.PromptInput{Prompt: "Fix the login bug and push.", Scope: "readOnly"}, coding.TurnScopeReadOnly, coding.TurnScopeSourceUser},
		{coding.PromptInput{Prompt: "Keep the repo unchanged.", Scope: "write"}, coding.TurnScopeWrite, coding.TurnScopeSourceUser},
		{coding.PromptInput{Prompt: "Why does the build fail on CI?", Scope: "auto"}, coding.TurnScopeReadOnly, coding.TurnScopeSourceModel},
	} {
		turn := next(c.input)
		if turn.Scope != c.scope || turn.ScopeSource != c.source {
			t.Errorf("%q (%q): scope %q from %q, want %q from %q", c.input.Prompt, c.input.Scope, turn.Scope, turn.ScopeSource, c.scope, c.source)
		}
	}
	if asked != 1 {
		t.Fatalf("the model is asked only for the unsure request: %d calls", asked)
	}
	if _, err := h.svc.Prompt(ctx, workspace, session.ID, coding.PromptInput{Prompt: "Fix it.", AuthorID: "user1", Scope: "publishEverything"}); err == nil {
		t.Fatal("an unknown scope is refused")
	}
}
