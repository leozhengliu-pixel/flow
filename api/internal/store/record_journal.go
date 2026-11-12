package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/idgen"
)

const recordJournalMaxBytes = 64 << 10

const RecordJournalRetentionBytes int64 = 256 << 20

var (
	ErrRecordJournalUnavailable = errors.New("record journal is unavailable")
	ErrRecordJournalCoverage    = errors.New("record journal writer coverage is incomplete")
)

type RecordDelta struct {
	Key     RecordKey `json:"key"`
	Version string    `json:"version"`
	Deleted bool      `json:"deleted"`
}

type CommittedChange struct {
	TxID          string        `json:"txId"`
	Seq           string        `json:"seq"`
	AuthzEpoch    string        `json:"authzEpoch"`
	Records       []RecordDelta `json:"records"`
	Invalidations []string      `json:"invalidations"`
}

type CommittedPage struct {
	Next           string            `json:"next"`
	Head           string            `json:"head"`
	AuthzEpoch     string            `json:"authzEpoch"`
	Transactions   []CommittedChange `json:"transactions"`
	HasMore        bool              `json:"hasMore"`
	ResyncRequired bool              `json:"resyncRequired"`
}

func journalDatabaseReady(db domainEventExecer) bool {
	switch d := db.(type) {
	case *sqlDatabase:
		return d.journalReady.Load()
	case *sqlTx:
		return d.database != nil && d.database.journalReady.Load()
	}
	return false
}

// recordJournalCommitHook receives every changed record in bounded batches
// inside the writer's transaction. P14 can register its recipient overlay
// here; an error rolls back every side effect, including the journal.
type recordJournalCommitHook func(context.Context, *sqlTx, string, []RecordDelta) error

// A participant that queues transaction-local publication state resets it
// here after rollback, including a savepoint rollback to the legacy path.
type recordJournalRollbackHook func(*sqlTx)

func (s *SQLiteStore) setRecordJournalCommitHook(hook recordJournalCommitHook) {
	if hook == nil {
		s.db.journalCommitHook.Store(nil)
	} else {
		s.db.journalCommitHook.Store(&hook)
	}
}

func (s *SQLiteStore) setRecordJournalRollbackHook(hook recordJournalRollbackHook) {
	if hook == nil {
		s.db.journalRollbackHook.Store(nil)
	} else {
		s.db.journalRollbackHook.Store(&hook)
	}
}

func (tx *sqlTx) resetRecordJournalParticipant() {
	if tx.database != nil {
		if hook := tx.database.journalRollbackHook.Load(); hook != nil {
			(*hook)(tx)
		}
	}
}

func recordJournalMode() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FLOW_RECORD_JOURNAL"))) {
	case "off", "0", "false":
		return "off"
	case "on":
		return "on"
	case "shadow":
		return "shadow"
	default:
		return "off"
	}
}

func journalWriteStatement(query string) bool {
	rest := strings.TrimSpace(query)
	word := func() string {
		rest = strings.TrimLeft(rest, " \t\r\n")
		i := 0
		for i < len(rest) && (rest[i] >= 'a' && rest[i] <= 'z' || rest[i] >= 'A' && rest[i] <= 'Z' || rest[i] == '_') {
			i++
		}
		result := rest[:i]
		rest = rest[i:]
		return result
	}
	verb := word()
	if strings.EqualFold(verb, "INSERT") {
		next := word()
		if strings.EqualFold(next, "OR") {
			word()
			next = word()
		} else if strings.EqualFold(next, "IGNORE") {
			next = word()
		}
		if !strings.EqualFold(next, "INTO") {
			return false
		}
	} else if strings.EqualFold(verb, "DELETE") {
		if !strings.EqualFold(word(), "FROM") {
			return false
		}
	} else if !strings.EqualFold(verb, "UPDATE") {
		return false
	}
	if !recordJournalTables[strings.ToLower(word())] {
		return false
	}
	// Session activity is not an authorization change. Its trigger also
	// ignores it, so it need not open an implicit journaling transaction.
	const touch = "update workspace_memberships set last_seen_at="
	query = strings.TrimSpace(query)
	return len(query) < len(touch) || !strings.EqualFold(query[:len(touch)], touch)
}

func (tx *sqlTx) beginRecordJournal(ctx context.Context) error {
	if tx.journalToken != "" || tx.numbering || tx.database == nil || !tx.database.journalReady.Load() || recordJournalMode() == "off" {
		return nil
	}
	prefix := "rjt"
	if tx.dialect == "sqlite" {
		prefix = "rjv2"
	}
	tx.journalToken = idgen.New(prefix)
	query := `UPDATE record_journal_context SET tx_id=? WHERE id=1`
	if tx.dialect == "mysql" {
		query = `SET @flow_record_tx=?`
	} else if tx.dialect == "postgres" {
		query = `SELECT set_config('flow.record_tx',?,true)`
	}
	var err error
	if statement := tx.database.journalWrites[query]; statement != nil {
		bound := tx.Tx.StmtContext(ctx, statement)
		_, err = bound.ExecContext(ctx, tx.journalToken)
		_ = bound.Close()
	} else {
		_, err = tx.Tx.ExecContext(ctx, rewriteSQL(query, tx.dialect), tx.journalToken)
	}
	if err == nil && tx.dialect == "mysql" {
		_, err = tx.Tx.ExecContext(ctx, `INSERT INTO record_journal_active(tx_id) VALUES(?)`, tx.journalToken)
	}
	if tx.dialect == "sqlite" && sqliteBusy(err) {
		return fmt.Errorf("%w: %w", ErrConcurrentWrite, err)
	}
	return err
}

func (tx *sqlTx) Rollback() error {
	if tx.journalToken != "" && tx.dialect == "mysql" {
		// MySQL session variables outlive rollback and pooled transactions.
		// Clear the token before returning this connection to the pool.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(tx.context()), time.Second)
		_, _ = tx.Tx.ExecContext(ctx, `SET @flow_record_tx=''`)
		cancel()
	}
	err := tx.Tx.Rollback()
	if err == nil && tx.journalToken != "" {
		tx.resetRecordJournalParticipant()
	}
	return err
}

type recordJournalDirty struct{ workspace, field, id, source string }

type pendingRecordJournal struct {
	workspace string
	change    CommittedChange
	events    []string
	bytes     int
	oversize  bool
}

func (pending *pendingRecordJournal) add(delta RecordDelta) {
	if pending.oversize {
		return
	}
	raw, _ := json.Marshal(delta)
	pending.bytes += len(raw) + 1
	if pending.bytes > recordJournalMaxBytes-1024 {
		pending.change.Records = []RecordDelta{}
		pending.change.Invalidations = []string{"workspace"}
		pending.oversize = true
		return
	}
	pending.change.Records = append(pending.change.Records, delta)
}

type recordJournalBudgetState struct {
	written, sequences, signals int
	counts                      *pendingIssueCounts
}

// flushRecordJournal drains only this transaction's trigger rows. Rows from
// old binaries remain a durable coverage failure. Large writes keep their
// original SQL budget and defer sidecars to repair when that budget is tight.
func (tx *sqlTx) flushRecordJournal() error {
	if tx.journalToken == "" {
		return nil
	}
	if done, err := tx.flushSQLRecordJournal(); done {
		return err
	}
	if err := tx.materializeInlineJournal(); err != nil {
		return err
	}
	if tx.journalToken == "" || tx.maxTransactionBytes <= 0 {
		return tx.flushRecordJournalRows()
	}
	ctx := tx.context()
	if _, err := tx.Tx.ExecContext(ctx, `SAVEPOINT flow_record_journal`); err != nil {
		return err
	}
	tx.journalBudget = &recordJournalBudgetState{tx.writtenBytes, len(tx.sequencing), len(tx.signals), tx.issueCounts}
	err := tx.flushRecordJournalRows()
	if !errors.Is(err, ErrTransactionWriteBudget) {
		return err
	}
	return tx.deferRecordJournalBudget()
}

func (tx *sqlTx) finishRecordJournalSavepoint() error {
	if tx.journalBudget == nil {
		return nil
	}
	_, err := tx.Tx.ExecContext(tx.context(), `RELEASE SAVEPOINT flow_record_journal`)
	if err == nil {
		tx.journalBudget = nil
	}
	return err
}

func (tx *sqlTx) retryLegacyRecordJournalBudget(err error) error {
	if !errors.Is(err, ErrTransactionWriteBudget) || tx.journalBudget == nil {
		return err
	}
	if err := tx.deferRecordJournalBudget(); err != nil {
		return err
	}
	if err := tx.queueEventSequencing(); err != nil {
		return err
	}
	if err := tx.flushAudit(); err != nil {
		return err
	}
	return tx.flushIssueCounts()
}

func (tx *sqlTx) deferRecordJournalBudget() error {
	// New derived SQL must not turn a previously valid large business write
	// into a rejected import/cascade. Retain trigger-captured keys/versions,
	// atomically make the catalog unavailable, and require bounded repair.
	ctx, state := tx.context(), tx.journalBudget
	if _, err := tx.Tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT flow_record_journal`); err != nil {
		return err
	}
	if err := tx.materializeInlineJournal(); err != nil {
		return err
	}
	tx.writeErr, tx.writtenBytes, tx.issueCounts = nil, state.written, state.counts
	tx.sequencing, tx.signals = tx.sequencing[:state.sequences], tx.signals[:state.signals]
	tx.resetRecordJournalParticipant()
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT workspace_key FROM record_journal_dirty WHERE tx_id=? AND source IN ('workspace_metadata_records','workspace_states')`, tx.journalToken)
	if err != nil {
		return err
	}
	for rows.Next() {
		var workspace string
		if err := rows.Scan(&workspace); err != nil {
			rows.Close()
			return err
		}
		tx.queueCacheRevision(workspace, cacheKindMetadata)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	query := `UPDATE metadata_catalog_collections SET last_record_key='',started=0,phase=0,omissions=0,digest='',revision='' WHERE workspace_key IN (SELECT workspace_key FROM record_journal_dirty WHERE tx_id=?)`
	if _, err := tx.Tx.ExecContext(ctx, rewriteSQL(query, tx.dialect), tx.journalToken); err != nil {
		return err
	}
	if tx.dialect == "sqlite" {
		_, err = tx.Tx.ExecContext(ctx, `UPDATE record_journal_context SET tx_id='' WHERE id=1`)
	} else if tx.dialect == "mysql" {
		if _, err = tx.Tx.ExecContext(ctx, `DELETE FROM record_journal_active WHERE tx_id=?`, tx.journalToken); err == nil {
			_, err = tx.Tx.ExecContext(ctx, `SET @flow_record_tx=''`)
		}
	}
	if err != nil {
		return err
	}
	if err := tx.finishRecordJournalSavepoint(); err != nil {
		return err
	}
	slog.Warn("record journal deferred to bounded repair", "reason", "transaction write budget", "transaction", tx.journalToken)
	return nil
}

func (tx *sqlTx) flushRecordJournalRows() error {
	if tx.journalToken == "" {
		return nil
	}
	ctx := tx.context()
	if tx.dialect != "sqlite" {
		var enabled int
		if err := tx.QueryRowContext(ctx, `SELECT enabled FROM record_journal_mode WHERE id=1`).Scan(&enabled); err != nil {
			return err
		}
		if enabled == 0 {
			// Disabling another replica must not reject a canonical write.
			// Retained dirty keys remain unavailable until activation repair.
			if tx.dialect == "mysql" {
				if _, err := tx.ExecContext(ctx, `DELETE FROM record_journal_active WHERE tx_id=?`, tx.journalToken); err != nil {
					return err
				}
				_, err := tx.ExecContext(ctx, `SET @flow_record_tx=''`)
				return err
			}
			return nil
		}
	}
	pending := map[string]*pendingRecordJournal{}
	get := func(workspace string) *pendingRecordJournal {
		if pending[workspace] == nil {
			pending[workspace] = &pendingRecordJournal{workspace: workspace, change: CommittedChange{TxID: idgen.New("rj"), Records: []RecordDelta{}, Invalidations: []string{}}}
			if tx.journalRepair {
				pending[workspace].oversize = true
				pending[workspace].change.Invalidations = []string{"workspace"}
			}
		}
		return pending[workspace]
	}
	for {
		rows, err := tx.QueryContext(ctx, `SELECT workspace_key,field,record_key,source FROM record_journal_dirty WHERE tx_id=? ORDER BY workspace_key,field,record_key LIMIT 128`, tx.journalToken)
		if err != nil {
			return err
		}
		batch := []recordJournalDirty{}
		for rows.Next() {
			var dirty recordJournalDirty
			if err := rows.Scan(&dirty.workspace, &dirty.field, &dirty.id, &dirty.source); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, dirty)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			break
		}
		for _, dirty := range batch {
			p := get(dirty.workspace)
			if dirty.source == "workspace_metadata_records" || dirty.source == "workspace_states" {
				tx.queueCacheRevision(dirty.workspace, cacheKindMetadata)
			} else if dirty.source == "issue_records" || dirty.source == "workspace_content_records" {
				tx.queueCacheRevision(dirty.workspace, cacheKindIssues)
			}
			if dirty.field == "@authz" {
				p.change.Invalidations = append(p.change.Invalidations, "authz")
			} else {
				var version int64
				var deleted int
				if err := tx.QueryRowContext(ctx, `SELECT version,deleted FROM record_versions WHERE workspace_key=? AND field=? AND record_key=?`, dirty.workspace, dirty.field, dirty.id).Scan(&version, &deleted); err != nil {
					return fmt.Errorf("journal version %s/%s/%s from %s: %w", dirty.workspace, dirty.field, dirty.id, dirty.source, err)
				}
				delta := RecordDelta{Key: RecordKey{dirty.field, dirty.id}, Version: strconv.FormatInt(version, 10), Deleted: deleted != 0}
				p.add(delta)
				if dirty.source == "workspace_metadata_records" {
					if err := tx.maintainRecordSummary(ctx, dirty, deleted != 0); err != nil {
						return fmt.Errorf("journal summary %s/%s/%s: %w", dirty.workspace, dirty.field, dirty.id, err)
					}
				}
				if hook := tx.database.journalCommitHook.Load(); hook != nil {
					var workspaceID string
					err := tx.QueryRowContext(ctx, `SELECT workspace_id FROM workspace_states WHERE workspace_key=?`, dirty.workspace).Scan(&workspaceID)
					if err != nil && !errors.Is(err, sql.ErrNoRows) {
						return err
					}
					if err == nil {
						if err := (*hook)(ctx, tx, workspaceID, []RecordDelta{delta}); err != nil {
							return err
						}
					}
				}
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM record_journal_dirty WHERE tx_id=? AND workspace_key=? AND field=? AND record_key=?`, tx.journalToken, dirty.workspace, dirty.field, dirty.id); err != nil {
				return err
			}
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT p.event_id,w.workspace_key FROM record_journal_event_pending p JOIN workspace_states w ON w.workspace_id=`+recordJournalIdentitySQL(tx.dialect, "p.workspace_id")+` WHERE p.tx_id=? ORDER BY p.workspace_id,p.event_id`, tx.journalToken)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, workspace string
		if err := rows.Scan(&id, &workspace); err != nil {
			rows.Close()
			return err
		}
		get(workspace).events = append(get(workspace).events, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	workspaces := make([]string, 0, len(pending))
	for workspace := range pending {
		workspaces = append(workspaces, workspace)
	}
	slices.Sort(workspaces)
	for _, workspace := range workspaces {
		p := pending[workspace]
		var workspaceID string
		err := tx.QueryRowContext(ctx, `SELECT workspace_id FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&workspaceID)
		if errors.Is(err, sql.ErrNoRows) {
			// A purge or rename removed this key in the same transaction.
			continue
		}
		if err != nil {
			return err
		}
		p.change.AuthzEpoch, err = readAuthzEpoch(ctx, tx, workspaceID)
		if err != nil {
			return err
		}
		slices.Sort(p.change.Invalidations)
		p.change.Invalidations = slices.Compact(p.change.Invalidations)
		if len(p.change.Records) == 0 && len(p.change.Invalidations) == 0 {
			// An event can describe a legacy side table rather than one of
			// the canonical record families. Never acknowledge it as an
			// empty change: consumers must refresh the affected workspace.
			p.change.Invalidations = []string{"workspace"}
		}
		var seq any
		if tx.dialect == "sqlite" {
			seq, err = tx.advanceRecordJournalHead(ctx, workspaceID, 1)
		} else {
			// Journal markers follow the transaction's domain events in the
			// existing commit-order queue. They never consume legacy seqs.
			tx.sequencing = append(tx.sequencing, pendingEventSequence{workspaceID: workspaceID, eventID: p.change.TxID})
		}
		if err != nil {
			return err
		}
		raw, err := json.Marshal(p.change)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO record_journal(id,workspace_id,seq,authz_epoch,payload,created_at) VALUES(?,?,?,?,?,?)`, p.change.TxID, workspaceID, seq, p.change.AuthzEpoch, raw, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
		for _, event := range p.events {
			if _, err := tx.ExecContext(ctx, `INSERT INTO record_journal_events(event_id,journal_id,workspace_id) VALUES(?,?,?)`, event, p.change.TxID, workspaceID); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM record_journal_event_pending WHERE tx_id=?`, tx.journalToken); err != nil {
		return err
	}
	if tx.dialect == "sqlite" {
		_, err = tx.ExecContext(ctx, `UPDATE record_journal_context SET tx_id='' WHERE id=1`)
	} else if tx.dialect == "mysql" {
		if _, err = tx.ExecContext(ctx, `DELETE FROM record_journal_active WHERE tx_id=?`, tx.journalToken); err == nil {
			_, err = tx.ExecContext(ctx, `SET @flow_record_tx=''`)
		}
	}
	return err
}

func (tx *sqlTx) maintainRecordSummary(ctx context.Context, dirty recordJournalDirty, deleted bool) error {
	// Backfill locks the checkpoint before catalog rows. Use the same order
	// so a dual write cannot deadlock a backfill installing this record.
	if _, err := tx.ExecContext(ctx, `UPDATE metadata_catalog_collections SET revision=? WHERE workspace_key=? AND field=?`, tx.journalToken, dirty.workspace, dirty.field); err != nil {
		return err
	}
	var oldOmitted int
	err := tx.QueryRowContext(ctx, `SELECT omitted FROM metadata_catalog WHERE workspace_key=? AND field=? AND record_key=?`, dirty.workspace, dirty.field, dirty.id).Scan(&oldOmitted)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	newOmitted := 0
	if deleted {
		for _, table := range []string{"metadata_catalog_relations", "metadata_catalog"} {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE workspace_key=? AND field=? AND record_key=?`, dirty.workspace, dirty.field, dirty.id); err != nil {
				return err
			}
		}
	} else {
		var size, order int64
		var prefix []byte
		data := "data"
		if tx.dialect == "sqlite" {
			data = "CAST(data AS BLOB)"
		}
		if err := tx.QueryRowContext(ctx, `SELECT LENGTH(`+data+`),collection_order,SUBSTR(`+data+`,1,64) FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`, dirty.workspace, dirty.field, dirty.id).Scan(&size, &order, &prefix); err != nil {
			return err
		}
		catalog := SQLiteStore{dialect: tx.dialect}
		key := RecordKey{dirty.field, dirty.id}
		var row metadataCatalogRow
		prefix = bytes.TrimSpace(prefix)
		if len(prefix) > 0 && prefix[0] == '{' {
			var err error
			row, err = catalog.projectCanonicalMetadata(ctx, tx, dirty.workspace, key, size)
			if err != nil {
				return err
			}
		} else {
			hash := sha256.New()
			if _, err := io.Copy(hash, &canonicalMetadataReader{ctx: ctx, tx: tx, workspace: dirty.workspace, key: key, size: size}); err != nil {
				return err
			}
			summary, _ := json.Marshal(map[string]string{"id": dirty.id})
			row = metadataCatalogRow{key: key, bodyBytes: size, version: hex.EncodeToString(hash.Sum(nil)), summary: summary, omitted: true}
		}
		row.order = order
		if row.omitted {
			newOmitted = 1
		}
		if err := writeMetadataCatalogRow(ctx, tx, dirty.workspace, row); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE metadata_catalog_collections SET omissions=omissions+? WHERE workspace_key=? AND field=? AND (phase>=1 OR (phase=0 AND started=1 AND last_record_key>=?))`, newOmitted-oldOmitted, dirty.workspace, dirty.field, dirty.id)
	return err
}

func (tx *sqlTx) advanceRecordJournalHead(ctx context.Context, workspaceID string, count int) (int64, error) {
	if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_record_journal_heads(workspace_id,head,pruned) VALUES(?,?,0) ON CONFLICT(workspace_id) DO UPDATE SET head=workspace_record_journal_heads.head+excluded.head`, workspaceID, count); err != nil {
		return 0, err
	}
	var head int64
	err := tx.QueryRowContext(ctx, `SELECT head FROM workspace_record_journal_heads WHERE workspace_id=?`, workspaceID).Scan(&head)
	return head, err
}

func (tx *sqlTx) numberRecordJournal(ctx context.Context, workspaceID string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	head, err := tx.advanceRecordJournalHead(ctx, workspaceID, len(ids))
	if err != nil {
		return err
	}
	for i, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE record_journal SET seq=? WHERE id=? AND seq IS NULL`, head-int64(len(ids))+int64(i)+1, id); err != nil {
			return err
		}
	}
	return nil
}

// RecordJournalHead is a journal cursor, distinct from the legacy event seq.
// Both streams use the same commit-order sequencer; the original event seq
// remains gap-free even when a transaction creates no domain event.
func (s *SQLiteStore) RecordJournalHead(ctx context.Context, workspaceID string) (string, error) {
	var head int64
	err := s.db.QueryRowContext(ctx, `SELECT head FROM workspace_record_journal_heads WHERE workspace_id=?`, workspaceID).Scan(&head)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return strconv.FormatInt(head, 10), err
}

// RecordJournalEventSeq maps an existing event to its complete transaction's
// journal cursor, for sync response headers and optimistic-write barriers.
func (s *SQLiteStore) RecordJournalEventSeq(ctx context.Context, eventID string) (string, error) {
	var seq sql.NullInt64
	query := `SELECT j.seq FROM record_journal_events r JOIN record_journal j ON j.id=r.journal_id WHERE r.event_id=?`
	args := []any{eventID}
	if s.dialect == "sqlite" {
		query = `SELECT seq FROM record_journal WHERE id='rje:'||? UNION ALL SELECT j.seq FROM record_journal_events r JOIN record_journal j ON j.id=r.journal_id WHERE r.event_id=? LIMIT 1`
		args = append(args, eventID)
	}
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&seq)
	if err != nil {
		return "", err
	}
	if !seq.Valid {
		return "", ErrRecordJournalUnavailable
	}
	return strconv.FormatInt(seq.Int64, 10), nil
}

// RecordJournalEventSeqForType locates unambiguous legacy event evidence for
// a journal transaction. Eventless or multi-event changes need invalidation
// when their deletion grants cannot be reconstructed from one legacy event.
func (s *SQLiteStore) RecordJournalEventSeqForType(ctx context.Context, workspaceID, txID, eventType string) (seq int64, found bool, err error) {
	var count int
	var number sql.NullInt64
	query := `SELECT COUNT(e.id),MAX(e.seq) FROM record_journal j LEFT JOIN record_journal_events r ON r.journal_id=j.id LEFT JOIN domain_events e ON e.id=` + recordJournalIdentitySQL(s.dialect, "r.event_id") + ` AND e.event_type=? WHERE j.workspace_id=? AND j.id=? GROUP BY j.id`
	args := []any{eventType, workspaceID, txID}
	if s.dialect == "sqlite" {
		query = `WITH journal AS (SELECT id FROM record_journal WHERE workspace_id=? AND id=?), evidence AS (
			SELECT event_id FROM record_journal_events r JOIN journal j ON r.journal_id=j.id
			UNION SELECT SUBSTR(id,5) FROM journal WHERE SUBSTR(id,1,4)='rje:'
		) SELECT COUNT(e.id),MAX(e.seq) FROM journal j LEFT JOIN evidence r ON 1=1 LEFT JOIN domain_events e ON e.id=r.event_id AND e.event_type=? GROUP BY j.id`
		args = []any{workspaceID, txID, eventType}
	}
	err = s.db.QueryRowContext(ctx, query, args...).Scan(&count, &number)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil || count != 1 || !number.Valid {
		return 0, true, err
	}
	return number.Int64, true, nil
}

// RecordVersion returns the monotone committed version, retaining tombstones
// across deletion/recreation. It is a trusted storage primitive; callers must
// authorize the key before disclosing either existence or a version.
func (s *SQLiteStore) RecordVersion(ctx context.Context, workspace string, key RecordKey) (version string, deleted bool, err error) {
	if !s.db.journalReady.Load() {
		return "", false, ErrRecordJournalUnavailable
	}
	var number int64
	var tombstone int
	if err = checkRecordJournalCoverage(ctx, s.db, workspace); err != nil {
		return "", false, err
	}
	err = s.db.QueryRowContext(ctx, `SELECT version,deleted FROM record_versions WHERE workspace_key=? AND field=? AND record_key=?`, workspace, key.Kind, key.ID).Scan(&number, &tombstone)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return strconv.FormatInt(number, 10), tombstone != 0, err
}

func checkRecordJournalCoverage(ctx context.Context, reader metadataRowReader, workspace string) error {
	var gaps int
	token := ""
	sqlite := false
	if tx, ok := reader.(*sqlTx); ok {
		token = tx.journalToken
		sqlite = tx.dialect == "sqlite"
	} else if db, ok := reader.(*sqlDatabase); ok {
		sqlite = db.dialect == "sqlite"
	}
	query := `SELECT (SELECT COUNT(*) FROM record_journal_coverage WHERE workspace_key=?)+(SELECT COUNT(*) FROM record_journal_dirty WHERE workspace_key=? AND (?='' OR tx_id<>?))+(SELECT 1-enabled FROM record_journal_mode WHERE id=1)`
	args := []any{workspace, workspace, token, token}
	if sqlite {
		query += `+(SELECT COUNT(*) FROM record_journal_context i,json_each(i.keys) k WHERE i.id=1 AND json_extract(k.value,'$.workspace')=? AND (?='' OR (SELECT tx_id FROM record_journal_context WHERE id=1)<>?))
			+(SELECT COUNT(*) FROM record_journal_context i,json_each(i.events) e JOIN workspace_states w ON w.workspace_id=json_extract(e.value,'$.workspaceId') WHERE i.id=1 AND w.workspace_key=? AND (?='' OR (SELECT tx_id FROM record_journal_context WHERE id=1)<>?))`
		args = append(args, workspace, token, token, workspace, token, token)
	}
	if err := reader.QueryRowContext(ctx, query, args...).Scan(&gaps); err != nil {
		return err
	}
	if gaps != 0 {
		return ErrRecordJournalCoverage
	}
	return nil
}

func (s *SQLiteStore) committedMetadataRecordVersion(ctx context.Context, reader metadataRowReader, workspace string, item *MetadataRecord) error {
	if recordJournalMode() != "on" {
		return nil
	}
	if !s.db.journalReady.Load() {
		return ErrRecordJournalUnavailable
	}
	if err := checkRecordJournalCoverage(ctx, reader, workspace); err != nil {
		return err
	}
	var version int64
	var deleted int
	err := reader.QueryRowContext(ctx, `SELECT version,deleted FROM record_versions WHERE workspace_key=? AND field=? AND record_key=?`, workspace, item.Key.Kind, item.Key.ID).Scan(&version, &deleted)
	if errors.Is(err, sql.ErrNoRows) {
		if item.Missing {
			return nil
		}
		return ErrRecordJournalCoverage
	}
	if err != nil {
		return err
	}
	if (deleted != 0) != item.Missing {
		return ErrRecordJournalCoverage
	}
	item.Version = strconv.FormatInt(version, 10)
	return nil
}

// ReadCommittedChanges returns complete transactions, never partial record
// sets. Missing old-writer coverage is an error; oversized transactions become
// one bounded invalidation. P11/P12 must authorize/redact this trusted stream.
func (s *SQLiteStore) ReadCommittedChanges(ctx context.Context, workspaceID, after string, limit, maxBytes int) (CommittedPage, error) {
	page := CommittedPage{Next: after, Head: "0", Transactions: []CommittedChange{}}
	if !s.db.journalReady.Load() || recordJournalMode() == "off" {
		return page, ErrRecordJournalUnavailable
	}
	since := int64(0)
	var err error
	if after != "" {
		since, err = strconv.ParseInt(after, 10, 64)
	}
	if err != nil || since < 0 || workspaceID == "" {
		return page, ErrMetadataCursor
	}
	page.Next = strconv.FormatInt(since, 10)
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if maxBytes <= 0 || maxBytes > MetadataRecordPageMaxBytes {
		maxBytes = MetadataRecordPageMaxBytes
	}
	if maxBytes < 1024 {
		return page, ErrMetadataPageBudget
	}
	if err := s.rescueStaleSequencing(ctx, workspaceID); err != nil {
		return page, err
	}
	tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	var workspace string
	var head, pruned int64
	if err := tx.QueryRowContext(ctx, `SELECT workspace_key FROM workspace_states WHERE workspace_id=?`, workspaceID).Scan(&workspace); err != nil {
		return page, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT head,pruned FROM workspace_record_journal_heads WHERE workspace_id=?`, workspaceID).Scan(&head, &pruned); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return page, err
	}
	page.Head = strconv.FormatInt(head, 10)
	page.AuthzEpoch, err = readAuthzEpoch(ctx, tx, workspaceID)
	if err != nil {
		return page, err
	}
	if since > head || since < pruned {
		page.ResyncRequired = true
		return page, nil
	}
	var gaps int
	if err := checkRecordJournalCoverage(ctx, tx, workspace); err != nil {
		page.ResyncRequired = true
		return page, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,seq,authz_epoch,LENGTH(payload),CASE WHEN LENGTH(payload)<=? THEN payload ELSE NULL END FROM record_journal WHERE workspace_id=? AND seq>? ORDER BY seq LIMIT ?`, maxBytes-512, workspaceID, since, limit+1)
	if err != nil {
		return page, err
	}
	used := 512
	for rows.Next() {
		var id, epoch string
		var seq, size int64
		var raw []byte
		if err := rows.Scan(&id, &seq, &epoch, &size, &raw); err != nil {
			rows.Close()
			return page, err
		}
		if len(page.Transactions) == limit {
			page.HasMore = true
			break
		}
		change := CommittedChange{TxID: id, Seq: strconv.FormatInt(seq, 10), AuthzEpoch: epoch, Records: []RecordDelta{}, Invalidations: []string{"workspace"}}
		if len(raw) != 0 {
			if err := json.Unmarshal(raw, &change); err != nil {
				rows.Close()
				return page, err
			}
			change.Seq = strconv.FormatInt(seq, 10)
		}
		encoded, _ := json.Marshal(change)
		if used+len(encoded) > maxBytes {
			page.HasMore = true
			break
		}
		used += len(encoded)
		page.Transactions = append(page.Transactions, change)
		page.Next = change.Seq
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return page, err
	}
	if !page.HasMore {
		page.Next = page.Head
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM record_journal_event_pending WHERE workspace_id=?`, workspaceID).Scan(&gaps); err != nil {
		return page, err
	}
	if gaps == 0 {
		// An old sequencer can discard journal-only queue markers. A
		// committed journal without its cursor or rescue row is a gap too.
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM record_journal j WHERE j.workspace_id=? AND j.seq IS NULL AND `+recordJournalMissingSequenceSQL(s.dialect), workspaceID).Scan(&gaps); err != nil {
			return page, err
		}
	}
	if gaps != 0 {
		page.Transactions = []CommittedChange{}
		page.Next, page.ResyncRequired = after, true
		return page, fmt.Errorf("%w: unjournaled events", ErrRecordJournalCoverage)
	}
	return page, nil
}

// PruneRecordJournal removes at most 128 complete transactions per call,
// independently of the legacy event retention policy. The persisted floor
// makes a reader behind pruning request a fresh manifest. Versions/tombstones
// remain, so an old upsert still cannot resurrect a deleted record.
func (s *SQLiteStore) PruneRecordJournal(ctx context.Context, workspaceID string, before time.Time, maxRetainedBytes int64) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var retained int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(LENGTH(payload)+256),0) FROM record_journal WHERE workspace_id=?`, workspaceID).Scan(&retained); err != nil {
		return 0, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,seq,created_at,LENGTH(payload)+256 FROM record_journal WHERE workspace_id=? AND seq IS NOT NULL AND seq<(SELECT head FROM workspace_record_journal_heads WHERE workspace_id=?) ORDER BY seq LIMIT 128`, workspaceID, workspaceID)
	if err != nil {
		return 0, err
	}
	type expired struct {
		id  string
		seq int64
	}
	batch := []expired{}
	for rows.Next() {
		var item expired
		var created string
		var size int64
		if err := rows.Scan(&item.id, &item.seq, &created, &size); err != nil {
			rows.Close()
			return 0, err
		}
		at, err := time.Parse(time.RFC3339Nano, created)
		if err != nil {
			rows.Close()
			return 0, err
		}
		if !at.Before(before) && (maxRetainedBytes <= 0 || retained <= maxRetainedBytes) {
			break
		}
		retained -= size
		batch = append(batch, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	if len(batch) > 0 {
		ids := make([]string, len(batch))
		for i, item := range batch {
			ids[i] = item.id
		}
		clause, args := bindList("journal_id", ids)
		if _, err := tx.ExecContext(ctx, `DELETE FROM record_journal_events WHERE `+clause, args...); err != nil {
			return 0, err
		}
		clause, args = bindList("id", ids)
		if _, err := tx.ExecContext(ctx, `DELETE FROM record_journal WHERE `+clause, args...); err != nil {
			return 0, err
		}
		floor := batch[len(batch)-1].seq
		if _, err := tx.ExecContext(ctx, `UPDATE workspace_record_journal_heads SET pruned=? WHERE workspace_id=? AND pruned<?`, floor, workspaceID, floor); err != nil {
			return 0, err
		}
	}
	return len(batch), tx.Commit()
}

func (s *SQLiteStore) pruneRecordJournalRetention(ctx context.Context, before time.Time) error {
	if !s.db.journalReady.Load() || recordJournalMode() == "off" {
		return nil
	}
	var workspaceID string
	err := s.db.QueryRowContext(ctx, `SELECT workspace_id FROM record_journal WHERE seq IS NOT NULL GROUP BY workspace_id HAVING COUNT(*)>1 AND (MIN(created_at)<? OR SUM(LENGTH(payload)+256)>?) ORDER BY MIN(created_at) LIMIT 1`, before.UTC().Format(time.RFC3339Nano), RecordJournalRetentionBytes).Scan(&workspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	for range 32 {
		count, err := s.PruneRecordJournal(ctx, workspaceID, before, RecordJournalRetentionBytes)
		if err != nil || count < 128 {
			return err
		}
	}
	return nil
}
