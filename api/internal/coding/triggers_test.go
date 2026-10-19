package coding

import (
	"context"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func issueTrigger(issue *IssueContext, request string) IssueTrigger {
	return IssueTrigger{WorkspaceKey: "ws", Source: domain.CodingSourceIssue, Issue: issue, Request: request, CreatorID: "u1", AnchorAppUserID: "app_flow"}
}

func TestStartForIssueStartsAndContinues(t *testing.T) {
	store := newFakeTriggerStore(env("e-web", "Web", "acme/web"), env("e-api", "API", "acme/payments-api"))
	notifier := &recordingNotifier{}
	triggers := newTestTriggers(store, notifier)
	issue := &IssueContext{ID: "i1", Identifier: "ENG-1", Title: "Checkout fails", Description: "Stack trace from acme/payments-api", TeamID: "t1"}

	trigger := issueTrigger(issue, "")
	trigger.AnchorTaskID = "task-delegation"
	trigger.IdempotencyKey = "delegation:task-delegation"
	result, err := triggers.StartForIssue(context.Background(), trigger)
	if err != nil {
		t.Fatal(err)
	}
	session := result.Session
	if result.Outcome != OutcomeStarted || !result.Created || session.Phase != domain.CodingPhaseQueued || session.State != domain.CodingSessionPending {
		t.Fatalf("result = %+v", result)
	}
	if session.EnvironmentID != "e-api" || session.AgentSessionID != "task-delegation" || session.IssueID != "i1" {
		t.Fatalf("session = %+v", session)
	}
	turns := store.turns(session.ID)
	if len(turns) != 1 || turns[0].Kind != TurnKindInitial || turns[0].Prompt == "" {
		t.Fatalf("initial turn = %+v", turns)
	}

	// Retrying the same delegation is idempotent.
	again, err := triggers.StartForIssue(context.Background(), trigger)
	if err != nil || again.Session.ID != session.ID || again.Created || again.Outcome != OutcomeStarted {
		t.Fatalf("retry = %+v, %v", again, err)
	}
	if len(store.turns(session.ID)) != 1 {
		t.Fatalf("turns = %+v", store.turns(session.ID))
	}
	// A new request on the issue is a follow-up turn, deduplicated by key.
	followUpRequest := issueTrigger(issue, "Also log the cart id")
	followUpRequest.IdempotencyKey = "comment:c1"
	for range 2 {
		result, err := triggers.StartForIssue(context.Background(), followUpRequest)
		if err != nil || result.Outcome != OutcomeContinued || result.Session.ID != session.ID {
			t.Fatalf("follow-up = %+v %v", result, err)
		}
	}
	if turns := store.turns(session.ID); len(turns) != 2 || turns[1].Kind != TurnKindFollowUp {
		t.Fatalf("turns = %+v", turns)
	}

	// A finished session is history: a new trigger on the issue starts a
	// new session (the session view continues the old one through /prompt).
	if _, err := store.UpdateCodingSession(context.Background(), "ws", session.ID, func(s *domain.CodingSession) error {
		s.Phase, s.State = domain.CodingPhaseDone, domain.CodingSessionComplete
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	followUp := issueTrigger(issue, "Also handle the empty cart")
	result, err = triggers.StartForIssue(context.Background(), followUp)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != OutcomeStarted || result.Session.ID == session.ID {
		t.Fatalf("result = %+v", result)
	}
}

func TestStartForIssueIntentGate(t *testing.T) {
	store := newFakeTriggerStore(env("e-web", "Web", "acme/web"))
	triggers := newTestTriggers(store, &recordingNotifier{})
	trigger := issueTrigger(&IssueContext{ID: "i1", Title: "Slow export"}, "Investigate why the export is slow")
	trigger.Source = domain.CodingSourceComment
	trigger.CheckIntent = true
	result, err := triggers.StartForIssue(context.Background(), trigger)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != OutcomeNotStarted || result.Intent == nil || result.Intent.Intent != IntentNoCode || result.Message == "" {
		t.Fatalf("result = %+v", result)
	}
	if len(store.sessions) != 0 {
		t.Fatal("no session may start for an investigation")
	}
	trigger.Request = "Fix the slow export"
	if result, err = triggers.StartForIssue(context.Background(), trigger); err != nil || result.Outcome != OutcomeStarted {
		t.Fatalf("fix request: %+v %v", result, err)
	}
}

func TestStartForIssueAmbiguousEnvironmentElicitation(t *testing.T) {
	store := newFakeTriggerStore(env("e-web", "Web", "acme/web"), env("e-api", "API", "acme/payments-api"))
	notifier := &recordingNotifier{}
	triggers := newTestTriggers(store, notifier)
	issue := &IssueContext{ID: "i1", Title: "Typo on the pricing page"}
	result, err := triggers.StartForIssue(context.Background(), issueTrigger(issue, ""))
	if err != nil {
		t.Fatal(err)
	}
	session := *result.Session
	if session.Phase != domain.CodingPhaseAwaitingUser || session.State != domain.CodingSessionAwaitingInput || session.EnvironmentID != "" {
		t.Fatalf("session = %+v", session)
	}
	if len(notifier.selections) != 1 || len(result.Pick.Suggestions) != 2 {
		t.Fatalf("selection elicitation = %+v %+v", notifier.selections, result.Pick)
	}
	// Without a choice, the trigger reports the waiting session.
	result, err = triggers.StartForIssue(context.Background(), issueTrigger(issue, "please"))
	if err != nil || result.Outcome != OutcomeNotStarted {
		t.Fatalf("result = %+v %v", result, err)
	}
	// The answer: choose an environment.
	choice := issueTrigger(issue, "")
	choice.EnvironmentID = "e-web"
	result, err = triggers.StartForIssue(context.Background(), choice)
	if err != nil || result.Outcome != OutcomeEnvironmentChosen {
		t.Fatalf("result = %+v %v", result, err)
	}
	if result.Session.Phase != domain.CodingPhaseQueued || result.Session.EnvironmentID != "e-web" || result.Session.Repository != "acme/web" {
		t.Fatalf("session = %+v", result.Session)
	}
	// Answering twice with the same environment is a no-op; another one fails.
	if _, err := triggers.SelectEnvironment(context.Background(), "ws", session.ID, "e-web", "u1", domain.CodingAgentSettings{}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := triggers.SelectEnvironment(context.Background(), "ws", session.ID, "e-api", "u1", domain.CodingAgentSettings{}, false); TriggerErrorCode(err) != domain.CodingErrPermissionDenied {
		t.Fatalf("err = %v", err)
	}
	if _, err := triggers.SelectEnvironment(context.Background(), "ws", session.ID, "missing", "u1", domain.CodingAgentSettings{}, false); TriggerErrorCode(err) != domain.CodingErrRepoNotInPolicy {
		t.Fatalf("err = %v", err)
	}
}

func TestStartForIssueNoEnvironment(t *testing.T) {
	triggers := newTestTriggers(newFakeTriggerStore(), &recordingNotifier{})
	_, err := triggers.StartForIssue(context.Background(), issueTrigger(&IssueContext{ID: "i1", Title: "x"}, "fix it"))
	if TriggerErrorCode(err) != domain.CodingErrNoPolicyRepos {
		t.Fatalf("err = %v", err)
	}
	var unavailable Triggers
	if _, err := unavailable.StartForIssue(context.Background(), IssueTrigger{}); TriggerErrorCode(err) != domain.CodingErrSandboxDisabled {
		t.Fatalf("err = %v", err)
	}
}

func TestLoopActionPolicy(t *testing.T) {
	web, api := env("e-web", "Web", "acme/web"), env("e-api", "API", "acme/payments-api")
	issue := &IssueContext{ID: "i1", Title: "Bug: checkout 500", Description: "in acme/payments-api", Labels: []string{"Bug"}}
	loopTrigger := func(settings domain.CodingAgentSettings, limits domain.SpendLimitSettings) IssueTrigger {
		trigger := issueTrigger(issue, "Start a coding session: fix the 500")
		trigger.Source, trigger.LoopID, trigger.SourceID = domain.CodingSourceLoop, "loop1", "run1"
		trigger.Settings, trigger.SpendLimits = settings, limits
		trigger.IdempotencyKey = "loop:loop1:run1"
		return trigger
	}
	cases := []struct {
		name      string
		settings  domain.CodingAgentSettings
		limits    domain.SpendLimitSettings
		spend     int64
		explicit  string
		wantCode  domain.CodingErrorCode
		wantEnvID string
	}{
		{name: "default access is none", settings: domain.CodingAgentSettings{}, wantCode: domain.CodingErrRepoNotInPolicy},
		{name: "all", settings: domain.CodingAgentSettings{LoopRepositoryAccess: "all"}, wantEnvID: "e-api"},
		{name: "selected allows only listed", settings: domain.CodingAgentSettings{LoopRepositoryAccess: "selected", LoopRepositories: []string{"e-web"}}, wantEnvID: "e-web"},
		{name: "selected explicit outside the list", settings: domain.CodingAgentSettings{LoopRepositoryAccess: "selected", LoopRepositories: []string{"e-web"}}, explicit: "e-api", wantCode: domain.CodingErrRepoNotInPolicy},
		{name: "selected empty list", settings: domain.CodingAgentSettings{LoopRepositoryAccess: "selected"}, wantCode: domain.CodingErrRepoNotInPolicy},
		{name: "loop limit reached", settings: domain.CodingAgentSettings{LoopRepositoryAccess: "all"}, limits: domain.SpendLimitSettings{LoopLimit: 5, LoopReset: "daily"}, spend: 5_000_000, wantCode: domain.CodingErrQuotaExceeded},
		{name: "loop override raises the limit", settings: domain.CodingAgentSettings{LoopRepositoryAccess: "all"}, limits: domain.SpendLimitSettings{LoopLimit: 5, LoopOverrides: map[string]float64{"loop1": 50}}, spend: 5_000_000, wantEnvID: "e-api"},
		{name: "loop override lowers the limit", settings: domain.CodingAgentSettings{LoopRepositoryAccess: "all"}, limits: domain.SpendLimitSettings{LoopLimit: 50, LoopOverrides: map[string]float64{"loop1": 1}}, spend: 1_000_000, wantCode: domain.CodingErrQuotaExceeded},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := newFakeTriggerStore(web, api)
			store.spend = c.spend
			triggers := newTestTriggers(store, &recordingNotifier{})
			trigger := loopTrigger(c.settings, c.limits)
			trigger.EnvironmentID = c.explicit
			result, err := triggers.StartForIssue(context.Background(), trigger)
			if c.wantCode != "" {
				if TriggerErrorCode(err) != c.wantCode {
					t.Fatalf("err = %v, want %s", err, c.wantCode)
				}
				if len(store.sessions) != 0 {
					t.Fatal("no session may start")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if result.Session.EnvironmentID != c.wantEnvID || result.Session.Source != domain.CodingSourceLoop || result.Session.Phase != domain.CodingPhaseQueued {
				t.Fatalf("session = %+v", result.Session)
			}
			if c.limits.LoopLimit > 0 && (len(store.spendQueries) != 1 || store.spendQueries[0].LoopID != "loop1") {
				t.Fatalf("spend queries = %+v", store.spendQueries)
			}
		})
	}
}

func TestLoopUnattendedRepositoryChoice(t *testing.T) {
	store := newFakeTriggerStore(env("e-web", "Web", "acme/web"), env("e-api", "API", "acme/payments-api"))
	triggers := newTestTriggers(store, &recordingNotifier{})
	trigger := issueTrigger(&IssueContext{ID: "i1", Title: "Something broke"}, "fix it")
	trigger.Source, trigger.LoopID = domain.CodingSourceLoop, "loop1"
	trigger.Settings = domain.CodingAgentSettings{LoopRepositoryAccess: "all"}
	if _, err := triggers.StartForIssue(context.Background(), trigger); TriggerErrorCode(err) != domain.CodingErrRepoNotInPolicy {
		t.Fatalf("a loop must not wait for a repository choice: %v", err)
	}
	trigger.RecentEnvironmentIDs = []string{"e-web"}
	result, err := triggers.StartForIssue(context.Background(), trigger)
	if err != nil || result.Session.EnvironmentID != "e-web" || result.Session.Phase != domain.CodingPhaseQueued {
		t.Fatalf("result = %+v %v", result, err)
	}
}

func TestSpendWindowStart(t *testing.T) {
	now := time.Date(2026, 10, 18, 9, 30, 0, 0, time.UTC) // a Sunday
	hour := func(v int) *int { return &v }
	cases := []struct {
		reset  string
		limits domain.SpendLimitSettings
		want   time.Time
	}{
		{"daily", domain.SpendLimitSettings{}, time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)},
		{"daily", domain.SpendLimitSettings{DailyResetHour: hour(10)}, time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)},
		{"weekly", domain.SpendLimitSettings{WeeklyResetDay: hour(1)}, time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)},
		{"weekly", domain.SpendLimitSettings{}, time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)},
		{"monthly", domain.SpendLimitSettings{}, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)},
		{"monthly", domain.SpendLimitSettings{MonthlyResetDay: hour(20)}, time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)},
		{"monthly", domain.SpendLimitSettings{MonthlyResetDay: hour(31)}, time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		if got := SpendWindowStart(c.reset, c.limits, now); !got.Equal(c.want) {
			t.Errorf("%s %+v: got %s want %s", c.reset, c.limits, got, c.want)
		}
	}
}

func TestTrustedSourcesAndApprovalFlow(t *testing.T) {
	cases := []struct {
		source  string
		trusted []string
		want    bool
	}{
		{"integration:slack", []string{"slack"}, true},
		{"integration:slack", []string{"integration:slack"}, true},
		{"integration:email", []string{"slack"}, false},
		{"appUser:u9", []string{"appUser:u9"}, true},
		{"github:acme/web", []string{"github:acme"}, true},
		{"github:acmecorp/web", []string{"github:acme"}, false},
		{"", []string{""}, false},
	}
	for _, c := range cases {
		if got := IsTrustedSource(c.source, c.trusted); got != c.want {
			t.Errorf("IsTrustedSource(%q, %v) = %t", c.source, c.trusted, got)
		}
	}
}

func startExternal(t *testing.T, triggers *Triggers, trusted []string) domain.CodingSession {
	t.Helper()
	trigger := issueTrigger(&IssueContext{ID: "i-ext", Title: "Customer: export broken", Origin: IntegrationOrigin("email")}, "fix the export")
	trigger.Settings.TrustedExternalSources = trusted
	result, err := triggers.StartForIssue(context.Background(), trigger)
	if err != nil {
		t.Fatal(err)
	}
	return *result.Session
}

func TestExternalSourceApproval(t *testing.T) {
	ctx := context.Background()
	t.Run("trusted source starts immediately", func(t *testing.T) {
		store := newFakeTriggerStore(env("e-web", "Web", "acme/web"))
		notifier := &recordingNotifier{}
		session := startExternal(t, newTestTriggers(store, notifier), []string{"email"})
		if session.Phase != domain.CodingPhaseQueued || session.ApprovalState != "" || len(notifier.approvals) != 0 {
			t.Fatalf("session = %+v", session)
		}
	})
	t.Run("approve", func(t *testing.T) {
		store := newFakeTriggerStore(env("e-web", "Web", "acme/web"))
		notifier := &recordingNotifier{}
		triggers := newTestTriggers(store, notifier)
		session := startExternal(t, triggers, nil)
		if session.Phase != domain.CodingPhaseAwaitingApproval || session.State != domain.CodingSessionAwaitingInput || session.ApprovalState != ApprovalRequired {
			t.Fatalf("session = %+v", session)
		}
		if len(notifier.approvals) != 1 {
			t.Fatalf("approval notification = %+v", notifier.approvals)
		}
		if _, err := triggers.DecideApproval(ctx, "ws", session.ID, "guest", false, DecisionApprove); TriggerErrorCode(err) != domain.CodingErrPermissionDenied {
			t.Fatalf("non-member: %v", err)
		}
		approved, err := triggers.DecideApproval(ctx, "ws", session.ID, "u2", true, DecisionApprove)
		if err != nil || approved.Phase != domain.CodingPhaseQueued || approved.ApprovalState != ApprovalApproved || approved.ApprovedBy != "u2" {
			t.Fatalf("approved = %+v %v", approved, err)
		}
		// Idempotent, and a late reject cannot undo it.
		if _, err := triggers.DecideApproval(ctx, "ws", session.ID, "u3", true, DecisionApprove); err != nil {
			t.Fatal(err)
		}
		if _, err := triggers.DecideApproval(ctx, "ws", session.ID, "u3", true, DecisionReject); TriggerErrorCode(err) != domain.CodingErrPermissionDenied {
			t.Fatalf("late reject: %v", err)
		}
		if len(notifier.resolved) != 2 || notifier.resolved[0] != session.ID+":approved" {
			t.Fatalf("resolved = %+v", notifier.resolved)
		}
	})
	t.Run("deny", func(t *testing.T) {
		store := newFakeTriggerStore(env("e-web", "Web", "acme/web"))
		notifier := &recordingNotifier{}
		triggers := newTestTriggers(store, notifier)
		session := startExternal(t, triggers, nil)
		rejected, err := triggers.DecideApproval(ctx, "ws", session.ID, "u2", true, DecisionReject)
		if err != nil || rejected.Phase != domain.CodingPhaseDone || rejected.State != domain.CodingSessionStale || rejected.ApprovalState != ApprovalRejected || rejected.EndedAt == nil {
			t.Fatalf("rejected = %+v %v", rejected, err)
		}
		// A rejected session cannot be revived by a follow-up.
		if _, err := triggers.Starter.PromptCodingSession(ctx, "ws", session.ID, Turn{ID: "t", Prompt: "please"}); TriggerErrorCode(err) != domain.CodingErrPermissionDenied {
			t.Fatalf("prompt after reject: %v", err)
		}
	})
	t.Run("timeout on decision", func(t *testing.T) {
		store := newFakeTriggerStore(env("e-web", "Web", "acme/web"))
		notifier := &recordingNotifier{}
		triggers := newTestTriggers(store, notifier)
		triggers.Config.Timeouts.Approval = time.Hour
		session := startExternal(t, triggers, nil)
		triggers.Now = func() time.Time { return fixedNow.Add(time.Hour) }
		expired, err := triggers.DecideApproval(ctx, "ws", session.ID, "u2", true, DecisionApprove)
		if TriggerErrorCode(err) != domain.CodingErrApprovalExpired || expired.Phase != domain.CodingPhaseDone || expired.ErrorCode != domain.CodingErrApprovalExpired || expired.State != domain.CodingSessionError {
			t.Fatalf("expired = %+v %v", expired, err)
		}
		if _, err := triggers.DecideApproval(ctx, "ws", session.ID, "u2", true, DecisionApprove); TriggerErrorCode(err) != domain.CodingErrApprovalExpired {
			t.Fatalf("second decision: %v", err)
		}
	})
	t.Run("timeout sweep", func(t *testing.T) {
		store := newFakeTriggerStore(env("e-web", "Web", "acme/web"))
		notifier := &recordingNotifier{}
		triggers := newTestTriggers(store, notifier)
		session := startExternal(t, triggers, nil)
		if count, err := triggers.ExpireApprovals(ctx, "ws"); err != nil || count != 0 {
			t.Fatalf("early sweep = %d %v", count, err)
		}
		triggers.Now = func() time.Time { return fixedNow.Add(7*24*time.Hour + time.Second) }
		if count, err := triggers.ExpireApprovals(ctx, "ws"); err != nil || count != 1 {
			t.Fatalf("sweep = %d %v", count, err)
		}
		if count, _ := triggers.ExpireApprovals(ctx, "ws"); count != 0 {
			t.Fatal("the sweep must be idempotent")
		}
		got, _ := store.CodingSession(ctx, "ws", session.ID)
		if got.ErrorCode != domain.CodingErrApprovalExpired || len(notifier.resolved) != 1 || notifier.resolved[0] != session.ID+":expired" {
			t.Fatalf("session = %+v resolved = %+v", got, notifier.resolved)
		}
	})
}

func TestNewTriggerID(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := newTriggerID(fixedNow)
		if len(id) != 26 || seen[id] {
			t.Fatalf("id %q", id)
		}
		for _, r := range id {
			if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z') {
				t.Fatalf("id %q has %q", id, r)
			}
		}
		seen[id] = true
	}
	if newTriggerID(fixedNow)[:10] >= newTriggerID(fixedNow.Add(time.Hour))[:10] {
		t.Fatal("ids sort by time")
	}
}
