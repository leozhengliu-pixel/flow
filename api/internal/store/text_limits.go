package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"flow/api/internal/domain"
)

// ErrInvalidValue is behind every value the store refuses before writing
// (a 400 whose message is written for the person who sent it).
var ErrInvalidValue = errors.New("invalid value")

// invalidValueError is an ErrInvalidValue with its own message.
type invalidValueError struct{ message string }

func (e *invalidValueError) Error() string { return e.message }
func (e *invalidValueError) Unwrap() error { return ErrInvalidValue }

func invalidValue(message string) error { return &invalidValueError{message: message} }

// ValueTooLongError is a value longer than the VARCHAR column that holds it.
// Handlers check their input first; this is the last line before MySQL or
// Postgres would refuse the write, and it maps to a 400 with Field's name.
type ValueTooLongError struct {
	Field string
	Max   int
}

func (e *ValueTooLongError) Error() string {
	return fmt.Sprintf("%s must be %d characters or fewer", e.Field, e.Max)
}

func (e *ValueTooLongError) Unwrap() error { return ErrInvalidValue }

// issueRecordFits checks the issue columns with a declared size. SQLite does
// not enforce VARCHAR sizes, so a SQLite workspace keeps rewriting issues
// whose titles predate the handler checks.
func (s *SQLiteStore) issueRecordFits(issue domain.Issue) error {
	if s.dialect == "sqlite" || s.dialect == "" {
		return nil
	}
	if utf8.RuneCountInString(issue.Title) > domain.IssueTitleMaxLength {
		return &ValueTooLongError{Field: "title", Max: domain.IssueTitleMaxLength}
	}
	return nil
}

// textField is one value and the size of its column, in characters.
type textField struct {
	name  string
	value string
	max   int
}

// checkTextFields returns a ValueTooLongError for the first field longer
// than its column.
func checkTextFields(fields ...textField) error {
	for _, field := range fields {
		if utf8.RuneCountInString(field.value) > field.max {
			return &ValueTooLongError{Field: field.name, Max: field.max}
		}
	}
	return nil
}

// checkSCIMUserLengths refuses SCIM user values longer than their columns
// (auth_identities.username/subject, auth_users.name/display_name/email/
// avatar_url) as ErrSCIMInvalid, a SCIM 400 invalidValue, on every dialect.
func checkSCIMUserLengths(input SCIMUserInput) error {
	if err := checkTextFields(
		textField{"userName", input.UserName, domain.SCIMValueMaxLength},
		textField{"externalId", input.ExternalID, domain.SCIMValueMaxLength},
		textField{"displayName", input.DisplayName, domain.UserNameMaxLength},
		textField{"name", input.Name.FullName(), domain.UserNameMaxLength},
		textField{"emails", input.Email, domain.EmailMaxLength},
		textField{"photos", input.AvatarURL, domain.AvatarURLMaxLength},
	); err != nil {
		return fmt.Errorf("%w: %s", ErrSCIMInvalid, err.Error())
	}
	return nil
}

// checkSCIMGroupLengths is checkSCIMUserLengths for scim_groups.
func checkSCIMGroupLengths(externalID, displayName string) error {
	if utf8.RuneCountInString(strings.TrimSpace(displayName)) > domain.SCIMValueMaxLength {
		return fmt.Errorf("%w: displayName must be %d characters or fewer", ErrSCIMInvalid, domain.SCIMValueMaxLength)
	}
	if utf8.RuneCountInString(strings.TrimSpace(externalID)) > domain.SCIMValueMaxLength {
		return fmt.Errorf("%w: externalId must be %d characters or fewer", ErrSCIMInvalid, domain.SCIMValueMaxLength)
	}
	return nil
}

// issueRecordColumns is issueRecordValues for a write: the issue must fit
// its columns first.
func (s *SQLiteStore) issueRecordColumns(workspace string, issue domain.Issue) ([]any, error) {
	if err := s.issueRecordFits(issue); err != nil {
		return nil, err
	}
	return issueRecordValues(workspace, issue)
}

// eventAggregateKey is the domain_events.aggregate_id stored for id: a
// VARCHAR(191) on MySQL, which some events fill with text a person typed (a
// list of invited emails, an external resource id). An id over 191 bytes
// keeps a readable prefix and a digest of the whole, so the write never fails
// and lookups by the same id (EventsPage, Events) still match.
func eventAggregateKey(id string) string {
	if len(id) <= 191 && utf8.ValidString(id) && !strings.ContainsRune(id, 0) {
		return id
	}
	sum := sha256.Sum256([]byte(id))
	return storableText(id, 120) + "#" + hex.EncodeToString(sum[:])
}

// TruncateRunes cuts value to at most max characters without splitting one.
func TruncateRunes(value string, max int) string {
	if max <= 0 {
		return ""
	}
	if utf8.RuneCountInString(value) <= max {
		return value
	}
	count := 0
	for index := range value {
		if count == max {
			return value[:index]
		}
		count++
	}
	return value
}

// storableText makes text read from an external system (a webhook
// receiver's response, a worker's error) safe for a text column: invalid
// UTF-8 becomes U+FFFD, NUL (refused by Postgres) is dropped, and the result
// is at most maxBytes bytes, cut on a character boundary.
func storableText(value string, maxBytes int) string {
	value = strings.ToValidUTF8(value, "�")
	value = strings.ReplaceAll(value, "\x00", "")
	if len(value) <= maxBytes {
		return value
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
}
