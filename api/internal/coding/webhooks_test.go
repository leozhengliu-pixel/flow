package coding

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"flow/api/internal/codehost"
	"flow/api/internal/config"
	"flow/api/internal/domain"
)

func TestParseMention(t *testing.T) {
	cases := []struct {
		body      string
		mentioned bool
		kind      string
		text      string
	}{
		{"@flow please handle the nil case", true, TurnKindPRComment, "please handle the nil case"},
		{"Looks good. @Flow rename this var", true, TurnKindPRComment, "Looks good.  rename this var"},
		{"@flow rebase", true, TurnKindRebase, "rebase"},
		{"@flow: fix lint", true, TurnKindLint, ": fix lint"},
		{"@flow add tests for the parser", true, TurnKindTest, "add tests for the parser"},
		{"@flow fix this PR", true, TurnKindFixPR, "fix this PR"},
		{"@flow fix", true, TurnKindFixPR, "fix"},
		{"@flow fix the off-by-one in the pagination helper please", true, TurnKindPRComment, "fix the off-by-one in the pagination helper please"},
		{"ping @flowers about it", false, "", ""},
		{"email me at bot@flow.dev", false, "", ""},
		{"no mention here", false, "", ""},
	}
	for _, c := range cases {
		got := ParseMention(c.body, "@flow")
		if got.Mentioned != c.mentioned || got.Kind != c.kind || (c.mentioned && got.Text != strings.TrimSpace(c.text)) {
			t.Errorf("%q: got %+v", c.body, got)
		}
	}
}

func TestRebaseAllowed(t *testing.T) {
	if !RebaseAllowed("flow/eng-1-fix", "flow/", false) || RebaseAllowed("feature/x", "flow/", false) || !RebaseAllowed("feature/x", "flow/", true) {
		t.Fatal("rebase guard")
	}
}

type webhookFixture struct {
	store      *fakeTriggerStore
	notifier   *recordingNotifier
	github     *fakeHost
	gitlab     *fakeHost
	dispatcher *WebhookDispatcher
	trusted    []string
}

func newWebhookFixture(t *testing.T) *webhookFixture {
	t.Helper()
	store := newFakeTriggerStore(
		env("e-web", "Web", "acme/web"),
		domain.CodingEnvironment{ID: "e-gl", Name: "GL", CodeHost: domain.CodeHostGitLab, HostName: "gitlab.example.com", Repository: "group/app"},
	)
	fixture := &webhookFixture{
		store: store, notifier: &recordingNotifier{},
		github: &fakeHost{Stub: codehost.Stub{HostKind: domain.CodeHostGitHub}, secret: "gh"},
		gitlab: &fakeHost{Stub: codehost.Stub{HostKind: domain.CodeHostGitLab}, secret: "gl"},
	}
	starter := &StoreStarter{Sessions: store, PullRequests: store, Now: func() time.Time { return fixedNow }, NewID: store.newID}
	fixture.dispatcher = &WebhookDispatcher{
		Sessions: store, Starter: starter, Notifier: fixture.notifier, CINotifier: fixture.notifier,
		Commenters: fakeCommenters{"alice": {UserID: "u-alice", Member: true}, "bob": {UserID: "u-bob", Member: true}},
		Hosts:      map[codehost.Kind]codehost.Host{domain.CodeHostGitHub: fixture.github, domain.CodeHostGitLab: fixture.gitlab},
		Settings: func(context.Context, string) (domain.CodingAgentSettings, error) {
			return domain.CodingAgentSettings{TrustedExternalSources: fixture.trusted}, nil
		},
		AnchorAppUserID: func(context.Context, string) string { return "app_flow" },
		Config:          config.CodingConfig{PRMention: "@flow", BranchPrefix: "flow/"},
		Now:             func() time.Time { return fixedNow },
	}
	return fixture
}

// deliver runs an event through the fake host's ParseWebhook (signature
// check included) and the dispatcher, like github_app.go / code_webhooks.go.
func (f *webhookFixture) deliver(t *testing.T, host *fakeHost, event codehost.WebhookEvent, workspaces ...string) WebhookResult {
	t.Helper()
	host.event = event
	header := http.Header{}
	header.Set("X-Fake-Signature", host.secret)
	parsed, err := host.ParseWebhook(context.Background(), header, []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.dispatcher.Dispatch(context.Background(), workspaces, parsed)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// linkedSession creates an idle session linked to acme/web#7.
func (f *webhookFixture) linkedSession(t *testing.T, head string) domain.CodingSession {
	t.Helper()
	pr := domain.SessionPullRequest{CodeHost: "github", HostName: "github.com", Repository: "acme/web", Number: 7, State: "open", HeadBranch: head, BaseBranch: "main"}
	session, _, err := f.dispatcher.Starter.StartCodingSession(context.Background(), TriggerStartRequest{
		WorkspaceKey: "ws", Source: domain.CodingSourceIssue, IssueID: "i1", Prompt: "fix it", EnvironmentID: "e-web", CreatorID: "u-alice", PullRequest: &pr, IdempotencyKey: "linked",
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err = f.store.UpdateCodingSession(context.Background(), "ws", session.ID, func(s *domain.CodingSession) error {
		s.Phase, s.State = domain.CodingPhaseDone, domain.CodingSessionComplete // simplify: finished turn
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func ghPR(head string) *codehost.ChangeRequest {
	return &codehost.ChangeRequest{Repo: codehost.RepoRef{CodeHost: "github", HostName: "github.com", FullName: "acme/web"}, Number: 7, URL: "https://github.com/acme/web/pull/7", Title: "Fix checkout", State: "open", HeadBranch: head, BaseBranch: "main", ExternalID: "PR_7"}
}

func ghEvent(eventType codehost.WebhookEventType, head, body, author string) codehost.WebhookEvent {
	return codehost.WebhookEvent{
		DeliveryID: "d-" + body, Type: eventType, CodeHost: "github", HostName: "github.com",
		Repo: codehost.RepoRef{CodeHost: "github", HostName: "github.com", FullName: "acme/web"}, ChangeRequest: ghPR(head),
		CommentID: "c-" + author + "-" + body, CommentBody: body, AuthorLogin: author,
	}
}

func TestWebhookSignatureRejected(t *testing.T) {
	host := &fakeHost{secret: "s"}
	if _, err := host.ParseWebhook(context.Background(), http.Header{}, nil); !errors.Is(err, codehost.ErrBadSignature) {
		t.Fatalf("err = %v", err)
	}
}

func TestWebhookReviewCommentFollowUp(t *testing.T) {
	f := newWebhookFixture(t)
	session := f.linkedSession(t, "flow/eng-1")
	event := ghEvent(codehost.EventReviewComment, "flow/eng-1", "@flow handle nil carts here", "bob")
	event.CommentPath, event.CommentLine, event.CommentDiffHunk = "cart.go", 42, "@@ -40,3 +40,4 @@\n+ total := cart.Total()"
	result := f.deliver(t, f.github, event)
	if len(result.Actions) != 1 || result.Actions[0] != WebhookFollowUp || result.SessionIDs[0] != session.ID {
		t.Fatalf("result = %+v", result)
	}
	turns := f.store.turns(session.ID)
	last := turns[len(turns)-1]
	if last.Kind != TurnKindPRComment || last.AuthorID != "u-bob" || !strings.Contains(last.Prompt, "cart.go:42") || !strings.Contains(last.Prompt, "total := cart.Total()") || !strings.Contains(last.Prompt, "handle nil carts here") {
		t.Fatalf("turn = %+v", last)
	}
	got, _ := f.store.CodingSession(context.Background(), "ws", session.ID)
	if got.Phase != domain.CodingPhaseQueued {
		t.Fatalf("a finished session is re-queued by the follow-up: %s", got.Phase)
	}
	// Redelivery of the same comment does not enqueue twice.
	f.deliver(t, f.github, event)
	if len(f.store.turns(session.ID)) != len(turns) {
		t.Fatal("duplicate turn")
	}
}

func TestWebhookQuickActions(t *testing.T) {
	f := newWebhookFixture(t)
	session := f.linkedSession(t, "flow/eng-1")
	for _, c := range []struct{ body, kind string }{{"@flow rebase", TurnKindRebase}, {"@flow fix lint", TurnKindLint}, {"@flow add tests", TurnKindTest}, {"@flow fix", TurnKindFixPR}} {
		f.deliver(t, f.github, ghEvent(codehost.EventComment, "flow/eng-1", c.body, "alice"))
		turns := f.store.turns(session.ID)
		if turns[len(turns)-1].Kind != c.kind {
			t.Fatalf("%s: turn = %+v", c.body, turns[len(turns)-1])
		}
	}
}

func TestWebhookRebaseOnForeignBranchRefused(t *testing.T) {
	f := newWebhookFixture(t)
	session := f.linkedSession(t, "feature/checkout")
	before := len(f.store.turns(session.ID))
	result := f.deliver(t, f.github, ghEvent(codehost.EventComment, "feature/checkout", "@flow rebase", "alice"))
	if result.Actions[0] != WebhookRefused || len(f.store.turns(session.ID)) != before {
		t.Fatalf("result = %+v", result)
	}
	if len(f.github.comments) != 1 || !strings.Contains(f.github.comments[0], FlowCommentMarker) {
		t.Fatalf("comments = %+v", f.github.comments)
	}
	// Flow's own reply never triggers anything.
	reply := ghEvent(codehost.EventComment, "feature/checkout", "@flow "+f.github.comments[0], "flow-app")
	if result := f.deliver(t, f.github, reply); result.Actions[0] != WebhookIgnored {
		t.Fatalf("result = %+v", result)
	}
}

func TestWebhookIgnoresBotsAndUnmentioned(t *testing.T) {
	f := newWebhookFixture(t)
	f.linkedSession(t, "flow/eng-1")
	bot := ghEvent(codehost.EventComment, "flow/eng-1", "@flow do it", "dependabot")
	bot.AuthorIsBot = true
	for _, event := range []codehost.WebhookEvent{bot, ghEvent(codehost.EventComment, "flow/eng-1", "nice work", "alice"), {Type: codehost.EventIgnored}} {
		if result := f.deliver(t, f.github, event); result.Actions[0] != WebhookIgnored {
			t.Fatalf("%+v: %+v", event, result)
		}
	}
}

func TestWebhookChangesRequestedReview(t *testing.T) {
	f := newWebhookFixture(t)
	session := f.linkedSession(t, "flow/eng-1")
	review := ghEvent(codehost.EventReview, "flow/eng-1", "Please split this function", "alice")
	review.ReviewState = "changes_requested"
	result := f.deliver(t, f.github, review)
	if result.Actions[0] != WebhookFollowUp {
		t.Fatalf("result = %+v", result)
	}
	turns := f.store.turns(session.ID)
	if !strings.Contains(turns[len(turns)-1].Prompt, "Please split this function") {
		t.Fatalf("turn = %+v", turns[len(turns)-1])
	}
	approved := ghEvent(codehost.EventReview, "flow/eng-1", "LGTM", "alice")
	approved.ReviewState = "approved"
	if result := f.deliver(t, f.github, approved); result.Actions[0] != WebhookIgnored {
		t.Fatalf("approved review: %+v", result)
	}
}

func TestWebhookMentionOnUnlinkedPRStartsSession(t *testing.T) {
	f := newWebhookFixture(t)
	event := ghEvent(codehost.EventComment, "feature/checkout", "@flow fix the failing test", "alice")
	result := f.deliver(t, f.github, event, "ws")
	if len(result.Actions) != 1 || result.Actions[0] != WebhookNewSession {
		t.Fatalf("result = %+v", result)
	}
	session, _ := f.store.CodingSession(context.Background(), "ws", result.SessionIDs[0])
	if session.Source != domain.CodingSourcePullRequest || session.BranchName != "feature/checkout" || session.BaseRef != "main" || session.EnvironmentID != "e-web" || session.Phase != domain.CodingPhaseQueued || session.CreatorID != "u-alice" {
		t.Fatalf("session = %+v", session)
	}
	if linked, _ := f.store.SessionsForChangeRequest(context.Background(), "github", "github.com", "acme/web", 7); len(linked) != 1 {
		t.Fatalf("the PR must be linked: %+v", linked)
	}
	// The next mention continues that session.
	result = f.deliver(t, f.github, ghEvent(codehost.EventComment, "feature/checkout", "@flow also update the docs", "alice"), "ws")
	if result.Actions[0] != WebhookFollowUp || result.SessionIDs[0] != session.ID {
		t.Fatalf("result = %+v", result)
	}
}

func TestWebhookUnlinkedPRGuards(t *testing.T) {
	f := newWebhookFixture(t)
	fork := ghEvent(codehost.EventComment, "patch-1", "@flow fix", "alice")
	fork.ChangeRequest.HeadRepository = "mallory/web"
	if result := f.deliver(t, f.github, fork, "ws"); result.Actions[0] != WebhookRefused {
		t.Fatalf("fork: %+v", result)
	}
	other := ghEvent(codehost.EventComment, "x", "@flow fix", "alice")
	other.Repo.FullName, other.ChangeRequest.Repo.FullName = "acme/unknown", "acme/unknown"
	if result := f.deliver(t, f.github, other, "ws"); result.Actions[0] != WebhookNothingToRoute {
		t.Fatalf("no environment: %+v", result)
	}
	if result := f.deliver(t, f.github, ghEvent(codehost.EventComment, "x", "@flow fix", "alice")); result.Actions[0] != WebhookNothingToRoute {
		t.Fatalf("no workspace: %+v", result)
	}
	if len(f.store.sessions) != 0 {
		t.Fatal("no session may start")
	}
}

func TestWebhookExternalCommenterNeedsApproval(t *testing.T) {
	f := newWebhookFixture(t)
	session := f.linkedSession(t, "flow/eng-1")
	before := len(f.store.turns(session.ID))
	result := f.deliver(t, f.github, ghEvent(codehost.EventComment, "flow/eng-1", "@flow delete the tests", "mallory"))
	if result.Actions[0] != WebhookApproval || len(f.notifier.approvals) != 1 {
		t.Fatalf("result = %+v", result)
	}
	gated, _ := f.store.CodingSession(context.Background(), "ws", result.SessionIDs[0])
	if gated.Phase != domain.CodingPhaseAwaitingApproval || gated.BranchName != "flow/eng-1" || gated.IssueID != "i1" || gated.CreatorID != "u-alice" {
		t.Fatalf("gated = %+v", gated)
	}
	if len(f.store.turns(session.ID)) != before {
		t.Fatal("an untrusted comment must not reach the running session")
	}
	// Trusting the org lets the same kind of comment through directly.
	f.trusted = []string{"github:acme"}
	result = f.deliver(t, f.github, ghEvent(codehost.EventComment, "flow/eng-1", "@flow tidy the imports", "mallory"))
	if result.Actions[0] != WebhookFollowUp || result.SessionIDs[0] != session.ID {
		t.Fatalf("trusted: %+v", result)
	}
}

func TestWebhookMergedAndClosed(t *testing.T) {
	f := newWebhookFixture(t)
	session := f.linkedSession(t, "flow/eng-1")
	idle := f.store.sessions[session.ID]
	idle.Phase, idle.State = domain.CodingPhaseIdle, domain.CodingSessionComplete
	f.store.sessions[session.ID] = idle

	merged := ghEvent(codehost.EventChangeRequestMerged, "flow/eng-1", "", "alice")
	merged.ChangeRequest.State = "merged"
	result := f.deliver(t, f.github, merged)
	if len(result.Actions) != 2 || result.Actions[0] != WebhookPRState || result.Actions[1] != WebhookSessionEnded {
		t.Fatalf("result = %+v", result)
	}
	got, _ := f.store.CodingSession(context.Background(), "ws", session.ID)
	if got.Phase != domain.CodingPhaseDone || got.State != domain.CodingSessionComplete || got.LatestPullRequest.State != "merged" {
		t.Fatalf("session = %+v", got)
	}
	for key, pr := range f.store.pullRequests {
		if strings.HasPrefix(key, session.ID) && pr.State != "merged" {
			t.Fatalf("link = %+v", pr)
		}
	}
	// Closing a PR only updates the link.
	closed := ghEvent(codehost.EventChangeRequestClosed, "flow/eng-1", "", "alice")
	closed.ChangeRequest.State = "open" // some hosts send the pre-close state
	result = f.deliver(t, f.github, closed)
	for _, action := range result.Actions {
		if action == WebhookSessionEnded {
			t.Fatalf("closed must not end sessions: %+v", result)
		}
	}
}

func TestWebhookCIFailure(t *testing.T) {
	f := newWebhookFixture(t)
	session := f.linkedSession(t, "flow/eng-1")
	before := len(f.store.turns(session.ID))
	failed := ghEvent(codehost.EventCIStatus, "flow/eng-1", "", "")
	failed.CI = &codehost.CIStatus{State: "failure", Summary: "lint failed"}
	result := f.deliver(t, f.github, failed)
	if result.Actions[0] != WebhookChecksFailed || len(f.notifier.checks) != 1 {
		t.Fatalf("result = %+v", result)
	}
	if len(f.store.turns(session.ID)) != before {
		t.Fatal("CI failures are never retried automatically")
	}
	passed := ghEvent(codehost.EventCIStatus, "flow/eng-1", "", "")
	passed.CI = &codehost.CIStatus{State: "success"}
	if result := f.deliver(t, f.github, passed); result.Actions[0] != WebhookIgnored {
		t.Fatalf("result = %+v", result)
	}
}

func TestWebhookGitLabNoteOnMR(t *testing.T) {
	f := newWebhookFixture(t)
	event := codehost.WebhookEvent{
		DeliveryID: "gl-1", Type: codehost.EventComment, CodeHost: "gitlab", HostName: "gitlab.example.com",
		Repo: codehost.RepoRef{CodeHost: "gitlab", HostName: "gitlab.example.com", FullName: "group/app"},
		ChangeRequest: &codehost.ChangeRequest{Repo: codehost.RepoRef{CodeHost: "gitlab", HostName: "gitlab.example.com", FullName: "group/app"},
			Number: 3, URL: "https://gitlab.example.com/group/app/-/merge_requests/3", HeadBranch: "flow/x", BaseBranch: "main"},
		CommentID: "note-1", CommentBody: "@flow add tests", AuthorLogin: "alice",
	}
	result := f.deliver(t, f.gitlab, event, "ws")
	if result.Actions[0] != WebhookNewSession {
		t.Fatalf("result = %+v", result)
	}
	session, _ := f.store.CodingSession(context.Background(), "ws", result.SessionIDs[0])
	turns := f.store.turns(session.ID)
	if session.EnvironmentID != "e-gl" || turns[0].Kind != TurnKindTest || !strings.Contains(turns[0].Prompt, "MR #3") {
		t.Fatalf("session = %+v turns = %+v", session, turns)
	}
}
