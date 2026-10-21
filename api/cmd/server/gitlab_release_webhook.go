package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
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
	Assets struct {
		Links []struct {
			Name     string `json:"name"`
			URL      string `json:"url"`
			LinkType string `json:"link_type"`
		} `json:"links"`
		// Sources (zip/tar.gz archives of the tag) are not mapped: GitLab
		// generates them for every tag, they carry nothing release-specific
		// beyond the "GitLab release" link and would only crowd the
		// release's resources.
	} `json:"assets"`
}

const (
	gitlabReleaseMaxLinks     = 50
	gitlabReleaseMaxURLLength = 2048
	gitlabReleaseMaxNameRunes = 200
)

// gitlabReleaseLinkURL accepts only absolute http(s) URLs of bounded length.
func gitlabReleaseLinkURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > gitlabReleaseMaxURLLength {
		return "", false
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", false
	}
	return raw, true
}

// gitlabReleaseLinks maps the release page and assets.links onto Flow release
// links (name -> title, url) and counts the links dropped for an invalid URL
// or the cap. link_type (package, image, runbook, other) has no counterpart
// on Flow resources and is not stored. Duplicate URLs keep their first name.
func gitlabReleaseLinks(hook gitlabReleaseHook) ([]releaseCILink, int) {
	var links []releaseCILink
	rejected := 0
	add := func(address, name, fallback string) {
		address, ok := gitlabReleaseLinkURL(address)
		if !ok {
			rejected++
			return
		}
		if slices.ContainsFunc(links, func(item releaseCILink) bool { return item.URL == address }) {
			return
		}
		if len(links) >= gitlabReleaseMaxLinks {
			rejected++
			return
		}
		name = strings.TrimSpace(name)
		if name == "" {
			name = fallback
		}
		if runes := []rune(name); len(runes) > gitlabReleaseMaxNameRunes {
			name = string(runes[:gitlabReleaseMaxNameRunes])
		}
		links = append(links, releaseCILink{URL: address, Label: name})
	}
	if strings.TrimSpace(hook.URL) != "" {
		add(hook.URL, "GitLab release", "GitLab release")
	}
	for _, link := range hook.Assets.Links {
		add(link.URL, link.Name, strings.TrimSpace(link.URL))
	}
	return links, rejected
}

// reconcileReleaseSourceLinks makes the links owned by source match desired:
// owned links no longer present are removed, renamed ones are retitled, new
// ones are added. Links without that owner (added in Flow) are never touched,
// and a desired URL a user already has as a manual link is not duplicated.
func reconcileReleaseSourceLinks(release *domain.Release, source string, desired []releaseCILink, now time.Time) {
	release.Resources = slices.DeleteFunc(release.Resources, func(item domain.ReleaseResource) bool {
		return item.Type == "link" && item.Source == source && !slices.ContainsFunc(desired, func(link releaseCILink) bool { return link.URL == item.URL })
	})
	for index, link := range desired {
		existing := slices.IndexFunc(release.Resources, func(item domain.ReleaseResource) bool { return item.Type == "link" && item.URL == link.URL })
		switch {
		case existing < 0:
			release.Resources = append(release.Resources, domain.ReleaseResource{ID: fmt.Sprintf("release_resource_%d_g%d", now.UnixNano(), index), Type: "link", Title: link.Label, URL: link.URL, Source: source, CreatedAt: now})
		case release.Resources[existing].Source == source:
			release.Resources[existing].Title = link.Label
		}
	}
}

// handleGitLabReleaseHook applies an authenticated Release Hook and, when the
// delivery was accepted, stamps the GitLab connection's lastWebhookAt like
// merge request hooks do. A bad signature never reaches here; rejected
// payloads (4xx) do not count as a working webhook.
func (s *server) handleGitLabReleaseHook(w http.ResponseWriter, r *http.Request, body []byte) {
	recorder := &statusRecorder{ResponseWriter: w}
	s.applyGitLabReleaseHook(recorder, r, body)
	if recorder.status < 200 || recorder.status >= 300 {
		return
	}
	now := time.Now().UTC()
	scope := store.WithMutationScope(r.Context(), store.MutationScope{Resources: []string{"gitlab.release_hook"}})
	_ = s.store.MutateWorkspace(scope, workspaceKey(r), "gitlab.release_webhook", "gitlab.release_hook", map[string]string{}, func(data *domain.Bootstrap) error {
		for i := range data.IntegrationConnections {
			if data.IntegrationConnections[i].Provider == "gitlab" {
				data.IntegrationConnections[i].LastWebhookAt = &now
				data.IntegrationConnections[i].LastError = ""
			}
		}
		return nil
	})
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
func gitlabReleaseInput(hook gitlabReleaseHook) (releaseCIInput, int) {
	tag, name := strings.TrimSpace(hook.Tag), strings.TrimSpace(hook.Name)
	if name == "" {
		name = tag
	}
	description := hook.Description
	input := releaseCIInput{Name: name, Version: tag, Description: &description, CommitSHA: strings.TrimSpace(hook.Commit.ID), Stage: "completed"}
	var rejected int
	input.Links, rejected = gitlabReleaseLinks(hook)
	for _, identifier := range gitlabReleaseIssueKey.FindAllString(name+"\n"+hook.Description, -1) {
		identifier = strings.ToUpper(identifier)
		if !slices.ContainsFunc(input.IssueReferences, func(item releaseCIIssueReference) bool { return item.Identifier == identifier }) {
			input.IssueReferences = append(input.IssueReferences, releaseCIIssueReference{Identifier: identifier})
		}
	}
	return input, rejected
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
	input, rejectedLinks := gitlabReleaseInput(hook)
	outcome, err := s.applyReleaseCIOperation(r.Context(), releaseAppBaseURL(s, r), workspace, pipelineID, "sync", input, releaseCIOptions{source: "gitlab", looseVersionMatch: true})
	var ciErr releaseCIError
	switch {
	case errors.As(err, &ciErr):
		writeError(w, ciErr.status, ciErr.message)
	case err != nil:
		respondMutation(w, err, http.StatusAccepted, nil)
	default:
		writeJSON(w, http.StatusAccepted, map[string]any{"pipelineId": pipelineID, "created": outcome.created, "success": outcome.result.Success, "release": outcome.result.Release, "skippedIssueIdentifiers": outcome.result.SkippedIssueIdentifiers, "unknownIssueIdentifiers": outcome.result.UnknownIssueIdentifiers, "rejectedLinks": rejectedLinks})
	}
}
