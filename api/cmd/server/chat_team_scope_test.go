package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// A team limited to its members or owners is not visible to non-members, so
// group bindings treat it as private like private or restricted access (on the
// team or a parent); each kind has its own blocked reason.
func TestChatTeamPrivacyIncludesMembershipRestriction(t *testing.T) {
	data := domain.Bootstrap{
		Teams: []domain.Team{{ID: "open"}, {ID: "members"}, {ID: "owners"}, {ID: "access-private"}, {ID: "access-restricted"}, {ID: "flag-private", Private: true}, {ID: "child-of-private"}, {ID: "child-of-owners"}, {ID: "owners-under-private"}},
		TeamSettings: map[string]domain.TeamSettings{
			"open":                 {Access: "public", MembershipRestriction: "open"},
			"members":              {Access: "public", MembershipRestriction: " Members "},
			"owners":               {Access: "public", MembershipRestriction: "owners"},
			"access-private":       {Access: " Private ", MembershipRestriction: "open"},
			"access-restricted":    {Access: "restricted"},
			"child-of-private":     {ParentTeamID: "access-private"},
			"child-of-owners":      {ParentTeamID: "owners", MembershipRestriction: "open"},
			"owners-under-private": {ParentTeamID: "flag-private", MembershipRestriction: "OWNERS"},
		},
	}
	for id, want := range map[string]string{
		"open": "", "members": domain.ChatBindingBlockedTeamMembersOnly, "owners": domain.ChatBindingBlockedTeamMembersOnly, "child-of-owners": domain.ChatBindingBlockedTeamMembersOnly,
		"access-private": domain.ChatBindingBlockedTeamPrivate, "access-restricted": domain.ChatBindingBlockedTeamPrivate, "flag-private": domain.ChatBindingBlockedTeamPrivate,
		"child-of-private": domain.ChatBindingBlockedTeamPrivate, "owners-under-private": domain.ChatBindingBlockedTeamPrivate,
	} {
		active, private := chatTeamState(data, id)
		if !active || private != (want != "") {
			t.Errorf("team %s: active=%t private=%t, want reason %q", id, active, private, want)
		}
		binding := domain.ChatBinding{TeamID: id}
		if reason := chatBindingScopeBlock(data, binding); reason != want {
			t.Errorf("team %s without private authorization: reason=%q want %q", id, reason, want)
		}
		binding.Authorization = &domain.ChatBindingAuthorization{PrivateTeam: true}
		if reason := chatBindingScopeBlock(data, binding); reason != "" {
			t.Errorf("team %s with private authorization: reason=%q", id, reason)
		}
		if chatPublicTeam(data, id) != (want == "") {
			t.Errorf("chatPublicTeam(%s)=%t", id, want != "")
		}
	}
	stamp := time.Now()
	for _, test := range []struct {
		name    string
		data    func(*domain.Bootstrap)
		binding func(*domain.ChatBinding)
		reason  string
	}{
		{"hipaa", func(data *domain.Bootstrap) { data.WorkspaceSettings.HIPAACompliance = true }, nil, domain.ChatBindingBlockedHIPAA},
		{"revoked", nil, func(binding *domain.ChatBinding) { binding.RevokedAt = &stamp }, domain.ChatBindingBlockedRevoked},
		{"no-team", nil, func(binding *domain.ChatBinding) { binding.TeamID = "" }, domain.ChatBindingBlockedTeamInactive},
		{"missing-team", nil, func(binding *domain.ChatBinding) { binding.TeamID = "gone" }, domain.ChatBindingBlockedTeamInactive},
		{"archived", func(data *domain.Bootstrap) { data.Teams[0].ArchivedAt = &stamp }, nil, domain.ChatBindingBlockedTeamInactive},
		{"archived-project", func(data *domain.Bootstrap) {
			data.Projects = []domain.Project{{ID: "p", TeamIDs: []string{"open"}, ArchivedAt: &stamp}}
		}, func(binding *domain.ChatBinding) { binding.ProjectIDs = []string{"p"} }, domain.ChatBindingBlockedProjects},
		{"project-with-private-team", func(data *domain.Bootstrap) {
			data.Projects = []domain.Project{{ID: "p", TeamIDs: []string{"open", "flag-private"}}}
		}, func(binding *domain.ChatBinding) { binding.ProjectIDs = []string{"p"} }, domain.ChatBindingBlockedProjects},
		{"project-with-members-only-team", func(data *domain.Bootstrap) {
			data.Projects = []domain.Project{{ID: "p", TeamIDs: []string{"open", "members"}}}
		}, func(binding *domain.ChatBinding) { binding.ProjectIDs = []string{"p"} }, domain.ChatBindingBlockedProjects},
		{"project-with-public-team", func(data *domain.Bootstrap) {
			data.Projects = []domain.Project{{ID: "p", TeamIDs: []string{"open"}}}
		}, func(binding *domain.ChatBinding) { binding.ProjectIDs = []string{"p"} }, ""},
		{"template-missing", nil, func(binding *domain.ChatBinding) { binding.TemplateID = "missing" }, domain.ChatBindingBlockedTemplate},
		{"template-form", func(data *domain.Bootstrap) {
			data.IssueTemplates = []domain.IssueTemplate{{ID: "form", TemplateType: "form"}}
		}, func(binding *domain.ChatBinding) { binding.TemplateID = "form" }, domain.ChatBindingBlockedTemplate},
	} {
		current := data
		current.Teams = append([]domain.Team(nil), data.Teams...)
		if test.data != nil {
			test.data(&current)
		}
		binding := domain.ChatBinding{TeamID: "open"}
		if test.binding != nil {
			test.binding(&binding)
		}
		if reason := chatBindingScopeBlock(current, binding); reason != test.reason {
			t.Errorf("%s: reason=%q want %q", test.name, reason, test.reason)
		}
		if chatBindingScopePermitted(current, binding) != (test.reason == "") {
			t.Errorf("%s: permitted disagrees with reason", test.name)
		}
		var headers []store.ChatEventTemplateHeader
		for _, template := range current.IssueTemplates {
			headers = append(headers, store.ChatEventTemplateHeader{ID: template.ID, TemplateType: template.TemplateType, FormFields: len(template.FormFields), TeamID: template.TeamID, VisibilityTeamID: template.VisibilityTeamID, ProjectID: template.ProjectID})
		}
		if reason := chatEventBindingScopeBlock(current, binding, headers); reason != test.reason {
			t.Errorf("%s: bounded reason=%q want %q", test.name, reason, test.reason)
		}
	}
}

type chatSkipLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *chatSkipLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *chatSkipLog) take() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	text := l.buf.String()
	l.buf.Reset()
	return text
}

func captureChatSkipLog(t *testing.T) *chatSkipLog {
	log := &chatSkipLog{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(log, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return log
}

// Group notifications of a team limited to its members or owners, like a
// private team (or a team under a private parent), need the binding's
// private-team authorization; without it they are skipped with their reason. Every policy skip is logged and kept on
// the binding, the settings list shows the current blocked reason, and bounded
// and legacy metadata modes decide identically.
func TestChatBindingSkipReasonsBoundedAndLegacy(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_CONNECTOR_SECRET_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	logs := captureChatSkipLog(t)
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		const secretTitle = "Chat scope confidential title"
		issue := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records", map[string]any{"title": secretTitle, "teamId": f.public}, workspace, http.StatusCreated)
		otherTeam := idgen.New("chat-scope-other-")
		parent := idgen.New("chat-scope-parent-")
		if err := repo.MutateWorkspace(t.Context(), workspace, "chat_scope.seed", "", nil, func(data *domain.Bootstrap) error {
			data.Teams = append(data.Teams, domain.Team{ID: otherTeam, Key: otherTeam}, domain.Team{ID: parent, Key: parent, Private: true})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		other := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records", map[string]any{"title": "Other team issue", "teamId": otherTeam}, workspace, http.StatusCreated)
		actor := f.principals[2].user
		at := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
		for _, test := range []struct {
			name     string
			settings domain.TeamSettings
			private  bool
			retired  bool
			hipaa    bool
			// authorizer authorizes the binding for a private team.
			authorizer string
			template   string
			otherTeam  bool
			jobs       int
			skipped    string
			blocked    string
		}{
			{name: "public-open", settings: domain.TeamSettings{Access: "public", MembershipRestriction: "open"}, jobs: 1},
			{name: "public-members", settings: domain.TeamSettings{Access: "public", MembershipRestriction: "members"}, skipped: domain.ChatBindingBlockedTeamMembersOnly, blocked: domain.ChatBindingBlockedTeamMembersOnly},
			{name: "public-owners", settings: domain.TeamSettings{Access: "public", MembershipRestriction: "owners"}, skipped: domain.ChatBindingBlockedTeamMembersOnly, blocked: domain.ChatBindingBlockedTeamMembersOnly},
			{name: "members-authorized", settings: domain.TeamSettings{Access: "public", MembershipRestriction: "members"}, authorizer: f.data.Viewer.ID, jobs: 1},
			{name: "owners-authorized", settings: domain.TeamSettings{Access: "public", MembershipRestriction: "owners"}, authorizer: f.data.Viewer.ID, jobs: 1},
			{name: "owners-authorizer-lost", settings: domain.TeamSettings{Access: "public", MembershipRestriction: "owners"}, authorizer: actor.ID, skipped: domain.ChatBindingBlockedAuthorizer, blocked: domain.ChatBindingBlockedAuthorizer},
			{name: "access-private", settings: domain.TeamSettings{Access: "private"}, skipped: domain.ChatBindingBlockedTeamPrivate, blocked: domain.ChatBindingBlockedTeamPrivate},
			{name: "access-restricted", settings: domain.TeamSettings{Access: "restricted", MembershipRestriction: "owners"}, skipped: domain.ChatBindingBlockedTeamPrivate, blocked: domain.ChatBindingBlockedTeamPrivate},
			{name: "team-private", settings: domain.TeamSettings{Access: "public"}, private: true, skipped: domain.ChatBindingBlockedTeamPrivate, blocked: domain.ChatBindingBlockedTeamPrivate},
			{name: "private-parent", settings: domain.TeamSettings{Access: "public", MembershipRestriction: "owners", ParentTeamID: parent}, skipped: domain.ChatBindingBlockedTeamPrivate, blocked: domain.ChatBindingBlockedTeamPrivate},
			{name: "private-authorized", settings: domain.TeamSettings{Access: "private"}, authorizer: f.data.Viewer.ID, jobs: 1},
			{name: "authorizer-lost", settings: domain.TeamSettings{Access: "private"}, authorizer: actor.ID, skipped: domain.ChatBindingBlockedAuthorizer, blocked: domain.ChatBindingBlockedAuthorizer},
			{name: "retired", retired: true, skipped: domain.ChatBindingBlockedTeamInactive, blocked: domain.ChatBindingBlockedTeamInactive},
			{name: "hipaa", hipaa: true, skipped: domain.ChatBindingBlockedHIPAA, blocked: domain.ChatBindingBlockedHIPAA},
			{name: "template-missing", template: "chat-scope-missing-template", skipped: domain.ChatBindingBlockedTemplate, blocked: domain.ChatBindingBlockedTemplate},
			// An event for another team's issue is not this binding's skip.
			{name: "other-team", settings: domain.TeamSettings{Access: "private"}, otherTeam: true, blocked: domain.ChatBindingBlockedTeamPrivate},
		} {
			t.Run(test.name, func(t *testing.T) {
				if err := repo.MutateWorkspace(t.Context(), workspace, "chat_scope.variant", "", nil, func(data *domain.Bootstrap) error {
					data.WorkspaceSettings.HIPAACompliance = test.hipaa
					for index := range data.Teams {
						if data.Teams[index].ID == f.public {
							data.Teams[index].Private = test.private
							data.Teams[index].RetiredAt = nil
							if test.retired {
								data.Teams[index].RetiredAt = &at
							}
						}
					}
					settings := test.settings
					settings.TeamID = f.public
					data.TeamSettings[f.public] = settings
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
					t.Fatal(err)
				}
				record := domain.ChatIntegrationRecord{WorkspaceKey: workspace, Provider: "lark", ConnectionID: idgen.New("chat-scope-connection-"), AppID: idgen.New("chat-scope-app-"), TenantKey: "chat-scope-tenant", Connected: true}
				binding := domain.ChatBinding{ID: idgen.New("chat-scope-binding-"), ConnectionID: record.ConnectionID, ChatID: "chat-scope-group", TeamID: f.public, TemplateID: test.template, Enabled: true, CreatedAt: at.Add(-time.Minute), Events: []string{"issue.created"}}
				if test.authorizer != "" {
					binding.Authorization = &domain.ChatBindingAuthorization{UserID: test.authorizer, Role: domain.ChatBindingRoleTeamOwner, PrivateTeam: true, AuthorizedAt: at}
				}
				event := domain.DomainEvent{ID: idgen.New("chat-scope-event-"), Type: "issue.created", AggregateID: issue.ID, CreatedAt: at}
				if test.otherTeam {
					event.AggregateID = other.ID
				}
				type outcome struct {
					Jobs        int
					Skipped     string
					SkipEventID string
					SkipType    string
					Blocked     string
				}
				var outputs []outcome
				t.Cleanup(func() { _ = repo.DeleteChatIntegration(context.Background(), workspace, record.ConnectionID) })
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					if err := repo.PutChatIntegration(t.Context(), record); err != nil {
						t.Fatal(err)
					}
					if err := repo.PutChatBinding(t.Context(), workspace, binding); err != nil {
						t.Fatal(err)
					}
					logs.take()
					service := &server{store: source}
					if err := service.queueChatDomainEvent(t.Context(), workspace, event); err != nil {
						t.Fatal(err)
					}
					jobs, err := repo.ClaimChatJobs(t.Context(), workspace, "notification", 100, time.Now().Add(time.Second))
					if err != nil {
						t.Fatal(err)
					}
					stored, err := repo.ChatBindings(t.Context(), workspace, record.ConnectionID)
					if err != nil || len(stored) != 1 {
						t.Fatal("stored bindings", len(stored), err)
					}
					views, err := service.chatBindingViews(t.Context(), workspace, stored)
					if err != nil || len(views) != 1 {
						t.Fatal("binding views", len(views), err)
					}
					result := outcome{Jobs: len(jobs), Blocked: views[0].BlockedReason}
					if skip := stored[0].LastSkipped; skip != nil {
						result.Skipped, result.SkipEventID, result.SkipType = skip.Reason, skip.EventID, skip.EventType
						if skip.At.IsZero() || views[0].LastSkipped == nil || *views[0].LastSkipped != *skip {
							t.Fatal("skip time missing or not listed", skip)
						}
					}
					if result.Jobs != test.jobs || result.Skipped != test.skipped || result.Blocked != test.blocked {
						t.Fatalf("source bounded=%t: %+v, want jobs=%d skipped=%q blocked=%q", source.BoundedMetadataEnabled(), result, test.jobs, test.skipped, test.blocked)
					}
					text := logs.take()
					if test.skipped != "" {
						if result.SkipEventID != event.ID || result.SkipType != event.Type {
							t.Fatalf("skip record names the wrong event: %+v", result)
						}
						for _, want := range []string{"chat binding skipped event", "reason=" + test.skipped, "binding_id=" + binding.ID, "event_id=" + event.ID, "event_type=issue.created", "provider=lark", "workspace=" + workspace} {
							if !strings.Contains(text, want) {
								t.Fatalf("skip log lacks %q: %s", want, text)
							}
						}
						if strings.Contains(text, secretTitle) || strings.Contains(text, "chat-scope-group") {
							t.Fatal("skip log carries content or the group", text)
						}
					} else if strings.Contains(text, "chat binding skipped event") {
						t.Fatalf("unexpected skip log: %s", text)
					}
					if source.MetadataWorkUsedForTesting() != 0 {
						t.Fatal("leaked metadata work")
					}
					outputs = append(outputs, result)
					if err := repo.DeleteChatIntegration(t.Context(), workspace, record.ConnectionID); err != nil {
						t.Fatal(err)
					}
				}
				if !reflect.DeepEqual(outputs[0], outputs[1]) {
					t.Fatal("bounded and legacy differ", outputs[0], outputs[1])
				}
			})
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("decoded complete metadata")
		}
	}
}

// The settings API lists each binding's current blocked reason and its last
// skipped event; a save clears the skip and never takes one from the client.
// Saving a binding of a team limited to its members records the same
// private-team authorization as a private team, so it delivers.
func TestChatBindingSettingsShowBlockedReason(t *testing.T) {
	provider := "feishu"
	f := newChatPermissionFixture(t, provider)
	f.authorizeTenant(provider)
	base := "/api/integrations/" + provider + "/chat"
	setTeam := func(settings domain.TeamSettings) {
		t.Helper()
		if err := f.s.store.MutateWorkspace(t.Context(), "test-workspace", "team.updated", f.publicTeam, nil, func(data *domain.Bootstrap) error {
			current := data.TeamSettings[f.publicTeam]
			current.Access, current.MembershipRestriction = settings.Access, settings.MembershipRestriction
			data.TeamSettings[f.publicTeam] = current
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	settings := func() map[string]any {
		t.Helper()
		return f.expect(f.admin, "GET", base, nil, 200)
	}
	listed := func() map[string]any {
		t.Helper()
		items, _ := settings()["bindings"].([]any)
		if len(items) != 1 {
			t.Fatalf("bindings: %v", items)
		}
		return items[0].(map[string]any)
	}
	teamListed := func(field string) bool {
		t.Helper()
		teams := settings()["permissions"].(map[string]any)["teams"].(map[string]any)
		ids, _ := teams[field].([]any)
		for _, id := range ids {
			if id == f.publicTeam {
				return true
			}
		}
		return false
	}
	setTeam(domain.TeamSettings{Access: "public", MembershipRestriction: "open"})
	if teamListed("privateTeamIds") || teamListed("membersOnlyTeamIds") {
		t.Fatal("open public team listed as private")
	}
	input := chatAuthorizedBinding(domain.ChatBinding{ChatID: "oc_internal", ChatName: "Vault room", TeamID: f.publicTeam, Events: []string{"issue.created"}, Enabled: true})
	input.LastSkipped = &domain.ChatBindingSkip{Reason: "forged", EventID: "forged"}
	saved := f.expect(f.admin, "POST", base+"/bindings", input, 200)
	if authorization := saved["authorization"].(map[string]any); authorization["privateTeam"] != false || saved["lastSkipped"] != nil {
		t.Fatalf("open team saved as private or with a client skip: %v", saved)
	}
	if item := listed(); item["blockedReason"] != nil || item["lastSkipped"] != nil {
		t.Fatalf("deliverable binding listed as blocked: %v", item)
	}
	// The team is limited to its owners after the binding was approved as a
	// public team's: it stops, and the list says why before any event.
	setTeam(domain.TeamSettings{Access: "public", MembershipRestriction: "owners"})
	if item := listed(); item["blockedReason"] != domain.ChatBindingBlockedTeamMembersOnly {
		t.Fatalf("members-only team binding without authorization: %v", item)
	}
	// The editor gets the team as private and members-only, so it asks for
	// the same approval and says why.
	if !teamListed("privateTeamIds") || !teamListed("membersOnlyTeamIds") {
		t.Fatal("members-only team not listed among private and members-only teams")
	}
	setTeam(domain.TeamSettings{Access: "private"})
	if !teamListed("privateTeamIds") || teamListed("membersOnlyTeamIds") {
		t.Fatal("private team listed as members-only")
	}
	if item := listed(); item["blockedReason"] != domain.ChatBindingBlockedTeamPrivate {
		t.Fatalf("private team binding without authorization: %v", item)
	}
	skip := domain.ChatBindingSkip{Reason: domain.ChatBindingBlockedTeamPrivate, EventID: "evt_skipped", EventType: "issue.created", At: time.Now().UTC().Truncate(time.Second)}
	if err := f.s.store.RecordChatBindingSkip(t.Context(), "test-workspace", f.record(provider).ConnectionID, saved["id"].(string), skip); err != nil {
		t.Fatal(err)
	}
	item := listed()
	last, _ := item["lastSkipped"].(map[string]any)
	if last["reason"] != skip.Reason || last["eventId"] != skip.EventID || last["eventType"] != skip.EventType || last["at"] == nil {
		t.Fatalf("last skipped event not listed: %v", item)
	}
	// Recording a skip leaves every other stored field as it was.
	if item["chatName"] != "Vault room" || item["teamId"] != f.publicTeam || item["enabled"] != true {
		t.Fatalf("skip record changed the binding: %v", item)
	}
	if err := f.s.store.RecordChatBindingSkip(t.Context(), "test-workspace", f.record(provider).ConnectionID, "missing-binding", skip); err != nil {
		t.Fatal("missing binding", err)
	}
	// Approving it again for the members-only team records a private-team
	// authorization: the binding delivers and the old skip is cleared.
	setTeam(domain.TeamSettings{Access: "public", MembershipRestriction: "members"})
	update := chatAuthorizedBinding(domain.ChatBinding{ID: saved["id"].(string), ChatID: "oc_internal", ChatName: "Vault room", TeamID: f.publicTeam, Events: []string{"issue.created"}, Enabled: true})
	resaved := f.expect(f.admin, "POST", base+"/bindings", update, 200)
	if authorization := resaved["authorization"].(map[string]any); authorization["privateTeam"] != true {
		t.Fatalf("members-only team approved without private-team authorization: %v", resaved)
	}
	if item := listed(); item["blockedReason"] != nil || item["lastSkipped"] != nil {
		t.Fatalf("approved members-only binding still blocked or keeps the old skip: %v", item)
	}
}
