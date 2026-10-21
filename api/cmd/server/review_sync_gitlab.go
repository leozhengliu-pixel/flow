package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
)

type gitlabAPIDiff struct {
	OldPath     string `json:"old_path"`
	NewPath     string `json:"new_path"`
	NewFile     bool   `json:"new_file"`
	RenamedFile bool   `json:"renamed_file"`
	DeletedFile bool   `json:"deleted_file"`
	Diff        string `json:"diff"`
	TooLarge    bool   `json:"too_large"`
	Collapsed   bool   `json:"collapsed"`
	// GeneratedFile: GitLab hides the hunks of generated files (lockfiles).
	GeneratedFile bool   `json:"generated_file"`
	AMode         string `json:"a_mode"`
	BMode         string `json:"b_mode"`
}

type gitlabAPIMergeRequest struct {
	Title        string `json:"title"`
	Description  string `json:"description"`
	State        string `json:"state"`
	Draft        bool   `json:"draft"`
	WorkInProg   bool   `json:"work_in_progress"`
	WebURL       string `json:"web_url"`
	SourceBranch string `json:"source_branch"`
	TargetBranch string `json:"target_branch"`
	SHA          string `json:"sha"`
	DiffRefs     *struct {
		BaseSHA  string `json:"base_sha"`
		HeadSHA  string `json:"head_sha"`
		StartSHA string `json:"start_sha"`
	} `json:"diff_refs"`
}

type gitlabAPINote struct {
	ID     int64  `json:"id"`
	Type   string `json:"type"`
	Body   string `json:"body"`
	System bool   `json:"system"`
	Author struct {
		Username  string `json:"username"`
		Name      string `json:"name"`
		AvatarURL string `json:"avatar_url"`
	} `json:"author"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	Resolvable bool      `json:"resolvable"`
	Resolved   bool      `json:"resolved"`
	Position   *struct {
		NewPath string `json:"new_path"`
		OldPath string `json:"old_path"`
		NewLine *int   `json:"new_line"`
		OldLine *int   `json:"old_line"`
		HeadSHA string `json:"head_sha"`
	} `json:"position"`
}

type gitlabAPIDiscussion struct {
	ID             string          `json:"id"`
	IndividualNote bool            `json:"individual_note"`
	Notes          []gitlabAPINote `json:"notes"`
}

// gitlabReviewFile maps an MR diff: new_file / deleted_file / renamed_file
// with old_path; GitLab sends hunks without file headers and no counts.
func gitlabReviewFile(diff gitlabAPIDiff) domain.ReviewFile {
	file := domain.ReviewFile{Path: diff.NewPath, Status: "modified", Patch: diff.Diff}
	switch {
	case diff.NewFile:
		file.Status = "added"
	case diff.DeletedFile:
		file.Status, file.Path = "deleted", cmpOr(diff.NewPath, diff.OldPath)
	case diff.RenamedFile:
		file.Status, file.PreviousPath = "renamed", diff.OldPath
	}
	for _, line := range strings.Split(diff.Diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
		case strings.HasPrefix(line, "+"):
			file.Additions++
		case strings.HasPrefix(line, "-"):
			file.Deletions++
		}
	}
	file.PatchUnavailable = diff.Diff == "" && (diff.TooLarge || diff.Collapsed || diff.GeneratedFile)
	return file
}

// gitlabPatchMissing: the diff of a content change came back empty without a
// reason (not collapsed or too large, not a rename or mode change, not an
// empty file added or deleted). GitLab does this while it is still computing
// the merge request diff, so the patch is read again from the compare API.
func gitlabPatchMissing(diff gitlabAPIDiff) bool {
	if diff.Diff != "" || diff.TooLarge || diff.Collapsed || diff.GeneratedFile {
		return false
	}
	if diff.RenamedFile || diff.NewFile || diff.DeletedFile {
		return false
	}
	return diff.AMode == "" || diff.AMode == diff.BMode
}

// fillGitLabMissingPatches reads the compare of the merge request's diff refs
// and takes its patch for every file whose merge request diff was empty. A
// file that still has none afterwards is marked patch-unavailable so the
// review says so instead of showing an empty diff. A failing compare leaves
// the files as read.
func fillGitLabMissingPatches(ctx context.Context, remote reviewRemote, baseSHA, headSHA string, diffs []gitlabAPIDiff) {
	missing := false
	for _, diff := range diffs {
		missing = missing || gitlabPatchMissing(diff)
	}
	if !missing {
		return
	}
	if baseSHA != "" && headSHA != "" {
		var compare struct {
			Diffs []gitlabAPIDiff `json:"diffs"`
		}
		query := url.Values{"from": {baseSHA}, "to": {headSHA}, "straight": {"true"}}
		if remote.REST(ctx, http.MethodGet, "/repository/compare?"+query.Encode(), nil, &compare) == nil {
			byPath := map[string]string{}
			for _, item := range compare.Diffs {
				if item.Diff != "" {
					byPath[item.NewPath] = item.Diff
				}
			}
			for index := range diffs {
				if patch, ok := byPath[diffs[index].NewPath]; ok && gitlabPatchMissing(diffs[index]) {
					diffs[index].Diff = patch
				}
			}
		}
	}
	for index := range diffs {
		if gitlabPatchMissing(diffs[index]) {
			diffs[index].Collapsed = true
		}
	}
}

func cmpOr(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// gitlabDiscussionComments maps a discussion: its first user note is the
// thread root, later notes reply to it, and the root carries the
// discussion's resolution.
func gitlabDiscussionComments(discussion gitlabAPIDiscussion) []remoteReviewComment {
	result := []remoteReviewComment{}
	rootID := ""
	var resolved *bool
	for _, note := range discussion.Notes {
		if note.System {
			continue
		}
		comment := remoteReviewComment{Kind: reviewRemoteNote, ID: strconv.FormatInt(note.ID, 10), ThreadID: discussion.ID, Type: "commented", Body: note.Body,
			Author: remoteReviewAuthor{Login: note.Author.Username, Name: note.Author.Name, AvatarURL: note.Author.AvatarURL}, CreatedAt: note.CreatedAt, UpdatedAt: note.UpdatedAt}
		if position := note.Position; position != nil {
			comment.Path, comment.CommitID = cmpOr(position.NewPath, position.OldPath), position.HeadSHA
			switch {
			case position.NewLine != nil:
				comment.Line, comment.Side = *position.NewLine, "RIGHT"
			case position.OldLine != nil:
				comment.Line, comment.Side = *position.OldLine, "LEFT"
			}
		}
		if rootID == "" {
			rootID = comment.ID
			if note.Resolvable {
				value := note.Resolved
				resolved = &value
			}
		} else {
			comment.ParentKind, comment.ParentID = reviewRemoteNote, rootID
		}
		comment.Resolved = resolved
		result = append(result, comment)
	}
	return result
}

// Some GitLab instances answer HTTP 500 on the merge request diffs endpoint
// for per_page above 30, so it is read in smaller pages (the budget still
// covers 3000 changed files). Other GitLab lists keep the default page size.
const (
	gitlabDiffsPageSize = 20
	gitlabDiffsMaxPages = 150
)

// fetchGitLabReviewSnapshot reads the merge request (diff refs), its diffs
// and its discussions.
func fetchGitLabReviewSnapshot(ctx context.Context, remote reviewRemote, review domain.CodeReview) (reviewSnapshot, error) {
	base := "/merge_requests/" + strconv.Itoa(review.Number)
	var request gitlabAPIMergeRequest
	if err := remote.REST(ctx, http.MethodGet, base, nil, &request); err != nil {
		return reviewSnapshot{}, err
	}
	diffs, truncated, err := fetchPagesSized[gitlabAPIDiff](ctx, remote, base+"/diffs", gitlabDiffsPageSize, gitlabDiffsMaxPages)
	if err == nil && truncated {
		// Never mirror a partial file list as the whole merge request.
		err = fmt.Errorf("%w (read %d files in %d pages of %d, more remain)", errReviewTooManyFiles, len(diffs), gitlabDiffsMaxPages, gitlabDiffsPageSize)
	}
	if reviewRemoteStatus(err) == http.StatusNotFound {
		// GitLab before 15.7: the single-response changes endpoint.
		var changes struct {
			Changes []gitlabAPIDiff `json:"changes"`
		}
		err = remote.REST(ctx, http.MethodGet, base+"/changes", nil, &changes)
		diffs = changes.Changes
	}
	if err != nil {
		return reviewSnapshot{}, fmt.Errorf("merge request diffs: %w", err)
	}
	discussions, err := fetchPages[gitlabAPIDiscussion](ctx, remote, base+"/discussions", 50)
	if err != nil {
		return reviewSnapshot{}, fmt.Errorf("merge request discussions: %w", err)
	}
	snapshot := reviewSnapshot{Provider: domain.CodeHostGitLab, HeadSHA: request.SHA, Complete: true, Files: make([]domain.ReviewFile, 0, len(diffs)),
		Pull: &remotePullRequest{Title: request.Title, Description: request.Description, BaseBranch: request.TargetBranch, HeadBranch: request.SourceBranch, URL: request.WebURL,
			Draft: request.Draft || request.WorkInProg, Merged: request.State == "merged", Closed: request.State == "closed" || request.State == "locked"}}
	if refs := request.DiffRefs; refs != nil {
		snapshot.HeadSHA, snapshot.BaseSHA, snapshot.StartSHA = cmpOr(refs.HeadSHA, request.SHA), refs.BaseSHA, refs.StartSHA
	}
	fillGitLabMissingPatches(ctx, remote, snapshot.BaseSHA, snapshot.HeadSHA, diffs)
	for _, diff := range diffs {
		snapshot.Files = append(snapshot.Files, gitlabReviewFile(diff))
	}
	// The commit count is best effort: a failing list leaves it unknown.
	if commits, err := fetchPages[struct{}](ctx, remote, base+"/commits", 10); err == nil {
		snapshot.Pull.Commits = len(commits)
	}
	for _, discussion := range discussions {
		snapshot.Comments = append(snapshot.Comments, gitlabDiscussionComments(discussion)...)
	}
	return snapshot, nil
}
