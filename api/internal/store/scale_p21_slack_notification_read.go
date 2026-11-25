package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"

	"flow/api/internal/domain"
)

type SlackNotificationTeam struct {
	TeamID string
	Key    string
}

// SlackNotificationRead owns one installed Slack routing generation. Data is
// partial rendering input; keep it open until every resulting post finishes.
type SlackNotificationRead struct {
	Data   domain.Bootstrap
	source *InstalledMetadataRead
	owner  *ResourceRead
	once   sync.Once
}

func (r *SlackNotificationRead) Close() {
	if r != nil {
		r.once.Do(func() {
			r.Data = domain.Bootstrap{}
			if r.source != nil {
				r.source.Close()
				r.source = nil
			}
			if r.owner != nil {
				r.owner.Close()
				r.owner = nil
			}
		})
	}
}

// BeginSlackNotificationRead preserves installed order and physical/default
// workspace selection. Only the first connected, OAuth-authorized Slack
// connection is retained; configs and unrelated providers are never decoded.
func (s *SQLiteStore) BeginSlackNotificationRead(ctx context.Context, workspace string) (_ *SlackNotificationRead, found bool, err error) {
	if !s.BoundedMetadataEnabled() {
		return nil, false, ErrBoundedMetadataCoverage
	}
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	source, found, err := s.BeginInstalledMetadataRead(ctx, workspace)
	if err != nil || !found {
		return nil, found, err
	}
	read := &SlackNotificationRead{source: source, owner: &ResourceRead{store: s}}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	if err = read.owner.reserve(ctx, 64<<10); err != nil {
		return nil, true, err
	}
	err = source.source.VisitFields(ctx, func(name string, node metadataLogicalNode) error {
		switch name {
		case "workspace":
			value, err := read.owner.projectNode(ctx, node, "URLKey")
			if err == nil {
				read.Data.Workspace = value.Interface().(domain.Workspace)
			}
			return err
		case "integrationConnections":
			err := node.Children(ctx, func(_ reflect.Value, connection metadataLogicalNode) error {
				var slack, connected, authorized bool
				if err := connection.Children(ctx, func(key reflect.Value, value metadataLogicalNode) error {
					var err error
					switch key.String() {
					case "Provider":
						slack, err = value.StringEqual(ctx, "slack")
					case "Status":
						connected, err = value.StringEqual(ctx, "connected")
					case "OAuthAuthorized":
						var decoded reflect.Value
						decoded, err = value.Value(ctx, 4096)
						if err == nil {
							authorized = decoded.Bool()
						}
					}
					return err
				}); err != nil {
					return err
				}
				if !slack || !connected || !authorized {
					return nil
				}
				value, err := read.owner.projectNode(ctx, connection, "ID")
				if err != nil {
					return err
				}
				selected := value.Interface().(domain.IntegrationConnection)
				selected.Provider, selected.Status, selected.OAuthAuthorized = "slack", "connected", true
				read.Data.IntegrationConnections = []domain.IntegrationConnection{selected}
				return ErrMetadataIterationDone
			})
			if errors.Is(err, ErrMetadataIterationDone) {
				err = nil
			}
			return err
		}
		return nil
	})
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return nil, true, err
	}
	return read, true, nil
}

// LoadTarget selects the first exact ID only. Slugs and previous slugs do not
// participate in domain-event aggregate lookup.
func (r *SlackNotificationRead) LoadTarget(ctx context.Context, kind, id string) error {
	if r == nil || r.source == nil || r.owner == nil {
		return errMetadataLogicalClosed
	}
	if kind != "project" && kind != "initiative" {
		return ErrMetadataRecordQuery
	}
	return r.source.source.VisitFields(ctx, func(name string, collection metadataLogicalNode) error {
		if name != kind+"s" {
			return nil
		}
		err := collection.Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
			matched := false
			if err := record.Children(ctx, func(key reflect.Value, value metadataLogicalNode) error {
				if key.String() == "ID" {
					var err error
					matched, err = value.StringEqual(ctx, id)
					return err
				}
				return nil
			}); err != nil || !matched {
				return err
			}
			fields := []string{"ID", "Name", "SlugID", "TeamIDs"}
			if kind == "initiative" {
				fields = []string{"ID", "Name", "SlugID", "LeadTeamID", "ContributingTeamIDs"}
			}
			value, err := r.owner.projectNode(ctx, record, fields...)
			if err != nil {
				return err
			}
			if kind == "project" {
				r.Data.Projects = []domain.Project{value.Interface().(domain.Project)}
			} else {
				r.Data.Initiatives = []domain.Initiative{value.Interface().(domain.Initiative)}
			}
			return ErrMetadataIterationDone
		})
		if errors.Is(err, ErrMetadataIterationDone) {
			err = nil
		}
		return err
	})
}

// LoadTeams retains only requested settings map keys and notification flags.
// A nonempty channel ID bypasses the unused channel name, as the caller does.
func (r *SlackNotificationRead) LoadTeams(ctx context.Context, requests []SlackNotificationTeam) error {
	if r == nil || r.source == nil || r.owner == nil {
		return errMetadataLogicalClosed
	}
	return r.source.source.VisitFields(ctx, func(name string, collection metadataLogicalNode) error {
		if name != "teamSettings" || len(requests) == 0 {
			return nil
		}
		return collection.MapEntries(ctx, func(key, record metadataLogicalNode) error {
			id, wanted := "", false
			for _, request := range requests {
				matched, err := key.StringEqual(ctx, request.TeamID)
				if err != nil {
					return err
				}
				if matched {
					id, wanted = request.TeamID, true
					break
				}
			}
			if !wanted {
				return nil
			}
			if uint64(len(id)) > (metadataWorkBytes-4096)/8 {
				return ErrMetadataWorkBudget
			}
			if err := r.owner.reserve(ctx, uint64(len(id))*8+4096); err != nil {
				return err
			}
			value, err := r.owner.projectNode(ctx, record, "SlackChannelID")
			if err != nil {
				return err
			}
			settings := value.Interface().(domain.TeamSettings)
			if settings.SlackChannelID == "" {
				value, err := r.owner.projectNode(ctx, record, "SlackChannelName")
				if err != nil {
					return err
				}
				settings.SlackChannelName = value.Interface().(domain.TeamSettings).SlackChannelName
			}
			if err := record.Children(ctx, func(key reflect.Value, flags metadataLogicalNode) error {
				if key.String() != "SlackNotifications" {
					return nil
				}
				return flags.MapEntries(ctx, func(key, enabled metadataLogicalNode) error {
					for _, request := range requests {
						if request.TeamID != id {
							continue
						}
						matched, err := key.StringEqual(ctx, request.Key)
						if err != nil || !matched {
							if err != nil {
								return err
							}
							continue
						}
						if uint64(len(request.Key)) > (metadataWorkBytes-256)/8 {
							return ErrMetadataWorkBudget
						}
						if err := r.owner.reserve(ctx, uint64(len(request.Key))*8+256); err != nil {
							return err
						}
						value, err := enabled.Value(ctx, 4096)
						if err != nil {
							return err
						}
						if settings.SlackNotifications == nil {
							settings.SlackNotifications = map[string]bool{}
						}
						settings.SlackNotifications[strings.Clone(request.Key)] = value.Bool()
						break
					}
					return nil
				})
			}); err != nil {
				return err
			}
			if r.Data.TeamSettings == nil {
				r.Data.TeamSettings = map[string]domain.TeamSettings{}
			}
			r.Data.TeamSettings[strings.Clone(id)] = settings
			return nil
		})
	})
}

// ReservePosts admits all outgoing goroutines and request/response encoding
// before the caller launches any of them. The same read holds this allowance
// until its final asynchronous post has completed.
func (r *SlackNotificationRead) ReservePosts(ctx context.Context, count int, inputBytes uint64) error {
	if r == nil || r.source == nil || r.owner == nil {
		return errMetadataLogicalClosed
	}
	const postWork = 64 << 10
	if count < 0 || uint64(count) > metadataWorkBytes/postWork {
		return ErrMetadataWorkBudget
	}
	cost := uint64(count) * postWork
	if inputBytes > (metadataWorkBytes-cost)/8 {
		return ErrMetadataWorkBudget
	}
	return r.owner.reserve(ctx, cost+inputBytes*8)
}
