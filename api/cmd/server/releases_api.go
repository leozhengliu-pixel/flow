package main

import (
	"errors"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"flow/api/internal/domain"
)

type reorderInput struct {
	IDs        []string `json:"ids"`
	PipelineID string   `json:"pipelineId,omitempty"`
	Archived   bool     `json:"archived,omitempty"`
}

type releasePipelineAccessKey struct {
	PipelineID string    `json:"pipelineId"`
	Prefix     string    `json:"prefix"`
	Secret     string    `json:"secret"`
	CreatedAt  time.Time `json:"createdAt"`
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func releasePipelineByID(data *domain.Bootstrap, id string) *domain.ReleasePipeline {
	index := slices.IndexFunc(data.ReleasePipelines, func(item domain.ReleasePipeline) bool { return item.ID == id })
	if index < 0 {
		return nil
	}
	return &data.ReleasePipelines[index]
}

func publicReleasePipeline(item domain.ReleasePipeline) domain.ReleasePipeline {
	item.AccessKeyHash = ""
	return item
}

func nextReleasePosition(data *domain.Bootstrap, pipelineID string) float64 {
	position := 0.0
	for _, item := range data.Releases {
		if item.PipelineID == pipelineID && item.Position >= position {
			position = item.Position + 1
		}
	}
	return position
}

func nextReleasePipelinePosition(data *domain.Bootstrap) float64 {
	position := 0.0
	for _, item := range data.ReleasePipelines {
		if item.Position >= position {
			position = item.Position + 1
		}
	}
	return position
}

func releaseLess(a, b domain.Release) bool {
	if a.Position != b.Position {
		return a.Position < b.Position
	}
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.Before(b.CreatedAt)
	}
	return a.ID < b.ID
}

func releasePipelineLess(a, b domain.ReleasePipeline) bool {
	if a.Position != b.Position {
		return a.Position < b.Position
	}
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.Before(b.CreatedAt)
	}
	return a.ID < b.ID
}

func archiveFilter(r *http.Request) (string, bool) {
	value := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("archived")))
	if value == "" || value == "false" {
		return "active", true
	}
	if value == "true" {
		return "archived", true
	}
	if value == "all" {
		return "all", true
	}
	return "", false
}

func archiveMatches(archivedAt *time.Time, filter string) bool {
	return filter == "all" || filter == "archived" && archivedAt != nil || filter == "active" && archivedAt == nil
}

func (s *server) listReleases(w http.ResponseWriter, r *http.Request) {
	filter, ok := archiveFilter(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "archived must be true, false, or all")
		return
	}
	pipelineID, status := strings.TrimSpace(r.URL.Query().Get("pipelineId")), strings.TrimSpace(r.URL.Query().Get("status"))
	if status != "" && !slices.Contains([]string{"planned", "inProgress", "released", "canceled"}, status) {
		writeError(w, http.StatusBadRequest, "invalid release status")
		return
	}
	result := []domain.Release{}
	for _, item := range s.workspaceData(r).Releases {
		if archiveMatches(item.ArchivedAt, filter) && (pipelineID == "" || item.PipelineID == pipelineID) && (status == "" || item.Status == status) {
			result = append(result, item)
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return releaseLess(result[i], result[j]) })
	writeJSON(w, http.StatusOK, result)
}

func (s *server) getRelease(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	data := s.workspaceData(r)
	index := slices.IndexFunc(data.Releases, func(item domain.Release) bool { return item.ID == id })
	if index < 0 {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}
	writeJSON(w, http.StatusOK, data.Releases[index])
}

func (s *server) reorderReleases(w http.ResponseWriter, r *http.Request) {
	var input reorderInput
	if !decodeJSON(w, r, &input) {
		return
	}
	var reordered []domain.Release
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "release.reordered", input.PipelineID, input, func(data *domain.Bootstrap) error {
		scope := []string{}
		for _, item := range data.Releases {
			if item.PipelineID == input.PipelineID && (item.ArchivedAt != nil) == input.Archived {
				scope = append(scope, item.ID)
			}
		}
		if !sameIDs(scope, input.IDs) {
			return errInvalid
		}
		now := time.Now().UTC()
		for position, id := range input.IDs {
			index := slices.IndexFunc(data.Releases, func(item domain.Release) bool { return item.ID == id })
			data.Releases[index].Position, data.Releases[index].UpdatedAt = float64(position), now
			reordered = append(reordered, data.Releases[index])
		}
		return nil
	})
	respondMutation(w, err, http.StatusOK, reordered)
}

func (s *server) listReleasePipelines(w http.ResponseWriter, r *http.Request) {
	filter, ok := archiveFilter(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "archived must be true, false, or all")
		return
	}
	result := []domain.ReleasePipeline{}
	for _, item := range s.workspaceData(r).ReleasePipelines {
		if archiveMatches(item.ArchivedAt, filter) {
			result = append(result, publicReleasePipeline(item))
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return releasePipelineLess(result[i], result[j]) })
	writeJSON(w, http.StatusOK, result)
}

func (s *server) getReleasePipeline(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	item := releasePipelineByID(&data, r.PathValue("id"))
	if item == nil {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}
	writeJSON(w, http.StatusOK, publicReleasePipeline(*item))
}

func (s *server) reorderReleasePipelines(w http.ResponseWriter, r *http.Request) {
	var input reorderInput
	if !decodeJSON(w, r, &input) {
		return
	}
	var reordered []domain.ReleasePipeline
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "release_pipeline.reordered", "release_pipelines", input, func(data *domain.Bootstrap) error {
		scope := []string{}
		for _, item := range data.ReleasePipelines {
			if (item.ArchivedAt != nil) == input.Archived {
				scope = append(scope, item.ID)
			}
		}
		if !sameIDs(scope, input.IDs) {
			return errInvalid
		}
		now := time.Now().UTC()
		for position, id := range input.IDs {
			index := slices.IndexFunc(data.ReleasePipelines, func(item domain.ReleasePipeline) bool { return item.ID == id })
			data.ReleasePipelines[index].Position, data.ReleasePipelines[index].UpdatedAt = float64(position), now
			reordered = append(reordered, publicReleasePipeline(data.ReleasePipelines[index]))
		}
		return nil
	})
	respondMutation(w, err, http.StatusOK, reordered)
}

func sameIDs(expected, actual []string) bool {
	if len(expected) != len(actual) {
		return false
	}
	seen := map[string]bool{}
	for _, id := range expected {
		seen[id] = true
	}
	for _, id := range actual {
		if !seen[id] {
			return false
		}
		delete(seen, id)
	}
	return len(seen) == 0
}

func (s *server) rotateReleasePipelineAccessKey(w http.ResponseWriter, r *http.Request) {
	secret, err := randomSecret("flow_release_")
	if err != nil {
		respondMutation(w, err, http.StatusCreated, nil)
		return
	}
	id := r.PathValue("id")
	var result releasePipelineAccessKey
	err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "release_pipeline.access_key_rotated", id, nil, func(data *domain.Bootstrap) error {
		pipeline := releasePipelineByID(data, id)
		if pipeline == nil {
			return errNotFound
		}
		now := time.Now().UTC()
		prefix := secret[:min(len(secret), 21)]
		pipeline.AccessKeyPrefix, pipeline.AccessKeyHash, pipeline.AccessKeyCreatedAt, pipeline.UpdatedAt = prefix, secretHash(secret), &now, now
		result = releasePipelineAccessKey{PipelineID: id, Prefix: prefix, Secret: secret, CreatedAt: now}
		return nil
	})
	respondMutation(w, err, http.StatusCreated, result)
}

func (s *server) deleteReleasePipeline(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "release_pipeline.deleted", id, nil, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.ReleasePipelines, func(item domain.ReleasePipeline) bool { return item.ID == id })
		if index < 0 {
			return errNotFound
		}
		if slices.ContainsFunc(data.Releases, func(item domain.Release) bool { return item.PipelineID == id }) {
			return errConflict
		}
		item := data.ReleasePipelines[index]
		item.AccessKeyHash = ""
		if err := appendTrash(data, "release_pipeline", item.ID, item.Name, item); err != nil {
			return err
		}
		data.ReleasePipelines = slices.Delete(data.ReleasePipelines, index, index+1)
		return nil
	})
	if errors.Is(err, errConflict) {
		writeError(w, http.StatusConflict, "release pipeline is still referenced by releases")
		return
	}
	respondMutation(w, err, http.StatusNoContent, nil)
}
