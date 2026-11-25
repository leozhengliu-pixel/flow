package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// ChatRequestAskInput declares the only new Ask a chat request may prepend.
// Authorization and issue creation have already run through the record route.
type ChatRequestAskInput struct {
	ID, IssueID, Title, Body, Source, TeamID, TemplateID string
}

// CreateChatRequestAsk runs the unchanged chat callback against its selected
// actor and first-match uniqueness result. All existing Asks remain native
// frames, including inline collections and repeated or invalid record IDs.
func (s *SQLiteStore) CreateChatRequestAsk(ctx context.Context, workspace string, input ChatRequestAskInput, mutate func(*domain.Bootstrap) (string, error)) error {
	actor, hasActor := actorFromContext(ctx)
	if !s.BoundedMetadataEnabled() || !hasActor || mutate == nil || input.ID == "" || input.IssueID == "" {
		return ErrBoundedMetadataCoverage
	}
	if workspace == "" {
		s.mu.RLock()
		workspace = s.lastWorkspaceKey
		s.mu.RUnlock()
	}
	webhook := false
	if s.webhookConfigured() {
		var err error
		webhook, err = s.boundedLegacyWebhookNeeded(ctx, workspace)
		if err != nil {
			return err
		}
	}
	ctx = context.WithValue(metadataLogicalUnpinnedContext(ctx), metadataWorkLeaseKey{}, nil)
	ctx, finish, err := s.startMetadataTimestampInputs(ctx)
	if err != nil {
		return err
	}
	defer finish()
	payload := map[string]string{"source": input.Source, "issueId": input.IssueID}
	ctx, collector, callback := collectMutationAudit(ctx, workspace, mutate)
	var event domain.DomainEvent
	var realtime json.RawMessage
	var eventOwner *ResourceRead
	defer func() {
		if eventOwner != nil {
			eventOwner.Close()
		}
	}()
	written := false
	apply := func(ctx context.Context) error {
		collector.pending = nil
		if eventOwner != nil {
			eventOwner.Close()
			eventOwner = nil
		}
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		if _, found := s.storedWorkspace(workspace); !found {
			return fmt.Errorf("workspace %q: not found", workspace)
		}
		if s.coordinator != nil {
			if err := s.reloadResidentWorkspace(ctx, workspace); err != nil {
				return err
			}
		}
		unbind, err := s.bindMetadataTimestampInputs(ctx)
		if err != nil {
			return err
		}
		defer unbind()
		owner := &ResourceRead{store: s}
		defer owner.Close()
		if err := owner.reserve(ctx, 128<<10); err != nil {
			return err
		}
		// Input strings are borrowed from the caller. Account for the two
		// selected callback containers and the audit's maps before allocation.
		for _, value := range []any{input, actor, actor.Via} {
			cost, err := metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(value), 0)
			if err != nil {
				return err
			}
			if uint64(cost) > (metadataWorkBytes-(16<<10))/2 {
				return ErrMetadataWorkBudget
			}
			if err := owner.reserve(ctx, uint64(cost)*2+16<<10); err != nil {
				return err
			}
		}
		stored, err := s.retainMetadataLogicalSource(ctx, workspace)
		if err != nil {
			return err
		}
		defer stored.Release()
		before, err := cloneMetadataLogicalSnapshot(ctx, stored)
		if err != nil {
			return err
		}
		defer before.Release()
		display, err := s.refreshLegacyDisplayImage(ctx, before)
		if err != nil {
			return err
		}
		defer display.Release()
		actorImage, err := s.prepareLegacyActorImage(ctx, display, &actor)
		if err != nil {
			return err
		}
		defer actorImage.Close()
		source := actorImage.Source()
		matched := false
		asksIndex, _, _ := metadataLogicalField("asks")
		err = source.fields[asksIndex].Children(ctx, func(_ reflect.Value, ask metadataLogicalNode) error {
			for _, field := range []struct{ name, value string }{{"ID", input.ID}, {"IssueID", input.IssueID}} {
				node, err := issueHotField(ctx, ask, field.name)
				if err != nil {
					return err
				}
				same, err := node.StringEqual(ctx, field.value)
				if err != nil {
					return err
				}
				if same {
					matched = true
					return ErrMetadataIterationDone
				}
			}
			return nil
		})
		if err != nil && !errors.Is(err, ErrMetadataIterationDone) {
			return err
		}
		var data domain.Bootstrap
		workspaceIndex, _, _ := metadataLogicalField("workspace")
		value, err := owner.projectNode(ctx, source.fields[workspaceIndex], "ID")
		if err != nil {
			return err
		}
		data.Workspace = value.Interface().(domain.Workspace)
		data.Viewer, data.Users = actor, []domain.User{actor}
		if matched {
			// The callback only observes existence, then returns ErrNoMutation.
			data.Asks = []domain.Ask{{ID: input.ID}}
		}
		beforeCallback := data
		id, err := callback(&data)
		if err != nil {
			return err
		}
		if matched || id != input.ID || len(data.Asks) != 1 {
			return ErrBoundedMetadataCoverage
		}
		created := data.Asks[0]
		if created.ID != input.ID || created.IssueID != input.IssueID ||
			created.Title != input.Title || created.Body != input.Body || created.Source != input.Source ||
			created.TeamID != input.TeamID || created.TemplateID != input.TemplateID ||
			created.Status != "approved" || !reflect.DeepEqual(created.Requester, actor) ||
			created.Approvals == nil || len(created.Approvals) != 0 ||
			created.CreatedAt.IsZero() || created.CreatedAt != created.UpdatedAt {
			return ErrBoundedMetadataCoverage
		}
		data.Asks = beforeCallback.Asks
		if !reflect.DeepEqual(data, beforeCallback) {
			return ErrBoundedMetadataCoverage
		}
		frames, err := newMetadataLogicalBuilder(ctx)
		if err != nil {
			return err
		}
		defer frames.Close()
		shape, err := legacyDiscussionShape(frames, source.fields[asksIndex].typ, false)
		if err != nil {
			return err
		}
		target, err := source.Transform(ctx, func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
			if name == "asks" {
				return chatRequestAskPrepend{base: node, shape: shape, ask: created}, true, nil
			}
			return metadataAggregateStoredNode{node}, false, nil
		})
		if err != nil {
			return err
		}
		defer target.Release()
		payloadRaw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		eventPublication, err := s.prepareMetadataAggregatePublication(ctx, metadataAggregateImage{Source: before},
			metadataAggregateImage{Source: target}, id, webhook, payloadRaw, "ask.created")
		if err != nil {
			return err
		}
		eventOwner = &ResourceRead{store: s}
		eventOwner.release = append(eventOwner.release, eventPublication.Close)
		previous := eventPublication.PreviousValues
		event = domain.DomainEvent{ID: idgen.New("evt_"), Type: "ask.created", AggregateID: id,
			Payload: payloadRaw, PreviousValues: previous, CreatedAt: time.Now().UTC()}
		realtime = eventPublication.RealtimePayload
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		lock := ""
		if s.dialect != "sqlite" {
			lock = " FOR UPDATE"
		}
		var lockedID string
		if err := tx.QueryRowContext(ctx, `SELECT workspace_id FROM workspace_states WHERE workspace_key=?`+lock, workspace).Scan(&lockedID); err != nil {
			return err
		}
		stamp, err := readMetadataStamp(ctx, tx, workspace)
		if err != nil {
			return err
		}
		if stamp.workspaceID != data.Workspace.ID || lockedID != data.Workspace.ID {
			return ErrResourceAccessChanged
		}
		if s.metadataResidency.requireCoverage {
			if err := checkRecordJournalCoverage(ctx, tx, workspace); err != nil {
				return err
			}
		}
		persistCtx := context.WithValue(ctx, legacyMetadataExtraFieldsKey{}, []string{"asks"})
		merged, err := s.persistLegacyMetadataImage(persistCtx, tx, workspace, stored, target)
		if err != nil {
			return err
		}
		defer merged.Release()
		candidate, err := metadataLogicalInstalledCandidate(ctx, merged)
		if err != nil {
			return err
		}
		publication, err := s.prepareLegacyResidentPublication(ctx, tx, workspace, candidate, stamp, stored.Stamp() == stamp, true)
		if err != nil {
			candidate.Release()
			return err
		}
		defer publication.Close()
		// The original full writer also updates the account's last workspace.
		s.mu.RLock()
		viewer := s.viewer
		s.mu.RUnlock()
		viewerCost, err := metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(viewer), 0)
		if err != nil {
			return err
		}
		if err := owner.reserve(ctx, uint64(viewerCost)); err != nil {
			return err
		}
		raw, err := json.Marshal(viewer)
		if err != nil {
			return err
		}
		if len(raw) == 0 || string(raw) == "{}" {
			raw, err = json.Marshal(actor)
			if err != nil {
				return err
			}
		}
		if err := writeAccountMetadata(ctx, tx, workspace, raw); err != nil {
			return err
		}
		storedPrevious, err := storedEventPreviousValues(ctx, tx, workspace, event.Type, id, previous)
		if err != nil {
			return err
		}
		if _, err := insertDomainEvent(ctx, tx, domainEventRow{ID: event.ID, Type: event.Type, AggregateID: id,
			Payload: payloadRaw, PreviousValues: storedPrevious, CreatedAt: event.CreatedAt.Format(time.RFC3339Nano),
			WorkspaceID: data.Workspace.ID, WorkspaceKey: workspace}); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		written = true
		return publication.Install()
	}
	err = mutationAttempts(ctx, fullMutationAttempts, func(ctx context.Context) error {
		if s.coordinator != nil {
			return s.coordinator.WithWorkspaceLock(ctx, workspace, func() error { return apply(ctx) })
		}
		return apply(ctx)
	}, s.refreshConflictedLogicalMetadata(ctx, workspace))
	if written {
		s.dropMetadataCache(ctx, workspace)
	}
	if errors.Is(err, ErrNoMutation) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(collector.pending) != 0 {
		return fmt.Errorf("chat Ask left uncommitted audit entries")
	}
	s.publishMutation(ctx, workspace, event, realtime)
	return nil
}

type chatRequestAskPrepend struct {
	base, shape metadataLogicalNode
	ask         domain.Ask
}

func (n chatRequestAskPrepend) nodeType() reflect.Type           { return n.base.typ }
func (n chatRequestAskPrepend) logicalBase() metadataLogicalNode { return n.shape }
func (n chatRequestAskPrepend) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n chatRequestAskPrepend) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	if err := visit(reflect.ValueOf(0), legacyDiscussionNativeRecord{reflect.ValueOf(n.ask)}); err != nil {
		return err
	}
	return n.base.Children(ctx, func(index reflect.Value, node metadataLogicalNode) error {
		return visit(reflect.ValueOf(int(index.Int())+1), metadataAggregateStoredNode{node})
	})
}
