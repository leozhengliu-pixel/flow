package store

import (
	"context"
	"crypto/sha256"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

const NotificationStateCapability = "recipient-inbox-v1"
const NotificationStateEvent = "notification.state"
const NotificationStateMaxChangedIDs = 50

// NotificationState is a recipient's overlay, never a workspace broadcast.
// Revision is a decimal string; notification contents and actors are omitted.
type NotificationState struct {
	Revision    string   `json:"revision"`
	UnreadCount int      `json:"unreadCount"`
	ChangedIDs  []string `json:"changedIds,omitempty"`
	Invalidated bool     `json:"invalidated"`
}

// NotificationStateChange must include the previous owner of removed or
// reassigned records. An unknown/large changed set uses Invalidated instead.
type NotificationStateChange struct {
	WorkspaceKey, RecipientID string
	ChangedIDs                []string
	Invalidated               bool
}

// NotificationStateCoverage is supplied by the integration owners. In
// particular, a single upgraded writer cannot enable mixed-replica delivery.
type NotificationStateCoverage struct {
	Writers                                      []string
	CommitHook, RecipientProjection, AllReplicas bool
	SnoozeExpiry                                 bool
}

func NotificationStateWriters() []string {
	return []string{
		"writeContentRecord", "writeContentRecordRows", "syncContentRecordsFrom", "persistWorkspaceTx", "UpdateNotificationRecord",
		"BatchNotificationRecords", "ErasePersonFromWorkspace", "scrubPersonCopies",
		"deleteTeamOwnedIssueRecords", "PurgeWorkspaceRows", "moveWorkspaceKeyRows", "rewriteWorkspaceKeyJSON",
	}
}

// NotificationStateReady is deliberately false for partial coverage. P10 may
// advertise the capability only after P09/P12 and the expiry producer are ready.
func NotificationStateReady(coverage NotificationStateCoverage) bool {
	if !coverage.CommitHook || !coverage.RecipientProjection || !coverage.AllReplicas || !coverage.SnoozeExpiry {
		return false
	}
	for _, writer := range NotificationStateWriters() {
		if !slices.Contains(coverage.Writers, writer) {
			return false
		}
	}
	return true
}

// The existing revision table is already migrated, workspace-scoped and
// purged with its workspace. Reserve one counter per recipient, within its
// VARCHAR(32) kind column. No notification bodies enter this sidecar.
func notificationStateKind(recipient string) string {
	sum := sha256.Sum256([]byte(recipient))
	return fmt.Sprintf("inbox:%x", sum[:13])
}

func (s *SQLiteStore) NotificationState(ctx context.Context, workspace, recipient string) (NotificationState, error) {
	return readNotificationState(ctx, s.db, workspace, recipient)
}

func readNotificationState(ctx context.Context, db *sqlDatabase, workspace, recipient string) (NotificationState, error) {
	result := NotificationState{}
	if workspace == "" || recipient == "" {
		return result, ErrAuthForbidden
	}
	store := SQLiteStore{dialect: db.dialect}
	// Both values come from the same statement snapshot, including on MySQL.
	// Match QueryNotifications' default filters exactly, including expired
	// snoozes and legacy JSON null/missing fields rather than trusting status.
	query := `SELECT (SELECT COALESCE(SUM(revision),0) FROM workspace_cache_revisions WHERE workspace_key=? AND kind=? AND shard=0),COUNT(*) FROM workspace_content_records WHERE workspace_key=? AND kind='notification' AND owner_id=? AND ` +
		store.jsonText("data", "archivedAt") + ` IS NULL AND ` + store.jsonText("data", "deletedAt") + ` IS NULL AND ` +
		store.jsonText("data", "readAt") + ` IS NULL AND (next_attempt_at='' OR next_attempt_at<=?)`
	var revision int64
	err := db.QueryRowContext(ctx, query, workspace, notificationStateKind(recipient), workspace, recipient, time.Now().UTC().Format(issueRecordTimestamp)).Scan(&revision, &result.UnreadCount)
	result.Revision = strconv.FormatInt(revision, 10)
	return result, err
}

type preparedNotificationState struct {
	change   NotificationStateChange
	revision string
}

type NotificationStateCommit struct {
	database *sqlDatabase
	states   []preparedNotificationState
}

type NotificationStatePublisher func(workspace, recipient string, state NotificationState)

// PrepareNotificationStates is P09's Commit hook. Call once, after all record
// writes and before the transaction commits. Capture owners before SQL deletes,
// including direct batch SQL; the legacy "news" signals omit read-state edits.
// Only call the returned commit's Publish after a successful database commit.
func (tx *sqlTx) PrepareNotificationStates(ctx context.Context, changes []NotificationStateChange) (NotificationStateCommit, error) {
	commit := NotificationStateCommit{database: tx.database}
	byRecipient := map[string]*NotificationStateChange{}
	for _, change := range changes {
		if change.WorkspaceKey == "" || change.RecipientID == "" {
			return NotificationStateCommit{}, fmt.Errorf("notification state requires workspace and recipient")
		}
		key := change.WorkspaceKey + "\x00" + change.RecipientID
		current := byRecipient[key]
		if current == nil {
			current = &NotificationStateChange{WorkspaceKey: change.WorkspaceKey, RecipientID: change.RecipientID}
			byRecipient[key] = current
		}
		current.Invalidated = current.Invalidated || change.Invalidated
		for _, id := range change.ChangedIDs {
			if id == "" || len(id) > 191 {
				return NotificationStateCommit{}, fmt.Errorf("invalid changed notification id")
			}
			if !current.Invalidated && !slices.Contains(current.ChangedIDs, id) {
				current.ChangedIDs = append(current.ChangedIDs, id)
				if len(current.ChangedIDs) > NotificationStateMaxChangedIDs {
					current.Invalidated = true
				}
			}
		}
		if current.Invalidated || len(current.ChangedIDs) == 0 {
			current.Invalidated, current.ChangedIDs = true, nil
		}
	}
	keys := make([]string, 0, len(byRecipient))
	for key := range byRecipient {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		change := *byRecipient[key]
		slices.Sort(change.ChangedIDs)
		kind := notificationStateKind(change.RecipientID)
		// Lock only this recipient, late in the transaction. Reading the row
		// after updating it observes our own increment even under repeatable read.
		if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_cache_revisions(workspace_key,kind,shard,revision) VALUES(?,?,0,1) ON CONFLICT(workspace_key,kind,shard) DO UPDATE SET revision=workspace_cache_revisions.revision+1`, change.WorkspaceKey, kind); err != nil {
			return NotificationStateCommit{}, err
		}
		var revision int64
		if err := tx.QueryRowContext(ctx, `SELECT revision FROM workspace_cache_revisions WHERE workspace_key=? AND kind=? AND shard=0`, change.WorkspaceKey, kind).Scan(&revision); err != nil {
			return NotificationStateCommit{}, err
		}
		commit.states = append(commit.states, preparedNotificationState{change, strconv.FormatInt(revision, 10)})
	}
	return commit, nil
}

// Publish reads once per affected recipient, after commit, through the
// canonical database. P12 must project each envelope to its named recipient
// using current authorization, also on replay. A failed read is recoverable
// and must not turn the already committed business write into a failed write.
func (commit NotificationStateCommit) Publish(ctx context.Context, publish NotificationStatePublisher) error {
	if publish == nil {
		return nil
	}
	var failures []string
	for _, prepared := range commit.states {
		change := prepared.change
		state, err := readNotificationState(ctx, commit.database, change.WorkspaceKey, change.RecipientID)
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		state.Invalidated = change.Invalidated || state.Revision != prepared.revision
		if !state.Invalidated {
			state.ChangedIDs = slices.Clone(change.ChangedIDs)
		}
		publish(change.WorkspaceKey, change.RecipientID, state)
	}
	if len(failures) != 0 {
		return fmt.Errorf("notification state publication: %s", strings.Join(failures, "; "))
	}
	return nil
}
