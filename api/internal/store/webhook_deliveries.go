package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// Webhook signing secrets and the outbound delivery queue live in their own
// tables: secrets are needed in plain form to sign each delivery (so they
// cannot stay hashed in the workspace document), and deliveries are written
// and claimed row by row, which must not rewrite workspace metadata.
//
// Delivery rows are the queue: an instance claims a due row with a
// conditional UPDATE (status and lease checked in the WHERE clause), so any
// number of instances can run the worker; a crashed instance's claim expires
// with its lease and the row becomes due again.

const webhookSecretCipherPrefix = "v1:"

func (s *SQLiteStore) ensureWebhookDeliveries(ctx context.Context) error {
	blob, text := "BLOB", "TEXT"
	if s.dialect == "mysql" {
		blob, text = "LONGBLOB", "MEDIUMTEXT"
	}
	if s.dialect == "postgres" {
		blob = "BYTEA"
	}
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS webhook_signing_secrets (ref VARCHAR(191) PRIMARY KEY,workspace_key VARCHAR(191) NOT NULL,secret ` + text + ` NOT NULL,created_at BIGINT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS webhook_deliveries (id VARCHAR(191) PRIMARY KEY,workspace_key VARCHAR(191) NOT NULL,webhook_id VARCHAR(191) NOT NULL,secret_ref VARCHAR(191) NOT NULL,kind VARCHAR(32) NOT NULL,event_id VARCHAR(191) NOT NULL,event_type VARCHAR(64) NOT NULL,action VARCHAR(64) NOT NULL,url ` + text + ` NOT NULL,payload ` + blob + ` NOT NULL,status VARCHAR(32) NOT NULL,attempt INTEGER NOT NULL,max_attempts INTEGER NOT NULL,next_attempt_at BIGINT NOT NULL,lease_until BIGINT NOT NULL,response_status INTEGER NOT NULL,response_body ` + text + `,error_text ` + text + `,duration_ms BIGINT NOT NULL,attempts ` + blob + `,redelivery_of VARCHAR(191) NOT NULL,created_at BIGINT NOT NULL,updated_at BIGINT NOT NULL,completed_at BIGINT NOT NULL)`,
	} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	for _, statement := range []string{
		`CREATE INDEX IF NOT EXISTS webhook_delivery_due ON webhook_deliveries(status,next_attempt_at)`,
		`CREATE INDEX IF NOT EXISTS webhook_delivery_hook ON webhook_deliveries(webhook_id,created_at)`,
		`CREATE INDEX IF NOT EXISTS webhook_delivery_created ON webhook_deliveries(created_at)`,
	} {
		if s.dialect == "mysql" {
			statement = strings.Replace(statement, "CREATE INDEX IF NOT EXISTS", "CREATE INDEX", 1)
		}
		if _, err := s.db.ExecContext(ctx, statement); err != nil && !(s.dialect == "mysql" && isDuplicateIndex(err)) {
			return err
		}
	}
	return nil
}

// webhookEvent attaches the transient actor and the enriched realtime payload
// (input, post-mutation entity) to an event handed to the webhook sink.
func webhookEvent(ctx context.Context, event domain.DomainEvent, snapshot json.RawMessage) domain.DomainEvent {
	actor, _ := actorFromContext(ctx)
	event.ActorID = actor.ID
	event.Snapshot = snapshot
	return event
}

// WebhookSecretRef names the signing secret of a workspace webhook.
func WebhookSecretRef(webhookID string) string { return "webhook:" + webhookID }

// ApplicationWebhookSecretRef names the signing secret an OAuth application
// uses for every workspace that installed it.
func ApplicationWebhookSecretRef(clientID string) string { return "app:" + clientID }

// sealWebhookSecret encrypts a signing secret when FLOW_CONNECTOR_SECRET_KEY is
// configured; otherwise it is stored as is, like the audit stream secret.
func sealWebhookSecret(ref, secret string) (string, error) {
	aead, err := connectorCipher()
	if err != nil {
		return secret, nil
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return webhookSecretCipherPrefix + base64.StdEncoding.EncodeToString(aead.Seal(nonce, nonce, []byte(secret), []byte(ref))), nil
}

func openWebhookSecret(ref, stored string) (string, error) {
	if !strings.HasPrefix(stored, webhookSecretCipherPrefix) {
		return stored, nil
	}
	aead, err := connectorCipher()
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, webhookSecretCipherPrefix))
	if err != nil || len(raw) < aead.NonceSize() {
		return "", errors.New("invalid webhook secret ciphertext")
	}
	plain, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], []byte(ref))
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func (s *SQLiteStore) SetWebhookSigningSecret(ctx context.Context, ref, workspace, secret string) error {
	if ref == "" || len(ref) > 191 || secret == "" {
		return ErrIssueQuery
	}
	sealed, err := sealWebhookSecret(ref, secret)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO webhook_signing_secrets(ref,workspace_key,secret,created_at) VALUES(?,?,?,?) ON CONFLICT(ref) DO UPDATE SET workspace_key=excluded.workspace_key,secret=excluded.secret,created_at=excluded.created_at`, ref, workspace, sealed, time.Now().UTC().UnixMilli())
	return err
}

// WebhookSigningSecret returns the secret and whether one is stored.
func (s *SQLiteStore) WebhookSigningSecret(ctx context.Context, ref string) (string, bool, error) {
	var stored string
	err := s.db.QueryRowContext(ctx, `SELECT secret FROM webhook_signing_secrets WHERE ref=?`, ref).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	secret, err := openWebhookSecret(ref, stored)
	if err != nil {
		return "", false, err
	}
	return secret, true, nil
}

func (s *SQLiteStore) DeleteWebhookSigningSecret(ctx context.Context, ref string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM webhook_signing_secrets WHERE ref=?`, ref)
	return err
}

const webhookDeliveryColumns = `id,workspace_key,webhook_id,secret_ref,kind,event_id,event_type,action,url,status,attempt,max_attempts,next_attempt_at,lease_until,response_status,response_body,error_text,duration_ms,attempts,redelivery_of,created_at,updated_at,completed_at`

func millis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UTC().UnixMilli()
}

func fromMillis(value int64) *time.Time {
	if value <= 0 {
		return nil
	}
	t := time.UnixMilli(value).UTC()
	return &t
}

// EnqueueWebhookDelivery inserts a delivery. Delivery IDs are derived from
// the webhook and the event, so enqueueing the same event twice (two
// instances, or a sweep that runs again after a restart) is a no-op.
func (s *SQLiteStore) EnqueueWebhookDelivery(ctx context.Context, delivery domain.WebhookDelivery) (bool, error) {
	if delivery.ID == "" || len(delivery.ID) > 191 || delivery.WebhookID == "" || len(delivery.Payload) == 0 {
		return false, ErrIssueQuery
	}
	now := time.Now().UTC()
	if delivery.CreatedAt.IsZero() {
		delivery.CreatedAt = now
	}
	next := now
	if delivery.NextAttemptAt != nil {
		next = *delivery.NextAttemptAt
	}
	if delivery.MaxAttempts <= 0 {
		delivery.MaxAttempts = 1
	}
	if delivery.Status == "" {
		delivery.Status = domain.WebhookDeliveryPending
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO webhook_deliveries(`+webhookDeliveryColumns+`,payload) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`,
		delivery.ID, delivery.WorkspaceKey, delivery.WebhookID, delivery.SecretRef, delivery.Kind, delivery.EventID, truncateText(delivery.EventType, 64), truncateText(delivery.Action, 64), delivery.URL,
		delivery.Status, 0, delivery.MaxAttempts, millis(next), int64(0), 0, "", "", int64(0), []byte("[]"), delivery.RedeliveryOf,
		millis(delivery.CreatedAt), millis(now), int64(0), []byte(delivery.Payload))
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

// truncateText is storableText: external text (a receiver's response, a
// worker's error) cut to limit bytes on a character boundary, as valid UTF-8,
// so strict MySQL (error 1366) and Postgres accept it.
func truncateText(value string, limit int) string { return storableText(value, limit) }

func scanWebhookDelivery(scan func(...any) error, withPayload bool) (domain.WebhookDelivery, error) {
	var d domain.WebhookDelivery
	var next, lease, created, updated, completed int64
	var responseBody, errorText sql.NullString
	var attempts, payload []byte
	targets := []any{&d.ID, &d.WorkspaceKey, &d.WebhookID, &d.SecretRef, &d.Kind, &d.EventID, &d.EventType, &d.Action, &d.URL, &d.Status, &d.Attempt, &d.MaxAttempts, &next, &lease, &d.ResponseStatus, &responseBody, &errorText, &d.DurationMS, &attempts, &d.RedeliveryOf, &created, &updated, &completed}
	if withPayload {
		targets = append(targets, &payload)
	}
	if err := scan(targets...); err != nil {
		return d, err
	}
	d.ResponseBody, d.Error = responseBody.String, errorText.String
	if d.Status == domain.WebhookDeliveryPending || d.Status == domain.WebhookDeliveryRetrying {
		d.NextAttemptAt = fromMillis(next)
	}
	if created := fromMillis(created); created != nil {
		d.CreatedAt = *created
	}
	if updated := fromMillis(updated); updated != nil {
		d.UpdatedAt = *updated
	}
	d.CompletedAt = fromMillis(completed)
	d.Attempts = []domain.WebhookDeliveryAttempt{}
	if len(attempts) > 0 {
		_ = json.Unmarshal(attempts, &d.Attempts)
	}
	if withPayload {
		d.Payload = json.RawMessage(payload)
	}
	return d, nil
}

// DueWebhookDeliveries lists deliveries whose next attempt is due, including
// claims whose lease expired (an instance stopped mid-delivery).
func (s *SQLiteStore) DueWebhookDeliveries(ctx context.Context, now time.Time, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 50
	}
	at := millis(now)
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM webhook_deliveries WHERE (status IN ('pending','retrying') AND next_attempt_at<=? AND lease_until<=?) OR (status='delivering' AND lease_until<=?) ORDER BY next_attempt_at,id LIMIT ?`, at, at, at, limit)
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

// ClaimWebhookDelivery leases a due delivery to the caller. It reports false
// when another instance holds it or it is not due.
func (s *SQLiteStore) ClaimWebhookDelivery(ctx context.Context, id string, now time.Time, lease time.Duration) (domain.WebhookDelivery, bool, error) {
	at := millis(now)
	result, err := s.db.ExecContext(ctx, `UPDATE webhook_deliveries SET status='delivering',lease_until=?,updated_at=? WHERE id=? AND ((status IN ('pending','retrying') AND next_attempt_at<=? AND lease_until<=?) OR (status='delivering' AND lease_until<=?))`, millis(now.Add(lease)), at, id, at, at, at)
	if err != nil {
		return domain.WebhookDelivery{}, false, err
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return domain.WebhookDelivery{}, false, err
	}
	row := s.db.QueryRowContext(ctx, `SELECT `+webhookDeliveryColumns+`,payload FROM webhook_deliveries WHERE id=?`, id)
	delivery, err := scanWebhookDelivery(row.Scan, true)
	return delivery, err == nil, err
}

// WebhookDeliveryOutcome is the result of one attempt.
type WebhookDeliveryOutcome struct {
	Succeeded      bool
	Canceled       bool
	ResponseStatus int
	ResponseBody   string
	Error          string
	Duration       time.Duration
	At             time.Time
	// NextAttemptAt schedules a retry; nil makes a failure final.
	NextAttemptAt *time.Time
}

// CompleteWebhookDeliveryAttempt records an attempt made under a claim.
func (s *SQLiteStore) CompleteWebhookDeliveryAttempt(ctx context.Context, delivery domain.WebhookDelivery, outcome WebhookDeliveryOutcome) (domain.WebhookDelivery, error) {
	attempt := delivery.Attempt + 1
	if outcome.Canceled {
		attempt = delivery.Attempt
	}
	at := outcome.At
	if at.IsZero() {
		at = time.Now().UTC()
	}
	status := domain.WebhookDeliveryFailed
	next := int64(0)
	completed := millis(at)
	switch {
	case outcome.Canceled:
		status = domain.WebhookDeliveryCanceled
	case outcome.Succeeded:
		status = domain.WebhookDeliverySucceeded
	case outcome.NextAttemptAt != nil:
		status, next, completed = domain.WebhookDeliveryRetrying, millis(*outcome.NextAttemptAt), 0
	}
	attempts := delivery.Attempts
	if !outcome.Canceled {
		attempts = append(attempts, domain.WebhookDeliveryAttempt{Attempt: attempt, At: at, ResponseStatus: outcome.ResponseStatus, DurationMS: outcome.Duration.Milliseconds(), Error: truncateText(outcome.Error, 512), Succeeded: outcome.Succeeded})
	}
	if len(attempts) > 10 {
		attempts = attempts[len(attempts)-10:]
	}
	encoded, err := json.Marshal(attempts)
	if err != nil {
		return delivery, err
	}
	body := truncateText(outcome.ResponseBody, domain.WebhookDeliveryBodyLimit)
	errorText := truncateText(outcome.Error, 2048)
	result, err := s.db.ExecContext(ctx, `UPDATE webhook_deliveries SET status=?,attempt=?,next_attempt_at=?,lease_until=0,response_status=?,response_body=?,error_text=?,duration_ms=?,attempts=?,updated_at=?,completed_at=? WHERE id=? AND status='delivering'`,
		status, attempt, next, outcome.ResponseStatus, body, errorText, outcome.Duration.Milliseconds(), encoded, millis(at), completed, delivery.ID)
	if err != nil {
		return delivery, err
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		if err == nil {
			err = ErrIssueVersion
		}
		return delivery, err
	}
	delivery.Status, delivery.Attempt, delivery.ResponseStatus, delivery.ResponseBody, delivery.Error = status, attempt, outcome.ResponseStatus, body, errorText
	delivery.DurationMS, delivery.Attempts, delivery.UpdatedAt = outcome.Duration.Milliseconds(), attempts, at
	delivery.NextAttemptAt, delivery.CompletedAt = fromMillis(next), fromMillis(completed)
	return delivery, nil
}

// WebhookDeliveryQuery pages through one webhook's delivery log, newest first.
type WebhookDeliveryQuery struct {
	Workspace, WebhookID, Cursor, Status string
	Limit                                int
}

type WebhookDeliveryPage struct {
	Items      []domain.WebhookDelivery `json:"items"`
	HasMore    bool                     `json:"hasMore"`
	NextCursor string                   `json:"nextCursor,omitempty"`
}

func (s *SQLiteStore) WebhookDeliveries(ctx context.Context, q WebhookDeliveryQuery) (WebhookDeliveryPage, error) {
	page := WebhookDeliveryPage{Items: []domain.WebhookDelivery{}}
	limit := q.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	where, args := "workspace_key=? AND webhook_id=?", []any{q.Workspace, q.WebhookID}
	switch q.Status {
	case "":
	case "failed":
		where += " AND (status='failed' OR status='retrying')"
	default:
		where += " AND status=?"
		args = append(args, q.Status)
	}
	if q.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(q.Cursor)
		var created int64
		var id string
		if err != nil {
			return page, ErrIssueQuery
		}
		if _, err := fmt.Sscanf(string(raw), "%d:%s", &created, &id); err != nil || id == "" {
			return page, ErrIssueQuery
		}
		where += " AND (created_at<? OR (created_at=? AND id<?))"
		args = append(args, created, created, id)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+webhookDeliveryColumns+` FROM webhook_deliveries WHERE `+where+` ORDER BY created_at DESC,id DESC LIMIT ?`, append(args, limit+1)...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		delivery, err := scanWebhookDelivery(rows.Scan, false)
		if err != nil {
			return page, err
		}
		if len(page.Items) == limit {
			page.HasMore = true
			break
		}
		page.Items = append(page.Items, delivery)
	}
	if page.HasMore {
		last := page.Items[len(page.Items)-1]
		page.NextCursor = base64.RawURLEncoding.EncodeToString(fmt.Appendf(nil, "%d:%s", millis(last.CreatedAt), last.ID))
	}
	return page, rows.Err()
}

// WebhookDelivery returns one delivery with its request payload.
func (s *SQLiteStore) WebhookDelivery(ctx context.Context, workspace, id string) (domain.WebhookDelivery, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+webhookDeliveryColumns+`,payload FROM webhook_deliveries WHERE workspace_key=? AND id=?`, workspace, id)
	return scanWebhookDelivery(row.Scan, true)
}

// CancelWebhookDeliveries stops queued deliveries of a deleted or disabled
// webhook. Lifecycle notices (an application's revocation) are kept.
func (s *SQLiteStore) CancelWebhookDeliveries(ctx context.Context, workspace, webhookID string) error {
	now := millis(time.Now())
	_, err := s.db.ExecContext(ctx, `UPDATE webhook_deliveries SET status='canceled',updated_at=?,completed_at=? WHERE workspace_key=? AND webhook_id=? AND kind<>'lifecycle' AND status IN ('pending','retrying')`, now, now, workspace, webhookID)
	return err
}

// DeleteWebhookDeliveries removes a deleted webhook's log.
func (s *SQLiteStore) DeleteWebhookDeliveries(ctx context.Context, workspace, webhookID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM webhook_deliveries WHERE workspace_key=? AND webhook_id=? AND kind<>'lifecycle'`, workspace, webhookID)
	return err
}

// PruneWebhookDeliveries deletes finished deliveries created before cutoff.
func (s *SQLiteStore) PruneWebhookDeliveries(ctx context.Context, cutoff time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM webhook_deliveries WHERE created_at<? AND status IN ('succeeded','failed','canceled')`, millis(cutoff))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// RecentResourceActivities returns a resource's latest activity entries,
// newest first. Webhook payloads use them to find the comment or attachment
// an issue event touched.
func (s *SQLiteStore) RecentResourceActivities(ctx context.Context, workspace, resource string, limit int) ([]domain.ActivityEvent, error) {
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM workspace_content_records WHERE workspace_key=? AND kind='activity' AND resource_id=? ORDER BY created_at DESC,id DESC LIMIT ?`, workspace, resource, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.ActivityEvent{}
	for rows.Next() {
		var raw []byte
		var activity domain.ActivityEvent
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if json.Unmarshal(raw, &activity) == nil {
			result = append(result, activity)
		}
	}
	return result, rows.Err()
}
