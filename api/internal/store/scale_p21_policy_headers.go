package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Only fields consumed by resourcePolicyMetadata cross the SQL boundary.
// In particular, team guidance and descriptions cannot consume policy work.
func (s *SQLiteStore) resourcePolicyHeaderJSON(field string) string {
	names := []string{"id", "key", "externalSource", "private", "archivedAt", "retiredAt"}
	if field == "teamSettings" {
		names = []string{"parentTeamId", "access", "membershipRestriction", "settingsPermission", "labelPermission", "memberPermission"}
	}
	function := "JSON_OBJECT"
	if s.dialect == "postgres" {
		function = "JSON_BUILD_OBJECT"
	}
	values := []string{}
	for _, name := range names {
		value := s.jsonText("data", name)
		if name == "private" {
			// JSON text access returns 0/1 on SQLite, true/false on others.
			cast := "TEXT"
			if s.dialect == "mysql" {
				cast = "CHAR"
			}
			value = "CASE WHEN CAST(" + value + " AS " + cast + ") IN ('true','1') THEN 'true' ELSE 'false' END"
		}
		values = append(values, "'"+name+"'", value)
	}
	result := function + "(" + strings.Join(values, ",") + ")"
	if s.dialect == "postgres" {
		result = "CONVERT_TO(CAST(" + result + " AS TEXT),'UTF8')"
	}
	return result
}

func (s *SQLiteStore) scanResourcePolicyHeaders(ctx context.Context, workspace, field string, visit func([]metadataCacheRow) error) error {
	projection := s.resourcePolicyHeaderJSON(field)
	length := "OCTET_LENGTH(" + projection + ")"
	if s.dialect == "sqlite" {
		length = "LENGTH(CAST(" + projection + " AS BLOB))"
	}
	last, started := "", false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		where := "workspace_key=? AND field=?"
		args := []any{MetadataRecordPageMaxBytes, workspace, field}
		if started {
			where += " AND record_key>?"
			args = append(args, last)
		}
		rows, err := s.db.QueryContext(ctx, `SELECT record_key,collection_order,`+length+`,CASE WHEN `+length+`<=? THEN `+projection+` ELSE NULL END FROM workspace_metadata_records WHERE `+where+` ORDER BY record_key LIMIT 32`, args...)
		if err != nil {
			return err
		}
		batch := []metadataCacheRow{}
		used := 0
		for rows.Next() {
			row := metadataCacheRow{Field: field}
			var raw []byte
			var size int
			if err := rows.Scan(&row.Key, &row.Order, &size, &raw); err != nil {
				rows.Close()
				return err
			}
			if raw == nil || size != len(raw) || size > MetadataRecordPageMaxBytes-used {
				rows.Close()
				return ErrMetadataPageBudget
			}
			used += size
			// SQL projects scalar strings. Restore the single boolean to the
			// same JSON type before the existing typed policy decoder.
			if field == "teams" {
				var values map[string]json.RawMessage
				if err := json.Unmarshal(raw, &values); err != nil {
					rows.Close()
					return err
				}
				var private string
				if err := json.Unmarshal(values["private"], &private); err != nil {
					rows.Close()
					return err
				}
				if private != "true" && private != "false" {
					rows.Close()
					return fmt.Errorf("%w: invalid private policy scalar", ErrMetadataRecordQuery)
				}
				values["private"] = json.RawMessage(private)
				raw, err = json.Marshal(values)
				if err != nil {
					rows.Close()
					return err
				}
			}
			row.Data = raw
			batch = append(batch, row)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}
		if err := visit(batch); err != nil {
			return err
		}
		last, started = batch[len(batch)-1].Key, true
	}
}
