package store

import (
	"context"
	"reflect"
	"sync"

	"flow/api/internal/domain"
)

// TeamMaintenanceRead owns the ticker's partial installed teams/settings view.
// Only Team.ID and automation settings are populated; Projects stays nil.
// Keep the read open through the entire sweep, including issue mutations.
type TeamMaintenanceRead struct {
	Data    domain.Bootstrap
	release []func()
	once    sync.Once
}

func (r *TeamMaintenanceRead) Close() {
	if r != nil {
		r.once.Do(func() {
			r.Data = domain.Bootstrap{}
			for _, release := range r.release {
				release()
			}
			r.release = nil
		})
	}
}

// BeginTeamMaintenanceRead preserves the installed accessor's physical/default
// workspace selection. It never reloads or normalizes canonical metadata.
func (s *SQLiteStore) BeginTeamMaintenanceRead(ctx context.Context, workspace string) (_ *TeamMaintenanceRead, found bool, err error) {
	if !s.BoundedMetadataEnabled() {
		data, found := s.WorkspaceMetadataFields(workspace, "teams", "teamSettings")
		if !found {
			return nil, false, nil
		}
		return &TeamMaintenanceRead{Data: data}, true, nil
	}
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	owner, found, err := s.BeginInstalledMetadataRead(ctx, workspace)
	if err != nil || !found {
		return nil, found, err
	}
	defer owner.Close()
	source, err := owner.retainSource(ctx, owner.workspace)
	if err != nil {
		return nil, true, err
	}
	defer source.Release()
	read := &TeamMaintenanceRead{}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	reserve := func(cost uint64) error {
		if cost > metadataWorkBytes {
			return ErrMetadataWorkBudget
		}
		_, release, err := s.tryAdmitAdditionalMetadataWork(ctx, int(cost))
		if err == nil {
			read.release = append(read.release, release)
		}
		return err
	}
	if err := reserve(64 << 10); err != nil {
		return nil, true, err
	}
	needed := map[string]string{}
	var maxKeyCost uint64
	teams, _, _ := metadataLogicalField("teams")
	err = source.fields[teams].Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
		return record.Children(ctx, func(key reflect.Value, node metadataLogicalNode) error {
			if key.String() != "ID" {
				return nil
			}
			if node.cost > (metadataWorkBytes-4096)/8 {
				return ErrMetadataWorkBudget
			}
			if err := reserve(node.cost*8 + 4096); err != nil {
				return err
			}
			value, err := node.Value(ctx, int(node.cost))
			if err != nil {
				return err
			}
			id := value.String()
			read.Data.Teams = append(read.Data.Teams, domain.Team{ID: id})
			needed[id] = id
			maxKeyCost = max(maxKeyCost, node.cost)
			return nil
		})
	})
	if err != nil {
		return nil, true, err
	}
	if len(needed) == 0 {
		return read, true, nil
	}
	settings, _, _ := metadataLogicalField("teamSettings")
	fields := []string{"AutoCloseStale", "StaleMonths", "AutoArchiveMonths", "StaleStatusID"}
	err = source.fields[settings].MapEntries(ctx, func(key, record metadataLogicalNode) error {
		if key.cost > maxKeyCost {
			return nil
		}
		if key.cost > (metadataWorkBytes-4096)/4 {
			return ErrMetadataWorkBudget
		}
		_, releaseKey, err := s.tryAdmitAdditionalMetadataWork(ctx, int(key.cost)*4+4096)
		if err != nil {
			return err
		}
		defer releaseKey()
		decoded, err := key.Value(ctx, int(key.cost))
		if err != nil {
			return err
		}
		id, wanted := needed[decoded.String()]
		if wanted {
			cost := uint64(4096)
			if err := record.Children(ctx, func(key reflect.Value, node metadataLogicalNode) error {
				for _, name := range fields {
					if key.String() == name {
						if node.cost > (metadataWorkBytes-cost)/8 {
							return ErrMetadataWorkBudget
						}
						cost += node.cost * 8
					}
				}
				return nil
			}); err != nil {
				return err
			}
			if err := reserve(cost); err != nil {
				return err
			}
			value := domain.TeamSettings{}
			target := reflect.ValueOf(&value).Elem()
			if err := record.Children(ctx, func(key reflect.Value, node metadataLogicalNode) error {
				for _, name := range fields {
					if key.String() == name {
						value, err := node.Value(ctx, int(cost))
						if err != nil {
							return err
						}
						target.FieldByName(name).Set(value)
					}
				}
				return nil
			}); err != nil {
				return err
			}
			if read.Data.TeamSettings == nil {
				read.Data.TeamSettings = map[string]domain.TeamSettings{}
			}
			read.Data.TeamSettings[id] = value
			return nil
		}
		return nil
	})
	if err != nil {
		return nil, true, err
	}
	return read, true, nil
}
