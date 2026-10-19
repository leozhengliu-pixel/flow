package gitlab

import (
	"context"
	"errors"
	"testing"

	"flow/api/internal/codehost"
)

func TestCompare(t *testing.T) {
	fake := newFakeGitLab(t)
	host, _ := newTestHost(t, fake, ModeConnection, fixedNow)
	ctx := context.Background()
	fake.project("acme/platform/api").Compare = map[string]map[string]any{"main...flow/fix-1": {
		"commit": map[string]any{"id": "head"},
		"diffs": []map[string]any{
			{"old_path": "a.go", "new_path": "a.go", "diff": "@@ -1,2 +1,3 @@\n a\n-b\n+c\n+d\n"},
			{"old_path": "new.go", "new_path": "new.go", "new_file": true, "diff": "@@ -0,0 +1 @@\n+x\n"},
			{"old_path": "old.go", "new_path": "moved.go", "renamed_file": true, "diff": ""},
			{"old_path": "gone.go", "new_path": "gone.go", "deleted_file": true, "diff": "@@ -1 +0,0 @@\n-y\n"},
			{"old_path": "big.json", "new_path": "big.json", "too_large": true, "diff": "@@ -1 +1 @@\n-q\n+r\n"},
			{"old_path": "logo.png", "new_path": "logo.png", "diff": "Binary files a/logo.png and b/logo.png differ\n"},
		},
	}}
	got, err := host.Compare(ctx, "ws", testRepo(fake), "main", "refs/heads/flow/fix-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.HeadSHA != "head" || len(got.Files) != 6 {
		t.Fatalf("comparison: %+v", got)
	}
	want := []codehost.ComparedFile{
		{Path: "a.go", Status: "modified", Additions: 2, Deletions: 1, Patch: "@@ -1,2 +1,3 @@\n a\n-b\n+c\n+d\n"},
		{Path: "new.go", Status: "added", Additions: 1, Patch: "@@ -0,0 +1 @@\n+x\n"},
		{Path: "moved.go", PreviousPath: "old.go", Status: "renamed"},
		{Path: "gone.go", Status: "deleted", Deletions: 1, Patch: "@@ -1 +0,0 @@\n-y\n"},
		{Path: "big.json", Status: "modified", TooLarge: true},
		{Path: "logo.png", Status: "modified", Binary: true},
	}
	for index, file := range want {
		if got.Files[index] != file {
			t.Errorf("file %d = %+v, want %+v", index, got.Files[index], file)
		}
	}
	if _, err := host.Compare(ctx, "ws", testRepo(fake), "main", "nope"); !errors.Is(err, codehost.ErrNotFound) {
		t.Fatalf("missing ref: %v", err)
	}
}
