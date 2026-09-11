package domain

// Credential issuance and reuse are auditable operations, but do not invalidate
// a browser's workspace data. Real consent, revocation and policy changes do.
func RoutineCredentialEvent(eventType string) bool {
	return eventType == "oauth_token.created" || eventType == "api_key.used" || eventType == "oauth_authorization.reused"
}
