package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"net/url"
	"time"

	"flow/api/internal/domain"
)

// Enterprise tenant authorization is its own permission domain, and it is
// granted on the chat platform itself: the tenant administrator approves and
// publishes a custom application's version in the admin console (and sets who
// may use it there). Until then the platform does not issue the application a
// tenant token. So Flow authorizes the tenant automatically once the
// configured credentials obtain a tenant_access_token and the bot's info is
// readable, and re-checks that on Test connection, periodically and after a
// token failure. Nobody signs in for it, and a Flow workspace role never
// stands in for it.

const (
	chatTenantNotApprovedMessage = "The app isn't approved or published in the tenant yet, or its credentials are wrong. Ask your chat platform admin to approve the app version in the admin console."
	chatPlatformUnreachable      = "Could not reach the chat platform; retry shortly"

	// An authorized tenant is re-checked every few hours; a tenant that is
	// not approved yet more often, so approval in the admin console shows up
	// without anyone pressing Test connection.
	chatTenantRecheckInterval        = 6 * time.Hour
	chatTenantPendingRecheckInterval = 10 * time.Minute
	// After a token failure the check runs at most this often.
	chatTenantFailureRecheckInterval = time.Minute
)

// chatTenantCheckError is a definitive answer of the platform: the
// application is not usable in the tenant. Transient failures (network,
// rate limits, 5xx) are not, and never change the authorization.
type chatTenantCheckError struct{ message string }

func (e chatTenantCheckError) Error() string { return e.message }

// chatTokenError marks a failure to obtain the tenant token, so a failed
// delivery can trigger a tenant re-check.
type chatTokenError struct{ err error }

func (e chatTokenError) Error() string { return e.err.Error() }
func (e chatTokenError) Unwrap() error { return e.err }

func chatTransient(err error) bool {
	var platform *chatPlatformError
	return errors.As(err, &platform) && platform.Retryable || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

// chatProbe is the automatic tenant check: the credentials obtain a tenant
// token and the bot's info is readable with it.
func chatProbe(ctx context.Context, record domain.ChatIntegrationRecord, credential chatCredentials) (string, string, error) {
	token, err := obtainChatTenantToken(ctx, record, credential)
	if err != nil {
		if chatTransient(err) {
			return "", "", err
		}
		return "", "", chatTenantCheckError{chatTenantNotApprovedMessage}
	}
	var response struct {
		Bot struct {
			OpenID string `json:"open_id"`
			Name   string `json:"app_name"`
		} `json:"bot"`
	}
	err = chatRequest(ctx, chatBaseURL(record.Provider)+"/open-apis/bot/v3/info/", http.MethodGet, token.Token, nil, &response)
	if err != nil {
		if chatTransient(err) {
			return "", "", err
		}
		return "", "", chatTenantCheckError{chatTenantNotApprovedMessage}
	}
	if response.Bot.OpenID == "" {
		return "", "", chatTenantCheckError{errChatBotUnpublished.Error()}
	}
	return response.Bot.OpenID, response.Bot.Name, nil
}

// chatCheckMessage is the message a failed check reports.
func chatCheckMessage(err error) string {
	var refusal chatTenantCheckError
	if errors.As(err, &refusal) {
		return refusal.message
	}
	if chatTransient(err) {
		return chatPlatformUnreachable
	}
	return "Chat application check failed; verify app credentials and bot publication"
}

// applyChatTenantCheck records the outcome of a check on record (the caller
// saves it). A success authorizes the tenant, recording the tenant key when
// known, the time and the workspace administrator who configured or tested
// the connection (configuredBy, empty for periodic checks). Connections
// authorized before the check was automatic become automatically authorized
// here. A definitive refusal withdraws the authorization; a transient failure
// only records the attempt. It reports whether the authorization changed.
func applyChatTenantCheck(record *domain.ChatIntegrationRecord, botID string, checkErr error, configuredBy string, now time.Time) bool {
	before := record.Connected
	check := &domain.ChatTenantCheck{CheckedAt: now}
	switch {
	case checkErr == nil:
		record.BotOpenID = botID
		authorization := domain.ChatTenantAuthorization{TenantKey: record.TenantKey, AuthorizedAt: now, ConfiguredBy: configuredBy}
		if record.Connected && record.TenantAuthorization != nil && !record.TenantAuthorization.AuthorizedAt.IsZero() {
			authorization.AuthorizedAt = record.TenantAuthorization.AuthorizedAt
		}
		if configuredBy == "" && record.TenantAuthorization != nil {
			authorization.ConfiguredBy = record.TenantAuthorization.ConfiguredBy
		}
		record.TenantAuthorization = &authorization
		record.Connected = true
	case chatTransient(checkErr):
		// The authorization stays as it was.
		check.ErrorCode = chatErrorCode(chatPlatformUnreachable)
	default:
		check.ErrorCode = chatErrorCode(chatCheckMessage(checkErr))
		record.Connected = false
	}
	record.TenantCheck = check
	return before != record.Connected
}

func chatConnectionStatus(record domain.ChatIntegrationRecord) string {
	if record.Connected {
		return "connected"
	}
	return "pending_authorization"
}

// adoptChatTenantKey records the application's tenant key the first time the
// platform names it. The tenant token, bot info and the scopes Flow asks for
// do not include it, so it comes from what the platform returns under the
// application's own credentials: the bot's internal groups (only groups of
// the application's tenant can be internal for a custom application's bot)
// or a member's linked account (only accounts of that tenant can authorize a
// custom application). Once recorded it never changes; every later group,
// account and callback must match it. The caller holds the chat
// configuration lock.
func (s *server) adoptChatTenantKey(ctx context.Context, record *domain.ChatIntegrationRecord, tenantKey string) error {
	if record.TenantKey != "" || tenantKey == "" {
		return nil
	}
	current, err := s.store.ChatIntegrationByID(ctx, record.Provider, record.ConnectionID)
	if err != nil {
		return err
	}
	if current.TenantKey != "" {
		*record = current
		return nil
	}
	current.TenantKey = tenantKey
	if current.TenantAuthorization != nil {
		current.TenantAuthorization.TenantKey = tenantKey
	}
	if err = s.store.PutChatIntegration(ctx, current); err != nil {
		return err
	}
	*record = current
	s.auditChatIntegrationContext(ctx, record.WorkspaceKey, "chat_tenant.key_recorded", "chat_integration", record.ConnectionID, map[string]any{"provider": record.Provider, "tenantKey": tenantKey})
	return nil
}

// chatInternalTenantKey reads the tenant key from the bot's internal groups;
// it is empty unless every internal group names the same tenant.
func chatInternalTenantKey(items []chatPlatformGroup) string {
	key := ""
	for _, item := range items {
		if item.External == nil || *item.External || item.TenantKey == "" {
			continue
		}
		if key != "" && key != item.TenantKey {
			return ""
		}
		key = item.TenantKey
	}
	return key
}

type chatPlatformGroup struct {
	ChatID    string `json:"chat_id"`
	Name      string `json:"name"`
	TenantKey string `json:"tenant_key"`
	External  *bool  `json:"external"`
}

type chatPlatformGroupPage struct {
	Data struct {
		Items     []chatPlatformGroup `json:"items"`
		PageToken string              `json:"page_token"`
		HasMore   bool                `json:"has_more"`
	} `json:"data"`
}

// learnChatTenantKey asks the platform for the bot's groups when the tenant
// key is still unknown. It never fails the caller: without an internal group
// the key is recorded later. The caller holds the chat configuration lock.
func (s *server) learnChatTenantKey(ctx context.Context, record *domain.ChatIntegrationRecord) {
	if record.TenantKey != "" || !record.Connected {
		return
	}
	var page chatPlatformGroupPage
	if err := s.chatPlatformRequest(ctx, *record, http.MethodGet, "/open-apis/im/v1/chats?"+url.Values{"page_size": {"50"}}.Encode(), nil, &page); err != nil {
		return
	}
	if err := s.adoptChatTenantKey(ctx, record, chatInternalTenantKey(page.Data.Items)); err != nil {
		log.Printf("Chat integration workspace=%s: could not record the tenant key: %v", record.WorkspaceKey, err)
	}
}

// runChatTenantCheck checks the stored application, saves the outcome and
// syncs the connection's display state. It is called under the chat
// configuration lock.
func (s *server) runChatTenantCheck(ctx context.Context, record domain.ChatIntegrationRecord, configuredBy string) (domain.ChatIntegrationRecord, error) {
	credential, err := s.chatCredentials(ctx, record)
	if err != nil {
		// Not the platform's answer: the authorization stays, and the
		// attempt is recorded so the periodic check waits its interval.
		record.TenantCheck = &domain.ChatTenantCheck{CheckedAt: time.Now().UTC(), ErrorCode: "chat_credentials_unreadable"}
		return record, errors.Join(err, s.store.PutChatIntegration(ctx, record))
	}
	botID, _, checkErr := chatProbe(ctx, record, credential)
	changed := applyChatTenantCheck(&record, botID, checkErr, configuredBy, time.Now().UTC())
	if err = s.store.PutChatIntegration(ctx, record); err != nil {
		return record, err
	}
	// A periodic check that could not reach the platform leaves the
	// connection's display state alone.
	if checkErr == nil || configuredBy != "" || !chatTransient(checkErr) {
		status, testStatus, lastError := chatConnectionStatus(record), "success", ""
		if checkErr != nil {
			status, testStatus, lastError = "error", "failed", chatCheckMessage(checkErr)
		}
		if err = s.syncChatConnection(ctx, record, configuredBy, status, testStatus, lastError); err != nil {
			return record, err
		}
	}
	if changed {
		s.auditChatTenant(ctx, record)
	}
	if checkErr == nil {
		s.learnChatTenantKey(ctx, &record)
	}
	return record, checkErr
}

func (s *server) auditChatTenant(ctx context.Context, record domain.ChatIntegrationRecord) {
	if record.Connected {
		metadata := map[string]any{"provider": record.Provider, "tenantKey": record.TenantKey, "method": "automatic"}
		if record.TenantAuthorization != nil && record.TenantAuthorization.ConfiguredBy != "" {
			metadata["configuredBy"] = record.TenantAuthorization.ConfiguredBy
		}
		s.auditChatIntegrationContext(ctx, record.WorkspaceKey, "chat_tenant.authorized", "chat_integration", record.ConnectionID, metadata)
		return
	}
	code := ""
	if record.TenantCheck != nil {
		code = record.TenantCheck.ErrorCode
	}
	s.auditChatIntegrationContext(ctx, record.WorkspaceKey, "chat_tenant.not_approved", "chat_integration", record.ConnectionID, map[string]any{"provider": record.Provider, "code": code})
}

// recheckChatTenant re-checks a workspace's application when due says so.
func (s *server) recheckChatTenant(ctx context.Context, workspace, provider string, due func(domain.ChatIntegrationRecord) bool) error {
	// Most ticks find nothing due; only then is the lock taken.
	record, err := s.store.ChatIntegration(ctx, workspace, provider)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil || !due(record) {
		return err
	}
	return s.withChatConfigurationLock(ctx, workspace, provider, func() error {
		record, err := s.store.ChatIntegration(ctx, workspace, provider)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil || !due(record) {
			return err
		}
		_, err = s.runChatTenantCheck(ctx, record, "")
		if err != nil && (chatTransient(err) || errors.As(err, new(chatTenantCheckError))) {
			return nil
		}
		return err
	})
}

func chatTenantCheckedAt(record domain.ChatIntegrationRecord) time.Time {
	if record.TenantCheck != nil {
		return record.TenantCheck.CheckedAt
	}
	return time.Time{}
}

// chatTenantRecheckDue: authorized tenants every few hours, others (not
// approved yet, or stored before checks were automatic) more often.
func chatTenantRecheckDue(now time.Time) func(domain.ChatIntegrationRecord) bool {
	return func(record domain.ChatIntegrationRecord) bool {
		interval := chatTenantPendingRecheckInterval
		if record.Connected && record.TenantCheck != nil {
			interval = chatTenantRecheckInterval
		}
		return now.Sub(chatTenantCheckedAt(record)) >= interval
	}
}

// recheckChatTenants is the periodic check, run by the chat worker.
func (s *server) recheckChatTenants(ctx context.Context, workspace string) error {
	var errs []error
	for _, provider := range []string{"feishu", "lark"} {
		errs = append(errs, s.recheckChatTenant(ctx, workspace, provider, chatTenantRecheckDue(time.Now())))
	}
	return errors.Join(errs...)
}

// recheckChatTenantAfterFailure runs the check soon after the platform
// refused the application's credentials, so the settings page says why.
func (s *server) recheckChatTenantAfterFailure(ctx context.Context, record domain.ChatIntegrationRecord, failure error) {
	var token chatTokenError
	if !errors.As(failure, &token) || chatTransient(failure) {
		return
	}
	now := time.Now()
	err := s.recheckChatTenant(ctx, record.WorkspaceKey, record.Provider, func(current domain.ChatIntegrationRecord) bool {
		return current.ConnectionID == record.ConnectionID && now.Sub(chatTenantCheckedAt(current)) >= chatTenantFailureRecheckInterval
	})
	if err != nil {
		log.Printf("Chat integration workspace=%s: tenant re-check: %v", record.WorkspaceKey, err)
	}
}

// chatTenantPermission describes the tenant domain for the settings page.
func chatTenantPermission(record domain.ChatIntegrationRecord, exists bool, connectedBy string) map[string]any {
	tenant := map[string]any{"status": "not_configured"}
	if !exists {
		return tenant
	}
	tenant["status"] = "pending"
	if record.TenantCheck != nil {
		tenant["checkedAt"] = record.TenantCheck.CheckedAt
		if record.TenantCheck.ErrorCode != "" {
			tenant["errorCode"] = record.TenantCheck.ErrorCode
		}
		// Only the platform's refusal means the app is not approved; an
		// unreachable platform or unreadable credentials leave it pending.
		if !record.Connected && chatPermissionDomains[record.TenantCheck.ErrorCode] == "tenant" {
			tenant["status"] = "not_approved"
		}
	}
	if chatTenantAuthorized(record) {
		tenant["status"] = "authorized"
		authorization := domain.ChatTenantAuthorization{TenantKey: record.TenantKey, AuthorizedAt: record.UpdatedAt, ConfiguredBy: connectedBy}
		if record.TenantAuthorization != nil {
			authorization.AuthorizedAt = firstNonZeroTime(record.TenantAuthorization.AuthorizedAt, authorization.AuthorizedAt)
			authorization.ConfiguredBy = firstNonEmpty(record.TenantAuthorization.ConfiguredBy, connectedBy)
		}
		if record.TenantKey != "" {
			tenant["tenantKey"] = record.TenantKey
		}
		tenant["authorization"] = authorization
	}
	return tenant
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}
