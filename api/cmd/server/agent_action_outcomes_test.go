package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// outcomeFixture is a signed-in workspace for moving issues into a private team through chat: the admin is in the
// private team Vault (VLT), the member is not. The member is in the public team Home (HOM) and the private team Crew
// (CRW), and only sees the public team Lobby (LOB). The model is a scripted Chat Completions provider.
type outcomeFixture struct {
	provider   *thinkingChatProvider
	repository *store.SQLiteStore
	host       *httptest.Server
	admin      *http.Client
	member     *http.Client
	home       []domain.Issue
	vault      domain.Issue
	lobby      domain.Issue
}

const outcomeWorkspace = "test-workspace"

func newOutcomeFixture(t *testing.T) outcomeFixture {
	t.Helper()
	provider := &thinkingChatProvider{issued: map[string]bool{}}
	model := httptest.NewServer(http.HandlerFunc(provider.serve))
	t.Cleanup(model.Close)
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	srv := &server{store: repository, uploadPath: t.TempDir(), agent: appconfig.AgentConfig{Enabled: true, Protocol: "openai-chat-completions", BaseURL: model.URL, Model: "flow-test", MaxOutputTokens: 256, ToolsEnabled: true, WriteTools: true}, agentClient: model.Client()}
	host := httptest.NewServer(newHandler(srv))
	t.Cleanup(host.Close)
	login := func(email string) *http.Client {
		client := authClient(t)
		authRequest[domain.AuthSession](t, client, http.MethodPost, host.URL+"/api/auth/login", map[string]string{"email": email, "password": "test-password"}, "", http.StatusOK)
		return client
	}
	admin := login("admin@example.test")
	authRequest[domain.WorkspaceMember](t, admin, http.MethodPatch, host.URL+"/api/workspaces/"+outcomeWorkspace+"/members/usr_member", map[string]string{"role": "member"}, "", http.StatusOK)
	teams := host.URL + "/api/workspaces/" + outcomeWorkspace + "/teams"
	home := authRequest[domain.Team](t, admin, http.MethodPost, teams, map[string]any{"name": "Home", "key": "HOM"}, "", http.StatusCreated)
	vault := authRequest[domain.Team](t, admin, http.MethodPost, teams, map[string]any{"name": "Vault", "key": "VLT", "private": true}, "", http.StatusCreated)
	crew := authRequest[domain.Team](t, admin, http.MethodPost, teams, map[string]any{"name": "Crew", "key": "CRW", "private": true}, "", http.StatusCreated)
	lobby := authRequest[domain.Team](t, admin, http.MethodPost, teams, map[string]any{"name": "Lobby", "key": "LOB"}, "", http.StatusCreated)
	for _, team := range []string{home.ID, crew.ID} {
		authRequest[any](t, admin, http.MethodPut, teams+"/"+team+"/members/usr_member", map[string]any{"member": true}, "", http.StatusNoContent)
	}
	fixture := outcomeFixture{provider: provider, repository: repository, host: host, admin: admin, member: login("member@example.test")}
	create := func(title, teamID string) domain.Issue {
		return authRequest[domain.Issue](t, admin, http.MethodPost, host.URL+"/api/issues", map[string]any{"title": title, "teamId": teamID}, outcomeWorkspace, http.StatusCreated)
	}
	for _, title := range []string{"Outcome one", "Outcome two", "Outcome three"} {
		fixture.home = append(fixture.home, create(title, home.ID))
	}
	fixture.vault, fixture.lobby = create("Outcome sealed", vault.ID), create("Outcome lobby", lobby.ID)
	return fixture
}

// chat sends one chat message as client and returns the finished reply.
func (f outcomeFixture) chat(t *testing.T, client *http.Client, message string) domain.AgentMessage {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"message": message, "location": "page"})
	request, err := http.NewRequest(http.MethodPost, f.host.URL+"/api/agent/sessions/stream", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Workspace-Key", outcomeWorkspace)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("chat status %d: %s", response.StatusCode, body)
	}
	sessions := authRequest[[]domain.AgentSession](t, client, http.MethodGet, f.host.URL+"/api/agent/sessions", nil, outcomeWorkspace, http.StatusOK)
	messages := sessions[0].Messages
	return messages[len(messages)-1]
}

func (f outcomeFixture) teamOf(t *testing.T, issue domain.Issue) string {
	t.Helper()
	stored, err := f.repository.IssueRecord(t.Context(), outcomeWorkspace, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	return stored.Team.Key
}

func moveCall(id, issue, team string) [3]string {
	return [3]string{id, "save_issue", `{"id":"` + issue + `","team":"` + team + `"}`}
}

// replyCalls returns the reply's save_issue rows by status.
func replyCalls(reply domain.AgentMessage) map[string][]domain.AgentToolCall {
	calls := map[string][]domain.AgentToolCall{}
	for _, part := range reply.Parts {
		if part.ToolCall != nil && part.ToolCall.Name == "save_issue" {
			calls[part.ToolCall.Status] = append(calls[part.ToolCall.Status], *part.ToolCall)
		}
	}
	return calls
}

// The initiator is in the private team: the agent moves with their rights, the moves land, and an all-success
// reply carries no extra summary.
func TestAgentChatMovesIssuesIntoAPrivateTeamTheInitiatorIsIn(t *testing.T) {
	f := newOutcomeFixture(t)
	one, two := f.home[0].Identifier, f.home[1].Identifier
	f.provider.turns = []string{
		chatCalls("Move both.", moveCall("call_1", one, "VLT"), moveCall("call_2", two, "VLT")),
		chatText("Moved " + one + " and " + two + " to Vault."),
	}
	reply := f.chat(t, f.admin, "Move "+one+" and "+two+" to the Vault team")
	if f.teamOf(t, f.home[0]) != "VLT" || f.teamOf(t, f.home[1]) != "VLT" {
		t.Fatalf("the moves did not land: %s %s", f.teamOf(t, f.home[0]), f.teamOf(t, f.home[1]))
	}
	if calls := replyCalls(reply); len(calls["completed"]) != 2 || len(calls["error"]) != 0 {
		t.Fatalf("two completed save_issue rows: %+v", calls)
	}
	if reply.Content != "Moved "+one+" and "+two+" to Vault." {
		t.Fatalf("an all-success reply is the model's answer: %q", reply.Content)
	}
}

// The initiator is not in the private team: save_issue is really called, refuses with its own reason (the team
// is not visible to them, so it is not found), nothing moves, and the reply reports both failures with that reason.
func TestAgentChatReportsARealDenialWithTheToolsReason(t *testing.T) {
	f := newOutcomeFixture(t)
	one, two := f.home[0].Identifier, f.home[1].Identifier
	f.provider.turns = []string{
		chatCalls("Move both.", moveCall("call_1", one, "VLT"), moveCall("call_2", two, "VLT")),
		chatText("Neither issue could be moved: the Vault team was not found."),
	}
	reply := f.chat(t, f.member, "Move "+one+" and "+two+" to the Vault team")
	if f.teamOf(t, f.home[0]) != "HOM" || f.teamOf(t, f.home[1]) != "HOM" {
		t.Fatal("a refused move changed an issue")
	}
	calls := replyCalls(reply)
	if len(calls["error"]) != 2 || !strings.Contains(calls["error"][0].Error, `team "VLT" not found`) {
		t.Fatalf("both calls ran and failed with the tool's reason: %+v", calls)
	}
	if result := chatLastToolContent(f.provider.body(1)); !strings.Contains(result, `team "VLT" not found`) {
		t.Fatalf("the model saw the tool's error: %q", result)
	}
	for _, want := range []string{"2 change(s) requested: 2 executed (0 succeeded, 2 failed), 0 not executed.", "Failed: " + one + " (save_issue): team \"VLT\" not found", "Failed: " + two} {
		if !strings.Contains(reply.Content, want) {
			t.Fatalf("the reply reports %q: %q", want, reply.Content)
		}
	}
}

// Some issues may move and some may not: the reply's counts are the tool results', whatever the model claims. The
// member moves two Home issues (their team) into Crew (a private team they are in), a Lobby issue they can only
// view, and a Vault issue they cannot see.
func TestAgentChatReportsExactCountsForAPartialMove(t *testing.T) {
	f := newOutcomeFixture(t)
	one, two, lobby, sealed := f.home[0].Identifier, f.home[1].Identifier, f.lobby.Identifier, f.vault.Identifier
	f.provider.turns = []string{
		chatCalls("Move all four.", moveCall("call_1", one, "CRW"), moveCall("call_2", two, "CRW"), moveCall("call_3", lobby, "CRW"), moveCall("call_4", sealed, "CRW")),
		// The model's own account is wrong: two of the four moves succeeded.
		chatText("All four moves failed because you do not have permission to move issues into a private team."),
	}
	reply := f.chat(t, f.member, "Move "+one+", "+two+", "+lobby+" and "+sealed+" into the Crew team")
	calls := replyCalls(reply)
	if f.teamOf(t, f.home[0]) != "CRW" || f.teamOf(t, f.home[1]) != "CRW" || f.teamOf(t, f.lobby) != "LOB" || f.teamOf(t, f.vault) != "VLT" {
		t.Fatalf("teams after the move: %s %s %s %s (%+v)", f.teamOf(t, f.home[0]), f.teamOf(t, f.home[1]), f.teamOf(t, f.lobby), f.teamOf(t, f.vault), calls)
	}
	if len(calls["completed"]) != 2 || len(calls["error"]) != 2 {
		t.Fatalf("two moves succeeded and two failed: %+v", calls)
	}
	for _, want := range []string{
		"4 change(s) requested: 4 executed (2 succeeded, 2 failed), 0 not executed.",
		"Failed: " + lobby + " (save_issue): " + issueEditForbiddenMessage,
		"Failed: " + sealed + " (save_issue): issue \"" + sealed + "\" not found",
	} {
		if !strings.Contains(reply.Content, want) {
			t.Fatalf("the reply reports %q: %q", want, reply.Content)
		}
	}
}

// The reported bug: the model reads the team, then answers that every move failed for lack of permission without
// calling save_issue. It is told once that nothing ran; when it answers the same way again, the reply says that
// nothing was executed instead of passing on the invented failure.
func TestAgentChatDoesNotPassOnFailuresNoToolReported(t *testing.T) {
	f := newOutcomeFixture(t)
	one, two := f.home[0].Identifier, f.home[1].Identifier
	invented := "两个 issue 都迁移失败：你没有权限将 issue 移入私有团队 VLT。"
	f.provider.turns = []string{
		chatCalls("Check the team.", [3]string{"call_team", "get_team", `{"query":"VLT"}`}),
		chatText(invented),
		chatText(invented),
	}
	reply := f.chat(t, f.admin, "把 "+one+" 和 "+two+" 迁移到 VLT 团队")
	if f.provider.count() != 3 {
		t.Fatalf("one read, the invented answer, one nudged retry: %d model calls", f.provider.count())
	}
	if nudge := chatLastUserContent(f.provider.body(2)); nudge != agentUngroundedOutcomeNudge {
		t.Fatalf("the retry is told that nothing ran: %q", nudge)
	}
	if !chatOffers(f.provider.body(0), "save_issue") {
		t.Fatal("a migration request offers save_issue on the first turn")
	}
	if reply.Content != agentNothingExecutedText(true) || strings.Contains(reply.Content, "迁移失败") || strings.Contains(reply.Content, "没有权限") {
		t.Fatalf("the reply says nothing was executed: %q", reply.Content)
	}
	if calls := replyCalls(reply); len(calls) != 0 {
		t.Fatalf("no save_issue ran: %+v", calls)
	}
	if f.teamOf(t, f.home[0]) != "HOM" || f.teamOf(t, f.home[1]) != "HOM" {
		t.Fatal("nothing moved")
	}
}

// After the nudge the model makes the real call: the move lands and the answer is the model's.
func TestAgentChatNudgedOutcomeLeadsToTheRealCall(t *testing.T) {
	f := newOutcomeFixture(t)
	one := f.home[0].Identifier
	f.provider.turns = []string{
		chatText("I couldn't move " + one + ": you don't have permission to move issues into Vault."),
		chatCalls("Call the tool.", moveCall("call_1", one, "VLT")),
		chatText("Moved " + one + " to Vault."),
	}
	reply := f.chat(t, f.admin, "Move "+one+" to the Vault team")
	if f.provider.count() != 3 || f.teamOf(t, f.home[0]) != "VLT" {
		t.Fatalf("the nudge led to the move: %d model calls, team %s", f.provider.count(), f.teamOf(t, f.home[0]))
	}
	if reply.Content != "Moved "+one+" to Vault." || strings.Contains(reply.Content, "permission") {
		t.Fatalf("the invented failure is gone from the reply: %q", reply.Content)
	}
}

func chatLastUserContent(payload map[string]any) string {
	messages, _ := payload["messages"].([]any)
	for index := len(messages) - 1; index >= 0; index-- {
		if message, _ := messages[index].(map[string]any); message["role"] == "user" {
			content, _ := message["content"].(string)
			return content
		}
	}
	return ""
}

func TestAgentOutcomeClaimMatchesReportsNotPlans(t *testing.T) {
	for _, text := range []string{
		"Both moves failed.",
		"You don't have permission to move issues into Vault.",
		"I couldn't move DEV-1.",
		"DEV-1 and DEV-2 have been moved to Vault.",
		"I moved DEV-1 to Vault.",
		"两个 issue 都迁移失败。",
		"你没有权限访问该团队。",
		"无法将 DEV-1 移动到 VLT。",
		"已将 DEV-1 迁移到 VLT 团队。",
	} {
		if !agentOutcomeClaim.MatchString(text) {
			t.Errorf("%q reports an outcome", text)
		}
	}
	for _, text := range []string{
		"Which team should these go to?",
		"I'll move DEV-1 and DEV-2 to Vault.",
		"要把它们移到哪个团队？",
		"DEV-1 is in the Home team.",
	} {
		if agentOutcomeClaim.MatchString(text) {
			t.Errorf("%q reports no outcome", text)
		}
	}
}
