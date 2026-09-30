package main

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

// Rich loop instructions: the editor sends ProseMirror/Tiptap JSON
// (instructionsData) beside the markdown instructions. The markdown stays
// what the model reads; mentions in the document are resolved into plain
// references so the model knows exactly which issue, project, user or
// document a chip meant.

// loopModelInstructions is the instruction text the model gets.
func loopModelInstructions(loop domain.Loop) string {
	if strings.TrimSpace(loop.Instructions) != "" || loop.InstructionsData == nil {
		return loop.Instructions
	}
	return proseMirrorPlainText(loop.InstructionsData)
}

// proseMirrorPlainText renders a ProseMirror document as readable text:
// blocks on their own lines, list items prefixed, mentions by label.
func proseMirrorPlainText(document map[string]any) string {
	var text strings.Builder
	var walk func(node map[string]any, prefix string)
	walk = func(node map[string]any, prefix string) {
		kind, _ := node["type"].(string)
		attrs, _ := node["attrs"].(map[string]any)
		switch kind {
		case "text":
			value, _ := node["text"].(string)
			text.WriteString(value)
			return
		case "hardBreak":
			text.WriteString("\n")
			return
		case "mention":
			label := firstNonEmpty(stringAttr(attrs, "label"), stringAttr(attrs, "title"), stringAttr(attrs, "id"))
			if firstNonEmpty(stringAttr(attrs, "mentionType"), "user") == "user" {
				label = "@" + label
			}
			text.WriteString(label)
			return
		}
		children, _ := node["content"].([]any)
		switch kind {
		case "bulletList", "orderedList", "taskList":
			for index, child := range children {
				item, ok := child.(map[string]any)
				if !ok {
					continue
				}
				marker := "- "
				if kind == "orderedList" {
					marker = fmt.Sprintf("%d. ", index+1)
				}
				text.WriteString(prefix + marker)
				walk(item, prefix+"  ")
			}
			return
		case "heading":
			level := 1
			if value, ok := attrs["level"].(float64); ok && value >= 1 && value <= 6 {
				level = int(value)
			}
			text.WriteString(strings.Repeat("#", level) + " ")
		case "codeBlock":
			text.WriteString("```\n")
		case "blockquote":
			text.WriteString("> ")
		}
		for index, child := range children {
			item, ok := child.(map[string]any)
			if !ok {
				continue
			}
			childKind, _ := item["type"].(string)
			if index > 0 && (kind == "listItem" || kind == "taskItem") && childKind != "bulletList" && childKind != "orderedList" && childKind != "taskList" {
				text.WriteString(prefix)
			}
			walk(item, prefix)
		}
		switch kind {
		case "codeBlock":
			text.WriteString("\n```\n\n")
		case "paragraph", "heading", "blockquote", "horizontalRule":
			if prefix == "" {
				text.WriteString("\n\n")
			} else {
				text.WriteString("\n")
			}
		}
	}
	walk(document, "")
	lines := strings.Split(strings.TrimSpace(text.String()), "\n")
	for index := range lines {
		lines[index] = strings.TrimRight(lines[index], " ")
	}
	return strings.ReplaceAll(strings.Join(lines, "\n"), "\n\n\n", "\n\n")
}

func stringAttr(attrs map[string]any, key string) string {
	value, _ := attrs[key].(string)
	return strings.TrimSpace(value)
}

type loopMention struct {
	Kind  string
	ID    string
	Label string
	Title string
	Href  string
}

func collectLoopMentions(value any, mentions *[]loopMention, seen map[string]bool) {
	switch typed := value.(type) {
	case map[string]any:
		if typed["type"] == "mention" {
			attrs, _ := typed["attrs"].(map[string]any)
			mention := loopMention{Kind: firstNonEmpty(stringAttr(attrs, "mentionType"), "user"), ID: stringAttr(attrs, "id"), Label: stringAttr(attrs, "label"), Title: stringAttr(attrs, "title"), Href: stringAttr(attrs, "href")}
			key := mention.Kind + "|" + firstNonEmpty(mention.ID, mention.Label)
			if key != mention.Kind+"|" && !seen[key] && len(*mentions) < 50 {
				seen[key] = true
				*mentions = append(*mentions, mention)
			}
			return
		}
		for _, child := range typed {
			collectLoopMentions(child, mentions, seen)
		}
	case []any:
		for _, child := range typed {
			collectLoopMentions(child, mentions, seen)
		}
	}
}

// loopInstructionReferences resolves the mentions in the instructions
// document into one line each, e.g. `- Issue DEV-4 "Import your data" (id issue_1)`.
func (s *server) loopInstructionReferences(ctx context.Context, workspace string, data domain.Bootstrap, loop domain.Loop) string {
	if loop.InstructionsData == nil {
		return ""
	}
	mentions := []loopMention{}
	collectLoopMentions(loop.InstructionsData, &mentions, map[string]bool{})
	var lines strings.Builder
	for _, mention := range mentions {
		fallback := firstNonEmpty(mention.Title, mention.Label, mention.ID)
		line := ""
		switch mention.Kind {
		case "issue":
			issue, err := s.store.IssueRecord(ctx, workspace, mention.ID)
			if err != nil && mention.Label != "" {
				issue, err = s.issueByIdentifier(ctx, workspace, mention.Label)
			}
			if err == nil {
				line = fmt.Sprintf("Issue %s %q (id %s)", issue.Identifier, issue.Title, issue.ID)
			} else {
				line = fmt.Sprintf("Issue %s", fallback)
			}
		case "project":
			line = fmt.Sprintf("Project %q", fallback)
			for _, project := range data.Projects {
				if project.ID == mention.ID || project.SlugID == mention.ID {
					line = fmt.Sprintf("Project %q (id %s)", project.Name, project.ID)
				}
			}
		case "document":
			line = fmt.Sprintf("Document %q", fallback)
			for _, document := range data.Documents {
				if document.ID == mention.ID || document.SlugID == mention.ID {
					line = fmt.Sprintf("Document %q (id %s)", document.Title, document.ID)
				}
			}
		case "user":
			line = "User " + strings.TrimPrefix(fallback, "@")
			if user := userByID(&data, mention.ID); user != nil {
				line = fmt.Sprintf("User %s (id %s)", firstNonEmpty(user.Name, user.DisplayName), user.ID)
			}
		default:
			line = fmt.Sprintf("%s%s %q", strings.ToUpper(mention.Kind[:1]), mention.Kind[1:], fallback)
			if mention.ID != "" && mention.ID != fallback {
				line += " (id " + mention.ID + ")"
			}
		}
		lines.WriteString("- " + line + "\n")
	}
	return lines.String()
}

var (
	loopIssueIdentifier = regexp.MustCompile(`\b[A-Z][A-Z0-9]{0,9}-\d+\b`)
	loopEntityPath      = regexp.MustCompile(`/(issue|project|document)/([^/?#]+)`)
)

// loopMarkdownDocument turns markdown instructions (as the loop builder agent
// writes them) into the editor's document, so they show as rich text with
// entity chips: known issue identifiers, @user names and links to Flow
// issues, projects and documents become mention nodes.
func loopMarkdownDocument(data *domain.Bootstrap, markdown string) map[string]any {
	document := mcpMarkdownDocument(markdown)
	var walk func(node map[string]any)
	walk = func(node map[string]any) {
		children, _ := node["content"].([]any)
		if len(children) == 0 {
			return
		}
		next := make([]any, 0, len(children))
		for _, child := range children {
			childNode, ok := child.(map[string]any)
			if !ok {
				next = append(next, child)
				continue
			}
			if childNode["type"] == "text" {
				next = append(next, loopTextMentions(data, childNode)...)
				continue
			}
			walk(childNode)
			next = append(next, childNode)
		}
		node["content"] = next
	}
	walk(document)
	return document
}

// loopTextMentions splits one text node into text and mention nodes.
func loopTextMentions(data *domain.Bootstrap, node map[string]any) []any {
	text, _ := node["text"].(string)
	marks, _ := node["marks"].([]any)
	for _, mark := range marks {
		markNode, _ := mark.(map[string]any)
		switch markNode["type"] {
		case "code":
			return []any{node}
		case "link":
			attrs, _ := markNode["attrs"].(map[string]any)
			if mention := loopLinkMention(data, stringAttr(attrs, "href"), text); mention != nil {
				return []any{mention}
			}
			return []any{node}
		}
	}
	type span struct {
		start, end int
		mention    map[string]any
	}
	spans := []span{}
	for _, match := range loopIssueIdentifier.FindAllStringIndex(text, -1) {
		if issue := loopIssueByIdentifier(data, text[match[0]:match[1]]); issue != nil {
			spans = append(spans, span{match[0], match[1], loopIssueMention(data, *issue)})
		}
	}
	for _, user := range data.Users {
		for _, name := range []string{user.DisplayName, user.Name} {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			for offset := 0; offset < len(text); {
				index := strings.Index(text[offset:], "@"+name)
				if index < 0 {
					break
				}
				start, end := offset+index, offset+index+1+len(name)
				offset = end
				if end < len(text) && (isWordByte(text[end])) {
					continue
				}
				spans = append(spans, span{start, end, map[string]any{"type": "mention", "attrs": map[string]any{"mentionType": "user", "id": user.ID, "label": name}}})
			}
		}
	}
	if len(spans) == 0 {
		return []any{node}
	}
	slices.SortFunc(spans, func(left, right span) int { return left.start - right.start })
	out := []any{}
	cursor := 0
	emit := func(value string) {
		if value == "" {
			return
		}
		piece := map[string]any{"type": "text", "text": value}
		if len(marks) > 0 {
			piece["marks"] = marks
		}
		out = append(out, piece)
	}
	for _, item := range spans {
		if item.start < cursor {
			continue // overlapping match
		}
		emit(text[cursor:item.start])
		out = append(out, item.mention)
		cursor = item.end
	}
	emit(text[cursor:])
	return out
}

func isWordByte(value byte) bool {
	return value == '_' || value == '-' || value >= '0' && value <= '9' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

// loopReferencedIssues looks up, through the identifier index and the
// requester's issue access, the issues the markdown refers to by identifier or
// link. Loop mutations run on workspace metadata without the issue collection,
// so mentions resolve against this short list instead.
func (s *server) loopReferencedIssues(r *http.Request, markdowns ...string) []domain.Issue {
	identifiers := []string{}
	seen := map[string]bool{}
	add := func(value string) {
		value = strings.ToUpper(strings.TrimSpace(value))
		if value != "" && !seen[value] && len(identifiers) < 50 {
			seen[value] = true
			identifiers = append(identifiers, value)
		}
	}
	for _, markdown := range markdowns {
		for _, match := range loopIssueIdentifier.FindAllString(markdown, -1) {
			add(match)
		}
		for _, match := range loopEntityPath.FindAllStringSubmatch(markdown, -1) {
			if match[1] == "issue" {
				add(match[2])
			}
		}
	}
	if len(identifiers) == 0 {
		return nil
	}
	_, query, err := s.issueRecordsQuery(r)
	if err != nil {
		return nil
	}
	issues := []domain.Issue{}
	for _, identifier := range identifiers {
		if issue, err := s.store.AuthorizedIssueRecord(r.Context(), query, identifier); err == nil && strings.EqualFold(issue.Identifier, identifier) {
			issues = append(issues, issue)
		}
	}
	return issues
}

// loopMarkdownDocumentWithIssues is loopMarkdownDocument with the issue
// collection replaced by the issues loopReferencedIssues found.
func loopMarkdownDocumentWithIssues(data *domain.Bootstrap, markdown string, issues []domain.Issue) map[string]any {
	view := *data
	view.Issues = issues
	return loopMarkdownDocument(&view, markdown)
}

func loopIssueByIdentifier(data *domain.Bootstrap, identifier string) *domain.Issue {
	for index := range data.Issues {
		if strings.EqualFold(data.Issues[index].Identifier, identifier) {
			return &data.Issues[index]
		}
	}
	return nil
}

func loopIssueMention(data *domain.Bootstrap, issue domain.Issue) map[string]any {
	return map[string]any{"type": "mention", "attrs": map[string]any{"mentionType": "issue", "id": issue.ID, "label": issue.Identifier, "title": issue.Title, "href": "/" + data.Workspace.URLKey + "/issue/" + issue.Identifier}}
}

// loopLinkMention turns a link to a Flow issue, project or document into a chip.
func loopLinkMention(data *domain.Bootstrap, href, text string) map[string]any {
	match := loopEntityPath.FindStringSubmatch(href)
	if match == nil {
		return nil
	}
	key := match[2]
	switch match[1] {
	case "issue":
		if issue := loopIssueByIdentifier(data, key); issue != nil {
			return loopIssueMention(data, *issue)
		}
	case "project":
		for _, project := range data.Projects {
			if project.SlugID == key || project.ID == key {
				return map[string]any{"type": "mention", "attrs": map[string]any{"mentionType": "project", "id": project.ID, "label": firstNonEmpty(project.Name, text), "href": href}}
			}
		}
	case "document":
		for _, document := range data.Documents {
			if document.SlugID == key || document.ID == key {
				return map[string]any{"type": "mention", "attrs": map[string]any{"mentionType": "document", "id": document.ID, "label": firstNonEmpty(document.Title, text), "href": href}}
			}
		}
	}
	return nil
}
