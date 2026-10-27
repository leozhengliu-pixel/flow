package codingstore

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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
	var asked atomic.Int32 // the model is asked in the background
	h.svc.TurnScopeModel = coding.TurnScopeModelFunc(func(context.Context, string) (string, error) {
		asked.Add(1)
		return coding.TurnScopeReadOnly, nil
	})
	var after int64
	next := func(input coding.PromptInput) coding.Turn {
		t.Helper()
		input.AuthorID = "user1"
		if _, err := h.svc.Prompt(ctx, workspace, session.ID, input); err != nil {
			t.Fatal(err)
		}
		// An unsure request is settled in the background: the long poll
		// returns it once the model answered.
		inbox, err := h.svc.Inbox(ctx, token, after, 10*time.Second)
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
	if asked.Load() != 1 {
		t.Fatalf("the model is asked only for the unsure request: %d calls", asked.Load())
	}
	if _, err := h.svc.Prompt(ctx, workspace, session.ID, coding.PromptInput{Prompt: "Fix it.", AuthorID: "user1", Scope: "publishEverything"}); err == nil {
		t.Fatal("an unknown scope is refused")
	}
}

// followUpSession runs a session's first turn and returns it with its
// sandbox token, ready for follow-ups.
func followUpSession(t *testing.T, h *harness) (domain.CodingSession, string) {
	t.Helper()
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
	return session, token
}

// inboxTurns are the turn items of an inbox answer.
func inboxTurns(inbox coding.InboxResponse) []coding.InboxItem {
	var turns []coding.InboxItem
	for _, item := range inbox.Items {
		if item.Kind == coding.InboxTurn && item.Turn != nil {
			turns = append(turns, item)
		}
	}
	return turns
}

// storedTurn is the queued item of a turn as stored.
func storedTurn(t *testing.T, h *harness, sessionID, turnID string) coding.InboxItem {
	t.Helper()
	items, err := h.store.CodingInbox(context.Background(), sessionID, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Turn != nil && item.Turn.ID == turnID {
			return item
		}
	}
	t.Fatalf("turn %s is not queued: %+v", turnID, items)
	return coding.InboxItem{}
}

// Sending a follow-up the parser is unsure about never waits on the model:
// the turn is queued at once, held back (with the turns behind it) while
// its scope is pending, and delivered with the model's answer.
func TestUnsureFollowUpIsQueuedAtOnceAndWaitsForItsScope(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	session, token := followUpSession(t, h)
	calls, release := make(chan context.Context, 1), make(chan string)
	h.svc.TurnScopeModel = coding.TurnScopeModelFunc(func(ctx context.Context, _ string) (string, error) {
		calls <- ctx
		select {
		case answer := <-release:
			return answer, nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	})

	request, cancel := context.WithCancel(ctx)
	began := time.Now()
	if _, err := h.svc.Prompt(request, workspace, session.ID, coding.PromptInput{Prompt: "Why does the build fail on CI?", AuthorID: "user1", TurnID: "turn_unsure"}); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(began); elapsed > time.Second {
		t.Fatalf("Prompt waited %s for the model", elapsed)
	}
	cancel() // the request ends; the classification carries on
	if _, err := h.svc.Prompt(ctx, workspace, session.ID, coding.PromptInput{Prompt: "Fix the login bug and push.", AuthorID: "user1", TurnID: "turn_sure"}); err != nil {
		t.Fatal(err)
	}
	var modelCtx context.Context
	select {
	case modelCtx = <-calls:
	case <-time.After(5 * time.Second):
		t.Fatal("the model was not asked")
	}
	if deadline, ok := modelCtx.Deadline(); !ok || time.Until(deadline) > coding.TurnScopeModelTimeout {
		t.Fatalf("the model call is bounded by the timeout: %v %v", deadline, ok)
	}
	if queued := storedTurn(t, h, session.ID, "turn_unsure").Turn; queued.ScopePendingSince == 0 || queued.Scope != coding.TurnScopeWrite || queued.ScopeSource != coding.TurnScopeSourceRules {
		t.Fatalf("queued with the parser's answer, pending: %+v", queued)
	}

	// Pending: neither the turn nor the one behind it is delivered.
	inbox, err := h.svc.Inbox(ctx, token, 0, 300*time.Millisecond)
	if err != nil || len(inboxTurns(inbox)) != 0 {
		t.Fatalf("a pending turn was delivered: %+v %v", inbox, err)
	}
	if modelCtx.Err() != nil {
		t.Fatalf("the classification was cancelled with the request: %v", modelCtx.Err())
	}

	release <- coding.TurnScopeReadOnly
	inbox, err = h.svc.Inbox(ctx, token, 0, 10*time.Second)
	turns := inboxTurns(inbox)
	if err != nil || len(turns) != 1 || turns[0].Turn.ID != "turn_unsure" {
		t.Fatalf("the settled turn is delivered first: %+v %v", inbox, err)
	}
	if turn := turns[0].Turn; turn.Scope != coding.TurnScopeReadOnly || turn.ScopeSource != coding.TurnScopeSourceModel || turn.ScopePendingSince != 0 {
		t.Fatalf("delivered with the model's scope: %+v", turn)
	}
	if _, err := h.svc.CompleteTurn(ctx, token, "turn_unsure", coding.TurnCompleteRequest{Status: coding.TurnSuccess, Scope: coding.TurnScopeReadOnly}); err != nil {
		t.Fatal(err)
	}
	inbox, err = h.svc.Inbox(ctx, token, turns[0].Seq, 5*time.Second)
	if turns = inboxTurns(inbox); err != nil || len(turns) != 1 || turns[0].Turn.ID != "turn_sure" || turns[0].Turn.Scope != coding.TurnScopeWrite {
		t.Fatalf("the next turn follows: %+v %v", inbox, err)
	}
}

// A follow-up that asks to commit the earlier uncommitted changes (in words
// or with the explicit flag) reaches the runner with includeLeftovers; other
// follow-ups and the turns Flow starts itself do not, and a model-settled
// scope keeps the flag.
func TestFollowUpTurnsCarryIncludeLeftovers(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	session, token := followUpSession(t, h)
	h.svc.TurnScopeModel = coding.TurnScopeModelFunc(func(context.Context, string) (string, error) { return coding.TurnScopeWrite, nil })
	var after int64
	for _, c := range []struct {
		input coding.PromptInput
		want  bool
	}{
		{coding.PromptInput{Prompt: "把之前未提交的改动也提交"}, true},
		{coding.PromptInput{Prompt: "Commit the earlier uncommitted changes.", Scope: "write", IncludeLeftovers: true}, true},
		{coding.PromptInput{Prompt: "不要提交之前的改动，修复登录 bug"}, false},
		{coding.PromptInput{Prompt: "Fix the login bug and push."}, false},
		{coding.PromptInput{Prompt: "Commit everything.", Kind: coding.TurnKindFixPR}, false},
	} {
		c.input.AuthorID = "user1"
		if _, err := h.svc.Prompt(ctx, workspace, session.ID, c.input); err != nil {
			t.Fatal(err)
		}
		inbox, err := h.svc.Inbox(ctx, token, after, 10*time.Second)
		turns := inboxTurns(inbox)
		if err != nil || len(turns) != 1 {
			t.Fatalf("%q: inbox %+v %v", c.input.Prompt, inbox, err)
		}
		after = turns[0].Seq
		turn := turns[0].Turn
		if turn.IncludeLeftovers != c.want {
			t.Errorf("%q: includeLeftovers %t, want %t", c.input.Prompt, turn.IncludeLeftovers, c.want)
		}
		if _, err := h.svc.CompleteTurn(ctx, token, turn.ID, coding.TurnCompleteRequest{Status: coding.TurnSuccess, Scope: turn.Scope}); err != nil {
			t.Fatal(err)
		}
	}
}

// A model error or timeout settles the pending scope with the parser's
// answer.
func TestPendingScopeFallsBackToTheParserOnModelFailure(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	session, token := followUpSession(t, h)
	h.svc.TurnScopeModel = coding.TurnScopeModelFunc(func(ctx context.Context, request string) (string, error) {
		if strings.Contains(request, "README") {
			return "", errors.New("overloaded")
		}
		<-ctx.Done() // never answers: the timeout ends the call
		return "", ctx.Err()
	})
	var after int64
	for _, c := range []struct{ turn, prompt, scope string }{
		{"turn_error", "Just check the README and fix the typos.", coding.TurnScopeNoPublish},
		{"turn_timeout", "Why does the build fail on CI?", coding.TurnScopeWrite},
	} {
		if _, err := h.svc.Prompt(ctx, workspace, session.ID, coding.PromptInput{Prompt: c.prompt, AuthorID: "user1", TurnID: c.turn}); err != nil {
			t.Fatal(err)
		}
		inbox, err := h.svc.Inbox(ctx, token, after, coding.TurnScopeModelTimeout+5*time.Second)
		turns := inboxTurns(inbox)
		if err != nil || len(turns) != 1 || turns[0].Turn.ID != c.turn {
			t.Fatalf("%s: %+v %v", c.turn, inbox, err)
		}
		if turn := turns[0].Turn; turn.Scope != c.scope || turn.ScopeSource != coding.TurnScopeSourceRules || turn.ScopePendingSince != 0 {
			t.Fatalf("%s falls back to the parser: %+v", c.turn, turn)
		}
		after = turns[0].Seq
		if _, err := h.svc.CompleteTurn(ctx, token, c.turn, coding.TurnCompleteRequest{Status: coding.TurnSuccess, Scope: c.scope}); err != nil {
			t.Fatal(err)
		}
	}
}

// A pending scope whose settling was lost (the API restarted during the
// model call) does not hold the turn forever: once stale, dispatch falls
// back to the parser's answer.
func TestStalePendingScopeFallsBackAtDispatch(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	session, token := followUpSession(t, h)
	// No model and no settler: what is left after a restart.
	turn := coding.Turn{ID: "turn_orphan", Prompt: "Just check the README and fix the typos.", AuthorID: "user1", Kind: coding.TurnFollowUp, Scope: coding.TurnScopeNoPublish, ScopeSource: coding.TurnScopeSourceRules, ScopePendingSince: time.Now().UnixMilli()}
	seq, err := h.store.EnqueueCodingInbox(ctx, workspace, session.ID, coding.InboxItem{Kind: coding.InboxTurn, Turn: &turn})
	if err != nil {
		t.Fatal(err)
	}
	if inbox, err := h.svc.Inbox(ctx, token, 0, 0); err != nil || len(inboxTurns(inbox)) != 0 {
		t.Fatalf("a fresh pending scope holds the turn: %+v %v", inbox, err)
	}
	if queued := storedTurn(t, h, session.ID, "turn_orphan").Turn; queued.ScopePendingSince == 0 {
		t.Fatalf("still pending: %+v", queued)
	}
	// Time passes beyond TurnScopePendingStale.
	stale := time.Now().Add(-coding.TurnScopePendingStale - time.Second).UnixMilli()
	if ok, err := h.store.UpdateUndeliveredCodingTurn(ctx, session.ID, seq, func(turn *coding.Turn) bool {
		turn.ScopePendingSince = stale
		return true
	}); err != nil || !ok {
		t.Fatalf("age the mark: %v %v", ok, err)
	}
	inbox, err := h.svc.Inbox(ctx, token, 0, 0)
	turns := inboxTurns(inbox)
	if err != nil || len(turns) != 1 || turns[0].Turn.ID != "turn_orphan" {
		t.Fatalf("the stale turn is delivered: %+v %v", inbox, err)
	}
	if got := turns[0].Turn; got.Scope != coding.TurnScopeNoPublish || got.ScopeSource != coding.TurnScopeSourceRules || got.ScopePendingSince != 0 {
		t.Fatalf("delivered with the parser's answer: %+v", got)
	}
	// The fallback is stored, and a late settle cannot rewrite the turn.
	if ok, err := h.store.UpdateUndeliveredCodingTurn(ctx, session.ID, seq, func(*coding.Turn) bool { return true }); err != nil || ok {
		t.Fatalf("a delivered turn is not rewritten: %v %v", ok, err)
	}
	if stored := storedTurn(t, h, session.ID, "turn_orphan").Turn; stored.ScopePendingSince != 0 {
		t.Fatalf("stored: %+v", stored)
	}
}
