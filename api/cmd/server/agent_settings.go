package main

import (
	"fmt"
	"net/http"
	"strings"

	"flow/api/internal/domain"
)

func workspaceAgentSystemPrompt(data domain.Bootstrap, issues []domain.Issue, skills []domain.PersonalAgentSkill) string {
	var prompt strings.Builder
	prompt.WriteString(agentSystemPrompt(data.Workspace.Name, issues, skills))
	if guidance:=strings.TrimSpace(data.WorkspaceSettings.AgentInstructions);guidance!="" {fmt.Fprintf(&prompt,"\nWorkspace guidance:\n%s\n",truncateSettingsText(guidance,8000))}
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

func (s *server) updateWorkspaceAgentGuidance(w http.ResponseWriter,r *http.Request) {
	var input struct {Instructions string `json:"instructions"`}
	if !decodeJSON(w,r,&input){return}
	if len([]rune(input.Instructions))>8000 {writeError(w,400,"workspace guidance must not exceed 8000 characters");return}
	err:=s.store.MutateWorkspace(r.Context(),workspaceKey(r),"workspace.agent_guidance_updated","workspace",nil,func(data *domain.Bootstrap)error{data.WorkspaceSettings.AgentInstructions=strings.TrimSpace(input.Instructions);return nil})
	respondMutation(w,err,200,map[string]string{"instructions":strings.TrimSpace(input.Instructions)})
}

func truncateSettingsText(value string, limit int) string {
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return value
}
