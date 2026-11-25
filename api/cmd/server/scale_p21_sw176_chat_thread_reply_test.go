package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW176ChatThreadReplyDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "off")
	f, database, issue := scaleP21SW44ChatFixture(t)
	repo, workspace := database.repo, database.key
	prefix := idgen.New("sw176-")
	actor := database.register(t, "Thread reply member")
	if err := repo.EnsureWorkspaceMembership(t.Context(), database.workspaceID, actor.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetTeamMembership(t.Context(), database.workspaceID, issue.Team.ID, actor.ID, "member", true); err != nil {
		t.Fatal(err)
	}
	baseIdentity := domain.ChatIdentity{
		UserID: actor.ID, OpenID: prefix + "open", TenantKey: f.record.TenantKey,
		AuthProvider: "password", AuthenticatedAt: time.Now().UTC(),
	}
	ownerID := f.identity.UserID
	f.identity = baseIdentity
	baseline, found := repo.BootstrapFor(workspace)
	if !found {
		t.Fatal("owned chat-thread fixture workspace missing")
	}
	saved, err := json.Marshal(baseline)
	if err != nil {
		t.Fatal(err)
	}
	probe, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	sqlFixture := scaleP21SW10Fixture{reader: database}
	sqlFixture.data.Workspace.URLKey = workspace
	platform := f.threadPlatform(t)
	rootMessage := prefix + "root"
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	threadStamp := time.Now().UTC().Add(-time.Hour)
	tests := []struct {
		name, mode, message string
		comments, posts     int
		replay              bool
	}{
		{name: "member-comment", comments: 1},
		{name: "private-team-member", mode: "private", comments: 1},
		{name: "public-viewer-role-denied", mode: "viewer", posts: 1, message: "does not have permission to comment"},
		{name: "private-team-outsider-denied", mode: "private-outsider", posts: 1, message: "no longer have access to this issue"},
		{name: "missing-consent-provider", mode: "provider", posts: 1, message: chatReconnectMessage},
		{name: "expired-consent", mode: "expired", posts: 1, message: chatReconnectMessage},
		{name: "membership-revoked", mode: "suspended", posts: 1, message: "workspace access has changed"},
		{name: "binding-revoked", mode: "revoked"},
		{name: "binding-updates-disabled", mode: "updates-off"},
		{name: "explicit-issue-sharing-outside-group-scope", mode: "shared", posts: 1, message: "no longer available in this group scope"},
		{name: "unlinked-member", mode: "unlinked", posts: 1, message: "Connect your Flow account"},
		{name: "missing-thread-root", mode: "missing"},
		{name: "redelivered-message-no-duplicate", comments: 1, replay: true},
	}
	pairs := 0
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			outcomes := []any{}
			for _, source := range []*store.SQLiteStore{repo, probe} {
				for _, resetSource := range []*store.SQLiteStore{repo, probe} {
					resetSource.SetWebhookSink(nil)
					resetSource.SetRealtimeSink(nil)
				}
				restore := store.ForceFullMutationsForTesting(true)
				err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw176_fixture", "", nil, func(data *domain.Bootstrap) error {
					var restored domain.Bootstrap
					if err := json.Unmarshal(saved, &restored); err != nil {
						return err
					}
					*data = restored
					private := test.mode == "private" || test.mode == "private-outsider"
					for index := range data.Teams {
						if data.Teams[index].ID == issue.Team.ID {
							data.Teams[index].Private = private
						}
					}
					settings := teamSettings(data, issue.Team.ID)
					settings.Access = "public"
					if private {
						settings.Access = "private"
					}
					data.TeamSettings[issue.Team.ID] = settings
					for index := range data.Issues {
						if data.Issues[index].ID == issue.ID {
							target := &data.Issues[index]
							target.Team.Private, target.Permissions, target.ArchivedAt = private, nil, nil
							if test.mode == "shared" {
								target.Permissions = []domain.IssuePermission{{
									ID: prefix + "grant", IssueID: issue.ID, SubjectType: "user", SubjectID: actor.ID,
									Role: "commenter", CreatedAt: stamp, UpdatedAt: stamp,
								}}
							}
						}
					}
					return nil
				})
				restore()
				if err != nil {
					t.Fatal(err)
				}
				if err := repo.EnsureWorkspaceMembership(t.Context(), database.workspaceID, actor.ID); err != nil {
					t.Fatal(err)
				}
				onTeam := test.mode != "viewer" && test.mode != "private-outsider"
				if err := repo.SetTeamMembership(t.Context(), database.workspaceID, issue.Team.ID, actor.ID, "member", onTeam); err != nil {
					t.Fatal(err)
				}
				if test.mode == "suspended" {
					if err := repo.SuspendMember(t.Context(), database.workspaceID, actor.ID); err != nil {
						t.Fatal(err)
					}
				}
				binding := f.binding
				binding.AllowUpdate, binding.RevokedAt, binding.Authorization = true, nil, nil
				if test.mode == "private" || test.mode == "private-outsider" {
					binding.Authorization = &domain.ChatBindingAuthorization{UserID: ownerID, Role: "team_owner", AuthorizedAt: stamp, PrivateTeam: true}
				}
				if test.mode == "revoked" {
					binding.RevokedAt = &stamp
				}
				if test.mode == "updates-off" {
					binding.AllowUpdate = false
				}
				if err := repo.PutChatBinding(t.Context(), workspace, binding); err != nil {
					t.Fatal(err)
				}
				identity := baseIdentity
				if test.mode == "provider" {
					identity.AuthProvider = ""
				}
				if test.mode == "expired" {
					identity.AuthenticatedAt = stamp
				}
				if err := repo.PutChatIdentity(t.Context(), workspace, f.record.ConnectionID, identity); err != nil {
					t.Fatal(err)
				}
				thread, err := repo.PutChatThread(t.Context(), workspace, domain.ChatThread{
					ConnectionID: f.record.ConnectionID, ChatID: binding.ChatID, BindingID: binding.ID,
					ResourceType: domain.ChatThreadResourceIssue, ResourceID: issue.ID,
					RootMessageID: rootMessage, CreatedAt: threadStamp, SyncFrom: threadStamp,
				})
				if err != nil {
					t.Fatal(err)
				}
				items, err := repo.ChatThreadItems(t.Context(), workspace, f.record.ConnectionID, thread.ID)
				if err != nil {
					t.Fatal(err)
				}
				for _, item := range items {
					if err := repo.DeleteChatThreadItem(t.Context(), workspace, f.record.ConnectionID, item.ID); err != nil {
						t.Fatal(err)
					}
				}
				if err := probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
					t.Fatal(err)
				}
				if routed, err := source.ChatThreadForRoot(t.Context(), workspace, f.record.ConnectionID, rootMessage); err != nil ||
					routed.ID != thread.ID || routed.ChatID != binding.ChatID || routed.ResourceType != domain.ChatThreadResourceIssue {
					t.Fatal("actual reply entry cannot route the fixture's retained issue thread", routed, err)
				}
				f.s.store = source
				f.sent, f.patched, f.responseIDs = nil, nil, map[string]string{}
				platform.calls = nil
				before := scaleP21SW59SQL(t, sqlFixture)
				prior, err := repo.Events(t.Context(), issue.ID)
				if err != nil {
					t.Fatal(err)
				}
				priorIDs := map[string]bool{}
				for _, event := range prior {
					priorIDs[event.ID] = true
				}
				got := scaleP21SW59Outcome{}
				source.SetWebhookSink(func(_ string, event domain.DomainEvent) {
					if event.ActorID != actor.ID || len(event.Snapshot) == 0 {
						t.Error("actual comment webhook lost the linked actor or full snapshot")
					}
					got.Webhooks = append(got.Webhooks, scaleP21SW106Envelope(t, map[string]any{
						"id": event.ID, "type": event.Type, "aggregateId": event.AggregateID, "actorId": event.ActorID,
						"payload": event.Payload, "previousValues": event.PreviousValues, "snapshot": event.Snapshot, "createdAt": event.CreatedAt,
					}))
				})
				captureRealtime := func(_ string, event domain.RealtimeEvent) {
					got.Realtime = append(got.Realtime, scaleP21SW106Envelope(t, event))
				}
				source.SetRealtimeSink(captureRealtime)
				if source != repo {
					repo.SetRealtimeSink(captureRealtime)
				}
				command := chatInboundCommand{
					Kind: chatThreadReplyKind, OpenID: identity.OpenID, ChatID: binding.ChatID,
					MessageID: prefix + test.name, RootMessageID: rootMessage, Text: "Keep the exact thread reply **body**.",
				}
				if test.mode == "unlinked" {
					command.OpenID = prefix + "unlinked"
				}
				if test.mode == "missing" {
					command.RootMessageID = prefix + "missing-root"
				}
				job := domain.ChatJob{ID: prefix + "job", WorkspaceKey: workspace, ConnectionID: f.record.ConnectionID, Kind: "inbound", DedupKey: prefix + test.name}
				started := time.Now().UTC()
				statements := 0
				func() {
					ctx, closeOutput := beginMetadataOutputReads(t.Context())
					defer closeOutput()
					if test.name == "member-comment" {
						ctx = store.WithSQLStatementObserver(ctx, func(string) { statements++ })
					}
					if err := f.s.processChatThreadReply(ctx, f.record, job, command); err != nil {
						t.Fatal("actual thread reply refused its independent header/point-read/comment-writer boundary", err)
					}
					if test.replay {
						if err := f.s.processChatThreadReply(ctx, f.record, job, command); err != nil {
							t.Fatal(err)
						}
					}
					if source.BoundedMetadataEnabled() && test.comments > 0 && source.MetadataWorkUsedForTesting() == 0 {
						t.Fatal("successful actual thread reply released output before the enclosing job scope")
					}
				}()
				finished := time.Now().UTC()
				source.SetWebhookSink(nil)
				source.SetRealtimeSink(nil)
				repo.SetRealtimeSink(nil)
				got.Rows = scaleP21SW59SQL(t, sqlFixture)
				after, err := repo.Events(t.Context(), issue.ID)
				if err != nil {
					t.Fatal(err)
				}
				for _, event := range after {
					if !priorIDs[event.ID] {
						if event.Type != "comment.created" || event.AggregateID != issue.ID {
							t.Fatal("thread reply changed actual comment event", event)
						}
						got.Events = append(got.Events, event)
					}
				}
				commentID := "comment_chat_" + chatKey(f.record.ConnectionID, "thread", command.MessageID)
				var imported []domain.Comment
				comments, _, err := repo.ResourceCommentsPage(t.Context(), workspace, issue.ID, "", 100)
				if err != nil {
					t.Fatal(err)
				}
				for _, comment := range comments {
					if comment.ID == commentID {
						if comment.User.ID != actor.ID || comment.Body != command.Text {
							t.Fatal("actual thread comment changed identity or body", comment)
						}
						imported = append(imported, comment)
					}
				}
				items, err = repo.ChatThreadItems(t.Context(), workspace, f.record.ConnectionID, thread.ID)
				if err != nil {
					t.Fatal(err)
				}
				if len(imported) != test.comments || len(got.Events) != test.comments || len(got.Webhooks) != test.comments || len(platform.calls) != test.posts {
					t.Fatal("thread reply branch changed comment/event/platform count", len(imported), len(got.Events), len(got.Webhooks), len(platform.calls), test)
				}
				primaryRealtime := 0
				for _, raw := range got.Realtime {
					var event domain.RealtimeEvent
					if err := json.Unmarshal(raw.(json.RawMessage), &event); err != nil {
						t.Fatal(err)
					}
					for _, committed := range got.Events {
						if event.ID == committed.ID {
							primaryRealtime++
						}
					}
				}
				if primaryRealtime != test.comments || test.comments == 0 && len(got.Realtime) != 0 {
					t.Fatal("actual comment realtime publication changed", primaryRealtime, len(got.Realtime))
				}
				if test.comments > 0 {
					if len(items) != 1 || items[0].ItemID != commentID || items[0].MessageID != command.MessageID ||
						items[0].Origin != domain.ChatThreadOriginChat || items[0].ItemType != chatThreadItemComment || items[0].Version != 1 {
						t.Fatal("thread reply lost its exact no-echo item reservation", items)
					}
				} else if !reflect.DeepEqual(before, got.Rows) {
					t.Fatal("refused or ignored thread reply changed issue/comment business rows")
				}
				if test.mode == "unlinked" {
					if len(items) != 1 || items[0].ItemType != chatThreadItemLinkCard || items[0].ItemID != command.OpenID ||
						items[0].Origin != domain.ChatThreadOriginFlow {
						t.Fatal("unlinked reply lost its exact once-per-thread link-card marker", items)
					}
				} else if test.comments == 0 && len(items) != 0 {
					t.Fatal("ignored or refused reply retained a comment reservation", items)
				}
				if test.message != "" {
					matched := false
					for _, call := range platform.calls {
						var content struct {
							Text   string `json:"text"`
							Header struct {
								Title struct {
									Content string `json:"content"`
								} `json:"title"`
							} `json:"header"`
						}
						raw, ok := call.body["content"].(string)
						if !ok {
							t.Fatal("actual platform reply lost its encoded message content", call.body)
						}
						if err := json.Unmarshal([]byte(raw), &content); err != nil {
							t.Fatal(err)
						}
						text := content.Text
						if test.mode == "unlinked" {
							text = content.Header.Title.Content
						}
						matched = matched || strings.Contains(text, test.message)
						if test.mode != "unlinked" && call.body["receive_id"] != command.OpenID {
							t.Fatal("thread refusal was not private to the linked member", call.body)
						}
					}
					if !matched {
						t.Fatal("actual platform outcome missed its exact branch", test.message, f.sent)
					}
				}
				calls := []any{}
				for _, call := range platform.calls {
					calls = append(calls, map[string]any{"method": call.method, "path": call.path, "body": call.body})
				}
				got.Response, err = json.Marshal(map[string]any{"comments": imported, "threadItems": items, "platform": calls})
				if err != nil {
					t.Fatal(err)
				}
				if source.BoundedMetadataEnabled() && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
					t.Fatal("thread reply retained output or decoded complete bounded metadata")
				}
				if test.name == "member-comment" {
					t.Logf("actual_thread_reply bounded=%t observed_SQL=%d", source.BoundedMetadataEnabled(), statements)
				}
				outcomes = append(outcomes, scaleP21SW59Normalized(t, got, before, started, finished))
			}
			if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
				t.Fatal("actual thread reply/comment/platform differential", scaleP21M2Diff(outcomes[0], outcomes[1]))
			}
			pairs++
		})
	}
	t.Logf("actual chat thread-reply pairs=%d; successful comment POST and intentional no-echo; private platform failures exact; independent point GET/writer unchanged", pairs)
}
