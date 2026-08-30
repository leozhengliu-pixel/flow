package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
)

func (s *server) codeWebhook(w http.ResponseWriter, r *http.Request) {
	provider := r.PathValue("provider")
	if provider != "github" && provider != "gitlab" {
		writeError(w, http.StatusNotFound, "unsupported provider")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 5<<20)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read webhook")
		return
	}
	connection := s.webhookConnection(r, provider)
	if connection == nil || !verifyCodeWebhook(provider, connection, r, body) {
		writeError(w, http.StatusUnauthorized, "invalid webhook signature")
		return
	}
	eventID := strings.TrimSpace(r.Header.Get("X-GitHub-Delivery"))
	if provider == "gitlab" {
		eventID = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
		if eventID == "" {
			eventID = strings.TrimSpace(r.Header.Get("X-Gitlab-Event-UUID"))
		}
	}
	if eventID == "" {
		eventID = fmt.Sprintf("%x", sha256.Sum256(body))
	}
	var event externalCodeReviewEvent
	if err := json.Unmarshal(body, &event); err != nil {
		writeError(w, http.StatusBadRequest, "invalid webhook payload")
		return
	}
	if event.Action == "" {
		event.Action = strings.TrimSpace(r.Header.Get("X-GitHub-Event"))
		if provider == "gitlab" {
			event.Action = strings.ToLower(strings.TrimSpace(r.Header.Get("X-Gitlab-Event")))
		}
	}
	if event.Number == 0 {
		event.Number = event.PullRequest.Number
	}
	if event.Number == 0 {
		event.Number = event.ObjectAttributes.IID
	}
	if event.Title == "" {
		event.Title = event.PullRequest.Title
	}
	if event.Title == "" {
		event.Title = event.ObjectAttributes.Title
	}
	if event.Title == "" || event.Number == 0 {
		writeJSON(w, http.StatusAccepted, map[string]any{"ignored": true, "reason": "event is not a pull/merge request"})
		return
	}
	var updated domain.CodeReview
	err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "code_review.webhook", eventID, map[string]any{"provider": provider, "action": event.Action}, func(data *domain.Bootstrap) error {
		if existing := slices.IndexFunc(data.Reviews, func(item domain.CodeReview) bool {
			return slices.ContainsFunc(item.Events, func(reviewEvent domain.ReviewEvent) bool { return reviewEvent.ID == eventID })
		}); existing >= 0 {
			updated = data.Reviews[existing]
			return nil
		}
		index := slices.IndexFunc(data.Reviews, func(item domain.CodeReview) bool {
			return item.Provider == provider && item.ExternalID == event.ExternalID()
		})
		now := time.Now().UTC()
		if index < 0 {
			review := event.toReview(provider, data.Viewer, now)
			review.ReviewerIDs = reviewerIDs(data, event)
			data.Reviews = append([]domain.CodeReview{review}, data.Reviews...)
			index = 0
		} else {
			review := data.Reviews[index]
			event.applyToReview(&review, now)
			review.ReviewerIDs = reviewerIDs(data, event)
			data.Reviews[index] = review
		}
		if index >= 0 {
			review := data.Reviews[index]
			previous := slices.Clone(review.IssueIDs)
			review.IssueIDs = normalizedStrings(append(review.IssueIDs, inferredReviewIssueIDs(data, review)...))
			for _, issueID := range review.IssueIDs {
				if !slices.Contains(previous, issueID) {
					appendActivity(data, issueID, "issue.review_linked", data.Viewer, map[string]string{"reviewId": review.ID, "reviewTitle": review.Title, "reviewNumber": fmt.Sprint(review.Number), "repository": review.RepositoryOwner + "/" + review.RepositoryName, "url": review.URL, "source": "webhook"})
				}
			}
			data.Reviews[index] = review
		}
		// Keep a durable event trail so review details and notification retries can
		// distinguish successive deliveries for the same PR/MR.
		if index >= 0 {
			review := data.Reviews[index]
			review.Events = append(review.Events, domain.ReviewEvent{ID: eventID, Type: event.Action, Body: provider, Actor: data.Viewer, CreatedAt: now})
			if len(review.Events) > 100 {
				review.Events = review.Events[len(review.Events)-100:]
			}
			data.Reviews[index] = review
			updated = review
		}
		updated = data.Reviews[index]
		for connectionIndex := range data.IntegrationConnections {
			if data.IntegrationConnections[connectionIndex].Provider == provider {
				data.IntegrationConnections[connectionIndex].LastWebhookAt = &now
				data.IntegrationConnections[connectionIndex].LastError = ""
				if data.IntegrationConnections[connectionIndex].LinkbackEnabled && updated.URL != "" {
					for issueIndex := range data.Issues {
						if !slices.Contains(updated.IssueIDs, data.Issues[issueIndex].ID) || slices.ContainsFunc(data.Issues[issueIndex].Attachments, func(item domain.Attachment) bool {
							return item.Provider == provider && item.ProviderID == updated.ExternalID
						}) {
							continue
						}
						data.Issues[issueIndex].Attachments = append(data.Issues[issueIndex].Attachments, domain.Attachment{ID: fmt.Sprintf("attachment_%s_%d", provider, now.UnixNano()), IssueID: data.Issues[issueIndex].ID, Title: updated.Title, URL: updated.URL, ContentType: "text/uri-list", CreatedAt: now, Creator: data.Viewer, Provider: provider, ProviderID: updated.ExternalID, ProviderURL: updated.URL, LinkbackURL: updated.URL, SyncStatus: "synced"})
					}
				}
			}
		}
		if !slices.ContainsFunc(data.Notifications, func(item domain.Notification) bool { return item.SourceID == eventID }) {
			data.Notifications = appendCodeReviewNotifications(data, updated, event, eventID, now)
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not persist webhook")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"reviewId": updated.ID, "eventId": eventID})
}

func inferredReviewIssueIDs(data *domain.Bootstrap, review domain.CodeReview) []string {
	haystack := strings.ToUpper(strings.Join([]string{review.Title, review.Description, review.HeadBranch}, " "))
	ids := make([]string, 0)
	for _, issue := range data.Issues {
		if strings.Contains(haystack, strings.ToUpper(issue.Identifier)) {
			ids = append(ids, issue.ID)
		}
	}
	return ids
}

// testIntegrationWebhook lets operators verify a configured connection and
// records the result. It intentionally does not call a provider API or emit a
// fake review event; delivery still has to come from GitHub/GitLab.
func (s *server) testIntegrationWebhook(w http.ResponseWriter, r *http.Request) {
	provider, id := strings.ToLower(r.PathValue("provider")), r.PathValue("id")
	if provider != "github" && provider != "gitlab" {
		writeError(w, http.StatusNotFound, "unsupported provider")
		return
	}
	var result map[string]any
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "integration.webhook_tested", id, nil, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.IntegrationConnections, func(item domain.IntegrationConnection) bool { return item.ID == id && item.Provider == provider })
		if index < 0 {
			return errNotFound
		}
		connection := &data.IntegrationConnections[index]
		secret := strings.TrimSpace(connection.Config["webhookSecret"])
		if envName := strings.TrimSpace(connection.Config["webhookSecretEnv"]); strings.HasPrefix(envName, "FLOW_INTEGRATION_") {
			secret = os.Getenv(envName)
		}
		if secret == "" {
			connection.LastError = "webhook secret is not configured"
			connection.UpdatedAt = time.Now().UTC()
			return errInvalid
		}
		now := time.Now().UTC()
		connection.LastWebhookAt = &now
		connection.LastError = ""
		connection.UpdatedAt = now
		result = map[string]any{"provider": provider, "connectionId": id, "status": "ready", "testedAt": now}
		return nil
	})
	if err != nil {
		if err == errInvalid {
			writeError(w, http.StatusUnprocessableEntity, "webhook secret is not configured")
			return
		}
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) webhookConnection(r *http.Request, provider string) *domain.IntegrationConnection {
	data, ok := s.store.BootstrapFor(workspaceKey(r))
	if !ok {
		return nil
	}
	for index := range data.IntegrationConnections {
		if data.IntegrationConnections[index].Provider == provider && data.IntegrationConnections[index].Status == "configured" {
			return &data.IntegrationConnections[index]
		}
	}
	return nil
}

func verifyCodeWebhook(provider string, connection *domain.IntegrationConnection, r *http.Request, body []byte) bool {
	secret := connection.Config["webhookSecret"]
	if envName := strings.TrimSpace(connection.Config["webhookSecretEnv"]); strings.HasPrefix(envName, "FLOW_INTEGRATION_") {
		secret = os.Getenv(envName)
	}
	if secret == "" {
		return false
	}
	if provider == "github" {
		raw := strings.TrimSpace(r.Header.Get("X-Hub-Signature-256"))
		if !strings.HasPrefix(raw, "sha256=") {
			return false
		}
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write(body)
		want, err := hex.DecodeString(strings.TrimPrefix(raw, "sha256="))
		return err == nil && hmac.Equal(mac.Sum(nil), want)
	}
	return hmac.Equal([]byte(secret), []byte(strings.TrimSpace(r.Header.Get("X-Gitlab-Token"))))
}

type externalCodeReviewEvent struct {
	Action      string `json:"action"`
	Number      int    `json:"number"`
	Title       string `json:"title"`
	Description string `json:"description"`
	ExternalURL string `json:"url"`
	Repository  struct {
		FullName string `json:"full_name"`
		Path     string `json:"path_with_namespace"`
	} `json:"repository"`
	Project struct {
		PathWithNamespace string `json:"path_with_namespace"`
		WebURL            string `json:"web_url"`
	} `json:"project"`
	PullRequest struct {
		Number  int    `json:"number"`
		ID      int64  `json:"id"`
		Title   string `json:"title"`
		Body    string `json:"body"`
		HTMLURL string `json:"html_url"`
		State   string `json:"state"`
		Merged  bool   `json:"merged"`
		User    struct {
			Login string `json:"login"`
		} `json:"user"`
		Base struct {
			Ref string `json:"ref"`
		} `json:"base"`
		Head struct {
			Ref string `json:"ref"`
			SHA string `json:"sha"`
		} `json:"head"`
		Additions int `json:"additions"`
		Deletions int `json:"deletions"`
		Commits   int `json:"commits"`
	} `json:"pull_request"`
	ObjectAttributes struct {
		ID           int64  `json:"id"`
		IID          int    `json:"iid"`
		Title        string `json:"title"`
		Description  string `json:"description"`
		URL          string `json:"url"`
		State        string `json:"state"`
		Action       string `json:"action"`
		SourceBranch string `json:"source_branch"`
		TargetBranch string `json:"target_branch"`
		LastCommit   struct {
			ID string `json:"id"`
		} `json:"last_commit"`
	} `json:"object_attributes"`
	User struct {
		Login    string `json:"login"`
		Username string `json:"username"`
	} `json:"user"`
	Reviewers []struct {
		Login    string `json:"login"`
		Username string `json:"username"`
	} `json:"requested_reviewers"`
	ReviewersGitlab []struct {
		Username string `json:"username"`
	} `json:"reviewers"`
}

func (e externalCodeReviewEvent) ExternalID() string {
	if e.PullRequest.ID != 0 {
		return strconv.FormatInt(e.PullRequest.ID, 10)
	}
	if e.ObjectAttributes.ID != 0 {
		return strconv.FormatInt(e.ObjectAttributes.ID, 10)
	}
	return strconv.Itoa(e.Number)
}

func (e externalCodeReviewEvent) toReview(provider string, actor domain.User, now time.Time) domain.CodeReview {
	r := domain.CodeReview{ID: fmt.Sprintf("review_%d", now.UnixNano()), SlugID: fmt.Sprintf("%s-%d", provider, e.Number), Provider: provider, ExternalID: e.ExternalID(), Number: e.Number, Status: "open", RepositoryOwner: "", RepositoryName: "", URL: e.ExternalURL, Author: actor, ReviewerIDs: []string{}, TeamReviewers: []string{}, IssueIDs: []string{}, BranchState: "upToDate", Checks: []domain.ReviewCheck{}, Files: []domain.ReviewFile{}, Events: []domain.ReviewEvent{}, CreatedAt: now, UpdatedAt: now}
	e.applyToReview(&r, now)
	return r
}

func (e externalCodeReviewEvent) applyToReview(r *domain.CodeReview, now time.Time) {
	if e.PullRequest.ID != 0 {
		r.Title, r.Description, r.URL = e.PullRequest.Title, e.PullRequest.Body, e.PullRequest.HTMLURL
		r.BaseBranch, r.HeadBranch = e.PullRequest.Base.Ref, e.PullRequest.Head.Ref
		r.Additions, r.Deletions, r.CommitCount = e.PullRequest.Additions, e.PullRequest.Deletions, e.PullRequest.Commits
		if e.PullRequest.Merged {
			r.Status = "merged"
		} else if e.PullRequest.State == "closed" {
			r.Status = "closed"
		}
	}
	if e.ObjectAttributes.ID != 0 {
		r.Title, r.Description, r.URL = e.ObjectAttributes.Title, e.ObjectAttributes.Description, e.ObjectAttributes.URL
		r.BaseBranch, r.HeadBranch = e.ObjectAttributes.TargetBranch, e.ObjectAttributes.SourceBranch
		if e.ObjectAttributes.State == "merged" {
			r.Status = "merged"
		} else if e.ObjectAttributes.State == "closed" {
			r.Status = "closed"
		}
	}
	if r.Title == "" {
		r.Title = e.Title
	}
	if e.Repository.FullName != "" {
		parts := strings.SplitN(e.Repository.FullName, "/", 2)
		if len(parts) == 2 {
			r.RepositoryOwner, r.RepositoryName = parts[0], parts[1]
		}
	} else if e.Repository.Path != "" {
		parts := strings.SplitN(e.Repository.Path, "/", 2)
		if len(parts) == 2 {
			r.RepositoryOwner, r.RepositoryName = parts[0], parts[1]
		}
	} else if e.Project.PathWithNamespace != "" {
		parts := strings.SplitN(e.Project.PathWithNamespace, "/", 2)
		if len(parts) == 2 {
			r.RepositoryOwner, r.RepositoryName = parts[0], parts[1]
		}
	}
	if r.URL == "" {
		r.URL = e.ExternalURL
	}
	r.UpdatedAt = now
}

func appendCodeReviewNotifications(data *domain.Bootstrap, review domain.CodeReview, event externalCodeReviewEvent, eventID string, now time.Time) []domain.Notification {
	actor := data.Viewer
	recipients := []string{data.Viewer.ID}
	for _, login := range reviewerLogins(event) {
		for _, user := range data.Users {
			local := strings.SplitN(user.Email, "@", 2)[0]
			if strings.EqualFold(login, user.Name) || strings.EqualFold(login, user.DisplayName) || strings.EqualFold(login, local) {
				recipients = append(recipients, user.ID)
			}
		}
	}
	recipients = uniqueStrings(recipients)
	result := append([]domain.Notification{}, data.Notifications...)
	for _, recipient := range recipients {
		result = append(result, domain.Notification{ID: fmt.Sprintf("notification_review_%d_%s", now.UnixNano(), recipient), RecipientID: recipient, Type: "codeReview", SourceType: "codeReview", SourceID: eventID, ReviewID: review.ID, Actor: actor, Category: "reviews", GroupKey: "review:" + review.ID, OccurrenceCount: 1, LatestActorIDs: []string{actor.ID}, CreatedAt: now, UpdatedAt: now})
	}
	return result
}

func reviewerLogins(event externalCodeReviewEvent) []string {
	result := []string{}
	for _, reviewer := range event.Reviewers {
		if reviewer.Login != "" {
			result = append(result, reviewer.Login)
		}
	}
	for _, reviewer := range event.ReviewersGitlab {
		if reviewer.Username != "" {
			result = append(result, reviewer.Username)
		}
	}
	return result
}
func reviewerIDs(data *domain.Bootstrap, event externalCodeReviewEvent) []string {
	result := []string{}
	for _, login := range reviewerLogins(event) {
		for _, user := range data.Users {
			local := strings.SplitN(user.Email, "@", 2)[0]
			if strings.EqualFold(login, user.Name) || strings.EqualFold(login, user.DisplayName) || strings.EqualFold(login, local) {
				result = append(result, user.ID)
			}
		}
	}
	return uniqueStrings(result)
}
func uniqueStrings(values []string) []string {
	result := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}
