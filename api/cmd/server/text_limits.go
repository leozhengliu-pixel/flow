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

// checkIssueTitle is the issue_records.title limit for every issue write path
// (REST, MCP, CSV import, templates, recurring issues, migrations).
func checkIssueTitle(title string) error {
	return checkTextLength("title", title, domain.IssueTitleMaxLength)
}
