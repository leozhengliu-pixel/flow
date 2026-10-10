package main

import (
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Before the first model call, a chat request is scanned for the resources it names in plain text: issue
// identifiers (DEV-16), Flow links to issues, projects, and documents, and project names. They are loaded into the
// instructions as compact summaries, so "introduce DEV-16" is answered in one call instead of a lookup round trip.

const (
	agentReferencedIssueLimit       = 10
	agentReferencedResourceLimit    = 5
	agentReferencedDescriptionRunes = 1500
)

var (
	agentIssueKeyPattern     = regexp.MustCompile(`[A-Za-z][A-Za-z0-9]{0,9}-[0-9]{1,7}`)
	agentProjectLinkPattern  = regexp.MustCompile(`/project/([A-Za-z0-9%._~-]+)`)
	agentDocumentLinkPattern = regexp.MustCompile(`/document/([A-Za-z0-9%._~-]+)`)
)

type agentMessageReferences struct {
	IssueIdentifiers []string
	Projects         []domain.Project
	Documents        []domain.Document
}

// findAgentMessageReferences lists the resources a message names. Issue identifiers only count when their prefix is
// a team key of the workspace, so "UTF-8" or "GPT-5" are not mistaken for issues.
func findAgentMessageReferences(data domain.Bootstrap, message string) agentMessageReferences {
	var refs agentMessageReferences
	keys := map[string]bool{}
	for _, team := range data.Teams {
		if team.Key != "" {
			keys[strings.ToUpper(team.Key)] = true
		}
	}
	for _, match := range agentIssueKeyPattern.FindAllStringIndex(message, -1) {
		if !agentIdentifierBoundary(message, match[0], match[1]) {
			continue
		}
		identifier := strings.ToUpper(message[match[0]:match[1]])
		if keys[identifier[:strings.IndexByte(identifier, '-')]] && !slices.Contains(refs.IssueIdentifiers, identifier) && len(refs.IssueIdentifiers) < agentReferencedIssueLimit {
			refs.IssueIdentifiers = append(refs.IssueIdentifiers, identifier)
		}
	}
	addProject := func(project domain.Project) {
		if len(refs.Projects) < agentReferencedResourceLimit && !slices.ContainsFunc(refs.Projects, func(item domain.Project) bool { return item.ID == project.ID }) {
			refs.Projects = append(refs.Projects, project)
		}
	}
	for _, match := range agentProjectLinkPattern.FindAllStringSubmatch(message, -1) {
		slug, _ := url.PathUnescape(match[1])
		for _, project := range data.Projects {
			if project.SlugID == slug || project.ID == slug {
				addProject(project)
			}
		}
	}
	lower := strings.ToLower(message)
	for _, project := range data.Projects {
		// Short names ("API", "Web") appear in ordinary sentences; only distinctive names count.
		if name := strings.ToLower(strings.TrimSpace(project.Name)); utf8.RuneCountInString(name) >= 4 && strings.Contains(lower, name) {
			addProject(project)
		}
	}
	for _, match := range agentDocumentLinkPattern.FindAllStringSubmatch(message, -1) {
		slug, _ := url.PathUnescape(match[1])
		for _, document := range data.Documents {
			if (document.SlugID == slug || document.ID == slug) && len(refs.Documents) < agentReferencedResourceLimit && !slices.ContainsFunc(refs.Documents, func(item domain.Document) bool { return item.ID == document.ID }) {
				refs.Documents = append(refs.Documents, document)
			}
		}
	}
	return refs
}

// agentIdentifierBoundary rejects matches inside longer tokens ("XDEV-16", "DEV-16a", "v1-2-3").
func agentIdentifierBoundary(text string, start, end int) bool {
	if start > 0 {
		if previous := text[start-1]; isASCIIWordByte(previous) || previous == '-' || previous == '.' {
			return false
		}
	}
	if end < len(text) {
		if next := text[end]; isASCIIWordByte(next) || next == '-' {
			return false
		}
	}
	return true
}

func isASCIIWordByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// agentReferencedIssues loads the issues named by identifier that the viewer can see and that are not loaded
// already. Unknown or hidden identifiers are skipped silently; the model can still look them up.
func (s *server) agentReferencedIssues(r *http.Request, identifiers []string, loaded []domain.Issue) []domain.Issue {
	identifiers = slices.DeleteFunc(slices.Clone(identifiers), func(identifier string) bool {
		return slices.ContainsFunc(loaded, func(issue domain.Issue) bool { return strings.EqualFold(issue.Identifier, identifier) })
	})
	if len(identifiers) == 0 || s.store == nil {
		return nil
	}
	data, q, err := s.pagedRealtimeMetadata(r)
	if err != nil {
		return nil
	}
	q.Filter = store.IssueFilter{Field: "identifier", Values: identifiers}
	q.Archived = "all"
	q.Limit = agentReferencedIssueLimit
	q.Text = ""
	q.Cursor = ""
	page, err := s.store.QueryIssueRecords(r.Context(), q)
	if err != nil || len(page.Items) == 0 {
		return nil
	}
	issues, err := s.projectIssueRecordReferences(r, data, q, page.Items)
	if err != nil {
		return nil
	}
	for index := range issues {
		issues[index].Description = truncateSettingsText(strings.TrimSpace(issues[index].Description), agentReferencedDescriptionRunes)
	}
	return issues
}

// agentLatestUserMessage is the request the reply answers.
func agentLatestUserMessage(session domain.AgentSession) string {
	for index := len(session.Messages) - 1; index >= 0; index-- {
		if session.Messages[index].Role == "user" {
			return session.Messages[index].Content
		}
	}
	return ""
}
