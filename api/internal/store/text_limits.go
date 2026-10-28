package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
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
func eventAggregateKey(id string) string { return indexKey(id) }

// indexKeyMaxBytes is the size of the VARCHAR(191) key columns. Bytes are
// the safe count: a value of at most 191 bytes has at most 191 characters.
const indexKeyMaxBytes = 191

// indexKey is the value stored in a VARCHAR(191) key column for text that
// is usually short but has no bound of its own (an id from an import, a
// field name built from one). Text that fits, is valid UTF-8 and has no NUL
// is stored as it is; anything else becomes a readable prefix plus a SHA-256
// digest of the whole (120 + 1 + 64 bytes), so the write never fails and a
// lookup that passes the same text through indexKey still matches.
func indexKey(text string) string {
	if len(text) <= indexKeyMaxBytes && utf8.ValidString(text) && !strings.ContainsRune(text, 0) {
		return text
	}
	sum := sha256.Sum256([]byte(text))
	return storableText(text, 120) + "#" + hex.EncodeToString(sum[:])
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

// jsonNULEscape is how JSON writes U+0000 inside a string.
var jsonNULEscape = []byte(`\u0000`)

// StripJSONNUL removes U+0000 (\u0000 escapes) from the strings of a JSON
// document; every other byte is kept, and an escaped backslash followed by
// the text "u0000" is text, not NUL. PostgreSQL refuses \u0000 when it parses
// JSON (unsupported Unicode escape sequence), so one stored row carrying it
// would fail every query that reads that column as JSON. raw must be JSON;
// it is returned as it is when it has no \u0000.
func StripJSONNUL(raw []byte) []byte {
	if !bytes.Contains(raw, jsonNULEscape) {
		return raw
	}
	out := make([]byte, 0, len(raw))
	inString := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch {
		case !inString:
			inString = c == '"'
		case c == '"':
			inString = false
		case c == '\\' && i+1 < len(raw):
			if bytes.HasPrefix(raw[i:], jsonNULEscape) {
				i += len(jsonNULEscape) - 1
				continue
			}
			out = append(out, c, raw[i+1])
			i++
			continue
		}
		out = append(out, c)
	}
	return out
}

// StripNUL removes U+0000 from text. PostgreSQL refuses it in text and
// varchar values (invalid byte sequence for encoding "UTF8": 0x00), and it
// has no meaning in names, titles or bodies.
func StripNUL(text string) string {
	if strings.IndexByte(text, 0) < 0 {
		return text
	}
	return strings.ReplaceAll(text, "\x00", "")
}

// textArgs drops NUL from the string arguments of a PostgreSQL statement,
// the last line for text that did not come through a JSON API body (CSV
// cells, query strings, mail, external payloads): PostgreSQL refuses NUL in
// any text or varchar value, a query's parameters included, so one NUL
// would fail the statement with SQLSTATE 22021. MySQL and SQLite store NUL
// and are left alone. Bytes ([]byte, BYTEA columns) are never changed.
func textArgs(dialect string, args []any) []any {
	if dialect != "postgres" {
		return args
	}
	var cleaned []any
	for i, arg := range args {
		text, ok := arg.(string)
		if !ok || strings.IndexByte(text, 0) < 0 {
			continue
		}
		if cleaned == nil {
			cleaned = slices.Clone(args)
		}
		cleaned[i] = StripNUL(text)
	}
	if cleaned == nil {
		return args
	}
	return cleaned
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
