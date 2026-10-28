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
// (file_uploads.content_type, VARCHAR(255): characters on MySQL utf8mb4 and
// Postgres).
const uploadContentTypeMaxLength = 255

// linkAttachmentTextMaxLength bounds a link attachment's title and subtitle.
const linkAttachmentTextMaxLength = 255

// issueIconMaxLength bounds an issue's icon in characters (it is kept in the
// issue's JSON data; the tool schema's maxLength counts characters too).
const issueIconMaxLength = 191

// checkIssueIcon is the icon limit of every issue write path.
func checkIssueIcon(icon string) error {
	return checkTextLength("icon", icon, issueIconMaxLength)
}

// uploadContentTypeTooLong reports a staged upload media type over its
// limit, counted in characters like the schema's maxLength and the column.
func uploadContentTypeTooLong(contentType string) bool {
	return utf8.RuneCountInString(contentType) > uploadContentTypeMaxLength
}

// recurrenceRuleMaxLength bounds a recurrence preset or RRULE passed to a
// Flow tool; the schema check enforces it.
const recurrenceRuleMaxLength = 120

// The Flow tool schemas (flow_mcp_tools.json) advertise these limits as
// maxLength, so MCP clients and the built-in agent's model see them and the
// shared schema check refuses an over-long value before the handler runs;
// flowToolLengthLimits lists them with the check the server runs on the same
// value, and a test keeps the schema, the limit and the check in step: both
// count characters (runes), so a value at the limit passes both and one
// character over fails both. A nil check means the schema is the only check.
var flowToolLengthLimits = map[string]map[string]textLimit{
	"save_issue":                {"title": {domain.IssueTitleMaxLength, checkIssueTitle}, "icon": {issueIconMaxLength, checkIssueIcon}, "recurrence": {recurrenceRuleMaxLength, nil}},
	"save_template":             {"name": {domain.IssueTitleMaxLength, checkTemplateName}, "title": {domain.IssueTitleMaxLength, checkIssueTitle}},
	"prepare_attachment_upload": {"title": {domain.UploadTitleMaxLength, checkUploadTitle}, "subtitle": {domain.UploadTitleMaxLength, checkUploadTitle}, "contentType": {uploadContentTypeMaxLength, checkUploadContentType}},
	"save_attachment":           {"title": {linkAttachmentTextMaxLength, checkLinkAttachmentText}, "subtitle": {linkAttachmentTextMaxLength, checkLinkAttachmentText}},
	"save_team":                 {"key": {teamKeyMaxLength, checkNewTeamKey}},
}

// textLimit is a character limit and the server check that enforces it.
type textLimit struct {
	max   int
	check func(string) error
}

func checkTemplateName(name string) error {
	return checkTextLength("name", name, domain.IssueTitleMaxLength)
}

// checkUploadTitle bounds a staged upload's title or subtitle.
func checkUploadTitle(value string) error {
	return checkTextLength("title", value, domain.UploadTitleMaxLength)
}

func checkUploadContentType(contentType string) error {
	if uploadContentTypeTooLong(contentType) {
		return textTooLong("contentType", uploadContentTypeMaxLength)
	}
	return nil
}

// checkLinkAttachmentText bounds a link attachment's title or subtitle.
func checkLinkAttachmentText(value string) error {
	return checkTextLength("title", value, linkAttachmentTextMaxLength)
}

// checkNewTeamKey bounds a new team's key.
func checkNewTeamKey(key string) error {
	return checkTextLength("key", key, teamKeyMaxLength)
}

// checkIssueTitle is the issue_records.title limit for every issue write path
// (REST, MCP, CSV import, templates, recurring issues, migrations).
func checkIssueTitle(title string) error {
	return checkTextLength("title", title, domain.IssueTitleMaxLength)
}
