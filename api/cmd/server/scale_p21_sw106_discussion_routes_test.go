package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// Authorization and aliased point writes have their own differential tables.
// Invoke the real handlers with the harness's authenticated actor context so
// this table reaches the ordinary Tier A writer without an issue-record marker.
func TestScaleP21SW106DiscussionRoutesDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21SW10Setup(t)
	repo, workspace := f.repository, f.data.Workspace.URLKey
	if err := f.service.stopDeliveryScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	owner, member := f.principals[0].user, f.principals[2].user
	issue := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records",
		map[string]any{"title": "SW106 ordinary discussion routes", "teamId": f.public}, workspace, http.StatusCreated)
	prefix := idgen.New("sw106-")
	rootID, replyID := prefix+"root", prefix+"reply"
	stamp := time.Now().UTC().Add(-time.Minute)
	restore := store.ForceFullMutationsForTesting(true)
	err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw106_seed", "", nil, func(data *domain.Bootstrap) error {
		for index := range data.Issues {
			if data.Issues[index].ID == issue.ID {
				data.Issues[index].SubscriberIDs = []string{owner.ID, member.ID}
			}
		}
		data.Comments[issue.ID] = []domain.Comment{
			{ID: rootID, Version: 3, Body: "Original root", BodyData: map[string]any{"type": "doc", "content": []any{}},
				User: member, Reactions: map[string][]string{"like": {owner.ID}}, CreatedAt: stamp},
			{ID: replyID, Version: 1, Body: "Original reply", User: member, ParentID: &rootID, CreatedAt: stamp.Add(time.Second)},
		}
		data.Activities[issue.ID] = []domain.ActivityEvent{}
		data.ThreadSubscriptions = []domain.ThreadSubscription{{
			ID: prefix + "subscription", IssueID: issue.ID, CommentID: rootID, UserID: member.ID,
			State: "subscribed", CreatedAt: stamp, UpdatedAt: stamp,
		}}
		settings := data.TeamSettings[f.public]
		settings.ResolvedSummaries = true
		data.TeamSettings[f.public] = settings
		channel := domain.NotificationChannelPreferences{Enabled: true, Categories: map[string]bool{"comments": true, "mentions": true}}
		for _, user := range []domain.User{owner, member} {
			data.NotificationPreferences[user.ID] = domain.NotificationPreferences{
				UserID: user.ID, Inbox: channel, Desktop: channel, Chat: &channel, EmailFormat: "immediate", UpdatedAt: stamp,
			}
		}
		data.Notifications = []domain.Notification{{
			ID: prefix + "notification", RecipientID: member.ID, IssueID: issue.ID, CommentID: rootID,
			Actor: owner, Category: "comments", Type: "comment", GroupKey: member.ID + ":" + issue.ID + ":comments",
			SourceType: "comment", SourceID: rootID, OccurrenceCount: 2, CreatedAt: stamp, UpdatedAt: stamp,
		}}
		data.NotificationDeliveries = []domain.NotificationDelivery{{
			ID: "delivery_" + prefix + "notification_chat", NotificationID: prefix + "notification",
			RecipientID: member.ID, Channel: "chat", Status: "pending", CreatedAt: stamp, UpdatedAt: stamp,
		}}
		return nil
	})
	restore()
	if err != nil {
		t.Fatal(err)
	}
	baseline, found := repo.BootstrapFor(workspace)
	if !found {
		t.Fatal("ordinary discussion fixture workspace missing")
	}
	saved, err := json.Marshal(baseline)
	if err != nil {
		t.Fatal(err)
	}
	reset := func(t *testing.T, resolved bool) {
		t.Helper()
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			source.SetWebhookSink(nil)
			source.SetRealtimeSink(nil)
		}
		var data domain.Bootstrap
		if err := json.Unmarshal(saved, &data); err != nil {
			t.Fatal(err)
		}
		if resolved {
			data.Comments[issue.ID][0].Resolved = true
			data.Comments[issue.ID][0].ThreadSummary = &domain.CommentThreadSummary{Content: "Existing summary"}
		}
		restore := store.ForceFullMutationsForTesting(true)
		err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw106_reset", "", nil, func(next *domain.Bootstrap) error {
			*next = data
			return nil
		})
		restore()
		if err != nil {
			t.Fatal(err)
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
	}
	mention := map[string]any{"type": "doc", "content": []any{map[string]any{
		"type": "paragraph", "content": []any{map[string]any{"type": "userMention", "attrs": map[string]any{"userId": member.ID, "label": member.Name}}},
	}}}
	for _, test := range []struct {
		name, method, tail, pattern string
		body                        any
		handler                     func(*server, http.ResponseWriter, *http.Request)
		code, events                int
		resolved, retry             bool
	}{
		{name: "create", method: http.MethodPost, body: map[string]any{"body": "Created exact comment", "bodyData": mention}, handler: (*server).createComment, code: http.StatusCreated, events: 1},
		{name: "reply", method: http.MethodPost, body: map[string]any{"body": "Created exact reply", "parentId": rootID}, handler: (*server).createComment, code: http.StatusCreated, events: 1},
		{name: "retry", method: http.MethodPost, body: map[string]any{"body": "Retry ignored"}, handler: (*server).createComment, code: http.StatusCreated, retry: true},
		{name: "update", method: http.MethodPatch, tail: "/" + rootID, pattern: "/{commentId}", body: map[string]any{"body": "Edited exact root", "expectedVersion": 3}, handler: (*server).updateComment, code: http.StatusOK, events: 1},
		{name: "unchanged", method: http.MethodPatch, tail: "/" + rootID, pattern: "/{commentId}", body: map[string]any{"resolved": false, "expectedVersion": 3}, handler: (*server).updateComment, code: http.StatusOK},
		{name: "conflict", method: http.MethodPatch, tail: "/" + rootID, pattern: "/{commentId}", body: map[string]any{"body": "Stale edit", "expectedVersion": 99}, handler: (*server).updateComment, code: http.StatusConflict},
		{name: "resolve", method: http.MethodPatch, tail: "/" + rootID, pattern: "/{commentId}", body: map[string]any{"resolved": true, "expectedVersion": 3}, handler: (*server).updateComment, code: http.StatusOK, events: 1},
		{name: "unresolve", method: http.MethodPatch, tail: "/" + rootID, pattern: "/{commentId}", body: map[string]any{"resolved": false, "expectedVersion": 3}, handler: (*server).updateComment, code: http.StatusOK, events: 1, resolved: true},
		{name: "delete", method: http.MethodDelete, tail: "/" + rootID, pattern: "/{commentId}", handler: (*server).deleteComment, code: http.StatusNoContent, events: 1},
		{name: "reaction-add", method: http.MethodPost, tail: "/" + rootID + "/reactions", pattern: "/{commentId}/reactions", body: map[string]any{"emoji": "heart"}, handler: (*server).toggleCommentReaction, code: http.StatusOK, events: 1},
		{name: "reaction-remove", method: http.MethodPost, tail: "/" + rootID + "/reactions", pattern: "/{commentId}/reactions", body: map[string]any{"emoji": "like"}, handler: (*server).toggleCommentReaction, code: http.StatusOK, events: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			var outcomes []any
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				reset(t, test.resolved)
				before := scaleP21SW59SQL(t, f)
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
					got.Webhooks = append(got.Webhooks, scaleP21SW106Envelope(t, map[string]any{
						"id": event.ID, "type": event.Type, "aggregateId": event.AggregateID, "actorId": event.ActorID,
						"payload": event.Payload, "previousValues": event.PreviousValues, "snapshot": event.Snapshot, "createdAt": event.CreatedAt,
					}))
				})
				captureRealtime := func(_ string, event domain.RealtimeEvent) {
					got.Realtime = append(got.Realtime, scaleP21SW106Envelope(t, map[string]any{
						"id": event.ID, "type": event.Type, "aggregateId": event.AggregateID, "actorId": event.ActorID,
						"seq": event.Seq, "clientId": event.ClientID, "payload": event.Payload, "createdAt": event.CreatedAt,
					}))
				}
				source.SetRealtimeSink(captureRealtime)
				if source != repo {
					repo.SetRealtimeSink(captureRealtime)
				}
				request := f.literalRequest(owner, nil, test.body)
				request.Method, request.URL.Path = test.method, "/api/issues/"+issue.ID+"/comments"+test.tail
				request.Header.Set("Content-Type", "application/json")
				creationKey := prefix + "created"
				if test.retry {
					creationKey = rootID
				}
				request = request.WithContext(store.WithCommentCreationKey(request.Context(), creationKey))
				if store.IsIssueDiscussionMutation(request.Context()) || store.UsesIssueRecordMutations(request.Context()) {
					t.Fatal("ordinary callback fixture unexpectedly selected a scoped writer")
				}
				code, events := test.code, test.events
				started := time.Now().UTC()
				func() {
					request, closeOutput := beginRequestResourceReads(request)
					defer closeOutput()
					restore := store.ForceFullMutationsForTesting(source == repo)
					defer restore()
					service := &server{store: source}
					call := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { test.handler(service, w, r) })
					mux := http.NewServeMux()
					mux.Handle(test.method+" /api/issues/{id}/comments"+test.pattern, call)
					response := &scaleP21SW59ResponseWriter{ResponseRecorder: httptest.NewRecorder(), t: t, source: source, requireHeld: code < 400 || code == http.StatusConflict}
					mux.ServeHTTP(response, request)
					got.Code, got.Header = response.Code, response.Header().Clone()
					if response.Body.Len() > 0 {
						got.Response = slices.Clone(response.Body.Bytes())
					}
				}()
				source.SetWebhookSink(nil)
				source.SetRealtimeSink(nil)
				repo.SetRealtimeSink(nil)
				got.Rows = scaleP21SW59SQL(t, f)
				after, err := repo.Events(t.Context(), issue.ID)
				if err != nil {
					t.Fatal(err)
				}
				for _, event := range after {
					if !priorIDs[event.ID] {
						got.Events = append(got.Events, event)
					}
				}
				if got.Code != code || len(got.Events) != events || len(got.Webhooks) != events {
					t.Fatalf("bounded=%t status=%d want=%d events/webhooks=%d/%d want=%d body=%s", source.BoundedMetadataEnabled(), got.Code, code, len(got.Events), len(got.Webhooks), events, got.Response)
				}
				if events == 0 && (!reflect.DeepEqual(before, got.Rows) || len(got.Realtime) != 0) {
					t.Fatal("refused, unchanged, conflicted or retried route changed business rows/publications")
				}
				if source.BoundedMetadataEnabled() && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
					t.Fatal("ordinary discussion route retained work or decoded full workspace metadata")
				}
				outcomes = append(outcomes, scaleP21SW59Normalized(t, got, before, started, time.Now().UTC()))
			}
			if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
				t.Fatal("ordinary discussion route/SQL/event/realtime/webhook differential", scaleP21M2Diff(outcomes[0], outcomes[1]))
			}
		})
	}
}

func scaleP21SW106Envelope(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
