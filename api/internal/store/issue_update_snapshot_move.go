package store

import (
	"context"
	"reflect"

	"flow/api/internal/domain"
)

// An unmarked move uses only the installed snapshot's number. In particular it
// neither reads nor locks the SQL sequence, even when that sequence is ahead.
func (r *issueHotInputs) issueMoveSnapshotNumber(ctx context.Context) (int, error) {
	field, _ := reflect.TypeFor[domain.Bootstrap]().FieldByName("NextIssueNumber")
	node := r.source.fields[field.Index[0]]
	if node.typ != reflect.TypeFor[int]() {
		return 0, ErrBoundedMetadataCoverage
	}
	if err := r.reserve(ctx, node.cost*8+4096); err != nil {
		return 0, err
	}
	value, err := node.Value(ctx, int(node.cost))
	if err != nil {
		return 0, err
	}
	return int(value.Int()), nil
}

// issueMoveSnapshotMember preserves the member fields the notification and
// visibility paths consume, without loading unrelated user profile bodies.
func (r *issueHotInputs) issueMoveSnapshotMember(ctx context.Context, record metadataLogicalNode) (domain.WorkspaceMember, error) {
	user, err := issueHotField(ctx, record, "User")
	if err != nil {
		return domain.WorkspaceMember{}, err
	}
	id, err := issueHotField(ctx, user, "ID")
	if err != nil {
		return domain.WorkspaceMember{}, err
	}
	fields := []string{"Role", "Status", "JoinedAt", "LastSeenAt", "ManagedBy"}
	cost, err := issueHotProjectionCost(ctx, record, fields)
	if err != nil {
		return domain.WorkspaceMember{}, err
	}
	if id.cost > (metadataWorkBytes-cost)/8 {
		return domain.WorkspaceMember{}, ErrMetadataWorkBudget
	}
	cost += id.cost * 8
	if err := r.reserve(ctx, cost); err != nil {
		return domain.WorkspaceMember{}, err
	}
	value, err := issueHotProjection(ctx, record, fields, cost)
	if err != nil {
		return domain.WorkspaceMember{}, err
	}
	member := value.Interface().(domain.WorkspaceMember)
	value, err = id.Value(ctx, int(id.cost))
	if err != nil {
		return domain.WorkspaceMember{}, err
	}
	member.User.ID = value.String()
	return member, nil
}

// This is the stored-image alternative to issueMoveAudience. The original
// writer refreshes membership SQL only under WithIssueRenumbering AND an actor.
// Duplicates and first-match order remain intact; one real unmatched member
// preserves the directory's nonempty authority when nobody selected occurs.
func (r *issueHotInputs) issueMoveSnapshotAudience(ctx context.Context, users []string) ([]domain.WorkspaceMember, []domain.TeamMember, error) {
	selected, err := r.keys(ctx, users...)
	if err != nil {
		return nil, nil, err
	}
	index, _, _ := metadataLogicalField("members")
	nilMembers, err := r.source.fields[index].isNil(ctx)
	if err != nil {
		return nil, nil, err
	}
	var members []domain.WorkspaceMember
	if !nilMembers {
		members = []domain.WorkspaceMember{}
	}
	var witness metadataLogicalNode
	hasWitness := false
	err = r.source.fields[index].Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
		user, err := issueHotField(ctx, record, "User")
		if err != nil {
			return err
		}
		matched, err := issueHotMatches(ctx, user, "ID", selected)
		if err != nil {
			return err
		}
		if !matched {
			if !hasWitness {
				witness, hasWitness = record, true
			}
			return nil
		}
		member, err := r.issueMoveSnapshotMember(ctx, record)
		if err != nil {
			return err
		}
		members = append(members, member)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	if len(members) == 0 && hasWitness {
		member, err := r.issueMoveSnapshotMember(ctx, witness)
		if err != nil {
			return nil, nil, err
		}
		members = append(members, member)
	}
	index, _, _ = metadataLogicalField("teamMembers")
	nilTeams, err := r.source.fields[index].isNil(ctx)
	if err != nil {
		return nil, nil, err
	}
	r.data.TeamMembers = nil
	if !nilTeams {
		r.data.TeamMembers = []domain.TeamMember{}
	}
	err = r.selectSlice(ctx, "teamMembers", func(record metadataLogicalNode) (bool, error) {
		return issueHotMatches(ctx, record, "UserID", selected)
	})
	return members, r.data.TeamMembers, err
}
