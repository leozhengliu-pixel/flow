package github

import (
	"errors"
	"net/http"
	"testing"

	"flow/api/internal/codehost"
)

func TestCompare(t *testing.T) {
	fake := newFakeGitHub(t)
	host, _ := newTestHost(t, fake, nil)
	ctx := t.Context()
	fake.setOverride("GET /repos/acme/app/compare/main...flow/fix-1", func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") == "" {
			t.Errorf("compare must authenticate")
		}
		writeJSONResponse(w, http.StatusOK, map[string]any{
			"merge_base_commit": map[string]any{"sha": "base"},
			"commits":           []map[string]any{{"sha": "one"}, {"sha": "head"}},
			"files": []map[string]any{
				{"filename": "src/a.ts", "status": "modified", "additions": 1, "deletions": 1, "changes": 2, "patch": "@@ -1,2 +1,2 @@\n-a\n+b\n c"},
				{"filename": "src/new.ts", "previous_filename": "src/old.ts", "status": "renamed", "changes": 0},
				{"filename": "img.png", "status": "added", "changes": 0},
				{"filename": "gone.txt", "status": "removed", "deletions": 3, "changes": 3},
			},
		})
		return true
	})
	got, err := host.Compare(ctx, "ws", appRepo, "refs/heads/main", "flow/fix-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.BaseSHA != "base" || got.HeadSHA != "head" || got.Truncated || len(got.Files) != 4 {
		t.Fatalf("comparison: %+v", got)
	}
	want := []codehost.ComparedFile{
		{Path: "src/a.ts", Status: "modified", Additions: 1, Deletions: 1, Patch: "@@ -1,2 +1,2 @@\n-a\n+b\n c"},
		{Path: "src/new.ts", PreviousPath: "src/old.ts", Status: "renamed"},
		{Path: "img.png", Status: "added", Binary: true},
		{Path: "gone.txt", Status: "deleted", Deletions: 3, TooLarge: true},
	}
	for index, file := range want {
		if got.Files[index] != file {
			t.Errorf("file %d = %+v, want %+v", index, got.Files[index], file)
		}
	}
	if _, err := host.Compare(ctx, "ws", appRepo, "main", "missing"); !errors.Is(err, codehost.ErrNotFound) {
		t.Fatalf("missing ref: %v", err)
	}
	if _, err := host.Compare(ctx, "ws", appRepo, "", "x"); err == nil {
		t.Fatal("an empty base must fail")
	}
}
