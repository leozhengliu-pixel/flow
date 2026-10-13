package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Agent change sets: every successful Flow write the chat agent makes is recorded on its tool call (domain.AgentChange)
// so the reply can show the reference app's result cards ("Created DEV-50", "Updated project") and revert exactly what the agent
// did with Undo, then re-apply it with Restore.

// agentChangeMaxBytes caps the before/after snapshots of one change; a larger change is shown but cannot be undone.
const agentChangeMaxBytes = 128 << 10

// agentChangeTrashTypes are entity types whose deletes go to the trash (so deleting is undone by restoring the entry).
var agentChangeTrashTypes = map[string]bool{"issue": true, "project": true, "initiative": true, "document": true}

// agentChangePermanentTypes are entity types whose deletes are permanent (like the reference app, customers have no trash):
// undoing the agent's create deletes the record for good, so the card offers no Restore afterwards, and the agent's
// own delete of one cannot be undone.
var agentChangePermanentTypes = map[string]bool{"customer": true}

// agentChangeSaveTools are the write tools that set an entity's fields (used to revert or re-apply an update).
var agentChangeSaveTools = map[string]string{
	"issue": "save_issue", "project": "save_project", "initiative": "save_initiative", "document": "save_document",
	"view": "save_view", "skill": "save_agent_skill", "milestone": "save_milestone",
}

// Writes that are not a resource of their own (comments, reactions, uploads…) get no card and no undo.
var agentChangeIgnoredTools = map[string]bool{
	"save_comment": true, "delete_comment": true, "save_reaction": true, "save_subscription": true, "update_notification": true,
	"create_reminder": true, "save_draft": true, "prepare_attachment_upload": true, "create_attachment": true,
	"create_attachment_from_upload": true, "save_attachment": true, "delete_attachment": true, "merge_diff": true,
	"submit_diff_review": true, "resolve_diff_thread": true, "delete_diff_comment": true, "save_document_permissions": true,
	"restore_document": true,
}

var agentChangeToolAccess = sync.OnceValue(func() map[string]string {
	access := map[string]string{}
	var inventory []flowMCPTool
	if json.Unmarshal(flowMCPToolInventory, &inventory) == nil {
		for _, item := range inventory {
			access[strings.TrimPrefix(item.Name, "mcp__flow.")] = item.Access
		}
	}
	return access
})

// agentChangeTarget names the entity a write tool touches and whether it creates, updates or deletes it. Tools added
// later follow the save_* / create_* / delete_* naming and are recorded generically (a card without Undo).
func agentChangeTarget(tool string, args map[string]any) (entity, action string, ok bool) {
	name := strings.TrimPrefix(tool, "mcp__flow.")
	if agentChangeIgnoredTools[name] || agentChangeToolAccess()[name] != "write" {
		return "", "", false
	}
	switch name {
	case "triage_issue":
		return "issue", "updated", true
	case "restore_document_version":
		return "document", "updated", true
	case "save_status_update":
		entity = "project_update"
		if stringArg(args, "type") == "initiative" {
			entity = "initiative_update"
		}
		action = "created"
		if stringArg(args, "id") != "" {
			action = "updated"
		}
		return entity, action, true
	}
	switch {
	case strings.HasPrefix(name, "save_"):
		entity, action = strings.TrimPrefix(name, "save_"), "created"
		if stringArg(args, "id") != "" {
			action = "updated"
		}
	case strings.HasPrefix(name, "create_"):
		entity, action = strings.TrimPrefix(name, "create_"), "created"
	case strings.HasPrefix(name, "delete_"):
		entity, action = strings.TrimPrefix(name, "delete_"), "deleted"
	default:
		return "", "", false
	}
	switch entity {
	case "agent_skill":
		entity = "skill"
	case "issue_label", "initiative_label":
		entity = "label"
	}
	return entity, action, entity != ""
}

type agentEntitySnapshot struct {
	ID       string
	ParentID string
	Label    string
	Fields   map[string]any
}

type agentChangeCapture struct {
	entity, action string
	args           map[string]any
	before         *agentEntitySnapshot
}

// executeAgentToolRecordingChange runs a chat tool call and, when it is a successful Flow write, records what it changed
// on call.Change (snapshots taken just before and after the write).
func (s *server) executeAgentToolRecordingChange(r *http.Request, data domain.Bootstrap, call *domain.AgentToolCall) ([]byte, error) {
	capture := s.beginAgentChange(r, data, *call)
	result, err := s.executeAgentTool(r, data, *call)
	if err == nil && capture != nil {
		call.Change = s.finishAgentChange(r, data, capture, result)
	}
	return result, err
}

func (s *server) beginAgentChange(r *http.Request, data domain.Bootstrap, call domain.AgentToolCall) *agentChangeCapture {
	if s.store == nil {
		return nil
	}
	var args map[string]any
	if len(call.Arguments) > 0 && json.Unmarshal(call.Arguments, &args) != nil {
		return nil
	}
	if args == nil {
		args = map[string]any{}
	}
	entity, action, ok := agentChangeTarget(call.Name, args)
	if !ok {
		return nil
	}
	capture := &agentChangeCapture{entity: entity, action: action, args: args}
	if action != "created" {
		ctx, actor := s.agentChangeActor(r, data.Viewer)
		if snapshot, found := s.agentEntitySnapshot(ctx, actor, entity, stringArg(args, "id"), stringArg(args, "project")); found {
			capture.before = &snapshot
		}
	}
	return capture
}

func (s *server) finishAgentChange(r *http.Request, data domain.Bootstrap, capture *agentChangeCapture, result []byte) *domain.AgentChange {
	var receipt map[string]any
	_ = json.Unmarshal(result, &receipt)
	change := &domain.AgentChange{Action: capture.action, EntityType: capture.entity}
	change.EntityID = firstNonEmpty(receiptString(receipt, "id"), stringArg(capture.args, "id"))
	change.ParentID = firstNonEmpty(receiptString(receipt, "projectId"), receiptString(receipt, "initiativeId"))
	if capture.before != nil {
		change.EntityID, change.Label = capture.before.ID, capture.before.Label
		if change.ParentID == "" {
			change.ParentID = capture.before.ParentID
		}
	}
	change.Label = firstNonEmpty(receiptString(receipt, "identifier"), receiptString(receipt, "name"), receiptString(receipt, "title"), change.Label, stringArg(capture.args, "name"), stringArg(capture.args, "title"))
	change.URL = agentChangePath(receipt, data.Workspace.URLKey, capture.entity)
	ctx, actor := s.agentChangeActor(r, data.Viewer)
	switch capture.action {
	case "created":
		if strings.HasSuffix(capture.entity, "_update") {
			// The card previews the posted update under the project or initiative it belongs to.
			preview := map[string]any{"body": truncateRunes(strings.TrimSpace(receiptString(receipt, "body")), 280), "health": receiptString(receipt, "health")}
			if parent, found := s.agentEntitySnapshot(ctx, actor, strings.TrimSuffix(capture.entity, "_update"), change.ParentID, ""); found {
				preview["parent"] = parent.Label
			}
			change.After, _ = json.Marshal(preview)
		}
		change.Undoable = change.EntityID != "" && agentChangeRemovable(capture.entity, receipt)
	case "deleted":
		change.Undoable = change.EntityID != "" && agentChangeTrashTypes[capture.entity]
	case "updated":
		if capture.before == nil {
			break
		}
		after, found := s.agentEntitySnapshot(ctx, actor, capture.entity, capture.before.ID, capture.before.ParentID)
		if !found {
			break
		}
		change.Label = firstNonEmpty(after.Label, change.Label)
		before, next := map[string]any{}, map[string]any{}
		for _, field := range sortedKeys(after.Fields) {
			if !agentValuesEqual(capture.before.Fields[field], after.Fields[field]) {
				change.Fields = append(change.Fields, field)
				before[field], next[field] = capture.before.Fields[field], after.Fields[field]
			}
		}
		if len(change.Fields) == 0 {
			break
		}
		beforeRaw, _ := json.Marshal(before)
		afterRaw, _ := json.Marshal(next)
		if len(beforeRaw)+len(afterRaw) > agentChangeMaxBytes {
			change.Note = "This change is too large to undo from the chat."
			break
		}
		change.Before, change.After, change.Undoable = beforeRaw, afterRaw, true
	}
	return change
}

// agentChangePath is the app path of the written entity: the tool's own link, or the slug route of a project or
// initiative (whose receipts are the records themselves).
func agentChangePath(receipt map[string]any, workspace, entity string) string {
	if link := receiptString(receipt, "url"); link != "" {
		if parsed, err := url.Parse(link); err == nil && strings.HasPrefix(parsed.Path, "/") {
			return parsed.Path
		}
	}
	if slug := receiptString(receipt, "slugId"); slug != "" && workspace != "" && (entity == "project" || entity == "initiative") {
		return mcpWorkspaceURL(workspace, nil, entity, slug)
	}
	return ""
}

// agentChangeRemovable reports whether a created entity can be removed again by Undo.
func agentChangeRemovable(entity string, receipt map[string]any) bool {
	switch entity {
	case "issue", "project", "initiative", "document", "view", "milestone", "customer":
		return true
	case "skill":
		return receiptString(receipt, "scope") != "team"
	case "project_update", "initiative_update":
		return receiptString(receipt, "projectId") != "" || receiptString(receipt, "initiativeId") != ""
	}
	return false
}

// agentChangeActor is the viewer acting through Flow's tools (the same identity and checks as the agent's own writes).
func (s *server) agentChangeActor(r *http.Request, viewer domain.User) (context.Context, mcpActor) {
	actor := mcpActor{WorkspaceKey: workspaceKey(r), User: viewer, APIKey: domain.APIKey{Scopes: []string{"read", "write"}}}
	if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok {
		actor.APIKey = key
	}
	ctx := context.WithValue(r.Context(), authUserContextKey{}, viewer)
	ctx = context.WithValue(ctx, workspaceKeyContextKey{}, workspaceKey(r))
	ctx = store.ContextWithActor(ctx, viewer)
	return ctx, actor
}

// agentEntitySnapshot reads the fields an agent write can change, in the write tool's argument shape (ids for
// references), so a difference can be written back with the same tool.
func (s *server) agentEntitySnapshot(ctx context.Context, actor mcpActor, entity, query, parent string) (agentEntitySnapshot, bool) {
	if query == "" {
		return agentEntitySnapshot{}, false
	}
	data, err := s.mcpWorkspaceData(ctx, actor)
	if err != nil {
		return agentEntitySnapshot{}, false
	}
	var snapshot agentEntitySnapshot
	switch entity {
	case "issue":
		if s.hydrateMCPIssueArguments(ctx, actor, &data, map[string]any{"id": query}) != nil {
			return snapshot, false
		}
		issue, err := mcpFindIssue(data, query)
		if err != nil {
			return snapshot, false
		}
		labels := []string{}
		for _, label := range issue.Labels {
			labels = append(labels, label.ID)
		}
		slices.Sort(labels)
		projectID := ""
		if issue.Project != nil {
			projectID = issue.Project.ID
		}
		var estimate any
		if issue.Estimate != nil {
			estimate = *issue.Estimate
		}
		snapshot = agentEntitySnapshot{ID: issue.ID, Label: issue.Identifier, Fields: map[string]any{
			"title": issue.Title, "description": issue.Description, "state": issue.State.ID, "priority": issue.Priority,
			"assignee": userIDOf(issue.Assignee), "project": projectID, "cycle": derefString(issue.CycleID), "labels": labels,
			"parentId": derefString(issue.ParentID), "dueDate": derefString(issue.DueDate), "estimate": estimate,
		}}
	case "project":
		project, err := mcpFindProject(data, query)
		if err != nil {
			return snapshot, false
		}
		snapshot = agentEntitySnapshot{ID: project.ID, Label: project.Name, Fields: map[string]any{
			"name": project.Name, "summary": project.Summary, "description": project.Description, "icon": project.Icon,
			"color": project.Color, "priority": project.Priority, "state": project.Status.ID, "lead": userIDOf(project.Lead),
			"startDate": derefString(project.StartDate), "targetDate": derefString(project.TargetDate),
			"labels": sortedStrings(project.LabelIDs), "setTeams": sortedStrings(project.TeamIDs), "setInitiatives": sortedStrings(project.Initiatives),
		}}
	case "initiative":
		initiative, err := mcpFindInitiative(data, query)
		if err != nil {
			return snapshot, false
		}
		snapshot = agentEntitySnapshot{ID: initiative.ID, Label: initiative.Name, Fields: map[string]any{
			"name": initiative.Name, "summary": initiative.Summary, "description": initiative.Description, "icon": initiative.Icon,
			"color": initiative.Color, "status": initiative.Status, "priority": initiative.Priority, "owner": userIDOf(initiative.Owner),
			"targetDate": derefString(initiative.TargetDate), "labels": sortedStrings(initiative.LabelIDs), "leadTeam": initiative.LeadTeamID,
		}}
	case "document":
		document, err := mcpFindDocument(data, query)
		if err != nil {
			return snapshot, false
		}
		snapshot = agentEntitySnapshot{ID: document.ID, Label: document.Title, Fields: map[string]any{
			"title": document.Title, "content": document.Content, "icon": document.Icon, "color": document.Color,
		}}
	case "view":
		view, err := mcpFindView(data, query)
		if err != nil {
			return snapshot, false
		}
		snapshot = agentEntitySnapshot{ID: view.ID, Label: view.Name, Fields: map[string]any{
			"name": view.Name, "description": view.Description, "icon": view.Icon, "color": view.Color,
		}}
	case "skill":
		index := slices.IndexFunc(data.AgentSkills, func(item domain.PersonalAgentSkill) bool {
			return item.UserID == actor.User.ID && equalFoldAny(query, item.ID, item.Name)
		})
		if index < 0 {
			return snapshot, false
		}
		skill := data.AgentSkills[index]
		snapshot = agentEntitySnapshot{ID: skill.ID, Label: skill.Name, Fields: map[string]any{"name": skill.Name, "instructions": skill.Instructions}}
	case "milestone":
		project, err := mcpFindProject(data, parent)
		if err != nil {
			return snapshot, false
		}
		index := slices.IndexFunc(project.Milestones, func(item domain.ProjectMilestone) bool { return equalFoldAny(query, item.ID, item.Name) })
		if index < 0 {
			return snapshot, false
		}
		milestone := project.Milestones[index]
		snapshot = agentEntitySnapshot{ID: milestone.ID, ParentID: project.ID, Label: milestone.Name, Fields: map[string]any{
			"name": milestone.Name, "description": milestone.Description, "targetDate": derefString(milestone.TargetDate),
		}}
	default:
		return snapshot, false
	}
	// Compare and store JSON-shaped values ([]any, float64), as the tools receive them.
	var normalized map[string]any
	if jsonClone(snapshot.Fields, &normalized) != nil {
		return snapshot, false
	}
	snapshot.Fields = normalized
	return snapshot, true
}

// agentValuesEqual compares snapshot values; text compares ignoring whitespace differences (editors re-save
// Markdown with their own spacing).
func agentValuesEqual(left, right any) bool {
	leftText, leftIsText := left.(string)
	rightText, rightIsText := right.(string)
	if leftIsText || rightIsText {
		if left == nil {
			leftText, leftIsText = "", true
		}
		if right == nil {
			rightText, rightIsText = "", true
		}
		return leftIsText && rightIsText && strings.Join(strings.Fields(leftText), " ") == strings.Join(strings.Fields(rightText), " ")
	}
	leftRaw, _ := json.Marshal(left)
	rightRaw, _ := json.Marshal(right)
	return string(leftRaw) == string(rightRaw)
}

// ---------------------------------------------------------------- undo / restore

type agentChangeActionInput struct {
	// PartIDs limits the action to these tool-call parts (one card); empty means every change in the reply.
	PartIDs []string `json:"partIds,omitempty"`
}

type agentChangeConflict struct{ message string }

func (e agentChangeConflict) Error() string { return e.message }

var agentChangeLocks sync.Map

// undoAgentMessageChanges reverts the writes an agent reply made (the reference app's Undo on a result card).
func (s *server) undoAgentMessageChanges(w http.ResponseWriter, r *http.Request) {
	s.applyAgentMessageChanges(w, r, true)
}

// restoreAgentMessageChanges re-applies writes that were undone (the reference app's Restore).
func (s *server) restoreAgentMessageChanges(w http.ResponseWriter, r *http.Request) {
	s.applyAgentMessageChanges(w, r, false)
}

func (s *server) applyAgentMessageChanges(w http.ResponseWriter, r *http.Request, undo bool) {
	var input agentChangeActionInput
	if r.ContentLength != 0 && !decodeJSON(w, r, &input) {
		return
	}
	sessionID, messageID := r.PathValue("id"), r.PathValue("messageId")
	lock, _ := agentChangeLocks.LoadOrStore(workspaceKey(r)+"\x00"+sessionID+"\x00"+messageID, &sync.Mutex{})
	lock.(*sync.Mutex).Lock()
	defer lock.(*sync.Mutex).Unlock()

	data := s.workspaceData(r)
	session, err := ownedAgentSession(&data, sessionID)
	if err != nil {
		writeError(w, http.StatusNotFound, "agent chat not found")
		return
	}
	index := slices.IndexFunc(session.Messages, func(item domain.AgentMessage) bool { return item.ID == messageID && item.Role == "assistant" })
	if index < 0 {
		writeError(w, http.StatusNotFound, "agent reply not found")
		return
	}
	type target struct {
		partID string
		call   domain.AgentToolCall
	}
	var targets []target
	for _, part := range session.Messages[index].Parts {
		call := part.ToolCall
		if call == nil || call.Change == nil || !call.Change.Undoable || len(input.PartIDs) > 0 && !slices.Contains(input.PartIDs, part.ID) {
			continue
		}
		if (call.Change.State == "undone") != undo {
			targets = append(targets, target{partID: part.ID, call: *call})
		}
	}
	if len(targets) == 0 {
		message := "There is nothing to undo in this reply"
		if !undo {
			message = "There is nothing to restore in this reply"
		}
		writeError(w, http.StatusConflict, message)
		return
	}
	if undo {
		slices.Reverse(targets)
	}
	ctx, actor := s.agentChangeActor(r, data.Viewer)
	// The reverted records change on the server only: publish their realtime events to this tab too (the client did
	// not apply them locally), so an open editor shows the restored text right away.
	ctx = store.ContextWithRealtimeClient(ctx, "")
	applied := map[string]domain.AgentChange{}
	var failure error
	for _, item := range targets {
		next, err := s.applyAgentChange(ctx, actor, item.call, undo)
		if err != nil {
			if failure == nil {
				failure = err
			}
			continue
		}
		applied[item.partID] = next
	}
	if len(applied) == 0 {
		var conflict agentChangeConflict
		switch {
		case errors.As(failure, &conflict):
			writeError(w, http.StatusConflict, conflict.message)
		case errors.Is(failure, errNotFound) || errors.Is(failure, store.ErrAuthForbidden) || errors.Is(failure, errConflict):
			respondMutation(w, failure, http.StatusOK, nil)
		default:
			writeError(w, http.StatusConflict, failure.Error())
		}
		return
	}
	var updated domain.AgentSession
	err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "agent.message_changes", sessionID, map[string]any{"messageId": messageID, "undo": undo}, func(next *domain.Bootstrap) error {
		current, err := ownedAgentSession(next, sessionID)
		if err != nil {
			return err
		}
		for messageIndex := range current.Messages {
			message := &current.Messages[messageIndex]
			if message.ID != messageID {
				continue
			}
			for partIndex := range message.Parts {
				part := &message.Parts[partIndex]
				if change, ok := applied[part.ID]; ok && part.ToolCall != nil {
					call := *part.ToolCall
					call.Change = &change
					part.ToolCall = &call
				}
			}
		}
		updated = *current
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

// applyAgentChange undoes (or, with undo false, re-applies) one recorded change and returns its new record.
func (s *server) applyAgentChange(ctx context.Context, actor mcpActor, call domain.AgentToolCall, undo bool) (domain.AgentChange, error) {
	change := *call.Change
	change.Note, change.Skipped = "", nil
	var err error
	switch {
	case change.Action == "updated":
		change, err = s.applyAgentFieldChange(ctx, actor, change, undo)
	case change.Action == "created" && undo, change.Action == "deleted" && !undo:
		err = s.removeAgentChangeEntity(ctx, actor, change)
	case change.Action == "created":
		change, err = s.recreateAgentChangeEntity(ctx, actor, change, call)
	default: // undoing a delete
		err = s.restoreAgentChangeEntity(ctx, actor, change)
	}
	if err != nil {
		return change, err
	}
	change.State = ""
	if undo {
		change.State = "undone"
		if change.Action == "created" && agentChangePermanentTypes[change.EntityType] {
			change.Undoable = false
			change.Note = fmt.Sprintf("%s was deleted permanently and can't be restored.", agentChangeName(change))
		}
	}
	now := time.Now().UTC()
	change.UpdatedAt = &now
	return change, nil
}

// applyAgentFieldChange writes back the fields the agent changed that still hold the value it left (undo) or the value
// it replaced (restore). Fields someone changed since are left alone; when that is every field nothing is written.
func (s *server) applyAgentFieldChange(ctx context.Context, actor mcpActor, change domain.AgentChange, undo bool) (domain.AgentChange, error) {
	var before, after map[string]any
	if json.Unmarshal(change.Before, &before) != nil || json.Unmarshal(change.After, &after) != nil {
		return change, fmt.Errorf("this change has no snapshot to undo")
	}
	expected, target := after, before
	if !undo {
		expected, target = before, after
	}
	current, found := s.agentEntitySnapshot(ctx, actor, change.EntityType, change.EntityID, change.ParentID)
	if !found {
		return change, agentChangeConflict{fmt.Sprintf("%s no longer exists", agentChangeName(change))}
	}
	args := map[string]any{"id": change.EntityID}
	if change.EntityType == "milestone" {
		args["project"] = change.ParentID
	}
	var written, conflicts []string
	for _, field := range change.Fields {
		switch {
		case agentValuesEqual(current.Fields[field], expected[field]):
			args[field] = target[field]
			written = append(written, field)
		case agentValuesEqual(current.Fields[field], target[field]):
		default:
			conflicts = append(conflicts, field)
		}
	}
	if len(written) == 0 && len(conflicts) > 0 {
		return change, agentChangeConflict{fmt.Sprintf("%s changed after the agent's edit (%s), so nothing was changed", agentChangeName(change), strings.Join(conflicts, ", "))}
	}
	if len(written) > 0 {
		if _, err := s.callFlowTool(ctx, actor, agentChangeSaveTools[change.EntityType], args); err != nil {
			return change, err
		}
	}
	if len(conflicts) > 0 {
		change.Skipped = conflicts
		change.Note = fmt.Sprintf("Left %s as is: it changed after the agent's edit.", strings.Join(conflicts, ", "))
	}
	return change, nil
}

// removeAgentChangeEntity deletes an entity the agent created (trash-backed types go to the trash; customers are
// deleted permanently).
func (s *server) removeAgentChangeEntity(ctx context.Context, actor mcpActor, change domain.AgentChange) error {
	id := change.EntityID
	var err error
	switch change.EntityType {
	case "issue":
		_, err = s.callFlowTool(ctx, actor, "delete_issue", map[string]any{"id": id})
	case "document":
		_, err = s.callFlowTool(ctx, actor, "delete_document", map[string]any{"id": id})
	case "project":
		_, err = s.invokeMCPRoute(ctx, actor, http.MethodDelete, "/api/projects/"+pathID(id), map[string]string{"id": id}, nil, s.deleteProject)
	case "initiative":
		_, err = s.invokeMCPRoute(ctx, actor, http.MethodDelete, "/api/initiatives/"+pathID(id), map[string]string{"id": id}, nil, s.deleteInitiative)
	case "customer":
		_, err = s.invokeMCPRoute(ctx, actor, http.MethodDelete, "/api/customers/"+pathID(id), map[string]string{"id": id}, nil, s.deleteCustomer)
	case "view":
		_, err = s.invokeMCPRoute(ctx, actor, http.MethodDelete, "/api/views/"+pathID(id), map[string]string{"id": id}, nil, s.deleteSavedView)
	case "skill":
		_, err = s.invokeMCPRoute(ctx, actor, http.MethodDelete, "/api/agent/skills/"+pathID(id), map[string]string{"id": id}, nil, s.deleteAgentSkill)
	case "milestone":
		_, err = s.invokeMCPRoute(ctx, actor, http.MethodDelete, "/api/projects/"+pathID(change.ParentID)+"/milestones/"+pathID(id), map[string]string{"id": change.ParentID, "milestoneId": id}, nil, s.deleteProjectMilestone)
	case "project_update":
		_, err = s.invokeMCPRoute(ctx, actor, http.MethodDelete, "/api/projects/"+pathID(change.ParentID)+"/updates/"+pathID(id), map[string]string{"id": change.ParentID, "updateId": id}, nil, s.deleteProjectUpdate)
	case "initiative_update":
		_, err = s.invokeMCPRoute(ctx, actor, http.MethodDelete, "/api/initiatives/"+pathID(change.ParentID)+"/updates/"+pathID(id), map[string]string{"id": change.ParentID, "updateId": id}, nil, s.deleteInitiativeUpdate)
	default:
		err = fmt.Errorf("%s can't be undone", agentChangeName(change))
	}
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "not found") {
		return agentChangeConflict{fmt.Sprintf("%s no longer exists", agentChangeName(change))}
	}
	return err
}

// restoreAgentChangeEntity brings a trashed entity back (Restore after undoing a create, Undo after a delete).
func (s *server) restoreAgentChangeEntity(ctx context.Context, actor mcpActor, change domain.AgentChange) error {
	if !agentChangeTrashTypes[change.EntityType] {
		return fmt.Errorf("%s can't be restored", agentChangeName(change))
	}
	for _, entry := range s.store.TrashSnapshot(actor.WorkspaceKey) {
		if entry.ResourceType == change.EntityType && entry.ResourceID == change.EntityID {
			_, err := s.invokeMCPRoute(ctx, actor, http.MethodPost, "/api/trash/"+pathID(entry.ID)+"/restore", map[string]string{"id": entry.ID}, nil, s.restoreTrashEntry)
			return err
		}
	}
	return agentChangeConflict{fmt.Sprintf("%s is no longer in the trash", agentChangeName(change))}
}

// recreateAgentChangeEntity re-applies a create that was undone: trashed entities come back from the trash, the
// others (views, skills, milestones, updates) are created again from the agent's original arguments.
func (s *server) recreateAgentChangeEntity(ctx context.Context, actor mcpActor, change domain.AgentChange, call domain.AgentToolCall) (domain.AgentChange, error) {
	if agentChangeTrashTypes[change.EntityType] {
		return change, s.restoreAgentChangeEntity(ctx, actor, change)
	}
	var args map[string]any
	if json.Unmarshal(call.Arguments, &args) != nil || args == nil {
		return change, fmt.Errorf("%s can't be restored", agentChangeName(change))
	}
	if change.EntityType == "milestone" && change.ParentID != "" {
		args["project"] = change.ParentID
	}
	result, err := s.callFlowTool(ctx, actor, strings.TrimPrefix(call.Name, "mcp__flow."), args)
	if err != nil {
		return change, err
	}
	var receipt map[string]any
	if jsonClone(result, &receipt) == nil {
		change.EntityID = firstNonEmpty(receiptString(receipt, "id"), change.EntityID)
	}
	return change, nil
}

func agentChangeName(change domain.AgentChange) string {
	kind := strings.ReplaceAll(change.EntityType, "_", " ")
	if change.Label == "" {
		return "The " + kind
	}
	return fmt.Sprintf("%s %q", strings.ToUpper(kind[:1])+kind[1:], change.Label)
}

func receiptString(receipt map[string]any, key string) string {
	value, _ := receipt[key].(string)
	return value
}

func userIDOf(user *domain.User) string {
	if user == nil {
		return ""
	}
	return user.ID
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func sortedStrings(values []string) []string {
	sorted := append([]string{}, values...)
	slices.Sort(sorted)
	return sorted
}

func sortedKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
