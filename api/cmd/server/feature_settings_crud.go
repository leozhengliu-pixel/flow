package main

import (
	"fmt"
	"net/http"
	"path"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

type releasePipelineInput struct {
	Name                        *string            `json:"name,omitempty"`
	TeamIDs                     *[]string          `json:"teamIds,omitempty"`
	Type                        *string            `json:"type,omitempty"`
	Production                  *bool              `json:"production,omitempty"`
	Stages                      *[]string          `json:"stages,omitempty"`
	StageStatuses               *map[string]string `json:"stageStatuses,omitempty"`
	PathFilters                 *[]string          `json:"pathFilters,omitempty"`
	ReleaseNotesTemplate        *string            `json:"releaseNotesTemplate,omitempty"`
	AutoGenerateReleaseNotes    *bool              `json:"autoGenerateReleaseNotes,omitempty"`
	MoveOpenIssuesToNextRelease *bool              `json:"moveOpenIssuesToNextRelease,omitempty"`
}

func applyReleasePipelineInput(data *domain.Bootstrap, item *domain.ReleasePipeline, input releasePipelineInput) error {
	if input.Name != nil {
		item.Name = strings.TrimSpace(*input.Name)
		if item.Name == "" {
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
	var created domain.ReleasePipeline
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "release_pipeline.created", "release_pipeline", input, func(data *domain.Bootstrap) error {
		now := time.Now().UTC()
		moveOpenIssues := true
		created = domain.ReleasePipeline{ID: fmt.Sprintf("release_pipeline_%d", now.UnixNano()), SlugID: uniqueReleasePipelineSlug(data, strings.TrimSpace(*input.Name)), Type: "scheduled", Production: true, MoveOpenIssuesToNextRelease: &moveOpenIssues, Position: nextReleasePipelinePosition(data), TeamIDs: []string{}, Stages: []string{"Planned", "In Progress", "Released", "Canceled"}, StageStatuses: map[string]string{"Planned": "planned", "In Progress": "inProgress", "Released": "released", "Canceled": "canceled"}, PathFilters: []string{}, CreatedAt: now, UpdatedAt: now}
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
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "release_pipeline.updated", id, input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.ReleasePipelines, func(item domain.ReleasePipeline) bool { return item.ID == id })
		if index < 0 {
			return errNotFound
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
