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
	SourceMessageID string `json:"sourceMessageId,omitempty"`
	Kind            string `json:"kind"`
	Operation       string `json:"operation,omitempty"`
	PersonalOpenID  string `json:"personalOpenId,omitempty"`
}

func chatEventKinds(event domain.DomainEvent) []string {
	switch event.Type {
	case "issue.created", "issue.status_changed", "issue.assignee_changed", "comment.created", "project.update_created":
		return []string{event.Type}
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
		bindings, err := s.store.ChatBindings(ctx, workspace, record.ConnectionID)
		if err != nil {
			return err
		}
		if len(bindings) == 0 {
			continue
		}
		if !resourceLoaded {
			var ok bool
			projectMetadata, ok = s.store.WorkspaceMetadataFields(workspace, "projects", "teams", "teamSettings", "issueTemplates", "workspaceSettings")
			if !ok {
				return chatInvalid("chat notification metadata unavailable")
			}
			if event.Type != "project.update_created" {
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
			if !binding.Enabled || !binding.CreatedAt.IsZero() && event.CreatedAt.Before(binding.CreatedAt) || !chatBindingScopePermitted(projectMetadata, binding) {
				continue
			}
			if issue != nil && !chatIssueMatchesBinding(*issue, binding) {
				continue
			}
			if issue == nil && !chatProjectMatchesBinding(projectMetadata, binding, event.AggregateID) {
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
				if kind == "project.update_created" {
					payload.ProjectID, payload.IssueID = event.AggregateID, ""
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
	issue, err := s.store.IssueRecord(ctx, record.WorkspaceKey, payload.IssueID)
	if err != nil {
		return chatInvalid("notification issue unavailable")
	}
	if !chatIssueMatchesBinding(issue, binding) {
		return nil
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
		if project.ID != projectID || project.ArchivedAt != nil || !slices.Contains(project.TeamIDs, binding.TeamID) || binding.ProjectID != "" && binding.ProjectID != project.ID {
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
		if project.ID != projectID || project.ArchivedAt != nil || !slices.Contains(project.TeamIDs, binding.TeamID) || binding.ProjectID != "" && binding.ProjectID != project.ID {
			continue
		}
		for _, teamID := range project.TeamIDs {
			if !chatPublicTeam(data, teamID) {
				return false
			}
		}
		return true
	}
	return false
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
	card, actions, err := s.chatIssueCard(ctx, record, binding, issue, key, "", sourceMessage)
	if err != nil {
		return err
	}
	elements := card["elements"].([]map[string]any)
	card["elements"] = append([]map[string]any{chatCardText(chatNotificationLabel(kind))}, elements...)
	content, _ := json.Marshal(card)
	body := map[string]any{"receive_id": binding.ChatID, "msg_type": "interactive", "content": string(content), "uuid": chatKey(key, "send")[:32]}
	path := "/open-apis/im/v1/messages?receive_id_type=chat_id"
	if sourceMessage != "" {
		path = "/open-apis/im/v1/messages/" + url.PathEscape(sourceMessage) + "/reply"
		delete(body, "receive_id")
		body["reply_in_thread"] = true
	}
	var result chatMessageResult
	if err := s.chatWorkflowRequest(ctx, record, http.MethodPost, path, body, &result); err != nil {
		return err
	}
	if result.Data.MessageID == "" {
		return errorsChatResponse()
	}
	for _, stored := range actions {
		var action chatCardAction
		if json.Unmarshal(stored.Payload, &action) != nil {
			return chatInvalid("invalid card action")
		}
		action.MessageID = result.Data.MessageID
		stored.Payload, _ = json.Marshal(action)
		if err := s.store.UpdateChatAction(ctx, stored); err != nil {
			return err
		}
	}
	return s.store.PutChatMessage(ctx, domain.ChatMessage{ID: "chat_message_" + chatKey(record.ConnectionID, result.Data.MessageID), WorkspaceKey: record.WorkspaceKey, ConnectionID: record.ConnectionID, ChatID: binding.ChatID, MessageID: result.Data.MessageID, IssueID: issue.ID, SourceMessageID: sourceMessage, Version: strconv.FormatInt(issue.Version, 10), UpdatedAt: time.Now().UTC()})
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
