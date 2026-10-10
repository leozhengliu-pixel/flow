package main

import (
	"fmt"
	"net/http"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type releasePipelineInput struct {
	Name          *string            `json:"name,omitempty"`
	TeamIDs       *[]string          `json:"teamIds,omitempty"`
	Type          *string            `json:"type,omitempty"`
	Production    *bool              `json:"production,omitempty"`
	Stages        *[]string          `json:"stages,omitempty"`
	StageStatuses *map[string]string `json:"stageStatuses,omitempty"`
	StageColors   *map[string]string `json:"stageColors,omitempty"`
	FrozenStages  *[]string          `json:"frozenStages,omitempty"`
	// StageRenames maps a current stage name to its new name so releases in the
	// renamed stage follow it instead of blocking the stage update.
	StageRenames                *map[string]string `json:"stageRenames,omitempty"`
	PathFilters                 *[]string          `json:"pathFilters,omitempty"`
	ReleaseNotesTemplate        *string            `json:"releaseNotesTemplate,omitempty"`
	AutoGenerateReleaseNotes    *bool              `json:"autoGenerateReleaseNotes,omitempty"`
	MoveOpenIssuesToNextRelease *bool              `json:"moveOpenIssuesToNextRelease,omitempty"`
}

const releasePipelineNameMaxLength = 120

var releaseStageColorPattern = regexp.MustCompile(`^#[0-9a-f]{6}$`)

func applyReleasePipelineInput(data *domain.Bootstrap, item *domain.ReleasePipeline, input releasePipelineInput) error {
	if input.Name != nil {
		item.Name = strings.TrimSpace(*input.Name)
		// Linear: "Name cannot exceed 120 characters."
		if item.Name == "" || len([]rune(item.Name)) > releasePipelineNameMaxLength {
			return errInvalid
		}
	}
	if input.TeamIDs != nil {
		item.TeamIDs = normalizedStrings(*input.TeamIDs)
		for _, id := range item.TeamIDs {
			if !slices.ContainsFunc(data.Teams, func(team domain.Team) bool { return team.ID == id }) {
				return errInvalid
			}
		}
	}
	if input.Type != nil {
		if *input.Type != "scheduled" && *input.Type != "continuous" {
			return errInvalid
		}
		item.Type = *input.Type
	}
	if input.Production != nil {
		item.Production = *input.Production
	}
	if input.Stages != nil {
		stages := normalizedStrings(*input.Stages)
		if len(stages) == 0 {
			return errInvalid
		}
		if input.StageRenames != nil {
			renames := map[string]string{}
			for from, to := range *input.StageRenames {
				from, to = strings.TrimSpace(from), strings.TrimSpace(to)
				if from == to || from == "" {
					continue
				}
				if !slices.Contains(item.Stages, from) || !slices.Contains(stages, to) || slices.Contains(stages, from) {
					return errInvalid
				}
				renames[from] = to
			}
			for index := range data.Releases {
				if data.Releases[index].PipelineID != item.ID {
					continue
				}
				if to, ok := renames[data.Releases[index].Stage]; ok {
					data.Releases[index].Stage = to
				}
			}
			for from, to := range renames {
				if status, ok := item.StageStatuses[from]; ok {
					item.StageStatuses[to] = status
				}
				if color, ok := item.StageColors[from]; ok {
					item.StageColors[to] = color
				}
				for index, frozen := range item.FrozenStages {
					if frozen == from {
						item.FrozenStages[index] = to
					}
				}
			}
		}
		if slices.ContainsFunc(data.Releases, func(release domain.Release) bool {
			return release.PipelineID == item.ID && release.Stage != "" && !slices.Contains(stages, release.Stage)
		}) {
			return errConflict
		}
		item.Stages = stages
		if item.StageStatuses == nil {
			item.StageStatuses = map[string]string{}
		}
		for key := range item.StageStatuses {
			if !slices.Contains(stages, key) {
				delete(item.StageStatuses, key)
			}
		}
		for _, stage := range stages {
			if _, ok := item.StageStatuses[stage]; !ok {
				item.StageStatuses[stage] = defaultReleaseStageStatus(stage)
			}
		}
	}
	if input.StageStatuses != nil {
		statuses := map[string]string{}
		for stage, status := range *input.StageStatuses {
			if !slices.Contains(item.Stages, stage) || !slices.Contains([]string{"planned", "inProgress", "released", "canceled"}, status) {
				return errInvalid
			}
			statuses[stage] = status
		}
		for _, stage := range item.Stages {
			if _, ok := statuses[stage]; !ok {
				statuses[stage] = defaultReleaseStageStatus(stage)
			}
		}
		item.StageStatuses = statuses
	}
	if input.StageColors != nil {
		colors := map[string]string{}
		for stage, color := range *input.StageColors {
			color = strings.ToLower(strings.TrimSpace(color))
			if !slices.Contains(item.Stages, stage) || !releaseStageColorPattern.MatchString(color) {
				return errInvalid
			}
			colors[stage] = color
		}
		item.StageColors = colors
	}
	if input.FrozenStages != nil {
		item.FrozenStages = normalizedStrings(*input.FrozenStages)
	}
	// Drop settings for stages that no longer exist, then enforce Linear's rule
	// that only started stages freeze and one started stage stays open.
	for stage := range item.StageColors {
		if !slices.Contains(item.Stages, stage) {
			delete(item.StageColors, stage)
		}
	}
	item.FrozenStages = slices.DeleteFunc(item.FrozenStages, func(stage string) bool { return !slices.Contains(item.Stages, stage) })
	if len(item.FrozenStages) > 0 {
		open := false
		for _, stage := range item.Stages {
			frozen := slices.Contains(item.FrozenStages, stage)
			if frozen && item.StageStatuses[stage] != "inProgress" {
				return errInvalid
			}
			if !frozen && item.StageStatuses[stage] == "inProgress" {
				open = true
			}
		}
		if !open {
			return errInvalid
		}
	}
	if input.PathFilters != nil {
		filters := normalizedStrings(*input.PathFilters)
		for _, filter := range filters {
			if _, err := path.Match(filter, ""); err != nil {
				return errInvalid
			}
		}
		item.PathFilters = filters
	}
	if input.ReleaseNotesTemplate != nil {
		item.ReleaseNotesTemplate = *input.ReleaseNotesTemplate
	}
	if input.AutoGenerateReleaseNotes != nil {
		item.AutoGenerateReleaseNotes = *input.AutoGenerateReleaseNotes
	}
	if input.MoveOpenIssuesToNextRelease != nil {
		value := *input.MoveOpenIssuesToNextRelease
		item.MoveOpenIssuesToNextRelease = &value
	}
	item.UpdatedAt = time.Now().UTC()
	return nil
}

func (s *server) createReleasePipeline(w http.ResponseWriter, r *http.Request) {
	var input releasePipelineInput
	if !decodeJSON(w, r, &input) || input.Name == nil {
		return
	}
	// Linear canCreateReleasePipeline: any non-guest workspace member.
	if role, _ := s.requestWorkspaceRole(r); role != "member" && !workspaceAdminRole(role) {
		writeError(w, http.StatusForbidden, "Guests cannot create release pipelines")
		return
	}
	var created domain.ReleasePipeline
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "release_pipeline.created", "release_pipeline", input, func(data *domain.Bootstrap) error {
		now := time.Now().UTC()
		moveOpenIssues := true
		created = domain.ReleasePipeline{ID: fmt.Sprintf("release_pipeline_%d", now.UnixNano()), SlugID: uniqueReleasePipelineSlug(data, strings.TrimSpace(*input.Name)), Type: "scheduled", Production: true, MoveOpenIssuesToNextRelease: &moveOpenIssues, Position: nextReleasePipelinePosition(data), TeamIDs: []string{}, Stages: []string{"Planned", "In Progress", "Released", "Canceled"}, StageStatuses: map[string]string{"Planned": "planned", "In Progress": "inProgress", "Released": "released", "Canceled": "canceled"}, PathFilters: []string{}, CreatedAt: now, UpdatedAt: now}
		// Linear: continuous pipelines start with a single completed stage.
		if input.Type != nil && *input.Type == "continuous" && input.Stages == nil {
			created.Stages, created.StageStatuses = []string{"Released"}, map[string]string{"Released": "released"}
		}
		if err := applyReleasePipelineInput(data, &created, input); err != nil {
			return err
		}
		data.ReleasePipelines = append([]domain.ReleasePipeline{created}, data.ReleasePipelines...)
		return nil
	})
	if err == nil {
		created = publicReleasePipeline(created)
	}
	respondMutation(w, err, http.StatusCreated, created)
}

func defaultReleaseStageStatus(_ string) string {
	return "planned"
}

func (s *server) updateReleasePipeline(w http.ResponseWriter, r *http.Request) {
	var input releasePipelineInput
	if !decodeJSON(w, r, &input) {
		return
	}
	id := r.PathValue("id")
	var updated domain.ReleasePipeline
	role, teamRoles := s.requestWorkspaceRole(r)
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "release_pipeline.updated", id, input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.ReleasePipelines, func(item domain.ReleasePipeline) bool { return item.ID == id })
		if index < 0 {
			return errNotFound
		}
		var nextTeamIDs *[]string
		if input.TeamIDs != nil {
			normalized := normalizedStrings(*input.TeamIDs)
			nextTeamIDs = &normalized
		}
		if !releasePipelineChangeAllowed(data, data.ReleasePipelines[index], nextTeamIDs, teamRoles, role) {
			return store.ErrAuthForbidden
		}
		if err := applyReleasePipelineInput(data, &data.ReleasePipelines[index], input); err != nil {
			return err
		}
		updated = data.ReleasePipelines[index]
		return nil
	})
	if err == nil {
		updated = publicReleasePipeline(updated)
	}
	if err == errConflict {
		writeError(w, http.StatusConflict, "pipeline stages are still referenced by releases")
		return
	}
	respondMutation(w, err, http.StatusOK, updated)
}

type customEmojiInput struct {
	Name     *string `json:"name,omitempty"`
	ImageURL *string `json:"imageUrl,omitempty"`
	Archived *bool   `json:"archived,omitempty"`
}

func applyCustomEmojiInput(item *domain.CustomEmoji, input customEmojiInput) error {
	if input.Name != nil {
		name := strings.Trim(strings.ToLower(strings.TrimSpace(*input.Name)), ":")
		if name == "" || strings.ContainsAny(name, " /\\") {
			return errInvalid
		}
		item.Name = name
	}
	if input.ImageURL != nil {
		if !strings.HasPrefix(*input.ImageURL, "data:image/") || len(*input.ImageURL) > 700_000 {
			return errInvalid
		}
		item.ImageURL = *input.ImageURL
	}
	if input.Archived != nil {
		if *input.Archived && item.ArchivedAt == nil {
			now := time.Now().UTC()
			item.ArchivedAt = &now
		} else if !*input.Archived {
			item.ArchivedAt = nil
		}
	}
	item.UpdatedAt = time.Now().UTC()
	return nil
}

func (s *server) createCustomEmoji(w http.ResponseWriter, r *http.Request) {
	var input customEmojiInput
	if !decodeJSON(w, r, &input) || input.Name == nil || input.ImageURL == nil {
		return
	}
	var created domain.CustomEmoji
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "custom_emoji.created", "custom_emoji", map[string]any{"name": *input.Name}, func(data *domain.Bootstrap) error {
		now := time.Now().UTC()
		created = domain.CustomEmoji{ID: fmt.Sprintf("custom_emoji_%d", now.UnixNano()), Creator: data.Viewer, CreatedAt: now, UpdatedAt: now}
		if err := applyCustomEmojiInput(&created, input); err != nil {
			return err
		}
		if slices.ContainsFunc(data.CustomEmojis, func(item domain.CustomEmoji) bool { return item.Name == created.Name && item.ArchivedAt == nil }) {
			return errInvalid
		}
		data.CustomEmojis = append([]domain.CustomEmoji{created}, data.CustomEmojis...)
		return nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

func (s *server) updateCustomEmoji(w http.ResponseWriter, r *http.Request) {
	var input customEmojiInput
	if !decodeJSON(w, r, &input) {
		return
	}
	id := r.PathValue("id")
	var updated domain.CustomEmoji
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "custom_emoji.updated", id, map[string]any{"id": id}, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.CustomEmojis, func(item domain.CustomEmoji) bool { return item.ID == id })
		if index < 0 {
			return errNotFound
		}
		if err := applyCustomEmojiInput(&data.CustomEmojis[index], input); err != nil {
			return err
		}
		updated = data.CustomEmojis[index]
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}
