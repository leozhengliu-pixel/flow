package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

const webhookFailureWriteRetention = 50

type webhookFailureWriteWork struct {
	store    *SQLiteStore
	ctx      context.Context
	releases []func()
}

func (w *webhookFailureWriteWork) close() {
	for _, release := range w.releases {
		release()
	}
	w.releases = nil
}

func (w *webhookFailureWriteWork) reserve(cost uint64) error {
	if cost > metadataWorkBytes {
		return ErrMetadataWorkBudget
	}
	_, release, err := w.store.tryAdmitAdditionalMetadataWork(w.ctx, int(cost))
	if err == nil {
		w.releases = append(w.releases, release)
	}
	return err
}

func webhookFailureWriteField(source *metadataLogicalSource, name string) metadataLogicalNode {
	index, _, _ := metadataLogicalField(name)
	return source.fields[index]
}

func webhookFailureWriteMatches(ctx context.Context, record metadataLogicalNode, field, id string) (bool, error) {
	child, err := metadataAggregateChild(ctx, metadataAggregateStoredNode{record}, field)
	if err != nil {
		return false, err
	}
	return metadataAggregateStringEqual(ctx, child, id)
}

// webhookFailureWriteCollection preserves the original array order while
// dropping only the oldest matching occurrences. Bodies remain typed frames.
type webhookFailureWriteCollection struct {
	base      metadataLogicalNode
	webhookID string
	drop      int
	failure   domain.WebhookFailureEvent
}

func (n webhookFailureWriteCollection) nodeType() reflect.Type           { return n.base.typ }
func (n webhookFailureWriteCollection) logicalBase() metadataLogicalNode { return n.base }
func (n webhookFailureWriteCollection) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n webhookFailureWriteCollection) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	matched, ordinal := 0, 0
	err := n.base.Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
		match, err := webhookFailureWriteMatches(ctx, record, "WebhookID", n.webhookID)
		if err != nil {
			return err
		}
		if match {
			matched++
			if matched <= n.drop {
				return nil
			}
		}
		err = visit(reflect.ValueOf(ordinal), metadataAggregateStoredNode{record})
		ordinal++
		return err
	})
	if err != nil {
		return err
	}
	return visit(reflect.ValueOf(ordinal), metadataAggregateNativeNode{reflect.ValueOf(n.failure)})
}

type webhookFailureWriteRecord struct {
	before, after metadataLogicalNode
	position      int
	retained      bool
}

type webhookFailureWritePlan struct {
	found    bool
	drop     int
	changes  []metadataRecordChange
	orders   map[metadataRecordKey]int
	previous map[string]int
}

func (w *webhookFailureWriteWork) encoded(node metadataLogicalNode) ([]byte, error) {
	if node.cost > (metadataWorkBytes-4096)/16 {
		return nil, ErrMetadataWorkBudget
	}
	if err := w.reserve(node.cost*16 + 4096); err != nil {
		return nil, err
	}
	value, err := node.Value(w.ctx, int(node.cost))
	if err != nil {
		return nil, err
	}
	return json.Marshal(value.Interface())
}

func (w *webhookFailureWriteWork) plan(tx *sqlTx, workspace string, source *metadataLogicalSource, failure domain.WebhookFailureEvent) (*webhookFailureWritePlan, error) {
	plan := &webhookFailureWritePlan{}
	found := false
	err := webhookFailureWriteField(source, "webhooks").Children(w.ctx, func(_ reflect.Value, record metadataLogicalNode) error {
		match, err := webhookFailureWriteMatches(w.ctx, record, "ID", failure.WebhookID)
		found = found || match
		return err
	})
	if err != nil || !found {
		return plan, err
	}
	plan.found = true
	failures := webhookFailureWriteField(source, "webhookFailureEvents")
	count := 0
	err = failures.Children(w.ctx, func(_ reflect.Value, record metadataLogicalNode) error {
		match, err := webhookFailureWriteMatches(w.ctx, record, "WebhookID", failure.WebhookID)
		if match {
			count++
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	plan.drop = max(0, count-(webhookFailureWriteRetention-1))
	records := map[string]*webhookFailureWriteRecord{}
	matched, position := 0, 0
	err = failures.Children(w.ctx, func(_ reflect.Value, record metadataLogicalNode) error {
		match, err := webhookFailureWriteMatches(w.ctx, record, "WebhookID", failure.WebhookID)
		if err != nil {
			return err
		}
		if match {
			matched++
		}
		retained := !match || matched > plan.drop
		idNode, err := metadataAggregateChild(w.ctx, metadataAggregateStoredNode{record}, "ID")
		if err != nil {
			return err
		}
		node, ok := idNode.(metadataAggregateStoredNode)
		if !ok || node.value.typ.Kind() != reflect.String {
			return ErrMetadataRecordQuery
		}
		if node.value.cost > (metadataWorkBytes-2048)/8 {
			return ErrMetadataWorkBudget
		}
		if err := w.reserve(node.value.cost*8 + 4096); err != nil {
			return err
		}
		value, err := node.value.Value(w.ctx, int(node.value.cost))
		if err != nil {
			return err
		}
		id := value.String()
		item := records[id]
		if item == nil {
			item = &webhookFailureWriteRecord{}
			records[id] = item
		}
		item.before = record
		if retained {
			item.after, item.position, item.retained = record, position, true
			position++
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(records)+1)
	for key := range records {
		keys = append(keys, key)
	}
	if _, exists := records[failure.ID]; !exists {
		keys = append(keys, failure.ID)
	}
	plan.previous = map[string]int{}
	for start := 0; start < len(keys); start += MetadataRecordBatchMaxKeys {
		batch := keys[start:min(start+MetadataRecordBatchMaxKeys, len(keys))]
		clause, args := bindList("record_key", batch)
		rows, err := tx.QueryContext(w.ctx, `SELECT record_key,collection_order FROM workspace_metadata_records WHERE workspace_key=? AND field='webhookFailureEvents' AND `+clause, append([]any{workspace}, args...)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var key string
			var order int
			if err := rows.Scan(&key, &order); err != nil {
				rows.Close()
				return nil, err
			}
			if slices.Contains(batch, key) {
				plan.previous[key] = order
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	wanted := []domain.Issue{{ID: failure.ID, Number: position}}
	for key, item := range records {
		if key == failure.ID {
			continue
		}
		if !item.retained {
			plan.changes = append(plan.changes, metadataRecordChange{field: "webhookFailureEvents", key: key, drop: true})
			continue
		}
		wanted = append(wanted, domain.Issue{ID: key, Number: item.position})
		if item.before.source == item.after.source && item.before.offset == item.after.offset {
			continue
		}
		// Duplicates retain every native occurrence, while the canonical row
		// belongs to the last surviving occurrence for this ID.
		before, err := w.encoded(item.before)
		if err != nil {
			return nil, err
		}
		after, err := w.encoded(item.after)
		if err != nil {
			return nil, err
		}
		plan.changes = append(plan.changes, metadataRecordChange{field: "webhookFailureEvents", key: key, base: before, raw: after})
	}
	raw, err := json.Marshal(failure)
	if err != nil {
		return nil, err
	}
	if err := w.reserve(uint64(len(raw))*16 + 4096); err != nil {
		return nil, err
	}
	change := metadataRecordChange{field: "webhookFailureEvents", key: failure.ID, insert: true, raw: raw}
	if old := records[failure.ID]; old != nil {
		change.insert = false
		change.base, err = w.encoded(old.before)
		if err != nil {
			return nil, err
		}
	}
	plan.changes = append(plan.changes, change)
	slices.SortFunc(wanted, func(a, b domain.Issue) int { return a.Number - b.Number })
	plan.orders = map[metadataRecordKey]int{}
	for key, order := range issueCollectionOrder(wanted, plan.previous) {
		plan.orders[metadataRecordKey{field: "webhookFailureEvents", key: key}] = order
	}
	return plan, nil
}

// RecordWebhookDeliveryFailure is the bounded audit-stream failure writer.
// The server retains its literal MutateWorkspace callback off mode. Membership
// selection, retention, append and event insertion share one root-locked tx.
func (s *SQLiteStore) RecordWebhookDeliveryFailure(ctx context.Context, workspace string, failure domain.WebhookFailureEvent, httpStatus int) error {
	if !s.BoundedMetadataEnabled() || !RecordHydrationEnabled() {
		return ErrBoundedMetadataCoverage
	}
	ctx = context.WithValue(metadataLogicalUnpinnedContext(ctx), metadataWorkLeaseKey{}, nil)
	ctx, release, err := s.tryAdmitAdditionalMetadataWork(ctx, 2<<20)
	if err != nil {
		return err
	}
	defer release()
	_, releaseInput, err := s.reserveMetadataAggregatePayload(ctx, failure)
	if err != nil {
		return err
	}
	defer releaseInput()
	if workspace == "" {
		s.mu.RLock()
		workspace = s.lastWorkspaceKey
		s.mu.RUnlock()
	}
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
			releaseEvent()
			releaseEvent = nil
		}
		committed, realtime = domain.DomainEvent{}, nil
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		if s.coordinator != nil {
			if err := s.reloadResidentWorkspace(ctx, workspace); err != nil {
				return err
			}
		}
		read, found, err := s.BeginInstalledMetadataRead(ctx, workspace)
		if err != nil {
			return err
		}
		if !found {
			return ErrBoundedMetadataCoverage
		}
		defer read.Close()
		source := read.source
		owned := &webhookFailureWriteWork{store: s, ctx: ctx}
		defer owned.close()
		tx, err := s.db.BeginTx(ctx, s.recordTxOptions())
		if err != nil {
			return err
		}
		defer tx.Rollback()
		lock := ""
		if tx.dialect != "sqlite" {
			lock = " FOR UPDATE"
		}
		var root []byte
		if err := tx.QueryRowContext(ctx, `SELECT CASE WHEN LENGTH(data)<=? THEN data ELSE NULL END FROM workspace_states WHERE workspace_key=?`+lock, MetadataRecordPageMaxBytes, workspace).Scan(&root); err != nil {
			return err
		}
		if root == nil {
			return ErrMetadataPageBudget
		}
		_, shapes, err := residentMetadataRoot(root)
		if err != nil {
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
		refreshed, err := source.Transform(ctx, func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
			base := metadataAggregateNode(metadataAggregateStoredNode{node})
			refresh := metadataAggregateRefreshField(source, name, base)
			_, changed := refresh.(metadataAggregateRefreshCollection)
			return refresh, changed, nil
		})
		if err != nil {
			return err
		}
		defer refreshed.Release()
		retention, err := owned.plan(tx, workspace, refreshed, failure)
		if err != nil {
			return err
		}
		inline := false
		if retention.found && shapes["webhookFailureEvents"] != "array" {
			nonempty := false
			if err := webhookFailureWriteField(source, "webhookFailureEvents").Children(ctx, func(_ reflect.Value, _ metadataLogicalNode) error {
				nonempty = true
				return nil
			}); err != nil {
				return err
			}
			inline = nonempty
		}
		candidate, err := refreshed.Transform(ctx, func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
			if name == "webhookFailureEvents" && retention.found {
				return webhookFailureWriteCollection{base: node, webhookID: failure.WebhookID, drop: retention.drop, failure: failure}, true, nil
			}
			return metadataAggregateStoredNode{node}, false, nil
		})
		if err != nil {
			return err
		}
		defer func() {
			if candidate != nil {
				candidate.Release()
			}
		}()
		payload := map[string]any{"executionId": failure.ExecutionID, "httpStatus": httpStatus}
		payloadRaw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		hooks, err := s.logicalWebhookNeeded(ctx, source, workspace)
		if err != nil {
			return err
		}
		plan, err := planMetadataAggregatePayload(ctx, metadataAggregateImage{Source: source}, metadataAggregateImage{Source: candidate}, failure.WebhookID, hooks && s.webhookConfigured(), payloadRaw)
		if err != nil {
			return err
		}
		_, releaseEvent, err = s.tryAdmitAdditionalMetadataWork(ctx, plan.RequiredBytes)
		if err != nil {
			return err
		}
		previous, entity, err := plan.Values(ctx)
		if err != nil {
			return err
		}
		committed = domain.DomainEvent{ID: idgen.New("evt_"), Type: "webhook.delivery_failed", AggregateID: failure.WebhookID, Payload: payloadRaw, PreviousValues: previous, CreatedAt: time.Now().UTC()}
		realtime = enrichRealtimePayload(payloadRaw, entity, committed.Type)
		complete := storageBefore
		if err := s.persistWorkspaceTx(identityCtx, tx, workspace, &storageBefore, &complete, nil, &committed, nil); err != nil {
			return err
		}
		if inline {
			postimage, err := s.persistMetadataLogicalInlinePostimage(ctx, tx, workspace, "webhookFailureEvents", candidate,
				webhookFailureWriteField(source, "webhookFailureEvents"), webhookFailureWriteField(candidate, "webhookFailureEvents"))
			if err != nil {
				return err
			}
			candidate.Release()
			candidate = postimage
		} else if retention.found {
			if err := ensureOAuthMetadataShape(ctx, tx, workspace, "webhookFailureEvents", "array", lock); err != nil {
				return err
			}
			writeCtx := context.WithValue(ctx, metadataAppendOrdersKey{}, retention.orders)
			if err := scaleP16WriteChanges(writeCtx, tx, workspace, newIssueReferences(domain.Bootstrap{}), retention.changes); err != nil {
				return err
			}
			for key, order := range retention.orders {
				if old, exists := retention.previous[key.key]; exists && old != order {
					if _, err := tx.ExecContext(ctx, `UPDATE workspace_metadata_records SET collection_order=? WHERE workspace_key=? AND field=? AND record_key=?`, order, workspace, key.field, key.key); err != nil {
						return err
					}
					tx.metadataWritten = true
				}
			}
		}
		if !inline {
			merged, err := metadataLogicalMergedPostimage(ctx, candidate, retention.changes)
			if err != nil {
				return err
			}
			candidate.Release()
			candidate = merged
		}
		if err := s.persistMetadataLogicalRefresh(ctx, tx, workspace, source, refreshed, MetadataRecordScope{}, candidate); err != nil {
			return err
		}
		publication, err := s.prepareMetadataLogicalPublication(ctx, tx, workspace, candidate, baseStamp, exactSource, true)
		if err != nil {
			return err
		}
		candidate = nil
		defer publication.Close()
		if err := tx.Commit(); err != nil {
			return err
		}
		return publication.Install()
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
	s.dropMetadataCache(ctx, workspace)
	releaseInput()
	release()
	s.publishMutation(context.WithValue(ctx, metadataWorkLeaseKey{}, nil), workspace, committed, realtime)
	return nil
}
