package store

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

// scaleP21OAuthApplicationByClientID is OAuthApplicationByClientID on a
// bounded store, whose resident workspaces hold no applications. It reads the
// one matching record from canonical SQL and decodes it as a load does
// (encoding/json into the record type), skipping workspaces this store has
// not installed. Several matches resolve to the first in workspace key,
// collection order and record key order: within a workspace that is the
// original's first match; across workspaces the original took the first in
// map iteration order, which is not deterministic. The secret is never
// returned. Work is admitted for the record and its decoded value, and
// released before returning: the result is a fresh, unshared copy.
func (s *SQLiteStore) scaleP21OAuthApplicationByClientID(clientID string) (domain.OAuthApplication, bool) {
	if strings.ContainsRune(clientID, '\x00') {
		// No stored identifier can match one: SQL drivers do not carry NUL
		// through a text parameter, as in the other client lookups.
		return domain.OAuthApplication{}, false
	}
	client := "COALESCE(" + s.jsonText("data", "clientId") + ",'')"
	if s.dialect == "mysql" {
		column := "JSON_EXTRACT(CONVERT(data USING utf8mb4),'$.clientId')"
		client = "CASE WHEN JSON_TYPE(" + column + ")='STRING' THEN JSON_UNQUOTE(" + column + ") ELSE '' END"
	}
	length := "OCTET_LENGTH(data)"
	if s.dialect == "sqlite" {
		length = "LENGTH(CAST(data AS BLOB))"
	}
	// The record buffer, its decoded strings and the slice headers of its
	// string lists.
	cost := "(3*" + length + "+32*(" + s.scaleP21SW17JSONArrayLength("data", "redirectUris") + "+" +
		s.scaleP21SW17JSONArrayLength("data", "scopes") + "+" + s.scaleP21SW17JSONArrayLength("data", "webhookResourceTypes") + ")+1048576)"
	query := "SELECT workspace_key,record_key,collection_order," + cost + ",CASE WHEN " + cost + "<=? THEN data ELSE NULL END" +
		" FROM workspace_metadata_records WHERE field='oauthApplications' AND " + s.metadataRecordKeyEquals("("+client+")", "?")
	lastWorkspace, lastKey, cursor := "", "", false
	var lastOrder int64
	for {
		need, advanced := 2<<20, false
		for attempt := 0; attempt < 3; attempt++ {
			if need > metadataWorkBytes-(1<<20) {
				return domain.OAuthApplication{}, false
			}
			ctx, release, err := s.admitMetadataWork(context.Background(), need)
			if err != nil {
				return domain.OAuthApplication{}, false
			}
			args := []any{need, clientID}
			candidate := query
			if cursor {
				candidate += ` AND (workspace_key>? OR (workspace_key=? AND (collection_order>? OR (collection_order=? AND record_key>?))))`
				args = append(args, lastWorkspace, lastWorkspace, lastOrder, lastOrder, lastKey)
			}
			var workspace, key string
			var order int64
			var size int
			var raw []byte
			err = s.db.QueryRowContext(ctx, candidate+" ORDER BY workspace_key,collection_order,record_key LIMIT 1", args...).Scan(&workspace, &key, &order, &size, &raw)
			if err != nil {
				release()
				return domain.OAuthApplication{}, false
			}
			if !s.HasWorkspace(workspace) {
				release()
				lastWorkspace, lastKey, lastOrder, cursor, advanced = workspace, key, order, true, true
				break
			}
			if raw == nil {
				// Larger than admitted: admit its size and read it again (a
				// concurrent change is measured again in SQL).
				release()
				need = max(need, size)
				continue
			}
			var app domain.OAuthApplication
			err = json.Unmarshal(raw, &app)
			release()
			if err != nil {
				return domain.OAuthApplication{}, false
			}
			app.ClientSecret = ""
			app.RedirectURIs = slices.Clone(app.RedirectURIs)
			app.Scopes = slices.Clone(app.Scopes)
			return app, true
		}
		if !advanced {
			return domain.OAuthApplication{}, false
		}
	}
}
