package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// Renaming a workspace's URL key.
//
// A workspace's rows are addressed three ways, and a rename (updateWorkspace)
// keeps each of them with the workspace:
//
//   - by its URL key, in every table with a workspace_key column. The set is
//     derived from workspacePurgeTables, so a table the purge knows about is
//     moved too; TestWorkspaceRenameCoversEverySchemaTable fails when a
//     table with a workspace_key column is in neither list. All of them move
//     in the rename's transaction. JSON values in those rows (and in OAuth
//     grants) that name the workspace ("workspaceKey":"<key>") are rewritten
//     in the same transaction.
//   - by its ID, in the workspace_id tables (memberships, SCIM, invitations,
//     domain events, ...). The ID never changes, so these rows stay.
//   - by values derived from the key: connector credential IDs (a hash of the
//     key, an owner and a connector), app developer token references
//     ("devtoken:<key>:<client>"), offloaded content objects
//     ("<key>/<blob>.json.gz" in the content store) and coding harness state
//     objects (renamed by the server's WorkspaceKeyObjects). Credential IDs and
//     references are rewritten in the transaction; objects are copied to the
//     new key before it commits and the old copies deleted after, so a crash
//     leaves an extra copy, never a missing one.
//
// Writers that still hold the old key (an agent run or coding session that
// started before the rename) can write rows under it afterwards. Every rename
// is recorded in workspace_key_renames, and the storage maintenance pass moves
// rows found under a recorded previous key to the current one
// (AdoptRenamedWorkspaceRows) for workspaceKeyAdoptWindow. Data migration 1040
// does the same for renames made before this existed, finding previous keys in
// the domain events.

// workspaceKeyAdoptWindow is how long after a rename the maintenance pass
// looks for rows written under the previous key.
const workspaceKeyAdoptWindow = 7 * 24 * time.Hour

// workspaceRenameSkipped lists tables with a workspace_key column that the
// generic rename does not move, and why.
var workspaceRenameSkipped = map[string]string{
	"workspace_states":            "the catalog row: updateWorkspace deletes it and writes the workspace under its new key",
	"workspace_metadata_records":  "moved by its own literal statement, which moves the metadata cache revision of the new key",
	"workspace_deletion_receipts": "a receipt keeps the key the workspace had when its purge started; a workspace being purged cannot be renamed",
}

// workspaceRenameContentTables hold user content, or indexes and caches built
// from it. Their rows move, but their values are not searched for the old key:
// text people wrote keeps the links they wrote, and these tables do not store
// a workspaceKey field.
var workspaceRenameContentTables = map[string]bool{
	"record_journal_coverage":        true,
	"record_journal_dirty":           true,
	"record_versions":                true,
	"metadata_catalog":               true,
	"metadata_catalog_relations":     true,
	"metadata_catalog_collections":   true,
	"workspace_content_records":      true,
	"issue_records":                  true,
	"issue_search_documents":         true,
	"metadata_search_documents":      true,
	"metadata_blobs":                 true,
	"document_collaboration_updates": true,
	"coding_snapshot_logs":           true,
}

// workspaceKeyJSONTables are tables without a workspace column whose JSON
// values name a workspace by its URL key.
var workspaceKeyJSONTables = []string{"oauth_authorization_codes", "oauth_refresh_tokens"}

// workspaceRenameTables lists the tables whose rows move with the key.
func workspaceRenameTables() []string {
	tables := []string{}
	for _, table := range workspacePurgeTables {
		if table.byID || workspaceRenameSkipped[table.table] != "" {
			continue
		}
		tables = append(tables, table.table)
	}
	return tables
}

// workspaceCredentialOwners are the fixed owners connector credentials are
// stored under besides members: provider credentials of integrations, GitLab
// member sign-in apps and integration OAuth tokens.
var workspaceCredentialOwners = []string{"provider", "gitlab-member-oauth", integrationOAuthOwner}

// WorkspaceKeyMove names a workspace whose objects move from one URL key to
// another.
type WorkspaceKeyMove struct {
	WorkspaceID, OldKey, NewKey string
	// CodingSessionIDs are the workspace's coding sessions.
	CodingSessionIDs []string
}

// WorkspaceKeyObjects moves objects outside the database whose names embed
// the workspace URL key (the server's coding harness state). The store calls
// CopyWorkspaceKeyObjects before a rename commits and DeleteWorkspaceKeyObjects
// for the old key after it committed (or for the new key when it failed). Both
// must be idempotent, and a copy must keep an object already at the
// destination.
type WorkspaceKeyObjects interface {
	CopyWorkspaceKeyObjects(ctx context.Context, move WorkspaceKeyMove) error
	DeleteWorkspaceKeyObjects(ctx context.Context, workspaceKey string, sessionIDs []string) error
}

// SetWorkspaceKeyObjects registers what moves the server's key-named objects.
func (s *SQLiteStore) SetWorkspaceKeyObjects(objects WorkspaceKeyObjects) {
	if objects == nil {
		s.keyObjects.Store(nil)
		return
	}
	s.keyObjects.Store(&objects)
}

func (s *SQLiteStore) workspaceKeyObjects() WorkspaceKeyObjects {
	if objects := s.keyObjects.Load(); objects != nil {
		return *objects
	}
	return nil
}

func (s *SQLiteStore) ensureWorkspaceKeyRenames(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS workspace_key_renames(workspace_id VARCHAR(191) NOT NULL,old_key VARCHAR(191) NOT NULL,new_key VARCHAR(191) NOT NULL,renamed_at VARCHAR(40) NOT NULL,PRIMARY KEY(workspace_id,old_key))`)
	return err
}

// ---------------------------------------------------------------- schema

type renameColumn struct {
	name   string
	binary bool
}

// workspaceKeyTablePlan is how one table's rows move: its unique keys that
// include workspace_key (the other columns of each), and the columns that
// may hold JSON naming the workspace.
type workspaceKeyTablePlan struct {
	table  string
	unique [][]string
	json   []renameColumn
}

// workspaceKeyMovePlan reads the schema of every table a rename touches. It
// runs before the move's transaction (SQLite may have one connection).
func (s *SQLiteStore) workspaceKeyMovePlan(ctx context.Context) ([]workspaceKeyTablePlan, error) {
	plans := []workspaceKeyTablePlan{}
	for _, table := range workspaceRenameTables() {
		plan := workspaceKeyTablePlan{table: table}
		columns, err := s.renameTableColumns(ctx, table)
		if err != nil {
			return nil, fmt.Errorf("%s columns: %w", table, err)
		}
		if !workspaceRenameContentTables[table] {
			for _, column := range columns {
				if column.name != "workspace_key" && jsonCapableColumn(s.dialect, column) {
					plan.json = append(plan.json, renameColumn{name: column.name, binary: binaryColumn(s.dialect, column)})
				}
			}
		}
		if plan.unique, err = s.workspaceUniqueKeys(ctx, table); err != nil {
			return nil, fmt.Errorf("%s keys: %w", table, err)
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

type tableColumn struct{ name, kind string }

func (s *SQLiteStore) renameTableColumns(ctx context.Context, table string) ([]tableColumn, error) {
	query := `SELECT name,type FROM pragma_table_info(?)`
	switch s.dialect {
	case "postgres":
		query = `SELECT column_name,data_type FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=? ORDER BY ordinal_position`
	case "mysql":
		query = `SELECT column_name,data_type FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name=? ORDER BY ordinal_position`
	}
	rows, err := s.db.QueryContext(ctx, query, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := []tableColumn{}
	for rows.Next() {
		var column tableColumn
		if err := rows.Scan(&column.name, &column.kind); err != nil {
			return nil, err
		}
		column.name, column.kind = strings.ToLower(column.name), strings.ToLower(column.kind)
		columns = append(columns, column)
	}
	return columns, rows.Err()
}

// jsonCapableColumn reports a TEXT or BLOB column (VARCHAR columns hold
// identifiers, not JSON documents).
func jsonCapableColumn(dialect string, column tableColumn) bool {
	switch dialect {
	case "postgres":
		return column.kind == "text" || column.kind == "bytea"
	case "mysql":
		return strings.HasSuffix(column.kind, "text") || strings.HasSuffix(column.kind, "blob")
	default:
		return strings.Contains(column.kind, "text") || strings.Contains(column.kind, "blob")
	}
}

func binaryColumn(dialect string, column tableColumn) bool {
	return dialect == "postgres" && column.kind == "bytea"
}

// workspaceUniqueKeys returns, for each primary or unique key of table that
// includes workspace_key, its other columns.
func (s *SQLiteStore) workspaceUniqueKeys(ctx context.Context, table string) ([][]string, error) {
	indexes := map[string][]string{}
	order := []string{}
	add := func(index, column string) {
		if _, ok := indexes[index]; !ok {
			order = append(order, index)
		}
		indexes[index] = append(indexes[index], strings.ToLower(column))
	}
	switch s.dialect {
	case "postgres", "mysql":
		query := `SELECT index_name,column_name FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name=? AND non_unique=0 ORDER BY index_name,seq_in_index`
		if s.dialect == "postgres" {
			query = `SELECT ic.relname,a.attname FROM pg_index x JOIN pg_class t ON t.oid=x.indrelid JOIN pg_namespace n ON n.oid=t.relnamespace JOIN pg_class ic ON ic.oid=x.indexrelid JOIN pg_attribute a ON a.attrelid=t.oid AND a.attnum=ANY(x.indkey) WHERE n.nspname=current_schema() AND t.relname=? AND x.indisunique ORDER BY ic.relname,a.attname`
		}
		rows, err := s.db.QueryContext(ctx, query, table)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var index, column string
			if err := rows.Scan(&index, &column); err != nil {
				rows.Close()
				return nil, err
			}
			add(index, column)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	default:
		names, err := s.workspaceStrings(ctx, `SELECT name FROM pragma_index_list(?) WHERE "unique"=1`, table)
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			columns, err := s.workspaceStrings(ctx, `SELECT name FROM pragma_index_info(?) ORDER BY seqno`, name)
			if err != nil {
				return nil, err
			}
			for _, column := range columns {
				add(name, column)
			}
		}
	}
	keys := [][]string{}
	for _, index := range order {
		columns := indexes[index]
		if !slices.Contains(columns, "workspace_key") {
			continue
		}
		others := []string{}
		for _, column := range columns {
			if column != "workspace_key" {
				others = append(others, column)
			}
		}
		keys = append(keys, others)
	}
	return keys, nil
}

// ---------------------------------------------------------------- rows

// workspaceKeyInUse reports whether any table still holds rows under key.
// A key that does (data of a deleted workspace whose rows were not purged, or
// rows written under a workspace's previous key) is not given to another
// workspace: its rows would show up there.
func (s *SQLiteStore) workspaceKeyInUse(ctx context.Context, key string) (string, error) {
	for _, table := range append(workspaceRenameTables(), "workspace_metadata_records") {
		var found int
		err := s.db.QueryRowContext(ctx, `SELECT 1 FROM `+table+` WHERE workspace_key=? LIMIT 1`, key).Scan(&found)
		if err == nil {
			return table, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("%s: %w", table, err)
		}
	}
	return "", nil
}

// moveWorkspaceKeyRows moves every row under oldKey to newKey inside tx and
// rewrites the JSON values naming oldKey. With adopt, rows that a unique key
// says the new key already has are deleted first (the current row wins);
// without it the caller made sure newKey holds no rows. It returns how many
// rows moved.
func (s *SQLiteStore) moveWorkspaceKeyRows(ctx context.Context, tx *sqlTx, plans []workspaceKeyTablePlan, oldKey, newKey string, adopt bool) (int64, error) {
	var moved int64
	for _, plan := range plans {
		if adopt {
			for _, others := range plan.unique {
				if _, err := tx.ExecContext(ctx, s.conflictingKeyRowsStatement(plan.table, others), s.conflictingKeyRowsArgs(oldKey, newKey)...); err != nil {
					return moved, fmt.Errorf("%s: %w", plan.table, err)
				}
			}
		}
		// Only the rows that move are searched for JSON naming the old key:
		// an adopt rewrites them while they are still under it (the new key
		// holds the whole workspace); a rename after the move, under the new
		// key, which then holds just them (chat rows included, which
		// renameChatWorkspace moved first).
		rewrite := func(filter string) error {
			for _, column := range plan.json {
				if err := s.rewriteWorkspaceKeyJSON(ctx, tx, plan.table, column, filter, oldKey, newKey); err != nil {
					return fmt.Errorf("%s.%s: %w", plan.table, column.name, err)
				}
			}
			return nil
		}
		if adopt {
			if err := rewrite(oldKey); err != nil {
				return moved, err
			}
		}
		result, err := tx.ExecContext(ctx, `UPDATE `+plan.table+` SET workspace_key=? WHERE workspace_key=?`, newKey, oldKey)
		if err != nil {
			return moved, fmt.Errorf("%s: %w", plan.table, err)
		}
		moved += rowsAffected(result)
		if !adopt {
			if err := rewrite(newKey); err != nil {
				return moved, err
			}
		}
	}
	for _, table := range workspaceKeyJSONTables {
		if err := s.rewriteWorkspaceKeyJSON(ctx, tx, table, renameColumn{name: "data", binary: s.dialect == "postgres"}, "", oldKey, newKey); err != nil {
			return moved, fmt.Errorf("%s: %w", table, err)
		}
	}
	// App developer tokens are referenced as devtoken:<key>:<client>.
	refs, err := txStrings(ctx, tx, `SELECT ref FROM webhook_signing_secrets WHERE workspace_key=? AND ref LIKE ?`, newKey, "devtoken:%")
	if err != nil {
		return moved, err
	}
	for _, ref := range refs {
		client, ok := strings.CutPrefix(ref, "devtoken:"+oldKey+":")
		if !ok {
			continue
		}
		next := applicationDeveloperTokenRef(newKey, client)
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM webhook_signing_secrets WHERE ref=?`, next).Scan(&exists); err != nil {
			return moved, err
		}
		if exists > 0 && adopt {
			// The current key's token wins.
			if _, err := tx.ExecContext(ctx, `DELETE FROM webhook_signing_secrets WHERE ref=?`, ref); err != nil {
				return moved, err
			}
			continue
		}
		if exists > 0 {
			if _, err := tx.ExecContext(ctx, `DELETE FROM webhook_signing_secrets WHERE ref=?`, next); err != nil {
				return moved, err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE webhook_signing_secrets SET ref=? WHERE ref=?`, next, ref); err != nil {
			return moved, err
		}
	}
	// Accounts that last opened the workspace open it under its new key.
	if _, err := tx.ExecContext(ctx, `UPDATE auth_account_state SET last_workspace_key=?,updated_at=? WHERE last_workspace_key=?`, newKey, time.Now().UTC().Format(time.RFC3339Nano), oldKey); err != nil {
		return moved, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE account_state SET last_workspace_key=? WHERE last_workspace_key=?`, newKey, oldKey); err != nil {
		return moved, err
	}
	return moved, nil
}

// conflictingKeyRowsStatement deletes the rows under the old key (first
// bound value) whose unique key the new key (second) already has.
func (s *SQLiteStore) conflictingKeyRowsStatement(table string, others []string) string {
	match := func(left, right string) string {
		clauses := ""
		for _, column := range others {
			clauses += ` AND ` + left + `.` + column + `=` + right + `.` + column
		}
		return clauses
	}
	switch s.dialect {
	case "postgres":
		return `DELETE FROM ` + table + ` AS o USING ` + table + ` AS c WHERE o.workspace_key=? AND c.workspace_key=?` + match("c", "o")
	case "mysql":
		// MySQL binds the join's value before the WHERE clause's: see
		// conflictingKeyRowsArgs.
		return `DELETE o FROM ` + table + ` AS o JOIN ` + table + ` AS c ON c.workspace_key=?` + match("c", "o") + ` WHERE o.workspace_key=?`
	default:
		return `DELETE FROM ` + table + ` WHERE workspace_key=? AND EXISTS (SELECT 1 FROM ` + table + ` AS c WHERE c.workspace_key=?` + match("c", table) + `)`
	}
}

func (s *SQLiteStore) conflictingKeyRowsArgs(oldKey, newKey string) []any {
	if s.dialect == "mysql" {
		return []any{newKey, oldKey}
	}
	return []any{oldKey, newKey}
}

// workspaceKeyJSONPatterns are the JSON members that name a workspace by its
// URL key, as encoding/json writes them.
func workspaceKeyJSONPatterns(key string) []string {
	encoded, _ := json.Marshal(key)
	return []string{`"workspaceKey":` + string(encoded), `"workspace":` + string(encoded)}
}

// rewriteWorkspaceKeyJSON replaces the members naming oldKey in a column,
// in the rows under filter (every row when filter is empty).
func (s *SQLiteStore) rewriteWorkspaceKeyJSON(ctx context.Context, tx *sqlTx, table string, column renameColumn, filter, oldKey, newKey string) error {
	olds, news := workspaceKeyJSONPatterns(oldKey), workspaceKeyJSONPatterns(newKey)
	name := column.name
	for index := range olds {
		var set, match string
		switch {
		case s.dialect == "postgres" && column.binary:
			set = `convert_to(replace(convert_from(` + name + `,'UTF8'),?,?),'UTF8')`
			match = `position(convert_to(?,'UTF8') IN ` + name + `)>0`
		case s.dialect == "postgres":
			set, match = `replace(`+name+`,?,?)`, `strpos(`+name+`,?)>0`
		case s.dialect == "mysql":
			set, match = `REPLACE(`+name+`,?,?)`, `LOCATE(?,`+name+`)>0`
		default:
			// Keep a BLOB a BLOB: replace() returns text.
			set = `CASE WHEN typeof(` + name + `)='blob' THEN CAST(replace(CAST(` + name + ` AS TEXT),?,?) AS BLOB) ELSE replace(` + name + `,?,?) END`
			match = `instr(` + name + `,?)>0`
		}
		args := []any{olds[index], news[index]}
		if s.dialect == "sqlite" {
			args = append(args, olds[index], news[index])
		}
		where := match
		if filter != "" {
			where = `workspace_key=? AND ` + match
			args = append(args, filter)
		}
		args = append(args, olds[index])
		if _, err := tx.ExecContext(ctx, `UPDATE `+table+` SET `+name+`=`+set+` WHERE `+where, args...); err != nil {
			return err
		}
	}
	return nil
}

func txStrings(ctx context.Context, tx *sqlTx, query string, args ...any) ([]string, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []string{}
	for rows.Next() {
		var value sql.NullString
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		if value.String != "" {
			values = append(values, value.String)
		}
	}
	return values, rows.Err()
}

// ---------------------------------------------------------------- credentials

// workspaceCredentialPairs lists the (owner, connector) pairs a workspace's
// connector credentials can be stored under: its integration connections and
// connector policies, owned by a fixed role, a member (current or former) or
// a connector's owner.
func (s *SQLiteStore) workspaceCredentialPairs(ctx context.Context, data domain.Bootstrap) ([][2]string, error) {
	owners := map[string]bool{}
	for _, owner := range workspaceCredentialOwners {
		owners[owner] = true
	}
	members, err := s.WorkspaceUserIDs(ctx, data.Workspace.ID)
	if err != nil {
		return nil, err
	}
	for _, id := range members {
		owners[id] = true
	}
	for _, user := range data.Users {
		owners[user.ID] = true
	}
	connectors := map[string]bool{}
	for _, connection := range data.IntegrationConnections {
		connectors[connection.ID] = true
	}
	var policies []struct {
		ID      string `json:"id"`
		OwnerID string `json:"ownerId"`
	}
	if raw, err := json.Marshal(data.Settings["applicationPolicies"]); err == nil {
		_ = json.Unmarshal(raw, &policies)
	}
	for _, policy := range policies {
		connectors[policy.ID] = true
		if policy.OwnerID != "" {
			owners[policy.OwnerID] = true
		}
	}
	pairs := [][2]string{}
	for connector := range connectors {
		if connector == "" {
			continue
		}
		for owner := range owners {
			if owner != "" {
				pairs = append(pairs, [2]string{owner, connector})
			}
		}
	}
	slices.SortFunc(pairs, func(a, b [2]string) int { return strings.Compare(a[0]+"\x00"+a[1], b[0]+"\x00"+b[1]) })
	return pairs, nil
}

// WorkspaceCredentialSecretIDs lists the connector_secrets ids the
// workspace's per-member, provider and integration credentials can have under
// its URL key.
func (s *SQLiteStore) WorkspaceCredentialSecretIDs(ctx context.Context, workspaceKey string) ([]string, error) {
	data, ok := s.WorkspaceMetadata(workspaceKey)
	if !ok {
		return nil, nil
	}
	pairs, err := s.workspaceCredentialPairs(ctx, data)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(pairs))
	for index, pair := range pairs {
		ids[index] = ConnectorSecretID(workspaceKey, pair[0], pair[1])
	}
	return ids, nil
}

// resealConnectorSecret re-encrypts a sealed credential for another id: the
// id is the ciphertext's associated data (PutConnectorSecret), so a renamed
// row must be sealed again to stay readable. It opens with the keyring (the
// current key or a retired one) and seals under the current key.
func resealConnectorSecret(id string, encrypted []byte, next string) ([]byte, error) {
	plain, _, err := openSecret(encrypted, []byte(id))
	if err != nil {
		return nil, err
	}
	return sealSecret(plain, []byte(next))
}

// moveWorkspaceCredentials moves the stored credentials of pairs from
// oldKey's ids to newKey's, sealed again for the new id. With adopt a
// credential the new key already has wins; without it the old (live)
// credential replaces a stale one. A credential that cannot be opened (no or
// another FLOW_CONNECTOR_SECRET_KEY) stays where it is and is counted in the
// log, never in clear text.
func moveWorkspaceCredentials(ctx context.Context, tx *sqlTx, pairs [][2]string, oldKey, newKey string, adopt bool) (int64, error) {
	var moved int64
	unreadable := 0
	for start := 0; start < len(pairs); start += 250 {
		batch := pairs[start:min(start+250, len(pairs))]
		byOld := map[string]string{}
		olds := make([]string, len(batch))
		for index, pair := range batch {
			olds[index] = ConnectorSecretID(oldKey, pair[0], pair[1])
			byOld[olds[index]] = ConnectorSecretID(newKey, pair[0], pair[1])
		}
		clause, args := bindList("id", olds)
		rows, err := tx.QueryContext(ctx, `SELECT id,ciphertext,expires_at FROM connector_secrets WHERE `+clause, args...)
		if err != nil {
			return moved, err
		}
		type sealed struct {
			id, expires string
			ciphertext  []byte
		}
		found := []sealed{}
		for rows.Next() {
			var item sealed
			if err := rows.Scan(&item.id, &item.ciphertext, &item.expires); err != nil {
				rows.Close()
				return moved, err
			}
			found = append(found, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return moved, err
		}
		for _, item := range found {
			next := byOld[item.id]
			var exists int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM connector_secrets WHERE id=?`, next).Scan(&exists); err != nil {
				return moved, err
			}
			if exists > 0 && adopt {
				if _, err := tx.ExecContext(ctx, `DELETE FROM connector_secrets WHERE id=?`, item.id); err != nil {
					return moved, err
				}
				continue
			}
			resealed, err := resealConnectorSecret(item.id, item.ciphertext, next)
			if err != nil {
				unreadable++
				continue
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM connector_secrets WHERE id=?`, next); err != nil {
				return moved, err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO connector_secrets(id,ciphertext,expires_at) VALUES(?,?,?)`, next, resealed, item.expires); err != nil {
				return moved, err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM connector_secrets WHERE id=?`, item.id); err != nil {
				return moved, err
			}
			moved++
		}
	}
	if unreadable > 0 {
		slog.Warn("connector credentials could not be moved to a renamed workspace key", "credentials", unreadable)
	}
	return moved, nil
}

// ---------------------------------------------------------------- objects

// offloadedObjectKeys lists the content-store bodies under key (rows whose
// body is not in the database).
func (s *SQLiteStore) offloadedObjectKeys(ctx context.Context, key string) ([]string, error) {
	if s.db.content == nil || s.db.content.objects == nil {
		return nil, nil
	}
	return s.workspaceStrings(ctx, `SELECT blob_key FROM metadata_blobs WHERE workspace_key=? AND data IS NULL ORDER BY blob_key`, key)
}

// copyOffloadedObjects copies bodies from oldKey's names to newKey's. Bodies
// are content-addressed, so a copy that exists already holds the same bytes.
// A body missing under the old key is skipped (and logged by count).
func (s *SQLiteStore) copyOffloadedObjects(ctx context.Context, oldKey, newKey string, keys []string, done map[string]bool) error {
	content := s.db.content
	missing := 0
	for _, key := range keys {
		if done[key] {
			continue
		}
		reader, _, _, err := content.objects.Open(ctx, offloadedObjectKey(oldKey, key))
		if err != nil {
			message := strings.ToLower(err.Error())
			if errors.Is(err, fs.ErrNotExist) || strings.Contains(message, "nosuchkey") || strings.Contains(message, "not found") {
				missing++
				done[key] = true
				continue
			}
			return fmt.Errorf("read offloaded content: %w", err)
		}
		body, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			return fmt.Errorf("read offloaded content: %w", err)
		}
		if _, err := content.objects.Put(ctx, offloadedObjectKey(newKey, key), bytes.NewReader(body), "application/gzip"); err != nil {
			return fmt.Errorf("copy offloaded content: %w", err)
		}
		done[key] = true
	}
	if missing > 0 {
		slog.Warn("offloaded bodies missing while moving a workspace key", "missing", missing)
	}
	return nil
}

// deleteOffloadedObjects deletes bodies stored under key (after their rows
// moved away); a failure leaves an unreferenced copy, which is logged.
func (s *SQLiteStore) deleteOffloadedObjects(ctx context.Context, key string, keys []string) {
	content := s.db.content
	if content == nil || content.objects == nil {
		return
	}
	failed := 0
	for _, blob := range keys {
		content.cache.remove(key + "/" + blob)
		if err := content.objects.Delete(ctx, offloadedObjectKey(key, blob)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			failed++
		}
	}
	if failed > 0 {
		slog.Warn("offloaded bodies under a previous workspace key could not be deleted", "failed", failed)
	}
}

// ---------------------------------------------------------------- move

// workspaceKeyMove is a prepared move of one workspace's rows and objects.
type workspaceKeyMove struct {
	WorkspaceKeyMove
	adopt   bool
	plans   []workspaceKeyTablePlan
	pairs   [][2]string
	blobs   []string
	copied  map[string]bool
	objects WorkspaceKeyObjects
}

// prepareWorkspaceKeyMove reads what the move needs and copies the objects
// to the new key. It runs before the move's transaction.
func (s *SQLiteStore) prepareWorkspaceKeyMove(ctx context.Context, data domain.Bootstrap, oldKey, newKey string, adopt bool, plans []workspaceKeyTablePlan) (*workspaceKeyMove, error) {
	move := &workspaceKeyMove{WorkspaceKeyMove: WorkspaceKeyMove{WorkspaceID: data.Workspace.ID, OldKey: oldKey, NewKey: newKey}, adopt: adopt, plans: plans, copied: map[string]bool{}, objects: s.workspaceKeyObjects()}
	var err error
	if move.plans == nil {
		if move.plans, err = s.workspaceKeyMovePlan(ctx); err != nil {
			return nil, err
		}
	}
	if move.pairs, err = s.workspaceCredentialPairs(ctx, data); err != nil {
		return nil, err
	}
	sessions := map[string]bool{}
	for _, key := range []string{oldKey, newKey} {
		ids, err := s.workspaceStrings(ctx, `SELECT id FROM coding_sessions WHERE workspace_key=?`, key)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			sessions[id] = true
		}
	}
	for id := range sessions {
		move.CodingSessionIDs = append(move.CodingSessionIDs, id)
	}
	slices.Sort(move.CodingSessionIDs)
	if move.blobs, err = s.offloadedObjectKeys(ctx, oldKey); err != nil {
		return nil, err
	}
	if len(move.blobs) > 0 {
		if err := s.copyOffloadedObjects(ctx, oldKey, newKey, move.blobs, move.copied); err != nil {
			s.finishWorkspaceKeyMove(ctx, move, false)
			return nil, err
		}
	}
	if move.objects != nil && len(move.CodingSessionIDs) > 0 {
		if err := move.objects.CopyWorkspaceKeyObjects(ctx, move.WorkspaceKeyMove); err != nil {
			s.finishWorkspaceKeyMove(ctx, move, false)
			return nil, fmt.Errorf("copy workspace objects: %w", err)
		}
	}
	return move, nil
}

// apply moves the rows inside tx. Bodies offloaded under the old key since
// prepare are copied first.
func (s *SQLiteStore) applyWorkspaceKeyMove(ctx context.Context, tx *sqlTx, move *workspaceKeyMove) (int64, error) {
	if s.db.content != nil && s.db.content.objects != nil {
		late, err := txStrings(ctx, tx, `SELECT blob_key FROM metadata_blobs WHERE workspace_key=? AND data IS NULL`, move.OldKey)
		if err != nil {
			return 0, err
		}
		if err := s.copyOffloadedObjects(ctx, move.OldKey, move.NewKey, late, move.copied); err != nil {
			return 0, err
		}
		for _, key := range late {
			if !slices.Contains(move.blobs, key) {
				move.blobs = append(move.blobs, key)
			}
		}
	}
	moved, err := s.moveWorkspaceKeyRows(ctx, tx, move.plans, move.OldKey, move.NewKey, move.adopt)
	if err != nil {
		return moved, err
	}
	credentials, err := moveWorkspaceCredentials(ctx, tx, move.pairs, move.OldKey, move.NewKey, move.adopt)
	return moved + credentials, err
}

// finish runs after the transaction: on success the old copies of the
// objects go; on failure the new copies of a rename do (an adopt keeps them:
// the new key may have had them already).
func (s *SQLiteStore) finishWorkspaceKeyMove(ctx context.Context, move *workspaceKeyMove, committed bool) {
	ctx = context.WithoutCancel(ctx)
	if !committed {
		if !move.adopt {
			s.deleteOffloadedObjects(ctx, move.NewKey, move.blobs)
			if move.objects != nil && len(move.CodingSessionIDs) > 0 {
				if err := move.objects.DeleteWorkspaceKeyObjects(ctx, move.NewKey, move.CodingSessionIDs); err != nil {
					slog.Warn("workspace objects copied for a failed rename could not be deleted", "workspace_id", move.WorkspaceID, "error", err)
				}
			}
		}
		return
	}
	s.deleteOffloadedObjects(ctx, move.OldKey, move.blobs)
	if move.objects != nil && len(move.CodingSessionIDs) > 0 {
		if err := move.objects.DeleteWorkspaceKeyObjects(ctx, move.OldKey, move.CodingSessionIDs); err != nil {
			slog.Warn("workspace objects under a previous key could not be deleted", "workspace_id", move.WorkspaceID, "error", err)
		}
	}
	// The commit queued a metadata revision for the old key (its catalog row
	// was deleted); the key holds nothing else now.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM workspace_cache_revisions WHERE workspace_key=?`, move.OldKey); err != nil {
		slog.Warn("cache revisions of a previous workspace key could not be deleted", "workspace_id", move.WorkspaceID, "error", err)
	}
	// Shared caches are named by key: none keeps serving the workspace's data
	// under the old key (which a new workspace may take).
	for _, key := range []string{move.OldKey, move.NewKey} {
		s.invalidateDeletedTeamCache(ctx, key, true, true)
		s.dropMetadataCache(ctx, key)
		s.forgetChatSnapshotFlag(key)
		s.issueGroupCounts.drop(key)
	}
}

// recordWorkspaceRename remembers that the workspace had oldKey, inside the
// rename's transaction. A key the workspace takes back is no longer previous.
func recordWorkspaceRename(ctx context.Context, tx *sqlTx, workspaceID, oldKey, newKey string, at time.Time) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_key_renames WHERE workspace_id=? AND old_key=?`, workspaceID, newKey); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO workspace_key_renames(workspace_id,old_key,new_key,renamed_at) VALUES(?,?,?,?) ON CONFLICT(workspace_id,old_key) DO UPDATE SET new_key=excluded.new_key,renamed_at=excluded.renamed_at`, workspaceID, oldKey, newKey, at.UTC().Format(time.RFC3339Nano))
	return err
}

// ---------------------------------------------------------------- adopt

// WorkspaceKeyAdoption reports what AdoptRenamedWorkspaceRows did.
type WorkspaceKeyAdoption struct {
	// Moved counts rows (and credentials) moved to a current key.
	Moved int64
	// Ambiguous counts previous keys skipped because another workspace had
	// the key too.
	Ambiguous int
}

// AdoptRenamedWorkspaceRows moves rows found under a previous URL key of a
// workspace (workspace_key_renames, renamed at or after since; the zero time
// means every rename) to its current key. A previous key is skipped while a
// workspace has it, or when another workspace (live or deleted) had it too:
// its rows could be either's.
func (s *SQLiteStore) AdoptRenamedWorkspaceRows(ctx context.Context, since time.Time) (WorkspaceKeyAdoption, error) {
	var report WorkspaceKeyAdoption
	type rename struct{ workspaceID, oldKey, renamedAt string }
	rows, err := s.db.QueryContext(ctx, `SELECT workspace_id,old_key,renamed_at FROM workspace_key_renames ORDER BY workspace_id,old_key`)
	if err != nil {
		return report, err
	}
	renames := []rename{}
	holders := map[string]map[string]bool{}
	for rows.Next() {
		var item rename
		if err := rows.Scan(&item.workspaceID, &item.oldKey, &item.renamedAt); err != nil {
			rows.Close()
			return report, err
		}
		renames = append(renames, item)
		if holders[item.oldKey] == nil {
			holders[item.oldKey] = map[string]bool{}
		}
		holders[item.oldKey][item.workspaceID] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return report, err
	}
	receipts, err := s.WorkspaceDeletionReceipts(ctx, "", 100000)
	if err != nil {
		return report, err
	}
	for _, receipt := range receipts {
		if holders[receipt.WorkspaceKey] == nil {
			holders[receipt.WorkspaceKey] = map[string]bool{}
		}
		holders[receipt.WorkspaceKey][receipt.WorkspaceID] = true
	}
	var plans []workspaceKeyTablePlan
	for _, item := range renames {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		if !since.IsZero() {
			if at, err := time.Parse(time.RFC3339Nano, item.renamedAt); err == nil && at.Before(since) {
				continue
			}
		}
		if len(holders[item.oldKey]) > 1 {
			report.Ambiguous++
			continue
		}
		if plans == nil {
			if plans, err = s.workspaceKeyMovePlan(ctx); err != nil {
				return report, err
			}
		}
		moved, err := s.adoptWorkspaceKey(ctx, item.workspaceID, item.oldKey, plans)
		report.Moved += moved
		if err != nil {
			return report, fmt.Errorf("workspace %s: %w", item.workspaceID, err)
		}
	}
	return report, nil
}

// adoptWorkspaceKey moves the rows under oldKey to the workspace's current
// key, under the catalog lock so no rename or create runs meanwhile.
func (s *SQLiteStore) adoptWorkspaceKey(ctx context.Context, workspaceID, oldKey string, plans []workspaceKeyTablePlan) (int64, error) {
	var moved int64
	run := func() error {
		_, current, ok := s.workspaceByID(workspaceID)
		if !ok || current == oldKey || s.HasWorkspace(oldKey) {
			return nil
		}
		data, ok := s.WorkspaceMetadata(current)
		if !ok {
			return nil
		}
		// Rows, credentials and objects all move; a key that holds none of
		// them makes an empty transaction.
		move, err := s.prepareWorkspaceKeyMove(ctx, data, oldKey, current, true, plans)
		if err != nil {
			return err
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		moved, err = s.applyWorkspaceKeyMove(ctx, tx, move)
		if err == nil {
			err = tx.Commit()
		}
		_ = tx.Rollback()
		s.finishWorkspaceKeyMove(ctx, move, err == nil)
		if err != nil {
			moved = 0
			return err
		}
		if moved > 0 {
			slog.Info("moved rows written under a previous workspace key", "workspace_id", workspaceID, "rows", moved)
		}
		return nil
	}
	if s.coordinator == nil {
		return moved, run()
	}
	err := s.coordinator.WithWorkspaceLock(ctx, "__workspace_catalog__", run)
	return moved, err
}
