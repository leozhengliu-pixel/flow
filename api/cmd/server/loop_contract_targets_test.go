package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// Output contract targets of issues, comments and status updates, title
// placeholders the run fills, and existing documents the instructions do not
// name.

func TestNormalizeLoopContractTargets(t *testing.T) {
	for _, item := range []struct {
		contract string
		problem  string
	}{
		{`{"outputs":[{"kind":"issue","operation":"create","target":{"mode":"new","titleTemplate":"Error: {message}"}}]}`, "target.placeholders"},
		{`{"outputs":[{"kind":"document","operation":"create","target":{"mode":"new","titleTemplate":"反馈-{客户名}-{date}","placeholders":[{"name":"topic","source":"x"}]}}]}`, "{客户名}"},
	} {
		var contract domain.LoopOutputContract
		if err := json.Unmarshal([]byte(item.contract), &contract); err != nil {
			t.Fatal(err)
		}
		if err := normalizeLoopContract(&contract); err == nil || !strings.Contains(err.Error(), item.problem) {
			t.Errorf("%s: %v, want %q", item.contract, err, item.problem)
		}
	}
	var contract domain.LoopOutputContract
	raw := `{"outputs":[
		{"kind":"issue","operation":"create","target":{"mode":"existing","titleTemplate":"Error: {message} {date}","placeholders":[{"name":"{message}","source":"the error's message"},{"name":"date"},{"name":"unused"}],"team":"Mobile","match":"x","title":"y"}},
		{"kind":"comment","operation":"create","target":{"mode":"new","titleTemplate":"x","project":"Mobile App"}},
		{"kind":"statusUpdate","operation":"create","target":{"mode":"matching","project":"Mobile App","initiative":"Growth","match":"x"}},
		{"kind":"document","operation":"append","target":{"mode":"existing","title":"Release log","ambiguous":true}},
		{"kind":"document","operation":"update","target":{"mode":"any","ambiguous":true,"description":"the team doc"}},
		{"kind":"issue","operation":"update","target":{"mode":"matching","match":"bugs without repro steps","team":"Mobile"}}]}`
	if err := json.Unmarshal([]byte(raw), &contract); err != nil {
		t.Fatal(err)
	}
	if err := normalizeLoopContract(&contract); err != nil {
		t.Fatal(err)
	}
	issue, comment, update, named, vague, matching := contract.Outputs[0].Target, contract.Outputs[1].Target, contract.Outputs[2].Target, contract.Outputs[3].Target, contract.Outputs[4].Target, contract.Outputs[5].Target
	if issue.Mode != "new" || len(issue.Placeholders) != 1 || issue.Placeholders[0].Name != "message" || issue.Match != "" || issue.Title != "" || issue.Team != "Mobile" {
		t.Errorf("new issue target = %+v", issue)
	}
	if comment.Mode != "any" || comment.TitleTemplate != "" || comment.Project != "Mobile App" {
		t.Errorf("comment target = %+v", comment)
	}
	if update.Mode != "any" || update.Initiative != "" || update.Match != "" || update.Project != "Mobile App" {
		t.Errorf("status update target = %+v", update)
	}
	if named.Ambiguous || !vague.Ambiguous || vague.Mode != "existing" {
		t.Errorf("document targets = %+v / %+v", named, vague)
	}
	if matching.Mode != "matching" || matching.Match != "bugs without repro steps" {
		t.Errorf("matching target = %+v", matching)
	}
	// The model's existing document without a title is ambiguous, whatever it says.
	parsed, err := parseLoopContractArguments(`{"readOnly":false,"outputs":[{"kind":"document","operation":"append","required":true,"target":{"mode":"any","description":"the team doc","ambiguous":false}}],"always":false,"skipConditions":[]}`)
	if err != nil || !parsed.Outputs[0].Target.Ambiguous || parsed.Outputs[0].Target.Mode != "existing" {
		t.Fatalf("parsed = %+v, %v", parsed, err)
	}
}

func TestLoopFillTitle(t *testing.T) {
	target := domain.LoopContractTarget{Placeholders: []domain.LoopContractPlaceholder{{Name: "topic", Source: "the error's subject"}}}
	rendered := "Bug: {topic} (2026-10-28)"
	if title, err := loopFillTitle(rendered, target, map[string]any{"title": "x", loopTitleFieldsArg: map[string]any{"topic": " Login\n500 "}}, "issue"); err != nil || title != "Bug: Login 500 (2026-10-28)" {
		t.Fatalf("filled = %q, %v", title, err)
	}
	// A title that fits the template is accepted without titleFields.
	if title, err := loopFillTitle(rendered, target, map[string]any{"title": "Bug: Checkout (2026-10-28)"}, "issue"); err != nil || title != "Bug: Checkout (2026-10-28)" {
		t.Fatalf("fitting title = %q, %v", title, err)
	}
	for _, args := range []map[string]any{
		{"title": "Bug: {topic} (2026-10-28)"},
		{"title": "Something else"},
		{loopTitleFieldsArg: map[string]any{"topic": "{still}"}},
		{loopTitleFieldsArg: map[string]any{"topic": "  "}},
	} {
		if title, err := loopFillTitle(rendered, target, args, "issue"); err == nil || !strings.Contains(err.Error(), `"topic": "<the error's subject>"`) {
			t.Errorf("%v: %q, %v", args, title, err)
		}
	}
	if title, err := loopFillTitle("Daily 2026-10-28", domain.LoopContractTarget{}, map[string]any{"title": "whatever"}, "document"); err != nil || title != "Daily 2026-10-28" {
		t.Fatalf("fixed title = %q, %v", title, err)
	}
	if got := renderLoopTitleNames("{team} weekly {project}", "Mobile", "Growth"); got != "Mobile weekly Growth" {
		t.Fatalf("names = %q", got)
	}
}

// save_document and save_issue offer titleFields only in a run whose
// contract has model-filled placeholders for them.
func TestLoopTitleFieldsTools(t *testing.T) {
	contract := &domain.LoopOutputContract{Outputs: []domain.LoopContractOutput{{Kind: "issue", Operation: loopOpCreate, Target: domain.LoopContractTarget{TitleTemplate: "Bug: {topic}", RenderedTitle: "Bug: {topic}"}}}}
	rewrite := loopTitleFieldsTools(contract)
	issue := rewrite(agentProviderTool{Name: "mcp__flow.save_issue", Parameters: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}}}`)})
	document := rewrite(agentProviderTool{Name: "save_document", Parameters: json.RawMessage(`{"type":"object","properties":{}}`)})
	if !strings.Contains(string(issue.Parameters), loopTitleFieldsArg) || strings.Contains(string(document.Parameters), loopTitleFieldsArg) {
		t.Fatalf("issue %s document %s", issue.Parameters, document.Parameters)
	}
	if stripped := loopWithoutTitleFields(json.RawMessage(`{"title":"x","titleFields":{"topic":"y"}}`)); string(stripped) != `{"title":"x"}` {
		t.Fatalf("stripped = %s", stripped)
	}
}

// A comment loop on the triggering issue: a comment on another issue is
// refused and does not count; the one on the triggering issue does.
func TestLoopCommentOnTriggeringIssue(t *testing.T) {
	provider := &fakeLoopProvider{}
	_, handler := newReliableLoopServer(t, provider)
	team := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK).Teams[0]
	trigger := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Crash on login", "teamId": team.ID}, http.StatusCreated)
	other := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Unrelated", "teamId": team.ID}, http.StatusCreated)
	instructions := "When a bug is reported, comment on it asking for repro steps."
	setLoopTestContract(instructions, `{"readOnly":false,"outputs":[{"kind":"comment","operation":"create","required":true,"target":{"mode":"triggering"},"cardinality":"onePerRun","evidence":"comment on it"}],"always":false,"skipConditions":[]}`)
	loop := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops", map[string]any{"name": "Repro steps", "instructions": instructions, "status": "published", "triggerType": "issue", "triggerConfig": map[string]any{"event": "created"}, "allowChangesOutsideTrigger": true}, http.StatusCreated)
	provider.script(
		`tool:save_comment {"issueId":"`+other.Identifier+`","body":"Please add repro steps."}`,
		`tool:save_comment {"issueId":"`+trigger.Identifier+`","body":"Please add repro steps."}`,
		`tool:finish_run {"status":"done","summary":"Asked for repro steps."}`,
	)
	started := requestJSON[domain.LoopRun](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs", map[string]any{"entityId": trigger.Identifier}, http.StatusAccepted)
	run := waitForRun(t, handler, loop.ID, started.ID)
	refused := loopRunCall(run, "save_comment", 0)
	if refused.Status != "blocked" || !strings.HasPrefix(refused.Error, loopContractRefusalPrefix) || !strings.Contains(refused.Error, "the issue that triggered the run ("+trigger.Identifier+")") {
		t.Fatalf("comment on another issue = %s %q", refused.Status, refused.Error)
	}
	posted := loopRunCall(run, "save_comment", 1)
	if posted.Status != "completed" || posted.Output == nil || !slices.Equal(posted.Output.Outputs, []int{0}) || posted.Output.ParentID != trigger.ID || posted.Output.ID == "" {
		t.Fatalf("comment on the triggering issue = %s %+v", posted.Status, posted.Output)
	}
	if run.Status != "completed" || run.Produced["comment"] != 1 {
		t.Fatalf("run = %s %s %q produced %v", run.Status, run.FailureReason, run.Error, run.Produced)
	}
	if prompt := loopRunPrompt(t, provider, 0); !strings.Contains(prompt, "on the item that triggered the run only") {
		t.Fatalf("prompt lacks the target:\n%s", prompt)
	}
	// Only the off-target comment: nothing counts.
	provider.script(`tool:save_comment {"issueId":"`+other.Identifier+`","body":"x"}`, `tool:finish_run {"status":"done","summary":"Commented."}`)
	next := requestJSON[domain.LoopRun](t, handler, http.MethodPost, "/api/loops/"+loop.ID+"/runs", map[string]any{"entityId": trigger.Identifier}, http.StatusAccepted)
	run = waitForRun(t, handler, loop.ID, next.ID)
	if run.Status != "needs_review" || run.Produced["comment"] != 0 || !strings.Contains(run.Error, "a comment on the item that triggered the run") {
		t.Fatalf("off-target run = %s %s %q produced %v", run.Status, run.FailureReason, run.Error, run.Produced)
	}
}

// Status updates on a named project or initiative go there and nowhere else.
func TestLoopStatusUpdateOnNamedProjectAndInitiative(t *testing.T) {
	provider := &fakeLoopProvider{}
	_, handler := newReliableLoopServer(t, provider)
	team := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK).Teams[0]
	mobile := requestJSON[domain.Project](t, handler, http.MethodPost, "/api/projects", map[string]any{"name": "Mobile App", "teamIds": []string{team.ID}}, http.StatusCreated)
	web := requestJSON[domain.Project](t, handler, http.MethodPost, "/api/projects", map[string]any{"name": "Web App", "teamIds": []string{team.ID}}, http.StatusCreated)
	instructions := "Every Friday post a status update on the Mobile App project."
	setLoopTestContract(instructions, `{"readOnly":false,"outputs":[{"kind":"statusUpdate","operation":"create","required":true,"target":{"mode":"existing","project":"Mobile App"},"cardinality":"onePerRun"}],"always":false,"skipConditions":[]}`)
	provider.script(
		`tool:save_status_update {"type":"project","project":"`+web.ID+`","body":"On track.","health":"onTrack"}`,
		`tool:save_status_update {"type":"project","project":"Mobile App","body":"On track.","health":"onTrack"}`,
		`tool:finish_run {"status":"done","summary":"Posted."}`,
	)
	loop, started := startTestRun(t, handler, "Mobile update", instructions, nil)
	run := waitForRun(t, handler, loop.ID, started.ID)
	if target := run.OutputContract.Outputs[0].Target; target.ProjectID != mobile.ID {
		t.Fatalf("resolved target = %+v", target)
	}
	if call := loopRunCall(run, "save_status_update", 0); call.Status != "blocked" || !strings.Contains(call.Error, `the project "Mobile App"`) {
		t.Fatalf("update on another project = %s %q", call.Status, call.Error)
	}
	if call := loopRunCall(run, "save_status_update", 1); call.Status != "completed" || call.Output == nil || call.Output.ParentID != mobile.ID {
		t.Fatalf("update on the project = %s %+v", call.Status, call.Output)
	}
	if run.Status != "completed" || run.Produced["statusUpdate"] != 1 {
		t.Fatalf("run = %s %q produced %v", run.Status, run.Error, run.Produced)
	}

	// A run's tools see an initiative through its projects in the loop's teams.
	initiative := requestJSON[domain.Initiative](t, handler, http.MethodPost, "/api/initiatives", map[string]any{"name": "Growth push"}, http.StatusCreated)
	requestJSON[domain.Initiative](t, handler, http.MethodPatch, "/api/initiatives/"+initiative.ID, map[string]any{"projectIds": []string{mobile.ID}}, http.StatusOK)
	onInitiative := "Post a weekly update on the Growth push initiative."
	setLoopTestContract(onInitiative, `{"readOnly":false,"outputs":[{"kind":"statusUpdate","operation":"create","required":true,"target":{"mode":"existing","initiative":"Growth push"}}],"always":false,"skipConditions":[]}`)
	provider.script(
		`tool:save_status_update {"type":"project","project":"Mobile App","body":"x"}`,
		`tool:save_status_update {"type":"initiative","initiative":"`+initiative.ID+`","body":"Growing."}`,
		`tool:finish_run {"status":"done","summary":"Posted."}`,
	)
	loop, started = startTestRun(t, handler, "Growth update", onInitiative, nil)
	run = waitForRun(t, handler, loop.ID, started.ID)
	if call := loopRunCall(run, "save_status_update", 0); call.Status != "blocked" || !strings.Contains(call.Error, `the initiative "Growth push"`) {
		t.Fatalf("project update in an initiative loop = %s %q", call.Status, call.Error)
	}
	// The contract lets the update on the initiative through, and so do the
	// run's tools: the initiative is linked to a project in the loop's team.
	if call := loopRunCall(run, "save_status_update", 1); call.Status != "completed" || call.Error != "" {
		t.Fatalf("update on the initiative = %s %q", call.Status, call.Error)
	}
	target := run.OutputContract.Outputs[0].Target
	if target.InitiativeID != initiative.ID || !loopTargetHolds("statusUpdate", target, loopWriteSubject{parentType: "initiative", parentID: initiative.ID}, "", "") || loopTargetHolds("statusUpdate", target, loopWriteSubject{parentType: "project", parentID: mobile.ID}, "", "") {
		t.Fatalf("initiative target = %+v", target)
	}
}

// Issue updates limited to a described set in a team and project; new
// issues in the contract's project with its title template, the run filling
// the placeholder through titleFields.
func TestLoopIssueTargetsAndTitlePlaceholders(t *testing.T) {
	provider := &fakeLoopProvider{}
	_, handler := newReliableLoopServer(t, provider)
	team := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK).Teams[0]
	reliability := requestJSON[domain.Project](t, handler, http.MethodPost, "/api/projects", map[string]any{"name": "Reliability", "teamIds": []string{team.ID}}, http.StatusCreated)
	inSet := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Timeout", "teamId": team.ID, "projectId": reliability.ID}, http.StatusCreated)
	outside := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Copy change", "teamId": team.ID}, http.StatusCreated)
	instructions := "Raise the priority of reliability bugs; for each new error open an issue titled Error: <message>."
	setLoopTestContract(instructions, `{"readOnly":false,"outputs":[
		{"kind":"issue","operation":"update","required":true,"target":{"mode":"matching","match":"reliability bugs","project":"Reliability"}},
		{"kind":"issue","operation":"create","required":false,"target":{"mode":"new","project":"Reliability","titleTemplate":"Error: {message} ({date})","placeholders":[{"name":"message","source":"the error's message"}]},"cardinality":"perItem","conditions":["for each new error"]}],"always":false,"skipConditions":[]}`)
	provider.script(
		`tool:save_issue {"id":"`+outside.Identifier+`","priority":1}`,
		`tool:save_issue {"id":"`+inSet.Identifier+`","priority":1}`,
		`tool:save_issue {"title":"Error: {message}","team":"`+team.ID+`","description":"x"}`,
		`tool:save_issue {"title":"anything","team":"`+team.ID+`","description":"Stack trace","titleFields":{"message":"Login 500"}}`,
		`tool:finish_run {"status":"done","summary":"Done."}`,
	)
	loop, started := startTestRun(t, handler, "Reliability", instructions, nil)
	run := waitForRun(t, handler, loop.ID, started.ID)
	if call := loopRunCall(run, "save_issue", 0); call.Status != "blocked" || !strings.Contains(call.Error, `issues matching "reliability bugs" in the project "Reliability"`) {
		t.Fatalf("update outside the set = %s %q", call.Status, call.Error)
	}
	if call := loopRunCall(run, "save_issue", 1); call.Status != "completed" || call.Output == nil || !slices.Equal(call.Output.Outputs, []int{0}) {
		t.Fatalf("update in the set = %s %+v", call.Status, call.Output)
	}
	if call := loopRunCall(run, "save_issue", 2); call.Status != "blocked" || !strings.Contains(call.Error, `titleFields {"message": "<the error's message>"}`) {
		t.Fatalf("create without titleFields = %s %q", call.Status, call.Error)
	}
	created := loopRunCall(run, "save_issue", 3)
	want := "Error: Login 500 (" + time.Now().UTC().Format("2006-01-02") + ")"
	if created.Status != "completed" || created.Output == nil || created.Output.Title != want || created.Output.ID == "" {
		t.Fatalf("create = %s %q %+v", created.Status, created.Error, created.Output)
	}
	issue := requestJSON[domain.Issue](t, handler, http.MethodGet, "/api/issue-records/"+created.Output.ID, nil, http.StatusOK)
	if issue.Title != want || issue.Project == nil || issue.Project.ID != reliability.ID {
		t.Fatalf("created issue = %q project %+v", issue.Title, issue.Project)
	}
	if run.Status != "completed" || run.Produced["issue"] != 1 {
		t.Fatalf("run = %s %s %q produced %v", run.Status, run.FailureReason, run.Error, run.Produced)
	}
	prompt := loopRunPrompt(t, provider, 0)
	for _, part := range []string{`titled exactly "Error: {message} (`, `pass titleFields {"message": "<the error's message>"} to save_issue`, `issues matching "reliability bugs" in the project "Reliability"`} {
		if !strings.Contains(prompt, part) {
			t.Fatalf("prompt lacks %q:\n%s", part, prompt)
		}
	}
	if content := string(created.Args); strings.Contains(content, loopTitleFieldsArg) {
		t.Fatalf("titleFields reached the tool: %s", content)
	}
}

// The loop editor's document picker searches the documents connection: by
// title, in a team, most recently updated first, a page at a time.
func TestLoopDocumentPickerSearch(t *testing.T) {
	_, handler := newReliableLoopServer(t, &fakeLoopProvider{})
	teams := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK).Teams
	first := loopTestDocument(t, handler, map[string]any{"title": "Release log", "content": "a", "teamIds": []string{teams[0].ID}})
	second := loopTestDocument(t, handler, map[string]any{"title": "release LOG archive", "content": "b", "teamIds": []string{teams[0].ID}})
	loopTestDocument(t, handler, map[string]any{"title": "Roadmap", "content": "c", "teamIds": []string{teams[0].ID}})
	query := "/api/documents?first=1&orderBy=updatedAt&teamId=" + teams[0].ID + "&filter=" + url.QueryEscape(`{"title":{"containsIgnoreCase":"release log"}}`)
	page := requestJSON[connection[domain.Document]](t, handler, http.MethodGet, query, nil, http.StatusOK)
	if len(page.Nodes) != 1 || page.Nodes[0].ID != second.ID || !page.PageInfo.HasNextPage || page.PageInfo.EndCursor == nil {
		t.Fatalf("first page = %+v", page)
	}
	next := requestJSON[connection[domain.Document]](t, handler, http.MethodGet, query+"&after="+url.QueryEscape(*page.PageInfo.EndCursor), nil, http.StatusOK)
	if len(next.Nodes) != 1 || next.Nodes[0].ID != first.ID || next.PageInfo.HasNextPage {
		t.Fatalf("second page = %+v", next)
	}
	if len(teams) > 1 {
		other := requestJSON[connection[domain.Document]](t, handler, http.MethodGet, "/api/documents?first=20&orderBy=updatedAt&teamId="+teams[1].ID+"&filter="+url.QueryEscape(`{"title":{"containsIgnoreCase":"release"}}`), nil, http.StatusOK)
		if len(other.Nodes) != 0 {
			t.Fatalf("other team = %+v", other.Nodes)
		}
	}
}

// "Append to the team doc" without a title: until a person picks the
// document, the run may read but writes to no existing document and goes to
// needs_review asking for the target; once picked, the append completes.
func TestLoopAmbiguousDocumentTarget(t *testing.T) {
	provider := &fakeLoopProvider{}
	_, handler := newReliableLoopServer(t, provider)
	document := loopTestDocument(t, handler, map[string]any{"title": "Team log", "content": "Existing entry"})
	instructions := "把本周总结追加到团队文档。"
	setLoopTestContract(instructions, `{"readOnly":false,"outputs":[{"kind":"document","operation":"append","required":true,"target":{"mode":"existing","ambiguous":true,"description":"the team document"},"cardinality":"onePerRun","evidence":"追加到团队文档"}],"always":false,"skipConditions":[]}`)
	provider.script(
		`tool:get_document {"id":"`+document.ID+`"}`,
		loopAppendCall(document.ID, "\n\n## 本周\n- 总结"),
		`tool:save_document {"title":"本周总结","content":"x"}`,
		`tool:finish_run {"status":"done","summary":"已追加。"}`,
	)
	loop, started := startTestRun(t, handler, "周报", instructions, nil)
	run := waitForRun(t, handler, loop.ID, started.ID)
	if call := loopRunCall(run, "get_document", 0); call.Status != "completed" {
		t.Fatalf("read = %s %q", call.Status, call.Error)
	}
	for nth := range 2 {
		if call := loopRunCall(run, "save_document", nth); call.Status != "blocked" || !strings.HasPrefix(call.Error, loopChooseTargetPrefix) {
			t.Fatalf("write %d with no chosen document = %s %q", nth, call.Status, call.Error)
		}
	}
	if run.Status != "needs_review" || run.FailureReason != "needs_target" || run.Error != loopChooseTargetNotice {
		t.Fatalf("run = %s %s %q", run.Status, run.FailureReason, run.Error)
	}
	if content := loopTestDocumentContent(t, handler, document.ID); content != "Existing entry" {
		t.Fatalf("document = %q", content)
	}
	if prompt := loopRunPrompt(t, provider, 0); !strings.Contains(prompt, "has not been chosen in the loop's outputs yet") || strings.Contains(prompt, "expected to produce output") {
		t.Fatalf("prompt:\n%s", prompt)
	}
	if !webTranslationKeys(t)[loopChooseTargetNotice] {
		t.Errorf("missing translation of %q", loopChooseTargetNotice)
	}

	// The owner picks the document: the append is the loop's output.
	contract := *run.OutputContract
	contract.Outputs = slices.Clone(contract.Outputs)
	contract.Outputs[0].Target.DocumentID = document.ID
	contract.Source = loopContractPerson
	updated := requestJSON[domain.Loop](t, handler, http.MethodPatch, "/api/loops/"+loop.ID, map[string]any{"outputContract": contract}, http.StatusOK)
	if target := updated.OutputContract.Outputs[0].Target; target.Ambiguous || target.DocumentID != document.ID {
		t.Fatalf("picked target = %+v", target)
	}
	provider.script(loopAppendCall(document.ID, "\n\n## 本周\n- 总结"), `tool:finish_run {"status":"done","summary":"已追加。"}`)
	run = waitForRun(t, handler, loop.ID, startNextRun(t, handler, loop.ID).ID)
	if run.Status != "completed" || loopTestDocumentContent(t, handler, document.ID) != "Existing entry\n\n## 本周\n- 总结" {
		t.Fatalf("picked run = %s %s %q", run.Status, run.FailureReason, run.Error)
	}
}
