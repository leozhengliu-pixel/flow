package store

import (
	"context"
	"errors"
	"reflect"
	"time"

	"flow/api/internal/domain"
)

// ChatDisplayRequest names display inputs, never authority. Empty user and team
// IDs still select their exact installed map keys. Policy adds only bindability
// headers; its team timestamps are presence sentinels, never source dates.
type ChatDisplayRequest struct {
	Directory                 bool
	Policy                    bool
	UserID, TeamID, ReviewRef string
}

// ChatDisplayRead owns its rendering projection through the final encoding.
// Data is incomplete and must never be used as a mutation preimage.
type ChatDisplayRead struct {
	Data   domain.Bootstrap
	source *InstalledMetadataRead
	owner  *ResourceRead
}

func (r *ChatDisplayRead) Close() {
	if r != nil {
		r.Data = domain.Bootstrap{}
		if r.source != nil {
			r.source.Close()
			r.source = nil
		}
		if r.owner != nil {
			r.owner.Close()
			r.owner = nil
		}
	}
}

// TransferOutput moves this handle into the containing operation's lifetime.
// A by-value rendering result without that lifetime fails before it escapes.
func (r *ChatDisplayRead) TransferOutput(ctx context.Context) error {
	if r == nil || !registerMetadataOutputClose(ctx, r.Close) {
		r.Close()
		return recordMetadataOutputError(ctx, ErrBoundedMetadataCoverage)
	}
	return nil
}

// BeginChatDisplayRead preserves installed user order and duplicate identities,
// exact settings keys, and the first review matching ID or current slug.
func (s *SQLiteStore) BeginChatDisplayRead(ctx context.Context, workspace string, request ChatDisplayRequest) (_ *ChatDisplayRead, found bool, err error) {
	if !s.BoundedMetadataEnabled() {
		return nil, false, ErrBoundedMetadataCoverage
	}
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	source, found, err := s.BeginInstalledMetadataRead(ctx, workspace)
	if err != nil || !found {
		return nil, found, err
	}
	r := &ChatDisplayRead{source: source, owner: &ResourceRead{store: s}}
	defer func() {
		if err != nil {
			r.Close()
		}
	}()
	if err = r.owner.reserve(ctx, 64<<10); err != nil {
		return nil, true, err
	}
	if request.Policy {
		r.Data.Teams = []domain.Team{}
		r.Data.TeamSettings = map[string]domain.TeamSettings{}
	}
	err = source.source.VisitFields(ctx, func(name string, node metadataLogicalNode) error {
		switch name {
		case "workspaceSettings":
			value, err := r.owner.projectNode(ctx, node, "HIPAACompliance")
			if err == nil {
				r.Data.WorkspaceSettings = value.Interface().(domain.WorkspaceSettings)
			}
			return err
		case "users":
			if !request.Directory {
				return nil
			}
			nilSlice, err := node.isNil(ctx)
			if err != nil {
				return err
			}
			if !nilSlice {
				r.Data.Users = []domain.User{}
			}
			return node.Children(ctx, func(_ reflect.Value, user metadataLogicalNode) error {
				value, err := r.owner.projectNode(ctx, user, "ID", "Name", "DisplayName", "Username", "Email")
				if err == nil {
					r.Data.Users = append(r.Data.Users, value.Interface().(domain.User))
				}
				return err
			})
		case "teams":
			if !request.Policy {
				return nil
			}
			return node.Children(ctx, func(_ reflect.Value, team metadataLogicalNode) error {
				value, err := r.owner.projectNode(ctx, team, "ID", "Private")
				if err != nil {
					return err
				}
				item := value.Interface().(domain.Team)
				if err := team.Children(ctx, func(key reflect.Value, value metadataLogicalNode) error {
					if key.String() != "ArchivedAt" && key.String() != "RetiredAt" {
						return nil
					}
					absent, err := value.isNil(ctx)
					if err != nil || absent {
						return err
					}
					if key.String() == "ArchivedAt" {
						item.ArchivedAt = new(time.Time)
					} else {
						item.RetiredAt = new(time.Time)
					}
					return nil
				}); err != nil {
					return err
				}
				r.Data.Teams = append(r.Data.Teams, item)
				return nil
			})
		case "userSettings", "teamSettings":
			id, fields := request.UserID, []string{"Language", "Timezone"}
			if name == "teamSettings" {
				id, fields = request.TeamID, []string{"TeamID", "Timezone"}
			}
			return node.MapEntries(ctx, func(key, settings metadataLogicalNode) error {
				mapID := id
				matched, err := key.StringEqual(ctx, id)
				policy := request.Policy && name == "teamSettings"
				if err != nil || !matched && !policy {
					return err
				}
				selected := fields
				if policy {
					selected = []string{"ParentTeamID", "Access", "MembershipRestriction"}
					if matched {
						selected = append(selected, fields...)
					}
					if key.cost > (metadataWorkBytes-256)/8 {
						return ErrMetadataWorkBudget
					}
					if err := r.owner.reserve(ctx, key.cost*8+256); err != nil {
						return err
					}
					value, err := key.Value(ctx, int(key.cost))
					if err != nil {
						return err
					}
					mapID = value.String()
				}
				value, err := r.owner.projectNode(ctx, settings, selected...)
				if err == nil {
					if name == "userSettings" {
						r.Data.UserSettings = map[string]domain.UserSettings{id: value.Interface().(domain.UserSettings)}
					} else {
						if r.Data.TeamSettings == nil {
							r.Data.TeamSettings = map[string]domain.TeamSettings{}
						}
						r.Data.TeamSettings[mapID] = value.Interface().(domain.TeamSettings)
					}
				}
				return err
			})
		case "reviews":
			return r.review(ctx, node, request.ReviewRef)
		}
		return nil
	})
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return nil, true, err
	}
	return r, true, nil
}

// LoadReview adds a reference discovered after live authority checks without
// replacing the installed generation used for settings and display names.
func (r *ChatDisplayRead) LoadReview(ctx context.Context, ref string) error {
	if r == nil || r.source == nil || r.owner == nil {
		return errMetadataLogicalClosed
	}
	return r.source.source.VisitFields(ctx, func(name string, node metadataLogicalNode) error {
		if name == "reviews" {
			return r.review(ctx, node, ref)
		}
		return nil
	})
}

func (r *ChatDisplayRead) review(ctx context.Context, node metadataLogicalNode, ref string) error {
	if ref == "" {
		return nil
	}
	err := node.Children(ctx, func(_ reflect.Value, review metadataLogicalNode) error {
		matched := false
		if err := review.Children(ctx, func(key reflect.Value, value metadataLogicalNode) error {
			if key.String() != "ID" && key.String() != "SlugID" {
				return nil
			}
			equal, err := value.StringEqual(ctx, ref)
			matched = matched || equal
			return err
		}); err != nil || !matched {
			return err
		}
		value, err := r.owner.projectNode(ctx, review, "ID", "SlugID", "Title")
		if err != nil {
			return err
		}
		r.Data.Reviews = []domain.CodeReview{value.Interface().(domain.CodeReview)}
		return ErrMetadataIterationDone
	})
	if errors.Is(err, ErrMetadataIterationDone) {
		err = nil
	}
	return err
}
