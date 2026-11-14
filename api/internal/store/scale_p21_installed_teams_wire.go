package store

import (
	"context"
	"strings"
	"time"

	"flow/api/internal/domain"
)

const installedTeamsWireReadMaxBytes = 64 << 20

// InstalledTeamsWireRead owns Team wire values and their encoding allowance.
// Timestamp use is restricted to JSON encoding and instant comparisons. Their
// locations and monotonic readings are deliberately not the installed values.
// Teams and its fields must not survive Close without another charged owner.
type InstalledTeamsWireRead struct {
	Teams   []domain.Team
	Found   bool
	release func()
}

func (r *InstalledTeamsWireRead) Close() error {
	if r != nil {
		r.Teams = nil
		r.Found = false
		if r.release != nil {
			r.release()
			r.release = nil
		}
	}
	return nil
}

// BeginInstalledTeamsWireRead preserves installed Team JSON bytes and encoding
// errors, not the complete native time.Time contract. It is trusted storage
// access with no visibility policy. Off invokes WorkspaceMetadata unchanged.
func (s *SQLiteStore) BeginInstalledTeamsWireRead(ctx context.Context, workspace string) (*InstalledTeamsWireRead, error) {
	if !s.BoundedMetadataEnabled() {
		data, found := s.WorkspaceMetadata(workspace)
		return &InstalledTeamsWireRead{Teams: data.Teams, Found: found}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if workspace == "" {
		workspace = s.lastWorkspaceKey
	}
	data, found := s.workspaces[workspace]
	if !found {
		return &InstalledTeamsWireRead{}, nil
	}
	cost := 4096
	// Each row covers the Team, four owned timestamps and fixed-offset
	// locations, plus conservative JSON escaping and encoder-buffer growth.
	charge := func(bytes, multiplier int) bool {
		if bytes > (installedTeamsWireReadMaxBytes-cost)/multiplier {
			return false
		}
		cost += bytes * multiplier
		return true
	}
	if !charge(len(data.Teams), 4096) {
		return nil, ErrMetadataWorkBudget
	}
	for _, team := range data.Teams {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for _, value := range []string{team.ID, team.Name, team.Key, team.Color, team.Icon, team.ExternalSource, team.ArchivedByID} {
			if !charge(len(value), 32) {
				return nil, ErrMetadataWorkBudget
			}
		}
	}
	_, release, err := s.tryAdmitAdditionalMetadataWork(ctx, cost)
	if err != nil {
		return nil, err
	}
	var teams []domain.Team
	if data.Teams != nil {
		teams = make([]domain.Team, len(data.Teams))
	}
	detachTime := func(source *time.Time) *time.Time {
		if source == nil {
			return nil
		}
		_, offset := source.Zone()
		when := source.In(time.FixedZone("", offset))
		return &when
	}
	for index, team := range data.Teams {
		team.ID = strings.Clone(team.ID)
		team.Name = strings.Clone(team.Name)
		team.Key = strings.Clone(team.Key)
		team.Color = strings.Clone(team.Color)
		team.Icon = strings.Clone(team.Icon)
		team.ExternalSource = strings.Clone(team.ExternalSource)
		team.ArchivedByID = strings.Clone(team.ArchivedByID)
		team.RetiredAt = detachTime(team.RetiredAt)
		team.ArchivedAt = detachTime(team.ArchivedAt)
		team.CreatedAt = detachTime(team.CreatedAt)
		team.UpdatedAt = detachTime(team.UpdatedAt)
		teams[index] = team
	}
	return &InstalledTeamsWireRead{Teams: teams, Found: true, release: release}, nil
}
