package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// Audit log writes. Workspace mutations stage entries with appendAudit /
// appendAuditEntries (advanced_features.go); the store commits them with the
// mutation. Events that change no workspace data go through recordAudit (one
// workspace) or recordAccountAudit (every workspace an account belongs to).
// Both land in the append-only audit_events table (store/audit_events.go).
//
// Each request carries its audit details (store.AuditRequest): the client
// address as FLOW_AUDIT_LOG_IP keeps it, the browser and operating system
// (the label account sessions show, not the raw user agent) and the
// credential that authenticated it.

const (
	auditIPTruncated = "truncated"
	auditIPFull      = "full"
	auditIPOff       = "off"
)

// auditClientIP is the request's client address as the audit log keeps it:
// IPv4 addresses lose their last octet and IPv6 addresses keep their /48
// unless FLOW_AUDIT_LOG_IP=full; off keeps none.
func (s *server) auditClientIP(r *http.Request) string {
	if s.auditLogIP == auditIPOff {
		return ""
	}
	raw := authClientAddress(r)
	if s.auditLogIP == auditIPFull {
		return raw
	}
	return truncatedAuditIP(raw)
}

func truncatedAuditIP(raw string) string {
	addr, err := netip.ParseAddr(raw)
	if err != nil {
		return ""
	}
	addr = addr.Unmap()
	bits := 48
	if addr.Is4() {
		bits = 24
	}
	prefix, err := addr.Prefix(bits)
	if err != nil {
		return ""
	}
	return prefix.Addr().String()
}

// auditUserAgent is the browser and operating system a request reported, as
// account sessions label them ("Chrome on macOS"); API clients that send no
// browser user agent are recorded as such.
func auditUserAgent(r *http.Request) string {
	if r.UserAgent() == "" {
		return ""
	}
	device := domain.AccountSession{}
	decorateAccountSession(&device, r)
	return device.Name
}

// withAuditRequest attaches the request's audit details. user is the
// authenticated account (empty before sign-in) and apiKey the credential when
// a bearer token authenticated the request.
func (s *server) withAuditRequest(r *http.Request, user domain.User, apiKey *domain.APIKey) context.Context {
	request := store.AuditRequest{IP: s.auditClientIP(r), UserAgent: auditUserAgent(r), UserID: user.ID}
	switch {
	case user.ID == "":
	case apiKey == nil:
		request.ActorType, request.ActorID = store.AuditActorUser, user.ID
	case apiKey.OAuthClientID != "" || apiKey.AuthorizationID != "" || apiKey.GrantType != "" || apiKey.Actor == "app":
		request.ActorType, request.ActorID = store.AuditActorOAuthApp, firstNonEmpty(apiKey.OAuthClientID, apiKey.ID)
	default:
		request.ActorType, request.ActorID = store.AuditActorAPIKey, apiKey.ID
	}
	return store.WithAuditRequest(r.Context(), request)
}

// recordAudit writes entries (oldest first) to a workspace's audit log
// outside a workspace mutation. A failure is logged: what the entries
// describe already happened.
func (s *server) recordAudit(ctx context.Context, workspaceKey string, entries ...domain.AuditLogEntry) {
	if len(entries) == 0 || workspaceKey == "" {
		return
	}
	workspaceID, err := s.store.WorkspaceIDForKey(ctx, workspaceKey)
	if err == nil {
		err = s.store.AppendAuditEntries(ctx, workspaceID, entries...)
	}
	if err != nil {
		log.Printf("audit log workspace=%s: %v", workspaceKey, err)
	}
}

// newAuditEntry is an entry made by actor (empty: the system).
func newAuditEntry(actor domain.User, action, resourceType, resourceID string, metadata map[string]any) domain.AuditLogEntry {
	return domain.AuditLogEntry{ID: idgen.New("audit_"), Actor: auditActorUser(actor), Action: action, ResourceType: resourceType, ResourceID: resourceID, Metadata: metadata, CreatedAt: time.Now().UTC()}
}

// auditActorUser is the part of a user an entry keeps.
func auditActorUser(user domain.User) domain.User {
	return domain.User{ID: user.ID, Name: user.Name, DisplayName: user.DisplayName, Email: user.Email, AvatarURL: user.AvatarURL, App: user.App, BuiltinAgent: user.BuiltinAgent}
}

// recordAccountAudit writes an account security event (resource "account",
// the account's id) to the audit log of every workspace the account is an
// active member of; an account with no workspace, or a sign-in attempt for
// an email no account has, goes to the instance-level log. email names the
// account when user is not known (a failed sign-in); it is kept only in
// that case, so the attempt can be traced.
func (s *server) recordAccountAudit(r *http.Request, user domain.User, email, action, result string, metadata map[string]any) {
	entry := newAuditEntry(user, action, "account", user.ID, metadata)
	entry.Result = result
	if user.ID != "" {
		entry.ActorType, entry.ActorID = store.AuditActorUser, user.ID
	} else if email != "" {
		if entry.Metadata == nil {
			entry.Metadata = map[string]any{}
		}
		entry.Metadata["email"] = email
	}
	ctx := r.Context()
	if _, ok := store.AuditRequestFrom(ctx); !ok {
		ctx = s.withAuditRequest(r, user, nil)
	}
	if err := s.store.RecordAccountAudit(ctx, user.ID, email, entry); err != nil {
		log.Printf("account audit %s: %v", action, err)
	}
}

// recordAccountResult audits an account change that may have been refused
// (err): a wrong current password is recorded as a failure. Requests the
// store rejected as invalid input are not security events and are skipped.
func (s *server) recordAccountResult(r *http.Request, user domain.User, action string, err error) {
	if s.authDisabled || user.ID == "" {
		return
	}
	switch {
	case err == nil:
		s.recordAccountAudit(r, user, "", action, store.AuditResultSuccess, nil)
	case errors.Is(err, store.ErrAuthInvalid) || errors.Is(err, store.ErrAuthForbidden):
		s.recordAccountAudit(r, user, "", action, store.AuditResultFailure, map[string]any{"reason": signInFailureReason(err)})
	}
}

// recordOAuthRevocation audits the revocation of a third-party application's
// authorization (workspace holds the workspace's authorizations as read
// before the revocation).
func (s *server) recordOAuthRevocation(r *http.Request, workspace domain.Bootstrap, actor domain.User, authorizationID string) {
	metadata := map[string]any{}
	for _, item := range workspace.OAuthAuthorizations {
		if item.ID == authorizationID {
			metadata["clientId"], metadata["clientName"], metadata["userId"] = item.ClientID, item.ClientName, item.UserID
		}
	}
	s.recordAudit(r.Context(), workspace.Workspace.URLKey, newAuditEntry(actor, "revoked", "oauth_authorization", authorizationID, metadata))
}

// agentWriteAudit returns the hook that audits each write call an agent
// makes with Flow's tools (and connector tools that may write): action
// tool_write on the agent session, task or run (resourceType/resourceID),
// made by the agent for actor, with the tool, its target and how it ended.
// Calls that never ran (an approval the user declined) are not writes.
func (s *server) agentWriteAudit(ctx context.Context, workspaceKey string, actor domain.User, resourceType, resourceID string) func(agentWriteOutcome) {
	ctx = context.WithoutCancel(ctx)
	return func(outcome agentWriteOutcome) {
		if outcome.Status != agentWriteSucceeded && outcome.Status != agentWriteFailed {
			return
		}
		metadata := map[string]any{"tool": outcome.Tool, "status": outcome.Status}
		if outcome.Target != "" {
			metadata["target"] = outcome.Target
		}
		entry := newAuditEntry(actor, "tool_write", resourceType, resourceID, metadata)
		entry.ActorType, entry.ActorID = store.AuditActorAgent, resourceID
		if outcome.Status == agentWriteFailed {
			entry.Result = store.AuditResultFailure
			metadata["reason"] = agentClip(outcome.Reason, 200)
		}
		s.recordAudit(ctx, workspaceKey, entry)
	}
}

// signInMethod names how a sign-in request authenticated, from its route.
func signInMethod(r *http.Request) string {
	path := r.URL.Path
	switch {
	case path == "/api/auth/login":
		return "password"
	case path == "/api/auth/token-login":
		return "email_code"
	case strings.HasPrefix(path, "/api/auth/enterprise/"):
		return "enterprise_sso"
	case strings.HasPrefix(path, "/api/auth/saml/"):
		return "saml"
	case strings.HasPrefix(path, "/api/auth/") && strings.HasSuffix(path, "/callback"):
		return strings.TrimSuffix(strings.TrimPrefix(path, "/api/auth/"), "/callback")
	case strings.Contains(path, "passkey"):
		return "passkey"
	}
	return "other"
}

// recordSignInFailure audits a refused sign-in for email (the account it
// names, when one has it, or the instance log).
func (s *server) recordSignInFailure(r *http.Request, email, reason string) {
	s.recordAccountAudit(r, domain.User{}, strings.ToLower(strings.TrimSpace(email)), "sign_in_failed", store.AuditResultFailure, map[string]any{"method": signInMethod(r), "reason": reason})
}

// signInFailureReason classifies a store sign-in error without its text.
func signInFailureReason(err error) string {
	switch {
	case errors.Is(err, store.ErrAuthInvalid):
		return "invalid_credentials"
	case strings.Contains(err.Error(), "not verified"):
		return "email_not_verified"
	}
	return "refused"
}

// pruneAuditLog removes audit entries older than the retention, a batch at
// a time, in every workspace and the instance log.
func (s *server) pruneAuditLog(ctx context.Context, now time.Time) {
	if s.auditRetention <= 0 {
		return
	}
	cutoff := now.Add(-s.auditRetention)
	workspaces, err := s.store.AuditWorkspaces(ctx)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("Audit log pruning: %v", err)
		}
		return
	}
	total := 0
	for _, workspaceID := range workspaces {
		for {
			deleted, err := s.store.PruneAuditEvents(ctx, workspaceID, cutoff)
			total += deleted
			if err != nil {
				if ctx.Err() == nil {
					log.Printf("Audit log pruning workspace=%s: %v", workspaceID, err)
				}
				break
			}
			if deleted < store.AuditPruneBatch || !maintenanceSleep(ctx) {
				break
			}
		}
	}
	if total > 0 {
		log.Printf("Pruned %d audit log entries older than %s", total, cutoff.Format(time.DateOnly))
	}
}

// runAuditVerifyCommand is `server audit-verify [--config file]`: it opens
// the configured database, verifies every audit log hash chain (each
// workspace's and the instance log) and prints one JSON line per chain. The
// exit status is 1 when any chain does not verify, 2 when it cannot run.
func runAuditVerifyCommand(args []string) int {
	if _, err := appconfig.LoadFileFromArgs(args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	configuration, err := appconfig.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	repository, err := store.OpenDatabase(configuration.Database)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	defer repository.Close()
	ctx := context.Background()
	workspaces, err := repository.AuditWorkspaces(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	status := 0
	encoder := json.NewEncoder(os.Stdout)
	for _, workspaceID := range workspaces {
		report, err := repository.VerifyAuditChain(ctx, workspaceID)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		if !report.Valid {
			status = 1
		}
		_ = encoder.Encode(struct {
			WorkspaceID string `json:"workspaceId"`
			store.AuditChainReport
		}{workspaceID, report})
	}
	return status
}

// GET /api/workspace/audit-log/verify (admins) recomputes the workspace's
// audit log hash chain.
func (s *server) verifyAuditLog(w http.ResponseWriter, r *http.Request) {
	workspaceID, err := s.store.WorkspaceIDForKey(r.Context(), workspaceKey(r))
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	report, err := s.store.VerifyAuditChain(r.Context(), workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not verify the audit log")
		return
	}
	writeJSON(w, http.StatusOK, report)
}
