package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// Deploy previews: preview environments deployed from a pull request's head
// branch. GitHub `deployment_status` and GitLab deployment webhooks update
// them automatically; other CI can report through
// PUT /api/reviews/{id}/previews. Linked issues show them beside the PR.

var deployPreviewStates = map[string]string{
	"pending": "pending", "queued": "pending", "created": "pending", "waiting": "pending",
	"in_progress": "building", "running": "building", "building": "building",
	"success": "ready", "ready": "ready",
	"failure": "failed", "failed": "failed", "error": "failed",
	"inactive": "inactive", "canceled": "inactive", "cancelled": "inactive", "skipped": "inactive",
}

type deploymentWebhook struct {
	Repository string
	Ref        string
	Preview    domain.DeployPreview
}

// parseDeploymentWebhook recognizes GitHub deployment_status and GitLab
// deployment events; other payloads return ok=false.
func parseDeploymentWebhook(provider string, header http.Header, body []byte) (deploymentWebhook, bool) {
	if provider == "github" && header.Get("X-GitHub-Event") == "deployment_status" {
		var event struct {
			Deployment struct {
				Environment string `json:"environment"`
				Ref         string `json:"ref"`
				SHA         string `json:"sha"`
			} `json:"deployment"`
			DeploymentStatus struct {
				State          string `json:"state"`
				EnvironmentURL string `json:"environment_url"`
				TargetURL      string `json:"target_url"`
				LogURL         string `json:"log_url"`
			} `json:"deployment_status"`
			Repository struct {
				FullName string `json:"full_name"`
			} `json:"repository"`
		}
		if json.Unmarshal(body, &event) != nil || event.Deployment.Ref == "" {
			return deploymentWebhook{}, false
		}
		return deploymentWebhook{Repository: event.Repository.FullName, Ref: event.Deployment.Ref, Preview: domain.DeployPreview{Provider: provider, Environment: event.Deployment.Environment, URL: firstNonEmpty(event.DeploymentStatus.EnvironmentURL, event.DeploymentStatus.TargetURL), LogURL: event.DeploymentStatus.LogURL, State: event.DeploymentStatus.State, CommitSHA: event.Deployment.SHA}}, true
	}
	if provider == "gitlab" {
		var event struct {
			ObjectKind             string `json:"object_kind"`
			Status                 string `json:"status"`
			Environment            string `json:"environment"`
			EnvironmentExternalURL string `json:"environment_external_url"`
			DeployableURL          string `json:"deployable_url"`
			Ref                    string `json:"ref"`
			SHA                    string `json:"sha"`
			Project                struct {
				PathWithNamespace string `json:"path_with_namespace"`
			} `json:"project"`
		}
		if json.Unmarshal(body, &event) != nil || event.ObjectKind != "deployment" || event.Ref == "" {
			return deploymentWebhook{}, false
		}
		return deploymentWebhook{Repository: event.Project.PathWithNamespace, Ref: event.Ref, Preview: domain.DeployPreview{Provider: provider, Environment: event.Environment, URL: event.EnvironmentExternalURL, LogURL: event.DeployableURL, State: event.Status, CommitSHA: event.SHA}}, true
	}
	return deploymentWebhook{}, false
}

func validPreviewURL(raw string) bool {
	if raw == "" {
		return true
	}
	parsed, err := url.Parse(raw)
	return err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != ""
}

// upsertDeployPreview records the preview on the review, keyed by environment.
// It reports whether the preview newly became ready.
func upsertDeployPreview(review *domain.CodeReview, preview domain.DeployPreview, now time.Time) (bool, error) {
	state, ok := deployPreviewStates[strings.ToLower(strings.TrimSpace(preview.State))]
	if !ok {
		return false, fmt.Errorf("unknown preview state %q", preview.State)
	}
	preview.State = state
	preview.Environment = strings.TrimSpace(preview.Environment)
	if preview.Environment == "" {
		preview.Environment = "Preview"
	}
	if !validPreviewURL(preview.URL) || !validPreviewURL(preview.LogURL) {
		return false, fmt.Errorf("preview URLs must be http(s)")
	}
	index := slices.IndexFunc(review.Previews, func(item domain.DeployPreview) bool {
		return strings.EqualFold(item.Environment, preview.Environment)
	})
	if index < 0 {
		preview.ID, preview.CreatedAt, preview.UpdatedAt = fmt.Sprintf("preview_%d", now.UnixNano()), now, now
		review.Previews = append(review.Previews, preview)
		return state == "ready", nil
	}
	current := review.Previews[index]
	becameReady := state == "ready" && current.State != "ready"
	current.State, current.UpdatedAt, current.Provider = state, now, firstNonEmpty(preview.Provider, current.Provider)
	if preview.URL != "" {
		current.URL = preview.URL
	}
	if preview.LogURL != "" {
		current.LogURL = preview.LogURL
	}
	if preview.CommitSHA != "" {
		current.CommitSHA = preview.CommitSHA
	}
	review.Previews[index] = current
	return becameReady, nil
}

func recordPreviewActivity(data *domain.Bootstrap, review domain.CodeReview, environment string) {
	preview := review.Previews[slices.IndexFunc(review.Previews, func(item domain.DeployPreview) bool { return strings.EqualFold(item.Environment, environment) })]
	for _, issueID := range review.IssueIDs {
		appendActivity(data, issueID, "issue.preview_ready", data.Viewer, map[string]string{"reviewId": review.ID, "reviewTitle": review.Title, "environment": preview.Environment, "url": preview.URL})
	}
}

func (s *server) applyDeploymentWebhook(w http.ResponseWriter, r *http.Request, provider, eventID string, event deploymentWebhook) {
	if _, known := deployPreviewStates[strings.ToLower(strings.TrimSpace(event.Preview.State))]; !known {
		writeJSON(w, http.StatusAccepted, map[string]any{"ignored": true, "reason": "unknown deployment state"})
		return
	}
	matched := 0
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "code_review.preview", eventID, map[string]any{"provider": provider, "ref": event.Ref, "environment": event.Preview.Environment, "state": event.Preview.State}, func(data *domain.Bootstrap) error {
		now := time.Now().UTC()
		for index := range data.Reviews {
			review := &data.Reviews[index]
			if review.Provider != provider || review.HeadBranch != event.Ref || (event.Repository != "" && !strings.EqualFold(review.RepositoryOwner+"/"+review.RepositoryName, event.Repository)) {
				continue
			}
			if review.Status == "merged" || review.Status == "closed" {
				continue
			}
			ready, err := upsertDeployPreview(review, event.Preview, now)
			if err != nil {
				return err
			}
			review.UpdatedAt = now
			matched++
			if ready {
				recordPreviewActivity(data, *review, firstNonEmpty(event.Preview.Environment, "Preview"))
			}
		}
		for connectionIndex := range data.IntegrationConnections {
			if data.IntegrationConnections[connectionIndex].Provider == provider {
				data.IntegrationConnections[connectionIndex].LastWebhookAt = &now
			}
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"eventId": eventID, "previews": matched})
}

func (s *server) putReviewPreview(w http.ResponseWriter, r *http.Request) {
	var input domain.DeployPreviewInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.URL) == "" && input.State == "ready" {
		writeError(w, http.StatusBadRequest, "a ready preview needs a url")
		return
	}
	var updated domain.CodeReview
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "code_review.preview", r.PathValue("id"), input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.Reviews, func(item domain.CodeReview) bool { return item.ID == r.PathValue("id") || item.SlugID == r.PathValue("id") })
		if index < 0 {
			return errNotFound
		}
		now := time.Now().UTC()
		review := &data.Reviews[index]
		ready, err := upsertDeployPreview(review, domain.DeployPreview{Provider: firstNonEmpty(input.Provider, "api"), Environment: input.Environment, URL: strings.TrimSpace(input.URL), LogURL: strings.TrimSpace(input.LogURL), State: input.State, CommitSHA: input.CommitSHA}, now)
		if err != nil {
			return fmt.Errorf("%w: %v", errInvalid, err)
		}
		review.UpdatedAt = now
		if ready {
			recordPreviewActivity(data, *review, firstNonEmpty(strings.TrimSpace(input.Environment), "Preview"))
		}
		updated = *review
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}
