package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Record journal capture modes (server.database.recordJournalCapture).
const (
	// RecordJournalCaptureAuto keeps database triggers where the account
	// can install them. A MySQL account without that authority (no TRIGGER
	// grant, or binary logging that requires SUPER) captures in the
	// application instead. A database already switched to application
	// capture stays there.
	RecordJournalCaptureAuto = "auto"
	// RecordJournalCaptureTriggers always uses triggers. Without trigger
	// authority a default MySQL start keeps legacy reads.
	RecordJournalCaptureTriggers = "triggers"
	// RecordJournalCaptureApplication (MySQL) records journal inputs in the
	// writer's transaction and issues no trigger DDL. Triggers installed
	// earlier keep firing harmlessly until they are dropped.
	RecordJournalCaptureApplication = "application"
)

// RecordJournalCaptureModes lists the accepted setting values.
var RecordJournalCaptureModes = []string{RecordJournalCaptureAuto, RecordJournalCaptureTriggers, RecordJournalCaptureApplication}

func normalizeRecordJournalCapture(value string) string {
	switch value = strings.ToLower(strings.TrimSpace(value)); value {
	case RecordJournalCaptureTriggers, RecordJournalCaptureApplication:
		return value
	default:
		return RecordJournalCaptureAuto
	}
}

// recordJournalCaptureSchema (MySQL) persists the capture a database uses,
// so a restart captures in the application before the journal stage runs
// and an automatic start does not switch back to triggers on its own.
// triggers records that a trigger-mode start set out to install the
// canonical-table triggers and no removal followed: they may still fire,
// even where this account cannot see them in information_schema.
// (record_journal_mode.capturing keeps meaning "the journal captures".)
const recordJournalCaptureSchema = `CREATE TABLE IF NOT EXISTS record_journal_capture(id INTEGER PRIMARY KEY,mode VARCHAR(16) NOT NULL,triggers INTEGER NOT NULL,changed_at VARCHAR(40) NOT NULL)`

type recordJournalCaptureState struct {
	mode     string
	triggers bool
	found    bool
}

func (s *SQLiteStore) persistedRecordJournalCapture(ctx context.Context) (recordJournalCaptureState, error) {
	var state recordJournalCaptureState
	var triggers int
	err := s.db.QueryRowContext(ctx, `SELECT mode,triggers FROM record_journal_capture WHERE id=1`).Scan(&state.mode, &triggers)
	if errors.Is(err, sql.ErrNoRows) {
		return state, nil
	}
	state.triggers, state.found = triggers != 0, err == nil
	return state, err
}

func (s *SQLiteStore) persistRecordJournalCapture(ctx context.Context, mode string, triggers bool) error {
	if _, err := s.db.ExecContext(ctx, recordJournalCaptureSchema); err != nil {
		return err
	}
	installed := 0
	if triggers {
		installed = 1
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO record_journal_capture(id,mode,triggers,changed_at) VALUES(1,?,?,?) ON CONFLICT(id) DO UPDATE SET mode=excluded.mode,triggers=excluded.triggers,changed_at=excluded.changed_at`,
		mode, installed, formatStoredTime(time.Now()))
	return err
}

// detectRecordJournalCapture runs before the schema stages that can rewrite
// canonical rows. On a database already in application capture with an
// enabled journal, those writes are captured (as untracked writers, like
// the triggers capture them) instead of being missed until repair.
func (s *SQLiteStore) detectRecordJournalCapture(ctx context.Context) error {
	if s.dialect != "mysql" || recordJournalMode(s.dialect) == "off" || s.recordJournalCapture == RecordJournalCaptureTriggers {
		return nil
	}
	var mode string
	var enabled, triggers int
	err := s.db.QueryRowContext(ctx, `SELECT c.mode,m.enabled,c.triggers FROM record_journal_capture c CROSS JOIN record_journal_mode m WHERE c.id=1 AND m.id=1`).Scan(&mode, &enabled, &triggers)
	if err != nil {
		// A database without the journal tables has nothing to capture.
		return nil
	}
	if mode == RecordJournalCaptureApplication && enabled != 0 {
		s.db.captureTriggers.Store(triggers != 0)
		s.db.appCapture.Store(true)
	}
	return nil
}

// mysqlTriggerAuthorityDenied reports whether this account certainly cannot
// create triggers, so an automatic start never issues the DDL. Grants that
// are not visible here (active roles) count as possibly sufficient: the
// start then attempts the DDL and falls back on a denial.
func (s *SQLiteStore) mysqlTriggerAuthorityDenied(ctx context.Context) (bool, error) {
	var binlog, trust int
	var role string
	if err := s.db.QueryRowContext(ctx, `SELECT @@GLOBAL.log_bin,@@GLOBAL.log_bin_trust_function_creators,COALESCE(CURRENT_ROLE(),'NONE')`).Scan(&binlog, &trust, &role); err != nil {
		return false, err
	}
	if role != "NONE" && role != "" {
		return false, nil
	}
	grantee := `CONCAT('''',SUBSTRING_INDEX(CURRENT_USER(),'@',1),'''@''',SUBSTRING_INDEX(CURRENT_USER(),'@',-1),'''')`
	var trigger, super int
	if err := s.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM information_schema.USER_PRIVILEGES WHERE GRANTEE=`+grantee+` AND PRIVILEGE_TYPE='TRIGGER')
		+(SELECT COUNT(*) FROM information_schema.SCHEMA_PRIVILEGES WHERE GRANTEE=`+grantee+` AND PRIVILEGE_TYPE='TRIGGER' AND DATABASE() LIKE TABLE_SCHEMA)
		+(SELECT COUNT(*) FROM information_schema.TABLE_PRIVILEGES WHERE GRANTEE=`+grantee+` AND PRIVILEGE_TYPE='TRIGGER' AND TABLE_SCHEMA=DATABASE()),
		(SELECT COUNT(*) FROM information_schema.USER_PRIVILEGES WHERE GRANTEE=`+grantee+` AND PRIVILEGE_TYPE='SUPER')`).Scan(&trigger, &super); err != nil {
		return false, err
	}
	return trigger == 0 || binlog != 0 && trust == 0 && super == 0, nil
}

// RecordJournalCapture reports how this replica captures journal inputs:
// "triggers", "application", or "" when the journal is not capturing.
func (s *SQLiteStore) RecordJournalCapture() string {
	switch {
	case s == nil || s.db == nil || !s.db.journalReady.Load():
		return ""
	case s.db.appCapture.Load():
		return RecordJournalCaptureApplication
	default:
		return RecordJournalCaptureTriggers
	}
}

// RecordJournalCaptureStatus is the `record-journal-capture status` report.
type RecordJournalCaptureStatus struct {
	// Configured is this process's setting; Capture what it uses.
	Configured string `json:"configured"`
	Capture    string `json:"capture"`
	// Persisted is the capture the database last switched to.
	Persisted string `json:"persisted"`
	// TriggersRecorded: a start recorded trigger installation and no drop
	// followed, so triggers may still fire alongside application capture.
	TriggersRecorded bool `json:"triggersRecorded"`
	Enabled          bool `json:"enabled"`
	// UncoveredWorkspaces still need RepairRecordJournalPass.
	UncoveredWorkspaces int `json:"uncoveredWorkspaces"`
}

func (s *SQLiteStore) RecordJournalCaptureStatus(ctx context.Context) (RecordJournalCaptureStatus, error) {
	status := RecordJournalCaptureStatus{Configured: s.recordJournalCapture, Capture: s.RecordJournalCapture()}
	var enabled, capturing int
	if err := s.db.QueryRowContext(ctx, `SELECT enabled,capturing FROM record_journal_mode WHERE id=1`).Scan(&enabled, &capturing); err != nil {
		return status, err
	}
	status.Enabled, status.TriggersRecorded = enabled != 0, capturing != 0
	if s.dialect == "mysql" {
		persisted, err := s.persistedRecordJournalCapture(ctx)
		if err != nil {
			return status, err
		}
		status.Persisted = persisted.mode
		if persisted.found {
			status.TriggersRecorded = persisted.triggers
		}
	}
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT workspace_key FROM record_journal_coverage UNION SELECT workspace_key FROM record_journal_dirty) uncovered`).Scan(&status.UncoveredWorkspaces)
	return status, err
}

// ScheduleRecordJournalResync makes the named workspaces (every workspace
// when none is named) repeat the bounded activation repair and advances
// their authorization epochs, in one transaction. Operators run it after
// changing canonical rows with SQL outside the application on a database
// that captures in the application; serving reports a resync until
// RepairRecordJournalPass has republished each workspace.
func (s *SQLiteStore) ScheduleRecordJournalResync(ctx context.Context, workspaceKeys ...string) (int, error) {
	if !s.db.journalReady.Load() {
		return 0, ErrRecordJournalUnavailable
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	filter, args := "1=1", []any(nil)
	if len(workspaceKeys) > 0 {
		filter, args = bindList("workspace_key", workspaceKeys)
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_states WHERE `+filter, args...).Scan(&count); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO record_journal_coverage(workspace_key,phase,last_field,last_key) SELECT workspace_key,0,'','' FROM workspace_states WHERE `+filter+` ON CONFLICT(workspace_key) DO UPDATE SET phase=0,last_field='',last_key=''`, args...); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_authz_epochs(workspace_id,epoch) SELECT workspace_id,1 FROM workspace_states WHERE `+filter+` ON CONFLICT(workspace_id) DO UPDATE SET epoch=workspace_authz_epochs.epoch+1`, args...); err != nil {
		return 0, err
	}
	return count, tx.Commit()
}

// DropRecordJournalTriggers finishes a switch to application capture: it
// removes the canonical-table triggers an earlier trigger-mode start
// installed. Run it only once every replica captures in the application
// (otherwise a replica that relies on the triggers would stop capturing). It
// needs an account with the TRIGGER privilege; it is idempotent.
func (s *SQLiteStore) DropRecordJournalTriggers(ctx context.Context) error {
	if s.dialect != "mysql" {
		return fmt.Errorf("application capture is available on MySQL only")
	}
	if !s.db.appCapture.Load() {
		return fmt.Errorf("set server.database.recordJournalCapture to application before dropping triggers")
	}
	return s.WithStartupLock(ctx, "record journal trigger removal", func(ctx context.Context) error {
		if err := s.persistRecordJournalCapture(ctx, RecordJournalCaptureApplication, true); err != nil {
			return err
		}
		if err := s.dropRecordJournalTriggers(ctx, true); err != nil {
			return err
		}
		if err := s.persistRecordJournalCapture(ctx, RecordJournalCaptureApplication, false); err != nil {
			return err
		}
		s.db.captureTriggers.Store(false)
		return nil
	})
}
