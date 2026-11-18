package store

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"
)

const teamMemberRequestMaxWork = 64 << 20

// TeamMemberRequestRead owns the small membership-handler input through response
// encoding. Team contains only ID/Key; Data contains workspace identity and,
// for development GETs, the selected team's complete wire membership rows.
type TeamMemberRequestRead struct {
	Data     domain.Bootstrap
	Team     domain.Team
	used     int
	reserved int
	releases []func()
	once     sync.Once
}

func (r *TeamMemberRequestRead) Close() {
	if r != nil {
		r.once.Do(func() {
			r.Data, r.Team = domain.Bootstrap{}, domain.Team{}
			for _, release := range r.releases {
				release()
			}
			r.releases = nil
		})
	}
}

func (r *TeamMemberRequestRead) charge(ctx context.Context, s *SQLiteStore, cost int) error {
	if cost < 0 || cost > teamMemberRequestMaxWork-r.used {
		return ErrMetadataWorkBudget
	}
	wanted := r.used + cost
	if wanted > r.reserved {
		additional := min(teamMemberRequestMaxWork-r.reserved, max(128<<10, wanted-r.reserved))
		_, release, err := s.tryAdmitAdditionalMetadataWork(ctx, additional)
		if err != nil {
			return err
		}
		r.releases = append(r.releases, release)
		r.reserved += additional
	}
	r.used = wanted
	return nil
}

func (r *TeamMemberRequestRead) scalar(ctx context.Context, s *SQLiteStore, record metadataLogicalNode, field string) (reflect.Value, error) {
	node, err := metadataAggregateChild(ctx, metadataAggregateStoredNode{record}, field)
	if err != nil {
		return reflect.Value{}, err
	}
	if node == nil {
		return reflect.Value{}, ErrBoundedMetadataCoverage
	}
	part := node.(metadataAggregateStoredNode).value
	if part.cost > teamMemberRequestMaxWork/16 {
		return reflect.Value{}, ErrMetadataWorkBudget
	}
	if err := r.charge(ctx, s, int(part.cost)*16); err != nil {
		return reflect.Value{}, err
	}
	return part.Value(ctx, int(part.cost))
}

// BeginTeamMemberRequestRead preserves the public team directory's order and
// visibility without constructing its unrelated resource projection. A nil
// access is trusted development-mode storage, never an authorization bypass
// for an authenticated caller. Unknown team errors retain found=true.
func (s *SQLiteStore) BeginTeamMemberRequestRead(ctx context.Context, workspace, ref string, access *AccessContext, developmentMembers bool) (_ *TeamMemberRequestRead, found bool, err error) {
	if !s.BoundedMetadataEnabled() {
		return nil, false, ErrBoundedMetadataCoverage
	}
	if access != nil {
		if err := access.checkBinding(s); err != nil {
			return nil, false, err
		}
		if workspace != access.policy.workspace {
			return nil, false, ErrAuthForbidden
		}
		if err := s.ValidateAccess(ctx, *access); err != nil {
			return nil, false, err
		}
	}
	snapshot, snapshotted := ctx.Value(workspaceReadKey{}).(workspaceRead)
	explicitSnapshot := access != nil && snapshotted && snapshot.database == s.db && (workspace == "" || snapshot.workspace == workspace)
	selectedWorkspace := workspace
	var source *metadataLogicalSource
	if !explicitSnapshot {
		owner, found, err := s.BeginInstalledMetadataRead(ctx, workspace)
		if err != nil || !found {
			return nil, found, err
		}
		defer owner.Close()
		source, err = owner.retainSource(ctx, owner.workspace)
		if err != nil {
			return nil, true, err
		}
		defer source.Release()
		selectedWorkspace = owner.workspace
	}
	read := &TeamMemberRequestRead{}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	if err := read.charge(ctx, s, 4096); err != nil {
		return nil, true, err
	}
	policy := domain.Bootstrap{}
	if explicitSnapshot {
		// workspaceReadSource gives an explicit parent snapshot precedence,
		// including its uncertified/canonical fallback and unknown revision.
		policy, err = s.teamMemberRequestSnapshotPolicy(ctx, snapshot.metadata, read)
	} else {
		// Without an explicit SQL read, publicMetadata reads the installed
		// workspace even when its revision is unknown. Do not substitute the
		// canonical policy selected by ResolveAccess for this source.
		policy, err = s.teamMemberRequestPolicy(ctx, source, read, access != nil)
	}
	if err != nil {
		return nil, true, err
	}
	if access != nil {
		if policy.Workspace.ID != access.WorkspaceID {
			origin := metadataLogicalOrigin{workspace: selectedWorkspace, stamp: copyStamp{workspaceID: policy.Workspace.ID}}
			if err := s.validateMetadataLogicalOrigin(ctx, origin, access.WorkspaceID); err != nil {
				return nil, true, ErrAuthForbidden
			}
		}
		policy.Viewer, policy.ViewerRole = access.policy.metadata.Viewer, access.policy.metadata.ViewerRole
		policy.TeamMembers, err = s.teamMemberRequestMemberships(ctx, selectedWorkspace, policy.Workspace.ID, access.PrincipalID, read)
		if err != nil {
			return nil, true, err
		}
	}
	visible := func(team domain.Team) bool {
		if access == nil {
			return true
		}
		return teamVisibleToUser(policy, team.ID, access.PrincipalID, policy.ViewerRole) &&
			(!access.policy.selected() || slices.Contains(access.policy.key.TeamIDs, team.ID))
	}
	index := slices.IndexFunc(policy.Teams, func(team domain.Team) bool { return visible(team) && team.ID == ref })
	if index < 0 {
		index = slices.IndexFunc(policy.Teams, func(team domain.Team) bool { return visible(team) && strings.EqualFold(team.Key, ref) })
	}
	if index < 0 {
		if access != nil {
			if err := s.ValidateAccess(ctx, *access); err != nil {
				return nil, true, err
			}
		}
		return nil, true, sql.ErrNoRows
	}
	team := policy.Teams[index]
	if err := read.charge(ctx, s, 4096+32*(len(policy.Workspace.ID)+len(team.ID)+len(team.Key))); err != nil {
		return nil, true, err
	}
	read.Data.Workspace.ID = strings.Clone(policy.Workspace.ID)
	read.Team = domain.Team{ID: strings.Clone(team.ID), Key: strings.Clone(team.Key)}
	if access == nil && developmentMembers {
		memberIndex, _, _ := metadataLogicalField("teamMembers")
		err = source.fields[memberIndex].Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
			node, err := metadataAggregateChild(ctx, metadataAggregateStoredNode{record}, "TeamID")
			if err != nil || node == nil {
				return errors.Join(err, ErrBoundedMetadataCoverage)
			}
			match, err := node.(metadataAggregateStoredNode).value.StringEqual(ctx, read.Team.ID)
			if err != nil || !match {
				return err
			}
			if record.cost > teamMemberRequestMaxWork/16 {
				return ErrMetadataWorkBudget
			}
			if err := read.charge(ctx, s, 1024+int(record.cost)*16); err != nil {
				return err
			}
			value, err := record.WireValue(ctx, int(record.cost))
			if err != nil {
				return err
			}
			read.Data.TeamMembers = append(read.Data.TeamMembers, value.Interface().(domain.TeamMember))
			return nil
		})
		if err != nil {
			return nil, true, err
		}
	}
	if access != nil {
		if err := s.ValidateAccess(ctx, *access); err != nil {
			return nil, true, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, true, err
	}
	return read, true, nil
}

// publicMetadata ignores the entire ListTeamMembers result on any SQL/Scan
// error, including errors in another person's row. Validate every original
// column before retaining only this principal's team/role scalars.
func (s *SQLiteStore) teamMemberRequestMemberships(ctx context.Context, workspace, workspaceID, principal string, read *TeamMemberRequestRead) ([]domain.TeamMember, error) {
	var tx *sqlTx
	if snapshot, ok := ctx.Value(workspaceReadKey{}).(workspaceRead); ok && snapshot.database == s.db && snapshot.workspace == workspace {
		tx = snapshot.reader
		if tx == nil {
			return nil, nil
		}
	} else {
		var err error
		tx, err = s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
		if err != nil {
			return nil, nil
		}
		defer tx.Rollback()
	}
	length := func(column string) string {
		if s.dialect == "sqlite" {
			return "LENGTH(CAST(" + column + " AS BLOB))"
		}
		return "OCTET_LENGTH(" + column + ")"
	}
	match, params := "m.user_id=?", []any{principal, workspaceID}
	switch s.dialect {
	case "mysql":
		match = "CAST(m.user_id AS BINARY)=CAST(? AS BINARY)"
	case "postgres":
		// PostgreSQL text parameters normalize NULs. Such a raw Go ID can
		// never equal the stored text in ListTeamMembers' Go predicate.
		if strings.ContainsRune(principal, '\x00') || strings.ToValidUTF8(principal, "") != principal {
			match, params = "1=0", []any{workspaceID}
		}
	}
	source := ` FROM (SELECT m.*,CASE WHEN ` + match + ` THEN 1 ELSE 0 END target_match FROM team_memberships m WHERE m.workspace_id=?) m`
	empty := func(column string) string { return "CASE WHEN " + column + " IS NULL THEN NULL ELSE '' END" }
	managed := `CASE WHEN EXISTS (SELECT 1 FROM scim_team_memberships sm WHERE sm.workspace_id=m.workspace_id AND sm.team_id=m.team_id AND sm.user_id=m.user_id AND sm.managed=1) THEN 1 ELSE 0 END`
	rows, err := tx.QueryContext(ctx, `SELECT m.target_match,COALESCE(`+length("m.team_id")+`,0),COALESCE(`+length("m.role")+`,0),`+
		empty("m.team_id")+","+empty("m.user_id")+","+empty("m.role")+","+empty("m.joined_at")+","+managed+","+empty("m.origin")+source+` ORDER BY `+teamMemberOrder(s.dialect, "m"), params...)
	if err != nil {
		return nil, nil
	}
	count, bytes := 0, 0
	err = func() error {
		defer rows.Close()
		for rows.Next() {
			var selected, teamBytes, roleBytes, managed int
			var team, user, role, joined, origin string
			if err := rows.Scan(&selected, &teamBytes, &roleBytes, &team, &user, &role, &joined, &managed, &origin); err != nil {
				return err
			}
			if selected == 0 {
				continue
			}
			if teamBytes < 0 || roleBytes < 0 || teamBytes > teamMemberRequestMaxWork-bytes || roleBytes > teamMemberRequestMaxWork-bytes-teamBytes {
				return ErrMetadataWorkBudget
			}
			count++
			bytes += teamBytes + roleBytes
			if count > teamMemberRequestMaxWork/1024 || bytes > teamMemberRequestMaxWork/32 {
				return ErrMetadataWorkBudget
			}
		}
		return rows.Err()
	}()
	if err != nil {
		if errors.Is(err, ErrMetadataWorkBudget) {
			return nil, err
		}
		return nil, nil
	}
	if err := read.charge(ctx, s, count*1024+bytes*32); err != nil {
		return nil, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT m.team_id,m.role`+source+` WHERE m.target_match=1 ORDER BY `+teamMemberOrder(s.dialect, "m"), params...)
	if err != nil {
		return nil, nil
	}
	defer rows.Close()
	result := make([]domain.TeamMember, 0, count)
	for rows.Next() {
		member := domain.TeamMember{UserID: principal}
		if err := rows.Scan(&member.TeamID, &member.Role); err != nil {
			return nil, nil
		}
		result = append(result, member)
	}
	if err := rows.Err(); err != nil {
		return nil, nil
	}
	return result, nil
}

func (s *SQLiteStore) teamMemberRequestSnapshotPolicy(ctx context.Context, source domain.Bootstrap, read *TeamMemberRequestRead) (domain.Bootstrap, error) {
	policy := domain.Bootstrap{TeamSettings: map[string]domain.TeamSettings{}}
	detach := func(value string) (string, error) {
		if err := read.charge(ctx, s, 512+len(value)*32); err != nil {
			return "", err
		}
		return strings.Clone(value), nil
	}
	var err error
	policy.Workspace.ID, err = detach(source.Workspace.ID)
	if err != nil {
		return policy, err
	}
	for _, original := range source.Teams {
		team := domain.Team{Private: original.Private}
		for _, field := range []struct {
			value string
			dest  *string
		}{{original.ID, &team.ID}, {original.Key, &team.Key}, {original.ExternalSource, &team.ExternalSource}} {
			*field.dest, err = detach(field.value)
			if err != nil {
				return policy, err
			}
		}
		if original.ArchivedAt != nil {
			team.ArchivedAt = new(time.Time)
		}
		if original.RetiredAt != nil {
			team.RetiredAt = new(time.Time)
		}
		policy.Teams = append(policy.Teams, team)
		id := team.ID
		for {
			if _, seen := policy.TeamSettings[id]; seen {
				break
			}
			original := source.TeamSettings[id]
			item := domain.TeamSettings{}
			for _, field := range []struct {
				value string
				dest  *string
			}{{original.ParentTeamID, &item.ParentTeamID}, {original.Access, &item.Access}, {original.MembershipRestriction, &item.MembershipRestriction}} {
				*field.dest, err = detach(field.value)
				if err != nil {
					return policy, err
				}
			}
			policy.TeamSettings[id] = item
			if item.ParentTeamID == "" {
				break
			}
			id = item.ParentTeamID
		}
	}
	return policy, nil
}

func (s *SQLiteStore) teamMemberRequestPolicy(ctx context.Context, source *metadataLogicalSource, read *TeamMemberRequestRead, permission bool) (domain.Bootstrap, error) {
	policy := domain.Bootstrap{TeamSettings: map[string]domain.TeamSettings{}}
	workspaceIndex, _, _ := metadataLogicalField("workspace")
	id, err := read.scalar(ctx, s, source.fields[workspaceIndex], "ID")
	if err != nil {
		return policy, err
	}
	policy.Workspace.ID = id.String()
	teamIndex, _, _ := metadataLogicalField("teams")
	err = source.fields[teamIndex].Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
		if err := read.charge(ctx, s, 1024); err != nil {
			return err
		}
		team := domain.Team{}
		for _, field := range []struct {
			name string
			dest *string
		}{{"ID", &team.ID}, {"Key", &team.Key}} {
			value, err := read.scalar(ctx, s, record, field.name)
			if err != nil {
				return err
			}
			*field.dest = value.String()
		}
		if permission {
			external, err := read.scalar(ctx, s, record, "ExternalSource")
			if err != nil {
				return err
			}
			team.ExternalSource = external.String()
			private, err := read.scalar(ctx, s, record, "Private")
			if err != nil {
				return err
			}
			team.Private = private.Bool()
			for _, field := range []string{"ArchivedAt", "RetiredAt"} {
				node, err := metadataAggregateChild(ctx, metadataAggregateStoredNode{record}, field)
				if err != nil || node == nil {
					return errors.Join(err, ErrBoundedMetadataCoverage)
				}
				nilValue, err := node.(metadataAggregateStoredNode).value.isNil(ctx)
				if err != nil {
					return err
				}
				if !nilValue {
					if field == "ArchivedAt" {
						team.ArchivedAt = new(time.Time)
					} else {
						team.RetiredAt = new(time.Time)
					}
				}
			}
		}
		policy.Teams = append(policy.Teams, team)
		return nil
	})
	if err != nil || !permission {
		return policy, err
	}
	settingsIndex, _, _ := metadataLogicalField("teamSettings")
	settings := metadataAggregateStoredNode{source.fields[settingsIndex]}
	for _, team := range policy.Teams {
		id := team.ID
		for {
			if _, seen := policy.TeamSettings[id]; seen {
				break
			}
			if err := read.charge(ctx, s, 512); err != nil {
				return policy, err
			}
			node, err := settings.mapValue(ctx, id)
			if err != nil {
				return policy, err
			}
			item := domain.TeamSettings{}
			if node != nil {
				record := node.(metadataAggregateStoredNode).value
				for _, field := range []struct {
					name string
					dest *string
				}{{"ParentTeamID", &item.ParentTeamID}, {"Access", &item.Access}, {"MembershipRestriction", &item.MembershipRestriction}} {
					value, err := read.scalar(ctx, s, record, field.name)
					if err != nil {
						return policy, err
					}
					*field.dest = value.String()
				}
			}
			policy.TeamSettings[id] = item
			if item.ParentTeamID == "" {
				break
			}
			id = item.ParentTeamID
		}
	}
	return policy, nil
}

// teamMemberOrder is ListTeamMembers' order: joined_at, then (for members
// who joined at the same instant, as a built-in application added to every
// team does) user and team, the order SQLite returns those ties in through its
// team_memberships_user index. It compares bytes as SQLite does: PostgreSQL's
// database collation and MySQL's utf8mb4_0900_ai_ci would not, and without the
// tie keys both return ties in physical order, which updates change.
func teamMemberOrder(dialect, alias string) string {
	columns := []string{"joined_at", "user_id", "team_id"}
	for i, column := range columns {
		column = alias + "." + column
		switch dialect {
		case "postgres":
			column += ` COLLATE "C"`
		case "mysql":
			column = "CAST(" + column + " AS BINARY)"
		}
		columns[i] = column
	}
	return strings.Join(columns, ",")
}
