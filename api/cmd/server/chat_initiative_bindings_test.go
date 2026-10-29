package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// sentMessages lets the permission fixture's platform accept group messages
// and records them.
func (f *chatPermissionFixture) sentMessages() *[]map[string]any {
	sent := &[]map[string]any{}
	mockChatTransport(f.t, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost && r.URL.Path == "/open-apis/im/v1/messages" {
			raw, _ := io.ReadAll(r.Body)
			var body map[string]any
			_ = json.Unmarshal(raw, &body)
			*sent = append(*sent, body)
			return chatTestResponse(200, `{"code":0,"data":{"message_id":"om_initiative_update"}}`), nil
		}
		return f.platform(r)
	})
	return sent
}

func (f *chatPermissionFixture) deliverInitiativeUpdate(id string, updateID ...string) {
	f.t.Helper()
	var payload json.RawMessage
	if len(updateID) > 0 {
		payload, _ = json.Marshal(childEventPayload{Child: &webhookChild{ID: updateID[0]}})
	}
	if err := f.s.queueChatDomainEvent(f.t.Context(), "test-workspace", domain.DomainEvent{ID: "evt_" + id + "_" + time.Now().Format(time.RFC3339Nano), Type: "initiative.update_created", AggregateID: id, Payload: payload, CreatedAt: time.Now()}); err != nil {
		f.t.Fatal(err)
	}
	jobs, err := f.s.store.ClaimChatJobs(f.t.Context(), "test-workspace", "notification", 10, time.Now().Add(time.Second))
	if err != nil {
		f.t.Fatal(err)
	}
	for _, job := range jobs {
		if err := f.s.processChatJob(f.t.Context(), job); err != nil {
			f.t.Fatal(err)
		}
		if err := f.s.store.FinishChatJob(f.t.Context(), job, "completed", time.Time{}, ""); err != nil {
			f.t.Fatal(err)
		}
	}
}

// An initiative without a lead team connects directly to a group: its owner
// or a workspace administrator authorizes it with the team binding's group
// checks and record, and its updates reach the group without any private
// team's content.
func TestChatInitiativeBindingsConnectInitiativesWithoutALeadTeam(t *testing.T) {
	provider := "lark"
	f := newChatPermissionFixture(t, provider)
	f.authorizeTenant(provider)
	sent := f.sentMessages()
	base := "/api/integrations/" + provider + "/chat"
	owner := f.member.user
	if err := f.s.store.MutateWorkspace(t.Context(), "test-workspace", "initiative.created", "initiative_direct", nil, func(data *domain.Bootstrap) error {
		data.Initiatives = append(data.Initiatives,
			domain.Initiative{ID: "initiative_direct", Name: "Company launch", SlugID: "company-launch-1", Health: "atRisk", Owner: &owner, ContributingTeamIDs: []string{f.privateTeam, f.publicTeam}},
			domain.Initiative{ID: "initiative_private_lead", Name: "Vault plan", SlugID: "vault-plan-1", Owner: &owner, LeadTeamID: f.privateTeam},
		)
		data.InitiativeUpdates["initiative_direct"] = []domain.InitiativeUpdate{
			{ID: "update_newer", InitiativeID: "initiative_direct", Body: "A later update must not replace this event", Health: "offTrack", CreatedAt: time.Now().Add(-time.Minute)},
			{ID: "update_posted", InitiativeID: "initiative_direct", Body: "Launch checklist complete.\nNext: customer rollout.", Health: "atRisk", CreatedAt: time.Now().Add(-2 * time.Minute)},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	binding := map[string]any{"initiativeId": "initiative_direct", "chatId": "oc_internal", "chatName": "Vault room", "enabled": true, "authorize": true}
	with := func(key string, value any) map[string]any {
		copy := map[string]any{}
		for k, v := range binding {
			copy[k] = v
		}
		copy[key] = value
		return copy
	}

	// Who may connect: the initiative's owner and workspace administrators.
	f.refusal(f.outsider, "POST", base+"/initiative-bindings", binding, 403, "chat_initiative_binding_permission_required", "initiative")
	f.refusal(f.outsider, "GET", base+"/chats?initiativeId=initiative_direct", nil, 403, "chat_team_binding_permission_required", "team")
	f.expect(f.member, "GET", base+"/chats?initiativeId=initiative_direct", nil, 200)
	f.refusal(f.member, "GET", base+"/chats", nil, 403, "chat_team_binding_permission_required", "team")
	f.refusal(f.member, "POST", base+"/initiative-bindings", with("authorize", false), 400, "chat_initiative_binding_authorization_required", "initiative")
	// The same group refusals as team bindings.
	for chatID, code := range map[string]string{"oc_external": "chat_group_external", "oc_foreign": "chat_group_other_tenant", "oc_no_bot": "chat_bot_not_in_group"} {
		f.refusal(f.member, "POST", base+"/initiative-bindings", with("chatId", chatID), 400, code, "")
	}
	// A private team leads this one: its updates are the team's content.
	f.refusal(f.admin, "POST", base+"/initiative-bindings", with("initiativeId", "initiative_private_lead"), 400, "chat_initiative_binding_scope", "")
	f.expect(f.member, "POST", base+"/initiative-bindings", with("initiativeId", "initiative_missing"), 404)

	saved := f.expect(f.member, "POST", base+"/initiative-bindings", binding, 200)
	authorization, _ := saved["authorization"].(map[string]any)
	if authorization["userId"] != owner.ID || authorization["role"] != domain.ChatInitiativeBindingRoleOwner || saved["revokedAt"] != nil {
		t.Fatalf("authorization not recorded: %v", saved)
	}
	f.refusal(f.admin, "POST", base+"/initiative-bindings", binding, 409, "chat_binding_conflict", "")
	if !f.audited("chat_initiative_binding.authorized", func(entry domain.AuditLogEntry) bool {
		return entry.ResourceID == saved["id"] && entry.Metadata["authorizedBy"] == owner.ID && entry.Metadata["initiativeId"] == "initiative_direct" && entry.Metadata["chatId"] == "oc_internal"
	}) {
		t.Fatal("authorization not audited")
	}
	// The group stays bindable to a team as well: initiative bindings are separate.
	f.expect(f.admin, "POST", base+"/bindings", chatAuthorizedBinding(domain.ChatBinding{ChatID: "oc_internal", ChatName: "Vault room", TeamID: f.publicTeam, Events: []string{"issue.created"}, Enabled: true}), 200)

	// Listing: the people who may change it see the bindings.
	list := f.expect(f.member, "GET", base+"/initiatives/initiative_direct/bindings", nil, 200)
	if items, _ := list["bindings"].([]any); len(items) != 1 || list["canManage"] != true || list["shareable"] != true {
		t.Fatalf("owner list: %v", list)
	}
	list = f.expect(f.outsider, "GET", base+"/initiatives/initiative_direct/bindings", nil, 200)
	if items, _ := list["bindings"].([]any); len(items) != 0 || list["canManage"] != false {
		t.Fatalf("outsider list: %v", list)
	}

	// Delivery: the update card names the initiative only, never the private
	// contributing team.
	f.deliverInitiativeUpdate("initiative_direct", "update_posted")
	if len(*sent) != 1 || (*sent)[0]["receive_id"] != "oc_internal" {
		t.Fatalf("initiative update not delivered: %#v", *sent)
	}
	content := (*sent)[0]["content"].(string)
	if !strings.Contains(content, "Company launch") || !strings.Contains(content, "/initiative/company-launch-1/overview") || !strings.Contains(content, "At risk") || !strings.Contains(content, "Launch checklist complete.") || !strings.Contains(content, "Next: customer rollout.") || strings.Contains(content, "A later update") || strings.Contains(content, "Vault") {
		t.Fatalf("initiative card: %s", content)
	}
	// Once a private team leads it, nothing more is posted.
	if err := f.s.store.MutateWorkspace(t.Context(), "test-workspace", "initiative.updated", "initiative_direct", nil, func(data *domain.Bootstrap) error {
		for index := range data.Initiatives {
			if data.Initiatives[index].ID == "initiative_direct" {
				data.Initiatives[index].LeadTeamID = f.privateTeam
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.deliverInitiativeUpdate("initiative_direct")
	if len(*sent) != 1 {
		t.Fatalf("private lead team's initiative delivered: %#v", *sent)
	}
	// Back to workspace level, with a new owner: the binding follows its
	// authorizer and stops.
	newOwner := f.outsider.user
	if err := f.s.store.MutateWorkspace(t.Context(), "test-workspace", "initiative.updated", "initiative_direct", nil, func(data *domain.Bootstrap) error {
		for index := range data.Initiatives {
			if data.Initiatives[index].ID == "initiative_direct" {
				data.Initiatives[index].LeadTeamID, data.Initiatives[index].Owner = "", &newOwner
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.deliverInitiativeUpdate("initiative_direct")
	if len(*sent) != 1 {
		t.Fatalf("binding kept delivering after its authorizer lost ownership: %#v", *sent)
	}
	// The former owner may no longer change it; the new owner and
	// administrators may.
	id := saved["id"].(string)
	f.refusal(f.member, "POST", base+"/initiative-bindings/"+id+"/revoke", nil, 403, "chat_initiative_binding_permission_required", "initiative")
	renewed := f.expect(f.outsider, "POST", base+"/initiative-bindings", map[string]any{"id": id, "initiativeId": "initiative_direct", "chatId": "oc_internal", "chatName": "Vault room", "enabled": true, "authorize": true}, 200)
	if renewed["authorization"].(map[string]any)["userId"] != newOwner.ID {
		t.Fatalf("re-authorization not recorded: %v", renewed)
	}
	f.deliverInitiativeUpdate("initiative_direct")
	if len(*sent) != 2 {
		t.Fatalf("re-authorized binding not delivering: %#v", *sent)
	}
	revoked := f.expect(f.admin, "POST", base+"/initiative-bindings/"+id+"/revoke", nil, 200)
	if revoked["revokedAt"] == nil || revoked["revokedBy"] != f.admin.user.ID || revoked["enabled"] != false {
		t.Fatalf("revocation: %v", revoked)
	}
	f.deliverInitiativeUpdate("initiative_direct")
	if len(*sent) != 2 {
		t.Fatal("revoked binding delivered")
	}
	f.refusal(f.outsider, "POST", base+"/initiative-bindings", map[string]any{"id": id, "initiativeId": "initiative_direct", "chatId": "oc_internal", "enabled": true, "authorize": true}, 409, "chat_binding_revoked", "")
	f.expect(f.outsider, "DELETE", base+"/initiative-bindings/"+id, nil, 204)
	for _, action := range []string{"chat_initiative_binding.updated", "chat_initiative_binding.revoked", "chat_initiative_binding.deleted"} {
		if !f.audited(action, func(entry domain.AuditLogEntry) bool { return entry.ResourceID == id }) {
			t.Fatalf("audit entry %s missing", action)
		}
	}
	// Disconnecting the application removes initiative bindings too.
	f.expect(f.member, "POST", base+"/initiative-bindings", binding, 403)
	record := f.record(provider)
	if err := f.s.store.PutChatInitiativeBinding(t.Context(), "test-workspace", domain.ChatInitiativeBinding{ID: "chat_initiative_binding_left", ConnectionID: record.ConnectionID, ChatID: "oc_internal", InitiativeID: "initiative_direct", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := f.s.store.DeleteChatIntegration(t.Context(), "test-workspace", record.ConnectionID); err != nil {
		t.Fatal(err)
	}
	if items, err := f.s.store.ChatInitiativeBindings(t.Context(), "test-workspace", record.ConnectionID, ""); err != nil || len(items) != 0 {
		t.Fatalf("initiative bindings survived disconnect: %v %v", items, err)
	}
}

func TestChatInitiativeTeamGroupCardsIncludeTheUpdateAndRespectProtection(t *testing.T) {
	f := newChatWorkflowFixture(t)
	platform := f.threadPlatform(t)
	f.binding.Events = []string{"initiative.update_created"}
	if err := f.s.store.PutChatBinding(t.Context(), f.record.WorkspaceKey, f.binding); err != nil {
		t.Fatal(err)
	}
	body := "Shipping the launch plan.\n" + strings.Repeat("x", 2300) + "not-in-preview"
	if err := f.s.store.MutateWorkspace(t.Context(), f.record.WorkspaceKey, "test.initiative", "initiative_group", nil, func(data *domain.Bootstrap) error {
		data.Initiatives = append(data.Initiatives, domain.Initiative{ID: "initiative_group", Name: "Launch", LeadTeamID: f.binding.TeamID, Health: "offTrack"})
		data.InitiativeUpdates["initiative_group"] = []domain.InitiativeUpdate{
			{ID: "update_latest", Body: "Newer update", Health: "offTrack", CreatedAt: time.Now().Add(-time.Minute)},
			{ID: "update_event", Body: body, Health: "onTrack", CreatedAt: time.Now().Add(-2 * time.Minute)},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(childEventPayload{Child: &webhookChild{ID: "update_event"}})
	event := domain.DomainEvent{ID: "evt_initiative_body", Type: "initiative.update_created", AggregateID: "initiative_group", Payload: payload}
	f.deliver(t, event)
	if len(platform.calls) != 1 {
		t.Fatalf("initiative group delivery: %+v", platform.calls)
	}
	content := platform.calls[0].body["content"].(string)
	if !strings.Contains(content, "Shipping the launch plan.") || !strings.Contains(content, "On track") || strings.Contains(content, "Newer update") || strings.Contains(content, "not-in-preview") {
		t.Fatalf("wrong update or unbounded preview: %s", content)
	}
	if err := f.s.store.MutateWorkspace(t.Context(), f.record.WorkspaceKey, "test.protection", "", nil, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.HIPAACompliance = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	event.ID = "evt_initiative_protected"
	f.deliver(t, event)
	if len(platform.calls) != 1 {
		t.Fatal("initiative update leaked with sensitive data protection")
	}
}
