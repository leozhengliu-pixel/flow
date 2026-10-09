package main

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// mcpMarkdownDocument converts agent-written Markdown into the rich-text
// document the Flow editor stores beside the Markdown projection (documents,
// document templates). The editor renders contentData in preference to the
// Markdown text, so saving Markdown alone would leave an empty or stale editor
// document behind. It covers the StarterKit blocks the editor offers:
// headings (1-3), paragraphs, bullet/ordered/task lists, quotes, code blocks
// and dividers, plus bold, italic, strike, inline code and links.
func mcpMarkdownDocument(markdown string) map[string]any {
	lines := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	content := mcpMarkdownBlocks(lines)
	if len(content) == 0 {
		content = []any{map[string]any{"type": "paragraph"}}
	}
	return map[string]any{"type": "doc", "content": content}
}

var (
	mcpMarkdownHeading = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	mcpMarkdownBullet  = regexp.MustCompile(`^\s*[-*+]\s+(.*)$`)
	mcpMarkdownOrdered = regexp.MustCompile(`^\s*(\d+)[.)]\s+(.*)$`)
	mcpMarkdownTask    = regexp.MustCompile(`^\[([ xX])\]\s+(.*)$`)
	mcpMarkdownRule    = regexp.MustCompile(`^\s*(?:(?:-\s*){3,}|(?:\*\s*){3,}|(?:_\s*){3,})$`)
	mcpMarkdownInline  = regexp.MustCompile("`([^`]+)`|\\*\\*([^*]+)\\*\\*|__([^_]+)__|~~([^~]+)~~|\\*([^*\\s][^*]*)\\*|_([^_\\s][^_]*)_|\\[([^\\]]+)\\]\\(([^)\\s]+)\\)")
)

func mcpMarkdownBlocks(lines []string) []any {
	blocks := []any{}
	for index := 0; index < len(lines); {
		line := lines[index]
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			index++
		case strings.HasPrefix(trimmed, "```"):
			language := strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			code := []string{}
			index++
			for index < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[index]), "```") {
				code = append(code, lines[index])
				index++
			}
			index++
			block := map[string]any{"type": "codeBlock", "attrs": map[string]any{"language": nil}}
			if language != "" {
				block["attrs"] = map[string]any{"language": language}
			}
			if text := strings.Join(code, "\n"); text != "" {
				block["content"] = []any{map[string]any{"type": "text", "text": text}}
			}
			blocks = append(blocks, block)
		case mcpMarkdownRule.MatchString(trimmed):
			blocks = append(blocks, map[string]any{"type": "horizontalRule"})
			index++
		case mcpMarkdownHeading.MatchString(trimmed):
			match := mcpMarkdownHeading.FindStringSubmatch(trimmed)
			level := min(len(match[1]), 3)
			block := map[string]any{"type": "heading", "attrs": map[string]any{"level": level}}
			if inline := mcpMarkdownInlineNodes(match[2]); len(inline) > 0 {
				block["content"] = inline
			}
			blocks = append(blocks, block)
			index++
		case strings.HasPrefix(trimmed, ">"):
			quoted := []string{}
			for index < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[index]), ">") {
				quoted = append(quoted, strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(lines[index]), ">"), " "))
				index++
			}
			blocks = append(blocks, map[string]any{"type": "blockquote", "content": mcpMarkdownNonEmpty(mcpMarkdownBlocks(quoted))})
		case mcpMarkdownBullet.MatchString(line):
			items, taskItems, task := []any{}, []any{}, false
			for index < len(lines) && mcpMarkdownBullet.MatchString(lines[index]) {
				text := mcpMarkdownBullet.FindStringSubmatch(lines[index])[1]
				if match := mcpMarkdownTask.FindStringSubmatch(text); match != nil {
					task = true
					taskItems = append(taskItems, map[string]any{"type": "taskItem", "attrs": map[string]any{"checked": match[1] != " "}, "content": []any{mcpMarkdownParagraph(match[2])}})
				}
				items = append(items, map[string]any{"type": "listItem", "content": []any{mcpMarkdownParagraph(text)}})
				index++
			}
			if task && len(taskItems) == len(items) {
				blocks = append(blocks, map[string]any{"type": "taskList", "content": taskItems})
			} else {
				blocks = append(blocks, map[string]any{"type": "bulletList", "content": items})
			}
		case mcpMarkdownOrdered.MatchString(line):
			items := []any{}
			start := mcpMarkdownOrdered.FindStringSubmatch(line)[1]
			for index < len(lines) && mcpMarkdownOrdered.MatchString(lines[index]) {
				items = append(items, map[string]any{"type": "listItem", "content": []any{mcpMarkdownParagraph(mcpMarkdownOrdered.FindStringSubmatch(lines[index])[2])}})
				index++
			}
			startNumber, _ := strconv.Atoi(start)
			blocks = append(blocks, map[string]any{"type": "orderedList", "attrs": map[string]any{"start": max(startNumber, 1)}, "content": items})
		default:
			paragraph := []string{}
			for index < len(lines) && mcpMarkdownParagraphLine(lines[index]) {
				paragraph = append(paragraph, strings.TrimSpace(lines[index]))
				index++
			}
			blocks = append(blocks, mcpMarkdownParagraph(strings.Join(paragraph, "\n")))
		}
	}
	return blocks
}

func mcpMarkdownParagraphLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return trimmed != "" && !strings.HasPrefix(trimmed, "```") && !strings.HasPrefix(trimmed, ">") && !mcpMarkdownHeading.MatchString(trimmed) && !mcpMarkdownRule.MatchString(trimmed) && !mcpMarkdownBullet.MatchString(line) && !mcpMarkdownOrdered.MatchString(line)
}

func mcpMarkdownNonEmpty(blocks []any) []any {
	if len(blocks) == 0 {
		return []any{map[string]any{"type": "paragraph"}}
	}
	return blocks
}

func mcpMarkdownParagraph(text string) map[string]any {
	paragraph := map[string]any{"type": "paragraph"}
	nodes := []any{}
	for lineIndex, line := range strings.Split(text, "\n") {
		if lineIndex > 0 {
			nodes = append(nodes, map[string]any{"type": "hardBreak"})
		}
		nodes = append(nodes, mcpMarkdownInlineNodes(line)...)
	}
	if len(nodes) > 0 {
		paragraph["content"] = nodes
	}
	return paragraph
}

// mcpMarkdownIntrawordUnderscore reports whether text[start:end], an
// underscore-delimited span, touches a letter or digit on the outside.
func mcpMarkdownIntrawordUnderscore(text string, start, end int) bool {
	before, _ := utf8.DecodeLastRuneInString(text[:start])
	after, _ := utf8.DecodeRuneInString(text[end:])
	wordRune := func(value rune) bool { return unicode.IsLetter(value) || unicode.IsDigit(value) }
	return (start > 0 && wordRune(before)) || (end < len(text) && wordRune(after))
}

func mcpMarkdownInlineNodes(text string) []any {
	nodes := []any{}
	appendText := func(value string, marks ...map[string]any) {
		if value == "" {
			return
		}
		node := map[string]any{"type": "text", "text": value}
		if len(marks) > 0 {
			list := make([]any, 0, len(marks))
			for _, mark := range marks {
				list = append(list, mark)
			}
			node["marks"] = list
		}
		nodes = append(nodes, node)
	}
	cursor := 0
	searchFrom := 0
	for searchFrom <= len(text) {
		match := mcpMarkdownInline.FindStringSubmatchIndex(text[searchFrom:])
		if match == nil {
			break
		}
		for index := range match {
			if match[index] >= 0 {
				match[index] += searchFrom
			}
		}
		// Underscore emphasis does not open or close inside a word
		// (snake_case names such as get_issue stay literal): skip the
		// underscore and look again after it.
		if (match[6] >= 0 || match[12] >= 0) && mcpMarkdownIntrawordUnderscore(text, match[0], match[1]) {
			searchFrom = match[0] + 1
			continue
		}
		searchFrom = match[1]
		appendText(text[cursor:match[0]])
		group := func(index int) string {
			if match[2*index] < 0 {
				return ""
			}
			return text[match[2*index]:match[2*index+1]]
		}
		switch {
		case match[2] >= 0:
			appendText(group(1), map[string]any{"type": "code"})
		case match[4] >= 0:
			appendText(group(2), map[string]any{"type": "bold"})
		case match[6] >= 0:
			appendText(group(3), map[string]any{"type": "bold"})
		case match[8] >= 0:
			appendText(group(4), map[string]any{"type": "strike"})
		case match[10] >= 0:
			appendText(group(5), map[string]any{"type": "italic"})
		case match[12] >= 0:
			appendText(group(6), map[string]any{"type": "italic"})
		default:
			// Link text keeps its own inline formatting (code, bold, ...).
			link := map[string]any{"type": "link", "attrs": map[string]any{"href": group(8)}}
			for _, child := range mcpMarkdownInlineNodes(group(7)) {
				node := child.(map[string]any)
				marks, _ := node["marks"].([]any)
				node["marks"] = append(marks, link)
				nodes = append(nodes, node)
			}
		}
		cursor = match[1]
	}
	appendText(text[cursor:])
	return nodes
}
