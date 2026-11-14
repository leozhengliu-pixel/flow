package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// BoundedMetadataEnabled describes this store, not a process environment
// variable that could change after OpenDatabase selected its representation.
func (s *SQLiteStore) BoundedMetadataEnabled() bool { return s.metadataResidency != nil }

// BoundedMetadataViewForTesting shares canonical SQL with a test fixture while
// loading only resident policy. It cannot activate a production application.
// The caller must keep the owning store alive and must not Close this view.
func (s *SQLiteStore) BoundedMetadataViewForTesting(ctx context.Context) (*SQLiteStore, error) {
	if !testing.Testing() {
		return nil, ErrBoundedMetadataCoverage
	}
	view := &SQLiteStore{db: s.db, dialect: s.dialect, maxStateBytes: s.maxStateBytes, viewer: s.viewer, workspaces: map[string]domain.Bootstrap{}, metadataResidency: newMetadataResidency()}
	view.metadataResidency.requireCoverage = false
	if err := view.loadResidentWorkspaces(ctx); err != nil {
		return nil, err
	}
	if err := view.captureLogicalTestView(ctx, s, view.WorkspaceKeys()); err != nil {
		return nil, err
	}
	return view, nil
}

// WorkspaceMetadataScoped is a trusted internal read. The off branch is the
// exact legacy accessor. In bounded mode omitted data is unavailable, not empty
// canonical state; callers must declare their complete reference closure.
func (s *SQLiteStore) WorkspaceMetadataScoped(ctx context.Context, workspace string, scope MetadataRecordScope) (domain.Bootstrap, bool, error) {
	if !s.BoundedMetadataEnabled() {
		data, ok := s.WorkspaceMetadata(workspace)
		return data, ok, nil
	}
	data, err := s.readScopedMetadata(ctx, workspace, scope)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Bootstrap{}, false, nil
	}
	return data, err == nil, err
}

// WorkspaceMetadataFieldsScoped preserves the field-only legacy accessor.
func (s *SQLiteStore) WorkspaceMetadataFieldsScoped(ctx context.Context, workspace string, scope MetadataRecordScope, legacyFields ...string) (domain.Bootstrap, bool, error) {
	if !s.BoundedMetadataEnabled() {
		data, ok := s.WorkspaceMetadataFields(workspace, legacyFields...)
		return data, ok, nil
	}
	return s.WorkspaceMetadataScoped(ctx, workspace, scope)
}

func (s *SQLiteStore) readScopedMetadata(ctx context.Context, workspace string, scope MetadataRecordScope) (domain.Bootstrap, error) {
	return s.readScopedMetadataResult(ctx, workspace, scope, nil)
}

func (s *SQLiteStore) readScopedMetadataWithOrigin(ctx context.Context, workspace string, scope MetadataRecordScope) (domain.Bootstrap, metadataLogicalOrigin, error) {
	var origin metadataLogicalOrigin
	data, err := s.readScopedMetadataResult(ctx, workspace, scope, &origin)
	return data, origin, err
}

func (s *SQLiteStore) readScopedMetadataResult(ctx context.Context, workspace string, scope MetadataRecordScope, origin *metadataLogicalOrigin) (domain.Bootstrap, error) {
	if !RecordHydrationEnabled() {
		return domain.Bootstrap{}, ErrBoundedMetadataCoverage
	}
	if read, handled, err := installedMetadataReadFromContext(ctx, s, workspace); handled {
		if err != nil {
			return domain.Bootstrap{}, err
		}
		selection, err := read.Select(ctx, scope)
		if err != nil {
			return domain.Bootstrap{}, err
		}
		if origin != nil {
			*origin = selection.origin
		}
		return selection.Data, nil
	}
	s.mu.RLock()
	if workspace == "" {
		workspace = s.lastWorkspaceKey
	}
	_, installed := s.workspaces[workspace]
	s.mu.RUnlock()
	// The legacy accessor uses exact installed-map identity. SQL collations
	// and Postgres text normalization must not turn a missing key into one.
	if !installed || s.dialect == "postgres" && strings.ContainsRune(workspace, '\x00') {
		if err := ctx.Err(); err != nil {
			return domain.Bootstrap{}, err
		}
		return domain.Bootstrap{}, sql.ErrNoRows
	}
	if err := validateMetadataRecordScope(scope); err != nil {
		return domain.Bootstrap{}, err
	}
	source, err := s.retainMetadataLogicalSource(ctx, workspace)
	if err != nil {
		return domain.Bootstrap{}, err
	}
	defer source.Release()
	if origin != nil {
		*origin = metadataLogicalOrigin{workspace: workspace, stamp: source.Stamp()}
	}
	ctx, release, err := s.admitMetadataWork(ctx, 2<<20)
	if err != nil {
		return domain.Bootstrap{}, err
	}
	defer release()
	data, err := source.Select(ctx, scope, 2<<20)
	if errors.Is(err, ErrMetadataWorkBudget) {
		return domain.Bootstrap{}, ErrMetadataPageBudget
	}
	if err == nil {
		cost, costErr := metadataAggregateNativeCost(ctx, reflect.ValueOf(data), 0)
		if costErr != nil {
			return domain.Bootstrap{}, costErr
		}
		encodingWork := cost*8 + 64<<10
		if encodingWork > metadataWorkBytes {
			return domain.Bootstrap{}, ErrMetadataPageBudget
		}
		if encodingWork > 2<<20 {
			_, releaseEncoding, admitErr := s.tryAdmitAdditionalMetadataWork(ctx, int(encodingWork)-(2<<20))
			if admitErr != nil {
				return domain.Bootstrap{}, admitErr
			}
			defer releaseEncoding()
		}
		err = validateMetadataLogicalSelection(data, scope)
	}
	return data, err
}

// ProjectScopedMetadata projects declared inputs using live principal policy.
// It does not load directory or body collections implicitly. Fields requiring a
// wider reference graph must use their dedicated authorized record/page API.
func (s *SQLiteStore) ProjectScopedMetadata(ctx context.Context, workspace, principal string, key *domain.APIKey, scope MetadataRecordScope) (domain.Bootstrap, error) {
	access, err := s.ResolveAccess(ctx, workspace, principal, key)
	if err != nil {
		return domain.Bootstrap{}, err
	}
	// The generic settings projection is deliberately narrow. Entity families
	// have dedicated authorization, reference redaction and count contracts.
	for _, field := range metadataRecordFields(scope) {
		if !slices.Contains([]string{"settings", "workspaceSettings", "identityProviders", "integrationConnections", "customEmojis"}, field) {
			return domain.Bootstrap{}, fmt.Errorf("%w: %s needs its authorized record/page adapter", ErrMetadataRecordQuery, field)
		}
	}
	// Settings also contains private posts, meetings and dashboards whose
	// projection needs entity references. Admit only explicitly reviewed keys.
	if slices.Contains(scope.Fields, "settings") {
		return domain.Bootstrap{}, fmt.Errorf("%w: settings requires reviewed point keys", ErrMetadataRecordQuery)
	}
	for _, record := range append(slices.Clone(scope.Read), scope.Write...) {
		if record.Kind == "settings" && record.ID != "applicationPolicies" {
			return domain.Bootstrap{}, fmt.Errorf("%w: settings/%s needs its authorized adapter", ErrMetadataRecordQuery, record.ID)
		}
	}
	data, origin, err := s.readScopedMetadataWithOrigin(ctx, workspace, scope)
	if err != nil {
		return domain.Bootstrap{}, err
	}
	if err := s.validateMetadataLogicalOrigin(ctx, origin, access.WorkspaceID); err != nil {
		return domain.Bootstrap{}, err
	}
	data.Viewer, data.ViewerRole = access.policy.metadata.Viewer, access.policy.metadata.ViewerRole
	if err := s.ValidateAccess(ctx, access); err != nil {
		return domain.Bootstrap{}, err
	}
	return data, nil
}

// MutateWorkspaceScoped and its aggregate variant declare metadata inputs.
// Authorization remains in the existing middleware/callback. Off mode forwards
// the original context unchanged; no scope, verification, or flag is injected.
func (s *SQLiteStore) MutateWorkspaceScoped(ctx context.Context, workspace, event, aggregate string, payload any, scope MetadataRecordScope, mutate func(*domain.Bootstrap) error) error {
	if !s.BoundedMetadataEnabled() {
		return s.MutateWorkspace(ctx, workspace, event, aggregate, payload, mutate)
	}
	return s.MutateWorkspaceScopedWithAggregate(ctx, workspace, event, payload, scope, func(data *domain.Bootstrap) (string, error) {
		return aggregate, mutate(data)
	})
}

func (s *SQLiteStore) MutateWorkspaceScopedWithAggregate(ctx context.Context, workspace, event string, payload any, scope MetadataRecordScope, mutate func(*domain.Bootstrap) (string, error)) error {
	if !s.BoundedMetadataEnabled() {
		return s.MutateWorkspaceWithAggregate(ctx, workspace, event, payload, mutate)
	}
	if !RecordHydrationEnabled() {
		return ErrBoundedMetadataCoverage
	}
	ctx = metadataLogicalUnpinnedContext(ctx)
	actor, hasActor := actorFromContext(ctx)
	actor = mutationInputActor(actor)
	refreshActor := hasActor
	fields, fieldScoped := metadataFieldsFromContext(ctx)
	if original, selected := metadataRecordScopeFromContext(ctx); selected && !projectRecordSelectionEnabled(ctx) {
		fields, fieldScoped = metadataRecordFields(original), true
	}
	metadataOnly := metadataOnlyMutation(event, payload)
	legacyFieldScoped := !projectRecordSelectionEnabled(ctx) && !metadataTeamMutation(event, payload) && metadataOnly && fieldScoped
	if legacyFieldScoped && !slices.Contains(fields, "users") {
		refreshActor = false
	}
	if refreshActor && !slices.Contains(scope.Fields, "users") {
		scope.Read = slices.DeleteFunc(slices.Clone(scope.Read), func(key RecordKey) bool {
			return key.Kind == "users" && key.ID == actor.ID
		})
		if !metadataRecordSelected(scope, "users", actor.ID, true) {
			scope.Write = append(slices.Clone(scope.Write), RecordKey{Kind: "users", ID: actor.ID})
		}
	}
	if err := validateMetadataRecordScope(scope); err != nil {
		return err
	}
	if _, content := mutationScopeFromContext(ctx); content || UsesIssueRecordMutations(ctx) {
		return fmt.Errorf("%w: scoped metadata callback declares issue/content side effects", ErrMetadataRecordQuery)
	}
	policyWrite := slices.ContainsFunc(append(metadataRecordFields(scope), metadataAppendKinds(ctx)...), func(field string) bool {
		mode := metadataResidencyFields()[field]
		return mode == "policy" || mode == "core"
	})
	policyWrite = policyWrite || hasActor && (!legacyFieldScoped || slices.Contains(fields, "viewer"))
	reservation := 8 << 20
	if metadataScopeResolver(ctx) != nil {
		reservation += 8 << 20
	}
	if policyWrite {
		reservation += metadataPolicyBytes
	}
	if !metadataOnly {
		reservation = max(reservation, metadataLogicalExternalWork)
	}
	if s.coordinator != nil {
		reservation = max(reservation, metadataPolicyBytes+2<<20)
	}
	if inputs, _ := ctx.Value(metadataTimestampInputsKey{}).(*MetadataTimestampInputs); inputs != nil {
		reservation += metadataTimestampInputBytes
	}
	ctx, finishTimestampInputs, err := s.startMetadataTimestampInputs(ctx)
	if err != nil {
		return err
	}
	defer finishTimestampInputs()
	ctx, release, err := s.admitMetadataWork(ctx, reservation)
	if err != nil {
		return err
	}
	defer release()
	if workspace == "" {
		s.mu.RLock()
		workspace = s.lastWorkspaceKey
		s.mu.RUnlock()
	}
	ctx = WithMetadataRecords(ctx, scope)
	ctx = context.WithValue(ctx, scopedMetadataWriteKey{}, true)
	ctx, collector, callback := collectMutationAudit(ctx, workspace, mutate)
	var committed domain.DomainEvent
	var realtime json.RawMessage
	var releaseEvent func()
	defer func() {
		if releaseEvent != nil {
			releaseEvent()
		}
	}()
	apply := func(ctx context.Context) error {
		if releaseEvent != nil {
			committed, realtime = domain.DomainEvent{}, nil
			releaseEvent()
			releaseEvent = nil
		}
		scope := scope
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		if s.coordinator != nil {
			if err := s.reloadResidentWorkspace(ctx, workspace); err != nil {
				return err
			}
		}
		unbindTimestampInputs, err := s.bindMetadataTimestampInputs(ctx)
		if err != nil {
			return err
		}
		defer unbindTimestampInputs()
		logicalSource, logicalErr := s.retainMetadataLogicalSource(ctx, workspace)
		if logicalErr != nil {
			return logicalErr
		}
		defer logicalSource.Release()
		actor := mutationInputActor(actor)
		workspaceID, err := s.WorkspaceIDForKey(ctx, workspace)
		if err != nil {
			return err
		}
		role, teams, activeActor := "", []string(nil), false
		if hasActor {
			if currentRole, status, roleErr := s.WorkspaceRole(ctx, workspaceID, actor.ID); roleErr == nil && status == "active" {
				role, teams, activeActor = currentRole, s.actorTeamIDs(ctx, workspaceID, actor.ID), true
			}
		}
		hooks, err := s.logicalWebhookNeeded(ctx, logicalSource, workspace)
		if err != nil {
			return err
		}
		tx, err := s.db.BeginTx(ctx, s.recordTxOptions())
		if err != nil {
			return err
		}
		defer tx.Rollback()
		// Serialize collection membership as well as existing rows. This is
		// required for callbacks that allocate IDs or validate uniqueness.
		lock := ""
		if tx.dialect != "sqlite" {
			lock = " FOR UPDATE"
		}
		var raw []byte
		if err := tx.QueryRowContext(ctx, `SELECT CASE WHEN LENGTH(data)<=? THEN data ELSE NULL END FROM workspace_states WHERE workspace_key=?`+lock, MetadataRecordPageMaxBytes, workspace).Scan(&raw); err != nil {
			return err
		}
		if raw == nil {
			return ErrMetadataPageBudget
		}
		if _, _, err := residentMetadataRoot(raw); err != nil {
			return err
		}
		if s.metadataResidency.requireCoverage {
			if err := checkRecordJournalCoverage(ctx, tx, workspace); err != nil {
				return err
			}
		}
		baseStamp, err := readMetadataStamp(ctx, tx, workspace)
		if err != nil {
			return err
		}
		exactSource := baseStamp == logicalSource.Stamp()
		if s.coordinator != nil && !exactSource {
			return ErrConcurrentWrite
		}
		storageBefore, err := s.decodeWorkspaceMetadata(ctx, workspace, raw, tx)
		if err != nil {
			return err
		}
		before, err := logicalSource.Select(ctx, scope, 2<<20)
		if err != nil {
			return err
		}
		if before.Workspace.ID != workspaceID {
			return ErrResourceAccessChanged
		}
		eventSource := logicalSource
		if !metadataOnly {
			eventSource, err = s.hydrateMetadataLogicalSource(ctx, tx, logicalSource, workspace)
			if err != nil {
				return err
			}
			defer eventSource.Release()
		}
		callbackSource := eventSource
		refreshComplete := !legacyFieldScoped || slices.Contains(fields, "users")
		deferredRefresh := refreshComplete && metadataOnly && eventSource.units != nil
		if refreshComplete && !deferredRefresh {
			callbackSource, err = metadataAggregateDisplaySource(ctx, eventSource, fields, legacyFieldScoped)
			if err != nil {
				return err
			}
			defer callbackSource.Release()
		}
		if !metadataOnly {
			callbackSource, err = refreshMetadataLogicalIssueReferences(ctx, callbackSource)
			if err != nil {
				return err
			}
			defer callbackSource.Release()
		}
		if resolve := metadataScopeResolver(ctx); resolve != nil {
			keys, err := resolve(cloneBootstrap(before))
			if err != nil {
				return err
			}
			scope, err = extendMetadataReadScope(scope, keys)
			if err != nil {
				return err
			}
			ctx = WithMetadataRecords(ctx, scope)
			storageBefore, err = s.decodeWorkspaceMetadata(ctx, workspace, raw, tx)
			if err != nil {
				return err
			}
			before, err = logicalSource.Select(ctx, scope, 2<<20)
			if err != nil {
				return err
			}
			if before.Workspace.ID != workspaceID {
				return ErrResourceAccessChanged
			}
		}
		next, err := callbackSource.Select(ctx, scope, 2<<20)
		if err != nil {
			return err
		}
		if deferredRefresh {
			err = refreshSelectedMetadataLogicalReferences(ctx, eventSource, &next, scope, fields, legacyFieldScoped, 2<<20)
			if err != nil {
				if cancelErr := ctx.Err(); cancelErr != nil {
					return cancelErr
				}
				// Unsupported selected layouts keep the original streamed
				// transform and selection path, with its original errors.
				callbackSource, err = metadataAggregateDisplaySource(ctx, eventSource, fields, legacyFieldScoped)
				if err != nil {
					return err
				}
				defer callbackSource.Release()
				deferredRefresh = false
				next, err = callbackSource.Select(ctx, scope, 2<<20)
				if err != nil {
					return err
				}
			}
		}
		originalRole := next.ViewerRole
		refreshDisplayReferences(&next)
		if hasActor {
			next.Viewer = actor
			if activeActor {
				next.ViewerRole, next.ViewerTeamIDs = role, teams
			}
			if refreshActor {
				if index := slices.IndexFunc(next.Users, func(user domain.User) bool { return user.ID == actor.ID }); index >= 0 {
					next.Users[index] = actor
				} else {
					next.Users = append(next.Users, actor)
				}
			}
		}
		validationBefore := before
		if metadataScopeResolver(ctx) != nil || refreshComplete {
			validationBefore = cloneBootstrap(next)
			validationBefore.ViewerRole, validationBefore.ViewerTeamIDs = originalRole, nil
		}
		next.FormerMembers = s.formerMemberLookup(ctx, tx, &next)
		next.CollaborationUpdates = collaborationUpdatesLookup(ctx, tx, workspace)
		aggregate, err := callback(&next)
		next.ViewerRole, next.ViewerTeamIDs, next.FormerMembers, next.CollaborationUpdates = originalRole, nil, nil, nil
		if err != nil {
			return err
		}
		if deferredRefresh {
			callbackSource, err = metadataAggregateDisplaySource(ctx, eventSource, fields, legacyFieldScoped)
			if err != nil {
				return err
			}
			defer callbackSource.Release()
		}
		if legacyFieldScoped && !slices.Contains(fields, "viewer") {
			next.Viewer = before.Viewer
		}
		writeScope, appendOrders, err := s.prepareMetadataAppends(ctx, tx, workspace, before, next, scope)
		if err != nil {
			return err
		}
		if err := validateScopedMetadataChanges(validationBefore, next, writeScope); err != nil {
			return err
		}
		encoded, err := json.Marshal(next)
		if err != nil {
			return err
		}
		budget := newMetadataHydrationBudget()
		if err := budget.consume(encoded); err != nil {
			return err
		}
		root, shapes, _, err := splitWorkspaceMetadata(encoded)
		if err != nil {
			return err
		}
		for _, field := range metadataRecordFields(writeScope) {
			value := root[field]
			if shapes[field] == "" && len(value) > 2 && (value[0] == '[' || value[0] == '{') && metadataResidencyFields()[field] != "core" {
				return fmt.Errorf("%w: unsplittable callback output %s", ErrMetadataRecordQuery, field)
			}
		}
		payloadWork, releasePayload, err := s.reserveMetadataAggregatePayload(ctx, payload)
		if err != nil {
			return err
		}
		defer func() {
			if releasePayload != nil {
				releasePayload()
			}
		}()
		payloadRaw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		committed = domain.DomainEvent{ID: idgen.New("evt_"), Type: event, AggregateID: aggregate, Payload: payloadRaw, CreatedAt: time.Now().UTC()}
		beforeImage := metadataAggregateImage{Source: eventSource, Selected: &before, Scope: scope}
		afterImage := metadataAggregateImage{Source: callbackSource, Selected: &next, Scope: writeScope}
		plan, err := planMetadataAggregatePayload(ctx, beforeImage, afterImage, aggregate, hooks && s.webhookConfigured(), payloadRaw)
		if err != nil {
			return err
		}
		defer plan.Close()
		releaseExtra := func() {}
		if extra := plan.RequiredBytes - payloadWork; extra > 0 {
			_, releaseExtra, err = s.tryAdmitAdditionalMetadataWork(ctx, extra)
			if err != nil {
				return err
			}
		}
		ownedPayload := releasePayload
		releaseEvent = func() {
			releaseExtra()
			ownedPayload()
		}
		releasePayload = nil
		previous, entity, err := plan.Values(ctx)
		if err != nil {
			return err
		}
		committed.PreviousValues = previous
		realtime = enrichRealtimePayload(payloadRaw, entity, event)
		// Complete fields use the proven delta writer. Point records are
		// written separately so a deletion never scans the whole collection.
		complete := cloneBootstrap(next)
		persistBefore := storageBefore
		left, logicalBefore, right := reflect.ValueOf(&persistBefore).Elem(), reflect.ValueOf(&before).Elem(), reflect.ValueOf(&complete).Elem()
		for _, field := range persistedMetadataFields() {
			if field.name == "viewer" || slices.Contains(scope.Fields, field.name) {
				left.Field(field.index).Set(logicalBefore.Field(field.index))
			} else {
				right.Field(field.index).Set(left.Field(field.index))
			}
		}
		inputCtx := context.WithValue(ctx, scopedMetadataImportInputsKey{}, scopedMetadataImportInputs{data: &next, scope: writeScope})
		if err := s.persistWorkspaceTx(inputCtx, tx, workspace, &persistBefore, &complete, nil, &committed, nil); err != nil {
			return err
		}
		changes, _, err := scaleP16RecordChanges(before, next)
		if err != nil {
			return err
		}
		changes = slices.DeleteFunc(changes, func(change metadataRecordChange) bool {
			if slices.Contains(scope.Fields, change.field) {
				return true
			}
			// The resolved callback was checked against its refreshed input.
			// Reference-only changes to read records enrich events but must
			// not rewrite canonical rows, as on the legacy delta path.
			return metadataScopeResolver(ctx) != nil && !metadataRecordSelected(scope, change.field, change.key, true)
		})
		for _, change := range changes {
			field, _, typed := metadataRecordType(change.field)
			isMap := typed && reflect.TypeFor[domain.Bootstrap]().Field(field.index).Type.Kind() == reflect.Map || change.field == "workspaceSettings"
			actorInsert := refreshActor && change.field == "users" && change.key == actor.ID
			_, appended := appendOrders[metadataRecordKey{field: change.field, key: change.key}]
			if change.insert && !isMap && !actorInsert && !appended {
				return fmt.Errorf("%w: point creation requires a complete field or a dedicated creation writer", ErrMetadataRecordQuery)
			}
			if change.insert && (actorInsert || appended) {
				if err := ensureOAuthMetadataShape(ctx, tx, workspace, change.field, "array", lock); err != nil {
					return err
				}
			}
			if change.insert && isMap {
				if err := ensureOAuthMetadataShape(ctx, tx, workspace, change.field, "map", lock); err != nil {
					return err
				}
			}
		}
		writeCtx := ctx
		if len(appendOrders) > 0 {
			writeCtx = context.WithValue(ctx, metadataAppendOrdersKey{}, appendOrders)
		}
		if err := scaleP16WriteChanges(writeCtx, tx, workspace, newIssueReferences(next), changes); err != nil {
			return err
		}
		candidate, err := callbackSource.Fork(ctx, before, next, writeScope, logicalSource.Stamp(), appendOrders)
		if err != nil {
			return err
		}
		merged, err := metadataLogicalMergedPostimage(ctx, candidate, changes)
		candidate.Release()
		if err != nil {
			return err
		}
		candidate = merged
		merged, err = metadataLogicalCompletePostimage(ctx, candidate, next, complete, scope)
		candidate.Release()
		if err != nil {
			return err
		}
		candidate = merged
		if !metadataOnly {
			installed, stripErr := metadataLogicalInstalledCandidate(ctx, candidate)
			candidate.Release()
			if stripErr != nil {
				return stripErr
			}
			candidate = installed
		}
		defer func() {
			if candidate != nil {
				candidate.Release()
			}
		}()
		if callbackSource != eventSource {
			if err := s.persistMetadataLogicalRefresh(ctx, tx, workspace, eventSource, callbackSource, scope, candidate); err != nil {
				return err
			}
		}
		capture := tx.captureMetadataCommitStampFrom(workspace, baseStamp)
		var policy domain.Bootstrap
		var cost int
		if policyWrite {
			// Check the candidate policy before commit, without asking the
			// transaction's not-yet-flushed journal to certify itself.
			probe := &SQLiteStore{db: s.db, dialect: s.dialect, metadataResidency: newMetadataResidency()}
			probe.metadataResidency.requireCoverage = false
			policy, _, cost, err = probe.readResidentWorkspace(ctx, tx, workspace)
			if err != nil {
				return err
			}
			policy, cost, err = s.metadataLogicalPolicy(ctx, candidate)
			if err != nil {
				return err
			}
			s.metadataResidency.mu.Lock()
			total := cost
			for key, bytes := range s.metadataResidency.policy {
				if key != workspace {
					total += bytes
				}
			}
			limit := s.metadataResidency.policyLimit
			s.metadataResidency.mu.Unlock()
			if total > limit {
				return ErrMetadataWorkBudget
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		finalStamp, ready := capture.result()
		if !ready {
			return ErrBoundedMetadataCoverage
		}
		sourceStamp := copyStamp{workspaceID: finalStamp.workspaceID, revision: -1}
		if exactSource {
			sourceStamp = finalStamp
		}
		if err := candidate.setStamp(sourceStamp); err != nil {
			return err
		}
		if policyWrite {
			// The writeMu serializes policy cost changes in this store. The
			// precommit check guarantees installation fits after commit too.
			if err := s.installResidentPolicySource(workspace, policy, finalStamp, 0, cost, true, candidate); err != nil {
				return err
			}
			candidate = nil
			return nil
		}
		s.installMetadataLogicalSource(workspace, candidate, true)
		candidate = nil
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
	}, s.refreshConflictedLogicalMetadata(ctx, workspace))
	if errors.Is(err, ErrNoMutation) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(collector.pending) != 0 {
		return fmt.Errorf("scoped mutation left uncommitted audit entries")
	}
	s.dropMetadataCache(ctx, workspace)
	// Publication owns a separate reservation and may synchronously start
	// another mutation after this operation has installed its source.
	release()
	s.publishMutation(ctx, workspace, committed, realtime)
	return nil
}

type scopedMetadataWriteKey struct{}

func validateScopedMetadataChanges(before, after domain.Bootstrap, scope MetadataRecordScope) error {
	if after.Issues != nil || after.Comments != nil || after.Activities != nil || after.Notifications != nil || after.NotificationDeliveries != nil {
		return ErrMetadataRecordQuery
	}
	left, right := reflect.ValueOf(&before).Elem(), reflect.ValueOf(&after).Elem()
	for _, field := range changedMetadataFields(before, after) {
		if field.name == "viewer" || slices.Contains(scope.Fields, field.name) {
			continue
		}
		a, b := left.Field(field.index), right.Field(field.index)
		if field.name == "workspaceSettings" {
			for i := 0; i < a.NumField(); i++ {
				if metadataValueEqual(a.Field(i), b.Field(i)) {
					continue
				}
				key := strings.Split(a.Type().Field(i).Tag.Get("json"), ",")[0]
				if !metadataRecordSelected(scope, field.name, key, true) {
					return fmt.Errorf("%w: undeclared write %s/%s", ErrMetadataRecordQuery, field.name, key)
				}
			}
			continue
		}
		if !rebaseableMetadataField(field, a.Type()) {
			return fmt.Errorf("%w: undeclared field %s", ErrMetadataRecordQuery, field.name)
		}
		old, next := newMetadataCollection(field, a), newMetadataCollection(field, b)
		keys := append(scopedCollectionKeys(old), scopedCollectionKeys(next)...)
		for _, id := range keys {
			x, xOK := old.record(id)
			y, yOK := next.record(id)
			if xOK == yOK && (!xOK || metadataValueEqual(x, y)) {
				continue
			}
			if !metadataRecordSelected(scope, field.name, id, true) {
				return fmt.Errorf("%w: undeclared write %s/%s", ErrMetadataRecordQuery, field.name, id)
			}
		}
		if a.Kind() == reflect.Slice {
			// A point selection cannot express collection reordering.
			previous := []string{}
			current := []string{}
			for _, id := range scopedCollectionKeys(old) {
				if _, exists := next.record(id); exists {
					previous = append(previous, id)
				}
			}
			for _, id := range scopedCollectionKeys(next) {
				if _, exists := old.record(id); exists {
					current = append(current, id)
				}
			}
			if !slices.Equal(previous, current) {
				return ErrMetadataRecordQuery
			}
		}
	}
	return nil
}

func scopedCollectionKeys(collection metadataCollection) []string {
	keys := make([]string, 0, len(collection.ids))
	for key := range collection.ids {
		keys = append(keys, key)
	}
	if collection.isMap() {
		slices.Sort(keys)
	} else {
		slices.SortFunc(keys, func(a, b string) int { return collection.ids[a] - collection.ids[b] })
	}
	return keys
}

// ScanMetadataRecords is a trusted, snapshot-consistent reader for exports and
// background jobs. Each callback owns at most one P15 page; retaining pages
// defeats the contract. It never accumulates records between callbacks.
func (s *SQLiteStore) ScanMetadataRecords(ctx context.Context, workspace, kind string, visit func(RecordBatch) error) error {
	if workspace == "" || !validMetadataKey(RecordKey{Kind: kind}) || visit == nil {
		return ErrMetadataRecordQuery
	}
	if _, ok := metadataFieldByName(kind); !ok {
		return ErrMetadataRecordQuery
	}
	ctx, release, err := s.admitMetadataWork(ctx, 2<<20)
	if err != nil {
		return err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return scanMetadataHeaders(ctx, tx, workspace, kind, func(key RecordKey, _ int64) error {
		batch, err := s.readCanonicalRecords(ctx, tx, workspace, []RecordKey{key}, "detail")
		if err != nil {
			return err
		}
		return visit(batch)
	})
}

// StreamMetadataRecords visits complete canonical JSON through fixed-size SQL
// chunks in one read snapshot. It preserves collection order and null/empty
// values. Readers are valid only during their callback; callers authorize and
// redact before exposing records, and must not retain or accumulate bodies.
func (s *SQLiteStore) StreamMetadataRecords(ctx context.Context, workspace, kind string, visit func(RecordKey, io.Reader) error) error {
	if workspace == "" || !validMetadataKey(RecordKey{Kind: kind}) || visit == nil {
		return ErrMetadataRecordQuery
	}
	if _, ok := metadataFieldByName(kind); !ok {
		return ErrMetadataRecordQuery
	}
	ctx, release, err := s.admitMetadataWork(ctx, 2<<20)
	if err != nil {
		return err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return scanMetadataHeaders(ctx, tx, workspace, kind, func(key RecordKey, size int64) error {
		reader := &canonicalMetadataReader{ctx: ctx, tx: tx, workspace: workspace, key: key, size: size}
		return visit(key, reader)
	})
}

func scanMetadataHeaders(ctx context.Context, tx *sqlTx, workspace, kind string, visit func(RecordKey, int64) error) error {
	// Chunk readers use bytes; tied keys follow sortMetadataRecordRows.
	keyOrder, keyBound, bodyLength := "record_key", "?", "LENGTH(data)"
	switch tx.dialect {
	case "sqlite":
		keyOrder, keyBound = "CAST(record_key AS BLOB)", "CAST(? AS BLOB)"
		bodyLength = "LENGTH(CAST(data AS BLOB))"
	case "mysql":
		keyOrder, keyBound = "CAST(record_key AS BINARY)", "CAST(? AS BINARY)"
	case "postgres":
		keyOrder, keyBound = `record_key COLLATE "C"`, `? COLLATE "C"`
	}
	last, started, ordinal := "", false, int64(0)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		where, args := "workspace_key=? AND field=?", []any{workspace, kind}
		if started {
			where += " AND (collection_order>? OR (collection_order=? AND " + keyOrder + ">" + keyBound + "))"
			args = append(args, ordinal, ordinal, last)
		}
		rows, err := tx.QueryContext(ctx, "SELECT record_key,collection_order,"+bodyLength+" FROM workspace_metadata_records WHERE "+where+" ORDER BY collection_order,"+keyOrder+" LIMIT 50", args...)
		if err != nil {
			return err
		}
		type header struct {
			id          string
			order, size int64
		}
		page := []header{}
		for rows.Next() {
			var item header
			if err := rows.Scan(&item.id, &item.order, &item.size); err != nil {
				rows.Close()
				return err
			}
			page = append(page, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(page) == 0 {
			return nil
		}
		// One detail per batch preserves large-row omission semantics without
		// letting a 50-key page pin fifty maximum-size bodies.
		for _, item := range page {
			if err := visit(RecordKey{Kind: kind, ID: item.id}, item.size); err != nil {
				return err
			}
		}
		last, started, ordinal = page[len(page)-1].id, true, page[len(page)-1].order
	}
}
