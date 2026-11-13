package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// Only existing point edits are ready here. Creates, deletes, complete-field
// writes, issue/content side effects and P17's snapshot selectors stay blocked.
func residentRecordEvent(event string) bool {
	switch event {
	case "project.updated", "project.resource_created", "project.resource_updated", "project.resource_deleted",
		"project.milestone_created", "project.milestone_updated", "project.milestones_reordered",
		"project.commented", "project.comment_updated", "project.comment_deleted", "project.comment_resolved", "project.comment_unresolved", "project.comment_reaction_toggled",
		"initiative.updated", "initiative.commented", "initiative.comment_updated", "initiative.comment_deleted", "initiative.comment_resolved", "initiative.comment_unresolved", "initiative.comment_reaction_toggled",
		"ai.conversation_updated", "agent.message_updated", "agent.message_completed", "agent.session_updated", "agent.session_titled", "agent.session_read":
		return true
	}
	return false
}

func (s *SQLiteStore) mutateResidentRecords(ctx context.Context, workspace, eventType string, payload any, mutate func(*domain.Bootstrap) (string, error)) error {
	scope, scoped := metadataRecordScopeFromContext(ctx)
	if !scoped || len(scope.Write) == 0 || !residentRecordEvent(eventType) || metadataRecordVerificationEnabled(ctx) {
		return fmt.Errorf("%w: mutation %q has no migrated point writer", ErrBoundedMetadataCoverage, eventType)
	}
	if _, content := mutationScopeFromContext(ctx); content {
		return fmt.Errorf("%w: mutation %q still needs issue/content records", ErrBoundedMetadataCoverage, eventType)
	}
	ctx, release, err := s.admitMetadataWork(ctx, 8<<20)
	if err != nil {
		return err
	}
	defer release()
	if workspace == "" {
		s.mu.RLock()
		workspace = s.lastWorkspaceKey
		s.mu.RUnlock()
	}
	var event domain.DomainEvent
	var realtime json.RawMessage
	webhookEnabled := false
	if s.webhookConfigured() {
		webhookEnabled, err = s.residentWebhookNeeded(ctx, workspace)
		if err != nil {
			return err
		}
	}
	apply := func(ctx context.Context) error {
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		actor, hasActor := actorFromContext(ctx)
		actor = mutationInputActor(actor)
		workspaceID, err := s.WorkspaceIDForKey(ctx, workspace)
		if err != nil {
			return err
		}
		role, teams := "", []string(nil)
		if hasActor {
			var status string
			role, status, err = s.WorkspaceRole(ctx, workspaceID, actor.ID)
			if err != nil || status != "active" {
				return ErrAuthForbidden
			}
			teams = s.actorTeamIDs(ctx, workspaceID, actor.ID)
		}
		tx, err := s.db.BeginTx(ctx, s.recordTxOptions())
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if s.metadataResidency.requireCoverage {
			if err := checkRecordJournalCoverage(ctx, tx, workspace); err != nil {
				return err
			}
		}
		if err := lockResidentRecordWrites(ctx, tx, workspace, scope.Write); err != nil {
			return err
		}
		var raw []byte
		if err := tx.QueryRowContext(ctx, `SELECT CASE WHEN LENGTH(data)<=? THEN data ELSE NULL END FROM workspace_states WHERE workspace_key=?`, MetadataRecordPageMaxBytes, workspace).Scan(&raw); err != nil {
			return err
		}
		if raw == nil {
			return ErrMetadataPageBudget
		}
		if _, _, err := residentMetadataRoot(raw); err != nil {
			return err
		}
		before, err := s.decodeWorkspaceMetadata(ctx, workspace, raw, tx)
		if err != nil {
			return err
		}
		next := cloneBootstrap(before)
		refreshDisplayReferences(&next)
		refreshIssueReferences(&next)
		originalRole := next.ViewerRole
		if hasActor {
			next.Viewer, next.ViewerRole, next.ViewerTeamIDs = actor, role, teams
		}
		next.FormerMembers = s.formerMemberLookup(ctx, tx, &next)
		next.CollaborationUpdates = collaborationUpdatesLookup(ctx, tx, workspace)
		aggregate, err := mutate(&next)
		next.ViewerRole, next.ViewerTeamIDs, next.FormerMembers, next.CollaborationUpdates = originalRole, nil, nil, nil
		if err != nil {
			return err
		}
		if err := validateResidentRecordChanges(before, next, scope); err != nil {
			return err
		}
		payloadRaw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		event = domain.DomainEvent{ID: idgen.New("evt_"), Type: eventType, AggregateID: aggregate, Payload: payloadRaw, CreatedAt: time.Now().UTC()}
		if webhookEnabled {
			event.PreviousValues = aggregatePreviousValues(before, next, aggregate)
		}
		realtime = enrichRealtimePayload(payloadRaw, aggregateJSONValue(next, aggregate), eventType)
		if len(realtime) > MetadataRecordPageMaxBytes {
			return ErrMetadataPageBudget
		}
		if err := s.persistWorkspaceTx(ctx, tx, workspace, &before, &next, nil, &event, nil); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		// This write changed no policy collection. Leave its immutable policy
		// copy installed; native details continue to use canonical SQL.
		s.mu.Lock()
		s.forgetCopyRevisionLocked(workspace)
		s.mu.Unlock()
		return nil
	}
	err = mutationAttempts(ctx, fullMutationAttempts, func(ctx context.Context) error {
		if s.coordinator != nil {
			return s.coordinator.WithWorkspaceLock(ctx, workspace, func() error { return apply(ctx) })
		}
		return apply(ctx)
	}, nil)
	if errors.Is(err, ErrNoMutation) {
		return nil
	}
	if err != nil {
		return err
	}
	s.dropMetadataCache(ctx, workspace)
	s.publishMutation(ctx, workspace, event, realtime)
	return nil
}

func lockResidentRecordWrites(ctx context.Context, tx *sqlTx, workspace string, keys []RecordKey) error {
	keys = slices.Clone(keys)
	slices.SortFunc(keys, func(a, b RecordKey) int { return compareResidentRecordKeys(a, b) })
	budget := newMetadataHydrationBudget()
	lock := ""
	if tx.dialect != "sqlite" {
		lock = " FOR UPDATE"
	}
	for _, key := range slices.Compact(keys) {
		field, ok := metadataFieldByName(key.Kind)
		if !ok || metadataResidencyFields()[key.Kind] != "records" || !rebaseableMetadataField(field, reflect.TypeFor[domain.Bootstrap]().Field(field.index).Type) {
			return ErrBoundedMetadataCoverage
		}
		var raw []byte
		if err := tx.QueryRowContext(ctx, `SELECT CASE WHEN LENGTH(data)<=? THEN data ELSE NULL END FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`+lock, budget.bytes, workspace, key.Kind, key.ID).Scan(&raw); err != nil {
			return err
		}
		if raw == nil {
			return ErrMetadataPageBudget
		}
		if err := budget.consume(raw); err != nil {
			return err
		}
	}
	return nil
}

func compareResidentRecordKeys(a, b RecordKey) int {
	if c := strings.Compare(a.Kind, b.Kind); c != 0 {
		return c
	}
	return strings.Compare(a.ID, b.ID)
}

func (s *SQLiteStore) residentWebhookNeeded(ctx context.Context, workspace string) (bool, error) {
	enabled := s.jsonText("data", "enabled") + ` IN ('1','true')`
	if s.dialect == "sqlite" {
		enabled = s.jsonText("data", "enabled") + `=1`
	}
	var needed int
	err := s.db.QueryRowContext(ctx, `SELECT CASE WHEN EXISTS(SELECT 1 FROM workspace_metadata_records WHERE workspace_key=? AND (
		(field='webhooks' AND `+enabled+` AND TRIM(COALESCE(`+s.jsonText("data", "url")+`,''))<>'') OR
		(field='loops' AND `+enabled+` AND COALESCE(`+s.jsonText("data", "status")+`,'')<>'draft' AND COALESCE(`+s.jsonText("data", "triggerType")+`,'') NOT IN ('','schedule')) OR
		(field='integrationConnections' AND `+s.jsonText("data", "provider")+` IN ('feishu','lark') AND `+s.jsonText("data", "status")+`='connected')
	)) THEN 1 ELSE 0 END`, workspace).Scan(&needed)
	if err != nil {
		return false, err
	}
	return needed != 0 || s.chatSnapshotFlag(ctx, workspace), nil
}

func validateResidentRecordChanges(before, after domain.Bootstrap, scope MetadataRecordScope) error {
	if after.Issues != nil || after.Comments != nil || after.Activities != nil || after.Notifications != nil || after.NotificationDeliveries != nil {
		return ErrBoundedMetadataCoverage
	}
	left, right := reflect.ValueOf(&before).Elem(), reflect.ValueOf(&after).Elem()
	for _, field := range changedMetadataFields(before, after) {
		// Viewer attribution is the existing root scalar update; every body
		// collection must retain its length, order and unselected records.
		if field.name == "viewer" {
			continue
		}
		a, b := left.Field(field.index), right.Field(field.index)
		if a.Kind() != reflect.Slice || a.Type().Elem().Kind() != reflect.Struct || !rebaseableMetadataField(field, a.Type()) || a.Len() != b.Len() || a.IsNil() != b.IsNil() {
			return fmt.Errorf("%w: complete-field write to %s", ErrBoundedMetadataCoverage, field.name)
		}
		for i := 0; i < a.Len(); i++ {
			id := a.Index(i).FieldByName("ID").String()
			if id != b.Index(i).FieldByName("ID").String() || !slices.Contains(scope.Write, RecordKey{field.name, id}) && !metadataValueEqual(a.Index(i), b.Index(i)) {
				return fmt.Errorf("%w: undeclared record write to %s/%s", ErrBoundedMetadataCoverage, field.name, id)
			}
		}
	}
	return nil
}
