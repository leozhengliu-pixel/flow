package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func chatEventBindingScopePermitted(data domain.Bootstrap, binding domain.ChatBinding, templates []store.ChatEventTemplateHeader) bool {
	return chatEventBindingScopeBlock(data, binding, templates) == ""
}

// chatEventBindingScopeBlock is chatBindingScopeBlock over template headers
// (bounded metadata): the same rules, order and reasons.
func chatEventBindingScopeBlock(data domain.Bootstrap, binding domain.ChatBinding, templates []store.ChatEventTemplateHeader) string {
	if reason := chatBindingTeamBlock(data, binding); reason != "" {
		return reason
	}
	if binding.TemplateID == "" {
		return ""
	}
	found := false
	for _, template := range templates {
		if template.ID != binding.TemplateID {
			continue
		}
		if template.TemplateType == "form" || template.FormFields > 0 || template.TeamID != "" && template.TeamID != binding.TeamID || template.VisibilityTeamID != "" && template.VisibilityTeamID != binding.TeamID || template.ProjectID != "" && !chatBindingLimitedToProject(binding, template.ProjectID) {
			return domain.ChatBindingBlockedTemplate
		}
		found = true
	}
	if !found {
		return domain.ChatBindingBlockedTemplate
	}
	return ""
}

type chatNotificationJob struct {
	BindingID       string `json:"bindingId"`
	BindingScope    string `json:"bindingScope"`
	IssueID         string `json:"issueId,omitempty"`
	ProjectID       string `json:"projectId,omitempty"`
	InitiativeID    string `json:"initiativeId,omitempty"`
	UpdateID        string `json:"updateId,omitempty"`
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
	// Request: the issue is a request made in the binding's group
	// (chat_requests.go), whose thread hears comments whatever the
	// binding's notifications.
	Request bool `json:"request,omitempty"`
	// PreviewResource names the item whose link previews a refresh job asks
	// the platform to request again (chat_unfurl_refresh.go).
	PreviewResource string `json:"previewResource,omitempty"`
}

func chatEventKinds(event domain.DomainEvent) []string {
	switch event.Type {
	case "issue.created", "issue.status_changed", "issue.assignee_changed", "comment.created", "project.update_created", "initiative.update_created":
		return []string{event.Type}
	case "comment.updated", "comment.deleted":
		// Reflected in the issue's thread by the groups that share comments.
		return []string{"comment.created"}
	case "project.update_updated", "project.update_deleted":
		// Redraw or retire the update's card in the groups that heard it.
		return []string{"project.update_created"}
	case "ask.decided":
		return []string{chatRequestStatusKind}
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
	if event.Type == "ask.decided" {
		if s.store.BoundedMetadataEnabled() {
			read, found, err := s.store.BeginChatEventPolicyRead(ctx, workspace, store.ChatEventPolicyRequest{AskID: &event.AggregateID})
			if err != nil {
				return err
			}
			if !found {
				return chatInvalid("request metadata unavailable")
			}
			defer read.Close()
			if !read.AskFound || read.AskIssueID == "" {
				return nil
			}
			event.AggregateID = read.AskIssueID
		} else {
			data, ok := s.store.WorkspaceMetadataFields(workspace, "asks")
			if !ok {
				return chatInvalid("request metadata unavailable")
			}
			index := slices.IndexFunc(data.Asks, func(ask domain.Ask) bool { return ask.ID == event.AggregateID })
			if index < 0 || data.Asks[index].IssueID == "" {
				return nil
			}
			event.AggregateID = data.Asks[index].IssueID
		}
	}
	projectUpdate := slices.Contains(kinds, "project.update_created")
	var issue *domain.Issue
	var projectMetadata domain.Bootstrap
	var metadataCtx context.Context
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
			if s.store.BoundedMetadataEnabled() {
				owner, found, err := s.store.BeginInstalledMetadataRead(ctx, workspace)
				if err != nil {
					return err
				}
				if !found {
					return chatInvalid("chat notification metadata unavailable")
				}
				defer owner.Close()
				metadataCtx, err = owner.Context(ctx)
				if err != nil {
					return err
				}
				pulse, found, err := s.store.BeginPulseRead(metadataCtx, workspace, false)
				if err != nil {
					return err
				}
				if !found {
					return chatInvalid("chat notification metadata unavailable")
				}
				defer pulse.Close()
				policy, found, err := s.store.BeginChatDisplayRead(metadataCtx, workspace, store.ChatDisplayRequest{Policy: true})
				if err != nil {
					return err
				}
				if !found {
					return chatInvalid("chat notification metadata unavailable")
				}
				defer policy.Close()
				projectMetadata = policy.Data
				projectMetadata.Projects, projectMetadata.Initiatives = pulse.Snapshot.Projects, pulse.Snapshot.Initiatives
				ok = true
			} else {
				projectMetadata, ok = s.store.WorkspaceMetadataFields(workspace, "projects", "initiatives", "teams", "teamSettings", "issueTemplates", "workspaceSettings")
			}
			if !ok {
				return chatInvalid("chat notification metadata unavailable")
			}
			if !projectUpdate && event.Type != "initiative.update_created" {
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
		var templates []store.ChatEventTemplateHeader
		if s.store.BoundedMetadataEnabled() {
			var ids []string
			for _, binding := range bindings {
				if binding.TemplateID != "" {
					ids = append(ids, binding.TemplateID)
				}
			}
			if len(ids) > 0 {
				read, found, err := s.store.BeginChatEventPolicyRead(metadataCtx, workspace, store.ChatEventPolicyRequest{TemplateIDs: ids})
				if err != nil {
					return err
				}
				if !found {
					return chatInvalid("chat notification metadata unavailable")
				}
				defer read.Close()
				templates = read.Templates
			}
		}
		// Requests made in the connection's groups (chat_requests.go).
		var requests []domain.ChatIntakeRequest
		if issue != nil {
			if requests, err = s.store.ChatIntakeRequestsForIssue(ctx, workspace, record.ConnectionID, issue.ID); err != nil {
				return err
			}
		}
		for _, binding := range bindings {
			if !binding.Enabled || !binding.CreatedAt.IsZero() && event.CreatedAt.Before(binding.CreatedAt) {
				continue
			}
			// A sharing rule that stops a binding is decided now, at event
			// time: the event is not queued for the group later. When the
			// event was for the binding's team, the skip is logged and kept
			// on the binding so its settings show it.
			var reason string
			if s.store.BoundedMetadataEnabled() {
				reason = chatEventBindingScopeBlock(projectMetadata, binding, templates)
			} else {
				reason = chatBindingScopeBlock(projectMetadata, binding)
			}
			if reason == "" && !s.chatBindingAuthorizerValid(ctx, workspace, binding) {
				reason = domain.ChatBindingBlockedAuthorizer
			}
			concerns := chatEventConcernsBinding(projectMetadata, binding, event, kinds, issue)
			if reason == "" && concerns {
				reason = chatEventResourceBlock(projectMetadata, binding, event, kinds, issue)
			}
			if reason != "" {
				if concerns {
					s.recordChatBindingSkip(ctx, workspace, record, binding, event, reason)
				}
				continue
			}
			if issue != nil && !chatIssueMatchesBinding(*issue, binding) {
				continue
			}
			requested := slices.ContainsFunc(requests, func(request domain.ChatIntakeRequest) bool {
				return request.ChatID == binding.ChatID && request.Status == domain.ChatRequestCreated
			})
			if projectUpdate && !chatProjectMatchesBinding(projectMetadata, binding, event.AggregateID) {
				continue
			}
			if event.Type == "initiative.update_created" && !chatInitiativeMatchesBinding(projectMetadata, binding, event.AggregateID) {
				continue
			}
			for _, kind := range kinds {
				// A request's thread hears its comments and status changes
				// (as a short note) whatever the binding's notifications; its
				// confirmation card is the request's own reply.
				switch {
				case requested && kind == "issue.created":
					continue
				case requested && kind == "issue.status_changed":
					kind = chatRequestStatusKind
				case requested && kind == "comment.created":
				case kind == chatRequestStatusKind && !requested:
					continue
				case requested && kind == chatRequestStatusKind:
				case kind != "refresh" && !slices.Contains(binding.Events, kind):
					continue
				}
				if kind == "refresh" && !binding.AllowUpdate {
					continue
				}
				payload := chatNotificationJob{BindingID: binding.ID, BindingScope: chatBindingFingerprint(binding), IssueID: event.AggregateID, Kind: kind, Request: requested}
				if kind == "comment.created" {
					payload.Event, payload.EventAt = event.Type, event.CreatedAt
				}
				if kind == "project.update_created" {
					// The job names the update, so its card shows that update.
					payload.ProjectID, payload.IssueID = event.AggregateID, ""
					child, _, _ := webhookChildOf(event.Payload)
					payload.UpdateID, payload.Event, payload.EventAt = child.ID, event.Type, event.CreatedAt
				}
				if kind == "initiative.update_created" {
					payload.InitiativeID, payload.IssueID = event.AggregateID, ""
					child, _, _ := webhookChildOf(event.Payload)
					payload.UpdateID, payload.EventAt = child.ID, event.CreatedAt
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
	if s.store.BoundedMetadataEnabled() {
		var close func()
		ctx, close = beginMetadataOutputReads(ctx)
		defer close()
	}
	var payload chatNotificationJob
	if json.Unmarshal(job.Payload, &payload) != nil {
		return chatInvalid("invalid stored notification")
	}
	if payload.InitiativeBindingID != "" {
		return s.chatSendInitiativeBindingUpdate(ctx, record, job, payload)
	}
	if payload.Kind == chatPreviewRefreshKind {
		return s.chatRefreshLinkPreviews(ctx, record, payload.PreviewResource)
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
	requestKind := payload.Kind == chatRequestResultKind || payload.Kind == chatRequestStatusKind || payload.Request && payload.Kind == "comment.created"
	if payload.Kind != "result" && payload.Kind != "refresh" && !requestKind && !slices.Contains(binding.Events, payload.Kind) {
		return nil
	}
	if payload.Kind == "project.update_created" {
		return s.chatSendProjectUpdate(ctx, record, binding, job, payload)
	}
	if payload.Kind == "initiative.update_created" {
		return s.chatSendInitiativeUpdate(ctx, record, binding, job, payload)
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
	switch payload.Kind {
	case chatRequestResultKind:
		return s.chatSendRequestResult(ctx, record, binding, issue, job, payload)
	case chatRequestStatusKind:
		return s.chatSendRequestStatus(ctx, record, binding, issue, job)
	}
	if payload.Kind == "issue.created" {
		// An issue made from a member's message (the message shortcut or a
		// request) answers in that message's thread itself.
		if thread, err := s.store.ChatThread(ctx, record.WorkspaceKey, record.ConnectionID, binding.ChatID, domain.ChatThreadResourceIssue, issue.ID); err == nil && thread.Origin != "" {
			return nil
		} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
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
		if err := s.chatAuthorizedPointRead(withChatIdentityAuthentication(ctx, identity), record.WorkspaceKey, identity.UserID, "/api/issue-records/"+url.PathEscape(issue.ID), &authorized); err != nil {
			if s.store.BoundedMetadataEnabled() && chatMessagePointReadTransient(err) {
				return err
			}
			return chatInvalid("result recipient access revoked")
		}
		link := fmt.Sprintf("%s/%s/issue/%s", s.chatAppOrigin(), url.PathEscape(record.WorkspaceKey), url.PathEscape(issue.Identifier))
		var settings domain.Bootstrap
		if s.store.BoundedMetadataEnabled() {
			display, found, err := s.store.BeginChatDisplayRead(ctx, record.WorkspaceKey, store.ChatDisplayRequest{UserID: identity.UserID})
			if err != nil {
				return err
			}
			if found {
				defer display.Close()
				settings = display.Data
			}
		} else {
			settings, _ = s.store.WorkspaceMetadataFields(record.WorkspaceKey, "userSettings")
		}
		if err := s.chatSendPersonalText(ctx, record, payload.PersonalOpenID, chatT(chatLanguage(settings, identity.UserID, record.Provider), "saved_link", issue.Identifier, link), chatKey(job.DedupKey, "success")); err != nil {
			return err
		}
		if payload.Operation != "create" {
			return s.chatRefreshIssueCards(ctx, record, binding, issue)
		}
	}
	return s.chatSendIssueCard(ctx, record, binding, issue, job.DedupKey, payload.SourceMessageID, payload.Kind)
}

// chatEventConcernsBinding: the event is one the binding asked for, about its
// team's work in its projects, so not delivering it is a skip worth recording.
// It ignores the sharing rules, which decide whether it is delivered.
func chatEventConcernsBinding(data domain.Bootstrap, binding domain.ChatBinding, event domain.DomainEvent, kinds []string, issue *domain.Issue) bool {
	if !slices.ContainsFunc(kinds, func(kind string) bool { return slices.Contains(binding.Events, kind) }) {
		return false
	}
	switch {
	case issue != nil:
		return issue.Team.ID == binding.TeamID && issue.ArchivedAt == nil && binding.CoversProject(chatIssueProjectID(*issue))
	case slices.Contains(kinds, "project.update_created"):
		return slices.ContainsFunc(data.Projects, func(project domain.Project) bool {
			return project.ID == event.AggregateID && project.ArchivedAt == nil && slices.Contains(project.TeamIDs, binding.TeamID) && binding.CoversProject(project.ID)
		})
	case event.Type == "initiative.update_created":
		return !chatBindingProjectScoped(binding) && slices.ContainsFunc(data.Initiatives, func(initiative domain.Initiative) bool {
			return initiative.ID == event.AggregateID && initiative.LeadTeamID != "" && initiative.LeadTeamID == binding.TeamID
		})
	}
	return false
}

// chatEventResourceBlock is why an event for a binding whose scope may be
// shared is still not delivered: the issue's own access restrictions, or a
// project or initiative shared with a team the group may not see.
func chatEventResourceBlock(data domain.Bootstrap, binding domain.ChatBinding, event domain.DomainEvent, kinds []string, issue *domain.Issue) string {
	switch {
	case issue != nil:
		if len(issue.Permissions) > 0 {
			return domain.ChatBindingBlockedIssueRestricted
		}
		if issue.Team.ArchivedAt != nil {
			return domain.ChatBindingBlockedTeamInactive
		}
		if issue.Team.Private && !chatPrivateBinding(binding) {
			return domain.ChatBindingBlockedTeamPrivate
		}
	case event.Type == "initiative.update_created":
		if !chatInitiativeMatchesBinding(data, binding, event.AggregateID) {
			return domain.ChatBindingBlockedInitiative
		}
	case slices.Contains(kinds, "project.update_created"):
		if !chatProjectMatchesBinding(data, binding, event.AggregateID) {
			return domain.ChatBindingBlockedProjects
		}
	}
	return ""
}

// recordChatBindingSkip logs a binding's skipped event and keeps it on the
// binding. Neither carries message content or secrets. A failed write is
// logged and does not hold the event cursor back.
func (s *server) recordChatBindingSkip(ctx context.Context, workspace string, record domain.ChatIntegrationRecord, binding domain.ChatBinding, event domain.DomainEvent, reason string) {
	slog.WarnContext(ctx, "chat binding skipped event", "workspace", workspace, "provider", record.Provider, "connection_id", record.ConnectionID, "binding_id", binding.ID, "team_id", binding.TeamID, "event_id", event.ID, "event_type", event.Type, "reason", reason)
	skip := domain.ChatBindingSkip{Reason: reason, EventID: event.ID, EventType: event.Type, At: time.Now().UTC()}
	if err := s.store.RecordChatBindingSkip(ctx, workspace, record.ConnectionID, binding.ID, skip); err != nil {
		slog.WarnContext(ctx, "chat binding skip not recorded", "workspace", workspace, "provider", record.Provider, "binding_id", binding.ID, "event_id", event.ID, "error", err.Error())
	}
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

func (s *server) chatSendInitiativeUpdate(ctx context.Context, record domain.ChatIntegrationRecord, binding domain.ChatBinding, job domain.ChatJob, payload chatNotificationJob) error {
	initiativeID := payload.InitiativeID
	if s.store.BoundedMetadataEnabled() {
		owner, found, err := s.store.BeginInstalledMetadataRead(ctx, record.WorkspaceKey)
		if err != nil {
			return err
		}
		if !found {
			return chatInvalid("initiative metadata unavailable")
		}
		defer owner.Close()
		bound, err := owner.Context(ctx)
		if err != nil {
			return err
		}
		pulse, found, err := s.store.BeginPulseRead(bound, record.WorkspaceKey, false)
		if err != nil {
			return err
		}
		if !found {
			return chatInvalid("initiative metadata unavailable")
		}
		defer pulse.Close()
		policy, found, err := s.store.BeginChatDisplayRead(bound, record.WorkspaceKey, store.ChatDisplayRequest{Policy: true})
		if err != nil {
			return err
		}
		if !found {
			return chatInvalid("initiative metadata unavailable")
		}
		defer policy.Close()
		data := policy.Data
		data.Initiatives = pulse.Snapshot.Initiatives
		if !chatInitiativeMatchesBinding(data, binding, initiativeID) {
			return nil
		}
		if err := pulse.LoadSource(bound, "initiative", initiativeID); err != nil {
			return err
		}
		data.Initiatives = pulse.Snapshot.Initiatives
		initiative, found := chatFindInitiative(data, initiativeID)
		if !found {
			return nil
		}
		display, found, err := s.store.BeginChatDisplayRead(bound, record.WorkspaceKey, store.ChatDisplayRequest{Directory: true, TeamID: initiative.LeadTeamID})
		if err != nil {
			return err
		}
		if !found {
			return chatInvalid("initiative metadata unavailable")
		}
		defer display.Close()
		data.Users, data.TeamSettings = display.Data.Users, display.Data.TeamSettings
		var ids []string
		if payload.UpdateID != "" {
			ids = []string{payload.UpdateID}
		}
		if err := pulse.LoadUpdates(bound, "initiative", initiativeID, ids); err != nil {
			return err
		}
		slug := firstNonEmpty(initiative.SlugID, initiative.ID)
		link := fmt.Sprintf("%s/%s/initiative/%s/overview", s.chatAppOrigin(), url.PathEscape(record.WorkspaceKey), url.PathEscape(slug))
		card := s.chatInitiativeUpdateCardFrom(chatGroupLanguage(record, binding), data, initiative, link, payload, pulse.Snapshot.InitiativeUpdates[initiativeID])
		content, _ := json.Marshal(card)
		return s.chatWorkflowRequest(ctx, record, http.MethodPost, "/open-apis/im/v1/messages?receive_id_type=chat_id", map[string]any{"receive_id": binding.ChatID, "msg_type": "interactive", "content": string(content), "uuid": chatKey(job.DedupKey, "initiative")[:32]}, nil)
	}
	data, ok := s.store.WorkspaceMetadataFields(record.WorkspaceKey, "initiatives", "teams", "teamSettings", "workspaceSettings", "users")
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
		card := s.chatInitiativeUpdateCard(chatGroupLanguage(record, binding), record, data, initiative, link, payload)
		content, _ := json.Marshal(card)
		return s.chatWorkflowRequest(ctx, record, http.MethodPost, "/open-apis/im/v1/messages?receive_id_type=chat_id", map[string]any{"receive_id": binding.ChatID, "msg_type": "interactive", "content": string(content), "uuid": chatKey(job.DedupKey, "initiative")[:32]}, nil)
	}
	return nil
}

func chatNotificationLabel(language, kind string) string {
	switch kind {
	case "issue.created", "issue.status_changed", "issue.assignee_changed", "comment.created":
		return chatT(language, kind)
	case chatRequestResultKind:
		return chatT(language, "request_received")
	case chatRequestStatusKind:
		return chatT(language, "request")
	default:
		return chatT(language, "saved")
	}
}

func (s *server) chatSendIssueCard(ctx context.Context, record domain.ChatIntegrationRecord, binding domain.ChatBinding, issue domain.Issue, key, sourceMessage, kind string) error {
	return s.chatSendIssueCardFrom(ctx, record, binding, issue, key, sourceMessage, kind, time.Time{})
}

// chatSendIssueCardFrom posts the card into the issue's thread in the group
// (chat_issue_threads.go); a thread it starts mirrors comments from syncFrom.
func (s *server) chatSendIssueCardFrom(ctx context.Context, record domain.ChatIntegrationRecord, binding domain.ChatBinding, issue domain.Issue, key, sourceMessage, kind string, syncFrom time.Time) error {
	if s.store.BoundedMetadataEnabled() {
		var close func()
		ctx, close = beginMetadataOutputReads(ctx)
		defer close()
	}
	card, actions, err := s.chatIssueCard(ctx, record, binding, issue, key, "", sourceMessage)
	if err != nil {
		return err
	}
	elements := card["elements"].([]map[string]any)
	label := chatCardText(chatNotificationLabel(chatGroupLanguage(record, binding), kind))
	card["elements"] = append([]map[string]any{label}, elements...)
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
	if s.store.BoundedMetadataEnabled() {
		var close func()
		ctx, close = beginMetadataOutputReads(ctx)
		defer close()
	}
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
