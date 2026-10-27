package store

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every server write narrowed with WithMetadataFields must use an event on
// the metadata-only allowlist. Otherwise the store ignores the field scope,
// loads every issue and content record and replaces them, which once
// deleted issues other servers created while an agent session archive was
// being audited.
func TestMetadataFieldScopedServerWritesUseMetadataOnlyEvents(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "cmd", "server", "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("server sources: %v", err)
	}
	literal := regexp.MustCompile(`^"([a-z_]+\.[a-z_.]+)"$`)
	checked := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		source := string(raw)
		for offset := 0; ; {
			index := strings.Index(source[offset:], "store.WithMetadataFields(")
			if index < 0 {
				break
			}
			start := offset + index
			offset = start + 1
			// The write that uses the scoped context, in the same function.
			body := source[start:]
			if end := strings.Index(body, "\nfunc "); end >= 0 {
				body = body[:end]
			}
			call := strings.Index(body, "MutateWorkspace")
			if call < 0 {
				continue // a helper that returns the scoped context
			}
			open := strings.Index(body[call:], "(")
			args := splitCallArgs(body[call+open+1:])
			if len(args) < 3 {
				t.Errorf("%s: cannot read the event of a field-scoped write", file)
				continue
			}
			events := []string{}
			if match := literal.FindStringSubmatch(args[2]); match != nil {
				events = append(events, match[1])
			} else {
				// An event chosen into a variable before the write.
				assignment := regexp.MustCompile(regexp.QuoteMeta(args[2]) + `\s*:?=\s*"([a-z_]+\.[a-z_.]+)"`)
				for _, match := range assignment.FindAllStringSubmatch(body[:call], -1) {
					events = append(events, match[1])
				}
			}
			if len(events) == 0 {
				t.Errorf("%s: field-scoped write with event %s that this test cannot resolve", file, args[2])
			}
			for _, event := range events {
				checked++
				if !metadataOnlyMutation(event, nil) && !metadataTeamMutation(event, nil) {
					t.Errorf("%s: %s is written with WithMetadataFields but is not on the metadata-only allowlist", file, event)
				}
			}
		}
	}
	if checked < 10 {
		t.Fatalf("checked only %d field-scoped writes", checked)
	}
}

// splitCallArgs splits the arguments of a call whose opening parenthesis
// precedes source, stopping at its closing parenthesis or the callback.
func splitCallArgs(source string) []string {
	args, depth, start := []string{}, 0, 0
	inString := false
	for index := 0; index < len(source); index++ {
		switch char := source[index]; {
		case char == '"' && (index == 0 || source[index-1] != '\\'):
			inString = !inString
		case inString:
		case char == '(' || char == '{' || char == '[':
			depth++
		case char == ')' || char == '}' || char == ']':
			if depth == 0 {
				return append(args, strings.TrimSpace(source[start:index]))
			}
			depth--
		case char == ',' && depth == 0:
			args = append(args, strings.TrimSpace(source[start:index]))
			start = index + 1
			if len(args) == 4 {
				return args
			}
		}
	}
	return args
}
