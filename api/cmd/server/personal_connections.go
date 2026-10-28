package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"flow/api/internal/codehost"
	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// Members' personal Google Calendar and Notion connections (Settings ›
// Account › Connected accounts; docs/integrations.md "Personal connections").
//
//   - Google Calendar: the member authorizes Flow (OAuth, PKCE, offline
//     access) to read their calendar's out-of-office events. Flow reads only
//     those events, on a schedule bounded per member, and shows the member as
//     out of office (profile card "Availability") while one is active. Scopes:
//     openid, email and calendar.events.readonly.
//   - Notion: Notion's link-preview connection lives on Notion's side; the row
//     sends the member to it (FLOW_NOTION_CONNECTION_URL) and records that
//     they allow previews. GET /api/notion/link-preview then answers Notion's
//     requests for the member's issue and project links, as the member.
//
// Tokens live only in the encrypted connector secret store, belong to one
// member, and never reach the browser.

const (
	googleCalendarCallbackPath = "/api/account/connections/google-calendar/callback"
	googleCalendarReadScope    = "https://www.googleapis.com/auth/calendar.events.readonly"
	googleCalendarWindow       = 14 * 24 * time.Hour
	googleSyncBatch            = 25
	googleSyncMaxBackoff       = 6 * time.Hour
	personalConnectionTick     = time.Minute
)

var googleCalendarScopes = []string{"openid", "email", googleCalendarReadScope}

func googleCalendarCallbackURL() string { return appBaseURL() + googleCalendarCallbackPath }

// googleEndpointsOverride replaces Google's endpoints (tests).
var googleEndpointsOverride *googleEndpoints

type googleEndpoints struct {
	AuthorizeURL string
	TokenURL     string
	RevokeURL    string
	UserinfoURL  string
	CalendarAPI  string
}

func defaultGoogleEndpoints() googleEndpoints {
	return googleEndpoints{
		AuthorizeURL: "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL:     "https://oauth2.googleapis.com/token",
		RevokeURL:    "https://oauth2.googleapis.com/revoke",
		UserinfoURL:  "https://openidconnect.googleapis.com/v1/userinfo",
		CalendarAPI:  "https://www.googleapis.com/calendar/v3",
	}
}

// googleEndpointsFor is Google's endpoints; in local development
// FLOW_DEV_GOOGLE_BASE_URL (a loopback fake) replaces them.
func (s *server) googleEndpointsFor() googleEndpoints {
	if googleEndpointsOverride != nil {
		return *googleEndpointsOverride
	}
	if base := strings.TrimRight(strings.TrimSpace(os.Getenv("FLOW_DEV_GOOGLE_BASE_URL")), "/"); base != "" && s.loopbackCodeHosts() && safeLocalDevelopmentURL(base) {
		return googleEndpoints{AuthorizeURL: base + "/authorize", TokenURL: base + "/token", RevokeURL: base + "/revoke", UserinfoURL: base + "/userinfo", CalendarAPI: base + "/calendar/v3"}
	}
	return defaultGoogleEndpoints()
}

type googleOAuthConfig struct {
	ClientID     string
	ClientSecret string
}

// googleOAuth is the deployment's Google OAuth client (FLOW_GOOGLE_OAUTH_CLIENT_ID
// and FLOW_GOOGLE_OAUTH_CLIENT_SECRET); false while unconfigured.
func googleOAuth() (googleOAuthConfig, bool) {
	config := googleOAuthConfig{ClientID: strings.TrimSpace(os.Getenv("FLOW_GOOGLE_OAUTH_CLIENT_ID")), ClientSecret: strings.TrimSpace(os.Getenv("FLOW_GOOGLE_OAUTH_CLIENT_SECRET"))}
	return config, config.ClientID != "" && config.ClientSecret != ""
}

// googleSyncInterval is how often each member's calendar is read
// (FLOW_GOOGLE_CALENDAR_SYNC_INTERVAL, 5 minutes to 24 hours, default 15 minutes).
func googleSyncInterval() time.Duration {
	interval := 15 * time.Minute
	if parsed, err := time.ParseDuration(strings.TrimSpace(os.Getenv("FLOW_GOOGLE_CALENDAR_SYNC_INTERVAL"))); err == nil {
		interval = min(max(parsed, 5*time.Minute), 24*time.Hour)
	}
	return interval
}

// notionConnectionURL is the Notion page members are sent to
// (FLOW_NOTION_CONNECTION_URL, HTTPS); "" while unconfigured or invalid.
func notionConnectionURL() string {
	raw := strings.TrimSpace(os.Getenv("FLOW_NOTION_CONNECTION_URL"))
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "https" && !(parsed.Scheme == "http" && safeLocalDevelopmentURL(raw))) {
		return ""
	}
	return parsed.String()
}

func (s *server) registerPersonalConnectionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/account/connections", s.listPersonalConnections)
	mux.HandleFunc("POST /api/account/connections/google-calendar/authorize", s.startGoogleCalendarLink)
	mux.HandleFunc("GET /api/account/connections/google-calendar/callback", s.finishGoogleCalendarLink)
	mux.HandleFunc("DELETE /api/account/connections/google-calendar", s.disconnectGoogleCalendar)
	mux.HandleFunc("POST /api/account/connections/notion/connect", s.connectNotion)
	mux.HandleFunc("DELETE /api/account/connections/notion", s.disconnectNotion)
	mux.HandleFunc("GET /api/notion/link-preview", s.getLinkPreview)
}

type personalConnectionOption struct {
	// Available: the deployment is configured for the connection.
	Available  bool                       `json:"available"`
	Connection *domain.PersonalConnection `json:"connection,omitempty"`
}

type personalConnectionsResponse struct {
	GoogleCalendar personalConnectionOption `json:"googleCalendar"`
	Notion         personalConnectionOption `json:"notion"`
}

// listPersonalConnections — GET /api/account/connections.
func (s *server) listPersonalConnections(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.codeHostAccountUser(w, r)
	if !ok {
		return
	}
	connections, err := s.store.ListPersonalConnections(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read connected accounts")
		return
	}
	_, googleConfigured := googleOAuth()
	response := personalConnectionsResponse{
		GoogleCalendar: personalConnectionOption{Available: googleConfigured && store.ConnectorSecretsConfigured()},
		Notion:         personalConnectionOption{Available: notionConnectionURL() != ""},
	}
	for index := range connections {
		switch connections[index].Provider {
		case domain.PersonalConnectionGoogleCalendar:
			response.GoogleCalendar.Connection = &connections[index]
		case domain.PersonalConnectionNotion:
			response.Notion.Connection = &connections[index]
		}
	}
	writeJSON(w, http.StatusOK, response)
}

// ---------------------------------------------------------------- outbound

// googleClient reaches a Google endpoint: public HTTPS through the hardened
// dialer, loopback only in local development.
func (s *server) googleClient(ctx context.Context, endpoint string) (*http.Client, error) {
	noRedirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if s.loopbackCodeHosts() && safeLocalDevelopmentURL(endpoint) {
		return &http.Client{Timeout: 15 * time.Second, CheckRedirect: noRedirect}, nil
	}
	if err := outboundEndpointError(ctx, endpoint, outboundPublicOnly); err != nil {
		return nil, errors.New("the Google endpoint is not allowed: " + err.Error())
	}
	client := secureOutboundClient(15*time.Second, outboundPublicOnly)
	client.CheckRedirect = noRedirect
	return client, nil
}

func (s *server) googleJSON(ctx context.Context, method, endpoint string, header http.Header, payload, out any) (int, error) {
	client, err := s.googleClient(ctx, endpoint)
	if err != nil {
		return 0, err
	}
	return doCodeHostJSON(ctx, client, method, endpoint, header, payload, out)
}

// ---------------------------------------------------------------- tokens

func (s *server) writePersonalConnectionTokens(ctx context.Context, id string, tokens codeHostAccountTokens) error {
	raw, err := json.Marshal(tokens)
	if err != nil {
		return err
	}
	return s.store.PutConnectorSecret(ctx, store.PersonalConnectionSecretID(id), raw, time.Now().AddDate(5, 0, 0))
}

func (s *server) readPersonalConnectionTokens(ctx context.Context, id string) (codeHostAccountTokens, error) {
	raw, err := s.store.ReadConnectorSecret(ctx, store.PersonalConnectionSecretID(id), false)
	if err != nil {
		return codeHostAccountTokens{}, codehost.ErrUserNotLinked
	}
	var tokens codeHostAccountTokens
	if json.Unmarshal(raw, &tokens) != nil || tokens.AccessToken == "" {
		return codeHostAccountTokens{}, codehost.ErrUserNotLinked
	}
	return tokens, nil
}

type googleTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
}

func (r googleTokenResponse) tokens(previousRefresh string) codeHostAccountTokens {
	tokens := codeHostAccountTokens{AccessToken: r.AccessToken, RefreshToken: firstNonEmpty(r.RefreshToken, previousRefresh)}
	if r.ExpiresIn > 0 {
		tokens.ExpiresAt = time.Now().UTC().Add(time.Duration(r.ExpiresIn) * time.Second)
	}
	return tokens
}

func (s *server) markPersonalConnectionReconnect(ctx context.Context, connection domain.PersonalConnection, reason string) {
	if err := s.store.SetPersonalConnectionStatus(context.WithoutCancel(ctx), connection.ID, domain.PersonalConnectionReconnect, reason, connection.TokenExpiresAt); err != nil {
		log.Printf("personal connection %s: mark reconnect: %v", connection.ID, err)
	}
}

// googleCalendarTokens returns the connection's access token, refreshed when it
// expires soon. Google keeps the refresh token across refreshes, but a lease on
// the row still keeps replicas from refreshing at once. A revoked grant marks
// the connection "reconnect" and returns codehost.ErrUserNotLinked.
func (s *server) googleCalendarTokens(ctx context.Context, connection domain.PersonalConnection) (codeHostAccountTokens, error) {
	if connection.Status != domain.PersonalConnectionActive {
		return codeHostAccountTokens{}, codehost.ErrUserNotLinked
	}
	tokens, err := s.readPersonalConnectionTokens(ctx, connection.ID)
	if err != nil {
		return tokens, err
	}
	if codeHostTokensFresh(tokens) {
		return tokens, nil
	}
	if tokens.RefreshToken == "" {
		s.markPersonalConnectionReconnect(ctx, connection, "The token expired. Reconnect Google Calendar.")
		return codeHostAccountTokens{}, codehost.ErrUserNotLinked
	}
	config, ok := googleOAuth()
	if !ok {
		return codeHostAccountTokens{}, fmt.Errorf("%w: Google sign-in is not configured", codehost.ErrTransient)
	}
	owner, err := codeHostRandomToken(18)
	if err != nil {
		return codeHostAccountTokens{}, err
	}
	deadline := time.Now().Add(codeHostRefreshWait)
	for {
		claimed, err := s.store.ClaimPersonalConnectionRefresh(ctx, connection.ID, owner, codeHostRefreshLease)
		if err != nil {
			return codeHostAccountTokens{}, fmt.Errorf("%w: refresh lease: %v", codehost.ErrTransient, err)
		}
		if claimed {
			break
		}
		if !time.Now().Before(deadline) {
			return codeHostAccountTokens{}, fmt.Errorf("%w: the token refresh is still in progress", codehost.ErrTransient)
		}
		select {
		case <-ctx.Done():
			return codeHostAccountTokens{}, ctx.Err()
		case <-time.After(codeHostRefreshPollEvery):
		}
		if current, err := s.readPersonalConnectionTokens(ctx, connection.ID); err != nil {
			return codeHostAccountTokens{}, err
		} else if codeHostTokensFresh(current) {
			return current, nil
		}
	}
	defer func() {
		if err := s.store.ReleasePersonalConnectionRefresh(context.WithoutCancel(ctx), connection.ID, owner); err != nil {
			log.Printf("personal connection %s: release refresh lease: %v", connection.ID, err)
		}
	}()
	if tokens, err = s.readPersonalConnectionTokens(ctx, connection.ID); err != nil {
		return tokens, err
	}
	if codeHostTokensFresh(tokens) {
		return tokens, nil
	}
	var out googleTokenResponse
	values := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tokens.RefreshToken}, "client_id": {config.ClientID}, "client_secret": {config.ClientSecret}}
	if _, err := s.googleJSON(ctx, http.MethodPost, s.googleEndpointsFor().TokenURL, nil, values, &out); err != nil {
		if status := codeHostStatus(err); status == http.StatusBadRequest || status == http.StatusUnauthorized {
			s.markPersonalConnectionReconnect(ctx, connection, "Google revoked the authorization or it expired. Reconnect Google Calendar.")
			return codeHostAccountTokens{}, codehost.ErrUserNotLinked
		}
		return codeHostAccountTokens{}, fmt.Errorf("%w: refresh: %v", codehost.ErrTransient, err)
	}
	if out.AccessToken == "" {
		s.markPersonalConnectionReconnect(ctx, connection, "Google revoked the authorization or it expired. Reconnect Google Calendar.")
		return codeHostAccountTokens{}, codehost.ErrUserNotLinked
	}
	refreshed := out.tokens(tokens.RefreshToken)
	if err := s.writePersonalConnectionTokens(ctx, connection.ID, refreshed); err != nil {
		return codeHostAccountTokens{}, err
	}
	expires := refreshed.ExpiresAt
	_ = s.store.SetPersonalConnectionStatus(ctx, connection.ID, domain.PersonalConnectionActive, "", &expires)
	return refreshed, nil
}

// ---------------------------------------------------------------- Google link

// startGoogleCalendarLink — POST /api/account/connections/google-calendar/authorize.
func (s *server) startGoogleCalendarLink(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.codeHostAccountUser(w, r)
	if !ok {
		return
	}
	config, ok := googleOAuth()
	if !ok {
		writeError(w, http.StatusConflict, "Google Calendar is not configured. An operator must set FLOW_GOOGLE_OAUTH_CLIENT_ID and FLOW_GOOGLE_OAUTH_CLIENT_SECRET.")
		return
	}
	if !store.ConnectorSecretsConfigured() {
		writeError(w, http.StatusServiceUnavailable, "Configure FLOW_CONNECTOR_SECRET_KEY before connecting accounts")
		return
	}
	state, challenge, err := s.newCodeHostLinkState(r.Context(), codeHostLinkState{UserID: userID, Workspace: workspaceKey(r), CodeHost: domain.PersonalConnectionGoogleCalendar, Method: domain.CodeHostAuthOAuth})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start Google authorization")
		return
	}
	values := url.Values{"client_id": {config.ClientID}, "redirect_uri": {googleCalendarCallbackURL()}, "response_type": {"code"}, "scope": {strings.Join(googleCalendarScopes, " ")},
		"state": {state}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}, "access_type": {"offline"}, "prompt": {"consent"}, "include_granted_scopes": {"false"}}
	writeJSON(w, http.StatusOK, map[string]string{"url": s.googleEndpointsFor().AuthorizeURL + "?" + values.Encode()})
}

func (s *server) personalConnectionResult(w http.ResponseWriter, r *http.Request, state codeHostLinkState, status, message string) {
	if !wantsHTMLRedirect(r) {
		code := http.StatusOK
		if status == "error" {
			code = http.StatusBadRequest
		}
		writeJSON(w, code, map[string]string{"status": status, "error": message})
		return
	}
	target := appBaseURL() + "/"
	if state.Workspace != "" {
		target += url.PathEscape(state.Workspace) + "/settings/account/connections"
	}
	values := url.Values{"personalConnection": {status}}
	if message != "" {
		values.Set("error", message)
	}
	http.Redirect(w, r, target+"?"+values.Encode(), http.StatusFound)
}

// finishGoogleCalendarLink — GET /api/account/connections/google-calendar/callback:
// consumes the one-time state, checks it belongs to the signed-in member,
// exchanges the code with the PKCE verifier, and stores the connection.
func (s *server) finishGoogleCalendarLink(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	state, err := s.consumeCodeHostLinkState(r.Context(), query.Get("state"))
	if err != nil {
		s.personalConnectionResult(w, r, codeHostLinkState{}, "error", err.Error())
		return
	}
	user := authUser(r)
	if user.ID == "" || user.ID != state.UserID {
		s.personalConnectionResult(w, r, state, "error", "This connection was started by another user")
		return
	}
	if state.CodeHost != domain.PersonalConnectionGoogleCalendar {
		s.personalConnectionResult(w, r, state, "error", "The link request is invalid. Start again from Flow.")
		return
	}
	if denied := query.Get("error"); denied != "" {
		s.personalConnectionResult(w, r, state, "error", "Authorization was not granted")
		return
	}
	code := strings.TrimSpace(query.Get("code"))
	if code == "" || len(code) > 2048 {
		s.personalConnectionResult(w, r, state, "error", "Google did not return an authorization code")
		return
	}
	connection, tokens, err := s.exchangeGoogleCalendarCode(r.Context(), state, code)
	if err != nil {
		log.Printf("google calendar link user=%s: %v", state.UserID, err)
		message := "Connecting Google Calendar failed. Try again."
		var scopeErr *googleScopeError
		if errors.As(err, &scopeErr) {
			message = scopeErr.Error()
		}
		s.personalConnectionResult(w, r, state, "error", message)
		return
	}
	connection.UserID = state.UserID
	connection.ID = newPersonalConnectionID()
	saved, err := s.store.UpsertPersonalConnection(r.Context(), connection)
	if err == nil {
		if err = s.writePersonalConnectionTokens(r.Context(), saved.ID, tokens); err != nil {
			_, _ = s.store.DeletePersonalConnection(context.WithoutCancel(r.Context()), saved.UserID, saved.ID)
		}
	}
	if err != nil {
		s.personalConnectionResult(w, r, state, "error", "Could not save the connection")
		return
	}
	s.auditPersonalConnection(r.Context(), r, state.Workspace, "personal_connection.linked", saved)
	// Read the calendar once now so the member's state is right straight away.
	syncCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 20*time.Second)
	s.syncPersonalConnectionNow(syncCtx, saved, time.Now())
	cancel()
	s.personalConnectionResult(w, r, state, "linked", "")
}

func newPersonalConnectionID() string {
	token, err := codeHostRandomToken(12)
	if err != nil {
		return idgen.New("pcn_")
	}
	return "pcn_" + token
}

// googleScopeError is a refusal the member can fix (they unticked a permission).
type googleScopeError struct{ message string }

func (e *googleScopeError) Error() string { return e.message }

func (s *server) exchangeGoogleCalendarCode(ctx context.Context, state codeHostLinkState, code string) (domain.PersonalConnection, codeHostAccountTokens, error) {
	config, ok := googleOAuth()
	if !ok {
		return domain.PersonalConnection{}, codeHostAccountTokens{}, errors.New("google calendar is no longer configured")
	}
	endpoints := s.googleEndpointsFor()
	var token googleTokenResponse
	values := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {googleCalendarCallbackURL()}, "client_id": {config.ClientID}, "client_secret": {config.ClientSecret}, "code_verifier": {state.Verifier}}
	if _, err := s.googleJSON(ctx, http.MethodPost, endpoints.TokenURL, nil, values, &token); err != nil {
		return domain.PersonalConnection{}, codeHostAccountTokens{}, err
	}
	if token.AccessToken == "" {
		return domain.PersonalConnection{}, codeHostAccountTokens{}, errors.New("google: no access token")
	}
	granted := strings.Fields(token.Scope)
	if !slices.Contains(granted, googleCalendarReadScope) {
		s.revokeGoogleToken(ctx, token.RefreshToken, token.AccessToken)
		return domain.PersonalConnection{}, codeHostAccountTokens{}, &googleScopeError{"Flow needs permission to see your calendar's out-of-office events. Connect again and keep the calendar permission ticked."}
	}
	if token.RefreshToken == "" {
		s.revokeGoogleToken(ctx, "", token.AccessToken)
		return domain.PersonalConnection{}, codeHostAccountTokens{}, errors.New("google: no refresh token (offline access was not granted)")
	}
	tokens := token.tokens("")
	var info struct {
		Email string `json:"email"`
		Sub   string `json:"sub"`
	}
	header := http.Header{"Authorization": {"Bearer " + tokens.AccessToken}}
	if _, err := s.googleJSON(ctx, http.MethodGet, endpoints.UserinfoURL, header, nil, &info); err != nil || (info.Email == "" && info.Sub == "") {
		s.revokeGoogleToken(ctx, token.RefreshToken, token.AccessToken)
		return domain.PersonalConnection{}, codeHostAccountTokens{}, errors.New("google: could not read the authorized account")
	}
	scopes := make([]string, 0, len(granted))
	for _, scope := range granted {
		if slices.Contains(googleCalendarScopes, scope) || strings.HasSuffix(scope, "/userinfo.email") {
			scopes = append(scopes, scope)
		}
	}
	connection := domain.PersonalConnection{Provider: domain.PersonalConnectionGoogleCalendar, Account: firstNonEmpty(info.Email, info.Sub), Scopes: scopes, Status: domain.PersonalConnectionActive}
	if !tokens.ExpiresAt.IsZero() {
		expires := tokens.ExpiresAt
		connection.TokenExpiresAt = &expires
	}
	return connection, tokens, nil
}

// revokeGoogleToken revokes a grant at Google (best effort): the refresh token
// when there is one, which revokes the whole grant, else the access token.
func (s *server) revokeGoogleToken(ctx context.Context, refresh, access string) {
	token := firstNonEmpty(refresh, access)
	if token == "" {
		return
	}
	_, _ = s.googleJSON(ctx, http.MethodPost, s.googleEndpointsFor().RevokeURL, nil, url.Values{"token": {token}}, nil)
}

// disconnectGoogleCalendar — DELETE /api/account/connections/google-calendar:
// revokes the grant at Google (best effort) and removes the connection.
func (s *server) disconnectGoogleCalendar(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.codeHostAccountUser(w, r)
	if !ok {
		return
	}
	s.disconnectPersonalConnection(w, r, userID, domain.PersonalConnectionGoogleCalendar, func(connection domain.PersonalConnection) {
		if tokens, err := s.readPersonalConnectionTokens(r.Context(), connection.ID); err == nil {
			revokeCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Second)
			defer cancel()
			s.revokeGoogleToken(revokeCtx, tokens.RefreshToken, tokens.AccessToken)
		}
	})
}

func (s *server) disconnectPersonalConnection(w http.ResponseWriter, r *http.Request, userID, provider string, beforeDelete func(domain.PersonalConnection)) {
	connection, err := s.store.PersonalConnectionFor(r.Context(), userID, provider)
	if errors.Is(err, store.ErrPersonalConnectionNotFound) {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read the connection")
		return
	}
	if beforeDelete != nil {
		beforeDelete(connection)
	}
	if _, err := s.store.DeletePersonalConnection(r.Context(), userID, connection.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "could not disconnect the account")
		return
	}
	s.auditPersonalConnection(r.Context(), r, workspaceKey(r), "personal_connection.unlinked", connection)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) auditPersonalConnection(ctx context.Context, r *http.Request, workspace, action string, connection domain.PersonalConnection) {
	if strings.TrimSpace(workspace) == "" {
		return
	}
	auditCtx := store.WithMetadataFields(context.WithoutCancel(ctx), "auditLog")
	if r != nil {
		auditCtx = store.WithMetadataFields(context.WithoutCancel(r.Context()), "auditLog")
	}
	metadata := map[string]any{"provider": connection.Provider, "account": connection.Account, "userId": connection.UserID}
	if err := s.store.MutateWorkspace(auditCtx, workspace, "audit.personal_connection", connection.ID, map[string]any{"action": action}, func(data *domain.Bootstrap) error {
		appendAudit(data, action, "personal_connection", connection.ID, metadata)
		return nil
	}); err != nil {
		log.Printf("personal connection audit workspace=%s action=%s: %v", workspace, action, err)
	}
}

// ---------------------------------------------------------------- calendar sync

type googleEventTime struct {
	DateTime string `json:"dateTime"`
	Date     string `json:"date"`
}

func (t googleEventTime) parse() (time.Time, bool) {
	if t.DateTime != "" {
		parsed, err := time.Parse(time.RFC3339, t.DateTime)
		return parsed, err == nil
	}
	if t.Date != "" {
		// All-day events carry no zone: read as UTC.
		parsed, err := time.Parse("2006-01-02", t.Date)
		return parsed, err == nil
	}
	return time.Time{}, false
}

type googleEvent struct {
	Status    string          `json:"status"`
	EventType string          `json:"eventType"`
	Start     googleEventTime `json:"start"`
	End       googleEventTime `json:"end"`
}

// awayWindow is the member's current or next out-of-office window: the first
// event that has not ended, extended over events that follow without a gap.
func awayWindow(events []googleEvent, now time.Time) (from, until *time.Time) {
	type span struct{ start, end time.Time }
	spans := []span{}
	for _, event := range events {
		if event.Status == "cancelled" || (event.EventType != "" && event.EventType != "outOfOffice") {
			continue
		}
		start, okStart := event.Start.parse()
		end, okEnd := event.End.parse()
		if okStart && okEnd && end.After(start) && end.After(now) {
			spans = append(spans, span{start, end})
		}
	}
	if len(spans) == 0 {
		return nil, nil
	}
	slices.SortFunc(spans, func(a, b span) int { return a.start.Compare(b.start) })
	start, end := spans[0].start, spans[0].end
	for _, next := range spans[1:] {
		if next.start.After(end) {
			break
		}
		if next.end.After(end) {
			end = next.end
		}
	}
	return &start, &end
}

// readGoogleAwayWindow reads the member's out-of-office events for the next two
// weeks (one request, only that event type) and returns their window.
func (s *server) readGoogleAwayWindow(ctx context.Context, tokens codeHostAccountTokens, now time.Time) (from, until *time.Time, err error) {
	query := url.Values{"eventTypes": {"outOfOffice"}, "singleEvents": {"true"}, "orderBy": {"startTime"}, "maxResults": {"25"},
		"timeMin": {now.UTC().Format(time.RFC3339)}, "timeMax": {now.Add(googleCalendarWindow).UTC().Format(time.RFC3339)},
		"fields": {"items(status,eventType,start,end)"}}
	var out struct {
		Items []googleEvent `json:"items"`
	}
	header := http.Header{"Authorization": {"Bearer " + tokens.AccessToken}}
	if _, err := s.googleJSON(ctx, http.MethodGet, s.googleEndpointsFor().CalendarAPI+"/calendars/primary/events?"+query.Encode(), header, nil, &out); err != nil {
		return nil, nil, err
	}
	from, until = awayWindow(out.Items, now)
	return from, until, nil
}

func googleSyncBackoff(interval time.Duration, failures int) time.Duration {
	delay := interval
	for range failures {
		delay *= 2
		if delay >= googleSyncMaxBackoff {
			return googleSyncMaxBackoff
		}
	}
	return delay
}

// syncPersonalConnectionNow reads one member's calendar if the connection is
// due (the claim keeps replicas and repeated calls from reading it twice in an
// interval) and stores the window only when it changed.
func (s *server) syncPersonalConnectionNow(ctx context.Context, connection domain.PersonalConnection, now time.Time) {
	interval := googleSyncInterval()
	claimed, err := s.store.ClaimPersonalConnectionSync(ctx, connection.ID, now, now.Add(interval))
	if err != nil || !claimed {
		if err != nil {
			log.Printf("personal connection %s: claim sync: %v", connection.ID, err)
		}
		return
	}
	// Re-read: the row may have changed since it was listed.
	current, err := s.store.PersonalConnectionFor(ctx, connection.UserID, connection.Provider)
	if err != nil || current.ID != connection.ID || current.Status != domain.PersonalConnectionActive {
		return
	}
	failures := s.personalConnectionFailures(ctx, current)
	tokens, err := s.googleCalendarTokens(ctx, current)
	if err == nil {
		var from, until *time.Time
		if from, until, err = s.readGoogleAwayWindow(ctx, tokens, now); err == nil {
			if _, err := s.store.RecordPersonalConnectionSyncSuccess(ctx, current, failures, from, until); err != nil {
				log.Printf("personal connection %s: record sync: %v", connection.ID, err)
			}
			return
		}
	}
	switch {
	case errors.Is(err, codehost.ErrUserNotLinked):
		return // already marked reconnect
	case codeHostStatus(err) == http.StatusUnauthorized, codeHostStatus(err) == http.StatusForbidden && failures >= 2:
		// 403 is also how Google reports an exhausted quota, so it only counts
		// as a lost permission after repeated refusals.
		s.markPersonalConnectionReconnect(ctx, current, "Google no longer lets Flow read your calendar. Reconnect Google Calendar.")
		return
	}
	failures++
	next := now.Add(googleSyncBackoff(interval, failures))
	if recordErr := s.store.RecordPersonalConnectionSyncFailure(ctx, current.ID, failures, next); recordErr != nil {
		log.Printf("personal connection %s: record failure: %v", connection.ID, recordErr)
	}
	log.Printf("personal connection %s: calendar sync failed (%d in a row, next %s): %v", connection.ID, failures, next.Format(time.RFC3339), err)
}

func (s *server) personalConnectionFailures(ctx context.Context, connection domain.PersonalConnection) int {
	failures, err := s.store.PersonalConnectionSyncFailures(ctx, connection.ID)
	if err != nil {
		return 0
	}
	return failures
}

// runPersonalConnectionScheduler reads members' calendars in the background.
// It does nothing unless Google is configured (and connector secrets are
// available), runs at most googleSyncBatch connections a minute, and every
// connection is read once per FLOW_GOOGLE_CALENDAR_SYNC_INTERVAL with backoff
// after errors; the claim in the database makes replicas share the work
// without reading a calendar twice.
func (s *server) runPersonalConnectionScheduler(ctx context.Context) {
	if s.store == nil {
		return
	}
	if _, ok := googleOAuth(); !ok || !store.ConnectorSecretsConfigured() {
		return
	}
	ticker := time.NewTicker(personalConnectionTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.store.WorkerContext().Done():
			return
		case <-ticker.C:
			s.runPersonalConnectionTick(ctx, time.Now())
		}
	}
}

func (s *server) runPersonalConnectionTick(ctx context.Context, now time.Time) {
	due, err := s.store.DuePersonalConnections(ctx, domain.PersonalConnectionGoogleCalendar, now, googleSyncBatch)
	if err != nil {
		log.Printf("personal connections: list due: %v", err)
		return
	}
	for _, connection := range due {
		if ctx.Err() != nil {
			return
		}
		syncCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		s.syncPersonalConnectionNow(syncCtx, connection, now)
		cancel()
	}
}

// applyCalendarAway marks members whose synced out-of-office window is active
// as out of office until its end (the profile card's "Availability").
func (s *server) applyCalendarAway(ctx context.Context, data *domain.Bootstrap) {
	if s.store == nil {
		return
	}
	ids := []string{}
	seen := map[string]bool{}
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, user := range data.Users {
		add(user.ID)
	}
	for _, member := range data.Members {
		add(member.User.ID)
	}
	add(data.Viewer.ID)
	if len(ids) == 0 || len(ids) > 2000 {
		return
	}
	now := time.Now()
	windows, err := s.store.ActiveAwayWindows(ctx, ids, now)
	if err != nil || len(windows) == 0 {
		return
	}
	mark := func(user *domain.User) {
		if window, ok := windows[user.ID]; ok && (window.AwayFrom == nil || !window.AwayFrom.After(now)) {
			until := *window.AwayUntil
			user.OutOfOfficeUntil = &until
		}
	}
	data.Users = slices.Clone(data.Users)
	for index := range data.Users {
		mark(&data.Users[index])
	}
	data.Members = slices.Clone(data.Members)
	for index := range data.Members {
		mark(&data.Members[index].User)
	}
	mark(&data.Viewer)
}

// ---------------------------------------------------------------- Notion

// connectNotion — POST /api/account/connections/notion/connect: records that
// the member lets Notion preview their Flow links and returns Notion's
// connection page to open.
func (s *server) connectNotion(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.codeHostAccountUser(w, r)
	if !ok {
		return
	}
	target := notionConnectionURL()
	if target == "" {
		writeError(w, http.StatusConflict, "Notion previews are not configured. An operator must set FLOW_NOTION_CONNECTION_URL.")
		return
	}
	saved, err := s.store.UpsertPersonalConnection(r.Context(), domain.PersonalConnection{ID: newPersonalConnectionID(), UserID: userID, Provider: domain.PersonalConnectionNotion, Status: domain.PersonalConnectionActive})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start the Notion connection")
		return
	}
	s.auditPersonalConnection(r.Context(), r, workspaceKey(r), "personal_connection.linked", saved)
	writeJSON(w, http.StatusOK, map[string]string{"url": target})
}

// disconnectNotion — DELETE /api/account/connections/notion: Notion's
// previews of the member's links stop working.
func (s *server) disconnectNotion(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.codeHostAccountUser(w, r)
	if !ok {
		return
	}
	s.disconnectPersonalConnection(w, r, userID, domain.PersonalConnectionNotion, nil)
}
