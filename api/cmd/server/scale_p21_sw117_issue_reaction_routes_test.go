package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// Reuse authenticated fixture contexts; permission primitives have their own M
// tables. The alias must retain its ordinary issue-record marker, never the
// discussion marker used by comment routes.
func TestScaleP21SW117IssueReactionRoutesDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "off")
	f := scaleP21SW10Setup(t)
	repo, workspace := f.repository, f.data.Workspace.URLKey
	if err := f.service.stopDeliveryScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	owner, member := f.principals[0].user, f.principals[2].user
	issue := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records",
		map[string]any{"title": "SW117 complete issue reaction response", "teamId": f.public}, workspace, http.StatusCreated)
	prefix := idgen.New("sw117-")
	retainedDescription := strings.Repeat("Retained issue description. ", 100)
	stamp := time.Now().UTC().Add(-time.Hour)
	restore := store.ForceFullMutationsForTesting(true)
	err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw117_seed", "", nil, func(data *domain.Bootstrap) error {
		for index := range data.Issues {
			if data.Issues[index].ID != issue.ID {
				continue
			}
			data.Issues[index].Version = 42
			data.Issues[index].UpdatedAt = stamp
			data.Issues[index].Description = retainedDescription
			data.Issues[index].DocumentContent = &domain.DocumentContent{
				ID: prefix + "document", ContentData: map[string]any{"type": "doc", "retained": "Exact structured issue content", "content": []any{}},
			}
		}
		data.Comments[issue.ID] = []domain.Comment{{ID: prefix + "unrelated-comment", Body: "Reaction must preserve comments", User: member, CreatedAt: stamp}}
		data.Activities[issue.ID] = []domain.ActivityEvent{}
		return nil
	})
	restore()
	if err != nil {
		t.Fatal(err)
	}
	baseline, found := repo.BootstrapFor(workspace)
	if !found {
		t.Fatal("issue reaction fixture workspace missing")
	}
	saved, err := json.Marshal(baseline)
	if err != nil {
		t.Fatal(err)
	}
	reset := func(t *testing.T, reactions map[string][]string) {
		t.Helper()
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			source.SetWebhookSink(nil)
			source.SetRealtimeSink(nil)
		}
		var data domain.Bootstrap
		if err := json.Unmarshal(saved, &data); err != nil {
			t.Fatal(err)
		}
		for index := range data.Issues {
			if data.Issues[index].ID != issue.ID {
				continue
			}
			data.Issues[index].Reactions = nil
			if reactions != nil {
				data.Issues[index].Reactions = map[string][]string{}
				for emoji, users := range reactions {
					data.Issues[index].Reactions[emoji] = slices.Clone(users)
				}
			}
		}
		restore := store.ForceFullMutationsForTesting(true)
		err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw117_reset", "", nil, func(next *domain.Bootstrap) error {
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
	for _, test := range []struct {
		name      string
		actor     domain.User
		reactions map[string][]string
		emoji     string
		added     bool
		code      int
	}{
		{name: "nil-map-add", actor: owner, emoji: "like", added: true, code: http.StatusOK},
		{name: "empty-map-add", actor: member, reactions: map[string][]string{}, emoji: "like", added: true, code: http.StatusOK},
		{name: "nil-list-add", actor: member, reactions: map[string][]string{"like": nil, "untouched": {}}, emoji: "like", added: true, code: http.StatusOK},
		{name: "empty-list-add", actor: owner, reactions: map[string][]string{"like": {}, "untouched": nil}, emoji: "like", added: true, code: http.StatusOK},
		{name: "duplicate-only-remove-key", actor: owner, reactions: map[string][]string{"like": {owner.ID, owner.ID}, "untouched": {member.ID}}, emoji: "like", code: http.StatusOK},
		{name: "duplicate-mixed-remove-all", actor: member, reactions: map[string][]string{"like": {member.ID, owner.ID, member.ID, owner.ID}, "untouched": nil}, emoji: "like", code: http.StatusOK},
		{name: "append-preserves-other-duplicates", actor: owner, reactions: map[string][]string{"like": {member.ID, member.ID}}, emoji: "like", added: true, code: http.StatusOK},
		{name: "emoji-keeps-surrounding-space", actor: member, reactions: map[string][]string{"like": {owner.ID}}, emoji: " like ", added: true, code: http.StatusOK},
		{name: "blank-emoji-refused", actor: owner, reactions: map[string][]string{"like": {owner.ID}}, emoji: " ", code: http.StatusBadRequest},
	} {
		for _, alias := range []bool{false, true} {
			topology := "ordinary"
			family := "issues"
			if alias {
				topology, family = "issue-record-alias", "issue-records"
			}
			t.Run(test.name+"/"+topology, func(t *testing.T) {
				var outcomes []any
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					reset(t, test.reactions)
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
					checkPublicationOwner := func() {
						t.Helper()
						if source.BoundedMetadataEnabled() && source.MetadataWorkUsedForTesting() == 0 {
							t.Error("issue reaction publication lost owned output")
						}
					}
					source.SetWebhookSink(func(_ string, event domain.DomainEvent) {
						checkPublicationOwner()
						got.Webhooks = append(got.Webhooks, scaleP21SW106Envelope(t, map[string]any{
							"id": event.ID, "type": event.Type, "aggregateId": event.AggregateID, "actorId": event.ActorID,
							"payload": event.Payload, "previousValues": event.PreviousValues, "snapshot": event.Snapshot, "createdAt": event.CreatedAt,
						}))
					})
					source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
						checkPublicationOwner()
						got.Realtime = append(got.Realtime, scaleP21SW106Envelope(t, map[string]any{
							"id": event.ID, "type": event.Type, "aggregateId": event.AggregateID, "actorId": event.ActorID,
							"seq": event.Seq, "clientId": event.ClientID, "payload": event.Payload, "createdAt": event.CreatedAt,
						}))
					})
					request := f.literalRequest(test.actor, nil, map[string]any{
						"emoji": test.emoji, "commentId": "client-must-not-select-comment", "added": !test.added,
					})
					request.Method, request.URL.Path = http.MethodPost, "/api/"+family+"/"+issue.ID+"/reactions"
					request.Header.Set("Content-Type", "application/json")
					var statements atomic.Int64
					var observing atomic.Bool
					observing.Store(true)
					request = request.WithContext(store.WithSQLStatementObserver(request.Context(), func(string) {
						if observing.Load() {
							statements.Add(1)
						}
					}))
					started := time.Now().UTC()
					called := false
					func() {
						request, closeOutput := beginRequestResourceReads(request)
						defer closeOutput()
						service := &server{store: source}
						call := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							called = true
							if store.IsIssueDiscussionMutation(r.Context()) || store.UsesIssueRecordMutations(r.Context()) != alias {
								t.Fatal("issue reaction route changed ordinary/issue-record writer topology")
							}
							service.toggleIssueReaction(w, r)
						})
						if alias {
							call = service.issueRecordAlias(call)
						}
						mux := http.NewServeMux()
						mux.Handle("POST /api/"+family+"/{id}/reactions", call)
						response := &scaleP21SW59ResponseWriter{
							ResponseRecorder: httptest.NewRecorder(), t: t, source: source, requireHeld: test.code == http.StatusOK,
						}
						mux.ServeHTTP(response, request)
						got.Code, got.Header = response.Code, response.Header().Clone()
						if response.Body.Len() > 0 {
							got.Response = slices.Clone(response.Body.Bytes())
						}
					}()
					observing.Store(false)
					t.Logf("topology=%s bounded=%t request_SQL=%d", topology, source.BoundedMetadataEnabled(), statements.Load())
					if !called {
						t.Fatalf("issue reaction handler never ran: bounded=%t status=%d body=%s", source.BoundedMetadataEnabled(), got.Code, got.Response)
					}
					finished := time.Now().UTC()
					source.SetWebhookSink(nil)
					source.SetRealtimeSink(nil)
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
					events := 0
					if test.code == http.StatusOK {
						events = 1
					}
					if got.Code != test.code || len(got.Events) != events || len(got.Webhooks) != events || len(got.Realtime) != events {
						t.Fatalf("bounded=%t status=%d want=%d events/webhooks/realtime=%d/%d/%d want=%d body=%s", source.BoundedMetadataEnabled(), got.Code, test.code, len(got.Events), len(got.Webhooks), len(got.Realtime), events, got.Response)
					}
					if events == 0 {
						if !reflect.DeepEqual(before, got.Rows) {
							t.Fatal("refused issue reaction changed SQL business rows")
						}
					} else {
						var updated domain.Issue
						if err := json.Unmarshal(got.Response, &updated); err != nil {
							t.Fatal(err)
						}
						if updated.ID != issue.ID || updated.Version != 43 || updated.Description != retainedDescription || updated.DocumentContent == nil ||
							updated.DocumentContent.ContentData["retained"] != "Exact structured issue content" || updated.UpdatedAt.Before(started) || updated.UpdatedAt.After(finished) {
							t.Fatal("issue reaction response lost full issue values or exact version/clock update")
						}
						expected := map[string][]string{}
						for emoji, users := range test.reactions {
							expected[emoji] = slices.Clone(users)
						}
						if test.added {
							expected[test.emoji] = append(expected[test.emoji], test.actor.ID)
						} else {
							var users []string
							for _, id := range expected[test.emoji] {
								if id != test.actor.ID {
									users = append(users, id)
								}
							}
							if len(users) == 0 {
								delete(expected, test.emoji)
							} else {
								expected[test.emoji] = users
							}
						}
						if !reflect.DeepEqual(updated.Reactions, expected) {
							t.Fatal("issue reaction changed nil/empty/duplicate removal semantics", scaleP21M2Diff(updated.Reactions, expected))
						}
						var payload domain.ReactionInput
						if err := json.Unmarshal(got.Events[0].Payload, &payload); err != nil {
							t.Fatal(err)
						}
						var published struct {
							ActorID string `json:"actorId"`
						}
						if err := json.Unmarshal(got.Webhooks[0].(json.RawMessage), &published); err != nil {
							t.Fatal(err)
						}
						if payload.Emoji != test.emoji || payload.CommentID != "" || payload.Added == nil || *payload.Added != test.added ||
							got.Events[0].Type != "issue.reaction_toggled" || got.Events[0].AggregateID != issue.ID || published.ActorID != test.actor.ID {
							t.Fatal("issue reaction event did not replace client Added/comment identity with exact callback result")
						}
					}
					if source.BoundedMetadataEnabled() && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
						t.Fatal("issue reaction route retained work or decoded full workspace metadata")
					}
					outcomes = append(outcomes, scaleP21SW59Normalized(t, got, before, started, finished))
				}
				if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
					t.Fatal("issue reaction response/SQL/events/webhooks/realtime differential", scaleP21M2Diff(outcomes[0], outcomes[1]))
				}
			})
		}
	}
}
