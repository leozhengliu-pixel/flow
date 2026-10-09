package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func waitForLoopRuns(t *testing.T, handler http.Handler, loopID string, count int) []domain.LoopRun {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		runs := requestJSON[[]domain.LoopRun](t, handler, http.MethodGet, "/api/loops/"+loopID+"/runs", nil, http.StatusOK)
		if len(runs) >= count && runs[0].Status != "running" {
			return runs
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("loop %s did not reach %d finished runs", loopID, count)
	return nil
}

func TestLoopTemplatesAndDraftLifecycle(t *testing.T) {
	_, handler := newLoopTestServer(t, &fakeLoopProvider{})
	templates := requestJSON[[]map[string]any](t, handler, http.MethodGet, "/api/loop-templates", nil, http.StatusOK)
	if len(templates) != 6 || templates[1]["id"] != "triage-agent" || templates[1]["requiresTeam"] != true || templates[1]["triggerLabel"] != "On triage" || templates[2]["triggerLabel"] != "Hourly" {
		t.Fatalf("templates = %v", templates)
	}
	if config := templates[3]["triggerConfig"].(map[string]any); config["unit"] != "week" || config["startDate"] == nil {
		t.Fatalf("weekly wrap config = %v", config)
	}
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	teamID := bootstrap.Teams[0].ID
	if body := requestJSON[map[string]any](t, handler, http.MethodPost, "/api/loops", map[string]any{"level": "workspace", "templateId": "triage-agent"}, http.StatusBadRequest); !strings.Contains(fmt.Sprint(body["error"]), "Triage loops must belong to a team") {
		t.Fatalf("workspace triage loop: %v", body)
	}
	draft := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops", map[string]any{"level": "team", "teamId": teamID, "templateId": "triage-agent"}, http.StatusCreated)
	if draft.Status != "draft" || draft.Enabled || draft.Name != "Triage agent" || draft.TriggerType != "issue" || draft.TriggerConfig["event"] != "triage" || !strings.Contains(draft.Instructions, "triage") || draft.TeamID != teamID {
		t.Fatalf("template draft = %#v", draft)
	}
	requestJSON[any](t, handler, http.MethodPost, "/api/loops/"+draft.ID+"/runs", map[string]any{"entityId": "x"}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodPatch, "/api/loops/"+draft.ID, map[string]any{"enabled": true}, http.StatusBadRequest)
	scratch := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops", map[string]any{"level": "workspace", "prompt": "Every Monday summarize bugs"}, http.StatusCreated)
	if scratch.Status != "draft" || scratch.SourcePrompt != "Every Monday summarize bugs" || scratch.TriggerConfig["startDate"] == nil {
		t.Fatalf("prompt draft = %#v", scratch)
	}
	requestJSON[any](t, handler, http.MethodPatch, "/api/loops/"+scratch.ID, map[string]any{"status": "published", "enabled": true}, http.StatusBadRequest)

	published := requestJSON[domain.Loop](t, handler, http.MethodPatch, "/api/loops/"+draft.ID, map[string]any{"status": "published", "enabled": true}, http.StatusOK)
	if published.Status != "published" || !published.Enabled || published.PublishedAt == nil || published.Description == "" {
		t.Fatalf("published = %#v", published)
	}
	requestJSON[any](t, handler, http.MethodPatch, "/api/loops/"+draft.ID, map[string]any{"status": "draft"}, http.StatusBadRequest)
	copied := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops/"+draft.ID+"/duplicate", nil, http.StatusCreated)
	if copied.ID == draft.ID || copied.Status != "draft" || copied.Enabled || copied.Name != "Triage agent (copy)" || copied.Instructions != published.Instructions {
		t.Fatalf("duplicate = %#v", copied)
	}
	listed := requestJSON[[]domain.Loop](t, handler, http.MethodGet, "/api/loops", nil, http.StatusOK)
	if len(listed) != 3 {
		t.Fatalf("listed %d loops", len(listed))
	}
}

func TestLegacyLoopsReadInCurrentShape(t *testing.T) {
	srv, handler := newLoopTestServer(t, &fakeLoopProvider{})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	legacy := domain.Loop{ID: "loop_legacy", Name: "Legacy", Level: "workspace", TriggerType: "issue", TriggerConfig: map[string]any{"action": "created or updated", "filter": true, "filterField": "priority", "filterOperator": "isNot", "filterValue": "Urgent"}, Instructions: "Do things. Then more.", TeamAccess: "allPublic", Enabled: true, OwnerID: bootstrap.Viewer.ID, Creator: bootstrap.Viewer, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := srv.store.MutateWorkspace(t.Context(), bootstrap.Workspace.URLKey, "loop.created", legacy.ID, nil, func(data *domain.Bootstrap) error {
		data.Loops = append(data.Loops, legacy)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	loop := requestJSON[domain.Loop](t, handler, http.MethodGet, "/api/loops/loop_legacy", nil, http.StatusOK)
	filters, _ := loop.TriggerConfig["filters"].([]any)
	if loop.Status != "published" || loop.CodeAccess != "read" || loop.TriggerConfig["event"] != "updated" || loop.TriggerConfig["action"] != nil || len(filters) != 1 || loop.Description != "Do things." {
		t.Fatalf("legacy loop = %#v", loop)
	}
	if filter := filters[0].(map[string]any); filter["field"] != "priority" || filter["operator"] != "isNot" || filter["value"] != "Urgent" {
		t.Fatalf("legacy filter = %v", filter)
	}
	schedule := normalizeLoopTriggerConfig("schedule", map[string]any{"starting": "2026-01-02", "unit": "week"})
	if schedule["startDate"] != "2026-01-02" || schedule["starting"] != nil || schedule["time"] != "10:00" {
		t.Fatalf("legacy schedule = %v", schedule)
	}
}

func TestLoopRunTurnLimitEndsWithSummaryAndRecordsProgress(t *testing.T) {
	replies := []string{`tool:report_progress {"title":"Reviewing issues","message":"I'll check every team."}`}
	for len(replies) < loopMaxToolTurns {
		replies = append(replies, "tool:list_teams {}")
	}
	replies = append(replies, "Checked every team; nothing needed changes.")
	provider := &fakeLoopProvider{replies: replies}
	var seen domain.LoopRun
	_, handler := newLoopTestServer(t, provider)
	loop := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops", map[string]any{"name": "Busy", "instructions": "Check every team.", "status": "published"}, http.StatusCreated)
	provider.wait = func(n int) {
		if n == 3 {
			// Mid-run: the run already shows its step and finished tool calls.
			runs := requestJSON[[]domain.LoopRun](t, handler, http.MethodGet, "/api/loops/"+loop.ID+"/runs", nil, http.StatusOK)
			seen = runs[0]
		}
	}
	run := requestJSON[domain.LoopRun](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs", nil, http.StatusAccepted)
	if run.TriggerLabel != "Manual run" {
		t.Fatalf("trigger label = %q", run.TriggerLabel)
	}
	finished := waitForLoopRun(t, handler, loop.ID)
	if finished.Status != "completed" || finished.Output != "Checked every team; nothing needed changes." {
		t.Fatalf("finished = %#v", finished)
	}
	if len(finished.Steps) != 1 || finished.Steps[0].Title != "Reviewing issues" || finished.Steps[0].Message == "" || finished.Steps[0].Order != 1 {
		t.Fatalf("steps = %#v", finished.Steps)
	}
	if len(finished.ToolCalls) != loopMaxToolTurns-1 || finished.ToolCalls[0].Name != "list_teams" || finished.ToolCalls[0].Label != "Listed teams" || finished.ToolCalls[0].Order != 2 || finished.ToolCalls[0].FinishedAt == nil {
		t.Fatalf("tool calls = %d %#v", len(finished.ToolCalls), finished.ToolCalls[0])
	}
	if seen.Status != "running" || len(seen.Steps) != 1 || len(seen.ToolCalls) < 2 {
		t.Fatalf("live run = %#v", seen)
	}
	single := requestJSON[domain.LoopRun](t, handler, http.MethodGet, "/api/loops/"+loop.ID+"/runs/"+run.ID, nil, http.StatusOK)
	if single.ID != run.ID || single.Status != "completed" {
		t.Fatalf("single run = %#v", single)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	last := len(provider.tools) - 1
	if last != loopMaxToolTurns || len(provider.tools[last]) != 0 || !strings.Contains(provider.inputs[last], "Do not call any more tools") {
		t.Fatalf("final turn: requests=%d tools=%v", last+1, provider.tools[last])
	}
}

func TestLoopRunLinksUseAppURLAndRunsOnChosenIssue(t *testing.T) {
	provider := &fakeLoopProvider{}
	srv, handler := newLoopTestServer(t, provider)
	srv.allowedOrigin = "https://flow.example"
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Linked", "teamId": bootstrap.Teams[0].ID}, http.StatusCreated)
	loop := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops", map[string]any{"name": "On demand", "instructions": "Look at the issue.", "triggerType": "issue", "triggerConfig": map[string]any{"event": "comment"}, "status": "published"}, http.StatusCreated)
	requestJSON[any](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs", nil, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs", map[string]any{"entityType": "project", "entityId": issue.ID}, http.StatusBadRequest)
	provider.mu.Lock()
	provider.replies = []string{`tool:get_issue {"id":"` + issue.Identifier + `"}`, "Done."}
	provider.mu.Unlock()
	run := requestJSON[domain.LoopRun](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs", map[string]any{"entityId": issue.Identifier}, http.StatusAccepted)
	if run.EntityID != issue.ID || run.EntityIdentifier != issue.Identifier || run.TriggerLabel != "Manual run" {
		t.Fatalf("run = %#v", run)
	}
	finished := waitForLoopRun(t, handler, loop.ID)
	if finished.Status != "completed" || len(finished.ToolCalls) != 1 || finished.ToolCalls[0].Args != issue.Identifier {
		t.Fatalf("finished = %#v", finished)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if !strings.Contains(provider.inputs[1], "https://flow.example/") || strings.Contains(provider.inputs[1], "flow.internal") {
		t.Fatalf("tool result links: %s", provider.inputs[1])
	}
}

func TestIssuePropertyTriggersFireOnlyOnMatchingChanges(t *testing.T) {
	provider := &fakeLoopProvider{}
	_, handler := newLoopTestServer(t, provider)
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	teamID := bootstrap.Teams[0].ID
	var started domain.WorkflowState
	for _, state := range bootstrap.States {
		if state.Type == "started" && (state.TeamID == "" || state.TeamID == teamID) {
			started = state
			break
		}
	}
	if started.ID == "" {
		t.Fatal("no started state in fixture")
	}
	statusLoop := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops", map[string]any{"name": "Started", "instructions": "React.", "triggerType": "issue", "status": "published",
		"triggerConfig": map[string]any{"event": "status", "value": "started", "filters": []any{map[string]any{"field": "assignee", "operator": "is", "value": nil}}}}, http.StatusCreated)
	priorityLoop := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops", map[string]any{"name": "Urgent", "instructions": "React.", "triggerType": "issue", "status": "published",
		"triggerConfig": map[string]any{"event": "priority", "value": "1"}}, http.StatusCreated)
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Watch me", "teamId": teamID}, http.StatusCreated)
	requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+issue.ID, map[string]any{"priority": 2}, http.StatusOK)
	time.Sleep(100 * time.Millisecond)
	for _, id := range []string{statusLoop.ID, priorityLoop.ID} {
		if runs := requestJSON[[]domain.LoopRun](t, handler, http.MethodGet, "/api/loops/"+id+"/runs", nil, http.StatusOK); len(runs) != 0 {
			t.Fatalf("loop %s ran on an unrelated change: %#v", id, runs)
		}
	}
	requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+issue.ID, map[string]any{"stateId": started.ID}, http.StatusOK)
	runs := waitForLoopRuns(t, handler, statusLoop.ID, 1)
	if want := "Triggered by " + issue.Identifier + " status → " + started.Name; runs[0].TriggerLabel != want {
		t.Fatalf("status label = %q, want %q", runs[0].TriggerLabel, want)
	}
	if runs[0].TriggerReason != "status" || runs[0].TriggerValue != started.Name {
		t.Fatalf("status reason = %q %q", runs[0].TriggerReason, runs[0].TriggerValue)
	}
	requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+issue.ID, map[string]any{"priority": 1}, http.StatusOK)
	runs = waitForLoopRuns(t, handler, priorityLoop.ID, 1)
	if !strings.Contains(runs[0].TriggerLabel, "priority → Urgent") {
		t.Fatalf("priority label = %q", runs[0].TriggerLabel)
	}
}

func TestLoopEventMatchingTriageAndLabels(t *testing.T) {
	team := domain.Team{ID: "team_a", Key: "A", Name: "Alpha"}
	data := domain.Bootstrap{Teams: []domain.Team{team}, TeamSettings: map[string]domain.TeamSettings{"team_a": {TeamID: "team_a", TriageEnabled: true}}}
	triageLoop := domain.Loop{TriggerType: "issue", Status: "published", Enabled: true, TriggerConfig: map[string]any{"event": "triage"}}
	issue := domain.Issue{ID: "i1", Identifier: "A-1", Team: team, State: domain.WorkflowState{ID: "s_backlog", Name: "Backlog", Type: "backlog"}}
	if ok, label := loopEventMatches(data, triageLoop, loopEvent{EntityType: "issue", EntityID: "i1", Kind: "created", Issue: &issue}); !ok || label != "Triggered by A-1 entering triage" {
		t.Fatalf("created into triage: %v %q", ok, label)
	}
	movedBack := map[string]json.RawMessage{"state": json.RawMessage(`{"id":"s_started","name":"In Progress","type":"started"}`)}
	if ok, _ := loopEventMatches(data, triageLoop, loopEvent{EntityType: "issue", EntityID: "i1", Kind: "updated", Issue: &issue, Previous: movedBack}); !ok {
		t.Fatal("moving into triage did not fire")
	}
	if ok, _ := loopEventMatches(data, triageLoop, loopEvent{EntityType: "issue", EntityID: "i1", Kind: "updated", Issue: &issue, Previous: map[string]json.RawMessage{"priority": json.RawMessage(`0`)}}); ok {
		t.Fatal("an update inside triage fired again")
	}
	triaged := time.Now()
	accepted := issue
	accepted.TriagedAt = &triaged
	if ok, _ := loopEventMatches(data, triageLoop, loopEvent{EntityType: "issue", EntityID: "i1", Kind: "created", Issue: &accepted}); ok {
		t.Fatal("triaged issue counted as in triage")
	}
	labelLoop := domain.Loop{TriggerType: "issue", TriggerConfig: map[string]any{"event": "labels", "value": "security"}}
	labeled := issue
	labeled.Labels = []domain.IssueLabel{{ID: "l1", Name: "Bug"}, {ID: "l2", Name: "Security"}}
	previous := map[string]json.RawMessage{"labels": json.RawMessage(`[{"id":"l1","name":"Bug"}]`)}
	if ok, label := loopEventMatches(data, labelLoop, loopEvent{EntityType: "issue", Kind: "updated", Issue: &labeled, Previous: previous}); !ok || !strings.Contains(label, "label Security added") {
		t.Fatalf("label added: %v %q", ok, label)
	}
	previous["labels"] = json.RawMessage(`[{"id":"l1","name":"Bug"},{"id":"l2","name":"Security"}]`)
	if ok, _ := loopEventMatches(data, labelLoop, loopEvent{EntityType: "issue", Kind: "updated", Issue: &labeled, Previous: previous}); ok {
		t.Fatal("unchanged labels fired")
	}
	projectLoop := domain.Loop{TriggerType: "project", TriggerConfig: map[string]any{"event": "status"}}
	if ok, _ := loopEventMatches(data, projectLoop, loopEvent{EntityType: "project", Kind: "updated", Previous: map[string]json.RawMessage{"name": json.RawMessage(`"x"`)}}); ok {
		t.Fatal("project rename matched a status trigger")
	}
	if ok, _ := loopEventMatches(data, projectLoop, loopEvent{EntityType: "project", Kind: "updated", Previous: map[string]json.RawMessage{"status": json.RawMessage(`{}`)}}); !ok {
		t.Fatal("project status change did not match")
	}
}

func TestNextLoopRunHourlyAndWeekdays(t *testing.T) {
	created := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	cases := []struct {
		config map[string]any
		after  time.Time
		want   time.Time
	}{
		{map[string]any{"interval": float64(1), "unit": "hour", "time": "09:00", "startDate": "2026-09-01"}, time.Date(2026, 9, 3, 13, 20, 0, 0, time.UTC), time.Date(2026, 9, 3, 14, 0, 0, 0, time.UTC)},
		{map[string]any{"interval": float64(3), "unit": "hour", "time": "09:00", "startDate": "2026-09-01"}, time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC), time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)},
		// 2026-09-29 is a Tuesday: the next Friday is 2026-10-02.
		{map[string]any{"interval": float64(1), "unit": "week", "time": "16:00", "startDate": "2026-09-29", "weekdays": []any{"fri"}}, time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC), time.Date(2026, 10, 2, 16, 0, 0, 0, time.UTC)},
		{map[string]any{"interval": float64(1), "unit": "week", "time": "09:00", "startDate": "2026-09-01", "weekdays": []any{"mon", "thu"}}, time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC), time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)},
		// Every two weeks from the week of 2026-09-01 (Sun 2026-08-30): the week of Sep 6 is skipped.
		{map[string]any{"interval": float64(2), "unit": "week", "time": "09:00", "startDate": "2026-09-01", "weekdays": []any{"mon"}}, time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC)},
		{map[string]any{"interval": float64(1), "unit": "day", "time": "07:00", "startDate": "2026-09-01", "weekdays": []any{"mon", "tue", "wed", "thu", "fri"}}, time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC), time.Date(2026, 10, 5, 7, 0, 0, 0, time.UTC)},
	}
	for index, item := range cases {
		if got := nextLoopRun(item.config, created, item.after); !got.Equal(item.want) {
			t.Errorf("case %d: next = %s, want %s", index, got, item.want)
		}
	}
}

func TestSaveCommentWithIssueAsIDCreatesComment(t *testing.T) {
	repository, actor, ctx := newMCPToolTestContext(t)
	issue := repository.Bootstrap().Issues[0]
	service := &server{store: repository}
	if _, err := service.callFlowTool(ctx, actor, "save_comment", map[string]any{"id": issue.Identifier, "body": "Posted by a loop"}); err != nil {
		t.Fatal(err)
	}
	comments := repository.Bootstrap().Comments[issue.ID]
	if len(comments) == 0 || comments[len(comments)-1].Body != "Posted by a loop" {
		t.Fatalf("comments = %#v", comments)
	}
}

func TestSaveCommentWithIssueIdentifierAsParentCreatesComment(t *testing.T) {
	repository, actor, ctx := newMCPToolTestContext(t)
	issue := repository.Bootstrap().Issues[0]
	service := &server{store: repository}
	if _, err := service.callFlowTool(ctx, actor, "save_comment", map[string]any{"issueId": issue.Identifier, "body": "Posted by identifier"}); err != nil {
		t.Fatal(err)
	}
	comments := repository.Bootstrap().Comments[issue.ID]
	if len(comments) == 0 || comments[len(comments)-1].Body != "Posted by identifier" {
		t.Fatalf("comments = %#v", comments)
	}
}

// streamAgent posts a streaming agent request and answers chip questions with the given answers.
func streamAgent(t *testing.T, srv *server, host *httptest.Server, path string, body map[string]any, answers []string) string {
	t.Helper()
	raw, _ := json.Marshal(body)
	done := make(chan string, 1)
	go func() {
		response, err := http.Post(host.URL+path, "application/json", strings.NewReader(string(raw)))
		if err != nil {
			done <- "error: " + err.Error()
			return
		}
		defer response.Body.Close()
		var builder strings.Builder
		reader := bufio.NewReader(response.Body)
		_, _ = io.Copy(&builder, reader)
		done <- builder.String()
	}()
	for _, answer := range answers {
		deadline := time.Now().Add(5 * time.Second)
		var elicitationID, sessionID string
		for elicitationID == "" && time.Now().Before(deadline) {
			srv.agentApprovalsMu.Lock()
			for id, pending := range srv.agentElicitations {
				elicitationID, sessionID = id, pending.SessionID
			}
			srv.agentApprovalsMu.Unlock()
			time.Sleep(10 * time.Millisecond)
		}
		if elicitationID == "" {
			t.Fatalf("no question was asked for answer %q", answer)
		}
		payload, _ := json.Marshal(map[string]any{"action": "accept", "content": map[string]any{"answer": answer}})
		response, err := http.Post(host.URL+"/api/agent/sessions/"+sessionID+"/elicitations/"+elicitationID, "application/json", strings.NewReader(string(payload)))
		if err != nil || response.StatusCode != http.StatusOK {
			t.Fatalf("answer %q: %v %v", answer, err, response)
		}
		response.Body.Close()
	}
	select {
	case stream := <-done:
		return stream
	case <-time.After(10 * time.Second):
		t.Fatal("agent stream did not finish")
		return ""
	}
}

func TestLoopBuilderAsksThenPublishesTemplateDraft(t *testing.T) {
	provider := &fakeLoopProvider{}
	srv, handler := newLoopTestServer(t, provider)
	host := httptest.NewServer(handler)
	t.Cleanup(host.Close)
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	draft := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops", map[string]any{"level": "team", "teamId": bootstrap.Teams[0].ID, "templateId": "triage-agent"}, http.StatusCreated)
	provider.replies = []string{
		`tool:ask_question {"question":"How much should the triage loop do on its own?","options":["Route and close clear duplicates","Route, but don't close","Suggest changes only"]}`,
		`tool:save_loop {"id":"` + draft.ID + `","instructions":"Route each triage issue. Never close issues."}`,
		`tool:save_loop {"id":"` + draft.ID + `","publish":true}`,
		"Your Triage agent loop is live and routes new triage issues.",
	}
	stream := streamAgent(t, srv, host, "/api/agent/sessions/stream", map[string]any{"message": "Set up this loop from the Triage agent template", "location": "toolbar", "loopIds": []string{draft.ID}}, []string{"Route, but don't close"})
	for _, want := range []string{"event: elicitation.requested", "event: elicitation.resolved", "event: session.completed"} {
		if !strings.Contains(stream, want) {
			t.Fatalf("stream missing %q: %s", want, stream)
		}
	}
	if strings.Contains(stream, "tool.approval_required") {
		t.Fatalf("save_loop on the session's draft asked for approval: %s", stream)
	}
	loop := requestJSON[domain.Loop](t, handler, http.MethodGet, "/api/loops/"+draft.ID, nil, http.StatusOK)
	if loop.Status != "published" || !loop.Enabled || loop.Instructions != "Route each triage issue. Never close issues." {
		t.Fatalf("loop = %#v", loop)
	}
	sessions := requestJSON[[]domain.AgentSession](t, handler, http.MethodGet, "/api/agent/sessions", nil, http.StatusOK)
	if len(sessions) != 1 || len(sessions[0].LoopIDs) != 1 {
		t.Fatalf("sessions = %#v", sessions)
	}
	titles, answered := []string{}, ""
	for _, part := range sessions[0].Messages[1].Parts {
		if part.Type == "elicitation" && part.ToolCall != nil {
			answered = string(part.ToolCall.Result)
		}
		if part.ToolCall != nil && part.ToolCall.Name == "save_loop" {
			titles = append(titles, part.ToolCall.Title)
		}
		if part.Type == "toolCall" && part.ToolCall != nil && part.ToolCall.Name == loopQuestionTool {
			t.Fatal("ask_question left a tool row")
		}
	}
	if !strings.Contains(answered, "Route, but don't close") || strings.Join(titles, "|") != "Updated workflow definition draft|Created automation" {
		t.Fatalf("answered=%s titles=%v", answered, titles)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if !strings.Contains(provider.inputs[0], "Loop builder") || !strings.Contains(provider.inputs[0], "I've opened a draft") || !strings.Contains(strings.Join(provider.tools[0], ","), loopQuestionTool) {
		t.Fatalf("loop builder prompt/tools missing: %v", provider.tools[0])
	}
	if !strings.Contains(provider.inputs[1], "Route, but don't close") {
		t.Fatalf("answer not returned to the model: %s", provider.inputs[1])
	}
}

func TestLoopBuilderFillsPromptDraftWithoutPublishing(t *testing.T) {
	provider := &fakeLoopProvider{}
	srv, handler := newLoopTestServer(t, provider)
	host := httptest.NewServer(handler)
	t.Cleanup(host.Close)
	draft := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops", map[string]any{"level": "workspace", "prompt": "Every Monday at 9 summarize new bugs"}, http.StatusCreated)
	provider.replies = []string{
		`tool:save_loop {"id":"` + draft.ID + `","name":"Monday bug summary","trigger":"schedule","unit":"week","time":"09:00","weekdays":["monday"],"instructions":"Summarize the bugs created last week."}`,
		"I set up a Monday bug summary. It remains a draft for your review; I didn't publish, enable, or run it.",
	}
	stream := streamAgent(t, srv, host, "/api/agent/sessions/stream", map[string]any{"message": "Every Monday at 9 summarize new bugs", "location": "toolbar", "loopIds": []string{draft.ID}}, nil)
	if !strings.Contains(stream, "event: session.completed") || strings.Contains(stream, "tool.approval_required") {
		t.Fatalf("stream = %s", stream)
	}
	loop := requestJSON[domain.Loop](t, handler, http.MethodGet, "/api/loops/"+draft.ID, nil, http.StatusOK)
	if loop.Status != "draft" || loop.Enabled || loop.Name != "Monday bug summary" || loop.TriggerConfig["unit"] != "week" || fmt.Sprint(loop.TriggerConfig["weekdays"]) != "[mon]" {
		t.Fatalf("loop = %#v", loop)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if !strings.Contains(provider.inputs[0], "It remains a draft for your review") {
		t.Fatal("prompt draft instructions missing from system prompt")
	}
}

func TestRepairLoopLevelKeepsOldTeamLoopsEditable(t *testing.T) {
	loop := domain.Loop{Level: "team", TriggerConfig: map[string]any{"teamIds": []any{"team_dev"}}}
	repairLoopLevel(&loop)
	if loop.Level != "team" || loop.TeamID != "team_dev" {
		t.Fatalf("loop = %#v", loop)
	}
	loop = domain.Loop{Level: "team", TriggerConfig: map[string]any{}}
	repairLoopLevel(&loop)
	if loop.Level != "workspace" || loop.TeamID != "" {
		t.Fatalf("loop without a team should become workspace-level: %#v", loop)
	}
}
