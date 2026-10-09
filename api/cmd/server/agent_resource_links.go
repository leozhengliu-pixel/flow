package main

import (
	"net/url"
	"strings"
)

// agentResourceLinkGuidance tells the model how to reference every kind of workspace resource in an answer. The web
// app renders each Flow link (and @Name) as an inline chip with a hover card, so the paths mirror the app's routes.
func agentResourceLinkGuidance(urlKey string) string {
	root := "/" + url.PathEscape(strings.TrimSpace(urlKey))
	paths := []string{
		"issue " + root + "/issue/ENG-12",
		"project " + root + "/project/{slugId}/overview",
		"initiative " + root + "/initiative/{slugId}/overview",
		"document " + root + "/document/{slugId}",
		"team " + root + "/team/{key}/overview",
		"cycle " + root + "/team/{teamKey}/cycle/{number}",
		"issue label " + root + "/issue-label/{name} (project-label and initiative-label for the other label types)",
		"project milestone " + root + "/project/{slugId}/overview#milestone-{milestoneId}",
		"customer " + root + "/customer/{name}-{last 12 characters of the customer id}",
		"release " + root + "/pipeline/{pipelineSlugId}/release/{releaseSlugId}/issues",
		"saved view " + root + "/view/{viewSlugId}",
		"code review " + root + "/review/{slugId}",
		"project update " + root + "/project/{slugId}/overview#update-{updateId}",
		"initiative update " + root + "/initiative/{slugId}/overview#update-{updateId}",
	}
	var guidance strings.Builder
	guidance.WriteString("\nResource links:\n")
	guidance.WriteString("- Mention every workspace resource you name (issues, projects, initiatives, documents, teams, cycles, labels, milestones, customers, releases, saved views, code reviews, updates) as a markdown link to its Flow path so the app shows it as a chip with a hover card; the link text is the resource's name. Use only the ids and slugs the tools return and never invent one. Paths: ")
	guidance.WriteString(strings.Join(paths, "; "))
	guidance.WriteString(".\n")
	guidance.WriteString("- Write people as @Display Name without a link. In tables and lists link each resource in its cell or item. Never put links or identifiers inside code spans or code blocks.\n")
	return guidance.String()
}
