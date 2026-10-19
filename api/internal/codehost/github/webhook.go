package github

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"flow/api/internal/codehost"
	"flow/api/internal/domain"
)

// VerifySignature checks X-Hub-Signature-256 (HMAC-SHA256 of the raw body
// with the App webhook secret) in constant time.
func VerifySignature(secret string, header http.Header, body []byte) bool {
	if secret == "" {
		return false
	}
	signature := strings.TrimSpace(header.Get("X-Hub-Signature-256"))
	hexSum, ok := strings.CutPrefix(signature, "sha256=")
	if !ok {
		return false
	}
	got, err := hex.DecodeString(hexSum)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}

type webhookUser struct {
	Login string `json:"login"`
	Type  string `json:"type"`
	// Email is only present when the user made it public.
	Email string `json:"email"`
}

func (u webhookUser) isBot() bool {
	return strings.EqualFold(u.Type, "Bot") || strings.HasSuffix(strings.ToLower(u.Login), "[bot]")
}

type webhookPayload struct {
	Action       string           `json:"action"`
	Installation *apiInstallation `json:"installation"`
	Repository   *struct {
		ID       int64  `json:"id"`
		FullName string `json:"full_name"`
	} `json:"repository"`
	Sender      webhookUser     `json:"sender"`
	PullRequest *apiPullRequest `json:"pull_request"`
	Issue       *struct {
		Number      int    `json:"number"`
		Title       string `json:"title"`
		HTMLURL     string `json:"html_url"`
		State       string `json:"state"`
		PullRequest *struct {
			URL string `json:"url"`
		} `json:"pull_request"`
	} `json:"issue"`
	Comment *struct {
		ID       int64       `json:"id"`
		Body     string      `json:"body"`
		Path     string      `json:"path"`
		Line     int         `json:"line"`
		DiffHunk string      `json:"diff_hunk"`
		HTMLURL  string      `json:"html_url"`
		User     webhookUser `json:"user"`
	} `json:"comment"`
	Review *struct {
		ID      int64       `json:"id"`
		Body    string      `json:"body"`
		State   string      `json:"state"`
		HTMLURL string      `json:"html_url"`
		User    webhookUser `json:"user"`
	} `json:"review"`
	CheckSuite *struct {
		HeadBranch   string           `json:"head_branch"`
		HeadSHA      string           `json:"head_sha"`
		Status       string           `json:"status"`
		Conclusion   string           `json:"conclusion"`
		URL          string           `json:"url"`
		PullRequests []apiPullRequest `json:"pull_requests"`
	} `json:"check_suite"`
}

// ParseWebhook verifies the signature and normalizes a delivery. Events Flow
// does not act on come back as EventIgnored (never an error), so the
// handler can acknowledge them.
func (h *Host) ParseWebhook(_ context.Context, header http.Header, body []byte) (codehost.WebhookEvent, error) {
	if !h.cfg.Configured() {
		return codehost.WebhookEvent{}, fmt.Errorf("%w: GitHub App is not configured", codehost.ErrNotConnected)
	}
	if !VerifySignature(h.cfg.WebhookSecret, header, body) {
		return codehost.WebhookEvent{}, codehost.ErrBadSignature
	}
	name := strings.TrimSpace(header.Get("X-GitHub-Event"))
	event := codehost.WebhookEvent{DeliveryID: strings.TrimSpace(header.Get("X-GitHub-Delivery")), Type: codehost.EventIgnored, CodeHost: domain.CodeHostGitHub, HostName: h.cfg.Host}
	if name == "ping" {
		return event, nil
	}
	var payload webhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return codehost.WebhookEvent{}, fmt.Errorf("github: invalid webhook payload: %w", err)
	}
	if event.DeliveryID == "" {
		event.DeliveryID = fmt.Sprintf("sha256:%x", sha256.Sum256(body))
	}
	event.Action = payload.Action
	event.AuthorLogin, event.AuthorIsBot = payload.Sender.Login, payload.Sender.isBot()
	if payload.Installation != nil && payload.Installation.ID != 0 {
		event.InstallationID = strconv.FormatInt(payload.Installation.ID, 10)
	}
	if payload.Repository != nil {
		event.Repo = codehost.RepoRef{CodeHost: domain.CodeHostGitHub, HostName: h.cfg.Host, FullName: payload.Repository.FullName, ID: strconv.FormatInt(payload.Repository.ID, 10)}
	}
	switch name {
	case "installation", "installation_repositories":
		if payload.Installation == nil {
			return event, nil
		}
		installation := h.installation(*payload.Installation)
		if payload.Action == "suspend" && installation.SuspendedAt == nil {
			now := h.now().UTC()
			installation.SuspendedAt = &now
		}
		if payload.Action == "unsuspend" {
			installation.SuspendedAt = nil
		}
		event.Type, event.Installation = codehost.EventInstallation, &installation
	case "pull_request":
		if payload.PullRequest == nil {
			return event, nil
		}
		changeRequest := h.changeRequest(event.Repo, *payload.PullRequest)
		event.ChangeRequest = &changeRequest
		switch payload.Action {
		case "opened", "reopened":
			event.Type = codehost.EventChangeRequestOpened
		case "closed":
			event.Type = codehost.EventChangeRequestClosed
			if changeRequest.State == "merged" {
				event.Type = codehost.EventChangeRequestMerged
			}
		case "synchronize", "ready_for_review", "converted_to_draft", "edited":
			event.Type = codehost.EventChangeRequestUpdate
		}
	case "issue_comment":
		if payload.Issue == nil || payload.Issue.PullRequest == nil || payload.Comment == nil || payload.Action != "created" {
			return event, nil
		}
		event.Type = codehost.EventComment
		event.ChangeRequest = &codehost.ChangeRequest{Repo: event.Repo, Number: payload.Issue.Number, URL: payload.Issue.HTMLURL, Title: payload.Issue.Title, State: payload.Issue.State}
		h.setComment(&event, payload)
	case "pull_request_review_comment":
		if payload.PullRequest == nil || payload.Comment == nil || payload.Action != "created" {
			return event, nil
		}
		changeRequest := h.changeRequest(event.Repo, *payload.PullRequest)
		event.Type, event.ChangeRequest = codehost.EventReviewComment, &changeRequest
		h.setComment(&event, payload)
		event.CommentPath, event.CommentLine = payload.Comment.Path, payload.Comment.Line
	case "pull_request_review":
		if payload.PullRequest == nil || payload.Review == nil || payload.Action != "submitted" {
			return event, nil
		}
		changeRequest := h.changeRequest(event.Repo, *payload.PullRequest)
		event.Type, event.ChangeRequest = codehost.EventReview, &changeRequest
		event.CommentID, event.CommentBody = strconv.FormatInt(payload.Review.ID, 10), payload.Review.Body
		event.CommentURL = payload.Review.HTMLURL
		event.ReviewState = strings.ToLower(payload.Review.State)
		if payload.Review.User.Login != "" {
			event.AuthorLogin, event.AuthorIsBot = payload.Review.User.Login, payload.Review.User.isBot()
			event.AuthorEmail = payload.Review.User.Email
		}
	case "check_suite":
		if payload.CheckSuite == nil || payload.Action != "completed" {
			return event, nil
		}
		state := "failure"
		switch payload.CheckSuite.Conclusion {
		case "success", "neutral", "skipped":
			state = "success"
		case "":
			state = "pending"
		}
		event.Type = codehost.EventCIStatus
		event.CI = &codehost.CIStatus{State: state, URL: payload.CheckSuite.URL, Summary: payload.CheckSuite.Conclusion}
		if len(payload.CheckSuite.PullRequests) > 0 {
			changeRequest := h.changeRequest(event.Repo, payload.CheckSuite.PullRequests[0])
			changeRequest.State = ""
			event.ChangeRequest = &changeRequest
		} else {
			event.ChangeRequest = &codehost.ChangeRequest{Repo: event.Repo, HeadBranch: payload.CheckSuite.HeadBranch, HeadSHA: payload.CheckSuite.HeadSHA}
		}
	}
	return event, nil
}

func (h *Host) setComment(event *codehost.WebhookEvent, payload webhookPayload) {
	event.CommentID, event.CommentBody = strconv.FormatInt(payload.Comment.ID, 10), payload.Comment.Body
	event.CommentURL, event.CommentDiffHunk = payload.Comment.HTMLURL, payload.Comment.DiffHunk
	if payload.Comment.User.Login != "" {
		event.AuthorLogin, event.AuthorIsBot = payload.Comment.User.Login, payload.Comment.User.isBot()
		event.AuthorEmail = payload.Comment.User.Email
	}
}
