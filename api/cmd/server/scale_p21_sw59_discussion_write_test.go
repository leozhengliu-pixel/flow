package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

type scaleP21SW59Content struct {
	Kind, Resource, ID, CreatedAt, Owner, Parent, Status, NextAttemptAt string
	Data                                                                json.RawMessage
}

type scaleP21SW59Rows struct {
	Issues        []any
	Content       []scaleP21SW59Content
	Subscriptions []any
}

type scaleP21SW59Outcome struct {
	Code     int
	Header   http.Header
	Response json.RawMessage
	Rows     scaleP21SW59Rows
	Events   []domain.DomainEvent
	Webhooks []any
	Realtime []any
}

type scaleP21SW59ResponseWriter struct {
	*httptest.ResponseRecorder
	t           *testing.T
	source      *store.SQLiteStore
	requireHeld bool
	sampleWork  func()
}

func (w *scaleP21SW59ResponseWriter) checkOwner() {
	w.t.Helper()
	if w.sampleWork != nil {
		w.sampleWork()
	}
	if w.requireHeld && w.source.BoundedMetadataEnabled() && w.source.MetadataWorkUsedForTesting() == 0 {
		w.t.Error("native discussion response lost output ownership before writing")
	}
}

func (w *scaleP21SW59ResponseWriter) WriteHeader(code int) {
	w.checkOwner()
	w.ResponseRecorder.WriteHeader(code)
}

func (w *scaleP21SW59ResponseWriter) Write(body []byte) (int, error) {
	w.checkOwner()
	return w.ResponseRecorder.Write(body)
}

func scaleP21SW59SQL(t *testing.T, f scaleP21SW10Fixture) scaleP21SW59Rows {
	t.Helper()
	workspace := f.data.Workspace.URLKey
	result := scaleP21SW59Rows{}
	rows := f.reader.query(t, `SELECT id,version,collection_order,data FROM issue_records WHERE workspace_key=? ORDER BY collection_order,id`, workspace)
	for rows.Next() {
		var id string
		var version, order int64
		var raw []byte
		if err := rows.Scan(&id, &version, &order, &raw); err != nil {
			t.Fatal(err)
		}
		result.Issues = append(result.Issues, []any{id, version, order, json.RawMessage(raw)})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	// Compare the SQL business records and their lookup columns. Storage CAS
	// revisions are not part of the response or publication contract.
	rows = f.reader.query(t, `SELECT kind,resource_id,id,created_at,owner_id,parent_id,status,next_attempt_at,data FROM workspace_content_records WHERE workspace_key=? ORDER BY kind,resource_id,created_at,id`, workspace)
	for rows.Next() {
		var item scaleP21SW59Content
		var raw []byte
		if err := rows.Scan(&item.Kind, &item.Resource, &item.ID, &item.CreatedAt, &item.Owner, &item.Parent, &item.Status, &item.NextAttemptAt, &raw); err != nil {
			t.Fatal(err)
		}
		item.Data = json.RawMessage(raw)
		result.Content = append(result.Content, item)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	rows = f.reader.query(t, `SELECT record_key,data FROM workspace_metadata_records WHERE workspace_key=? AND field='threadSubscriptions' ORDER BY record_key`, workspace)
	for rows.Next() {
		var key string
		var raw []byte
		if err := rows.Scan(&key, &raw); err != nil {
			t.Fatal(err)
		}
		result.Subscriptions = append(result.Subscriptions, []any{key, json.RawMessage(raw)})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	return result
}

func scaleP21SW59Normalized(t *testing.T, result scaleP21SW59Outcome, before scaleP21SW59Rows, started, finished time.Time) any {
	t.Helper()
	existing := map[string]bool{}
	for _, row := range before.Content {
		existing[row.Kind+"\n"+row.ID] = true
	}
	ids := map[string]string{}
	recipientIDs := map[string]bool{}
	activities := 0
	for _, row := range result.Rows.Content {
		if row.Kind == "activity" && !existing[row.Kind+"\n"+row.ID] {
			ids[row.ID] = fmt.Sprintf("<generated-activity:%d>", activities)
			activities++
		}
	}
	for index, event := range result.Events {
		ids[event.ID] = fmt.Sprintf("<generated-event:%d>", index)
	}
	for _, row := range result.Rows.Content {
		if row.Kind != "notification" || existing[row.Kind+"\n"+row.ID] {
			continue
		}
		var item domain.Notification
		if err := json.Unmarshal(row.Data, &item); err != nil {
			t.Fatal(err)
		}
		ids[row.ID] = "<generated-notification:" + item.RecipientID + ":" + item.GroupKey + ":" + item.Category + ">"
		recipientIDs[row.ID] = true
	}
	for _, row := range result.Rows.Content {
		if row.Kind != "delivery" || existing[row.Kind+"\n"+row.ID] {
			continue
		}
		var item domain.NotificationDelivery
		if err := json.Unmarshal(row.Data, &item); err != nil {
			t.Fatal(err)
		}
		notification := item.NotificationID
		if normalized, found := ids[notification]; found {
			notification = normalized
		}
		ids[row.ID] = "<generated-delivery:" + notification + ":" + item.Channel + ">"
		recipientIDs[row.ID] = true
	}
	for _, raw := range result.Realtime {
		var event domain.RealtimeEvent
		if err := json.Unmarshal(raw.(json.RawMessage), &event); err != nil {
			t.Fatal(err)
		}
		if event.Type == "notification.created" {
			notification := event.AggregateID
			if normalized, found := ids[notification]; found {
				notification = normalized
			}
			ids[event.ID] = "<generated-notification-event:" + notification + ">"
		}
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	var normalize func(any, string) any
	normalize = func(value any, field string) any {
		switch item := value.(type) {
		case string:
			if normalized, found := ids[item]; found {
				return normalized
			}
			switch strings.ToLower(field) {
			case "createdat", "updatedat", "editedat", "nextattemptat":
				if clock, err := time.Parse(time.RFC3339Nano, item); err == nil && !clock.Before(started) && !clock.After(finished) {
					return "<generated-clock>"
				}
			}
		case map[string]any:
			for key, child := range item {
				item[key] = normalize(child, key)
			}
		case []any:
			// Only newly generated recipient effects are unordered: the
			// legacy callback appends them while ranging over its audience map.
			// Existing entries and every other array keep their exact positions.
			var positions []int
			for index, child := range item {
				if field == "notifications" || field == "notificationDeliveries" {
					if object, ok := child.(map[string]any); ok {
						id, _ := object["id"].(string)
						if recipientIDs[id] {
							positions = append(positions, index)
						}
					}
				}
				item[index] = normalize(child, field)
			}
			unordered := make([]any, 0, len(positions))
			for _, index := range positions {
				unordered = append(unordered, item[index])
			}
			sort.SliceStable(unordered, func(i, j int) bool {
				a, _ := json.Marshal(unordered[i])
				b, _ := json.Marshal(unordered[j])
				return bytes.Compare(a, b) < 0
			})
			for index, position := range positions {
				item[position] = unordered[index]
			}
		}
		return value
	}
	normalized := normalize(value, "")
	// SQL rows are a set. Their query tie-breaker contains independently
	// generated IDs; compare by normalized business identity after mapping.
	// Responses and publication envelope arrays retain their own order.
	rows := normalized.(map[string]any)["Rows"].(map[string]any)
	if content, ok := rows["Content"].([]any); ok {
		sort.SliceStable(content, func(i, j int) bool {
			left, right := content[i].(map[string]any), content[j].(map[string]any)
			for _, field := range []string{"Kind", "Resource", "ID"} {
				a, b := left[field].(string), right[field].(string)
				if a != b {
					return a < b
				}
			}
			return false
		})
	}
	return normalized
}

func TestScaleP21SW59NativeDiscussionWriteDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21SW10Setup(t)
	repo, workspace := f.repository, f.data.Workspace.URLKey
	if err := f.service.stopDeliveryScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	prefix := idgen.New("sw59-discussion-")
	metadata, found := repo.WorkspaceMetadata(workspace)
	if !found {
		t.Fatal("discussion fixture workspace missing")
	}
	privateTeam := ""
	for _, project := range metadata.Projects {
		if project.ID == "m2-unrelated" {
			privateTeam = project.TeamIDs[0]
		}
	}
	if privateTeam == "" {
		t.Fatal("discussion fixture private team missing")
	}
	privateAdmin := authClient(t)
	authRequest[domain.AuthSession](t, privateAdmin, http.MethodPost, f.host.URL+"/api/auth/login",
		map[string]string{"email": f.principals[1].user.Email, "password": "p03-fixture-password"}, "", http.StatusOK)
	public := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records",
		map[string]any{"title": "SW59 public discussion", "teamId": f.public}, workspace, http.StatusCreated)
	parent := authRequest[domain.Issue](t, privateAdmin, http.MethodPost, f.host.URL+"/api/issue-records",
		map[string]any{"title": "SW59 grant parent", "teamId": privateTeam}, workspace, http.StatusCreated)
	child := authRequest[domain.Issue](t, privateAdmin, http.MethodPost, f.host.URL+"/api/issue-records",
		map[string]any{"title": "SW59 inherited discussion", "teamId": privateTeam, "parentId": parent.ID}, workspace, http.StatusCreated)
	_, former := scaleP03Join(t, f.scaleP03PlatformFixture, "SW59 former", prefix+"former@example.test", "member", f.public)
	if err := repo.SuspendMember(t.Context(), f.data.Workspace.ID, former.ID); err != nil {
		t.Fatal(err)
	}
	owner, member, guest, muted := f.principals[0].user, f.principals[2].user, f.principals[3].user, f.principals[4].user
	sessions := map[string]string{}
	for _, principal := range f.principals {
		_, token, err := repo.Login(t.Context(), principal.user.Email, "p03-fixture-password")
		if err != nil {
			t.Fatal("create real discussion session", err)
		}
		sessions[principal.user.ID] = token
	}
	stamp := time.Now().UTC().Add(-time.Minute)
	mention := func(user domain.User) map[string]any {
		return map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "content": []any{
			map[string]any{"type": "userMention", "attrs": map[string]any{"userId": user.ID, "label": user.Name}},
		}}}}
	}
	nativeBody := func() map[string]any {
		return map[string]any{"type": "doc", "nativeInteger": int64(9007199254740993), "content": []any{}}
	}
	type testCase struct {
		name, operation, credential string
		actor                       int
		private, alias, revoked     bool
		replay, noSummary           bool
		code, events                int
	}
	tests := []testCase{
		{name: "admin-create-mention", operation: "create", actor: 0, credential: "session", code: http.StatusCreated, events: 1},
		{name: "selected-empty-refused", operation: "create", actor: 0, credential: "selected-empty", code: http.StatusForbidden},
		{name: "member-reply-thread-mute", operation: "reply", actor: 2, credential: "unrestricted", code: http.StatusCreated, events: 1},
		{name: "guest-author-update", operation: "update", actor: 3, credential: "session", code: http.StatusOK, events: 1},
		{name: "guest-cannot-edit-other", operation: "update-other", actor: 3, credential: "session", code: http.StatusForbidden},
		{name: "admin-delete-thread", operation: "delete", actor: 0, credential: "session", code: http.StatusNoContent, events: 1},
		{name: "reply-author-delete-selected", operation: "delete-reply", actor: 4, credential: "selected-set", code: http.StatusNoContent, events: 1},
		{name: "author-resolve-summary", operation: "resolve", actor: 3, credential: "session", code: http.StatusOK, events: 1},
		{name: "author-resolve-without-summary", operation: "resolve", actor: 3, credential: "session", noSummary: true, code: http.StatusOK, events: 1},
		{name: "admin-reopen", operation: "reopen", actor: 0, credential: "unrestricted", code: http.StatusOK, events: 1},
		{name: "author-unchanged", operation: "unchanged", actor: 3, credential: "session", code: http.StatusOK},
		{name: "author-version-conflict", operation: "conflict", actor: 3, credential: "session", code: http.StatusConflict},
		{name: "member-comment-reaction-add", operation: "comment-reaction", actor: 2, credential: "selected-set", code: http.StatusOK, events: 1},
		{name: "guest-comment-reaction-remove-duplicates", operation: "comment-reaction", actor: 3, credential: "session", code: http.StatusOK, events: 1},
		{name: "private-team-member-reaction", operation: "comment-reaction", actor: 4, credential: "unrestricted", private: true, code: http.StatusOK, events: 1},
		{name: "private-outsider-reaction-refused", operation: "comment-reaction", actor: 2, credential: "session", private: true, code: http.StatusForbidden},
		{name: "inherited-commenter-create", operation: "create", actor: 3, credential: "session", private: true, code: http.StatusCreated, events: 1},
		{name: "queued-grant-revoked", operation: "create", actor: 3, credential: "session", private: true, revoked: true, code: http.StatusForbidden},
		{name: "replayed-grant-revoked", operation: "create", actor: 3, credential: "session", private: true, revoked: true, replay: true, code: http.StatusForbidden},
		{name: "new-former-mention-refused", operation: "former-create", actor: 0, credential: "session", code: http.StatusBadRequest},
		{name: "existing-former-mention-kept", operation: "former-update", actor: 3, credential: "session", code: http.StatusOK, events: 1},
		{name: "deterministic-create-retry", operation: "retry", actor: 0, credential: "session", code: http.StatusCreated},
		{name: "previous-identifier-create", operation: "create", actor: 0, credential: "session", alias: true, code: http.StatusCreated, events: 1},
	}
	reset := func(test testCase, reload bool) {
		t.Helper()
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			source.SetWebhookSink(nil)
			source.SetRealtimeSink(nil)
		}
		if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
		// Seed normalized defaults as canonical rows before comparing writers.
		err := func() error {
			restore := store.ForceFullMutationsForTesting(true)
			defer restore()
			return repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw59_fixture", "", nil, func(data *domain.Bootstrap) error {
				for index := range data.Issues {
					switch data.Issues[index].ID {
					case public.ID:
						issue := public
						issue.Creator = owner
						issue.PreviousIdentifiers = []string{"SWOLD-59001", "SWOLD-59001"}
						issue.SubscriberIDs = []string{owner.ID, member.ID, muted.ID, former.ID, member.ID}
						issue.Permissions = []domain.IssuePermission{{ID: prefix + "public-grant", IssueID: public.ID, SubjectType: "user", SubjectID: guest.ID, Role: "commenter", CreatedAt: stamp, UpdatedAt: stamp}}
						issue.Reactions = map[string][]string{"like": {guest.ID, guest.ID}}
						issue.DocumentContent = &domain.DocumentContent{ID: prefix + "document", ContentData: nativeBody()}
						data.Issues[index] = issue
					case parent.ID:
						issue := parent
						issue.Permissions = []domain.IssuePermission{{ID: prefix + "parent-grant", IssueID: parent.ID, SubjectType: "user", SubjectID: guest.ID, Role: "commenter", CreatedAt: stamp, UpdatedAt: stamp}}
						data.Issues[index] = issue
					case child.ID:
						issue := child
						issue.SubscriberIDs = []string{owner.ID, member.ID, muted.ID, former.ID}
						issue.Reactions = map[string][]string{"like": {guest.ID, guest.ID}}
						data.Issues[index] = issue
					}
				}
				data.ThreadSubscriptions = nil
				for _, id := range []string{public.ID, child.ID} {
					rootID := id + "-root"
					reactions := map[string][]string{"like": {guest.ID, guest.ID}}
					root := domain.Comment{ID: rootID, Version: 3, Body: "Original root", BodyData: nativeBody(), Reactions: reactions, User: guest, CreatedAt: stamp}
					if test.operation == "reopen" {
						root.Resolved, root.ThreadSummary = true, &domain.CommentThreadSummary{Content: "Existing exact summary"}
					}
					data.Comments[id] = []domain.Comment{
						root,
						{ID: id + "-reply", Version: 2, Body: "Original reply", ParentID: &rootID, Reactions: reactions, User: muted, CreatedAt: stamp.Add(time.Second)},
						{ID: id + "-former", Version: 4, Body: "Retained former mention", BodyData: mention(former), Reactions: nil, User: guest, CreatedAt: stamp.Add(2 * time.Second)},
					}
					data.Activities[id] = []domain.ActivityEvent{}
					data.ThreadSubscriptions = append(data.ThreadSubscriptions,
						domain.ThreadSubscription{ID: id + "-mute", UserID: muted.ID, IssueID: id, CommentID: rootID, State: "muted", CreatedAt: stamp, UpdatedAt: stamp},
						domain.ThreadSubscription{ID: id + "-watch", UserID: member.ID, IssueID: id, CommentID: rootID, State: "subscribed", CreatedAt: stamp, UpdatedAt: stamp})
				}
				for _, teamID := range []string{f.public, privateTeam} {
					settings := data.TeamSettings[teamID]
					settings.ResolvedSummaries = !test.noSummary
					data.TeamSettings[teamID] = settings
				}
				channel := domain.NotificationChannelPreferences{Enabled: true, Categories: map[string]bool{"comments": true, "mentions": true}}
				for _, user := range []domain.User{owner, member, guest, muted, former} {
					data.NotificationPreferences[user.ID] = domain.NotificationPreferences{UserID: user.ID, Inbox: channel, Desktop: channel, Chat: &channel, EmailFormat: "immediate", UpdatedAt: stamp}
				}
				id := prefix + "existing-notification"
				data.Notifications = []domain.Notification{{ID: id, RecipientID: owner.ID, IssueID: public.ID, CommentID: public.ID + "-root",
					ActivityID: prefix + "old-activity", Actor: muted, Type: "comment", Category: "comments", GroupKey: owner.ID + ":" + public.ID + ":comments",
					SourceType: "comment", SourceID: public.ID + "-root", OccurrenceCount: 2, LatestActorIDs: []string{muted.ID, muted.ID}, CreatedAt: stamp, UpdatedAt: stamp}}
				data.NotificationDeliveries = []domain.NotificationDelivery{{ID: "delivery_" + id + "_chat", NotificationID: id, RecipientID: owner.ID, Channel: "chat", Status: "pending", CreatedAt: stamp, UpdatedAt: stamp}}
				return nil
			})
		}()
		if err != nil {
			t.Fatal(err)
		}
		preferences, found := repo.WorkspaceMetadataFields(workspace, "notificationPreferences")
		if !found {
			t.Fatal("seeded discussion preferences missing")
		}
		var keys []store.RecordKey
		for _, user := range []domain.User{owner, member, guest, muted, former} {
			preference, found := preferences.NotificationPreferences[user.ID]
			if !found || preference.Chat == nil || !preference.Chat.Enabled || !preference.Desktop.Enabled || preference.Email.Enabled || preference.EmailFormat != "immediate" {
				t.Fatal("fixture did not install configured discussion preferences", user.ID)
			}
			keys = append(keys, store.RecordKey{Kind: "notificationPreferences", ID: user.ID})
		}
		canonical, err := repo.ReadCanonicalRecords(t.Context(), workspace, keys, "detail")
		if err != nil {
			t.Fatal(err)
		}
		if len(canonical.Items) != len(keys) {
			t.Fatal("fixture preference precondition omitted requested canonical rows")
		}
		for _, record := range canonical.Items {
			var preference domain.NotificationPreferences
			if record.Missing || record.Omitted || json.Unmarshal(record.Data, &preference) != nil ||
				preference.Chat == nil || !preference.Chat.Enabled || !preference.Desktop.Enabled || preference.Email.Enabled || preference.EmailFormat != "immediate" {
				t.Fatal("fixture did not persist configured discussion preferences", record.Key)
			}
		}
		if reload {
			if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
	}
	for _, reload := range []bool{false, true} {
		for _, test := range tests {
			t.Run(fmt.Sprintf("reload=%t/%s", reload, test.name), func(t *testing.T) {
				var outcomes []any
				var baselines []scaleP21SW59Rows
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					reset(test, reload)
					target := public.ID
					if test.private {
						target = child.ID
					}
					var key *domain.APIKey
					for _, credential := range scaleP21M3Credentials() {
						if credential.name == test.credential {
							key = credential.key
							if key != nil && test.credential == "selected-set" {
								key.TeamIDs = []string{f.public}
							}
						}
					}
					user := f.principals[test.actor].user
					method, tail, commentID := http.MethodPost, "/comments", ""
					var body any = map[string]any{"body": "Created exact comment", "bodyData": mention(owner)}
					handler := (*server).createComment
					switch test.operation {
					case "reply":
						body = map[string]any{"body": "Exact thread reply", "parentId": target + "-root"}
					case "update", "update-other", "unchanged", "conflict", "resolve", "reopen", "former-update":
						method, commentID, handler = http.MethodPatch, target+"-root", (*server).updateComment
						body = map[string]any{"body": "Edited exact root", "expectedVersion": 3}
						switch test.operation {
						case "update-other":
							commentID = target + "-reply"
						case "unchanged":
							body = map[string]any{"resolved": false, "expectedVersion": 3}
						case "conflict":
							body = map[string]any{"body": "Stale edit", "expectedVersion": 99}
						case "resolve", "reopen":
							body = map[string]any{"resolved": test.operation == "resolve", "expectedVersion": 3}
						case "former-update":
							commentID = target + "-former"
							body = map[string]any{"body": "Edited retained mention", "bodyData": mention(former), "expectedVersion": 4}
						}
					case "delete", "delete-reply":
						method, commentID, handler, body = http.MethodDelete, target+"-root", (*server).deleteComment, nil
						if test.operation == "delete-reply" {
							commentID = target + "-reply"
						}
					case "comment-reaction":
						commentID, handler, body = target+"-root", (*server).toggleCommentReaction, map[string]any{"emoji": "like"}
					case "former-create":
						body = map[string]any{"body": "Refused new former mention", "bodyData": mention(former)}
					}
					if commentID != "" {
						tail += "/" + commentID
						if test.operation == "comment-reaction" {
							tail += "/reactions"
						}
					}
					makeRequest := func() *http.Request {
						r := f.literalRequest(user, key, body)
						if key == nil {
							r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessions[user.ID]})
						}
						ref := target
						if test.alias {
							ref = "swold-59001"
						}
						r.Method, r.URL.Path = method, "/api/issue-records/"+ref+tail
						r.Header.Set("Content-Type", "application/json")
						creationKey := prefix + "created"
						if test.operation == "retry" {
							creationKey = target + "-root"
						}
						return r.WithContext(store.WithCommentCreationKey(r.Context(), creationKey))
					}
					r := makeRequest()
					if test.revoked {
						if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "issues"), workspace, "scale_p21.sw59_revoke", parent.ID, nil, func(data *domain.Bootstrap) error {
							for index := range data.Issues {
								if data.Issues[index].ID == parent.ID {
									data.Issues[index].Permissions = nil
								}
							}
							return nil
						}); err != nil {
							t.Fatal(err)
						}
						if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
							t.Fatal(err)
						}
						if test.replay {
							r = makeRequest()
						}
					}
					before := scaleP21SW59SQL(t, f)
					baselines = append(baselines, before)
					prior, err := repo.Events(t.Context(), target)
					if err != nil {
						t.Fatal(err)
					}
					priorIDs := map[string]bool{}
					for _, event := range prior {
						priorIDs[event.ID] = true
					}
					got := scaleP21SW59Outcome{}
					statements, observedPeakWork := 0, 0
					sampleWork := func() { observedPeakWork = max(observedPeakWork, source.MetadataWorkUsedForTesting()) }
					source.SetWebhookSink(func(_ string, event domain.DomainEvent) {
						sampleWork()
						if source.BoundedMetadataEnabled() && source.MetadataWorkUsedForTesting() == 0 {
							t.Error("native discussion webhook lost ownership before publication")
						}
						raw, err := json.Marshal(map[string]any{
							"id": event.ID, "type": event.Type, "aggregateId": event.AggregateID, "actorId": event.ActorID,
							"payload": event.Payload, "previousValues": event.PreviousValues, "snapshot": event.Snapshot, "createdAt": event.CreatedAt,
						})
						if err != nil {
							t.Fatal(err)
						}
						got.Webhooks = append(got.Webhooks, json.RawMessage(raw))
					})
					captureRealtime := func(_ string, event domain.RealtimeEvent) {
						sampleWork()
						if source.BoundedMetadataEnabled() && source.MetadataWorkUsedForTesting() == 0 {
							t.Error("native discussion realtime output lost ownership before publication")
						}
						raw, err := json.Marshal(map[string]any{
							"id": event.ID, "type": event.Type, "aggregateId": event.AggregateID, "actorId": event.ActorID,
							"seq": event.Seq, "clientId": event.ClientID, "payload": event.Payload, "createdAt": event.CreatedAt,
						})
						if err != nil {
							t.Fatal(err)
						}
						got.Realtime = append(got.Realtime, json.RawMessage(raw))
					}
					source.SetRealtimeSink(captureRealtime)
					if source != repo {
						// The shared SQL fixture's committed-notification callback
						// belongs to its original store, while the writer uses source.
						repo.SetRealtimeSink(captureRealtime)
					}
					r = r.WithContext(store.WithSQLStatementObserver(r.Context(), func(string) {
						statements++
						sampleWork()
					}))
					started := time.Now().UTC()
					func() {
						r, closeOutput := beginRequestResourceReads(r)
						defer closeOutput()
						restore := store.ForceFullMutationsForTesting(source == repo)
						defer restore()
						service := &server{store: source}
						w := &scaleP21SW59ResponseWriter{
							ResponseRecorder: httptest.NewRecorder(), t: t, source: source,
							requireHeld: test.code < http.StatusBadRequest || test.code == http.StatusConflict,
							sampleWork:  sampleWork,
						}
						mux := http.NewServeMux()
						pattern := "/api/issue-records/{id}/comments"
						if commentID != "" {
							pattern += "/{commentId}"
							if test.operation == "comment-reaction" {
								pattern += "/reactions"
							}
						}
						mux.HandleFunc(method+" "+pattern, service.issueRecordAlias(func(w http.ResponseWriter, r *http.Request) { handler(service, w, r) }))
						if key == nil {
							service.authenticate(mux).ServeHTTP(w, r)
						} else if service.authorizeWorkspaceRequest(w, r, user) {
							mux.ServeHTTP(w, r)
						}
						got.Code = w.Code
						got.Header = w.Header().Clone()
						if w.Body.Len() != 0 {
							got.Response = slices.Clone(w.Body.Bytes())
						}
					}()
					source.SetWebhookSink(nil)
					source.SetRealtimeSink(nil)
					repo.SetRealtimeSink(nil)
					t.Logf("bounded=%t request_SQL=%d observed_peak_work=%d", source.BoundedMetadataEnabled(), statements, observedPeakWork)
					got.Rows = scaleP21SW59SQL(t, f)
					events, err := repo.Events(t.Context(), target)
					if err != nil {
						t.Fatal(err)
					}
					for _, event := range events {
						if !priorIDs[event.ID] {
							got.Events = append(got.Events, event)
						}
					}
					primaryIDs := map[string]bool{}
					for _, event := range got.Events {
						primaryIDs[event.ID] = true
					}
					primaryRealtime := 0
					for _, raw := range got.Realtime {
						var event domain.RealtimeEvent
						if err := json.Unmarshal(raw.(json.RawMessage), &event); err != nil {
							t.Fatal(err)
						}
						if primaryIDs[event.ID] {
							primaryRealtime++
						}
					}
					if got.Code != test.code || len(got.Events) != test.events || len(got.Webhooks) != test.events || primaryRealtime != test.events || test.events == 0 && len(got.Realtime) != 0 {
						t.Fatalf("actual native discussion write status=%d want=%d primary events=%d/%d/%d want=%d total realtime=%d response=%s", got.Code, test.code, len(got.Events), len(got.Webhooks), primaryRealtime, test.events, len(got.Realtime), got.Response)
					}
					if test.events == 0 && !reflect.DeepEqual(before, got.Rows) {
						t.Fatal("refused, unchanged or retried discussion write changed SQL business rows")
					}
					for _, row := range got.Rows.Content {
						if row.Kind == "notification" && (row.Owner == former.ID || test.operation == "reply" && row.Owner == muted.ID) {
							t.Fatal("discussion notified a former member or muted thread participant")
						}
					}
					if source == f.probe && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
						t.Fatal("native discussion write leaked work or decoded complete workspace metadata")
					}
					outcomes = append(outcomes, scaleP21SW59Normalized(t, got, before, started, time.Now().UTC()))
				}
				if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
					if !reflect.DeepEqual(baselines[0], baselines[1]) {
						t.Log("fixture reset SQL preimages differ", scaleP21M2Diff(baselines[0], baselines[1]))
					}
					t.Fatal("actual native discussion response/SQL/publication differential", scaleP21M2Diff(outcomes[0], outcomes[1]))
				}
			})
		}
	}
}
