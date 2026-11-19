package store

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"flow/api/internal/domain"
)

// Ordinary stores retain a zero-value local mutex. A private cold facade
// delegates only mutation critical sections to its bounded owner.
type coldWriteMutex struct {
	local  sync.Mutex
	shared *coldWriteMutex
	// A default COLD legacy write (coldLegacyMutation) brings the facade
	// up to the parent's latest generation right after it takes the shared
	// lock and publishes its result to the parent right before it returns
	// the lock, so no other writer runs between its base, its commit and
	// the parent's image.
	onLock, onUnlock func()
}

func (m *coldWriteMutex) Lock() {
	if m.shared != nil {
		if !m.shared.TryLock() {
			started := time.Now()
			m.shared.Lock()
			observeWriteLockWait(&coldWriterLockWaits, started)
		}
		m.locked()
		return
	}
	if m.local.TryLock() {
		return
	}
	started := time.Now()
	m.local.Lock()
	observeWriteLockWait(&writerLockWaits, started)
}

// writeLockWaitStats counts the writers that found a store's write lock held
// and how long they waited (load measurement). writerLockWaits covers every
// writer of a live store; coldWriterLockWaits the COLD facades among them.
type writeLockWaitStats struct{ waits, nanos, longest atomic.Int64 }

var writerLockWaits, coldWriterLockWaits writeLockWaitStats

func observeWriteLockWait(stats *writeLockWaitStats, started time.Time) {
	waited := int64(time.Since(started))
	stats.waits.Add(1)
	stats.nanos.Add(waited)
	storeMaxInt64(&stats.longest, waited)
}

// locked runs onLock; if it panics the shared lock is returned first, so a
// panicking COLD write never leaves the live store's writers locked out.
func (m *coldWriteMutex) locked() {
	if m.onLock == nil {
		return
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			m.shared.Unlock()
			panic(recovered)
		}
	}()
	m.onLock()
}

func (m *coldWriteMutex) Unlock() {
	if m.shared != nil {
		// The shared lock is returned even when publication panics.
		defer m.shared.Unlock()
		if m.onUnlock != nil {
			m.onUnlock()
		}
		return
	}
	m.local.Unlock()
}

func (m *coldWriteMutex) TryLock() bool {
	if m.shared != nil {
		if !m.shared.TryLock() {
			return false
		}
		m.locked()
		return true
	}
	return m.local.TryLock()
}

type coldFullWrite struct {
	// legacy marks a default COLD legacy write (coldLegacyMutation).
	legacy    bool
	parent    *SQLiteStore
	requested string
	workspace string
	source    *metadataLogicalSource
	prepared  *coldFullPublication
	decoded   *metadataLogicalBuilder
}

type coldFullPublication struct {
	workspace string
	source    *metadataLogicalSource
	policy    domain.Bootstrap
	costs     map[string]int
	total     int
	capture   *metadataCommitStamp
}

func (w *coldFullWrite) close() {
	if w.prepared != nil && w.prepared.source != nil {
		w.prepared.source.Release()
	}
	if w.source != nil {
		w.source.Release()
	}
	if w.decoded != nil {
		w.decoded.Close()
	}
	w.decoded = nil
	w.prepared, w.source = nil, nil
}

// withColdFullMutation covers only the original full metadata writer. It
// deliberately does not redirect optimized writers or content transactions.
// Their precommit and notification-signal publication boundaries differ.
func (s *SQLiteStore) withColdFullMutation(ctx context.Context, workspace, event string, payload any, mutate func(*domain.Bootstrap) (string, error)) error {
	if s.metadataResidency == nil {
		return s.mutateFull(ctx, workspace, event, payload, mutate)
	}
	_, scoped := mutationScopeFromContext(ctx)
	if mutate == nil || scoped || UsesIssueRecordMutations(ctx) || projectRecordSelectionEnabled(ctx) ||
		!metadataOnlyMutation(event, payload) || userSettingsRecordMutation(event, payload) ||
		standaloneFavoriteMutation(event, payload) || metadataTeamMutation(event, payload) ||
		event == "loop.output_contract" || event == "workspace_preferences.updated" && featureFlagsOnly(payload) {
		return fmt.Errorf("%w: cold mutation requires original full metadata dispatch", ErrBoundedMetadataCoverage)
	}
	return s.withColdMutationStore(ctx, workspace, func(ctx context.Context, facade *SQLiteStore) error {
		return facade.mutateFull(ctx, workspace, event, payload, mutate)
	})
}

// withColdMutation retains the original public aggregate dispatcher, including
// audit collection. Only writer paths with precommit publication hooks qualify.
func (s *SQLiteStore) withColdMutation(ctx context.Context, workspace, event string, payload any, mutate func(*domain.Bootstrap) (string, error)) error {
	if s.metadataResidency == nil {
		return s.MutateWorkspaceWithAggregate(ctx, workspace, event, payload, mutate)
	}
	_, scoped := mutationScopeFromContext(ctx)
	loopContract := event == "loop.output_contract" && !fullMutationsForced.Load()
	if mutate == nil || scoped || UsesIssueRecordMutations(ctx) || projectRecordSelectionEnabled(ctx) ||
		metadataRecordVerificationEnabled(ctx) || !metadataOnlyMutation(event, payload) && !loopContract && !metadataTeamMutation(event, payload) {
		return fmt.Errorf("%w: cold mutation writer is not integrated", ErrBoundedMetadataCoverage)
	}
	return s.withColdMutationStore(ctx, workspace, func(ctx context.Context, facade *SQLiteStore) error {
		return facade.MutateWorkspaceWithAggregate(ctx, workspace, event, payload, mutate)
	})
}

func (s *SQLiteStore) withColdMutationStore(ctx context.Context, workspace string, run func(context.Context, *SQLiteStore) error) error {
	if s.metadataResidency == nil {
		return run(ctx, s)
	}
	return s.withColdMetadataRead(ctx, func(ctx context.Context, facade *SQLiteStore) error {
		if facade.coldWrite != nil {
			return ErrMetadataRecordQuery
		}
		state := &coldFullWrite{parent: s, requested: workspace}
		facade.coldWrite, facade.writeMu.shared = state, &s.writeMu
		defer func() {
			state.close()
			facade.coldWrite, facade.writeMu.shared = nil, nil
		}()
		return run(ctx, facade)
	})
}

// The parent write lock is already held; refresh explicitly before the
// original callback chooses its base. Errors never hide inside mutex methods.
func (s *SQLiteStore) refreshColdFullWriteLocked(ctx context.Context) (string, error) {
	state := s.coldWrite
	// Original conflict refresh changes only the facade's conflicting rows.
	// Preserve that retry base while the parent's generation is unchanged.
	if state.source != nil {
		parent := state.parent
		parent.mu.RLock()
		selected := state.requested
		if selected == "" {
			selected = parent.lastWorkspaceKey
		}
		parent.metadataResidency.mu.Lock()
		unchanged := selected == state.workspace && parent.metadataResidency.logical[selected] == state.source
		parent.metadataResidency.mu.Unlock()
		parent.mu.RUnlock()
		if unchanged {
			if state.prepared != nil && state.prepared.source != nil {
				state.prepared.source.Release()
			}
			state.prepared = nil
			if state.decoded != nil {
				state.decoded.Close()
				state.decoded = nil
			}
			return selected, nil
		}
	}
	state.close()
	if err := s.refreshColdMetadataRead(ctx, state.requested); err != nil {
		return "", err
	}
	workspace := state.requested
	if workspace == "" {
		workspace = s.lastWorkspaceKey
	}
	read := ctx.Value(coldMetadataReadKey{}).(*coldMetadataRead)
	read.mu.Lock()
	source := read.sources[workspace]
	if source == nil {
		read.mu.Unlock()
		return "", fmt.Errorf("workspace %q: not found", workspace)
	}
	err := source.Retain()
	read.mu.Unlock()
	if err != nil {
		return "", err
	}
	state.source = source
	state.workspace = workspace
	stamp := source.Stamp()
	s.mu.Lock()
	if stamp.revision >= 0 {
		s.setCopyRevisionLocked(workspace, stamp)
	} else {
		s.forgetCopyRevisionLocked(workspace)
	}
	s.mu.Unlock()
	return workspace, nil
}

// Only the fresh standard JSON decode is audited, before native import inputs
// are restored. The operation owns these bounded location receipts until retry
// or publication; the installed graph itself is never treated as JSON input.
func (s *SQLiteStore) setColdMetadataRecords(ctx context.Context, data *domain.Bootstrap, records map[metadataRecordKey][]byte) error {
	state := s.coldWrite
	if state == nil || state.source == nil {
		return ErrMetadataRecordQuery
	}
	if state.decoded == nil {
		var err error
		state.decoded, err = newMetadataLogicalBuilder(metadataLogicalSourceScratchContext(ctx, state.source))
		if err != nil {
			return err
		}
	}
	return setMetadataRecordsDecoded(data, records, state.decoded.auditJSONLocations)
}

func (s *SQLiteStore) prepareColdFullWrite(ctx context.Context, tx *sqlTx, workspace string, data domain.Bootstrap) (err error) {
	state := s.coldWrite
	if state.legacy && state.source == nil {
		// A default COLD legacy writer that never selected an installed
		// base (anything but the integrated writers) publishes through the
		// parent's reload (coldLegacyMutation).
		return nil
	}
	if state.prepared != nil || state.source == nil {
		return ErrMetadataRecordQuery
	}
	// A default COLD legacy write persisted its issue and content records
	// in this transaction, exactly as legacy; only the metadata image is
	// published (collectionMetadata below drops the record collections).
	if !coldLegacyWrite(ctx) && (data.Issues != nil || data.Activities != nil || data.Comments != nil ||
		data.Notifications != nil || data.NotificationDeliveries != nil) {
		return fmt.Errorf("%w: cold metadata callback changed external collections", ErrBoundedMetadataCoverage)
	}
	builder, err := newMetadataLogicalBuilder(metadataLogicalSourceScratchContext(ctx, state.source))
	if err != nil {
		return err
	}
	defer builder.Close()
	for _, storage := range state.source.stores {
		if err := builder.inheritLocations(storage); err != nil {
			return err
		}
	}
	if state.decoded != nil {
		if err := builder.inheritLocations(state.decoded.storage); err != nil {
			return err
		}
	}
	if err := builder.inheritTimestampInputs(ctx); err != nil {
		return err
	}
	data = collectionMetadata(data)
	value := reflect.ValueOf(data)
	for index := 0; index < value.NumField(); index++ {
		field := value.Type().Field(index)
		part := value.Field(index)
		if field.Tag.Get("json") == "-" && field.Name != "NextIssueNumber" {
			part = reflect.Zero(part.Type())
		}
		node, err := builder.writeValue(part, 0, false)
		if err != nil {
			return err
		}
		if node.unavailable {
			return fmt.Errorf("%w: cold postimage field %s", ErrBoundedMetadataCoverage, field.Name)
		}
		builder.source.fields[index] = node
	}
	source, err := builder.Finish(copyStamp{workspaceID: data.Workspace.ID, revision: -1})
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			source.Release()
		}
	}()
	parent := state.parent
	policy, cost, err := parent.metadataLogicalPolicy(ctx, source)
	if err != nil {
		return err
	}
	r := parent.metadataResidency
	r.mu.Lock()
	costs := make(map[string]int, len(r.policy))
	total := cost
	for key, bytes := range r.policy {
		if key != workspace {
			costs[key] = bytes
			total += bytes
		}
	}
	limit := r.policyLimit
	r.mu.Unlock()
	if total > limit {
		return ErrMetadataWorkBudget
	}
	costs[workspace] = cost
	state.prepared = &coldFullPublication{
		workspace: workspace, source: source, policy: policy, costs: costs,
		total: total, capture: tx.captureMetadataCommitStamp(workspace),
	}
	return nil
}

// Preparation completed before commit and the shared write lock excludes
// competing metadata installs. This transfers ownership without fallible work.
func (s *SQLiteStore) installColdFullWriteLocked(workspace string, write *metadataWrite, current copyRevision, currentOK bool) {
	state := s.coldWrite
	prepared := state.prepared
	if prepared == nil || prepared.workspace != workspace {
		panic("cold full mutation has no prepared publication")
	}
	stamp, ready := prepared.capture.result()
	if !ready {
		panic("cold full mutation installed before commit")
	}
	sourceStamp := copyStamp{workspaceID: prepared.policy.Workspace.ID, revision: -1}
	if write != nil && currentOK && write.workspace == workspace && current.epoch == write.base.epoch {
		write.mu.Lock()
		if write.exact && write.base.workspaceID == stamp.workspaceID && write.next == stamp.revision {
			sourceStamp = stamp
		}
		write.mu.Unlock()
	}
	prepared.source.stamp = sourceStamp
	parent := state.parent
	parent.mu.Lock()
	r := parent.metadataResidency
	r.mu.Lock()
	previous := r.logical[workspace]
	parent.workspaces[workspace] = prepared.policy
	parent.lastWorkspaceKey = workspace
	parent.forgetCopyRevisionLocked(workspace)
	r.logical[workspace] = prepared.source
	r.policy, r.policyBytes = prepared.costs, prepared.total
	r.stamps[workspace], r.applied[workspace] = stamp, 0
	prepared.source = nil
	r.mu.Unlock()
	parent.mu.Unlock()
	if previous != nil {
		previous.Release()
	}
	state.prepared = nil
}
