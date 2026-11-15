package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func scaleP21SW108Normalized(t *testing.T, got scaleP21SW59Outcome, before scaleP21SW59Rows, started, finished time.Time) any {
	t.Helper()
	known := map[string]bool{}
	for _, row := range before.Subscriptions {
		known[row.([]any)[0].(string)] = true
	}
	generated := map[string]string{}
	for _, row := range got.Rows.Subscriptions {
		values := row.([]any)
		id := values[0].(string)
		if known[id] {
			continue
		}
		var thread domain.ThreadSubscription
		if err := json.Unmarshal(values[1].(json.RawMessage), &thread); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(id, "thread_subscription_") || thread.ID != id ||
			thread.CreatedAt.Before(started) || thread.CreatedAt.After(finished) || !thread.UpdatedAt.Equal(thread.CreatedAt) {
			t.Fatal("new thread subscription lost its generated ID/clock contract", thread)
		}
		generated[id] = fmt.Sprintf("<generated-thread:%d>", len(generated))
	}
	value := scaleP21SW59Normalized(t, got, before, started, finished)
	var normalize func(any) any
	normalize = func(value any) any {
		switch item := value.(type) {
		case string:
			if replacement, found := generated[item]; found {
				return replacement
			}
		case map[string]any:
			for key, child := range item {
				item[key] = normalize(child)
			}
		case []any:
			for index, child := range item {
				item[index] = normalize(child)
			}
		}
		return value
	}
	return normalize(value)
}

func TestScaleP21SW108ThreadSubscriptionRoutesDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_NARROW_MUTATION_VERIFY", "off")
	f := scaleP21SW10Setup(t)
	repo, workspace := f.repository, f.data.Workspace.URLKey
	if err := f.service.stopDeliveryScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	owner := f.principals[0].user
	public := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records",
		map[string]any{"title": "SW108 thread routes", "teamId": f.public}, workspace, http.StatusCreated)
	metadata, _ := repo.WorkspaceMetadata(workspace)
	privateTeam := ""
	for _, project := range metadata.Projects {
		if project.ID == "m2-unrelated" {
			privateTeam = project.TeamIDs[0]
		}
	}
	if privateTeam == "" {
		t.Fatal("private permission fixture missing")
	}
	privateAdmin := authClient(t)
	authRequest[domain.AuthSession](t, privateAdmin, http.MethodPost, f.host.URL+"/api/auth/login",
		map[string]string{"email": f.principals[1].user.Email, "password": "p03-fixture-password"}, "", http.StatusOK)
	private := authRequest[domain.Issue](t, privateAdmin, http.MethodPost, f.host.URL+"/api/issue-records",
		map[string]any{"title": "SW108 private thread", "teamId": privateTeam}, workspace, http.StatusCreated)
	authRequest[[]domain.IssuePermission](t, privateAdmin, http.MethodPut, f.host.URL+"/api/issues/"+private.ID+"/permissions",
		map[string]any{"permissions": []map[string]string{{"subjectType": "user", "subjectId": f.principals[3].user.ID, "role": "viewer"}}}, workspace, http.StatusOK)
	at := time.Now().UTC().Add(-time.Minute)
	restore := store.ForceFullMutationsForTesting(true)
	err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw108_seed", "", nil, func(data *domain.Bootstrap) error {
		for _, issue := range []domain.Issue{public, private} {
			root := issue.ID + "-root"
			data.Comments[issue.ID] = []domain.Comment{
				{ID: root, Version: 1, Body: "Thread root", User: owner, CreatedAt: at},
				{ID: issue.ID + "-reply", Version: 1, Body: "Thread reply", User: owner, ParentID: &root, CreatedAt: at.Add(time.Second)},
			}
			data.Activities[issue.ID] = []domain.ActivityEvent{}
		}
		return nil
	})
	restore()
	if err != nil {
		t.Fatal(err)
	}
	baseline, found := repo.BootstrapFor(workspace)
	if !found {
		t.Fatal("thread route baseline missing")
	}
	saved, err := json.Marshal(baseline)
	if err != nil {
		t.Fatal(err)
	}
	type testCase struct {
		name, method, state, seed, comment, credential string
		actor                                          int
		private, authorize                             bool
		code, events                                   int
	}
	tests := []testCase{
		{name: "subscribe-new", method: http.MethodPut, state: "subscribed", seed: "missing", code: http.StatusOK, events: 1},
		{name: "mute-new", method: http.MethodPut, state: "muted", seed: "missing", code: http.StatusOK, events: 1},
		{name: "update-first-duplicate-tuple", method: http.MethodPut, state: "muted", code: http.StatusOK, events: 1},
		{name: "unchanged", method: http.MethodPut, state: "subscribed", code: http.StatusOK},
		{name: "clear-all-matches", method: http.MethodDelete, code: http.StatusNoContent, events: 1},
		{name: "clear-missing", method: http.MethodDelete, seed: "missing", code: http.StatusNoContent, events: 1},
		{name: "invalid-state", method: http.MethodPut, state: "invalid", code: http.StatusBadRequest},
		{name: "missing-comment", method: http.MethodPut, state: "muted", comment: "missing", code: http.StatusNotFound},
		{name: "cyclic-thread", method: http.MethodPut, state: "muted", seed: "cycle", code: http.StatusNotFound},
	}
	// Keep production authorization separate from the ordinary handler-boundary
	// cases above, whose contexts intentionally reach the full writer (SW106).
	for actor, principal := range f.principals {
		for _, credential := range scaleP21M3Credentials() {
			for _, method := range []string{http.MethodPut, http.MethodDelete} {
				tests = append(tests, testCase{
					name:   principal.name + "/" + credential.name + "/" + method,
					method: method, state: "muted", actor: actor, credential: credential.name,
					private: true, authorize: true, code: -1,
				})
			}
		}
	}
	reset := func(test testCase, user domain.User, target string) {
		t.Helper()
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			source.SetWebhookSink(nil)
			source.SetRealtimeSink(nil)
		}
		var data domain.Bootstrap
		if err := json.Unmarshal(saved, &data); err != nil {
			t.Fatal(err)
		}
		root := target + "-root"
		first := domain.ThreadSubscription{ID: target + "-thread", IssueID: target, CommentID: root, UserID: user.ID, State: "subscribed", CreatedAt: at, UpdatedAt: at}
		duplicate := first
		duplicate.ID, duplicate.State = target+"-duplicate", "muted"
		if test.seed == "missing" {
			first.UserID, duplicate.UserID = "unrelated-user", "unrelated-user"
		}
		unrelated := first
		unrelated.ID, unrelated.IssueID, unrelated.CommentID = target+"-unrelated-thread", target+"-other-issue", "other-root"
		data.ThreadSubscriptions = []domain.ThreadSubscription{unrelated, first, duplicate}
		if test.seed == "cycle" {
			reply := target + "-reply"
			data.Comments[target][0].ParentID = &reply
		}
		restore := store.ForceFullMutationsForTesting(true)
		err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw108_reset", "", nil, func(next *domain.Bootstrap) error {
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
	for _, test := range tests {
		for _, family := range []string{"issues", "issue-records"} {
			t.Run(test.name+"/"+family, func(t *testing.T) {
				user, target := f.principals[test.actor].user, public.ID
				if test.private {
					target = private.ID
				}
				var key *domain.APIKey
				for _, credential := range scaleP21M3Credentials() {
					if credential.name == test.credential {
						key = credential.key
						if key != nil && test.credential == "selected-set" {
							key.TeamIDs = []string{privateTeam}
						}
					}
				}
				var outputs []any
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					reset(test, user, target)
					before := scaleP21SW59SQL(t, f)
					prior, err := repo.Events(t.Context(), target)
					if err != nil {
						t.Fatal(err)
					}
					ids := map[string]bool{}
					for _, event := range prior {
						ids[event.ID] = true
					}
					got := scaleP21SW59Outcome{}
					source.SetWebhookSink(func(_ string, event domain.DomainEvent) {
						got.Webhooks = append(got.Webhooks, scaleP21SW106Envelope(t, map[string]any{
							"id": event.ID, "type": event.Type, "aggregateId": event.AggregateID, "actorId": event.ActorID,
							"payload": event.Payload, "previousValues": event.PreviousValues, "snapshot": event.Snapshot, "createdAt": event.CreatedAt,
						}))
					})
					source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
						got.Realtime = append(got.Realtime, scaleP21SW106Envelope(t, event))
					})
					comment := target + "-reply"
					if test.comment == "missing" {
						comment = target + "-missing"
					}
					var body any
					if test.method == http.MethodPut {
						body = map[string]string{"state": test.state}
					}
					request := f.literalRequest(user, key, body)
					request.Method, request.URL.Path = test.method, "/api/"+family+"/"+target+"/comments/"+comment+"/subscription"
					request.Header.Set("Content-Type", "application/json")
					started := time.Now().UTC()
					func() {
						request, closeOutput := beginRequestResourceReads(request)
						defer closeOutput()
						service := &server{store: source}
						handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							if test.method == http.MethodPut {
								service.setThreadSubscription(w, r)
							} else {
								service.clearThreadSubscription(w, r)
							}
						})
						if family == "issue-records" {
							handler = service.issueRecordAlias(handler)
						}
						mux := http.NewServeMux()
						mux.Handle(test.method+" /api/"+family+"/{id}/comments/{commentId}/subscription", handler)
						response := &scaleP21SW59ResponseWriter{ResponseRecorder: httptest.NewRecorder(), t: t, source: source, requireHeld: test.code > 0 && test.code < 400}
						if !test.authorize || service.authorizeWorkspaceRequest(response, request, user) {
							mux.ServeHTTP(response, request)
						}
						got.Code, got.Header = response.Code, response.Header().Clone()
						got.Response = slices.Clone(response.Body.Bytes())
					}()
					finished := time.Now().UTC()
					source.SetWebhookSink(nil)
					source.SetRealtimeSink(nil)
					got.Rows = scaleP21SW59SQL(t, f)
					after, err := repo.Events(t.Context(), target)
					if err != nil {
						t.Fatal(err)
					}
					for _, event := range after {
						if !ids[event.ID] {
							got.Events = append(got.Events, event)
						}
					}
					if test.code > 0 && (got.Code != test.code || len(got.Events) != test.events || len(got.Webhooks) != test.events || len(got.Realtime) != test.events) {
						t.Fatalf("bounded=%t status=%d want=%d events=%d/%d/%d want=%d body=%s", source.BoundedMetadataEnabled(), got.Code, test.code, len(got.Events), len(got.Webhooks), len(got.Realtime), test.events, got.Response)
					}
					if got.Code >= 400 || test.code > 0 && test.events == 0 {
						if !reflect.DeepEqual(before, got.Rows) || len(got.Events)+len(got.Webhooks)+len(got.Realtime) != 0 {
							t.Fatal("refused/no-op thread route changed SQL/publications")
						}
					}
					if source.BoundedMetadataEnabled() && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
						t.Fatal("thread route leaked work or hydrated full metadata")
					}
					outputs = append(outputs, scaleP21SW108Normalized(t, got, before, started, finished))
				}
				if !reflect.DeepEqual(outputs[0], outputs[1]) {
					t.Fatal("thread route/SQL/events differ", scaleP21M2Diff(outputs[0], outputs[1]))
				}
			})
		}
	}
}
