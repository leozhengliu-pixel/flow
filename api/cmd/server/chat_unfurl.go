package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// Link previews. When a Flow link is posted, the platform asks the app for a
// preview with the url.preview.get callback, once per viewer (operator), and
// shows the answer to that viewer only.
//
// Whose permissions apply: the viewer's. Flow answers as the viewer's linked
// Flow account and reads the item through the same API the web app uses, so
// what the viewer cannot open in Flow is never described. On top of that, in
// a group (any chat that is not the viewer's direct conversation with the
// bot), content of private or restricted teams, issues with their own access
// list and documents shared with specific people is described only when the
// group is bound to that team with its owners' authorization (see
// chatPreviewScope): a member's own access never discloses a private item to
// the rest of a group. Anything that fails either check, and links to pages
// that have no preview, get the neutral "Flow link" line with no content.
// Unlinked viewers get a hint to link their account. With sensitive data
// protection on, links are not expanded at all.
//
// The preview is in the viewer's Flow language, with people named by their
// display name and times in the viewer's time zone. The platform caches a
// preview; Flow remembers the preview tokens of each item it described and
// asks the platform to request them again when the item changes
// (chat_unfurl_refresh.go).

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

// chatLinkPreviewMarker names the record that the platform asked this
// connection for link previews in the month of at.
func chatLinkPreviewMarker(record domain.ChatIntegrationRecord, at time.Time) string {
	return "chat_link_preview_" + chatKey(record.ConnectionID, at.UTC().Format("2006-01"))
}

// chatNoteLinkPreview records that the app's link preview is configured: the
// platform asks for previews only then. It lasts about a month after the
// latest request.
func (s *server) chatNoteLinkPreview(ctx context.Context, record domain.ChatIntegrationRecord) {
	now := time.Now().UTC()
	id := chatLinkPreviewMarker(record, now)
	if _, err := s.store.GetChatAction(ctx, record.WorkspaceKey, record.ConnectionID, id); err == nil {
		return
	}
	_ = s.store.PutChatAction(ctx, domain.ChatAction{ID: id, WorkspaceKey: record.WorkspaceKey, ConnectionID: record.ConnectionID, Payload: json.RawMessage(`{"kind":"link_preview"}`), ExpiresAt: now.Add(31*24*time.Hour - time.Minute)})
}

// chatLinkPreviewConfigured reports whether the platform asked for link
// previews this month or the last: a pasted link then already shows the
// platform's preview.
func (s *server) chatLinkPreviewConfigured(ctx context.Context, record domain.ChatIntegrationRecord) bool {
	now := time.Now().UTC()
	lastMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Add(-time.Hour)
	for _, at := range []time.Time{now, lastMonth} {
		if _, err := s.store.GetChatAction(ctx, record.WorkspaceKey, record.ConnectionID, chatLinkPreviewMarker(record, at)); err == nil {
			return true
		}
	}
	return false
}

// writeChatLinkPreview answers an authenticated url.preview.get callback.
func (s *server) writeChatLinkPreview(ctx context.Context, w http.ResponseWriter, record domain.ChatIntegrationRecord, plaintext []byte) {
	s.chatNoteLinkPreview(ctx, record)
	var callback chatPreviewCallback
	if json.Unmarshal(plaintext, &callback) != nil {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	event := callback.Event
	preview, resource := s.chatLinkPreviewFor(ctx, record, event.Operator.TenantKey, event.Operator.OpenID, event.Context.URL, event.Context.ChatID)
	if resource != "" && event.Context.PreviewToken != "" {
		s.chatRememberPreviewToken(ctx, record, resource, event.Context.PreviewToken, event.Operator.OpenID)
	}
	writeJSON(w, http.StatusOK, preview)
}

type chatLinkTarget struct {
	// kind: issue, comment, project, project_update, initiative, document,
	// view, cycle, team, workspace or unknown (another page of the
	// workspace).
	kind, id string
	// team is the team key of cycle and team links; anchor the comment or
	// update a link points at.
	team, anchor string
}

// chatLinkTarget parses a link to a page of this workspace.
func (s *server) chatLinkTarget(record domain.ChatIntegrationRecord, raw string) (chatLinkTarget, bool) {
	link, err := url.Parse(strings.TrimSpace(raw))
	origin, originErr := url.Parse(s.chatAppOrigin())
	if err != nil || originErr != nil || len(raw) > 2048 || link.Scheme != "https" && link.Scheme != "http" || !strings.EqualFold(link.Host, origin.Host) || link.User != nil {
		return chatLinkTarget{}, false
	}
	path := strings.TrimPrefix(link.Path, strings.TrimRight(origin.Path, "/"))
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] != record.WorkspaceKey {
		return chatLinkTarget{}, false
	}
	if len(parts) == 1 {
		return chatLinkTarget{kind: "workspace"}, true
	}
	if len(parts) < 3 || parts[2] == "" {
		return chatLinkTarget{kind: "unknown"}, true
	}
	target := chatLinkTarget{kind: parts[1], id: parts[2]}
	switch parts[1] {
	case "issue":
		if anchor, ok := strings.CutPrefix(link.Fragment, "comment-"); ok && anchor != "" {
			target.kind, target.anchor = "comment", anchor
		}
	case "project":
		if anchor, ok := strings.CutPrefix(link.Fragment, "update-"); ok && anchor != "" {
			target.kind, target.anchor = "project_update", anchor
		}
	case "initiative", "document", "view":
	case "team":
		target.team = parts[2]
		target.kind, target.id = "team", ""
		if len(parts) >= 5 && parts[3] == "cycle" {
			target.kind, target.id = "cycle", parts[4]
		}
	default:
		return chatLinkTarget{kind: "unknown"}, true
	}
	return target, true
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

// chatPreviewTitleLimit bounds a preview's link line, in runes.
const chatPreviewTitleLimit = 80

// chatPreviewSummaryLimit bounds a preview's text, in runes.
const chatPreviewSummaryLimit = 240

// chatLinkPreviewTitle is the preview's link line, cut at a rune boundary.
func chatLinkPreviewTitle(title string) string {
	runes := []rune(strings.TrimSpace(title))
	if len(runes) <= chatPreviewTitleLimit {
		return string(runes)
	}
	return string(runes[:chatPreviewTitleLimit-1]) + "…"
}

// chatPreviewText cuts plain text for a preview: whitespace folded, at a
// rune boundary.
func chatPreviewSnippet(text string, limit int) string {
	runes := []rune(strings.Join(strings.Fields(text), " "))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit-1]) + "…"
}

// chatPreviewHint asks an unlinked viewer to link their account. Their Flow
// language is unknown: the platform's default.
func chatPreviewHint(record domain.ChatIntegrationRecord) map[string]any {
	return map[string]any{"inline": map[string]any{"title": chatT(chatProviderLanguage(record.Provider), "preview_connect")}}
}

// chatPreviewNeutral is the preview of a link the viewer (or the group) may
// not see, or of a page without a preview: no content at all.
func chatPreviewNeutral(language string) map[string]any {
	return map[string]any{"inline": map[string]any{"title": chatT(language, "preview_neutral")}}
}

type chatPreviewField struct{ label, value string }

// chatPreviewContent is what a preview card shows: the link line (also the
// card's header), the header colour, short field columns, a text and a
// footnote.
type chatPreviewContent struct {
	title, link, template string
	fields                []chatPreviewField
	// summary is card markdown (already escaped).
	summary string
	note    string
}

// chatPreviewAnswer is the url.preview.get answer: the inline link line and
// the card the platform shows when the preview is expanded.
func chatPreviewAnswer(language string, content chatPreviewContent) map[string]any {
	title := chatLinkPreviewTitle(content.title)
	elements := []map[string]any{}
	if content.summary != "" {
		elements = append(elements, chatMarkdownElement(content.summary))
	}
	fields := []map[string]any{}
	for _, field := range content.fields {
		if strings.TrimSpace(field.value) == "" {
			continue
		}
		fields = append(fields, map[string]any{"is_short": true, "text": map[string]any{"tag": "lark_md", "content": "**" + chatPreviewEscape(field.label) + "**\n" + chatPreviewEscape(chatPreviewSnippet(field.value, 120))}})
	}
	if len(fields) > 0 {
		elements = append(elements, map[string]any{"tag": "div", "fields": fields})
	}
	if content.note != "" {
		elements = append(elements, map[string]any{"tag": "note", "elements": []map[string]any{chatPlainText(content.note)}})
	}
	elements = append(elements, map[string]any{"tag": "action", "actions": []map[string]any{{"tag": "button", "text": chatPlainText(chatT(language, "open_in_flow")), "url": content.link, "type": "primary"}}})
	card := chatCard(title, elements...)
	delete(card["config"].(map[string]bool), "update_multi")
	if content.template != "" {
		card["header"].(map[string]any)["template"] = content.template
	}
	return map[string]any{"inline": map[string]any{"title": title}, "card": map[string]any{"type": "raw", "data": card}}
}

// chatPreviewEscape keeps a value from being read as card markdown: emphasis,
// links (an escaped "[" never opens one, so parentheses stay as written),
// tags and code.
func chatPreviewEscape(text string) string {
	var builder strings.Builder
	for _, r := range text {
		if strings.ContainsRune("\\`*_~[]<>", r) {
			builder.WriteRune('\\')
		}
		builder.WriteRune(r)
	}
	return builder.String()
}

func chatPlainText(text string) map[string]any {
	return map[string]any{"tag": "plain_text", "content": chatTruncateText(text, 2048)}
}

func chatUserName(user *domain.User, empty string) string {
	return firstNonEmpty(chatPersonLabel(user, nil), empty)
}

func chatTeamNames(data domain.Bootstrap, ids []string) string {
	names := []string{}
	for _, id := range ids {
		for _, team := range data.Teams {
			if team.ID == id && !slices.Contains(names, team.Name) {
				names = append(names, team.Name)
			}
		}
	}
	return strings.Join(names, ", ")
}

// chatStateTemplate is the header colour of an issue in a state of this type.
func chatStateTemplate(stateType string) string {
	switch stateType {
	case "started":
		return "blue"
	case "completed":
		return "green"
	case "triage":
		return "orange"
	}
	return "grey"
}

// chatPreviewDate writes a calendar date (YYYY-MM-DD) in the language.
func chatPreviewDate(language string, value *string) string {
	if value == nil || *value == "" {
		return ""
	}
	day, err := time.Parse("2006-01-02", *value)
	if err != nil {
		return *value
	}
	return day.Format(chatT(language, "date_layout"))
}

func chatPreviewLabels(language string, labels []domain.IssueLabel) string {
	names := []string{}
	for _, label := range labels {
		if len(names) == 3 {
			names = append(names, chatT(language, "more_count", len(labels)-3))
			break
		}
		names = append(names, label.Name)
	}
	return strings.Join(names, chatT(language, "list_separator"))
}

// chatPreviewViewer is who a preview is for: their language, time zone and
// the workspace's people.
type chatPreviewViewer struct {
	language, zone string
	users          []domain.User
}

func (viewer chatPreviewViewer) time(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	return chatFormatTime(viewer.language, at, viewer.zone)
}

// chatLinkPreview is the url.preview.get answer for one viewer; {} leaves
// the link unexpanded.
func (s *server) chatLinkPreview(ctx context.Context, record domain.ChatIntegrationRecord, tenantKey, openID, rawURL, chatID string) map[string]any {
	preview, _ := s.chatLinkPreviewFor(ctx, record, tenantKey, openID, rawURL, chatID)
	return preview
}

// chatLinkPreviewFor answers a preview request and names the item it
// described ("" for none), whose changes refresh the preview.
func (s *server) chatLinkPreviewFor(ctx context.Context, record domain.ChatIntegrationRecord, tenantKey, openID, rawURL, chatID string) (map[string]any, string) {
	none := map[string]any{}
	if tenantKey != record.TenantKey || openID == "" {
		return none, ""
	}
	target, ok := s.chatLinkTarget(record, rawURL)
	if !ok {
		return none, ""
	}
	identity, err := s.store.ChatIdentityForOpenID(ctx, record.WorkspaceKey, record.ConnectionID, openID)
	if err != nil || identity.TenantKey != record.TenantKey {
		return chatPreviewHint(record), ""
	}
	data, ok := s.store.WorkspaceMetadataFields(record.WorkspaceKey, "workspace", "teams", "teamSettings", "projects", "issueTemplates", "workspaceSettings", "userSettings", "users")
	if !ok || data.WorkspaceSettings.HIPAACompliance {
		return none, ""
	}
	viewer := chatPreviewViewer{language: chatLanguage(data, identity.UserID, record.Provider), zone: data.UserSettings[identity.UserID].Timezone, users: data.Users}
	neutral := chatPreviewNeutral(viewer.language)
	ctx = withChatIdentityAuthentication(ctx, identity)
	base := s.chatAppOrigin() + "/" + url.PathEscape(record.WorkspaceKey)
	switch target.kind {
	case "unknown":
		return neutral, ""
	case "workspace":
		name := firstNonEmpty(data.Workspace.Name, record.WorkspaceKey)
		return chatPreviewAnswer(viewer.language, chatPreviewContent{title: name, link: base}), ""
	case "issue", "comment":
		var issue domain.Issue
		if s.chatAuthorizedMutation(ctx, record.WorkspaceKey, identity.UserID, http.MethodGet, "/api/issue-records/"+url.PathEscape(target.id), nil, &issue) != nil || issue.ID == "" {
			return neutral, ""
		}
		scope := s.chatPreviewScope(ctx, record, data, chatID)
		if !scope.team(issue.Team.ID) || !scope.personal && len(issue.Permissions) > 0 {
			return neutral, ""
		}
		content := chatIssuePreview(viewer, issue, base)
		if target.kind == "comment" {
			comment, err := s.store.ResourceComment(ctx, record.WorkspaceKey, issue.ID, target.anchor)
			if err == nil {
				author := firstNonEmpty(chatPersonLabel(&comment.User, viewer.users), chatT(viewer.language, "comment_heading_unknown"))
				content.summary = "**" + chatPreviewEscape(author) + "**" + chatT(viewer.language, "label_separator") + chatPreviewEscape(chatPreviewSnippet(markdownToPlain(comment.Body), chatPreviewSummaryLimit))
				content.link += "#comment-" + url.PathEscape(comment.ID)
			}
		}
		return chatPreviewAnswer(viewer.language, content), "issue:" + issue.ID
	}
	var metadata domain.Bootstrap
	if s.chatAuthorizedMutation(ctx, record.WorkspaceKey, identity.UserID, http.MethodGet, "/api/issue-records/bootstrap", nil, &metadata) != nil {
		return neutral, ""
	}
	matches := func(id, slug string, previous []string) bool {
		return target.id == id || target.id == slug || slices.Contains(previous, target.id)
	}
	scope := func() chatPreviewScope { return s.chatPreviewScope(ctx, record, data, chatID) }
	switch target.kind {
	case "project", "project_update":
		for _, project := range metadata.Projects {
			if !matches(project.ID, project.SlugID, project.PreviousSlugIDs) {
				continue
			}
			if len(project.TeamIDs) > 0 && !slices.ContainsFunc(project.TeamIDs, func(id string) bool { return teamContentVisible(metadata, id) }) || !scope().teams(project.TeamIDs...) {
				return neutral, ""
			}
			updates := []domain.ProjectUpdate{}
			if feed, ok := s.store.PulseFeed(record.WorkspaceKey); ok {
				updates = feed.ProjectUpdates[project.ID]
			}
			link := base + "/project/" + url.PathEscape(firstNonEmpty(project.SlugID, project.ID)) + "/overview"
			if target.kind == "project_update" {
				index := slices.IndexFunc(updates, func(update domain.ProjectUpdate) bool { return update.ID == target.anchor })
				if index < 0 {
					return neutral, ""
				}
				return chatPreviewAnswer(viewer.language, chatProjectUpdatePreview(viewer, project, updates[index], link, s.chatAppOrigin())), "project:" + project.ID
			}
			return chatPreviewAnswer(viewer.language, chatProjectPreview(viewer, data, project, updates, link)), "project:" + project.ID
		}
	case "initiative":
		for _, initiative := range metadata.Initiatives {
			if !matches(initiative.ID, initiative.SlugID, initiative.PreviousSlugIDs) {
				continue
			}
			teams := append([]string{initiative.LeadTeamID}, initiative.ContributingTeamIDs...)
			if !scope().teams(teams...) {
				return neutral, ""
			}
			health, template := chatHealthText(viewer.language, initiative.Health)
			content := chatPreviewContent{
				title: initiative.Name, link: base + "/initiative/" + url.PathEscape(firstNonEmpty(initiative.SlugID, initiative.ID)) + "/overview", template: template,
				fields: []chatPreviewField{
					{chatT(viewer.language, "health"), health},
					{chatT(viewer.language, "owner"), firstNonEmpty(chatPersonLabel(initiative.Owner, viewer.users), chatT(viewer.language, "no_owner"))},
					{chatT(viewer.language, "target_date"), chatPreviewDate(viewer.language, initiative.TargetDate)},
					{chatT(viewer.language, "teams"), chatTeamNames(data, teams)},
				},
				summary: chatPreviewEscape(chatPreviewSnippet(initiative.Summary, chatPreviewSummaryLimit)),
			}
			return chatPreviewAnswer(viewer.language, content), "initiative:" + initiative.ID
		}
	case "document":
		document, err := documentByID(&metadata, target.id)
		if err != nil || documentRole(s, metadata, *document) == "none" {
			return neutral, ""
		}
		teams := append(append(slices.Clone(document.TeamIDs), document.InheritedTeamIDs...), document.IssueTeamID)
		current := scope()
		if !current.teams(teams...) {
			return neutral, ""
		}
		// A document shared with specific people is not shown to a group.
		if !current.personal && len(document.Permissions) > 0 && !slices.ContainsFunc(document.Permissions, func(permission domain.DocumentPermission) bool { return permission.SubjectType == "workspace" }) {
			return neutral, ""
		}
		return chatPreviewAnswer(viewer.language, chatDocumentPreview(viewer, data, *document, base)), "document:" + document.ID
	case "view":
		for _, view := range metadata.SavedViews {
			if !matches(view.ID, view.SlugID, nil) {
				continue
			}
			current := scope()
			// A personal view stays with its owner; a team's view follows the team.
			if view.SharedAt == nil && view.OwnerID != "" && (!current.personal || view.OwnerID != identity.UserID) || !current.teams(view.TeamID) {
				return neutral, ""
			}
			content := chatPreviewContent{title: view.Name, link: base + "/view/" + url.PathEscape(firstNonEmpty(view.SlugID, view.ID)), template: "indigo",
				fields:  []chatPreviewField{{chatT(viewer.language, "team"), chatTeamNames(data, []string{view.TeamID})}},
				summary: chatPreviewEscape(chatPreviewSnippet(view.Description, chatPreviewSummaryLimit))}
			return chatPreviewAnswer(viewer.language, content), ""
		}
	case "team", "cycle":
		index := slices.IndexFunc(metadata.Teams, func(team domain.Team) bool { return strings.EqualFold(team.Key, target.team) })
		if index < 0 {
			return neutral, ""
		}
		team := metadata.Teams[index]
		if !teamContentVisible(metadata, team.ID) || !scope().team(team.ID) {
			return neutral, ""
		}
		if target.kind == "team" {
			content := chatPreviewContent{title: team.Name, link: base + "/team/" + url.PathEscape(team.Key) + "/active", template: "indigo",
				fields:  []chatPreviewField{{chatT(viewer.language, "team_key"), team.Key}},
				summary: chatPreviewEscape(chatPreviewSnippet(teamSettings(&data, team.ID).Description, chatPreviewSummaryLimit))}
			return chatPreviewAnswer(viewer.language, content), ""
		}
		number, err := strconv.Atoi(target.id)
		if err != nil {
			return neutral, ""
		}
		for _, cycle := range metadata.Cycles {
			if cycle.TeamID != team.ID || cycle.Number != number {
				continue
			}
			return chatPreviewAnswer(viewer.language, chatCyclePreview(viewer, team, cycle, base, time.Now())), ""
		}
	}
	return neutral, ""
}

func chatIssuePreview(viewer chatPreviewViewer, issue domain.Issue, base string) chatPreviewContent {
	language := viewer.language
	assignee := firstNonEmpty(chatPersonLabel(issue.Assignee, viewer.users), chatT(language, "unassigned"))
	fields := []chatPreviewField{
		{chatT(language, "status"), issue.State.Name},
		{chatT(language, "priority"), chatPriorityText(language, issue.Priority)},
		{chatT(language, "assignee"), assignee},
		{chatT(language, "labels"), chatPreviewLabels(language, issue.Labels)},
	}
	if issue.Project != nil {
		fields = append(fields, chatPreviewField{chatT(language, "project"), issue.Project.Name})
	}
	fields = append(fields, chatPreviewField{chatT(language, "due_date"), chatPreviewDate(language, issue.DueDate)}, chatPreviewField{chatT(language, "team"), issue.Team.Name})
	return chatPreviewContent{
		title: issue.Identifier + " " + issue.Title, link: base + "/issue/" + url.PathEscape(issue.Identifier), template: chatStateTemplate(issue.State.Type), fields: fields,
		note: chatT(language, "updated_at", viewer.time(issue.UpdatedAt)),
	}
}

func chatProjectPreview(viewer chatPreviewViewer, data domain.Bootstrap, project domain.Project, updates []domain.ProjectUpdate, link string) chatPreviewContent {
	language := viewer.language
	health, template := chatHealthText(language, project.Health)
	content := chatPreviewContent{title: project.Name, link: link, template: template, fields: []chatPreviewField{
		{chatT(language, "status"), project.Status.Name},
		{chatT(language, "health"), health},
		{chatT(language, "lead"), firstNonEmpty(chatPersonLabel(project.Lead, viewer.users), chatT(language, "no_lead"))},
		{chatT(language, "progress"), chatT(language, "percent", int(project.Progress*100+0.5))},
		{chatT(language, "target_date"), chatPreviewDate(language, project.TargetDate)},
		{chatT(language, "teams"), chatTeamNames(data, project.TeamIDs)},
	}}
	if len(updates) > 0 {
		latest := updates[0]
		content.summary = chatPreviewEscape(chatPreviewSnippet(markdownToPlain(latest.Body), chatPreviewSummaryLimit))
		content.note = chatT(language, "latest_update", firstNonEmpty(chatPersonLabel(&latest.User, viewer.users), chatT(language, "comment_heading_unknown")), viewer.time(latest.CreatedAt))
	} else {
		content.summary = chatPreviewEscape(chatPreviewSnippet(project.Summary, chatPreviewSummaryLimit))
	}
	return content
}

func chatProjectUpdatePreview(viewer chatPreviewViewer, project domain.Project, update domain.ProjectUpdate, link, origin string) chatPreviewContent {
	language := viewer.language
	health, template := chatHealthText(language, update.Health)
	body, truncated := chatCardMarkdown(chatUpdateDocument(chatProjectPulseUpdate(update)), chatPreviewSummaryLimit, chatMarkdownContext{origin: origin, directory: viewer.users})
	if truncated {
		body += " …"
	}
	return chatPreviewContent{title: project.Name + " · " + chatT(language, "project_update"), link: link + "#update-" + url.PathEscape(update.ID), template: template, summary: body,
		fields: []chatPreviewField{
			{chatT(language, "health"), health},
			{chatT(language, "author"), firstNonEmpty(chatPersonLabel(&update.User, viewer.users), chatT(language, "comment_heading_unknown"))},
			{chatT(language, "posted"), viewer.time(update.CreatedAt)},
		}}
}

func chatDocumentPreview(viewer chatPreviewViewer, data domain.Bootstrap, document domain.Document, base string) chatPreviewContent {
	language := viewer.language
	editor := document.Creator
	if count := len(document.Revisions); count > 0 {
		editor = document.Revisions[count-1].Author
	}
	projects := []string{}
	for _, project := range data.Projects {
		if slices.Contains(document.ProjectIDs, project.ID) {
			projects = append(projects, project.Name)
		}
	}
	return chatPreviewContent{
		title: firstNonEmpty(strings.TrimSpace(document.Title), chatT(language, "untitled_document")), link: fmt.Sprintf("%s/document/%s", base, url.PathEscape(firstNonEmpty(document.SlugID, document.ID))), template: "wathet",
		summary: chatPreviewEscape(chatPreviewSnippet(markdownToPlain(document.Content), chatPreviewSummaryLimit)),
		fields: []chatPreviewField{
			{chatT(language, "teams"), chatTeamNames(data, append(slices.Clone(document.TeamIDs), document.IssueTeamID))},
			{chatT(language, "project"), strings.Join(projects, chatT(language, "list_separator"))},
		},
		note: chatT(language, "last_edited", firstNonEmpty(chatPersonLabel(&editor, viewer.users), chatT(language, "comment_heading_unknown")), viewer.time(document.UpdatedAt)),
	}
}

func chatCyclePreview(viewer chatPreviewViewer, team domain.Team, cycle domain.Cycle, base string, now time.Time) chatPreviewContent {
	language := viewer.language
	name := firstNonEmpty(cycle.Name, chatT(language, "cycle_number", cycle.Number))
	status, template := chatT(language, "cycle_active"), "blue"
	switch {
	case now.Before(cycle.StartsAt):
		status, template = chatT(language, "cycle_upcoming"), "grey"
	case !cycle.EndsAt.IsZero() && now.After(cycle.EndsAt):
		status, template = chatT(language, "cycle_completed"), "green"
	}
	layout := chatT(language, "date_layout")
	return chatPreviewContent{title: team.Name + " · " + name, link: base + "/team/" + url.PathEscape(team.Key) + "/cycle/" + strconv.Itoa(cycle.Number), template: template,
		fields: []chatPreviewField{
			{chatT(language, "status"), status},
			{chatT(language, "dates"), chatT(language, "date_range", cycle.StartsAt.Format(layout), cycle.EndsAt.Format(layout))},
		}}
}
