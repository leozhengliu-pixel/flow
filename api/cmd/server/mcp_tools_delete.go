package main

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"

	"flow/api/internal/domain"
)

// Delete tools for the resources Flow's UI can delete: projects, initiatives,
// saved views, loops, agent skills, customers and customer requests. Each runs
// the routed REST handler (so the UI's permission checks, trash and side
// effects apply) and returns a receipt the agent can report from:
// {deleted, type, id, name, restorable, restorableUntil?, affected?, note?}.
// "restorable" is true only when Flow keeps the item in its trash and can
// restore it; "affected" counts linked items the delete removes or unlinks so
// the agent (and the chat's bulk-delete approval) can mention side effects.

// mcpDeletion is one resolved delete: what it removes and how to run it.
type mcpDeletion struct {
	Type       string
	ID         string
	Name       string
	Restorable bool
	// TrashType is the trash entry's resource type, when the delete trashes.
	TrashType string
	Extra     map[string]any
	Affected  map[string]int
	Note      string
	run       func(context.Context) error
}

func (d *mcpDeletion) receipt() map[string]any {
	receipt := map[string]any{"type": d.Type, "id": d.ID, "name": d.Name, "restorable": d.Restorable}
	for key, value := range d.Extra {
		receipt[key] = value
	}
	if affected := mcpNonZeroCounts(d.Affected); len(affected) > 0 {
		receipt["affected"] = affected
	}
	if d.Note != "" {
		receipt["note"] = d.Note
	}
	return receipt
}

func mcpNonZeroCounts(counts map[string]int) map[string]int {
	result := map[string]int{}
	for key, value := range counts {
		if value > 0 {
			result[key] = value
		}
	}
	return result
}

// mcpDeleteTools are the delete tools mcpResolveDeletion understands.
var mcpDeleteTools = []string{"delete_project", "delete_initiative", "delete_view", "delete_loop", "delete_agent_skill", "delete_customer", "delete_customer_request"}

func (s *server) deleteMCPResource(ctx context.Context, actor mcpActor, data domain.Bootstrap, tool string, args map[string]any) (any, error) {
	deletion, err := s.mcpResolveDeletion(ctx, actor, data, tool, args)
	if err != nil {
		return nil, err
	}
	if err := deletion.run(ctx); err != nil {
		return nil, err
	}
	receipt := deletion.receipt()
	receipt["deleted"] = true
	if deletion.TrashType != "" {
		if fresh, err := s.mcpWorkspaceData(ctx, actor); err == nil {
			for _, entry := range fresh.Trash {
				if entry.ResourceType == deletion.TrashType && entry.ResourceID == deletion.ID {
					receipt["restorableUntil"] = entry.ExpiresAt
					break
				}
			}
		}
	}
	return receipt, nil
}

// flowDeletePreview describes what a delete_* tool call would remove without
// running it (the item, whether it can be restored, and affected linked
// items), for approval prompts. It reports an error when the target cannot be
// found or the tool is not a delete this preview knows.
func (s *server) flowDeletePreview(ctx context.Context, actor mcpActor, tool string, args map[string]any) (map[string]any, error) {
	tool = strings.TrimPrefix(tool, "mcp__flow.")
	data, err := s.mcpWorkspaceData(ctx, actor)
	if err != nil {
		return nil, err
	}
	switch tool {
	case "delete_issue":
		if err := s.hydrateMCPIssueArguments(ctx, actor, &data, map[string]any{"id": stringArg(args, "id")}); err != nil {
			return nil, err
		}
		issue, err := mcpFindIssue(data, stringArg(args, "id"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": "issue", "id": issue.ID, "identifier": issue.Identifier, "name": issue.Title, "restorable": true}, nil
	case "delete_document":
		document, err := mcpFindDocument(data, stringArg(args, "id"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": "document", "id": document.ID, "name": document.Title, "restorable": true}, nil
	}
	if !slices.Contains(mcpDeleteTools, tool) {
		return nil, fmt.Errorf("%s is not a delete tool", tool)
	}
	deletion, err := s.mcpResolveDeletion(ctx, actor, data, tool, args)
	if err != nil {
		return nil, err
	}
	return deletion.receipt(), nil
}

func (s *server) mcpResolveDeletion(ctx context.Context, actor mcpActor, data domain.Bootstrap, tool string, args map[string]any) (*mcpDeletion, error) {
	query := stringArg(args, "id")
	if query == "" {
		return nil, fmt.Errorf("id is required")
	}
	route := func(path string, handler http.HandlerFunc, id string) func(context.Context) error {
		return func(ctx context.Context) error {
			_, err := s.invokeMCPRoute(ctx, actor, http.MethodDelete, path, map[string]string{"id": id}, nil, handler)
			return err
		}
	}
	switch tool {
	case "delete_project":
		project, err := mcpFindProject(data, query)
		if err != nil {
			return nil, err
		}
		issues, err := s.mcpProjectIssueCount(ctx, actor, project.ID)
		if err != nil {
			return nil, err
		}
		deletion := &mcpDeletion{Type: "project", TrashType: "project", ID: project.ID, Name: project.Name, Restorable: true,
			Extra:    map[string]any{"slugId": project.SlugID},
			Affected: map[string]int{"issues": issues, "milestones": len(project.Milestones), "updates": len(data.ProjectUpdates[project.ID])},
			run:      route("/api/projects/"+pathID(project.ID), s.deleteProject, project.ID)}
		if issues > 0 {
			deletion.Note = "The project's issues are archived and removed from the project; restoring the project un-archives them and puts them back in it."
		}
		return deletion, nil
	case "delete_initiative":
		initiative, err := mcpFindInitiative(data, query)
		if err != nil {
			return nil, err
		}
		children := 0
		for _, item := range data.Initiatives {
			if slices.Contains(item.ParentInitiativeIDs, initiative.ID) {
				children++
			}
		}
		projects := slices.Clone(initiative.ProjectIDs)
		for _, project := range data.Projects {
			if slices.Contains(project.Initiatives, initiative.ID) && !slices.Contains(projects, project.ID) {
				projects = append(projects, project.ID)
			}
		}
		deletion := &mcpDeletion{Type: "initiative", TrashType: "initiative", ID: initiative.ID, Name: initiative.Name, Restorable: true,
			Extra:    map[string]any{"slugId": initiative.SlugID},
			Affected: map[string]int{"projects": len(projects), "subInitiatives": children, "updates": len(data.InitiativeUpdates[initiative.ID])},
			run:      route("/api/initiatives/"+pathID(initiative.ID), s.deleteInitiative, initiative.ID)}
		if children > 0 || len(projects) > 0 {
			deletion.Note = "Its projects and sub-initiatives are kept but unlinked from it; restoring the initiative links them again."
		}
		return deletion, nil
	case "delete_view":
		view, err := mcpFindView(data, query)
		if err != nil {
			return nil, err
		}
		return &mcpDeletion{Type: "view", ID: view.ID, Name: view.Name, Extra: map[string]any{"scope": view.Scope},
			Note: "Saved views are deleted permanently; Flow cannot restore them.",
			run:  route("/api/views/"+pathID(view.ID), s.deleteSavedView, view.ID)}, nil
	case "delete_loop":
		index := slices.IndexFunc(data.Loops, func(item domain.Loop) bool { return equalFoldAny(query, item.ID, item.Name) })
		if index < 0 {
			return nil, fmt.Errorf("loop %q not found", query)
		}
		loop := data.Loops[index]
		runs := 0
		if items, err := s.store.ListLoopRuns(ctx, data.Workspace.URLKey, loop.ID, loopRunHistory); err == nil {
			runs = len(items)
		}
		// Like the reference app ("Delete this loop?"), deleting a loop cannot be undone.
		return &mcpDeletion{Type: "loop", ID: loop.ID, Name: loop.Name, Affected: map[string]int{"runs": runs},
			Note: "Loops are deleted permanently with their saved versions and run history; Flow cannot restore them.",
			run:  route("/api/loops/"+pathID(loop.ID), s.deleteLoop, loop.ID)}, nil
	case "delete_agent_skill":
		return s.mcpResolveSkillDeletion(actor, data, query, stringArg(args, "team"))
	case "delete_customer":
		customer, err := mcpFindCustomer(data, query)
		if err != nil {
			return nil, err
		}
		requests := 0
		visible, _, err := s.visibleMCPCustomerRequests(ctx, actor, data)
		if err != nil {
			return nil, err
		}
		for _, request := range visible {
			if request.CustomerID == customer.ID {
				requests++
			}
		}
		// Like the reference app, deleting a customer cannot be undone and deletes its
		// customer requests with it.
		deletion := &mcpDeletion{Type: "customer", ID: customer.ID, Name: customer.Name,
			Affected: map[string]int{"requests": requests},
			Note:     "Customers are deleted permanently; Flow cannot restore them.",
			run:      route("/api/customers/"+pathID(customer.ID), s.deleteCustomer, customer.ID)}
		if requests > 0 {
			deletion.Note = "Customers are deleted permanently with all their customer requests; Flow cannot restore them."
		}
		return deletion, nil
	case "delete_customer_request":
		visible, issues, err := s.visibleMCPCustomerRequests(ctx, actor, data)
		if err != nil {
			return nil, err
		}
		index := slices.IndexFunc(visible, func(item domain.CustomerRequest) bool { return item.ID == query })
		if index < 0 {
			return nil, fmt.Errorf("customer request %q not found", query)
		}
		request := visible[index]
		name := mcpCustomerName(data, request.CustomerID)
		extra := map[string]any{"body": request.Body}
		if issue, ok := issues[request.IssueID]; ok {
			extra["issue"] = map[string]any{"id": issue.ID, "identifier": issue.Identifier, "title": issue.Title}
		}
		return &mcpDeletion{Type: "customer_request", TrashType: "customer_request", ID: request.ID, Name: name, Restorable: true, Extra: extra,
			run: route("/api/customer-requests/"+pathID(request.ID), s.deleteCustomerRequest, request.ID)}, nil
	}
	return nil, fmt.Errorf("tool %q is not implemented", tool)
}

// mcpResolveSkillDeletion finds one of the viewer's personal skills, or with
// team a team skill, which is removed through the team settings route (team
// settings permission applies). Skills are deleted permanently.
func (s *server) mcpResolveSkillDeletion(actor mcpActor, data domain.Bootstrap, query, teamQuery string) (*mcpDeletion, error) {
	note := "Skills are deleted permanently; Flow cannot restore them."
	if teamQuery != "" {
		team, err := mcpFindTeam(data, teamQuery)
		if err != nil {
			return nil, err
		}
		skills := slices.Clone(data.TeamSettings[team.ID].AgentSkills)
		index := slices.IndexFunc(skills, func(item domain.TeamAgentSkill) bool { return equalFoldAny(query, item.ID, item.Name) })
		if index < 0 {
			return nil, fmt.Errorf("team skill %q not found", query)
		}
		skill := skills[index]
		remaining := slices.Delete(skills, index, index+1)
		return &mcpDeletion{Type: "agent_skill", ID: skill.ID, Name: skill.Name, Note: note,
			Extra: map[string]any{"scope": "team", "team": map[string]any{"id": team.ID, "key": team.Key, "name": team.Name}},
			run: func(ctx context.Context) error {
				_, err := s.invokeMCPRoute(ctx, actor, http.MethodPatch, "/api/teams/"+pathID(team.ID)+"/settings", map[string]string{"id": team.ID}, map[string]any{"agentSkills": remaining}, s.updateStructuredTeamSettings)
				return err
			}}, nil
	}
	index := slices.IndexFunc(data.AgentSkills, func(item domain.PersonalAgentSkill) bool {
		return item.UserID == actor.User.ID && equalFoldAny(query, item.ID, item.Name)
	})
	if index < 0 {
		return nil, fmt.Errorf("agent skill %q not found; pass team to delete a team skill", query)
	}
	skill := data.AgentSkills[index]
	return &mcpDeletion{Type: "agent_skill", ID: skill.ID, Name: skill.Name, Note: note, Extra: map[string]any{"scope": "personal"},
		run: func(ctx context.Context) error {
			_, err := s.invokeMCPRoute(ctx, actor, http.MethodDelete, "/api/agent/skills/"+pathID(skill.ID), map[string]string{"id": skill.ID}, nil, s.deleteAgentSkill)
			return err
		}}, nil
}

// mcpProjectIssueCount counts the issues in a project the actor can see.
func (s *server) mcpProjectIssueCount(ctx context.Context, actor mcpActor, projectID string) (int, error) {
	q, err := s.mcpIssueQuery(ctx, actor)
	if err != nil {
		return 0, err
	}
	q.ProjectIDs, q.Limit, q.Summary, q.IncludeTotal = []string{projectID}, 1, true, true
	page, err := s.store.QueryIssueRecords(ctx, q)
	if err != nil {
		return 0, err
	}
	return int(page.Total), nil
}

// Batch deletes: every delete tool addressed by `id` also takes `ids`, so one call (and one bulk-delete approval)
// carries all the targets of "delete these 3 sub-issues". Each target is deleted like a single call; the receipt
// lists the per-item receipts and the targets that failed.

// mcpBatchDeleteLimit caps the targets of one batch delete call.
const mcpBatchDeleteLimit = 50

// mcpBatchDeleteTools are the delete tools whose schema addresses the target by `id`.
var mcpBatchDeleteTools = sync.OnceValue(func() map[string]bool {
	tools := map[string]bool{}
	var inventory []flowMCPTool
	if json.Unmarshal(flowMCPToolInventory, &inventory) != nil {
		return tools
	}
	for _, item := range inventory {
		name := strings.TrimPrefix(item.Name, "mcp__flow.")
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if strings.HasPrefix(name, "delete_") && json.Unmarshal(item.InputSchema, &schema) == nil && schema.Properties["id"] != nil {
			tools[name] = true
		}
	}
	return tools
})

// mcpBatchDeleteSchema adds `ids` to a delete tool's input schema (raw, decoded) and makes `id` optional.
func mcpBatchDeleteSchema(schema map[string]any) {
	properties, _ := schema["properties"].(map[string]any)
	if properties == nil {
		return
	}
	properties["ids"] = map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 1, "maxItems": mcpBatchDeleteLimit,
		"description": "Delete several items in one call: their IDs, instead of `id`."}
	if required, ok := schema["required"].([]any); ok {
		schema["required"] = slices.DeleteFunc(required, func(value any) bool { return value == "id" })
	}
}

// callFlowBatchDelete runs a delete tool once per entry of args.ids.
func (s *server) callFlowBatchDelete(ctx context.Context, actor mcpActor, name string, args map[string]any) (any, error) {
	if stringArg(args, "id") != "" {
		return nil, fmt.Errorf("pass either id or ids, not both")
	}
	ids := []string{}
	for _, id := range stringsArg(args, "ids") {
		if id = strings.TrimSpace(id); id != "" && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("ids must name at least one item")
	}
	if len(ids) > mcpBatchDeleteLimit {
		return nil, fmt.Errorf("ids can name at most %d items", mcpBatchDeleteLimit)
	}
	items := []any{}
	failures := []map[string]string{}
	for _, id := range ids {
		single := maps.Clone(args)
		delete(single, "ids")
		single["id"] = id
		result, err := s.callFlowTool(ctx, actor, name, single)
		if err != nil {
			failures = append(failures, map[string]string{"id": id, "error": err.Error()})
			continue
		}
		items = append(items, result)
	}
	if len(items) == 0 {
		messages := make([]string, 0, len(failures))
		for _, failure := range failures {
			messages = append(messages, failure["id"]+": "+failure["error"])
		}
		return nil, fmt.Errorf("nothing was deleted: %s", strings.Join(messages, "; "))
	}
	receipt := map[string]any{"deleted": len(items), "items": items}
	if len(failures) > 0 {
		receipt["errors"] = failures
	}
	return receipt, nil
}
