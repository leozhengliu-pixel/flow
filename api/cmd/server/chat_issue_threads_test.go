package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

type chatThreadCall struct {
	method, path string
	body         map[string]any
}

// chatThreadPlatform records the message requests the workflow fixture's
// platform answers, and refuses replies to and updates of messages marked
// gone the way the platform does for a recalled or deleted message.
type chatThreadPlatform struct {
	calls []chatThreadCall
	gone  map[string]bool
}

func (f *chatWorkflowFixture) threadPlatform(t *testing.T) *chatThreadPlatform {
	t.Helper()
	platform := &chatThreadPlatform{gone: map[string]bool{}}
	inner := http.DefaultTransport
	http.DefaultTransport = chatWorkflowTransport(func(r *http.Request) (*http.Response, error) {
		if (r.URL.Hostname() == "open.feishu.cn" || r.URL.Hostname() == "open.larksuite.com") && strings.Contains(r.URL.Path, "/im/v1/messages") && (r.Method == http.MethodPost || r.Method == http.MethodPatch) {
			raw, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			target := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/open-apis/im/v1/messages/"), "/reply")
			if platform.gone[target] {
				return chatTestResponse(400, `{"code":230110,"msg":"Action unavailable as the message has been deleted"}`), nil
			}
			var body map[string]any
			_ = json.Unmarshal(raw, &body)
			platform.calls = append(platform.calls, chatThreadCall{r.Method, r.URL.Path, body})
		}
		return inner.RoundTrip(r)
	})
	t.Cleanup(func() { http.DefaultTransport = inner })
	return platform
}

func (p *chatThreadPlatform) since(index int) []chatThreadCall { return p.calls[index:] }

func chatThreadReplyTo(call chatThreadCall) string {
	if call.method != http.MethodPost || !strings.HasSuffix(call.path, "/reply") {
		return ""
	}
	return strings.TrimSuffix(strings.TrimPrefix(call.path, "/open-apis/im/v1/messages/"), "/reply")
}

func (f *chatWorkflowFixture) notify(t *testing.T, kind, issueID, key string) {
	t.Helper()
	job := domain.ChatJob{ID: "job_" + key, WorkspaceKey: f.record.WorkspaceKey, ConnectionID: f.record.ConnectionID, Kind: "notification", DedupKey: key}
	job.Payload, _ = json.Marshal(chatNotificationJob{BindingID: f.binding.ID, BindingScope: chatBindingFingerprint(f.binding), IssueID: issueID, Kind: kind})
	if err := f.s.processChatJob(t.Context(), job); err != nil {
		t.Fatal(err)
	}
}

// deliver queues a domain event like the committed event log and runs the
// notification jobs it produced.
func (f *chatWorkflowFixture) deliver(t *testing.T, event domain.DomainEvent) {
	t.Helper()
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	if err := f.s.queueChatDomainEvent(t.Context(), f.record.WorkspaceKey, event); err != nil {
		t.Fatal(err)
	}
	f.runJobs(t, "notification")
}

func (f *chatWorkflowFixture) runJobs(t *testing.T, kind string) {
	t.Helper()
	jobs, err := f.s.store.ClaimChatJobs(t.Context(), f.record.WorkspaceKey, kind, 20, time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range jobs {
		if err := f.s.processChatJob(t.Context(), job); err != nil {
			t.Fatalf("%s job: %v", kind, err)
		}
		if err := f.s.store.FinishChatJob(t.Context(), job, "completed", time.Time{}, ""); err != nil {
			t.Fatal(err)
		}
	}
}

func (f *chatWorkflowFixture) thread(t *testing.T, issueID string) domain.ChatThread {
	t.Helper()
	thread, err := f.s.store.ChatThread(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, f.binding.ChatID, domain.ChatThreadResourceIssue, issueID)
	if err != nil {
		t.Fatalf("issue thread: %v", err)
	}
	return thread
}

// replyEnvelope is a member's text reply in a thread.
func (f *chatWorkflowFixture) replyEnvelope(openID, root, messageID, text string) chatWebhookEnvelope {
	envelope := f.messageEnvelope()
	envelope.Header.EventID = "evt_" + messageID
	envelope.Event.Sender.SenderID.OpenID = openID
	envelope.Event.Message.MessageID = messageID
	envelope.Event.Message.RootID = root
	envelope.Event.Message.Content = `{"text":` + string(must(json.Marshal(text))) + `}`
	envelope.Event.Message.Mentions = nil
	return envelope
}

func must(value []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return value
}

func (f *chatWorkflowFixture) addComment(t *testing.T, issueID, body string) domain.Comment {
	t.Helper()
	var comment domain.Comment
	ctx := withChatIdentityAuthentication(t.Context(), f.identity)
	if err := f.s.chatAuthorizedMutation(ctx, f.record.WorkspaceKey, f.identity.UserID, http.MethodPost, "/api/issue-records/"+issueID+"/comments", domain.CommentCreateInput{Body: body}, &comment); err != nil {
		t.Fatal(err)
	}
	return comment
}

func (f *chatWorkflowFixture) comments(t *testing.T, issueID string) []domain.Comment {
	t.Helper()
	comments, _, err := f.s.store.ResourceCommentsPage(t.Context(), f.record.WorkspaceKey, issueID, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	return comments
}

func chatCallsMention(calls []chatThreadCall, text string) []chatThreadCall {
	matched := []chatThreadCall{}
	for _, call := range calls {
		raw, _ := json.Marshal(call.body)
		if strings.Contains(string(raw), text) {
			matched = append(matched, call)
		}
	}
	return matched
}

// All notifications about an issue in a group go into one thread: the first
// card is its root and later ones reply to it. Once the root is deleted, the
// next notification starts a new thread.
func TestChatIssueThreadKeepsOneRootPerIssueAndRestartsWhenTheRootIsGone(t *testing.T) {
	f := newChatWorkflowFixture(t)
	platform := f.threadPlatform(t)
	issue := f.s.store.Bootstrap().Issues[0]

	f.notify(t, "issue.created", issue.ID, "thread_created")
	if len(platform.calls) != 1 || platform.calls[0].path != "/open-apis/im/v1/messages" || platform.calls[0].body["receive_id"] != f.binding.ChatID {
		t.Fatalf("first notification did not start a thread: %#v", platform.calls)
	}
	root := f.thread(t, issue.ID)
	messages, _ := f.s.store.ChatMessagesForIssue(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, issue.ID)
	if len(messages) != 1 || root.RootMessageID != messages[0].MessageID || root.BindingID != f.binding.ID {
		t.Fatalf("thread root not recorded: %+v %+v", root, messages)
	}

	f.notify(t, "issue.status_changed", issue.ID, "thread_state")
	f.notify(t, "issue.status_changed", issue.ID, "thread_state_again")
	for _, call := range platform.since(1) {
		if chatThreadReplyTo(call) != root.RootMessageID || call.body["reply_in_thread"] != true || call.body["receive_id"] != nil {
			t.Fatalf("later notification left the thread: %#v", call)
		}
	}
	if again := f.thread(t, issue.ID); again.ID != root.ID {
		t.Fatalf("root reused, thread replaced: %+v", again)
	}

	// Another issue gets its own thread.
	other := f.s.store.Bootstrap().Issues[1]
	if other.Team.ID == f.binding.TeamID {
		f.notify(t, "issue.created", other.ID, "thread_other")
		if f.thread(t, other.ID).RootMessageID == root.RootMessageID {
			t.Fatal("two issues share a thread")
		}
	}

	// The root is deleted in the group: the next card starts a new thread.
	platform.gone[root.RootMessageID] = true
	before := len(platform.calls)
	f.notify(t, "issue.status_changed", issue.ID, "thread_after_delete")
	restarted := platform.since(before)
	if len(restarted) != 1 || restarted[0].path != "/open-apis/im/v1/messages" || restarted[0].body["receive_id"] != f.binding.ChatID {
		t.Fatalf("deleted root did not start a new thread: %#v", restarted)
	}
	renewed := f.thread(t, issue.ID)
	if renewed.RootMessageID == root.RootMessageID || renewed.ID == root.ID {
		t.Fatalf("thread still points at the deleted root: %+v", renewed)
	}
	if _, err := f.s.store.ChatThreadForRoot(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, root.RootMessageID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("old root still routes replies: %v", err)
	}
	before = len(platform.calls)
	f.notify(t, "issue.status_changed", issue.ID, "thread_after_restart")
	if calls := platform.since(before); len(calls) != 1 || chatThreadReplyTo(calls[0]) != renewed.RootMessageID {
		t.Fatalf("notification after restart: %#v", calls)
	}
}

// A linked member's reply in the thread becomes a Flow comment as them, and is
// not posted back; Flow comments go into the thread, and their edits and
// deletions are reflected there.
func TestChatIssueThreadSyncsCommentsBothWaysWithoutEchoes(t *testing.T) {
	f := newChatWorkflowFixture(t)
	platform := f.threadPlatform(t)
	issue := f.s.store.Bootstrap().Issues[0]
	f.notify(t, "issue.created", issue.ID, "sync_created")
	root := f.thread(t, issue.ID)
	before := len(f.comments(t, issue.ID))

	// A reply in the thread, without mentioning the bot, through the webhook.
	if response := f.callback(t, f.replyEnvelope(f.identity.OpenID, root.RootMessageID, "om_member_reply", "Reproduced on staging"), time.Now(), true); response.Code != http.StatusOK {
		t.Fatalf("reply callback: %d %s", response.Code, response.Body.String())
	}
	// Redelivered by the platform: deduplicated.
	f.callback(t, f.replyEnvelope(f.identity.OpenID, root.RootMessageID, "om_member_reply", "Reproduced on staging"), time.Now(), true)
	f.runJobs(t, "inbound")
	comments := f.comments(t, issue.ID)
	if len(comments) != before+1 {
		t.Fatalf("reply not imported once: %d comments, want %d", len(comments), before+1)
	}
	imported := comments[len(comments)-1]
	if imported.User.ID != f.identity.UserID || imported.Body != "Reproduced on staging" {
		t.Fatalf("reply not written as the member: %+v", imported)
	}

	// Its comment event does not post it back into the thread.
	calls := len(platform.calls)
	f.deliver(t, domain.DomainEvent{ID: "evt_imported", Type: "comment.created", AggregateID: issue.ID})
	if echoed := chatCallsMention(platform.since(calls), "Reproduced on staging"); len(echoed) != 0 {
		t.Fatalf("imported reply echoed back: %#v", echoed)
	}

	// A comment written in Flow is posted into the thread as a reply.
	written := f.addComment(t, issue.ID, "Fix merged, deploying")
	calls = len(platform.calls)
	f.deliver(t, domain.DomainEvent{ID: "evt_written", Type: "comment.created", AggregateID: issue.ID})
	posted := chatCallsMention(platform.since(calls), "Fix merged, deploying")
	if len(posted) != 1 || chatThreadReplyTo(posted[0]) != root.RootMessageID {
		t.Fatalf("Flow comment not posted into the thread: %#v", platform.since(calls))
	}
	items, _ := f.s.store.ChatThreadItems(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, root.ID)
	mirror := items[slices.IndexFunc(items, func(item domain.ChatThreadItem) bool { return item.ItemID == written.ID })]
	if mirror.Origin != domain.ChatThreadOriginFlow || mirror.MessageID == "" {
		t.Fatalf("mirrored comment not recorded: %+v", mirror)
	}
	// Delivered again (another event): not posted twice.
	calls = len(platform.calls)
	f.deliver(t, domain.DomainEvent{ID: "evt_written_again", Type: "comment.created", AggregateID: issue.ID})
	if len(platform.since(calls)) != 0 {
		t.Fatalf("comment posted twice: %#v", platform.since(calls))
	}

	// The bot's own message in the thread, and a member message that carries
	// a mirrored comment, are never imported.
	bot := f.replyEnvelope(f.identity.OpenID, root.RootMessageID, mirror.MessageID, "Fix merged, deploying")
	bot.Event.Sender.SenderType = "app"
	f.callback(t, bot, time.Now(), true)
	echo := f.replyEnvelope(f.identity.OpenID, root.RootMessageID, mirror.MessageID, "Fix merged, deploying")
	echo.Header.EventID = "evt_echo"
	f.callback(t, echo, time.Now(), true)
	f.runJobs(t, "inbound")
	if got := len(f.comments(t, issue.ID)); got != before+2 {
		t.Fatalf("bot message or mirrored message imported: %d comments, want %d", got, before+2)
	}

	// Editing the Flow comment updates its message; deleting it notes it.
	f.editComment(t, issue.ID, written.ID, "Fix merged, deployed to production")
	patches := len(f.patched)
	f.deliver(t, domain.DomainEvent{ID: "evt_edited", Type: "comment.updated", AggregateID: issue.ID})
	if len(f.patched) != patches+1 || !strings.Contains(f.patched[patches]["content"].(string), "deployed to production") || !strings.Contains(f.patched[patches]["content"].(string), "Edited in Flow") {
		t.Fatalf("edit not reflected: %#v", f.patched[patches:])
	}
	// Editing the imported reply in Flow posts the edit: the member's own
	// message cannot be changed by the bot.
	f.editComment(t, issue.ID, imported.ID, "Reproduced on staging and production")
	calls = len(platform.calls)
	f.deliver(t, domain.DomainEvent{ID: "evt_edited_import", Type: "comment.updated", AggregateID: issue.ID})
	if posted := chatCallsMention(platform.since(calls), "staging and production"); len(posted) != 1 || chatThreadReplyTo(posted[0]) != root.RootMessageID {
		t.Fatalf("edit of an imported reply not posted: %#v", platform.since(calls))
	}
	f.deleteComment(t, issue.ID, written.ID)
	patches = len(f.patched)
	f.deliver(t, domain.DomainEvent{ID: "evt_deleted", Type: "comment.deleted", AggregateID: issue.ID})
	if len(f.patched) != patches+1 || !strings.Contains(f.patched[patches]["content"].(string), "deleted in Flow") || strings.Contains(f.patched[patches]["content"].(string), "deployed to production") {
		t.Fatalf("deletion not reflected: %#v", f.patched[patches:])
	}
	f.deliver(t, domain.DomainEvent{ID: "evt_deleted_again", Type: "comment.deleted", AggregateID: issue.ID})
	if len(f.patched) != patches+1 {
		t.Fatal("deletion noted twice")
	}
}

func (f *chatWorkflowFixture) editComment(t *testing.T, issueID, commentID, body string) {
	t.Helper()
	if err := f.s.store.MutateWorkspace(t.Context(), f.record.WorkspaceKey, "comment.updated", issueID, map[string]string{"commentId": commentID}, func(data *domain.Bootstrap) error {
		for index := range data.Comments[issueID] {
			if data.Comments[issueID][index].ID == commentID {
				now := time.Now().UTC()
				data.Comments[issueID][index].Body, data.Comments[issueID][index].EditedAt = body, &now
				data.Comments[issueID][index].Version++
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func (f *chatWorkflowFixture) deleteComment(t *testing.T, issueID, commentID string) {
	t.Helper()
	if err := f.s.store.MutateWorkspace(t.Context(), f.record.WorkspaceKey, "comment.deleted", issueID, map[string]string{"commentId": commentID}, func(data *domain.Bootstrap) error {
		data.Comments[issueID] = slices.DeleteFunc(data.Comments[issueID], func(comment domain.Comment) bool { return comment.ID == commentID })
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// A reply becomes a comment only with the binding's update flag, from a linked
// member, and with their own access; an unlinked member is answered with one
// link card per thread. A create command addressed to the bot keeps its
// meaning inside a thread.
func TestChatIssueThreadRepliesNeedUpdatesALinkedMemberAndTheirAccess(t *testing.T) {
	f := newChatWorkflowFixture(t)
	platform := f.threadPlatform(t)
	data := f.s.store.Bootstrap()
	issue := data.Issues[0]
	f.notify(t, "issue.created", issue.ID, "flags_created")
	root := f.thread(t, issue.ID)
	before := len(f.comments(t, issue.ID))

	// Updates off: replies stay in the group.
	off := f.binding
	off.AllowUpdate = false
	if err := f.s.store.PutChatBinding(t.Context(), f.record.WorkspaceKey, off); err != nil {
		t.Fatal(err)
	}
	calls := len(platform.calls)
	f.callback(t, f.replyEnvelope(f.identity.OpenID, root.RootMessageID, "om_flag_off", "Just chatting"), time.Now(), true)
	f.runJobs(t, "inbound")
	if len(f.comments(t, issue.ID)) != before || len(platform.since(calls)) != 0 {
		t.Fatalf("reply imported or answered while updates are off: %#v", platform.since(calls))
	}
	if err := f.s.store.PutChatBinding(t.Context(), f.record.WorkspaceKey, f.binding); err != nil {
		t.Fatal(err)
	}

	// An unlinked member: one link card in the thread, however many replies.
	for _, id := range []string{"om_unlinked_1", "om_unlinked_2"} {
		f.callback(t, f.replyEnvelope("ou_unlinked", root.RootMessageID, id, "Me too"), time.Now(), true)
	}
	f.runJobs(t, "inbound")
	links := chatCallsMention(platform.since(calls), "Connect your Flow account")
	if len(links) != 1 || chatThreadReplyTo(links[0]) != "om_unlinked_1" || links[0].body["reply_in_thread"] != true || !strings.Contains(links[0].body["content"].(string), "/settings/account/connections") {
		t.Fatalf("link card: %#v", platform.since(calls))
	}
	if len(f.comments(t, issue.ID)) != before {
		t.Fatal("unlinked reply became a comment")
	}

	// A linked member without access to the issue's team gets a private
	// answer and no comment.
	guest, _, err := f.s.store.LoginExternal(t.Context(), "chat-thread-guest@example.test", "Thread guest", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.store.EnsureWorkspaceMembership(t.Context(), data.Workspace.ID, guest.User.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.s.store.UpdateMemberRole(t.Context(), data.Workspace.ID, guest.User.ID, "guest"); err != nil {
		t.Fatal(err)
	}
	if err := f.s.store.SetTeamMembership(t.Context(), data.Workspace.ID, issue.Team.ID, guest.User.ID, "", false); err != nil {
		t.Fatal(err)
	}
	guestIdentity := domain.ChatIdentity{UserID: guest.User.ID, OpenID: "ou_thread_guest", TenantKey: f.record.TenantKey, AuthProvider: "password", AuthenticatedAt: time.Now().UTC()}
	if err := f.s.store.PutChatIdentity(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, guestIdentity); err != nil {
		t.Fatal(err)
	}
	calls = len(platform.calls)
	f.callback(t, f.replyEnvelope(guestIdentity.OpenID, root.RootMessageID, "om_guest_reply", "Can I see this?"), time.Now(), true)
	f.runJobs(t, "inbound")
	if got := f.comments(t, issue.ID); len(got) != before {
		role, _ := f.s.store.TeamRole(t.Context(), data.Workspace.ID, issue.Team.ID, guest.User.ID)
		t.Fatalf("reply from a member without access became a comment: %+v team role %q", got[len(got)-1].User, role)
	}
	for _, call := range platform.since(calls) {
		if call.body["receive_id"] != guestIdentity.OpenID {
			t.Fatalf("refusal not private: %#v", call)
		}
	}

	// A create command in the thread still opens the create form.
	create := f.replyEnvelope(f.identity.OpenID, root.RootMessageID, "om_thread_create", "@_user_1 创建事项 Follow-up")
	raw, _ := json.Marshal([]map[string]any{{"key": "@_user_1", "id": map[string]string{"open_id": f.record.BotOpenID}}})
	_ = json.Unmarshal(raw, &create.Event.Message.Mentions)
	command, _, accepted, err := f.s.chatCommandForEnvelope(t.Context(), f.record, create)
	if err != nil || !accepted || command.Kind != "create_form" || command.Title != "Follow-up" {
		t.Fatalf("create command in a thread: %+v %v %v", command, accepted, err)
	}
	// A reply in a thread Flow does not know is ignored.
	stray := f.replyEnvelope(f.identity.OpenID, "om_unknown_root", "om_stray", "Hello")
	if _, _, accepted, _ := f.s.chatCommandForEnvelope(t.Context(), f.record, stray); accepted {
		t.Fatal("reply in an unknown thread accepted")
	}
	// Mentions of other members read as their names.
	named := f.replyEnvelope(f.identity.OpenID, root.RootMessageID, "om_named", "@_user_2 please check")
	raw, _ = json.Marshal([]map[string]any{{"key": "@_user_2", "name": "Ada", "id": map[string]string{"open_id": "ou_ada"}}})
	_ = json.Unmarshal(raw, &named.Event.Message.Mentions)
	if command, _, _, _ := f.s.chatCommandForEnvelope(t.Context(), f.record, named); command.Kind != chatThreadReplyKind || command.Text != "@Ada please check" {
		t.Fatalf("mention text: %+v", command)
	}
}

// The thread mapping is generic: any resource type maps to a root, and a new
// root for the same resource replaces the old thread with its items.
func TestChatThreadStoreReplacesThreadsAndKeepsItemsOnce(t *testing.T) {
	f := newChatWorkflowFixture(t)
	ws, conn := f.record.WorkspaceKey, f.record.ConnectionID
	first, err := f.s.store.PutChatThread(t.Context(), ws, domain.ChatThread{ConnectionID: conn, ChatID: "oc_group_test", ResourceType: "ask", ResourceID: "ask_1", RootMessageID: "om_root_1"})
	if err != nil {
		t.Fatal(err)
	}
	item := domain.ChatThreadItem{ThreadID: first.ID, ItemType: "comment", ItemID: "comment_1", MessageID: "om_item_1", Origin: domain.ChatThreadOriginChat, Version: 1}
	for attempt, want := range []bool{true, false} {
		inserted, err := f.s.store.InsertChatThreadItem(t.Context(), ws, conn, item)
		if err != nil || inserted != want {
			t.Fatalf("insert %d: %v %v", attempt, inserted, err)
		}
	}
	if found, err := f.s.store.ChatThreadItemForMessage(t.Context(), ws, conn, "om_item_1"); err != nil || found.ItemID != "comment_1" {
		t.Fatalf("item by message: %+v %v", found, err)
	}
	second, err := f.s.store.PutChatThread(t.Context(), ws, domain.ChatThread{ConnectionID: conn, ChatID: "oc_group_test", ResourceType: "ask", ResourceID: "ask_1", RootMessageID: "om_root_2"})
	if err != nil || second.ID == first.ID {
		t.Fatalf("replacement: %+v %v", second, err)
	}
	if items, _ := f.s.store.ChatThreadItems(t.Context(), ws, conn, first.ID); len(items) != 0 {
		t.Fatalf("items of the replaced thread kept: %+v", items)
	}
	if _, err := f.s.store.ChatThreadForRoot(t.Context(), ws, conn, "om_root_1"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("replaced root still found: %v", err)
	}
	if found, err := f.s.store.ChatThreadForRoot(t.Context(), ws, conn, "om_root_2"); err != nil || found.ResourceID != "ask_1" {
		t.Fatalf("new root: %+v %v", found, err)
	}
	if _, err := f.s.store.PutChatThread(t.Context(), ws, domain.ChatThread{ConnectionID: "chat_connection_missing", ChatID: "oc", ResourceType: "ask", ResourceID: "ask_2", RootMessageID: "om_root_3"}); err == nil {
		t.Fatal("thread stored for an unknown connection")
	}
	if err := f.s.store.DeleteChatIntegration(t.Context(), ws, conn); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.store.ChatThread(t.Context(), ws, conn, "oc_group_test", "ask", "ask_1"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("disconnect kept threads: %v", err)
	}
}
