package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// generateCommentActions and resolveCommentAction on their closed bounded
// writers, compared with the legacy discussion and scoped writers: the claim,
// the model request, the proposal filter and append (stale comment, existing
// actions, settings turned off, and a parent-only grant that the claim and
// append inherit as the read does),
// the claim callback's own races, and every resolution outcome (accepted
// update, link and sub-issue, dismissal, idempotence, conflicts, missing
// actions and comments, a turned-off setting and a creation failure that
// rolls the whole resolution back).

type scaleP21CommentActionProvider struct {
	mu       sync.Mutex
	calls    []map[string]any
	requests []json.RawMessage
	before   func()
	server   *httptest.Server
}

func scaleP21CommentActionSSE(calls []map[string]any) string {
	toolCalls := []any{}
	for index, call := range calls {
		raw, _ := json.Marshal(call)
		toolCalls = append(toolCalls, map[string]any{"index": index, "id": "call-" + string(rune('a'+index)), "function": map[string]any{"name": "propose_comment_action", "arguments": string(raw)}})
	}
	chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": toolCalls}, "finish_reason": "tool_calls"}}})
	return "data: " + string(chunk) + "\n\ndata: [DONE]\n\n"
}

func newScaleP21CommentActionProvider(t *testing.T) *scaleP21CommentActionProvider {
	p := &scaleP21CommentActionProvider{}
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		p.mu.Lock()
		p.requests = append(p.requests, raw)
		calls, before := p.calls, p.before
		p.mu.Unlock()
		if before != nil {
			before()
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(scaleP21CommentActionSSE(calls)))
	}))
	t.Cleanup(p.server.Close)
	return p
}

func (p *scaleP21CommentActionProvider) configure(service *server) {
	service.agent = appconfig.AgentConfig{Enabled: true, ToolsEnabled: true, WriteTools: true, BaseURL: p.server.URL, Model: "scripted", MaxOutputTokens: 256}
	service.agentClient = p.server.Client()
}

type scaleP21CommentActionSetup struct {
	f                    scaleP21IssueUpdateFixture
	owner, member, guest domain.User
	issue, parent, other string
	stateA, stateB       string
	comment              domain.Comment
	at                   time.Time
}

func scaleP21CommentActionFixture(t *testing.T) scaleP21CommentActionSetup {
	t.Helper()
	f := scaleP21IssueUpdateSetup(t)
	if err := f.service.stopDeliveryScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	full, ok := f.repository.BootstrapFor(f.data.Workspace.URLKey)
	if !ok {
		t.Fatal("fixture workspace missing")
	}
	setup := scaleP21CommentActionSetup{f: f, owner: f.principals[0].user, member: f.principals[2].user, guest: f.principals[3].user,
		issue: f.triage.ID, parent: f.visible.ID, at: time.Date(2026, 4, 5, 6, 7, 8, 0, time.UTC)}
	for _, state := range statesForTeam(&full, f.public) {
		if state.ID != f.triage.State.ID && setup.stateB == "" {
			setup.stateB = state.ID
		}
	}
	setup.stateA = f.triage.State.ID
	if setup.stateB == "" {
		t.Fatal("fixture needs a second state")
	}
	setup.comment = domain.Comment{ID: "ca-comment", Version: 3, Body: "Please make this urgent, move it along and see https://example.test/spec for details.", User: setup.member, CreatedAt: setup.at}
	return setup
}

// seed makes the triage issue a child of the visible issue, adds the
// member's comment and enables suggestions for the actors.
func (c scaleP21CommentActionSetup) seed(t *testing.T, change func(*domain.Bootstrap)) {
	t.Helper()
	c.f.exec(t, `UPDATE issue_number_sequences SET last_number=? WHERE workspace_key=?`, c.f.sequence, c.f.data.Workspace.URLKey)
	c.f.seedWriter(t, func(data *domain.Bootstrap) {
		parent := c.parent
		for index := range data.Issues {
			if data.Issues[index].ID == c.issue {
				data.Issues[index].ParentID = &parent
				data.Issues[index].Priority = 3
				// Created long before every run: an edit by its creator within
				// three minutes of creation adds no history, so a wall-clock
				// creation time made owner-accepts-status depend on how long
				// the earlier cases took (legacy inside the window, bounded
				// after it under -race).
				data.Issues[index].CreatedAt = c.at
				// The member edits through a direct grant.
				data.Issues[index].Permissions = append(data.Issues[index].Permissions, domain.IssuePermission{
					ID: "ca-member-editor", IssueID: c.issue, SubjectType: "user", SubjectID: c.member.ID, Role: "editor", CreatedAt: c.at, UpdatedAt: c.at})
			}
		}
		data.Comments[c.issue] = append(data.Comments[c.issue], c.comment)
		enabled := true
		if data.UserSettings == nil {
			data.UserSettings = map[string]domain.UserSettings{}
		}
		for _, user := range []domain.User{c.owner, c.member, c.guest} {
			settings := data.UserSettings[user.ID]
			settings.UserID, settings.AgentEnabled, settings.SuggestedActionsOnComments = user.ID, true, &enabled
			data.UserSettings[user.ID] = settings
		}
		if data.WorkspaceSettings.FeatureFlags == nil {
			data.WorkspaceSettings.FeatureFlags = map[string]bool{}
		}
		data.WorkspaceSettings.FeatureFlags["ai-agent"] = true
		data.Labels = append(data.Labels, domain.IssueLabel{ID: "ca-archived", Name: "Archived", Scope: "workspace", ArchivedAt: &c.at})
		if change != nil {
			change(data)
		}
	})
}

// race changes the workspace while the model request is in flight: through
// the literal full writer (never holding a store lock), then a bounded
// source reloads that committed state from SQL.
func (c scaleP21CommentActionSetup) race(t *testing.T, service *server, change func(*domain.Bootstrap)) {
	restore := store.ForceFullMutationsForTesting(true)
	err := c.f.repository.MutateWorkspace(context.Background(), c.f.data.Workspace.URLKey, "scale_p21.comment_action_race", "", nil, func(data *domain.Bootstrap) error {
		change(data)
		return nil
	})
	restore()
	if err != nil {
		t.Error(err)
	}
	if service.store != c.f.repository {
		if err := service.store.ReloadWorkspace(context.Background(), c.f.data.Workspace.URLKey); err != nil {
			t.Error(err)
		}
	}
}

func TestScaleP21CommentActionGenerationDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, narrow := range []string{"off", "on"} {
		t.Run("narrow-"+narrow, func(t *testing.T) {
			t.Setenv("FLOW_NARROW_MUTATION_INPUTS", narrow)
			scaleP21CommentActionGeneration(t)
		})
	}
}

func scaleP21CommentActionGeneration(t *testing.T) {
	c := scaleP21CommentActionFixture(t)
	f, workspace := c.f, c.f.data.Workspace.URLKey
	provider := newScaleP21CommentActionProvider(t)
	labels := []string{"triage-l-public"}
	priority, zero := 1, 0
	owner := c.owner.ID
	proposals := map[string][]map[string]any{
		"update": {
			{"action": "priority", "arguments": map[string]any{"priority": priority}},
			{"action": "priority", "arguments": map[string]any{"priority": priority}},
			{"action": "priority", "arguments": map[string]any{"priority": 3}},
			{"action": "status", "arguments": map[string]any{"stateId": c.stateB}},
			{"action": "assignee", "arguments": map[string]any{"assigneeId": owner}},
			{"action": "labels", "arguments": map[string]any{"labelIds": labels}},
		},
		"link-subissue": {
			{"action": "link", "arguments": map[string]any{"url": "https://example.test/spec", "title": "Spec"}},
			{"action": "link", "arguments": map[string]any{"url": "https://example.test/elsewhere"}},
			{"action": "subIssue", "arguments": map[string]any{"title": "Follow up", "unknown": true}},
			{"action": "subIssue", "arguments": map[string]any{"title": "Follow up", "description": "Details"}},
			{"action": "labels", "arguments": map[string]any{"labelIds": []string{"ca-archived"}}},
			{"action": "priority", "arguments": map[string]any{"priority": zero}},
		},
	}
	type generationCase struct {
		name, script string
		actor        domain.User
		before       func(t *testing.T, service *server)
		change       func(*domain.Bootstrap)
	}
	cases := []generationCase{
		{name: "update", script: "update", actor: c.member},
		{name: "link-subissue", script: "link-subissue", actor: c.member},
		{name: "owner-actor", script: "update", actor: c.owner},
		{name: "already-claimed", script: "update", actor: c.member, change: func(data *domain.Bootstrap) {
			for index := range data.Comments[c.issue] {
				if data.Comments[c.issue][index].ID == c.comment.ID {
					data.Comments[c.issue][index].ActionsGeneratedAt = &c.at
				}
			}
		}},
		{name: "existing-action", script: "update", actor: c.member, change: func(data *domain.Bootstrap) {
			data.CommentActions = append(data.CommentActions, domain.CommentAction{ID: "ca-existing", IssueID: c.parent, CommentID: c.comment.ID, Status: "dismissed", Action: "priority", CreatedAt: c.at})
		}},
		{name: "comment-changed-before-append", script: "update", actor: c.member, before: func(t *testing.T, service *server) {
			changed := c.comment
			changed.Body, changed.Version = "Never mind.", 4
			raw, _ := json.Marshal(changed)
			f.exec(t, `UPDATE workspace_content_records SET data=? WHERE workspace_key=? AND kind='comment' AND resource_id=? AND id=?`, raw, workspace, c.issue, c.comment.ID)
		}},
		{name: "setting-off-before-append", script: "update", actor: c.member, before: func(t *testing.T, service *server) {
			c.race(t, service, func(data *domain.Bootstrap) {
				off := false
				settings := data.UserSettings[c.member.ID]
				settings.SuggestedActionsOnComments = &off
				data.UserSettings[c.member.ID] = settings
			})
		}},
		{name: "action-added-before-append", script: "update", actor: c.member, before: func(t *testing.T, service *server) {
			c.race(t, service, func(data *domain.Bootstrap) {
				data.CommentActions = append(data.CommentActions, domain.CommentAction{ID: "ca-raced", IssueID: c.issue, CommentID: c.comment.ID, Status: "pending", Action: "priority", CreatedAt: c.at})
			})
		}},
		{name: "issue-archived-before-append", script: "update", actor: c.member, before: func(t *testing.T, service *server) {
			c.race(t, service, func(data *domain.Bootstrap) {
				for index := range data.Issues {
					if data.Issues[index].ID == c.issue {
						data.Issues[index].ArchivedAt = &c.at
					}
				}
			})
		}},
		{name: "parent-only-grant", script: "update", actor: c.guest, change: func(data *domain.Bootstrap) {
			for index := range data.Issues {
				switch data.Issues[index].ID {
				case c.parent:
					data.Issues[index].Permissions = []domain.IssuePermission{{ID: "ca-parent-grant", IssueID: c.parent, SubjectType: "user", SubjectID: c.guest.ID, Role: "editor", CreatedAt: c.at, UpdatedAt: c.at}}
				}
			}
			for index := range data.TeamMembers {
				if data.TeamMembers[index].UserID == c.guest.ID {
					data.TeamMembers[index].TeamID += "-elsewhere"
				}
			}
			for index := range data.Comments[c.issue] {
				if data.Comments[c.issue][index].ID == c.comment.ID {
					data.Comments[c.issue][index].User = c.guest
				}
			}
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			f.scaleP21TriageWriterFixture.prepare = func(t *testing.T) { c.seed(t, test.change) }
			defer func() { f.scaleP21TriageWriterFixture.prepare = nil }()
			outcomes := f.writerDifferential(t, test.name, func(service *server) (*httptest.ResponseRecorder, any) {
				provider.configure(service)
				provider.mu.Lock()
				provider.calls, provider.requests = proposals[test.script], nil
				provider.before = nil
				if test.before != nil {
					provider.before = func() { test.before(t, service) }
				}
				provider.mu.Unlock()
				ctx := store.ContextWithActor(t.Context(), test.actor)
				service.generateCommentActions(ctx, workspace, c.issue, c.comment.ID, test.actor)
				provider.mu.Lock()
				defer provider.mu.Unlock()
				return nil, provider.requests
			})
			legacy := outcomes[0]
			t.Logf("legacy: %d events, %d model requests", len(legacy.State.Events), len(legacy.Extra.([]json.RawMessage)))
			// parent-only-grant: the guest edits only through the parent's
			// grant, which the read inherits; the claim and append must too.
			if test.name == "update" || test.name == "link-subissue" || test.name == "parent-only-grant" {
				if len(legacy.State.Events) != 2 || !strings.Contains(string(legacy.Installed), "comment_action_") {
					t.Fatalf("legacy did not claim and append: %v", legacy.State.Events)
				}
			}
		})
	}
}

// The claim callback's own races: a comment stamped or rewritten between the
// generation read and the claim transaction.
func TestScaleP21CommentActionClaimRaceDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, narrow := range []string{"off", "on"} {
		t.Run("narrow-"+narrow, func(t *testing.T) {
			t.Setenv("FLOW_NARROW_MUTATION_INPUTS", narrow)
			c := scaleP21CommentActionFixture(t)
			f, workspace := c.f, c.f.data.Workspace.URLKey
			for _, shape := range []string{"claims", "stamped", "body-changed", "missing-comment", "not-allowed"} {
				t.Run(shape, func(t *testing.T) {
					f.scaleP21TriageWriterFixture.prepare = func(t *testing.T) {
						c.seed(t, func(data *domain.Bootstrap) {
							for index := range data.Comments[c.issue] {
								comment := &data.Comments[c.issue][index]
								if comment.ID != c.comment.ID {
									continue
								}
								switch shape {
								case "stamped":
									comment.ActionsGeneratedAt = &c.at
								case "body-changed":
									comment.Body = "Rewritten"
								case "missing-comment":
									comment.ID = "ca-other"
								}
							}
							if shape == "not-allowed" {
								off := false
								settings := data.UserSettings[c.member.ID]
								settings.SuggestedActionsOnComments = &off
								data.UserSettings[c.member.ID] = settings
							}
						})
					}
					defer func() { f.scaleP21TriageWriterFixture.prepare = nil }()
					outcomes := f.writerDifferential(t, shape, func(service *server) (*httptest.ResponseRecorder, any) {
						service.agent = appconfig.AgentConfig{Enabled: true, ToolsEnabled: true, WriteTools: true}
						ctx, closeOutput := beginIndependentMetadataOutputReads(store.ContextWithActor(t.Context(), c.member))
						defer closeOutput()
						claimed := false
						claimCtx := store.WithIssueDiscussionMutation(ctx, c.issue, c.comment.ID)
						claimCtx = store.ContextWithMutationResults(claimCtx, &claimed)
						if service.store.BoundedMetadataEnabled() {
							claimCtx = store.ContextWithCommentActionProposal(claimCtx, store.CommentActionProposal{IssueID: c.issue, CommentID: c.comment.ID})
						}
						err := service.store.MutateWorkspace(claimCtx, workspace, "comment.actions_started", c.issue, nil, func(next *domain.Bootstrap) error {
							claimed = false
							current, e := issueByID(next, c.issue)
							if e != nil || !service.commentActionsAllowed(*next, *current) {
								return store.ErrNoMutation
							}
							for i, comment := range next.Comments[c.issue] {
								if comment.ID == c.comment.ID && comment.ActionsGeneratedAt == nil && comment.Body == c.comment.Body {
									now := time.Now().UTC()
									next.Comments[c.issue][i].ActionsGeneratedAt = &now
									claimed = true
									return nil
								}
							}
							return store.ErrNoMutation
						})
						return nil, map[string]any{"claimed": claimed, "error": scaleP21ChatPointError(err)}
					})
					legacy := outcomes[0].Extra.(map[string]any)
					if (shape == "claims") != (legacy["claimed"] == true) {
						t.Fatalf("legacy claim outcome %v", legacy)
					}
				})
			}
		})
	}
}

func (c scaleP21CommentActionSetup) resolveRequest(service *server, user domain.User, issueID, actionID, status string) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(map[string]string{"status": status})
	r := httptest.NewRequest(http.MethodPost, "/api/issues/"+issueID+"/comment-actions/"+actionID, bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	workspace := c.f.data.Workspace.URLKey
	r.Header.Set("X-Workspace-Key", workspace)
	r.SetPathValue("workspaceKey", workspace)
	r.SetPathValue("workspace", workspace)
	r.SetPathValue("id", issueID)
	r.SetPathValue("actionId", actionID)
	ctx := context.WithValue(r.Context(), authUserContextKey{}, user)
	ctx = store.ContextWithActor(ctx, user)
	r = r.WithContext(ctx)
	r, closeOutput := beginRequestResourceReads(r)
	defer closeOutput()
	w := httptest.NewRecorder()
	service.resolveCommentAction(w, r)
	return w
}

func TestScaleP21CommentActionResolutionDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, narrow := range []string{"off", "on"} {
		t.Run("narrow-"+narrow, func(t *testing.T) {
			t.Setenv("FLOW_NARROW_MUTATION_INPUTS", narrow)
			scaleP21CommentActionResolution(t)
		})
	}
}

func scaleP21CommentActionResolution(t *testing.T) {
	c := scaleP21CommentActionFixture(t)
	f := c.f
	priority, same := 1, 3
	labels := []string{"triage-l-public"}
	assignee, stateB, stale := c.owner.ID, c.stateB, c.stateA
	action := func(id, kind, status string, version int64, arguments domain.CommentActionArguments) domain.CommentAction {
		return domain.CommentAction{ID: id, IssueID: c.issue, CommentID: c.comment.ID, CommentVersion: version, TargetUserID: c.member.ID,
			Status: status, Action: kind, Arguments: arguments, CreatedAt: c.at}
	}
	actions := []domain.CommentAction{
		action("ca-priority", "priority", "pending", 3, domain.CommentActionArguments{Priority: &priority}),
		action("ca-status", "status", "pending", 3, domain.CommentActionArguments{StateID: &stateB}),
		action("ca-assignee", "assignee", "pending", 3, domain.CommentActionArguments{AssigneeID: &assignee}),
		action("ca-labels", "labels", "pending", 3, domain.CommentActionArguments{LabelIDs: &labels}),
		action("ca-link", "link", "pending", 3, domain.CommentActionArguments{URL: "https://example.test/spec", Title: "Spec"}),
		action("ca-subissue", "subIssue", "pending", 3, domain.CommentActionArguments{Title: "Follow up", Description: "Details"}),
		action("ca-accepted", "priority", "accepted", 3, domain.CommentActionArguments{Priority: &priority}),
		action("ca-dismissed", "priority", "dismissed", 3, domain.CommentActionArguments{Priority: &priority}),
		action("ca-stale", "priority", "pending", 2, domain.CommentActionArguments{Priority: &priority}),
		action("ca-same", "priority", "pending", 3, domain.CommentActionArguments{Priority: &same}),
		action("ca-same-state", "status", "pending", 3, domain.CommentActionArguments{StateID: &stale}),
		action("ca-no-comment", "priority", "pending", 3, domain.CommentActionArguments{Priority: &priority}),
	}
	actions[11].CommentID = "ca-missing-comment"
	// The first occurrence of an ID belongs to another issue; the callback
	// resolves the first one naming this issue.
	shadow := action("ca-priority", "priority", "pending", 3, domain.CommentActionArguments{Priority: &same})
	shadow.IssueID = c.parent
	cases := []struct {
		name, action, status string
		user                 domain.User
		change               func(*domain.Bootstrap)
		code                 int
	}{
		{name: "accept-priority", action: "ca-priority", status: "accepted", code: 200},
		{name: "accept-status", action: "ca-status", status: "accepted", code: 200},
		{name: "accept-assignee", action: "ca-assignee", status: "accepted", code: 200},
		{name: "accept-labels", action: "ca-labels", status: "accepted", code: 200},
		{name: "accept-link", action: "ca-link", status: "accepted", code: 200},
		{name: "accept-subissue", action: "ca-subissue", status: "accepted", code: 200},
		{name: "dismiss", action: "ca-priority", status: "dismissed", code: 200},
		{name: "accepted-again", action: "ca-accepted", status: "accepted", code: 200},
		{name: "accept-dismissed", action: "ca-dismissed", status: "accepted", code: 409},
		{name: "accept-stale", action: "ca-stale", status: "accepted", code: 409},
		{name: "dismiss-stale", action: "ca-stale", status: "dismissed", code: 200},
		{name: "accept-invalid", action: "ca-same", status: "accepted", code: 409},
		{name: "accept-same-state", action: "ca-same-state", status: "accepted", code: 409},
		{name: "missing-comment", action: "ca-no-comment", status: "accepted", code: 404},
		{name: "missing-action", action: "ca-unknown", status: "accepted", code: 404},
		{name: "setting-off", action: "ca-priority", status: "accepted", code: 403, change: func(data *domain.Bootstrap) {
			off := false
			settings := data.UserSettings[c.member.ID]
			settings.SuggestedActionsOnComments = &off
			data.UserSettings[c.member.ID] = settings
		}},
		{name: "subissue-rolls-back", action: "ca-subissue", status: "accepted", change: func(data *domain.Bootstrap) {
			for index := range data.Teams {
				if data.Teams[index].ID == f.public {
					data.Teams[index].RetiredAt = &c.at
				}
			}
		}},
		{name: "owner-accepts-status", action: "ca-status", status: "accepted", user: c.owner, code: 200},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			user := test.user
			if user.ID == "" {
				user = c.member
			}
			f.scaleP21TriageWriterFixture.prepare = func(t *testing.T) {
				c.seed(t, func(data *domain.Bootstrap) {
					data.CommentActions = append(append(data.CommentActions, shadow), actions...)
					if test.change != nil {
						test.change(data)
					}
				})
			}
			defer func() { f.scaleP21TriageWriterFixture.prepare = nil }()
			outcomes := f.writerDifferential(t, test.name, func(service *server) (*httptest.ResponseRecorder, any) {
				service.agent = appconfig.AgentConfig{Enabled: true, ToolsEnabled: true, WriteTools: true}
				return c.resolveRequest(service, user, c.issue, test.action, test.status), nil
			})
			legacy := outcomes[0]
			t.Logf("legacy %d %s", legacy.Code, legacy.Body)
			if test.code != 0 && legacy.Code != test.code {
				t.Fatalf("legacy status %d, want %d: %s", legacy.Code, test.code, legacy.Body)
			}
			if legacy.Code != http.StatusOK && len(legacy.State.Events) != 0 {
				t.Fatalf("legacy failure published %d events", len(legacy.State.Events))
			}
		})
	}
}
