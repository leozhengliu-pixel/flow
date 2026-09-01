package main

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

type loopInput struct {
	Name                       *string        `json:"name,omitempty"`
	Icon                       *string        `json:"icon,omitempty"`
	Color                      *string        `json:"color,omitempty"`
	Level                      *string        `json:"level,omitempty"`
	TriggerType                *string        `json:"triggerType,omitempty"`
	TriggerConfig              map[string]any `json:"triggerConfig,omitempty"`
	Instructions               *string        `json:"instructions,omitempty"`
	ConnectorIDs               *[]string      `json:"connectorIds,omitempty"`
	TeamAccess                 *string        `json:"teamAccess,omitempty"`
	AllowChangesOutsideTrigger *bool          `json:"allowChangesOutsideTrigger,omitempty"`
	AllowExternalSync          *bool          `json:"allowExternalSync,omitempty"`
	Enabled                    *bool          `json:"enabled,omitempty"`
}

var loopTriggerTypes = []string{"schedule", "issue", "project", "initiative", "cycle"}

func validateLoopInput(input loopInput, creating bool) error {
	if creating && (input.Name == nil || strings.TrimSpace(*input.Name) == "") {
		return fmt.Errorf("name is required")
	}
	if input.TriggerType != nil && !slices.Contains(loopTriggerTypes, *input.TriggerType) {
		return fmt.Errorf("invalid trigger type")
	}
	if input.Level != nil && *input.Level != "workspace" && *input.Level != "team" {
		return fmt.Errorf("invalid loop level")
	}
	if input.TeamAccess != nil && !slices.Contains([]string{"allPublic", "selected"}, *input.TeamAccess) {
		return fmt.Errorf("invalid team access")
	}
	return nil
}

func applyLoopInput(loop *domain.Loop, input loopInput) {
	if input.Name != nil {
		loop.Name = strings.TrimSpace(*input.Name)
	}
	if input.Icon != nil {
		loop.Icon = strings.TrimSpace(*input.Icon)
	}
	if input.Color != nil {
		loop.Color = strings.TrimSpace(*input.Color)
	}
	if input.Level != nil {
		loop.Level = *input.Level
	}
	if input.TriggerType != nil {
		loop.TriggerType = *input.TriggerType
	}
	if input.TriggerConfig != nil {
		loop.TriggerConfig = input.TriggerConfig
	}
	if input.Instructions != nil {
		loop.Instructions = *input.Instructions
	}
	if input.ConnectorIDs != nil {
		loop.ConnectorIDs = slices.Clone(*input.ConnectorIDs)
	}
	if input.TeamAccess != nil {
		loop.TeamAccess = *input.TeamAccess
	}
	if input.AllowChangesOutsideTrigger != nil {
		loop.AllowChangesOutsideTrigger = *input.AllowChangesOutsideTrigger
	}
	if input.AllowExternalSync != nil {
		loop.AllowExternalSync = *input.AllowExternalSync
	}
	if input.Enabled != nil {
		loop.Enabled = *input.Enabled
	}
}

func (s *server) listLoops(w http.ResponseWriter, r *http.Request) {
	data, ok := s.store.BootstrapFor(workspaceKey(r))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	writeJSON(w, http.StatusOK, data.Loops)
}

func (s *server) getLoop(w http.ResponseWriter, r *http.Request) {
	data, ok := s.store.BootstrapFor(workspaceKey(r))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	id := r.PathValue("id")
	for _, loop := range data.Loops {
		if loop.ID == id {
			writeJSON(w, http.StatusOK, loop)
			return
		}
	}
	writeError(w, http.StatusNotFound, "loop not found")
}

func (s *server) createLoop(w http.ResponseWriter, r *http.Request) {
	var input loopInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := validateLoopInput(input, true); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var created domain.Loop
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "loop.created", input, func(data *domain.Bootstrap) (string, error) {
		now := time.Now().UTC()
		created = domain.Loop{ID: fmt.Sprintf("loop_%d", now.UnixNano()), Name: strings.TrimSpace(*input.Name), Icon: "Automation", Color: "#d9b84b", Level: "workspace", TriggerType: "schedule", TriggerConfig: map[string]any{"interval": 1, "unit": "day", "time": "10:00"}, Instructions: "", ConnectorIDs: []string{}, TeamAccess: "allPublic", AllowChangesOutsideTrigger: true, Enabled: true, Creator: data.Viewer, CreatedAt: now, UpdatedAt: now}
		applyLoopInput(&created, input)
		if created.TriggerConfig == nil {
			created.TriggerConfig = map[string]any{}
		}
		data.Loops = append([]domain.Loop{created}, data.Loops...)
		appendAudit(data, "created", "loop", created.ID, map[string]any{"triggerType": created.TriggerType})
		return created.ID, nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

func (s *server) updateLoop(w http.ResponseWriter, r *http.Request) {
	var input loopInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := validateLoopInput(input, false); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id := r.PathValue("id")
	var updated domain.Loop
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "loop.updated", id, input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.Loops, func(item domain.Loop) bool { return item.ID == id })
		if index < 0 {
			return errNotFound
		}
		applyLoopInput(&data.Loops[index], input)
		data.Loops[index].UpdatedAt = time.Now().UTC()
		updated = data.Loops[index]
		appendAudit(data, "updated", "loop", id, nil)
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteLoop(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "loop.deleted", id, nil, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.Loops, func(item domain.Loop) bool { return item.ID == id })
		if index < 0 {
			return errNotFound
		}
		item := data.Loops[index]
		if err := appendTrash(data, "loop", item.ID, item.Name, item); err != nil {
			return err
		}
		data.Loops = slices.Delete(data.Loops, index, index+1)
		return nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
}
