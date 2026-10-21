package gitlab

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/codehost"
)

// Two Flow replicas minting the same session's project token against one
// GitLab (the fake revokes the old value on rotation, as GitLab does, and
// checks git smart-HTTP Basic auth against the active tokens).

// memSecrets is the shared connector secret store of the replicas.
type memSecrets struct {
	mu       sync.Mutex
	data     map[string][]byte
	failRead error
}

func newMemSecrets() *memSecrets { return &memSecrets{data: map[string][]byte{}} }

func (m *memSecrets) PutConnectorSecret(_ context.Context, id string, data []byte, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[id] = append([]byte(nil), data...)
	return nil
}

func (m *memSecrets) ReadConnectorSecret(_ context.Context, id string, _ bool) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failRead != nil {
		return nil, m.failRead
	}
	raw, ok := m.data[id]
	if !ok {
		return nil, sql.ErrNoRows
	}
	return raw, nil
}

func (m *memSecrets) DeleteConnectorSecret(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, id)
	return nil
}

func (m *memSecrets) len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.data)
}

// sharedLock stands in for the Redis lock all replicas share.
func sharedLock() TokenLocker {
	var mu sync.Mutex
	return func(_ context.Context, _ string, action func() error) error {
		mu.Lock()
		defer mu.Unlock()
		return action()
	}
}

// replicas builds two hosts on one fake GitLab; shared wires the shared store.
func replicas(t *testing.T, fake *fakeGitLab, shared bool) (*Host, *Host, *memSecrets) {
	t.Helper()
	a, _ := newTestHost(t, fake, ModeProjectToken, fixedNow)
	b, _ := newTestHost(t, fake, ModeProjectToken, fixedNow)
	secrets := newMemSecrets()
	if shared {
		lock := sharedLock()
		a.SetSharedTokens(secrets, lock)
		b.SetSharedTokens(secrets, lock)
	}
	return a, b, secrets
}

func mustMint(t *testing.T, host *Host, request codehost.CredentialRequest) codehost.Credential {
	t.Helper()
	credential, err := host.MintCredential(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	return credential
}

func activeSessionTokens(fake *fakeGitLab, name string) int {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	n := 0
	for _, token := range fake.projects["acme/platform/api"].Tokens {
		if token.Name == name && token.Active {
			n++
		}
	}
	return n
}

const tokensPath = "POST /projects/acme%2Fplatform%2Fapi/access_tokens"

// The defect: per-process copies. Replica B rotates the token replica A
// handed out; A keeps answering with the revoked value.
func TestPerProcessTokensGoStaleAcrossReplicas(t *testing.T) {
	fake := newFakeGitLab(t)
	a, b, _ := replicas(t, fake, false)
	first := mustMint(t, a, mintRequest(fake, "s1"))
	second := mustMint(t, b, mintRequest(fake, "s1"))
	stale := mustMint(t, a, mintRequest(fake, "s1"))
	if fingerprint(stale.Password) != fingerprint(first.Password) || fingerprint(second.Password) == fingerprint(first.Password) {
		t.Fatalf("A=%s B=%s A again=%s", fingerprint(first.Password), fingerprint(second.Password), fingerprint(stale.Password))
	}
	if got := fake.gitStatus(t, stale); got != http.StatusUnauthorized {
		t.Fatalf("the per-process copy is expected to be revoked by B's rotation, git answered %d", got)
	}
	// Bounded recovery without a shared store: git's erase drops A's dead
	// copy once (GitLab confirms it is inactive); the retry mints afresh.
	dropped, err := a.InvalidateCredential(context.Background(), mintRequest(fake, "s1"), codehost.CredentialFingerprint(stale.Password))
	if err != nil || !dropped {
		t.Fatalf("invalidate: %v %v", dropped, err)
	}
	if retried := mustMint(t, a, mintRequest(fake, "s1")); fake.gitStatus(t, retried) != http.StatusOK {
		t.Fatal("the retry after the erase must get a working token")
	}
}

func TestReplicasShareOneProjectToken(t *testing.T) {
	fake := newFakeGitLab(t)
	a, b, _ := replicas(t, fake, true)
	var want string
	for i := range 8 {
		host := a
		if i%2 == 1 {
			host = b
		}
		credential := mustMint(t, host, mintRequest(fake, "s1"))
		if want == "" {
			want = fingerprint(credential.Password)
		}
		if got := fingerprint(credential.Password); got != want {
			t.Fatalf("request %d: replica served %s, want %s", i, got, want)
		}
		if status := fake.gitStatus(t, credential); status != http.StatusOK {
			t.Fatalf("request %d: git %d", i, status)
		}
	}
	if created, rotated := fake.count(tokensPath), fake.count(tokensPath+"/"); created != 1 || rotated != 0 {
		t.Fatalf("created=%d rotated=%d, want one creation and no rotation", created-rotated, rotated)
	}

	// Concurrent first requests on both replicas mint once.
	fake2 := newFakeGitLab(t)
	a2, b2, _ := replicas(t, fake2, true)
	var wg sync.WaitGroup
	seen := sync.Map{}
	for i := range 16 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			host := a2
			if i%2 == 1 {
				host = b2
			}
			credential, err := host.MintCredential(context.Background(), mintRequest(fake2, "s2"))
			if err != nil {
				t.Error(err)
				return
			}
			seen.Store(fingerprint(credential.Password), true)
		}(i)
	}
	wg.Wait()
	distinct := 0
	seen.Range(func(any, any) bool { distinct++; return true })
	if distinct != 1 || fake2.count(tokensPath) != 1 || activeSessionTokens(fake2, "flow-cs-s2") != 1 {
		t.Fatalf("distinct=%d requests=%d active=%d", distinct, fake2.count(tokensPath), activeSessionTokens(fake2, "flow-cs-s2"))
	}
}

// Stop on one replica (revocation at session end, or the sweep of ended
// sessions), continue in a new sandbox whose requests reach either replica:
// for an unpushed branch, a pushed branch and a branch with a draft MR.
func TestReplicasStopAndResume(t *testing.T) {
	type stopper func(t *testing.T, a, b *Host, fake *fakeGitLab)
	stops := map[string]stopper{
		"stop on the minting replica": func(t *testing.T, a, _ *Host, _ *fakeGitLab) {
			if err := a.RevokeSessionCredentials(context.Background(), "ws", "s1"); err != nil {
				t.Fatal(err)
			}
		},
		"stop on the other replica": func(t *testing.T, _, b *Host, _ *fakeGitLab) {
			if err := b.RevokeSessionCredentials(context.Background(), "ws", "s1"); err != nil {
				t.Fatal(err)
			}
		},
		"sweep on the other replica": func(t *testing.T, _, b *Host, fake *fakeGitLab) {
			ended := func(name string, _ time.Time) bool { return name != "flow-cs-s1" }
			if revoked, err := b.SweepProjectTokens(context.Background(), "ws", []codehost.RepoRef{testRepo(fake)}, ended); err != nil || revoked != 1 {
				t.Fatalf("sweep: %d %v", revoked, err)
			}
		},
	}
	branches := []string{"unpushed", "pushed", "draft MR"}
	for stopName, stop := range stops {
		for _, branch := range branches {
			t.Run(stopName+"/"+branch, func(t *testing.T) {
				fake := newFakeGitLab(t)
				a, b, secrets := replicas(t, fake, true)
				ctx := context.Background()
				repo := testRepo(fake)
				input := codehost.CreateChangeRequestInput{Repo: repo, Head: "flow/s1", Base: "main", Title: "Draft: Fix", Draft: true}
				// First run: clone, push, draft MR.
				run1 := mustMint(t, a, mintRequest(fake, "s1"))
				if fake.gitStatus(t, run1) != http.StatusOK {
					t.Fatal("first run clone")
				}
				var mr codehost.ChangeRequest
				if branch != "unpushed" {
					fake.mu.Lock()
					fake.projects["acme/platform/api"].Branches["flow/s1"] = strings.Repeat("b", 40)
					fake.mu.Unlock()
				}
				if branch == "draft MR" {
					created, err := a.CreateChangeRequest(ctx, "ws", input)
					if err != nil {
						t.Fatal(err)
					}
					mr = created
				}
				stop(t, a, b, fake)
				if fake.gitStatus(t, run1) != http.StatusUnauthorized {
					t.Fatal("the stopped session's token must be revoked")
				}
				// Continue: the new sandbox's requests alternate between replicas.
				var resumed []string
				for i, host := range []*Host{a, b, a, b} {
					credential := mustMint(t, host, mintRequest(fake, "s1"))
					if status := fake.gitStatus(t, credential); status != http.StatusOK {
						t.Fatalf("resume request %d: git %d with fp=%s (old fp=%s)", i, status, fingerprint(credential.Password), fingerprint(run1.Password))
					}
					resumed = append(resumed, fingerprint(credential.Password))
				}
				for _, fp := range resumed {
					if fp != resumed[0] || fp == fingerprint(run1.Password) {
						t.Fatalf("resumed fingerprints %v (old %s)", resumed, fingerprint(run1.Password))
					}
				}
				if activeSessionTokens(fake, "flow-cs-s1") != 1 || secrets.len() != 1 {
					t.Fatalf("active=%d shared records=%d", activeSessionTokens(fake, "flow-cs-s1"), secrets.len())
				}
				// Development continues on the original branch and MR.
				fake.mu.Lock()
				fake.projects["acme/platform/api"].Branches["flow/s1"] = strings.Repeat("c", 40)
				fake.mu.Unlock()
				again, err := b.CreateChangeRequest(ctx, "ws", input)
				if err != nil {
					t.Fatal(err)
				}
				if branch == "draft MR" && (again.Number != mr.Number || len(fake.project("acme/platform/api").MRs) != 1) {
					t.Fatalf("the original MR !%d must be reused, got !%d (%d MRs)", mr.Number, again.Number, len(fake.project("acme/platform/api").MRs))
				}
				// The session ends for good: nothing stays active or stored.
				if err := b.RevokeSessionCredentials(ctx, "ws", "s1"); err != nil {
					t.Fatal(err)
				}
				if activeSessionTokens(fake, "flow-cs-s1") != 0 || secrets.len() != 0 {
					t.Fatalf("after the end: active=%d shared records=%d", activeSessionTokens(fake, "flow-cs-s1"), secrets.len())
				}
			})
		}
	}
}

// The two diagnostics of the defect, serially (no concurrency, so a mint
// lock alone would not help): A→B→A, and revoke on B then request on A.
func TestSerialReplicaRequestsNeverServeARevokedToken(t *testing.T) {
	fake := newFakeGitLab(t)
	a, b, _ := replicas(t, fake, true)
	ctx := context.Background()
	t1 := mustMint(t, a, mintRequest(fake, "s1"))
	t2 := mustMint(t, b, mintRequest(fake, "s1"))
	t3 := mustMint(t, a, mintRequest(fake, "s1"))
	if t1.Password != t2.Password || t2.Password != t3.Password || fake.gitStatus(t, t3) != http.StatusOK {
		t.Fatalf("A→B→A: %s %s %s", fingerprint(t1.Password), fingerprint(t2.Password), fingerprint(t3.Password))
	}
	if err := b.RevokeSessionCredentials(ctx, "ws", "s1"); err != nil {
		t.Fatal(err)
	}
	if fake.gitStatus(t, t1) != http.StatusUnauthorized {
		t.Fatal("B's revocation must revoke the token A minted")
	}
	after := mustMint(t, a, mintRequest(fake, "s1"))
	if after.Password == t1.Password || fake.gitStatus(t, after) != http.StatusOK {
		t.Fatalf("after B revoked, A served fp=%s (revoked fp=%s)", fingerprint(after.Password), fingerprint(t1.Password))
	}
	// A cache hit is a read: no GitLab call and no store write.
	before := len(fake.requests)
	mustMint(t, b, mintRequest(fake, "s1"))
	if len(fake.requests) != before {
		t.Fatalf("a hit called GitLab: %v", fake.requests[before:])
	}
}

func TestRejectedSharedTokenIsDroppedOnlyWhenDead(t *testing.T) {
	fake := newFakeGitLab(t)
	a, b, _ := replicas(t, fake, true)
	ctx := context.Background()
	current := mustMint(t, a, mintRequest(fake, "s1"))
	// A rejection of a token that is not the current one, or of a token
	// GitLab still reports active (a 403 on access), changes nothing.
	if dropped, err := b.InvalidateCredential(ctx, mintRequest(fake, "s1"), codehost.CredentialFingerprint("glpat-something-else")); err != nil || dropped {
		t.Fatalf("foreign fingerprint: %v %v", dropped, err)
	}
	if dropped, err := b.InvalidateCredential(ctx, mintRequest(fake, "s1"), codehost.CredentialFingerprint(current.Password)); err != nil || dropped {
		t.Fatalf("active token: %v %v", dropped, err)
	}
	if again := mustMint(t, b, mintRequest(fake, "s1")); again.Password != current.Password || fake.count(tokensPath+"/") != 0 {
		t.Fatal("an active token is kept, never rotated")
	}
	// Revoked outside Flow: git fails once, the erase drops it, the retry works.
	fake.mu.Lock()
	for _, token := range fake.projects["acme/platform/api"].Tokens {
		token.Active, token.Revoked = false, true
	}
	fake.mu.Unlock()
	if fake.gitStatus(t, mustMint(t, a, mintRequest(fake, "s1"))) != http.StatusUnauthorized {
		t.Fatal("expected the externally revoked token to fail")
	}
	if dropped, err := b.InvalidateCredential(ctx, mintRequest(fake, "s1"), codehost.CredentialFingerprint(current.Password)); err != nil || !dropped {
		t.Fatalf("dead token: %v %v", dropped, err)
	}
	fresh := mustMint(t, a, mintRequest(fake, "s1"))
	if fresh.Password == current.Password || fake.gitStatus(t, fresh) != http.StatusOK || fake.gitStatus(t, mustMint(t, b, mintRequest(fake, "s1"))) != http.StatusOK {
		t.Fatal("the retry must get one fresh token on both replicas")
	}
}

func TestSharedStoreReadFailureNeverRotates(t *testing.T) {
	fake := newFakeGitLab(t)
	a, b, secrets := replicas(t, fake, true)
	first := mustMint(t, a, mintRequest(fake, "s1"))
	secrets.mu.Lock()
	secrets.failRead = errors.New("database is locked")
	secrets.mu.Unlock()
	_, err := b.MintCredential(context.Background(), mintRequest(fake, "s1"))
	if !errors.Is(err, codehost.ErrTransient) {
		t.Fatalf("err = %v, want transient", err)
	}
	if fake.count(tokensPath+"/") != 0 || fake.gitStatus(t, first) != http.StatusOK {
		t.Fatal("a failed shared read must not rotate the token other replicas serve")
	}
}

func TestRevokeIDCarriesTheTokenName(t *testing.T) {
	connection, project, id, name, ok := decodeRevokeID(encodeRevokeID("c1", "acme%2Fapp", 9, "flow-cs-s1"))
	if !ok || connection != "c1" || project != "acme%2Fapp" || id != 9 || name != "flow-cs-s1" {
		t.Fatalf("decoded %s %s %d %s %v", connection, project, id, name, ok)
	}
	if _, _, id, name, ok := decodeRevokeID("gitlab-pat|c1|acme%2Fapp|9"); !ok || id != 9 || name != "" {
		t.Fatal("the four-part form still decodes")
	}
}
