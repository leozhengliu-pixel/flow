package main

import (
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"flow/api/internal/domain"
)

func formEmbedEnabled(r *http.Request) bool {
	switch strings.ToLower(strings.TrimSpace(r.FormValue("embed"))) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

func applyIssueAttachmentEmbed(data *domain.Bootstrap, issue *domain.Issue, attachment domain.Attachment) error {
	if attachment.URL == "" || attachmentEmbedKind(attachment) == "link" {
		return nil
	}
	if containsAttachmentURL(issue.Description, attachment.URL) {
		return nil
	}
	block := attachmentEmbedMarkdown(attachment)
	if block == "" {
		return nil
	}
	next := appendIssueEmbedMarkdown(issue.Description, block)
	_, err := applyUpdate(data, issue, domain.IssueUpdateInput{Description: &next})
	return err
}

func appendIssueEmbedMarkdown(description, block string) string {
	block = strings.TrimSpace(block)
	if block == "" {
		return description
	}
	trimmed := strings.TrimRight(description, "\n")
	if trimmed == "" {
		return block + "\n"
	}
	return trimmed + "\n\n" + block + "\n"
}

func attachmentEmbedMarkdown(attachment domain.Attachment) string {
	// The label keeps the original file name; the URL takes its directive
	// encoding (spaces, parentheses…), while the attachment keeps its raw URL.
	label := attachmentEmbedLabel(attachment.Title)
	source := encodeDirectiveURL(attachment.URL)
	switch attachmentEmbedKind(attachment) {
	case "image":
		return "![" + label + "](" + source + ")"
	case "video":
		return "::video[" + label + "](" + source + ")"
	case "file":
		return "::file[" + label + "](" + source + "){size=\"" + strconv.FormatInt(attachment.Size, 10) + "\" type=\"" + strings.ReplaceAll(attachment.ContentType, `"`, "") + "\"}"
	default:
		return ""
	}
}

func attachmentEmbedKind(attachment domain.Attachment) string {
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(attachment.ContentType, ";")[0]))
	if contentType == "text/uri-list" {
		return "link"
	}
	if strings.HasPrefix(contentType, "image/") {
		return "image"
	}
	if strings.HasPrefix(contentType, "video/") {
		return "video"
	}
	name := attachment.Title
	if name == "" {
		name = attachment.URL
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".bmp", ".avif", ".ico", ".tif", ".tiff":
		return "image"
	case ".mp4", ".webm", ".mov", ".m4v", ".ogv", ".mkv":
		return "video"
	}
	return "file"
}

func attachmentEmbedLabel(title string) string {
	title = strings.NewReplacer("\r", " ", "\n", " ", "[", "", "]", "").Replace(title)
	title = strings.TrimSpace(title)
	if title == "" {
		return "file"
	}
	return title
}
