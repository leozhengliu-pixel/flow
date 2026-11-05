package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"

	"flow/api/internal/domain"
)

// chatOutboundFixture records the committed domain events and runs the
// chat consumer over them like the scheduler does.
func (f *chatWorkflowFixture) followEvents(t *testing.T) {
	t.Helper()
	f.s.store.SetWebhookSink(func(string, domain.DomainEvent) {})
	if err := f.s.store.InitializeChatDomainCursor(t.Context(), f.record.WorkspaceKey); err != nil {
		t.Fatal(err)
	}
}

// pendingEvents are the committed events the chat consumer has not read.
func (f *chatWorkflowFixture) pendingEvents(t *testing.T) []domain.DomainEvent {
	t.Helper()
	records, err := f.s.store.ChatDomainEventPage(t.Context(), f.record.WorkspaceKey, 200)
	if err != nil {
		t.Fatal(err)
	}
	events := []domain.DomainEvent{}
	for _, record := range records {
		events = append(events, record.Event)
	}
	return events
}

// syncEvents queues the committed events and runs the notification jobs;
// it returns how many jobs ran.
func (f *chatWorkflowFixture) syncEvents(t *testing.T) int {
	t.Helper()
	if err := f.s.queueCommittedChatEvents(t.Context(), f.record.WorkspaceKey); err != nil {
		t.Fatal(err)
	}
	jobs, err := f.s.store.ClaimChatJobs(t.Context(), f.record.WorkspaceKey, "notification", 50, time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range jobs {
		if err := f.s.processChatJob(t.Context(), job); err != nil {
			t.Fatalf("notification job: %v", err)
		}
		if err := f.s.store.FinishChatJob(t.Context(), job, "completed", time.Time{}, ""); err != nil {
			t.Fatal(err)
		}
	}
	return len(jobs)
}

// mcpTool runs a Flow MCP write tool as the fixture's member, the way the
// MCP endpoint and the agent tools call it.
func (f *chatWorkflowFixture) mcpTool(t *testing.T, name string, args map[string]any) map[string]any {
	t.Helper()
	viewer := f.s.store.Bootstrap().Viewer
	actor := mcpActor{WorkspaceKey: f.record.WorkspaceKey, User: viewer, APIKey: domain.APIKey{Scopes: []string{"read", "write"}}}
	data, err := f.s.mcpWorkspaceData(t.Context(), actor)
	if err == nil {
		err = f.s.hydrateMCPIssueArguments(t.Context(), actor, &data, args)
	}
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.s.callFlowWriteTool(t.Context(), actor, data, name, args)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	raw, _ := json.Marshal(result)
	var object map[string]any
	_ = json.Unmarshal(raw, &object)
	return object
}

func chatEventPayload(event domain.DomainEvent) map[string]any {
	var payload map[string]any
	_ = json.Unmarshal(event.Payload, &payload)
	return payload
}

// Every comment write path records the canonical event on the issue with
// the comment in the payload, so the group sync hears it: MCP (and the agent
// tools on it) create, reply, edit and delete, and the REST route. Each
// comment reaches the issue's thread exactly once, and a later backfill
// posts nothing again.
func TestChatCommentWritePathsSyncOnceToTheIssueThread(t *testing.T) {
	f := newChatWorkflowFixture(t)
	platform := f.threadPlatform(t)
	issue := f.s.store.Bootstrap().Issues[0]
	f.notify(t, "issue.created", issue.ID, "outbound_thread")
	root := f.thread(t, issue.ID)
	f.followEvents(t)

	expectEvent := func(want, commentID string) domain.DomainEvent {
		t.Helper()
		events := f.pendingEvents(t)
		for _, event := range events {
			if event.Type == want && event.AggregateID == issue.ID && chatEventPayload(event)["commentId"] == commentID {
				return event
			}
		}
		t.Fatalf("no %s event on the issue naming comment %s: %+v", want, commentID, events)
		return domain.DomainEvent{}
	}
	posted := func(marker string) []chatThreadCall {
		sent := []chatThreadCall{}
		for _, call := range chatCallsMention(platform.calls, marker) {
			if call.method == http.MethodPost {
				sent = append(sent, call)
			}
		}
		return sent
	}

	first := f.mcpTool(t, "save_comment", map[string]any{"issueId": issue.ID, "body": "mcp-marker-one 第一条"})
	expectEvent("comment.created", first["id"].(string))
	if jobs := f.syncEvents(t); jobs != 1 {
		t.Fatalf("MCP comment queued %d sync jobs, want 1", jobs)
	}
	second := f.mcpTool(t, "save_comment", map[string]any{"issueId": issue.Identifier, "body": "mcp-marker-two"})
	expectEvent("comment.created", second["id"].(string))
	f.syncEvents(t)
	for _, marker := range []string{"mcp-marker-one", "mcp-marker-two"} {
		if calls := posted(marker); len(calls) != 1 || chatThreadReplyTo(calls[0]) != root.RootMessageID {
			t.Fatalf("%s posted %d times, want once in the thread: %#v", marker, len(calls), calls)
		}
	}

	// A reply targets the comment; the event still names the issue.
	reply := f.mcpTool(t, "save_comment", map[string]any{"parentId": first["id"], "body": "mcp-reply-marker"})
	if payload := chatEventPayload(expectEvent("comment.created", reply["id"].(string))); payload["parentId"] != first["id"] {
		t.Fatalf("reply event does not name its parent: %v", payload)
	}
	f.syncEvents(t)
	if calls := posted("mcp-reply-marker"); len(calls) != 1 || !strings.Contains(calls[0].body["content"].(string), chatT(chatLanguageChinese, "replied", "")) {
		t.Fatalf("reply not posted once as a reply: %#v", calls)
	}

	// An edit targets the comment; the posted message is updated in place.
	patches := len(f.patched)
	f.mcpTool(t, "save_comment", map[string]any{"id": first["id"], "body": "mcp-marker-one edited"})
	expectEvent("comment.updated", first["id"].(string))
	f.syncEvents(t)
	if len(f.patched) != patches+1 || !strings.Contains(f.patched[patches]["content"].(string), "mcp-marker-one edited") || len(posted("mcp-marker-one edited")) != 0 {
		t.Fatalf("MCP edit not reflected on the posted message: %#v", f.patched[patches:])
	}

	// The REST route afterwards posts its own comment and nothing again.
	before := len(platform.calls)
	rest := f.addComment(t, issue.ID, "rest-marker")
	expectEvent("comment.created", rest.ID)
	f.syncEvents(t)
	if calls := platform.since(before); len(calls) != 1 || len(chatCallsMention(calls, "rest-marker")) != 1 {
		t.Fatalf("REST comment sync posted %#v, want only the REST comment", calls)
	}

	// Deleting through MCP retires the posted message.
	patches = len(f.patched)
	f.mcpTool(t, "delete_comment", map[string]any{"id": second["id"]})
	expectEvent("comment.deleted", second["id"].(string))
	f.syncEvents(t)
	if len(f.patched) != patches+1 || !strings.Contains(f.patched[patches]["content"].(string), chatT(chatLanguageChinese, "comment_deleted")) {
		t.Fatalf("MCP deletion not reflected: %#v", f.patched[patches:])
	}

	// Replaying every comment event posts nothing.
	before = len(platform.calls)
	f.deliver(t, domain.DomainEvent{ID: "evt_backfill_replay", Type: "comment.created", AggregateID: issue.ID})
	if len(platform.since(before)) != 0 {
		t.Fatalf("backfill reposted comments: %#v", platform.since(before))
	}
}

// Comments the group never heard of (written before the write paths recorded
// the canonical events) are posted by the next sync, each once, and are not
// posted again by later syncs.
func TestChatCommentBackfillPostsMissedCommentsOnce(t *testing.T) {
	f := newChatWorkflowFixture(t)
	platform := f.threadPlatform(t)
	issue := f.s.store.Bootstrap().Issues[0]
	f.notify(t, "issue.created", issue.ID, "backfill_thread")
	root := f.thread(t, issue.ID)
	f.followEvents(t)
	for _, marker := range []string{"silent-one", "silent-two"} {
		comment := newMCPComment(f.s.store.Bootstrap().Viewer, marker, nil, nil)
		comment.CreatedAt = root.SyncFrom.Add(time.Second)
		// An event name no consumer reads, as MCP used to record.
		if err := f.s.store.MutateWorkspace(t.Context(), f.record.WorkspaceKey, "comment.create", comment.ID, nil, func(data *domain.Bootstrap) error {
			data.Comments[issue.ID] = append(data.Comments[issue.ID], comment)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if jobs := f.syncEvents(t); jobs != 0 || len(chatCallsMention(platform.calls, "silent-")) != 0 {
		t.Fatalf("unrecognised events synced: %d jobs", jobs)
	}
	f.addComment(t, issue.ID, "loud-three")
	f.syncEvents(t)
	f.addComment(t, issue.ID, "loud-four")
	f.syncEvents(t)
	for _, marker := range []string{"silent-one", "silent-two", "loud-three", "loud-four"} {
		if calls := chatCallsMention(platform.calls, marker); len(calls) != 1 {
			t.Fatalf("%s posted %d times, want once", marker, len(calls))
		}
	}
}

// MCP comment writes on other resources record their resource's REST event.
func TestMCPCommentEventsNameTheResource(t *testing.T) {
	parent := "comment_root"
	data := domain.Bootstrap{
		Issues:            []domain.Issue{{ID: "issue_1", Identifier: "ENG-1"}},
		Projects:          []domain.Project{{ID: "project_1", Comments: []domain.Comment{{ID: "project_comment"}}}},
		Initiatives:       []domain.Initiative{{ID: "initiative_1", Comments: []domain.Comment{{ID: "initiative_comment"}}}},
		Documents:         []domain.Document{{ID: "document_1"}},
		Comments:          map[string][]domain.Comment{"issue_1": {{ID: parent}}, "document_1": {{ID: "document_comment"}}},
		ProjectUpdates:    map[string][]domain.ProjectUpdate{"project_1": {{ID: "update_1", Comments: []domain.Comment{{ID: "update_comment"}}}}},
		InitiativeUpdates: map[string][]domain.InitiativeUpdate{"initiative_1": {{ID: "iupdate_1"}}},
	}
	for _, tc := range []struct{ target, operation, event, aggregate string }{
		{"issue_1", "create", "comment.created", "issue_1"},
		{"ENG-1", "create", "comment.created", "issue_1"},
		{parent, "reply", "comment.created", "issue_1"},
		{parent, "update", "comment.updated", "issue_1"},
		{parent, "delete", "comment.deleted", "issue_1"},
		{"project_1", "create", "project.commented", "project_1"},
		{"project_comment", "update", "project.comment_updated", "project_1"},
		{"project_comment", "delete", "project.comment_deleted", "project_1"},
		{"initiative_comment", "reply", "initiative.commented", "initiative_1"},
		{"document_1", "create", "document.comment_created", "document_1"},
		{"document_comment", "update", "document.comment_updated", "document_1"},
		{"update_1", "create", "project.update_commented", "project_1"},
		{"update_comment", "delete", "project.update_comment_deleted", "project_1"},
		{"iupdate_1", "create", "initiative.update_commented", "initiative_1"},
	} {
		event, aggregate, _ := mcpCommentEvent(data, tc.target, tc.operation)
		if event != tc.event || aggregate != tc.aggregate {
			t.Errorf("%s %s: recorded %s on %s, want %s on %s", tc.operation, tc.target, event, aggregate, tc.event, tc.aggregate)
		}
	}
}

func chatCardJSON(t *testing.T, card map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// An update card carries the update: health with its colour, author by
// display name, the posting time in the team's zone, and the body as card
// markdown (list, bold, link, mention by name), cut with a link to the
// update itself when it is long.
func TestChatUpdateCardShowsTheUpdate(t *testing.T) {
	author := domain.User{ID: "user_8f3a", Name: "user_8f3a", DisplayName: "王小明", Email: "wang@example.com"}
	mentioned := domain.User{ID: "user_li", Name: "li", DisplayName: "李雷"}
	body := "本周进展 **顺利** marker-7731\n\n- 完成 [设计稿](https://example.com/design)\n- 修复 <script> 与 *问题*\n\n1. 发布\n2. 回顾"
	document := mcpMarkdownDocument(body)
	// A person mentioned in the editor is written with their current name.
	document["content"] = append(document["content"].([]any), map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": "负责人 "}, map[string]any{"type": "mention", "attrs": map[string]any{"mentionType": "user", "id": mentioned.ID, "label": "old name"}}}})
	update := chatPulseUpdate{ID: "project_update_1", Body: body, BodyData: document, Health: "atRisk", User: author, CreatedAt: time.Date(2026, 11, 4, 8, 30, 0, 0, time.UTC)}
	link := "https://flow.example/acme/project/launch/overview#update-project_update_1"
	card := chatUpdateCard(chatUpdateCardInput{Language: chatLanguageChinese, Heading: "project_update", Name: "发布计划", Link: link, Origin: "https://flow.example", Zone: "Asia/Shanghai", Directory: []domain.User{author, mentioned}, Update: update})
	content := chatCardJSON(t, card)
	for _, want := range []string{
		`"template":"orange"`, `项目更新 · 发布计划`, `**存在风险**`, `王小明`, `2026年11月4日 16:30（UTC+08:00）`,
		`本周进展 **顺利** marker-7731`, `- 完成 [设计稿](https://example.com/design)`, `*问题*`, `1. 发布\n2. 回顾`, `@李雷`, `查看更新`, link,
	} {
		if !strings.Contains(content, want) {
			t.Errorf("update card lacks %s:\n%s", want, content)
		}
	}
	for _, unwanted := range []string{"user_8f3a", "old name", "Project update", "Read more", "At risk"} {
		if strings.Contains(content, unwanted) {
			t.Errorf("update card shows %q:\n%s", unwanted, content)
		}
	}
	if !strings.Contains(content, `修复 \\\u003cscript\\\u003e`) {
		t.Errorf("markup in the body is not escaped:\n%s", content)
	}

	// English, and a body longer than the card: cut at a block, with a link
	// to the update.
	long := strings.Repeat("Paragraph with enough words to fill the card.\n\n", 80)
	update = chatPulseUpdate{ID: "project_update_2", Body: long, Health: "onTrack", User: author, CreatedAt: update.CreatedAt}
	content = chatCardJSON(t, chatUpdateCard(chatUpdateCardInput{Language: chatLanguageEnglish, Heading: "project_update", Name: "Launch", Link: link, Zone: "America/New_York", Update: update}))
	for _, want := range []string{`"template":"green"`, "Project update · Launch", "**On track**", "王小明", "Nov 4, 2026, 3:30 AM (UTC-05:00)", "[Read more](" + link + ")"} {
		if !strings.Contains(content, want) {
			t.Errorf("long English card lacks %s", want)
		}
	}
	var parsed struct {
		Elements []map[string]any `json:"elements"`
	}
	_ = json.Unmarshal([]byte(content), &parsed)
	if text := parsed.Elements[1]["content"].(string); utf8RuneCount(text) > chatUpdateBodyLimit+200 || strings.Count(text, "Paragraph") >= 80 {
		t.Errorf("long body not cut: %d runes", utf8RuneCount(text))
	}
}

func utf8RuneCount(text string) int { return len([]rune(text)) }

// A project update posted in Flow reaches the group with its content; an
// edit redraws that card and a deletion retires it.
func TestChatProjectUpdateCardLifecycle(t *testing.T) {
	f := newChatWorkflowFixture(t)
	f.binding.Events = append(f.binding.Events, "project.update_created")
	if err := f.s.store.PutChatBinding(t.Context(), f.record.WorkspaceKey, f.binding); err != nil {
		t.Fatal(err)
	}
	viewer := f.s.store.Bootstrap().Viewer
	projectID := "project_chat_update"
	if err := f.s.store.MutateWorkspace(t.Context(), f.record.WorkspaceKey, "test.chat_project", projectID, nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, domain.Project{ID: projectID, SlugID: "chat-launch", Name: "发布计划", TeamIDs: []string{f.binding.TeamID}, Health: "onTrack", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()})
		for index := range data.Users {
			if data.Users[index].ID == viewer.ID {
				data.Users[index].DisplayName = "王小明"
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.followEvents(t)
	handler := func(serve func(http.ResponseWriter, *http.Request), values map[string]string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for key, value := range values {
				r.SetPathValue(key, value)
			}
			r.Header.Set("X-Workspace-Key", f.record.WorkspaceKey)
			serve(w, r)
		})
	}
	body := "本周完成：\n\n- **支付** 接入\n- 文档 marker-5521"
	created := requestJSON[domain.ProjectUpdate](t, handler(f.s.createProjectUpdate, map[string]string{"id": projectID}), http.MethodPost, "/api/projects/"+projectID+"/updates", map[string]any{"body": body, "health": "offTrack"}, http.StatusCreated)
	sent := len(f.sent)
	f.syncEvents(t)
	if len(f.sent) != sent+1 {
		t.Fatalf("project update posted %d cards, want 1", len(f.sent)-sent)
	}
	content := f.sent[sent]["content"].(string)
	for _, want := range []string{"marker-5521", "- **支付** 接入", "**偏离计划**", `"template":"red"`, "王小明", "#update-" + created.ID, "项目更新 · 发布计划"} {
		if !strings.Contains(content, want) {
			t.Errorf("project update card lacks %s:\n%s", want, content)
		}
	}

	patches := len(f.patched)
	edited := "已更新 marker-6642"
	requestJSON[domain.ProjectUpdate](t, handler(f.s.updateProjectUpdate, map[string]string{"id": projectID, "updateId": created.ID}), http.MethodPatch, "/api/projects/"+projectID+"/updates/"+created.ID, map[string]any{"body": edited}, http.StatusOK)
	f.syncEvents(t)
	if len(f.patched) != patches+1 || !strings.Contains(f.patched[patches]["content"].(string), "marker-6642") || len(f.sent) != sent+1 {
		t.Fatalf("edit did not redraw the card: %d patches, %d sends", len(f.patched)-patches, len(f.sent)-sent)
	}

	patches = len(f.patched)
	request, err := http.NewRequest(http.MethodDelete, "/api/projects/"+projectID+"/updates/"+created.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler(f.s.deleteProjectUpdate, map[string]string{"id": projectID, "updateId": created.ID}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete update: %d %s", recorder.Code, recorder.Body.String())
	}
	f.syncEvents(t)
	if len(f.patched) != patches+1 || !strings.Contains(f.patched[patches]["content"].(string), chatT(chatLanguageChinese, "update_deleted")) {
		t.Fatalf("deletion did not retire the card: %#v", f.patched[patches:])
	}
}

// Group issue cards are in the binding's language, name people by display
// name and show the team's own state names.
func TestChatIssueCardLanguageAndPeople(t *testing.T) {
	f := newChatWorkflowFixture(t)
	issue := f.s.store.Bootstrap().Issues[0]
	issue.Priority = 1
	issue.Assignee = &domain.User{ID: "user_8f3a", Name: "user_8f3a", DisplayName: "李雷", Email: "lei@example.com"}
	for _, tc := range []struct {
		language        string
		want, forbidden []string
	}{
		{"zh-CN", []string{"在 Flow 中打开", "评论", "指派给我", "状态", "优先级", "紧急", "李雷", issue.State.Name}, []string{"Open in Flow", "Assign to me", "Comment", "Priority", "Urgent", "user_8f3a"}},
		{"en-US", []string{"Open in Flow", "Comment", "Assign to me", "Status", "Priority", "Urgent", "李雷"}, []string{"在 Flow 中打开", "指派给我", "优先级", "user_8f3a"}},
	} {
		binding := f.binding
		binding.Language = tc.language
		card, _, err := f.s.chatIssueCard(t.Context(), f.record, binding, issue, "language_"+tc.language, "", "")
		if err != nil {
			t.Fatal(err)
		}
		card["elements"] = append([]map[string]any{chatCardText(chatNotificationLabel(chatGroupLanguage(f.record, binding), "issue.created"))}, card["elements"].([]map[string]any)...)
		content := chatCardJSON(t, card)
		for _, want := range append(tc.want, chatT(chatGroupLanguage(f.record, binding), "issue.created")) {
			if !strings.Contains(content, want) {
				t.Errorf("%s card lacks %q:\n%s", tc.language, want, content)
			}
		}
		for _, unwanted := range tc.forbidden {
			if strings.Contains(content, unwanted) {
				t.Errorf("%s card shows %q:\n%s", tc.language, unwanted, content)
			}
		}
	}
	// Without a language the platform decides: Feishu Chinese, Lark English.
	if chatGroupLanguage(domain.ChatIntegrationRecord{Provider: "feishu"}, domain.ChatBinding{}) != chatLanguageChinese || chatGroupLanguage(domain.ChatIntegrationRecord{Provider: "lark"}, domain.ChatBinding{}) != chatLanguageEnglish {
		t.Fatal("platform default language")
	}
	unassigned := issue
	unassigned.Assignee = nil
	if line := chatIssueSummaryLine(chatLanguageChinese, unassigned, nil); !strings.Contains(line, "未分配") || !strings.Contains(line, "紧急") {
		t.Fatalf("summary line: %s", line)
	}
	comment := domain.Comment{ID: "comment_1", Body: "hello", User: domain.User{ID: "user_8f3a", Name: "user_8f3a"}}
	if heading := chatCardJSON(t, chatCommentCard(chatLanguageEnglish, comment, "", []domain.User{{ID: "user_8f3a", Name: "user_8f3a", DisplayName: "Lei Li"}})); !strings.Contains(heading, "Lei Li commented") {
		t.Fatalf("comment author: %s", heading)
	}
}

// A binding's language is one of the supported ones, and a save that leaves
// it out keeps it.
func TestChatBindingLanguageIsKept(t *testing.T) {
	provider := "lark"
	f := newChatPermissionFixture(t, provider)
	f.authorizeTenant(provider)
	f.addGroup("oc_language")
	base := "/api/integrations/" + provider + "/chat"
	binding := domain.ChatBinding{ChatID: "oc_language", ChatName: "Language", TeamID: f.publicTeam, Events: []string{"issue.created"}, Enabled: true}
	refused := binding
	refused.Language = "fr-FR"
	f.expect(f.admin, "POST", base+"/bindings", chatAuthorizedBinding(refused), 400)
	binding.Language = "zh-CN"
	saved := f.expect(f.admin, "POST", base+"/bindings", chatAuthorizedBinding(binding), 200)
	if saved["language"] != "zh-CN" {
		t.Fatalf("language not saved: %v", saved)
	}
	binding.ID, binding.Language, binding.AllowUpdate = saved["id"].(string), "", true
	if kept := f.expect(f.admin, "POST", base+"/bindings", chatAuthorizedBinding(binding), 200); kept["language"] != "zh-CN" || kept["allowUpdate"] != true {
		t.Fatalf("a save without the language reset it: %v", kept)
	}
	stored, err := f.s.chatBindingByID(t.Context(), f.record(provider), binding.ID)
	if err != nil || chatGroupLanguage(f.record(provider), stored) != chatLanguageChinese {
		t.Fatalf("Lark group with a Chinese binding: %+v %v", stored, err)
	}
	cleared := f.expect(f.admin, "POST", base+"/bindings", map[string]any{"id": binding.ID, "chatId": binding.ChatID, "chatName": binding.ChatName, "teamId": binding.TeamID, "events": binding.Events, "enabled": true, "authorize": true, "language": ""}, 200)
	if cleared["language"] != nil {
		t.Fatalf("language not cleared: %v", cleared)
	}
}

func TestChatPersonLabel(t *testing.T) {
	for _, tc := range []struct {
		user domain.User
		want string
	}{
		{domain.User{ID: "u1", Name: "Name", DisplayName: "Display"}, "Display"},
		{domain.User{ID: "u1", Name: "Name"}, "Name"},
		{domain.User{ID: "u1", Name: "u1", Username: "handle"}, "handle"},
		{domain.User{ID: "u1", Name: "a@example.com", Email: "a@example.com"}, "a"},
		{domain.User{ID: "u1"}, "u1"},
	} {
		if got := chatPersonLabel(&tc.user, nil); got != tc.want {
			t.Errorf("%+v: %q, want %q", tc.user, got, tc.want)
		}
	}
	if chatPersonLabel(nil, nil) != "" {
		t.Fatal("nil person")
	}
}

// Every IM text has both languages, and the Chinese one is translated.
func TestChatIMTextsAreTranslated(t *testing.T) {
	same := map[string]bool{"list_separator": true, "time_utc": true, "comment_heading_unknown": true, "time_layout": true, "time_zone": true}
	for key, texts := range chatIMTexts {
		if texts[0] == "" || texts[1] == "" {
			t.Errorf("%s lacks a language", key)
		}
		if !same[key] && !strings.ContainsFunc(texts[1], func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
			t.Errorf("%s is not translated: %q", key, texts[1])
		}
		if strings.Count(texts[0], "%") != strings.Count(texts[1], "%") {
			t.Errorf("%s: the languages take different arguments", key)
		}
	}
}

// chatIMBuilders are the functions that draw group cards, summaries, thread
// messages and comment sync. Their fixed texts come from chatIMTexts.
var chatIMBuilders = map[string][]string{
	"chat_cards.go":               {"chatAssigneeOptions", "chatSendCreateForm", "chatSendUpdateForm", "chatSendPersonalForm", "chatIssueCard"},
	"chat_card_actions.go":        {"chatPriorityOptions", "chatQuickElements", "chatIssueSummaryLine", "chatGroupQuickControls", "chatPersonalIssueCard", "chatPersonalNotificationCard"},
	"chat_notifications.go":       {"chatNotificationLabel", "chatSendIssueCardFrom", "chatSendInitiativeUpdate", "chatRefreshIssueCards"},
	"chat_issue_threads.go":       {"chatCommentCard", "chatNoticeCard", "chatSyncIssueComments"},
	"chat_update_cards.go":        nil, // every function
	"chat_initiative_bindings.go": {"chatSendInitiativeBindingUpdate"},
	"chat_binding_projects.go":    {"chatCreateProjectSelect", "chatProjectsLabel"},
}

var chatEnglishText = regexp.MustCompile(`[A-Za-z]{2,} [A-Za-z]{2,}|^[A-Z][a-z]+`)

// A new English text in an IM card builder fails here: add it to
// chatIMTexts and write it with chatT.
func TestChatIMBuildersHaveNoUntranslatedText(t *testing.T) {
	// Calls whose string arguments are keys, IDs or internal errors.
	exempt := map[string]bool{"chatT": true, "chatKey": true, "chatInvalid": true, "Errorf": true, "New": true, "Printf": true, "WorkspaceMetadataFields": true, "chatHealthText": true}
	for file, functions := range chatIMBuilders {
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || functions != nil && !slices.Contains(functions, function.Name.Name) {
				continue
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				if call, ok := node.(*ast.CallExpr); ok {
					name := ""
					switch fun := call.Fun.(type) {
					case *ast.Ident:
						name = fun.Name
					case *ast.SelectorExpr:
						name = fun.Sel.Name
					}
					return !exempt[name]
				}
				if pair, ok := node.(*ast.KeyValueExpr); ok {
					// Map keys are card fields; only values are text.
					ast.Inspect(pair.Value, func(inner ast.Node) bool { return chatCheckLiteral(t, file, function.Name.Name, inner, exempt) })
					return false
				}
				return chatCheckLiteral(t, file, function.Name.Name, node, exempt)
			})
		}
	}
}

func chatCheckLiteral(t *testing.T, file, function string, node ast.Node, exempt map[string]bool) bool {
	t.Helper()
	if call, ok := node.(*ast.CallExpr); ok {
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			return !exempt[fun.Name]
		case *ast.SelectorExpr:
			return !exempt[fun.Sel.Name]
		}
	}
	literal, ok := node.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return true
	}
	text, err := strconv.Unquote(literal.Value)
	if err == nil && text != "Flow" && chatEnglishText.MatchString(text) {
		t.Errorf("%s %s: untranslated IM text %q; add it to chatIMTexts", file, function, text)
	}
	return true
}
