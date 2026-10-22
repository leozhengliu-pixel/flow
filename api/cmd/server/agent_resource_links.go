package main

import (
	"net/http"
	"net/url"
	"regexp"
	"slices"
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
		"customer " + root + "/customer/{lowercase-hyphenated-name}-{last 12 characters of the customer id} (no spaces, e.g. acme-corp-123456789012)",
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

// agentLinkBaseURL is the origin links in tool results and answers use: the app URL a background run was given,
// else the request's own origin.
func agentLinkBaseURL(r *http.Request) string {
	// A background run sets the key even when no app URL is known; an empty base then keeps links relative (they
	// open in the app) instead of pointing at the run's synthetic host.
	if base, ok := r.Context().Value(agentBaseURLKey{}).(string); ok {
		return base
	}
	return externalBaseURL(r)
}

// agentAbsoluteURL matches an absolute http(s) URL in an answer (bare, in a markdown link target or in <…>).
var agentAbsoluteURL = regexp.MustCompile(`https?://[^\s<>()\[\]"'` + "`" + `]+`)

// agentLinkSections are the workspace path sections the answer guidance links to.
var agentLinkSections = map[string]bool{
	"issue": true, "project": true, "initiative": true, "document": true, "team": true, "issue-label": true,
	"project-label": true, "initiative-label": true, "customer": true, "pipeline": true, "view": true, "review": true,
	"loop": true, "loops": true,
}

// rebaseAgentWorkspaceLinks points links to this workspace's pages at the app's own origin. Models sometimes put a
// made-up host in front of a Flow path: "https://app.flow.dev/acme/issue/ENG-1", the workspace as the host
// ("https://acme/issue/ENG-1") or a subdomain ("https://acme.flow.dev/issue/ENG-1"), or a Flow-looking host without
// the workspace ("https://flow.dev/team/ENG/cycle/1"). Such a link leads nowhere and the web app cannot show it as a
// chip. Links to other paths, other workspaces and real external sites are left alone.
func rebaseAgentWorkspaceLinks(text, urlKey, baseURL string) string {
	urlKey = strings.TrimSpace(urlKey)
	if urlKey == "" || !strings.Contains(text, "://") {
		return text
	}
	base, err := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/"))
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") {
		return text
	}
	root := "/" + url.PathEscape(urlKey)
	prefixes := []string{"/" + urlKey + "/", root + "/"}
	section := func(path string) bool {
		name, _, _ := strings.Cut(strings.TrimPrefix(path, "/"), "/")
		return agentLinkSections[name]
	}
	return agentAbsoluteURL.ReplaceAllStringFunc(text, func(raw string) string {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Host == "" {
			return raw
		}
		if strings.EqualFold(parsed.Host, base.Host) && parsed.Scheme == base.Scheme {
			return raw
		}
		rest := raw[len(parsed.Scheme)+len("://"):]
		path := rest[min(len(rest), strings.IndexAny(rest+"/", "/")):]
		for _, prefix := range prefixes {
			if strings.HasPrefix(path, prefix) {
				if !section(strings.TrimPrefix(path, prefix[:len(prefix)-1])) {
					return raw
				}
				return base.Scheme + "://" + base.Host + path
			}
		}
		host := strings.ToLower(parsed.Hostname())
		key := strings.ToLower(urlKey)
		// The workspace as the host or a subdomain, or a host with a "flow" label (flow.dev, app.flow.app…).
		flowHost := host == key || strings.HasPrefix(host, key+".") || slices.Contains(strings.Split(host, "."), "flow")
		if flowHost && !strings.EqualFold(parsed.Host, base.Host) && section(path) {
			return base.Scheme + "://" + base.Host + root + path
		}
		return raw
	})
}
