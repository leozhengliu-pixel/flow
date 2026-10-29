package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"

	"flow/api/internal/domain"
)

type chatNotificationJob struct {
	BindingID       string `json:"bindingId"`
	BindingScope    string `json:"bindingScope"`
	IssueID         string `json:"issueId,omitempty"`
	ProjectID       string `json:"projectId,omitempty"`
	InitiativeID    string `json:"initiativeId,omitempty"`
	SourceMessageID string `json:"sourceMessageId,omitempty"`
	Kind            string `json:"kind"`
	Operation       string `json:"operation,omitempty"`
	PersonalOpenID  string `json:"personalOpenId,omitempty"`
	// InitiativeBindingID names an initiative's own group binding
	// (chat_initiative_bindings.go) instead of a team binding.
	InitiativeBindingID string `json:"initiativeBindingId,omitempty"`
	// Event and EventAt are the domain event a comment sync runs for
	// (chat_issue_threads.go).
	Event   string    `json:"event,omitempty"`
	EventAt time.Time `json:"eventAt,omitzero"`
}

func chatEventKinds(event domain.DomainEvent) []string {
	switch event.Type {
	case "issue.created", "issue.status_changed", "issue.assignee_changed", "comment.created", "project.update_created", "initiative.update_created":
		return []string{event.Type}
	case "comment.updated", "comment.deleted":
		// Reflected in the issue's thread by the groups that share comments.
		return []string{"comment.created"}
	case "issue.updated":
		var before map[string]json.RawMessage
		if json.Unmarshal(event.PreviousValues, &before) != nil {
			return []string{"refresh"}
		}
		kinds := []string{"refresh"}
		if _, ok := before["state"]; ok {
			kinds = append(kinds, "issue.status_changed")
		}
		if _, ok := before["assignee"]; ok {
			kinds = append(kinds, "issue.assignee_changed")
		}
		return kinds
	}
	return nil
}

// Called by both the committed event sink and its durable replay cursor. The
// event/binding/kind key makes replay safe across restarts and service replicas.
func (s *server) queueChatDomainEvent(ctx context.Context, workspace string, event domain.DomainEvent) error {
	kinds := chatEventKinds(event)
	if len(kinds) == 0 {
		return nil
	}
	var issue *domain.Issue
	var projectMetadata domain.Bootstrap
	resourceLoaded := false
	for _, provider := range []string{"feishu", "lark"} {
		record, err := s.store.ChatIntegration(ctx, workspace, provider)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if !record.Connected {
			continue
		}
		if err := s.queueChatInitiativeBindingEvent(ctx, workspace, record, event); err != nil {
			return err
		}
		bindings, err := s.store.ChatBindings(ctx, workspace, record.ConnectionID)
		if err != nil {
			return err
		}
		if len(bindings) == 0 {
			continue
		}
		if !resourceLoaded {
			var ok bool
			projectMetadata, ok = s.store.WorkspaceMetadataFields(workspace, "projects", "initiatives", "teams", "teamSettings", "issueTemplates", "workspaceSettings")
			if !ok {
				return chatInvalid("chat notification metadata unavailable")
			}
			if event.Type != "project.update_created" && event.Type != "initiative.update_created" {
				current, err := s.store.IssueRecord(ctx, workspace, event.AggregateID)
				if errors.Is(err, sql.ErrNoRows) {
					return nil
				}
				if err != nil {
					return err
				}
				issue = &current
			}
			resourceLoaded = true
		}
		for _, binding := range bindings {
			if !binding.Enabled || !binding.CreatedAt.IsZero() && event.CreatedAt.Before(binding.CreatedAt) || !chatBindingScopePermitted(projectMetadata, binding) || !s.chatBindingAuthorizerValid(ctx, workspace, binding) {
				continue
			}
			if issue != nil && !chatIssueMatchesBinding(*issue, binding) {
				continue
			}
			if event.Type == "project.update_created" && !chatProjectMatchesBinding(projectMetadata, binding, event.AggregateID) {
				continue
			}
			if event.Type == "initiative.update_created" && !chatInitiativeMatchesBinding(projectMetadata, binding, event.AggregateID) {
				continue
			}
			for _, kind := range kinds {
				if kind != "refresh" && !slices.Contains(binding.Events, kind) {
					continue
				}
				if kind == "refresh" && !binding.AllowUpdate {
					continue
				}
				payload := chatNotificationJob{BindingID: binding.ID, BindingScope: chatBindingFingerprint(binding), IssueID: event.AggregateID, Kind: kind}
				if kind == "comment.created" {
					payload.Event, payload.EventAt = event.Type, event.CreatedAt
				}
				if kind == "project.update_created" {
					payload.ProjectID, payload.IssueID = event.AggregateID, ""
				}
				if kind == "initiative.update_created" {
					payload.InitiativeID, payload.IssueID = event.AggregateID, ""
				}
				key := chatKey(record.ConnectionID, binding.ID, event.ID, kind)
				raw, _ := json.Marshal(payload)
				if _, err := s.store.EnqueueChatJob(ctx, domain.ChatJob{ID: "chat_job_" + key, WorkspaceKey: workspace, ConnectionID: record.ConnectionID, Kind: "notification", DedupKey: key, Payload: raw, RunAt: time.Now().UTC()}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *server) processChatNotification(ctx context.Context, record domain.ChatIntegrationRecord, job domain.ChatJob) error {
	var payload chatNotificationJob
	if json.Unmarshal(job.Payload, &payload) != nil {
		return chatInvalid("invalid stored notification")
	}
	if payload.InitiativeBindingID != "" {
		return s.chatSendInitiativeBindingUpdate(ctx, record, job, payload)
	}
	if payload.Kind == chatMessageResultKind {
		return s.processChatMessageResult(ctx, record, job, payload)
	}
	binding, err := s.chatBindingByID(ctx, record, payload.BindingID)
	if err != nil {
		return err
	}
	if payload.BindingScope != chatBindingFingerprint(binding) {
		return nil
	}
	if payload.Kind != "result" && payload.Kind != "refresh" && !slices.Contains(binding.Events, payload.Kind) {
		return nil
	}
	if payload.Kind == "project.update_created" {
		return s.chatSendProjectUpdate(ctx, record, binding, job, payload.ProjectID)
	}
	if payload.Kind == "initiative.update_created" {
		return s.chatSendInitiativeUpdate(ctx, record, binding, job, payload.InitiativeID)
	}
	issue, err := s.store.IssueRecord(ctx, record.WorkspaceKey, payload.IssueID)
	if err != nil {
		return chatInvalid("notification issue unavailable")
	}
	if !chatIssueMatchesBinding(issue, binding) {
		return nil
	}
	if payload.Kind == "comment.created" {
		return s.chatSyncIssueComments(ctx, record, binding, issue, job, payload)
	}
	if payload.Kind == "issue.created" {
		messages, err := s.store.ChatMessagesForIssue(ctx, record.WorkspaceKey, record.ConnectionID, issue.ID)
		if err != nil {
			return err
		}
		for _, message := range messages {
			if message.ChatID == binding.ChatID && message.SourceMessageID != "" {
				return nil
			}
		}
	}
	if payload.Kind == "refresh" {
		return s.chatRefreshIssueCards(ctx, record, binding, issue)
	}
	if payload.Kind == "result" && payload.PersonalOpenID != "" {
		// Re-check the identity immediately before disclosing an operation result.
		identity, identityErr := s.store.ChatIdentityForOpenID(ctx, record.WorkspaceKey, record.ConnectionID, payload.PersonalOpenID)
		if identityErr != nil || identity.TenantKey != record.TenantKey {
			return chatInvalid("result recipient unbound")
		}
		var authorized domain.Issue
		if err := s.chatAuthorizedMutation(withChatIdentityAuthentication(ctx, identity), record.WorkspaceKey, identity.UserID, http.MethodGet, "/api/issue-records/"+url.PathEscape(issue.ID), nil, &authorized); err != nil {
			return chatInvalid("result recipient access revoked")
		}
		link := fmt.Sprintf("%s/%s/issue/%s", s.chatAppOrigin(), url.PathEscape(record.WorkspaceKey), url.PathEscape(issue.Identifier))
		if err := s.chatSendPersonalText(ctx, record, payload.PersonalOpenID, "Saved in Flow: "+issue.Identifier+" "+link, chatKey(job.DedupKey, "success")); err != nil {
			return err
		}
		if payload.Operation != "create" {
			return s.chatRefreshIssueCards(ctx, record, binding, issue)
		}
	}
	return s.chatSendIssueCard(ctx, record, binding, issue, job.DedupKey, payload.SourceMessageID, payload.Kind)
}

func (s *server) chatSendProjectUpdate(ctx context.Context, record domain.ChatIntegrationRecord, binding domain.ChatBinding, job domain.ChatJob, projectID string) error {
	data, ok := s.store.WorkspaceMetadataFields(record.WorkspaceKey, "projects", "teams", "teamSettings", "workspaceSettings")
	if !ok {
		return chatInvalid("project metadata unavailable")
	}
	if !chatProjectMatchesBinding(data, binding, projectID) {
		return nil
	}
	for _, project := range data.Projects {
		if project.ID != projectID || project.ArchivedAt != nil || !slices.Contains(project.TeamIDs, binding.TeamID) || !binding.CoversProject(project.ID) {
			continue
		}
		slug := project.SlugID
		if slug == "" {
			slug = project.ID
		}
		link := fmt.Sprintf("%s/%s/project/%s/overview", s.chatAppOrigin(), url.PathEscape(record.WorkspaceKey), url.PathEscape(slug))
		card := chatCard("Project update", chatCardText(project.Name), map[string]any{"tag": "action", "actions": []map[string]any{{"tag": "button", "text": chatPlain("Open in Flow"), "url": link}}})
		content, _ := json.Marshal(card)
		return s.chatWorkflowRequest(ctx, record, http.MethodPost, "/open-apis/im/v1/messages?receive_id_type=chat_id", map[string]any{"receive_id": binding.ChatID, "msg_type": "interactive", "content": string(content), "uuid": chatKey(job.DedupKey, "project")[:32]}, nil)
	}
	return nil
}

func chatProjectMatchesBinding(data domain.Bootstrap, binding domain.ChatBinding, projectID string) bool {
	if data.WorkspaceSettings.HIPAACompliance {
		return false
	}
	for _, project := range data.Projects {
		if project.ID != projectID || project.ArchivedAt != nil || !slices.Contains(project.TeamIDs, binding.TeamID) || !binding.CoversProject(project.ID) {
			continue
		}
		for _, teamID := range project.TeamIDs {
			if !chatTeamShareable(data, binding, teamID) {
				return false
			}
		}
		return true
	}
	return false
}

// chatInitiativeMatchesBinding: an initiative update reaches the groups of the
// initiative's lead team, when every contributing team may be shared with them.
// Workspace-level initiatives (no lead team) have no team group.
func chatInitiativeMatchesBinding(data domain.Bootstrap, binding domain.ChatBinding, initiativeID string) bool {
	if data.WorkspaceSettings.HIPAACompliance || chatBindingProjectScoped(binding) {
		return false
	}
	for _, initiative := range data.Initiatives {
		if initiative.ID != initiativeID || initiative.LeadTeamID == "" || initiative.LeadTeamID != binding.TeamID {
			continue
		}
		for _, teamID := range initiative.ContributingTeamIDs {
			if !chatTeamShareable(data, binding, teamID) {
				return false
			}
		}
		return chatTeamShareable(data, binding, initiative.LeadTeamID)
	}
	return false
}

func (s *server) chatSendInitiativeUpdate(ctx context.Context, record domain.ChatIntegrationRecord, binding domain.ChatBinding, job domain.ChatJob, initiativeID string) error {
	data, ok := s.store.WorkspaceMetadataFields(record.WorkspaceKey, "initiatives", "teams", "teamSettings", "workspaceSettings")
	if !ok {
		return chatInvalid("initiative metadata unavailable")
	}
	if !chatInitiativeMatchesBinding(data, binding, initiativeID) {
		return nil
	}
	for _, initiative := range data.Initiatives {
		if initiative.ID != initiativeID {
			continue
		}
		slug := initiative.SlugID
		if slug == "" {
			slug = initiative.ID
		}
		link := fmt.Sprintf("%s/%s/initiative/%s/overview", s.chatAppOrigin(), url.PathEscape(record.WorkspaceKey), url.PathEscape(slug))
		card := chatCard("Initiative update", chatCardText(initiative.Name), map[string]any{"tag": "action", "actions": []map[string]any{{"tag": "button", "text": chatPlain("Open in Flow"), "url": link}}})
		content, _ := json.Marshal(card)
		return s.chatWorkflowRequest(ctx, record, http.MethodPost, "/open-apis/im/v1/messages?receive_id_type=chat_id", map[string]any{"receive_id": binding.ChatID, "msg_type": "interactive", "content": string(content), "uuid": chatKey(job.DedupKey, "initiative")[:32]}, nil)
	}
	return nil
}

func chatNotificationLabel(kind string) string {
	switch kind {
	case "issue.created":
		return "Issue created"
	case "issue.status_changed":
		return "State changed"
	case "issue.assignee_changed":
		return "Assignee changed"
	case "comment.created":
		return "New comment"
	default:
		return "Saved in Flow"
	}
}

func (s *server) chatSendIssueCard(ctx context.Context, record domain.ChatIntegrationRecord, binding domain.ChatBinding, issue domain.Issue, key, sourceMessage, kind string) error {
	return s.chatSendIssueCardFrom(ctx, record, binding, issue, key, sourceMessage, kind, time.Time{})
}

// chatSendIssueCardFrom posts the card into the issue's thread in the group
// (chat_issue_threads.go); a thread it starts mirrors comments from syncFrom.
func (s *server) chatSendIssueCardFrom(ctx context.Context, record domain.ChatIntegrationRecord, binding domain.ChatBinding, issue domain.Issue, key, sourceMessage, kind string, syncFrom time.Time) error {
	card, actions, err := s.chatIssueCard(ctx, record, binding, issue, key, "", sourceMessage)
	if err != nil {
		return err
	}
	elements := card["elements"].([]map[string]any)
	card["elements"] = append([]map[string]any{chatCardText(chatNotificationLabel(kind))}, elements...)
	messageID, err := s.chatSendInIssueThread(ctx, record, binding, issue.ID, key, sourceMessage, chatInteractive(card), syncFrom)
	if err != nil {
		return err
	}
	for _, stored := range actions {
		var action chatCardAction
		if json.Unmarshal(stored.Payload, &action) != nil {
			return chatInvalid("invalid card action")
		}
		action.MessageID = messageID
		stored.Payload, _ = json.Marshal(action)
		if err := s.store.UpdateChatAction(ctx, stored); err != nil {
			return err
		}
	}
	return s.store.PutChatMessage(ctx, domain.ChatMessage{ID: "chat_message_" + chatKey(record.ConnectionID, messageID), WorkspaceKey: record.WorkspaceKey, ConnectionID: record.ConnectionID, ChatID: binding.ChatID, MessageID: messageID, IssueID: issue.ID, SourceMessageID: sourceMessage, Version: strconv.FormatInt(issue.Version, 10), UpdatedAt: time.Now().UTC()})
}

func (s *server) chatRefreshIssueCards(ctx context.Context, record domain.ChatIntegrationRecord, binding domain.ChatBinding, issue domain.Issue) error {
	messages, err := s.store.ChatMessagesForIssue(ctx, record.WorkspaceKey, record.ConnectionID, issue.ID)
	if err != nil {
		return err
	}
	for _, message := range messages {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		version, _ := strconv.ParseInt(message.Version, 10, 64)
		if message.ChatID != binding.ChatID || version >= issue.Version || message.CreatedAt.Before(time.Now().Add(-14*24*time.Hour)) {
			continue
		}
		// Always load the current committed state immediately before a PATCH;
		// delayed events must not restore their old snapshots onto the card.
		latest, err := s.store.IssueRecord(ctx, record.WorkspaceKey, issue.ID)
		if err != nil || !chatIssueMatchesBinding(latest, binding) {
			return nil
		}
		card, _, err := s.chatIssueCard(ctx, record, binding, latest, message.MessageID, message.MessageID, message.SourceMessageID)
		if err != nil {
			return err
		}
		content, _ := json.Marshal(card)
		if err := s.chatWorkflowRequest(ctx, record, http.MethodPatch, "/open-apis/im/v1/messages/"+url.PathEscape(message.MessageID), map[string]string{"content": string(content)}, nil); err != nil {
			return err
		}
		message.Version = strconv.FormatInt(latest.Version, 10)
		if err := s.store.PutChatMessage(ctx, message); err != nil {
			return err
		}
	}
	return nil
}
