package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	"flow/api/internal/domain"
)

// TeamResourceRead owns one trusted SQL snapshot and its selected-row work
// allowance. Authorization belongs to the existing team-content boundary.
type TeamResourceRead struct {
	ctx       context.Context
	tx        *sqlTx
	store     *SQLiteStore
	workspace string
	teamID    string
	maximum   int64
	sections  bool
	resources bool
	release   func()
	once      sync.Once
	err       error
}

// BeginTeamResourceRead checks workspace and team identity in the same snapshot
// used by both typed visitors. The allowance covers the largest selected row,
// not the total number of rows, and remains held until Close.
func (s *SQLiteStore) BeginTeamResourceRead(ctx context.Context, workspace, teamID string) (*TeamResourceRead, bool, bool, error) {
	if workspace == "" {
		s.mu.RLock()
		workspace = s.lastWorkspaceKey
		s.mu.RUnlock()
	}
	// textArgs strips PostgreSQL NULs; preserve exact identity comparisons.
	if s.dialect == "postgres" && strings.ContainsRune(workspace, '\x00') {
		return nil, false, false, nil
	}
	tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return nil, false, false, err
	}
	var workspaceID string
	var teams, sections, resources int
	if err := tx.QueryRowContext(ctx, `SELECT workspace_id,`+s.scaleP21SW18CollectionState("teams")+`,`+s.scaleP21SW18CollectionState("teamResourceSections")+`,`+s.scaleP21SW18CollectionState("teamPinnedResources")+` FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&workspaceID, &teams, &sections, &resources); err != nil {
		_ = tx.Rollback()
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, false, nil
		}
		return nil, false, false, err
	}
	if teams < 0 || sections < 0 || resources < 0 {
		_ = tx.Rollback()
		return nil, true, false, ErrBoundedMetadataCoverage
	}
	if s.dialect == "postgres" && strings.ContainsRune(teamID, '\x00') {
		_ = tx.Rollback()
		return nil, true, false, nil
	}
	if teams == 0 {
		_ = tx.Rollback()
		return nil, true, false, nil
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_metadata_records WHERE workspace_key=? AND field='teams' AND `+s.metadataRecordKeyEquals("record_key", "?"), workspace, teamID).Scan(&exists); err != nil {
		_ = tx.Rollback()
		return nil, true, false, err
	}
	if exists == 0 {
		_ = tx.Rollback()
		return nil, true, false, nil
	}
	var maximum sql.NullInt64
	kinds := []string{}
	if sections != 0 {
		kinds = append(kinds, "teamResourceSections")
	}
	if resources != 0 {
		kinds = append(kinds, "teamPinnedResources")
	}
	if len(kinds) != 0 {
		clause, values := bindList("field", kinds)
		args := append([]any{workspace}, values...)
		args = append(args, teamID)
		if err := tx.QueryRowContext(ctx, `SELECT MAX(LENGTH(data)) FROM workspace_metadata_records WHERE workspace_key=? AND `+clause+` AND `+s.metadataRecordKeyEquals(s.jsonText("data", "teamId"), "?"), args...).Scan(&maximum); err != nil {
			_ = tx.Rollback()
			return nil, true, true, err
		}
	}
	// Flat typed rows retain strings, numbers and timestamps. Reserve input,
	// decoded strings, encoder growth and escaped output for one row at a time.
	const fixedBytes, multiplier = 2 << 20, 12
	if maximum.Int64 < 0 || maximum.Int64 > int64((int(^uint(0)>>1)-fixedBytes)/multiplier) {
		_ = tx.Rollback()
		return nil, true, true, ErrMetadataWorkBudget
	}
	ctx, release, err := s.admitMetadataWork(ctx, fixedBytes+int(maximum.Int64)*multiplier)
	if err != nil {
		_ = tx.Rollback()
		return nil, true, true, err
	}
	return &TeamResourceRead{ctx: ctx, tx: tx, store: s, workspace: workspace, teamID: teamID, maximum: maximum.Int64, sections: sections != 0, resources: resources != 0, release: release}, true, true, nil
}

func (r *TeamResourceRead) Close() error {
	if r == nil {
		return nil
	}
	r.once.Do(func() {
		r.err = r.tx.Rollback()
		r.release()
	})
	return r.err
}

func (r *TeamResourceRead) VisitSections(visit func(domain.TeamResourceSection) error) error {
	if !r.sections {
		return nil
	}
	return r.visit("teamResourceSections", func(reader io.Reader) error {
		var section domain.TeamResourceSection
		if err := json.NewDecoder(reader).Decode(&section); err != nil {
			return err
		}
		return visit(section)
	})
}

func (r *TeamResourceRead) VisitResources(visit func(domain.TeamPinnedResource) error) error {
	if !r.resources {
		return nil
	}
	return r.visit("teamPinnedResources", func(reader io.Reader) error {
		var resource domain.TeamPinnedResource
		if err := json.NewDecoder(reader).Decode(&resource); err != nil {
			return err
		}
		return visit(resource)
	})
}

// A root manifest controls whether its canonical rows are active. Unsplit
// nonempty arrays cannot be mistaken for empty canonical state.
func (s *SQLiteStore) scaleP21SW18CollectionState(field string) string {
	value := func(path string) string { return s.jsonText("data", path) }
	kind := func(path string) string {
		switch s.dialect {
		case "mysql":
			return "LOWER(JSON_TYPE(JSON_EXTRACT(CONVERT(data USING utf8mb4),'$." + path + "')))"
		case "postgres":
			parts := strings.ReplaceAll(path, ".", ",")
			return "jsonb_typeof(convert_from(data,'UTF8')::jsonb #> '{" + parts + "}')"
		default:
			return "json_type(data,'$." + path + "')"
		}
	}
	length := "json_array_length(data,'$." + field + "')"
	textKind := "'text'"
	switch s.dialect {
	case "mysql":
		length = "JSON_LENGTH(JSON_EXTRACT(CONVERT(data USING utf8mb4),'$." + field + "'))"
		textKind = "'string'"
	case "postgres":
		length = "jsonb_array_length(convert_from(data,'UTF8')::jsonb -> '" + field + "')"
		textKind = "'string'"
	}
	manifest := metadataCollectionsKey + "." + field
	empty := "CASE WHEN " + kind(field) + "='array' THEN CASE WHEN " + length + "=0 THEN 0 ELSE -1 END WHEN " + kind(field) + " IS NULL OR " + kind(field) + "='null' THEN 0 ELSE -1 END"
	return "CASE WHEN " + kind(manifest) + "=" + textKind + " AND " + s.metadataRecordKeyEquals(value(manifest), "'array'") + " THEN 1 WHEN " + kind(manifest) + " IS NULL THEN " + empty + " ELSE -1 END"
}

func (r *TeamResourceRead) visit(kind string, visit func(io.Reader) error) error {
	position := "COALESCE(" + r.store.jsonText("data", "position") + ",0)"
	parameter := "?"
	switch r.store.dialect {
	case "mysql":
		extract := "JSON_EXTRACT(CONVERT(data USING utf8mb4), '$.position')"
		position = "CASE WHEN JSON_TYPE(" + extract + ") IN ('INTEGER','DOUBLE','DECIMAL') THEN " + extract + " ELSE CAST('0' AS JSON) END"
		parameter = "CAST(? AS JSON)"
	case "postgres":
		position = "CAST(" + position + " AS DOUBLE PRECISION)"
	}
	var lastID string
	var lastPosition float64
	var lastOrder int64
	started := false
	for {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		where := `workspace_key=? AND field=? AND ` + r.store.metadataRecordKeyEquals(r.store.jsonText("data", "teamId"), "?")
		args := []any{r.workspace, kind, r.teamID}
		if started {
			where += ` AND (` + position + `>` + parameter + ` OR (` + position + `=` + parameter + ` AND (collection_order>? OR (collection_order=? AND record_key>?))))`
			var value any = lastPosition
			if r.store.dialect == "mysql" {
				value = strconv.FormatFloat(lastPosition, 'g', -1, 64)
			}
			args = append(args, value, value, lastOrder, lastOrder, lastID)
		}
		rows, err := r.tx.QueryContext(r.ctx, `SELECT record_key,collection_order,`+position+`,LENGTH(data) FROM workspace_metadata_records WHERE `+where+` ORDER BY `+position+`,collection_order,record_key LIMIT 50`, args...)
		if err != nil {
			return err
		}
		type header struct {
			id       string
			order    int64
			position float64
			size     int64
		}
		batch := []header{}
		for rows.Next() {
			var item header
			if err := rows.Scan(&item.id, &item.order, &item.position, &item.size); err != nil {
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
		if len(batch) == 0 {
			return nil
		}
		for _, item := range batch {
			if item.size > r.maximum {
				return fmt.Errorf("%w: team resource snapshot changed", ErrResourceAccessChanged)
			}
			reader := &canonicalMetadataReader{ctx: r.ctx, tx: r.tx, workspace: r.workspace, key: RecordKey{Kind: kind, ID: item.id}, size: item.size}
			if err := visit(reader); err != nil {
				return err
			}
		}
		last := batch[len(batch)-1]
		lastID, lastPosition, lastOrder, started = last.id, last.position, last.order, true
	}
}
