package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// fakeGoogle is a Google OAuth and Calendar API stand-in.
type fakeGoogle struct {
	*httptest.Server
	mu             sync.Mutex
	challenge      string
	grantedScope   string
	noRefresh      bool
	expiresIn      int
	refreshStatus  int
	calendarStatus int
	events         []map[string]any
	tokenForms     []url.Values
	revoked        []string
	calendarCalls  int
	calendarQuery  url.Values
	userinfoAuth   string
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	t.Helper()
	f := &fakeGoogle{expiresIn: 3600, grantedScope: "openid https://www.googleapis.com/auth/userinfo.email " + googleCalendarReadScope}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		f.tokenForms = append(f.tokenForms, r.PostForm)
		if r.PostForm.Get("client_id") != "google-client" || r.PostForm.Get("client_secret") != "google-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.PostForm.Get("grant_type") {
		case "authorization_code":
			sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
			if r.PostForm.Get("code") != "good" || base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
			body := map[string]any{"access_token": "ya29.access_1", "expires_in": f.expiresIn, "scope": f.grantedScope}
			if !f.noRefresh {
				body["refresh_token"] = "1//refresh_1"
			}
			_ = json.NewEncoder(w).Encode(body)
		case "refresh_token":
			if f.refreshStatus != 0 {
				w.WriteHeader(f.refreshStatus)
				_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
			if r.PostForm.Get("refresh_token") != "1//refresh_1" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "ya29.access_2", "expires_in": 3600, "scope": f.grantedScope})
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	})
	mux.HandleFunc("POST /revoke", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		f.revoked = append(f.revoked, r.PostForm.Get("token"))
		f.mu.Unlock()
		_, _ = w.Write([]byte(`{}`))
	})
	mux.HandleFunc("GET /userinfo", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.userinfoAuth = r.Header.Get("Authorization")
		f.mu.Unlock()
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ya29.") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"sub":"1234","email":"member@example.com"}`))
	})
	mux.HandleFunc("GET /calendar/v3/calendars/primary/events", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calendarCalls++
		f.calendarQuery = r.URL.Query()
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ya29.") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if f.calendarStatus != 0 {
			w.WriteHeader(f.calendarStatus)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": f.events})
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func (f *fakeGoogle) endpoints() googleEndpoints {
	return googleEndpoints{AuthorizeURL: f.URL + "/authorize", TokenURL: f.URL + "/token", RevokeURL: f.URL + "/revoke", UserinfoURL: f.URL + "/userinfo", CalendarAPI: f.URL + "/calendar/v3"}
}

func (f *fakeGoogle) calendarCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calendarCalls
}

func (f *fakeGoogle) revokedTokens() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.revoked...)
}

func (f *fakeGoogle) setEvents(events ...map[string]any) {
	f.mu.Lock()
	f.events = events
	f.mu.Unlock()
}

func oooEvent(start, end time.Time) map[string]any {
	return map[string]any{"eventType": "outOfOffice", "status": "confirmed", "start": map[string]string{"dateTime": start.Format(time.RFC3339)}, "end": map[string]string{"dateTime": end.Format(time.RFC3339)}}
}

type personalFixture struct {
	t       *testing.T
	repo    *store.SQLiteStore
	server  *server
	handler http.Handler
	google  *fakeGoogle
	viewer  domain.User
	dbPath  string
}

func newPersonalFixture(t *testing.T, configured bool) *personalFixture {
	t.Helper()
	enableConnectorSecrets(t)
	t.Setenv("FLOW_APP_URL", "https://flow.example")
	t.Setenv("FLOW_GOOGLE_OAUTH_CLIENT_ID", "")
	t.Setenv("FLOW_GOOGLE_OAUTH_CLIENT_SECRET", "")
	t.Setenv("FLOW_NOTION_CONNECTION_URL", "")
	t.Setenv("FLOW_GOOGLE_CALENDAR_SYNC_INTERVAL", "")
	google := newFakeGoogle(t)
	googleEndpointsOverride = func() *googleEndpoints { e := google.endpoints(); return &e }()
	t.Cleanup(func() { googleEndpointsOverride = nil })
	if configured {
		t.Setenv("FLOW_GOOGLE_OAUTH_CLIENT_ID", "google-client")
		t.Setenv("FLOW_GOOGLE_OAUTH_CLIENT_SECRET", "google-secret")
	}
	dbPath := filepath.Join(t.TempDir(), "flow.db")
	repository, err := store.OpenSQLiteTestFixture(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	s := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true}
	return &personalFixture{t: t, repo: repository, server: s, handler: newHandler(s), google: google, viewer: repository.Account().Viewer, dbPath: dbPath}
}

func (f *personalFixture) do(method, target string, input any) *httptest.ResponseRecorder {
	var body *bytes.Reader
	if input != nil {
		raw, _ := json.Marshal(input)
		body = bytes.NewReader(raw)
	} else {
		body = bytes.NewReader(nil)
	}
	request := httptest.NewRequest(method, target, body)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	recorder := httptest.NewRecorder()
	f.handler.ServeHTTP(recorder, request)
	return recorder
}

func (f *personalFixture) connections() personalConnectionsResponse {
	f.t.Helper()
	rec := f.do(http.MethodGet, "/api/account/connections?workspace=test-workspace", nil)
	if rec.Code != http.StatusOK {
		f.t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	for _, secret := range []string{"ya29.", "1//refresh", "google-secret"} {
		if strings.Contains(rec.Body.String(), secret) {
			f.t.Fatalf("connections response leaks %s: %s", secret, rec.Body.String())
		}
	}
	var response personalConnectionsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &response)
	return response
}

// authorize starts the Google link and returns the state to call back with.
func (f *personalFixture) authorize() (state string, authorizeURL *url.URL) {
	f.t.Helper()
	rec := f.do(http.MethodPost, "/api/account/connections/google-calendar/authorize?workspace=test-workspace", nil)
	if rec.Code != http.StatusOK {
		f.t.Fatalf("authorize: %d %s", rec.Code, rec.Body.String())
	}
	var start map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &start)
	parsed, err := url.Parse(start["url"])
	if err != nil {
		f.t.Fatal(err)
	}
	f.google.mu.Lock()
	f.google.challenge = parsed.Query().Get("code_challenge")
	f.google.mu.Unlock()
	return parsed.Query().Get("state"), parsed
}

func (f *personalFixture) callback(code, state string) *httptest.ResponseRecorder {
	return f.do(http.MethodGet, googleCalendarCallbackPath+"?code="+code+"&state="+url.QueryEscape(state), nil)
}

func (f *personalFixture) connectGoogle() domain.PersonalConnection {
	f.t.Helper()
	state, _ := f.authorize()
	if rec := f.callback("good", state); rec.Code != http.StatusOK {
		f.t.Fatalf("callback: %d %s", rec.Code, rec.Body.String())
	}
	connection := f.connections().GoogleCalendar.Connection
	if connection == nil {
		f.t.Fatal("google calendar not connected")
	}
	return *connection
}

func (f *personalFixture) tokens(id string) codeHostAccountTokens {
	f.t.Helper()
	tokens, err := f.server.readPersonalConnectionTokens(f.t.Context(), id)
	if err != nil {
		f.t.Fatal(err)
	}
	return tokens
}

func TestGoogleCalendarUnconfiguredOffersNothing(t *testing.T) {
	f := newPersonalFixture(t, false)
	response := f.connections()
	if response.GoogleCalendar.Available || response.Notion.Available {
		t.Fatalf("unconfigured deployment offers connections: %+v", response)
	}
	if rec := f.do(http.MethodPost, "/api/account/connections/google-calendar/authorize?workspace=test-workspace", nil); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "FLOW_GOOGLE_OAUTH_CLIENT_ID") {
		t.Fatalf("authorize while unconfigured: %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.do(http.MethodPost, "/api/account/connections/notion/connect?workspace=test-workspace", nil); rec.Code != http.StatusConflict {
		t.Fatalf("notion connect while unconfigured: %d %s", rec.Code, rec.Body.String())
	}
	// No scheduler work and no calls without configuration.
	f.server.runPersonalConnectionTick(t.Context(), time.Now())
	if f.google.calendarCallCount() != 0 {
		t.Fatal("calendar was read while unconfigured")
	}
}

func TestGoogleCalendarLinkFlowStoresSealedTokensAndReadsOnlyOutOfOffice(t *testing.T) {
	f := newPersonalFixture(t, true)
	away := time.Now().Add(-time.Hour).Truncate(time.Second)
	f.google.setEvents(oooEvent(away, away.Add(26*time.Hour)))

	state, authorize := f.authorize()
	query := authorize.Query()
	if !strings.HasPrefix(authorize.String(), f.google.URL+"/authorize?") || query.Get("client_id") != "google-client" || query.Get("redirect_uri") != "https://flow.example"+googleCalendarCallbackPath ||
		query.Get("code_challenge_method") != "S256" || query.Get("access_type") != "offline" || query.Get("response_type") != "code" {
		t.Fatalf("authorize url %s", authorize)
	}
	if scope := query.Get("scope"); scope != "openid email "+googleCalendarReadScope {
		t.Fatalf("scopes %q: only the calendar's read scope may be requested", scope)
	}
	if rec := f.callback("good", state); rec.Code != http.StatusOK {
		t.Fatalf("callback: %d %s", rec.Code, rec.Body.String())
	}
	response := f.connections()
	connection := response.GoogleCalendar.Connection
	if !response.GoogleCalendar.Available || connection == nil || connection.Account != "member@example.com" || connection.Status != domain.PersonalConnectionActive {
		t.Fatalf("connection %+v", response.GoogleCalendar)
	}
	if tokens := f.tokens(connection.ID); tokens.AccessToken != "ya29.access_1" || tokens.RefreshToken != "1//refresh_1" {
		t.Fatalf("tokens %+v", tokens)
	}
	// The first read happened at link time, for out-of-office events only.
	if f.google.calendarCallCount() != 1 || f.google.calendarQuery.Get("eventTypes") != "outOfOffice" || f.google.calendarQuery.Get("fields") == "" {
		t.Fatalf("calendar reads %d query %v", f.google.calendarCallCount(), f.google.calendarQuery)
	}
	if connection.AwayUntil == nil || !connection.AwayUntil.Equal(away.Add(26*time.Hour)) {
		t.Fatalf("away window %+v", connection)
	}
	// The member shows as out of office in the workspace data.
	bootstrap := domain.Bootstrap{Viewer: f.viewer, Users: []domain.User{f.viewer}}
	f.server.applyCalendarAway(t.Context(), &bootstrap)
	if bootstrap.Viewer.OutOfOfficeUntil == nil || !bootstrap.Viewer.OutOfOfficeUntil.Equal(away.Add(26*time.Hour)) || bootstrap.Users[0].OutOfOfficeUntil == nil {
		t.Fatalf("viewer not out of office: %+v", bootstrap.Viewer)
	}
	if audits := auditActionsFor(f.repo, "personal_connection"); len(audits) != 1 || audits[0] != "personal_connection.linked" {
		// (the log lists the newest entry first)
		t.Fatalf("audit %v", audits)
	}
}

func auditActionsFor(repo *store.SQLiteStore, resource string) []string {
	data, _ := repo.WorkspaceMetadataFields("test-workspace", "auditLog")
	actions := []string{}
	for _, entry := range data.AuditLog {
		if entry.ResourceType == resource {
			actions = append(actions, entry.Action)
		}
	}
	return actions
}

func TestGoogleCalendarCallbackRejectsBadAndReusedState(t *testing.T) {
	f := newPersonalFixture(t, true)
	if rec := f.callback("good", "chl.not-a-real-state"); rec.Code != http.StatusBadRequest {
		t.Fatalf("forged state: %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.callback("good", "unprefixed"); rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed state: %d %s", rec.Code, rec.Body.String())
	}
	state, _ := f.authorize()
	if rec := f.callback("good", state); rec.Code != http.StatusOK {
		t.Fatalf("callback: %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.callback("good", state); rec.Code != http.StatusBadRequest {
		t.Fatalf("a state must work once: %d %s", rec.Code, rec.Body.String())
	}
	// A wrong PKCE verifier (state of another request) is refused by Google.
	other, _ := f.authorize()
	f.google.mu.Lock()
	f.google.challenge = "someone-elses-challenge"
	f.google.mu.Unlock()
	if rec := f.callback("good", other); rec.Code != http.StatusBadRequest {
		t.Fatalf("pkce mismatch: %d %s", rec.Code, rec.Body.String())
	}
	// Denied consent.
	denied, _ := f.authorize()
	if rec := f.do(http.MethodGet, googleCalendarCallbackPath+"?error=access_denied&state="+url.QueryEscape(denied), nil); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "not granted") {
		t.Fatalf("denied: %d %s", rec.Code, rec.Body.String())
	}
	// A state made for a code host link is not a Google link.
	hostState, _, err := f.server.newCodeHostLinkState(t.Context(), codeHostLinkState{UserID: f.viewer.ID, Workspace: "test-workspace", CodeHost: domain.CodeHostGitHub, Method: domain.CodeHostAuthOAuth})
	if err != nil {
		t.Fatal(err)
	}
	if rec := f.callback("good", hostState); rec.Code != http.StatusBadRequest {
		t.Fatalf("code host state accepted: %d %s", rec.Code, rec.Body.String())
	}
	// A state started by another member is refused.
	foreign, _, err := f.server.newCodeHostLinkState(t.Context(), codeHostLinkState{UserID: "someone-else", Workspace: "test-workspace", CodeHost: domain.PersonalConnectionGoogleCalendar, Method: domain.CodeHostAuthOAuth})
	if err != nil {
		t.Fatal(err)
	}
	if rec := f.callback("good", foreign); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "another user") {
		t.Fatalf("foreign state: %d %s", rec.Code, rec.Body.String())
	}
}

func TestGoogleCalendarRefusesWithoutCalendarPermissionOrOfflineAccess(t *testing.T) {
	f := newPersonalFixture(t, true)
	f.google.grantedScope = "openid https://www.googleapis.com/auth/userinfo.email"
	state, _ := f.authorize()
	if rec := f.callback("good", state); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "calendar permission") {
		t.Fatalf("missing scope: %d %s", rec.Code, rec.Body.String())
	}
	if f.connections().GoogleCalendar.Connection != nil {
		t.Fatal("connected without the calendar scope")
	}
	if revoked := f.google.revokedTokens(); len(revoked) != 1 || revoked[0] != "1//refresh_1" {
		t.Fatalf("a refused grant must be revoked: %v", revoked)
	}
	f.google.grantedScope = "openid email " + googleCalendarReadScope
	f.google.noRefresh = true
	state, _ = f.authorize()
	if rec := f.callback("good", state); rec.Code != http.StatusBadRequest {
		t.Fatalf("no refresh token: %d %s", rec.Code, rec.Body.String())
	}
	if f.connections().GoogleCalendar.Connection != nil {
		t.Fatal("connected without offline access")
	}
}

func TestGoogleCalendarRefreshesExpiredTokenAndMarksRevokedGrantReconnect(t *testing.T) {
	f := newPersonalFixture(t, true)
	connection := f.connectGoogle()
	// An access token past its expiry is refreshed with the sealed refresh token.
	if err := f.server.writePersonalConnectionTokens(t.Context(), connection.ID, codeHostAccountTokens{AccessToken: "ya29.stale", RefreshToken: "1//refresh_1", ExpiresAt: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	tokens, err := f.server.googleCalendarTokens(t.Context(), connection)
	if err != nil || tokens.AccessToken != "ya29.access_2" || tokens.RefreshToken != "1//refresh_1" {
		t.Fatalf("refreshed %+v %v", tokens, err)
	}
	if stored := f.tokens(connection.ID); stored.AccessToken != "ya29.access_2" {
		t.Fatalf("stored %+v", stored)
	}
	// Google revokes the grant: the connection needs a reconnect, the sync stops.
	if err := f.server.writePersonalConnectionTokens(t.Context(), connection.ID, codeHostAccountTokens{AccessToken: "ya29.stale", RefreshToken: "1//refresh_1", ExpiresAt: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	f.google.mu.Lock()
	f.google.refreshStatus = http.StatusBadRequest
	f.google.mu.Unlock()
	if _, err := f.server.googleCalendarTokens(t.Context(), connection); err == nil {
		t.Fatal("a revoked grant must not yield a token")
	}
	view := f.connections().GoogleCalendar.Connection
	if view == nil || view.Status != domain.PersonalConnectionReconnect || view.StatusReason == "" {
		t.Fatalf("after revoke: %+v", view)
	}
	before := f.google.calendarCallCount()
	f.server.runPersonalConnectionTick(t.Context(), time.Now().Add(48*time.Hour))
	if f.google.calendarCallCount() != before {
		t.Fatal("a connection that needs a reconnect must not be read")
	}
	// Reconnecting makes it active again.
	f.google.mu.Lock()
	f.google.refreshStatus = 0
	f.google.mu.Unlock()
	f.connectGoogle()
	if view := f.connections().GoogleCalendar.Connection; view == nil || view.Status != domain.PersonalConnectionActive {
		t.Fatalf("after reconnect: %+v", view)
	}
}

func TestGoogleCalendarDisconnectRevokesAtGoogleAndDeletesTokens(t *testing.T) {
	f := newPersonalFixture(t, true)
	connection := f.connectGoogle()
	if rec := f.do(http.MethodDelete, "/api/account/connections/google-calendar?workspace=test-workspace", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("disconnect: %d %s", rec.Code, rec.Body.String())
	}
	if revoked := f.google.revokedTokens(); len(revoked) != 1 || revoked[0] != "1//refresh_1" {
		t.Fatalf("revoked %v", revoked)
	}
	if f.connections().GoogleCalendar.Connection != nil {
		t.Fatal("still connected")
	}
	if _, err := f.server.readPersonalConnectionTokens(t.Context(), connection.ID); err == nil {
		t.Fatal("sealed tokens survived the disconnect")
	}
	if rec := f.do(http.MethodDelete, "/api/account/connections/google-calendar?workspace=test-workspace", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("second disconnect: %d", rec.Code)
	}
	if audits := auditActionsFor(f.repo, "personal_connection"); len(audits) != 2 || audits[0] != "personal_connection.unlinked" || audits[1] != "personal_connection.linked" {
		t.Fatalf("audit %v", audits)
	}
	// The away window is gone with the connection.
	bootstrap := domain.Bootstrap{Viewer: f.viewer, Users: []domain.User{f.viewer}}
	f.server.applyCalendarAway(t.Context(), &bootstrap)
	if bootstrap.Viewer.OutOfOfficeUntil != nil {
		t.Fatal("out-of-office outlived the connection")
	}
}

func TestGoogleCalendarSyncIsBoundedAndWritesOnlyOnChange(t *testing.T) {
	f := newPersonalFixture(t, true)
	now := time.Now()
	f.google.setEvents(oooEvent(now.Add(48*time.Hour), now.Add(72*time.Hour)))
	connection := f.connectGoogle()
	updatedAt := func() time.Time {
		current, err := f.repo.PersonalConnectionFor(t.Context(), f.viewer.ID, domain.PersonalConnectionGoogleCalendar)
		if err != nil {
			t.Fatal(err)
		}
		return current.UpdatedAt
	}
	if connection.AwayFrom == nil || connection.AwayUntil == nil {
		t.Fatalf("upcoming window not stored: %+v", connection)
	}
	// An upcoming window does not make the member out of office yet.
	bootstrap := domain.Bootstrap{Viewer: f.viewer}
	f.server.applyCalendarAway(t.Context(), &bootstrap)
	if bootstrap.Viewer.OutOfOfficeUntil != nil {
		t.Fatal("upcoming window shown as active")
	}
	stored := updatedAt()
	// Not due again within the interval, however often the tick runs.
	for range 5 {
		f.server.runPersonalConnectionTick(t.Context(), now.Add(time.Minute))
	}
	if f.google.calendarCallCount() != 1 {
		t.Fatalf("calendar read %d times inside one interval", f.google.calendarCallCount())
	}
	// Due again, nothing changed: one read, no write.
	later := now.Add(googleSyncInterval() + time.Minute)
	f.server.runPersonalConnectionTick(t.Context(), later)
	if f.google.calendarCallCount() != 2 || !updatedAt().Equal(stored) {
		t.Fatalf("reads %d; unchanged window must not be rewritten (updated %v -> %v)", f.google.calendarCallCount(), stored, updatedAt())
	}
	// The window changes: it is written.
	f.google.setEvents(oooEvent(now.Add(-time.Hour), now.Add(96*time.Hour)))
	f.server.runPersonalConnectionTick(t.Context(), later.Add(googleSyncInterval()+time.Minute))
	current, _ := f.repo.PersonalConnectionFor(t.Context(), f.viewer.ID, domain.PersonalConnectionGoogleCalendar)
	if current.AwayUntil == nil || current.AwayUntil.Before(now.Add(95*time.Hour)) || !updatedAt().After(stored) {
		t.Fatalf("changed window not stored: %+v", current)
	}
	// The event is gone: the window clears.
	f.google.setEvents()
	f.server.runPersonalConnectionTick(t.Context(), later.Add(3*googleSyncInterval()))
	if current, _ = f.repo.PersonalConnectionFor(t.Context(), f.viewer.ID, domain.PersonalConnectionGoogleCalendar); current.AwayUntil != nil {
		t.Fatalf("window not cleared: %+v", current)
	}
}

func TestGoogleCalendarSyncBacksOffOnErrorsAndReconnectsOnUnauthorized(t *testing.T) {
	f := newPersonalFixture(t, true)
	connection := f.connectGoogle()
	f.google.mu.Lock()
	f.google.calendarStatus = http.StatusInternalServerError
	f.google.mu.Unlock()
	interval := googleSyncInterval()
	at := time.Now().Add(interval + time.Minute)
	calls := f.google.calendarCallCount()
	f.server.runPersonalConnectionTick(t.Context(), at)
	if f.google.calendarCallCount() != calls+1 {
		t.Fatal("due connection not read")
	}
	failures, _ := f.repo.PersonalConnectionSyncFailures(t.Context(), connection.ID)
	if failures != 1 {
		t.Fatalf("failures %d", failures)
	}
	// Backed off: not retried one interval later, retried after the doubled delay.
	f.server.runPersonalConnectionTick(t.Context(), at.Add(interval+time.Minute))
	if f.google.calendarCallCount() != calls+1 {
		t.Fatal("error not backed off")
	}
	f.server.runPersonalConnectionTick(t.Context(), at.Add(2*interval+time.Minute))
	if f.google.calendarCallCount() != calls+2 {
		t.Fatal("not retried after the backoff")
	}
	if got := googleSyncBackoff(interval, 20); got != googleSyncMaxBackoff {
		t.Fatalf("backoff is unbounded: %v", got)
	}
	// Recovery resets the failure count.
	f.google.mu.Lock()
	f.google.calendarStatus = 0
	f.google.mu.Unlock()
	f.server.runPersonalConnectionTick(t.Context(), at.Add(10*time.Hour))
	if failures, _ = f.repo.PersonalConnectionSyncFailures(t.Context(), connection.ID); failures != 0 {
		t.Fatalf("failures after recovery %d", failures)
	}
	// 401 from the calendar: Google no longer accepts the grant.
	f.google.mu.Lock()
	f.google.calendarStatus = http.StatusUnauthorized
	f.google.mu.Unlock()
	f.server.runPersonalConnectionTick(t.Context(), at.Add(20*time.Hour))
	if view := f.connections().GoogleCalendar.Connection; view == nil || view.Status != domain.PersonalConnectionReconnect {
		t.Fatalf("unauthorized: %+v", view)
	}
}

func TestTwoReplicasReadACalendarOnce(t *testing.T) {
	f := newPersonalFixture(t, true)
	f.connectGoogle()
	before := f.google.calendarCallCount()
	replica, err := store.OpenSQLiteTestFixture(f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { replica.Close() })
	second := &server{store: replica, uploadPath: t.TempDir(), authDisabled: true}
	at := time.Now().Add(googleSyncInterval() + time.Minute)
	var wg sync.WaitGroup
	for _, s := range []*server{f.server, second, f.server, second} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.runPersonalConnectionTick(t.Context(), at)
		}()
	}
	wg.Wait()
	if got := f.google.calendarCallCount() - before; got != 1 {
		t.Fatalf("calendar read %d times by concurrent replicas", got)
	}
}

func TestAwayWindowMergesContiguousEventsAndIgnoresOthers(t *testing.T) {
	now := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	day := func(date string) googleEventTime { return googleEventTime{Date: date} }
	events := []googleEvent{
		{EventType: "outOfOffice", Status: "cancelled", Start: day("2026-10-20"), End: day("2026-10-30")},
		{EventType: "outOfOffice", Start: day("2026-10-21"), End: day("2026-10-22")},
		{EventType: "outOfOffice", Start: day("2026-10-20"), End: day("2026-10-21")},
		{EventType: "focusTime", Start: day("2026-10-25"), End: day("2026-10-26")},
		{EventType: "outOfOffice", Start: day("2026-10-24"), End: day("2026-10-25")},
		{EventType: "outOfOffice", Start: day("2026-10-01"), End: day("2026-10-02")},
	}
	from, until := awayWindow(events, now)
	if from == nil || until == nil || from.Format("2006-01-02") != "2026-10-20" || until.Format("2006-01-02") != "2026-10-22" {
		t.Fatalf("window %v %v", from, until)
	}
	if from, until := awayWindow(nil, now); from != nil || until != nil {
		t.Fatal("empty calendar must have no window")
	}
}

func TestNotionConnectionEnablesLinkPreviewsForTheMember(t *testing.T) {
	f := newPersonalFixture(t, true)
	t.Setenv("FLOW_NOTION_CONNECTION_URL", "https://www.notion.com/connections/flow")
	issueList := f.do(http.MethodGet, "/api/issues?workspace=test-workspace&limit=1", nil)
	match := regexp.MustCompile(`"identifier":"([A-Za-z0-9]+-[0-9]+)"`).FindStringSubmatch(issueList.Body.String())
	if match == nil {
		t.Fatalf("no issue to preview: %d %s", issueList.Code, issueList.Body.String())
	}
	link := "https://flow.example/test-workspace/issue/" + match[1] + "/some-title"
	preview := func(link string) *httptest.ResponseRecorder {
		return f.do(http.MethodGet, "/api/notion/link-preview?url="+url.QueryEscape(link), nil)
	}

	if !f.connections().Notion.Available || f.connections().Notion.Connection != nil {
		t.Fatalf("notion state %+v", f.connections().Notion)
	}
	if rec := preview(link); rec.Code != http.StatusForbidden {
		t.Fatalf("preview before connecting: %d %s", rec.Code, rec.Body.String())
	}
	rec := f.do(http.MethodPost, "/api/account/connections/notion/connect?workspace=test-workspace", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "https://www.notion.com/connections/flow") {
		t.Fatalf("connect: %d %s", rec.Code, rec.Body.String())
	}
	if connection := f.connections().Notion.Connection; connection == nil || connection.Status != domain.PersonalConnectionActive {
		t.Fatalf("not connected: %+v", connection)
	}
	rec = preview(link)
	var result linkPreview
	_ = json.Unmarshal(rec.Body.Bytes(), &result)
	if rec.Code != http.StatusOK || result.Kind != "issue" || result.Identifier != match[1] || result.Title == "" || result.URL != "https://flow.example/test-workspace/issue/"+match[1] {
		t.Fatalf("preview: %d %s", rec.Code, rec.Body.String())
	}
	for name, bad := range map[string]string{
		"foreign host":   "https://evil.example/test-workspace/issue/" + match[1],
		"private target": "http://127.0.0.1/test-workspace/issue/" + match[1],
		"credentials":    "https://user:pw@flow.example/test-workspace/issue/" + match[1],
		"not a record":   "https://flow.example/test-workspace/settings/account",
		"missing issue":  "https://flow.example/test-workspace/issue/NOPE-99999",
		"other kind":     "https://flow.example/test-workspace/view/abc",
	} {
		if rec := preview(bad); rec.Code < 400 || rec.Code >= 500 {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	if rec := f.do(http.MethodDelete, "/api/account/connections/notion?workspace=test-workspace", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("disconnect: %d", rec.Code)
	}
	if rec := preview(link); rec.Code != http.StatusForbidden {
		t.Fatalf("preview after disconnect: %d %s", rec.Code, rec.Body.String())
	}
}

func TestPersonalConnectionsRequireSignIn(t *testing.T) {
	f := newPersonalFixture(t, true)
	f.server.authDisabled = false
	rec := f.do(http.MethodGet, "/api/account/connections?workspace=test-workspace", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d %s", rec.Code, rec.Body.String())
	}
}
