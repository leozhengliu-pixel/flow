package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// The audit log is an append-only table, audit_events, separate from the
// workspace document. Nothing updates or deletes a row except retention
// (PruneAuditEvents), which only removes a workspace's oldest rows.
//
// Every row is numbered per workspace (seq, in commit order) and chained:
// hash = SHA-256 of the previous row's hash and the row's stored columns.
// audit_chain_heads keeps each workspace's last seq and hash, and the seq and
// hash of the last row retention removed, so VerifyAuditChain can tell an
// edited, removed or inserted row from the regular pruning of old ones.
//
// Entries reach the table two ways, both through insertAuditEntriesTx:
//   - a workspace mutation stages them on Bootstrap.AuditLog (appendAudit in
//     the server); dispatchMutation takes them off the snapshot and the
//     mutation's transaction inserts them when it commits (sqlTx.Commit), so
//     an entry exists exactly when the change it describes does;
//   - events with no workspace change (sign-ins, downloads, agent writes)
//     call AppendAuditEntries or RecordAccountAudit.
//
// Instance-level entries (a sign-in attempt for an unknown account, or an
// account that belongs to no workspace) use the workspace id "".

const (
	AuditActorUser     = "user"
	AuditActorAPIKey   = "api_key"
	AuditActorOAuthApp = "oauth_app"
	AuditActorSCIM     = "scim"
	AuditActorAgent    = "agent"
	AuditActorSystem   = "system"

	AuditResultSuccess = "success"
	AuditResultFailure = "failure"

	// InstanceAuditWorkspace is the workspace id of instance-level entries.
	InstanceAuditWorkspace = ""

	// AuditPruneBatch is how many entries one retention statement deletes.
	AuditPruneBatch = 1000

	// auditLegacyMigrationVersion records the move of the audit entries the
	// workspace document kept (its latest 1,000) into audit_events.
	auditLegacyMigrationVersion = 1030

	// auditTimeLayout is fixed width in UTC, so stored times sort as text.
	auditTimeLayout = "2006-01-02T15:04:05.000000000Z07:00"
	// auditMetadataMaxBytes bounds one entry's details.
	auditMetadataMaxBytes = 64 << 10
	auditVerifyPage       = 1000
)

// AuditRequest is what the HTTP layer knows about the request a write comes
// from: the client address and user agent (already reduced as configured),
// the signed-in user and the credential used.
type AuditRequest struct {
	IP        string
	UserAgent string
	UserID    string
	// ActorType and ActorID name the credential: user (a session),
	// api_key, oauth_app or agent.
	ActorType string
	ActorID   string
}

type auditRequestKey struct{}

// WithAuditRequest attaches the request's audit details to ctx; audit entries
// written under ctx that do not set them take them from here.
func WithAuditRequest(ctx context.Context, request AuditRequest) context.Context {
	return context.WithValue(ctx, auditRequestKey{}, request)
}

// AuditRequestFrom returns the request details WithAuditRequest attached.
func AuditRequestFrom(ctx context.Context) (AuditRequest, bool) {
	request, ok := ctx.Value(auditRequestKey{}).(AuditRequest)
	return request, ok
}

// auditCollector carries the entries one workspace mutation staged to the
// transaction that commits it.
type auditCollector struct {
	workspaceKey string
	workspaceID  string
	request      AuditRequest
	pending      []domain.AuditLogEntry
}

type auditCollectorKey struct{}

func auditCollectorFrom(ctx context.Context) *auditCollector {
	collector, _ := ctx.Value(auditCollectorKey{}).(*auditCollector)
	return collector
}

// collectMutationAudit wraps a mutation callback: the entries it stages on
// Bootstrap.AuditLog are taken off the snapshot (which never persists them)
// and kept for the mutation's transaction. A callback that fails stages
// nothing, and every rerun starts over.
func collectMutationAudit(ctx context.Context, workspaceKey string, mutate func(*domain.Bootstrap) (string, error)) (context.Context, *auditCollector, func(*domain.Bootstrap) (string, error)) {
	collector := &auditCollector{workspaceKey: workspaceKey}
	collector.request, _ = AuditRequestFrom(ctx)
	ctx = context.WithValue(ctx, auditCollectorKey{}, collector)
	return ctx, collector, func(data *domain.Bootstrap) (string, error) {
		collector.pending = nil
		before := data.AuditLog
		id, err := mutate(data)
		added := stagedAuditEntries(before, data.AuditLog)
		data.AuditLog = before
		if err != nil {
			return id, err
		}
		collector.workspaceID = data.Workspace.ID
		collector.pending = added
		return id, nil
	}
}

// stagedAuditEntries returns the entries of after that before did not have,
// oldest first (the log is staged newest first).
func stagedAuditEntries(before, after []domain.AuditLogEntry) []domain.AuditLogEntry {
	if len(after) == 0 {
		return nil
	}
	known := make(map[string]bool, len(before))
	for _, entry := range before {
		known[entry.ID] = true
	}
	added := []domain.AuditLogEntry{}
	for _, entry := range after {
		if !known[entry.ID] {
			added = append(added, entry)
		}
	}
	slices.Reverse(added)
	return added
}

// flushAudit inserts the entries of the mutation this transaction commits.
func (tx *sqlTx) flushAudit() error {
	if tx.audit == nil || len(tx.audit.pending) == 0 {
		return nil
	}
	ctx := tx.context()
	if tx.audit.workspaceID == "" {
		// A snapshot without its workspace record: look the id up by key.
		if err := tx.QueryRowContext(ctx, `SELECT workspace_id FROM workspace_states WHERE workspace_key=?`, tx.audit.workspaceKey).Scan(&tx.audit.workspaceID); err != nil {
			return fmt.Errorf("audit log workspace %q: %w", tx.audit.workspaceKey, err)
		}
	}
	entries := make([]domain.AuditLogEntry, len(tx.audit.pending))
	for index, entry := range tx.audit.pending {
		entries[index] = completeAuditEntry(entry, tx.audit.request)
	}
	return insertAuditEntriesTx(ctx, tx, tx.audit.workspaceID, entries)
}

func (s *SQLiteStore) ensureAuditEvents(ctx context.Context) error {
	text := "TEXT"
	if s.dialect == "mysql" {
		text = "MEDIUMTEXT"
	}
	id := "VARCHAR(191)"
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS audit_events(id ` + id + ` PRIMARY KEY,workspace_id ` + id + ` NOT NULL,seq BIGINT NOT NULL,created_at VARCHAR(40) NOT NULL,actor_type VARCHAR(32) NOT NULL,actor_id ` + id + ` NOT NULL,user_id ` + id + ` NOT NULL,actor ` + text + ` NOT NULL,action ` + id + ` NOT NULL,resource_type ` + id + ` NOT NULL,resource_id ` + id + ` NOT NULL,ip VARCHAR(64) NOT NULL,user_agent ` + id + ` NOT NULL,result VARCHAR(32) NOT NULL,metadata ` + text + ` NOT NULL,prev_hash VARCHAR(64) NOT NULL,hash VARCHAR(64) NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS audit_chain_heads(workspace_id ` + id + ` PRIMARY KEY,seq BIGINT NOT NULL,hash VARCHAR(64) NOT NULL,pruned_seq BIGINT NOT NULL,pruned_hash VARCHAR(64) NOT NULL)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS audit_events_seq ON audit_events(workspace_id,seq)`,
		`CREATE INDEX IF NOT EXISTS audit_events_time ON audit_events(workspace_id,created_at)`,
	} {
		if s.dialect == "mysql" && strings.Contains(statement, " INDEX ") {
			// MySQL has no CREATE INDEX IF NOT EXISTS; a duplicate is ignored.
			statement = strings.Replace(statement, " IF NOT EXISTS", "", 1)
		}
		if _, err := s.db.ExecContext(ctx, statement); err != nil && !(s.dialect == "mysql" && isDuplicateIndex(err)) {
			return err
		}
	}
	return nil
}

// auditRow is an entry as stored: every column the hash covers, in the form
// the database returns it (valid UTF-8, no NUL, within the column sizes).
type auditRow struct {
	id, workspaceID                     string
	seq                                 int64
	createdAt, actorType, actorID       string
	userID, actor, action, resourceType string
	resourceID, ip, userAgent, result   string
	metadata, prevHash, hash            string
}

type auditActor struct {
	Actor    domain.User      `json:"actor"`
	BotActor *domain.ActorBot `json:"botActor,omitempty"`
}

// auditSecretKeys are detail keys never stored: values under them are
// credentials or message bodies.
var auditSecretKeys = map[string]bool{
	"password": true, "currentpassword": true, "newpassword": true, "secret": true, "clientsecret": true,
	"token": true, "accesstoken": true, "refreshtoken": true, "apikey": true, "privatekey": true,
	"authorization": true, "cookie": true, "body": true, "content": true, "message": true,
}

// scrubAuditMetadata drops credential and message-body keys at any depth.
func scrubAuditMetadata(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		clean := make(map[string]any, len(typed))
		for key, item := range typed {
			if auditSecretKeys[strings.ToLower(key)] {
				continue
			}
			clean[key] = scrubAuditMetadata(item)
		}
		return clean
	case []any:
		clean := make([]any, len(typed))
		for index, item := range typed {
			clean[index] = scrubAuditMetadata(item)
		}
		return clean
	default:
		return value
	}
}

func auditMetadataJSON(metadata map[string]any) (string, error) {
	if len(metadata) == 0 {
		return "{}", nil
	}
	// Round-trip through JSON first so typed values (structs, slices of
	// structs) become maps the scrubber can walk.
	raw, err := json.Marshal(metadata)
	if err != nil {
		return "", err
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return "", err
	}
	if raw, err = json.Marshal(scrubAuditMetadata(generic)); err != nil {
		return "", err
	}
	if len(raw) > auditMetadataMaxBytes {
		return `{"truncated":true}`, nil
	}
	return string(raw), nil
}

// completeAuditEntry fills what an entry left unset from the request it was
// written in: client address, user agent, the credential that acted and the
// result.
func completeAuditEntry(entry domain.AuditLogEntry, request AuditRequest) domain.AuditLogEntry {
	if entry.ID == "" {
		entry.ID = idgen.New("audit_")
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	ownRequest := request.UserID != "" && request.UserID == entry.Actor.ID
	if entry.IP == "" {
		// Writers that record a client address in the details (webhook and
		// integration callers) keep doing so.
		entry.IP = legacyAuditIP(entry.Metadata)
	}
	if entry.IP == "" && (ownRequest || entry.Actor.ID == "" || request.UserID == "") {
		entry.IP = request.IP
	}
	if entry.UserAgent == "" && (ownRequest || entry.Actor.ID == "" || request.UserID == "") {
		entry.UserAgent = request.UserAgent
	}
	if entry.ActorType == "" {
		switch {
		case entry.BotActor != nil && entry.BotActor.Type == "scim" || entry.Actor.ID == "scim":
			entry.ActorType = AuditActorSCIM
			if entry.BotActor != nil {
				entry.ActorID = entry.BotActor.ID
			}
		case ownRequest && request.ActorType != "":
			entry.ActorType, entry.ActorID = request.ActorType, request.ActorID
		case entry.Actor.ID == "":
			entry.ActorType = AuditActorSystem
		case entry.BotActor != nil && entry.BotActor.Type == "oauthApplication":
			entry.ActorType, entry.ActorID = AuditActorOAuthApp, entry.BotActor.ID
		default:
			entry.ActorType, entry.ActorID = AuditActorUser, entry.Actor.ID
		}
	}
	if entry.ActorID == "" && entry.ActorType == AuditActorUser {
		entry.ActorID = entry.Actor.ID
	}
	if entry.Result == "" {
		entry.Result = AuditResultSuccess
	}
	return entry
}

func auditRowFor(workspaceID string, entry domain.AuditLogEntry) (auditRow, error) {
	actor, err := json.Marshal(auditActor{Actor: entry.Actor, BotActor: entry.BotActor})
	if err != nil {
		return auditRow{}, err
	}
	metadata, err := auditMetadataJSON(entry.Metadata)
	if err != nil {
		return auditRow{}, err
	}
	return auditRow{
		id: storableText(entry.ID, 191), workspaceID: workspaceID, createdAt: entry.CreatedAt.UTC().Format(auditTimeLayout),
		actorType: storableText(entry.ActorType, 32), actorID: storableText(entry.ActorID, 191), userID: storableText(entry.Actor.ID, 191),
		actor: storableText(string(actor), auditMetadataMaxBytes), action: storableText(entry.Action, 191),
		resourceType: storableText(entry.ResourceType, 191), resourceID: storableText(entry.ResourceID, 191),
		ip: storableText(entry.IP, 64), userAgent: storableText(entry.UserAgent, 191), result: storableText(entry.Result, 32),
		metadata: metadata,
	}, nil
}

// digest is the row's chain hash: SHA-256 over the previous hash and every
// stored column, each followed by a unit separator.
func (row auditRow) digest() string {
	hash := sha256.New()
	for _, part := range []string{row.prevHash, row.workspaceID, strconv.FormatInt(row.seq, 10), row.id, row.createdAt, row.actorType, row.actorID, row.userID, row.actor, row.action, row.resourceType, row.resourceID, row.ip, row.userAgent, row.result, row.metadata} {
		hash.Write([]byte(part))
		hash.Write([]byte{0x1f})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func (row auditRow) entry() domain.AuditLogEntry {
	entry := domain.AuditLogEntry{ID: row.id, Action: row.action, ResourceType: row.resourceType, ResourceID: row.resourceID, ActorType: row.actorType, ActorID: row.actorID, IP: row.ip, UserAgent: row.userAgent, Result: row.result, Seq: row.seq, Hash: row.hash}
	entry.CreatedAt, _ = time.Parse(time.RFC3339Nano, row.createdAt)
	var actor auditActor
	if json.Unmarshal([]byte(row.actor), &actor) == nil {
		entry.Actor, entry.BotActor = actor.Actor, actor.BotActor
	}
	if row.metadata != "" && row.metadata != "{}" {
		_ = json.Unmarshal([]byte(row.metadata), &entry.Metadata)
	}
	return entry
}

type auditHead struct {
	seq, prunedSeq   int64
	hash, prunedHash string
}

// lockAuditHead reads (creating it when missing) and locks a workspace's
// chain head for the rest of tx.
func lockAuditHead(ctx context.Context, tx *sqlTx, workspaceID string) (auditHead, error) {
	const create = `INSERT INTO audit_chain_heads(workspace_id,seq,hash,pruned_seq,pruned_hash) VALUES(?,0,'',0,'') ON CONFLICT(workspace_id) DO NOTHING`
	read := func(lock string) (auditHead, error) {
		var head auditHead
		err := tx.QueryRowContext(ctx, `SELECT seq,hash,pruned_seq,pruned_hash FROM audit_chain_heads WHERE workspace_id=?`+lock, workspaceID).Scan(&head.seq, &head.hash, &head.prunedSeq, &head.prunedHash)
		return head, err
	}
	if tx.dialect == "sqlite" {
		// One writer at a time: writing first takes the database write lock
		// (waiting for it) instead of failing to upgrade a read.
		if _, err := tx.ExecContext(ctx, create, workspaceID); err != nil {
			return auditHead{}, err
		}
		return read("")
	}
	// A missing head is created in its own statement, outside tx: creating it
	// inside after a locking read would deadlock two first writers on MySQL
	// (gap locks). The plain read takes no lock; the locking read then sees
	// the committed row.
	if _, err := read(""); errors.Is(err, sql.ErrNoRows) && tx.database != nil {
		if _, err := tx.database.ExecContext(ctx, create, workspaceID); err != nil {
			return auditHead{}, err
		}
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return auditHead{}, err
	}
	return read(" FOR UPDATE")
}

// insertAuditEntriesTx appends entries (oldest first) to a workspace's log.
func insertAuditEntriesTx(ctx context.Context, tx *sqlTx, workspaceID string, entries []domain.AuditLogEntry) error {
	if len(entries) == 0 {
		return nil
	}
	head, err := lockAuditHead(ctx, tx, workspaceID)
	if err != nil {
		return fmt.Errorf("audit log head: %w", err)
	}
	for _, entry := range entries {
		row, err := auditRowFor(workspaceID, entry)
		if err != nil {
			return fmt.Errorf("audit entry %s: %w", entry.ID, err)
		}
		row.seq, row.prevHash = head.seq+1, head.hash
		row.hash = row.digest()
		if _, err := tx.ExecContext(ctx, `INSERT INTO audit_events(id,workspace_id,seq,created_at,actor_type,actor_id,user_id,actor,action,resource_type,resource_id,ip,user_agent,result,metadata,prev_hash,hash) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			row.id, row.workspaceID, row.seq, row.createdAt, row.actorType, row.actorID, row.userID, row.actor, row.action, row.resourceType, row.resourceID, row.ip, row.userAgent, row.result, row.metadata, row.prevHash, row.hash); err != nil {
			return fmt.Errorf("audit entry %s: %w", entry.ID, err)
		}
		head.seq, head.hash = row.seq, row.hash
	}
	_, err = tx.ExecContext(ctx, `UPDATE audit_chain_heads SET seq=?,hash=? WHERE workspace_id=?`, head.seq, head.hash, workspaceID)
	return err
}

// AppendAuditEntries writes entries (oldest first) to a workspace's audit
// log in their own transaction, completing them from ctx's AuditRequest.
func (s *SQLiteStore) AppendAuditEntries(ctx context.Context, workspaceID string, entries ...domain.AuditLogEntry) error {
	if len(entries) == 0 {
		return nil
	}
	request, _ := AuditRequestFrom(ctx)
	completed := make([]domain.AuditLogEntry, len(entries))
	for index, entry := range entries {
		completed[index] = completeAuditEntry(entry, request)
	}
	tx, err := s.db.BeginTx(context.WithoutCancel(ctx), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertAuditEntriesTx(ctx, tx, workspaceID, completed); err != nil {
		return err
	}
	return tx.Commit()
}

// RecordAccountAudit writes an account event (sign-in, sign-out, password or
// second-factor change, session revocation) to the audit log of every
// workspace the account is an active member of, or to the instance log when
// it has none or is unknown. userID may be empty when only the email the
// attempt named is known.
func (s *SQLiteStore) RecordAccountAudit(ctx context.Context, userID, email string, entry domain.AuditLogEntry) error {
	ctx = context.WithoutCancel(ctx)
	if userID == "" && strings.TrimSpace(email) != "" {
		if user, _, err := s.authUserByEmail(ctx, email); err == nil {
			userID = user.ID
		}
	}
	if userID != "" && entry.Actor.ID == "" {
		actor := domain.User{ID: userID}
		if err := s.db.QueryRowContext(ctx, `SELECT name,display_name,email,avatar_url FROM auth_users WHERE id=?`, userID).Scan(&actor.Name, &actor.DisplayName, &actor.Email, &actor.AvatarURL); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		entry.Actor = actor
		if entry.ResourceType == "account" && entry.ResourceID == "" {
			entry.ResourceID = userID
		}
	}
	workspaces := []string{}
	if userID != "" {
		rows, err := s.db.QueryContext(ctx, `SELECT workspace_id FROM workspace_memberships WHERE user_id=? AND status='active' ORDER BY workspace_id`, userID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			workspaces = append(workspaces, id)
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	if len(workspaces) == 0 {
		workspaces = []string{InstanceAuditWorkspace}
	}
	request, _ := AuditRequestFrom(ctx)
	base := completeAuditEntry(entry, request)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for index, workspaceID := range workspaces {
		item := base
		if index > 0 {
			item.ID = idgen.New("audit_")
		}
		if err := insertAuditEntriesTx(ctx, tx, workspaceID, []domain.AuditLogEntry{item}); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// AuditQuery narrows ListAuditEntries. Zero values do not filter.
type AuditQuery struct {
	Since, Until  time.Time
	ResourceTypes []string
	// ActorIDs match the acting user or the credential (API key, OAuth
	// client, SCIM token) that acted.
	ActorIDs   []string
	ResourceID string
	IP         string
	// Limit caps the entries returned, newest first (0: no cap).
	Limit int
}

const auditColumns = `id,workspace_id,seq,created_at,actor_type,actor_id,user_id,actor,action,resource_type,resource_id,ip,user_agent,result,metadata,prev_hash,hash`

func scanAuditRows(rows *sql.Rows) ([]auditRow, error) {
	defer rows.Close()
	result := []auditRow{}
	for rows.Next() {
		var row auditRow
		if err := rows.Scan(&row.id, &row.workspaceID, &row.seq, &row.createdAt, &row.actorType, &row.actorID, &row.userID, &row.actor, &row.action, &row.resourceType, &row.resourceID, &row.ip, &row.userAgent, &row.result, &row.metadata, &row.prevHash, &row.hash); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// ListAuditEntries returns a workspace's audit entries, newest first.
func (s *SQLiteStore) ListAuditEntries(ctx context.Context, workspaceID string, query AuditQuery) ([]domain.AuditLogEntry, error) {
	clauses, args := []string{"workspace_id=?"}, []any{workspaceID}
	if !query.Since.IsZero() {
		clauses, args = append(clauses, "created_at>=?"), append(args, query.Since.UTC().Format(auditTimeLayout))
	}
	if !query.Until.IsZero() {
		clauses, args = append(clauses, "created_at<?"), append(args, query.Until.UTC().Format(auditTimeLayout))
	}
	if len(query.ResourceTypes) > 0 {
		clause, values := bindList("resource_type", query.ResourceTypes)
		clauses, args = append(clauses, clause), append(args, values...)
	}
	if len(query.ActorIDs) > 0 {
		users, userArgs := bindList("user_id", query.ActorIDs)
		actors, actorArgs := bindList("actor_id", query.ActorIDs)
		clauses, args = append(clauses, "("+users+" OR "+actors+")"), append(append(args, userArgs...), actorArgs...)
	}
	if query.ResourceID != "" {
		clauses, args = append(clauses, "resource_id=?"), append(args, query.ResourceID)
	}
	if query.IP != "" {
		clauses, args = append(clauses, "ip=?"), append(args, query.IP)
	}
	statement := `SELECT ` + auditColumns + ` FROM audit_events WHERE ` + strings.Join(clauses, " AND ") + ` ORDER BY created_at DESC,seq DESC`
	if query.Limit > 0 {
		statement += ` LIMIT ` + strconv.Itoa(query.Limit)
	}
	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	stored, err := scanAuditRows(rows)
	if err != nil {
		return nil, err
	}
	entries := make([]domain.AuditLogEntry, len(stored))
	for index, row := range stored {
		entries[index] = row.entry()
	}
	return entries, nil
}

// AuditEntryTypeCounts counts a workspace's entries by resource type and
// action.
func (s *SQLiteStore) AuditEntryTypeCounts(ctx context.Context, workspaceID string) (map[[2]string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT resource_type,action,COUNT(*) FROM audit_events WHERE workspace_id=? GROUP BY resource_type,action`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[[2]string]int{}
	for rows.Next() {
		var resourceType, action string
		var count int
		if err := rows.Scan(&resourceType, &action, &count); err != nil {
			return nil, err
		}
		counts[[2]string{resourceType, action}] = count
	}
	return counts, rows.Err()
}

// AuditEntriesAfter returns up to limit entries with a seq above afterSeq,
// oldest first, for the audit log stream. A stream created before entries
// were numbered passes the time and id of the last entry it delivered
// (legacyAt non-zero); entries up to that point are skipped.
func (s *SQLiteStore) AuditEntriesAfter(ctx context.Context, workspaceID string, afterSeq int64, legacyAt time.Time, legacyID string, limit int) ([]domain.AuditLogEntry, error) {
	statement, args := `SELECT `+auditColumns+` FROM audit_events WHERE workspace_id=? AND seq>?`, []any{workspaceID, afterSeq}
	if afterSeq == 0 && !legacyAt.IsZero() {
		at := legacyAt.UTC().Format(auditTimeLayout)
		statement += ` AND (created_at>? OR created_at=? AND id>?)`
		args = append(args, at, at, legacyID)
	}
	statement += ` ORDER BY seq LIMIT ` + strconv.Itoa(max(limit, 1))
	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	stored, err := scanAuditRows(rows)
	if err != nil {
		return nil, err
	}
	entries := make([]domain.AuditLogEntry, len(stored))
	for index, row := range stored {
		entries[index] = row.entry()
	}
	return entries, nil
}

// AuditWorkspaces lists the workspace ids that have an audit log ("" is the
// instance log).
func (s *SQLiteStore) AuditWorkspaces(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT workspace_id FROM audit_chain_heads ORDER BY workspace_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// PruneAuditEvents deletes up to AuditPruneBatch of a workspace's oldest
// entries created before cutoff and reports how many it deleted. Only a
// prefix of the chain is removed (an entry stays while any older-numbered
// one is still inside the retention window), and the head records the last
// removed entry, so the remaining chain still verifies. The head row is
// locked first: servers pruning at the same time take turns.
func (s *SQLiteStore) PruneAuditEvents(ctx context.Context, workspaceID string, cutoff time.Time) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	head, err := lockAuditHead(ctx, tx, workspaceID)
	if err != nil {
		return 0, err
	}
	boundary := head.seq + 1
	var first sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT MIN(seq) FROM audit_events WHERE workspace_id=? AND created_at>=?`, workspaceID, cutoff.UTC().Format(auditTimeLayout)).Scan(&first); err != nil {
		return 0, err
	}
	if first.Valid {
		boundary = first.Int64
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,seq,hash FROM audit_events WHERE workspace_id=? AND seq<? ORDER BY seq LIMIT `+strconv.Itoa(AuditPruneBatch), workspaceID, boundary)
	if err != nil {
		return 0, err
	}
	ids := []string{}
	var lastSeq int64
	var lastHash string
	for rows.Next() {
		var id, hash string
		var seq int64
		if err := rows.Scan(&id, &seq, &hash); err != nil {
			rows.Close()
			return 0, err
		}
		ids, lastSeq, lastHash = append(ids, id), seq, hash
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	clause, args := bindList("id", ids)
	result, err := tx.ExecContext(ctx, `DELETE FROM audit_events WHERE workspace_id=? AND `+clause, append([]any{workspaceID}, args...)...)
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE audit_chain_heads SET pruned_seq=?,pruned_hash=? WHERE workspace_id=? AND pruned_seq<?`, lastSeq, lastHash, workspaceID, lastSeq); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	deleted, err := result.RowsAffected()
	return int(deleted), err
}

// AuditChainReport is the outcome of VerifyAuditChain.
type AuditChainReport = domain.AuditChainReport

// VerifyAuditChain recomputes a workspace's chain: entries must be numbered
// without gaps from the last pruned one to the head, each must link to the
// previous hash, and each stored hash must match its columns.
func (s *SQLiteStore) VerifyAuditChain(ctx context.Context, workspaceID string) (AuditChainReport, error) {
	var head auditHead
	err := s.db.QueryRowContext(ctx, `SELECT seq,hash,pruned_seq,pruned_hash FROM audit_chain_heads WHERE workspace_id=?`, workspaceID).Scan(&head.seq, &head.hash, &head.prunedSeq, &head.prunedHash)
	if errors.Is(err, sql.ErrNoRows) {
		return AuditChainReport{Valid: true}, nil
	}
	if err != nil {
		return AuditChainReport{}, err
	}
	report := AuditChainReport{Valid: true, PrunedThrough: head.prunedSeq, HeadHash: head.hash}
	fail := func(seq int64, problem string) (AuditChainReport, error) {
		report.Valid, report.BrokenAt, report.Problem = false, seq, problem
		return report, nil
	}
	expectedSeq, expectedHash := head.prunedSeq+1, head.prunedHash
	after := int64(-1)
	for {
		rows, err := s.db.QueryContext(ctx, `SELECT `+auditColumns+` FROM audit_events WHERE workspace_id=? AND seq>? AND seq<=? ORDER BY seq LIMIT `+strconv.Itoa(auditVerifyPage), workspaceID, after, head.seq)
		if err != nil {
			return AuditChainReport{}, err
		}
		page, err := scanAuditRows(rows)
		if err != nil {
			return AuditChainReport{}, err
		}
		for _, row := range page {
			if report.Entries == 0 && row.seq != expectedSeq {
				// Retention may have run since the head was read: the first
				// entry follows the prune point recorded now.
				if err := s.db.QueryRowContext(ctx, `SELECT pruned_seq,pruned_hash FROM audit_chain_heads WHERE workspace_id=?`, workspaceID).Scan(&head.prunedSeq, &head.prunedHash); err != nil {
					return AuditChainReport{}, err
				}
				expectedSeq, expectedHash, report.PrunedThrough = head.prunedSeq+1, head.prunedHash, head.prunedSeq
				if row.seq < expectedSeq {
					after = row.seq
					continue
				}
			}
			if row.seq != expectedSeq {
				return fail(expectedSeq, fmt.Sprintf("entry %d is missing", expectedSeq))
			}
			if row.prevHash != expectedHash {
				return fail(row.seq, "the entry does not link to the previous entry")
			}
			if row.digest() != row.hash {
				return fail(row.seq, "the entry was changed after it was written")
			}
			if report.Entries == 0 {
				report.FirstSeq = row.seq
			}
			report.Entries++
			report.LastSeq = row.seq
			expectedSeq, expectedHash = row.seq+1, row.hash
			after = row.seq
		}
		if len(page) < auditVerifyPage {
			break
		}
	}
	if expectedSeq != head.seq+1 {
		return fail(expectedSeq, fmt.Sprintf("entries %d to %d are missing", expectedSeq, head.seq))
	}
	if expectedHash != head.hash {
		return fail(head.seq, "the last entry does not match the recorded head")
	}
	return report, nil
}

// MigrateLegacyAuditLog moves audit entries the workspace documents still
// hold (written before the audit_events table, or by an older server during
// a rolling upgrade) into the table, oldest first, and removes them from the
// document in the same transaction. Entries already in the table are not
// written twice, so it is safe to repeat.
func (s *SQLiteStore) MigrateLegacyAuditLog(ctx context.Context) error {
	moved := 0
	for _, key := range s.WorkspaceKeys() {
		if data, ok := s.storedWorkspace(key); !ok || len(data.AuditLog) == 0 {
			continue
		}
		var count int
		err := s.rerunBackgroundWrite(ctx, "audit log migration", key, func() (err error) {
			count, err = s.migrateWorkspaceAuditLog(ctx, key)
			return err
		})
		if err != nil {
			return fmt.Errorf("workspace %q: %w", key, err)
		}
		moved += count
	}
	if moved > 0 {
		slog.Info("moved audit log entries into the audit_events table", "entries", moved)
	}
	return nil
}

func (s *SQLiteStore) migrateWorkspaceAuditLog(ctx context.Context, key string) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	before, ok := s.storedWorkspace(key)
	if !ok || len(before.AuditLog) == 0 {
		return 0, nil
	}
	legacy := slices.Clone(before.AuditLog)
	slices.SortStableFunc(legacy, func(a, b domain.AuditLogEntry) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	existing := map[string]bool{}
	for start := 0; start < len(legacy); start += 500 {
		ids := []string{}
		for _, entry := range legacy[start:min(len(legacy), start+500)] {
			ids = append(ids, entry.ID)
		}
		clause, args := bindList("id", ids)
		rows, err := tx.QueryContext(ctx, `SELECT id FROM audit_events WHERE `+clause, args...)
		if err != nil {
			return 0, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return 0, err
			}
			existing[id] = true
		}
		err = rows.Close()
		if err != nil {
			return 0, err
		}
	}
	pending := []domain.AuditLogEntry{}
	for _, entry := range legacy {
		if entry.ID == "" || existing[entry.ID] {
			continue
		}
		existing[entry.ID] = true
		entry.IP = legacyAuditIP(entry.Metadata)
		pending = append(pending, completeAuditEntry(entry, AuditRequest{}))
	}
	if err := insertAuditEntriesTx(ctx, tx, before.Workspace.ID, pending); err != nil {
		return 0, err
	}
	after := before
	after.AuditLog = []domain.AuditLogEntry{}
	if err := s.persistMetadataTx(ctx, tx, key, before, &after); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	s.installWorkspace(key, after, false)
	s.dropMetadataCache(ctx, key)
	s.mu.Lock()
	if !slices.Contains(s.startupCacheDrops, key) {
		s.startupCacheDrops = append(s.startupCacheDrops, key)
	}
	s.mu.Unlock()
	return len(pending), nil
}

// legacyAuditIP is the client address an entry written before the table
// kept in its details, if any.
func legacyAuditIP(metadata map[string]any) string {
	for _, key := range []string{"ip", "ipAddress", "clientIp"} {
		if value, ok := metadata[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}
