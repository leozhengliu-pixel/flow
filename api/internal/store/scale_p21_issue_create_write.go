package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// createBoundedIssueRecords retains the creation callback and transaction,
// selecting only its template, policy and reference closure. Metadata outside
// that closure stays in the installed logical image through publication.
func (s *SQLiteStore) createBoundedIssueRecords(ctx context.Context, workspace string, payload any, mutate func(*domain.Bootstrap) (string, error)) error {
	input, ok := payload.(domain.IssueCreateInput)
	if !ok || !metadataOutputOwnerPresent(ctx) {
		return ErrBoundedMetadataCoverage
	}
	if workspace == "" {
		s.mu.RLock()
		workspace = s.lastWorkspaceKey
		s.mu.RUnlock()
	}
	if _, found := s.storedWorkspace(workspace); !found {
		return fmt.Errorf("workspace not found")
	}
	// The literal writer captures its actor before the retry loop. A
	// concurrent profile refresh during a metadata rebase must not change the
	// actor of the retried creation.
	actor, hasActor := actorFromContext(ctx)
	if !hasActor {
		entryCtx := metadataLogicalUnpinnedContext(context.WithoutCancel(ctx))
		entryCtx = context.WithValue(entryCtx, metadataWorkLeaseKey{}, nil)
		source, err := s.retainMetadataLogicalSource(entryCtx, workspace)
		if err != nil {
			return err
		}
		owner := &ResourceRead{store: s, source: source}
		index, _, _ := metadataLogicalField("viewer")
		value, err := owner.node(entryCtx, source.fields[index], false)
		if err != nil {
			owner.Close()
			return err
		}
		actor = value.Interface().(domain.User)
		if !registerMetadataOutputClose(ctx, owner.Close) {
			owner.Close()
			return ErrBoundedMetadataCoverage
		}
	}
	if err := ctx.Err(); err != nil {
		if hasActor {
			return ErrAuthForbidden
		}
		return err
	}
	ctx = metadataLogicalUnpinnedContext(ctx)
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	ctx, finish, err := s.startMetadataTimestampInputs(ctx)
	if err != nil {
		return err
	}
	defer finish()
	var event domain.DomainEvent
	var realtime json.RawMessage
	metadataWritten := false
	err = mutationAttempts(ctx, fullMutationAttempts, func(ctx context.Context) error {
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		if _, found := s.storedWorkspace(workspace); !found {
			return fmt.Errorf("workspace not found")
		}
		unbind, err := s.bindMetadataTimestampInputs(ctx)
		if err != nil {
			return err
		}
		defer unbind()
		attempt := &boundedDiscussionAttempt{owner: &ResourceRead{store: s}}
		transferred := false
		defer func() {
			if !transferred {
				attempt.Close()
			}
		}()
		source, err := s.retainMetadataLogicalSource(ctx, workspace)
		if err != nil {
			return err
		}
		attempt.owner.source = source
		beforeImage, err := metadataLogicalInstalledCandidate(ctx, source)
		if err != nil {
			return err
		}
		defer beforeImage.Release()
		cloned, err := cloneMetadataLogicalSnapshot(ctx, beforeImage)
		if err != nil {
			return err
		}
		defer cloned.Release()
		var data domain.Bootstrap
		attempt.data = &data
		for _, name := range []string{"workspace", "viewerRole"} {
			index, _, _ := metadataLogicalField(name)
			value, err := attempt.owner.node(ctx, cloned.fields[index], false)
			if err != nil {
				return err
			}
			reflect.ValueOf(&data).Elem().Field(index).Set(value)
		}
		if err := attempt.reserveValue(ctx, actor); err != nil {
			return err
		}
		data.Viewer = actor
		if hasActor {
			role, status, err := s.WorkspaceRole(ctx, data.Workspace.ID, actor.ID)
			if err != nil || status != "active" {
				return ErrAuthForbidden
			}
			data.ViewerRole = role
		}
		tx, err := s.db.BeginTx(ctx, s.recordTxOptions())
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err := writeIssueNumber(ctx, tx, workspace, 0); err != nil {
			return err
		}
		lock := ""
		if s.dialect != "sqlite" {
			lock = " FOR UPDATE"
		}
		var last int
		if err := tx.QueryRowContext(ctx, `SELECT last_number FROM issue_number_sequences WHERE workspace_key=?`+lock, workspace).Scan(&last); err != nil {
			return err
		}
		data.NextIssueNumber = last + 1
		if s.metadataResidency.requireCoverage {
			if err := checkRecordJournalCoverage(ctx, tx, workspace); err != nil {
				return err
			}
		}
		baseStamp, err := readMetadataStamp(ctx, tx, workspace)
		if err != nil {
			return err
		}
		if data.Workspace.ID != baseStamp.workspaceID {
			return ErrResourceAccessChanged
		}
		attempt.rows = s.newIssueMutationRows(tx, workspace)
		if key := IssueCreationKey(ctx); key != "" {
			records, err := attempt.rows.load(ctx, []string{key}, true)
			if err != nil {
				return err
			}
			if record, ok := records[key]; ok {
				var issue domain.Issue
				if err := json.Unmarshal(record.raw, &issue); err != nil {
					return err
				}
				data.Issues = append(data.Issues, issue)
			}
		}
		previous := map[string]domain.Issue{}
		if input.ParentID != nil && *input.ParentID != "" {
			// The legacy creation path reads the parent and then its children,
			// each in ID order with its independent synchronous limit.
			for _, column := range []string{"id", "parent_id"} {
				ids, err := s.issueCreateFamilyIDs(ctx, tx, workspace, column, *input.ParentID, lock, attempt.owner)
				if err != nil {
					return err
				}
				for start := 0; start < len(ids); start += 1000 {
					batch := ids[start:min(start+1000, len(ids))]
					records, err := attempt.rows.load(ctx, batch, true)
					if err != nil {
						return err
					}
					for _, recordID := range batch {
						record, ok := records[recordID]
						if !ok {
							continue
						}
						// Creation uses the stored JSON without normalizing the
						// parent's embedded references.
						var issue domain.Issue
						if err := json.Unmarshal(record.raw, &issue); err != nil {
							return err
						}
						record.issue = issue
						if _, seen := previous[record.issue.ID]; !seen {
							previous[record.issue.ID] = record.issue
							data.Issues = append(data.Issues, record.issue)
						}
					}
				}
			}
			if len(previous) > 1000 {
				return fmt.Errorf("issue family exceeds synchronous limit")
			}
		}
		inputs, err := s.loadIssueCreateInputs(ctx, cloned, &data, input)
		if err != nil {
			return err
		}
		attempt.inputs = inputs.issueHotInputs
		inputs.install(&data)
		data.Activities, data.Comments = map[string][]domain.ActivityEvent{}, map[string][]domain.Comment{}
		data.Notifications, data.NotificationDeliveries = []domain.Notification{}, []domain.NotificationDelivery{}
		attempt.lookups, err = newIssueEditorLookups(ctx, inputs.issueHotInputs, tx, workspace, data.Workspace.ID, domain.Issue{})
		if err != nil {
			return err
		}
		releaseCallback, err := attempt.reserveIssueCreateCallback(ctx, data, payload)
		if err != nil {
			return err
		}
		defer releaseCallback()
		before := cloneBootstrap(data)
		attempt.lookups.install(&data)
		if !registerMetadataOutputClose(ctx, attempt.Close) {
			return ErrBoundedMetadataCoverage
		}
		transferred = true
		id, err := mutate(&data)
		releaseCallback()
		data.FormerMembers, data.CollaborationUpdates, data.CollaborationWork = nil, nil, nil
		if err != nil {
			return err
		}
		if err := attempt.lookups.check(); err != nil {
			return err
		}
		newCount := 0
		var created domain.Issue
		changed := []domain.Issue{}
		for _, issue := range data.Issues {
			if issue.ID == id {
				created = issue
			}
			old, exists := previous[issue.ID]
			if !exists {
				newCount++
			}
			last = max(last, issue.Number)
			if !exists || !reflect.DeepEqual(old, issue) {
				changed = append(changed, issue)
			}
		}
		if err := s.writeIssueCreateRows(ctx, tx, workspace, attempt.rows, changed, data); err != nil {
			return err
		}
		if created.ID == "" || newCount < 1 || newCount > 1000 {
			return fmt.Errorf("invalid issue create result")
		}
		if err := writeIssueNumber(ctx, tx, workspace, last); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO issue_collection_counts(workspace_key,total) VALUES(?,?) ON CONFLICT(workspace_key) DO UPDATE SET total=issue_collection_counts.total+excluded.total`, workspace, newCount); err != nil {
			return err
		}
		if err := func() error {
			releaseContent, err := reserveIssueBatchContentEncoding(ctx, s, data)
			if err != nil {
				return err
			}
			defer releaseContent()
			writeCtx := context.WithValue(ctx, legacyContentWriteBudgetKey{}, s)
			for resource, items := range data.Activities {
				for _, item := range items {
					if err := writeContentRecord(writeCtx, tx, workspace, "activity", resource, item); err != nil {
						return err
					}
				}
			}
			for resource, items := range data.Comments {
				for _, item := range items {
					if err := writeContentRecord(writeCtx, tx, workspace, "comment", resource, item); err != nil {
						return err
					}
				}
			}
			for resource, items := range notificationRecords(data.Notifications) {
				for _, item := range items {
					if err := writeContentRecord(writeCtx, tx, workspace, "notification", resource, item); err != nil {
						return err
					}
				}
			}
			for _, item := range data.NotificationDeliveries {
				if err := writeContentRecord(writeCtx, tx, workspace, "delivery", "", item); err != nil {
					return err
				}
			}
			return nil
		}(); err != nil {
			return err
		}
		patches, releasePatches, err := inputs.patch(ctx, before, data)
		if err != nil {
			return err
		}
		defer releasePatches()
		patches["nextIssueNumber"] = metadataAggregateNativeNode{reflect.ValueOf(data.NextIssueNumber)}
		requested, err := source.Transform(ctx, func(name string, original metadataLogicalNode) (metadataAggregateNode, bool, error) {
			if patch, ok := patches[name]; ok {
				return patch, true, nil
			}
			return metadataAggregateStoredNode{original}, false, nil
		})
		if err != nil {
			return err
		}
		defer requested.Release()
		loaded := make([]domain.Issue, 0, len(previous))
		for _, issue := range previous {
			loaded = append(loaded, issue)
		}
		progress, err := s.prepareScopedIssueReactionProgress(ctx, requested, loaded, data.Issues)
		if err != nil {
			return err
		}
		defer progress.Close()
		fields := []string{}
		for name := range patches {
			fields = append(fields, name)
		}
		slices.Sort(fields)
		persistCtx := context.WithValue(ctx, legacyMetadataExtraFieldsKey{}, fields)
		merged, err := s.persistLegacyMetadataImageWithProgress(persistCtx, tx, workspace, source, requested, progress)
		if err != nil {
			return err
		}
		defer merged.Release()
		if err := attempt.reserveIssueCreateEvent(ctx, payload, created); err != nil {
			return err
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		event = domain.DomainEvent{ID: idgen.New("evt_"), Type: "issue.created", AggregateID: id, Payload: raw, CreatedAt: time.Now().UTC()}
		realtime = enrichRealtimePayload(raw, created, event.Type)
		candidate, err := metadataLogicalInstalledCandidate(ctx, merged)
		if err != nil {
			return err
		}
		publication, err := s.prepareLegacyResidentPublication(ctx, tx, workspace, candidate, baseStamp, source.Stamp() == baseStamp, false)
		if err != nil {
			candidate.Release()
			return err
		}
		defer publication.Close()
		if _, err := insertDomainEvent(ctx, tx, domainEventRow{ID: event.ID, Type: event.Type, AggregateID: id, Payload: raw, CreatedAt: event.CreatedAt.Format(time.RFC3339Nano), WorkspaceID: data.Workspace.ID, WorkspaceKey: workspace}); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		if err := publication.Install(); err != nil {
			return err
		}
		metadataWritten = tx.metadataWritten
		return nil
	}, s.refreshConflictedLogicalMetadata(ctx, workspace))
	if metadataWritten {
		s.dropMetadataCache(ctx, workspace)
	}
	if errors.Is(err, ErrNoMutation) {
		return nil
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("workspace not found")
		}
		return err
	}
	s.publishMutation(ctx, workspace, event, realtime)
	return nil
}

func (s *SQLiteStore) issueCreateFamilyIDs(ctx context.Context, tx *sqlTx, workspace, column, parent, lock string, owner *ResourceRead) ([]string, error) {
	length := "OCTET_LENGTH(id)"
	if s.dialect == "sqlite" {
		length = "LENGTH(CAST(id AS BLOB))"
	}
	var count, size int64
	query := `SELECT id,` + length + ` AS size FROM issue_records WHERE workspace_key=? AND ` + column + `=? ORDER BY id LIMIT 1001`
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(size),0) FROM (`+query+`) AS selected`, workspace, parent).Scan(&count, &size); err != nil {
		return nil, err
	}
	if count < 0 || count > 1001 || size < 0 || uint64(size) > (metadataWorkBytes-4096)/8 {
		return nil, ErrMetadataWorkBudget
	}
	if err := owner.reserve(ctx, uint64(size)*8+uint64(count)*512+4096); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM issue_records WHERE workspace_key=? AND `+column+`=? ORDER BY id LIMIT 1001`+lock, workspace, parent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0, int(count))
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
