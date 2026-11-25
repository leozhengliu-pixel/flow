package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// AgentSessionLifecycleScope describes the fixed session-only callbacks used
// by reply cleanup, worker saves and chat read markers. A nil owner is a
// wildcard; a pointer to "" matches the installed empty owner exactly.
type AgentSessionLifecycleScope struct {
	ID                string
	OwnerID           *string
	MatchSlug         bool
	ExcludeDeleted    bool
	AppendActivities  bool
	PersistActor      bool
	CallbackInput     any
	FirstSession      bool
	HeaderOnly        bool
	MessageID         *string
	StreamingMessages bool
}

type agentSessionLifecycleInputs struct {
	owner    *ResourceRead
	source   *metadataLogicalSource
	data     domain.Bootstrap
	ordinals []int
	nodes    []metadataLogicalNode
	messages [][]int
}

func (r *agentSessionLifecycleInputs) Close() {
	if r != nil {
		r.data = domain.Bootstrap{}
		r.ordinals, r.nodes, r.messages, r.source = nil, nil, nil, nil
		r.owner.Close()
	}
}

func (s *SQLiteStore) loadAgentSessionLifecycleInputs(ctx context.Context, source *metadataLogicalSource, scope AgentSessionLifecycleScope) (_ *agentSessionLifecycleInputs, err error) {
	r := &agentSessionLifecycleInputs{owner: &ResourceRead{store: s}, source: source}
	defer func() {
		if err != nil {
			r.Close()
		}
	}()
	if err := source.Retain(); err != nil {
		return nil, err
	}
	r.owner.source = source
	if err := r.owner.reserve(ctx, 128<<10); err != nil {
		return nil, err
	}
	for _, name := range []string{"workspace", "viewer", "viewerRole"} {
		index, _, _ := metadataLogicalField(name)
		value, err := r.owner.node(ctx, source.fields[index], false)
		if err != nil {
			return nil, err
		}
		reflect.ValueOf(&r.data).Elem().Field(index).Set(value)
	}
	index, _, _ := metadataLogicalField("agentSessions")
	isNil, err := source.fields[index].isNil(ctx)
	if err != nil {
		return nil, err
	}
	if !isNil {
		r.data.AgentSessions = []domain.AgentSession{}
	}
	err = source.fields[index].Children(ctx, func(ordinal reflect.Value, node metadataLogicalNode) error {
		selected := false
		err := r.owner.withHeader(ctx, node, []string{"ID", "SlugID", "UserID", "DeletedAt"}, func(value reflect.Value) error {
			item := value.Interface().(domain.AgentSession)
			selected = (item.ID == scope.ID || scope.MatchSlug && item.SlugID == scope.ID) &&
				(scope.OwnerID == nil || item.UserID == *scope.OwnerID) &&
				(!scope.ExcludeDeleted || item.DeletedAt == nil)
			return nil
		})
		if err != nil || !selected {
			return err
		}
		// Session metadata and selected messages have separate ownership.
		// Read markers never decode transcripts; worker saves decode only
		// their first matching message, preserving the rest as native frames.
		fields := make([]string, 0, node.typ.NumField()-1)
		for index := 0; index < node.typ.NumField(); index++ {
			if name := node.typ.Field(index).Name; name != "Messages" {
				fields = append(fields, name)
			}
		}
		value, err := r.owner.projectNode(ctx, node, fields...)
		if err != nil {
			return err
		}
		session := value.Interface().(domain.AgentSession)
		messages, err := issueHotField(ctx, node, "Messages")
		if err != nil {
			return err
		}
		nilMessages, err := messages.isNil(ctx)
		if err != nil {
			return err
		}
		if !nilMessages {
			session.Messages = []domain.AgentMessage{}
		}
		var messageOrdinals []int
		if !scope.HeaderOnly {
			err = messages.Children(ctx, func(index reflect.Value, message metadataLogicalNode) error {
				match := false
				err := r.owner.withHeader(ctx, message, []string{"ID", "Status"}, func(value reflect.Value) error {
					header := value.Interface().(domain.AgentMessage)
					match = (scope.MessageID == nil || header.ID == *scope.MessageID) &&
						(!scope.StreamingMessages || header.Status == "streaming")
					return nil
				})
				if err != nil || !match {
					return err
				}
				value, err := r.owner.node(ctx, message, false)
				if err != nil {
					return err
				}
				if err := r.owner.reserve(ctx, 128); err != nil {
					return err
				}
				session.Messages = append(session.Messages, value.Interface().(domain.AgentMessage))
				messageOrdinals = append(messageOrdinals, int(index.Int()))
				if scope.MessageID != nil {
					return ErrMetadataIterationDone
				}
				return ctx.Err()
			})
			if errors.Is(err, ErrMetadataIterationDone) {
				err = nil
			}
			if err != nil {
				return err
			}
		}
		if err := r.owner.reserve(ctx, 1024); err != nil {
			return err
		}
		r.data.AgentSessions = append(r.data.AgentSessions, session)
		r.ordinals = append(r.ordinals, int(ordinal.Int()))
		r.nodes = append(r.nodes, node)
		r.messages = append(r.messages, messageOrdinals)
		if scope.FirstSession {
			return ErrMetadataIterationDone
		}
		return ctx.Err()
	})
	if errors.Is(err, ErrMetadataIterationDone) {
		err = nil
	}
	if err != nil {
		return nil, err
	}
	return r, nil
}

// Only the lifecycle fields below can change. Immutable selected fields are
// admitted independently without materializing another transcript.
func (r *agentSessionLifecycleInputs) merge(ctx context.Context, scope AgentSessionLifecycleScope) (*metadataLogicalSource, error) {
	if len(r.data.AgentSessions) != len(r.nodes) || !scope.AppendActivities && len(r.data.AgentActivities) != 0 {
		return nil, ErrBoundedMetadataCoverage
	}
	for index, session := range r.data.AgentSessions {
		if len(session.Messages) != len(r.messages[index]) {
			return nil, ErrBoundedMetadataCoverage
		}
		value := reflect.ValueOf(session)
		err := r.nodes[index].Children(ctx, func(key reflect.Value, node metadataLogicalNode) error {
			name := key.String()
			if name == "Messages" || name == "UpdatedAt" || name == "LastReadAt" {
				return nil
			}
			original, err := r.owner.node(ctx, node, false)
			if err != nil {
				return err
			}
			if !metadataValueEqual(original, value.FieldByName(name)) {
				return ErrBoundedMetadataCoverage
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return r.source.Transform(ctx, func(name string, original metadataLogicalNode) (metadataAggregateNode, bool, error) {
		switch name {
		case "agentSessions":
			return agentSessionLifecycleCollection{base: original, ordinals: r.ordinals, sessions: r.data.AgentSessions, messageOrdinals: r.messages}, len(r.ordinals) != 0, nil
		case "agentActivities":
			return agentSessionLifecycleCollection{base: original, activities: r.data.AgentActivities}, len(r.data.AgentActivities) != 0, nil
		default:
			return metadataAggregateStoredNode{value: original}, false, nil
		}
	})
}

type agentSessionLifecycleCollection struct {
	base            metadataLogicalNode
	ordinals        []int
	sessions        []domain.AgentSession
	activities      []domain.AgentActivity
	messageOrdinals [][]int
}

func (n agentSessionLifecycleCollection) nodeType() reflect.Type           { return n.base.typ }
func (n agentSessionLifecycleCollection) logicalBase() metadataLogicalNode { return n.base }
func (n agentSessionLifecycleCollection) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n agentSessionLifecycleCollection) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	selected, count := 0, 0
	err := n.base.Children(ctx, func(key reflect.Value, node metadataLogicalNode) error {
		var child metadataAggregateNode = metadataAggregateStoredNode{value: node}
		if selected < len(n.ordinals) && int(key.Int()) == n.ordinals[selected] {
			child = agentSessionLifecycleNode{
				base: node, session: n.sessions[selected], messageOrdinals: n.messageOrdinals[selected],
			}
			selected++
		}
		count++
		return visit(key, child)
	})
	if err != nil {
		return err
	}
	for _, activity := range n.activities {
		if err := visit(reflect.ValueOf(count), metadataAggregateNativeNode{value: reflect.ValueOf(activity)}); err != nil {
			return err
		}
		count++
	}
	return ctx.Err()
}

type agentSessionLifecycleNode struct {
	base            metadataLogicalNode
	session         domain.AgentSession
	messageOrdinals []int
}

func (n agentSessionLifecycleNode) nodeType() reflect.Type { return n.base.typ }
func (n agentSessionLifecycleNode) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n agentSessionLifecycleNode) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	value := reflect.ValueOf(n.session)
	return n.base.Children(ctx, func(key reflect.Value, node metadataLogicalNode) error {
		var child metadataAggregateNode = metadataAggregateStoredNode{value: node}
		switch key.String() {
		case "UpdatedAt", "LastReadAt":
			child = metadataAggregateNativeNode{value: value.FieldByName(key.String())}
		case "Messages":
			child = agentSessionLifecycleMessages{base: node, ordinals: n.messageOrdinals, messages: n.session.Messages}
		}
		return visit(key, child)
	})
}

type agentSessionLifecycleMessages struct {
	base     metadataLogicalNode
	ordinals []int
	messages []domain.AgentMessage
}

func (n agentSessionLifecycleMessages) nodeType() reflect.Type           { return n.base.typ }
func (n agentSessionLifecycleMessages) logicalBase() metadataLogicalNode { return n.base }
func (n agentSessionLifecycleMessages) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n agentSessionLifecycleMessages) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	selected := 0
	return n.base.Children(ctx, func(key reflect.Value, node metadataLogicalNode) error {
		var child metadataAggregateNode = metadataAggregateStoredNode{value: node}
		if selected < len(n.ordinals) && int(key.Int()) == n.ordinals[selected] {
			child = metadataAggregateNativeNode{value: reflect.ValueOf(n.messages[selected])}
			selected++
		}
		return visit(key, child)
	})
}

// MutateAgentSessionLifecycle preserves the literal full writer off mode.
// Bounded mode admits only native selected session occurrences and newly
// appended activities. It neither grants access nor loads offloaded bodies.
func (s *SQLiteStore) MutateAgentSessionLifecycle(ctx context.Context, workspace, event, aggregate string, scope AgentSessionLifecycleScope, mutate func(*domain.Bootstrap) error) error {
	if !s.BoundedMetadataEnabled() {
		return s.MutateWorkspace(ctx, workspace, event, aggregate, nil, mutate)
	}
	if mutate == nil || !metadataOutputOwnerPresent(ctx) ||
		event != "agent.message_updated" && event != "agent.message_completed" && event != "agent.session_read" {
		return ErrBoundedMetadataCoverage
	}
	fields, fieldScoped := metadataFieldsFromContext(ctx)
	if scope.PersistActor == fieldScoped || fieldScoped &&
		(!slices.Contains(fields, "agentSessions") || scope.AppendActivities != slices.Contains(fields, "agentActivities")) {
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
	var committed domain.DomainEvent
	var realtime json.RawMessage
	var publicationWork *metadataAggregatePublication
	defer func() {
		if publicationWork != nil {
			publicationWork.Close()
		}
	}()
	written := false
	apply := func(ctx context.Context) error {
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		if s.coordinator != nil {
			if err := s.reloadResidentWorkspace(ctx, workspace); err != nil {
				return fmt.Errorf("reload workspace before mutation: %w", err)
			}
		}
		if _, found := s.storedWorkspace(workspace); !found {
			return fmt.Errorf("workspace %q: not found", workspace)
		}
		unbind, err := s.bindMetadataTimestampInputs(ctx)
		if err != nil {
			return err
		}
		defer unbind()
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
		prepared := before
		if scope.PersistActor {
			prepared, err = s.refreshLegacyDisplayImage(ctx, before)
			if err != nil {
				return err
			}
			defer prepared.Release()
		}
		var actor *domain.User
		if value, found := actorFromContext(ctx); found {
			actor = &value
		}
		actorImage, err := s.prepareLegacyActorImage(ctx, prepared, actor)
		if err != nil {
			return err
		}
		defer actorImage.Close()
		inputs, err := s.loadAgentSessionLifecycleInputs(ctx, actorImage.Source(), scope)
		if err != nil {
			return err
		}
		transferred := false
		defer func() {
			if !transferred {
				inputs.Close()
			}
		}()
		// Failed attempts retain callback-native values through their output
		// owner, but their aggregate maps/encoded sink frames have no
		// consumer. Release that phase before a retry. Successful frames
		// transfer to the outer synchronous publication lifetime.
		var sinkWork *metadataAggregatePublication
		sinkTransferred := false
		defer func() {
			if !sinkTransferred && sinkWork != nil {
				sinkWork.Close()
			}
		}()
		nativeRole := inputs.data.ViewerRole
		if actor != nil {
			role, teams, apply, err := s.boundedLegacyActorDirectory(ctx, inputs.data.Workspace.ID, actor.ID, inputs.owner)
			if err != nil {
				return err
			}
			if apply {
				inputs.data.ViewerRole, inputs.data.ViewerTeamIDs = role, teams
			}
		}
		budget := &boundedDiscussionAttempt{owner: inputs.owner}
		if err := budget.reserveValue(ctx, scope.CallbackInput); err != nil {
			return err
		}
		// Terminal interruption may append one bounded activity/error frame
		// per existing message. Own that output before invoking its callback.
		for _, session := range inputs.data.AgentSessions {
			if err := budget.reserveValue(ctx, session); err != nil {
				return err
			}
			if uint64(len(session.Messages)) > (metadataWorkBytes-(64<<10))/(16<<10) {
				return ErrMetadataWorkBudget
			}
			if err := inputs.owner.reserve(ctx, uint64(len(session.Messages))*(16<<10)+64<<10); err != nil {
				return err
			}
		}
		if !registerMetadataOutputClose(ctx, inputs.Close) {
			return ErrBoundedMetadataCoverage
		}
		transferred = true
		if err := mutate(&inputs.data); err != nil {
			return err
		}
		inputs.data.ViewerRole, inputs.data.ViewerTeamIDs = nativeRole, nil
		for _, value := range []any{inputs.data.AgentSessions, inputs.data.AgentActivities} {
			if err := budget.reserveValue(ctx, value); err != nil {
				return err
			}
		}
		callback, err := inputs.merge(ctx, scope)
		if err != nil {
			return err
		}
		defer callback.Release()
		target := callback
		if !scope.PersistActor {
			target, err = callback.Transform(ctx, func(name string, original metadataLogicalNode) (metadataAggregateNode, bool, error) {
				if name == "viewer" || name == "users" {
					index, _, _ := metadataLogicalField(name)
					return metadataAggregateStoredNode{value: before.fields[index]}, true, nil
				}
				return metadataAggregateStoredNode{value: original}, false, nil
			})
			if err != nil {
				return err
			}
			defer target.Release()
		}
		sinkWork, err = s.prepareMetadataAggregatePublication(ctx,
			metadataAggregateImage{Source: before}, metadataAggregateImage{Source: target},
			aggregate, webhook, json.RawMessage("null"), event)
		if err != nil {
			return err
		}
		previous, nextRealtime := sinkWork.PreviousValues, sinkWork.RealtimePayload
		nextEvent := domain.DomainEvent{ID: idgen.New("evt_"), Type: event, AggregateID: aggregate, Payload: json.RawMessage("null"), PreviousValues: previous, CreatedAt: time.Now().UTC()}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if s.metadataResidency.requireCoverage {
			if err := checkRecordJournalCoverage(ctx, tx, workspace); err != nil {
				return err
			}
		}
		stamp, err := readMetadataStamp(ctx, tx, workspace)
		if err != nil {
			return err
		}
		if stamp.workspaceID == "" || stamp.workspaceID != stored.Stamp().workspaceID {
			return ErrResourceAccessChanged
		}
		persistCtx := context.WithValue(ctx, legacyMetadataExtraFieldsKey{}, []string{"agentSessions", "agentActivities"})
		persistCtx = context.WithValue(persistCtx, agentSessionLifecycleWriteKey{}, true)
		merged, err := s.persistLegacyMetadataImage(persistCtx, tx, workspace, stored, target)
		if err != nil {
			return err
		}
		defer merged.Release()
		candidate, err := metadataLogicalInstalledCandidate(ctx, merged)
		if err != nil {
			return err
		}
		publication, err := s.prepareLegacyResidentPublication(ctx, tx, workspace, candidate, stamp, stamp == stored.Stamp(), true)
		if err != nil {
			candidate.Release()
			return err
		}
		defer publication.Close()
		if err := budget.reserveValue(ctx, s.viewer); err != nil {
			return err
		}
		viewerRaw, _ := json.Marshal(s.viewer)
		if len(viewerRaw) == 0 || string(viewerRaw) == "{}" {
			index, _, _ := metadataLogicalField("viewer")
			value, err := inputs.owner.node(ctx, merged.fields[index], false)
			if err != nil {
				return err
			}
			if err := budget.reserveValue(ctx, value.Interface()); err != nil {
				return err
			}
			viewerRaw, _ = json.Marshal(value.Interface())
		}
		if err := writeAccountMetadata(ctx, tx, workspace, viewerRaw); err != nil {
			return err
		}
		storedPrevious, err := storedEventPreviousValues(ctx, tx, workspace, event, aggregate, previous)
		if err != nil {
			return err
		}
		if _, err := insertDomainEvent(ctx, tx, domainEventRow{ID: nextEvent.ID, Type: event, AggregateID: aggregate, Payload: nextEvent.Payload,
			PreviousValues: storedPrevious, CreatedAt: nextEvent.CreatedAt.Format(time.RFC3339Nano), WorkspaceID: stamp.workspaceID, WorkspaceKey: workspace}); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		written = true
		if err := publication.Install(); err != nil {
			return err
		}
		committed, realtime = nextEvent, nextRealtime
		publicationWork, sinkTransferred = sinkWork, true
		return nil
	}
	err = mutationAttempts(ctx, fullMutationAttempts, func(ctx context.Context) error {
		if s.coordinator != nil {
			return s.coordinator.WithWorkspaceLock(ctx, workspace, func() error { return apply(ctx) })
		}
		return apply(ctx)
	}, s.refreshAgentSessionLifecycleConflict(ctx, workspace))
	if written {
		s.dropMetadataCache(ctx, workspace)
	}
	if errors.Is(err, ErrNoMutation) {
		return nil
	}
	if err != nil {
		return err
	}
	s.publishMutation(ctx, workspace, committed, realtime)
	return nil
}
