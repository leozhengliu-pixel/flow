package store

import (
	"context"
	"errors"
	"reflect"
	"strings"

	"flow/api/internal/domain"
)

// LoadWorkflow adds the raw workflow and estimation closure from this read's
// pinned generation. The caller's existing workflow helpers retain sorting,
// fallback defaults and the final option limit.
func (r *ChatDisplayRead) LoadWorkflow(ctx context.Context, teamID string) error {
	if r == nil || r.source == nil || r.source.source == nil || r.owner == nil {
		return errMetadataLogicalClosed
	}
	if err := r.owner.reserve(ctx, 64<<10); err != nil {
		return err
	}
	settingsField, _, _ := metadataLogicalField("teamSettings")
	settingsNode := r.source.source.fields[settingsField]
	loaded := map[string]bool{}
	load := func(id string) (domain.TeamSettings, error) {
		if saved, visited := loaded[id]; visited {
			if saved {
				return r.Data.TeamSettings[id], nil
			}
			return domain.TeamSettings{}, nil
		}
		if uint64(len(id)) > (metadataWorkBytes-8192)/8 {
			return domain.TeamSettings{}, ErrMetadataWorkBudget
		}
		if err := r.owner.reserve(ctx, uint64(len(id))*8+8192); err != nil {
			return domain.TeamSettings{}, err
		}
		loaded[id] = false
		var selected domain.TeamSettings
		err := settingsNode.MapEntries(ctx, func(key, record metadataLogicalNode) error {
			matched, err := key.StringEqual(ctx, id)
			if err != nil || !matched {
				return err
			}
			value, err := r.owner.projectNode(ctx, record,
				"TeamID", "ParentTeamID", "InheritWorkflowStatuses", "DefaultStateID", "InheritIssueEstimation", "EstimateType")
			if err != nil {
				return err
			}
			selected = value.Interface().(domain.TeamSettings)
			preserved := r.Data.TeamSettings[id]
			preserved.TeamID, preserved.ParentTeamID = selected.TeamID, selected.ParentTeamID
			preserved.InheritWorkflowStatuses, preserved.DefaultStateID = selected.InheritWorkflowStatuses, selected.DefaultStateID
			preserved.InheritIssueEstimation, preserved.EstimateType = selected.InheritIssueEstimation, selected.EstimateType
			if r.Data.TeamSettings == nil {
				r.Data.TeamSettings = map[string]domain.TeamSettings{}
			}
			r.Data.TeamSettings[strings.Clone(id)] = preserved
			loaded[id] = true
			return ErrMetadataIterationDone
		})
		if errors.Is(err, ErrMetadataIterationDone) {
			err = nil
		}
		return selected, err
	}
	walk := func(workflow bool) (string, bool, error) {
		seen := map[string]bool{}
		for id := teamID; ; {
			if seen[id] {
				return "", true, nil
			}
			if err := r.owner.reserve(ctx, 512); err != nil {
				return "", false, err
			}
			seen[id] = true
			settings, err := load(id)
			if err != nil {
				return "", false, err
			}
			inherits := settings.InheritWorkflowStatuses
			if !workflow {
				inherits = settings.TeamID != "" && settings.InheritIssueEstimation
			}
			if !inherits || settings.ParentTeamID == "" {
				return id, false, nil
			}
			id = settings.ParentTeamID
		}
	}
	terminal, cycle, err := walk(true)
	if err != nil {
		return err
	}
	if _, _, err := walk(false); err != nil {
		return err
	}
	r.Data.States = []domain.WorkflowState{}
	if cycle {
		return ctx.Err()
	}
	statesField, _, _ := metadataLogicalField("states")
	states := r.source.source.fields[statesField]
	specific := false
	err = states.Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
		matched, err := chatWorkflowStateMatches(ctx, record, terminal)
		if err != nil || !matched {
			return err
		}
		specific = true
		return ErrMetadataIterationDone
	})
	if err != nil && !errors.Is(err, ErrMetadataIterationDone) {
		return err
	}
	selectedTeam := terminal
	if !specific {
		selectedTeam = ""
	}
	err = states.Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
		matched, err := chatWorkflowStateMatches(ctx, record, selectedTeam)
		if err != nil || !matched {
			return err
		}
		value, err := r.owner.projectNode(ctx, record, "ID", "TeamID", "Name", "Position", "Default")
		if err != nil {
			return err
		}
		r.Data.States = append(r.Data.States, value.Interface().(domain.WorkflowState))
		return nil
	})
	if err != nil {
		return err
	}
	return ctx.Err()
}

func chatWorkflowStateMatches(ctx context.Context, record metadataLogicalNode, teamID string) (bool, error) {
	matched := false
	err := record.Children(ctx, func(key reflect.Value, value metadataLogicalNode) error {
		if key.String() != "TeamID" {
			return nil
		}
		var err error
		matched, err = value.StringEqual(ctx, teamID)
		return err
	})
	return matched, err
}
