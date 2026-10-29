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
// headings (1-4), paragraphs, bullet/ordered/task lists, quotes, code blocks,
// dividers and GFM tables, the description editor's image, video and file
// card blocks, plus bold, italic, strike, inline code and links.
func mcpMarkdownDocument(markdown string) map[string]any {
	lines := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	content := mcpMarkdownBlocks(lines)
	if len(content) == 0 {
		content = []any{map[string]any{"type": "paragraph"}}
	}
	return map[string]any{"type": "doc", "content": content}
}

var (
	mcpMarkdownHeading      = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	mcpMarkdownBullet       = regexp.MustCompile(`^\s*[-*+]\s+(.*)$`)
	mcpMarkdownOrdered      = regexp.MustCompile(`^\s*(\d+)[.)]\s+(.*)$`)
	mcpMarkdownTask         = regexp.MustCompile(`^\[([ xX])\]\s+(.*)$`)
	mcpMarkdownRule         = regexp.MustCompile(`^\s*(?:(?:-\s*){3,}|(?:\*\s*){3,}|(?:_\s*){3,})$`)
	mcpMarkdownTableDivider = regexp.MustCompile(`^\s*\|?\s*:?-+:?\s*(?:\|\s*:?-+:?\s*)*\|?\s*$`)
	// A line that is only an image, video or file card is that block, as the
	// editor's Markdown tokenizers read it (image-extension.ts, file-extension.ts).
	mcpMarkdownImage  = regexp.MustCompile(`^!\[([^\]]*)\]\(([^)\s]+)(?:\s+"([^"]*)")?\)$`)
	mcpMarkdownVideo  = regexp.MustCompile(`^::video\[([^\]]*)\]\(([^)\s]+)\)$`)
	mcpMarkdownFile   = regexp.MustCompile(`^::file\[([^\]]*)\]\(([^)\s]+)\)(?:\{size="([^"]*)" type="([^"]*)"\})?$`)
	mcpMarkdownInline = regexp.MustCompile("`([^`]+)`|\\*\\*([^*]+)\\*\\*|__([^_]+)__|~~([^~]+)~~|\\*([^*\\s][^*]*)\\*|_([^_\\s][^_]*)_|\\[([^\\]]+)\\]\\(([^)\\s]+)\\)")
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
		case mcpMarkdownTableStart(lines, index):
			header := mcpMarkdownTableCells(line)
			rows := []any{mcpMarkdownTableRow("tableHeader", header, len(header))}
			index += 2
			for index < len(lines) && strings.TrimSpace(lines[index]) != "" && strings.Contains(lines[index], "|") {
				rows = append(rows, mcpMarkdownTableRow("tableCell", mcpMarkdownTableCells(lines[index]), len(header)))
				index++
			}
			blocks = append(blocks, map[string]any{"type": "table", "content": rows})
		case mcpMarkdownMediaBlock(trimmed) != nil:
			blocks = append(blocks, mcpMarkdownMediaBlock(trimmed))
			index++
		case mcpMarkdownRule.MatchString(trimmed):
			blocks = append(blocks, map[string]any{"type": "horizontalRule"})
			index++
		case mcpMarkdownHeading.MatchString(trimmed):
			match := mcpMarkdownHeading.FindStringSubmatch(trimmed)
			level := min(len(match[1]), 4)
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
			for index < len(lines) && mcpMarkdownParagraphLine(lines[index]) && (len(paragraph) == 0 || !mcpMarkdownTableStart(lines, index)) {
				paragraph = append(paragraph, strings.TrimSpace(lines[index]))
				index++
			}
			blocks = append(blocks, mcpMarkdownParagraph(strings.Join(paragraph, "\n")))
		}
	}
	return blocks
}

// mcpMarkdownTableStart reports whether lines[index] opens a GFM table: a
// row with a pipe followed by a divider row with as many columns (so a "---"
// under a line that happens to contain a pipe stays a rule). Headings, quotes
// and list items keep their own meaning.
func mcpMarkdownTableStart(lines []string, index int) bool {
	line := lines[index]
	if trimmed := strings.TrimSpace(line); mcpMarkdownHeading.MatchString(trimmed) || strings.HasPrefix(trimmed, ">") || mcpMarkdownBullet.MatchString(line) || mcpMarkdownOrdered.MatchString(line) {
		return false
	}
	if index+1 >= len(lines) || !strings.Contains(line, "|") || !strings.Contains(lines[index+1], "-") || !mcpMarkdownTableDivider.MatchString(lines[index+1]) {
		return false
	}
	return len(mcpMarkdownTableCells(line)) == len(mcpMarkdownTableCells(lines[index+1]))
}

// mcpMarkdownTableCells splits a table row into its trimmed cells. The outer
// pipes are optional; an escaped pipe (\|) and pipes inside code spans stay in
// the cell.
func mcpMarkdownTableCells(line string) []string {
	row := strings.TrimSpace(line)
	row = strings.TrimPrefix(row, "|")
	if strings.HasSuffix(row, "|") && !strings.HasSuffix(row, "\\|") {
		row = strings.TrimSuffix(row, "|")
	}
	cells := []string{}
	var cell strings.Builder
	inCode := false
	for index := 0; index < len(row); index++ {
		switch char := row[index]; {
		case char == '\\' && index+1 < len(row) && row[index+1] == '|':
			cell.WriteByte('|')
			index++
		case char == '`':
			inCode = !inCode
			cell.WriteByte(char)
		case char == '|' && !inCode:
			cells = append(cells, strings.TrimSpace(cell.String()))
			cell.Reset()
		default:
			cell.WriteByte(char)
		}
	}
	return append(cells, strings.TrimSpace(cell.String()))
}

// mcpMarkdownTableRow builds a table row of exactly columns cells (the
// header's count): missing cells are empty and extra ones are dropped, as GFM
// renders them.
func mcpMarkdownTableRow(cellType string, cells []string, columns int) map[string]any {
	content := make([]any, 0, columns)
	for column := range columns {
		value := ""
		if column < len(cells) {
			value = cells[column]
		}
		content = append(content, map[string]any{"type": cellType, "content": []any{mcpMarkdownParagraph(value)}})
	}
	return map[string]any{"type": "tableRow", "content": content}
}

func mcpMarkdownParagraphLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return trimmed != "" && !strings.HasPrefix(trimmed, "```") && !strings.HasPrefix(trimmed, ">") && !mcpMarkdownHeading.MatchString(trimmed) && !mcpMarkdownRule.MatchString(trimmed) && !mcpMarkdownBullet.MatchString(line) && !mcpMarkdownOrdered.MatchString(line) && mcpMarkdownMediaBlock(trimmed) == nil
}

// mcpMarkdownMediaBlock is the image, video or file node of a line that holds
// only ![alt](src "title"), ::video[title](src) or
// ::file[title](src){size="" type=""}, or nil. Sources are http(s) URLs or
// paths on this server (such as /uploads/…); anything else stays text.
func mcpMarkdownMediaBlock(trimmed string) map[string]any {
	source := func(value string) bool {
		return strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//")
	}
	if match := mcpMarkdownImage.FindStringSubmatch(trimmed); match != nil && source(match[2]) {
		var title any
		if match[3] != "" {
			title = match[3]
		}
		return map[string]any{"type": "image", "attrs": map[string]any{"src": match[2], "alt": match[1], "title": title}}
	}
	if match := mcpMarkdownVideo.FindStringSubmatch(trimmed); match != nil && source(match[2]) {
		return map[string]any{"type": "video", "attrs": map[string]any{"src": match[2], "title": match[1]}}
	}
	if match := mcpMarkdownFile.FindStringSubmatch(trimmed); match != nil && source(match[2]) {
		var size, contentType any
		if number, err := strconv.ParseInt(match[3], 10, 64); err == nil {
			size = number
		}
		if match[4] != "" {
			contentType = match[4]
		}
		return map[string]any{"type": "file", "attrs": map[string]any{"src": match[2], "title": firstNonEmpty(match[1], "File"), "size": size, "contentType": contentType}}
	}
	return nil
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
