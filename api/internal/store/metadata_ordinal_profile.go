package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

type metadataOrdinalProfile interface {
	Input() *domain.Bootstrap
	Merge(context.Context) (*metadataLogicalSource, error)
	Close()
}

type metadataOrdinalProfileFactory func(context.Context, *metadataLogicalSource) (metadataOrdinalProfile, error)

// mutateMetadataOrdinalProfile supplies a closed ordinal adapter to the
// existing scoped mutation loop. It owns no transaction, retry or commit loop.
func (s *SQLiteStore) mutateMetadataOrdinalProfile(
	ctx context.Context, workspace, event, aggregate string, payload any,
	factory metadataOrdinalProfileFactory, mutate func(*domain.Bootstrap) error,
) error {
	// Validate this contract before timestamp/admission, factory or callback
	// work. A complete metadata-only image cannot stand in for a field scope.
	if s == nil || !s.BoundedMetadataEnabled() || factory == nil || mutate == nil ||
		!metadataOnlyMutation(event, payload) || !metadataOutputOwnerPresent(ctx) {
		return ErrBoundedMetadataCoverage
	}
	if _, scoped := metadataFieldsFromContext(ctx); scoped {
		return ErrBoundedMetadataCoverage
	}
	if _, scoped := metadataRecordScopeFromContext(ctx); scoped || metadataScopeResolver(ctx) != nil || len(metadataAppendKinds(ctx)) != 0 {
		return ErrBoundedMetadataCoverage
	}
	return s.mutateWorkspaceScopedWithAggregate(ctx, workspace, event, payload, MetadataRecordScope{},
		func(data *domain.Bootstrap) (string, error) { return aggregate, mutate(data) }, factory)
}

type metadataOrdinalAttemptResult struct {
	candidate *metadataLogicalSource
	event     domain.DomainEvent
	realtime  json.RawMessage
	release   func()
}

// applyMetadataOrdinalProfile performs only the alternate callback/image phase
// of one existing scoped attempt. Its caller owns the already-open transaction,
// audit collector, coordinator lock and common commit/install/publication tail.
func (s *SQLiteStore) applyMetadataOrdinalProfile(
	ctx context.Context, tx *sqlTx, workspace, workspaceID, eventType string,
	payload any, stored *metadataLogicalSource,
	actor domain.User, hasActor, activeActor bool, role string, teams []string, hooks bool,
	factory metadataOrdinalProfileFactory, callback func(*domain.Bootstrap) (string, error),
) (result metadataOrdinalAttemptResult, err error) {
	if !metadataOnlyMutation(eventType, payload) || factory == nil || callback == nil {
		return result, ErrBoundedMetadataCoverage
	}
	traceMark(ctx, "ordinal-enter")
	ctx = metadataLogicalSourceScratchContext(ctx, stored)
	before, err := cloneMetadataLogicalSnapshot(ctx, stored)
	traceMark(ctx, "ordinal-snapshot")
	if err != nil {
		return result, err
	}
	defer before.Release()
	// Literal full metadata callbacks refresh displays before substituting
	// their request actor. Reversing these passes changes embedded snapshots.
	display, err := s.refreshLegacyDisplayImage(ctx, before)
	traceMark(ctx, "ordinal-display")
	if err != nil {
		return result, err
	}
	defer display.Release()
	var callbackActor *domain.User
	if hasActor {
		callbackActor = &actor
	}
	adjusted, err := s.prepareLegacyActorImage(ctx, display, callbackActor)
	traceMark(ctx, "ordinal-actor")
	if err != nil {
		return result, err
	}
	defer adjusted.Close()
	profile, err := factory(ctx, adjusted.Source())
	traceMark(ctx, "ordinal-profile")
	if err != nil {
		if profile != nil {
			profile.Close()
		}
		return result, err
	}
	if profile == nil || profile.Input() == nil {
		if profile != nil {
			profile.Close()
		}
		return result, ErrBoundedMetadataCoverage
	}
	// The callback may capture a native value even when it returns an error.
	// Register before any callback work; failed registration releases locally.
	captured := &ResourceRead{store: s}
	captured.release = append(captured.release, profile.Close)
	if !registerMetadataOutputClose(ctx, captured.Close) {
		captured.Close()
		return result, ErrBoundedMetadataCoverage
	}
	data := profile.Input()
	if data.Workspace.ID != workspaceID {
		return result, ErrResourceAccessChanged
	}
	lookups, err := newIssueEditorLookups(ctx, &issueHotInputs{store: s, source: adjusted.Source()}, tx, workspace, workspaceID, domain.Issue{})
	if err != nil {
		return result, err
	}
	captured.release = append(captured.release, lookups.close)
	originalRole, originalTeams := data.ViewerRole, data.ViewerTeamIDs
	originalFormer, originalUpdates := data.FormerMembers, data.CollaborationUpdates
	if hasActor && activeActor {
		data.ViewerRole, data.ViewerTeamIDs = role, teams
	}
	data.FormerMembers = lookups.formerMembers
	data.CollaborationUpdates = collaborationUpdatesLookup(ctx, tx, workspace)
	traceMark(ctx, "load")
	aggregate, callbackErr := callback(data)
	data.ViewerRole, data.ViewerTeamIDs = originalRole, originalTeams
	data.FormerMembers, data.CollaborationUpdates = originalFormer, originalUpdates
	traceMark(ctx, "mutate")
	if callbackErr != nil {
		return result, callbackErr
	}
	if lookups.err != nil {
		return result, lookups.err
	}
	patched, err := profile.Merge(ctx)
	traceMark(ctx, "ordinal-merge")
	if err != nil {
		return result, err
	}
	if patched == nil {
		return result, ErrBoundedMetadataCoverage
	}
	defer patched.Release()
	_, releasePayload, err := s.reserveMetadataAggregatePayload(ctx, payload)
	if err != nil {
		return result, err
	}
	defer func() {
		if releasePayload != nil {
			releasePayload()
		}
	}()
	payloadRaw, err := json.Marshal(payload)
	if err != nil {
		return result, err
	}
	publication, err := s.prepareMetadataAggregatePublication(ctx,
		metadataAggregateImage{Source: before}, metadataAggregateImage{Source: patched},
		aggregate, hooks && s.webhookConfigured(), payloadRaw, eventType)
	traceMark(ctx, "ordinal-publication")
	if err != nil {
		return result, err
	}
	transferred := false
	defer func() {
		if !transferred {
			publication.Close()
		}
		if err != nil && result.candidate != nil {
			result.candidate.Release()
			result.candidate = nil
		}
	}()
	result.event = domain.DomainEvent{
		ID: idgen.New("evt_"), Type: eventType, AggregateID: aggregate,
		Payload: payloadRaw, PreviousValues: publication.PreviousValues, CreatedAt: time.Now().UTC(),
	}
	result.realtime = publication.RealtimePayload
	if err := s.persistLegacyImportInputs(ctx, tx, workspace, patched); err != nil {
		return result, err
	}
	traceMark(ctx, "ordinal-imports")
	// This closed kernel adds only the two SW202 collections to the proven
	// metadata-image writer; the profile rejects all other callback edits.
	writeCtx := context.WithValue(ctx, legacyMetadataExtraFieldsKey{}, []string{"integrationDeliveries", "integrationConnections"})
	result.candidate, err = s.persistLegacyMetadataImage(writeCtx, tx, workspace, stored, patched)
	traceMark(ctx, "ordinal-records")
	if err != nil {
		return result, err
	}
	tail := &ResourceRead{store: s}
	defer tail.Close()
	var identity domain.Bootstrap
	for _, name := range []string{"workspace", "viewer", "viewerRole"} {
		index, _, _ := metadataLogicalField(name)
		value, err := tail.node(ctx, result.candidate.fields[index], false)
		if err != nil {
			return result, err
		}
		switch name {
		case "workspace":
			identity.Workspace = value.Interface().(domain.Workspace)
		case "viewer":
			identity.Viewer = value.Interface().(domain.User)
		case "viewerRole":
			identity.ViewerRole = value.String()
		}
	}
	if identity.Workspace.ID != workspaceID {
		return result, fmt.Errorf("%w: ordinal postimage workspace changed", ErrMetadataRecordQuery)
	}
	// Reuse account/event persistence, stored previous-values and audit
	// attachment. The complete metadata image was already persisted above.
	tailCtx := context.WithValue(ctx, scopedMetadataPersistedKey{}, true)
	beforeIdentity := identity
	if err := s.persistWorkspaceTx(tailCtx, tx, workspace, &beforeIdentity, &identity, nil, &result.event, nil); err != nil {
		return result, err
	}
	traceMark(ctx, "ordinal-tail")
	ownedPayload := releasePayload
	result.release = func() {
		publication.Close()
		ownedPayload()
	}
	releasePayload = nil
	transferred = true
	return result, nil
}
