package main

import (
	"fmt"
	"strings"

	"flow/api/internal/domain"
)

func workspaceAgentSystemPrompt(data domain.Bootstrap, issues []domain.Issue, skills []domain.PersonalAgentSkill) string {
	var prompt strings.Builder
	prompt.WriteString(agentSystemPrompt(data.Workspace.Name, issues, skills))
	if guidance := strings.TrimSpace(data.UserSettings[data.Viewer.ID].AgentInstructions); guidance != "" {
		fmt.Fprintf(&prompt, "\nPersonal guidance:\n%s\n", truncateSettingsText(guidance, 4000))
	}
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
	return prompt.String()
}

func truncateSettingsText(value string, limit int) string {
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return value
}
