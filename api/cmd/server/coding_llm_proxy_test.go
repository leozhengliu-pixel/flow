package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flow/api/internal/coding"
	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/llmproxy"
	"flow/api/internal/sandbox"
	"flow/api/internal/store"
	"golang.org/x/crypto/ssh"
)

type fakeSandboxAuthStore struct {
	grant     coding.SandboxGrant
	grantErr  error
	session   domain.CodingSession
	sandboxes []sandbox.Record
}

func (f fakeSandboxAuthStore) AuthenticateSandboxToken(_ context.Context, token string) (coding.SandboxGrant, error) {
	if f.grantErr != nil {
		return coding.SandboxGrant{}, f.grantErr
	}
	if token != "fcs_good" {
		return coding.SandboxGrant{}, coding.ErrTokenInvalid
	}
	return f.grant, nil
}

func (f fakeSandboxAuthStore) CodingSession(_ context.Context, workspace, id string) (domain.CodingSession, error) {
	if id != f.session.ID {
		return domain.CodingSession{}, coding.ErrNotFound
	}
	return f.session, nil
}

func (f fakeSandboxAuthStore) CodingSandboxes(context.Context, string, string) ([]sandbox.Record, error) {
	return f.sandboxes, nil
}

func TestAuthenticateCodingSandboxToken(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	ended := now.Add(-time.Minute)
	base := fakeSandboxAuthStore{
		grant:     coding.SandboxGrant{WorkspaceKey: "ws", SandboxID: "sbx", SessionID: "sess", Kind: sandbox.KindSession, ExpiresAt: now.Add(time.Hour)},
		session:   domain.CodingSession{ID: "sess", CreatorID: "user", Phase: domain.CodingPhaseRunning, Harness: "claudeCode", Model: "claude-sonnet-4-5", ModelSelection: "auto", Source: domain.CodingSourceLoop, SourceID: "loop-7"},
		sandboxes: []sandbox.Record{{ID: "other", Phase: sandbox.PhaseRunning}, {ID: "sbx", Phase: sandbox.PhaseStarting, Desired: sandbox.DesiredRunning}},
	}
	caller, err := authenticateCodingSandboxToken(context.Background(), base, "fcs_good", now)
	if err != nil {
		t.Fatal(err)
	}
	llm := llmCallerFor(caller)
	want := llmproxy.Caller{WorkspaceKey: "ws", SessionID: "sess", SandboxID: "sbx", UserID: "user", LoopID: "loop-7", Harness: "claudeCode", Model: "claude-sonnet-4-5", AutoModel: true}
	if llm != want {
		t.Fatalf("caller %+v, want %+v", llm, want)
	}

	mutate := func(change func(*fakeSandboxAuthStore)) fakeSandboxAuthStore {
		copy := base
		copy.sandboxes = append([]sandbox.Record(nil), base.sandboxes...)
		change(&copy)
		return copy
	}
	transient := errors.New("db down")
	cases := []struct {
		name      string
		store     any
		token     string
		unauth    bool
		transient bool
	}{
		{"no session store", struct{}{}, "fcs_good", false, false},
		{"empty token", base, "", true, false},
		{"unknown token", base, "fcs_bad", true, false},
		{"store outage", mutate(func(f *fakeSandboxAuthStore) { f.grantErr = transient }), "fcs_good", false, true},
		{"warm sandbox", mutate(func(f *fakeSandboxAuthStore) { f.grant.Kind, f.grant.SessionID = sandbox.KindWarm, "" }), "fcs_good", true, false},
		{"snapshot sandbox", mutate(func(f *fakeSandboxAuthStore) { f.grant.Kind = sandbox.KindSnapshot }), "fcs_good", true, false},
		{"expired grant", mutate(func(f *fakeSandboxAuthStore) { f.grant.ExpiresAt = now.Add(-time.Second) }), "fcs_good", true, false},
		{"terminal session", mutate(func(f *fakeSandboxAuthStore) { f.session.Phase = domain.CodingPhaseDone }), "fcs_good", true, false},
		{"ended session", mutate(func(f *fakeSandboxAuthStore) { f.session.EndedAt = &ended }), "fcs_good", true, false},
		{"missing session", mutate(func(f *fakeSandboxAuthStore) { f.session.ID = "other" }), "fcs_good", true, false},
		{"stopped sandbox", mutate(func(f *fakeSandboxAuthStore) { f.sandboxes[1].Desired = sandbox.DesiredStopped }), "fcs_good", true, false},
		{"failed sandbox", mutate(func(f *fakeSandboxAuthStore) { f.sandboxes[1].Phase = sandbox.PhaseFailed }), "fcs_good", true, false},
		{"unknown sandbox", mutate(func(f *fakeSandboxAuthStore) { f.sandboxes = f.sandboxes[:1] }), "fcs_good", true, false},
	}
	for _, tc := range cases {
		_, err := authenticateCodingSandboxToken(context.Background(), tc.store, tc.token, now)
		switch {
		case err == nil:
			t.Errorf("%s: accepted", tc.name)
		case errors.Is(err, llmproxy.ErrUnauthenticated) != tc.unauth:
			t.Errorf("%s: unauthenticated=%v (%v)", tc.name, !tc.unauth, err)
		case tc.transient && !errors.Is(err, transient):
			t.Errorf("%s: %v", tc.name, err)
		}
	}
	pinned := base
	pinned.session.ModelSelection, pinned.session.Source = "claude-opus-4-1", domain.CodingSourceIssue
	caller, _ = authenticateCodingSandboxToken(context.Background(), pinned, "fcs_good", now)
	if llm := llmCallerFor(caller); llm.AutoModel || llm.LoopID != "" {
		t.Fatalf("pinned caller %+v", llm)
	}
}

func codingProxyTestServer(t *testing.T, enabled bool) (*server, http.Handler) {
	t.Helper()
	t.Setenv("FLOW_CODING_SECRET_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32)))
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	s := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true}
	s.initCoding(appconfig.CodingConfig{Provider: appconfig.CodingProviderDisabled})
	s.coding.config.Enabled = enabled
	return s, newHandler(s)
}

func TestCodingSandboxRoutesRejectUnknownTokens(t *testing.T) {
	_, disabled := codingProxyTestServer(t, false)
	_, enabled := codingProxyTestServer(t, true)
	for _, test := range []struct {
		handler http.Handler
		path    string
		want    int
	}{
		{disabled, "/internal/llm-proxy/anthropic/v1/messages", http.StatusNotImplemented},
		{disabled, "/internal/coding-runtime/sign", http.StatusNotImplemented},
		// Coding on with the session store linked in: an unknown token is 401, never 200 or 503.
		{enabled, "/internal/llm-proxy/anthropic/v1/messages", http.StatusUnauthorized},
		{enabled, "/internal/coding-runtime/sign", http.StatusUnauthorized},
	} {
		request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(`{"payload":"eA=="}`))
		request.Header.Set("Authorization", "Bearer fcs_token")
		recorder := httptest.NewRecorder()
		test.handler.ServeHTTP(recorder, request)
		if recorder.Code != test.want {
			t.Errorf("%s: %d, want %d (%s)", test.path, recorder.Code, test.want, recorder.Body.String())
		}
	}
}

func TestCommitSigningKeyStoresMaterialForCodingSessions(t *testing.T) {
	s, handler := codingProxyTestServer(t, true)
	_, private, _ := ed25519.GenerateKey(nil)
	block, err := ssh.MarshalPrivateKey(private, "flow-test")
	if err != nil {
		t.Fatal(err)
	}
	created := requestJSON[commitSigningKeyResponse](t, handler, http.MethodPost, "/api/account/signing-key", map[string]string{"name": "Laptop", "privateKey": string(encodePrivateKeyPEM(block))}, http.StatusCreated)
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	userID := bootstrap.Viewer.ID
	format, material, err := s.store.SigningKeyMaterial(context.Background(), userID)
	if err != nil || format != "ssh" || !strings.Contains(string(material), "OPENSSH PRIVATE KEY") {
		t.Fatalf("material: %q %v", format, err)
	}
	fetched := requestJSON[commitSigningKeyResponse](t, handler, http.MethodGet, "/api/account/signing-key", nil, http.StatusOK)
	if fetched.NeedsReupload || fetched.Fingerprint != created.Fingerprint {
		t.Fatalf("fetched %+v", fetched)
	}
	// "Require signed commits" resolves to the stored key.
	workspace := "" // the test fixture's default workspace
	if err := s.store.MutateWorkspace(context.Background(), workspace, "test", userID, nil, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.CodingAgentSettings.CommitSigningEnabled = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	resolution, err := s.resolveCodingCommitSigning(context.Background(), workspace, userID)
	if err != nil || resolution.Format != "ssh" || !strings.HasPrefix(resolution.PublicKey, "ssh-ed25519 ") {
		t.Fatalf("resolution %+v %v", resolution, err)
	}

	// A key uploaded before material was stored: the account shows it, sessions ask for a re-upload.
	if err := s.store.DeleteSigningKeyMaterial(context.Background(), userID); err != nil {
		t.Fatal(err)
	}
	if fetched := requestJSON[commitSigningKeyResponse](t, handler, http.MethodGet, "/api/account/signing-key", nil, http.StatusOK); !fetched.NeedsReupload {
		t.Fatal("legacy key must be flagged for re-upload")
	}
	_, err = s.resolveCodingCommitSigning(context.Background(), workspace, userID)
	var signingErr *coding.CommitSigningError
	if !errors.As(err, &signingErr) || signingErr.Code != domain.CodingErrMissingCommitSigningKey || signingErr.Message != coding.LegacySigningKeyMessage {
		t.Fatalf("legacy resolution: %v", err)
	}

	// Removing the key removes the material too.
	requestJSON[commitSigningKeyResponse](t, handler, http.MethodPost, "/api/account/signing-key", map[string]string{"name": "Laptop", "privateKey": string(encodePrivateKeyPEM(block))}, http.StatusCreated)
	requestJSON[any](t, handler, http.MethodDelete, "/api/account/signing-key", nil, http.StatusNoContent)
	if has, _ := s.store.HasSigningKeyMaterial(context.Background(), userID); has {
		t.Fatal("material survived key removal")
	}
	_, err = s.resolveCodingCommitSigning(context.Background(), workspace, userID)
	if !errors.As(err, &signingErr) || signingErr.Message != coding.MissingSigningKeyMessage {
		t.Fatalf("missing resolution: %v", err)
	}
}
