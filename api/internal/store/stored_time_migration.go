package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const storedTimeMigrationVersion = 36

type storedTimeTable struct {
	name       string
	key, times []string
	largeRows  bool
}

// Only columns that used variable-width timestamps belong here. In
// particular, issue, content, and catalog sort columns already store fixed
// width. Erasure markers (auth_users.deleted_at) are authorization inputs,
// so rewriting their spelling would advance the authorization epoch.
var storedTimeTables = []storedTimeTable{
	{"api_signing_secrets", []string{"name"}, []string{"created_at"}, false},
	{"application_agent_activities", []string{"session_id", "id"}, []string{"created_at"}, true},
	{"application_agent_tasks", []string{"id"}, []string{"created_at", "lease_until", "next_attempt", "stale_at", "merged_at", "archived_at"}, true},
	{"auth_account_state", []string{"user_id"}, []string{"updated_at"}, false},
	{"auth_email_events", []string{"user_id", "kind", "item_key"}, []string{"created_at"}, false},
	{"auth_identities", []string{"id"}, []string{"created_at", "last_login_at"}, false},
	{"auth_session_security", []string{"token_hash"}, []string{"mfa_verified_at"}, false},
	{"auth_sessions", []string{"token_hash"}, []string{"created_at", "last_seen_at", "expires_at"}, false},
	{"auth_tokens", []string{"token_hash"}, []string{"created_at", "expires_at", "used_at"}, false},
	{"auth_users", []string{"id"}, []string{"created_at", "updated_at", "email_verified_at"}, false},
	{"code_host_accounts", []string{"id"}, []string{"updated_at", "linked_at", "token_expires_at"}, false},
	{"connector_secrets", []string{"id"}, []string{"expires_at"}, false},
	{"document_collaboration_updates", []string{"update_id"}, []string{"created_at"}, true},
	{"domain_events", []string{"id"}, []string{"created_at"}, true},
	{"file_uploads", []string{"id"}, []string{"created_at", "updated_at", "expires_at"}, false},
	{"oauth_authorization_codes", []string{"code_hash"}, []string{"created_at", "expires_at", "used_at"}, false},
	{"oauth_clients", []string{"client_id"}, []string{"created_at"}, false},
	{"oauth_refresh_tokens", []string{"token_hash"}, []string{"created_at", "expires_at", "revoked_at"}, false},
	{"personal_connections", []string{"id"}, []string{"linked_at", "updated_at", "token_expires_at", "away_from", "away_until"}, false},
	{"personal_data_exports", []string{"id"}, []string{"created_at", "completed_at", "expires_at"}, false},
	{"recently_viewed", []string{"user_id", "workspace_id", "resource_type", "resource_id"}, []string{"last_viewed_at"}, false},
	{"record_journal", []string{"id"}, []string{"created_at"}, true},
	{"scim_group_memberships", []string{"group_id", "user_id"}, []string{"updated_at"}, false},
	{"scim_groups", []string{"id"}, []string{"created_at", "updated_at"}, false},
	{"scim_team_memberships", []string{"group_id", "user_id"}, []string{"updated_at"}, false},
	{"scim_tokens", []string{"id"}, []string{"created_at", "last_used_at", "revoked_at"}, false},
	{"scim_user_roles", []string{"workspace_id", "user_id"}, []string{"updated_at"}, false},
	{"search_history", []string{"user_id", "workspace_id", "query"}, []string{"last_used_at"}, false},
	{"team_default_favorite_overrides", []string{"workspace_key", "user_id", "resource_type", "resource_id"}, []string{"updated_at"}, false},
	{"team_default_favorites", []string{"id"}, []string{"created_at", "updated_at"}, false},
	{"team_memberships", []string{"workspace_id", "team_id", "user_id"}, []string{"joined_at"}, false},
	{"workspace_deletion_receipts", []string{"workspace_id"}, []string{"requested_at", "started_at", "completed_at"}, false},
	{"workspace_departures", []string{"workspace_id", "user_id"}, []string{"joined_at", "left_at"}, false},
	{"workspace_invitations", []string{"id"}, []string{"created_at", "expires_at", "accepted_at"}, false},
	{"workspace_key_renames", []string{"workspace_id", "old_key"}, []string{"renamed_at"}, false},
	{"workspace_memberships", []string{"workspace_id", "user_id"}, []string{"joined_at", "last_seen_at"}, false},
	{"workspace_states", []string{"workspace_key"}, []string{"updated_at"}, true},
}

// Run under the schema startup lock, after the tables exist and before
// metadata is loaded. Old writers must be stopped before the upgrade.
func (s *SQLiteStore) migrateStoredTimes(ctx context.Context) error {
	applied, err := s.migrationApplied(ctx, storedTimeMigrationVersion)
	if err != nil || applied {
		return err
	}
	for _, table := range storedTimeTables {
		if table.name == "auth_email_events" {
			// Preserve lazy creation on installations without a ledger.
			columns, err := s.renameTableColumns(ctx, table.name)
			if err != nil {
				return err
			}
			if len(columns) == 0 {
				continue
			}
		}
		batchSize := 128
		if table.largeRows {
			batchSize = 32
		}
		var after []string
		for {
			after, err = s.migrateStoredTimeBatch(ctx, table, after, batchSize)
			if err != nil {
				return fmt.Errorf("database migration %d (%s): %w", storedTimeMigrationVersion, table.name, err)
			}
			if after == nil {
				break
			}
		}
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO schema_migrations(version,name,applied_at) VALUES(?,?,?)`, storedTimeMigrationVersion, "fixed-width UTC timestamps", formatStoredTime(time.Now()))
	return err
}

func (s *SQLiteStore) migrateStoredTimeBatch(ctx context.Context, table storedTimeTable, after []string, limit int) ([]string, error) {
	// Keep the read and write together. This is a representation change, so
	// use the same SQL triggers as normal writes without staging app events
	// or advancing workspace revisions in the application transaction wrapper.
	options := claimTxOptions
	if s.dialect == "sqlite" {
		options = nil
	}
	tx, err := s.db.DB.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	columns := append(append([]string{}, table.key...), table.times...)
	query := "SELECT " + strings.Join(columns, ",") + " FROM " + table.name
	var args []any
	switch {
	case after == nil:
	case len(table.key) == 1:
		query += " WHERE " + table.key[0] + ">?"
		args = append(args, after[0])
	case s.dialect != "mysql":
		query += " WHERE (" + strings.Join(table.key, ",") + ")>(" + placeholders(len(table.key)) + ")"
		for _, value := range after {
			args = append(args, value)
		}
	default:
		// Expand the composite key comparison so MySQL can use its whole
		// primary key to seek into the next batch.
		parts := make([]string, len(table.key))
		for i, column := range table.key {
			parts[i] = "("
			for j, prefix := range table.key[:i] {
				parts[i] += prefix + "=? AND "
				args = append(args, after[j])
			}
			parts[i] += column + ">?)"
			args = append(args, after[i])
		}
		query += " WHERE " + strings.Join(parts, " OR ")
	}
	query += " ORDER BY " + strings.Join(table.key, ",") + " LIMIT ?" + s.lockClause()
	rows, err := tx.QueryContext(ctx, rewriteSQL(query, s.dialect), append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	key := make([]string, len(table.key))
	times := make([]sql.NullString, len(table.times))
	dest := make([]any, len(columns))
	for i := range key {
		dest[i] = &key[i]
	}
	for i := range times {
		dest[len(key)+i] = &times[i]
	}
	count, changed := 0, 0
	args = nil
	for rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		count++
		dirty := false
		for i := range times {
			if times[i].Valid {
				next := normalizeStoredTime(times[i].String)
				dirty = dirty || next != times[i].String
				times[i].String = next
			}
		}
		if !dirty {
			continue
		}
		changed++
		for _, value := range key {
			args = append(args, value)
		}
		for _, value := range times {
			args = append(args, value)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if changed != 0 {
		query := storedTimeBatchUpdate(s.dialect, table, columns, changed)
		if _, err := tx.ExecContext(ctx, rewriteSQL(query, s.dialect), args...); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if count < limit {
		return nil, nil
	}
	return key, nil
}

func storedTimeBatchUpdate(dialect string, table storedTimeTable, columns []string, count int) string {
	match := make([]string, len(table.key))
	for i, column := range table.key {
		match[i] = "t." + column + "=v." + column
	}
	set := make([]string, len(table.times))
	for i, column := range table.times {
		set[i] = column + "=v." + column
		if dialect == "mysql" {
			set[i] = "t." + set[i]
		}
	}
	if dialect == "mysql" {
		// MySQL uses an UPDATE JOIN. The derived table has one row per
		// changed primary key, so the join can look up just those rows.
		first := make([]string, len(columns))
		for i, column := range columns {
			first[i] = "? AS " + column
		}
		input := "SELECT " + strings.Join(first, ",") + strings.Repeat(" UNION ALL SELECT "+placeholders(len(columns)), count-1)
		return "UPDATE (" + input + ") AS v STRAIGHT_JOIN " + table.name + " AS t ON " + strings.Join(match, " AND ") + " SET " + strings.Join(set, ",")
	}
	mark := "?"
	if dialect == "postgres" {
		mark = "CAST(? AS TEXT)"
	}
	tuple := "(" + strings.TrimSuffix(strings.Repeat(mark+",", len(columns)), ",") + ")"
	input := tuple + strings.Repeat(","+tuple, count-1)
	return "WITH v(" + strings.Join(columns, ",") + ") AS (VALUES " + input + ") UPDATE " + table.name +
		" AS t SET " + strings.Join(set, ",") + " FROM v WHERE " + strings.Join(match, " AND ")
}
