package main

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"

	"github.com/crewjam/saml/samlsp"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const linkTestIssuer = "https://idp.link-test.example"

type linkTestIDP struct {
	key    *rsa.PrivateKey
	server *httptest.Server
	claims map[string]any
}

func newLinkTestIDP(t *testing.T) *linkTestIDP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	idp := &linkTestIDP{key: key}
	idp.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "id_token": idp.sign(t)})
	}))
	t.Cleanup(idp.server.Close)
	return idp
}

func (i *linkTestIDP) sign(t *testing.T) string {
	t.Helper()
	encode := func(value any) string {
		raw, _ := json.Marshal(value)
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	signing := encode(map[string]any{"alg": "RS256", "typ": "JWT"}) + "." + encode(i.claims)
	digest := sha256.Sum256([]byte(signing))
	signature, err := rsa.SignPKCS1v15(rand.Reader, i.key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(signature)
}

type linkTestEnv struct {
	s      *server
	repo   *store.SQLiteStore
	idp    *linkTestIDP
	mux    *http.ServeMux
	victim domain.AuthSession
}

func newLinkTestEnv(t *testing.T) *linkTestEnv {
	t.Helper()
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "link.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	idp := newLinkTestIDP(t)
	client := &oidcClient{name: "Test IdP", issuer: linkTestIssuer, oauth: oauth2.Config{ClientID: "cid", Endpoint: oauth2.Endpoint{AuthURL: idp.server.URL + "/authorize", TokenURL: idp.server.URL + "/token"}, Scopes: []string{oidc.ScopeOpenID}},
		verifier: oidc.NewVerifier(linkTestIssuer, &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{&idp.key.PublicKey}}, &oidc.Config{ClientID: "cid"})}
	s := &server{store: repo, allowedOrigin: "https://flow.example.com", externalAuth: &externalAuth{
		config: config.AuthConfig{AutoProvision: true}, appURL: "https://flow.example.com", providers: map[string]*oidcClient{"oidc": client}}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/auth/{provider}/callback", s.finishOIDC)
	mux.HandleFunc("GET /api/account/identities/link/{provider}/start", func(w http.ResponseWriter, r *http.Request) {
		// Stand-in for the authenticate middleware: the session cookie names the user.
		if cookie, err := r.Cookie(sessionCookieName); err == nil {
			if user, err := repo.AuthenticateSession(r.Context(), cookie.Value); err == nil {
				r = r.WithContext(context.WithValue(r.Context(), authUserContextKey{}, user))
			}
		}
		s.startOIDCLink(w, r)
	})
	victim, _, err := repo.LoginExternal(t.Context(), "victim@example.com", "Victim", "", true)
	if err != nil {
		t.Fatal(err)
	}
	return &linkTestEnv{s: s, repo: repo, idp: idp, mux: mux, victim: victim}
}

func (e *linkTestEnv) callback(t *testing.T, claims map[string]any, state externalAuthState, session string) *httptest.ResponseRecorder {
	t.Helper()
	claims["iss"], claims["aud"], claims["exp"], claims["iat"] = linkTestIssuer, "cid", 4102444800, 1700000000
	claims["nonce"] = state.Nonce
	e.idp.claims = claims
	req := httptest.NewRequest(http.MethodGet, "/api/auth/oidc/callback?code=c&state="+url.QueryEscape(state.State), nil)
	req.AddCookie(&http.Cookie{Name: externalAuthCookie, Value: encodeExternalState(state)})
	if session != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

func (e *linkTestEnv) identities(t *testing.T, userID string) []domain.AuthIdentity {
	t.Helper()
	items, err := e.repo.ListAuthIdentities(t.Context(), userID)
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func loginState(link bool) externalAuthState {
	return externalAuthState{Provider: "oidc", State: "state1", Nonce: "nonce1", Verifier: "verifier1", Link: link, LinkWorkspace: map[bool]string{true: "test-workspace"}[link]}
}

func hasSessionCookie(rec *httptest.ResponseRecorder) bool {
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName && c.Value != "" {
			return true
		}
	}
	return false
}

func TestOIDCSignInWithVictimEmailIsNotMerged(t *testing.T) {
	for name, extra := range map[string]map[string]any{
		"email_verified absent": {},
		"email_verified false":  {"email_verified": false},
	} {
		t.Run(name, func(t *testing.T) {
			e := newLinkTestEnv(t)
			claims := map[string]any{"sub": "attacker-sub", "email": "victim@example.com", "name": "Attacker"}
			for k, v := range extra {
				claims[k] = v
			}
			rec := e.callback(t, claims, loginState(false), "")
			location := rec.Header().Get("Location")
			if rec.Code != http.StatusFound || !strings.Contains(location, "/auth/error") || !strings.Contains(location, url.QueryEscape("link this provider from your account settings")) {
				t.Fatalf("status=%d location=%q", rec.Code, location)
			}
			if hasSessionCookie(rec) || len(e.identities(t, e.victim.User.ID)) != 0 {
				t.Fatal("a session or identity was issued for the victim")
			}
		})
	}
}

func TestOIDCSignInWithVerifiedEmailLinksFirstTime(t *testing.T) {
	e := newLinkTestEnv(t)
	rec := e.callback(t, map[string]any{"sub": "victim-sub", "email": "victim@example.com", "email_verified": true, "name": "Victim"}, loginState(false), "")
	if rec.Code != http.StatusFound || !hasSessionCookie(rec) || len(e.identities(t, e.victim.User.ID)) != 1 {
		t.Fatalf("verified email did not link: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	// The same identity then signs in unchanged, even with a different email.
	rec = e.callback(t, map[string]any{"sub": "victim-sub", "email": "changed@example.com", "name": "Victim"}, loginState(false), "")
	if rec.Code != http.StatusFound || !hasSessionCookie(rec) || len(e.identities(t, e.victim.User.ID)) != 1 {
		t.Fatalf("linked identity sign-in changed: %d %s", rec.Code, rec.Header().Get("Location"))
	}
}

func TestOIDCSignInProvisionsNewUsersAsBefore(t *testing.T) {
	e := newLinkTestEnv(t)
	rec := e.callback(t, map[string]any{"sub": "new-sub", "email": "new@example.com", "email_verified": true, "name": "New"}, loginState(false), "")
	if rec.Code != http.StatusFound || !hasSessionCookie(rec) {
		t.Fatalf("new user was refused: %d %s", rec.Code, rec.Header().Get("Location"))
	}
}

func TestOIDCAuthenticatedLinkBindsToTheSessionUser(t *testing.T) {
	e := newLinkTestEnv(t)
	other, _, err := e.repo.LoginExternal(t.Context(), "other@example.com", "Other", "", true)
	if err != nil {
		t.Fatal(err)
	}
	_, otherToken, err := e.repo.LoginExternal(t.Context(), "other@example.com", "Other", "", true)
	if err != nil {
		t.Fatal(err)
	}
	// The start endpoint needs a session and marks the round trip as a link.
	anonymous := httptest.NewRecorder()
	e.mux.ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, "/api/account/identities/link/oidc/start", nil))
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous link start: %d", anonymous.Code)
	}
	startReq := httptest.NewRequest(http.MethodGet, "/api/account/identities/link/oidc/start?workspace=test-workspace", nil)
	startReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: otherToken})
	start := httptest.NewRecorder()
	e.mux.ServeHTTP(start, startReq)
	var cookie string
	for _, c := range start.Result().Cookies() {
		if c.Name == externalAuthCookie {
			cookie = c.Value
		}
	}
	state := externalAuthState{Provider: "oidc", State: "state1", Nonce: "nonce1", Verifier: "verifier1", Link: true, LinkWorkspace: "test-workspace"}
	if start.Code != http.StatusFound || cookie != encodeExternalState(externalAuthState{Provider: "oidc", State: strings.Split(cookie, ".")[1], Nonce: strings.Split(cookie, ".")[2], Verifier: strings.Split(cookie, ".")[3], Link: true, LinkWorkspace: "test-workspace"}) {
		t.Fatalf("link start: %d cookie=%q", start.Code, cookie)
	}
	// The IdP account carries the victim's email, unverified: it is linked to
	// the session user (other), not to the account its email names.
	claims := map[string]any{"sub": "link-sub", "email": "victim@example.com", "name": "Other"}
	rec := e.callback(t, claims, state, otherToken)
	if rec.Code != http.StatusFound || !strings.HasSuffix(rec.Header().Get("Location"), "/test-workspace/settings/account/connections") {
		t.Fatalf("link callback: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if got := e.identities(t, other.User.ID); len(got) != 1 || got[0].Subject != "link-sub" {
		t.Fatalf("session user identities=%#v", got)
	}
	if len(e.identities(t, e.victim.User.ID)) != 0 {
		t.Fatal("the identity was linked to the account named by the email")
	}
	// No session, no link.
	rec = e.callback(t, map[string]any{"sub": "link-sub-2", "name": "X"}, state, "")
	if !strings.Contains(rec.Header().Get("Location"), "/auth/error") || len(e.identities(t, other.User.ID)) != 1 {
		t.Fatalf("link without a session: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	// An identity already linked elsewhere is not moved.
	if _, victimToken := e.victim, e.victimToken(t); victimToken != "" {
		rec = e.callback(t, map[string]any{"sub": "link-sub", "name": "V"}, state, victimToken)
		if !strings.Contains(rec.Header().Get("Location"), "/auth/error") || len(e.identities(t, e.victim.User.ID)) != 0 {
			t.Fatalf("identity moved: %d %s", rec.Code, rec.Header().Get("Location"))
		}
	}
}

func (e *linkTestEnv) victimToken(t *testing.T) string {
	t.Helper()
	_, token, err := e.repo.LoginExternal(t.Context(), "victim@example.com", "Victim", "", true)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func samlAssertion(e *linkTestEnv, attributes samlsp.Attributes) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/auth/saml/complete", nil)
	req = req.WithContext(samlsp.ContextWithSession(req.Context(), samlsp.JWTSessionClaims{Attributes: attributes}))
	rec := httptest.NewRecorder()
	e.s.finishSAML(rec, req)
	return rec
}

func TestSAMLSignInLinksExistingAccountWithoutDomainRestriction(t *testing.T) {
	e := newLinkTestEnv(t)
	rec := samlAssertion(e, samlsp.Attributes{"email": {"victim@example.com"}, "uid": {"victim-uid"}, "name": {"Victim"}})
	identities := e.identities(t, e.victim.User.ID)
	if rec.Code != http.StatusFound || !hasSessionCookie(rec) || len(identities) != 1 || identities[0].Provider != "saml" {
		t.Fatalf("existing email-only SAML user was not linked: %d %s %#v", rec.Code, rec.Body.String(), identities)
	}
	// A new email is provisioned and then signs in again by its identity.
	for i := 0; i < 2; i++ {
		rec = samlAssertion(e, samlsp.Attributes{"email": {"saml.new@example.com"}, "uid": {"new-uid"}, "name": {"New"}})
		if rec.Code != http.StatusFound || !hasSessionCookie(rec) {
			t.Fatalf("SAML sign-in %d: %d %s", i, rec.Code, rec.Body.String())
		}
	}
}

func TestSAMLSignInLinksExistingAccountInAllowedDomain(t *testing.T) {
	e := newLinkTestEnv(t)
	e.s.externalAuth.config.AllowedDomains = []string{"example.com"}
	rec := samlAssertion(e, samlsp.Attributes{"email": {"victim@example.com"}, "uid": {"victim-uid"}, "name": {"Victim"}})
	if rec.Code != http.StatusFound || !hasSessionCookie(rec) {
		t.Fatalf("existing account in an allowed domain was refused: %d %s", rec.Code, rec.Body.String())
	}
	identities := e.identities(t, e.victim.User.ID)
	if len(identities) != 1 || identities[0].Provider != "saml" || identities[0].Subject != "victim-uid" {
		t.Fatalf("SAML identity was not created for the account: %#v", identities)
	}
	// Later sign-ins use the identity, so the account is reached even if the asserted email changes.
	rec = samlAssertion(e, samlsp.Attributes{"email": {"renamed@example.com"}, "uid": {"victim-uid"}, "name": {"Victim"}})
	if rec.Code != http.StatusFound || !hasSessionCookie(rec) || len(e.identities(t, e.victim.User.ID)) != 1 {
		t.Fatalf("identity sign-in changed: %d %s", rec.Code, rec.Body.String())
	}
}

func TestSAMLSignInOutsideAllowedDomainIsRefused(t *testing.T) {
	e := newLinkTestEnv(t)
	e.s.externalAuth.config.AllowedDomains = []string{"corp.example"}
	rec := samlAssertion(e, samlsp.Attributes{"email": {"victim@example.com"}, "uid": {"attacker-uid"}, "name": {"Attacker"}})
	if rec.Code != http.StatusForbidden || hasSessionCookie(rec) || len(e.identities(t, e.victim.User.ID)) != 0 {
		t.Fatalf("outside the allowed domains: %d %s", rec.Code, rec.Body.String())
	}
}
