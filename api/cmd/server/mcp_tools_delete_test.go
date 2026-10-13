package main

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

// restoreFromTrash restores the trash entry of a deleted resource as the
// workspace owner, the way Recently deleted does.
func (f *mcpContractFixture) restoreFromTrash(t *testing.T, resourceType, resourceID string) {
	t.Helper()
	index := slices.IndexFunc(f.repository.Bootstrap().Trash, func(entry domain.TrashEntry) bool {
		return entry.ResourceType == resourceType && entry.ResourceID == resourceID
	})
	if index < 0 {
		t.Fatalf("%s %s is not in the trash", resourceType, resourceID)
	}
	entry := f.repository.Bootstrap().Trash[index]
	actor := mcpActor{WorkspaceKey: f.data.Workspace.URLKey, User: f.data.Viewer, APIKey: domain.APIKey{Scopes: []string{"read", "write"}}}
	if _, err := f.service.invokeMCPRoute(t.Context(), actor, http.MethodPost, "/api/trash/"+entry.ID+"/restore", map[string]string{"id": entry.ID}, nil, f.service.restoreTrashEntry); err != nil {
		t.Fatalf("restore %s: %v", resourceType, err)
	}
}

func mcpAffected(t *testing.T, receipt map[string]any) map[string]any {
	t.Helper()
	affected, _ := receipt["affected"].(map[string]any)
	if affected == nil {
		affected = map[string]any{}
	}
	return affected
}

func TestMCPDeleteProjectMovesItToTrashWithItsCounts(t *testing.T) {
	f := newMCPContractFixture(t)
	team := f.data.Teams[0].ID
	project := mcpObject(t, f, "save_project", map[string]any{"name": "QA fix – Retired project", "setTeams": []string{team}})
	id := project["id"].(string)
	mcpObject(t, f, "save_issue", map[string]any{"team": team, "title": "Work in the project", "project": id})
	mcpObject(t, f, "save_milestone", map[string]any{"project": id, "name": "Beta"})

	readOnly := f.addMCPKey(t, "read-only-project-delete", "usr_admin", []string{"read"}, nil)
	mcpDeniedWith(t, f, readOnly, "delete_project", map[string]any{"id": id}, "write scope")
	restricted := f.addMCPKey(t, "restricted-project-delete", "usr_admin", []string{"read", "write"}, []string{})
	mcpDeniedWith(t, f, restricted, "delete_project", map[string]any{"id": id}, "not found")
	if message := mcpToolError(t, f, "delete_project", map[string]any{"id": "No such project"}); !strings.Contains(message, "not found") {
		t.Fatalf("missing project: %s", message)
	}

	actor := mcpActor{WorkspaceKey: f.data.Workspace.URLKey, User: f.data.Viewer, APIKey: domain.APIKey{Scopes: []string{"read", "write"}}}
	previewed, err := f.service.flowDeletePreview(t.Context(), actor, "mcp__flow.delete_project", map[string]any{"id": "QA fix – Retired project"})
	var preview map[string]any
	if cloneErr := jsonClone(previewed, &preview); cloneErr != nil {
		t.Fatal(cloneErr)
	}
	if err != nil || preview["name"] != "QA fix – Retired project" || preview["restorable"] != true || mcpAffected(t, preview)["issues"] != float64(1) || mcpAffected(t, preview)["milestones"] != float64(1) {
		t.Fatalf("preview: %v %v", preview, err)
	}
	if !slices.ContainsFunc(f.repository.Bootstrap().Projects, func(item domain.Project) bool { return item.ID == id }) {
		t.Fatal("preview deleted the project")
	}

	deleted := mcpObject(t, f, "delete_project", map[string]any{"id": "QA fix – Retired project"})
	if deleted["deleted"] != true || deleted["restorable"] != true || deleted["type"] != "project" || deleted["id"] != id || deleted["name"] != "QA fix – Retired project" || deleted["restorableUntil"] == nil {
		t.Fatalf("receipt: %v", deleted)
	}
	if affected := mcpAffected(t, deleted); affected["issues"] != float64(1) || affected["milestones"] != float64(1) {
		t.Fatalf("affected: %v", affected)
	}
	if note, _ := deleted["note"].(string); !strings.Contains(note, "issues are kept") {
		t.Fatalf("note: %v", deleted["note"])
	}
	if slices.ContainsFunc(f.repository.Bootstrap().Projects, func(item domain.Project) bool { return item.ID == id }) {
		t.Fatal("project still listed after delete")
	}
	if message := mcpToolError(t, f, "delete_project", map[string]any{"id": id}); !strings.Contains(message, "not found") {
		t.Fatalf("second delete: %s", message)
	}
	f.restoreFromTrash(t, "project", id)
	if found := mcpObject(t, f, "get_project", map[string]any{"query": id}); found["name"] != "QA fix – Retired project" {
		t.Fatalf("restored project: %v", found)
	}
}

func TestMCPDeleteInitiativeCountsChildrenAndRestores(t *testing.T) {
	f := newMCPContractFixture(t)
	parent := mcpObject(t, f, "save_initiative", map[string]any{"name": "QA fix – Parent"})
	mcpObject(t, f, "save_initiative", map[string]any{"name": "QA fix – Child", "parentInitiatives": []any{parent["id"]}})
	project := mcpObject(t, f, "save_project", map[string]any{"name": "Linked", "setTeams": []string{f.data.Teams[0].ID}, "setInitiatives": []any{parent["id"]}})

	deleted := mcpObject(t, f, "delete_initiative", map[string]any{"id": "QA fix – Parent"})
	if deleted["deleted"] != true || deleted["restorable"] != true || deleted["name"] != "QA fix – Parent" {
		t.Fatalf("receipt: %v", deleted)
	}
	if affected := mcpAffected(t, deleted); affected["subInitiatives"] != float64(1) || affected["projects"] != float64(1) {
		t.Fatalf("affected: %v", affected)
	}
	f.restoreFromTrash(t, "initiative", parent["id"].(string))
	if note, _ := deleted["note"].(string); !strings.Contains(note, "unlinked") {
		t.Fatalf("note: %v", deleted["note"])
	}
	if restored := mcpObject(t, f, "get_initiative", map[string]any{"query": parent["id"]}); restored["name"] != "QA fix – Parent" {
		t.Fatalf("restored initiative: %v", restored)
	}
	if found := mcpObject(t, f, "get_project", map[string]any{"query": project["id"]}); found["name"] != "Linked" {
		t.Fatalf("linked project was deleted with the initiative: %v", found)
	}
	guest := f.guestSecret(t)
	mcpDeniedWith(t, f, guest, "delete_initiative", map[string]any{"id": parent["id"]}, "Guests", "cannot", "not found", "forbidden")
}

func TestMCPDeleteViewLoopAndSkills(t *testing.T) {
	f := newMCPContractFixture(t)
	view := mcpObject(t, f, "save_view", map[string]any{"name": "QA fix – View", "filters": []map[string]any{{"field": "priority", "values": []string{"High"}}}})
	deletedView := mcpObject(t, f, "delete_view", map[string]any{"id": "QA fix – View"})
	if deletedView["deleted"] != true || deletedView["restorable"] != false || deletedView["id"] != view["id"] || deletedView["restorableUntil"] != nil {
		t.Fatalf("view receipt: %v", deletedView)
	}
	if len(f.repository.Bootstrap().SavedViews) != len(f.data.SavedViews) {
		t.Fatal("view still stored")
	}

	loop := mcpObject(t, f, "save_loop", map[string]any{"name": "QA fix – Loop", "instructions": "Summarize the week."})
	deletedLoop := mcpObject(t, f, "delete_loop", map[string]any{"id": "qa fix – loop"})
	if deletedLoop["deleted"] != true || deletedLoop["restorable"] != true || deletedLoop["name"] != "QA fix – Loop" || deletedLoop["restorableUntil"] == nil {
		t.Fatalf("loop receipt: %v", deletedLoop)
	}
	if len(f.repository.Bootstrap().Loops) != 0 {
		t.Fatal("loop still stored")
	}
	f.restoreFromTrash(t, "loop", loop["id"].(string))
	if loops := f.repository.Bootstrap().Loops; len(loops) != 1 || loops[0].Name != "QA fix – Loop" || loops[0].NextRunAt != nil {
		t.Fatalf("restored loops: %+v", loops)
	}
	if message := mcpToolError(t, f, "delete_loop", map[string]any{"id": "missing loop"}); !strings.Contains(message, "not found") {
		t.Fatalf("missing loop: %s", message)
	}

	mcpObject(t, f, "save_agent_skill", map[string]any{"name": "QA fix – Skill", "instructions": "Be brief."})
	team := mcpObject(t, f, "save_agent_skill", map[string]any{"team": "TST", "name": "QA fix – Team skill", "instructions": "Label bugs."})
	if message := mcpToolError(t, f, "delete_agent_skill", map[string]any{"id": "QA fix – Team skill"}); !strings.Contains(message, "pass team") {
		t.Fatalf("team skill without team: %s", message)
	}
	personal := mcpObject(t, f, "delete_agent_skill", map[string]any{"id": "QA fix – Skill"})
	if personal["deleted"] != true || personal["restorable"] != false || personal["scope"] != "personal" {
		t.Fatalf("personal skill receipt: %v", personal)
	}
	teamDeleted := mcpObject(t, f, "delete_agent_skill", map[string]any{"id": team["id"], "team": "TST"})
	if teamDeleted["deleted"] != true || teamDeleted["scope"] != "team" || teamDeleted["name"] != "QA fix – Team skill" {
		t.Fatalf("team skill receipt: %v", teamDeleted)
	}
	data := f.repository.Bootstrap()
	if len(data.AgentSkills) != 0 || len(data.TeamSettings["team_test"].AgentSkills) != 0 {
		t.Fatalf("skills left: %+v %+v", data.AgentSkills, data.TeamSettings["team_test"].AgentSkills)
	}

	guest := f.guestSecret(t)
	mcpObject(t, f, "save_loop", map[string]any{"name": "Owner loop", "instructions": "Run."})
	mcpDeniedWith(t, f, guest, "delete_loop", map[string]any{"id": "Owner loop"}, "cannot perform this action", "not found")
}

func TestMCPDeleteToolsAreDestructiveAndHiddenFromReadonly(t *testing.T) {
	f := newMCPContractFixture(t)
	list := func(path string) map[string]map[string]any {
		t.Helper()
		request, _ := http.NewRequest(http.MethodPost, f.host.URL+path, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		request.Header.Set("Authorization", "Bearer "+f.secret)
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var body struct {
			Result struct {
				Tools []map[string]any `json:"tools"`
			} `json:"result"`
		}
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		tools := map[string]map[string]any{}
		for _, tool := range body.Result.Tools {
			tools[tool["name"].(string)] = tool
		}
		return tools
	}
	full := list("/mcp")
	for _, name := range append(slices.Clone(mcpDeleteTools), "save_customer", "save_customer_request", "save_cycle", "get_customer") {
		tool, ok := full[name]
		if !ok {
			t.Fatalf("%s not listed", name)
		}
		annotations := tool["annotations"].(map[string]any)
		if destructive := strings.HasPrefix(name, "delete_"); annotations["destructiveHint"] != destructive || annotations["readOnlyHint"] != (name == "get_customer") {
			t.Errorf("%s annotations: %v", name, annotations)
		}
	}
	readonly := list("/mcp/readonly")
	if _, ok := readonly["get_customer"]; !ok {
		t.Fatal("readonly hides get_customer")
	}
	for _, name := range append(slices.Clone(mcpDeleteTools), "save_customer", "save_customer_request", "save_cycle") {
		if _, ok := readonly[name]; ok {
			t.Errorf("readonly lists %s", name)
		}
	}
}
