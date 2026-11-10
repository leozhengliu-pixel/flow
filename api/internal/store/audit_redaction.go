package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// Erasing a person from the audit log.
//
// The audit log keeps what happened, and erasure must remove who the person
// was, without breaking the hash chain that shows the log was not edited.
// The chain hash covers the identifying columns (ids, action, resource,
// time, result) directly and the personal ones (actor, ip, user_agent,
// metadata) only through personal_digest, a SHA-256 commitment over a random
// per-row salt and those columns (auditRow.personalCommitment). While a row
// keeps its salt, VerifyAuditChain recomputes the commitment, so any edit of
// the personal columns is detected.
//
// RedactAuditPerson rewrites the personal columns of the person's rows (their
// name, email and avatar become the pseudonymous "Deleted user" identity,
// their client address and browser are dropped, and their name and email
// leave the details of entries about them), clears the salt and keeps the
// commitment: the chain still verifies, and without the salt the commitment
// cannot be tested against a guessed name or email. Each batch appends a
// redaction record to the same chain (action personal_data_redacted, resource
// the person's pseudonymous id, details the redacted seqs). A row without a
// salt that no later record lists fails verification, so removing a salt is
// not a way to change an entry unnoticed. Ids, actions, resources and times
// stay covered by the chain hash as before.

// AuditActionPersonalDataRedacted is the action of a redaction record.
const AuditActionPersonalDataRedacted = "personal_data_redacted"

// auditRedactionBatch is how many rows one transaction (and one redaction
// record) covers; the record's seq ranges stay well within the details limit.
const auditRedactionBatch = 500

// auditRedactionRange is a closed seq range a redaction record lists, with
// the seq of that record.
type auditRedactionRange struct {
	from, to, record int64
}

type auditRedactions []auditRedactionRange

// covers reports whether a redaction record written after seq lists it.
func (r auditRedactions) covers(seq int64) bool {
	for _, item := range r {
		if item.from <= seq && seq <= item.to && item.record > seq {
			return true
		}
	}
	return false
}

type auditRedactionDetails struct {
	Seqs [][2]int64 `json:"seqs"`
}

// loadAuditRedactions reads a chain's redaction records.
func (s *SQLiteStore) loadAuditRedactions(ctx context.Context, workspaceID string) (auditRedactions, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT seq,metadata FROM audit_events WHERE workspace_id=? AND action=? AND actor_type=?`, workspaceID, AuditActionPersonalDataRedacted, AuditActorSystem)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := auditRedactions{}
	for rows.Next() {
		var seq int64
		var raw string
		if err := rows.Scan(&seq, &raw); err != nil {
			return nil, err
		}
		var details auditRedactionDetails
		if json.Unmarshal([]byte(raw), &details) != nil {
			continue
		}
		for _, item := range details.Seqs {
			result = append(result, auditRedactionRange{from: item[0], to: item[1], record: seq})
		}
	}
	return result, rows.Err()
}

// seqRanges compresses ascending seqs into closed ranges.
func seqRanges(seqs []int64) [][2]int64 {
	ranges := [][2]int64{}
	for _, seq := range seqs {
		if n := len(ranges); n > 0 && ranges[n-1][1]+1 == seq {
			ranges[n-1][1] = seq
			continue
		}
		ranges = append(ranges, [2]int64{seq, seq})
	}
	return ranges
}

// auditPersonClause selects the rows that name the person: made by them,
// about them, or with their id (or, when given, their email) in the actor or
// the details. Redaction records are never redacted.
func auditPersonClause(userID, email string) (string, []any) {
	clause := `action<>? AND (user_id=? OR actor_id=? OR resource_id=? OR actor LIKE ? ESCAPE '!' OR metadata LIKE ? ESCAPE '!'`
	args := []any{AuditActionPersonalDataRedacted, userID, userID, userID, labelReferencePattern(userID), labelReferencePattern(userID)}
	if email = strings.ToLower(strings.TrimSpace(email)); email != "" {
		clause += ` OR LOWER(actor) LIKE ? ESCAPE '!' OR LOWER(metadata) LIKE ? ESCAPE '!'`
		args = append(args, labelReferencePattern(email), labelReferencePattern(email))
	}
	return clause + `)`, args
}

// RedactAuditPerson erases userID (and their email, when given) from the
// audit log chains of workspaceIDs, or from every chain (the instance log
// included) when workspaceIDs is nil, keeping each chain verifiable. It
// returns how many entries it rewrote. Repeating it is safe.
func (s *SQLiteStore) RedactAuditPerson(ctx context.Context, workspaceIDs []string, userID, email string) (int, error) {
	if strings.TrimSpace(userID) == "" {
		return 0, fmt.Errorf("redact audit person: no user id")
	}
	if workspaceIDs == nil {
		// The instance log's id is "" (InstanceAuditWorkspace).
		clause, args := auditPersonClause(userID, email)
		rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT workspace_id FROM audit_events WHERE `+clause, args...)
		if err != nil {
			return 0, err
		}
		workspaceIDs = []string{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return 0, err
			}
			workspaceIDs = append(workspaceIDs, id)
		}
		if err := rows.Close(); err != nil {
			return 0, err
		}
	}
	total := 0
	for _, workspaceID := range workspaceIDs {
		after := int64(0)
		for {
			redacted, last, more, err := s.redactAuditBatch(ctx, workspaceID, userID, email, after)
			total += redacted
			if err != nil {
				return total, fmt.Errorf("audit log %q: %w", workspaceID, err)
			}
			if !more {
				break
			}
			after = last
		}
	}
	if total > 0 {
		slog.Info("erased a person from the audit log", "entries", total, "chains", len(workspaceIDs))
	}
	return total, nil
}

// redactAuditBatch rewrites up to auditRedactionBatch of the person's rows
// with a seq above after, in one transaction with their redaction record. It
// reports how many it rewrote, the last seq it looked at and whether more
// rows may follow.
func (s *SQLiteStore) redactAuditBatch(ctx context.Context, workspaceID, userID, email string, after int64) (int, int64, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, false, err
	}
	defer tx.Rollback()
	// Appends and retention wait for the chain head while rows change.
	if _, err := lockAuditHead(ctx, tx, workspaceID); err != nil {
		return 0, 0, false, err
	}
	clause, args := auditPersonClause(userID, email)
	rows, err := tx.QueryContext(ctx, `SELECT `+auditColumns+` FROM audit_events WHERE workspace_id=? AND seq>? AND `+clause+` ORDER BY seq LIMIT `+fmt.Sprint(auditRedactionBatch), append([]any{workspaceID, after}, args...)...)
	if err != nil {
		return 0, 0, false, err
	}
	page, err := scanAuditRows(rows)
	if err != nil || len(page) == 0 {
		return 0, 0, false, err
	}
	anonymous := domain.DeletedUserIdentity(userID)
	seqs := []int64{}
	for _, row := range page {
		next := redactAuditRow(row, anonymous, email)
		if next.actor == row.actor && next.ip == row.ip && next.userAgent == row.userAgent && next.metadata == row.metadata {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE audit_events SET actor=?,ip=?,user_agent=?,metadata=?,personal_salt='' WHERE workspace_id=? AND seq=?`, next.actor, next.ip, next.userAgent, next.metadata, workspaceID, row.seq); err != nil {
			return 0, 0, false, err
		}
		seqs = append(seqs, row.seq)
	}
	last := page[len(page)-1].seq
	more := len(page) == auditRedactionBatch
	if len(seqs) == 0 {
		return 0, last, more, tx.Commit()
	}
	record := completeAuditEntry(domain.AuditLogEntry{
		Action: AuditActionPersonalDataRedacted, ResourceType: "account", ResourceID: userID, ActorType: AuditActorSystem,
		Metadata: map[string]any{"seqs": seqRanges(seqs)}, CreatedAt: time.Now().UTC(),
	}, AuditRequest{})
	if err := insertAuditEntriesTx(ctx, tx, workspaceID, []domain.AuditLogEntry{record}); err != nil {
		return 0, 0, false, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, false, err
	}
	return len(seqs), last, more, nil
}

// redactAuditRow is row with the person's personal data removed: their
// identity in the actor and details becomes the pseudonymous one, the
// personal fields of an entry about them are blanked, their email leaves
// the details, and an entry they made loses its client address and browser.
func redactAuditRow(row auditRow, anonymous domain.User, email string) auditRow {
	about := row.userID == anonymous.ID || row.actorID == anonymous.ID || row.resourceID == anonymous.ID
	if next, changed := ScrubPersonJSON([]byte(row.actor), anonymous, false); changed {
		row.actor = storableText(string(next), auditMetadataMaxBytes)
	}
	if next, changed := ScrubPersonJSON([]byte(row.metadata), anonymous, about); changed {
		row.metadata = storableText(string(next), auditMetadataMaxBytes)
	}
	if next, changed := scrubEmailJSON([]byte(row.actor), email); changed {
		row.actor = storableText(string(next), auditMetadataMaxBytes)
	}
	if next, changed := scrubEmailJSON([]byte(row.metadata), email); changed {
		row.metadata = storableText(string(next), auditMetadataMaxBytes)
	}
	if row.userID == anonymous.ID || row.actorID == anonymous.ID {
		row.ip, row.userAgent = "", ""
	}
	return row
}

// scrubEmailJSON removes every occurrence of email (any case) from the
// string values of a JSON document.
func scrubEmailJSON(raw []byte, email string) ([]byte, bool) {
	email = strings.TrimSpace(email)
	if email == "" || !strings.Contains(strings.ToLower(string(raw)), strings.ToLower(email)) {
		return raw, false
	}
	pattern := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(email))
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return raw, false
	}
	changed := false
	var walk func(any) any
	walk = func(node any) any {
		switch typed := node.(type) {
		case map[string]any:
			for key, child := range typed {
				typed[key] = walk(child)
			}
			return typed
		case []any:
			for index, child := range typed {
				typed[index] = walk(child)
			}
			return typed
		case string:
			if pattern.MatchString(typed) {
				changed = true
				return pattern.ReplaceAllString(typed, "")
			}
			return typed
		default:
			return node
		}
	}
	value = walk(value)
	if !changed {
		return raw, false
	}
	next, err := json.Marshal(value)
	if err != nil {
		return raw, false
	}
	return StripJSONNUL(next), true
}
