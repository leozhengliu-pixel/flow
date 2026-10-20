package main

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// linkGitLabOAuthAccount links the viewer's GitLab account through the OAuth
// flow with a token that already expires inside the refresh margin.
func (f *memberAccountsFixture) linkGitLabOAuthAccount() domain.CodeHostAccount {
	f.t.Helper()
	if rec := f.do(http.MethodPut, "/api/integrations/gitlab/"+f.conn+"/oauth-application?workspace=test-workspace", map[string]string{"applicationId": "app-1", "secret": "app-secret"}); rec.Code != http.StatusOK {
		f.t.Fatalf("save oauth application: %d %s", rec.Code, rec.Body.String())
	}
	start := requestJSON[map[string]string](f.t, f.handler, http.MethodPost, "/api/account/code-host-accounts/gitlab/authorize?workspace=test-workspace", map[string]string{"instanceId": f.conn}, http.StatusOK)
	authorize, err := url.Parse(start["url"])
	if err != nil {
		f.t.Fatal(err)
	}
	f.gitlab.challenge = authorize.Query().Get("code_challenge")
	f.gitlab.expiresIn = 60
	if rec := f.do(http.MethodGet, codeHostLinkCallbackPath+"?code=good&state="+url.QueryEscape(authorize.Query().Get("state")), nil); rec.Code != http.StatusOK {
		f.t.Fatalf("callback: %d %s", rec.Code, rec.Body.String())
	}
	accounts := f.accounts().Accounts
	if len(accounts) != 1 {
		f.t.Fatalf("accounts %+v", accounts)
	}
	account, err := f.repo.CodeHostAccount(f.t.Context(), f.viewer.ID, accounts[0].ID)
	if err != nil {
		f.t.Fatal(err)
	}
	return account
}

// Several API replicas (separate database handles on one database, no shared
// memory) refresh the same expiring GitLab token at once. GitLab rotates the
// refresh token and rejects a reused one, so only one replica may refresh:
// the others must wait and return the token it stored, and the account must
// stay connected.
func TestMemberTokenRefreshIsSerializedAcrossReplicas(t *testing.T) {
	f := newMemberAccountsFixture(t)
	account := f.linkGitLabOAuthAccount()
	replica, err := store.OpenSQLiteTestFixture(f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { replica.Close() })
	replicas := []*server{f.server, {store: replica, uploadPath: t.TempDir(), authDisabled: true}}

	f.gitlab.expiresIn = 7200
	f.gitlab.refreshDelay = 300 * time.Millisecond
	const callers = 8
	var wg sync.WaitGroup
	results := make([]codeHostAccountTokens, callers)
	errs := make([]error, callers)
	start := make(chan struct{})
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i], errs[i] = replicas[i%len(replicas)].memberHostTokens(t.Context(), account)
		}()
	}
	close(start)
	wg.Wait()
	for i := range callers {
		if errs[i] != nil || results[i].AccessToken != "gloa_access_2" || results[i].RefreshToken != "glr_2" {
			t.Fatalf("caller %d: %+v %v", i, results[i], errs[i])
		}
	}
	f.gitlab.mu.Lock()
	refreshes := f.gitlab.refreshes
	f.gitlab.mu.Unlock()
	if refreshes != 1 {
		t.Fatalf("refresh grants sent = %d, want exactly 1", refreshes)
	}
	current, err := replica.CodeHostAccount(t.Context(), account.UserID, account.ID)
	if err != nil || current.Status != domain.CodeHostAccountActive {
		t.Fatalf("account after concurrent refresh %+v %v", current, err)
	}
}

// A replica that crashed while holding the lease does not block refreshes
// forever: the lease expires and the next caller refreshes.
func TestMemberTokenRefreshTakesOverAnExpiredLease(t *testing.T) {
	f := newMemberAccountsFixture(t)
	account := f.linkGitLabOAuthAccount()
	f.gitlab.expiresIn = 7200
	if ok, err := f.repo.ClaimCodeHostAccountRefresh(t.Context(), account.ID, "crashed-replica", 300*time.Millisecond); !ok || err != nil {
		t.Fatalf("claim %v %v", ok, err)
	}
	began := time.Now()
	tokens, err := f.server.memberHostTokens(t.Context(), account)
	if err != nil || tokens.AccessToken != "gloa_access_2" {
		t.Fatalf("refresh after lease expiry %+v %v", tokens, err)
	}
	if time.Since(began) < 250*time.Millisecond {
		t.Fatal("the refresh must wait for the other replica's lease")
	}
	if !strings.HasPrefix(tokens.RefreshToken, "glr_") {
		t.Fatalf("tokens %+v", tokens)
	}
}
