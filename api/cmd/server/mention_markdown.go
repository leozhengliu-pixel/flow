package main

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Markdown that agents, MCP clients and API callers write references
// workspace resources as links, bare Flow URLs, issue identifiers and @names.
// The web editor stores those references as `mention` nodes
// ({"type":"mention","attrs":{"mentionType","id","label","title","href"}});
// this file builds the same nodes server side so every rich-text surface
// stores references exactly like the editor does. It mirrors the web's
// convertMentionLinks (anyText + text) in
// web/src/components/editor/mentions/mention-model.ts, with the route and
// label rules of web/src/components/agent/agent-entity-refs.ts.

// mentionAttrs are a mention node's attributes.
type mentionAttrs struct {
	Kind, ID, Label, Title, Href string
}

func (attrs mentionAttrs) node() map[string]any {
	return map[string]any{"type": "mention", "attrs": map[string]any{"mentionType": attrs.Kind, "id": attrs.ID, "label": attrs.Label, "title": attrs.Title, "href": attrs.Href}}
}

// mentionResolver resolves references against workspace data. Issues are not
// part of paged workspace data: issues holds the ones the requester may access
// (see mentionReferencedIssues), and only those become issue mentions.
type mentionResolver struct {
	data      *domain.Bootstrap
	issues    []domain.Issue
	teamKeys  map[string]bool
	usernames map[string]string
	people    []mentionPerson
}

type mentionPerson struct {
	name string // lower-cased display name, name or username
	user domain.User
}

func newMentionResolver(data *domain.Bootstrap, issues []domain.Issue) *mentionResolver {
	resolver := &mentionResolver{data: data, issues: issues, teamKeys: map[string]bool{}, usernames: map[string]string{}}
	for _, team := range data.Teams {
		if key := strings.ToUpper(strings.TrimSpace(team.Key)); key != "" {
			resolver.teamKeys[key] = true
		}
	}
	derived := map[string]string(nil)
	for _, user := range data.Users {
		if user.Username == "" && derived == nil {
			derived = workspaceUsernames(bootstrapUsers(data), data.UserSettings)
		}
		resolver.usernames[user.ID] = firstNonEmpty(user.Username, derived[user.ID])
	}
	suspended := map[string]bool{}
	for _, member := range data.Members {
		if member.Status != "" && member.Status != "active" {
			suspended[member.User.ID] = true
		}
	}
	seen := map[string]bool{}
	for _, user := range data.Users {
		if user.App || user.BuiltinAgent || suspended[user.ID] {
			continue
		}
		for _, name := range []string{user.DisplayName, user.Name, resolver.usernames[user.ID]} {
			key := strings.ToLower(strings.TrimSpace(name))
			if utf8.RuneCountInString(key) > 1 && !seen[key] {
				seen[key] = true
				resolver.people = append(resolver.people, mentionPerson{name: key, user: user})
			}
		}
	}
	slices.SortStableFunc(resolver.people, func(left, right mentionPerson) int { return utf16Length(right.name) - utf16Length(left.name) })
	return resolver
}

// mentionMarkdownDocument converts markdown into the editor document with
// every reference it can resolve as a mention node.
func mentionMarkdownDocument(data *domain.Bootstrap, issues []domain.Issue, markdown string) map[string]any {
	return newMentionResolver(data, issues).markdownDocument(markdown)
}

func (m *mentionResolver) markdownDocument(markdown string) map[string]any {
	return linkBareURLs(m.convert(mcpMarkdownDocument(markdown)))
}

var mentionBareURL = regexp.MustCompile(`https?://[^\s<>()\[\]]+`)

// linkBareURLs gives the bare http(s) URLs left in a document a link mark, as
// markdown renderers autolink them (references to Flow resources are mention
// nodes by now).
func linkBareURLs(document map[string]any) map[string]any {
	var walk func(node map[string]any)
	walk = func(node map[string]any) {
		if kind, _ := node["type"].(string); kind == "codeBlock" || kind == "diagram" || kind == "mention" {
			return
		}
		children, _ := node["content"].([]any)
		next := make([]any, 0, len(children))
		for _, child := range children {
			childNode, _ := child.(map[string]any)
			if childNode == nil || childNode["type"] != "text" {
				if childNode != nil {
					walk(childNode)
				}
				next = append(next, child)
				continue
			}
			text, _ := childNode["text"].(string)
			if _, linked := mentionLinkHref(childNode); linked || mentionHasMark(childNode, "code") {
				next = append(next, child)
				continue
			}
			cursor := 0
			piece := func(value string, href string) {
				if value == "" {
					return
				}
				item := map[string]any{"type": "text", "text": value}
				marks, _ := childNode["marks"].([]any)
				if href != "" {
					marks = append(slices.Clone(marks), map[string]any{"type": "link", "attrs": map[string]any{"href": href}})
				}
				if len(marks) > 0 {
					item["marks"] = marks
				}
				next = append(next, item)
			}
			for _, match := range mentionBareURL.FindAllStringIndex(text, -1) {
				end := match[0] + len(strings.TrimRight(text[match[0]:match[1]], ".,;:!?"))
				if match[0] > 0 && (isWordByte(text[match[0]-1]) || text[match[0]-1] == '/') {
					continue
				}
				piece(text[cursor:match[0]], "")
				piece(text[match[0]:end], text[match[0]:end])
				cursor = end
			}
			if cursor == 0 {
				next = append(next, child)
				continue
			}
			piece(text[cursor:], "")
		}
		if children != nil {
			node["content"] = next
		}
	}
	walk(document)
	return document
}

// companionDocument is the rich document stored beside markdown on surfaces
// that render markdown well on their own (comments, updates, issue
// descriptions): it is only worth storing when it adds a mention, and only when
// the markdown has nothing the converter would flatten (tables, images, nested
// lists, raw HTML). Otherwise nil keeps the markdown authoritative.
func (m *mentionResolver) companionDocument(markdown string) map[string]any {
	if strings.TrimSpace(markdown) == "" || !mentionMarkdownRepresentable(markdown) {
		return nil
	}
	document := m.markdownDocument(markdown)
	if !documentHasMention(document) {
		return nil
	}
	return document
}

var (
	mentionMarkdownTableRow    = regexp.MustCompile(`^\s*\|.*\|\s*$`)
	mentionMarkdownNestedList  = regexp.MustCompile(`^(?: {2,}|\t+)(?:[-*+]|\d+[.)])\s`)
	mentionMarkdownHTML        = regexp.MustCompile(`</?[A-Za-z][A-Za-z0-9-]*(?:\s[^>]*)?/?>`)
	mentionMarkdownImageOrAuto = regexp.MustCompile(`!\[[^\]]*\]\(|<https?://`)
)

// mentionMarkdownRepresentable reports whether mcpMarkdownDocument keeps the
// markdown's structure (it has no tables, images, nested lists or HTML).
func mentionMarkdownRepresentable(markdown string) bool {
	inFence := false
	for _, line := range strings.Split(markdown, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if mentionMarkdownTableRow.MatchString(line) || mentionMarkdownNestedList.MatchString(line) || mentionMarkdownHTML.MatchString(line) || mentionMarkdownImageOrAuto.MatchString(line) {
			return false
		}
	}
	return true
}

func documentHasMention(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		if typed["type"] == "mention" {
			return true
		}
		return documentHasMention(typed["content"])
	case []any:
		return slices.ContainsFunc(typed, documentHasMention)
	}
	return false
}

// convert turns the references in a document into mention nodes, in place:
// links to workspace resources (whatever their text), bare Flow URLs,
// team-key issue identifiers and @names. Code, code blocks, existing mention
// nodes and references that do not resolve are left as they are, so
// converting twice changes nothing.
func (m *mentionResolver) convert(document map[string]any) map[string]any {
	var walk func(node map[string]any)
	walk = func(node map[string]any) {
		kind, _ := node["type"].(string)
		if kind == "codeBlock" || kind == "diagram" || kind == "mention" {
			return
		}
		children, _ := node["content"].([]any)
		if len(children) == 0 {
			return
		}
		next := make([]any, 0, len(children))
		for index := 0; index < len(children); {
			child, _ := children[index].(map[string]any)
			if child == nil || child["type"] != "text" {
				if child != nil {
					walk(child)
				}
				next = append(next, children[index])
				index++
				continue
			}
			// Consecutive text nodes under one link (differently marked
			// parts of its text) form one reference.
			if href, linked := mentionLinkHref(child); linked {
				end := index + 1
				for end < len(children) {
					sibling, _ := children[end].(map[string]any)
					if sibling == nil || sibling["type"] != "text" {
						break
					}
					if other, ok := mentionLinkHref(sibling); !ok || other != href {
						break
					}
					end++
				}
				run := children[index:end]
				index = end
				text, code := "", false
				for _, item := range run {
					piece := item.(map[string]any)
					value, _ := piece["text"].(string)
					text += value
					code = code || mentionHasMark(piece, "code")
				}
				if !code && href != "" {
					if attrs, ok := m.urlTarget(href, text, true); ok {
						next = append(next, attrs.node())
						continue
					}
				}
				next = append(next, run...)
				continue
			}
			next = append(next, m.textNodes(child)...)
			index++
		}
		node["content"] = next
	}
	walk(document)
	return document
}

func mentionLinkHref(node map[string]any) (string, bool) {
	marks, _ := node["marks"].([]any)
	for _, mark := range marks {
		markNode, _ := mark.(map[string]any)
		if markNode["type"] == "link" {
			attrs, _ := markNode["attrs"].(map[string]any)
			href, _ := attrs["href"].(string)
			return strings.TrimSpace(href), true
		}
	}
	return "", false
}

func mentionHasMark(node map[string]any, kind string) bool {
	marks, _ := node["marks"].([]any)
	return slices.ContainsFunc(marks, func(mark any) bool {
		markNode, _ := mark.(map[string]any)
		return markNode["type"] == kind
	})
}

type mentionSpan struct {
	start, end int
	attrs      mentionAttrs
}

var mentionURLInText = regexp.MustCompile(`(?:https?://[^\s<>()\[\]]+|/[\w.~%-]+/(?:issue|project|initiative|document|team|view|review|customer|profiles|pipeline|issue-label|project-label|initiative-label)/[^\s<>()\[\]]+)`)

// mentionIdentifierCandidates matches `ABC-123`; mentionIdentifierSpans
// checks the boundaries the web's lookbehind / lookahead enforce.
var mentionIdentifierCandidates = regexp.MustCompile(`[A-Za-z][A-Za-z0-9]*-[0-9]+`)

// mentionIdentifierSpans finds issue identifiers that stand on their own (not
// inside a word, a path, an anchor, an email or a longer identifier).
func mentionIdentifierSpans(text string) [][2]int {
	spans := [][2]int{}
	for _, match := range mentionIdentifierCandidates.FindAllStringIndex(text, -1) {
		if match[0] > 0 && (isWordByte(text[match[0]-1]) || strings.IndexByte("/#.@-", text[match[0]-1]) >= 0) {
			continue
		}
		if match[1] < len(text) && isWordByte(text[match[1]]) {
			continue
		}
		spans = append(spans, [2]int{match[0], match[1]})
	}
	return spans
}

// textNodes splits one text node into text and mention nodes.
func (m *mentionResolver) textNodes(node map[string]any) []any {
	text, _ := node["text"].(string)
	if text == "" || mentionHasMark(node, "code") {
		return []any{node}
	}
	spans := []mentionSpan{}
	free := func(start, end int) bool {
		return !slices.ContainsFunc(spans, func(span mentionSpan) bool { return start < span.end && end > span.start })
	}
	for _, match := range mentionURLInText.FindAllStringIndex(text, -1) {
		raw := text[match[0]:match[1]]
		raw = strings.TrimRight(raw, ".,;:!?")
		end := match[0] + len(raw)
		if attrs, ok := m.urlTarget(raw, "", false); ok && free(match[0], end) {
			spans = append(spans, mentionSpan{match[0], end, attrs})
		}
	}
	for _, match := range mentionIdentifierSpans(text) {
		if !free(match[0], match[1]) {
			continue
		}
		if issue := m.issueByIdentifier(text[match[0]:match[1]]); issue != nil {
			spans = append(spans, mentionSpan{match[0], match[1], m.issueAttrs(*issue)})
		}
	}
	for start := 0; start < len(text); start++ {
		if text[start] != '@' {
			continue
		}
		if start > 0 {
			previous, _ := utf8.DecodeLastRuneInString(text[:start])
			if !unicode.IsSpace(previous) && previous != '(' {
				continue
			}
		}
		for _, person := range m.people {
			end := start + 1 + len(person.name)
			if end > len(text) || !strings.EqualFold(text[start+1:end], person.name) || end < len(text) && isWordByte(text[end]) {
				continue
			}
			if free(start, end) {
				spans = append(spans, mentionSpan{start, end, m.userAttrs(person.user)})
			}
			break
		}
	}
	if len(spans) == 0 {
		return []any{node}
	}
	slices.SortFunc(spans, func(left, right mentionSpan) int { return left.start - right.start })
	out := []any{}
	cursor := 0
	emit := func(value string) {
		if value == "" {
			return
		}
		piece := map[string]any{}
		for key, item := range node {
			piece[key] = item
		}
		piece["text"] = value
		out = append(out, piece)
	}
	for _, span := range spans {
		emit(text[cursor:span.start])
		out = append(out, span.attrs.node())
		cursor = span.end
	}
	emit(text[cursor:])
	return out
}

// ---------------------------------------------------------------- resources

func (m *mentionResolver) issueByIdentifier(identifier string) *domain.Issue {
	key := strings.ToUpper(strings.TrimSpace(identifier))
	if !m.teamKey(key) {
		return nil
	}
	for index := range m.issues {
		if strings.EqualFold(m.issues[index].Identifier, key) {
			return &m.issues[index]
		}
	}
	return nil
}

// teamKey reports whether an identifier's prefix is a team key. Workspace data
// without teams (a bare test bootstrap) leaves the decision to the issue list.
func (m *mentionResolver) teamKey(identifier string) bool {
	if len(m.teamKeys) == 0 {
		return true
	}
	prefix, _, ok := strings.Cut(identifier, "-")
	return ok && m.teamKeys[strings.ToUpper(prefix)]
}

func (m *mentionResolver) root() string {
	return "/" + encodeURIComponent(m.data.Workspace.URLKey)
}

func (m *mentionResolver) issueAttrs(issue domain.Issue) mentionAttrs {
	return mentionAttrs{Kind: "issue", ID: issue.ID, Label: issue.Identifier, Title: issue.Title, Href: m.root() + "/issue/" + encodeURIComponent(issue.Identifier) + "/" + encodeURIComponent(routeSlug(issue.Title))}
}

func (m *mentionResolver) userAttrs(user domain.User) mentionAttrs {
	return mentionAttrs{Kind: "user", ID: user.ID, Label: firstNonEmpty(user.DisplayName, user.Name)}
}

func (m *mentionResolver) projectPath(project domain.Project) string {
	return m.root() + "/project/" + encodeURIComponent(project.SlugID) + "/overview"
}

func (m *mentionResolver) projectAttrs(project domain.Project) mentionAttrs {
	return mentionAttrs{Kind: "project", ID: project.ID, Label: project.Name, Href: m.projectPath(project)}
}

func (m *mentionResolver) teamByKey(key string) *domain.Team {
	for index := range m.data.Teams {
		if strings.EqualFold(m.data.Teams[index].Key, key) {
			return &m.data.Teams[index]
		}
	}
	return nil
}

func (m *mentionResolver) findProject(slugOrID string) *domain.Project {
	for index := range m.data.Projects {
		if m.data.Projects[index].SlugID == slugOrID || m.data.Projects[index].ID == slugOrID {
			return &m.data.Projects[index]
		}
	}
	return nil
}

var (
	mentionGitHubPull     = regexp.MustCompile(`(?i)^https://(?:www\.)?github\.com/([^/\s]+)/([^/\s]+)/pull/(\d+)(?:[/?#].*)?$`)
	mentionUpdateAnchor   = regexp.MustCompile(`^(?:project-|initiative-)?update-`)
	mentionProjectTabs    = map[string]bool{"overview": true, "activity": true, "issues": true, "requests": true}
	mentionInitiativeTabs = map[string]bool{"overview": true, "activity": true, "projects": true}
)

// urlTarget resolves a URL (a link's href, or a bare URL in text) to the
// workspace resource it names. Relative paths and absolute http(s) URLs on any
// host count when their path is a Flow route of this workspace. Updates and
// comments are only addressable by link (link=true), labelled by its text.
func (m *mentionResolver) urlTarget(raw, text string, link bool) (mentionAttrs, bool) {
	raw = strings.TrimSpace(raw)
	if match := mentionGitHubPull.FindStringSubmatch(raw); match != nil {
		number, _ := strconv.Atoi(match[3])
		for _, review := range m.data.Reviews {
			if review.Number == number && strings.EqualFold(review.RepositoryOwner, match[1]) && strings.EqualFold(review.RepositoryName, match[2]) {
				return m.reviewAttrs(review), true
			}
		}
		return mentionAttrs{}, false
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return mentionAttrs{}, false
	}
	switch {
	case parsed.Scheme == "" && parsed.Host == "" && strings.HasPrefix(raw, "/"):
	case (strings.EqualFold(parsed.Scheme, "http") || strings.EqualFold(parsed.Scheme, "https")) && parsed.Host != "":
	default:
		return mentionAttrs{}, false
	}
	segments := []string{}
	for _, segment := range strings.Split(parsed.EscapedPath(), "/") {
		if segment == "" {
			continue
		}
		if decoded, err := url.PathUnescape(segment); err == nil {
			segment = decoded
		}
		segments = append(segments, segment)
	}
	if len(segments) < 2 || segments[0] != m.data.Workspace.URLKey {
		return mentionAttrs{}, false
	}
	href := parsed.EscapedPath()
	if parsed.RawQuery != "" {
		href += "?" + parsed.RawQuery
	}
	if parsed.Fragment != "" {
		href += "#" + parsed.EscapedFragment()
	}
	hash := parsed.Fragment
	linkOnly := func(kind, fallback string) (mentionAttrs, bool) {
		if !link {
			return mentionAttrs{}, false
		}
		return mentionAttrs{Kind: kind, ID: href, Label: firstNonEmpty(strings.TrimSpace(text), fallback), Href: href}, true
	}
	at := func(index int) string {
		if index < len(segments) {
			return segments[index]
		}
		return ""
	}
	count, section, third, fourth, fifth, sixth := len(segments), at(1), at(2), at(3), at(4), at(5)
	view := func(id string) (mentionAttrs, bool) {
		for _, item := range m.data.SavedViews {
			if item.ID == id || item.SlugID == id {
				return mentionAttrs{Kind: "view", ID: item.ID, Label: item.Name, Href: m.root() + "/view/" + encodeURIComponent(firstNonEmpty(item.SlugID, item.ID))}, true
			}
		}
		return mentionAttrs{}, false
	}
	switch section {
	case "issue":
		if third == "" || count > 4 {
			break
		}
		if strings.HasPrefix(hash, "comment-") {
			return linkOnly("comment", "Comment")
		}
		if issue := m.issueByIdentifier(third); issue != nil {
			return m.issueAttrs(*issue), true
		}
	case "project":
		if third == "" {
			break
		}
		if fourth == "view" && fifth != "" && fifth != "new" && (count == 5 || count == 6 && sixth == "edit") {
			return view(fifth)
		}
		if !(count == 3 || count == 4 && (fourth == "updates" || mentionProjectTabs[fourth]) || count == 5 && fourth == "view" && fifth == "new") {
			break
		}
		if mentionUpdateAnchor.MatchString(hash) {
			return linkOnly("update", "Update")
		}
		project := m.findProject(third)
		if project == nil {
			break
		}
		if milestoneID, ok := strings.CutPrefix(hash, "milestone-"); ok {
			for _, milestone := range project.Milestones {
				if milestone.ID == milestoneID {
					return mentionAttrs{Kind: "milestone", ID: milestone.ID, Label: milestone.Name, Href: m.projectPath(*project) + "#milestone-" + milestone.ID}, true
				}
			}
			break
		}
		return m.projectAttrs(*project), true
	case "initiative":
		if third == "" || !(count == 3 || fourth == "updates" || fourth == "view" && fifth != "" && count == 5 || count == 4 && mentionInitiativeTabs[fourth]) {
			break
		}
		if mentionUpdateAnchor.MatchString(hash) {
			return linkOnly("update", "Update")
		}
		for _, initiative := range m.data.Initiatives {
			if initiative.SlugID == third || initiative.ID == third {
				return mentionAttrs{Kind: "initiative", ID: initiative.ID, Label: initiative.Name, Href: m.root() + "/initiative/" + encodeURIComponent(initiative.SlugID) + "/overview"}, true
			}
		}
	case "document":
		if third == "" || count != 3 {
			break
		}
		for _, document := range m.data.Documents {
			if document.SlugID == third || document.ID == third {
				return mentionAttrs{Kind: "document", ID: document.ID, Label: document.Title, Href: m.root() + "/document/" + encodeURIComponent(document.SlugID)}, true
			}
		}
	case "profiles":
		if third == "" || !(count == 3 || count == 4 && fourth == "created") {
			break
		}
		for _, user := range m.data.Users {
			if strings.EqualFold(m.usernames[user.ID], third) || user.ID == third {
				return m.userAttrs(user), true
			}
		}
	case "team":
		team := m.teamByKey(third)
		if third == "" || team == nil {
			break
		}
		switch {
		case count == 3 || count == 4 && fourth == "overview":
			return mentionAttrs{Kind: "team", ID: team.ID, Label: team.Name, Href: m.root() + "/team/" + encodeURIComponent(team.Key) + "/overview"}, true
		case count == 5 && fourth == "cycle" && fifth != "upcoming":
			for _, cycle := range m.data.Cycles {
				if cycle.TeamID == team.ID && (strconv.Itoa(cycle.Number) == fifth || cycle.ID == fifth) {
					return m.cycleAttrs(cycle), true
				}
			}
		case fourth == "view" && fifth != "" && (count == 5 || count == 6 && sixth == "edit"):
			return view(fifth)
		case fourth == "projects" && fifth == "view" && sixth != "" && sixth != "new" && (count == 6 || count == 7 && at(6) == "edit"):
			return view(sixth)
		}
	case "view":
		if third != "" && (count == 3 || count == 4 && fourth == "edit") {
			return view(third)
		}
	case "projects":
		if third == "view" && fourth != "" && fourth != "new" && (count == 4 || count == 5 && fifth == "edit") {
			return view(fourth)
		}
	case "issue-label", "project-label", "initiative-label":
		if third == "" || count != 3 {
			break
		}
		resourceType := strings.TrimSuffix(section, "-label")
		var found *domain.IssueLabel
		for index := range m.data.Labels {
			label := &m.data.Labels[index]
			if !strings.EqualFold(label.Name, third) {
				continue
			}
			if firstNonEmpty(label.ResourceType, "issue") == resourceType {
				found = label
				break
			}
			if found == nil {
				found = label
			}
		}
		if found != nil {
			return m.labelAttrs(*found), true
		}
	case "customer":
		if third == "" || count != 3 {
			break
		}
		for _, customer := range m.data.Customers {
			if customer.ID == third || lastUTF16(customer.ID, 12) == lastUTF16(third, 12) {
				return mentionAttrs{Kind: "customer", ID: customer.ID, Label: customer.Name, Href: m.root() + "/customer/" + routeSlug(customer.Name) + "-" + lastUTF16(customer.ID, 12)}, true
			}
		}
	case "pipeline":
		if third == "" || fourth != "release" || fifth == "" || sixth != "issues" && sixth != "release-notes" || count != 6 {
			break
		}
		var pipeline *domain.ReleasePipeline
		for index := range m.data.ReleasePipelines {
			if m.data.ReleasePipelines[index].SlugID == third || m.data.ReleasePipelines[index].ID == third {
				pipeline = &m.data.ReleasePipelines[index]
				break
			}
		}
		for _, release := range m.data.Releases {
			if (release.SlugID == fifth || release.ID == fifth) && (pipeline == nil || release.PipelineID == "" || release.PipelineID == pipeline.ID) {
				return m.releaseAttrs(release), true
			}
		}
	case "review":
		if third == "" || !(count == 3 || count == 4 && (fourth == "review" || fourth == "changes")) {
			break
		}
		for _, review := range m.data.Reviews {
			if review.SlugID == third || review.ID == third {
				return m.reviewAttrs(review), true
			}
		}
	}
	return mentionAttrs{}, false
}

func (m *mentionResolver) cycleAttrs(cycle domain.Cycle) mentionAttrs {
	key := ""
	for _, team := range m.data.Teams {
		if team.ID == cycle.TeamID {
			key = team.Key
		}
	}
	return mentionAttrs{Kind: "cycle", ID: cycle.ID, Label: firstNonEmpty(cycle.Name, "Cycle "+strconv.Itoa(cycle.Number)), Href: m.root() + "/team/" + encodeURIComponent(key) + "/cycle/" + strconv.Itoa(cycle.Number)}
}

func (m *mentionResolver) labelAttrs(label domain.IssueLabel) mentionAttrs {
	resourceType := "issue"
	if label.ResourceType == "project" || label.ResourceType == "initiative" {
		resourceType = label.ResourceType
	}
	return mentionAttrs{Kind: "label", ID: label.ID, Label: label.Name, Href: m.root() + "/" + resourceType + "-label/" + encodeURIComponent(label.Name)}
}

func (m *mentionResolver) releaseAttrs(release domain.Release) mentionAttrs {
	pipelineSlug := release.PipelineID
	for _, pipeline := range m.data.ReleasePipelines {
		if pipeline.ID == release.PipelineID {
			pipelineSlug = pipeline.SlugID
			break
		}
	}
	return mentionAttrs{Kind: "release", ID: release.ID, Label: release.Name, Href: m.root() + "/pipeline/" + encodeURIComponent(pipelineSlug) + "/release/" + encodeURIComponent(release.SlugID) + "/issues"}
}

func (m *mentionResolver) reviewAttrs(review domain.CodeReview) mentionAttrs {
	return mentionAttrs{Kind: "review", ID: review.ID, Label: review.Title, Href: m.root() + "/review/" + encodeURIComponent(review.SlugID)}
}

// ---------------------------------------------------------------- issues

// mentionIssueReferences lists, in order and at most 50, the issue
// identifiers markdown names (bare, or in an issue URL) whose prefix is a team
// key of the workspace.
func mentionIssueReferences(data *domain.Bootstrap, markdowns ...string) []string {
	resolver := &mentionResolver{data: data, teamKeys: map[string]bool{}}
	for _, team := range data.Teams {
		resolver.teamKeys[strings.ToUpper(strings.TrimSpace(team.Key))] = true
	}
	identifiers := []string{}
	seen := map[string]bool{}
	add := func(value string) {
		value = strings.ToUpper(strings.TrimSpace(value))
		if value != "" && !seen[value] && len(identifiers) < 50 && resolver.teamKey(value) && mentionIdentifierCandidates.FindString(value) == value {
			seen[value] = true
			identifiers = append(identifiers, value)
		}
	}
	for _, markdown := range markdowns {
		for _, match := range mentionIdentifierSpans(markdown) {
			add(markdown[match[0]:match[1]])
		}
		for _, match := range mentionIssuePath.FindAllStringSubmatch(markdown, -1) {
			if decoded, err := url.PathUnescape(match[1]); err == nil {
				add(decoded)
			}
		}
	}
	return identifiers
}

var mentionIssuePath = regexp.MustCompile(`/issue/([^/?#\s)\]>]+)`)

// mentionReferencedIssues looks up, through the identifier index and the
// requester's issue access, the issues markdown refers to that known does not
// already hold. Bounded by mentionIssueReferences' cap; never scans issues.
func (s *server) mentionReferencedIssues(ctx context.Context, query store.IssueRecordQuery, data *domain.Bootstrap, known []domain.Issue, markdowns ...string) []domain.Issue {
	issues := []domain.Issue{}
	for _, identifier := range mentionIssueReferences(data, markdowns...) {
		if slices.ContainsFunc(known, func(issue domain.Issue) bool { return strings.EqualFold(issue.Identifier, identifier) }) {
			continue
		}
		if issue, err := s.store.AuthorizedIssueRecord(ctx, query, identifier); err == nil && strings.EqualFold(issue.Identifier, identifier) {
			issues = append(issues, issue)
		}
	}
	return issues
}

// mcpMentionResolver resolves the references in markdown an MCP caller (or
// the Flow agent) writes, with the issues that caller may access.
func (s *server) mcpMentionResolver(ctx context.Context, actor mcpActor, data *domain.Bootstrap, markdowns ...string) *mentionResolver {
	issues := slices.Clone(data.Issues)
	if query, err := s.mcpIssueQuery(ctx, actor); err == nil {
		issues = append(issues, s.mentionReferencedIssues(ctx, query, data, data.Issues, markdowns...)...)
	}
	return newMentionResolver(data, issues)
}

// ---------------------------------------------------------------- paths

// encodeURIComponent matches JavaScript's encodeURIComponent, which the web's
// route builders use.
func encodeURIComponent(value string) string {
	var builder strings.Builder
	for _, char := range []byte(value) {
		if char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || strings.IndexByte("-_.!~*'()", char) >= 0 {
			builder.WriteByte(char)
			continue
		}
		fmt.Fprintf(&builder, "%%%02X", char)
	}
	return builder.String()
}

// routeSlug matches the web route builders' slug(): NFKC, lower case, runs of
// anything but letters and numbers as one dash, at most 80 UTF-16 units.
func routeSlug(value string) string {
	value = strings.ToLower(norm.NFKC.String(value))
	var builder strings.Builder
	dash := false
	for _, char := range value {
		if unicode.IsLetter(char) || unicode.IsNumber(char) {
			builder.WriteRune(char)
			dash = false
		} else if !dash {
			builder.WriteByte('-')
			dash = true
		}
	}
	slug := strings.Trim(builder.String(), "-")
	units := 0
	for index, char := range slug {
		units += utf16.RuneLen(char)
		if units > 80 {
			slug = slug[:index]
			break
		}
	}
	if slug == "" {
		return "issue"
	}
	return slug
}

func utf16Length(value string) int {
	return len(utf16.Encode([]rune(value)))
}

// lastUTF16 is JavaScript's value.slice(-count) for ASCII-range ids.
func lastUTF16(value string, count int) string {
	runes := []rune(value)
	if len(runes) <= count {
		return value
	}
	return string(runes[len(runes)-count:])
}
