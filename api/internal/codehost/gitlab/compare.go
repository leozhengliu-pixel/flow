package gitlab

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"flow/api/internal/codehost"
)

var _ codehost.Comparer = (*Host)(nil)

type apiCompare struct {
	Commit *struct {
		ID string `json:"id"`
	} `json:"commit"`
	Diffs []struct {
		OldPath     string `json:"old_path"`
		NewPath     string `json:"new_path"`
		Diff        string `json:"diff"`
		NewFile     bool   `json:"new_file"`
		RenamedFile bool   `json:"renamed_file"`
		DeletedFile bool   `json:"deleted_file"`
		TooLarge    bool   `json:"too_large"`
		Collapsed   bool   `json:"collapsed"`
	} `json:"diffs"`
	CompareTimeout bool `json:"compare_timeout"`
}

// Compare reads GET /projects/:id/repository/compare?from=base&to=head
// (straight=false: the changes since the merge base, like a merge request).
func (h *Host) Compare(ctx context.Context, workspaceKey string, repo codehost.RepoRef, base, head string) (codehost.Comparison, error) {
	base, head = strings.TrimPrefix(strings.TrimSpace(base), "refs/heads/"), strings.TrimPrefix(strings.TrimSpace(head), "refs/heads/")
	if base == "" || head == "" {
		return codehost.Comparison{}, errors.New("gitlab: base and head are required")
	}
	_, client, err := h.repoClient(ctx, workspaceKey, repo)
	if err != nil {
		return codehost.Comparison{}, err
	}
	var out apiCompare
	query := url.Values{"from": {base}, "to": {head}, "straight": {"false"}}
	if _, err := client.do(ctx, http.MethodGet, "/projects/"+projectPath(repo)+"/repository/compare?"+query.Encode(), nil, &out); err != nil {
		return codehost.Comparison{}, err
	}
	result := codehost.Comparison{Truncated: out.CompareTimeout}
	if out.Commit != nil {
		result.HeadSHA = out.Commit.ID
	}
	for _, diff := range out.Diffs {
		file := codehost.ComparedFile{Path: diff.NewPath, Status: "modified", TooLarge: diff.TooLarge || diff.Collapsed}
		switch {
		case diff.NewFile:
			file.Status = "added"
		case diff.DeletedFile:
			file.Status, file.Path = "deleted", diff.OldPath
		case diff.RenamedFile:
			file.Status, file.PreviousPath = "renamed", diff.OldPath
		}
		if !file.TooLarge {
			file.Patch = diff.Diff
			if strings.HasPrefix(diff.Diff, "Binary files ") {
				file.Patch, file.Binary = "", true
			}
		}
		for _, hunk := range codehost.ParsePatch(file.Patch) {
			for _, line := range hunk.Lines {
				switch line.Kind {
				case "add":
					file.Additions++
				case "delete":
					file.Deletions++
				}
			}
		}
		result.Files = append(result.Files, file)
	}
	return result, nil
}
