package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func (f *chatPermissionFixture) addProjects(projects ...domain.Project) {
	f.t.Helper()
	if err := f.s.store.MutateWorkspace(f.t.Context(), "test-workspace", "project.created", projects[0].ID, nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, projects...)
		return nil
	}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *chatPermissionFixture) addGroup(id string) {
	f.groups[id] = `{"code":0,"data":{"name":"` + id + `","tenant_key":"tenant_a","external":false,"chat_mode":"group"}}`
	f.botIn[id] = true
}

// A binding covers all projects, one project or a chosen set: the server
// checks every project of the set, and deliveries and created issues stay in
// it.
func TestChatBindingProjectSetsLimitScopeAndDeliveries(t *testing.T) {
	provider := "feishu"
	f := newChatPermissionFixture(t, provider)
	f.authorizeTenant(provider)
	base := "/api/integrations/" + provider + "/chat"
	now := time.Now().UTC()
	if err := f.s.store.MutateWorkspace(t.Context(), "test-workspace", "team.created", "team_set_other", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: "team_set_other", Name: "Other", Key: "OTH", Color: "#111111", CreatedAt: &now})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.addProjects(
		domain.Project{ID: "project_set_a", Name: "Alpha", TeamIDs: []string{f.publicTeam}},
		domain.Project{ID: "project_set_b", Name: "Beta", TeamIDs: []string{f.publicTeam}},
		domain.Project{ID: "project_set_c", Name: "Gamma", TeamIDs: []string{f.publicTeam}},
		domain.Project{ID: "project_set_mixed", Name: "Mixed", TeamIDs: []string{f.publicTeam, f.privateTeam}},
		domain.Project{ID: "project_set_foreign", Name: "Foreign", TeamIDs: []string{"team_set_other"}},
	)
	binding := domain.ChatBinding{ChatID: "oc_internal", ChatName: "Vault room", TeamID: f.publicTeam, ProjectIDs: []string{"project_set_b", "project_set_a", "project_set_b"}, Events: []string{"issue.created", "project.update_created"}, Enabled: true, AllowCreate: true}

	// Every project must belong to the team and disclose no private team.
	for _, ids := range [][]string{{"project_set_a", "project_set_foreign"}, {"project_set_mixed"}, {"project_set_a", "project_missing"}} {
		refused := binding
		refused.ProjectIDs = ids
		f.refusal(f.admin, "POST", base+"/bindings", chatAuthorizedBinding(refused), 400, "chat_binding_scope", "")
	}
	tooMany := binding
	tooMany.ProjectIDs = nil
	for index := range chatBindingMaxProjects + 1 {
		tooMany.ProjectIDs = append(tooMany.ProjectIDs, fmt.Sprintf("project_%d", index))
	}
	f.refusal(f.admin, "POST", base+"/bindings", chatAuthorizedBinding(tooMany), 400, "chat_invalid_binding", "")

	saved := f.expect(f.admin, "POST", base+"/bindings", chatAuthorizedBinding(binding), 200)
	if ids, _ := saved["projectIds"].([]any); len(ids) != 2 || ids[0] != "project_set_a" || ids[1] != "project_set_b" || saved["projectId"] != nil {
		t.Fatalf("project set not saved: %v", saved)
	}
	if !f.audited("chat_binding.authorized", func(entry domain.AuditLogEntry) bool {
		raw, _ := json.Marshal(entry.Metadata["projectIds"])
		return entry.ResourceID == saved["id"] && string(raw) == `["project_set_a","project_set_b"]`
	}) {
		t.Fatal("project set not audited")
	}
	// The single-project form still works and reads back as a one-project set.
	f.addGroup("oc_single")
	single := f.expect(f.admin, "POST", base+"/bindings", chatAuthorizedBinding(domain.ChatBinding{ChatID: "oc_single", ChatName: "Single", TeamID: f.publicTeam, ProjectID: "project_set_c", Events: []string{"issue.created"}, Enabled: true}), 200)
	if ids, _ := single["projectIds"].([]any); single["projectId"] != "project_set_c" || len(ids) != 1 || ids[0] != "project_set_c" {
		t.Fatalf("single project binding: %v", single)
	}

	record := f.record(provider)
	stored, err := f.s.chatBindingByID(t.Context(), record, saved["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	issue := domain.Issue{ID: "issue_set", Team: domain.Team{ID: f.publicTeam}, Project: &domain.ProjectSummary{ID: "project_set_b"}}
	outside := issue
	outside.Project = &domain.ProjectSummary{ID: "project_set_c"}
	unscheduled := issue
	unscheduled.Project = nil
	if !chatIssueMatchesBinding(issue, stored) || chatIssueMatchesBinding(outside, stored) || chatIssueMatchesBinding(unscheduled, stored) {
		t.Fatal("issue scope ignores the project set")
	}
	// Project updates reach the group for the set's projects only.
	for _, id := range []string{"project_set_a", "project_set_c"} {
		if err := f.s.queueChatDomainEvent(t.Context(), "test-workspace", domain.DomainEvent{ID: "evt_update_" + id, Type: "project.update_created", AggregateID: id, CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := f.s.store.ClaimChatJobs(t.Context(), "test-workspace", "notification", 10, time.Now())
	if err != nil || len(jobs) != 1 || !strings.Contains(string(jobs[0].Payload), "project_set_a") || !strings.Contains(string(jobs[0].Payload), stored.ID) {
		t.Fatalf("project updates queued: %d %v", len(jobs), err)
	}

	// Issues created from the group choose one of the set's projects.
	action := chatCardAction{ProjectIDs: []string{"project_set_a", "project_set_b"}}
	input, err := chatCreateInput(stored, action, map[string]json.RawMessage{"title": json.RawMessage(`"From the group"`), "projectId": json.RawMessage(`"project_set_b"`)})
	if err != nil || input.ProjectID == nil || *input.ProjectID != "project_set_b" {
		t.Fatalf("create input project: %+v %v", input, err)
	}
	for _, chosen := range []string{`"project_set_c"`, `""`} {
		if _, err := chatCreateInput(stored, action, map[string]json.RawMessage{"title": json.RawMessage(`"From the group"`), "projectId": json.RawMessage(chosen)}); err == nil {
			t.Fatalf("project %s accepted outside the set", chosen)
		}
	}
	data, _ := f.s.store.WorkspaceMetadataFields("test-workspace", "projects")
	control, offered := chatCreateProjectSelect(data, stored, "project_set_b")
	if control == nil || control["name"] != "projectId" || control["initial_option"] != "project_set_b" || !slices.Equal(offered, []string{"project_set_a", "project_set_b"}) {
		t.Fatalf("create form project choice: %v %v", control, offered)
	}
	singleBinding, _ := f.s.chatBindingByID(t.Context(), record, single["id"].(string))
	if control, _ := chatCreateProjectSelect(data, singleBinding, ""); control != nil {
		t.Fatal("one-project binding asks for a project")
	}
	if input, err := chatCreateInput(singleBinding, chatCardAction{}, map[string]json.RawMessage{"title": json.RawMessage(`"Single"`)}); err != nil || input.ProjectID == nil || *input.ProjectID != "project_set_c" {
		t.Fatalf("one-project create input: %+v %v", input, err)
	}

	// A stored single-project binding keeps its fingerprint, so its cards
	// and queued notifications stay valid after the migration.
	legacy := domain.ChatBinding{ID: "binding_legacy", ChatID: "oc_legacy", TeamID: f.publicTeam, ProjectID: "project_set_c", TemplateID: "template_1"}
	migrated := legacy
	migrated.NormalizeProjects()
	if chatBindingFingerprint(migrated) != chatKey(legacy.ID, legacy.ChatID, legacy.TeamID, legacy.ProjectID, legacy.TemplateID) || chatBindingFingerprint(legacy) != chatBindingFingerprint(migrated) {
		t.Fatal("migrated binding fingerprint changed")
	}
	if chatBindingFingerprint(stored) == chatBindingFingerprint(singleBinding) {
		t.Fatal("fingerprints of different scopes collide")
	}
}
