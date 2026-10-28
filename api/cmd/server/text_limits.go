package main

import (
	"fmt"
	"unicode/utf8"

	"flow/api/internal/domain"
)

// textTooLong is a 400 (errInvalid) naming the field and its limit.
func textTooLong(field string, max int) error {
	return fmt.Errorf("%w: %s must be %d characters or fewer", errInvalid, field, max)
}

// checkTextLength refuses a value longer than its VARCHAR column. Lengths are
// characters (runes), as MySQL utf8mb4 and Postgres count them.
func checkTextLength(field, value string, max int) error {
	if utf8.RuneCountInString(value) > max {
		return textTooLong(field, max)
	}
	return nil
}

// csvSourceIDMaxBytes keeps "csv:"+sourceId inside the VARCHAR(191) value of
// issue_attribute_records (ids are ASCII in practice; bytes are the safe count).
const csvSourceIDMaxBytes = 160

// teamKeyMaxLength bounds a new team's key; renaming one is stricter
// (teamIdentifierPattern).
const teamKeyMaxLength = 32

// uploadContentTypeMaxLength bounds a staged upload's media type
// (file_uploads.content_type).
const uploadContentTypeMaxLength = 255

// linkAttachmentTextMaxLength bounds a link attachment's title and subtitle.
const linkAttachmentTextMaxLength = 255

// issueIconMaxLength bounds an issue's icon (bytes).
const issueIconMaxLength = 191

// recurrenceRuleMaxLength bounds a recurrence preset or RRULE passed to a
// Flow tool; the schema check enforces it.
const recurrenceRuleMaxLength = 120

// The Flow tool schemas (flow_mcp_tools.json) advertise these limits as
// maxLength, so MCP clients and the built-in agent's model see them and the
// shared schema check refuses an over-long value before the handler runs;
// flowToolLengthLimits lists them and a test keeps the two in step.
var flowToolLengthLimits = map[string]map[string]int{
	"save_issue":                {"title": domain.IssueTitleMaxLength, "icon": issueIconMaxLength, "recurrence": recurrenceRuleMaxLength},
	"save_template":             {"name": domain.IssueTitleMaxLength, "title": domain.IssueTitleMaxLength},
	"prepare_attachment_upload": {"title": domain.UploadTitleMaxLength, "subtitle": domain.UploadTitleMaxLength, "contentType": uploadContentTypeMaxLength},
	"save_attachment":           {"title": linkAttachmentTextMaxLength, "subtitle": linkAttachmentTextMaxLength},
	"save_team":                 {"key": teamKeyMaxLength},
}

// checkIssueTitle is the issue_records.title limit for every issue write path
// (REST, MCP, CSV import, templates, recurring issues, migrations).
func checkIssueTitle(title string) error {
	return checkTextLength("title", title, domain.IssueTitleMaxLength)
}
