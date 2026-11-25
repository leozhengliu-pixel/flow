package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"

	"flow/api/internal/domain"
)

const scaleP21SW19SCIMMaxWork = 64 << 20

// SCIMTeamRead owns the trusted canonical snapshot and the work allowance for
// its typed results. Callers must finish using Teams before Close.
type SCIMTeamRead struct {
	ctx, parent context.Context
	store       *SQLiteStore
	tx          *sqlTx
	workspace   string
	linked      []string
	resolved    []string
	headerCost  int64
	release     func()
	output      func()
	once        sync.Once
}

// BeginSCIMTeamRead resolves explicit mappings before the first case-folded
// team ID/key match in canonical order. Team names and other bodies do not
// enter this selector.
func (s *SQLiteStore) BeginSCIMTeamRead(ctx context.Context, workspace string, groups []SCIMGroup) (*SCIMTeamRead, bool, error) {
	if workspace == "" {
		s.mu.RLock()
		workspace = s.lastWorkspaceKey
		s.mu.RUnlock()
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if strings.ContainsRune(workspace, '\x00') {
		return nil, false, nil
	}
	tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return nil, false, err
	}
	fail := func(found bool, err error) (*SCIMTeamRead, bool, error) {
		_ = tx.Rollback()
		return nil, found, err
	}
	workspaceEqual := s.metadataRecordKeyEquals("workspace_key", "?")
	var actual string
	// Match the legacy Go map identity in the same snapshot as team output.
	if err := tx.QueryRowContext(ctx, `SELECT workspace_key FROM workspace_states WHERE `+workspaceEqual, workspace).Scan(&actual); errors.Is(err, sql.ErrNoRows) {
		return fail(false, nil)
	} else if err != nil {
		return fail(false, err)
	} else if actual != workspace {
		return fail(false, nil)
	}
	var mappingBytes, maximumKey sql.NullInt64
	key := "COALESCE(" + s.jsonText("data", "key") + ",'')"
	length := "OCTET_LENGTH(" + key + ")"
	if s.dialect == "sqlite" {
		length = "LENGTH(CAST(" + key + " AS BLOB))"
	}
	if len(groups) != 0 {
		if err := tx.QueryRowContext(ctx, `SELECT LENGTH(data) FROM workspace_metadata_records
			WHERE `+workspaceEqual+` AND field='workspaceSettings' AND record_key='scimTeamGroupMapping'`, workspace).Scan(&mappingBytes); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fail(true, err)
		}
		if err := tx.QueryRowContext(ctx, `SELECT MAX(`+length+`) FROM workspace_metadata_records WHERE `+workspaceEqual+` AND field='teams'`, workspace).Scan(&maximumKey); err != nil {
			return fail(true, err)
		}
	}
	cost := int64(2<<20) + int64(len(groups))*256
	if mappingBytes.Int64 < 0 || maximumKey.Int64 < 0 || mappingBytes.Int64 > scaleP21SW19SCIMMaxWork/8 || maximumKey.Int64 > scaleP21SW19SCIMMaxWork/8 {
		return fail(true, ErrMetadataWorkBudget)
	}
	cost += 8 * (mappingBytes.Int64 + maximumKey.Int64)
	if cost > scaleP21SW19SCIMMaxWork {
		return fail(true, ErrMetadataWorkBudget)
	}
	admitted, release, err := s.admitMetadataWork(ctx, int(cost))
	if err != nil {
		return fail(true, err)
	}
	r := &SCIMTeamRead{ctx: admitted, parent: ctx, store: s, tx: tx, workspace: workspace, release: release, resolved: make([]string, len(groups)), headerCost: cost}
	mapping := map[string]string{}
	if mappingBytes.Valid {
		var raw []byte
		if err := tx.QueryRowContext(admitted, `SELECT data FROM workspace_metadata_records WHERE `+workspaceEqual+` AND field='workspaceSettings' AND record_key='scimTeamGroupMapping'`, workspace).Scan(&raw); err != nil {
			r.Close()
			return nil, true, err
		}
		if err := json.Unmarshal(raw, &mapping); err != nil {
			r.Close()
			return nil, true, err
		}
	}
	for index, group := range groups {
		for teamID, groupName := range mapping {
			if !strings.EqualFold(strings.TrimSpace(groupName), group.DisplayName) && !strings.EqualFold(strings.TrimSpace(groupName), group.ExternalID) {
				continue
			}
			var exists int
			if err := tx.QueryRowContext(admitted, `SELECT COUNT(*) FROM workspace_metadata_records WHERE `+workspaceEqual+` AND field='teams' AND `+s.metadataRecordKeyEquals("record_key", "?"), workspace, teamID).Scan(&exists); err != nil {
				r.Close()
				return nil, true, err
			}
			if exists != 0 {
				r.resolved[index] = teamID
				break
			}
		}
	}
	if slices.Contains(r.resolved, "") {
		err = r.headers(func(id string, order, size int64) error {
			if !slices.Contains(r.resolved, "") {
				return nil
			}
			var value sql.NullString
			if err := tx.QueryRowContext(admitted, `SELECT `+key+` FROM workspace_metadata_records WHERE `+workspaceEqual+` AND field='teams' AND `+s.metadataRecordKeyEquals("record_key", "?"), workspace, id).Scan(&value); err != nil {
				return err
			}
			for index, group := range groups {
				if r.resolved[index] == "" && (strings.EqualFold(id, group.ExternalID) || strings.EqualFold(value.String, group.ExternalID)) {
					r.resolved[index] = id
				}
			}
			return nil
		})
		if err != nil {
			r.Close()
			return nil, true, err
		}
	}
	for _, id := range r.resolved {
		if id != "" && !slices.Contains(r.linked, id) {
			r.linked = append(r.linked, id)
		}
	}
	return r, true, nil
}

func (r *SCIMTeamRead) Close() {
	if r == nil {
		return
	}
	r.once.Do(func() {
		_ = r.tx.Rollback()
		if r.output != nil {
			r.output()
		}
		r.release()
	})
}

func (r *SCIMTeamRead) TeamID(index int) string {
	if index < 0 || index >= len(r.resolved) {
		return ""
	}
	return r.resolved[index]
}

func (r *SCIMTeamRead) headers(visit func(string, int64, int64) error) error {
	started, lastID, lastOrder := false, "", int64(0)
	for {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		where, args := r.store.metadataRecordKeyEquals("workspace_key", "?")+` AND field='teams'`, []any{r.workspace}
		if started {
			where += ` AND (collection_order>? OR (collection_order=? AND record_key>?))`
			args = append(args, lastOrder, lastOrder, lastID)
		}
		rows, err := r.tx.QueryContext(r.ctx, `SELECT record_key,collection_order,LENGTH(data) FROM workspace_metadata_records WHERE `+where+` ORDER BY collection_order,record_key LIMIT 50`, args...)
		if err != nil {
			return err
		}
		type header struct {
			id          string
			order, size int64
		}
		batch := []header{}
		for rows.Next() {
			var item header
			if err := rows.Scan(&item.id, &item.order, &item.size); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, item := range batch {
			if err := visit(item.id, item.order, item.size); err != nil {
				return err
			}
		}
		if len(batch) < 50 {
			return nil
		}
		last := batch[len(batch)-1]
		started, lastID, lastOrder = true, last.id, last.order
	}
}

// Teams returns every unlinked team, preserving canonical order and typed
// wire fields. The adaptive allowance covers all returned objects until Close.
func (r *SCIMTeamRead) Teams() ([]domain.Team, error) {
	cost, count := int64(1<<20), 0
	if err := r.headers(func(id string, order, size int64) error {
		if slices.Contains(r.linked, id) {
			return nil
		}
		if size < 0 || size > scaleP21SW19SCIMMaxWork/8 {
			return ErrMetadataWorkBudget
		}
		cost += 8*size + 1024
		count++
		if cost > scaleP21SW19SCIMMaxWork {
			return ErrMetadataWorkBudget
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if r.output != nil {
		return nil, ErrMetadataRecordQuery
	}
	// Include selector storage too when the parent already has a reusable
	// work lease. Without one, the extra selector charge is conservative.
	if cost+r.headerCost > scaleP21SW19SCIMMaxWork {
		return nil, ErrMetadataWorkBudget
	}
	_, release, err := r.store.admitMetadataWork(r.parent, int(cost+r.headerCost))
	if err != nil {
		return nil, err
	}
	r.output = release
	result := make([]domain.Team, 0, count)
	err = r.headers(func(id string, order, size int64) error {
		if slices.Contains(r.linked, id) {
			return nil
		}
		reader := &canonicalMetadataReader{ctx: r.ctx, tx: r.tx, workspace: r.workspace, key: RecordKey{Kind: "teams", ID: id}, size: size}
		var team domain.Team
		if err := json.NewDecoder(reader).Decode(&team); err != nil {
			return err
		}
		if team.ID != id {
			return ErrMetadataRecordQuery
		}
		result = append(result, team)
		return nil
	})
	return result, err
}
