package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// PulseSummaryBatchMaxCandidates matches the scheduler's existing
// pulseMaxUsersPerTick. One whole batch commits or rolls back together.
const PulseSummaryBatchMaxCandidates = 1000

type PulseSummaryCandidate struct {
	UserID       string
	Schedule     string
	End          time.Time
	Notification *domain.Notification // nil still advances this recipient's cursor
}

// PulseSummaryDispatchInput contains only the current Pulse delivery policy.
// Categories retains nil/nonnull shape and the exact optional "pulse" key.
// The notification is read-only. prepare may only return delivery decisions.
type PulseSummaryDispatchInput struct {
	Notification   domain.Notification
	Preferences    domain.NotificationPreferences
	HasPreferences bool
	PushEnabled    bool
}

type PulseSummaryDispatch struct {
	Send       bool
	Deliveries []domain.NotificationDelivery
}

type pulseSummaryWork struct {
	store    *SQLiteStore
	ctx      context.Context
	releases []func()
}

func (w *pulseSummaryWork) close() {
	for _, release := range w.releases {
		release()
	}
	w.releases = nil
}

func (w *pulseSummaryWork) reserve(cost uint64) error {
	if cost == 0 {
		return nil
	}
	if cost > metadataWorkBytes {
		return ErrMetadataWorkBudget
	}
	_, release, err := w.store.tryAdmitAdditionalMetadataWork(w.ctx, int(cost))
	if err == nil {
		w.releases = append(w.releases, release)
	}
	return err
}

func (w *pulseSummaryWork) native(node metadataLogicalNode, copies uint64) (reflect.Value, error) {
	if node.cost > (metadataWorkBytes-256)/copies {
		return reflect.Value{}, ErrMetadataWorkBudget
	}
	if err := w.reserve(node.cost*copies + 256); err != nil {
		return reflect.Value{}, err
	}
	return node.Value(w.ctx, int(node.cost))
}

func pulseSummaryField(source *metadataLogicalSource, name string) metadataLogicalNode {
	index, _, _ := metadataLogicalField(name)
	return source.fields[index]
}

// CommitPulseSummaries is a trusted bounded scheduler writer. The caller keeps
// its original MutateWorkspace callback off mode. Input End values come from
// pulseWindow and are UTC. No issue or existing content records are hydrated.
func (s *SQLiteStore) CommitPulseSummaries(ctx context.Context, workspace string, candidates []PulseSummaryCandidate, prepare func(PulseSummaryDispatchInput) (PulseSummaryDispatch, error)) error {
	if !s.BoundedMetadataEnabled() || !RecordHydrationEnabled() {
		return ErrBoundedMetadataCoverage
	}
	if len(candidates) > PulseSummaryBatchMaxCandidates {
		return ErrMetadataWorkBudget
	}
	if len(candidates) == 0 {
		return ctx.Err()
	}
	if prepare == nil {
		return ErrMetadataRecordQuery
	}
	ctx = context.WithValue(metadataLogicalUnpinnedContext(ctx), metadataWorkLeaseKey{}, nil)
	work, release, err := s.tryAdmitAdditionalMetadataWork(ctx, 2<<20)
	if err != nil {
		return err
	}
	defer release()
	ctx = work
	_, releaseInput, err := s.reserveMetadataAggregatePayload(ctx, candidates)
	if err != nil {
		return err
	}
	defer releaseInput()
	needed, maxID := make(map[string]int, len(candidates)), 0
	for index, candidate := range candidates {
		_, duplicate := needed[candidate.UserID]
		if candidate.UserID == "" || duplicate || candidate.End.IsZero() || candidate.End.Location() != time.UTC ||
			candidate.Schedule != "daily" && candidate.Schedule != "weekly" {
			return ErrMetadataRecordQuery
		}
		needed[candidate.UserID], maxID = index, max(maxID, len(candidate.UserID))
		if notification := candidate.Notification; notification != nil {
			if notification.ID != "pulse_"+candidate.UserID+"_"+strconv.FormatInt(candidate.End.Unix(), 10) ||
				notification.RecipientID != candidate.UserID || notification.Type != "pulseSummary" ||
				notification.Category != "pulse" || notification.SourceType != "pulse" || notification.IssueID != "" {
				return ErrMetadataRecordQuery
			}
		}
	}
	if workspace == "" {
		s.mu.RLock()
		workspace = s.lastWorkspaceKey
		s.mu.RUnlock()
	}
	scope := MetadataRecordScope{Write: []RecordKey{{Kind: "settings", ID: "pulseDeliveryCursors"}}}
	ctx = WithMetadataRecords(ctx, scope)
	ctx = context.WithValue(ctx, scopedMetadataWriteKey{}, true)
	var event domain.DomainEvent
	var realtime json.RawMessage
	var releaseEvent func()
	defer func() {
		if releaseEvent != nil {
			releaseEvent()
		}
	}()
	ctx, collector, _ := collectMutationAudit(ctx, workspace, func(*domain.Bootstrap) (string, error) { return "", nil })
	apply := func(ctx context.Context) error {
		collector.pending = nil
		if releaseEvent != nil {
			releaseEvent()
			releaseEvent = nil
		}
		event, realtime = domain.DomainEvent{}, nil
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		if s.coordinator != nil {
			if err := s.reloadResidentWorkspace(ctx, workspace); err != nil {
				return err
			}
		}
		source, err := s.retainMetadataLogicalSource(ctx, workspace)
		if err != nil {
			return err
		}
		defer source.Release()
		owned := &pulseSummaryWork{store: s, ctx: ctx}
		defer owned.close()
		// Four channels are the complete output of enqueueNotificationDeliveries.
		if err := owned.reserve(uint64(len(candidates)) * (8 << 10)); err != nil {
			return err
		}
		enabled, workspaceSchedule, err := owned.policy(source)
		if err != nil {
			return err
		}
		if !enabled {
			return ErrNoMutation
		}
		tx, err := s.db.BeginTx(ctx, s.recordTxOptions())
		if err != nil {
			return err
		}
		defer tx.Rollback()
		lock := ""
		if s.dialect != "sqlite" {
			lock = " FOR UPDATE"
		}
		var root []byte
		if err := tx.QueryRowContext(ctx, `SELECT CASE WHEN LENGTH(data)<=? THEN data ELSE NULL END FROM workspace_states WHERE workspace_key=?`+lock, MetadataRecordPageMaxBytes, workspace).Scan(&root); err != nil {
			return err
		}
		if root == nil {
			return ErrMetadataPageBudget
		}
		if _, _, err := residentMetadataRoot(root); err != nil {
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
		if source.Stamp().workspaceID != baseStamp.workspaceID {
			return ErrResourceAccessChanged
		}
		exactSource := source.Stamp() == baseStamp
		if s.coordinator != nil && !exactSource {
			return ErrConcurrentWrite
		}
		identityCtx := WithMetadataRecords(ctx, MetadataRecordScope{})
		storageBefore, err := s.decodeWorkspaceMetadata(identityCtx, workspace, root, tx)
		if err != nil {
			return err
		}
		if storageBefore.Workspace.ID != baseStamp.workspaceID {
			return ErrResourceAccessChanged
		}
		before, nativeRaw, nativeFound, err := owned.cursor(source)
		if err != nil {
			return err
		}
		canonicalRaw, canonicalFound, err := owned.canonicalCursor(tx, workspace, lock)
		if err != nil {
			return err
		}
		if nativeFound != canonicalFound || nativeFound && !storedRecordEqual(tx, newIssueReferences(domain.Bootstrap{}), "settings", canonicalRaw, nativeRaw) {
			// A peer's cursor advance is rechecked by rerunning the complete
			// batch, rather than publishing an already delivered summary.
			return &metadataConflictError{keys: []metadataRecordKey{{"settings", "pulseDeliveryCursors"}}}
		}
		current := map[string]time.Time{}
		_ = json.Unmarshal(nativeRaw, &current)
		if current == nil {
			current = map[string]time.Time{}
		}
		schedules, preferences, push, err := owned.recipients(source, candidates, needed, maxID)
		if err != nil {
			return err
		}
		next := domain.Bootstrap{Settings: map[string]any{}}
		changed := false
		for _, candidate := range candidates {
			if !current[candidate.UserID].Before(candidate.End) {
				continue
			}
			schedule := schedules[candidate.UserID]
			if schedule == "" || schedule == "default" {
				schedule = workspaceSchedule
			}
			if schedule != candidate.Schedule {
				continue
			}
			if candidate.Notification != nil {
				prefs, hasPrefs := preferences[candidate.UserID]
				dispatch, err := prepare(PulseSummaryDispatchInput{
					Notification: *candidate.Notification, Preferences: prefs, HasPreferences: hasPrefs, PushEnabled: push[candidate.UserID],
				})
				if err != nil {
					return err
				}
				if err := pulseSummaryValidateDispatch(candidate, dispatch); err != nil {
					return err
				}
				if dispatch.Send {
					encoding, err := metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(dispatch.Deliveries), 0)
					if err != nil {
						return err
					}
					if err := owned.reserve(uint64(encoding)); err != nil {
						return err
					}
					next.Notifications = append(next.Notifications, *candidate.Notification)
					next.NotificationDeliveries = append(next.NotificationDeliveries, dispatch.Deliveries...)
				}
			}
			current[candidate.UserID] = candidate.End
			changed = true
		}
		if !changed {
			return ErrNoMutation
		}
		next.Settings["pulseDeliveryCursors"] = current
		for _, value := range []any{before, next.Settings} {
			encoding, err := metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(value), 0)
			if err != nil {
				return err
			}
			if err := owned.reserve(uint64(encoding)); err != nil {
				return err
			}
		}
		changes, _, err := scaleP16RecordChanges(before, next)
		if err != nil {
			return err
		}
		if !nativeFound {
			if err := ensureOAuthMetadataShape(ctx, tx, workspace, "settings", "map", lock); err != nil {
				return err
			}
		}
		_, releaseEvent, err = s.reserveMetadataAggregatePayload(ctx, nil)
		if err != nil {
			return err
		}
		event = domain.DomainEvent{ID: idgen.New("evt_"), Type: "pulse.summary_scheduled", Payload: json.RawMessage("null"), CreatedAt: time.Now().UTC()}
		realtime = enrichRealtimePayload(event.Payload, nil, event.Type)
		complete := storageBefore
		complete.Notifications, complete.NotificationDeliveries = next.Notifications, next.NotificationDeliveries
		if err := s.persistWorkspaceTx(ctx, tx, workspace, &storageBefore, &complete, nil, &event, nil); err != nil {
			return err
		}
		if err := scaleP16WriteChanges(ctx, tx, workspace, newIssueReferences(domain.Bootstrap{}), changes); err != nil {
			return err
		}
		value, present := next.Settings["pulseDeliveryCursors"]
		for _, change := range changes {
			if change.merged || change.skipped {
				present = !change.skipped && !change.drop
				if present {
					if err := json.Unmarshal(change.raw, &value); err != nil {
						return err
					}
				}
			}
		}
		candidate, err := pulseSummaryForkCursor(ctx, source, value, present)
		if err != nil {
			return err
		}
		defer func() {
			if candidate != nil {
				candidate.Release()
			}
		}()
		// This is the only fallible ownership check. The candidate stays
		// private until installation, so stamping it after commit cannot fail.
		if err := candidate.setStamp(baseStamp); err != nil {
			return err
		}
		capture := tx.captureMetadataCommitStampFrom(workspace, baseStamp)
		if err := tx.Commit(); err != nil {
			return err
		}
		finalStamp := capture.value // Commit made every registered capture ready.
		if !exactSource {
			finalStamp.revision = -1
		}
		candidate.mu.Lock()
		candidate.stamp = finalStamp
		candidate.mu.Unlock()
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
	}, s.refreshPulseSummaryCursor(ctx, workspace))
	if errors.Is(err, ErrNoMutation) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(collector.pending) != 0 {
		return fmt.Errorf("Pulse summary mutation left uncommitted audit entries")
	}
	s.dropMetadataCache(ctx, workspace)
	release()
	s.publishMutation(context.WithValue(ctx, metadataWorkLeaseKey{}, nil), workspace, event, realtime)
	return nil
}

func pulseSummaryValidateDispatch(candidate PulseSummaryCandidate, dispatch PulseSummaryDispatch) error {
	if len(dispatch.Deliveries) > 4 || !dispatch.Send && len(dispatch.Deliveries) != 0 {
		return ErrMetadataRecordQuery
	}
	seen := map[string]bool{}
	for _, delivery := range dispatch.Deliveries {
		if candidate.Notification == nil || delivery.NotificationID != candidate.Notification.ID || delivery.RecipientID != candidate.UserID ||
			delivery.ID != "delivery_"+candidate.Notification.ID+"_"+delivery.Channel || seen[delivery.Channel] ||
			delivery.Channel != "email" && delivery.Channel != "desktop" && delivery.Channel != "chat" && delivery.Channel != "push" ||
			delivery.Status != "pending" && (delivery.Status != "digest" || delivery.Channel != "email") ||
			delivery.Attempts != 0 || delivery.DeliveredAt != nil || delivery.AcknowledgedAt != nil || delivery.Error != "" {
			return ErrMetadataRecordQuery
		}
		seen[delivery.Channel] = true
	}
	return nil
}

func (w *pulseSummaryWork) policy(source *metadataLogicalSource) (enabled bool, schedule string, err error) {
	enabled = true
	err = pulseSummaryField(source, "workspaceSettings").Children(w.ctx, func(key reflect.Value, node metadataLogicalNode) error {
		switch key.String() {
		case "FeatureFlags":
			return node.MapEntries(w.ctx, func(key, value metadataLogicalNode) error {
				match, err := key.StringEqual(w.ctx, "pulse")
				if err != nil || !match {
					return err
				}
				flag, err := w.native(value, 8)
				if err == nil {
					enabled = flag.Bool()
				}
				return err
			})
		case "FeatureSettings":
			return node.Children(w.ctx, func(key reflect.Value, value metadataLogicalNode) error {
				if key.String() != "PulseWorkspaceSchedule" {
					return nil
				}
				selected, err := w.native(value, 8)
				if err == nil {
					schedule = selected.String()
				}
				return err
			})
		}
		return nil
	})
	return
}

func (w *pulseSummaryWork) cursor(source *metadataLogicalSource) (before domain.Bootstrap, raw []byte, found bool, err error) {
	before.Settings = map[string]any{}
	err = pulseSummaryField(source, "settings").MapEntries(w.ctx, func(key, node metadataLogicalNode) error {
		match, err := key.StringEqual(w.ctx, "pulseDeliveryCursors")
		if err != nil || !match {
			return err
		}
		value, err := w.native(node, 16)
		if err != nil {
			return err
		}
		found = true
		before.Settings["pulseDeliveryCursors"] = value.Interface()
		raw, err = json.Marshal(value.Interface())
		return err
	})
	return
}

func (w *pulseSummaryWork) canonicalCursor(tx *sqlTx, workspace, lock string) ([]byte, bool, error) {
	length := w.store.scaleP21SW22VisibilityLength("data")
	var size uint64
	err := tx.QueryRowContext(w.ctx, `SELECT `+length+` FROM workspace_metadata_records WHERE workspace_key=? AND field='settings' AND record_key='pulseDeliveryCursors'`+lock, workspace).Scan(&size)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if size > (metadataWorkBytes-(64<<10))/32 {
		return nil, true, ErrMetadataWorkBudget
	}
	if err := w.reserve(size*32 + 64<<10); err != nil {
		return nil, true, err
	}
	var raw []byte
	if err := tx.QueryRowContext(w.ctx, `SELECT CASE WHEN `+length+`<=? THEN data ELSE NULL END FROM workspace_metadata_records WHERE workspace_key=? AND field='settings' AND record_key='pulseDeliveryCursors'`+lock, size, workspace).Scan(&raw); err != nil {
		return nil, true, err
	}
	if raw == nil {
		return nil, true, ErrConcurrentWrite
	}
	parsedWork, err := metadataAggregatePayloadJSONWork(w.ctx, raw)
	if err == nil {
		err = w.reserve(parsedWork)
	}
	return raw, true, err
}

func (w *pulseSummaryWork) wanted(key metadataLogicalNode, needed map[string]int, maxID int) (index int, found bool, err error) {
	cursor := metadataLogicalCursor{node: key, offset: key.offset + 16, ctx: w.ctx}
	length, err := cursor.uint()
	if err != nil || length > uint64(maxID) {
		return 0, false, err
	}
	if key.cost > (metadataWorkBytes-256)/8 {
		return 0, false, ErrMetadataWorkBudget
	}
	_, release, err := w.store.tryAdmitAdditionalMetadataWork(w.ctx, int(key.cost)*8+256)
	if err != nil {
		return 0, false, err
	}
	defer release()
	value, err := key.Value(w.ctx, int(key.cost))
	if err == nil {
		index, found = needed[value.String()]
	}
	return
}

func (w *pulseSummaryWork) recipients(source *metadataLogicalSource, candidates []PulseSummaryCandidate, needed map[string]int, maxID int) (map[string]string, map[string]domain.NotificationPreferences, map[string]bool, error) {
	schedules := map[string]string{}
	preferences := map[string]domain.NotificationPreferences{}
	push := map[string]bool{}
	for _, kind := range []string{"userSettings", "notificationPreferences"} {
		err := pulseSummaryField(source, kind).MapEntries(w.ctx, func(key, node metadataLogicalNode) error {
			index, wanted, err := w.wanted(key, needed, maxID)
			if err != nil || !wanted {
				return err
			}
			id := candidates[index].UserID
			if kind == "userSettings" {
				return node.Children(w.ctx, func(key reflect.Value, value metadataLogicalNode) error {
					if key.String() != "PulseSchedule" {
						return nil
					}
					selected, err := w.native(value, 8)
					if err == nil {
						schedules[id] = selected.String()
					}
					return err
				})
			}
			value, err := w.preferences(node)
			if err == nil {
				preferences[id] = value
			}
			return err
		})
		if err != nil {
			return nil, nil, nil, err
		}
	}
	err := pulseSummaryField(source, "pushSubscriptions").Children(w.ctx, func(_ reflect.Value, record metadataLogicalNode) error {
		var user, enabled metadataLogicalNode
		if err := record.Children(w.ctx, func(key reflect.Value, node metadataLogicalNode) error {
			switch key.String() {
			case "UserID":
				user = node
			case "Enabled":
				enabled = node
			}
			return nil
		}); err != nil {
			return err
		}
		index, wanted, err := w.wanted(user, needed, maxID)
		if err != nil || !wanted {
			return err
		}
		value, err := w.native(enabled, 8)
		if err == nil && value.Bool() {
			push[candidates[index].UserID] = true
		}
		return err
	})
	return schedules, preferences, push, err
}

func (w *pulseSummaryWork) preferences(node metadataLogicalNode) (value domain.NotificationPreferences, err error) {
	target := reflect.ValueOf(&value).Elem()
	err = node.Children(w.ctx, func(key reflect.Value, child metadataLogicalNode) error {
		switch key.String() {
		case "Inbox", "Email", "Desktop":
			channel, err := w.channel(child)
			if err == nil {
				target.FieldByName(key.String()).Set(reflect.ValueOf(channel))
			}
			return err
		case "Chat":
			return child.Children(w.ctx, func(_ reflect.Value, channel metadataLogicalNode) error {
				selected, err := w.channel(channel)
				if err == nil {
					value.Chat = &selected
				}
				return err
			})
		case "UserID", "EmailFormat", "DelayLowPriority", "ImmediateUrgent":
			selected, err := w.native(child, 8)
			if err == nil {
				target.FieldByName(key.String()).Set(selected)
			}
			return err
		}
		return nil
	})
	return
}

func (w *pulseSummaryWork) channel(node metadataLogicalNode) (value domain.NotificationChannelPreferences, err error) {
	err = node.Children(w.ctx, func(key reflect.Value, child metadataLogicalNode) error {
		switch key.String() {
		case "Enabled":
			selected, err := w.native(child, 8)
			if err == nil {
				value.Enabled = selected.Bool()
			}
			return err
		case "Categories":
			isNil, err := child.isNil(w.ctx)
			if err != nil || isNil {
				return err
			}
			value.Categories = map[string]bool{}
			return child.MapEntries(w.ctx, func(key, flag metadataLogicalNode) error {
				match, err := key.StringEqual(w.ctx, "pulse")
				if err != nil || !match {
					return err
				}
				selected, err := w.native(flag, 8)
				if err == nil {
					value.Categories["pulse"] = selected.Bool()
				}
				return err
			})
		}
		return nil
	})
	return
}

// The replacement is either fresh standard-JSON data, or the cursor's typed
// time map from that JSON parse plus UTC pulseWindow ends. Auditing these known
// parsed locations preserves native preimages without a read-only WireValue.
func pulseSummaryForkCursor(ctx context.Context, source *metadataLogicalSource, value any, present bool) (*metadataLogicalSource, error) {
	builder, err := newMetadataLogicalBuilder(metadataLogicalSourceScratchContext(ctx, source))
	if err != nil {
		return nil, err
	}
	defer builder.Close()
	var replacement metadataAggregateNode
	if present {
		selected := reflect.ValueOf(&value).Elem()
		if err := builder.auditJSONLocations(selected); err != nil {
			return nil, err
		}
		node, err := builder.writeValue(selected, 0, false)
		if err != nil {
			return nil, err
		}
		if node.unavailable {
			return nil, ErrBoundedMetadataCoverage
		}
		replacement = metadataAggregateStoredNode{node}
	}
	return source.Transform(ctx, func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
		base := metadataAggregateStoredNode{node}
		if name != "settings" {
			return base, false, nil
		}
		return metadataLogicalPatchedCollection{base: base, shape: node, key: "pulseDeliveryCursors", value: replacement}, true, nil
	})
}

func (s *SQLiteStore) refreshPulseSummaryCursor(ctx context.Context, workspace string) func(error) error {
	return func(conflict error) error {
		keys, handled := conflictedMetadataKeys(conflict)
		if !handled {
			return nil
		}
		if len(keys) != 1 || keys[0] != (metadataRecordKey{"settings", "pulseDeliveryCursors"}) {
			return ErrMetadataRecordQuery
		}
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		source, err := s.retainMetadataLogicalSource(ctx, workspace)
		if err != nil {
			return err
		}
		defer source.Release()
		owned := &pulseSummaryWork{store: s, ctx: ctx}
		defer owned.close()
		tx, err := s.db.BeginTx(ctx, s.recordTxOptions())
		if err != nil {
			return err
		}
		defer tx.Rollback()
		lock := ""
		if s.dialect != "sqlite" {
			lock = " FOR UPDATE"
		}
		raw, present, err := owned.canonicalCursor(tx, workspace, lock)
		if err != nil {
			return err
		}
		stamp, err := readMetadataStamp(ctx, tx, workspace)
		if err != nil {
			return err
		}
		if stamp.workspaceID != source.Stamp().workspaceID {
			return ErrResourceAccessChanged
		}
		var value any
		if present {
			if err := json.Unmarshal(raw, &value); err != nil {
				return err
			}
		}
		candidate, err := pulseSummaryForkCursor(ctx, source, value, present)
		if err != nil {
			return err
		}
		stamp.revision = -1
		if err := candidate.setStamp(stamp); err != nil {
			candidate.Release()
			return err
		}
		s.installMetadataLogicalSource(workspace, candidate)
		s.mu.Lock()
		s.forgetCopyRevisionLocked(workspace)
		s.mu.Unlock()
		return nil
	}
}
