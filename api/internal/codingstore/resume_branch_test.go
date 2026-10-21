package codingstore

import (
	"context"
	"errors"
	"strings"
	"testing"

	"flow/api/internal/coding"
	"flow/api/internal/domain"
)

// Continuing a session whose branch may never have reached the code host:
// the bootstrap may only claim an existing branch on explicit publish
// evidence (a successful push, a PR/MR, a PR-sourced session), never because
// the last turn reported a head SHA (that can be the clone baseline).

const cloneBaseline = "1111111111111111111111111111111111111111"

// stoppedFirstTurn runs a session's first turn and stops it; complete is the
// runner's stopped report.
func stoppedFirstTurn(t *testing.T, h *harness, prompt string, complete coding.TurnCompleteRequest) domain.CodingSession {
	t.Helper()
	ctx := context.Background()
	session := h.start(prompt)
	session = h.drive(session.ID, domain.CodingPhaseStarting)
	h.simulateSandboxes()
	token := h.tokens[session.SandboxID]
	boot, err := h.svc.Bootstrap(ctx, token, 0)
	if err != nil || boot.Turn == nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if boot.Repo.ExistingBranch || boot.Repo.BaseSHA != "" {
		t.Fatalf("a new session creates its branch from the base ref: %+v", boot.Repo)
	}
	if _, err := h.svc.Heartbeat(ctx, token, coding.HeartbeatRequest{Phase: "running"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Stop(ctx, workspace, session.ID, "user1", false); err != nil {
		t.Fatal(err)
	}
	h.drive(session.ID, domain.CodingPhaseStopping)
	complete.Status = coding.TurnStopped
	if _, err := h.svc.CompleteTurn(ctx, token, boot.Turn.ID, complete); err != nil {
		t.Fatal(err)
	}
	h.exec(`UPDATE coding_sandboxes SET desired='stopped' WHERE id=?`, session.SandboxID)
	return h.drive(session.ID, domain.CodingPhaseDone)
}

// continueBootstrap continues a finished session and returns the next sandbox's bootstrap.
func continueBootstrap(t *testing.T, h *harness, id string) coding.BootstrapResponse {
	t.Helper()
	ctx := context.Background()
	if _, err := h.svc.Prompt(ctx, workspace, id, coding.PromptInput{Prompt: "Continue", AuthorID: "user1"}); err != nil {
		t.Fatal(err)
	}
	session := h.drive(id, domain.CodingPhaseStarting)
	h.simulateSandboxes()
	boot, err := h.svc.Bootstrap(ctx, h.tokens[session.SandboxID], 0)
	if err != nil {
		t.Fatal(err)
	}
	return boot
}

func TestContinueAfterStopBeforeAnyCommitCreatesTheBranchFromTheBaseline(t *testing.T) {
	h := newHarness(t)
	// Stopped right after the clone: no changes, no commits, nothing pushed;
	// the reported head is the clone baseline.
	session := stoppedFirstTurn(t, h, "Stop after clone", coding.TurnCompleteRequest{HeadSHA: cloneBaseline})
	if session.HeadSHA != cloneBaseline || session.Pushed || session.ExistingBranch {
		t.Fatalf("after the stop: head %q pushed %v existing %v", session.HeadSHA, session.Pushed, session.ExistingBranch)
	}
	boot := continueBootstrap(t, h, session.ID)
	if boot.Repo.ExistingBranch {
		t.Fatal("an unpublished branch must not be checked out as an existing remote branch")
	}
	if boot.Repo.BaseSHA != cloneBaseline || boot.Repo.BaseRef != "main" || boot.Repo.BranchName != session.BranchName {
		t.Fatalf("the branch is created again from the exact clone baseline: %+v", boot.Repo)
	}
	if after := h.session(session.ID); after.ExistingBranch {
		t.Fatal("the session must not record an unpublished branch as existing")
	}
}

func TestContinueRepeatedIsIdempotent(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	session := stoppedFirstTurn(t, h, "Repeat", coding.TurnCompleteRequest{HeadSHA: cloneBaseline})
	for range 2 {
		if _, err := h.svc.Prompt(ctx, workspace, session.ID, coding.PromptInput{Prompt: "Continue", AuthorID: "user1"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.svc.Restart(ctx, workspace, session.ID, "user1", false); !errors.Is(err, coding.ErrConflict) {
		t.Fatalf("a restart of a re-queued session conflicts: %v", err)
	}
	queued := h.session(session.ID)
	if queued.Phase != domain.CodingPhaseQueued || queued.ExistingBranch || queued.HeadSHA != cloneBaseline {
		t.Fatalf("repeated continues: %+v", queued)
	}
	queued = h.drive(session.ID, domain.CodingPhaseStarting)
	h.simulateSandboxes()
	boot, err := h.svc.Bootstrap(ctx, h.tokens[queued.SandboxID], 0)
	if err != nil || boot.Repo.ExistingBranch || boot.Repo.BaseSHA != cloneBaseline {
		t.Fatalf("bootstrap after repeated continues: %+v %v", boot.Repo, err)
	}
	if records, _ := h.store.CodingSandboxes(ctx, workspace, session.ID); len(records) != 2 {
		t.Fatalf("one new sandbox for the continued session, got %d records", len(records))
	}
}

func TestContinueAfterRestartBeforeAnyCommit(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	session := stoppedFirstTurn(t, h, "Restart after clone", coding.TurnCompleteRequest{HeadSHA: cloneBaseline})
	restarted, err := h.svc.Restart(ctx, workspace, session.ID, "user1", false)
	if err != nil || restarted.ExistingBranch {
		t.Fatalf("restart: existing %v %v", restarted.ExistingBranch, err)
	}
	restarted = h.drive(session.ID, domain.CodingPhaseStarting)
	h.simulateSandboxes()
	boot, err := h.svc.Bootstrap(ctx, h.tokens[restarted.SandboxID], 0)
	if err != nil || boot.Repo.ExistingBranch || boot.Repo.BaseSHA != cloneBaseline {
		t.Fatalf("bootstrap after restart: %+v %v", boot.Repo, err)
	}
}

func TestContinueAfterUnpushedCommitsSaysSo(t *testing.T) {
	h := newHarness(t)
	// The stop's WIP commit (uncommitted changes) or earlier commits could
	// not be pushed: the commits are recorded, the push is not.
	wip := "2222222222222222222222222222222222222222"
	session := stoppedFirstTurn(t, h, "Unpushed commits", coding.TurnCompleteRequest{
		HeadSHA: wip, Commits: []domain.CodingCommit{{SHA: wip, Message: "WIP: stopped by user"}},
		Error: &coding.ErrorDetail{Code: domain.CodingErrPushFailed, Message: "Pushing failed"},
	})
	boot := continueBootstrap(t, h, session.ID)
	if boot.Repo.ExistingBranch {
		t.Fatal("unpushed commits are no evidence of a remote branch")
	}
	if boot.Repo.BaseSHA != "" {
		t.Fatalf("an unpushed head is not a baseline the code host knows: %q", boot.Repo.BaseSHA)
	}
	found := false
	for _, activity := range h.activities(session.AgentSessionID) {
		if strings.Contains(activity.Body, "were never pushed") && strings.Contains(activity.Body, session.BranchName) {
			found = true
		}
	}
	if !found {
		t.Fatal("continuing without the unpushed commits must say so on the session")
	}
}

func TestContinueAfterPushKeepsTheExistingBranch(t *testing.T) {
	h := newHarness(t)
	// Pushed (the stop's WIP commit), no PR yet: Pushed is cleared when the
	// session ends, the published branch is remembered.
	wip := "3333333333333333333333333333333333333333"
	session := stoppedFirstTurn(t, h, "Pushed no PR", coding.TurnCompleteRequest{HeadSHA: wip, Pushed: true, Commits: []domain.CodingCommit{{SHA: wip, Message: "WIP"}}})
	if session.Pushed || !session.ExistingBranch || len(session.PullRequests) != 0 {
		t.Fatalf("after the stop: %+v", session)
	}
	boot := continueBootstrap(t, h, session.ID)
	if !boot.Repo.ExistingBranch || boot.Repo.BaseSHA != "" {
		t.Fatalf("a pushed branch is checked out again: %+v", boot.Repo)
	}
}

func TestContinueWithPullRequestKeepsTheExistingBranch(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	session := stoppedFirstTurn(t, h, "With PR", coding.TurnCompleteRequest{HeadSHA: cloneBaseline})
	if _, err := h.store.UpdateCodingSession(ctx, workspace, session.ID, func(current *domain.CodingSession) error {
		current.PullRequests = []domain.SessionPullRequest{{Number: 7, URL: "https://github.com/acme/app/pull/7", HeadBranch: current.BranchName, State: "open"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if boot := continueBootstrap(t, h, session.ID); !boot.Repo.ExistingBranch {
		t.Fatalf("a branch with a PR is published: %+v", boot.Repo)
	}
}

func TestContinueRepairsAnExistingBranchFlagWithoutEvidence(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	// A session continued before this fix: the head-SHA guess marked the
	// never-pushed branch as existing.
	session := stoppedFirstTurn(t, h, "Earlier guess", coding.TurnCompleteRequest{HeadSHA: cloneBaseline})
	if _, err := h.store.UpdateCodingSession(ctx, workspace, session.ID, func(current *domain.CodingSession) error {
		current.ExistingBranch = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if boot := continueBootstrap(t, h, session.ID); boot.Repo.ExistingBranch || boot.Repo.BaseSHA != cloneBaseline {
		t.Fatalf("no commit was ever recorded, so the branch was never published: %+v", boot.Repo)
	}
}

// flaggedByTheOldGuess is a session whose ExistingBranch came from the old
// head-SHA heuristic: commits are recorded (never pushed), no push is.
func flaggedByTheOldGuess(t *testing.T, h *harness, prompt string, mutate func(*domain.CodingSession)) coding.BootstrapResponse {
	t.Helper()
	wip := "4444444444444444444444444444444444444444"
	session := stoppedFirstTurn(t, h, prompt, coding.TurnCompleteRequest{
		HeadSHA: wip, Commits: []domain.CodingCommit{{SHA: wip, Message: "WIP: stopped by user"}},
		Error: &coding.ErrorDetail{Code: domain.CodingErrPushFailed, Message: "Pushing failed"},
	})
	if _, err := h.store.UpdateCodingSession(context.Background(), workspace, session.ID, func(current *domain.CodingSession) error {
		current.ExistingBranch = true
		if mutate != nil {
			mutate(current)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return continueBootstrap(t, h, session.ID)
}

func TestContinueMarksAGuessedExistingBranchAsUnconfirmed(t *testing.T) {
	h := newHarness(t)
	boot := flaggedByTheOldGuess(t, h, "Old guess with commits", nil)
	if !boot.Repo.ExistingBranch || !boot.Repo.BranchUnconfirmed {
		t.Fatalf("a flag without any recorded push is a guess the runner may recover from: %+v", boot.Repo)
	}
}

func TestContinueKeepsConfirmedPublishEvidenceStrict(t *testing.T) {
	cases := map[string]func(*domain.CodingSession){
		"a recorded push": func(s *domain.CodingSession) { s.BranchPushed = true },
		"a pull request": func(s *domain.CodingSession) {
			s.PullRequests = []domain.SessionPullRequest{{Number: 9, URL: "https://github.com/acme/app/pull/9", HeadBranch: s.BranchName, State: "open"}}
		},
		"a pull request session": func(s *domain.CodingSession) { s.Source = domain.CodingSourcePullRequest },
		"someone else's branch":  func(s *domain.CodingSession) { s.BranchName = "feature/theirs" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			boot := flaggedByTheOldGuess(t, h, "Confirmed "+name, mutate)
			if !boot.Repo.ExistingBranch || boot.Repo.BranchUnconfirmed {
				t.Fatalf("%s is confirmed evidence; a missing branch must fail, not start over: %+v", name, boot.Repo)
			}
		})
	}
}

func TestPushedTurnRecordsTheBranchPushedEvidence(t *testing.T) {
	h := newHarness(t)
	wip := "5555555555555555555555555555555555555555"
	session := stoppedFirstTurn(t, h, "Pushed evidence", coding.TurnCompleteRequest{HeadSHA: wip, Pushed: true, Commits: []domain.CodingCommit{{SHA: wip, Message: "WIP"}}})
	if !session.BranchPushed {
		t.Fatal("a pushed turn records the durable push evidence")
	}
	boot := continueBootstrap(t, h, session.ID)
	if !boot.Repo.ExistingBranch || boot.Repo.BranchUnconfirmed || !h.session(session.ID).BranchPushed {
		t.Fatalf("the evidence survives continuing: %+v", boot.Repo)
	}
}
