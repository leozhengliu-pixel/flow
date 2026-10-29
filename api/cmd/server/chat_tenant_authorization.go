package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"net/url"
	"slices"
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
	// The tenant key and name are read with the app permission 「获取企业信息」
	// (tenant:tenant:readonly), granted when the app version is approved.
	chatTenantInfoPermissionMessage  = "Grant the app the 「获取企业信息」 permission (tenant:tenant:readonly) and publish a new version so Flow can show the tenant"
	chatTenantInfoUnavailableMessage = "The chat platform did not return the app's tenant; Flow reads it again on the next check"
	chatTenantMismatchMessage        = "The chat platform reports a different tenant than the one this connection recorded; disconnect the application and connect it again"
	chatTenantMismatchCode           = "chat_tenant_mismatch"

	chatTenantKeyFromPlatform = "platform"
	chatTenantKeyObserved     = "observed"

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

// chatProbeResult is what a successful check learned from the platform.
type chatProbeResult struct {
	BotOpenID, BotName string
	// TenantKey and TenantName come from the tenant information; empty when
	// it was not readable (TenantInfoErr says why).
	TenantKey, TenantName string
	TenantInfoErr         error
}

// chatTenantInfoPermissionCodes are the platform's answers when the app may
// not read the tenant information: 99991672 is the generic "the app has not
// been granted a required scope"; 1184001 is the endpoint's own "no
// permission to get the tenant's information".
var chatTenantInfoPermissionCodes = []int{99991672, 1184001}

// readChatTenantInfo reads the tenant the app's tenant token belongs to:
// GET /open-apis/tenant/v2/tenant/query (app permission 「获取企业信息」,
// tenant:tenant:readonly), whose data.tenant carries tenant_key and name.
// Transient failures are returned as they are; the missing permission and
// other refusals as chatTenantCheckError with their own message.
func readChatTenantInfo(ctx context.Context, provider, token string) (string, string, error) {
	var response struct {
		Data struct {
			Tenant struct {
				Name      string `json:"name"`
				TenantKey string `json:"tenant_key"`
			} `json:"tenant"`
		} `json:"data"`
	}
	err := chatRequest(ctx, chatBaseURL(provider)+"/open-apis/tenant/v2/tenant/query", http.MethodGet, token, nil, &response)
	var platform *chatPlatformError
	switch {
	case err != nil && chatTransient(err):
		return "", "", err
	case errors.As(err, &platform) && slices.Contains(chatTenantInfoPermissionCodes, platform.Code):
		return "", "", chatTenantCheckError{chatTenantInfoPermissionMessage}
	case err != nil || response.Data.Tenant.TenantKey == "":
		return "", "", chatTenantCheckError{chatTenantInfoUnavailableMessage}
	}
	return response.Data.Tenant.TenantKey, response.Data.Tenant.Name, nil
}

// chatProbe is the automatic tenant check: the credentials obtain a tenant
// token and the bot's info is readable with it. It then reads the tenant's
// information with the same token; that never fails the check, except when
// it names another tenant than the one recorded (chat_tenant_mismatch).
func chatProbe(ctx context.Context, record domain.ChatIntegrationRecord, credential chatCredentials) (chatProbeResult, error) {
	result := chatProbeResult{}
	token, err := obtainChatTenantToken(ctx, record, credential)
	if err != nil {
		if chatTransient(err) {
			return result, err
		}
		return result, chatTenantCheckError{chatTenantNotApprovedMessage}
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
			return result, err
		}
		return result, chatTenantCheckError{chatTenantNotApprovedMessage}
	}
	if response.Bot.OpenID == "" {
		return result, chatTenantCheckError{errChatBotUnpublished.Error()}
	}
	result.BotOpenID, result.BotName = response.Bot.OpenID, response.Bot.Name
	result.TenantKey, result.TenantName, result.TenantInfoErr = readChatTenantInfo(ctx, record.Provider, token.Token)
	if result.TenantKey != "" && record.TenantKey != "" && result.TenantKey != record.TenantKey {
		// Never silently replaced: groups, accounts and callbacks were
		// checked against the recorded key.
		return result, chatTenantCheckError{chatTenantMismatchMessage}
	}
	return result, nil
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

// chatCheckStatus is the HTTP status of a failed check: a tenant mismatch is
// a conflict with the recorded connection, other failures are the platform's.
func chatCheckStatus(err error) int {
	if chatCheckMessage(err) == chatTenantMismatchMessage {
		return http.StatusConflict
	}
	return http.StatusBadGateway
}

// applyChatTenantCheck records the outcome of a check on record (the caller
// saves it). A success authorizes the tenant, recording the tenant key and
// name read from the platform (or why they could not be read), the time and
// the workspace administrator who configured or tested the connection
// (configuredBy, empty for periodic checks). Connections authorized before
// the check was automatic become automatically authorized here, and those
// without a tenant key get it. A definitive refusal, or a tenant other than
// the recorded one, withdraws the authorization; a transient failure only
// records the attempt. It reports whether the authorization changed.
func applyChatTenantCheck(record *domain.ChatIntegrationRecord, probe chatProbeResult, checkErr error, configuredBy string, now time.Time) bool {
	before := record.Connected
	previous := record.TenantCheck
	check := &domain.ChatTenantCheck{CheckedAt: now}
	switch {
	case checkErr == nil:
		record.BotOpenID = probe.BotOpenID
		switch {
		case probe.TenantKey != "":
			record.TenantKey, record.TenantName, record.TenantKeySource = probe.TenantKey, probe.TenantName, chatTenantKeyFromPlatform
		case chatTransient(probe.TenantInfoErr):
			// Unknown this time: keep what the last check found.
			if previous != nil {
				check.TenantInfoErrorCode = previous.TenantInfoErrorCode
			}
		case probe.TenantInfoErr != nil:
			check.TenantInfoErrorCode = chatErrorCode(chatCheckMessage(probe.TenantInfoErr))
		}
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
		if previous != nil {
			check.TenantInfoErrorCode = previous.TenantInfoErrorCode
		}
	default:
		check.ErrorCode = chatErrorCode(chatCheckMessage(checkErr))
		if check.ErrorCode == chatTenantMismatchCode {
			check.ReportedTenantKey = probe.TenantKey
		}
		record.Connected = false
	}
	record.TenantCheck = check
	return before != record.Connected
}

// chatTenantRefusal is the message for an action that needs the tenant
// authorization while it is not current.
func chatTenantRefusal(record domain.ChatIntegrationRecord) string {
	if record.TenantCheck != nil && record.TenantCheck.ErrorCode == chatTenantMismatchCode {
		return chatTenantMismatchMessage
	}
	return chatTenantNotApprovedMessage
}

// chatTenantKeyFallback: the tenant key may be taken from what the platform
// returns under the app's credentials (the bot's internal groups, a linked
// account) only while the app may not read the tenant information.
func chatTenantKeyFallback(record domain.ChatIntegrationRecord) bool {
	return record.TenantKey == "" && record.TenantCheck != nil && record.TenantCheck.TenantInfoErrorCode == chatErrorCode(chatTenantInfoPermissionMessage)
}

func chatConnectionStatus(record domain.ChatIntegrationRecord) string {
	if record.Connected {
		return "connected"
	}
	return "pending_authorization"
}

// adoptChatTenantKey is the fallback for an app that lacks the tenant
// information permission: it records the application's tenant key the first
// time the platform names it under the application's own credentials, in the
// bot's internal groups (only groups of the application's tenant can be
// internal for a custom application's bot) or a member's linked account
// (only accounts of that tenant can authorize a custom application). Once
// recorded it is never replaced; every later group, account and callback
// must match it, and a connection check that reads another tenant flags the
// mismatch. The caller holds the chat configuration lock.
func (s *server) adoptChatTenantKey(ctx context.Context, record *domain.ChatIntegrationRecord, tenantKey string) error {
	if !chatTenantKeyFallback(*record) || tenantKey == "" {
		return nil
	}
	current, err := s.store.ChatIntegrationByID(ctx, record.Provider, record.ConnectionID)
	if err != nil {
		return err
	}
	if !chatTenantKeyFallback(current) {
		*record = current
		return nil
	}
	current.TenantKey, current.TenantKeySource = tenantKey, chatTenantKeyObserved
	if current.TenantAuthorization != nil {
		current.TenantAuthorization.TenantKey = tenantKey
	}
	if err = s.store.PutChatIntegration(ctx, current); err != nil {
		return err
	}
	*record = current
	s.auditChatTenantKey(ctx, current)
	return nil
}

func (s *server) auditChatTenantKey(ctx context.Context, record domain.ChatIntegrationRecord) {
	s.auditChatIntegrationContext(ctx, record.WorkspaceKey, "chat_tenant.key_recorded", "chat_integration", record.ConnectionID, map[string]any{"provider": record.Provider, "tenantKey": record.TenantKey, "source": record.TenantKeySource})
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

// learnChatTenantKey records the tenant key when it is still unknown (the
// last connection check could not read the tenant information): it reads the
// tenant information again, and while the app lacks that permission falls
// back to the bot's groups. It never fails the caller: the key is otherwise
// recorded later. The caller holds the chat configuration lock.
func (s *server) learnChatTenantKey(ctx context.Context, record *domain.ChatIntegrationRecord) {
	if record.TenantKey != "" || !record.Connected {
		return
	}
	if !chatTenantKeyFallback(*record) {
		s.readChatTenantKey(ctx, record)
	}
	if !chatTenantKeyFallback(*record) {
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

// readChatTenantKey reads the tenant information outside a connection check
// and records the key and name, or why they are not readable, on the stored
// connection. The caller holds the chat configuration lock.
func (s *server) readChatTenantKey(ctx context.Context, record *domain.ChatIntegrationRecord) {
	token, err := s.chatTenantToken(ctx, *record)
	if err != nil {
		return
	}
	key, name, infoErr := readChatTenantInfo(ctx, record.Provider, token)
	if chatTransient(infoErr) {
		return
	}
	current, err := s.store.ChatIntegrationByID(ctx, record.Provider, record.ConnectionID)
	if err != nil {
		return
	}
	*record = current
	if current.TenantKey != "" || !current.Connected {
		return
	}
	check := domain.ChatTenantCheck{CheckedAt: time.Now().UTC()}
	if current.TenantCheck != nil {
		check = *current.TenantCheck
	}
	check.TenantInfoErrorCode = ""
	if infoErr != nil {
		check.TenantInfoErrorCode = chatErrorCode(chatCheckMessage(infoErr))
	} else {
		current.TenantKey, current.TenantName, current.TenantKeySource = key, name, chatTenantKeyFromPlatform
		if current.TenantAuthorization != nil {
			current.TenantAuthorization.TenantKey = key
		}
	}
	current.TenantCheck = &check
	if err = s.store.PutChatIntegration(ctx, current); err != nil {
		log.Printf("Chat integration workspace=%s: could not record the tenant key: %v", record.WorkspaceKey, err)
		return
	}
	*record = current
	if current.TenantKey != "" {
		s.auditChatTenantKey(ctx, current)
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
	probe, checkErr := chatProbe(ctx, record, credential)
	keyBefore := record.TenantKey
	changed := applyChatTenantCheck(&record, probe, checkErr, configuredBy, time.Now().UTC())
	if err = s.store.PutChatIntegration(ctx, record); err != nil {
		return record, err
	}
	if record.TenantKey != keyBefore {
		s.auditChatTenantKey(ctx, record)
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
	if code == chatTenantMismatchCode {
		s.auditChatIntegrationContext(ctx, record.WorkspaceKey, "chat_tenant.mismatch", "chat_integration", record.ConnectionID, map[string]any{"provider": record.Provider, "tenantKey": record.TenantKey, "reportedTenantKey": record.TenantCheck.ReportedTenantKey})
		return
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
// approved yet, stored before checks were automatic, or whose tenant
// information was not readable, so a newly granted permission shows up soon)
// more often.
func chatTenantRecheckDue(now time.Time) func(domain.ChatIntegrationRecord) bool {
	return func(record domain.ChatIntegrationRecord) bool {
		interval := chatTenantPendingRecheckInterval
		if record.Connected && record.TenantCheck != nil && record.TenantKey != "" && record.TenantCheck.TenantInfoErrorCode == "" {
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
		if !record.Connected && record.TenantCheck.ErrorCode == chatTenantMismatchCode {
			tenant["status"] = "mismatch"
			tenant["tenantKey"] = record.TenantKey
			tenant["reportedTenantKey"] = record.TenantCheck.ReportedTenantKey
		}
		if record.TenantCheck.TenantInfoErrorCode != "" {
			tenant["tenantInfoErrorCode"] = record.TenantCheck.TenantInfoErrorCode
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
			tenant["tenantKeySource"] = firstNonEmpty(record.TenantKeySource, chatTenantKeyObserved)
		}
		if record.TenantName != "" {
			tenant["tenantName"] = record.TenantName
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
