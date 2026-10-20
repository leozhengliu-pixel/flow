package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
)

type githubAPIUser struct {
	Login     string `json:"login"`
	AvatarURL string `json:"avatar_url"`
	Email     string `json:"email"`
}

func (u githubAPIUser) author() remoteReviewAuthor {
	return remoteReviewAuthor{Login: u.Login, AvatarURL: u.AvatarURL, Email: u.Email}
}

type githubAPIFile struct {
	Filename         string  `json:"filename"`
	PreviousFilename string  `json:"previous_filename"`
	Status           string  `json:"status"`
	Additions        int     `json:"additions"`
	Deletions        int     `json:"deletions"`
	Changes          int     `json:"changes"`
	Patch            *string `json:"patch"`
}

// githubAPIReviewComment is a pull request review (inline) comment, from
// the REST API and the pull_request_review_comment / _thread webhooks.
type githubAPIReviewComment struct {
	ID                  int64         `json:"id"`
	PullRequestReviewID int64         `json:"pull_request_review_id"`
	InReplyToID         int64         `json:"in_reply_to_id"`
	Path                string        `json:"path"`
	Line                *int          `json:"line"`
	OriginalLine        *int          `json:"original_line"`
	StartLine           *int          `json:"start_line"`
	OriginalStartLine   *int          `json:"original_start_line"`
	Side                string        `json:"side"`
	StartSide           *string       `json:"start_side"`
	CommitID            string        `json:"commit_id"`
	DiffHunk            string        `json:"diff_hunk"`
	Body                string        `json:"body"`
	HTMLURL             string        `json:"html_url"`
	User                githubAPIUser `json:"user"`
	CreatedAt           time.Time     `json:"created_at"`
	UpdatedAt           time.Time     `json:"updated_at"`
}

type githubAPIIssueComment struct {
	ID        int64         `json:"id"`
	Body      string        `json:"body"`
	HTMLURL   string        `json:"html_url"`
	User      githubAPIUser `json:"user"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
}

type githubAPIReview struct {
	ID          int64         `json:"id"`
	Body        string        `json:"body"`
	State       string        `json:"state"`
	HTMLURL     string        `json:"html_url"`
	CommitID    string        `json:"commit_id"`
	User        githubAPIUser `json:"user"`
	SubmittedAt *time.Time    `json:"submitted_at"`
}

type githubAPIPull struct {
	Title     string  `json:"title"`
	Body      *string `json:"body"`
	HTMLURL   string  `json:"html_url"`
	State     string  `json:"state"`
	Draft     bool    `json:"draft"`
	Merged    bool    `json:"merged"`
	MergedAt  *string `json:"merged_at"`
	Additions int     `json:"additions"`
	Deletions int     `json:"deletions"`
	Commits   int     `json:"commits"`
	Head      struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"base"`
}

// githubReviewThread is a GraphQL review thread: its resolution and the
// database ids of its comments.
type githubReviewThread struct {
	ID         string `json:"id"`
	IsResolved bool   `json:"isResolved"`
	IsOutdated bool   `json:"isOutdated"`
	Comments   struct {
		Nodes []githubThreadComment `json:"nodes"`
	} `json:"comments"`
}

type githubThreadComment struct {
	DatabaseID int64 `json:"databaseId"`
}

const githubReviewThreadsQuery = `query($owner: String!, $name: String!, $number: Int!, $after: String) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      reviewThreads(first: 100, after: $after) {
        pageInfo { hasNextPage endCursor }
        nodes { id isResolved isOutdated comments(first: 100) { nodes { databaseId } } }
      }
    }
  }
}`

// fetchGitHubReviewThreads reads every review thread of a pull request.
func fetchGitHubReviewThreads(ctx context.Context, remote reviewRemote, review domain.CodeReview) ([]githubReviewThread, error) {
	threads := []githubReviewThread{}
	var after any
	for page := 0; page < 50; page++ {
		var out struct {
			Repository struct {
				PullRequest *struct {
					ReviewThreads struct {
						PageInfo struct {
							HasNextPage bool   `json:"hasNextPage"`
							EndCursor   string `json:"endCursor"`
						} `json:"pageInfo"`
						Nodes []githubReviewThread `json:"nodes"`
					} `json:"reviewThreads"`
				} `json:"pullRequest"`
			} `json:"repository"`
		}
		variables := map[string]any{"owner": review.RepositoryOwner, "name": review.RepositoryName, "number": review.Number, "after": after}
		if err := remote.GraphQL(ctx, githubReviewThreadsQuery, variables, &out); err != nil {
			return nil, err
		}
		if out.Repository.PullRequest == nil {
			return threads, nil
		}
		connection := out.Repository.PullRequest.ReviewThreads
		threads = append(threads, connection.Nodes...)
		if !connection.PageInfo.HasNextPage || connection.PageInfo.EndCursor == "" {
			break
		}
		after = connection.PageInfo.EndCursor
	}
	return threads, nil
}

func githubFileStatus(status string) string {
	switch status {
	case "added", "copied":
		return "added"
	case "removed":
		return "deleted"
	case "renamed":
		return "renamed"
	}
	return "modified" // modified, changed, unchanged
}

func githubReviewFile(file githubAPIFile) domain.ReviewFile {
	result := domain.ReviewFile{Path: file.Filename, Status: githubFileStatus(file.Status), Additions: file.Additions, Deletions: file.Deletions}
	if result.Status == "renamed" {
		result.PreviousPath = file.PreviousFilename
	}
	if file.Patch != nil {
		result.Patch = *file.Patch
	} else if file.Changes > 0 || file.Additions > 0 || file.Deletions > 0 {
		// GitHub leaves the patch out of binary and very large diffs.
		result.PatchUnavailable = true
	}
	return result
}

func githubReviewState(state string) string {
	switch strings.ToUpper(state) {
	case "APPROVED":
		return "approved"
	case "CHANGES_REQUESTED":
		return "changes_requested"
	case "DISMISSED":
		return "review_dismissed"
	case "COMMENTED":
		return "review_commented"
	}
	return ""
}

func githubSide(side string) string {
	if strings.EqualFold(side, "LEFT") {
		return "LEFT"
	}
	return "RIGHT"
}

// githubInlineComment maps a review comment; thread (optional) gives its
// resolution and thread id.
func githubInlineComment(comment githubAPIReviewComment, thread *githubReviewThread) remoteReviewComment {
	result := remoteReviewComment{Kind: reviewRemoteReviewComment, ID: strconv.FormatInt(comment.ID, 10), Type: "commented", Body: comment.Body, Path: comment.Path,
		Side: githubSide(comment.Side), CommitID: comment.CommitID, DiffHunk: comment.DiffHunk, URL: comment.HTMLURL, Author: comment.User.author(), CreatedAt: comment.CreatedAt, UpdatedAt: comment.UpdatedAt}
	switch {
	case comment.Line != nil:
		result.Line = *comment.Line
	case comment.OriginalLine != nil:
		// The line is gone from the current diff: show it where it was.
		result.Line, result.Outdated = *comment.OriginalLine, true
	}
	switch {
	case comment.StartLine != nil:
		result.StartLine = *comment.StartLine
	case comment.OriginalStartLine != nil:
		result.StartLine = *comment.OriginalStartLine
	}
	if result.StartLine > 0 {
		result.StartSide = result.Side
		if comment.StartSide != nil && *comment.StartSide != "" {
			result.StartSide = githubSide(*comment.StartSide)
		}
	}
	if comment.InReplyToID != 0 {
		result.ParentKind, result.ParentID = reviewRemoteReviewComment, strconv.FormatInt(comment.InReplyToID, 10)
	}
	if thread != nil {
		resolved := thread.IsResolved
		result.Resolved, result.ThreadID = &resolved, thread.ID
		result.Outdated = result.Outdated || thread.IsOutdated
	}
	return result
}

func githubConversationComment(comment githubAPIIssueComment) remoteReviewComment {
	return remoteReviewComment{Kind: reviewRemoteIssueComment, ID: strconv.FormatInt(comment.ID, 10), Type: "commented", Body: comment.Body, URL: comment.HTMLURL,
		Author: comment.User.author(), CreatedAt: comment.CreatedAt, UpdatedAt: comment.UpdatedAt}
}

// githubReviewSummary maps a submitted review; pending reviews are skipped.
func githubReviewSummary(review githubAPIReview) (remoteReviewComment, bool) {
	kind := githubReviewState(review.State)
	if kind == "" {
		return remoteReviewComment{}, false
	}
	result := remoteReviewComment{Kind: reviewRemoteReview, ID: strconv.FormatInt(review.ID, 10), Type: kind, Body: review.Body, URL: review.HTMLURL, CommitID: review.CommitID, Author: review.User.author()}
	if review.SubmittedAt != nil {
		result.CreatedAt = *review.SubmittedAt
	}
	return result, true
}

// fetchGitHubReviewSnapshot reads the pull request, its files (renames,
// missing patches), inline comments with their threads, conversation
// comments and reviews.
func fetchGitHubReviewSnapshot(ctx context.Context, remote reviewRemote, review domain.CodeReview) (reviewSnapshot, error) {
	base := "/pulls/" + strconv.Itoa(review.Number)
	var pull githubAPIPull
	if err := remote.REST(ctx, http.MethodGet, base, nil, &pull); err != nil {
		return reviewSnapshot{}, err
	}
	files, err := fetchPages[githubAPIFile](ctx, remote, base+"/files", 30)
	if err != nil {
		return reviewSnapshot{}, fmt.Errorf("pull request files: %w", err)
	}
	inline, err := fetchPages[githubAPIReviewComment](ctx, remote, base+"/comments", 50)
	if err != nil {
		return reviewSnapshot{}, fmt.Errorf("review comments: %w", err)
	}
	conversation, err := fetchPages[githubAPIIssueComment](ctx, remote, "/issues/"+strconv.Itoa(review.Number)+"/comments", 50)
	if err != nil {
		return reviewSnapshot{}, fmt.Errorf("conversation comments: %w", err)
	}
	reviews, err := fetchPages[githubAPIReview](ctx, remote, base+"/reviews", 20)
	if err != nil {
		return reviewSnapshot{}, fmt.Errorf("reviews: %w", err)
	}
	threadOf := map[int64]*githubReviewThread{}
	if len(inline) > 0 {
		threads, err := fetchGitHubReviewThreads(ctx, remote, review)
		if err != nil {
			// Resolution is GraphQL only; without it comments still sync.
			log.Printf("review sync %s/%s#%d: review threads: %v", review.RepositoryOwner, review.RepositoryName, review.Number, err)
		}
		for index := range threads {
			for _, node := range threads[index].Comments.Nodes {
				threadOf[node.DatabaseID] = &threads[index]
			}
		}
	}
	description := ""
	if pull.Body != nil {
		description = *pull.Body
	}
	snapshot := reviewSnapshot{Provider: domain.CodeHostGitHub, HeadSHA: pull.Head.SHA, BaseSHA: pull.Base.SHA, Complete: true, Files: make([]domain.ReviewFile, 0, len(files)),
		Pull: &remotePullRequest{Title: pull.Title, Description: description, BaseBranch: pull.Base.Ref, HeadBranch: pull.Head.Ref, URL: pull.HTMLURL, Draft: pull.Draft,
			Merged: pull.Merged || (pull.MergedAt != nil && *pull.MergedAt != ""), Closed: pull.State == "closed", Additions: pull.Additions, Deletions: pull.Deletions, Commits: pull.Commits}}
	for _, file := range files {
		snapshot.Files = append(snapshot.Files, githubReviewFile(file))
	}
	for _, comment := range inline {
		snapshot.Comments = append(snapshot.Comments, githubInlineComment(comment, threadOf[comment.ID]))
	}
	for _, comment := range conversation {
		snapshot.Comments = append(snapshot.Comments, githubConversationComment(comment))
	}
	// A comment-only review with no body that only holds replies is how
	// GitHub records a thread reply, not a review of its own.
	startsThreads := map[int64]bool{}
	for _, comment := range inline {
		if comment.InReplyToID == 0 {
			startsThreads[comment.PullRequestReviewID] = true
		}
	}
	for _, item := range reviews {
		if strings.EqualFold(item.State, "COMMENTED") && strings.TrimSpace(item.Body) == "" && !startsThreads[item.ID] {
			continue
		}
		if summary, ok := githubReviewSummary(item); ok {
			snapshot.Comments = append(snapshot.Comments, summary)
		}
	}
	return snapshot, nil
}
