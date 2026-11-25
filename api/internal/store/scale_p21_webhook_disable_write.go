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

func webhookDisableNative(w *webhookFailureWriteWork, node metadataLogicalNode, copies uint64) (reflect.Value, error) {
	if node.cost > (metadataWorkBytes-4096)/copies {
		return reflect.Value{}, ErrMetadataWorkBudget
	}
	if err := w.reserve(node.cost*copies + 4096); err != nil {
		return reflect.Value{}, err
	}
	return node.Value(w.ctx, int(node.cost))
}

func webhookDisableFirst(ctx context.Context, source *metadataLogicalSource, field, id string) (metadataLogicalNode, bool, error) {
	var found metadataLogicalNode
	err := webhookFailureWriteField(source, field).Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
		match, err := webhookFailureWriteMatches(ctx, record, "ID", id)
		if err != nil {
			return err
		}
		if match {
			found = record
			return errMetadataAggregateStop
		}
		return nil
	})
	if errors.Is(err, errMetadataAggregateStop) {
		err = nil
	}
	return found, found.typ != nil, err
}

func webhookDisableMap(ctx context.Context, source *metadataLogicalSource, field, id string) (metadataLogicalNode, bool, error) {
	var found metadataLogicalNode
	err := webhookFailureWriteField(source, field).MapEntries(ctx, func(key, value metadataLogicalNode) error {
		match, err := key.StringEqual(ctx, id)
		if err != nil {
			return err
		}
		if match {
			found = value
		}
		return nil
	})
	return found, found.typ != nil, err
}

// headers preserves the literal resource query and physical row locator.
// Delivery selection uses the JSON notification IDs, including empty IDs.
func webhookDisableHeaders(e *metadataLogicalExternal, kind, resource string, parents []string) error {
	body, id, owner := "LENGTH(data)", "LENGTH(id)", "LENGTH(resource_id)"
	physical := "0"
	if e.store.dialect == "sqlite" {
		body, id, owner = "LENGTH(CAST(data AS BLOB))", "LENGTH(CAST(id AS BLOB))", "LENGTH(CAST(resource_id AS BLOB))"
		physical = "CASE WHEN typeof(id)='blob' THEN 1 ELSE 0 END+CASE WHEN typeof(resource_id)='blob' THEN 2 ELSE 0 END"
	} else if e.store.dialect == "postgres" {
		body, id, owner = "OCTET_LENGTH(data)", "OCTET_LENGTH(id)", "OCTET_LENGTH(resource_id)"
	}
	query := `SELECT CASE WHEN ` + owner + `<=? THEN resource_id ELSE NULL END,CASE WHEN ` + id + `<=? THEN id ELSE NULL END,` + physical + `,` + body + ` FROM workspace_content_records WHERE workspace_key=? AND kind=?`
	args := []any{metadataLogicalExternalRecord, metadataLogicalExternalRecord, e.workspace, kind}
	if kind == "delivery" {
		clause, values := bindList("parent_id", parents)
		query += " AND " + clause
		args = append(args, values...)
	} else {
		query += " AND resource_id=? ORDER BY created_at,id"
		args = append(args, resource)
	}
	rows, err := e.tx.QueryContext(e.ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var resource, id string
		var physical int
		var size int64
		if err := rows.Scan(&resource, &id, &physical, &size); err != nil {
			return err
		}
		if size < 0 || size > metadataLogicalExternalRecord {
			return ErrMetadataWorkBudget
		}
		if _, err := e.stage.exec(e.ctx, int64(len(resource)+len(id)+128),
			`INSERT INTO external_headers(kind,resource,id,physical,size) VALUES(?,?,?,?,?)`, kind, []byte(resource), []byte(id), physical, size); err != nil {
			return err
		}
	}
	return rows.Err()
}

func webhookDisableDeliveryCollection(e *metadataLogicalExternal) error {
	if err := e.builder.BeginCollection("notificationDeliveries", false); err != nil {
		return err
	}
	err := e.walk("delivery", false, func(header metadataLogicalExternalHeader) error {
		raw, err := e.body("delivery", header)
		if err != nil {
			return err
		}
		var value domain.NotificationDelivery
		if err := metadataLogicalExternalDecode(raw, &value); err != nil {
			return err
		}
		v := reflect.ValueOf(value)
		if err := e.builder.auditJSONLocations(v); err != nil {
			return err
		}
		return e.builder.Append(reflect.Value{}, v)
	})
	if err != nil {
		return err
	}
	return e.builder.EndCollection()
}

// This source has exactly the external collections mutateScoped supplies.
// Imported bodies and unrelated issue/content records never enter this image.
func (s *SQLiteStore) webhookDisableScopedSource(ctx context.Context, tx *sqlTx, source *metadataLogicalSource, workspace, resource string, work *webhookFailureWriteWork) (_ *metadataLogicalSource, err error) {
	ctx = metadataLogicalSourceScratchContext(ctx, source)
	stage, err := sw26NewMigrationStage(ctx)
	if err != nil {
		return nil, err
	}
	defer stage.Close()
	for _, query := range []string{
		`CREATE TABLE external_headers(seq INTEGER PRIMARY KEY,kind TEXT NOT NULL,resource BLOB NOT NULL,id BLOB NOT NULL,physical INTEGER NOT NULL,size INTEGER NOT NULL,payload BLOB)`,
		`CREATE INDEX external_headers_group ON external_headers(kind,resource,seq)`,
	} {
		if _, err := stage.exec(ctx, 4096, query); err != nil {
			return nil, err
		}
	}
	builder, err := newMetadataLogicalBuilder(metadataLogicalSourceScratchContext(ctx, source))
	if err != nil {
		return nil, err
	}
	defer builder.Close()
	if err := builder.Field("issues", reflect.ValueOf([]domain.Issue{})); err != nil {
		return nil, err
	}
	external := &metadataLogicalExternal{s, ctx, tx, workspace, stage, builder}
	part := func() error {
		next, err := newMetadataLogicalBuilder(ctx)
		if err != nil {
			return err
		}
		builder.source.stores = append(builder.source.stores, next.storage)
		builder.storage, builder.offset = next.storage, 0
		next.source.stores = nil
		next.source.Release()
		return nil
	}
	for _, field := range [][2]string{{"comments", "comment"}, {"activities", "activity"}, {"notifications", "notification"}} {
		if resource != "" {
			if err := webhookDisableHeaders(external, field[1], resource, nil); err != nil {
				return nil, err
			}
		}
		if err := part(); err != nil {
			return nil, err
		}
		if err := external.collection(field[0], field[1], webhookFailureWriteField(source, field[0]).typ); err != nil {
			return nil, err
		}
	}
	parents := []string{}
	flush := func() error {
		if len(parents) == 0 {
			return nil
		}
		err := webhookDisableHeaders(external, "delivery", "", parents)
		parents = parents[:0]
		return err
	}
	index, _, _ := metadataLogicalField("notifications")
	err = builder.source.fields[index].Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
		id, err := issueHotField(ctx, record, "ID")
		if err != nil {
			return err
		}
		value, err := webhookDisableNative(work, id, 8)
		if err != nil {
			return err
		}
		parents = append(parents, value.String())
		if len(parents) == 500 {
			return flush()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := flush(); err != nil {
		return nil, err
	}
	if err := part(); err != nil {
		return nil, err
	}
	if err := webhookDisableDeliveryCollection(external); err != nil {
		return nil, err
	}
	err = source.VisitFields(ctx, func(name string, node metadataLogicalNode) error {
		index, _, ok := metadataLogicalField(name)
		if !ok || name == "-" || builder.source.fields[index].typ != nil {
			return nil
		}
		if !slices.Contains(builder.source.stores, node.source) {
			if err := node.source.Retain(); err != nil {
				return err
			}
			builder.source.stores = append(builder.source.stores, node.source)
		}
		builder.source.fields[index] = node
		return nil
	})
	if err != nil {
		return nil, err
	}
	return builder.Finish(source.Stamp())
}

func webhookDisablePreferences(w *webhookFailureWriteWork, record metadataLogicalNode) (domain.NotificationPreferences, error) {
	var result domain.NotificationPreferences
	err := record.Children(w.ctx, func(key reflect.Value, node metadataLogicalNode) error {
		switch key.String() {
		case "EmailFormat", "DelayLowPriority", "ImmediateUrgent":
			value, err := webhookDisableNative(w, node, 8)
			if err == nil {
				reflect.ValueOf(&result).Elem().FieldByName(key.String()).Set(value)
			}
			return err
		case "Email", "Desktop", "Chat":
			channelNode := node
			if key.String() == "Chat" {
				channelNode = metadataLogicalNode{}
				if err := node.Children(w.ctx, func(_ reflect.Value, child metadataLogicalNode) error { channelNode = child; return nil }); err != nil {
					return err
				}
				if channelNode.typ == nil {
					return nil
				}
			}
			var channel domain.NotificationChannelPreferences
			err := channelNode.Children(w.ctx, func(field reflect.Value, child metadataLogicalNode) error {
				if field.String() == "Enabled" {
					value, err := webhookDisableNative(w, child, 8)
					if err == nil {
						channel.Enabled = value.Bool()
					}
					return err
				}
				if field.String() != "Categories" {
					return nil
				}
				nilValue, err := child.isNil(w.ctx)
				if err != nil || nilValue {
					return err
				}
				channel.Categories = map[string]bool{}
				return child.MapEntries(w.ctx, func(k, value metadataLogicalNode) error {
					match, err := k.StringEqual(w.ctx, "integrations")
					if err != nil || !match {
						return err
					}
					selected, err := webhookDisableNative(w, value, 8)
					if err == nil {
						channel.Categories["integrations"] = selected.Bool()
					}
					return err
				})
			})
			if err != nil {
				return err
			}
			switch key.String() {
			case "Email":
				result.Email = channel
			case "Desktop":
				result.Desktop = channel
			case "Chat":
				result.Chat = &channel
			}
		}
		return nil
	})
	return result, err
}

func webhookDisableInputs(w *webhookFailureWriteWork, source *metadataLogicalSource, hook domain.Webhook) (domain.Bootstrap, error) {
	data := domain.Bootstrap{Webhooks: []domain.Webhook{hook}}
	viewer, err := webhookDisableNative(w, webhookFailureWriteField(source, "viewer"), 16)
	if err != nil {
		return data, err
	}
	data.Viewer = viewer.Interface().(domain.User)
	workspaceID, err := issueHotField(w.ctx, webhookFailureWriteField(source, "workspace"), "ID")
	if err != nil {
		return data, err
	}
	value, err := webhookDisableNative(w, workspaceID, 8)
	if err != nil {
		return data, err
	}
	data.Workspace.ID = value.String()
	if hook.CreatorID != "" {
		if user, found, err := webhookDisableFirst(w.ctx, source, "users", hook.CreatorID); err != nil {
			return data, err
		} else if found {
			selected, err := webhookDisableNative(w, user, 24)
			if err != nil {
				return data, err
			}
			data.Users = []domain.User{selected.Interface().(domain.User)}
		}
	}
	if len(data.Users) == 0 || !data.Users[0].Active {
		return data, nil
	}
	recipient := data.Users[0].ID
	if prefs, found, err := webhookDisableMap(w.ctx, source, "notificationPreferences", recipient); err != nil {
		return data, err
	} else if found {
		preferences, err := webhookDisablePreferences(w, prefs)
		if err != nil {
			return data, err
		}
		data.NotificationPreferences = map[string]domain.NotificationPreferences{recipient: preferences}
	}
	err = webhookFailureWriteField(source, "pushSubscriptions").Children(w.ctx, func(_ reflect.Value, record metadataLogicalNode) error {
		match, err := webhookFailureWriteMatches(w.ctx, record, "UserID", recipient)
		if err != nil || !match {
			return err
		}
		enabled, err := issueHotField(w.ctx, record, "Enabled")
		if err != nil {
			return err
		}
		value, err := webhookDisableNative(w, enabled, 8)
		if err == nil && value.Bool() {
			data.PushSubscriptions = []domain.PushSubscription{{UserID: recipient, Enabled: true}}
		}
		return err
	})
	if err != nil || !fullMutationsForced.Load() {
		return data, err
	}
	issueNode, found, err := webhookDisableFirst(w.ctx, source, "issues", "")
	if err != nil || !found {
		return data, err
	}
	issue := domain.Issue{}
	for _, name := range []string{"Priority", "Team", "Assignee"} {
		node, err := issueHotField(w.ctx, issueNode, name)
		if err != nil {
			return data, err
		}
		if name == "Priority" {
			value, err := webhookDisableNative(w, node, 8)
			if err != nil {
				return data, err
			}
			issue.Priority = int(value.Int())
			continue
		}
		if name == "Assignee" {
			unwrapped, err := metadataAggregateUnwrap(w.ctx, metadataAggregateStoredNode{node}, true)
			if err != nil {
				return data, err
			}
			if unwrapped == nil {
				continue
			}
			node = unwrapped.(metadataAggregateStoredNode).value
		}
		id, err := issueHotField(w.ctx, node, "ID")
		if err != nil {
			return data, err
		}
		value, err := webhookDisableNative(w, id, 8)
		if err != nil {
			return data, err
		}
		if name == "Team" {
			issue.Team.ID = value.String()
		} else {
			issue.Assignee = &domain.User{ID: value.String()}
		}
	}
	data.Issues = []domain.Issue{issue}
	if settings, found, err := webhookDisableMap(w.ctx, source, "teamSettings", issue.Team.ID); err != nil {
		return data, err
	} else if found {
		zone, err := issueHotField(w.ctx, settings, "Timezone")
		if err != nil {
			return data, err
		}
		value, err := webhookDisableNative(w, zone, 8)
		if err != nil {
			return data, err
		}
		data.TeamSettings = map[string]domain.TeamSettings{issue.Team.ID: {Timezone: value.String()}}
	}
	err = webhookFailureWriteField(source, "issueSlas").Children(w.ctx, func(_ reflect.Value, record metadataLogicalNode) error {
		match, err := webhookFailureWriteMatches(w.ctx, record, "IssueID", "")
		if err != nil || !match {
			return err
		}
		breached, err := webhookFailureWriteMatches(w.ctx, record, "Status", "breached")
		if err == nil && breached {
			data.IssueSLAs = []domain.IssueSLA{{IssueID: "", Status: "breached"}}
		}
		return err
	})
	return data, err
}

func webhookDisablePersistInputs(w *webhookFailureWriteWork, source *metadataLogicalSource, output domain.Bootstrap) (domain.Bootstrap, error) {
	data := domain.Bootstrap{Users: slices.Clone(output.Users)}
	for _, name := range []string{"comments", "activities", "notifications", "notificationDeliveries"} {
		node := webhookFailureWriteField(source, name)
		value, err := webhookDisableNative(w, node, 12)
		if err != nil {
			return data, err
		}
		index, _, _ := metadataLogicalField(name)
		reflect.ValueOf(&data).Elem().Field(index).Set(value)
	}
	data.Notifications = append(data.Notifications, output.Notifications...)
	for _, delivery := range output.NotificationDeliveries {
		index := slices.IndexFunc(data.NotificationDeliveries, func(item domain.NotificationDelivery) bool { return item.ID == delivery.ID })
		if index < 0 {
			data.NotificationDeliveries = append(data.NotificationDeliveries, delivery)
			continue
		}
		data.NotificationDeliveries[index].Status = delivery.Status
		data.NotificationDeliveries[index].UpdatedAt = delivery.UpdatedAt
		if delivery.Channel == "email" && delivery.Status == "digest" {
			data.NotificationDeliveries[index].NextAttemptAt = delivery.NextAttemptAt
		}
	}
	// The canonical display comparison needs directory membership, not full
	// profiles. Refreshed rows already own the actual display values.
	ids := map[string]bool{}
	for _, comments := range data.Comments {
		for _, comment := range comments {
			ids[comment.User.ID] = true
		}
	}
	for _, activities := range data.Activities {
		for _, activity := range activities {
			ids[activity.Actor.ID] = true
		}
	}
	for _, notification := range data.Notifications {
		ids[notification.Actor.ID] = true
	}
	for id := range ids {
		if _, found, err := webhookDisableFirst(w.ctx, source, "users", id); err != nil {
			return data, err
		} else if found {
			data.Users = append(data.Users, domain.User{ID: id})
		}
	}
	return data, nil
}

// MutateFailingWebhookDisable is restricted to the server's disable callback.
// Its content appends, audit and first native webhook update share one commit.
func (s *SQLiteStore) MutateFailingWebhookDisable(ctx context.Context, workspace, webhookID string, mutate func(*domain.Bootstrap) error) error {
	if !s.BoundedMetadataEnabled() || !RecordHydrationEnabled() || mutate == nil {
		return ErrBoundedMetadataCoverage
	}
	ctx = context.WithValue(metadataLogicalUnpinnedContext(ctx), metadataWorkLeaseKey{}, nil)
	ctx, release, err := s.tryAdmitAdditionalMetadataWork(ctx, 2<<20)
	if err != nil {
		return err
	}
	defer release()
	if workspace == "" {
		s.mu.RLock()
		workspace = s.lastWorkspaceKey
		s.mu.RUnlock()
	}
	ctx, collector, callback := collectMutationAudit(ctx, workspace, func(data *domain.Bootstrap) (string, error) { return webhookID, mutate(data) })
	var event domain.DomainEvent
	var realtime json.RawMessage
	var releaseEvent func()
	defer func() {
		if releaseEvent != nil {
			releaseEvent()
		}
	}()
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
		node, found, err := webhookDisableFirst(ctx, source, "webhooks", webhookID)
		if err != nil {
			return err
		}
		if !found {
			return ErrNoMutation
		}
		hookValue, err := webhookDisableNative(owned, node, 24)
		if err != nil {
			return err
		}
		hook := hookValue.Interface().(domain.Webhook)
		if !hook.Enabled {
			return ErrNoMutation
		}
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
		exact := source.Stamp() == baseStamp
		if s.coordinator != nil && !exact {
			return ErrConcurrentWrite
		}
		identityCtx := WithMetadataRecords(ctx, MetadataRecordScope{})
		identityRoot, err := scopedMetadataRoot(root, MetadataRecordScope{})
		if err != nil {
			return err
		}
		identityBudget := newMetadataHydrationBudget()
		if err := identityBudget.consume(identityRoot); err != nil {
			return err
		}
		var storage domain.Bootstrap
		if err := json.Unmarshal(identityRoot, &storage); err != nil {
			return err
		}
		parserCtx, releaseParser, err := s.tryAdmitAdditionalMetadataWork(ctx, metadataLogicalExternalWork)
		if err != nil {
			return err
		}
		scoped, err := s.webhookDisableScopedSource(parserCtx, tx, source, workspace, webhookID, owned)
		releaseParser()
		if err != nil {
			return err
		}
		defer scoped.Release()
		eventSource := scoped
		if fullMutationsForced.Load() {
			parserCtx, releaseParser, err := s.tryAdmitAdditionalMetadataWork(ctx, metadataLogicalExternalWork)
			if err != nil {
				return err
			}
			eventSource, err = s.hydrateMetadataLogicalSource(parserCtx, tx, source, workspace)
			releaseParser()
			if err != nil {
				return err
			}
			defer eventSource.Release()
		}
		refreshed, err := eventSource.Transform(ctx, func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
			base := metadataAggregateNode(metadataAggregateStoredNode{node})
			if name == "members" && !fullMutationsForced.Load() {
				return base, false, nil
			}
			result := metadataAggregateRefreshField(eventSource, name, base)
			_, changed := result.(metadataAggregateRefreshCollection)
			return result, changed, nil
		})
		if err != nil {
			return err
		}
		defer refreshed.Release()
		callbackSource := refreshed
		if fullMutationsForced.Load() {
			callbackSource, err = refreshMetadataLogicalIssueReferences(ctx, refreshed)
			if err != nil {
				return err
			}
			defer callbackSource.Release()
		}
		input, err := webhookDisableInputs(owned, callbackSource, hook)
		if err != nil {
			return err
		}
		_, err = callback(&input)
		if err != nil {
			return err
		}
		if len(input.Webhooks) != 1 || input.Webhooks[0].ID != hook.ID || len(input.Notifications) > 1 || len(input.NotificationDeliveries) > 4 {
			return ErrMetadataRecordQuery
		}
		if input.Webhooks[0].Enabled || input.Webhooks[0].DisabledReason != "deliveryFailures" {
			return ErrMetadataRecordQuery
		}
		for _, value := range []any{input.Webhooks, input.Notifications, input.NotificationDeliveries, collector.pending} {
			cost, err := metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(value), 0)
			if err != nil {
				return err
			}
			if err := owned.reserve(uint64(cost)); err != nil {
				return err
			}
		}
		// Persistence uses the admitted resource scope, never the full
		// forced-oracle issue/content graph.
		persistSource := scoped
		refreshedScoped, err := scoped.Transform(ctx, func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
			base := metadataAggregateNode(metadataAggregateStoredNode{node})
			if name == "members" {
				return base, false, nil
			}
			result := metadataAggregateRefreshField(scoped, name, base)
			_, changed := result.(metadataAggregateRefreshCollection)
			return result, changed, nil
		})
		if err != nil {
			return err
		}
		defer refreshedScoped.Release()
		persistSource = refreshedScoped
		persist, err := webhookDisablePersistInputs(owned, persistSource, input)
		if err != nil {
			return err
		}
		candidate, err := callbackSource.Transform(ctx, func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
			if name == "webhooks" {
				return metadataLogicalPatchedCollection{base: metadataAggregateStoredNode{node}, shape: node, key: hook.ID, value: metadataAggregateNativeNode{reflect.ValueOf(input.Webhooks[0])}}, true, nil
			}
			if name == "notifications" {
				return webhookDisableAppendCollection{base: node, values: reflect.ValueOf(input.Notifications)}, true, nil
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
		hooks, err := s.logicalWebhookNeeded(ctx, source, workspace)
		if err != nil {
			return err
		}
		payload := json.RawMessage(`{"reason":"deliveryFailures"}`)
		eventPublication, err := s.prepareMetadataAggregatePublication(ctx, metadataAggregateImage{Source: eventSource}, metadataAggregateImage{Source: candidate}, webhookID, hooks && s.webhookConfigured(), payload, "webhook.auto_disabled")
		if err != nil {
			return err
		}
		releaseEvent = eventPublication.Close
		event = domain.DomainEvent{ID: idgen.New("evt_"), Type: "webhook.auto_disabled", AggregateID: webhookID, Payload: payload, PreviousValues: eventPublication.PreviousValues, CreatedAt: time.Now().UTC()}
		realtime = eventPublication.RealtimePayload
		keys := []string{}
		if webhookID != "" {
			keys = append(keys, webhookID)
		}
		deliveries := map[string]bool{}
		for _, delivery := range persist.NotificationDeliveries {
			deliveries[delivery.ID] = true
		}
		if _, err := s.persistScopedRecords(ctx, tx, workspace, persist, nil, nil, false, keys, deliveries); err != nil {
			return err
		}
		scope := MetadataRecordScope{Write: []RecordKey{{Kind: "webhooks", ID: webhookID}}}
		changes, _, err := scaleP16RecordChanges(domain.Bootstrap{Webhooks: []domain.Webhook{hook}}, domain.Bootstrap{Webhooks: input.Webhooks})
		if err != nil {
			return err
		}
		if shapes["webhooks"] == "array" {
			if err := scaleP16WriteChanges(ctx, tx, workspace, newIssueReferences(domain.Bootstrap{}), changes); err != nil {
				return err
			}
			post, err := metadataLogicalMergedPostimage(ctx, candidate, changes)
			if err != nil {
				return err
			}
			candidate.Release()
			candidate = post
		} else {
			post, err := s.persistMetadataLogicalInlinePostimage(ctx, tx, workspace, "webhooks", candidate, webhookFailureWriteField(source, "webhooks"), webhookFailureWriteField(candidate, "webhooks"))
			if err != nil {
				return err
			}
			candidate.Release()
			candidate = post
		}
		if err := s.persistMetadataLogicalRefresh(ctx, tx, workspace, eventSource, refreshed, scope, candidate); err != nil {
			return err
		}
		installed, err := metadataLogicalInstalledCandidate(ctx, candidate)
		if err != nil {
			return err
		}
		candidate.Release()
		candidate = installed
		complete := storage
		if err := s.persistWorkspaceTx(identityCtx, tx, workspace, &storage, &complete, nil, &event, nil); err != nil {
			return err
		}
		publication, err := s.prepareMetadataLogicalPublication(ctx, tx, workspace, candidate, baseStamp, exact, true)
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
	if len(collector.pending) != 0 {
		return fmt.Errorf("webhook disable left uncommitted audit entries")
	}
	s.dropMetadataCache(ctx, workspace)
	release()
	s.publishMutation(context.WithValue(ctx, metadataWorkLeaseKey{}, nil), workspace, event, realtime)
	return nil
}

type webhookDisableAppendCollection struct {
	base   metadataLogicalNode
	values reflect.Value
}

func (n webhookDisableAppendCollection) nodeType() reflect.Type           { return n.base.typ }
func (n webhookDisableAppendCollection) logicalBase() metadataLogicalNode { return n.base }
func (n webhookDisableAppendCollection) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n webhookDisableAppendCollection) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	ordinal := 0
	if err := n.base.Children(ctx, func(_ reflect.Value, node metadataLogicalNode) error {
		err := visit(reflect.ValueOf(ordinal), metadataAggregateStoredNode{node})
		ordinal++
		return err
	}); err != nil {
		return err
	}
	for index := 0; index < n.values.Len(); index++ {
		if err := visit(reflect.ValueOf(ordinal), metadataAggregateNativeNode{n.values.Index(index)}); err != nil {
			return err
		}
		ordinal++
	}
	return nil
}
