package store

import (
	"context"
	"database/sql"
)

type residentOAuthIdentityRead uint8

const (
	residentOAuthHome residentOAuthIdentityRead = iota
	residentOAuthApplicationSecret
	residentOAuthConfidentialSecret
)

// These trusted readers select scalars, even when the application's description
// or manifest exceeds a detail page. Authorization stays with their callers.
func (s *SQLiteStore) residentOAuthIdentityValue(workspace, ref string, mode residentOAuthIdentityRead) (string, bool, func()) {
	stringColumn := func(field string) string {
		if s.dialect == "mysql" {
			column := "JSON_EXTRACT(CONVERT(data USING utf8mb4), '$." + field + "')"
			return "CASE WHEN JSON_TYPE(" + column + ")='STRING' THEN JSON_UNQUOTE(" + column + ") ELSE '' END"
		}
		return "COALESCE(" + s.jsonText("data", field) + ",'')"
	}
	value := "workspace_key"
	predicate := s.metadataRecordKeyEquals("("+stringColumn("clientId")+")", "?")
	if mode != residentOAuthHome {
		value = "(" + stringColumn("clientSecret") + ")"
	}
	if mode == residentOAuthApplicationSecret {
		if workspace == "" {
			return "", false, func() {}
		}
		predicate = s.metadataRecordKeyEquals("record_key", "?")
	}
	where, args := "field='oauthApplications' AND "+predicate, []any{ref}
	if workspace != "" {
		where += " AND workspace_key=?"
		args = append(args, workspace)
	}
	if mode == residentOAuthConfidentialSecret {
		where += " AND " + value + "<>''"
	}
	length := "OCTET_LENGTH(" + value + ")"
	if s.dialect == "sqlite" {
		length = "LENGTH(CAST(" + value + " AS BLOB))"
	}
	limit := MetadataRecordPageMaxBytes
	for attempt := 0; attempt < 3; attempt++ {
		if limit > (metadataWorkBytes-(1<<20))/3 {
			return "", false, func() {}
		}
		ctx, release, err := s.admitMetadataWork(context.Background(), max(2<<20, 3*limit+(1<<20)))
		if err != nil {
			return "", false, func() {}
		}
		var foundWorkspace string
		var size int
		var result sql.NullString
		err = s.db.QueryRowContext(ctx, "SELECT workspace_key,"+length+",CASE WHEN "+length+"<=? THEN "+value+" ELSE NULL END FROM workspace_metadata_records WHERE "+where+" ORDER BY workspace_key,collection_order,record_key LIMIT 1", append([]any{limit}, args...)...).Scan(&foundWorkspace, &size, &result)
		if err != nil || !s.HasWorkspace(foundWorkspace) {
			release()
			return "", false, func() {}
		}
		if result.Valid {
			return result.String, true, release
		}
		release()
		if size <= limit || size > s.maxStateBytes {
			return "", false, func() {}
		}
		// Preserve legal legacy scalar sizes. Reserve their larger work bound
		// before fetching; a concurrent growth is checked again in SQL.
		limit = size
	}
	return "", false, func() {}
}
