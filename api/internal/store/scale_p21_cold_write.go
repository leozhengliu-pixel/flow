package store

import (
	"context"
	"fmt"
	"reflect"
	"sync"

	"flow/api/internal/domain"
)

// Ordinary stores retain a zero-value local mutex. A private cold facade
// delegates only mutation critical sections to its bounded owner.
type coldWriteMutex struct {
	local  sync.Mutex
	shared *coldWriteMutex
}

func (m *coldWriteMutex) Lock() {
	if m.shared != nil {
		m.shared.Lock()
		return
	}
	m.local.Lock()
}

func (m *coldWriteMutex) Unlock() {
	if m.shared != nil {
		m.shared.Unlock()
		return
	}
	m.local.Unlock()
}

func (m *coldWriteMutex) TryLock() bool {
	if m.shared != nil {
		return m.shared.TryLock()
	}
	return m.local.TryLock()
}

type coldFullWrite struct {
	parent    *SQLiteStore
	requested string
	workspace string
	source    *metadataLogicalSource
	prepared  *coldFullPublication
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
		return facade.mutateFull(ctx, workspace, event, payload, mutate)
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

func (s *SQLiteStore) prepareColdFullWrite(ctx context.Context, tx *sqlTx, workspace string, data domain.Bootstrap) (err error) {
	state := s.coldWrite
	if state.prepared != nil || state.source == nil {
		return ErrMetadataRecordQuery
	}
	if data.Issues != nil || data.Activities != nil || data.Comments != nil ||
		data.Notifications != nil || data.NotificationDeliveries != nil {
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
	if err := builder.inheritTimestampInputs(ctx); err != nil {
		return err
	}
	data = collectionMetadata(data)
	value := reflect.ValueOf(data)
	for index := 0; index < value.NumField(); index++ {
		field := value.Type().Field(index)
		part := value.Field(index)
		if field.Tag.Get("json") == "-" {
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
