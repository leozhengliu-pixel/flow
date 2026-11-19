package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

var errLoopRunStartMatched = errors.New("loop run start record matched")

// LoopRunStartRead owns the installed inputs of one loop run start: the
// workspace key, the loops feature flag, the first loop with the requested ID
// (its complete native value) and, on request, the name of the run's entity.
// The installed generation stays pinned until Close; the decoded loop's
// allowance stays charged while any ExecutionLease is held, so a local run
// that keeps the loop until it finishes keeps its allowance too.
type LoopRunStartRead struct {
	Data domain.Bootstrap

	mu      sync.Mutex
	refs    int
	closed  bool
	initial sync.Once
	pin     *ResourceRead
	owned   *ResourceRead
}

func (r *LoopRunStartRead) release() {
	r.mu.Lock()
	r.refs--
	done := r.refs == 0
	owned := r.owned
	if done {
		r.Data, r.owned = domain.Bootstrap{}, nil
	}
	r.mu.Unlock()
	if done {
		owned.Close()
	}
}

// Close ends the start's own lifetime: the pinned generation is released at
// once and the loop allowance when the last execution lease ends.
func (r *LoopRunStartRead) Close() {
	if r == nil {
		return
	}
	r.initial.Do(func() {
		r.mu.Lock()
		r.closed = true
		pin := r.pin
		r.pin = nil
		r.mu.Unlock()
		pin.Close()
		r.release()
	})
}

// ExecutionLease keeps the captured loop's allowance after Close, for work
// that still uses the loop. The returned release is idempotent.
func (r *LoopRunStartRead) ExecutionLease() (func(), error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.refs == 0 {
		return nil, errMetadataLogicalClosed
	}
	r.refs++
	var once sync.Once
	return func() { once.Do(r.release) }, nil
}

// MutationContext binds the run-start stamp's callback values to this
// lifetime. The stamp reads the current installed generation, not the pin.
func (r *LoopRunStartRead) MutationContext(ctx context.Context) (context.Context, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.refs == 0 {
		return nil, errMetadataLogicalClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return r.owned.metadataOutputContext(metadataLogicalUnpinnedContext(ctx)), nil
}

func loopRunStartChild(ctx context.Context, record metadataLogicalNode, name string) (metadataLogicalNode, bool, error) {
	var result metadataLogicalNode
	found := false
	err := record.Children(ctx, func(key reflect.Value, node metadataLogicalNode) error {
		if key.String() == name {
			result, found = node, true
		}
		return nil
	})
	return result, found, err
}

// loopRunStartFirst visits the first record of field whose native ID equals id.
func loopRunStartFirst(ctx context.Context, source *metadataLogicalSource, field, id string, visit func(int, metadataLogicalNode) error) error {
	index, _, _ := metadataLogicalField(field)
	err := source.fields[index].Children(ctx, func(ordinal reflect.Value, record metadataLogicalNode) error {
		node, found, err := loopRunStartChild(ctx, record, "ID")
		if err != nil || !found {
			return err
		}
		match, err := node.StringEqual(ctx, id)
		if err != nil || !match {
			return err
		}
		if err := visit(int(ordinal.Int()), record); err != nil {
			return err
		}
		return errLoopRunStartMatched
	})
	if errors.Is(err, errLoopRunStartMatched) {
		return nil
	}
	return err
}

// BeginLoopRunStartRead selects from the same physical/default installed
// registry as WorkspaceMetadataFields. found is false for an unknown workspace;
// a missing loop leaves Data.Loops empty.
func (s *SQLiteStore) BeginLoopRunStartRead(ctx context.Context, workspace, loopID string) (_ *LoopRunStartRead, found bool, err error) {
	if !s.BoundedMetadataEnabled() {
		return nil, false, ErrBoundedMetadataCoverage
	}
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	installed, found, err := s.BeginInstalledMetadataRead(ctx, workspace)
	if err != nil || !found {
		return nil, found, err
	}
	defer installed.Close()
	source, err := installed.retainSource(ctx, installed.workspace)
	if err != nil {
		return nil, true, err
	}
	r := &LoopRunStartRead{refs: 1, pin: &ResourceRead{store: s, source: source}, owned: &ResourceRead{store: s}}
	defer func() {
		if err != nil {
			r.Close()
		}
	}()
	if err := r.pin.reserve(ctx, 64<<10); err != nil {
		return nil, true, err
	}
	index, _, _ := metadataLogicalField("workspace")
	value, err := r.pin.projectNode(ctx, source.fields[index], "URLKey")
	if err != nil {
		return nil, true, err
	}
	r.Data.Workspace = value.Interface().(domain.Workspace)
	index, _, _ = metadataLogicalField("workspaceSettings")
	value, err = r.pin.projectNode(ctx, source.fields[index], "FeatureFlags")
	if err != nil {
		return nil, true, err
	}
	r.Data.WorkspaceSettings = value.Interface().(domain.WorkspaceSettings)
	err = loopRunStartFirst(ctx, source, "loops", loopID, func(_ int, record metadataLogicalNode) error {
		value, err := largeRowRecordValue(ctx, r.owned, record)
		if err == nil {
			r.Data.Loops = []domain.Loop{value.Interface().(domain.Loop)}
		}
		return err
	})
	if err != nil {
		return nil, true, err
	}
	return r, true, ctx.Err()
}

// LoadEntity selects the run entity's first ID match with the fields its
// label reads, from the pinned generation. Issues are read separately.
func (r *LoopRunStartRead) LoadEntity(ctx context.Context, entityType, entityID string) error {
	field, names := "", ""
	switch entityType {
	case "project":
		field, names = "projects", "ID Name"
	case "initiative":
		field, names = "initiatives", "ID Name"
	case "release":
		field, names = "releases", "ID Name"
	case "team":
		field, names = "teams", "ID Name"
	case "cycle":
		field, names = "cycles", "ID Name Number"
	}
	if field == "" || entityID == "" {
		return ctx.Err()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.pin == nil {
		return errMetadataLogicalClosed
	}
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	index, _, _ := metadataLogicalField(field)
	target := reflect.ValueOf(&r.Data).Elem().Field(index)
	err := loopRunStartFirst(ctx, r.pin.source, field, entityID, func(_ int, record metadataLogicalNode) error {
		value, err := r.pin.projectNode(ctx, record, strings.Fields(names)...)
		if err == nil {
			target.Set(reflect.Append(reflect.MakeSlice(target.Type(), 0, 1), value))
		}
		return err
	})
	if err != nil {
		return err
	}
	return ctx.Err()
}

// loopRunStampInputs are the closed run-start callback's inputs: the
// workspace ID, the first loop with the ID (its ID, or the whole loop when a
// v1 may be created) and that loop's versions as ID/LoopID/Version headers
// in native order. Existing version bodies are never decoded.
type loopRunStampInputs struct {
	owner   *ResourceRead
	data    domain.Bootstrap
	before  domain.Bootstrap
	ordinal int
	once    sync.Once
	// nextRun: the loop.scheduled stamp, which reads only the loop's ID
	// and changes only its NextRunAt.
	nextRun bool
}

func (r *loopRunStampInputs) Close() {
	if r != nil {
		r.once.Do(func() {
			r.data, r.before = domain.Bootstrap{}, domain.Bootstrap{}
			r.owner.Close()
		})
	}
}

func (s *SQLiteStore) loadLoopRunStampInputs(ctx context.Context, source *metadataLogicalSource, loopID string, nextRun bool) (_ *loopRunStampInputs, err error) {
	r := &loopRunStampInputs{owner: &ResourceRead{store: s}, ordinal: -1, nextRun: nextRun}
	defer func() {
		if err != nil {
			r.Close()
		}
	}()
	if err := r.owner.reserve(ctx, 64<<10); err != nil {
		return nil, err
	}
	index, _, _ := metadataLogicalField("workspace")
	value, err := r.owner.projectNode(ctx, source.fields[index], "ID")
	if err != nil {
		return nil, err
	}
	r.data.Workspace = value.Interface().(domain.Workspace)
	var record metadataLogicalNode
	err = loopRunStartFirst(ctx, source, "loops", loopID, func(ordinal int, node metadataLogicalNode) error {
		value, err := r.owner.projectNode(ctx, node, "ID")
		if err == nil {
			r.ordinal, record = ordinal, node
			r.data.Loops = []domain.Loop{value.Interface().(domain.Loop)}
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	if r.ordinal >= 0 && !nextRun {
		index, _, _ = metadataLogicalField("loopVersions")
		err = source.fields[index].Children(ctx, func(_ reflect.Value, node metadataLogicalNode) error {
			parent, found, err := loopRunStartChild(ctx, node, "LoopID")
			if err != nil || !found {
				return err
			}
			match, err := parent.StringEqual(ctx, loopID)
			if err != nil || !match {
				return err
			}
			value, err := r.owner.projectNode(ctx, node, "ID", "LoopID", "Version")
			if err == nil {
				r.data.LoopVersions = append(r.data.LoopVersions, value.Interface().(domain.LoopVersion))
			}
			return err
		})
		if err != nil {
			return nil, err
		}
		if len(r.data.LoopVersions) == 0 {
			// Only a loop without versions is read whole: its v1 snapshots it.
			value, err := largeRowRecordValue(ctx, r.owner, record)
			if err != nil {
				return nil, err
			}
			r.data.Loops[0] = value.Interface().(domain.Loop)
		}
	}
	cost, err := metadataAggregateNativeCost(ctx, reflect.ValueOf(r.data), 0)
	if err != nil {
		return nil, err
	}
	// The callback's copy, its v1 and the before image. They share the
	// loop's large strings (ensureLoopVersion assigns them, the clone
	// copies string headers), which the decode above already admitted.
	for _, loop := range r.data.Loops {
		cost -= min(cost, largeRowSharedStrings(reflect.ValueOf(loop)))
	}
	if cost > (metadataWorkBytes-(64<<10))/3 {
		return nil, ErrMetadataWorkBudget
	}
	if err := r.owner.reserve(ctx, cost*3+64<<10); err != nil {
		return nil, err
	}
	r.before = cloneBootstrap(r.data)
	return r, ctx.Err()
}

// image checks the callback changed only the first loop's Version, VersionID
// and LastRunAt and at most appended the loop's v1, then patches the stored
// loop occurrence and appends the v1 after every existing version.
func (r *loopRunStampInputs) image(ctx context.Context, source *metadataLogicalSource, loopID string) (*metadataLogicalSource, error) {
	if r.ordinal < 0 || len(r.before.Loops) != 1 || len(r.data.Loops) != 1 {
		return nil, ErrBoundedMetadataCoverage
	}
	before, after := r.before.Loops[0], r.data.Loops[0]
	allowed := before
	if r.nextRun {
		allowed.NextRunAt = after.NextRunAt
	} else {
		allowed.Version, allowed.VersionID, allowed.LastRunAt = after.Version, after.VersionID, after.LastRunAt
	}
	if after.ID != loopID || !reflect.DeepEqual(allowed, after) {
		return nil, ErrBoundedMetadataCoverage
	}
	existing := r.before.LoopVersions
	versions := r.data.LoopVersions
	if len(versions) < len(existing) || len(versions) > len(existing)+1 || len(existing) != 0 && !reflect.DeepEqual(existing, versions[:len(existing)]) {
		return nil, ErrBoundedMetadataCoverage
	}
	appended := versions[len(existing):]
	if len(appended) == 1 && (len(existing) != 0 || appended[0].ID != loopID+"_v1" || appended[0].LoopID != loopID || appended[0].Version != 1) {
		return nil, ErrBoundedMetadataCoverage
	}
	unlisted, original := r.data, r.before
	unlisted.Loops, unlisted.LoopVersions, original.Loops, original.LoopVersions = nil, nil, nil, nil
	if !reflect.DeepEqual(unlisted, original) {
		return nil, ErrBoundedMetadataCoverage
	}
	if r.nextRun && len(appended) != 0 {
		return nil, ErrBoundedMetadataCoverage
	}
	changed := map[string]reflect.Value{"LastRunAt": reflect.ValueOf(after.LastRunAt)}
	var nextRunAt *time.Time
	if r.nextRun {
		// A schedule's next run is in the loop's named time zone. The row,
		// event and realtime bytes carry only its RFC 3339 offset, so the
		// image keeps the instant as a load decodes it (a fixed zone).
		nextRunAt = after.NextRunAt
		if nextRunAt != nil {
			raw, err := json.Marshal(*nextRunAt)
			if err != nil {
				return nil, err
			}
			var loaded time.Time
			if err := json.Unmarshal(raw, &loaded); err != nil {
				return nil, err
			}
			nextRunAt = &loaded
		}
		changed = map[string]reflect.Value{"NextRunAt": reflect.ValueOf(nextRunAt)}
	}
	if before.Version != after.Version {
		changed["Version"] = reflect.ValueOf(after.Version)
	}
	if before.VersionID != after.VersionID {
		changed["VersionID"] = reflect.ValueOf(after.VersionID)
	}
	frames, err := newMetadataLogicalBuilder(metadataLogicalSourceScratchContext(ctx, source))
	if err != nil {
		return nil, err
	}
	defer frames.Close()
	for _, storage := range source.stores {
		if err := frames.inheritLocations(storage); err != nil {
			return nil, err
		}
	}
	if err := frames.inheritTimestampInputs(ctx); err != nil {
		return nil, err
	}
	if err := frames.auditJSONLocations(reflect.ValueOf(nextRunAt)); err != nil {
		return nil, err
	}
	frame := func(node metadataAggregateNode) (metadataLogicalNode, error) {
		value, err := frames.writeAggregateNode(node, 0)
		if err == nil && (value.blocked || value.unavailable) {
			err = ErrBoundedMetadataCoverage
		}
		return value, err
	}
	loopIndex, _, _ := metadataLogicalField("loops")
	loopBase := source.fields[loopIndex]
	loops := legacyDiscussionSlicePatch{base: loopBase, shape: loopBase, replacements: map[int]metadataLogicalNode{}}
	err = loopBase.Children(ctx, func(key reflect.Value, node metadataLogicalNode) error {
		if int(key.Int()) != r.ordinal {
			return nil
		}
		value, err := frame(legacyDiscussionNativeDelta{base: node, changed: changed})
		if err == nil {
			loops.replacements[r.ordinal] = value
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	if len(loops.replacements) != 1 {
		return nil, ErrBoundedMetadataCoverage
	}
	versionIndex, _, _ := metadataLogicalField("loopVersions")
	versionBase := source.fields[versionIndex]
	var versionPatch metadataAggregateNode
	if len(appended) == 1 {
		shape, err := legacyDiscussionShape(frames, versionBase.typ, false)
		if err != nil {
			return nil, err
		}
		value, err := frame(legacyDiscussionNativeRecord{value: reflect.ValueOf(appended[0])})
		if err != nil {
			return nil, err
		}
		versionPatch = legacyDiscussionSlicePatch{base: versionBase, shape: shape, appended: []metadataLogicalNode{value}}
	}
	return source.Transform(ctx, func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
		switch {
		case name == "loops":
			return loops, true, nil
		case name == "loopVersions" && versionPatch != nil:
			return versionPatch, true, nil
		}
		return metadataAggregateStoredNode{value: node}, false, nil
	})
}

// MutateLoopRunStart is the closed loop.run_started transaction of a loop run
// start: the original MutateWorkspace with its loops/loopVersions callback
// when bounded metadata is off; otherwise the callback runs on the selected
// stamp inputs and its result is written as one logical metadata image,
// with the original event, webhook and realtime publication.
func (s *SQLiteStore) MutateLoopRunStart(ctx context.Context, workspace, loopID string, payload any, mutate func(*domain.Bootstrap) error) error {
	if !s.BoundedMetadataEnabled() {
		return s.MutateWorkspace(WithMetadataFields(ctx, "loops", "loopVersions"), workspace, "loop.run_started", loopID, payload, mutate)
	}
	if !metadataOutputOwnerPresent(ctx) {
		return ErrBoundedMetadataCoverage
	}
	return s.mutateLoopStamp(ctx, workspace, "loop.run_started", loopID, payload, mutate, false)
}

// MutateLoopNextRun is the loop.scheduled stamp of the schedule runner: the
// original MutateWorkspace with its loops callback when bounded metadata is
// off; otherwise the callback receives the first loop with the ID (its ID
// only) and may change only its NextRunAt, written as one logical metadata
// image (a large loop row through the large-row kernels) with the original
// event, webhook and realtime publication. The callback's inputs end with
// the call.
func (s *SQLiteStore) MutateLoopNextRun(ctx context.Context, workspace, loopID string, mutate func(*domain.Bootstrap) error) error {
	if !s.BoundedMetadataEnabled() {
		return s.MutateWorkspace(WithMetadataFields(ctx, "loops"), workspace, "loop.scheduled", loopID, nil, mutate)
	}
	var closes []func()
	var mu sync.Mutex
	defer func() {
		mu.Lock()
		owned := closes
		closes = nil
		mu.Unlock()
		for _, close := range owned {
			close()
		}
	}()
	ctx = WithMetadataOutputOwner(ctx, func(close func()) bool {
		mu.Lock()
		defer mu.Unlock()
		closes = append(closes, close)
		return true
	})
	return s.mutateLoopStamp(ctx, workspace, "loop.scheduled", loopID, nil, mutate, true)
}

func (s *SQLiteStore) mutateLoopStamp(ctx context.Context, workspace, eventType, loopID string, payload any, mutate func(*domain.Bootstrap) error, nextRun bool) error {
	if mutate == nil || !metadataOutputOwnerPresent(ctx) || UsesIssueRecordMutations(ctx) {
		return ErrBoundedMetadataCoverage
	}
	if _, actor := actorFromContext(ctx); actor {
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
	ctx, release, err := s.tryAdmitAdditionalMetadataWork(ctx, metadataTimestampInputBytes)
	if err != nil {
		return err
	}
	defer release()
	ctx, finish, err := s.startMetadataTimestampInputs(ctx)
	if err != nil {
		return err
	}
	defer finish()
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
			if err := s.reloadResidentWorkspace(context.WithValue(ctx, metadataWorkLeaseKey{}, nil), workspace); err != nil {
				return fmt.Errorf("reload workspace before mutation: %w", err)
			}
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
		inputs, err := s.loadLoopRunStampInputs(ctx, stored, loopID, nextRun)
		if err != nil {
			return err
		}
		if !registerMetadataOutputClose(ctx, inputs.Close) {
			inputs.Close()
			return ErrBoundedMetadataCoverage
		}
		if err := mutate(&inputs.data); err != nil {
			return err
		}
		target, err := inputs.image(ctx, stored, loopID)
		if err != nil {
			return err
		}
		defer target.Release()
		payloadRaw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		eventPublication, err := s.prepareMetadataAggregatePublication(ctx, metadataAggregateImage{Source: stored},
			metadataAggregateImage{Source: target}, loopID, webhook, payloadRaw, eventType)
		if err != nil {
			return err
		}
		eventOwner = &ResourceRead{store: s}
		eventOwner.release = append(eventOwner.release, eventPublication.Close)
		previous := eventPublication.PreviousValues
		nextEvent := domain.DomainEvent{ID: idgen.New("evt_"), Type: eventType, AggregateID: loopID,
			Payload: payloadRaw, PreviousValues: previous, CreatedAt: time.Now().UTC()}
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
		if stamp.workspaceID == "" || stamp.workspaceID != inputs.data.Workspace.ID || lockedID != stamp.workspaceID {
			return ErrResourceAccessChanged
		}
		if s.metadataResidency.requireCoverage {
			if err := checkRecordJournalCoverage(ctx, tx, workspace); err != nil {
				return err
			}
		}
		persistCtx := context.WithValue(ctx, legacyMetadataExtraFieldsKey{}, []string{"loops", "loopVersions"})
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
		// The original full writer also records the account's last workspace.
		s.mu.RLock()
		viewer := s.viewer
		s.mu.RUnlock()
		budget := &boundedDiscussionAttempt{owner: inputs.owner}
		if err := budget.reserveValue(ctx, viewer); err != nil {
			return err
		}
		viewerRaw, _ := json.Marshal(viewer)
		if len(viewerRaw) == 0 || string(viewerRaw) == "{}" {
			index, _, _ := metadataLogicalField("viewer")
			value, err := inputs.owner.node(ctx, merged.fields[index], false)
			if err != nil {
				return err
			}
			viewerRaw, _ = json.Marshal(value.Interface())
		}
		if err := writeAccountMetadata(ctx, tx, workspace, viewerRaw); err != nil {
			return err
		}
		storedPrevious, err := storedEventPreviousValues(ctx, tx, workspace, eventType, loopID, previous)
		if err != nil {
			return err
		}
		if _, err := insertDomainEvent(ctx, tx, domainEventRow{ID: nextEvent.ID, Type: eventType, AggregateID: loopID,
			Payload: payloadRaw, PreviousValues: storedPrevious, CreatedAt: nextEvent.CreatedAt.Format(time.RFC3339Nano),
			WorkspaceID: stamp.workspaceID, WorkspaceKey: workspace}); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		written = true
		if err := publication.Install(); err != nil {
			return err
		}
		event, realtime = nextEvent, eventPublication.RealtimePayload
		return nil
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
	s.publishMutation(ctx, workspace, event, realtime)
	return nil
}
