package store

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"
)

type cycleScheduleMaintenanceKey struct{}

// ContextWithCycleScheduleMaintenance selects the literal ticker callback and
// carries its already captured clock into callback work admission.
func ContextWithCycleScheduleMaintenance(ctx context.Context, now time.Time) context.Context {
	return context.WithValue(ctx, cycleScheduleMaintenanceKey{}, now)
}

func cycleScheduleMaintenance(ctx context.Context) (time.Time, bool) {
	now, found := ctx.Value(cycleScheduleMaintenanceKey{}).(time.Time)
	return now, found
}

type CycleScheduleRead struct {
	Data  domain.Bootstrap
	owner *ResourceRead
	once  sync.Once
}

func (r *CycleScheduleRead) Close() {
	if r != nil {
		r.once.Do(func() {
			r.Data = domain.Bootstrap{}
			r.owner.Close()
			r.owner = nil
		})
	}
}

// BeginCycleScheduleRead preserves the installed physical/default source and
// occurrence order, retaining only the ticker's preflight scalar inputs.
func (s *SQLiteStore) BeginCycleScheduleRead(ctx context.Context, workspace string) (_ *CycleScheduleRead, found bool, err error) {
	if !s.BoundedMetadataEnabled() {
		data, found := s.WorkspaceMetadataFields(workspace, "teams", "cycleSettings", "cycles")
		if !found {
			return nil, false, nil
		}
		return &CycleScheduleRead{Data: data}, true, nil
	}
	owner, found, err := s.BeginInstalledMetadataRead(ctx, workspace)
	if err != nil || !found {
		return nil, found, err
	}
	defer owner.Close()
	source, err := owner.retainSource(ctx, owner.workspace)
	if err != nil {
		return nil, true, err
	}
	read := &CycleScheduleRead{owner: &ResourceRead{store: s, source: source}}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	read.Data, err = loadCycleScheduleMetadata(ctx, read.owner, source, true)
	if err != nil {
		return nil, true, err
	}
	return read, true, nil
}

// The source decides nil/empty collection shapes and every duplicate ordinal.
// These values are callback headers; the complete native source stays intact.
func loadCycleScheduleMetadata(ctx context.Context, owner *ResourceRead, source *metadataLogicalSource, preflight bool) (domain.Bootstrap, error) {
	var data domain.Bootstrap
	if err := owner.reserve(ctx, 64<<10); err != nil {
		return data, err
	}
	if !preflight {
		for _, name := range []string{"workspace", "viewer", "viewerRole"} {
			index, _, _ := metadataLogicalField(name)
			value, err := cycleScheduleNativeValue(ctx, owner, source.fields[index])
			if err != nil {
				return data, err
			}
			reflect.ValueOf(&data).Elem().Field(index).Set(value)
		}
	}
	for _, name := range []string{"teams", "cycles", "cycleSettings"} {
		index, _, _ := metadataLogicalField(name)
		node := source.fields[index]
		missing, err := node.isNil(ctx)
		if err != nil {
			return data, err
		}
		if missing {
			continue
		}
		switch name {
		case "teams":
			data.Teams = []domain.Team{}
			err = node.Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
				if err := owner.reserve(ctx, uint64(reflect.TypeFor[domain.Team]().Size())*2+64); err != nil {
					return err
				}
				value, err := cycleScheduleProjectNode(ctx, owner, record, "ID")
				if err == nil {
					data.Teams = append(data.Teams, value.Interface().(domain.Team))
				}
				return err
			})
		case "cycles":
			data.Cycles = []domain.Cycle{}
			names := []string{"TeamID", "Status", "StartsAt", "EndsAt"}
			if !preflight {
				names = append(names, "ID", "Number", "UpdatedAt")
			}
			err = node.Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
				if err := owner.reserve(ctx, uint64(reflect.TypeFor[domain.Cycle]().Size())*2+64); err != nil {
					return err
				}
				value, err := cycleScheduleProjectNode(ctx, owner, record, names...)
				if err == nil {
					data.Cycles = append(data.Cycles, value.Interface().(domain.Cycle))
				}
				return err
			})
		case "cycleSettings":
			data.CycleSettings = map[string]domain.CycleSettings{}
			names := []string{"Enabled", "AutoCreate"}
			if !preflight {
				names = append(names, "AutoMigrate", "DurationWeeks", "CooldownWeeks", "UpcomingCount")
			}
			err = node.MapEntries(ctx, func(key, record metadataLogicalNode) error {
				ownedKey, err := cycleScheduleNativeValue(ctx, owner, key)
				if err != nil {
					return err
				}
				if err := owner.reserve(ctx, uint64(reflect.TypeFor[domain.CycleSettings]().Size())*2+64); err != nil {
					return err
				}
				value, err := cycleScheduleProjectNode(ctx, owner, record, names...)
				if err == nil {
					data.CycleSettings[ownedKey.String()] = value.Interface().(domain.CycleSettings)
				}
				return err
			})
		}
		if err != nil {
			return data, err
		}
	}
	return data, ctx.Err()
}

type cycleScheduleImageProfile struct{}

func (cycleScheduleImageProfile) kind() scopedImageWriteKind {
	return scopedImageCycleSchedule
}

func (cycleScheduleImageProfile) extraFields() []string {
	return []string{"cycles"}
}

// Run the actual server-supplied resolver against this attempt's locked source.
// The installed preflight is never carried forward as write authority.
func (cycleScheduleImageProfile) resolve(ctx context.Context, owner *ResourceRead, source *metadataLogicalSource) (MutationScope, error) {
	if _, selected := cycleScheduleMaintenance(ctx); !selected {
		return MutationScope{}, ErrBoundedMetadataCoverage
	}
	scope, found := mutationScopeFromContext(ctx)
	if !found || scope.Resolve == nil || scope.AllIssues || scope.CreateIssues || scope.Expand != nil || scope.StatusAutomation {
		return MutationScope{}, ErrBoundedMetadataCoverage
	}
	data, err := loadCycleScheduleMetadata(ctx, owner, source, false)
	if err != nil {
		return MutationScope{}, err
	}
	scope = scope.merge(scope.Resolve(data))
	scope.Resolve = nil
	return scope, nil
}

func (cycleScheduleImageProfile) inputs(ctx context.Context, owner *ResourceRead, source *metadataLogicalSource) (domain.Bootstrap, error) {
	return loadCycleScheduleMetadata(ctx, owner, source, false)
}

func cycleScheduleOnlyChanged(before, after reflect.Value, allowed []string) bool {
	for index := range before.NumField() {
		if !slices.Contains(allowed, before.Type().Field(index).Name) &&
			!reflect.DeepEqual(before.Field(index).Interface(), after.Field(index).Interface()) {
			return false
		}
	}
	return true
}

func (cycleScheduleImageProfile) validate(before, after domain.Bootstrap) error {
	left, right := reflect.ValueOf(before), reflect.ValueOf(after)
	for index := range left.NumField() {
		name, _, _ := strings.Cut(left.Type().Field(index).Tag.Get("json"), ",")
		if name == "-" || name == "cycles" || name == "issues" || name == "activities" {
			continue
		}
		if !reflect.DeepEqual(left.Field(index).Interface(), right.Field(index).Interface()) {
			return ErrBoundedMetadataCoverage
		}
	}
	if len(after.Cycles) < len(before.Cycles) || len(after.Issues) != len(before.Issues) ||
		before.Cycles != nil && after.Cycles == nil {
		return ErrBoundedMetadataCoverage
	}
	for index := range before.Cycles {
		if !cycleScheduleOnlyChanged(reflect.ValueOf(before.Cycles[index]), reflect.ValueOf(after.Cycles[index]),
			[]string{"Status", "StartsAt", "EndsAt", "UpdatedAt"}) {
			return ErrBoundedMetadataCoverage
		}
	}
	for _, cycle := range after.Cycles[len(before.Cycles):] {
		empty := domain.Cycle{Resources: []domain.CycleResource{}}
		if cycle.ID == "" || !slices.ContainsFunc(before.Teams, func(team domain.Team) bool { return team.ID == cycle.TeamID }) ||
			!cycleScheduleOnlyChanged(reflect.ValueOf(empty), reflect.ValueOf(cycle),
				[]string{"ID", "Number", "Name", "TeamID", "StartsAt", "EndsAt", "Status", "CreatedAt", "UpdatedAt"}) {
			return ErrBoundedMetadataCoverage
		}
	}
	for index := range before.Issues {
		if !cycleScheduleOnlyChanged(reflect.ValueOf(before.Issues[index]), reflect.ValueOf(after.Issues[index]),
			[]string{"CycleID", "UpdatedAt"}) {
			return ErrBoundedMetadataCoverage
		}
	}
	// RecordIssueActivity owns the entire scoped activity contract: it may
	// append, merge a recent update, delete a cancelled merge, or suppress it.
	return nil
}

func cycleScheduleInputDue(data domain.Bootstrap, teamID string, now time.Time) bool {
	settings := data.CycleSettings[teamID]
	if !settings.Enabled || !settings.AutoCreate {
		return false
	}
	for _, cycle := range data.Cycles {
		if cycle.TeamID == teamID && cycle.Status == "current" {
			end := cycle.EndsAt.UTC()
			day := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, time.UTC)
			return !now.Before(day.AddDate(0, 0, 1))
		}
	}
	var next *domain.Cycle
	for index := range data.Cycles {
		cycle := &data.Cycles[index]
		if cycle.TeamID == teamID && cycle.Status == "upcoming" && (next == nil || cycle.StartsAt.Before(next.StartsAt)) {
			next = cycle
		}
	}
	return next == nil || !next.StartsAt.After(now)
}

func (cycleScheduleImageProfile) reserveCallback(ctx context.Context, owner *ResourceRead, data domain.Bootstrap, payload any) error {
	now, selected := cycleScheduleMaintenance(ctx)
	if !selected {
		return ErrBoundedMetadataCoverage
	}
	// An advanced team creates at most one missing next cycle plus the
	// requested upcoming count. Charge before the literal append loop; an
	// extreme legal setting is refused by the existing work budget.
	if err := owner.reserve(ctx, 4096); err != nil {
		return err
	}
	charged := map[string]bool{}
	transitions := uint64(0)
	for _, team := range data.Teams {
		if !cycleScheduleInputDue(data, team.ID, now) {
			continue
		}
		transitions++
		if charged[team.ID] {
			continue
		}
		// The first advance leaves a current cycle whose end day is not
		// over, or an upcoming cycle after now. Duplicate headers retain
		// callback order, but cannot generate a second batch for this ID.
		if err := owner.reserve(ctx, 512); err != nil {
			return err
		}
		charged[team.ID] = true
		wanted := data.CycleSettings[team.ID].UpcomingCount
		if wanted < 1 {
			wanted = 2
		}
		unit := uint64(reflect.TypeFor[domain.Cycle]().Size())*8 + 8192
		if uint64(len(team.ID)) > (metadataWorkBytes-unit)/16 {
			return ErrMetadataWorkBudget
		}
		unit += uint64(len(team.ID)) * 16
		count := uint64(wanted) + 1 + uint64(len(data.Teams))
		if count >= metadataWorkBytes/unit {
			return ErrMetadataWorkBudget
		}
		if err := owner.reserve(ctx, count*unit); err != nil {
			return err
		}
	}
	if transitions == 0 {
		return ctx.Err()
	}
	// Appending may replace the existing header backing array. Strings are
	// shared; charge the copied native slots rather than encoding cycle bodies.
	cycleUnit := uint64(reflect.TypeFor[domain.Cycle]().Size()) * 2
	if uint64(len(data.Cycles)) > (metadataWorkBytes-128)/cycleUnit {
		return ErrMetadataWorkBudget
	}
	if err := owner.reserve(ctx, uint64(len(data.Cycles))*cycleUnit+128); err != nil {
		return err
	}
	// Shared cycle IDs can move one issue more than once across due teams.
	// Own every possible activity/map/slice update, plus the largest temporary
	// description-before hash buffer used by the unchanged history merger.
	hashBytes := uint64(0)
	for _, issue := range data.Issues {
		unit := uint64(reflect.TypeFor[domain.ActivityEvent]().Size())*8 + 8192
		events := data.Activities[issue.ID]
		slots := uint64(len(events)) + transitions
		eventSize := uint64(reflect.TypeFor[domain.ActivityEvent]().Size()) * 2
		if slots > (metadataWorkBytes-unit)/eventSize {
			return ErrMetadataWorkBudget
		}
		unit += slots * eventSize
		metadataEntries := uint64(2)
		for _, event := range events {
			metadataEntries = max(metadataEntries, uint64(len(event.Metadata))+2)
			hashBytes = max(hashBytes, uint64(len(event.Metadata["descriptionBefore"])))
		}
		if metadataEntries > (metadataWorkBytes-unit)/1024 {
			return ErrMetadataWorkBudget
		}
		unit += metadataEntries * 1024
		if uint64(len(issue.ID)) > (metadataWorkBytes-unit)/16 {
			return ErrMetadataWorkBudget
		}
		unit += uint64(len(issue.ID)) * 16
		if transitions > metadataWorkBytes/unit {
			return ErrMetadataWorkBudget
		}
		if err := owner.reserve(ctx, unit*transitions); err != nil {
			return err
		}
	}
	if hashBytes > metadataWorkBytes-256 {
		return ErrMetadataWorkBudget
	}
	if err := owner.reserve(ctx, hashBytes+256); err != nil {
		return err
	}
	return ctx.Err()
}

// Patch existing native occurrences, preserving every omitted body and field.
// Appended cycles are the complete records produced by the literal callback.
func (cycleScheduleImageProfile) image(ctx context.Context, owner *ResourceRead, source *metadataLogicalSource, before, after domain.Bootstrap) (*metadataLogicalSource, error) {
	if err := (cycleScheduleImageProfile{}).validate(before, after); err != nil {
		return nil, err
	}
	if reflect.DeepEqual(before.Cycles, after.Cycles) {
		if err := source.Retain(); err != nil {
			return nil, err
		}
		return source, nil
	}
	if uint64(len(after.Cycles)) > (metadataWorkBytes-4096)/512 {
		return nil, ErrMetadataWorkBudget
	}
	if err := owner.reserve(ctx, uint64(len(after.Cycles))*512+4096); err != nil {
		return nil, err
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
	field, _, _ := metadataLogicalField("cycles")
	base := source.fields[field]
	shape, err := legacyDiscussionShape(frames, reflect.TypeFor[[]domain.Cycle](), after.Cycles == nil)
	if err != nil {
		return nil, err
	}
	patch := legacyDiscussionSlicePatch{base: base, shape: shape, replacements: map[int]metadataLogicalNode{}, dropped: map[int]bool{}}
	seen := 0
	err = base.Children(ctx, func(index reflect.Value, record metadataLogicalNode) error {
		ordinal := int(index.Int())
		if ordinal != seen || ordinal >= len(before.Cycles) {
			return ErrBoundedMetadataCoverage
		}
		seen++
		left, right := before.Cycles[ordinal], after.Cycles[ordinal]
		if reflect.DeepEqual(left, right) {
			return nil
		}
		changed := map[string]reflect.Value{}
		for _, name := range []string{"Status", "StartsAt", "EndsAt", "UpdatedAt"} {
			oldValue, newValue := reflect.ValueOf(left).FieldByName(name), reflect.ValueOf(right).FieldByName(name)
			if !reflect.DeepEqual(oldValue.Interface(), newValue.Interface()) {
				changed[name] = newValue
			}
		}
		value, err := frames.writeAggregateNode(legacyDiscussionNativeDelta{base: record, changed: changed}, 0)
		if err != nil {
			return err
		}
		if value.blocked || value.unavailable {
			return ErrBoundedMetadataCoverage
		}
		patch.replacements[ordinal] = value
		return nil
	})
	if err != nil {
		return nil, err
	}
	if seen != len(before.Cycles) {
		return nil, ErrBoundedMetadataCoverage
	}
	for _, cycle := range after.Cycles[len(before.Cycles):] {
		value := reflect.ValueOf(cycle)
		cost, err := metadataAggregateNativeCost(ctx, value, 0)
		if err != nil {
			return nil, err
		}
		if cost > (metadataWorkBytes-4096)/8 {
			return nil, ErrMetadataWorkBudget
		}
		if err := owner.reserve(ctx, cost*8+4096); err != nil {
			return nil, err
		}
		node, err := frames.writeAggregateNode(legacyDiscussionNativeRecord{value: value}, 0)
		if err != nil {
			return nil, err
		}
		if node.blocked || node.unavailable {
			return nil, ErrBoundedMetadataCoverage
		}
		patch.appended = append(patch.appended, node)
	}
	return source.Transform(ctx, func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
		if name == "cycles" {
			return patch, true, nil
		}
		return metadataAggregateStoredNode{value: node}, false, nil
	})
}

func (s *SQLiteStore) mutateBoundedCycleSchedule(ctx context.Context, workspace, event string, payload any, mutate func(*domain.Bootstrap) (string, error)) error {
	if _, selected := cycleScheduleMaintenance(ctx); !selected || event != "cycles.automatically_maintained" || payload != nil {
		return ErrBoundedMetadataCoverage
	}
	return s.mutateBoundedScopedImage(ctx, workspace, event, payload, cycleScheduleImageProfile{}, mutate)
}
