package store

import (
	"context"
	"strings"
)

// SecretSealReport counts what ResealSecrets found in the secret tables.
type SecretSealReport struct {
	// Resealed secrets were rewritten under the current key: plain-text
	// values from older releases and values a retired key sealed.
	Resealed int
	// Plaintext secrets are still stored as older releases wrote them
	// because no key is configured.
	Plaintext int
	// Unreadable secrets open with none of the configured keys (a key was
	// lost or dropped from FLOW_CONNECTOR_SECRET_PREVIOUS_KEYS too early).
	Unreadable int
}

func (r *SecretSealReport) add(other SecretSealReport) {
	r.Resealed += other.Resealed
	r.Plaintext += other.Plaintext
	r.Unreadable += other.Unreadable
}

// resealBatch bounds the rows read per query.
const resealBatch = 200

// ResealSecrets brings every stored secret under the current key: it seals
// webhook and audit stream signing secrets that older releases stored in
// plain text, and rewrites connector credentials, integration OAuth tokens
// and signing secrets that a key listed in FLOW_CONNECTOR_SECRET_PREVIOUS_KEYS
// sealed. Each rewrite is a compare-and-swap on the stored value, so
// replicas running it together, or a concurrent update, never lose a write.
// Without a key it changes nothing and only counts plain-text values. It is
// idempotent and runs at every start.
func (s *SQLiteStore) ResealSecrets(ctx context.Context) (SecretSealReport, error) {
	var report SecretSealReport
	keyed := ConnectorSecretsConfigured()
	if keyed {
		connectors, err := s.resealConnectorSecrets(ctx)
		if err != nil {
			return report, err
		}
		report.add(connectors)
	}
	for _, table := range []struct {
		name, key string
		aad       func(string) string
	}{
		{"webhook_signing_secrets", "ref", func(ref string) string { return ref }},
		{"audit_stream_secrets", "webhook_id", auditStreamSecretAAD},
	} {
		texts, err := s.resealTextSecrets(ctx, table.name, table.key, table.aad, keyed)
		if err != nil {
			return report, err
		}
		report.add(texts)
	}
	return report, nil
}

func (s *SQLiteStore) resealConnectorSecrets(ctx context.Context) (SecretSealReport, error) {
	var report SecretSealReport
	after := ""
	for {
		type row struct {
			id     string
			sealed []byte
		}
		rows, err := s.db.QueryContext(ctx, `SELECT id,ciphertext FROM connector_secrets WHERE id>? ORDER BY id LIMIT ?`, after, resealBatch)
		if err != nil {
			return report, err
		}
		batch := []row{}
		for rows.Next() {
			var item row
			if err := rows.Scan(&item.id, &item.sealed); err != nil {
				rows.Close()
				return report, err
			}
			batch = append(batch, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return report, err
		}
		for _, item := range batch {
			after = item.id
			plain, current, err := openSecret(item.sealed, []byte(item.id))
			if err != nil {
				report.Unreadable++
				continue
			}
			if current {
				continue
			}
			resealed, err := sealSecret(plain, []byte(item.id))
			if err != nil {
				return report, err
			}
			// A concurrent write already stored a value under the current key.
			result, err := s.db.ExecContext(ctx, `UPDATE connector_secrets SET ciphertext=? WHERE id=? AND ciphertext=?`, resealed, item.id, item.sealed)
			if err != nil {
				return report, err
			}
			if count, _ := result.RowsAffected(); count == 1 {
				report.Resealed++
			}
		}
		if len(batch) < resealBatch {
			return report, nil
		}
	}
}

func (s *SQLiteStore) resealTextSecrets(ctx context.Context, table, keyColumn string, aad func(string) string, keyed bool) (SecretSealReport, error) {
	var report SecretSealReport
	after := ""
	for {
		type row struct{ key, stored string }
		rows, err := s.db.QueryContext(ctx, `SELECT `+keyColumn+`,secret FROM `+table+` WHERE `+keyColumn+`>? ORDER BY `+keyColumn+` LIMIT ?`, after, resealBatch)
		if err != nil {
			return report, err
		}
		batch := []row{}
		for rows.Next() {
			var item row
			if err := rows.Scan(&item.key, &item.stored); err != nil {
				rows.Close()
				return report, err
			}
			batch = append(batch, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return report, err
		}
		for _, item := range batch {
			after = item.key
			if !keyed {
				if !strings.HasPrefix(item.stored, sealedTextPrefix) {
					report.Plaintext++
				}
				continue
			}
			plain, stale, err := openSecretText(item.stored, aad(item.key))
			if err != nil {
				report.Unreadable++
				continue
			}
			if !stale {
				continue
			}
			sealed, err := sealSecretText(plain, aad(item.key))
			if err != nil {
				return report, err
			}
			result, err := s.db.ExecContext(ctx, `UPDATE `+table+` SET secret=? WHERE `+keyColumn+`=? AND secret=?`, sealed, item.key, item.stored)
			if err != nil {
				return report, err
			}
			if count, _ := result.RowsAffected(); count == 1 {
				report.Resealed++
			}
		}
		if len(batch) < resealBatch {
			return report, nil
		}
	}
}
