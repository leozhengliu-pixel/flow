package main

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Incremental review sync from webhooks: GitHub comment, review and thread
// deliveries update the matching events in place; pull request changes and
// GitLab merge request / note hooks queue a full sync.

// githubReviewActivityEvent reports GitHub events that carry review comments.
func githubReviewActivityEvent(name string) bool {
	switch name {
	case "pull_request_review_comment", "pull_request_review", "pull_request_review_thread", "issue_comment":
		return true
	}
	return false
}

// githubReviewActivityOnly reports deliveries only the comment sync handles
// (edits, deletions, dismissals, thread resolution): they do not add a
// review delivery event or notifications.
func githubReviewActivityOnly(name, action string) bool {
	switch name {
	case "pull_request_review_thread":
		return true
	case "pull_request_review_comment", "issue_comment":
		return action == "edited" || action == "deleted"
	case "pull_request_review":
		return action == "edited" || action == "dismissed"
	}
	return false
}

type githubReviewActivityPayload struct {
	Action     string `json:"action"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	PullRequest *struct {
		Number int `json:"number"`
		Head   struct {
			SHA string `json:"sha"`
		} `json:"head"`
	} `json:"pull_request"`
	Issue *struct {
		Number      int `json:"number"`
		PullRequest *struct {
			URL string `json:"url"`
		} `json:"pull_request"`
	} `json:"issue"`
	Comment json.RawMessage  `json:"comment"`
	Review  *githubAPIReview `json:"review"`
	Thread  *struct {
		NodeID   string                   `json:"node_id"`
		Comments []githubAPIReviewComment `json:"comments"`
	} `json:"thread"`
}

func (p githubReviewActivityPayload) number() int {
	if p.PullRequest != nil {
		return p.PullRequest.Number
	}
	if p.Issue != nil && p.Issue.PullRequest != nil {
		return p.Issue.Number
	}
	return 0
}

// githubReviewActivity turns a delivery into comments to merge and remote
// keys to delete.
func githubReviewActivity(name string, payload githubReviewActivityPayload) ([]remoteReviewComment, []string, error) {
	comments, deleted := []remoteReviewComment{}, []string{}
	switch name {
	case "pull_request_review_comment":
		var comment githubAPIReviewComment
		if err := json.Unmarshal(payload.Comment, &comment); err != nil {
			return nil, nil, err
		}
		if payload.Action == "deleted" {
			deleted = append(deleted, reviewRemoteReviewComment+":"+strconv.FormatInt(comment.ID, 10))
		} else {
			comments = append(comments, githubInlineComment(comment, nil))
		}
	case "issue_comment":
		var comment githubAPIIssueComment
		if err := json.Unmarshal(payload.Comment, &comment); err != nil {
			return nil, nil, err
		}
		if payload.Action == "deleted" {
			deleted = append(deleted, reviewRemoteIssueComment+":"+strconv.FormatInt(comment.ID, 10))
		} else {
			comments = append(comments, githubConversationComment(comment))
		}
	case "pull_request_review":
		if payload.Review == nil {
			return nil, nil, nil
		}
		review := *payload.Review
		// An empty comment-only review is usually a thread reply's container;
		// the full sync adds it when it starts threads.
		if strings.EqualFold(review.State, "commented") && strings.TrimSpace(review.Body) == "" {
			return nil, nil, nil
		}
		if summary, ok := githubReviewSummary(review); ok {
			comments = append(comments, summary)
		}
	case "pull_request_review_thread":
		if payload.Thread == nil {
			return nil, nil, nil
		}
		thread := githubReviewThread{ID: payload.Thread.NodeID, IsResolved: payload.Action == "resolved"}
		for _, comment := range payload.Thread.Comments {
			comments = append(comments, githubInlineComment(comment, &thread))
		}
	}
	return comments, deleted, nil
}

// applyGitHubReviewActivity merges one comment / review / thread delivery
// into the matching review and returns its id ("" when no review matches).
func (s *server) applyGitHubReviewActivity(ctx context.Context, workspace, name string, body []byte) (domain.CodeReview, error) {
	var payload githubReviewActivityPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return domain.CodeReview{}, err
	}
	number := payload.number()
	data, ok := s.store.WorkspaceMetadata(workspace)
	if !ok || number == 0 {
		return domain.CodeReview{}, nil
	}
	review, found := findSyncedReview(data, domain.CodeHostGitHub, payload.Repository.FullName, number)
	if !found {
		return domain.CodeReview{}, nil
	}
	comments, deleted, err := githubReviewActivity(name, payload)
	if err != nil {
		return domain.CodeReview{}, err
	}
	if len(comments) == 0 && len(deleted) == 0 {
		return review, nil
	}
	appUsers := s.githubAppUsers(ctx, workspace)
	headSHA := ""
	if payload.PullRequest != nil {
		headSHA = payload.PullRequest.Head.SHA
	}
	var updated domain.CodeReview
	scope := store.WithMutationScope(ctx, store.MutationScope{Resources: []string{review.ID}})
	err = s.store.MutateWorkspace(scope, workspace, "review.code_host_activity", review.ID, map[string]string{"event": name, "action": payload.Action}, func(data *domain.Bootstrap) error {
		index := reviewIndex(*data, review.ID)
		if index < 0 {
			return errNotFound
		}
		current := data.Reviews[index]
		removeRemoteReviewEvents(&current, domain.CodeHostGitHub, deleted...)
		mergeReviewSnapshot(&current, reviewSnapshot{Provider: domain.CodeHostGitHub, HeadSHA: headSHA, Comments: comments}, reviewActorResolver(data, domain.CodeHostGitHub, appUsers), time.Now().UTC())
		current.UpdatedAt = time.Now().UTC()
		data.Reviews[index], updated = current, current
		return nil
	})
	return updated, err
}

// afterCodeReviewWebhook applies the review comment activity of a verified
// delivery and queues a full sync when the request's files may have
// changed (or the review was never synced). handled reports a delivery
// that matched a review.
func (s *server) afterCodeReviewWebhook(ctx context.Context, workspace, provider, eventName string, body []byte) (string, error) {
	if provider == domain.CodeHostGitLab {
		var hook struct {
			ObjectKind string `json:"object_kind"`
			Project    struct {
				PathWithNamespace string `json:"path_with_namespace"`
			} `json:"project"`
			ObjectAttributes struct {
				IID int `json:"iid"`
			} `json:"object_attributes"`
			MergeRequest *struct {
				IID int `json:"iid"`
			} `json:"merge_request"`
		}
		if json.Unmarshal(body, &hook) != nil {
			return "", nil
		}
		number := 0
		switch hook.ObjectKind {
		case "merge_request":
			number = hook.ObjectAttributes.IID
		case "note":
			if hook.MergeRequest != nil {
				number = hook.MergeRequest.IID
			}
		}
		data, ok := s.store.WorkspaceMetadata(workspace)
		if !ok || number == 0 {
			return "", nil
		}
		if review, found := findSyncedReview(data, domain.CodeHostGitLab, hook.Project.PathWithNamespace, number); found {
			s.queueReviewSync(workspace, review.ID)
			return review.ID, nil
		}
		return "", nil
	}
	if provider != domain.CodeHostGitHub {
		return "", nil
	}
	if githubReviewActivityEvent(eventName) {
		review, err := s.applyGitHubReviewActivity(ctx, workspace, eventName, body)
		if err != nil || review.ID == "" {
			return "", err
		}
		if review.SyncedAt == nil {
			s.queueReviewSync(workspace, review.ID)
		}
		return review.ID, nil
	}
	if eventName == "pull_request" {
		var payload struct {
			Action     string `json:"action"`
			Repository struct {
				FullName string `json:"full_name"`
			} `json:"repository"`
			PullRequest struct {
				Number int `json:"number"`
			} `json:"pull_request"`
		}
		if json.Unmarshal(body, &payload) != nil {
			return "", nil
		}
		data, ok := s.store.WorkspaceMetadata(workspace)
		if !ok {
			return "", nil
		}
		if review, found := findSyncedReview(data, domain.CodeHostGitHub, payload.Repository.FullName, payload.PullRequest.Number); found {
			switch payload.Action {
			case "opened", "reopened", "synchronize", "edited", "ready_for_review", "converted_to_draft":
				s.queueReviewSync(workspace, review.ID)
			default:
				if review.SyncedAt == nil {
					s.queueReviewSync(workspace, review.ID)
				}
			}
			return review.ID, nil
		}
	}
	return "", nil
}
