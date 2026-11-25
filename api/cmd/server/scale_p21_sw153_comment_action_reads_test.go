package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

type scaleP21SW153Start struct {
	Workspace string
	Actor     domain.User
	Allowed   bool
}

// This is the literal original startCommentActions path up to its goroutine.
// The legacy oracle does not call the new bounded input selector.
func scaleP21SW153LegacyStart(s *server, r *http.Request, issueID string, comment domain.Comment) scaleP21SW153Start {
	if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok && !apiKeyHasScope(key, "write") {
		return scaleP21SW153Start{}
	}
	if comment.User.App || comment.BotActor != nil || comment.User.Via != nil {
		return scaleP21SW153Start{}
	}
	var data domain.Bootstrap
	if read, bounded := r.Context().Value(boundedIssueWriteAuthorizationContext{}).(*store.IssueWriteAuthorizationRead); bounded {
		data = read.Data
	} else {
		data = s.workspaceData(r)
	}
	data.Viewer = requestActor(s, r)
	if read, bounded := r.Context().Value(boundedIssueWriteAuthorizationContext{}).(*store.IssueWriteAuthorizationRead); bounded {
		settings, ok, err := read.CurrentCommentActionSettings(r.Context(), data.Viewer.ID)
		if err != nil {
			return scaleP21SW153Start{}
		}
		if ok && settings.UserID != "" {
			data.UserSettings = map[string]domain.UserSettings{data.Viewer.ID: settings}
		}
	} else if _, settings, ok := s.store.AccountCollections(data.Workspace.URLKey, data.Viewer.ID); ok && settings.UserID != "" {
		data.UserSettings = map[string]domain.UserSettings{data.Viewer.ID: settings}
	}
	issue, err := issueByID(&data, issueID)
	if err != nil || !s.commentActionsAllowed(data, *issue) {
		return scaleP21SW153Start{}
	}
	return scaleP21SW153Start{Workspace: data.Workspace.URLKey, Actor: data.Viewer, Allowed: true}
}

func TestScaleP21SW153CommentActionReadRoutesDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "off")
	f := scaleP21SW10Setup(t)
	repo, workspace := f.repository, f.data.Workspace.URLKey
	if err := f.service.stopDeliveryScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	owner, member, guest := f.principals[0].user, f.principals[2].user, f.principals[3].user
	prefix := idgen.New("sw153-")
	viaActor := owner
	viaActor.Via = &domain.ActorBot{ID: prefix + "request-app", Name: "Request attribution"}
	appActor := owner
	appActor.App = true
	issue := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records",
		map[string]any{"title": "SW153 inert comment actions", "teamId": f.public}, workspace, http.StatusCreated)
	stamp := time.Now().UTC().Add(-time.Hour)
	comment := domain.Comment{ID: prefix + "comment", Version: 9, User: owner, Body: "Please make this urgent.", CreatedAt: stamp}
	priority, samePriority := 1, 3
	globalState, globalLabel, rawUser := prefix+"global-state", prefix+"global-label", prefix+"raw-user"
	labelIDs := []string{globalLabel}
	actions := []domain.CommentAction{}
	add := func(name, status, kind string, version int64, arguments domain.CommentActionArguments) int {
		actions = append(actions, domain.CommentAction{
			ID: prefix + name, IssueID: issue.ID, CommentID: comment.ID, CommentVersion: version,
			TargetUserID: member.ID, Status: status, Action: kind, Arguments: arguments, CreatedAt: stamp,
		})
		return len(actions) - 1
	}
	keep := []int{
		add("accepted-invalid", "accepted", "unknown-action", 9, domain.CommentActionArguments{}),
		add("accepted-stale", "accepted", "priority", 1, domain.CommentActionArguments{Priority: &samePriority}),
		add("pending", "pending", "priority", 9, domain.CommentActionArguments{Priority: &priority}),
	}
	add("pending-stale", "pending", "priority", 8, domain.CommentActionArguments{Priority: &priority})
	add("dismissed", "dismissed", "priority", 9, domain.CommentActionArguments{Priority: &priority})
	keep = append(keep, add("other-status", "queued", "priority", 1, domain.CommentActionArguments{Priority: &priority}))
	add("other-invalid", "queued", "priority", 9, domain.CommentActionArguments{Priority: &samePriority})
	missing := add("accepted-missing-comment", "accepted", "unknown-action", 9, domain.CommentActionArguments{})
	actions[missing].CommentID = prefix + "missing-comment"
	otherIssue := add("other-issue", "accepted", "unknown-action", 9, domain.CommentActionArguments{})
	actions[otherIssue].IssueID = prefix + "other-issue"
	keep = append(keep,
		add("global-state", "pending", "status", 9, domain.CommentActionArguments{StateID: &globalState}),
		add("global-label", "pending", "labels", 9, domain.CommentActionArguments{LabelIDs: &labelIDs}),
		add("raw-user", "pending", "assignee", 9, domain.CommentActionArguments{AssigneeID: &rawUser}),
	)
	duplicate := actions[keep[0]]
	duplicate.TargetUserID, duplicate.CommentVersion = guest.ID, 2
	actions = append(actions, duplicate)
	keep = append(keep, len(actions)-1)
	wantActions := []domain.CommentAction{}
	for _, index := range keep {
		wantActions = append(wantActions, actions[index])
	}
	mutate := func(t *testing.T, event string, change func(*domain.Bootstrap)) {
		t.Helper()
		restore := store.ForceFullMutationsForTesting(true)
		err := repo.MutateWorkspace(t.Context(), workspace, event, "", nil, func(data *domain.Bootstrap) error {
			change(data)
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
	mutate(t, "scale_p21.sw153_seed", func(data *domain.Bootstrap) {
		for index := range data.Issues {
			if data.Issues[index].ID == issue.ID {
				data.Issues[index].Priority = samePriority
				data.Issues[index].Assignee = nil
				data.Issues[index].Labels = nil
				data.Issues[index].Permissions = []domain.IssuePermission{
					{ID: prefix + "member-editor", IssueID: issue.ID, SubjectType: "user", SubjectID: member.ID, Role: "editor", CreatedAt: stamp, UpdatedAt: stamp},
					{ID: prefix + "guest-viewer", IssueID: issue.ID, SubjectType: "user", SubjectID: guest.ID, Role: "viewer", CreatedAt: stamp, UpdatedAt: stamp},
				}
			}
		}
		data.Comments[issue.ID] = []domain.Comment{comment}
		data.CommentActions = append(data.CommentActions, actions...)
		data.States = slices.DeleteFunc(data.States, func(state domain.WorkflowState) bool { return state.TeamID == f.public })
		data.States = append(data.States, domain.WorkflowState{ID: globalState, Name: "Global active", Type: "started"})
		data.Labels = append(data.Labels, domain.IssueLabel{ID: globalLabel, Name: "Global label", Scope: "workspace"})
		data.Users = append(data.Users, domain.User{ID: rawUser, Name: "Raw directory witness", Active: true})
		if data.UserSettings == nil {
			data.UserSettings = map[string]domain.UserSettings{}
		}
		for _, principal := range f.principals {
			data.UserSettings[principal.user.ID] = domain.UserSettings{UserID: principal.user.ID, AgentEnabled: true}
		}
		if data.WorkspaceSettings.FeatureFlags == nil {
			data.WorkspaceSettings.FeatureFlags = map[string]bool{}
		}
		data.WorkspaceSettings.FeatureFlags["ai"] = true
	})
	baseline, ok := repo.BootstrapFor(workspace)
	if !ok {
		t.Fatal("comment-action fixture workspace missing")
	}
	saved, err := json.Marshal(baseline)
	if err != nil {
		t.Fatal(err)
	}
	type testCase struct {
		name, operation, gate, afterAuthorization string
		actor                                     domain.User
		key                                       *domain.APIKey
		code                                      int
		listed, allowed                           bool
	}
	tests := []testCase{}
	for principalIndex, principal := range f.principals {
		for _, credential := range []struct {
			name string
			key  *domain.APIKey
		}{
			{"session", nil},
			{"all", &domain.APIKey{ID: prefix + "all", Scopes: []string{"read", "write"}, TeamRestriction: "all"}},
			{"selected-empty", &domain.APIKey{ID: prefix + "empty", Scopes: []string{"read", "write"}, TeamRestriction: "selected", TeamIDs: []string{}}},
			{"selected-team", &domain.APIKey{ID: prefix + "selected", Scopes: []string{"read", "write"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}},
		} {
			code := http.StatusOK
			if principalIndex >= 3 || credential.name == "selected-empty" {
				code = http.StatusForbidden
			}
			tests = append(tests, testCase{name: "list/" + principal.name + "/" + credential.name, operation: "list", actor: principal.user, key: credential.key, code: code, listed: code == http.StatusOK})
		}
	}
	tests = append(tests,
		testCase{name: "list/handed-issue-fresh-raw-generation", operation: "list", actor: owner, code: http.StatusOK, listed: true, afterAuthorization: "raw-generation"},
		testCase{name: "list/preference-disabled", operation: "list", actor: owner, code: http.StatusOK, gate: "preference"},
		testCase{name: "list/workspace-ai-disabled", operation: "list", actor: owner, code: http.StatusOK, gate: "workspace"},
		testCase{name: "list/agent-disabled", operation: "list", actor: owner, code: http.StatusOK, gate: "agent"},
		testCase{name: "list/read-only-key", operation: "list", actor: owner, code: http.StatusOK, listed: true, key: &domain.APIKey{ID: prefix + "read-list", Scopes: []string{"read"}, TeamRestriction: "all"}},
		testCase{name: "list/archived", operation: "list", actor: owner, code: http.StatusOK, gate: "archived"},
		testCase{name: "start/editor", operation: "start", actor: owner, code: http.StatusOK, allowed: true},
		testCase{name: "start/actor-attribution", operation: "start", actor: viaActor, code: http.StatusOK, allowed: true},
		testCase{name: "start/app-actor", operation: "start", actor: appActor, code: http.StatusOK},
		testCase{name: "start/public-viewer", operation: "start", actor: f.principals[4].user, code: http.StatusOK},
		testCase{name: "start/preference-disabled", operation: "start", actor: owner, code: http.StatusOK, gate: "preference"},
		testCase{name: "start/actor-agent-disabled", operation: "start", actor: owner, code: http.StatusOK, gate: "agent-preference"},
		testCase{name: "start/current-preference-after-handoff", operation: "start", actor: owner, code: http.StatusOK, afterAuthorization: "preference"},
		testCase{name: "start/workspace-ai-disabled", operation: "start", actor: owner, code: http.StatusOK, gate: "workspace"},
		testCase{name: "start/agent-disabled", operation: "start", actor: owner, code: http.StatusOK, gate: "agent"},
		testCase{name: "start/write-tools-disabled", operation: "start", actor: owner, code: http.StatusOK, gate: "write-tools"},
		testCase{name: "start/read-only-key", operation: "start", actor: owner, code: http.StatusOK, key: &domain.APIKey{ID: prefix + "read", Scopes: []string{"read"}, TeamRestriction: "all"}},
		testCase{name: "start/app-comment", operation: "start", actor: owner, code: http.StatusOK, gate: "app-comment"},
		testCase{name: "start/bot-comment", operation: "start", actor: owner, code: http.StatusOK, gate: "bot-comment"},
		testCase{name: "start/via-comment", operation: "start", actor: owner, code: http.StatusOK, gate: "via-comment"},
		testCase{name: "start/archived", operation: "start", actor: owner, code: http.StatusOK, gate: "archived"},
	)
	pairs := 0
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			type outcome struct {
				Code   int
				Header http.Header
				Body   json.RawMessage
			}
			outcomes := []outcome{}
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				mutate(t, "scale_p21.sw153_reset", func(data *domain.Bootstrap) {
					var restored domain.Bootstrap
					if err := json.Unmarshal(saved, &restored); err != nil {
						t.Fatal(err)
					}
					*data = restored
				})
				setGate := func(gate string) {
					mutate(t, "scale_p21.sw153_gate", func(data *domain.Bootstrap) {
						switch gate {
						case "preference":
							disabled := false
							data.UserSettings[test.actor.ID] = domain.UserSettings{UserID: test.actor.ID, AgentEnabled: true, SuggestedActionsOnComments: &disabled}
						case "workspace":
							data.WorkspaceSettings.FeatureFlags["ai"] = false
						case "agent-preference":
							data.UserSettings[test.actor.ID] = domain.UserSettings{UserID: test.actor.ID, AgentEnabled: false}
						case "archived":
							for index := range data.Issues {
								if data.Issues[index].ID == issue.ID {
									data.Issues[index].ArchivedAt = &stamp
								}
							}
						}
					})
				}
				if test.gate == "preference" || test.gate == "workspace" || test.gate == "agent-preference" || test.gate == "archived" {
					setGate(test.gate)
				}
				service := &server{store: source, agent: appconfig.AgentConfig{Enabled: true, ToolsEnabled: true, WriteTools: true}}
				if test.gate == "agent" {
					service.agent.Enabled = false
				}
				if test.gate == "write-tools" {
					service.agent.WriteTools = false
				}
				request := scaleP21SW47Request(f.scaleP21M2Fixture, test.actor, test.key, http.MethodGet, "/api/issue-records/"+issue.ID+"/comment-actions", issue.ID, "", nil)
				if test.operation == "start" {
					request.Method, request.URL.Path = http.MethodPost, "/api/issue-records/"+issue.ID+"/comments"
				}
				statements := 0
				measureSQL := test.name == "list/admin-private-outsider/session"
				if measureSQL {
					request = request.WithContext(store.WithSQLStatementObserver(request.Context(), func(string) { statements++ }))
				}
				var got outcome
				func() {
					request, closeOutput := beginRequestResourceReads(request)
					defer closeOutput()
					response := &scaleP21SW59ResponseWriter{ResponseRecorder: httptest.NewRecorder(), t: t, source: source, requireHeld: test.code == http.StatusOK}
					if !service.resourceAllowedLegacy(request, workspace, test.actor.ID) {
						writeError(response, http.StatusForbidden, "This resource is outside your teams")
					} else {
						extra := domain.CommentAction{}
						if test.afterAuthorization == "preference" {
							setGate("preference")
						} else if test.afterAuthorization == "raw-generation" {
							freshUser := prefix + "fresh-raw-user"
							extra = domain.CommentAction{ID: prefix + "fresh-action", IssueID: issue.ID, CommentID: comment.ID, CommentVersion: comment.Version,
								TargetUserID: guest.ID, Status: "pending", Action: "assignee", Arguments: domain.CommentActionArguments{AssigneeID: &freshUser}, CreatedAt: stamp}
							mutate(t, "scale_p21.sw153_peer_generation", func(data *domain.Bootstrap) {
								for index := range data.Issues {
									if data.Issues[index].ID == issue.ID {
										data.Issues[index].Priority = priority
									}
								}
								data.Users = append(data.Users, domain.User{ID: freshUser, Name: "Fresh raw directory", Active: true})
								data.CommentActions = append(data.CommentActions, extra)
							})
						}
						before := scaleP21SW59SQL(t, f)
						mux := http.NewServeMux()
						handler := service.issueRecordAlias(func(w http.ResponseWriter, r *http.Request) {
							if test.operation == "list" {
								service.listCommentActions(w, r)
								return
							}
							currentComment := comment
							switch test.gate {
							case "app-comment":
								currentComment.User.App = true
							case "bot-comment":
								currentComment.BotActor = &domain.ActorBot{ID: prefix + "bot"}
							case "via-comment":
								currentComment.User.Via = &domain.ActorBot{ID: prefix + "via"}
							}
							var gate scaleP21SW153Start
							if source.BoundedMetadataEnabled() {
								workspace, actor, allowed := service.commentActionStartRead(r, issue.ID, currentComment)
								gate = scaleP21SW153Start{Workspace: workspace, Actor: actor, Allowed: allowed}
							} else {
								gate = scaleP21SW153LegacyStart(service, r, issue.ID, currentComment)
							}
							if gate.Allowed != test.allowed || gate.Allowed && (gate.Workspace != workspace || !reflect.DeepEqual(gate.Actor, test.actor)) {
								t.Fatal("comment-action synchronous production launch gate changed", source.BoundedMetadataEnabled(), gate)
							}
							if !gate.Allowed {
								// The denied production path returns before creating a
								// goroutine. Positive tuples do not launch generation.
								service.startCommentActions(r, issue.ID, currentComment)
							}
							writeJSON(w, http.StatusOK, gate)
						})
						tail := "/comment-actions"
						if test.operation == "start" {
							tail = "/comments"
						}
						mux.HandleFunc(request.Method+" /api/issue-records/{id}"+tail, handler)
						mux.ServeHTTP(response, request)
						if !reflect.DeepEqual(before, scaleP21SW59SQL(t, f)) {
							t.Fatal("comment-action read or denied start changed business rows")
						}
						if test.operation == "list" && test.code == http.StatusOK {
							expected := []domain.CommentAction{}
							if test.listed {
								expected = slices.Clone(wantActions)
								if extra.ID != "" {
									expected = append(expected, extra)
								}
							}
							var actual []domain.CommentAction
							if err := json.Unmarshal(response.Body.Bytes(), &actual); err != nil || !reflect.DeepEqual(actual, expected) {
								t.Fatal("comment-action route changed exact action order/status/source/validation results", err, actual, expected)
							}
						}
					}
					got = outcome{Code: response.Code, Header: response.Header().Clone(), Body: bytes.Clone(response.Body.Bytes())}
				}()
				if got.Code != test.code {
					t.Fatal("comment-action route status changed", got.Code, test.code, string(got.Body))
				}
				if source.BoundedMetadataEnabled() && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
					t.Fatal("comment-action route leaked output work or decoded complete bounded metadata")
				}
				if measureSQL {
					t.Logf("representative_comment_action_list bounded=%t request_context_SQL=%d; fresh raw Background selector is outside the request observer", source.BoundedMetadataEnabled(), statements)
				}
				outcomes = append(outcomes, got)
			}
			if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
				t.Fatal("comment-action route or synchronous start-gate differential", scaleP21M2Diff(outcomes[0], outcomes[1]))
			}
			pairs++
		})
	}
	t.Logf("comment-action list/permission/source/start-gate pairs=%d; accepted bypass and global raw validation exact; positive gate tuples observed without launching goroutines or model/network work", pairs)
}
