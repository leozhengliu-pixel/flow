package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// Link previews. When a Flow issue, project, document or initiative link is
// posted, the platform asks the app for a preview with the url.preview.get
// callback, once per viewer (operator), and shows the answer to that viewer
// only. Flow answers as the viewer's linked Flow account: what they cannot
// open in Flow is not expanded, and unlinked viewers only see a hint to link
// their account. In a group, content of private or restricted teams (and
// issues and documents with their own access lists) is expanded only when
// the group is bound to that team with its owners' authorization. With
// sensitive data protection on, nothing is expanded.

type chatPreviewCallback struct {
	Event struct {
		Operator struct {
			TenantKey string `json:"tenant_key"`
			OpenID    string `json:"open_id"`
		} `json:"operator"`
		Host    string `json:"host"`
		Context struct {
			URL          string `json:"url"`
			PreviewToken string `json:"preview_token"`
			MessageID    string `json:"open_message_id"`
			ChatID       string `json:"open_chat_id"`
		} `json:"context"`
	} `json:"event"`
}

// writeChatLinkPreview answers an authenticated url.preview.get callback.
func (s *server) writeChatLinkPreview(ctx context.Context, w http.ResponseWriter, record domain.ChatIntegrationRecord, plaintext []byte) {
	var callback chatPreviewCallback
	if json.Unmarshal(plaintext, &callback) != nil {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	event := callback.Event
	writeJSON(w, http.StatusOK, s.chatLinkPreview(ctx, record, event.Operator.TenantKey, event.Operator.OpenID, event.Context.URL, event.Context.ChatID))
}

type chatLinkTarget struct{ kind, id string }

// chatLinkTarget parses a link to this workspace's issue, project, document
// or initiative page.
func (s *server) chatLinkTarget(record domain.ChatIntegrationRecord, raw string) (chatLinkTarget, bool) {
	link, err := url.Parse(strings.TrimSpace(raw))
	origin, originErr := url.Parse(s.chatAppOrigin())
	if err != nil || originErr != nil || len(raw) > 2048 || link.Scheme != "https" && link.Scheme != "http" || !strings.EqualFold(link.Host, origin.Host) || link.User != nil {
		return chatLinkTarget{}, false
	}
	path := strings.TrimPrefix(link.Path, strings.TrimRight(origin.Path, "/"))
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 3 || parts[0] != record.WorkspaceKey || parts[2] == "" {
		return chatLinkTarget{}, false
	}
	switch parts[1] {
	case "issue", "project", "document", "initiative":
		return chatLinkTarget{kind: parts[1], id: parts[2]}, true
	}
	return chatLinkTarget{}, false
}

// chatPreviewScope decides what a chat may show: everything the viewer can
// open in a personal context, otherwise public teams plus the teams the
// group is bound to with a private-team authorization.
type chatPreviewScope struct {
	data     domain.Bootstrap
	personal bool
	bindings []domain.ChatBinding
}

func (scope chatPreviewScope) team(teamID string) bool {
	if scope.personal {
		return true
	}
	if chatPublicTeam(scope.data, teamID) {
		return true
	}
	return slices.ContainsFunc(scope.bindings, func(binding domain.ChatBinding) bool {
		return binding.TeamID == teamID && chatTeamShareable(scope.data, binding, teamID)
	})
}

func (scope chatPreviewScope) teams(ids ...string) bool {
	for _, id := range ids {
		if id != "" && !scope.team(id) {
			return false
		}
	}
	return true
}

func (s *server) chatPreviewScope(ctx context.Context, record domain.ChatIntegrationRecord, data domain.Bootstrap, chatID string) chatPreviewScope {
	scope := chatPreviewScope{data: data, personal: chatID == ""}
	if scope.personal {
		return scope
	}
	bindings, err := s.store.ChatBindings(ctx, record.WorkspaceKey, record.ConnectionID)
	if err == nil {
		for _, binding := range bindings {
			if binding.ChatID == chatID && binding.Enabled && binding.RevokedAt == nil && chatBindingScopePermitted(data, binding) && s.chatBindingAuthorizerValid(ctx, record.WorkspaceKey, binding) {
				scope.bindings = append(scope.bindings, binding)
			}
		}
	}
	if len(scope.bindings) > 0 {
		return scope
	}
	// A direct conversation is a personal context. Anything the platform
	// does not confirm in time is treated as a group.
	chatCtx, cancel := context.WithTimeout(ctx, 800*time.Millisecond)
	defer cancel()
	var chat struct {
		Data struct {
			ChatMode  string `json:"chat_mode"`
			TenantKey string `json:"tenant_key"`
		} `json:"data"`
	}
	if s.chatPlatformRequest(chatCtx, record, http.MethodGet, "/open-apis/im/v1/chats/"+url.PathEscape(chatID), nil, &chat) == nil && chat.Data.ChatMode == "p2p" {
		scope.personal = true
	}
	return scope
}

func chatPreviewHint() map[string]any {
	return map[string]any{"inline": map[string]any{"title": "Flow", "i18n_title": map[string]string{"en_us": "Flow · Connect your Flow account to preview links", "zh_cn": "Flow · 关联 Flow 账号后可预览链接"}}}
}

func chatPreviewField(en, zh, value string) map[string]any {
	return map[string]any{"is_short": true, "text": chatI18nPlain(chatTruncateText(en+": "+value, 200), chatTruncateText(zh+"："+value, 200))}
}

func chatPreviewResponse(title, link string, fields []map[string]any, summary string) map[string]any {
	elements := []map[string]any{}
	if summary != "" {
		elements = append(elements, chatCardText(chatTruncateText(summary, 300)))
	}
	if len(fields) > 0 {
		elements = append(elements, map[string]any{"tag": "div", "fields": fields})
	}
	elements = append(elements, map[string]any{"tag": "action", "actions": []map[string]any{{"tag": "button", "text": chatI18nPlain("Open in Flow", "在 Flow 中打开"), "url": link, "type": "default"}}})
	card := chatCard(title, elements...)
	delete(card["config"].(map[string]bool), "update_multi")
	return map[string]any{"inline": map[string]any{"title": chatTruncateText(title, 200)}, "card": map[string]any{"type": "raw", "data": card}}
}

func chatUserName(user *domain.User, empty string) string {
	if user == nil {
		return empty
	}
	return firstNonEmpty(user.DisplayName, user.Name)
}

func chatTeamNames(data domain.Bootstrap, ids []string) string {
	names := []string{}
	for _, id := range ids {
		for _, team := range data.Teams {
			if team.ID == id {
				names = append(names, team.Name)
			}
		}
	}
	return strings.Join(names, ", ")
}

// chatLinkPreview is the url.preview.get answer for one viewer; {} leaves
// the link unexpanded.
func (s *server) chatLinkPreview(ctx context.Context, record domain.ChatIntegrationRecord, tenantKey, openID, rawURL, chatID string) map[string]any {
	none := map[string]any{}
	if tenantKey != record.TenantKey || openID == "" {
		return none
	}
	target, ok := s.chatLinkTarget(record, rawURL)
	if !ok {
		return none
	}
	identity, err := s.store.ChatIdentityForOpenID(ctx, record.WorkspaceKey, record.ConnectionID, openID)
	if err != nil || identity.TenantKey != record.TenantKey {
		return chatPreviewHint()
	}
	data, ok := s.store.WorkspaceMetadataFields(record.WorkspaceKey, "teams", "teamSettings", "projects", "issueTemplates", "workspaceSettings")
	if !ok || data.WorkspaceSettings.HIPAACompliance {
		return none
	}
	ctx = withChatIdentityAuthentication(ctx, identity)
	base := s.chatAppOrigin() + "/" + url.PathEscape(record.WorkspaceKey)
	if target.kind == "issue" {
		var issue domain.Issue
		if s.chatAuthorizedMutation(ctx, record.WorkspaceKey, identity.UserID, http.MethodGet, "/api/issue-records/"+url.PathEscape(target.id), nil, &issue) != nil || issue.ID == "" {
			return none
		}
		scope := s.chatPreviewScope(ctx, record, data, chatID)
		if !scope.team(issue.Team.ID) || !scope.personal && len(issue.Permissions) > 0 {
			return none
		}
		fields := []map[string]any{
			chatPreviewField("Status", "状态", issue.State.Name),
			chatPreviewField("Assignee", "负责人", chatUserName(issue.Assignee, "Unassigned")),
			chatPreviewField("Priority", "优先级", priorityLabel(issue.Priority)),
			chatPreviewField("Team", "团队", issue.Team.Name),
		}
		return chatPreviewResponse(issue.Identifier+" "+issue.Title, base+"/issue/"+url.PathEscape(issue.Identifier), fields, "")
	}
	var metadata domain.Bootstrap
	if s.chatAuthorizedMutation(ctx, record.WorkspaceKey, identity.UserID, http.MethodGet, "/api/issue-records/bootstrap", nil, &metadata) != nil {
		return none
	}
	matches := func(id, slug string, previous []string) bool {
		return target.id == id || target.id == slug || slices.Contains(previous, target.id)
	}
	switch target.kind {
	case "project":
		for _, project := range metadata.Projects {
			if !matches(project.ID, project.SlugID, project.PreviousSlugIDs) {
				continue
			}
			if len(project.TeamIDs) > 0 && !slices.ContainsFunc(project.TeamIDs, func(id string) bool { return teamContentVisible(metadata, id) }) {
				return none
			}
			if !s.chatPreviewScope(ctx, record, data, chatID).teams(project.TeamIDs...) {
				return none
			}
			fields := []map[string]any{
				chatPreviewField("Status", "状态", project.Status.Name),
				chatPreviewField("Lead", "负责人", chatUserName(project.Lead, "No lead")),
				chatPreviewField("Priority", "优先级", priorityLabel(project.Priority)),
				chatPreviewField("Teams", "团队", chatTeamNames(data, project.TeamIDs)),
			}
			return chatPreviewResponse(project.Name, base+"/project/"+url.PathEscape(firstNonEmpty(project.SlugID, project.ID))+"/overview", fields, project.Summary)
		}
	case "initiative":
		for _, initiative := range metadata.Initiatives {
			if !matches(initiative.ID, initiative.SlugID, initiative.PreviousSlugIDs) {
				continue
			}
			teams := append([]string{initiative.LeadTeamID}, initiative.ContributingTeamIDs...)
			if !s.chatPreviewScope(ctx, record, data, chatID).teams(teams...) {
				return none
			}
			fields := []map[string]any{
				chatPreviewField("Status", "状态", initiative.Status),
				chatPreviewField("Owner", "负责人", chatUserName(initiative.Owner, "No owner")),
				chatPreviewField("Teams", "团队", chatTeamNames(data, teams)),
			}
			return chatPreviewResponse(initiative.Name, base+"/initiative/"+url.PathEscape(firstNonEmpty(initiative.SlugID, initiative.ID))+"/overview", fields, initiative.Summary)
		}
	case "document":
		document, err := documentByID(&metadata, target.id)
		if err != nil || documentRole(s, metadata, *document) == "none" {
			return none
		}
		scope := s.chatPreviewScope(ctx, record, data, chatID)
		teams := append(append(slices.Clone(document.TeamIDs), document.InheritedTeamIDs...), document.IssueTeamID)
		if !scope.teams(teams...) {
			return none
		}
		// A document shared with specific people is not shown to a group.
		if !scope.personal && len(document.Permissions) > 0 && !slices.ContainsFunc(document.Permissions, func(permission domain.DocumentPermission) bool { return permission.SubjectType == "workspace" }) {
			return none
		}
		title := firstNonEmpty(strings.TrimSpace(document.Title), "Untitled document")
		summary := strings.Join(strings.Fields(markdownToPlain(document.Content)), " ")
		return chatPreviewResponse(title, fmt.Sprintf("%s/document/%s", base, url.PathEscape(firstNonEmpty(document.SlugID, document.ID))), nil, summary)
	}
	return none
}
