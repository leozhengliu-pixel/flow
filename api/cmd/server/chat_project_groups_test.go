package main

import (
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func chatProjectGroupNames(t *testing.T, body map[string]any) []string {
	t.Helper()
	names := []string{}
	items, _ := body["groups"].([]any)
	for _, raw := range items {
		item := raw.(map[string]any)
		for key := range item {
			if !slices.Contains([]string{"id", "chatName", "provider"}, key) {
				t.Fatalf("project group discloses %q: %v", key, item)
			}
		}
		names = append(names, item["chatName"].(string))
	}
	slices.Sort(names)
	return names
}

// Anyone who can see a project learns which groups it is connected to (the
// group's name only); someone who cannot see the project gets 404. Only
// active bindings that name the project, of a project team the viewer can
// see, are listed.
func TestChatProjectGroupsFollowProjectVisibility(t *testing.T) {
	provider := "feishu"
	f := newChatPermissionFixture(t, provider)
	base := "/api/integrations/" + provider + "/chat/projects/"
	f.addProjects(
		domain.Project{ID: "pg_public", Name: "Launch", TeamIDs: []string{f.publicTeam}},
		domain.Project{ID: "pg_other", Name: "Other", TeamIDs: []string{f.publicTeam}},
		domain.Project{ID: "pg_private", Name: "Vault work", TeamIDs: []string{f.privateTeam}},
		domain.Project{ID: "pg_shared", Name: "Shared", TeamIDs: []string{f.publicTeam, f.privateTeam}},
	)
	record := f.record(provider)
	now := time.Now().UTC()
	for _, binding := range []domain.ChatBinding{
		{ID: "pg_b_active", ChatID: "oc_1", ChatName: "Release room", TeamID: f.publicTeam, ProjectIDs: []string{"pg_public", "pg_other"}, Events: []string{"issue.created"}, Enabled: true, Authorization: &domain.ChatBindingAuthorization{UserID: f.admin.user.ID}},
		{ID: "pg_b_revoked", ChatID: "oc_2", ChatName: "Old room", TeamID: f.publicTeam, ProjectIDs: []string{"pg_public"}, Events: []string{"issue.created"}, Enabled: false, RevokedAt: &now, RevokedBy: f.admin.user.ID},
		{ID: "pg_b_paused", ChatID: "oc_3", ChatName: "Paused room", TeamID: f.publicTeam, ProjectIDs: []string{"pg_public"}, Events: []string{"issue.created"}, Enabled: false},
		{ID: "pg_b_elsewhere", ChatID: "oc_4", ChatName: "Other room", TeamID: f.publicTeam, ProjectIDs: []string{"pg_other"}, Events: []string{"issue.created"}, Enabled: true},
		{ID: "pg_b_team", ChatID: "oc_5", ChatName: "Team room", TeamID: f.publicTeam, Events: []string{"issue.created"}, Enabled: true},
		{ID: "pg_b_vault", ChatID: "oc_6", ChatName: "Vault room", TeamID: f.privateTeam, ProjectIDs: []string{"pg_private"}, Events: []string{"issue.created"}, Enabled: true},
		{ID: "pg_b_shared_private", ChatID: "oc_7", ChatName: "Vault shared room", TeamID: f.privateTeam, ProjectIDs: []string{"pg_shared"}, Events: []string{"issue.created"}, Enabled: true},
		{ID: "pg_b_shared_public", ChatID: "oc_8", ChatName: "Shared room", TeamID: f.publicTeam, ProjectIDs: []string{"pg_shared"}, Events: []string{"issue.created"}, Enabled: true},
	} {
		binding.ConnectionID = record.ConnectionID
		binding.CreatedAt = now
		if err := f.s.store.PutChatBinding(t.Context(), "test-workspace", binding); err != nil {
			t.Fatal(err)
		}
	}
	check := func(actor chatPermissionActor, project string, canManage bool, want ...string) {
		t.Helper()
		body := f.expect(actor, "GET", base+project+"/groups", nil, 200)
		if got := chatProjectGroupNames(t, body); !slices.Equal(got, want) || body["canManage"] != canManage {
			t.Fatalf("%s on %s: %v canManage=%v, want %v canManage=%v", actor.user.Email, project, got, body["canManage"], want, canManage)
		}
	}

	// A plain workspace member sees the public project's active group, not
	// the revoked or paused ones, one scoped elsewhere or a team-wide one.
	check(f.outsider, "pg_public", false, "Release room")
	check(f.admin, "pg_public", true, "Release room")
	check(f.outsider, "pg_other", false, "Other room", "Release room")
	// The private team's project is hidden from those outside the team.
	f.refusal(f.outsider, "GET", base+"pg_private/groups", nil, 404, "chat_project_not_found", "")
	f.refusal(f.owner, "GET", base+"pg_private/groups", nil, 404, "chat_project_not_found", "")
	check(f.member, "pg_private", false, "Vault room")
	check(f.lead, "pg_private", true, "Vault room")
	// A project shared with a private team never discloses that team's group
	// to those outside it.
	check(f.outsider, "pg_shared", false, "Shared room")
	check(f.member, "pg_shared", false, "Shared room", "Vault shared room")
	// Visibility follows the project as the viewer sees it: an administrator
	// outside the private team manages its bindings in settings, but the
	// project view shows only what they see of the project.
	check(f.admin, "pg_shared", true, "Shared room")
	f.refusal(f.outsider, "GET", base+"pg_missing/groups", nil, 404, "chat_project_not_found", "")
	f.refusal(f.outsider, "GET", "/api/integrations/slack/chat/projects/pg_public/groups", nil, 404, "chat_unsupported_provider", "")
	// No application for this provider: nothing is connected.
	if body := f.expect(f.outsider, "GET", "/api/integrations/lark/chat/projects/pg_public/groups", nil, 200); len(chatProjectGroupNames(t, body)) != 0 {
		t.Fatalf("groups without an application: %v", body)
	}

	// The binding configuration stays with those who manage it.
	if bindings, _ := f.expect(f.outsider, "GET", "/api/integrations/"+provider+"/chat", nil, 200)["bindings"].([]any); len(bindings) != 0 {
		t.Fatalf("member sees binding configuration: %v", bindings)
	}
}

// An initiative's groups: its owner and administrators see the binding
// records; anyone else who sees the initiative learns the active groups'
// names only, and never those of an initiative a private team leads.
func TestChatInitiativeGroupsShowNamesToViewers(t *testing.T) {
	provider := "lark"
	f := newChatPermissionFixture(t, provider)
	base := "/api/integrations/" + provider + "/chat/initiatives/"
	owner := f.lead.user
	if err := f.s.store.MutateWorkspace(t.Context(), "test-workspace", "initiative.created", "ig_open", nil, func(data *domain.Bootstrap) error {
		data.Initiatives = append(data.Initiatives,
			domain.Initiative{ID: "ig_open", Name: "Launch", SlugID: "launch-1", Owner: &owner},
			domain.Initiative{ID: "ig_private", Name: "Vault plan", SlugID: "vault-plan-1", Owner: &owner, LeadTeamID: f.privateTeam},
		)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	record := f.record(provider)
	now := time.Now().UTC()
	authorization := &domain.ChatInitiativeBindingAuthorization{UserID: owner.ID, Role: domain.ChatInitiativeBindingRoleOwner, AuthorizedAt: now}
	for _, binding := range []domain.ChatInitiativeBinding{
		{ID: "ig_b_active", ChatID: "oc_1", ChatName: "Launch room", InitiativeID: "ig_open", Enabled: true, Authorization: authorization},
		{ID: "ig_b_paused", ChatID: "oc_2", ChatName: "Paused room", InitiativeID: "ig_open", Authorization: authorization},
		{ID: "ig_b_revoked", ChatID: "oc_3", ChatName: "Old room", InitiativeID: "ig_open", Authorization: authorization, RevokedAt: &now, RevokedBy: owner.ID},
		{ID: "ig_b_private", ChatID: "oc_4", ChatName: "Vault room", InitiativeID: "ig_private", Enabled: true, Authorization: authorization},
	} {
		binding.ConnectionID = record.ConnectionID
		binding.CreatedAt = now
		if err := f.s.store.PutChatInitiativeBinding(t.Context(), "test-workspace", binding); err != nil {
			t.Fatal(err)
		}
	}
	names := func(actor chatPermissionActor, initiative string) []string {
		t.Helper()
		body := f.expect(actor, "GET", base+initiative+"/bindings", nil, 200)
		out := []string{}
		items, _ := body["bindings"].([]any)
		for _, raw := range items {
			item := raw.(map[string]any)
			if body["canManage"] != true && (item["chatId"] != "" || item["authorization"] != nil || item["revokedAt"] != nil) {
				t.Fatalf("%s sees more than the group's name: %v", actor.user.Email, item)
			}
			out = append(out, item["chatName"].(string))
		}
		slices.Sort(out)
		return out
	}
	if got := names(f.outsider, "ig_open"); !slices.Equal(got, []string{"Launch room"}) {
		t.Fatalf("member view of ig_open: %v", got)
	}
	if got := names(f.lead, "ig_open"); !slices.Equal(got, []string{"Launch room", "Old room", "Paused room"}) {
		t.Fatalf("owner view of ig_open: %v", got)
	}
	// A private team leads ig_private: the outsider cannot see it, and a
	// plain member of the team learns nothing (it is never shared).
	for _, actor := range []chatPermissionActor{f.outsider, f.member} {
		if got := names(actor, "ig_private"); len(got) != 0 {
			t.Fatalf("%s sees a private-led initiative's group: %v", actor.user.Email, got)
		}
	}
}
