package main

import (
	"context"
	"log"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// createTeamRecords runs createTeam's unchanged callback through the bounded
// team creation writer. Its inputs are the parent and copy-from teams the
// callback may copy settings, cycle settings and statuses from.
func (s *server) createTeamRecords(ctx context.Context, workspace, teamID, parentID, copyFromID string, payload any, create func(*domain.Bootstrap) error) error {
	return s.store.CreateTeamRecords(ctx, workspace, payload, store.TeamCreateWrite{
		TeamID:        teamID,
		SourceTeamIDs: []string{parentID, copyFromID},
		Development:   s.authDisabled,
	}, create)
}

// recordCreatedTeamMemberAudit records the creator's new membership as
// recordTeamMemberAudit does. Bounded, the same audit callback runs through
// the scoped writer: it reads no record and only stages audit entries.
func (s *server) recordCreatedTeamMemberAudit(ctx context.Context, workspace string, actor teamMemberAuditActor, reason string, changes []domain.TeamMemberChange) {
	if !s.store.BoundedMetadataEnabled() {
		s.recordTeamMemberAudit(ctx, workspace, actor, reason, changes, nil)
		return
	}
	if len(changes) == 0 || workspace == "" {
		return
	}
	names := s.teamMemberNames(ctx, workspace)
	defer names.closeUnscoped()
	if names.err != nil {
		log.Printf("team member audit names workspace=%s: %v", workspace, names.err)
		return
	}
	names.decorate(changes)
	ctx = store.WithMetadataFields(context.WithoutCancel(ctx), "auditLog")
	err := s.store.MutateWorkspaceScoped(ctx, workspace, "team_member.updated", changes[0].TeamID, map[string]any{"auditEntries": len(changes)}, store.MetadataRecordScope{}, func(data *domain.Bootstrap) error {
		appendTeamMemberAudit(data, actor, names, reason, changes, nil)
		return nil
	})
	if err != nil {
		log.Printf("team member audit workspace=%s: %v", workspace, err)
	}
}
