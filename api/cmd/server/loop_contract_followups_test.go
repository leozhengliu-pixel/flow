package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestLoopSemanticIssueMatchRefusalAndCache(t *testing.T) {
	provider := &fakeLoopProvider{matchReplies: []string{
		`tool:record_issue_match {"matches":false,"reason":"Reproduction steps are already present."}`,
		`tool:record_issue_match {"matches":true,"reason":"The bug has no reproduction steps."}`,
	}}
	srv, handler := newReliableLoopServer(t, provider)
	data := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Login crashes", "description": "Steps: open login, click sign in.", "teamId": data.Teams[0].ID}, http.StatusCreated)
	contract := &domain.LoopOutputContract{Source: loopContractPerson, Outputs: []domain.LoopContractOutput{{Kind: "comment", Operation: loopOpCreate, Target: domain.LoopContractTarget{Mode: "matching", Match: "bugs without reproduction steps"}}}}
	rec := &loopRunRecorder{workspace: data.Workspace.URLKey, run: domain.LoopRun{LoopID: "loop_match", VersionID: "loop_match_v1"}}
	call := domain.AgentToolCall{Name: "save_comment", Arguments: json.RawMessage(`{"issueId":"` + issue.ID + `","body":"Please add repro steps."}`)}
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := srv.checkLoopOutputCall(context.Background(), rec, data, contract, call, "comment"); err == nil || !strings.Contains(err.Error(), "does not match") || !strings.Contains(err.Error(), "Reproduction steps are already present") {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
	}
	if len(provider.matchRequests) != 1 {
		t.Fatalf("%d model requests for the same issue version", len(provider.matchRequests))
	}
	payload := provider.matchRequests[0]
	if payload["model"] != "flow-test" || payload["temperature"] != float64(0) || payload["max_output_tokens"] != float64(2048) {
		t.Fatalf("unexpected structured request settings")
	}
	requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+issue.ID, map[string]any{"description": "Login crashes; no reproduction details."}, http.StatusOK)
	for attempt := 0; attempt < 2; attempt++ {
		prepared, err := srv.checkLoopOutputCall(context.Background(), rec, data, contract, call, "comment")
		if err != nil || len(prepared.record.Outputs) != 1 {
			t.Fatalf("updated issue: %+v, %v", prepared, err)
		}
	}
	if len(provider.matchRequests) != 2 {
		t.Fatalf("%d model requests after issue update", len(provider.matchRequests))
	}
	rec.run.VersionID = "loop_match_v2"
	if _, err := srv.checkLoopOutputCall(context.Background(), rec, data, contract, call, "comment"); err != nil {
		t.Fatal(err)
	}
	if len(provider.matchRequests) != 3 {
		t.Fatalf("loop version did not invalidate the match")
	}
	contract.Outputs[0].Target.Match = "bugs affecting customers"
	if _, err := srv.checkLoopOutputCall(context.Background(), rec, data, contract, call, "comment"); err != nil {
		t.Fatal(err)
	}
	if len(provider.matchRequests) != 4 {
		t.Fatalf("person edit did not invalidate the match")
	}
	provider.matchReplies = []string{`tool:record_issue_match {"reason":"Missing boolean"}`}
	contract.Outputs[0].Target.Match = "bugs needing urgent review"
	if _, err := srv.checkLoopOutputCall(context.Background(), rec, data, contract, call, "comment"); err == nil || !strings.Contains(err.Error(), "Cannot verify") {
		t.Fatalf("invalid answer: %v", err)
	}
	rec.issueMatchChecks = 32
	contract.Outputs[0].Target.Match = "another semantic description"
	if _, err := srv.checkLoopOutputCall(context.Background(), rec, data, contract, call, "comment"); err == nil || !strings.Contains(err.Error(), "32 issue target checks") {
		t.Fatalf("budget: %v", err)
	}
}

func TestLoopStructuralIssueMatchesSkipModel(t *testing.T) {
	provider := &fakeLoopProvider{}
	srv, handler := newReliableLoopServer(t, provider)
	data := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "A bug", "teamId": data.Teams[0].ID, "priority": 2}, http.StatusCreated)
	issue.Labels = []domain.IssueLabel{{ID: "label_customer", Name: "customer"}}
	issue.Assignee = &data.Viewer
	issue.Project = &domain.ProjectSummary{ID: "project_mobile", Name: "Mobile"}
	data.Labels = append(data.Labels, issue.Labels...)
	data.Projects = append(data.Projects, domain.Project{ID: "project_mobile", Name: "Mobile"})
	data.Users = append(data.Users, data.Viewer)
	for _, description := range []string{"priority: High", "issues labeled customer", "assignee: " + data.Viewer.ID, "project: Mobile", "status: " + issue.State.Name, "priority: 2 and label: customer and project: Mobile"} {
		if matches, structural := loopStructuralIssueMatch(description, issue, data); !structural || !matches {
			t.Errorf("%q = %v, %v", description, matches, structural)
		}
	}
	if matches, structural := loopStructuralIssueMatch("priority: Low", issue, data); !structural || matches {
		t.Fatalf("wrong priority matched")
	}
	if _, structural := loopStructuralIssueMatch("issues labeled customer without repro steps", issue, data); structural {
		t.Fatal("semantic suffix was consumed as a structural filter")
	}
	rec := &loopRunRecorder{workspace: data.Workspace.URLKey}
	for _, priority := range []string{"High", "Low"} {
		err := srv.checkLoopIssueMatch(context.Background(), rec, data, &domain.LoopOutputContract{}, domain.LoopContractTarget{Mode: "matching", Match: "priority: " + priority}, loopWriteSubject{parentType: "issue", parentID: issue.ID})
		if (err == nil) != (priority == "High") {
			t.Errorf("priority %s: %v", priority, err)
		}
	}
	if len(provider.matchRequests) != 0 {
		t.Fatal("structural filters called the model")
	}
}

func TestLoopStatusUpdateScopeAndExistingUpdateTarget(t *testing.T) {
	provider := &fakeLoopProvider{}
	srv, handler := newReliableLoopServer(t, provider)
	data := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	home := data.Teams[0]
	other := domain.Team{ID: "team_other", Name: "Other", Key: "OTH"}
	if err := srv.store.MutateWorkspace(context.Background(), data.Workspace.URLKey, "test.teams", other.ID, nil, func(data *domain.Bootstrap) error { data.Teams = append(data.Teams, other); return nil }); err != nil {
		t.Fatal(err)
	}
	foreign := requestJSON[domain.Project](t, handler, http.MethodPost, "/api/projects", map[string]any{"name": "Foreign project", "teamIds": []string{other.ID}}, http.StatusCreated)
	allowed := requestJSON[domain.Project](t, handler, http.MethodPost, "/api/projects", map[string]any{"name": "Home project", "teamIds": []string{home.ID}}, http.StatusCreated)
	initiative := requestJSON[domain.Initiative](t, handler, http.MethodPost, "/api/initiatives", map[string]any{"name": "Foreign initiative", "projectIds": []string{foreign.ID}}, http.StatusCreated)
	initiative = requestJSON[domain.Initiative](t, handler, http.MethodPatch, "/api/initiatives/"+initiative.ID, map[string]any{"projectIds": []string{foreign.ID}}, http.StatusOK)
	data = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	loop := domain.Loop{ID: "loop_scope", TeamID: home.ID, TeamAccess: "selected", AllowChangesOutsideTrigger: true}
	scope := srv.newLoopScope(data, loop, loopTrigger{})
	for _, item := range []struct{ kind, ref string }{{"project", foreign.ID}, {"project", foreign.Name}, {"initiative", initiative.ID}, {"initiative", initiative.Name}} {
		for _, id := range []string{"", "old_update"} {
			raw, _ := json.Marshal(map[string]any{"type": item.kind, item.kind: item.ref, "id": id, "body": "Changed"})
			if err := scope.check(context.Background(), domain.AgentToolCall{Name: "save_status_update", Arguments: raw}); err == nil || !strings.Contains(err.Error(), "outside the loop's team access") {
				t.Errorf("%s %s: %v", item.kind, item.ref, err)
			}
		}
	}
	rec := &loopRunRecorder{workspace: data.Workspace.URLKey}
	for _, kind := range []string{"project", "initiative"} {
		target := domain.LoopContractTarget{ProjectID: allowed.ID}
		ref := foreign.ID
		if kind == "initiative" {
			target = domain.LoopContractTarget{InitiativeID: "different_initiative"}
			ref = initiative.ID
		}
		contract := &domain.LoopOutputContract{Outputs: []domain.LoopContractOutput{{Kind: "statusUpdate", Operation: loopOpCreate, Target: target}}}
		raw, _ := json.Marshal(map[string]any{"type": kind, kind: ref, "id": "old_update", "body": "Changed"})
		if _, err := srv.checkLoopOutputCall(context.Background(), rec, data, contract, domain.AgentToolCall{Name: "save_status_update", Arguments: raw}, "statusUpdate"); err == nil {
			t.Fatalf("editing an older update escaped the named %s", kind)
		}
	}
	// Unresolved named targets must never become unrestricted writes.
	if loopTargetHolds("statusUpdate", domain.LoopContractTarget{Project: "Missing project"}, loopWriteSubject{parentType: "project", parentID: allowed.ID}, "", "") {
		t.Fatal("unresolved project became unrestricted")
	}
}

func TestLoopStalePersonContractExtractsSuggestion(t *testing.T) {
	provider := &fakeLoopProvider{contracts: []string{"tool:record_output_contract " + loopDailyDocumentContract}}
	srv, handler := newReliableLoopServer(t, provider)
	srv.loopContractLive = true
	person := &domain.LoopOutputContract{ReadOnly: true, Outputs: []domain.LoopContractOutput{}}
	loop := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops", map[string]any{"name": "Review", "instructions": "Just reply.", "status": "published", "outputContract": person}, http.StatusCreated)
	changed := requestJSON[domain.Loop](t, handler, http.MethodPatch, "/api/loops/"+loop.ID, map[string]any{"instructions": loopDailyDocumentZH}, http.StatusOK)
	if !changed.OutputContract.Stale || !changed.OutputContract.ReadOnly {
		t.Fatal("person contract was not retained as stale")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		changed = requestJSON[domain.Loop](t, handler, http.MethodGet, "/api/loops/"+loop.ID, nil, http.StatusOK)
		if changed.SuggestedOutputContract != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no background suggestion")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if changed.SuggestedOutputContract.ReadOnly || !changed.OutputContract.ReadOnly || changed.OutputContract.Source != loopContractPerson {
		t.Fatalf("suggestion replaced the person's outputs")
	}
	if current := srv.loopContractFor(context.Background(), "test-workspace", changed); !current.ReadOnly || current.Source != loopContractPerson {
		t.Fatalf("runs did not keep the person's contract")
	}
	requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/output-contract", nil, http.StatusOK)
	provider.mu.Lock()
	count := len(provider.extractions)
	provider.mu.Unlock()
	if count != 1 {
		t.Fatalf("%d extractions, want one cached suggestion", count)
	}
	accepted := requestJSON[domain.Loop](t, handler, http.MethodPatch, "/api/loops/"+loop.ID, map[string]any{"outputContract": changed.SuggestedOutputContract}, http.StatusOK)
	if accepted.OutputContract.Stale || accepted.OutputContract.ReadOnly || accepted.SuggestedOutputContract != nil {
		t.Fatalf("review was not accepted: %+v", accepted)
	}
	srv.storeLoopContract("test-workspace", loop.ID, loopContractHashOf(changed), changed.SuggestedOutputContract)
	again := requestJSON[domain.Loop](t, handler, http.MethodGet, "/api/loops/"+loop.ID, nil, http.StatusOK)
	if again.SuggestedOutputContract != nil || again.OutputContract.Source != loopContractPerson {
		t.Fatal("late extraction undid the review")
	}
}

func TestLoopDocumentContentSearch(t *testing.T) {
	_, handler := newReliableLoopServer(t, &fakeLoopProvider{})
	data := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	team := data.Teams[0].ID
	first := loopTestDocument(t, handler, map[string]any{"title": "Operations notes", "content": "# Releases\nThe canary deployment checklist is ready.", "teamIds": []string{team}})
	second := loopTestDocument(t, handler, map[string]any{"title": "Another log", "content": "The canary deployment needs signoff.", "teamIds": []string{team}})
	loopTestDocument(t, handler, map[string]any{"title": "Unrelated", "content": "Nothing matches here.", "teamIds": []string{team}})
	query := "/api/documents?first=1&orderBy=updatedAt&teamId=" + team + "&query=canary%20deployment"
	page := requestJSON[connection[domain.Document]](t, handler, http.MethodGet, query, nil, http.StatusOK)
	if len(page.Nodes) != 1 || page.Nodes[0].ID != second.ID || !page.PageInfo.HasNextPage || !strings.Contains(page.Nodes[0].SearchSnippet, "canary deployment") || len(page.Nodes[0].MatchedTerms) != 2 {
		t.Fatalf("content search: %+v", page)
	}
	next := requestJSON[connection[domain.Document]](t, handler, http.MethodGet, query+"&after="+url.QueryEscape(*page.PageInfo.EndCursor), nil, http.StatusOK)
	if len(next.Nodes) != 1 || next.Nodes[0].ID != first.ID || next.PageInfo.HasNextPage {
		t.Fatalf("content search next page: %+v", next)
	}
}
