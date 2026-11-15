package store

import (
	"context"
	"reflect"
	"strings"
	"sync"

	"flow/api/internal/domain"
)

// The source is already hydrated, reference-refreshed and actor-substituted.
// Ordinals name occurrences in that source, never physical SQL or decoded IDs.
type legacyDiscussionInputs struct {
	Data                                                                              *domain.Bootstrap
	ResourceKey                                                                       string
	CommentsPresent, ActivitiesPresent                                                bool
	TargetIssueOrdinal                                                                int
	IssueOrdinals, NotificationOrdinals, DeliveryOrdinals, ThreadSubscriptionOrdinals []int
	input                                                                             *issueHotInputs
	once                                                                              sync.Once
}

func (r *legacyDiscussionInputs) Close() {
	if r == nil {
		return
	}
	r.once.Do(func() {
		if r.input != nil {
			r.input.close()
		}
		r.input, r.Data = nil, nil
		r.ResourceKey = ""
		r.IssueOrdinals, r.NotificationOrdinals, r.DeliveryOrdinals, r.ThreadSubscriptionOrdinals = nil, nil, nil, nil
	})
}

func (r *legacyDiscussionInputs) value(ctx context.Context, node metadataLogicalNode, names ...string) (reflect.Value, error) {
	cost, err := issueHotProjectionCost(ctx, node, names)
	if err != nil {
		return reflect.Value{}, err
	}
	if err := r.input.reserve(ctx, cost); err != nil {
		return reflect.Value{}, err
	}
	return issueHotProjection(ctx, node, names, cost)
}

func (r *legacyDiscussionInputs) field(name string) metadataLogicalNode {
	index, _, _ := metadataLogicalField(name)
	return r.input.source.fields[index]
}

func (r *legacyDiscussionInputs) ownedKey(ctx context.Context, keys map[string]bool, id string) error {
	if keys[id] {
		return nil
	}
	if uint64(len(id)) > (metadataWorkBytes-1024)/8 {
		return ErrMetadataWorkBudget
	}
	if err := r.input.reserve(ctx, uint64(len(id))*8+1024); err != nil {
		return err
	}
	keys[strings.Clone(id)] = true
	return nil
}

// No membership, role lookup or display refresh belongs here. The original
// callback decides missing-issue/comment precedence from the selected values.
func (s *SQLiteStore) loadLegacyDiscussionInputs(ctx context.Context, source *metadataLogicalSource, issueID string) (_ *legacyDiscussionInputs, err error) {
	if s == nil || !s.BoundedMetadataEnabled() || source == nil {
		return nil, ErrBoundedMetadataCoverage
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := source.Retain(); err != nil {
		return nil, err
	}
	read := &issueHotInputs{store: s, source: source}
	r := &legacyDiscussionInputs{input: read, Data: &read.data, TargetIssueOrdinal: -1}
	defer func() {
		if err != nil {
			r.Close()
		}
	}()
	ctx = metadataLogicalSourceScratchContext(metadataLogicalUnpinnedContext(ctx), source)
	if uint64(len(issueID)) > (metadataWorkBytes-(128<<10))/8 {
		return nil, ErrMetadataWorkBudget
	}
	if err := read.reserve(ctx, uint64(len(issueID))*8+128<<10); err != nil {
		return nil, err
	}
	r.ResourceKey = strings.Clone(issueID)
	for _, name := range []string{"workspace", "viewer", "viewerRole"} {
		index, _, _ := metadataLogicalField(name)
		value, err := r.value(ctx, source.fields[index])
		if err != nil {
			return nil, err
		}
		reflect.ValueOf(r.Data).Elem().Field(index).Set(value)
	}
	for _, name := range append([]string{"issues", "comments", "activities", "notifications", "notificationDeliveries"}, issueDiscussionInputFields...) {
		if err := read.discussionCollectionShape(ctx, name); err != nil {
			return nil, err
		}
	}
	r.CommentsPresent, err = r.content(ctx, "comments")
	if err != nil {
		return nil, err
	}
	r.ActivitiesPresent, err = r.content(ctx, "activities")
	if err != nil {
		return nil, err
	}

	// Every native user ID can become a recipient through a supported mention.
	// Build exact GroupKeys without retaining unrelated User bodies.
	groups := map[string]bool{}
	if err := r.field("users").Children(ctx, func(_ reflect.Value, user metadataLogicalNode) error {
		_, err := read.withHeader(ctx, user, []string{"ID"}, func(value reflect.Value) (bool, error) {
			id := value.Interface().(domain.User).ID
			for _, category := range []string{"comments", "mentions"} {
				length := uint64(len(id)) + uint64(len(issueID)) + uint64(len(category)) + 2
				if length > (metadataWorkBytes-1024)/8 {
					return false, ErrMetadataWorkBudget
				}
				if err := read.reserve(ctx, length*8+1024); err != nil {
					return false, err
				}
				groups[id+":"+issueID+":"+category] = true
			}
			return true, nil
		})
		return err
	}); err != nil {
		return nil, err
	}
	wantedIssues := map[string]bool{}
	if err := r.ownedKey(ctx, wantedIssues, issueID); err != nil {
		return nil, err
	}
	if err := r.field("notifications").Children(ctx, func(index reflect.Value, notification metadataLogicalNode) error {
		wanted, err := read.withHeader(ctx, notification, []string{"GroupKey", "ReadAt", "DeletedAt", "ArchivedAt"}, func(value reflect.Value) (bool, error) {
			item := value.Interface().(domain.Notification)
			return groups[item.GroupKey] && item.ReadAt == nil && item.DeletedAt == nil && item.ArchivedAt == nil, nil
		})
		if err != nil || !wanted {
			return err
		}
		// Keep all ages: the callback's actual activity time supplies the
		// exact six-hour predicate, including future native timestamps.
		value, err := r.value(ctx, notification)
		if err != nil {
			return err
		}
		if err := read.reserve(ctx, 64); err != nil {
			return err
		}
		item := value.Interface().(domain.Notification)
		r.Data.Notifications = append(r.Data.Notifications, item)
		r.NotificationOrdinals = append(r.NotificationOrdinals, int(index.Int()))
		return r.ownedKey(ctx, wantedIssues, item.IssueID)
	}); err != nil {
		return nil, err
	}

	seenIssues := map[string]bool{}
	if err := r.field("issues").Children(ctx, func(index reflect.Value, issue metadataLogicalNode) error {
		wanted, err := read.withHeader(ctx, issue, []string{"ID"}, func(value reflect.Value) (bool, error) {
			id := value.Interface().(domain.Issue).ID
			if !wantedIssues[id] || seenIssues[id] {
				return false, nil
			}
			return true, r.ownedKey(ctx, seenIssues, id)
		})
		if err != nil || !wanted {
			return err
		}
		value, err := r.value(ctx, issue)
		if err != nil {
			return err
		}
		if err := read.reserve(ctx, 64); err != nil {
			return err
		}
		item := value.Interface().(domain.Issue)
		r.Data.Issues = append(r.Data.Issues, item)
		r.IssueOrdinals = append(r.IssueOrdinals, int(index.Int()))
		if item.ID == issueID {
			r.TargetIssueOrdinal = int(index.Int())
		}
		return nil
	}); err != nil {
		return nil, err
	}

	// Existing delivery callbacks inspect only ID, then assign these fields:
	// Status, UpdatedAt and (digest only) NextAttemptAt. Keep every occurrence
	// so a future generated ID can collide with any first native ID. Merge
	// only those assigned fields onto the original occurrence, never this
	// intentionally partial header as a complete delivery.
	if err := r.field("notificationDeliveries").Children(ctx, func(index reflect.Value, delivery metadataLogicalNode) error {
		value, err := r.value(ctx, delivery, "ID")
		if err != nil {
			return err
		}
		if err := read.reserve(ctx, 64); err != nil {
			return err
		}
		r.Data.NotificationDeliveries = append(r.Data.NotificationDeliveries, value.Interface().(domain.NotificationDelivery))
		r.DeliveryOrdinals = append(r.DeliveryOrdinals, int(index.Int()))
		return nil
	}); err != nil {
		return nil, err
	}
	if err := r.metadata(ctx); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *legacyDiscussionInputs) content(ctx context.Context, name string) (bool, error) {
	present := false
	err := r.field(name).MapEntries(ctx, func(key, values metadataLogicalNode) error {
		matched, err := key.StringEqual(ctx, r.ResourceKey)
		if err != nil || !matched {
			return err
		}
		value, err := r.value(ctx, values)
		if err != nil {
			return err
		}
		present = true
		index, _, _ := metadataLogicalField(name)
		target := reflect.ValueOf(r.Data).Elem().Field(index)
		target.SetMapIndex(reflect.ValueOf(r.ResourceKey), value)
		return nil
	})
	return present, err
}

func (r *legacyDiscussionInputs) metadata(ctx context.Context) error {
	read, data := r.input, r.Data
	users, err := read.keys(ctx, data.Viewer.ID)
	if err != nil {
		return err
	}
	recipients, err := read.keys(ctx, data.Viewer.ID)
	if err != nil {
		return err
	}
	teams, err := read.keys(ctx)
	if err != nil {
		return err
	}
	states, err := read.keys(ctx)
	if err != nil {
		return err
	}
	labels, err := read.keys(ctx)
	if err != nil {
		return err
	}
	projects, err := read.keys(ctx)
	if err != nil {
		return err
	}
	issues, err := read.keys(ctx, r.ResourceKey)
	if err != nil {
		return err
	}
	for _, issue := range data.Issues {
		for _, pair := range []struct {
			keys map[string]bool
			id   string
		}{
			{issues, issue.ID}, {teams, issue.Team.ID}, {states, issue.State.ID}, {users, issue.Creator.ID},
		} {
			if err := read.addKey(ctx, pair.keys, pair.id); err != nil {
				return err
			}
		}
		for _, user := range []*domain.User{issue.Assignee, issue.Delegate} {
			if user != nil {
				if err := read.addKey(ctx, users, user.ID); err != nil {
					return err
				}
			}
		}
		for _, id := range issue.SubscriberIDs {
			if err := read.addKey(ctx, recipients, id); err != nil {
				return err
			}
		}
		if issue.Project != nil {
			if err := read.addKey(ctx, projects, issue.Project.ID); err != nil {
				return err
			}
		}
		for _, label := range issue.Labels {
			if err := read.addKey(ctx, labels, label.ID); err != nil {
				return err
			}
		}
	}
	for _, comments := range data.Comments {
		for _, comment := range comments {
			if err := read.addKey(ctx, users, comment.User.ID); err != nil {
				return err
			}
			if err := read.addKey(ctx, recipients, comment.User.ID); err != nil {
				return err
			}
		}
	}
	for _, events := range data.Activities {
		for _, event := range events {
			if err := read.addKey(ctx, users, event.Actor.ID); err != nil {
				return err
			}
		}
	}
	for _, notification := range data.Notifications {
		if err := read.addKey(ctx, users, notification.Actor.ID); err != nil {
			return err
		}
		if err := read.addKey(ctx, recipients, notification.RecipientID); err != nil {
			return err
		}
	}
	if err := read.discussionUsers(ctx, users, recipients); err != nil {
		return err
	}
	for _, selection := range []struct {
		field string
		keys  map[string]bool
		names []string
	}{
		{"teams", teams, nil}, {"states", states, nil}, {"labels", labels, nil}, {"projects", projects, []string{"ID", "Name", "Icon", "Color"}},
	} {
		if err := read.selectSlice(ctx, selection.field, func(record metadataLogicalNode) (bool, error) {
			return issueHotMatches(ctx, record, "ID", selection.keys)
		}, selection.names...); err != nil {
			return err
		}
	}
	if err := read.selectMap(ctx, "teamSettings", teams, "ResolvedSummaries", "Timezone"); err != nil {
		return err
	}
	if err := r.field("threadSubscriptions").Children(ctx, func(index reflect.Value, record metadataLogicalNode) error {
		id, err := issueHotField(ctx, record, "IssueID")
		if err != nil {
			return err
		}
		matched, err := id.StringEqual(ctx, r.ResourceKey)
		if err != nil || !matched {
			return err
		}
		value, err := r.value(ctx, record)
		if err != nil {
			return err
		}
		if err := read.reserve(ctx, 64); err != nil {
			return err
		}
		subscription := value.Interface().(domain.ThreadSubscription)
		data.ThreadSubscriptions = append(data.ThreadSubscriptions, subscription)
		r.ThreadSubscriptionOrdinals = append(r.ThreadSubscriptionOrdinals, int(index.Int()))
		return read.addKey(ctx, recipients, subscription.UserID)
	}); err != nil {
		return err
	}
	if err := read.selectMap(ctx, "notificationPreferences", recipients,
		"UserID", "Inbox", "Email", "Desktop", "Chat", "EmailFormat", "ImmediateUrgent", "DelayLowPriority"); err != nil {
		return err
	}
	if err := read.selectSlice(ctx, "pushSubscriptions", func(record metadataLogicalNode) (bool, error) {
		return issueHotMatches(ctx, record, "UserID", recipients)
	}, "UserID", "Enabled"); err != nil {
		return err
	}
	// This is the legacy full callback, regardless of the native narrow flag.
	return read.selectSlice(ctx, "issueSlas", func(record metadataLogicalNode) (bool, error) {
		return issueHotMatches(ctx, record, "IssueID", issues)
	})
}

// MergeCallback patches the complete prepared source. Partial callback headers
// are never substituted for complete global collections or delivery records.
func (r *legacyDiscussionInputs) MergeCallback(ctx context.Context) (_ *metadataLogicalSource, err error) {
	if r == nil || r.input == nil || r.Data == nil {
		return nil, errMetadataLogicalClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	source := r.input.source
	ctx = metadataLogicalSourceScratchContext(metadataLogicalUnpinnedContext(ctx), source)
	original, err := r.input.store.loadLegacyDiscussionInputs(ctx, source, r.ResourceKey)
	if err != nil {
		return nil, err
	}
	defer original.Close()
	if !reflect.DeepEqual(r.IssueOrdinals, original.IssueOrdinals) || !reflect.DeepEqual(r.NotificationOrdinals, original.NotificationOrdinals) ||
		!reflect.DeepEqual(r.DeliveryOrdinals, original.DeliveryOrdinals) || !reflect.DeepEqual(r.ThreadSubscriptionOrdinals, original.ThreadSubscriptionOrdinals) ||
		r.TargetIssueOrdinal != original.TargetIssueOrdinal || r.CommentsPresent != original.CommentsPresent || r.ActivitiesPresent != original.ActivitiesPresent {
		return nil, ErrBoundedMetadataCoverage
	}
	before, next := reflect.ValueOf(*original.Data), reflect.ValueOf(*r.Data)
	for index := range before.NumField() {
		name, _, _ := strings.Cut(before.Type().Field(index).Tag.Get("json"), ",")
		switch name {
		case "comments", "activities", "notifications", "notificationDeliveries", "threadSubscriptions":
			continue
		}
		if !reflect.DeepEqual(before.Field(index).Interface(), next.Field(index).Interface()) {
			return nil, ErrBoundedMetadataCoverage
		}
	}
	frames, err := newMetadataLogicalBuilder(ctx)
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
	changes := map[string]metadataAggregateNode{}
	for _, name := range []string{"comments", "activities"} {
		index, _, _ := metadataLogicalField(name)
		oldMap, newMap := before.Field(index), next.Field(index)
		for _, key := range newMap.MapKeys() {
			if key.String() != r.ResourceKey {
				return nil, ErrBoundedMetadataCoverage
			}
		}
		if !oldMap.IsNil() && newMap.IsNil() {
			return nil, ErrBoundedMetadataCoverage
		}
		old, new := oldMap.MapIndex(reflect.ValueOf(r.ResourceKey)), newMap.MapIndex(reflect.ValueOf(r.ResourceKey))
		if old.IsValid() == new.IsValid() && oldMap.IsNil() == newMap.IsNil() &&
			(!old.IsValid() || reflect.DeepEqual(old.Interface(), new.Interface())) {
			continue
		}
		base, present, err := r.group(ctx, name)
		if err != nil {
			return nil, err
		}
		if !present {
			base, err = legacyDiscussionShape(frames, oldMap.Type().Elem(), true)
			if err != nil {
				return nil, err
			}
		}
		shape, err := legacyDiscussionShape(frames, oldMap.Type(), oldMap.IsNil() && newMap.IsNil())
		if err != nil {
			return nil, err
		}
		var group metadataAggregateNode
		if new.IsValid() {
			if !old.IsValid() {
				old = reflect.Zero(new.Type())
			}
			patch, changed, err := r.mergeGroup(ctx, frames, name, base, old, new)
			if err != nil {
				return nil, err
			}
			if changed {
				group = patch
			} else {
				group = metadataAggregateStoredNode{value: base}
			}
		}
		changes[name] = legacyDiscussionGroupPatch{base: r.field(name), shape: shape, key: r.ResourceKey, present: new.IsValid(), group: group}
	}
	for _, name := range []string{"notifications", "notificationDeliveries", "threadSubscriptions"} {
		index, _, _ := metadataLogicalField(name)
		old, new := before.Field(index), next.Field(index)
		if reflect.DeepEqual(old.Interface(), new.Interface()) {
			continue
		}
		if !old.IsNil() && new.IsNil() {
			return nil, ErrBoundedMetadataCoverage
		}
		patch, err := r.mergeFlat(ctx, frames, name, r.field(name), old, new)
		if err != nil {
			return nil, err
		}
		changes[name] = patch
	}
	if len(changes) == 0 {
		if err := source.Retain(); err != nil {
			return nil, err
		}
		return source, nil
	}
	result, err := source.Transform(ctx, func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
		if replacement, changed := changes[name]; changed {
			return replacement, true, nil
		}
		return metadataAggregateStoredNode{value: node}, false, nil
	})
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		result.Release()
		return nil, err
	}
	return result, nil
}

func legacyDiscussionShape(builder *metadataLogicalBuilder, typ reflect.Type, nilValue bool) (metadataLogicalNode, error) {
	value := reflect.Zero(typ)
	if !nilValue {
		if typ.Kind() == reflect.Map {
			value = reflect.MakeMap(typ)
		} else {
			value = reflect.MakeSlice(typ, 0, 0)
		}
	}
	return builder.writeValue(value, 0, false)
}

func (r *legacyDiscussionInputs) group(ctx context.Context, name string) (metadataLogicalNode, bool, error) {
	var result metadataLogicalNode
	found := false
	err := r.field(name).MapEntries(ctx, func(key, node metadataLogicalNode) error {
		match, err := key.StringEqual(ctx, r.ResourceKey)
		if match {
			result, found = node, true
		}
		return err
	})
	return result, found, err
}

type legacyDiscussionRecordDelta struct {
	before, next reflect.Value
	allowed      []string
}

func (r *legacyDiscussionInputs) nativeFrame(ctx context.Context, frames *metadataLogicalBuilder, value reflect.Value) (metadataLogicalNode, error) {
	cost, err := metadataAggregateNativeCost(ctx, value, 0)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	if cost > (metadataWorkBytes-4096)/8 {
		return metadataLogicalNode{}, ErrMetadataWorkBudget
	}
	if err := r.input.reserve(ctx, cost*8+4096); err != nil {
		return metadataLogicalNode{}, err
	}
	node, err := frames.writeAggregateNode(legacyDiscussionNativeRecord{value: value}, 0)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	if node.blocked || node.unavailable {
		return metadataLogicalNode{}, ErrBoundedMetadataCoverage
	}
	return node, nil
}

func (r *legacyDiscussionInputs) deltaFrame(ctx context.Context, frames *metadataLogicalBuilder, base metadataLogicalNode, delta legacyDiscussionRecordDelta) (metadataLogicalNode, error) {
	changed := map[string]reflect.Value{}
	for index := range delta.before.NumField() {
		left, right := delta.before.Field(index), delta.next.Field(index)
		if reflect.DeepEqual(left.Interface(), right.Interface()) {
			continue
		}
		name := delta.before.Type().Field(index).Name
		if !legacyDiscussionNameAllowed(delta.allowed, name) {
			return metadataLogicalNode{}, ErrBoundedMetadataCoverage
		}
		cost, err := metadataAggregateNativeCost(ctx, right, 0)
		if err != nil {
			return metadataLogicalNode{}, err
		}
		if cost > (metadataWorkBytes-4096)/8 {
			return metadataLogicalNode{}, ErrMetadataWorkBudget
		}
		if err := r.input.reserve(ctx, cost*8+4096); err != nil {
			return metadataLogicalNode{}, err
		}
		changed[name] = right
	}
	if len(changed) == 0 {
		return base, nil
	}
	node, err := frames.writeAggregateNode(legacyDiscussionNativeDelta{base: base, changed: changed}, 0)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	if node.blocked || node.unavailable {
		return metadataLogicalNode{}, ErrBoundedMetadataCoverage
	}
	return node, nil
}

func legacyDiscussionNameAllowed(names []string, name string) bool {
	for _, candidate := range names {
		if candidate == name {
			return true
		}
	}
	return false
}

func (r *legacyDiscussionInputs) mergeGroup(ctx context.Context, frames *metadataLogicalBuilder, name string, base metadataLogicalNode, before, next reflect.Value) (legacyDiscussionSlicePatch, bool, error) {
	shape, err := legacyDiscussionShape(frames, next.Type(), next.IsNil())
	if err != nil {
		return legacyDiscussionSlicePatch{}, false, err
	}
	patch := legacyDiscussionSlicePatch{base: base, shape: shape, replacements: map[int]metadataLogicalNode{}, dropped: map[int]bool{}}
	if err := r.input.reserve(ctx, uint64(before.Len()+next.Len())*256+4096); err != nil {
		return patch, false, err
	}
	if name == "activities" {
		if next.Len() < before.Len() {
			return patch, false, ErrBoundedMetadataCoverage
		}
		for index := range before.Len() {
			if !reflect.DeepEqual(before.Index(index).Interface(), next.Index(index).Interface()) {
				return patch, false, ErrBoundedMetadataCoverage
			}
		}
	} else if next.Len() < before.Len() {
		kept := 0
		for index := range before.Len() {
			if kept < next.Len() && reflect.DeepEqual(before.Index(index).Interface(), next.Index(kept).Interface()) {
				kept++
			} else {
				patch.dropped[index] = true
			}
		}
		if kept != next.Len() {
			return patch, false, ErrBoundedMetadataCoverage
		}
	} else {
		deltas := map[int]legacyDiscussionRecordDelta{}
		for index := range before.Len() {
			if next.Len() > before.Len() && !reflect.DeepEqual(before.Index(index).Interface(), next.Index(index).Interface()) {
				return patch, false, ErrBoundedMetadataCoverage
			}
			if !reflect.DeepEqual(before.Index(index).Interface(), next.Index(index).Interface()) {
				deltas[index] = legacyDiscussionRecordDelta{before: before.Index(index), next: next.Index(index), allowed: []string{"Version", "Body", "BodyData", "EditedAt", "Resolved", "ThreadSummary", "Reactions"}}
			}
		}
		if err := base.Children(ctx, func(index reflect.Value, node metadataLogicalNode) error {
			delta, changed := deltas[int(index.Int())]
			if !changed {
				return nil
			}
			replacement, err := r.deltaFrame(ctx, frames, node, delta)
			if err == nil {
				patch.replacements[int(index.Int())] = replacement
			}
			return err
		}); err != nil {
			return patch, false, err
		}
	}
	for index := before.Len(); index < next.Len(); index++ {
		if name == "activities" && !boundedIssueDiscussionEvent(next.Index(index).Interface().(domain.ActivityEvent).Type) {
			return patch, false, ErrBoundedMetadataCoverage
		}
		node, err := r.nativeFrame(ctx, frames, next.Index(index))
		if err != nil {
			return patch, false, err
		}
		patch.appended = append(patch.appended, node)
	}
	return patch, true, nil
}

func (r *legacyDiscussionInputs) mergeFlat(ctx context.Context, frames *metadataLogicalBuilder, name string, base metadataLogicalNode, before, next reflect.Value) (legacyDiscussionSlicePatch, error) {
	nilValue, err := base.isNil(ctx)
	if err != nil {
		return legacyDiscussionSlicePatch{}, err
	}
	shape, err := legacyDiscussionShape(frames, next.Type(), nilValue && next.IsNil())
	if err != nil {
		return legacyDiscussionSlicePatch{}, err
	}
	patch := legacyDiscussionSlicePatch{base: base, shape: shape, replacements: map[int]metadataLogicalNode{}, dropped: map[int]bool{}}
	if err := r.input.reserve(ctx, uint64(before.Len()+next.Len())*256+4096); err != nil {
		return patch, err
	}
	if name == "threadSubscriptions" {
		kept := 0
		for index := range before.Len() {
			if kept < next.Len() && reflect.DeepEqual(before.Index(index).Interface(), next.Index(kept).Interface()) {
				kept++
			} else {
				patch.dropped[r.ThreadSubscriptionOrdinals[index]] = true
			}
		}
		if kept != next.Len() {
			return patch, ErrBoundedMetadataCoverage
		}
		return patch, nil
	}
	if next.Len() < before.Len() {
		return patch, ErrBoundedMetadataCoverage
	}
	ordinals := r.NotificationOrdinals
	allowed := []string{"OccurrenceCount", "Actor", "LatestActorIDs", "SourceType", "SourceID", "CommentID", "ActivityID", "UpdatedAt"}
	if name == "notificationDeliveries" {
		ordinals = r.DeliveryOrdinals
		allowed = []string{"Status", "UpdatedAt", "NextAttemptAt"}
	}
	deltas := map[int]legacyDiscussionRecordDelta{}
	for index := range before.Len() {
		if reflect.DeepEqual(before.Index(index).Interface(), next.Index(index).Interface()) {
			continue
		}
		if name == "notificationDeliveries" {
			delivery := next.Index(index).Interface().(domain.NotificationDelivery)
			if delivery.Status != "pending" && delivery.Status != "digest" || delivery.UpdatedAt.IsZero() {
				return patch, ErrBoundedMetadataCoverage
			}
		}
		deltas[ordinals[index]] = legacyDiscussionRecordDelta{before: before.Index(index), next: next.Index(index), allowed: allowed}
	}
	if err := base.Children(ctx, func(index reflect.Value, node metadataLogicalNode) error {
		delta, changed := deltas[int(index.Int())]
		if !changed {
			return nil
		}
		replacement, err := r.deltaFrame(ctx, frames, node, delta)
		if err == nil {
			patch.replacements[int(index.Int())] = replacement
		}
		return err
	}); err != nil {
		return patch, err
	}
	for index := before.Len(); index < next.Len(); index++ {
		node, err := r.nativeFrame(ctx, frames, next.Index(index))
		if err != nil {
			return patch, err
		}
		patch.appended = append(patch.appended, node)
	}
	return patch, nil
}

type legacyDiscussionNativeRecord struct{ value reflect.Value }

func (n legacyDiscussionNativeRecord) nodeType() reflect.Type { return n.value.Type() }
func (n legacyDiscussionNativeRecord) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	cost, err := metadataAggregateNativeCost(ctx, n.value, 0)
	if err != nil {
		return reflect.Value{}, err
	}
	if limit < 0 || cost > uint64(limit) {
		return reflect.Value{}, ErrMetadataWorkBudget
	}
	return n.value, nil
}
func (n legacyDiscussionNativeRecord) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	for index := range n.value.NumField() {
		if err := ctx.Err(); err != nil {
			return err
		}
		value := n.value.Field(index)
		var child metadataAggregateNode = metadataAggregateNativeNode{value: value}
		if value.Type() == reflect.TypeFor[domain.User]() {
			child = legacyActorUserNode{value: value}
		}
		if err := visit(reflect.ValueOf(n.value.Type().Field(index).Name), child); err != nil {
			return err
		}
	}
	return nil
}

type legacyDiscussionNativeDelta struct {
	base    metadataLogicalNode
	changed map[string]reflect.Value
}

func (n legacyDiscussionNativeDelta) nodeType() reflect.Type { return n.base.typ }
func (n legacyDiscussionNativeDelta) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n legacyDiscussionNativeDelta) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	return n.base.Children(ctx, func(key reflect.Value, node metadataLogicalNode) error {
		var child metadataAggregateNode = metadataAggregateStoredNode{value: node}
		if value, changed := n.changed[key.String()]; changed {
			child = metadataAggregateNativeNode{value: value}
			if value.Type() == reflect.TypeFor[domain.User]() {
				child = legacyActorUserNode{value: value}
			}
		}
		return visit(key, child)
	})
}

type legacyDiscussionSlicePatch struct {
	base, shape  metadataLogicalNode
	replacements map[int]metadataLogicalNode
	dropped      map[int]bool
	appended     []metadataLogicalNode
}

func (n legacyDiscussionSlicePatch) nodeType() reflect.Type           { return n.base.typ }
func (n legacyDiscussionSlicePatch) logicalBase() metadataLogicalNode { return n.shape }
func (n legacyDiscussionSlicePatch) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n legacyDiscussionSlicePatch) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	output := 0
	err := n.base.Children(ctx, func(index reflect.Value, node metadataLogicalNode) error {
		ordinal := int(index.Int())
		if n.dropped[ordinal] {
			return nil
		}
		if replacement, changed := n.replacements[ordinal]; changed {
			node = replacement
		}
		if err := visit(reflect.ValueOf(output), metadataAggregateStoredNode{value: node}); err != nil {
			return err
		}
		output++
		return nil
	})
	if err != nil {
		return err
	}
	for _, node := range n.appended {
		if err := visit(reflect.ValueOf(output), metadataAggregateStoredNode{value: node}); err != nil {
			return err
		}
		output++
	}
	return nil
}

type legacyDiscussionGroupPatch struct {
	base, shape metadataLogicalNode
	key         string
	present     bool
	group       metadataAggregateNode
}

func (n legacyDiscussionGroupPatch) nodeType() reflect.Type           { return n.base.typ }
func (n legacyDiscussionGroupPatch) logicalBase() metadataLogicalNode { return n.shape }
func (n legacyDiscussionGroupPatch) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n legacyDiscussionGroupPatch) nodeChildren(context.Context, func(reflect.Value, metadataAggregateNode) error) error {
	return ErrMetadataRecordQuery
}
func (n legacyDiscussionGroupPatch) logicalMapEntries(ctx context.Context, visit func(metadataAggregateNode, metadataAggregateNode) error) error {
	found := false
	err := n.base.MapEntries(ctx, func(key, value metadataLogicalNode) error {
		match, err := key.StringEqual(ctx, n.key)
		if err != nil {
			return err
		}
		if match {
			found = true
			if n.present {
				return visit(metadataAggregateStoredNode{value: key}, n.group)
			}
			return nil
		}
		return visit(metadataAggregateStoredNode{value: key}, metadataAggregateStoredNode{value: value})
	})
	if err != nil {
		return err
	}
	if !found && n.present {
		return visit(metadataAggregateNativeNode{value: reflect.ValueOf(n.key)}, n.group)
	}
	return nil
}
