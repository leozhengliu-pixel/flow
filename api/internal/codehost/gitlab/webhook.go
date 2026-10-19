package gitlab

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"flow/api/internal/codehost"
	"flow/api/internal/domain"
)

// GitLab webhook event names (X-Gitlab-Event).
const (
	HookMergeRequest = "Merge Request Hook"
	HookNote         = "Note Hook"
	HookPipeline     = "Pipeline Hook"
)

// WebhookVerification carries what ParseWebhook needs to verify a GitLab
// delivery. GitLab hooks are per workspace (…/gitlab/webhook?workspace=),
// so the caller resolves the workspace connection and passes its secret.
type WebhookVerification struct {
	// Secrets are the accepted X-Gitlab-Token values (the connection's
	// webhook secret). Empty means every delivery is rejected.
	Secrets []string
	// ConnectionID becomes WebhookEvent.InstallationID.
	ConnectionID string
	// SelfUsernames are GitLab users whose notes are Flow's own (the
	// connection user); they are marked AuthorIsBot.
	SelfUsernames []string
}

type webhookVerificationKey struct{}

// WithWebhookVerification attaches the verification data to ctx for ParseWebhook.
func WithWebhookVerification(ctx context.Context, verification WebhookVerification) context.Context {
	return context.WithValue(ctx, webhookVerificationKey{}, verification)
}

// VerifyToken compares X-Gitlab-Token with the accepted secrets in constant time.
func VerifyToken(header http.Header, secrets []string) bool {
	presented := strings.TrimSpace(header.Get("X-Gitlab-Token"))
	if presented == "" {
		return false
	}
	ok := false
	for _, secret := range secrets {
		if secret = strings.TrimSpace(secret); secret != "" && hmac.Equal([]byte(secret), []byte(presented)) {
			ok = true
		}
	}
	return ok
}

// DeliveryID is the delivery's idempotency key: Idempotency-Key, then
// X-Gitlab-Event-UUID, then the body hash (same order as code_webhooks.go).
func DeliveryID(header http.Header, body []byte) string {
	if id := strings.TrimSpace(header.Get("Idempotency-Key")); id != "" {
		return id
	}
	if id := strings.TrimSpace(header.Get("X-Gitlab-Event-UUID")); id != "" {
		return id
	}
	return fmt.Sprintf("%x", sha256.Sum256(body))
}

type hookUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
	// Email is only sent when the user made it public.
	Email string `json:"email"`
}

type hookProject struct {
	ID                int64  `json:"id"`
	PathWithNamespace string `json:"path_with_namespace"`
	WebURL            string `json:"web_url"`
}

type hookMergeRequest struct {
	ID              int64  `json:"id"`
	IID             int    `json:"iid"`
	Title           string `json:"title"`
	URL             string `json:"url"`
	State           string `json:"state"`
	Action          string `json:"action"`
	SourceBranch    string `json:"source_branch"`
	TargetBranch    string `json:"target_branch"`
	SourceProjectID int64  `json:"source_project_id"`
	TargetProjectID int64  `json:"target_project_id"`
	Draft           bool   `json:"draft"`
	WorkInProgress  bool   `json:"work_in_progress"`
	OldRev          string `json:"oldrev"`
	LastCommit      struct {
		ID string `json:"id"`
	} `json:"last_commit"`
}

type hookPayload struct {
	ObjectKind string      `json:"object_kind"`
	User       hookUser    `json:"user"`
	Project    hookProject `json:"project"`
	// Merge Request Hook: object_attributes is the MR. Note Hook: the note
	// (merge_request carries the MR). Pipeline Hook: the pipeline.
	ObjectAttributes json.RawMessage   `json:"object_attributes"`
	MergeRequest     *hookMergeRequest `json:"merge_request"`
	Builds           []struct {
		Name   string `json:"name"`
		Stage  string `json:"stage"`
		Status string `json:"status"`
	} `json:"builds"`
}

type hookNote struct {
	ID           int64  `json:"id"`
	Note         string `json:"note"`
	NoteableType string `json:"noteable_type"`
	System       bool   `json:"system"`
	Type         string `json:"type"` // DiffNote, DiscussionNote, null
	URL          string `json:"url"`
	Position     *struct {
		NewPath string `json:"new_path"`
		OldPath string `json:"old_path"`
		NewLine *int   `json:"new_line"`
		OldLine *int   `json:"old_line"`
	} `json:"position"`
}

type hookPipeline struct {
	ID     int64  `json:"id"`
	Ref    string `json:"ref"`
	Tag    bool   `json:"tag"`
	SHA    string `json:"sha"`
	Status string `json:"status"`
	URL    string `json:"url"`
}

// botUsername matches GitLab's project/group access token bot users.
var botUsername = regexp.MustCompile(`^(project|group)_\d+_bot(_[0-9a-f]+)?$`)

// ParseWebhook verifies X-Gitlab-Token against the secrets attached with
// WithWebhookVerification and normalizes Merge Request, Note and Pipeline
// hooks. Other hooks (and system notes, issue notes, unfinished pipelines)
// come back as EventIgnored.
func (h *Host) ParseWebhook(ctx context.Context, header http.Header, body []byte) (codehost.WebhookEvent, error) {
	verification, _ := ctx.Value(webhookVerificationKey{}).(WebhookVerification)
	if !VerifyToken(header, verification.Secrets) {
		return codehost.WebhookEvent{}, codehost.ErrBadSignature
	}
	var payload hookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return codehost.WebhookEvent{}, fmt.Errorf("gitlab webhook: invalid payload: %w", err)
	}
	event := codehost.WebhookEvent{
		DeliveryID:     DeliveryID(header, body),
		Type:           codehost.EventIgnored,
		CodeHost:       domain.CodeHostGitLab,
		HostName:       hostOf(payload.Project.WebURL),
		InstallationID: verification.ConnectionID,
		Repo: codehost.RepoRef{
			CodeHost: domain.CodeHostGitLab,
			HostName: hostOf(payload.Project.WebURL),
			FullName: payload.Project.PathWithNamespace,
		},
		AuthorLogin: payload.User.Username,
		AuthorIsBot: isBot(payload.User.Username, verification.SelfUsernames),
		AuthorEmail: payload.User.Email,
	}
	if payload.Project.ID != 0 {
		event.Repo.ID = strconv.FormatInt(payload.Project.ID, 10)
	}
	kind := strings.TrimSpace(header.Get("X-Gitlab-Event"))
	if kind == "" {
		kind = map[string]string{"merge_request": HookMergeRequest, "note": HookNote, "pipeline": HookPipeline}[payload.ObjectKind]
	}
	switch kind {
	case HookMergeRequest:
		var mergeRequest hookMergeRequest
		if err := json.Unmarshal(payload.ObjectAttributes, &mergeRequest); err != nil || mergeRequest.IID == 0 {
			return event, nil
		}
		event.ChangeRequest = hookChangeRequest(event.Repo, mergeRequest)
		event.Type = mergeRequestEventType(mergeRequest)
		if event.Type == codehost.EventReview {
			event.ReviewState = "approved"
		}
	case HookNote:
		var note hookNote
		if err := json.Unmarshal(payload.ObjectAttributes, &note); err != nil || note.System || note.NoteableType != "MergeRequest" || payload.MergeRequest == nil {
			return event, nil
		}
		event.ChangeRequest = hookChangeRequest(event.Repo, *payload.MergeRequest)
		event.CommentID = strconv.FormatInt(note.ID, 10)
		event.CommentBody = note.Note
		event.CommentURL = note.URL
		event.Type = codehost.EventComment
		if note.Type == "DiffNote" || note.Position != nil {
			event.Type = codehost.EventReviewComment
			if note.Position != nil {
				event.CommentPath = firstNonEmpty(note.Position.NewPath, note.Position.OldPath)
				if note.Position.NewLine != nil {
					event.CommentLine = *note.Position.NewLine
				} else if note.Position.OldLine != nil {
					event.CommentLine = *note.Position.OldLine
				}
			}
		}
	case HookPipeline:
		var pipeline hookPipeline
		if err := json.Unmarshal(payload.ObjectAttributes, &pipeline); err != nil || pipeline.ID == 0 {
			return event, nil
		}
		state := normalizePipelineStatus(pipeline.Status)
		if state != "success" && state != "failure" {
			return event, nil // only finished pipelines become follow-up context
		}
		url := pipeline.URL
		if url == "" && payload.Project.WebURL != "" {
			url = fmt.Sprintf("%s/-/pipelines/%d", strings.TrimRight(payload.Project.WebURL, "/"), pipeline.ID)
		}
		ci := &codehost.CIStatus{State: state, URL: url, Summary: fmt.Sprintf("Pipeline #%d %s", pipeline.ID, pipeline.Status)}
		failed := []string{}
		for _, build := range payload.Builds {
			if build.Status == "failed" {
				failed = append(failed, build.Stage+"/"+build.Name)
			}
		}
		if len(failed) > 0 {
			ci.Summary += ": failed jobs " + summarizeList(failed, 5)
		}
		event.CI = ci
		event.Type = codehost.EventCIStatus
		if payload.MergeRequest != nil && payload.MergeRequest.IID != 0 {
			event.ChangeRequest = hookChangeRequest(event.Repo, *payload.MergeRequest)
		} else if !pipeline.Tag {
			// Branch pipeline: L resolves the MR by head branch.
			event.ChangeRequest = &codehost.ChangeRequest{Repo: event.Repo, HeadBranch: pipeline.Ref}
		}
		if event.ChangeRequest != nil && pipeline.SHA != "" {
			event.ChangeRequest.HeadSHA = pipeline.SHA
		}
	}
	return event, nil
}

func hookChangeRequest(repo codehost.RepoRef, mergeRequest hookMergeRequest) *codehost.ChangeRequest {
	state := mergeRequest.State
	switch mergeRequest.Action {
	case "merge":
		state = "merged"
	case "close":
		state = "closed"
	}
	return &codehost.ChangeRequest{
		Repo:       repo,
		Number:     mergeRequest.IID,
		ExternalID: strconv.FormatInt(mergeRequest.ID, 10),
		URL:        mergeRequest.URL,
		Title:      mergeRequest.Title,
		State:      normalizeMRState(state),
		Draft:      mergeRequest.Draft || mergeRequest.WorkInProgress || draftTitle.MatchString(mergeRequest.Title),
		HeadBranch: mergeRequest.SourceBranch,
		BaseBranch: mergeRequest.TargetBranch,
		HeadSHA:    mergeRequest.LastCommit.ID,
	}
}

// mergeRequestEventType maps object_attributes.action of a Merge Request Hook.
func mergeRequestEventType(mergeRequest hookMergeRequest) codehost.WebhookEventType {
	switch mergeRequest.Action {
	case "open", "reopen":
		return codehost.EventChangeRequestOpened
	case "close":
		return codehost.EventChangeRequestClosed
	case "merge":
		return codehost.EventChangeRequestMerged
	case "approved", "approval":
		return codehost.EventReview
	case "update", "unapproved", "unapproval":
		return codehost.EventChangeRequestUpdate
	}
	switch normalizeMRState(mergeRequest.State) {
	case "merged":
		return codehost.EventChangeRequestMerged
	case "closed":
		return codehost.EventChangeRequestClosed
	}
	return codehost.EventChangeRequestUpdate
}

func isBot(username string, self []string) bool {
	username = strings.TrimSpace(username)
	if username == "" {
		return false
	}
	for _, candidate := range self {
		if candidate = strings.TrimSpace(candidate); candidate != "" && strings.EqualFold(candidate, username) {
			return true
		}
	}
	return botUsername.MatchString(username) || strings.EqualFold(username, "ghost")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
