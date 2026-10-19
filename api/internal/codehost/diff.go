package codehost

import (
	"context"
	"strconv"
	"strings"
)

// Comparer is implemented by hosts that can compare two refs (GitHub's
// compare API, GitLab's repository compare). It is optional: callers type
// assert a Host to it, so hosts without it keep satisfying Host.
type Comparer interface {
	// Compare returns the files changed between base and head (the merge
	// base of the two, like `git diff base...head`), with their patches.
	Compare(ctx context.Context, workspaceKey string, repo RepoRef, base, head string) (Comparison, error)
}

// Comparison is the change set between two refs.
type Comparison struct {
	BaseSHA string
	HeadSHA string
	Files   []ComparedFile
	// Truncated: the host returned only part of the files (GitHub caps
	// compare at 300 files; GitLab marks collapsed or overflowing diffs).
	Truncated bool
}

// ComparedFile is one changed file with its unified diff hunks text.
type ComparedFile struct {
	Path         string
	PreviousPath string
	Status       string // added | modified | deleted | renamed
	Additions    int
	Deletions    int
	// Patch is the unified diff body (from the first "@@" line); empty for a
	// binary file or one the host left out because it is too large.
	Patch  string
	Binary bool
	// TooLarge: the host did not include the patch because of its size.
	TooLarge bool
}

// DiffLine is one line of a hunk.
type DiffLine struct {
	Kind    string `json:"kind"` // context | add | delete | note
	OldLine int    `json:"oldLine,omitempty"`
	NewLine int    `json:"newLine,omitempty"`
	Text    string `json:"text"`
}

// DiffHunk is one "@@ -a,b +c,d @@" section of a patch.
type DiffHunk struct {
	Header   string     `json:"header"`
	OldStart int        `json:"oldStart"`
	OldLines int        `json:"oldLines"`
	NewStart int        `json:"newStart"`
	NewLines int        `json:"newLines"`
	Lines    []DiffLine `json:"lines"`
}

// ParsePatch splits a unified diff body into hunks with old and new line
// numbers. Lines before the first hunk header (a git "diff --git" preamble)
// are skipped; "\ No newline at end of file" becomes a note line. It never
// fails: malformed headers start a hunk at line 1.
func ParsePatch(patch string) []DiffHunk {
	if patch == "" {
		return nil
	}
	var hunks []DiffHunk
	var current *DiffHunk
	oldLine, newLine := 0, 0
	lines := strings.Split(strings.ReplaceAll(patch, "\r\n", "\n"), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "@@") {
			hunk := parseHunkHeader(line)
			hunks = append(hunks, hunk)
			current = &hunks[len(hunks)-1]
			oldLine, newLine = hunk.OldStart, hunk.NewStart
			continue
		}
		if current == nil {
			continue
		}
		switch {
		case strings.HasPrefix(line, "+"):
			current.Lines = append(current.Lines, DiffLine{Kind: "add", NewLine: newLine, Text: line[1:]})
			newLine++
		case strings.HasPrefix(line, "-"):
			current.Lines = append(current.Lines, DiffLine{Kind: "delete", OldLine: oldLine, Text: line[1:]})
			oldLine++
		case strings.HasPrefix(line, `\`):
			current.Lines = append(current.Lines, DiffLine{Kind: "note", Text: strings.TrimSpace(strings.TrimPrefix(line, `\`))})
		default:
			text := line
			if strings.HasPrefix(text, " ") {
				text = text[1:]
			}
			current.Lines = append(current.Lines, DiffLine{Kind: "context", OldLine: oldLine, NewLine: newLine, Text: text})
			oldLine++
			newLine++
		}
	}
	return hunks
}

// parseHunkHeader reads "@@ -oldStart[,oldLines] +newStart[,newLines] @@ section".
func parseHunkHeader(line string) DiffHunk {
	hunk := DiffHunk{Header: line, OldStart: 1, NewStart: 1, OldLines: 1, NewLines: 1}
	body := strings.TrimPrefix(line, "@@")
	end := strings.Index(body, "@@")
	if end < 0 {
		return hunk
	}
	for _, field := range strings.Fields(body[:end]) {
		if len(field) < 2 || (field[0] != '-' && field[0] != '+') {
			continue
		}
		start, count := field[1:], "1"
		if before, after, ok := strings.Cut(start, ","); ok {
			start, count = before, after
		}
		s, errStart := strconv.Atoi(start)
		c, errCount := strconv.Atoi(count)
		if errStart != nil || errCount != nil {
			continue
		}
		if field[0] == '-' {
			hunk.OldStart, hunk.OldLines = s, c
		} else {
			hunk.NewStart, hunk.NewLines = s, c
		}
	}
	return hunk
}
