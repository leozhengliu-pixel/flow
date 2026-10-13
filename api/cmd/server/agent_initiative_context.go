package main

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// Initiatives in a chat's context (the initiative page it was opened on, or @-mentions) are loaded into the
// instructions like mentioned projects: the initiative itself, its projects and its latest updates, bounded so a
// large initiative cannot crowd out the conversation.
const (
	agentInitiativeContextLimit   = 5
	agentInitiativeProjectLimit   = 15
	agentInitiativeChildLimit     = 10
	agentInitiativeUpdateLimit    = 3
	agentInitiativeSummaryRunes   = 500
	agentInitiativeDescRunes      = 2000
	agentInitiativeUpdateRunes    = 600
	agentInitiativeProjectSummary = 160
)

// selectedAgentInitiatives returns the initiatives with the given ids, in workspace order. The workspace data is
// already narrowed to what the viewer can see.
func selectedAgentInitiatives(initiatives []domain.Initiative, ids []string) []domain.Initiative {
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	selected := []domain.Initiative{}
	for _, initiative := range initiatives {
		if wanted[initiative.ID] {
			selected = append(selected, initiative)
		}
	}
	return selected
}

// agentInitiativeTeamIDs lists the lead and contributing teams of initiatives in context, whose guidance applies.
func agentInitiativeTeamIDs(initiatives []domain.Initiative) []string {
	ids := []string{}
	for _, initiative := range initiatives {
		if initiative.LeadTeamID != "" {
			ids = append(ids, initiative.LeadTeamID)
		}
		ids = append(ids, initiative.ContributingTeamIDs...)
	}
	return ids
}

// agentInitiativePrompt describes the initiatives in context for the model.
func agentInitiativePrompt(data domain.Bootstrap, initiatives []domain.Initiative) string {
	if len(initiatives) == 0 {
		return ""
	}
	var prompt strings.Builder
	prompt.WriteString("\nInitiatives in context:\n")
	for index, initiative := range initiatives {
		if index == agentInitiativeContextLimit {
			fmt.Fprintf(&prompt, "- …and %d more initiatives (look them up with get_initiative).\n", len(initiatives)-index)
			break
		}
		writeAgentInitiative(&prompt, data, initiative)
	}
	return prompt.String()
}

func writeAgentInitiative(prompt *strings.Builder, data domain.Bootstrap, initiative domain.Initiative) {
	fmt.Fprintf(prompt, "- Initiative %q (id %s", initiative.Name, initiative.ID)
	if initiative.Status != "" {
		fmt.Fprintf(prompt, ", status %s", initiative.Status)
	}
	if initiative.Health != "" {
		fmt.Fprintf(prompt, ", health %s", initiative.Health)
	}
	if initiative.Owner != nil {
		fmt.Fprintf(prompt, ", owner %s", initiative.Owner.DisplayName)
	}
	if initiative.TargetDate != nil && *initiative.TargetDate != "" {
		fmt.Fprintf(prompt, ", target %s", *initiative.TargetDate)
	}
	if initiative.LeadTeamID != "" {
		if team := agentTeamName(data, initiative.LeadTeamID); team != "" {
			fmt.Fprintf(prompt, ", lead team %s", team)
		}
	}
	prompt.WriteString(")\n")
	if summary := strings.TrimSpace(initiative.Summary); summary != "" {
		fmt.Fprintf(prompt, "  Summary: %s\n", truncateSettingsText(summary, agentInitiativeSummaryRunes))
	}
	if description := strings.TrimSpace(initiative.Description); description != "" {
		fmt.Fprintf(prompt, "  Description: %s\n", truncateSettingsText(description, agentInitiativeDescRunes))
	}
	projects := agentInitiativeProjects(data, initiative)
	if len(projects) > 0 {
		fmt.Fprintf(prompt, "  Projects (%d):\n", len(projects))
		for index, project := range projects {
			if index == agentInitiativeProjectLimit {
				fmt.Fprintf(prompt, "    - …and %d more\n", len(projects)-index)
				break
			}
			fmt.Fprintf(prompt, "    - %q (id %s, status %s, %d%% complete", project.Name, project.ID, project.Status.Name, agentProgressPercent(project.Progress))
			if project.Health != "" {
				fmt.Fprintf(prompt, ", health %s", project.Health)
			}
			if project.Lead != nil {
				fmt.Fprintf(prompt, ", lead %s", project.Lead.DisplayName)
			}
			if project.TargetDate != nil && *project.TargetDate != "" {
				fmt.Fprintf(prompt, ", target %s", *project.TargetDate)
			}
			prompt.WriteString(")")
			if summary := strings.TrimSpace(project.Summary); summary != "" {
				fmt.Fprintf(prompt, ": %s", truncateSettingsText(summary, agentInitiativeProjectSummary))
			}
			prompt.WriteString("\n")
		}
	}
	children := []domain.Initiative{}
	for _, item := range data.Initiatives {
		if item.ID != initiative.ID && slices.Contains(item.ParentInitiativeIDs, initiative.ID) {
			children = append(children, item)
		}
	}
	if len(children) > 0 {
		names := []string{}
		for index, child := range children {
			if index == agentInitiativeChildLimit {
				names = append(names, fmt.Sprintf("…and %d more", len(children)-index))
				break
			}
			names = append(names, fmt.Sprintf("%q (id %s, status %s)", child.Name, child.ID, firstNonEmpty(child.Status, "unknown")))
		}
		fmt.Fprintf(prompt, "  Sub-initiatives: %s\n", strings.Join(names, ", "))
	}
	updates := slices.Clone(data.InitiativeUpdates[initiative.ID])
	if len(updates) > 0 {
		slices.SortStableFunc(updates, func(a, b domain.InitiativeUpdate) int { return b.CreatedAt.Compare(a.CreatedAt) })
		prompt.WriteString("  Latest updates (newest first):\n")
		for index, update := range updates {
			if index == agentInitiativeUpdateLimit {
				break
			}
			fmt.Fprintf(prompt, "    - %s", update.CreatedAt.UTC().Format(time.DateOnly))
			if name := strings.TrimSpace(update.User.DisplayName); name != "" {
				fmt.Fprintf(prompt, " by %s", name)
			}
			if update.Health != "" {
				fmt.Fprintf(prompt, " (health %s)", update.Health)
			}
			if body := strings.Join(strings.Fields(update.Body), " "); body != "" {
				fmt.Fprintf(prompt, ": %s", truncateSettingsText(body, agentInitiativeUpdateRunes))
			}
			prompt.WriteString("\n")
		}
	}
}

// agentInitiativeProjects lists the initiative's visible, unarchived projects: those it lists and those that name it.
func agentInitiativeProjects(data domain.Bootstrap, initiative domain.Initiative) []domain.Project {
	projects := []domain.Project{}
	for _, project := range data.Projects {
		if project.ArchivedAt == nil && (slices.Contains(initiative.ProjectIDs, project.ID) || slices.Contains(project.Initiatives, initiative.ID)) {
			projects = append(projects, project)
		}
	}
	return projects
}

func agentTeamName(data domain.Bootstrap, id string) string {
	for _, team := range data.Teams {
		if team.ID == id {
			return team.Name
		}
	}
	return ""
}

// agentProgressPercent reads a project's progress fraction (0–1) as a whole percentage.
func agentProgressPercent(progress float64) int {
	if math.IsNaN(progress) || progress <= 0 {
		return 0
	}
	if progress >= 1 {
		return 100
	}
	return int(math.Round(progress * 100))
}

// agentInitiativeTitleContext names the initiatives in context for the chat title, without their details.
func agentInitiativeTitleContext(initiatives []domain.Initiative) string {
	if len(initiatives) == 0 {
		return ""
	}
	names := []string{}
	for _, initiative := range initiatives {
		names = append(names, fmt.Sprintf("%q", initiative.Name))
	}
	return "\nInitiatives in context: " + strings.Join(names, ", ") + "\n"
}
