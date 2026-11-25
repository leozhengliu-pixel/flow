package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

// TeamRecordForAccess returns one team row visible in the settings directory.
// An admin may inspect a private team's row without reading that team's content.
func (s *SQLiteStore) TeamRecordForAccess(ctx context.Context, access AccessContext, ref string) (domain.Team, error) {
	ctx, release, err := s.admitMetadataWork(ctx, 2<<20)
	if err != nil {
		return domain.Team{}, err
	}
	defer release()
	if strings.ContainsRune(access.PrincipalID, '\x00') || strings.ContainsRune(access.WorkspaceID, '\x00') {
		return domain.Team{}, ErrAuthForbidden
	}
	if err := access.checkBinding(s); err != nil {
		return domain.Team{}, err
	}
	if strings.ContainsRune(access.policy.workspace, '\x00') {
		return domain.Team{}, ErrAuthForbidden
	}
	if err := s.ValidateAccess(ctx, access); err != nil {
		return domain.Team{}, err
	}
	p := access.policy
	if _, err := s.scaleP21SW22TeamStamp(ctx, s.db, p.workspace); errors.Is(err, sql.ErrNoRows) {
		return domain.Team{}, ErrAuthForbidden
	} else if err != nil {
		return domain.Team{}, err
	}
	if strings.ContainsRune(ref, '\x00') {
		return domain.Team{}, sql.ErrNoRows
	}
	visible := func(team domain.Team) bool {
		return p.teams.Teams[team.ID] && (!p.selected() || slices.Contains(p.key.TeamIDs, team.ID))
	}
	id := scaleP21SW22TeamReference(p.metadata.Teams, ref, visible)
	if id == "" {
		if err := s.ValidateAccess(ctx, access); err != nil {
			return domain.Team{}, err
		}
		return domain.Team{}, sql.ErrNoRows
	}
	tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return domain.Team{}, err
	}
	defer tx.Rollback()
	stamp, err := s.scaleP21SW22TeamStamp(ctx, tx, p.workspace)
	if err != nil {
		return domain.Team{}, err
	}
	if stamp.workspaceID != p.stamp.workspaceID {
		return domain.Team{}, errors.Join(ErrAuthForbidden, ErrResourceAccessChanged)
	}
	if s.db.journalReady.Load() {
		epoch, err := readAuthzEpoch(ctx, tx, access.WorkspaceID)
		if err != nil {
			return domain.Team{}, err
		}
		if epoch != access.AuthzEpoch {
			return domain.Team{}, errors.Join(ErrAuthForbidden, ErrResourceAccessChanged)
		}
	} else if stamp != p.stamp {
		return domain.Team{}, errors.Join(ErrAuthForbidden, ErrResourceAccessChanged)
	}
	item, err := s.scaleP21SW22ReadTeam(ctx, tx, p.workspace, id)
	if err != nil {
		return domain.Team{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Team{}, err
	}
	if err := s.ValidateAccess(ctx, access); err != nil {
		return domain.Team{}, err
	}
	return item, nil
}

// TeamRecordForWorkspace is trusted storage for the auth-disabled directory.
// The boolean distinguishes a missing workspace from a missing team.
func (s *SQLiteStore) TeamRecordForWorkspace(ctx context.Context, workspace, ref string) (domain.Team, bool, error) {
	// A stale trusted catalog can require eight bounded key headers in one
	// SQL page. Reserve their transfer/strings plus the selected record.
	ctx, release, err := s.admitMetadataWork(ctx, 8<<20)
	if err != nil {
		return domain.Team{}, false, err
	}
	defer release()
	if workspace == "" {
		s.mu.RLock()
		workspace = s.lastWorkspaceKey
		s.mu.RUnlock()
	}
	if strings.ContainsRune(workspace, '\x00') {
		return domain.Team{}, false, nil
	}
	tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return domain.Team{}, false, err
	}
	defer tx.Rollback()
	stamp, err := s.scaleP21SW22TeamStamp(ctx, tx, workspace)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Team{}, false, nil
	} else if err != nil {
		return domain.Team{}, false, err
	}
	if strings.ContainsRune(ref, '\x00') {
		return domain.Team{}, true, sql.ErrNoRows
	}
	var id string
	current := false
	s.mu.RLock()
	if source, exists := s.workspaces[workspace]; exists {
		var resident copyStamp
		if s.metadataResidency != nil {
			resident, _, current = s.residentStamp(workspace)
			current = current && resident == stamp
		} else if known, exact := s.copyRevisionLocked(workspace); exact {
			current = known.workspaceID == stamp.workspaceID && known.revision == stamp.revision
		}
		if current {
			id = scaleP21SW22TeamReference(source.Teams, ref, func(domain.Team) bool { return true })
		}
	}
	s.mu.RUnlock()
	if current && id == "" {
		return domain.Team{}, true, sql.ErrNoRows
	}
	if !current {
		err = tx.QueryRowContext(ctx, `SELECT record_key FROM workspace_metadata_records
			WHERE workspace_key=? AND field='teams' AND `+s.metadataRecordKeyEquals("record_key", "?"), workspace, ref).Scan(&id)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return domain.Team{}, true, err
		}
		if errors.Is(err, sql.ErrNoRows) {
			id, err = s.scaleP21SW22TeamKey(ctx, tx, workspace, ref)
			if err != nil {
				return domain.Team{}, true, err
			}
		}
	}
	item, err := s.scaleP21SW22ReadTeam(ctx, tx, workspace, id)
	if err != nil {
		return domain.Team{}, true, err
	}
	return item, true, tx.Commit()
}

func scaleP21SW22TeamReference(teams []domain.Team, ref string, visible func(domain.Team) bool) string {
	for _, team := range teams {
		if visible(team) && team.ID == ref {
			return team.ID
		}
	}
	for _, team := range teams {
		if visible(team) && strings.EqualFold(team.Key, ref) {
			return team.ID
		}
	}
	return ""
}

func (s *SQLiteStore) scaleP21SW22TeamStamp(ctx context.Context, reader metadataRowReader, workspace string) (copyStamp, error) {
	var stamp copyStamp
	err := reader.QueryRowContext(ctx, `SELECT `+metadataStampColumns+` FROM workspace_states WHERE `+s.metadataRecordKeyEquals("workspace_key", "?"), workspace, cacheKindMetadata, workspace).Scan(&stamp.workspaceID, &stamp.revision)
	return stamp, err
}

func (s *SQLiteStore) scaleP21SW22ReadTeam(ctx context.Context, reader metadataRowReader, workspace, id string) (domain.Team, error) {
	var size int
	var raw []byte
	err := reader.QueryRowContext(ctx, `SELECT LENGTH(data),CASE WHEN LENGTH(data)<=? THEN data ELSE NULL END
		FROM workspace_metadata_records WHERE workspace_key=? AND field='teams' AND `+s.metadataRecordKeyEquals("record_key", "?"), MetadataRecordPageMaxBytes, workspace, id).Scan(&size, &raw)
	if err != nil {
		return domain.Team{}, err
	}
	if raw == nil || size > MetadataRecordPageMaxBytes {
		return domain.Team{}, ErrMetadataPageBudget
	}
	budget := newMetadataHydrationBudget()
	if err := budget.consume(raw); err != nil {
		return domain.Team{}, err
	}
	var item domain.Team
	if err := json.Unmarshal(raw, &item); err != nil {
		return domain.Team{}, err
	}
	if item.ID != id {
		return domain.Team{}, ErrMetadataRecordQuery
	}
	return item, nil
}

func (s *SQLiteStore) scaleP21SW22TeamKey(ctx context.Context, tx *sqlTx, workspace, ref string) (string, error) {
	var lastOrder int
	lastID, started := "", false
	for {
		where, args := "workspace_key=? AND field='teams'", []any{workspace}
		if started {
			where += " AND (collection_order>? OR (collection_order=? AND record_key>?))"
			args = append(args, lastOrder, lastOrder, lastID)
		}
		key := "COALESCE(" + s.jsonText("data", "key") + ",'')"
		length := "OCTET_LENGTH(" + key + ")"
		if s.dialect == "sqlite" {
			length = "LENGTH(CAST(" + key + " AS BLOB))"
		}
		rows, err := tx.QueryContext(ctx, `SELECT record_key,collection_order,`+length+`,
			CASE WHEN `+length+`<=? THEN `+key+` ELSE NULL END
			FROM workspace_metadata_records WHERE `+where+` ORDER BY collection_order,record_key LIMIT 8`, append([]any{MetadataRecordPageMaxBytes}, args...)...)
		if err != nil {
			return "", err
		}
		count, found := 0, ""
		for rows.Next() {
			var size int
			var current sql.NullString
			if err := rows.Scan(&lastID, &lastOrder, &size, &current); err != nil {
				rows.Close()
				return "", err
			}
			count++
			if !current.Valid || size > MetadataRecordPageMaxBytes {
				rows.Close()
				return "", ErrMetadataPageBudget
			}
			if strings.EqualFold(current.String, ref) {
				found = lastID
				break
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return "", err
		}
		if found != "" {
			return found, nil
		}
		if count < 8 {
			return "", sql.ErrNoRows
		}
		started = true
	}
}
