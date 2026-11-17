package store

import (
	"context"
	"reflect"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

type advancedScheduleImageProfile struct{}

func (advancedScheduleImageProfile) kind() scopedImageWriteKind {
	return scopedImageAdvancedSchedule
}

func (advancedScheduleImageProfile) extraFields() []string {
	return []string{"projects", "projectUpdates", "trash", "issueSlas", "slaEvents"}
}

func (advancedScheduleImageProfile) resolve(ctx context.Context, owner *ResourceRead, source *metadataLogicalSource) (MutationScope, error) {
	if advancedScheduleMaintenance(ctx) == nil {
		return MutationScope{}, ErrBoundedMetadataCoverage
	}
	scope, found := mutationScopeFromContext(ctx)
	if !found || scope.Resolve == nil || scope.Expand != nil || scope.CreateIssues || scope.StatusAutomation {
		return MutationScope{}, ErrBoundedMetadataCoverage
	}
	data, err := loadAdvancedScheduleMetadata(ctx, owner, source, false)
	if err != nil {
		return MutationScope{}, err
	}
	scope = scope.merge(scope.Resolve(data))
	scope.Resolve = nil
	return scope, nil
}

func (advancedScheduleImageProfile) inputs(ctx context.Context, owner *ResourceRead, source *metadataLogicalSource) (domain.Bootstrap, error) {
	data, err := loadAdvancedScheduleMetadata(ctx, owner, source, false)
	if err == nil {
		state := advancedScheduleMaintenance(ctx)
		if state == nil {
			return data, ErrBoundedMetadataCoverage
		}
		state.source, state.owner = source, owner
	}
	return data, err
}

func (advancedScheduleImageProfile) validate(before, after domain.Bootstrap) error {
	left, right := reflect.ValueOf(before), reflect.ValueOf(after)
	mutable := []string{"projects", "projectUpdates", "trash", "issueSlas", "slaEvents", "comments", "activities", "notifications", "notificationDeliveries"}
	for index := range left.NumField() {
		name, _, _ := strings.Cut(left.Type().Field(index).Tag.Get("json"), ",")
		if name == "-" || slices.Contains(mutable, name) {
			continue
		}
		if !reflect.DeepEqual(left.Field(index).Interface(), right.Field(index).Interface()) {
			return ErrBoundedMetadataCoverage
		}
	}
	if len(before.Projects) != len(after.Projects) || (before.Projects == nil) != (after.Projects == nil) ||
		len(before.ProjectUpdates) != len(after.ProjectUpdates) || (before.ProjectUpdates == nil) != (after.ProjectUpdates == nil) ||
		len(after.SLAEvents) < len(before.SLAEvents) || (before.SLAEvents != nil && after.SLAEvents == nil) {
		return ErrBoundedMetadataCoverage
	}
	for index := range before.Projects {
		if !cycleScheduleOnlyChanged(reflect.ValueOf(before.Projects[index]), reflect.ValueOf(after.Projects[index]), []string{"Health"}) {
			return ErrBoundedMetadataCoverage
		}
	}
	for key, old := range before.ProjectUpdates {
		next, found := after.ProjectUpdates[key]
		if !found || len(old) != len(next) || (old == nil) != (next == nil) {
			return ErrBoundedMetadataCoverage
		}
		for index := range old {
			allowed := []string{}
			if index == 0 {
				allowed = []string{"DueAt", "Missing"}
			}
			if !cycleScheduleOnlyChanged(reflect.ValueOf(old[index]), reflect.ValueOf(next[index]), allowed) {
				return ErrBoundedMetadataCoverage
			}
		}
	}
	if !reflect.DeepEqual(before.SLAEvents, after.SLAEvents[:len(before.SLAEvents)]) &&
		!(len(before.SLAEvents) == 0 && len(after.SLAEvents) > 0) {
		return ErrBoundedMetadataCoverage
	}
	for key, comments := range after.Comments {
		if previous, found := before.Comments[key]; !found || !reflect.DeepEqual(previous, comments) {
			return ErrBoundedMetadataCoverage
		}
	}
	// applySLARules owns IssueSLAs and the existing exact activity/notification
	// helpers. It never changes issues, comments or unrelated directories.
	return nil
}

func (advancedScheduleImageProfile) reserveCallback(ctx context.Context, owner *ResourceRead, data domain.Bootstrap, payload any) error {
	state := advancedScheduleMaintenance(ctx)
	if state == nil || payload != nil {
		return ErrBoundedMetadataCoverage
	}
	if state.first != nil {
		_ = state.first.Close()
	}
	state.first, state.err, state.calls = nil, nil, 0
	if err := reserveCycleScheduleClone(ctx, owner, data); err != nil {
		return err
	}
	state.before = cloneCallbackInput(data)
	// Two streamed fingerprints retain fixed buffers/spools. Callback output
	// owns only actual recipient candidates, not Projects times all Users.
	if err := owner.reserve(ctx, 512<<10); err != nil {
		return err
	}
	owner.mu.Lock()
	owner.release = append(owner.release, func() {
		if state.first != nil {
			_ = state.first.Close()
			state.first = nil
		}
		state.before, state.source, state.owner = domain.Bootstrap{}, nil, nil
	})
	owner.mu.Unlock()
	cost, err := advancedScheduleCallbackWork(ctx, owner, data)
	if err != nil {
		return err
	}
	return owner.reserve(ctx, cost)
}

func advancedScheduleRuleMultiplicity(ctx context.Context, owner *ResourceRead, rules []domain.SLARule) (uint64, error) {
	scratch := &ResourceRead{store: owner.store}
	defer scratch.Close()
	if err := scratch.reserve(ctx, 64<<10); err != nil {
		return 0, err
	}
	counts := map[string]uint64{}
	var maximum uint64
	for _, rule := range rules {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if counts[rule.ID] == 0 {
			if uint64(len(rule.ID)) > (metadataWorkBytes-1024)/8 {
				return 0, ErrMetadataWorkBudget
			}
			if err := scratch.reserve(ctx, uint64(len(rule.ID))*8+1024); err != nil {
				return 0, err
			}
		}
		counts[rule.ID]++
		maximum = max(maximum, counts[rule.ID])
	}
	return maximum, nil
}

func advancedScheduleCallbackWork(ctx context.Context, owner *ResourceRead, data domain.Bootstrap) (uint64, error) {
	// Existing headers may be copied during append growth; mutable native
	// containers share immutable strings, matching the existing clone helper.
	cost, err := cycleScheduleCloneValueWork(ctx, reflect.ValueOf([]any{
		data.IssueSLAs, data.SLAEvents, data.Activities, data.Notifications, data.NotificationDeliveries,
	}), false, 0)
	if err != nil {
		return 0, err
	}
	add := func(count, unit, width uint64) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if count == 0 {
			return nil
		}
		if width > metadataWorkBytes/64 {
			return ErrMetadataWorkBudget
		}
		unit += width * 64
		if unit > metadataWorkBytes || count > (metadataWorkBytes-cost)/unit {
			return ErrMetadataWorkBudget
		}
		cost += count * unit
		return nil
	}
	slaUnit := uint64(reflect.TypeFor[domain.IssueSLA]().Size()+2*reflect.TypeFor[domain.SLAEvent]().Size()+
		reflect.TypeFor[domain.ActivityEvent]().Size())*8 + 16<<10
	noticeUnit := uint64(reflect.TypeFor[domain.Notification]().Size()+
		4*reflect.TypeFor[domain.NotificationDelivery]().Size())*8 + 4<<10
	var rules uint64
	settings, _ := data.Settings["sla"].(map[string]any)
	if enabled, configured := settings["enabled"].(bool); !configured || enabled {
		rules, err = advancedScheduleRuleMultiplicity(ctx, owner, data.SLARules)
		if err != nil {
			return 0, err
		}
	}
	for _, issue := range data.Issues {
		// Only the winning ID can emit lifecycle events. Its duplicate rule
		// occurrences can restart or pause that SLA repeatedly, each adding
		// at most two events and one activity. Breach recipients are subscribers.
		if err := add(rules, slaUnit, uint64(len(issue.ID))); err != nil {
			return 0, err
		}
		for _, recipient := range issue.SubscriberIDs {
			if err := add(rules, noticeUnit, uint64(len(issue.ID))+uint64(len(recipient))); err != nil {
				return 0, err
			}
		}
	}
	for _, project := range data.Projects {
		for _, recipient := range project.MemberIDs {
			if err := add(1, noticeUnit, uint64(len(project.ID))+uint64(len(recipient))); err != nil {
				return 0, err
			}
		}
		if project.Lead != nil {
			if err := add(1, noticeUnit, uint64(len(project.ID))+uint64(len(project.Lead.ID))); err != nil {
				return 0, err
			}
		} else if len(project.MemberIDs) == 0 {
			if err := add(1, noticeUnit, uint64(len(project.ID))+uint64(len(data.Viewer.ID))); err != nil {
				return 0, err
			}
		}
	}
	return cost, nil
}

type advancedScheduleMapPatch struct {
	base         metadataLogicalNode
	replacements map[string]metadataAggregateNode
	owner        *ResourceRead
}

func (p advancedScheduleMapPatch) nodeType() reflect.Type           { return p.base.typ }
func (p advancedScheduleMapPatch) logicalBase() metadataLogicalNode { return p.base }
func (p advancedScheduleMapPatch) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, p, limit)
}
func (p advancedScheduleMapPatch) nodeChildren(context.Context, func(reflect.Value, metadataAggregateNode) error) error {
	return ErrMetadataRecordQuery
}
func (p advancedScheduleMapPatch) logicalMapEntries(ctx context.Context, visit func(metadataAggregateNode, metadataAggregateNode) error) error {
	return p.base.MapEntries(ctx, func(key, value metadataLogicalNode) error {
		name, err := cycleScheduleNativeValue(ctx, p.owner, key)
		if err != nil {
			return err
		}
		var item metadataAggregateNode = metadataAggregateStoredNode{value}
		if replacement, changed := p.replacements[name.String()]; changed {
			item = replacement
		}
		return visit(metadataAggregateStoredNode{key}, item)
	})
}

func (profile advancedScheduleImageProfile) image(ctx context.Context, owner *ResourceRead, source *metadataLogicalSource, before, after domain.Bootstrap) (*metadataLogicalSource, error) {
	if err := profile.validate(before, after); err != nil {
		return nil, err
	}
	state := advancedScheduleMaintenance(ctx)
	if state == nil {
		return nil, ErrBoundedMetadataCoverage
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
	frame := func(node metadataAggregateNode) (metadataLogicalNode, error) {
		value, err := frames.writeAggregateNode(node, 0)
		if err == nil && (value.blocked || value.unavailable) {
			return value, ErrBoundedMetadataCoverage
		}
		return value, err
	}
	replacements := map[string]metadataAggregateNode{}
	for _, name := range []string{"projects", "trash", "slaEvents"} {
		index, _, _ := metadataLogicalField(name)
		base := source.fields[index]
		var old, next reflect.Value
		switch name {
		case "projects":
			old, next = reflect.ValueOf(before.Projects), reflect.ValueOf(after.Projects)
		case "trash":
			old, next = reflect.ValueOf(before.Trash), reflect.ValueOf(after.Trash)
		case "slaEvents":
			old, next = reflect.ValueOf(before.SLAEvents), reflect.ValueOf(after.SLAEvents)
		}
		if reflect.DeepEqual(old.Interface(), next.Interface()) {
			continue
		}
		shape, err := legacyDiscussionShape(frames, base.typ, next.IsNil())
		if err != nil {
			return nil, err
		}
		patch := legacyDiscussionSlicePatch{base: base, shape: shape, replacements: map[int]metadataLogicalNode{}, dropped: map[int]bool{}}
		seen, output := 0, 0
		err = base.Children(ctx, func(ordinal reflect.Value, record metadataLogicalNode) error {
			position := int(ordinal.Int())
			if position != seen || position >= old.Len() {
				return ErrBoundedMetadataCoverage
			}
			seen++
			switch name {
			case "projects":
				if before.Projects[position].Health != after.Projects[position].Health {
					replacement, err := frame(legacyDiscussionNativeDelta{base: record, changed: map[string]reflect.Value{
						"Health": reflect.ValueOf(after.Projects[position].Health),
					}})
					if err != nil {
						return err
					}
					patch.replacements[position] = replacement
				}
			case "trash":
				if output < next.Len() && reflect.DeepEqual(old.Index(position).Interface(), next.Index(output).Interface()) {
					output++
				} else if advancedScheduleExpired(before.Trash[position], state) {
					patch.dropped[position] = true
				} else {
					return ErrBoundedMetadataCoverage
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		if seen != old.Len() || name == "trash" && output != next.Len() {
			return nil, ErrBoundedMetadataCoverage
		}
		if name == "slaEvents" {
			for position := old.Len(); position < next.Len(); position++ {
				value, err := frame(legacyDiscussionNativeRecord{next.Index(position)})
				if err != nil {
					return nil, err
				}
				patch.appended = append(patch.appended, value)
			}
		}
		replacements[name] = patch
	}
	if !reflect.DeepEqual(before.ProjectUpdates, after.ProjectUpdates) {
		index, _, _ := metadataLogicalField("projectUpdates")
		base := source.fields[index]
		patch := advancedScheduleMapPatch{base: base, replacements: map[string]metadataAggregateNode{}, owner: owner}
		err = base.MapEntries(ctx, func(key, list metadataLogicalNode) error {
			name, err := cycleScheduleNativeValue(ctx, owner, key)
			if err != nil {
				return err
			}
			old, next := before.ProjectUpdates[name.String()], after.ProjectUpdates[name.String()]
			if reflect.DeepEqual(old, next) {
				return nil
			}
			slice := legacyDiscussionSlicePatch{base: list, shape: list, replacements: map[int]metadataLogicalNode{}, dropped: map[int]bool{}}
			err = list.Children(ctx, func(ordinal reflect.Value, record metadataLogicalNode) error {
				if ordinal.Int() != 0 {
					return nil
				}
				value, err := frame(legacyDiscussionNativeDelta{base: record, changed: map[string]reflect.Value{
					"DueAt": reflect.ValueOf(next[0].DueAt), "Missing": reflect.ValueOf(next[0].Missing),
				}})
				if err == nil {
					slice.replacements[0] = value
				}
				return err
			})
			patch.replacements[name.String()] = slice
			return err
		})
		if err != nil {
			return nil, err
		}
		replacements["projectUpdates"] = patch
	}
	if !reflect.DeepEqual(before.IssueSLAs, after.IssueSLAs) {
		replacements["issueSlas"] = metadataAggregateNativeNode{reflect.ValueOf(after.IssueSLAs)}
	}
	return source.Transform(ctx, func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
		if replacement, changed := replacements[name]; changed {
			return replacement, true, nil
		}
		return metadataAggregateStoredNode{node}, false, nil
	})
}

func (s *SQLiteStore) mutateBoundedAdvancedSchedule(ctx context.Context, workspace, event string, payload any, mutate func(*domain.Bootstrap) (string, error)) error {
	state := advancedScheduleMaintenance(ctx)
	if state == nil || event != "schedules.maintained" || payload != nil {
		return ErrBoundedMetadataCoverage
	}
	return s.mutateBoundedScopedImage(ctx, workspace, event, payload, advancedScheduleImageProfile{}, func(data *domain.Bootstrap) (string, error) {
		aggregate, err := mutate(data)
		if state.err != nil {
			return "", state.err
		}
		return aggregate, err
	})
}
