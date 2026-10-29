package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"
	"unicode/utf8"

	"flow/api/internal/domain"
)

type chatMessageResult struct {
	Data struct {
		MessageID string `json:"message_id"`
		ChatID    string `json:"chat_id"`
	} `json:"data"`
}

func chatPlain(text string) map[string]string {
	return map[string]string{"tag": "plain_text", "content": chatTruncateText(text, 2048)}
}

func chatTruncateText(text string, maximum int) string {
	if len(text) <= maximum {
		return text
	}
	text = text[:maximum-3]
	for !utf8.ValidString(text) {
		text = text[:len(text)-1]
	}
	return text + "..."
}

func chatCard(title string, elements ...map[string]any) map[string]any {
	return map[string]any{"config": map[string]bool{"wide_screen_mode": true, "update_multi": true}, "header": map[string]any{"title": chatPlain(chatTruncateText(title, 100)), "template": "blue"}, "elements": elements}
}

func chatCardText(text string) map[string]any {
	return map[string]any{"tag": "div", "text": chatPlain(text)}
}

func chatSelect(name, label, initial string, options []map[string]any) map[string]any {
	result := map[string]any{"tag": "select_static", "name": name, "placeholder": chatPlain(label), "options": options, "required": true}
	if initial != "" {
		result["initial_option"] = initial
	}
	return result
}

func chatOption(value, text string) map[string]any {
	return map[string]any{"value": value, "text": chatPlain(chatTruncateText(text, 80))}
}

func chatInput(name, placeholder, value string, maximum int) map[string]any {
	maximum = min(maximum, 1000)
	runes := []rune(value)
	if len(runes) > maximum {
		value = string(runes[:maximum])
	}
	return map[string]any{"tag": "input", "name": name, "placeholder": chatPlain(placeholder), "default_value": value, "required": true, "max_length": maximum}
}

func chatSubmit(actionID, label string) map[string]any {
	return map[string]any{"tag": "button", "name": "submit", "action_type": "form_submit", "type": "primary", "text": chatPlain(label), "value": map[string]string{"actionId": actionID}}
}

func chatForm(elements ...map[string]any) map[string]any {
	return map[string]any{"tag": "form", "name": "flow_form", "elements": elements}
}

func chatStateOptions(data domain.Bootstrap, teamID string) ([]map[string]any, []string, string) {
	states := statesForTeam(&data, teamID)
	options := make([]map[string]any, 0, len(states))
	ids := make([]string, 0, len(states))
	defaultID := ""
	settings := teamSettings(&data, teamID)
	for _, state := range states {
		if len(options) == 100 {
			break
		}
		options = append(options, chatOption(state.ID, state.Name))
		ids = append(ids, state.ID)
		if defaultID == "" || state.Default {
			defaultID = state.ID
		}
	}
	if slices.Contains(ids, settings.DefaultStateID) {
		defaultID = settings.DefaultStateID
	}
	return options, ids, defaultID
}

func chatAssigneeOptions(data domain.Bootstrap, preferred string) ([]map[string]any, []string) {
	options := []map[string]any{chatOption("__unassigned", "Unassigned")}
	ids := []string{}
	users := slices.Clone(data.Users)
	slices.SortStableFunc(users, func(a, b domain.User) int {
		if a.ID == preferred && b.ID != preferred {
			return -1
		}
		if b.ID == preferred && a.ID != preferred {
			return 1
		}
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})
	for _, user := range users {
		if len(options) == 100 {
			break
		}
		if user.App || !user.Active {
			continue
		}
		options = append(options, chatOption(user.ID, user.Name))
		ids = append(ids, user.ID)
	}
	return options, ids
}

func chatCreateDefaults(metadata domain.Bootstrap, binding domain.ChatBinding, title string) (chatCardAction, error) {
	action := chatCardAction{Kind: "create", BindingID: binding.ID, BindingScope: chatBindingFingerprint(binding), ChatID: binding.ChatID, Title: title}
	team := slices.IndexFunc(metadata.Teams, func(team domain.Team) bool {
		return team.ID == binding.TeamID && !team.Private && team.ArchivedAt == nil && team.RetiredAt == nil
	})
	if team < 0 || !teamContentVisible(metadata, binding.TeamID) {
		return action, chatInvalid("target team unavailable")
	}
	_, action.StateIDs, _ = chatStateOptions(metadata, binding.TeamID)
	_, action.AssigneeIDs = chatAssigneeOptions(metadata, "")
	if len(action.StateIDs) == 0 {
		return action, chatInvalid("team workflow unavailable")
	}
	for _, projectID := range chatBindingProjects(binding) {
		if !slices.ContainsFunc(metadata.Projects, func(project domain.Project) bool {
			return project.ID == projectID && slices.Contains(project.TeamIDs, binding.TeamID)
		}) {
			return action, chatInvalid("target project unavailable")
		}
	}
	if binding.TemplateID != "" {
		index := slices.IndexFunc(metadata.IssueTemplates, func(template domain.IssueTemplate) bool { return template.ID == binding.TemplateID })
		if index < 0 {
			return action, chatInvalid("template unavailable")
		}
		template := metadata.IssueTemplates[index]
		_, action.AssigneeIDs = chatAssigneeOptions(metadata, template.AssigneeID)
		if template.TeamID != "" && template.TeamID != binding.TeamID || template.StateID != "" && !slices.Contains(action.StateIDs, template.StateID) || template.AssigneeID != "" && !slices.Contains(action.AssigneeIDs, template.AssigneeID) || template.ProjectID != "" && chatBindingProjectScoped(binding) && !chatBindingLimitedToProject(binding, template.ProjectID) {
			return action, chatInvalid("template does not match the target team workflow")
		}
		if action.Title == "" {
			action.Title = template.Title
			if action.Title == "" {
				action.Title = template.Name
			}
		}
	}
	return action, nil
}

func (s *server) chatSendCreateForm(ctx context.Context, record domain.ChatIntegrationRecord, binding domain.ChatBinding, identity domain.ChatIdentity, metadata domain.Bootstrap, job domain.ChatJob, command chatInboundCommand) error {
	action, err := chatCreateDefaults(metadata, binding, command.Title)
	if err != nil {
		return s.chatSendCommandError(ctx, record, job, command.OpenID, "This group's team, project, or template is no longer available for creating an issue. Ask a Flow administrator to check the group binding.")
	}
	action.OpenID, action.SourceMessageID = command.OpenID, command.MessageID
	options, _, stateID := chatStateOptions(metadata, binding.TeamID)
	priority, assigneeID, templateProject := teamSettings(&metadata, binding.TeamID).DefaultPriority, "__unassigned", ""
	for _, template := range metadata.IssueTemplates {
		if template.ID == binding.TemplateID {
			templateProject = template.ProjectID
			if template.StateID != "" {
				stateID = template.StateID
			}
			priority = template.Priority
			if template.AssigneeID != "" {
				assigneeID = template.AssigneeID
			}
		}
	}
	assignees, assigneeIDs := chatAssigneeOptions(metadata, assigneeID)
	action.AssigneeIDs = assigneeIDs
	actionID := "chat_action_" + chatKey(record.ConnectionID, job.DedupKey, "create")
	priorities := []map[string]any{chatOption("0", "No priority"), chatOption("1", "Urgent"), chatOption("2", "High"), chatOption("3", "Medium"), chatOption("4", "Low")}
	fields := []map[string]any{chatInput("title", "Issue title", action.Title, 1000)}
	// A binding with several projects asks which one the issue belongs to.
	if projectSelect, offered := chatCreateProjectSelect(metadata, binding, templateProject); projectSelect != nil {
		action.ProjectIDs = offered
		fields = append(fields, projectSelect)
	}
	fields = append(fields, chatSelect("stateId", "State", stateID, options), chatSelect("priority", "Priority", strconv.Itoa(priority), priorities), chatSelect("assigneeId", "Assignee", assigneeID, assignees), chatSubmit(actionID, "Create issue"))
	card := chatCard("Create Flow issue", chatCardText("Team: "+chatTeamName(metadata, binding.TeamID)+chatProjectsLabel(metadata, binding)), chatForm(fields...))
	return s.chatSendPersonalForm(ctx, record, identity, job, actionID, action, card)
}

func chatTeamName(metadata domain.Bootstrap, teamID string) string {
	for _, team := range metadata.Teams {
		if team.ID == teamID {
			return team.Name
		}
	}
	return "Flow"
}

func (s *server) chatSendUpdateForm(ctx context.Context, record domain.ChatIntegrationRecord, binding domain.ChatBinding, identity domain.ChatIdentity, metadata domain.Bootstrap, job domain.ChatJob, command chatInboundCommand, original chatCardAction) error {
	var issue domain.Issue
	if err := s.chatAuthorizedMutation(ctx, record.WorkspaceKey, identity.UserID, http.MethodGet, "/api/issue-records/"+url.PathEscape(original.IssueID), nil, &issue); err != nil {
		return s.chatMutationFailure(ctx, record, job, command.OpenID, err, "This issue is no longer available in this group scope.")
	}
	if !chatIssueMatchesBinding(issue, binding) {
		return s.chatSendCommandError(ctx, record, job, command.OpenID, "This issue is no longer available in this group scope.")
	}
	if issuePermissionRank(issueRole(s, metadata, issue)) < issuePermissionRank("commenter") || original.Kind != "open_comment" && issuePermissionRank(issueRole(s, metadata, issue)) < issuePermissionRank("editor") {
		return s.chatSendCommandError(ctx, record, job, command.OpenID, "Your Flow account does not have permission to perform this action.")
	}
	action := chatCardAction{Kind: original.Kind[len("open_"):], BindingID: binding.ID, BindingScope: chatBindingFingerprint(binding), ChatID: binding.ChatID, MessageID: original.MessageID, IssueID: issue.ID, ExpectedVersion: issue.Version, OpenID: identity.OpenID, SourceMessageID: original.SourceMessageID}
	actionID := "chat_action_" + chatKey(record.ConnectionID, job.DedupKey, action.Kind)
	var control map[string]any
	switch action.Kind {
	case "state":
		options, ids, defaultID := chatStateOptions(metadata, issue.Team.ID)
		action.StateIDs = ids
		selected := issue.State.ID
		if !slices.Contains(ids, selected) {
			selected = defaultID
		}
		if len(ids) == 0 {
			return chatInvalid("team workflow unavailable")
		}
		control = chatSelect("stateId", "State", selected, options)
	case "assignee":
		selected := "__unassigned"
		if issue.Assignee != nil {
			selected = issue.Assignee.ID
		}
		options, ids := chatAssigneeOptions(metadata, selected)
		action.AssigneeIDs = ids
		if selected != "__unassigned" && !slices.Contains(ids, selected) {
			selected = "__unassigned"
		}
		control = chatSelect("assigneeId", "Assignee", selected, options)
	case "comment":
		control = chatInput("comment", "Comment", "", 1000)
	default:
		return chatInvalid("unsupported issue form")
	}
	card := chatCard("Update "+issue.Identifier, chatCardText(issue.Title), chatForm(control, chatSubmit(actionID, "Confirm")))
	return s.chatSendPersonalForm(ctx, record, identity, job, actionID, action, card)
}

func (s *server) chatSendPersonalForm(ctx context.Context, record domain.ChatIntegrationRecord, identity domain.ChatIdentity, job domain.ChatJob, actionID string, action chatCardAction, card map[string]any) error {
	payload, _ := json.Marshal(action)
	stored := domain.ChatAction{ID: actionID, WorkspaceKey: record.WorkspaceKey, ConnectionID: record.ConnectionID, UserID: identity.UserID, Payload: payload, ExpiresAt: time.Now().UTC().Add(15 * time.Minute)}
	if err := s.store.PutChatAction(ctx, stored); err != nil {
		return err
	}
	// The deterministic message UUID is paired with the durable action. A retry
	// after sending attaches the same message instead of issuing a new command.
	content, _ := json.Marshal(card)
	if len(content) > 28<<10 {
		return s.chatSendCommandError(ctx, record, job, identity.OpenID, "This form exceeds the platform's card limit. Open Flow to finish the operation: "+s.chatAppOrigin()+"/"+url.PathEscape(record.WorkspaceKey))
	}
	var result chatMessageResult
	if err := s.chatWorkflowRequest(ctx, record, http.MethodPost, "/open-apis/im/v1/messages?receive_id_type=open_id", map[string]any{"receive_id": identity.OpenID, "msg_type": "interactive", "content": string(content), "uuid": chatKey(job.DedupKey, "form")[:32]}, &result); err != nil {
		return err
	}
	if result.Data.MessageID == "" || result.Data.ChatID == "" {
		return errorsChatResponse()
	}
	action.PersonalChatID, action.PersonalMessage = result.Data.ChatID, result.Data.MessageID
	stored.Payload, _ = json.Marshal(action)
	return s.store.UpdateChatAction(ctx, stored)
}

func errorsChatResponse() error { return fmt.Errorf("chat platform response omitted message identity") }

func chatSharedActionID(record domain.ChatIntegrationRecord, binding domain.ChatBinding, issue domain.Issue, key, kind string) string {
	return "chat_action_" + chatKey(record.ConnectionID, binding.ID, issue.ID, key, strconv.FormatInt(issue.Version, 10), kind)
}

func (s *server) chatIssueCard(ctx context.Context, record domain.ChatIntegrationRecord, binding domain.ChatBinding, issue domain.Issue, key, messageID, sourceMessage string) (map[string]any, []domain.ChatAction, error) {
	link := fmt.Sprintf("%s/%s/issue/%s", s.chatAppOrigin(), url.PathEscape(record.WorkspaceKey), url.PathEscape(issue.Identifier))
	assignee := "Unassigned"
	if issue.Assignee != nil {
		assignee = issue.Assignee.Name
	}
	elements := []map[string]any{chatCardText(issue.Title), chatCardText(fmt.Sprintf("%s\n%s\n%s", issue.Team.Name, issue.State.Name, assignee))}
	buttons := []map[string]any{{"tag": "button", "text": chatPlain("Open in Flow"), "url": link, "type": "default"}}
	actions := []domain.ChatAction{}
	if binding.AllowUpdate {
		for _, option := range []struct{ kind, label string }{{"open_state", "State"}, {"open_assignee", "Assignee"}, {"open_comment", "Comment"}} {
			id := chatSharedActionID(record, binding, issue, key, option.kind)
			payload, _ := json.Marshal(chatCardAction{Kind: option.kind, BindingID: binding.ID, BindingScope: chatBindingFingerprint(binding), ChatID: binding.ChatID, MessageID: messageID, IssueID: issue.ID, ExpectedVersion: issue.Version, SourceMessageID: sourceMessage})
			action := domain.ChatAction{ID: id, WorkspaceKey: record.WorkspaceKey, ConnectionID: record.ConnectionID, Payload: payload, ExpiresAt: time.Now().UTC().Add(14 * 24 * time.Hour)}
			if err := s.store.PutChatAction(ctx, action); err != nil {
				return nil, nil, err
			}
			actions = append(actions, action)
			buttons = append(buttons, map[string]any{"tag": "button", "text": chatPlain(option.label), "value": map[string]string{"actionId": id}, "type": "default"})
		}
	}
	elements = append(elements, map[string]any{"tag": "action", "actions": buttons})
	if binding.AllowUpdate {
		controls, quick, err := s.chatGroupQuickControls(ctx, record, binding, issue, key, messageID, sourceMessage)
		if err != nil {
			return nil, nil, err
		}
		elements, actions = append(elements, controls...), append(actions, quick...)
	}
	return chatCard(issue.Identifier, elements...), actions, nil
}
