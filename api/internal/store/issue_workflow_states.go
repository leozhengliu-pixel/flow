package store

import (
	"context"
	"reflect"

	"flow/api/internal/domain"
)

type issueWorkflowStates struct {
	states  []domain.WorkflowState
	source  *metadataLogicalSource
	release []func()
}

func (r *issueWorkflowStates) close() {
	r.states = nil
	if r.source != nil {
		r.source.Release()
		r.source = nil
	}
	for _, release := range r.release {
		release()
	}
	r.release = nil
}

// The caller holds writeMu and keeps this ownership through callback and commit.
// Like storedWorkspace, this reads the installed physical workspace generation,
// independently of a request's borrowed read snapshot.
func (s *SQLiteStore) loadIssueWorkflowStates(ctx context.Context, workspace string, data *domain.Bootstrap, payload any) (_ *issueWorkflowStates, err error) {
	read := &issueWorkflowStates{}
	defer func() {
		if err != nil {
			read.close()
		}
	}()
	reserve := func(cost int) error {
		_, release, err := s.tryAdmitAdditionalMetadataWork(ctx, cost)
		if err == nil {
			read.release = append(read.release, release)
		}
		return err
	}
	if err := reserve(64 << 10); err != nil {
		return nil, err
	}
	read.source, err = s.retainMetadataLogicalSource(metadataLogicalUnpinnedContext(ctx), workspace)
	if err != nil {
		return nil, err
	}
	needed := map[string]bool{}
	addTeam := func(team string) error {
		for steps := 0; steps <= len(data.TeamSettings); steps++ {
			settings := data.TeamSettings[team]
			if !settings.InheritWorkflowStatuses || settings.ParentTeamID == "" {
				if _, exists := needed[team]; !exists {
					if err := reserve(512); err != nil {
						return err
					}
					needed[team] = false
				}
				return nil
			}
			team = settings.ParentTeamID
		}
		return nil
	}
	for _, issue := range data.Issues {
		if err := addTeam(issue.Team.ID); err != nil {
			return nil, err
		}
	}
	if input, ok := payload.(domain.IssueUpdateInput); ok && input.TeamID != nil {
		if err := addTeam(*input.TeamID); err != nil {
			return nil, err
		}
	}
	states, _, _ := metadataLogicalField("states")
	collection := read.source.fields[states]
	teamNode := func(record metadataLogicalNode) (team metadataLogicalNode, err error) {
		err = record.Children(ctx, func(key reflect.Value, node metadataLogicalNode) error {
			if key.String() == "TeamID" {
				team = node
			}
			return nil
		})
		return team, err
	}
	// Specific-team existence suppresses shared defaults even if its only
	// state is reserved or its ID duplicates another team's state.
	if err := collection.Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
		team, err := teamNode(record)
		if err != nil {
			return err
		}
		for id := range needed {
			equal, err := team.StringEqual(ctx, id)
			if err != nil {
				return err
			}
			if equal {
				needed[id] = true
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	shared := false
	for _, specific := range needed {
		shared = shared || !specific
	}
	err = collection.Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
		team, err := teamNode(record)
		if err != nil {
			return err
		}
		wanted := false
		if shared {
			wanted, err = team.StringEqual(ctx, "")
		}
		for id, specific := range needed {
			if wanted || err != nil {
				break
			}
			if specific {
				wanted, err = team.StringEqual(ctx, id)
			}
		}
		if err != nil || !wanted {
			return err
		}
		if record.cost > (metadataWorkBytes-4096)/8 {
			return ErrMetadataWorkBudget
		}
		if err := reserve(int(record.cost)*8 + 4096); err != nil {
			return err
		}
		value, err := record.Value(ctx, int(record.cost))
		if err != nil {
			return err
		}
		read.states = append(read.states, value.Interface().(domain.WorkflowState))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return read, ctx.Err()
}
