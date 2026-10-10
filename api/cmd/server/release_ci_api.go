package main

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Release pipeline access keys and the CI API they unlock. The shape follows
// Linear's access-key operations used by its release CLI and GitHub Action:
// read the pipeline settings, list recent releases, sync (create a release or
// add issues to the current one), complete and update (move to a stage).

// releaseAccessKeyGrace is how long a rotated or soft-revoked key keeps working.
const releaseAccessKeyGrace = time.Hour

type releasePipelineAccessKey struct {
	PipelineID string    `json:"pipelineId"`
	Prefix     string    `json:"prefix"`
	Secret     string    `json:"secret"`
	CreatedAt  time.Time `json:"createdAt"`
	// PreviousKeyExpiresAt is set when the replaced key keeps working for a grace period.
	PreviousKeyExpiresAt *time.Time `json:"previousKeyExpiresAt,omitempty"`
}

type releaseAccessKeyRotateInput struct {
	RevokeImmediately bool `json:"revokeImmediately"`
}

func releaseAccessKeyActive(pipeline *domain.ReleasePipeline, now time.Time) bool {
	return pipeline.AccessKeyHash != "" && (pipeline.AccessKeyRevokedAt == nil || now.Before(*pipeline.AccessKeyRevokedAt))
}

// releasePipelineKeyMatches accepts the current key, or a rotated key during its grace period.
func releasePipelineKeyMatches(pipeline *domain.ReleasePipeline, secret string, now time.Time) bool {
	hash := []byte(secretHash(secret))
	if releaseAccessKeyActive(pipeline, now) && subtle.ConstantTimeCompare([]byte(pipeline.AccessKeyHash), hash) == 1 {
		return true
	}
	return pipeline.PreviousAccessKeyHash != "" && pipeline.PreviousAccessKeyExpiresAt != nil && now.Before(*pipeline.PreviousAccessKeyExpiresAt) && subtle.ConstantTimeCompare([]byte(pipeline.PreviousAccessKeyHash), hash) == 1
}

// releaseAccessKeySecret reads the key from "Authorization: Bearer <key>" or a
// bare "Authorization: <key>" like Linear's SDK sends.
func releaseAccessKeySecret(r *http.Request) string {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(header) > 7 && strings.EqualFold(header[:7], "bearer ") {
		return strings.TrimSpace(header[7:])
	}
	if strings.HasPrefix(header, "flow_release_") {
		return header
	}
	return ""
}

// pipelineForAccessKey finds the workspace and pipeline an access key belongs
// to; pipelineID narrows the lookup when the URL names the pipeline.
func (s *server) pipelineForAccessKey(secret, pipelineID string) (string, string, bool) {
	if secret == "" {
		return "", "", false
	}
	now := time.Now().UTC()
	for _, key := range s.store.WorkspaceKeys() {
		data, ok := s.store.WorkspaceMetadata(key)
		if !ok {
			continue
		}
		for index := range data.ReleasePipelines {
			pipeline := &data.ReleasePipelines[index]
			if (pipelineID == "" || pipeline.ID == pipelineID) && releasePipelineKeyMatches(pipeline, secret, now) {
				return key, pipeline.ID, true
			}
		}
	}
	return "", "", false
}

// readOptionalJSON decodes a request body that may be empty.
func readOptionalJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	if r.Body == nil {
		return true
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 || string(body) == "null" {
		return true
	}
	if err := json.Unmarshal(body, target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

// rotateReleasePipelineAccessKey creates the pipeline's key, or rotates it. A
// rotated key keeps working for an hour unless revokeImmediately is set.
func (s *server) rotateReleasePipelineAccessKey(w http.ResponseWriter, r *http.Request) {
	var input releaseAccessKeyRotateInput
	if !readOptionalJSON(w, r, &input) {
		return
	}
	secret, err := randomSecret("flow_release_")
	if err != nil {
		respondMutation(w, err, http.StatusCreated, nil)
		return
	}
	id := r.PathValue("id")
	var result releasePipelineAccessKey
	role, teamRoles := s.requestWorkspaceRole(r)
	err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "release_pipeline.access_key_rotated", id, input, func(data *domain.Bootstrap) error {
		pipeline := releasePipelineByID(data, id)
		if pipeline == nil {
			return errNotFound
		}
		if !releasePipelineAdministrable(data, pipeline.TeamIDs, teamRoles, role) {
			return store.ErrAuthForbidden
		}
		now := time.Now().UTC()
		if releaseAccessKeyActive(pipeline, now) && !input.RevokeImmediately {
			expires := now.Add(releaseAccessKeyGrace)
			if pipeline.AccessKeyRevokedAt != nil && pipeline.AccessKeyRevokedAt.Before(expires) {
				expires = *pipeline.AccessKeyRevokedAt
			}
			pipeline.PreviousAccessKeyHash, pipeline.PreviousAccessKeyExpiresAt = pipeline.AccessKeyHash, &expires
		} else {
			pipeline.PreviousAccessKeyHash, pipeline.PreviousAccessKeyExpiresAt = "", nil
		}
		prefix := secret[:min(len(secret), 21)]
		pipeline.AccessKeyPrefix, pipeline.AccessKeyHash, pipeline.AccessKeyCreatedAt, pipeline.UpdatedAt = prefix, secretHash(secret), &now, now
		pipeline.AccessKeyLastUsedAt, pipeline.AccessKeyRevokedAt = nil, nil
		result = releasePipelineAccessKey{PipelineID: id, Prefix: prefix, Secret: secret, CreatedAt: now, PreviousKeyExpiresAt: pipeline.PreviousAccessKeyExpiresAt}
		return nil
	})
	respondMutation(w, err, http.StatusCreated, result)
}

// revokeReleasePipelineAccessKey stops the key after the grace period, or at
// once with ?immediate=true (also when a revocation is already scheduled).
func (s *server) revokeReleasePipelineAccessKey(w http.ResponseWriter, r *http.Request) {
	immediate, ok := queryBool(w, r, "immediate")
	if !ok {
		return
	}
	id := r.PathValue("id")
	var updated domain.ReleasePipeline
	role, teamRoles := s.requestWorkspaceRole(r)
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "release_pipeline.access_key_revoked", id, map[string]bool{"immediate": immediate}, func(data *domain.Bootstrap) error {
		pipeline := releasePipelineByID(data, id)
		if pipeline == nil {
			return errNotFound
		}
		if !releasePipelineAdministrable(data, pipeline.TeamIDs, teamRoles, role) {
			return store.ErrAuthForbidden
		}
		now := time.Now().UTC()
		if !releaseAccessKeyActive(pipeline, now) && pipeline.PreviousAccessKeyHash == "" {
			return errNotFound
		}
		if immediate || pipeline.AccessKeyRevokedAt != nil || !releaseAccessKeyActive(pipeline, now) {
			pipeline.AccessKeyPrefix, pipeline.AccessKeyHash, pipeline.AccessKeyCreatedAt = "", "", nil
			pipeline.AccessKeyLastUsedAt, pipeline.AccessKeyRevokedAt = nil, nil
			pipeline.PreviousAccessKeyHash, pipeline.PreviousAccessKeyExpiresAt = "", nil
		} else {
			at := now.Add(releaseAccessKeyGrace)
			pipeline.AccessKeyRevokedAt = &at
			if pipeline.PreviousAccessKeyExpiresAt != nil && pipeline.PreviousAccessKeyExpiresAt.After(at) {
				pipeline.PreviousAccessKeyExpiresAt = &at
			}
		}
		pipeline.UpdatedAt = now
		updated = publicReleasePipeline(*pipeline)
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

type releaseCIIssueReference struct {
	Identifier string `json:"identifier"`
	CommitSHA  string `json:"commitSha,omitempty"`
}

type releaseCILink struct {
	URL   string `json:"url"`
	Label string `json:"label,omitempty"`
}

type releaseCIDocument struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

// releaseCIInput is the body of the sync, complete, update and legacy event
// endpoints. Field names match Linear's ReleaseSyncInput /
// ReleaseCompleteInput / ReleaseUpdateByPipelineInput.
type releaseCIInput struct {
	Name                    string                    `json:"name,omitempty"`
	Version                 string                    `json:"version,omitempty"`
	Description             *string                   `json:"description,omitempty"`
	CommitSHA               string                    `json:"commitSha,omitempty"`
	PreserveStoredCommitSHA bool                      `json:"preserveStoredCommitSha,omitempty"`
	Stage                   string                    `json:"stage,omitempty"`
	IssueReferences         []releaseCIIssueReference `json:"issueReferences,omitempty"`
	RevertedIssueReferences []releaseCIIssueReference `json:"revertedIssueReferences,omitempty"`
	Links                   []releaseCILink           `json:"links,omitempty"`
	Documents               []releaseCIDocument       `json:"documents,omitempty"`
	// ReleaseNotes is markdown, or an object with a content/body field.
	ReleaseNotes          json.RawMessage  `json:"releaseNotes,omitempty"`
	PullRequestReferences []map[string]any `json:"pullRequestReferences,omitempty"`
	Repository            map[string]any   `json:"repository,omitempty"`
}

type releaseCIStage struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Frozen bool   `json:"frozen,omitempty"`
}

type releaseCIRelease struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Version    string          `json:"version,omitempty"`
	CommitSHA  string          `json:"commitSha,omitempty"`
	URL        string          `json:"url"`
	Status     string          `json:"status"`
	Stage      *releaseCIStage `json:"stage"`
	IssueCount int             `json:"issueCount"`
	CreatedAt  time.Time       `json:"createdAt"`
	ReleasedAt *time.Time      `json:"releasedAt,omitempty"`
}

type releaseCIResult struct {
	Success bool              `json:"success"`
	Release *releaseCIRelease `json:"release"`
	// SkippedIssueIdentifiers were not added because the release's stage is frozen.
	SkippedIssueIdentifiers []string `json:"skippedIssueIdentifiers,omitempty"`
	// UnknownIssueIdentifiers matched no issue in the workspace.
	UnknownIssueIdentifiers []string `json:"unknownIssueIdentifiers,omitempty"`
}

type releaseCIPipeline struct {
	ID                  string           `json:"id"`
	Name                string           `json:"name"`
	SlugID              string           `json:"slugId"`
	Type                string           `json:"type"`
	Production          bool             `json:"production"`
	IncludePathPatterns []string         `json:"includePathPatterns"`
	Stages              []releaseCIStage `json:"stages"`
	URL                 string           `json:"url"`
}

// releaseCIError carries a status and message out of a mutation.
type releaseCIError struct {
	status  int
	message string
}

func (e releaseCIError) Error() string { return e.message }

func ciError(status int, format string, args ...any) error {
	return releaseCIError{status: status, message: fmt.Sprintf(format, args...)}
}

func releaseAppBaseURL(s *server, r *http.Request) string {
	if value := strings.TrimRight(strings.TrimSpace(os.Getenv("FLOW_APP_URL")), "/"); value != "" {
		return value
	}
	if value := strings.TrimRight(strings.TrimSpace(s.allowedOrigin), "/"); value != "" && value != "*" {
		return value
	}
	return externalBaseURL(r)
}

func releaseWebURL(base, workspace string, pipeline *domain.ReleasePipeline, release domain.Release) string {
	return fmt.Sprintf("%s/%s/pipeline/%s/release/%s/issues", base, url.PathEscape(workspace), url.PathEscape(pipeline.SlugID), url.PathEscape(release.SlugID))
}

func releaseCIView(base, workspace string, pipeline *domain.ReleasePipeline, release domain.Release) *releaseCIRelease {
	view := &releaseCIRelease{ID: release.ID, Name: release.Name, Version: release.Version, CommitSHA: release.CommitSHA, URL: releaseWebURL(base, workspace, pipeline, release), Status: release.Status, IssueCount: len(release.IssueIDs), CreatedAt: release.CreatedAt, ReleasedAt: release.ReleasedAt}
	if release.Stage != "" {
		view.Stage = &releaseCIStage{Name: release.Stage, Status: pipeline.StageStatuses[release.Stage], Frozen: slices.Contains(pipeline.FrozenStages, release.Stage)}
	}
	return view
}

func releaseCIStages(pipeline *domain.ReleasePipeline) []releaseCIStage {
	stages := make([]releaseCIStage, 0, len(pipeline.Stages))
	for _, stage := range pipeline.Stages {
		stages = append(stages, releaseCIStage{Name: stage, Status: pipeline.StageStatuses[stage], Frozen: slices.Contains(pipeline.FrozenStages, stage)})
	}
	return stages
}

// authenticateReleaseCI resolves the access key and records its use. It
// writes the error response itself.
func (s *server) authenticateReleaseCI(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	w.Header().Set("Cache-Control", "no-store")
	workspace, pipelineID, ok := s.pipelineForAccessKey(releaseAccessKeySecret(r), r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid release pipeline access key")
		return "", "", false
	}
	return workspace, pipelineID, true
}

// touchReleaseAccessKey records key use for read-only calls, at most once a minute.
func (s *server) touchReleaseAccessKey(r *http.Request, workspace, pipelineID string) {
	data, ok := s.store.WorkspaceMetadata(workspace)
	if !ok {
		return
	}
	pipeline := releasePipelineByID(&data, pipelineID)
	now := time.Now().UTC()
	if pipeline == nil || pipeline.AccessKeyLastUsedAt != nil && now.Sub(*pipeline.AccessKeyLastUsedAt) < time.Minute {
		return
	}
	_ = s.store.MutateWorkspace(r.Context(), workspace, "release_pipeline.access_key_used", pipelineID, nil, func(data *domain.Bootstrap) error {
		if pipeline := releasePipelineByID(data, pipelineID); pipeline != nil {
			pipeline.AccessKeyLastUsedAt = &now
		}
		return nil
	})
}

// releaseCIPipelineSettings mirrors Linear's releasePipelineByAccessKey.
func (s *server) releaseCIPipelineSettings(w http.ResponseWriter, r *http.Request) {
	workspace, pipelineID, ok := s.authenticateReleaseCI(w, r)
	if !ok {
		return
	}
	data, found := s.store.WorkspaceMetadata(workspace)
	pipeline := releasePipelineByID(&data, pipelineID)
	if !found || pipeline == nil {
		writeError(w, http.StatusNotFound, "release pipeline not found")
		return
	}
	s.touchReleaseAccessKey(r, workspace, pipelineID)
	patterns := slices.Clone(pipeline.PathFilters)
	if patterns == nil {
		patterns = []string{}
	}
	writeJSON(w, http.StatusOK, releaseCIPipeline{ID: pipeline.ID, Name: pipeline.Name, SlugID: pipeline.SlugID, Type: pipeline.Type, Production: pipeline.Production, IncludePathPatterns: patterns, Stages: releaseCIStages(pipeline), URL: fmt.Sprintf("%s/%s/pipeline/%s/releases", releaseAppBaseURL(s, r), url.PathEscape(data.Workspace.URLKey), url.PathEscape(pipeline.SlugID))})
}

// releaseCIRecentReleases mirrors Linear's recentReleasesByAccessKey: newest first, limit 1–50 (default 20).
func (s *server) releaseCIRecentReleases(w http.ResponseWriter, r *http.Request) {
	workspace, pipelineID, ok := s.authenticateReleaseCI(w, r)
	if !ok {
		return
	}
	limit := 20
	if value := strings.TrimSpace(r.URL.Query().Get("limit")); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 50 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 50")
			return
		}
		limit = parsed
	}
	data, found := s.store.WorkspaceMetadata(workspace)
	pipeline := releasePipelineByID(&data, pipelineID)
	if !found || pipeline == nil {
		writeError(w, http.StatusNotFound, "release pipeline not found")
		return
	}
	s.touchReleaseAccessKey(r, workspace, pipelineID)
	releases := []domain.Release{}
	for _, release := range data.Releases {
		if release.PipelineID == pipelineID && release.ArchivedAt == nil {
			releases = append(releases, release)
		}
	}
	sort.SliceStable(releases, func(i, j int) bool { return releases[i].CreatedAt.After(releases[j].CreatedAt) })
	base := releaseAppBaseURL(s, r)
	result := []*releaseCIRelease{}
	for _, release := range releases[:min(limit, len(releases))] {
		result = append(result, releaseCIView(base, data.Workspace.URLKey, pipeline, release))
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) releaseCISync(w http.ResponseWriter, r *http.Request) {
	s.releaseCIMutation(w, r, "sync")
}

func (s *server) releaseCIComplete(w http.ResponseWriter, r *http.Request) {
	s.releaseCIMutation(w, r, "complete")
}

func (s *server) releaseCIUpdate(w http.ResponseWriter, r *http.Request) {
	s.releaseCIMutation(w, r, "update")
}

// receiveReleasePipelineEvent is the original per-pipeline event endpoint:
// upsert a release by version and move it to a stage (default: the first
// started stage). It also accepts the sync fields (issues, links, documents,
// notes).
func (s *server) receiveReleasePipelineEvent(w http.ResponseWriter, r *http.Request) {
	s.releaseCIMutation(w, r, "event")
}

func normalizeReleaseCIInput(input *releaseCIInput) {
	input.Name, input.Version, input.CommitSHA, input.Stage = strings.TrimSpace(input.Name), strings.TrimSpace(input.Version), strings.TrimSpace(input.CommitSHA), strings.TrimSpace(input.Stage)
}

func releaseCIIdentifiers(input releaseCIInput) []string {
	identifiers := []string{}
	for _, reference := range append(slices.Clone(input.IssueReferences), input.RevertedIssueReferences...) {
		if identifier := strings.ToUpper(strings.TrimSpace(reference.Identifier)); identifier != "" && !slices.Contains(identifiers, identifier) {
			identifiers = append(identifiers, identifier)
		}
	}
	return identifiers
}

// releaseNotesText accepts a markdown string or {"content"|"body"|"markdown": "..."}.
func releaseNotesText(raw json.RawMessage) (string, bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return "", false, nil
	}
	var text string
	if err := json.Unmarshal(trimmed, &text); err == nil {
		return text, true, nil
	}
	var object map[string]any
	if err := json.Unmarshal(trimmed, &object); err != nil {
		return "", false, errInvalid
	}
	for _, key := range []string{"content", "body", "markdown"} {
		if value, ok := object[key].(string); ok {
			return value, true, nil
		}
	}
	return "", false, errInvalid
}

func (s *server) releaseCIMutation(w http.ResponseWriter, r *http.Request, operation string) {
	workspace, pipelineID, ok := s.authenticateReleaseCI(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	var input releaseCIInput
	if !decodeJSON(w, r, &input) {
		return
	}
	normalizeReleaseCIInput(&input)
	notes, hasNotes, notesErr := releaseNotesText(input.ReleaseNotes)
	if notesErr != nil {
		writeError(w, http.StatusBadRequest, "releaseNotes must be a string or an object with content")
		return
	}
	if operation == "update" && input.Stage == "" {
		writeError(w, http.StatusBadRequest, "stage is required")
		return
	}
	if operation == "event" && input.Version == "" && input.Name == "" {
		writeError(w, http.StatusBadRequest, "name or version is required")
		return
	}
	identifiers := releaseCIIdentifiers(input)
	// Completion automations and frozen checks need the pipeline's open releases' issues loaded.
	ctx := store.WithMutationScope(r.Context(), store.MutationScope{IssueIdentifiers: identifiers, Resolve: func(data domain.Bootstrap) store.MutationScope {
		extra := store.MutationScope{}
		for _, release := range data.Releases {
			if release.PipelineID == pipelineID && release.ArchivedAt == nil && (release.Status == "planned" || release.Status == "inProgress" || input.Version != "" && release.Version == input.Version) {
				extra.IssueIDs = append(extra.IssueIDs, release.IssueIDs...)
			}
		}
		return extra
	}})
	base := releaseAppBaseURL(s, r)
	result := releaseCIResult{Success: true}
	created := false
	var final domain.Release
	logged := input
	logged.ReleaseNotes = nil
	err := s.store.MutateWorkspace(ctx, workspace, "release.ci_"+operation, pipelineID, logged, func(data *domain.Bootstrap) error {
		pipeline := releasePipelineByID(data, pipelineID)
		if pipeline == nil {
			return ciError(http.StatusNotFound, "release pipeline not found")
		}
		now := time.Now().UTC()
		pipeline.AccessKeyLastUsedAt = &now
		index, isNew, err := releaseCITarget(data, pipeline, input, operation, now)
		if err != nil {
			return err
		}
		created = isNew
		previousStatus := ""
		if !isNew {
			previousStatus = data.Releases[index].Status
		}
		release := &data.Releases[index]
		if input.Name != "" {
			release.Name = input.Name
		}
		if input.Description != nil {
			release.Description = *input.Description
		}
		if input.CommitSHA != "" && !(input.PreserveStoredCommitSHA && release.CommitSHA != "") {
			release.CommitSHA = input.CommitSHA
		}
		result.SkippedIssueIdentifiers, result.UnknownIssueIdentifiers = applyReleaseCIIssues(data, release, input)
		applyReleaseCIResources(release, input, now)
		if hasNotes {
			setReleaseNotes(data, release, notes, now, "note_updated")
		}
		if err := moveReleaseCIStage(data, pipeline, release, input, operation, now); err != nil {
			return err
		}
		release.UpdatedAt = now
		data.ReleaseHistory = append(data.ReleaseHistory, domain.ReleaseHistory{ID: fmt.Sprintf("release_history_%d", now.UnixNano()), ReleaseID: release.ID, Actor: data.Viewer, Action: "ci_" + operation, Metadata: map[string]any{"stage": release.Stage, "commitSha": release.CommitSHA, "issues": len(input.IssueReferences)}, CreatedAt: now})
		releaseID := release.ID
		if err := applyReleaseSettingAutomations(data, previousStatus, release, now); err != nil {
			return err
		}
		// Re-read by id: completion may have appended a follow-up release.
		final = data.Releases[slices.IndexFunc(data.Releases, func(item domain.Release) bool { return item.ID == releaseID })]
		result.Release = releaseCIView(base, data.Workspace.URLKey, pipeline, final)
		return nil
	})
	var ciErr releaseCIError
	switch {
	case errors.As(err, &ciErr):
		writeError(w, ciErr.status, ciErr.message)
	case err != nil:
		respondMutation(w, err, http.StatusOK, nil)
	case operation == "event":
		// The original event endpoint answers with the release itself.
		writeJSON(w, map[bool]int{true: http.StatusCreated, false: http.StatusOK}[created], final)
	case created:
		writeJSON(w, http.StatusCreated, result)
	default:
		writeJSON(w, http.StatusOK, result)
	}
}

func shortCommitSHA(sha string) string {
	return sha[:min(len(sha), 7)]
}

// latestPipelineRelease returns the newest unarchived release with a status
// (by start date for started releases, creation date otherwise), or -1.
func latestPipelineRelease(data *domain.Bootstrap, pipelineID, status string) int {
	best := -1
	for index, item := range data.Releases {
		if item.PipelineID != pipelineID || item.ArchivedAt != nil || item.Status != status {
			continue
		}
		if best < 0 || releaseCITime(item).After(releaseCITime(data.Releases[best])) {
			best = index
		}
	}
	return best
}

func releaseCITime(release domain.Release) time.Time {
	if release.Status == "inProgress" && release.StartedAt != nil {
		return *release.StartedAt
	}
	return release.CreatedAt
}

func releaseByVersion(data *domain.Bootstrap, pipelineID, version string) int {
	return slices.IndexFunc(data.Releases, func(item domain.Release) bool {
		return item.PipelineID == pipelineID && item.ArchivedAt == nil && version != "" && item.Version == version
	})
}

// releaseCITarget finds the release an operation acts on, creating one for
// sync/event when needed. It returns the release index and whether it is new.
func releaseCITarget(data *domain.Bootstrap, pipeline *domain.ReleasePipeline, input releaseCIInput, operation string, now time.Time) (int, bool, error) {
	if operation != "sync" && operation != "event" && pipeline.Type == "continuous" {
		return 0, false, ciError(http.StatusBadRequest, "%s is only available for scheduled pipelines; continuous pipelines create completed releases on sync", operation)
	}
	version := input.Version
	if index := releaseByVersion(data, pipeline.ID, version); index >= 0 {
		return index, false, nil
	}
	switch operation {
	case "complete", "update":
		if version != "" {
			return 0, false, ciError(http.StatusNotFound, "release %q not found in this pipeline", version)
		}
		if index := latestPipelineRelease(data, pipeline.ID, "inProgress"); index >= 0 {
			return index, false, nil
		}
		if operation == "update" {
			if index := latestPipelineRelease(data, pipeline.ID, "planned"); index >= 0 {
				return index, false, nil
			}
		}
		return 0, false, ciError(http.StatusNotFound, "no started release to %s; pass a version", operation)
	case "sync":
		if pipeline.Type == "scheduled" && version == "" {
			if index := latestPipelineRelease(data, pipeline.ID, "inProgress"); index >= 0 {
				return index, false, nil
			}
			if index := latestPipelineRelease(data, pipeline.ID, "planned"); index >= 0 {
				return index, false, nil
			}
		}
	}
	if version == "" {
		version = shortCommitSHA(input.CommitSHA)
	}
	name := input.Name
	if name == "" {
		name = version
	}
	if name == "" {
		return 0, false, ciError(http.StatusBadRequest, "name, version or commitSha is required")
	}
	stage, status := "", ""
	switch {
	case operation == "sync" && pipeline.Type == "continuous":
		stage, status = firstReleaseStage(pipeline, "released", false), "released"
	default:
		stage, status = firstReleaseStage(pipeline, "inProgress", true), "inProgress"
		if stage == "" {
			stage, status = firstReleaseStage(pipeline, "planned", false), "planned"
		}
	}
	if stage == "" {
		return 0, false, ciError(http.StatusBadRequest, "pipeline has no %s stage", status)
	}
	release := domain.Release{ID: fmt.Sprintf("release_%d", now.UnixNano()), SlugID: uniqueReleaseSlug(data, name, now), Name: name, Version: version, PipelineID: pipeline.ID, Position: nextReleasePosition(data, pipeline.ID), ProjectIDs: []string{}, IssueIDs: []string{}, SubscriberIDs: []string{data.Viewer.ID}, Resources: []domain.ReleaseResource{}, Creator: data.Viewer, CreatedAt: now, UpdatedAt: now}
	// setReleaseCIStage sets the stage, status and their timestamps.
	data.Releases = append(data.Releases, release)
	index := len(data.Releases) - 1
	if err := setReleaseCIStage(pipeline, &data.Releases[index], stage, now); err != nil {
		return 0, false, err
	}
	return index, true, nil
}

func normalizeReleaseStageName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.NewReplacer("-", " ", "_", " ").Replace(value)
	return strings.Join(strings.Fields(value), " ")
}

// matchReleaseStage resolves a stage like Linear's CLI: exact name first, then
// case-insensitively with dashes/underscores as spaces, then a stage type
// (planned, started, completed/released, canceled) to its first unfrozen stage.
func matchReleaseStage(pipeline *domain.ReleasePipeline, value string) string {
	for _, stage := range pipeline.Stages {
		if stage == value {
			return stage
		}
	}
	normalized := normalizeReleaseStageName(value)
	for _, stage := range pipeline.Stages {
		if normalizeReleaseStageName(stage) == normalized {
			return stage
		}
	}
	status := map[string]string{"planned": "planned", "started": "inProgress", "inprogress": "inProgress", "in progress": "inProgress", "completed": "released", "released": "released", "canceled": "canceled", "cancelled": "canceled"}[normalized]
	if status == "" {
		return ""
	}
	if stage := firstReleaseStage(pipeline, status, true); stage != "" {
		return stage
	}
	return firstReleaseStage(pipeline, status, false)
}

func setReleaseCIStage(pipeline *domain.ReleasePipeline, release *domain.Release, stage string, now time.Time) error {
	status := pipeline.StageStatuses[stage]
	if !slices.Contains([]string{"planned", "inProgress", "released", "canceled"}, status) {
		return ciError(http.StatusBadRequest, "release stage %q not found", stage)
	}
	release.Stage, release.Status = stage, status
	if (status == "inProgress" || status == "released") && release.StartedAt == nil {
		release.StartedAt = &now
	}
	if status == "released" && release.ReleasedAt == nil {
		release.ReleasedAt = &now
	}
	if status != "released" {
		release.ReleasedAt = nil
	}
	return nil
}

func moveReleaseCIStage(data *domain.Bootstrap, pipeline *domain.ReleasePipeline, release *domain.Release, input releaseCIInput, operation string, now time.Time) error {
	switch operation {
	case "complete":
		stage := firstReleaseStage(pipeline, "released", false)
		if stage == "" {
			return ciError(http.StatusBadRequest, "pipeline has no completed stage")
		}
		if release.Status == "released" {
			return nil
		}
		return setReleaseCIStage(pipeline, release, stage, now)
	case "update", "event":
		value := input.Stage
		if value == "" {
			if operation == "event" {
				return nil
			}
			return ciError(http.StatusBadRequest, "stage is required")
		}
		stage := matchReleaseStage(pipeline, value)
		if stage == "" {
			return ciError(http.StatusBadRequest, "release stage %q not found", value)
		}
		if stage == release.Stage {
			return nil
		}
		release.StageFrozenAt = nil
		return setReleaseCIStage(pipeline, release, stage, now)
	}
	return nil
}

// applyReleaseCIIssues links referenced issues and unlinks reverted ones.
// Frozen releases keep their issue set: syncs never add to frozen stages.
func applyReleaseCIIssues(data *domain.Bootstrap, release *domain.Release, input releaseCIInput) ([]string, []string) {
	skipped, unknown := []string{}, []string{}
	find := func(identifier string) *domain.Issue {
		for index := range data.Issues {
			if strings.EqualFold(data.Issues[index].Identifier, identifier) && data.Issues[index].ArchivedAt == nil {
				return &data.Issues[index]
			}
		}
		return nil
	}
	frozen := releaseStageFrozen(data, *release)
	for _, reference := range input.IssueReferences {
		identifier := strings.ToUpper(strings.TrimSpace(reference.Identifier))
		if identifier == "" {
			continue
		}
		issue := find(identifier)
		switch {
		case issue == nil:
			if !slices.Contains(unknown, identifier) {
				unknown = append(unknown, identifier)
			}
		case slices.Contains(release.IssueIDs, issue.ID):
		case frozen:
			if !slices.Contains(skipped, identifier) {
				skipped = append(skipped, identifier)
			}
		default:
			release.IssueIDs = append(release.IssueIDs, issue.ID)
		}
	}
	for _, reference := range input.RevertedIssueReferences {
		if issue := find(strings.TrimSpace(reference.Identifier)); issue != nil {
			release.IssueIDs = slices.DeleteFunc(release.IssueIDs, func(id string) bool { return id == issue.ID })
		}
	}
	if len(skipped) == 0 {
		skipped = nil
	}
	if len(unknown) == 0 {
		unknown = nil
	}
	return skipped, unknown
}

// applyReleaseCIResources adds links (deduplicated by URL) and upserts
// documents by title, like Linear's --link and --document flags.
func applyReleaseCIResources(release *domain.Release, input releaseCIInput, now time.Time) {
	if release.Resources == nil {
		release.Resources = []domain.ReleaseResource{}
	}
	for index, link := range input.Links {
		address := strings.TrimSpace(link.URL)
		if address == "" || slices.ContainsFunc(release.Resources, func(item domain.ReleaseResource) bool { return item.Type == "link" && item.URL == address }) {
			continue
		}
		title := strings.TrimSpace(link.Label)
		if title == "" {
			title = address
		}
		release.Resources = append(release.Resources, domain.ReleaseResource{ID: fmt.Sprintf("release_resource_%d_l%d", now.UnixNano(), index), Type: "link", Title: title, URL: address, CreatedAt: now})
	}
	for index, document := range input.Documents {
		title, content := strings.TrimSpace(document.Title), document.Content
		if title == "" || strings.TrimSpace(content) == "" {
			continue
		}
		existing := slices.IndexFunc(release.Resources, func(item domain.ReleaseResource) bool {
			return item.Type == "document" && item.DocumentID == "" && item.Title == title
		})
		if existing >= 0 {
			release.Resources[existing].Content = content
			continue
		}
		release.Resources = append(release.Resources, domain.ReleaseResource{ID: fmt.Sprintf("release_resource_%d_d%d", now.UnixNano(), index), Type: "document", Title: title, Content: content, CreatedAt: now})
	}
}
