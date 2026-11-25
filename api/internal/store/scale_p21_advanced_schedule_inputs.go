package store

import (
	"context"
	"reflect"
	"time"

	"flow/api/internal/domain"
)

type advancedScheduleMaintenanceKey struct{}

// ContextWithAdvancedScheduleMaintenance carries the ticker's captured clock
// and the already configured trash retention into its closed write profile.
func ContextWithAdvancedScheduleMaintenance(ctx context.Context, now time.Time, retention time.Duration) context.Context {
	ctx = context.WithValue(ctx, advancedScheduleStreamKey{}, true)
	return context.WithValue(ctx, advancedScheduleMaintenanceKey{}, &advancedScheduleState{now: now, retention: retention})
}

func advancedScheduleMaintenance(ctx context.Context) *advancedScheduleState {
	state, _ := ctx.Value(advancedScheduleMaintenanceKey{}).(*advancedScheduleState)
	return state
}

type AdvancedScheduleRead struct {
	Data  domain.Bootstrap
	owner *ResourceRead
}

func (r *AdvancedScheduleRead) Close() {
	if r != nil && r.owner != nil {
		r.Data = domain.Bootstrap{}
		r.owner.Close()
		r.owner = nil
	}
}

func (s *SQLiteStore) BeginAdvancedScheduleRead(ctx context.Context, workspace string) (_ *AdvancedScheduleRead, found bool, err error) {
	if !s.BoundedMetadataEnabled() {
		return nil, false, ErrBoundedMetadataCoverage
	}
	installed, found, err := s.BeginInstalledMetadataRead(ctx, workspace)
	if err != nil || !found {
		return nil, found, err
	}
	defer installed.Close()
	source, err := installed.retainSource(ctx, installed.workspace)
	if err != nil {
		return nil, true, err
	}
	read := &AdvancedScheduleRead{owner: &ResourceRead{store: s, source: source}}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	read.Data, err = loadAdvancedScheduleMetadata(ctx, read.owner, source, true)
	return read, true, err
}

func advancedScheduleExpired(entry domain.TrashEntry, state *advancedScheduleState) bool {
	return state.now.After(entry.ExpiresAt) ||
		!entry.DeletedAt.IsZero() && state.now.After(entry.DeletedAt.Add(state.retention))
}

// Project/update bodies and retained trash payloads stay in their original
// frames. Every occurrence and nil/empty shape still has a callback slot.
func loadAdvancedScheduleMetadata(ctx context.Context, owner *ResourceRead, source *metadataLogicalSource, preflight bool) (domain.Bootstrap, error) {
	var data domain.Bootstrap
	if err := owner.reserve(ctx, 64<<10); err != nil {
		return data, err
	}
	names := []string{"workspace", "issueSlas", "slaRules"}
	if !preflight {
		names = append(names, "viewer", "viewerRole", "users", "states", "labels", "teamSettings", "notificationPreferences", "pushSubscriptions")
	}
	for _, name := range names {
		index, _, _ := metadataLogicalField(name)
		value, err := cycleScheduleNativeValue(ctx, owner, source.fields[index])
		if err != nil {
			return data, err
		}
		reflect.ValueOf(&data).Elem().Field(index).Set(value)
	}
	settingsIndex, _, _ := metadataLogicalField("settings")
	settingsMissing, err := source.fields[settingsIndex].isNil(ctx)
	if err != nil {
		return data, err
	}
	if !settingsMissing {
		data.Settings = map[string]any{}
	}
	if err := source.fields[settingsIndex].MapEntries(ctx, func(key, node metadataLogicalNode) error {
		for _, name := range []string{"sla", "projectUpdates"} {
			match, err := key.StringEqual(ctx, name)
			if err != nil {
				return err
			}
			if match {
				value, err := cycleScheduleNativeValue(ctx, owner, node)
				if err != nil {
					return err
				}
				data.Settings[name] = value.Interface()
			}
		}
		return nil
	}); err != nil {
		return data, err
	}
	projectIndex, _, _ := metadataLogicalField("projects")
	missing, err := source.fields[projectIndex].isNil(ctx)
	if err != nil {
		return data, err
	}
	if !missing {
		data.Projects = []domain.Project{}
	}
	err = scopedImageVisit(ctx, source, "projects", func(_ int, node metadataLogicalNode) error {
		fields := []string{"ID", "CreatedAt", "UpdateCadence", "UpdateSchedule", "Status", "Health"}
		if !preflight {
			fields = append(fields, "MemberIDs", "Lead")
		}
		value, err := cycleScheduleProjectNode(ctx, owner, node, fields...)
		if err == nil {
			data.Projects = append(data.Projects, value.Interface().(domain.Project))
		}
		return err
	})
	if err != nil {
		return data, err
	}
	updateIndex, _, _ := metadataLogicalField("projectUpdates")
	missing, err = source.fields[updateIndex].isNil(ctx)
	if err != nil {
		return data, err
	}
	if !missing {
		data.ProjectUpdates = map[string][]domain.ProjectUpdate{}
	}
	err = source.fields[updateIndex].MapEntries(ctx, func(key, list metadataLogicalNode) error {
		name, err := cycleScheduleNativeValue(ctx, owner, key)
		if err != nil {
			return err
		}
		var updates []domain.ProjectUpdate
		missing, err := list.isNil(ctx)
		if err != nil {
			return err
		}
		if !missing {
			updates = []domain.ProjectUpdate{}
		}
		err = list.Children(ctx, func(ordinal reflect.Value, node metadataLogicalNode) error {
			if err := owner.reserve(ctx, uint64(reflect.TypeFor[domain.ProjectUpdate]().Size())*2+64); err != nil {
				return err
			}
			var update domain.ProjectUpdate
			if ordinal.Int() == 0 {
				value, err := cycleScheduleProjectNode(ctx, owner, node, "CreatedAt", "DueAt", "Missing")
				if err != nil {
					return err
				}
				update = value.Interface().(domain.ProjectUpdate)
			}
			updates = append(updates, update)
			return nil
		})
		data.ProjectUpdates[name.String()] = updates
		return err
	})
	if err != nil {
		return data, err
	}
	trashIndex, _, _ := metadataLogicalField("trash")
	missing, err = source.fields[trashIndex].isNil(ctx)
	if err != nil {
		return data, err
	}
	if !missing {
		data.Trash = []domain.TrashEntry{}
	}
	err = scopedImageVisit(ctx, source, "trash", func(_ int, node metadataLogicalNode) error {
		value, err := cycleScheduleProjectNode(ctx, owner, node, "DeletedAt", "ExpiresAt")
		if err != nil {
			return err
		}
		entry := value.Interface().(domain.TrashEntry)
		if !preflight {
			state := advancedScheduleMaintenance(ctx)
			if state == nil {
				return ErrBoundedMetadataCoverage
			}
			if advancedScheduleExpired(entry, state) {
				value, err = cycleScheduleNativeValue(ctx, owner, node)
				if err != nil {
					return err
				}
				entry = value.Interface().(domain.TrashEntry)
			}
		}
		data.Trash = append(data.Trash, entry)
		return nil
	})
	if err != nil {
		return data, err
	}
	if !preflight {
		index, _, _ := metadataLogicalField("slaEvents")
		missing, err := source.fields[index].isNil(ctx)
		if err != nil {
			return data, err
		}
		if !missing {
			data.SLAEvents = []domain.SLAEvent{}
		}
		err = source.fields[index].Children(ctx, func(_ reflect.Value, _ metadataLogicalNode) error {
			if err := owner.reserve(ctx, uint64(reflect.TypeFor[domain.SLAEvent]().Size())*2+64); err != nil {
				return err
			}
			data.SLAEvents = append(data.SLAEvents, domain.SLAEvent{})
			return nil
		})
		if err != nil {
			return data, err
		}
	}
	return data, ctx.Err()
}
