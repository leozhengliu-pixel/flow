package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

// seedStoreMembers adds workspace members through the store the way people join (register, verify, accept an
// invitation) and returns their ids by email.
func seedStoreMembers(t *testing.T, srv *server, people [][2]string) map[string]string {
	t.Helper()
	workspaceID := srv.store.Bootstrap().Workspace.ID
	ids := map[string]string{}
	for _, person := range people {
		user, token, err := srv.store.Register(t.Context(), person[0], person[1], "bulk-member-pass")
		if err != nil {
			t.Fatal(err)
		}
		if err := srv.store.VerifyEmail(t.Context(), token); err != nil {
			t.Fatal(err)
		}
		invitation, err := srv.store.Invite(t.Context(), workspaceID, "usr_admin", person[1], "member", nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := srv.store.AcceptInvitation(t.Context(), invitation.Token, user.ID); err != nil {
			t.Fatal(err)
		}
		ids[person[1]] = user.ID
	}
	if srv.authDisabled {
		// Without sign-in, membership writes check the workspace's own member list: the same people are on it.
		if err := mutateTestWorkspace(t, srv.store, func(data *domain.Bootstrap) {
			materializeDevelopmentMembers(data)
			for _, person := range people {
				user := domain.User{ID: ids[person[1]], Name: person[0], DisplayName: person[0], Email: person[1], Active: true, EmailVerified: true}
				data.Users = append(data.Users, user)
				data.Members = append(data.Members, domain.WorkspaceMember{User: user, Role: "member", Status: "active"})
			}
		}); err != nil {
			t.Fatal(err)
		}
	}
	return ids
}

func chatOffers(payload map[string]any, name string) bool {
	tools, _ := payload["tools"].([]any)
	return slices.ContainsFunc(tools, func(raw any) bool {
		function, _ := raw.(map[string]any)["function"].(map[string]any)
		return function["name"] == name
	})
}

func chatLastToolContent(payload map[string]any) string {
	messages, _ := payload["messages"].([]any)
	for index := len(messages) - 1; index >= 0; index-- {
		if message, _ := messages[index].(map[string]any); message["role"] == "tool" {
			content, _ := message["content"].(string)
			return content
		}
	}
	return ""
}

func lastAgentReply(t *testing.T, handler http.Handler) domain.AgentMessage {
	t.Helper()
	session := requestJSON[[]domain.AgentSession](t, handler, http.MethodGet, "/api/agent/sessions", nil, http.StatusOK)[0]
	return session.Messages[len(session.Messages)-1]
}

// The reported request: a team of 40 named people. save_team is offered on the first turn (no load_tools step), the
// model passes everyone in one call, and the one result says who was added and who was not (a person who is not a
// member, a name two members share), so the reply takes two model turns.
func TestAgentChatCreatesFortyPersonTeamInOneStep(t *testing.T) {
	provider := &thinkingChatProvider{}
	srv, handler := newThinkingChatServer(t, provider)
	ids := seedStoreMembers(t, srv, fortyPeople())
	names := []string{}
	for index := 1; index <= 38; index++ {
		names = append(names, fmt.Sprintf("Person %02d", index))
	}
	names = append(names, "Alex Kim", "outsider@example.test")
	arguments, _ := json.Marshal(map[string]any{"name": "Platform", "key": "PLAT", "members": names})
	provider.turns = []string{
		chatCalls("Create the team with everyone at once.", [3]string{"call_team", "save_team", string(arguments)}),
		chatText("Created Platform with 38 people. Not added: Alex Kim (two members have that name) and outsider@example.test (not in the workspace)."),
	}
	streamRuntime(t, handler, "/api/agent/sessions/stream", `{"message":"创建一个40人的团队，叫 Platform，把这些人加进去：`+strings.Join(names, "、")+`","location":"page"}`)
	if provider.count() != 2 {
		t.Fatalf("the bulk request takes two model turns, got %d", provider.count())
	}
	if !chatOffers(provider.body(0), "save_team") {
		t.Fatal("creating a team offers save_team on the first turn, without a load_tools step")
	}
	result := chatLastToolContent(provider.body(1))
	for _, want := range []string{`"unresolved"`, `"query":"Alex Kim"`, `"status":"ambiguous"`, `"query":"outsider@example.test"`, `"status":"not_found"`, `"memberCount":39`} {
		if !strings.Contains(result, want) {
			t.Fatalf("the save_team result reports %s: %s", want, result)
		}
	}
	data := srv.store.Bootstrap()
	team := data.Teams[slices.IndexFunc(data.Teams, func(team domain.Team) bool { return team.Key == "PLAT" })]
	for index := 1; index <= 38; index++ {
		id := ids[fmt.Sprintf("person%02d@bulk.test", index)]
		if !slices.ContainsFunc(data.TeamMembers, func(member domain.TeamMember) bool { return member.TeamID == team.ID && member.UserID == id }) {
			t.Fatalf("Person %02d is on the team", index)
		}
	}
	reply := lastAgentReply(t, handler)
	if !strings.HasPrefix(reply.Content, "Created Platform with 38 people.") || slices.ContainsFunc(reply.Parts, func(part domain.AgentMessagePart) bool { return part.Type == "error" }) {
		t.Fatalf("the reply is the answer, without an error: %q %+v", reply.Content, reply.Parts)
	}
}

// A reply that reaches its turn budget is not a bare error: the last turn runs without tools (tool_choice none),
// told to say what was done and what is left, and its answer is the reply, with a line saying it stopped.
func TestAgentChatTurnBudgetEndsWithASummary(t *testing.T) {
	provider := &thinkingChatProvider{}
	srv, handler := newThinkingChatServer(t, provider)
	srv.agent.MaxToolTurns = 4
	for index := range 4 {
		provider.turns = append(provider.turns, chatCalls("Keep looking.", [3]string{fmt.Sprintf("call_%d", index), "list_teams", fmt.Sprintf(`{"query":"team %d"}`, index)}))
	}
	provider.turns = append(provider.turns, chatText("I looked through four team searches; comparing the rest is still left. Say Continue to go on."))
	streamRuntime(t, handler, "/api/agent/sessions/stream", `{"message":"Compare every team's workload.","location":"page"}`)
	if provider.count() != 5 {
		t.Fatalf("four tool turns and the summary turn, got %d", provider.count())
	}
	last := provider.body(4)
	if last["tool_choice"] != "none" || !strings.Contains(chatLastToolContent(last), "This reply must stop now") || !strings.Contains(chatLastToolContent(last), "step limit (4 steps)") {
		t.Fatalf("the summary turn calls no tools and is told why: %v %q", last["tool_choice"], chatLastToolContent(last))
	}
	for index := range 4 {
		if provider.body(index)["tool_choice"] != nil {
			t.Fatalf("turn %d may call tools", index)
		}
	}
	reply := lastAgentReply(t, handler)
	if !strings.HasPrefix(reply.Content, "I looked through four team searches") {
		t.Fatalf("the summary is the reply: %q", reply.Content)
	}
	notice := reply.Parts[len(reply.Parts)-1]
	if notice.Type != "event" || notice.Text != "Stopped at the step limit for one reply (4 steps)" || slices.ContainsFunc(reply.Parts, func(part domain.AgentMessagePart) bool { return part.Type == "error" }) {
		t.Fatalf("the reply says it stopped at the limit, without an error row: %+v", reply.Parts)
	}
}

// A model that keeps sending the same failing call is not run again: the repeats are answered from the first
// failure, and after agentRepeatedFailureLimit of them the reply stops — long before the turn budget — with Flow's
// own summary when the model has none (in the user's language, with Continue offered).
func TestAgentChatStopsRepeatingAFailedCall(t *testing.T) {
	provider := &thinkingChatProvider{}
	_, handler := newThinkingChatServer(t, provider)
	for index := range 1 + agentRepeatedFailureLimit {
		provider.turns = append(provider.turns, chatCalls("Try again.", [3]string{fmt.Sprintf("call_%d", index), "get_issue", `{"id":"NOPE-404"}`}))
	}
	provider.turns = append(provider.turns, chatText(""))
	streamRuntime(t, handler, "/api/agent/sessions/stream", `{"message":"看一下 NOPE-404","location":"page"}`)
	if provider.count() != 2+agentRepeatedFailureLimit {
		t.Fatalf("one failed call, %d repeats, then the summary turn: %d model turns", agentRepeatedFailureLimit, provider.count())
	}
	if repeat := chatLastToolContent(provider.body(2)); !strings.Contains(repeat, "already made this exact call and it failed") {
		t.Fatalf("a repeat is answered from the earlier failure: %s", repeat)
	}
	reply := lastAgentReply(t, handler)
	if !strings.Contains(reply.Content, "工具调用反复失败") || !strings.Contains(reply.Content, "```suggestions\n继续\n```") {
		t.Fatalf("Flow's own summary offers Continue in the user's language: %q", reply.Content)
	}
	calls := 0
	for _, part := range reply.Parts {
		if part.ToolCall != nil && part.ToolCall.Name == "get_issue" {
			calls++
			if part.Status != "error" {
				t.Fatalf("every get_issue row failed: %+v", part)
			}
		}
	}
	if calls != 1+agentRepeatedFailureLimit {
		t.Fatalf("each call shows as a row: %d", calls)
	}
}

// The tool-call budget holds even when one turn sends many calls: calls over it are not run, and the reply ends
// with the summary turn.
func TestAgentChatToolCallBudgetStopsParallelCalls(t *testing.T) {
	provider := &thinkingChatProvider{}
	srv, handler := newThinkingChatServer(t, provider)
	srv.agent.MaxToolCalls = 5
	calls := [][3]string{}
	for index := range 8 {
		calls = append(calls, [3]string{fmt.Sprintf("call_%d", index), "list_teams", fmt.Sprintf(`{"query":"q%d"}`, index)})
	}
	provider.turns = []string{chatCalls("Search everything.", calls...), chatText("Ran five searches; three were not run.")}
	streamRuntime(t, handler, "/api/agent/sessions/stream", `{"message":"Search all teams.","location":"page"}`)
	if provider.count() != 2 {
		t.Fatalf("the call budget ends the reply after the turn: %d", provider.count())
	}
	refused := 0
	messages, _ := provider.body(1)["messages"].([]any)
	for _, raw := range messages {
		if message, _ := raw.(map[string]any); message["role"] == "tool" && strings.Contains(fmt.Sprint(message["content"]), "tool-call budget (5 calls)") {
			refused++
		}
	}
	if refused != 3 || provider.body(1)["tool_choice"] != "none" {
		t.Fatalf("three calls over the budget are not run, and the summary turn calls no tools: %d %v", refused, provider.body(1)["tool_choice"])
	}
	if notice := lastAgentReply(t, handler).Parts; notice[len(notice)-1].Text != "Stopped at the tool-call limit for one reply (5 calls)" {
		t.Fatalf("notice: %+v", notice[len(notice)-1])
	}
}

// A built-in thread run has the same budget: at its turn cap it asks for the last word without tools and posts it
// as the run's reply, not "Agent reached its tool execution limit".
func TestBuiltinThreadRunTurnBudgetPostsASummary(t *testing.T) {
	h := newDelegationHarness(t, "acme/api")
	h.s.agent.MaxToolTurns = 3
	run := h.delegate(t, "整理团队列表", "请列出所有团队并对比它们的负载。", func(index int, _ domain.Issue) string {
		if index < 3 {
			return chatToolCallSSE(fmt.Sprintf("c_%d", index), "list_teams", map[string]any{"query": fmt.Sprintf("q%d", index)})
		}
		return chatTextSSE("已列出团队，负载对比还没完成。回复“继续”接着做。")
	})
	if len(run.requests) != 4 {
		t.Fatalf("three tool turns and the summary turn: %d", len(run.requests))
	}
	if !strings.Contains(run.requests[3].lastContent(), "This reply must stop now") {
		t.Fatalf("the summary turn is told why: %q", run.requests[3].lastContent())
	}
	if errors := run.errors(); len(errors) > 0 {
		t.Fatalf("no tool-limit error: %v", errors)
	}
	if !strings.Contains(run.response(), "负载对比还没完成") {
		t.Fatalf("the summary is the run's reply: %q", run.response())
	}
}

func TestAgentToolBudgetLetsFailedCallsRunAgainAfterAWrite(t *testing.T) {
	budget := newAgentToolBudget(10)
	lookup := domain.AgentToolCall{Name: "get_issue", Arguments: json.RawMessage(`{"id":"ENG-9"}`)}
	if _, run := budget.before(lookup); !run {
		t.Fatal("a first call runs")
	}
	budget.after(lookup, "issue not found", true)
	if message, run := budget.before(domain.AgentToolCall{Name: "get_issue", Arguments: json.RawMessage(`{ "id": "ENG-9" }`)}); run || !strings.Contains(message, "issue not found") {
		t.Fatalf("the same call spelled differently is a repeat: %v %q", run, message)
	}
	write := domain.AgentToolCall{Name: "save_issue", Arguments: json.RawMessage(`{"title":"ENG-9"}`)}
	if _, run := budget.before(write); !run {
		t.Fatal("another call runs")
	}
	budget.after(write, `{"id":"issue_9"}`, false)
	if _, run := budget.before(lookup); !run {
		t.Fatal("after a successful write the lookup may run again")
	}
	budget.endTurn()
	if budget.stop != "" {
		t.Fatalf("no stop: %q", budget.stop)
	}
	for range agentFailedTurnLimit {
		call := domain.AgentToolCall{Name: "get_issue", Arguments: json.RawMessage(fmt.Sprintf(`{"id":"X-%d"}`, budget.calls))}
		budget.before(call)
		budget.after(call, "not found", true)
		budget.endTurn()
	}
	if budget.stop != agentBudgetFailures {
		t.Fatalf("turns where every call fails stop the reply: %q", budget.stop)
	}
}
