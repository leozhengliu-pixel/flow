package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"flow/api/internal/codehost"
)

var _ codehost.Comparer = (*Host)(nil)

// githubCompareMaxFiles is GitHub's cap on the files a compare returns.
const githubCompareMaxFiles = 300

type apiComparison struct {
	MergeBaseCommit struct {
		SHA string `json:"sha"`
	} `json:"merge_base_commit"`
	Commits []struct {
		SHA string `json:"sha"`
	} `json:"commits"`
	Files []struct {
		Filename         string `json:"filename"`
		PreviousFilename string `json:"previous_filename"`
		Status           string `json:"status"`
		Additions        int    `json:"additions"`
		Deletions        int    `json:"deletions"`
		Changes          int    `json:"changes"`
		Patch            string `json:"patch"`
	} `json:"files"`
}

// Compare reads GET /repos/{owner}/{repo}/compare/{base}...{head} (three
// dots: the changes on head since it left base, what a pull request shows).
func (h *Host) Compare(ctx context.Context, workspaceKey string, repo codehost.RepoRef, base, head string) (codehost.Comparison, error) {
	base, head = strings.TrimPrefix(strings.TrimSpace(base), "refs/heads/"), strings.TrimPrefix(strings.TrimSpace(head), "refs/heads/")
	if base == "" || head == "" {
		return codehost.Comparison{}, errors.New("github: base and head are required")
	}
	token, owner, name, err := h.repoToken(ctx, workspaceKey, repo, permissionsFlow)
	if err != nil {
		return codehost.Comparison{}, err
	}
	var out apiComparison
	err = h.tokenRequest(ctx, "compare", http.MethodGet, repoPath(owner, name)+"/compare/"+refPath(base)+"..."+refPath(head)+"?per_page=100", token, nil, &out)
	if errors.Is(err, codehost.ErrNotFound) {
		return codehost.Comparison{}, fmt.Errorf("%w: compare %s...%s", codehost.ErrNotFound, base, head)
	}
	if err != nil {
		return codehost.Comparison{}, err
	}
	result := codehost.Comparison{BaseSHA: out.MergeBaseCommit.SHA, Truncated: len(out.Files) >= githubCompareMaxFiles}
	if len(out.Commits) > 0 {
		result.HeadSHA = out.Commits[len(out.Commits)-1].SHA
	}
	for _, file := range out.Files {
		status := file.Status
		switch status {
		case "removed":
			status = "deleted"
		case "renamed", "added":
		default:
			status = "modified" // modified, changed, copied, unchanged
		}
		compared := codehost.ComparedFile{Path: file.Filename, PreviousPath: file.PreviousFilename, Status: status, Additions: file.Additions, Deletions: file.Deletions, Patch: file.Patch}
		if file.Patch == "" && file.Changes > 0 {
			// GitHub leaves the patch out for binary files and for very large diffs.
			compared.TooLarge = true
		}
		if file.Patch == "" && file.Changes == 0 && status != "renamed" {
			compared.Binary = true
		}
		result.Files = append(result.Files, compared)
	}
	return result, nil
}
