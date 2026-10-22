package main

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"flow/api/internal/domain"
)

// App mentions written as markdown (@<app name>) by MCP callers become the same
// structured user-mention node the editor stores. The store then starts the
// agent session for it (store.syncApplicationMentions) and the session is
// delivered like a UI mention. Only apps the author may start become nodes;
// everything else stays plain text and is reported back in the tool result.

// mentionAppPolicy says which apps the author of a comment may mention.
type mentionAppPolicy struct {
	author        domain.User
	guestBlocked  bool
	teamIDs       []string // teams of the commented resource; nil when unknown (the store decides)
	notes         []string
	mentionedApps []domain.User
}

func (m *mentionResolver) addApp(user domain.User) {
	if m.apps == nil {
		m.apps = map[string][]domain.User{}
	}
	for _, name := range []string{user.DisplayName, user.Name} {
		key := strings.ToLower(strings.TrimSpace(name))
		if utf8.RuneCountInString(key) < 2 || slices.ContainsFunc(m.apps[key], func(item domain.User) bool { return item.ID == user.ID }) {
			continue
		}
		if len(m.apps[key]) == 0 {
			m.appNames = append(m.appNames, key)
		}
		m.apps[key] = append(m.apps[key], user)
	}
	slices.SortStableFunc(m.appNames, func(left, right string) int { return len(right) - len(left) })
}

// enableAppMentions turns @<app name> into structured mentions for the given author.
func (m *mentionResolver) enableAppMentions(author domain.User, role string, preventGuestAgents bool, teamIDs []string) {
	m.appPolicy = &mentionAppPolicy{author: author, guestBlocked: role == "guest" && preventGuestAgents, teamIDs: teamIDs}
}

// appMentionSpan handles an @ at start when it names an app. It reports true
// when it consumed the position (a mention node or an explained refusal).
func (m *mentionResolver) appMentionSpan(text string, start int, free func(int, int) bool, spans *[]mentionSpan) bool {
	matches := func(name string) bool {
		end := start + 1 + len(name)
		return end <= len(text) && strings.EqualFold(text[start+1:end], name) && (end == len(text) || !isWordByte(text[end]))
	}
	appName := ""
	for _, name := range m.appNames {
		if matches(name) {
			appName = name
			break
		}
	}
	if appName == "" {
		return false
	}
	policy := m.appPolicy
	personLen := 0
	for _, person := range m.people {
		if matches(person.name) {
			personLen = len(person.name)
			break
		}
	}
	candidates := m.apps[appName]
	switch {
	case personLen > len(appName):
		return false
	case personLen == len(appName):
		policy.note(fmt.Sprintf("@%s names a person and the app %s; it was left as a person mention and the app was not started. Mention the app with its structured mention instead.", text[start+1:start+1+len(appName)], candidates[0].DisplayName))
		return false
	case len(candidates) > 1:
		names := []string{}
		for _, candidate := range candidates {
			names = append(names, candidate.ID)
		}
		policy.note(fmt.Sprintf("@%s is ambiguous: it matches %d apps (%s). No app was started; use a structured mention with the app's user id.", text[start+1:start+1+len(appName)], len(candidates), strings.Join(names, ", ")))
		return true
	}
	app := candidates[0]
	if reason := m.appRefusal(app); reason != "" {
		policy.note(fmt.Sprintf("@%s was not turned into a mention and started nothing: %s.", app.DisplayName, reason))
		return true
	}
	end := start + 1 + len(appName)
	if free(start, end) {
		*spans = append(*spans, mentionSpan{start, end, m.userAttrs(app)})
		if !slices.ContainsFunc(policy.mentionedApps, func(item domain.User) bool { return item.ID == app.ID }) {
			policy.mentionedApps = append(policy.mentionedApps, app)
		}
	}
	return true
}

// appRefusal explains why the author may not start app, or "" when they may.
func (m *mentionResolver) appRefusal(app domain.User) string {
	policy := m.appPolicy
	switch {
	case policy.author.App:
		return "comments written by an app cannot start another app"
	case app.ID == policy.author.ID:
		return "an app cannot mention itself"
	case !app.Active:
		return "the app is not active in this workspace"
	case !slices.Contains(app.AppScopes, "app:mentionable"):
		return "the app does not accept mentions (it lacks the app:mentionable capability)"
	case policy.guestBlocked:
		return "this workspace does not let guests start agents"
	}
	if policy.teamIDs != nil {
		covered := slices.ContainsFunc(policy.teamIDs, func(id string) bool {
			if slices.Contains(app.AppTeamIDs, id) {
				return true
			}
			if !app.AppAllPublicTeams {
				return false
			}
			index := slices.IndexFunc(m.data.Teams, func(team domain.Team) bool { return team.ID == id })
			return index >= 0 && !m.data.Teams[index].Private && m.data.Teams[index].RetiredAt == nil
		})
		if !covered {
			return "the app has no access to this team"
		}
	}
	return ""
}

func structToMap(value any) (map[string]any, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	return result, json.Unmarshal(raw, &result)
}

func (p *mentionAppPolicy) note(text string) {
	if !slices.Contains(p.notes, text) {
		p.notes = append(p.notes, text)
	}
}

// mentionTeamIDs returns the teams a comment target belongs to (a parent
// resource or an existing comment), or nil when they cannot be told.
func mentionTeamIDs(data domain.Bootstrap, targetID string) []string {
	resolve := func(id string) []string {
		if issue, err := mcpFindIssue(data, id); err == nil {
			return []string{issue.Team.ID}
		}
		if project, err := fullProjectByID(&data, id); err == nil && len(project.TeamIDs) > 0 {
			return project.TeamIDs
		}
		if document, err := mcpFindDocument(data, id); err == nil {
			teams := slices.Clone(document.TeamIDs)
			if document.IssueID != "" {
				if issue, err := mcpFindIssue(data, document.IssueID); err == nil {
					teams = append(teams, issue.Team.ID)
				}
			}
			for _, projectID := range document.ProjectIDs {
				if project, err := fullProjectByID(&data, projectID); err == nil {
					teams = append(teams, project.TeamIDs...)
				}
			}
			if len(teams) > 0 {
				return teams
			}
		}
		return nil
	}
	if teams := resolve(targetID); teams != nil {
		return teams
	}
	for resource, comments := range data.Comments {
		if slices.ContainsFunc(comments, func(item domain.Comment) bool { return item.ID == targetID }) {
			return resolve(resource)
		}
	}
	for _, project := range data.Projects {
		if slices.ContainsFunc(project.Comments, func(item domain.Comment) bool { return item.ID == targetID }) {
			return resolve(project.ID)
		}
	}
	return nil
}

// mcpCommentMentionDocument builds the editor document for a markdown comment,
// with app mentions the author may start. It returns the document and the
// resolver whose appPolicy holds the report.
func (s *server) mcpCommentMentionDocument(ctx context.Context, actor mcpActor, data *domain.Bootstrap, targetID, body string) (map[string]any, *mentionAppPolicy) {
	resolver := s.mcpMentionResolver(ctx, actor, data, body)
	resolver.enableAppMentions(actor.User, data.ViewerRole, data.WorkspaceSettings.PreventGuestAgents, mentionTeamIDs(*data, targetID))
	document := resolver.companionDocument(body)
	if document == nil && !mentionMarkdownRepresentable(body) {
		// Tables, images, nested lists and HTML keep the markdown authoritative, so no node is stored.
		resolver.markdownDocument(body)
		if len(resolver.appPolicy.mentionedApps) > 0 {
			resolver.appPolicy.note("The body has tables, images, nested lists or HTML, so its @app mentions stayed plain text and started nothing. Move the mention into a plain paragraph.")
			resolver.appPolicy.mentionedApps = nil
		}
	}
	return document, resolver.appPolicy
}

// mentionReport adds what the app mentions did to a saved comment. A comment
// without app mentions or notes is returned unchanged.
func (s *server) mentionReport(ctx context.Context, data domain.Bootstrap, comment domain.Comment, policy *mentionAppPolicy) any {
	if policy == nil || len(policy.notes) == 0 && len(policy.mentionedApps) == 0 {
		return comment
	}
	result, err := structToMap(comment)
	if err != nil {
		return comment
	}
	started := []map[string]any{}
	notes := slices.Clone(policy.notes)
	for _, app := range policy.mentionedApps {
		entry := map[string]any{"app": app.DisplayName, "appUserId": app.ID, "started": false}
		tasks, err := s.store.ListAgentTasks(ctx, data.Workspace.URLKey, "", app.ID)
		if err == nil {
			for _, task := range tasks {
				if task.CommentID == comment.ID {
					entry["started"], entry["sessionId"] = true, task.ID
					break
				}
			}
		}
		if entry["started"] != true {
			notes = append(notes, fmt.Sprintf("@%s was stored as a mention but no session was started (the app has no access to this resource's team, or workspace policy blocks it).", app.DisplayName))
		}
		started = append(started, entry)
	}
	if len(started) > 0 {
		result["agentMentions"] = started
	}
	if len(notes) > 0 {
		result["mentionNotes"] = notes
	}
	return result
}
