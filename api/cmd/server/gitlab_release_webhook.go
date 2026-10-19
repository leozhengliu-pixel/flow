package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

// GitLab Release Hooks mirror a project's releases into a release pipeline.
// They arrive on the connection webhook (/api/integrations/gitlab/webhook),
// authenticated by X-Gitlab-Token like merge-request hooks, and go through
// the CI API's sync mutation so issue linking, frozen stages, release history
// and completion automations behave exactly as for /api/release-ci/sync.
// Tag pushes are not releases (a tag can exist without a published release)
// and stay ignored.

type gitlabReleaseHook struct {
	ObjectKind  string `json:"object_kind"`
	Action      string `json:"action"`
	Tag         string `json:"tag"`
	Name        string `json:"name"`
	Description string `json:"description"`
	URL         string `json:"url"`
	Commit      struct {
		ID string `json:"id"`
	} `json:"commit"`
}

// gitlabReleaseIssueKey finds issue identifiers (KEY-123) in release names and notes.
var gitlabReleaseIssueKey = regexp.MustCompile(`\b[A-Za-z][A-Za-z0-9]{0,9}-[0-9]{1,7}\b`)

// isGitLabReleaseHook reports whether a GitLab delivery is a Release Hook.
func isGitLabReleaseHook(provider string, header http.Header, body []byte) bool {
	if provider != "gitlab" {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(header.Get("X-Gitlab-Event")), "Release Hook") {
		return true
	}
	var event struct {
		ObjectKind string `json:"object_kind"`
	}
	return json.Unmarshal(body, &event) == nil && event.ObjectKind == "release"
}

// gitlabReleaseInput maps a Release Hook onto a CI sync that completes the release.
func gitlabReleaseInput(hook gitlabReleaseHook) releaseCIInput {
	tag, name := strings.TrimSpace(hook.Tag), strings.TrimSpace(hook.Name)
	if name == "" {
		name = tag
	}
	description := hook.Description
	input := releaseCIInput{Name: name, Version: tag, Description: &description, CommitSHA: strings.TrimSpace(hook.Commit.ID), Stage: "completed"}
	if address := strings.TrimSpace(hook.URL); address != "" {
		input.Links = []releaseCILink{{URL: address, Label: "GitLab release"}}
	}
	for _, identifier := range gitlabReleaseIssueKey.FindAllString(name+"\n"+hook.Description, -1) {
		identifier = strings.ToUpper(identifier)
		if !slices.ContainsFunc(input.IssueReferences, func(item releaseCIIssueReference) bool { return item.Identifier == identifier }) {
			input.IssueReferences = append(input.IssueReferences, releaseCIIssueReference{Identifier: identifier})
		}
	}
	return input
}

// gitlabReleasePipeline resolves the hook's target pipeline: ?pipeline=<id or
// slugId>, else the workspace's only pipeline. It returns "" with a reason
// when no pipeline is selected.
func gitlabReleasePipeline(data domain.Bootstrap, selector string) (string, string, int) {
	if selector = strings.TrimSpace(selector); selector != "" {
		index := slices.IndexFunc(data.ReleasePipelines, func(item domain.ReleasePipeline) bool { return item.ID == selector })
		if index < 0 {
			index = slices.IndexFunc(data.ReleasePipelines, func(item domain.ReleasePipeline) bool { return strings.EqualFold(item.SlugID, selector) })
		}
		if index < 0 {
			return "", fmt.Sprintf("release pipeline %q not found", selector), http.StatusNotFound
		}
		return data.ReleasePipelines[index].ID, "", 0
	}
	if len(data.ReleasePipelines) == 1 {
		return data.ReleasePipelines[0].ID, "", 0
	}
	return "", fmt.Sprintf("no release pipeline is selected: the workspace has %d release pipelines; add ?pipeline=<pipeline id or slugId> to the webhook URL", len(data.ReleasePipelines)), http.StatusAccepted
}

// applyGitLabReleaseHook handles a verified GitLab Release Hook.
func (s *server) applyGitLabReleaseHook(w http.ResponseWriter, r *http.Request, body []byte) {
	var hook gitlabReleaseHook
	if err := json.Unmarshal(body, &hook); err != nil {
		writeError(w, http.StatusBadRequest, "invalid webhook payload")
		return
	}
	switch hook.Action {
	case "create", "update":
	case "delete":
		writeJSON(w, http.StatusAccepted, map[string]any{"ignored": true, "reason": "GitLab release deleted; Flow releases are kept"})
		return
	default:
		writeJSON(w, http.StatusAccepted, map[string]any{"ignored": true, "reason": fmt.Sprintf("GitLab release action %q is not handled", hook.Action)})
		return
	}
	if strings.TrimSpace(hook.Tag) == "" {
		writeError(w, http.StatusBadRequest, "GitLab release tag is required")
		return
	}
	workspace := workspaceKey(r)
	data, ok := s.store.WorkspaceMetadata(workspace)
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	pipelineID, reason, status := gitlabReleasePipeline(data, r.URL.Query().Get("pipeline"))
	if pipelineID == "" {
		if status == http.StatusAccepted {
			writeJSON(w, status, map[string]any{"ignored": true, "reason": reason})
		} else {
			writeError(w, status, reason)
		}
		return
	}
	outcome, err := s.applyReleaseCIOperation(r.Context(), releaseAppBaseURL(s, r), workspace, pipelineID, "sync", gitlabReleaseInput(hook), releaseCIOptions{source: "gitlab", looseVersionMatch: true})
	var ciErr releaseCIError
	switch {
	case errors.As(err, &ciErr):
		writeError(w, ciErr.status, ciErr.message)
	case err != nil:
		respondMutation(w, err, http.StatusAccepted, nil)
	default:
		writeJSON(w, http.StatusAccepted, map[string]any{"pipelineId": pipelineID, "created": outcome.created, "success": outcome.result.Success, "release": outcome.result.Release, "skippedIssueIdentifiers": outcome.result.SkippedIssueIdentifiers, "unknownIssueIdentifiers": outcome.result.UnknownIssueIdentifiers})
	}
}
