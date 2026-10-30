package main

import (
	"net/url"
	"regexp"
	"strings"
)

// Models copy references out of the conversation the way the user saw them: an issue chip becomes
// " DEV-42 QA agent – Export CSV fails…", a link becomes "[DEV-42](/dev/issue/DEV-42)", an app URL is pasted
// whole. Tools match ids, identifiers and slugs exactly, so such values failed with "issue not found" and the
// agent silently skipped the work. normalizeAgentToolArgs cleans id-like arguments before a tool runs; free-text
// fields (title, description, body, …) are never touched.

var (
	// An issue identifier at the start of a value, followed by the end or by a separator other than a hyphen
	// (so slugs such as "q3-2026-plan" and uuids are never cut).
	agentLeadingIssueKey = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9]{0,9}-[0-9]{1,7})(?:$|[\s:：,，;；.。(（\[【"“'‘–—|/])`)
	// A whole markdown link: [label](target).
	agentMarkdownLink = regexp.MustCompile(`^\[((?:[^\]\\]|\\.)*)\]\(([^)\s]*)\)`)
	// An app path naming a resource: /<workspace>/<kind>/<key>… or flow://<kind>/<key>.
	agentResourcePath = regexp.MustCompile(`(?:^|/)(issue|project|document|initiative|view|team|cycle|customer|milestone|release|loop|update)s?/([^/?#\s]+)`)
)

// agentIssueReferenceKeys hold issue references by convention (identifiers, ids or chip text).
var agentIssueReferenceKeys = map[string]bool{
	"issue": true, "parent": true, "duplicateOf": true, "blockedBy": true, "blocks": true, "relatedTo": true,
	"removeBlockedBy": true, "removeBlocks": true, "removeRelatedTo": true,
}

// normalizeAgentToolArgs cleans the id-like arguments of a tool call in place.
func normalizeAgentToolArgs(tool string, args map[string]any) {
	tool = strings.TrimPrefix(tool, "mcp__flow.")
	issueTool := strings.Contains(tool, "issue")
	for key, value := range args {
		if strings.HasPrefix(key, "__") {
			continue
		}
		mode := agentArgumentMode(key, issueTool)
		if mode == agentArgumentText {
			continue
		}
		switch typed := value.(type) {
		case string:
			args[key] = normalizeAgentReference(typed, mode == agentArgumentIssue)
		case []any:
			for index, item := range typed {
				if text, ok := item.(string); ok {
					typed[index] = normalizeAgentReference(text, mode == agentArgumentIssue)
				}
			}
		}
	}
}

type agentArgumentKind int

const (
	agentArgumentText agentArgumentKind = iota
	// agentArgumentReference: an id, slug or name; links and chip markup are reduced to their key.
	agentArgumentReference
	// agentArgumentIssue: also "DEV-42 Some title" → "DEV-42".
	agentArgumentIssue
)

func agentArgumentMode(key string, issueTool bool) agentArgumentKind {
	switch {
	case agentIssueReferenceKeys[key]:
		return agentArgumentIssue
	case key == "id":
		if issueTool {
			return agentArgumentIssue
		}
		return agentArgumentReference
	case key == "issueId" || key == "issueIds" || key == "parentId" || key == "parentIds" || key == "commentId":
		return agentArgumentIssue
	case strings.Contains(key, "IssueId") || strings.Contains(key, "IssueIds"):
		return agentArgumentIssue
	case key == "team" || key == "teams" || key == "teamId" || key == "teamIds" ||
		key == "setTeams" || key == "addTeams" || key == "removeTeams" ||
		key == "project" || key == "projects" || key == "projectId" || key == "projectIds" ||
		key == "setProjects" || key == "addProjects" || key == "removeProjects" ||
		key == "initiative" || key == "initiatives" || key == "initiativeId" || key == "initiativeIds" ||
		key == "setInitiatives" || key == "addInitiatives" || key == "removeInitiatives" || key == "parentInitiatives" ||
		key == "document" || key == "documents" || key == "documentId" || key == "documentIds" ||
		key == "cycle" || key == "cycleId" || key == "milestone" || key == "milestoneId" ||
		key == "release" || key == "releaseId" || key == "releaseIds" || key == "setReleases" || key == "addReleases" || key == "removeReleases" || key == "view" || key == "viewId" ||
		key == "user" || key == "userId" || key == "assignee" || key == "assigneeId" ||
		key == "delegate" || key == "delegateId" || key == "lead" || key == "leadId" || key == "owner" || key == "ownerId" ||
		key == "creator" || key == "creatorId" || key == "template" || key == "templateId" || key == "skill" || key == "skillId" ||
		key == "parentTeam" || key == "parentTeamId" || key == "sourceTeam" || key == "sourceTeamId" || key == "leadTeam":
		return agentArgumentReference
	case strings.HasSuffix(key, "Ids"):
		// Unknown plural id fields are still references, but never issue chip text.
		return agentArgumentReference
	case strings.HasSuffix(key, "Id"):
		return agentArgumentReference
	}
	return agentArgumentText
}

// normalizeAgentReference reduces chip text, markdown links and app URLs to the reference they name.
func normalizeAgentReference(value string, issue bool) string {
	cleaned := strings.TrimSpace(value)
	cleaned = strings.Trim(cleaned, "`\"'“”‘’")
	cleaned = strings.TrimSpace(strings.TrimPrefix(cleaned, "@"))
	if match := agentMarkdownLink.FindStringSubmatch(cleaned); match != nil {
		if key := agentReferenceFromURL(match[2]); key != "" {
			return key
		}
		label := strings.NewReplacer(`\[`, "[", `\]`, "]", `\\`, `\`).Replace(match[1])
		cleaned = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(label), "@"))
	} else if strings.Contains(cleaned, "://") || strings.HasPrefix(cleaned, "/") {
		if !strings.ContainsAny(cleaned, " \t\n") {
			if key := agentReferenceFromURL(cleaned); key != "" {
				return key
			}
		}
	}
	if issue && strings.ContainsAny(cleaned, " \t\n:：,，(（\"“–—") {
		if match := agentLeadingIssueKey.FindStringSubmatch(cleaned); match != nil {
			return strings.ToUpper(match[1])
		}
	}
	if cleaned == "" {
		return value
	}
	return cleaned
}

// agentReferenceFromURL is the resource key an app URL or path names ("…/dev/issue/DEV-42/title" → "DEV-42").
func agentReferenceFromURL(raw string) string {
	path := raw
	if parsed, err := url.Parse(raw); err == nil {
		switch {
		case parsed.Scheme == "flow":
			path = parsed.Host + parsed.Path
		case parsed.Scheme == "http" || parsed.Scheme == "https" || parsed.Scheme == "":
			path = parsed.Path
		default:
			return ""
		}
	}
	match := agentResourcePath.FindStringSubmatch(path)
	if match == nil {
		return ""
	}
	key, err := url.PathUnescape(match[2])
	if err != nil {
		return ""
	}
	return key
}

// agentIssueReferenceKey is the issue identifier a lookup string starts with when it carries more than the
// identifier ("DEV-42 QA agent – …", "[DEV-42](…)"), or "" when there is nothing to recover.
func agentIssueReferenceKey(query string) string {
	key := normalizeAgentReference(query, true)
	if key == query {
		return ""
	}
	return key
}
