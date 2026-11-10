package main

import (
	"context"
	"log"
	"net/http"

	"flow/api/internal/store"
)

// Secrets Flow must use in plain form (integration OAuth tokens, webhook,
// application webhook and audit stream signing secrets, developer tokens)
// are sealed with FLOW_CONNECTOR_SECRET_KEY. Without a key they cannot be
// saved: the request fails with 503 instead of storing them in plain text.

// signingSecretKeyMissing is the error for a signing secret that cannot be
// saved because no key is configured.
const signingSecretKeyMissing = "Configure FLOW_CONNECTOR_SECRET_KEY before creating signing secrets"

// requireSigningSecretStorage writes the 503 and reports false when signing
// secrets cannot be stored. Handlers call it before changing anything.
func requireSigningSecretStorage(w http.ResponseWriter) bool {
	if store.ConnectorSecretsConfigured() {
		return true
	}
	writeError(w, http.StatusServiceUnavailable, signingSecretKeyMissing)
	return false
}

// resealStoredSecrets runs store.ResealSecrets once at startup, in the
// background: it seals signing secrets older releases kept in plain text and
// moves secrets sealed with a retired key (FLOW_CONNECTOR_SECRET_PREVIOUS_KEYS)
// to the current one. Only counts are logged.
func (s *server) resealStoredSecrets(ctx context.Context) {
	report, err := s.store.ResealSecrets(ctx)
	if err != nil {
		log.Printf("secret storage: resealing stored secrets failed: %v", err)
		return
	}
	logSecretSealReport(report, store.ConnectorSecretsConfigured())
}

func logSecretSealReport(report store.SecretSealReport, keyed bool) {
	if report.Resealed > 0 {
		log.Printf("secret storage: sealed %d stored secrets under the current FLOW_CONNECTOR_SECRET_KEY", report.Resealed)
	}
	if !keyed {
		log.Printf("WARNING: FLOW_CONNECTOR_SECRET_KEY is not set: integration OAuth, webhook signing and audit stream secrets cannot be saved")
	}
	if report.Plaintext > 0 {
		log.Printf("WARNING: %d webhook or audit stream signing secrets from an earlier release are still stored in plain text; set FLOW_CONNECTOR_SECRET_KEY and restart to encrypt them", report.Plaintext)
	}
	if report.Unreadable > 0 {
		log.Printf("WARNING: %d stored secrets cannot be decrypted with FLOW_CONNECTOR_SECRET_KEY or FLOW_CONNECTOR_SECRET_PREVIOUS_KEYS; add the key that sealed them to FLOW_CONNECTOR_SECRET_PREVIOUS_KEYS, or reconnect the affected integrations", report.Unreadable)
	}
}
