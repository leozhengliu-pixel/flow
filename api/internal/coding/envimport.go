package coding

// .env import (docs/coding-sessions-design.md §18). Owner: track K.
//
// POST /api/coding/environments/{id}/env-import parses a pasted or dropped
// .env file and splits it into plain variables and secret suggestions. The
// parse is a preview: nothing is stored, the editor saves the confirmed lists
// through the normal PATCH and secrets endpoints.

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"flow/api/internal/codingenv"
	"flow/api/internal/domain"
)

// Limits of one import.
const (
	EnvImportMaxBytes   = 256 << 10
	EnvImportMaxEntries = 1000
)

// EnvValueMaxBytes bounds one value (shared with environment validation).
const EnvValueMaxBytes = codingenv.EnvValueMaxBytes

// ErrEnvImportTooLarge reports an import over EnvImportMaxBytes or EnvImportMaxEntries.
var ErrEnvImportTooLarge = errors.New("coding: .env file is too large")

// EnvImportWarning is a line the parser skipped or changed.
type EnvImportWarning struct {
	Line    int    `json:"line"`
	Message string `json:"message"`
}

// EnvImportResult is the preview answered by the env-import route.
type EnvImportResult struct {
	Variables []domain.CodingEnvVar `json:"variables"`
	Secrets   []domain.CodingEnvVar `json:"secrets"`
	Warnings  []EnvImportWarning    `json:"warnings,omitempty"`
}

// ValidEnvName reports whether name is a portable environment variable name.
func ValidEnvName(name string) bool { return codingenv.ValidEnvName(name) }

// ImportDotEnv parses content and classifies the entries.
func ImportDotEnv(content string) (EnvImportResult, error) {
	entries, warnings, err := ParseDotEnv(content)
	if err != nil {
		return EnvImportResult{}, err
	}
	result := EnvImportResult{Variables: []domain.CodingEnvVar{}, Secrets: []domain.CodingEnvVar{}, Warnings: warnings}
	for _, entry := range entries {
		if IsSecretEnv(entry.Name, entry.Value) {
			result.Secrets = append(result.Secrets, entry)
		} else {
			result.Variables = append(result.Variables, entry)
		}
	}
	return result, nil
}

// ParseDotEnv parses a .env file with the usual dotenv rules:
//
//   - blank lines and lines starting with # are ignored;
//   - an optional "export " prefix is dropped;
//   - KEY=VALUE, with optional spaces around "=";
//   - unquoted values end at an inline comment (whitespace then #) and are trimmed;
//   - 'single' and `backtick` quoted values are literal and may span lines;
//   - "double" quoted values may span lines and expand \n \r \t \" \\ and \$;
//   - ${VAR} references are kept literally (no expansion);
//   - a later duplicate key wins (with a warning).
//
// Malformed lines are skipped with a warning; the error is only for input
// over the limits. Entries keep the order of their last definition.
func ParseDotEnv(content string) ([]domain.CodingEnvVar, []EnvImportWarning, error) {
	if len(content) > EnvImportMaxBytes {
		return nil, nil, ErrEnvImportTooLarge
	}
	content = strings.TrimPrefix(content, "\uFEFF")
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	lines := strings.Split(content, "\n")
	var entries []domain.CodingEnvVar
	index := map[string]int{}
	warnings := []EnvImportWarning{}
	warn := func(line int, format string, args ...any) {
		warnings = append(warnings, EnvImportWarning{Line: line, Message: fmt.Sprintf(format, args...)})
	}
	for number := 0; number < len(lines); number++ {
		lineNumber := number + 1
		line := strings.TrimSpace(lines[number])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if rest, ok := cutExport(line); ok {
			line = rest
		}
		name, rest, found := strings.Cut(line, "=")
		name = strings.TrimSpace(name)
		if !found {
			warn(lineNumber, "skipped: expected KEY=VALUE")
			continue
		}
		if !ValidEnvName(name) {
			warn(lineNumber, "skipped: %q is not a valid variable name", truncate(name, 64))
			continue
		}
		rest = strings.TrimLeft(rest, " \t")
		var value string
		if rest != "" && (rest[0] == '"' || rest[0] == '\'' || rest[0] == '`') {
			quote := rest[0]
			body, tail, consumed, ok := readQuoted(quote, rest[1:], lines[number+1:])
			if !ok {
				warn(lineNumber, "skipped %s: unterminated %c quote", name, quote)
				number += consumed
				continue
			}
			number += consumed
			tail = strings.TrimSpace(tail)
			if tail != "" && !strings.HasPrefix(tail, "#") {
				warn(lineNumber, "%s: ignored text after the closing quote", name)
			}
			if quote == '"' {
				body = unescapeDouble(body)
			}
			value = body
		} else {
			value = stripInlineComment(rest)
		}
		if len(value) > EnvValueMaxBytes {
			warn(lineNumber, "skipped %s: value is longer than %d bytes", name, EnvValueMaxBytes)
			continue
		}
		if position, exists := index[name]; exists {
			warn(lineNumber, "%s is defined more than once; the last value is used", name)
			entries = append(entries[:position], entries[position+1:]...)
			for key, at := range index {
				if at > position {
					index[key] = at - 1
				}
			}
		}
		if len(entries) >= EnvImportMaxEntries {
			return nil, nil, ErrEnvImportTooLarge
		}
		index[name] = len(entries)
		entries = append(entries, domain.CodingEnvVar{Name: name, Value: value})
	}
	return entries, warnings, nil
}

func cutExport(line string) (string, bool) {
	if !strings.HasPrefix(line, "export") || len(line) < 7 || (line[6] != ' ' && line[6] != '\t') {
		return line, false
	}
	return strings.TrimLeft(line[6:], " \t"), true
}

// readQuoted reads a quoted value starting after the opening quote; it may
// continue on the following lines. It returns the body, the text after the
// closing quote and how many extra lines it consumed.
func readQuoted(quote byte, first string, following []string) (body, tail string, consumed int, ok bool) {
	var builder strings.Builder
	segment := first
	for {
		if end := closingQuote(quote, segment); end >= 0 {
			builder.WriteString(segment[:end])
			return builder.String(), segment[end+1:], consumed, true
		}
		if consumed >= len(following) {
			return "", "", consumed, false
		}
		builder.WriteString(segment)
		builder.WriteByte('\n')
		segment = following[consumed]
		consumed++
		if builder.Len() > EnvValueMaxBytes {
			return "", "", consumed, false
		}
	}
}

func closingQuote(quote byte, segment string) int {
	for index := 0; index < len(segment); index++ {
		switch {
		case quote == '"' && segment[index] == '\\':
			index++ // skip the escaped character
		case segment[index] == quote:
			return index
		}
	}
	return -1
}

func unescapeDouble(value string) string {
	if !strings.Contains(value, `\`) {
		return value
	}
	var builder strings.Builder
	for index := 0; index < len(value); index++ {
		if value[index] != '\\' || index+1 >= len(value) {
			builder.WriteByte(value[index])
			continue
		}
		index++
		switch value[index] {
		case 'n':
			builder.WriteByte('\n')
		case 'r':
			builder.WriteByte('\r')
		case 't':
			builder.WriteByte('\t')
		case '"', '\\', '$', '`', '\'':
			builder.WriteByte(value[index])
		default:
			builder.WriteByte('\\')
			builder.WriteByte(value[index])
		}
	}
	return builder.String()
}

func stripInlineComment(value string) string {
	for index := 0; index < len(value); index++ {
		if value[index] == '#' && (index == 0 || value[index-1] == ' ' || value[index-1] == '\t') {
			value = value[:index]
			break
		}
	}
	return strings.TrimSpace(value)
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}

var secretNameSuffixes = []string{"_KEY", "_SECRET", "_TOKEN", "_DSN", "_PASS", "_PWD", "_CREDENTIALS", "_CREDENTIAL", "_PRIVATE_KEY", "_CERT_KEY"}

// IsSecretEnv classifies an entry as a secret: names ending in _KEY, _SECRET,
// _TOKEN or _DSN (and similar), names containing PASSWORD or PASSWD, names
// equal to KEY, SECRET, TOKEN or PASSWORD, and URLs (any *_URL, or any value
// that parses as one) that carry credentials (scheme://user:password@host).
func IsSecretEnv(name, value string) bool {
	upper := strings.ToUpper(name)
	switch upper {
	case "KEY", "SECRET", "TOKEN", "PASSWORD", "PASSWD", "DSN", "CREDENTIALS":
		return true
	}
	if strings.Contains(upper, "PASSWORD") || strings.Contains(upper, "PASSWD") || strings.Contains(upper, "SECRET") {
		return true
	}
	for _, suffix := range secretNameSuffixes {
		if strings.HasSuffix(upper, suffix) {
			return true
		}
	}
	return urlHasCredentials(value)
}

func urlHasCredentials(value string) bool {
	value = strings.TrimSpace(value)
	if !strings.Contains(value, "://") || !strings.Contains(value, "@") {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.User == nil {
		return false
	}
	// user:password@ and token@ (a token as the user name) both carry a credential.
	password, _ := parsed.User.Password()
	return password != "" || parsed.User.Username() != ""
}

// SortEnvVars sorts variables by name (stable output for hashing and tests).
func SortEnvVars(vars []domain.CodingEnvVar) []domain.CodingEnvVar {
	sorted := append([]domain.CodingEnvVar(nil), vars...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	return sorted
}
