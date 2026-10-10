package main

import "strings"

// Documents start untitled (the editor shows a placeholder). Everything that
// reads a title for display falls back to this name.
const untitledDocumentTitle = "Untitled document"

// documentDisplayTitle is the title shown in search results, mentions,
// notifications, resources and agent context.
func documentDisplayTitle(title string) string {
	if title = strings.TrimSpace(title); title != "" {
		return title
	}
	return untitledDocumentTitle
}
