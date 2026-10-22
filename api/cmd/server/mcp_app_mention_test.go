package main

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type mcpMentionApp struct {
	user     domain.User
	id       string
	receiver *webhookReceiver
}

type mcpMentionFixture struct {
	s       *server
	handler http.Handler
	seed    domain.Bootstrap
	issue   domain.Issue
}

func newMCPMentionFixture(t *testing.T) *mcpMentionFixture {
	t.Helper()
	s, handler, seed, _ := webhookTestServer(t)
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues?workspace=test-workspace", map[string]any{"title": "Mention target", "teamId": seed.Teams[0].ID}, http.StatusCreated)
	return &mcpMentionFixture{s: s, handler: handler, seed: seed, issue: issue}
}

// install registers an OAuth application with a fake webhook receiver and installs it.
func (f *mcpMentionFixture) install(t *testing.T, name string, scopes []string, active bool, teamIDs []string) mcpMentionApp {
	t.Helper()
	receiver := newWebhookReceiver(t, http.StatusOK)
	app := requestJSON[domain.OAuthApplication](t, f.handler, http.MethodPost, "/api/oauth-applications?workspace=test-workspace", map[string]any{
		"name": name, "redirectUris": []string{"https://bot.example/callback"}, "scopes": []string{"read", "write", "app:assignable", "app:mentionable"},
	}, http.StatusCreated)
	requestJSON[applicationWebhookSettingsResponse](t, f.handler, http.MethodPut, "/api/oauth-applications/"+app.ID+"/webhook?workspace=test-workspace", map[string]any{
		"enabled": true, "url": receiver.server.URL, "resourceTypes": []string{"AgentSessionEvent"},
	}, http.StatusOK)
	installed := requestJSON[struct {
		Application domain.ApplicationInstallation `json:"application"`
	}](t, f.handler, http.MethodPost, "/api/application-installations?workspace=test-workspace", map[string]any{
		"clientId": app.ClientID, "teamIds": teamIDs, "scopes": scopes, "active": active,
	}, http.StatusOK).Application
	f.s.reconcileApplicationWebhooks(context.Background(), "test-workspace")
	user, err := f.s.store.UserByID(t.Context(), installed.UserID)
	if err != nil {
		t.Fatal(err)
	}
	return mcpMentionApp{user: user, id: installed.UserID, receiver: receiver}
}

func (f *mcpMentionFixture) actor(user domain.User, requester string) mcpActor {
	return mcpActor{WorkspaceKey: "test-workspace", User: user, AuthorizationUserID: requester, APIKey: domain.APIKey{Scopes: []string{"read", "write"}}}
}

func (f *mcpMentionFixture) comment(t *testing.T, actor mcpActor, args map[string]any) map[string]any {
	t.Helper()
	result, err := f.s.runFlowTool(store.ContextWithActor(t.Context(), actor.User), actor, "save_comment", args)
	if err != nil {
		t.Fatal(err)
	}
	if comment, ok := result.(domain.Comment); ok {
		out, _ := structToMap(comment)
		return out
	}
	return result.(map[string]any)
}

// sessions counts the sessions started for app and delivers each, returning how many webhooks arrived.
func (f *mcpMentionFixture) sessions(t *testing.T, app mcpMentionApp) (int, int) {
	t.Helper()
	tasks, err := f.s.store.ListAgentTasks(t.Context(), "test-workspace", f.issue.ID, app.id)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		f.s.deliverApplicationTask(t.Context(), task)
	}
	return len(tasks), app.receiver.count()
}

func notesOf(result map[string]any) string {
	notes, _ := result["mentionNotes"].([]string)
	return strings.Join(notes, "\n")
}

func TestMCPSaveCommentAppMentions(t *testing.T) {
	f := newMCPMentionFixture(t)
	full := []string{"read", "write", "app:assignable", "app:mentionable"}
	team := []string{f.seed.Teams[0].ID}
	owner := f.seed.Viewer
	human := f.actor(owner, "")

	valid := f.install(t, "Valid bot", full, true, team)
	noCapability := f.install(t, "Silent bot", []string{"read", "write"}, true, team)
	inactive := f.install(t, "Sleepy bot", full, false, team)
	other := requestJSON[domain.Team](t, f.handler, http.MethodPost, "/api/workspaces/test-workspace/teams?workspace=test-workspace", map[string]any{"name": "Elsewhere", "key": "ELS"}, http.StatusCreated)
	elsewhere := f.install(t, "Remote bot", full, true, []string{other.ID})
	twinA := f.install(t, "Twin", full, true, team)
	twinB := f.install(t, "Twin", full, true, team)

	t.Run("valid app gets structured mention, one session, one delivery", func(t *testing.T) {
		result := f.comment(t, human, map[string]any{"issueId": f.issue.ID, "body": "Hey @Valid bot please look"})
		if got := mentionKinds(result["bodyData"]); !slices.Equal(got, []string{"user:" + valid.id}) {
			t.Fatalf("bodyData mentions = %v (%v)", got, result)
		}
		entries, _ := result["agentMentions"].([]map[string]any)
		if len(entries) != 1 || entries[0]["started"] != true || result["mentionNotes"] != nil {
			t.Fatalf("report: %v", result)
		}
		if sessions, deliveries := f.sessions(t, valid); sessions != 1 {
			t.Fatalf("sessions=%d deliveries=%d", sessions, deliveries)
		}
		valid.receiver.waitFor(t, func(envelope map[string]any, _ http.Header) bool { return envelope["type"] == "AgentSessionEvent" })
		if valid.receiver.count() != 1 {
			t.Fatalf("deliveries = %d", valid.receiver.count())
		}
		// A structured mention from the editor path creates the same kind of session.
		requestJSON[domain.Comment](t, f.handler, http.MethodPost, "/api/issues/"+f.issue.ID+"/comments?workspace=test-workspace", map[string]any{"body": "@Valid bot", "bodyData": map[string]any{"type": "doc", "content": []any{map[string]any{"type": "mention", "attrs": map[string]any{"mentionType": "user", "id": valid.id, "label": "Valid bot"}}}}}, http.StatusCreated)
		if sessions, _ := f.sessions(t, valid); sessions != 2 {
			t.Fatalf("UI + MCP mentions should be two sessions, got %d", sessions)
		}
	})

	for name, tc := range map[string]struct {
		app  mcpMentionApp
		body string
		note string
	}{
		"missing capability": {noCapability, "@Silent bot hi", "app:mentionable"},
		"inactive":           {inactive, "@Sleepy bot hi", "not active"},
		"no team access":     {elsewhere, "@Remote bot hi", "no access to this team"},
		"same name":          {twinA, "@Twin hi", "ambiguous"},
	} {
		t.Run(name, func(t *testing.T) {
			result := f.comment(t, human, map[string]any{"issueId": f.issue.ID, "body": tc.body})
			if result["id"] == nil {
				t.Fatalf("comment not created: %v", result)
			}
			if kinds := mentionKinds(result["bodyData"]); len(kinds) != 0 {
				t.Fatalf("mention node stored: %v", kinds)
			}
			if note := notesOf(result); !strings.Contains(note, tc.note) {
				t.Fatalf("notes %q lack %q (%v)", note, tc.note, result)
			}
			for _, app := range []mcpMentionApp{tc.app, twinB} {
				if sessions, deliveries := f.sessions(t, app); sessions != 0 || deliveries != 0 {
					t.Fatalf("%s: sessions=%d deliveries=%d", app.user.Name, sessions, deliveries)
				}
			}
		})
	}

	t.Run("self mention and app-authored comments start nothing", func(t *testing.T) {
		before, _ := f.sessions(t, valid)
		appActor := f.actor(valid.user, owner.ID)
		result := f.comment(t, appActor, map[string]any{"issueId": f.issue.ID, "body": "@Valid bot do it again"})
		if kinds := mentionKinds(result["bodyData"]); len(kinds) != 0 || !strings.Contains(notesOf(result), "written by an app") {
			t.Fatalf("self mention: %v %v", kinds, result)
		}
		result = f.comment(t, appActor, map[string]any{"issueId": f.issue.ID, "body": "@Silent bot @Twin handoff"})
		if kinds := mentionKinds(result["bodyData"]); len(kinds) != 0 {
			t.Fatalf("app-to-app mention stored: %v", kinds)
		}
		if after, _ := f.sessions(t, valid); after != before {
			t.Fatalf("sessions grew %d -> %d", before, after)
		}
		if sessions, _ := f.sessions(t, noCapability); sessions != 0 {
			t.Fatalf("loop started %d sessions", sessions)
		}
	})

	t.Run("replay of the same comment does not duplicate", func(t *testing.T) {
		before, _ := f.sessions(t, valid)
		created := f.comment(t, human, map[string]any{"issueId": f.issue.ID, "body": "Replay @Valid bot"})
		for range 2 {
			f.comment(t, human, map[string]any{"id": created["id"], "body": "Replay @Valid bot (edited)"})
		}
		if after, _ := f.sessions(t, valid); after != before+1 {
			t.Fatalf("sessions %d -> %d, want exactly one new", before, after)
		}
	})

	t.Run("person mentions keep working", func(t *testing.T) {
		result := f.comment(t, human, map[string]any{"issueId": f.issue.ID, "body": "Thanks @Test member"})
		if got := mentionKinds(result["bodyData"]); !slices.Equal(got, []string{"user:usr_member"}) || result["agentMentions"] != nil || result["mentionNotes"] != nil {
			t.Fatalf("user mention: %v", result)
		}
	})
}
