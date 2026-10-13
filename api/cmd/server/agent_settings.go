package main

import (
	"fmt"
	"net/http"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

// workspaceAgentSystemPrompt is the built-in agent's prompt for comment threads and delegated work. teamIDs adds
// the guidance of teams in context besides the issues' teams (the team of a project or document session).
func workspaceAgentSystemPrompt(data domain.Bootstrap, issues []domain.Issue, skills []domain.PersonalAgentSkill, teamIDs ...string) string {
	return buildWorkspaceAgentPrompt(data, issues, skills, agentProgressRule, "", "", teamIDs...)
}

const (
	workspaceAgentGuidanceLimit = 8000
	teamAgentGuidanceLimit      = 8000
	// agentGuidanceTeamLimit caps how many teams' guidance one prompt carries (each with its parent teams).
	agentGuidanceTeamLimit = 8
)

// agentGuidanceRule is one level of agent guidance: the workspace's, or a team's (Team settings › Agents).
type agentGuidanceRule struct {
	Origin   string // "workspace" or "team"
	TeamID   string
	TeamKey  string
	TeamName string
	Body     string
}

// teamAgentGuidanceChain resolves a team's guidance: parent teams first (the root ancestor first), then
// the team itself, so later rules are more specific and the team's own guidance wins on conflict. Empty levels are
// skipped.
func teamAgentGuidanceChain(data domain.Bootstrap, teamID string) []agentGuidanceRule {
	chain := []string{}
	seen := map[string]bool{}
	for id := teamID; id != "" && !seen[id] && len(chain) < 16; id = data.TeamSettings[id].ParentTeamID {
		seen[id] = true
		chain = append(chain, id)
	}
	rules := []agentGuidanceRule{}
	for index := len(chain) - 1; index >= 0; index-- {
		id := chain[index]
		body := strings.TrimSpace(data.TeamSettings[id].AgentGuidance)
		if body == "" {
			continue
		}
		rule := agentGuidanceRule{Origin: "team", TeamID: id, Body: truncateSettingsText(body, teamAgentGuidanceLimit)}
		if team := slices.IndexFunc(data.Teams, func(team domain.Team) bool { return team.ID == id }); team >= 0 {
			rule.TeamKey, rule.TeamName = data.Teams[team].Key, data.Teams[team].Name
		}
		rules = append(rules, rule)
	}
	return rules
}

// teamsAgentGuidance is the guidance of the given teams (each with its parent teams), each team's rules once.
func teamsAgentGuidance(data domain.Bootstrap, teamIDs []string) []agentGuidanceRule {
	rules := []agentGuidanceRule{}
	done := map[string]bool{}
	teams := 0
	for _, teamID := range teamIDs {
		if teamID == "" || done["team:"+teamID] || teams >= agentGuidanceTeamLimit {
			continue
		}
		done["team:"+teamID] = true
		teams++
		for _, rule := range teamAgentGuidanceChain(data, teamID) {
			if !done[rule.TeamID] {
				done[rule.TeamID] = true
				rules = append(rules, rule)
			}
		}
	}
	return rules
}

// agentGuidanceChain is all the guidance for work in the given teams: the workspace's first, then each team's chain.
func agentGuidanceChain(data domain.Bootstrap, teamIDs ...string) []agentGuidanceRule {
	rules := []agentGuidanceRule{}
	if body := strings.TrimSpace(data.WorkspaceSettings.AgentInstructions); body != "" {
		rules = append(rules, agentGuidanceRule{Origin: "workspace", Body: truncateSettingsText(body, workspaceAgentGuidanceLimit)})
	}
	return append(rules, teamsAgentGuidance(data, teamIDs)...)
}

// agentProjectTeamIDs lists the teams of projects in context, whose guidance applies to them.
func agentProjectTeamIDs(projects []domain.Project) []string {
	ids := []string{}
	for _, project := range projects {
		ids = append(ids, project.TeamIDs...)
	}
	return ids
}

// agentTeamGuidancePrompt writes the team guidance of the teams in context for the system prompt.
func agentTeamGuidancePrompt(rules []agentGuidanceRule) string {
	if len(rules) == 0 {
		return ""
	}
	var prompt strings.Builder
	prompt.WriteString("\nTeam guidance (set in team settings; it applies to that team's work and wins over workspace guidance, and a team's own guidance wins over its parent team's, when they conflict):\n")
	for _, rule := range rules {
		fmt.Fprintf(&prompt, "Team %s guidance:\n%s\n", firstNonEmpty(rule.TeamName, rule.TeamID), rule.Body)
	}
	return prompt.String()
}

// buildWorkspaceAgentPrompt puts what stays the same between requests first (rules, link paths, extraStatic,
// workspace and personal guidance) so providers can reuse their prompt cache, and the per-request context last
// (today, loaded issues, skills, extraDynamic).
// teamIDs adds teams in context besides the issues' teams (for example the teams of mentioned projects); their
// guidance (with parent teams) follows the context.
func buildWorkspaceAgentPrompt(data domain.Bootstrap, issues []domain.Issue, skills []domain.PersonalAgentSkill, toolRule, extraStatic, extraDynamic string, teamIDs ...string) string {
	var prompt strings.Builder
	prompt.WriteString(agentPromptRules(toolRule))
	prompt.WriteString(agentResourceLinkGuidance(data.Workspace.URLKey))
	prompt.WriteString(extraStatic)
	if guidance := strings.TrimSpace(data.WorkspaceSettings.AgentInstructions); guidance != "" {
		fmt.Fprintf(&prompt, "\nWorkspace guidance:\n%s\n", truncateSettingsText(guidance, workspaceAgentGuidanceLimit))
	}
	if guidance := strings.TrimSpace(data.UserSettings[data.Viewer.ID].AgentInstructions); guidance != "" {
		fmt.Fprintf(&prompt, "\nPersonal guidance:\n%s\n", truncateSettingsText(guidance, 4000))
	}
	prompt.WriteString(agentPromptContext(data.Workspace.Name, issues, skills))
	contextTeams := make([]string, 0, len(issues)+len(teamIDs))
	for _, issue := range issues {
		contextTeams = append(contextTeams, issue.Team.ID)
	}
	prompt.WriteString(agentTeamGuidancePrompt(teamsAgentGuidance(data, append(contextTeams, teamIDs...))))
	teams := map[string]bool{}
	for _, issue := range issues {
		if teams[issue.Team.ID] {
			continue
		}
		teams[issue.Team.ID] = true
		settings := data.TeamSettings[issue.Team.ID]
		for _, skill := range settings.AgentSkills {
			if skill.Enabled {
				fmt.Fprintf(&prompt, "\nTeam %s skill %s:\n%s\n", issue.Team.Name, skill.Name, truncateSettingsText(skill.Instructions, 4000))
			}
		}
	}
	prompt.WriteString(extraDynamic)
	return prompt.String()
}

func (s *server) updateWorkspaceAgentGuidance(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Instructions string `json:"instructions"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if len([]rune(input.Instructions)) > workspaceAgentGuidanceLimit {
		writeError(w, 400, "workspace guidance must not exceed 8000 characters")
		return
	}
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "workspace.agent_guidance_updated", "workspace", nil, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.AgentInstructions = strings.TrimSpace(input.Instructions)
		return nil
	})
	respondMutation(w, err, 200, map[string]string{"instructions": strings.TrimSpace(input.Instructions)})
}

func truncateSettingsText(value string, limit int) string {
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return value
}
