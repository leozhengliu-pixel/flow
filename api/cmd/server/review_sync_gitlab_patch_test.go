package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type compareRemote struct {
	body   string
	paths  []string
	failed bool
}

func (c *compareRemote) Provider() string { return "gitlab" }
func (c *compareRemote) REST(_ context.Context, _ string, path string, _, out any) error {
	c.paths = append(c.paths, path)
	if c.failed {
		return errors.New("boom")
	}
	return json.Unmarshal([]byte(c.body), out)
}
func (c *compareRemote) GraphQL(context.Context, string, map[string]any, any) error {
	return errors.New("unused")
}

func TestGitLabEmptyMergeRequestDiffsReadTheCompare(t *testing.T) {
	remote := &compareRemote{body: `{"diffs":[{"new_path":"a.go","diff":"@@ -1 +1,2 @@\n x\n+y\n"}]}`}
	diffs := []gitlabAPIDiff{
		{OldPath: "a.go", NewPath: "a.go"},
		{OldPath: "b.go", NewPath: "b.go", RenamedFile: true},
		{OldPath: "c.go", NewPath: "c.go"},
	}
	fillGitLabMissingPatches(context.Background(), remote, "base", "head", diffs)
	if len(remote.paths) != 1 || !strings.HasPrefix(remote.paths[0], "/repository/compare?") || !strings.Contains(remote.paths[0], "from=base") {
		t.Fatalf("compare paths = %v", remote.paths)
	}
	first, renamed, unknown := gitlabReviewFile(diffs[0]), gitlabReviewFile(diffs[1]), gitlabReviewFile(diffs[2])
	if first.Additions != 1 || first.Patch == "" || first.PatchUnavailable {
		t.Fatalf("compare patch not used: %#v", first)
	}
	if renamed.PatchUnavailable || !unknown.PatchUnavailable {
		t.Fatalf("renamed=%#v unknown=%#v", renamed, unknown)
	}
}

func TestGitLabMissingPatchKeepsLegitimateEmptyDiffs(t *testing.T) {
	remote := &compareRemote{failed: true}
	diffs := []gitlabAPIDiff{
		{NewPath: "empty.txt", NewFile: true},
		{NewPath: "mode.sh", AMode: "100644", BMode: "100755"},
		{NewPath: "yarn.lock", GeneratedFile: true},
	}
	fillGitLabMissingPatches(context.Background(), remote, "base", "head", diffs)
	if len(remote.paths) != 0 {
		t.Fatalf("compare read for files that need none: %v", remote.paths)
	}
	if gitlabReviewFile(diffs[0]).PatchUnavailable || gitlabReviewFile(diffs[1]).PatchUnavailable || !gitlabReviewFile(diffs[2]).PatchUnavailable {
		t.Fatal("unexpected patch availability")
	}
}
