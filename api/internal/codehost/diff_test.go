package codehost

import (
	"reflect"
	"testing"
)

func TestParsePatch(t *testing.T) {
	tests := []struct {
		name  string
		patch string
		want  []DiffHunk
	}{
		{name: "empty", patch: "", want: nil},
		{name: "modify", patch: "@@ -10,3 +10,4 @@ func main() {\n a\n-b\n+c\n+d\n e\n", want: []DiffHunk{{
			Header: "@@ -10,3 +10,4 @@ func main() {", OldStart: 10, OldLines: 3, NewStart: 10, NewLines: 4,
			Lines: []DiffLine{{Kind: "context", OldLine: 10, NewLine: 10, Text: "a"}, {Kind: "delete", OldLine: 11, Text: "b"}, {Kind: "add", NewLine: 11, Text: "c"}, {Kind: "add", NewLine: 12, Text: "d"}, {Kind: "context", OldLine: 12, NewLine: 13, Text: "e"}},
		}}},
		{name: "single-line counts and no newline note", patch: "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -0,0 +1 @@\n+x\n\\ No newline at end of file", want: []DiffHunk{{
			Header: "@@ -0,0 +1 @@", OldStart: 0, OldLines: 0, NewStart: 1, NewLines: 1,
			Lines: []DiffLine{{Kind: "add", NewLine: 1, Text: "x"}, {Kind: "note", Text: "No newline at end of file"}},
		}}},
		{name: "two hunks with CRLF", patch: "@@ -1 +1 @@\r\n-a\r\n+b\r\n@@ -9,2 +9,2 @@\r\n c\r\n-d\r\n+e\r\n", want: []DiffHunk{
			{Header: "@@ -1 +1 @@", OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 1, Lines: []DiffLine{{Kind: "delete", OldLine: 1, Text: "a"}, {Kind: "add", NewLine: 1, Text: "b"}}},
			{Header: "@@ -9,2 +9,2 @@", OldStart: 9, OldLines: 2, NewStart: 9, NewLines: 2, Lines: []DiffLine{{Kind: "context", OldLine: 9, NewLine: 9, Text: "c"}, {Kind: "delete", OldLine: 10, Text: "d"}, {Kind: "add", NewLine: 10, Text: "e"}}},
		}},
		{name: "malformed header", patch: "@@ nonsense\n+a\n", want: []DiffHunk{{Header: "@@ nonsense", OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 1, Lines: []DiffLine{{Kind: "add", NewLine: 1, Text: "a"}}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ParsePatch(test.patch); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("ParsePatch:\n got %+v\nwant %+v", got, test.want)
			}
		})
	}
}
