package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type oauthPerfFixture struct {
	client              *http.Client
	host                *httptest.Server
	db                  *sql.DB
	registration        domain.OAuthClient
	verifier, challenge string
	omitWorkspaceHeader bool
}

func newOAuthPerfFixture(t testing.TB, directorySize int, poison bool) *oauthPerfFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "oauth-http.db")
	repo, err := store.OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if repo != nil {
			_ = repo.Close()
		}
	})
	service := &server{store: repo, uploadPath: t.TempDir()}
	// This fixture reopens the store and measures only the OAuth HTTP chain.
	// Background schedulers must not retain the old store or affect allocations.
	service.workflowSchedulerStarted.Store(true)
	service.deliverySchedulerStarted.Store(true)
	initialMetadata, ok := repo.WorkspaceMetadata("test-workspace")
	if !ok {
		t.Fatal("fixture workspace is missing")
	}
	initialProjectCount := len(initialMetadata.Projects)
	host := httptest.NewServer(newHandler(service))
	t.Cleanup(host.Close)
	jar, _ := cookiejar.New(nil)
	fixture := &oauthPerfFixture{host: host, client: &http.Client{Jar: jar, Timeout: 20 * time.Second}, verifier: strings.Repeat("v", 48)}
	digest := sha256.Sum256([]byte(fixture.verifier))
	fixture.challenge = base64.RawURLEncoding.EncodeToString(digest[:])
	if _, err = fixture.json(http.MethodPost, "/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK); err != nil {
		t.Fatal(err)
	}
	raw, err := fixture.json(http.MethodPost, "/oauth/register", map[string]any{"client_name": "OAuth bounded HTTP", "redirect_uris": []string{"http://127.0.0.1:43119/callback"}, "token_endpoint_auth_method": "none"}, "", http.StatusCreated)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &fixture.registration); err != nil {
		t.Fatal(err)
	}
	fixture.db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fixture.db.Close() })
	tx, err := fixture.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < directorySize; i++ {
		id := fmt.Sprintf("oauth-unrelated-project-%d", i)
		project, _ := json.Marshal(map[string]any{"id": id, "name": id, "description": strings.Repeat("x", 1024)})
		if poison && i == 0 {
			project = []byte(`{"id":17,"name":"unrelated project must not be decoded"}`)
		}
		if _, err = tx.Exec(`INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES('test-workspace','projects',?,?,?)`, id, i+10000, project); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
	}
	if poison {
		if _, err = tx.Exec(`UPDATE issue_records SET data='{"id":17,"title":"unrelated issue must not be decoded"}'`); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if _, err = tx.Exec(`INSERT INTO workspace_content_records(workspace_key,kind,resource_id,id,created_at,data) VALUES('test-workspace','comment','unrelated','oauth-poison-comment','2026-09-11T00:00:00Z','{"id":17,"body":"unrelated comment must not be decoded"}')`); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if !poison {
		// Populate the live cache as well as SQL: an external INSERT alone would
		// leave WorkspaceMetadata cloning the small pre-insert directory and make
		// the allocation-growth regression falsely pass.
		if err = repo.Close(); err != nil {
			t.Fatal(err)
		}
		repo, err = store.OpenSQLiteTestFixture(path)
		if err != nil {
			t.Fatal(err)
		}
		service.store = repo
		repo.SetRealtimeSink(service.publishRealtime)
		repo.SetWebhookSink(service.dispatchWebhookEvent)
		metadata, found := repo.WorkspaceMetadata("test-workspace")
		if !found || len(metadata.Projects) != initialProjectCount+directorySize {
			t.Fatalf("live project directory not hydrated: got=%d want=%d", len(metadata.Projects), initialProjectCount+directorySize)
		}
	}
	// Any compatibility whole-workspace rewrite fails even if malformed JSON
	// was ignored. OAuth-owned rows and event/credential indexes remain writable.
	for _, table := range []string{"issue_records", "workspace_content_records"} {
		for _, action := range []string{"INSERT", "UPDATE", "DELETE"} {
			query := fmt.Sprintf(`CREATE TRIGGER oauth_guard_%s_%s BEFORE %s ON %s BEGIN SELECT RAISE(ABORT,'OAuth touched unrelated records'); END`, table, action, action, table)
			if _, err = fixture.db.Exec(query); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, action := range []string{"UPDATE", "DELETE"} {
		query := fmt.Sprintf(`CREATE TRIGGER oauth_guard_metadata_%s BEFORE %s ON workspace_metadata_records WHEN OLD.field NOT IN ('oauthAuthorizations','apiKeys','workspaceSettings.applicationPolicies') BEGIN SELECT RAISE(ABORT,'OAuth rewrote unrelated metadata'); END`, action, action)
		if _, err = fixture.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	return fixture
}

func (f *oauthPerfFixture) request(method, path, body, contentType, token string, want int) ([]byte, error) {
	req, err := http.NewRequest(method, f.host.URL+path, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	if !f.omitWorkspaceHeader {
		req.Header.Set("X-Workspace-Key", "test-workspace")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != want {
		return nil, fmt.Errorf("%s %s status %d, want %d: %s", method, path, response.StatusCode, want, raw)
	}
	return raw, nil
}
func (f *oauthPerfFixture) json(method, path string, value any, token string, want int) ([]byte, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return f.request(method, path, string(body), "application/json", token, want)
}
func (f *oauthPerfFixture) form(path string, values url.Values, want int) ([]byte, error) {
	return f.request(http.MethodPost, path, values.Encode(), "application/x-www-form-urlencoded", "", want)
}

type oauthPerfToken struct {
	Access  string `json:"access_token"`
	Refresh string `json:"refresh_token"`
}
type oauthPerfTiming struct{ Authorize, Token, Initialize, Tools time.Duration }

func (f *oauthPerfFixture) authorize() (string, error) {
	raw, err := f.json(http.MethodPost, "/api/oauth/authorization-request", map[string]any{"clientId": f.registration.ClientID, "redirectUri": f.registration.RedirectURIs[0], "responseType": "code", "scope": "read write", "state": "bounded-state", "codeChallenge": f.challenge, "codeChallengeMethod": "S256", "workspaceKey": "test-workspace", "approve": true}, "", http.StatusOK)
	if err != nil {
		return "", err
	}
	var decision struct {
		Redirect string `json:"redirect"`
	}
	if err = json.Unmarshal(raw, &decision); err != nil {
		return "", err
	}
	redirect, err := url.Parse(decision.Redirect)
	if err != nil {
		return "", err
	}
	if redirect.Query().Get("state") != "bounded-state" || redirect.Query().Get("code") == "" {
		return "", fmt.Errorf("invalid OAuth redirect %q", decision.Redirect)
	}
	return redirect.Query().Get("code"), nil
}
func (f *oauthPerfFixture) exchange(code string, want int) (oauthPerfToken, error) {
	raw, err := f.form("/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {f.registration.ClientID}, "redirect_uri": {f.registration.RedirectURIs[0]}, "code_verifier": {f.verifier}}, want)
	var token oauthPerfToken
	if err == nil {
		err = json.Unmarshal(raw, &token)
	}
	return token, err
}
func (f *oauthPerfFixture) rpc(method, token string, want int) error {
	raw, err := f.json(http.MethodPost, "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": map[string]any{"protocolVersion": "2025-11-25", "clientInfo": map[string]string{"name": "bounded-test", "version": "1"}, "capabilities": map[string]any{}}}, token, want)
	if err != nil || want != http.StatusOK {
		return err
	}
	var result mcpRPCResponse
	if err = json.Unmarshal(raw, &result); err != nil {
		return err
	}
	if result.Error != nil {
		return fmt.Errorf("%s protocol error: %+v", method, result.Error)
	}
	if result.Result == nil {
		return fmt.Errorf("%s omitted result", method)
	}
	if method == "tools/list" {
		object, ok := result.Result.(map[string]any)
		if !ok {
			return fmt.Errorf("tools/list returned wrong result type")
		}
		tools, ok := object["tools"].([]any)
		if !ok || len(tools) != 48 {
			return fmt.Errorf("tools/list inventory=%d, expected 48", len(tools))
		}
	}
	return nil
}
func (f *oauthPerfFixture) flow() (oauthPerfToken, oauthPerfTiming, error) {
	var timing oauthPerfTiming
	start := time.Now()
	code, err := f.authorize()
	timing.Authorize = time.Since(start)
	if err != nil {
		return oauthPerfToken{}, timing, err
	}
	start = time.Now()
	token, err := f.exchange(code, 200)
	timing.Token = time.Since(start)
	if err != nil {
		return token, timing, err
	}
	if token.Access == "" || token.Refresh == "" {
		return token, timing, fmt.Errorf("token exchange omitted credentials")
	}
	if _, err = f.exchange(code, 400); err != nil {
		return token, timing, err
	}
	start = time.Now()
	err = f.rpc("initialize", token.Access, 200)
	timing.Initialize = time.Since(start)
	if err != nil {
		return token, timing, err
	}
	start = time.Now()
	err = f.rpc("tools/list", token.Access, 200)
	timing.Tools = time.Since(start)
	return token, timing, err
}

func TestOAuthAuthorizationHTTPDoesNotHydrateOrRewriteUnrelatedRecords(t *testing.T) {
	for _, omitHeader := range []bool{false, true} {
		t.Run(fmt.Sprintf("omit_workspace_header_%t", omitHeader), func(t *testing.T) {
			f := newOAuthPerfFixture(t, 1000, true)
			f.omitWorkspaceHeader = omitHeader
			// OAuth consent has no use for issue totals. The compatibility account
			// reader would fail scanning this value into int; the exact reader must
			// leave both it and the unrelated metadata directory untouched.
			if _, err := f.db.Exec(`UPDATE issue_collection_counts SET total='not-an-issue-count' WHERE workspace_key='test-workspace'`); err != nil {
				t.Fatal(err)
			}
			query := url.Values{"response_type": {"code"}, "client_id": {f.registration.ClientID}, "redirect_uri": {f.registration.RedirectURIs[0]}, "scope": {"read write"}, "code_challenge": {f.challenge}, "code_challenge_method": {"S256"}}
			if _, err := f.json(http.MethodGet, "/api/oauth/authorization-request?"+query.Encode(), nil, "", 200); err != nil {
				t.Fatal(err)
			}
			token, timing, err := f.flow()
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("local SQLite, 1000 unrelated projects: authorize=%s token=%s initialize=%s tools/list=%s", timing.Authorize, timing.Token, timing.Initialize, timing.Tools)
			if _, err = f.form("/oauth/revoke", url.Values{"token": {token.Access}, "client_id": {f.registration.ClientID}}, 200); err != nil {
				t.Fatal(err)
			}
			if err = f.rpc("initialize", token.Access, 401); err != nil {
				t.Fatal(err)
			}
			if _, err = f.form("/oauth/revoke", url.Values{"token": {token.Access}, "client_id": {f.registration.ClientID}}, 200); err != nil {
				t.Fatal(err)
			}
			var raw []byte
			if err = f.db.QueryRow(`SELECT data FROM workspace_metadata_records WHERE field='projects' AND record_key='oauth-unrelated-project-0'`).Scan(&raw); err != nil || !bytes.Equal(raw, []byte(`{"id":17,"name":"unrelated project must not be decoded"}`)) {
				t.Fatalf("unrelated projection changed: %s %v", raw, err)
			}
		})
	}
}

func TestOAuthAuthorizationHTTPPreservesWorkspaceAndSessionPolicies(t *testing.T) {
	for _, scenario := range []string{"wrong-body-workspace", "expired-workspace-session", "required-mfa"} {
		t.Run(scenario, func(t *testing.T) {
			f := newOAuthPerfFixture(t, 50, true)
			body := map[string]any{"clientId": f.registration.ClientID, "redirectUri": f.registration.RedirectURIs[0], "responseType": "code", "scope": "read write", "codeChallenge": f.challenge, "codeChallengeMethod": "S256", "workspaceKey": "test-workspace", "approve": true}
			want := http.StatusForbidden
			switch scenario {
			case "wrong-body-workspace":
				body["workspaceKey"] = "outside-workspace"
			case "expired-workspace-session":
				want = http.StatusUnauthorized
				if _, err := f.db.Exec(`UPDATE auth_sessions SET created_at=?`, time.Now().UTC().Add(-40*24*time.Hour).Format(time.RFC3339Nano)); err != nil {
					t.Fatal(err)
				}
			case "required-mfa":
				for _, action := range []string{"UPDATE", "DELETE"} {
					if _, err := f.db.Exec("DROP TRIGGER oauth_guard_metadata_" + action); err != nil {
						t.Fatal(err)
					}
				}
				for _, field := range []string{"requireTwoFactor", "disableAdminBypass"} {
					if _, err := f.db.Exec(`INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES('test-workspace','workspaceSettings',?,0,'true') ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET data='true'`, field); err != nil {
						t.Fatal(err)
					}
				}
			}
			raw, err := f.json(http.MethodPost, "/api/oauth/authorization-request", body, "", want)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "required-mfa" && !bytes.Contains(raw, []byte(`"mfa_required"`)) {
				t.Fatalf("MFA policy did not survive exact OAuth access: %s", raw)
			}
			var count int
			if err = f.db.QueryRow(`SELECT COUNT(*) FROM oauth_authorization_codes`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("denied consent issued an authorization code: %d %v", count, err)
			}
		})
	}
}

func TestOAuthAuthorizationHTTP20ConcurrentFlows(t *testing.T) {
	f := newOAuthPerfFixture(t, 1000, true)
	type result struct {
		token  oauthPerfToken
		timing oauthPerfTiming
		err    error
	}
	results := make(chan result, 20)
	var group sync.WaitGroup
	for i := 0; i < 20; i++ {
		group.Add(1)
		go func() { defer group.Done(); token, timing, err := f.flow(); results <- result{token, timing, err} }()
	}
	group.Wait()
	close(results)
	var authorize, exchange, initialize, tools []time.Duration
	var tokens []oauthPerfToken
	for result := range results {
		if result.err != nil {
			t.Error(result.err)
			continue
		}
		tokens = append(tokens, result.token)
		authorize = append(authorize, result.timing.Authorize)
		exchange = append(exchange, result.timing.Token)
		initialize = append(initialize, result.timing.Initialize)
		tools = append(tools, result.timing.Tools)
	}
	if t.Failed() {
		return
	}
	for phase, values := range map[string][]time.Duration{"authorize": authorize, "token": exchange, "initialize": initialize, "tools/list": tools} {
		slices.Sort(values)
		t.Logf("20 concurrent local HTTP %s p95=%s max=%s", phase, values[18], values[19])
	}
	var authorizationID string
	if err := f.db.QueryRow(`SELECT record_key FROM workspace_metadata_records WHERE workspace_key='test-workspace' AND field='oauthAuthorizations' ORDER BY collection_order LIMIT 1`).Scan(&authorizationID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.json(http.MethodDelete, "/api/oauth/authorizations/"+authorizationID, nil, "", 204); err != nil {
		t.Fatal(err)
	}
	for _, token := range tokens {
		if err := f.rpc("initialize", token.Access, 401); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOAuthAuthorizationHTTPConcurrentWorkspaceTraffic(t *testing.T) {
	f := newOAuthPerfFixture(t, 1000, false)
	// The explicit control issue write is allowed to append its activity and
	// notifications. The isolated OAuth test keeps these tables fully protected.
	for _, action := range []string{"INSERT", "UPDATE", "DELETE"} {
		if _, err := f.db.Exec("DROP TRIGGER oauth_guard_workspace_content_records_" + action); err != nil {
			t.Fatal(err)
		}
	}
	var issueID string
	if err := f.db.QueryRow(`SELECT id FROM issue_records WHERE workspace_key='test-workspace' ORDER BY id LIMIT 1`).Scan(&issueID); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"INSERT", "UPDATE", "DELETE"} {
		if _, err := f.db.Exec("DROP TRIGGER oauth_guard_issue_records_" + action); err != nil {
			t.Fatal(err)
		}
		alias := "NEW"
		if action == "DELETE" {
			alias = "OLD"
		}
		query := fmt.Sprintf(`CREATE TRIGGER oauth_guard_issue_records_%s BEFORE %s ON issue_records WHEN %s.id <> '%s' BEGIN SELECT RAISE(ABORT,'OAuth traffic rewrote unrelated issue'); END`, action, action, alias, strings.ReplaceAll(issueID, "'", "''"))
		if _, err := f.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	errors := make(chan error, 23)
	var group sync.WaitGroup
	for i := 0; i < 20; i++ {
		group.Add(1)
		go func() { defer group.Done(); <-start; _, _, err := f.flow(); errors <- err }()
	}
	traffic := []struct {
		method, path string
		input        any
	}{
		{http.MethodGet, "/api/issue-records/bootstrap", nil},
		{http.MethodPost, "/api/realtime/presence?issues=paged", map[string]string{"clientId": "oauth-competing-presence"}},
		{http.MethodPatch, "/api/issue-records/" + issueID, map[string]string{"title": "Updated during OAuth traffic"}},
	}
	for _, job := range traffic {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			began := time.Now()
			_, err := f.json(job.method, job.path, job.input, "", 200)
			t.Logf("20 OAuth flows plus %s elapsed=%s", job.path, time.Since(began))
			errors <- err
		}()
	}
	close(start)
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Error(err)
		}
	}
}

func BenchmarkOAuthAuthorizationHTTPMetadataBounded(b *testing.B) {
	for _, size := range []int{0, 2000} {
		b.Run(fmt.Sprintf("projects_%d", size), func(b *testing.B) {
			f := newOAuthPerfFixture(b, size, false)
			// Exclude server/fixture setup, password hashing and connection warmup.
			if _, _, err := f.flow(); err != nil {
				b.Fatal(err)
			}
			runtime.GC()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, _, err := f.flow(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestOAuthAuthorizationHTTPAllocationsDoNotScaleWithProjectDirectory(t *testing.T) {
	measure := func(size int) uint64 {
		f := newOAuthPerfFixture(t, size, false)
		if _, _, err := f.flow(); err != nil {
			t.Fatal(err)
		}
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		for i := 0; i < 5; i++ {
			if _, _, err := f.flow(); err != nil {
				t.Fatal(err)
			}
		}
		runtime.ReadMemStats(&after)
		return (after.TotalAlloc - before.TotalAlloc) / 5
	}
	small, large := measure(0), measure(2000)
	t.Logf("complete OAuth HTTP chain allocation: existing directory=%d bytes/op, +2000 projects=%d bytes/op", small, large)
	if large > small+2<<20 || large > 8<<20 {
		t.Fatalf("OAuth allocations scale with unrelated project directory: baseline=%d large=%d", small, large)
	}
}

func BenchmarkOAuthAuthorizationHTTPStages(b *testing.B) {
	for _, size := range []int{0, 2000} {
		b.Run(fmt.Sprintf("projects_%d", size), func(b *testing.B) {
			f := newOAuthPerfFixture(b, size, false)
			token, _, err := f.flow()
			if err != nil {
				b.Fatal(err)
			}
			for _, stage := range []string{"authorize", "token", "initialize", "tools/list"} {
				b.Run(stage, func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						switch stage {
						case "authorize":
							_, err = f.authorize()
						case "token":
							b.StopTimer()
							var code string
							code, err = f.authorize()
							b.StartTimer()
							if err == nil {
								_, err = f.exchange(code, 200)
							}
						default:
							err = f.rpc(stage, token.Access, 200)
						}
						if err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		})
	}
}
