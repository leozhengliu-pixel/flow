package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"flow/api/internal/domain"
)

// ErrConcurrentWrite reports that a full-workspace mutation would overwrite
// or delete a record another server changed after the mutation loaded it.
// The store retries such a mutation on fresh records before returning it.
var ErrConcurrentWrite = errors.New("this change conflicts with a change made at the same time; try again")

// fullMutationAttempts bounds how often a full-workspace mutation reruns
// after a concurrent record change.
const fullMutationAttempts = 3

// A full-workspace mutation (mutateFull) loads every issue and content
// record outside its write transaction, runs the callback and then replaces
// the collections. Standalone record transactions (issue creation, scoped
// issue and comment writes, agent replies) commit on other servers without
// the workspace lock in that window. recordBase remembers what the mutation
// loaded so the replace can tell its own changes from theirs:
//
//   - a row it did not load was committed meanwhile and is kept;
//   - a row it loaded that changed or disappeared meanwhile is left as the
//     other server wrote it when the callback did not change that record,
//     and is a conflict (ErrConcurrentWrite) when it did;
//   - a row it loaded and the callback removed is deleted only while it is
//     still as loaded.
type recordBase struct {
	// issues is nil when the issues were not loaded from their records.
	issues map[string]recordFingerprint
	// content holds the loaded records by kind; a kind missing from it was
	// not loaded from records.
	content map[string]map[contentRecordKey]recordFingerprint
}

// recordFingerprint hashes a record as stored when loaded and as the
// callback received it (encoded the way the replace encodes it).
type recordFingerprint struct{ stored, loaded [32]byte }

type contentRecordKey struct{ resource, id string }

func newRecordBase() *recordBase {
	return &recordBase{content: map[string]map[contentRecordKey]recordFingerprint{}}
}

func (base *recordBase) contentKind(kind string) map[contentRecordKey]recordFingerprint {
	if base == nil {
		return nil
	}
	return base.content[kind]
}

func (base *recordBase) issueRecords() map[string]recordFingerprint {
	if base == nil {
		return nil
	}
	return base.issues
}

// fingerprintLoaded records how each loaded record reads in data, the
// snapshot the callback is about to receive.
func (base *recordBase) fingerprintLoaded(data domain.Bootstrap) error {
	for _, issue := range data.Issues {
		fingerprint, ok := base.issues[issue.ID]
		if !ok {
			continue
		}
		values, err := issueRecordValues("", issue)
		if err != nil {
			return err
		}
		fingerprint.loaded = sha256.Sum256(values[len(values)-1].([]byte))
		base.issues[issue.ID] = fingerprint
	}
	if err := fingerprintContent(base.content["activity"], data.Activities); err != nil {
		return err
	}
	if err := fingerprintContent(base.content["comment"], data.Comments); err != nil {
		return err
	}
	if err := fingerprintContent(base.content["notification"], notificationRecords(data.Notifications)); err != nil {
		return err
	}
	return fingerprintContent(base.content["delivery"], map[string][]domain.NotificationDelivery{"": data.NotificationDeliveries})
}

func fingerprintContent[T any](records map[contentRecordKey]recordFingerprint, items map[string][]T) error {
	if records == nil {
		return nil
	}
	for resource, values := range items {
		for _, value := range values {
			id, hash, err := contentRecordHash(value)
			if err != nil {
				return err
			}
			key := contentRecordKey{resource, id}
			if fingerprint, ok := records[key]; ok {
				fingerprint.loaded = hash
				records[key] = fingerprint
			}
		}
	}
	return nil
}

// contentRecordHash is a content record's id and the hash of the payload
// syncContentRecords would write for it.
func contentRecordHash[T any](value T) (string, [32]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", [32]byte{}, err
	}
	var identity struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &identity); err != nil {
		return "", [32]byte{}, err
	}
	return identity.ID, sha256.Sum256(raw), nil
}

// readIssueRecordsWithBase reads the workspace's issues like
// readIssueRecords and remembers each stored payload's hash in base.
func (s *SQLiteStore) readIssueRecordsWithBase(ctx context.Context, workspace string, base *recordBase) ([]domain.Issue, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,data FROM issue_records WHERE workspace_key=? ORDER BY collection_order,id`, workspace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	issues := []domain.Issue{}
	fingerprints := map[string]recordFingerprint{}
	for rows.Next() {
		var id string
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var issue domain.Issue
		if err := json.Unmarshal(raw, &issue); err != nil {
			return nil, err
		}
		normalizeIssueRecord(&issue)
		issues = append(issues, issue)
		fingerprints[id] = recordFingerprint{stored: sha256.Sum256(raw)}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	base.issues = fingerprints
	return issues, nil
}

// readContentRecordsWithBase reads one kind like readContentRecords and
// remembers each stored payload's hash in base.
func readContentRecordsWithBase[T any](ctx context.Context, s *SQLiteStore, workspace, kind string, base *recordBase) (map[string][]T, error) {
	if base == nil {
		return readContentRecords[T](ctx, s, workspace, kind, nil)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT resource_id,id,data FROM workspace_content_records WHERE workspace_key=? AND kind=? ORDER BY created_at,id`, workspace, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := map[string][]T{}
	fingerprints := map[contentRecordKey]recordFingerprint{}
	for rows.Next() {
		var key contentRecordKey
		var raw []byte
		if err := rows.Scan(&key.resource, &key.id, &raw); err != nil {
			return nil, err
		}
		var item T
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		values[key.resource] = append(values[key.resource], item)
		fingerprints[key] = recordFingerprint{stored: sha256.Sum256(raw)}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	base.content[kind] = fingerprints
	return values, nil
}

// lockReplacedRecords takes, in one order (issue number sequence, issues,
// content records), the row locks a based replace needs on MySQL and
// PostgreSQL, so no standalone record transaction commits a change to a
// loaded row between the replace's comparison and its write. SQLite has a
// single writer: a transaction that read before another one committed
// fails to write instead.
func (s *SQLiteStore) lockReplacedRecords(ctx context.Context, tx *sqlTx, workspace string, base *recordBase, issues bool, kinds []string) error {
	if base == nil || s.dialect == "sqlite" {
		return nil
	}
	if issues && base.issues != nil {
		var last int
		if err := tx.QueryRowContext(ctx, `SELECT last_number FROM issue_number_sequences WHERE workspace_key=? FOR UPDATE`, workspace).Scan(&last); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err := lockRows(ctx, tx, `SELECT id FROM issue_records WHERE workspace_key=? FOR UPDATE`, workspace); err != nil {
			return err
		}
	}
	locked := []any{workspace}
	for _, kind := range kinds {
		if base.content[kind] != nil {
			locked = append(locked, kind)
		}
	}
	if len(locked) == 1 {
		return nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(locked)-1), ",")
	return lockRows(ctx, tx, `SELECT id FROM workspace_content_records WHERE workspace_key=? AND kind IN (`+placeholders+`) FOR UPDATE`, locked...)
}

func lockRows(ctx context.Context, tx *sqlTx, query string, args ...any) error {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	for rows.Next() {
	}
	err = rows.Err()
	rows.Close()
	return err
}
