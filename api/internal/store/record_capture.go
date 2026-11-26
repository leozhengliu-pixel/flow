package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"slices"
	"strings"
)

// Application capture records the journal inputs of a MySQL write inside the
// writer's own transaction, in place of the canonical-table triggers that a
// restricted account (no TRIGGER, or no SUPER with binary logging on) cannot
// install. It produces exactly the rows the triggers in journal_schema.go
// write: monotone record_versions with tombstones, record_journal_dirty keys
// under the transaction token, workspace_authz_epochs advances and
// record_journal_event_pending markers. The commit flush, sequencing and
// readers are unchanged, so cursor order, rollback and coverage behave as
// they do with triggers.
//
// Every write to a journal table goes through sqlTx.ExecContext (the writer
// coverage test enforces it). For each one, capture reads the rows the
// statement can change before it runs (UPDATE/DELETE: the same filter, with
// FOR UPDATE, which takes the locks the statement takes anyway; INSERT with a
// conflict clause: only keys that already exist, so new keys are never
// gap-locked), runs it, reads the changed rows back and applies the trigger
// conditions to the before and after images. A statement it cannot analyse
// is still executed; capture then schedules a journal repair of every
// workspace in the same transaction (a safe resync, never a silent gap).

// captureTable describes one journal table.
type captureTable struct {
	name string
	pk   []string
	// relevant: assigning any other column cannot change a captured value.
	// Nil: every column is relevant.
	relevant map[string]bool
	// versioned tables keep record_versions; the others only advance the
	// authorization epoch and invalidate authorization.
	versioned bool
	// inserts and updates: whether those events are captured at all.
	inserts, updates bool
	// updateAlways: an update of a matched row is a change whatever it set.
	updateAlways bool
	// keyChange: changing a primary key column is a change (the triggers'
	// NEW.workspace_key<>OLD.workspace_key).
	keyChange bool
}

func captureColumnSet(names ...string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, name := range names {
		set[name] = true
	}
	return set
}

var captureTables = map[string]*captureTable{
	"workspace_metadata_records": {name: "workspace_metadata_records", pk: []string{"workspace_key", "field", "record_key"},
		relevant: captureColumnSet("workspace_key", "field", "record_key", "collection_order", "data"), versioned: true, inserts: true, updates: true, keyChange: true},
	"workspace_states": {name: "workspace_states", pk: []string{"workspace_key"},
		relevant: captureColumnSet("workspace_key", "data"), versioned: true, inserts: true, updates: true},
	"issue_records": {name: "issue_records", pk: []string{"workspace_key", "id"},
		relevant: captureColumnSet("workspace_key", "id", "collection_order", "data"), versioned: true, inserts: true, updates: true, keyChange: true},
	"workspace_content_records": {name: "workspace_content_records", pk: []string{"workspace_key", "kind", "resource_id", "id"},
		relevant:  captureColumnSet("workspace_key", "kind", "resource_id", "id", "data", "status", "next_attempt_at", "owner_id", "record_version"),
		versioned: true, inserts: true, updates: true, keyChange: true},
	"issue_permission_records": {name: "issue_permission_records", pk: []string{"workspace_key", "issue_id", "subject_type", "subject_id"},
		inserts: true, updates: true, updateAlways: true},
	"workspace_memberships": {name: "workspace_memberships", pk: []string{"workspace_id", "user_id"},
		relevant: captureColumnSet("workspace_id", "user_id", "role", "status"), inserts: true, updates: true, keyChange: true},
	"team_memberships": {name: "team_memberships", pk: []string{"workspace_id", "team_id", "user_id"},
		relevant: captureColumnSet("workspace_id", "team_id", "user_id", "role", "origin"), inserts: true, updates: true, keyChange: true},
	"auth_users": {name: "auth_users", pk: []string{"id"},
		relevant: captureColumnSet("id", "active", "deleted_at"), updates: true},
	"domain_events": {name: "domain_events", pk: []string{"id"}, inserts: true},
}

// metadataPolicyFields are the records whose change revokes cached
// authorization (the metadata trigger's epoch condition).
var metadataPolicyFields = captureColumnSet("teams", "teamSettings", "documents", "projects", "initiatives", "apiKeys", "oauthApplications", "oauthAuthorizations", "users", "workspaceSettings", "settings")

func (t *captureTable) touched(assigned map[string]captureValue) bool {
	if !t.updates {
		return false
	}
	if t.relevant == nil {
		return true
	}
	for column := range assigned {
		if t.relevant[column] {
			return true
		}
	}
	return false
}

// onlyRelevant: every assigned column is relevant, so any change the
// database reports is a captured change.
func (t *captureTable) onlyRelevant(assigned map[string]captureValue) bool {
	if t.relevant == nil {
		return true
	}
	for column := range assigned {
		if !t.relevant[column] {
			return false
		}
	}
	return true
}

// columns are the expressions capture reads for a row of the table
// qualified by q: the primary key, then the journal workspace, field and
// record key, the compared state, the metadata policy state and an
// auxiliary value (the workspace ID of a root).
func (t *captureTable) columns(q string) []string {
	c := func(name string) string { return q + "." + name }
	cols := make([]string, 0, len(t.pk)+6)
	for _, column := range t.pk {
		cols = append(cols, c(column))
	}
	json := func(path string) string {
		text := "CONVERT(" + c("data") + " USING utf8mb4)"
		return "SHA2(CAST(JSON_REMOVE(" + text + ",'$." + path + "') AS BINARY),256)"
	}
	valid := "JSON_VALID(CONVERT(" + c("data") + " USING utf8mb4))"
	switch t.name {
	case "workspace_metadata_records":
		policy := "CASE WHEN " + c("field") + " IN ('apiKeys','oauthAuthorizations') AND " + valid + " THEN " + json("lastUsedAt") +
			" WHEN " + c("field") + "='teamSettings' AND " + valid + " THEN " + json("timezone") + " ELSE SHA2(" + c("data") + ",256) END"
		cols = append(cols, c("workspace_key"), c("field"), c("record_key"), "SHA2(CONCAT("+c("collection_order")+",'|',"+c("data")+"),256)", policy, "''")
	case "workspace_states":
		cols = append(cols, c("workspace_key"), "'@core'", "'root'", "SHA2("+c("data")+",256)", "''", c("workspace_id"))
	case "issue_records":
		cols = append(cols, c("workspace_key"), "'issues'", c("id"), "SHA2(CONCAT("+c("collection_order")+",'|',"+c("data")+"),256)", "''", "''")
	case "workspace_content_records":
		field := "CASE " + c("kind") + " WHEN 'comment' THEN 'comments' WHEN 'activity' THEN 'activities' WHEN 'notification' THEN 'notifications' WHEN 'delivery' THEN 'notificationDeliveries' ELSE " + c("kind") + " END"
		state := "SHA2(CONCAT_WS('|'," + c("status") + "," + c("next_attempt_at") + "," + c("owner_id") + "," + c("record_version") + "," + c("data") + "),256)"
		cols = append(cols, c("workspace_key"), field, recordJournalContentKeySQL("mysql", c("resource_id"), c("id")), state, "''", "''")
	case "issue_permission_records":
		cols = append(cols, c("workspace_key"), "'@authz'", c("issue_id"), "CONCAT("+c("role")+",'')", "''", "''")
	case "workspace_memberships":
		cols = append(cols, c("workspace_id"), "'@authz'", c("user_id"), "CONCAT("+c("role")+",'|',"+c("status")+")", "''", "''")
	case "team_memberships":
		cols = append(cols, c("workspace_id"), "'@authz'", c("user_id"), "CONCAT("+c("role")+",'|',"+c("origin")+")", "''", "''")
	case "auth_users":
		cols = append(cols, "''", "'@authz'", c("id"), "CONCAT("+c("active")+",'|',COALESCE("+c("deleted_at")+",''))", "''", "''")
	case "domain_events":
		cols = append(cols, "COALESCE("+c("workspace_id")+",'')", "''", c("id"), "''", "''", "''")
	}
	return cols
}

// captureRow is one row image read by capture. Keys are compared as the
// stored bytes, like the triggers' NEW/OLD values.
type captureRow struct {
	pk                                   []string
	workspace, field, key, state, policy string
	aux                                  string
}

func (r captureRow) id() string { return strings.Join(r.pk, "\x00") }

type capturePlan struct {
	table *captureTable
	st    captureStatement
	// pre: rows read (locked) before the statement, by stored key.
	pre      map[string]captureRow
	preOrder []captureRow
	// keys: the primary keys an INSERT names, for the read-back.
	keys [][]any
	// decided: a single-row INSERT whose affected-rows count decides it.
	decided bool
	// members: workspace memberships of deleted accounts, read before the
	// delete cascades.
	members []captureMember
	// replace: a bounded DELETE runs as a delete of the locked keys.
	replace bool
	// coarse: why the statement could not be analysed.
	coarse string
}

type captureMember struct{ workspaceID, workspaceKey, user string }

// captureEffects collects the journal inputs one statement produces.
type captureEffects struct {
	versions [][4]any
	dirty    [][4]string
	epochs   map[string]bool
	events   [][2]string
	caches   map[[2]string]bool
	// lookup caches for the statement.
	workspaceIDs  map[string]string
	workspaceKeys map[string]string
}

// captureApplication reports whether this transaction's writes to journal
// tables are captured by the application.
func (tx *sqlTx) captureApplication() bool {
	return tx.dialect == "mysql" && tx.database != nil && tx.database.appCapture.Load() && !tx.numbering
}

func (tx *sqlTx) captureQuery(ctx context.Context, query string, args []any, scan func(*sql.Rows) error) error {
	observeSQLStatement(ctx, query)
	rows, err := tx.Tx.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (tx *sqlTx) captureExec(ctx context.Context, query string, args ...any) error {
	observeSQLStatement(ctx, query)
	_, err := tx.Tx.ExecContext(ctx, query, args...)
	return err
}

func scanCaptureRow(rows *sql.Rows, width int) (captureRow, error) {
	values := make([]sql.NullString, width+6)
	targets := make([]any, len(values))
	for i := range values {
		targets[i] = &values[i]
	}
	if err := rows.Scan(targets...); err != nil {
		return captureRow{}, err
	}
	row := captureRow{pk: make([]string, width)}
	for i := range width {
		row.pk[i] = values[i].String
	}
	row.workspace, row.field, row.key = values[width].String, values[width+1].String, values[width+2].String
	row.state, row.policy, row.aux = values[width+3].String, values[width+4].String, values[width+5].String
	return row, nil
}

// readCaptureRows reads rows of the table by primary key, in chunks.
func (tx *sqlTx) readCaptureRows(ctx context.Context, table *captureTable, keys [][]any, lock bool) ([]captureRow, error) {
	var result []captureRow
	columns := strings.Join(table.columns("t"), ",")
	tuple := "(" + strings.TrimSuffix(strings.Repeat("?,", len(table.pk)), ",") + ")"
	target := "(t." + strings.Join(table.pk, ",t.") + ")"
	for start := 0; start < len(keys); start += 200 {
		part := keys[start:min(start+200, len(keys))]
		args := make([]any, 0, len(part)*len(table.pk))
		for _, key := range part {
			args = append(args, key...)
		}
		query := "SELECT " + columns + " FROM " + table.name + " t WHERE " + target + " IN (" + strings.TrimSuffix(strings.Repeat(tuple+",", len(part)), ",") + ")"
		if lock {
			query += " FOR UPDATE"
		}
		err := tx.captureQuery(ctx, query, args, func(rows *sql.Rows) error {
			row, err := scanCaptureRow(rows, len(table.pk))
			result = append(result, row)
			return err
		})
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func captureKeyArgs(row captureRow) []any {
	key := make([]any, len(row.pk))
	for i, value := range row.pk {
		key[i] = value
	}
	return key
}

// captureBefore analyses a journal-table write and reads what capture needs
// before it runs. A nil plan means the statement changes nothing captured.
func (tx *sqlTx) captureBefore(ctx context.Context, query string, args []any) (*capturePlan, error) {
	st := parseCaptureStatement(query, args)
	if st.unknown != "" {
		return &capturePlan{coarse: st.unknown, st: st}, nil
	}
	table := captureTables[st.table]
	if table == nil {
		return &capturePlan{coarse: "table", st: st}, nil
	}
	plan := &capturePlan{table: table, st: st, pre: map[string]captureRow{}}
	switch st.verb {
	case captureUpdate:
		if !table.touched(st.assigned) {
			return nil, nil
		}
		for _, column := range table.pk {
			if value, ok := st.assigned[column]; ok && !value.known {
				plan.coarse = "computed key"
				return plan, nil
			}
		}
		return plan, tx.capturePreFilter(ctx, plan)
	case captureDelete:
		if table.name == "domain_events" {
			return nil, nil
		}
		if err := tx.capturePreFilter(ctx, plan); err != nil {
			return nil, err
		}
		plan.replace = st.limited
		if table.name == "auth_users" && len(plan.preOrder) > 0 {
			return plan, tx.captureAccountMembers(ctx, plan)
		}
		return plan, nil
	}
	if !table.inserts {
		return nil, nil
	}
	tuples := st.tuples
	if st.selectSQL != "" {
		var err error
		if tuples, err = tx.captureSelectTuples(ctx, st); err != nil {
			return nil, err
		}
	}
	positions := make([]int, len(table.pk))
	for i, column := range table.pk {
		positions[i] = slices.Index(st.columns, column)
		if positions[i] < 0 {
			plan.coarse = "insert without key column"
			return plan, nil
		}
	}
	for _, tuple := range tuples {
		key := make([]any, len(positions))
		for i, position := range positions {
			if !tuple[position].known {
				plan.coarse = "computed key"
				return plan, nil
			}
			key[i] = tuple[position].value
		}
		plan.keys = append(plan.keys, key)
	}
	if table.name == "domain_events" {
		// Event IDs are unique; only an insert that wrote the row counts.
		workspace := slices.Index(st.columns, "workspace_id")
		if st.conflict == conflictNone && workspace >= 0 && st.selectSQL == "" {
			plan.decided = true
			for _, tuple := range tuples {
				if !tuple[workspace].known {
					plan.decided = false
				}
			}
		}
	}
	if st.conflict == conflictNone || len(plan.keys) == 0 {
		return plan, nil
	}
	if len(plan.keys) == 1 && table.name != "domain_events" && (st.conflict == conflictNothing || table.onlyRelevant(st.assigned) && !plan.metadataPolicyKey()) {
		// One row: the affected-rows count says inserted (1), changed (2)
		// or untouched (0), and every change it can report is captured.
		plan.decided = true
		return plan, nil
	}
	// Probe without locks, then lock only the keys that exist: a locking
	// read of a missing key would gap-lock it against concurrent inserts.
	existing, err := tx.readCaptureRows(ctx, table, plan.keys, false)
	if err != nil || len(existing) == 0 {
		return plan, err
	}
	keys := make([][]any, len(existing))
	for i, row := range existing {
		keys[i] = captureKeyArgs(row)
	}
	locked, err := tx.readCaptureRows(ctx, table, keys, true)
	for _, row := range locked {
		plan.pre[row.id()] = row
		plan.preOrder = append(plan.preOrder, row)
	}
	return plan, err
}

// metadataPolicyKey: a single metadata upsert whose epoch decision compares
// the policy state before and after.
func (plan *capturePlan) metadataPolicyKey() bool {
	if plan.table.name != "workspace_metadata_records" || len(plan.keys) != 1 {
		return false
	}
	field, _ := plan.keys[0][1].(string)
	if raw, ok := plan.keys[0][1].([]byte); ok {
		field = string(raw)
	}
	return field == "apiKeys" || field == "oauthAuthorizations" || field == "teamSettings"
}

func (tx *sqlTx) capturePreFilter(ctx context.Context, plan *capturePlan) error {
	st := plan.st
	query := "SELECT " + strings.Join(plan.table.columns(st.alias), ",") + " FROM " + st.from
	if st.where != "" {
		query += " WHERE " + st.where
	}
	if st.tail != "" {
		query += " " + st.tail
	}
	query += " FOR UPDATE"
	return tx.captureQuery(ctx, query, st.filterArgs, func(rows *sql.Rows) error {
		row, err := scanCaptureRow(rows, len(plan.table.pk))
		if err == nil && plan.pre[row.id()].pk == nil {
			plan.pre[row.id()] = row
			plan.preOrder = append(plan.preOrder, row)
		}
		return err
	})
}

func (tx *sqlTx) captureSelectTuples(ctx context.Context, st captureStatement) ([][]captureValue, error) {
	var tuples [][]captureValue
	err := tx.captureQuery(ctx, "SELECT * FROM ("+st.selectSQL+") flow_capture_rows", st.selectArgs, func(rows *sql.Rows) error {
		values := make([]sql.NullString, len(st.columns))
		targets := make([]any, len(values))
		for i := range values {
			targets[i] = &values[i]
		}
		if err := rows.Scan(targets...); err != nil {
			return err
		}
		tuple := make([]captureValue, len(values))
		for i, value := range values {
			tuple[i] = captureValue{known: value.Valid, value: value.String}
		}
		tuples = append(tuples, tuple)
		return nil
	})
	return tuples, err
}

func (tx *sqlTx) captureAccountMembers(ctx context.Context, plan *capturePlan) error {
	users := make([]string, 0, len(plan.preOrder))
	for _, row := range plan.preOrder {
		users = append(users, row.pk[0])
	}
	for start := 0; start < len(users); start += 200 {
		clause, args := bindList("m.user_id", users[start:min(start+200, len(users))])
		err := tx.captureQuery(ctx, "SELECT m.workspace_id,COALESCE(w.workspace_key,''),m.user_id FROM workspace_memberships m LEFT JOIN workspace_states w ON w.workspace_id=m.workspace_id WHERE "+clause, args, func(rows *sql.Rows) error {
			var member captureMember
			err := rows.Scan(&member.workspaceID, &member.workspaceKey, &member.user)
			plan.members = append(plan.members, member)
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// captureDeleteKeys runs a bounded DELETE as a delete of the keys capture
// locked, so the rows it reports are exactly the rows deleted.
func (tx *sqlTx) captureDeleteKeys(ctx context.Context, plan *capturePlan) (sql.Result, error) {
	table := plan.table
	tuple := "(" + strings.TrimSuffix(strings.Repeat("?,", len(table.pk)), ",") + ")"
	var deleted int64
	for start := 0; start < len(plan.preOrder); start += 500 {
		part := plan.preOrder[start:min(start+500, len(plan.preOrder))]
		args := make([]any, 0, len(part)*len(table.pk))
		for _, row := range part {
			args = append(args, captureKeyArgs(row)...)
		}
		query := "DELETE FROM " + table.name + " WHERE (" + strings.Join(table.pk, ",") + ") IN (" + strings.TrimSuffix(strings.Repeat(tuple+",", len(part)), ",") + ")"
		observeSQLStatement(ctx, query)
		result, err := tx.Tx.ExecContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		deleted += rowsAffected(result)
	}
	return captureResult(deleted), nil
}

type captureResult int64

func (r captureResult) LastInsertId() (int64, error) { return 0, nil }
func (r captureResult) RowsAffected() (int64, error) { return int64(r), nil }

// captureAfter records the journal inputs of a statement that succeeded.
func (tx *sqlTx) captureAfter(ctx context.Context, plan *capturePlan, result sql.Result) error {
	if plan.coarse != "" {
		return tx.captureResync(ctx, plan)
	}
	affected, countErr := result.RowsAffected()
	if countErr == nil && affected == 0 {
		return nil
	}
	effects := &captureEffects{epochs: map[string]bool{}, caches: map[[2]string]bool{}, workspaceIDs: map[string]string{}, workspaceKeys: map[string]string{}}
	table := plan.table
	switch plan.st.verb {
	case captureDelete:
		if table.name == "auth_users" {
			// The BEFORE DELETE trigger: every membership's epoch, and keys
			// only for the workspaces that exist.
			for _, member := range plan.members {
				if member.workspaceKey == "" {
					effects.epochs[member.workspaceID] = true
					continue
				}
				effects.authz(member.workspaceID, member.workspaceKey, member.user, table.name)
			}
			break
		}
		if err := tx.captureDeleted(ctx, plan, effects); err != nil {
			return err
		}
	case captureUpdate:
		if err := tx.captureUpdated(ctx, plan, affected, countErr == nil, effects); err != nil {
			return err
		}
	case captureInsert:
		if err := tx.captureInserted(ctx, plan, affected, countErr == nil, effects); err != nil {
			return err
		}
	}
	return tx.applyCaptureEffects(ctx, effects)
}

func (effects *captureEffects) authz(workspaceID, workspaceKey, key, source string) {
	if workspaceID != "" {
		effects.epochs[workspaceID] = true
	}
	effects.dirty = append(effects.dirty, [4]string{workspaceKey, "@authz", key, source})
}

func (tx *sqlTx) captureWorkspaceID(ctx context.Context, effects *captureEffects, workspace string) (string, error) {
	if id, ok := effects.workspaceIDs[workspace]; ok {
		return id, nil
	}
	var id string
	err := tx.captureQuery(ctx, "SELECT workspace_id FROM workspace_states WHERE workspace_key=?", []any{workspace}, func(rows *sql.Rows) error {
		return rows.Scan(&id)
	})
	effects.workspaceIDs[workspace] = id
	return id, err
}

func (tx *sqlTx) captureWorkspaceKey(ctx context.Context, effects *captureEffects, workspaceID string) (string, error) {
	if key, ok := effects.workspaceKeys[workspaceID]; ok {
		return key, nil
	}
	var key string
	err := tx.captureQuery(ctx, "SELECT workspace_key FROM workspace_states WHERE workspace_id=?", []any{workspaceID}, func(rows *sql.Rows) error {
		return rows.Scan(&key)
	})
	effects.workspaceKeys[workspaceID] = key
	return key, err
}

func pulseCursor(row captureRow) bool {
	return row.field == "settings" && row.key == "pulseDeliveryCursors"
}

// captureChanged records an inserted (old nil) or updated row.
func (tx *sqlTx) captureChanged(ctx context.Context, table *captureTable, row captureRow, old *captureRow, effects *captureEffects) error {
	switch table.name {
	case "workspace_memberships", "team_memberships":
		key, err := tx.captureWorkspaceKey(ctx, effects, row.workspace)
		if err != nil {
			return err
		}
		effects.authz(row.workspace, key, row.key, table.name)
		return nil
	case "auth_users":
		return tx.captureAccountChanged(ctx, row.key, effects)
	case "issue_permission_records":
		id, err := tx.captureWorkspaceID(ctx, effects, row.workspace)
		if err != nil {
			return err
		}
		effects.authz(id, row.workspace, row.key, table.name)
		return nil
	case "domain_events":
		if row.workspace != "" {
			effects.events = append(effects.events, [2]string{row.key, row.workspace})
		}
		return nil
	}
	effects.versions = append(effects.versions, [4]any{row.workspace, row.field, row.key, 0})
	effects.dirty = append(effects.dirty, [4]string{row.workspace, row.field, row.key, table.name})
	kind := cacheKindIssues
	if table.name == "workspace_metadata_records" || table.name == "workspace_states" {
		kind = cacheKindMetadata
	}
	if tx.journalToken == "" {
		effects.caches[[2]string{row.workspace, kind}] = true
	}
	switch table.name {
	case "workspace_states":
		if row.aux != "" {
			effects.epochs[row.aux] = true
		}
	case "workspace_metadata_records":
		policy := metadataPolicyFields[row.field]
		if old == nil {
			policy = policy && !pulseCursor(row)
		} else {
			policy = policy && !(pulseCursor(row) && pulseCursor(*old))
			if row.field == "apiKeys" || row.field == "oauthAuthorizations" || row.field == "teamSettings" {
				policy = policy && row.policy != old.policy
			}
		}
		if policy {
			id, err := tx.captureWorkspaceID(ctx, effects, row.workspace)
			if err != nil {
				return err
			}
			if id != "" {
				effects.epochs[id] = true
			}
		}
	}
	return nil
}

func (tx *sqlTx) captureAccountChanged(ctx context.Context, user string, effects *captureEffects) error {
	return tx.captureQuery(ctx, "SELECT m.workspace_id,COALESCE(w.workspace_key,'') FROM workspace_memberships m LEFT JOIN workspace_states w ON w.workspace_id=m.workspace_id WHERE m.user_id=?", []any{user}, func(rows *sql.Rows) error {
		var id, key string
		if err := rows.Scan(&id, &key); err != nil {
			return err
		}
		effects.epochs[id] = true
		if key != "" {
			effects.dirty = append(effects.dirty, [4]string{key, "@authz", user, "auth_users"})
		}
		return nil
	})
}

func (tx *sqlTx) captureDeleted(ctx context.Context, plan *capturePlan, effects *captureEffects) error {
	table := plan.table
	exists := map[string]bool{}
	workspaceExists := func(key string) (bool, error) {
		if found, ok := exists[key]; ok {
			return found, nil
		}
		id, err := tx.captureWorkspaceID(ctx, effects, key)
		exists[key] = id != ""
		return id != "", err
	}
	var orphans []captureRow
	for _, row := range plan.preOrder {
		switch table.name {
		case "workspace_states":
			if row.aux != "" {
				effects.epochs[row.aux] = true
			}
			effects.dirty = append(effects.dirty, [4]string{row.workspace, "@authz", "root", table.name})
			continue
		case "workspace_memberships", "team_memberships":
			key, err := tx.captureWorkspaceKey(ctx, effects, row.workspace)
			if err != nil {
				return err
			}
			if key != "" {
				effects.authz(row.workspace, key, row.key, table.name)
			}
			continue
		}
		found, err := workspaceExists(row.workspace)
		if err != nil {
			return err
		}
		if table.name == "issue_permission_records" {
			if found {
				effects.authz(effects.workspaceIDs[row.workspace], row.workspace, row.key, table.name)
			}
			continue
		}
		if !found {
			// A rename moves versions before canonical rows; a purge has
			// already removed them. Tombstone only keys still versioned.
			orphans = append(orphans, row)
			continue
		}
		tx.captureTombstone(table, row, effects)
	}
	if len(orphans) > 0 {
		versioned := map[string]bool{}
		for start := 0; start < len(orphans); start += 200 {
			part := orphans[start:min(start+200, len(orphans))]
			args := make([]any, 0, len(part)*3)
			for _, row := range part {
				args = append(args, row.workspace, row.field, row.key)
			}
			query := "SELECT workspace_key,field,record_key FROM record_versions WHERE (workspace_key,field,record_key) IN (" + strings.TrimSuffix(strings.Repeat("(?,?,?),", len(part)), ",") + ")"
			err := tx.captureQuery(ctx, query, args, func(rows *sql.Rows) error {
				var workspace, field, key string
				err := rows.Scan(&workspace, &field, &key)
				versioned[workspace+"\x00"+field+"\x00"+key] = true
				return err
			})
			if err != nil {
				return err
			}
		}
		for _, row := range orphans {
			if versioned[row.workspace+"\x00"+row.field+"\x00"+row.key] {
				tx.captureTombstone(table, row, effects)
			}
		}
	}
	return nil
}

func (tx *sqlTx) captureTombstone(table *captureTable, row captureRow, effects *captureEffects) {
	effects.versions = append(effects.versions, [4]any{row.workspace, row.field, row.key, 1})
	effects.dirty = append(effects.dirty, [4]string{row.workspace, row.field, row.key, table.name})
	if tx.journalToken == "" {
		kind := cacheKindIssues
		if table.name == "workspace_metadata_records" {
			kind = cacheKindMetadata
		}
		effects.caches[[2]string{row.workspace, kind}] = true
	}
	if table.name == "workspace_metadata_records" && metadataPolicyFields[row.field] && !pulseCursor(row) {
		if id := effects.workspaceIDs[row.workspace]; id != "" {
			effects.epochs[id] = true
		}
	}
}

func (tx *sqlTx) captureUpdated(ctx context.Context, plan *capturePlan, affected int64, counted bool, effects *captureEffects) error {
	table, st := plan.table, plan.st
	keyChanged := false
	for _, column := range table.pk {
		if _, ok := st.assigned[column]; ok {
			keyChanged = true
		}
	}
	newKey := func(row captureRow) []any {
		key := captureKeyArgs(row)
		for i, column := range table.pk {
			if value, ok := st.assigned[column]; ok {
				key[i] = value.value
			}
		}
		return key
	}
	if !keyChanged && counted && affected == int64(len(plan.preOrder)) && table.onlyRelevant(st.assigned) &&
		table.name != "workspace_metadata_records" && table.name != "workspace_states" {
		// Every locked row changed a relevant column: no read-back needed.
		for _, row := range plan.preOrder {
			old := row
			if err := tx.captureChanged(ctx, table, row, &old, effects); err != nil {
				return err
			}
		}
		return nil
	}
	keys := make([][]any, len(plan.preOrder))
	for i, row := range plan.preOrder {
		keys[i] = newKey(row)
	}
	after, err := tx.readCaptureRows(ctx, table, keys, true)
	if err != nil {
		return err
	}
	byKey := map[string]captureRow{}
	for _, row := range after {
		byKey[row.id()] = row
	}
	for _, old := range plan.preOrder {
		key := newKey(old)
		parts := make([]string, len(key))
		for i, value := range key {
			parts[i] = captureString(value)
		}
		row, ok := byKey[strings.Join(parts, "\x00")]
		if !ok {
			// A collation-equal key: match the single row read back.
			if len(after) != 1 || len(plan.preOrder) != 1 {
				continue
			}
			row = after[0]
		}
		if table.updateAlways || row.state != old.state || keyChanged && table.keyChange {
			if err := tx.captureChanged(ctx, table, row, &old, effects); err != nil {
				return err
			}
		}
	}
	return nil
}

func captureString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

func (tx *sqlTx) captureInserted(ctx context.Context, plan *capturePlan, affected int64, counted bool, effects *captureEffects) error {
	table := plan.table
	if table.name == "domain_events" && plan.decided {
		workspace, id := slices.Index(plan.st.columns, "workspace_id"), slices.Index(plan.st.columns, "id")
		for _, tuple := range plan.st.tuples {
			if ws := captureString(tuple[workspace].value); ws != "" {
				effects.events = append(effects.events, [2]string{captureString(tuple[id].value), ws})
			}
		}
		return nil
	}
	after, err := tx.readCaptureRows(ctx, table, plan.keys, true)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, row := range after {
		if seen[row.id()] {
			continue
		}
		seen[row.id()] = true
		switch {
		case plan.st.conflict == conflictNone:
			err = tx.captureChanged(ctx, table, row, nil, effects)
		case plan.decided:
			if !counted || affected == 1 {
				err = tx.captureChanged(ctx, table, row, nil, effects)
			} else {
				// Updated (2): a relevant column changed.
				old := row
				old.state = ""
				err = tx.captureChanged(ctx, table, row, &old, effects)
			}
		default:
			old, existed := plan.pre[row.id()]
			if !existed {
				err = tx.captureChanged(ctx, table, row, nil, effects)
			} else if plan.st.conflict == conflictUpdate && (table.updateAlways || old.state != row.state) {
				err = tx.captureChanged(ctx, table, row, &old, effects)
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (tx *sqlTx) applyCaptureEffects(ctx context.Context, effects *captureEffects) error {
	token := tx.journalToken
	for start := 0; start < len(effects.versions); start += 200 {
		part := effects.versions[start:min(start+200, len(effects.versions))]
		args := make([]any, 0, len(part)*4)
		for _, version := range part {
			args = append(args, version[0], version[1], version[2], version[3])
		}
		if err := tx.captureExec(ctx, "INSERT INTO record_versions(workspace_key,field,record_key,version,deleted) VALUES "+strings.TrimSuffix(strings.Repeat("(?,?,?,1,?),", len(part)), ",")+
			" ON DUPLICATE KEY UPDATE version=version+1,deleted=VALUES(deleted)", args...); err != nil {
			return err
		}
	}
	for start := 0; start < len(effects.dirty); start += 200 {
		part := effects.dirty[start:min(start+200, len(effects.dirty))]
		args := make([]any, 0, len(part)*5)
		for _, dirty := range part {
			args = append(args, token, dirty[0], dirty[1], dirty[2], dirty[3])
		}
		if err := tx.captureExec(ctx, "INSERT INTO record_journal_dirty(tx_id,workspace_key,field,record_key,source) VALUES "+strings.TrimSuffix(strings.Repeat("(?,?,?,?,?),", len(part)), ",")+
			" ON DUPLICATE KEY UPDATE source=VALUES(source)", args...); err != nil {
			return err
		}
	}
	caches := make([][2]string, 0, len(effects.caches))
	for cache := range effects.caches {
		caches = append(caches, cache)
	}
	slices.SortFunc(caches, func(a, b [2]string) int { return strings.Compare(a[0]+"\x00"+a[1], b[0]+"\x00"+b[1]) })
	for _, cache := range caches {
		// A writer without a journal token is an untracked writer, as for
		// the triggers: advance the cache revision it does not queue.
		if err := tx.captureExec(ctx, "INSERT INTO workspace_cache_revisions(workspace_key,kind,shard,revision) VALUES(?,?,0,1) ON DUPLICATE KEY UPDATE revision=revision+1", cache[0], cache[1]); err != nil {
			return err
		}
	}
	epochs := make([]string, 0, len(effects.epochs))
	for id := range effects.epochs {
		if id != "" {
			epochs = append(epochs, id)
		}
	}
	slices.Sort(epochs)
	if len(epochs) > 0 {
		args := make([]any, len(epochs))
		for i, id := range epochs {
			args[i] = id
		}
		if err := tx.captureExec(ctx, "INSERT INTO workspace_authz_epochs(workspace_id,epoch) VALUES "+strings.TrimSuffix(strings.Repeat("(?,1),", len(epochs)), ",")+
			" ON DUPLICATE KEY UPDATE epoch=epoch+1", args...); err != nil {
			return err
		}
	}
	for start := 0; start < len(effects.events); start += 200 {
		part := effects.events[start:min(start+200, len(effects.events))]
		args := make([]any, 0, len(part)*3)
		for _, event := range part {
			args = append(args, token, event[0], event[1])
		}
		if err := tx.captureExec(ctx, "INSERT INTO record_journal_event_pending(tx_id,event_id,workspace_id) VALUES "+strings.TrimSuffix(strings.Repeat("(?,?,?),", len(part)), ",")+
			" ON DUPLICATE KEY UPDATE tx_id=VALUES(tx_id)", args...); err != nil {
			return err
		}
	}
	return nil
}

// captureResync is the answer to a write capture could not analyse: every
// workspace restarts the bounded activation repair in the writer's own
// transaction, and every authorization epoch advances. Serving reports a
// resync until RepairRecordJournalPass has republished each workspace.
func (tx *sqlTx) captureResync(ctx context.Context, plan *capturePlan) error {
	table := plan.st.table
	if captureTables[table] == nil {
		table = "unknown"
	}
	slog.Warn("record journal application capture scheduled a journal repair", "table", table, "reason", plan.coarse)
	if err := tx.captureExec(ctx, "INSERT INTO record_journal_coverage(workspace_key,phase,last_field,last_key) SELECT workspace_key,0,'','' FROM workspace_states ON DUPLICATE KEY UPDATE phase=0,last_field='',last_key=''"); err != nil {
		return err
	}
	return tx.captureExec(ctx, "INSERT INTO workspace_authz_epochs(workspace_id,epoch) SELECT workspace_id,1 FROM workspace_states ON DUPLICATE KEY UPDATE epoch=epoch+1")
}
