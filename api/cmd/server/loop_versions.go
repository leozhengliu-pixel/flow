package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// Published versions ("Show published versions"): every publish and every
// change to a published loop's definition stores an immutable snapshot.

// Versions kept per loop; version numbers keep counting after older ones drop.
const loopVersionHistory = 50

func loopVersionID(loopID string, version int) string {
	return fmt.Sprintf("%s_v%d", loopID, version)
}

// loopDefinition is the versioned part of a loop.
func loopDefinition(loop domain.Loop) domain.LoopDefinition {
	connectors := slices.Clone(loop.ConnectorIDs)
	if connectors == nil {
		connectors = []string{}
	}
	return domain.LoopDefinition{
		Name: loop.Name, Description: loop.Description, Icon: loop.Icon, Color: loop.Color, Level: loop.Level, TeamID: loop.TeamID,
		TriggerType: loop.TriggerType, TriggerConfig: normalizeLoopTriggerConfig(loop.TriggerType, loop.TriggerConfig),
		Instructions: loop.Instructions, InstructionsData: cloneJSONMap(loop.InstructionsData), ConnectorIDs: connectors,
		TeamAccess: loop.TeamAccess, AllowChangesOutsideTrigger: loop.AllowChangesOutsideTrigger, AllowExternalSync: loop.AllowExternalSync,
		WebSearch: loop.WebSearch, CodeAccess: loopCodeAccess(loop),
	}
}

func cloneJSONMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var clone map[string]any
	if json.Unmarshal(raw, &clone) != nil {
		return nil
	}
	return clone
}

// sameJSON compares two values by their JSON encoding, so numbers decoded as
// float64 and ints written by the server compare equal.
func sameJSON(a, b any) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}

// loopDefinitionChanges lists the versioned fields that differ.
func loopDefinitionChanges(before, after domain.LoopDefinition) []string {
	changes := []string{}
	add := func(changed bool, field string) {
		if changed {
			changes = append(changes, field)
		}
	}
	add(before.Name != after.Name, "name")
	add(before.TriggerType != after.TriggerType || !sameJSON(before.TriggerConfig, after.TriggerConfig), "trigger")
	add(before.Instructions != after.Instructions || !sameJSON(before.InstructionsData, after.InstructionsData), "instructions")
	add(!reflect.DeepEqual(before.ConnectorIDs, after.ConnectorIDs), "connectors")
	add(before.TeamAccess != after.TeamAccess, "teamAccess")
	add(before.AllowChangesOutsideTrigger != after.AllowChangesOutsideTrigger, "allowChangesOutsideTrigger")
	add(before.AllowExternalSync != after.AllowExternalSync, "allowExternalSync")
	add(before.WebSearch != after.WebSearch, "webSearch")
	add(before.CodeAccess != after.CodeAccess, "codeAccess")
	add(before.Level != after.Level || before.TeamID != after.TeamID, "level")
	return changes
}

func loopVersionsFor(data *domain.Bootstrap, loopID string) []domain.LoopVersion {
	versions := []domain.LoopVersion{}
	for _, version := range data.LoopVersions {
		if version.LoopID == loopID {
			versions = append(versions, version)
		}
	}
	slices.SortFunc(versions, func(a, b domain.LoopVersion) int { return b.Version - a.Version })
	return versions
}

// legacyLoopVersion is the v1 of a published loop stored before versions existed.
func legacyLoopVersion(data *domain.Bootstrap, loop domain.Loop) domain.LoopVersion {
	at := loop.CreatedAt
	if loop.PublishedAt != nil {
		at = *loop.PublishedAt
	}
	by := loop.Creator
	if user := userByID(data, loop.Creator.ID); user != nil {
		by = *user
	}
	return domain.LoopVersion{ID: loopVersionID(loop.ID, 1), LoopID: loop.ID, Version: 1, PublishedAt: at, PublishedBy: by, ChangeSummary: []string{"published"}, Definition: loopDefinition(loop)}
}

// ensureLoopVersion returns the loop's current version, storing a v1 for a
// published loop that has none yet (loops published before versions existed).
func ensureLoopVersion(data *domain.Bootstrap, loop *domain.Loop) domain.LoopVersion {
	if versions := loopVersionsFor(data, loop.ID); len(versions) > 0 {
		return versions[0]
	}
	if loop.Status == "draft" {
		return domain.LoopVersion{}
	}
	version := legacyLoopVersion(data, *loop)
	data.LoopVersions = append(data.LoopVersions, version)
	loop.Version, loop.VersionID = version.Version, version.ID
	return version
}

// recordLoopVersion snapshots a published loop when it was just published or
// its definition changed. before is nil when the loop was not published yet.
// It returns the new version, or nil when nothing versioned changed.
func recordLoopVersion(data *domain.Bootstrap, before *domain.Loop, after *domain.Loop, now time.Time, restoredFrom int) *domain.LoopVersion {
	if after.Status == "draft" {
		return nil
	}
	summary := []string{"published"}
	latest := 0
	if before != nil && before.Status != "draft" {
		previous := ensureLoopVersion(data, before)
		latest = previous.Version
		summary = loopDefinitionChanges(previous.Definition, loopDefinition(*after))
		if restoredFrom > 0 {
			summary = append([]string{"restored"}, summary...)
		} else if len(summary) == 0 {
			after.Version, after.VersionID = previous.Version, previous.ID
			return nil
		}
	}
	for _, version := range data.LoopVersions {
		if version.LoopID == after.ID && version.Version > latest {
			latest = version.Version
		}
	}
	version := domain.LoopVersion{ID: loopVersionID(after.ID, latest+1), LoopID: after.ID, Version: latest + 1, PublishedAt: now, PublishedBy: data.Viewer, ChangeSummary: summary, RestoredFromVersion: restoredFrom, Definition: loopDefinition(*after)}
	data.LoopVersions = append(data.LoopVersions, version)
	// Keep the newest loopVersionHistory versions of this loop.
	if versions := loopVersionsFor(data, after.ID); len(versions) > loopVersionHistory {
		cutoff := versions[loopVersionHistory-1].Version
		data.LoopVersions = slices.DeleteFunc(data.LoopVersions, func(item domain.LoopVersion) bool {
			return item.LoopID == after.ID && item.Version < cutoff
		})
	}
	after.Version, after.VersionID = version.Version, version.ID
	return &version
}

// presentLoopVersions returns a loop's versions newest first, with a
// synthesized v1 for published loops stored before versions existed.
func presentLoopVersions(data *domain.Bootstrap, loop domain.Loop) []domain.LoopVersion {
	versions := loopVersionsFor(data, loop.ID)
	if len(versions) == 0 && loop.Status != "draft" {
		versions = []domain.LoopVersion{legacyLoopVersion(data, loop)}
	}
	for index := range versions {
		versions[index].Current = index == 0
		if versions[index].ChangeSummary == nil {
			versions[index].ChangeSummary = []string{}
		}
	}
	return versions
}

func (s *server) listLoopVersions(w http.ResponseWriter, r *http.Request) {
	data, ok := s.store.WorkspaceMetadata(workspaceKey(r))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	loop := loopByID(&data, r.PathValue("id"))
	if loop == nil {
		writeError(w, http.StatusNotFound, "loop not found")
		return
	}
	writeJSON(w, http.StatusOK, presentLoopVersions(&data, *loop))
}

func (s *server) getLoopVersion(w http.ResponseWriter, r *http.Request) {
	data, ok := s.store.WorkspaceMetadata(workspaceKey(r))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	loop := loopByID(&data, r.PathValue("id"))
	if loop == nil {
		writeError(w, http.StatusNotFound, "loop not found")
		return
	}
	for _, version := range presentLoopVersions(&data, *loop) {
		if version.ID == r.PathValue("versionId") {
			writeJSON(w, http.StatusOK, version)
			return
		}
	}
	writeError(w, http.StatusNotFound, "loop version not found")
}

// restoreLoopVersion applies a snapshot's definition as a new version.
func (s *server) restoreLoopVersion(w http.ResponseWriter, r *http.Request) {
	id, versionID := r.PathValue("id"), r.PathValue("versionId")
	var restored domain.Loop
	var runs []domain.LoopRun
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "loop.version_restored", id, map[string]string{"versionId": versionID}, func(data *domain.Bootstrap) error {
		loop := loopByID(data, id)
		if loop == nil {
			return errNotFound
		}
		if loop.Status == "draft" {
			return fmt.Errorf("%w: only created loops have published versions", errConflict)
		}
		if err := s.checkLoopEditor(data, *loop); err != nil {
			return err
		}
		var source *domain.LoopVersion
		for _, version := range presentLoopVersions(data, *loop) {
			if version.ID == versionID {
				source = &version
				break
			}
		}
		if source == nil {
			return errNotFound
		}
		before := *loop
		next := *loop
		definition := source.Definition
		next.Name, next.Description, next.Icon, next.Color = definition.Name, definition.Description, definition.Icon, definition.Color
		next.Level, next.TeamID = definition.Level, definition.TeamID
		next.TriggerType, next.TriggerConfig = definition.TriggerType, cloneJSONMap(definition.TriggerConfig)
		next.Instructions, next.InstructionsData = definition.Instructions, cloneJSONMap(definition.InstructionsData)
		next.ConnectorIDs = slices.Clone(definition.ConnectorIDs)
		next.TeamAccess, next.AllowChangesOutsideTrigger, next.AllowExternalSync = definition.TeamAccess, definition.AllowChangesOutsideTrigger, definition.AllowExternalSync
		next.WebSearch, next.CodeAccess = definition.WebSearch, definition.CodeAccess
		next.NextRunAt = nil
		if err := checkLoop(data, next, false); err != nil {
			return err
		}
		now := time.Now().UTC()
		next.UpdatedAt = now
		recordLoopVersion(data, &before, &next, now, source.Version)
		*loop = next
		restored = next
		appendAudit(data, "updated", "loop", id, map[string]any{"restoredVersion": source.Version})
		runs = data.LoopRuns
		return nil
	})
	if err != nil && strings.Contains(err.Error(), "only created loops") {
		writeError(w, http.StatusConflict, "Only created loops have published versions")
		return
	}
	respondLoopMutation(w, err, http.StatusOK, presentLoop(runs, restored))
}

type loopRunFeedbackInput struct {
	Rating  *string `json:"rating"`
	Comment string  `json:"comment,omitempty"`
}

// setLoopRunFeedback stores the viewer's thumbs up/down on a run.
func (s *server) setLoopRunFeedback(w http.ResponseWriter, r *http.Request) {
	var input loopRunFeedbackInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Rating != nil && *input.Rating != "up" && *input.Rating != "down" {
		writeError(w, http.StatusBadRequest, "rating must be up, down or null")
		return
	}
	comment := strings.TrimSpace(input.Comment)
	if len([]rune(comment)) > 2000 {
		writeError(w, http.StatusBadRequest, "comment is too long (max 2000 characters)")
		return
	}
	id, runID := r.PathValue("id"), r.PathValue("runId")
	var updated domain.LoopRun
	viewerID := authUser(r).ID
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "loop.run_feedback", id, map[string]any{"runId": runID}, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.LoopRuns, func(item domain.LoopRun) bool { return item.ID == runID && item.LoopID == id })
		if index < 0 {
			return errNotFound
		}
		userID := firstNonEmpty(viewerID, data.Viewer.ID)
		viewerID = userID
		run := &data.LoopRuns[index]
		run.Feedback = slices.DeleteFunc(slices.Clone(run.Feedback), func(item domain.LoopRunFeedback) bool { return item.UserID == userID })
		if input.Rating != nil {
			run.Feedback = append(run.Feedback, domain.LoopRunFeedback{UserID: userID, Rating: *input.Rating, Comment: comment, At: time.Now().UTC()})
		}
		updated = *run
		return nil
	})
	respondMutation(w, err, http.StatusOK, presentLoopRun(updated, viewerID))
}
