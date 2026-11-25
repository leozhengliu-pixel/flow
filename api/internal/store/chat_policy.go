package store

import (
	"context"
	"strings"
	"sync"
	"time"
	"unsafe"

	"flow/api/internal/domain"
)

const scaleP21SW19ChatPolicyMaxWork = 64 << 20

// ChatTeamPolicyRead owns the policy headers and their work allowance. The
// trusted caller must retain it while constructing and encoding its result.
type ChatTeamPolicyRead struct {
	data    domain.Bootstrap
	release func()
	once    sync.Once
}

// Metadata exposes only bindability inputs. ArchivedAt and RetiredAt are
// owned zero-time sentinels for presence, not the source dates or timezones.
// Callers must not encode these partial Team values as complete records.
func (r *ChatTeamPolicyRead) Metadata() domain.Bootstrap { return r.data }

func (r *ChatTeamPolicyRead) Close() {
	if r != nil {
		r.once.Do(r.release)
	}
}

// BeginChatTeamPolicyRead snapshots only the resident scalars used to decide
// whether a team is bindable. It does not authorize a viewer or load bodies.
func (s *SQLiteStore) BeginChatTeamPolicyRead(ctx context.Context, workspace string) (*ChatTeamPolicyRead, bool, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		s.mu.RLock()
		if workspace == "" {
			workspace = s.lastWorkspaceKey
		}
		source, found := s.workspaces[workspace]
		cost, err := scaleP21SW19ChatPolicyCost(ctx, source)
		s.mu.RUnlock()
		if !found {
			return nil, false, nil
		}
		if err != nil {
			return nil, true, err
		}
		var admitted context.Context
		var release func()
		parent, nested := ctx.Value(metadataWorkLeaseKey{}).(*metadataWorkLease)
		if nested && s.metadataResidency != nil && parent.budget == s.metadataResidency.work {
			// Retained output must stay charged after its caller releases
			// an incoming lease; never wait behind that caller's own work.
			admitted, release, err = s.tryAdmitAdditionalMetadataWork(ctx, cost)
		} else {
			admitted, release, err = s.admitMetadataWork(ctx, cost)
		}
		if err != nil {
			return nil, true, err
		}
		// Never wait for admission while holding the store lock. Recheck
		// the current source before allocating its caller-owned snapshot.
		s.mu.RLock()
		source, found = s.workspaces[workspace]
		current, err := scaleP21SW19ChatPolicyCost(admitted, source)
		if !found || err != nil || current > cost {
			s.mu.RUnlock()
			release()
			if !found || err != nil {
				return nil, found, err
			}
			continue
		}
		data := domain.Bootstrap{
			Teams:        make([]domain.Team, len(source.Teams)),
			TeamSettings: make(map[string]domain.TeamSettings, len(source.TeamSettings)),
		}
		data.WorkspaceSettings.HIPAACompliance = source.WorkspaceSettings.HIPAACompliance
		for index, team := range source.Teams {
			data.Teams[index] = domain.Team{
				ID: strings.Clone(team.ID), Private: team.Private,
				ArchivedAt: scaleP21SW19PresenceTime(team.ArchivedAt),
				RetiredAt:  scaleP21SW19PresenceTime(team.RetiredAt),
			}
		}
		for id, settings := range source.TeamSettings {
			data.TeamSettings[strings.Clone(id)] = domain.TeamSettings{
				ParentTeamID:          strings.Clone(settings.ParentTeamID),
				Access:                strings.Clone(settings.Access),
				MembershipRestriction: strings.Clone(settings.MembershipRestriction),
			}
		}
		s.mu.RUnlock()
		if err := admitted.Err(); err != nil {
			release()
			return nil, true, err
		}
		return &ChatTeamPolicyRead{data: data, release: release}, true, nil
	}
}

func scaleP21SW19PresenceTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	return new(time.Time)
}

func scaleP21SW19ChatPolicyCost(ctx context.Context, data domain.Bootstrap) (int, error) {
	// Charge concrete typed storage, map slack/buckets, cloned strings and
	// presence sentinels, output IDs and the helpers' longest ancestry set.
	cost := int64(2<<20) + int64(unsafe.Sizeof(ChatTeamPolicyRead{}))
	cost += int64(len(data.Teams)) * (int64(unsafe.Sizeof(domain.Team{})) + 320)
	cost += int64(len(data.TeamSettings)) * (2*int64(unsafe.Sizeof(domain.TeamSettings{})) + 256)
	add := func(size int64) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		cost += size
		if cost > scaleP21SW19ChatPolicyMaxWork {
			return ErrMetadataWorkBudget
		}
		return nil
	}
	if err := add(0); err != nil {
		return 0, err
	}
	for _, team := range data.Teams {
		// JSON escapes each source byte to at most six bytes, including
		// invalid UTF-8. Allow 32x for encoding growth and retained output,
		// in addition to the snapshot's own cloned ID bytes.
		if err := add(33*int64(len(team.ID)) + 2*int64(unsafe.Sizeof(time.Time{}))); err != nil {
			return 0, err
		}
	}
	for id, settings := range data.TeamSettings {
		if err := add(int64(len(id)) + int64(len(settings.ParentTeamID)) + int64(len(settings.Access)) + int64(len(settings.MembershipRestriction))); err != nil {
			return 0, err
		}
	}
	return int(cost), nil
}
